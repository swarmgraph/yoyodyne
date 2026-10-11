package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// A run's developer is invoked on the backend the run recorded, not on the one
// the developer slot is configured for now (recordedbackend.go).

const testCodexModel = "gpt-6.1-sol"

// codexStoppedRun runs the item with the developer configured for Codex until
// its repair budget is spent on the reviewer's findings, and returns the
// stopped run's record, the store it is in, and the fixture it ran in.
func codexStoppedRun(t *testing.T) (repository, worktreeRoot string, store *runstate.Store, tracker *orchestratortest.Tracker, stopped runstate.State) {
	t.Helper()
	repository, worktreeRoot, store = restartableFixture(t)
	tracker = &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	codex := codexLike(orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("incomplete\n"), 0o600)
	}, repairVerdict))
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, codex, []string{"test -f feature.txt"}), codex)
	pipeline.Config.Agents["developer"] = codexDeveloper()
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 0
	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), "independent review requires repair") {
		t.Fatalf("Run() error = %v, want the repair budget spent", err)
	}
	stopped, err = store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stopped.Backend != domain.BackendCodex || stopped.ProviderSessionID != codex.DeveloperSession {
		t.Fatalf("stopped run = backend %q session %q, want the Codex developer's session recorded", stopped.Backend, stopped.ProviderSessionID)
	}
	return repository, worktreeRoot, store, tracker, stopped
}

// carryOutRepair grants a repair of the stopped run and carries it out through
// the repair continuer, continuing the run in the pipeline continuing builds.
func carryOutRepair(t *testing.T, repository, worktreeRoot string, store *runstate.Store, tracker *orchestratortest.Tracker, stopped runstate.State, backends DeveloperBackends, continuing func() Pipeline) (RepairContinueResult, error) {
	t.Helper()
	docket := &memoryDocket{}
	if _, err := docketerOverStore(docket, store, continuing().Config).RecordStoppedRun(stopped); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	worktrees, err := gitworktree.New(gitworktree.Options{Runner: execution.OSProcessRunner{}, RepositoryRoot: repository, WorktreeRoot: worktreeRoot, Timeout: testGitBudget})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	cfg := continuing().Config
	if _, err := store.Triage().GrantRepair(context.Background(), tracker.Item.ID, triageDecided(runstate.TriageDecisionRepair, stopped.RunID),
		TriageRepairGrantRounds(cfg.Triage), time.Now(), TriageCaps(cfg.Execution, cfg.Triage)); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewIntakeHoldStore() error = %v", err)
	}
	continuer := RepairContinuer{
		Docket: docket, Runs: store, Intake: intake, Decisions: store.Triage(), Items: tracker, Worktrees: worktrees,
		ConfiguredAttempts: 0, Capacity: 1, Backends: backends, Events: store,
		Start: func(ctx context.Context, workItemID, runID string) (Outcome, error) {
			return continuing().Continue(ctx, workItemID, runID)
		},
	}
	return continuer.Continue(context.Background(), RepairContinueRequest{Run: stopped.RunID})
}

// codexLike makes a fake provider answer the way the Codex adapter does: it
// never reports the model it served, and it refuses to launch a developer asked
// for no model at all, which is how the real adapter refuses one.
func codexLike(provider *orchestratortest.Backend) *orchestratortest.Backend {
	respond := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleDeveloper && strings.TrimSpace(request.Model) == "" {
			return backend.RunResult{}, fmt.Errorf("Codex model %q does not accept effort %q; accepted values: no effort level", request.Model, request.Effort)
		}
		result, err := respond(request)
		result.ResolvedModel = ""
		return result, err
	}
	return provider
}

func codexDeveloper() config.AgentConfig {
	return config.AgentConfig{Role: domain.RoleDeveloper, Backend: domain.BackendCodex, Model: testCodexModel, Instances: 1}
}

// codexOnly is a harness that can still start Codex beside the configured
// Claude Code developer.
func codexOnly(codex backend.Backend) func(domain.Backend) (backend.Backend, bool) {
	return func(named domain.Backend) (backend.Backend, bool) {
		if named == domain.BackendCodex {
			return codex, true
		}
		return nil, false
	}
}

