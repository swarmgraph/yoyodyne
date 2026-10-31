package orchestrator

// The declarative path, driven against real runs, and the one key that rolls it
// back.
//
// The parity harness beside this file walks transcripts somebody wrote down and
// holds them against the recorded baseline. That answers whether the definitions
// can express what the pipeline did; it cannot answer whether a run happening
// now produces those transitions, because a transcript is written by hand and a
// run is not. This reads the instance back off the store after a real run, so
// what is compared is a sequence the run actually produced against the
// transcript the harness measures that same path by. A divergence here is the
// observation being wrong about the pipeline.
//
// The runs are the baseline's own scenarios rather than copies of them, which is
// what the default buys: a baseline scenario is a run executing the definition,
// so the transcripts below are read off exactly the runs the recorded traces are
// of. What each file measures still differs — a trace is the delivery, and this
// is where the definition sent it.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// legacy is this fixture's pipeline rolled back to the path that executes
// nothing declarative, which is the whole of what a project writes to roll back.
// It keeps somewhere to record instances, so what it measures is the key rather
// than the absence of a store.
func (f *baselineFixture) legacy(t *testing.T, provider recordingBackend, commands []string) Pipeline {
	t.Helper()
	pipeline := f.pipeline(t, provider, commands)
	pipeline.Config.Execution.DeclarativeDelivery = false
	return pipeline
}

// automaticLegacy is the same under automatic integration, which is the policy
// every path but the human-approval one runs under.
func (f *baselineFixture) automaticLegacy(t *testing.T, provider recordingBackend, commands []string) Pipeline {
	t.Helper()
	return automatic(f.legacy(t, provider, commands), provider)
}

// observedRun reads back the instance a run was observed through, and fails if
// the run was never observed at all.
func observedRun(t *testing.T, store *runstate.Store) (runstate.State, runstate.WorkflowInstance) {
	t.Helper()
	state, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.WorkflowInstanceID == "" {
		t.Fatalf("the run records no workflow instance; it was not observed")
	}
	instance, err := store.LoadWorkflowInstance(state.WorkflowInstanceID)
	if err != nil {
		t.Fatalf("LoadWorkflowInstance(%s) error = %v", state.WorkflowInstanceID, err)
	}
	return state, instance
}

// deliveryTranscript is an instance's path said the way the parity harness says
// one: the state each action was performed in and the outcome it produced there,
// which is what makes a declarative run's record diffable against the recorded
// baseline. The initial checkpoint is skipped because nothing transitioned into
// it — an instance is created standing on its first state.
func deliveryTranscript(instance runstate.WorkflowInstance) []string {
	transcript := make([]string, 0, len(instance.Checkpoints))
	for _, checkpoint := range instance.Checkpoints {
		if checkpoint.From == "" {
			continue
		}
		transcript = append(transcript, checkpoint.From+" "+checkpoint.Outcome)
	}
	return transcript
}

// deliveryTrialSteps is what the observation can report, by state, and it is
// this test's enumeration rather than the code's: the classifiers in
// declarative.go decide an outcome each from the ending they are handed, and
// nothing in them can be asked what the whole set is. Holding it against the
// registry is what says the two vocabularies have not drifted apart — an outcome
// the trial can report and no step produces would reach a run as a divergence
// against a definition that was right.
func deliveryTrialSteps() map[string][]string {
	return map[string][]string{
		deliveryClaim:     {"claimed", "unavailable"},
		deliveryDevelop:   {"produced", "reissued", "relaunches-spent", "stopped"},
		deliveryCheck:     {"failed", "failed-unrepaired", "passed", "refused", "refused-unrepaired", "unrunnable"},
		deliveryReview:    {"approved", "changes-requested", "stopped", "unresolved"},
		deliveryIntegrate: {"conflicted", "contended", "integrated", "reconciling", "superseded"},
		deliveryPublish:   {"published", "stopped", "superseded"},
		deliveryComplete:  {"completed"},
		deliveryCleanUp:   {"cleaned", "partial"},
	}
}

// transcriptOf is a parity scenario said the way an instance says its own path,
// so the two can be compared as one list against another.
func transcriptOf(t *testing.T, trace string) ([]string, string) {
	t.Helper()
	for _, scenario := range parityScenarios() {
		if scenario.trace != trace {
			continue
		}
		transcript := make([]string, 0, len(scenario.steps))
		for _, step := range scenario.steps {
			transcript = append(transcript, step.state+" "+step.outcome)
		}
		return transcript, scenario.terminal
	}
	t.Fatalf("no parity scenario measures the trace %q", trace)
	return nil, ""
}

// declarativeScenario is one delivery path, named by the recorded trace whose
// transcript the instance observing it has to reproduce.
type declarativeScenario struct {
	// trace is the recorded path this run is one of, and the transcript the
	// parity harness measures that path by is what the instance must record.
	trace string
	drive func(t *testing.T) *baselineFixture
}

