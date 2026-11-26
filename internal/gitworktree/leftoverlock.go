package gitworktree

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
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// A Git lock file left in a run's own checkout.
//
// Git takes a lock by creating a file beside what it is about to change —
// index.lock beside the index, HEAD.lock beside HEAD — and renames or removes it
// when it is done. A Git process killed part-way through a write never gets
// there, and the file it leaves refuses every later Git command that needs the
// same lock, with a message telling a person to remove it by hand. The harness
// kills Git on its own budgets, so it is the harness that leaves these, and
// removing one is a step it can take itself once it has established that the
// lock is not somebody's live write.
//
// What it looks at is the checkout's own Git directory, the registration under
// the common directory's worktrees/, and nothing else: the locks there belong
// to this checkout alone. The common directory's locks — a branch's ref lock,
// packed-refs.lock — are shared with the primary checkout and every other run,
// and are left to Git and to whoever holds them.

// ErrGitLockHeld reports a Git lock file in a run's checkout that the harness
// left alone, because a live process is using it or because whether one is
// could not be established. The stop it causes is the machine's rather than the
// change's: nothing about the work was judged.
var ErrGitLockHeld = errors.New("a Git lock file in the run's checkout is in use or could not be shown to be left over")

// GitLockClearing is what the harness did about one Git lock file it found in a
// run's checkout before writing there: removed it, or left it alone, and why,
// in words a person reads.
type GitLockClearing struct {
	// Path is the lock file, absolute.
	Path string
	// Removed says the harness took the file away. A lock left alone is
	// recorded too, so the run's record says why the harness would not.
	Removed bool
	// Why is the reason in plain words: that no live process was using it, or
	// which process was, or what could not be asked.
	Why string
	// At is when the lock was looked at.
	At time.Time
}

// gitLockUser is one live process using a lock or working in the checkout.
type gitLockUser struct {
	PID     int
	Command string
}

func (u gitLockUser) String() string {
	if u.Command == "" {
		return "process " + strconv.Itoa(u.PID)
	}
	return fmt.Sprintf("%s (process %d)", u.Command, u.PID)
}

// gitLockUsers answers which live processes are using a lock file or working
// in a checkout. The default asks lsof; a test supplies its own.
type gitLockUsers func(ctx context.Context, lock string, places []string) ([]gitLockUser, error)

// gitLockClearingsKey carries, on a context, where the manager says each lock
// it removed or left alone.
type gitLockClearingsKey struct{}

// WithGitLockClearings is ctx carrying where the manager says what it did about
// each Git lock file it found in a run's checkout. It travels on the context
// for the reason a crossing does: one manager serves every run a process hosts,
// and the record to write it on is the run's.
func WithGitLockClearings(ctx context.Context, record func(GitLockClearing)) context.Context {
	return context.WithValue(ctx, gitLockClearingsKey{}, record)
}

func saidGitLock(ctx context.Context, clearing GitLockClearing) {
	if record, wired := ctx.Value(gitLockClearingsKey{}).(func(GitLockClearing)); wired && record != nil {
		record(clearing)
	}
}

// gitLockLookupTimeout bounds each question put to lsof.
const gitLockLookupTimeout = 20 * time.Second

// clearLeftoverGitLocks removes every lock file in the checkout's own Git
// directory that no live process is using, before the harness writes there.
// A lock a live process holds, or one whose users could not be asked about, is
// left exactly where it is, and the first of those is returned as
// ErrGitLockHeld in words that say why.
//
// No live process is established two ways, and both must answer: nothing has
// the lock file open, which is how Git holds a lock while it writes, and no Git
// process is working in the checkout or its Git directory, which is how a Git
// process between taking a lock and writing to it would show. A file that
// changed between the look and the removal is left alone, because it is no
// longer the file that was asked about.
//
// A checkout this cannot place — not the harness's, not registered, its Git
// directory unreadable — is not refused here. The ownership checks every write
// already makes refuse it in their own words, and a lock nobody could find is
// one this has nothing to say about.
func (m *Manager) clearLeftoverGitLocks(ctx context.Context, worktree Worktree) error {
	path, err := m.validateOwnedPath(worktree)
	if err != nil {
		return nil
	}
	common, gitDirectory, err := m.checkoutGitDirectory(ctx, path)
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(gitDirectory)
	if err != nil {
		return nil
	}
	var locks []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".lock") {
			locks = append(locks, entry.Name())
		}
	}
	if len(locks) == 0 {
		return nil
	}
	root, err := repowrite.NewRoot(common)
	if err != nil {
		return fmt.Errorf("resolve the common Git directory: %w", err)
	}
	registration, err := filepath.Rel(root.Path(), gitDirectory)
	if err != nil {
		return fmt.Errorf("place the checkout's Git directory under the common one: %w", err)
	}
	users := m.lockUsers
	if users == nil {
		users = m.lsofGitLockUsers
	}
	var held []error
	for _, name := range locks {
		lock := filepath.Join(gitDirectory, name)
		clearing, err := m.clearLeftoverGitLock(ctx, root, filepath.ToSlash(filepath.Join(registration, name)), lock, []string{path, gitDirectory}, users)
		if clearing.Path == "" {
			continue
		}
		saidGitLock(ctx, clearing)
		if err != nil {
			held = append(held, err)
		}
	}
	if len(held) > 0 {
		return held[0]
	}
	return nil
}

