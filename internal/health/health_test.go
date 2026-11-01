package health

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

var observedAt = time.Date(2026, 10, 16, 15, 0, 0, 0, time.UTC)

const product = domain.ProductID("yoyodyne")

// fakeRuns is a run store whose runs and presence a test sets: a run named in
// absent has no process behind it, and one named in unreadable cannot be asked.
type fakeRuns struct {
	states     []runstate.State
	err        error
	absent     map[string]bool
	unreadable map[string]bool
}

func (f fakeRuns) Incomplete() ([]runstate.State, error)  { return f.states, f.err }
func (f fakeRuns) Outstanding() ([]runstate.State, error) { return f.states, f.err }
func (f fakeRuns) Recorded() ([]runstate.State, error)    { return f.states, f.err }
func (f fakeRuns) Price(string) (runstate.ItemPrice, error) {
	return runstate.ItemPrice{}, nil
}
func (f fakeRuns) Presence(state runstate.State, _ time.Duration, _ time.Time) (runstate.RunPresence, error) {
	if f.unreadable[state.RunID] {
		return runstate.RunPresence{Found: true}, errors.New("the holder stamp could not be read")
	}
	if f.absent[state.RunID] {
		return runstate.RunPresence{Says: "the process that held it, pid 4242, has exited without recording an ending"}, nil
	}
	return runstate.RunPresence{Found: true}, nil
}

type fakeReaders struct {
	running []runstate.ConfigReader
}

func (f fakeReaders) Mismatches() ([]runstate.ConfigMismatch, error) { return nil, nil }
func (f fakeReaders) Running() ([]runstate.ConfigReader, error)      { return f.running, nil }

type fakePasses struct{ passes []runstate.Sweep }

func (f fakePasses) List() ([]runstate.Sweep, []runstate.UnreadableSweep, error) {
	return f.passes, nil, nil
}

// fakeFiles is the file system as a test arranges it: every path named is a
// file anybody may execute, and every other path is missing.
type fakeFiles map[string]bool

type fileInfo struct{ fs.FileInfo }

func (fileInfo) Mode() fs.FileMode { return 0o755 }

func (f fakeFiles) stat(path string) (fs.FileInfo, error) {
	if f[path] {
		return fileInfo{}, nil
	}
	return nil, fs.ErrNotExist
}

func run(id, item string) runstate.State {
	started := observedAt.Add(-2 * time.Hour)
	return runstate.State{SchemaVersion: runstate.StateSchemaVersion, RunID: id, ProductID: product, RepositoryID: "yoyodyne",
		Backend: domain.BackendClaudeCode, WorkItemID: item, Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping, StartedAt: started, UpdatedAt: started}
}

// whole is a factory every source of which reads whole: one supervisor and a
// scheduler on the current build, on a search path that finds the provider's
// command, naming a configuration file that exists.
func whole(t *testing.T) (Observer, fakeFiles) {
	t.Helper()
	keeper, err := runstate.NewHealthObservationStore(t.TempDir(), product)
	if err != nil {
		t.Fatal(err)
	}
	files := fakeFiles{"/bin/claude": true, "/repo/.yoyodyne/config.yaml": true}
	started := observedAt.Add(-time.Hour)
	readers := fakeReaders{running: []runstate.ConfigReader{
		{Service: runstate.ConfigReaderSupervisor, PID: 100, Build: "build-current", StartedAt: started, ConfigPath: "/repo/.yoyodyne/config.yaml", SearchPath: "/bin"},
		{Service: "scheduler", PID: 200, Build: "build-current", StartedAt: started, ConfigPath: "/repo/.yoyodyne/config.yaml", SearchPath: "/bin"},
	}}
	standing := readmodel.Standing{Services: &readmodel.Services{SupervisorRunning: true, Recorded: true, Record: runstate.Supervision{
		PID: 100, Deployed: "build-current", Children: []runstate.SupervisedChild{{Service: config.ServiceScheduler, State: runstate.ChildRunning, PID: 200}},
	}}}
	observer := Observer{
		Product:          product,
		Sources:          readmodel.Sources{Runs: fakeRuns{}, ConfigReaders: readers, Passes: fakePasses{}},
		Standing:         func(context.Context) readmodel.Standing { return standing },
		ProviderCommands: []string{"claude"},
		Keeper:           keeper,
		Now:              func() time.Time { return observedAt },
		Stat:             files.stat,
	}
	return observer, files
}

func withStanding(o Observer, standing readmodel.Standing) Observer {
	services := o.standing(context.Background()).Services
	if standing.Services == nil {
		standing.Services = services
	}
	o.Standing = func(context.Context) readmodel.Standing { return standing }
	return o
}

