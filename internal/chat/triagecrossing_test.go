package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// crossedItem is the item the ceremonies below were held over.
const crossedItem = "yoyodyne-ifd.143"

func crossingTracker() *fakeTracker {
	return &fakeTracker{items: map[string]beads.WorkItem{
		crossedItem: {ID: crossedItem, Title: "the item that stopped", Status: "open"},
	}}
}

// One of the week's override ceremonies, replayed as the development manager
// now takes it. What used to be a refusal, an escalation, an operator at a
// terminal and a decision recorded afterwards is one turn: the cap is crossed
// with its argument, the argument lands on the item, and the decision the cap
// refused is recorded in the same reply.
func TestTheDevelopmentManagerCrossesACapAndRecordsTheDecisionItRefused(t *testing.T) {
	t.Parallel()

	budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
	tracker := crossingTracker()
	reply := triageReply(t, tracker, budgets, trackerReply("The change was right and the ground moved under it.",
		`{"action":"triage","id":"`+crossedItem+`","run":"`+stoppedRun+`","decision":"cross","budget":"`+runstate.TriageRerunBudget+`","reason":"the one re-run went on a base that has since landed, so this is a fresh question rather than the same one again"}`,
		`{"action":"triage","id":"`+crossedItem+`","run":"`+stoppedRun+`","decision":"rerun","reason":"the change was right and the ground moved under it"}`))

	// The re-run budget starts at one and the item has not spent it, so the
	// crossing is exercised against a cap that has already refused something: put
	// the item past it first.
	if len(reply.Actions) != 2 {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	for _, outcome := range reply.Actions {
		if !outcome.Applied {
			t.Fatalf("%q was refused: %s", outcome.Action.Decision, outcome.Failure)
		}
	}

	crossing := reply.Actions[0]
	// The crossing says the cap, the ceiling, and which of the five it was, and it
	// says plainly that nothing was bought — a crossing read as a grant is a
	// development manager who thinks the item has an attempt it does not.
	for _, want := range []string{
		"crossed the " + runstate.TriageRerunBudget + " cap for " + crossedItem + " to 2",
		"crossing 1 of 5",
		"nothing was spent and no attempt was bought",
	} {
		if !strings.Contains(crossing.Summary, want) {
			t.Fatalf("the crossing summary is missing %q:\n%s", want, crossing.Summary)
		}
	}
	if crossing.Crossing == nil {
		t.Fatalf("the crossing outcome carries no record of what the store permitted")
	}
	if crossing.Crossing.Budget != runstate.TriageRerunBudget || crossing.Crossing.Cap != 2 ||
		crossing.Crossing.Crossing != 1 || crossing.Crossing.Crossings != runstate.MaxDelegatedCapCrossings {
		t.Fatalf("the crossing outcome = %+v", *crossing.Crossing)
	}

	// The reason and the count are on the item, which is where they outlive the
	// channel message that carried them to the operator.
	if len(tracker.updates) != 2 {
		t.Fatalf("updates = %#v", tracker.updates)
	}
	notes := tracker.updates[0].change.AppendNotes
	for _, want := range []string{
		"the " + runstate.TriageRerunBudget + " cap crossed to 2 on the development manager's own authority",
		"crossing 1 of 5 for this item",
		"on the stopped work of run " + stoppedRun,
		"by the development manager in conversation",
		"a base that has since landed",
	} {
		if !strings.Contains(notes, want) {
			t.Fatalf("the item's crossing note is missing %q:\n%s", want, notes)
		}
	}

	// And the decision the cap refused is recorded against the raised budget.
	counters, err := budgets.store.Counters(crossedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.Reruns != 1 || counters.DelegatedCrossings() != 1 {
		t.Fatalf("counters after the crossing and the decision = %#v", counters)
	}

	// A crossing is not what became of the stoppage, so what the harness writes
	// down about the delivery is the decision rather than the step before it.
	decision, reason, found := reply.TriageDecision(stoppedRun)
	if !found || decision != decisionRerun || !strings.Contains(reason, "the ground moved") {
		t.Fatalf("TriageDecision() = %q, %q, %v, want the re-run rather than the crossing", decision, reason, found)
	}
}

// The refusal a development manager actually meets now offers his own crossing
// before it offers the operator's command, and it offers one per budget that
// refused: an action standing behind two of them is what cost two override
// ceremonies minutes apart on each of two items.
func TestACapRefusalOffersTheCrossingThatIsTheDevelopmentManagersOwn(t *testing.T) {
	t.Parallel()

	budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 1, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 1)
	ctx := context.Background()
	// One grant of the one permitted, committing the one round the cap has, so a
	// second repair is refused by both budgets at once — which is the refusal the
	// wording was rebuilt for.
	granted := runstate.TriageDecision{
		Decision:     decisionRepair,
		RunID:        stoppedRun,
		Reason:       "the first grant, recorded before this conversation",
		DecidedBy:    "development manager",
		Conversation: "chat-0123456789abcdef",
		Turn:         1,
	}
	if _, err := budgets.store.GrantRepair(ctx, crossedItem, granted, 1, budgets.clock.Now(), budgets.caps); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}

	refused := triageReply(t, crossingTracker(), budgets, trackerReply("One more go.",
		`{"action":"triage","id":"`+crossedItem+`","run":"`+stoppedRun+`","decision":"repair","reason":"the findings still look actionable"}`))
	if len(refused.Actions) != 1 || refused.Actions[0].Applied {
		t.Fatalf("actions = %#v", refused.Actions)
	}
	failure := refused.Actions[0].Failure
	for _, want := range []string{
		`"decision":"cross"`,
		`"budget":"` + runstate.TriageRepairGrantBudget + `"`,
		`"budget":"` + runstate.TriageReviewRoundBudget + `"`,
		"both are needed",
		"5 times per item",
		"yoyo triage override",
	} {
		if !strings.Contains(failure, want) {
			t.Fatalf("the refusal is missing %q:\n%s", want, failure)
		}
	}
}

