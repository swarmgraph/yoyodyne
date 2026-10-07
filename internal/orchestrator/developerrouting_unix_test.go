//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package orchestrator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// These drive developer runs pinned to a developer slot's endpoint pair through
// the real pipeline, worktrees and run store, with one fake provider per
// backend. Each fake honours the launch gate the way the real adapters do — it
// registers its process before it does any work — so every attempt is
// reserved, registered, launched and ended on the run's record exactly as a
// real one is. The process it registers is one that has already exited, so the
// record reads its tree as stopped.

const (
	routedCodexModel  = "gpt-6.1-sol"
	routedCodexAlias  = "codex-account"
	routedClaudeAlias = "default"
)

// exitedProcessGroup is the identifier of a process that has already exited,
// which no process group has, so an execution registered under it reads as
// stopped once its hold is let go.
func exitedProcessGroup(t *testing.T) int {
	t.Helper()
	process := exec.Command("true")
	if err := process.Run(); err != nil {
		t.Fatalf("run true: %v", err)
	}
	return process.Process.Pid
}

// gatedBackend is a fake provider that registers through the launch gate
// before it does any work, and counts the launches it was gated for.
type gatedBackend struct {
	*orchestratortest.Backend
	group    int
	launches *int
}

func (b gatedBackend) Run(ctx context.Context, request backend.RunRequest) (backend.RunResult, error) {
	if gate := request.LaunchGate; gate != nil {
		if err := gate.Register(execution.StartedProcess{PID: b.group, ProcessGroup: b.group, StartedAt: time.Now()}); err != nil {
			return backend.RunResult{Process: execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: -1}},
				fmt.Errorf("%w: register the launch: %w", execution.ErrProcessNotStarted, err)
		}
		*b.launches++
	}
	return b.Backend.Run(ctx, request)
}

// routedProvider is one fake provider for a run's developer. refuse says which
// requests it refuses for a usage limit, and develop what a served attempt
// writes into the worktree.
type routedProvider struct {
	name     domain.Backend
	fake     *orchestratortest.Backend
	launches int
}

func newRoutedProvider(name domain.Backend, session string, refuse func(backend.RunRequest) *backend.UsageLimit, develop func(backend.RunRequest) error) *routedProvider {
	provider := &routedProvider{name: name, fake: &orchestratortest.Backend{DeveloperSession: session}}
	provider.fake.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role != domain.RoleDeveloper {
			return backend.RunResult{}, fmt.Errorf("unexpected role %q", request.Role)
		}
		if limit := refuse(request); limit != nil {
			return backend.RunResult{
				Backend: name, SessionID: session, IsError: true, StopReason: "usage_limit", UsageLimit: limit,
				Process: execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1}, LastEvent: request.LastSequence,
			}, nil
		}
		if err := develop(request); err != nil {
			return backend.RunResult{}, err
		}
		return backend.RunResult{
			Backend: name, SessionID: session, ResolvedModel: request.Model, FinalText: "implemented the work item",
			Process: execution.ProcessResult{Status: execution.ProcessSucceeded}, LastEvent: request.LastSequence,
		}, nil
	}
	return provider
}

func (p *routedProvider) adapter(t *testing.T) backend.Backend {
	return gatedBackend{Backend: p.fake, group: exitedProcessGroup(t), launches: &p.launches}
}

func (p *routedProvider) developerRequests() []backend.RunRequest {
	return p.fake.RequestsForRole(domain.RoleDeveloper)
}

// writeRoutedFeature is what a served developer attempt writes.
func writeRoutedFeature(request backend.RunRequest) error {
	return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
}

// limitedModels refuses every request for one of the named models.
func limitedModels(limit backend.UsageLimit, models ...string) func(backend.RunRequest) *backend.UsageLimit {
	return func(request backend.RunRequest) *backend.UsageLimit {
		for _, model := range models {
			if request.Model == model {
				copied := limit
				return &copied
			}
		}
		return nil
	}
}

func refuseNothing(backend.RunRequest) *backend.UsageLimit { return nil }

type fixedAccount string

func (f fixedAccount) ChooseAccount() (config.AccountEndpoint, error) {
	return config.AccountEndpoint{Alias: string(f)}, nil
}

// knownLimits is the record of endpoints already known to be limited, keyed by
// account and model.
type knownLimits map[string]bool

func (k knownLimits) KnownLimited(account, model string, _ time.Time) (KnownLimit, bool, error) {
	if k[account+"/"+model] {
		return KnownLimit{Says: "the test recorded a refusal of it"}, true, nil
	}
	return KnownLimit{}, false, nil
}