func TestARepairOfACodexRunResumesUnderCodexAfterTheDeveloperMovedToClaudeCode(t *testing.T) {
	t.Parallel()

	for _, erased := range []bool{false, true} {
		name := "session on the record"
		if erased {
			name = "session erased by an earlier failed resume"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repository, worktreeRoot, store, tracker, stopped := codexStoppedRun(t)
			original := stopped.ProviderSessionID
			if erased {
				// What a failed resume left before it stopped overwriting the record:
				// no session on it, and the one Codex opened still in the event log.
				appendRunStarted(t, store, stopped.RunID, stopped.LastSequence+1, "codex", original)
				stopped.ProviderSessionID = ""
				if err := store.Save(stopped); err != nil {
					t.Fatalf("Save() error = %v", err)
				}
			}
			implement := func(request backend.RunRequest) error {
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
			}
			// The developer slot is now configured for Claude Code. Its adapter
			// reviews; Codex is still on the machine.
			claude := orchestratortest.RoleBackend(implement, approveVerdict)
			codex := codexLike(orchestratortest.RoleBackend(implement, approveVerdict))
			continuing := func() Pipeline {
				continued := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, claude, []string{"test -f feature.txt"}), claude)
				continued.RecordedBackends = codexOnly(codex)
				continued.Config.Execution.RepairAttemptsBeforeReplan = 0
				return continued
			}
			result, err := carryOutRepair(t, repository, worktreeRoot, store, tracker, stopped,
				DeveloperBackends{Configured: domain.BackendClaudeCode, Other: codexOnly(codex)}, continuing)
			if err != nil {
				t.Fatalf("Continue() error = %v", err)
			}
			if !result.Continued || result.FreshSession != "" {
				t.Fatalf("result = %#v, want the run's own session re-entered", result)
			}
			if erased && (result.RestoredSession != original || !strings.Contains(result.Reason, "restored from the run's event log")) {
				t.Fatalf("restored = %q, reason = %q; want the erased session %q restored and said so", result.RestoredSession, result.Reason, original)
			}
			if requests := claude.RequestsForRole(domain.RoleDeveloper); len(requests) != 0 {
				t.Fatalf("Claude Code was asked to develop %d time(s), want never: %#v", len(requests), requests)
			}
			requests := codex.RequestsForRole(domain.RoleDeveloper)
			if len(requests) != 1 || requests[0].SessionID != original {
				t.Fatalf("Codex developer requests = %#v, want one resuming session %q", requests, original)
			}
			// Codex reported no model for any attempt, so the one asked for is the
			// one the run's record says it was developed with.
			if requests[0].Model != testCodexModel {
				t.Fatalf("Codex was asked for model %q, want %q, the one the run was developed with", requests[0].Model, testCodexModel)
			}
			if result.Outcome.Integration == nil || !tracker.Closed {
				t.Fatalf("the continued run did not land its change: %#v, closed = %t", result.Outcome.Integration, tracker.Closed)
			}
			landed, err := store.Load(stopped.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if landed.Backend != domain.BackendCodex || landed.ProviderSessionID != original {
				t.Fatalf("run = backend %q session %q, want the Codex session kept", landed.Backend, landed.ProviderSessionID)
			}
			if landed.ProviderModel != testCodexModel || landed.ProviderResolvedModel != "" || landed.ProviderModelReported {
				t.Fatalf("run = model %q resolved %q reported %t; want %q kept and no reported model recorded",
					landed.ProviderModel, landed.ProviderResolvedModel, landed.ProviderModelReported, testCodexModel)
			}
		})
	}
}

