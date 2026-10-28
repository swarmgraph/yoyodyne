package orchestrator

// A round the environment refused, against a real repository.
//
// The field cases were items that had been granted a repair, dispatched into a
// clean worktree by a build that did not carry the gate the grant relied on, and
// charged for the round anyway. The grant was gone, the item was one step nearer
// its cap, and the escalation that followed reads afterwards as work nobody could
// finish.
//
// Each test asserts a different part of the class. That sequence with the
// accounting asserted: the run refuses, the round is classified environmental,
// and the item stands exactly where it stood before it. The same failure
// repeated past what the round budget has room for, which is the bound the item
// asks for rather than one case of it. The conjunction from the other side — an
// empty delivery with no environmental cause still spends. Each cause the
// harness recognizes from the failure alone rather than from a refusal site of
// its own. A granted round that never ran, whose worktree is full of what
// earlier rounds delivered and which is refused all the same. And the guarantee
// that a round turned away never lends its classification to the round after it.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// environmentalCaps leaves the round budget with room the grant is not truncated
// against, so what these tests measure is a refused round spending nothing rather
// than a grant the cap had already cut. The stoppage they start from has cost
// three rounds and the grant asks for two, which fits.
var environmentalCaps = runstate.TriageCaps{ReviewRounds: 8, RepairGrants: 1, Reruns: 1, MergeRearms: 2}

// environmentalRefusals is how many times the sequence test lets the environment
// refuse the same item. It is more than the round budget has room for after the
// grant, which is the point: one round leaked per refusal would have the item at
// its cap well before the loop ends.
const environmentalRefusals = 5

