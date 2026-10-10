package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/action"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/rolecapability"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/separation"
)

const runstatePackage = "../runstate"

func TestTheDeliveryRegistryIsBuildable(t *testing.T) {
	t.Parallel()

	registry, err := deliveryRegistry()
	if err != nil {
		t.Fatalf("deliveryRegistry() error = %v", err)
	}
	want := []string{
		"work-item.claim",
		"candidate.develop",
		"candidate.publish",
		"candidate.check",
		"candidate.review",
		"candidate.integrate",
		"run.complete",
		"run.clean-up",
	}
	if names := registry.Names(); !slices.Equal(names, want) {
		t.Errorf("Names() = %v, want %v", names, want)
	}
}

func TestEveryRegisteredActionWrapsAFunctionThisPackageHas(t *testing.T) {
	t.Parallel()

	declared := methodsDeclaredIn(t, ".")
	for _, step := range deliverySteps() {
		if !declared[step.action.Wraps] {
			t.Errorf("%q wraps %s, and this package declares no such method",
				step.action.Name, step.action.Wraps)
		}
	}
}

// sequencers are the functions that decide which steps a delivery run performs
// and in what order. They are the delivery loop's control flow and nothing else:
// everything a run does, it does because one of these called it or because a
// step one of these called did.
//
// `finish` is one of them rather than a step, and that is the whole of what
// covers the completing end of a run. It performs nothing itself — it orders
// `complete` and then `cleanUp`, both registered, and turns what either returns
// into the run's outcome — so an operation that arrives between them is walked
// from here and has to be registered or excused. While it was excused as a step
// of its own, everything inside it was invisible to this test, which is how
// recording the outcome, closing the item and pricing it came to be delivery
// work no definition could express.
//
// `promoteApproved` is one for the same reason: it orders the independence
// check, the last holds, `integrate` and then `finish`, and turns a promotion
// that lost its race into the replay the gate is re-earned from. It is reached
// from the loop and from the integration resume, which is why it is a sequencer
// of its own rather than the tail of `verifyReviewAndFinish`.
var sequencers = []string{
	"(Pipeline).Run",
	"(Pipeline).resumeRun",
	"(Pipeline).PublishDocument",
	"(Pipeline).publishDocumentAttempt",
	"(*activeRun).reviewDocument",
	"(*activeRun).verifyReviewAndFinish",
	"(*activeRun).promoteApproved",
	"(*activeRun).repairLoop",
	"(*activeRun).finish",
}

// TestEveryStepTheDeliveryLoopCallsIsRegistered is the coverage claim, held
// against the pipeline's own calls rather than against a proxy for them.
//
// It walks the sequencers above and every registered step, and collects each
// *activeRun method they call directly. A step acts on a run — that is what the
// registry is a registry of — so an operation added to the delivery loop is a
// call to one of those methods, and it has to be either registered or written
// into the exemption table below with a reason. Adding one and doing neither
// fails here, which is the silent unregistered step this test exists to stop.
//
// Walking one level into each registered step as well as the sequencers is what
// catches the case the registry already contains: publishAttempt is called by
// develop rather than by Run, so a test rooted only at the control flow would
// not see it, and a second step added the same way would slip past for the same
// reason.
func TestEveryStepTheDeliveryLoopCallsIsRegistered(t *testing.T) {
	t.Parallel()

	registered := map[string]string{}
	for _, step := range deliverySteps() {
		registered[step.action.Wraps] = step.action.Name
	}
	roots := slices.Clone(sequencers)
	for wraps := range registered {
		roots = append(roots, wraps)
	}

	called := activeRunMethodsCalledBy(t, roots)
	for _, method := range slices.Sorted(maps.Keys(called)) {
		key := "(*activeRun)." + method
		if _, isStep := registered[key]; isStep {
			continue
		}
		// A sequencer calling another sequencer is the delivery loop's shape rather
		// than a step of it, and it is already named above.
		if slices.Contains(sequencers, key) {
			continue
		}
		if _, excused := notAStep[method]; excused {
			continue
		}
		t.Errorf("the delivery loop calls %s (from %s) and nothing registers it; "+
			"register an action for it, or add it to notAStep with the reason it is not a step",
			key, strings.Join(called[method], ", "))
	}

	// An exemption for something nothing calls any more is a sentence that has
	// stopped being true, and the next person reads it as a statement about code
	// that is still there.
	for method := range notAStep {
		if _, isCalled := called[method]; !isCalled {
			t.Errorf("notAStep excuses %q and the delivery loop no longer calls it; remove the exemption", method)
		}
	}
	// And an exemption for something that is registered is two answers to one
	// question.
	for method := range notAStep {
		if name, isStep := registered["(*activeRun)."+method]; isStep {
			t.Errorf("notAStep excuses %q and %q registers it", method, name)
		}
	}
}

// TestAStepDeclaresWhatTheStepsInsideItRequire holds a declaration to what
// performing it actually reaches.
//
// Capabilities are what an action requires, not what the lines of its own
// function require, and one step calling another is where the two come apart:
// candidate.develop ends by calling publishAttempt, so going through its door
// reaches the forge even though nothing in develop's own body mentions it. A
// declaration that missed that would understate the authority the action needs
// in the one place authority is written down, which is worse than not writing it
// down at all.
//
// The claim is only about steps calling steps, because that is the case where
// the answer is already recorded: the inner action has declared what it needs,
// so the outer one can be held to it without anybody having to re-derive it.
func TestAStepDeclaresWhatTheStepsInsideItRequire(t *testing.T) {
	t.Parallel()

	steps := deliverySteps()
	byWraps := map[string]action.Action[*activeRun]{}
	for _, step := range steps {
		byWraps[step.action.Wraps] = step.action
	}
	for _, step := range steps {
		for _, method := range slices.Sorted(maps.Keys(activeRunMethodsCalledBy(t, []string{step.action.Wraps}))) {
			inner, isStep := byWraps["(*activeRun)."+method]
			if !isStep || inner.Name == step.action.Name {
				continue
			}
			for _, required := range inner.Capabilities {
				if !slices.Contains(step.action.Capabilities, required) {
					t.Errorf("%q performs %q and does not declare %q, which %q requires",
						step.action.Name, inner.Name, required, inner.Name)
				}
			}
		}
	}
}

