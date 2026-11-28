package execution

import (
	"context"
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// fakeProcesses is a process table and a signal a test controls: a process
// ends on the first signal it does not ignore, and one whose signal is refused
// never ends.
type fakeProcesses struct {
	mu        sync.Mutex
	entries   map[int]processEntry
	ignore    map[int]syscall.Signal
	refuse    map[int]error
	reuse     map[int]string // a number handed to a new process at the first re-read
	listed    int
	cwd       map[int]string
	ports     map[int][]string
	holders   map[string][]int
	signalled []string
	// connected is each process's live local connections, and onConnections
	// is called with the number of each reading of them before it is answered,
	// so a test can connect or close between one reading and the next.
	connected     map[int][]localConnection
	connectionsAt int
	onConnections func(reading int)
	connectionErr error
}

func newFakeProcesses(entries ...processEntry) *fakeProcesses {
	fake := &fakeProcesses{entries: map[int]processEntry{}, ignore: map[int]syscall.Signal{}, refuse: map[int]error{}, reuse: map[int]string{}, cwd: map[int]string{}, ports: map[int][]string{}, holders: map[string][]int{}, connected: map[int][]localConnection{}}
	for _, entry := range entries {
		fake.entries[entry.PID] = entry
	}
	return fake
}

func (f *fakeProcesses) list() ([]processEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listed++
	if f.listed == 2 {
		for pid, start := range f.reuse {
			entry := f.entries[pid]
			entry.Start = start
			f.entries[pid] = entry
		}
	}
	listed := make([]processEntry, 0, len(f.entries))
	for _, entry := range f.entries {
		listed = append(listed, entry)
	}
	return listed, nil
}

func (f *fakeProcesses) workingDirectories(pids []int) map[int]string {
	return f.cwd
}

func (f *fakeProcesses) holding(paths []string) map[int]bool {
	holding := map[int]bool{}
	for _, path := range paths {
		for _, pid := range f.holders[path] {
			holding[pid] = true
		}
	}
	return holding
}

func (f *fakeProcesses) connections() (map[int][]localConnection, error) {
	f.mu.Lock()
	f.connectionsAt++
	reading, hook := f.connectionsAt, f.onConnections
	f.mu.Unlock()
	if hook != nil {
		hook(reading)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.connectionErr != nil {
		return nil, f.connectionErr
	}
	answered := map[int][]localConnection{}
	for pid, connections := range f.connected {
		answered[pid] = append([]localConnection(nil), connections...)
	}
	return answered, nil
}

// connect records a live local connection between two processes, as each end
// would be read.
func (f *fakeProcesses) connect(a, b int, over string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected[a] = append(f.connected[a], localConnection{Over: over, Peers: []int{b}})
	f.connected[b] = append(f.connected[b], localConnection{Over: over, Peers: []int{a}})
}

func (f *fakeProcesses) disconnect() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected = map[int][]localConnection{}
}

func (f *fakeProcesses) listening(pid int) []string {
	return f.ports[pid]
}

func (f *fakeProcesses) signal(pid int, signal syscall.Signal) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signalled = append(f.signalled, signal.String()+" "+strconv.Itoa(pid))
	if err := f.refuse[pid]; err != nil {
		return err
	}
	if f.ignore[pid] == signal {
		return nil
	}
	delete(f.entries, pid)
	return nil
}

func marked(pid int, runID string) processEntry {
	return processEntry{PID: pid, Parent: 1, Start: "1", Command: "java", Environment: []string{"PATH=/bin", RunIdentityVariable + "=" + runID}}
}

func quickStop(fake *fakeProcesses) RunProcesses {
	return RunProcesses{TermWait: 100 * time.Millisecond, KillWait: 100 * time.Millisecond, table: fake, signal: fake.signal}
}

func TestRunProcessesStopsOnlyThisRunsProcesses(t *testing.T) {
	fake := newFakeProcesses(marked(4001, "run-a"), marked(4002, "run-b"), processEntry{PID: 4003, Start: "1", Command: "zsh"})
	left := quickStop(fake).Stop(context.Background(), RunProcessesRequest{RunID: "run-a"})
	if len(left) != 0 {
		t.Fatalf("Stop() left %+v, want nothing", left)
	}
	if _, ok := fake.entries[4001]; ok {
		t.Fatal("this run's process is still running")
	}
	if _, ok := fake.entries[4002]; !ok {
		t.Fatal("another run's process was stopped")
	}
	if !slices.Equal(fake.signalled, []string{"terminated 4001"}) {
		t.Fatalf("signals = %v, want only the stop signal to this run's process", fake.signalled)
	}
}

