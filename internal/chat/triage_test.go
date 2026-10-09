package chat

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// The run a docket entry names, in the shape the harness mints them, and a
// second one for the entry beside it: two entries is what a decision can be
// transposed between.
const (
	stoppedRun      = "run-0123456789abcdef0123456789abcdef"
	otherStoppedRun = "run-fedcba9876543210fedcba9876543210"
	// countingProcess is who a round these tests seed was charged by. A round is
	// only ever given back to the process that charged it, and nothing here gives
	// one back; what it needs is somebody to have charged it.
	countingProcess = "pid-1-000000000000000a"
)

// A triage decision lands on the work item, and it says which stoppage it
// settled. That is the whole point of recording it: the next reader of a run
// that stopped — a later conversation, a later operator — finds the decision
// beside the evidence rather than the evidence alone, and does not decide it a
// second time.
func TestATriageDecisionIsRecordedOnTheWorkItem(t *testing.T) {
	t.Parallel()

	answer := trackerReply("The refused half is its own item now.",
		`{"action":"triage","id":"yoyodyne-ifd.70","run":"`+stoppedRun+`","decision":"rescope","reason":"the reviewer refused the migration, which was never in this item's criteria"}`)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.70": {ID: "yoyodyne-ifd.70", Title: "the item that stopped", Status: "open"},
	}}
	reply := triageReply(t, tracker, nil, answer)

	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if len(tracker.updates) != 1 || tracker.updates[0].id != "yoyodyne-ifd.70" {
		t.Fatalf("updates = %#v", tracker.updates)
	}
	notes := tracker.updates[0].change.AppendNotes
	for _, want := range []string{
		"Triaged: re-scoped, with what was out of scope split out",
		"on the stopped work of run " + stoppedRun,
		"by the development manager in conversation",
		"the reviewer refused the migration",
	} {
		if !strings.Contains(notes, want) {
			t.Fatalf("the item's triage note is missing %q:\n%s", want, notes)
		}
	}
	// Re-scoping buys no further attempt at the work, so nothing was blocked and
	// nothing was spent.
	if len(tracker.blocked) != 0 {
		t.Fatalf("a re-scope blocked the item: %#v", tracker.blocked)
	}
	rendered := renderTrackerOutcomes(domain.RoleDevelopmentManager, reply.Actions)
	if !strings.Contains(rendered, `triaged yoyodyne-ifd.70 as "rescope"`) {
		t.Fatalf("the operator was not told what was decided:\n%s", rendered)
	}
}

// The note on the item is for people; the durable record is what the harness
// acts on. A decision that lived only in prose is why the verb that carries one
// out had to be handed the reasoning again as words on a command line, and
// record them as the development manager's — so what lands here is the decision,
// the stoppage it settles, the reasoning, and the turn it was recorded on.
func TestATriageDecisionIsRecordedDurablyWhereTheHarnessReadsIt(t *testing.T) {
	t.Parallel()

	const reasoning = "the change is correct and its base moved under it, so nothing here needs repairing"
	budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
	answer := trackerReply("Decided.",
		`{"action":"triage","id":"yoyodyne-ifd.311","run":"`+stoppedRun+`","decision":"rerun","reason":"`+reasoning+`"}`)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.311": {ID: "yoyodyne-ifd.311", Title: "the item that stopped", Status: "open"},
	}}
	reply := triageReply(t, tracker, budgets, answer)
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}

	counters := budgets.counters(t, "yoyodyne-ifd.311")
	decided, found := counters.DecisionOf(stoppedRun)
	if !found {
		t.Fatalf("counters = %#v, want the decision standing about the stoppage it names", counters)
	}
	if decided.Decision != runstate.TriageDecisionRerun || decided.Reason != reasoning {
		t.Fatalf("decision = %#v, want the re-run and the reasoning it was recorded with", decided)
	}
	// The role and the turn are the conversation's own facts, and they are what an
	// attribution built from this decision cites.
	if decided.DecidedBy != RoleTitle(domain.RoleDevelopmentManager) {
		t.Fatalf("decision = %#v, want the role that recorded it", decided)
	}
	if strings.TrimSpace(decided.Conversation) == "" || decided.Turn < 1 {
		t.Fatalf("decision = %#v, want the conversation and turn it was recorded on", decided)
	}
	// The spend and the decision are one write, so the budget moved with it.
	if counters.Reruns != 1 {
		t.Fatalf("counters = %#v, want the decision's spend recorded beside it", counters)
	}
}

// A decision that spends nothing is recorded just as durably. It buys no
// attempt, so no budget moves — but the next reader of the stoppage, and the
// lifecycle that will one day close its docket entry, need to know somebody
// looked and what they concluded.
func TestADecisionThatSpendsNothingIsStillRecordedDurably(t *testing.T) {
	t.Parallel()

	budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
	answer := trackerReply("Decided.",
		`{"action":"triage","id":"yoyodyne-ifd.311","run":"`+stoppedRun+`","decision":"wait","reason":"the forge still has the merge, so waiting is what it needs"}`)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.311": {ID: "yoyodyne-ifd.311", Title: "the item that stopped", Status: "open"},
	}}
	if reply := triageReply(t, tracker, budgets, answer); len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}

	counters := budgets.counters(t, "yoyodyne-ifd.311")
	decided, found := counters.DecisionOf(stoppedRun)
	if !found || decided.Decision != runstate.TriageDecisionWait {
		t.Fatalf("counters = %#v, want the wait recorded about the stoppage", counters)
	}
	if counters.Passes() != 0 {
		t.Fatalf("counters = %#v, want a decision that buys no attempt to spend nothing", counters)
	}
}

// Escalating is the decision that reaches a person, and it is two things at
// once: the item says durably that it is waiting on somebody, and the report
// reaches the pile the operator reads. Either alone is how stopped work went
// unnoticed before this workflow existed.
func TestAnEscalationBlocksTheItemAndReachesTheOperator(t *testing.T) {
	t.Parallel()

	answer := reportReply(
		trackerReply("This one is upstream of the change.",
			`{"action":"triage","id":"yoyodyne-ifd.1.9","run":"`+stoppedRun+`","decision":"escalate","reason":"the findings dispute the acceptance criteria, and another attempt loses the same argument"}`),
		`{"severity":"warning","message":"yoyodyne-ifd.1.9 has been round triage twice; its criteria are disputed rather than its change."}`,
	)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.1.9": {ID: "yoyodyne-ifd.1.9", Title: "the item that keeps coming back", Status: "open"},
	}}
	reports := &fakeReports{}
	reply := triageReplyWithReports(t, tracker, nil, reports, answer)

	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if len(tracker.blocked) != 1 || tracker.blocked[0][0] != "yoyodyne-ifd.1.9" {
		t.Fatalf("blocked items = %#v", tracker.blocked)
	}
	if !strings.Contains(tracker.blocked[0][1], "Escalated to the operator by triage") ||
		!strings.Contains(tracker.blocked[0][1], "on the stopped work of run "+stoppedRun) {
		t.Fatalf("the blocker does not say what it was:\n%s", tracker.blocked[0][1])
	}
	// An escalation is a blocker rather than a note, because a note leaves the
	// item looking like work still in flight.
	if len(tracker.updates) != 0 {
		t.Fatalf("an escalation was recorded as an ordinary note: %#v", tracker.updates)
	}
	if len(reports.appended) != 1 || reports.appended[0].Severity != report.SeverityWarning {
		t.Fatalf("collected reports = %#v", reports.appended)
	}
}

// An escalation nobody is told about is not an escalation. The harness refuses
// it whole rather than blocking the item and leaving the operator to find it,
// which is exactly the failure this workflow replaces.
func TestAnEscalationWithNoReportIsRefusedAndChangesNothing(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name  string
		reply string
	}{
		{
			name: "no report at all",
			reply: trackerReply("Somebody should look at this.",
				`{"action":"triage","id":"yoyodyne-ifd.90","run":"`+stoppedRun+`","decision":"escalate","reason":"it needs a branch protection changed"}`),
		},
		{
			name: "a report that asks for nothing",
			reply: reportReply(
				trackerReply("Somebody should look at this.",
					`{"action":"triage","id":"yoyodyne-ifd.90","run":"`+stoppedRun+`","decision":"escalate","reason":"it needs a branch protection changed"}`),
				`{"severity":"note","message":"the forge dropped the merge."}`),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			tracker := &fakeTracker{items: map[string]beads.WorkItem{
				"yoyodyne-ifd.90": {ID: "yoyodyne-ifd.90", Title: "the stuck publication", Status: "open"},
			}}
			options := triageOptions(t, tracker, nil, testCase.reply)
			options.Reports = &fakeReports{}
			_, err := openTestSession(t, options).Send(context.Background(), "Work the docket.")
			escalation := &EscalationError{}
			if !errors.As(err, &escalation) {
				t.Fatalf("Send() error = %v, want an escalation refusal", err)
			}
			if escalation.WorkItemID != "yoyodyne-ifd.90" {
				t.Fatalf("the refusal names %q", escalation.WorkItemID)
			}
			if len(tracker.blocked) != 0 || len(tracker.updates) != 0 {
				t.Fatalf("a refused escalation changed the tracker: blocked %#v, updates %#v", tracker.blocked, tracker.updates)
			}
		})
	}
}