// A run whose recorded backend this harness can no longer start is refused
// before the grant is spent, with its session left on its record, and the
// refusal is on the item and the docket for the development manager.
func TestARepairOnABackendThatCannotBeStartedIsRefusedBeforeAnythingIsSpent(t *testing.T) {
	t.Parallel()

	state := continuableState()
	state.Backend = domain.BackendCodex
	harness := newContinueHarness(t, state)
	continuer := harness.continuer()
	continuer.Backends = DeveloperBackends{Configured: domain.BackendClaudeCode}
	repairer := &countedRepair{RepairContinuer: continuer}
	watch := harness.carryOut()
	watch.Repairer = repairer
	watch.Notes = harness.tracker
	watch.Clock = laterClock{after: time.Minute}

	carried, _, err := watch.Carry(context.Background(), theOneOutstanding(t, watch))
	if err != nil || carried.Carried || carried.Cause != triage.CarryOutBackendUnavailable || carried.RecordProblem != "" {
		t.Fatalf("Carry() = %+v, %v; want the repair refused for the backend", carried, err)
	}
	if len(harness.started) != 0 || harness.carried(t) != 0 {
		t.Fatalf("started = %#v, carried = %d; want nothing dispatched and nothing spent", harness.started, harness.carried(t))
	}
	if reloaded := harness.reload(t); reloaded.ProviderSessionID != "developer-session" || reloaded.Backend != domain.BackendCodex {
		t.Fatalf("run = session %q backend %q; want the record untouched", reloaded.ProviderSessionID, reloaded.Backend)
	}
	for _, want := range []string{"codex", "claude-code", "re-run", "no provider was called", "spent nothing"} {
		if !strings.Contains(harness.tracker.Notes, want) {
			t.Fatalf("item note %q lacks %q", harness.tracker.Notes, want)
		}
	}
	entries, _ := harness.docket.List()
	docket := Docketer{Decisions: harness.runs.Triage(), Reruns: harness.runs.Reruns()}
	if problems := docket.joinDecisions(entries, docketedRunsOf(entries), nil, nil); len(problems) != 0 {
		t.Fatal(problems)
	}
	if len(entries) != 1 || entries[0].CarryOut == nil || entries[0].CarryOut.Cause != triage.CarryOutBackendUnavailable {
		t.Fatalf("entries = %+v; want the refusal on her docket", entries)
	}
	if !strings.Contains(entries[0].CarryOut.Clears, "re-run") {
		t.Fatalf("clears = %q, want it to name the re-run", entries[0].CarryOut.Clears)
	}
	// A later pull does not try it again.
	watch.Clock = laterClock{after: 24 * time.Hour}
	if outstanding, err := watch.Outstanding(); err != nil || len(outstanding) != 0 || repairer.attempts != 1 {
		t.Fatalf("Outstanding() = %+v, %v, attempts %d; want no second attempt", outstanding, err, repairer.attempts)
	}
}

// The backend the developer is configured for, or one the harness can still
// start, is no reason to refuse.
func TestARepairOnALaunchableBackendIsNotRefused(t *testing.T) {
	t.Parallel()

	for _, backends := range []DeveloperBackends{
		{Configured: domain.BackendClaudeCode},
		{Configured: domain.BackendCodex, Other: codexOnly(nil)},
	} {
		state := continuableState()
		if backends.Configured == domain.BackendCodex {
			state.Backend = domain.BackendCodex
			state.ProviderModel = testCodexModel
		}
		harness := newContinueHarness(t, state)
		continuer := harness.continuer()
		continuer.Backends = backends
		if result, err := continuer.Continue(context.Background(), continueRequest()); err != nil || !result.Continued {
			t.Fatalf("Continue() = %#v, %v; want the repair carried out", result, err)
		}
	}
	state := continuableState()
	state.Backend = domain.BackendCodex
	state.ProviderModel = testCodexModel
	harness := newContinueHarness(t, state)
	continuer := harness.continuer()
	continuer.Backends = DeveloperBackends{Configured: domain.BackendClaudeCode, Other: codexOnly(nil)}
	if result, err := continuer.Continue(context.Background(), continueRequest()); err != nil || !result.Continued {
		t.Fatalf("Continue() = %#v, %v; want a Codex run carried out where Codex can still start", result, err)
	}
}

// Where the continuer was not asked, the pipeline still refuses before any
// provider call, and the session stays on the record.
func TestThePipelineRefusesARecordedBackendItCannotStartBeforeAnyProviderCall(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store, tracker, stopped := codexStoppedRun(t)
	claude := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	continuing := func() Pipeline {
		continued := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, claude, []string{"test -f feature.txt"}), claude)
		continued.Config.Execution.RepairAttemptsBeforeReplan = 0
		return continued
	}
	_, err := carryOutRepair(t, repository, worktreeRoot, store, tracker, stopped, DeveloperBackends{}, continuing)
	if !errors.Is(err, ErrRecordedBackendUnavailable) {
		t.Fatalf("Continue() error = %v, want the recorded backend refused", err)
	}
	if len(claude.Requests) != 0 {
		t.Fatalf("provider requests = %#v, want none", claude.Requests)
	}
	after, err := store.Load(stopped.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if after.ProviderSessionID != stopped.ProviderSessionID {
		t.Fatalf("session = %q, want %q kept", after.ProviderSessionID, stopped.ProviderSessionID)
	}
}