func TestRunProcessesNeverSignalsItsOwnLineage(t *testing.T) {
	fake := newFakeProcesses(marked(os.Getpid(), "run-a"), marked(os.Getppid(), "run-a"))
	if left := quickStop(fake).Stop(context.Background(), RunProcessesRequest{RunID: "run-a"}); len(left) != 0 || len(fake.signalled) != 0 {
		t.Fatalf("Stop() left %+v and sent %v, want the harness's own processes untouched", left, fake.signalled)
	}
}

func TestRunProcessesLeavesANumberReusedByAnotherProcess(t *testing.T) {
	fake := newFakeProcesses(marked(4001, "run-a"))
	fake.reuse[4001] = "2"
	if left := quickStop(fake).Stop(context.Background(), RunProcessesRequest{RunID: "run-a"}); len(left) != 0 {
		t.Fatalf("Stop() left %+v, want nothing", left)
	}
	if len(fake.signalled) != 0 {
		t.Fatalf("signals = %v, want none to a number another process now holds", fake.signalled)
	}
}

func TestRunProcessesKillsOneThatIgnoresTheStopSignal(t *testing.T) {
	fake := newFakeProcesses(marked(4001, "run-a"))
	fake.ignore[4001] = syscall.SIGTERM
	if left := quickStop(fake).Stop(context.Background(), RunProcessesRequest{RunID: "run-a"}); len(left) != 0 {
		t.Fatalf("Stop() left %+v, want nothing", left)
	}
	if !slices.Equal(fake.signalled, []string{"terminated 4001", "killed 4001"}) {
		t.Fatalf("signals = %v, want the stop signal then the kill signal", fake.signalled)
	}
}

func TestRunProcessesNamesOneItCouldNotStop(t *testing.T) {
	fake := newFakeProcesses(marked(4242, "run-a"))
	fake.refuse[4242] = syscall.EPERM
	fake.ports[4242] = []string{"127.0.0.1:25565"}
	left := quickStop(fake).Stop(context.Background(), RunProcessesRequest{RunID: "run-a"})
	if len(left) != 1 {
		t.Fatalf("Stop() left %+v, want the one process", left)
	}
	want := "a process this run started is still running: process 4242, java, listening on 127.0.0.1:25565; the harness could not stop it: permission refused"
	if got := left[0].Describe(); got != want {
		t.Fatalf("Describe() = %q\nwant %q", got, want)
	}
}

func TestRunProcessesNamesAndNeverStopsAnUnmarkedProcessInTheWorktree(t *testing.T) {
	worktree := t.TempDir()
	fake := newFakeProcesses(processEntry{PID: 4005, Start: "1", Command: "zsh", Environment: []string{"PATH=/bin"}}, processEntry{PID: 4006, Start: "1", Command: "vim"})
	fake.cwd[4005] = worktree + "/internal"
	fake.cwd[4006] = "/"
	left := quickStop(fake).Stop(context.Background(), RunProcessesRequest{RunID: "run-a", Folders: []string{worktree}})
	if len(left) != 1 || left[0].PID != 4005 || !left[0].Unmarked {
		t.Fatalf("Stop() left %+v, want the shell in the worktree named", left)
	}
	if !strings.Contains(left[0].Describe(), "not stopped: it carries no sign that this run started it") {
		t.Fatalf("Describe() = %q", left[0].Describe())
	}
	if len(fake.signalled) != 0 {
		t.Fatalf("signals = %v, want none", fake.signalled)
	}
}

func TestWithRunIdentityReplacesAnInheritedMark(t *testing.T) {
	environment := WithRunIdentity([]string{"PATH=/bin", RunIdentityVariable + "=run-outer"}, "run-inner")
	if !slices.Equal(environment, []string{"PATH=/bin", RunIdentityVariable + "=run-inner"}) {
		t.Fatalf("WithRunIdentity() = %v", environment)
	}
	if id, ok := runIdentityIn(environment); !ok || id != "run-inner" {
		t.Fatalf("runIdentityIn() = %q, %v", id, ok)
	}
}

// A process that cleared its environment but still holds the file the harness
// handed one of the run's launches is the run's and is stopped; one holding
// another run's file is not, and neither is one that holds this run's file but
// carries another run's identifier.
func TestRunProcessesStopsAnUnmarkedProcessHoldingTheRunsLaunchFile(t *testing.T) {
	fake := newFakeProcesses(
		processEntry{PID: 4010, Start: "1", Command: "java"},
		processEntry{PID: 4011, Start: "1", Command: "java"},
		marked(4012, "run-b"),
	)
	fake.holders["/state/run-a.attempt-1.hold"] = []int{4010, 4012}
	fake.holders["/state/run-b.attempt-1.hold"] = []int{4011}
	left := quickStop(fake).Stop(context.Background(), RunProcessesRequest{RunID: "run-a", Holds: []string{"/state/run-a.attempt-1.hold"}})
	if len(left) != 0 {
		t.Fatalf("Stop() left %+v, want nothing", left)
	}
	if !slices.Equal(fake.signalled, []string{"terminated 4010"}) {
		t.Fatalf("signals = %v, want only the stop signal to the process holding this run's file", fake.signalled)
	}
}

