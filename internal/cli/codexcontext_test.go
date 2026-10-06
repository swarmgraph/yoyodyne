package cli

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/backend/claudecode"
	"github.com/mason-bryant/yoyodyne/internal/backend/codex"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// What a project names for its Codex roles reaches the adapter that runs them,
// whichever account it runs under; nothing is named for any other adapter.
func TestTheCodexAdapterIsGivenWhatTheProjectNames(t *testing.T) {
	t.Parallel()
	named := backend.NamedContext{Skills: []backend.ContextFile{{Path: ".yoyodyne/skills/review", Roles: []domain.AgentRole{domain.RoleReviewer}}}}
	cfg := config.Config{Codex: named}
	// buildComponents sets the product's repository to the harness's own
	// checkout, which is where a relative path is read from for every role.
	cfg.Product.Repository = "/checkout"
	for _, configDir := range []string{"", "/accounts/second"} {
		provider, isCodex := providerBackendIn(cfg, domain.BackendCodex, execution.OSProcessRunner{}, configDir).(codex.Backend)
		if !isCodex {
			t.Fatalf("the codex backend did not build the Codex adapter")
		}
		if len(provider.Context.Skills) != 1 || provider.Context.Skills[0].Path != "/checkout/.yoyodyne/skills/review" || provider.ConfigDir != configDir {
			t.Fatalf("adapter = %+v", provider)
		}
	}
}

// What a project names for its Claude Code roles reaches the Claude Code adapter,
// and what it names for Codex does not.
func TestTheClaudeCodeAdapterIsGivenWhatTheProjectNames(t *testing.T) {
	t.Parallel()
	cfg := config.Config{
		ClaudeCode: backend.NamedContext{Instructions: []backend.ContextFile{{Path: "docs/agent-notes.md"}}},
		Codex:      backend.NamedContext{Skills: []backend.ContextFile{{Path: ".yoyodyne/skills/codex-only"}}},
	}
	cfg.Product.Repository = "/checkout"
	for _, configDir := range []string{"", "/accounts/second"} {
		provider, isClaudeCode := providerBackendIn(cfg, domain.BackendClaudeCode, execution.OSProcessRunner{}, configDir).(claudecode.Backend)
		if !isClaudeCode {
			t.Fatalf("the claude-code backend did not build the Claude Code adapter")
		}
		if len(provider.Context.Instructions) != 1 || provider.Context.Instructions[0].Path != "/checkout/docs/agent-notes.md" || len(provider.Context.Skills) != 0 || provider.ConfigDir != configDir {
			t.Fatalf("adapter = %+v", provider)
		}
	}
}