func endpoint(provider domain.Backend, model, account string) *domain.EndpointSpec {
	return &domain.EndpointSpec{Provider: provider, Model: model, Account: account}
}

var (
	claudeOpus   = endpoint(domain.BackendClaudeCode, "opus", routedClaudeAlias)
	claudeSonnet = endpoint(domain.BackendClaudeCode, "sonnet", routedClaudeAlias)
	codexSol     = endpoint(domain.BackendCodex, routedCodexModel, routedCodexAlias)
)

// routedSlots is a configuration's developer slots, each with the pair given.
func routedSlots(pairs ...[2]*domain.EndpointSpec) []domain.DeveloperSlot {
	slots := make([]domain.DeveloperSlot, len(pairs))
	for index, pair := range pairs {
		if pair[0] == nil {
			continue
		}
		slots[index] = domain.DeveloperSlot{Routing: &domain.EndpointPair{Enabled: true, Primary: pair[0], Alternate: pair[1]}}
	}
	return slots
}

// routedPipeline is a pipeline whose developer slots carry the pairs given,
// with a fake provider for each backend and a reviewer of its own that
// approves.
func routedPipeline(t *testing.T, repository, worktreeRoot string, store *runstate.Store, tracker *orchestratortest.Tracker, claude, codex *routedProvider, slots []domain.DeveloperSlot, checks ...string) Pipeline {
	t.Helper()
	if len(checks) == 0 {
		checks = []string{"exit 0"}
	}
	claudeAdapter, codexAdapter := claude.adapter(t), codex.adapter(t)
	pipeline := newSharedPipeline(t, repository, worktreeRoot, store, tracker, claudeAdapter, checks)
	reviewer := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	pipeline = automatic(pipeline, reviewer)
	pipeline.Reviewer = review.Reviewer{Backend: reviewer, Model: testReviewerModel}
	pipeline.Config.Accounts = map[string]config.Account{
		routedClaudeAlias: {Provider: domain.BackendClaudeCode},
		routedCodexAlias:  {Provider: domain.BackendCodex},
	}
	pipeline.Config.Execution.MaxConcurrentDevelopers = len(slots)
	pipeline.Config.Execution.DeveloperSlots = slots
	developer := pipeline.Config.Agents["developer"]
	developer.Instances = len(slots)
	developer.Account = routedClaudeAlias
	pipeline.Config.Agents["developer"] = developer
	reviewerAgent := pipeline.Config.Agents["reviewer"]
	reviewerAgent.Account = routedClaudeAlias
	pipeline.Config.Agents["reviewer"] = reviewerAgent
	pipeline.RecordedBackends = func(named domain.Backend) (backend.Backend, bool) {
		if named == domain.BackendCodex {
			return codexAdapter, true
		}
		return nil, false
	}
	pipeline.Accounts = fixedAccount(routedClaudeAlias)
	pipeline.StateRoot = t.TempDir()
	return pipeline
}

func routedTracker() *orchestratortest.Tracker {
	return &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
}