// declarativeScenarios is every recorded path the parity harness holds a
// transcript for, driven by the baseline scenario that recorded it.
//
// The list is the transcripts rather than the traces: a path the harness does
// not measure has nothing here to compare an instance against, so naming it
// would be a scenario that asserted nothing. The baseline drives the rest, and
// their instances are read there.
func declarativeScenarios() []declarativeScenario {
	measured := []string{
		"human-approved-change-is-preserved-for-its-approver",
		"automatic-run-promotes-reviews-closes-and-cleans-up",
		"failing-check-is-repaired-and-then-promoted",
		"failing-check-spends-the-repair-budget-and-blocks",
		"review-findings-are-repaired-and-then-promoted",
		"review-findings-spend-the-repair-budget-and-block",
		"protected-path-refusal-is-repaired-before-any-check-runs",
		"usage-limit-pause-exits-resumable-and-a-later-invocation-finishes-it",
	}
	scenarios := make([]declarativeScenario, 0, len(measured))
	for _, trace := range measured {
		scenarios = append(scenarios, declarativeScenario{trace: trace, drive: baselineDrive(trace)})
	}
	return scenarios
}

// baselineDrive is the baseline scenario that recorded one trace, looked up by
// name so a scenario renamed there fails here rather than quietly measuring
// nothing.
func baselineDrive(trace string) func(t *testing.T) *baselineFixture {
	return func(t *testing.T) *baselineFixture {
		t.Helper()
		for _, scenario := range baselineScenarios() {
			if scenario.name == trace {
				return scenario.drive(t)
			}
		}
		t.Fatalf("no baseline scenario drives the trace %q", trace)
		return nil
	}
}

// TestADeclarativeRunRecordsTheSequenceTheDefinitionChose is the default path
// itself: every measured path driven for real, and the instance's own record of
// where the definition sent it held against the transcript the parity harness
// measures that path by.
//
// The instance rather than anything this test watched is deliberate, and it is
// the same reason the parity walk reads the record: what a later process would
// see is the checkpoints, so comparing those is comparing what was durably said
// to have happened.
func TestADeclarativeRunRecordsTheSequenceTheDefinitionChose(t *testing.T) {
	t.Parallel()

	for _, scenario := range declarativeScenarios() {
		t.Run(scenario.trace, func(t *testing.T) {
			t.Parallel()
			fixture := scenario.drive(t)
			state, instance := observedRun(t, fixture.store)

			if state.WorkflowDivergence != "" {
				t.Errorf("the run recorded the divergence %q; a path the harness measures is one the definition expresses", state.WorkflowDivergence)
			}
			want, terminal := transcriptOf(t, scenario.trace)
			if got := deliveryTranscript(instance); !slices.Equal(got, want) {
				t.Errorf("the instance recorded\n\t%v\nand the transcript this path is measured by is\n\t%v", got, want)
			}
			if !instance.Terminal || instance.State != terminal {
				t.Errorf("the instance ended in %q (terminal=%t) and the transcript ends in %q", instance.State, instance.Terminal, terminal)
			}
			// The instance is pinned to the definition it started on, and the run
			// names the instance rather than the other way round: both are what a
			// second process reads to pick the observation back up.
			if instance.InstanceID != deliveryInstanceID(state.RunID) {
				t.Errorf("the instance is %q and the run is %q", instance.InstanceID, state.RunID)
			}
			if instance.Digest == "" {
				t.Errorf("the instance is pinned to no definition")
			}
		})
	}
}

// TestARunThatEndsOffTheDefinitionRecordsThatItDid is the soak's counting held
// to being honest about what it counts.
//
// A run can end by a route the definition has no outcome for — this one ends on
// a reviewer whose reply cannot be read as a verdict twice over, which is a
// review error and not the operator's stop. Nothing observes such an ending,
// deliberately: naming the nearest outcome would record the run ending somewhere
// it did not, so the instance is left standing where the two last agreed.
//
// What must not follow is silence. The soak counts the items carrying no
// divergence, so a run whose instance stopped mid-graph and said nothing would
// be counted as one that walked the definition to the end. The divergence is the
// gap itself, and it names the state the instance stopped in.
func TestARunThatEndsOffTheDefinitionRecordsThatItDid(t *testing.T) {
	t.Parallel()

	fixture := newBaselineFixture(t, baselineItem())
	// The reviewer answers with something that is not a verdict, twice: the run
	// asks once more and then fails on it, having bought no verdict at all.
	provider := orchestratortest.RoleBackend(baselineImplements, "not a verdict at all", "still not a verdict")
	fixture.invoke(t, "run", fixture.automatic(t, provider, []string{"test -f feature.txt"}))

	state, instance := observedRun(t, fixture.store)
	if !state.Status.Terminal() {
		t.Fatalf("the run ended in %s; this measures what a terminal run records", state.Status)
	}
	// The instance is left where the two last agreed rather than dragged to an
	// outcome the run never produced.
	if instance.Terminal {
		t.Errorf("the instance ended in the terminal %q; this run left by a route the definition has no outcome for", instance.State)
	}
	if instance.State != deliveryReview {
		t.Errorf("the instance stands in %q and the run ended asking for a verdict in %q", instance.State, deliveryReview)
	}
	if state.WorkflowDivergence == "" {
		t.Fatalf("the run ended terminally with its instance standing in %q and recorded no divergence; the soak would count it as clean", instance.State)
	}
	if !strings.Contains(state.WorkflowDivergence, instance.State) {
		t.Errorf("the divergence is %q and does not name the state %q the instance stopped in", state.WorkflowDivergence, instance.State)
	}
}