// The founding case of this item. A granted repair is carried out into a
// worktree holding none of the change, the run refuses, and what the round would
// have cost the item is given back rather than spent on the harness's own
// failure.
func TestAnEmptyDiffRoundTheEnvironmentRefusedSpendsNothing(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	stopped := stopWithPreservedChange(t, repository, worktreeRoot, store, tracker, &memoryDocket{})

	// The development manager's decision, which spends the item's repair-grant
	// budget as it is recorded.
	if _, err := store.Triage().GrantRepair(context.Background(), tracker.Item.ID, triageDecided(runstate.TriageDecisionRepair, stopped.RunID), 2, docketedNow, environmentalCaps); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	before, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}

	// What the stale-binary dispatch handed the granted round: the run re-entered
	// under its grant, in a worktree seeded from the target branch rather than
	// from the preserved one.
	emptyPreservedWorktree(t, stopped.WorktreePath)
	continueOnGrant(t, store, tracker, stopped.RunID)

	docket := &memoryDocket{}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		t.Errorf("a developer was invoked in %s, which holds none of the change the grant was to repair", request.WorkingDirectory)
		return nil
	}, approveVerdict)
	continuing := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	continuing.Docket = docketerOverStore(docket, store, continuing.Config)

	if _, err := continuing.Run(context.Background(), tracker.Item.ID); !errors.Is(err, ErrPreservedChangeMissing) {
		t.Fatalf("Run() error = %v, want the round refused for holding none of its change", err)
	}
	if invocations := len(provider.Requests); invocations != 0 {
		t.Fatalf("provider invocations = %d, want the refusal to have spent nothing on a provider", invocations)
	}

	refused, err := store.Load(stopped.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	assertSavedStopClass(t, store, stopped.RunID, runstate.CauseHandbackMissingChange.StopClass())
	// The class is on the run, named, and settled.
	environmental := refused.Environmental
	if environmental == nil {
		t.Fatal("the run records no environmental cause, so nothing downstream can tell this round from a delivery of nothing")
	}
	if environmental.Cause != runstate.CauseHandbackMissingChange || !environmental.Refused {
		t.Fatalf("environmental = %#v, want the handback refusal classified", environmental)
	}
	if environmental.Problem != "" {
		t.Fatalf("the settle could not pay the refusal back: %s", environmental.Problem)
	}
	// And the refusal is handed to a person, which is what the run's durable
	// blocker says. Every surface reads the outcome off that field rather than off
	// the failure beside it, so a refusal that recorded no blocker would be
	// reported as a run the harness merely failed to carry — the same word this
	// vocabulary exists to stop, and one severity quieter in the channel.
	if strings.TrimSpace(refused.Blocker) == "" {
		t.Fatal("the refused handback records no durable blocker, so nothing reading the run can tell a stoppage somebody has to decide from a run that broke")
	}
	if outcome := refused.Outcome(); outcome != runstate.OutcomeStopped {
		t.Fatalf("outcome = %q, want %q: the refusal is a stoppage somebody has to decide about", outcome, runstate.OutcomeStopped)
	}
	// The grant is back: the run's record says the continuation bought nothing,
	// while the run's own budget still counts the attempt slot it spent.
	if !environmental.GrantReturned {
		t.Fatal("the granted repair round was not returned, so the grant was spent on a worktree that held nothing")
	}
	if carried := refused.CarriedOutRepairAttempts(); carried != 0 {
		t.Fatalf("carried-out repair attempts = %d, want the grant untouched", carried)
	}
	if granted := refused.GrantedRepairAttempts(); granted != 1 {
		t.Fatalf("granted repair attempts = %d, want the run's own budget to still count the attempt it spent", granted)
	}
	// And the cap is untouched: every figure the guards refuse against is exactly
	// what it was before the round.
	after, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if after.ReviewRounds != before.ReviewRounds || after.CommittedRounds != before.CommittedRounds {
		t.Fatalf("counters after the refusal = %d round(s), %d committed; want %d and %d",
			after.ReviewRounds, after.CommittedRounds, before.ReviewRounds, before.CommittedRounds)
	}
	// The development manager reads it on the docket rather than deriving it from
	// a blocker: the counters on the entry mean something different because of it.
	if len(docket.entries) != 1 {
		t.Fatalf("docket entries = %d, want the refused round docketed once", len(docket.entries))
	}
	entry := docket.entries[0]
	if entry.Environmental == nil || !entry.Environmental.Refused {
		t.Fatalf("docket entry environmental = %#v, want the refusal carried onto it", entry.Environmental)
	}
	rendered := entry.Render()
	for _, want := range []string{"ended by something outside the work", string(runstate.CauseHandbackMissingChange), "stands where it did before the round"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the docket entry does not say %q:\n%s", want, rendered)
		}
	}
}

// The guarantee stated as a bound rather than as one case: however many times
// the environment refuses an item, it is no nearer its cap at the end than at
// the start. A class that leaked one round per refusal would be a slower walk to
// the same escalation.
func TestNoSequenceOfEnvironmentalRefusalsWalksAnItemToItsCap(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	stopped := stopWithPreservedChange(t, repository, worktreeRoot, store, tracker, &memoryDocket{})
	if _, err := store.Triage().GrantRepair(context.Background(), tracker.Item.ID, triageDecided(runstate.TriageDecisionRepair, stopped.RunID), 2, docketedNow, environmentalCaps); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	before, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if before.RoundsUncommitted(environmentalCaps.ReviewRounds) < 1 {
		t.Fatalf("the fixture starts with no round left to lose, so this proves nothing: %#v", before)
	}
	emptyPreservedWorktree(t, stopped.WorktreePath)

	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		t.Errorf("a developer was invoked in %s across a run of environmental refusals", request.WorkingDirectory)
		return nil
	}, approveVerdict)
	continuing := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)

	// More refusals than the round budget has room for. If any one of them leaked
	// a round, the item would be at its cap by the end of this loop.
	for refusal := 0; refusal < environmentalRefusals; refusal++ {
		continueOnGrant(t, store, tracker, stopped.RunID)
		if _, err := continuing.Run(context.Background(), tracker.Item.ID); !errors.Is(err, ErrPreservedChangeMissing) {
			t.Fatalf("refusal %d: Run() error = %v, want the round refused", refusal, err)
		}
		after, err := store.Triage().Counters(tracker.Item.ID)
		if err != nil {
			t.Fatalf("Counters() error = %v", err)
		}
		if after.ReviewRounds != before.ReviewRounds {
			t.Fatalf("refusal %d left the item at %d round(s), want %d", refusal, after.ReviewRounds, before.ReviewRounds)
		}
		if after.RoundsUncommitted(environmentalCaps.ReviewRounds) != before.RoundsUncommitted(environmentalCaps.ReviewRounds) {
			t.Fatalf("refusal %d moved the item toward its cap: %d round(s) uncommitted, want %d",
				refusal, after.RoundsUncommitted(environmentalCaps.ReviewRounds), before.RoundsUncommitted(environmentalCaps.ReviewRounds))
		}
	}
	refused, err := store.Load(stopped.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if carried := refused.CarriedOutRepairAttempts(); carried != 0 {
		t.Fatalf("carried-out repair attempts = %d after a run of refusals, want the grant untouched", carried)
	}
}

