package config

import (
	"strings"
	"testing"
)

// The shipped template states an effort level beside the model for every agent,
// and states the provider's own default for the model it names rather than a
// level somebody preferred: Claude Code's documentation gives "medium" as the
// default of Opus 5.5, which is what "opus" serves.
func TestTheTemplatePinsEveryAgentToTheProvidersDefaultEffort(t *testing.T) {
	t.Parallel()

	for source, cfg := range map[string]Config{
		"the bundle":   loadProject(t, minimalProjectConfig, nil).Config,
		"the scaffold": loadScaffold(t, ScaffoldOptions{ProductID: "example", Repository: "."}).Config,
	} {
		if len(cfg.Agents) == 0 {
			t.Fatalf("%s configures no agents", source)
		}
		for name, agent := range cfg.Agents {
			if agent.Model != "opus" {
				t.Fatalf("%s: agent %q model = %q; the pinned default below is Opus 5.5's and has to be revisited for another model", source, name, agent.Model)
			}
			if got := cfg.AgentEffort(name); got != "medium" {
				t.Fatalf("%s: agent %q effort = %q, want Claude Code's default for Opus 5.5, medium", source, name, got)
			}
		}
	}
}

// A level the agent's provider does not accept is refused when the file loads,
// with a sentence naming the levels it does.
func TestAnEffortTheProviderDoesNotAcceptIsRefusedNamingTheLevels(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, minimalProjectConfig+`agents:
  architect:
    effort: extreme
`, nil)
	if err == nil {
		t.Fatal("LoadResolved() succeeded, want the level refused")
	}
	want := `agent "architect" names effort "extreme", which provider "claude-code" does not accept; effort is one of low, medium, high, xhigh, or max`
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

// Every level Claude Code accepts loads, and a later layer stating the key empty
// removes an inherited level, which is how an agent goes back to the provider
// resolving its own.
func TestEveryAcceptedLevelLoadsAndEmptyRemovesAnInheritedOne(t *testing.T) {
	t.Parallel()

	for _, level := range []string{"low", "medium", "high", "xhigh", "max"} {
		cfg := loadProject(t, minimalProjectConfig+`agents:
  developer:
    effort: `+level+`
`, nil).Config
		if got := cfg.AgentEffort("developer"); got != level {
			t.Fatalf("effort = %q, want %q", got, level)
		}
	}
	resolved := loadProject(t, minimalProjectConfig+`agents:
  developer:
    effort: ""
`, nil)
	if got := resolved.Config.AgentEffort("developer"); got != "" {
		t.Fatalf("effort = %q, want the inherited level removed", got)
	}
	if origin := resolved.Origins["agents.developer.effort"]; origin == "" {
		t.Fatalf("the removal has no origin; origins = %v", resolved.Origins)
	}
}

// An unadvertised Codex effort is refused before any invocation starts.
func TestACodexAgentNamingAnUnacceptedEffortIsRefused(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, minimalProjectConfig+`agents:
  developer:
    backend: codex
    model: gpt-6-astra
    effort: extreme
`, nil)
	if err == nil {
		t.Fatal("LoadResolved() succeeded, want the Codex agent's level refused")
	}
	want := `agent "developer" names effort "extreme", which provider "codex" does not accept; effort is one of low, medium, high, xhigh, max, or ultra`
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

// A failover onto Codex can retain the configured high effort.
func TestAFailoverCrossingOntoCodexWithHighEffortLoads(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig+`accounts:
  default:
    provider: claude-code
  codex-account:
    provider: codex
agents:
  developer:
    model: fable
    effort: high
    failover:
      enabled: true
      model: gpt-6-astra
      provider: codex
      account: codex-account
`, nil).Config
	if got := cfg.AgentEffort("developer"); got != "high" {
		t.Fatalf("effort = %q, want the agent's own level kept", got)
	}
}

func TestCodexEffortUsesTheModelsLevelsAndExplicitDefault(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ model, level, want string }{
		{"gpt-6-astra", "high", "high"},
		{"gpt-6.1-sol", "", "low"},
		{"gpt-6-sol", "", "medium"},
		{"gpt-6-luna", "max", "max"},
	} {
		cfg := loadProject(t, minimalProjectConfig+`agents:
  developer:
    backend: codex
    model: `+test.model+`
    effort: "`+test.level+`"
`, nil).Config
		if got := cfg.AgentEffort("developer"); got != test.want {
			t.Fatalf("%s at %q: got %q, want %q", test.model, test.level, got, test.want)
		}
	}
	_, err := loadProjectError(t, minimalProjectConfig+`agents:
  developer:
    backend: codex
    model: gpt-6-luna
    effort: ultra
`, nil)
	if err == nil || !strings.Contains(err.Error(), "low, medium, high, xhigh, or max") {
		t.Fatalf("model-specific refusal = %v", err)
	}
}

func TestTechnicalHealthProgramManagerKeepsCodexAstraHighWithoutFallback(t *testing.T) {
	t.Parallel()
	cfg := loadProject(t, minimalProjectConfig+`agents:
  technical-health-pm:
    role: program-manager
    backend: codex
    model: gpt-6-astra
    effort: high
`, nil).Config
	agent := cfg.Agents["technical-health-pm"]
	if agent.Backend != "codex" || agent.Model != "gpt-6-astra" || cfg.AgentEffort("technical-health-pm") != "high" || agent.Failover.Enabled || agent.ModelVersion != "" {
		t.Fatalf("technical-health configuration = %+v", agent)
	}
}

func TestCodexModelVersionValidatesEffortForBothSelectorsAtLoad(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, model, version, level string
		want                        []string
	}{
		{"family refuses effort", "gpt-6-luna", "gpt-6-astra", "ultra", []string{`model "gpt-6-luna"`, "low, medium, high, xhigh, or max"}},
		{"family is unestablished", "unlisted-family", "gpt-6-astra", "high", []string{`Codex model "unlisted-family"`, "not established"}},
		{"family is unestablished with default effort", "unlisted-family", "gpt-6-astra", "", []string{`Codex model "unlisted-family"`, "not established"}},
		{"version refuses effort", "gpt-6-astra", "gpt-6-luna", "ultra", []string{`model "gpt-6-luna"`, "low, medium, high, xhigh, or max"}},
		{"version is unestablished", "gpt-6-astra", "unlisted-version", "high", []string{`Codex model "unlisted-version"`, "not established"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := loadProjectError(t, minimalProjectConfig+`agents:
  developer:
    backend: codex
    model: `+test.model+`
    model_version: `+test.version+`
    effort: "`+test.level+`"
`, nil)
			if err == nil {
				t.Fatal("LoadResolved() succeeded, want the version or family fallback refused")
			}
			for _, want := range test.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %v, want %q", err, want)
				}
			}
		})
	}
}

func TestCodexModelVersionKeepsExplicitOrDefaultEffortWhenBothSelectorsAcceptIt(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ level, want string }{
		{"high", "high"},
		{"", "medium"},
	} {
		cfg := loadProject(t, minimalProjectConfig+`agents:
  developer:
    backend: codex
    model: gpt-6-astra
    model_version: gpt-6-sol
    effort: "`+test.level+`"
`, nil).Config
		if got := cfg.AgentEffort("developer"); got != test.want {
			t.Fatalf("effort %q resolved to %q, want the version's effort %q", test.level, got, test.want)
		}
	}
}