func loadRouting(t *testing.T, store *runstate.Store, runID string) (runstate.State, *runstate.RunRouting) {
	t.Helper()
	state, err := store.Load(runID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Routing == nil {
		t.Fatalf("run %s recorded no routing", runID)
	}
	return state, state.Routing
}

// developerOperations is the run's developer operations, oldest first.
func developerOperations(routing *runstate.RunRouting) []runstate.RoutedOperation {
	var operations []runstate.RoutedOperation
	for _, operation := range routing.Operations {
		if operation.Role == domain.RoleDeveloper {
			operations = append(operations, operation)
		}
	}
	return operations
}

// A run reserved for a slot with a pair records the slot and the pair before it
// claims anything, and its developer is invoked on the pair's primary — here
// the Codex primary of a slot whose configured developer is Claude Code.
func TestARoutedRunStartsOnItsSlotsPrimaryAndRecordsEveryAttempt(t *testing.T) {
	t.Parallel()
	repository, worktreeRoot, store := restartableFixture(t)
	tracker := routedTracker()
	claude := newRoutedProvider(domain.BackendClaudeCode, "claude-session", refuseNothing, writeRoutedFeature)
	codex := newRoutedProvider(domain.BackendCodex, "codex-session", refuseNothing, writeRoutedFeature)
	slots := routedSlots([2]*domain.EndpointSpec{codexSol, claudeOpus}, [2]*domain.EndpointSpec{claudeOpus, codexSol})
	pipeline := routedPipeline(t, repository, worktreeRoot, store, tracker, claude, codex, slots)

	outcome, err := pipeline.Run(withDeveloperSlot(context.Background(), 1), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("the routed run did not complete: %#v", outcome)
	}
	if len(claude.developerRequests()) != 0 {
		t.Fatalf("the Claude Code developer was invoked %d time(s) for a slot whose primary is Codex", len(claude.developerRequests()))
	}
	requests := codex.developerRequests()
	if len(requests) != 1 || codex.launches != 1 {
		t.Fatalf("Codex developer invocations = %d, gated launches = %d, want one of each", len(requests), codex.launches)
	}
	if requests[0].Model != routedCodexModel || requests[0].AccountAlias != routedCodexAlias {
		t.Fatalf("invoked on model %q account %q, want the slot primary's", requests[0].Model, requests[0].AccountAlias)
	}
	state, routing := loadRouting(t, store, outcome.RunID)
	if state.RecordedSlot() != 1 || routing.Slot.Origin != runstate.RoutingClaimed {
		t.Fatalf("recorded slot = %d (%v), want slot 1 claimed", state.RecordedSlot(), routing.Slot)
	}
	snapshot := routing.Developer
	if snapshot == nil || !snapshot.Explicit || snapshot.Primary.Provider != domain.BackendCodex || snapshot.Alternate == nil || snapshot.Alternate.Provider != domain.BackendClaudeCode {
		t.Fatalf("pinned snapshot = %#v, want slot 1's codex-then-claude pair", snapshot)
	}
	if state.Backend != domain.BackendCodex || state.AccountAlias != routedCodexAlias || state.ProviderModel != routedCodexModel {
		t.Fatalf("run recorded backend %s account %s model %s, want the primary's", state.Backend, state.AccountAlias, state.ProviderModel)
	}
	operations := developerOperations(routing)
	if len(operations) != 1 || operations[0].Kind != runstate.OperationDevelop || operations[0].Completed == nil {
		t.Fatalf("developer operations = %#v, want one completed develop operation", operations)
	}
	operation := operations[0]
	if operation.Switch != nil || operation.SwitchAllowance != 1 || len(operation.Attempts) != 1 {
		t.Fatalf("operation = %#v, want one attempt, no switch, and its switch unspent", operation)
	}
	attempt := operation.Attempts[0]
	if attempt.Choice != runstate.EndpointPrimary || attempt.Execution == nil || attempt.LaunchedAt == nil ||
		attempt.Ended == nil || attempt.Ended.Classification != "served" || attempt.Ended.Termination != runstate.TerminationConfirmed {
		t.Fatalf("attempt = %#v, want a primary attempt registered before launch and ended served with its stop confirmed", attempt)
	}
}

// A classified usage limit on the primary moves the operation to its alternate
// once, automatically, in either provider direction and between two models of
// one provider. The alternate's attempt is a new session rebuilt from the run's
// record, the refused session is never offered to it, the work the run had is
// still in its worktree, and no repair attempt or relaunch is spent.
func TestAUsageLimitOnThePrimaryMovesTheOperationToItsAlternateOnce(t *testing.T) {
	t.Parallel()
	limit := backend.UsageLimit{Kind: "five_hour", ResetsAt: baseTime.Add(3 * time.Hour)}
	for _, test := range []struct {
		name               string
		pair               [2]*domain.EndpointSpec
		limited            string
		primary, alternate domain.Backend
		unknownReset       bool
	}{
		{name: "codex to claude code", pair: [2]*domain.EndpointSpec{codexSol, claudeOpus}, limited: routedCodexModel, primary: domain.BackendCodex, alternate: domain.BackendClaudeCode},
		{name: "claude code to codex", pair: [2]*domain.EndpointSpec{claudeOpus, codexSol}, limited: "opus", primary: domain.BackendClaudeCode, alternate: domain.BackendCodex, unknownReset: true},
		{name: "one provider, another model", pair: [2]*domain.EndpointSpec{claudeOpus, claudeSonnet}, limited: "opus", primary: domain.BackendClaudeCode, alternate: domain.BackendClaudeCode},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository, worktreeRoot, store := restartableFixture(t)
			tracker := routedTracker()
			refusal := limit
			if test.unknownReset {
				refusal.ResetsAt = time.Time{}
			}
			// The primary's first attempt writes part of the work before it is
			// refused, and the alternate checks it is still there.
			develop := func(request backend.RunRequest) error {
				if request.Model != test.limited {
					if _, err := os.Stat(filepath.Join(request.WorkingDirectory, "partial.txt")); err != nil {
						return fmt.Errorf("the alternate found the primary's work gone: %w", err)
					}
				}
				return writeRoutedFeature(request)
			}
			refuse := func(request backend.RunRequest) *backend.UsageLimit {
				if request.Model != test.limited {
					return nil
				}
				if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "partial.txt"), []byte("begun\n"), 0o600); err != nil {
					t.Errorf("write the partial work: %v", err)
				}
				copied := refusal
				return &copied
			}
			claude := newRoutedProvider(domain.BackendClaudeCode, "claude-session", refuse, develop)
			codex := newRoutedProvider(domain.BackendCodex, "codex-session", refuse, develop)
			pipeline := routedPipeline(t, repository, worktreeRoot, store, tracker, claude, codex, routedSlots(test.pair))
			clock := &pausingClock{now: baseTime}
			pipeline = waiting(pipeline, clock, 6*time.Hour, time.Minute)

			outcome, err := pipeline.Run(withDeveloperSlot(context.Background(), 1), tracker.Item.ID)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if outcome.Integration == nil || !tracker.Closed || outcome.Paused {
				t.Fatalf("the switched run did not complete: %#v", outcome)
			}
			if len(clock.slept) != 0 {
				t.Fatalf("waits = %v, want the alternate to serve without waiting the limit out", clock.slept)
			}
			var all []backend.RunRequest
			all = append(all, claude.developerRequests()...)
			all = append(all, codex.developerRequests()...)
			if len(all) != 2 || claude.launches+codex.launches != 2 {
				t.Fatalf("developer invocations = %d, gated launches = %d, want the refused primary and one alternate", len(all), claude.launches+codex.launches)
			}
			var primaryRequest, alternateRequest backend.RunRequest
			for _, request := range all {
				if request.Model == test.limited {
					primaryRequest = request
				} else {
					alternateRequest = request
				}
			}
			if alternateRequest.Model == "" || alternateRequest.Model == primaryRequest.Model {
				t.Fatalf("requests = %#v, want one on each endpoint", all)
			}
			if alternateRequest.SessionID != "" {
				t.Fatalf("the alternate was offered session %q, which another endpoint opened", alternateRequest.SessionID)
			}
			if !strings.Contains(alternateRequest.Prompt, reconstructedPromptHeading) || !strings.Contains(alternateRequest.Prompt, outcome.Branch) {
				t.Fatalf("the alternate's prompt does not rebuild the run's context:\n%s", alternateRequest.Prompt)
			}
			state, routing := loadRouting(t, store, outcome.RunID)
			operations := developerOperations(routing)
			if len(operations) != 1 {
				t.Fatalf("developer operations = %d, want the one operation across the switch", len(operations))
			}
			operation := operations[0]
			sw := operation.Switch
			if sw == nil || sw.Trigger != runstate.SwitchUsageLimit || sw.Progress != runstate.TransitionOutcomeRecorded || operation.SwitchAllowance != 0 || operation.Selected != runstate.EndpointAlternate {
				t.Fatalf("operation = %#v, want its one switch spent on the usage limit and the alternate selected", operation)
			}
			if len(operation.Attempts) != 2 {
				t.Fatalf("attempts = %#v, want the primary's and the alternate's", operation.Attempts)
			}
			source, destination := operation.Attempts[0], operation.Attempts[1]
			if source.Choice != runstate.EndpointPrimary || source.Ended == nil || source.Ended.Classification != runstate.UsageLimitClassification || sw.SourceAttempt != source.ID {
				t.Fatalf("source attempt = %#v, want the primary ended on its usage limit", source)
			}
			if destination.ID != sw.DestinationAttempt || destination.Predecessor != source.ID || destination.Choice != runstate.EndpointAlternate ||
				destination.Mode != runstate.SessionReconstruction || destination.Endpoint.Provider != test.alternate || destination.Ended == nil || destination.Ended.Classification != "served" {
				t.Fatalf("destination attempt = %#v, want the reserved alternate attempt rebuilt from the record and served", destination)
			}
			if state.RepairAttempts != 0 || state.TransientRelaunches != 0 {
				t.Fatalf("repair attempts = %d, relaunches = %d, want a switch to spend neither", state.RepairAttempts, state.TransientRelaunches)
			}
			if state.Backend != test.alternate || state.ProviderSessionID == "" {
				t.Fatalf("run ended on backend %s with session %q, want the alternate's own", state.Backend, state.ProviderSessionID)
			}
		})
	}
}

