package chat

import (
	"context"
	"slices"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// advancingClock is a clock a test moves by hand.
type advancingClock struct{ now time.Time }

func (c *advancingClock) Now() time.Time { return c.now }

func countShown(shown []string, id string) int {
	n := 0
	for _, s := range shown {
		if s == id {
			n++
		}
	}
	return n
}

func siblingsUnderOneParent() map[string]beads.WorkItem {
	return map[string]beads.WorkItem{
		"yoyodyne-a.1": {ID: "yoyodyne-a.1", Title: "First", Status: "open", Parent: "yoyodyne-a"},
		"yoyodyne-a.2": {ID: "yoyodyne-a.2", Title: "Second", Status: "open", Parent: "yoyodyne-a"},
		"yoyodyne-a": {ID: "yoyodyne-a", Title: "Parent", Status: "open",
			Dependencies: []beads.Dependency{{ID: "yoyodyne-b", Type: beads.BlocksDependency, Status: "open"}}},
		"yoyodyne-b": {ID: "yoyodyne-b", Title: "Blocker", Status: "open"},
	}
}

// Every `bd show` waits to be let into the store, so reading many items under a
// few parents must not read the parents again for each of them: the read of the
// second sibling asks the tracker for neither the parent nor what it waits on.
func TestReadingSiblingsReadsTheirParentOnce(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Reading.",
			`{"action":"read","id":"yoyodyne-a.1"}`, `{"action":"read","id":"yoyodyne-a.2"}`)},
		{SessionID: "session-1", FinalText: "Read."},
	}}
	options := testOptions(t, provider)
	tracker := &fakeTracker{items: siblingsUnderOneParent()}
	options.Tracker = tracker
	session := openTestSession(t, options)
	reply, err := session.Send(context.Background(), "Are they ready?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	for _, action := range reply.Actions {
		if !action.Applied || action.Detail == "" {
			t.Fatalf("action = %#v, want the read applied", action)
		}
	}
	for _, id := range []string{"yoyodyne-a", "yoyodyne-b"} {
		if got := countShown(tracker.shown, id); got != 1 {
			t.Errorf("%s was read %d times for two sibling reads, want once; reads: %v", id, got, tracker.shown)
		}
	}
}

// A parent held for a read is never used after this session wrote something
// (the write may be to that parent), and never past its age.
func TestAHeldParentIsReadAgainAfterAWriteOrItsAge(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{})
	clock := &advancingClock{now: fixedClock{}.Now()}
	options.Clock = clock
	tracker := &fakeTracker{items: siblingsUnderOneParent()}
	options.Tracker = tracker
	session := openTestSession(t, options)
	ctx := context.Background()

	for range 3 {
		if _, err := session.readAncestor(ctx, "yoyodyne-a"); err != nil {
			t.Fatal(err)
		}
	}
	if got := countShown(tracker.shown, "yoyodyne-a"); got != 1 {
		t.Fatalf("three reads asked the tracker %d times, want 1", got)
	}
	session.ancestors.clear()
	if _, err := session.readAncestor(ctx, "yoyodyne-a"); err != nil {
		t.Fatal(err)
	}
	if got := countShown(tracker.shown, "yoyodyne-a"); got != 2 {
		t.Fatalf("a read after a write asked the tracker %d times in all, want 2", got)
	}
	clock.now = clock.now.Add(ancestorReadsMaxAge)
	if _, err := session.readAncestor(ctx, "yoyodyne-a"); err != nil {
		t.Fatal(err)
	}
	if got := countShown(tracker.shown, "yoyodyne-a"); got != 3 {
		t.Fatalf("a read after its age asked the tracker %d times in all, want 3", got)
	}
	// A failed read is asked again rather than remembered.
	for range 2 {
		if _, err := session.readAncestor(ctx, "yoyodyne-missing"); err == nil {
			t.Fatal("reading an item the tracker lacks succeeded")
		}
	}
	if got := countShown(tracker.shown, "yoyodyne-missing"); got != 2 || !slices.Contains(tracker.shown, "yoyodyne-missing") {
		t.Fatalf("a failed read was asked %d times, want 2", got)
	}
}