// The other half of the definition, which is what keeps the class honest. A run
// that delivers nothing and records no environmental cause is in no class at
// all: nothing is given back, because nothing was refused, and the run stops on
// its own repair budget exactly as any run that could not satisfy its reviewer.
//
// What it is not charged is a review round, and that is yoyodyne-ifd.391's rule
// rather than this class's: the cap counts only rounds that ended in a verdict
// requiring repair against a change that was present, and a reviewer shown an
// empty diff was not arguing with a change. Until that item the empty delivery
// spent as any round did, so that laziness could not hide in the class — but a
// mis-selected run and a stale worktree deliver the same empty diff, and the
// development manager reported both counting identically to real repair rounds.
// What bounds a developer that delivers nothing is still the run's own repair
// budget, spent here, and the once-per-item grant and re-run any further run of
// the item needs.
func TestAnEmptyDeliveryWithNoEnvironmentalCauseIsInNoClassAndSpendsNoRound(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// A developer that writes nothing at all, judged until the run's own repair
	// budget is spent. Nothing about the environment refused it.
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, repairVerdict)
	running := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)

	outcome, err := running.Run(context.Background(), tracker.Item.ID)
	if err == nil {
		t.Fatal("Run() ended without stopping, so nothing here spent a budget")
	}
	if !outcome.Blocked {
		t.Fatalf("outcome = %#v, want the run blocked on its unresolved findings", outcome)
	}
	if outcome.Environmental != nil {
		t.Fatalf("outcome environmental = %#v, want an empty delivery nobody can account for left in no class at all", outcome.Environmental)
	}
	spent, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if spent.Environmental != nil {
		t.Fatalf("the run records an environmental cause it never had: %#v", spent.Environmental)
	}
	// The run spent its own budget: every verdict was reached and every attempt
	// the reviewer asked for was made.
	if spent.ReviewRounds < 2 || spent.RepairAttempts < 1 {
		t.Fatalf("run review rounds = %d, repair attempts = %d; want the run to have spent its own repair budget on the empty delivery", spent.ReviewRounds, spent.RepairAttempts)
	}
	counters, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.ReviewRounds != 0 {
		t.Fatalf("review rounds = %d, want none: no verdict here was against a change that was present", counters.ReviewRounds)
	}
	// And the verdicts were recorded rather than passed over, so a re-review of
	// the last attempt would still be free.
	if want := runstate.RoundKey(outcome.RunID, spent.RepairAttempts); counters.LastJudged != want {
		t.Fatalf("last judged attempt = %q, want %q: an uncharged verdict is still recorded against the attempt it judged", counters.LastJudged, want)
	}
	if invocations := len(provider.RequestsForRole(domain.RoleDeveloper)); invocations == 0 {
		t.Fatal("no developer was invoked, so this is not the empty delivery the test is about")
	}
}

