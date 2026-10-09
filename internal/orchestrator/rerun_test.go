package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/humangate"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// The decision a development manager records when the ground moved under a
// correct change, and the guidance it leaves on the item for the developer that
// runs it again.
const (
	rerunReasoning = "the change is correct and its base moved under it: main took a rename this branch predates, so nothing here needs repairing and it needs running again from the start"
	// priorRunID is the stopped run in the end-to-end test, which has to be a
	// different run to the one the test pipeline reserves for the fresh attempt.
	priorRunID    = "run-99998888777766665555444433332222"
	rerunGuidance = "Triaged: to be run again from the start. The preserved branch yoyodyne/task/abc holds the finished rename; cherry-pick its second commit rather than writing it again."
	// decidedRunID is the stoppage a decision stands about where the test is not
	// about which stoppage it was. The sequences that are about that name their
	// own run.
	decidedRunID = "run-11112222333344445555666677778888"
	// decidedIn and decidedOnTurn are where the development manager recorded the
	// decision. A re-run cites them, so they are what a test asserting the
	// attribution looks for.
	decidedIn     = "chat-0123456789abcdef"
	decidedOnTurn = 3
)

// triageDecided is one decision of the development manager's as the durable
// record holds it: the word, the stoppage it settles, the reasoning it was made
// on, and where it was recorded.
func triageDecided(decision, runID string) runstate.TriageDecision {
	return runstate.TriageDecision{
		Decision:     decision,
		RunID:        runID,
		Reason:       rerunReasoning,
		DecidedBy:    "development manager",
		Conversation: decidedIn,
		Turn:         decidedOnTurn,
	}
}

// startedRun is one call of the re-run action's starter: what it was asked to
// run, and the selection it was asked to run it under.
type startedRun struct {
	workItemID string
	selection  runstate.Selection
}

// rerunHarness is the durable state a re-run acts on, held together so a test
// can drive one decision without rebuilding four stores.
type rerunHarness struct {
	docket *memoryDocket
	runs   *runstate.Store
	intake *runstate.IntakeHoldStore
	reruns *runstate.RerunStore
	// holds is the operator's pause over everything the harness spends. A re-run
	// asked for by hand never reads it — the pipeline does — and a carry-out the
	// harness fires itself does, which is why it is built here beside the rest.
	holds *runstate.OperatorHoldStore
	// item is the work item as the tracker has it, which is what says whether a
	// fresh run may start on it, and itemErr a tracker that could not be asked.
	item    beads.WorkItem
	itemErr error
	// released is each note a released claim carried, and releaseErr a tracker
	// that refuses the release.
	released   []string
	releaseErr error
	started    []startedRun
	// outcome is what the starter reports, and failure what it returns. A test
	// that cares about what the action does after a run sets them.
	outcome Outcome
	failure error
	// retirement is what the fake retirer reports, and retired counts the times
	// it was asked. A harness that leaves it zero is one nothing should retire.
	retirement gitworktree.Retirement
	retired    int
	retireErr  error
	// closed is what the action asked the forge to close and deleted the remote
	// branches it asked to have removed, which together are what retiring the
	// stopped run's publication does.
	closed    []publish.CloseRequest
	closeErr  error
	deleted   []string
	deleteErr error
	// capacity is execution.max_concurrent_developers as the action reads it. It
	// leaves room for a run of something else, so only the sequences that are
	// about a full harness fill it.
	capacity int
}

func (h *rerunHarness) RetirePreserved(context.Context, gitworktree.Worktree, string) (gitworktree.Retirement, error) {
	h.retired++
	return h.retirement, h.retireErr
}

func (h *rerunHarness) DeleteRemoteBranch(_ context.Context, worktree gitworktree.Worktree, _ string) error {
	if h.deleteErr != nil {
		return h.deleteErr
	}
	h.deleted = append(h.deleted, worktree.Branch)
	return nil
}

func (h *rerunHarness) Close(_ context.Context, request publish.CloseRequest) (publish.Closure, error) {
	if h.closeErr != nil {
		return publish.Closure{}, h.closeErr
	}
	h.closed = append(h.closed, request)
	return publish.Closure{Closed: true, State: "CLOSED"}, nil
}

// Show is the tracker's answer about the item, read and never written. A harness
// leaves the item open; the sequences that are about the item's own state are the
// ones that move it.
func (h *rerunHarness) Show(context.Context, string) (beads.WorkItem, error) {
	return h.item, h.itemErr
}

// Release is the one write a re-run makes to the item: giving back a claim the
// stopped run left on it. Each note is kept so a test can read what the item was
// told, and releaseErr is a tracker that would not take the write.
func (h *rerunHarness) Release(_ context.Context, _ string, reason string) (beads.WorkItem, error) {
	if h.releaseErr != nil {
		return beads.WorkItem{}, h.releaseErr
	}
	h.released = append(h.released, reason)
	h.item.Status = "open"
	return h.item, nil
}

func (h *rerunHarness) rerunner() Rerunner {
	return Rerunner{
		Docket:    h.docket,
		Runs:      h.runs,
		Intake:    h.intake,
		Reruns:    h.reruns,
		Decisions: h.runs.Triage(),
		Items:     h,
		Gates:     h.runs,
		Capacity:  h.capacity,
		Preserved: h,
		// The forge the stopped run's publication is closed through. A stopped run
		// that published nothing never reaches it, which is every sequence here
		// that is not about a publication.
		Publications: h,
		Clock:        docketClock{},
		Start: func(_ context.Context, workItemID string, selection runstate.Selection) (Outcome, error) {
			h.started = append(h.started, startedRun{workItemID: workItemID, selection: selection})
			return h.outcome, h.failure
		},
	}
}

// newRerunHarness records one stopped run, dockets it, and leaves everything
// else as a fresh product: no hold, no re-run claimed, nothing in flight.
func newRerunHarness(t *testing.T, state runstate.State) *rerunHarness {
	t.Helper()
	harness := newDocketedHarness(t, state)
	// The decision itself: the development manager recorded a re-run of this
	// item's stopped run, which spent the item's re-run budget in the same write.
	// That record is what the action reads to know somebody decided this, and
	// what the reasoning it records is read from.
	recordRerunDecision(t, harness.runs, state.WorkItemID, state.RunID)
	return harness
}

// newDocketedHarness records one stopped run and dockets it, with nothing yet
// decided about it.
func newDocketedHarness(t *testing.T, state runstate.State) *rerunHarness {
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
	holds, err := runstate.NewOperatorHoldStore(root)
	if err != nil {
		t.Fatalf("runstate.NewOperatorHoldStore() error = %v", err)
	}
	docket := &memoryDocket{}
	if _, err := docketerOver(nil, docket).RecordStoppedRun(state); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	return &rerunHarness{
		docket: docket,
		runs:   runs,
		intake: intake,
		holds:  holds,
		reruns: runs.Reruns(),
		// The item has been put back to something a run may start on, which is
		// what a development manager deciding a re-run of a blocked item does
		// before the harness is asked to carry the decision out.
		item:     beads.WorkItem{ID: state.WorkItemID, Title: state.WorkItemTitle, Status: "open"},
		capacity: 2,
		outcome: Outcome{
			RunID:      "run-fedcba9876543210fedcba9876543210",
			WorkItemID: state.WorkItemID,
			Status:     runstate.StatusSucceeded,
		},
	}
}

// recordRerunDecision is what the development manager's triage does to the
// item's durable record when it decides a re-run of one stopped run: it writes
// the decision and spends the item's one re-run in a single write, before
// anything acts on either.
func recordRerunDecision(t *testing.T, runs *runstate.Store, workItemID, priorRunID string) {
	t.Helper()
	if _, err := runs.Triage().RecordRerun(context.Background(), workItemID, triageDecided(runstate.TriageDecisionRerun, priorRunID), docketedNow, rerunCaps); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}
}

// rerunCaps are the harness defaults the decision is recorded against: triage
// acts alone once per item, under the configured round cap.
var rerunCaps = runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 2}

// integrated is the fresh run landing its work, which is what retires what the
// stopped run preserved.
func (h *rerunHarness) integrated() {
	h.outcome.Integration = &gitworktree.Integration{
		TargetBranch: "main",
		SourceCommit: strings.Repeat("c", 40),
		TargetCommit: strings.Repeat("d", 40),
	}
	h.retirement = gitworktree.Retirement{
		Worktree: gitworktree.WorktreeRemoval{Path: "/state/worktrees/task", Removed: true},
		Branch:   gitworktree.Removal{Branch: "yoyodyne/task/abc", Removed: true},
	}
}

// rerunRequest names the stoppage and nothing else: what the fresh run records
// as why it exists is read from the decision, not carried here.
func rerunRequest() RerunRequest {
	return RerunRequest{Run: docketedRunID}
}

// The invariant's second half: work the harness chose accounts for itself. A
// re-run is the development manager choosing, so that is what the run records,
// in the words the decision was reasoned in.
func TestARerunRecordsTheTriageDecisionAsWhyTheRunExists(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	if len(harness.started) != 1 || harness.started[0].workItemID != docketedItem {
		t.Fatalf("started = %#v, want the docketed item run once", harness.started)
	}
	selection := harness.started[0].selection
	if selection.By != runstate.SelectedByDevelopmentManager {
		t.Fatalf("selected by = %q, want the development manager whose triage chose it", selection.By)
	}
	if !strings.Contains(selection.Reason, rerunReasoning) {
		t.Fatalf("reason = %q, want the reasoning the decision was recorded with", selection.Reason)
	}
	// The stoppage it settles is named as well as the reasoning: a reason that
	// only carried the argument would not say which stopped run it was about.
	if !strings.Contains(selection.Reason, docketedRunID) {
		t.Fatalf("reason = %q, want the stopped run it settles named", selection.Reason)
	}
	if selection.Reason != result.Reason {
		t.Fatalf("recorded reason %q is not what the result reports (%q)", selection.Reason, result.Reason)
	}
	// A selection the run state would refuse would be a reason nothing records.
	if err := selection.Validate(); err != nil {
		t.Fatalf("the recorded selection is not one a run may carry: %v", err)
	}
}

