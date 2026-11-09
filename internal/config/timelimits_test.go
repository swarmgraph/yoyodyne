package config

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/budget"
)

// An agent that states nothing keeps exactly the figures the harness used
// before the key existed.
func TestEveryTimeLimitDefaultsToTheFigureUsedBeforeItWasConfigurable(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig, nil).Config
	for name, agent := range cfg.Agents {
		limits := agent.TimeLimits
		if limits != (TimeLimits{}) {
			t.Errorf("agent %q time_limits = %+v, want nothing stated by the built-in bundle", name, limits)
		}
		for _, check := range []struct {
			key       string
			got, want time.Duration
		}{
			{"turn", limits.TurnLimit(), 15 * time.Minute},
			{"message", limits.MessageLimit(), 45 * time.Minute},
			{"slack_turn", limits.SlackTurnLimit(), 10 * time.Minute},
			{"exchange_answer", limits.ExchangeAnswerLimit(), 5 * time.Minute},
			{"side_thread_turn", limits.SideThreadTurnLimit(), 5 * time.Minute},
			{"review", limits.ReviewLimit(), 15 * time.Minute},
			{"provider_call", limits.ProviderCallLimit(), 4 * time.Hour},
		} {
			if check.got != check.want {
				t.Errorf("agent %q %s = %s, want %s", name, check.key, check.got, check.want)
			}
		}
		if got := limits.MessageBudget(); got != (budget.Limits{Turns: 16, Elapsed: 45 * time.Minute}) {
			t.Errorf("agent %q message budget = %+v, want 16 turns, 45 minutes, and no spend limit", name, got)
		}
		// The budget package reads the defaults it is given exactly as it reads
		// a zero, so a message of an agent that states nothing is decided as one
		// was before.
		if got, want := budget.MessageLimits(limits.MessageBudget()), budget.MessageLimits(budget.Limits{}); got != want {
			t.Errorf("agent %q message limits = %+v, want the budget package's defaults %+v", name, got, want)
		}
	}
}

func TestAConversationRoleSetsItsOwnTurnAndMessageLimits(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig+`agents:
  architect:
    time_limits:
      turn: 45m
      message: 1h30m
      message_turns: 30
      message_spend_usd: 4.5
      slack_turn: 20m
      exchange_answer: 10m
      side_thread_turn: 12m
`, nil).Config
	limits := cfg.Agents["architect"].TimeLimits
	if got := limits.TurnLimit(); got != 45*time.Minute {
		t.Errorf("turn = %s, want 45m", got)
	}
	if got := limits.MessageBudget(); got != (budget.Limits{Turns: 30, Elapsed: 90 * time.Minute, SpendUSD: 4.5}) {
		t.Errorf("message budget = %+v, want 30 turns, 1h30m, $4.50", got)
	}
	if limits.SlackTurnLimit() != 20*time.Minute || limits.ExchangeAnswerLimit() != 10*time.Minute || limits.SideThreadTurnLimit() != 12*time.Minute {
		t.Errorf("limits = %+v, want the slack, exchange, and side-thread figures stated", limits)
	}
	// One agent's limits are its own.
	if got := cfg.Agents["product-manager"].TimeLimits.TurnLimit(); got != DefaultTurnTimeLimit {
		t.Errorf("product-manager turn = %s, want the default; another agent's setting reached it", got)
	}
}

func TestTheReviewerAndTheDeveloperSetTheirOwnCallLimits(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig+`agents:
  reviewer:
    time_limits:
      review: 40m
  developer:
    time_limits:
      provider_call: 6h
`, nil).Config
	if got := cfg.Agents["reviewer"].TimeLimits.ReviewLimit(); got != 40*time.Minute {
		t.Errorf("reviewer review = %s, want 40m", got)
	}
	if got := cfg.Agents["developer"].TimeLimits.ProviderCallLimit(); got != 6*time.Hour {
		t.Errorf("developer provider_call = %s, want 6h", got)
	}
}

