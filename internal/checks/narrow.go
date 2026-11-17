package checks

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// ChangedGoPackagesVariable is the environment variable every check is given,
// naming the Go packages the change under test touches. A check that wants the
// per-run gate narrowed to them reads it — `go test -race
// $YOYODYNE_CHANGED_GO_PACKAGES` — and a check that does not is unaffected by
// it being there.
//
// It is an environment variable rather than a substitution into the command,
// because a check is a line of shell the operator wrote and the shell already
// has a way to say "what the harness told me": the line reads as itself in the
// configuration, the narrowing is visible in it, and a check that never
// mentions the variable runs exactly as it always has.
const ChangedGoPackagesVariable = "YOYODYNE_CHANGED_GO_PACKAGES"

// WholeModule is the value the variable takes where the gate cannot be
// narrowed: every package, in the form the Go command takes it.
const WholeModule = "./..."

// Narrowing is what the per-run gate is told about the Go packages a change
// touches: the packages themselves, or that the whole module has to stand in
// for them and why.
type Narrowing struct {
	// Packages are the packages the change touches, as the "./dir" patterns the
	// Go command takes, sorted and without repeats. Empty with Whole false is a
	// change that touches no Go package at all.
	Packages []string
	// Whole reports that the gate is given the whole module rather than a
	// narrowing, with Reason saying why: the root is no Go module, so there is
	// nothing to narrow within, or the change touched what every package
	// depends on.
	Whole  bool
	Reason string
}

// Value is what the variable is set to: the packages separated by spaces, the
// whole module where the narrowing could not be made, and nothing where the
// change touches no package.
func (n Narrowing) Value() string {
	if n.Whole {
		return WholeModule
	}
	return strings.Join(n.Packages, " ")
}

// Env is the variable and its value, in the form a process environment takes.
func (n Narrowing) Env() string {
	return ChangedGoPackagesVariable + "=" + n.Value()
}

// NoGoPackages is how Describe opens for a change that touches no Go package,
// so a record of what checks were told can be read for it
// (TouchesNoGoPackage).
const NoGoPackages = "no Go package"

// TouchesNoGoPackage reports a recorded description (Describe) of a change that
// touches no Go package, which a check narrowed to Go packages covers nothing of.
func TouchesNoGoPackage(described string) bool {
	return strings.HasPrefix(described, NoGoPackages)
}

// Describe says what the gate was narrowed to, for the record.
func (n Narrowing) Describe() string {
	switch {
	case n.Whole:
		return "the whole module (" + n.Reason + ")"
	case len(n.Packages) == 0:
		return NoGoPackages + " (the change touches none)"
	default:
		return strings.Join(n.Packages, " ")
	}
}

// NarrowGoPackages works out which Go packages a change touches, from the
// repository-relative paths it changed and the tree at root.
//
// A changed file belongs to the nearest directory above it that holds Go
// source, which is how the Go command itself files a package's test data and
// embedded files under the package: a file under `pkg/testdata/` is `pkg`'s. A
// file above every package — a document, the Makefile, a workflow — belongs to
// none, and is left to the checks that run whole; what this narrows is the suite
// the operator chose to narrow, and the rest of the gate still runs over
// everything. The module files are the exception, because a dependency change
// reaches every package: a change to `go.mod` or `go.sum` is the whole module.
//
// It reads the tree rather than running the Go command, because it is asked on
// the way to building one stage's environment and a `go list` over the module
// would cost more than the narrowing saves on a small change. What it cannot
// see is a package that depends on a touched one — that is what the landing
// check over the whole module is for.
func NarrowGoPackages(root string, changed []string) Narrowing {
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return Narrowing{Whole: true, Reason: "the repository root is not a Go module"}
	}
	packages := map[string]struct{}{}
	for _, changedPath := range changed {
		relative := path.Clean(filepath.ToSlash(strings.TrimSpace(changedPath)))
		if relative == "" || relative == "." || strings.HasPrefix(relative, "../") {
			continue
		}
		switch relative {
		case "go.mod", "go.sum", "go.work", "go.work.sum":
			return Narrowing{Whole: true, Reason: relative + " changed, which every package depends on"}
		}
		if strings.HasPrefix(relative, "vendor/") {
			return Narrowing{Whole: true, Reason: "vendored dependencies changed, which every package may depend on"}
		}
		if directory, found := packageDirectory(root, path.Dir(relative)); found {
			packages[directory] = struct{}{}
		}
	}
	narrowed := make([]string, 0, len(packages))
	for directory := range packages {
		narrowed = append(narrowed, directory)
	}
	sort.Strings(narrowed)
	return Narrowing{Packages: narrowed}
}

// packageDirectory is the nearest directory at or above the one given that the
// Go command would read as a package, as a "./dir" pattern, and whether there
// is one before the root is passed. A directory the Go command ignores — one
// named testdata, or one whose name begins with "." or "_" — is stepped over
// rather than reported, because a pattern naming it matches nothing.
func packageDirectory(root, directory string) (string, bool) {
	for directory != "." && directory != "/" && directory != "" {
		if !ignoredByGo(path.Base(directory)) && holdsGoSource(filepath.Join(root, filepath.FromSlash(directory))) {
			return "./" + directory, true
		}
		directory = path.Dir(directory)
	}
	if holdsGoSource(root) {
		return ".", true
	}
	return "", false
}

func ignoredByGo(name string) bool {
	return name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// holdsGoSource reports a directory the Go command would compile something in.
// A directory that cannot be read — one the change deleted, say — holds
// nothing, and the package it was is nothing to test now.
func holdsGoSource(directory string) bool {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") {
			return true
		}
	}
	return false
}
