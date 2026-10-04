package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The decision a development manager records when the change is nearly right and
// the run ran out of attempts: the findings are the ones worth acting on, and
// the developer that wrote the change is the one to act on them.

// continueCaps are the harness defaults the development manager's decision is
// recorded against, with room in the round budget so a test measuring the grant
// is measuring the grant.
var continueCaps = runstate.TriageCaps{ReviewRounds: 6, RepairGrants: 1, Reruns: 1, MergeRearms: 2}

// continueGrantRounds is what triage.repair_grant_attempts says a grant is worth
// in these tests, which is what the development manager's decision spends.
const continueGrantRounds = 2

// continueHarness is the durable state a repair-continue acts on, held together
// so a test can drive one decision without rebuilding four stores.
type continueHarness struct {
	docket    *memoryDocket
	runs      *runstate.Store
	intake    *runstate.IntakeHoldStore
	tracker   *orchestratortest.Tracker
	ownership *orchestratortest.Ownership
	// started is the continuations the carry-out dispatched.
	started []continuedRun
	outcome Outcome
	failure error
	// capacity is execution.max_concurrent_developers as the action reads it.
	capacity int
}

func (h *continueHarness) continuer() RepairContinuer {
	return RepairContinuer{
		Docket:             h.docket,
		Runs:               h.runs,
		Intake:             h.intake,
		Decisions:          h.runs.Triage(),
		Items:              h.tracker,
		Worktrees:          h.ownership,
		ConfiguredAttempts: 2,
		Capacity:           h.capacity,
		Clock:              docketClock{},
		Start: func(_ context.Context, workItemID, runID string) (Outcome, error) {
			h.started = append(h.started, continuedRun{workItemID: workItemID, runID: runID})
			return h.outcome, h.failure
		},
	}
}

// continuedRun is one continuation the carry-out dispatched: the item, and the
// run it named to be re-entered. The run is recorded because naming it is what
// keeps a repair from being carried out as a fresh run of the same item.
type continuedRun struct {
	workItemID string
	runID      string
}

// continuableState is the stopped run this action is about: one whose repair
// budget was spent on findings its developer never resolved, with the branch,
// the worktree, and the session it stopped in all preserved.
func continuableState() runstate.State {
	state := stoppedState()
	state.ProviderSessionID = "developer-session"
	return state
}

// newContinueHarness is an undecided product with the development manager's
// repair decision recorded on it, which is the ordinary case: the decision is
// made in the conversation and this action carries it out.
func newContinueHarness(t *testing.T, state runstate.State) *continueHarness {
	t.Helper()
	harness := newUndecidedHarness(t, state)
	// The decision itself: the development manager recorded a repair of this
	// item, which spent the item's repair-grant budget and sized the grant from
	// the configuration. That footprint is what the action reads to know somebody
	// decided this and how much it is worth.
	recordRepairDecision(t, harness.runs, state.WorkItemID)
	return harness
}

// newUndecidedHarness records one stopped run, dockets it, and leaves everything
// else as a fresh product: no hold, nothing decided, nothing in flight, and the
// item blocked exactly as the run stopping left it.
func newUndecidedHarness(t *testing.T, state runstate.State) *continueHarness {
	t.Helper()
	root := t.TempDir()
	runs, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	if err := runs.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewIntakeHoldStore() error = %v", err)
	}
	docket := &memoryDocket{}
	if _, err := docketerOver(nil, docket).RecordStoppedRun(state); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	return &continueHarness{
		docket:    docket,
		runs:      runs,
		intake:    intake,
		tracker:   &orchestratortest.Tracker{Item: beads.WorkItem{ID: state.WorkItemID, Title: state.WorkItemTitle, Status: "blocked"}},
		ownership: &orchestratortest.Ownership{},
		capacity:  2,
		outcome:   Outcome{RunID: state.RunID, WorkItemID: state.WorkItemID, Status: runstate.StatusSucceeded},
	}
}

// recordRepairDecision is what the development manager's triage does to the
// item's durable record when it decides a repair: it spends the item's one
// grant, truncated to the rounds the cap still has room for, before anything
// acts on the decision.
func recordRepairDecision(t *testing.T, runs *runstate.Store, workItemID string) runstate.RepairGrant {
	t.Helper()
	granted, err := runs.Triage().GrantRepair(context.Background(), workItemID, triageDecided(runstate.TriageDecisionRepair, docketedRunID), continueGrantRounds, docketedNow, continueCaps)
	if err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	return granted
}

func continueRequest() RepairContinueRequest {
	return RepairContinueRequest{Run: docketedRunID}
}

// carried reports how much of the item's grant the harness has handed to the
// stopped run, which is what a refused carry-out must leave at zero.
func (h *continueHarness) carried(t *testing.T) int {
	t.Helper()
	return h.reload(t).GrantedRepairAttempts()
}

// spent reports what the item's durable triage record now says, which is what
// every other reader of the same budget reads.
func (h *continueHarness) spent(t *testing.T) runstate.TriageCounters {
	t.Helper()
	counters, err := h.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	return counters
}