// clearLeftoverGitLock decides one lock file. A clearing with no path is a
// lock that was already gone when it was looked at, which is nothing to say.
func (m *Manager) clearLeftoverGitLock(ctx context.Context, root repowrite.Root, relative, lock string, places []string, users gitLockUsers) (GitLockClearing, error) {
	clearing := GitLockClearing{Path: lock, At: time.Now().UTC()}
	before, err := os.Lstat(lock)
	if errors.Is(err, os.ErrNotExist) {
		return GitLockClearing{}, nil
	}
	if err != nil {
		clearing.Why = fmt.Sprintf("the harness left Git's lock file %s alone because it could not look at it: %v", lock, err)
		return clearing, fmt.Errorf("%w: %s", ErrGitLockHeld, clearing.Why)
	}
	using, err := users(ctx, lock, places)
	if err != nil {
		if _, gone := os.Lstat(lock); errors.Is(gone, os.ErrNotExist) {
			return GitLockClearing{}, nil
		}
		clearing.Why = fmt.Sprintf("the harness left Git's lock file %s alone because it could not find out whether a live process is using it (%v); once no Git process is working in %s it can be removed by hand",
			lock, err, places[0])
		return clearing, fmt.Errorf("%w: %s", ErrGitLockHeld, clearing.Why)
	}
	if len(using) > 0 {
		named := make([]string, 0, len(using))
		for _, user := range using {
			named = append(named, user.String())
		}
		clearing.Why = fmt.Sprintf("the harness left Git's lock file %s alone because a live process is using it or working in the checkout: %s; it is released when that process ends",
			lock, strings.Join(named, ", "))
		return clearing, fmt.Errorf("%w: %s", ErrGitLockHeld, clearing.Why)
	}
	after, err := os.Lstat(lock)
	if errors.Is(err, os.ErrNotExist) {
		return GitLockClearing{}, nil
	}
	if err != nil || !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		clearing.Why = fmt.Sprintf("the harness left Git's lock file %s alone because it changed while the harness was asking who was using it, so something is writing it", lock)
		return clearing, fmt.Errorf("%w: %s", ErrGitLockHeld, clearing.Why)
	}
	if _, err := root.RemoveFile(relative); err != nil {
		clearing.Why = fmt.Sprintf("the harness found Git's lock file %s left over, with no live process using it, and could not remove it: %v", lock, err)
		return clearing, fmt.Errorf("%w: %s", ErrGitLockHeld, clearing.Why)
	}
	clearing.Removed = true
	clearing.Why = fmt.Sprintf("the harness removed Git's lock file %s, last changed %s, because no live process had it open and no Git process was working in the checkout, so a Git process that ended part-way through a write had left it",
		lock, before.ModTime().UTC().Format(time.RFC3339))
	return clearing, nil
}

