package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// Inspection and recovery happen inside a preserved worktree, and an agent runs
// `yoyo` from the worktree it was given. Both read the configuration the
// worktree carries, which resolves the repository to the worktree itself — a
// directory under the configured worktree root, which is what the containment
// check refuses. Every verb built on buildComponents failed there until the
// repository was resolved to the checkout the worktree was added from.
func TestVerbsRunFromInsideAManagedWorktree(t *testing.T) {
	// Not parallel: the state root the worktree root is derived from is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	project := t.TempDir()
	managedWorktreeProject(t, project)
	worktree := addManagedWorktree(t, project, stateRoot, "yoyodyne-task-abcd1234")
	configPath := filepath.Join(worktree, config.DirectoryName, config.FileName)

	// buildComponents is what every affected verb is built on, so what it resolves
	// the repository to is the whole of the fix: the checkout, not the worktree.
	parts, err := buildComponents(configPath)
	if err != nil {
		t.Fatalf("buildComponents() from inside a managed worktree error = %v", err)
	}
	if canonical(t, parts.repository) != canonical(t, project) {
		t.Fatalf("repository = %q, want the checkout %q the worktree was added from", parts.repository, project)
	}
	if parts.config.Product.Repository != parts.repository {
		t.Fatalf("Product.Repository = %q, want the resolved repository %q", parts.config.Product.Repository, parts.repository)
	}

	// The verbs themselves, run as they would be from inside the worktree. `run`,
	// `review`, and `chat` are the same wiring and are left to buildComponents
	// above, because each of them reaches a provider before it does anything else.
	for _, verb := range [][]string{
		{"cost"},
		{"directive", "list"},
		{"pause"},
		{"resume"},
		{"reconcile"},
	} {
		name := strings.Join(verb, " ")
		stdout, stderr, code := runCLI(t, append(verb, "--config", configPath)...)
		if code != 0 {
			t.Fatalf("%s code = %d, stdout = %q, stderr = %q", name, code, stdout, stderr)
		}
		if strings.Contains(stderr, "must not contain one another") {
			t.Fatalf("%s stderr = %q, want no containment refusal from inside a managed worktree", name, stderr)
		}
	}
}

// A repository that is under the worktree root and is not a worktree of a
// checkout outside it is a configuration nobody can work in, and the refusal is
// read by somebody standing in it — so it names what to do instead.
func TestARepositoryInsideTheWorktreeRootIsRefusedWithSomethingToDo(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	// A checkout of its own, sitting where the harness keeps its worktrees.
	project := filepath.Join(home.WorktreeDirectory(stateRoot, "yoyodyne", "yoyodyne"), "checkout")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	configPath := managedWorktreeProject(t, project)

	_, stderr, code := runCLI(t, "cost", "--config", configPath)
	if code == 0 {
		t.Fatal("cost accepted a repository inside the worktree root")
	}
	for _, want := range []string{"inside the worktree root", "run yoyo from the checkout", "execution.worktree_root"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("cost stderr = %q, want it to say %q", stderr, want)
		}
	}
}

// managedWorktreeProject is a checkout carrying the project's configuration, as
// a project whose `.yoyodyne` is checked in does — which is what makes a
// worktree of it carry one too.
func managedWorktreeProject(t *testing.T, project string) string {
	t.Helper()

	git(t, project, "init", "-b", "main")
	git(t, project, "config", "user.name", "Yoyodyne Test")
	git(t, project, "config", "user.email", "yoyodyne@example.invalid")
	commit(t, project, "first")
	directory := filepath.Join(project, config.DirectoryName)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, config.FileName), []byte(validConfig), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	git(t, project, "add", config.DirectoryName)
	git(t, project, "commit", "-m", "configuration")
	return filepath.Join(directory, config.FileName)
}

// addManagedWorktree adds a worktree exactly where an `auto` worktree root puts
// one: under the state root, by product and repository id.
func addManagedWorktree(t *testing.T, project, stateRoot, name string) string {
	t.Helper()

	worktree := filepath.Join(home.WorktreeDirectory(stateRoot, "yoyodyne", "yoyodyne"), name)
	git(t, project, "worktree", "add", "-b", "yoyodyne/task/abcd1234", worktree)
	return worktree
}

func canonical(t *testing.T, path string) string {
	t.Helper()

	link, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) error = %v", path, err)
	}
	return link
}
