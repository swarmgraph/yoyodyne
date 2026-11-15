package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// storeReader answers the standing from a real run store, as the dashboard's
// own reader does, and everything else as the stub does.
type storeReader struct {
	stubReader
	runs *runstate.Store
	now  time.Time
}

func (r storeReader) Standing(ctx context.Context) (readmodel.Standing, error) {
	return readmodel.ReadStanding(ctx, readmodel.Sources{Runs: r.runs, Now: func() time.Time { return r.now }}), nil
}

// One run record the dashboard's build cannot read — a stop class a newer build
// wrote — does not blank /api/standing: the runs that read are on the running
// panel, nothing says the runs could not be read, and the unreadable record is
// among the factory problems as an unreadable run, by its run and its file.
func TestTheStandingEndpointServesTheReadableRunsAndNamesTheUnreadableOne(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
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
		started := now.Add(-time.Duration(index+1) * time.Minute)
		if err := store.Create(runstate.State{
			SchemaVersion: runstate.StateSchemaVersion, RunID: id, ProductID: "yoyodyne", RepositoryID: "yoyodyne",
			WorkItemID: item, Backend: "claude-code", Status: runstate.StatusRunning, StartedAt: started, UpdatedAt: started,
		}); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		ids = append(ids, id)
	}
	bad := ids[1]
	path := filepath.Join(store.Root(), bad+".json")
	refused := `{"schema_version": ` + strconv.Itoa(runstate.StateSchemaVersion) + `, "run_id": "` + bad + `", "product_id": "yoyodyne", "work_item_id": "yoyodyne-ifd.2", "status": "running", "stop_class": "a-stop-class-from-a-newer-build"}`
	if err := os.WriteFile(path, []byte(refused), 0o600); err != nil {
		t.Fatal(err)
	}

	w := serve(t, storeReader{runs: store, now: now})
	response, body := w.get("/api/standing", bearer(w.server.Token()))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/standing = %d %s", response.StatusCode, body)
	}
	var served struct {
		Running []struct {
			RunID string `json:"run_id"`
		} `json:"running"`
		RunningProblem  string `json:"running_problem"`
		FactoryProblems []struct {
			Kind          string `json:"kind"`
			ID            string `json:"id"`
			What          string `json:"what"`
			UnreadableRun *struct {
				RunID string `json:"run_id"`
				Path  string `json:"path"`
			} `json:"unreadable_run"`
		} `json:"factory_problems"`
	}
	if err := json.Unmarshal([]byte(body), &served); err != nil {
		t.Fatalf("decode /api/standing: %v\n%s", err, body)
	}
	if served.RunningProblem != "" {
		t.Fatalf("running_problem = %q, want the readable runs served", served.RunningProblem)
	}
	running := map[string]bool{}
	for _, run := range served.Running {
		running[run.RunID] = true
	}
	if len(running) != 2 || !running[ids[0]] || !running[ids[2]] || running[bad] {
		t.Fatalf("running = %+v, want exactly %s and %s", served.Running, ids[0], ids[2])
	}
	found := false
	for _, entry := range served.FactoryProblems {
		if entry.Kind == string(readmodel.AttentionUnreadableRun) && entry.ID == bad {
			found = entry.UnreadableRun != nil && entry.UnreadableRun.Path == path && entry.What != ""
		}
	}
	if !found {
		t.Fatalf("factory_problems = %+v, want the unreadable run %s named with its file %s", served.FactoryProblems, bad, path)
	}
}