// The three decisions that buy another attempt at work that already failed go
// through the durable budget, and the budget is what stops an item nothing else
// was going to. A grant is cut to what the cap has room for, and the next one is
// refused outright — with the refusal naming the cap, which is the development
// manager's evidence for escalating instead.
func TestARepairGrantSpendsTheDurableBudgetAndIsRefusedOnceItIsGone(t *testing.T) {
	t.Parallel()

	budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 2, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
	// One round is already spent by the run that stopped, so a grant of two is
	// cut to the one the cap still has room for.
	if _, err := budgets.store.RecordReviewRound(context.Background(), "yoyodyne-ifd.90", runstate.RoundKey(stoppedRun, 0), countingProcess, budgets.clock.Now()); err != nil {
		t.Fatalf("RecordReviewRound() error = %v", err)
	}
	answer := trackerReply("The findings are actionable.",
		`{"action":"triage","id":"yoyodyne-ifd.90","run":"`+stoppedRun+`","decision":"repair","reason":"every finding names a file and a line, and the branch is preserved"}`)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.90": {ID: "yoyodyne-ifd.90", Title: "the item that stopped", Status: "open"},
	}}
	reply := triageReply(t, tracker, budgets, answer)
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if !strings.Contains(reply.Actions[0].Summary, "the grant was cut from 2 round(s) to the 1") {
		t.Fatalf("the truncation was not reported: %q", reply.Actions[0].Summary)
	}
	counters, err := budgets.store.Counters("yoyodyne-ifd.90")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.RepairGrants != 1 || counters.TruncatedGrants != 1 || counters.GrantedRounds != 1 {
		t.Fatalf("counters after one grant = %#v", counters)
	}

	// Triage grants one repair per item and a second is a person's decision, so
	// the next one is refused whatever the rounds say.
	spent := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.90": {ID: "yoyodyne-ifd.90", Title: "the item that stopped", Status: "open"},
	}}
	refused := triageReply(t, spent, budgets, trackerReply("One more go.",
		`{"action":"triage","id":"yoyodyne-ifd.90","run":"`+stoppedRun+`","decision":"repair","reason":"the findings still look actionable"}`))
	if len(refused.Actions) != 1 || refused.Actions[0].Applied {
		t.Fatalf("actions = %#v", refused.Actions)
	}
	if !strings.Contains(refused.Actions[0].Failure, "repair grant is refused for yoyodyne-ifd.90: 1 of 1 permitted repair grant(s) are spent") {
		t.Fatalf("the refusal does not name the cap: %q", refused.Actions[0].Failure)
	}
	// A refused decision writes nothing: an item that carried a repair note the
	// cap then contradicted would be worse than one carrying no note at all.
	if len(spent.updates) != 0 {
		t.Fatalf("a refused grant wrote on the item: %#v", spent.updates)
	}
}

// A re-run and a merge re-arm each spend a budget of their own, and neither is
// bounded by the review rounds: a re-run buys a whole fresh run and spends no
// round itself, and a re-arm buys no round at all. Both are exercised against
// the real record rather than a stand-in, because what they promise is that the
// second one is refused — and a budget that only ever counted up would look
// exactly the same from the conversation.
//
// The item here has no reviewer verdict against it at all, which is the case the
// shared round budget cannot bound: with only that budget, an item whose runs
// keep stopping before review could be re-run for ever.
func TestARerunAndAMergeRearmSpendTheirOwnBudgets(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		// decision is what the development manager records, summary is what the
		// operator is told it spent, and refusal is what the budget says when the
		// second one asks.
		decision string
		summary  string
		counted  func(runstate.TriageCounters) int
		refusal  string
	}{
		{
			name:     "a re-run",
			decision: "rerun",
			summary:  "1 re-run(s) of it are now recorded",
			counted:  func(counters runstate.TriageCounters) int { return counters.Reruns },
			refusal:  "re-run is refused for yoyodyne-ifd.68.3: 1 of 1 permitted re-run(s) are spent",
		},
		{
			name:     "a merge re-arm",
			decision: "rearm",
			summary:  "1 merge re-arm(s) of publication " + triage.PublicationKey(stoppedRun, 7) + " are now recorded",
			counted:  func(counters runstate.TriageCounters) int { return counters.MergeRearms },
			refusal:  "merge re-arm is refused for publication " + triage.PublicationKey(stoppedRun, 7) + " of yoyodyne-ifd.68.3: 1 of 1 permitted merge re-arm(s) are spent",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
			decided := trackerReply("Decided.",
				`{"action":"triage","id":"yoyodyne-ifd.68.3","run":"`+stoppedRun+`","decision":"`+testCase.decision+`","reason":"the change was right and the ground moved under it"}`)
			first := &fakeTracker{items: map[string]beads.WorkItem{
				"yoyodyne-ifd.68.3": {ID: "yoyodyne-ifd.68.3", Title: "the item that stopped", Status: "open"},
			}}
			reply := triageReply(t, first, budgets, decided)
			if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
				t.Fatalf("actions = %#v", reply.Actions)
			}
			if !strings.Contains(reply.Actions[0].Summary, testCase.summary) {
				t.Fatalf("the summary does not say what was spent: %q", reply.Actions[0].Summary)
			}
			if len(first.updates) != 1 {
				t.Fatalf("updates = %#v", first.updates)
			}
			counters, err := budgets.store.Counters("yoyodyne-ifd.68.3")
			if err != nil {
				t.Fatalf("Counters() error = %v", err)
			}
			if testCase.counted(counters) != 1 {
				t.Fatalf("counters after one %s = %#v", testCase.decision, counters)
			}

			// The budget is now gone — with rounds to spare, which is the point —
			// and the same decision asked for again is refused rather than counted
			// a second time.
			second := &fakeTracker{items: map[string]beads.WorkItem{
				"yoyodyne-ifd.68.3": {ID: "yoyodyne-ifd.68.3", Title: "the item that stopped", Status: "open"},
			}}
			refused := triageReply(t, second, budgets, decided)
			if len(refused.Actions) != 1 || refused.Actions[0].Applied {
				t.Fatalf("actions = %#v", refused.Actions)
			}
			if !strings.Contains(refused.Actions[0].Failure, testCase.refusal) {
				t.Fatalf("the refusal does not name the cap: %q", refused.Actions[0].Failure)
			}
			if len(second.updates) != 0 {
				t.Fatalf("a refused decision wrote on the item: %#v", second.updates)
			}
			after, err := budgets.store.Counters("yoyodyne-ifd.68.3")
			if err != nil {
				t.Fatalf("Counters() error = %v", err)
			}
			if testCase.counted(after) != 1 {
				t.Fatalf("a refused decision moved the counter: %#v", after)
			}
		})
	}
}

// A conversation with no budget wired may still decide anything that spends
// nothing, and may not grant, re-run, or re-arm. An unreadable budget is never
// spent through as though it were empty.
func TestTheDecisionsThatSpendABudgetAreRefusedWithoutOne(t *testing.T) {
	t.Parallel()

	for decision, wantApplied := range map[string]bool{
		"repair":   false,
		"rerun":    false,
		"rearm":    false,
		"rescope":  true,
		"wait":     true,
		"escalate": true,
		// Crossing a cap spends none of the budgets, but it writes to the same
		// record they are refused against, so a conversation that cannot reach that
		// record cannot cross one either. A crossing recorded nowhere is a cap
		// reported as raised that every guard still refuses.
		"cross": false,
	} {
		t.Run(decision, func(t *testing.T) {
			t.Parallel()

			crossed := ""
			if decision == "cross" {
				crossed = `,"budget":"` + runstate.TriageReviewRoundBudget + `"`
			}
			answer := trackerReply("Decided.",
				`{"action":"triage","id":"yoyodyne-ifd.90","run":"`+stoppedRun+`","decision":"`+decision+`"`+crossed+`,"reason":"the docket entry says so"}`)
			if decision == "escalate" {
				answer = reportReply(answer, `{"severity":"warning","message":"yoyodyne-ifd.90 needs a repository setting only a person can change."}`)
			}
			tracker := &fakeTracker{items: map[string]beads.WorkItem{
				"yoyodyne-ifd.90": {ID: "yoyodyne-ifd.90", Title: "the item that stopped", Status: "open"},
			}}
			reply := triageReply(t, tracker, nil, answer)
			if len(reply.Actions) != 1 || reply.Actions[0].Applied != wantApplied {
				t.Fatalf("%q applied = %#v, want %v", decision, reply.Actions, wantApplied)
			}
			if wantApplied {
				return
			}
			if !strings.Contains(reply.Actions[0].Failure, "no triage budget is wired to this conversation") {
				t.Fatalf("%q was refused for the wrong reason: %q", decision, reply.Actions[0].Failure)
			}
		})
	}
}