// A crossing that argued nothing is refused before anything is recorded, and so
// is one that does not say which cap it is crossing. Both are the delegation's
// own conditions rather than form-filling: the argument is what reaches the
// operator, and the cap is what a guess would raise while the other one went on
// refusing the same decision.
func TestACrossingIsRefusedWithoutAJustificationOrACap(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		action string
		want   string
	}{
		{
			name:   "no reason",
			action: `{"action":"triage","id":"` + crossedItem + `","run":"` + stoppedRun + `","decision":"cross","budget":"` + runstate.TriageReviewRoundBudget + `"}`,
			want:   "a crossing nobody argued for is refused outright",
		},
		{
			name:   "no budget",
			action: `{"action":"triage","id":"` + crossedItem + `","run":"` + stoppedRun + `","decision":"cross","reason":"it deserves another go"}`,
			want:   `triage "cross" requires "budget"`,
		},
		{
			name:   "a budget nothing bounds",
			action: `{"action":"triage","id":"` + crossedItem + `","run":"` + stoppedRun + `","decision":"cross","budget":"patience","reason":"it deserves another go"}`,
			want:   `triage budget "patience" is not a cap`,
		},
		{
			name:   "a budget on a decision that crosses nothing",
			action: `{"action":"triage","id":"` + crossedItem + `","run":"` + stoppedRun + `","decision":"rerun","budget":"` + runstate.TriageReviewRoundBudget + `","reason":"the ground moved"}`,
			want:   `only the "cross" decision names a "budget"`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			// These are refused where every malformed action is: before anything in
			// the block runs, so nothing is written and nothing is crossed.
			budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
			tracker := crossingTracker()
			options := triageOptions(t, tracker, budgets, trackerReply("Decided.", testCase.action))
			reply, err := openTestSession(t, options).Send(context.Background(), "Work the docket.")
			if refusal := refusalOf(reply, err); !strings.Contains(refusal, testCase.want) {
				t.Fatalf("Send() refusal = %q, want it to contain %q", refusal, testCase.want)
			}
			if len(tracker.updates) != 0 {
				t.Fatalf("a refused crossing wrote on the item: %#v", tracker.updates)
			}
			counters, countErr := budgets.store.Counters(crossedItem)
			if countErr != nil {
				t.Fatalf("Counters() error = %v", countErr)
			}
			if len(counters.Overrides) != 0 {
				t.Fatalf("a refused crossing crossed something: %+v", counters.Overrides)
			}
		})
	}
}

