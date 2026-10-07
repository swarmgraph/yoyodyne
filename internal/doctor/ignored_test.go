package doctor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// A configuration that is there and does not load is somebody's edit. `yoyo
// init` refuses to write over it and `--force` would delete the edit, so the
// remedy is the editor on that file -- and following it, with an editor that
// puts the file right, leaves a configuration doctor calls valid.
func TestAConfigurationThatDoesNotLoadIsOpenedInTheEditorRatherThanRegenerated(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = strings.Replace(healthyConfig, "checks:\n", "checks: [\n", 1)
	report := world.diagnose()

	finding, found := findingFor(report, "configuration")
	if !found || finding.Status != StatusProblem {
		t.Fatalf("configuration = %#v, want a problem: %s", finding, render(report))
	}
	path := filepath.Join(world.project, config.DirectoryName, config.FileName)
	if !strings.Contains(finding.Remedy, "${EDITOR:-vi}") || !strings.Contains(finding.Remedy, path) {
		t.Fatalf("remedy = %q, want the editor on %s", finding.Remedy, path)
	}
	if strings.Contains(finding.Remedy, "yoyo init") {
		t.Fatalf("remedy = %q, want nothing that refuses, or deletes, the file that is there", finding.Remedy)
	}
	if !strings.Contains(finding.Detail, path) {
		t.Errorf("detail = %q, want the file named beside why it does not load", finding.Detail)
	}

	// Followed: the "editor" here writes the healthy configuration over the
	// broken one, which is what the operator does by hand.
	repaired := filepath.Join(t.TempDir(), "repaired.yaml")
	if err := os.WriteFile(repaired, []byte(healthyConfig), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runRemedy(t, finding.Remedy, "EDITOR=cp -f "+repaired)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	world.configuration = string(content)
	after, _ := findingFor(world.diagnose(), "configuration")
	if after.Status != StatusOK {
		t.Fatalf("configuration after the remedy = %#v, want it valid", after)
	}
}

// YOYODYNE_CONFIG naming nothing is neither a missing configuration nor a
// broken one: `yoyo init` would write a configuration the variable still hides,
// so the remedy is to stop naming the wrong one.
func TestAVariableNamingNoConfigurationIsUnset(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.variables[config.EnvironmentVariable] = "/nowhere/config.yaml"
	world.configError = config.NotFoundError{StartDirectory: world.project}
	report := world.diagnose()

	finding, _ := findingFor(report, "configuration")
	if finding.Status != StatusProblem || finding.Remedy != "unset "+config.EnvironmentVariable {
		t.Fatalf("configuration = %#v, want the variable unset", finding)
	}
}

// The three answers about the ignore rules: none, the repository's own
// .gitignore, and a rule local to this checkout. Only the second is a warning,
// because only it costs the clones and worktrees a configuration nobody chose
// to keep from them, and it is the only one with a command that undoes it.
func TestAnIgnoredConfigurationIsAWarningOnlyWhenTheRepositoryIgnoresIt(t *testing.T) {
	t.Parallel()

	t.Run("not ignored", func(t *testing.T) {
		t.Parallel()
		world := newWorld(t)
		world.runner.reply("check-ignore", failed(""))
		finding, found := findingFor(world.diagnose(), "configuration-ignored")
		if !found || finding.Status != StatusOK {
			t.Fatalf("configuration-ignored = %#v, found = %t, want ok", finding, found)
		}
	})

	t.Run("ignored by the repository's .gitignore", func(t *testing.T) {
		t.Parallel()
		world := newWorld(t)
		world.runner.reply("check-ignore", succeeded(".gitignore:1:.yoyodyne\t.yoyodyne/config.yaml\n"))
		report := world.diagnose()
		finding, _ := findingFor(report, "configuration-ignored")
		if finding.Status != StatusWarning {
			t.Fatalf("configuration-ignored = %#v, want a warning", finding)
		}
		if !strings.Contains(finding.Remedy, "add --force -- .yoyodyne") || !strings.Contains(finding.Remedy, "commit") {
			t.Errorf("remedy = %q, want the configuration directory added and committed", finding.Remedy)
		}
		if !report.Healthy() {
			t.Errorf("Diagnose() = %s, want work still able to run in this checkout", report.Status)
		}
	})

	t.Run("ignored by a rule local to this checkout", func(t *testing.T) {
		t.Parallel()
		world := newWorld(t)
		world.runner.reply("check-ignore", succeeded(".git/info/exclude:1:.yoyodyne/\t.yoyodyne/config.yaml\n"))
		finding, _ := findingFor(world.diagnose(), "configuration-ignored")
		if finding.Status != StatusOK {
			t.Fatalf("configuration-ignored = %#v, want the supported choice said rather than warned about", finding)
		}
		if !strings.Contains(finding.Summary, "only this checkout") || !strings.Contains(finding.Detail, "yoyo init --external") {
			t.Errorf("configuration-ignored = %#v, want what it costs and the way out said", finding)
		}
	})
}

// The commit remedy is run against a real repository whose .gitignore names the
// configuration, and afterwards Git no longer ignores it.
func TestTheCommitRemedyLeavesTheConfigurationNoLongerIgnored(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	repository := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(repository, config.DirectoryName, "personas"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	runRemedy(t, "git -C "+shellQuote(repository)+" init -q -b main")
	for name, content := range map[string]string{
		".gitignore": ".yoyodyne\n",
		filepath.Join(config.DirectoryName, config.FileName):            "product: {}\n",
		filepath.Join(config.DirectoryName, "personas", "developer.md"): "# Developer\n",
	} {
		if err := os.WriteFile(filepath.Join(repository, name), []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	configPath := filepath.Join(repository, config.DirectoryName, config.FileName)

	diagnosis := &diagnosis{env: Environment{Runner: execution.OSProcessRunner{}}}
	before := diagnosis.checkConfigurationIgnored(context.Background(), repository, configPath)
	if before.Status != StatusWarning {
		t.Fatalf("configuration-ignored = %#v, want a warning before the remedy", before)
	}
	runRemedy(t, before.Remedy,
		"GIT_AUTHOR_NAME=Doctor Test", "GIT_AUTHOR_EMAIL=doctor@example.com",
		"GIT_COMMITTER_NAME=Doctor Test", "GIT_COMMITTER_EMAIL=doctor@example.com")
	after := diagnosis.checkConfigurationIgnored(context.Background(), repository, configPath)
	if after.Status != StatusOK {
		t.Fatalf("configuration-ignored = %#v, want ok after the remedy", after)
	}
	listed, err := exec.Command("git", "-C", repository, "ls-files", config.DirectoryName).Output()
	if err != nil {
		t.Fatalf("git ls-files error = %v", err)
	}
	if !strings.Contains(string(listed), "personas/developer.md") {
		t.Errorf("committed = %q, want the personas committed beside the configuration", listed)
	}
}

// runRemedy runs a remedy the way an operator pasting it would: through a
// shell, with whatever the test adds to the environment.
func runRemedy(t *testing.T, remedy string, environment ...string) {
	t.Helper()
	command := exec.Command("sh", "-c", remedy)
	command.Env = append(os.Environ(), environment...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("remedy %q error = %v: %s", remedy, err, output)
	}
}