// A primary already known to be limited is passed over before anything is
// launched on it: the alternate serves the first attempt, and no primary
// attempt is recorded that never happened.
func TestAPrimaryKnownToBeLimitedIsSkippedWithoutRecordingAnAttempt(t *testing.T) {
	t.Parallel()
	repository, worktreeRoot, store := restartableFixture(t)
	tracker := routedTracker()
	claude := newRoutedProvider(domain.BackendClaudeCode, "claude-session", refuseNothing, writeRoutedFeature)
	codex := newRoutedProvider(domain.BackendCodex, "codex-session", refuseNothing, writeRoutedFeature)
	pipeline := routedPipeline(t, repository, worktreeRoot, store, tracker, claude, codex, routedSlots([2]*domain.EndpointSpec{claudeOpus, codexSol}))
	pipeline.EndpointLimits = knownLimits{routedClaudeAlias + "/opus": true}

	outcome, err := pipeline.Run(withDeveloperSlot(context.Background(), 1), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("the run did not complete: %#v", outcome)
	}
	if len(claude.developerRequests()) != 0 || len(codex.developerRequests()) != 1 {
		t.Fatalf("invocations: claude %d, codex %d; want only the alternate", len(claude.developerRequests()), len(codex.developerRequests()))
	}
	_, routing := loadRouting(t, store, outcome.RunID)
	operation := developerOperations(routing)[0]
	if operation.Switch == nil || operation.Switch.Trigger != runstate.SwitchPrimaryLimited || operation.Switch.SourceAttempt != "" || operation.SwitchAllowance != 0 {
		t.Fatalf("operation = %#v, want the switch past a known limit with no source attempt", operation)
	}
	if len(operation.Attempts) != 1 || operation.Attempts[0].Choice != runstate.EndpointAlternate || operation.Attempts[0].Mode != runstate.SessionFresh {
		t.Fatalf("attempts = %#v, want the alternate's alone, starting fresh", operation.Attempts)
	}
}

