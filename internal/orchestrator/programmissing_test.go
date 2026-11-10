package orchestrator

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// missingProgram is what the adapter hands back for an invocation whose
// program was found nowhere: nothing started, the wait for the program beside
// the error that names it (backend.ProgramMissingOutage).
func missingProgram(request backend.RunRequest) (backend.RunResult, error) {
	cause := &backend.ExecutableError{Binary: "claude", Provider: domain.BackendClaudeCode, Path: "/usr/bin:/bin",
		Folders: []string{"/Users/someone/.local/bin", "/opt/homebrew/bin", "/usr/local/bin"}, Cause: exec.ErrNotFound}
	err := errors.Join(execution.ErrProcessNotStarted, cause)
	outage, _ := backend.ProgramMissingOutage(err)
	return backend.RunResult{Backend: domain.BackendClaudeCode, IsError: true, ProviderOutage: outage, LastEvent: request.LastSequence}, err
}

// A run under way whose provider's program goes missing at review waits for
// it and carries on by itself once it is back. It is recorded as the missing
// program, not as a sandbox that could not be entered, and the review round
// and every attempt are left unspent; one critical report says so.
func TestARunUnderWayWaitsForAMissingProgramAndCarriesOnWhenItIsBack(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := refusingBackend(0, nil, approveVerdict)
	served := provider.Respond
	missing := 0
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleReviewer && missing < 2 {
			missing++
			return missingProgram(request)
		}
		return served(request)
	}
	pipeline, store := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	clock := &pausingClock{now: baseTime}
	pipeline = waiting(automatic(pipeline, provider), clock, 10*time.Minute, 10*time.Minute)
	pipeline.Config.Execution.TransientRelaunchesBeforeBlocking = 0
	pipeline.Config.Execution.UsageLimitUnknownResetPause = config.Duration(5 * time.Minute)
	outages := newOutageStore(t)
	pipeline.ProviderOutages = outages
	reports := &fakeReports{}
	pipeline.Reports = reports

	var held []runstate.State
	var standing []runstate.ProviderOutage
	clock.onSleep = func() {
		loaded, err := store.Load(pipelineRunID)
		if err != nil {
			t.Errorf("Load() during the wait error = %v", err)
			return
		}
		held = append(held, loaded)
		if outage, away, err := outages.Standing(); err == nil && away {
			standing = append(standing, outage)
		}
	}
	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(held) == 0 || clock.waited() != 2*5*time.Minute {
		t.Fatalf("waited %s, want one probe interval for each review the missing program turned away", clock.waited())
	}
	for _, state := range held {
		if state.Phase != runstate.PhaseReviewing || state.PauseCause != runstate.PauseProviderProgramMissing ||
			state.ReviewRounds != 0 || state.TransientRelaunches != 0 || state.RepairAttempts != 0 || state.UsageLimitPausedSeconds != 0 {
			t.Fatalf("held state = phase %q, pause %q, rounds %d, relaunches %d, repairs %d; want the review waiting on the missing program with nothing spent",
				state.Phase, state.PauseCause, state.ReviewRounds, state.TransientRelaunches, state.RepairAttempts)
		}
		if state.Environmental != nil && state.Environmental.Cause == runstate.CauseSandboxSpawnFailure {
			t.Fatalf("the missing program was recorded as a sandbox that could not be entered: %+v", state.Environmental)
		}
	}
	if len(standing) == 0 || standing[0].Cause != domain.ProviderProgramMissing || standing[0].Provider != domain.BackendClaudeCode ||
		!strings.Contains(standing[0].Says(), "claude-code program cannot be found") || !strings.Contains(standing[0].Says(), "providers.claude-code.binary") {
		t.Fatalf("outage on the product = %+v, want the missing program named with the fix", standing)
	}
	if outcome.Integration == nil || !tracker.Closed || tracker.Blocked || outcome.RepairAttempts != 0 || outcome.TransientRelaunches != 0 {
		t.Fatalf("outcome = %#v (blocked=%t), want the run finished with nothing spent", outcome, tracker.Blocked)
	}
	if reviews := len(provider.RequestsForRole(domain.RoleReviewer)); reviews != 3 {
		t.Fatalf("review invocations = %d, want the two turned away and the one that judged the change", reviews)
	}
	if finished, err := store.Load(outcome.RunID); err != nil || finished.ReviewRounds != 1 {
		t.Fatalf("finished run = rounds %d, %v; want only the review that took place counted", finished.ReviewRounds, err)
	}
	if _, away, err := outages.Standing(); err != nil || away {
		t.Fatalf("Standing() after the program came back = %t, %v, want the outage cleared", away, err)
	}
	critical := 0
	for _, filed := range reports.appended {
		if filed.Severity == report.SeverityCritical && strings.Contains(filed.Message, "program cannot be found") {
			critical++
		}
	}
	if critical != 1 {
		t.Fatalf("filed %d critical report(s) about the missing program, want one for the outage however often it was met: %+v", critical, reports.appended)
	}
}