// A turn as long as the message holding it would be stopped by both limits at
// once, so the configuration is refused, naming the persona. Defaults count: a
// turn raised to the default message limit is refused until the message limit
// is raised too.
func TestATurnNotShorterThanItsMessageIsRefused(t *testing.T) {
	t.Parallel()

	for _, block := range []string{
		"      turn: 45m\n",
		"      turn: 1h\n      message: 1h\n",
		"      message: 10m\n",
	} {
		_, err := loadProjectError(t, minimalProjectConfig+"agents:\n  development-manager:\n    time_limits:\n"+block, nil)
		if err == nil {
			t.Fatalf("time_limits\n%s loaded, want a turn not shorter than its message refused", block)
		}
		if !strings.Contains(err.Error(), `agent "development-manager" time_limits.turn`) || !strings.Contains(err.Error(), "not shorter than its message limit") {
			t.Fatalf("error = %v, want it to name the agent and the turn against the message", err)
		}
	}
}

func TestATimeLimitOutsideItsRangeIsRefusedNamingTheAgentKeyAndValue(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		agent, block, want string
	}{
		{"architect", "      turn: 10s\n", `agent "architect" time_limits.turn is 10s, which is outside its range of 1m0s to 4h0m0s`},
		{"architect", "      message: 9h\n", `agent "architect" time_limits.message is 9h0m0s, which is outside its range`},
		{"architect", "      message_turns: 500\n", `agent "architect" time_limits.message_turns is 500, which is outside its range of 1 to 100`},
		{"architect", "      message_spend_usd: -1\n", `agent "architect" time_limits.message_spend_usd is -1, which is outside its range`},
		{"architect", "      slack_turn: 5h\n", `agent "architect" time_limits.slack_turn is 5h0m0s`},
		{"architect", "      exchange_answer: 2h\n", `agent "architect" time_limits.exchange_answer is 2h0m0s`},
		{"architect", "      side_thread_turn: 30s\n", `agent "architect" time_limits.side_thread_turn is 30s`},
		{"reviewer", "      review: 5h\n", `agent "reviewer" time_limits.review is 5h0m0s`},
		{"developer", "      provider_call: 5m\n", `agent "developer" time_limits.provider_call is 5m0s, which is outside its range of 10m0s to 24h0m0s`},
	} {
		_, err := loadProjectError(t, minimalProjectConfig+"agents:\n  "+test.agent+":\n    time_limits:\n"+test.block, nil)
		if err == nil {
			t.Fatalf("%s time_limits\n%s loaded, want it refused", test.agent, test.block)
		}
		if !strings.Contains(err.Error(), test.want) {
			t.Fatalf("error = %v, want it to contain %q", err, test.want)
		}
	}
}

// A limit on an agent whose role nothing applies it to would read as in force
// while nothing reads it.
func TestATimeLimitOnARoleThatDoesNotUseItIsRefused(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		agent, block, want string
	}{
		{"developer", "      turn: 20m\n", `agent "developer" time_limits.turn is set, but only an agent that holds a conversation uses it`},
		{"reviewer", "      provider_call: 1h\n", `agent "reviewer" time_limits.provider_call is set, but only the developer uses it`},
		{"architect", "      review: 20m\n", `agent "architect" time_limits.review is set, but only the reviewer uses it`},
	} {
		_, err := loadProjectError(t, minimalProjectConfig+"agents:\n  "+test.agent+":\n    time_limits:\n"+test.block, nil)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("error = %v, want it to contain %q", err, test.want)
		}
	}
}

func TestATimeLimitsBlockIsAKeyARoleMayApply(t *testing.T) {
	t.Parallel()

	if err := RoleMayApply("agents.product-manager.time_limits.turn"); err != nil {
		t.Fatalf("RoleMayApply(time_limits.turn) = %v, want it applicable", err)
	}
}
