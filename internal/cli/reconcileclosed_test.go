package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// closedListing is the tracker's closed listing, and whether it was asked for.
type closedListing struct {
	items  []beads.WorkItem
	err    error
	asked  bool
	status string
}

func (l *closedListing) List(_ context.Context, status string) ([]beads.WorkItem, error) {
	l.asked = true
	l.status = status
	return l.items, l.err
}

// The reconcile sweep closes the entries standing for the items the tracker
// holds as closed, leaves an open item's entry and an unfinished publication
// standing, and reports how many it closed.
func TestTheReconcileSweepClosesTheEntriesOfClosedItems(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewDocketStore() error = %v", err)
	}
	recorded := time.Now().Add(-time.Hour).UTC()
	closedItem := deliveredEntry()
	closedItem.RecordedAt = recorded
	openItem := deliveredEntry()
	openItem.RunID = "run-fedcba9876543210fedcba9876543210"
	openItem.Key = triage.Key(triage.ClassStoppedRun, openItem.RunID)
	openItem.WorkItemID = "yoyodyne-ifd.500"
	openItem.RecordedAt = recorded
	publication := triage.Entry{
		SchemaVersion: triage.SchemaVersion,
		Key:           triage.PublicationKey(closedItem.RunID, 42),
		Class:         triage.ClassPublication,
		ProductID:     "yoyodyne",
		RunID:         closedItem.RunID,
		WorkItemID:    closedItem.WorkItemID,
		RecordedAt:    recorded,
		Publication:   &triage.Publication{Number: 42, State: "OPEN", ApprovedAt: recorded.Add(-3 * time.Hour)},
	}
	for _, entry := range []triage.Entry{closedItem, openItem, publication} {
		if _, err := store.RecordOnce(entry); err != nil {
			t.Fatalf("RecordOnce(%s) error = %v", entry.Key, err)
		}
	}
	tracker := &closedListing{items: []beads.WorkItem{{ID: closedItem.WorkItemID, Status: "closed"}}}

	closed, cut, err := sweepClosedItems(context.Background(), orchestrator.Docketer{Docket: store}, tracker)
	if err != nil || cut != "" {
		t.Fatalf("sweepClosedItems() = %q, %v; want a whole listing read", cut, err)
	}
	if closed != 1 {
		t.Fatalf("closed = %d, want the closed item's stopped run", closed)
	}
	if tracker.status != "closed" {
		t.Fatalf("listed status %q, want the closed items", tracker.status)
	}
	entries, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	for _, entry := range entries {
		switch entry.Key {
		case closedItem.Key:
			if entry.Closed == nil || entry.Closed.Decision != "item-closed" ||
				!strings.Contains(entry.Closed.Reason, "the tracker holds yoyodyne-ifd.209.16 as closed, as a reconcile sweep read it") {
				t.Fatalf("closed item's entry = %#v, want it closed with the sweep's reason", entry.Closed)
			}
		case openItem.Key, publication.Key:
			if entry.Closed != nil {
				t.Fatalf("entry %s = %#v, want it left standing", entry.Key, entry.Closed)
			}
		}
	}

	var stdout, stderr bytes.Buffer
	reportReconcileResult(&stdout, &stderr, false, reconcileSweep{ClosedWithItem: closed}, nil)
	if !strings.Contains(stdout.String(), "1 triage docket entry(s) closed because the tracker holds their item as closed") {
		t.Fatalf("text report = %q", stdout.String())
	}
	stdout.Reset()
	reportReconcileResult(&stdout, &stderr, true, reconcileSweep{ClosedWithItem: closed}, nil)
	if !strings.Contains(stdout.String(), `"closed_with_item":1`) {
		t.Fatalf("json report = %q", stdout.String())
	}
}

// A docket with nothing standing asks the tracker nothing, so a product with no
// entries sweeps cleanly whether or not its tracker can be listed.
func TestTheSweepOverAnEmptyDocketAsksTheTrackerNothing(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewDocketStore() error = %v", err)
	}
	tracker := &closedListing{err: errors.New("no beads database found")}
	closed, _, err := sweepClosedItems(context.Background(), orchestrator.Docketer{Docket: store}, tracker)
	if err != nil || closed != 0 || tracker.asked {
		t.Fatalf("sweep = %d, %v, asked = %t, want nothing done and nothing asked", closed, err, tracker.asked)
	}
}
