package beads

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// Which processes hold the tracker's store, for whoever has to say so.
//
// bd opens its embedded Dolt store under an exclusive lock, every bd process
// takes it, and a call that cannot get it waits until its own limit stops it.
// A call stopped that way used to say only that it was stopped, and when the
// store was unavailable for over a minute and a half nothing recorded what had
// been holding it (docs/diagnoses/yoyodyne-6u7-tracker-timeouts.md). So a call
// stopped at a limit names the processes that had the store open when it was
// stopped, and since when each had been running; and the store's cleanup looks
// here first, so as not to start while another process is using the store.
//
// The operating system is asked rather than any record the harness keeps,
// because the holder is as often a person's own `bd` at a terminal as one of
// the harness's: `lsof` names every process with a file under the store open,
// and `ps` says when each started and what it was asked.

// StoreHolder is one process that had a file under the tracker's store open.
type StoreHolder struct {
	PID int `json:"pid"`
	// Command is what the process was started as, bounded, and empty where it
	// could not be read.
	Command string `json:"command,omitempty"`
	// Since is when the process started, and zero where it could not be read;
	// SinceUnread is then why not.
	Since       time.Time `json:"since,omitzero"`
	SinceUnread string    `json:"since_unread,omitempty"`
}

// Says is the holder in words: its process number, what it was asked, and since
// when it has been running.
func (h StoreHolder) Says() string {
	said := fmt.Sprintf("process %d", h.PID)
	if h.PID == os.Getpid() {
		said += " (this one)"
	}
	if h.Command != "" {
		said += " (" + h.Command + ")"
	}
	switch {
	case !h.Since.IsZero():
		said += ", running since " + h.Since.Local().Format("2006-01-02 15:04:05 MST")
	case h.SinceUnread != "":
		said += ", since when could not be read: " + h.SinceUnread
	}
	return said
}

// SayHolders is a list of holders as one clause, naming at most
// maxHoldersSaid of them and counting the rest.
func SayHolders(holders []StoreHolder) string {
	said := make([]string, 0, min(len(holders), maxHoldersSaid))
	for index, holder := range holders {
		if index == maxHoldersSaid {
			said = append(said, fmt.Sprintf("and %d more", len(holders)-maxHoldersSaid))
			break
		}
		said = append(said, holder.Says())
	}
	return strings.Join(said, "; ")
}

// HolderFinder names the processes holding files under a path open: every file
// under it where the path is a directory, and the file itself otherwise. An
// error is the finder unable to tell, said in words a reader acts on.
type HolderFinder interface {
	Holders(ctx context.Context, path string) ([]StoreHolder, error)
}

const (
	// holderLookupTimeout bounds each question put to lsof and ps, which answer
	// at once on a store of a few dozen files.
	holderLookupTimeout = 5 * time.Second
	// maxHoldersSaid is how many holders one account names. A store open in more
	// processes than this is a queue, and the first few say what it is a queue of.
	maxHoldersSaid = 5
	// maxHolderCommandBytes bounds what of a holder's command line is kept: enough
	// for `bd export -o .beads/issues.jsonl`, not a page of arguments.
	maxHolderCommandBytes = 160
)

// OSHolderFinder asks lsof which processes have the path open, and ps when each
// started and what it was started as.
type OSHolderFinder struct {
	Runner execution.ProcessRunner
}

func (f OSHolderFinder) Holders(ctx context.Context, path string) ([]StoreHolder, error) {
	if f.Runner == nil {
		return nil, errors.New("no process runner is wired to ask lsof")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("%s could not be looked at: %w", path, err)
	}
	// -F pc is lsof's machine form, a `p<pid>` line per process followed by a
	// `c<name>` line; -n and -P keep it from looking up names it would only
	// print, and -w keeps its warnings about other file systems out of what is
	// read as its answer.
	args := []string{"-w", "-n", "-P", "-F", "pc"}
	if info.IsDir() {
		args = append(args, "+D", path)
	} else {
		args = append(args, "--", path)
	}
	result, err := f.Runner.Run(ctx, execution.Command{Name: "lsof", Args: args, Timeout: holderLookupTimeout}, nil)
	if err != nil {
		return nil, fmt.Errorf("lsof could not be run: %w", err)
	}
	holders, err := lsofProcesses(result)
	if err != nil || len(holders) == 0 {
		return nil, err
	}
	return f.describe(ctx, holders), nil
}

// lsofProcesses reads the process numbers out of lsof's answer. lsof exits 1
// whenever any file it was asked about is not open, which under +D is nearly
// always, and also when it could not look at all; so what it wrote is read
// whatever it exited with, and only an answer naming no process that also
// says something is a failure to tell.
func lsofProcesses(result execution.ProcessResult) ([]StoreHolder, error) {
	named := map[int]string{}
	var pids []int
	current := 0
	for _, line := range strings.Split(result.Stdout, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "p"):
			pid, err := strconv.Atoi(line[1:])
			if err != nil || pid <= 0 {
				current = 0
				continue
			}
			if _, seen := named[pid]; !seen {
				named[pid] = ""
				pids = append(pids, pid)
			}
			current = pid
		case strings.HasPrefix(line, "c") && current > 0:
			named[current] = boundedCommand(line[1:])
		}
	}
	sort.Ints(pids)
	holders := make([]StoreHolder, 0, len(pids))
	for _, pid := range pids {
		holders = append(holders, StoreHolder{PID: pid, Command: named[pid]})
	}
	if len(holders) > 0 || result.Status == execution.ProcessSucceeded {
		return holders, nil
	}
	if result.ExitCode == 1 && strings.TrimSpace(result.Stdout) == "" && strings.TrimSpace(result.Stderr) == "" {
		return nil, nil
	}
	said := strings.TrimSpace(firstNonBlank(result.Stderr, result.Stdout))
	if said == "" {
		said = fmt.Sprintf("it ended %s with exit code %d and said nothing", result.Status, result.ExitCode)
	}
	return nil, fmt.Errorf("lsof did not answer: %s", said)
}

