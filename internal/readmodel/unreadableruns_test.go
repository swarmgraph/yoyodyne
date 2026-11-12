package readmodel

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// storeWithOneUnreadableRun is a run store holding two runs in flight that
// read, and a third in flight whose record was changed in place to one this
// build refuses, as a newer build's stop class once was. It returns the store,
// the two readable runs, and the unreadable one.
func storeWithOneUnreadableRun(t *testing.T) (*runstate.Store, []string, string) {
	t.Helper()
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, 3)
	for index, item := range []string{"yoyodyne-ifd.1", "yoyodyne-ifd.2", "yoyodyne-ifd.3"} {
		id, err := runstate.NewRunID()
		if err != nil {
			t.Fatal(err)
		}
		started := noon.Add(-time.Duration(index+1) * time.Minute)
		if err := store.Create(runstate.State{
			SchemaVersion: runstate.StateSchemaVersion,
			RunID:         id,
			ProductID:     "yoyodyne",
			RepositoryID:  "yoyodyne",
			WorkItemID:    item,
			Backend:       "claude-code",
			Status:        runstate.StatusRunning,
			StartedAt:     started,
			UpdatedAt:     started,
		}); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		ids = append(ids, id)
	}
	bad := ids[1]
	refused := `{"schema_version": ` + strconv.Itoa(runstate.StateSchemaVersion) + `, "run_id": "` + bad + `", "product_id": "yoyodyne", "work_item_id": "yoyodyne-ifd.2", "status": "running", "stop_class": "not-a-stop-class-this-build-knows"}`
	if err := os.WriteFile(filepath.Join(store.Root(), bad+".json"), []byte(refused), 0o600); err != nil {
		t.Fatal(err)
	}
	return store, []string{ids[0], ids[2]}, bad
}

// One run record the build cannot read does not blank the running line: the
// standing lists every run in flight that reads, says no problem in place of
// them, and names the unreadable record — by its run, its file, and what
// refused it — among the factory problems and on the attention line.
func TestTheStandingListsTheReadableRunsAndNamesTheUnreadableOne(t *testing.T) {
	t.Parallel()
	store, readable, bad := storeWithOneUnreadableRun(t)

	standing := ReadStanding(context.Background(), Sources{Runs: store, Now: func() time.Time { return noon }})
	if standing.RunningProblem != "" {
		t.Fatalf("running_problem = %q, want the readable runs listed", standing.RunningProblem)
	}
	listed := map[string]bool{}
	for _, run := range append(append([]RunningRun{}, standing.Running...), standing.PausedRuns...) {
		listed[run.RunID] = true
	}
	if len(listed) != 2 || !listed[readable[0]] || !listed[readable[1]] || listed[bad] {
		t.Fatalf("running = %+v, want exactly the two readable runs", listed)
	}
	if standing.SlotHoldersProblem != "" {
		t.Errorf("slot_holders_problem = %q, want the slot holders read past the record too", standing.SlotHoldersProblem)
	}

	path := filepath.Join(store.Root(), bad+".json")
	named := func(entries []Attention) *Attention {
		for index := range entries {
			if entries[index].Kind == AttentionUnreadableRun && entries[index].ID == bad {
				return &entries[index]
			}
		}
		return nil
	}
	entry := named(standing.FactoryProblems)
	if entry == nil {
		t.Fatalf("factory problems = %+v, want the unreadable run %s named", standing.FactoryProblems, bad)
	}
	if entry.UnreadableRun == nil || entry.UnreadableRun.Path != path || !strings.Contains(entry.UnreadableRun.Reason, "stop_class") {
		t.Fatalf("unreadable run = %+v, want its file %s and the stop class that refused it", entry.UnreadableRun, path)
	}
	if what := entry.What(); !strings.Contains(what, bad) || !strings.Contains(what, path) || !strings.Contains(what, "could not be read") {
		t.Fatalf("what = %q, want the run, its file, and that it could not be read", what)
	}
	if entry.Mover != MoverDevelopmentManager {
		t.Errorf("mover = %q, want the development manager", entry.Mover)
	}
	// Every other section that lists runs read past it too, rather than saying
	// it could not read the runs at all.
	if strings.Contains(standing.NeedsHumanProblem, bad) {
		t.Errorf("needs_human_problem = %q, want the sections that list runs to read past %s", standing.NeedsHumanProblem, bad)
	}
	if named(standing.NeedsHuman) == nil {
		t.Errorf("needs-a-human = %+v, want the unreadable run on the line yoyo status prints", standing.NeedsHuman)
	}
	if rendered := standing.Render(); !strings.Contains(rendered, bad) || !strings.Contains(rendered, "could not be read") {
		t.Errorf("rendered standing does not name the unreadable run %s:\n%s", bad, rendered)
	}
}