// The invariant's first half. The development manager naming an item is not the
// operator naming it, so the hold applies — and nothing is claimed under one,
// which is what leaves the stoppage its one re-run for afterwards.
func TestAHeldIntakeStartsNoRerunAndSpendsNothing(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	held, err := harness.intake.Hold(runstate.IntakeHolderOperator, "the queue is heading somewhere odd", docketedNow)
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err != nil {
		t.Fatalf("Rerun() error = %v, want a held intake reported rather than a failure", err)
	}
	if result.Started || len(harness.started) != 0 {
		t.Fatalf("started = %t / %#v, want nothing started under a hold", result.Started, harness.started)
	}
	if result.IntakeHeld == nil || !result.IntakeHeld.HeldAt.Equal(held.HeldAt) {
		t.Fatalf("intake held = %#v, want the hold that stopped it", result.IntakeHeld)
	}
	if _, claimed, err := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); err != nil || claimed {
		t.Fatalf("claimed = %t, error = %v, want the stoppage to keep its re-run", claimed, err)
	}
	// Released, the same decision is carried out: the hold delayed the re-run
	// rather than consuming it.
	if _, _, err := harness.intake.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if _, err := harness.rerunner().Rerun(context.Background(), rerunRequest()); err != nil {
		t.Fatalf("Rerun() after the hold was released error = %v", err)
	}
	if len(harness.started) != 1 {
		t.Fatalf("started = %#v, want the re-run to have run once the hold was lifted", harness.started)
	}
}

// Triage acts on one stoppage once. The bound is the docket entry rather than
// the item, because what a second re-run of one stoppage means is that nobody
// decided anything new.
func TestOneDocketedStoppageIsRerunOnce(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	if _, err := harness.rerunner().Rerun(context.Background(), rerunRequest()); err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	_, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if !errors.Is(err, runstate.ErrRerunTaken) {
		t.Fatalf("second Rerun() error = %v, want the entry's re-run to be spent", err)
	}
	if len(harness.started) != 1 {
		t.Fatalf("started = %#v, want exactly one run for one stoppage", harness.started)
	}
}

// One decision buys one re-run of the stoppage it was about, and the item's
// counter alone cannot say so: it is a total nothing clears, so a second
// stoppage of an already re-run item would pass it on the strength of a decision
// that was about the first stoppage. The decision names its own stoppage, so the
// second one is refused until somebody decides about that one — which past the
// cap is an escalation rather than a larger budget.
func TestASecondStoppageOfAnAlreadyRerunItemIsRefused(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	if _, err := harness.rerunner().Rerun(context.Background(), rerunRequest()); err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	// The fresh run stopped too, and its stoppage was docketed like any other.
	second := stoppedState()
	second.RunID = harness.outcome.RunID
	second.Blocker = "Yoyodyne stopped this item: the ground moved again."
	if err := harness.runs.Create(second); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := docketerOver(nil, harness.docket).RecordStoppedRun(second); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}

	_, err := harness.rerunner().Rerun(context.Background(), RerunRequest{Run: second.RunID})
	if err == nil || !strings.Contains(err.Error(), "no triage decision about the stoppage of run "+second.RunID) {
		t.Fatalf("Rerun() error = %v, want a refusal naming the stoppage nobody has decided about", err)
	}
	if len(harness.started) != 1 {
		t.Fatalf("started = %#v, want the item run again once in total", harness.started)
	}
	if _, claimed, _ := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, second.RunID)); claimed {
		t.Fatalf("the refused second stoppage was claimed anyway")
	}
	// Deciding about the second stoppage is what makes it actionable — and the
	// cap is what makes that a person's decision rather than this action's.
	if _, err := harness.runs.Triage().RecordRerun(context.Background(), second.WorkItemID, triageDecided(runstate.TriageDecisionRerun, decidedRunID), docketedNow, rerunCaps); err == nil {
		t.Fatal("RecordRerun() gave a second re-run of one item, which the cap refuses")
	}
}

// The architect's condition: the stoppage has to be over, and it is proved from
// the run's own record rather than assumed from the entry being on the docket.
// The docket says what was true when it was written.
func TestARerunIsRefusedWhileAnythingOfTheStoppedRunIsStillLive(t *testing.T) {
	t.Parallel()

	for _, refusal := range []struct {
		name  string
		state func(runstate.State) runstate.State
		want  string
	}{
		{
			// A run that was docketed and has since been picked up again is owed
			// the rest of its own step, and a re-run would be a second developer
			// on one item.
			name: "the stopped run is running again",
			state: func(state runstate.State) runstate.State {
				state.Status = runstate.StatusRunning
				state.CompletedAt = nil
				return state
			},
			want: "resumable",
		},
	} {
		t.Run(refusal.name, func(t *testing.T) {
			t.Parallel()

			// The docket entry is made from the stoppage as it was, and the run
			// record then moves on: that is exactly the disagreement this checks.
			harness := newRerunHarness(t, stoppedState())
			moved := refusal.state(stoppedState())
			if err := harness.runs.Save(moved); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			_, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
			if err == nil || !strings.Contains(err.Error(), refusal.want) {
				t.Fatalf("Rerun() error = %v, want a refusal naming %q", err, refusal.want)
			}
			// Both of these clear on their own, so what the refusal owes a reader
			// is that the decision is still there to carry out afterwards.
			if !strings.Contains(err.Error(), "keeps its re-run") {
				t.Fatalf("refusal %q does not say the stoppage keeps its re-run", err)
			}
			if len(harness.started) != 0 {
				t.Fatalf("started = %#v, want nothing started", harness.started)
			}
			if _, claimed, _ := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); claimed {
				t.Fatalf("a refused re-run spent the stoppage's claim")
			}
		})
	}
}

// Two live runs of one item cannot exist. The reservation refuses the second
// anyway; refusing here is what keeps the collision from costing the stoppage
// its only re-run.
func TestARerunIsRefusedWhileTheItemHasARunInFlight(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	inFlight := stoppedState()
	inFlight.RunID = "run-11112222333344445555666677778888"
	inFlight.Status = runstate.StatusRunning
	inFlight.CompletedAt = nil
	inFlight.Blocker = ""
	if err := harness.runs.Create(inFlight); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	_, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err == nil || !strings.Contains(err.Error(), inFlight.RunID) {
		t.Fatalf("Rerun() error = %v, want a refusal naming the run in flight", err)
	}
	if !strings.Contains(err.Error(), "keeps its re-run") {
		t.Fatalf("refusal %q does not say the stoppage keeps its re-run", err)
	}
	if len(harness.started) != 0 {
		t.Fatalf("started = %#v, want nothing started", harness.started)
	}
	if _, claimed, _ := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); claimed {
		t.Fatalf("a refused re-run spent the stoppage's claim")
	}
}

// A carry-out that meets a full harness waits: the development manager's
// decision does not expire because two developers happened to be busy at that
// second, so nothing is claimed, nothing fails, and the state is said plainly.
// Asking again once a slot frees carries out the same decision.
func TestARerunMeetingAFullHarnessWaitsAndSpendsNothing(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	harness.capacity = 1
	occupying := runningState("run-11112222333344445555666677778888", "yoyodyne-ifd.other")
	if err := harness.runs.Create(occupying); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err != nil {
		t.Fatalf("Rerun() error = %v, want a full harness waited on rather than failed", err)
	}
	if result.Started || len(harness.started) != 0 {
		t.Fatalf("started = %t / %#v, want nothing started with no slot free", result.Started, harness.started)
	}
	if result.CapacityFull == nil || result.CapacityFull.Active != 1 || result.CapacityFull.Limit != 1 {
		t.Fatalf("capacity = %#v, want what it is waiting on", result.CapacityFull)
	}
	if _, claimed, err := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); err != nil || claimed {
		t.Fatalf("claimed = %t, error = %v, want the stoppage to keep its re-run", claimed, err)
	}
	// What it is waiting on is what a reader has to be told, and that the
	// authorization is still there to be carried out.
	rendered := result.Render()
	for _, want := range []string{"limit 1", "keeps its one re-run", "decision still stands"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered = %q, is missing %q", rendered, want)
		}
	}

	// The other run ends, and the same decision is carried out: the full harness
	// delayed the re-run rather than consuming it.
	occupying.Status = runstate.StatusSucceeded
	completed := docketedNow
	occupying.CompletedAt = &completed
	if err := harness.runs.Save(occupying); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := harness.rerunner().Rerun(context.Background(), rerunRequest()); err != nil {
		t.Fatalf("Rerun() once a slot freed error = %v", err)
	}
	if len(harness.started) != 1 {
		t.Fatalf("started = %#v, want the re-run to have run once a developer was free", harness.started)
	}
}

// The slot can go to another run between the reading and the reservation, and a
// claim taken for a run the reservation then refused is a re-run spent on a run
// that never existed. It is given back, and the carry-out reports the waiting
// state it would have reported a moment earlier.
func TestAReservationRefusedForCapacityGivesTheClaimBack(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	harness.failure = fmt.Errorf("reserve developer run: %w", runstate.CapacityError{Limit: 2, Active: 2})
	harness.outcome = Outcome{}
	result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err != nil {
		t.Fatalf("Rerun() error = %v, want the raced slot waited on rather than failed", err)
	}
	if result.Started {
		t.Fatalf("result = %#v, want a run that was never reserved reported as not started", result)
	}
	if result.CapacityFull == nil || result.CapacityFull.Active != 2 {
		t.Fatalf("capacity = %#v, want what the reservation refused it for", result.CapacityFull)
	}
	if result.RecordProblem != "" {
		t.Fatalf("record problem = %q, want the claim given back cleanly", result.RecordProblem)
	}
	if _, claimed, err := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); err != nil || claimed {
		t.Fatalf("claimed = %t, error = %v, want the claim given back", claimed, err)
	}
	// The decision survives the race: asking again once a developer is free
	// carries out the same one rather than meeting the once-only guard.
	harness.failure = nil
	harness.outcome = Outcome{RunID: "run-fedcba9876543210fedcba9876543210", WorkItemID: docketedItem, Status: runstate.StatusSucceeded}
	if _, err := harness.rerunner().Rerun(context.Background(), rerunRequest()); err != nil {
		t.Fatalf("Rerun() after the race error = %v", err)
	}
	if len(harness.started) != 2 {
		t.Fatalf("starts = %d, want the refused attempt and the one that ran", len(harness.started))
	}
}

// A claim that could not be given back is said out loud: the stoppage has then
// paid for a wait that was meant to cost it nothing, and only somebody looking
// can give it back.
func TestAClaimThatCouldNotBeGivenBackIsReported(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	harness.failure = fmt.Errorf("reserve developer run: %w", runstate.CapacityError{Limit: 2, Active: 2})
	harness.outcome = Outcome{}
	rerunner := harness.rerunner()
	rerunner.Reruns = unwithdrawableReruns{RerunRecords: harness.reruns}
	result, err := rerunner.Rerun(context.Background(), rerunRequest())
	if err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	if result.CapacityFull == nil {
		t.Fatalf("result = %#v, want the full harness still reported", result)
	}
	if !strings.Contains(result.RecordProblem, "could not be given back") {
		t.Fatalf("record problem = %q, want the spent claim named", result.RecordProblem)
	}
}

