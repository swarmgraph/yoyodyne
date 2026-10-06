package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func TestRequestSizeMeasuresTheComposedPromptOnFreshAndResumedTurns(t *testing.T) {
	t.Parallel()
	for _, role := range []domain.AgentRole{domain.RoleDeveloper, domain.RoleReviewer, domain.RoleProductManager, domain.RoleDevelopmentManager, domain.RoleArchitect, domain.RoleProgramManager} {
		for _, session := range []string{"", "old-session"} {
			request := backendapi.RunRequest{Role: role, WorkingDirectory: "/repo with spaces", SystemPrompt: "Follow the role contract.", Prompt: "Current evidence: 世界 🐝", SessionID: session}
			size, limit := (Backend{}).RequestSize(request)
			if size != len(composePrompt(request, "")) || limit != 1<<20 {
				t.Fatalf("size=%d, limit=%d, composed=%d", size, limit, len(composePrompt(request, "")))
			}
			if size <= len(request.Prompt) {
				t.Fatal("system instructions and adapter text were not counted")
			}
		}
	}
}

func TestRequestSizeRefusesAnOversizedPromptBeforeLaunchingCodex(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{}
	request := backendapi.RunRequest{RunID: testRunID, Role: domain.RoleReviewer, WorkingDirectory: t.TempDir(), Prompt: strings.Repeat("x", 1<<20)}
	_, err := (Backend{Runner: runner}).Run(context.Background(), request)
	var tooLarge *backendapi.RequestTooLarge
	if !errors.As(err, &tooLarge) || len(runner.commands) != 0 {
		t.Fatalf("Run error=%v, provider launched %d times", err, len(runner.commands))
	}
	if tooLarge.Bytes <= tooLarge.LimitBytes {
		t.Fatal("adapter-added instructions did not count toward the limit")
	}
}

func TestRequestSizeCountsWhatTheProjectNames(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, "notes.md"), []byte(strings.Repeat("n", 4096)), 0o644); err != nil {
		t.Fatal(err)
	}
	request := backendapi.RunRequest{Role: domain.RoleDeveloper, WorkingDirectory: repository, Prompt: "work"}
	bare, _ := (Backend{}).RequestSize(request)
	named, _ := (Backend{Context: backendapi.NamedContext{Instructions: []backendapi.ContextFile{{Path: "notes.md"}}}}).RequestSize(request)
	if named < bare+4096 {
		t.Fatalf("named instruction file was not counted: bare=%d named=%d", bare, named)
	}
}