// TestTheRollbackLeavesARunExecutingNothingDeclarative holds the one key to
// being the whole rollback: the same run under the same pipeline with
// `declarative_delivery: false` records no instance and delivers the work
// exactly as a run made before the definition existed did.
func TestTheRollbackLeavesARunExecutingNothingDeclarative(t *testing.T) {
	t.Parallel()

	fixture := newBaselineFixture(t, baselineItem())
	provider := orchestratortest.RoleBackend(baselineImplements, approveVerdict)
	// The pipeline keeps somewhere to record instances, which is what production
	// wires unconditionally, so what this measures is the key rather than the
	// absence of a store.
	pipeline := automatic(fixture.legacy(t, provider, []string{"test -f feature.txt"}), provider)
	if pipeline.Instances == nil {
		t.Fatalf("the rolled-back pipeline has nowhere to record an instance; this measures the key")
	}
	outcome := fixture.invoke(t, "run", pipeline)
	if outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("the rolled-back run ended in %s; the legacy path did not deliver the work", outcome.Status)
	}

	state, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.WorkflowInstanceID != "" {
		t.Errorf("the run records the instance %q and its project had rolled back", state.WorkflowInstanceID)
	}
	if state.WorkflowDivergence != "" {
		t.Errorf("the run records the divergence %q and nothing was observing it", state.WorkflowDivergence)
	}
	// Nor is it recorded as a run that should have been observed and was not. That
	// is the other way to carry no instance, and the rollback is the one case where
	// carrying none is what the project asked for.
	if state.WorkflowUnobserved != "" {
		t.Errorf("the run records %q and its project had rolled back; a rollback is not a failed observation", state.WorkflowUnobserved)
	}
	if _, err := fixture.store.LoadWorkflowInstance(deliveryInstanceID(pipelineRunID)); err == nil {
		t.Errorf("an instance was recorded for a run nothing observed")
	}
}

// TestALegacyRunInFlightIsNeverMigratedIntoTheDefinition is the resumability
// half of the flip, driven the only way it can be shown: a run started under the
// rollback, left in flight, and picked up by a process whose configuration is on
// the default.
//
// The rule it holds is that the run's own record decides. A run reserved on the
// legacy path names no instance, and no later process invents one for it however
// its configuration reads — so a legacy run stays a legacy run for the whole of
// its life, and the default reaches new runs only. It is what an operator who
// undoes a rollback, or who upgrades a build whose default has moved, gets: the
// runs in flight finish on what they started on.
func TestALegacyRunInFlightIsNeverMigratedIntoTheDefinition(t *testing.T) {
	t.Parallel()

	fixture := newBaselineFixture(t, baselineItem())
	resetsAt := baseTime.Add(2 * time.Hour)
	limit := &backend.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt}

	// The first invocation is refused for want of capacity and the wait is longer
	// than this process holds open, so it exits with the run in flight. The
	// project has rolled back, which is what makes this a legacy run.
	refused := usageLimitBackend(1, limit, approveVerdict)
	rolledBack := waiting(fixture.automaticLegacy(t, refused, []string{"test -f feature.txt"}),
		&pausingClock{now: baseTime}, 6*time.Hour, time.Minute)
	fixture.invoke(t, "paused invocation", rolledBack)

	paused, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if paused.Status.Terminal() {
		t.Fatalf("the first invocation ended the run in %s; there is nothing left in flight to resume", paused.Status)
	}
	if paused.WorkflowInstanceID != "" {
		t.Fatalf("the run was started on the legacy path and records the instance %q", paused.WorkflowInstanceID)
	}

	// The rollback is undone between the two invocations, which is the whole of
	// what this test is about.
	served := usageLimitBackend(0, limit, approveVerdict)
	resuming := waiting(fixture.automatic(t, served, []string{"test -f feature.txt"}),
		&pausingClock{now: resetsAt.Add(time.Minute)}, 6*time.Hour, time.Minute)
	outcome := fixture.invoke(t, "resumed invocation", resuming)
	if outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("the resumed run ended in %s; the legacy path did not finish the work", outcome.Status)
	}

	finished, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if finished.WorkflowInstanceID != "" {
		t.Errorf("the resumed run records the instance %q; a run in flight is never migrated onto the definition", finished.WorkflowInstanceID)
	}
	if finished.WorkflowDivergence != "" {
		t.Errorf("the resumed run records the divergence %q and was never observed", finished.WorkflowDivergence)
	}
	if _, err := fixture.store.LoadWorkflowInstance(deliveryInstanceID(pipelineRunID)); err == nil {
		t.Errorf("an instance was recorded for a run that started on the legacy path")
	}
}

