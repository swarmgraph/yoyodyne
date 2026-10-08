package supervise

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// processTable is the machine as the copies tests drive it: every process that
// recorded itself as a part, whether it is running, has exited uncollected, or
// is gone, and which of them are the supervisor's children.
type processTable struct {
	mu        sync.Mutex
	processes map[int]*tabledProcess
	forgotten []int
	collected []int
	nextPID   int
}

type tabledProcess struct {
	reader   runstate.ConfigReader
	exited   bool
	gone     bool
	children bool
}

func newProcessTable() *processTable {
	return &processTable{processes: map[int]*tabledProcess{}, nextPID: 5000}
}

// start is a process the supervisor started recording itself.
func (p *processTable) start(service, build string, at time.Time) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nextPID++
	p.processes[p.nextPID] = &tabledProcess{
		reader:   runstate.ConfigReader{Service: service, PID: p.nextPID, Build: build, StartedAt: at},
		children: true,
	}
	return p.nextPID
}

// stranger is a running copy somebody other than the supervisor started.
func (p *processTable) stranger(service, build string, at time.Time) int {
	pid := p.start(service, build, at)
	p.mu.Lock()
	p.processes[pid].children = false
	p.mu.Unlock()
	return pid
}

// exit is a process exiting, which leaves it in the table until its parent
// collects it — what every stopped sink did before the supervisor collected
// what it stops.
func (p *processTable) exit(pid int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if process, ok := p.processes[pid]; ok {
		process.exited = true
	}
}

func (p *processTable) Processes() ([]runstate.RecordedProcess, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var recorded []runstate.RecordedProcess
	for _, process := range p.processes {
		look := runstate.ProcessLook{}
		if !process.gone {
			look = runstate.ProcessLook{Exists: true, Exited: process.exited, StartedAt: process.reader.StartedAt}
		}
		recorded = append(recorded, runstate.RecordedProcess{Reader: process.reader, Look: look})
	}
	return recorded, nil
}

func (p *processTable) Forget(process runstate.RecordedProcess) error {
	if !process.Gone() {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.processes, process.Reader.PID)
	p.forgotten = append(p.forgotten, process.Reader.PID)
	return nil
}

func (p *processTable) collect(pid int) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	process, ok := p.processes[pid]
	if !ok || !process.exited || !process.children {
		return false, nil
	}
	process.gone = true
	p.collected = append(p.collected, pid)
	return true, nil
}

// running is every copy of a service still running, as any reader of the
// records now counts them.
func (p *processTable) running(service string) []runstate.ConfigReader {
	processes, _ := p.Processes()
	var running []runstate.ConfigReader
	for _, process := range processes {
		if process.Reader.Service == service && process.Running() {
			running = append(running, process.Reader)
		}
	}
	return running
}

// tabledChild is a part whose starts and stops are processes in the table: a
// start records a process on the build on disk, and a stop lets go of the
// lease and leaves the process exited and uncollected, exactly as a real stop
// of a part the supervisor launched did.
type tabledChild struct {
	*deployableChild
	table *processTable
	clock *clock
}

func (c *tabledChild) Ensure(ctx context.Context) (Ensured, error) {
	ensured, err := c.deployableChild.Ensure(ctx)
	if err != nil || !ensured.Started {
		return ensured, err
	}
	pid := c.table.start(string(c.name), c.binary.revision(), c.clock.Now())
	c.fakeChild.mu.Lock()
	c.fakeChild.pid = pid
	c.fakeChild.mu.Unlock()
	ensured.PID = pid
	return ensured, nil
}

func (c *tabledChild) Stop(ctx context.Context) (Stopped, error) {
	stopped, err := c.deployableChild.Stop(ctx)
	if stopped.WasRunning {
		c.table.exit(stopped.PID)
	}
	return stopped, err
}

