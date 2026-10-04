package cli

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
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// modelRecordingBackend is a provider that answers every turn with a finished
// sweep and records the model each turn asked it for, which is the whole of what
// the selection is judged by: the selector that reached the provider.
type modelRecordingBackend struct {
	models []string
}

func (b *modelRecordingBackend) Run(_ context.Context, request backendapi.RunRequest) (backendapi.RunResult, error) {
	b.models = append(b.models, request.Model)
	return backendapi.RunResult{
		Backend:      domain.BackendClaudeCode,
		SessionID:    "session-1",
		FinalText:    "Looked.\n\n```yoyodyne-sweep\n" + `{"status":"complete","summary":"nothing unresolved"}` + "\n```\n",
		CostUSD:      0.12,
		CostReported: true,
	}, nil
}

// A recurring task that names its own model has its pass served on that model,
// over the role's own conversation, and the pass's record names it; a turn the
// task does not cover — the operator's next message in the same conversation —
// asks for the role's configured model again.
//
// The conversation is opened the way Wake opens it, through the same model
// resolution openChatOnModel applies, over a provider that records what it was
// asked for. Nothing between the configuration and the provider's request line is
// replaced but the provider itself.
func TestARecurringTaskRunsOnItsOwnModelAndDecisionTurnsStayOnTheRoles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := config.Config{
		Agents: map[string]config.AgentConfig{
			"development-manager": {Role: domain.RoleDevelopmentManager, Backend: domain.BackendClaudeCode, Model: "fable"},
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
	provider := &modelRecordingBackend{}
	// open is openChatOnModel's model resolution over a fake provider: the agent
	// the role resolves to, the task's model laid over it, and the session opened
	// on what that resolution asks for.
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
	trigger := orchestrator.Trigger{
		Tasks:   cfg.RecurringTasks,
		Claims:  sweeps,
		Reports: sweeps,
		Roles:   roleConversation{open: open},
	}
	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(fired.Fired) != 1 || fired.Fired[0].Turns != 1 {
		t.Fatalf("fired = %+v, want the one due task answered in one turn", fired.Fired)
	}
	if len(provider.models) != 1 || provider.models[0] != "sonnet" {
		t.Fatalf("the pass asked the provider for %v, want the task's own sonnet", provider.models)
	}

	recorded, _, err := sweeps.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 || recorded[0].Model != "sonnet" {
		t.Fatalf("sweep records = %+v, want one naming the model its pass ran on", recorded)
	}
	if fired.Fired[0].Model != "sonnet" {
		t.Errorf("fired model = %q, want sonnet", fired.Fired[0].Model)
	}

	// The operator's next message into the same conversation is a decision turn
	// the task does not cover, so it asks for the role's own model.
	session, _, _, err := open(context.Background(), domain.RoleDevelopmentManager, "", "", orchestrator.RecurringTurnOptions{})
	if err != nil {
		t.Fatalf("open() error = %v", err)
	}
	if !session.Resumed() {
		t.Error("the operator's turn opened a new conversation, want the one the pass was taken in")
	}
	if _, err := session.Send(context.Background(), "what did the sweep find?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(provider.models) != 2 || provider.models[1] != "fable" {
		t.Fatalf("the provider was asked for %v, want the operator's turn on the role's fable", provider.models)
	}
}

// A task's model reaches the provider's command line exactly as an agent's does,
// so the wake refuses one that could not name a model before anything is asked,
// and reports it as a firing that never reached the role.
func TestAWakeRefusesAModelThatCouldNotNameOne(t *testing.T) {
	t.Parallel()

	prepared := preparedChat{name: "development-manager", identity: runstate.ConversationIdentity{Role: domain.RoleDevelopmentManager}}
	if err := prepared.onModel("--dangerously"); err == nil {
		t.Fatal("onModel() accepted a flag as a model selector")
	}
	if err := prepared.onModel("  "); err != nil || prepared.requestedModel() != "" {
		t.Fatalf("onModel(blank) = %v, requested %q; want the agent's own model left in charge", err, prepared.requestedModel())
	}
}

// A pin is a version of the agent's own family, so a turn asking for another
// model carries none, and a turn asking for the agent's own model keeps it.
func TestATaskOnAnotherModelCarriesNoPin(t *testing.T) {
	t.Parallel()

	cfg := config.Config{Agents: map[string]config.AgentConfig{
		"development-manager": {Role: domain.RoleDevelopmentManager, Model: "fable", ModelVersion: "claude-fable-5-1"},
	}}
	prepared := preparedChat{parts: components{config: cfg}, name: "development-manager", agent: cfg.Agents["development-manager"]}
	if prepared.modelVersion() != "claude-fable-5-1" {
		t.Errorf("version = %q, want the agent's pin on its own turns", prepared.modelVersion())
	}
	if err := prepared.onModel("sonnet"); err != nil {
		t.Fatalf("onModel() error = %v", err)
	}
	if prepared.requestedModel() != "sonnet" || prepared.modelVersion() != "" {
		t.Errorf("requested %q version %q, want sonnet with no fable pin", prepared.requestedModel(), prepared.modelVersion())
	}
	same := preparedChat{parts: components{config: cfg}, name: "development-manager", agent: cfg.Agents["development-manager"]}
	if err := same.onModel("fable"); err != nil {
		t.Fatalf("onModel() error = %v", err)
	}
	if same.modelVersion() != "claude-fable-5-1" {
		t.Errorf("version = %q, want the pin kept for a task naming the agent's own model", same.modelVersion())
	}
}

// `yoyo status --spend` attributes what the recurring tasks cost to the model
// each pass ran on, as a split of the conversations it already prices rather
// than as spend in addition to them.
func TestSpendAttributesSweepCostToTheTasksModel(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	now := time.Now()
	recordStreamConversation(t, stateRoot, now.Add(-2*time.Hour), []time.Time{now.Add(-2 * time.Hour), now.Add(-time.Hour)})
	sweeps, err := runstate.NewSweepStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewSweepStore() error = %v", err)
	}
	for _, pass := range []runstate.Sweep{
		sweepOn(now.Add(-2*time.Hour), "sonnet", 0.25),
		sweepOn(now.Add(-time.Hour), "sonnet", 0.5),
		sweepOn(now.Add(-30*time.Minute), "fable", 1.5),
	} {
		if err := sweeps.Append(pass); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	stdout, stderr, code := runCLI(t, "status", "--spend", "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "recurring tasks, by the model each pass ran on") {
		t.Fatalf("stdout = %q, want the recurring tasks' split", stdout)
	}
	rows := map[string]string{}
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 5 && fields[0] == "development-manager-sweep" {
			rows[fields[1]] = strings.Join(fields[2:], " ")
		}
	}
	if rows["sonnet"] != "2 2 $0.75" || rows["fable"] != "1 1 $1.50" {
		t.Errorf("sweep rows = %v, want two passes at $0.75 on sonnet and one at $1.50 on fable; stdout = %q", rows, stdout)
	}

	// A report of the runs alone has no pass in it to attribute.
	stdout, _, _ = runCLI(t, "status", "--spend", "--kind", "runs", "--config", configPath)
	if strings.Contains(stdout, "recurring tasks") {
		t.Errorf("a runs-only report carries the recurring tasks' split: %q", stdout)
	}
}

func sweepOn(at time.Time, model string, cost float64) runstate.Sweep {
	return runstate.Sweep{
		SchemaVersion: runstate.SweepSchemaVersion,
		ProductID:     "yoyodyne",
		Task:          "development-manager-sweep",
		Role:          domain.RoleDevelopmentManager,
		StartedAt:     at,
		EndedAt:       at.Add(time.Minute),
		Turns:         1,
		CostUSD:       cost,
		Model:         model,
		Problem:       "no account in this fixture",
	}
}