// A process list that cannot be read is said as the harness not having been
// able to look, never as a process still running.
func TestRunProcessesSaysWhenItCouldNotLook(t *testing.T) {
	left := (RunProcesses{table: unreadableTable{}}).Stop(context.Background(), RunProcessesRequest{RunID: "run-a"})
	if len(left) != 1 {
		t.Fatalf("Stop() = %+v, want one entry saying the list could not be read", left)
	}
	said := left[0].Describe()
	if said != "the harness could not check for processes this run left running; the harness could not list this machine's processes: refused" {
		t.Fatalf("Describe() = %q", said)
	}
}

type unreadableTable struct{}

func (unreadableTable) list() ([]processEntry, error)           { return nil, errors.New("refused") }
func (unreadableTable) workingDirectories([]int) map[int]string { return nil }
func (unreadableTable) listening(int) []string                  { return nil }
func (unreadableTable) holding([]string) map[int]bool           { return nil }
func (unreadableTable) connections() (map[int][]localConnection, error) {
	return nil, errors.New("refused")
}

// MarkedRuns names every run a process started with, once each and in order,
// and leaves out a process that has exited, process 1, and this process.
func TestMarkedRunsNamesEachRunAProcessCarries(t *testing.T) {
	fake := newFakeProcesses(
		processEntry{PID: 1, Environment: []string{RunIdentityVariable + "=run-init"}},
		processEntry{PID: os.Getpid(), Environment: []string{RunIdentityVariable + "=run-self"}},
		processEntry{PID: 501, Start: "a", Environment: []string{RunIdentityVariable + "=run-b"}},
		processEntry{PID: 502, Start: "a", Environment: []string{RunIdentityVariable + "=run-a"}},
		processEntry{PID: 503, Start: "a", Environment: []string{RunIdentityVariable + "=run-b"}},
		processEntry{PID: 504, Start: "a", Gone: true, Environment: []string{RunIdentityVariable + "=run-gone"}},
		processEntry{PID: 505, Start: "a", Environment: []string{"PATH=/bin"}},
	)
	marked, err := (RunProcesses{table: fake}).MarkedRuns()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"run-a", "run-b"}; !slices.Equal(marked, want) {
		t.Fatalf("MarkedRuns() = %v, want %v", marked, want)
	}
	if _, err := (RunProcesses{table: unreadableTable{}}).MarkedRuns(); err == nil {
		t.Fatal("MarkedRuns() said nothing of a table it could not read")
	}
}

// A child whose starting environment the process list hides, as macOS hides a
// system program's, is the run's while a running parent carries the run's
// identifier, and is stopped with it. One under another run's process, or one
// whose parent has ended, is not; one of the latter working in the worktree is
// named, saying its environment could not be read.
func TestRunProcessesStopsAHiddenChildOfTheRunsProcess(t *testing.T) {
	worktree := t.TempDir()
	parent := marked(4100, "run-a")
	hidden := processEntry{PID: 4101, Parent: 4100, Start: "1", Command: "sleep"}
	grandchild := processEntry{PID: 4102, Parent: 4101, Start: "1", Command: "sleep"}
	theirs := processEntry{PID: 4201, Parent: 4200, Start: "1", Command: "sleep"}
	orphan := processEntry{PID: 4301, Parent: 1, Start: "1", Command: "sleep"}
	fake := newFakeProcesses(parent, hidden, grandchild, marked(4200, "run-b"), theirs, orphan)
	fake.cwd[4301] = worktree
	left := quickStop(fake).Stop(context.Background(), RunProcessesRequest{RunID: "run-a", Folders: []string{worktree}})
	for _, pid := range []int{4100, 4101, 4102} {
		if _, ok := fake.entries[pid]; ok {
			t.Errorf("process %d, the run's or under it, is still running", pid)
		}
	}
	for _, pid := range []int{4200, 4201, 4301} {
		if _, ok := fake.entries[pid]; !ok {
			t.Errorf("process %d, which is not the run's, was stopped", pid)
		}
	}
	if len(left) != 1 || left[0].PID != 4301 || !left[0].Unmarked ||
		!strings.Contains(left[0].Describe(), "not stopped: its starting environment could not be read, and it carries no other sign that this run started it") {
		t.Fatalf("Stop() left %+v", left)
	}
}