// The contradiction the first exercised re-run met on yoyodyne-ifd.125.1, in the
// shape anything refused past the claim leaves: a refusal made before the fresh
// run was reserved recorded no run and spent the stoppage's one re-run anyway, so
// the next attempt was refused as already re-run for a run that had never
// happened. Nothing was reserved, so the claim goes back and the same decision is
// carried out by asking again.
func TestARerunRefusedBeforeItWasReservedGivesTheClaimBackAndCanBeAskedAgain(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	harness.failure = errors.New("repository is not ready for an isolated run: the checkout has uncommitted changes")
	harness.outcome = Outcome{}
	result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("Rerun() error = %v, want the refusal reported", err)
	}
	// The refusal is only free if it says so, because what a reader decides next
	// turns on whether asking again is worth anything.
	if !strings.Contains(err.Error(), "keeps its re-run") {
		t.Fatalf("refusal %q does not say the stoppage keeps its re-run", err)
	}
	if result.Started {
		t.Fatalf("result = %#v, want a run that was never reserved reported as not started", result)
	}
	if result.RecordProblem != "" {
		t.Fatalf("record problem = %q, want the claim given back cleanly", result.RecordProblem)
	}
	if _, claimed, err := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); err != nil || claimed {
		t.Fatalf("claimed = %t, error = %v, want the claim given back", claimed, err)
	}

	// And the decision survives it: the second attempt carries out the same one
	// rather than meeting the once-only guard for a run nobody made.
	harness.failure = nil
	harness.outcome = Outcome{RunID: "run-fedcba9876543210fedcba9876543210", WorkItemID: docketedItem, Status: runstate.StatusSucceeded}
	if _, err := harness.rerunner().Rerun(context.Background(), rerunRequest()); err != nil {
		t.Fatalf("Rerun() after the refusal error = %v, want the same decision carried out", err)
	}
	if len(harness.started) != 2 {
		t.Fatalf("starts = %d, want the refused attempt and the one that ran", len(harness.started))
	}
}

// A run that was reserved and then failed is settled like any other run that
// ended badly: the claim stands, because something happened on it. The run its
// outcome names is what says so.
func TestARunThatFailedAfterItWasReservedKeepsItsClaim(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	harness.failure = errors.New("the worktree could not be created")
	_, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err == nil || !strings.Contains(err.Error(), "worktree could not be created") {
		t.Fatalf("Rerun() error = %v, want the failure reported", err)
	}
	if _, claimed, _ := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); !claimed {
		t.Fatal("a re-run whose fresh run was reserved and then failed gave its claim back")
	}
}

// A claim that could not be given back after a refusal is said out loud in the
// refusal itself, because that is the only thing this path reports: the stoppage
// has spent its re-run on a run that never started, and only somebody looking can
// give it back.
func TestAClaimThatCouldNotBeGivenBackAfterARefusalIsReported(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	harness.failure = errors.New("repository is not ready for an isolated run: the checkout has uncommitted changes")
	harness.outcome = Outcome{}
	rerunner := harness.rerunner()
	rerunner.Reruns = unwithdrawableReruns{RerunRecords: harness.reruns}
	result, err := rerunner.Rerun(context.Background(), rerunRequest())
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("Rerun() error = %v, want the refusal still reported", err)
	}
	if !strings.Contains(err.Error(), "could not be given back") {
		t.Fatalf("Rerun() error = %v, want the spent claim named in the refusal", err)
	}
	if !strings.Contains(result.RecordProblem, "could not be given back") {
		t.Fatalf("record problem = %q, want the spent claim named", result.RecordProblem)
	}
}

// runningState is a run of something else that is in flight, which is what
// occupies a developer slot.
func runningState(runID, workItemID string) runstate.State {
	state := stoppedState()
	state.RunID = runID
	state.WorkItemID = workItemID
	state.Status = runstate.StatusRunning
	state.CompletedAt = nil
	state.Blocker = ""
	return state
}

// unwithdrawableReruns claims exactly as the store does and cannot give a claim
// back.
type unwithdrawableReruns struct {
	RerunRecords
}

func (unwithdrawableReruns) Withdraw(context.Context, string) error {
	return errors.New("the record is unwritable")
}

// The item a fresh run would start on is read before anything is claimed. A run
// that stopped on a durable blocker blocked its item, so this is the ordinary
// state of a docketed stoppage — and a refusal here that spent the stoppage's one
// re-run would make the decision self-defeating on exactly the items it is for.
func TestARerunOfAnItemNoRunCanStartOnIsRefusedAndSpendsNothing(t *testing.T) {
	t.Parallel()

	for _, refusal := range []struct {
		name string
		item beads.WorkItem
		want string
	}{
		{
			// Blocked and a claim the stopped run left are deliberately not here on
			// their own: stopping a run blocks the item or leaves it claimed, and
			// refusing the re-run for either was a decision spent on the field rather
			// than on anything the item was actually waiting for — the 102.5 seam for
			// the one, and 429.3's four refusals for the other. A claimed item still
			// waiting on unfinished work is refused for the work.
			name: "a claimed item is still waiting on unfinished work",
			item: beads.WorkItem{
				ID:           docketedItem,
				Status:       "in_progress",
				Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.102.5", Type: "blocks", Status: "open"}},
			},
			want: "blocked by: yoyodyne-ifd.102.5",
		},
		{
			name: "the item is closed",
			item: beads.WorkItem{ID: docketedItem, Status: "closed"},
			want: `status is "closed", want open or blocked`,
		},
		{
			name: "something the item depends on is not done",
			item: beads.WorkItem{
				ID:           docketedItem,
				Status:       "open",
				Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.102.5", Type: "blocks", Status: "open"}},
			},
			want: "blocked by: yoyodyne-ifd.102.5",
		},
		{
			// And the same reading of a blocked one: what refuses it is the
			// unfinished work it waits for, which is a fact about the dependency
			// graph rather than about the status field.
			name: "a blocked item is still waiting on unfinished work",
			item: beads.WorkItem{
				ID:           docketedItem,
				Status:       "blocked",
				Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.102.5", Type: "blocks", Status: "open"}},
			},
			want: "blocked by: yoyodyne-ifd.102.5",
		},
	} {
		t.Run(refusal.name, func(t *testing.T) {
			t.Parallel()

			harness := newRerunHarness(t, stoppedState())
			harness.item = refusal.item
			_, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
			if err == nil || !strings.Contains(err.Error(), refusal.want) {
				t.Fatalf("Rerun() error = %v, want a refusal naming %q", err, refusal.want)
			}
			// The refusal is only free if it says what would make it stop refusing.
			if !strings.Contains(err.Error(), "keeps its re-run") {
				t.Fatalf("refusal %q does not say the stoppage keeps its re-run", err)
			}
			if len(harness.started) != 0 {
				t.Fatalf("started = %#v, want nothing started", harness.started)
			}
			if _, claimed, _ := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); claimed {
				t.Fatalf("a re-run refused on the item's own state spent the stoppage's claim")
			}
			if len(harness.released) != 0 {
				t.Fatalf("released = %q, want a refused re-run to leave the item as it found it", harness.released)
			}
		})
	}
}

// The shape yoyodyne-ifd.429.3 was stood off in four times: the stopped run is
// terminal, its branch survives so the claim audit leaves its claim standing,
// and the item still reads in_progress. The claim has nothing working behind it,
// so the carry-out gives it back with a note saying what moved it and why — as
// the pipeline's claim does for a stale blocked status — and the decision fires.
func TestARerunSupersedesTheClaimATerminalStoppedRunLeft(t *testing.T) {
	t.Parallel()

	var pipelined *pipelinedRerun
	pipelined = newPipelinedRerun(t, func() {
		// The release has landed by the time the fresh run starts, which is what
		// the pipeline's own start reads the item for.
		if pipelined.tracker.Item.Status != "open" {
			t.Errorf("item status = %q when the fresh run starts, want the stale claim given back", pipelined.tracker.Item.Status)
		}
	})
	pipelined.tracker.Item.Status = "in_progress"
	pipelined.tracker.Claimed = true

	result, err := pipelined.rerunner.Rerun(context.Background(), RerunRequest{Run: priorRunID})
	if err != nil {
		t.Fatalf("Rerun() error = %v, want the recorded decision carried out", err)
	}
	if !result.Started || result.Outcome.Integration == nil {
		t.Fatalf("result = %#v, want the fresh run started and integrated", result)
	}
	if !pipelined.tracker.Released {
		t.Fatalf("the claim the stopped run left was never given back")
	}
	// What moved the status and why is on the item, in the note the release carried.
	for _, want := range []string{"released this item's in_progress claim", priorRunID, "no run of this item is in flight", decidedIn} {
		if !strings.Contains(pipelined.tracker.ReleaseReason, want) {
			t.Fatalf("release note = %q, want it to say %q", pipelined.tracker.ReleaseReason, want)
		}
	}
	if result.SupersededClaim != pipelined.tracker.ReleaseReason {
		t.Fatalf("result says the note was %q, the item was given %q", result.SupersededClaim, pipelined.tracker.ReleaseReason)
	}
	// Given back before the fresh run claimed it, and claimed by that run again.
	release, claim := slices.Index(pipelined.tracker.Calls, "release"), slices.Index(pipelined.tracker.Calls, "claim")
	if release < 0 || claim < 0 || release > claim {
		t.Fatalf("tracker calls = %v, want the stale claim released before the fresh run's claim", pipelined.tracker.Calls)
	}
	if !strings.Contains(result.Render(), "was released first") {
		t.Fatalf("render = %q, want the release said", result.Render())
	}
}

// A claim a live run holds is not stale, whichever run it is, and giving it back
// would put two developers on one piece of work. It is refused before anything
// is written or spent.
func TestARerunLeavesAClaimALiveRunHolds(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	harness.item.Status = "in_progress"
	live := runningState("run-00001111222233334444555566667777", docketedItem)
	if err := harness.runs.Create(live); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	_, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err == nil || !strings.Contains(err.Error(), live.RunID) {
		t.Fatalf("Rerun() error = %v, want a refusal naming the live run %s", err, live.RunID)
	}
	if len(harness.released) != 0 {
		t.Fatalf("released = %q, want a live run's claim left where it is", harness.released)
	}
	if len(harness.started) != 0 {
		t.Fatalf("started = %#v, want nothing started", harness.started)
	}
	if _, claimed, _ := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); claimed {
		t.Fatalf("a re-run refused for a live claim spent the stoppage's claim")
	}
}

