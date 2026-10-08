package chat

// The admission-side half of the executor marker: work whose execution is a
// persona conversation says so as it is admitted, and work admitted before the
// marker existed can acquire one. Both doors matter — the queue that provoked
// this was admitted entirely through the first, before the second existed, so a
// marker with no way onto an existing item would have left every item it was
// written for unmarked.

import (
	"context"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Admission carries the marker to the tracker, so the item is never selectable
// for a developer run rather than being marked in a second action that leaves a
// window where it is.
func TestAdmissionCarriesTheExecutorToTheTracker(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting the promotion.",
			`{"action":"create","kind":"feature","title":"Promote the brief","description":"The architect promotes it.","goal":"`+recordedGoal+`","executor":"conversation:architect","reason":"the architect carries it in conversation"}`)},
		{SessionID: "session-1", FinalText: "It is in the queue."},
	}})
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)
	options.Admission = Admission{WorkItems: domain.ApprovalAutomatic}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "the architect should promote the brief")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the creation carried out", reply.Actions)
	}
	if len(tracker.created) != 1 || tracker.created[0].Executor != domain.ConversationWith(domain.RoleArchitect) {
		t.Fatalf("created = %#v, want the executor carried to the tracker", tracker.created)
	}
}

// The queue is older than the marker, so an item already in it can be marked.
// This is the action the items that provoked the whole thing are marked with.
func TestAnItemAlreadyInTheQueueCanAcquireAnExecutor(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Marking the promotion item.",
			`{"action":"update","id":"yoyodyne-ifd.138","executor":"conversation:architect","reason":"no developer run can promote a document the architect owns"}`)},
		{SessionID: "session-1", FinalText: "It is marked."},
	}})
	options.Tracker = tracker
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "138 is not developer work")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the update carried out: %#v", reply.Actions, reply.Actions[0].Failure)
	}
	if len(tracker.updates) != 1 || tracker.updates[0].change.Executor != domain.ConversationWith(domain.RoleArchitect) {
		t.Fatalf("updates = %#v, want the executor applied to the existing item", tracker.updates)
	}
	// The operator reads what changed, and an update that only marked the item
	// would otherwise be reported as an edit that named nothing.
	if !strings.Contains(reply.Actions[0].Summary, "executor") {
		t.Fatalf("summary = %q, want it to say what was changed", reply.Actions[0].Summary)
	}
}

// An executor is a marker selection reads, so a word nothing reads would take an
// item out of every pass's reach without anybody having said what carries it.
func TestAnExecutorTheHarnessDoesNotRecognizeIsRefused(t *testing.T) {
	t.Parallel()

	if err := (TrackerAction{
		Action:      actionCreate,
		Title:       "Promote the brief",
		Description: "The architect promotes it.",
		Goal:        recordedGoal,
		Executor:    "architect",
		Reason:      "the architect carries it",
	}).Validate(); err == nil || !strings.Contains(err.Error(), "architect") {
		t.Fatalf("Validate() error = %v, want the unknown executor named", err)
	}
	if err := (TrackerAction{
		Action:   actionUpdate,
		ID:       "yoyodyne-ifd.138",
		Executor: "architect",
		Reason:   "it is not developer work",
	}).Validate(); err == nil || !strings.Contains(err.Error(), "architect") {
		t.Fatalf("Validate() error = %v, want the unknown executor named", err)
	}
	// Nothing else takes it: an action that names an argument its operation has no
	// use for was misunderstood, and marking an item through a reprioritization
	// would be a marker nobody reading the queue's history could find.
	if err := (TrackerAction{
		Action:   actionAttribute,
		ID:       "yoyodyne-ifd.138",
		Goal:     recordedGoal,
		Executor: domain.WorkItemExecutorConversation,
		Reason:   "it serves that goal",
	}).Validate(); err == nil || !strings.Contains(err.Error(), "executor") {
		t.Fatalf("Validate() error = %v, want attribute refused for carrying an executor", err)
	}
}

// A marker that says only that a conversation carries the work is refused where
// it is written, because it is the whole stretch between the handoff and
// somebody picking the item up that has nothing else to name: the pickup names
// the role, and until there is one this marker is all a thread has.
func TestAnExecutorThatDoesNotSayWhoseConversationIsRefused(t *testing.T) {
	t.Parallel()

	for _, action := range []TrackerAction{
		{
			Action:      actionCreate,
			Title:       "Promote the brief",
			Description: "The architect promotes it.",
			Goal:        recordedGoal,
			Executor:    domain.WorkItemExecutorConversation,
			Reason:      "a conversation carries it",
		},
		{
			Action:   actionUpdate,
			ID:       "yoyodyne-ifd.138",
			Executor: domain.WorkItemExecutorConversation,
			Reason:   "it is not developer work",
		},
	} {
		err := action.Validate()
		if err == nil {
			t.Fatalf("%s with an unattributed executor = nil error, want it refused", action.Action)
		}
		// The refusal names what was missing and what to write instead, because the
		// role that reads it is one turn away from writing the marker again.
		if !strings.Contains(err.Error(), "whose conversation") || !strings.Contains(err.Error(), "conversation:architect") {
			t.Fatalf("%s refusal = %v, want the missing role named along with the executors that carry one", action.Action, err)
		}
	}
}

// The contracts are what a role writes the marker from, so an executor the
// harness accepts and no contract names is one nothing will ever write. They are
// prose rather than a generated list, which is exactly why this is checked.
func TestTheContractsNameEveryExecutorAnItemMayCarry(t *testing.T) {
	t.Parallel()

	for _, contract := range []struct {
		who  string
		text string
	}{
		{who: "product manager", text: productManagerContract},
		{who: "development manager", text: developmentManagerContract},
	} {
		for _, executor := range domain.WorkItemExecutors {
			if !strings.Contains(contract.text, string(executor)) {
				t.Fatalf("the %s contract never names the executor %q", contract.who, executor)
			}
		}
	}
}

// A read says what carries the item, because an operator or a role deciding
// whether work is stuck needs to see that nothing was ever going to pull it.
func TestReadingAnItemSaysWhatCarriesIt(t *testing.T) {
	t.Parallel()

	rendered := renderWorkItemEvidence(workItemWithExecutor(domain.ConversationWith(domain.RoleArchitect)), recordedGoals(recordedGoal))
	if !strings.Contains(rendered, "executor: conversation:architect") {
		t.Fatalf("rendered = %q, want the executor stated", rendered)
	}
	// Ordinary work says nothing, because a line on every item is a line nobody
	// reads on the one item it matters for.
	if strings.Contains(renderWorkItemEvidence(workItemWithExecutor(""), recordedGoals(recordedGoal)), "executor:") {
		t.Fatalf("ordinary work was labelled with an executor")
	}
}

func workItemWithExecutor(executor domain.WorkItemExecutor) beads.WorkItem {
	return beads.WorkItem{
		ID:          "yoyodyne-ifd.138",
		Title:       "Promote the brief",
		Description: "The architect promotes it.",
		Status:      "open",
		Priority:    1,
		IssueType:   "task",
		Executor:    executor,
	}
}
