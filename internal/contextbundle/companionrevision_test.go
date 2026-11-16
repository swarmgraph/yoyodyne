package contextbundle

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// companionFixture is a project repository with no intent of its own, and a
// companion intent repository whose checkout carries uncommitted words that the
// commit the returned revision names does not.
func companionFixture(t *testing.T) (project, companion string, revision *Revision) {
	t.Helper()
	project = t.TempDir()
	companion = t.TempDir()
	for path, content := range map[string]string{
		"docs/product/brief.md":      "# Brief\n\nUncommitted edit nobody reviewed.\n",
		"docs/product/draft.md":      "# Draft\n\nOnly in the checkout.\n",
		"docs/designs/widget.md":     "# Widget\n\nUncommitted widget design.\n",
		"docs/designs/checkout-only": "x",
	} {
		full := filepath.Join(companion, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	committed := map[string]string{
		"docs/product/brief.md":  "# Brief\n\nCommitted and reviewed words.\n",
		"docs/designs/widget.md": "# Widget\n\nCommitted widget design.\n",
	}
	revision = &Revision{
		Name: "commit abc123 of the companion intent repository's main",
		ListFiles: func() ([]string, error) {
			var files []string
			for path := range committed {
				files = append(files, path)
			}
			sort.Strings(files)
			return files, nil
		},
		Read: func(path string, _ int64) (int64, []byte, error) {
			content, held := committed[path]
			if !held {
				return 0, nil, fmt.Errorf("%w: %s", ErrNotAtRevision, path)
			}
			return int64(len(content)), []byte(content), nil
		},
	}
	return project, companion, revision
}

func TestCompanionIntentIsReadAtItsRevisionNotItsCheckout(t *testing.T) {
	t.Parallel()
	project, companion, revision := companionFixture(t)
	item := beads.WorkItem{ID: "yoyodyne-1", Title: "Task", Status: "in_progress",
		Description: "Follow [the widget design](docs/designs/widget.md)."}
	bundle, err := Assemble(Request{
		RepositoryRoot: project, IntentRoot: companion, IntentRevision: revision,
		WorkItem: item, Specifications: "docs/product",
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if !strings.Contains(bundle.Text, "Committed and reviewed words.") {
		t.Fatalf("the committed specification was not carried:\n%s", bundle.Text)
	}
	if !strings.Contains(bundle.Text, "Committed widget design.") {
		t.Fatalf("a reference absent from the project was not read from the companion revision:\n%s", bundle.Text)
	}
	for _, unwanted := range []string{"Uncommitted edit nobody reviewed.", "Only in the checkout.", "Uncommitted widget design."} {
		if strings.Contains(bundle.Text, unwanted) {
			t.Fatalf("bundle carried the checkout's %q:\n%s", unwanted, bundle.Text)
		}
	}
	if !strings.Contains(bundle.Text, "as it stands at "+revision.Name) || !strings.Contains(bundle.Text, "uncommitted files") {
		t.Fatalf("the section does not name the revision:\n%s", bundle.Text)
	}
	if strings.Contains(bundle.Text, "as that repository's checkout stands") {
		t.Fatalf("the section still says the checkout is read:\n%s", bundle.Text)
	}
}

func TestCompanionIntentWithoutARevisionStillReadsTheCheckout(t *testing.T) {
	t.Parallel()
	project, companion, _ := companionFixture(t)
	item := beads.WorkItem{ID: "yoyodyne-1", Title: "Task", Status: "in_progress"}
	bundle, err := Assemble(Request{RepositoryRoot: project, IntentRoot: companion, WorkItem: item, Specifications: "docs/product"})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	for _, want := range []string{"Uncommitted edit nobody reviewed.", "Only in the checkout.", "as that repository's checkout stands"} {
		if !strings.Contains(bundle.Text, want) {
			t.Errorf("checkout read does not carry %q:\n%s", want, bundle.Text)
		}
	}
}

func TestAssembleIntentReadsCompanionIntentAtItsRevision(t *testing.T) {
	t.Parallel()
	project, companion, revision := companionFixture(t)
	text, err := AssembleIntent(project, companion, "docs/product", nil, revision)
	if err != nil {
		t.Fatalf("AssembleIntent() error = %v", err)
	}
	if !strings.Contains(text, "Committed and reviewed words.") || !strings.Contains(text, "as it stands at "+revision.Name) {
		t.Fatalf("the committed home was not read and named:\n%s", text)
	}
	for _, unwanted := range []string{"Uncommitted edit nobody reviewed.", "Only in the checkout."} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("branch intent carried the checkout's %q:\n%s", unwanted, text)
		}
	}
	checkout, err := AssembleIntent(project, companion, "docs/product", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(checkout, "Uncommitted edit nobody reviewed.") {
		t.Fatalf("a nil revision no longer reads the checkout:\n%s", checkout)
	}
}
