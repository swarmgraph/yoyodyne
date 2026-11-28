package supervise

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// binaryOnDisk is a deployed binary as a test writes it: the file's content is
// the revision it was built from, so deploying a build is writing the file.
type binaryOnDisk struct {
	path string
	at   time.Time
}

func newBinary(t *testing.T, revision string) *binaryOnDisk {
	t.Helper()
	binary := &binaryOnDisk{path: filepath.Join(t.TempDir(), "yoyo"), at: time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)}
	binary.deploy(t, revision)
	return binary
}

// deploy writes a new build over the binary, the way `make build` does.
func (b *binaryOnDisk) deploy(t *testing.T, revision string) {
	t.Helper()
	if err := os.WriteFile(b.path, []byte(revision), 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	b.at = b.at.Add(time.Minute)
	if err := os.Chtimes(b.path, b.at, b.at); err != nil {
		t.Fatalf("stamp binary: %v", err)
	}
}

func (b *binaryOnDisk) revision() string {
	content, err := os.ReadFile(b.path)
	if err != nil {
		return ""
	}
	return string(content)
}

func (b *binaryOnDisk) deployed() *DeployedBinary {
	return &DeployedBinary{Path: b.path, Read: func(path string) (string, error) {
		content, err := os.ReadFile(path)
		return string(content), err
	}}
}

// deployableChild is a fake child that says which build it is on — the one on
// disk when it was started — and what it is in the middle of.
type deployableChild struct {
	*fakeChild
	binary *binaryOnDisk
	events *[]string
	mu     sync.Mutex
	build  string
	busy   string
}

func newDeployable(name config.ServiceName, binary *binaryOnDisk, events *[]string) *deployableChild {
	return &deployableChild{fakeChild: &fakeChild{name: name}, binary: binary, events: events}
}

func (d *deployableChild) Ensure(ctx context.Context) (Ensured, error) {
	ensured, err := d.fakeChild.Ensure(ctx)
	if err == nil && ensured.Started {
		d.mu.Lock()
		d.build = d.binary.revision()
		d.mu.Unlock()
		d.note("start")
	}
	return ensured, err
}

func (d *deployableChild) Stop(ctx context.Context) (Stopped, error) {
	d.note("stop")
	return d.fakeChild.Stop(ctx)
}

func (d *deployableChild) Build(context.Context) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.build, nil
}

func (d *deployableChild) Busy(context.Context) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.busy, nil
}

func (d *deployableChild) setBusy(busy string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.busy = busy
}

func (d *deployableChild) stopCount() int {
	d.fakeChild.mu.Lock()
	defer d.fakeChild.mu.Unlock()
	return d.fakeChild.stops
}

func (d *deployableChild) note(event string) {
	if d.events != nil {
		*d.events = append(*d.events, string(d.name)+" "+event)
	}
}

// selfRestartingChild is the watch: it says which build it is on and takes a
// deployed build up by re-executing itself, which the test drives.
type selfRestartingChild struct {
	*deployableChild
}

func (selfRestartingChild) RestartsItself() string { return "it restarts itself into it between runs" }

// reexec is the child letting its lease go to re-execute, and then holding it
// again as the build now on disk.
func (s selfRestartingChild) letGo() { s.die() }

func (s selfRestartingChild) comeBack() {
	s.fakeChild.mu.Lock()
	s.fakeChild.running = true
	s.fakeChild.mu.Unlock()
	s.mu.Lock()
	s.build = s.binary.revision()
	s.mu.Unlock()
}

func deploySupervisor(t *testing.T, store *runstate.SupervisionStore, clock *clock, binary *binaryOnDisk, children ...Child) *Supervisor {
	supervisor := newSupervisor(t, store, clock, children...)
	supervisor.Binary = binary.deployed()
	return supervisor
}

// A deploy over a supervised part moves it onto the new build, and the move is
// recorded as a restart: deployed six times inside three minutes, which as
// deaths would have spent the five-in-two-minutes bound and left the part
// degraded, it is running on the last build with nothing counted against it.
func TestADeployOverASupervisedPartRestartsItWithTheBoundUntouched(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	binary := newBinary(t, "1111111111111111")
	sink := newDeployable(config.ServiceSlack, binary, nil)
	supervisor := deploySupervisor(t, store, clock, binary, sink)

	supervisor.Tick(context.Background())
	first := child(t, loaded(t, store), config.ServiceSlack)
	if first.Build != "1111111111111111" || !first.BuildSince.Equal(clock.Now()) {
		t.Fatalf("slack = %+v, want it on the build it was started from, since it was started", first)
	}
	if recorded := loaded(t, store); recorded.Deployed != "1111111111111111" {
		t.Fatalf("record says the binary on disk is %q, want the build written there", recorded.Deployed)
	}

	builds := []string{"2222222222222222", "3333333333333333", "4444444444444444", "5555555555555555", "6666666666666666", "7777777777777777"}
	for index, build := range builds {
		binary.deploy(t, build)
		clock.advance(DeployEvery)
		supervisor.Tick(context.Background())
		if sink.startCount() != index+2 || sink.stopCount() != index+1 {
			t.Fatalf("after deploy %d, starts = %d stops = %d, want the part stopped and started again once per deploy", index+1, sink.startCount(), sink.stopCount())
		}
	}

	got := child(t, loaded(t, store), config.ServiceSlack)
	if got.State != runstate.ChildRunning {
		t.Fatalf("slack = %+v, want running: a deploy is not a death, and six of them do not degrade a part", got)
	}
	if got.Failures != 0 || !got.NextStartAt.IsZero() || !got.DiedAt.IsZero() || got.Reason != "" {
		t.Errorf("slack = %+v, want no failure, no backoff, and no death recorded against it", got)
	}
	if got.Restarts != len(builds) || !got.RestartedAt.Equal(clock.Now()) {
		t.Errorf("slack restarts = %d at %s, want %d, the last at %s", got.Restarts, got.RestartedAt, len(builds), clock.Now())
	}
	if got.Build != "7777777777777777" || !got.BuildSince.Equal(clock.Now()) || got.RestartingInto != "" || got.Redeploy != "" {
		t.Errorf("slack = %+v, want it on the last build deployed, since that restart, with no restart outstanding", got)
	}
	said := DescribeChild(got)
	if !strings.Contains(said, "on build 777777777777 since ") || !strings.Contains(said, "restarted into a deployed build 6 times") {
		t.Errorf("DescribeChild = %q, want the build it is on, since when, and how many deploys moved it", said)
	}
}