// TestARunOnTheDefinitionIsFinishedOnItAfterARollback is the same rule read the
// other way, and it is worth its own run because it is the direction a rollback
// takes: a run that started on the definition keeps being stepped by a process
// whose configuration no longer asks for it, so its instance reaches a terminal
// rather than stopping wherever the operator happened to edit the file. That is
// what "in-flight instances finish on whatever they started on" costs the
// rollback, and it is the behaviour rather than an oversight.
func TestARunOnTheDefinitionIsFinishedOnItAfterARollback(t *testing.T) {
	t.Parallel()

	fixture := newBaselineFixture(t, baselineItem())
	resetsAt := baseTime.Add(2 * time.Hour)
	limit := &backend.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt}

	refused := usageLimitBackend(1, limit, approveVerdict)
	fixture.invoke(t, "paused invocation", waiting(fixture.automatic(t, refused, []string{"test -f feature.txt"}),
		&pausingClock{now: baseTime}, 6*time.Hour, time.Minute))

	paused, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if paused.WorkflowInstanceID == "" {
		t.Fatalf("the run was started on the default path and records no instance")
	}

	served := usageLimitBackend(0, limit, approveVerdict)
	off := waiting(automatic(fixture.legacy(t, served, []string{"test -f feature.txt"}), served),
		&pausingClock{now: resetsAt.Add(time.Minute)}, 6*time.Hour, time.Minute)
	fixture.invoke(t, "resumed invocation", off)

	state, instance := observedRun(t, fixture.store)
	if state.WorkflowDivergence != "" {
		t.Errorf("the run recorded the divergence %q", state.WorkflowDivergence)
	}
	want, terminal := transcriptOf(t, "usage-limit-pause-exits-resumable-and-a-later-invocation-finishes-it")
	if got := deliveryTranscript(instance); !slices.Equal(got, want) {
		t.Errorf("the instance recorded\n\t%v\nand the transcript this path is measured by is\n\t%v", got, want)
	}
	if !instance.Terminal || instance.State != terminal {
		t.Errorf("the instance ended in %q (terminal=%t) and the transcript ends in %q", instance.State, instance.Terminal, terminal)
	}
}

// observedFixture is a run standing where a fresh one stands, with its instance
// begun and nothing performed. It is built directly rather than driven because
// what the tests below are about is what the observation does when the run and
// the definition stop agreeing, and none of the paths driven above reaches that.
// The real path that does — a process killed while its reviewer was being asked,
// resumed at the checks because a resumed run re-earns the whole gate — needs an
// interrupted process to reach, and what it produces is the first of the two
// disagreements stated here.
func observedFixture(t *testing.T) (*activeRun, *runstate.Store) {
	t.Helper()
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	state := completingRun("yoyodyne-task", runstate.PhaseDeveloping)
	pipeline := Pipeline{Store: store, Instances: store}
	// Stated rather than left to the zero value, because this configuration is
	// built here rather than loaded and the harness default is what a loaded one
	// would carry.
	pipeline.Config.Execution.DeclarativeDelivery = true
	pipeline.Config.Approvals.Integration = domain.ApprovalAutomatic
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	run := &activeRun{pipeline: pipeline, state: state}
	run.beginDeliveryTrial()
	if run.trial == nil {
		t.Fatalf("the observation did not begin over a store that can record it")
	}
	return run, store
}

