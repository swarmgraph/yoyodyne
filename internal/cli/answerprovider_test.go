package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A Claude conversation may ask a Codex agent, including a declared provider.
// The answering adapter, model, account and sandbox must all belong to that agent.
func TestAnsweringTurnsUseTheConfiguredProviderAndReadOnlyPolicy(t *testing.T) {
	t.Parallel()
	for _, surface := range []string{"exchange", "side-thread"} {
		for _, named := range []domain.Backend{domain.BackendCodex, "declared-codex"} {
			t.Run(surface+"/"+string(named), func(t *testing.T) {
				cfg := answeringConfig()
				binary := filepath.Join(t.TempDir(), "codex")
				if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
					t.Fatal(err)
				}
				cfg.Providers = map[string]backend.ProviderPlugin{string(named): {Binary: binary}}
				if named != domain.BackendCodex {
					cfg.Providers = map[string]backend.ProviderPlugin{string(named): {Adapter: domain.BackendCodex, Binary: binary, Roles: []domain.AgentRole{domain.RoleArchitect}, Postures: []backend.Posture{backend.PostureReadOnly}, Dialect: backend.DialectSpec{Rules: []backend.DialectRule{{Type: "retry", Answer: backend.AnswerRetrying}}}}}
				}
				agent := cfg.Agents["architect"]
				agent.Backend = named
				agent.Model = "gpt-6.1-sol"
				agent.Account = "answering"
				cfg.Agents["architect"] = agent
				cfg.Accounts = map[string]config.Account{"default": {Provider: domain.BackendClaudeCode}, "answering": {Provider: named}}
				cli := &answeringCLIRunner{}
				asker := &capturingBackend{}
				parts := components{config: cfg, repository: t.TempDir(), stateRoot: t.TempDir()}
				var storeErr error
				parts.spend, storeErr = runstate.NewSpendStore(parts.stateRoot, cfg.Product.ID)
				if storeErr != nil {
					t.Fatal(storeErr)
				}
				var gotBackend domain.Backend
				var gotModel, gotAlias, gotAnswer string
				var err error
				if surface == "exchange" {
					conductor := conversationExchanges(parts, domain.RoleProductManager, asker, cli).(exchange.Conductor)
					spoken, callErr := conductor.Voice.Answer(context.Background(), answeringQuestion())
					gotBackend, gotModel, gotAlias, gotAnswer, err = spoken.Backend, spoken.Model, spoken.AccountAlias, spoken.Answer, callErr
				} else {
					side, buildErr := (preparedChat{parts: parts, provider: asker, runner: cli}).sideThreads()
					if buildErr != nil {
						t.Fatal(buildErr)
					}
					question := testSideQuestion()
					question.SessionID = ""
					spoken, callErr := side.Voice.Answer(context.Background(), question)
					gotBackend, gotModel, gotAlias, gotAnswer, err = spoken.Backend, spoken.Model, spoken.AccountAlias, spoken.Answer, callErr
				}
				if err != nil {
					t.Fatal(err)
				}
				if asker.calls != 0 {
					t.Fatalf("answer reused asking adapter %d times", asker.calls)
				}
				if gotBackend != named || gotModel != agent.Model || gotAlias != "answering" || gotAnswer != "The answer." {
					t.Fatalf("answer identity = %s/%s/%s, text=%q", gotBackend, gotModel, gotAlias, gotAnswer)
				}
				if len(cli.commands) != 1 {
					t.Fatalf("commands=%v", cli.commands)
				}
				cmd := cli.commands[0]
				args := strings.Join(cmd.Args, "\n")
				if cmd.Name != binary || !strings.Contains(args, "--sandbox\nread-only") || !strings.Contains(args, "--model\n"+agent.Model) || !strings.Contains(args, `approval_policy="never"`) {
					t.Fatalf("wrong answering invocation: %s %v", cmd.Name, cmd.Args)
				}
				account, accountErr := cfg.Endpoint(parts.stateRoot, "answering")
				if accountErr != nil {
					t.Fatal(accountErr)
				}
				if !strings.Contains(strings.Join(cmd.Env, "\n"), "CODEX_HOME="+account.Directory) {
					t.Fatalf("answer did not use configured account %q", account.Directory)
				}
			})
		}
	}
}

