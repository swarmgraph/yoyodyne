package chat

import (
	"context"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A conversation turn asks for the agent's effort level, and the conversation's
// record, its evidence, and the turn's cost line all say what was asked.
func TestAConversationTurnAsksForTheAgentsEffortAndRecordsIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	log := &recordingSpendLog{}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Noted.", CostUSD: 0.02, CostReported: true},
	}}
	options := testOptions(t, provider)
	options.Store = newTestStore(t, root)
	options.Spend = log
	options.Effort = "medium"

	reply, err := openTestSession(t, options).Send(context.Background(), "what is next?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(provider.requests) != 1 || provider.requests[0].Effort != "medium" {
		t.Fatalf("requests = %#v, want one at medium", provider.requests)
	}
	if reply.Evidence.Effort != "medium" {
		t.Fatalf("evidence effort = %q, want medium", reply.Evidence.Effort)
	}
	if len(log.lines) != 1 || log.lines[0].Effort != "medium" {
		t.Fatalf("cost lines = %#v, want the turn's line recording medium", log.lines)
	}
	recorded, err := options.Store.Load(runstate.ConversationIdentity{Agent: options.Agent, Role: options.Role})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.ProviderEffort != "medium" {
		t.Fatalf("recorded effort = %q, want medium", recorded.ProviderEffort)
	}
}

// A turn that crosses onto another provider keeps the agent's level where that
// provider accepts it, and is asked with none where it accepts none -- and the
// conversation's record then says none was asked, rather than the level the
// agent configured.
func TestACrossedTurnKeepsTheEffortOnBothAdapters(t *testing.T) {
	t.Parallel()

	for _, adapter := range []domain.Backend{domain.BackendClaudeCode, domain.BackendCodex} {
		resetsAt := time.Date(2026, 9, 7, 11, 0, 0, 0, time.UTC)
		held := &speakingBackend{results: []backendapi.RunResult{
			{IsError: true, StopReason: "usage_limit", UsageLimit: &backendapi.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt}},
		}}
		crossed := &speakingBackend{results: []backendapi.RunResult{
			{SessionID: "second-session-1", FinalText: "The second one first."},
		}}
		options := crossingOptions(t, held, crossed)
		options.Providers = effortProviderRegistry(t, adapter)
		options.UsageLimits = newTestUsageLimits(t)
		options.Effort = "high"
		if adapter == domain.BackendCodex {
			options.FailoverModel = "gpt-6-astra"
			options.FailoverEndpoint.Model = "gpt-6-astra"
		}

		if _, err := openTestSession(t, options).Send(context.Background(), "what now?"); err != nil {
			t.Fatalf("%s: Send() error = %v, want the turn served on the other provider", adapter, err)
		}
		want := "high"

		if len(held.requests) != 1 || held.requests[0].Effort != "high" {
			t.Fatalf("%s: held requests = %#v, want the refused attempt at high", adapter, held.requests)
		}
		if len(crossed.requests) != 1 || crossed.requests[0].Effort != want {
			t.Fatalf("%s: crossed requests = %#v, want one at %q", adapter, crossed.requests, want)
		}
		recorded, err := options.Store.Load(runstate.ConversationIdentity{Agent: options.Agent, Role: options.Role})
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if recorded.ProviderEffort != want {
			t.Fatalf("%s: recorded effort = %q, want %q", adapter, recorded.ProviderEffort, want)
		}
	}
}

func TestCrossedTurnEvidenceUsesTheRequestedEffortOfTheServingInvocation(t *testing.T) {
	t.Parallel()
	held := &speakingBackend{results: []backendapi.RunResult{{IsError: true, UsageLimit: &backendapi.UsageLimit{Kind: "window", ResetsAt: time.Now().Add(time.Hour)}}}}
	crossed := &speakingBackend{results: []backendapi.RunResult{{SessionID: "second-session", FinalText: "Done."}}}
	options := crossingOptions(t, held, crossed)
	options.Provider, options.Model, options.Effort = domain.BackendCodex, "gpt-6-astra", "ultra"
	options.UsageLimits = newTestUsageLimits(t)
	options.Providers = effortProviderRegistry(t, domain.BackendClaudeCode)
	reply, err := openTestSession(t, options).Send(context.Background(), "continue")
	if err != nil {
		t.Fatal(err)
	}
	if len(crossed.requests) != 1 || crossed.requests[0].Effort != "" || reply.Evidence.Effort != "" {
		t.Fatalf("the sweep's turn evidence repeated the configured ultra: requests=%+v evidence=%+v", crossed.requests, reply.Evidence)
	}
}

