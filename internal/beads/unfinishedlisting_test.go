package beads

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The two listings a reader that keeps closed work asks for are what bd is
// told: the unfinished one is `bd list` narrowed to no status and given no
// --all, and the closed one names the status and the time in UTC.
func TestUnfinishedAndClosedSinceListingsAskBdForThemselves(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{responses: []string{"[]", "[]"}}
	client := Client{Runner: runner}
	if _, err := client.ListUnfinished(context.Background()); err != nil {
		t.Fatal(err)
	}
	since := time.Date(2026, 10, 24, 12, 13, 0, 0, time.FixedZone("PDT", -7*60*60))
	if _, err := client.ListClosedSince(context.Background(), since); err != nil {
		t.Fatal(err)
	}
	if len(runner.args) != 2 {
		t.Fatalf("calls = %#v, want two", runner.args)
	}
	if want := []string{"list", "--json", "--limit=0"}; !slices.Equal(runner.args[0], want) {
		t.Fatalf("unfinished listing = %#v, want %#v", runner.args[0], want)
	}
	if want := []string{"list", "--json", "--limit=0", "--status=closed", "--updated-after=2026-10-24T19:13:00Z"}; !slices.Equal(runner.args[1], want) {
		t.Fatalf("closed listing = %#v, want %#v", runner.args[1], want)
	}
	if _, err := client.ListClosedSince(context.Background(), time.Time{}); err == nil {
		t.Fatal("a closed listing with no time was asked for")
	}
}

// TestUnfinishedAndChangedClosedListingConformance pins what the shared
// tracker reading rests on (internal/readmodel, sharedlisting.go): that `bd
// list` given no status is every item that is not closed, whatever else its
// status is; that together with the closed items it is exactly what `bd list
// --all` answers; and that the closed listing narrowed by --updated-after holds
// an item closed after the time and leaves out one closed before it. A bd that
// left deferred or claimed work out of the bare listing would drop it from
// every status reading; one that read the time in another zone would hand back
// the wrong closed items, or none.
func TestUnfinishedAndChangedClosedListingConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	create := func(title string) WorkItem {
		t.Helper()
		item, err := client.Create(ctx, NewWorkItem{Title: title, Description: "d", Type: "task"})
		if err != nil {
			t.Fatalf("Create(%q) error = %v", title, err)
		}
		return item
	}
	open := create("Still queued")
	blocked := create("Stopped for somebody")
	if _, err := client.Block(ctx, blocked.ID, "waiting on a person"); err != nil {
		t.Fatalf("Block() error = %v", err)
	}
	claimed := create("Being worked")
	if _, err := client.claim(ctx, claimed.ID); err != nil {
		t.Fatalf("claim() error = %v", err)
	}
	deferred := create("Put off")
	runCommand(t, project, "bd", "update", deferred.ID, "--status=deferred")
	earlier := create("Landed before the mark")
	if _, err := client.Complete(ctx, earlier.ID, "landed"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	// bd keeps a change's time to the second, so the mark is a clear second
	// either side of the two closes.
	time.Sleep(1500 * time.Millisecond)
	mark := time.Now()
	time.Sleep(1500 * time.Millisecond)
	later := create("Landed after the mark")
	if _, err := client.Complete(ctx, later.ID, "landed"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	unfinished, err := client.ListUnfinished(ctx)
	if err != nil {
		t.Fatalf("ListUnfinished() error = %v", err)
	}
	for id, want := range map[string]string{open.ID: statusOpen, blocked.ID: statusBlocked, claimed.ID: statusInProgress, deferred.ID: "deferred"} {
		item, found := itemIn(unfinished, id)
		if !found || item.Status != want {
			t.Fatalf("ListUnfinished() gives %s as %#v (found = %v), want it listed %s", id, item, found, want)
		}
	}
	for _, id := range []string{earlier.ID, later.ID} {
		if item, found := itemIn(unfinished, id); found {
			t.Fatalf("ListUnfinished() carries the closed item %#v", item)
		}
	}

	closed, err := client.List(ctx, statusClosed)
	if err != nil {
		t.Fatalf("List(closed) error = %v", err)
	}
	every, err := client.List(ctx, "")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	var together, all []string
	for _, item := range append(unfinished, closed...) {
		together = append(together, item.ID)
	}
	for _, item := range every {
		all = append(all, item.ID)
	}
	slices.Sort(together)
	slices.Sort(all)
	if !slices.Equal(together, all) {
		t.Fatalf("the unfinished and closed listings hold %v, every item is %v; want the same items", together, all)
	}

	changed, err := client.ListClosedSince(ctx, mark)
	if err != nil {
		t.Fatalf("ListClosedSince() error = %v", err)
	}
	if item, found := itemIn(changed, later.ID); !found || item.Status != statusClosed {
		t.Fatalf("ListClosedSince(mark) gives %s as %#v (found = %v), want it listed closed", later.ID, item, found)
	}
	if item, found := itemIn(changed, earlier.ID); found {
		t.Fatalf("ListClosedSince(mark) carries %#v, closed before the mark", item)
	}
	for _, item := range changed {
		if item.Status != statusClosed {
			t.Fatalf("ListClosedSince(mark) carries %#v, which is not closed", item)
		}
	}
}