// A resume the provider fails before opening a session — the failure every
// mismatched resume met — leaves the run's session on its record.
func TestAFailedResumeNeverErasesTheRecordedSession(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store, tracker, stopped := codexStoppedRun(t)
	codex := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	respond := codex.Respond
	codex.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role != domain.RoleDeveloper {
			return respond(request)
		}
		return backend.RunResult{
			IsError:   true,
			FinalText: "No conversation found with session ID: " + request.SessionID,
			Process:   execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1},
			LastEvent: request.LastSequence,
		}, nil
	}
	continuing := func() Pipeline {
		continued := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, codex, []string{"test -f feature.txt"}), codex)
		continued.Config.Agents["developer"] = codexDeveloper()
		continued.Config.Execution.RepairAttemptsBeforeReplan = 0
		return continued
	}
	if _, err := carryOutRepair(t, repository, worktreeRoot, store, tracker, stopped, DeveloperBackends{Configured: domain.BackendCodex}, continuing); err == nil {
		t.Fatal("the repair succeeded, want the failed resume to end the run")
	}
	if requests := codex.RequestsForRole(domain.RoleDeveloper); len(requests) == 0 || requests[0].SessionID != stopped.ProviderSessionID {
		t.Fatalf("developer requests = %#v, want the recorded session resumed", requests)
	}
	after, err := store.Load(stopped.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if after.ProviderSessionID != stopped.ProviderSessionID {
		t.Fatalf("session after the failed resume = %q, want %q kept", after.ProviderSessionID, stopped.ProviderSessionID)
	}
}

func TestErasedSessionIsTheLatestDeveloperSessionOnTheRecordedBackend(t *testing.T) {
	t.Parallel()

	state := runstate.State{RunID: docketedRunID, Backend: domain.BackendCodex}
	sequence := uint64(0)
	event := func(eventType execution.EventType, source string, payload any) execution.Event {
		sequence++
		built, err := execution.NewEvent(docketedRunID, sequence, time.Now(), eventType, source, payload)
		if err != nil {
			t.Fatalf("NewEvent() error = %v", err)
		}
		return built
	}
	events := []execution.Event{
		event(execution.EventRunStarted, "codex", map[string]any{"session_id": "codex-developer-1"}),
		event(execution.EventRunCompleted, "codex", map[string]any{"role": "developer"}),
		// A review whose terminal named no role, read by its window.
		event(execution.EventReviewStarted, "harness.review", nil),
		event(execution.EventRunStarted, "codex", map[string]any{"session_id": "codex-reviewer-1"}),
		event(execution.EventRunCompleted, "codex", nil),
		event(execution.EventReviewCompleted, "harness.review", nil),
		event(execution.EventRunStarted, "codex", map[string]any{"session_id": "codex-developer-2"}),
		event(execution.EventRunCompleted, "codex", map[string]any{"role": "developer"}),
		// A reviewer whose terminal names its role, outside any review window.
		event(execution.EventRunStarted, "codex", map[string]any{"session_id": "codex-reviewer-2"}),
		event(execution.EventRunFailed, "codex", map[string]any{"role": "reviewer"}),
		// The failed resume: Claude Code never opened a session.
		event(execution.EventProcessOutput, "claude-code", map[string]any{"stream": "stderr", "text": "No conversation found with session ID: codex-developer-2"}),
		// A session on another backend is never the run's.
		event(execution.EventRunStarted, "claude-code", map[string]any{"session_id": "claude-developer"}),
		event(execution.EventRunFailed, "claude-code", map[string]any{"role": "developer"}),
	}
	if got := erasedSession(state, events, domain.BackendCodex); got != "codex-developer-2" {
		t.Fatalf("erasedSession() = %q, want codex-developer-2", got)
	}
	state.ProviderSessionID = "on-the-record"
	if got := erasedSession(state, events, domain.BackendCodex); got != "" {
		t.Fatalf("erasedSession() = %q for a record that holds a session, want nothing", got)
	}
	if got := erasedSession(runstate.State{Backend: domain.BackendCodex}, events[10:11], domain.BackendCodex); got != "" {
		t.Fatalf("erasedSession() = %q from a log naming no session, want nothing", got)
	}
}

func appendRunStarted(t *testing.T, store *runstate.Store, runID string, sequence uint64, source, session string) {
	t.Helper()
	event, err := execution.NewEvent(runID, sequence, time.Now(), execution.EventRunStarted, source, map[string]any{"session_id": session})
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	if err := store.AppendEvent(event); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
}

// fixedModels is a spend log that names one model for every run, or none.
type fixedModels string

