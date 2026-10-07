package home

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// clone makes a Git repository with an origin remote, which is what a binding
// is written from and what tells a second clone from a second product.
func clone(t *testing.T, remote string) string {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "checkout")
	git(t, "", "init", "-q", "-b", "main", repository)
	// A test that commits needs an author, and a build machine has none of its
	// own to lend.
	git(t, repository, "config", "user.name", "Yoyodyne Test")
	git(t, repository, "config", "user.email", "yoyodyne@example.invalid")
	if remote != "" {
		git(t, repository, "remote", "add", "origin", remote)
	}
	return repository
}

func git(t *testing.T, directory string, args ...string) {
	t.Helper()
	if directory != "" {
		args = append([]string{"-C", directory}, args...)
	}
	if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

// The default home is ~/.yoyodyne on every platform, unless only the earlier
// platform default exists, which is kept until the migration moves it.
func TestTheDefaultHomeIsDotYoyodyneOnEveryPlatform(t *testing.T) {
	t.Parallel()

	noEnv := func(string) string { return "" }
	for _, goos := range []string{"darwin", "linux", "windows"} {
		user := t.TempDir()
		userHome := func() (string, error) { return user, nil }
		got, origin, err := Default(noEnv, userHome, goos)
		if err != nil || got != filepath.Join(user, ".yoyodyne") || origin != OriginDefault {
			t.Fatalf("%s, nothing on disk: Default() = %q, %q, %v; want ~/.yoyodyne", goos, got, origin, err)
		}

		earlier, err := EarlierDefault(noEnv, userHome, goos)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(earlier, 0o700); err != nil {
			t.Fatal(err)
		}
		if got, origin, _ := Default(noEnv, userHome, goos); got != earlier || origin != OriginEarlierDefault {
			t.Fatalf("%s, only the earlier home: Default() = %q, %q; want the earlier home %q kept", goos, got, origin, earlier)
		}

		// The machine file is kept in ~/.yoyodyne whichever home is in use, so
		// writing one does not move the state to an empty home.
		if err := os.MkdirAll(filepath.Join(user, ".yoyodyne"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(user, ".yoyodyne", MachineFileName), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if got, origin, _ := Default(noEnv, userHome, goos); got != earlier || origin != OriginEarlierDefault {
			t.Fatalf("%s, the machine file alone in ~/.yoyodyne: Default() = %q, %q; want the earlier home kept", goos, got, origin)
		}

		if err := os.MkdirAll(filepath.Join(user, ".yoyodyne", ProjectsDirectoryName), 0o700); err != nil {
			t.Fatal(err)
		}
		if got, origin, _ := Default(noEnv, userHome, goos); got != filepath.Join(user, ".yoyodyne") || origin != OriginDefault {
			t.Fatalf("%s, both homes: Default() = %q, %q; want ~/.yoyodyne", goos, got, origin)
		}
	}
}

// A new home keeps each project's records under projects/<id>/state and its
// worktrees under projects/<id>/worktrees; a home still holding the earlier
// builds' products/ directory is read the earlier way.
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
	if got, want := BindingPath(fresh, "example"), filepath.Join(fresh, "projects", "example", "repository.json"); got != want {
		t.Errorf("BindingPath() = %q, want %q", got, want)
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

	root := filepath.Join(t.TempDir(), ".yoyodyne")
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
	if says := agreement.Says("thing"); !strings.Contains(says, "bound it to the repository at "+repository) {
		t.Errorf("Says() = %q", says)
	}

	git(t, repository, "commit", "-q", "--allow-empty", "-m", "first")
	worktree := filepath.Join(t.TempDir(), "worktree")
	git(t, repository, "worktree", "add", "-q", worktree)
	again, err := Agree(AgreeOptions{Root: root, ProductID: "thing", Checkout: worktree})
	if err != nil || again.Bound {
		t.Fatalf("Agree() from a worktree = %+v, %v; want it to agree without binding again", again, err)
	}
}

// refused binds the id to one clone, starts from another, and returns the
// refusal, which must name both repositories and the remedy.
func refused(t *testing.T, first, second string, problem BindingProblem, names ...string) {
	t.Helper()
	root := t.TempDir()
	if _, err := Agree(AgreeOptions{Root: root, ProductID: "thing", Checkout: first}); err != nil {
		t.Fatal(err)
	}
	before, _, _ := ReadBinding(root, "thing")
	if problem == MissingRepository {
		if err := os.RemoveAll(first); err != nil {
			t.Fatal(err)
		}
	}
	_, err := Agree(AgreeOptions{Root: root, ProductID: "thing", Checkout: second})
	var refusal *BindingError
	if !errors.As(err, &refusal) || refusal.Problem != problem {
		t.Fatalf("Agree() = %v, want a %s refusal", err, problem)
	}
	for _, want := range append([]string{first, second}, names...) {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}
	// A refused start never re-binds.
	if after, _, _ := ReadBinding(root, "thing"); after != before {
		t.Fatalf("a refused start rewrote the binding to %+v", after)
	}
}

func TestASecondCloneOfABoundProjectRefusesToStart(t *testing.T) {
	t.Parallel()

	refused(t, clone(t, "git@github.com:example/thing.git"), clone(t, "https://github.com/example/thing"),
		SecondClone, "second clone", "this machine runs one clone", BindCommand+" --replace")
}

func TestTwoProductsSharingAnIDRefuseToStart(t *testing.T) {
	t.Parallel()

	refused(t, clone(t, "git@github.com:example/thing.git"), clone(t, "git@github.com:somebody/else.git"),
		SharedID, "two products share the id thing", RenameCommand+" thing <new>")
}

func TestABoundRepositoryThatIsMissingRefusesToStart(t *testing.T) {
	t.Parallel()

	refused(t, clone(t, ""), clone(t, ""), MissingRepository, "no longer there", BindCommand)
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

	common, _ := CommonGitDirectory(repository)
	if _, _, err := writeBinding(AgreeOptions{Root: root, ProductID: "thing", Checkout: repository}, common, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Agree(AgreeOptions{Root: root, ProductID: "thing", Checkout: clone(t, "")}); err == nil {
		t.Fatal("a binding in the earlier layout was not held to")
	}
}

// A directory that is not a Git checkout has nothing to bind by.
func TestAStartOutsideGitBindsNothing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if agreement, err := Agree(AgreeOptions{Root: root, ProductID: "thing", Checkout: t.TempDir()}); err != nil || agreement.Bound {
		t.Fatalf("Agree() outside Git = %+v, %v", agreement, err)
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

// A home's layout is decided by whether a products/ directory is at its top
// (EarlierLayout), so a writer anywhere that joined that name itself would switch
// a new home to the earlier layout under every store reading it. The name is
// joined here and nowhere else in the product's source, and the command suite's
// TestMain checks the new home it runs every command against never acquires one.
func TestOnlyThisPackageNamesTheEarlierProductsDirectory(t *testing.T) {
	t.Parallel()

	literal := regexp.MustCompile(`"products(/[^"]*)?"`)
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
		if filepath.ToSlash(relative) == "internal/home/home.go" {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if literal.Match(source) {
			t.Errorf("%s names the products directory; resolve a product's records through home.ProductDirectory", filepath.ToSlash(relative))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A project directory a new home is given holds the binding, the state, and the
// worktrees under projects/<id>/, and the home never gains products/.
func TestANewHomeKeepsEveryProductUnderProjects(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if _, err := Agree(AgreeOptions{Root: root, ProductID: "thing", Checkout: clone(t, "")}); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{ProductDirectory(root, "thing"), WorktreeDirectory(root, "thing", "repo")} {
		if !strings.HasPrefix(directory, filepath.Join(root, ProjectsDirectoryName, "thing")+string(filepath.Separator)) {
			t.Errorf("%s is not under the project directory", directory)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "products")); !os.IsNotExist(err) || EarlierLayout(root) {
		t.Fatalf("a new home acquired a products directory (%v)", err)
	}
}
