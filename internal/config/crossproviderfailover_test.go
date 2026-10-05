package config

// An agent whose alternate is on another provider.
//
// The refusals matter more than the resolution here. A crossing is the one place
// an agent's endpoint changes without anybody configuring the change, so a
// project that wrote down a crossing onto a provider that cannot hold the role's
// posture has to be told when the file is read — hearing it when a window closes
// costs the turn the failover existed to save.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// crossingProviders is a project naming a second provider its agents may fail
// over onto, and the account that signs it in. Its roles and postures are the
// declaration's own, which is what every eligibility answer here is derived from.
const crossingProviders = `providers:
  second-provider:
    adapter: claude-code
    binary: second-provider
    roles:
      - product-manager
      - architect
      - development-manager
      - developer
      - reviewer
    postures:
      - read-only
      - worktree-write
    capabilities:
      structured_events: true
      session_resumption: true
    dialect:
      rules:
        - answer: interrupted
          terminal: true
          failed: true
accounts:
  default:
    provider: claude-code
  second-account:
    provider: second-provider
  codex-account:
    provider: codex
`

// The block an operator writes for a crossing, and what it resolves to: the
// alternate model, on the provider and the account the block names.
func TestAnAgentFailsOverOntoAnotherProvidersEndpoint(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig+crossingProviders+`agents:
  development-manager:
    model: fable
    failover:
      enabled: true
      model: second-model
      provider: second-provider
      account: second-account
`, nil).Config
	failover := cfg.AgentFailover("development-manager")
	if !failover.CrossesProviders(cfg.Agents["development-manager"].Backend) {
		t.Fatalf("failover = %#v, want it read as leaving the provider the agent runs on", failover)
	}

	providers, err := cfg.ProviderRegistry()
	if err != nil {
		t.Fatalf("ProviderRegistry() error = %v", err)
	}
	choice, crosses, err := cfg.AgentFailoverEndpoint(providers, filepath.Join(t.TempDir(), "state"), "development-manager")
	if err != nil {
		t.Fatalf("AgentFailoverEndpoint() error = %v", err)
	}
	if !crosses {
		t.Fatal("AgentFailoverEndpoint() reported no alternate, want the endpoint the agent named")
	}
	if choice.Endpoint.Provider != "second-provider" || choice.Endpoint.Model != "second-model" {
		t.Fatalf("endpoint = %#v, want the provider and model the failover block named", choice.Endpoint)
	}
	if choice.Endpoint.AccountAlias != "second-account" {
		t.Fatalf("account = %q, want the alias the failover block named", choice.Endpoint.AccountAlias)
	}
	// The adapter that reaches it is part of the endpoint's identity, because a
	// record naming only the provider cannot say which harness code read its
	// stream.
	if !choice.Endpoint.Runnable() {
		t.Fatalf("endpoint = %#v, want one this build ships an adapter for", choice.Endpoint)
	}
}

// An agent that names only a model fails over exactly as it did before an
// alternate could name a provider: within its own provider, with nothing to
// resolve and no crossing to make.
func TestAnAlternateThatNamesNoProviderStaysOnTheAgentsOwn(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig+`agents:
  development-manager:
    model: fable
    failover:
      enabled: true
      model: opus
`, nil).Config
	agent := cfg.Agents["development-manager"]
	if agent.Failover.CrossesProviders(agent.Backend) {
		t.Fatal("the failover reads as crossing providers, want it staying on the one the agent runs on")
	}
	if provider := agent.Failover.AlternateProvider(agent.Backend); provider != domain.BackendClaudeCode {
		t.Fatalf("alternate provider = %q, want the agent's own", provider)
	}
	providers, err := cfg.ProviderRegistry()
	if err != nil {
		t.Fatalf("ProviderRegistry() error = %v", err)
	}
	choice, crosses, err := cfg.AgentFailoverEndpoint(providers, filepath.Join(t.TempDir(), "state"), "development-manager")
	if err != nil {
		t.Fatalf("AgentFailoverEndpoint() error = %v", err)
	}
	if !crosses || choice.Endpoint.Provider != domain.BackendClaudeCode {
		t.Fatalf("endpoint = %#v, want the agent's own provider asking the alternate model", choice.Endpoint)
	}
}

