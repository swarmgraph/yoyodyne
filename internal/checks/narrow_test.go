package checks

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The narrowing files each changed path under the nearest package above it, the
// way the Go command files a package's own test data, and leaves alone what is
// above every package: a document changes no package's race behaviour, and the
// checks that run whole still cover it.
func TestAChangeIsNarrowedToThePackagesItTouches(t *testing.T) {
	t.Parallel()

	root := goModule(t,
		"internal/checks/runner.go",
		"internal/checks/testdata/fixture.txt",
		"internal/orchestrator/pipeline.go",
		"cmd/yoyo/main.go",
	)
	narrowing := NarrowGoPackages(root, []string{
		"internal/checks/testdata/fixture.txt",
		"internal/orchestrator/pipeline_test.go",
		"internal/orchestrator/pipeline.go",
		"docs/configuration.md",
		"Makefile",
	})
	if narrowing.Whole {
		t.Fatalf("narrowing = %#v, want the packages rather than the whole module", narrowing)
	}
	if want := []string{"./internal/checks", "./internal/orchestrator"}; !reflect.DeepEqual(narrowing.Packages, want) {
		t.Fatalf("packages = %v, want %v", narrowing.Packages, want)
	}
	if got := narrowing.Env(); got != "YOYODYNE_CHANGED_GO_PACKAGES=./internal/checks ./internal/orchestrator" {
		t.Fatalf("Env() = %q", got)
	}
}

// A change that reaches every package is not narrowed, and the reason is
// recorded rather than inferred: a dependency change is the whole module.
func TestAModuleFileChangeIsTheWholeModule(t *testing.T) {
	t.Parallel()

	root := goModule(t, "internal/checks/runner.go")
	for _, changed := range []string{"go.mod", "go.sum", "vendor/example.com/dep/dep.go"} {
		narrowing := NarrowGoPackages(root, []string{"internal/checks/runner.go", changed})
		if !narrowing.Whole || narrowing.Value() != WholeModule || narrowing.Reason == "" {
			t.Fatalf("%s: narrowing = %#v, want the whole module with a reason", changed, narrowing)
		}
	}
}

// A repository that is not a Go module has nothing to narrow within, and says
// so rather than reporting that the change touches no package.
func TestARepositoryThatIsNoGoModuleIsTheWholeModule(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	narrowing := NarrowGoPackages(root, []string{"src/index.ts"})
	if !narrowing.Whole || narrowing.Reason == "" {
		t.Fatalf("narrowing = %#v, want the whole module with a reason", narrowing)
	}
}

// A change above every package is a change to no package: the variable is
// empty, and what it describes says why.
func TestAChangeTouchingNoPackageIsEmpty(t *testing.T) {
	t.Parallel()

	root := goModule(t, "internal/checks/runner.go")
	narrowing := NarrowGoPackages(root, []string{"docs/operations.md", "README.md", "internal/README.md"})
	if narrowing.Whole || len(narrowing.Packages) != 0 || narrowing.Value() != "" {
		t.Fatalf("narrowing = %#v, want no package", narrowing)
	}
	if got := narrowing.Describe(); got != "no Go package (the change touches none)" {
		t.Fatalf("Describe() = %q", got)
	}
}

// A directory the Go command ignores is stepped over rather than named: a
// pattern naming testdata matches nothing, and the package that owns it is what
// the change is to.
func TestADirectoryTheGoCommandIgnoresIsFiledUnderItsPackage(t *testing.T) {
	t.Parallel()

	root := goModule(t, "internal/doclink/doclink.go", "internal/doclink/testdata/broken.go", "internal/doclink/_fixtures/x.go")
	narrowing := NarrowGoPackages(root, []string{"internal/doclink/testdata/broken.go", "internal/doclink/_fixtures/x.go"})
	if want := []string{"./internal/doclink"}; !reflect.DeepEqual(narrowing.Packages, want) {
		t.Fatalf("packages = %v, want %v", narrowing.Packages, want)
	}
}

