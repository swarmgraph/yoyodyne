package claudecode

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

// longFlag matches an option as the CLI's own help lists it, which is how the
// set of flags it knows is read rather than written down here a second time.
var longFlag = regexp.MustCompile(`--[A-Za-z0-9][A-Za-z0-9-]*`)

// Every flag this backend passes has to be one the installed CLI knows, and a
// test driving a fake process cannot tell a correct flag from a misspelled one.
// An unknown option is not ignored: Claude Code refuses the whole invocation
// before it reaches the provider, so a flag wrong here -- or right but newer
// than the installed CLI -- fails every invocation of every role that receives
// it. For the flags on the read-only branch that is the reviewer, the product
// manager, the architect, and the development manager at once, and the first
// evidence of it would be a day of runs that cannot be reviewed.
//
// The arguments come from the backend rather than from a list repeated here, so
// a flag added later is covered without anybody remembering to cover it. Unlike
// the conformance run above this costs nothing -- `--help` makes no provider
// call and needs no account -- so it is gated on the CLI being installed rather
// than on opting in, and skips where it is not.
func TestTheInstalledCLIKnowsEveryFlagThisBackendPasses(t *testing.T) {
	t.Parallel()

	binary, err := exec.LookPath("claude")
	if err != nil {
		t.Skipf("Claude Code is not installed, so what it accepts cannot be asked here: %v", err)
	}
	help, err := exec.Command(binary, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("claude --help error = %v: %s", err, help)
	}
	known := make(map[string]bool)
	for _, flag := range longFlag.FindAllString(string(help), -1) {
		known[flag] = true
	}
	if !known["--print"] && !known["--output-format"] {
		t.Fatalf("claude --help listed no recognizable options, so this asserts nothing: %s", help)
	}

	// Every role this backend serves, and a request that sets each optional
	// field, so no branch of the argument assembly goes unasked.
	for _, role := range []domain.AgentRole{
		domain.RoleDeveloper, domain.RoleReviewer,
		domain.RoleProductManager, domain.RoleArchitect, domain.RoleDevelopmentManager,
	} {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()

			stream := `{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"ok"}` + "\n"
			runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: stream}}}
			if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
				RunID:            testRunID,
				Role:             role,
				WorkingDirectory: "/worktree",
				Prompt:           "do the work",
				SystemPrompt:     "the contract",
				SessionID:        "session-1",
				Model:            "claude-opus-5",
			}); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			for _, argument := range runner.commands[0].Args {
				// A flag is an argument that begins with one, so a value that
				// happens to contain a dash pair -- a settings document, a prompt --
				// is never mistaken for an option the CLI has to know.
				if !strings.HasPrefix(argument, "--") {
					continue
				}
				if !known[argument] {
					t.Fatalf("a %s invocation passes %q, which %s does not list; an unknown option makes the CLI refuse the whole invocation",
						role, argument, binary)
				}
			}
		})
	}
}

// conformanceBackend is the gate every check against the installed CLI shares:
// opted into because each one spends a provider invocation, and skipped where
// the CLI is absent or nobody is signed in.
func conformanceBackend(t *testing.T) Backend {
	t.Helper()

	if os.Getenv("YOYODYNE_CLAUDE_CONFORMANCE") != "1" {
		t.Skip("set YOYODYNE_CLAUDE_CONFORMANCE=1 to run against the installed Claude Code CLI")
	}
	provider := Backend{Runner: execution.OSProcessRunner{}}
	availability, err := provider.CheckAvailability(context.Background())
	if err != nil {
		t.Fatalf("CheckAvailability() error = %v", err)
	}
	if !availability.Installed || !availability.Authenticated {
		t.Skipf("Claude Code unavailable or unauthenticated: %#v", availability)
	}
	return provider
}

func TestLocalConformance(t *testing.T) {
	provider := conformanceBackend(t)
	result, err := provider.Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: t.TempDir(),
		Prompt:           "Reply with exactly: ok",
		AllowedTools:     []string{},
		Timeout:          2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.IsError || result.SessionID == "" || result.FinalText == "" {
		t.Fatalf("Run() result = %#v", result)
	}

	// A read-only role is invoked with flags the developer's is not, and the one
	// that keeps its prompt prefix stable is the newest of them. An installed CLI
	// that does not know a flag refuses the whole invocation rather than ignoring
	// it, so this is where a version too old to carry it surfaces -- as one
	// skipped conformance run rather than as every review of the day failing.
	advisory, err := provider.Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleReviewer,
		WorkingDirectory: t.TempDir(),
		Prompt:           "Reply with exactly: ok",
		AllowedTools:     []string{},
		Timeout:          2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Run() as a reviewer error = %v", err)
	}
	if advisory.IsError || advisory.FinalText == "" {
		t.Fatalf("Run() as a reviewer result = %#v", advisory)
	}
}