// The supersession asks again, where it writes, whether anything holds the item:
// a run reserved while the readings before it were being made is the claim's
// live holder, and is refused exactly as the reading before it refuses.
func TestSupersedingAStaleClaimAsksAgainWhetherARunHoldsIt(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	live := runningState("run-00001111222233334444555566667777", docketedItem)
	if err := harness.runs.Create(live); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	prior, err := harness.runs.Load(docketedRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	entry, err := harness.rerunner().entry(docketedRunID)
	if err != nil {
		t.Fatalf("entry() error = %v", err)
	}
	if _, err := harness.rerunner().supersedeStaleClaim(context.Background(), entry, prior, triageDecided(runstate.TriageDecisionRerun, docketedRunID)); err == nil || !strings.Contains(err.Error(), live.RunID) {
		t.Fatalf("supersedeStaleClaim() error = %v, want a refusal naming the live run", err)
	}
	if len(harness.released) != 0 {
		t.Fatalf("released = %q, want a live run's claim left where it is", harness.released)
	}
}

// A tracker that will not take the release leaves the claim standing and the
// stoppage its re-run: nothing was started on an item still claimed.
func TestARerunWhoseStaleClaimCannotBeGivenBackSpendsNothing(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	harness.item.Status = "in_progress"
	harness.releaseErr = errors.New("bd update failed")
	_, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err == nil || !strings.Contains(err.Error(), "bd update failed") || !strings.Contains(err.Error(), "keeps its re-run") {
		t.Fatalf("Rerun() error = %v, want the failed release named and the re-run kept", err)
	}
	if len(harness.started) != 0 {
		t.Fatalf("started = %#v, want nothing started", harness.started)
	}
	if _, claimed, _ := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); claimed {
		t.Fatalf("a re-run whose stale claim stood spent the stoppage's claim")
	}
}

// A re-run claim refused after the stale item claim was given back says so: the
// item reads open with a note promising a fresh run, and the refusal is where the
// reader learns none followed.
func TestARerunClaimRefusedAfterTheStaleClaimWasReleasedSaysSo(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	harness.item.Status = "in_progress"
	rerunner := harness.rerunner()
	rerunner.Reruns = refusingRerunClaims{RerunRecords: harness.reruns, err: errors.New("claimed by another carry-out")}
	_, err := rerunner.Rerun(context.Background(), rerunRequest())
	if err == nil || !strings.Contains(err.Error(), "claimed by another carry-out") || !strings.Contains(err.Error(), "had already been released") {
		t.Fatalf("Rerun() error = %v, want the refused claim named and the release said", err)
	}
	if len(harness.released) != 1 {
		t.Fatalf("released = %q, want the stale claim given back once", harness.released)
	}
	if len(harness.started) != 0 {
		t.Fatalf("started = %#v, want nothing started", harness.started)
	}
}

// refusingRerunClaims is a re-run record whose Claim is refused, as it is when a
// concurrent carry-out of the same stoppage took it first.
type refusingRerunClaims struct {
	RerunRecords
	err error
}

func (r refusingRerunClaims) Claim(context.Context, runstate.Rerun) (runstate.Rerun, error) {
	return runstate.Rerun{}, r.err
}

// A step only a person can take refuses a re-run before the claim, exactly as
// it refuses the scheduler's pull. A re-run is the harness choosing work, and the
// development manager deciding one is not the operator taking the step the item
// reserved for them: the docketed stoppage of the flip in the 209.6/209.7 shape,
// re-run on her decision, would otherwise start with the operator's soak reading
// still untaken — the same jump, arriving through triage instead of the tracker.
func TestARerunOfAGatedItemIsRefusedUntilThePersonRecordsTheAct(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	harness.item.Description = humangate.DeclareMarker + " soak-reviewed — the operator has read a week of soak runs and is content to flip\n"
	_, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err == nil {
		t.Fatal("Rerun() started an item whose gate nobody has passed")
	}
	for _, want := range []string{"waiting on a person", "soak-reviewed", "keeps its re-run", "yoyo gate record <name> --for " + docketedItem} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not say %q", err, want)
		}
	}
	if len(harness.started) != 0 {
		t.Fatalf("started = %#v, want nothing started past the gate", harness.started)
	}
	if _, claimed, _ := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); claimed {
		t.Fatal("a re-run refused on a human gate spent the stoppage's claim")
	}

	// An act recorded against another item that happens to reuse the name passes
	// nothing here: the gate is the item's own.
	if err := harness.runs.RecordHumanAct(humanAct("yoyodyne-ifd.209.6", "soak-reviewed")); err != nil {
		t.Fatalf("RecordHumanAct() error = %v", err)
	}
	if _, err := harness.rerunner().Rerun(context.Background(), rerunRequest()); err == nil || !strings.Contains(err.Error(), "waiting on a person") {
		t.Fatalf("Rerun() error = %v, want the gate still holding after another item's act", err)
	}

	// The operator records the act against this item, and the same decision is
	// carried out.
	if err := harness.runs.RecordHumanAct(humanAct(docketedItem, "soak-reviewed")); err != nil {
		t.Fatalf("RecordHumanAct() error = %v", err)
	}
	result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err != nil {
		t.Fatalf("Rerun() after the act error = %v", err)
	}
	if !result.Started || len(harness.started) != 1 {
		t.Fatalf("result = %#v, started = %#v, want the decision carried out once the act is on the record", result, harness.started)
	}
}

// humanAct is one recorded step, against the subject that declared the gate.
func humanAct(subject, gate string) runstate.HumanAct {
	return runstate.HumanAct{
		SchemaVersion: runstate.HumanActSchemaVersion,
		ProductID:     "yoyodyne",
		Subject:       subject,
		Gate:          gate,
		Person:        "mason",
		Statement:     "read the week of soak runs and is content to flip",
		RecordedAt:    docketedNow,
	}
}

// A tracker that could not be asked refuses the re-run rather than claiming
// through the silence: an item nobody could read is not an item somebody proved
// a run may start on.
func TestARerunIsRefusedWhenTheItemCannotBeRead(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	harness.itemErr = errors.New("bd show failed")
	_, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err == nil || !strings.Contains(err.Error(), "bd show failed") {
		t.Fatalf("Rerun() error = %v, want a refusal naming what could not be read", err)
	}
	if _, claimed, _ := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); claimed {
		t.Fatalf("a re-run refused for an unreadable item spent the stoppage's claim")
	}
}

// A run nothing docketed is re-run on the development manager's decision, and
// its one re-run is counted against the key the docket would have given it,
// because a run the harness cancelled on its way out is never docketed and her
// decision about one used to be refused at every pass (yoyodyne-ifd.428.52). A
// run the harness holds no record of is still refused, naming the docket.
func TestARerunOfARunNothingDocketedStartsAndOneWithNoRecordIsRefused(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	harness.docket.entries = nil
	if _, err := harness.rerunner().Rerun(context.Background(), rerunRequest()); err != nil {
		t.Fatalf("Rerun() error = %v, want the recorded run re-run", err)
	}
	if len(harness.started) != 1 {
		t.Fatalf("started = %#v, want one fresh run", harness.started)
	}
	if _, claimed, _ := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); !claimed {
		t.Fatalf("want the re-run claimed under the run's stopped-run key")
	}

	unrecorded := newRerunHarness(t, stoppedState())
	unrecorded.docket.entries = nil
	_, err := unrecorded.rerunner().Rerun(context.Background(), RerunRequest{Run: "run-44444444444444444444444444444444"})
	if err == nil || !strings.Contains(err.Error(), "triage docket") {
		t.Fatalf("Rerun() error = %v, want a refusal naming the docket", err)
	}
	if len(unrecorded.started) != 0 {
		t.Fatalf("started = %#v, want nothing started", unrecorded.started)
	}
}