func find(t *testing.T, observation Observation, key string) Anomaly {
	t.Helper()
	for _, anomaly := range observation.Anomalies {
		if anomaly.Key == key {
			return anomaly
		}
	}
	t.Fatalf("no condition %s in %+v", key, observation.Anomalies)
	return Anomaly{}
}

// A factory read whole with nothing wrong is said to be so, and only then.
func TestAWholeReadingWithNothingFoundSaysSo(t *testing.T) {
	t.Parallel()

	observer, _ := whole(t)
	observation := observer.Observe(context.Background(), Caller{Role: domain.RoleDevelopmentManager}, nil)
	if !observation.Complete || len(observation.Failures) != 0 || observation.Totals.Conditions != 0 {
		t.Fatalf("observation = %+v, want a whole reading with nothing found", observation)
	}
	if observation.Says != "Every source was read whole, and no condition was found." {
		t.Fatalf("says %q", observation.Says)
	}
	if !strings.HasPrefix(observation.ID, "health-20261016T150000Z-") || !observation.ObservedAt.Equal(observedAt) {
		t.Fatalf("observation is %s at %s", observation.ID, observation.ObservedAt)
	}
	names := map[string]bool{}
	for _, source := range observation.Sources {
		names[source.Name] = source.Complete
	}
	for _, name := range []string{SourceRuns, SourceHeld, SourceFactory, SourceServices, SourceEnvironment, SourcePasses} {
		if complete, read := names[name]; !read || !complete {
			t.Fatalf("source %q was not reported read whole: %+v", name, observation.Sources)
		}
	}
}

// A run in flight with no process behind it is an actionable condition the
// development manager owns; a run paused on work its item waits on, and one
// waiting out a usage limit, are legitimate waits naming what releases them;
// a run whose worker is live is no condition at all.
func TestARunWithNoWorkerIsActionableAndAWaitIsNot(t *testing.T) {
	t.Parallel()

	observer, _ := whole(t)
	workerless, paused, limited, live := run("run-dead", "yoyodyne-a"), run("run-paused", "yoyodyne-b"), run("run-limited", "yoyodyne-c"), run("run-live", "yoyodyne-d")
	paused.DependencyPause = &runstate.DependencyPause{Blockers: []string{"yoyodyne-z"}}
	resets := observedAt.Add(time.Hour)
	limited.UsageLimitResetsAt, limited.PauseCause = &resets, runstate.PauseUsageLimit
	observer.Sources.Runs = fakeRuns{states: []runstate.State{workerless, paused, limited, live}, absent: map[string]bool{"run-dead": true, "run-paused": true, "run-limited": true}}
	observation := observer.Observe(context.Background(), Caller{Role: domain.RoleDevelopmentManager}, nil)

	dead := find(t, observation, "yoyodyne/run-dead/no-process")
	if dead.Classification != ClassWorkerless || !dead.Actionable || dead.Owner != readmodel.MoverDevelopmentManager || !strings.Contains(dead.Says, "pid 4242") || dead.Needs == "" {
		t.Fatalf("workerless run = %+v", dead)
	}
	wait := find(t, observation, "yoyodyne/run-paused/dependency-wait")
	if wait.Classification != ClassDependencyWait || wait.Actionable || !strings.Contains(wait.Needs, "yoyodyne-z") {
		t.Fatalf("dependency wait = %+v", wait)
	}
	usage := find(t, observation, "yoyodyne/run-limited/wait")
	if usage.Classification != ClassWait || usage.Actionable {
		t.Fatalf("usage-limit wait = %+v", usage)
	}
	if observation.Totals != (Totals{Conditions: 3, Actionable: 1, Waits: 2, Services: 2}) {
		t.Fatalf("totals = %+v", observation.Totals)
	}
	if observation.Anomalies[0].Key != dead.Key {
		t.Fatalf("the actionable condition is not listed first: %+v", observation.Anomalies)
	}
	for _, anomaly := range observation.Anomalies {
		if anomaly.Identity.Run == "run-live" {
			t.Fatalf("a run with a live worker was listed: %+v", anomaly)
		}
	}
}

