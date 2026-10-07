package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// stateRootProject is a Git checkout carrying its own configuration, which is
// what a process records its state root against.
func stateRootProject(t *testing.T) (project, configPath string) {
	t.Helper()
	project = t.TempDir()
	git(t, project, "init", "-b", "main")
	configPath = filepath.Join(project, config.DirectoryName, config.FileName)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(validConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	return project, configPath
}

// Two processes reading different roots refuse rather than diverge: the first
// command records the root it opened in the checkout's marker, and a later one
// that resolved another root — here because a shell exported a different
// YOYODYNE_STATE_HOME — refuses to start, naming both, and records nothing.
func TestAProcessOnASecondStateRootRefusesToStart(t *testing.T) {
	// Not parallel: the state root every command resolves is set for this process.
	t.Setenv("YOYODYNE_CONFIG_HOME", t.TempDir())
	first := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", first)
	project, configPath := stateRootProject(t)

	if _, stderr, code := runCLI(t, "reports", "--config", configPath); code != 0 {
		t.Fatalf("reports on the first root code = %d, stderr = %q", code, stderr)
	}
	marker := filepath.Join(project, ".git", "yoyodyne", "state-root")
	content, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("the first process recorded no marker: %v", err)
	}
	if strings.TrimSpace(string(content)) != first {
		t.Fatalf("marker = %q, want %q", content, first)
	}

	second := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", second)
	for _, args := range [][]string{
		{"reports", "--config", configPath},
		{"status", "--config", configPath},
		{"amendment", "list", "--config", configPath},
	} {
		_, stderr, code := runCLI(t, args...)
		if code == 0 {
			t.Fatalf("%v on a second root succeeded; want a refusal", args)
		}
		for _, want := range []string{first, second, marker, runstate.RootOriginEnvironment} {
			if !strings.Contains(stderr, want) {
				t.Errorf("%v refusal %q does not name %q", args, stderr, want)
			}
		}
	}
	if entries, err := os.ReadDir(second); err != nil || len(entries) != 0 {
		t.Fatalf("the refused processes wrote under the second root: %v, %v", entries, err)
	}

	t.Setenv("YOYODYNE_STATE_HOME", first)
	if _, stderr, code := runCLI(t, "reports", "--config", configPath); code != 0 {
		t.Fatalf("reports back on the recorded root code = %d, stderr = %q", code, stderr)
	}
}

// A command's first start against a product id creates the project directory
// and binds it to the checkout; a second repository using the same id under the
// same home refuses to start, naming both and the command that settles it, and
// writes nothing over the binding.
func TestASecondRepositoryWithABoundIDRefusesToStart(t *testing.T) {
	// Not parallel: the state root every command resolves is set for this process.
	t.Setenv("YOYODYNE_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", root)
	first, firstConfig := stateRootProject(t)
	if _, stderr, code := runCLI(t, "reports", "--config", firstConfig); code != 0 {
		t.Fatalf("reports from the first repository code = %d, stderr = %q", code, stderr)
	}
	binding := filepath.Join(root, "projects", "yoyodyne", "repository.json")
	before, err := os.ReadFile(binding)
	if err != nil {
		t.Fatalf("the first start wrote no binding: %v", err)
	}

	second, secondConfig := stateRootProject(t)
	_, stderr, code := runCLI(t, "reports", "--config", secondConfig)
	if code == 0 {
		t.Fatal("reports from a second repository with the same id succeeded; want a refusal")
	}
	for _, want := range []string{first, second, "two products share the id yoyodyne", "yoyo project rename"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("refusal %q does not name %q", stderr, want)
		}
	}
	if after, _ := os.ReadFile(binding); string(after) != string(before) {
		t.Fatalf("the refused start rewrote the binding to %s", after)
	}
}

