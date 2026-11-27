package execution

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Stopping every process a run started, including one that left the run's
// process group (docs/designs/stopping-what-a-run-started.md).
//
// The runner ends a command's process group when the command ends
// (reapProcessTree), and a process that put itself in a group or session of its
// own is out of that reach: a test server started from a developer's shell kept
// a port for fourteen hours after its run ended. So every process the harness
// starts for a run carries the run's identifier in its environment, which every
// process it starts in turn inherits unless it is made to clear it, and when a
// session or the run ends the harness lists the processes of the user it runs
// as and stops the ones that started with this run's identifier.
//
// Stopping the wrong process is the worse mistake: it can destroy a person's
// work or another run's, and nothing brings that back, while a process missed is
// named and can still be stopped. So the identifier is the only thing that makes
// a process the run's. A process inside the run's worktree or scratch folder
// that does not carry it — one that cleared its environment, or a person's own
// shell opened in the worktree — is named and never stopped.

// RunIdentityVariable carries the identifier of the run a process was started
// for. It is under the harness's own prefix, so the explicit environment passes
// it on to everything the process starts.
const RunIdentityVariable = "YOYODYNE_RUN_ID"

// WithRunIdentity returns environment marked as started for runID. A nil
// environment is this process's own; a mark already there is replaced, because
// a harness launched by a harness inherits the outer run's mark and the process
// being launched is the inner run's. An empty runID leaves the environment as
// it is.
func WithRunIdentity(environment []string, runID string) []string {
	if environment == nil {
		environment = os.Environ()
	}
	if strings.TrimSpace(runID) == "" {
		return environment
	}
	kept := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if strings.HasPrefix(entry, RunIdentityVariable+"=") {
			continue
		}
		kept = append(kept, entry)
	}
	return append(kept, RunIdentityVariable+"="+runID)
}

// runIdentityIn is the run a starting environment names, and whether it names
// one.
func runIdentityIn(environment []string) (string, bool) {
	for _, entry := range environment {
		if value, found := strings.CutPrefix(entry, RunIdentityVariable+"="); found && value != "" {
			return value, true
		}
	}
	return "", false
}

// LeftProcess is a process the harness found for a run and left running: one it
// could not stop, or one inside the run's folders that carries no sign the run
// started it.
type LeftProcess struct {
	PID     int    `json:"pid"`
	Command string `json:"command,omitempty"`
	// Listening is every address and port the process listens on, as lsof
	// names them.
	Listening []string `json:"listening,omitempty"`
	// Unmarked is a process found only by its working directory, which the
	// harness never stops because only a person can say whether it is theirs.
	Unmarked bool `json:"unmarked,omitempty"`
	// Why says why it is still running.
	Why string `json:"why"`
}

// Describe is the line every surface shows for the process.
func (l LeftProcess) Describe() string {
	var line strings.Builder
	if l.Unmarked {
		line.WriteString("a process inside this run's folders is still running: ")
	} else {
		line.WriteString("a process this run started is still running: ")
	}
	fmt.Fprintf(&line, "process %d", l.PID)
	if l.Command != "" {
		line.WriteString(", " + l.Command)
	}
	if len(l.Listening) > 0 {
		line.WriteString(", listening on " + strings.Join(l.Listening, ", "))
	}
	line.WriteString("; " + l.Why)
	return line.String()
}

// unmarkedWhy is what is said of a process found only by its working directory.
const unmarkedWhy = "not stopped: it carries no sign that this run started it"

// RunProcessesRequest names the run whose processes are stopped and the folders
// a process left inside is named from: the run's worktree and scratch folder.
type RunProcessesRequest struct {
	RunID   string
	Folders []string
}

// RunProcesses finds and stops the processes a run started. The zero value
// waits as the design says and reads this machine's processes.
type RunProcesses struct {
	// TermWait is how long a process is given to end after the stop signal, and
	// KillWait how long after the kill signal. Zero is 10 and 5 seconds.
	TermWait time.Duration
	KillWait time.Duration
	// table and signal are this machine's unless a test supplies its own.
	table  processTable
	signal func(pid int, signal syscall.Signal) error
}

// processEntry is one process as the table read it.
type processEntry struct {
	PID    int
	Parent int
	// Start identifies when the process started, in whatever form the platform
	// gives it; two readings of one number that differ here are two processes.
	Start   string
	Gone    bool // a process that has exited and waits only to be reaped
	Command string
	// Environment is the environment the process started with, and nil where it
	// could not be read.
	Environment []string
}

// processTable is the machine's processes as the harness may see them.
type processTable interface {
	// list is the processes of the user the harness runs as.
	list() ([]processEntry, error)
	// workingDirectories is where each named process is working, for those it
	// can say.
	workingDirectories(pids []int) map[int]string
	// listening is every address and port a process listens on.
	listening(pid int) []string
}

