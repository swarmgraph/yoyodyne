package beads

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func testPlacement(priority int) Placement {
	return Placement{
		By:       domain.RoleProductManager,
		At:       time.Date(2026, 10, 29, 13, 50, 0, 0, time.UTC),
		Priority: priority,
		Reason:   "promised first by the operator",
	}
}

// itemWithPlacement is bd's answer for one item whose metadata carries the
// placement as the harness writes it: one JSON object held as a string value.
func itemWithPlacement(t *testing.T, status string, priority int, placement *Placement, notes string) string {
	t.Helper()
	metadata := map[string]string{}
	if placement != nil {
		value, err := placement.encode()
		if err != nil {
			t.Fatal(err)
		}
		metadata[placedKey] = value
	}
	encoded, err := json.Marshal(map[string]any{
		"id": "yoyodyne-u6ei", "title": "t", "status": status, "priority": priority,
		"issue_type": "task", "notes": notes, "metadata": metadata,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestAPlacementIsWrittenToTheTrackerAndReadBack(t *testing.T) {
	t.Parallel()

	placement := testPlacement(0)
	runner := &fakeRunner{responses: []string{"[" + itemWithPlacement(t, "open", 0, &placement, "Placed first") + "]"}}
	item, err := (Client{Runner: runner}).Update(context.Background(), "yoyodyne-u6ei", WorkItemChange{Placement: &placement, AppendNotes: "Placed first"})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	wrote := ""
	for _, arg := range runner.args[0] {
		if strings.HasPrefix(arg, "--set-metadata="+placedKey+"=") {
			wrote = arg
		}
	}
	if !strings.Contains(wrote, `"priority":0`) || !strings.Contains(wrote, `"by":"product-manager"`) || !strings.Contains(wrote, "promised first by the operator") {
		t.Fatalf("the update wrote %q, want the whole placement under %s", wrote, placedKey)
	}
	placed := item.PlacedFirst()
	if placed == nil || !placed.At.Equal(placement.At) || placed.Reason != placement.Reason {
		t.Fatalf("PlacedFirst() = %#v, want the placement read back", placed)
	}

	// Ending a placement writes the key empty, and an item whose key is empty
	// reads as one nobody placed.
	ended := Placement{}
	clearing := &fakeRunner{responses: []string{"[" + itemWithPlacement(t, "open", 0, nil, "") + "]"}}
	back, err := (Client{Runner: clearing}).Update(context.Background(), "yoyodyne-u6ei", WorkItemChange{Placement: &ended})
	if err != nil {
		t.Fatalf("Update() ending error = %v", err)
	}
	if !contains(clearing.args[0], "--set-metadata="+placedKey+"=") {
		t.Fatalf("the ending wrote no placement: %#v", clearing.args[0])
	}
	if back.PlacedFirst() != nil {
		t.Fatalf("PlacedFirst() = %#v after the ending, want none", back.PlacedFirst())
	}

	// A placement bd did not keep is a failure, in either direction.
	unstored := &fakeRunner{responses: []string{"[" + itemWithPlacement(t, "open", 0, nil, "") + "]"}}
	if _, err := (Client{Runner: unstored}).Update(context.Background(), "yoyodyne-u6ei", WorkItemChange{Placement: &placement}); err == nil {
		t.Fatal("Update() with a placement bd did not store = nil error, want a failure")
	}
	stuck := &fakeRunner{responses: []string{"[" + itemWithPlacement(t, "open", 0, &placement, "") + "]"}}
	if _, err := (Client{Runner: stuck}).Update(context.Background(), "yoyodyne-u6ei", WorkItemChange{Placement: &ended}); err == nil ||
		!strings.Contains(err.Error(), "still placed") {
		t.Fatalf("Update() ending a placement bd kept = %v, want it reported", err)
	}
}

// A placement orders its item only while the item is unfinished and still at
// the priority it was placed among, and a value the harness could not have
// written is no placement at all.
func TestAPlacementOrdersOnlyAnUnfinishedItemAtItsOwnPriority(t *testing.T) {
	t.Parallel()

	placement := testPlacement(0)
	if (WorkItem{Status: "open", Priority: 0, Placement: &placement}).PlacedFirst() == nil {
		t.Fatal("an open item at the placed priority is not placed")
	}
	if (WorkItem{Status: "open", Priority: 1, Placement: &placement}).PlacedFirst() != nil {
		t.Fatal("an item moved to another priority is still placed")
	}
	if (WorkItem{Status: "closed", Priority: 0, Placement: &placement}).PlacedFirst() != nil {
		t.Fatal("a closed item is still placed")
	}
	for _, saved := range []string{`"not json"`, `"{\"priority\":0}"`, `7`} {
		if got := placementIn(map[string]json.RawMessage{placedKey: json.RawMessage(saved)}); got != nil {
			t.Fatalf("placementIn(%s) = %#v, want none", saved, got)
		}
	}
	// The line every listing shows says who, when in the operator's zone with the
	// zone named, and why.
	zone := time.FixedZone("PDT", -7*60*60)
	if line := placement.LineIn(zone); line != "placed first among priority 0 by the Lead Product Manager at 06:50 PDT on October 29: promised first by the operator" {
		t.Fatalf("LineIn() = %q", line)
	}
}

// Closing a placed item ends its placement and records on the item what ended
// it; closing an item with no placement makes no second write.
func TestClosingAPlacedItemEndsItsPlacementAndRecordsIt(t *testing.T) {
	t.Parallel()

	placement := testPlacement(0)
	note := "Placement first among priority 0 ended: the item closed. It had been " + placement.Line() + "."
	runner := &fakeRunner{responses: []string{
		"[" + itemWithPlacement(t, "closed", 0, &placement, "") + "]",
		"[" + itemWithPlacement(t, "closed", 0, nil, note) + "]",
	}}
	closed, err := (Client{Runner: runner}).Complete(context.Background(), "yoyodyne-u6ei", "merged")
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(runner.args) < 2 || !contains(runner.args[1], "--set-metadata="+placedKey+"=") || !appendsNote(runner.args[1], note) {
		t.Fatalf("calls = %#v, want the close followed by the ending of the placement with its note", runner.args)
	}
	if closed.Placement != nil {
		t.Fatalf("Complete() placement = %#v, want it ended", closed.Placement)
	}

	plain := &fakeRunner{responses: []string{"[" + itemWithPlacement(t, "closed", 0, nil, "") + "]"}}
	if _, err := (Client{Runner: plain}).Complete(context.Background(), "yoyodyne-u6ei", "merged"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(plain.args) != 1 {
		t.Fatalf("calls = %#v, want the close alone", plain.args)
	}
}

func TestPlacedAmongListsOnePriorityEarliestFirst(t *testing.T) {
	t.Parallel()

	early, late := testPlacement(0), testPlacement(0)
	late.At = early.At.Add(time.Hour)
	other := testPlacement(1)
	items := []WorkItem{
		{ID: "yoyodyne-late", Status: "open", Priority: 0, Placement: &late},
		{ID: "yoyodyne-plain", Status: "open", Priority: 0},
		{ID: "yoyodyne-p1", Status: "open", Priority: 1, Placement: &other},
		{ID: "yoyodyne-early", Status: "blocked", Priority: 0, Placement: &early},
		{ID: "yoyodyne-self", Status: "open", Priority: 0, Placement: &early},
	}
	var got []string
	for _, item := range PlacedAmong(items, 0, "yoyodyne-self") {
		got = append(got, item.ID)
	}
	if strings.Join(got, ",") != "yoyodyne-early,yoyodyne-late" {
		t.Fatalf("PlacedAmong() = %v", got)
	}
}

func appendsNote(args []string, note string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, "--append-notes=") && strings.HasSuffix(arg, note) {
			return true
		}
	}
	return false
}

func contains(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
