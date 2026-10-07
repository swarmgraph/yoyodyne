package home

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// clone makes a Git repository with an origin remote, which is what a binding
// is written from and what tells a second clone from a second product.
func clone(t *testing.T, remote string) string {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "checkout")
	run(t, "", "init", "-q", "-b", "main", repository)
	// A test that commits needs an author, and a build machine has none of its
	// own to lend: the forge's runner refused the commit without these.
	run(t, repository, "config", "user.name", "Yoyodyne Test")
	run(t, repository, "config", "user.email", "yoyodyne@example.invalid")
	if remote != "" {
		run(t, repository, "remote", "add", "origin", remote)
	}
	return repository
}

func run(t *testing.T, directory string, args ...string) {
	t.Helper()
	if directory != "" {
		args = append([]string{"-C", directory}, args...)
	}
	if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

// A new home keeps each project's records under projects/<id>/state and its
// worktrees under projects/<id>/worktrees; a home still holding the earlier
// builds' products/ directory is read the earlier way, so a build deployed over
// a running harness finds its state where it left it.
func TestTheLayoutFollowsWhatTheHomeHolds(t *testing.T) {
	t.Parallel()

	fresh := t.TempDir()
	if got, want := ProductDirectory(fresh, "example"), filepath.Join(fresh, "projects", "example", "state"); got != want {
		t.Errorf("ProductDirectory() in a new home = %q, want %q", got, want)
	}
	if got, want := WorktreeDirectory(fresh, "example", "repo"), filepath.Join(fresh, "projects", "example", "worktrees"); got != want {
		t.Errorf("WorktreeDirectory() in a new home = %q, want %q", got, want)
	}
	if got := ProductDirectoryWithin(fresh, "example"); got != "projects/example/state" {
		t.Errorf("ProductDirectoryWithin() = %q", got)
	}

	earlier := t.TempDir()
	if err := os.MkdirAll(filepath.Join(earlier, "products", "example"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !EarlierLayout(earlier) || EarlierLayout(fresh) {
		t.Fatalf("EarlierLayout() = %v for the earlier home and %v for the new one", EarlierLayout(earlier), EarlierLayout(fresh))
	}
	if got, want := ProductDirectory(earlier, "example"), filepath.Join(earlier, "products", "example"); got != want {
		t.Errorf("ProductDirectory() in the earlier home = %q, want %q", got, want)
	}
	if got, want := WorktreeDirectory(earlier, "example", "repo"), filepath.Join(earlier, "worktrees", "example", "repo"); got != want {
		t.Errorf("WorktreeDirectory() in the earlier home = %q, want %q", got, want)
	}
}

// The first start against an id with no project directory creates it and
// writes the binding from the repository's Git common directory; every
// worktree of that clone agrees with it afterwards.
func TestTheFirstStartBindsAndEveryWorktreeOfTheCloneAgrees(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repository := clone(t, "git@github.com:example/thing.git")
	agreement, err := Agree(AgreeOptions{Root: root, ProductID: "thing", Checkout: repository})
	if err != nil || !agreement.Bound {
		t.Fatalf("first Agree() = %+v, %v; want the binding written", agreement, err)
	}
	binding, found, err := ReadBinding(root, "thing")
	if err != nil || !found {
		t.Fatalf("ReadBinding() = %v, %v", found, err)
	}
	common, _ := CommonGitDirectory(repository)
	if binding.GitCommonDirectory != common || binding.RemoteURL != "git@github.com:example/thing.git" || binding.BoundBy == "" || binding.BoundAt.IsZero() {
		t.Fatalf("binding = %+v, want the common directory %s, the remote, and who bound it when", binding, common)
	}
	if !strings.Contains(agreement.Says("thing"), "bound project thing") {
		t.Errorf("Says() = %q", agreement.Says("thing"))
	}

	run(t, repository, "commit", "-q", "--allow-empty", "-m", "first")
	worktree := filepath.Join(t.TempDir(), "worktree")
	run(t, repository, "worktree", "add", "-q", worktree)
	again, err := Agree(AgreeOptions{Root: root, ProductID: "thing", Checkout: worktree})
	if err != nil || again.Bound {
		t.Fatalf("Agree() from a worktree = %+v, %v; want it to agree without binding again", again, err)
	}
}

// The three refusals, each naming its one command.
func TestAStartAgainstAnotherRepositoryIsRefusedNamingItsRemedy(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		second  string
		problem BindingProblem
		remedy  string
	}{
		{name: "second clone", second: "https://github.com/example/thing", problem: SecondClone, remedy: BindCommand + " --replace"},
		{name: "shared id", second: "git@github.com:somebody/else.git", problem: SharedID, remedy: RenameCommand + " thing <new-id>"},
	} {
		root := t.TempDir()
		first := clone(t, "git@github.com:example/thing.git")
		if _, err := Agree(AgreeOptions{Root: root, ProductID: "thing", Checkout: first}); err != nil {
			t.Fatal(err)
		}
		second := clone(t, test.second)
		_, err := Agree(AgreeOptions{Root: root, ProductID: "thing", Checkout: second})
		var refusal *BindingError
		if !errors.As(err, &refusal) || refusal.Problem != test.problem {
			t.Fatalf("%s: Agree() = %v, want a %s refusal", test.name, err, test.problem)
		}
		for _, want := range []string{first, second, test.remedy} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: refusal %q does not name %q", test.name, err, want)
			}
		}
	}

	root := t.TempDir()
	first := clone(t, "")
	if _, err := Agree(AgreeOptions{Root: root, ProductID: "thing", Checkout: first}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(first); err != nil {
		t.Fatal(err)
	}
	second := clone(t, "")
	_, err := Agree(AgreeOptions{Root: root, ProductID: "thing", Checkout: second})
	var refusal *BindingError
	if !errors.As(err, &refusal) || refusal.Problem != MissingRepository {
		t.Fatalf("missing: Agree() = %v, want a missing-repository refusal", err)
	}
	for _, want := range []string{first, "no longer there", BindCommand} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing: refusal %q does not name %q", err, want)
		}
	}
	// The harness never re-binds on that alone.
	if binding, _, _ := ReadBinding(root, "thing"); binding.Repository != first {
		t.Fatalf("a refused start rebound the project to %s", binding.Repository)
	}
}

