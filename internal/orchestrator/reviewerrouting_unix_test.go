//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package orchestrator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// These drive runs whose reviewer agent enables fallback through the real
// pipeline, worktrees and run store. Each endpoint of the reviewer's pair is a
// fake provider that registers through the launch gate before it does any work,
// as the real adapters do, so every review attempt is reserved, registered,
// launched and ended on the run's record. The developer is a separate fake on
// Claude Code, so what the reviewer was handed can be told apart from what the
// developer had.

const developerSessionForReviewTests = "claude-session"

// reviewProvider is the fake provider behind one provider of the reviewer's
// pair. refuse says which requests it refuses for a usage limit, and reply what
// a served review answers.
type reviewProvider struct {
	name  domain.Backend
	mu    sync.Mutex
	asked []backend.RunRequest
	// launches counts the reviews the launch gate was taken for.
	launches int
	refuse   func(backend.RunRequest) *backend.UsageLimit
	reply    func(request backend.RunRequest) (backend.RunResult, error)
	group    int
}

func newReviewProvider(t *testing.T, name domain.Backend, refuse func(backend.RunRequest) *backend.UsageLimit) *reviewProvider {
	provider := &reviewProvider{name: name, refuse: refuse, group: exitedProcessGroup(t)}
	provider.reply = func(request backend.RunRequest) (backend.RunResult, error) {
		return provider.served(request, approveVerdict), nil
	}
	return provider
}

func (p *reviewProvider) served(request backend.RunRequest, text string) backend.RunResult {
	return backend.RunResult{
		Backend: p.name, SessionID: "reviewer-session-" + string(p.name), ResolvedModel: request.Model, FinalText: text,
		Process: execution.ProcessResult{Status: execution.ProcessSucceeded}, LastEvent: request.LastSequence,
	}
}

func (p *reviewProvider) Run(_ context.Context, request backend.RunRequest) (backend.RunResult, error) {
	if request.Role != domain.RoleReviewer {
		return backend.RunResult{}, fmt.Errorf("the reviewer's provider was asked to act as %q", request.Role)
	}
	if gate := request.LaunchGate; gate != nil {
		if err := gate.Register(execution.StartedProcess{PID: p.group, ProcessGroup: p.group, StartedAt: time.Now()}); err != nil {
			return backend.RunResult{Process: execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: -1}},
				fmt.Errorf("%w: register the launch: %w", execution.ErrProcessNotStarted, err)
		}
		p.mu.Lock()
		p.launches++
		p.mu.Unlock()
	}
	p.mu.Lock()
	p.asked = append(p.asked, request)
	p.mu.Unlock()
	if limit := p.refuse(request); limit != nil {
		copied := *limit
		return backend.RunResult{
			Backend: p.name, IsError: true, StopReason: "usage_limit", UsageLimit: &copied,
			Process: execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1}, LastEvent: request.LastSequence,
		}, nil
	}
	return p.reply(request)
}

func (p *reviewProvider) CheckAvailability(context.Context) (backend.Availability, error) {
	return backend.Availability{Installed: true, Authenticated: true}, nil
}

func (p *reviewProvider) requests() []backend.RunRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]backend.RunRequest(nil), p.asked...)
}

type reviewFixture struct {
	repository, worktreeRoot string
	store                    *runstate.Store
	tracker                  *orchestratortest.Tracker
}

func newReviewFixture(t *testing.T) reviewFixture {
	repository, worktreeRoot, store := restartableFixture(t)
	return reviewFixture{repository: repository, worktreeRoot: worktreeRoot, store: store, tracker: routedTracker()}
}

// reviewerPairSpec is a reviewer agent and the fallback it may be served by.
type reviewerPairSpec struct {
	primary  config.AgentConfig
	failover config.Failover
}

var (
	claudeReviewerPrimary = config.AgentConfig{Role: domain.RoleReviewer, Backend: domain.BackendClaudeCode, Model: "opus", Account: routedClaudeAlias, Instances: 1}
	codexReviewerPrimary  = config.AgentConfig{Role: domain.RoleReviewer, Backend: domain.BackendCodex, Model: routedCodexModel, Account: routedCodexAlias, Instances: 1}
	toCodex               = config.Failover{Enabled: true, Provider: domain.BackendCodex, Model: routedCodexModel, Account: routedCodexAlias}
	toClaudeOpus          = config.Failover{Enabled: true, Provider: domain.BackendClaudeCode, Model: "opus", Account: routedClaudeAlias}
	toClaudeSonnet        = config.Failover{Enabled: true, Model: "sonnet"}
)

