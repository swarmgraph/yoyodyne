package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/contextbundle"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// A sweep that met a stopped run with no recorded reason, a run it could not
// docket, and runs it could not settle — a tracker read that timed out, a forge
// that answered HTTP 500, a checkout that reports no branch — names every one
// of them and finishes: each is about one run, and the runs beside them were
// settled and docketed.
func TestAReconcileFinishesPastTheRunsItCouldNotDocketOrSettle(t *testing.T) {
	t.Parallel()

	sweep := reconcileSweep{
		Docketed:   1,
		Unrecorded: []string{"run-11111111111111111111111111111111"},
		NotDocketed: []orchestrator.NotDocketed{{
			RunID:   "run-22222222222222222222222222222222",
			Problem: "docket the stoppage of run run-22222222222222222222222222222222: the docket is unwritable",
		}},
		Runs: []orchestrator.Reconciliation{
			{RunID: "run-24448281", WorkItemID: "yoyodyne-ifd.429.41", Action: orchestrator.ActionUnsettled,
				Failure: "bd show yoyodyne-ifd.429.41 did not answer within its 30s bound on any of 3 attempts"},
			{RunID: "run-33333333", WorkItemID: "yoyodyne-ifd.429.42", Action: orchestrator.ActionUnsettled,
				Failure: "ask the forge about the queued merge: HTTP 500"},
		},
		Convergence: orchestrator.Convergence{Worktrees: []orchestrator.WorktreeSweep{{
			RunID: "run-d1e2fa58", WorkItemID: "yoyodyne-ifd.433.36", Path: "/worktrees/d1e2fa58",
			Failure: "the worktree reports no branch while the record names yoyodyne/yoyodyne-ifd-433-36/d1e2fa58",
		}}},
		ListingCut: "the tracker's listing stopped at bd's default limit after 1213 work item(s), so the 2 item(s) the docket names that it left out were read one at a time",
	}
	var stdout, stderr bytes.Buffer
	if code := reportReconcileResult(&stdout, &stderr, false, sweep, nil); code != 0 {
		t.Fatalf("exit = %d, want the sweep to finish\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	said := stdout.String() + stderr.String()
	for _, want := range []string{
		"run run-11111111111111111111111111111111 is on the development manager's list with no recorded reason for its stop",
		"run run-22222222222222222222222222222222 not put on the development manager's list: ",
		"did not answer within its 30s bound",
		"HTTP 500",
		"reports no branch",
		"stopped at bd's default limit after 1213 work item(s)",
	} {
		if !strings.Contains(said, want) {
			t.Fatalf("the sweep did not say %q:\n%s", want, said)
		}
	}

	stdout.Reset()
	if code := reportReconcileResult(&stdout, &stderr, true, sweep, nil); code != 0 {
		t.Fatalf("json exit = %d", code)
	}
	for _, want := range []string{`"unrecorded":["run-11111111111111111111111111111111"]`, `"not_docketed":[{"run_id":"run-22222222222222222222222222222222"`, `"listing_cut":`} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("json report lacks %s:\n%s", want, stdout.String())
		}
	}
}

// cutTracker is a tracker holding more work items than bd's old default limit,
// whose listing bd will not lift that limit for: it returns the first rows and
// refuses the rest as possibly cut, and reads any one item it holds.
type cutTracker struct {
	rows  []beads.WorkItem
	held  map[string]beads.WorkItem
	shown []string
	// broken is an item whose own reading fails.
	broken string
}

func (c *cutTracker) List(_ context.Context, status string) ([]beads.WorkItem, error) {
	var rows []beads.WorkItem
	for _, item := range c.rows {
		if status == "" || item.Status == status {
			rows = append(rows, item)
		}
	}
	return nil, &beads.ListingCut{Verb: "list", Items: rows}
}

func (c *cutTracker) Show(_ context.Context, id string) (beads.WorkItem, error) {
	c.shown = append(c.shown, id)
	if id == c.broken {
		return beads.WorkItem{}, errors.New("bd show did not answer within its 30s bound")
	}
	item, held := c.held[id]
	if !held {
		return beads.WorkItem{}, beads.ErrNoSuchWorkItem
	}
	return item, nil
}

// trackerPastTheOldLimit holds 1,300 items, more than the 1,213 a cut listing
// returned, and the closed item the docket names is past the cut.
func trackerPastTheOldLimit(closedPastTheCut, openPastTheCut string) *cutTracker {
	tracker := &cutTracker{held: map[string]beads.WorkItem{}}
	for index := range 1300 {
		status := "open"
		if index%2 == 0 {
			status = "closed"
		}
		item := beads.WorkItem{ID: fmt.Sprintf("yoyodyne-%d", index), Status: status}
		tracker.held[item.ID] = item
		if index < 1213 {
			tracker.rows = append(tracker.rows, item)
		}
	}
	tracker.held[closedPastTheCut] = beads.WorkItem{ID: closedPastTheCut, Status: "closed"}
	tracker.held[openPastTheCut] = beads.WorkItem{ID: openPastTheCut, Status: "open"}
	return tracker
}

