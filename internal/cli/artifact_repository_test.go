package cli

// The tests here read this repository's own artifacts rather than a fixture,
// because a fixture cannot fail the way that matters.
//
// Every artifact test in the package below builds a synthetic store in a
// temporary directory, and every document in one satisfies whatever the loader
// currently requires, because the test wrote it that way. The documents that are
// actually governed — the brief, the goals, the non-goals, and the decision
// records — were written before any given rule and are the ones a tightened
// loader silently drops. A document that stops loading does not fail loudly: it
// leaves the set, and what referred to it is reported as naming something nobody
// wrote.
//
// Whether the artifacts could be read at all is about the reader, which is a
// developer's to fix, so it fails here. What a successful read reported about
// one document — a `supports` entry naming nothing, an artifact reaching no
// brief, a revision recorded by a role that does not own the file — is about a
// document in an artifact home, which every developer's diff refuses, so it is
// escalated to its owner and fails `yoyo artifact check` instead.
// internal/governeddoc says why.

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/governeddoc"
)

func TestThisRepositoryOwnArtifactsAreReadableByTheHarness(t *testing.T) {
	t.Parallel()

	set, cfg := repositoryArtifacts(t)
	// A repository whose homes moved would otherwise pass this by recording
	// nothing at all. That is the reader looking in the wrong place rather than
	// any document being wrong, so it fails here.
	if len(set.Artifacts) == 0 {
		t.Fatal("this repository records no artifacts; the homes are wrong or nothing is governed")
	}
	// A file the loader refuses is failed by the check beside
	// this one, which exists to catch a loader that stopped accepting what this
	// repository ships (governed_documents_repository_test.go). What is routed
	// here is the rest: every reference problem is reported over documents that
	// loaded rather than refusing them — a `supports` entry naming a document
	// nobody wrote, a document nothing connects to the brief, a revision crossing
	// the ownership boundary — and each is a file in an artifact home that only
	// its owner may open.
	defects := make([]governeddoc.Defect, 0, len(set.ReferenceProblems))
	for _, problem := range set.ReferenceProblems {
		detail := string(problem.Kind) + " — " + problem.Reason
		defects = append(defects, governeddoc.Defect{Path: problem.Path, Detail: detail})
	}
	governeddoc.Report(cfg, defects, t.Errorf, governeddoc.Escalate)
}

func TestThisRepositoryOwnArtifactsRecordAnOwnerForEveryKind(t *testing.T) {
	t.Parallel()

	// Each kind this repository actually files is one the ownership table has to
	// place. A kind with no owner cannot be written through the harness at all,
	// so finding one here means a governed document is outside the boundary
	// rather than inside it.
	//
	// This one is not routed anywhere, and deliberately: a document whose kind is
	// unknown is refused by Validate and never reaches this loop, so a kind that
	// loaded and has no owner is the table in internal/artifact disagreeing with
	// itself. That is code, and it is a developer's to fix.
	set, _ := repositoryArtifacts(t)
	for _, recorded := range set.Artifacts {
		if _, known := artifact.Owner(recorded.Kind); !known {
			t.Errorf("%s is of kind %q, which no role owns", recorded.Path, recorded.Kind)
		}
	}
}

// repositoryArtifacts loads this repository's artifacts as the harness loads
// them, through the configuration rather than a guessed set of homes: a test
// that hardcoded the directories would keep passing after the project moved
// them, which is one of the ways a gate quietly stops running.
func repositoryArtifacts(t *testing.T) (artifact.Set, config.Config) {
	t.Helper()

	repository, cfg := repositoryConfiguration(t)
	set, err := artifactStore(repository, cfg.Product).Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return set, cfg
}