// TestEveryCapabilityTheseActionsRequireHasAHolder joins the two registries the
// authority workstream builds: what an action requires, and who holds it.
//
// Nothing checks one against the other at run time yet — a definition is compiled
// under a grant its caller assembled, not under any role's bundle — so this is the
// claim that the join will be possible at all. A capability a step requires and no
// role holds is authority nothing could ever satisfy, and the two capabilities the
// promotion needs are held by the harness rather than by a role, which is the
// answer the invariant demands rather than a hole in the table.
func TestEveryCapabilityTheseActionsRequireHasAHolder(t *testing.T) {
	t.Parallel()

	holders, err := rolecapability.Default()
	if err != nil {
		t.Fatalf("rolecapability.Default() error = %v", err)
	}
	for _, step := range deliverySteps() {
		for _, required := range step.action.Capabilities {
			if len(holders.RolesHolding(required)) > 0 {
				continue
			}
			if _, harness := holders.HarnessHolds(required); harness {
				continue
			}
			t.Errorf("%q requires %q and nothing holds it: no role's bundle carries it and it is not recorded as the harness's own",
				step.action.Name, required)
		}
	}
}

// TestEveryRegisteredActionPassesTheSeparationPolicies is the parity claim for
// the separation workstream, made where a definition cannot reach: the compiler
// holds the actions a definition selects to these policies, and this holds every
// action the harness registers to them whether a definition selects it or not.
//
// It is worth having separately because the compiler only ever sees what was
// selected. An action registered with a combination the policies refuse would sit
// in the registry unnoticed until the first definition named it, and the point of
// writing the rules over the vocabulary is that they can be asked of the table
// itself.
func TestEveryRegisteredActionPassesTheSeparationPolicies(t *testing.T) {
	t.Parallel()

	for _, step := range deliverySteps() {
		operation := separation.Operation{Name: step.action.Name, Requires: step.action.Capabilities}
		if err := separation.CheckOperation("the registered action", operation); err != nil {
			t.Errorf("%q: %v", step.action.Name, err)
		}
	}
}

// TestTheRolesThatAuthorizeAPromotionCannotPerformOne is the role half of the
// same rule, held against the bundles this repository ships.
func TestTheRolesThatAuthorizeAPromotionCannotPerformOne(t *testing.T) {
	t.Parallel()

	holders, err := rolecapability.Default()
	if err != nil {
		t.Fatalf("rolecapability.Default() error = %v", err)
	}
	if err := separation.CheckHolders(holders); err != nil {
		t.Errorf("CheckHolders() error = %v", err)
	}
}

// TestEveryRunPhaseHasARegisteredAction is the second guard, and it catches what
// the first cannot: a step whose call the walk above sees as ordinary control
// flow, but which a run can be found sitting in. A run's phase is the durable
// record of the step it reached, so a phase no registered action names is a
// place a run stops that this registry says nothing about.
//
// It is the second guard and not the first, because a phase is a coarser thing
// than a step: a phase two steps run in is covered by either of them, so a step
// can go missing under a phase somebody else's step already names. That is what
// happened to the completing phase, which candidate.integrate claimed because
// promoting a change ends by recording it. The coverage claim is the walk above,
// held against what the loop calls; this holds the durable record to it.
func TestEveryRunPhaseHasARegisteredAction(t *testing.T) {
	t.Parallel()

	covering := map[runstate.Phase][]string{}
	for _, step := range deliverySteps() {
		for _, phase := range step.phases {
			covering[phase] = append(covering[phase], step.action.Name)
		}
	}
	declared := phasesDeclaredInRunstate(t)
	for _, phase := range declared {
		// The terminal phase is where a run has finished rather than a step it
		// performs, so nothing registers an action for it. It is exempted by name so
		// that a reader can see it was decided rather than missed.
		if phase == runstate.PhaseComplete {
			if named := covering[phase]; len(named) > 0 {
				t.Errorf("%v registered for the terminal phase %q, which is not a step", named, phase)
			}
			continue
		}
		if len(covering[phase]) == 0 {
			t.Errorf("runstate declares the phase %q and no registered action names it; a run reaches a step this registry does not cover", phase)
		}
	}
	for phase, named := range covering {
		if !slices.Contains(declared, phase) {
			t.Errorf("%v name the phase %q, which runstate does not declare", named, phase)
		}
	}
}