// The harness depends on two things about a developer run's sandbox that it
// does not control, does not assert anywhere else, and that no fake process can
// demonstrate: that the run may read an absolute path outside its worktree, and
// that it may write inside the Git directory of the repository that worktree
// belongs to. Both are the provider's behaviour rather than this backend's, and
// a Claude Code release that tightened either would break runs with nothing
// going red -- an in-run sweep that reads the primary checkout would stop
// finding what it was told to read, and the build cache internal/execution
// points into `.git` would fail every Go command at setup with "operation not
// permitted", which reads as a broken toolchain.
//
// The two probes below are what makes such a release fail a named check
// instead. They are opt-in for the same reason as the conformance run above:
// each spends a provider invocation.

func TestLocalConformanceARunReadsAnAbsolutePathOutsideItsWorktree(t *testing.T) {
	provider := conformanceBackend(t)
	fixture := newSandboxProbeFixture(t)

	// A token nothing but the file can supply, so a reply carrying it is evidence
	// of the read rather than of a plausible guess about a path it was handed.
	token := probeToken(t)
	outside := filepath.Join(fixture.checkout, "outside-the-worktree.txt")
	if err := os.WriteFile(outside, []byte(token+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	result, err := provider.Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: fixture.worktree,
		// Every tool a developer run is granted except the shell, so the read has
		// to go through the grant this pins rather than through a `cat` the
		// OS-level sandbox governs instead.
		AllowedTools: developerToolsWithoutBash(),
		Prompt: "Read the file at the absolute path " + outside + ". " +
			"Reply with exactly the one line it contains and nothing else. " +
			"If you could not read it, reply with the error you were given.",
		Timeout: 2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(result.FinalText, token) {
		t.Fatalf("a developer run could not read %s, so this backend's unscoped Read grant no longer reaches outside the worktree; the run reported: %q",
			outside, result.FinalText)
	}
}

func TestLocalConformanceARunWritesInsideItsRepositorysGitDirectory(t *testing.T) {
	provider := conformanceBackend(t)
	fixture := newSandboxProbeFixture(t)

	// Under the directory the harness points every run's Go build cache at, and
	// reachable by nothing else the run holds: it is outside the worktree the run
	// works in and outside the temporary directory the fixture gave it.
	token := probeToken(t)
	written := filepath.Join(fixture.gitDirectory, "yoyodyne", "sandbox-probe", token)
	command := "mkdir -p '" + filepath.Dir(written) + "' && printf ok > '" + written + "'"

	result, err := provider.Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: fixture.worktree,
		// The shell alone, because the write this pins is one a subprocess makes:
		// the Go command creating its own cache, not a tool call.
		AllowedTools: []string{"Bash"},
		Prompt: "Run this exact command with Bash and nothing else: " + command + "\n" +
			"Then reply with exactly ok if it succeeded, or with the error it printed if it did not.",
		Timeout: 2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// What the run says it did is context for a failure; what it actually wrote is
	// the evidence, so the filesystem is asked rather than the reply parsed.
	content, readErr := os.ReadFile(written)
	if readErr != nil {
		t.Fatalf("a developer run wrote nothing at %s, so the sandbox no longer grants the repository's Git directory and every run's build cache fails at setup: %v; the run reported: %q",
			written, readErr, result.FinalText)
	}
	if strings.TrimSpace(string(content)) != "ok" {
		t.Fatalf("%s = %q, want the probe's own content; the run reported: %q", written, content, result.FinalText)
	}
}

// Both probes are worth exactly what their fixture is: a Git directory that sat
// inside the worktree, or a marker the worktree carried its own copy of, would
// make one of them pass while proving nothing about any grant. This asks the
// fixture for those properties directly, and it needs no provider -- so it runs
// under the declared checks, where the probes themselves skip.
func TestTheSandboxProbeFixturePutsItsTargetsOutsideTheWorktree(t *testing.T) {
	fixture := newSandboxProbeFixture(t)

	common := probeGitOutput(t, fixture.worktree, "rev-parse", "--git-common-dir")
	if !filepath.IsAbs(common) {
		common = filepath.Join(fixture.worktree, common)
	}
	if resolved, want := resolvedPath(t, common), resolvedPath(t, fixture.gitDirectory); resolved != want {
		t.Fatalf("the worktree's Git common directory = %s, want %s; the write probe would be aiming at the wrong directory", resolved, want)
	}
	if within(t, fixture.gitDirectory, fixture.worktree) {
		t.Fatalf("%s is inside the worktree, so writing there proves only that the working directory is granted", fixture.gitDirectory)
	}

	// The name the read probe uses, so what is asserted here is the file that
	// probe reads rather than a stand-in for it.
	marker := "outside-the-worktree.txt"
	if err := os.WriteFile(filepath.Join(fixture.checkout, marker), []byte("probe\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(fixture.worktree, marker)); err == nil {
		t.Fatalf("the worktree carries its own %s, so reading the checkout's copy would prove nothing", marker)
	}

	// The temporary directory a run is given is granted alongside its working
	// directory, so it has to stay inside the worktree for the two targets above
	// to be reachable only by the grants under test.
	if temporary := os.Getenv("TMPDIR"); !within(t, temporary, fixture.worktree) {
		t.Fatalf("the run's temporary directory = %s, want one inside %s", temporary, fixture.worktree)
	}
}

// sandboxProbeFixture is the shape a developer run actually has: a checkout, and
// a linked worktree of it that the run works in. The checkout beside the
// worktree stands in for the primary checkout a run reads from, and its `.git`
// is the Git directory a run's build cache goes into -- both outside the
// worktree, which is what makes the two probes probe anything.
type sandboxProbeFixture struct {
	checkout     string
	worktree     string
	gitDirectory string
}

func newSandboxProbeFixture(t *testing.T) sandboxProbeFixture {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed, so a linked worktree cannot be built here: %v", err)
	}
	root := t.TempDir()
	checkout := filepath.Join(root, "checkout")
	if err := os.MkdirAll(checkout, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	probeGit(t, checkout, "init", "-b", "main")
	probeGit(t, checkout, "config", "user.name", "Yoyodyne Test")
	probeGit(t, checkout, "config", "user.email", "yoyodyne@example.invalid")
	// Git otherwise hands this repository to a detached maintenance process that
	// outlives the command starting it and is still working inside `.git` when Go
	// deletes the temporary directory underneath it.
	probeGit(t, checkout, "config", "maintenance.auto", "false")
	probeGit(t, checkout, "config", "gc.auto", "0")
	if err := os.WriteFile(filepath.Join(checkout, "README.txt"), []byte("probe\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	probeGit(t, checkout, "add", "README.txt")
	probeGit(t, checkout, "commit", "-m", "initial")

	worktree := filepath.Join(root, "worktree")
	probeGit(t, checkout, "worktree", "add", "--detach", worktree)
	// Registered after the TempDir call above and therefore run before Go's
	// removal, so the registration comes off while the checkout still exists.
	t.Cleanup(func() {
		_ = exec.Command("git", "-C", checkout, "worktree", "remove", "--force", worktree).Run()
	})

	// A run's sandbox grants its temporary directory as well as its working
	// directory, so a probe target under the machine's own would say nothing
	// about either grant under test here. Pointing the run's temporary directory
	// inside its worktree makes that grant a subset of one it already holds,
	// which leaves the checkout beside it reachable only by the grants probed.
	runTemporary := filepath.Join(worktree, ".probe-tmp")
	if err := os.MkdirAll(runTemporary, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	t.Setenv("TMPDIR", runTemporary)

	return sandboxProbeFixture{
		checkout:     checkout,
		worktree:     worktree,
		gitDirectory: filepath.Join(checkout, ".git"),
	}
}

// developerToolsWithoutBash is what the backend grants a developer, minus the
// shell. Taking it from the grant itself rather than restating it means a change
// that scoped `Read` to the worktree is caught here too, from this side.
func developerToolsWithoutBash() []string {
	tools := make([]string, 0, len(developerTools))
	for _, tool := range developerTools {
		if tool == "Bash" {
			continue
		}
		tools = append(tools, tool)
	}
	return tools
}

func probeToken(t *testing.T) string {
	t.Helper()

	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		t.Fatalf("rand.Read() error = %v", err)
	}
	return hex.EncodeToString(value)
}

func probeGit(t *testing.T, repository string, args ...string) {
	t.Helper()

	probeGitOutput(t, repository, args...)
}

func probeGitOutput(t *testing.T, repository string, args ...string) string {
	t.Helper()

	output, err := exec.Command("git", append([]string{"-C", repository}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v error = %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

// within reports whether a path is the root or sits under it, comparing what the
// paths resolve to: a temporary directory on macOS is reached through a symlink,
// so comparing the names alone would answer no to paths that are the same place.
func within(t *testing.T, path, root string) bool {
	t.Helper()

	if strings.TrimSpace(path) == "" {
		return false
	}
	relative, err := filepath.Rel(resolvedPath(t, root), resolvedPath(t, path))
	if err != nil {
		return false
	}
	return relative == "." || !strings.HasPrefix(relative, "..")
}

func resolvedPath(t *testing.T, path string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return resolved
}