// The same model on another provider is a different endpoint and a real
// alternate, so the refusal that catches an agent naming its own model does not
// catch it.
func TestTheSameModelOnAnotherProviderIsAnAlternate(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig+crossingProviders+`agents:
  development-manager:
    model: fable
    failover:
      enabled: true
      model: fable
      provider: second-provider
      account: second-account
`, nil).Config
	if alternate := cfg.AgentFailoverModel("development-manager"); alternate != "fable" {
		t.Fatalf("failover model = %q, want the same selector asked of the other provider", alternate)
	}
}

func TestACrossingIsRefusedWhereTheAlternateCouldNotServeTheRole(t *testing.T) {
	t.Parallel()

	// The reviewer reasons over bounded evidence with no tools at all, and this
	// provider scopes writes to a worktree and can express nothing narrower.
	writesOnly := strings.Replace(crossingProviders, `    postures:
      - read-only
      - worktree-write`, `    postures:
      - worktree-write`, 1)
	for _, test := range []struct {
		name    string
		agent   string
		project string
		want    string
	}{
		{
			name:    "a provider this project does not name",
			project: crossingProviders,
			agent: `  development-manager:
    model: fable
    failover:
      enabled: true
      model: second-model
      provider: nobody-declared-this
`,
			want: `fails over to provider "nobody-declared-this", which is not a provider this project names`,
		},
		{
			name:    "a provider that cannot hold the role's posture",
			project: writesOnly,
			agent: `  reviewer:
    model: fable
    failover:
      enabled: true
      model: second-model
      provider: second-provider
      account: second-account
`,
			want: `cannot hold the "read-only" tool access`,
		},
		{
			name:    "an account this project does not declare",
			project: crossingProviders,
			agent: `  development-manager:
    model: fable
    failover:
      enabled: true
      model: second-model
      provider: second-provider
      account: nobody-declared-this
`,
			want: `fails over onto account "nobody-declared-this", which this project does not declare`,
		},
		{
			// A home another adapter reads, which is the pairing that actually dies
			// unauthenticated. Two providers on one adapter read the same shape of
			// home and are not refused, which is why this names one on a different
			// adapter rather than merely a different provider.
			name:    "an account holding an authentication another adapter reads",
			project: crossingProviders,
			agent: `  development-manager:
    model: fable
    failover:
      enabled: true
      model: second-model
      provider: second-provider
      account: codex-account
`,
			want: "would authenticate as nobody",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := loadProjectError(t, minimalProjectConfig+test.project+"agents:\n"+test.agent, nil)
			if err == nil {
				t.Fatal("LoadResolved() succeeded, want the crossing refused where the file is read")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want it to name %q", err, test.want)
			}
		})
	}
}

// A caller that cannot cross does not substitute at all. Asking the agent's own
// provider for a model that belongs to another one is not a fallback: it is an
// invocation that fails on a selector nobody there has heard of, at exactly the
// moment the fallback was supposed to save the turn.
func TestACrossingIsNotOfferedToACallerThatCannotCross(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig+crossingProviders+`agents:
  development-manager:
    model: fable
    failover:
      enabled: true
      model: second-model
      provider: second-provider
      account: second-account
  architect:
    model: fable
    failover:
      enabled: true
      model: opus
`, nil).Config
	if within := cfg.AgentFailoverModelWithinProvider("development-manager"); within != "" {
		t.Fatalf("within-provider alternate = %q, want nothing: that model belongs to another provider", within)
	}
	// The crossing is still there for the caller that can make one.
	if alternate := cfg.AgentFailoverModel("development-manager"); alternate != "second-model" {
		t.Fatalf("alternate = %q, want the model the agent named", alternate)
	}
	// And an alternate that never left the provider is offered to everybody.
	if within := cfg.AgentFailoverModelWithinProvider("architect"); within != "opus" {
		t.Fatalf("within-provider alternate = %q, want the alternate on the agent's own provider", within)
	}
}