// A newer run of an item with an older stoppage is a condition of its own: its
// identity names its own run, so it is first seen now and has recurred once,
// while the older stoppage keeps the history the pass records give it.
func TestANewerRunOfAnItemWithAnOlderStoppageHasADistinctIdentity(t *testing.T) {
	t.Parallel()

	observer, _ := whole(t)
	observer.Sources.Runs = fakeRuns{states: []runstate.State{run("run-new", "yoyodyne-x")}, absent: map[string]bool{"run-new": true}}
	observer = withStanding(observer, readmodel.Standing{NotStartable: []readmodel.Refused{
		{WorkItemID: "yoyodyne-x", Title: "the item", Kind: backlog.HeldForAPerson, RunID: "run-old", Reason: "run run-old stopped; the development manager has not decided"},
	}})
	earlier := observedAt.Add(-3 * time.Hour)
	observer.Sources.Passes = fakePasses{passes: []runstate.Sweep{
		{Task: "triage", Role: domain.RoleDevelopmentManager, StartedAt: earlier, EndedAt: earlier,
			Health: &runstate.SweepHealth{Observation: "health-20261016T120000Z-00000000", ObservedAt: earlier, Delivered: []string{"yoyodyne/run-old/held-after-stoppage"}}},
	}}
	observation := observer.Observe(context.Background(), Caller{Role: domain.RoleDevelopmentManager}, nil)

	old := find(t, observation, "yoyodyne/run-old/held-after-stoppage")
	newer := find(t, observation, "yoyodyne/run-new/no-process")
	if old.Key == newer.Key || old.Identity.Run == newer.Identity.Run {
		t.Fatalf("the two runs of one item share an identity: %+v and %+v", old, newer)
	}
	if !old.FirstSeen.Equal(earlier) || old.Recurrence != 2 || !old.LastSeen.Equal(observedAt) {
		t.Fatalf("the older stoppage's history = first %s, recurrence %d, last %s", old.FirstSeen, old.Recurrence, old.LastSeen)
	}
	if !newer.FirstSeen.Equal(observedAt) || newer.Recurrence != 1 {
		t.Fatalf("the newer run inherited the stoppage's history: first %s, recurrence %d", newer.FirstSeen, newer.Recurrence)
	}
	if newer.WorkItem != old.WorkItem {
		t.Fatalf("both conditions are about yoyodyne-x: %q and %q", newer.WorkItem, old.WorkItem)
	}
}

// A source that could not be read is named, and its failure said, and the
// observation is partial rather than empty or healthy, though it found nothing
// in what it could read.
func TestAFailedSourceIsShownAndNeverReadAsHealthy(t *testing.T) {
	t.Parallel()

	observer, _ := whole(t)
	observer.Sources.Runs = fakeRuns{err: errors.New("runs directory unreadable")}
	observation := observer.Observe(context.Background(), Caller{Role: domain.RoleDevelopmentManager}, nil)
	if observation.Complete || len(observation.Failures) != 1 || !strings.Contains(observation.Failures[0], "runs directory unreadable") {
		t.Fatalf("observation = %+v, want the runs' failure said", observation)
	}
	if !strings.HasPrefix(observation.Says, "Partial: runs in flight could not be read whole") || strings.Contains(observation.Says, "no condition was found") {
		t.Fatalf("a partial observation says %q", observation.Says)
	}
	for _, source := range observation.Sources {
		if source.Name == SourceRuns && (source.Complete || source.Problem == "") {
			t.Fatalf("the runs source = %+v", source)
		}
	}
	section := Section(observation)
	if !strings.Contains(section, "What could not be read:") || !strings.Contains(section, "runs directory unreadable") {
		t.Fatalf("the pass's section hides the failure:\n%s", section)
	}

	// A run whose worker could not be asked is uncertain, and the run source is
	// partial with it.
	observer.Sources.Runs = fakeRuns{states: []runstate.State{run("run-q", "yoyodyne-q")}, unreadable: map[string]bool{"run-q": true}}
	observation = observer.Observe(context.Background(), Caller{Role: domain.RoleDevelopmentManager}, nil)
	if uncertain := find(t, observation, "yoyodyne/run-q/process-unknown"); uncertain.Classification != ClassWorkerUncertain || observation.Complete {
		t.Fatalf("an unreadable worker = %+v, complete %t", uncertain, observation.Complete)
	}

	// Nothing wired is a source not read, never an empty one.
	observer.Sources = readmodel.Sources{}
	observation = observer.Observe(context.Background(), Caller{Role: domain.RoleDevelopmentManager}, nil)
	if observation.Complete || len(observation.Failures) < 3 {
		t.Fatalf("an observation over nothing = %+v", observation)
	}
}

