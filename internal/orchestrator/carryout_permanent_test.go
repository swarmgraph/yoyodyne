package orchestrator

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

type countedRepair struct {
	RepairContinuer
	attempts int
}

type recoveringCarryOutNotes struct {
	*orchestratortest.Tracker
	failures int
	landed   bool
	writes   int
}

func (n *recoveringCarryOutNotes) RecordOutcome(ctx context.Context, id, note string) (beads.WorkItem, error) {
	n.writes++
	if n.failures > 0 {
		n.failures--
		if n.landed {
			_, _ = n.Tracker.RecordOutcome(ctx, id, note)
			n.Item.Notes += note
		}
		return beads.WorkItem{}, errors.New("the tracker did not confirm the note")
	}
	item, err := n.Tracker.RecordOutcome(ctx, id, note)
	n.Item.Notes += note
	return item, err
}

func TestAPermanentRefusalNoteIsRetriedWithoutRetryingTheAction(t *testing.T) {
	t.Parallel()
	for _, landed := range []bool{false, true} {
		name := "append refused"
		if landed {
			name = "append landed but confirmation failed"
		}
		t.Run(name, func(t *testing.T) {
			harness := newContinueHarness(t, continuableState())
			continuer := harness.continuer()
			continuer.Remains = &orchestratortest.Survival{Survival: gitworktree.Survival{BranchExists: true}}
			repairer := &countedRepair{RepairContinuer: continuer}
			notes := &recoveringCarryOutNotes{Tracker: harness.tracker, failures: 1, landed: landed}
			watch := harness.carryOut()
			watch.Repairer, watch.Notes = repairer, notes
			watch.Clock = laterClock{after: time.Minute}
			harness.docket.close(triage.Key(triage.ClassStoppedRun, docketedRunID), runstate.TriageDecisionRepair, docketedNow)
			first, _, err := watch.Carry(context.Background(), theOneOutstanding(t, watch))
			if err != nil || first.Cause != triage.CarryOutWorktreeGone || !strings.Contains(first.RecordProblem, "remains pending") {
				t.Fatalf("first refusal = %+v, %v; want the note failure recorded", first, err)
			}
			counters, err := harness.runs.Triage().Counters(docketedItem)
			if err != nil || len(counters.PendingCarryOutNotes) != 1 {
				t.Fatalf("durable pending notes = %+v, %v; want the note saved with the refusal", counters, err)
			}
			note := counters.PendingCarryOutNotes[0]
			if !strings.Contains(note, counters.CarryOuts[0].Refusal) || !strings.Contains(note, "re-run or an escalation") {
				t.Fatalf("pending note lacks the refusal or decisions: %s", note)
			}
			// Rebuild the carry-out and its store view as a later process would.
			next := harness.carryOut()
			next.Repairer, next.Notes = repairer, notes
			next.Clock = laterClock{after: 24 * time.Hour}
			queue := newScheduleHarness()
			scheduler := Scheduler{Open: func(ctx context.Context) (Pull, error) {
				pull, err := queue.open(ctx)
				pull.CarryOut = next
				return pull, err
			}}
			for pull := 0; pull < 3; pull++ {
				if schedule, err := scheduler.Schedule(context.Background()); err != nil || schedule.CarryOutNoteProblem != "" || len(schedule.Started) != 0 {
					t.Fatalf("later pull = %+v, %v; want only the pending note delivered", schedule, err)
				}
			}
			counters, err = harness.runs.Triage().Counters(docketedItem)
			if err != nil || len(counters.PendingCarryOutNotes) != 0 || len(counters.CarryOuts) != 1 || counters.CarryOuts[0].Attempts != 1 {
				t.Fatalf("after delivery = %+v, %v; want the pending note cleared and refusal untouched", counters, err)
			}
			if repairer.attempts != 1 || len(notes.NoteRecords) != 1 || notes.NoteRecords[0] != note {
				t.Fatalf("action attempts = %d, notes = %+v; want one action and one exact note", repairer.attempts, notes.NoteRecords)
			}
			wantWrites := 2
			if landed {
				wantWrites = 1
			}
			if notes.writes != wantWrites {
				t.Fatalf("note writes = %d, want %d", notes.writes, wantWrites)
			}
		})
	}
}

func (c *countedRepair) Continue(ctx context.Context, request RepairContinueRequest) (RepairContinueResult, error) {
	c.attempts++
	return c.RepairContinuer.Continue(ctx, request)
}

