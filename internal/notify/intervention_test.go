package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/intervention"
)

func handStep(unblocked *intervention.Unblocked, items ...string) intervention.Event {
	at := time.Date(2026, 10, 24, 18, 49, 0, 0, time.UTC)
	return intervention.Event{
		SchemaVersion: intervention.SchemaVersion,
		ID:            "intervention-0123456789abcdef0123456789abcdef",
		ProductID:     "yoyodyne",
		Kind:          intervention.KindRestart,
		At:            at,
		Items:         items,
		Said:          "restarted the dashboard by hand",
		Unblocked:     unblocked,
		Observed:      true,
		By:            "the operator's assistant",
		RecordedBy:    "factory-flow-pm",
		RecordedAt:    at,
	}
}

// A hand step is said in the thread of what it unblocked: an item's own, a
// run's item's, and the product line for a part, a conversation, or nothing.
// A message the operator sent a role is in its conversation already and posts
// nothing.
func TestAHandStepIsSaidInTheThreadOfWhatItUnblocked(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		event     intervention.Event
		runItem   string
		wantTopic Topic
	}{
		{"an item", handStep(&intervention.Unblocked{Kind: intervention.UnblockedItem, ID: "yoyodyne-ifd.436.8"}), "", Topic{Kind: TopicWorkItem, ID: "yoyodyne-ifd.436.8"}},
		{"a run", handStep(&intervention.Unblocked{Kind: intervention.UnblockedRun, ID: "run-1234"}), "yoyodyne-ifd.434.9", Topic{Kind: TopicWorkItem, ID: "yoyodyne-ifd.434.9"}},
		{"a part", handStep(&intervention.Unblocked{Kind: intervention.UnblockedPart, ID: "dashboard"}, "yoyodyne-ifd.1"), "", Product()},
		{"nothing said", handStep(nil, "yoyodyne-ifd.1"), "", Topic{Kind: TopicWorkItem, ID: "yoyodyne-ifd.1"}},
	}
	for _, tc := range cases {
		notification, err := FromIntervention(tc.event, tc.runItem)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if notification.Topic.Kind != tc.wantTopic.Kind || notification.Topic.ID != tc.wantTopic.ID {
			t.Errorf("%s: topic = %+v, want %+v", tc.name, notification.Topic, tc.wantTopic)
		}
		message, err := Render(notification.Topic, notification.Speaker, notification.Event)
		if err != nil {
			t.Fatalf("%s: render: %v", tc.name, err)
		}
		if !strings.Contains(message.Body, "restarted the dashboard by hand.") || !strings.Contains(message.Body, "the operator's assistant") {
			t.Errorf("%s: said %q, want who took the step and what was done", tc.name, message.Body)
		}
	}
	message := handStep(nil)
	message.Kind = intervention.KindMessage
	message.Observed, message.By, message.RecordedBy, message.Via = false, "", "", "Slack"
	notification, err := FromIntervention(message, "")
	if err != nil || notification.Posts() {
		t.Errorf("a message the operator sent posted %+v, %v; want nothing", notification, err)
	}
}