// A decision the harness cannot act on is refused before anything in the block
// runs, exactly as any other malformed action is: a decision outside the
// vocabulary, and a stoppage the decision does not name.
func TestATriageDecisionIsRefusedWhenItNamesNoDecisionOrNoStoppage(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		action string
		want   string
	}{
		{
			name:   "a decision outside the vocabulary",
			action: `{"action":"triage","id":"yoyodyne-ifd.90","run":"` + stoppedRun + `","decision":"abandon","reason":"why"}`,
			want:   `triage decision "abandon" is not a decision`,
		},
		{
			name:   "no decision at all",
			action: `{"action":"triage","id":"yoyodyne-ifd.90","run":"` + stoppedRun + `","reason":"why"}`,
			want:   `triage requires "decision"`,
		},
		{
			// Waiting and escalating may name no run, for an attempt that never became
			// one; every decision that acts on a run needs the run it acts on.
			name:   "no run",
			action: `{"action":"triage","id":"yoyodyne-ifd.90","decision":"rerun","reason":"why"}`,
			want:   `triage requires "run"`,
		},
		{
			name:   "a run identifier no run could have",
			action: `{"action":"triage","id":"yoyodyne-ifd.90","run":"the last one","decision":"wait","reason":"why"}`,
			want:   `is not a run identifier`,
		},
		{
			// The reasoning is what the decision is: it is recorded durably and a
			// carry-out records it as why the run it starts exists, so a decision
			// without one leaves the harness nothing to attribute a run to.
			name:   "no reasoning",
			action: `{"action":"triage","id":"yoyodyne-ifd.90","run":"` + stoppedRun + `","decision":"wait"}`,
			want:   `triage requires "reason"`,
		},
		{
			name:   "an argument triage has no use for",
			action: `{"action":"triage","id":"yoyodyne-ifd.90","run":"` + stoppedRun + `","decision":"wait","note":"and this","reason":"why"}`,
			want:   `triage does not take "note"`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			tracker := &fakeTracker{items: map[string]beads.WorkItem{
				"yoyodyne-ifd.90": {ID: "yoyodyne-ifd.90", Title: "the item that stopped", Status: "open"},
			}}
			options := triageOptions(t, tracker, nil, trackerReply("Decided.", testCase.action))
			reply, err := openTestSession(t, options).Send(context.Background(), "Work the docket.")
			if refusal := refusalOf(reply, err); !strings.Contains(refusal, testCase.want) {
				t.Fatalf("Send() refusal = %q, want it to contain %q", refusal, testCase.want)
			}
			if len(tracker.updates) != 0 || len(tracker.blocked) != 0 {
				t.Fatalf("a refused block changed the tracker: updates %#v, blocked %#v", tracker.updates, tracker.blocked)
			}
		})
	}
}

// A decision names two things that have to agree: the item it lands on, and the
// run whose stoppage it settles. A docket of several entries is where they come
// apart — two entries transposed put each decision's reasoning onto the other
// item, and both then read as decided about a change neither is. The run record
// is what settles it, and a decision whose run was made for somebody else's work
// is refused before anything is spent or written.
func TestATriageDecisionIsRefusedWhenItsRunIsAnotherItemsStoppedWork(t *testing.T) {
	t.Parallel()

	runs := fakeStoppedRuns{items: map[string]string{
		stoppedRun:      "yoyodyne-ifd.90",
		otherStoppedRun: "yoyodyne-ifd.70",
	}}
	for _, testCase := range []struct {
		name    string
		run     string
		applied bool
		want    string
	}{
		{
			name:    "the item's own stopped work",
			run:     stoppedRun,
			applied: true,
		},
		{
			name: "another item's stopped work",
			run:  otherStoppedRun,
			want: "was made for yoyodyne-ifd.70 rather than for yoyodyne-ifd.90",
		},
		{
			name: "a run nothing recorded",
			run:  "run-00000000000000000000000000000000",
			want: "could not be read, so nothing says it is yoyodyne-ifd.90's stopped work",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
			tracker := &fakeTracker{items: map[string]beads.WorkItem{
				"yoyodyne-ifd.90": {ID: "yoyodyne-ifd.90", Title: "the item that stopped", Status: "open"},
			}}
			reply := triageReplyAbout(t, tracker, budgets, runs, trackerReply("Decided.",
				`{"action":"triage","id":"yoyodyne-ifd.90","run":"`+testCase.run+`","decision":"repair","reason":"every finding names a file and a line"}`))
			if len(reply.Actions) != 1 || reply.Actions[0].Applied != testCase.applied {
				t.Fatalf("actions = %#v, want applied = %v", reply.Actions, testCase.applied)
			}
			counters, err := budgets.store.Counters("yoyodyne-ifd.90")
			if err != nil {
				t.Fatalf("Counters() error = %v", err)
			}
			if testCase.applied {
				if len(tracker.updates) != 1 || counters.RepairGrants != 1 {
					t.Fatalf("a decision about the item's own run was not carried out: updates %#v, counters %#v",
						tracker.updates, counters)
				}
				return
			}
			if !strings.Contains(reply.Actions[0].Failure, testCase.want) {
				t.Fatalf("the refusal reads = %q", reply.Actions[0].Failure)
			}
			// The check is asked before anything is spent, so a refused decision
			// leaves the item exactly as much budget as it had — and writes no
			// reasoning about somebody else's change onto it.
			if len(tracker.updates) != 0 || len(tracker.blocked) != 0 {
				t.Fatalf("a refused decision changed the tracker: updates %#v, blocked %#v", tracker.updates, tracker.blocked)
			}
			if counters.RepairGrants != 0 {
				t.Fatalf("a refused decision spent the item's budget: %#v", counters)
			}
		})
	}
}

// Triage belongs to the role the docket is delivered to. Every other role is
// refused it, so no persona and no conversation can move where stopped work is
// decided.
func TestOnlyTheDevelopmentManagerMayTriage(t *testing.T) {
	t.Parallel()

	for _, role := range ConversationalRoles() {
		if role == domain.RoleDevelopmentManager {
			continue
		}
		tracker := &fakeTracker{items: map[string]beads.WorkItem{
			"yoyodyne-ifd.90": {ID: "yoyodyne-ifd.90", Title: "the item that stopped", Status: "open"},
		}}
		options := triageOptions(t, tracker, nil, trackerReply("Decided.",
			`{"action":"triage","id":"yoyodyne-ifd.90","run":"`+stoppedRun+`","decision":"wait","reason":"the forge still has it"}`))
		options.Role = role
		options.Agent = string(role)
		_, err := openTestSession(t, options).Send(context.Background(), "Work the docket.")
		authority := &AuthorityError{}
		if !errors.As(err, &authority) {
			t.Fatalf("%s was not refused the triage action: %v", role, err)
		}
		if len(tracker.updates) != 0 || len(tracker.blocked) != 0 {
			t.Fatalf("%s changed the tracker: updates %#v, blocked %#v", role, tracker.updates, tracker.blocked)
		}
	}
}

// Closed work has left the backlog: there is nothing to hand back, nothing to
// run again, and an escalation would put a closed item back into a blocked
// state nobody asked for. The refusal says where a note about it belongs
// instead.
func TestTriageIsRefusedOnWorkThatHasClosed(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.90": {ID: "yoyodyne-ifd.90", Title: "the item that stopped", Status: "closed"},
	}}
	reply := triageReply(t, tracker, nil, trackerReply("Handing it back.",
		`{"action":"triage","id":"yoyodyne-ifd.90","run":"`+stoppedRun+`","decision":"rescope","reason":"half of it was out of scope"}`))
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if !strings.Contains(reply.Actions[0].Failure, "is closed, so what becomes of the work that stopped is no longer a decision") {
		t.Fatalf("the refusal reads = %q", reply.Actions[0].Failure)
	}
	if len(tracker.updates) != 0 || len(tracker.blocked) != 0 {
		t.Fatalf("a refused decision changed the tracker: updates %#v, blocked %#v", tracker.updates, tracker.blocked)
	}
}