func TestAPermanentRepairRefusalIsRecordedOnceAndNeverRetried(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		remains   gitworktree.Survival
		headError error
		cause     triage.CarryOutCause
	}{
		{name: "retired worktree", remains: gitworktree.Survival{BranchExists: true}, cause: triage.CarryOutWorktreeGone},
		{name: "deleted branch", remains: gitworktree.Survival{WorktreePresent: true}, cause: triage.CarryOutBranchGone},
		{name: "moved HEAD", remains: gitworktree.Survival{BranchExists: true, WorktreePresent: true}, headError: gitworktree.ErrOwnedHeadMoved, cause: triage.CarryOutHeadMoved},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newContinueHarness(t, continuableState())
			continuer := harness.continuer()
			continuer.Remains = &orchestratortest.Survival{Survival: test.remains}
			harness.ownership.Err = test.headError
			repairer := &countedRepair{RepairContinuer: continuer}
			watch := harness.carryOut()
			watch.Repairer = repairer
			watch.Notes = harness.tracker
			watch.Clock = laterClock{after: time.Minute}
			harness.docket.close(triage.Key(triage.ClassStoppedRun, docketedRunID), runstate.TriageDecisionRepair, docketedNow)

			carried, _, err := watch.Carry(context.Background(), theOneOutstanding(t, watch))
			if err != nil || carried.Carried || carried.Cause != test.cause || carried.RecordProblem != "" {
				t.Fatalf("Carry() = %+v, %v; want the permanent refusal recorded", carried, err)
			}
			for _, after := range []time.Duration{time.Hour, 24 * time.Hour, 7 * 24 * time.Hour} {
				watch.Clock = laterClock{after: after}
				if outstanding, err := watch.Outstanding(); err != nil || len(outstanding) != 0 {
					t.Fatalf("after %s Outstanding() = %+v, %v; want no second attempt", after, outstanding, err)
				}
				if written, err := watch.RecordUnattempted(context.Background(), time.Minute, nil); err != nil || len(written) != 0 {
					t.Fatalf("RecordUnattempted() = %+v, %v; want the refusal left intact", written, err)
				}
			}
			counters, err := harness.runs.Triage().Counters(docketedItem)
			if err != nil || len(counters.CarryOuts) != 1 || counters.CarryOuts[0].Attempts != 1 || repairer.attempts != 1 {
				t.Fatalf("counters = %+v, %v, attempts = %d; want one gate and one attempt", counters, err, repairer.attempts)
			}
			if harness.carried(t) != 0 || len(harness.started) != 0 || len(harness.tracker.NoteRecords) != 1 {
				t.Fatalf("started = %+v, notes = %+v; want no spend and one item note", harness.started, harness.tracker.NoteRecords)
			}
			for _, want := range []string{counters.CarryOuts[0].Refusal, "will not clear on its own", "re-run", "escalation"} {
				if !strings.Contains(harness.tracker.Notes, want) {
					t.Fatalf("item note %q lacks %q", harness.tracker.Notes, want)
				}
			}
			entries, _ := harness.docket.List()
			docket := Docketer{Decisions: harness.runs.Triage(), Reruns: harness.runs.Reruns()}
			if problems := docket.joinDecisions(entries, docketedRunsOf(entries), nil, nil); len(problems) != 0 {
				t.Fatal(problems)
			}
			if len(entries) != 1 || entries[0].CarryOut == nil || entries[0].CarryOut.Cause != test.cause || !entries[0].Critical() {
				t.Fatalf("entries = %+v; want the one permanent gate on her docket", entries)
			}
			if rendered := entries[0].Render(); !strings.Contains(rendered, "no further attempt") || strings.Contains(rendered, "keeps trying it") {
				t.Fatalf("entry promises the wrong next step: %s", rendered)
			}
		})
	}
}

type countedRearmer struct {
	Rearmer
	attempts int
}

func (c *countedRearmer) Rearm(ctx context.Context, request RearmRequest) (RearmResult, error) {
	c.attempts++
	return c.Rearmer.Rearm(ctx, request)
}