// An alternate that cannot serve leaves the operation on its primary, waiting
// the limit out with the reason recorded, and nothing is launched on it. A
// later process, once the primary is known to be limited and the alternate can
// serve, resumes the same operation and moves it then — the switch was never
// spent on the alternate that could not take it.
func TestAnIneligibleAlternateKeepsThePrimaryAndARestartMovesTheSameOperation(t *testing.T) {
	t.Parallel()
	repository, worktreeRoot, store := restartableFixture(t)
	tracker := routedTracker()
	resetsAt := baseTime.Add(2 * time.Hour)
	limit := backend.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt}
	slots := routedSlots([2]*domain.EndpointSpec{claudeOpus, codexSol})

	claude := newRoutedProvider(domain.BackendClaudeCode, "claude-session", limitedModels(limit, "opus"), writeRoutedFeature)
	codex := newRoutedProvider(domain.BackendCodex, "codex-session", refuseNothing, writeRoutedFeature)
	first := waiting(routedPipeline(t, repository, worktreeRoot, store, tracker, claude, codex, slots), &pausingClock{now: baseTime}, 6*time.Hour, time.Minute)
	first.EndpointLimits = knownLimits{routedCodexAlias + "/" + routedCodexModel: true}
	paused, err := first.Run(withDeveloperSlot(context.Background(), 1), tracker.Item.ID)
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if !paused.Paused || len(codex.developerRequests()) != 0 || len(claude.developerRequests()) != 1 {
		t.Fatalf("outcome = %#v with codex invoked %d time(s), want the run paused on its primary and the alternate untouched", paused, len(codex.developerRequests()))
	}
	_, routing := loadRouting(t, store, paused.RunID)
	operation := developerOperations(routing)[0]
	if operation.Selected != runstate.EndpointPrimary || operation.SwitchAllowance != 1 || operation.Switch != nil {
		t.Fatalf("operation = %#v, want it still on its primary with its switch unspent", operation)
	}
	if operation.Waiting == nil || operation.Waiting.Endpoint != runstate.EndpointPrimary || !strings.Contains(operation.Waiting.Reason, "already known to have reached its usage limit") {
		t.Fatalf("wait = %#v, want the operation waiting on its primary saying why the alternate could not serve", operation.Waiting)
	}

	// A later process, with the reset passed, the primary known to be limited
	// again, and the alternate clear: the same operation moves.
	claudeAgain := newRoutedProvider(domain.BackendClaudeCode, "claude-session", limitedModels(limit, "opus"), writeRoutedFeature)
	codexAgain := newRoutedProvider(domain.BackendCodex, "codex-session", refuseNothing, writeRoutedFeature)
	second := waiting(routedPipeline(t, repository, worktreeRoot, store, tracker, claudeAgain, codexAgain, slots), &pausingClock{now: resetsAt.Add(time.Minute)}, 6*time.Hour, time.Minute)
	second.EndpointLimits = knownLimits{routedClaudeAlias + "/opus": true}
	outcome, err := second.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if outcome.RunID != paused.RunID || outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("outcome = %#v, want the same run resumed and completed", outcome)
	}
	if len(claudeAgain.developerRequests()) != 0 || len(codexAgain.developerRequests()) != 1 {
		t.Fatalf("resumed invocations: claude %d, codex %d; want only the alternate", len(claudeAgain.developerRequests()), len(codexAgain.developerRequests()))
	}
	_, routing = loadRouting(t, store, paused.RunID)
	operations := developerOperations(routing)
	if len(operations) != 1 || operations[0].ID != operation.ID {
		t.Fatalf("operations = %#v, want the operation %s carried across the restart", operations, operation.ID)
	}
	resumed := operations[0]
	if resumed.Switch == nil || resumed.Switch.Trigger != runstate.SwitchPrimaryLimited || resumed.SwitchAllowance != 0 || len(resumed.Attempts) != 2 || resumed.Waiting != nil {
		t.Fatalf("operation = %#v, want the refused primary attempt, the switch past the known limit, and the alternate's attempt", resumed)
	}
}

