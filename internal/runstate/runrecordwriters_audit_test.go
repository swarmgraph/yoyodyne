package runstate

// The account of every place in this package that can write a whole run record,
// held to the code it describes.
//
// A run's routing changes only through UpdateRouting, and Save refuses a record
// whose routing is not the one stored (see Save and checkRoutingWrite). Both
// guarantees hold only while every write of a run record goes through one of
// those two, or through Create, which makes a record that does not exist yet
// and so has no stored routing to lose. A writer that replaced the record file
// any other way would drop or forge a run's routing without either check seeing
// it, and the run would be resumed on a backend nobody chose.
//
// So the sweep below fails on:
//
//   - a function naming a run record's path (statePath) that is not listed in
//     runRecordPathSites;
//   - a function calling writeState other than Save and UpdateRouting;
//   - a *Store method other than statePath building a name ending in exactly
//     ".json" by concatenation, which is how a writer would name the record
//     without asking statePath for it. The other stores in this package name
//     their own records this way, so they are not swept for it.
//
// It fails on a listed site that is gone too, so the table stays the account of
// what is there. What it does not see is a write reached by a path computed some
// other way — read back from a directory listing, say — and a writer outside
// this package; neither exists today.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// runRecordPathSites is every function that names a run record's file, and what
// it does with it.
var runRecordPathSites = map[string]string{
	"(*Store).Create":        "writes a record that must not exist yet, with O_EXCL, so there is no stored routing to lose or overwrite",
	"(*Store).writeState":    "the one replacement of an existing record; only Save and UpdateRouting call it, under the run's write lock",
	"(*Store).load":          "reads the record; Load and Read both go through it",
	"(*Store).storedRouting": "reads the stored routing so Save can compare it before writing",
}

// runRecordReplacers is every function allowed to call writeState.
var runRecordReplacers = map[string]bool{
	"(*Store).Save":          true,
	"(*Store).UpdateRouting": true,
}

// runRecordWriteSite is one place the sweep found, keyed by the function it sits
// in.
type runRecordWriteSite struct {
	names    bool // calls statePath
	replaces bool // calls writeState
	builds   bool // concatenates a name ending in ".json"
	at       token.Position
}

func TestEveryRunRecordWriteGoesThroughSaveOrUpdateRouting(t *testing.T) {
	fileSet := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fileSet, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, parsed)
	}
	problems, found := runRecordWriteProblems(fileSet, files)
	for _, problem := range problems {
		t.Error(problem)
	}
	var stale []string
	for key := range runRecordPathSites {
		if found[key] == nil || !found[key].names {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	for _, key := range stale {
		t.Errorf("%s is in runRecordPathSites and no longer names a run record's file; take it out of the table", key)
	}
	for key, why := range runRecordPathSites {
		if strings.TrimSpace(why) == "" {
			t.Errorf("%s is listed without saying what it does with a run record", key)
		}
	}
}

// TestTheRunRecordSweepCatchesABypassingWriter holds the sweep to the case it
// exists for, so a sweep that stopped seeing anything would fail here rather
// than pass above.
func TestTheRunRecordSweepCatchesABypassingWriter(t *testing.T) {
	const source = `package runstate

func (s *Store) statePath(runID string) (string, error) { return filepath.Join(s.root, runID+".json"), nil }
func (s *Store) writeState(state State) error { return nil }
func (s *Store) Save(state State) error { return s.writeState(state) }
func (s *Store) StopQuietly(state State) error {
	path, _ := s.statePath(state.RunID)
	return replaceJSONFile(s.root, path, "run state", state)
}
func (s *Store) ForceSave(state State) error { return s.writeState(state) }
func (s *Store) Sideways(state State) error {
	return replaceJSONFile(s.root, filepath.Join(s.root, state.RunID+".json"), "run state", state)
}
`
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, "bypass.go", source, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	found, _ := runRecordWriteProblems(fileSet, []*ast.File{parsed})
	problems := strings.Join(found, "\n")
	for _, want := range []string{"(*Store).StopQuietly names", "(*Store).ForceSave replaces", "(*Store).Sideways builds"} {
		if !strings.Contains(problems, want) {
			t.Errorf("the sweep did not report %q; it reported:\n%s", want, problems)
		}
	}
	if strings.Contains(problems, "(*Store).Save replaces") || strings.Contains(problems, "(*Store).statePath builds") {
		t.Errorf("the sweep reported an allowed site:\n%s", problems)
	}
}

// runRecordWriteProblems sweeps files for the sites described at the top of
// this file and returns one sentence for each that is not allowed, with every
// site it found.
func runRecordWriteProblems(fileSet *token.FileSet, files []*ast.File) ([]string, map[string]*runRecordWriteSite) {
	found := map[string]*runRecordWriteSite{}
	site := func(key string, at token.Pos) *runRecordWriteSite {
		if found[key] == nil {
			found[key] = &runRecordWriteSite{at: fileSet.Position(at)}
		}
		return found[key]
	}
	for _, parsed := range files {
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			key := functionName(function)
			sweepsNames := strings.HasPrefix(key, "(*Store).")
			ast.Inspect(function.Body, func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.CallExpr:
					switch calledName(node) {
					case "statePath":
						site(key, node.Pos()).names = true
					case "writeState":
						site(key, node.Pos()).replaces = true
					}
				case *ast.BinaryExpr:
					if literal, ok := node.Y.(*ast.BasicLit); sweepsNames && ok && node.Op == token.ADD && literal.Value == `".json"` {
						site(key, node.Pos()).builds = true
					}
				}
				return true
			})
		}
	}

	var problems []string
	for key, at := range found {
		if at.names {
			if _, listed := runRecordPathSites[key]; !listed {
				problems = append(problems, key+" names a run record's file (at "+at.at.String()+") and is not in runRecordPathSites. A whole run record is written only through Save, or UpdateRouting for its routing; if this only reads the record, list it with what it does.")
			}
		}
		if at.replaces && !runRecordReplacers[key] {
			problems = append(problems, key+" replaces a run record through writeState (at "+at.at.String()+"). Only Save and UpdateRouting may, because they hold the run's write lock and compare the stored routing first; call Save instead.")
		}
		if at.builds && key != "(*Store).statePath" {
			problems = append(problems, key+" builds a name ending in \".json\" (at "+at.at.String()+"), which is how a run record is named. Ask statePath for a run record's name and write it through Save; another record wants a suffix of its own, such as \".stop.json\".")
		}
	}
	sort.Strings(problems)
	return problems, found
}

// calledName is the name a call is made by, whether on a receiver or not.
func calledName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fun.Sel.Name
	case *ast.Ident:
		return fun.Name
	}
	return ""
}