// A cap refusal has to leave the development manager holding something to write.
// Naming the remedy without naming the verb is what sent two overrides into a
// work item's notes — where "record an override against the item" pointed, and
// where no guard reads — and left the resubmitted decision meeting the identical
// refusal.
//
// It offers his own crossing first and the operator's command after it, because
// the crossing is the one that costs nobody a wait: the operator delegated it
// precisely so that the ordinary refusal stops producing an escalation.
func TestACapRefusalNamesTheCommandThatCrossesTheCap(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		err  error
		want []string
	}{
		{
			name: "the refusal a cap produces",
			err: runstate.TriageCapError{
				Action:     runstate.TriageRerun,
				WorkItemID: "yoyodyne-ifd.224",
				Refusals: []runstate.TriageCapRefusal{{
					Budget: runstate.TriageReviewRoundBudget,
					Spent:  4,
					Cap:    4,
				}},
			},
			want: []string{
				"re-run is refused for yoyodyne-ifd.224",
				`yoyo triage override --budget "review round"`,
				// The ceiling is filled in rather than left as a placeholder: the
				// operator types the command rather than reconstructing the figure.
				"--cap 5",
				"yoyodyne-ifd.224",
				"Nothing written into the item's notes crosses that cap",
				// His own crossing, offered before the escalation and with the budget
				// that refused already in it.
				`"decision":"cross"`,
				`"budget":"review round"`,
				"up to 5 times per item",
			},
		},
		{
			// The shape that cost two override ceremonies minutes apart on each of
			// yoyodyne-ifd.272 and yoyodyne-ifd.209.20: both budgets behind one
			// decision are spent, so both commands reach the operator at once.
			name: "the refusal two spent budgets produce",
			err: runstate.TriageCapError{
				Action:     runstate.TriageRepairGrant,
				WorkItemID: "yoyodyne-ifd.272",
				Refusals: []runstate.TriageCapRefusal{
					{Budget: runstate.TriageRepairGrantBudget, Spent: 1, Cap: 1},
					{Budget: runstate.TriageReviewRoundBudget, Spent: 4, Cap: 4},
				},
			},
			want: []string{
				"repair grant is refused for yoyodyne-ifd.272",
				"1 of 1 permitted repair grant(s) are spent",
				"4 of 4 permitted review round(s) are spent",
				`yoyo triage override --budget "repair grant" --cap 2`,
				`yoyo triage override --budget "review round" --cap 5`,
				// One crossing per budget as well, for the same reason: crossing one
				// leaves the same decision refused by the other.
				`"budget":"repair grant"`,
				`"budget":"review round"`,
				"both are needed",
			},
		},
		{
			// Nothing produces this today, and a refusal that lost the detail must
			// still leave the operator holding a verb rather than a description.
			name: "a cap refusal carrying no budget to name",
			err:  fmt.Errorf("the budget is gone: %w", runstate.ErrTriageCapReached),
			want: []string{
				`yoyo triage override --budget "<budget>"`,
				`"decision":"cross"`,
				"Nothing written into the item's notes crosses that cap",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			refusal := refusedPastCap(testCase.err)
			if !errors.Is(refusal, runstate.ErrTriageCapReached) {
				t.Fatalf("the refusal stopped being a cap refusal: %v", refusal)
			}
			for _, want := range testCase.want {
				if !strings.Contains(refusal.Error(), want) {
					t.Fatalf("the refusal is missing %q:\n%s", want, refusal)
				}
			}
		})
	}

	// Everything else is left exactly as it was: a tracker that would not answer
	// is not a cap, and dressing it as one sends the operator to a command that
	// changes nothing.
	other := errors.New("the tracker could not be reached")
	if refusedPastCap(other) != other {
		t.Fatalf("a failure that is not a cap refusal was rewritten: %v", refusedPastCap(other))
	}
}

// The failure this reporting exists for, replayed. A triage decision spends the
// item's durable budget and then writes the decision onto the item, and on
// 2026-09-06 the second of those timed out on yoyodyne-ifd.142: the result said
// "failed, and changed nothing: bd update timed_out" while the re-run it had
// already spent was durable, and the duplicate that invited was stopped by the
// cap rather than by anything the development manager was told. So the account
// names the spend, and never claims nothing changed.
func TestATimedOutTriageWriteNamesTheSpendItAlreadyMade(t *testing.T) {
	t.Parallel()

	budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
	answer := trackerReply("Its ground moved, so it starts over.",
		`{"action":"triage","id":"yoyodyne-ifd.142","run":"`+stoppedRun+`","decision":"rerun","reason":"the change is right and the branch it was written against has moved under it"}`)
	tracker := &fakeTracker{
		items: map[string]beads.WorkItem{
			"yoyodyne-ifd.142": {ID: "yoyodyne-ifd.142", Title: "the item whose re-run was reported as unrecorded", Status: "open"},
		},
		// bd took the write and the harness stopped waiting on it, which is the
		// whole of what a timeout says: the command failed, and what it was
		// carrying may well be in the store.
		durableErr: errors.New("bd update failed with status timed_out and exit code -1"),
	}
	reply := triageReply(t, tracker, budgets, answer)

	if len(reply.Actions) != 1 {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	outcome := reply.Actions[0]
	// Nothing reports it as applied — the write it was told failed — and nothing
	// reports it as having changed nothing either.
	if outcome.Applied || !outcome.PartlyLanded() {
		t.Fatalf("outcome = %#v, want a failure with the spend standing behind it", outcome)
	}
	rendered := renderTrackerOutcomes(domain.RoleDevelopmentManager, reply.Actions)
	// The results carry the standing contract, which says what "changed nothing"
	// means; what must not say it is the line about this action.
	results := strings.ReplaceAll(renderTrackerResults(reply.Actions), trackerResultsPreamble, "")
	for _, account := range []string{rendered, results} {
		if strings.Contains(account, "changed nothing") {
			t.Fatalf("a durable spend was reported as having changed nothing:\n%s", account)
		}
		for _, want := range []string{
			"did not finish, and part of it stands",
			"the re-run is spent against yoyodyne-ifd.142's durable budget: 1 re-run(s) of it are now recorded",
			"carries it, so the write landed",
		} {
			if !strings.Contains(account, want) {
				t.Fatalf("the account is missing %q:\n%s", want, account)
			}
		}
	}

	// And the spend really was durable, which is how the shape was found: the
	// same decision asked for again meets the cap rather than being recorded.
	again := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.142": {ID: "yoyodyne-ifd.142", Title: "the item whose re-run was reported as unrecorded", Status: "open"},
	}}
	refused := triageReply(t, again, budgets, trackerReply("Then start it over.",
		`{"action":"triage","id":"yoyodyne-ifd.142","run":"`+stoppedRun+`","decision":"rerun","reason":"the change is right and its ground has moved"}`))
	if len(refused.Actions) != 1 || refused.Actions[0].Applied {
		t.Fatalf("actions = %#v", refused.Actions)
	}
	if !strings.Contains(refused.Actions[0].Failure, "1 of 1 permitted re-run(s) are spent") {
		t.Fatalf("the retry was not refused by the spend the first attempt made: %q", refused.Actions[0].Failure)
	}
}

// The other half of the same answer. A write the tracker refused outright landed
// nothing, and saying so is worth as much as naming what did: the decision has to
// be recorded before anything reads the item as decided, while the spend behind
// it must not be made again.
func TestATriageWriteThatLandedNothingSaysSoBesideTheSpendThatStands(t *testing.T) {
	t.Parallel()

	budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
	answer := trackerReply("Its ground moved, so it starts over.",
		`{"action":"triage","id":"yoyodyne-ifd.142","run":"`+stoppedRun+`","decision":"rerun","reason":"the change is right and the branch it was written against has moved under it"}`)
	tracker := &fakeTracker{
		items: map[string]beads.WorkItem{
			"yoyodyne-ifd.142": {ID: "yoyodyne-ifd.142", Title: "the item whose re-run was reported as unrecorded", Status: "open"},
		},
		err: errors.New("bd update failed with status failed and exit code 1: the item is locked"),
	}
	reply := triageReply(t, tracker, budgets, answer)

	if len(reply.Actions) != 1 {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	outcome := reply.Actions[0]
	if outcome.Applied || !outcome.PartlyLanded() {
		t.Fatalf("outcome = %#v, want a failure with the spend standing behind it", outcome)
	}
	rendered := renderTrackerOutcomes(domain.RoleDevelopmentManager, reply.Actions)
	for _, want := range []string{
		"landed, and is not to be done again: the re-run is spent against yoyodyne-ifd.142's durable budget",
		"not known to have landed: the decision recorded on the item",
		"does not find it",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the account is missing %q:\n%s", want, rendered)
		}
	}
}

// An escalation is not a note, so what settles a failed one is not a note
// either. Blocking is one bd invocation that sets the status and appends the
// reason, and a block the store kept leaves both marks — so a reading that finds
// either says the write landed, and the escalation is not to be made a second
// time. The second case is the one that separates them: an item that comes back
// blocked with nothing legible in its notes is still an item somebody is waiting
// on.
func TestADurableEscalationIsReportedAsLandedHoweverItsBlockWasReported(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name      string
		losesNote bool
	}{
		{name: "the block leaves both marks"},
		{name: "only the status is legible afterwards", losesNote: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			answer := reportReply(
				trackerReply("This one is upstream of the change.",
					`{"action":"triage","id":"yoyodyne-ifd.142","run":"`+stoppedRun+`","decision":"escalate","reason":"the findings dispute the acceptance criteria, and another attempt loses the same argument"}`),
				`{"severity":"warning","message":"yoyodyne-ifd.142 has been round triage twice; its criteria are disputed rather than its change."}`,
			)
			tracker := &fakeTracker{
				items: map[string]beads.WorkItem{
					"yoyodyne-ifd.142": {ID: "yoyodyne-ifd.142", Title: "the item that keeps coming back", Status: "open"},
				},
				durableErr:     errors.New("bd update failed with status timed_out and exit code -1"),
				blockLosesNote: testCase.losesNote,
			}
			reply := triageReplyWithReports(t, tracker, nil, &fakeReports{}, answer)

			if len(reply.Actions) != 1 {
				t.Fatalf("actions = %#v", reply.Actions)
			}
			outcome := reply.Actions[0]
			if outcome.Applied || !outcome.PartlyLanded() {
				t.Fatalf("outcome = %#v, want a failure with the blocker standing behind it", outcome)
			}
			rendered := renderTrackerOutcomes(domain.RoleDevelopmentManager, reply.Actions)
			if strings.Contains(rendered, "changed nothing") {
				t.Fatalf("a durable blocker was reported as having changed nothing:\n%s", rendered)
			}
			for _, want := range []string{
				"did not finish, and part of it stands",
				"landed, and is not to be done again: the blocker naming the operator",
				"escalating again would block it twice over",
			} {
				if !strings.Contains(rendered, want) {
					t.Fatalf("the account is missing %q:\n%s", want, rendered)
				}
			}
		})
	}
}

