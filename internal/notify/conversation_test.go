package notify

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func conversationWith(role domain.AgentRole) runstate.Conversation {
	return runstate.Conversation{
		SchemaVersion:  runstate.ConversationSchemaVersion,
		ConversationID: "chat-11558d32000000000000000000000000",
		ProductID:      "yoyodyne",
		RepositoryID:   "yoyodyne",
		Agent:          string(role),
		Role:           role,
		Backend:        domain.BackendClaudeCode,
		StartedAt:      moment,
		UpdatedAt:      moment,
	}
}

// recorded is one event of a conversation's log, built the way the conversation
// itself builds one so a test cannot pass against a payload nothing writes.
func recorded(t *testing.T, sequence uint64, eventType execution.EventType, payload any) execution.Event {
	t.Helper()
	event, err := execution.NewEvent("chat-11558d32000000000000000000000000", sequence, moment, eventType, "harness.chat", payload)
	if err != nil {
		t.Fatalf("record a %s event: %v", eventType, err)
	}
	return event
}

// applied is a tracker action the harness carried out, in the shape the
// conversation records one.
func applied(t *testing.T, sequence uint64, workItemID string, action map[string]any) execution.Event {
	t.Helper()
	return recorded(t, sequence, execution.EventTrackerActionApplied, map[string]any{
		"action_id":    "t1.1",
		"turn":         1,
		"action":       action,
		"work_item_id": workItemID,
		"summary":      "the harness's own account of it",
	})
}

// appliedTo is a tracker action carried out against an item the tracker already
// says something carries, which is the reading the harness takes as the action
// runs rather than anything the action itself said.
func appliedTo(t *testing.T, sequence uint64, workItemID, executor string, action map[string]any) execution.Event {
	t.Helper()
	return recorded(t, sequence, execution.EventTrackerActionApplied, map[string]any{
		"action_id":          "t1.1",
		"turn":               1,
		"action":             action,
		"work_item_id":       workItemID,
		"work_item_title":    "Promote the brief to its next revision",
		"work_item_executor": executor,
		"summary":            "the harness's own account of it",
	})
}

// said selects one event of a log and insists the result can actually be said,
// because a milestone nothing has words for reaches nobody.
func said(t *testing.T, conversation runstate.Conversation, events []execution.Event, index int) (Notification, Message) {
	t.Helper()
	notification, err := FromConversation(conversation, events, index)
	if err != nil {
		t.Fatalf("select from conversation event %d: %v", index, err)
	}
	if notification.Silent() {
		t.Fatalf("conversation event %d said nothing", index)
	}
	message, err := Render(notification.Topic, notification.Speaker, notification.Event)
	if err != nil {
		t.Fatalf("a selected %s could not be said: %v", notification.Event.Kind, err)
	}
	return notification, message
}

func TestAdmittedWorkIsSaidByTheRoleThatAdmittedItWithItsGoal(t *testing.T) {
	// The queue growing is the harness's steering wheel turning, and an item that
	// does not say what it is for is the work nobody can later decide to stop.
	conversation := conversationWith(domain.RoleProductManager)
	events := []execution.Event{applied(t, 1, "yoyodyne-ifd.113", map[string]any{
		"action":      "create",
		"title":       "Report what conversations do to the backlog",
		"description": "the item's own words",
		"goal":        "Work the harness runs on its own is visible while it runs",
		"reason":      "the channel says nothing about the queue moving",
	})}
	notification, message := said(t, conversation, events, 0)
	if notification.Event.Kind != KindItemAdmitted {
		t.Fatalf("a creation by the product manager is %s", notification.Event.Kind)
	}
	if notification.Topic.Key() != "work-item:yoyodyne-ifd.113" {
		t.Fatalf("addressed to %q", notification.Topic.Key())
	}
	if notification.Speaker.Role != domain.RoleProductManager {
		t.Fatalf("spoken by %q", notification.Speaker.Key())
	}
	if notification.Event.Refs.ConversationID != conversation.ConversationID {
		t.Fatalf("refs %+v do not lead back to the conversation", notification.Event.Refs)
	}
	if !strings.Contains(message.Body, "Work the harness runs on its own is visible while it runs") {
		t.Fatalf("body %q does not say what the work is for", message.Body)
	}
	if !strings.Contains(message.Body, "yoyodyne-ifd.113") {
		t.Fatalf("body %q does not say what was admitted", message.Body)
	}
	// An admission is usually the first thing said about an item, so it is where
	// most threads get the name their header carries.
	if notification.Topic.Title != "Report what conversations do to the backlog" {
		t.Fatalf("topic title = %q, want what the item is called", notification.Topic.Title)
	}
	if message.TopicTitle != notification.Topic.Title {
		t.Fatalf("envelope carried %q, want the topic's own title", message.TopicTitle)
	}
}

func TestACreationUnderAParentIsDecompositionRatherThanAdmission(t *testing.T) {
	// The development manager may not admit work at all, so recording what it did
	// as an admission would say it exercised authority it does not have.
	conversation := conversationWith(domain.RoleDevelopmentManager)
	parent := "yoyodyne-ifd.68"
	events := []execution.Event{applied(t, 1, "yoyodyne-ifd.68.5", map[string]any{
		"action":      "create",
		"title":       "The voice work under the reporting epic",
		"description": "the item's own words",
		"goal":        "Work the harness runs on its own is visible while it runs",
		"parent":      parent,
		"reason":      "the epic is too large to run as one item",
	})}
	notification, message := said(t, conversation, events, 0)
	if notification.Event.Kind != KindItemDecomposed {
		t.Fatalf("a creation under a parent is %s", notification.Event.Kind)
	}
	if notification.Speaker.Role != domain.RoleDevelopmentManager {
		t.Fatalf("spoken by %q", notification.Speaker.Key())
	}
	// The record keeps what it was cut out of, for anybody auditing where a piece
	// of work came from.
	if notification.Event.Detail.Parent != parent {
		t.Fatalf("detail %+v does not record what was decomposed", notification.Event.Detail)
	}
	// A decomposition is addressed to the thread of the item it created, not of
	// the item it came out of. That is what the words have to respect: the only
	// item a reader has in front of them is the new one, named in the header
	// above the message, so anything the sentence points at as nearby resolves to
	// the item the sentence is already about.
	if notification.Topic.Key() != "work-item:yoyodyne-ifd.68.5" {
		t.Fatalf("addressed to %q, want the thread of the item that was created", notification.Topic.Key())
	}
	if strings.Contains(strings.ToLower(message.Body), "above") {
		t.Fatalf("body %q points at what it was cut out of by where it sits, which is this item's own thread", message.Body)
	}
	// The message carries the new item's reference to the shared resolver.
	if !strings.Contains(message.Body, "yoyodyne-ifd.68.5") {
		t.Fatalf("body %q does not say what was cut out", message.Body)
	}
	if !strings.Contains(message.Body, "a larger item") {
		t.Fatalf("body %q does not say what it was cut out of at all", message.Body)
	}
	for _, id := range trackerIdentifier.FindAllString(message.Body, -1) {
		if id == parent {
			t.Fatalf("body %q names the parent instead of the new item", message.Body)
		}
	}
}

