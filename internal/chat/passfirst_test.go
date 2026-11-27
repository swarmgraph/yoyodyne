package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// The Lead Product Manager names a developer's item for the development
// manager's next pass with the step she owes, and the request is written with
// who, when, which role and why, beside a note saying so. Dropping one writes
// the ending and a note naming what it had been.
func TestTheLeadProductManagerPutsAnItemFirstInARolesPassAndDropsIt(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-9k1y.22": {ID: "yoyodyne-9k1y.22", Title: "Delivered epics", Status: "open", Priority: 0},
		"yoyodyne-old": {ID: "yoyodyne-old", Title: "An earlier step", Status: "open", Priority: 1, PassFirst: &beads.PassFirst{
			Role: domain.RoleArchitect, By: domain.RoleProductManager, At: time.Date(2026, 11, 27, 18, 0, 0, 0, time.UTC), Reason: "rule on it"}},
	}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Putting the hold-lift first in her pass and dropping the old request.",
			`{"action":"put-first","id":"yoyodyne-9k1y.22","to":"development-manager","reason":"lift the design hold so the fix lands by Saturday"}`,
			`{"action":"drop-first","id":"yoyodyne-old","reason":"the architect ruled already"}`)},
		{SessionID: "session-1", FinalText: "Done."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Make sure the development manager lifts the hold first.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.updates) != 2 {
		t.Fatalf("updates = %#v, want the request and the drop", tracker.updates)
	}
	named := tracker.updates[0]
	if named.id != "yoyodyne-9k1y.22" || named.change.PassFirst == nil {
		t.Fatalf("put-first recorded %#v", named)
	}
	written := *named.change.PassFirst
	if written.Role != domain.RoleDevelopmentManager || written.By != domain.RoleProductManager || written.At.IsZero() ||
		written.Reason != "lift the design hold so the fix lands by Saturday" {
		t.Fatalf("request written = %#v", written)
	}
	if !strings.Contains(named.change.AppendNotes, "Named first for the development manager's next pass") {
		t.Fatalf("put-first notes = %q", named.change.AppendNotes)
	}
	dropped := tracker.updates[1]
	if dropped.id != "yoyodyne-old" || dropped.change.PassFirst == nil || *dropped.change.PassFirst != (beads.PassFirst{}) ||
		!strings.Contains(dropped.change.AppendNotes, "It had been named first for the architect's next pass") {
		t.Fatalf("drop-first recorded %#v, want the request ended with what it had been", dropped)
	}
	rendered := renderTrackerOutcomes(domain.RoleProductManager, reply.Actions)
	for _, required := range []string{"named yoyodyne-9k1y.22 for the development manager's next pass", "dropped the request that had yoyodyne-old"} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered outcomes = %q, want %q", rendered, required)
		}
	}
}

// A request to a role whose pass reads no single queue is refused before
// anything is written, and so is one naming no role.
func TestPutFirstNamesARoleWhosePassTakesOneOrder(t *testing.T) {
	t.Parallel()

	for _, to := range []string{"", "program-manager", "operator"} {
		action := TrackerAction{Action: actionPutFirst, ID: "yoyodyne-9k1y.22", To: to, Reason: "now"}
		if err := action.Validate(); err == nil || !strings.Contains(err.Error(), `"development-manager" or "architect"`) {
			t.Errorf("put-first to %q: Validate() = %v, want it refused naming the roles it can name", to, err)
		}
	}
	if err := (TrackerAction{Action: actionPutFirst, ID: "yoyodyne-9k1y.22", To: "architect", Reason: "now"}).Validate(); err != nil {
		t.Errorf("put-first to the architect: Validate() = %v", err)
	}
}
