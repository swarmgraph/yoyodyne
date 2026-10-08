package beads

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func pacific(t *testing.T) *time.Location {
	t.Helper()
	zone, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	return zone
}

func TestTrackerRunsInUTC(t *testing.T) {
	t.Parallel()
	environment := inUTC([]string{"TZ=America/Los_Angeles", "PATH=/tools", "TZ=Asia/Tokyo"})
	if !slices.Equal(environment, []string{"PATH=/tools", "TZ=UTC"}) {
		t.Fatalf("environment = %v", environment)
	}
	runner := &fakeRunner{responses: []string{"updated"}}
	if _, err := (Client{Runner: runner}).run(context.Background(), "dep", "add", "a", "b"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(runner.commands[0].Env, "TZ=UTC") {
		t.Fatal("the bd invocation did not run with TZ=UTC, so the links it adds are stamped in the machine's wall clock")
	}
}

// TestDependencyStampsFromTheExportAreCorrected reads a sample of the links in
// this repository's own export whose stamps sit seven hours before the item
// that carries them. Every sampled item was created with its link — `bd create
// --parent`, or a blocker named at admission — so the instant each link was
// really made is the item's own creation, give or take the second bd takes.
func TestDependencyStampsFromTheExportAreCorrected(t *testing.T) {
	t.Parallel()
	file, err := os.Open(filepath.Join("testdata", "dependency-stamps.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	zone := pacific(t)
	corrected, untouched := 0, 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var raw rawWorkItem
		if err := json.Unmarshal(scanner.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		created := admittedAt(raw.CreatedAt)
		for _, dependency := range raw.Dependencies {
			written := admittedAt(dependency.CreatedAt)
			got := dependencyCreatedAt(dependency.CreatedAt, created, zone)
			if !written.Before(created.Add(-dependencyStampSlack)) {
				// A link stamped after its item is left as written, whatever its
				// writer's zone was: nothing in the stamp says it is wrong.
				if !got.Equal(written) {
					t.Errorf("%s -> %s: read %s, want %s as written", raw.ID, dependency.DependsOnID, got, written)
				}
				untouched++
				continue
			}
			if gap := got.Sub(created); gap < -5*time.Second || gap > 5*time.Second {
				t.Errorf("%s -> %s: written %s, read %s, want within seconds of the item's creation at %s",
					raw.ID, dependency.DependsOnID, dependency.CreatedAt, got.Format(time.RFC3339), raw.CreatedAt)
			}
			corrected++
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if corrected < 10 || untouched < 1 {
		t.Fatalf("sample corrected %d link(s) and left %d alone, want at least 10 and 1", corrected, untouched)
	}
}

func TestDependencyStampCorrection(t *testing.T) {
	t.Parallel()
	zone := pacific(t)
	itemCreated := time.Date(2026, 1, 15, 18, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		recorded string
		item     time.Time
		want     time.Time
	}{
		// Winter is eight hours behind, and the correction follows the zone's
		// own offset on that date rather than a fixed seven.
		{"pacific standard time", "2026-01-15T10:00:00Z", itemCreated, itemCreated},
		{"written in UTC", "2026-01-15T17:59:59Z", itemCreated, itemCreated.Add(-time.Second)},
		{"added later", "2026-01-16T09:00:00Z", itemCreated, time.Date(2026, 1, 16, 9, 0, 0, 0, time.UTC)},
		// Still before its item when read in Pacific time, so written in some
		// other zone; left as written rather than guessed at.
		{"another zone", "2026-01-14T10:00:00Z", itemCreated, time.Date(2026, 1, 14, 10, 0, 0, 0, time.UTC)},
		{"item creation unknown", "2026-01-15T10:00:00Z", time.Time{}, time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)},
		{"unreadable", "yesterday", itemCreated, time.Time{}},
	}
	for _, tc := range cases {
		if got := dependencyCreatedAt(tc.recorded, tc.item, zone); !got.Equal(tc.want) {
			t.Errorf("%s: dependencyCreatedAt(%q) = %s, want %s", tc.name, tc.recorded, got, tc.want)
		}
	}
}

func TestOnlyAnEdgeCarriesTheLinkStamp(t *testing.T) {
	t.Parallel()
	items, err := decodeWorkItems([]byte(`[
		{"id":"p-1.1","created_at":"2026-10-08T05:00:40Z","dependencies":[
			{"issue_id":"p-1.1","depends_on_id":"p-1","type":"parent-child","created_at":"2026-10-08T05:00:39Z"}]},
		{"id":"p-2.1","created_at":"2026-10-08T05:00:40Z","dependencies":[
			{"id":"p-2","dependency_type":"parent-child","created_at":"2026-10-01T05:00:00Z"}]}]`))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := items[0].Dependencies[0].CreatedAt, time.Date(2026, 10, 8, 5, 0, 39, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("an edge's link stamp = %s, want %s", got, want)
	}
	if got := items[1].Dependencies[0].CreatedAt; !got.IsZero() {
		t.Fatalf("a linked item's own creation was read as the link's stamp: %s", got)
	}
}

// TestDependencyStampConformance makes links through the harness at a known
// instant and reads their stamps back from the export, with bd running in a
// zone nine hours ahead of UTC. Before the harness ran bd in UTC, both stamps
// came back nine hours late. Not parallel, because the zone is the process's
// environment.
func TestDependencyStampConformance(t *testing.T) {
	t.Setenv("TZ", "Asia/Tokyo")
	project := newTracker(t)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	before := time.Now().UTC().Truncate(time.Second)
	parent, err := client.Create(ctx, NewWorkItem{Title: "Measure delivery", Description: "The whole.", Type: "epic"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := client.Create(ctx, NewWorkItem{Title: "Chart lead time", Description: "One part.", Type: "task", Parent: parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := client.Create(ctx, NewWorkItem{Title: "Serve the numbers", Description: "First.", Type: "task"})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.AddBlocker(ctx, child.ID, blocker.ID); err != nil {
		t.Fatal(err)
	}
	after := time.Now().UTC().Add(time.Second)

	if err := client.RefreshExport(ctx); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(project, ExportPath))
	if err != nil {
		t.Fatal(err)
	}
	stamps := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		var raw rawWorkItem
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			t.Fatal(err)
		}
		if raw.ID != child.ID {
			continue
		}
		for _, dependency := range raw.Dependencies {
			stamps[dependency.DependsOnID] = dependency.CreatedAt
		}
	}
	for _, linked := range []string{parent.ID, blocker.ID} {
		recorded, ok := stamps[linked]
		if !ok {
			t.Fatalf("the export carries no link from %s to %s: %v", child.ID, linked, stamps)
		}
		stamp, err := time.Parse(time.RFC3339, recorded)
		if err != nil {
			t.Fatal(err)
		}
		if stamp.Before(before) || stamp.After(after) {
			t.Errorf("%s stamped the link %s -> %s at %s, want between %s and %s; the stamp is the writer's wall clock "+
				"labelled UTC unless bd runs in UTC", bdVersion(t, project), child.ID, linked, recorded,
				before.Format(time.RFC3339), after.Format(time.RFC3339))
		}
	}
}
