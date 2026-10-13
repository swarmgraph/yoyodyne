package claudecode

import (
	"context"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// A compact_boundary is the provider saying it compacted the session the
// invocation ran in, and the result carries it with the size it reported.
func TestACompactBoundaryIsOnTheResult(t *testing.T) {
	t.Parallel()

	stream := `{"type":"system","subtype":"compact_boundary","session_id":"s","compact_metadata":{"trigger":"auto","pre_tokens":251234}}` + "\n" +
		`{"type":"system","subtype":"compact_boundary","session_id":"s"}` + "\n" +
		`{"type":"result","subtype":"success","session_id":"s","is_error":false,"result":"done","total_cost_usd":0.01}` + "\n"
	runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: stream}}}
	result, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID: testRunID, Role: domain.RoleArchitect, WorkingDirectory: "/repository", Prompt: "the pass",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Compactions) != 2 {
		t.Fatalf("compactions = %+v", result.Compactions)
	}
	first := result.Compactions[0]
	if first.Trigger != "auto" || first.BeforeTokens != 251234 || first.AfterTokens != 0 || !first.At.Equal(fixedClock{}.Now().UTC()) {
		t.Fatalf("first compaction = %+v", first)
	}
	if second := result.Compactions[1]; second.BeforeTokens != 0 || second.Trigger != "" {
		t.Fatalf("a compaction reporting nothing = %+v", second)
	}
}

func TestTheContextWindowFollowsTheModelFamily(t *testing.T) {
	t.Parallel()
	for model, want := range map[string]int{"opus": 1_000_000, "claude-sonnet-5-5": 1_000_000, "haiku": 200_000, "": 200_000} {
		if got := (Backend{}).ContextWindowTokens(backendapi.RunRequest{Model: model}); got != want {
			t.Errorf("ContextWindowTokens(%q) = %d, want %d", model, got, want)
		}
	}
}