// The sixth crossing of one item is the operator's again, and the refusal says
// so with the command in it. An item crossed five times is one where something
// other than the budget is wrong, which is what the bound exists to surface.
func TestTheSixthCrossingIsRefusedAndSendsTheDevelopmentManagerToTheOperator(t *testing.T) {
	t.Parallel()

	budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
	crossing := `{"action":"triage","id":"` + crossedItem + `","run":"` + stoppedRun + `","decision":"cross","budget":"` + runstate.TriageReviewRoundBudget + `","reason":"the findings are narrow and the change is preserved"}`
	for taken := 1; taken <= runstate.MaxDelegatedCapCrossings; taken++ {
		reply := triageReply(t, crossingTracker(), budgets, trackerReply("Crossing it.", crossing))
		if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
			t.Fatalf("crossing %d = %#v", taken, reply.Actions)
		}
	}

	tracker := crossingTracker()
	refused := triageReply(t, tracker, budgets, trackerReply("One more.", crossing))
	if len(refused.Actions) != 1 || refused.Actions[0].Applied {
		t.Fatalf("the sixth crossing = %#v", refused.Actions)
	}
	failure := refused.Actions[0].Failure
	for _, want := range []string{
		"already carries 5 of 5 cap crossing(s)",
		"cap is the operator's again",
		"yoyo triage override",
	} {
		if !strings.Contains(failure, want) {
			t.Fatalf("the sixth crossing's refusal is missing %q:\n%s", want, failure)
		}
	}
	if len(tracker.updates) != 0 {
		t.Fatalf("a refused crossing wrote on the item: %#v", tracker.updates)
	}
}

// The whole of what the delegation promised, end to end: the ceremony that used
// to be a refusal, an escalation, an operator at a terminal and a decision
// afterwards is a recording on the item and a line in the operator's channel,
// from the same turn and the same durable record.
//
// It is asserted against the event the conversation actually wrote rather than
// against a payload a test composed, because that record is the only thing the
// channel reads: a crossing whose figures never reached the log would be a
// crossing the operator is told nothing about, which is the delegation without
// the condition it was granted under.
func TestAnOverrideCeremonyBecomesARecordingOnTheItemAndALineInTheChannel(t *testing.T) {
	t.Parallel()

	budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
	tracker := crossingTracker()
	options := triageOptions(t, tracker, budgets, trackerReply("Crossing it and running it again.",
		`{"action":"triage","id":"`+crossedItem+`","run":"`+stoppedRun+`","decision":"cross","budget":"`+runstate.TriageRerunBudget+`","reason":"the one re-run went on a base that has since landed"}`))
	session := openTestSession(t, options)
	if _, err := session.Send(context.Background(), "Work the docket."); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// The recording: the item carries the cap, the count, and the argument.
	if len(tracker.updates) != 1 {
		t.Fatalf("updates = %#v", tracker.updates)
	}
	for _, want := range []string{"crossed to 2", "crossing 1 of 5", "a base that has since landed"} {
		if !strings.Contains(tracker.updates[0].change.AppendNotes, want) {
			t.Fatalf("the item's note is missing %q:\n%s", want, tracker.updates[0].change.AppendNotes)
		}
	}

	// The channel line, read off the conversation's own log by the thing that
	// posts it.
	conversationID := session.Evidence().ConversationID
	events, err := options.Store.(*runstate.ConversationStore).LoadEvents(conversationID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	conversation := runstate.Conversation{
		ConversationID: conversationID,
		Role:           domain.RoleDevelopmentManager,
		Agent:          string(domain.RoleDevelopmentManager),
	}
	var said notify.Message
	for index := range events {
		notification, err := notify.FromConversation(conversation, events, index)
		if err != nil {
			t.Fatalf("select event %d: %v", index, err)
		}
		if notification.Silent() || notification.Event.Kind != notify.KindCapCrossed {
			continue
		}
		said, err = notify.Render(notification.Topic, notification.Speaker, notification.Event)
		if err != nil {
			t.Fatalf("say the crossing: %v", err)
		}
	}
	if said.Kind != notify.KindCapCrossed {
		t.Fatalf("nothing in the conversation's log reached the channel as a crossing")
	}
	if said.Severity != report.SeverityWarning {
		t.Fatalf("the crossing reached the channel at %q severity", said.Severity)
	}
	for _, want := range []string{runstate.TriageRerunBudget, "2", "crossing 1 of 5", "a base that has since landed"} {
		if !strings.Contains(said.Body, want) {
			t.Fatalf("the channel line is missing %q:\n%s", want, said.Body)
		}
	}
}

// spelledNumbers is the small table this contract's bound is read through. It is
// a table rather than a general spelling of numbers because the contract says one
// number and the point is only that the word and the guard agree: a bound raised
// to a figure nothing here spells fails loudly, which is the right way for it to
// fail — somebody has to write the new word into the contract either way.
var spelledNumbers = map[string]int{
	"one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
	"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
}

// The bound the development manager is told is the bound the store enforces.
// They are two statements of one number — the word in the contract and the
// constant the guard refuses against — and a contract promising a budget the
// guard does not give is a role planning around crossings it will be refused.
func TestTheContractStatesTheCrossingBoundTheStoreEnforces(t *testing.T) {
	t.Parallel()

	spelled, known := spelledNumbers[maxDelegatedCapCrossingsText]
	if !known {
		t.Fatalf("the contract spells the crossing bound %q, which this test cannot read as a number; spell the store's bound of %d",
			maxDelegatedCapCrossingsText, runstate.MaxDelegatedCapCrossings)
	}
	if spelled != runstate.MaxDelegatedCapCrossings {
		t.Fatalf("the contract states %s (%d) crossings and the store enforces %d",
			maxDelegatedCapCrossingsText, spelled, runstate.MaxDelegatedCapCrossings)
	}
	// And it is actually in the contract, rather than a constant nothing reaches:
	// the number agreeing with the guard buys nothing if the role never sees it.
	prompt := SystemPrompt(domain.RoleDevelopmentManager, Admission{}, nil, hostilePersona)
	for _, required := range []string{
		`"decision":"repair|rerun|rescope|rearm|wait|escalate|stop|proceed|retire-raise|cross|give-back-claim|leave-claim-spent"`,
		maxDelegatedCapCrossingsText + " times per item",
		"a crossing without one is refused outright",
		"yoyo triage override",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("the development manager's contract does not state %q", required)
		}
	}
}