func (h *continueHarness) reload(t *testing.T) runstate.State {
	t.Helper()
	state, err := h.runs.Load(docketedRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return state
}

func (h *continueHarness) save(t *testing.T, state runstate.State) {
	t.Helper()
	if err := h.runs.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
}

// What the action is for: the same run goes on, on the change it already has,
// with the grant recorded where the loop that spends it will read it.
func TestARepairContinuesTheSameRunUnderTheConfiguredGrant(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	result, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if !result.Continued || len(harness.started) != 1 || harness.started[0].workItemID != docketedItem {
		t.Fatalf("started = %#v, continued = %t, want the docketed item continued once", harness.started, result.Continued)
	}
	// And the dispatch named the run it verified rather than the item alone. A
	// dispatch that named only the item is one a fresh run satisfies, which is
	// what every recorded loss of a repair round actually was.
	if harness.started[0].runID != docketedRunID {
		t.Fatalf("continued run = %q, want the docketed run %q named by the dispatch", harness.started[0].runID, docketedRunID)
	}
	// Nothing started over: the run the docket entry names is the run that goes
	// on, in the worktree, branch, and session it stopped in.
	state := harness.reload(t)
	if state.RunID != docketedRunID || state.Status != runstate.StatusRunning || state.Phase != runstate.PhaseDeveloping {
		t.Fatalf("continued run = %s %s/%s, want the docketed run running and developing again", state.RunID, state.Status, state.Phase)
	}
	if state.CompletedAt != nil {
		t.Fatalf("completed at = %v, want a run that is going again to be recorded as unfinished", state.CompletedAt)
	}
	if state.WorktreePath != continuableState().WorktreePath || state.Branch != continuableState().Branch || state.ProviderSessionID != "developer-session" {
		t.Fatalf("continued run lost what it stopped with: %#v", state)
	}
	// The reviewer's findings are what the continued attempt is handed back, in
	// the words the reviewer wrote them.
	if len(state.ReviewFindingDetails) != 1 || state.ReviewFindingDetails[0].Message != "add the missing file" {
		t.Fatalf("findings = %#v, want the reviewer's own findings intact", state.ReviewFindingDetails)
	}
	// The grant is on the run, where the repair loop reads its budget from.
	if len(state.RepairContinuations) != 1 {
		t.Fatalf("continuations = %#v, want the one grant this carry-out made", state.RepairContinuations)
	}
	granted := state.RepairContinuations[0]
	if granted.GrantedAttempts != continueGrantRounds {
		t.Fatalf("granted = %#v, want the configured grant of two attempts in full", granted)
	}
	// The attempt this re-entry is about is counted as it is granted, exactly as
	// the repair loop counts its own, so the grant is worth what it says.
	if state.RepairAttempts != continuableState().RepairAttempts+1 {
		t.Fatalf("repair attempts = %d, want the continued attempt counted", state.RepairAttempts)
	}
	if budget := state.RepairBudget(2); budget != 4 || result.RepairBudget != budget {
		t.Fatalf("repair budget = %d (result %d), want the configured two plus the granted two", budget, result.RepairBudget)
	}
	// The item's durable budget is where the grant came from, and this carried
	// the whole of it out rather than spending a second one.
	if spent := harness.spent(t); spent.RepairGrants != 1 || spent.GrantedRounds != continueGrantRounds {
		t.Fatalf("counters = %#v, want the development manager's one grant and no second", spent)
	}
}

// stalledState is the other stoppage this action answers: a run whose provider
// the harness stopped in its first attempt, settled by the reconciling sweep
// half an hour later, with the developer session it stalled in preserved and no
// failure ever returned to that developer.
func stalledState() runstate.State {
	state := continuableState()
	state.Phase = runstate.PhaseDeveloping
	state.RepairAttempts = 0
	state.ReviewRounds = 0
	state.ReviewSummary = ""
	state.ReviewFindings = 0
	state.ReviewFindingDetails = nil
	state.CheckFailure = nil
	state.Blocker = "Yoyodyne stopped this item: the harness stopped its provider because it produced no output for longer than the harness allows, and nothing continued the run within 30m0s of that."
	state.Environmental = &runstate.EnvironmentalRefusal{
		Cause:      runstate.CauseProcessVanished,
		Detail:     "no live process held the run, no ending was recorded on it, and the harness stopped its provider because it produced no output for longer than the harness allows",
		RecordedAt: docketedNow.Add(-time.Hour),
		Settled:    true,
	}
	return state
}

// A stall judges nothing, so what the run is owed is the attempt the harness
// stopped it in. Before this it was refused here for want of a repair input,
// which left a re-run as the only decision anything could carry out — and a
// re-run starts over from the target branch with the session and the
// uncommitted work in the preserved worktree both discarded.
func TestARepairCarriesOnAStalledAttemptAndChargesItNothing(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, stalledState())
	result, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if !result.Continued || !result.Stall || len(harness.started) != 1 || harness.started[0].runID != docketedRunID {
		t.Fatalf("started = %#v, result = %#v, want the stalled run itself carried on", harness.started, result)
	}
	state := harness.reload(t)
	if state.Status != runstate.StatusRunning || state.Phase != runstate.PhaseDeveloping || state.ProviderSessionID != "developer-session" {
		t.Fatalf("continued run = %#v, want it developing again in the session it stalled in", state)
	}
	// The attempt is not counted, because there was no failure to answer: what
	// the continuation buys is the attempt that was interrupted, and charging one
	// would take it off a budget that had bought nothing.
	if state.RepairAttempts != 0 {
		t.Fatalf("repair attempts = %d, want a stall to count none", state.RepairAttempts)
	}
	if len(state.RepairContinuations) != 1 || !state.RepairContinuations[0].Stall {
		t.Fatalf("continuations = %#v, want the one continuation recorded as a stall", state.RepairContinuations)
	}
	// The item's grant is still consumed by it, which is what keeps one decision
	// to one continuation: the guard that refuses a second reads exactly this.
	if carried := state.CarriedOutRepairAttempts(); carried != continueGrantRounds {
		t.Fatalf("carried out = %d of a grant of %d, want the decision's whole grant consumed by the one continuation it authorized",
			carried, continueGrantRounds)
	}
	// And what the item and the run record says what it was, rather than
	// borrowing the repair's account of a change somebody complained about.
	for _, want := range []string{"continued in the developer session it stalled in", "counts no review round and no repair attempt"} {
		if !strings.Contains(result.Reason, want) {
			t.Fatalf("reason = %q, does not say %q", result.Reason, want)
		}
	}
}

// A stalled attempt may never have written anything, and an empty worktree is
// exactly what the attempt it is owed starts from. The gate that refuses a
// handback arriving on a worktree holding none of its change is therefore not
// asked here — asking it would refuse the decision this carry-out exists for —
// and it is still asked of every continuation that is a repair of a change.
func TestAStalledAttemptIsCarriedOnIntoAWorktreeThatHoldsNothingYet(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, stalledState())
	harness.ownership.Changed = []string{}
	result, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err != nil {
		t.Fatalf("Continue() error = %v, want a stalled first attempt carried on into the worktree it had not written to", err)
	}
	if !result.Continued || len(harness.ownership.Read) != 0 {
		t.Fatalf("continued = %t, change reads = %#v, want the continuation made without asking for a change nobody made",
			result.Continued, harness.ownership.Read)
	}
	// The worktree is still proved to be the one the harness left, which is the
	// architect's condition and is asked of every continuation.
	if len(harness.ownership.Asked) != 1 {
		t.Fatalf("ownership checks = %#v, want the preserved worktree still proved to be the harness's", harness.ownership.Asked)
	}

	// A repair of a change is unchanged: an empty worktree there is refused
	// before the grant is spent.
	repairing := newContinueHarness(t, continuableState())
	repairing.ownership.Changed = []string{}
	if _, err := repairing.continuer().Continue(context.Background(), continueRequest()); !errors.Is(err, ErrPreservedChangeMissing) {
		t.Fatalf("Continue() error = %v, want a handback onto an empty worktree still refused", err)
	}
}

// The harness carries decisions out; it does not make them. An item nobody
// granted a repair is an item nobody decided this about, and the size of what a
// grant is worth is read from that record rather than from the configuration a
// second time.
func TestARepairIsRefusedWithoutTheDevelopmentManagersGrant(t *testing.T) {
	t.Parallel()

	harness := newUndecidedHarness(t, continuableState())
	_, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err == nil || !strings.Contains(err.Error(), "recorded no triage decision about the stoppage of run "+docketedRunID+" on "+docketedItem+"'s triage record") {
		t.Fatalf("Continue() error = %v, want a refusal naming the missing record", err)
	}
	if len(harness.started) != 0 || harness.tracker.Claimed {
		t.Fatalf("started = %#v, claimed = %t, want nothing continued on nobody's decision", harness.started, harness.tracker.Claimed)
	}
	// Decided, the same carry-out runs, and it hands the run the rounds the
	// decision was worth rather than a number of its own.
	recordRepairDecision(t, harness.runs, docketedItem)
	result, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err != nil {
		t.Fatalf("Continue() after the decision was recorded error = %v", err)
	}
	if result.Granted != continueGrantRounds || result.Decided != continueGrantRounds {
		t.Fatalf("granted = %d of %d decided, want the recorded grant carried out", result.Granted, result.Decided)
	}
}