// TestAnObservationTheDefinitionCannotFollowIsRecordedOnTheRun is what a soak
// counts: a boundary the definition and the run do not agree on is written down
// on the run, and the instance is left standing where the two last agreed rather
// than dragged into a position neither of them was in.
func TestAnObservationTheDefinitionCannotFollowIsRecordedOnTheRun(t *testing.T) {
	t.Parallel()

	for _, disagreement := range []struct {
		what    string
		state   string
		outcome string
	}{
		{
			what:    "a state the instance is not standing in",
			state:   deliveryCheck,
			outcome: "passed",
		},
		{
			what:    "an outcome the state does not route",
			state:   deliveryClaim,
			outcome: "produced",
		},
	} {
		t.Run(disagreement.what, func(t *testing.T) {
			t.Parallel()
			run, store := observedFixture(t)
			run.observe(context.Background(), disagreement.state, disagreement.outcome)

			if run.state.WorkflowDivergence == "" {
				t.Fatalf("%s was observed and the run records no divergence", disagreement.what)
			}
			recorded, err := store.Load(run.state.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if recorded.WorkflowDivergence != run.state.WorkflowDivergence {
				t.Errorf("the divergence on disk is %q and the run holds %q; a run can diverge after its own last save",
					recorded.WorkflowDivergence, run.state.WorkflowDivergence)
			}
			instance, err := store.LoadWorkflowInstance(run.state.WorkflowInstanceID)
			if err != nil {
				t.Fatalf("LoadWorkflowInstance() error = %v", err)
			}
			if instance.State != deliveryClaim || len(instance.Checkpoints) != 1 {
				t.Errorf("the instance stands in %q with %d checkpoint(s); it was moved by a boundary the definition refused",
					instance.State, len(instance.Checkpoints))
			}

			// Nothing further is observed, so a run that carries on delivering after
			// a divergence does not write a second answer over the first.
			first := run.state.WorkflowDivergence
			run.observe(context.Background(), deliveryClaim, "claimed")
			if run.state.WorkflowDivergence != first {
				t.Errorf("the run kept observing after it diverged: %q became %q", first, run.state.WorkflowDivergence)
			}
			after, err := store.LoadWorkflowInstance(run.state.WorkflowInstanceID)
			if err != nil {
				t.Fatalf("LoadWorkflowInstance() error = %v", err)
			}
			if len(after.Checkpoints) != 1 {
				t.Errorf("the instance recorded %d checkpoint(s) after the run stopped being observed", len(after.Checkpoints))
			}
		})
	}
}

// TestTheObservationReportsOnlyOutcomesTheRegisteredStepsProduce holds the
// observation to the same vocabulary the definitions are compiled against.
//
// An outcome the observation can report and the registry does not declare is a
// transition no definition routes, which would reach a run as a divergence
// against nothing — the definition would be right and the observation wrong. The
// other direction is checked too: an outcome a step produces and nothing here
// can report is a branch no run would ever exercise, which is coverage that
// looks broader than it is.
func TestTheObservationReportsOnlyOutcomesTheRegisteredStepsProduce(t *testing.T) {
	t.Parallel()

	for _, builtin := range builtinDeliveryWorkflows() {
		graph, err := compileDelivery(builtin)
		if err != nil {
			t.Fatalf("compileDelivery(%s) error = %v", builtin.Source, err)
		}
		for _, state := range graph.States() {
			node, _ := graph.Node(state)
			reported, named := deliveryTrialSteps()[state]
			if !named {
				t.Errorf("%s declares the state %q and the trial says nothing about it", builtin.Source, state)
				continue
			}
			declared := deliveryAnswers[node.Action().Name]
			for _, outcome := range reported {
				if !slices.Contains(declared, outcome) {
					t.Errorf("the trial reports %q for %q, which %q never produces; it answers with %v",
						outcome, state, node.Action().Name, declared)
				}
			}
			for _, outcome := range declared {
				if !slices.Contains(reported, outcome) {
					t.Errorf("%q produces %q in the state %q and the trial can never report it",
						node.Action().Name, outcome, state)
				}
			}
		}
	}
}

// A run whose process dies never records its own ending: a sweep settles it, and
// that settlement is the only ending it gets. So the gap an unfinished
// observation leaves has to be recorded there too, in the same words the live
// pipeline uses. Without it the runs a soak most needs to hear about — the ones
// the network, the provider, or the machine killed — are exactly the runs
// recorded as having agreed with the definition throughout.
//
// The completed case is the damaging one, and is why this is a table rather than
// one scenario: the work lands and the item closes, so a run whose observation
// stopped halfway reads afterwards precisely like one that walked the definition
// to the end.
func TestASweepRecordsTheGapAnInterruptedObservationLeaves(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		haltAt     runstate.Phase
		wantAction ReconcileAction
	}{
		{name: "settled as completed", haltAt: runstate.PhaseCompleting, wantAction: ActionCompleted},
		// Halted where the reviewer would have been asked: the gate passed and was
		// observed, so the instance stands in the review state with nothing left to
		// step it. Halting while developing would not do — the process survives the
		// refused write there and observes the developer's own ending, which sends
		// the instance to a terminal, so there is no gap to find.
		{name: "settled as blocked", haltAt: runstate.PhaseReviewing, wantAction: ActionBlocked},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			repository, worktreeRoot, store := restartableFixture(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
			}, approveVerdict)
			halting := &haltingStore{StateStore: store, at: test.haltAt}
			pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
			// Instances go to the store itself rather than through the halting
			// wrapper, because what the halt models is a process that stopped writing
			// its run record: the instance on disk is whatever the dead process had
			// already recorded.
			pipeline.Instances = store

			if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil || !halting.halted {
				t.Fatalf("interrupted Run() error = %v, halted = %t", err, halting.halted)
			}
			interrupted, err := store.Load(pipelineRunID)
			if err != nil {
				t.Fatalf("Load() interrupted state error = %v", err)
			}
			if interrupted.Status.Terminal() {
				t.Fatalf("the run recorded its own terminal; this measures the ending a sweep writes")
			}
			if interrupted.WorkflowInstanceID == "" {
				t.Fatalf("the interrupted run records no instance; it was never observed")
			}
			if interrupted.WorkflowDivergence != "" {
				t.Fatalf("the run already recorded %q; this measures the gap the sweep closes", interrupted.WorkflowDivergence)
			}

			results := reconcileSweep(t, repository, worktreeRoot, store, tracker)
			if len(results) != 1 || results[0].Action != test.wantAction {
				t.Fatalf("reconciliation = %#v, want one %q", results, test.wantAction)
			}

			settled, err := store.Load(interrupted.RunID)
			if err != nil {
				t.Fatalf("Load() settled state error = %v", err)
			}
			if !settled.Status.Terminal() {
				t.Fatalf("settled state = %#v, want a terminal run", settled)
			}
			instance, err := store.LoadWorkflowInstance(settled.WorkflowInstanceID)
			if err != nil {
				t.Fatalf("LoadWorkflowInstance(%s) error = %v", settled.WorkflowInstanceID, err)
			}
			if instance.Terminal {
				t.Fatalf("the instance ended in the terminal %q; there is no gap here to record", instance.State)
			}
			if settled.WorkflowDivergence == "" {
				t.Fatalf("the sweep made the run terminal as %s with its instance standing in %q and recorded no divergence; the run would read as having walked the definition to the end",
					settled.Status, instance.State)
			}
			if !strings.Contains(settled.WorkflowDivergence, instance.State) {
				t.Errorf("the divergence is %q and does not name the state %q the instance stopped in", settled.WorkflowDivergence, instance.State)
			}
		})
	}
}