// A part in the middle of something is left on its build until it finishes,
// and the record says what it is waiting out.
func TestARestartIntoADeployedBuildWaitsOutWhatThePartIsDoing(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	binary := newBinary(t, "1111111111111111")
	sink := newDeployable(config.ServiceSlack, binary, nil)
	supervisor := deploySupervisor(t, store, clock, binary, sink)
	supervisor.Tick(context.Background())

	sink.setBusy("the turn it is answering in the product-manager conversation")
	binary.deploy(t, "2222222222222222")
	clock.advance(DeployEvery)
	supervisor.Tick(context.Background())
	if sink.stopCount() != 0 {
		t.Fatalf("stops = %d, want a busy part left running", sink.stopCount())
	}
	waiting := child(t, loaded(t, store), config.ServiceSlack)
	if !strings.Contains(waiting.Redeploy, "behind the deployed 222222222222") || !strings.Contains(waiting.Redeploy, "once it finishes the turn it is answering") {
		t.Fatalf("redeploy = %q, want it to say the part is behind and what the restart waits out", waiting.Redeploy)
	}

	sink.setBusy("")
	clock.advance(DeployEvery)
	supervisor.Tick(context.Background())
	if sink.stopCount() != 1 || sink.startCount() != 2 {
		t.Fatalf("stops = %d starts = %d, want the part restarted once it had finished", sink.stopCount(), sink.startCount())
	}
	if got := child(t, loaded(t, store), config.ServiceSlack); got.Build != "2222222222222222" || got.Restarts != 1 || got.Redeploy != "" {
		t.Errorf("slack = %+v, want it moved onto the deployed build as one restart", got)
	}
}

// Two parts behind one deploy are moved one at a time: the first is stopped
// and started again before the second is touched.
func TestPartsAreMovedOntoADeployedBuildOneAtATime(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	binary := newBinary(t, "1111111111111111")
	var events []string
	sink := newDeployable(config.ServiceSlack, binary, &events)
	dashboard := newDeployable(config.ServiceDashboard, binary, &events)
	supervisor := deploySupervisor(t, store, clock, binary, sink, dashboard)
	supervisor.NotYet = nil
	supervisor.Tick(context.Background())

	events = nil
	binary.deploy(t, "2222222222222222")
	clock.advance(DeployEvery)
	supervisor.Tick(context.Background())

	want := "slack stop,slack start,dashboard stop,dashboard start"
	if got := strings.Join(events, ","); got != want {
		t.Fatalf("events = %s, want %s: each part stopped and started again before the next is touched", got, want)
	}
	recorded := loaded(t, store)
	for _, name := range []config.ServiceName{config.ServiceSlack, config.ServiceDashboard} {
		if got := child(t, recorded, name); got.Build != "2222222222222222" || got.Restarts != 1 || got.Failures != 0 {
			t.Errorf("%s = %+v, want it on the deployed build after one restart", name, got)
		}
	}
}

// A part somebody started by hand, and the supervisor took back, is moved onto
// a deployed build exactly as one the supervisor started is.
func TestAPartStartedByHandIsRestartedIntoADeployedBuild(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	binary := newBinary(t, "2222222222222222")
	sink := newDeployable(config.ServiceSlack, binary, nil)
	sink.running, sink.pid, sink.build = true, 555, "1111111111111111"
	supervisor := deploySupervisor(t, store, clock, binary, sink)

	supervisor.Tick(context.Background())

	if sink.stopCount() != 1 || sink.startCount() != 1 {
		t.Fatalf("stops = %d starts = %d, want the part taken back and moved onto the build on disk", sink.stopCount(), sink.startCount())
	}
	got := child(t, loaded(t, store), config.ServiceSlack)
	if got.State != runstate.ChildRunning || got.Build != "2222222222222222" || got.Restarts != 1 || got.Failures != 0 || got.Reattached {
		t.Errorf("slack = %+v, want it running on the deployed build, recorded as a restart the supervisor made", got)
	}
}