// A repair the development manager has since decided against is not one to
// carry out. One decision stands per stopped run, and a re-run recorded in the
// repair's place released the rounds the repair reserved — which is
// yoyodyne-ifd.309's shape, a repair the harness could not carry out and a
// re-run recorded instead — so a repair carried out on that run afterwards would
// spend attempts the item's record no longer holds room for, on a decision
// nobody holds any more.
func TestARepairIsRefusedOnceARerunStandsInItsPlace(t *testing.T) {
	t.Parallel()

	state := continuableState()
	harness := newUndecidedHarness(t, state)
	// The repair decided about this very stoppage, then the re-run decided about
	// it instead.
	if _, err := harness.runs.Triage().GrantRepair(context.Background(), state.WorkItemID, triageDecided(runstate.TriageDecisionRepair, state.RunID), continueGrantRounds, docketedNow, continueCaps); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	if _, err := harness.runs.Triage().RecordRerun(context.Background(), state.WorkItemID, triageDecided(runstate.TriageDecisionRerun, state.RunID), docketedNow, continueCaps); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}
	_, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err == nil || !strings.Contains(err.Error(), `is "rerun" rather than a repair`) {
		t.Fatalf("Continue() error = %v, want a refusal naming the re-run standing in the repair's place", err)
	}
	if len(harness.started) != 0 || harness.tracker.Claimed {
		t.Fatalf("started = %#v, claimed = %t, want nothing continued on a superseded decision", harness.started, harness.tracker.Claimed)
	}
	// And the reservation went with the repair: the item stands committed to
	// nothing beyond what it has spent.
	if spent := harness.spent(t); spent.CommittedRounds != spent.ReviewRounds {
		t.Fatalf("committed rounds = %d with %d spent, want the superseded repair's reservation released", spent.CommittedRounds, spent.ReviewRounds)
	}
}

// The invariant's second half, and the item's own requirement: the reasoning is
// recorded durably in both places a later reader looks — on the run, which is
// what outlives the process, and on the item, which is what a person reads.
func TestARepairRecordsTheTriageReasoningOnTheRunAndTheItem(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	result, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	recorded := harness.reload(t).RepairContinuations[0]
	if !strings.Contains(recorded.Reason, rerunReasoning) {
		t.Fatalf("run reason = %q, want the reasoning the decision was recorded with", recorded.Reason)
	}
	// And the record it was read from is cited, so the attribution names a turn
	// somebody can go and check rather than only the role.
	if !strings.Contains(recorded.Reason, "recorded by the development manager in conversation "+decidedIn) {
		t.Fatalf("run reason = %q, want it to cite the decision it was read from", recorded.Reason)
	}
	// The stoppage it settles and the grant it verified are named as well as the
	// argument: a reason carrying only the prose would not say what was spent.
	for _, want := range []string{docketedRunID, docketedItem, "2 further repair attempt(s)"} {
		if !strings.Contains(recorded.Reason, want) {
			t.Fatalf("run reason = %q, is missing %q", recorded.Reason, want)
		}
	}
	if recorded.Reason != result.Reason || !strings.Contains(harness.tracker.Notes, result.Reason) {
		t.Fatalf("the item's notes (%q) and the run (%q) do not carry the same reasoning", harness.tracker.Notes, recorded.Reason)
	}
	// A reason the run state would refuse to hold would be a reason nothing
	// records.
	if err := harness.reload(t).Validate(); err != nil {
		t.Fatalf("the continued run is not one the store would hold: %v", err)
	}
}

// The architect's constraint (b). A run that is going again has not stopped, and
// the blocker on its record is what the docket, the status surface, and
// reconciliation all read as the fact that it has.
func TestARepairSupersedesTheBlockerOnBothTheRunAndTheItem(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	result, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	state := harness.reload(t)
	if state.Blocker != "" || state.Failure != "" {
		t.Fatalf("continued run still says it stopped: blocker = %q, failure = %q", state.Blocker, state.Failure)
	}
	// Clearing it does not lose it: the words it was recorded in travel with the
	// continuation that superseded them.
	superseded := continuableState().Blocker
	if state.RepairContinuations[0].SupersededBlocker != superseded || result.SupersededBlocker != superseded {
		t.Fatalf("superseded blocker = %q / %q, want the blocker the run stopped on", state.RepairContinuations[0].SupersededBlocker, result.SupersededBlocker)
	}
	// The docket agrees, which is what stops the same stoppage being docketed a
	// second time behind a run that is going again.
	if stoppedRun(state) {
		t.Fatalf("a run that is going again is still docketable as stopped work: %#v", state)
	}
	// And on the item: the re-entry is what puts it back, rather than somebody
	// remembering to reopen it first.
	if !harness.tracker.Claimed || harness.tracker.Item.Status != "in_progress" {
		t.Fatalf("item status = %q, claimed = %t, want the item put back by the re-entry itself", harness.tracker.Item.Status, harness.tracker.Claimed)
	}
	// The decision is recorded before the claim, so the item never reads as work
	// somebody quietly restarted.
	if got := strings.Join(harness.tracker.Calls, ","); got != "record,claim" {
		t.Fatalf("tracker calls = %q, want the decision recorded and then the item claimed", got)
	}
	// An item put back from blocked is not told about a claim it never held.
	if strings.Contains(harness.tracker.Notes, "still read in_progress") {
		t.Fatalf("item notes = %q, want no account of a claim the item did not carry", harness.tracker.Notes)
	}
}

// The item's other stale status: the stopped run left it claimed rather than
// blocked. The run is terminal and nothing of the item is in flight, so the
// claim has nothing working behind it; the continuation supersedes it, and the
// item is told what moved it and why before the claim changes hands.
func TestARepairSupersedesAClaimTheStoppedRunLeftAndSaysSo(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	harness.tracker.Item.Status = "in_progress"
	harness.tracker.Claimed = true
	result, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if !result.Continued || len(harness.started) != 1 {
		t.Fatalf("continued = %t, started = %#v, want the decision carried out", result.Continued, harness.started)
	}
	if got := strings.Join(harness.tracker.Calls, ","); got != "record,claim" {
		t.Fatalf("tracker calls = %q, want the account recorded and then the item claimed", got)
	}
	for _, want := range []string{result.Reason, "still read in_progress from run " + result.RunID, "no run of this item in flight"} {
		if !strings.Contains(harness.tracker.Notes, want) {
			t.Fatalf("item notes = %q, want them to say %q", harness.tracker.Notes, want)
		}
	}
}

// A claim a live run holds is not the stopped run's to supersede, and giving it
// to the continuation would put two developers on one piece of work.
func TestARepairLeavesAClaimALiveRunHolds(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	harness.tracker.Item.Status = "in_progress"
	live := runningState("run-00001111222233334444555566667777", continuableState().WorkItemID)
	if err := harness.runs.Create(live); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	_, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err == nil || !strings.Contains(err.Error(), live.RunID) {
		t.Fatalf("Continue() error = %v, want a refusal naming the live run %s", err, live.RunID)
	}
	if len(harness.started) != 0 || harness.tracker.Notes != "" || len(harness.tracker.Calls) != 0 {
		t.Fatalf("started = %#v, notes = %q, calls = %v, want nothing continued or written", harness.started, harness.tracker.Notes, harness.tracker.Calls)
	}
}