// Once an operation has moved to its alternate it stays there. When the
// alternate is limited too the run waits on the alternate; a later process,
// even under a configuration that pairs the slot differently, resumes on the
// alternate the run was pinned to, never back on the primary, with no second
// switch and no counter reset.
func TestAnOperationOnItsAlternateStaysThereAcrossARestartAndAConfigurationChange(t *testing.T) {
	t.Parallel()
	repository, worktreeRoot, store := restartableFixture(t)
	tracker := routedTracker()
	resetsAt := baseTime.Add(2 * time.Hour)
	limit := backend.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt}

	claude := newRoutedProvider(domain.BackendClaudeCode, "claude-session", limitedModels(limit, "opus"), writeRoutedFeature)
	codex := newRoutedProvider(domain.BackendCodex, "codex-session", limitedModels(limit, routedCodexModel), writeRoutedFeature)
	first := waiting(routedPipeline(t, repository, worktreeRoot, store, tracker, claude, codex, routedSlots([2]*domain.EndpointSpec{claudeOpus, codexSol})),
		&pausingClock{now: baseTime}, 6*time.Hour, time.Minute)
	paused, err := first.Run(withDeveloperSlot(context.Background(), 1), tracker.Item.ID)
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if !paused.Paused || len(claude.developerRequests()) != 1 || len(codex.developerRequests()) != 1 {
		t.Fatalf("outcome = %#v; claude %d, codex %d; want one refusal on each and the run paused", paused, len(claude.developerRequests()), len(codex.developerRequests()))
	}
	pausedState, routing := loadRouting(t, store, paused.RunID)
	operation := developerOperations(routing)[0]
	if operation.Selected != runstate.EndpointAlternate || operation.Waiting == nil || operation.Waiting.Endpoint != runstate.EndpointAlternate {
		t.Fatalf("operation = %#v, want it waiting on the alternate it moved to", operation)
	}
	pinned := routing.Developer.Digest

	// The configuration now pairs slot 1 the other way round, and both
	// endpoints serve.
	claudeAgain := newRoutedProvider(domain.BackendClaudeCode, "claude-session", refuseNothing, writeRoutedFeature)
	codexAgain := newRoutedProvider(domain.BackendCodex, "codex-session", refuseNothing, writeRoutedFeature)
	second := waiting(routedPipeline(t, repository, worktreeRoot, store, tracker, claudeAgain, codexAgain, routedSlots([2]*domain.EndpointSpec{codexSol, claudeSonnet})),
		&pausingClock{now: resetsAt.Add(time.Minute)}, 6*time.Hour, time.Minute)
	outcome, err := second.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if outcome.RunID != paused.RunID || outcome.Integration == nil {
		t.Fatalf("outcome = %#v, want the same run resumed and completed", outcome)
	}
	if len(claudeAgain.developerRequests()) != 0 {
		t.Fatalf("the resumed run went back to the Claude Code primary %d time(s)", len(claudeAgain.developerRequests()))
	}
	requests := codexAgain.developerRequests()
	if len(requests) != 1 || requests[0].Model != routedCodexModel || requests[0].AccountAlias != routedCodexAlias {
		t.Fatalf("resumed requests = %#v, want one on the pinned Codex alternate", requests)
	}
	state, routing := loadRouting(t, store, paused.RunID)
	if routing.Developer.Digest != pinned || len(routing.Reconfigurations) != 0 {
		t.Fatalf("the pinned pair changed under the run: %s, want %s", routing.Developer.Digest, pinned)
	}
	operations := developerOperations(routing)
	resumed := operations[0]
	if len(operations) != 1 || resumed.ID != operation.ID || resumed.SwitchAllowance != 0 || resumed.Switch == nil || resumed.Switch.ID != operation.Switch.ID {
		t.Fatalf("operations = %#v, want the one operation with its one switch", operations)
	}
	if len(resumed.Attempts) != 3 || resumed.Attempts[2].Choice != runstate.EndpointAlternate {
		t.Fatalf("attempts = %#v, want the resumed attempt on the alternate", resumed.Attempts)
	}
	// The alternate's own session was carried: the same endpoint resumes it.
	if requests[0].SessionID != pausedState.ProviderSessionID || resumed.Attempts[2].Mode != runstate.SessionNativeResume {
		t.Fatalf("resumed session %q mode %s, want the alternate's own session %q resumed", requests[0].SessionID, resumed.Attempts[2].Mode, pausedState.ProviderSessionID)
	}
	if state.RepairAttempts != 0 || state.TransientRelaunches != 0 {
		t.Fatalf("repair attempts = %d, relaunches = %d, want neither spent", state.RepairAttempts, state.TransientRelaunches)
	}
}