// TestPerformingClaimReachesTheClaim is what makes the door more than
// documentation, and it pins the one thing the extraction of claim out of Run
// could have changed: the run has to end up holding the item the tracker
// returned from the claim, not the one Run read before it.
func TestPerformingClaimReachesTheClaim(t *testing.T) {
	t.Parallel()

	const itemID = "yoyodyne-ifd.209.2"
	registry, err := deliveryRegistry()
	if err != nil {
		t.Fatalf("deliveryRegistry() error = %v", err)
	}
	claim, found := registry.Lookup("work-item.claim")
	if !found {
		t.Fatal(`Lookup("work-item.claim") found nothing`)
	}

	// The item the tracker holds before the claim is open, and
	// orchestratortest.Tracker's claim is what moves it to in_progress. So a run
	// left holding an open item is a run that kept what Run read rather than what
	// the claim returned.
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
		ID:     itemID,
		Title:  "Action and capability registries wrapping the existing pipeline steps",
		Status: "open",
	}}
	run := &activeRun{
		pipeline: Pipeline{Tracker: tracker, Repository: t.TempDir()},
		state:    runstate.State{WorkItemID: itemID},
	}
	if err := claim.Perform(context.Background(), run); err != nil {
		t.Fatalf("Perform() error = %v", err)
	}
	if !run.claimed {
		t.Error("the run does not hold the item it claimed")
	}
	if run.item.Status != "in_progress" {
		t.Errorf("run.item.Status = %q, want in_progress: the run kept the item from before the claim", run.item.Status)
	}
	if run.context == "" {
		t.Error("the run was given no context to hand its developer")
	}
	if !strings.Contains(run.context, itemID) {
		t.Errorf("the run's context does not name %s", itemID)
	}
}

func TestPerformingARefusedClaimReportsTheRefusal(t *testing.T) {
	t.Parallel()

	registry, err := deliveryRegistry()
	if err != nil {
		t.Fatalf("deliveryRegistry() error = %v", err)
	}
	claim, _ := registry.Lookup("work-item.claim")
	refused := errors.New("the tracker refused")
	run := &activeRun{
		pipeline: Pipeline{Tracker: &orchestratortest.Tracker{OnClaim: func() error { return refused }}},
		state:    runstate.State{WorkItemID: "yoyodyne-ifd.209.2"},
	}
	err = claim.Perform(context.Background(), run)
	if !errors.Is(err, refused) {
		t.Fatalf("Perform() error = %v, want the tracker's refusal", err)
	}
	if !strings.Contains(err.Error(), "claim work item") {
		t.Errorf("Perform() error = %v, and it is not the one claim wraps", err)
	}
	if run.claimed {
		t.Error("a refused claim left the run holding the item")
	}
}

// TestPerformingCompleteClosesAndPricesTheItem is what makes the completing door
// worth having: a run driven through it has to finish with its work item exactly
// as the hard-coded loop leaves it.
//
// The parity harness cannot make this claim. It walks the real topology with
// every door performing nothing, so it measures the sequence and never what a
// step does — which is how a definition that promoted a change and never closed
// its item would have walked it clean.
func TestPerformingCompleteClosesAndPricesTheItem(t *testing.T) {
	t.Parallel()

	const itemID = "yoyodyne-ifd.209.18"
	registry, err := deliveryRegistry()
	if err != nil {
		t.Fatalf("deliveryRegistry() error = %v", err)
	}
	complete, found := registry.Lookup("run.complete")
	if !found {
		t.Fatal(`Lookup("run.complete") found nothing`)
	}

	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	state := completingRun(itemID, runstate.PhaseCompleting)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: itemID, Status: "in_progress"}}
	prices := &orchestratortest.Pricer{Cost: beads.Cost{TotalUSD: 3.50, Runs: 1}}
	run := &activeRun{
		pipeline: Pipeline{Tracker: tracker, Prices: prices, Store: store},
		claimed:  true,
		state:    state,
		outcome: Outcome{Integration: &gitworktree.Integration{
			TargetBranch: "main",
			SourceCommit: "b0bb1e5",
		}},
	}
	if err := complete.Perform(context.Background(), run); err != nil {
		t.Fatalf("Perform() error = %v", err)
	}

	if want := []string{"record", "complete"}; !slices.Equal(tracker.Calls, want) {
		t.Errorf("the tracker was asked for %v, want %v", tracker.Calls, want)
	}
	if !tracker.Closed {
		t.Error("the promoted item was not closed")
	}
	if !run.outcome.WorkItemClosed {
		t.Error("the run does not report the item as closed")
	}
	if want := []string{itemID}; !slices.Equal(prices.Priced, want) {
		t.Errorf("the run priced %v, want %v", prices.Priced, want)
	}
	if run.outcome.Cost == nil || run.outcome.Cost.TotalUSD != 3.50 {
		t.Errorf("the run reports the cost %v, and the ledger priced it at 3.50", run.outcome.Cost)
	}
	// The run is durably terminal before anything is removed, with the cleanup
	// still to do. That boundary is what a definition's next state stands on.
	if run.state.Status != runstate.StatusSucceeded {
		t.Errorf("run.state.Status = %q, want %q", run.state.Status, runstate.StatusSucceeded)
	}
	if run.state.Phase != runstate.PhaseCleaningUp {
		t.Errorf("run.state.Phase = %q, want %q", run.state.Phase, runstate.PhaseCleaningUp)
	}
	if run.state.CompletedAt == nil {
		t.Error("the run was not recorded as completed")
	}
	if run.state.WorktreeRemoved || run.state.BranchRemoved {
		t.Error("completing removed an artifact; removing them is run.clean-up")
	}
	saved, err := store.Load(run.state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if saved.Status != runstate.StatusSucceeded || saved.Phase != runstate.PhaseCleaningUp {
		t.Errorf("the durable record is %q in %q; a process that died here would resume the cleanup",
			saved.Status, saved.Phase)
	}
}

