package checks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A path list reads the way a .gitignore does: an unanchored pattern matches
// any component, a "/" anchors it at the root, a trailing "/" means a directory
// and what is under it, and a later "!" takes back what an earlier line covered.
func TestAPathListCoversWhatItsPatternsName(t *testing.T) {
	t.Parallel()

	patterns := []string{
		"/README.md",
		"/internal/",
		"!*_test.go",
		"!testdata/",
		"cmd",
	}
	for _, test := range []struct {
		path string
		want bool
	}{
		{path: "README.md", want: true},
		{path: "docs/README.md", want: false},
		{path: "internal/cli/status.go", want: true},
		{path: "internal/cli/status_test.go", want: false},
		{path: "internal/dashboard/testdata/renders/page.html", want: false},
		{path: "internal", want: false},
		{path: "cmd/yoyo/main.go", want: true},
		{path: "tools/cmd/main.go", want: true},
		{path: "docs/developing-yoyo.md", want: false},
		{path: "scripts/walk-getting-started.sh", want: false},
	} {
		touched, ok := Touching(patterns, []string{test.path})
		if ok != test.want {
			t.Errorf("Touching(%q) = %v, want %v", test.path, ok, test.want)
		}
		if ok && touched != test.path {
			t.Errorf("Touching(%q) named %q", test.path, touched)
		}
	}
}

// The first covered path is the one named, so the record says which path of
// the change added the check.
func TestTouchingNamesTheFirstPathTheListCovers(t *testing.T) {
	t.Parallel()

	touched, ok := Touching([]string{"/README.md", "/scripts/"}, []string{"docs/a.md", "./scripts/walk.sh", "README.md"})
	if !ok || touched != "scripts/walk.sh" {
		t.Fatalf("Touching() = %q, %v, want scripts/walk.sh", touched, ok)
	}
	if _, ok := Touching([]string{"/README.md"}, nil); ok {
		t.Fatal("an empty change touched something")
	}
}

// Comments and blank lines say nothing, and a line that is not a pattern is
// refused naming its line, rather than read as a pattern matching nothing.
func TestReadingAPathListRefusesALineThatIsNotAPattern(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, "list.paths"), []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	write("# a comment\n\n/README.md\n  !*_test.go  \n")
	patterns, err := ReadPathPatterns(root, "list.paths")
	if err != nil {
		t.Fatalf("ReadPathPatterns() error = %v", err)
	}
	if strings.Join(patterns, "|") != "/README.md|!*_test.go" {
		t.Fatalf("patterns = %q", patterns)
	}
	write("/README.md\n[unclosed\n")
	if _, err := ReadPathPatterns(root, "list.paths"); err == nil || !strings.Contains(err.Error(), "list.paths:2") {
		t.Fatalf("ReadPathPatterns() error = %v, want the bad line named", err)
	}
	write("!/\n")
	if _, err := ReadPathPatterns(root, "list.paths"); err == nil || !strings.Contains(err.Error(), "names no path") {
		t.Fatalf("ReadPathPatterns() error = %v, want a pattern naming nothing refused", err)
	}
	if _, err := ReadPathPatterns(root, "missing.paths"); err == nil {
		t.Fatal("a missing list read as an empty one")
	}
}

// This repository's own list for the getting-started walk parses, covers the README
// and the program it documents, and leaves out what the walk never builds or
// reads — which is what makes a change to a document elsewhere cost nothing.
func TestTheGettingStartedWalksListCoversTheReadmeAndTheProgram(t *testing.T) {
	t.Parallel()

	patterns, err := ReadPathPatterns("../..", "scripts/walk-adoption.paths")
	if err != nil {
		t.Fatalf("ReadPathPatterns() error = %v", err)
	}
	for _, covered := range []string{
		"README.md",
		"scripts/walk-getting-started.sh",
		"scripts/walk-adoption.paths",
		"Makefile",
		"go.mod",
		"cmd/yoyo/main.go",
		"internal/cli/status.go",
		"internal/config/builtin/v1/personas/developer.md",
	} {
		if _, ok := Touching(patterns, []string{covered}); !ok {
			t.Errorf("the getting-started walk's list does not cover %s", covered)
		}
	}
	for _, left := range []string{
		"docs/developing-yoyo.md",
		"internal/cli/status_test.go",
		"internal/dashboard/testdata/renders/page.html",
		"scripts/cut-release.sh",
		".beads/issues.jsonl",
	} {
		if _, ok := Touching(patterns, []string{left}); ok {
			t.Errorf("the getting-started walk's list covers %s, which the walk neither builds nor reads", left)
		}
	}
}