// After a deploy exactly one copy of the part runs, and it is on the build just
// deployed: the copy the deploy stopped is collected rather than left in the
// process table, where every reading of the records would count it as a copy
// still running on the old build.
func TestARedeployLeavesExactlyOneCopyOfThePartOnTheNewBuild(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 10, 4, 17, 0, 0, 0, time.UTC)}
	binary := newBinary(t, "1111111111111111")
	table := newProcessTable()
	sink := &tabledChild{deployableChild: newDeployable(config.ServiceSlack, binary, nil), table: table, clock: clock}
	supervisor := deploySupervisor(t, store, clock, binary, sink)
	supervisor.Copies = table
	supervisor.Collect = table.collect

	supervisor.Tick(context.Background())
	first := table.running("slack")
	if len(first) != 1 || first[0].Build != "1111111111111111" {
		t.Fatalf("running copies after the first start = %+v, want one, on the build on disk", first)
	}

	for _, build := range []string{"2222222222222222", "3333333333333333", "4444444444444444"} {
		binary.deploy(t, build)
		clock.advance(DeployEvery)
		supervisor.Tick(context.Background())
		running := table.running("slack")
		if len(running) != 1 {
			t.Fatalf("after deploying %s, %d copies of the sink run: %+v, want exactly one", build, len(running), running)
		}
		if running[0].Build != build {
			t.Fatalf("after deploying %s, the one copy is on build %s, want the build just deployed", build, running[0].Build)
		}
		if got := child(t, loaded(t, store), config.ServiceSlack); got.PID != running[0].PID || got.Build != build {
			t.Fatalf("the record names %+v, want the one running copy, pid %d on %s", got, running[0].PID, build)
		}
	}
	if len(table.collected) != 3 {
		t.Errorf("collected %v, want each of the three copies the deploys stopped", table.collected)
	}
	if processes, _ := table.Processes(); len(processes) != 1 {
		t.Errorf("records left = %+v, want only the running copy's: a stopped copy's record is removed once it is gone", processes)
	}
}

// A running copy the supervisor did not start and cannot account for is said,
// with its process, build, and start, and left running: it is neither stopped
// nor sent a signal, and its record is kept so every reading goes on reporting
// it until somebody finds what started it.
func TestACopyTheSupervisorCannotAccountForIsReportedAndLeftRunning(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 10, 4, 17, 0, 0, 0, time.UTC)}
	binary := newBinary(t, "2222222222222222")
	table := newProcessTable()
	sink := &tabledChild{deployableChild: newDeployable(config.ServiceSlack, binary, nil), table: table, clock: clock}
	var mu sync.Mutex
	var logged []string
	supervisor := deploySupervisor(t, store, clock, binary, sink)
	supervisor.Copies = table
	supervisor.Collect = table.collect
	supervisor.Log = func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logged = append(logged, fmt.Sprintf(format, args...))
	}
	strangerStarted := time.Date(2026, 10, 3, 12, 19, 0, 0, time.UTC)
	stranger := table.stranger("slack", "1111111111111111", strangerStarted)

	supervisor.Tick(context.Background())
	clock.advance(DeployEvery)
	supervisor.Tick(context.Background())

	running := table.running("slack")
	if len(running) != 2 {
		t.Fatalf("running copies = %+v, want the supervisor's and the one it cannot account for, both left running", running)
	}
	if sink.stopCount() != 0 {
		t.Errorf("the supervisor stopped its own sink %d times over a copy it did not start", sink.stopCount())
	}
	if len(table.collected) != 0 || len(table.forgotten) != 0 {
		t.Errorf("collected %v and forgot %v, want the stranger's process and record left alone", table.collected, table.forgotten)
	}
	mu.Lock()
	defer mu.Unlock()
	said := 0
	for _, line := range logged {
		if strings.Contains(line, "cannot account for") {
			said++
			for _, want := range []string{fmt.Sprintf("pid %d", stranger), "on build 111111111111", strangerStarted.Local().Format(time.RFC3339), "left running"} {
				if !strings.Contains(line, want) {
					t.Errorf("the supervisor said %q, want %q in it", line, want)
				}
			}
		}
	}
	if said != 1 {
		t.Errorf("the supervisor said the copy %d times over two looks, want once: %q", said, logged)
	}
}