// A part that restarts itself is not stopped: the supervisor waits for it, and
// the moment its lease is let go for the re-execution is a restart rather than
// a death.
func TestAPartThatRestartsItselfIsWaitedForAndItsGapIsNotADeath(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	binary := newBinary(t, "1111111111111111")
	scheduler := selfRestartingChild{newDeployable(config.ServiceScheduler, binary, nil)}
	supervisor := deploySupervisor(t, store, clock, binary, scheduler)
	supervisor.Tick(context.Background())

	binary.deploy(t, "2222222222222222")
	clock.advance(DeployEvery)
	supervisor.Tick(context.Background())
	if scheduler.stopCount() != 0 {
		t.Fatalf("stops = %d, want a part that restarts itself left to do it", scheduler.stopCount())
	}
	waiting := child(t, loaded(t, store), config.ServiceScheduler)
	if waiting.RestartingInto != "2222222222222222" || !strings.Contains(waiting.Redeploy, "restarts itself") {
		t.Fatalf("scheduler = %+v, want it recorded as restarting itself into the deployed build", waiting)
	}

	scheduler.letGo()
	clock.advance(DefaultPoll)
	supervisor.Tick(context.Background())
	gap := child(t, loaded(t, store), config.ServiceScheduler)
	if gap.Failures != 0 || !gap.DiedAt.IsZero() || !strings.Contains(gap.Reason, "restarting itself") {
		t.Fatalf("scheduler = %+v, want the gap read as a restart with nothing counted", gap)
	}

	scheduler.comeBack()
	clock.advance(DefaultPoll)
	supervisor.Tick(context.Background())
	got := child(t, loaded(t, store), config.ServiceScheduler)
	if got.State != runstate.ChildRunning || got.Build != "2222222222222222" || got.Restarts != 1 || got.Failures != 0 || got.RestartingInto != "" {
		t.Errorf("scheduler = %+v, want it running on the deployed build, recorded as one restart", got)
	}
	if scheduler.startCount() != 1 {
		t.Errorf("starts = %d, want the supervisor to have started nothing over a part that came back itself", scheduler.startCount())
	}
}

// A part that let its lease go to restart itself and has not come back within
// the grace is started from the deployed binary, still as a restart.
func TestAPartThatDoesNotComeBackFromRestartingItselfIsStartedFromTheBinary(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	binary := newBinary(t, "1111111111111111")
	scheduler := selfRestartingChild{newDeployable(config.ServiceScheduler, binary, nil)}
	supervisor := deploySupervisor(t, store, clock, binary, scheduler)
	supervisor.Tick(context.Background())

	// Deployed and re-executed before the supervisor's next look at builds:
	// the binary alone says the gap is a restart.
	binary.deploy(t, "2222222222222222")
	scheduler.letGo()
	clock.advance(DefaultPoll)
	supervisor.Tick(context.Background())
	if got := child(t, loaded(t, store), config.ServiceScheduler); got.Failures != 0 || got.RestartingInto != "2222222222222222" {
		t.Fatalf("scheduler = %+v, want the gap read as a restart into the deployed build", got)
	}

	clock.advance(SelfRestartGrace)
	supervisor.Tick(context.Background())
	if scheduler.startCount() != 2 {
		t.Fatalf("starts = %d, want the part started from the binary once the grace ran out", scheduler.startCount())
	}
	got := child(t, loaded(t, store), config.ServiceScheduler)
	if got.State != runstate.ChildRunning || got.Build != "2222222222222222" || got.Restarts != 1 || got.Failures != 0 {
		t.Errorf("scheduler = %+v, want it running on the deployed build, recorded as a restart", got)
	}
}

// A part that dies on its own, with no deploy behind it, is still a death.
func TestADeathWithNoDeployIsStillADeath(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	binary := newBinary(t, "1111111111111111")
	scheduler := selfRestartingChild{newDeployable(config.ServiceScheduler, binary, nil)}
	supervisor := deploySupervisor(t, store, clock, binary, scheduler)
	supervisor.Tick(context.Background())

	scheduler.letGo()
	clock.advance(DefaultPoll)
	supervisor.Tick(context.Background())
	if got := child(t, loaded(t, store), config.ServiceScheduler); got.Failures != 1 || got.Restarts != 0 {
		t.Errorf("scheduler = %+v, want a death counted and no restart recorded", got)
	}
}

// passingChild is the sink: its work comes in passes, and the supervisor holds
// it between two of them for the length of its stop.
type passingChild struct {
	*deployableChild
	inPass bool
	held   bool
}

func (p *passingChild) HoldBetweenPasses(context.Context) (func(), string, error) {
	if p.inPass {
		return nil, "the pass over the records it is making", nil
	}
	p.held = true
	p.note("hold")
	return func() { p.held = false; p.note("release") }, "", nil
}