// reviewerRoutedPipeline is a pipeline whose reviewer agent has the pair given
// and whose reviews are made on the fake providers, by provider. The developer
// is Claude Code and never reviews.
func (f reviewFixture) pipeline(t *testing.T, spec reviewerPairSpec, claude, codex *reviewProvider) Pipeline {
	t.Helper()
	developer := newRoutedProvider(domain.BackendClaudeCode, developerSessionForReviewTests, refuseNothing, writeRoutedFeature)
	pipeline := routedPipeline(t, f.repository, f.worktreeRoot, f.store, f.tracker, developer, newRoutedProvider(domain.BackendCodex, "codex-session", refuseNothing, writeRoutedFeature), routedSlots([2]*domain.EndpointSpec{}))
	agent := spec.primary
	agent.Failover = spec.failover
	pipeline.Config.Agents["reviewer"] = agent
	providers := map[domain.Backend]*reviewProvider{domain.BackendClaudeCode: claude, domain.BackendCodex: codex}
	pipeline.ReviewerFor = func(endpoint runstate.RoutedEndpoint) (ChangeReviewer, bool) {
		provider, ok := providers[endpoint.Provider]
		if !ok || provider == nil {
			return nil, false
		}
		return review.Reviewer{Backend: provider, Model: requestedModel(endpoint), Effort: endpoint.Effort}, true
	}
	// The pipeline's own reviewer is what a project without fallback uses; a routed
	// run reaching it is a defect, so it fails whatever it is asked.
	pipeline.Reviewer = review.Reviewer{Backend: orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict), Model: "opus"}
	pipeline.RecordedBackends = func(named domain.Backend) (backend.Backend, bool) {
		switch named {
		case domain.BackendCodex:
			return codex, codex != nil
		case domain.BackendClaudeCode:
			return claude, claude != nil
		}
		return nil, false
	}
	return pipeline
}

func reviewOperations(routing *runstate.RunRouting) []runstate.RoutedOperation {
	var operations []runstate.RoutedOperation
	for _, operation := range routing.Operations {
		if operation.Role == domain.RoleReviewer {
			operations = append(operations, operation)
		}
	}
	return operations
}

func limitedReviewModels(limit backend.UsageLimit, models ...string) func(backend.RunRequest) *backend.UsageLimit {
	return limitedModels(limit, models...)
}

