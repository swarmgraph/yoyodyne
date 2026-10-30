package orchestrator

// The parity harness: the built-in delivery definitions walked against the
// recorded baseline of what the hard-coded pipeline actually does.
//
// What it is for is the one claim a definition cannot make about itself. Any
// well-formed state machine compiles; the question is whether this one is the
// pipeline, and the only answer worth anything is the frozen behaviour in
// testdata/baseline. So every recorded path is written down here as a
// transcript -- the states the run stood in and the outcome it produced in each
// -- and each transcript is put through two doors that have to agree.
//
// The witness holds a transcript against the trace it claims to describe. A
// developer invocation in the trace is a `develop` state in the transcript, a
// verdict is a `review` state, `repair_attempts` is the number of times the
// transcript went back to the developer from the gate, `integration_retries` is
// the number of `superseded` outcomes, and the commands the event log records
// are the check states that got as far as running them. A transcript that
// claims a path the trace does not evidence fails there, which is what stops
// this file from being a story about the pipeline rather than a reading of it.
//
// The executor walks the transcript through the real graph. The definition is
// compiled by the same loader, under the same grant, from the same bytes the
// build ships, and stepped by `internal/workflow`'s own executor, so a
// transition the definition does not have is a step that fails rather than a
// mismatch nobody notices. What the doors perform is replaced by nothing at all:
// the actions are the registered ones -- their names, their capabilities and
// what they wrap, taken from the same table `actions.go` builds the delivery
// registry from -- with Perform swapped for a function that does nothing, so the
// walk is over the real topology without claiming a work item or promoting
// anything. That is the whole of what "compiled and executed only under the test
// harness" means here.
//
// What it does not do is compare a run's durable record field for field, and it
// could not: everything the pipeline does outside its eight registered steps --
// the pre-claim questions, the pauses, the budgets -- is Go control flow behind
// no door, so there is nothing to execute that would produce a record to
// compare. The traces stay the measure; this measures the sequence against them,
// and the change summary says which paths that leaves unmeasured. What a step
// does rather than when it runs is held where the door can actually be performed
// -- run.complete's tracker writes are in actions_test.go, because a walk whose
// doors perform nothing cannot tell a run that closed its item from one that did
// not.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/workflow"
)

// parityStep is one state an instance stood in, and the outcome it produced
// there.
type parityStep struct {
	state   string
	outcome string
}

// parityScenario is one recorded delivery path as a transcript of the built-in
// definition that expresses it.
type parityScenario struct {
	// trace is the recorded baseline this scenario is measured against, by the
	// name of its file without the extension.
	trace string
	// workflow is the built-in definition this path would be bound to.
	workflow string
	// steps is the transcript, in order.
	steps []parityStep
	// terminal is where the transcript ends. A run a process was killed inside
	// reaches none, and names the state it was left standing in instead.
	terminal string
	standing string
	// unexpressible is why no definition walks this path at all, for the paths
	// that stop before the first state. A scenario carrying it has no transcript,
	// and the trace is held to having no run behind it.
	unexpressible string
}

// The states of the built-in definitions, by the names the files declare them
// under. They are constants because the witness counts them and a state renamed
// in a file without being renamed here would be a count that quietly stopped
// measuring anything.
//
// They are the same constants the declarative path names a run's boundaries
// with rather than a second copy: what this harness measures and what a real run
// is observed against have to be the same eight states or one of them is
// measuring a definition nobody runs.
const (
	parityClaim     = deliveryClaim
	parityDevelop   = deliveryDevelop
	parityCheck     = deliveryCheck
	parityReview    = deliveryReview
	parityIntegrate = deliveryIntegrate
	parityPublish   = deliveryPublish
	parityComplete  = deliveryComplete
	parityCleanUp   = deliveryCleanUp
)