func TestAnUnmakeableRearmWaitsForANewDecision(t *testing.T) {
	t.Parallel()
	harness := newRearmHarness(t)
	harness.decide(t)
	harness.docket.close(harness.publication(), runstate.TriageDecisionRearm, docketedNow)
	harness.forge.StatusErr = UnrearmablePublicationError{RunID: harness.state.RunID, Number: 92, Why: "this publication cannot be made from its recorded promotion"}
	rearmer := &countedRearmer{Rearmer: harness.rearmer()}
	watch := harness.carryOut(nil)
	watch.Rearmer = rearmer
	watch.Clock = laterClock{after: time.Minute}
	carried, err := watch.CarryRearms(context.Background(), false)
	if err != nil || len(carried) != 1 || carried[0].Cause != triage.CarryOutPublicationUnmakeable {
		t.Fatalf("CarryRearms() = %+v, %v; want a permanent forge refusal", carried, err)
	}
	// Even a changed environment cannot revive the old decision.
	harness.forge.StatusErr = nil
	watch.Clock = laterClock{after: 7 * 24 * time.Hour}
	if again, err := watch.CarryRearms(context.Background(), false); err != nil || len(again) != 0 || rearmer.attempts != 1 {
		t.Fatalf("later pull = %+v, %v, attempts %d; want no second attempt", again, err, rearmer.attempts)
	}
	if entry := refusedOnTheDocket(t, harness); entry.CarryOut.Cause != triage.CarryOutPublicationUnmakeable || entry.CarryOut.Attempts != 1 {
		t.Fatalf("docket entry = %+v; want one permanent gate", entry)
	}
	caps := rearmCaps
	caps.MergeRearms = 2
	if _, err := harness.runs.Triage().RecordMergeRearm(context.Background(), harness.state.WorkItemID, harness.publication(),
		triageDecided(runstate.TriageDecisionRearm, harness.state.RunID), docketedNow.Add(8*24*time.Hour), caps); err != nil {
		t.Fatal(err)
	}
	watch.Clock = laterClock{after: 8*24*time.Hour + time.Minute}
	rearmer.Clock = watch.Clock
	if again, err := watch.CarryRearms(context.Background(), false); err != nil || len(again) != 1 || !again[0].Carried || rearmer.attempts != 2 {
		t.Fatalf("new decision = %+v, %v, attempts %d; want it attempted", again, err, rearmer.attempts)
	}
}

func TestAnUnpromotedRearmIsNotAttemptedAgainAfterItsRefusal(t *testing.T) {
	t.Parallel()
	harness := newRearmHarness(t)
	state := harness.state
	state.Integration = nil
	if err := harness.runs.Save(state); err != nil {
		t.Fatal(err)
	}
	harness.decide(t)
	watch := harness.carryOut(nil)
	watch.Clock = laterClock{after: time.Minute}
	first, err := watch.CarryRearms(context.Background(), false)
	if err != nil || len(first) != 1 || first[0].Cause != triage.CarryOutPublicationUnmakeable {
		t.Fatalf("first = %+v, %v", first, err)
	}
	watch.Clock = laterClock{after: 24 * time.Hour}
	if again, err := watch.CarryRearms(context.Background(), false); err != nil || len(again) != 0 {
		t.Fatalf("later = %+v, %v; want no retry of an unpromoted publication", again, err)
	}
}

func TestPermanentCarryOutCausesAreTypedAndUnreadableWorkKeepsItsPacing(t *testing.T) {
	t.Parallel()
	for _, cause := range triage.CarryOutCauses() {
		if got := carryOutCause(permanentCarryOut(cause, errors.New("the original refusal"))); got != cause {
			t.Fatalf("cause = %q, want %q", got, cause)
		}
	}
	for _, err := range []error{
		errors.New("worktree HEAD moved, according to untrusted text"),
		WorktreeSurgeryError{Cause: errors.New("reading the worktree timed out")},
		continuableRepair(continuableState(), triage.Found{Unknown: true}),
	} {
		if got := carryOutCause(err); got != "" {
			t.Fatalf("%v was permanently classified as %q; a failed reading may clear", err, got)
		}
	}
}

func TestPermanentCarryOutCausesAreNamedInTheRunStopInventory(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("../../docs/run-stops.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, cause := range triage.CarryOutCauses() {
		if !strings.Contains(string(body), "`"+string(cause)+"`") {
			t.Errorf("the run-stop inventory does not name %s", cause)
		}
	}
}