// A cause the harness recognizes from the failure that ended the run rather than
// from a refusal site of its own. A worktree that could not be cut from the
// primary checkout is the environment refusing the round as squarely as an empty
// handback is, and it reaches the class through the sentinel the refusing package
// declares rather than through a message somebody could reword.
func TestARoundTurnedAwayByThePrimaryCheckoutIsRefusedEnvironmentally(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		t.Errorf("a developer was invoked in %s, and no worktree was ever cut for this run", request.WorkingDirectory)
		return nil
	}, approveVerdict)
	starting := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	starting.NewRunID = runstate.NewRunID
	starting.Worktrees = dirtyPrimaryWorktrees{starting.Worktrees}

	outcome, err := starting.Run(context.Background(), tracker.Item.ID)
	assertSavedStopClass(t, store, outcome.RunID, runstate.CauseDirtyPrimary.StopClass())
	if err == nil {
		t.Fatal("Run() started work in a checkout no worktree could be cut from")
	}
	if outcome.Environmental == nil {
		t.Fatalf("outcome = %#v, want the round classified by the cause its failure names", outcome)
	}
	if outcome.Environmental.Cause != runstate.CauseDirtyPrimary {
		t.Fatalf("environmental cause = %q, want %q", outcome.Environmental.Cause, runstate.CauseDirtyPrimary)
	}
	if !outcome.Environmental.Settled || !outcome.Environmental.Refused {
		t.Fatalf("environmental = %#v, want the round settled and refused", outcome.Environmental)
	}
	// It reached nothing that spends — no worktree, no reviewer, no grant — so
	// there was nothing to give back, and the record says that rather than
	// claiming a return.
	if outcome.Environmental.RoundReturned || outcome.Environmental.GrantReturned {
		t.Fatalf("environmental = %#v, want a round that reached nothing that spends to have returned nothing", outcome.Environmental)
	}
	if outcome.Environmental.Problem != "" {
		t.Fatalf("the settle reported a problem it did not have: %s", outcome.Environmental.Problem)
	}
	counters, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.ReviewRounds != 0 || counters.RepairGrants != 0 {
		t.Fatalf("counters = %#v, want an item charged nothing for a round the environment turned away", counters)
	}
}

// The second cause the harness recognizes from the failure alone, and the one
// whose evidence is the wrapping chain rather than a single call: a provider
// invocation the machine never started travels from the process runner, through
// the metered provider, through the record of the dead attempt, and out as the
// error that ends the run. Every layer of that has to preserve the sentinel, and
// this is what says it does.
func TestAProviderInvocationTheMachineNeverStartedIsRefusedEnvironmentally(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// What the sandbox refusing to spawn an agent looks like from here: the
	// invocation returns the runner's sentinel and nothing was ever asked.
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error {
		return fmt.Errorf("%w: start %q: operation not permitted", execution.ErrProcessNotStarted, "claude")
	}, approveVerdict)
	starting := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	starting.NewRunID = runstate.NewRunID

	outcome, err := starting.Run(context.Background(), tracker.Item.ID)
	assertSavedStopClass(t, store, outcome.RunID, runstate.CauseSandboxSpawnFailure.StopClass())
	if err == nil {
		t.Fatal("Run() finished on an invocation the machine never started")
	}
	if outcome.Environmental == nil {
		t.Fatalf("outcome = %#v, want the round classified: the sentinel did not survive the layers between the runner and the settle", outcome)
	}
	if outcome.Environmental.Cause != runstate.CauseSandboxSpawnFailure {
		t.Fatalf("environmental cause = %q, want %q", outcome.Environmental.Cause, runstate.CauseSandboxSpawnFailure)
	}
	if !outcome.Environmental.Settled || !outcome.Environmental.Refused {
		t.Fatalf("environmental = %#v, want the round settled and refused", outcome.Environmental)
	}
	if outcome.Environmental.Problem != "" {
		t.Fatalf("the settle reported a problem it did not have: %s", outcome.Environmental.Problem)
	}
	// A worktree was cut and nothing was written into it, which is the empty
	// delivery half of the definition met rather than assumed.
	if outcome.WorktreePath == "" {
		t.Fatal("no worktree was recorded, so the emptiness this classified on was not read from one")
	}
	counters, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.ReviewRounds != 0 {
		t.Fatalf("review rounds = %d, want an item charged nothing for an invocation that never ran", counters.ReviewRounds)
	}
}

