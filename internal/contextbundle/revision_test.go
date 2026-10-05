package contextbundle

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// revisionOf is a revision holding exactly the files given, so a test can tell
// what was read at it from what the working tree holds.
func revisionOf(name string, files map[string]string) *Revision {
	return &Revision{
		Name: name,
		ListFiles: func() ([]string, error) {
			var paths []string
			for path := range files {
				paths = append(paths, path)
			}
			return paths, nil
		},
		Read: func(path string, maxBytes int64) (int64, []byte, error) {
			content, ok := files[path]
			if !ok {
				return 0, nil, fmt.Errorf("%s: %w", path, ErrNotAtRevision)
			}
			if int64(len(content)) > maxBytes {
				return int64(len(content)), nil, nil
			}
			return int64(len(content)), []byte(content), nil
		},
	}
}

// A reference read at a revision is the revision's copy, never the working
// tree's, and every way it can be carried — whole, excerpted, or stated as left
// out — names the revision it came from.
func TestAssembleReadsReferencesAtARevisionAndLabelsThem(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "docs", "guide.md"), "the checkout's later copy\n")
	writeFile(t, filepath.Join(root, "docs", "new.md"), "a deliverable the change itself creates\n")
	const name = "base commit 0123456789abcdef"
	revision := revisionOf(name, map[string]string{
		"docs/guide.md": "the base's copy\n",
		"docs/big.md":   sectionedDocument(30, 12, 17, "zarquon telemetry"),
		"docs/none.md":  strings.Repeat("no headings at all. ", 1200),
	})
	item := beads.WorkItem{
		ID:          "yoyodyne-1",
		Title:       "Report zarquon telemetry",
		Status:      "open",
		Description: "Extract docs/guide.md into docs/new.md; docs/big.md and docs/none.md describe the zarquon telemetry.",
	}
	const maxBytes = 8 << 10

	bundle, err := Assemble(Request{RepositoryRoot: root, WorkItem: item, MaxBytes: maxBytes, Revision: revision})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if len(bundle.Text) > maxBytes {
		t.Fatalf("bundle is %d bytes, exceeding the %d it was budgeted", len(bundle.Text), maxBytes)
	}
	for _, want := range []string{
		"## Referenced file: docs/guide.md (at " + name + ")\n\ndocs/guide.md as it stands at " + name + ", which may differ from the file as it stands now.",
		"the base's copy",
		"## Referenced file: docs/big.md (excerpt, at " + name + ")",
		"The whole file is at\n" + name,
		"## Section 17",
		"## Referenced file: docs/none.md (omitted, at " + name + ")",
		"consult it at " + name,
	} {
		if !strings.Contains(bundle.Text, want) {
			t.Fatalf("bundle omitted %q: %s", want, bundle.Text)
		}
	}
	for _, unwanted := range []string{"the checkout's later copy", "docs/new.md (", "in the worktree"} {
		if strings.Contains(bundle.Text, unwanted) {
			t.Fatalf("bundle carried %q, which the revision does not hold: %s", unwanted, bundle.Text)
		}
	}
}