// checkoutGitDirectory is the common Git directory and the checkout's own Git
// directory inside it, both with symlinks resolved. A checkout whose Git
// directory is not a registration directly under the common directory's
// worktrees/ is refused: its locks are not this checkout's alone.
func (m *Manager) checkoutGitDirectory(ctx context.Context, path string) (string, string, error) {
	common, err := m.commonGitDirectory(ctx)
	if err != nil {
		return "", "", err
	}
	common, err = filepath.EvalSymlinks(common)
	if err != nil {
		return "", "", fmt.Errorf("resolve the common Git directory: %w", err)
	}
	result, err := m.run(ctx, "-C", path, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", "", err
	}
	if result.Status != execution.ProcessSucceeded {
		return "", "", fmt.Errorf("resolve the checkout's Git directory failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	directory, err := filepath.EvalSymlinks(strings.TrimSpace(result.Stdout))
	if err != nil {
		return "", "", fmt.Errorf("resolve the checkout's Git directory: %w", err)
	}
	if filepath.Dir(directory) != filepath.Join(common, worktreeRegistrations) {
		return "", "", fmt.Errorf("the checkout's Git directory %s is not a worktree registration under %s", directory, common)
	}
	return common, directory, nil
}

// lsofGitLockUsers asks lsof two things: which processes have the lock file
// open, and which Git processes are working in any of places. Either naming a
// process is a live user; lsof failing to answer either is an answer nobody
// can act on, and is returned as an error.
func (m *Manager) lsofGitLockUsers(ctx context.Context, lock string, places []string) ([]gitLockUser, error) {
	// -F pcn is lsof's machine form: a `p<pid>` line per process, its `c<name>`,
	// and an `n<path>` per file. -n and -P keep it from looking up names, and -w
	// keeps its warnings about other file systems out of what is read.
	open, err := m.lsof(ctx, "-w", "-n", "-P", "-F", "pcn", "--", lock)
	if err != nil {
		return nil, err
	}
	// -c git takes every command whose name begins with git, and -d cwd only
	// their working directories, joined by -a.
	working, err := m.lsof(ctx, "-w", "-n", "-P", "-a", "-c", "git", "-d", "cwd", "-F", "pcn")
	if err != nil {
		return nil, err
	}
	users := map[int]gitLockUser{}
	for _, process := range open {
		users[process.PID] = gitLockUser{PID: process.PID, Command: process.Command}
	}
	for _, process := range working {
		for _, name := range process.Names {
			if withinAny(name, places) {
				users[process.PID] = gitLockUser{PID: process.PID, Command: process.Command}
			}
		}
	}
	listed := make([]gitLockUser, 0, len(users))
	for _, user := range users {
		listed = append(listed, user)
	}
	sort.Slice(listed, func(i, j int) bool { return listed[i].PID < listed[j].PID })
	return listed, nil
}

func withinAny(name string, places []string) bool {
	for _, place := range places {
		if name == place || strings.HasPrefix(name, place+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (m *Manager) lsof(ctx context.Context, args ...string) ([]lsofProcess, error) {
	result, err := m.runner.Run(ctx, execution.Command{Name: "lsof", Args: args, Env: execution.GitEnvironment(nil), Timeout: gitLockLookupTimeout}, nil)
	if err != nil {
		return nil, fmt.Errorf("lsof could not be run: %w", err)
	}
	return readLsof(result)
}

// lsofProcess is one process in lsof's machine form, with the files it named.
type lsofProcess struct {
	PID     int
	Command string
	Names   []string
}

// readLsof reads lsof's machine form. lsof exits 1 when nothing it was asked
// about is open, which is an answer, and also when it could not look, which is
// not: an exit of 1 that named nothing and said nothing is the first, and any
// other failure that named nothing is the second.
func readLsof(result execution.ProcessResult) ([]lsofProcess, error) {
	var processes []lsofProcess
	for _, line := range strings.Split(result.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, err := strconv.Atoi(line[1:])
			if err != nil || pid <= 0 {
				continue
			}
			processes = append(processes, lsofProcess{PID: pid})
		case 'c':
			if len(processes) > 0 {
				processes[len(processes)-1].Command = line[1:]
			}
		case 'n':
			if len(processes) > 0 {
				processes[len(processes)-1].Names = append(processes[len(processes)-1].Names, line[1:])
			}
		}
	}
	if len(processes) > 0 || result.Status == execution.ProcessSucceeded {
		return processes, nil
	}
	if result.ExitCode == 1 && strings.TrimSpace(result.Stdout) == "" && strings.TrimSpace(result.Stderr) == "" {
		return nil, nil
	}
	said := strings.TrimSpace(result.Stderr)
	if said == "" {
		said = fmt.Sprintf("it ended %s with exit code %d and said nothing", result.Status, result.ExitCode)
	}
	return nil, fmt.Errorf("lsof did not answer: %s", said)
}