// The status is only this escalation's evidence where the item was not already
// blocked when the action started. An item somebody blocked earlier is blocked
// now for a reason that is not this decision, so a failed block on one of those
// settles nothing — reading it as landed would report an escalation nobody made
// and leave a person nobody told.
func TestABlockedItemsPriorStateIsNotReadAsThisEscalationLanding(t *testing.T) {
	t.Parallel()

	answer := reportReply(
		trackerReply("This one is upstream of the change.",
			`{"action":"triage","id":"yoyodyne-ifd.142","run":"`+stoppedRun+`","decision":"escalate","reason":"the findings dispute the acceptance criteria, and another attempt loses the same argument"}`),
		`{"severity":"warning","message":"yoyodyne-ifd.142 has been round triage twice; its criteria are disputed rather than its change."}`,
	)
	tracker := &fakeTracker{
		items: map[string]beads.WorkItem{
			// Already blocked before this decision was asked for, and the write that
			// would have blocked it again was refused outright.
			"yoyodyne-ifd.142": {ID: "yoyodyne-ifd.142", Title: "the item that keeps coming back", Status: "blocked"},
		},
		err: errors.New("bd update failed with status timed_out and exit code -1"),
	}
	reply := triageReplyWithReports(t, tracker, nil, &fakeReports{}, answer)

	if len(reply.Actions) != 1 {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	outcome := reply.Actions[0]
	if outcome.Applied || outcome.PartlyLanded() || len(outcome.Unknown) != 1 {
		t.Fatalf("outcome = %#v, want a failure that settled nothing from a blocker it did not place", outcome)
	}
	rendered := renderTrackerOutcomes(domain.RoleDevelopmentManager, reply.Actions)
	if !strings.Contains(rendered, "not known to have landed: the blocker naming the operator") {
		t.Fatalf("the account does not leave the blocker unsettled:\n%s", rendered)
	}
}

// A decision that spends nothing can still have its one write fail, and then
// there is no spend to name and nothing that says the write landed. That is not
// "changed nothing": the harness not knowing and nothing having happened are
// different claims, and only the second frees the action to be asked for again.
func TestAnUnconfirmedTriageWriteSaysItDidNotFinishRatherThanThatItFailed(t *testing.T) {
	t.Parallel()

	answer := reportReply(
		trackerReply("This one is upstream of the change.",
			`{"action":"triage","id":"yoyodyne-ifd.142","run":"`+stoppedRun+`","decision":"escalate","reason":"the findings dispute the acceptance criteria, and another attempt loses the same argument"}`),
		`{"severity":"warning","message":"yoyodyne-ifd.142 has been round triage twice; its criteria are disputed rather than its change."}`,
	)
	tracker := &fakeTracker{
		items: map[string]beads.WorkItem{
			"yoyodyne-ifd.142": {ID: "yoyodyne-ifd.142", Title: "the item that keeps coming back", Status: "open"},
		},
		// The write was refused rather than taken, so reading the item back finds
		// no blocker — which settles nothing about a write that timed out and is
		// all this can honestly say about one that did not.
		err: errors.New("bd update failed with status timed_out and exit code -1"),
	}
	reply := triageReplyWithReports(t, tracker, nil, &fakeReports{}, answer)

	if len(reply.Actions) != 1 {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	outcome := reply.Actions[0]
	// An escalation spends no budget, so there is nothing standing behind this
	// failure — only something nothing can answer about.
	if outcome.Applied || outcome.PartlyLanded() || len(outcome.Unknown) != 1 {
		t.Fatalf("outcome = %#v, want a failure with nothing landed and one thing unsettled", outcome)
	}
	rendered := renderTrackerOutcomes(domain.RoleDevelopmentManager, reply.Actions)
	results := strings.ReplaceAll(renderTrackerResults(reply.Actions), trackerResultsPreamble, "")
	for _, account := range []string{rendered, results} {
		if strings.Contains(account, "changed nothing") {
			t.Fatalf("an unsettled write was reported as having changed nothing:\n%s", account)
		}
		for _, want := range []string{
			"did not finish, and what it may have changed is not settled",
			"not known to have landed: the blocker naming the operator",
		} {
			if !strings.Contains(account, want) {
				t.Fatalf("the account is missing %q:\n%s", want, account)
			}
		}
	}
}

// A decision that was recorded whole says what it did and nothing else. The
// spend is noted on the outcome before the write that could fail, so a write
// that did not fail has to take the note back with it: an account of what a
// failure left behind, printed under an action that succeeded, reports work that
// finished as work that half did.
func TestARecordedDecisionCarriesNoAccountOfWhatAFailureWouldHaveLeft(t *testing.T) {
	t.Parallel()

	budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
	answer := trackerReply("Its ground moved, so it starts over.",
		`{"action":"triage","id":"yoyodyne-ifd.142","run":"`+stoppedRun+`","decision":"rerun","reason":"the change is right and the branch it was written against has moved under it"}`)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.142": {ID: "yoyodyne-ifd.142", Title: "the item that stopped", Status: "open"},
	}}
	reply := triageReply(t, tracker, budgets, answer)

	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	outcome := reply.Actions[0]
	if len(outcome.Landed) != 0 || len(outcome.Unknown) != 0 {
		t.Fatalf("outcome = %#v, want nothing left over from the failure that did not happen", outcome)
	}
	rendered := renderTrackerOutcomes(domain.RoleDevelopmentManager, reply.Actions)
	// The standing contract explains the words; what must carry none of them is
	// the line about this action.
	results := strings.ReplaceAll(renderTrackerResults(reply.Actions), trackerResultsPreamble, "")
	for _, account := range []string{rendered, results} {
		for _, unwanted := range []string{"landed, and is not to be done again", "not known to have landed", "did not finish"} {
			if strings.Contains(account, unwanted) {
				t.Fatalf("a recorded decision was reported with %q in it:\n%s", unwanted, account)
			}
		}
		if !strings.Contains(account, "1 re-run(s) of it are now recorded") {
			t.Fatalf("the spend was not reported where it belongs, in the summary:\n%s", account)
		}
	}
}

// A decision refused before anything was spent still says it changed nothing,
// because that is what happened. The new answer is for a failure with something
// behind it, and widening it to every failure would cost the sentence the
// meaning it is being kept for.
func TestADecisionRefusedBeforeAnythingIsSpentStillReportsChangingNothing(t *testing.T) {
	t.Parallel()

	answer := trackerReply("This one goes back for a repair.",
		`{"action":"triage","id":"yoyodyne-ifd.142","run":"`+stoppedRun+`","decision":"repair","reason":"the findings each name a file and a line"}`)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.142": {ID: "yoyodyne-ifd.142", Title: "the item that stopped", Status: "open"},
	}}
	// The run the decision names is another item's stopped work, which is refused
	// before any budget is read and before anything is written.
	stoppages := fakeStoppedRuns{items: map[string]string{stoppedRun: "yoyodyne-ifd.9"}}
	reply := triageReplyAbout(t, tracker, nil, stoppages, answer)

	if len(reply.Actions) != 1 || reply.Actions[0].Applied || reply.Actions[0].PartlyLanded() {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	rendered := renderTrackerOutcomes(domain.RoleDevelopmentManager, reply.Actions)
	if !strings.Contains(rendered, "failed, and changed nothing") {
		t.Fatalf("a refusal that changed nothing stopped saying so:\n%s", rendered)
	}
	if strings.Contains(rendered, "landed") {
		t.Fatalf("a refusal that changed nothing reported something as landed:\n%s", rendered)
	}
}

// triageOptions is a development manager's conversation answering with one
// reply and then closing off, which is the shape every test here needs.
func triageOptions(t *testing.T, tracker Tracker, budgets TriageBudgets, answer string) Options {
	t.Helper()

	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: answer},
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: "That is what I decided."},
	}})
	options.Role = domain.RoleDevelopmentManager
	options.Agent = string(domain.RoleDevelopmentManager)
	options.Tracker = tracker
	if budgets != nil {
		options.Triage = budgets
	}
	return options
}