// The per-item grant counter is what bounds this: triage acts alone once, so an
// item whose grant has been carried out has no decision of its own left to act
// on, and a second is an escalation rather than a larger budget.
func TestASecondRepairOfOneItemIsRefusedOnceTheGrantIsCarriedOut(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	if _, err := harness.continuer().Continue(context.Background(), continueRequest()); err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	// The continued run stopped again and was docketed like any other stoppage.
	second := harness.reload(t)
	second.Status = runstate.StatusFailed
	completed := docketedNow
	second.CompletedAt = &completed
	second.Blocker = "Yoyodyne stopped this item: the granted repair budget was spent too."
	harness.save(t, second)

	_, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err == nil || !strings.Contains(err.Error(), "the harness has carried out 2") {
		t.Fatalf("second Continue() error = %v, want a refusal naming the grant already carried out", err)
	}
	if len(harness.started) != 1 {
		t.Fatalf("started = %#v, want exactly one continuation for one grant", harness.started)
	}
	// And deciding a second is what the cap makes a person's decision rather than
	// this action's.
	if _, err := harness.runs.Triage().GrantRepair(context.Background(), docketedItem, triageDecided(runstate.TriageDecisionRepair, decidedRunID), continueGrantRounds, docketedNow, continueCaps); !errors.Is(err, runstate.ErrTriageCapReached) {
		t.Fatalf("GrantRepair() error = %v, want a second grant of one item refused", err)
	}
}

// The other bound the item names: the rounds cap, which is what an item may cost
// in total across every run of it. Past it another repair is not triage's to
// grant at all, so the decision this action carries out is never made — and this
// finds no grant to act on.
func TestARepairIsRefusedOnceTheRoundCapHasNoRoomLeft(t *testing.T) {
	t.Parallel()

	harness := newUndecidedHarness(t, continuableState())
	for round := 0; round < continueCaps.ReviewRounds; round++ {
		if _, err := harness.runs.Triage().RecordReviewRound(context.Background(), docketedItem,
			runstate.RoundKey(docketedRunID, round), countingProcess, docketedNow); err != nil {
			t.Fatalf("RecordReviewRound() error = %v", err)
		}
	}
	// The development manager's own decision is what the cap refuses, and it
	// refuses it by the round budget rather than by the grant's own.
	_, grantErr := harness.runs.Triage().GrantRepair(context.Background(), docketedItem, triageDecided(runstate.TriageDecisionRepair, decidedRunID), continueGrantRounds, docketedNow, continueCaps)
	var capped runstate.TriageCapError
	if !errors.As(grantErr, &capped) {
		t.Fatalf("GrantRepair() error = %v, want a cap refusal", grantErr)
	}
	if _, refusedByRounds := capped.RefusedBy(runstate.TriageReviewRoundBudget); !refusedByRounds {
		t.Fatalf("GrantRepair() error = %v, want the review round budget to refuse it", grantErr)
	}

	_, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err == nil || !strings.Contains(err.Error(), "recorded no triage decision") {
		t.Fatalf("Continue() error = %v, want nothing to carry out past the cap", err)
	}
	if len(harness.started) != 0 || harness.tracker.Claimed {
		t.Fatalf("started = %#v, claimed = %t, want nothing continued past the cap", harness.started, harness.tracker.Claimed)
	}
	if state := harness.reload(t); state.Blocker == "" || !state.Status.Terminal() {
		t.Fatalf("a refused repair superseded the blocker anyway: %#v", state)
	}
}

// A grant the round cap cut is carried out at the size it was recorded, not the
// size the configuration asks for: the cut is what says the item is at the end
// of what it will be given, and a carry-out reading the configuration again
// would hand the run attempts the cap never let it have.
func TestARepairCarriesOutTheGrantAtTheSizeTheCapLeftIt(t *testing.T) {
	t.Parallel()

	harness := newUndecidedHarness(t, continuableState())
	for round := 0; round < continueCaps.ReviewRounds-1; round++ {
		if _, err := harness.runs.Triage().RecordReviewRound(context.Background(), docketedItem,
			runstate.RoundKey(docketedRunID, round), countingProcess, docketedNow); err != nil {
			t.Fatalf("RecordReviewRound() error = %v", err)
		}
	}
	if granted := recordRepairDecision(t, harness.runs, docketedItem); granted.Rounds != 1 || !granted.Truncated {
		t.Fatalf("the decision granted %#v, want it cut to the one round the cap had left", granted)
	}

	result, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if result.Granted != 1 || result.Decided != 1 || !result.Truncated {
		t.Fatalf("granted = %d of %d decided, truncated = %t, want the one round the cap left", result.Granted, result.Decided, result.Truncated)
	}
	if granted := harness.reload(t).RepairContinuations[0]; granted.GrantedAttempts != 1 {
		t.Fatalf("recorded grant = %#v, want the run handed only what the cap left", granted)
	}
	if budget := harness.reload(t).RepairBudget(2); budget != 3 {
		t.Fatalf("repair budget = %d, want the configured two plus the one round granted", budget)
	}
	if !strings.Contains(result.Reason, "already cut to 1") {
		t.Fatalf("reason = %q, want the cut said out loud", result.Reason)
	}
}

// The invariant's first half. Continuing a run spends on a provider and the
// development manager naming the item is not the operator naming it, so the hold
// applies — and nothing is spent under one, which is what leaves the item its
// grant for afterwards.
func TestAHeldIntakeContinuesNothingAndSpendsNothing(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	held, err := harness.intake.Hold(runstate.IntakeHolderOperator, "the queue is heading somewhere odd", docketedNow)
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	result, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err != nil {
		t.Fatalf("Continue() error = %v, want a held intake reported rather than a failure", err)
	}
	if result.Continued || len(harness.started) != 0 {
		t.Fatalf("continued = %t / %#v, want nothing continued under a hold", result.Continued, harness.started)
	}
	if result.IntakeHeld == nil || !result.IntakeHeld.HeldAt.Equal(held.HeldAt) {
		t.Fatalf("intake held = %#v, want the hold that stopped it", result.IntakeHeld)
	}
	if carried := harness.carried(t); carried != 0 {
		t.Fatalf("carried out = %d, want the item to keep its grant", carried)
	}
	if state := harness.reload(t); state.Blocker == "" {
		t.Fatalf("a held carry-out superseded the blocker anyway: %#v", state)
	}
	// Released, the same decision is carried out: the hold delayed the repair
	// rather than consuming it.
	if _, _, err := harness.intake.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if _, err := harness.continuer().Continue(context.Background(), continueRequest()); err != nil {
		t.Fatalf("Continue() after the hold was released error = %v", err)
	}
	if len(harness.started) != 1 {
		t.Fatalf("started = %#v, want the repair to have run once the hold was lifted", harness.started)
	}
}

// The architect's constraint (a). What a continued developer is handed back is
// whatever is in that worktree, so a worktree something has touched since the
// blocker is a person's to look at — and asking before anything is spent is what
// makes the refusal free.
func TestARepairRefusesAWorktreeThatIsNotAsTheHarnessLeftIt(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	harness.ownership.Err = errors.New("worktree HEAD is 9f9f9f, want the commit the harness recorded (aaaaaa)")

	_, err := harness.continuer().Continue(context.Background(), continueRequest())
	if !errors.Is(err, ErrWorktreeNotAsLeft) {
		t.Fatalf("Continue() error = %v, want the worktree refused", err)
	}
	// The refusal names what it is escalating and why, because nothing else here
	// will notice the worktree.
	if !strings.Contains(err.Error(), "a person's to look at") {
		t.Fatalf("refusal = %v, want it to say whose decision this now is", err)
	}
	if len(harness.started) != 0 {
		t.Fatalf("started = %#v, want nothing continued", harness.started)
	}
	if carried := harness.carried(t); carried != 0 {
		t.Fatalf("carried out = %d, want a refused re-entry to have spent nothing of the grant", carried)
	}
	// The item is left blocked, which is the durable state an escalation would
	// have made anyway.
	if harness.tracker.Claimed || harness.tracker.Item.Status != "blocked" {
		t.Fatalf("item status = %q, claimed = %t, want it left waiting on a person", harness.tracker.Item.Status, harness.tracker.Claimed)
	}
	if state := harness.reload(t); state.Blocker == "" || !state.Status.Terminal() {
		t.Fatalf("a refused repair superseded the blocker anyway: %#v", state)
	}
	// The gate was asked about this run's own worktree, from the run's record
	// rather than from the docket entry that describes it.
	if len(harness.ownership.Asked) != 1 || harness.ownership.Asked[0].RunID != docketedRunID {
		t.Fatalf("ownership asked about %#v, want the stopped run's own worktree", harness.ownership.Asked)
	}
}