// The other half of the same rule, and the one the recorded baseline holds: a
// blocked settlement records a divergence when there is a gap and records none
// when there is not.
//
// `reconciliation-blocks-a-run-interrupted-while-developing` is a process that
// stops writing its run record as the developer's attempt ends, and its trace
// carries an instance and no divergence. That reads like the blocked path having
// been missed, and it is the opposite: the process survives the refused write,
// records its own ending, and the developer's ending is one the definition has an
// outcome for, so the instance reaches the `abandoned` terminal before the sweep
// ever looks at it. An observation that finished is not a gap, and inventing a
// divergence for one would make every such run read as a disagreement.
//
// It is measured here rather than left to be inferred from an empty field,
// because an empty field is exactly what the defect would look like too.
func TestASweepRecordsNoDivergenceWhereTheObservationReachedATerminal(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	// The baseline scenario's own halt: writes stop while the developer's work is
	// still the only thing that happened.
	halting := &haltingStore{StateStore: store, at: runstate.PhaseChecking}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"test -f feature.txt"}), provider)
	pipeline.Instances = store

	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil || !halting.halted {
		t.Fatalf("interrupted Run() error = %v, halted = %t", err, halting.halted)
	}

	results := reconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("reconciliation = %#v, want one %q", results, ActionBlocked)
	}
	settled, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() settled state error = %v", err)
	}
	if !settled.Status.Terminal() {
		t.Fatalf("settled state = %#v, want a terminal run", settled)
	}
	if settled.WorkflowInstanceID == "" {
		t.Fatalf("the settled run records no instance; it was never observed")
	}
	instance, err := store.LoadWorkflowInstance(settled.WorkflowInstanceID)
	if err != nil {
		t.Fatalf("LoadWorkflowInstance(%s) error = %v", settled.WorkflowInstanceID, err)
	}
	if !instance.Terminal {
		t.Fatalf("the instance stands in %q rather than a terminal; this measures the settlement that has no gap to record", instance.State)
	}
	if settled.WorkflowDivergence != "" {
		t.Errorf("the observation reached the terminal %q and the run was still settled carrying the divergence %q",
			instance.State, settled.WorkflowDivergence)
	}
}

