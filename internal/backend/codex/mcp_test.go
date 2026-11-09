package codex

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// mcpServersSetting is the `--config` value an invocation turns its MCP
// servers off with, and whether it has one.
func mcpServersSetting(args []string) (string, bool) {
	for _, value := range configValues(args, "exec") {
		if strings.HasPrefix(value, "mcp_servers=") {
			return value, true
		}
	}
	return "", false
}

// withoutMCPServersSetting is args without the setting that turns the servers
// off, which is the invocation as it was made before it had one.
func withoutMCPServersSetting(args []string) []string {
	var kept []string
	for index := 0; index < len(args); index++ {
		if args[index] == "--config" && index+1 < len(args) && strings.HasPrefix(args[index+1], "mcp_servers=") {
			index++
			continue
		}
		kept = append(kept, args[index])
	}
	return kept
}

func TestTheListingIsAskedWithTheTurnsOwnConfigurationOnly(t *testing.T) {
	t.Parallel()
	args := []string{"exec", "--config", "a=1", "--disable", "plugins", "--enable", "x",
		"--cd", "/work", "resume", "session-one", "--json", "--config", "after=1", "--model", "m", "-"}
	want := []string{"mcp", "list", "--json", "--config", "a=1", "--disable", "plugins", "--enable", "x"}
	if got := mcpListArgs(args); !reflect.DeepEqual(got, want) {
		t.Fatalf("mcpListArgs() = %q, want %q", got, want)
	}
}