func triageReply(t *testing.T, tracker Tracker, budgets TriageBudgets, answer string) Reply {
	t.Helper()

	return triageReplyWithReports(t, tracker, budgets, &fakeReports{}, answer)
}

func triageReplyWithReports(t *testing.T, tracker Tracker, budgets TriageBudgets, reports Reports, answer string) Reply {
	t.Helper()

	options := triageOptions(t, tracker, budgets, answer)
	options.Reports = reports
	return triageSend(t, options)
}

// triageReplyAbout is the same conversation with the run records wired, which is
// how the command line wires a development manager's: the decision is checked
// against what the harness recorded about the run it names.
func triageReplyAbout(t *testing.T, tracker Tracker, budgets TriageBudgets, stoppages Stoppages, answer string) Reply {
	t.Helper()

	options := triageOptions(t, tracker, budgets, answer)
	options.Reports = &fakeReports{}
	options.Stoppages = stoppages
	return triageSend(t, options)
}

func triageSend(t *testing.T, options Options) Reply {
	t.Helper()

	reply, err := openTestSession(t, options).Send(context.Background(), "Work the docket.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	return reply
}

// triageReplyClosing is the same conversation with the docket wired, which is
// how the command line wires a development manager's: a decision settles the
// entry it was made about, so the stoppage stops being put to her.
func triageReplyClosing(t *testing.T, tracker Tracker, docket TriageEntries, answer string) Reply {
	t.Helper()

	options := triageOptions(t, tracker, nil, answer)
	options.Reports = &fakeReports{}
	options.Docket = docket
	return triageSend(t, options)
}

// fakeDocket is the durable docket as a decision reaches it: what was closed,
// and what refuses to close.
type fakeDocket struct {
	closed []DocketClosure
	// closes is how many entries each call reports settling, and err is a docket
	// that could not be written — the state where the decision stands and the
	// entry does not.
	closes int
	err    error
}

// Close reports what it closed alongside what failed, as the docket itself does:
// one run can carry two entries, and a decision that settled one of them and
// could not settle the other has done half of what it was going to.
func (d *fakeDocket) Close(_ context.Context, closure DocketClosure) (int, error) {
	d.closed = append(d.closed, closure)
	return d.closes, d.err
}

// The other half of the docket's lifecycle: an entry is created where work
// stops, and a decision closes it. Without this the docket is rebuilt from the
// same durable records at every scan and the same settled stoppage comes back
// for ever — and a re-scope spends no counter, so nothing else the harness reads
// says she looked at it.
func TestATriageDecisionClosesTheDocketEntryItSettled(t *testing.T) {
	t.Parallel()

	answer := trackerReply("The refused half is its own item now.",
		`{"action":"triage","id":"yoyodyne-ifd.70","run":"`+stoppedRun+`","decision":"rescope","reason":"the reviewer refused the migration"}`)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.70": {ID: "yoyodyne-ifd.70", Title: "the item that stopped", Status: "open"},
	}}
	docket := &fakeDocket{closes: 1}
	reply := triageReplyClosing(t, tracker, docket, answer)

	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if len(docket.closed) != 1 {
		t.Fatalf("closed = %#v, want the entry this decision settled", docket.closed)
	}
	closure := docket.closed[0]
	if closure.RunID != stoppedRun || closure.Decision != "rescope" {
		t.Fatalf("closure = %#v, want the stoppage and the decision it was settled with", closure)
	}
	// A re-scope answers the run rather than a publication nobody merged, and
	// closing the wrong one would take a live question off the docket. The run is
	// every entry it can be docketed as under its own identifier: what it stopped
	// on, that it died before claiming, or the escalation a role raised from it.
	if slices.Contains(closure.Classes, triage.ClassPublication) ||
		!slices.Contains(closure.Classes, triage.ClassStoppedRun) ||
		!slices.Contains(closure.Classes, triage.ClassUnstartedRun) ||
		!slices.Contains(closure.Classes, triage.ClassEscalation) {
		t.Fatalf("classes = %#v, want the run's own entries and not its publication", closure.Classes)
	}
	if !strings.Contains(closure.Reason, "refused the migration") || !strings.Contains(closure.DecidedBy, "development manager") {
		t.Fatalf("closure = %#v, want the reasoning and who decided it", closure)
	}
	if rendered := renderTrackerOutcomes(domain.RoleDevelopmentManager, reply.Actions); !strings.Contains(rendered, "docket entry(s) of that run are closed") {
		t.Fatalf("the operator was not told the entry was closed:\n%s", rendered)
	}
}

// A wait answers any entry a run has — the publication the forge has not
// finished, and the run's own stoppage, where on 2026-10-01 eleven waits closed
// nothing — and an escalation hands the whole run to the operator. Which entries a decision
// closes is what keeps a live question on the docket while a settled one leaves.
func TestWhichEntriesADecisionClosesFollowsWhatItAnswers(t *testing.T) {
	t.Parallel()

	for _, decided := range []struct {
		decision string
		classes  []triage.Class
	}{
		{decision: "wait", classes: []triage.Class{triage.ClassStoppedRun, triage.ClassUnstartedRun, triage.ClassEscalation, triage.ClassPublication}},
		{decision: "escalate", classes: []triage.Class{triage.ClassStoppedRun, triage.ClassUnstartedRun, triage.ClassEscalation, triage.ClassPublication}},
	} {
		t.Run(decided.decision, func(t *testing.T) {
			t.Parallel()

			answer := trackerReply("Decided.",
				`{"action":"triage","id":"yoyodyne-ifd.70","run":"`+stoppedRun+`","decision":"`+decided.decision+`","reason":"the forge still has it"}`)
			if decided.decision == "escalate" {
				answer = reportReply(answer,
					`{"severity":"warning","message":"yoyodyne-ifd.70 needs a branch protection changed."}`)
			}
			tracker := &fakeTracker{items: map[string]beads.WorkItem{
				"yoyodyne-ifd.70": {ID: "yoyodyne-ifd.70", Title: "the item that stopped", Status: "open"},
			}}
			docket := &fakeDocket{closes: len(decided.classes)}
			reply := triageReplyClosing(t, tracker, docket, answer)

			if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
				t.Fatalf("actions = %#v", reply.Actions)
			}
			if len(docket.closed) != 1 {
				t.Fatalf("closed = %#v, want one closure", docket.closed)
			}
			if fmt.Sprint(docket.closed[0].Classes) != fmt.Sprint(decided.classes) {
				t.Fatalf("classes = %#v, want %#v", docket.closed[0].Classes, decided.classes)
			}
			// Waiting is the one decision that holds for a while rather than
			// settling anything: a merge the forge still has is a merge somebody
			// looks at again. How long is the harness's, so what travels from here
			// is only that it lapses.
			if wants := decided.decision == "wait"; docket.closed[0].Revisit != wants {
				t.Fatalf("revisit = %t on %q, want %t", docket.closed[0].Revisit, decided.decision, wants)
			}
		})
	}
}

// One run can carry two entries, so a decision can close one of them and fail on
// the other. What was closed is said beside what failed: a reader told only about
// the failure would go looking for the entry that is no longer there.
func TestADecisionSaysBothWhatItClosedAndWhatItCouldNot(t *testing.T) {
	t.Parallel()

	answer := trackerReply("Decided.",
		`{"action":"triage","id":"yoyodyne-ifd.70","run":"`+stoppedRun+`","decision":"rescope","reason":"the reviewer refused the migration"}`)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.70": {ID: "yoyodyne-ifd.70", Title: "the item that stopped", Status: "open"},
	}}
	docket := &fakeDocket{closes: 1, err: errors.New("the docket is unwritable")}
	reply := triageReplyClosing(t, tracker, docket, answer)

	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the decision recorded", reply.Actions)
	}
	rendered := renderTrackerOutcomes(domain.RoleDevelopmentManager, reply.Actions)
	if !strings.Contains(rendered, "1 docket entry(s) of that run are closed") {
		t.Fatalf("what was closed was not said:\n%s", rendered)
	}
	if !strings.Contains(rendered, "could not be closed and will be put to you again") {
		t.Fatalf("what failed was not said:\n%s", rendered)
	}
}

// A docket that could not be written leaves the decision recorded and the entry
// standing, which is what every entry did before closing existed. Failing the
// action instead would report a recorded decision as though nothing had
// happened, and invite exactly the second decision this prevents.
func TestADocketEntryThatCouldNotBeClosedIsSaidRatherThanFailingTheDecision(t *testing.T) {
	t.Parallel()

	answer := trackerReply("Decided.",
		`{"action":"triage","id":"yoyodyne-ifd.70","run":"`+stoppedRun+`","decision":"rescope","reason":"the reviewer refused the migration"}`)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.70": {ID: "yoyodyne-ifd.70", Title: "the item that stopped", Status: "open"},
	}}
	docket := &fakeDocket{err: errors.New("the docket is unwritable")}
	reply := triageReplyClosing(t, tracker, docket, answer)

	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the decision recorded", reply.Actions)
	}
	if len(tracker.updates) != 1 {
		t.Fatalf("updates = %#v, want the decision on the item", tracker.updates)
	}
	rendered := renderTrackerOutcomes(domain.RoleDevelopmentManager, reply.Actions)
	if !strings.Contains(rendered, "could not be closed and will be put to you again") {
		t.Fatalf("the standing entry was not said out loud:\n%s", rendered)
	}
}

