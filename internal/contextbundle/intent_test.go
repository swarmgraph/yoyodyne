package contextbundle

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// intentFixture is a specification home holding the brief, a goals document, an
// index, and one document that is neither the brief nor the goals — the case the
// operator's direction of 2026-09-27 is about: everything filed there is
// authoritative, not a fixed pair.
func intentFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeProductFile(t, root, "docs/product/brief.md", wellFormed)
	writeProductFile(t, root, "docs/product/goals/v1-goals.md", "# Goals doc\n\nWhy.\n\n## Goals\n\n- [one] One goal.\n")
	writeProductFile(t, root, "docs/product/README.md", "# docs/product\n\n**Owner.** The product manager.\n")
	writeProductFile(t, root, "docs/product/pricing-principles.md", "# Pricing principles\n\nThe extra document nobody reads today.\n")
	return root
}

// A document in the specification home that is neither the brief nor the goals
// reaches a conversation's briefing, labelled as authoritative product intent,
// and the index there arrives with its ownership statements framed as rules.
func TestAnExtraDocumentInTheSpecificationHomeReachesTheBriefing(t *testing.T) {
	root := intentFixture(t)
	bundle, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	for _, want := range []string{
		"## " + IntentHeading + ": docs/product/pricing-principles.md",
		"The extra document nobody reads today.",
		"## " + IntentHeading + ": docs/product/brief.md",
		"## Directory index: docs/product/README.md",
		"is a rule you work under, not a description",
	} {
		if !strings.Contains(bundle.Text, want) {
			t.Fatalf("briefing is missing %q:\n%s", want, bundle.Text)
		}
	}
}

// The same home reaches a developer's context: the work item is what the run is
// for, and every document under the specifications directory is carried after
// it, whatever the item's own prose named.
func TestAnExtraDocumentInTheSpecificationHomeReachesAWorkItemContext(t *testing.T) {
	root := intentFixture(t)
	item := beads.WorkItem{ID: "yoyodyne-1", Title: "Task", Status: "in_progress", Description: "Build it; see docs/product/brief.md."}
	bundle, err := Assemble(Request{RepositoryRoot: root, WorkItem: item, Specifications: "docs/product"})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	for _, want := range []string{
		"# Authoritative product intent",
		"## " + IntentHeading + ": docs/product/pricing-principles.md",
		"The extra document nobody reads today.",
		"## " + IntentHeading + ": docs/product/goals/v1-goals.md",
		"## Directory index: docs/product/README.md",
		"is a rule you work under, not a description",
	} {
		if !strings.Contains(bundle.Text, want) {
			t.Fatalf("work item context is missing %q:\n%s", want, bundle.Text)
		}
	}
	// The brief the item named is carried once, under the home's label, rather
	// than a second time as a referenced file.
	if strings.Contains(bundle.Text, "## Referenced file: docs/product/brief.md") {
		t.Fatalf("a document the home carried was carried again as a reference:\n%s", bundle.Text)
	}
	if strings.Index(bundle.Text, "# Assigned work item") > strings.Index(bundle.Text, "# Authoritative product intent") {
		t.Fatalf("the work item must come first:\n%s", bundle.Text)
	}
	if bundle.SpecificationsIncluded != 3 {
		t.Fatalf("SpecificationsIncluded = %d, want 3 (the index is not one)", bundle.SpecificationsIncluded)
	}

	// A caller that names no specification home is given none, which is what the
	// validation-only assembly relies on.
	bare, err := Assemble(Request{RepositoryRoot: root, WorkItem: item})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if strings.Contains(bare.Text, "# Authoritative product intent") {
		t.Fatalf("a context with no specification home carried one:\n%s", bare.Text)
	}
}

// A reviewer's context is read at the change's base, and so is the home: what a
// document says at the base is what the change was written against, and a
// document the base does not hold is left out rather than read from the tree.
func TestTheSpecificationHomeIsReadAtTheReviewedRevision(t *testing.T) {
	root := intentFixture(t)
	atBase := map[string]string{
		"docs/product/brief.md":              wellFormed,
		"docs/product/pricing-principles.md": "# Pricing principles\n\nAs the base had it.\n",
	}
	revision := &Revision{
		Name: "base commit abc123",
		ListFiles: func() ([]string, error) {
			return []string{"docs/product/brief.md", "docs/product/pricing-principles.md"}, nil
		},
		Read: func(path string, maxBytes int64) (int64, []byte, error) {
			content, held := atBase[path]
			if !held {
				return 0, nil, fmt.Errorf("%w: %s", ErrNotAtRevision, path)
			}
			return int64(len(content)), []byte(content), nil
		},
	}
	item := beads.WorkItem{ID: "yoyodyne-1", Title: "Task", Status: "in_progress"}
	bundle, err := Assemble(Request{RepositoryRoot: root, WorkItem: item, Specifications: "docs/product", Revision: revision})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if !strings.Contains(bundle.Text, "As the base had it.") || strings.Contains(bundle.Text, "nobody reads today") {
		t.Fatalf("the home was not read at the base:\n%s", bundle.Text)
	}
	if strings.Contains(bundle.Text, "## "+IntentHeading+": docs/product/goals/v1-goals.md") {
		t.Fatalf("a document the base does not hold was carried:\n%s", bundle.Text)
	}
	if !strings.Contains(bundle.Text, "as it stands at base commit abc123") {
		t.Fatalf("the section does not say which revision it was read at:\n%s", bundle.Text)
	}
}

