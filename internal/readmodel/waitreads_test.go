package readmodel

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// batchTrackerOf is trackerOf that can also read several items in one call, as
// beads.Client can, and counts every call it is asked.
type batchTrackerOf struct {
	items   trackerOf
	down    bool
	batches *[][]string
	singles *[]string
}

func (t batchTrackerOf) Show(ctx context.Context, id string) (beads.WorkItem, error) {
	*t.singles = append(*t.singles, id)
	return t.items.Show(ctx, id)
}

func (t batchTrackerOf) ShowMany(_ context.Context, ids []string) (map[string]beads.WorkItem, error) {
	*t.batches = append(*t.batches, slices.Clone(ids))
	if t.down {
		return nil, errors.New("bd show: timed out waiting for the store")
	}
	read := map[string]beads.WorkItem{}
	for _, id := range ids {
		if item, found := t.items[id]; found {
			read[id] = item
		}
	}
	return read, nil
}

// The pull reads every standing wait each time it runs, and asked the tracker
// for each waited item and each level of each held item's parents with a read
// of its own, which on bd's one-command-at-a-time store kept every listing
// waiting (yoyodyne-ifd.428.126.4.24). Read together, the waits of several held
// items, and their parents, are asked once for each level of the deepest chain,
// and say what they said before.
func TestStandingWaitsAreReadTogetherOneLevelAtATime(t *testing.T) {
	t.Parallel()

	stopped := time.Date(2026, 11, 19, 18, 0, 0, 0, time.UTC)
	items := trackerOf{
		"yoyodyne-ifd.429.146.2": {ID: "yoyodyne-ifd.429.146.2", Status: "blocked", Parent: "yoyodyne-ifd.429.146"},
		"yoyodyne-ifd.429.146":   {ID: "yoyodyne-ifd.429.146", Status: "open", Parent: "yoyodyne-ifd.429"},
		"yoyodyne-ifd.429":       {ID: "yoyodyne-ifd.429", Status: "open"},
		"yoyodyne-ifd.68.25":     {ID: "yoyodyne-ifd.68.25", Status: "blocked", Parent: "yoyodyne-ifd.68"},
		"yoyodyne-ifd.68":        {ID: "yoyodyne-ifd.68", Status: "open"},
		"yoyodyne-ifd.68.32":     {ID: "yoyodyne-ifd.68.32", Status: "closed", ClosedAt: stopped.Add(time.Hour)},
		"yoyodyne-ifd.117.5":     {ID: "yoyodyne-ifd.117.5", Status: "blocked"},
		"yoyodyne-ifd.435.14":    {ID: "yoyodyne-ifd.435.14", Status: "open"},
	}
	held := func() map[string]backlog.Hold {
		return map[string]backlog.Hold{
			// A wait on its own grandparent, which can never end.
			"yoyodyne-ifd.429.146.2": heldFor("run-0123456789abcdef0123456789abcdef", "the run stopped", decidedStanding{waitsOn: "yoyodyne-ifd.429"}, stopped),
			// A wait on work that has closed.
			"yoyodyne-ifd.68.25": heldFor("run-1123456789abcdef0123456789abcdef", "the run stopped", decidedStanding{waitsOn: "yoyodyne-ifd.68.32"}, stopped),
			// A wait on open work elsewhere, which still stands.
			"yoyodyne-ifd.117.5": heldFor("run-2123456789abcdef0123456789abcdef", "the run stopped", decidedStanding{waitsOn: "yoyodyne-ifd.435.14"}, stopped),
		}
	}

	alone := held()
	endWaits(context.Background(), alone, items)

	var batches [][]string
	var singles []string
	together := held()
	endWaits(context.Background(), together, batchTrackerOf{items: items, batches: &batches, singles: &singles})

	if len(singles) != 0 {
		t.Fatalf("read %v one at a time, want everything read in the batches", singles)
	}
	// The held items and what they wait on; then the parents of the held items;
	// then the grandparent's level, which holds nothing new to read.
	if len(batches) != 2 {
		t.Fatalf("asked the tracker %d times (%v), want 2: one for the waits and held items, one for their parents", len(batches), batches)
	}
	if len(batches[0]) != 6 || !slices.Contains(batches[1], "yoyodyne-ifd.429.146") || !slices.Contains(batches[1], "yoyodyne-ifd.68") {
		t.Fatalf("batches = %v, want the six waits and held items first and their parents next", batches)
	}
	for id, hold := range alone {
		if got := together[id]; got.Reason != hold.Reason || got.WaitsOn != hold.WaitsOn {
			t.Fatalf("%s read together = %#v, want what reading it alone says, %#v", id, got, hold)
		}
	}
	if !strings.Contains(together["yoyodyne-ifd.429.146.2"].Reason, "that wait can never end, because yoyodyne-ifd.429.146.2 is under yoyodyne-ifd.429.146, which is under yoyodyne-ifd.429") {
		t.Fatalf("hold = %#v, want the wait on its grandparent read as never ending", together["yoyodyne-ifd.429.146.2"])
	}
	if together["yoyodyne-ifd.68.25"].WaitsOn != "" || together["yoyodyne-ifd.117.5"].WaitsOn != "yoyodyne-ifd.435.14" {
		t.Fatalf("holds = %#v, want the wait on closed work ended and the one on open work standing", together)
	}
}

