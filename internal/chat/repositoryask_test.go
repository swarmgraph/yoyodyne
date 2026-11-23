package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/repositoryask"
)

// fakeRepositoryAsker answers with the session it was handed, so the test sees
// the helper's invocation go through the conversation's meter.
type fakeRepositoryAsker struct {
	asked    []repositoryask.Request
	sessions []repositoryask.Session
}

func (f *fakeRepositoryAsker) Ask(ctx context.Context, request repositoryask.Request, session repositoryask.Session) repositoryask.Answer {
	f.asked = append(f.asked, request)
	f.sessions = append(f.sessions, session)
	result, err := session.Provider.Run(ctx, backendapi.RunRequest{RunID: session.RunID, Role: session.Role, Model: session.Model, AllowedTools: []string{}})
	if err != nil {
		return repositoryask.Answer{Question: request.Question, Problem: err.Error()}
	}
	if result.IsError {
		return repositoryask.Answer{Question: request.Question, Problem: "the helper's session ended in an error"}
	}
	return repositoryask.Answer{
		Question:  request.Question,
		Paths:     request.Paths,
		Commit:    "0123456789abcdef0123456789abcdef01234567",
		Model:     session.Model,
		Handed:    3,
		Citations: []repositoryask.Citation{{Path: "docs/product/operating-rules.md", Start: 12, End: 14}},
		Text:      "The rule on sizing work items is on lines 12 to 14.",
	}
}

const repositoryAskReply = "Let me find where that rule is.\n\n" + repositoryask.Fence + "\n" +
	`{"question":"Where do the operating rules say how large a work item should be?","paths":["docs/product"]}` + "\n```"

// The Lead Product Manager asks the reading helper; the harness runs the
// helper's session on the lightest model through this conversation's meter, so
// its cost is a line against her role and this conversation, and the answer
// reaches her next turn framed as untrusted evidence and a pointer.
func TestAConversationRoleAsksTheReadingHelperAndItsCostIsHers(t *testing.T) {
	t.Parallel()

	log := &recordingSpendLog{}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: repositoryAskReply, CostUSD: 0.40, CostReported: true},
		{SessionID: "helper-1", FinalText: "{}", CostUSD: 0.01, CostReported: true},
		{SessionID: "session-1", FinalText: "The sizing rule is on lines 12 to 14; I read it and it says 300 to 600 lines.", CostUSD: 0.42, CostReported: true},
	}}
	asker := &fakeRepositoryAsker{}
	options := testOptions(t, provider)
	options.Spend = log
	options.RepositoryAsk = asker
	options.RepositoryAskModel = "haiku"
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "How big should a work item be?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(asker.asked) != 1 || asker.asked[0].Paths[0] != "docs/product" {
		t.Fatalf("the helper was asked %#v", asker.asked)
	}
	if got := asker.sessions[0]; got.Model != "haiku" || got.Role != domain.RoleProductManager || got.RunID != session.Evidence().ConversationID {
		t.Fatalf("the helper's session was %+v, want the lightest model, the asking role and this conversation", got)
	}
	if len(provider.requests) != 3 || provider.requests[1].Model != "haiku" {
		t.Fatalf("requests = %d, the helper's on %q; want three with the helper's on haiku", len(provider.requests), provider.requests[1].Model)
	}
	if len(log.lines) != 3 {
		t.Fatalf("cost lines = %d, want one for each turn and one for the helper", len(log.lines))
	}
	helper := log.lines[1]
	if helper.Role != domain.RoleProductManager || helper.Model != "haiku" || helper.ConversationID != session.Evidence().ConversationID || !helper.Known() || helper.AmountUSD != 0.01 {
		t.Fatalf("the helper's cost line = %#v, want it against the asking role and this conversation", helper)
	}
	continuation := provider.requests[2].Prompt
	for _, want := range []string{"# Answer from the reading helper", "docs/product/operating-rules.md lines 12 to 14", "untrusted evidence", "read that passage yourself", "says nothing about what the product is for"} {
		if !strings.Contains(continuation, want) {
			t.Fatalf("the continuation lacks %q:\n%s", want, continuation)
		}
	}
	if len(reply.RepositoryAsks) != 1 || reply.RepositoryAsks[0].Problem != "" {
		t.Fatalf("reply repository asks = %#v", reply.RepositoryAsks)
	}
}