func TestTheStandingSetAndItsGoalStatementsReachReviewAtTheBase(t *testing.T) {
	root := intentFixture(t)
	const goals = "# V1 goals\n\n## Goals\n\n- Make the surfaces read clearly.\n- Run development autonomously.\n- Trace changes to intent.\n\n## Standing goals\n\nThe first two goals apply to every change, whichever goal the item serves.\n"
	revision := &Revision{
		Name:      "base commit abc123",
		ListFiles: func() ([]string, error) { return []string{"docs/product/goals/v1-goals.md"}, nil },
		Read: func(path string, _ int64) (int64, []byte, error) {
			if path != "docs/product/goals/v1-goals.md" {
				return 0, nil, ErrNotAtRevision
			}
			return int64(len(goals)), []byte(goals), nil
		},
	}
	item := beads.WorkItem{ID: "yoyodyne-1", Title: "Record attribution", Status: "in_progress", Notes: "Goal served: Trace changes to intent."}
	bundle, err := Assemble(Request{RepositoryRoot: root, WorkItem: item, Specifications: "docs/product", Revision: revision})
	if err != nil {
		t.Fatal(err)
	}
	branch, err := AssembleIntent(root, "docs/product", revision)
	if err != nil {
		t.Fatal(err)
	}
	for scope, text := range map[string]string{"work item": bundle.Text, "branch": branch} {
		for _, want := range []string{goals, "base commit abc123", IntentHeading} {
			if !strings.Contains(text, want) {
				t.Errorf("%s context is missing %q", scope, want)
			}
		}
		if strings.Contains(text, "[one] One goal.") {
			t.Errorf("%s context read goals from the checkout rather than the base", scope)
		}
	}
}

// A home too large for its share is named rather than silently cut short.
func TestASpecificationHomeTooLargeForItsShareNamesWhatItLeftOut(t *testing.T) {
	root := intentFixture(t)
	writeProductFile(t, root, "docs/product/zz-huge.md", strings.Repeat("x ", 8<<10))
	item := beads.WorkItem{ID: "yoyodyne-1", Title: "Task", Status: "in_progress"}
	bundle, err := Assemble(Request{RepositoryRoot: root, WorkItem: item, Specifications: "docs/product", MaxBytes: 16 << 10})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if !strings.Contains(bundle.Text, "could not be carried here: docs/product/zz-huge.md") {
		t.Fatalf("the document that did not fit is not named:\n%s", bundle.Text)
	}
	if !strings.Contains(bundle.Text, "The extra document nobody reads today.") {
		t.Fatalf("a document that fits was dropped:\n%s", bundle.Text)
	}
	if bundle.Bytes > 16<<10 {
		t.Fatalf("bundle is %d bytes, over its budget", bundle.Bytes)
	}
}

// A specification home outside the repository is refused, as the conversations
// refuse it.
func TestASpecificationHomeOutsideTheRepositoryIsRefused(t *testing.T) {
	root := intentFixture(t)
	item := beads.WorkItem{ID: "yoyodyne-1", Title: "Task", Status: "in_progress"}
	_, err := Assemble(Request{RepositoryRoot: root, WorkItem: item, Specifications: "../elsewhere"})
	if err == nil || errors.Is(err, ErrNotAtRevision) {
		t.Fatalf("Assemble() error = %v, want the directory refused", err)
	}
}

func TestIntentBundleNamesRemovedAddedAndRenamedDocuments(t *testing.T) {
	for _, change := range []struct{ name, old, added string }{
		{name: "removed", old: "removed.md"},
		{name: "added", added: "added.md"},
		{name: "renamed", old: "old-name.md", added: "new-name.md"},
	} {
		t.Run(change.name, func(t *testing.T) {
			root := t.TempDir()
			base := map[string]string{"docs/product/brief.md": "# Brief\nBase intent.\n"}
			writeProductFile(t, root, "docs/product/brief.md", base["docs/product/brief.md"])
			if change.old != "" {
				base["docs/product/"+change.old] = "# Rule\nThe base rule must survive review.\n"
			}
			if change.added != "" {
				writeProductFile(t, root, "docs/product/"+change.added, "# Rule\nCandidate rule.\n")
			}
			revision := revisionOf("base commit abc123", base)
			bundle, err := Assemble(Request{RepositoryRoot: root, WorkItem: beads.WorkItem{ID: "task", Title: "Task"}, Specifications: "docs/product", Revision: revision})
			if err != nil {
				t.Fatal(err)
			}
			branch, err := AssembleIntent(root, "docs/product", revision)
			if err != nil {
				t.Fatal(err)
			}
			for _, text := range []string{bundle.Text, branch} {
				if change.old != "" {
					for _, want := range []string{"The base rule must survive review.", "Removed: docs/product/" + change.old, "## " + IntentHeading + ": docs/product/" + change.old} {
						if !strings.Contains(text, want) {
							t.Fatalf("missing %q in %s", want, text)
						}
					}
				}
				if change.added != "" && !strings.Contains(text, "Added: docs/product/"+change.added) {
					t.Fatalf("addition missing: %s", text)
				}
				if strings.Contains(text, "Candidate rule.") {
					t.Fatal("candidate contents treated as base intent")
				}
			}
		})
	}
}

func TestIntentRevisionRequiresACompleteListing(t *testing.T) {
	for _, revision := range []*Revision{
		{Name: "base", Read: revisionOf("base", nil).Read},
		{Name: "base", ListFiles: func() ([]string, error) { return nil, errors.New("listing incomplete") }},
	} {
		if _, err := AssembleIntent(t.TempDir(), "docs/product", revision); err == nil {
			t.Fatal("accepted a revision without a complete listing")
		}
	}
}