// parityScenarios is every recorded delivery path, said as a transcript.
//
// The order is the order the traces are listed in the baseline document, which
// is the order somebody comparing the two would read them.
func parityScenarios() []parityScenario {
	promoted := []parityStep{
		{parityClaim, "claimed"},
		{parityDevelop, "produced"},
		{parityCheck, "passed"},
		{parityReview, "approved"},
		{parityIntegrate, "integrated"},
		{parityComplete, "completed"},
		{parityCleanUp, "cleaned"},
	}
	// A pause, an overload, a stop the harness made and a transient death are one
	// transition apiece in the same place: the attempt is reissued and the run is
	// back in the developer. The traces differ in what the run recorded about the
	// wait, which is a durable field rather than a sequence, so the four walk the
	// same path.
	reissuedThenPromoted := append([]parityStep{
		{parityClaim, "claimed"},
		{parityDevelop, "reissued"},
	}, promoted[1:]...)
	// A recoverable death carries on past the relaunch budget, so the same
	// transition is taken four times: twice for the relaunches the budget bought
	// and twice for the waits the recovery window bought after it. The definition
	// has one transition for all four, which is the point — what tells a relaunch
	// from a recovery is the run's record rather than the sequence.
	recoveredThenPromoted := append([]parityStep{
		{parityClaim, "claimed"},
		{parityDevelop, "reissued"},
		{parityDevelop, "reissued"},
		{parityDevelop, "reissued"},
		{parityDevelop, "reissued"},
	}, promoted[1:]...)

	return []parityScenario{
		{
			trace:    "human-approved-change-is-preserved-for-its-approver",
			workflow: HumanApprovalWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
				// The same independent review the automatic loop buys, and in place of
				// the promotion the publication that hands the approved change to the
				// person who merges it.
				{parityReview, "approved"},
				{parityPublish, "published"},
				// The same completing step the automatic loop performs, doing less of
				// it: nothing was promoted, so the outcome is recorded on the item and
				// the run is priced, and the item is neither closed nor cleaned up
				// after.
				{parityComplete, "completed"},
			},
			terminal: "preserved",
		},
		{
			trace:    "automatic-run-promotes-reviews-closes-and-cleans-up",
			workflow: DeliveryWorkflowID,
			steps:    promoted,
			terminal: "delivered",
		},
		{
			trace:    "failing-check-is-repaired-and-then-promoted",
			workflow: DeliveryWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
				{parityCheck, "failed"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
				{parityReview, "approved"},
				{parityIntegrate, "integrated"},
				{parityComplete, "completed"},
				{parityCleanUp, "cleaned"},
			},
			terminal: "delivered",
		},
		{
			trace:    "failing-check-spends-the-repair-budget-and-blocks",
			workflow: DeliveryWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
				{parityCheck, "failed"},
				{parityDevelop, "produced"},
				{parityCheck, "failed"},
				{parityDevelop, "produced"},
				{parityCheck, "failed-unrepaired"},
			},
			terminal: "blocked",
		},
		{
			trace:    "review-findings-are-repaired-and-then-promoted",
			workflow: DeliveryWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
				{parityReview, "changes-requested"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
				{parityReview, "approved"},
				{parityIntegrate, "integrated"},
				{parityComplete, "completed"},
				{parityCleanUp, "cleaned"},
			},
			terminal: "delivered",
		},
		{
			trace:    "review-findings-spend-the-repair-budget-and-block",
			workflow: DeliveryWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
				{parityReview, "changes-requested"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
				{parityReview, "changes-requested"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
				{parityReview, "unresolved"},
			},
			terminal: "blocked",
		},
		{
			trace:    "protected-path-refusal-is-repaired-before-any-check-runs",
			workflow: DeliveryWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
				{parityCheck, "refused"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
				{parityReview, "approved"},
				{parityIntegrate, "integrated"},
				{parityComplete, "completed"},
				{parityCleanUp, "cleaned"},
			},
			terminal: "delivered",
		},
		{
			trace:    "protected-path-refusal-spends-the-repair-budget-and-blocks",
			workflow: DeliveryWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
				{parityCheck, "refused"},
				{parityDevelop, "produced"},
				{parityCheck, "refused"},
				{parityDevelop, "produced"},
				{parityCheck, "refused-unrepaired"},
			},
			terminal: "blocked",
		},
		{
			trace:    "protected-path-grant-admits-the-change-it-names",
			workflow: DeliveryWorkflowID,
			steps:    promoted,
			terminal: "delivered",
		},
		{
			trace:    "usage-limit-pause-exits-resumable-and-a-later-invocation-finishes-it",
			workflow: DeliveryWorkflowID,
			steps:    reissuedThenPromoted,
			terminal: "delivered",
		},
		{
			trace:    "server-overload-pause-reissues-the-same-attempt",
			workflow: DeliveryWorkflowID,
			steps:    reissuedThenPromoted,
			terminal: "delivered",
		},
		{
			trace:    "provider-stopped-on-time-is-resumable-and-continues-the-same-attempt",
			workflow: DeliveryWorkflowID,
			steps:    reissuedThenPromoted,
			terminal: "delivered",
		},
		{
			trace:    "operator-stop-cancels-the-run-and-preserves-its-work",
			workflow: DeliveryWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
				// The stop is taken at the reviewer's own boundary, before a verdict
				// is bought: the state was entered and produced no judgement, which is
				// why the witness counts it as a review that obtained nothing.
				{parityReview, "stopped"},
			},
			terminal: "abandoned",
		},
		{
			trace:    "verdict-fields-the-schema-does-not-name-are-recorded-as-drift",
			workflow: DeliveryWorkflowID,
			steps:    promoted,
			terminal: "delivered",
		},
		{
			trace:    "partial-cleanup-leaves-a-succeeded-run-reporting-what-survives",
			workflow: DeliveryWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
				{parityReview, "approved"},
				{parityIntegrate, "integrated"},
				{parityComplete, "completed"},
				{parityCleanUp, "partial"},
			},
			terminal: "delivered",
		},
		{
			// An outstanding publication does not change the path: the promotion
			// landed and the forge's silence is reported beside it.
			trace:    "outstanding-publication-leaves-a-succeeded-run-naming-it",
			workflow: DeliveryWorkflowID,
			steps:    promoted,
			terminal: "delivered",
		},
		{
			// A completion record that arrived late is a cleanup the definition
			// sees as unfinished, which is how the pipeline observes it.
			trace:    "completion-record-that-arrives-late-says-so",
			workflow: DeliveryWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
				{parityReview, "approved"},
				{parityIntegrate, "integrated"},
				{parityComplete, "completed"},
				{parityCleanUp, "partial"},
			},
			terminal: "delivered",
		},
		{
			trace:    "environment-refusing-the-probe-ends-the-run-as-outside-the-work",
			workflow: DeliveryWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "stopped"},
			},
			terminal: "abandoned",
		},
		{
			trace:    "transient-provider-death-relaunches-without-charging-the-developer",
			workflow: DeliveryWorkflowID,
			steps:    reissuedThenPromoted,
			terminal: "delivered",
		},
		{
			trace:    "transient-deaths-spend-the-relaunch-budget-and-block",
			workflow: DeliveryWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "reissued"},
				{parityDevelop, "reissued"},
				{parityDevelop, "relaunches-spent"},
			},
			terminal: "blocked",
		},
		{
			trace:    "recoverable-death-carries-on-past-the-relaunch-budget",
			workflow: DeliveryWorkflowID,
			steps:    recoveredThenPromoted,
			terminal: "delivered",
		},
		{
			trace:         "unresolved-directive-pauses-the-work-before-anything-is-claimed",
			unexpressible: "the directive is read before the claim, and a definition begins at the claim; nothing was claimed, so there is no first state for an instance to stand in",
		},
		{
			trace:         "unfinished-dependency-pauses-the-work-before-anything-is-claimed",
			unexpressible: "the dependency is read at the same boundary as the directive, before the claim and before any state",
		},
		{
			trace:         "operator-hold-starts-nothing-at-all",
			unexpressible: "the hold is the first question Run asks and it is asked before anything is claimed; a held harness never reaches a state",
		},
		{
			trace:    "operator-hold-parks-a-claimed-run-and-accounts-for-what-it-cost",
			workflow: DeliveryWorkflowID,
			// The hold reached a run that was already developing and was lifted while
			// it waited, so the wait happened inside the developer's own state and the
			// sequence is the ordinary one. What the park cost is a durable field on
			// the run rather than a transition.
			steps:    promoted,
			terminal: "delivered",
		},
		{
			trace:         "intake-hold-starts-nothing-the-harness-chose",
			unexpressible: "the intake hold stops the choosing rather than the work, before a run is reserved and before any state",
		},
		{
			trace:    "promotion-is-replayed-when-the-target-branch-moves",
			workflow: DeliveryWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
				{parityReview, "approved"},
				{parityIntegrate, "superseded"},
				{parityCheck, "passed"},
				{parityReview, "approved"},
				{parityIntegrate, "integrated"},
				{parityComplete, "completed"},
				{parityCleanUp, "cleaned"},
			},
			terminal: "delivered",
		},
		{
			trace:    "a-replay-that-stops-on-the-change-spends-the-integration-budget",
			workflow: DeliveryWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
				{parityReview, "approved"},
				{parityIntegrate, "superseded"},
				{parityCheck, "passed"},
				{parityReview, "unresolved"},
			},
			terminal: "blocked",
		},
		{
			trace:    "reconciliation-completes-a-run-interrupted-inside-integration",
			workflow: DeliveryWorkflowID,
			// The process died inside the promotion, which is the one boundary
			// durable state cannot describe. The instance is left standing where the
			// executor would leave it: the action was performed and its outcome was
			// never recorded. What settles it is the sweep, and no definition
			// expresses that -- `Reconciler.Reconcile` is not a registered action.
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
				{parityReview, "approved"},
			},
			standing: parityIntegrate,
		},
		{
			trace:    "reconciliation-blocks-a-run-interrupted-while-developing",
			workflow: DeliveryWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
			},
			standing: parityCheck,
		},
	}
}

