//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package orchestrator

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// runHolderRootEnv and runHolderRunEnv tell TestRunLeaseHolderProcess which run
// to hold, and their absence tells it it is an ordinary test with nothing to do.
const (
	runHolderRootEnv = "YOYODYNE_TEST_RUN_HOLDER_ROOT"
	runHolderRunEnv  = "YOYODYNE_TEST_RUN_HOLDER_RUN"
)

// TestRunLeaseHolderProcess is not a test. It is the process the test below
// kills: it takes one run's lease the way a process working on the run does,
// says so, and waits to be killed. The wait carries its own bound, so a parent
// that dies before killing it does not leave it running.
func TestRunLeaseHolderProcess(t *testing.T) {
	root, runID := os.Getenv(runHolderRootEnv), os.Getenv(runHolderRunEnv)
	if root == "" || runID == "" {
		return
	}
	store, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		fmt.Println("store:", err)
		os.Exit(2)
	}
	_, lease, err := store.AdoptRun(context.Background(), runID)
	if err != nil {
		fmt.Println("adopt:", err)
		os.Exit(2)
	}
	// Collection must not release the lease while this process is still alive.
	runtime.GC()
	fmt.Println("run lease held")
	time.Sleep(60 * time.Second)
	runtime.KeepAlive(lease)
	os.Exit(0)
}

func awaitHeldRunLease(output io.Reader) error {
	scanner := bufio.NewScanner(output)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), "run lease held") {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return errors.New("the holder exited without taking the run's lease")
}

// A paused run whose process is killed, and a stop written for it afterwards,
// is the shape run-3b94404c was found in on 2026-09-27: parked on a dependency,
// its process gone since the evening before, a stop the development manager
// decided standing unread because only a live process reads one — and developer
// slot 1 held for twenty hours. The sweep honours the stop in the dead process's
// place, at once and without waiting out the grace: the run ends cancelled with
// its change preserved, the item is told, the decided stoppage is docketed as
// settled, and the slot is free.
func TestTheSweepHonoursAStopOnAPausedRunWhoseProcessWasKilled(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if request.Role != domain.RoleDeveloper {
			return nil
		}
		tracker.Item.Dependencies = blockedBy("yoyodyne-blocker")
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	paused, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || !paused.Paused || paused.PausedByDependency == nil {
		t.Fatalf("Run() = %#v, %v; want a run paused on the work its item waits on", paused, err)
	}
	tracker.Item.Status = "in_progress"

	// A process takes the paused run up, as a `yoyo run` continuing it would.
	stateRoot := stateRootOf(store.Root())
	child := exec.Command(os.Args[0], "-test.run=^TestRunLeaseHolderProcess$")
	child.Env = append(os.Environ(), runHolderRootEnv+"="+stateRoot, runHolderRunEnv+"="+paused.RunID)
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe() error = %v", err)
	}
	if err := child.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	}()
	if err := awaitHeldRunLease(output); err != nil {
		t.Fatalf("the holder never took the run's lease: %v", err)
	}

	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	reconciler := Reconciler{
		Tracker:   tracker,
		Worktrees: newObserver(t, repository, worktreeRoot),
		Store:     store,
		Docket:    docketerOverStore(docket, store, pipeline.Config),
	}

	// While it lives, the run is its holder's: the sweep leaves it, and a reading
	// finds the process behind it.
	held, err := reconciler.Reconcile(context.Background())
	if err != nil || len(held) != 1 || held[0].Action != ActionHeld {
		t.Fatalf("Reconcile() with a live holder = %#v, %v; want the run left to it", held, err)
	}
	recorded, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if presence, err := store.Presence(recorded, readmodel.DefaultDeadClaimThreshold, time.Now()); err != nil || !presence.Found {
		t.Fatalf("Presence() with a live holder = %#v, %v; want the process found", presence, err)
	}

	// Killed outright: nothing it would have written on the way out is written.
	if err := child.Process.Kill(); err != nil {
		t.Fatalf("Kill() error = %v", err)
	}
	_ = child.Wait()
	presence, err := store.Presence(recorded, readmodel.DefaultDeadClaimThreshold, time.Now())
	if err != nil || presence.Found || !strings.Contains(presence.Says, "has exited") {
		t.Fatalf("Presence() after the kill = %#v, %v; want no process found at once", presence, err)
	}

	// The development manager decides the run is superseded, and the harness
	// writes the stop on her behalf.
	request := runstate.StopRequest{
		SchemaVersion: runstate.StopSchemaVersion,
		ProductID:     "yoyodyne",
		RunID:         paused.RunID,
		WorkItemID:    tracker.Item.ID,
		RequestedAt:   time.Now().UTC(),
		Reason:        "superseded by yoyodyne-other",
		RequestedBy:   "the development manager in conversation chat-test",
		Decision:      runstate.TriageDecisionStop,
	}
	if err := store.RecordStop(request); err != nil {
		t.Fatalf("RecordStop() error = %v", err)
	}

	// No grace is waited out: the clock is the real one, seconds after the park.
	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionCancelled || results[0].Failure != "" || results[0].DocketProblem != "" {
		t.Fatalf("reconciliation = %#v, want the stop honoured as a cancellation", results)
	}
	for _, want := range []string{"the development manager in conversation chat-test stopped this run", "superseded by yoyodyne-other", "the harness's sweep ended it in that process's place"} {
		if !strings.Contains(results[0].Detail, want) {
			t.Fatalf("detail %q does not say %q", results[0].Detail, want)
		}
	}
	settled, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.Status != runstate.StatusCancelled || settled.CompletedAt == nil || settled.DependencyPause != nil {
		t.Fatalf("settled run = %#v, want it cancelled with its park cleared", settled)
	}
	if settled.SettledQuietSince == nil || !settled.SettledQuietSince.Equal(recorded.UpdatedAt) {
		t.Fatalf("settled run quiet since %v, want %s, when its record last moved", settled.SettledQuietSince, recorded.UpdatedAt)
	}
	// The change is where the developer left it.
	if settled.WorktreeRemoved || settled.BranchRemoved {
		t.Fatalf("settled run = %#v, want its branch and worktree kept", settled)
	}
	if _, err := os.Stat(filepath.Join(settled.WorktreePath, "feature.txt")); err != nil {
		t.Fatalf("the paused run's work is not where it was left: %v", err)
	}
	if !strings.Contains(tracker.Notes, "the harness's sweep ended it in that process's place") {
		t.Fatalf("item notes = %q, want the stop recorded on the item", tracker.Notes)
	}
	// The slot is free: the slot and the in-flight guard read this listing.
	incomplete, err := store.Incomplete()
	if err != nil || len(incomplete) != 0 {
		t.Fatalf("Incomplete() = %#v, %v; want the stopped run holding no slot", incomplete, err)
	}
	// Her decision closes the stoppage it made, as it would have had the run
	// stopped itself.
	entries, err := docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 || entries[0].RunID != paused.RunID || entries[0].Class != triage.ClassStoppedRun ||
		entries[0].StopRequested == nil || entries[0].Closed == nil {
		t.Fatalf("docket = %#v, want the decided stop docketed and closed by her decision", entries)
	}

	// Settled once: a second sweep has nothing to do.
	again, err := reconciler.Reconcile(context.Background())
	if err != nil || len(again) != 0 {
		t.Fatalf("second Reconcile() = %#v, %v; want nothing outstanding", again, err)
	}
}

