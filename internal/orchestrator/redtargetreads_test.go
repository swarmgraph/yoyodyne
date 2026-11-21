package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// statusTracker answers Show item by item and Statuses in one call, counting
// both, so a test can tell which reading a pass made.
type statusTracker struct {
	statuses map[string]string
	// unlisted is the items Statuses leaves out of its answer, as bd leaves out
	// an item it holds nothing under; Show still answers or refuses them.
	unlisted  map[string]bool
	shows     []string
	listings  [][]string
	listErr   error
	refuseIDs map[string]error
}

func (t *statusTracker) Show(_ context.Context, id string) (beads.WorkItem, error) {
	t.shows = append(t.shows, id)
	if err := t.refuseIDs[id]; err != nil {
		return beads.WorkItem{}, err
	}
	return beads.WorkItem{ID: id, Status: t.statuses[id]}, nil
}

func (t *statusTracker) Statuses(_ context.Context, ids []string) (map[string]string, error) {
	t.listings = append(t.listings, append([]string(nil), ids...))
	if t.listErr != nil {
		return nil, t.listErr
	}
	read := map[string]string{}
	for _, id := range ids {
		if !t.unlisted[id] {
			read[id] = t.statuses[id]
		}
	}
	return read, nil
}

// showOnly is a reader with no batched reading, as a test fake or another
// tracker may be: it is asked item by item, as before.
type showOnly struct{ inner *statusTracker }

func (s showOnly) Show(ctx context.Context, id string) (beads.WorkItem, error) {
	return s.inner.Show(ctx, id)
}

// The carry-out, the re-arming and reconcile each ask, every pass, whether the
// items a merge withdrawn for a red target waits on are closed. Read one by one
// that was a call per item per pass, a sixth of the harness's tracker calls in
// the hour from 03:00 on November 21; the tracker client now answers all of them
// in one call, and an item that call does not answer for is still read alone.
func TestTheItemsARedTargetMergeWaitsOnAreReadInOneCall(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	waiting := runstate.TargetRed{TargetBranch: "main", Checks: []runstate.TargetRedCheck{{Name: "test", WorkItem: "yoyodyne-ifd.436.4"}, {Name: "race", WorkItem: "yoyodyne-ifd.141.5"}}}

	tracker := &statusTracker{statuses: map[string]string{"yoyodyne-ifd.436.4": "in_progress", "yoyodyne-ifd.141.5": "closed"}}
	statuses, err := itemStatuses(ctx, tracker, waiting.WaitingOn())
	if err != nil || len(statuses) != 2 || statuses["yoyodyne-ifd.436.4"] != "in_progress" || statuses["yoyodyne-ifd.141.5"] != "closed" {
		t.Fatalf("itemStatuses = %v, %v, want both items' statuses", statuses, err)
	}
	if len(tracker.listings) != 1 || len(tracker.shows) != 0 {
		t.Fatalf("read with %d listings and shows %v, want one listing and no show", len(tracker.listings), tracker.shows)
	}

	answered, err := CarryOut{Items: tracker}.targetRedAnswered(ctx, waiting)
	if err != nil || answered {
		t.Fatalf("carry-out targetRedAnswered = %v, %v, want not answered while 436.4 is open", answered, err)
	}
	err = Rearmer{Items: tracker}.targetRedAnswered(ctx, waiting)
	if err == nil || !strings.Contains(err.Error(), "yoyodyne-ifd.436.4") || !strings.Contains(err.Error(), "in_progress") {
		t.Fatalf("rearmer targetRedAnswered = %v, want a refusal naming 436.4 and its status", err)
	}
	if len(tracker.listings) != 3 || len(tracker.shows) != 0 {
		t.Fatalf("three passes read with %d listings and shows %v, want one listing each and no show", len(tracker.listings), tracker.shows)
	}

	// An item the listing does not answer for is read alone, so one the tracker
	// holds nothing under fails as it always did, naming it.
	gone := &statusTracker{
		statuses:  map[string]string{"yoyodyne-ifd.436.4": "closed"},
		unlisted:  map[string]bool{"yoyodyne-ifd.141.5": true},
		refuseIDs: map[string]error{"yoyodyne-ifd.141.5": errors.New("no issue found")},
	}
	if _, err := (CarryOut{Items: gone}).targetRedAnswered(ctx, waiting); err == nil || !strings.Contains(err.Error(), "yoyodyne-ifd.141.5") {
		t.Fatalf("targetRedAnswered with a missing item = %v, want the failure naming it", err)
	}
	if len(gone.shows) != 1 || gone.shows[0] != "yoyodyne-ifd.141.5" {
		t.Fatalf("shows = %v, want only the item the listing left out", gone.shows)
	}

	// A failed listing names the first item, as the item-by-item reading did.
	failing := &statusTracker{listErr: errors.New("the tracker did not answer")}
	if _, err := (CarryOut{Items: failing}).targetRedAnswered(ctx, waiting); err == nil || !strings.Contains(err.Error(), "yoyodyne-ifd.436.4") || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("targetRedAnswered with the listing refused = %v, want it named", err)
	}

	// A reader with no batched reading is asked item by item.
	plain := &statusTracker{statuses: map[string]string{"yoyodyne-ifd.436.4": "closed", "yoyodyne-ifd.141.5": "closed"}}
	if answered, err := (CarryOut{Items: showOnly{plain}}).targetRedAnswered(ctx, waiting); err != nil || !answered {
		t.Fatalf("targetRedAnswered through Show = %v, %v, want answered", answered, err)
	}
	if len(plain.shows) != 2 || len(plain.listings) != 0 {
		t.Fatalf("plain reader got shows %v and %d listings, want two shows", plain.shows, len(plain.listings))
	}
}