//
// The gate stoppages no recorded trace reaches.
//

// humanApprovalStoppage is one way the gate ends a run whose integration a
// person approves, and the way to drive the real pipeline into it.
//
// They exist because the baseline records exactly one path through
// `delivery-human-approval.yaml` — the change that passes and is approved — so
// the outcomes the check state routes anywhere else would otherwise be a claim
// about the pipeline with nothing behind it. That is the one thing a definition
// must not be, so rather than assert where they go, each one is driven through
// the real non-automatic pipeline and the definition's path is held against
// what the run actually did and left behind.
//
// Driven rather than recorded is deliberate: the traces under testdata/baseline
// are the 211 baseline's artifact, and adding to them is re-recording somebody
// else's frozen document. What these produce is the same evidence, read in the
// same test that uses it.
type humanApprovalStoppage struct {
	// outcome names the stoppage by the outcome its last check produced, in the
	// definition's own vocabulary.
	outcome string
	// what names the stoppage in a failure.
	what string
	// handedBack is whether the stoppage is repair input: a failing check and a
	// refused change go back to the developer until the shared repair budget is
	// spent, exactly as under automatic integration, and a suite the machine
	// could not run is not repair input and ends the run on its first round.
	handedBack bool
	// drive runs the real non-automatic pipeline into it.
	drive func(t *testing.T) *baselineFixture
}