// Each supervised service is reported with every running copy's build and
// start time and whether that build is current; an older copy left running
// beside the supervisor's is a condition, and so is each copy's build.
func TestAnOlderCopyLeftRunningIsReported(t *testing.T) {
	t.Parallel()

	observer, _ := whole(t)
	readers := observer.Sources.ConfigReaders.(fakeReaders)
	readers.running = append(readers.running, runstate.ConfigReader{Service: "scheduler", PID: 150, Build: "build-old", StartedAt: observedAt.Add(-48 * time.Hour), ConfigPath: "/repo/.yoyodyne/config.yaml", SearchPath: "/bin"})
	observer.Sources.ConfigReaders = readers
	observation := observer.Observe(context.Background(), Caller{Role: domain.RoleDevelopmentManager}, nil)

	var scheduler Service
	for _, service := range observation.Services {
		if service.Name == "scheduler" {
			scheduler = service
		}
	}
	if len(scheduler.Copies) != 2 || scheduler.Copies[0].PID != 150 || scheduler.Copies[0].BuildIs != "old" || scheduler.Copies[0].Supervised ||
		scheduler.Copies[1].PID != 200 || scheduler.Copies[1].BuildIs != "current" || !scheduler.Copies[1].Supervised || scheduler.State != string(runstate.ChildRunning) {
		t.Fatalf("scheduler = %+v", scheduler)
	}
	old := find(t, observation, fmt.Sprintf("yoyodyne/-/old-build:scheduler:pid-150-%d", observedAt.Add(-48*time.Hour).Unix()))
	if old.Classification != ClassOldBuild || !old.Actionable || !strings.Contains(old.Says, "build-old") || !strings.Contains(old.Needs, "did not start") {
		t.Fatalf("old copy = %+v", old)
	}
	if observation.Totals.Actionable != 1 || !observation.Complete {
		t.Fatalf("observation = %+v", observation)
	}
}

// A part that launches roles is checked for every provider command on the
// search path it runs with, not on anybody's shell; a part that recorded no
// search path makes the reading partial rather than passing it.
func TestAProviderCommandMissingFromAServicesSearchPathIsReported(t *testing.T) {
	t.Parallel()

	observer, _ := whole(t)
	observer.ProviderCommands = []string{"claude", "codex"}
	readers := observer.Sources.ConfigReaders.(fakeReaders)
	readers.running[1].SearchPath = "/usr/bin:/bin"
	observer.Sources.ConfigReaders = readers
	observation := observer.Observe(context.Background(), Caller{Role: domain.RoleDevelopmentManager}, nil)

	missing := find(t, observation, "yoyodyne/-/missing-command:scheduler:codex")
	if missing.Classification != ClassMissingCommand || !missing.Actionable || !strings.Contains(missing.Says, "codex") {
		t.Fatalf("missing command = %+v", missing)
	}
	for _, service := range observation.Services {
		if service.Name != "scheduler" {
			if len(service.Copies) > 0 && len(service.Copies[0].Commands) > 0 {
				t.Fatalf("a part that launches no role was checked for provider commands: %+v", service)
			}
			continue
		}
		commands := service.Copies[0].Commands
		want := []Command{{Name: "claude", Found: true, Path: "/bin/claude"}, {Name: "codex"}}
		if service.Copies[0].SearchPath != "/usr/bin:/bin" || !reflect.DeepEqual(commands, want) {
			t.Fatalf("scheduler copy = %+v", service.Copies[0])
		}
	}

	readers.running[1].SearchPath = ""
	observer.Sources.ConfigReaders = readers
	observation = observer.Observe(context.Background(), Caller{Role: domain.RoleDevelopmentManager}, nil)
	if observation.Complete || !strings.Contains(strings.Join(observation.Failures, "\n"), "records no search path") {
		t.Fatalf("a copy with no recorded search path read whole: %+v", observation)
	}
}

// The configuration file a running part names is looked for, and one that is
// not there is a condition.
func TestAConfigurationFileAServiceNamesThatIsMissingIsReported(t *testing.T) {
	t.Parallel()

	observer, files := whole(t)
	delete(files, "/repo/.yoyodyne/config.yaml")
	observation := observer.Observe(context.Background(), Caller{Role: domain.RoleDevelopmentManager}, nil)
	missing := find(t, observation, "yoyodyne/-/missing-config:scheduler:/repo/.yoyodyne/config.yaml")
	if missing.Classification != ClassMissingConfig {
		t.Fatalf("missing configuration = %+v", missing)
	}
	for _, service := range observation.Services {
		for _, copied := range service.Copies {
			if copied.ConfigExists || copied.ConfigFile != "/repo/.yoyodyne/config.yaml" {
				t.Fatalf("copy = %+v", copied)
			}
		}
	}
}

