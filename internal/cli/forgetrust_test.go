package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/forgetrust"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

var cannotVerify = forgetrust.Unverifiable{Err: errors.New("x509: OSStatus -26276")}

// A supervisor detached from a shell that cannot verify certificates would
// hand that to every part, so `yoyo start` refuses and says where to start it.
func TestStartRefusesToDetachASupervisorThatCannotVerifyGitHub(t *testing.T) {
	t.Parallel()

	stateRoot := t.TempDir()
	store, err := runstate.NewSupervisionStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := config.LoadResolved(writeConfig(t, validConfig))
	if err != nil {
		t.Fatal(err)
	}
	launcher := &recordingLauncher{store: store}
	p := &product{
		resolved:  resolved,
		stateRoot: stateRoot,
		store:     store,
		program:   "/opt/yoyo/bin/yoyo",
		launcher:  launcher,
		now:       time.Now,
		verifies:  func() error { return cannotVerify },
	}
	var stdout, stderr strings.Builder
	if code := p.start(context.Background(), &stdout, &stderr, false); code != 1 {
		t.Fatalf("start code = %d, want 1; stdout %q", code, stdout.String())
	}
	if len(launcher.launched) != 0 {
		t.Fatalf("launched %d processes, want none", len(launcher.launched))
	}
	for _, want := range []string{"start refused", "x509: OSStatus -26276", "check GitHub for merged pull requests", "ordinary terminal", "yoyo setup"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("start said %q, want %q in it", stderr.String(), want)
		}
	}
}

// A supervisor that cannot verify GitHub's certificate files one report
// naming the check and the error, and not another while that one is open.
func TestASupervisorThatCannotVerifyGitHubReportsItOnce(t *testing.T) {
	t.Parallel()

	reports, err := runstate.NewReportStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 13, 16, 0, 0, 0, time.UTC)
	first, err := reportForgeTrust(reports, "yoyodyne", "repo", "d36aa8a087afb08ed18e380aa83c8c67ab9a3637", cannotVerify, now)
	if err != nil || first == "" {
		t.Fatalf("reportForgeTrust() = %q, %v; want a report filed", first, err)
	}
	again, err := reportForgeTrust(reports, "yoyodyne", "repo", "d36aa8a087afb08ed18e380aa83c8c67ab9a3637", cannotVerify, now.Add(time.Minute))
	if err != nil || again != "" {
		t.Fatalf("second reportForgeTrust() = %q, %v; want nothing filed while the first is open", again, err)
	}
	filed, err := reports.List()
	if err != nil || len(filed) != 1 {
		t.Fatalf("List() = %d reports, %v; want one", len(filed), err)
	}
	said := filed[0]
	if said.Severity != report.SeverityWarning || said.For != domain.RoleDevelopmentManager {
		t.Errorf("report severity %q for %q, want a warning for the development manager", said.Severity, said.For)
	}
	for _, want := range []string{"cannot verify GitHub's certificate", "x509: OSStatus -26276", "pull requests merged outside the merge queue", "ordinary terminal"} {
		if !strings.Contains(said.Message, want) {
			t.Errorf("report says %q, want %q in it", said.Message, want)
		}
	}

	// Once somebody has dealt with it, a supervisor that still cannot verify
	// says so again.
	if err := reports.Handle(report.Handling{SchemaVersion: report.HandlingSchemaVersion, ReportID: first, Role: domain.RoleProductManager, RunID: "chat-1", ProductID: "yoyodyne", RepositoryID: "repo", Reason: "restarted from a terminal", RecordedAt: now}); err != nil {
		t.Fatal(err)
	}
	later, err := reportForgeTrust(reports, "yoyodyne", "repo", "d36aa8a087afb08ed18e380aa83c8c67ab9a3637", cannotVerify, now.Add(time.Hour))
	if err != nil || later == "" {
		t.Fatalf("reportForgeTrust() after handling = %q, %v; want a fresh report", later, err)
	}
}