// The case the worktree cannot answer, and the one a granted repair is most
// exposed to. A continued round runs in the worktree earlier rounds already
// filled, so "is the diff empty" asks whether the run ever delivered anything
// rather than whether this round did — and a round whose developer the machine
// never started would read as a delivery, spend the grant on work no agent did,
// and be described everywhere as a round that delivered a change.
func TestAGrantedRoundThatNeverRanIsRefusedThoughItsWorktreeHoldsTheChange(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	stopped := stopWithPreservedChange(t, repository, worktreeRoot, store, tracker, &memoryDocket{})
	if _, err := store.Triage().GrantRepair(context.Background(), tracker.Item.ID, triageDecided(runstate.TriageDecisionRepair, stopped.RunID), 2, docketedNow, environmentalCaps); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	// The grant carried out onto the change it was granted to repair. Nothing is
	// emptied here: the worktree holds all ten files, which is the whole point.
	continueOnGrant(t, store, tracker, stopped.RunID)
	if present := presentHandbackFiles(stopped.WorktreePath); len(present) != len(handbackFiles) {
		t.Fatalf("the continued worktree holds %d of the change's %d file(s), so this is not the case the test is about", len(present), len(handbackFiles))
	}

	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error {
		return fmt.Errorf("%w: start %q: operation not permitted", execution.ErrProcessNotStarted, "claude")
	}, approveVerdict)
	continuing := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)

	outcome, err := continuing.Run(context.Background(), tracker.Item.ID)
	if err == nil {
		t.Fatal("Run() finished a granted round whose developer the machine never started")
	}
	if outcome.Environmental == nil || outcome.Environmental.Cause != runstate.CauseSandboxSpawnFailure {
		t.Fatalf("environmental = %#v, want the granted round classified by the invocation that never started", outcome.Environmental)
	}
	if !outcome.Environmental.Settled || !outcome.Environmental.Refused {
		t.Fatalf("environmental = %#v, want a round that added nothing refused though its worktree is full", outcome.Environmental)
	}
	// The grant is what this round would otherwise have burned, and it is back.
	if !outcome.Environmental.GrantReturned {
		t.Fatal("the granted repair round was not returned, so the grant was spent on a round no agent ever ran")
	}
	refused, err := store.Load(stopped.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if carried := refused.CarriedOutRepairAttempts(); carried != 0 {
		t.Fatalf("carried-out repair attempts = %d, want the grant untouched", carried)
	}
	// And the change is still there: nothing about refusing the round touched what
	// the earlier rounds delivered.
	if present := presentHandbackFiles(stopped.WorktreePath); len(present) != len(handbackFiles) {
		t.Fatalf("the refusal left %d of the change's %d file(s)", len(present), len(handbackFiles))
	}
}