// Stop stops every process that started with this run's identifier and returns
// what it left running. It never looks at another user's processes, at process
// 1, or at this process and the processes it descends from. A table that
// cannot be read at all is said as one entry rather than as nothing found.
func (r RunProcesses) Stop(ctx context.Context, request RunProcessesRequest) []LeftProcess {
	runID := strings.TrimSpace(request.RunID)
	if runID == "" {
		return nil
	}
	table := r.table
	if table == nil {
		table = systemProcessTable{}
	}
	signal := r.signal
	if signal == nil {
		signal = sendSignal
	}
	entries, err := table.list()
	if err != nil {
		return []LeftProcess{{Why: "the harness could not list this machine's processes to stop the run's: " + err.Error()}}
	}
	excluded := ownLineage(entries)
	var marked []processEntry
	var others []processEntry
	for _, entry := range entries {
		if entry.PID <= 1 || entry.Gone || excluded[entry.PID] {
			continue
		}
		if id, ok := runIdentityIn(entry.Environment); ok {
			if id == runID {
				marked = append(marked, entry)
			}
			continue
		}
		others = append(others, entry)
	}
	left := r.stopAll(ctx, table, signal, marked)
	left = append(left, unmarkedInside(table, others, request.Folders)...)
	for index := range left {
		if left[index].PID > 0 {
			left[index].Listening = table.listening(left[index].PID)
		}
	}
	sort.Slice(left, func(i, j int) bool { return left[i].PID < left[j].PID })
	return left
}

// stopAll sends the stop signal to every target, waits, sends the kill signal
// to what is left, waits again, and returns what is still running. Before every
// signal the table is read again, and a number now held by a process with a
// different start time is left alone: it is somebody else's.
func (r RunProcesses) stopAll(ctx context.Context, table processTable, signal func(int, syscall.Signal) error, targets []processEntry) []LeftProcess {
	if len(targets) == 0 {
		return nil
	}
	termWait, killWait := r.TermWait, r.KillWait
	if termWait <= 0 {
		termWait = 10 * time.Second
	}
	if killWait <= 0 {
		killWait = 5 * time.Second
	}
	refused := map[int]string{}
	remaining := targets
	for _, round := range []struct {
		signal syscall.Signal
		wait   time.Duration
	}{{syscall.SIGTERM, termWait}, {syscall.SIGKILL, killWait}} {
		remaining = stillTheSame(table, remaining)
		for _, target := range remaining {
			if err := signal(target.PID, round.signal); err != nil && !errors.Is(err, syscall.ESRCH) {
				refused[target.PID] = signalRefusal(err)
			}
		}
		remaining = waitGone(ctx, table, remaining, round.wait)
		if len(remaining) == 0 {
			return nil
		}
	}
	left := make([]LeftProcess, 0, len(remaining))
	for _, target := range remaining {
		why := "the harness could not stop it: it was still running after the kill signal"
		if refusal, ok := refused[target.PID]; ok {
			why = "the harness could not stop it: " + refusal
		}
		left = append(left, LeftProcess{PID: target.PID, Command: target.Command, Why: why})
	}
	return left
}

// signalRefusal says why a signal was not delivered.
func signalRefusal(err error) string {
	if errors.Is(err, syscall.EPERM) {
		return "permission refused"
	}
	return err.Error()
}

// stillTheSame is the targets still running under the number and start time
// they were found with.
func stillTheSame(table processTable, targets []processEntry) []processEntry {
	entries, err := table.list()
	if err != nil {
		return targets
	}
	now := make(map[int]processEntry, len(entries))
	for _, entry := range entries {
		now[entry.PID] = entry
	}
	kept := targets[:0:0]
	for _, target := range targets {
		if current, ok := now[target.PID]; ok && !current.Gone && current.Start == target.Start {
			kept = append(kept, target)
		}
	}
	return kept
}

// waitGone waits up to wait for the targets to end and returns those still
// running at the end of it.
func waitGone(ctx context.Context, table processTable, targets []processEntry, wait time.Duration) []processEntry {
	deadline := time.Now().Add(wait)
	for {
		targets = stillTheSame(table, targets)
		if len(targets) == 0 || !time.Now().Before(deadline) {
			return targets
		}
		select {
		case <-ctx.Done():
			return targets
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// unmarkedInside is every process working inside one of the folders that does
// not carry a run's identifier, named and never stopped.
func unmarkedInside(table processTable, candidates []processEntry, folders []string) []LeftProcess {
	var roots []string
	for _, folder := range folders {
		if strings.TrimSpace(folder) == "" {
			continue
		}
		roots = append(roots, filepath.Clean(folder))
		if resolved, err := filepath.EvalSymlinks(folder); err == nil {
			roots = append(roots, resolved)
		}
	}
	if len(roots) == 0 || len(candidates) == 0 {
		return nil
	}
	pids := make([]int, 0, len(candidates))
	for _, candidate := range candidates {
		pids = append(pids, candidate.PID)
	}
	directories := table.workingDirectories(pids)
	var named []LeftProcess
	for _, candidate := range candidates {
		directory, ok := directories[candidate.PID]
		if !ok || !insideAny(directory, roots) {
			continue
		}
		named = append(named, LeftProcess{PID: candidate.PID, Command: candidate.Command, Unmarked: true, Why: unmarkedWhy})
	}
	return named
}

func insideAny(directory string, roots []string) bool {
	directory = filepath.Clean(directory)
	for _, root := range roots {
		if directory == root || strings.HasPrefix(directory, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// ownLineage is this process and every process it descends from, which the
// harness never signals: a harness started by a harness carries the outer
// run's identifier.
func ownLineage(entries []processEntry) map[int]bool {
	parents := make(map[int]int, len(entries))
	for _, entry := range entries {
		parents[entry.PID] = entry.Parent
	}
	lineage := map[int]bool{}
	for pid := os.Getpid(); pid > 1 && !lineage[pid]; pid = parents[pid] {
		lineage[pid] = true
		if _, known := parents[pid]; !known {
			break
		}
	}
	lineage[os.Getppid()] = true
	return lineage
}