// A part in the middle of a pass is restarted only once the pass is over, and
// is held from starting another for as long as its stop takes, so the restart
// lands between two passes.
func TestARestartWaitsOutAPassAndHoldsThePartBetweenPassesWhileItStops(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	binary := newBinary(t, "1111111111111111")
	var events []string
	sink := &passingChild{deployableChild: newDeployable(config.ServiceSlack, binary, &events)}
	supervisor := deploySupervisor(t, store, clock, binary, sink)
	supervisor.Tick(context.Background())

	sink.inPass = true
	binary.deploy(t, "2222222222222222")
	clock.advance(DeployEvery)
	supervisor.Tick(context.Background())
	if sink.stopCount() != 0 {
		t.Fatalf("stops = %d, want a part in the middle of a pass left running", sink.stopCount())
	}
	waiting := child(t, loaded(t, store), config.ServiceSlack)
	if !strings.Contains(waiting.Redeploy, "once it finishes the pass over the records it is making") || waiting.RestartingInto != "" {
		t.Fatalf("slack = %+v, want it waiting out the pass with no restart begun", waiting)
	}

	sink.inPass = false
	events = nil
	clock.advance(DeployEvery)
	supervisor.Tick(context.Background())
	if got, want := strings.Join(events, ","), "slack hold,slack stop,slack release,slack start"; got != want {
		t.Fatalf("events = %s, want %s: held between passes for the stop, and let go before the part in its place starts", got, want)
	}
	if sink.held {
		t.Error("the part is still held between passes after its restart, so the part started in its place would never make one")
	}
	if got := child(t, loaded(t, store), config.ServiceSlack); got.Build != "2222222222222222" || got.Restarts != 1 || got.Failures != 0 {
		t.Errorf("slack = %+v, want it moved onto the deployed build as one restart", got)
	}
}

// A part the supervisor found already running is on its build since a moment
// the supervisor never saw, so the record gives no date for it rather than the
// moment of the first look; a move the supervisor sees is dated.
func TestAReattachedPartIsNotDatedOnItsBuildUntilItMoves(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	binary := newBinary(t, "1111111111111111")
	scheduler := selfRestartingChild{newDeployable(config.ServiceScheduler, binary, nil)}
	scheduler.running, scheduler.pid, scheduler.build = true, 555, "1111111111111111"
	supervisor := deploySupervisor(t, store, clock, binary, scheduler)

	supervisor.Tick(context.Background())
	got := child(t, loaded(t, store), config.ServiceScheduler)
	if got.Build != "1111111111111111" || !got.BuildSince.IsZero() {
		t.Fatalf("scheduler = %+v, want its build known and no date given for when it moved there", got)
	}
	if said := DescribeChild(got); !strings.Contains(said, "on build 111111111111") || strings.Contains(said, "since") {
		t.Errorf("DescribeChild = %q, want the build named with no date the record does not have", said)
	}

	binary.deploy(t, "2222222222222222")
	scheduler.letGo()
	clock.advance(DefaultPoll)
	supervisor.Tick(context.Background())
	scheduler.comeBack()
	clock.advance(DefaultPoll)
	supervisor.Tick(context.Background())
	if moved := child(t, loaded(t, store), config.ServiceScheduler); moved.Build != "2222222222222222" || !moved.BuildSince.Equal(clock.Now()) {
		t.Errorf("scheduler = %+v, want the move it was seen to make dated", moved)
	}
}

// The supervisor does not wait for a part that moves itself before moving
// itself: while the scheduler drains its runs on the old build, nothing the
// supervisor is moving holds its own take-up back, and only the moment the
// scheduler lets its lease go to re-execute does. While the supervisor is
// behind, its record says since when — the moment the build was written — and
// what it waits for.
func TestTheSupervisorIsNotHeldBackByAPartDrainingItself(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	binary := newBinary(t, "1111111111111111")
	sink := newDeployable(config.ServiceSlack, binary, nil)
	scheduler := selfRestartingChild{newDeployable(config.ServiceScheduler, binary, nil)}
	supervisor := deploySupervisor(t, store, clock, binary, sink, scheduler)
	supervisor.Build = "1111111111111111"
	supervisor.Tick(context.Background())
	if recorded := loaded(t, store); recorded.TakeUpWait != "" {
		t.Fatalf("take-up wait = %q, want nothing said by a supervisor on the deployed build", recorded.TakeUpWait)
	}

	binary.deploy(t, "2222222222222222")
	clock.advance(DeployEvery)
	supervisor.Tick(context.Background())
	draining := child(t, loaded(t, store), config.ServiceScheduler)
	if draining.RestartingInto != "2222222222222222" {
		t.Fatalf("scheduler = %+v, want it taking the deployed build up itself", draining)
	}
	if moving := supervisor.Moving(); moving != "" {
		t.Fatalf("Moving() = %q while only the scheduler is draining itself, want nothing holding the supervisor back", moving)
	}
	recorded := loaded(t, store)
	if !recorded.DeployedAt.Equal(binary.at) || recorded.TakeUpWait != "at its next look" {
		t.Fatalf("record = deployed at %s, waiting %q; want the build's write time %s and the supervisor moving at its next look", recorded.DeployedAt, recorded.TakeUpWait, binary.at)
	}

	supervisor.AwaitTakeUp("once the release cut under way since 2026-09-28 05:00 PDT has finished")
	clock.advance(DefaultPoll)
	supervisor.Tick(context.Background())
	if recorded := loaded(t, store); !strings.Contains(recorded.TakeUpWait, "release cut") {
		t.Fatalf("take-up wait = %q, want what holds the supervisor back recorded", recorded.TakeUpWait)
	}

	scheduler.letGo()
	clock.advance(DefaultPoll)
	supervisor.Tick(context.Background())
	if moving := supervisor.Moving(); moving != string(config.ServiceScheduler) {
		t.Fatalf("Moving() = %q while the scheduler has let its lease go to re-execute, want the scheduler named", moving)
	}
}