// A crossing is durable before its note is written, exactly as a spend is, so a
// note the tracker took and timed out on leaves a cap that moved and an item
// that does not say so. The account has to name the crossing as standing rather
// than report the action as having changed nothing: a crossing asked for again
// costs the item another of its five and the operator another message about a
// cap that had already moved.
func TestATimedOutCrossingNoteNamesTheCrossingThatAlreadyStands(t *testing.T) {
	t.Parallel()

	budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
	tracker := crossingTracker()
	tracker.durableErr = errors.New("bd update failed with status timed_out and exit code -1")
	reply := triageReply(t, tracker, budgets, trackerReply("One more run, on a base that moved.",
		`{"action":"triage","id":"`+crossedItem+`","run":"`+stoppedRun+`","decision":"cross","budget":"`+runstate.TriageRerunBudget+`","reason":"the one re-run went on a base that has since landed"}`))

	if len(reply.Actions) != 1 {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	outcome := reply.Actions[0]
	if outcome.Applied || !outcome.PartlyLanded() {
		t.Fatalf("outcome = %#v, want a failure with the crossing standing behind it", outcome)
	}
	rendered := renderTrackerOutcomes(domain.RoleDevelopmentManager, reply.Actions)
	if strings.Contains(rendered, "changed nothing") {
		t.Fatalf("a durable crossing was reported as having changed nothing:\n%s", rendered)
	}
	for _, want := range []string{
		"the " + runstate.TriageRerunBudget + " cap for " + crossedItem + " is crossed to 2 on the item's durable triage record, crossing 1 of 5",
		"carries it, so the write landed",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the account is missing %q:\n%s", want, rendered)
		}
	}

	// And the crossing really is in force: the cap it raised stands on the
	// record, and the count the next crossing is refused against has moved.
	counters, err := budgets.store.Counters(crossedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if got := counters.DelegatedCrossings(); got != 1 {
		t.Fatalf("DelegatedCrossings() = %d, want the crossing whose note timed out", got)
	}
	if override, ok := counters.OverrideOf(runstate.TriageRerunBudget); !ok || override.Cap != 2 {
		t.Fatalf("OverrideOf(%q) = %#v, %v, want the cap crossed to 2", runstate.TriageRerunBudget, override, ok)
	}
}
