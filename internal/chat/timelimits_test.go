package chat

import (
	"context"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func TestTheTurnDefaultIsTheConfigurationsDefault(t *testing.T) {
	t.Parallel()
	if DefaultTurnTimeout != config.DefaultTurnTimeLimit {
		t.Fatalf("DefaultTurnTimeout = %s, want 15m and config.DefaultTurnTimeLimit", DefaultTurnTimeout)
	}
	if got := (Options{}).timeout(); got != DefaultTurnTimeout {
		t.Fatalf("an unset turn limit = %s, want %s", got, DefaultTurnTimeout)
	}
}

// A conversation role's configured turn limit is every provider invocation's
// total time, and its configured message limit is the one the message is held
// to.
func TestAConfiguredTurnAndMessageLimitAreTheOnesApplied(t *testing.T) {
	t.Parallel()
	limits := config.TimeLimits{
		Turn:         config.Duration(40 * time.Minute),
		Message:      config.Duration(2 * time.Hour),
		MessageTurns: 3,
	}
	provider := neverStops(healthRequest)
	options := testOptions(t, provider)
	options.Role, options.Agent = domain.RoleDevelopmentManager, string(domain.RoleDevelopmentManager)
	options.HealthObserver = &fixedHealth{}
	options.Timeout = limits.TurnLimit()
	options.MessageBudget = limits.MessageBudget()
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Keep looking.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("provider turns = %d, want the configured 3", len(provider.requests))
	}
	for index, request := range provider.requests {
		if request.Timeout != 40*time.Minute {
			t.Errorf("turn %d timeout = %s, want the configured 40m", index+1, request.Timeout)
		}
		// The provider's own limit on a session that says nothing is not
		// something a turn limit reaches: the request leaves it to the adapter.
		if request.IdleTimeout != 0 {
			t.Errorf("turn %d idle timeout = %s, want it left to the adapter", index+1, request.IdleTimeout)
		}
	}
	if reply.Stopped == "" {
		t.Fatal("reply.Stopped is empty, want the message stopped at its configured turn limit")
	}
}