// overdueScheduler is the watch with a wind-down a test can leave stuck: it
// never restarts itself, and says it is overdue once the test says so.
type overdueScheduler struct {
	selfRestartingChild
	overdue *string
}

func (o overdueScheduler) RestartOverdue(context.Context, time.Time) (string, error) {
	return *o.overdue, nil
}

// A scheduler past its wind-down limit that has not restarted itself is
// restarted by the supervisor once it says it is overdue, and not before: it
// is stopped once, the way `yoyo stop` stops it, started from the deployed
// binary, and recorded as a restart rather than a death, so the runs it had
// stopped and preserved for the move are picked up by the session that comes
// back exactly as after a restart it made itself. While a restart is held, it
// is left alone and the record says why.
func TestASchedulerPastItsWindDownLimitIsRestartedOntoTheDeployedBuild(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 11, 18, 17, 50, 0, 0, time.UTC)}
	binary := newBinary(t, "1111111111111111")
	overdue := ""
	scheduler := overdueScheduler{
		selfRestartingChild: selfRestartingChild{newDeployable(config.ServiceScheduler, binary, nil)},
		overdue:             &overdue,
	}
	supervisor := deploySupervisor(t, store, clock, binary, scheduler)
	held := ""
	supervisor.RestartHold = func() string { return held }
	supervisor.Tick(context.Background())

	// The build lands and the scheduler begins winding down; inside its limit,
	// and past it but not yet overdue, it is left to restart itself.
	binary.deploy(t, "2222222222222222")
	for range 4 {
		clock.advance(DeployEvery)
		supervisor.Tick(context.Background())
	}
	if scheduler.stopCount() != 0 {
		t.Fatalf("stops = %d, want a scheduler that is not overdue left to restart itself", scheduler.stopCount())
	}

	// Overdue while a restart is held: left alone, and the record says why.
	overdue = "has been past its limit for winding down to move onto a new build for 5m0s"
	held = "the provider cannot be reached"
	clock.advance(DeployEvery)
	supervisor.Tick(context.Background())
	if scheduler.stopCount() != 0 {
		t.Fatalf("stops = %d, want nothing restarted while a restart is held", scheduler.stopCount())
	}
	if got := child(t, loaded(t, store), config.ServiceScheduler); !strings.Contains(got.Redeploy, "past its limit for winding down") || !strings.Contains(got.Redeploy, "the provider cannot be reached") {
		t.Fatalf("scheduler = %+v, want the record to say it is overdue and what holds the restart", got)
	}

	held = ""
	clock.advance(DeployEvery)
	supervisor.Tick(context.Background())
	if scheduler.stopCount() != 1 || scheduler.startCount() != 2 {
		t.Fatalf("stops = %d starts = %d, want the overdue scheduler stopped once and started again", scheduler.stopCount(), scheduler.startCount())
	}
	got := child(t, loaded(t, store), config.ServiceScheduler)
	if got.State != runstate.ChildRunning || got.Build != "2222222222222222" || got.RestartingInto != "" {
		t.Fatalf("scheduler = %+v, want it running on the deployed build", got)
	}
	if got.Restarts != 1 || got.Failures != 0 || !got.DiedAt.IsZero() {
		t.Fatalf("scheduler = %+v, want one restart into a deployed build and no death counted against it", got)
	}

	// On the deployed build it is no longer behind, so it is not asked again.
	overdue = ""
	clock.advance(DeployEvery)
	supervisor.Tick(context.Background())
	if scheduler.stopCount() != 1 {
		t.Fatalf("stops = %d, want a scheduler on the deployed build left running", scheduler.stopCount())
	}
}

// scheduledScheduler is the watch whose move the supervisor times: it says
// where its move stands as the test sets it, and never restarts itself.
type scheduledScheduler struct {
	selfRestartingChild
	standing *MoveStanding
}

func (s scheduledScheduler) MoveStanding(context.Context) (MoveStanding, error) {
	return *s.standing, nil
}

func newScheduledScheduler(binary *binaryOnDisk) (scheduledScheduler, *MoveStanding) {
	standing := &MoveStanding{}
	return scheduledScheduler{selfRestartingChild: selfRestartingChild{newDeployable(config.ServiceScheduler, binary, nil)}, standing: standing}, standing
}