// The other half of the dispatch refusal: a run turned away by the environment
// stays live and resumable, so the round it is eventually judged on is a
// different one and must not inherit the refusal. A record left standing would
// tell an operator the item stands where it did on a round that spent a review
// round — the misreading this class exists to prevent, inverted.
func TestAResumedRunDoesNotInheritADispatchTheEnvironmentTurnedAway(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	stopped := stopWithPreservedChange(t, repository, worktreeRoot, store, tracker, &memoryDocket{})
	// Live again at the review, with its change preserved. This item has already
	// spent rounds, which is what a stale refusal would contradict.
	reEnterAt(t, store, tracker, stopped.RunID, runstate.PhaseReviewing)

	turnedAway := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		t.Errorf("a developer was invoked in %s by a dispatch the environment turned away", request.WorkingDirectory)
		return nil
	}, repairVerdict)
	refusing := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, turnedAway, []string{"exit 0"}), turnedAway)
	refusing.Worktrees = unreadyPrimaryWorktrees{refusing.Worktrees}
	if _, err := refusing.Run(context.Background(), tracker.Item.ID); err == nil {
		t.Fatal("Run() resumed against a checkout nothing may be resumed against")
	}
	turned, err := store.Load(stopped.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if turned.Environmental == nil || turned.Environmental.Cause != runstate.CauseDirtyPrimary || !turned.Environmental.Refused {
		t.Fatalf("environmental = %#v, want the turned-away dispatch recorded on the run", turned.Environmental)
	}

	spentBefore, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}

	// The same run resumed once the checkout is the harness's again, and stopped
	// for a reason of its own: the reviewer asks for repairs it has no budget left
	// for.
	judging := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, repairVerdict)
	resumed := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, judging, []string{"exit 0"}), judging)
	if _, err := resumed.Run(context.Background(), tracker.Item.ID); err == nil {
		t.Fatal("Run() finished a run whose reviewer kept asking for repairs")
	}
	ordinary, err := store.Load(stopped.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if ordinary.Environmental != nil {
		t.Fatalf("the resumed round inherited a refusal from the dispatch before it: %#v", ordinary.Environmental)
	}
	if !ordinary.Status.Terminal() || ordinary.Blocker == "" {
		t.Fatalf("resumed run = %#v, want it ended on a durable blocker of its own", ordinary)
	}
	// And the rounds this item has already spent are still spent. That is what the
	// stale record would have contradicted: an ordinary stop announcing an
	// environmental refusal tells a reader an item at three rounds stands where it
	// did before them.
	spentAfter, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if spentAfter.ReviewRounds < 1 || spentAfter.ReviewRounds != spentBefore.ReviewRounds {
		t.Fatalf("review rounds = %d, want the %d this item had already spent left exactly as they were",
			spentAfter.ReviewRounds, spentBefore.ReviewRounds)
	}
}

// The other direction the accounting can fail in, and the one the attempt key
// alone cannot catch: a round given back that was genuinely spent. A run
// re-entered at the review is the same run identifier at the same attempt
// number as the process that already charged the item for judging that attempt,
// so a refusal in the new process finds its own key at the head of the record —
// and giving it back would credit the item for a verdict it really got.
func TestAReEnteredRunCannotReturnTheRoundItsPredecessorCharged(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	stopped := stopWithPreservedChange(t, repository, worktreeRoot, store, tracker, &memoryDocket{})
	spent, err := store.Load(stopped.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	before, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	// The shape this is about, proved rather than assumed: the head of the item's
	// record is the attempt a re-entry at the review settles under, charged by the
	// process that has just ended.
	head := runstate.RoundKey(stopped.RunID, spent.RepairAttempts)
	if before.ReviewRounds < 1 || before.LastRound != head {
		t.Fatalf("counters = %d round(s) at head %q, want the round the re-entry will settle under charged and at the head %q",
			before.ReviewRounds, before.LastRound, head)
	}
	if before.LastRoundCharger == "" {
		t.Fatalf("the head round names no charging process, so this proves nothing: %#v", before)
	}

	// The same run picked up by a second process, in a worktree that lost the
	// change — which is the environment refusing the round it is picked up for.
	emptyPreservedWorktree(t, stopped.WorktreePath)
	reEnterAt(t, store, tracker, stopped.RunID, runstate.PhaseReviewing)

	second := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		t.Errorf("a provider was invoked from %s by a re-entry the environment refused", request.WorkingDirectory)
		return nil
	}, approveVerdict)
	continuing := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second)
	if _, err := continuing.Run(context.Background(), tracker.Item.ID); !errors.Is(err, ErrPreservedChangeMissing) {
		t.Fatalf("Run() error = %v, want the re-entry refused for holding none of its change", err)
	}

	refused, err := store.Load(stopped.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	environmental := refused.Environmental
	if environmental == nil || !environmental.Refused {
		t.Fatalf("environmental = %#v, want the re-entered round classified and refused", environmental)
	}
	if environmental.RoundReturned {
		t.Fatal("the re-entered process gave back the review round the process before it charged")
	}
	// And it says so, because a round left spent and a refusal that reached
	// nothing to give back leave the item in different places.
	if !environmental.RoundLeftSpent || environmental.RoundChargedBy != before.LastRoundCharger {
		t.Fatalf("environmental = %#v, want the round recorded as left spent under %q", environmental, before.LastRoundCharger)
	}
	described := environmental.Describe()
	if !strings.Contains(described, "left spent") || strings.Contains(described, "stands where it did") {
		t.Fatalf("Describe() = %q, want a round left spent said as one rather than as an item standing where it did", described)
	}
	// The counters are exactly as the process before left them: the round it spent
	// is still spent, and the head still names it.
	after, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if after.ReviewRounds != before.ReviewRounds {
		t.Fatalf("review rounds = %d after the re-entry, want the %d the process before it spent", after.ReviewRounds, before.ReviewRounds)
	}
	if after.LastRound != head || after.LastRoundCharger != before.LastRoundCharger {
		t.Fatalf("head = %q charged by %q, want the refused return to have left the record alone", after.LastRound, after.LastRoundCharger)
	}
}