// TestPerformingCompleteOnAnUnpromotedChangeRecordsWithoutClosing is the other
// half of the same door, which the human-approval definition selects: a change
// nobody promoted is recorded and priced, and the item is left for the person
// who approves the promotion.
func TestPerformingCompleteOnAnUnpromotedChangeRecordsWithoutClosing(t *testing.T) {
	t.Parallel()

	const itemID = "yoyodyne-ifd.209.18"
	registry, err := deliveryRegistry()
	if err != nil {
		t.Fatalf("deliveryRegistry() error = %v", err)
	}
	complete, _ := registry.Lookup("run.complete")
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	state := completingRun(itemID, runstate.PhaseChecking)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: itemID, Status: "in_progress"}}
	prices := &orchestratortest.Pricer{}
	run := &activeRun{
		pipeline: Pipeline{Tracker: tracker, Prices: prices, Store: store},
		claimed:  true,
		state:    state,
	}
	if err := complete.Perform(context.Background(), run); err != nil {
		t.Fatalf("Perform() error = %v", err)
	}
	if want := []string{"record"}; !slices.Equal(tracker.Calls, want) {
		t.Errorf("the tracker was asked for %v, want %v; nothing was promoted", tracker.Calls, want)
	}
	if tracker.Closed {
		t.Error("the item was closed and nobody has promoted the change")
	}
	if want := []string{itemID}; !slices.Equal(prices.Priced, want) {
		t.Errorf("the run priced %v, want %v", prices.Priced, want)
	}
	// There is nothing to clean up after, so this is where the run ends.
	if run.state.Phase != runstate.PhaseComplete {
		t.Errorf("run.state.Phase = %q, want %q", run.state.Phase, runstate.PhaseComplete)
	}
}

// completingRun is a run about to be completed, valid enough for the store to
// take: the fields runstate requires, and the phase the step is entered from.
func completingRun(itemID string, phase runstate.Phase) runstate.State {
	started := baseTime
	return runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         pipelineRunID,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    itemID,
		WorkItemTitle: "A registered run.complete action",
		Backend:       domain.BackendClaudeCode,
		Status:        runstate.StatusRunning,
		Phase:         phase,
		StartedAt:     started,
		UpdatedAt:     started,
	}
}

// cleanedWorktreeManager removes everything it is asked to. It is
// orchestratortest.PartialWorktreeManager with the one method the cleanup step
// calls answered, because every other method it inherits refuses and none of
// them is reached.
type cleanedWorktreeManager struct {
	orchestratortest.PartialWorktreeManager
}

func (cleanedWorktreeManager) CleanupIntegrated(context.Context, gitworktree.CleanupRequest) (gitworktree.Cleanup, error) {
	return gitworktree.Cleanup{WorktreeRemoved: true, BranchRemoved: true}, nil
}