// The earlier home's state predates bindings, so a start there writes none;
// the migration writes each. A binding already there is held to all the same.
func TestTheEarlierLayoutWritesNoBindingOfItsOwn(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "products", "thing"), 0o700); err != nil {
		t.Fatal(err)
	}
	repository := clone(t, "")
	if agreement, err := Agree(AgreeOptions{Root: root, ProductID: "thing", Checkout: repository}); err != nil || agreement.Bound {
		t.Fatalf("Agree() in the earlier layout = %+v, %v; want nothing written", agreement, err)
	}
	if _, err := os.Stat(filepath.Join(root, "projects")); !os.IsNotExist(err) {
		t.Fatalf("a start in the earlier layout made %s (%v)", filepath.Join(root, "projects"), err)
	}
	if _, err := Bind(AgreeOptions{Root: root, ProductID: "thing", Checkout: repository}); err != nil {
		t.Fatal(err)
	}
	other := clone(t, "")
	if _, err := Agree(AgreeOptions{Root: root, ProductID: "thing", Checkout: other}); err == nil {
		t.Fatal("a binding in the earlier layout was not held to")
	}
	if !EarlierLayout(root) {
		t.Fatal("a binding changed the layout the home is read in")
	}
}

// A reading surface holds a start to a binding and writes none.
func TestAReadOnlyAgreementWritesNothing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if _, err := Agree(AgreeOptions{Root: root, ProductID: "thing", Checkout: clone(t, ""), ReadOnly: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "projects")); !os.IsNotExist(err) {
		t.Fatalf("a read-only agreement wrote into the home (%v)", err)
	}
}

func TestRemotesAreComparedAsThePlaceTheyName(t *testing.T) {
	t.Parallel()

	for _, pair := range [][2]string{
		{"git@github.com:example/thing.git", "https://github.com/example/thing"},
		{"ssh://git@github.com/example/thing.git", "https://github.com/Example/thing/"},
	} {
		if !sameRemote(pair[0], pair[1]) {
			t.Errorf("sameRemote(%q, %q) = false", pair[0], pair[1])
		}
	}
	if sameRemote("", "") || sameRemote("git@github.com:a/b.git", "git@github.com:a/c.git") {
		t.Error("sameRemote matched remotes that name nothing, or two places")
	}
}

// Projects lists every project directory and, in the earlier layout, every
// product the earlier builds kept records for; BoundProject finds the one a
// checkout is bound to.
func TestProjectsAreListedAndFoundByTheirBinding(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "products", "older"), 0o700); err != nil {
		t.Fatal(err)
	}
	repository := clone(t, "")
	if _, err := Bind(AgreeOptions{Root: root, ProductID: "thing", Checkout: repository}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ProjectDirectory(root, "thing"), ConfigFileName), []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	projects, err := Projects(root)
	if err != nil || len(projects) != 2 {
		t.Fatalf("Projects() = %+v, %v", projects, err)
	}
	if projects[0].ID != "older" || !projects[0].Earlier || projects[1].ID != "thing" || !projects[1].Bound || projects[1].Configuration == "" {
		t.Fatalf("Projects() = %+v", projects)
	}
	found, ok, err := BoundProject(root, filepath.Join(repository))
	if err != nil || !ok || found.ID != "thing" {
		t.Fatalf("BoundProject() = %+v, %v, %v", found, ok, err)
	}
}
