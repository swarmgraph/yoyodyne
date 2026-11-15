package beads

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func itemsJSON(ids ...string) string {
	rows := make([]string, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, fmt.Sprintf(`{"id":%q,"title":"Work %s","status":"closed","priority":1,"issue_type":"task"}`, id, id))
	}
	return "[" + strings.Join(rows, ",") + "]"
}

// Several items are read in one bd command, and an item bd does not hold is
// absent from the answer rather than a failure.
func TestShowManyReadsSeveralItemsInOneCall(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{responses: []string{itemsJSON("yoyodyne-1", "yoyodyne-3")}}
	read, err := (Client{Runner: runner}).ShowMany(context.Background(), []string{"yoyodyne-1", "yoyodyne-2", "yoyodyne-3", "yoyodyne-1"})
	if err != nil {
		t.Fatalf("ShowMany() error = %v", err)
	}
	if len(runner.args) != 1 {
		t.Fatalf("ShowMany() ran %d bd commands, want 1: %v", len(runner.args), runner.args)
	}
	want := []string{"show", "yoyodyne-1", "yoyodyne-2", "yoyodyne-3", "--json"}
	if !slices.Equal(runner.args[0], want) {
		t.Fatalf("ShowMany() ran %v, want %v (each id once)", runner.args[0], want)
	}
	if len(read) != 2 || read["yoyodyne-1"].Title != "Work yoyodyne-1" || read["yoyodyne-3"].Status != "closed" {
		t.Fatalf("ShowMany() = %+v, want yoyodyne-1 and yoyodyne-3", read)
	}
	if _, ok := read["yoyodyne-2"]; ok {
		t.Fatal("ShowMany() answered an item bd did not hold")
	}
}

// bd exits non-zero when it holds none of the items named; that is an empty
// answer, not the tracker failing. A refusal that is the tracker failing, and
// a timeout, are still failures.
func TestShowManyTellsNoItemsFromATrackerThatFailed(t *testing.T) {
	t.Parallel()

	none := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessFailed, ExitCode: 1,
		Stderr: `Error fetching yoyodyne-8: no issue found matching "yoyodyne-8"` + "\n",
		Stdout: `{"error":"no issues found matching the provided IDs","schema_version":1}`}}}
	read, err := (Client{Runner: none}).ShowMany(context.Background(), []string{"yoyodyne-8"})
	if err != nil || len(read) != 0 {
		t.Fatalf("ShowMany() of items bd does not hold = %v, %v; want an empty answer", read, err)
	}

	down := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "failed to open database: LOCK: operation not permitted\n"}}}
	if _, err := (Client{Runner: down}).ShowMany(context.Background(), []string{"yoyodyne-8"}); err == nil {
		t.Fatal("ShowMany() over a tracker that could not be opened succeeded")
	}
	stopped := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessTimedOut, ExitCode: -1}}}
	if _, err := (Client{Runner: stopped, Holders: fixedHolders{}}).ShowMany(context.Background(), []string{"yoyodyne-8"}); err == nil || !timedOut(err) {
		t.Fatalf("ShowMany() stopped at its limit = %v, want a timeout, asked once", err)
	}
	if len(stopped.args) != 1 {
		t.Fatalf("ShowMany() stopped at its limit asked %d times, want once", len(stopped.args))
	}
}

// More items than one command names are read in as many commands as it takes.
func TestShowManyBatchesALongList(t *testing.T) {
	t.Parallel()

	ids := make([]string, maxItemsPerShow+3)
	for index := range ids {
		ids[index] = fmt.Sprintf("yoyodyne-%d", index)
	}
	runner := &fakeRunner{responses: []string{itemsJSON(ids[:maxItemsPerShow]...), itemsJSON(ids[maxItemsPerShow:]...)}}
	read, err := (Client{Runner: runner}).ShowMany(context.Background(), ids)
	if err != nil {
		t.Fatalf("ShowMany() error = %v", err)
	}
	if len(runner.args) != 2 || len(read) != len(ids) {
		t.Fatalf("ShowMany() ran %d commands and read %d items, want 2 and %d", len(runner.args), len(read), len(ids))
	}
}

// An item read ahead is given to the first read of it without asking bd, and
// once: a second read asks bd, as does a read of an item that was not read
// ahead. Ending the hold gives nothing out afterwards.
func TestPrefetchedItemAnswersItsFirstReadOnly(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{responses: []string{
		itemsJSON("yoyodyne-1", "yoyodyne-2"),
		itemsJSON("yoyodyne-1"),
		itemsJSON("yoyodyne-5"),
		itemsJSON("yoyodyne-2"),
	}}
	client := Client{Runner: runner, Reads: &ItemReads{}}
	release := client.PrefetchItems(context.Background(), []string{"yoyodyne-1", "yoyodyne-2"})
	if item, err := client.Show(context.Background(), "yoyodyne-1"); err != nil || item.ID != "yoyodyne-1" {
		t.Fatalf("Show() of a prefetched item = %+v, %v", item, err)
	}
	if len(runner.args) != 1 {
		t.Fatalf("the first read of a prefetched item asked bd: %v", runner.args)
	}
	if _, err := client.Show(context.Background(), "yoyodyne-1"); err != nil || len(runner.args) != 2 {
		t.Fatalf("the second read of an item did not ask bd (%v): %v", err, runner.args)
	}
	if _, err := client.Show(context.Background(), "yoyodyne-5"); err != nil || len(runner.args) != 3 {
		t.Fatalf("a read of an item not prefetched did not ask bd (%v): %v", err, runner.args)
	}
	release()
	if client.Reads.Held() != 0 {
		t.Fatalf("ending the hold left %d items held", client.Reads.Held())
	}
	if _, err := client.Show(context.Background(), "yoyodyne-2"); err != nil || len(runner.args) != 4 {
		t.Fatalf("a read after the hold ended did not ask bd (%v): %v", err, runner.args)
	}
}