// A repair is a new operation: it starts from the primary again with its own
// switch, and on the alternate it is handed the failure it answers and the
// work the run already has.
func TestARepairIsANewOperationThatStartsFromThePrimaryAndKeepsItsFindings(t *testing.T) {
	t.Parallel()
	repository, worktreeRoot, store := restartableFixture(t)
	tracker := routedTracker()
	limit := backend.UsageLimit{Kind: "five_hour", ResetsAt: baseTime.Add(3 * time.Hour)}
	served := 0
	develop := func(request backend.RunRequest) error {
		served++
		if served == 1 {
			return writeRoutedFeature(request)
		}
		if _, err := os.Stat(filepath.Join(request.WorkingDirectory, "feature.txt")); err != nil {
			return fmt.Errorf("the repair found the first attempt's work gone: %w", err)
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "fixed.txt"), []byte("fixed\n"), 0o600)
	}
	claude := newRoutedProvider(domain.BackendClaudeCode, "claude-session", limitedModels(limit, "opus"), develop)
	codex := newRoutedProvider(domain.BackendCodex, "codex-session", refuseNothing, develop)
	pipeline := routedPipeline(t, repository, worktreeRoot, store, tracker, claude, codex, routedSlots([2]*domain.EndpointSpec{claudeOpus, codexSol}),
		"test -f fixed.txt || { echo fixed.txt is missing; exit 1; }")
	pipeline = waiting(pipeline, &pausingClock{now: baseTime}, 6*time.Hour, time.Minute)

	outcome, err := pipeline.Run(withDeveloperSlot(context.Background(), 1), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || !tracker.Closed || outcome.RepairAttempts != 1 {
		t.Fatalf("outcome = %#v, want the run completed after one repair", outcome)
	}
	if len(claude.developerRequests()) != 2 || len(codex.developerRequests()) != 2 {
		t.Fatalf("claude %d, codex %d; want each operation refused once on the primary and served once on the alternate",
			len(claude.developerRequests()), len(codex.developerRequests()))
	}
	repairRequest := codex.developerRequests()[1]
	if !strings.Contains(repairRequest.Prompt, "fixed.txt is missing") || !strings.Contains(repairRequest.Prompt, reconstructedPromptHeading) {
		t.Fatalf("the repair on the alternate lost the failure it answers or the run's context:\n%s", repairRequest.Prompt)
	}
	if repairRequest.SessionID != "" {
		t.Fatalf("the repair on the alternate was offered session %q, opened before the primary's refusal", repairRequest.SessionID)
	}
	_, routing := loadRouting(t, store, outcome.RunID)
	operations := developerOperations(routing)
	if len(operations) != 2 || operations[0].Kind != runstate.OperationDevelop || operations[1].Kind != runstate.OperationRepair {
		t.Fatalf("operations = %#v, want the develop operation and the repair", operations)
	}
	for _, operation := range operations {
		if operation.Completed == nil || operation.Switch == nil || operation.Switch.Trigger != runstate.SwitchUsageLimit || len(operation.Attempts) != 2 {
			t.Fatalf("operation = %#v, want each completed with its own switch from a refused primary attempt", operation)
		}
		if operation.Attempts[0].Choice != runstate.EndpointPrimary {
			t.Fatalf("operation %s started on its %s, want each genuine operation to start on the primary", operation.ID, operation.Attempts[0].Choice)
		}
	}
}

