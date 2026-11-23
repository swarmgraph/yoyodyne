package claudecode

// Which account an invocation is actually made under.
//
// The account is a directory the provider reads its own authentication from, so
// the whole of the mechanism is one environment variable on the command. That is
// small enough to get wrong silently: an invocation that dropped it would run on
// whichever account the machine happened to be signed in as, record the alias it
// was asked for, and be indistinguishable afterwards from one that did what it
// said. So it is asserted on the command rather than inferred from the result.

import (
	"context"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func TestAnInvocationIsMadeInItsAccountsProviderHome(t *testing.T) {
	t.Parallel()

	stream := `{"type":"result","subtype":"success","session_id":"s","is_error":false,"result":"done","total_cost_usd":0.01}` + "\n"
	runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: stream}}}
	if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		ScratchDirectory: testScratch,
		WorkingDirectory: "/worktree",
		Prompt:           "implement the task",
		AccountAlias:     "second",
		AccountConfigDir: "/state/accounts/second",
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	assertConfigDir(t, runner.commands[0], "/state/accounts/second")
}

// A backend value built for one account serves it when the request names none,
// which is what a diagnosis asking one alias whether it is signed in needs:
// CheckAvailability takes no request, so the account has to be on the value.
func TestABackendBuiltForAnAccountAsksAboutThatAccount(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{results: []execution.ProcessResult{
		{Status: execution.ProcessSucceeded, Stdout: "2.1.222 (Claude Code)\n"},
		{Status: execution.ProcessSucceeded, Stdout: `{"loggedIn":true,"authMethod":"oauth"}`},
	}}
	availability, err := (Backend{Runner: runner, ConfigDir: "/state/accounts/second"}).CheckAvailability(context.Background())
	if err != nil {
		t.Fatalf("CheckAvailability() error = %v", err)
	}
	if !availability.Authenticated {
		t.Fatalf("CheckAvailability() = %#v", availability)
	}
	// Both probes are asked of that account. Asking the version of one home and
	// the authentication of another would report a home nobody has as signed in.
	for _, command := range runner.commands {
		assertConfigDir(t, command, "/state/accounts/second")
	}
}

// The request's account wins over the value's, because one backend value serves
// runs affined to different accounts and the run's own record is what says which.
func TestARequestsAccountOverridesTheOneTheBackendWasBuiltFor(t *testing.T) {
	t.Parallel()

	stream := `{"type":"result","subtype":"success","session_id":"s","is_error":false,"result":"done","total_cost_usd":0.01}` + "\n"
	runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: stream}}}
	if _, err := (Backend{Runner: runner, Clock: fixedClock{}, ConfigDir: "/state/accounts/first"}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		ScratchDirectory: testScratch,
		WorkingDirectory: "/worktree",
		Prompt:           "implement the task",
		AccountConfigDir: "/state/accounts/second",
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	assertConfigDir(t, runner.commands[0], "/state/accounts/second")
}

// An installation with one account names no provider home at all, so the
// provider reads the one it would have read anyway. This is what makes the
// account plumbing additive rather than a change every existing installation
// absorbs — what the invocation does carry beside it is the run's build cache,
// which is nobody's account.
func TestAnInvocationUnderNoNamedAccountNamesNoProviderHome(t *testing.T) {
	t.Parallel()

	stream := `{"type":"result","subtype":"success","session_id":"s","is_error":false,"result":"done","total_cost_usd":0.01}` + "\n"
	runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: stream}}}
	if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		ScratchDirectory: testScratch,
		WorkingDirectory: t.TempDir(),
		Prompt:           "implement the task",
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, entry := range runner.commands[0].Env {
		if strings.HasPrefix(entry, ProviderHomeVariable+"=") {
			t.Fatalf("an invocation naming no account was pointed at a provider home: %q", entry)
		}
	}
}

// assertConfigDir holds one command to the provider home it was supposed to run
// in. The rest of the process environment has to survive beside it — the
// provider needs PATH and HOME like any other program — so what is checked is
// that the variable is there and says the right thing, rather than that it is
// the only thing there.
func assertConfigDir(t *testing.T, command execution.Command, want string) {
	t.Helper()

	found := ""
	for _, entry := range command.Env {
		if value, ok := strings.CutPrefix(entry, ProviderHomeVariable+"="); ok {
			found = value
		}
	}
	if found != want {
		t.Fatalf("%s = %q on the command, want %q", ProviderHomeVariable, found, want)
	}
	if len(command.Env) < 2 {
		t.Fatalf("the command was given only %d environment entries, so the process environment was replaced rather than added to", len(command.Env))
	}
}