// The incident this exists for: builds landing every few minutes while the
// scheduler, on a build that does not begin its move, stays behind. The limit
// is counted from when the supervisor first recorded it behind, so each
// landing leaves the deadline where it was; at the deadline the supervisor
// restarts it, and it comes back on the newest build, not on any of the ones
// that landed between (yoyodyne-ifd.428.225).
func TestBuildsLandingEveryFewMinutesLeaveTheSchedulersDeadlineWhereItWas(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 11, 27, 20, 0, 0, 0, time.UTC)}
	binary := newBinary(t, "1111111111111111")
	scheduler, _ := newScheduledScheduler(binary)
	supervisor := deploySupervisor(t, store, clock, binary, scheduler)
	supervisor.Tick(context.Background())

	binary.deploy(t, "2222222222222222")
	clock.advance(DeployEvery)
	supervisor.Tick(context.Background())
	behind := clock.now
	got := child(t, loaded(t, store), config.ServiceScheduler)
	if !got.MoveDue.Equal(behind) || !got.MoveBy.Equal(behind.Add(MoveLimit)) {
		t.Fatalf("scheduler = %+v, want it recorded behind at %s and to be on the newest build by %s", got, behind, behind.Add(MoveLimit))
	}
	for _, build := range []string{"3333333333333333", "4444444444444444", "5555555555555555"} {
		clock.advance(4 * time.Minute)
		binary.deploy(t, build)
		supervisor.Tick(context.Background())
		got = child(t, loaded(t, store), config.ServiceScheduler)
		if !got.MoveDue.Equal(behind) || !got.MoveBy.Equal(behind.Add(MoveLimit)) {
			t.Fatalf("after build %s landed, scheduler = %+v; want the deadline left at %s", short(build), got, behind.Add(MoveLimit))
		}
		if scheduler.stopCount() != 0 {
			t.Fatalf("stops = %d after build %s landed, want the scheduler left to begin its move within its limit", scheduler.stopCount(), short(build))
		}
	}
	for _, want := range []string{"on build " + short("1111111111111111"), "newest installed build " + short("5555555555555555"), "since", "has not begun winding down"} {
		if !strings.Contains(got.Redeploy, want) {
			t.Fatalf("scheduler redeploy = %q, want it to say %q", got.Redeploy, want)
		}
	}

	clock.advance(behind.Add(MoveLimit).Sub(clock.now))
	supervisor.Tick(context.Background())
	if scheduler.stopCount() != 1 || scheduler.startCount() != 2 {
		t.Fatalf("stops = %d starts = %d at its deadline, want the scheduler stopped once and started from the binary", scheduler.stopCount(), scheduler.startCount())
	}
	got = child(t, loaded(t, store), config.ServiceScheduler)
	if got.State != runstate.ChildRunning || got.Build != "5555555555555555" || got.Restarts != 1 || got.Failures != 0 || !got.MoveDue.IsZero() || !got.MoveBy.IsZero() || got.MoveHeld != "" {
		t.Fatalf("scheduler = %+v, want it on the newest build after one restart, with nothing left of the timing", got)
	}
}

// The other parts move onto a new build at the supervisor's next looks while
// the scheduler, winding down, is still behind; the scheduler is left to wind
// down — pausing its runs itself — within its limit, and then moves itself,
// with nothing stopped by the supervisor.
func TestTheOtherPartsMoveWhileTheSchedulerIsBehindAndThenTheSchedulerMoves(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 11, 27, 20, 18, 0, 0, time.UTC)}
	binary := newBinary(t, "1111111111111111")
	sink := newDeployable(config.ServiceSlack, binary, nil)
	dashboard := newDeployable(config.ServiceDashboard, binary, nil)
	scheduler, standing := newScheduledScheduler(binary)
	supervisor := deploySupervisor(t, store, clock, binary, sink, scheduler, dashboard)
	supervisor.Tick(context.Background())

	binary.deploy(t, "2222222222222222")
	*standing = MoveStanding{WindingDown: true}
	for range 3 {
		clock.advance(DeployEvery)
		supervisor.Tick(context.Background())
	}
	recorded := loaded(t, store)
	for _, name := range []config.ServiceName{config.ServiceSlack, config.ServiceDashboard} {
		if got := child(t, recorded, name); got.Build != "2222222222222222" {
			t.Fatalf("%s = %+v, want it moved onto the new build while the scheduler winds down", name, got)
		}
	}
	got := child(t, recorded, config.ServiceScheduler)
	if got.Build != "1111111111111111" || scheduler.stopCount() != 0 || !strings.Contains(got.Redeploy, "winding down to move onto it, and is to be on it by") {
		t.Fatalf("scheduler = %+v, stops = %d; want it behind, left to wind down, and the record saying by when", got, scheduler.stopCount())
	}

	scheduler.letGo()
	clock.advance(DefaultPoll)
	supervisor.Tick(context.Background())
	scheduler.comeBack()
	clock.advance(DefaultPoll)
	supervisor.Tick(context.Background())
	got = child(t, loaded(t, store), config.ServiceScheduler)
	if got.State != runstate.ChildRunning || got.Build != "2222222222222222" || !got.MoveDue.IsZero() || scheduler.stopCount() != 0 {
		t.Fatalf("scheduler = %+v, stops = %d; want it on the new build by its own move, never stopped", got, scheduler.stopCount())
	}
}

