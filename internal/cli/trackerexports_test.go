package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

func TestTrackerExportMaintenanceRecordsAndReportsItsRemovals(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skipf("SKIPPED, not passed: lsof is unavailable: %v", err)
	}
	parts := components{repository: t.TempDir(), stateRoot: t.TempDir(), runner: execution.OSProcessRunner{}}
	parts.config.Product.ID = "product"
	if err := os.Mkdir(filepath.Join(parts.repository, ".beads"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parts.repository, ".beads", ".~issues.jsonl.1")
	if err := os.WriteFile(path, []byte("unfinished"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * beads.ExportTemporaryAge)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	cleaned, err := maintainTrackerExports(context.Background(), parts)
	if err != nil || len(cleaned.Removed) != 1 {
		t.Fatalf("maintenance = %+v, %v", cleaned, err)
	}
	entries, err := os.ReadDir(filepath.Join(home.ProductDirectory(parts.stateRoot, "product"), "tracker-export-cleanups"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("durable removal records = %v, %v", entries, err)
	}
	var stdout, stderr bytes.Buffer
	sweep := reconcileSweep{TrackerExports: cleaned}
	if code := reportReconcileResult(&stdout, &stderr, false, sweep, nil); code != 0 || !strings.Contains(stdout.String(), "removed abandoned tracker export temporary") {
		t.Fatalf("removal output = %q, %q, code %d", stdout.String(), stderr.String(), code)
	}
	stdout.Reset()
	if code := reportReconcileResult(&stdout, &stderr, true, sweep, nil); code != 0 {
		t.Fatal(stderr.String())
	}
	var output reconcileOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil || len(output.TrackerExports.Removed) != 1 {
		t.Fatalf("JSON removals = %+v, %v", output.TrackerExports, err)
	}
}

// The clean-up's failure to refresh the export leads with the cause where the
// export ran out of time, and is still the export's failure; any other failure
// is said as it was.
func TestTheCleanUpsExportFailureNamesItsCause(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".beads", "embeddeddolt"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".beads", "metadata.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".beads", "embeddeddolt", "chunks"), make([]byte, 3<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		holders []beads.StoreHolder
		want    string
	}{
		"behind another process": {
			holders: []beads.StoreHolder{{PID: 38601, Command: "bd list --all --json"}},
			want:    "refresh the tracker export: exporting every work item ran 0s without finishing, on a store of 3.0 MB on disk, while 1 other process(es) had the store open, so it was most likely waiting behind them: bd export failed with status timed_out",
		},
		"with nothing else holding the store": {
			want: "refresh the tracker export: exporting every work item ran 0s without finishing, on a store of 3.0 MB on disk, while no other process had the store open, so the tracker itself was too slow: bd export failed with status timed_out",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client := beads.Client{Runner: timedOutRunner{}, Dir: project, Holders: holdersAre(tc.holders)}
			err := refreshTrackerExport(client.RefreshExport(context.Background()))
			if err == nil || !beads.Unanswered(err) {
				t.Fatalf("refreshTrackerExport() = %v, want the export's timeout", err)
			}
			if !strings.HasPrefix(zeroRan(err.Error()), tc.want) {
				t.Errorf("refreshTrackerExport() = %q, want it to begin %q", err, tc.want)
			}
		})
	}
	if err := refreshTrackerExport(errors.New("lock the tracker export: denied")); err == nil || err.Error() != "refresh the tracker export: lock the tracker export: denied" {
		t.Errorf("refreshTrackerExport() of another failure = %v", err)
	}
	if err := refreshTrackerExport(nil); err != nil {
		t.Errorf("refreshTrackerExport(nil) = %v", err)
	}
}

// timedOutRunner answers every call as bd stopped at its limit.
type timedOutRunner struct{}

func (timedOutRunner) Run(context.Context, execution.Command, execution.OutputObserver) (execution.ProcessResult, error) {
	return execution.ProcessResult{Status: execution.ProcessTimedOut, ExitCode: -1}, nil
}

// holdersAre is a finder that names the holders it was given.
type holdersAre []beads.StoreHolder

func (h holdersAre) Holders(context.Context, string) ([]beads.StoreHolder, error) { return h, nil }

// zeroRan says the duration of the first "ran ..." as 0s, since a call the
// runner answers at once runs for a few microseconds.
func zeroRan(said string) string {
	start := strings.Index(said, " ran ")
	if start < 0 {
		return said
	}
	end := strings.Index(said[start+len(" ran "):], " ")
	if end < 0 {
		return said
	}
	return said[:start] + " ran 0s" + said[start+len(" ran ")+end:]
}