// A change to a package other packages import runs their tests too: the ones
// that import it directly, the ones that reach it through another package, and
// the ones whose tests alone import it. A package that only a test imports does
// not carry the change further, and a package that reaches none of it stays
// out. The record says which packages are there because they import a changed
// one.
func TestAChangeToAPackageWithImportersRunsTheirTestsToo(t *testing.T) {
	t.Parallel()

	root := goModuleWith(t, map[string]string{
		"internal/beads/client.go":        "package beads\n",
		"internal/beads/client_test.go":   "package beads\n\nimport \"testing\"\n",
		"internal/status/status.go":       "package status\n\nimport \"example.com/project/internal/beads\"\n",
		"internal/watch/watch.go":         "package watch\n\nimport (\n\t\"fmt\"\n\tst \"example.com/project/internal/status\"\n)\n",
		"cmd/yoyo/main.go":                "package main\n\nimport _ \"example.com/project/internal/watch\"\n",
		"internal/probe/probe.go":         "package probe\n",
		"internal/probe/probe_test.go":    "package probe_test\n\nimport \"example.com/project/internal/beads\"\n",
		"internal/usesprobe/usesprobe.go": "package usesprobe\n\nimport \"example.com/project/internal/probe\"\n",
		"internal/unrelated/unrelated.go": "package unrelated\n\nimport \"example.com/project/internal/probe\"\n",
		"internal/beads/testdata/x.go":    "package x\n\nimport \"example.com/project/internal/beads\"\n",
		"nested/go.mod":                   "module example.com/project/nested\n",
		"nested/n.go":                     "package nested\n\nimport \"example.com/project/internal/beads\"\n",
		"internal/broken/broken.go":       "package broken\n\nimport (\n",
	})
	narrowing := NarrowGoPackages(root, []string{"internal/beads/client.go"}).WithImporters(root)
	if narrowing.Whole {
		t.Fatalf("narrowing = %#v, want packages rather than the whole module", narrowing)
	}
	wantPackages := []string{"./cmd/yoyo", "./internal/beads", "./internal/probe", "./internal/status", "./internal/watch"}
	if !reflect.DeepEqual(narrowing.Packages, wantPackages) {
		t.Fatalf("packages = %v, want %v", narrowing.Packages, wantPackages)
	}
	wantImporters := []string{"./cmd/yoyo", "./internal/probe", "./internal/status", "./internal/watch"}
	if !reflect.DeepEqual(narrowing.Importers, wantImporters) {
		t.Fatalf("importers = %v, want %v", narrowing.Importers, wantImporters)
	}
	if got, want := narrowing.Value(), "./cmd/yoyo ./internal/beads ./internal/probe ./internal/status ./internal/watch"; got != want {
		t.Fatalf("Value() = %q, want %q", got, want)
	}
	if got, want := narrowing.Describe(), "./internal/beads, which the change touches, and the 4 package(s) that import them directly or through other packages: ./cmd/yoyo ./internal/probe ./internal/status ./internal/watch"; got != want {
		t.Fatalf("Describe() = %q, want %q", got, want)
	}
}

// A change to a package nothing imports runs that package alone, and the
// record reads as it always did.
func TestAChangeToAPackageWithoutImportersRunsItAlone(t *testing.T) {
	t.Parallel()

	root := goModuleWith(t, map[string]string{
		"internal/beads/client.go":  "package beads\n",
		"internal/status/status.go": "package status\n\nimport \"example.com/project/internal/beads\"\n",
		"cmd/yoyo/main.go":          "package main\n\nimport _ \"example.com/project/internal/status\"\n",
	})
	narrowing := NarrowGoPackages(root, []string{"cmd/yoyo/main.go"}).WithImporters(root)
	if narrowing.Whole || len(narrowing.Importers) != 0 {
		t.Fatalf("narrowing = %#v, want the changed package and no importers", narrowing)
	}
	if want := []string{"./cmd/yoyo"}; !reflect.DeepEqual(narrowing.Packages, want) {
		t.Fatalf("packages = %v, want %v", narrowing.Packages, want)
	}
	if got := narrowing.Describe(); got != "./cmd/yoyo" {
		t.Fatalf("Describe() = %q, want %q", got, "./cmd/yoyo")
	}
}

// Asking for importers changes nothing where there is nothing to widen: the
// whole module stays whole, and a change touching no package touches none.
func TestImportersAreNotSoughtForTheWholeModuleOrNoPackage(t *testing.T) {
	t.Parallel()

	root := goModule(t, "internal/checks/runner.go")
	for _, narrowing := range []Narrowing{
		{Whole: true, Reason: "go.mod changed, which every package depends on"},
		{},
	} {
		if got := narrowing.WithImporters(root); !reflect.DeepEqual(got, narrowing) {
			t.Fatalf("WithImporters(%#v) = %#v, want it unchanged", narrowing, got)
		}
	}
}

// Where the module's imports cannot be read, the whole module stands in for the
// importers and says why, rather than running the changed package alone.
func TestImportersThatCannotBeListedAreTheWholeModule(t *testing.T) {
	t.Parallel()

	root := goModuleWith(t, map[string]string{"internal/beads/client.go": "package beads\n"})
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("go 1.25\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	narrowing := Narrowing{Packages: []string{"./internal/beads"}}.WithImporters(root)
	if !narrowing.Whole || !strings.Contains(narrowing.Reason, "could not be listed") {
		t.Fatalf("narrowing = %#v, want the whole module with a reason", narrowing)
	}
}

// goModuleWith lays out a module with the given files and contents.
func goModuleWith(t *testing.T, files map[string]string) string {
	t.Helper()
	root := goModule(t)
	for relative, content := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	return root
}

// goModule lays out a module with Go source at the given paths, so the
// narrowing reads a tree rather than a fixture that describes one.
func goModule(t *testing.T, files ...string) string {
	t.Helper()
	root := t.TempDir()
	write := func(relative, content string) {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	write("go.mod", "module example.com/project\n")
	for _, file := range files {
		write(file, "package x\n")
	}
	return root
}