// TestPerformingCleanUpRecordsTheRunAsComplete holds the other end of the
// completing pair: a run driven through this door has to end where the
// hard-coded loop ends it.
//
// The terminal phase is the last thing a delivery run writes, and it is written
// here rather than beside the call for exactly this reason — a definition that
// ordered complete and then clean-up would otherwise leave every run it drove
// sitting in cleaning_up with nothing left to clean up.
func TestPerformingCleanUpRecordsTheRunAsComplete(t *testing.T) {
	t.Parallel()

	registry, err := deliveryRegistry()
	if err != nil {
		t.Fatalf("deliveryRegistry() error = %v", err)
	}
	cleanUp, _ := registry.Lookup("run.clean-up")
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	// The run as run.complete leaves it: succeeded, with the cleanup outstanding.
	state := completingRun("yoyodyne-ifd.209.18", runstate.PhaseCleaningUp)
	completedAt := baseTime
	state.Status = runstate.StatusSucceeded
	state.CompletedAt = &completedAt
	// A removed artifact is only valid against a recorded promotion, and a
	// recorded promotion is only valid with the approval and the two independent
	// invocations that authorized it. All of it is what run.complete leaves
	// behind; none of it is what this test is about.
	commit := strings.Repeat("b", 40)
	state.WorktreePath = t.TempDir()
	state.Branch = "yoyodyne/task/209-18"
	state.BaseCommit = strings.Repeat("a", 40)
	state.ReviewDecision = runstate.ReviewApprove
	state.ProviderSessionID = "developer-session"
	state.ProviderModel = "opus"
	state.ReviewSessionID = "reviewer-session"
	state.ReviewModel = "opus"
	state.Integration = &runstate.Integration{
		TargetBranch:         "main",
		SourceCommit:         commit,
		TargetCommit:         commit,
		PreviousTargetCommit: strings.Repeat("d", 40),
	}
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	run := &activeRun{
		pipeline: Pipeline{Worktrees: cleanedWorktreeManager{}, Store: store},
		state:    state,
		outcome: Outcome{Integration: &gitworktree.Integration{
			TargetBranch: "main",
			SourceCommit: "b0bb1e5",
		}},
	}
	if err := cleanUp.Perform(context.Background(), run); err != nil {
		t.Fatalf("Perform() error = %v", err)
	}

	if !run.state.WorktreeRemoved || !run.state.BranchRemoved {
		t.Errorf("the run records worktree_removed=%t branch_removed=%t; both were removed",
			run.state.WorktreeRemoved, run.state.BranchRemoved)
	}
	if run.state.Phase != runstate.PhaseComplete {
		t.Errorf("run.state.Phase = %q, want %q", run.state.Phase, runstate.PhaseComplete)
	}
	if run.outcome.Phase != runstate.PhaseComplete {
		t.Errorf("run.outcome.Phase = %q, want %q", run.outcome.Phase, runstate.PhaseComplete)
	}
	saved, err := store.Load(run.state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if saved.Phase != runstate.PhaseComplete {
		t.Errorf("the durable record is in %q; a run nothing is left of is complete", saved.Phase)
	}
}

func TestPerformingCleanUpReachesTheCleanUp(t *testing.T) {
	t.Parallel()

	registry, err := deliveryRegistry()
	if err != nil {
		t.Fatalf("deliveryRegistry() error = %v", err)
	}
	cleanUp, found := registry.Lookup("run.clean-up")
	if !found {
		t.Fatal(`Lookup("run.clean-up") found nothing`)
	}
	run := &activeRun{
		pipeline: Pipeline{Worktrees: orchestratortest.PartialWorktreeManager{}},
		outcome: Outcome{Integration: &gitworktree.Integration{
			TargetBranch: "main",
			SourceCommit: "b0bb1e5",
		}},
	}
	err = cleanUp.Perform(context.Background(), run)
	if err == nil {
		t.Fatal("Perform() returned no error, and the worktree manager cannot clean up")
	}
	if !strings.Contains(err.Error(), "clean up integrated run artifacts") {
		t.Errorf("Perform() error = %v, and it is not the one cleanUp wraps", err)
	}
}

// TestPerformingIntegrateRefusesAChangeTheRecordDoesNotShowPassedItsGate is the
// promotion reading its own gate off the record rather than trusting whoever
// called it. The repair loop orders the checks in front of the promotion, and
// that is a property of one caller; a definition that routed straight to this
// door has to be refused here on the evidence — and refused before the lease is
// taken or the phase is written, so a refusal leaves the run exactly as the
// reviewer left it. This door is the only route into a promotion: a resumed run
// re-enters the repair loop and re-earns the gate before it reaches here, and
// reconciliation never promotes — recoverIntegration only records a promotion
// the repository already shows, and refuses that without an approving verdict.
//
// yoyodyne-ifd.362 asked for this after five landings carried a test that was
// red on the forge: the check phase had run and exited 0 on every one of them,
// so the gate was never bypassed — but a gate that holds by control flow alone
// had nothing on the record to say so, and nothing to refuse a route that skips
// the loop.
func TestPerformingIntegrateRefusesAChangeTheRecordDoesNotShowPassedItsGate(t *testing.T) {
	t.Parallel()

	registry, err := deliveryRegistry()
	if err != nil {
		t.Fatalf("deliveryRegistry() error = %v", err)
	}
	integrate, found := registry.Lookup("candidate.integrate")
	if !found {
		t.Fatal(`Lookup("candidate.integrate") found nothing`)
	}
	const checkedCommit = "c0ffee01c0ffee01c0ffee01c0ffee01c0ffee01"
	earned := &runstate.ChecksPassed{Content: orchestratortest.PartialContentIdentity, Attempt: 1, Commit: checkedCommit, Commands: []string{"make test"}, At: baseTime}
	for name, test := range map[string]struct {
		shape func(*runstate.State)
		want  string
	}{
		"a failing check still recorded": {
			shape: func(s *runstate.State) {
				s.ChecksPassed = nil
				s.CheckFailure = &runstate.CheckFailure{Command: "make test", ExitCode: 2}
			},
			want: "make test exited with 2",
		},
		"a protected-path refusal still recorded": {
			shape: func(s *runstate.State) {
				s.PathRefusal = &runstate.PathRefusal{Paths: []string{".yoyodyne/config.yaml"}}
			},
			want: "protected-path refusal",
		},
		"no passing checks recorded at all": {
			shape: func(s *runstate.State) { s.ChecksPassed = nil },
			want:  "no configured check is recorded as having passed",
		},
		"checks that passed over an earlier attempt": {
			shape: func(s *runstate.State) { s.RepairAttempts = 2 },
			want:  "passed over attempt 1 and the change being promoted is attempt 2",
		},
		"checks that passed at a different commit": {
			shape: func(s *runstate.State) { s.HarnessCommit = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef" },
			want:  `passed at commit "` + checkedCommit + `" and the change being promoted is at "deadbeef`,
		},
		// The binding that holds where nothing was committed: the worktree names
		// a different change from the one the checks passed over.
		"checks that passed over different content": {
			shape: func(s *runstate.State) {
				passed := *earned
				passed.Content = gitworktree.ContentIdentityPrefix + strings.Repeat("f", 64)
				s.ChecksPassed = &passed
			},
			want: "passed over content " + gitworktree.ContentIdentityPrefix + strings.Repeat("f", 64) + " and the change being promoted is " + orchestratortest.PartialContentIdentity,
		},
		"no approving verdict": {
			shape: func(s *runstate.State) { s.ReviewDecision = runstate.ReviewRepair },
			want:  "rather than an approval",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
			if err != nil {
				t.Fatalf("runstate.NewStore() error = %v", err)
			}
			state := gatedRun(t, earned)
			test.shape(&state)
			if err := store.Create(state); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			run := &activeRun{
				pipeline: Pipeline{Worktrees: orchestratortest.PartialWorktreeManager{}, Store: store},
				claimed:  true,
				state:    state,
				worktree: gitworktree.Worktree{TargetBranch: "main"},
			}
			err = integrate.Perform(context.Background(), run)
			if !errors.Is(err, ErrIntegrationUnearned) {
				t.Fatalf("Perform() error = %v, want %v", err, ErrIntegrationUnearned)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("Perform() error = %v, want it to say %q", err, test.want)
			}
			// Nothing was taken and nothing was written: the run is where the
			// reviewer left it, and the record says so.
			if run.state.Phase != runstate.PhaseReviewing || run.state.Integration != nil {
				t.Errorf("a refused promotion moved the run to %q with integration %v", run.state.Phase, run.state.Integration)
			}
			saved, err := store.Load(state.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if saved.Phase != runstate.PhaseReviewing {
				t.Errorf("the durable record is in %q; a refused promotion wrote a phase it never earned", saved.Phase)
			}
		})
	}

	// The control: the same record with the gate earned passes the reading and
	// reaches the promotion itself, which is what proves the refusals above are
	// the gate and not the manager refusing everything.
	t.Run("checks that passed over exactly this attempt", func(t *testing.T) {
		t.Parallel()

		store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
		if err != nil {
			t.Fatalf("runstate.NewStore() error = %v", err)
		}
		state := gatedRun(t, earned)
		if err := store.Create(state); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		run := &activeRun{
			pipeline: Pipeline{Worktrees: orchestratortest.PartialWorktreeManager{}, Store: store},
			claimed:  true,
			state:    state,
			worktree: gitworktree.Worktree{TargetBranch: "main"},
		}
		err = integrate.Perform(context.Background(), run)
		if errors.Is(err, ErrIntegrationUnearned) {
			t.Fatalf("Perform() refused a change whose record shows it earned the promotion: %v", err)
		}
		if err == nil || !strings.Contains(err.Error(), "partial worktree cannot be integrated") {
			t.Fatalf("Perform() error = %v, want the promotion itself to have been reached", err)
		}
	})
}

// gatedRun is a reviewed run standing in front of its promotion, with the
// record showing the gate earned for exactly the attempt and commit it holds.
func gatedRun(t *testing.T, earned *runstate.ChecksPassed) runstate.State {
	t.Helper()
	state := completingRun("yoyodyne-ifd.362", runstate.PhaseReviewing)
	state.WorktreePath = filepath.Join(t.TempDir(), "worktree")
	state.Branch = "yoyodyne/yoyodyne-ifd-362/0123456789abcdef"
	state.BaseCommit = "0123456789abcdef0123456789abcdef01234567"
	state.TargetBranch = "main"
	state.RepairAttempts = 1
	state.HarnessCommit = earned.Commit
	state.ReviewDecision = runstate.ReviewApprove
	state.ChecksPassed = earned
	return state
}

// parseNonTestFiles is every non-test Go file in a directory, parsed. Test files
// are left out throughout: an action wrapping something only the tests have
// would be a door onto code no run executes.
func parseNonTestFiles(t *testing.T, directory string) []*ast.File {
	t.Helper()

	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read %s: %v", directory, err)
	}
	set := token.NewFileSet()
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(set, filepath.Join(directory, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, parsed)
	}
	if len(files) == 0 {
		t.Fatalf("%s holds no non-test Go files; the test is reading the wrong directory", directory)
	}
	return files
}

// methodsDeclaredIn is every method a directory's package declares, keyed the
// way an action's Wraps writes it.
func methodsDeclaredIn(t *testing.T, directory string) map[string]bool {
	t.Helper()

	declared := map[string]bool{}
	for _, file := range parseNonTestFiles(t, directory) {
		for _, declaration := range file.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if !isFunction || function.Recv == nil || len(function.Recv.List) != 1 {
				continue
			}
			receiver, named := receiverName(function.Recv.List[0].Type)
			if !named {
				continue
			}
			declared[fmt.Sprintf("(%s).%s", receiver, function.Name.Name)] = true
		}
	}
	if len(declared) == 0 {
		t.Fatalf("%s declares no methods; the test is reading the wrong directory", directory)
	}
	return declared
}