func TestAnsweringTurnsRefuseAProviderWithoutReadOnlyAccess(t *testing.T) {
	t.Parallel()
	cfg := answeringConfig()
	cfg.Providers = map[string]backend.ProviderPlugin{"writes-only": {Adapter: domain.BackendCodex, Binary: "custom-codex", Roles: []domain.AgentRole{domain.RoleArchitect}, Postures: []backend.Posture{backend.PostureWorktreeWrite}, Dialect: backend.DialectSpec{Rules: []backend.DialectRule{{Type: "retry", Answer: backend.AnswerRetrying}}}}}
	agent := cfg.Agents["architect"]
	agent.Backend = "writes-only"
	cfg.Agents["architect"] = agent
	for _, surface := range []string{"exchange", "side-thread"} {
		t.Run(surface, func(t *testing.T) {
			cli := &answeringCLIRunner{}
			var err error
			if surface == "exchange" {
				_, err = (exchangeVoice{config: cfg, runner: cli, repository: t.TempDir()}).Answer(context.Background(), answeringQuestion())
			} else {
				_, err = (sideVoice{config: cfg, runner: cli, repository: t.TempDir()}).Answer(context.Background(), testSideQuestion())
			}
			if err == nil || !strings.Contains(err.Error(), "read-only") {
				t.Fatalf("error=%v, want read-only capability refusal", err)
			}
			if len(cli.commands) != 0 {
				t.Fatalf("refused endpoint launched: %v", cli.commands)
			}
		})
	}
}

type answeringCLIRunner struct{ commands []execution.Command }

func (r *answeringCLIRunner) Run(_ context.Context, command execution.Command, observe execution.OutputObserver) (execution.ProcessResult, error) {
	r.commands = append(r.commands, command)
	for _, line := range []string{`{"type":"thread.started","thread_id":"answer-session"}`, `{"type":"item.completed","item":{"type":"agent_message","text":"The answer."}}`, `{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}`} {
		observe(execution.Output{Stream: execution.StreamStdout, Text: line})
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
}

func TestAnsweringSessionIdentityFollowsProviderAndAccount(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name         string
		priorBackend domain.Backend
		priorAccount string
		resume       bool
	}{
		{"same endpoint", domain.BackendCodex, "answering", true},
		{"changed provider", domain.BackendClaudeCode, "answering", false},
		{"changed account", domain.BackendCodex, "old-account", false},
		{"unknown provider", "", "answering", false},
		{"unknown account", domain.BackendCodex, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := answeringConfig()
			a := cfg.Agents["architect"]
			a.Backend = domain.BackendCodex
			a.Model = "gpt-6.1-sol"
			a.Account = "answering"
			cfg.Agents["architect"] = a
			cfg.Accounts = map[string]config.Account{"answering": {Provider: domain.BackendCodex}}
			question := answeringQuestion()
			question.SessionID = "previous-session"
			question.SessionBackend = test.priorBackend
			question.SessionAccountAlias = test.priorAccount
			question.Earlier = []exchange.Round{{Number: 1, Question: "old question", Answer: "old answer"}}
			provider := &capturingBackend{result: backend.RunResult{FinalText: "answer", SessionID: "new-session"}}
			_, err := (exchangeVoice{config: cfg, provider: provider, repository: t.TempDir()}).Answer(context.Background(), question)
			if err != nil {
				t.Fatal(err)
			}
			expected := ""
			if test.resume {
				expected = "previous-session"
			}
			if provider.request.SessionID != expected {
				t.Fatalf("exchange session=%q,want %q", provider.request.SessionID, expected)
			}
			if !strings.Contains(provider.request.Prompt, "old question") || !strings.Contains(provider.request.Prompt, "old answer") {
				t.Fatalf("exchange lost durable history: %q", provider.request.Prompt)
			}
			side := testSideQuestion()
			side.SessionID = "previous-session"
			side.SessionBackend = test.priorBackend
			side.SessionAccountAlias = test.priorAccount
			provider = &capturingBackend{result: backend.RunResult{FinalText: "answer", SessionID: "new-session"}}
			_, err = (sideVoice{config: cfg, provider: provider, repository: t.TempDir()}).Answer(context.Background(), side)
			if test.resume {
				if err != nil || provider.request.SessionID != "previous-session" {
					t.Fatalf("same endpoint side resume: err=%v session=%q", err, provider.request.SessionID)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), "start a new side thread") || provider.calls != 0 {
					t.Fatalf("foreign side session attempted: err=%v calls=%d", err, provider.calls)
				}
			}
		})
	}
}