// describe asks ps when each holder started and what it was started as. A
// holder ps does not answer for keeps the name lsof gave it and says why its
// start is not known: it may have exited between the two questions, or ps may
// not be allowed to run here, and the number is still what held the store.
func (f OSHolderFinder) describe(ctx context.Context, holders []StoreHolder) []StoreHolder {
	listed := make([]string, 0, len(holders))
	for _, holder := range holders {
		listed = append(listed, strconv.Itoa(holder.PID))
	}
	result, err := f.Runner.Run(ctx, execution.Command{
		Name: "ps",
		Args: []string{"-o", "pid=,lstart=,command=", "-p", strings.Join(listed, ",")},
		// lstart is printed in the C locale's words whatever the machine's
		// language, which is what parsing it below needs.
		Env:     append(os.Environ(), "LC_ALL=C"),
		Timeout: holderLookupTimeout,
	}, nil)
	unread := ""
	switch {
	case err != nil:
		unread = fmt.Sprintf("ps could not be run: %v", err)
	case result.Status != execution.ProcessSucceeded && strings.TrimSpace(result.Stdout) == "":
		unread = "ps did not answer"
		if said := strings.TrimSpace(result.Stderr); said != "" {
			unread += ": " + said
		}
	}
	described := map[int]StoreHolder{}
	if unread == "" {
		described = psProcesses(result.Stdout, time.Local)
	}
	for index := range holders {
		found, ok := described[holders[index].PID]
		switch {
		case ok && !found.Since.IsZero():
			holders[index].Since = found.Since
		case unread != "":
			holders[index].SinceUnread = unread
		default:
			holders[index].SinceUnread = "ps did not list it, so it had probably exited by the time it was asked"
		}
		if ok && found.Command != "" {
			holders[index].Command = found.Command
		}
	}
	return holders
}

// psProcesses reads `ps -o pid=,lstart=,command=`: a process number, the start
// time in five words (`Thu Oct 16 05:41:25 2026`), and the command line.
func psProcesses(output string, zone *time.Location) map[int]StoreHolder {
	described := map[int]StoreHolder{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		holder := StoreHolder{PID: pid}
		if since, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", fmt.Sprintf("%s %s %s %s %s", fields[1], fields[2], fields[3], fields[4], fields[5]), zone); err == nil {
			holder.Since = since
		}
		holder.Command = boundedCommand(afterFields(line, 6))
		described[pid] = holder
	}
	return described
}

// afterFields is what follows the first count space-separated fields of line,
// as the line has it.
func afterFields(line string, count int) string {
	rest := line
	for range count {
		rest = strings.TrimLeft(rest, " \t")
		end := strings.IndexAny(rest, " \t")
		if end < 0 {
			return ""
		}
		rest = rest[end:]
	}
	return strings.TrimSpace(rest)
}

// boundedCommand is a command line folded onto one line and kept to
// maxHolderCommandBytes, with the program named by its base name, since the
// directory it ran from says nothing about what it was doing.
func boundedCommand(command string) string {
	program, rest, _ := strings.Cut(strings.TrimSpace(command), " ")
	return oneline.Fold(filepath.Base(program)+" "+rest, maxHolderCommandBytes)
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// holdersFinder is the client's finder: the one it was given, or the
// operating system's. It is not the client's own Runner, which a test scripts
// with bd's answers alone.
func (c Client) holdersFinder() HolderFinder {
	if c.Holders != nil {
		return c.Holders
	}
	return OSHolderFinder{Runner: execution.OSProcessRunner{}}
}

// whoHeld says which processes held path open, for an account of a call that
// was stopped waiting on it. what names the path in words: "the tracker's
// store", "the write lock for <id>". It never fails: a finder that cannot tell
// says why, and that is the account. This process is named among the holders
// where it is one, because a write waiting on an item's lock holds the lock
// file open itself, and another of its own calls may be what it waits on.
func (c Client) whoHeld(what, path string) string {
	if path == "" {
		return fmt.Sprintf("which process held %s could not be told: no store was found from %s", what, describeDir(c.Dir))
	}
	ctx, cancel := context.WithTimeout(context.Background(), holderLookupTimeout)
	defer cancel()
	holders, err := c.holdersFinder().Holders(ctx, path)
	if err != nil {
		return fmt.Sprintf("which process held %s could not be told: %v", what, err)
	}
	if len(holders) == 0 {
		return fmt.Sprintf("no process had %s open when it was stopped", what)
	}
	return fmt.Sprintf("when it was stopped, %s was held open by %s", what, SayHolders(holders))
}

func describeDir(dir string) string {
	if dir == "" {
		return "the directory this process runs in"
	}
	return dir
}

// databaseDirectory is the part of a store bd's database lives in, which is
// what its lock guards and what a cleanup shrinks: the embedded Dolt directory,
// a Dolt server's data directory, or the store itself where neither is there.
func databaseDirectory(store string) string {
	for _, name := range []string{"embeddeddolt", "dolt"} {
		if candidate := filepath.Join(store, name); isDirectory(candidate) {
			return candidate
		}
	}
	return store
}
