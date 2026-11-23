package cli

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type unfinishedItems []beads.WorkItem

func (u unfinishedItems) ListUnfinished(context.Context) ([]beads.WorkItem, error) { return u, nil }

type latestRuns map[string]time.Time

func (l latestRuns) Latest(id string) (runstate.State, error) {
	if at, found := l[id]; found {
		return runstate.State{WorkItemID: id, StartedAt: at}, nil
	}
	return runstate.State{}, fmt.Errorf("%w: %s", runstate.ErrNoRecordedRun, id)
}

// The reading carries the latest tag, each release item the Lead Product
// Manager decided with what it waits on, and every priority 0 item, each with
// its latest run; ordinary work at another priority is left out.
func TestWaitingWorkReadsTheReleaseWhatItWaitsOnAndThePriority0Work(t *testing.T) {
	t.Parallel()
	admitted := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	ran := admitted.Add(48 * time.Hour)
	release := beads.WorkItem{ID: "x-release", Title: "Cut v0.6.0: next", Status: "open", Priority: 1, CreatedAt: admitted,
		Origin:       domain.WorkItemOrigin{AdmittedBy: domain.RoleProductManager},
		Dependencies: []beads.Dependency{{ID: "x-fix", Type: beads.BlocksDependency, Status: "open"}}}
	items := unfinishedItems{
		release,
		{ID: "x-fix", Title: "Readiness", Status: "open", Priority: 2, CreatedAt: admitted},
		{ID: "x-p0", Title: "Merge queue", Status: "open", Priority: 0, CreatedAt: admitted, Parking: domain.WorkItemParking("waits on the operator")},
		{ID: "x-other", Title: "Other", Status: "open", Priority: 1, CreatedAt: admitted},
	}
	reader := waitingWork{items: items, runs: latestRuns{"x-fix": ran},
		runner: &scriptedRunner{outputs: map[string]string{"git": "v0.5.0\t2026-09-20T14:03:00-07:00\n"}}, repository: t.TempDir()}
	reading, err := reader.Waiting(context.Background())
	if err != nil {
		t.Fatalf("Waiting() error = %v", err)
	}
	if reading.LatestTag != "v0.5.0" || !reading.TaggedAt.Equal(time.Date(2026, 9, 20, 21, 3, 0, 0, time.UTC)) {
		t.Errorf("latest tag = %q at %s", reading.LatestTag, reading.TaggedAt)
	}
	if len(reading.Items) != 3 {
		t.Fatalf("items = %+v, want the release, what it waits on, and the priority 0 item", reading.Items)
	}
	got := map[string]bool{}
	for _, item := range reading.Items {
		got[item.ID] = true
		switch item.ID {
		case "x-release":
			if !item.Release || len(item.WaitsOn) != 1 || item.WaitsOn[0] != "x-fix" || !item.LastRun.IsZero() {
				t.Errorf("release = %+v", item)
			}
		case "x-fix":
			if item.Release || !item.LastRun.Equal(ran) {
				t.Errorf("blocker = %+v, want its latest run", item)
			}
		case "x-p0":
			if item.Parking != "waits on the operator" || !item.AdmittedAt.Equal(admitted) {
				t.Errorf("priority 0 = %+v", item)
			}
		}
	}
	if got["x-other"] {
		t.Error("ordinary priority 1 work was read")
	}
}

func TestWaitingWorkReadsNoTagWhereTheRepositoryHasNone(t *testing.T) {
	t.Parallel()
	reader := waitingWork{items: unfinishedItems{}, runs: latestRuns{},
		runner: &scriptedRunner{outputs: map[string]string{"git": ""}}, repository: t.TempDir()}
	reading, err := reader.Waiting(context.Background())
	if err != nil || reading.LatestTag != "" || !reading.TaggedAt.IsZero() {
		t.Errorf("Waiting() = %+v, %v, want no tag", reading, err)
	}
}