// A marker left naming a root that has since been deleted — a temporary state
// home a test or a shell used and removed — refuses every command, and the
// refusal names the writer, the moment, and the one command that clears it;
// that command clears it and the next command starts.
func TestAMarkerNamingADeletedRootIsClearedByTheRebindItNames(t *testing.T) {
	// Not parallel: the state root every command resolves is set for this process.
	t.Setenv("YOYODYNE_CONFIG_HOME", t.TempDir())
	gone := filepath.Join(t.TempDir(), "deleted-root")
	t.Setenv("YOYODYNE_STATE_HOME", gone)
	project, configPath := stateRootProject(t)
	if _, stderr, code := runCLI(t, "reports", "--config", configPath); code != 0 {
		t.Fatalf("reports code = %d, stderr = %q", code, stderr)
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	current := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", current)
	_, stderr, code := runCLI(t, "reports", "--config", configPath)
	if code == 0 {
		t.Fatal("reports over a marker naming a deleted root succeeded; want the refusal")
	}
	for _, want := range []string{gone, "no longer exists", "yoyo state-root rebind", "process ", current} {
		if !strings.Contains(stderr, want) {
			t.Errorf("refusal %q does not say %q", stderr, want)
		}
	}

	stdout, stderr, code := runCLI(t, "state-root", "rebind", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, "rebound") || !strings.Contains(stdout, current) {
		t.Fatalf("state-root rebind code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	marker := filepath.Join(project, ".git", "yoyodyne", "state-root")
	if content, _ := os.ReadFile(marker); strings.TrimSpace(string(content)) != current {
		t.Fatalf("marker after the rebind = %q, want %q", content, current)
	}
	if _, stderr, code := runCLI(t, "reports", "--config", configPath); code != 0 {
		t.Fatalf("reports after the rebind code = %d, stderr = %q", code, stderr)
	}

	// A root still on disk is a second root, and rebind refuses to move off it.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	if _, stderr, code := runCLI(t, "state-root", "rebind", "--config", configPath); code == 0 || !strings.Contains(stderr, current) {
		t.Fatalf("state-root rebind over a root still there code = %d, stderr = %q; want a refusal naming it", code, stderr)
	}
}

// The machine key sets the root when no variable does, and config show says so
// by the file that set it.
func TestTheMachineKeySetsTheRootAndConfigShowNamesIt(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("YOYODYNE_CONFIG_HOME", configHome)
	t.Setenv("YOYODYNE_STATE_HOME", "")
	root := t.TempDir()
	machinePath := filepath.Join(configHome, runstate.MachineFileName)
	if err := os.WriteFile(machinePath, []byte("state_root: "+root+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	project, configPath := stateRootProject(t)

	stdout, stderr, code := runCLI(t, "config", "show", "--origins", "--config", configPath)
	if code != 0 {
		t.Fatalf("config show code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"# state root: " + root + " (from machine:" + machinePath + ")",
		"state_root: machine:" + machinePath,
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("config show does not say %q:\n%s", want, stdout)
		}
	}
	if _, err := os.Stat(filepath.Join(project, ".git", "yoyodyne", "state-root")); !os.IsNotExist(err) {
		t.Fatalf("config show recorded a marker (%v); reporting the root must record nothing", err)
	}

	stdout, stderr, code = runCLI(t, "config", "show", "--json", "--config", configPath)
	if code != 0 {
		t.Fatalf("config show --json code = %d, stderr = %q", code, stderr)
	}
	var payload struct {
		StateRoot map[string]string `json:"state_root"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.StateRoot["path"] != root || payload.StateRoot["origin"] != "machine:"+machinePath {
		t.Fatalf("config show --json state_root = %v", payload.StateRoot)
	}

	if _, stderr, code := runCLI(t, "reports", "--config", configPath); code != 0 {
		t.Fatalf("reports code = %d, stderr = %q", code, stderr)
	}
	if content, _ := os.ReadFile(filepath.Join(project, ".git", "yoyodyne", "state-root")); strings.TrimSpace(string(content)) != root {
		t.Fatalf("marker = %q, want the machine key's root %q", content, root)
	}
}

// resolvedTestStateRoot is the root this test process resolves, for a test that
// writes records a command then reads. It takes no marker, because the test is
// arranging the records rather than being a process that opens them.
func resolvedTestStateRoot() (string, error) {
	resolved, err := runstate.ResolveRoot(os.Getenv, os.UserHomeDir, runtime.GOOS)
	return resolved.Path, err
}

// Every process that opens a product's records resolves the root and agrees it
// with the checkout's marker, and the one place that does both is
// productStateRoot. ResolveRoot alone is the unguarded half, so it is allowed
// only there and in the two surfaces that report the root without opening
// anything under it. A new caller anywhere else is a process that could diverge
// onto a second root instead of refusing, so this reads the source and fails on
// one, and on anything reading the variables that choose the root directly.
func TestNothingOpensTheStateRootUnguarded(t *testing.T) {
	t.Parallel()

	allowed := map[string]bool{
		"internal/cli/run.go":       true, // productStateRoot, the guarded path
		"internal/cli/cli.go":       true, // config show: reports, records nothing
		"internal/doctor/doctor.go": true, // doctor: reports, reads the marker only
		"internal/cli/stateroot.go": true, // state-root rebind: replaces only a marker whose root is gone
	}
	variables := regexp.MustCompile(`Getenv\("(YOYODYNE_STATE_HOME|XDG_STATE_HOME)"\)|getenv\("(YOYODYNE_STATE_HOME|XDG_STATE_HOME)"\)`)
	repository := filepath.Join("..", "..")
	err := filepath.WalkDir(repository, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name := entry.Name(); name == ".git" || name == "node_modules" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(repository, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if strings.HasPrefix(relative, "internal/runstate/") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(source)
		if strings.Contains(text, "runstate.ResolveRoot(") && !allowed[relative] {
			t.Errorf("%s resolves the state root with runstate.ResolveRoot and never agrees it with the checkout's marker; open it through productStateRoot", relative)
		}
		if variables.MatchString(text) {
			t.Errorf("%s reads a variable that chooses the state root directly; resolve the root through runstate.ResolveRoot and the marker instead", relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