func TestAnAttributionAndAReprioritizationSayWhatChanged(t *testing.T) {
	conversation := conversationWith(domain.RoleProductManager)
	events := []execution.Event{
		applied(t, 1, "yoyodyne-ifd.99", map[string]any{
			"action": "attribute",
			"id":     "yoyodyne-ifd.99",
			"goal":   "Directives reach the agent regardless of which one received them",
			"reason": "it was admitted before goals were checked",
		}),
		applied(t, 2, "yoyodyne-ifd.99", map[string]any{
			"action":   "reprioritize",
			"id":       "yoyodyne-ifd.99",
			"priority": 0,
			"reason":   "nothing else is worth doing before it",
		}),
	}
	attributed, attribution := said(t, conversation, events, 0)
	if attributed.Event.Kind != KindItemAttributed {
		t.Fatalf("an attribution is %s", attributed.Event.Kind)
	}
	if !strings.Contains(attribution.Body, "Directives reach the agent") {
		t.Fatalf("body %q does not name the goal", attribution.Body)
	}
	reordered, order := said(t, conversation, events, 1)
	if reordered.Event.Kind != KindItemReprioritized {
		t.Fatalf("a reprioritization is %s", reordered.Event.Kind)
	}
	// Zero is the top of this queue rather than an unstated place in it, which is
	// the one value an absent priority would be mistaken for.
	if !strings.Contains(order.Body, "priority 0") {
		t.Fatalf("body %q does not say where in the queue it went", order.Body)
	}
}

// An action that names no title of its own is still about an item somebody has
// to recognize, and for an item admitted before the channel existed a
// reordering is the first thing ever said about it. So the topic is named from
// what the record says the tracker called the item, and the thread it opens is
// headed like any other.
func TestAnActionThatNamesNoTitleIsNamedByTheItemItActedOn(t *testing.T) {
	conversation := conversationWith(domain.RoleProductManager)
	events := []execution.Event{recorded(t, 1, execution.EventTrackerActionApplied, map[string]any{
		"action_id": "t1.1",
		"turn":      1,
		"action": map[string]any{
			"action":   "reprioritize",
			"id":       "yoyodyne-ifd.6",
			"priority": 2,
			"reason":   "the adapter is parked and nothing is waiting on it",
		},
		"work_item_id":    "yoyodyne-ifd.6",
		"work_item_title": "Park the Codex adapter until the provider answers",
		"summary":         "the harness's own account of it",
	})}
	notification, message := said(t, conversation, events, 0)
	if notification.Topic.Title != "Park the Codex adapter until the provider answers" {
		t.Fatalf("topic title = %q, want what the tracker called the item", notification.Topic.Title)
	}
	if message.TopicTitle != notification.Topic.Title {
		t.Fatalf("envelope carried %q, want the topic's own title", message.TopicTitle)
	}
	// The title names the topic and nothing else: what a reordering says is where
	// the item went in the queue.
	if !strings.Contains(message.Body, "priority 2") {
		t.Fatalf("body %q does not say where in the queue it went", message.Body)
	}
}

// A record written before titles travelled with tracker actions leaves the topic
// addressed exactly as it was, so nothing is worse off than before.
func TestAnActionRecordedWithoutATitleLeavesTheTopicUnnamed(t *testing.T) {
	conversation := conversationWith(domain.RoleProductManager)
	events := []execution.Event{applied(t, 1, "yoyodyne-ifd.6", map[string]any{
		"action":   "reprioritize",
		"id":       "yoyodyne-ifd.6",
		"priority": 2,
		"reason":   "recorded before titles travelled with an action",
	})}
	notification, _ := said(t, conversation, events, 0)
	if notification.Topic.Title != "" {
		t.Fatalf("topic title = %q, want a topic named by its identifier alone", notification.Topic.Title)
	}
}

func TestAPriorityTheRecordDoesNotStateIsSaidAsAbsent(t *testing.T) {
	conversation := conversationWith(domain.RoleProductManager)
	events := []execution.Event{applied(t, 1, "yoyodyne-ifd.99", map[string]any{
		"action": "reprioritize",
		"id":     "yoyodyne-ifd.99",
		"reason": "recorded before the priority was carried",
	})}
	_, message := said(t, conversation, events, 0)
	if strings.Contains(message.Body, "priority 0") {
		t.Fatalf("body %q reads an absent priority as the top of the queue", message.Body)
	}
	if !strings.Contains(message.Body, "does not state") {
		t.Fatalf("body %q does not state the absence", message.Body)
	}
}

