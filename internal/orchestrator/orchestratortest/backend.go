package orchestratortest

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/selfcheck"
)

var _ backend.Backend = (*Backend)(nil)

// Backend is the provider with the agent taken out. Respond answers each
// invocation, and Requests is every invocation it was asked for, in order.
type Backend struct {
	ReportedAvailability backend.Availability
	Respond              func(backend.RunRequest) (backend.RunResult, error)
	Requests             []backend.RunRequest
	// Session identities are configurable so a test can prove that missing or
	// reused provider identity never reaches integration.
	DeveloperSession string
	ReviewerSession  string
	// DeveloperFinalText replaces what a served developer attempt says about its
	// work, which is what a test that cares about the summary itself sets.
	DeveloperFinalText string
	// DeveloperRecordsNoExecution makes the served developer reply exactly what
	// the test wrote, with no verification record added to it. It is for the
	// tests about the execution-evidence gate itself, which need a reply that
	// records nothing.
	DeveloperRecordsNoExecution bool
	// DeveloperFinalTextByAttempt says it per attempt instead, for a test where
	// what the developer says has to change between the first attempt and the
	// repair. The last entry repeats once the list runs out, the way the review
	// verdicts do.
	DeveloperFinalTextByAttempt []string
	// DeveloperAfterReplyByAttempt says, per attempt, what became of work the
	// developer's session left running after its final reply, as the runner
	// reports it; nil is a session that exited with its reply. Unlike the texts
	// above, an attempt past the end of the list left nothing running.
	DeveloperAfterReplyByAttempt []*execution.AfterReply
	DeveloperAttempts            int
}

func (f *Backend) CheckAvailability(context.Context) (backend.Availability, error) {
	if !f.ReportedAvailability.Installed && !f.ReportedAvailability.Authenticated && f.ReportedAvailability.AuthMethod == "" {
		return backend.Availability{Installed: true, Authenticated: true}, nil
	}
	return f.ReportedAvailability, nil
}

func (*Backend) Capabilities() backend.Capabilities {
	return backend.Capabilities{StructuredEvents: true}
}

func (f *Backend) Run(_ context.Context, request backend.RunRequest) (backend.RunResult, error) {
	f.Requests = append(f.Requests, request)
	result, err := f.Respond(request)
	// A developer that recorded nothing it executed is refused before its change
	// reaches a reviewer, so every fake developer here carries the record its
	// contract asks for unless its own test is about the absence. It is added
	// where every fake passes rather than in each of them, so a test written next
	// year inherits it instead of a copy of it.
	if request.Role == domain.RoleDeveloper && !f.DeveloperRecordsNoExecution {
		result.FinalText = WithVerification(result.FinalText)
	}
	return result, err
}

func (f *Backend) RequestsForRole(role domain.AgentRole) []backend.RunRequest {
	var matching []backend.RunRequest
	for _, request := range f.Requests {
		if request.Role == role {
			matching = append(matching, request)
		}
	}
	return matching
}

// passingVerification is the record of its own executions a developer's contract
// asks every reply to carry: the probe it ran before it changed anything, and a
// check it ran against the change. Every fake developer below carries one unless
// its test is about the absence, because a run whose developer records nothing
// is refused before it reaches a reviewer — which is the gate rather than an
// accident of these doubles.
const passingVerification = "\n\n" + selfcheck.Fence + "\n" +
	`{"probe":{"command":"make build","outcome":"passed"},"checks":[{"command":"make test","outcome":"passed"}]}` + "\n```"

// WithVerification adds that record to a reply that does not already write one
// of its own, so a test about anything else does not have to.
func WithVerification(reply string) string {
	if strings.Contains(reply, selfcheck.Fence) {
		return reply
	}
	return reply + passingVerification
}

// RoleBackend serves the developer and the reviewer from one fake provider, so
// a test can prove the two invocations are actually distinct rather than
// assuming it from separate doubles. The reviewer answers with each verdict in
// turn and repeats the last one, which is what lets a test drive a repair loop
// to a chosen outcome.
func RoleBackend(develop func(backend.RunRequest) error, verdicts ...string) *Backend {
	provider := &Backend{DeveloperSession: "developer-session", ReviewerSession: "reviewer-session"}
	reviews := 0
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		switch request.Role {
		case domain.RoleDeveloper:
			if err := develop(request); err != nil {
				return backend.RunResult{}, err
			}
			finalText := "implemented the work item"
			if provider.DeveloperFinalText != "" {
				finalText = provider.DeveloperFinalText
			}
			if texts := provider.DeveloperFinalTextByAttempt; len(texts) > 0 {
				finalText = texts[min(provider.DeveloperAttempts, len(texts)-1)]
			}
			process := execution.ProcessResult{Status: execution.ProcessSucceeded}
			if attempt := provider.DeveloperAttempts; attempt < len(provider.DeveloperAfterReplyByAttempt) {
				process.AfterReply = provider.DeveloperAfterReplyByAttempt[attempt]
			}
			provider.DeveloperAttempts++
			return backend.RunResult{
				Backend:       domain.BackendClaudeCode,
				SessionID:     provider.DeveloperSession,
				ResolvedModel: DeveloperResolved,
				FinalText:     finalText,
				Process:       process,
				LastEvent:     request.LastSequence,
			}, nil
		case domain.RoleReviewer:
			verdict := verdicts[len(verdicts)-1]
			if reviews < len(verdicts) {
				verdict = verdicts[reviews]
			}
			reviews++
			return backend.RunResult{
				Backend:       domain.BackendClaudeCode,
				SessionID:     provider.ReviewerSession,
				ResolvedModel: ReviewerResolved,
				FinalText:     verdict,
				Process:       execution.ProcessResult{Status: execution.ProcessSucceeded},
				LastEvent:     request.LastSequence,
			}, nil
		default:
			return backend.RunResult{}, fmt.Errorf("unexpected role %q", request.Role)
		}
	}
	return provider
}

// RequestsMade is every invocation this backend served, in order.
func (f *Backend) RequestsMade() []backend.RunRequest {
	return f.Requests
}

// DeveloperSessionID is the session this backend reports serving the developer.
func (f *Backend) DeveloperSessionID() string {
	return f.DeveloperSession
}