func (m fixedModels) LastDeveloperModel(string, domain.Backend) (string, error) {
	return string(m), nil
}

// failingLaunch makes every developer launch fail before the provider opens a
// session, the way a refused or rate-limited launch does.
func failingLaunch(provider *orchestratortest.Backend) *orchestratortest.Backend {
	respond := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleDeveloper {
			return backend.RunResult{}, errors.New("the provider refused the launch")
		}
		return respond(request)
	}
	return provider
}

// A launch that fails on Codex leaves the model the run was developed with on
// its record, so the next continuation can still ask for it.
func TestAFailedCodexLaunchLeavesTheRunsRecordedModel(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store, tracker, stopped := codexStoppedRun(t)
	if stopped.ProviderModel != testCodexModel || stopped.ProviderModelReported {
		t.Fatalf("stopped run = model %q reported %t; want %q asked for and nothing reported", stopped.ProviderModel, stopped.ProviderModelReported, testCodexModel)
	}
	claude := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	codex := failingLaunch(codexLike(orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)))
	continuing := func() Pipeline {
		continued := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, claude, []string{"test -f feature.txt"}), claude)
		continued.RecordedBackends = codexOnly(codex)
		continued.Config.Execution.RepairAttemptsBeforeReplan = 0
		return continued
	}
	if _, err := carryOutRepair(t, repository, worktreeRoot, store, tracker, stopped,
		DeveloperBackends{Configured: domain.BackendClaudeCode, Other: codexOnly(codex)}, continuing); err == nil {
		t.Fatal("the repair succeeded, want the failed launch to end the run")
	}
	if requests := codex.RequestsForRole(domain.RoleDeveloper); len(requests) == 0 || requests[0].Model != testCodexModel {
		t.Fatalf("Codex developer requests = %#v, want the launch asked for %q", requests, testCodexModel)
	}
	after, err := store.Load(stopped.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if after.ProviderModel != testCodexModel || after.ProviderSessionID != stopped.ProviderSessionID {
		t.Fatalf("run after the failed launch = model %q session %q; want %q and %q kept",
			after.ProviderModel, after.ProviderSessionID, testCodexModel, stopped.ProviderSessionID)
	}
}

// A Claude Code run continued on Claude Code is unchanged: it asks for the
// developer's configured model, and records what Claude Code reported serving.
// Where that launch fails, the model the run was last served on stays on its
// record rather than the one nothing ran on.
func TestAClaudeCodeContinuationAsksForTheConfiguredModel(t *testing.T) {
	t.Parallel()

	const reconfigured = "sonnet"
	for _, fails := range []bool{false, true} {
		name := "launch served"
		if fails {
			name = "launch failed"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repository, worktreeRoot, store := restartableFixture(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			first := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("incomplete\n"), 0o600)
			}, repairVerdict)
			pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"test -f feature.txt"}), first)
			pipeline.Config.Execution.RepairAttemptsBeforeReplan = 0
			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			if err == nil {
				t.Fatal("Run() succeeded, want the repair budget spent")
			}
			stopped, err := store.Load(outcome.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if stopped.Backend != domain.BackendClaudeCode || stopped.ProviderModel != testDeveloperModel || !stopped.ProviderModelReported {
				t.Fatalf("stopped run = backend %q model %q reported %t; want Claude Code on %q with its reported model recorded",
					stopped.Backend, stopped.ProviderModel, stopped.ProviderModelReported, testDeveloperModel)
			}
			claude := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
			}, approveVerdict)
			if fails {
				claude = failingLaunch(claude)
			}
			continuing := func() Pipeline {
				continued := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, claude, []string{"test -f feature.txt"}), claude)
				developer := continued.Config.Agents["developer"]
				developer.Model = reconfigured
				continued.Config.Agents["developer"] = developer
				continued.Config.Execution.RepairAttemptsBeforeReplan = 0
				return continued
			}
			_, err = carryOutRepair(t, repository, worktreeRoot, store, tracker, stopped, DeveloperBackends{Configured: domain.BackendClaudeCode}, continuing)
			if fails != (err != nil) {
				t.Fatalf("Continue() error = %v, want failure %t", err, fails)
			}
			if requests := claude.RequestsForRole(domain.RoleDeveloper); len(requests) != 1 || requests[0].Model != reconfigured {
				t.Fatalf("Claude Code developer requests = %#v, want one asking for the configured %q", requests, reconfigured)
			}
			after, err := store.Load(stopped.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			want, reported := reconfigured, true
			if fails {
				want = testDeveloperModel
			}
			if after.ProviderModel != want || after.ProviderModelReported != reported {
				t.Fatalf("run = model %q reported %t; want %q, reported %t", after.ProviderModel, after.ProviderModelReported, want, reported)
			}
		})
	}
}

