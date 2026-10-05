package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// pooledConfig is the smallest pooled configuration: two accounts whose stable
// order is one then two, and the agents that would be served from them.
func pooledConfig(t *testing.T, extra string) Config {
	t.Helper()
	return mustDecodeConfig(t, `version: 1
product:
  id: yoyodyne
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
accounts:
  one: {}
  two: {}
agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
  reviewer:
    role: reviewer
    backend: claude-code
    model: sonnet
`+extra)
}

func builtInRegistry(t *testing.T) *backend.Registry {
	t.Helper()
	registry, err := backend.NewRegistry(nil)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return registry
}

// The pool answers with an endpoint rather than with an account, so what a
// caller is handed says the provider, the adapter that reaches it, the account,
// and the model together — and the account endpoint beside it still says where
// on this machine that alias authenticates.
func TestThePoolAnswersWithAnEndpointAndTheAccountItAuthenticatesUnder(t *testing.T) {
	t.Parallel()

	cfg := pooledConfig(t, "")
	choice, err := cfg.ChooseEndpoint(builtInRegistry(t), "/state", "developer", backend.Endpoint{}, nil)
	if err != nil {
		t.Fatalf("ChooseEndpoint() error = %v", err)
	}
	want := backend.Endpoint{
		Provider:       domain.BackendClaudeCode,
		AdapterVersion: backend.ClaudeCodeAdapterVersion,
		AccountAlias:   "one",
		Model:          "opus",
	}
	if !choice.Endpoint.Same(want) {
		t.Fatalf("ChooseEndpoint() = %s, want %s", choice.Endpoint, want)
	}
	if dir := filepath.Join("/state", "accounts", "one"); choice.Account.Directory != dir {
		t.Fatalf("the chosen account authenticates in %q, want %q", choice.Account.Directory, dir)
	}
}

// The cursor is an endpoint and not an account, which is what keying the pool on
// endpoints buys: a turn served on some other endpoint family does not move this
// one's rotation, so two agents on different models do not read each other's
// position as their own.
func TestTheCursorIsAnEndpointRatherThanAnAccount(t *testing.T) {
	t.Parallel()

	cfg := pooledConfig(t, "")
	registry := builtInRegistry(t)
	served, err := cfg.ChooseEndpoint(registry, "/state", "developer", backend.Endpoint{}, nil)
	if err != nil {
		t.Fatalf("ChooseEndpoint() error = %v", err)
	}

	next, err := cfg.ChooseEndpoint(registry, "/state", "developer", served.Endpoint, nil)
	if err != nil {
		t.Fatalf("ChooseEndpoint() error = %v", err)
	}
	if next.Endpoint.AccountAlias != "two" {
		t.Fatalf("after a turn on %q the pool chose %q, want the next account", served.Endpoint, next.Endpoint)
	}

	// The reviewer asks a different model, so the developer's cursor is not its
	// cursor: its own rotation starts at the top rather than where somebody else's
	// turn left off.
	elsewhere, err := cfg.ChooseEndpoint(registry, "/state", "reviewer", served.Endpoint, nil)
	if err != nil {
		t.Fatalf("ChooseEndpoint() error = %v", err)
	}
	if elsewhere.Endpoint.AccountAlias != "one" {
		t.Fatalf("the reviewer's rotation started at %q, want the top of its own pool", elsewhere.Endpoint)
	}
}

// A budget is the operator's limit on an account, so it stays keyed on the
// account whichever endpoint is asking — and a pool with nothing left to spend
// refuses in the same words it always did.
func TestTheEndpointPoolHonoursTheAccountBudgets(t *testing.T) {
	t.Parallel()

	cfg := pooledConfig(t, "")
	spent := map[string]float64{"one": 12}
	budget := 10.0
	cfg.Accounts["one"] = Account{WeeklyBudgetUSD: &budget}

	registry := builtInRegistry(t)
	choice, err := cfg.ChooseEndpoint(registry, "/state", "developer", backend.Endpoint{}, spent)
	if err != nil {
		t.Fatalf("ChooseEndpoint() error = %v", err)
	}
	if choice.Endpoint.AccountAlias != "two" {
		t.Fatalf("the pool chose %s, want the account that has not spent its budget", choice.Endpoint)
	}

	cfg.Accounts["two"] = Account{WeeklyBudgetUSD: &budget}
	spent["two"] = 12
	_, err = cfg.ChooseEndpoint(registry, "/state", "developer", backend.Endpoint{}, spent)
	if err == nil || !strings.Contains(err.Error(), "spent its weekly budget") {
		t.Fatalf("ChooseEndpoint() = %v, want the refusal naming the budgets", err)
	}
}