func TestApprovedWorkIsSaidByTheHarnessAndCarriesTheGoalItWasProposedUnder(t *testing.T) {
	// The decision was the operator's, and the operator is not a persona: no
	// persona narrates a decision it did not make. The goal is on the proposal
	// rather than on the creation, which is why the earlier record is read.
	conversation := conversationWith(domain.RoleProductManager)
	proposed := map[string]any{
		"id":              "1.1",
		"conversation_id": conversation.ConversationID,
		"turn":            1,
		"proposal": map[string]any{
			"title":       "Thread conversation milestones into the channel",
			"description": "the proposal's own words",
			"rationale":   "the backlog changing is invisible",
			"goal":        "Work the harness runs on its own is visible while it runs",
		},
	}
	events := []execution.Event{
		recorded(t, 1, execution.EventProposalRecorded, proposed),
		recorded(t, 2, execution.EventProposalApproved, proposed),
		recorded(t, 3, execution.EventProposalCreated, map[string]any{
			"proposal_id":  "1.1",
			"turn":         1,
			"work_item_id": "yoyodyne-ifd.114",
			"title":        "Thread conversation milestones into the channel",
		}),
	}
	// The approval and the creation are one decision, so only the half that names
	// the item is said.
	if notification, err := FromConversation(conversation, events, 1); err != nil || !notification.Silent() {
		t.Fatalf("the approval said %+v (%v), want nothing beside the creation", notification.Event.Kind, err)
	}
	notification, message := said(t, conversation, events, 2)
	if notification.Event.Kind != KindWorkApproved {
		t.Fatalf("an approved proposal is %s", notification.Event.Kind)
	}
	if !notification.Speaker.IsHarness() {
		t.Fatalf("spoken by %q", notification.Speaker.Key())
	}
	if notification.Topic.Key() != "work-item:yoyodyne-ifd.114" {
		t.Fatalf("addressed to %q", notification.Topic.Key())
	}
	if !strings.Contains(message.Body, "Work the harness runs on its own is visible while it runs") {
		t.Fatalf("body %q does not carry the goal it was proposed under", message.Body)
	}
}

func TestDeclinedWorkIsAddressedToTheProductWithTheReasonKept(t *testing.T) {
	// Nothing was created, so there is no item to thread it to; burying it in
	// somebody else's thread would misfile it.
	conversation := conversationWith(domain.RoleProductManager)
	events := []execution.Event{recorded(t, 1, execution.EventProposalRejected, map[string]any{
		"id":              "1.1",
		"conversation_id": conversation.ConversationID,
		"turn":            1,
		"proposal": map[string]any{
			"title":       "Rewrite the CLI in Rust",
			"description": "port everything",
			"rationale":   "it would be faster",
			"goal":        "Support development in any language",
		},
		"reason": "not this quarter",
	})}
	notification, message := said(t, conversation, events, 0)
	if notification.Event.Kind != KindWorkDeclined {
		t.Fatalf("a declined proposal is %s", notification.Event.Kind)
	}
	if notification.Topic.Kind != TopicProduct {
		t.Fatalf("addressed to %q", notification.Topic.Key())
	}
	if !notification.Speaker.IsHarness() {
		t.Fatalf("spoken by %q", notification.Speaker.Key())
	}
	if !strings.Contains(message.Body, "not this quarter") || !strings.Contains(message.Body, "Rewrite the CLI in Rust") {
		t.Fatalf("body %q loses what was declined or why", message.Body)
	}
}

func TestNothingIsSaidAboutWhatDidNotMoveTheBacklog(t *testing.T) {
	// Most of a conversation's log is the turn itself, and a channel that
	// reported every record of it would be the event log this exists not to be.
	conversation := conversationWith(domain.RoleProductManager)
	events := []execution.Event{
		recorded(t, 1, execution.EventAgentMessage, map[string]any{"text": "what the product manager said"}),
		recorded(t, 2, execution.EventTrackerActionRequested, map[string]any{
			"action_id": "t1.1",
			"turn":      1,
			"action":    map[string]any{"action": "create", "title": "asked for, not yet done"},
		}),
		recorded(t, 3, execution.EventTrackerActionFailed, map[string]any{
			"action_id":    "t1.1",
			"turn":         1,
			"action":       map[string]any{"action": "create", "title": "refused by the tracker"},
			"work_item_id": "",
			"failure":      "the tracker refused it",
		}),
		applied(t, 4, "yoyodyne-ifd.113", map[string]any{
			"action": "read",
			"id":     "yoyodyne-ifd.113",
		}),
		applied(t, 5, "yoyodyne-ifd.113", map[string]any{
			"action": "update",
			"id":     "yoyodyne-ifd.113",
			"note":   "what was learned",
			"reason": "worth recording on the item",
		}),
	}
	for index := range events {
		notification, err := FromConversation(conversation, events, index)
		if err != nil {
			t.Fatalf("select from conversation event %d: %v", index, err)
		}
		if !notification.Silent() {
			t.Fatalf("event %d (%s) said %s", index, events[index].Type, notification.Event.Kind)
		}
	}
}

// Routing work to a role's conversation is the transition that had no
// representation at all: the item stops being a run's to carry, and from there
// nothing happens to it until somebody opens a conversation about it.
func TestMarkingWorkForAConversationIsSaidAsAHandoff(t *testing.T) {
	conversation := conversationWith(domain.RoleDevelopmentManager)
	events := []execution.Event{appliedTo(t, 1, "yoyodyne-ifd.138", "", map[string]any{
		"action":   "update",
		"id":       "yoyodyne-ifd.138",
		"executor": string(domain.ConversationWith(domain.RoleArchitect)),
		"reason":   "no developer run can promote a document the architect owns",
	})}
	notification, message := said(t, conversation, events, 0)
	if notification.Event.Kind != KindWorkHandedOff {
		t.Fatalf("marking work for a conversation is %s", notification.Event.Kind)
	}
	if notification.Speaker.Role != domain.RoleDevelopmentManager {
		t.Fatalf("spoken by %q", notification.Speaker.Key())
	}
	if !strings.Contains(message.Body, "conversation") {
		t.Fatalf("body %q does not say what carries the work now", message.Body)
	}
	if !strings.Contains(message.Body, "no developer run can promote a document the architect owns") {
		t.Fatalf("body %q loses why the work was routed", message.Body)
	}
	// A thread that goes quiet here is waiting on a person opening a conversation,
	// and nothing else in the record would ever tell the reader that.
	if !strings.Contains(message.Body, "no run will ever be started for this") {
		t.Fatalf("body %q does not say whose move follows the handoff", message.Body)
	}
	// And it says which person, which is the whole of the difference between a
	// thread an operator can read and one they have to reconstruct: the pickup
	// names the role, so without this the wait before it belongs to nobody.
	if !strings.Contains(message.Body, "the architect's conversation") {
		t.Fatalf("body %q does not say whose conversation carries the item", message.Body)
	}
	if !strings.HasSuffix(message.Body, nextMoveLead+"the architect's, in conversation — no run will ever be started for this.") {
		t.Fatalf("body %q leaves the wait for the pickup unattributed", message.Body)
	}
}