// dirtyPrimaryWorktrees passes the readiness gate and then refuses to cut a
// worktree, which is the window that gate cannot close: the checkout is the
// harness's to read at the moment it is asked and somebody's to write into a
// moment later. It is also the only route to the refusal that ends a run — the
// gate before the claim turns a run away before there is any run to record
// anything on.
type dirtyPrimaryWorktrees struct {
	WorktreeManager
}

func (dirtyPrimaryWorktrees) Create(context.Context, gitworktree.CreateRequest) (gitworktree.Worktree, error) {
	return gitworktree.Worktree{}, fmt.Errorf("%w: primary repository has uncommitted changes: notes.txt", gitworktree.ErrPrimaryNotReady)
}

// killedCheckoutWorktrees is a `git worktree add` the harness's own budget ended
// while it was still writing the tree out, which is what the gitworktree manager
// returns for one — see TestAWorktreeCheckoutKilledByItsBudgetIsSaidInOneSentence
// for the real deadline killing a real add. Nothing was made, so nothing is
// recorded and the run has nowhere it could have delivered to.
type killedCheckoutWorktrees struct {
	WorktreeManager
}

func (killedCheckoutWorktrees) Create(context.Context, gitworktree.CreateRequest) (gitworktree.Worktree, error) {
	return gitworktree.Worktree{}, fmt.Errorf("%w: the checkout of 1099 file(s) was still running after 5m0s, so no worktree was made and no agent of this run was invoked", gitworktree.ErrCheckoutKilled)
}

// unreadyPrimaryWorktrees refuses the readiness gate itself, which is where a
// resumed run is turned back: the dispatch never reaches the run, and the run
// stays exactly as the process that stopped it left it.
type unreadyPrimaryWorktrees struct {
	WorktreeManager
}

func (unreadyPrimaryWorktrees) ValidateReady(context.Context) error {
	return fmt.Errorf("%w: primary repository has uncommitted changes: notes.txt", gitworktree.ErrPrimaryNotReady)
}

