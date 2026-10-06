package config

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// A project names the skills and instruction files its Codex roles are given;
// nothing else from the account's home reaches them.
func TestAProjectNamesTheSkillsAndInstructionFilesItsCodexRolesAreGiven(t *testing.T) {
	t.Parallel()

	input := validBootstrapConfig + `codex:
  skills:
    - path: .yoyodyne/skills/review
      roles: [reviewer]
  instructions:
    - path: docs/agent-notes.md
`
	cfg, err := Decode(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	want := backend.NamedContext{
		Skills:       []backend.ContextFile{{Path: ".yoyodyne/skills/review", Roles: []domain.AgentRole{domain.RoleReviewer}}},
		Instructions: []backend.ContextFile{{Path: "docs/agent-notes.md"}},
	}
	if len(cfg.Codex.Skills) != 1 || cfg.Codex.Skills[0].Path != want.Skills[0].Path || len(cfg.Codex.Skills[0].Roles) != 1 || cfg.Codex.Skills[0].Roles[0] != domain.RoleReviewer ||
		len(cfg.Codex.Instructions) != 1 || cfg.Codex.Instructions[0].Path != want.Instructions[0].Path {
		t.Fatalf("codex = %+v, want %+v", cfg.Codex, want)
	}
}

func TestANamedSkillOrInstructionFileWithNoPathOrAnUnknownRoleIsRefused(t *testing.T) {
	t.Parallel()

	for name, section := range map[string]string{
		"no path":      "codex:\n  skills:\n    - roles: [reviewer]\n",
		"unknown role": "codex:\n  instructions:\n    - path: docs/notes.md\n      roles: [janitor]\n",
	} {
		_, err := Decode(strings.NewReader(validBootstrapConfig + section))
		if err == nil || !strings.Contains(err.Error(), "codex.") {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}