// A refusal the provider did not classify as a usage limit never moves an
// operation: a login nobody has renewed is waited out on the primary exactly
// as on a run with no pair, and the alternate is never asked.
func TestALoginRefusalIsNeverAUsageLimitSwitch(t *testing.T) {
	t.Parallel()
	repository, worktreeRoot, store := restartableFixture(t)
	tracker := routedTracker()
	refused := 0
	claude := newRoutedProvider(domain.BackendClaudeCode, "claude-session", refuseNothing, writeRoutedFeature)
	respond := claude.fake.Respond
	claude.fake.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if refused == 0 {
			refused++
			return backend.RunResult{
				Backend: domain.BackendClaudeCode, IsError: true, StopReason: "api_error", FinalText: "Not logged in",
				ProviderOutage: &backend.ProviderOutage{Cause: domain.ProviderUnauthenticated, Detail: "not logged in"},
				Process:        execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1}, LastEvent: request.LastSequence,
			}, nil
		}
		return respond(request)
	}
	codex := newRoutedProvider(domain.BackendCodex, "codex-session", refuseNothing, writeRoutedFeature)
	pipeline := routedPipeline(t, repository, worktreeRoot, store, tracker, claude, codex, routedSlots([2]*domain.EndpointSpec{claudeOpus, codexSol}))
	pipeline = waiting(pipeline, &pausingClock{now: baseTime}, 6*time.Hour, 6*time.Hour)
	pipeline.Config.Execution.UsageLimitUnknownResetPause = config.Duration(30 * time.Minute)

	outcome, err := pipeline.Run(withDeveloperSlot(context.Background(), 1), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || len(codex.developerRequests()) != 0 || len(claude.developerRequests()) != 2 {
		t.Fatalf("outcome = %#v; claude %d, codex %d; want the login waited out on the primary", outcome, len(claude.developerRequests()), len(codex.developerRequests()))
	}
	_, routing := loadRouting(t, store, outcome.RunID)
	operation := developerOperations(routing)[0]
	if operation.Switch != nil || operation.SwitchAllowance != 1 || operation.Attempts[0].Ended.Classification != "provider_unavailable" {
		t.Fatalf("operation = %#v, want no switch and the refusal recorded as the provider unavailable", operation)
	}
}

// A project without pairs keeps exactly what it had: no slot, no pinned pair,
// and no operation on the run's record. A project with a pair on another slot
// records the slot a run occupies and nothing more, and invokes the developer
// it always did.
func TestRunsOnSlotsWithoutAPairKeepTheirRouting(t *testing.T) {
	t.Parallel()
	repository, worktreeRoot, store := restartableFixture(t)
	tracker := routedTracker()
	claude := newRoutedProvider(domain.BackendClaudeCode, "claude-session", refuseNothing, writeRoutedFeature)
	codex := newRoutedProvider(domain.BackendCodex, "codex-session", refuseNothing, writeRoutedFeature)
	pipeline := routedPipeline(t, repository, worktreeRoot, store, tracker, claude, codex, routedSlots([2]*domain.EndpointSpec{nil, nil}, [2]*domain.EndpointSpec{codexSol, claudeOpus}))

	outcome, err := pipeline.Run(withDeveloperSlot(context.Background(), 1), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || len(codex.developerRequests()) != 0 || len(claude.developerRequests()) != 1 {
		t.Fatalf("outcome = %#v; claude %d, codex %d; want the configured developer", outcome, len(claude.developerRequests()), len(codex.developerRequests()))
	}
	if request := claude.developerRequests()[0]; request.Model != testDeveloperModel || request.LaunchGate != nil {
		t.Fatalf("request = model %q gated %t, want the developer's model, launched as before", request.Model, request.LaunchGate != nil)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.RecordedSlot() != 1 || state.Routing.Developer != nil || len(state.Routing.Operations) != 0 {
		t.Fatalf("routing = %#v, want slot 1 recorded and nothing pinned", state.Routing)
	}

	plainRepository, plainRoot, plainStore := restartableFixture(t)
	plainTracker := routedTracker()
	plain := newRoutedProvider(domain.BackendClaudeCode, "claude-session", refuseNothing, writeRoutedFeature)
	unrouted := newSharedPipeline(t, plainRepository, plainRoot, plainStore, plainTracker, plain.adapter(t), []string{"exit 0"})
	outcome, err = unrouted.Run(withDeveloperSlot(context.Background(), 1), plainTracker.Item.ID)
	if err != nil {
		t.Fatalf("unrouted Run() error = %v", err)
	}
	state, err = plainStore.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Routing != nil {
		t.Fatalf("a project with no pairs recorded routing: %#v", state.Routing)
	}
}