// A maintenance failure since the caller's last pass is listed with whether a
// finding has carried it to a role; one that none has is a condition, and the
// ones a raised finding carries are not.
func TestAnUndeliveredMaintenanceFailureIsReported(t *testing.T) {
	t.Parallel()

	observer, _ := whole(t)
	failed := func(at time.Time) runstate.Sweep {
		return runstate.Sweep{Task: config.MaintenanceTaskName, StartedAt: at, EndedAt: at.Add(time.Minute), Problem: "reconcile failed",
			Steps: []runstate.SweepStep{{Name: "reconcile", Outcome: runstate.StepFailed, Detail: "bd timed out"}}}
	}
	ran := func(at time.Time) runstate.Sweep {
		return runstate.Sweep{Task: config.MaintenanceTaskName, StartedAt: at, EndedAt: at.Add(time.Minute), Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "ran"},
			Steps: []runstate.SweepStep{{Name: "reconcile", Outcome: runstate.StepRan}}}
	}
	lastPass := observedAt.Add(-5 * time.Hour)
	passes := []runstate.Sweep{
		{Task: "triage", Role: domain.RoleDevelopmentManager, StartedAt: lastPass, EndedAt: lastPass, Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "done"}},
		// A run of three, raised as a finding, then cleared.
		failed(observedAt.Add(-4 * time.Hour)), failed(observedAt.Add(-3*time.Hour - 50*time.Minute)), failed(observedAt.Add(-3*time.Hour - 40*time.Minute)),
		ran(observedAt.Add(-3 * time.Hour)),
		// One failure since, which no finding carries.
		failed(observedAt.Add(-10 * time.Minute)),
		// And one before the caller's last pass, which is not since it.
		failed(observedAt.Add(-6 * time.Hour)),
	}
	observer.Sources.Passes = fakePasses{passes: passes}
	observation := observer.Observe(context.Background(), Caller{Role: domain.RoleDevelopmentManager}, nil)

	if observation.MaintenanceSince == nil || !observation.MaintenanceSince.Equal(lastPass) || len(observation.Maintenance) != 4 {
		t.Fatalf("maintenance since %v = %+v", observation.MaintenanceSince, observation.Maintenance)
	}
	for i, delivered := range []bool{true, true, true, false} {
		if observation.Maintenance[i].Delivered != delivered || !strings.Contains(observation.Maintenance[i].Problem, "bd timed out") {
			t.Fatalf("maintenance failure %d = %+v, want delivered %t", i, observation.Maintenance[i], delivered)
		}
	}
	key := "yoyodyne/-/maintenance-failure:" + observedAt.Add(-10*time.Minute).Format(time.RFC3339Nano)
	if undelivered := find(t, observation, key); undelivered.Classification != ClassUndeliveredMaintenance || !undelivered.Actionable {
		t.Fatalf("undelivered failure = %+v", undelivered)
	}
	if observation.Totals.Actionable != 1 {
		t.Fatalf("totals = %+v; only the undelivered failure is a condition", observation.Totals)
	}
}

// A result larger than the rendering bound carries exact totals, the
// identities it left out, and a cursor; health.follow returns the rest part by
// part; and an observation older than fifteen minutes is refused.
func TestARenderingBoundIsFollowedToTheEndAndAnOldObservationIsRefused(t *testing.T) {
	t.Parallel()

	observer, _ := whole(t)
	var states []runstate.State
	absent := map[string]bool{}
	for i := range 45 {
		id := fmt.Sprintf("run-%02d", i)
		states = append(states, run(id, "yoyodyne-"+id))
		absent[id] = true
	}
	observer.Sources.Runs = fakeRuns{states: states, absent: absent}
	observation := observer.Observe(context.Background(), Caller{Role: domain.RoleDevelopmentManager}, nil)
	if observation.Totals.Conditions != 45 || len(observation.Anomalies) != MaxRendered || len(observation.Omitted) != 25 || observation.Cursor != "20" || observation.KeepProblem != "" {
		t.Fatalf("first part: totals %+v, %d shown, %d omitted, cursor %q, keep %q", observation.Totals, len(observation.Anomalies), len(observation.Omitted), observation.Cursor, observation.KeepProblem)
	}
	if observation.Omitted[0] != "yoyodyne/run-20/no-process" {
		t.Fatalf("omitted = %v", observation.Omitted)
	}
	section := Section(observation)
	if !strings.Contains(section, "20 of the 45 conditions") || !strings.Contains(section, "cursor 20") {
		t.Fatalf("the section does not say how to follow:\n%s", section)
	}

	seen := len(observation.Anomalies)
	cursor := observation.Cursor
	for cursor != "" {
		part, err := observer.Follow(observation.ID, cursor)
		if err != nil {
			t.Fatalf("Follow(%s) error = %v", cursor, err)
		}
		if part.Totals != observation.Totals || part.Anomalies[0].Key != fmt.Sprintf("yoyodyne/run-%02d/no-process", seen) {
			t.Fatalf("part from %s = %+v", cursor, part)
		}
		seen += len(part.Anomalies)
		if len(part.Omitted) != 45-seen {
			t.Fatalf("part from %s leaves %d out, want %d", cursor, len(part.Omitted), 45-seen)
		}
		cursor = part.Cursor
	}
	if seen != 45 {
		t.Fatalf("following handed back %d conditions, want 45", seen)
	}
	if _, err := observer.Follow(observation.ID, "60"); err == nil {
		t.Fatal("a cursor past the end was followed")
	}
	if _, err := observer.Follow("health-20261016T150000Z-ffffffff", "20"); !errors.Is(err, runstate.ErrHealthObservationUnknown) {
		t.Fatalf("an unknown observation: %v", err)
	}

	observer.Now = func() time.Time { return observedAt.Add(MaxAge + time.Second) }
	if _, err := observer.Follow(observation.ID, "20"); err == nil || !strings.Contains(err.Error(), "observe again") {
		t.Fatalf("an observation older than %s was followed: %v", MaxAge, err)
	}
}