// An agent that named no account of its own is still refused where the account it
// would actually be served under cannot sign the alternate in. The alias is the
// pool's answer rather than the key the failover block wrote down, so an agent
// that wrote no key is not a hole in the check — it is the common case.
func TestACrossingIsRefusedOnTheAccountTheAgentWouldActuallyBeServedUnder(t *testing.T) {
	t.Parallel()

	// No `account` anywhere: not on the agent, not in the failover block. The pool
	// serves the agent's own provider from `default`, which holds Claude Code's
	// authentication and cannot sign in a provider on the Codex adapter.
	_, err := loadProjectError(t, minimalProjectConfig+`accounts:
  default:
    provider: claude-code
agents:
  developer:
    model: fable
    failover:
      enabled: true
      model: gpt-6-astra
      provider: codex
`, nil)
	if err == nil {
		t.Fatal("LoadResolved() succeeded, want the crossing refused where the file is read")
	}
	if !strings.Contains(err.Error(), "would authenticate as nobody") {
		t.Fatalf("error = %v, want the account that could not sign the alternate in named", err)
	}
}

// The same agent with an account that does hold the alternate's authentication
// loads clean, so the refusal above is about the pool rather than about naming no
// key.
func TestACrossingLoadsWhereThePoolCanSignTheAlternateIn(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig+`accounts:
  default:
    provider: claude-code
  codex-account:
    provider: codex
agents:
  developer:
    model: fable
    failover:
      enabled: true
      model: gpt-6-astra
      provider: codex
      account: codex-account
`, nil).Config
	providers, err := cfg.ProviderRegistry()
	if err != nil {
		t.Fatalf("ProviderRegistry() error = %v", err)
	}
	choice, crosses, err := cfg.AgentFailoverEndpoint(providers, filepath.Join(t.TempDir(), "state"), "developer")
	if err != nil || !crosses {
		t.Fatalf("AgentFailoverEndpoint() = %#v, crosses %v, error %v", choice, crosses, err)
	}
	if choice.Endpoint.Provider != domain.BackendCodex || choice.Endpoint.AccountAlias != "codex-account" {
		t.Fatalf("endpoint = %#v, want the alternate on the account that signs it in", choice.Endpoint)
	}
}

// A block that says where an alternate would be served and names no alternate is
// half written, and is refused as one. Switching failover off is how an operator
// keeps a choice they already made; a provider standing alone is a choice nobody
// finished, and left to load it would read as a configured crossing that can
// never happen.
func TestAFailoverThatSaysWhereButNotWhatIsRefused(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		block string
		want  string
	}{
		{
			name: "a provider with no model",
			block: `    failover:
      enabled: true
      provider: codex
`,
			want: "failover.provider answers where and failover.model answers what",
		},
		{
			name: "an account with no model",
			block: `    failover:
      account: codex-account
`,
			want: "failover.account answers where and failover.model answers what",
		},
		{
			// Switched off and half written is still half written: there is no choice
			// here being parked, because none was ever finished.
			name: "both, with failover switched off",
			block: `    failover:
      enabled: false
      provider: codex
      account: codex-account
`,
			want: "failover.provider and failover.account answers where and failover.model answers what",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := loadProjectError(t, minimalProjectConfig+`accounts:
  default:
    provider: claude-code
  codex-account:
    provider: codex
agents:
  developer:
    model: fable
`+test.block, nil)
			if err == nil {
				t.Fatal("LoadResolved() succeeded, want the half-written block refused where the file is read")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want it to name %q", err, test.want)
			}
		})
	}
}

// And the complete block, switched off, still loads: that is an operator keeping
// the choice they made rather than making one, which is what `enabled: false`
// beside a stated alternate has always meant.
func TestACompleteCrossingSwitchedOffKeepsTheChoiceAndLoads(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig+`accounts:
  default:
    provider: claude-code
  codex-account:
    provider: codex
agents:
  developer:
    model: fable
    failover:
      enabled: false
      model: gpt-6-astra
      provider: codex
      account: codex-account
`, nil).Config
	if alternate := cfg.AgentFailoverModel("developer"); alternate != "" {
		t.Fatalf("failover model = %q, want nothing while it is switched off", alternate)
	}
	if kept := cfg.AgentFailover("developer"); kept.Model != "gpt-6-astra" || kept.Provider != domain.BackendCodex {
		t.Fatalf("kept block = %#v, want the whole choice kept so it can be switched back on", kept)
	}
}
