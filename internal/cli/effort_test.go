package cli

// Every kind of turn an agent takes as itself asks for that agent's effort
// level, and what it records says so. A run's developer and reviewer
// invocations are held to the same in internal/orchestrator, a branch review in
// internal/orchestrator's branch review, and a conversation's own turn and its
// cost line in internal/chat; this holds the invocations wired here: an
// answering round, a side thread, and a recurring pass -- which is also how a
// program manager instance's pass is taken.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// effortAnsweringConfig is the answering configuration with the architect
// naming a level, which is the only difference between the two.
func effortAnsweringConfig() config.Config {
	cfg := answeringConfig()
	architect := cfg.Agents["architect"]
	architect.Effort = "high"
	cfg.Agents["architect"] = architect
	return cfg
}

func TestAnAnsweringRoundAsksForTheAgentsEffortAndRecordsIt(t *testing.T) {
	t.Parallel()

	provider := &sequencingBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "It costs a week."},
	}}
	costs := &recordedSpend{}
	voice := exchangeVoice{
		config:      effortAnsweringConfig(),
		provider:    provider,
		repository:  t.TempDir(),
		stateRoot:   t.TempDir(),
		usageLimits: newTestExchangeUsageLimits(t),
		productID:   "yoyodyne",
		clock:       exchangeFixedNow,
		spend:       costs,
	}
	spoken, err := voice.Answer(context.Background(), answeringQuestion())
	if err != nil {
		t.Fatalf("Answer() error = %v", err)
	}
	if provider.calls != 1 || provider.requests[0].Effort != "high" {
		t.Fatalf("requests = %#v, want one at the architect's high", provider.requests)
	}
	if spoken.Effort != "high" {
		t.Fatalf("spoken effort = %q, want the level the round asked for recorded on the exchange", spoken.Effort)
	}
	if len(costs.lines) != 1 || costs.lines[0].Effort != "high" {
		t.Fatalf("cost lines = %#v, want one recording high", costs.lines)
	}
}

func TestASideTurnAsksForTheAgentsEffortAndRecordsIt(t *testing.T) {
	t.Parallel()

	provider := &capturingBackend{result: backendapi.RunResult{SessionID: "session-side-2", FinalText: "It does not."}}
	costs := &recordedSpend{}
	voice := sideVoice{
		config:     effortAnsweringConfig(),
		provider:   provider,
		repository: t.TempDir(),
		productID:  "yoyodyne",
		spend:      costs,
	}
	spoken, err := voice.Answer(context.Background(), testSideQuestion())
	if err != nil {
		t.Fatalf("Answer() error = %v", err)
	}
	if provider.request.Effort != "high" {
		t.Fatalf("side turn asked at %q, want the architect's high", provider.request.Effort)
	}
	if spoken.Effort != "high" {
		t.Fatalf("spoken effort = %q, want the level recorded on the side thread", spoken.Effort)
	}
	if len(costs.lines) != 1 || costs.lines[0].Effort != "high" {
		t.Fatalf("cost lines = %#v, want one recording high", costs.lines)
	}
}

// effortRecordingBackend answers every turn with a finished sweep and records
// the effort each turn asked for.
type effortRecordingBackend struct {
	requests []backendapi.RunRequest
}

func (b *effortRecordingBackend) Run(_ context.Context, request backendapi.RunRequest) (backendapi.RunResult, error) {
	b.requests = append(b.requests, request)
	return backendapi.RunResult{
		Backend:   domain.BackendClaudeCode,
		SessionID: "session-1",
		FinalText: "Looked.\n\n```yoyodyne-sweep\n" + `{"status":"complete","summary":"nothing unresolved"}` + "\n```\n",
	}, nil
}