// What the stopped run preserved is kept while the fresh run has not landed,
// and retired once it has. Both are recorded, because an artifact whose
// disposition nobody wrote down is the orphan nobody discovers.
func TestThePreservedArtifactsAreKeptUntilTheFreshRunIntegratesAndThenRetired(t *testing.T) {
	t.Parallel()

	t.Run("kept while nothing has landed", func(t *testing.T) {
		t.Parallel()

		harness := newRerunHarness(t, stoppedState())
		result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
		if err != nil {
			t.Fatalf("Rerun() error = %v", err)
		}
		if result.Preserved.Disposition != runstate.PreservedKept {
			t.Fatalf("disposition = %q, want the artifacts kept", result.Preserved.Disposition)
		}
		if harness.retired != 0 {
			t.Fatalf("retirements = %d, want nothing retired before the work landed", harness.retired)
		}
		recorded, found, err := harness.reruns.Find(result.DocketKey)
		if err != nil || !found {
			t.Fatalf("Find() = %t, error = %v", found, err)
		}
		if recorded.Preserved.Branch != "yoyodyne/task/abc" || recorded.Preserved.WorktreePath != "/state/worktrees/task" {
			t.Fatalf("recorded artifacts = %#v, want the two the stopped run preserved", recorded.Preserved)
		}
		if recorded.RunID != harness.outcome.RunID {
			t.Fatalf("recorded run = %q, want the fresh run it started", recorded.RunID)
		}
	})

	t.Run("retired once the work has landed", func(t *testing.T) {
		t.Parallel()

		harness := newRerunHarness(t, stoppedState())
		harness.integrated()
		result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
		if err != nil {
			t.Fatalf("Rerun() error = %v", err)
		}
		if harness.retired != 1 {
			t.Fatalf("retirements = %d, want the preserved artifacts retired once", harness.retired)
		}
		if result.Preserved.Disposition != runstate.PreservedRetired || result.Preserved.RetiredAt == nil {
			t.Fatalf("disposition = %#v, want a dated retirement", result.Preserved)
		}
		recorded, _, err := harness.reruns.Find(result.DocketKey)
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		if recorded.Preserved.Disposition != runstate.PreservedRetired {
			t.Fatalf("recorded disposition = %q, want the retirement durable", recorded.Preserved.Disposition)
		}
		// The stopped run's own record is what everything else in the harness
		// reads to find out whether those artifacts are still there, so the
		// removal is written onto it as well.
		stopped, err := harness.runs.Load(docketedRunID)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if !stopped.WorktreeRemoved || !stopped.BranchRemoved {
			t.Fatalf("run %s still advertises what was retired: worktree removed = %t, branch removed = %t (%s)",
				stopped.RunID, stopped.WorktreeRemoved, stopped.BranchRemoved, result.RecordProblem)
		}
		// The removal is evidence rather than an assertion: a stopped run
		// promoted nothing, so what earned it is the run that superseded it.
		if stopped.ArtifactsRetiredBy != harness.outcome.RunID {
			t.Fatalf("retired by = %q, want the fresh run that superseded it", stopped.ArtifactsRetiredBy)
		}
		if result.RecordProblem != "" {
			t.Fatalf("record problem = %q, want every record written", result.RecordProblem)
		}
	})

	t.Run("says so when the removal could not be written onto the stopped run", func(t *testing.T) {
		t.Parallel()

		harness := newRerunHarness(t, stoppedState())
		harness.integrated()
		rerunner := harness.rerunner()
		rerunner.Runs = unwritableRuns{RerunRuns: harness.runs}
		result, err := rerunner.Rerun(context.Background(), rerunRequest())
		if err != nil {
			t.Fatalf("Rerun() error = %v, want the run to stand and the record named", err)
		}
		// The artifacts really are gone, so the re-run's own record says retired.
		if result.Preserved.Disposition != runstate.PreservedRetired {
			t.Fatalf("disposition = %q, want the retirement that happened", result.Preserved.Disposition)
		}
		if !strings.Contains(result.RecordProblem, "still says otherwise") {
			t.Fatalf("record problem = %q, want the stale run record named", result.RecordProblem)
		}
	})

	t.Run("retires nothing while somebody else owns the stopped run", func(t *testing.T) {
		t.Parallel()

		harness := newRerunHarness(t, stoppedState())
		harness.integrated()
		// Another process is acting on the stopped run. Removing its artifacts
		// without being able to record the removal is the stale record this
		// refuses to create.
		_, lease, err := harness.runs.AdoptRun(context.Background(), docketedRunID)
		if err != nil {
			t.Fatalf("AdoptRun() error = %v", err)
		}
		defer lease.Release()

		result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
		if err != nil {
			t.Fatalf("Rerun() error = %v", err)
		}
		if harness.retired != 0 {
			t.Fatalf("retirements = %d, want nothing removed while another owner holds the run", harness.retired)
		}
		if result.Preserved.Disposition != runstate.PreservedKept {
			t.Fatalf("disposition = %q, want the artifacts kept", result.Preserved.Disposition)
		}
		if !strings.Contains(result.Preserved.Problem, "could not be taken") {
			t.Fatalf("problem = %q, want why nothing was retired", result.Preserved.Problem)
		}
	})

	t.Run("names only what survived a retirement that failed part way", func(t *testing.T) {
		t.Parallel()

		harness := newRerunHarness(t, stoppedState())
		harness.integrated()
		// The worktree went and the branch deletion then failed, which is what a
		// run with no recorded target branch does to a retirement.
		harness.retirement = gitworktree.Retirement{
			Worktree: gitworktree.WorktreeRemoval{Path: "/state/worktrees/task", Removed: true},
		}
		harness.retireErr = errors.New("integration target branch is required")
		result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
		if err != nil {
			t.Fatalf("Rerun() error = %v, want a retirement problem recorded rather than a failed re-run", err)
		}
		if result.Preserved.Disposition != runstate.PreservedKept {
			t.Fatalf("disposition = %q, want the branch that survived reported as kept", result.Preserved.Disposition)
		}
		if result.Preserved.WorktreePath != "" {
			t.Fatalf("preserved worktree = %q, want the one that was removed left unnamed", result.Preserved.WorktreePath)
		}
		if !strings.Contains(result.Preserved.Problem, "integration target branch is required") {
			t.Fatalf("problem = %q, want what stopped the retirement", result.Preserved.Problem)
		}
	})

	t.Run("kept with the reason when it could not be retired", func(t *testing.T) {
		t.Parallel()

		harness := newRerunHarness(t, stoppedState())
		harness.integrated()
		harness.retirement = gitworktree.Retirement{
			Worktree: gitworktree.WorktreeRemoval{Path: "/state/worktrees/task", Removed: true},
			Branch: gitworktree.Removal{
				Branch: "yoyodyne/task/abc",
				Kept:   "yoyodyne/task/abc is not contained in main, so it still carries work nothing has promoted",
			},
		}
		result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
		if err != nil {
			t.Fatalf("Rerun() error = %v", err)
		}
		if result.Preserved.Disposition != runstate.PreservedKept {
			t.Fatalf("disposition = %q, want what survived reported as kept", result.Preserved.Disposition)
		}
		if !strings.Contains(result.Preserved.Problem, "not contained in main") {
			t.Fatalf("problem = %q, want why the branch was kept", result.Preserved.Problem)
		}
		// The worktree did go, and a reader must not be sent after it.
		if result.Preserved.WorktreePath != "" || result.Preserved.Branch == "" {
			t.Fatalf("preserved = %#v, want only what actually survived", result.Preserved)
		}
	})
}

