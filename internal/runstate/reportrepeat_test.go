package runstate

import (
	"os"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/report"
)

// Two runs file the same fact, differing only in a run identifier, a commit
// and a number. The pile keeps one standing report, written once and never
// edited, and one repeat record naming it.
func TestAReportThatRepeatsAStandingOneIsKeptAsARepeatOfIt(t *testing.T) {
	t.Parallel()

	store := newTestReportStore(t, t.TempDir())
	standing := testReport("report-0123456789abcdef0123456789abcdef", report.SeverityWarning, "run run-1111aaaa left a server on port 8080 at 6ab5a03f")
	filed, err := store.File(standing, "", nil)
	if err != nil || filed.Repeat != nil {
		t.Fatalf("File(first) = %#v, %v", filed, err)
	}
	before, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}

	again := testReport("report-fedcba9876543210fedcba9876543210", report.SeverityNote, "run run-2222bbbb left a server on port 9090 at b8f3ef41")
	again.RunID = "run-fedcba9876543210fedcba9876543210"
	filed, err = store.File(again, "", nil)
	if err != nil || filed.Repeat == nil || filed.Repeat.ReportID != standing.ID || filed.Count != 1 || filed.Repeat.Message != again.Message || filed.Repeat.RunID != again.RunID {
		t.Fatalf("File(repeat) = %#v, %v", filed, err)
	}
	named := testReport("report-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", report.SeverityWarning, "the same server, worded otherwise")
	filed, err = store.File(named, standing.ID, []string{standing.ID})
	if err != nil || filed.Repeat == nil || !filed.Repeat.Named || filed.Count != 2 {
		t.Fatalf("File(named) = %#v, %v", filed, err)
	}

	after, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("the standing report's log changed when it was repeated")
	}
	reports, err := store.List()
	if err != nil || len(reports) != 1 {
		t.Fatalf("List() = %d reports, %v", len(reports), err)
	}
	repeats, err := store.Repeats()
	if err != nil || len(repeats) != 2 {
		t.Fatalf("Repeats() = %#v, %v", repeats, err)
	}

	// A name that was not on the run's list files a new report and says why.
	unlisted := testReport("report-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", report.SeverityNote, "worded otherwise again")
	filed, err = store.File(unlisted, standing.ID, nil)
	if err != nil || filed.Repeat != nil || !strings.Contains(filed.NotUsed, "not on the list") {
		t.Fatalf("File(unlisted) = %#v, %v", filed, err)
	}
}

// A handling of the standing report covers its repeats: a matching report
// filed after it is a new report, because the unhandled check is made again
// under the lock the repeat would be written under.
func TestAReportFiledAfterTheStandingOneWasHandledIsANewReport(t *testing.T) {
	t.Parallel()

	store := newTestReportStore(t, t.TempDir())
	standing := testReport("report-0123456789abcdef0123456789abcdef", report.SeverityWarning, "the sandbox refused the temporary folder")
	if _, err := store.File(standing, "", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Handle(testHandling(standing.ID, "admitted as work")); err != nil {
		t.Fatal(err)
	}
	again := testReport("report-fedcba9876543210fedcba9876543210", report.SeverityWarning, "the sandbox refused the temporary folder")
	filed, err := store.File(again, standing.ID, []string{standing.ID})
	if err != nil || filed.Repeat != nil || !strings.Contains(filed.NotUsed, "handled since") {
		t.Fatalf("File(after handling) = %#v, %v", filed, err)
	}
	if reports, err := store.List(); err != nil || len(reports) != 2 {
		t.Fatalf("List() = %d, %v", len(reports), err)
	}
}

// A repeat record a restart cut short is dropped when read, and the ones
// around it still count.
func TestARepeatCutShortIsDroppedWhenRead(t *testing.T) {
	t.Parallel()

	store := newTestReportStore(t, t.TempDir())
	standing := testReport("report-0123456789abcdef0123456789abcdef", report.SeverityWarning, "a test server was left running")
	if _, err := store.File(standing, "", nil); err != nil {
		t.Fatal(err)
	}
	again := testReport("report-fedcba9876543210fedcba9876543210", report.SeverityWarning, "a test server was left running")
	if _, err := store.File(again, "", nil); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(store.RepeatPath(), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"schema_version":1,"report_id":"report-0123`); err != nil {
		t.Fatal(err)
	}
	file.Close()
	repeats, err := store.Repeats()
	if err != nil || len(repeats) != 1 {
		t.Fatalf("Repeats() = %#v, %v", repeats, err)
	}
}
