package chat

import (
	"context"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// readItemAsRole has a role read one item through the tracker capability and
// returns what the read handed back.
func readItemAsRole(t *testing.T, items map[string]beads.WorkItem, id string) string {
	t.Helper()
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Reading it.", `{"action":"read","id":"`+id+`"}`)},
		{SessionID: "session-1", FinalText: "Read."},
	}}
	options := testOptions(t, provider)
	options.Tracker = &fakeTracker{items: items}
	session := openTestSession(t, options)
	reply, err := session.Send(context.Background(), "Is it ready?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the read applied", reply.Actions)
	}
	return reply.Actions[0].Detail
}

// The yoyodyne-ifd.433.21.1 shape, October 5: the slice recorded only its own
// parent link, the parent waited on the architect's design, and every read of
// the slice said nothing about it. The read now names the parent and what the
// parent waits on.
func TestAnItemReadSaysItIsBlockedThroughItsParent(t *testing.T) {
	t.Parallel()

	items := map[string]beads.WorkItem{
		"yoyodyne-ifd.433.21.1": {ID: "yoyodyne-ifd.433.21.1", Title: "First slice", Status: "open", IssueType: "task",
			Parent: "yoyodyne-ifd.433.21"},
		"yoyodyne-ifd.433.21": {ID: "yoyodyne-ifd.433.21", Title: "Automatic document publication", Status: "open", IssueType: "feature",
			Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.437.14", Type: beads.BlocksDependency, Status: "open"}}},
	}
	items["yoyodyne-ifd.437.14"] = beads.WorkItem{ID: "yoyodyne-ifd.437.14", Title: "Amendment ownership design", Status: "open", IssueType: "task"}
	detail := readItemAsRole(t, items, "yoyodyne-ifd.433.21.1")
	want := "inherited block: blocked through its parent 'Automatic document publication' (yoyodyne-ifd.433.21), " +
		"which waits on 'Amendment ownership design' (yoyodyne-ifd.437.14, open). " +
		"The item records no such link itself; the tracker holds it back with its parent, and offers it once that work is finished."
	if !strings.Contains(detail, want) {
		t.Fatalf("the read does not say the inherited block:\n%s", detail)
	}
}

// /show is the same read, so the operator is told what the role is told.
func TestShowSaysAnItemIsBlockedThroughItsParent(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{})
	options.Tracker = &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.433.21.1": {ID: "yoyodyne-ifd.433.21.1", Title: "First slice", Status: "open", IssueType: "task",
			Parent: "yoyodyne-ifd.433.21"},
		"yoyodyne-ifd.433.21": {ID: "yoyodyne-ifd.433.21", Title: "Automatic document publication", Status: "open", IssueType: "feature",
			Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.437.14", Type: beads.BlocksDependency, Status: "open"}}},
	}}
	options.Work = &fakeWork{}
	session := openTestSession(t, options)

	var out strings.Builder
	if err := session.Converse(context.Background(), testConsole(strings.NewReader("/show yoyodyne-ifd.433.21.1\n/exit\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	// The work the parent waits on is not one the tracker here describes, so it is
	// named by its identifier alone.
	if want := "inherited block: blocked through its parent 'Automatic document publication' (yoyodyne-ifd.433.21), which waits on yoyodyne-ifd.437.14 (open)."; !strings.Contains(out.String(), want) {
		t.Fatalf("/show does not say the inherited block:\n%s", out.String())
	}
}

// A chain of two: the item's parent waits on nothing, and the parent's parent
// does. Both links are named, so a reader can follow the chain to the wait.
func TestAnItemReadNamesBothLinksOfAnInheritedBlockThroughAChainOfTwo(t *testing.T) {
	t.Parallel()

	items := map[string]beads.WorkItem{
		"yoyodyne-ifd.9.1.1": {ID: "yoyodyne-ifd.9.1.1", Title: "Leaf", Status: "open", IssueType: "task", Parent: "yoyodyne-ifd.9.1"},
		"yoyodyne-ifd.9.1":   {ID: "yoyodyne-ifd.9.1", Title: "Middle", Status: "open", IssueType: "feature", Parent: "yoyodyne-ifd.9"},
		"yoyodyne-ifd.9": {ID: "yoyodyne-ifd.9", Title: "Top", Status: "open", IssueType: "epic",
			Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.12", Type: beads.BlocksDependency, Status: "in_progress"}}},
	}
	detail := readItemAsRole(t, items, "yoyodyne-ifd.9.1.1")
	want := "inherited block: blocked through its parent 'Middle' (yoyodyne-ifd.9.1), whose parent is 'Top' (yoyodyne-ifd.9), which waits on yoyodyne-ifd.12 (in_progress)."
	if !strings.Contains(detail, want) {
		t.Fatalf("the read does not name both links of the chain:\n%s", detail)
	}
}

// An item whose parents wait on nothing, or wait only on work the item already
// records itself, is not said to be blocked through anything.
func TestAnItemReadSaysNothingOfAnInheritedBlockWhereThereIsNone(t *testing.T) {
	t.Parallel()

	items := map[string]beads.WorkItem{
		"yoyodyne-ifd.5.1": {ID: "yoyodyne-ifd.5.1", Title: "Child", Status: "open", IssueType: "task", Parent: "yoyodyne-ifd.5",
			Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.6", Type: beads.BlocksDependency, Status: "open"}}},
		"yoyodyne-ifd.5": {ID: "yoyodyne-ifd.5", Title: "Parent", Status: "open", IssueType: "feature",
			Dependencies: []beads.Dependency{
				{ID: "yoyodyne-ifd.6", Type: beads.BlocksDependency, Status: "open"},
				{ID: "yoyodyne-ifd.7", Type: beads.BlocksDependency, Status: "closed"},
			}},
	}
	detail := readItemAsRole(t, items, "yoyodyne-ifd.5.1")
	if strings.Contains(detail, "inherited block") || strings.Contains(detail, "blocked through") {
		t.Fatalf("the read reports an inherited block where there is none:\n%s", detail)
	}
}

// A parent the tracker would not describe is said as unread rather than read as
// holding nothing.
func TestAnItemReadSaysWhenItsParentCouldNotBeRead(t *testing.T) {
	t.Parallel()

	items := map[string]beads.WorkItem{
		"yoyodyne-ifd.5.1": {ID: "yoyodyne-ifd.5.1", Title: "Child", Status: "open", IssueType: "task", Parent: "yoyodyne-ifd.5"},
	}
	detail := readItemAsRole(t, items, "yoyodyne-ifd.5.1")
	if !strings.Contains(detail, "inherited block: whether an item it was broken out of holds it back could not be read in full") {
		t.Fatalf("the read does not say the parent was unread:\n%s", detail)
	}
}
