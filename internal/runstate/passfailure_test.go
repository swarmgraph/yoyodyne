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

func TestSkippedPassesNeitherCountNorClearButAPartialPassClears(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	var passes []Sweep
	for i := 0; i < 3; i++ {
		passes = append(passes, Sweep{ProductID: "example", Task: "role-pass", Role: domain.RoleArchitect, StartedAt: start.Add(time.Duration(i) * time.Hour), EndedAt: start.Add(time.Duration(i)*time.Hour + time.Minute), Failed: true, Problem: "failed"})
	}
	passes = append(passes,
		Sweep{Task: "role-pass", Role: domain.RoleArchitect, StartedAt: start.Add(3 * time.Hour), EndedAt: start.Add(3*time.Hour + time.Minute), Problem: "waiting for the provider"},
		Sweep{Task: "role-pass", Role: domain.RoleArchitect, StartedAt: start.Add(4 * time.Hour), EndedAt: start.Add(4*time.Hour + time.Minute), Missed: &MissedPass{Trigger: PassTriggerSchedule, How: MissUnfired}, Problem: "unfired"})
	findings := PassFailuresOf(passes)
	if len(findings) != 1 || findings[0].Failures != 3 || !findings[0].ClearedAt.IsZero() {
		t.Fatalf("finding = %+v, want three failures standing through a wait and a miss", findings)
	}
	// A pass that carried out its actions and says more work waits did its
	// work: it ends the run and clears the finding, as a finished pass does.
	partialEnd := start.Add(5*time.Hour + time.Minute)
	passes = append(passes, Sweep{Task: "role-pass", Role: domain.RoleArchitect, StartedAt: start.Add(5 * time.Hour), EndedAt: partialEnd, Turns: 4, Result: &sweep.Result{Status: sweep.StatusMore, Summary: "progress"}})
	findings = PassFailuresOf(passes)
	if len(findings) != 1 || findings[0].Failures != 3 || !findings[0].ClearedAt.Equal(partialEnd) {
		t.Fatalf("finding = %+v, want it cleared by the partial pass", findings)
	}
}

// The architect's passes of 2026-10-04 and 05: every working pass wrote its
// report block several times in a reply and ended "more", and the few that
// failed were hours apart. Those were counted as 14 failures in a row, and the
// finding could never clear. Failures separated by working passes are not a run.
func TestWorkingPassesWithSeveralBlocksBreakARunOfFailures(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	several := "the architect answered with more than one sweep block: the reply carried 4 sweep blocks, and the last of them is the account recorded; extra blocks are not a failure"
	var passes []Sweep
	for i := 0; i < 12; i++ {
		at := start.Add(time.Duration(i) * time.Hour)
		pass := Sweep{Task: "architect-pass", Role: domain.RoleArchitect, StartedAt: at, EndedAt: at.Add(time.Minute), Turns: 4,
			Result: &sweep.Result{Status: sweep.StatusMore, Summary: "progress"}, Problem: several}
		if i%3 == 0 {
			pass.Failed = true
			pass.Problem = several + "; turn 4 of the recurring task architect-pass failed, so its pass is partial: the architect asked for a question put to the developer, which that role has no authority for; nothing was carried out"
		}
		passes = append(passes, pass)
	}
	if findings := PassFailuresOf(passes); len(findings) != 0 {
		t.Fatalf("findings = %+v, want none: no two failures were in a row", findings)
	}
	// Nor does a pass that only wrote several blocks count as failed at all.
	if failed, worked := passFailedOrWorked(passes[1]); failed || !worked {
		t.Fatalf("a pass with several blocks and more waiting: failed %v, worked %v", failed, worked)
	}
}

// A line about a failing pass leads with what went wrong in ordinary words, and
// the record's own text follows it.
func TestAFailingPassLineLeadsWithWhatWentWrong(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 5, 16, 24, 0, 0, time.UTC)
	for _, c := range []struct {
		pass Sweep
		says string
	}{
		{Sweep{Role: domain.RoleArchitect, Turns: 3, Failed: true, Result: &sweep.Result{Status: sweep.StatusMore},
			Problem: "turn 4 of the recurring task architect-pass failed, so its pass is partial: the architect asked for a question put to the developer, which that role has no authority for; nothing was carried out"},
			"the architect asked the developer a question, which it may not do, so the actions of turn 4 were not carried out"},
		{Sweep{Role: domain.RoleArchitect, Turns: 2, Failed: true, Problem: "turn 3 of the recurring task architect-pass failed, so its pass is partial: an ask the harness cannot read: invalid ask"},
			"the architect wrote a question for another role that the harness could not read, so the actions of turn 3 were not carried out"},
		{Sweep{Role: domain.RoleArchitect, Turns: 1, Problem: "the architect answered in prose without a sweep block"},
			"the architect gave no account of the pass, so what it found is only in its conversation"},
		{Sweep{Role: domain.RoleDevelopmentManager, NotStarted: PreTurnMessageRefused, Problem: "message too large"},
			"the harness refused the message it composed for the pass, so nothing was asked of the development manager"},
		{Sweep{Role: domain.RoleArchitect, Turns: 2, Failed: true, Problem: "turn 2 of the recurring task architect-pass failed: the provider stopped"},
			"turn 2 of the architect's pass stopped before its actions were carried out"},
	} {
		if got := PassWentWrong(c.pass); got != c.says {
			t.Errorf("PassWentWrong() = %q, want %q", got, c.says)
		}
	}
	failure := PassFailure{Task: "architect-pass", Failures: 3, FirstAt: at, WentWrong: PassWentWrong(Sweep{Role: domain.RoleArchitect, Turns: 1}), Problem: "the architect answered in prose without a sweep block"}
	line := failure.Says()
	if !strings.HasPrefix(line, "the architect gave no account of the pass") || !strings.Contains(line, "has failed 3 times in a row") || !strings.HasSuffix(line, "the architect answered in prose without a sweep block") {
		t.Fatalf("line = %q", line)
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