// A docket entry whose closed item lies past where the tracker's listing was
// cut is closed by the reconcile sweep, which reads that item on its own and
// says the listing was cut; and the development manager's list does not show it
// as open. An entry whose item is open past the cut stays listed.
func TestAClosedItemPastACutListingIsNotShownAsOpen(t *testing.T) {
	t.Parallel()

	const closedItem, openItem = "yoyodyne-ifd.192", "yoyodyne-ifd.500"
	recorded := time.Now().Add(-time.Hour).UTC()
	onClosed := deliveredEntry()
	onClosed.WorkItemID = closedItem
	onClosed.RecordedAt = recorded
	onOpen := deliveredEntry()
	onOpen.RunID = "run-fedcba9876543210fedcba9876543210"
	onOpen.Key = triage.Key(triage.ClassStoppedRun, onOpen.RunID)
	onOpen.WorkItemID = openItem
	onOpen.RecordedAt = recorded

	// The development manager's list, read from the same cut listing.
	tracker := trackerPastTheOldLimit(closedItem, openItem)
	docket := []triage.Entry{onClosed, onOpen}
	read, err := readDocketItems(context.Background(), tracker, "", docketItemIDs(docket))
	if err != nil {
		t.Fatalf("readDocketItems() error = %v", err)
	}
	if !strings.Contains(read.cut, "after 1213 work item(s)") || len(read.unread) != 0 {
		t.Fatalf("read = cut %q, unread %v; want the cut said and both items read", read.cut, read.unread)
	}
	window := contextbundle.TriageDocketWindow(contextbundle.ProductRequest{
		TriageDocket: docket, TriageDocketAt: time.Now(),
		TriageDocketItems: read.items, TriageDocketItemsUnavailable: docketItemsUnknown(read),
	}, nil, nil)
	if len(window.Listed) != 1 || window.Listed[0].Entry.WorkItemID != openItem {
		t.Fatalf("listed = %+v, want only the open item's entry", window.Listed)
	}
	if !strings.Contains(window.Text, "1 docket entry(s) are not listed because the work item they stopped is closed") {
		t.Fatalf("window does not say the closed item's entry was left out:\n%s", window.Text)
	}

	// The reconcile sweep, over the closed listing cut the same way.
	store, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range docket {
		if _, err := store.RecordOnce(entry); err != nil {
			t.Fatal(err)
		}
	}
	sweepTracker := trackerPastTheOldLimit(closedItem, openItem)
	closed, cut, err := sweepClosedItems(context.Background(), orchestrator.Docketer{Docket: store}, sweepTracker)
	if err != nil {
		t.Fatalf("sweepClosedItems() error = %v, want it to finish past the cut", err)
	}
	if closed != 1 || !strings.Contains(cut, "stopped at bd's default limit") {
		t.Fatalf("sweep = %d closed, %q; want the closed item's entry closed and the cut said", closed, cut)
	}
	entries, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if (entry.WorkItemID == closedItem) != (entry.Closed != nil) {
			t.Fatalf("entry on %s closed = %#v, want only the closed item's entry closed", entry.WorkItemID, entry.Closed)
		}
	}
}

// An item past the cut that cannot be read on its own either is not taken for
// closed or for open: its entry stays listed, and the window says why.
func TestAnItemPastACutThatCannotBeReadKeepsItsEntryListedAndSaysWhy(t *testing.T) {
	t.Parallel()

	tracker := trackerPastTheOldLimit("yoyodyne-ifd.192", "yoyodyne-ifd.500")
	tracker.broken = "yoyodyne-ifd.192"
	entry := deliveredEntry()
	entry.WorkItemID = "yoyodyne-ifd.192"
	entry.RecordedAt = time.Now().Add(-time.Hour).UTC()
	read, err := readDocketItems(context.Background(), tracker, "", docketItemIDs([]triage.Entry{entry}))
	if err != nil {
		t.Fatalf("readDocketItems() error = %v", err)
	}
	if len(read.unread) != 1 || !strings.Contains(read.cut, "could not be read (yoyodyne-ifd.192)") {
		t.Fatalf("read = cut %q, unread %v; want the unread item named", read.cut, read.unread)
	}
	window := contextbundle.TriageDocketWindow(contextbundle.ProductRequest{
		TriageDocket: []triage.Entry{entry}, TriageDocketAt: time.Now(),
		TriageDocketItems: read.items, TriageDocketItemsUnavailable: docketItemsUnknown(read),
	}, nil, nil)
	if len(window.Listed) != 1 || !strings.Contains(window.Text, "could not be read (yoyodyne-ifd.192)") {
		t.Fatalf("window = %+v\n%s\nwant the entry listed and the unread item named", window.Listed, window.Text)
	}
}
