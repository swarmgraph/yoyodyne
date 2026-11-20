package contextbundle

import (
	"os"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// A scheduled intake query's rules document is read by the message reader and
// is not delivered to every role, even filed under the specification home or
// beside a role's own documents. Every other document there still is, and a
// caller that withholds nothing is given exactly what it was before.
func TestWithheldDocumentsAreNotCarriedAsIntent(t *testing.T) {
	root := intentFixture(t)
	const rules = "docs/product/intake/rules.md"
	writeProductFile(t, root, rules, "# Intake rules\n\n## Rules\n\n- [slack-bug-report] A person reports something broken.\n")
	writeProductFile(t, root, "docs/designs/intake/rules.md", "# Designs rules\n\n## Rules\n\n- [design-copy] Kept beside the designs.\n")
	withheld := []string{rules, "docs/designs/intake/rules.md"}
	item := beads.WorkItem{ID: "yoyodyne-1", Title: "Task", Status: "in_progress", Description: "Build it."}

	carried, err := Assemble(Request{RepositoryRoot: root, WorkItem: item, Specifications: "docs/product"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(carried.Text, "slack-bug-report") {
		t.Fatalf("with nothing withheld the document is product intent like any other:\n%s", carried.Text)
	}

	revision := &Revision{
		Name: "base commit abc123",
		ListFiles: func() ([]string, error) {
			return []string{"docs/product/brief.md", rules}, nil
		},
		Read: func(path string, _ int64) (int64, []byte, error) {
			content, err := os.ReadFile(root + "/" + path)
			if err != nil {
				return 0, nil, ErrNotAtRevision
			}
			return int64(len(content)), content, nil
		},
	}
	work, err := Assemble(Request{RepositoryRoot: root, WorkItem: item, Specifications: "docs/product", Withheld: withheld})
	if err != nil {
		t.Fatal(err)
	}
	reviewed, err := Assemble(Request{RepositoryRoot: root, WorkItem: item, Specifications: "docs/product", Revision: revision, Withheld: withheld})
	if err != nil {
		t.Fatal(err)
	}
	branch, err := AssembleIntent(root, "", "docs/product", revision, nil, withheld)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
		RoleDocuments:           []DocumentSet{{Label: "Design", Directory: "docs/designs"}},
		Withheld:                withheld,
	})
	if err != nil {
		t.Fatal(err)
	}
	for scope, text := range map[string]string{"developer": work.Text, "reviewer": reviewed.Text, "branch review": branch, "conversation": conversation.Text} {
		for _, absent := range []string{"slack-bug-report", "design-copy", "intake/rules.md"} {
			if strings.Contains(text, absent) {
				t.Errorf("%s context carries the withheld rules document (%q):\n%s", scope, absent, text)
			}
		}
		if !strings.Contains(text, "docs/product/brief.md") {
			t.Errorf("%s context lost the rest of the specification home:\n%s", scope, text)
		}
	}
	if !strings.Contains(work.Text, "The extra document nobody reads today.") {
		t.Errorf("withholding one document dropped another:\n%s", work.Text)
	}
}

func TestWithholdDropsOnlyTheNamedPaths(t *testing.T) {
	paths := []string{"docs/product/a.md", "docs/product/intake/rules.md", "docs/product/b.md"}
	if got := strings.Join(withhold(paths, nil), ","); got != strings.Join(paths, ",") {
		t.Fatalf("withhold(nothing) = %s", got)
	}
	if got := strings.Join(withhold(paths, []string{"docs/product/intake/rules.md", "elsewhere.md"}), ","); got != "docs/product/a.md,docs/product/b.md" {
		t.Fatalf("withhold() = %s", got)
	}
}