// A wind-down that waits out a merge queue check has its limit lengthened
// once, to that check's own time limit and MoveLimit past it, and never again
// by a later check. Past the limit nothing is stopped, and the record says
// plainly what holds the move: the queue check by name, and the run not yet
// at a point where it can be paused, by its run and item.
func TestAWindDownPastItsLimitNamesTheQueueCheckAndTheRunThatHoldIt(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 11, 27, 19, 0, 0, 0, time.UTC)}
	binary := newBinary(t, "1111111111111111")
	scheduler, standing := newScheduledScheduler(binary)
	supervisor := deploySupervisor(t, store, clock, binary, scheduler)
	supervisor.Tick(context.Background())
	binary.deploy(t, "2222222222222222")
	clock.advance(DeployEvery)
	behind := clock.now
	queue := "the merge queue for main, checking build 4 of the queued change, to finish make test"
	checkUntil := behind.Add(40 * time.Minute)
	*standing = MoveStanding{WindingDown: true, QueueCheck: queue, QueueCheckUntil: checkUntil}
	supervisor.Tick(context.Background())
	got := child(t, loaded(t, store), config.ServiceScheduler)
	if !got.MoveBy.Equal(checkUntil.Add(MoveLimit)) || got.MoveCheck != queue {
		t.Fatalf("scheduler = %+v, want its limit the queue check's own and %s past it, %s", got, MoveLimit, checkUntil.Add(MoveLimit))
	}

	clock.advance(30 * time.Minute)
	standing.QueueCheck, standing.QueueCheckUntil = "the merge queue for main, checking build 5 of the queued change, to finish make test", clock.now.Add(time.Hour)
	supervisor.Tick(context.Background())
	if got := child(t, loaded(t, store), config.ServiceScheduler); !got.MoveBy.Equal(checkUntil.Add(MoveLimit)) || got.MoveCheck != queue {
		t.Fatalf("scheduler = %+v, want a later queue check leaving the limit where the first one put it", got)
	}

	standing.Waiting = "run run-7 (yoyodyne-ifd.9) to finish make test, until 2026-11-27 11:05 AKST at the latest"
	clock.advance(checkUntil.Add(MoveLimit).Sub(clock.now) + time.Minute)
	supervisor.Tick(context.Background())
	got = child(t, loaded(t, store), config.ServiceScheduler)
	if scheduler.stopCount() != 0 {
		t.Fatalf("stops = %d, want nothing stopped to meet the limit", scheduler.stopCount())
	}
	for _, want := range []string{queue, "run run-7 (yoyodyne-ifd.9)"} {
		if !strings.Contains(got.MoveHeld, want) || !strings.Contains(got.Redeploy, want) {
			t.Fatalf("scheduler = %+v, want what holds its move past its limit to name %q", got, want)
		}
	}
}

// The timing is kept in the supervisor's record, so a supervisor restarted
// while the scheduler is behind — and it restarts onto every build installed
// — carries on the deadline the one before it set, even with a newer build
// landed in between, and restarts the scheduler at it.
func TestASupervisorRestartedWhileTheSchedulerIsBehindKeepsTheDeadline(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 11, 27, 21, 19, 0, 0, time.UTC)}
	binary := newBinary(t, "1111111111111111")
	scheduler, _ := newScheduledScheduler(binary)
	first := deploySupervisor(t, store, clock, binary, scheduler)
	first.Tick(context.Background())
	binary.deploy(t, "2222222222222222")
	clock.advance(DeployEvery)
	first.Tick(context.Background())
	deadline := clock.now.Add(MoveLimit)

	clock.advance(8 * time.Minute)
	binary.deploy(t, "3333333333333333")
	restarted := deploySupervisor(t, store, clock, binary, scheduler)
	restarted.Tick(context.Background())
	if got := child(t, loaded(t, store), config.ServiceScheduler); !got.MoveBy.Equal(deadline) {
		t.Fatalf("after the supervisor restarted, scheduler = %+v; want its deadline still %s", got, deadline)
	}
	if scheduler.stopCount() != 0 {
		t.Fatalf("stops = %d, want the scheduler left to move itself before its deadline", scheduler.stopCount())
	}
	clock.advance(deadline.Sub(clock.now))
	restarted.Tick(context.Background())
	if scheduler.stopCount() != 1 {
		t.Fatalf("stops = %d at %s, want the scheduler restarted at the deadline the first supervisor set", scheduler.stopCount(), clock.now)
	}
}

// Past its limit while every restart is held, or after a restart that failed,
// the scheduler is left where it is and the record says what holds it.
func TestASchedulerPastItsLimitSaysWhenARestartIsHeldOrFailed(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 11, 27, 11, 18, 0, 0, time.UTC)}
	binary := newBinary(t, "1111111111111111")
	scheduler, _ := newScheduledScheduler(binary)
	supervisor := deploySupervisor(t, store, clock, binary, scheduler)
	held := "the provider cannot be reached"
	supervisor.RestartHold = func() string { return held }
	supervisor.Tick(context.Background())
	binary.deploy(t, "2222222222222222")
	clock.advance(DeployEvery)
	supervisor.Tick(context.Background())
	clock.advance(MoveLimit + DeployEvery)
	supervisor.Tick(context.Background())
	got := child(t, loaded(t, store), config.ServiceScheduler)
	if scheduler.stopCount() != 0 || got.MoveHeld != "nothing is restarted while the provider cannot be reached" || !strings.Contains(got.Redeploy, held) {
		t.Fatalf("stops = %d, scheduler = %+v; want nothing restarted and the hold named", scheduler.stopCount(), got)
	}

	held = ""
	scheduler.fakeChild.mu.Lock()
	scheduler.fakeChild.startErr = errors.New("launchctl refused the start")
	scheduler.fakeChild.mu.Unlock()
	clock.advance(DeployEvery)
	supervisor.Tick(context.Background())
	got = child(t, loaded(t, store), config.ServiceScheduler)
	if !strings.HasPrefix(got.MoveHeld, "a restart that failed") || !strings.Contains(got.MoveHeld, "launchctl refused the start") || got.MoveDue.IsZero() {
		t.Fatalf("scheduler = %+v, want the failed restart and its reason recorded as what holds the move", got)
	}
}