// The development manager and the factory-flow program manager observing the
// same state are handed the same conditions, classified the same way, over the
// same sources read as completely.
func TestBothManagersReceiveTheSameClassificationAndCoverage(t *testing.T) {
	t.Parallel()

	observer, _ := whole(t)
	paused := run("run-paused", "yoyodyne-b")
	paused.DependencyPause = &runstate.DependencyPause{Blockers: []string{"yoyodyne-z"}}
	observer.Sources.Runs = fakeRuns{states: []runstate.State{run("run-dead", "yoyodyne-a"), paused}, absent: map[string]bool{"run-dead": true}, unreadable: map[string]bool{"run-paused": true}}
	observer = withStanding(observer, readmodel.Standing{NotStartableProblem: "the tracker did not answer"})
	manager := observer.Observe(context.Background(), Caller{Role: domain.RoleDevelopmentManager}, nil)
	flow := observer.Observe(context.Background(), Caller{Role: domain.RoleProgramManager, Agent: "factory-flow"}, nil)
	if manager.ID == flow.ID {
		t.Fatal("two observations share an identifier")
	}
	if !reflect.DeepEqual(manager.Anomalies, flow.Anomalies) || !reflect.DeepEqual(manager.Sources, flow.Sources) ||
		!reflect.DeepEqual(manager.Failures, flow.Failures) || manager.Complete != flow.Complete || manager.Says != flow.Says {
		t.Fatalf("the managers were handed different observations:\n%+v\n%+v", manager, flow)
	}
	if manager.Complete || len(manager.Anomalies) != 2 {
		t.Fatalf("observation = %+v", manager)
	}
}

// An observation restricted to runs carries only those runs' conditions.
func TestAnObservationRestrictedToRunsCarriesOnlyThem(t *testing.T) {
	t.Parallel()

	observer, files := whole(t)
	delete(files, "/repo/.yoyodyne/config.yaml")
	observer.Sources.Runs = fakeRuns{states: []runstate.State{run("run-a", "yoyodyne-a"), run("run-b", "yoyodyne-b")}, absent: map[string]bool{"run-a": true, "run-b": true}}
	observation := observer.Observe(context.Background(), Caller{Role: domain.RoleDevelopmentManager}, []string{"run-b"})
	if len(observation.Anomalies) != 1 || observation.Anomalies[0].Key != "yoyodyne/run-b/no-process" || !reflect.DeepEqual(observation.RestrictedTo, []string{"run-b"}) {
		t.Fatalf("restricted observation = %+v", observation.Anomalies)
	}
}

// What a pass's record keeps is every condition the observation found, shown
// or left out, in its order.
func TestThePassRecordKeepsEveryIdentityDelivered(t *testing.T) {
	t.Parallel()

	observer, _ := whole(t)
	var states []runstate.State
	absent := map[string]bool{}
	for i := range 25 {
		id := fmt.Sprintf("run-%02d", i)
		states = append(states, run(id, "yoyodyne-"+id))
		absent[id] = true
	}
	observer.Sources.Runs = fakeRuns{states: states, absent: absent}
	section, record := observer.Deliver(context.Background(), Caller{Role: domain.RoleDevelopmentManager})
	if record == nil || len(record.Delivered) != 25 || record.Delivered[24] != "yoyodyne/run-24/no-process" || !record.Complete || record.Validate() != nil {
		t.Fatalf("record = %+v", record)
	}
	if !strings.Contains(section, record.Observation) || !strings.Contains(section, "not from the events since your last pass") {
		t.Fatalf("section = %s", section)
	}
}