func humanApprovalStoppages() []humanApprovalStoppage {
	return []humanApprovalStoppage{
		{
			outcome:    "failed",
			what:       "a configured check that ran and failed",
			handedBack: true,
			drive: func(t *testing.T) *baselineFixture {
				t.Helper()
				fixture := newBaselineFixture(t, baselineItem())
				provider := orchestratortest.RoleBackend(baselineImplements, approveVerdict)
				fixture.invoke(t, "run", fixture.pipeline(t, provider, []string{"exit 1"}))
				return fixture
			},
		},
		{
			outcome:    "refused",
			what:       "a change touching an upstream artifact home the item never granted",
			handedBack: true,
			drive: func(t *testing.T) *baselineFixture {
				t.Helper()
				fixture := newBaselineFixture(t, baselineItem())
				provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
					if err := baselineImplements(request); err != nil {
						return err
					}
					return writeUpstream(t, request.WorkingDirectory, "docs/product/brief.md", "the product is whatever this run needed it to be\n")
				}, approveVerdict)
				fixture.invoke(t, "run", fixture.pipeline(t, provider, []string{"test -f feature.txt"}))
				return fixture
			},
		},
		{
			outcome: "unrunnable",
			what:    "a suite the machine could not run at all",
			drive: func(t *testing.T) *baselineFixture {
				t.Helper()
				fixture := newBaselineFixture(t, baselineItem())
				provider := orchestratortest.RoleBackend(baselineImplements, approveVerdict)
				pipeline := fixture.pipeline(t, provider, []string{"test -f feature.txt"})
				pipeline.Checks = refusingChecks{cause: errors.New("no check process could be started")}
				fixture.invoke(t, "run", pipeline)
				return fixture
			},
		},
	}
}

// refusingChecks is a check runner that could not run the suite at all, which is
// the one gate stoppage a developer's own change cannot produce.
type refusingChecks struct{ cause error }

func (r refusingChecks) Run(_ context.Context, request checks.Request, _ func(execution.Event) error) ([]checks.Result, uint64, error) {
	return nil, request.LastSequence, r.cause
}

// TestHumanApprovalDefinitionEndsAStoppedRunWhereThePipelineLeavesIt holds the
// unrecorded check outcomes to the runs they describe.
//
// The path is built from what the run did — one developer invocation for the
// first attempt and one for each repair the run recorded — and the terminal is
// derived from what the run left rather than read from the file, so this fails
// if the definition sends one of them somewhere the run did not go.
func TestHumanApprovalDefinitionEndsAStoppedRunWhereThePipelineLeavesIt(t *testing.T) {
	t.Parallel()

	graph := parityGraph(t, HumanApprovalWorkflowID)
	if _, isAState := graph.Node(parityCheck); !isAState {
		t.Fatalf("the %s definition declares no %q state", HumanApprovalWorkflowID, parityCheck)
	}

	for _, stoppage := range humanApprovalStoppages() {
		t.Run(stoppage.outcome, func(t *testing.T) {
			t.Parallel()
			fixture := stoppage.drive(t)
			state, err := fixture.store.Load(pipelineRunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}

			succeeded := state.Status == runstate.StatusSucceeded
			if succeeded {
				t.Errorf("%s left the run succeeded; the gate did not stop it", stoppage.what)
			}
			developed := 0
			reviewed := 0
			for _, invocation := range fixture.invocations() {
				switch {
				case strings.HasPrefix(invocation, "developer:"):
					developed++
				case strings.HasPrefix(invocation, "reviewer:"):
					reviewed++
				}
			}
			if reviewed != 0 {
				t.Errorf("%s was reviewed %d time(s); a change the gate stops is never shown to the reviewer", stoppage.what, reviewed)
			}
			// The shared fixture's execution.repair_attempts_before_replan.
			budget := state.RepairBudget(2)
			wantRepairs := 0
			if stoppage.handedBack {
				wantRepairs = budget
			}
			if state.RepairAttempts != wantRepairs || developed != wantRepairs+1 {
				t.Errorf("%s spent repair_attempts = %d over %d developer invocation(s), want %d repair(s) on the same budget automatic integration spends",
					stoppage.what, state.RepairAttempts, developed, wantRepairs)
			}
			if state.WorktreePath == "" || state.Branch == "" {
				t.Errorf("%s left no worktree or branch on the record (%q, %q)", stoppage.what, state.WorktreePath, state.Branch)
			}
			if state.WorktreeRemoved || state.BranchRemoved {
				t.Errorf("%s removed an artifact (worktree=%t branch=%t); a stopped run leaves its work standing",
					stoppage.what, state.WorktreeRemoved, state.BranchRemoved)
			}

			steps := []parityStep{{parityClaim, "claimed"}}
			for round := 0; round < state.RepairAttempts; round++ {
				steps = append(steps, parityStep{parityDevelop, "produced"}, parityStep{parityCheck, stoppage.outcome})
			}
			last := stoppage.outcome
			if stoppage.handedBack {
				last += "-unrepaired"
			}
			steps = append(steps, parityStep{parityDevelop, "produced"}, parityStep{parityCheck, last})
			want := terminalTheRunEarned(fixture.tracker.Record().Closed, fixture.tracker.Record().Blocked, succeeded)
			walkTranscript(t, parityScenario{
				trace:    "human-approval-" + stoppage.outcome,
				workflow: HumanApprovalWorkflowID,
				steps:    steps,
				terminal: want,
			})
		})
	}
}