// A machine that sleeps across the scheduler's limit wakes with the
// scheduler's own loop yet to run: the first look after waking must not stop
// it, and a look MoveLimit after the wake the operating system records must.
func TestASchedulerIsNotRestartedAtTheFirstLookAfterTheMachineSleptThroughItsMove(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 11, 19, 20, 0, 0, 0, time.UTC)}
	binary := newBinary(t, "1111111111111111")
	scheduler, _ := newScheduledScheduler(binary)
	supervisor := deploySupervisor(t, store, clock, binary, scheduler)
	var mu sync.Mutex
	var power []runstate.PowerEvent
	supervisor.PowerHistory = func(context.Context) ([]runstate.PowerEvent, error) {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(power), nil
	}
	supervisor.Tick(context.Background())
	binary.deploy(t, "2222222222222222")
	clock.advance(DeployEvery)
	supervisor.Tick(context.Background())

	asleep := clock.now.Add(time.Minute)
	clock.advance(3 * time.Hour)
	mu.Lock()
	power = []runstate.PowerEvent{
		{At: asleep, Source: "pmset: Sleep"},
		{At: clock.now, Awake: true, Source: "pmset: Wake"},
	}
	mu.Unlock()
	supervisor.Tick(context.Background())
	if scheduler.stopCount() != 0 {
		t.Fatalf("stops = %d, want the scheduler left to begin its move after the machine woke", scheduler.stopCount())
	}
	if got := child(t, loaded(t, store), config.ServiceScheduler); !got.MoveBy.Equal(clock.now.Add(MoveLimit)) {
		t.Fatalf("scheduler = %+v, want its limit counted again from the wake at %s", got, clock.now)
	}
	clock.advance(MoveLimit + DeployEvery)
	supervisor.Tick(context.Background())
	if scheduler.stopCount() != 1 {
		t.Fatalf("stops = %d, want the scheduler restarted once it had its whole limit awake", scheduler.stopCount())
	}
}

// The supervisor times its own move the same way: from when it first recorded
// itself behind, left there by builds landing meanwhile and by a restart onto
// the same old build, and cleared once it is on the newest build. Past its
// limit the record carries what holds it, which status and the health check
// say (readmodel.Services.SupervisorBehind).
func TestTheSupervisorTimesItsOwnMoveFromWhenItFirstFellBehind(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 11, 27, 16, 15, 0, 0, time.UTC)}
	binary := newBinary(t, "1111111111111111")
	first := deploySupervisor(t, store, clock, binary)
	first.Build = "1111111111111111"
	first.Tick(context.Background())
	if recorded := loaded(t, store); !recorded.BehindSince.IsZero() {
		t.Fatalf("record = %+v, want nothing timed for a supervisor on the newest build", recorded)
	}

	binary.deploy(t, "2222222222222222")
	clock.advance(DeployEvery)
	first.Tick(context.Background())
	behind := clock.now
	clock.advance(4 * time.Minute)
	binary.deploy(t, "3333333333333333")
	first.AwaitTakeUp("once the dashboard has settled")
	first.Tick(context.Background())
	if recorded := loaded(t, store); !recorded.BehindSince.Equal(behind) || !recorded.BehindBy.Equal(behind.Add(MoveLimit)) {
		t.Fatalf("record = behind since %s, by %s; want %s and %s, left there by the newer landing", recorded.BehindSince, recorded.BehindBy, behind, behind.Add(MoveLimit))
	}

	clock.advance(time.Minute)
	again := deploySupervisor(t, store, clock, binary)
	again.Build = "1111111111111111"
	again.AwaitTakeUp("once the dashboard has settled")
	again.Tick(context.Background())
	recorded := loaded(t, store)
	if !recorded.BehindSince.Equal(behind) {
		t.Fatalf("after a restart onto the same build, behind since = %s, want %s kept", recorded.BehindSince, behind)
	}
	clock.advance(MoveLimit)
	again.Tick(context.Background())
	if recorded := loaded(t, store); !recorded.BehindBy.Before(clock.now.Add(time.Second)) || recorded.TakeUpWait != "once the dashboard has settled" {
		t.Fatalf("record = %+v, want it past its limit with what holds it recorded", recorded)
	}

	moved := deploySupervisor(t, store, clock, binary)
	moved.Build = "3333333333333333"
	moved.Tick(context.Background())
	if recorded := loaded(t, store); !recorded.BehindSince.IsZero() || !recorded.BehindBy.IsZero() {
		t.Fatalf("record = %+v, want nothing timed once the supervisor is on the newest build", recorded)
	}
}