// A conversation with no docket wired records the decision and closes nothing,
// rather than failing a decision it was asked to make.
func TestADecisionWithoutADocketIsStillRecorded(t *testing.T) {
	t.Parallel()

	answer := trackerReply("Decided.",
		`{"action":"triage","id":"yoyodyne-ifd.70","run":"`+stoppedRun+`","decision":"rescope","reason":"the reviewer refused the migration"}`)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.70": {ID: "yoyodyne-ifd.70", Title: "the item that stopped", Status: "open"},
	}}
	reply := triageReply(t, tracker, nil, answer)

	if len(reply.Actions) != 1 || !reply.Actions[0].Applied || len(tracker.updates) != 1 {
		t.Fatalf("actions = %#v, updates = %#v", reply.Actions, tracker.updates)
	}
}

// fakeStoppedRuns is the harness's record of which run was made for which work
// item, which is what a decision is checked against, and of where each item's
// own change is, which is what a decomposition under it is gated on.
type fakeStoppedRuns struct {
	items map[string]string
	// unlanded is the change each work item's own runs left off the integration
	// target, keyed by item. An item missing from it has nothing unlanded, which
	// is every item in every test that is not about the substrate gate.
	unlanded map[string]UnlandedChange
	// unreadable is the item whose records will not be read, so a gate that
	// cannot establish anything is a state a test can produce.
	unreadable string
	// raised is every run that ended by raising its item as unmeetable. A run
	// missing from it stopped, which is every run in every test that is not about
	// a raise.
	raised map[string]bool
}

func (f fakeStoppedRuns) WorkItemOf(_ context.Context, runID string) (string, error) {
	workItemID, recorded := f.items[runID]
	if !recorded {
		return "", fmt.Errorf("no run %s is recorded", runID)
	}
	return workItemID, nil
}

func (f fakeStoppedRuns) Raised(_ context.Context, runID string) (bool, error) {
	if _, recorded := f.items[runID]; !recorded {
		return false, fmt.Errorf("no run %s is recorded", runID)
	}
	return f.raised[runID], nil
}

func (f fakeStoppedRuns) UnlandedChange(_ context.Context, workItemID string) (UnlandedChange, bool, error) {
	if f.unreadable != "" && f.unreadable == workItemID {
		return UnlandedChange{}, false, fmt.Errorf("the run records of %s could not be read", workItemID)
	}
	unlanded, found := f.unlanded[workItemID]
	return unlanded, found, nil
}

// testTriageBudgets is the real durable budget over a temporary state root,
// wired the way the command line wires it. The gate is what these tests are
// about, so it is the gate itself rather than a stand-in for it.
type triageBudgetGate struct {
	store  *runstate.TriageStore
	caps   runstate.TriageCaps
	rounds int
	clock  fixedClock
}

func newTriageBudgetGate(t *testing.T, caps runstate.TriageCaps, rounds int) *triageBudgetGate {
	t.Helper()

	store, err := runstate.NewTriageStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewTriageStore() error = %v", err)
	}
	return &triageBudgetGate{store: store, caps: caps, rounds: rounds}
}

func (b *triageBudgetGate) GrantRepair(ctx context.Context, workItemID string, decision runstate.TriageDecision) (runstate.RepairGrant, error) {
	return b.store.GrantRepair(ctx, workItemID, decision, b.rounds, b.now(), b.caps)
}

func (b *triageBudgetGate) RecordRerun(ctx context.Context, workItemID string, decision runstate.TriageDecision) (runstate.TriageCounters, error) {
	return b.store.RecordRerun(ctx, workItemID, decision, b.now(), b.caps)
}

func (b *triageBudgetGate) RecordDecision(ctx context.Context, workItemID string, decision runstate.TriageDecision) (runstate.TriageCounters, error) {
	return b.store.RecordDecision(ctx, workItemID, decision, b.now())
}

// counters is what the item's durable record says, which is where a decision is
// read back from: the words a conversation reported are not the record, and the
// whole point of the record is that they are not the same thing.
func (b *triageBudgetGate) counters(t *testing.T, workItemID string) runstate.TriageCounters {
	t.Helper()
	counters, err := b.store.Counters(workItemID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	return counters
}

// RecordMergeRearm keys the budget to the publication the decision's run made,
// the way the command line wires it. The number is fixed here because these tests
// are about the gate rather than about resolving a publication: what matters is
// that one run's decision spends one publication's budget.
func (b *triageBudgetGate) RecordMergeRearm(ctx context.Context, workItemID string, decision runstate.TriageDecision) (runstate.MergeRearmDecision, error) {
	publication := triage.PublicationKey(decision.RunID, 7)
	counters, err := b.store.RecordMergeRearm(ctx, workItemID, publication, decision, b.now(), b.caps)
	if err != nil {
		return runstate.MergeRearmDecision{}, err
	}
	return runstate.MergeRearmDecision{Publication: publication, Counters: counters}, nil
}

func (b *triageBudgetGate) CrossCap(ctx context.Context, workItemID, budget, reason string) (runstate.TriageCrossing, error) {
	return b.store.CrossCap(ctx, workItemID, domain.RoleDevelopmentManager, budget, reason, b.now(), b.caps)
}

func (b *triageBudgetGate) now() time.Time { return b.clock.Now() }

// What the harness writes down after delivering a stoppage is what she actually
// recorded, read from the applied actions rather than from the prose of the
// reply.
func TestAReplySaysWhichStoppageItDecided(t *testing.T) {
	t.Parallel()

	reply := Reply{Actions: []TrackerOutcome{
		{
			// A decision about a different stoppage, which is exactly what a
			// conversation working a docket of several entries produces.
			Applied: true,
			Action:  TrackerAction{Action: actionTriage, ID: "yoyodyne-other", Run: otherStoppedRun, Decision: decisionRerun},
		},
		{
			// A refused decision changed nothing, so nothing here may report it as
			// one.
			Applied: false,
			Action:  TrackerAction{Action: actionTriage, ID: "yoyodyne-task", Run: stoppedRun, Decision: decisionRerun},
		},
		{
			Applied: true,
			Action: TrackerAction{
				Action: actionTriage, ID: "yoyodyne-task", Run: stoppedRun, Decision: decisionRepair,
				Reason: "the findings are narrow and the change is preserved",
			},
		},
	}}

	decision, reason, found := reply.TriageDecision(stoppedRun)
	if !found || decision != decisionRepair || !strings.Contains(reason, "the findings are narrow") {
		t.Fatalf("TriageDecision() = %q, %q, %v, want the applied decision about this stoppage", decision, reason, found)
	}
	if _, _, found := reply.TriageDecision("run-9999999999999999999999999999ffff"); found {
		t.Fatalf("TriageDecision() found a decision about a stoppage nothing in the reply names")
	}
	// A turn that answered and decided nothing is an answer rather than a
	// failure, and reports as one.
	if _, _, found := (Reply{}).TriageDecision(stoppedRun); found {
		t.Fatalf("TriageDecision() found a decision in a reply that recorded none")
	}
}

// The brake's hold is decided from this conversation and acted on by the
// watching session: the decision lands on the hold's own record, names this
// conversation and turn as what decided it, and lifts nothing here. A decision
// aimed at the operator's hold, or at no hold, is refused rather than recorded
// onto a switch she does not hold.
func TestABrakeDecisionIsRecordedOnTheBrakesOwnHold(t *testing.T) {
	t.Parallel()

	trippedAt := fixedClock{}.Now().Add(-5 * time.Minute)
	intake := &fakeIntake{
		held: true, heldAt: trippedAt, heldBy: runstate.IntakeHolderBrake,
		reason: "3 run(s) blocked in a row with nothing landing between them, which is the configured brake at 3",
		brake: &runstate.IntakeBrake{
			Blocked:        []runstate.BrakeBlockedRun{{RunID: stoppedRun, WorkItemID: "yoyodyne-ifd.311", Reason: "review required repair"}},
			CooldownEndsAt: trippedAt.Add(30 * time.Minute),
		},
	}
	answer := trackerReply("Three verdicts on three different changes; the line is fine.",
		`{"action":"brake","decision":"release","reason":"three verdicts on three changes, none of them about the machine"}`)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{}}
	options := triageOptions(t, tracker, nil, answer)
	options.Reports = &fakeReports{}
	options.Intake = intake
	reply := triageSend(t, options)
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the brake decision applied", reply.Actions)
	}
	if !strings.Contains(reply.Actions[0].Summary, "release the brake's hold") {
		t.Fatalf("summary = %q, want the release said as recorded rather than made", reply.Actions[0].Summary)
	}
	if len(intake.decisions) != 1 || intake.decisions[0].Decision != runstate.BrakeDecisionRelease {
		t.Fatalf("decisions = %#v, want the release recorded on the hold", intake.decisions)
	}
	if by := intake.decisions[0].DecidedBy; !strings.Contains(by, "development-manager conversation") {
		t.Fatalf("decided by %q, want this conversation named", by)
	}
	if !intake.held {
		t.Fatal("the conversation lifted the hold itself, want the watching session to lift it on the recorded decision")
	}

	// The operator's hold takes no brake decision.
	theirs := &fakeIntake{held: true, heldAt: trippedAt, heldBy: runstate.IntakeHolderOperator, reason: "reordering"}
	options = triageOptions(t, tracker, nil, trackerReply("Releasing.",
		`{"action":"brake","decision":"release","reason":"the queue looks fine"}`))
	options.Reports = &fakeReports{}
	options.Intake = theirs
	reply = triageSend(t, options)
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want a decision about the operator's hold refused", reply.Actions)
	}
	if !strings.Contains(reply.Actions[0].Failure, "not held by the harness's own brake") {
		t.Fatalf("failure = %q, want the refusal to say whose hold it is", reply.Actions[0].Failure)
	}
	if !theirs.held || len(theirs.decisions) != 0 {
		t.Fatalf("the operator's hold = held %t with %d decision(s), want it untouched", theirs.held, len(theirs.decisions))
	}

	// A decision outside the vocabulary, or one naming an item, is refused before
	// anything is run.
	for _, block := range []string{
		`{"action":"brake","decision":"ignore","reason":"no"}`,
		`{"action":"brake","id":"yoyodyne-ifd.311","decision":"release","reason":"no"}`,
	} {
		options = triageOptions(t, tracker, nil, trackerReply("Deciding.", block))
		options.Reports = &fakeReports{}
		options.Intake = intake
		if reply, err := openTestSession(t, options).Send(context.Background(), "Work the docket."); refusalOf(reply, err) == "" {
			t.Fatalf("block %s: Send() refused nothing, want the malformed decision refused whole", block)
		}
	}
	if len(intake.decisions) != 1 {
		t.Fatalf("decisions = %d, want the refused blocks to have recorded nothing", len(intake.decisions))
	}
}