// terminalTheRunEarned reads a finished run back as the terminal a definition
// has to send it to. What became of the work item and whether the run succeeded
// are the whole of what tells the endings apart: a blocker is somebody's to
// decide, a closure is work that landed, a success that closed nothing is a
// change waiting on its approver, and anything else stopped with nothing to
// show. It is the same reading `witnessEnding` holds a recorded trace to, said
// forwards so a driven run can be compared to a definition rather than to a
// sentence.
func terminalTheRunEarned(closed, blocked, succeeded bool) string {
	switch {
	case blocked:
		return "blocked"
	case closed:
		return "delivered"
	case succeeded:
		return "preserved"
	default:
		return "abandoned"
	}
}

// TestBuiltinDeliveryDefinitionsCompile is the first half of the item's own
// acceptance: the definitions this build ships compile clean, against the
// registry the pipeline's steps are registered in and under the authority a
// delivery run holds.
func TestBuiltinDeliveryDefinitionsCompile(t *testing.T) {
	t.Parallel()

	for _, builtin := range builtinDeliveryWorkflows() {
		t.Run(builtin.ID, func(t *testing.T) {
			t.Parallel()
			graph, err := compileDelivery(builtin)
			if err != nil {
				t.Fatalf("compileDelivery(%s) error = %v", builtin.Source, err)
			}
			if graph.ID() != builtin.ID {
				t.Errorf("ID() = %q, want %q", graph.ID(), builtin.ID)
			}
			if graph.Schema() != workflow.SchemaVersion {
				t.Errorf("Schema() = %d, want %d", graph.Schema(), workflow.SchemaVersion)
			}
			// A definition an instance pins itself to has to digest the same on
			// every read of the same bytes, or the pin is a refusal waiting to
			// happen.
			again, err := compileDelivery(builtin)
			if err != nil {
				t.Fatalf("compileDelivery(%s) second error = %v", builtin.Source, err)
			}
			if graph.Digest() != again.Digest() {
				t.Errorf("Digest() = %q then %q; one definition digests to one thing", graph.Digest(), again.Digest())
			}
		})
	}
}

// TestBuiltinDeliveryDefinitionsSelectEveryRegisteredStep names the registered
// steps no built-in definition selects, which is none.
//
// `candidate.publish` is selected by the human-approval definition alone, after
// the review: there `candidate.develop` publishes nothing, so the pull request a
// person merges from is opened for an approved change. delivery.yaml still
// leaves it out, because its `candidate.develop` ends by calling publishAttempt
// and selecting it too would publish every attempt twice. Pinning the list is
// what keeps a step nothing selects a decision rather than an omission -- a step
// that stops being selected shows up here instead of as a sequence quietly
// missing a stage.
func TestBuiltinDeliveryDefinitionsSelectEveryRegisteredStep(t *testing.T) {
	t.Parallel()

	selected := map[string]bool{}
	for _, builtin := range builtinDeliveryWorkflows() {
		graph, err := compileDelivery(builtin)
		if err != nil {
			t.Fatalf("compileDelivery(%s) error = %v", builtin.Source, err)
		}
		for _, state := range graph.States() {
			node, _ := graph.Node(state)
			selected[node.Action().Name] = true
		}
	}
	registry, err := deliveryRegistry()
	if err != nil {
		t.Fatalf("deliveryRegistry() error = %v", err)
	}
	var unselected []string
	for _, name := range registry.Names() {
		if !selected[name] {
			unselected = append(unselected, name)
		}
	}
	var want []string
	if !slices.Equal(unselected, want) {
		t.Errorf("the built-in definitions select every registered step except %v; they leave out %v", want, unselected)
	}
}