// A run whose record lost its model to an earlier failed launch gets it back
// from the spend log before the repair is carried out, and one nothing names is
// refused before the grant is spent rather than launched with no model.
func TestACodexRunWhoseRecordLostItsModel(t *testing.T) {
	t.Parallel()

	for _, logged := range []string{testCodexModel, ""} {
		name := "spend log names it"
		if logged == "" {
			name = "nothing names it"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repository, worktreeRoot, store, tracker, stopped := codexStoppedRun(t)
			// What a launch refused for an empty model left before this was fixed.
			stopped.ProviderModel, stopped.ProviderResolvedModel = "", ""
			if err := store.Save(stopped); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			implement := func(request backend.RunRequest) error {
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
			}
			claude := orchestratortest.RoleBackend(implement, approveVerdict)
			codex := codexLike(orchestratortest.RoleBackend(implement, approveVerdict))
			continuing := func() Pipeline {
				continued := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, claude, []string{"test -f feature.txt"}), claude)
				continued.RecordedBackends = codexOnly(codex)
				continued.Config.Execution.RepairAttemptsBeforeReplan = 0
				return continued
			}
			result, err := carryOutRepair(t, repository, worktreeRoot, store, tracker, stopped,
				DeveloperBackends{Configured: domain.BackendClaudeCode, Other: codexOnly(codex), Models: fixedModels(logged)}, continuing)
			after, loadErr := store.Load(stopped.RunID)
			if loadErr != nil {
				t.Fatalf("Load() error = %v", loadErr)
			}
			if logged == "" {
				var refused RecordedModelError
				if !errors.As(err, &refused) || !errors.Is(err, ErrRecordedBackendUnavailable) || carryOutCause(err) != triage.CarryOutBackendUnavailable {
					t.Fatalf("Continue() error = %v, want the run refused for naming no model", err)
				}
				if len(codex.Requests) != 0 || len(claude.Requests) != 0 {
					t.Fatalf("provider requests = %#v / %#v, want none", codex.Requests, claude.Requests)
				}
				if after.ProviderSessionID != stopped.ProviderSessionID {
					t.Fatalf("session = %q, want %q kept", after.ProviderSessionID, stopped.ProviderSessionID)
				}
				return
			}
			if err != nil {
				t.Fatalf("Continue() error = %v", err)
			}
			if result.RestoredModel != testCodexModel || !strings.Contains(result.Reason, "restored from the run's own lines in the spend log") {
				t.Fatalf("restored = %q, reason = %q; want %q restored and said so", result.RestoredModel, result.Reason, testCodexModel)
			}
			if requests := codex.RequestsForRole(domain.RoleDeveloper); len(requests) != 1 || requests[0].Model != testCodexModel {
				t.Fatalf("Codex developer requests = %#v, want one asking for %q", requests, testCodexModel)
			}
			if after.ProviderModel != testCodexModel {
				t.Fatalf("run model = %q, want %q", after.ProviderModel, testCodexModel)
			}
		})
	}
}

// Where the continuer was not asked, the pipeline still refuses a run on
// another backend that names no model, before any provider call.
func TestThePipelineRefusesARunNamingNoModelBeforeAnyProviderCall(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store, tracker, stopped := codexStoppedRun(t)
	stopped.ProviderModel, stopped.ProviderResolvedModel = "", ""
	if err := store.Save(stopped); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	claude := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	codex := codexLike(orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict))
	continuing := func() Pipeline {
		continued := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, claude, []string{"test -f feature.txt"}), claude)
		continued.RecordedBackends = codexOnly(codex)
		continued.Config.Execution.RepairAttemptsBeforeReplan = 0
		return continued
	}
	_, err := carryOutRepair(t, repository, worktreeRoot, store, tracker, stopped, DeveloperBackends{}, continuing)
	var refused RecordedModelError
	if !errors.As(err, &refused) {
		t.Fatalf("Continue() error = %v, want the run refused for naming no model", err)
	}
	if len(codex.Requests) != 0 || len(claude.Requests) != 0 {
		t.Fatalf("provider requests = %#v / %#v, want none", codex.Requests, claude.Requests)
	}
}