// activeRunMethodsCalledBy is every *activeRun method called directly from each
// of the named functions, and which of them calls it.
//
// A call is recognized by its selector alone — the method name — and kept only
// when this package declares an *activeRun method of that name. That is what
// keeps `p.Store.Save(...)` and `lease.Release()` out of the answer without the
// test having to resolve types: the collaborators' methods are named differently
// from the run's, and one that were not would be a name worth looking at anyway.
func activeRunMethodsCalledBy(t *testing.T, roots []string) map[string][]string {
	t.Helper()

	files := parseNonTestFiles(t, ".")
	bodies := map[string]*ast.FuncDecl{}
	onTheRun := map[string]bool{}
	for _, file := range files {
		for _, declaration := range file.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if !isFunction || function.Recv == nil || len(function.Recv.List) != 1 {
				continue
			}
			receiver, named := receiverName(function.Recv.List[0].Type)
			if !named {
				continue
			}
			bodies[fmt.Sprintf("(%s).%s", receiver, function.Name.Name)] = function
			if receiver == "*activeRun" {
				onTheRun[function.Name.Name] = true
			}
		}
	}
	called := map[string][]string{}
	for _, root := range roots {
		body, found := bodies[root]
		if !found {
			t.Fatalf("%s is named as a root and this package declares no such method", root)
		}
		ast.Inspect(body, func(node ast.Node) bool {
			call, isCall := node.(*ast.CallExpr)
			if !isCall {
				return true
			}
			selector, isSelector := call.Fun.(*ast.SelectorExpr)
			if !isSelector || !onTheRun[selector.Sel.Name] {
				return true
			}
			if !slices.Contains(called[selector.Sel.Name], root) {
				called[selector.Sel.Name] = append(called[selector.Sel.Name], root)
			}
			return true
		})
	}
	return called
}

// receiverName renders a method receiver the way Wraps writes it: `*activeRun`
// for a pointer receiver and `Pipeline` for a value one. Anything else — a
// generic receiver, say — is not named rather than guessed at.
func receiverName(expression ast.Expr) (string, bool) {
	switch receiver := expression.(type) {
	case *ast.Ident:
		return receiver.Name, true
	case *ast.StarExpr:
		pointed, isIdent := receiver.X.(*ast.Ident)
		if !isIdent {
			return "", false
		}
		return "*" + pointed.Name, true
	default:
		return "", false
	}
}