func TestAPermanentRefusalIsDeliveredAfterTheOriginalStoppageWasAlreadyShown(t *testing.T) {
	t.Parallel()
	state := reviewStoppedState(docketedRunID, docketedItem)
	judge := &standingJudge{judgment: Judgment{ConversationID: "chat-abc"}}
	escalator := escalatorOver(t, []runstate.State{state}, judge, nil)
	clock := &movingClock{now: escalationNow}
	escalator.Clock = clock
	if sweep, err := escalator.Escalate(context.Background()); err != nil || len(sweep.Escalated) != 1 {
		t.Fatalf("original delivery = %+v, %v", sweep, err)
	}
	store, err := runstate.NewTriageStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	decided := escalationNow.Add(time.Minute)
	if _, err := store.GrantRepair(context.Background(), docketedItem,
		triageDecided(runstate.TriageDecisionRepair, docketedRunID), 1, decided, continueCaps); err != nil {
		t.Fatal(err)
	}
	key := triage.Key(triage.ClassStoppedRun, docketedRunID)
	escalator.Docket.(*memoryDocket).close(key, runstate.TriageDecisionRepair, decided)
	refused := decided.Add(time.Minute)
	if _, err := store.RecordCarryOutRefusal(context.Background(), docketedItem, runstate.TriageCarryOut{
		RunID: docketedRunID, Decision: runstate.TriageDecisionRepair, DecidedAt: decided,
		Cause: triage.CarryOutWorktreeGone, Gate: runstate.TriageGatePreservedWork,
		Refusal: "the preserved checkout was retired", Clears: "the development manager records a re-run or an escalation",
	}, refused); err != nil {
		t.Fatal(err)
	}
	escalator.Decisions = store
	clock.now = refused.Add(time.Minute)
	sweep, err := escalator.Escalate(context.Background())
	if err != nil || len(sweep.Escalated) != 1 || !sweep.Escalated[0].Delivered || len(judge.shown) != 2 {
		t.Fatalf("permanent refusal delivery = %+v, %v; shown %d", sweep, err, len(judge.shown))
	}
	if shown := judge.shown[1]; shown.CarryOut == nil || shown.CarryOut.Refusal != "the preserved checkout was retired" || shown.CarryOut.Cause != triage.CarryOutWorktreeGone {
		t.Fatalf("shown = %+v; want the refusal's words on the docket entry", shown)
	}
	clock.now = refused.Add(7 * 24 * time.Hour)
	if again, err := escalator.Escalate(context.Background()); err != nil || len(again.Escalated) != 0 || len(judge.shown) != 2 {
		t.Fatalf("second delivery = %+v, %v; shown %d, want the gate delivered once", again, err, len(judge.shown))
	}
}

func TestAPermanentUndocketedRefusalIsDeliveredFromAFoldedEarlierEntry(t *testing.T) {
	t.Parallel()
	for _, stoppedFirst := range []bool{true, false} {
		name := "stopped entry first"
		if !stoppedFirst {
			name = "escalation entry first"
		}
		t.Run(name, func(t *testing.T) {
			state := reviewStoppedState(docketedRunID, docketedItem)
			state.LandingOutcome = runstate.LandingEscalate
			judge := &standingJudge{judgment: Judgment{ConversationID: "chat-abc"}}
			escalator := escalatorOver(t, []runstate.State{state}, judge, nil)
			clock := &movingClock{now: escalationNow}
			escalator.Clock = clock
			docket := escalator.Docket.(*memoryDocket)
			raised := stoppedEntry(state)
			raised.Key = triage.Key(triage.ClassEscalation, state.RunID)
			raised.Class = triage.ClassEscalation
			raised.RecordedAt = raised.RecordedAt.Add(time.Minute)
			raised.Blocker = ""
			raised.Escalation = &triage.Escalation{RaisedBy: "developer", Reason: "the item cannot be met as written"}
			if _, err := docket.RecordOnce(raised); err != nil {
				t.Fatal(err)
			}
			if !stoppedFirst {
				docket.entries[0], docket.entries[1] = docket.entries[1], docket.entries[0]
			}
			// The original stopped run was already shown beneath this newer
			// escalation, whose key therefore carries its prior delivery.
			if sweep, err := escalator.Escalate(context.Background()); err != nil || len(sweep.Escalated) != 1 || sweep.Escalated[0].DocketKey != raised.Key {
				t.Fatalf("original folded stopped-run delivery = %+v, %v", sweep, err)
			}
			store, err := runstate.NewTriageStore(t.TempDir(), "yoyodyne")
			if err != nil {
				t.Fatal(err)
			}
			const undocketedRun = undocketedRunID
			decided := escalationNow.Add(time.Minute)
			if _, err := store.GrantRepair(context.Background(), docketedItem,
				triageDecided(runstate.TriageDecisionRepair, undocketedRun), 1, decided, continueCaps); err != nil {
				t.Fatal(err)
			}
			refused := decided.Add(time.Minute)
			const refusal = "the repair needs a docketed stoppage that the harness does not hold"
			if _, err := store.RecordCarryOutRefusal(context.Background(), docketedItem, runstate.TriageCarryOut{
				RunID: undocketedRun, Decision: runstate.TriageDecisionRepair, DecidedAt: decided,
				Cause: triage.CarryOutStoppageMissing, Gate: runstate.TriageGateHarness,
				Refusal: refusal, Clears: "the development manager records a re-run or an escalation",
			}, refused); err != nil {
				t.Fatal(err)
			}
			escalator.Decisions = store
			clock.now = refused.Add(time.Minute)
			if sweep, err := escalator.Escalate(context.Background()); err != nil || len(sweep.Escalated) != 1 || !sweep.Escalated[0].Delivered || len(judge.shown) != 2 {
				t.Fatalf("folded permanent refusal delivery = %+v, %v; shown %d, want one new delivery", sweep, err, len(judge.shown))
			}
			shown := judge.shown[1]
			if shown.Class != triage.ClassEscalation || len(shown.Earlier) != 1 {
				t.Fatalf("shown = %+v; want the newer escalation with the stopped run folded beneath it", shown)
			}
			for _, want := range []string{undocketedRun, refusal, "re-run or an escalation"} {
				if rendered := shown.Render(); !strings.Contains(rendered, want) {
					t.Fatalf("delivery lacks %q: %s", want, rendered)
				}
			}
			for _, after := range []time.Duration{time.Hour, 24 * time.Hour, 7 * 24 * time.Hour} {
				clock.now = refused.Add(after)
				if sweep, err := escalator.Escalate(context.Background()); err != nil || len(sweep.Escalated) != 0 || len(judge.shown) != 2 {
					t.Fatalf("later delivery = %+v, %v; shown %d, want no duplicate", sweep, err, len(judge.shown))
				}
			}
		})
	}
}