// The request block refuses what it cannot answer before anything is read.
func TestTheRequestBlockIsValidatedWhole(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{
		`{"requests":[{"action":"observe","observation":"health-20261016T150000Z-1a2b3c4d"}]}`,
		`{"requests":[{"action":"follow","observation":"health-20261016T150000Z-1a2b3c4d","cursor":"0"}]}`,
		`{"requests":[{"action":"follow","observation":"../../etc","cursor":"20"}]}`,
		`{"requests":[{"action":"observe","runs":["../run"]}]}`,
		`{"requests":[{"action":"heal"}]}`,
		`{"requests":[{"action":"observe","extra":true}]}`,
		`{"requests":[]}`,
	} {
		if _, err := Decode(bad); err == nil {
			t.Errorf("Decode(%s) accepted it", bad)
		}
	}
	requests, err := Decode(`{"requests":[{"action":"observe","product":"yoyodyne","runs":["run-1"]},{"action":"follow","observation":"health-20261016T150000Z-1a2b3c4d","cursor":"20"}]}`)
	if err != nil || len(requests) != 2 || requests[1].Tool() != "health.follow" || requests[0].Tool() != "health.observe" {
		t.Fatalf("Decode() = %+v, %v", requests, err)
	}
}

// Over the real run store, an observation delivered to a pass is taken when
// the pass is taken: a run recorded in flight after the observer was built,
// with no process behind it, is in the section the pass is handed, with no
// event read and nothing cached in between.
func TestAPassIsHandedTheWorkerlessRunsTheStoreHoldsWhenItIsTaken(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runs, err := runstate.NewStore(root, product)
	if err != nil {
		t.Fatal(err)
	}
	sweeps, err := runstate.NewSweepStore(root, product)
	if err != nil {
		t.Fatal(err)
	}
	observer, _ := whole(t)
	observer.Sources.Runs, observer.Sources.Passes = runs, sweeps
	before, _ := observer.Deliver(context.Background(), Caller{Role: domain.RoleProgramManager, Agent: "flow-watcher"})
	if strings.Contains(before, "run-") {
		t.Fatalf("an empty store was delivered runs:\n%s", before)
	}

	id, err := runstate.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	state := run(id, "yoyodyne-late")
	if err := runs.Create(state); err != nil {
		t.Fatal(err)
	}
	section, record := observer.Deliver(context.Background(), Caller{Role: domain.RoleProgramManager, Agent: "flow-watcher"})
	key := "yoyodyne/" + id + "/no-process"
	if !strings.Contains(section, key) || !strings.Contains(section, `"classification":"no process behind the run"`) || record == nil || len(record.Delivered) != 1 || record.Delivered[0] != key {
		t.Fatalf("the pass was not handed the workerless run %s:\n%s\nrecord %+v", id, section, record)
	}
}

// A run whose developer slot the harness freed for want of a live worker is
// put in front of the development manager as its own condition, saying what
// the release did, and the observation carries what holds each developer slot
// as the standing read it.
func TestAReleasedSlotIsTheDevelopmentManagersAndTheSlotHoldersAreCarried(t *testing.T) {
	t.Parallel()

	observer, _ := whole(t)
	released := run("run-released", "yoyodyne-a")
	released.Worker = &runstate.WorkerIdentity{PID: 4242, RunID: "run-released", Attempt: 1}
	released.SlotRelease = &runstate.SlotRelease{At: observedAt, Slot: 3, WorkerAttempt: 1, Says: "The harness freed developer slot 3."}
	observer.Sources.Runs = fakeRuns{states: []runstate.State{released}, absent: map[string]bool{"run-released": true}}
	holders := []runstate.SlotHolder{{Slot: 3, RunID: "run-released", WorkItemID: "yoyodyne-a", Since: observedAt.Add(-time.Hour), Says: "not counted: The harness freed developer slot 3."}}
	observer = withStanding(observer, readmodel.Standing{SlotHolders: holders})
	observation := observer.Observe(context.Background(), Caller{Role: domain.RoleDevelopmentManager}, nil)

	freed := find(t, observation, "yoyodyne/run-released/slot-released")
	if freed.Classification != ClassSlotReleased || !freed.Actionable || freed.Owner != readmodel.MoverDevelopmentManager ||
		!strings.Contains(freed.Says, "freed developer slot 3") || freed.Needs == "" {
		t.Fatalf("released run = %+v", freed)
	}
	if len(observation.Slots) != 1 || observation.Slots[0].RunID != "run-released" || observation.Slots[0].Since.IsZero() {
		t.Fatalf("Slots = %+v, want the slot holder the standing read", observation.Slots)
	}
	if !strings.Contains(Section(observation), `"developer_slots"`) {
		t.Fatal("the pass's health section does not carry the developer slots")
	}
}

// trackerOf answers each status listing with the items a test filed under it,
// a listing of no status with every item, and the open ones as ready, as bd
// does for work nothing blocks.
type trackerOf map[string][]beads.WorkItem

