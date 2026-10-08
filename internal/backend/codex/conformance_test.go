package codex

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// optionLine matches the line of a command's help that defines an option — the
// long name, and whether a value follows it — which is how what a command level
// accepts is read rather than written down here a second time. Only definition
// lines count, so an option a description merely mentions is not mistaken for
// one the level takes.
var optionLine = regexp.MustCompile(`^\s+(?:-[A-Za-z0-9], )?(--[A-Za-z0-9][A-Za-z0-9-]*)( <[^>]+>)?`)

// commandLine matches a subcommand listed under a help's "Commands:" heading.
var commandLine = regexp.MustCompile(`^\s+([a-z][a-z-]*)\s{2,}`)

// commandHelp is what one command level's help says it accepts: each option
// with whether it takes a value, and the subcommands beneath it.
type commandHelp struct {
	options     map[string]bool
	subcommands map[string]bool
}

func readCommandHelp(t *testing.T, source, help string) commandHelp {
	t.Helper()
	parsed := commandHelp{options: make(map[string]bool), subcommands: make(map[string]bool)}
	section := ""
	for _, line := range strings.Split(help, "\n") {
		if heading := strings.TrimSpace(line); strings.HasSuffix(heading, ":") && !strings.HasPrefix(line, " ") {
			section = heading
			continue
		}
		switch section {
		case "Options:":
			if match := optionLine.FindStringSubmatch(line); match != nil {
				parsed.options[match[1]] = match[2] != ""
			}
		case "Commands:":
			if match := commandLine.FindStringSubmatch(line); match != nil {
				parsed.subcommands[match[1]] = true
			}
		}
	}
	if len(parsed.options) == 0 {
		t.Fatalf("%s listed no recognizable options, so checking against it asserts nothing:\n%s", source, help)
	}
	return parsed
}

// commandContract is the help of each command level this backend invokes, keyed
// by the level's command words.
type commandContract map[string]commandHelp

// misplacedOption walks one invocation's arguments level by level and names the
// first option given to a level whose help does not list it. A subcommand word
// moves the walk down a level, so an option is judged by the level it was
// placed on rather than by whether any level anywhere accepts it — which is the
// difference between this and the check it replaced, which read `exec --help`
// alone and passed `--sandbox` after `resume` because `exec` takes it.
func (contract commandContract) misplacedOption(args []string) error {
	if len(args) == 0 || args[0] != "exec" {
		return fmt.Errorf("an invocation does not begin with exec: %q", args)
	}
	level := "exec"
	for index := 1; index < len(args); index++ {
		argument := args[index]
		help, known := contract[level]
		if !known {
			return fmt.Errorf("no recorded help for %q, so %q cannot be checked", level, args)
		}
		if strings.HasPrefix(argument, "-") && argument != "-" {
			takesValue, listed := help.options[argument]
			if !listed {
				return fmt.Errorf("%q is passed to %q, whose help does not list it; the CLI refuses the whole invocation", argument, level)
			}
			if takesValue {
				index++
			}
			continue
		}
		if help.subcommands[argument] {
			level += " " + argument
		}
	}
	return nil
}

// optionLevel is the command level an option was given to, or "" where it was
// not given at all.
func optionLevel(contract commandContract, args []string, option string) string {
	level := "exec"
	for _, argument := range args[1:] {
		if argument == option {
			return level
		}
		if contract[level].subcommands[argument] {
			level += " " + argument
		}
	}
	return ""
}

// invocationsToCheck is every shape of command line this backend builds, from
// requests that set each optional field, so no branch of the argument assembly
// goes unasked. They come from the backend rather than from a list repeated
// here, so an option added later is checked without anybody remembering to.
func invocationsToCheck(t *testing.T) map[string][]string {
	t.Helper()
	invocations := make(map[string][]string)
	for _, role := range []domain.AgentRole{domain.RoleDeveloper, domain.RoleReviewer} {
		for name, sessionID := range map[string]string{"a fresh session": "", "a resumed session": "session-1"} {
			repository, worktree := sandboxRepository(t, true)
			runner := &fakeRunner{results: []execution.ProcessResult{{
				Status: execution.ProcessSucceeded,
				Stdout: lines(`{"id":"0","msg":{"type":"task_complete","last_agent_message":"ok"}}`),
			}}}
			if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
				RunID:            testRunID,
				Role:             role,
				WorkingDirectory: worktree,
				RepositoryRoot:   repository,
				Prompt:           "do the work",
				SystemPrompt:     "the contract",
				SessionID:        sessionID,
				Model:            "gpt-6.1-sol",
			}); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			invocations[string(role)+" "+name] = runner.commands[0].Args
		}
	}
	return invocations
}

// assertTheCommandContractHolds is the check itself: every invocation places
// each option on a level that lists it, and none is without its permission
// profile. The second half is not implied by the first — dropping the profile
// would satisfy any help there is — and a resumed session that could not be
// given its profile would run under whatever it was started with. `--sandbox`
// is refused outright, because the CLI lets it replace the selected profile.
func assertTheCommandContractHolds(t *testing.T, source string, contract commandContract) {
	t.Helper()
	for name, args := range invocationsToCheck(t) {
		if err := contract.misplacedOption(args); err != nil {
			t.Errorf("%s, checked against %s: %v", name, source, err)
		}
		selected := false
		for _, value := range configValues(args, "exec") {
			selected = selected || strings.HasPrefix(value, "default_permissions=")
		}
		if !selected {
			t.Errorf("%s is made without a permission profile ahead of resume: %q", name, args)
		}
		if level := optionLevel(contract, args, "--sandbox"); level != "" {
			t.Errorf("%s passes --sandbox to %q, which replaces its permission profile: %q", name, level, args)
		}
	}
}