func TestTheListingsServerNamesAreRead(t *testing.T) {
	t.Parallel()
	names, err := readMCPServerNames(`[{"name":"a","enabled":true},{"name":"b c","enabled":false},{"name":"a"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b c"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %q, want %q", names, want)
	}
	for _, listing := range []string{"", "No MCP servers configured yet.", `[{"enabled":true}]`, `{"name":"a"}`} {
		if _, err := readMCPServerNames(listing); err == nil {
			t.Errorf("readMCPServerNames(%q) read a listing it cannot have understood", listing)
		}
	}
}

// Every name is turned off in one inline table, quoted, so a name with a space
// or a dot is one key rather than a path the CLI would split.
func TestEveryListedServerIsTurnedOffInOneTable(t *testing.T) {
	t.Parallel()
	got := mcpServersOffArgs([]string{"plain", "with space", "with.dot", `with "quote"`})
	want := []string{"--config", `mcp_servers={"plain"={enabled=false},"with space"={enabled=false},"with.dot"={enabled=false},"with \"quote\""={enabled=false}}`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mcpServersOffArgs() = %q, want %q", got, want)
	}
	if got := mcpServersOffArgs(nil); got != nil {
		t.Fatalf("mcpServersOffArgs(nil) = %q, want no setting", got)
	}
}

// A developer's turn, fresh and resumed, turns off every server the CLI lists
// for it, on `exec` ahead of `resume` where the command contract allows it, and
// its permission profile, cache, and scratch grants are what they would have
// been with no server listed.
func TestADevelopersTurnTurnsOffEveryServerTheCLIListsForIt(t *testing.T) {
	t.Parallel()
	repository, worktree := sandboxRepository(t, true)
	for _, session := range []string{"", "session-one"} {
		plain := sandboxCommand(t, repository, worktree, session, domain.RoleDeveloper)
		if _, found := mcpServersSetting(plain.Args); found {
			t.Fatalf("a turn with no server listed carries an MCP setting: %q", plain.Args)
		}
		runner := &fakeRunner{
			mcpListing: `[{"name":"account server","enabled":true},{"name":"project.server","enabled":false}]`,
			results: []execution.ProcessResult{{Status: execution.ProcessSucceeded,
				Stdout: lines(`{"id":"0","msg":{"type":"task_complete","last_agent_message":"ok"}}`)}},
		}
		if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol",
			RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: worktree,
			RepositoryRoot: repository, Prompt: "do the work", SessionID: session,
		}); err != nil {
			t.Fatal(err)
		}
		if len(runner.listings) != 1 || len(runner.commands) != 1 {
			t.Fatalf("session %q: %d listings and %d turns, want one of each", session, len(runner.listings), len(runner.commands))
		}
		listing, turn := runner.listings[0], runner.commands[0]
		if listing.Dir != turn.Dir || !reflect.DeepEqual(listing.Env, turn.Env) {
			t.Fatalf("session %q: the listing was asked in %q with %q, the turn made in %q with %q", session, listing.Dir, listing.Env, turn.Dir, turn.Env)
		}
		if !reflect.DeepEqual(listing.Args, mcpListArgs(turn.Args)) && !reflect.DeepEqual(listing.Args, mcpListArgs(withoutMCPServersSetting(turn.Args))) {
			t.Fatalf("session %q: the listing %q was not asked with the turn's configuration %q", session, listing.Args, turn.Args)
		}
		setting, found := mcpServersSetting(turn.Args)
		if want := `mcp_servers={"account server"={enabled=false},"project.server"={enabled=false}}`; !found || setting != want {
			t.Fatalf("session %q: MCP setting = %q, want %q", session, setting, want)
		}
		if err := recordedContract(t).misplacedOption(turn.Args); err != nil {
			t.Fatal(err)
		}
		if got, want := profileFilesystem(t, turn.Args), profileFilesystem(t, plain.Args); !reflect.DeepEqual(got, want) {
			t.Fatalf("session %q: the profile with servers listed is %q, without %q", session, got, want)
		}
		if got, want := withProfileNamesFixed(withoutMCPServersSetting(turn.Args)), withProfileNamesFixed(plain.Args); !reflect.DeepEqual(got, want) {
			t.Fatalf("session %q: turning the servers off changed the rest of the command line:\n got %q\nwant %q", session, got, want)
		}
	}
}

// A listing that fails, or says something that is not a listing, refuses the
// turn: it is never made with servers nobody looked for.
func TestADeveloperTurnIsRefusedWhenTheServersCannotBeListed(t *testing.T) {
	t.Parallel()
	repository, worktree := sandboxRepository(t, true)
	for name, runner := range map[string]execution.ProcessRunner{
		"unreadable": &fakeRunner{mcpListing: "Error: something", results: completedTurn()},
		"failed":     &failingListingRunner{},
	} {
		_, err := (Backend{Runner: runner}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol",
			RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: worktree,
			RepositoryRoot: repository, Prompt: "do the work",
		})
		if err == nil || !strings.Contains(err.Error(), "MCP servers") {
			t.Fatalf("%s listing: Run() error = %v, want the turn refused for the listing", name, err)
		}
		if fake, ok := runner.(*fakeRunner); ok && len(fake.commands) != 0 {
			t.Fatalf("%s listing: the turn was made anyway", name)
		}
		if failing, ok := runner.(*failingListingRunner); ok && failing.turns != 0 {
			t.Fatalf("%s listing: the turn was made anyway", name)
		}
	}
}

type failingListingRunner struct{ turns int }

func (r *failingListingRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	if isMCPListing(command) {
		return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "Error: failed to load configuration\n"}, nil
	}
	r.turns++
	return execution.ProcessResult{}, errors.New("the turn was made")
}

// A read-only role ignores the account's configuration and launches from an
// empty directory, so it has no servers to list and asks for none.
func TestAReadOnlyRoleAsksForNoListing(t *testing.T) {
	t.Parallel()
	repository, worktree := sandboxRepository(t, true)
	runner := &fakeRunner{results: completedTurn()}
	if _, err := (Backend{Runner: runner}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol",
		RunID: testRunID, Role: domain.RoleReviewer, WorkingDirectory: worktree,
		RepositoryRoot: repository, Prompt: "review",
	}); err != nil {
		t.Fatal(err)
	}
	if len(runner.listings) != 0 {
		t.Fatalf("a reviewer asked for %d listings", len(runner.listings))
	}
}

// listingRunner asks the installed CLI for the listing and makes no turn: the
// turn's command is kept for the test to ask the CLI about.
type listingRunner struct {
	turn execution.Command
}

func (r *listingRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if isMCPListing(command) {
		return execution.OSProcessRunner{}.Run(ctx, command, observer)
	}
	r.turn = command
	if command.Stdin != nil {
		_, _ = command.Stdin.Read(make([]byte, 1))
	}
	observer(execution.Output{Stream: execution.StreamStdout, Text: `{"type":"thread.started","thread_id":"session-1"}`})
	observer(execution.Output{Stream: execution.StreamStdout, Text: `{"type":"turn.completed"}`})
	return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
}

// The installed CLI is asked, without a provider call, whether a developer's
// turn as the adapter builds it, fresh and resumed, would start an MCP server
// that the account's config.toml or a trusted worktree's .codex/config.toml
// lists. Each server is a script that records being started, and
// `codex debug prompt-input` opens the session — servers included — without
// reaching a provider. The control asks the same with the adapter's MCP setting
// taken off, and has to see both servers start: without that the check would
// pass on servers the CLI never read. The worktree's server has a dot in its
// name, which a dotted `--config` path cannot reach. It skips where Codex is
// not installed.
func TestTheInstalledCLIStartsNoMCPServerForADeveloper(t *testing.T) {
	t.Parallel()

	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Skipf("Codex is not installed, so what it would start cannot be asked here: %v", err)
	}
	for _, session := range []string{"", "session-one"} {
		t.Run("session "+session, func(t *testing.T) {
			t.Parallel()
			repository, worktree := sandboxRepositoryNamed(t, true, "repository with spaces")
			physical, err := filepath.EvalSymlinks(worktree)
			if err != nil {
				t.Fatal(err)
			}
			home, servers := t.TempDir(), t.TempDir()
			server := func(name string) string {
				script := filepath.Join(servers, name+".sh")
				body := "#!/bin/sh\necho started >> " + shellQuote(filepath.Join(servers, name+".started")) + "\ncat >/dev/null\n"
				if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
					t.Fatal(err)
				}
				return script
			}
			account := "[mcp_servers.account_server]\ncommand = " + tomlString(server("account")) + "\n"
			for _, directory := range []string{worktree, physical} {
				account += "[projects." + tomlString(directory) + "]\ntrust_level = \"trusted\"\n"
			}
			if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(account), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(worktree, ".codex"), 0o755); err != nil {
				t.Fatal(err)
			}
			project := "[mcp_servers.\"project.server\"]\ncommand = " + tomlString(server("project")) + "\n"
			if err := os.WriteFile(filepath.Join(worktree, ".codex", "config.toml"), []byte(project), 0o600); err != nil {
				t.Fatal(err)
			}

			runner := &listingRunner{}
			if _, err := (Backend{Binary: binary, Runner: runner, ConfigDir: home}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol",
				RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: worktree,
				RepositoryRoot: repository, Prompt: "do the work", SessionID: session,
			}); err != nil {
				t.Fatal(err)
			}
			if _, found := mcpServersSetting(runner.turn.Args); !found {
				t.Fatalf("the CLI listed no server for the adapter to turn off: %q", runner.turn.Args)
			}

			started := func(args []string) []string {
				for _, name := range []string{"account", "project"} {
					_ = os.Remove(filepath.Join(servers, name+".started"))
				}
				openSession(t, binary, runner.turn, args)
				var names []string
				for _, name := range []string{"account", "project"} {
					if _, err := os.Stat(filepath.Join(servers, name+".started")); err == nil {
						names = append(names, name)
					}
				}
				return names
			}
			if names := started(runner.turn.Args); len(names) != 0 {
				t.Fatalf("a developer's turn started the MCP servers %q", names)
			}
			if names := started(withoutMCPServersSetting(runner.turn.Args)); !slices.Equal(names, []string{"account", "project"}) {
				t.Fatalf("without the adapter's setting the CLI started %q, not both servers, so this check shows nothing about the files it reads", names)
			}
		})
	}
}

// openSession opens the session the turn's configuration describes, as
// `codex debug prompt-input`, in the turn's directory and environment. Opening
// it starts its MCP servers; it reaches no provider.
func openSession(t *testing.T, binary string, turn execution.Command, args []string) {
	t.Helper()
	probe := []string{"debug", "prompt-input"}
	for index := 1; index < len(args); index++ {
		switch args[index] {
		case "resume":
			index = len(args)
		case "--config", "--enable", "--disable":
			probe = append(probe, args[index], args[index+1])
			index++
		}
	}
	command := exec.Command(binary, append(probe, "hello")...)
	command.Dir = turn.Dir
	command.Env = turn.Env
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("codex debug prompt-input: %v\n%s", err, output)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