// A batch the tracker did not answer is not followed by a read of each item
// alone, each of which would wait on the same store again: every wait says it
// could not be read, with the batch's error.
func TestStandingWaitsWhoseBatchWentUnansweredSayTheyCouldNotBeRead(t *testing.T) {
	t.Parallel()

	stopped := time.Date(2026, 11, 19, 18, 0, 0, 0, time.UTC)
	held := map[string]backlog.Hold{
		"yoyodyne-ifd.68.25": heldFor("run-1123456789abcdef0123456789abcdef", "the run stopped", decidedStanding{waitsOn: "yoyodyne-ifd.68.32"}, stopped),
		"yoyodyne-ifd.117.5": heldFor("run-2123456789abcdef0123456789abcdef", "the run stopped", decidedStanding{waitsOn: "yoyodyne-ifd.435.14"}, stopped),
	}
	var batches [][]string
	var singles []string
	endWaits(context.Background(), held, batchTrackerOf{items: trackerOf{}, down: true, batches: &batches, singles: &singles})

	if len(batches) != 1 || len(singles) != 0 {
		t.Fatalf("asked %d batches and %v alone, want one batch and nothing after it", len(batches), singles)
	}
	for id, hold := range held {
		if !strings.Contains(hold.Reason, "could not be read") || !strings.Contains(hold.Reason, "timed out waiting for the store") {
			t.Fatalf("%s = %#v, want it saying the wait could not be read, with the tracker's error", id, hold)
		}
	}
}

// An item a batch that answered left out is one the tracker said nothing of,
// so it is asked about alone, which says why.
func TestAStandingWaitTheBatchLeftOutIsReadAlone(t *testing.T) {
	t.Parallel()

	stopped := time.Date(2026, 11, 19, 18, 0, 0, 0, time.UTC)
	held := map[string]backlog.Hold{
		"yoyodyne-ifd.117.5": heldFor("run-2123456789abcdef0123456789abcdef", "the run stopped", decidedStanding{waitsOn: "yoyodyne-gone"}, stopped),
	}
	var batches [][]string
	var singles []string
	items := trackerOf{"yoyodyne-ifd.117.5": {ID: "yoyodyne-ifd.117.5", Status: "blocked"}}
	endWaits(context.Background(), held, batchTrackerOf{items: items, batches: &batches, singles: &singles})

	if !slices.Equal(singles, []string{"yoyodyne-gone"}) {
		t.Fatalf("read %v alone, want only the item the batch left out", singles)
	}
	if hold := held["yoyodyne-ifd.117.5"]; !strings.Contains(hold.Reason, "could not be read") {
		t.Fatalf("hold = %#v, want it saying the wait could not be read", hold)
	}
}