// The whole action over the real pipeline: the guidance the development manager
// recorded on the item reaches the developer that runs it again, and the run
// that reaches the durable record says the development manager chose it and why.
func TestARerunHandsTheDevelopmentManagersGuidanceToTheDeveloper(t *testing.T) {
	t.Parallel()

	pipelined := newPipelinedRerun(t, nil)
	result, err := pipelined.rerunner.Rerun(context.Background(), RerunRequest{Run: priorRunID})
	if err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	if result.Outcome.Integration == nil {
		t.Fatalf("the fresh run did not integrate: %#v", result.Outcome)
	}
	developer := pipelined.provider.RequestsForRole(domain.RoleDeveloper)
	if len(developer) != 1 {
		t.Fatalf("developer invocations = %d, want the fresh attempt", len(developer))
	}
	if !strings.Contains(developer[0].Prompt, rerunGuidance) {
		t.Fatalf("the developer was not given the triage guidance:\n%s", developer[0].Prompt)
	}
	fresh, err := pipelined.runs.Load(result.Outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if fresh.Selection == nil || fresh.Selection.By != runstate.SelectedByDevelopmentManager {
		t.Fatalf("recorded selection = %#v, want the development manager's", fresh.Selection)
	}
	if !strings.Contains(fresh.Selection.Reason, rerunReasoning) {
		t.Fatalf("recorded reason = %q, want the triage reasoning", fresh.Selection.Reason)
	}
	if fresh.Selection.At.IsZero() {
		t.Fatalf("recorded selection carries no moment: %#v", fresh.Selection)
	}
}

// A hold the operator places after the claim is taken still stops the run. That
// second reading is the enforcement — the action's own reading before the claim
// only keeps a held harness from spending the stoppage's re-run on a start that
// would decline — and it is the pipeline that makes it, on the same hold record
// the action read.
//
// Nothing was reserved behind it, so the claim taken for the run comes back: a
// hold arriving a moment later must cost the stoppage exactly what a hold read a
// moment earlier costs it, which is nothing.
func TestAHoldArrivingAfterTheClaimStillStopsTheFreshRun(t *testing.T) {
	t.Parallel()

	var pipelined *pipelinedRerun
	pipelined = newPipelinedRerun(t, func() {
		// The operator holds intake while the claim is being taken, which is the
		// window the action itself cannot cover.
		if _, err := pipelined.intake.Hold(runstate.IntakeHolderOperator, "stop choosing while I reorder the queue", docketedNow); err != nil {
			t.Fatalf("Hold() error = %v", err)
		}
	})
	result, err := pipelined.rerunner.Rerun(context.Background(), RerunRequest{Run: priorRunID})
	if err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	// The run declined to claim anything, so the carry-out reports the hold in
	// place of a run rather than a run that is not there.
	if result.Started || result.PausedBeforeStarting == nil || result.PausedBeforeStarting.PausedByIntake == nil {
		t.Fatalf("result = %#v, want the fresh run held by intake and nothing started", result)
	}
	if result.PausedBeforeStarting.RunID != "" || result.PausedBeforeStarting.Integration != nil {
		t.Fatalf("outcome = %#v, want nothing reserved and nothing integrated", result.PausedBeforeStarting)
	}
	if invocations := len(pipelined.provider.RequestsForRole(domain.RoleDeveloper)); invocations != 0 {
		t.Fatalf("developer invocations = %d, want none under a hold", invocations)
	}
	if _, claimed, err := pipelined.reruns.Find(triage.Key(triage.ClassStoppedRun, priorRunID)); err != nil || claimed {
		t.Fatalf("claimed = %t, error = %v, want the claim given back", claimed, err)
	}
}

// The two doors a re-run's claim was spent through with nothing behind it: the
// operator's hold on all activity, and a directive nobody has settled. Both are
// read by the pipeline where it would start the fresh run — which is past the
// claim — and both stop it before a run is reserved, the item is claimed, or an
// agent is invoked. So both give the claim back, and the same decision is carried
// out once the pause lifts rather than being refused as already re-run for a run
// that never happened.
func TestAPauseMetWhereTheFreshRunWouldStartGivesTheClaimBack(t *testing.T) {
	t.Parallel()

	for _, door := range []struct {
		name string
		// pause is what arrives while the claim is being taken, and lift is what
		// settles it so the same decision can be carried out.
		pause func(t *testing.T, pipelined *pipelinedRerun)
		lift  func(t *testing.T, pipelined *pipelinedRerun)
		// met is what the carry-out has to name as what stopped it.
		met func(outcome Outcome) bool
	}{
		{
			name: "the operator paused all harness activity",
			pause: func(t *testing.T, pipelined *pipelinedRerun) {
				if _, err := pipelined.holds.Hold(docketedNow); err != nil {
					t.Fatalf("Hold() error = %v", err)
				}
			},
			lift: func(t *testing.T, pipelined *pipelinedRerun) {
				if _, _, err := pipelined.holds.Release(); err != nil {
					t.Fatalf("Release() error = %v", err)
				}
			},
			met: func(outcome Outcome) bool { return outcome.PausedByOperator != nil },
		},
		{
			name: "a directive about the work is unresolved",
			pause: func(t *testing.T, pipelined *pipelinedRerun) {
				pausingDirective(t, pipelined.directives, directive.KindArtifact, nil)
			},
			lift: func(t *testing.T, pipelined *pipelinedRerun) {
				recorded, err := pipelined.directives.List()
				if err != nil || len(recorded) != 1 {
					t.Fatalf("List() = %d directives, error = %v, want the one that paused it", len(recorded), err)
				}
				if _, err := pipelined.directives.Resolve(recorded[0].ID, "the goal still covers this work", docketedNow); err != nil {
					t.Fatalf("Resolve() error = %v", err)
				}
			},
			met: func(outcome Outcome) bool { return outcome.PausedByDirective != nil },
		},
	} {
		t.Run(door.name, func(t *testing.T) {
			t.Parallel()

			var pipelined *pipelinedRerun
			paused := false
			pipelined = newPipelinedRerun(t, func() {
				// It arrives while the carry-out is committing to the work, which is the
				// window no reading before the claim can cover. It happens once: the
				// second attempt is the one that finds the pause lifted.
				if paused {
					return
				}
				paused = true
				door.pause(t, pipelined)
			})
			result, err := pipelined.rerunner.Rerun(context.Background(), RerunRequest{Run: priorRunID})
			if err != nil {
				t.Fatalf("Rerun() error = %v, want the pause reported rather than a failure", err)
			}
			if result.Started || result.PausedBeforeStarting == nil {
				t.Fatalf("result = %#v, want the pause reported and nothing started", result)
			}
			if !door.met(*result.PausedBeforeStarting) {
				t.Fatalf("outcome = %#v, want it to name what paused it", result.PausedBeforeStarting)
			}
			if result.PausedBeforeStarting.RunID != "" || result.PausedBeforeStarting.Integration != nil {
				t.Fatalf("outcome = %#v, want nothing reserved and nothing integrated", result.PausedBeforeStarting)
			}
			if invocations := len(pipelined.provider.RequestsForRole(domain.RoleDeveloper)); invocations != 0 {
				t.Fatalf("developer invocations = %d, want none behind a pause", invocations)
			}
			if result.RecordProblem != "" {
				t.Fatalf("record problem = %q, want the claim given back cleanly", result.RecordProblem)
			}
			if _, claimed, err := pipelined.reruns.Find(triage.Key(triage.ClassStoppedRun, priorRunID)); err != nil || claimed {
				t.Fatalf("claimed = %t, error = %v, want the stoppage to keep its re-run", claimed, err)
			}
			// What the carry-out reports has to say the pause and the accounting, since
			// a reader who is told only that nothing ran cannot tell this from a
			// stoppage that has spent everything it had.
			rendered := result.Render()
			for _, want := range []string{"NOT STARTED", "keeps its one re-run", "decision still stands"} {
				if !strings.Contains(rendered, want) {
					t.Fatalf("rendered = %q, is missing %q", rendered, want)
				}
			}

			// The pause lifts, and the same decision is carried out — rather than
			// being refused by the once-only guard for a run that never happened.
			door.lift(t, pipelined)
			rerun, err := pipelined.rerunner.Rerun(context.Background(), RerunRequest{Run: priorRunID})
			if err != nil {
				t.Fatalf("Rerun() once the pause lifted error = %v", err)
			}
			if !rerun.Started || rerun.Outcome.Integration == nil {
				t.Fatalf("result = %#v, want the fresh run started and landed", rerun)
			}
		})
	}
}

// A claim the harness could not give back after a pause is said out loud, for the
// reason a raced reservation's is: the stoppage has then spent its one re-run on a
// run that never started, and only somebody looking can put it back.
func TestAClaimThatCouldNotBeGivenBackAfterAPauseIsReported(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	held := runstate.OperatorHold{HeldAt: docketedNow}
	harness.outcome = Outcome{WorkItemID: docketedItem, Paused: true, PausedByOperator: &held}
	rerunner := harness.rerunner()
	rerunner.Reruns = unwithdrawableReruns{RerunRecords: harness.reruns}
	result, err := rerunner.Rerun(context.Background(), rerunRequest())
	if err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	if result.PausedBeforeStarting == nil {
		t.Fatalf("result = %#v, want the pause still reported", result)
	}
	if !strings.Contains(result.RecordProblem, "could not be given back") {
		t.Fatalf("record problem = %q, want the spent claim named", result.RecordProblem)
	}
	if !strings.Contains(result.RecordProblem, "pause on all harness activity") {
		t.Fatalf("record problem = %q, want it to name what the run met", result.RecordProblem)
	}
}

// The sequence this seam was opened by, over the real pipeline: the last free
// developer slot goes while the claim is being taken, and the reservation
// refuses the fresh run for capacity. The claim is given back, so the same
// decision launches on the first attempt that finds a slot — rather than being
// refused by the once-only guard for a run that never happened.
func TestARerunRacedForTheLastSlotGivesTheClaimBackAndLaunchesLater(t *testing.T) {
	t.Parallel()

	var pipelined *pipelinedRerun
	occupying := runningState("run-11112222333344445555666677778888", "yoyodyne-ifd.other")
	raced := false
	pipelined = newPipelinedRerun(t, func() {
		// Another run takes the last slot while this carry-out is committing to the
		// work, which is the window the reading before the claim cannot cover. It
		// happens once: the second attempt is the one that finds a developer.
		if raced {
			return
		}
		raced = true
		if err := pipelined.runs.Create(occupying); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	})
	result, err := pipelined.rerunner.Rerun(context.Background(), RerunRequest{Run: priorRunID})
	if err != nil {
		t.Fatalf("Rerun() error = %v, want the raced slot waited on rather than failed", err)
	}
	if result.Started || result.CapacityFull == nil {
		t.Fatalf("result = %#v, want a full harness reported and nothing started", result)
	}
	if invocations := len(pipelined.provider.RequestsForRole(domain.RoleDeveloper)); invocations != 0 {
		t.Fatalf("developer invocations = %d, want none behind a reservation that was refused", invocations)
	}
	if _, claimed, err := pipelined.reruns.Find(triage.Key(triage.ClassStoppedRun, priorRunID)); err != nil || claimed {
		t.Fatalf("claimed = %t, error = %v, want the claim given back", claimed, err)
	}

	// The other run ends, and the same decision is carried out.
	occupying.Status = runstate.StatusSucceeded
	completed := docketedNow
	occupying.CompletedAt = &completed
	if err := pipelined.runs.Save(occupying); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	rerun, err := pipelined.rerunner.Rerun(context.Background(), RerunRequest{Run: priorRunID})
	if err != nil {
		t.Fatalf("Rerun() once a slot freed error = %v", err)
	}
	if !rerun.Started || rerun.Outcome.Integration == nil {
		t.Fatalf("result = %#v, want the fresh run started and landed", rerun)
	}
}

// The sequence that was worked around by hand on yoyodyne-ifd.125.1, replayed
// over the real pipeline — and no longer a sequence at all. Stopping the run
// blocked the item, and a status set when work stops and never rewritten when
// what stopped it clears is not something to refuse a decision over. Nothing the
// item is waiting for is unfinished, so the development manager's decision is
// carried out on the first attempt, with nobody having had to remember to reopen
// the item beforehand.
func TestARerunOnABlockedItemIsCarriedOutWithoutAnybodyReopeningIt(t *testing.T) {
	t.Parallel()

	pipelined := newPipelinedRerun(t, nil)
	// What stopping the run did to the item, which is the state a docketed
	// stoppage is ordinarily found in.
	pipelined.tracker.Item.Status = "blocked"
	rerun, err := pipelined.rerunner.Rerun(context.Background(), RerunRequest{Run: priorRunID})
	if err != nil {
		t.Fatalf("Rerun() on a blocked item error = %v, want the decision carried out", err)
	}
	if !rerun.Started || rerun.Outcome.Integration == nil {
		t.Fatalf("result = %#v, want the fresh run started and landed", rerun)
	}
}

// The same accounting over the real pipeline, for a refusal the action's own
// pre-flight cannot cover: something else starts the item while this carry-out is
// committing to it, and the pipeline refuses the fresh run before it reserves
// anything. Nothing was reserved, so the claim goes back rather than leaving the
// stoppage refused as already re-run for a run nobody made.
func TestARerunRefusedByThePipelineGivesTheClaimBackAndLaunchesLater(t *testing.T) {
	t.Parallel()

	var pipelined *pipelinedRerun
	racing := runningState("run-22223333444455556666777788889999", docketedItem)
	raced := false
	pipelined = newPipelinedRerun(t, func() {
		if raced {
			return
		}
		raced = true
		if err := pipelined.runs.Create(racing); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	})
	result, err := pipelined.rerunner.Rerun(context.Background(), RerunRequest{Run: priorRunID})
	if err == nil || !strings.Contains(err.Error(), racing.RunID) {
		t.Fatalf("Rerun() error = %v, want the run that took the item named", err)
	}
	if !strings.Contains(err.Error(), "keeps its re-run") {
		t.Fatalf("refusal %q does not say the stoppage keeps its re-run", err)
	}
	if result.Started {
		t.Fatalf("result = %#v, want nothing reported as started", result)
	}
	if invocations := len(pipelined.provider.RequestsForRole(domain.RoleDeveloper)); invocations != 0 {
		t.Fatalf("developer invocations = %d, want none behind a run that was never reserved", invocations)
	}
	if _, claimed, err := pipelined.reruns.Find(triage.Key(triage.ClassStoppedRun, priorRunID)); err != nil || claimed {
		t.Fatalf("claimed = %t, error = %v, want the claim given back", claimed, err)
	}

	// The other run ends, and the same decision is carried out.
	racing.Status = runstate.StatusSucceeded
	completed := docketedNow
	racing.CompletedAt = &completed
	if err := pipelined.runs.Save(racing); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	rerun, err := pipelined.rerunner.Rerun(context.Background(), RerunRequest{Run: priorRunID})
	if err != nil {
		t.Fatalf("Rerun() once the item was free error = %v", err)
	}
	if !rerun.Started || rerun.Outcome.Integration == nil {
		t.Fatalf("result = %#v, want the fresh run started and landed", rerun)
	}
}

// pipelinedRerun is the re-run action over the real pipeline: one repository,
// one state root, and one intake hold record read by both the action and the
// run it starts.
type pipelinedRerun struct {
	rerunner Rerunner
	runs     *runstate.Store
	intake   *runstate.IntakeHoldStore
	// holds is the operator's pause on all activity and directives what they have
	// directed, both read by the pipeline where it would start the fresh run. They
	// are the two pauses the action itself never reads, so a test that drives one
	// is driving the door the pipeline holds rather than a second rendering of it.
	holds      *runstate.OperatorHoldStore
	directives *runstate.DirectiveStore
	provider   *orchestratortest.Backend
	// tracker is the one work item both readings ask about: the action's, before
	// it claims anything, and the pipeline's, where it would start the work.
	tracker *orchestratortest.Tracker
	reruns  *runstate.RerunStore
}

// newPipelinedRerun builds it over a stopped, docketed, decided-about run.
// beforeStart is called between the claim and the run, which is where a test
// puts something that arrives while the harness is committing to the work.
func newPipelinedRerun(t *testing.T, beforeStart func()) *pipelinedRerun {
	t.Helper()
	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
		ID:     docketedItem,
		Title:  docketedTitle,
		Status: "open",
		// Where a triage decision lands: the item's notes, which every run's
		// context bundle carries to the developer.
		Notes: rerunGuidance,
	}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	// One intake record for both readings: the pipeline reads it where it would
	// start the work, and the action reads it before it claims anything. A test
	// that wired them to two records would prove nothing about either, which is
	// why the pipeline's own is replaced here rather than left where the shared
	// fixture put it.
	root := t.TempDir()
	intake, err := runstate.NewIntakeHoldStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewIntakeHoldStore() error = %v", err)
	}
	pipeline.Intake = intake
	// The operator's hold and the directive record are the pipeline's own, kept
	// here so a test can place one between the claim and the run: they are read
	// where the fresh run would start, which is the door this fixture exists to
	// drive.
	holds := newOperatorHoldStore(t)
	pipeline.Holds = holds
	directives := newDirectiveStore(t)
	pipeline.Directives = directives
	// And one re-run record for both readings, for exactly the reason there is one
	// intake record. The action claims the re-run before it starts anything, and
	// the pipeline reads that claim where it would otherwise refuse to start a
	// fresh run on work a repair is owed — so a test that wired them to two
	// records would prove nothing about either, and would have the fresh run
	// refused by a claim it had itself just taken. It is the store's own, which is
	// what `buildRerunner` and `pipelineFrom` share in the command.
	reruns := store.Reruns()
	// The stoppage this decision is about, recorded in the same store the fresh
	// run is reserved in, so what is in flight is read from one place. It is a
	// different run to the one this pipeline will reserve, because the item has
	// been run twice: once into a stoppage, and once again on the decision.
	stopped := stoppedState()
	stopped.RunID = priorRunID
	if err := store.Create(stopped); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	docket := &memoryDocket{}
	if _, err := docketerOver(nil, docket).RecordStoppedRun(stopped); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	recordRerunDecision(t, store, stopped.WorkItemID, stopped.RunID)

	return &pipelinedRerun{
		runs:       store,
		intake:     intake,
		holds:      holds,
		directives: directives,
		provider:   provider,
		tracker:    tracker,
		reruns:     reruns,
		rerunner: Rerunner{
			Docket:    docket,
			Runs:      store,
			Intake:    intake,
			Reruns:    reruns,
			Decisions: store.Triage(),
			// One tracker for both readings, for the reason there is one intake
			// record: a test that wired the action to a different item from the one
			// the pipeline starts on would prove nothing about either.
			Items: tracker,
			Gates: store,
			// The limit the pipeline reserves against, so the action's reading and
			// the reservation's are one number rather than two.
			Capacity: 1,
			Start: func(ctx context.Context, workItemID string, selection runstate.Selection) (Outcome, error) {
				if beforeStart != nil {
					beforeStart()
				}
				fresh := pipeline
				fresh.Selection = selection
				return fresh.Run(ctx, workItemID)
			},
		},
	}
}