// continueOnGrant records what carrying out a repair grant records on the run
// before the pipeline is asked to continue it: the continuation, the attempt it
// buys, and the stoppage superseded. It is the harness's own re-entry written by
// hand, so what these tests drive is the state a granted round actually starts
// from rather than an approximation of it.
func continueOnGrant(t *testing.T, store *runstate.Store, tracker *orchestratortest.Tracker, runID string) {
	t.Helper()
	state, err := store.Load(runID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	state.RepairContinuations = append(state.RepairContinuations, runstate.RepairContinuation{
		GrantedAttempts:   1,
		Reason:            "Triaged: the repair loop was re-entered on the change it already has, under a grant recorded against the item's durable triage budget.",
		ContinuedAt:       docketedNow,
		SupersededBlocker: state.Blocker,
	})
	state.RepairAttempts++
	state.Blocker = ""
	state.Failure = ""
	// The refusal on the record describes the round this continuation supersedes,
	// and that round has settled. A fresh round is classified on its own evidence.
	state.Environmental = nil
	state.Status = runstate.StatusRunning
	state.Phase = runstate.PhaseDeveloping
	state.CompletedAt = nil
	saveRecord(t, store, state)
	if _, _, err := tracker.Claim(context.Background(), tracker.Item.ID); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
}

// A creation the harness's own deadline killed is the environment refusing the
// round before the round could begin: no worktree exists, no agent was invoked,
// and what stopped it is the machine having been too busy for a local Git
// command. It spent three runs of yoyodyne-ifd.441 in three hours, one of them a
// recorded re-run, so what this asserts is the accounting rather than only the
// classification — the item stands exactly where it did.
func TestARoundTurnedAwayByAKilledWorktreeCheckoutIsRefusedEnvironmentally(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		t.Errorf("a developer was invoked in %s, and no worktree was ever cut for this run", request.WorkingDirectory)
		return nil
	}, approveVerdict)
	starting := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	starting.NewRunID = runstate.NewRunID
	starting.Worktrees = killedCheckoutWorktrees{starting.Worktrees}

	outcome, err := starting.Run(context.Background(), tracker.Item.ID)
	assertSavedStopClass(t, store, outcome.RunID, runstate.CauseWorktreeCheckoutKilled.StopClass())
	if err == nil {
		t.Fatal("Run() started work with no worktree to do it in")
	}
	if outcome.Environmental == nil {
		t.Fatalf("outcome = %#v, want the round classified by the cause its creation names", outcome)
	}
	if outcome.Environmental.Cause != runstate.CauseWorktreeCheckoutKilled {
		t.Fatalf("environmental cause = %q, want %q", outcome.Environmental.Cause, runstate.CauseWorktreeCheckoutKilled)
	}
	// The refusing site says nothing of this round ran, because it is the only
	// place that knows: the developer is invoked past the creation, so a creation
	// that did not return has invoked nobody.
	if !outcome.Environmental.NothingRan {
		t.Fatalf("environmental = %#v, want the refusal recorded as one nothing of the round ran under", outcome.Environmental)
	}
	if !outcome.Environmental.Settled || !outcome.Environmental.Refused {
		t.Fatalf("environmental = %#v, want the round settled and refused", outcome.Environmental)
	}
	if outcome.Environmental.Problem != "" {
		t.Fatalf("the settle reported a problem it did not have: %s", outcome.Environmental.Problem)
	}
	// What the record says about it is the one sentence the creation gave it,
	// rather than the progress meter Git leaves on a killed checkout.
	if !strings.Contains(outcome.Environmental.Detail, "1099 file(s)") {
		t.Fatalf("detail = %q, want the cause named in the words the creation refused in", outcome.Environmental.Detail)
	}
	recorded, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !strings.Contains(recorded.Failure, "1099 file(s)") || strings.Contains(recorded.Failure, "Updating files") {
		t.Fatalf("failure = %q, want the cause rather than the checkout's progress", recorded.Failure)
	}
	counters, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.ReviewRounds != 0 || counters.RepairGrants != 0 || counters.Reruns != 0 {
		t.Fatalf("counters = %#v, want an item charged nothing for a round the machine turned away", counters)
	}
}
