package claudecode

import (
	"context"
	"errors"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func TestRequestSizeIncludesJSONEscapingAndTheSystemPrompt(t *testing.T) {
	t.Parallel()
	size, limit := (Backend{}).RequestSize(backendapi.RunRequest{Prompt: "\"\\\n", SystemPrompt: "role contract"})
	if size != len(`"\"\\\n"`)+len(`"role contract"`) || limit != 32<<20 {
		t.Fatalf("size=%d, limit=%d", size, limit)
	}
}

func TestRequestSizeRefusesAnOversizedPromptBeforeLaunchingClaude(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{}
	request := backendapi.RunRequest{RunID: testRunID, Role: domain.RoleReviewer, WorkingDirectory: t.TempDir(), Prompt: "Evidence\n" + strings.Repeat("\n", 6<<20)}
	// Escaping doubles this input; the system prompt takes it past 32 MiB.
	request.SystemPrompt = strings.Repeat("x", 21<<20)
	_, err := (Backend{Runner: runner}).Run(context.Background(), request)
	var tooLarge *backendapi.RequestTooLarge
	if !errors.As(err, &tooLarge) || len(runner.commands) != 0 {
		t.Fatalf("Run error=%v, provider launched %d times", err, len(runner.commands))
	}
}