// A dispatch whose provider's program is found nowhere claims nothing and is
// reported as the wait it is, so the scheduler counts it toward nothing; the
// operator is told once however many dispatches meet it, and the next
// dispatch that finds the program clears it and runs.
func TestADispatchIntoAMissingProgramIsAWaitReportedOnce(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := &orchestratortest.Backend{ReportedAvailability: backend.Availability{
		ProgramMissing: true,
		Missing:        `"claude" was not found on PATH /usr/bin:/bin or in the usual install folders /opt/homebrew/bin`,
	}}
	pipeline, _ := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Reviewer = approvingReviewer()
	outages := newOutageStore(t)
	pipeline.ProviderOutages = outages
	reports := &fakeReports{}
	pipeline.Reports = reports

	for range 2 {
		_, err := pipeline.Run(context.Background(), tracker.Item.ID)
		var away ProviderOutageError
		if !errors.As(err, &away) || away.Cause != domain.ProviderProgramMissing || !strings.Contains(err.Error(), "was not found on PATH") {
			t.Fatalf("Run() error = %v, want the dispatch turned away as a wait for the missing program", err)
		}
	}
	if tracker.Claimed {
		t.Fatal("a dispatch into a missing program claimed the item")
	}
	standing, recorded, err := outages.Standing()
	if err != nil || !recorded || standing.Cause != domain.ProviderProgramMissing || standing.Refusals != 2 {
		t.Fatalf("Standing() = %+v, %t, %v, want the missing program recorded once and met twice", standing, recorded, err)
	}
	if len(reports.appended) != 1 || reports.appended[0].Severity != report.SeverityCritical {
		t.Fatalf("reports = %+v, want one critical report for the outage, not one per dispatch", reports.appended)
	}

	provider.ReportedAvailability = backend.Availability{Installed: true, Authenticated: true, Path: "/Users/someone/.local/bin/claude"}
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleDeveloper {
			if err := os.WriteFile(request.WorkingDirectory+"/feature.txt", []byte("implemented\n"), 0o600); err != nil {
				return backend.RunResult{}, err
			}
		}
		return backend.RunResult{Backend: domain.BackendClaudeCode, SessionID: "session", ResolvedModel: developerResolved,
			FinalText: "implemented the work item", LastEvent: request.LastSequence}, nil
	}
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err != nil {
		t.Fatalf("Run() once the program is back error = %v", err)
	}
	if _, recorded, err := outages.Standing(); err != nil || recorded {
		t.Fatalf("Standing() once the program is back = %t, %v, want it cleared", recorded, err)
	}
}

// programProbe stands in for the developer's provider as a watch asks it
// whether its program is there.
type programProbe struct {
	mu        sync.Mutex
	installed bool
	asked     int
}

func (p *programProbe) CheckAvailability(context.Context) (backend.Availability, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.asked++
	if !p.installed {
		return backend.Availability{ProgramMissing: true, Missing: "claude was not found"}, nil
	}
	return backend.Availability{Installed: true, Authenticated: true, Path: "/Users/someone/.local/bin/claude"}, nil
}

func (p *programProbe) install() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.installed = true
}

// While the program is missing the watch starts nothing, without counting it
// toward the brake; once the program is found again it resumes by itself and
// says so, with no restart and nothing released by hand.
func TestWatchingResumesByItselfWhenAMissingProgramIsBack(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	outages := newOutageStore(t)
	probe := &programProbe{}
	harness.outages, harness.provider = outages, probe
	if _, err := outages.Notice(runstate.ProviderOutageObservation{
		Cause: domain.ProviderProgramMissing, Provider: domain.BackendClaudeCode, Waiting: "the dispatch of yoyodyne-one",
	}); err != nil {
		t.Fatal(err)
	}
	sessions := &recordedSessions{}
	dispatched := 0
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		dispatched++
		return h.complete(id), nil
	}
	dispatchedWhileMissing := 0
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		switch sleeps {
		case 3:
			dispatchedWhileMissing = dispatched
			probe.install()
			return true
		case 6:
			return false
		default:
			return true
		}
	}
	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if dispatchedWhileMissing != 0 {
		t.Fatalf("dispatched %d run(s) while the program was missing, want none started", dispatchedWhileMissing)
	}
	if reason := sessions.said(runstate.WatchIdle); !strings.Contains(reason, "claude-code program cannot be found") {
		t.Fatalf("idle reason = %q, want the missing program named", reason)
	}
	if schedule.Braked != nil || schedule.BlockedInARow != 0 {
		t.Fatalf("schedule = braked %v, %d blocked in a row; want the wait counted toward nothing", schedule.Braked, schedule.BlockedInARow)
	}
	if _, away, err := outages.Standing(); err != nil || away {
		t.Fatalf("Standing() after the program came back = %t, %v, want it cleared by the watch", away, err)
	}
	resumed := strings.Join(schedule.FollowUpReleased, "\n")
	if !strings.Contains(resumed, "runs are being started again") || !strings.Contains(resumed, "/Users/someone/.local/bin/claude") ||
		!strings.Contains(schedule.Render(), "runs are being started again") {
		t.Fatalf("released = %q, want the session to say the program was found and where: %s", resumed, schedule.Render())
	}
	if dispatched != 1 {
		t.Fatalf("dispatched %d run(s), want the item started once the program was back: %s", dispatched, schedule.Render())
	}
}