// Every role can be handed work, and the handoff names whichever one it was.
// Which roles carry work in conversation is a product judgement rather than a
// fact about the harness, so a handoff that could only name some of them would
// be back to the silence for the rest.
func TestAHandoffNamesWhicheverRoleCarriesTheWork(t *testing.T) {
	conversation := conversationWith(domain.RoleProductManager)
	for _, role := range domain.Roles() {
		t.Run(string(role), func(t *testing.T) {
			events := []execution.Event{appliedTo(t, 1, "yoyodyne-ifd.138", "", map[string]any{
				"action":   "update",
				"id":       "yoyodyne-ifd.138",
				"executor": string(domain.ConversationWith(role)),
				"reason":   "no run carries this one",
			})}
			_, message := said(t, conversation, events, 0)
			if !strings.Contains(message.Body, "the "+role.Title()+"'s conversation") {
				t.Fatalf("body %q does not name the %s", message.Body, role)
			}
			if !strings.HasSuffix(message.Body, nextMoveLead+"the "+role.Title()+"'s, in conversation — no run will ever be started for this.") {
				t.Fatalf("body %q does not leave the move with the %s", message.Body, role)
			}
		})
	}
}

// Work marked before the marker named a role is still narrated, and is still
// narrated as unattributed. The record does not say whose conversation it went
// to, and a thread that picked a role would send the operator to somebody who
// was never handed the item — which is worse than the silence it replaced.
func TestAHandoffWhoseMarkerNamesNoRoleSaysOnlyWhatTheRecordHolds(t *testing.T) {
	conversation := conversationWith(domain.RoleProductManager)
	events := []execution.Event{appliedTo(t, 1, "yoyodyne-ifd.138", "", map[string]any{
		"action":   "update",
		"id":       "yoyodyne-ifd.138",
		"executor": string(domain.WorkItemExecutorConversation),
		"reason":   "marked before the marker carried a role",
	})}
	notification, message := said(t, conversation, events, 0)
	if notification.Event.Kind != KindWorkHandedOff {
		t.Fatalf("an unattributed marker is %s, want it still read as a handoff", notification.Event.Kind)
	}
	if !strings.Contains(message.Body, "a role's conversation") {
		t.Fatalf("body %q does not say a conversation carries it", message.Body)
	}
	if !strings.HasSuffix(message.Body, nextMoveLead+nextMoves[KindWorkHandedOff]) {
		t.Fatalf("body %q names a role the record never did", message.Body)
	}
}

// The routing being recorded is not the routing being acted on, and the second
// is what a reader waiting on a handoff is actually waiting for. It is said once
// per conversation: a role carrying work writes on the item repeatedly, and a
// pickup said every time would stop meaning anything.
func TestTheFirstThingARoleDoesToHandedWorkIsSaidAsPickingItUp(t *testing.T) {
	conversation := conversationWith(domain.RoleArchitect)
	events := []execution.Event{
		appliedTo(t, 1, "yoyodyne-ifd.138", "conversation", map[string]any{
			"action": "read",
			"id":     "yoyodyne-ifd.138",
		}),
		appliedTo(t, 2, "yoyodyne-ifd.138", "conversation", map[string]any{
			"action": "update",
			"id":     "yoyodyne-ifd.138",
			"note":   "the revision the brief needs",
			"reason": "starting on the promotion",
		}),
		appliedTo(t, 3, "yoyodyne-ifd.138", "conversation", map[string]any{
			"action": "update",
			"id":     "yoyodyne-ifd.138",
			"note":   "the second half of it",
			"reason": "carrying on",
		}),
	}
	// Reading an item is what a role does before deciding anything, so it is not
	// the work starting.
	if notification, err := FromConversation(conversation, events, 0); err != nil || !notification.Silent() {
		t.Fatalf("a read said %s (%v), want nothing", notification.Event.Kind, err)
	}
	notification, message := said(t, conversation, events, 1)
	if notification.Event.Kind != KindWorkPickedUp {
		t.Fatalf("the first change to handed work is %s", notification.Event.Kind)
	}
	if notification.Speaker.Role != domain.RoleArchitect {
		t.Fatalf("spoken by %q", notification.Speaker.Key())
	}
	// The pickup carries the item's id so posting resolves its current fields.
	if !strings.Contains(message.Body, "yoyodyne-ifd.138") {
		t.Fatalf("body %q loses the item taken up", message.Body)
	}
	if repeated, err := FromConversation(conversation, events, 2); err != nil || !repeated.Silent() {
		t.Fatalf("a second change said %s (%v), want the pickup said once", repeated.Event.Kind, err)
	}
}

// Keeping the queue around handed work is not carrying it. An item marked for a
// conversation is still attributed to a goal and still reordered, both are still
// what they always were, and a reading that took either for somebody starting the
// work would report a pickup nobody performed and lose the change that did
// happen.
func TestKeepingTheQueueAroundHandedWorkIsNotPickingItUp(t *testing.T) {
	conversation := conversationWith(domain.RoleProductManager)
	for _, tidying := range []struct {
		name   string
		action map[string]any
		want   Kind
		says   string
	}{
		{
			name: "reprioritize",
			action: map[string]any{
				"action":   "reprioritize",
				"id":       "yoyodyne-ifd.138",
				"priority": 1,
				"reason":   "the brief matters more than the rest of the epic",
			},
			want: KindItemReprioritized,
			says: "priority 1",
		},
		{
			name: "attribute",
			action: map[string]any{
				"action": "attribute",
				"id":     "yoyodyne-ifd.138",
				"goal":   "Run development nearly autonomously",
				"reason": "it was admitted before goals were checked",
			},
			want: KindItemAttributed,
			says: "Run development nearly autonomously",
		},
	} {
		t.Run(tidying.name, func(t *testing.T) {
			// The first act of the conversation, which is exactly where a pickup arm
			// reading every change would swallow it.
			events := []execution.Event{appliedTo(t, 1, "yoyodyne-ifd.138", "conversation", tidying.action)}
			notification, message := said(t, conversation, events, 0)
			if notification.Event.Kind != tidying.want {
				t.Fatalf("a %s on conversation-carried work is %s, want %s", tidying.name, notification.Event.Kind, tidying.want)
			}
			if !strings.Contains(message.Body, tidying.says) {
				t.Fatalf("body %q loses what the %s actually changed", message.Body, tidying.name)
			}
			// The item is still not queued for a run and never will be, so what
			// follows is the handoff's answer rather than the queue's.
			if !strings.HasSuffix(message.Body, nextMoveLead+nextMoves[KindWorkHandedOff]) {
				t.Fatalf("body %q says this is waiting for a run", message.Body)
			}
		})
	}
}

