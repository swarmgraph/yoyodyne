package runstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

func TestCollectedReportsSurviveTheProcessThatMadeThem(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestReportStore(t, root)
	first := testReport("report-0123456789abcdef0123456789abcdef", report.SeverityWarning, "the declared bundle version is inert")
	second := testReport("report-fedcba9876543210fedcba9876543210", report.SeverityNote, "bd lint could not run in the sandbox")
	for _, reported := range []report.Report{first, second} {
		if err := store.Append(reported); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	// A second process reads what the first wrote, in the order it was written:
	// a report outlives the run that made it, so the pile is what an operator
	// reads long afterwards rather than anything held in memory.
	reloaded, err := newTestReportStore(t, root).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(reloaded) != 2 {
		t.Fatalf("List() = %#v", reloaded)
	}
	if reloaded[0].ID != first.ID || reloaded[1].ID != second.ID {
		t.Fatalf("reports came back out of order: %s then %s", reloaded[0].ID, reloaded[1].ID)
	}
	if reloaded[0].Message != first.Message || reloaded[0].Severity != first.Severity {
		t.Fatalf("report did not survive intact: %#v", reloaded[0])
	}
	if !reloaded[0].RecordedAt.Equal(first.RecordedAt) {
		t.Fatalf("recorded time = %s, want %s", reloaded[0].RecordedAt, first.RecordedAt)
	}
}

func TestListingReportsBeforeAnybodyReportedIsNotAFailure(t *testing.T) {
	t.Parallel()

	reports, err := newTestReportStore(t, t.TempDir()).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(reports) != 0 {
		t.Fatalf("List() = %#v, want nothing", reports)
	}
}

func TestReportsAreRefusedWhenTheyCannotBeReadBackOrDoNotBelongHere(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestReportStore(t, root)
	// A record that fails its own contract never reaches the log, so anything in
	// the log is something an operator can be shown.
	invalid := testReport("report-0123456789abcdef0123456789abcdef", "urgent", "unknown severity")
	if err := store.Append(invalid); err == nil {
		t.Fatal("Append() accepted a report with an invalid severity")
	}
	// The store is per product, exactly as the run and conversation stores are.
	foreign := testReport("report-0123456789abcdef0123456789abcdef", report.SeverityNote, "from elsewhere")
	foreign.ProductID = "elsewhere"
	if err := store.Append(foreign); err == nil {
		t.Fatal("Append() accepted another product's report")
	}

	// A log line that cannot be decoded fails the read rather than being skipped:
	// a pile that quietly drops what it cannot parse is one nobody can trust to
	// be complete.
	if err := store.Append(testReport("report-0123456789abcdef0123456789abcdef", report.SeverityNote, "good")); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if err := os.WriteFile(store.Path(), []byte("{\"schema_version\":1}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := store.List(); err == nil || !strings.Contains(err.Error(), "decode report log") {
		t.Fatalf("List() error = %v, want a decode failure", err)
	}
}

func TestReportStoreKeepsThePileBesideTheRunsRatherThanAmongThem(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestReportStore(t, root)
	runs, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if strings.HasPrefix(store.Path(), runs.Root()+string(filepath.Separator)) {
		t.Fatalf("the report log is inside the run directory: %s", store.Path())
	}
	if filepath.Dir(store.Path()) != home.ProductDirectory(root, "yoyodyne") {
		t.Fatalf("report log path = %s", store.Path())
	}
}

func newTestReportStore(t *testing.T, root string) *ReportStore {
	t.Helper()

	store, err := NewReportStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewReportStore() error = %v", err)
	}
	return store
}

func testReport(id string, severity report.Severity, message string) report.Report {
	return report.Report{
		SchemaVersion: report.SchemaVersion,
		ID:            id,
		Role:          "developer",
		Agent:         "developer",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		WorkItemID:    "yoyodyne-ifd.19",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Severity:      severity,
		Message:       message,
		RecordedAt:    time.Date(2026, 8, 17, 9, 30, 0, 0, time.UTC),
	}
}

// What became of a report is durable in the same way and for the same reason the
// report is: the conversation that decided is long over before anybody asks what
// happened about this.
func TestWhatBecameOfAReportSurvivesTheProcessThatDecidedIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestReportStore(t, root)
	reported := testReport("report-0123456789abcdef0123456789abcdef", report.SeverityWarning, "the declared bundle version is inert")
	if err := store.Append(reported); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if err := store.Handle(testHandling(reported.ID, "admitted as yoyodyne-ifd.150")); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	reloaded := newTestReportStore(t, root)
	handlings, err := reloaded.Handlings()
	if err != nil {
		t.Fatalf("Handlings() error = %v", err)
	}
	if len(handlings) != 1 || handlings[0].ReportID != reported.ID || handlings[0].Reason != "admitted as yoyodyne-ifd.150" {
		t.Fatalf("Handlings() = %#v", handlings)
	}
	// The pile itself is untouched. A disposition is a second record about a
	// report, never an edit to it.
	pile, err := reloaded.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(pile) != 1 || pile[0].Message != reported.Message {
		t.Fatalf("the pile changed when a report was handled: %#v", pile)
	}
	if reloaded.HandlingPath() == reloaded.Path() {
		t.Fatal("the dispositions are written into the pile itself")
	}

	// Deciding again is history rather than a write to refuse, and the later
	// decision is the one that is read.
	second := testHandling(reported.ID, "and the work landed")
	second.RecordedAt = second.RecordedAt.Add(time.Hour)
	if err := reloaded.Handle(second); err != nil {
		t.Fatalf("Handle() a second time error = %v", err)
	}
	handlings, err = newTestReportStore(t, root).Handlings()
	if err != nil {
		t.Fatalf("Handlings() error = %v", err)
	}
	if current := report.Handled(handlings)[reported.ID]; current.Reason != "and the work landed" {
		t.Fatalf("the current disposition = %#v", current)
	}
}

// A store addresses one product's pile, and a disposition from another product's
// is refused for the reason a report from one is: two products' records must
// never be readable as one.
func TestAHandlingFromAnotherProductIsRefused(t *testing.T) {
	t.Parallel()

	store := newTestReportStore(t, t.TempDir())
	elsewhere := testHandling("report-0123456789abcdef0123456789abcdef", "dealt with")
	elsewhere.ProductID = "somebody-else"
	err := store.Handle(elsewhere)
	if err == nil || !strings.Contains(err.Error(), "does not match store product") {
		t.Fatalf("Handle() error = %v", err)
	}
	// A handling with nothing said about it takes a report out of everybody's
	// view and says nothing about why, which is worse than leaving it in.
	silent := testHandling("report-0123456789abcdef0123456789abcdef", "")
	if err := store.Handle(silent); err == nil || !strings.Contains(err.Error(), "reason is required") {
		t.Fatalf("Handle() with no reason error = %v", err)
	}
}

func testHandling(reportID, reason string) report.Handling {
	return report.Handling{
		SchemaVersion: report.HandlingSchemaVersion,
		ReportID:      reportID,
		Role:          "product-manager",
		Agent:         "product-manager",
		RunID:         "chat-0123456789abcdef0123456789abcdef",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Reason:        reason,
		RecordedAt:    time.Date(2026, 8, 22, 11, 0, 0, 0, time.UTC),
	}
}
