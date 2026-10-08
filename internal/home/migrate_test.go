package home

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, target, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustExist(t *testing.T, target string) {
	t.Helper()
	if _, err := os.Lstat(target); err != nil {
		t.Errorf("%s is not there after the migration: %v", target, err)
	}
}

func mustBeGone(t *testing.T, target string) {
	t.Helper()
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s is still there after the migration (%v)", target, err)
	}
}

// committedClone is a clone with one commit, which a worktree can be cut from.
func committedClone(t *testing.T, remote string) string {
	t.Helper()
	repository := clone(t, remote)
	git(t, repository, "commit", "-q", "--allow-empty", "-m", "start")
	return repository
}

// registeredWorktrees is every worktree path the repository has registered,
// with symlinks resolved so a temporary directory reached two ways compares as
// one.
func registeredWorktrees(t *testing.T, repository string) []string {
	t.Helper()
	output, err := exec.Command("git", "-C", repository, "worktree", "list", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, line := range strings.Split(string(output), "\n") {
		if worktree, ok := strings.CutPrefix(line, "worktree "); ok {
			paths = append(paths, resolvedOrSelf(worktree))
		}
	}
	return paths
}

// A home laid out the earlier way moves whole into the machine home: every
// product's records into its project's state, its worktrees under the project
// with the repository told where each now is, the external configuration and
// what is kept beside it into the project directory its id names, the
// machine-wide records to the top, and the machine file into the home. What is
// not a record is named as left behind, the emptied home names where the state
// went, the home every process resolves is the machine home, and running it
// again moves nothing more.
func TestAnEarlierHomeMovesIntoTheMachineHome(t *testing.T) {
	t.Parallel()

	user := t.TempDir()
	userHome := func() (string, error) { return user, nil }
	noEnv := func(string) string { return "" }
	from, err := EarlierDefault(noEnv, userHome, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(user, DirectoryName)
	configurations, err := EarlierConfigurationHome(noEnv, userHome)
	if err != nil {
		t.Fatal(err)
	}

	repository := committedClone(t, "git@github.com:example/thing.git")
	records := []string{"runs/run-1.json", "docket.jsonl", "reports.jsonl", "conversations/product-manager.json",
		"memory/product-manager/notes.json", "sweeps/sweeps.jsonl"}
	for _, record := range records {
		writeFile(t, filepath.Join(from, "products", "thing", filepath.FromSlash(record)), "{}\n")
	}
	worktree := filepath.Join(from, "worktrees", "thing", "thing", "thing-1-abc")
	git(t, repository, "worktree", "add", "-q", "-b", "run-1", worktree)
	writeFile(t, filepath.Join(worktree, "feature.txt"), "half done\n")
	writeFile(t, filepath.Join(from, OperatorHoldFileName), "{}\n")
	writeFile(t, filepath.Join(from, AccountsDirectoryName, "default", "settings.json"), "{}\n")
	writeFile(t, filepath.Join(from, LogsDirectoryName, "harness.log"), "line\n")
	writeFile(t, filepath.Join(from, "dashboard-restart.log"), "the operator's own\n")

	other := committedClone(t, "")
	writeFile(t, filepath.Join(configurations, "projects", "other-0123456789ab", ConfigFileName),
		"product:\n  id: other\n  repository: "+other+"\n")
	writeFile(t, filepath.Join(configurations, "projects", "other-0123456789ab", "personas", "developer.md"), "persona\n")
	writeFile(t, filepath.Join(configurations, MachineFileName), "# nothing set\n")

	options := MigrateOptions{From: from, To: to, ConfigurationHome: configurations,
		MachineFrom: filepath.Join(configurations, MachineFileName), MachineTo: filepath.Join(to, MachineFileName),
		Now: func() time.Time { return time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC) }, BoundBy: "the test"}
	migration, err := MoveHome(options)
	if err != nil {
		t.Fatalf("MoveHome() error = %v", err)
	}
	if migration.Failed() {
		t.Fatalf("MoveHome() left something it attempted: %+v", migration.LeftBehind)
	}

	for _, record := range records {
		mustExist(t, filepath.Join(ProjectDirectory(to, "thing"), StateDirectoryName, filepath.FromSlash(record)))
	}
	mustBeGone(t, filepath.Join(from, "products"))
	mustBeGone(t, filepath.Join(from, "worktrees"))
	if EarlierLayout(to) || EarlierLayout(from) {
		t.Fatal("a home is still laid out the earlier way after the migration")
	}
	moved := filepath.Join(ProjectDirectory(to, "thing"), WorktreesDirectoryName, "thing-1-abc")
	mustExist(t, filepath.Join(moved, "feature.txt"))
	if got, ok := MigratedWorktreePath(from, to, "thing", worktree); !ok || got != moved {
		t.Fatalf("MigratedWorktreePath() = %q, %v; want %q", got, ok, moved)
	}
	registered := registeredWorktrees(t, repository)
	found := false
	for _, path := range registered {
		found = found || path == resolvedOrSelf(moved)
	}
	if !found {
		t.Fatalf("the repository has the worktree registered at %v, want it at %s", registered, moved)
	}
	for _, name := range []string{OperatorHoldFileName, AccountsDirectoryName, LogsDirectoryName, MachineFileName} {
		mustExist(t, filepath.Join(to, name))
		mustBeGone(t, filepath.Join(from, name))
	}
	mustBeGone(t, filepath.Join(configurations, MachineFileName))
	mustExist(t, filepath.Join(ProjectDirectory(to, "other"), ConfigFileName))
	mustExist(t, filepath.Join(ProjectDirectory(to, "other"), "personas", "developer.md"))
	mustBeGone(t, configurations)

	for id, checkout := range map[string]string{"thing": repository, "other": other} {
		binding, bound, err := ReadBinding(to, id)
		if err != nil || !bound || !SameDirectory(binding.GitCommonDirectory, filepath.Join(checkout, ".git")) {
			t.Errorf("the binding of %s = %+v, %v, %v; want it bound to %s", id, binding, bound, err, checkout)
		}
	}
	if len(migration.Bound) != 2 {
		t.Errorf("bound = %v, want both bindings said", migration.Bound)
	}
	stray := false
	for _, left := range migration.LeftBehind {
		stray = stray || (left.What == "dashboard-restart.log" && !left.Failed)
	}
	if !stray {
		t.Errorf("left behind = %+v, want the operator's own file named and left", migration.LeftBehind)
	}
	if went, ok := MovedTo(from); !ok || went != to {
		t.Fatalf("MovedTo() = %q, %v; want the emptied home to name %s", went, ok, to)
	}
	if path, origin, err := Default(noEnv, userHome, "darwin"); err != nil || path != to || origin != OriginDefault {
		t.Fatalf("Default() = %q, %q, %v; want the machine home once the migration has run", path, origin, err)
	}
	if len(migration.Products) != 1 || migration.Products[0] != "thing" {
		t.Fatalf("products = %v, want the one product moved", migration.Products)
	}

	again, err := MoveHome(options)
	if err != nil || again.Failed() || len(again.Moved) != 0 {
		t.Fatalf("a second migration = %+v, %v; want nothing more moved and nothing failed", again, err)
	}
}

// A product whose records would land on records already in the machine home
// stops the migration before anything moves.
func TestAMigrationOntoRecordsAlreadyThereMovesNothing(t *testing.T) {
	t.Parallel()

	from, to := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(from, "products", "thing", "docket.jsonl"), "{}\n")
	writeFile(t, filepath.Join(to, ProjectsDirectoryName, "thing", StateDirectoryName, "docket.jsonl"), "{}\n")
	_, err := MoveHome(MigrateOptions{From: from, To: to})
	var conflict *ConflictError
	if !errors.As(err, &conflict) || !strings.Contains(err.Error(), "nothing was moved") {
		t.Fatalf("MoveHome() error = %v, want the conflict refused before anything moved", err)
	}
	mustExist(t, filepath.Join(from, "products", "thing", "docket.jsonl"))
}