// Tidying the queue must not consume the pickup either: a conversation that
// reorders an item and then starts working on it has started working on it, and
// the reordering is no reason to leave the thread's most important message
// unsaid.
func TestQueueTidyingDoesNotConsumeThePickupThatFollowsIt(t *testing.T) {
	conversation := conversationWith(domain.RoleArchitect)
	events := []execution.Event{
		appliedTo(t, 1, "yoyodyne-ifd.138", "conversation", map[string]any{
			"action":   "reprioritize",
			"id":       "yoyodyne-ifd.138",
			"priority": 0,
			"reason":   "doing this one first",
		}),
		appliedTo(t, 2, "yoyodyne-ifd.138", "conversation", map[string]any{
			"action": "update",
			"id":     "yoyodyne-ifd.138",
			"note":   "the revision as promoted",
			"reason": "starting the promotion",
		}),
	}
	if reordered, _ := said(t, conversation, events, 0); reordered.Event.Kind != KindItemReprioritized {
		t.Fatalf("the reordering is %s", reordered.Event.Kind)
	}
	taken, _ := said(t, conversation, events, 1)
	if taken.Event.Kind != KindWorkPickedUp {
		t.Fatalf("the first act that carries the work is %s, want the pickup", taken.Event.Kind)
	}
}

// Handing work back to the run queue is the inverse of a handoff, and nothing
// reports it yet. Saying nothing is a gap; narrating it as a role taking the work
// up would be a message that is exactly wrong, so the executor being spoken about
// at all keeps the action out of the pickup.
func TestTakingTheMarkerOffWorkIsNeverSaidToBePickingItUp(t *testing.T) {
	conversation := conversationWith(domain.RoleProductManager)
	events := []execution.Event{recorded(t, 1, execution.EventTrackerActionApplied, map[string]any{
		"action_id": "t1.1",
		"turn":      1,
		"action": map[string]any{
			"action":   "update",
			"id":       "yoyodyne-ifd.138",
			"executor": "",
			"reason":   "a run can carry this after all",
		},
		"work_item_id":       "yoyodyne-ifd.138",
		"work_item_executor": "conversation",
		"summary":            "the harness's own account of it",
	})}
	notification, err := FromConversation(conversation, events, 0)
	if err != nil {
		t.Fatalf("select from a cleared executor: %v", err)
	}
	if notification.Event.Kind == KindWorkPickedUp {
		t.Fatal("work handed back to the run queue was said to have been picked up")
	}
}

// Closing an item is otherwise said by the run that finished it, and work a
// conversation carries has no run. Without this its thread simply stops, which
// is the silence that reads exactly like abandoned work.
func TestClosingWorkAConversationCarriedIsTheAccountOfItFinishing(t *testing.T) {
	conversation := conversationWith(domain.RoleArchitect)
	events := []execution.Event{appliedTo(t, 1, "yoyodyne-ifd.138", "conversation", map[string]any{
		"action": "close",
		"id":     "yoyodyne-ifd.138",
		"reason": "the brief is promoted and the revision is recorded",
	})}
	notification, message := said(t, conversation, events, 0)
	if notification.Event.Kind != KindWorkCarriedOut {
		t.Fatalf("closing work a conversation carried is %s", notification.Event.Kind)
	}
	if !strings.Contains(message.Body, "nobody's — the item is done") {
		t.Fatalf("body %q does not say the thread is waiting on nobody", message.Body)
	}
	// Ordinary work keeps the rule it always had: its run says it finished, and a
	// second account of that from the conversation would say one thing twice.
	ordinary := []execution.Event{appliedTo(t, 1, "yoyodyne-ifd.113", "", map[string]any{
		"action": "close",
		"id":     "yoyodyne-ifd.113",
		"reason": "the run landed it",
	})}
	if closed, err := FromConversation(conversation, ordinary, 0); err != nil || !closed.Silent() {
		t.Fatalf("closing ordinary work said %s (%v), want the run to say it", closed.Event.Kind, err)
	}
}