// The 309 shape: an approved change the environment stopped short of its
// promotion, asked for a repair. The refusal it used to get was true — no
// findings, no failing check, no refused paths — and pointed nowhere, and what
// that bought was a re-run and four overrides for a change nobody disputed. So
// the refusal says what the run is and names the verb that resumes it, with the
// cause, in one sentence; and the docket entry the development manager read
// before asking carries the same sentence, so the two cannot send her to
// different places.
func TestARepairOfAnApprovedChangeTheEnvironmentStoppedNamesTheResumeVerb(t *testing.T) {
	t.Parallel()

	stopped := approvedStoppedState()
	harness := newContinueHarness(t, stopped)
	_, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err == nil {
		t.Fatal("Continue() error = nil, want an approved change the environment stopped refused a repair")
	}
	refusal := err.Error()
	want := "run " + docketedRunID + "'s change is approved and the environment stopped it at the integrating phase — dirty-primary (the primary checkout carried state the harness does not own) — so what it needs is `yoyo triage resume " + docketedRunID + "` once the cause has cleared, which resumes the promotion with the approval standing and charges no review round, repair grant, or re-run"
	if refusal != want {
		t.Fatalf("refusal = %q\nwant      %q", refusal, want)
	}
	// One sentence: it says what the run is, the cause, and the verb, and it does
	// not say the true-and-useless thing it replaced.
	if strings.Count(refusal, ". ") != 0 || strings.Contains(refusal, "no reviewer findings") || strings.Contains(refusal, "nothing to repair") {
		t.Fatalf("refusal is not the one sentence naming the resume path: %q", refusal)
	}
	// The docket entry she read before asking says the same sentence, so what the
	// entry told her to do and what the refusal tells her to do are one thing.
	if len(harness.docket.entries) != 1 {
		t.Fatalf("docket = %#v, want the one stopped run", harness.docket.entries)
	}
	if rendered := harness.docket.entries[0].Render(); !strings.Contains(rendered, want) {
		t.Fatalf("the docket entry does not carry the refusal's sentence:\n%s", rendered)
	}
	// Nothing was spent and nothing was written: the grant is still the item's,
	// the item is still where the stop left it, and the run is still stopped with
	// its stop on the record for the resume to read.
	if len(harness.started) != 0 || harness.tracker.Claimed {
		t.Fatalf("started = %#v, claimed = %t, want nothing continued", harness.started, harness.tracker.Claimed)
	}
	if carried := harness.carried(t); carried != 0 {
		t.Fatalf("carried out = %d, want the refusal to have spent nothing of the grant", carried)
	}
	if state := harness.reload(t); state.IntegrationStop == nil || !state.ResumableIntegration() {
		t.Fatalf("a refused repair changed the stopped run: %#v", state)
	}
}

// The same stop once its branch is gone. The resume would refuse, so the
// repair's refusal names no resume: it says the branch is gone and that a re-run
// is the way on, which is what the docket entry for the same stoppage says, by
// the same look and the same rule.
func TestARepairOfAnApprovedChangeWhoseBranchIsGoneNamesNoResume(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, approvedStoppedState())
	continuer := harness.continuer()
	continuer.Remains = &orchestratortest.Survival{Survival: gitworktree.Survival{WorktreePresent: true}}
	_, err := continuer.Continue(context.Background(), continueRequest())
	if err == nil {
		t.Fatal("Continue() error = nil, want an approved change the environment stopped refused a repair")
	}
	refusal := err.Error()
	if strings.Contains(refusal, "triage resume") {
		t.Fatalf("refusal = %q, want no resume named once the branch is gone", refusal)
	}
	for _, want := range []string{"run " + docketedRunID + "'s branch is gone", "checked and NOT there", "a re-run is the way on"} {
		if !strings.Contains(refusal, want) {
			t.Fatalf("refusal = %q, want it to say %q", refusal, want)
		}
	}
	if len(harness.started) != 0 || harness.carried(t) != 0 {
		t.Fatalf("started = %#v, carried = %d, want nothing continued and nothing spent", harness.started, harness.carried(t))
	}
}

// An approving verdict can carry minor findings, which read as a failure
// returned to the developer. The stop is asked about ahead of them, because a
// repair loop re-entered on them would spend a grant to have an approved change
// repaired — and the answer is the same whatever else the record holds.
func TestARepairIsRefusedForAnApprovedStopEvenWhereTheVerdictCarriedFindings(t *testing.T) {
	t.Parallel()

	stopped := approvedStoppedState()
	stopped.ReviewFindings = 1
	stopped.ReviewFindingDetails = []runstate.Finding{{Severity: "minor", Message: "a comment could be shorter"}}
	harness := newContinueHarness(t, stopped)
	_, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err == nil || !strings.Contains(err.Error(), "`yoyo triage resume "+docketedRunID+"`") {
		t.Fatalf("Continue() error = %v, want the resume verb named over the verdict's findings", err)
	}
	if len(harness.started) != 0 || harness.carried(t) != 0 {
		t.Fatalf("started = %#v, carried = %d, want nothing continued and nothing spent", harness.started, harness.carried(t))
	}
}

// The failure this item was filed for: a handback that arrives on a worktree
// holding none of the change it is a repair of. It is refused rather than
// carried out, because what a continued developer would be given is the
// reviewer's findings about a change that is not in front of it — which is
// delivered as an empty repair or as the same change reinvented by hand.
func TestARepairRefusesAWorktreeThatHoldsNoneOfThePreservedChange(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	// As the harness left it, and empty: the ownership gate passes and this is the
	// only thing that catches it.
	harness.ownership.Changed = []string{}

	_, err := harness.continuer().Continue(context.Background(), continueRequest())
	if !errors.Is(err, ErrPreservedChangeMissing) {
		t.Fatalf("Continue() error = %v, want the handback refused for holding no change", err)
	}
	// The refusal names what it is escalating and why, because a worktree that
	// looks valid is what made this silent in the first place.
	if !strings.Contains(err.Error(), "a person's to look at") {
		t.Fatalf("refusal = %v, want it to say whose decision this now is", err)
	}
	if len(harness.started) != 0 {
		t.Fatalf("started = %#v, want nothing continued", harness.started)
	}
	if carried := harness.carried(t); carried != 0 {
		t.Fatalf("carried out = %d, want a refused re-entry to have spent nothing of the grant", carried)
	}
	if harness.tracker.Claimed || harness.tracker.Item.Status != "blocked" {
		t.Fatalf("item status = %q, claimed = %t, want it left waiting on a person", harness.tracker.Item.Status, harness.tracker.Claimed)
	}
	if state := harness.reload(t); state.Blocker == "" || !state.Status.Terminal() {
		t.Fatalf("a refused repair superseded the blocker anyway: %#v", state)
	}
	// The change was read from the stopped run's own worktree, from the run's
	// record rather than from the docket entry that describes it.
	if len(harness.ownership.Read) != 1 || harness.ownership.Read[0].RunID != docketedRunID {
		t.Fatalf("the change was read from %#v, want the stopped run's own worktree", harness.ownership.Read)
	}
}