// A usage limit on the reviewer's primary moves the review to the alternate
// once, automatically, in either provider direction and between two models of
// one provider. The alternate reviews the same candidate in a new session that
// owes nothing to the developer's or the refused one, no wait is taken, and no
// repair attempt or review round is spent beyond the one verdict.
func TestAUsageLimitOnTheReviewersPrimaryMovesTheReviewToItsAlternateOnce(t *testing.T) {
	t.Parallel()
	limit := backend.UsageLimit{Kind: "weekly", ResetsAt: baseTime.Add(30 * time.Hour)}
	for _, test := range []struct {
		name               string
		spec               reviewerPairSpec
		limited            string
		primary, alternate domain.Backend
		alternateAlias     string
		alternateModel     string
	}{
		{name: "claude code to codex", spec: reviewerPairSpec{claudeReviewerPrimary, toCodex}, limited: "opus", primary: domain.BackendClaudeCode, alternate: domain.BackendCodex, alternateAlias: routedCodexAlias, alternateModel: routedCodexModel},
		{name: "codex to claude code", spec: reviewerPairSpec{codexReviewerPrimary, toClaudeOpus}, limited: routedCodexModel, primary: domain.BackendCodex, alternate: domain.BackendClaudeCode, alternateAlias: routedClaudeAlias, alternateModel: "opus"},
		{name: "one provider, another model", spec: reviewerPairSpec{claudeReviewerPrimary, toClaudeSonnet}, limited: "opus", primary: domain.BackendClaudeCode, alternate: domain.BackendClaudeCode, alternateAlias: routedClaudeAlias, alternateModel: "sonnet"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			f := newReviewFixture(t)
			refuse := limitedReviewModels(limit, test.limited)
			claude, codex := newReviewProvider(t, domain.BackendClaudeCode, refuse), newReviewProvider(t, domain.BackendCodex, refuse)
			pipeline := f.pipeline(t, test.spec, claude, codex)
			clock := &pausingClock{now: baseTime}
			pipeline = waiting(pipeline, clock, 48*time.Hour, time.Minute)

			outcome, err := pipeline.Run(context.Background(), f.tracker.Item.ID)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if outcome.Integration == nil || !f.tracker.Closed || outcome.Paused || len(clock.slept) != 0 {
				t.Fatalf("outcome = %#v, waits = %v; want the alternate to serve the review without waiting the limit out", outcome, clock.slept)
			}
			var primaryRequests, alternateRequests []backend.RunRequest
			for _, provider := range []*reviewProvider{claude, codex} {
				for _, request := range provider.requests() {
					if request.Model == test.limited {
						primaryRequests = append(primaryRequests, request)
					} else {
						alternateRequests = append(alternateRequests, request)
					}
				}
			}
			if len(primaryRequests) != 1 || len(alternateRequests) != 1 {
				t.Fatalf("review invocations: primary %d, alternate %d; want the refused primary once and the alternate once", len(primaryRequests), len(alternateRequests))
			}
			served := alternateRequests[0]
			if served.Model != test.alternateModel || served.AccountAlias != test.alternateAlias {
				t.Fatalf("the alternate review ran on model %q account %q, want %q on %q", served.Model, served.AccountAlias, test.alternateModel, test.alternateAlias)
			}
			if served.SessionID != "" || served.SessionID == developerSessionForReviewTests {
				t.Fatalf("the alternate review was offered session %q, which another endpoint or the developer opened", served.SessionID)
			}
			if served.Role != domain.RoleReviewer {
				t.Fatalf("the review ran as %q, want the reviewer", served.Role)
			}
			state, routing := loadRouting(t, f.store, outcome.RunID)
			operations := reviewOperations(routing)
			if len(operations) != 1 {
				t.Fatalf("review operations = %d, want the one operation across the switch", len(operations))
			}
			operation := operations[0]
			if operation.Candidate == "" || operation.Candidate != state.ReviewHeadCommit || operation.Candidate != outcome.ReviewHeadCommit {
				t.Fatalf("operation candidate = %q, run reviewed %q: the operation is not bound to the exact candidate", operation.Candidate, state.ReviewHeadCommit)
			}
			sw := operation.Switch
			if sw == nil || sw.Trigger != runstate.SwitchUsageLimit || operation.SwitchAllowance != 0 || operation.Selected != runstate.EndpointAlternate || operation.Completed == nil {
				t.Fatalf("operation = %#v, want its one switch spent on the usage limit and completed on the alternate", operation)
			}
			if len(operation.Attempts) != 2 {
				t.Fatalf("attempts = %#v, want the primary's and the alternate's", operation.Attempts)
			}
			source, destination := operation.Attempts[0], operation.Attempts[1]
			if source.Choice != runstate.EndpointPrimary || source.Endpoint.Provider != test.primary || source.Ended == nil || source.Ended.Classification != runstate.UsageLimitClassification {
				t.Fatalf("source attempt = %#v, want the primary ended on its usage limit", source)
			}
			if destination.ID != sw.DestinationAttempt || destination.Predecessor != source.ID || destination.Choice != runstate.EndpointAlternate ||
				destination.Mode != runstate.SessionReconstruction || destination.Endpoint.Provider != test.alternate || destination.Endpoint.AccountAlias != test.alternateAlias ||
				destination.Ended == nil || destination.Ended.Classification != "served" || destination.Session != nil {
				t.Fatalf("destination attempt = %#v, want the alternate rebuilt from evidence, with no session handed over, and served", destination)
			}
			if state.RepairAttempts != 0 || state.TransientRelaunches != 0 || state.ReviewRounds != 1 {
				t.Fatalf("repair attempts = %d, relaunches = %d, review rounds = %d; want a switch to spend neither and exactly one counted verdict", state.RepairAttempts, state.TransientRelaunches, state.ReviewRounds)
			}
			if state.ReviewModel != test.alternateModel || state.ReviewDecision != string(review.DecisionApprove) {
				t.Fatalf("recorded review model %q decision %q, want the serving alternate's and an approval", state.ReviewModel, state.ReviewDecision)
			}
			// The developer's own routing and record are the developer's: the review
			// did not move them.
			if routing.Reviewer == nil || routing.Developer != nil || state.Backend != domain.BackendClaudeCode || state.AccountAlias != routedClaudeAlias {
				t.Fatalf("routing = %#v, backend %s account %s; want the reviewer pinned and the developer untouched", routing, state.Backend, state.AccountAlias)
			}
		})
	}
}