// phasesDeclaredInRunstate is every run phase, read from every non-test file in
// the package that declares them rather than from a list kept here. A list here
// would be a second thing to update, and a step added with a new phase and no
// action for it would update neither.
func phasesDeclaredInRunstate(t *testing.T) []runstate.Phase {
	t.Helper()

	var phases []runstate.Phase
	for _, file := range parseNonTestFiles(t, runstatePackage) {
		for _, declaration := range file.Decls {
			general, isGeneral := declaration.(*ast.GenDecl)
			if !isGeneral || general.Tok != token.CONST {
				continue
			}
			for _, specification := range general.Specs {
				value, isValue := specification.(*ast.ValueSpec)
				if !isValue {
					continue
				}
				named, isNamed := value.Type.(*ast.Ident)
				if !isNamed || named.Name != "Phase" {
					continue
				}
				for _, assigned := range value.Values {
					literal, isLiteral := assigned.(*ast.BasicLit)
					if !isLiteral || literal.Kind != token.STRING {
						continue
					}
					unquoted, err := strconv.Unquote(literal.Value)
					if err != nil {
						t.Fatalf("read the value of a Phase constant in %s: %v", runstatePackage, err)
					}
					phases = append(phases, runstate.Phase(unquoted))
				}
			}
		}
	}
	if len(phases) == 0 {
		t.Fatalf("%s declares no Phase constants; the test is reading the wrong thing", runstatePackage)
	}
	return phases
}

