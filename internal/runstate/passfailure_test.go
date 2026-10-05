package runstate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

func TestAnUnfiledPassFailureIsFiledAndClearedAfterSuccess(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store, _ := NewSweepStore(root, "example")
	reports, _ := NewReportStore(root, "example")
	start := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		at := start.Add(time.Duration(i) * time.Hour)
		if err := store.Append(Sweep{Task: "maintenance", StartedAt: at, EndedAt: at.Add(time.Minute), Problem: "reconcile failed", Steps: []SweepStep{{Name: "reconcile", Outcome: StepFailed, Detail: "reconcile failed"}}}); err != nil {
			t.Fatal(err)
		}
	}
	// The log reads as absent, but its missing parent prevents an append.
	if err := os.Symlink(filepath.Join(root, "missing", "reports.jsonl"), reports.Path()); err != nil {
		t.Fatal(err)
	}
	attribution := report.Attribution{RepositoryID: "example"}
	if err := store.RecordPassFailures(context.Background(), reports, attribution, "factory-watch"); err == nil || !strings.Contains(err.Error(), "open report log") {
		t.Fatalf("filing error = %v, want a refused report append", err)
	}
	recoveredAt := start.Add(3*time.Hour + time.Minute)
	if err := store.Append(Sweep{Task: "maintenance", StartedAt: start.Add(3 * time.Hour), EndedAt: recoveredAt, Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "recovered"}, Steps: []SweepStep{{Name: "reconcile", Outcome: StepRan, Detail: "recovered"}}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(reports.Path()); err != nil {
		t.Fatal(err)
	}
	// Retry from a new store after the pass has already recovered, twice.
	restarted, _ := NewSweepStore(root, "example")
	for i := 0; i < 2; i++ {
		if err := restarted.RecordPassFailures(context.Background(), reports, attribution, "factory-watch"); err != nil {
			t.Fatal(err)
		}
	}
	filed, err := reports.List()
	if err != nil || len(filed) != 1 || !filed[0].RecordedAt.Equal(start.Add(2*time.Hour+time.Minute)) {
		t.Fatalf("filed = %+v, %v, want one finding at the third failure", filed, err)
	}
	handled, err := reports.Handlings()
	if err != nil || len(handled) != 1 || handled[0].ReportID != filed[0].ID || handled[0].PassFailureCleared != 3 || !handled[0].RecordedAt.Equal(recoveredAt) {
		t.Fatalf("clearing = %+v, %v, want one clearing after three failures", handled, err)
	}
}

func TestAPassFailureKeepsTheLastErrorOnOneLineWithoutCuttingACharacter(t *testing.T) {
	t.Parallel()
	failure := PassFailure{Task: "maintenance", Failures: 3, FirstAt: time.Now(), Problem: strings.Repeat("earlier output é\n", 300) + "the last error"}
	line := failure.Says()
	if strings.Contains(line, "\n") || !strings.HasSuffix(line, "the last error") || !utf8.ValidString(line) {
		t.Fatalf("failure line = %q", line)
	}
}

func TestSkippedAndPartialPassesDoNotCountAsFailuresOrClearOne(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	var passes []Sweep
	for i := 0; i < 3; i++ {
		passes = append(passes, Sweep{ProductID: "example", Task: "role-pass", Role: domain.RoleArchitect, StartedAt: start.Add(time.Duration(i) * time.Hour), EndedAt: start.Add(time.Duration(i)*time.Hour + time.Minute), Failed: true, Problem: "failed"})
	}
	passes = append(passes,
		Sweep{Task: "role-pass", Role: domain.RoleArchitect, StartedAt: start.Add(3 * time.Hour), EndedAt: start.Add(3*time.Hour + time.Minute), Problem: "waiting for the provider"},
		Sweep{Task: "role-pass", Role: domain.RoleArchitect, StartedAt: start.Add(4 * time.Hour), EndedAt: start.Add(4*time.Hour + time.Minute), Missed: &MissedPass{Trigger: PassTriggerSchedule, How: MissUnfired}, Problem: "unfired"},
		Sweep{Task: "role-pass", Role: domain.RoleArchitect, StartedAt: start.Add(5 * time.Hour), EndedAt: start.Add(5*time.Hour + time.Minute), Result: &sweep.Result{Status: sweep.StatusMore, Summary: "progress"}})
	findings := PassFailuresOf(passes)
	if len(findings) != 1 || findings[0].Failures != 3 || !findings[0].ClearedAt.IsZero() {
		t.Fatalf("finding = %+v", findings)
	}
}

func TestFailureFindingsAreSerializedAndRetriedFromTheSweepLog(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store, _ := NewSweepStore(root, "example")
	reports, _ := NewReportStore(root, "example")
	start := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		at := start.Add(time.Duration(i) * time.Hour)
		if err := store.Append(Sweep{Task: "role-pass", Role: domain.RoleArchitect, StartedAt: at, EndedAt: at.Add(time.Minute), Turns: 1, Failed: true, Problem: "turn failed"}); err != nil {
			t.Fatal(err)
		}
	}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := store.RecordPassFailures(context.Background(), reports, report.Attribution{RepositoryID: "example"}, "factory-watch"); err != nil {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	filed, err := reports.List()
	if err != nil || len(filed) != 1 {
		t.Fatalf("filed = %+v, %v", filed, err)
	}
	if err := store.Append(Sweep{Task: "role-pass", Role: domain.RoleArchitect, StartedAt: start.Add(4 * time.Hour), EndedAt: start.Add(4*time.Hour + time.Minute), Turns: 1, Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "recovered"}}); err != nil {
		t.Fatal(err)
	}
	// A new store after a lost callback finds and records the recovery once.
	restarted, _ := NewSweepStore(root, "example")
	for i := 0; i < 2; i++ {
		if err := restarted.RecordPassFailures(context.Background(), reports, report.Attribution{RepositoryID: "example"}, "factory-watch"); err != nil {
			t.Fatal(err)
		}
	}
	handled, err := reports.Handlings()
	if err != nil || len(handled) != 1 || !strings.Contains(handled[0].Reason, "after 3 consecutive failures") {
		t.Fatalf("clear = %+v, %v", handled, err)
	}
	// A later run is a new finding, with its own identity.
	for i := 5; i < 8; i++ {
		at := start.Add(time.Duration(i) * time.Hour)
		if err := store.Append(Sweep{Task: "role-pass", Role: domain.RoleArchitect, StartedAt: at, EndedAt: at.Add(time.Minute), NotStarted: PreTurnContextUnassembled, Problem: "context unassembled"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.RecordPassFailures(context.Background(), reports, report.Attribution{RepositoryID: "example"}, "factory-watch"); err != nil {
		t.Fatal(err)
	}
	filed, _ = reports.List()
	if len(filed) != 2 || filed[0].ID == filed[1].ID {
		t.Fatalf("new run = %+v", filed)
	}
}

func TestFailureOutputTailKeepsTheEndWithoutSplittingACharacter(t *testing.T) {
	output := FailureOutputTail(strings.Repeat("é", 3000) + "last cause")
	if !utf8.ValidString(output) || !strings.HasSuffix(output, "last cause") || !strings.HasPrefix(output, "[earlier output omitted;") || len(output) > MaxSweepTextBytes {
		t.Fatalf("tail = %q", output)
	}
}