// The journey the operator read and found missing: an item admitted, routed to
// the architect after a run could not carry it, taken up there, and finished
// there. Replayed against the thread it should read end to end, with nothing
// between the handoff and the pickup left for the reader to infer.
func TestTheReroutedItemReadsCompleteInItsThread(t *testing.T) {
	admitted := conversationWith(domain.RoleProductManager)
	routed := conversationWith(domain.RoleDevelopmentManager)
	carried := conversationWith(domain.RoleArchitect)

	admission := []execution.Event{applied(t, 1, "yoyodyne-ifd.138", map[string]any{
		"action":      "create",
		"title":       "Promote the brief to its next revision",
		"description": "the item's own words",
		"goal":        "Run development nearly autonomously",
		"reason":      "the brief has outrun what it says",
	})}
	handoff := []execution.Event{appliedTo(t, 1, "yoyodyne-ifd.138", "", map[string]any{
		"action":   "update",
		"id":       "yoyodyne-ifd.138",
		"executor": "conversation",
		"reason":   "a run produced an empty diff; the architect owns the document",
	})}
	work := []execution.Event{
		appliedTo(t, 1, "yoyodyne-ifd.138", "conversation", map[string]any{
			"action": "update",
			"id":     "yoyodyne-ifd.138",
			"note":   "the revision as promoted",
			"reason": "starting the promotion",
		}),
		appliedTo(t, 2, "yoyodyne-ifd.138", "conversation", map[string]any{
			"action": "close",
			"id":     "yoyodyne-ifd.138",
			"reason": "the brief is promoted",
		}),
	}

	type step struct {
		conversation runstate.Conversation
		events       []execution.Event
		index        int
	}
	thread := []step{
		{admitted, admission, 0},
		{routed, handoff, 0},
		{carried, work, 0},
		{carried, work, 1},
	}
	wantKinds := []Kind{KindItemAdmitted, KindWorkHandedOff, KindWorkPickedUp, KindWorkCarriedOut}
	for position, taken := range thread {
		notification, message := said(t, taken.conversation, taken.events, taken.index)
		if notification.Event.Kind != wantKinds[position] {
			t.Fatalf("step %d of the journey is %s, want %s", position, notification.Event.Kind, wantKinds[position])
		}
		if notification.Topic.Key() != "work-item:yoyodyne-ifd.138" {
			t.Fatalf("step %d is addressed to %q, want the item's own thread", position, notification.Topic.Key())
		}
		// The whole point of the journey being readable is that no message in it
		// leaves the reader guessing who holds the ball next.
		if !strings.Contains(message.Body, nextMoveLead) {
			t.Fatalf("step %d reads as %q, which never says whose move follows", position, message.Body)
		}
	}
}

func TestAnEventOutsideTheLogIsRefusedRatherThanInvented(t *testing.T) {
	conversation := conversationWith(domain.RoleProductManager)
	if _, err := FromConversation(conversation, nil, 0); err == nil {
		t.Fatal("selecting from an empty log succeeded")
	}
}

func TestARecordThatCannotBeReadIsReportedRatherThanSaidWrongly(t *testing.T) {
	// A payload nothing can decode is a record nothing true can be said about, and
	// the sink reads past it once it has been told.
	conversation := conversationWith(domain.RoleProductManager)
	event := recorded(t, 1, execution.EventTrackerActionApplied, map[string]any{"action": "create"})
	event.Payload = json.RawMessage(`{"action":"not an action object"}`)
	if _, err := FromConversation(conversation, []execution.Event{event}, 0); err == nil {
		t.Fatal("an unreadable tracker payload was selected without complaint")
	}
}

// An ask exchange is the second thing a conversation does that an operator
// cannot otherwise see, and it reaches them in a thread of its own so one
// conversation between two roles can be followed without reading past
// everything else the asking conversation did.
func TestAnAskExchangeIsSaidInAThreadOfItsOwn(t *testing.T) {
	conversation := conversationWith(domain.RoleProductManager)
	exchangeID := "exchange-7f3a000000000000000000000000000a"
	events := []execution.Event{
		recorded(t, 1, execution.EventExchangeRound, map[string]any{
			"exchange": exchangeID,
			"asked":    "architect",
			"round":    1,
			"rounds":   10,
			"state":    "open",
			"cost_usd": 0.25,
			"question": "what does this goal cost, and what am I missing?",
			"text":     "More than the ordering assumes.",
		}),
		recorded(t, 2, execution.EventExchangeClosed, map[string]any{
			"exchange": exchangeID,
			"asked":    "architect",
			"round":    1,
			"rounds":   10,
			"state":    "resolved",
			"outcome":  "resolved",
			"cost_usd": 0.25,
			"question": "what does this goal cost, and what am I missing?",
			"text":     "",
		}),
	}

	notification, message := said(t, conversation, events, 0)
	if notification.Event.Kind != KindExchangeTurn {
		t.Fatalf("a round is %s", notification.Event.Kind)
	}
	if notification.Topic.Kind != TopicExchange || notification.Topic.ID != exchangeID {
		t.Fatalf("topic = %+v, want the exchange", notification.Topic)
	}
	// The words are the architect's, so the architect speaks them: agent-authored
	// text is posted as its author wrote it, and an architect's judgement in the
	// product manager's voice would attribute an opinion to a persona that did not
	// hold it.
	if notification.Speaker.Role != domain.RoleArchitect {
		t.Fatalf("speaker = %+v, want the answering role", notification.Speaker)
	}
	for _, wanted := range []string{"round 1 of 10", "More than the ordering assumes."} {
		if !strings.Contains(message.Body, wanted) {
			t.Fatalf("body %q does not carry %q", message.Body, wanted)
		}
	}

	closed, closing := said(t, conversation, events, 1)
	if closed.Event.Kind != KindExchangeClosed || closed.Event.Severity != report.SeverityNote {
		t.Fatalf("a resolved close is %s at %s", closed.Event.Kind, closed.Event.Severity)
	}
	// A closing is the harness saying an exchange is over, which is nobody's
	// opinion.
	if !closed.Speaker.IsHarness() {
		t.Fatalf("a closing speaker = %+v, want the harness", closed.Speaker)
	}
	if !strings.Contains(closing.Body, "resolved") {
		t.Fatalf("body %q does not say how it ended", closing.Body)
	}
}

// The ending nobody wants is the one worth interrupting for: an exchange that
// reached its cap says what it left unsettled, at a severity that carries.
func TestAnExchangeClosedAtItsCapIsSaidAsUnresolved(t *testing.T) {
	conversation := conversationWith(domain.RoleArchitect)
	events := []execution.Event{recorded(t, 1, execution.EventExchangeClosed, map[string]any{
		"exchange": "exchange-7f3a000000000000000000000000000b",
		"asked":    "product-manager",
		"round":    10,
		"rounds":   10,
		"state":    "unresolved-after-rounds",
		"outcome":  "unresolved-after-rounds",
		"cost_usd": 2.5,
		"question": "if we sacrifice some performance, is that an unacceptable trade-off?",
		"text":     "",
	})}
	notification, message := said(t, conversation, events, 0)
	if notification.Event.Severity != report.SeverityWarning {
		t.Fatalf("an unresolved close is %s, want warning", notification.Event.Severity)
	}
	for _, wanted := range []string{"unresolved at its round cap", "unacceptable trade-off"} {
		if !strings.Contains(message.Body, wanted) {
			t.Fatalf("body %q does not carry %q", message.Body, wanted)
		}
	}
}