// A primary already known to be limited is passed over before anything is
// launched on it, and no primary attempt is recorded that never happened.
func TestAReviewersPrimaryKnownToBeLimitedIsSkippedWithoutRecordingAnAttempt(t *testing.T) {
	t.Parallel()
	f := newReviewFixture(t)
	claude, codex := newReviewProvider(t, domain.BackendClaudeCode, refuseNothing), newReviewProvider(t, domain.BackendCodex, refuseNothing)
	pipeline := f.pipeline(t, reviewerPairSpec{claudeReviewerPrimary, toCodex}, claude, codex)
	pipeline.EndpointLimits = knownLimits{routedClaudeAlias + "/opus": true}

	outcome, err := pipeline.Run(context.Background(), f.tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || len(claude.requests()) != 0 || claude.launches != 0 || len(codex.requests()) != 1 {
		t.Fatalf("outcome = %#v, claude reviews %d, codex reviews %d; want the review on the alternate and nothing launched on the known-limited primary", outcome, len(claude.requests()), len(codex.requests()))
	}
	_, routing := loadRouting(t, f.store, outcome.RunID)
	operation := reviewOperations(routing)[0]
	if operation.Switch == nil || operation.Switch.Trigger != runstate.SwitchPrimaryLimited || len(operation.Attempts) != 1 || operation.Attempts[0].Choice != runstate.EndpointAlternate {
		t.Fatalf("operation = %#v, want one alternate attempt and no primary attempt recorded", operation)
	}
}

// Both endpoints limited: the candidate is kept, nothing is integrated, the
// review waits on its primary with the reason recorded, no review is launched
// on the alternate, and the endpoints are not bounced between. A later process,
// once the primary is known to be limited and the alternate is clear, resumes
// the same operation and moves it then, without having spent a counted verdict.
func TestBothReviewEndpointsLimitedKeepTheCandidateAndARestartMovesTheSameOperation(t *testing.T) {
	t.Parallel()
	f := newReviewFixture(t)
	resetsAt := baseTime.Add(2 * time.Hour)
	limit := backend.UsageLimit{Kind: "weekly", ResetsAt: resetsAt}
	claude := newReviewProvider(t, domain.BackendClaudeCode, limitedReviewModels(limit, "opus"))
	codex := newReviewProvider(t, domain.BackendCodex, refuseNothing)
	spec := reviewerPairSpec{claudeReviewerPrimary, toCodex}
	first := waiting(f.pipeline(t, spec, claude, codex), &pausingClock{now: baseTime}, 6*time.Hour, time.Minute)
	first.EndpointLimits = knownLimits{routedCodexAlias + "/" + routedCodexModel: true}

	paused, err := first.Run(context.Background(), f.tracker.Item.ID)
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if !paused.Paused || paused.Integration != nil || f.tracker.Closed || len(codex.requests()) != 0 || codex.launches != 0 {
		t.Fatalf("outcome = %#v, codex reviews %d; want the candidate kept, the run paused on its primary and the alternate untouched", paused, len(codex.requests()))
	}
	pausedState, routing := loadRouting(t, f.store, paused.RunID)
	operation := reviewOperations(routing)[0]
	if operation.Selected != runstate.EndpointPrimary || operation.Switch != nil || operation.SwitchAllowance != 1 || operation.Completed != nil {
		t.Fatalf("operation = %#v, want it open on its primary with its switch unspent", operation)
	}
	if operation.Waiting == nil || operation.Waiting.Endpoint != runstate.EndpointPrimary || !strings.Contains(operation.Waiting.Reason, "already known to have reached its usage limit") {
		t.Fatalf("wait = %#v, want the operation waiting on its primary saying why the alternate could not serve", operation.Waiting)
	}
	if pausedState.ReviewRounds != 0 || pausedState.ReviewDecision != "" || pausedState.UsageLimitAccount != routedClaudeAlias || pausedState.UsageLimitModel != "opus" {
		t.Fatalf("paused state: rounds %d decision %q refused account %q model %q; want no counted verdict and the refusal recorded against the reviewer's endpoint", pausedState.ReviewRounds, pausedState.ReviewDecision, pausedState.UsageLimitAccount, pausedState.UsageLimitModel)
	}

	claudeAgain := newReviewProvider(t, domain.BackendClaudeCode, limitedReviewModels(limit, "opus"))
	codexAgain := newReviewProvider(t, domain.BackendCodex, refuseNothing)
	second := waiting(f.pipeline(t, spec, claudeAgain, codexAgain), &pausingClock{now: resetsAt.Add(time.Minute)}, 6*time.Hour, time.Minute)
	second.EndpointLimits = knownLimits{routedClaudeAlias + "/opus": true}
	outcome, err := second.Run(context.Background(), f.tracker.Item.ID)
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if outcome.RunID != paused.RunID || outcome.Integration == nil || !f.tracker.Closed {
		t.Fatalf("outcome = %#v, want the same run resumed and completed", outcome)
	}
	if len(claudeAgain.requests()) != 0 || len(codexAgain.requests()) != 1 {
		t.Fatalf("resumed reviews: claude %d, codex %d; want only the alternate", len(claudeAgain.requests()), len(codexAgain.requests()))
	}
	state, routing := loadRouting(t, f.store, paused.RunID)
	operations := reviewOperations(routing)
	if len(operations) != 1 || operations[0].ID != operation.ID {
		t.Fatalf("operations = %#v, want the operation %s carried across the restart", operations, operation.ID)
	}
	resumed := operations[0]
	if resumed.Switch == nil || resumed.Switch.Trigger != runstate.SwitchPrimaryLimited || resumed.SwitchAllowance != 0 || len(resumed.Attempts) != 2 || resumed.Waiting != nil || resumed.Completed == nil {
		t.Fatalf("operation = %#v, want the refused primary attempt, the switch past the known limit, and the alternate's attempt", resumed)
	}
	if state.ReviewRounds != 1 || state.RepairAttempts != 0 {
		t.Fatalf("review rounds = %d, repair attempts = %d; want one counted verdict across the restart and no repair", state.ReviewRounds, state.RepairAttempts)
	}
}

// Once switched, the review stays on the alternate: when the alternate is
// limited too the run waits on the alternate, and a later process, under a
// configuration that has since turned fallback off, resumes on the alternate
// the run was pinned to — never back on the primary, with no second switch.
func TestAReviewOnItsAlternateStaysThereAcrossARestartAndAConfigurationChange(t *testing.T) {
	t.Parallel()
	f := newReviewFixture(t)
	resetsAt := baseTime.Add(2 * time.Hour)
	limit := backend.UsageLimit{Kind: "weekly", ResetsAt: resetsAt}
	spec := reviewerPairSpec{claudeReviewerPrimary, toCodex}
	claude := newReviewProvider(t, domain.BackendClaudeCode, limitedReviewModels(limit, "opus"))
	codex := newReviewProvider(t, domain.BackendCodex, limitedReviewModels(limit, routedCodexModel))
	first := waiting(f.pipeline(t, spec, claude, codex), &pausingClock{now: baseTime}, 6*time.Hour, time.Minute)

	paused, err := first.Run(context.Background(), f.tracker.Item.ID)
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if !paused.Paused || len(claude.requests()) != 1 || len(codex.requests()) != 1 {
		t.Fatalf("outcome = %#v; claude %d, codex %d; want one refusal on each endpoint and the run paused", paused, len(claude.requests()), len(codex.requests()))
	}
	pausedState, routing := loadRouting(t, f.store, paused.RunID)
	operation := reviewOperations(routing)[0]
	if operation.Selected != runstate.EndpointAlternate || operation.Waiting == nil || operation.Waiting.Endpoint != runstate.EndpointAlternate {
		t.Fatalf("operation = %#v, want it waiting on the alternate it moved to", operation)
	}
	if pausedState.UsageLimitAccount != routedCodexAlias || pausedState.UsageLimitModel != routedCodexModel {
		t.Fatalf("the park names account %q model %q, want the alternate's", pausedState.UsageLimitAccount, pausedState.UsageLimitModel)
	}
	pinned := routing.Reviewer.Digest

	claudeAgain := newReviewProvider(t, domain.BackendClaudeCode, refuseNothing)
	codexAgain := newReviewProvider(t, domain.BackendCodex, refuseNothing)
	// Fallback is off in the configuration now.
	second := waiting(f.pipeline(t, reviewerPairSpec{claudeReviewerPrimary, config.Failover{}}, claudeAgain, codexAgain), &pausingClock{now: resetsAt.Add(time.Minute)}, 6*time.Hour, time.Minute)
	outcome, err := second.Run(context.Background(), f.tracker.Item.ID)
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if outcome.RunID != paused.RunID || outcome.Integration == nil {
		t.Fatalf("outcome = %#v, want the same run resumed and completed", outcome)
	}
	if len(claudeAgain.requests()) != 0 || len(codexAgain.requests()) != 1 {
		t.Fatalf("resumed reviews: claude %d, codex %d; want one on the pinned alternate and none back on the primary", len(claudeAgain.requests()), len(codexAgain.requests()))
	}
	state, routing := loadRouting(t, f.store, paused.RunID)
	operations := reviewOperations(routing)
	resumed := operations[0]
	if routing.Reviewer.Digest != pinned || len(operations) != 1 || resumed.ID != operation.ID || resumed.Switch == nil || resumed.Switch.ID != operation.Switch.ID || resumed.SwitchAllowance != 0 || len(resumed.Attempts) != 3 || resumed.Attempts[2].Choice != runstate.EndpointAlternate {
		t.Fatalf("operations = %#v, want the one operation with its one switch and the resumed attempt on the alternate", operations)
	}
	if state.ReviewRounds != 1 {
		t.Fatalf("review rounds = %d, want one counted verdict", state.ReviewRounds)
	}
}

// An alternate that cannot hold the reviewer's read-only posture is never used.
// The configuration that names one is refused before anything is launched, and
// the check made again at the moment of a switch refuses it for the same reason
// where a configuration reached the run another way.
func TestAnAlternateThatCannotEnforceReviewerPermissionsIsNotUsed(t *testing.T) {
	t.Parallel()
	f := newReviewFixture(t)
	limit := backend.UsageLimit{Kind: "weekly", ResetsAt: baseTime.Add(30 * time.Hour)}
	claude := newReviewProvider(t, domain.BackendClaudeCode, limitedReviewModels(limit, "opus"))
	codex := newReviewProvider(t, domain.BackendCodex, refuseNothing)
	writesOnly := config.Failover{Enabled: true, Provider: "writes-only", Model: "any", Account: "writes-only-account"}
	pipeline := f.pipeline(t, reviewerPairSpec{claudeReviewerPrimary, writesOnly}, claude, codex)
	pipeline.Config.Providers = map[string]backend.ProviderPlugin{"writes-only": {
		Adapter: domain.BackendCodex, Binary: "custom-codex", Roles: []domain.AgentRole{domain.RoleReviewer, domain.RoleDeveloper},
		Postures: []backend.Posture{backend.PostureWorktreeWrite},
		Dialect:  backend.DialectSpec{Rules: []backend.DialectRule{{Type: "retry", Answer: backend.AnswerRetrying}}},
	}}
	pipeline.Config.Accounts["writes-only-account"] = config.Account{Provider: "writes-only"}

	if _, err := pipeline.Run(context.Background(), f.tracker.Item.ID); err == nil || !strings.Contains(err.Error(), `cannot hold the "read-only" tool access`) {
		t.Fatalf("Run() error = %v, want the configuration refused for an alternate that cannot hold the reviewer's read-only access", err)
	}
	if len(claude.requests()) != 0 || len(codex.requests()) != 0 || f.tracker.Closed {
		t.Fatalf("reviews: claude %d, codex %d, closed %v; want nothing launched", len(claude.requests()), len(codex.requests()), f.tracker.Closed)
	}
	run := &activeRun{pipeline: pipeline}
	problem := run.endpointProblemFor(context.Background(), runstate.RoutedEndpoint{Provider: "writes-only", AccountAlias: "writes-only-account", Model: "any"}, domain.RoleReviewer)
	if !strings.Contains(problem, `cannot hold the "read-only" tool access`) {
		t.Fatalf("endpointProblemFor() = %q, want a switch refused for an alternate that cannot hold the reviewer's read-only access", problem)
	}
}

// A review that did not end in a verdict — here an answer cut off partway — is
// not one: nothing is integrated, no round is counted, the operation stays open
// on its primary and the attempt is recorded as failed.
func TestAPartialReviewIsNotAVerdict(t *testing.T) {
	t.Parallel()
	f := newReviewFixture(t)
	claude := newReviewProvider(t, domain.BackendClaudeCode, refuseNothing)
	claude.reply = func(request backend.RunRequest) (backend.RunResult, error) {
		return claude.served(request, `{"decision":"approve","approves":"implem`), nil
	}
	codex := newReviewProvider(t, domain.BackendCodex, refuseNothing)
	pipeline := f.pipeline(t, reviewerPairSpec{claudeReviewerPrimary, toCodex}, claude, codex)

	outcome, err := pipeline.Run(context.Background(), f.tracker.Item.ID)
	if err == nil {
		t.Fatalf("Run() succeeded with %#v, want a refusal to integrate on a reply that is not a verdict", outcome)
	}
	if f.tracker.Closed || len(codex.requests()) != 0 {
		t.Fatalf("closed = %v, codex reviews %d; want nothing integrated and no switch for a reply that merely was not a verdict", f.tracker.Closed, len(codex.requests()))
	}
	state, routing := loadRouting(t, f.store, outcome.RunID)
	operations := reviewOperations(routing)
	if len(operations) != 1 || operations[0].Completed != nil || operations[0].Switch != nil || len(operations[0].Attempts) == 0 {
		t.Fatalf("operations = %#v, want one open operation on its primary", operations)
	}
	for _, attempt := range operations[0].Attempts {
		if attempt.Ended == nil || attempt.Ended.Classification == "served" {
			t.Fatalf("attempt = %#v, want it ended as something other than a served verdict", attempt)
		}
	}
	if state.ReviewDecision != "" || state.ReviewRounds != 0 {
		t.Fatalf("decision %q rounds %d, want no verdict recorded", state.ReviewDecision, state.ReviewRounds)
	}
}

// A result for a candidate the worktree no longer holds — the review ran while
// the branch moved — supplies no verdict: nothing is integrated on it and no
// round is counted, however plainly it approves.
func TestAReviewCompletedForAnotherCandidateCannotAuthorizeIntegration(t *testing.T) {
	t.Parallel()
	f := newReviewFixture(t)
	claude := newReviewProvider(t, domain.BackendClaudeCode, refuseNothing)
	claude.reply = func(request backend.RunRequest) (backend.RunResult, error) {
		// The candidate moves while the review is under way.
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "late.txt"), []byte("late\n"), 0o600); err != nil {
			return backend.RunResult{}, err
		}
		for _, args := range [][]string{{"add", "late.txt"}, {"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-m", "late"}} {
			if output, err := exec.Command("git", append([]string{"-C", request.WorkingDirectory}, args...)...).CombinedOutput(); err != nil {
				return backend.RunResult{}, fmt.Errorf("git %v: %v: %s", args, err, output)
			}
		}
		return claude.served(request, approveVerdict), nil
	}
	codex := newReviewProvider(t, domain.BackendCodex, refuseNothing)
	pipeline := f.pipeline(t, reviewerPairSpec{claudeReviewerPrimary, toCodex}, claude, codex)

	outcome, err := pipeline.Run(context.Background(), f.tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), "not accepted") {
		t.Fatalf("Run() = %#v, %v; want a refusal that names the unaccepted result", outcome, err)
	}
	if f.tracker.Closed {
		t.Fatal("work was closed on a review of another candidate")
	}
	state, routing := loadRouting(t, f.store, outcome.RunID)
	operation := reviewOperations(routing)[0]
	if operation.Completed != nil || state.ReviewDecision != "" || state.ReviewRounds != 0 {
		t.Fatalf("operation completed = %v, decision %q, rounds %d; want no verdict taken from it", operation.Completed, state.ReviewDecision, state.ReviewRounds)
	}
}

