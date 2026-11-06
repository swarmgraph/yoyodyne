package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// heavyCheckConfig is validConfig with the one check named heavy, as
// execution.heavy_checks names it.
const heavyCheckConfig = validConfig + `execution:
  heavy_checks:
    go test: 1
`

// The check times are read from the run records' event logs on disk, in the
// store the recorded mode reads, and printed week by week in the operator's
// zone, which is named; a project that names no heavy check is told so.
func TestStatusChecksPrintsTheHeavyChecksTimesWeekByWeek(t *testing.T) {
	// Not parallel: the state root and the zone the command reads are set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	t.Setenv("TZ", "America/New_York")

	stdout, stderr, code := runCLI(t, "status", "--checks", "--config", writeConfig(t, validConfig))
	if code != 0 || !strings.Contains(stdout, "Not recorded: the project names no heavy checks") {
		t.Fatalf("with no heavy checks, code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}

	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	asked := time.Now().Add(-time.Hour).Truncate(time.Second)
	run := recordedRun(t, store, runstate.StatusSucceeded, "yoyodyne-ifd.7", asked.Add(-time.Minute))
	saveRun(t, store, run)
	for sequence, recorded := range []struct {
		at      time.Time
		kind    execution.EventType
		payload map[string]any
	}{
		{asked, execution.EventCommandStarted, map[string]any{"command": "go test ./...", "kind": "check", "whole_module": true, "whole_module_reason": "go.mod changed, which every package depends on"}},
		// Two minutes waiting for a place, then twelve minutes run.
		{asked.Add(14 * time.Minute), execution.EventCommandCompleted, map[string]any{"command": "go test ./...", "kind": "check", "passed": true, "elapsed": "12m0s"}},
	} {
		event, err := execution.NewEvent(run.RunID, uint64(sequence+1), recorded.at, recorded.kind, "harness.checks", recorded.payload)
		if err != nil {
			t.Fatalf("NewEvent() error = %v", err)
		}
		if err := store.AppendEvent(event); err != nil {
			t.Fatalf("AppendEvent() error = %v", err)
		}
	}

	configPath := writeConfig(t, heavyCheckConfig)
	stdout, stderr, code = runCLI(t, "status", "--checks", "--config", configPath)
	if code != 0 {
		t.Fatalf("status --checks code = %d, stderr = %q", code, stderr)
	}
	for _, words := range []string{
		"week by week from Monday to Sunday in America/New_York (E",
		"go test: ran 1 time, 1 passed; the runs that passed took 12 min (median); each waited 2 min (median) for a free place",
		"told to run the whole module 1 time: 1 because go.mod changed, which every package depends on",
	} {
		if !strings.Contains(stdout, words) {
			t.Fatalf("status --checks does not say %q:\n%s", words, stdout)
		}
	}

	stdout, stderr, code = runCLI(t, "status", "--checks", "--json", "--config", configPath)
	var output checkTimesOutput
	if code != 0 || json.Unmarshal([]byte(stdout), &output) != nil || output.CheckTimes == nil {
		t.Fatalf("status --checks --json code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	// The run launched an hour ago, which is last week in the first hour of a
	// Monday, so its week is found rather than assumed.
	var current readmodel.CheckTimesWeek
	for _, week := range output.CheckTimes.Weeks {
		if len(week.Checks) > 0 {
			current = week
		}
	}
	if output.CheckTimes.Zone != "America/New_York" || len(current.Checks) != 1 || current.Checks[0].PassedRan.MedianSeconds != 720 || current.Checks[0].WholeModule != 1 {
		t.Fatalf("status --checks --json = %+v", output.CheckTimes)
	}
}

// The check times are one question, asked apart from the others, about every
// heavy check at once.
func TestStatusChecksRefusesWhatItCannotHonor(t *testing.T) {
	t.Parallel()

	for _, refused := range []struct {
		args []string
		want string
	}{
		{[]string{"status", "--checks", "--shipped"}, "different questions"},
		{[]string{"status", "--checks", "--failed"}, "--failed selects among the recorded runs"},
		{[]string{"status", "--checks", "--limit", "3"}, "--limit bounds a listing"},
		{[]string{"status", "--checks", "--kind", "runs"}, "takes only --config and --json"},
		{[]string{"status", "--checks", "--since", "week"}, "takes only --config and --json"},
		{[]string{"status", "--checks", "yoyodyne-ifd.7"}, "takes no id"},
	} {
		_, stderr, code := runCLI(t, refused.args...)
		if code != 2 {
			t.Fatalf("%v: code = %d, want 2; stderr = %q", refused.args, code, stderr)
		}
		if !strings.Contains(stderr, refused.want) {
			t.Fatalf("%v: stderr = %q, want it to contain %q", refused.args, stderr, refused.want)
		}
	}
}