// A run paused on work its item waits on is not a park nothing continues: a
// watching session's pull continues it once that work closes, so the sweep
// leaves it however long the wait lasts rather than settling it after the grace
// and handing the development manager a stoppage nobody caused.
func TestTheSweepLeavesADependencyPausedRunToThePull(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if request.Role != domain.RoleDeveloper {
			return nil
		}
		tracker.Item.Dependencies = blockedBy("yoyodyne-blocker")
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	paused, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || !paused.Paused || paused.PausedByDependency == nil {
		t.Fatalf("Run() = %#v, %v; want a run paused on the work its item waits on", paused, err)
	}
	tracker.Item.Status = "in_progress"
	recorded, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	sweep := Reconciler{
		Tracker:   tracker,
		Worktrees: newObserver(t, repository, worktreeRoot),
		Store:     store,
		Docket:    docketerOverStore(docket, store, pipeline.Config),
		Clock:     &pausingClock{now: recorded.UpdatedAt.Add(24 * time.Hour)},
	}

	results, err := sweep.Reconcile(context.Background())
	if err != nil || len(results) != 1 || results[0].Action != ActionResumable || !strings.Contains(results[0].Detail, "continues it at the first pull after that work closes") {
		t.Fatalf("Reconcile() a day into the wait = %#v, %v; want it resumable, naming the pull as what continues it", results, err)
	}
	left, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if left.Status.Terminal() || left.DependencyPause == nil {
		t.Fatalf("paused run after the sweep = %#v, want it still paused", left)
	}
	entries, err := docket.List()
	if err != nil || len(entries) != 0 {
		t.Fatalf("docket = %#v, %v; want nothing docketed for a wait on other work", entries, err)
	}
}