// TestABlockedSettlementRecordsTheGapItsInstanceLeaves asks the blocked
// settlement itself, at the function every route into it shares.
//
// The two sweep-driven tests above reach blockRun through abandon, which is one
// of its three callers; a recorded integration the repository contradicts
// reaches it too. A divergence recorded on one route and not another is exactly
// the defect the blocked settlement was suspected of, so it is measured at
// blockRun directly rather than inferred from the route the pipeline happened
// to take: over a run whose instance still stands mid-graph, which has to
// record the gap in the words the live pipeline uses, and over one whose
// instance reached a terminal, which has to record nothing. The second is the
// recorded baseline's blocked trace with no pipeline in front of it.
func TestABlockedSettlementRecordsTheGapItsInstanceLeaves(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		standing string
		terminal bool
	}{
		{name: "instance still mid-graph", standing: deliveryReview, terminal: false},
		{name: "instance on a terminal", standing: "abandoned", terminal: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
			if err != nil {
				t.Fatalf("runstate.NewStore() error = %v", err)
			}
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "in_progress"}}
			now := time.Now()
			state := runstate.State{
				SchemaVersion:      runstate.StateSchemaVersion,
				RunID:              "run-abcdef0123456789abcdef0123456789",
				ProductID:          "yoyodyne",
				RepositoryID:       "yoyodyne",
				WorkItemID:         tracker.Item.ID,
				Backend:            "claude-code",
				Status:             runstate.StatusRunning,
				Phase:              runstate.PhaseReviewing,
				StartedAt:          now,
				UpdatedAt:          now,
				WorkflowInstanceID: "run-abcdef0123456789abcdef0123456789-delivery",
			}
			if err := store.Create(state); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			instance := runstate.WorkflowInstance{
				SchemaVersion:    runstate.WorkflowInstanceSchemaVersion,
				InstanceID:       state.WorkflowInstanceID,
				WorkflowID:       "delivery",
				DefinitionSchema: 1,
				Digest:           "wf-" + strings.Repeat("0", 64),
				State:            test.standing,
				Terminal:         test.terminal,
				Checkpoints: []runstate.WorkflowCheckpoint{
					{Sequence: 0, State: deliveryClaim, At: now},
					{Sequence: 1, State: test.standing, Terminal: test.terminal, From: deliveryClaim, Outcome: "claimed", At: now},
				},
			}
			if err := store.CreateWorkflowInstance(instance); err != nil {
				t.Fatalf("CreateWorkflowInstance() error = %v", err)
			}

			result, err := Reconciler{Tracker: tracker, Store: store}.blockRun(context.Background(), state, tracker.Item.Status, gitworktree.Observation{}, "interrupted while reviewing")
			if err != nil {
				t.Fatalf("blockRun() error = %v", err)
			}
			if result.Action != ActionBlocked {
				t.Fatalf("blockRun() action = %q, want %q", result.Action, ActionBlocked)
			}
			settled, err := store.Load(state.RunID)
			if err != nil {
				t.Fatalf("Load() settled state error = %v", err)
			}
			if !settled.Status.Terminal() || settled.Blocker == "" {
				t.Fatalf("settled state = status %s, blocker %q; this measures the blocked settlement and the run was not settled as one", settled.Status, settled.Blocker)
			}
			if test.terminal {
				if settled.WorkflowDivergence != "" {
					t.Errorf("the observation reached the terminal %q and the blocked settlement still recorded the divergence %q", test.standing, settled.WorkflowDivergence)
				}
				return
			}
			want := unfinishedInstance(settled.Status, test.standing).Error()
			if settled.WorkflowDivergence != want {
				t.Errorf("the blocked settlement recorded the divergence %q, want %q — the words a live pipeline records for the same gap", settled.WorkflowDivergence, want)
			}
		})
	}
}

// The same sweep over a run nobody was observing records nothing, which is what
// the rollback has to reach on this path as well: settling a legacy run must not
// invent an observation of it.
func TestASweepRecordsNothingForARunNobodyWasObserving(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	halting := &haltingStore{StateStore: store, at: runstate.PhaseCompleting}
	// Somewhere to record instances and a project that rolled back, so what this
	// measures is the key rather than the absence of a store.
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
	pipeline.Instances = store
	pipeline.Config.Execution.DeclarativeDelivery = false

	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil || !halting.halted {
		t.Fatalf("interrupted Run() error = %v, halted = %t", err, halting.halted)
	}
	if results := reconcileSweep(t, repository, worktreeRoot, store, tracker); len(results) != 1 {
		t.Fatalf("reconciliation = %#v, want one settlement", results)
	}
	settled, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() settled state error = %v", err)
	}
	if settled.WorkflowInstanceID != "" || settled.WorkflowDivergence != "" {
		t.Errorf("a run on the legacy path was settled carrying instance %q and divergence %q",
			settled.WorkflowInstanceID, settled.WorkflowDivergence)
	}
}

// blockObservation makes the next run this pipeline reserves unobservable, and
// reports the refusal its trial will meet.
//
// An instance already stands under the identifier that run will be given, which
// is what a process that died between creating one and recording it on the run
// leaves behind. Which of the ways an observation can fail to start this is does
// not matter to what is being measured — a trial that could not start is one
// thing however it could not — and this is the one that needs nothing injected
// into the store the run writes to.
func blockObservation(t *testing.T, pipeline Pipeline) string {
	t.Helper()
	trial, err := pipeline.deliveryTrialOver(pipelineRunID)
	if err != nil {
		t.Fatalf("deliveryTrialOver() error = %v", err)
	}
	if _, err := trial.executor.Start(trial.instance); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	_, refused := trial.executor.Start(trial.instance)
	if refused == nil {
		t.Fatalf("a second instance was created under %s; this fixture does not stop an observation", trial.instance)
	}
	return refused.Error()
}

