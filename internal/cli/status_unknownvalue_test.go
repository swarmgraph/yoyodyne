package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A run a newer build recorded with a stop class and a landing outcome this
// build does not know, and a field it has never heard of, is listed by `yoyo
// status` with each value shown as unknown beside what was recorded, rather than
// the listing failing for it.
func TestStatusShowsAValueThisBuildDoesNotKnowAsUnknown(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)
	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	run := recordedRun(t, store, runstate.StatusFailed, "yoyodyne-later", time.Now().Add(-time.Minute))
	run.StopClass = runstate.StopRelaunchBudget
	run.Failure = "the run stopped"
	run.LandingOutcome = runstate.LandingEvidence
	run.LandingReason = "the work is waiting on another item"
	saveRun(t, store, run)

	path := filepath.Join(store.Root(), run.RunID+".json")
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var carried map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &carried); err != nil {
		t.Fatal(err)
	}
	carried["stop_class"] = json.RawMessage(`"a-cause-from-a-later-build"`)
	carried["landing_outcome"] = json.RawMessage(`"an-outcome-from-a-later-build"`)
	carried["a_field_from_a_later_build"] = json.RawMessage(`true`)
	rewritten, err := json.Marshal(carried)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, rewritten, 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runCLI(t, "status", "--config", configPath, "--failed")
	if code != 0 {
		t.Fatalf("status exited %d: %s", code, stderr)
	}
	for _, want := range []string{
		run.RunID,
		`stop cause: unknown to this build (recorded as "a-cause-from-a-later-build")`,
		`landing outcome: unknown to this build (recorded as "an-outcome-from-a-later-build")`,
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status output missing %q:\n%s", want, stdout)
		}
	}
}