// parkedRun is a run parked on a dependency with its process gone, over a real
// repository, for a test to rewrite into whatever park it is about.
type parkedRun struct {
	repository, worktreeRoot string
	store                    *runstate.Store
	tracker                  *orchestratortest.Tracker
	docket                   *runstate.DocketStore
	pipeline                 Pipeline
	state                    runstate.State
}

func parkRunOnADependency(t *testing.T) parkedRun {
	t.Helper()
	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if request.Role != domain.RoleDeveloper {
			return nil
		}
		tracker.Item.Dependencies = blockedBy("yoyodyne-blocker")
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	paused, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || !paused.Paused || paused.PausedByDependency == nil {
		t.Fatalf("Run() = %#v, %v; want a run paused on the work its item waits on", paused, err)
	}
	tracker.Item.Status = "in_progress"
	state, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	return parkedRun{repository: repository, worktreeRoot: worktreeRoot, store: store, tracker: tracker, docket: docket, pipeline: pipeline, state: state}
}

// repark rewrites the run's park, as though the run had parked on something else.
func (p parkedRun) repark(t *testing.T, rewrite func(*runstate.State)) runstate.State {
	t.Helper()
	state := p.state
	state.DependencyPause = nil
	rewrite(&state)
	if err := p.store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	return state
}

func (p parkedRun) sweepAt(t *testing.T, at time.Time, holds OperatorHolds) Reconciliation {
	t.Helper()
	reconciler := Reconciler{
		Tracker:   p.tracker,
		Worktrees: newObserver(t, p.repository, p.worktreeRoot),
		Store:     p.store,
		Docket:    docketerOverStore(p.docket, p.store, p.pipeline.Config),
		Clock:     &pausingClock{now: at},
		Holds:     holds,
	}
	results, err := reconciler.Reconcile(context.Background())
	if err != nil || len(results) != 1 {
		t.Fatalf("Reconcile() = %#v, %v; want the one parked run", results, err)
	}
	return results[0]
}

// The operator's pause is theirs while it stands, however long that is: the
// sweep never settles a run parked on it, which is the pause's whole promise.
// Once it is lifted, a run nothing continued is a run with no process behind it
// like any other, and is settled.
func TestTheSweepLeavesARunOnAStandingPauseAndSettlesItOnceLifted(t *testing.T) {
	t.Parallel()

	parked := parkRunOnADependency(t)
	state := parked.repark(t, func(state *runstate.State) {
		heldSince := state.UpdatedAt
		state.OperatorHeldSince = &heldSince
		state.PauseCause = runstate.PauseOperatorHold
	})
	holds := newOperatorHoldStore(t)
	if _, err := holds.Hold(state.UpdatedAt); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	dayLater := state.UpdatedAt.Add(24 * time.Hour)

	standing := parked.sweepAt(t, dayLater, holds)
	if standing.Action != ActionResumable {
		t.Fatalf("sweep under a standing pause = %#v, want the park left alone", standing)
	}
	// A sweep that cannot read the pause does not guess it lifted.
	unwired := parked.sweepAt(t, dayLater, nil)
	if unwired.Action != ActionResumable {
		t.Fatalf("sweep with no pause to read = %#v, want the park left alone", unwired)
	}
	after, err := parked.store.Load(state.RunID)
	if err != nil || after.Status.Terminal() || after.OperatorHeldSince == nil {
		t.Fatalf("parked run after the sweeps = %#v, %v; want it untouched", after, err)
	}

	if _, _, err := holds.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	lifted := parked.sweepAt(t, dayLater, holds)
	if lifted.Action != ActionBlocked || !strings.Contains(lifted.Detail, "operator's pause of all harness activity, which has since been lifted") {
		t.Fatalf("sweep after the pause lifted = %#v, want the run settled as having no process", lifted)
	}
	settled, err := parked.store.Load(state.RunID)
	if err != nil || !settled.Status.Terminal() || settled.OperatorHeldSince != nil || settled.PauseCause != "" {
		t.Fatalf("settled run = %#v, %v; want it terminal with the park cleared", settled, err)
	}
}

