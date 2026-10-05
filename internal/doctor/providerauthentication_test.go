package doctor

import (
	"strings"
	"testing"
)

// A machine with no provider key exported says so, because an empty list of
// complaints and a check that never ran read the same.
func TestDiagnoseSaysWhenNoProviderKeyIsExported(t *testing.T) {
	t.Parallel()

	report := newWorld(t).diagnose()
	finding, found := findingFor(report, "provider-authentication")
	if !found {
		t.Fatalf("no provider-authentication finding: %s", render(report))
	}
	if finding.Status != StatusOK {
		t.Fatalf("provider-authentication = %s on a machine exporting nothing: %s", finding.Status, render(report))
	}
}

// An installation authenticating a provider by an exported key used to work and
// now authenticates nothing. The provider's own finding says the provider is not
// authenticated; this is what says why, before the first run finds out.
func TestDiagnoseWarnsAboutAProviderAuthenticatedByAnExportedKey(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.variables["ANTHROPIC_API_KEY"] = "sk-exported-in-a-shell-profile"
	world.variables["CLAUDE_CODE_OAUTH_TOKEN"] = "oauth-exported-in-a-shell-profile"
	report := world.diagnose()

	finding, found := findingFor(report, "provider-authentication")
	if !found {
		t.Fatalf("no provider-authentication finding: %s", render(report))
	}
	if finding.Status != StatusWarning {
		t.Fatalf("provider-authentication = %s, want a warning: %s", finding.Status, render(report))
	}
	for _, name := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"} {
		if !strings.Contains(finding.Summary, name) {
			t.Errorf("the summary does not name %s: %q", name, finding.Summary)
		}
	}
	// The value is never read back out. A diagnostic that helpfully printed one
	// would put it in a terminal, a scrollback, and whatever collects them.
	for _, value := range world.variables {
		if strings.Contains(finding.Summary+finding.Detail+finding.Remedy, value) {
			t.Errorf("the finding carries the value of a provider key: %q", finding.Summary+finding.Detail+finding.Remedy)
		}
	}
	// The remedy is the provider's own login, which is where authentication
	// actually lives.
	if finding.Remedy != "claude auth login" {
		t.Errorf("remedy = %q, want the provider's own login", finding.Remedy)
	}
	// It is something about an installation that works: the exit status is
	// decided by whether anything would stop work running, and an exported key
	// beside a provider that is signed in stops nothing.
	if !report.Healthy() {
		t.Errorf("an exported provider key made the whole report unhealthy: %s", render(report))
	}
}

// The remedy follows the provider the project's developer actually runs on,
// rather than naming Claude Code's login to somebody whose agents are Codex's.
func TestTheExportedKeyRemedyNamesTheProvidersOwnLogin(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = codexConfig
	world.variables["OPENAI_API_KEY"] = "sk-exported"
	world.runner.reply("codex --version", succeeded("codex-cli 0.20.0"))
	world.runner.reply("codex login status", succeeded("Logged in"))

	finding, found := findingFor(world.diagnose(), "provider-authentication")
	if !found {
		t.Fatal("no provider-authentication finding")
	}
	if finding.Remedy != "codex login" {
		t.Errorf("remedy = %q, want the login of the provider the developer runs on", finding.Remedy)
	}
}

// codexConfig is the healthy configuration with its one agent on the other
// provider, which is what makes the remedy a different command.
const codexConfig = `version: 1
product:
  id: yoyodyne
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
  publishing: human
checks:
  - go test ./...
agents:
  developer:
    role: developer
    backend: codex
    model: gpt-6.1-sol
`