// notAStep is every *activeRun method the delivery loop calls that is not a step
// of it, and why.
//
// It is a table rather than a heuristic because the distinction is a judgement
// somebody made: everything here was decided not to be an operation a workflow
// definition should be able to order, gate, or skip. Writing the judgement down
// is the point — a method that arrives and is neither registered nor excused
// fails the test, so the next person has to make the same judgement out loud
// instead of the question never being asked.
var notAStep = map[string]string{
	// How a run ends. None of these advances the work; they turn a stopped,
	// refused, or finished step into the outcome the run reports, and a definition
	// that could reorder them would be reordering the reporting rather than the
	// delivery.
	"fail":                       "turns a failure into the run's outcome",
	"stop":                       "turns a stopped step into the run's outcome, which for a pause or a hold leaves the run in flight",
	"endPromotion":               "turns a failed promotion into the run's outcome, through stop where the hosting session cancelled the run for its redeploy and through fail otherwise",
	"blockOnFailingCheck":        "hands a spent repair budget to a person",
	"blockOnRefusedPaths":        "hands a spent repair budget to a person",
	"blockOnUnresolvedFindings":  "hands a spent repair budget to a person",
	"blockOnMissingVerification": "hands a spent repair budget to a person",
	"blockOnSpentRelaunchBudget": "hands a spent relaunch budget to a person",
	"replayStopEnds":             "hands a spent integration budget to a person, on a replay that stopped on the change",
	"blockOnReviewBound":         "hands a change too large for the reviewer's copy to a person, with its file sizes",
	"chargeReplayStop":           "spends one of the run's charged replays before a replayed change is handed back",

	// The runtime envelope. Holds, directives, dependency waits, operator stops
	// and provider pauses are guarantees wrapped around every step rather than
	// steps between them, and the design puts them outside what a definition can
	// reach for exactly that reason: a sequence that could omit one would be a
	// sequence that could spend through a pause the operator placed.
	"stopRequested":           "reads whether the operator has asked this run to stop",
	"holdForOperator":         "waits out the operator's hold on spending",
	"holdForDirective":        "waits out a directive that pauses this work",
	"holdForDependency":       "waits out work this item was made to depend on",
	"pauseForUsageLimit":      "waits out a provider usage limit",
	"pauseForServerOverload":  "waits out a provider that could not serve the invocation",
	"pauseForProviderOutage":  "waits out a provider nobody is logged into or nobody can reach, spending nothing",
	"awaitRecordedUsageLimit": "serves a usage-limit deadline an earlier process recorded",

	// Developer routing (developerrouting.go): which endpoint of a pinned pair a
	// developer invocation runs on, and the record of each attempt made there.
	// They are part of the one developer invocation candidate.develop already
	// is, as the account and model it reads are, rather than steps beside it.
	"pinDeveloperRouting":        "records the run's developer slot and pinned endpoint pair before the claim",
	"beginDeveloperOperation":    "finds or opens the logical operation a developer invocation serves",
	"prepareDeveloperAttempt":    "reserves and gates the next attempt of the developer invocation",
	"finishDeveloperAttempt":     "records how a developer attempt ended",
	"switchDeveloperEndpoint":    "moves a developer operation to its alternate on a usage limit, or records why it waits",
	"selectedDeveloperEndpoint":  "reads which endpoint the developer operation has selected",
	"completeDeveloperOperation": "closes the logical operation the developer has answered",
	"clearDirectivePause":        "consumes a directive pause the run recorded",
	"clearDependencyPause":       "consumes a dependency pause the run recorded",
	"clearTrackerPause":          "consumes a tracker park the run recorded, and gives its recovery window back",
	"clearOperatorHold":          "consumes an operator hold the run recorded",
	"recordProviderStop":         "records that the harness stopped the provider on time",
	"responseCause":              "records machine observations of an interrupted provider response without advancing the work; it diagnoses an invocation and cannot be reordered or skipped as a delivery step",
	"recordRelaunch":             "spends one of the run's relaunches",
	"mayRelaunch":                "reads whether the run has a relaunch left",
	"recovering":                 "waits out a failure whose class says the next attempt may well succeed, and asks the same boundary again",
	"recoverProvider":            "waits out a provider death whose class says the next invocation may well succeed",
	"carrySession":               "keeps the session an ended invocation established, so the next one resumes in it",
	"recordEnvironmentalRefusal": "records that the machine, not the work, refused the round",

	// The environment an invocation is made in, prepared before the first one and
	// again by any process that resumes the run. It is not a step of the delivery
	// and could not be reordered into one: it changes nothing about the work, and
	// what it creates is outside the worktree and can never enter the change.
	"prepareScratch": "cuts the run the scratch directory its developer contract names",
	// What a fresh worktree starts from is part of cutting it, and it is decided
	// by the run's recorded selection rather than by anything a definition could
	// order: a re-run of a raise starts from the raising run's preserved change,
	// every other run from the target branch alone, and nothing has been
	// delivered yet either way.
	"liftPreserved": "starts the worktree from the preserved change the run's selection names",

	// After the delivery. The landing checks run once the run is terminal, its
	// item settled and its artifacts gone, over the target branch rather than
	// over the change, and nothing they find changes what the run recorded: a
	// red landing is reported and filed as its own work, never a verdict on the
	// run that landed it. A definition that could order them would be ordering
	// something after the run it defines has ended.
	"runLandingChecks": "runs the landing checks over the integrated commit once the run is over, and files a red landing as its own work",
	// The same holds of naming the running parts that cannot read what landed:
	// it runs once the run is over, reads the parts' records, and changes nothing
	// the run recorded.
	"nameUnreadingParts": "saves the running parts a landing leaves unable to read configuration keys and attempts delivery before completing the run",

	// Inside a step rather than beside one. Actions are coarse by design — a
	// promotion is one operation that takes the lease, checks the remote, moves the
	// branch and merges the request — so the parts of a registered step are not
	// separately orderable and must not become so.
	"attemptDevelopment":         "one provider invocation inside candidate.develop",
	"commitAttempt":              "records what one developer invocation left in the worktree, inside candidate.develop",
	"attemptReview":              "one provider invocation inside candidate.review",
	"recordReviewVerdict":        "records a verdict against the item, charging a round where it sent the work back, inside candidate.review",
	"gateProtectedPaths":         "the scope refusal candidate.check makes before it spends a suite",
	"gateReviewBound":            "the review-bound refusal candidate.check makes before it spends a suite or a review",
	"integrationEarned":          "the reading of the gate off the record that candidate.integrate makes before it takes anything",
	"gateCandidateVerification":  "the execution-evidence refusal candidate.check makes; a conversation author has no execution tools, so its document still receives the configured checks",
	"validateIndependentReview":  "checks the author and reviewer identities before promotion; conversation documents have an owning role rather than a developer invocation",
	"prepareDocument":            "materializes the already confirmed immutable document before the ordinary check, review, and integration sequence; no workflow or agent may rewrite it",
	"recoverDocumentIntegration": "observes and records an interrupted promotion under its lease and existing revision-bound evidence; never performs a new promotion",
	"settleRemoteTarget":         "the pre-promotion remote check inside candidate.integrate",
	"publishIntegration":         "the merge candidate.integrate asks the forge for once the promotion stands",
	"landsThroughPullRequest":    "asks the forge whether the target is protected, which picks how candidate.integrate promotes",
	"landedThroughPullRequest":   "reads whether a landing through the pull request reached the target, inside candidate.integrate",
	"blockOnUnlandedPullRequest": "hands a landing the forge did not merge to a person, inside candidate.integrate",
	"repair":                     "records one repair attempt and re-enters candidate.develop with the findings",
	"prepareIntegrationRetry":    "replays a change whose promotion lost its race, so candidate.integrate can be re-earned",
	"moveOntoTargetForRepair":    "puts a change whose replay conflicted onto the target for its developer to reconcile, which is the setup of the repair candidate.develop then performs",
	"reconcilingPublishedBranch": "reads whether candidate.develop's publication replaces the published branch rather than extending it",
	"republishRebase":            "replaces the published run branch with the one the local branch now carries, inside candidate.develop's publication",
	"verifyHandback":             "checks a resumed run still has the change it preserved",
	"closeCheckStage":            "records how the check stage ended, inside candidate.check",

	// The declarative path. These step the workflow instance a run is observed
	// through and are the one group here that is not part of the delivery at all:
	// they perform nothing, decide nothing, and a run that skipped every one of
	// them delivers identically. Registering one would be registering the
	// observation of a step as a step.
	"beginDeliveryTrial":  "records the workflow instance a new run is observed through",
	"resumeDeliveryTrial": "picks that instance back up on a run this process did not start",
	"observe":             "records that the run performed one state and produced one outcome",
	"observeDevelopEnded": "names which outcome the developer's state produced",
	"observeCheckEnded":   "names which outcome the gate produced",
	"observeReviewEnded":  "names which outcome the reviewer's state produced",

	// Recording and derivation. These write down what happened or read back what
	// the run already knows; none of them does anything to the work.
	"recordWorktree":               "records the worktree the run was given",
	"recordHarnessCommit":          "records the commit the harness made of a developer's change",
	"recordCheckFailure":           "records the failing check as the run's outstanding repair input",
	"recordPathRefusal":            "records the refused paths as the run's outstanding repair input",
	"recordDevelopment":            "records what a developer invocation produced and cost",
	"openWithDeveloperRefusals":    "reads back the developer's own refused proposals into the prompt it is about to be sent, inside candidate.develop",
	"carryReviewEvidence":          "puts the verdict the record holds onto the outcome a run resumed at its promotion reports",
	"deliveredInvariants":          "selects the invariants a developer is given",
	"repairBudget":                 "reads how many repair attempts this run may still make",
	"recordPrice":                  "prices the item against what this run spent, inside run.complete",
	"mergeQueued":                  "reads whether the forge only queued the merge, which is what run.complete waits for before closing the item",
	"publicationRecorded":          "reads the record back to confirm the pull request the run reports is on it, inside run.complete, and refuses the completion where it is not",
	"applyUndischargedDisposition": "records on the run where its item was actually settled, inside run.complete",
	"recordOutcomeOnce":            "records the run's outcome note on the item, read back before it is made again, inside run.complete",
	"reopenUndischargedOnce":       "puts an item the change did not discharge back in the backlog, read back before it is made again, inside run.complete",
}