// A wait on a provider nobody could reach is measured from the probe it
// recorded, not from its last write: until that probe has passed, nothing has
// failed to serve it.
func TestTheSweepTimesADeadOutageWaitFromItsProbe(t *testing.T) {
	t.Parallel()

	parked := parkRunOnADependency(t)
	probe := parked.state.UpdatedAt.Add(2 * time.Hour)
	state := parked.repark(t, func(state *runstate.State) {
		state.Phase = runstate.PhaseDeveloping
		state.PauseCause = runstate.PauseProviderUnauthenticated
		state.UsageLimitResetsAt = &probe
	})

	// Past the grace from its last write, but not from its probe.
	early := parked.sweepAt(t, state.UpdatedAt.Add(DefaultVanishedGrace+time.Minute), nil)
	if early.Action != ActionResumable {
		t.Fatalf("sweep before the probe's grace = %#v, want the wait left", early)
	}
	inside := parked.sweepAt(t, probe.Add(DefaultVanishedGrace-time.Minute), nil)
	if inside.Action != ActionResumable {
		t.Fatalf("sweep inside the probe's grace = %#v, want the wait left", inside)
	}
	past := parked.sweepAt(t, probe.Add(DefaultVanishedGrace), nil)
	if past.Action != ActionBlocked || !strings.Contains(past.Detail, "nothing asked the provider again at its recorded probe") ||
		!strings.Contains(past.Detail, probe.UTC().Format(time.RFC3339)) {
		t.Fatalf("sweep past the probe's grace = %#v, want the dead wait settled from its probe", past)
	}
	settled, err := parked.store.Load(state.RunID)
	if err != nil || !settled.Status.Terminal() || settled.UsageLimitResetsAt != nil || settled.PauseCause != "" {
		t.Fatalf("settled run = %#v, %v; want it terminal with the wait cleared", settled, err)
	}
}

// A directive park and a tracker park are settled past the grace exactly as a
// dependency park is, each named in the account for what it was.
func TestTheSweepSettlesDirectiveAndTrackerParksNothingContinued(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		park   func(*runstate.State)
		clears func(runstate.State) bool
		says   string
	}{
		{
			name: "directive",
			park: func(state *runstate.State) {
				state.DirectivePause = &runstate.DirectivePause{DirectiveID: "directive-0123", Kind: "question", Unresolved: "which branch does this land on?"}
			},
			clears: func(state runstate.State) bool { return state.DirectivePause == nil },
			says:   "unresolved directive directive-0123",
		},
		{
			name: "tracker",
			park: func(state *runstate.State) {
				state.TrackerPause = &runstate.TrackerPause{Boundary: runstate.RetryDependencyRead, Attempts: 3, WaitedSeconds: 7200, Failure: "bd show timed out"}
			},
			clears: func(state runstate.State) bool { return state.TrackerPause == nil },
			says:   "it parked because",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			parked := parkRunOnADependency(t)
			state := parked.repark(t, test.park)
			inside := parked.sweepAt(t, state.UpdatedAt.Add(DefaultVanishedGrace-time.Minute), nil)
			if inside.Action != ActionResumable || !strings.Contains(inside.Detail, "settles it as a stopped run") {
				t.Fatalf("sweep inside the grace = %#v, want it resumable with the grace said", inside)
			}
			past := parked.sweepAt(t, state.UpdatedAt.Add(DefaultVanishedGrace), nil)
			if past.Action != ActionBlocked || !strings.Contains(past.Detail, test.says) {
				t.Fatalf("sweep past the grace = %#v, want it settled naming %q", past, test.says)
			}
			settled, err := parked.store.Load(state.RunID)
			if err != nil || !settled.Status.Terminal() || !test.clears(settled) {
				t.Fatalf("settled run = %#v, %v; want it terminal with the park cleared", settled, err)
			}
		})
	}
}

// A stop reaches a run only up to its last provider-call boundary, so the sweep
// honours one only there too: a run already promoting, or with its promotion on
// the record, is completed from that promotion rather than recorded as stopped
// with its change already on the target.
func TestTheSweepHonoursAStopOnlyBeforeThePromotion(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		state runstate.State
		want  bool
	}{
		{"parked before its checks", runstate.State{Status: runstate.StatusRunning, Phase: runstate.PhaseChecking}, true},
		{"reserved and not started", runstate.State{Status: runstate.StatusPending}, true},
		{"integrating", runstate.State{Status: runstate.StatusRunning, Phase: runstate.PhaseIntegrating}, false},
		{"promotion recorded", runstate.State{Status: runstate.StatusRunning, Phase: runstate.PhaseReviewing, Integration: &runstate.Integration{}}, false},
		{"already ended", runstate.State{Status: runstate.StatusFailed, Phase: runstate.PhaseChecking}, false},
	} {
		if got := honoursStop(test.state); got != test.want {
			t.Errorf("honoursStop(%s) = %t, want %t", test.name, got, test.want)
		}
	}
}
