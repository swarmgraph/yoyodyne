package cli

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
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
	for _, configDir := range []string{"", "/accounts/second"} {
		provider, isCodex := providerBackendIn(cfg, domain.BackendCodex, execution.OSProcessRunner{}, configDir).(codex.Backend)
		if !isCodex {
			t.Fatalf("the codex backend did not build the Codex adapter")
		}
		if len(provider.Context.Skills) != 1 || provider.Context.Skills[0].Path != ".yoyodyne/skills/review" || provider.ConfigDir != configDir {
			t.Fatalf("adapter = %+v", provider)
		}
	}
}