func (t trackerOf) List(_ context.Context, status string) ([]beads.WorkItem, error) {
	if status != "" {
		return t[status], nil
	}
	var every []beads.WorkItem
	for _, items := range t {
		every = append(every, items...)
	}
	return every, nil
}

func (t trackerOf) Ready(context.Context) ([]beads.WorkItem, error) { return t["open"], nil }

// stoppedRuns is the harness's record of the runs it stopped, and no
// escalations.
type stoppedRuns []runstate.State

func (s stoppedRuns) Recorded() ([]runstate.State, error)       { return s, nil }
func (s stoppedRuns) Escalated() ([]runstate.Escalation, error) { return nil, nil }

// Every item the harness holds after a stopped run is listed with its owner,
// whatever the tracker says of it. A run claims its item and nothing reopens it
// when the run stops, so a held item can stay marked in progress and assigned to
// whoever claimed it; the admitted queue is read from open and blocked work
// alone, which is how such items went missing from the list. The held items are
// built from tracker items and stopped runs and read through the read model
// itself, so a filter anywhere between the two drops one and fails here.
func TestEveryHeldItemIsListedWhateverItsStatusOrAssignee(t *testing.T) {
	t.Parallel()

	stopped := func(id, item string) runstate.State {
		state := run(id, item)
		completed := observedAt.Add(-time.Hour)
		state.Status, state.CompletedAt, state.UpdatedAt = runstate.StatusFailed, &completed, completed
		state.Branch, state.WorktreePath = "yoyodyne/"+item+"/"+id, "/state/worktrees/"+id
		state.Blocker = "Yoyodyne stopped this item: a configured check still failed after every permitted attempt."
		return state
	}
	tracker := trackerOf{
		"open": {{ID: "yoyodyne-open", Title: "held and open", Status: "open"}},
		"in_progress": {
			{ID: "yoyodyne-claimed", Title: "held and marked in progress", Status: "in_progress"},
			{ID: "yoyodyne-assigned", Title: "held, in progress, and assigned to a person", Status: "in_progress", Assignee: "Mason Bryant"},
			// Carried by a run in flight, so it is on the running line and not held.
			{ID: "yoyodyne-carried", Title: "being worked on", Status: "in_progress", Assignee: "Mason Bryant"},
			// In progress with nothing holding it.
			{ID: "yoyodyne-free", Title: "claimed and not held", Status: "in_progress"},
		},
	}
	stoppages := stoppedRuns{stopped("run-open", "yoyodyne-open"), stopped("run-claimed", "yoyodyne-claimed"),
		stopped("run-assigned", "yoyodyne-assigned"), stopped("run-carried-old", "yoyodyne-carried")}
	carrying := run("run-carrying", "yoyodyne-carried")

	observer, _ := whole(t)
	observer.Sources.Runs = fakeRuns{states: []runstate.State{carrying}}
	services := observer.standing(context.Background()).Services
	sources := readmodel.Sources{Runs: fakeRuns{states: []runstate.State{carrying}}, Stoppages: stoppages, Tracker: tracker,
		Capacity: 2, Now: func() time.Time { return observedAt }}
	observer.Standing = func(ctx context.Context) readmodel.Standing {
		standing := readmodel.ReadStanding(ctx, sources)
		standing.Services = services
		return standing
	}
	observation := observer.Observe(context.Background(), Caller{Role: domain.RoleDevelopmentManager}, nil)

	for run, item := range map[string]string{"run-open": "yoyodyne-open", "run-claimed": "yoyodyne-claimed", "run-assigned": "yoyodyne-assigned"} {
		var listed *Anomaly
		for i, anomaly := range observation.Anomalies {
			if anomaly.Key == "yoyodyne/"+run+"/held-after-stoppage" {
				listed = &observation.Anomalies[i]
			}
		}
		if listed == nil {
			t.Fatalf("held item %s (run %s) is missing from the health check: %+v", item, run, observation.Anomalies)
		}
		if listed.Classification != ClassHeldStoppage || listed.WorkItem != item || listed.Owner != ownership.MoverDevelopmentManager || listed.Needs == "" {
			t.Fatalf("held item %s = %+v", item, *listed)
		}
		if marked := strings.Contains(listed.Says, "the tracker still marks it in progress"); marked != (item != "yoyodyne-open") {
			t.Fatalf("held item %s says %q", item, listed.Says)
		}
	}
	for _, anomaly := range observation.Anomalies {
		if anomaly.WorkItem == "yoyodyne-free" || (anomaly.WorkItem == "yoyodyne-carried" && anomaly.Classification == ClassHeldStoppage) {
			t.Fatalf("an item nothing holds was listed as held: %+v", anomaly)
		}
	}
}