// An attempt that never became a run names no run, so a decision about it names
// the item instead. Waiting and escalating are the two that need no run: the note
// or the blocker lands on the item, and the closure goes to the item's attempts
// rather than to a run's entries.
func TestADecisionAboutAnAttemptThatNeverBecameARunNamesTheItemAndNoRun(t *testing.T) {
	t.Parallel()

	for _, decided := range []struct {
		decision string
		revisit  bool
	}{
		{decision: "wait", revisit: true},
		{decision: "escalate"},
	} {
		t.Run(decided.decision, func(t *testing.T) {
			t.Parallel()

			answer := trackerReply("Decided.",
				`{"action":"triage","id":"yoyodyne-ifd.90","decision":"`+decided.decision+`","reason":"the primary checkout is dirty, and only a person can clean it"}`)
			if decided.decision == "escalate" {
				answer = reportReply(answer, `{"severity":"warning","message":"yoyodyne-ifd.90 cannot be dispatched while the primary checkout has uncommitted changes."}`)
			}
			tracker := &fakeTracker{items: map[string]beads.WorkItem{
				"yoyodyne-ifd.90": {ID: "yoyodyne-ifd.90", Title: "the item that never started", Status: "open"},
			}}
			docket := &fakeDocket{closes: 1}
			reply := triageReplyClosing(t, tracker, docket, answer)

			if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
				t.Fatalf("actions = %#v, want the decision recorded", reply.Actions)
			}
			var written string
			switch decided.decision {
			case "escalate":
				if len(tracker.blocked) != 1 || tracker.blocked[0][0] != "yoyodyne-ifd.90" {
					t.Fatalf("blocked = %#v, want the item blocked", tracker.blocked)
				}
				written = tracker.blocked[0][1]
			default:
				if len(tracker.updates) != 1 || tracker.updates[0].id != "yoyodyne-ifd.90" {
					t.Fatalf("updates = %#v, want the decision noted on the item", tracker.updates)
				}
				written = tracker.updates[0].change.AppendNotes
			}
			if !strings.Contains(written, "never became a run") || !strings.Contains(written, "only a person can clean it") {
				t.Fatalf("the item records %q, want what was decided about and why", written)
			}
			if len(docket.closed) != 1 {
				t.Fatalf("closed = %#v, want one closure", docket.closed)
			}
			closure := docket.closed[0]
			if closure.RunID != "" || closure.WorkItemID != "yoyodyne-ifd.90" || closure.Decision != decided.decision || closure.Revisit != decided.revisit {
				t.Fatalf("closure = %#v, want the item's attempts closed by %q and no run named", closure, decided.decision)
			}
			if fmt.Sprint(closure.Classes) != fmt.Sprint([]triage.Class{triage.ClassUnstartedAttempt}) {
				t.Fatalf("classes = %#v, want only the attempts that never became a run", closure.Classes)
			}
			if rendered := renderTrackerOutcomes(domain.RoleDevelopmentManager, reply.Actions); !strings.Contains(rendered, "docket entry(s) of attempts at that item are closed") {
				t.Fatalf("the operator was not told the entry was closed:\n%s", rendered)
			}
		})
	}
}

// A wait can name the admitted work it depends on, and that work travels to the
// docket so the entry stays off it until the work closes rather than for the
// ordinary window (yoyodyne-ifd.428.84). Work that has already closed is not
// something to wait on, and nothing is recorded for it.
func TestAWaitNamesTheWorkItWaitsOnAndRefusesWorkAlreadyClosed(t *testing.T) {
	t.Parallel()

	for _, named := range []struct {
		name    string
		status  string
		applied bool
	}{
		{name: "open work", status: "in_progress", applied: true},
		{name: "closed work", status: "closed", applied: false},
	} {
		t.Run(named.name, func(t *testing.T) {
			t.Parallel()

			answer := trackerReply("Decided.",
				`{"action":"triage","id":"yoyodyne-ifd.70","run":"`+stoppedRun+`","decision":"wait","waits_on":"yoyodyne-ifd.71","reason":"nothing to do until the recovery lands"}`)
			tracker := &fakeTracker{items: map[string]beads.WorkItem{
				"yoyodyne-ifd.70": {ID: "yoyodyne-ifd.70", Title: "the item that stopped", Status: "open"},
				"yoyodyne-ifd.71": {ID: "yoyodyne-ifd.71", Title: "the recovery", Status: named.status},
			}}
			docket := &fakeDocket{closes: 1}
			reply := triageReplyClosing(t, tracker, docket, answer)

			if len(reply.Actions) != 1 || reply.Actions[0].Applied != named.applied {
				t.Fatalf("actions = %#v, want applied = %t", reply.Actions, named.applied)
			}
			if !named.applied {
				if len(docket.closed) != 0 {
					t.Fatalf("closed = %#v, want nothing closed for a wait on closed work", docket.closed)
				}
				if !strings.Contains(reply.Actions[0].Failure+reply.Actions[0].Detail+reply.Actions[0].Summary, "already closed") {
					t.Fatalf("refusal = %#v, want it to say the work is already closed", reply.Actions[0])
				}
				return
			}
			if len(docket.closed) != 1 || docket.closed[0].WaitsOn != "yoyodyne-ifd.71" || !docket.closed[0].Revisit {
				t.Fatalf("closed = %#v, want the wait carrying the work it waits on", docket.closed)
			}
			if len(tracker.updates) != 1 || !strings.Contains(tracker.updates[0].change.AppendNotes, "waiting on yoyodyne-ifd.71") {
				t.Fatalf("updates = %#v, want the note to name the work the wait is on", tracker.updates)
			}
		})
	}
}

// Only a wait names the work it waits on, and never its own item.
func TestOnlyAWaitNamesWorkToWaitOn(t *testing.T) {
	t.Parallel()

	for _, action := range []TrackerAction{
		{Action: actionTriage, ID: "yoyodyne-ifd.70", Run: stoppedRun, Decision: "rerun", WaitsOn: "yoyodyne-ifd.71", Reason: "why"},
		{Action: actionTriage, ID: "yoyodyne-ifd.70", Run: stoppedRun, Decision: "wait", WaitsOn: "yoyodyne-ifd.70", Reason: "why"},
	} {
		if problems := action.triageProblems(); len(problems) == 0 {
			t.Fatalf("triageProblems(%#v) = none, want the waits_on refused", action)
		}
	}
	fine := TrackerAction{Action: actionTriage, ID: "yoyodyne-ifd.70", Run: stoppedRun, Decision: "wait", WaitsOn: "yoyodyne-ifd.71", Reason: "why"}
	if problems := fine.triageProblems(); len(problems) != 0 {
		t.Fatalf("triageProblems(wait on other work) = %v, want none", problems)
	}
}