// A preserved worktree nobody can read at all is the same answer as an empty
// one: there is no change to hand a developer, and which of the two happened is
// a person's to find out.
func TestARepairRefusesAPreservedWorktreeItCannotRead(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	harness.ownership.ReadErr = errors.New("worktree is not registered with the expected branch")

	_, err := harness.continuer().Continue(context.Background(), continueRequest())
	if !errors.Is(err, ErrPreservedChangeMissing) {
		t.Fatalf("Continue() error = %v, want the handback refused for a change it could not read", err)
	}
	if len(harness.started) != 0 || harness.carried(t) != 0 {
		t.Fatalf("started = %#v, carried = %d, want nothing continued and nothing spent", harness.started, harness.carried(t))
	}
}

// The defect this action was built after: every pre-flight refusal is asked
// before the counter is spent, so a refused carry-out costs the item nothing and
// asking again once it no longer applies carries out the same decision.
func TestEveryRefusalIsAskedBeforeTheGrantIsSpent(t *testing.T) {
	t.Parallel()

	for _, refusal := range []struct {
		name    string
		arrange func(*testing.T, *continueHarness)
		want    string
	}{
		{
			// A run that was docketed and has since been picked up again is owed
			// the rest of its own step.
			name: "the stopped run is running again",
			arrange: func(t *testing.T, h *continueHarness) {
				state := h.reload(t)
				state.Status = runstate.StatusRunning
				state.CompletedAt = nil
				h.save(t, state)
			},
			want: "resumable",
		},
		{
			// A run that stopped with no failure ever returned to its developer
			// has nothing to carry on with — unless the harness is what stopped
			// it, which is the one exception and is
			// TestARepairCarriesOnAStalledAttemptAndChargesItNothing.
			name: "nothing was ever returned to the developer",
			arrange: func(t *testing.T, h *continueHarness) {
				state := h.reload(t)
				state.ReviewFindingDetails = nil
				state.ReviewFindings = 0
				state.CheckFailure = nil
				state.PathRefusal = nil
				h.save(t, state)
			},
			want: "there is no attempt to carry on with",
		},
		{
			// A run whose artifacts triage already retired has nothing left to
			// continue in.
			name: "what it preserved has been retired",
			arrange: func(t *testing.T, h *continueHarness) {
				state := h.reload(t)
				state.WorktreeRemoved = true
				state.BranchRemoved = true
				state.ArtifactsRetiredBy = "run-11112222333344445555666677778888"
				h.save(t, state)
			},
			want: "no verified branch and checkout",
		},
		{
			// An item somebody closed is not one a stopped run may be continued
			// on, whatever its budget still says.
			name:    "the item was closed",
			arrange: func(_ *testing.T, h *continueHarness) { h.tracker.Item.Status = "closed" },
			want:    `status is "closed"`,
		},
		{
			// An item waiting on other work is refused for the reason a fresh run
			// of it would be.
			name: "the item waits on other work",
			arrange: func(_ *testing.T, h *continueHarness) {
				h.tracker.Item.Dependencies = []beads.Dependency{{ID: "yoyodyne-ifd.9", Type: "blocks", Status: "open"}}
			},
			want: "is blocked by",
		},
	} {
		t.Run(refusal.name, func(t *testing.T) {
			t.Parallel()
			harness := newContinueHarness(t, continuableState())
			refusal.arrange(t, harness)

			_, err := harness.continuer().Continue(context.Background(), continueRequest())
			if err == nil || !strings.Contains(err.Error(), refusal.want) {
				t.Fatalf("Continue() error = %v, want a refusal naming %q", err, refusal.want)
			}
			if len(harness.started) != 0 {
				t.Fatalf("started = %#v, want nothing continued", harness.started)
			}
			if carried := harness.carried(t); carried != 0 {
				t.Fatalf("carried out = %d, want a refused carry-out to have spent nothing of the grant", carried)
			}
		})
	}
}

// A full harness says nothing about whether the run should go on, and stops
// being true on its own. So it is a state to wait on rather than a refusal, and
// waiting costs the item nothing because it is asked before the grant.
func TestAFullHarnessWaitsRatherThanSpendingTheGrant(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	harness.capacity = 1
	other := continuableState()
	other.RunID = "run-11112222333344445555666677778888"
	other.WorkItemID = "yoyodyne-ifd.other"
	other.Status = runstate.StatusRunning
	other.CompletedAt = nil
	other.Blocker = ""
	if err := harness.runs.Create(other); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	result, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err != nil {
		t.Fatalf("Continue() error = %v, want a full harness reported rather than a failure", err)
	}
	if result.Continued || result.CapacityFull == nil || result.CapacityFull.Limit != 1 {
		t.Fatalf("result = %#v, want the harness reported as full", result)
	}
	if carried := harness.carried(t); carried != 0 {
		t.Fatalf("carried out = %d, want the item to keep its grant", carried)
	}
	if !strings.Contains(result.Render(), "keeps its repair grant") {
		t.Fatalf("render = %q, want it to say the decision still stands", result.Render())
	}
}

// A run something is already running is not work that has stopped, whatever the
// docket entry said when it was written.
func TestARepairIsRefusedWhileTheItemHasARunInFlight(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	live := continuableState()
	live.RunID = "run-11112222333344445555666677778888"
	live.Status = runstate.StatusRunning
	live.CompletedAt = nil
	live.Blocker = ""
	if err := harness.runs.Create(live); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err == nil || !strings.Contains(err.Error(), "in flight") {
		t.Fatalf("Continue() error = %v, want the live run to refuse it", err)
	}
	if carried := harness.carried(t); carried != 0 {
		t.Fatalf("carried out = %d, want nothing of the grant spent", carried)
	}
}

// A decision names the stoppage it settles, and a run nothing docketed is not a
// stoppage this may act on.
func TestARepairNeedsADocketedStoppage(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	for _, request := range []struct {
		name string
		ask  RepairContinueRequest
		want string
	}{
		{name: "not a run", ask: RepairContinueRequest{Run: "yoyodyne-ifd.102.5"}, want: "not a run identifier"},
		{
			name: "not on the docket",
			ask:  RepairContinueRequest{Run: "run-11112222333344445555666677778888"},
			want: "no stoppage to repair",
		},
	} {
		t.Run(request.name, func(t *testing.T) {
			if _, err := harness.continuer().Continue(context.Background(), request.ask); err == nil || !strings.Contains(err.Error(), request.want) {
				t.Fatalf("Continue() error = %v, want %q", err, request.want)
			}
		})
	}
}

// A carry-out wired without what bounds it, or without the parts that make the
// re-entry safe, refuses rather than inventing either.
func TestARepairRefusesToActWithoutWhatBoundsIt(t *testing.T) {
	t.Parallel()

	_, err := RepairContinuer{}.Continue(context.Background(), continueRequest())
	if err == nil {
		t.Fatal("Continue() with nothing wired started something")
	}
	for _, want := range []string{"triage docket", "intake hold", "triage budget", "work item", "worktree", "developer capacity"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal = %v, is missing %q", err, want)
		}
	}
}

