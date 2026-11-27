package chat

import (
	"context"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
)

const undeclaredLine = "Environments: android client (criteria 1, 2); ios client (criterion 3)"

func declaredAndroid() []config.Environment {
	return []config.Environment{{Name: "android client", Check: "scripts/ready-android.sh", Test: "scripts/integration-android.sh"}}
}

// An item whose Environments: line names an environment the project does not
// declare is refused where it is created, rewritten or proposed, quoting the
// line; one naming only declared environments, or with no line, is written.
func TestAnItemNamingAnUndeclaredEnvironmentIsRefusedAtEveryDoorIntoTheQueue(t *testing.T) {
	t.Parallel()

	wanted := []string{undeclaredLine, `"ios client"`, "does not declare"}

	t.Run("create", func(t *testing.T) {
		t.Parallel()
		tracker := &fakeTracker{}
		provider := &fakeBackend{results: []backendapi.RunResult{
			{SessionID: "session-1", FinalText: trackerReply("Admitting two.",
				`{"action":"create","kind":"feature","title":"Build the clients","description":"Builds both clients.\n`+undeclaredLine+`","goal":"Run development nearly autonomously.","reason":"the operator directed it"}`,
				`{"action":"create","kind":"feature","title":"Build the android client","description":"Builds it.\nEnvironments: android client (criterion 1)","goal":"Run development nearly autonomously.","reason":"the operator directed it"}`)},
			{SessionID: "session-1", FinalText: "One refused."},
		}}
		options := testOptions(t, provider)
		options.Tracker = tracker
		options.Goals = recordedGoals("Run development nearly autonomously.")
		options.Environments = declaredAndroid()
		session := openTestSession(t, options)

		reply, err := session.Send(context.Background(), "Admit the client work.")
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		if len(reply.Actions) != 2 || reply.Actions[0].Applied || !reply.Actions[1].Applied {
			t.Fatalf("actions = %#v, want the first refused and the second created", reply.Actions)
		}
		for _, want := range wanted {
			if !strings.Contains(reply.Actions[0].Failure, want) {
				t.Fatalf("refusal %q never says %q", reply.Actions[0].Failure, want)
			}
		}
		if len(tracker.created) != 1 || tracker.created[0].Title != "Build the android client" {
			t.Fatalf("created = %#v, want only the item naming a declared environment", tracker.created)
		}
	})

	t.Run("update", func(t *testing.T) {
		t.Parallel()
		tracker := &fakeTracker{items: map[string]beads.WorkItem{
			"yoyodyne-ifd.1": {ID: "yoyodyne-ifd.1", Title: "Build the clients", Description: "Builds both clients.", Status: "open"},
		}}
		provider := &fakeBackend{results: []backendapi.RunResult{
			{SessionID: "session-1", FinalText: trackerReply("Naming the environments.",
				`{"action":"update","id":"yoyodyne-ifd.1","description":"Builds both clients.\n`+undeclaredLine+`","reason":"say what the criteria need"}`)},
			{SessionID: "session-1", FinalText: "Refused."},
		}}
		options := testOptions(t, provider)
		options.Tracker = tracker
		options.Environments = declaredAndroid()
		session := openTestSession(t, options)

		reply, err := session.Send(context.Background(), "Name the environments on it.")
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		if len(tracker.updates) != 0 || len(reply.Actions) != 1 || reply.Actions[0].Applied {
			t.Fatalf("updates = %#v, actions = %#v; want the update refused", tracker.updates, reply.Actions)
		}
		for _, want := range wanted {
			if !strings.Contains(reply.Actions[0].Failure, want) {
				t.Fatalf("refusal %q never says %q", reply.Actions[0].Failure, want)
			}
		}
	})

	t.Run("proposal", func(t *testing.T) {
		t.Parallel()
		tracker := &fakeTracker{}
		options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
			SessionID: "session-1",
			FinalText: proposalReply("One item follows.",
				`{"kind":"feature","title":"Build the clients","description":"Builds both clients.\n`+undeclaredLine+`","rationale":"the clients need it","goal":"`+recordedGoal+`"}`),
		}}})
		options.Tracker = tracker
		options.Goals = recordedGoals(recordedGoal)
		options.Environments = declaredAndroid()
		session := openTestSession(t, options)

		reply, err := session.Send(context.Background(), "what follows")
		if err != nil || len(reply.RefusedBlocks) != 1 {
			t.Fatalf("Send() = %q, %v, want the proposal refused alone", reply.RefusedBlocks, err)
		}
		for _, want := range wanted {
			if !strings.Contains(reply.RefusedBlocks[0], want) {
				t.Fatalf("refusal %q never says %q", reply.RefusedBlocks[0], want)
			}
		}
		if len(reply.Proposals) != 0 || len(reply.Admitted) != 0 || len(tracker.created) != 0 {
			t.Fatalf("proposals = %#v, admitted = %#v, created = %#v; want nothing put to the operator or the queue", reply.Proposals, reply.Admitted, tracker.created)
		}
	})

	t.Run("no line", func(t *testing.T) {
		t.Parallel()
		tracker := &fakeTracker{}
		provider := &fakeBackend{results: []backendapi.RunResult{
			{SessionID: "session-1", FinalText: trackerReply("Admitting one.",
				`{"action":"create","kind":"feature","title":"Build the clients","description":"Builds both clients.","goal":"Run development nearly autonomously.","reason":"the operator directed it"}`)},
			{SessionID: "session-1", FinalText: "Done."},
		}}
		options := testOptions(t, provider)
		options.Tracker = tracker
		options.Goals = recordedGoals("Run development nearly autonomously.")
		session := openTestSession(t, options)

		reply, err := session.Send(context.Background(), "Admit the client work.")
		if err != nil || len(reply.Actions) != 1 || !reply.Actions[0].Applied || len(tracker.created) != 1 {
			t.Fatalf("Send() = %#v, %v; want an item with no line created as before", reply.Actions, err)
		}
	})
}
