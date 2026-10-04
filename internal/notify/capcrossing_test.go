package notify

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// crossingRecorded is a cap crossing as the conversation records one: the action, and
// what the store said the crossing actually came to.
func crossingRecorded(t *testing.T, budget string, cap, crossing, crossings int, reason string) []execution.Event {
	t.Helper()
	return []execution.Event{recorded(t, 1, execution.EventTrackerActionApplied, map[string]any{
		"action_id": "t1.1",
		"turn":      1,
		"action": map[string]any{
			"action":   "triage",
			"id":       "yoyodyne-ifd.143",
			"run":      "run-0123456789abcdef0123456789abcdef",
			"decision": "cross",
			"budget":   budget,
			"reason":   reason,
		},
		"work_item_id":    "yoyodyne-ifd.143",
		"work_item_title": "Unmeetable items do not dispatch",
		"crossing": map[string]any{
			"budget":    budget,
			"cap":       cap,
			"crossing":  crossing,
			"crossings": crossings,
		},
		"summary": "the harness's own account of it",
	})}
}

// The half of the delegation the operator kept. A crossing is in force the
// moment it is recorded, so the only thing standing between it and an item given
// room nobody wanted it to have is that the operator reads about it — with the
// item, the cap, which crossing it was, and the argument, all in the message.
func TestACapCrossingReachesTheOperatorWithTheCapTheCountAndTheReason(t *testing.T) {
	conversation := conversationWith(domain.RoleDevelopmentManager)
	events := crossingRecorded(t, "review round", 5, 2, 5,
		"the change was right and the ground moved under it; the rounds went on a base that has since landed")

	notification, message := said(t, conversation, events, 0)
	if notification.Event.Kind != KindCapCrossed {
		t.Fatalf("a crossing is reported as %s", notification.Event.Kind)
	}
	// A warning rather than a note, because a note is marked with nothing and
	// something read tomorrow is not a veto.
	if notification.Event.Severity != report.SeverityWarning {
		t.Fatalf("a crossing is reported at %q severity", notification.Event.Severity)
	}
	if notification.Topic.Key() != "work-item:yoyodyne-ifd.143" {
		t.Fatalf("addressed to %q", notification.Topic.Key())
	}
	// The role's own act, in the role's own voice: the harness only wrote it down.
	if notification.Speaker.Role != domain.RoleDevelopmentManager {
		t.Fatalf("spoken by %q", notification.Speaker.Key())
	}
	for _, want := range []string{
		// The outgoing surface resolves the identifier from the current tracker
		// rather than using the title saved with the crossing.
		"yoyodyne-ifd.143",
		"review round",
		"5",
		"crossing 2 of 5",
		"the ground moved under it",
	} {
		if !strings.Contains(message.Body, want) {
			t.Fatalf("the crossing message is missing %q:\n%s", want, message.Body)
		}
	}
	// The clause that says the operator still has a say, which is the whole of
	// what the delegation was granted on.
	if !strings.Contains(message.Body, "only if they disagree") {
		t.Fatalf("the crossing message does not leave the operator a move:\n%s", message.Body)
	}
}

// Every other triage decision stays out of the channel. Deciding about stopped
// work is the job the docket was delivered for, and a channel that narrated each
// decision would be the event log the vocabulary exists not to be — the crossing
// is reported because it moves a budget the project set, not because it is a
// decision.
func TestTheOtherTriageDecisionsSayNothingInTheChannel(t *testing.T) {
	conversation := conversationWith(domain.RoleDevelopmentManager)
	for _, decision := range []string{"repair", "rerun", "rescope", "rearm", "wait", "escalate"} {
		events := []execution.Event{applied(t, 1, "yoyodyne-ifd.143", map[string]any{
			"action":   "triage",
			"id":       "yoyodyne-ifd.143",
			"run":      "run-0123456789abcdef0123456789abcdef",
			"decision": decision,
			"reason":   "the docket entry says so",
		})}
		notification, err := FromConversation(conversation, events, 0)
		if err != nil {
			t.Fatalf("select a %q decision: %v", decision, err)
		}
		if !notification.Silent() {
			t.Fatalf("a %q decision was reported as %s", decision, notification.Event.Kind)
		}
	}
}

// A crossing whose figures the record does not carry is still said, with the
// absences stated. Withholding the message for want of a count would be the
// delegation quietly losing its condition to a schema change, which is the one
// way this could fail without anybody noticing.
func TestACrossingIsSaidEvenWhereTheRecordCarriesNoFigures(t *testing.T) {
	conversation := conversationWith(domain.RoleDevelopmentManager)
	events := []execution.Event{applied(t, 1, "yoyodyne-ifd.143", map[string]any{
		"action":   "triage",
		"id":       "yoyodyne-ifd.143",
		"run":      "run-0123456789abcdef0123456789abcdef",
		"decision": "cross",
		"budget":   "re-run",
		"reason":   "the ground moved",
	})}
	_, message := said(t, conversation, events, 0)
	for _, want := range []string{"re-run", "a ceiling the record does not carry", "a crossing the record does not count"} {
		if !strings.Contains(message.Body, want) {
			t.Fatalf("the crossing message is missing %q:\n%s", want, message.Body)
		}
	}
}