// A configured Codex reviewer can be selected from a matching account pool.
func TestThePoolServesACodexReviewer(t *testing.T) {
	t.Parallel()
	cfg := pooledConfig(t, "")
	reviewer := cfg.Agents["reviewer"]
	reviewer.Backend = domain.BackendCodex
	cfg.Agents["reviewer"] = reviewer
	cfg.Accounts["one"] = Account{Provider: domain.BackendCodex}
	cfg.Accounts["two"] = Account{Provider: domain.BackendCodex}
	choice, err := cfg.ChooseEndpoint(builtInRegistry(t), "/state", "reviewer", backend.Endpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if choice.Endpoint.Provider != domain.BackendCodex {
		t.Fatalf("provider = %q, want Codex", choice.Endpoint.Provider)
	}

	// An agent nothing configured has no endpoint either, which is refused rather
	// than answered with somebody else's.
	if _, err := cfg.ChooseEndpoint(builtInRegistry(t), "/state", "nobody", backend.Endpoint{}, nil); err == nil {
		t.Fatal("ChooseEndpoint() answered for an agent this configuration does not name")
	}
}

// An agent's own invocations are made on a stable endpoint rather than a
// rotating one, for the reason its account is stable: a conversation lasts for
// weeks, and an agent moved between accounts each turn would have no provider
// session left to resume.
func TestAnAgentsOwnEndpointIsTheAccountItIsAssignedTo(t *testing.T) {
	t.Parallel()

	cfg := pooledConfig(t, "")
	reviewer := cfg.Agents["reviewer"]
	reviewer.Account = "two"
	cfg.Agents["reviewer"] = reviewer

	choice, err := cfg.AgentEndpoint(builtInRegistry(t), "/state", "reviewer")
	if err != nil {
		t.Fatalf("AgentEndpoint() error = %v", err)
	}
	if choice.Endpoint.AccountAlias != "two" || choice.Endpoint.Model != "sonnet" {
		t.Fatalf("AgentEndpoint() = %s, want the endpoint the agent is assigned to", choice.Endpoint)
	}
	if choice.Account.Alias != "two" {
		t.Fatalf("the endpoint authenticates as %q, want the agent's own account", choice.Account.Alias)
	}
}

// An agent assigned to no account is served by the first account in the pool
// that can sign its provider in, rather than by the first account full stop. A
// conversation held in another provider's home authenticates as nobody, and it
// is the same failure a run pointed at one meets — arrived at through the agent's
// own endpoint instead of the rotation.
func TestAnAgentAssignedToNoAccountTakesTheFirstThatHoldsItsProvider(t *testing.T) {
	t.Parallel()

	cfg := pooledConfig(t, "")
	developer := cfg.Agents["developer"]
	developer.Backend = domain.BackendCodex
	developer.Model = "gpt-6.1-sol"
	cfg.Agents["developer"] = developer
	cfg.Accounts["two"] = Account{Provider: domain.BackendCodex}

	choice, err := cfg.AgentEndpoint(builtInRegistry(t), "/state", "developer")
	if err != nil {
		t.Fatalf("AgentEndpoint() error = %v", err)
	}
	if choice.Account.Alias != "two" {
		t.Fatalf("AgentEndpoint() authenticates as %q, want the account that holds the agent's provider", choice.Account.Alias)
	}
	// The reviewer is on Claude Code and takes the top of the pool as it always
	// did, which is the account that holds its own provider.
	reviewer, err := cfg.AgentEndpoint(builtInRegistry(t), "/state", "reviewer")
	if err != nil {
		t.Fatalf("AgentEndpoint() error = %v", err)
	}
	if reviewer.Account.Alias != "one" {
		t.Fatalf("AgentEndpoint() authenticates as %q, want the first account holding Claude Code's login", reviewer.Account.Alias)
	}
}

// The pool asks exactly what configuration validation asked, so nothing the
// loader accepted is refused when a run comes to be served. Codex is the case
// that makes the difference visible: a project may name it for a developer
// agent, validation accepts that because Codex's sandbox holds the developer's
// posture, and the pool answers with the endpoint that provider's own adapter
// reaches rather than asking a second question of its own. A pool that asked one
// would turn a configuration error into a failure at work-claim time.
//
// The account that serves it says it is Codex's, because a pooled home is one
// provider's authentication and the pool will not hand a Codex run a Claude Code
// home.
func TestThePoolRefusesNothingConfigurationValidationAccepted(t *testing.T) {
	t.Parallel()

	cfg := pooledConfig(t, "")
	developer := cfg.Agents["developer"]
	developer.Backend = domain.BackendCodex
	developer.Model = "gpt-6.1-sol"
	cfg.Agents["developer"] = developer
	cfg.Accounts["two"] = Account{Provider: domain.BackendCodex}
	// Configuration validation accepts it: the roles Codex declares and the
	// posture the developer requires are what it reads, both hold, and an account
	// holds the provider the developer runs on.
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want a configuration naming a Codex developer accepted", err)
	}

	choice, err := cfg.ChooseEndpoint(builtInRegistry(t), "/state", "developer", backend.Endpoint{}, nil)
	if err != nil {
		t.Fatalf("ChooseEndpoint() error = %v, want the pool to answer for what the loader accepted", err)
	}
	// The endpoint says what is true of it: the provider the agent named, and the
	// compiled adapter that reaches it.
	if choice.Endpoint.Provider != domain.BackendCodex || choice.Endpoint.AdapterVersion != backend.CodexAdapterVersion {
		t.Fatalf("ChooseEndpoint() = %s, want the Codex endpoint carrying its adapter's version", choice.Endpoint)
	}
	// And the account it is served by is the one that holds Codex's
	// authentication, rather than the first account in the rotation.
	if choice.Account.Alias != "two" {
		t.Fatalf("ChooseEndpoint() chose account %q, want the account that holds the provider's authentication", choice.Account.Alias)
	}
}

