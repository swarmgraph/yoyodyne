package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A standing report two later runs repeated is listed once, with the count and
// the latest repeat under it, and --json carries the repeat records under it.
func TestTheReportsListingShowsAStandingReportOnceWithItsRepeats(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	store, err := runstate.NewReportStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	filing := func(id, runID string, minute int) report.Report {
		return report.Report{
			SchemaVersion: report.SchemaVersion, ID: id, Role: "developer", Agent: "developer",
			RunID: runID, WorkItemID: "yoyodyne-ifd.66", ProductID: "yoyodyne", RepositoryID: "yoyodyne",
			Severity: report.SeverityWarning, Message: "the sandbox refused the temporary folder in run " + runID,
			RecordedAt: time.Date(2026, 11, 27, 9, minute, 0, 0, time.UTC),
		}
	}
	const standingID = "report-0123456789abcdef0123456789abcde1"
	for index, reported := range []report.Report{
		filing(standingID, "run-1111", 0),
		filing("report-0123456789abcdef0123456789abcde2", "run-2222", 1),
		filing("report-0123456789abcdef0123456789abcde3", "run-3333", 2),
	} {
		filed, err := store.File(reported, "", nil)
		if err != nil || (index > 0) != (filed.Repeat != nil) {
			t.Fatalf("File(%d) = %#v, %v", index, filed, err)
		}
	}

	stdout, stderr, code := runCLI(t, "reports", "--config", configPath)
	if code != 0 {
		t.Fatalf("reports code = %d, stderr = %q", code, stderr)
	}
	if strings.Count(stdout, "report-") != 1 {
		t.Fatalf("the standing report is not listed once:\n%s", stdout)
	}
	if want := "repeated 2 times, the latest by run run-3333 on yoyodyne-ifd.66 at "; !strings.Contains(stdout, want) {
		t.Fatalf("listing does not say %q:\n%s", want, stdout)
	}

	stdout, stderr, code = runCLI(t, "reports", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("reports --json code = %d, stderr = %q", code, stderr)
	}
	var decoded reportsOutput
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Reports) != 1 || decoded.Reports[0].ID != standingID || len(decoded.Reports[0].Repeats) != 2 || decoded.Reports[0].Repeats[1].RunID != "run-3333" {
		t.Fatalf("reports = %#v", decoded.Reports)
	}
}

// A run's closing lines say it repeated a standing report and the count it
// brought it to, and why a name it gave was not used.
func TestARunsClosingLinesSayWhatItRepeated(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	reportCollectedReports(&out, console.NewTheme(func(string) string { return "" }, nil), orchestrator.Outcome{
		Repeats:     []report.Counted{{Repeat: report.Repeat{ReportID: "report-0123456789abcdef0123456789abcde1", Severity: report.SeverityNote}, Count: 16}},
		ReportNotes: "report report-0123456789abcdef0123456789abcde2 was filed as a new report because it names \"x\" as the report it repeats, which was not on the list of standing reports the run was handed",
	})
	for _, want := range []string{"repeated report report-0123456789abcdef0123456789abcde1 rather than filing a new one; it has now been repeated 16 times", "was filed as a new report because"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("closing lines = %q, want %q", out.String(), want)
		}
	}
	if strings.Contains(out.String(), "reported 1 thing") {
		t.Errorf("a repeat was said as a new report: %q", out.String())
	}
}

// After the outcome, a run prints one block holding the reports it filed that
// no pass reads, whole with severity and addressee, and then what its refused
// reports said, after the closing line that says what was wrong.
func TestARunsClosingLinesPrintUnreadReportsThenRefusedMessages(t *testing.T) {
	t.Parallel()
	unread := report.Report{ID: "report-0123456789abcdef0123456789abcde3", For: domain.RoleDevelopmentManager, Severity: report.SeverityWarning, Message: "the test server is left running"}
	refused := report.RefusedBy{Role: domain.RoleDeveloper, Refused: report.Refused{Severity: "loud", Message: "the plugin crashes", Why: `severity "loud" must be "critical", "warning", or "note"`}}
	var out bytes.Buffer
	reportCollectedReports(&out, console.NewTheme(func(string) string { return "" }, nil), orchestrator.Outcome{
		Reports:        []report.Report{unread},
		ReportProblem:  refused.Refused.Says(refused.Role),
		UnreadReports:  []report.Report{unread},
		RefusedReports: []report.RefusedBy{refused},
	})
	said := out.String()
	problem := strings.Index(said, `a developer report was refused: severity "loud"`)
	whole := strings.Index(said, "warning report report-0123456789abcdef0123456789abcde3, for the development manager, which no pass reads here: the test server is left running")
	message := strings.Index(said, "refused loud report from the developer, naming no role: the plugin crashes")
	if problem < 0 || whole < problem || message < whole {
		t.Fatalf("closing lines = %q", said)
	}
}