// The decision standing about this stoppage has to be a re-run. A development
// manager who decided to wait decided something the harness must not start a run
// on: carrying it out would attribute a fresh run to a decision nobody made,
// which is the whole failure this record exists to end.
func TestAStoppageDecidedSomethingOtherThanARerunIsRefused(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	// The development manager came back to the same stoppage and decided to wait,
	// which supersedes the re-run they had recorded about it.
	if _, err := harness.runs.Triage().RecordDecision(context.Background(), docketedItem,
		triageDecided(runstate.TriageDecisionWait, docketedRunID), docketedNow); err != nil {
		t.Fatalf("RecordDecision() error = %v", err)
	}
	_, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err == nil || !strings.Contains(err.Error(), `is "wait" rather than a re-run`) {
		t.Fatalf("Rerun() error = %v, want a refusal naming the decision that is standing", err)
	}
	if len(harness.started) != 0 {
		t.Fatalf("started = %#v, want nothing started", harness.started)
	}
	if _, claimed, _ := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); claimed {
		t.Fatalf("a re-run nobody decided spent the stoppage's claim")
	}
}

// A recorded reasoning longer than a run's selection may hold is folded to fit
// rather than refusing to carry out the decision.
func TestALongDecisionIsFoldedIntoWhatTheRunCanRecord(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	// The development manager argued it at length, on the same stoppage, which
	// supersedes the shorter decision the harness was built with.
	longWinded := triageDecided(runstate.TriageDecisionRerun, docketedRunID)
	longWinded.Reason = strings.Repeat("the ground moved. ", 200)
	if _, err := harness.runs.Triage().RecordRerun(context.Background(), docketedItem, longWinded, docketedNow,
		runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 2, MergeRearms: 2}); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}
	result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	if len(result.Reason) > runstate.MaxSelectionReasonBytes {
		t.Fatalf("reason is %d bytes, which a run's selection would refuse", len(result.Reason))
	}
	if err := harness.started[0].selection.Validate(); err != nil {
		t.Fatalf("the folded selection is not one a run may carry: %v", err)
	}
}

// A record that could not be updated after the run never becomes silence: the
// run happened, and where its artifacts stand is what somebody has to be told.
func TestARecordThatCouldNotBeSettledIsReportedBesideTheRun(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	rerunner := harness.rerunner()
	rerunner.Reruns = unsettlableReruns{RerunRecords: harness.reruns}
	result, err := rerunner.Rerun(context.Background(), rerunRequest())
	if err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	if !result.Started {
		t.Fatalf("the run was not started: %#v", result)
	}
	if !strings.Contains(result.RecordProblem, "could not be recorded") {
		t.Fatalf("record problem = %q, want the unrecorded disposition named", result.RecordProblem)
	}
}

// The reason a re-run records names the development manager, so the harness
// checks that one actually decided this rather than taking the caller's word:
// the decision spends the item's re-run budget as it is recorded, and an item
// carrying none is an item nobody decided this about.
func TestARerunNobodyDecidedIsRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runs, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	if err := runs.Create(stoppedState()); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewIntakeHoldStore() error = %v", err)
	}
	docket := &memoryDocket{}
	if _, err := docketerOver(nil, docket).RecordStoppedRun(stoppedState()); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	started := 0
	rerunner := Rerunner{
		Docket: docket, Runs: runs, Intake: intake, Reruns: runs.Reruns(), Decisions: runs.Triage(),
		Items: openWorkItem(docketedItem), Gates: runs, Capacity: 1,
		Start: func(context.Context, string, runstate.Selection) (Outcome, error) {
			started++
			return Outcome{}, nil
		},
	}
	_, err = rerunner.Rerun(context.Background(), rerunRequest())
	if err == nil || !strings.Contains(err.Error(), "no triage decision about the stoppage of run") {
		t.Fatalf("Rerun() error = %v, want a refusal naming the missing decision record", err)
	}
	if started != 0 {
		t.Fatalf("started = %d, want nothing started on nobody's decision", started)
	}
	if _, claimed, _ := runs.Reruns().Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); claimed {
		t.Fatalf("a re-run nobody decided spent the stoppage's claim")
	}
}

// The recorded reason cites the decision it was built from, which is what makes
// the attribution on the run checkable rather than only plausible: every part of
// it is read from the record the development manager's conversation wrote, so a
// reader who doubts it can go to the turn it names.
func TestTheRecordedReasonCitesTheDecisionItWasReadFrom(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	for _, want := range []string{
		"durable triage budget",
		"recorded by the development manager in conversation " + decidedIn,
		fmt.Sprintf("after turn %d", decidedOnTurn),
		"reasoning that decision was recorded with",
		rerunReasoning,
	} {
		if !strings.Contains(result.Reason, want) {
			t.Fatalf("reason %q is missing %q", result.Reason, want)
		}
	}
}

// openWorkItem is a tracker reporting one item in a state a fresh run may start
// on, for the sequences whose subject is something other than the item itself.
type openWorkItem string

func (id openWorkItem) Show(context.Context, string) (beads.WorkItem, error) {
	return beads.WorkItem{ID: string(id), Status: "open"}, nil
}

func (id openWorkItem) Release(context.Context, string, string) (beads.WorkItem, error) {
	return beads.WorkItem{ID: string(id), Status: "open"}, nil
}

// unwritableRuns reads exactly as the store does, including taking the stopped
// run's lease, and cannot write the removal back onto it.
type unwritableRuns struct {
	RerunRuns
}

func (unwritableRuns) Save(runstate.State) error {
	return errors.New("the run record is unwritable")
}

// unsettlableReruns claims exactly as the store does and then cannot write down
// what became of the run.
type unsettlableReruns struct {
	RerunRecords
}

func (unsettlableReruns) Settle(context.Context, string, string, runstate.PreservedArtifacts) (runstate.Rerun, error) {
	return runstate.Rerun{}, errors.New("the record is unwritable")
}

// A re-run wired without the parts it needs refuses before it reads anything.
func TestARerunnerWithoutItsPartsRefuses(t *testing.T) {
	t.Parallel()

	_, err := Rerunner{}.Rerun(context.Background(), rerunRequest())
	if err == nil {
		t.Fatal("Rerun() with nothing wired did not refuse")
	}
	for _, want := range []string{"triage docket", "intake hold", "one per docketed stoppage", "triage record", "work item", "human gates", "start a run", "developer capacity"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal is missing %q: %v", want, err)
		}
	}
}

// The whole of what yoyodyne-ifd.268 is about, end to end: a run that died
// holding its change is docketed, and the re-run the development manager records
// against that stoppage is one the harness carries out.
//
// Every one of the three incidents ended the other way. The docket had no entry
// for the death, so the verb that acts on one refused, the recorded decision sat
// on the item as a phantom, and a person dispatched the item by name instead —
// which starts a run and records nothing about why.
func TestARerunCarriesOutTheDecisionAboutARunThatDiedHoldingItsChange(t *testing.T) {
	t.Parallel()

	died := diedHoldingItsChange()
	harness := newRerunHarness(t, died)
	// The stoppage is on the docket at all, which is what the three incidents
	// lacked and what the verb below looks the run up in.
	if len(harness.docket.entries) != 1 || harness.docket.entries[0].Failure != died.Failure {
		t.Fatalf("the death did not reach the docket: %#v", harness.docket.entries)
	}

	result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	if !result.Started || len(harness.started) != 1 {
		t.Fatalf("the recorded decision was not carried out: result = %#v, started = %#v", result, harness.started)
	}
	// And the fresh run accounts for itself as the development manager's decision,
	// which is what a dispatch by name could never record.
	if harness.started[0].selection.By != runstate.SelectedByDevelopmentManager {
		t.Fatalf("selection = %#v, want the development manager's decision", harness.started[0].selection)
	}
	if !strings.Contains(result.Reason, rerunReasoning) {
		t.Fatalf("reason = %q, want the reasoning the decision was recorded with", result.Reason)
	}
	// What the death left is what the re-run is about, so it is named on the
	// result rather than left to be found.
	if result.Preserved.Branch != died.Branch || result.Preserved.Disposition != runstate.PreservedKept {
		t.Fatalf("preserved = %#v, want the branch the change is on", result.Preserved)
	}
	// And the stoppage gets exactly one, the same as every other.
	if _, claimed, _ := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); !claimed {
		t.Fatal("the carried-out re-run was not claimed against the stoppage")
	}
}