// A result reported again, or for another candidate, never makes a second
// verdict: the recorded ending stands, and a result naming another candidate
// ends nothing. The counted verdict stays one.
func TestADuplicateOrMisdirectedReviewResultCreatesNoSecondVerdict(t *testing.T) {
	t.Parallel()
	f := newReviewFixture(t)
	claude, codex := newReviewProvider(t, domain.BackendClaudeCode, refuseNothing), newReviewProvider(t, domain.BackendCodex, refuseNothing)
	pipeline := f.pipeline(t, reviewerPairSpec{claudeReviewerPrimary, toCodex}, claude, codex)
	outcome, err := pipeline.Run(context.Background(), f.tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	state, routing := loadRouting(t, f.store, outcome.RunID)
	operation := reviewOperations(routing)[0]
	attempt := operation.Attempts[0]
	report := func(candidate string, ending runstate.AttemptEnding) runstate.ResultDisposition {
		var disposition runstate.ResultDisposition
		if _, err := f.store.UpdateRouting(context.Background(), state, func(r *runstate.RunRouting) (bool, error) {
			var changed bool
			var err error
			disposition, changed, err = r.AcceptResult(runstate.AttemptResult{Operation: operation.ID, Attempt: attempt.ID, Candidate: candidate, Ending: ending})
			return changed, err
		}); err != nil {
			t.Fatalf("AcceptResult() error = %v", err)
		}
		return disposition
	}
	if got := report(operation.Candidate, *attempt.Ended); got != runstate.ResultAdopted {
		t.Fatalf("the same result reported again = %s, want it adopted without change", got)
	}
	if got := report(operation.Candidate, runstate.AttemptEnding{Classification: "failed", Result: "a later report", At: attempt.Ended.At}); got != runstate.ResultLate {
		t.Fatalf("a different ending reported late = %s, want it recorded as late", got)
	}
	if got := report(strings.Repeat("e", 40), *attempt.Ended); got != runstate.ResultRefused {
		t.Fatalf("a result naming another candidate = %s, want it refused", got)
	}
	after, routing := loadRouting(t, f.store, outcome.RunID)
	if ended := reviewOperations(routing)[0].Attempts[0].Ended; ended == nil || ended.Classification != "served" {
		t.Fatalf("recorded ending = %#v, want the original ending to stand", ended)
	}
	if after.ReviewRounds != 1 || state.ReviewRounds != 1 {
		t.Fatalf("review rounds = %d, want the one counted verdict", after.ReviewRounds)
	}
}

// A developer's session is never the reviewer's to resume. A reviewer whose first
// reply could not be read is asked again, and when the session its first reply
// named is the developer's, the second asking starts one of its own.
func TestADeveloperSessionIsNeverOfferedToTheReviewer(t *testing.T) {
	t.Parallel()
	f := newReviewFixture(t)
	claude := newReviewProvider(t, domain.BackendClaudeCode, refuseNothing)
	answers := 0
	claude.reply = func(request backend.RunRequest) (backend.RunResult, error) {
		answers++
		text := "I could not decide."
		if answers > 1 {
			text = approveVerdict
		}
		result := claude.served(request, text)
		// The first reply names the developer's own session; the second is the
		// reviewer's own.
		if answers == 1 {
			result.SessionID = developerSessionForReviewTests
		}
		return result, nil
	}
	codex := newReviewProvider(t, domain.BackendCodex, refuseNothing)
	pipeline := f.pipeline(t, reviewerPairSpec{claudeReviewerPrimary, toCodex}, claude, codex)

	outcome, err := pipeline.Run(context.Background(), f.tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	requests := claude.requests()
	if outcome.Integration == nil || len(requests) != 2 {
		t.Fatalf("outcome = %#v, reviews %d; want the unreadable reply asked for again and approved", outcome, len(requests))
	}
	for _, request := range requests {
		if request.SessionID == developerSessionForReviewTests {
			t.Fatalf("the reviewer was offered the developer's session %q", request.SessionID)
		}
	}
	_, routing := loadRouting(t, f.store, outcome.RunID)
	operation := reviewOperations(routing)[0]
	if len(operation.Attempts) != 2 || operation.Switch != nil {
		t.Fatalf("operation = %#v, want two attempts on the one operation, both on the primary", operation)
	}
}

// A project whose reviewer agent enables no fallback is reviewed exactly as it
// always was: the pipeline's own reviewer, under the run's account, with no
// routing recorded and the routed reviewer never built.
func TestAProjectWithoutReviewerFallbackKeepsItsExistingReview(t *testing.T) {
	t.Parallel()
	f := newReviewFixture(t)
	claude, codex := newReviewProvider(t, domain.BackendClaudeCode, refuseNothing), newReviewProvider(t, domain.BackendCodex, refuseNothing)
	pipeline := f.pipeline(t, reviewerPairSpec{claudeReviewerPrimary, config.Failover{}}, claude, codex)
	built := 0
	inner := pipeline.ReviewerFor
	pipeline.ReviewerFor = func(endpoint runstate.RoutedEndpoint) (ChangeReviewer, bool) {
		built++
		return inner(endpoint)
	}

	outcome, err := pipeline.Run(context.Background(), f.tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || built != 0 || len(claude.requests()) != 0 || len(codex.requests()) != 0 {
		t.Fatalf("outcome = %#v, routed reviewers built %d; want the existing reviewer to have judged it", outcome, built)
	}
	state, err := f.store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Routing != nil && (state.Routing.Reviewer != nil || len(reviewOperations(state.Routing)) != 0) {
		t.Fatalf("routing = %#v, want no reviewer routing recorded", state.Routing)
	}
	if state.ReviewRounds != 1 {
		t.Fatalf("review rounds = %d, want one", state.ReviewRounds)
	}
}

func (p *reviewProvider) Capabilities() backend.Capabilities {
	return backend.Capabilities{StructuredEvents: true}
}
