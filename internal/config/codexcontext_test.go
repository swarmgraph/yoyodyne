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

// A project names the skills and instruction files its Claude Code roles are
// given the same way, in a section of its own; nothing else from the account's
// home reaches them.
func TestAProjectNamesTheSkillsAndInstructionFilesItsClaudeCodeRolesAreGiven(t *testing.T) {
	t.Parallel()

	input := validBootstrapConfig + `claude_code:
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
	if len(cfg.ClaudeCode.Skills) != 1 || cfg.ClaudeCode.Skills[0].Path != ".yoyodyne/skills/review" || len(cfg.ClaudeCode.Skills[0].Roles) != 1 || cfg.ClaudeCode.Skills[0].Roles[0] != domain.RoleReviewer ||
		len(cfg.ClaudeCode.Instructions) != 1 || cfg.ClaudeCode.Instructions[0].Path != "docs/agent-notes.md" {
		t.Fatalf("claude_code = %+v", cfg.ClaudeCode)
	}
	if len(cfg.Codex.Skills) != 0 || len(cfg.Codex.Instructions) != 0 {
		t.Fatalf("a file named for Claude Code reached Codex: %+v", cfg.Codex)
	}
	for name, section := range map[string]string{
		"no path":      "claude_code:\n  skills:\n    - roles: [reviewer]\n",
		"unknown role": "claude_code:\n  instructions:\n    - path: docs/notes.md\n      roles: [janitor]\n",
	} {
		_, err := Decode(strings.NewReader(validBootstrapConfig + section))
		if err == nil || !strings.Contains(err.Error(), "claude_code.") {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}
