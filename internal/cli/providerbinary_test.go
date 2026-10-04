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
	"github.com/mason-bryant/yoyodyne/internal/execution"
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
						trigger := roleConversation{open: func(context.Context, domain.AgentRole, string, string) (*chat.Session, *runstate.ConversationHold, error) {
							return session, nil, nil
						}}
						turn, err := trigger.Wake(context.Background(), role, string(role), "fixture-pass", "", message)
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
	return session
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