// effortProviderRegistry is the second provider of crossingOptions, launched by
// the named adapter, whose levels are the ones it inherits.
func effortProviderRegistry(t *testing.T, adapter domain.Backend) *backendapi.Registry {
	t.Helper()

	registry, err := backendapi.NewRegistry(map[domain.Backend]backendapi.ProviderPlugin{
		"second-provider": {
			Adapter: adapter,
			Roles: []domain.AgentRole{
				domain.RoleProductManager, domain.RoleArchitect, domain.RoleDevelopmentManager,
				domain.RoleDeveloper, domain.RoleReviewer,
			},
			Postures: []backendapi.Posture{backendapi.PostureReadOnly, backendapi.PostureWorktreeWrite},
			Dialect: backendapi.DialectSpec{Rules: []backendapi.DialectRule{
				{Answer: backendapi.AnswerRefused, Terminal: truth(true), Failed: truth(true)},
			}},
		},
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return registry
}

func TestCodexConversationRecordsExplicitOrConfiguredEffortPerTurn(t *testing.T) {
	t.Parallel()
	for _, effort := range []string{"", "high"} {
		t.Run("effort="+effort, func(t *testing.T) {
			descriptor, _ := backendapi.BuiltInDescriptor(domain.BackendCodex)
			descriptions := []string{
				descriptor.DescribeInvocationEffort(effort, "high", true),
				descriptor.DescribeInvocationEffort(effort, "", false),
			}
			provider := &fakeBackend{results: []backendapi.RunResult{
				{Backend: domain.BackendCodex, SessionID: "codex-session", FinalText: "First.", ResolvedEffort: "high", EffortReported: true, EffortDescription: descriptions[0]},
				{Backend: domain.BackendCodex, SessionID: "codex-session", FinalText: "Second.", EffortDescription: descriptions[1]},
			}}
			log := &recordingSpendLog{}
			options := testOptions(t, provider)
			options.Provider, options.Model, options.Effort = domain.BackendCodex, "gpt-6.1-sol", effort
			options.Spend = log
			session := openTestSession(t, options)
			for index := 0; index < 2; index++ {
				reply, err := session.Send(context.Background(), "continue")
				if err != nil {
					t.Fatal(err)
				}
				if reply.Evidence.Effort != effort || reply.Evidence.EffortReported != (index == 0) || reply.Evidence.EffortDescription != descriptions[index] {
					t.Fatalf("turn %d evidence = %+v", index, reply.Evidence)
				}
				recorded, err := options.Store.Load(runstate.ConversationIdentity{Agent: options.Agent, Role: options.Role})
				if err != nil {
					t.Fatal(err)
				}
				if recorded.ProviderEffort != effort || recorded.ProviderEffortReported != (index == 0) || recorded.ProviderEffortDescription != descriptions[index] || log.lines[index].EffortDescription != descriptions[index] {
					t.Fatalf("turn %d lost the effort source: record=%+v, cost=%+v", index, recorded, log.lines[index])
				}
				want := ""
				if index == 0 {
					want = "high"
				}
				if recorded.ProviderResolvedEffort != want || log.lines[index].ResolvedEffort != want || log.lines[index].EffortReported != (index == 0) || log.lines[index].Effort != effort {
					t.Fatalf("turn %d effort was guessed or lost: record=%+v, cost=%+v", index, recorded, log.lines[index])
				}
			}
			if len(provider.requests) != 2 || provider.requests[0].Effort != effort || provider.requests[1].Effort != effort || provider.requests[0].SessionID != "" || provider.requests[1].SessionID != "codex-session" {
				t.Fatalf("initial and resumed requests = %+v", provider.requests)
			}
		})
	}
}
