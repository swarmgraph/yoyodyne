package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/doctor"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Real local fixture processes exercise the same adapter and failover wiring
// as terminal chats and scheduled turns. They do not contact a model service.
func TestDesktopCodexOutsidePATHServesPrimaryFallbackAndScheduledTurns(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "Desktop App.app", "Contents", "Resources", "codex-cli", "bin", "codex")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	codexScript := `#!/bin/sh
case "$1" in
  --version) printf 'codex-cli fixture\n';;
  login) printf 'Logged in using ChatGPT\n';;
  exec)
    while IFS= read -r line || [ -n "$line" ]; do :; done
    printf '%s\n' '{"type":"thread.started","thread_id":"codex-fixture-session"}' '{"type":"item.completed","item":{"type":"agent_message","text":"Codex fixture reply."}}' '{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}';;
  *) exit 2;;
esac
`
	if err := os.WriteFile(binary, []byte(codexScript), 0o755); err != nil {
		t.Fatal(err)
	}
	terminalBin := filepath.Join(root, "terminal bin")
	if err := os.MkdirAll(terminalBin, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeScript := `#!/bin/sh
case "$1" in
  --version) printf 'Claude fixture\n'; exit 0;;
  auth) printf '%s\n' '{"loggedIn":true,"authMethod":"claude.ai"}'; exit 0;;
esac
limited=no
while IFS= read -r line || [ -n "$line" ]; do
  case "$line" in *request-fallback*) limited=yes;; esac
done
if [ "$limited" = yes ]; then
  printf '%s\n' '{"type":"rate_limit_event","session_id":"claude-fixture-session","rate_limit_info":{"status":"rejected","resetsAt":4102444800,"rateLimitType":"five_hour"}}' '{"type":"result","subtype":"error","session_id":"claude-fixture-session","is_error":true,"terminal_reason":"usage_limit","result":"limit reached"}'
  exit 1
fi
printf '%s\n' '{"type":"result","subtype":"success","session_id":"claude-fixture-session","is_error":false,"result":"Claude fixture reply."}'
`
	if err := os.WriteFile(filepath.Join(terminalBin, "claude"), []byte(claudeScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", terminalBin)
	cfg := config.Config{
		Product: config.Product{ID: "executable-fixture"},
		Providers: map[string]backend.ProviderPlugin{
			"codex": {Binary: binary},
		},
		Accounts: map[string]config.Account{
			"default": {Provider: domain.BackendClaudeCode},
			"codex":   {Provider: domain.BackendCodex},
		},
		Agents: map[string]config.AgentConfig{
			"architect": {Role: domain.RoleArchitect, Backend: domain.BackendCodex, Account: "codex", Model: "gpt-6-astra"},
		},
	}
	for _, role := range []domain.AgentRole{domain.RoleProductManager, domain.RoleDevelopmentManager} {
		cfg.Agents[string(role)] = config.AgentConfig{Role: role, Backend: domain.BackendClaudeCode, Model: "fable", Account: "default", Failover: config.Failover{Enabled: true, Model: "gpt-6-astra", Provider: domain.BackendCodex, Account: "codex"}}
	}

	for _, role := range []domain.AgentRole{domain.RoleArchitect, domain.RoleProductManager, domain.RoleDevelopmentManager} {
		t.Run(string(role), func(t *testing.T) {
			for _, scheduled := range []bool{false, true} {
				t.Run(map[bool]string{false: "terminal", true: "scheduled"}[scheduled], func(t *testing.T) {
					runner := &providerFixtureRunner{}
					session := executableFixtureSession(t, cfg, role, runner)
					message := "ping"
					if role != domain.RoleArchitect {
						// Opening and serving a primary turn must work before the
						// next turn actually launches the configured fallback.
						reply, err := session.Send(context.Background(), message)
						if err != nil || reply.Text != "Claude fixture reply." {
							t.Fatalf("primary turn = %+v, %v", reply, err)
						}
						message = "request-fallback"
					}
					if scheduled {
						trigger := roleConversation{open: func(context.Context, domain.AgentRole, string, string, orchestrator.RecurringTurnOptions) (*chat.Session, *runstate.ConversationHold, *runstate.SweepConversationReplacement, error) {
							return session, nil, nil, nil
						}}
						turn, err := trigger.Wake(context.Background(), role, string(role), "fixture-pass", "", message, orchestrator.RecurringTurnOptions{})
						wantModel := "gpt-6-astra"
						if role != domain.RoleArchitect {
							wantModel = "codex's gpt-6-astra"
						}
						if err != nil || turn.Model != wantModel {
							t.Fatalf("scheduled turn = %+v, %v", turn, err)
						}
					} else {
						reply, err := session.Send(context.Background(), message)
						if err != nil || reply.Text != "Codex fixture reply." {
							t.Fatalf("Codex turn = %+v, %v", reply, err)
						}
					}
					if session.Evidence().SessionID != "codex-fixture-session" {
						t.Fatalf("no completed Codex process: %+v", session.Evidence())
					}
					started := false
					for _, command := range runner.commands {
						if command.Name == binary && len(command.Args) > 0 && command.Args[0] == "exec" {
							started = true
							if !strings.Contains(strings.Join(command.Args, "\n"), "--sandbox\nread-only") {
								t.Fatalf("role lost read-only sandbox: %v", command.Args)
							}
						}
					}
					if !started {
						t.Fatal("selected a model without launching the configured Codex")
					}
				})
			}
		})
	}
}

func executableFixtureSession(t *testing.T, cfg config.Config, role domain.AgentRole, runner execution.ProcessRunner) *chat.Session {
	t.Helper()
	name := string(role)
	agent := cfg.Agents[name]
	root := t.TempDir()
	account, err := cfg.Endpoint(root, agent.Account)
	if err != nil {
		t.Fatal(err)
	}
	provider := providerBackendIn(cfg, agent.Backend, runner, account.Directory)
	available, err := provider.CheckAvailability(context.Background())
	if err != nil || !available.Authenticated {
		t.Fatalf("cannot open primary: %+v, %v", available, err)
	}
	session, _ := openFixtureSession(t, cfg, role, runner, root)
	return session
}

// openFixtureSession opens the conversation as prepareChat wires it, under the
// given state root, and returns the product's substitution log beside it.
func openFixtureSession(t *testing.T, cfg config.Config, role domain.AgentRole, runner execution.ProcessRunner, root string) (*chat.Session, *runstate.UsageLimitStore) {
	t.Helper()
	name := string(role)
	agent := cfg.Agents[name]
	account, err := cfg.Endpoint(root, agent.Account)
	if err != nil {
		t.Fatal(err)
	}
	provider := providerBackendIn(cfg, agent.Backend, runner, account.Directory)
	alternate := conversationFailover(cfg, root, name, runner, io.Discard)
	store, err := runstate.NewConversationStore(root, cfg.Product.ID)
	if err != nil {
		t.Fatal(err)
	}
	limits, err := runstate.NewUsageLimitStore(root, cfg.Product.ID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := chat.Open(chat.Options{Role: role, Agent: name, Backend: provider, Store: store, Model: agent.Model, Provider: agent.Backend, Providers: providerRegistry(cfg), AccountAlias: account.Alias, Repository: t.TempDir(), ProductID: cfg.Product.ID, RepositoryID: "fixture", Persona: "Inspect only.", Briefing: chat.Briefing{Text: "Controlled executable fixture.", GatheredAt: time.Now()}, UsageLimits: limits, FailoverModel: alternate.model, FailoverEndpoint: alternate.endpoint, FailoverBackend: alternate.backend, FailoverAccountConfigDir: alternate.configDir})
	if err != nil {
		t.Fatal(err)
	}
	return session, limits
}

type providerFixtureRunner struct{ commands []execution.Command }

func (r *providerFixtureRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	r.commands = append(r.commands, command)
	return (execution.OSProcessRunner{}).Run(ctx, command, observer)
}

func TestLoginRemedyUsesTheConfiguredExecutableAndAccountHome(t *testing.T) {
	t.Parallel()
	const binary = "/Applications/Desktop App.app/Contents/Resources/codex-cli/bin/codex"
	const directory = "/state/codex account"
	cfg := config.Config{Providers: map[string]backend.ProviderPlugin{"codex": {Binary: binary}}, Accounts: map[string]config.Account{"codex": {Provider: domain.BackendCodex}}}
	actual := accountLoginCommand(cfg, domain.BackendClaudeCode, config.AccountEndpoint{Alias: "codex", Directory: directory})
	want := doctor.AccountLoginCommand(domain.BackendCodex, directory, binary)
	if actual != want || !strings.Contains(actual, "'"+binary+"' login") || !strings.Contains(actual, "CODEX_HOME='"+directory+"'") {
		t.Fatalf("login remedy = %q, want configured CLI and account home %q", actual, want)
	}
}

// A role whose configured provider's executable is not installed here is served
// by its configured alternate on another provider, in its terminal
// conversation, its scheduled pass, and an exchange another role starts, and the
// record says which provider served and why. A role with no alternate is never
// moved: it fails with the missing executable and how to set it up. Real local
// fixture processes stand in for both CLIs; no model service is contacted.
func TestAMissingPrimaryExecutableIsServedByTheConfiguredAlternateProvider(t *testing.T) {
	terminalBin := filepath.Join(t.TempDir(), "terminal bin")
	if err := os.MkdirAll(terminalBin, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeScript := `#!/bin/sh
case "$1" in
  --version) printf 'Claude fixture\n'; exit 0;;
  auth) printf '%s\n' '{"loggedIn":true,"authMethod":"claude.ai"}'; exit 0;;
esac
while IFS= read -r line || [ -n "$line" ]; do :; done
printf '%s\n' '{"type":"result","subtype":"success","session_id":"claude-fixture-session","is_error":false,"result":"Claude fixture reply."}'
`
	if err := os.WriteFile(filepath.Join(terminalBin, "claude"), []byte(claudeScript), 0o755); err != nil {
		t.Fatal(err)
	}
	// Codex is installed nowhere this process looks, and nothing names it.
	t.Setenv("PATH", terminalBin)
	cfg := config.Config{
		Product: config.Product{ID: "executable-fixture"},
		Accounts: map[string]config.Account{
			"default": {Provider: domain.BackendClaudeCode},
			"codex":   {Provider: domain.BackendCodex},
		},
		Agents: map[string]config.AgentConfig{
			"architect": {Role: domain.RoleArchitect, Backend: domain.BackendCodex, Account: "codex", Model: "gpt-6-astra",
				Failover: config.Failover{Enabled: true, Model: "fable", Provider: domain.BackendClaudeCode, Account: "default"}},
			// Configured with no alternate, so it is never moved.
			"development-manager": {Role: domain.RoleDevelopmentManager, Backend: domain.BackendCodex, Account: "codex", Model: "gpt-6-astra"},
			"product-manager":     {Role: domain.RoleProductManager, Backend: domain.BackendClaudeCode, Account: "default", Model: "fable"},
		},
	}
	cfg.Execution.UsageLimitUnknownResetPause = config.Duration(30 * time.Minute)

	wantRecorded := func(t *testing.T, limits *runstate.UsageLimitStore) {
		t.Helper()
		entries, err := limits.List()
		if err != nil || len(entries) == 0 {
			t.Fatalf("substitutions = %+v, %v; want the move written down", entries, err)
		}
		entry := entries[0]
		if entry.Reason() != runstate.SubstitutedForExecutable || entry.Provider != domain.BackendCodex || entry.ServedByProvider != domain.BackendClaudeCode || entry.ServedBy != "fable" {
			t.Fatalf("substitution = %+v, want codex moved to claude-code's fable for want of an executable", entry)
		}
		if !strings.Contains(entry.Waiting, "codex") || !strings.Contains(entry.Describe(), "could not be found or started") {
			t.Fatalf("substitution waiting = %q, describe = %q; want why", entry.Waiting, entry.Describe())
		}
	}

	t.Run("conversation opens on the alternate", func(t *testing.T) {
		alternate, servable := servableAlternate(context.Background(), cfg, t.TempDir(), "architect", &providerFixtureRunner{})
		if !servable || alternate.endpoint.Provider != domain.BackendClaudeCode {
			t.Fatalf("servable alternate = %+v, %v; want claude-code", alternate, servable)
		}
		if _, servable := servableAlternate(context.Background(), cfg, t.TempDir(), "development-manager", &providerFixtureRunner{}); servable {
			t.Fatal("a role with no alternate was offered one")
		}
	})

	for _, scheduled := range []bool{false, true} {
		t.Run(map[bool]string{false: "terminal", true: "scheduled"}[scheduled], func(t *testing.T) {
			runner := &providerFixtureRunner{}
			session, limits := openFixtureSession(t, cfg, domain.RoleArchitect, runner, t.TempDir())
			if scheduled {
				trigger := roleConversation{open: func(context.Context, domain.AgentRole, string, string, orchestrator.RecurringTurnOptions) (*chat.Session, *runstate.ConversationHold, *runstate.SweepConversationReplacement, error) {
					return session, nil, nil, nil
				}}
				turn, err := trigger.Wake(context.Background(), domain.RoleArchitect, "architect", "fixture-pass", "", "ping", orchestrator.RecurringTurnOptions{})
				if err != nil || turn.Model != "claude-code's fable" {
					t.Fatalf("scheduled turn = %+v, %v", turn, err)
				}
			} else {
				reply, err := session.Send(context.Background(), "ping")
				if err != nil || reply.Text != "Claude fixture reply." {
					t.Fatalf("terminal turn = %+v, %v", reply, err)
				}
			}
			if session.Evidence().ServedModel != "claude-code's fable" {
				t.Fatalf("evidence = %+v, want the serving provider named", session.Evidence())
			}
			for _, command := range runner.commands {
				if filepath.Base(command.Name) != "claude" {
					t.Fatalf("launched %q, want only the alternate's executable", command.Name)
				}
			}
			wantRecorded(t, limits)
		})
	}

	t.Run("exchange", func(t *testing.T) {
		root := t.TempDir()
		limits, err := runstate.NewUsageLimitStore(root, cfg.Product.ID)
		if err != nil {
			t.Fatal(err)
		}
		runner := &providerFixtureRunner{}
		voice := exchangeVoice{config: cfg, runner: runner, repository: t.TempDir(), stateRoot: root, usageLimits: limits, productID: cfg.Product.ID}
		spoken, err := voice.Answer(context.Background(), exchange.Question{
			ExchangeID: "exchange-" + strings.Repeat("b", 32), Role: domain.RoleArchitect, Asker: domain.RoleProductManager,
			Round: 1, MaxRounds: 10, Question: "ping",
		})
		if err != nil || spoken.Answer != "Claude fixture reply." {
			t.Fatalf("exchange = %+v, %v", spoken, err)
		}
		if spoken.Backend != domain.BackendClaudeCode || spoken.AccountAlias != "default" || spoken.Model != "fable" {
			t.Fatalf("exchange record = %+v, want the provider and account that answered", spoken)
		}
		wantRecorded(t, limits)
	})

	t.Run("no alternate", func(t *testing.T) {
		session, limits := openFixtureSession(t, cfg, domain.RoleDevelopmentManager, &providerFixtureRunner{}, t.TempDir())
		_, err := session.Send(context.Background(), "ping")
		if err == nil || !strings.Contains(err.Error(), `"codex" was not found on PATH`) || !strings.Contains(err.Error(), "providers.codex.binary") {
			t.Fatalf("turn error = %v, want the missing executable and its setup", err)
		}
		if entries, _ := limits.List(); len(entries) != 0 {
			t.Fatalf("substitutions = %+v, want none for a role with no alternate", entries)
		}
	})
}