// A round the record cannot be read from says so rather than being posted with
// the exchange it belongs to left blank.
func TestAnUnreadableExchangeRecordIsRefusedRatherThanSaid(t *testing.T) {
	conversation := conversationWith(domain.RoleProductManager)
	events := []execution.Event{recorded(t, 1, execution.EventExchangeRound, map[string]any{
		"asked": "architect",
		"round": 1,
	})}
	if _, err := FromConversation(conversation, events, 0); err == nil {
		t.Fatal("a round naming no exchange was said anyway")
	}
}

// A block of tracker actions the harness would not read produces no per-action
// record at all, so before this the channel said nothing about a dozen
// admissions and dispositions that never happened. It is said as a warning: the
// queue did not move, nobody chose that, and the role that asked believed it had.
func TestARefusedTrackerBlockIsSaidAgainstTheProductWithWhatItCost(t *testing.T) {
	conversation := conversationWith(domain.RoleProductManager)
	events := []execution.Event{recorded(t, 1, execution.EventTrackerBlockRefused, map[string]any{
		"turn":    1,
		"role":    string(domain.RoleProductManager),
		"actions": 12,
		"problem": "the product manager asked for tracker actions the harness cannot read: decode tracker actions: 12 actions in one reply, limit is 10",
	})}

	notification, message := said(t, conversation, events, 0)
	if notification.Event.Kind != KindTrackerBlockRefused {
		t.Fatalf("kind = %q, want %q", notification.Event.Kind, KindTrackerBlockRefused)
	}
	// Nothing changed about any one item, so it belongs to the line rather than
	// to a thread it would misfile itself in.
	if notification.Topic.Kind != TopicProduct {
		t.Fatalf("topic = %+v, want the product", notification.Topic)
	}
	// The harness refused it, so the harness says so — and the role that asked is
	// in the message rather than in the display name.
	if !notification.Speaker.IsHarness() {
		t.Fatalf("speaker = %+v, want the harness", notification.Speaker)
	}
	if notification.Event.Severity != report.SeverityWarning {
		t.Fatalf("severity = %q, want a warning", notification.Event.Severity)
	}
	if notification.Event.Refs.ConversationID != conversation.ConversationID {
		t.Fatalf("refs = %+v, want the conversation it was refused in", notification.Event.Refs)
	}
	for _, wanted := range []string{"product manager", "12 tracker actions", "limit is 10"} {
		if !strings.Contains(message.Body, wanted) {
			t.Fatalf("message %q does not say %q", message.Body, wanted)
		}
	}
	// Whose move it is, since the actions come back only if that role issues them
	// again: nothing else in the harness will.
	if !strings.Contains(message.Body, "the role that asked") {
		t.Fatalf("message %q does not say whose move follows it", message.Body)
	}
}

// A refused block the harness woke a role for and got another refused block from
// is the loss with the repair spent, so it is said at critical rather than as a
// second warning about an unrelated block.
func TestARefusalTheHarnessWokeAndLostAgainIsSaidAsTheOperatorsToLookAt(t *testing.T) {
	conversation := conversationWith(domain.RoleProductManager)
	events := []execution.Event{recorded(t, 1, execution.EventTrackerRefusalUnresolved, map[string]any{
		"turn":          6,
		"role":          string(domain.RoleProductManager),
		"actions":       4,
		"problem":       "the product manager asked for tracker actions the harness cannot read: decode tracker actions: unexpected trailing content after the actions",
		"previous":      "the product manager asked for tracker actions the harness cannot read: decode tracker actions: actions[0]: reason is the parking reason at 512 bytes, limit is 480",
		"woken":         true,
		"refused_again": true,
	})}

	notification, message := said(t, conversation, events, 0)
	if notification.Event.Kind != KindTrackerRefusalUnresolved {
		t.Fatalf("kind = %q, want %q", notification.Event.Kind, KindTrackerRefusalUnresolved)
	}
	if notification.Event.Severity != report.SeverityCritical {
		t.Fatalf("severity = %q, want a critical: the harness has spent its attempt and the actions are still lost", notification.Event.Severity)
	}
	if notification.Topic.Kind != TopicProduct || !notification.Speaker.IsHarness() {
		t.Fatalf("topic = %+v, speaker = %+v, want the harness speaking to the product", notification.Topic, notification.Speaker)
	}
	// What the harness did about it is in the message, because a reader deciding
	// whether to step in needs to know the automatic path has already been tried.
	for _, wanted := range []string{"woke this conversation", "4 tracker actions", "trailing content"} {
		if !strings.Contains(message.Body, wanted) {
			t.Fatalf("message %q does not say %q", message.Body, wanted)
		}
	}
	if !strings.Contains(message.Body, "the operator's") {
		t.Fatalf("message %q does not say whose move follows it", message.Body)
	}
}

// The other path into the same record: two unreadable blocks in a row in a
// conversation somebody was driving by hand, with no wakeup ever made. The
// message must not claim the harness had woken anybody, because it had not.
func TestARefusalNothingAnsweredDoesNotClaimTheHarnessWokeTheRole(t *testing.T) {
	conversation := conversationWith(domain.RoleProductManager)
	refusal := "the product manager asked for tracker actions the harness cannot read: decode tracker actions: actions[0]: handle report \"the-third-one\" is not a report identifier; a report is named exactly as it was listed to you"
	events := []execution.Event{recorded(t, 1, execution.EventTrackerRefusalUnresolved, map[string]any{
		"turn":          3,
		"role":          string(domain.RoleProductManager),
		"actions":       2,
		"problem":       refusal,
		"previous":      refusal,
		"woken":         false,
		"refused_again": true,
	})}

	_, message := said(t, conversation, events, 0)
	if strings.Contains(message.Body, "woke") || strings.Contains(message.Body, "woken") {
		t.Fatalf("message %q claims a wakeup the harness never made", message.Body)
	}
	// And the same defect twice is said as that, because it is the difference
	// between a role that cannot get one action right and one making a fresh
	// mistake.
	if !strings.Contains(message.Body, "exactly the same one") {
		t.Fatalf("message %q does not say the same defect came back", message.Body)
	}
	if !strings.Contains(message.Body, "never answered") {
		t.Fatalf("message %q does not say the earlier refusal went unanswered", message.Body)
	}
}