// recordedContract is the help recorded from a genuine Codex CLI under
// testdata/cli-help, whose README says which one and where it came from.
func recordedContract(t *testing.T) commandContract {
	t.Helper()
	contract := make(commandContract)
	for level, file := range map[string]string{"exec": "exec.txt", "exec resume": "exec-resume.txt"} {
		help, err := os.ReadFile(filepath.Join("testdata", "cli-help", file))
		if err != nil {
			t.Fatalf("ReadFile() error = %v", err)
		}
		contract[level] = readCommandHelp(t, file, string(help))
	}
	return contract
}

// Every option this backend passes has to be one the command level it is given
// to accepts, and a test driving a fake process cannot tell. A misplaced option
// is not ignored: the CLI refuses the whole invocation before it reaches the
// provider, so an option on the wrong level fails every invocation of that
// shape for every role. This is the check against the recorded help, so it runs
// everywhere, Codex installed or not.
func TestEveryInvocationKeepsTheRecordedCLIsCommandContract(t *testing.T) {
	t.Parallel()

	assertTheCommandContractHolds(t, "the recorded help", recordedContract(t))
}

// The check has to be able to fail. The sequence below is the one every resumed
// session was made with until it was found refused: `--sandbox` after `resume`,
// which `exec` takes and `exec resume` does not. The adapter no longer passes
// `--sandbox` anywhere, but it is still the option whose placement was wrong.
func TestTheCommandContractCheckRefusesAnOptionOnALevelThatDoesNotListIt(t *testing.T) {
	t.Parallel()

	refused := []string{"exec", "resume", "session-1", "--json", "--skip-git-repo-check", "--sandbox", "workspace-write", "--model", "gpt-6.1-sol", "-"}
	err := recordedContract(t).misplacedOption(refused)
	if err == nil || !strings.Contains(err.Error(), `"--sandbox" is passed to "exec resume"`) {
		t.Fatalf("misplacedOption() error = %v, want it to name --sandbox on exec resume", err)
	}
}

// The same check against the Codex installed on the machine running it, where
// there is one, because the recorded help is one version's and the installed
// CLI is the one a run will actually meet. Asking for help makes no provider
// call and needs no account, so it is gated on the CLI being installed rather
// than on opting in, and skips where it is not.
func TestTheInstalledCLIKeepsTheCommandContract(t *testing.T) {
	t.Parallel()

	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Skipf("Codex is not installed, so what it accepts cannot be asked here; the recorded help is checked instead: %v", err)
	}
	contract := make(commandContract)
	for level, words := range map[string][]string{"exec": {"exec"}, "exec resume": {"exec", "resume"}} {
		// Standard output alone, because the CLI may warn on standard error about
		// things that are not its help.
		help, err := exec.Command(binary, append(words, "--help")...).Output()
		if err != nil {
			t.Fatalf("codex %s --help error = %v", level, err)
		}
		contract[level] = readCommandHelp(t, binary+" "+level+" --help", string(help))
	}
	assertTheCommandContractHolds(t, binary, contract)
}

// The stream vocabulary this adapter reads was taken from the provider's
// documented protocol rather than from a recorded run, so the one thing the unit
// tests cannot show is that a real CLI still speaks it. This is where that is
// checked, against whatever Codex is installed on the machine running it.
//
// It is opt-in for the reason the Claude Code conformance check is: it starts a
// real provider, spends real capacity, and depends on an account this repository
// does not own. What it is not is optional evidence — a change to the flags, the
// sandbox mapping, or the event names is a change nothing else here can catch.
func TestLocalConformance(t *testing.T) {
	if os.Getenv("YOYODYNE_CODEX_CONFORMANCE") != "1" {
		t.Skip("set YOYODYNE_CODEX_CONFORMANCE=1 to run against the installed Codex CLI")
	}
	provider := Backend{Runner: execution.OSProcessRunner{}}
	availability, err := provider.CheckAvailability(context.Background())
	if err != nil {
		t.Fatalf("CheckAvailability() error = %v", err)
	}
	if !availability.Installed || !availability.Authenticated {
		t.Skipf("Codex unavailable or unauthenticated: %#v", availability)
	}

	// The developer, because its permission profile is the one that writes:
	// a turn that completes under it shows the profile's directories were
	// accepted by a real CLI.
	result, err := provider.Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol",
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: t.TempDir(),
		Prompt:           "Reply with exactly: ok",
		Timeout:          5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.IsError || result.FinalText == "" {
		t.Fatalf("Run() result = %#v", result)
	}
	// The session is what a later invocation resumes, and the resolved model is
	// the only durable evidence of what actually served this one. A stream whose
	// vocabulary has moved on still produces a terminal from the process exit,
	// so these two are what actually show the events were read.
	if result.SessionID == "" || result.ResolvedModel == "" {
		t.Fatalf("Run() read no session or model from the stream: %#v", result)
	}
}