// Old budgets held the spend without recording the decision. Model that read
// while keeping the ordinary durable refusal writer and the real actions.
type legacyCarryDecisions struct{ *runstate.TriageStore }

func (s legacyCarryDecisions) Counters(id string) (runstate.TriageCounters, error) {
	counters, err := s.TriageStore.Counters(id)
	counters.Decisions = nil
	return counters, err
}

func TestAPreDecisionRecordGrantIsDocketedOnceWithoutAuthorizingARepair(t *testing.T) {
	t.Parallel()
	harness := newContinueHarness(t, continuableState())
	watch := harness.carryOut()
	legacy := legacyCarryDecisions{harness.runs.Triage()}
	watch.Decisions = legacy
	watch.Notes = harness.tracker
	repairer := &countedRepair{RepairContinuer: harness.continuer()}
	watch.Repairer = repairer
	watch.Clock = laterClock{after: time.Minute}
	first, _, err := watch.Carry(context.Background(), theOneOutstanding(t, watch))
	if err != nil || first.Carried || first.Cause != triage.CarryOutDecisionMissing || repairer.attempts != 0 {
		t.Fatalf("old grant = %+v, %v, action attempts %d; want only the missing decision refused", first, err, repairer.attempts)
	}
	watch.Clock = laterClock{after: 7 * 24 * time.Hour}
	if tasks, err := watch.Outstanding(); err != nil || len(tasks) != 0 {
		t.Fatalf("legacy grant was offered again: %+v, %v", tasks, err)
	}
	counters, err := legacy.Counters(docketedItem)
	finding, found := counters.RefusedCarryOut(docketedRunID)
	refused, _ := counters.CarryOutFindings()
	if err != nil || !found || finding.Attempts != 1 || refused != 1 || len(harness.tracker.NoteRecords) != 1 {
		t.Fatalf("legacy refusal = %+v, %v; want one recorded gate and one note", counters, err)
	}
	entries, _ := harness.docket.List()
	docket := Docketer{Decisions: legacy, Reruns: harness.runs.Reruns()}
	if problems := docket.joinDecisions(entries, docketedRunsOf(entries), nil, nil); len(problems) != 0 || entries[0].CarryOut == nil || entries[0].CarryOut.Cause != triage.CarryOutDecisionMissing || !entries[0].Critical() {
		t.Fatalf("legacy docket = %+v, %v; want the permanent missing-decision gate", entries, problems)
	}
}
