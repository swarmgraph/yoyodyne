package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

// What a run leaves on an item it landed evidence for rather than the work: the
// item parked on the run's account of what it still owes, and notes whose last
// word is that the item stays open.
const (
	evidenceParking = "yoyodyne run run-9e5347d7 landed evidence for this item rather than the work, and did not discharge it: a repeat approval while the first is landing still has to be refused, with a test"
	evidenceNotes   = "Yoyodyne run run-9e5347d7 landed evidence for this item and did not discharge it, so the item stays open with its change integrated and parked."
)

// An item a run parked on landed evidence is closed only by somebody deciding
// to: here a role's close or retire, recorded as that role's, in its own words.
// The role is told in its results that the item was parked and what the parking
// was waiting for, because closing it ends that work, and the decision is made
// where the results are read rather than found afterwards in notes that still
// say the item stays open.
func TestClosingAnItemParkedOnLandedEvidenceSaysWhatTheParkingWasWaitingFor(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-bhb.1": {ID: "yoyodyne-bhb.1", Title: "Approving from the command line", Status: "open",
			Parking: evidenceParking, Notes: evidenceNotes},
		"yoyodyne-ifd.429.27": {ID: "yoyodyne-ifd.429.27", Title: "Queued-merge check reads", Status: "open",
			Parking: evidenceParking, Notes: evidenceNotes},
		"yoyodyne-ifd.22": {ID: "yoyodyne-ifd.22", Title: "Ordinary open work", Status: "open"},
	}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Backlog review.",
			`{"action":"close","id":"yoyodyne-bhb.1","reason":"already done on main"}`,
			`{"action":"retire","id":"yoyodyne-ifd.429.27","reason":"obsolete"}`,
			`{"action":"close","id":"yoyodyne-ifd.22","reason":"the work landed"}`)},
		{SessionID: "session-1", FinalText: "Three items are out of the backlog."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Review the backlog.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.closed) != 3 || len(reply.Actions) != 3 {
		t.Fatalf("closed = %#v, actions = %d; want all three closed by the role's own actions", tracker.closed, len(reply.Actions))
	}
	// The closure is the role's, in its words, and says so on the item.
	for _, closed := range tracker.closed {
		if !strings.Contains(closed[1], "by the Lead Product Manager in conversation") {
			t.Fatalf("closure of %s recorded %q, want it to name the role that closed it", closed[0], closed[1])
		}
	}
	for index, want := range []string{"closed yoyodyne-bhb.1 as done", "retired yoyodyne-ifd.429.27 from the backlog without it being done"} {
		summary := reply.Actions[index].Summary
		for _, required := range []string{want, "it was parked, and closing it ends that parking", "a repeat approval while the first is landing still has to be refused"} {
			if !strings.Contains(summary, required) {
				t.Fatalf("summary = %q, want it to contain %q", summary, required)
			}
		}
	}
	// Work nobody parked is closed with nothing said about a parking.
	if summary := reply.Actions[2].Summary; strings.Contains(summary, "parked") {
		t.Fatalf("closing unparked work spoke of a parking: %q", summary)
	}
}

// A read of a closed item says who closed it, when, and why, ahead of the notes.
// Without it the notes' last word stood as the account, and an item a role had
// closed read as one closed by the harness against a run's note saying it stays
// open. A parking still on a closed item is what it was waiting for when it was
// closed, never a hold on work still to be pulled.
func TestAReadOfAClosedItemSaysWhoClosedItAndWhy(t *testing.T) {
	t.Parallel()

	closedAt := time.Date(2026, 11, 15, 21, 13, 35, 0, time.UTC)
	item := beads.WorkItem{
		ID: "yoyodyne-bhb.1", Title: "Approving from the command line", Status: "closed",
		ClosedAt:    closedAt,
		CloseReason: "Closed as done by the Lead Product Manager in conversation chat-1, after turn 5612.\n\nReason: Already done.",
		Parking:     evidenceParking,
		Notes:       evidenceNotes,
	}
	rendered, err := renderWorkItemRead(item, goal.Set{}, nil)
	if err != nil {
		t.Fatalf("renderWorkItemRead() error = %v", err)
	}
	closure := "closed: " + closedAt.Local().Format("2006-01-02 15:04 MST") +
		" — Closed as done by the Lead Product Manager in conversation chat-1, after turn 5612. Reason: Already done."
	if !strings.Contains(rendered, closure) {
		t.Fatalf("read = %q, want it to contain %q", rendered, closure)
	}
	if notes := strings.Index(rendered, "notes:"); notes < 0 || strings.Index(rendered, closure) > notes {
		t.Fatalf("the closure is not said ahead of the notes:\n%s", rendered)
	}
	if !strings.Contains(rendered, "parked when it was closed: "+evidenceParking) {
		t.Fatalf("read = %q, want the parking said as what the item was waiting for when it closed", rendered)
	}
	if strings.Contains(rendered, "no pull selects it") {
		t.Fatalf("a closed item's parking read as a hold on work still to be pulled:\n%s", rendered)
	}

	// A closure the tracker kept no reason for says so rather than saying nothing.
	item.CloseReason, item.Parking = "", ""
	rendered, err = renderWorkItemRead(item, goal.Set{}, nil)
	if err != nil {
		t.Fatalf("renderWorkItemRead() error = %v", err)
	}
	if !strings.Contains(rendered, "— the tracker recorded no reason") {
		t.Fatalf("read = %q, want a closure with no reason said as one", rendered)
	}

	// Open work carries no closure line, parked or not.
	item.Status, item.Parking = "open", domain.WorkItemParking(evidenceParking)
	rendered, err = renderWorkItemRead(item, goal.Set{}, nil)
	if err != nil {
		t.Fatalf("renderWorkItemRead() error = %v", err)
	}
	if strings.Contains(rendered, "\nclosed: ") || !strings.Contains(rendered, "no pull selects it") {
		t.Fatalf("open parked work read wrongly:\n%s", rendered)
	}
}
