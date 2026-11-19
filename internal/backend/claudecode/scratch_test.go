package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// A developer's sandbox is given its own scratch folder in the state directory
// as the one write grant the harness adds, on a fresh launch and on a resume
// alike, and nothing else in the state directory: not the state directory
// itself, another run's folder, or the run records beside them
// (docs/decisions/developer-run-scratch-outside-git.md).
func TestADeveloperSandboxGrantsItsOwnScratchAndNothingElseInTheStateDirectory(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	scratch, err := execution.PrepareScratchDirectory(state, testRunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := execution.PrepareScratchDirectory(state, "run-another"); err != nil {
		t.Fatal(err)
	}
	worktree := t.TempDir()
	for _, session := range []string{"", "session-one"} {
		runner := &fakeRunner{results: claudeCompletedTurn()}
		if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
			RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: worktree,
			ScratchDirectory: scratch, Prompt: "do the work", SessionID: session,
		}); err != nil {
			t.Fatal(err)
		}
		settings, _ := optionValue(runner.commands[0].Args, "--settings")
		var decoded struct {
			Sandbox struct {
				Enabled                  bool           `json:"enabled"`
				AllowUnsandboxedCommands bool           `json:"allowUnsandboxedCommands"`
				Filesystem               map[string]any `json:"filesystem"`
			} `json:"sandbox"`
		}
		if err := json.Unmarshal([]byte(settings), &decoded); err != nil {
			t.Fatal(err)
		}
		if !decoded.Sandbox.Enabled || decoded.Sandbox.AllowUnsandboxedCommands {
			t.Fatalf("the developer's sandbox is not on and binding: %s", settings)
		}
		if len(decoded.Sandbox.Filesystem) != 1 {
			t.Fatalf("the developer's sandbox file system settings = %v, want allowWrite alone", decoded.Sandbox.Filesystem)
		}
		if got := sandboxWrites(t, settings); !slices.Equal(got, []string{scratch}) {
			t.Fatalf("the developer's sandbox may write %q, want only its scratch folder %q", got, scratch)
		}
		resolvedState, _ := filepath.EvalSymlinks(state)
		for _, path := range []string{resolvedState, filepath.Join(resolvedState, "scratch"), filepath.Join(resolvedState, "scratch", "run-another")} {
			if strings.Contains(settings, `"`+path+`"`) {
				t.Fatalf("the developer's settings name %q, beside its own scratch: %s", path, settings)
			}
		}
	}

	// A read-only role is given no scratch grant whatever the request carries.
	runner := &fakeRunner{results: claudeCompletedTurn()}
	if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID: testRunID, Role: domain.RoleReviewer, WorkingDirectory: worktree,
		ScratchDirectory: scratch, Prompt: "review the work",
	}); err != nil {
		t.Fatal(err)
	}
	if settings, _ := optionValue(runner.commands[0].Args, "--settings"); strings.Contains(settings, "allowWrite") {
		t.Fatalf("a reviewer was given a write grant: %s", settings)
	}
}

// A scratch directory that is not this run's own folder is refused before the
// CLI starts, with the reason, rather than granted or quietly dropped.
func TestADeveloperIsRefusedAScratchThatIsNotItsOwnFolder(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	if _, err := execution.PrepareScratchDirectory(state, testRunID); err != nil {
		t.Fatal(err)
	}
	other, err := execution.PrepareScratchDirectory(state, "run-another")
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(state)
	for name, scratch := range map[string]string{
		"another run's folder": other,
		"the state directory":  resolved,
		"the scratch folders":  filepath.Join(resolved, "scratch"),
	} {
		runner := &fakeRunner{results: claudeCompletedTurn()}
		_, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
			RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: t.TempDir(),
			ScratchDirectory: scratch, Prompt: "do the work",
		})
		if err == nil || !strings.Contains(err.Error(), "scratch directory") || len(runner.commands) != 0 {
			t.Fatalf("%s: Run() = %v, commands = %d, want a refusal naming the scratch directory before the CLI starts", name, err, len(runner.commands))
		}
	}
}

// The grant above is only as good as the installed CLI's sandbox honouring it,
// which no fake process can show. This asks a real developer session to write
// its own scratch folder, another run's, a run record's folder, and the state
// directory itself, with one command: the write to its own folder landing is
// what makes the others' absence mean the sandbox refused them. It spends a
// provider invocation, so it is opt-in like the other conformance probes.
func TestLocalConformanceADeveloperWritesOnlyItsOwnScratchInTheStateDirectory(t *testing.T) {
	provider := conformanceBackend(t)
	fixture := newSandboxProbeFixture(t)

	state := filepath.Join(filepath.Dir(fixture.checkout), "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	scratch, err := execution.PrepareScratchDirectory(state, testRunID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := execution.PrepareScratchDirectory(state, "run-another")
	if err != nil {
		t.Fatal(err)
	}
	records := filepath.Join(state, "products", "project", "runs")
	if err := os.MkdirAll(records, 0o700); err != nil {
		t.Fatal(err)
	}
	token := probeToken(t)
	allowed := filepath.Join(scratch, "probe-"+token)
	refused := []string{filepath.Join(other, "probe-"+token), filepath.Join(records, "probe-"+token), filepath.Join(state, "probe-"+token)}
	command := "printf ok > '" + allowed + "'"
	for _, path := range refused {
		command += "; printf ok > '" + path + "'"
	}

	result, err := provider.Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: fixture.worktree,
		RepositoryRoot:   fixture.checkout,
		ScratchDirectory: scratch,
		AllowedTools:     []string{"Bash"},
		Prompt: "Run this exact command with Bash, once, and nothing else: " + command + "\n" +
			"Part of it is expected to be refused; do not retry it or work around it. " +
			"Then reply with whatever it printed, or with exactly ok if it printed nothing.",
		Timeout: 2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	requireACompletedProbe(t, result)
	if _, statErr := os.Stat(allowed); statErr != nil {
		t.Fatalf("a developer run wrote nothing in its own scratch folder at %s, so the sandbox does not grant it: %v; the run reported: %q", allowed, statErr, result.FinalText)
	}
	for _, path := range refused {
		if _, statErr := os.Stat(path); statErr == nil {
			t.Fatalf("a developer run wrote %s, in the state directory beside its own scratch folder; the run reported: %q", path, result.FinalText)
		}
	}
}