// A recurring pass on a model of the task's own keeps the agent's level, the
// pass's record names it, and the conversation it was taken in records it.
func TestARecurringPassKeepsTheAgentsEffortOnTheTasksModel(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := config.Config{
		Agents: map[string]config.AgentConfig{
			"development-manager": {Role: domain.RoleDevelopmentManager, Backend: domain.BackendClaudeCode, Model: "fable", Effort: "low"},
		},
		RecurringTasks: map[string]config.RecurringTask{
			"development-manager-sweep": {
				Role:    domain.RoleDevelopmentManager,
				Every:   config.Duration(time.Hour),
				Enabled: true,
				Prompt:  "sweep for unresolved issues",
				Model:   "sonnet",
			},
		},
	}
	conversations, err := runstate.NewConversationStore(root, "example")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	provider := &effortRecordingBackend{}
	open := func(_ context.Context, role domain.AgentRole, _, model string, _ orchestrator.RecurringTurnOptions) (*chat.Session, *runstate.ConversationHold, *runstate.SweepConversationReplacement, error) {
		prepared := preparedChat{
			parts:    components{config: cfg},
			name:     "development-manager",
			agent:    cfg.Agents["development-manager"],
			identity: runstate.ConversationIdentity{Agent: "development-manager", Role: role},
		}
		if err := prepared.onModel(model); err != nil {
			return nil, nil, nil, err
		}
		session, err := chat.Open(chat.Options{
			Role:         role,
			Agent:        prepared.name,
			Backend:      provider,
			Store:        conversations,
			Model:        prepared.requestedModel(),
			ModelVersion: prepared.modelVersion(),
			Effort:       prepared.effort(),
			Provider:     domain.BackendClaudeCode,
			AccountAlias: config.DefaultAccountAlias,
			Repository:   filepath.Join(root, "repository"),
			ProductID:    "example",
			RepositoryID: "example",
			Briefing:     chat.Briefing{Text: "the product is a harness", GatheredAt: time.Now().UTC()},
		})
		return session, nil, nil, err
	}
	sweeps, err := runstate.NewSweepStore(root, "example")
	if err != nil {
		t.Fatalf("NewSweepStore() error = %v", err)
	}
	fired, err := orchestrator.Trigger{
		Tasks:   cfg.RecurringTasks,
		Claims:  sweeps,
		Reports: sweeps,
		Roles:   roleConversation{open: open},
	}.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(provider.requests) != 1 || provider.requests[0].Model != "sonnet" || provider.requests[0].Effort != "low" {
		t.Fatalf("requests = %#v, want the pass on sonnet at the agent's low", provider.requests)
	}
	if len(fired.Fired) != 1 || fired.Fired[0].Effort != "low" {
		t.Fatalf("fired = %+v, want the pass's effort carried back", fired.Fired)
	}
	recorded, _, err := sweeps.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 || recorded[0].Effort != "low" {
		t.Fatalf("sweep records = %+v, want one naming the level its pass asked for", recorded)
	}
	conversation, err := conversations.Load(runstate.ConversationIdentity{Agent: "development-manager", Role: domain.RoleDevelopmentManager})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if conversation.ProviderEffort != "low" {
		t.Fatalf("conversation effort = %q, want the level the turn asked for", conversation.ProviderEffort)
	}
}

// `yoyo agent list` says the level beside the model, and says nothing of one
// for an agent that names none.
func TestTheAgentListingSaysTheEffortBesideTheModel(t *testing.T) {
	t.Parallel()

	named := renderAgent(agentReport{Name: "architect", Role: domain.RoleArchitect, Backend: domain.BackendClaudeCode, Model: "opus", Effort: "medium", Instances: 1})
	if !strings.Contains(named, "model opus at medium effort,") {
		t.Fatalf("listing = %q, want the level beside the model", named)
	}
	unnamed := renderAgent(agentReport{Name: "architect", Role: domain.RoleArchitect, Backend: domain.BackendClaudeCode, Model: "opus", Instances: 1})
	if strings.Contains(unnamed, "effort") {
		t.Fatalf("listing = %q, want nothing said of a level the agent does not name", unnamed)
	}
}

// The reviewer a branch review is wired with carries the reviewer agent's level,
// and a shadow review's overriding model moves the model and not the level.
func TestABranchReviewIsWiredWithTheReviewersEffort(t *testing.T) {
	t.Parallel()

	cfg := config.Config{Agents: map[string]config.AgentConfig{
		"reviewer": {Role: domain.RoleReviewer, Backend: domain.BackendClaudeCode, Model: "opus", Effort: "xhigh"},
	}}
	for _, model := range []string{"", "sonnet"} {
		wired, ok := branchReviewerFrom(components{config: cfg}, model).Reviewer.(review.Reviewer)
		if !ok {
			t.Fatal("the branch review is not wired with the harness's reviewer")
		}
		if wired.Effort != "xhigh" {
			t.Fatalf("model override %q: reviewer effort = %q, want the reviewer agent's xhigh", model, wired.Effort)
		}
	}
}
