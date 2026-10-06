package chat

import (
	"context"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Each conversation turn's record says what that turn was given beside its
// prompt, rewritten turn by turn the way the model is, and says "none" where
// that was nothing.
func TestAConversationTurnRecordsWhatItLoaded(t *testing.T) {
	t.Parallel()
	notes := backendapi.LoadedItem{Name: "agent-notes.md", Source: backendapi.LoadedFromProjectConfiguration, Path: "/repository/docs/agent-notes.md"}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{Backend: domain.BackendCodex, SessionID: "codex-session", FinalText: "First.", Loaded: backendapi.NewLoaded(nil, nil, []backendapi.LoadedItem{notes})},
		{Backend: domain.BackendCodex, SessionID: "codex-session", FinalText: "Second.", Loaded: backendapi.NewLoaded(nil, nil, nil)},
	}}
	options := testOptions(t, provider)
	options.Provider, options.Model, options.Effort = domain.BackendCodex, "gpt-6.1-sol", ""
	session := openTestSession(t, options)
	want := []string{
		"skills: none; plugins: none; instruction files: agent-notes.md (project configuration, /repository/docs/agent-notes.md)",
		"skills: none; plugins: none; instruction files: none",
	}
	for index := range want {
		if _, err := session.Send(context.Background(), "continue"); err != nil {
			t.Fatal(err)
		}
		recorded, err := options.Store.Load(runstate.ConversationIdentity{Agent: options.Agent, Role: options.Role})
		if err != nil {
			t.Fatal(err)
		}
		if recorded.ProviderLoaded == nil || recorded.ProviderLoaded.Summary != want[index] {
			t.Fatalf("turn %d loaded = %+v, want %q", index, recorded.ProviderLoaded, want[index])
		}
	}
}

// A Claude Code turn's record names its settings sources and connectors too.
func TestAClaudeCodeConversationTurnRecordsItsSettingsSourcesAndConnectors(t *testing.T) {
	t.Parallel()
	notes := backendapi.LoadedItem{Name: "agent-notes.md", Source: backendapi.LoadedFromProjectConfiguration, Path: "/repository/docs/agent-notes.md"}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "First.", Loaded: backendapi.NewLoaded(nil, nil, []backendapi.LoadedItem{notes}).WithSettingsAndConnectors(nil, nil)},
		{SessionID: "session-1", FinalText: "Second.", Loaded: backendapi.NewLoaded(nil, nil, nil).WithSettingsAndConnectors(nil, nil)},
	}}
	options := testOptions(t, provider)
	session := openTestSession(t, options)
	want := []string{
		"settings sources: none; skills: none; plugins: none; connectors: none; instruction files: agent-notes.md (project configuration, /repository/docs/agent-notes.md)",
		"settings sources: none; skills: none; plugins: none; connectors: none; instruction files: none",
	}
	for index := range want {
		if _, err := session.Send(context.Background(), "continue"); err != nil {
			t.Fatal(err)
		}
		recorded, err := options.Store.Load(runstate.ConversationIdentity{Agent: options.Agent, Role: options.Role})
		if err != nil {
			t.Fatal(err)
		}
		if recorded.ProviderLoaded == nil || recorded.ProviderLoaded.Summary != want[index] {
			t.Fatalf("turn %d loaded = %+v, want %q", index, recorded.ProviderLoaded, want[index])
		}
	}
}