// A role whose bundle does not name repository.ask is refused the block, and
// nothing is asked or spent on it.
func TestARoleWithoutTheGrantIsRefusedTheReadingHelper(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: repositoryAskReply},
		{SessionID: "session-1", FinalText: "Understood."},
	}}
	asker := &fakeRepositoryAsker{}
	options := testOptions(t, provider)
	options.Role = domain.RoleDevelopmentManager
	options.Agent = string(domain.RoleDevelopmentManager)
	options.RepositoryAsk = asker
	options.RepositoryAskModel = "haiku"
	_, err := openTestSession(t, options).Send(context.Background(), "How big should a work item be?")
	var refusal *AuthorityError
	if err != nil && !errors.As(err, &refusal) {
		t.Fatalf("Send() error = %v", err)
	}
	if len(asker.asked) != 0 {
		t.Fatalf("a role without the grant had the helper asked %#v", asker.asked)
	}
	if !authorities[domain.RoleArchitect].RepositoryAsk || !authorities[domain.RoleProgramManager].RepositoryAsk || !authorities[domain.RoleProductManager].RepositoryAsk {
		t.Fatal("the architect, the program manager and the Lead Product Manager must each hold the reading helper")
	}
	if authorities[domain.RoleDevelopmentManager].RepositoryAsk {
		t.Fatal("the development manager holds the reading helper, which the design does not give her")
	}
}

// A provider that names no lightest model has the helper off: the role is told
// why, and no session is started.
func TestTheReadingHelperIsOffWhereTheProviderNamesNoLightestModel(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: repositoryAskReply},
		{SessionID: "session-1", FinalText: "I could not ask; I will read the file instead."},
	}}
	options := testOptions(t, provider)
	options.RepositoryAsk = repositoryask.Asker{}
	reply, err := openTestSession(t, options).Send(context.Background(), "How big should a work item be?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("requests = %d, want the two turns and no helper session", len(provider.requests))
	}
	if len(reply.RepositoryAsks) != 1 || !strings.Contains(reply.RepositoryAsks[0].Problem, "names no lightest model") {
		t.Fatalf("reply repository asks = %#v", reply.RepositoryAsks)
	}
	if !strings.Contains(provider.requests[1].Prompt, "Your question was not answered") {
		t.Fatalf("the role was not told:\n%s", provider.requests[1].Prompt)
	}
}

// An exhausted usage limit met by the helper's session is recorded against the
// asking conversation, as one met by the conversation's own turn is, and the
// role is told its question went unanswered.
func TestAnExhaustedLimitMetByTheReadingHelperIsRecorded(t *testing.T) {
	t.Parallel()

	resetsAt := time.Date(2026, 11, 23, 11, 0, 0, 0, time.UTC)
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: repositoryAskReply},
		{IsError: true, StopReason: "usage_limit", UsageLimit: &backendapi.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt}},
		{SessionID: "session-1", FinalText: "I will read the rules myself."},
	}}
	limits := newTestUsageLimits(t)
	options := testOptions(t, provider)
	options.UsageLimits = limits
	options.RepositoryAsk = &fakeRepositoryAsker{}
	options.RepositoryAskModel = "haiku"
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "How big should a work item be?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	recorded, err := limits.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 1 || recorded[0].ConversationID != session.Evidence().ConversationID || recorded[0].Model != "haiku" || recorded[0].Kind != "five_hour" {
		t.Fatalf("recorded limits = %#v, want the helper's refusal against this conversation", recorded)
	}
	if len(reply.RepositoryAsks) != 1 || reply.RepositoryAsks[0].Problem == "" {
		t.Fatalf("reply repository asks = %#v, want the ask unanswered", reply.RepositoryAsks)
	}
}
