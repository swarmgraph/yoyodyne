package checks

import (
	"bufio"
	"errors"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
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
	// change that touches no Go package at all. After WithImporters it also
	// holds the packages that import them, which Importers names.
	Packages []string
	// Importers are the packages in Packages that hold no changed file and are
	// there because they import a package that does, directly or through other
	// packages in the module (WithImporters). Sorted, and empty where nothing
	// imports a changed package or the importers were not asked for.
	Importers []string
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

// Describe says what the checks were narrowed to, for the record: the packages,
// and where some are there only because they import a changed one, which are
// which.
func (n Narrowing) Describe() string {
	switch {
	case n.Whole:
		return "the whole module (" + n.Reason + ")"
	case len(n.Packages) == 0:
		return NoGoPackages + " (the change touches none)"
	case len(n.Importers) == 0:
		return strings.Join(n.Packages, " ")
	default:
		importers := map[string]bool{}
		for _, importer := range n.Importers {
			importers[importer] = true
		}
		var touched []string
		for _, pkg := range n.Packages {
			if !importers[pkg] {
				touched = append(touched, pkg)
			}
		}
		return strings.Join(touched, " ") + ", which the change touches, and the " +
			strconv.Itoa(len(n.Importers)) + " package(s) that import them directly or through other packages: " +
			strings.Join(n.Importers, " ")
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
// would cost more than the narrowing saves on a small change. It names only the
// packages holding a changed file; WithImporters adds the packages that depend
// on them, and the check of the merge candidate over the whole module covers
// whatever neither names.
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

// WithImporters adds to the narrowing every package in the module at root that
// imports one of its packages, directly or through other packages, and names
// them in Importers, so a change to a package meets the tests of the code that
// uses it before review rather than first at the check of the merge candidate.
//
// The imports are read from the source, as NarrowGoPackages reads the tree,
// rather than from `go list`, which would build the module's whole package
// graph to answer a question the import lines answer. Every Go file counts
// whatever its build constraints, so a package is never left out for being
// built only on another system. A package's test files count for that package
// alone: a package whose tests import a changed package is tested, and that
// does not make the packages importing it depend on the change. Directories
// the Go command ignores, the vendor tree, and nested modules are not read.
//
// A narrowing that is the whole module, or names no package, is returned as it
// is. Where the module's imports cannot be read, the whole module stands in,
// saying why: a doubt costs time rather than a check.
func (n Narrowing) WithImporters(root string) Narrowing {
	if n.Whole || len(n.Packages) == 0 {
		return n
	}
	graph, err := readImportGraph(root)
	if err != nil {
		return Narrowing{Whole: true, Reason: "the packages that import the changed ones could not be listed: " + err.Error()}
	}
	touched := map[string]bool{}
	reached := map[string]bool{}
	queue := append([]string(nil), n.Packages...)
	for _, pkg := range n.Packages {
		touched[pkg] = true
		reached[pkg] = true
	}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		for _, importer := range graph.importedBy[next] {
			if !reached[importer] {
				reached[importer] = true
				queue = append(queue, importer)
			}
		}
	}
	for pkg, imports := range graph.testImports {
		if reached[pkg] {
			continue
		}
		for _, imported := range imports {
			if reached[imported] {
				reached[pkg] = true
				break
			}
		}
	}
	var packages, importers []string
	for pkg := range reached {
		packages = append(packages, pkg)
		if !touched[pkg] {
			importers = append(importers, pkg)
		}
	}
	sort.Strings(packages)
	sort.Strings(importers)
	return Narrowing{Packages: packages, Importers: importers}
}

// importGraph is the module's packages, as "./dir" patterns, and what imports
// what among them: importedBy follows the packages' own source, and
// testImports is what each package's test files import that its source does
// not.
type importGraph struct {
	importedBy  map[string][]string
	testImports map[string][]string
}

// readImportGraph reads the import lines of every Go file in the module at
// root, keeping the imports of the module's own packages.
func readImportGraph(root string) (importGraph, error) {
	module, err := modulePath(filepath.Join(root, "go.mod"))
	if err != nil {
		return importGraph{}, err
	}
	imports := map[string]map[string]bool{}
	testImports := map[string]map[string]bool{}
	fileset := token.NewFileSet()
	walkErr := filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if file == root {
				return nil
			}
			name := entry.Name()
			if ignoredByGo(name) || name == "vendor" {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(file, "go.mod")); err == nil {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		relative, err := filepath.Rel(root, filepath.Dir(file))
		if err != nil {
			return err
		}
		pkg := pattern(filepath.ToSlash(relative))
		// A file whose imports cannot be parsed adds no edge: it does not
		// compile, and the package's own tests say so wherever they run.
		parsed, err := parser.ParseFile(fileset, file, nil, parser.ImportsOnly)
		if err != nil || parsed == nil {
			return nil
		}
		into := imports
		if strings.HasSuffix(entry.Name(), "_test.go") {
			into = testImports
		}
		for _, spec := range parsed.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			var dir string
			switch {
			case imported == module:
				dir = "."
			case strings.HasPrefix(imported, module+"/"):
				dir = strings.TrimPrefix(imported, module+"/")
			default:
				continue
			}
			if into[pkg] == nil {
				into[pkg] = map[string]bool{}
			}
			into[pkg][pattern(dir)] = true
		}
		return nil
	})
	if walkErr != nil {
		return importGraph{}, walkErr
	}
	graph := importGraph{importedBy: map[string][]string{}, testImports: map[string][]string{}}
	for pkg, imported := range imports {
		for dependency := range imported {
			if dependency != pkg {
				graph.importedBy[dependency] = append(graph.importedBy[dependency], pkg)
			}
		}
	}
	for pkg, imported := range testImports {
		for dependency := range imported {
			if dependency != pkg && !imports[pkg][dependency] {
				graph.testImports[pkg] = append(graph.testImports[pkg], dependency)
			}
		}
	}
	return graph, nil
}

// pattern is a module-relative directory as the "./dir" pattern the Go command
// takes, and "." for the module's root.
func pattern(directory string) string {
	if directory == "." || directory == "" {
		return "."
	}
	return "./" + directory
}

// modulePath is the path the module directive in a go.mod file declares.
func modulePath(goMod string) (string, error) {
	file, err := os.Open(goMod)
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if index := strings.Index(line, "//"); index >= 0 {
			line = strings.TrimSpace(line[:index])
		}
		rest, found := strings.CutPrefix(line, "module")
		if !found || (rest != "" && rest[0] != ' ' && rest[0] != '\t') {
			continue
		}
		rest = strings.TrimSpace(rest)
		if unquoted, err := strconv.Unquote(rest); err == nil {
			rest = unquoted
		}
		if rest != "" {
			return rest, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", errors.New(goMod + " declares no module path")
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
