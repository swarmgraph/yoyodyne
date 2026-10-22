package doctor

// What this repository's own checkout is diagnosed as. The rest of this
// package's tests build an installation to say what a check does; these two say
// what it currently reports here, which is the only place the indexes those
// checks are about actually live.
//
// Both are about files inside the artifact homes, which every developer's diff
// refuses. An index that is missing or has stopped answering is escalated to the
// role that owns its home rather than failing the build for everybody —
// internal/governeddoc says why, and `yoyo artifact check` is where it does
// fail. What stays a failure here is the harness's own code: a load that
// errored, an index read as a governed document, or a directory that yielded no
// constraint at all.

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/artifacthome"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/dashboard"
	"github.com/mason-bryant/yoyodyne/internal/governeddoc"
	"github.com/mason-bryant/yoyodyne/internal/invariant"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

func TestThisRepositoryReportsItsOwnArtifactHomesDocumented(t *testing.T) {
	t.Parallel()

	// The item this covers is done when `yoyo doctor` says this repository's homes
	// are documented, so the assertion is the check itself against this checkout
	// rather than a fixture that would keep passing while the real indexes were
	// missing.
	root, resolved := thisRepository(t)
	finding := (&diagnosis{}).checkArtifactHomes(root, root, resolved)
	if finding.Status != StatusOK {
		// An index that is missing or has stopped answering is a file in an
		// artifact home, so it is routed to that home's owner. One defect is made
		// for each home whose index is not written, against that home's own index
		// file, because the path is what decides the owner: a missing designs
		// index is the architect's, and reporting it against any one home would
		// name the wrong role and the wrong command to reach them.
		//
		// This escalating is not the class going unchecked: `yoyo artifact check`
		// reads the same index states and exits non-zero on one that is missing,
		// incomplete, or unreadable. What it does not read is whether an index still
		// matches the text the generator would write, which is artifacthome's own
		// decision — an operator who added a paragraph has not broken their index —
		// so a generator whose wording moved on leaves the doors saying less than it
		// does and nothing reports it.
		intent, err := repowrite.NewRoot(resolved.Config.Product.IntentRoot(root))
		if err != nil {
			t.Fatalf("artifact-readmes = %s: %s, and the homes could not be read again to say which: %v", finding.Status, finding.Summary, err)
		}
		defects := governeddoc.IndexDefects(artifacthome.Inspect(intent, resolved.Config))
		if len(defects) == 0 {
			// The finding is not OK and no home's index is unwritten: the check and
			// the inspection it is built on disagree, which is code rather than any
			// document, so it fails here.
			t.Fatalf("artifact-readmes = %s: %s (%s), yet every home's index reads as written", finding.Status, finding.Summary, finding.Detail)
		}
		governeddoc.Report(resolved.Config, defects, t.Errorf, governeddoc.Escalate)
		return
	}
	// The homes are named rather than counted, because which directory the check
	// read is the whole of what a reader would act on — the invariants home most
	// of all, since it carries its own identity scheme and is still a home this
	// check asks an index of. A home missing from the detail is the check having
	// stopped reading it, which is the reader rather than any document.
	product := resolved.Config.Product
	for _, home := range []string{
		product.Specifications,
		product.Specifications + "/goals",
		product.Designs,
		product.Decisions,
		product.Invariants,
	} {
		if !strings.Contains(finding.Detail, home+"/"+artifacthome.FileName) {
			t.Errorf("detail = %q, want %s among the homes it read", finding.Detail, home)
		}
	}
}

// The Node finding is asked only of a product carrying the dashboard page's
// render script, so a script that moved or was renamed would stand the finding
// down without a word: the one product that ships the dashboard would stop being
// asked whether it can draw it. The path is a constant three things share, and
// this is what holds that constant against the checkout it is about.
func TestThisRepositoryIsAskedAboutTheNodeItsDashboardNeeds(t *testing.T) {
	t.Parallel()

	root, _ := thisRepository(t)
	script := filepath.Join(root, filepath.FromSlash(dashboard.RenderScript))
	if info, err := os.Stat(script); err != nil || info.IsDir() {
		t.Fatalf("%s is not a file in this checkout (%v), so nothing here is asked about node and the page's renders go uncompared with nothing saying so", dashboard.RenderScript, err)
	}
	// The real machine, because what is being held is the gate rather than the
	// answer: this checkout produces a node finding whether or not the machine
	// running the suite has Node.
	findings := (&diagnosis{}).checkNode(root)
	if len(findings) != 1 || findings[0].Check != "node" {
		t.Fatalf("checkNode() = %+v, want the one node finding a product shipping the dashboard is given", findings)
	}
}

func TestTheIndexesThisRepositoryCarriesAreNotReadAsGovernedDocuments(t *testing.T) {
	t.Parallel()

	// An index is a file inside a home that is not a document of the kind filed
	// there, so every loader that walks a home has to skip it by name. This says
	// the ones this repository now carries are skipped rather than reported, for
	// the artifact homes and for the invariants directory both.
	root, resolved := thisRepository(t)
	product := resolved.Config.Product

	artifacts, err := artifact.StoreFor(root, product).Load()
	if err != nil {
		t.Fatalf("artifact Load() error = %v", err)
	}
	// The question here is whether the indexes are skipped, so a problem naming
	// one is the exemption having stopped working, which is this package's own
	// code and fails. A problem naming any other document is failed by the check
	// that loads every governed document with the real loader
	// (internal/cli/governed_documents_repository_test.go), and what the documents
	// say about each other is escalated by the artifact gate beside it; neither is
	// this test's question.
	isIndex := func(file string) bool { return strings.EqualFold(path.Base(file), artifacthome.FileName) }
	for _, problem := range artifacts.Problems {
		if isIndex(problem.Path) {
			t.Errorf("an index was reported as a governed document: %s", problem)
		}
	}
	for _, recorded := range artifacts.Artifacts {
		if isIndex(recorded.Path) {
			t.Errorf("%s was loaded as the artifact %q; an index states no intent anything refers to", recorded.Path, recorded.ID)
		}
	}

	invariants, err := (invariant.Store{RepositoryRoot: root, Directory: product.Invariants}).Load()
	if err != nil {
		t.Fatalf("invariant Load() error = %v", err)
	}
	for _, problem := range invariants.Problems {
		if isIndex(problem.Path) {
			t.Errorf("an index was reported as an invariant: %s", problem)
		}
	}
	// The constraints themselves still load, so a clean report is the index being
	// skipped rather than the whole directory going unread. A directory that
	// yielded nothing is the reader, and it fails here.
	if len(invariants.Active) == 0 {
		t.Fatal("no invariant loaded, so a clean problem list says nothing about the index")
	}
}

// thisRepository is the checkout these tests run in, read through the same
// discovery the commands use so a home that moves in the configuration moves
// here too.
func thisRepository(t *testing.T) (string, config.Resolved) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("Abs() error = %v", err)
	}
	if root, err = filepath.EvalSymlinks(root); err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	discovered, err := config.Discover(root)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	loaded, err := config.Load(discovered)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return root, config.Resolved{Config: loaded, Path: discovered}
}