// A command that names an item drops what is held for it, so the read after a
// write sees the write; and a write's own read of the item never takes it.
func TestAWriteDropsTheItemsItNames(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{responses: []string{
		itemsJSON("yoyodyne-1", "yoyodyne-2"),
		`{"issue_id":"yoyodyne-1","depends_on_id":"yoyodyne-9","status":"added"}`,
		workItemJSON("in_progress", ""),
	}}
	client := Client{Runner: runner, Reads: &ItemReads{}}
	defer client.PrefetchItems(context.Background(), []string{"yoyodyne-1", "yoyodyne-2"})()
	if err := client.AddBlocker(context.Background(), "yoyodyne-1", "yoyodyne-9"); err != nil {
		t.Fatalf("AddBlocker() error = %v", err)
	}
	if client.Reads.Held() != 1 {
		t.Fatalf("after a write naming yoyodyne-1, %d items are held, want only yoyodyne-2", client.Reads.Held())
	}
	if item, err := client.Show(context.Background(), "yoyodyne-1"); err != nil || item.Status != "in_progress" || len(runner.args) != 3 {
		t.Fatalf("the read after a write = %+v, %v after %v; want it read from bd", item, err, runner.args)
	}
}

// Nothing read ahead is given out once it is older than the bound.
func TestAnItemHeldTooLongIsReadAgain(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 15, 1, 0, 0, 0, time.UTC)
	reads := &ItemReads{now: func() time.Time { return now }}
	runner := &fakeRunner{responses: []string{itemsJSON("yoyodyne-1"), workItemJSON("open", "")}}
	client := Client{Runner: runner, Reads: reads}
	defer client.PrefetchItems(context.Background(), []string{"yoyodyne-1"})()
	now = now.Add(itemReadsMaxAge + time.Second)
	if item, err := client.Show(context.Background(), "yoyodyne-1"); err != nil || item.Status != "open" || len(runner.args) != 2 {
		t.Fatalf("a read past the bound = %+v, %v after %v; want it read from bd", item, err, runner.args)
	}
}

// A client with nowhere to hold items reads nothing ahead, and a batch that
// fails holds nothing, so each item is read as it always was.
func TestPrefetchWithoutAHoldOrWithAFailureChangesNothing(t *testing.T) {
	t.Parallel()

	idle := &fakeRunner{}
	(Client{Runner: idle}).PrefetchItems(context.Background(), []string{"yoyodyne-1"})()
	if len(idle.args) != 0 {
		t.Fatalf("a client with no hold asked bd %v", idle.args)
	}

	runner := &fakeRunner{
		results:   []execution.ProcessResult{{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "failed to open database\n"}},
		responses: []string{"", workItemJSON("open", "")},
	}
	client := Client{Runner: runner, Reads: &ItemReads{}}
	defer client.PrefetchItems(context.Background(), []string{"yoyodyne-1"})()
	if client.Reads.Held() != 0 {
		t.Fatal("a failed batch held items")
	}
	if _, err := client.Show(context.Background(), "yoyodyne-1"); err != nil || len(runner.args) != 2 {
		t.Fatalf("a read after a failed batch = %v after %v; want it read from bd", err, runner.args)
	}
}

// A batched read is named in the call log and tally as one read of several
// items, so a slow one says what it was.
func TestABatchedReadIsDescribedAsOne(t *testing.T) {
	t.Parallel()

	if got := describeCall([]string{"show", "yoyodyne-1", "yoyodyne-2", "yoyodyne-3", "--json"}); got != "reading 3 work items in one call, yoyodyne-1 first" {
		t.Fatalf("describeCall() of a batched read = %q", got)
	}
	if got := describeCall([]string{"show", "yoyodyne-1", "--json"}); got != "reading work item yoyodyne-1" {
		t.Fatalf("describeCall() of a read of one item = %q", got)
	}
}

// bd answers several ids in one show, each exactly as a show of it alone, and
// leaves out an id it holds nothing under while still answering the rest.
func TestShowManyConformance(t *testing.T) {
	t.Parallel()
	project := newTracker(t)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()
	first, err := client.Create(ctx, NewWorkItem{Title: "First", Description: "d", Type: "task"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	second, err := client.Create(ctx, NewWorkItem{Title: "Second", Description: "d", Type: "task"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := client.RecordOutcome(ctx, first.ID, "A note on the first."); err != nil {
		t.Fatalf("RecordOutcome() error = %v", err)
	}
	missing := filepath.Base(project) + "-zzzz9"
	read, err := client.ShowMany(ctx, []string{first.ID, missing, second.ID})
	if err != nil {
		t.Fatalf("ShowMany() error = %v", err)
	}
	if len(read) != 2 {
		t.Fatalf("ShowMany() read %d items, want the 2 bd holds: %+v", len(read), read)
	}
	alone, err := client.Show(ctx, first.ID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if fmt.Sprintf("%+v", read[first.ID]) != fmt.Sprintf("%+v", alone) {
		t.Fatalf("an item read with others = %+v, read alone = %+v", read[first.ID], alone)
	}
	none, err := client.ShowMany(ctx, []string{missing})
	if err != nil || len(none) != 0 {
		t.Fatalf("ShowMany() of an item bd does not hold = %v, %v; want an empty answer", none, err)
	}
}
