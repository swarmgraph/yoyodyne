package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A project whose only recurring pass is the development manager's.
const configWithManagerSweep = validConfig + `  manager:
    role: development-manager
    backend: claude-code
    model: opus
recurring_tasks:
  manager-sweep:
    role: development-manager
    every: 1h
    enabled: true
    prompt: Sweep what waits on you.
`

// yoyo status prints the reports line from the project's own pile and
// configuration, says which addressees no pass reads, and carries the figures
// in --json; yoyo reports opens with the same total.
func TestStatusPrintsTheUnhandledReportsByAddresseeFromTheProjectsPile(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, configWithManagerSweep)

	stdout, stderr, code := runCLI(t, "status", "--config", configPath)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(stdout, "reports: ") {
		t.Fatalf("status printed a reports line with nothing filed:\n%s", stdout)
	}

	store, err := runstate.NewReportStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	filedAt := time.Now().Add(-time.Hour).UTC()
	for i, role := range []domain.AgentRole{domain.RoleDevelopmentManager, ""} {
		if err := store.Append(report.Report{
			SchemaVersion: report.SchemaVersion,
			ID:            []string{"report-00000000000000000000000000000e01", "report-00000000000000000000000000000e02"}[i],
			Role:          domain.RoleDeveloper,
			RunID:         "run-0123456789abcdef0123456789abcdef",
			ProductID:     "yoyodyne",
			RepositoryID:  "yoyodyne",
			Severity:      report.SeverityWarning,
			Message:       "a test server was left running",
			For:           role,
			RecordedAt:    filedAt.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}

	stdout, stderr, code = runCLI(t, "status", "--config", configPath)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	want := "reports: 2 unhandled (development manager 1, no role named 1); no pass reads those naming no role; read them with yoyo reports\n"
	if !strings.Contains(stdout, want) {
		t.Fatalf("status does not print %q:\n%s", want, stdout)
	}

	stdout, stderr, code = runCLI(t, "reports", "--config", configPath)
	if code != 0 {
		t.Fatalf("reports code = %d, stderr = %q", code, stderr)
	}
	if !strings.HasPrefix(stdout, "reports: 2 of 2 collected report(s) are unhandled") {
		t.Fatalf("reports opens with %q", strings.SplitN(stdout, "\n", 2)[0])
	}

	stdout, stderr, code = runCLI(t, "status", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("status --json code = %d, stderr = %q", code, stderr)
	}
	var decoded struct {
		Standing struct {
			Reports report.Pile `json:"reports"`
		} `json:"standing"`
	}
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatal(err)
	}
	got := decoded.Standing.Reports.Addressees
	if len(got) != 2 || got[0].Role != domain.RoleDevelopmentManager || got[0].Unhandled != 1 || !got[0].Read ||
		got[1].Role != "" || got[1].Unhandled != 1 || got[1].Read {
		t.Fatalf("addressees = %+v", got)
	}
}
