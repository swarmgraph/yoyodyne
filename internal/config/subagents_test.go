package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// Nothing configured is sub-agents off, for every developer run in every slot.
func TestSubagentsAreOffUnlessConfiguredOn(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig, nil)
	for slot := 0; slot <= 1; slot++ {
		if setting := resolved.Config.DeveloperSubagents("developer", slot); setting.Enabled || setting.Model != "" || setting.Effort != "" {
			t.Fatalf("slot %d: %+v, want off", slot, setting)
		}
	}
	if notices := resolved.Config.SubagentNotices(); len(notices) != 0 {
		t.Fatalf("notices with sub-agents off: %v", notices)
	}
}

// Turned on with nothing else, sub-agents run on the default model with the
// provider's own effort, and the setting shows where it came from in the
// effective configuration.
func TestSubagentsOnDefaultToSonnetAndNoEffort(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig+"agents:\n  developer:\n    subagents:\n      enabled: true\n", nil)
	setting := resolved.Config.DeveloperSubagents("developer", 0)
	if !setting.Enabled || setting.Model != DefaultSubagentModel || setting.Effort != "" || setting.Origin != "agents.developer.subagents" {
		t.Fatalf("setting = %+v", setting)
	}
	if origin := resolved.Origins["agents.developer.subagents"]; origin == "" {
		t.Fatalf("no origin for the sub-agent setting: %v", resolved.Origins)
	}
	encoded, err := json.Marshal(resolved)
	if err != nil || !strings.Contains(string(encoded), `"subagents":{"enabled":true}`) {
		t.Fatalf("effective configuration does not show the setting: %v", err)
	}
}

// A slot's setting wins over the agent's, then the agent's, then off; the
// effort is always the agent's.
func TestASlotsSubagentSettingWinsOverTheAgents(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig+`execution:
  max_concurrent_developers: 3
  developer_slots:
    - subagents: {enabled: false}
    - subagents: {model: haiku}
    - {}
agents:
  developer:
    instances: 3
    subagents:
      enabled: true
      model: sonnet
      effort: medium
`, nil)
	for slot, want := range map[int]SubagentSetting{
		1: {Origin: "execution.developer_slots slot 1.subagents"},
		2: {Enabled: true, Model: "haiku", Effort: "medium", Origin: "agents.developer.subagents"},
		3: {Enabled: true, Model: "sonnet", Effort: "medium", Origin: "agents.developer.subagents"},
	} {
		if got := resolved.Config.DeveloperSubagents("developer", slot); got != want {
			t.Fatalf("slot %d = %+v, want %+v", slot, got, want)
		}
	}

	off := loadProject(t, minimalProjectConfig+`execution:
  max_concurrent_developers: 1
  developer_slots:
    - subagents: {enabled: true}
`, nil)
	if got := off.Config.DeveloperSubagents("developer", 1); !got.Enabled || got.Model != DefaultSubagentModel {
		t.Fatalf("a slot turning sub-agents on over an agent that says nothing = %+v", got)
	}
}

// What the configuration refuses when it loads: sub-agents on a role that is
// not the developer, a model that is not a selector, an effort the provider
// cannot apply to a sub-agent, and sub-agents on where nothing the run could
// use offers them.
func TestSubagentSettingsThatCannotTakeEffectAreRefused(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		project string
		wanted  string
	}{
		{"a reviewer", "agents:\n  reviewer:\n    subagents: {enabled: true}\n", `sub-agents are a developer's, and agent "reviewer" is a reviewer`},
		{"no selector", "agents:\n  developer:\n    subagents: {enabled: true, model: 'two words'}\n", "agents.developer.subagents.model"},
		{"an effort Claude Code cannot apply", "agents:\n  developer:\n    subagents: {enabled: true, effort: ultra}\n", `"ultra" is not an effort claude-code can apply to a sub-agent`},
		{"a provider without them", routingAccounts + "agents:\n  developer:\n    backend: codex\n    model: gpt-6.1-sol\n    account: codex-account\n    subagents: {enabled: true}\n",
			"no provider this agent could run on offers them (Codex does not offer them)"},
		{"a slot on a provider without them", routingAccounts + `execution:
  max_concurrent_developers: 1
  developer_slots:
    - subagents: {enabled: true}
      routing:
        enabled: true
        primary: {provider: codex, model: gpt-6.1-sol, account: codex-account}
        alternate: {provider: codex, model: gpt-6.1-sol-mini, account: codex-account}
`, "execution.developer_slots slot 1.subagents: sub-agents are on, and no provider this slot could run on offers them"},
	} {
		_, err := loadProjectError(t, minimalProjectConfig+test.project, nil)
		if err == nil || !strings.Contains(err.Error(), test.wanted) {
			t.Fatalf("%s: error = %v, want it to contain %q", test.name, err, test.wanted)
		}
	}
}

// A slot pair that mixes providers keeps its sub-agents where they are
// offered, and the configuration says once which provider will serve its
// runs without them rather than refusing the pair.
func TestAMixedPairIsNotRefusedAndSaysWhichEndpointGoesWithout(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig+routingAccounts+`execution:
  max_concurrent_developers: 1
  developer_slots:
    - routing:
        enabled: true
        primary: {provider: claude-code, model: opus, account: default}
        alternate: {provider: codex, model: gpt-6.1-sol, account: codex-account}
agents:
  developer:
    subagents: {enabled: true}
`, nil)
	notices := resolved.Config.SubagentNotices()
	if len(notices) != 1 || !strings.Contains(notices[0], "developer slot 1 has sub-agents on, and its runs on codex go without them: Codex does not offer them") {
		t.Fatalf("notices = %v", notices)
	}
}
