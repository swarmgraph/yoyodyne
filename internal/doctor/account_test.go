package doctor

// Whether each pooled account is actually signed in.
//
// This is the check that exists because of when the failure would otherwise be
// found. A provider home nobody has logged into refuses every invocation the
// pool sends there, and without this the harness discovers that as a run refused
// by a provider — after an item has been claimed and a worktree cut for it. So
// what is under test is the gate (a project with one account is not asked at
// all), the answer for each half of the pool, and that each account is asked in
// the home the harness would actually invoke it in.

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// accountDiagnosis is the diagnosis these tests drive, wired to the same world
// every other test here uses so the state root the accounts hang off is the one
// this machine would really resolve.
func accountDiagnosis(w *world) *diagnosis {
	return &diagnosis{env: Environment{
		Runner:      w.runner,
		LookPath:    w.lookPath,
		Getenv:      w.getenv,
		UserHomeDir: func() (string, error) { return w.project, nil },
		GOOS:        w.goos,
	}}
}

// accountsConfig is a resolved configuration with whatever a test wants to say
// about accounts in it. It is built directly rather than decoded because what is
// under test is the diagnosis rather than the loader.
func accountsConfig(accounts map[string]config.Account) config.Resolved {
	return config.Resolved{
		Config: config.Config{
			Accounts: accounts,
			Agents: map[string]config.AgentConfig{
				"developer": {Role: domain.RoleDeveloper, Backend: domain.BackendClaudeCode, Model: "opus"},
			},
		},
		Path: filepath.Join("project", ".yoyodyne", "config.yaml"),
	}
}

func builtInRegistry(t *testing.T) *backend.Registry {
	t.Helper()
	registry, err := backend.NewRegistry(nil)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return registry
}

// The gate. A project with one account authenticates where the machine already
// does, whatever that account is called, so the provider check has already asked
// the only question there is — and asking it again under a second name would be
// a diagnosis that looked longer without saying more.
func TestAProjectWithOneAccountIsNotAskedAboutItTwice(t *testing.T) {
	t.Parallel()

	for name, accounts := range map[string]map[string]config.Account{
		"no account at all": nil,
		"one named account": {"work": {}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			world := newWorld(t)
			diagnosis := accountDiagnosis(world)
			findings := diagnosis.checkAccounts(context.Background(), accountsConfig(accounts), builtInRegistry(t))
			if len(findings) != 0 {
				t.Fatalf("checkAccounts() = %+v, want nothing for a project that does not pool", findings)
			}
			// And nothing was asked of the provider, which is the half of the gate
			// that costs something: a probe per alias is a process per alias.
			if len(world.runner.commands) != 0 {
				t.Fatalf("an unpooled project ran %d provider command(s)", len(world.runner.commands))
			}
		})
	}
}

// Under a pool each alias is reported by name, with which half of the pool it is
// in, so an operator reading the diagnosis knows both whether the account works
// and what the harness will do with it.
func TestEachPooledAccountIsReportedByNameAndPoolHalf(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	home := filepath.Join(world.stateRoot, "accounts", "second")
	// The three aliases are asked the same two questions and are told apart only
	// by the home they are asked in, which is exactly the distinction the check
	// exists to make. Only `second` is unsigned.
	world.runner.replyInEnv("auth status --json", "CLAUDE_CONFIG_DIR="+home,
		succeeded(`{"loggedIn":false,"authMethod":"none"}`))

	findings := accountDiagnosis(world).checkAccounts(context.Background(), accountsConfig(map[string]config.Account{
		"default": {},
		"second":  {},
		"spare":   {Pool: config.PoolReserved},
	}), builtInRegistry(t))

	if len(findings) != 3 {
		t.Fatalf("checkAccounts() returned %d finding(s), want one per configured account", len(findings))
	}
	byCheck := map[string]Finding{}
	for _, finding := range findings {
		byCheck[finding.Check] = finding
	}

	signedIn, found := byCheck["account:default"]
	if !found || signedIn.Status != StatusOK {
		t.Fatalf("account:default = %+v, want the signed-in account reported healthy", signedIn)
	}
	if !strings.Contains(signedIn.Summary, "active pool") {
		t.Fatalf("account:default summary = %q, want the half of the pool it is in", signedIn.Summary)
	}

	// The account nobody has signed in to is the state this check exists to
	// catch, and the remedy is the login for its own home rather than the
	// machine's.
	missing, found := byCheck["account:second"]
	if !found || missing.Status != StatusProblem {
		t.Fatalf("account:second = %+v, want the unauthenticated account reported as a problem", missing)
	}
	if !strings.Contains(missing.Detail, home) {
		t.Fatalf("account:second detail = %q, want the provider home %q", missing.Detail, home)
	}
	if !strings.Contains(missing.Remedy, "CLAUDE_CONFIG_DIR=") ||
		!strings.Contains(missing.Remedy, "claude auth login") ||
		!strings.Contains(missing.Remedy, home) {
		t.Fatalf("account:second remedy = %q, want the login for that account's own home", missing.Remedy)
	}

	reserved, found := byCheck["account:spare"]
	if !found || reserved.Status != StatusOK {
		t.Fatalf("account:spare = %+v, want the reserved account reported healthy", reserved)
	}
	if !strings.Contains(reserved.Summary, "reserved pool") {
		t.Fatalf("account:spare summary = %q, want it named as reserved", reserved.Summary)
	}
}

