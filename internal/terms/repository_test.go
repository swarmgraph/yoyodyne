package terms

// The check itself, run over this repository rather than over a fixture. This is
// the point of the package: the fixtures above prove the checker works, and this
// is what makes a coinage nothing defines fail a declared check instead of
// waiting for a reviewer to notice it, or for an operator to meet a word with
// nowhere to look it up.
//
// It runs under `make test`, which is one of this project's declared checks, so
// it is applied on every verify pass of every run — including each repair — the
// same way the link, goals, and artifact gates beside it are.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repositoryRoot is the checkout these tests run in, reached from the package
// directory. The documents are read where they actually live rather than from a
// copy, because a copy is exactly what cannot carry the word that came back.
const repositoryRoot = "../.."

func TestThisRepositoryOwnCoinedTermsAreRegistered(t *testing.T) {
	t.Parallel()

	documents, err := Documents(repositoryRoot)
	if err != nil {
		t.Fatalf("Documents() error = %v", err)
	}
	// A walk that found nothing would report no coinage, which is the one way this
	// gate can pass while reading nothing at all.
	if len(documents) == 0 {
		t.Fatal("no documents were found in this repository's artifact homes; the walk is looking in the wrong place")
	}
	// The same for the guides, which is where the `yoyo triage rearm` prose is.
	guides, err := GuideFiles(repositoryRoot)
	if err != nil {
		t.Fatalf("GuideFiles() error = %v", err)
	}
	if len(guides) == 0 {
		t.Fatal("no guides were found in this repository; the walk is looking in the wrong place")
	}
	entries, err := Register(repositoryRoot)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("%s carries no entries; a register nothing is in permits nothing and proves nothing", RegisterPath)
	}
	problems, err := Check(repositoryRoot)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	for _, problem := range problems {
		// Reported one at a time rather than as a count: what a reader needs is the
		// file, the line, and what to write instead.
		t.Errorf("%s", problem)
	}
}

// `posture` was retired on 2026-09-25 (yoyodyne-ifd.437.6): the operator found it
// unclear. So this repository's own register has to refuse it, and not only a
// fixture's: a command's string and a governed document are both reported. The
// row excused three governed documents until their owner amended them, and the
// architect's amendments under yoyodyne-ifd.437.7 took the word out of all three,
// so the program manager design, once excused, is refused like any other.
func TestThisRepositoryRefusesPosture(t *testing.T) {
	t.Parallel()

	entries, err := Register(repositoryRoot)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	for _, entry := range entries {
		if entry.Term == "posture" {
			t.Fatalf("%s:%d registers posture; it was retired and belongs in the replaced table", RegisterPath, entry.Line)
		}
	}
	registerBody, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(RegisterPath)))
	if err != nil {
		t.Fatalf("read %s: %v", RegisterPath, err)
	}
	const amended = "docs/designs/program-manager.md"
	directory := root(t, string(registerBody), map[string]string{
		"internal/cli/one.go":   "package cli\n\nconst said = \"the reviewer's tool posture\"\n",
		"docs/designs/other.md": "# Other\n\nEvery role has a posture.\n",
		amended:                 "# Program manager\n\nIt holds no tool posture.\n",
	})
	problems, err := Check(directory)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	reported := make(map[string]bool)
	for _, problem := range problems {
		if problem.Term == "posture" && problem.Path != RegisterPath {
			reported[problem.Path] = true
			if !strings.Contains(problem.Reason, "tool access") {
				t.Errorf("Check() reason = %q, want the plain wording in it", problem.Reason)
			}
		}
	}
	for _, path := range []string{"internal/cli/one.go", "docs/designs/other.md", amended} {
		if !reported[path] {
			t.Errorf("Check() did not refuse posture in %s; reported %v", path, problems)
		}
	}
}

// The compound half of the check, over this repository: every compound word a
// person or a role reads is accounted for — registered, replaced, a term the
// inventory is waiting on, ordinary English by form or by the register's list,
// or one of the compounds the inventory lists as unread — and every word listed
// as unread is still written somewhere.
func TestThisRepositoryCoinsNoCompoundNothingDefines(t *testing.T) {
	t.Parallel()

	personas, err := PersonaFiles(repositoryRoot)
	if err != nil {
		t.Fatalf("PersonaFiles() error = %v", err)
	}
	if len(personas) == 0 {
		t.Fatal("no personas were found in this repository; the walk is looking in the wrong place")
	}
	problems, err := Compounds(repositoryRoot)
	if err != nil {
		t.Fatalf("Compounds() error = %v", err)
	}
	for _, problem := range problems {
		t.Errorf("%s", problem)
	}
}
