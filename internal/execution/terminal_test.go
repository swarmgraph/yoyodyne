package execution_test

// Every place in the harness that writes or reads a provider invocation's
// terminal, and what each of them owes.
//
// A terminal is the only event carrying what an invocation cost, and what that
// money is attributed to is decided entirely by the role the terminal names. So
// the claim the phase split rests on — every terminal in a run's log says whose
// invocation it ended — is a claim about every emitter rather than about the one
// somebody remembered. An enumeration held in a comment goes stale the first
// time a second backend, a summarizer, or a shadow reviewer learns to write one,
// and it goes stale silently, which is exactly the failure the claim exists to
// prevent: money landing in a phase bucket because nothing said it should not.
//
// So the enumeration is a test. It reads the tree for every file that names a
// terminal event type at all and fails on any this table does not account for.
// A new one therefore arrives with a failing test naming what it owes, rather
// than as an invocation that quietly inflates somebody's repair column.
//
// It is deliberately a source sweep rather than a type assertion, for the reason
// the provider-invocation sweep beside it is: nothing in Go can express "and
// this emitter sets the role". What is enforced is that the set is closed and
// that each member has a written account; the accounts themselves are held by
// the tests named in them.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// terminalEventSites is every file that names run.completed or run.failed, and
// what it does with one. An emitter owes the role that made the invocation, in
// the payload, under "role"; a reader owes nothing but is listed so that adding
// an emitter cannot hide among the readers.
var terminalEventSites = map[string]string{
	"internal/execution/event.go": "declares them, and states the contract: from TerminalRoleSchemaVersion a terminal " +
		"names the role that made the invocation, because where a terminal sits says nothing about whose it is",
	"internal/backend/claudecode/parser.go": "emits them. parseResult writes the role " +
		"the invocation was made as into the payload, asserted by TestATerminalNamesTheRoleTheInvocationWasMadeAs",
	"internal/backend/codex/parser.go": "emits them. parseTerminal writes the role the invocation was made as into " +
		"the payload and no cost at all, because Codex reports what an invocation read and wrote and never what it " +
		"cost; asserted by TestATerminalCarriesTokensAndNoPrice",
	"internal/runstate/price.go": "reads them: the phase split places each terminal by the role it names and places " +
		"one that names none nowhere, asserted by TestStoreWillNotPlaceATerminalThatCouldHaveNamedItsPhaseAndDidNot",
	"internal/chat/activity.go": "reads them: a conversation's activity line says a turn finished, and attributes no " +
		"money to any phase",
	"internal/orchestrator/recordedbackend.go": "reads them: erasedSession reads the role a terminal names to tell " +
		"the developer's sessions from the reviewer's when it restores a session an earlier attempt erased, and " +
		"attributes no money to any phase",
}

// TestEveryTerminalEventSiteIsAccountedFor fails when the tree grows a file that
// touches a terminal event and this table does not say what it does with one.
// The branch reviewer and the exchange voice are the cases worth stating out
// loud: both make provider invocations whose terminals are written for them by
// the backend adapter, so they are absent here because they emit nothing of
// their own rather than because they were overlooked. A future backend adapter,
// or anything that writes a terminal by hand, fails this test until it says how
// its invocations are attributed.
func TestEveryTerminalEventSiteIsAccountedFor(t *testing.T) {
	t.Parallel()

	found := terminalSites(t, moduleRoot(t))
	var unaccounted []string
	for _, site := range found {
		if _, accounted := terminalEventSites[site]; !accounted {
			unaccounted = append(unaccounted, site)
		}
	}
	if len(unaccounted) > 0 {
		t.Fatalf("these touch a provider invocation's terminal and nothing here says what they do with one: %v\n"+
			"A file that emits a terminal must put the role the invocation was made as in the payload under \"role\", or the money on it lands in no phase at all. Add it to terminalEventSites either way.",
			unaccounted)
	}
	// The table going stale the other way is worth catching too: an entry for a
	// file that no longer touches a terminal is an account of nothing, and it
	// would hide a genuinely new emitter behind a table that looks maintained.
	for site := range terminalEventSites {
		if !listed(found, site) {
			t.Errorf("terminalEventSites names %s, which no longer touches a terminal event", site)
		}
	}
}

// terminalSites is every non-test file naming either terminal event type. It
// matches the identifier rather than the string, so a log line quoting
// "run.completed" is not a site and the constants' own names are; comments are
// not parsed into the tree, so prose about them is not one either. Tests are
// excluded because what is being enumerated is what the harness does.
func terminalSites(t *testing.T, root string) []string {
	t.Helper()

	var sites []string
	err := filepath.WalkDir(root, func(candidate string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "bin", "dist", "testdata":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), candidate, nil, 0)
		if err != nil {
			return err
		}
		if !namesATerminal(parsed) {
			return nil
		}
		relative, err := filepath.Rel(root, candidate)
		if err != nil {
			return err
		}
		sites = append(sites, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		t.Fatalf("read the tree for terminal events: %v", err)
	}
	sort.Strings(sites)
	if len(sites) == 0 {
		t.Fatal("no terminal event found anywhere, which means this sweep is looking for the wrong thing rather than that the harness records no invocation")
	}
	return sites
}

func namesATerminal(file *ast.File) bool {
	named := false
	ast.Inspect(file, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if !ok {
			return true
		}
		if identifier.Name == "EventRunCompleted" || identifier.Name == "EventRunFailed" {
			named = true
		}
		return !named
	})
	return named
}

// moduleRoot is the module this test is part of, found by walking up to the
// go.mod rather than counting directories, so moving this file does not silently
// sweep a subtree.
func moduleRoot(t *testing.T) string {
	t.Helper()

	directory, err := os.Getwd()
	if err != nil {
		t.Fatalf("locate the working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("no go.mod above this test, so there is no tree to sweep")
		}
		directory = parent
	}
}

func listed(haystack []string, needle string) bool {
	for _, candidate := range haystack {
		if candidate == needle {
			return true
		}
	}
	return false
}