// unobservedRunFixture drives the automatic path with its trial unable to start,
// and reports the refusal the run met. It is the ordinary promotion scenario in
// every other respect, which is the point: what it produces differs from that
// scenario's recorded trace by the observation and by nothing else.
func unobservedRunFixture(t *testing.T) (*baselineFixture, string) {
	t.Helper()
	fixture := newBaselineFixture(t, baselineItem())
	provider := orchestratortest.RoleBackend(baselineImplements, approveVerdict)
	pipeline := fixture.automatic(t, provider, []string{"test -f feature.txt"})
	refused := blockObservation(t, pipeline)
	fixture.invoke(t, "run", pipeline)
	return fixture, refused
}

// TestATrialThatCouldNotStartIsRecordedAsUnobserved is the first half of the
// shape that froze a bad baseline: a run whose observation could not begin used
// to record nothing at all, so it was indistinguishable from a run whose project
// had rolled back — the same absent instance, the same absent divergence, and
// nothing anywhere saying which of the two it was.
//
// What it must be instead is loud. The run delivers exactly as it would have,
// because an observation never decides anything about a run, and it says on its
// own record that nothing was watching it and why.
func TestATrialThatCouldNotStartIsRecordedAsUnobserved(t *testing.T) {
	t.Parallel()

	fixture, refused := unobservedRunFixture(t)
	if status := fixture.steps[0].outcome.Status; status != runstate.StatusSucceeded {
		t.Fatalf("the run ended in %s; an observation that could not start is never a reason delivery does not happen", status)
	}

	state, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.WorkflowInstanceID != "" {
		t.Errorf("the run records the instance %q and its trial never started", state.WorkflowInstanceID)
	}
	if state.WorkflowUnobserved == "" {
		t.Fatalf("the run records no reason it was unobserved, so it reads exactly like a run whose project rolled back")
	}
	if !strings.Contains(state.WorkflowUnobserved, refused) {
		t.Errorf("the run records %q and the refusal its trial met was %q; the cause is what somebody reading this has to act on",
			state.WorkflowUnobserved, refused)
	}
	// The two fields say different things and a record carrying both would be
	// saying that an observation which never began disagreed with the run.
	if state.WorkflowDivergence != "" {
		t.Errorf("the run records the divergence %q and no instance ever observed it", state.WorkflowDivergence)
	}
}

// TestTheBaselineRecorderRefusesToFreezeARunNothingObserved is the other half:
// the recorder refuses the trace rather than freezing it.
//
// This is what actually cost something. A run nothing observed produces a trace
// differing from the right one by a single absent field, which every other check
// in the baseline passes, so `-update-baseline` froze one — and the frozen
// document then agreed with three consecutive full checks and began failing when
// the observation started working again. The refusal is scoped to a run that was
// eligible for observation, so the two legitimate ways a trace carries no
// instance — a project that rolled back, and a path that reserves no run at all
// — are recorded exactly as before.
func TestTheBaselineRecorderRefusesToFreezeARunNothingObserved(t *testing.T) {
	t.Parallel()

	for _, path := range []struct {
		what    string
		drive   func(t *testing.T) *baselineFixture
		refused bool
	}{
		{
			what: "a run whose trial could not start",
			drive: func(t *testing.T) *baselineFixture {
				fixture, _ := unobservedRunFixture(t)
				return fixture
			},
			refused: true,
		},
		{
			what:  "a run the definition observed",
			drive: baselineAutomaticPromotion,
		},
		{
			what: "a run whose project rolled back",
			drive: func(t *testing.T) *baselineFixture {
				fixture := newBaselineFixture(t, baselineItem())
				provider := orchestratortest.RoleBackend(baselineImplements, approveVerdict)
				fixture.invoke(t, "run", automatic(fixture.legacy(t, provider, []string{"test -f feature.txt"}), provider))
				return fixture
			},
		},
	} {
		t.Run(path.what, func(t *testing.T) {
			t.Parallel()

			fixture := path.drive(t)
			trace := fixture.trace(t, baselineScenario{name: "unrecorded", freezes: "nothing; this trace is built to be judged rather than frozen"})
			refusal := baselineObservationRefusal(fixture.observable, trace.Durable)
			if path.refused && refusal == "" {
				t.Errorf("the recorder would freeze a trace of a run nothing observed")
			}
			if !path.refused && refusal != "" {
				t.Errorf("the recorder refused a trace it has to freeze: %s", refusal)
			}
		})
	}
}