// yoyodyne-ifd.132 past the run's own budget: a replay conflict on a run with no
// repair attempt left blocks, keeping the conflict on its record, and the repair
// the development manager grants afterwards hands that conflict to the same
// developer session — moved onto the target first — rather than leaving a fresh
// run as the only way on.
func TestARepairContinuationReconcilesAReplayConflictTheStoppedRunCouldNot(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	stopping := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("this run's version\n"), 0o600); err != nil {
			return err
		}
		writePipelineFile(t, repository, "feature.txt", "someone else's version\n")
		runPipelineGit(t, repository, "add", "feature.txt")
		runPipelineGit(t, repository, "commit", "-m", "a conflicting edit on main")
		return nil
	}, approveVerdict)
	build := func(provider *orchestratortest.Backend) Pipeline {
		pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"test -f feature.txt"}), provider)
		pipeline.Config.Execution.RepairAttemptsBeforeReplan = 0
		return pipeline
	}
	pipeline := build(stopping)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !errors.Is(err, gitworktree.ErrRebaseConflict) {
		t.Fatalf("Run() error = %v, want the conflict stopping a run with no attempt left", err)
	}
	stopped, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stopped.ReplayConflict == nil || stopped.ReplayConflict.Moved {
		t.Fatalf("stopped run recorded conflict %#v, want it kept and not yet moved", stopped.ReplayConflict)
	}
	// Under a loaded machine the local Git budget can kill the replay part-way,
	// and the worktree manager still reports a killed rebase as a conflict whose
	// "exit code -1" reads as the environment's — yoyodyne-ifd.406, not this
	// path. Such a run is owed `yoyo triage resume` rather than a repair, so what
	// this test is about cannot be exercised on it, and saying so is better than
	// failing on a defect another item owns.
	if stopped.IntegrationStop != nil {
		t.Skipf("the replay was stopped by the environment rather than by a conflict (%s: %s); see yoyodyne-ifd.406",
			stopped.IntegrationStop.Cause, stopped.IntegrationStop.Detail)
	}
	docket := &memoryDocket{}
	if _, err := docketerOverStore(docket, store, pipeline.Config).RecordStoppedRun(stopped); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	worktrees, err := gitworktree.New(gitworktree.Options{Runner: execution.OSProcessRunner{}, RepositoryRoot: repository, WorktreeRoot: worktreeRoot, Timeout: testGitBudget})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewIntakeHoldStore() error = %v", err)
	}
	var markersSeen bool
	continuing := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		path := filepath.Join(request.WorkingDirectory, "feature.txt")
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		markersSeen = strings.Contains(string(content), "<<<<<<<")
		return os.WriteFile(path, []byte("both versions, reconciled\n"), 0o600)
	}, approveVerdict)
	if _, err := store.Triage().GrantRepair(context.Background(), tracker.Item.ID, triageDecided(runstate.TriageDecisionRepair, outcome.RunID),
		TriageRepairGrantRounds(pipeline.Config.Triage), time.Now(), TriageCaps(pipeline.Config.Execution, pipeline.Config.Triage)); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	continuer := RepairContinuer{
		Docket: docket, Runs: store, Intake: intake, Decisions: store.Triage(), Items: tracker, Worktrees: worktrees,
		ConfiguredAttempts: 0,
		Capacity:           pipeline.Config.Execution.MaxConcurrentDevelopers,
		Start: func(ctx context.Context, workItemID, runID string) (Outcome, error) {
			return build(continuing).Continue(ctx, workItemID, runID)
		},
	}

	result, err := continuer.Continue(context.Background(), RepairContinueRequest{Run: outcome.RunID})
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if !result.Continued || result.Outcome.RunID != outcome.RunID || result.Outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("result = %#v, closed = %t; want the same run continued and its reconciled change landed", result, tracker.Closed)
	}
	if !markersSeen {
		t.Fatal("the continued developer was not handed the change moved onto the target with the conflict left in it")
	}
	developerRequests := continuing.RequestsForRole(domain.RoleDeveloper)
	if len(developerRequests) != 1 || developerRequests[0].SessionID != stopping.DeveloperSession {
		t.Fatalf("continued developer invocations = %d, want one in the stopped run's own session", len(developerRequests))
	}
	if !strings.Contains(developerRequests[0].Prompt, "Integration conflict: repair required") {
		t.Fatalf("continued prompt does not hand back the conflict:\n%s", developerRequests[0].Prompt)
	}
	if integrated := gitLine(t, repository, "show", "main:feature.txt"); integrated != "both versions, reconciled" {
		t.Fatalf("integrated feature.txt = %q, want the reconciliation", integrated)
	}
}

// The whole of it, over a real repository: a run that spends its repair budget
// and blocks, then goes on under a grant — same branch, same worktree, same
// developer session — and lands the change it already had.
func TestARepairContinuationLandsTheChangeTheStoppedRunAlreadyHad(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// The first developer leaves the reviewer something to object to, and the
	// reviewer keeps objecting until the run's repair budget is spent.
	stopping := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("incomplete\n"), 0o600)
	}, repairVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, stopping, []string{"test -f feature.txt"}), stopping)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), "independent review requires repair") {
		t.Fatalf("Run() error = %v, want the repair budget spent", err)
	}
	if !tracker.Blocked || outcome.Integration != nil {
		t.Fatalf("the stopped run did not block its item: blocked = %t, integration = %#v", tracker.Blocked, outcome.Integration)
	}
	stopped, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	docket := &memoryDocket{}
	if _, err := docketerOverStore(docket, store, pipeline.Config).RecordStoppedRun(stopped); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	worktrees, err := gitworktree.New(gitworktree.Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: repository,
		WorktreeRoot:   worktreeRoot,
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewIntakeHoldStore() error = %v", err)
	}
	// The continued attempt answers the findings; the reviewer then approves.
	continuing := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	// The development manager's decision, recorded exactly as the conversation
	// records one — about the docketed run — and it spends the item's repair
	// grant; the three rounds the stopped run cost leave the cap room for one of
	// the two it asks for.
	granted, err := store.Triage().GrantRepair(context.Background(), tracker.Item.ID, triageDecided(runstate.TriageDecisionRepair, outcome.RunID),
		TriageRepairGrantRounds(pipeline.Config.Triage), time.Now(), TriageCaps(pipeline.Config.Execution, pipeline.Config.Triage))
	if err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	if granted.Rounds != 1 || !granted.Truncated {
		t.Fatalf("the decision granted %#v, want it cut to the one round the cap had left", granted)
	}
	continuer := RepairContinuer{
		Docket:             docket,
		Runs:               store,
		Intake:             intake,
		Decisions:          store.Triage(),
		Items:              tracker,
		Worktrees:          worktrees,
		ConfiguredAttempts: pipeline.Config.Execution.RepairAttemptsBeforeReplan,
		Capacity:           pipeline.Config.Execution.MaxConcurrentDevelopers,
		Start: func(ctx context.Context, workItemID, runID string) (Outcome, error) {
			return automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, continuing, []string{"test -f feature.txt"}), continuing).
				Continue(ctx, workItemID, runID)
		},
	}

	result, err := continuer.Continue(context.Background(), RepairContinueRequest{Run: outcome.RunID})
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if !result.Continued || result.Outcome.RunID != outcome.RunID {
		t.Fatalf("result = %#v, want the same run continued rather than a fresh one", result)
	}
	// The grant was carried out at the size the round cap left it, and one
	// further attempt was all the change needed.
	if result.Granted != 1 || !result.Truncated {
		t.Fatalf("granted = %d, truncated = %t, want the grant carried out at the size the cap left it", result.Granted, result.Truncated)
	}
	if result.Outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("the continued run did not land its change: %#v, closed = %t", result.Outcome.Integration, tracker.Closed)
	}
	// It continued the change the stopped run already had: the same branch and
	// the same worktree, in the developer session that already held the context.
	if result.Outcome.Branch != stopped.Branch || result.Outcome.WorktreePath != stopped.WorktreePath {
		t.Fatalf("continued run moved: branch %q worktree %q, want %q and %q",
			result.Outcome.Branch, result.Outcome.WorktreePath, stopped.Branch, stopped.WorktreePath)
	}
	developerRequests := continuing.RequestsForRole(domain.RoleDeveloper)
	if len(developerRequests) != 1 {
		t.Fatalf("continued developer invocations = %d, want the one attempt the grant bought", len(developerRequests))
	}
	continued := developerRequests[0]
	if continued.SessionID != stopping.DeveloperSession || continued.WorkingDirectory != stopped.WorktreePath {
		t.Fatalf("continued attempt = session %q in %q, want the stopped run's own session and worktree", continued.SessionID, continued.WorkingDirectory)
	}
	// What it was handed back is the reviewer's findings, unedited, numbered
	// against the budget the grant made.
	for _, want := range []string{"repair attempt 3 of 3", `"message": "add the missing file"`} {
		if !strings.Contains(continued.Prompt, want) {
			t.Fatalf("continued prompt is missing %q:\n%s", want, continued.Prompt)
		}
	}
	if integrated := gitLine(t, repository, "show", "main:feature.txt"); integrated != "implemented" {
		t.Fatalf("integrated feature.txt = %q, want the repaired content", integrated)
	}
	// The granted round approved the change, so it spent nothing and the
	// reservation the grant made for it is released: the item stands at the three
	// rounds it cost, not the four it was committed to. That is the accounting
	// yoyodyne-ifd.349 was refused its re-run under, read at the end of a real
	// continuation rather than replayed against the store alone.
	counters, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.ReviewRounds != 3 || counters.CommittedRounds != 3 {
		t.Fatalf("counters after the approved continuation = %d spent, %d committed; want the reserved round released to 3 and 3", counters.ReviewRounds, counters.CommittedRounds)
	}
	if counters.GrantOutstanding() {
		t.Fatal("the grant reads as outstanding after the round it bought was judged, so the docket would go on offering the stoppage a handback on it")
	}
}