// The default alias stays the machine's own home even under a pool, and every
// other alias is asked in a home of its own. A check that probed one home while
// the harness invoked in another is the one failure here that nothing
// downstream would catch, so it is asserted on the commands.
func TestEachAccountIsAskedInTheHomeTheHarnessWouldInvokeIn(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	accountDiagnosis(world).checkAccounts(context.Background(), accountsConfig(map[string]config.Account{
		"default": {},
		"second":  {},
	}), builtInRegistry(t))

	if len(world.runner.invocations) != 4 {
		t.Fatalf("checkAccounts() ran %d command(s), want two probes for each of two accounts", len(world.runner.invocations))
	}
	// The first two are the default alias, which names no provider home: the
	// account that was already signed in keeps the login it had. What it is
	// asked with is the same explicit environment a run is given, which is the
	// point of asking -- a home the run would authenticate in is the one probed.
	for _, command := range world.runner.invocations[:2] {
		for _, entry := range command.Env {
			if strings.HasPrefix(entry, "CLAUDE_CONFIG_DIR="+world.stateRoot) {
				t.Fatalf("the default alias was pointed at a pooled provider home: %v", command.Env)
			}
		}
	}
	want := "CLAUDE_CONFIG_DIR=" + filepath.Join(world.stateRoot, "accounts", "second")
	for _, command := range world.runner.invocations[2:] {
		if !slices.Contains(command.Env, want) {
			t.Fatalf("the second alias was asked with %v, want %q", command.Env, want)
		}
	}
}

// Each account is asked about its own provider. A pool that holds one account
// per provider is the shape account failover has to work across, and a diagnosis
// that asked every home whether `claude` was signed in there would report a
// Codex account as broken and a Claude Code account as fine whichever of them
// was really which.
//
// This is the diagnosis half of yoyodyne-ifd.351: `yoyo doctor` reported a pool
// healthy while a Codex run in it would have been handed a Claude Code provider
// home and died unauthenticated after claiming an item.
func TestEachAccountIsAskedAboutItsOwnProvider(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	// Codex says it is not signed in, which is the state this check exists to
	// catch and the one the Claude Code accounts are not in.
	world.runner.reply("login status", failed("not logged in"))

	findings := accountDiagnosis(world).checkAccounts(context.Background(), accountsConfig(map[string]config.Account{
		"default": {},
		"second":  {Provider: domain.BackendCodex},
	}), builtInRegistry(t))

	byCheck := map[string]Finding{}
	for _, finding := range findings {
		byCheck[finding.Check] = finding
	}
	onCodex, found := byCheck["account:second"]
	if !found || onCodex.Status != StatusProblem {
		t.Fatalf("account:second = %+v, want the unauthenticated Codex account reported as a problem", onCodex)
	}
	if !strings.Contains(onCodex.Summary, `provider "codex"`) {
		t.Fatalf("account:second summary = %q, want the provider it holds named", onCodex.Summary)
	}
	home := filepath.Join(world.stateRoot, "accounts", "second")
	if !strings.Contains(onCodex.Remedy, "CODEX_HOME="+home) || !strings.Contains(onCodex.Remedy, "codex login") {
		t.Fatalf("account:second remedy = %q, want codex's own login in codex's own home variable", onCodex.Remedy)
	}
	if machine, found := byCheck["account:default"]; !found || machine.Status != StatusOK {
		t.Fatalf("account:default = %+v, want the Claude Code account still reported healthy", machine)
	}

	// And the executables are each provider's own, asked in that account's home:
	// a Codex account asked by `claude` is a diagnosis of somebody else's login.
	for _, command := range world.runner.invocations {
		home, wantBinary := "", "claude"
		if slices.ContainsFunc(command.Env, func(entry string) bool { return strings.HasPrefix(entry, "CODEX_HOME=") }) {
			home, wantBinary = "codex home", "codex"
		}
		if filepath.Base(command.Name) != wantBinary {
			t.Fatalf("a command in the %q was %q, want %q", home, command.Name, wantBinary)
		}
	}
}

// The executable asked about is the developer's provider rather than this
// build's own, because the pool exists to serve runs and a run is what the
// developer's provider spends. A project that declared a fork of Claude Code is
// therefore diagnosed with the binary its runs will really use.
func TestAPooledAccountIsAskedWithTheDevelopersOwnExecutable(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	registry, err := backend.NewRegistry(map[domain.Backend]backend.ProviderPlugin{
		"my-harness": {
			Adapter:  domain.BackendClaudeCode,
			Binary:   "my-harness",
			Roles:    domain.Roles(),
			Postures: backend.Postures,
			Dialect: backend.DialectSpec{Rules: []backend.DialectRule{
				{Answer: backend.AnswerRefused, Type: "result"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	resolved := accountsConfig(map[string]config.Account{"default": {}, "second": {}})
	resolved.Config.Agents["developer"] = config.AgentConfig{
		Role: domain.RoleDeveloper, Backend: "my-harness", Model: "opus",
	}

	diagnosis := accountDiagnosis(world)
	diagnosis.env.LookPath = func(binary string) (string, error) { return filepath.Join("/usr/local/bin", binary), nil }
	diagnosis.checkAccounts(context.Background(), resolved, registry)

	if len(world.runner.invocations) == 0 {
		t.Fatal("checkAccounts() asked nothing")
	}
	for _, command := range world.runner.invocations {
		if command.Name != "/usr/local/bin/my-harness" {
			t.Fatalf("an account was asked with %q, want the developer's own executable", command.Name)
		}
	}
}