// A home the machine file named is laid out the new way where it stands: no
// second home, no marker, and the machine-wide records stay at its top.
func TestAHomeNamedOnPurposeIsLaidOutWhereItStands(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "products", "thing", "docket.jsonl"), "{}\n")
	writeFile(t, filepath.Join(root, OperatorHoldFileName), "{}\n")
	migration, err := MoveHome(MigrateOptions{From: root, To: root})
	if err != nil || migration.Failed() {
		t.Fatalf("MoveHome() = %+v, %v", migration, err)
	}
	mustExist(t, filepath.Join(ProductDirectory(root, "thing"), "docket.jsonl"))
	mustExist(t, filepath.Join(root, OperatorHoldFileName))
	mustBeGone(t, filepath.Join(root, MovedMarkerName))
	if EarlierLayout(root) || ProductDirectory(root, "thing") != filepath.Join(root, ProjectsDirectoryName, "thing", StateDirectoryName) {
		t.Fatal("the home is still read the earlier way")
	}
}

// A home still laid out the earlier way is said to be, naming the command that
// moves it, wherever it was found except a home the variable named, which is
// how the migration is deferred on purpose; a home laid out the new way says
// nothing.
func TestAnEarlierHomeIsSaidToBeOneAndNamesTheMigration(t *testing.T) {
	t.Parallel()

	earlier := t.TempDir()
	writeFile(t, filepath.Join(earlier, "products", "thing", "docket.jsonl"), "{}\n")
	for _, origin := range []string{OriginEarlierDefault, OriginXDG, MachineOrigin("/m/machine.yaml")} {
		said, still := StillEarlier(Resolved{Path: earlier, Origin: origin})
		if !still || !strings.Contains(said, MigrateCommand) || !strings.Contains(said, earlier) || !strings.Contains(said, "moves nothing on its own") {
			t.Errorf("StillEarlier(%s) = %q, %v; want the home named and the migration with it", origin, said, still)
		}
	}
	if said, still := StillEarlier(Resolved{Path: earlier, Origin: OriginEnvironment}); still {
		t.Errorf("a home the variable named was said to want migrating: %q", said)
	}
	if said, still := StillEarlier(Resolved{Path: t.TempDir(), Origin: OriginDefault}); still {
		t.Errorf("a new home was said to want migrating: %q", said)
	}
}
