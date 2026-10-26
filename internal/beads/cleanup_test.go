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

const (
	threeItems = `[{"id":"calc-1","title":"a","status":"open","priority":2,"issue_type":"task"},{"id":"calc-2","title":"b","status":"closed","priority":2,"issue_type":"task"},{"id":"calc-3","title":"c","status":"open","priority":2,"issue_type":"task"}]`
	twoItems   = `[{"id":"calc-1","title":"a","status":"open","priority":2,"issue_type":"task"},{"id":"calc-3","title":"c","status":"open","priority":2,"issue_type":"task"}]`
)

// A cleanup counts every work item, closed ones included, before bd's cleanup
// and after it, and names the items it held before that it does not after.
func TestACleanupCountsTheStoresItemsEitherSide(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		after       string
		wantAfter   int
		wantMissing []string
	}{
		"nothing lost": {after: threeItems, wantAfter: 3},
		"an item lost": {after: twoItems, wantAfter: 2, wantMissing: []string{"calc-2"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			runner := &fakeRunner{responses: []string{threeItems, "", tc.after}}
			client := cleanupProject(t, runner)

			cleaned, err := client.CleanUpStore(context.Background(), time.Minute)
			if err != nil {
				t.Fatalf("CleanUpStore() error = %v", err)
			}
			if !cleaned.Ran() || !cleaned.Counted || cleaned.ItemsBefore != 3 || cleaned.ItemsAfter != tc.wantAfter || !reflect.DeepEqual(cleaned.Missing, tc.wantMissing) {
				t.Errorf("CleanUpStore() = %+v, want 3 items before, %d after, missing %v", cleaned, tc.wantAfter, tc.wantMissing)
			}
			if changed := cleaned.ItemsChanged(); changed != (tc.wantMissing != nil) {
				t.Errorf("ItemsChanged() = %t, want %t", changed, tc.wantMissing != nil)
			}
			want := [][]string{
				{"list", "--json", "--limit=0", "--all"},
				{"gc", "--skip-decay", "--force"},
				{"list", "--json", "--limit=0", "--all"},
			}
			if !reflect.DeepEqual(runner.args, want) {
				t.Errorf("bd args = %v, want the count, the cleanup, and the count", runner.args)
			}
		})
	}
}

// A store whose items cannot be counted is not cleaned up, since nothing could
// then show the cleanup lost none.
func TestACleanupWhoseItemsCannotBeCountedIsNotStarted(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "database is locked"}}}
	client := cleanupProject(t, runner)

	cleaned, err := client.CleanUpStore(context.Background(), time.Minute)
	if err == nil || !strings.Contains(err.Error(), "could not be counted before its store was cleaned up, so the cleanup was not started") || !strings.Contains(err.Error(), "database is locked") {
		t.Fatalf("CleanUpStore() error = %v, want the count's failure said", err)
	}
	if cleaned.Ran() || len(runner.args) != 1 {
		t.Errorf("CleanUpStore() = %+v after %v, want nothing started", cleaned, runner.args)
	}
}

// A cleanup bd fails is returned with what was measured of it, so it can be
// recorded as one that ran, and nothing is counted after it.
func TestAFailedCleanupIsReturnedAsOneThatRan(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{responses: []string{threeItems}, failures: map[int]execution.ProcessResult{1: {Status: execution.ProcessFailed, ExitCode: 1, Stderr: "dolt gc: disk full"}}}
	client := cleanupProject(t, runner)

	cleaned, err := client.CleanUpStore(context.Background(), time.Minute)
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("CleanUpStore() error = %v, want bd's failure", err)
	}
	if !cleaned.Ran() || cleaned.Counted || cleaned.ItemsBefore != 3 || cleaned.SizeAfter != cleaned.SizeBefore || cleaned.SizeBefore == 0 {
		t.Errorf("CleanUpStore() = %+v, want a cleanup that ran, counted before, measured either side", cleaned)
	}
	if len(runner.args) != 2 {
		t.Errorf("bd args = %v, want no count after a failed cleanup", runner.args)
	}
}
