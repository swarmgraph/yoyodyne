package codex

import (
	"context"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// An invocation's total time is what a developer's time_limits.provider_call
// and every conversation, exchange, side-thread, and review limit set, and four
// hours where none is set.
func TestTheTotalTimeDefaultIsTheConfigurationsDefault(t *testing.T) {
	t.Parallel()
	if defaultTimeout != config.DefaultProviderCallTimeLimit {
		t.Fatalf("defaultTimeout = %s, want config.DefaultProviderCallTimeLimit %s", defaultTimeout, config.DefaultProviderCallTimeLimit)
	}
}

// No configured total reaches the limit on a session that says nothing: a
// longest permitted limit is still held to five minutes of silence, and a
// silent session is stopped as stalled rather than left to its total.
func TestTheNoOutputLimitHoldsWhateverTotalIsConfigured(t *testing.T) {
	t.Parallel()

	for _, total := range []time.Duration{config.MaxProviderCallTimeLimit, config.MaxTurnTimeLimit, config.MinAnswerTimeLimit} {
		runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessStalled, ExitCode: -1}}}
		result, err := (Backend{Runner: runner}).Run(context.Background(), backendapi.RunRequest{
			RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: "/worktree", Prompt: "implement",
			Timeout: total,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		command := runner.commands[0]
		if command.Timeout != total {
			t.Errorf("total = %s, want the configured %s", command.Timeout, total)
		}
		if command.IdleTimeout != 5*time.Minute {
			t.Errorf("with a total of %s the no-output limit = %s, want 5m", total, command.IdleTimeout)
		}
		if !result.IsError || result.StopReason != string(execution.ProcessStalled) {
			t.Errorf("with a total of %s a silent session = %#v, want it stopped as stalled", total, result)
		}
	}
}
