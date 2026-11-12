package maintain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Every pass rebuilds the index of run records. A record an older build wrote
// back in place as live — round the run store, so the index still says it
// ended — is among the runs in flight once the pass has run, and a record that
// will not read is named on the step rather than failing it.
func TestEveryPassRebuildsTheRunIndexAndNamesWhatWillNotRead(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	runs, err := runstate.NewStore(t.TempDir(), "calc")
	if err != nil {
		t.Fatal(err)
	}
	f.pass.RunIndex = runs

	started := f.now.Add(-time.Hour)
	completed := f.now.Add(-time.Minute)
	record := func(item string, status runstate.Status) runstate.State {
		id, err := runstate.NewRunID()
		if err != nil {
			t.Fatal(err)
		}
		state := runstate.State{
			SchemaVersion: runstate.StateSchemaVersion, RunID: id, ProductID: "calc", RepositoryID: "calc",
			WorkItemID: item, Backend: "claude-code", Status: status, StartedAt: started, UpdatedAt: completed,
		}
		if status.Terminal() {
			state.CompletedAt = &completed
		}
		return state
	}
	ended := record("calc-1", runstate.StatusSucceeded)
	if err := runs.Create(ended); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if active, err := runs.Incomplete(); err != nil || len(active) != 0 {
		t.Fatalf("Incomplete() = %+v, %v, want nothing in flight", active, err)
	}
	// Written back as live in place, by something that went round the store.
	revived := record("calc-1", runstate.StatusRunning)
	revived.RunID = ended.RunID
	writeRecord(t, runs, revived.RunID, revived)
	if active, err := runs.Incomplete(); err != nil || len(active) != 0 {
		t.Fatalf("Incomplete() before the pass = %+v, %v; the index was expected not to know yet", active, err)
	}
	// And one record nothing can read, which says it ended.
	spoiled := record("calc-2", runstate.StatusSucceeded)
	if err := runs.Create(spoiled); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	refused := `{"schema_version": ` + strconv.Itoa(runstate.StateSchemaVersion) + `, "run_id": "` + spoiled.RunID + `", "work_item_id": "calc-2", "status": "succeeded", "stop_class": "from-a-newer-build"}`
	if err := os.WriteFile(filepath.Join(runs.Root(), spoiled.RunID+".json"), []byte(refused), 0o600); err != nil {
		t.Fatal(err)
	}

	recorded := f.pass1(t)
	step := stepNamed(t, recorded, StepRunIndex)
	if step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "rebuilt") || !strings.Contains(step.Detail, spoiled.RunID) {
		t.Fatalf("run-index step = %+v, want it run, naming the record that could not be read", step)
	}
	active, err := runs.Incomplete()
	if err != nil || len(active) != 1 || active[0].RunID != revived.RunID {
		t.Fatalf("Incomplete() after the pass = %+v, %v, want the run written back as live", active, err)
	}
}

func writeRecord(t *testing.T, runs *runstate.Store, runID string, state runstate.State) {
	t.Helper()
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs.Root(), runID+".json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}
