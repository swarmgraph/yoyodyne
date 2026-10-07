package cli

import (
	"bytes"
	"context"
	"encoding/json"
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