// TestParityCoversEveryRecordedTrace refuses a recorded path this harness says
// nothing about, and a scenario naming a trace nobody records.
//
// It is the check that stops a path being skipped silently, which is the one
// failure a harness of transcripts cannot report by failing to match: a trace
// with no scenario is measured by nothing at all and looks exactly like one that
// passed.
func TestParityCoversEveryRecordedTrace(t *testing.T) {
	t.Parallel()

	recorded, err := filepath.Glob(filepath.Join(baselineDirectory, "*.json"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	measured := map[string]bool{}
	for _, scenario := range parityScenarios() {
		if measured[scenario.trace] {
			t.Errorf("%s is measured by two parity scenarios", scenario.trace)
		}
		measured[scenario.trace] = true
	}
	for _, path := range recorded {
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		if !measured[name] {
			t.Errorf("%s is recorded and no parity scenario walks it; write its transcript or name why no definition can express it", path)
		}
		delete(measured, name)
	}
	for name := range measured {
		t.Errorf("the parity scenario %q names a trace nothing records", name)
	}
}

// TestBuiltinDeliveryDefinitionWalksEveryRecordedPath is the harness itself:
// every recorded path witnessed against its trace, and walked through the
// definition that claims to express it.
func TestBuiltinDeliveryDefinitionWalksEveryRecordedPath(t *testing.T) {
	t.Parallel()

	for _, scenario := range parityScenarios() {
		t.Run(scenario.trace, func(t *testing.T) {
			t.Parallel()
			trace := readParityTrace(t, scenario.trace)
			if scenario.unexpressible != "" {
				witnessNoRun(t, scenario, trace)
				return
			}
			witnessTranscript(t, scenario, trace)
			walkTranscript(t, scenario)
		})
	}
}

//
// The trace, as this harness reads one.
//

// parityTrace is the half of a recorded baseline the witness reads: what the run
// asked the provider for, what the harness itself observed, what became of the
// work item, and the durable counters. Everything else a trace carries is left
// out, because what is being measured here is a sequence.
type parityTrace struct {
	Steps           []parityTracedStep `json:"steps"`
	Reconciliations []map[string]any   `json:"reconciliations"`
	Durable         map[string]any     `json:"durable_run_record"`
	Events          []string           `json:"events"`
	WorkItem        struct {
		Calls   []string `json:"calls"`
		Closed  bool     `json:"closed"`
		Blocked bool     `json:"blocked"`
	} `json:"work_item"`
	Provider []string `json:"provider_invocations"`
}

type parityTracedStep struct {
	Ending string `json:"ending"`
}

func readParityTrace(t *testing.T, name string) parityTrace {
	t.Helper()
	path := filepath.Join(baselineDirectory, name+".json")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	var trace parityTrace
	if err := json.Unmarshal(content, &trace); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", path, err)
	}
	return trace
}

// counter is a durable counter the trace carries, or zero where it carries none:
// a counter nothing spent is absent from the record rather than recorded as nil.
func (p parityTrace) counter(field string) int {
	value, recorded := p.Durable[field]
	if !recorded {
		return 0
	}
	number, isANumber := value.(float64)
	if !isANumber {
		return 0
	}
	return int(number)
}

// invocations is how many of the run's provider calls were made as a role.
func (p parityTrace) invocations(role string) int {
	made := 0
	for _, invocation := range p.Provider {
		if strings.HasPrefix(invocation, role+":") {
			made++
		}
	}
	return made
}

// events is how many of one kind the harness appended, reading back the counting
// the trace does for consecutive repeats.
func (p parityTrace) events(kind string) int {
	appended := 0
	for _, entry := range p.Events {
		name, suffix, repeated := strings.Cut(entry, " x")
		if name != kind {
			continue
		}
		if !repeated {
			appended++
			continue
		}
		count := 0
		if _, err := fmt.Sscanf(suffix, "%d", &count); err != nil {
			continue
		}
		appended += count
	}
	return appended
}

// ending is the outcome word the last invocation of the pipeline was read as,
// which is what the terminal of a transcript has to agree with.
func (p parityTrace) ending() string {
	if len(p.Steps) == 0 {
		return ""
	}
	return p.Steps[len(p.Steps)-1].Ending
}

//
// The witness.
//

// witnessNoRun holds a path that never reached a state to having nothing behind
// it: no run, no claim, and nothing asked of the provider. A trace that recorded
// any of the three would be one whose scenario says it stops before the claim
// and whose evidence says otherwise.
func witnessNoRun(t *testing.T, scenario parityScenario, trace parityTrace) {
	t.Helper()
	if len(scenario.steps) > 0 {
		t.Errorf("%s is named unexpressible and carries a transcript; it is one or the other", scenario.trace)
	}
	if trace.Durable != nil {
		t.Errorf("%s: %s, and the trace records a run", scenario.trace, scenario.unexpressible)
	}
	if len(trace.WorkItem.Calls) > 0 {
		t.Errorf("%s: %s, and the trace records the tracker calls %v", scenario.trace, scenario.unexpressible, trace.WorkItem.Calls)
	}
	if len(trace.Provider) > 0 {
		t.Errorf("%s: %s, and the trace records the provider invocations %v", scenario.trace, scenario.unexpressible, trace.Provider)
	}
}

// witnessTranscript holds a transcript against the trace it claims to describe.
//
// Each rule is one thing the trace records and the transcript therefore has to
// say. Together they pin every state the transcript can contain: the developer
// invocations pin `develop`, the verdicts pin `review`, the commands pin
// `check`, `repair_attempts` pins which of the transitions into the developer
// are repairs, `integration_retries` pins the replays, and where the item ended
// up pins the terminal.
func witnessTranscript(t *testing.T, scenario parityScenario, trace parityTrace) {
	t.Helper()

	developed := 0
	verdicts := 0
	repairs := 0
	replays := 0
	commanded := 0
	previous := ""
	for _, step := range scenario.steps {
		switch step.state {
		case parityDevelop:
			developed++
			if previous == parityCheck || previous == parityReview {
				repairs++
			}
		case parityReview:
			// A review state that was entered and produced no verdict bought
			// nothing: the run stopped at that boundary before the reviewer was
			// asked, which is what the operator-stop path does.
			if step.outcome != "stopped" {
				verdicts++
			}
		case parityCheck:
			// A change refused for what it touched is refused in front of the
			// checks, so that round ran no commands at all.
			if !strings.HasPrefix(step.outcome, "refused") {
				commanded++
			}
		case parityIntegrate:
			if step.outcome == "superseded" {
				replays++
			}
		}
		previous = step.state
	}
	// A step the process died inside performed its action and never recorded an
	// outcome, so what it did is evidenced in the trace and absent from the tally
	// above.
	if scenario.standing == parityCheck {
		commanded++
	}

	if want := trace.invocations("developer"); developed != want {
		t.Errorf("the transcript stands in %s %d time(s) and the trace records %d developer invocation(s)", parityDevelop, developed, want)
	}
	if want := trace.invocations("reviewer"); verdicts != want {
		t.Errorf("the transcript obtains %d verdict(s) and the trace records %d reviewer invocation(s)", verdicts, want)
	}
	if want := trace.counter("review_rounds"); verdicts != want {
		t.Errorf("the transcript obtains %d verdict(s) and the run recorded review_rounds = %d", verdicts, want)
	}
	if want := trace.counter("repair_attempts"); repairs != want {
		t.Errorf("the transcript returns to %s from the gate %d time(s) and the run recorded repair_attempts = %d", parityDevelop, repairs, want)
	}
	if want := trace.counter("integration_retries"); replays != want {
		t.Errorf("the transcript replays the promotion %d time(s) and the run recorded integration_retries = %d", replays, want)
	}
	if want := trace.events("command.started"); commanded != want {
		t.Errorf("the transcript runs the project's commands in %d %s state(s) and the log records %d command.started event(s)", commanded, parityCheck, want)
	}
	witnessEnding(t, scenario, trace)
}

// witnessEnding holds where a transcript ends against what became of the work
// item. The three terminals are three different answers to "what happened to my
// work", and a transcript ending in the wrong one would be a definition that
// reads a blocked run as a delivered one.
func witnessEnding(t *testing.T, scenario parityScenario, trace parityTrace) {
	t.Helper()
	switch {
	case scenario.terminal == "":
		if len(trace.Reconciliations) == 0 {
			t.Errorf("the transcript is left standing in %q and the trace records no settlement; a run nothing finished is settled by a sweep", scenario.standing)
		}
	case scenario.terminal == "delivered":
		if !trace.WorkItem.Closed {
			t.Errorf("the transcript ends in %q and the trace does not record the item closed", scenario.terminal)
		}
	case scenario.terminal == "blocked":
		if !trace.WorkItem.Blocked {
			t.Errorf("the transcript ends in %q and the trace does not record the item blocked", scenario.terminal)
		}
	case scenario.terminal == "preserved":
		if trace.WorkItem.Closed || trace.WorkItem.Blocked {
			t.Errorf("the transcript ends in %q and the trace records the item closed=%t blocked=%t; a change preserved for its approver is neither",
				scenario.terminal, trace.WorkItem.Closed, trace.WorkItem.Blocked)
		}
		if ending := trace.ending(); ending != "succeeded" {
			t.Errorf("the transcript ends in %q and the run was read as %q", scenario.terminal, ending)
		}
	case scenario.terminal == "abandoned":
		if trace.WorkItem.Closed || trace.WorkItem.Blocked {
			t.Errorf("the transcript ends in %q and the trace records the item closed=%t blocked=%t; an abandoned run leaves neither",
				scenario.terminal, trace.WorkItem.Closed, trace.WorkItem.Blocked)
		}
		if ending := trace.ending(); ending == "succeeded" {
			t.Errorf("the transcript ends in %q and the run was read as %q", scenario.terminal, ending)
		}
	default:
		t.Errorf("the transcript ends in %q, which no built-in definition declares", scenario.terminal)
	}
}

//
// The walk.
//

// walkTranscript steps a transcript through the definition that claims to
// express it, and reads the walk back off the durable record.
//
// The record rather than the return value is deliberate: what a resumed process
// would see is the checkpoints, so comparing those is comparing what the
// executor durably said happened rather than what this test watched it do.
func walkTranscript(t *testing.T, scenario parityScenario) {
	t.Helper()

	graph := parityGraph(t, scenario.workflow)
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	grant, err := deliveryGrant()
	if err != nil {
		t.Fatalf("deliveryGrant() error = %v", err)
	}
	stepped := 0
	executor := workflow.Executor[*activeRun]{
		Graph:     graph,
		Instances: store,
		Grant:     grant,
		Outcome: func(state string, _ *activeRun) (string, error) {
			if stepped >= len(scenario.steps) {
				return "", fmt.Errorf("the definition performed %d states and the transcript has %d", stepped+1, len(scenario.steps))
			}
			step := scenario.steps[stepped]
			stepped++
			if state != step.state {
				return "", fmt.Errorf("step %d of the transcript stands in %q and the definition performed %q", stepped, step.state, state)
			}
			return step.outcome, nil
		},
		Now: func() time.Time { return baseTime },
	}

	instance, err := executor.Start(scenario.trace)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if instance.State != parityClaim {
		t.Fatalf("an instance starts in %q; every delivery definition begins at %q", instance.State, parityClaim)
	}
	// The subject is nil because nothing is performed: the doors this graph holds
	// are the registered ones with their Perform replaced, so the walk is over the
	// real topology and touches nothing outside this test.
	for range scenario.steps {
		instance, err = executor.Step(context.Background(), scenario.trace, nil)
		if err != nil {
			t.Fatalf("Step() error = %v", err)
		}
	}
	if stepped != len(scenario.steps) {
		t.Errorf("the definition performed %d states and the transcript has %d", stepped, len(scenario.steps))
	}

	// The checkpoints are the initial state and then one per transition, so the
	// transcript's step i is the crossing recorded at i+1.
	if want := len(scenario.steps) + 1; len(instance.Checkpoints) != want {
		t.Fatalf("the instance recorded %d checkpoint(s) and the transcript is %d transition(s)", len(instance.Checkpoints), want-1)
	}
	for index, step := range scenario.steps {
		crossing := instance.Checkpoints[index+1]
		if crossing.From != step.state || crossing.Outcome != step.outcome {
			t.Errorf("transition %d was recorded as %q on %q and the transcript is %q on %q",
				index+1, crossing.From, crossing.Outcome, step.state, step.outcome)
		}
	}
	switch {
	case scenario.terminal != "":
		if !instance.Terminal || instance.State != scenario.terminal {
			t.Errorf("the walk ended in %q (terminal=%t) and the transcript ends in %q", instance.State, instance.Terminal, scenario.terminal)
		}
	default:
		if instance.Terminal || instance.State != scenario.standing {
			t.Errorf("the walk ended in %q (terminal=%t) and the transcript leaves the instance standing in %q", instance.State, instance.Terminal, scenario.standing)
		}
	}
}

// parityGraph compiles one built-in definition into a graph whose doors perform
// nothing.
//
// It is the same graph the declarative path steps beside a real run, built by
// the same function, which is the point: a harness that walked a topology of its
// own would be measuring something no run is ever observed against. Everything
// about the actions except Perform is the registered thing — the names, the
// summaries, the capabilities and what each wraps come from the table
// `actions.go` builds the delivery registry from — and the digest inside says
// the definition is the shipped one, which is what makes it safe to walk a
// promotion in a unit test.
func parityGraph(t *testing.T, id string) workflow.Graph[*activeRun] {
	t.Helper()

	// No configuration path, so the definition walked is the one this build ships
	// whatever any project keeps of its own: the baseline is what this build's
	// sequence does, and a harness that read a project's file would be measuring
	// the wrong thing.
	graph, err := observedDeliveryGraph(id, "")
	if err != nil {
		t.Fatalf("observedDeliveryGraph(%s) error = %v", id, err)
	}
	return graph
}