// A death that was docketed holding its change and has since lost it is still
// run again on the decision recorded about it: a re-run starts from the target
// branch, so there is nothing about the missing change for it to refuse.
func TestARerunOfADeathThatLeftNothingBehindStartsFromTheTarget(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, diedHoldingItsChange())
	gone := diedHoldingItsChange()
	gone.Branch = ""
	gone.WorktreePath = ""
	gone.BaseCommit = ""
	gone.TargetBranch = ""
	if err := harness.runs.Save(gone); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err != nil {
		t.Fatalf("Rerun() error = %v, want the item started again", err)
	}
	if len(harness.started) != 1 || harness.started[0].selection.Lift != nil {
		t.Fatalf("started = %#v, want one fresh run from the target branch", harness.started)
	}
	if result.Preserved.Disposition != runstate.PreservedGone {
		t.Fatalf("preserved = %#v, want nothing recorded as kept", result.Preserved)
	}
	if _, claimed, _ := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); !claimed {
		t.Fatal("the carried-out re-run was not claimed against the stoppage")
	}
}

// The accounting failure yoyodyne-ifd.441 found: a re-run whose fresh run died
// creating its worktree spent the item's one re-run on a run no developer ever
// saw, and the next stoppage of it was an escalation rather than a decision.
// The run existed, so this is a claim given back past the reservation — the only
// one there is — and the run's own record is what earns it.
func TestAFreshRunTheEnvironmentRefusedBeforeAnythingRanGivesTheClaimBack(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	freshRunID := "run-fedcba9876543210fedcba9876543210"
	harness.failure = fmt.Errorf("create isolated worktree: %w: the checkout of 1099 file(s) was still running after 5m0s, so no worktree was made and no agent of this run was invoked", gitworktree.ErrCheckoutKilled)
	harness.outcome = Outcome{
		RunID:      freshRunID,
		WorkItemID: docketedItem,
		Status:     runstate.StatusFailed,
		Environmental: &runstate.EnvironmentalRefusal{
			Cause:      runstate.CauseWorktreeCheckoutKilled,
			Detail:     "the checkout of 1099 file(s) was still running after 5m0s",
			RecordedAt: docketedNow,
			NothingRan: true,
			Settled:    true,
			Refused:    true,
		},
	}
	result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err == nil || !strings.Contains(err.Error(), "1099 file(s)") {
		t.Fatalf("Rerun() error = %v, want the failure reported as it was", err)
	}
	if !result.ClaimGivenBack {
		t.Fatalf("result = %#v, want the claim reported as given back", result)
	}
	if result.RecordProblem != "" {
		t.Fatalf("record problem = %q, want the claim given back cleanly", result.RecordProblem)
	}
	if !strings.Contains(result.Render(), "keeps its one re-run") {
		t.Fatalf("Render() = %q, want the accounting said rather than left to be inferred", result.Render())
	}
	if _, claimed, err := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); err != nil || claimed {
		t.Fatalf("claimed = %t, error = %v, want the claim given back", claimed, err)
	}

	// And the decision survives it: asking again once the machine is not what
	// stops it carries out the same one rather than meeting the once-only guard.
	harness.failure = nil
	harness.outcome = Outcome{RunID: freshRunID, WorkItemID: docketedItem, Status: runstate.StatusSucceeded}
	if _, err := harness.rerunner().Rerun(context.Background(), rerunRequest()); err != nil {
		t.Fatalf("Rerun() after the refusal error = %v, want the same decision carried out", err)
	}
	if len(harness.started) != 2 {
		t.Fatalf("starts = %d, want the refused attempt and the one that ran", len(harness.started))
	}
}

// A fresh run the provider's usage window stopped keeps the re-run its decision
// authorized, even though an agent was asked something before the window
// closed: nothing it produced was judged, so what the claim bought is nothing.
// The window lifts by itself, and asking again after it carries out the same
// decision.
func TestAFreshRunTheUsageWindowStoppedGivesTheClaimBack(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	freshRunID := "run-fedcba9876543210fedcba9876543210"
	resetsAt := docketedNow.Add(92 * time.Hour)
	harness.outcome = Outcome{
		RunID:      freshRunID,
		WorkItemID: docketedItem,
		Status:     runstate.StatusCancelled,
		Environmental: &runstate.EnvironmentalRefusal{
			Cause:      runstate.CauseUsageWindow,
			RecordedAt: docketedNow,
			ResetsAt:   &resetsAt,
			Settled:    true,
			Refused:    true,
		},
	}
	result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	if !result.ClaimGivenBack || result.RecordProblem != "" {
		t.Fatalf("result = %#v, want the claim given back cleanly", result)
	}
	if _, claimed, err := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); err != nil || claimed {
		t.Fatalf("claimed = %t, error = %v, want the claim given back", claimed, err)
	}
}

// A round the environment refused after an agent had already been asked
// something keeps its claim: the claim bought a developer's work, whatever
// became of it, and giving it back would let one decision start two runs.
func TestAFreshRunRefusedAfterAnAgentRanKeepsItsClaim(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	harness.failure = errors.New("the sandbox the agent runs in could not be entered")
	harness.outcome = Outcome{
		RunID:      "run-fedcba9876543210fedcba9876543210",
		WorkItemID: docketedItem,
		Status:     runstate.StatusFailed,
		Environmental: &runstate.EnvironmentalRefusal{
			Cause:      runstate.CauseSandboxSpawnFailure,
			RecordedAt: docketedNow,
			Settled:    true,
			Refused:    true,
		},
	}
	result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err == nil {
		t.Fatalf("Rerun() error = %v, want the failure reported", err)
	}
	if result.ClaimGivenBack {
		t.Fatalf("result = %#v, want a round that reached an agent to keep its claim", result)
	}
	if _, claimed, _ := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); !claimed {
		t.Fatal("a re-run whose fresh run reached an agent gave its claim back")
	}
}

// publishedStoppedState is the same stopped run with the publication it left
// open: the branch it pushed and a pull request nothing has merged, which is
// what a stopped run of a publishing project always leaves behind.
func publishedStoppedState() runstate.State {
	state := stoppedState()
	state.PullRequest = &runstate.PullRequest{
		Remote:     "origin",
		Branch:     state.Branch,
		Number:     124,
		URL:        "https://example.invalid/pull/124",
		HeadCommit: strings.Repeat("b", 40),
		State:      "OPEN",
	}
	return state
}

// The convergence sweep finds these eventually; triage knows at the moment it
// decides. Once the fresh run has integrated, the pull request the stopped run
// left open carries work that has landed by another vehicle and will never
// merge, so it is closed here rather than left for the next sweep — which is
// what the close-by-hand practice could not keep up with.
func TestARerunClosesThePublicationTheStoppedRunLeftOpen(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, publishedStoppedState())
	harness.integrated()
	harness.merged(126)

	result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	if result.Publication == nil || !result.Publication.Closed || !result.Publication.BranchDeleted {
		t.Fatalf("publication = %#v, want the request closed and its branch deleted", result.Publication)
	}
	if len(harness.closed) != 1 || harness.closed[0].Number != 124 {
		t.Fatalf("closed = %#v, want the stopped run's pull request 124 closed once", harness.closed)
	}
	comment := harness.closed[0].Comment
	for _, expected := range []string{"#126", harness.outcome.RunID, docketedRunID, docketedItem} {
		if !strings.Contains(comment, expected) {
			t.Errorf("close comment does not name %q:\n%s", expected, comment)
		}
	}
	if len(harness.deleted) != 1 || harness.deleted[0] != "yoyodyne/task/abc" {
		t.Errorf("deleted remote branches = %v, want the one the closed request published", harness.deleted)
	}
	if result.RecordProblem != "" {
		t.Errorf("record problem = %q, want none", result.RecordProblem)
	}

	// The run's own record says which vehicle retired it, so a later sweep does
	// not ask the forge about a request this already closed.
	retired, err := harness.runs.Load(docketedRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !strings.Contains(retired.PullRequest.Superseded, "#126") {
		t.Errorf("recorded supersession = %q, want the merged pull request that landed the work", retired.PullRequest.Superseded)
	}
}

// Nothing is superseded until something lands. A fresh run that failed leaves the
// stopped run's publication exactly where it was — closing it would retire a
// request for work that is still nobody's, on the strength of an attempt that
// achieved nothing.
func TestARerunLeavesThePublicationOpenWhenTheFreshRunIntegratedNothing(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, publishedStoppedState())
	harness.outcome.Status = runstate.StatusFailed

	if _, err := harness.rerunner().Rerun(context.Background(), rerunRequest()); err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	if len(harness.closed) != 0 {
		t.Fatalf("closed = %#v, want nothing closed behind a run that landed nothing", harness.closed)
	}
	if len(harness.deleted) != 0 {
		t.Fatalf("deleted = %v, want the published branch left where it is", harness.deleted)
	}
	kept, err := harness.runs.Load(docketedRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if kept.PullRequest.Superseded != "" {
		t.Errorf("recorded supersession = %q, want none", kept.PullRequest.Superseded)
	}
}

// merged is the fresh run's own publication landing, which makes it the vehicle
// the superseded request is closed in the name of.
func (h *rerunHarness) merged(number int) {
	h.outcome.PullRequest = &runstate.PullRequest{
		Remote:     "origin",
		Branch:     "yoyodyne/task/fedcba98",
		Number:     number,
		URL:        fmt.Sprintf("https://example.invalid/pull/%d", number),
		HeadCommit: strings.Repeat("c", 40),
		State:      "MERGED",
		Merged:     true,
	}
}

func TestRerunChecksTheRepositoryEvenWhenRemovalFlagsDisagree(t *testing.T) {
	t.Parallel()
	for _, there := range []bool{true, false} {
		harness := newRerunHarness(t, stoppedState())
		state, err := harness.runs.Load(docketedRunID)
		if err != nil {
			t.Fatal(err)
		}
		state.Blocker = ""
		state.Failure = "the provider ended after writing the change"
		state.BranchRemoved, state.WorktreeRemoved = there, there
		if there {
			state.ArtifactsRetiredBy = priorRunID
		}
		if err := harness.runs.Save(state); err != nil {
			t.Fatal(err)
		}
		rerunner := harness.rerunner()
		rerunner.Remains = &orchestratortest.Survival{Survival: gitworktree.Survival{BranchExists: there, WorktreePresent: there}}
		// The re-run starts either way; what the repository holds decides what it
		// records as kept, whatever the flags say.
		result, err := rerunner.Rerun(context.Background(), rerunRequest())
		if err != nil || len(harness.started) != 1 {
			t.Fatalf("Rerun() = %#v, %v, want started", result, err)
		}
		if kept := result.Preserved.Disposition == runstate.PreservedKept; kept != there {
			t.Fatalf("preserved = %#v, want kept %t", result.Preserved, there)
		}
	}
}
