package doclink

// The check itself, run over this repository rather than over a fixture. This
// is the whole point of the package: the fixtures above prove the checker works,
// and this is what makes a broken link in this project's documentation fail a
// declared check instead of costing a reviewer a paragraph saying they could not
// verify it.
//
// It runs under `make test`, which is one of this project's declared checks, so
// it is checked on every verify pass of every run — including each repair — for
// the same reason the artifact and goals gates beside it are.
//
// Where the broken link is decides what happens about it. Most of this
// repository's Markdown is a developer's to edit, and a link broken there still
// fails here. A link written inside an artifact home is in a document every
// developer's diff refuses, so it is escalated to the role that owns that home
// and fails `yoyo artifact check` instead — see internal/governeddoc.

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/governeddoc"
)

// repositoryRoot is the checkout these tests run in, reached from the package
// directory. The documents are read where they actually live rather than from a
// copy, because a copy is exactly what cannot have the link that is broken.
const repositoryRoot = "../.."

func TestThisRepositoryOwnDocumentationLinksResolve(t *testing.T) {
	t.Parallel()

	documents, err := Documents(repositoryRoot)
	if err != nil {
		t.Fatalf("Documents() error = %v", err)
	}
	// A walk that found nothing would report no broken links, which is the one
	// way this gate can pass while checking nothing at all.
	if len(documents) == 0 {
		t.Fatal("no Markdown documents were found in this repository; the walk is looking in the wrong place")
	}
	problems, err := Check(repositoryRoot)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	// Reported one at a time rather than as a count: what a reader needs is the
	// file, the line, and the target, and a tally sends them looking.
	defects := make([]governeddoc.Defect, 0, len(problems))
	for _, problem := range problems {
		detail := fmt.Sprintf("line %d: %s", problem.Line, problem.Reason)
		defects = append(defects, governeddoc.Defect{Path: problem.Path, Detail: detail})
	}
	cfg, read := repositoryConfiguration()
	if !read {
		// Nothing here then knows which directories are artifact homes, so every
		// broken link is reported as one to fix here. That is the stricter of the
		// two answers, which is the right way to be wrong: a link this gate failed
		// on and could have escalated costs one message, and the reverse costs a
		// broken link nobody fails on. The configuration is itself a protected path,
		// so it is not this check's to diagnose either.
		t.Log("this repository's configuration could not be read; every broken link below is reported as one to fix here")
	}
	governeddoc.Report(cfg, defects, t.Errorf, governeddoc.Escalate)
}

// repositoryConfiguration is this repository's own configuration, discovered the
// way the commands discover it. It is read rather than assumed for the reason
// every other gate reads it: which directories are artifact homes is the
// project's to say, and a hardcoded list would route a moved home to nobody.
func repositoryConfiguration() (config.Config, bool) {
	root, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return config.Config{}, false
	}
	discovered, err := config.Discover(root)
	if err != nil {
		return config.Config{}, false
	}
	loaded, err := config.Load(discovered)
	if err != nil {
		return config.Config{}, false
	}
	return loaded, true
}

// releasePreamble is appended to every release's notes by scripts/release-body.sh
// and handed to `gh release create --notes-file` by .github/workflows/release.yml.
const releasePreamble = ".github/release-notes-preamble.md"

// The one link in this repository that a later change here cannot correct. The
// preamble points a newcomer at the README's getting-started section as the
// repo-root URL, because release notes are read on the forge rather than in a
// checkout, and that text is published: every release already out carries it,
// and nothing this repository does afterwards rewrites those notes. So the
// anchor is frozen — docs/docs-map.md records it as Tier 1 — and the test above
// is what holds the freeze, by resolving the URL against the README the same as
// any other link.
//
// This asserts the coverage rather than the link: the test above already fails
// when the heading moves, and would go on passing in silence if the URL spelling
// stopped being resolved at all. What is checked here is that the preamble's
// fragments are still being looked for in the README, so the freeze cannot be
// lost by a change to the checker any more than by a change to the README.
func TestThePublishedReleaseNotesLinkIntoTheReadmeIsHeldByThisChecker(t *testing.T) {
	t.Parallel()

	content, err := read(filepath.Join(repositoryRoot, filepath.FromSlash(releasePreamble)))
	if err != nil {
		t.Fatalf("read %s: %v", releasePreamble, err)
	}
	home := forgeHome(repositoryRoot)
	if home == "" {
		t.Fatal("this repository's forge home could not be derived from go.mod, so no link written as a URL into it is resolved")
	}
	// A README with no headings at all is the restructure this guards against,
	// arrived at without touching the checkout: every fragment the preamble points
	// into the README is then a fragment the README does not carry.
	moved := map[string]map[string]bool{"README.md": {}, releasePreamble: {}}
	problems := problemsIn(repositoryRoot, home, releasePreamble, content, moved)
	if len(problems) == 0 {
		t.Fatalf("%s makes no link into the README that this checker resolves, so a heading it names could move and every published release's notes would point at nothing with no check to say so", releasePreamble)
	}
	for _, problem := range problems {
		if !strings.Contains(problem.Reason, "README.md carries no heading") {
			t.Errorf("a problem reported against a README with no headings names something else: %s", problem)
		}
	}
}
