package beads

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// cleanupProject is a project whose store the client finds, with a fake bd
// answering the calls a cleanup makes in order.
func cleanupProject(t *testing.T, runner *fakeRunner) Client {
	t.Helper()
	project := t.TempDir()
	storeAt(t, filepath.Join(project, ".beads"), "metadata.json", "embeddeddolt/")
	writeFile(t, filepath.Join(project, ".beads", "embeddeddolt", "store"), strings.Repeat("x", 2048))
	return Client{Runner: runner, Dir: project, Holders: fixedHolders{}}
}

const threeItems = `[{"id":"calc-1","title":"a","status":"open","priority":2,"issue_type":"task"},{"id":"calc-2","title":"b","status":"closed","priority":2,"issue_type":"task"},{"id":"calc-3","title":"c","status":"open","priority":2,"issue_type":"task"}]`

// A cleanup lists nothing before bd's cleanup starts, and counts every work
// item, closed ones included, once it has ended.
func TestACleanupCountsTheStoresItemsOnlyAfterIt(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{responses: []string{"", threeItems}}
	client := cleanupProject(t, runner)

	cleaned, err := client.CleanUpStore(context.Background(), time.Minute)
	if err != nil {
		t.Fatalf("CleanUpStore() error = %v", err)
	}
	if !cleaned.Ran() || !cleaned.Counted || cleaned.ItemsAfter != 3 || cleaned.NotCounted != "" {
		t.Errorf("CleanUpStore() = %+v, want a cleanup that ran with 3 items counted after it", cleaned)
	}
	want := [][]string{
		{"gc", "--skip-decay", "--force"},
		{"list", "--json", "--limit=0", "--all"},
	}
	if !reflect.DeepEqual(runner.args, want) {
		t.Errorf("bd args = %v, want the cleanup and then the count", runner.args)
	}
}

// On a store too large to list in time, the cleanup still starts and
// finishes: nothing waits on a listing before it, and the count after it that
// times out is said as unknown rather than failing the cleanup.
func TestACleanupStartsAndFinishesWhereListingTheItemsTimesOut(t *testing.T) {
	t.Parallel()
	timedOut := execution.ProcessResult{Status: execution.ProcessTimedOut, ExitCode: -1}
	runner := &fakeRunner{responses: []string{""}, failures: map[int]execution.ProcessResult{1: timedOut}}
	client := cleanupProject(t, runner)

	cleaned, err := client.CleanUpStore(context.Background(), time.Minute)
	if err != nil {
		t.Fatalf("CleanUpStore() error = %v, want a cleanup that finished", err)
	}
	if !cleaned.Ran() || cleaned.Stopped || cleaned.Counted || cleaned.NotCounted == "" || cleaned.SizeBefore == 0 {
		t.Errorf("CleanUpStore() = %+v, want a finished cleanup whose count after is unknown, with why", cleaned)
	}
	if len(runner.args) != 2 || runner.args[0][0] != "gc" {
		t.Errorf("bd args = %v, want the cleanup first and one try at the count after it", runner.args)
	}
}

// A cleanup bd fails is returned with what was measured of it, so it can be
// recorded as one that ran, and nothing is listed either side of it.
func TestAFailedCleanupIsReturnedAsOneThatRan(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{failures: map[int]execution.ProcessResult{0: {Status: execution.ProcessFailed, ExitCode: 1, Stderr: "dolt gc: disk full"}}}
	client := cleanupProject(t, runner)

	cleaned, err := client.CleanUpStore(context.Background(), time.Minute)
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("CleanUpStore() error = %v, want bd's failure", err)
	}
	if !cleaned.Ran() || cleaned.Counted || cleaned.SizeAfter != cleaned.SizeBefore || cleaned.SizeBefore == 0 {
		t.Errorf("CleanUpStore() = %+v, want a cleanup that ran, measured either side", cleaned)
	}
	if len(runner.args) != 1 {
		t.Errorf("bd args = %v, want no count around a failed cleanup", runner.args)
	}
}

// Looking again for items a cleanup found missing reads each on its own,
// never a listing of every item, and stops at the first still missing.
func TestMissingItemsAreLookedForAgainOneByOne(t *testing.T) {
	t.Parallel()
	notFound := execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "Error: Issue calc-3 not found"}
	one := `[{"id":"calc-2","title":"b","status":"closed","priority":2,"issue_type":"task"}]`
	runner := &fakeRunner{responses: []string{one}, failures: map[int]execution.ProcessResult{1: notFound}}
	client := cleanupProject(t, runner)

	still, err := client.MissingItems(context.Background(), []string{"calc-4", "calc-3", "calc-2"})
	if err != nil || !reflect.DeepEqual(still, []string{"calc-3"}) {
		t.Errorf("MissingItems() = %v, %v, want calc-3 still missing", still, err)
	}
	want := [][]string{{"show", "calc-2", "--json"}, {"show", "calc-3", "--json"}}
	if !reflect.DeepEqual(runner.args, want) {
		t.Errorf("bd args = %v, want one read per item, stopping at the first missing", runner.args)
	}
}

// Every item found again is none missing, and a read that fails is the
// tracker not answering, never an item that is gone.
func TestMissingItemsFoundAgainOrNotReadable(t *testing.T) {
	t.Parallel()
	one := `[{"id":"calc-2","title":"b","status":"closed","priority":2,"issue_type":"task"}]`
	found := &fakeRunner{responses: []string{one}}
	if still, err := cleanupProject(t, found).MissingItems(context.Background(), []string{"calc-2"}); err != nil || len(still) != 0 {
		t.Errorf("MissingItems() = %v, %v, want none missing", still, err)
	}
	locked := &fakeRunner{failures: map[int]execution.ProcessResult{0: {Status: execution.ProcessFailed, ExitCode: 1, Stderr: "database is locked"}}}
	if still, err := cleanupProject(t, locked).MissingItems(context.Background(), []string{"calc-2"}); err == nil || still != nil {
		t.Errorf("MissingItems() = %v, %v, want the read's failure", still, err)
	}
}