// A pooled account holds one provider's authentication, and an invocation
// pointed at another provider's home authenticates as nobody. So the pool leaves
// such an account out, and — when that leaves it nothing — refuses before a run
// has claimed a work item, naming what each account holds.
//
// This is the shape yoyodyne-ifd.351 was admitted for: a developer configured
// for Codex on a pooling installation was handed a Claude Code provider home as
// CODEX_HOME, and found out by dying unauthenticated after the item was claimed
// and the worktree cut.
func TestThePoolWillNotServeAnAgentFromAnotherProvidersAccount(t *testing.T) {
	t.Parallel()

	cfg := pooledConfig(t, "")
	developer := cfg.Agents["developer"]
	developer.Backend = domain.BackendCodex
	developer.Model = "gpt-6.1-sol"
	cfg.Agents["developer"] = developer

	// Both accounts have provider homes of their own under the state root, and a
	// home nobody named a provider for is a Claude Code home — which is what
	// `bin/yoyo-account` and `yoyo doctor` have made every one of them.
	_, err := cfg.ChooseEndpoint(builtInRegistry(t), "/state", "developer", backend.Endpoint{}, nil)
	if err == nil {
		t.Fatal("ChooseEndpoint() served a Codex developer out of a pool of Claude Code accounts")
	}
	for _, want := range []string{`no configured account holds provider "codex"`, `one holds "claude-code"`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ChooseEndpoint() = %v, want it to name %q", err, want)
		}
	}
	// It is not reported as a spent budget, which is the other way a pool runs out
	// and the one thing waiting would fix.
	if strings.Contains(err.Error(), "weekly budget") {
		t.Fatalf("ChooseEndpoint() = %v, want a provider mismatch rather than a budget", err)
	}

	// The same project is refused when its configuration is read, so the ordinary
	// way to meet this is an edit rather than a run.
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted a project no configured account could sign its developer in to")
	} else if !strings.Contains(err.Error(), "accounts.<alias>.provider") {
		t.Fatalf("Validate() = %v, want it to name how an account states its provider", err)
	}
}

// A single account authenticates where the machine does, whatever provider is
// asking, so nothing about provider-scoped accounts reaches a project that pools
// nothing. The same is true of the `default` alias under a pool: it keeps the
// machine's own home, and each provider reads its own there.
func TestAnAccountInTheMachinesOwnHomeServesWhicheverProviderAsks(t *testing.T) {
	t.Parallel()

	lone := mustDecodeConfig(t, `version: 1
product:
  id: yoyodyne
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
accounts:
  work: {}
agents:
  developer:
    role: developer
    backend: codex
    model: gpt-6.1-sol
`)
	if err := lone.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want a single-account project on Codex accepted", err)
	}
	choice, err := lone.ChooseEndpoint(builtInRegistry(t), "/state", "developer", backend.Endpoint{}, nil)
	if err != nil {
		t.Fatalf("ChooseEndpoint() error = %v, want the lone account to serve whichever provider asks", err)
	}
	if choice.Account.Alias != "work" || choice.Account.Directory != "" {
		t.Fatalf("ChooseEndpoint() = %#v, want the lone account in the machine's own home", choice.Account)
	}

	pooled := pooledConfig(t, "")
	if provider := pooled.AccountProvider(DefaultAccountAlias); provider != "" {
		t.Fatalf("AccountProvider(%q) = %q, want the machine's own home to hold nobody in particular", DefaultAccountAlias, provider)
	}
	if provider := pooled.AccountProvider("one"); provider != domain.BackendClaudeCode {
		t.Fatalf("AccountProvider(%q) = %q, want a pooled home that names no provider read as Claude Code's", "one", provider)
	}
}

func TestThePoolRefusesADeclaredProviderMissingReadOnlyAccess(t *testing.T) {
	t.Parallel()
	cfg := pooledConfig(t, "")
	terminal, failed := true, true
	registry, err := backend.NewRegistry(map[domain.Backend]backend.ProviderPlugin{"writes-only": {
		Adapter:  domain.BackendCodex,
		Roles:    []domain.AgentRole{domain.RoleReviewer},
		Postures: []backend.Posture{backend.PostureWorktreeWrite},
		Dialect:  backend.DialectSpec{Rules: []backend.DialectRule{{Answer: backend.AnswerRefused, Terminal: &terminal, Failed: &failed}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	reviewer := cfg.Agents["reviewer"]
	reviewer.Backend = "writes-only"
	cfg.Agents["reviewer"] = reviewer
	_, err = cfg.ChooseEndpoint(registry, "/state", "reviewer", backend.Endpoint{}, nil)
	if err == nil || !strings.Contains(err.Error(), `cannot hold the "read-only" tool access`) {
		t.Fatalf("ChooseEndpoint() = %v, want posture refusal before account rotation", err)
	}
}