// The third way a refusal goes unanswered, and the quietest: the turn the harness
// woke came back in prose with no tracker action at all. Nothing was refused on
// it, so a message written around a second refusal would describe something that
// did not happen — and this ending would otherwise reach nobody, because the
// wakeup is spent and no second refusal is coming.
func TestAWokenTurnThatReIssuedNothingIsSaidWithoutInventingASecondRefusal(t *testing.T) {
	conversation := conversationWith(domain.RoleProductManager)
	events := []execution.Event{recorded(t, 1, execution.EventTrackerRefusalUnresolved, map[string]any{
		"turn":          7,
		"role":          string(domain.RoleProductManager),
		"actions":       5,
		"problem":       "the product manager asked for tracker actions the harness cannot read: decode tracker actions: 12 actions in one reply, limit is 10",
		"woken":         true,
		"refused_again": false,
	})}

	notification, message := said(t, conversation, events, 0)
	if notification.Event.Severity != report.SeverityCritical {
		t.Fatalf("severity = %q, want a critical: the wakeup is spent and the actions are still lost", notification.Event.Severity)
	}
	if !strings.Contains(message.Body, "asked for no tracker action at all") {
		t.Fatalf("message %q does not say the woken turn put nothing back", message.Body)
	}
	// It must not read as a second refused block: nothing was refused on this turn.
	for _, invented := range []string{"refused too", "the same refusal back", "second block"} {
		if strings.Contains(message.Body, invented) {
			t.Fatalf("message %q invents a second refusal: %q", message.Body, invented)
		}
	}
	// What is still lost is the block the wakeup was for, counted from that record
	// rather than from a turn that refused nothing.
	if !strings.Contains(message.Body, "5 tracker actions") {
		t.Fatalf("message %q does not say how much is still lost", message.Body)
	}
}

// A refusal the harness handed back within the same message is said as that,
// both ways it can end: the block sent back refused too, and a round that put
// nothing back. Neither claims a wakeup, because none was made.
func TestAHandBackThatDidNotTakeIsSaidAsAHandBack(t *testing.T) {
	conversation := conversationWith(domain.RoleProductManager)
	for _, testCase := range []struct {
		refusedAgain bool
		want         string
	}{
		{refusedAgain: true, want: "handed the refusal back within the same message, and the block it sent back was refused too"},
		{refusedAgain: false, want: "handed the refusal back within the same message and the round it took asked for no tracker action at all"},
	} {
		events := []execution.Event{recorded(t, 1, execution.EventTrackerRefusalUnresolved, map[string]any{
			"turn":          2,
			"role":          string(domain.RoleProductManager),
			"actions":       1,
			"problem":       "the product manager asked for tracker actions the harness cannot read: decode tracker actions: the tracker block is empty",
			"previous":      "the product manager asked for tracker actions the harness cannot read: invalid tracker actions: actions[0]: a title is 212 bytes, limit is 200",
			"woken":         false,
			"handed_back":   true,
			"refused_again": testCase.refusedAgain,
		})}
		_, message := said(t, conversation, events, 0)
		if !strings.Contains(message.Body, testCase.want) {
			t.Fatalf("message %q does not say %q", message.Body, testCase.want)
		}
		if strings.Contains(message.Body, "woke") {
			t.Fatalf("message %q claims a wakeup the harness never made", message.Body)
		}
	}
}

// The fourth way, and the one no turn was taken on: every wakeup the harness
// made met a provider that never took it until the attempts ran out. It is said
// critically like the others, and it must name the provider rather than any
// turn of the role's, because the fix is a login or an outage ending.
func TestWakeupsThatNeverReachedTheRoleAreSaidAsTheProviders(t *testing.T) {
	conversation := conversationWith(domain.RoleProductManager)
	events := []execution.Event{recorded(t, 1, execution.EventTrackerRefusalUnresolved, map[string]any{
		"turn":          4,
		"role":          string(domain.RoleProductManager),
		"actions":       2,
		"problem":       "the product manager asked for tracker actions the harness cannot read: decode tracker actions: the tracker block is empty",
		"woken":         false,
		"refused_again": false,
		"attempts":      3,
		"never_taken":   "the provider answered nobody, so the wakeup was never taken: not logged in",
	})}

	notification, message := said(t, conversation, events, 0)
	if notification.Event.Severity != report.SeverityCritical {
		t.Fatalf("severity = %q, want a critical: the harness has stopped trying and the actions are still lost", notification.Event.Severity)
	}
	if !strings.Contains(message.Body, "tried 3 times") || !strings.Contains(message.Body, "provider never took the turn") {
		t.Fatalf("message %q does not say the provider was what the wakeups met", message.Body)
	}
	for _, invented := range []string{"asked for no tracker action", "refused too", "never answered"} {
		if strings.Contains(message.Body, invented) {
			t.Fatalf("message %q describes a turn nobody took: %q", message.Body, invented)
		}
	}
}

// A block nobody could count is not a block that asked for nothing, and the
// message must not read as one.
func TestARefusedTrackerBlockNobodyCouldCountSaysSo(t *testing.T) {
	conversation := conversationWith(domain.RoleArchitect)
	events := []execution.Event{recorded(t, 1, execution.EventTrackerBlockRefused, map[string]any{
		"turn":    2,
		"role":    string(domain.RoleArchitect),
		"actions": 0,
		"problem": "the architect asked for tracker actions the harness cannot read: decode tracker actions: unexpected end of JSON input",
	})}

	_, message := said(t, conversation, events, 0)
	if !strings.Contains(message.Body, "does not count") {
		t.Fatalf("message %q does not state the count as absent", message.Body)
	}
	if strings.Contains(message.Body, "no tracker actions") {
		t.Fatalf("message %q reads an uncounted block as an empty one", message.Body)
	}
	if !strings.Contains(message.Body, "architect") {
		t.Fatalf("message %q does not name the role that asked", message.Body)
	}
}