// The item's own requirement, from yoyodyne-ifd.368: the grant counter says
// somebody granted this item a repair, and only the decision says it was this
// stoppage. A repair recorded about another run of the item is not one about
// this run, so it is refused naming the record that is missing rather than
// carried out on the strength of the counter.
func TestARepairOfAnotherRunIsNotCarriedOutOnThisOne(t *testing.T) {
	t.Parallel()

	harness := newUndecidedHarness(t, continuableState())
	if _, err := harness.runs.Triage().GrantRepair(context.Background(), docketedItem,
		triageDecided(runstate.TriageDecisionRepair, decidedRunID), continueGrantRounds, docketedNow, continueCaps); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	_, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err == nil || !strings.Contains(err.Error(), "recorded no triage decision about the stoppage of run "+docketedRunID) {
		t.Fatalf("Continue() error = %v, want a refusal naming the missing record for this run", err)
	}
	if len(harness.started) != 0 || harness.tracker.Claimed || harness.tracker.Notes != "" {
		t.Fatalf("started = %#v, claimed = %t, notes = %q, want nothing continued or written", harness.started, harness.tracker.Claimed, harness.tracker.Notes)
	}
}

// A run whose own record names a different item from the docket entry would be
// continued as that entry's work, which is a repair silently retargeted. It is
// refused naming both, before anything is spent or written.
func TestARepairOfARunMadeForAnotherItemIsRefusedNamingIt(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	stored, err := harness.runs.Load(docketedRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	stored.WorkItemID = "yoyodyne-ifd.68.20"
	if err := harness.runs.Save(stored); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	_, err = harness.continuer().Continue(context.Background(), continueRequest())
	if err == nil || !strings.Contains(err.Error(), `made for "yoyodyne-ifd.68.20" while its docket entry names `+docketedItem) {
		t.Fatalf("Continue() error = %v, want a refusal naming both items", err)
	}
	if len(harness.started) != 0 || harness.tracker.Claimed || harness.tracker.Notes != "" {
		t.Fatalf("started = %#v, claimed = %t, notes = %q, want nothing continued or written", harness.started, harness.tracker.Claimed, harness.tracker.Notes)
	}
}

func TestRepairChecksTheRepositoryEvenWhenRemovalFlagsDisagree(t *testing.T) {
	t.Parallel()
	for _, there := range []bool{true, false} {
		state := continuableState()
		state.BranchRemoved, state.WorktreeRemoved = there, there
		if !there {
			state.ArtifactsRetiredBy = ""
		} else {
			state.ArtifactsRetiredBy = priorRunID
		}
		harness := newContinueHarness(t, state)
		continuer := harness.continuer()
		continuer.Remains = &orchestratortest.Survival{Survival: gitworktree.Survival{BranchExists: there, WorktreePresent: there}}
		result, err := continuer.Continue(context.Background(), continueRequest())
		if (err == nil) != there || result.Continued != there || (len(harness.started) > 0) != there {
			t.Fatalf("Continue() = %#v, %v, want continued %t", result, err, there)
		}
		if err != nil && (!strings.Contains(err.Error(), state.Branch) || !strings.Contains(err.Error(), state.WorktreePath)) {
			t.Fatalf("refusal does not name the branch and checkout: %v", err)
		}
	}
}

// The ownership registry's first build stopped here, with no blocker or
// failing check. A decided repair must reach the existing change's checks.
func TestRepairOfAStageTimeoutContinuesItsChecksOnThePreservedChange(t *testing.T) {
	t.Parallel()
	state := continuableState()
	state.Status, state.Phase = runstate.StatusTimedOut, runstate.PhaseChecking
	state.Blocker = ""
	state.Failure = "check stage reached its 30m0s execution.check_stage_timeout bound during make race"
	state.ReviewFindingDetails, state.CheckFailure = nil, nil
	state.ReviewFindings, state.ReviewDecision, state.ReviewSummary = 0, "", ""
	state.CheckStage = &runstate.CheckStage{StartedAt: state.StartedAt, BoundSeconds: 1800, Command: "make race", StoppedAtBound: true}
	harness := newContinueHarness(t, state)
	continuer := harness.continuer()
	continuer.Remains = &orchestratortest.Survival{Survival: gitworktree.Survival{BranchExists: true, WorktreePresent: true}}
	result, err := continuer.Continue(context.Background(), continueRequest())
	if err != nil || !result.Continued || !result.Checks || result.ResumesAt != runstate.PhaseChecking || len(harness.started) != 1 {
		t.Fatalf("Continue() = %#v, %v", result, err)
	}
	continued := harness.reload(t)
	if continued.Phase != runstate.PhaseChecking || continued.RepairAttempts != state.RepairAttempts || continued.ReviewRounds != state.ReviewRounds || !continued.RepairContinuations[0].CheckStage {
		t.Fatalf("continued = %#v, want checks continued without a developer attempt or review round", continued)
	}
}
