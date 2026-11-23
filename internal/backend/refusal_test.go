package backend_test

// Every place the harness asks a provider for work, and what happens at each of
// them when the answer is "no capacity".
//
// An exhausted usage limit is the one refusal that means hours in which nothing
// happens, so the claim the harness makes about it is a claim about every
// process rather than about the ones somebody remembered: any out-of-tokens
// refusal, wherever it is met, leaves a durable record the Slack sink says at
// warning severity. A claim of that shape cannot be held by enumerating the call
// sites in a comment, because the enumeration goes stale the first time somebody
// adds one — and the way it goes stale is silent, which is exactly the failure
// the claim exists to prevent.
//
// So the enumeration is a test. It reads the tree for every construction of a
// RunRequest, which is the one thing every provider invocation must build, and
// fails on any that this table does not account for. A new provider invocation
// therefore arrives with a failing test naming what it owes, rather than as a
// process that meets a limit and says nothing.
//
// It is deliberately a source sweep rather than a type assertion. Nothing in Go
// can express "and this call site records a refusal", so what is enforced is
// that the set is closed and that each member has a written account; the
// accounts themselves are held by the tests named in them.

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

// providerInvocations is every file that asks a provider for work, and how an
// out-of-tokens refusal met there becomes durable. A refusal inside a run is
// said by the run parking, which is the run's own state and needs nothing else;
// everywhere else records one, because there is no run record to cross.
var providerInvocations = map[string]string{
	"internal/orchestrator/pipeline.go": "a run: pauseForUsageLimit records the limit and the deadline in durable run " +
		"state before it waits, and notify.FromRun says the park at warning severity",
	"internal/review/reviewer.go": "a review: the refusal travels back on review.Result, and whoever asked for the " +
		"review accounts for it — the pipeline by parking the run, BranchReviewer by recording it",
	"internal/chat/chat.go": "a conversation turn: Session.noteUsageLimit records the refusal against the " +
		"conversation, because a turn has no run to park. A turn an operator typed — `yoyo chat`, " +
		"interactive or --message — is waited out by Session.waitOutUsageLimit under the bounds a run " +
		"waits under and reissued on the same session, and fails at their terminal with the reset stated " +
		"only where that wait is one the harness will not take; one the harness took to put a stopped run " +
		"to the development manager is given no such bounds and fails carrying chat.ErrProviderCapacity, " +
		"and the stoppage keeps its delivery for after the limit resets rather than counting as having " +
		"been put to her",
	"internal/cli/exchange.go": "an inter-role ask exchange answering a round: exchangeVoice.noteUsageLimit " +
		"records the refusal naming the exchange it stopped, because an answering round has neither a run " +
		"to park nor a conversation of its own to fail at anybody's terminal",
	"internal/repositoryask/asker.go": "the reading helper's session, asked by a conversation role: the " +
		"conversation hands it a provider wrapped by chat's usageLimitNoting, which records the refusal " +
		"against the asking conversation through Session.noteUsageLimit, and the ask ends with the refusal " +
		"on its answer for the role to be told rather than waiting it out",
	"internal/cli/sidestream.go": "a side conversation taking a turn: sideVoice.noteSideUsageLimit records the " +
		"refusal naming the side thread it stopped and the main conversation that thread is held beside, " +
		"because a side turn has no run to park and the conversation it is beside is not its own to fail — " +
		"and a side thread is exactly the question somebody asked because the main thread was already busy",
}

// TestEveryProviderInvocationAccountsForAnExhaustedLimit fails when the tree
// grows a provider invocation this table does not name. The scheduler is the
// case worth stating out loud: a watching `yoyo work` session reads the tracker
// and starts runs, and the one thing it asks a provider itself is a conversation
// turn — a stopped run put in front of the development manager — which is
// accounted for as a conversation turn above rather than as a call site of its
// own, because it is the conversation's own invocation. Teaching selection to
// ask a provider anything else — a model choosing between ready items — fails
// this test until that call says what becomes of a refusal.
func TestEveryProviderInvocationAccountsForAnExhaustedLimit(t *testing.T) {
	t.Parallel()

	found := invocationSites(t, repositoryRoot(t))
	var unaccounted []string
	for _, site := range found {
		if _, accounted := providerInvocations[site]; !accounted {
			unaccounted = append(unaccounted, site)
		}
	}
	if len(unaccounted) > 0 {
		t.Fatalf("these ask a provider for work and nothing here says what an exhausted usage limit does to them: %v\n"+
			"Record the refusal where it is met — runstate.UsageLimitStore is what every process outside a run writes to — and add it to providerInvocations.",
			unaccounted)
	}
	// The table going stale the other way is worth catching too: an entry for a
	// call site that no longer exists is an account of nothing, and it would
	// hide a genuinely new one behind a table that looks maintained.
	for site := range providerInvocations {
		if !contains(found, site) {
			t.Errorf("providerInvocations names %s, which no longer asks a provider for anything", site)
		}
	}
}

// invocationSites is every file that builds a backend.RunRequest, which is the
// one thing a provider invocation cannot avoid constructing. Tests are excluded:
// what is being enumerated is what the harness does, and a double that builds a
// request is exercising one of these rather than being another.
func invocationSites(t *testing.T, root string) []string {
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
		if !buildsRunRequest(parsed) {
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
		t.Fatalf("read the tree for provider invocations: %v", err)
	}
	sort.Strings(sites)
	if len(sites) == 0 {
		t.Fatal("no provider invocation found anywhere, which means this sweep is looking for the wrong thing rather than that the harness asks nothing of a provider")
	}
	return sites
}

// buildsRunRequest reports a file that constructs the request a provider
// invocation is made with, written as backend.RunRequest{…} outside this package
// and RunRequest{…} inside it.
func buildsRunRequest(file *ast.File) bool {
	built := false
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		switch named := literal.Type.(type) {
		case *ast.SelectorExpr:
			if package_, ok := named.X.(*ast.Ident); ok && named.Sel.Name == "RunRequest" && package_.Name == "backend" {
				built = true
			}
		case *ast.Ident:
			if named.Name == "RunRequest" && file.Name.Name == "backend" {
				built = true
			}
		}
		return !built
	})
	return built
}

// repositoryRoot is the module this test is part of, found by walking up to the
// go.mod rather than counting directories, so moving this file does not silently
// sweep a subtree.
func repositoryRoot(t *testing.T) string {
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

func contains(haystack []string, needle string) bool {
	for _, candidate := range haystack {
		if candidate == needle {
			return true
		}
	}
	return false
}
