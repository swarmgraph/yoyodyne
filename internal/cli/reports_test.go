package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The pile an operator was told about is readable without opening a
// conversation. That is the whole point of the verb: a run that finishes
// overnight says it reported something, and reading it must not cost an
// interactive conversation with a provider behind it.
func TestReportsReadsTheSamePileTheConversationShows(t *testing.T) {
	// Not parallel: the state root the command addresses is set here, and the
	// pile it reads is written under it.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	// Nothing reported yet is an answer, and it says where the pile would be so
	// an operator can tell it from reading the wrong product's store.
	stdout, stderr, code := runCLI(t, "reports", "--config", configPath)
	if code != 0 {
		t.Fatalf("reports code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "nothing has been reported") || !strings.Contains(stdout, "reports.jsonl") {
		t.Fatalf("stdout = %q", stdout)
	}

	store, err := runstate.NewReportStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewReportStore() error = %v", err)
	}
	if err := store.Append(report.Report{
		SchemaVersion: report.SchemaVersion,
		ID:            "report-0123456789abcdef0123456789abcdef",
		Role:          "developer",
		Agent:         "developer",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		WorkItemID:    "yoyodyne-ifd.70",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Severity:      report.SeverityCritical,
		Message:       "bd lint could not run in its sandbox, so nothing linted the item",
		RecordedAt:    time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	stdout, stderr, code = runCLI(t, "reports", "--config", configPath)
	if code != 0 {
		t.Fatalf("reports code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"1 of 1 collected report(s) are unhandled",
		"the oldest filed",
		"critical",
		"from the developer on title unavailable (yoyodyne-ifd.70)",
		"bd lint could not run",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}

	// A script reads the same pile, with the attribution that makes it
	// triageable rather than the rendering that makes it readable.
	stdout, stderr, code = runCLI(t, "reports", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("reports --json code = %d, stderr = %q", code, stderr)
	}
	var decoded reportsOutput
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if len(decoded.Reports) != 1 {
		t.Fatalf("reports = %#v", decoded.Reports)
	}
	collected := decoded.Reports[0]
	if collected.Role != "developer" || collected.WorkItemID != "yoyodyne-ifd.70" || collected.Severity != report.SeverityCritical {
		t.Fatalf("collected = %#v", collected)
	}
	if len(decoded.Handlings) != 0 {
		t.Fatalf("handlings = %#v, want none before anybody has decided", decoded.Handlings)
	}

	// Once the product manager has decided what becomes of it, the listing says
	// so. A report somebody dealt with and one nobody has read look identical
	// otherwise, and the second is the only one that still needs anybody.
	if err := store.Handle(report.Handling{
		SchemaVersion: report.HandlingSchemaVersion,
		ReportID:      "report-0123456789abcdef0123456789abcdef",
		Role:          "product-manager",
		Agent:         "product-manager",
		RunID:         "chat-0123456789abcdef0123456789abcdef",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Reason:        "admitted as yoyodyne-ifd.150",
		RecordedAt:    time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	stdout, stderr, code = runCLI(t, "reports", "--config", configPath)
	if code != 0 {
		t.Fatalf("reports code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{"nobody is waiting on any of the 1 collected report(s)", "handled", "admitted as title unavailable (yoyodyne-ifd.150)"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
	stdout, stderr, code = runCLI(t, "reports", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("reports --json code = %d, stderr = %q", code, stderr)
	}
	decoded = reportsOutput{}
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if len(decoded.Handlings) != 1 || decoded.Handlings[0].ReportID != "report-0123456789abcdef0123456789abcdef" {
		t.Fatalf("handlings = %#v", decoded.Handlings)
	}
}

// The pile is printed oldest first, which is the surface where a critical report
// is furthest from the reader's eye: it can be under a hundred notes. So the
// severity is marked at the margin rather than only stated mid-line, and the
// mark is what survives everything — this listing is a buffer rather than a
// terminal, so nothing here is coloured at all and the distinction still holds.
func TestTheReportsListingMarksWhatIsCriticalAndLeavesTheJSONAlone(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	store, err := runstate.NewReportStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewReportStore() error = %v", err)
	}
	for index, filed := range []struct {
		id       string
		severity report.Severity
	}{
		{"report-0123456789abcdef0123456789abcde1", report.SeverityNote},
		{"report-0123456789abcdef0123456789abcde2", report.SeverityCritical},
	} {
		if err := store.Append(report.Report{
			SchemaVersion: report.SchemaVersion,
			ID:            filed.id,
			Role:          "developer",
			Agent:         "developer",
			RunID:         "run-0123456789abcdef0123456789abcdef",
			WorkItemID:    "yoyodyne-ifd.66",
			ProductID:     "yoyodyne",
			RepositoryID:  "yoyodyne",
			Severity:      filed.severity,
			Message:       "something the developer noticed",
			RecordedAt:    time.Date(2026, 8, 18, 9, index, 0, 0, time.UTC),
		}); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	stdout, stderr, code := runCLI(t, "reports", "--config", configPath)
	if code != 0 {
		t.Fatalf("reports code = %d, stderr = %q", code, stderr)
	}
	var marked, unmarked int
	for _, line := range strings.Split(stdout, "\n") {
		switch {
		case strings.HasPrefix(line, "  !! report-"):
			marked++
		case strings.HasPrefix(line, "     report-"):
			unmarked++
		}
	}
	if marked != 1 || unmarked != 1 {
		t.Fatalf("%d marked and %d unmarked report line(s) in %q", marked, unmarked, stdout)
	}

	// None of it reaches the document a script reads: the severity is a recorded
	// field there and the marker is a way of showing it to a person.
	stdout, stderr, code = runCLI(t, "reports", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("reports --json code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(stdout, "!!") {
		t.Fatalf("the marker reached --json: %q", stdout)
	}
	var decoded reportsOutput
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if len(decoded.Reports) != 2 || decoded.Reports[1].Severity != report.SeverityCritical {
		t.Fatalf("reports = %#v", decoded.Reports)
	}
}

// The verb reads the pile without building the repository-facing components, so
// that the two still address one store is asserted rather than assumed. A
// reports verb pointed at a different root would answer "nothing has been
// reported" against a pile that is not empty, which is the one failure this
// verb cannot afford: it is the surface an operator checks precisely when they
// were told there was something to read.
func TestReportsReadsTheSameStoreTheRunComponentsAppendTo(t *testing.T) {
	// Not parallel: the state root both paths resolve is set here.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig)

	parts, err := buildComponents(configPath)
	if err != nil {
		t.Fatalf("buildComponents() error = %v", err)
	}
	store, err := reportStore(configPath)
	if err != nil {
		t.Fatalf("reportStore() error = %v", err)
	}
	if store.Path() != parts.reports.Path() {
		t.Fatalf("reports read %s, but runs append to %s", store.Path(), parts.reports.Path())
	}
}

func TestReportsRefusesArgumentsItCannotHonor(t *testing.T) {
	t.Parallel()

	_, stderr, code := runCLI(t, "reports", "yoyodyne-ifd.70")
	if code != 2 {
		t.Fatalf("code = %d, want 2; stderr = %q", code, stderr)
	}
	if !strings.Contains(stderr, "does not accept positional arguments") {
		t.Fatalf("stderr = %q", stderr)
	}
}

// A report is a claim about the build that filed it, so the listing says how far
// that build is behind the target branch — counted in the product's repository
// against a real history, the way the channel counts a watch session's build.
// A report from before reports carried a build says so rather than reading as
// current, and a build the repository never held is named as uncounted.
func TestTheReportsListingSaysHowFarEachReportsBuildIsBehind(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)

	project := t.TempDir()
	git(t, project, "init", "-b", "main")
	git(t, project, "config", "user.name", "Yoyodyne Test")
	git(t, project, "config", "user.email", "yoyodyne@example.invalid")
	commit(t, project, "first")
	filedFrom := strings.TrimSpace(gitOutput(t, project, "rev-parse", "HEAD"))
	commit(t, project, "the fix")
	commit(t, project, "another")
	configPath := filepath.Join(project, config.DirectoryName, config.FileName)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(configPath, []byte(validConfig), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	store, err := runstate.NewReportStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewReportStore() error = %v", err)
	}
	for i, build := range []string{filedFrom, "", "fedcba9876543210fedcba9876543210fedcba98"} {
		if err := store.Append(report.Report{
			SchemaVersion: report.SchemaVersion,
			ID:            fmt.Sprintf("report-0123456789abcdef0123456789abcde%d", i),
			Role:          "developer",
			RunID:         fmt.Sprintf("run-0123456789abcdef0123456789abcde%d", i),
			Build:         build,
			ProductID:     "yoyodyne",
			RepositoryID:  "yoyodyne",
			Severity:      report.SeverityNote,
			Message:       "the invariants index is reported as unreadable",
			RecordedAt:    time.Date(2026, 9, 22, 9, i, 0, 0, time.UTC),
		}); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	stdout, stderr, code := runCLI(t, "reports", "--config", configPath)
	if code != 0 {
		t.Fatalf("reports code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"(run-0123456789abcdef0123456789abcde0, build " + filedFrom[:12] + ", 2 change(s) behind the target branch)",
		"(run-0123456789abcdef0123456789abcde1, no build recorded)",
		"(run-0123456789abcdef0123456789abcde2, build fedcba987654, not counted against the target branch)",
		"1 build(s) could not be counted against the target branch",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout is missing %q:\n%s", want, stdout)
		}
	}

	stdout, stderr, code = runCLI(t, "reports", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("reports --json code = %d, stderr = %q", code, stderr)
	}
	var decoded reportsOutput
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if decoded.Reports[0].Build != filedFrom || decoded.Builds[filedFrom].Behind != 2 {
		t.Fatalf("reports = %#v, builds = %#v, want the build and its count carried as data", decoded.Reports[0], decoded.Builds)
	}
	if decoded.Builds["fedcba9876543210fedcba9876543210fedcba98"].Problem == "" {
		t.Fatalf("builds = %#v, want the unheld build to say why it was not counted", decoded.Builds)
	}
}

func gitOutput(t *testing.T, repository string, args ...string) string {
	t.Helper()

	output, err := exec.Command("git", append([]string{"-C", repository}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v error = %v", args, err)
	}
	return string(output)
}
