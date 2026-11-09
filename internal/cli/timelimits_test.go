package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
)

// The figures an agent stating nothing is held to are the configuration's, and
// they are the figures these places used before the keys existed.
func TestTheExchangeAndSideThreadDefaultsAreTheConfigurationsDefaults(t *testing.T) {
	t.Parallel()

	if exchangeAnswerTimeout != config.DefaultExchangeAnswerTimeLimit {
		t.Errorf("exchange answer default = %s, want 5m and the configuration's default", exchangeAnswerTimeout)
	}
	if sideTurnTimeout != config.DefaultSideThreadTurnTimeLimit {
		t.Errorf("side-thread turn default = %s, want 5m and the configuration's default", sideTurnTimeout)
	}
	if chat.DefaultTurnTimeout != config.DefaultTurnTimeLimit {
		t.Errorf("chat turn default = %s, configuration default = %s, want them equal", chat.DefaultTurnTimeout, config.DefaultTurnTimeLimit)
	}
}

func TestAnExchangeAnswerAndASideTurnTakeTheAnsweringAgentsOwnLimits(t *testing.T) {
	t.Parallel()

	cfg := answeringConfig()
	architect := cfg.Agents["architect"]
	architect.TimeLimits = config.TimeLimits{
		ExchangeAnswer: config.Duration(12 * time.Minute),
		SideThreadTurn: config.Duration(9 * time.Minute),
	}
	cfg.Agents["architect"] = architect

	provider := &capturingBackend{result: backend.RunResult{SessionID: "session-2", FinalText: "An answer."}}
	answer := exchangeVoice{config: cfg, provider: provider, repository: t.TempDir(), productID: "yoyodyne"}
	if _, err := answer.Answer(context.Background(), exchange.Question{
		ExchangeID: "exchange-" + strings.Repeat("a", 32),
		Role:       domain.RoleArchitect,
		Asker:      domain.RoleProductManager,
		Round:      1,
		MaxRounds:  10,
		Question:   "what does this cost?",
	}); err != nil {
		t.Fatalf("Answer() error = %v", err)
	}
	if provider.request.Timeout != 12*time.Minute {
		t.Errorf("exchange answer timeout = %s, want the architect's 12m", provider.request.Timeout)
	}

	side := &capturingBackend{result: backend.RunResult{SessionID: "session-side-2", FinalText: "An answer."}}
	voice := sideVoice{config: cfg, provider: side, repository: t.TempDir(), productID: "yoyodyne", spend: &recordedSpend{}}
	if _, err := voice.Answer(context.Background(), testSideQuestion()); err != nil {
		t.Fatalf("side Answer() error = %v", err)
	}
	if side.request.Timeout != 9*time.Minute {
		t.Errorf("side turn timeout = %s, want the architect's 9m", side.request.Timeout)
	}
}

// A recurring pass waits for its conversation and takes its turn within the
// woken agent's own turn limit, and an agent stating none keeps the default.
func TestARecurringPassIsBoundedByTheWokenAgentsTurnLimit(t *testing.T) {
	t.Parallel()

	cfg := config.Config{Agents: map[string]config.AgentConfig{
		"technical-health-pm": {Role: domain.RoleProgramManager, TimeLimits: config.TimeLimits{Turn: config.Duration(45 * time.Minute), Message: config.Duration(time.Hour)}},
		"product-manager":     {Role: domain.RoleProductManager},
	}}
	roles := roleConversation{config: cfg}
	if got := roles.turnLimit(domain.RoleProgramManager, "technical-health-pm"); got != 45*time.Minute {
		t.Errorf("program manager pass bound = %s, want its configured 45m", got)
	}
	if got := roles.turnLimit(domain.RoleProductManager, ""); got != chat.DefaultTurnTimeout {
		t.Errorf("product manager pass bound = %s, want the default %s", got, chat.DefaultTurnTimeout)
	}
	if got := roles.turnLimit(domain.RoleArchitect, "nobody"); got != chat.DefaultTurnTimeout {
		t.Errorf("an unconfigured agent's pass bound = %s, want the default", got)
	}
}
