package contextbundle

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// wellFormed is a specification that follows the contract: an introduction
// saying what the thing is and why it exists, then the goals that serve it.
const wellFormed = `# Bounded runs

Yoyodyne runs one bounded work item at a time, because a change nobody can
review is not a change anybody can trust.

## Goals

- A run integrates only behind checks and an independent review.
`

func TestAssembleProductReadsSpecificationsAndTrackerState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/runs.md", wellFormed)
	// The specifications directory is walked to any depth, so a specification
	// nested below it is evidence exactly as one sitting directly under it is.
	writeProductFile(t, root, "docs/product/goals/autonomy.md", "# Autonomy\n\nThe operator states intent and approves it.\n\n## Goals\n\n- Routine work needs no per-change gate.\n")
	// Only Markdown inside the configured directory is product evidence.
	writeProductFile(t, root, "docs/product/diagram.png", "not markdown")
	writeProductFile(t, root, "README.md", "# Yoyodyne\n\nWhat the product ships, described to whoever uses it.\n")
	writeProductFile(t, root, "docs/designs/v1-harness-design.md", "# Design\n\nArchitecture, not intent.\n")
	writeProductFile(t, root, "internal/notes.md", "implementation notes")

	bundle, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
		WorkItems: []beads.WorkItem{
			{ID: "yoyodyne-ifd.20", Title: "Work from a configurable specifications directory", Status: "in_progress", Priority: 1, IssueType: "task"},
		},
	})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	got := []string{}
	for _, reference := range bundle.References {
		got = append(got, reference.Path)
	}
	want := []string{"docs/product/goals/autonomy.md", "docs/product/runs.md"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("specifications = %v, want %v", got, want)
	}
	for _, required := range []string{
		"## Authoritative product intent: docs/product/runs.md",
		"a change nobody can\nreview is not a change anybody can trust",
		"Routine work needs no per-change gate.",
		"- yoyodyne-ifd.20 [in_progress, p1, task] Work from a configurable specifications directory",
	} {
		if !strings.Contains(bundle.Text, required) {
			t.Fatalf("product context is missing %q:\n%s", required, bundle.Text)
		}
	}
	// What is outside the specifications directory and is still not evidence: the
	// design document and the source say how the product is built rather than
	// what it is for or what it ships, and nothing that is not Markdown is read
	// at all.
	for _, excluded := range []string{"Architecture, not intent.", "implementation notes", "not markdown"} {
		if strings.Contains(bundle.Text, excluded) {
			t.Fatalf("product context read outside what it is given (%q):\n%s", excluded, bundle.Text)
		}
	}
	// The specifications are what the references are, whatever else the context
	// carries: a repository with a README and no specification has no recorded
	// intent, and a caller counting references is asking about intent.
	if len(bundle.References) != 2 {
		t.Fatalf("references = %d, want the 2 specifications alone", len(bundle.References))
	}
	if len(bundle.SpecificationProblems) != 0 {
		t.Fatalf("well-formed specifications reported problems: %v", bundle.SpecificationProblems)
	}
	if bundle.Bytes != len(bundle.Text) {
		t.Fatalf("bundle bytes = %d, text = %d", bundle.Bytes, len(bundle.Text))
	}
}

// The product manager sets the order work is pulled in, so the state it reasons
// over is listed in that order. A queue shown in some other order would have it
// reasoning about a sequence nobody works in.
func TestAssembleProductListsWorkInBacklogOrder(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/runs.md", wellFormed)

	bundle, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
		WorkItems: []beads.WorkItem{
			{ID: "yoyodyne-ifd.26", Title: "See and stop what is pulled", Status: "open", Priority: 3, IssueType: "task"},
			{ID: "yoyodyne-ifd.3", Title: "The scheduler that runs it", Status: "open", Priority: 0, IssueType: "task"},
			{ID: "yoyodyne-ifd.4", Title: "The development manager that pulls", Status: "open", Priority: 1, IssueType: "task"},
		},
	})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}

	positions := make([]int, 0, 3)
	for _, id := range []string{"yoyodyne-ifd.3", "yoyodyne-ifd.4", "yoyodyne-ifd.26"} {
		at := strings.Index(bundle.Text, "- "+id+" ")
		if at < 0 {
			t.Fatalf("product context is missing %s:\n%s", id, bundle.Text)
		}
		positions = append(positions, at)
	}
	if positions[0] > positions[1] || positions[1] > positions[2] {
		t.Fatalf("work items are not in backlog order (positions %v):\n%s", positions, bundle.Text)
	}
	// What the order means is stated, including the part of it nobody decided.
	for _, required := range []string{
		"These are in backlog order: highest priority first",
		"nothing has decided which of those comes first",
	} {
		if !strings.Contains(bundle.Text, required) {
			t.Fatalf("product context does not say %q:\n%s", required, bundle.Text)
		}
	}
}

func TestAssembleProductReadsTheConfiguredDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "specs/runs.md", wellFormed)
	writeProductFile(t, root, "docs/product/other.md", "# Other\n\nElsewhere.\n\n## Goals\n\n- Not this one.\n")

	bundle, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "specs"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if len(bundle.References) != 1 || bundle.References[0].Path != "specs/runs.md" {
		t.Fatalf("specifications = %v, want specs/runs.md alone", bundle.References)
	}
	if !strings.Contains(bundle.Text, "specifications under specs") {
		t.Fatalf("the configured directory is not named in the context:\n%s", bundle.Text)
	}
}

func TestAssembleProductSurfacesSpecificationsThatIgnoreTheStructure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/no-goals.md", "# Brief\n\nWhat this is and why it exists.\n")
	writeProductFile(t, root, "docs/product/no-introduction.md", "# Goals\n\n- An outcome with nothing saying what it serves.\n")
	writeProductFile(t, root, "docs/product/empty-goals.md", "# Brief\n\nWhat this is.\n\n## Goals\n\n## Something else\n\nProse.\n")
	writeProductFile(t, root, "docs/product/good.md", wellFormed)

	bundle, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if len(bundle.References) != 4 {
		t.Fatalf("references = %d, want 4: a specification that ignores the structure is still read", len(bundle.References))
	}
	// The intent survives: refusing to load these would lose what somebody wrote.
	for _, required := range []string{
		"An outcome with nothing saying what it serves.",
		"## Specifications that do not follow the required structure",
		"docs/product/no-goals.md: it states no goals",
		"docs/product/no-introduction.md: it opens with its goals",
		"docs/product/empty-goals.md: its `Goals` section is empty",
	} {
		if !strings.Contains(bundle.Text, required) {
			t.Fatalf("product context is missing %q:\n%s", required, bundle.Text)
		}
	}
	if strings.Contains(bundle.Text, "docs/product/good.md:") {
		t.Fatalf("a well-formed specification was reported as a problem:\n%s", bundle.Text)
	}

	reported := map[string]string{}
	for _, problem := range bundle.SpecificationProblems {
		reported[problem.Path] = problem.Reason
	}
	if len(reported) != 3 {
		t.Fatalf("reported problems = %v, want three", reported)
	}
	for _, path := range []string{"docs/product/no-goals.md", "docs/product/no-introduction.md", "docs/product/empty-goals.md"} {
		if reported[path] == "" {
			t.Fatalf("%s is not reported to the caller: %v", path, reported)
		}
	}
}

// A directory index is not a specification and is not held to a specification's
// shape. Both readings of one would be reported forever and neither would be
// actionable: `docs/product/goals/README.md` was reported for opening with its
// goals because its title read "Goals directory", and the same file rewritten to
// say what is filed there would be reported for stating none. The index is still
// carried into the context, because it says what is filed beside it.
func TestADirectoryIndexIsNotHeldToTheSpecificationShape(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/good.md", wellFormed)
	writeProductFile(t, root, "docs/product/goals/README.md", "# Goals directory\n\nWhat is filed here, and whose it is.\n")
	writeProductFile(t, root, "docs/product/README.md", "# docs/product\n\n**Purpose.** The brief and the goals that serve it.\n")

	bundle, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if len(bundle.SpecificationProblems) != 0 {
		t.Fatalf("problems = %v, want an index held to no shape at all", bundle.SpecificationProblems)
	}
	if strings.Contains(bundle.Text, "## Specifications that do not follow the required structure") {
		t.Fatalf("an index was reported as a specification that ignores the structure:\n%s", bundle.Text)
	}
	// Carried, and carried as what it is: an index arrives under its own heading
	// so the reader is not left to work out that it states nothing.
	if !strings.Contains(bundle.Text, "## Directory index: docs/product/goals/README.md") {
		t.Fatalf("the index was dropped rather than carried under its own heading:\n%s", bundle.Text)
	}
	if strings.Contains(bundle.Text, "## Authoritative product intent: docs/product/README.md") {
		t.Fatalf("an index was carried as a specification:\n%s", bundle.Text)
	}
	if bundle.SpecificationsIncluded != 1 || len(bundle.References) != 3 {
		t.Fatalf("specifications = %d, references = %d: only good.md states anything",
			bundle.SpecificationsIncluded, len(bundle.References))
	}
}

// A non-goals document states what the product will not do under a `Non-goals`
// heading and states no goals, and the artifact contract says it is not
// malformed for that. Held to the specification's shape it was reported in
// every product-manager conversation for stating no goals, and the only way to
// clear the report was to rewrite it to say something it does not mean. It is
// held to its own shape instead, so one that states no non-goals is still
// reported; and a document that should state goals and does not is reported
// exactly as before.
func TestANonGoalsDocumentIsHeldToTheNonGoalsShape(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/good.md", wellFormed)
	// The two documents this repository was reported for, in the shape they are
	// actually written: an index, and non-goals under their own heading.
	writeProductFile(t, root, "docs/product/goals/README.md", "# docs/product/goals\n\n**Purpose.** The goals and the non-goals that bound them.\n")
	writeProductFile(t, root, "docs/product/goals/v1-non-goals.md", "---\nid: v1-non-goals\nkind: non-goals\n---\n\n# V1 non-goals\n\nWhat the first version deliberately does not do.\n\n## Non-goals\n\n- A hosted control plane.\n")
	// Named non-goals with no frontmatter: read from the name, as the goals are.
	writeProductFile(t, root, "docs/product/goals/v2-nongoals.md", "# V2 non-goals\n\nWhat the second version does not do.\n\n## Non-goals\n\n- Remote execution.\n")
	// Non-goals that state none, and goals that state none: both still reported.
	writeProductFile(t, root, "docs/product/goals/empty-non-goals.md", "---\nid: empty-non-goals\nkind: non-goals\n---\n\n# Empty non-goals\n\nAn introduction and nothing bounded.\n")
	writeProductFile(t, root, "docs/product/goals/v3-goals.md", "---\nid: v3-goals\nkind: goals\n---\n\n# V3 goals\n\nAn introduction and no goals.\n")

	bundle, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	reported := map[string]string{}
	for _, problem := range bundle.SpecificationProblems {
		reported[problem.Path] = problem.Reason
	}
	for _, correct := range []string{"docs/product/goals/README.md", "docs/product/goals/v1-non-goals.md", "docs/product/goals/v2-nongoals.md", "docs/product/good.md"} {
		if reason, ok := reported[correct]; ok {
			t.Fatalf("%s is written correctly and was reported: %s", correct, reason)
		}
	}
	if reason := reported["docs/product/goals/empty-non-goals.md"]; !strings.Contains(reason, "it states no non-goals") {
		t.Fatalf("a non-goals document stating none is reported as %q, want it states no non-goals", reason)
	}
	if reason := reported["docs/product/goals/v3-goals.md"]; !strings.Contains(reason, "it states no goals") {
		t.Fatalf("a goals document stating none is reported as %q, want it states no goals", reason)
	}
	if len(reported) != 2 {
		t.Fatalf("reported problems = %v, want exactly the two documents stating nothing", reported)
	}
	// Still carried as a specification, and still not counted as the goals: the
	// shape it is held to changes nothing about what it is read as.
	if !strings.Contains(bundle.Text, "## Authoritative product intent: docs/product/goals/v1-non-goals.md") {
		t.Fatalf("the non-goals document was not carried as a specification:\n%s", bundle.Text)
	}
}

// The ordinary starting case, after `yoyo init`. It writes an index into the
// specifications directory and another into the goals directory beneath it, so
// the directory a repository with no intent has is no longer empty. What must
// not change is what the product manager is told: that intent is not written
// down, which is what makes the first conversation on a fresh repository ask
// what the product is for rather than infer it from two files about filing.
func TestADirectoryHoldingOnlyIndexesRecordsNoProductIntent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/README.md",
		"# docs/product\n\n**Purpose.** The brief and the goals that serve it.\n\n**Owner.** The product manager.\n\n**Editing by hand.** You may.\n")
	writeProductFile(t, root, "docs/product/goals/README.md",
		"# docs/product/goals\n\n**Purpose.** The goals derived from the brief.\n\n**Owner.** The product manager.\n\n**Editing by hand.** You may.\n")

	bundle, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if bundle.SpecificationsIncluded != 0 {
		t.Fatalf("specifications = %d, want none: an index states no intent", bundle.SpecificationsIncluded)
	}
	for _, required := range []string{
		"No specification was found under docs/product.",
		"Say that product intent is not written down",
		"- Brief: none recorded.",
		"- Goals: none recorded.",
	} {
		if !strings.Contains(bundle.Text, required) {
			t.Fatalf("product context is missing %q:\n%s", required, bundle.Text)
		}
	}
	// The indexes are still there, and still not called specifications, so the
	// context does not say in one section that none was found and in the next
	// that here are two.
	if len(bundle.References) != 2 {
		t.Fatalf("references = %d, want both indexes carried", len(bundle.References))
	}
	if strings.Contains(bundle.Text, "## Authoritative product intent: ") {
		t.Fatalf("an index was carried as a specification alongside a report that none was found:\n%s", bundle.Text)
	}
	if len(bundle.SpecificationProblems) != 0 {
		t.Fatalf("problems = %v, want an index held to no shape", bundle.SpecificationProblems)
	}
	if bundle.Bytes != len(bundle.Text) {
		t.Fatalf("bundle bytes = %d, text = %d", bundle.Bytes, len(bundle.Text))
	}
}

// The same thing one level down: `yoyo init` writes an index into the designs
// home too, and a home holding only that has still recorded no designs. The
// architect has to be told to treat them as unwritten rather than as something
// it has read, which is what that note exists for.
func TestARoleDocumentHomeHoldingOnlyItsIndexIsStillUnwritten(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/runs.md", wellFormed)
	writeProductFile(t, root, "docs/designs/README.md",
		"# docs/designs\n\n**Purpose.** How what the goals ask for gets built.\n\n**Owner.** The architect.\n\n**Editing by hand.** You may.\n")

	bundle, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
		RoleDocuments:           []DocumentSet{{Label: "Design", Directory: "docs/designs"}},
	})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(bundle.Text, "Nothing was found under docs/designs.") {
		t.Fatalf("a designs home holding only its index reads as written:\n%s", bundle.Text)
	}
	if strings.Contains(bundle.Text, "Your own documents are here too") {
		t.Fatalf("the architect is told it has read designs that were never written:\n%s", bundle.Text)
	}
	// The index is still carried, and still not called a design.
	if !strings.Contains(bundle.Text, "## Directory index: docs/designs/README.md") {
		t.Fatalf("the index was dropped rather than carried under its own heading:\n%s", bundle.Text)
	}
	if strings.Contains(bundle.Text, "## Design: docs/designs/README.md") {
		t.Fatalf("an index was carried as a design:\n%s", bundle.Text)
	}
}

func TestSpecificationStructureProblem(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		problem bool
	}{
		{name: "introduction then goals", content: wellFormed},
		{name: "no title heading", content: "What this is.\n\n## Goals\n\n- An outcome.\n"},
		{name: "singular goal heading", content: "# Brief\n\nWhat this is.\n\n## Goal\n\n- An outcome.\n"},
		{name: "goals with subsections", content: "# Brief\n\nWhat this is.\n\n## Goals\n\n### First\n\nAn outcome.\n"},
		{name: "goals in a lower-cased heading", content: "# Brief\n\nWhat this is.\n\n### goals\n\n- An outcome.\n"},
		{name: "empty file", content: "", problem: true},
		{name: "blank file", content: "\n\n   \n", problem: true},
		{name: "no goals at all", content: "# Brief\n\nWhat this is.\n", problem: true},
		{name: "goals before any introduction", content: "# Goals\n\n- An outcome.\n", problem: true},
		// A fenced block is not an introductory paragraph saying what the thing
		// is, so a specification whose only prose is a code sample still opens
		// with its goals.
		{name: "fenced block is not an introduction", content: "# Brief\n\n```\nyoyo run\n```\n\n## Goals\n\n- An outcome.\n", problem: true},
		// A heading inside a fence is not the goals heading either.
		{name: "goals heading inside a fence", content: "# Brief\n\nWhat this is.\n\n```\n## Goals\n```\n", problem: true},
		// Artifact identity metadata is not part of the document a person reads,
		// so the structure contract is checked over what follows it.
		{name: "identity frontmatter then the document", content: "---\nid: brief\nkind: brief\n---\n\n# Brief\n\nWhat this is.\n\n## Goals\n\n- An outcome.\n"},
		{name: "identity frontmatter is not an introduction", content: "---\nid: brief\nkind: brief\n---\n\n# Goals\n\n- An outcome.\n", problem: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			reason := specificationStructureProblem(testCase.content)
			if testCase.problem && reason == "" {
				t.Fatalf("specificationStructureProblem() = %q, want a problem", reason)
			}
			if !testCase.problem && reason != "" {
				t.Fatalf("specificationStructureProblem() = %q, want no problem", reason)
			}
		})
	}
}

func TestNonGoalsStructureProblem(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		problem string
	}{
		{name: "introduction then non-goals", content: "# V1 non-goals\n\nWhat v1 does not do.\n\n## Non-goals\n\n- A hosted control plane.\n"},
		{name: "non-goals heading in either spelling", content: "# What v1 leaves out\n\nWhat v1 does not do.\n\n## Nongoals\n\n- A hosted control plane.\n"},
		{name: "non-goals heading with a space", content: "# What v1 leaves out\n\nWhat v1 does not do.\n\n### Non Goals\n\n- A hosted control plane.\n"},
		{name: "non-goals with subsections", content: "# V1 non-goals\n\nWhat v1 does not do.\n\n## Non-goals\n\n### Hosting\n\nNot hosted.\n"},
		{name: "identity frontmatter then the document", content: "---\nid: v1-non-goals\nkind: non-goals\n---\n\n# V1 non-goals\n\nWhat v1 does not do.\n\n## Non-goals\n\n- A hosted control plane.\n"},
		{name: "empty file", content: "", problem: "the file is empty"},
		// A `Goals` heading is not where a non-goals document states its content,
		// so this document has stated none.
		{name: "goals heading only", content: "# V1 non-goals\n\nWhat v1 does not do.\n\n## Goals\n\n- Nothing here.\n", problem: "it states no non-goals; a non-goals document names its non-goals under a `Non-goals` heading"},
		{name: "no non-goals at all", content: "# V1 non-goals\n\nWhat v1 does not do.\n", problem: "it states no non-goals"},
		{name: "non-goals before any introduction", content: "# Non-goals\n\n- A hosted control plane.\n", problem: "it opens with its non-goals; a non-goals document opens with an introduction saying what the thing is and why it exists"},
		{name: "empty non-goals section", content: "# V1 non-goals\n\nWhat v1 does not do.\n\n## Non-goals\n\n## Something else\n\nProse.\n", problem: "its `Non-goals` section is empty; the non-goals that bound the goals are missing"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			reason := nonGoalsStructureProblem(testCase.content)
			if testCase.problem == "" && reason != "" {
				t.Fatalf("nonGoalsStructureProblem() = %q, want no problem", reason)
			}
			if !strings.HasPrefix(reason, testCase.problem) {
				t.Fatalf("nonGoalsStructureProblem() = %q, want %q", reason, testCase.problem)
			}
		})
	}
}

// Which shape a document is held to is decided the way its intent kind is:
// by the kind it records, and failing that by its name. A document recording
// some other kind is a specification whatever it is called.
func TestDocumentStructureProblemChoosesTheShapeByKindThenName(t *testing.T) {
	t.Parallel()

	nonGoals := "# Bounds\n\nWhat this does not do.\n\n## Non-goals\n\n- Hosting.\n"
	goals := "# Brief\n\nWhat this is.\n\n## Goals\n\n- An outcome.\n"
	rules := "# Operating rules\n\nWhat every role applies.\n\n## Rules\n\n- A rule.\n"
	cases := []struct {
		name    string
		path    string
		content string
		problem bool
	}{
		{name: "frontmatter non-goals under any name", path: "docs/product/bounds.md", content: "---\nid: bounds\nkind: non-goals\n---\n\n" + nonGoals},
		{name: "named non-goals without frontmatter", path: "docs/product/goals/v1-non-goals.md", content: nonGoals},
		{name: "named nongoals without frontmatter", path: "docs/product/goals/v1-nongoals.md", content: nonGoals},
		{name: "frontmatter goals named non-goals", path: "docs/product/goals/v1-non-goals.md", content: "---\nid: v1-non-goals\nkind: goals\n---\n\n" + goals},
		{name: "a specification stating non-goals only", path: "docs/product/brief.md", content: nonGoals, problem: true},
		{name: "frontmatter goals stating non-goals only", path: "docs/product/goals/v1-non-goals.md", content: "---\nid: v1-non-goals\nkind: goals\n---\n\n" + nonGoals, problem: true},
		{name: "a non-goals document stating goals only", path: "docs/product/goals/v1-non-goals.md", content: goals, problem: true},
		{name: "frontmatter rules stating rules", path: "docs/product/operating-rules.md", content: "---\nid: operating-rules\nkind: rules\n---\n\n" + rules},
		{name: "frontmatter rules stating goals only", path: "docs/product/operating-rules.md", content: "---\nid: operating-rules\nkind: rules\n---\n\n" + goals, problem: true},
		{name: "rules without frontmatter are a specification", path: "docs/product/operating-rules.md", content: rules, problem: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			reason := documentStructureProblem(testCase.path, testCase.content)
			if testCase.problem && reason == "" {
				t.Fatalf("documentStructureProblem() = %q, want a problem", reason)
			}
			if !testCase.problem && reason != "" {
				t.Fatalf("documentStructureProblem() = %q, want no problem", reason)
			}
		})
	}
}

func TestAssembleProductStatesWhenThereAreNoSpecifications(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "README.md", "# Yoyodyne\n")

	// A directory that does not exist yet is a project that has written nothing
	// down, not a broken configuration.
	missing, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(missing.Text, "No specification was found under docs/product.") {
		t.Fatalf("an empty specifications directory is not reported:\n%s", missing.Text)
	}
	if len(missing.References) != 0 {
		t.Fatalf("references = %v, want none", missing.References)
	}

	writeProductFile(t, root, "docs/product/notes.txt", "not markdown")
	empty, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(empty.Text, "No specification was found under docs/product.") {
		t.Fatalf("a directory with no Markdown is not reported:\n%s", empty.Text)
	}
}

// A repository that has written its intent down is told so plainly, so a
// product manager reading a substantive brief and goals has no reason to open by
// asking for either.
func TestAssembleProductSaysWhatIntentIsRecorded(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/brief.md", "---\nid: brief\nkind: brief\n---\n\n# Product brief\n\n"+strings.Repeat("What this is, who it is for, and what finished means. ", 12)+"\n\n## Goals\n\n- An outcome.\n")
	writeProductFile(t, root, "docs/product/goals/v1-goals.md", "---\nid: v1-goals\nkind: goals\n---\n\n# V1 goals\n\n"+strings.Repeat("The outcomes the first version reaches. ", 12)+"\n\n## Goals\n\n- An outcome.\n")

	bundle, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	for _, required := range []string{
		"## Recorded product intent",
		"- Brief: docs/product/brief.md, about ",
		"- Goals: docs/product/goals/v1-goals.md, about ",
	} {
		if !strings.Contains(bundle.Text, required) {
			t.Fatalf("recorded intent is missing %q:\n%s", required, bundle.Text)
		}
	}
	// Neither document is thin, so nothing here reads as a repository waiting to
	// be asked what it is for.
	for _, absent := range []string{"none recorded", "little more than a placeholder"} {
		if strings.Contains(bundle.Text, absent) {
			t.Fatalf("recorded intent says %q about substantive documents:\n%s", absent, bundle.Text)
		}
	}
}

// The blind repository: nothing written down, and the emptiness said in so many
// words rather than left to be inferred from a context with no brief in it.
func TestAssembleProductSaysWhenIntentIsNotRecorded(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	bundle, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	for _, required := range []string{
		"- Brief: none recorded.",
		"- Goals: none recorded.",
		"the operator's to state",
	} {
		if !strings.Contains(bundle.Text, required) {
			t.Fatalf("an unwritten product is not reported: missing %q:\n%s", required, bundle.Text)
		}
	}

	// A specification that is neither of the two documents leaves both missing:
	// what the product will not do is not what it is for.
	writeProductFile(t, root, "docs/product/goals/v1-non-goals.md", "---\nid: v1-non-goals\nkind: non-goals\n---\n\n# V1 non-goals\n\nWhat the first version does not do.\n\n## Goals\n\n- Nothing here.\n")
	neither, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(neither.Text, "- Goals: none recorded.") {
		t.Fatalf("a non-goals document is counted as the goals:\n%s", neither.Text)
	}
}

// Goals stated inside the brief are goals. The structure contract already asks
// every specification to state them under a `Goals` heading, so a project that
// wrote them there has written them, and asking for goals that are on disk would
// be a question with its answer already in the context.
func TestAssembleProductCountsGoalsStatedInsideTheBrief(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/brief.md", "# Product brief\n\n"+strings.Repeat("What this is and who it is for. ", 10)+"\n\n## Goals\n\n"+strings.Repeat("- An outcome the product reaches. ", 10)+"\n")

	stated, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(stated.Text, "- Goals: the `Goals` section of docs/product/brief.md, about ") {
		t.Fatalf("goals stated inside the brief are not counted:\n%s", stated.Text)
	}

	// A goals document is where goals live once there is one, and the brief's own
	// section is not named beside it: that would report one intent twice.
	writeProductFile(t, root, "docs/product/goals/v1-goals.md", "# V1 goals\n\n"+strings.Repeat("The outcomes the first version reaches. ", 10)+"\n\n## Goals\n\n- An outcome.\n")
	both, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(both.Text, "- Goals: docs/product/goals/v1-goals.md, about ") {
		t.Fatalf("the goals document is not what the goals are read from:\n%s", both.Text)
	}
	if strings.Contains(both.Text, "the `Goals` section of") {
		t.Fatalf("the brief's own section is named beside a goals document:\n%s", both.Text)
	}

	// A heading with nothing under it states no goals, which is the case the
	// structure contract reports as an empty goals section.
	empty := t.TempDir()
	writeProductFile(t, empty, "docs/product/brief.md", "# Product brief\n\nWhat this is and who it is for.\n\n## Goals\n\n## Something else\n\nProse.\n")
	heading, err := AssembleProduct(ProductRequest{RepositoryRoot: empty, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(heading.Text, "- Goals: none recorded.") {
		t.Fatalf("an empty goals heading is counted as goals:\n%s", heading.Text)
	}
}

// A document that exists and says almost nothing is reported as what it is,
// with the count beside it: whether a short document is enough is a judgment,
// and one made from a verdict alone is not one.
func TestAssembleProductSaysWhenIntentIsAPlaceholder(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/brief.md", "# Product brief\n\nTODO.\n\n## Goals\n\n- TODO.\n")

	bundle, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(bundle.Text, "- Brief: docs/product/brief.md, about 3 words and little more than a placeholder.") {
		t.Fatalf("a placeholder brief is not reported as one:\n%s", bundle.Text)
	}
}

// The recorded-intent section is charged to the context as a fixed allowance
// before any specification is read, not as what it goes on to render, so what it
// renders has to fit inside that allowance however the repository files its
// intent. A section that outgrew the reserve would push the assembled context
// past the byte budget it was assembled to, with nothing to catch it.
//
// This renders the section at its largest, and every part of "largest" is what
// the bounds actually permit rather than a number chosen to pass: each entry is
// a placeholder, which is the longest form an entry takes, and states its goals
// inside the brief, which is the longest way one is named; every path is longer
// than the fold allows; and the tail counts documents that were not named.
func TestRecordedIntentFitsWhatIsReservedForIt(t *testing.T) {
	t.Parallel()

	// The allowance is sized against the fold, so what the fold actually returns
	// is pinned here rather than assumed.
	longPath := strings.Repeat("nested/", 40) + "v1-goals.md"
	folded := singleLine(longPath, maxIntentPathBytes)
	if len(folded) > maxIntentPathBytes+len("...") {
		t.Fatalf("singleLine folds %d bytes to %d, want at most %d", len(longPath), len(folded), maxIntentPathBytes+len("..."))
	}

	// Below stubProseWords, so every entry carries the placeholder clause, and at
	// the widest count that stays below it.
	widest := intentDocument{path: longPath, words: stubProseWords - 1, inline: true}
	// More documents than are named, so the tail that says how many were left out
	// is rendered at more digits than a repository could reach: every included
	// specification costs the context far more than one byte, so the default
	// budget cannot hold this many of them.
	total := maxRecordedIntentDocuments + MaxProductBytes
	worst := recordedIntent{brief: make([]intentDocument, total), goals: make([]intentDocument, total)}
	for index := range maxRecordedIntentDocuments {
		worst.brief[index] = widest
		worst.goals[index] = widest
	}

	// The configured directory is added to the allowance rather than counted
	// against it, so it is left out of what is measured here.
	rendered := len(renderRecordedIntent("", worst))
	if rendered+recordedIntentHeadroom > maxRecordedIntentBytes {
		t.Fatalf("recorded intent renders %d bytes with %d bytes of headroom required, allowance is %d",
			rendered, recordedIntentHeadroom, maxRecordedIntentBytes)
	}
}

// What a placeholder entry costs is the largest an entry gets, which is what the
// worst case above is built from. A word count large enough to need more digits
// than a placeholder's does not overtake it, because the clause a placeholder
// adds is worth far more than those digits.
func TestPlaceholderEntriesAreTheLongestOnesRendered(t *testing.T) {
	t.Parallel()

	path := "docs/product/goals/v1-goals.md"
	placeholder := renderIntentDocuments([]intentDocument{{path: path, words: stubProseWords - 1, inline: true}})
	counted := renderIntentDocuments([]intentDocument{{path: path, words: MaxProductBytes, inline: true}})
	if len(placeholder) <= len(counted) {
		t.Fatalf("a placeholder entry renders %d bytes and a counted one %d; the worst case is built from the wrong one",
			len(placeholder), len(counted))
	}
}

func TestIntentKind(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		path    string
		content string
		kind    string
	}{
		{name: "frontmatter brief", path: "docs/product/anything.md", content: "---\nid: anything\nkind: brief\n---\n\n# Brief\n", kind: kindBrief},
		{name: "frontmatter goals", path: "docs/product/anything.md", content: "---\nid: anything\nkind: goals\n---\n\n# Goals\n", kind: kindGoals},
		// A document's own word on what it is beats where it is filed.
		{name: "frontmatter non-goals in a goals directory", path: "docs/product/goals/v1-non-goals.md", content: "---\nid: v1-non-goals\nkind: non-goals\n---\n\n# Non-goals\n", kind: ""},
		{name: "frontmatter design", path: "docs/product/brief.md", content: "---\nid: brief\nkind: design\n---\n\n# A design\n", kind: ""},
		// A key inside the metadata is not the document's own kind.
		{name: "nested kind key", path: "docs/product/notes.md", content: "---\nid: notes\nrevisions:\n    - kind: brief\n---\n\n# Notes\n", kind: ""},
		// Nothing has been asked of the operator about identity yet, so a document
		// with no frontmatter is read by what it is called.
		{name: "named brief", path: "docs/product/brief.md", content: "# Brief\n", kind: kindBrief},
		{name: "named goals", path: "docs/product/v1-goals.md", content: "# Goals\n", kind: kindGoals},
		{name: "filed under goals", path: "docs/product/goals/v1.md", content: "# V1\n", kind: kindGoals},
		{name: "named non-goals", path: "docs/product/goals/v1-non-goals.md", content: "# Non-goals\n", kind: ""},
		{name: "a directory index states no intent", path: "docs/product/goals/README.md", content: "# Goals directory\n", kind: ""},
		{name: "an ordinary specification", path: "docs/product/runs.md", content: wellFormed, kind: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if kind := intentKind(testCase.path, testCase.content); kind != testCase.kind {
				t.Fatalf("intentKind(%q) = %q, want %q", testCase.path, kind, testCase.kind)
			}
		})
	}
}

func TestProseWords(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		words   int
	}{
		{name: "prose", content: "One two three four.\n", words: 4},
		// Identity metadata, a title, and a heading for every unanswered question
		// are not a written document.
		{name: "frontmatter and headings only", content: "---\nid: brief\nkind: brief\n---\n\n# Product brief\n\n## Goals\n", words: 0},
		{name: "a fenced block is not prose", content: "# Brief\n\n```\nyoyo run --now\n```\n\nTwo words.\n", words: 2},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if words := proseWords(testCase.content); words != testCase.words {
				t.Fatalf("proseWords() = %d, want %d", words, testCase.words)
			}
		})
	}
}

// What the product ships is carried so the role deciding what to build next can
// name the surfaces that already exist. It is labeled as description, because
// the same documentation read as authority is what let a stale README sentence
// reach the operator as current product fact on 2026-08-16.
func TestAssembleProductDescribesWhatIsShipped(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/brief.md", wellFormed)
	writeProductFile(t, root, "README.md", "# Yoyodyne\n\n`bin/yoyo-status` follows a run as it happens.\n")
	writeProductFile(t, root, "docs/configuration.md", "# Configuration\n\nA project owns its configuration outright.\n")

	bundle, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
		ShippedDocumentation:    []string{"README.md", "docs/configuration.md"},
		CommandHelp:             "Usage: yoyo <command>\n\n  cost  price work items from the runs made for them\n",
	})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	for _, required := range []string{
		"## What the product ships today",
		"### Shipped documentation: README.md",
		"`bin/yoyo-status` follows a run as it happens.",
		"### Shipped documentation: docs/configuration.md",
		"A project owns its configuration outright.",
		"### Command help",
		"cost  price work items from the runs made for them",
	} {
		if !strings.Contains(bundle.Text, required) {
			t.Fatalf("the shipped surface is missing %q:\n%s", required, bundle.Text)
		}
	}
	// The label is as much the point as the content: what it is, what it is not,
	// and what to do when it disagrees with the specifications.
	for _, required := range []string{
		"It describes the implementation as built.",
		"It is not authority about intent",
		"the specifications above remain\nthe only statement of that",
		"report the conflict",
	} {
		if !strings.Contains(bundle.Text, required) {
			t.Fatalf("the shipped surface is not labeled as description (%q):\n%s", required, bundle.Text)
		}
	}
	// Intent is stated before what was built from it, so the section that decides
	// nothing cannot be read first and become the frame for the one that does.
	if strings.Index(bundle.Text, "## Authoritative product intent: docs/product/brief.md") > strings.Index(bundle.Text, "## What the product ships today") {
		t.Fatalf("the shipped surface is rendered before the specifications:\n%s", bundle.Text)
	}
}

// A project ships whatever documentation it wrote. A repository holding none of
// what is looked for is told so, rather than getting a section that quietly
// carries less than it says it does.
func TestAssembleProductStatesWhenNothingDescribesWhatIsShipped(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/brief.md", wellFormed)

	named := []string{"README.md", "docs/configuration.md"}
	bundle, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product", ShippedDocumentation: named})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(bundle.Text, "This repository holds none of the operator-facing documentation looked for here") {
		t.Fatalf("missing documentation is not stated:\n%s", bundle.Text)
	}
	// One document present and the other missing is neither of those cases: what
	// is there is carried, and nothing claims the rest was found.
	writeProductFile(t, root, "README.md", "# Yoyodyne\n\nWhat it does.\n")
	partial, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product", ShippedDocumentation: named})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(partial.Text, "### Shipped documentation: README.md") {
		t.Fatalf("the documentation that is there was not read:\n%s", partial.Text)
	}
	if strings.Contains(partial.Text, "docs/configuration.md") {
		t.Fatalf("documentation that is not there is named as read:\n%s", partial.Text)
	}
}

// Intent wins the budget over description, by construction rather than by the
// order the sections happen to be written in: the specifications are read first
// and the documentation takes what is left, so a repository too large for both
// keeps the half that is authoritative.
func TestAssembleProductSpendsTheBudgetOnIntentFirst(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/brief.md", wellFormed)
	writeProductFile(t, root, "README.md", "# Yoyodyne\n\n"+strings.Repeat("described ", 512))

	bundle, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
		ShippedDocumentation:    []string{"README.md"},
		MaxBytes:                6144,
	})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(bundle.Text, "# Bounded runs") {
		t.Fatalf("a specification was dropped for documentation:\n%s", bundle.Text)
	}
	if !strings.Contains(bundle.Text, "This documentation did not fit and is not included above:\n\n- README.md") {
		t.Fatalf("documentation that did not fit is not named:\n%s", bundle.Text)
	}
	if bundle.Bytes > 6144 {
		t.Fatalf("bundle is %d bytes, over the %d it was assembled to", bundle.Bytes, 6144)
	}
}

// The note is written after the budget has been spent, so what it can cost is
// charged before it. Every path it can name is one of a fixed set, so the
// reserve is exact rather than an allowance -- but only while it stays that way.
func TestShippedDocumentationNoteFitsWhatIsReservedForIt(t *testing.T) {
	t.Parallel()

	shipped := HarnessShippedDocumentation
	reserved := longestShippedDocumentationNote(shipped)
	// The size line and the standing grow with the figure, so each shape is
	// rendered at every standing the set can have: with room to spare, inside
	// the margin, and at the ceiling.
	var notes []string
	for _, bytes := range []int{1, ShippedDocumentationCeiling - ShippedDocumentationMargin + 1, ShippedDocumentationCeiling, 9 * ShippedDocumentationCeiling} {
		notes = append(notes,
			renderShippedDocumentationNote(shipped, shippedDocumentation{omitted: shipped, bytes: bytes, found: len(shipped)}),
			renderShippedDocumentationNote(shipped, shippedDocumentation{rendered: "### Shipped documentation: README.md", omitted: shipped[1:], bytes: bytes, found: len(shipped)}),
			renderShippedDocumentationNote(shipped, shippedDocumentation{rendered: "### Shipped documentation: README.md", bytes: bytes, found: len(shipped)}),
		)
	}
	for _, note := range append([]string{noShippedDocumentation, noShippedDocumentationNamed}, notes...) {
		if len(note) > reserved {
			t.Fatalf("a note renders %d bytes against a reserve of %d:\n%s", len(note), reserved, note)
		}
	}
}

// Every document the shipped set names has to actually reach the context, or the
// product manager is told the product ships something it was never shown. The
// set grew from two entries to eight when the README was reduced to a landing
// page and the content it used to carry moved into the documents it links to, so
// what was one large document is now seven -- and a bound that quietly dropped
// the tail of that list would look exactly like a product that never had those
// surfaces, which is the ifd.20 narrowing arrived at by accident.
func TestEveryShippedDocumentReachesTheContext(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/brief.md", wellFormed)
	for index, documentPath := range HarnessShippedDocumentation {
		writeProductFile(t, root, documentPath, fmt.Sprintf("# Document %d\n\nSurface %d is described here.\n", index, index))
	}

	bundle, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
		ShippedDocumentation:    HarnessShippedDocumentation,
	})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	for index, documentPath := range HarnessShippedDocumentation {
		if !strings.Contains(bundle.Text, "### Shipped documentation: "+documentPath) {
			t.Errorf("%s is named in the shipped set and has no section in the context", documentPath)
		}
		if !strings.Contains(bundle.Text, fmt.Sprintf("Surface %d is described here.", index)) {
			t.Errorf("%s has a section, but its content did not reach the context", documentPath)
		}
	}
	// Documentation read and then dropped for room is named rather than silently
	// absent, so at the default budget that list has to be empty too.
	if strings.Contains(bundle.Text, "This documentation did not fit and is not included above:") {
		t.Errorf("documentation was dropped for room at the default budget:\n%s", bundle.Text)
	}
}

// The shipped set is a hardcoded list of paths, so a rename or a typo in it is a
// document the product manager silently stops being given -- the same narrowing
// again, reached by a spelling mistake rather than by a decision. The fixture
// above cannot catch that, because it writes its fixtures from the same list, so
// this checks the list against the repository it is a description of.
func TestShippedDocumentationNamesDocumentsThisRepositoryHas(t *testing.T) {
	t.Parallel()

	total := 0
	for _, documentPath := range HarnessShippedDocumentation {
		information, err := os.Stat(filepath.Join("../..", documentPath))
		if err != nil {
			t.Errorf("the shipped set names %s, which this repository does not have: %v", documentPath, err)
			continue
		}
		total += int(information.Size())
	}
	// The set is measured against a ceiling with a margin under it, and only
	// the ceiling fails. A budget the set was always within bytes of made every
	// documentation edit a red gate unrelated to the change — four raises in
	// three weeks, and a report per run asking the same product question — so
	// the question is asked once, in the constant's comment, and the gate warns
	// for the length of the margin before it fails. The size is logged either
	// way: what yoyodyne-ifd.117.4's reduction buys is only visible as a number.
	t.Logf("shipped documentation is %d bytes across %d documents; the ceiling is %d bytes and the warning starts %d bytes under it",
		total, len(HarnessShippedDocumentation), ShippedDocumentationCeiling, ShippedDocumentationMargin)
	switch standing := ShippedDocumentationStanding(total); {
	case total >= ShippedDocumentationCeiling:
		t.Error(standing)
	case standing != "":
		t.Logf("WARNING: %s", standing)
	}
}

// The gate measures the set on disk, and the briefing gives the set what is left
// once the specifications, the tracker, the help, and the docket have taken
// theirs — so a set that passes the gate has to still fit the briefing with all
// of those at their largest, or the gate is green over a product manager that is
// not being shown the last document on the list. That is what happened at 896
// KiB: the set was 19 KiB under the budget and docs/configuration.md was
// dropped from every briefing. This assembles this repository's own set with
// the bounded sections at their bounds and refuses any omission.
func TestShippedDocumentationFitsTheBriefingBesideEverythingElse(t *testing.T) {
	t.Parallel()

	items := make([]beads.WorkItem, maxProductWorkItems)
	for index := range items {
		items[index] = beads.WorkItem{
			ID:       fmt.Sprintf("yoyodyne-ifd.%d", index),
			Title:    strings.Repeat("t", maxWorkItemTitleBytes),
			Status:   "open",
			Priority: 2,
		}
	}
	bundle, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          "../..",
		SpecificationsDirectory: "docs/product",
		ShippedDocumentation:    HarnessShippedDocumentation,
		WorkItems:               items,
		CommandHelp:             strings.Repeat("usage: yoyo <command>\n", maxCommandHelpBytes/len("usage: yoyo <command>\n")+1),
	})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if index := strings.Index(bundle.Text, "This documentation did not fit and is not included above:"); index >= 0 {
		// Which of the two grew is the whole of what a failure here has to say,
		// or it reads as the old wall — a red gate on a documentation edit that
		// says nothing about what to decide. The set is measured against its
		// ceiling; everything else shares the reserve, and the specifications
		// are the only part of it nothing bounds.
		specifications := 0
		if err := filepath.WalkDir("../../docs/product", func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".md") {
				return err
			}
			information, err := entry.Info()
			if err != nil {
				return err
			}
			specifications += int(information.Size())
			return nil
		}); err != nil {
			t.Errorf("size the specifications: %v", err)
		}
		t.Fatalf("the shipped documentation passes the gate and is still dropped from the briefing.\n"+
			"The set is %d bytes against a ceiling of %d, which the gate judges; the specifications are %d bytes and share the %d-byte reserve with the bounded sections, which nothing bounds.\n"+
			"If the set is inside its ceiling, it is the specifications that have outgrown productContextReserve, and that is a product decision about the context rather than a documentation edit to trim.\n%s",
			bundle.ShippedDocumentationBytes, ShippedDocumentationCeiling, specifications, productContextReserve, bundle.Text[index:])
	}
	for _, documentPath := range HarnessShippedDocumentation {
		if !strings.Contains(bundle.Text, "### Shipped documentation: "+documentPath) {
			t.Errorf("%s is in the shipped set and has no section in the briefing", documentPath)
		}
	}
	if !strings.Contains(bundle.Text, fmt.Sprintf("The shipped documentation is %d bytes across %d document(s) on disk.", bundle.ShippedDocumentationBytes, len(HarnessShippedDocumentation))) {
		t.Errorf("the briefing does not record the set's size:\n%s", bundle.Text[len(bundle.Text)-2000:])
	}
}

// The standing is one derivation shared by the gate, the briefing, and the
// operator's warning, so the three thresholds are pinned here: silence with room
// to spare, a warning naming the room left once inside the margin, and the
// ceiling reached once at it.
func TestShippedDocumentationStanding(t *testing.T) {
	t.Parallel()

	if standing := ShippedDocumentationStanding(ShippedDocumentationCeiling - ShippedDocumentationMargin); standing != "" {
		t.Errorf("a set exactly the margin under the ceiling has room to spare and got %q", standing)
	}
	inside := ShippedDocumentationStanding(ShippedDocumentationCeiling - ShippedDocumentationMargin + 1)
	if !strings.Contains(inside, fmt.Sprintf("%d bytes of room remain", ShippedDocumentationMargin-1)) || !strings.Contains(inside, "yoyodyne-ifd.117.4") {
		t.Errorf("a set one byte inside the margin should warn with the room left and name the reduction, got %q", inside)
	}
	at := ShippedDocumentationStanding(ShippedDocumentationCeiling)
	if !strings.Contains(at, "at or past") || !strings.Contains(at, "make test fails") {
		t.Errorf("a set at the ceiling should say the gate fails, got %q", at)
	}
	// Both sentences name the decision the next reader of them is about to
	// re-ask: yoyodyne-ifd.240 is where the product manager chose raising the
	// bound over trimming the guides or narrowing the set, and a warning that
	// only said how much room was left would send its reader back to the same
	// question with no record that it has been answered.
	for _, standing := range []string{inside, at} {
		if !strings.Contains(standing, "yoyodyne-ifd.240") {
			t.Errorf("the standing should name yoyodyne-ifd.240 as the precedent for raising the bound, got %q", standing)
		}
	}
}

// The harness's own set is eight generic paths — docs/work.md, docs/reporting.md,
// docs/operations.md and five more — and an adopting project can hold a file at
// any of them meaning something else entirely. Applied there it would put a
// stranger's prose in front of that project's product manager labeled "what the
// product ships", which is description arriving as gospel about a product it is
// not describing. So a repository that is not the one the set describes is shown
// what its own configuration names, and where it names nothing it is shown
// nothing and told so.
func TestAnAdoptingProjectIsNotShownTheHarnessOwnDocumentation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "go.mod", "module github.com/someone/ledger\n\ngo 1.24.0\n")
	writeProductFile(t, root, "docs/product/brief.md", wellFormed)
	// Every one of the eight, holding this project's own unrelated prose.
	for _, documentPath := range HarnessShippedDocumentation {
		writeProductFile(t, root, documentPath, "# Ledger\n\nThis is the ledger project's own "+documentPath+".\n")
	}

	bundle, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	for _, documentPath := range HarnessShippedDocumentation {
		if strings.Contains(bundle.Text, "### Shipped documentation: "+documentPath) {
			t.Errorf("%s is a path the harness holds for its own repository, and it was carried for an adopting project", documentPath)
		}
	}
	if strings.Contains(bundle.Text, "This is the ledger project's own") {
		t.Errorf("an adopting project's own files were carried as what the product ships:\n%s", bundle.Text)
	}
	// What is missing is a setting rather than the documents, and the two are
	// said differently: a project told its repository holds no documentation
	// would report itself as undocumented when it is only unconfigured.
	if !strings.Contains(bundle.Text, "This project names no operator-facing documentation") {
		t.Errorf("a project that named no documentation is not told so:\n%s", bundle.Text)
	}
	if strings.Contains(bundle.Text, "This repository holds none of the operator-facing documentation looked for here") {
		t.Errorf("a project that named no documentation is told its repository holds none:\n%s", bundle.Text)
	}
}

// The scoping turns on one string, and a fixture that writes its own go.mod
// passes whatever that string says -- so a typo in it, or a module renamed
// later, would drop all eight documents from this repository's own product
// manager with a green suite. That is the ifd.20 narrowing again, reached by a
// spelling mistake, and it is the same failure the neighbouring test guards
// against for paths. So the constant is checked against the repository it is a
// claim about, the way that test checks the paths.
func TestTheHarnessRecognizesThisRepository(t *testing.T) {
	t.Parallel()

	root, err := repowrite.NewRoot("../..")
	if err != nil {
		t.Fatalf("NewRoot() error = %v", err)
	}
	if !describesTheHarness(root) {
		declaration, err := os.ReadFile(filepath.Join("../..", "go.mod"))
		if err != nil {
			t.Fatalf("this repository was not recognized as the one the shipped set describes, and its go.mod could not be read: %v", err)
		}
		t.Fatalf("this repository was not recognized as the one the shipped set describes, so its product manager is shown none of it.\n"+
			"harnessModulePath is %q, and go.mod opens:\n%s", harnessModulePath, firstLines(string(declaration), 3))
	}
}

// firstLines is what a failure above quotes of go.mod, so the mismatch is read
// beside the constant rather than looked up afterwards.
func firstLines(content string, count int) string {
	lines := strings.Split(content, "\n")
	if len(lines) > count {
		lines = lines[:count]
	}
	return strings.Join(lines, "\n")
}

// The harness's own set still describes the harness's own repository, so it is
// carried there with nothing configured: narrowing this product manager's view
// to the command help is the ifd.20 narrowing arrived at through a scoping fix.
// The repository is identified by the module it declares, which is its own
// statement of what it is rather than a name an adopting project can hold by
// coincidence.
func TestTheHarnessOwnRepositoryIsShownItsOwnDocumentation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "go.mod", "module "+harnessModulePath+"\n\ngo 1.24.0\n")
	writeProductFile(t, root, "docs/product/brief.md", wellFormed)
	for index, documentPath := range HarnessShippedDocumentation {
		writeProductFile(t, root, documentPath, fmt.Sprintf("# Document %d\n\nSurface %d is described here.\n", index, index))
	}

	bundle, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	for _, documentPath := range HarnessShippedDocumentation {
		if !strings.Contains(bundle.Text, "### Shipped documentation: "+documentPath) {
			t.Errorf("%s was not carried for the repository the set describes", documentPath)
		}
	}
}

// What a project configures is the whole of what it is shown, in its own
// repository and in the harness's alike. A set that merged with the harness's
// own would be a project describing itself and getting somebody else's documents
// beside its own, which is the same collision by another door.
func TestConfiguredDocumentationIsTheWholeOfWhatIsCarried(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "go.mod", "module "+harnessModulePath+"\n\ngo 1.24.0\n")
	writeProductFile(t, root, "docs/product/brief.md", wellFormed)
	writeProductFile(t, root, "README.md", "# Yoyodyne\n\nThe set the harness holds names this.\n")
	writeProductFile(t, root, "docs/handbook.md", "# Handbook\n\nThe set this project names holds this.\n")

	bundle, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
		ShippedDocumentation:    []string{"docs/handbook.md"},
	})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(bundle.Text, "### Shipped documentation: docs/handbook.md") {
		t.Fatalf("the documentation this project names was not carried:\n%s", bundle.Text)
	}
	if strings.Contains(bundle.Text, "### Shipped documentation: README.md") {
		t.Fatalf("the harness's own set was carried beside the one this project named:\n%s", bundle.Text)
	}
}

// Whitespace around a configured path is not part of the path, and reading it
// as though it were loses the document in silence: the confinement rule is made
// on the trimmed path, and resolving the untrimmed one finds no such file and
// skips it, so a document configured and validated is simply never carried and
// nothing anywhere says which one.
func TestConfiguredDocumentationIsTrimmedBeforeItIsRead(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/brief.md", wellFormed)
	writeProductFile(t, root, "docs/handbook.md", "# Handbook\n\nWhat this project ships.\n")

	bundle, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
		ShippedDocumentation:    []string{"  docs/handbook.md  "},
	})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(bundle.Text, "### Shipped documentation: docs/handbook.md") {
		t.Fatalf("a configured path with surrounding whitespace was not carried:\n%s", bundle.Text)
	}
}

// A configured path that climbs out of the repository names text nobody reviewed
// with the code. Reading one is refused where every other reference is: the
// resolution proves what it opens is inside the repository, so the confinement
// does not depend on the configuration having been validated first.
func TestConfiguredDocumentationIsConfinedToTheRepository(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/brief.md", wellFormed)

	if _, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
		ShippedDocumentation:    []string{"../elsewhere.md"},
	}); err == nil {
		t.Fatal("a shipped-documentation path outside the repository was read")
	}
}

// Help is compiled into the product rather than read from it, so a caller that
// supplies far too much of it is a mistake in the caller. It is cut rather than
// carried, and cut at a line, so what survives is still help.
func TestCommandHelpIsBounded(t *testing.T) {
	t.Parallel()

	help := strings.Repeat("  run   run one Beads work item in an isolated worktree\n", 4096)
	bounded := boundedCommandHelp(help)
	if len(bounded) > maxCommandHelpBytes+len("\n[the rest of the command help is not included here]") {
		t.Fatalf("bounded help is %d bytes, over the %d bound", len(bounded), maxCommandHelpBytes)
	}
	if !strings.Contains(bounded, "[the rest of the command help is not included here]") {
		t.Fatal("help was cut without saying so")
	}
	if strings.HasSuffix(strings.TrimSuffix(bounded, "\n[the rest of the command help is not included here]"), "worktre") {
		t.Fatal("help was cut mid-line")
	}
	if unbounded := "Usage: yoyo <command>\n"; boundedCommandHelp(unbounded) != strings.TrimSpace(unbounded) {
		t.Fatalf("help inside the bound was changed: %q", boundedCommandHelp(unbounded))
	}
}

// Help with no line to cut at is still cut on a rune boundary: the byte the bound
// lands on here is the middle of an "é", and half of one is broken text.
func TestCommandHelpWithNoLineIsCutOnARuneBoundary(t *testing.T) {
	t.Parallel()

	help := "x" + strings.Repeat("é", maxCommandHelpBytes)
	if utf8.ValidString(help[:maxCommandHelpBytes]) {
		t.Fatal("the bound falls on a rune boundary, so this test no longer exercises a mid-rune cut")
	}
	bounded := boundedCommandHelp(help)
	kept, marked := strings.CutSuffix(bounded, "\n[the rest of the command help is not included here]")
	if !marked || !utf8.ValidString(kept) || len(kept) > maxCommandHelpBytes || len(kept) < maxCommandHelpBytes-1 {
		t.Fatalf("bounded help kept %d bytes, marked = %v, valid = %v, want at most %d valid bytes and the omission said",
			len(kept), marked, utf8.ValidString(kept), maxCommandHelpBytes)
	}
}

func TestAssembleProductNamesWhatItCouldNotInclude(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/brief.md", wellFormed)
	writeProductFile(t, root, "docs/product/huge.md", strings.Repeat("x", 4096))

	// The budget has to clear what is reserved before any specification is read —
	// the header, the tracker state, and the recorded-intent allowance — and leave
	// room for one specification but not the other.
	bundle, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product", MaxBytes: 5632})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(bundle.Text, "## Specifications omitted for size") || !strings.Contains(bundle.Text, "- docs/product/huge.md") {
		t.Fatalf("omitted specification is not reported:\n%s", bundle.Text)
	}
	if !strings.Contains(bundle.Text, "# Bounded runs") {
		t.Fatalf("a specification that fits was dropped:\n%s", bundle.Text)
	}

	// A budget that cannot even hold the tracker state is a caller error rather
	// than a silently empty conversation.
	if _, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product", MaxBytes: 32}); err == nil {
		t.Fatal("AssembleProduct() tiny budget error = nil")
	}
}

func TestAssembleProductStatesWhenTrackerStateIsMissing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/brief.md", wellFormed)

	unavailable, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
		WorkItemsUnavailable:    "bd list failed: no database",
	})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(unavailable.Text, "Beads state is unavailable: bd list failed: no database") {
		t.Fatalf("missing tracker state is not reported:\n%s", unavailable.Text)
	}
	if !strings.Contains(unavailable.Text, "Do not assume there is no work in flight") {
		t.Fatalf("missing tracker state reads as an empty tracker:\n%s", unavailable.Text)
	}

	empty, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(empty.Text, "Beads reported no matching work items.") {
		t.Fatalf("an empty tracker is not reported:\n%s", empty.Text)
	}
}

func TestAssembleProductRefusesSpecificationsOutsideTheRepository(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/brief.md", wellFormed)
	for _, directory := range []string{"", "   ", "..", "../elsewhere", "/etc"} {
		if _, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: directory}); err == nil {
			t.Fatalf("AssembleProduct(%q) error = nil", directory)
		}
	}

	// A path inside the repository that is not a directory is a configuration
	// mistake rather than a conversation with no intent in it.
	if _, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product/brief.md"}); err == nil {
		t.Fatal("AssembleProduct() on a file error = nil")
	}
}

func writeProductFile(t *testing.T, root, relative, content string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

// A role that owns documents is given them. The product manager is not, and
// that is the same decision read the other way: an architect that cannot see
// the design it owns answers from memory, and a product manager that can see it
// has the implementation arguing about what the product is for.
func TestAssembleProductGivesARoleItsOwnDocuments(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/runs.md", wellFormed)
	writeProductFile(t, root, "docs/designs/v1-harness-design.md", "# Design\n\nArchitecture, not intent.\n")
	writeProductFile(t, root, "docs/decisions/beads.md", "# Beads is the durable workflow store\n\nWhat was decided and why.\n")
	writeProductFile(t, root, "docs/decisions/invariants/one-promotion.md", "# One promotion per target branch\n\nThe constraint itself.\n")

	architect, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
		RoleDocuments: []DocumentSet{
			{Label: "Design", Directory: "docs/designs"},
			{Label: "Architectural invariant", Directory: "docs/decisions/invariants"},
			{Label: "Decision record", Directory: "docs/decisions"},
		},
	})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	for _, required := range []string{
		"## Authoritative product intent: docs/product/runs.md",
		"## Design: docs/designs/v1-harness-design.md",
		"Architecture, not intent.",
		"## Architectural invariant: docs/decisions/invariants/one-promotion.md",
		"## Decision record: docs/decisions/beads.md",
		"Your own documents are here too, from docs/designs, docs/decisions/invariants, docs/decisions.",
	} {
		if !strings.Contains(architect.Text, required) {
			t.Fatalf("the architect's context is missing %q:\n%s", required, architect.Text)
		}
	}
	// A document reachable from two of the sets is carried once, under the label
	// it was first read as: the invariants sit inside the decision records.
	if count := strings.Count(architect.Text, "docs/decisions/invariants/one-promotion.md"); count != 1 {
		t.Fatalf("the nested invariant appears %d times:\n%s", count, architect.Text)
	}
	// Intent is still counted as intent, whatever else the references now hold.
	if architect.SpecificationsIncluded != 1 || len(architect.References) != 4 {
		t.Fatalf("specifications = %d, references = %d", architect.SpecificationsIncluded, len(architect.References))
	}
	// The header no longer claims the design was withheld, because it was not.
	if strings.Contains(architect.Text, "the design document, and any way to run a") {
		t.Fatalf("the architect is told it has not read the design it was given:\n%s", architect.Text)
	}

	// The product manager asks for none of them and is given none of them.
	productManager, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
	})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	for _, excluded := range []string{"Architecture, not intent.", "The constraint itself.", "What was decided and why."} {
		if strings.Contains(productManager.Text, excluded) {
			t.Fatalf("the product manager was given %q:\n%s", excluded, productManager.Text)
		}
	}

	// A role's documents are confined to the repository exactly as the
	// specifications are, and a set that says nothing about what it holds is
	// refused rather than rendered as an anonymous pile.
	if _, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
		RoleDocuments:           []DocumentSet{{Label: "Design", Directory: "../elsewhere"}},
	}); err == nil {
		t.Fatal("AssembleProduct() read a directory outside the repository")
	}
	if _, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
		RoleDocuments:           []DocumentSet{{Directory: "docs/designs"}},
	}); err == nil {
		t.Fatal("AssembleProduct() accepted an unlabelled document set")
	}
}

// The header says what actually arrived. A role told its designs are here when
// the directory is empty answers as though it had read them, and — worse — is no
// longer told that the design document is among what it has not read.
func TestTheRoleDocumentNoteSaysWhatWasActuallyFound(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/runs.md", wellFormed)
	writeProductFile(t, root, "docs/designs/v1-harness-design.md", "# Design\n\nArchitecture, not intent.\n")

	sets := []DocumentSet{
		{Label: "Design", Directory: "docs/designs"},
		// Nothing has been written here, which is the case this is about.
		{Label: "Architectural invariant", Directory: "docs/decisions/invariants"},
	}
	mixed, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
		RoleDocuments:           sets,
	})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	for _, required := range []string{
		"Your own documents are here too, from docs/designs.",
		"Nothing was found under docs/decisions/invariants.",
		"treat them as unwritten rather than as something you have read",
	} {
		if !strings.Contains(mixed.Text, required) {
			t.Fatalf("the header is missing %q:\n%s", required, mixed.Text)
		}
	}
	if strings.Contains(mixed.Text, "here too, from docs/designs, docs/decisions/invariants") {
		t.Fatalf("an empty directory was reported as carrying documents:\n%s", mixed.Text)
	}

	// A role whose every directory is empty is told so, and is told the design
	// document is among what it has not read — which is exactly what the product
	// manager's own header says, and what a role given the designs must not be
	// told.
	empty, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
		RoleDocuments:           []DocumentSet{{Label: "Design", Directory: "docs/decisions"}},
	})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if strings.Contains(empty.Text, "Your own documents are here too") {
		t.Fatalf("documents nobody wrote were reported as present:\n%s", empty.Text)
	}
	for _, required := range []string{
		"Nothing was found under docs/decisions.",
		"the source, the design document, and any way to run a",
	} {
		if !strings.Contains(empty.Text, required) {
			t.Fatalf("the empty-directory header is missing %q:\n%s", required, empty.Text)
		}
	}

	// And the reserve still bounds what the note can cost, whichever way it went.
	for _, bundle := range []Bundle{mixed, empty} {
		if bundle.Bytes != len(bundle.Text) {
			t.Fatalf("bundle bytes = %d, text = %d", bundle.Bytes, len(bundle.Text))
		}
	}
	if longest := longestRoleDocumentNote(sets); longest < len(renderRoleDocumentNote(sets, map[string]bool{"docs/designs": true})) {
		t.Fatalf("the reserved note bound %d is smaller than a note it must cover", longest)
	}
}

// docketEntry is one stopped piece of work as the docket carries it.
func docketEntry(runID, item string) triage.Entry {
	return triage.Entry{
		SchemaVersion: triage.SchemaVersion,
		Key:           triage.Key(triage.ClassStoppedRun, runID),
		Class:         triage.ClassStoppedRun,
		ProductID:     "yoyodyne",
		RunID:         runID,
		WorkItemID:    item,
		RecordedAt:    time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC),
		Blocker:       "Yoyodyne stopped this item: the repair budget was spent.",
		Findings:      []triage.Finding{{Severity: "blocker", Message: "add the missing file", File: "feature.txt", Line: 1}},
		Artifacts: triage.Artifacts{
			Branch: "yoyodyne/task/abc", WorktreePath: "/state/worktrees/task",
			// What the docket found in the repository as it was built, which is what
			// the development manager reads rather than the run's removal flags.
			Found: &triage.Found{
				At:     time.Date(2026, 8, 19, 12, 5, 0, 0, time.UTC),
				Branch: "yoyodyne/task/abc", WorktreePath: "/state/worktrees/task",
				BranchThere: true, WorktreeThere: true,
			},
		},
		Counters: triage.Counters{ReviewRounds: 3, ReviewRoundsCap: 4, RepairAttempts: 2, RepairGrantAttempts: 2},
	}
}

// The docket reaches the conversation the way the backlog does: carried in the
// context rather than by an operator who noticed something had stopped.
func TestAssembleProductCarriesTheTriageDocket(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/runs.md", wellFormed)
	bundle, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
		TriageDocket:            []triage.Entry{docketEntry("run-0123456789abcdef0123456789abcdef", "yoyodyne-task")},
	})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	for _, required := range []string{
		"## Triage docket",
		"An entry states that something stopped or never started.",
		"[stopped run] 2026-08-19T12:00:00Z on yoyodyne-task",
		"Blocker: Yoyodyne stopped this item: the repair budget was spent.",
		"Finding [blocker] (feature.txt:1): add the missing file",
		"Branch (checked and there at 2026-08-19T12:05:00Z): yoyodyne/task/abc",
		"3 of 4 review round(s) used",
	} {
		if !strings.Contains(bundle.Text, required) {
			t.Fatalf("product context is missing %q:\n%s", required, bundle.Text)
		}
	}
}

// A role that was given no docket has no docket section at all, which is what
// keeps this the development manager's evidence rather than another thing every
// conversation reads past.
func TestAssembleProductCarriesNoDocketSectionForARoleWithoutOne(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/runs.md", wellFormed)
	bundle, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
	})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if strings.Contains(bundle.Text, "Triage docket") {
		t.Fatalf("a role with no docket was given one:\n%s", bundle.Text)
	}
}

// A docket that could not be read is stated. Rendering it as a product where
// nothing has stopped is the one thing that must not happen: the role would
// read an absence as an answer.
func TestAssembleProductSaysWhenTheDocketCouldNotBeRead(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/runs.md", wellFormed)
	bundle, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
		TriageDocketUnavailable: "open docket: permission denied",
	})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	for _, required := range []string{
		"The triage docket could not be read: open docket: permission denied",
		"Do not assume nothing has stopped",
	} {
		if !strings.Contains(bundle.Text, required) {
			t.Fatalf("product context is missing %q:\n%s", required, bundle.Text)
		}
	}
}

// The docket grows with everything that ever stopped and the context budget
// does not, so it is bounded — and what it could not show is stated rather than
// left to read as a complete docket.
func TestAssembleProductBoundsTheDocketAndSaysWhatItCutOut(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/runs.md", wellFormed)
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	entries := make([]triage.Entry, 0, maxDocketEntries+5)
	for index := range maxDocketEntries + 5 {
		entry := docketEntry(fmt.Sprintf("run-%032x", index), fmt.Sprintf("yoyodyne-%d", index))
		// yoyodyne-0 stopped first, forty days ago, and each after it a day later.
		entry.RecordedAt = now.AddDate(0, 0, index-40)
		entries = append(entries, entry)
	}
	bundle, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
		TriageDocket:            entries,
		TriageDocketAt:          now,
	})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	// The oldest are what is kept, because a stoppage waiting longest for a
	// decision is the one that must not be pushed out by the latest; and what
	// was left out is counted with how long the oldest of it has waited.
	if !strings.Contains(bundle.Text, "5 further live docket entry(s) have no room here for their evidence, so each is named below in one line with who moves next and the command that shows it whole; the oldest of them stopped 15d ago") {
		t.Fatalf("a cut docket did not say what it cut:\n%s", bundle.Text)
	}
	listed, named := strings.Join(listedDocketItems(bundle.Text), " "), namedDocketItems(bundle.Text)
	if !strings.Contains(listed+" ", "yoyodyne-0 ") || strings.Contains(listed+" ", "yoyodyne-29 ") || !slices.Contains(named, "yoyodyne-29") {
		t.Fatalf("the docket kept the newest entries rather than the oldest:\n%s", bundle.Text)
	}
}

// A run docketed more than once is one entry standing where its latest docketing
// was recorded, and it has waited since its first. The docket is ordered by that,
// so a stoppage an escalation was later docketed beside is not moved behind the
// stoppages that came after it.
func TestTheDocketListsAFoldedRunByWhenItFirstStopped(t *testing.T) {
	t.Parallel()

	earliest := docketEntry("run-00000000000000000000000000000001", "yoyodyne-folded")
	later := docketEntry("run-00000000000000000000000000000002", "yoyodyne-later")
	later.RecordedAt = earliest.RecordedAt.Add(time.Hour)
	folded := earliest
	folded.Key = triage.Key(triage.ClassEscalation, earliest.RunID)
	folded.Class = triage.ClassEscalation
	folded.RecordedAt = earliest.RecordedAt.Add(2 * time.Hour)
	folded.Earlier = []triage.Entry{earliest}

	rendered, _ := TriageDocket(ProductRequest{TriageDocket: []triage.Entry{later, folded}})
	first, second := strings.Index(rendered, "on yoyodyne-folded"), strings.Index(rendered, "on yoyodyne-later")
	if first < 0 || second < 0 || first > second {
		t.Fatalf("the run that stopped first is not listed first:\n%s", rendered)
	}
}

// The window is the live docket and only that: an entry on a closed item and a
// second entry about one run are not listed, the live ones are listed oldest
// stoppage first, and what did not fit is counted. The position it hands back
// is past the last entry it listed, so the next window opens with the first one
// this window did not show.
func TestTheDocketWindowIsTheLiveDeduplicatedDocketOldestFirstAndResumes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/runs.md", wellFormed)
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	stopped := func(index, daysAgo int, item string) triage.Entry {
		entry := docketEntry(fmt.Sprintf("run-%032x", index), item)
		entry.RecordedAt = now.AddDate(0, 0, -daysAgo)
		return entry
	}
	var entries []triage.Entry
	// Dead: stoppages on work that has since closed, newer than everything live,
	// which is exactly what filled the window newest first.
	for index := range 40 {
		entries = append(entries, stopped(1000+index, 1, "yoyodyne-closed"))
	}
	// Live: maxDocketEntries+3 stoppages on open work, recorded newest first so
	// the log's own order is the wrong one.
	for index := maxDocketEntries + 2; index >= 0; index-- {
		entries = append(entries, stopped(index, 10+index, fmt.Sprintf("yoyodyne-live-%d", index)))
	}
	// Repeated: the oldest live run left a second entry, a publication, later on.
	repeatFirst := now.AddDate(0, 0, -(10 + maxDocketEntries + 2))
	repeat := stopped(maxDocketEntries+2, 2, fmt.Sprintf("yoyodyne-live-%d", maxDocketEntries+2))
	repeat.Class = triage.ClassPublication
	repeat.Key = triage.Key(triage.ClassPublication, repeat.RunID)
	entries = append(entries, repeat)

	request := ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
		TriageDocket:            entries,
		TriageDocketAt:          now,
		TriageDocketItems: []beads.WorkItem{
			{ID: "yoyodyne-closed", Status: "closed"},
			{ID: "yoyodyne-live-0", Status: "blocked"},
		},
	}
	bundle, err := AssembleProduct(request)
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	listed := listedDocketItems(bundle.Text)
	if strings.Contains(bundle.Text, "on yoyodyne-closed") {
		t.Fatalf("an entry on a closed item was listed:\n%v", listed)
	}
	if len(listed) != maxDocketEntries {
		t.Fatalf("listed %d entries, want %d: %v", len(listed), maxDocketEntries, listed)
	}
	// Oldest stoppage first: the repeated run waited longest, and is listed once.
	for position, item := range listed {
		want := fmt.Sprintf("yoyodyne-live-%d", maxDocketEntries+2-position)
		if item != want {
			t.Fatalf("entry %d is on %s, want %s; listed %v", position, item, want, listed)
		}
	}
	for _, required := range []string{
		"Docketed 1 time(s) before for this run",
		"This run has waited since " + repeatFirst.Format(time.RFC3339) + ", when it was first docketed.",
		"3 further live docket entry(s) have no room here for their evidence, so each is named below in one line with who moves next and the command that shows it whole; the oldest of them stopped 12d ago",
		"40 docket entry(s) are not listed because the work item they stopped is closed.",
	} {
		if !strings.Contains(bundle.Text, required) {
			t.Fatalf("the docket window is missing %q:\n%s", required, bundle.Text)
		}
	}
	if bundle.TriageDocketPosition == nil {
		t.Fatal("a window that walked past entries handed back no position")
	}

	// The next window resumes: what this one left out is listed first.
	request.TriageDocketPosition = *bundle.TriageDocketPosition
	next, err := AssembleProduct(request)
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	resumed := listedDocketItems(next.Text)
	if got, want := resumed[:3], []string{"yoyodyne-live-2", "yoyodyne-live-1", "yoyodyne-live-0"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("the next window opened with %v, want %v", got, want)
	}
}

// gatedDocketEntry is a stoppage the development manager already decided, whose
// re-run the harness was refused carrying out by a gate that will not clear on
// its own, carrying a failing check as long as the ones that filled the window on
// 2026-09-26 and a second docketing of the same run folded beneath it.
func gatedDocketEntry(index int, recorded time.Time) triage.Entry {
	run := fmt.Sprintf("run-gated-%023x", index)
	entry := docketEntry(run, fmt.Sprintf("yoyodyne-decided-%d", index))
	entry.RecordedAt = recorded
	entry.Check = &triage.Check{Command: "make check", ExitCode: 2, Output: strings.Repeat("--- FAIL: a test that failed at length\n", 300)}
	entry.Closed = &triage.Closure{Decision: "rerun", ClosedAt: recorded.Add(time.Hour)}
	entry.CarryOut = &triage.CarryOut{Decision: "rerun", Gate: "dependency", Refusal: "the item waits on unfinished work",
		Clears: "the work it waits on closing", Attempts: 140, RefusedAt: recorded.Add(2 * time.Hour)}
	earlier := entry
	earlier.Key = triage.Key(triage.ClassPublication, run)
	earlier.Class = triage.ClassPublication
	earlier.RecordedAt = recorded.Add(-time.Hour)
	earlier.Earlier = nil
	entry.Earlier = []triage.Entry{earlier}
	return entry
}

// The docket of 2026-09-26: four decided entries, each waiting on a gate and
// each long enough to take a quarter of the docket's bytes, and 29 stoppages
// nobody had decided. The window lists undecided work first and fills its
// bound with it, says how many entries remain and how many of those are already
// decided, and the next window carries on with the undecided ones it left out.
func TestTheDocketWindowListsEveryUndecidedStoppageBeforeAnyDecidedOne(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	var entries []triage.Entry
	// The decided entries stopped first, so neither age nor criticality may put
	// them ahead.
	for index := range 4 {
		entries = append(entries, gatedDocketEntry(index, now.AddDate(0, 0, -60+index)))
	}
	for index := range 29 {
		entry := docketEntry(fmt.Sprintf("run-%032x", index), fmt.Sprintf("yoyodyne-undecided-%d", index))
		entry.RecordedAt = now.AddDate(0, 0, -30+index)
		entries = append(entries, entry)
	}
	request := ProductRequest{TriageDocket: entries, TriageDocketAt: now, TriageDocketItems: []beads.WorkItem{}}

	rendered, position := TriageDocket(request)
	listed := listedDocketItems(rendered)
	if len(listed) != maxDocketEntries {
		t.Fatalf("listed %d entries, want the window filled to %d: %v", len(listed), maxDocketEntries, listed)
	}
	for index, item := range listed {
		if want := fmt.Sprintf("yoyodyne-undecided-%d", index); item != want {
			t.Fatalf("entry %d is on %s, want %s: every undecided stoppage comes first, oldest first; listed %v", index, item, want, listed)
		}
	}
	if len(rendered) > MaxTriageDocketBytes {
		t.Fatalf("the docket is %d bytes, past its bound of %d", len(rendered), MaxTriageDocketBytes)
	}
	if want := "8 further live docket entry(s) have no room here for their evidence, so each is named below in one line with who moves next and the command that shows it whole; the oldest of them stopped 60d ago. 4 of them nobody has decided, and 4 are decisions of yours already recorded and waiting on the harness carrying them out."; !strings.Contains(rendered, want) {
		t.Fatalf("the docket did not say what remains, want %q:\n%s", want, rendered)
	}
	if position == nil {
		t.Fatal("a window that walked past entries handed back no position")
	}

	request.TriageDocketPosition = *position
	next, _ := TriageDocket(request)
	resumed := listedDocketItems(next)
	if got, want := resumed[:4], []string{"yoyodyne-undecided-25", "yoyodyne-undecided-26", "yoyodyne-undecided-27", "yoyodyne-undecided-28"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("the next window opened with %v, want %v", got, want)
	}
	if strings.Contains(strings.Join(resumed, " "), "yoyodyne-decided-") {
		t.Fatalf("a decided entry was listed while undecided ones were left out:\n%v", resumed)
	}
	if named := strings.Join(namedDocketItems(next), " "); !strings.Contains(named, "yoyodyne-decided-0") {
		t.Fatalf("the decided entries with no room for their evidence were not named: %v", named)
	}
}

// Where the undecided entries leave room, the decided ones follow them — the
// ones a gate refused ahead of the ones waiting — and an entry longer than its
// share of the docket's bytes is cut and says so rather than crowding out the
// entries after it.
func TestTheDocketWindowSharesItsBytesSoEveryEntryIsListed(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	var entries []triage.Entry
	for index := range 6 {
		entry := gatedDocketEntry(index, now.AddDate(0, 0, -60+index))
		if index == 0 {
			entry.CarryOut.Waiting = true
		}
		entries = append(entries, entry)
	}
	for index := range 3 {
		entry := docketEntry(fmt.Sprintf("run-%032x", index), fmt.Sprintf("yoyodyne-undecided-%d", index))
		entry.RecordedAt = now.AddDate(0, 0, -30+index)
		entries = append(entries, entry)
	}

	rendered, _ := TriageDocket(ProductRequest{TriageDocket: entries, TriageDocketAt: now, TriageDocketItems: []beads.WorkItem{}})
	want := []string{"yoyodyne-undecided-0", "yoyodyne-undecided-1", "yoyodyne-undecided-2",
		"yoyodyne-decided-1", "yoyodyne-decided-2", "yoyodyne-decided-3", "yoyodyne-decided-4", "yoyodyne-decided-5", "yoyodyne-decided-0"}
	if got := listedDocketItems(rendered); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("listed %v, want %v", got, want)
	}
	if len(rendered) > MaxTriageDocketBytes {
		t.Fatalf("the docket is %d bytes, past its bound of %d", len(rendered), MaxTriageDocketBytes)
	}
	if !strings.Contains(rendered, "…[cut here so that every listed entry fits the docket: this entry runs to ") {
		t.Fatalf("a long entry was not cut with the cut declared:\n%s", rendered)
	}
	if strings.Contains(rendered, "further live docket entry(s)") {
		t.Fatalf("a docket that listed everything said entries were left out:\n%s", rendered)
	}
	// The short undecided entries are within their share and listed whole.
	if strings.Count(rendered, "Finding [blocker] (feature.txt:1)") < 3 {
		t.Fatalf("an undecided entry within its share was cut:\n%s", rendered)
	}
}

func TestCutDocketEntryKeepsWholeLinesAndItsHeading(t *testing.T) {
	t.Parallel()

	section := "  [stopped run] heading\n" + strings.Repeat("      evidence line\n", 200)
	entry := docketEntry("run-heading", "yoyodyne-ifd.9")
	if got := cutDocketEntry(section, 0, entry); got != section {
		t.Fatal("an entry with no share was cut")
	}
	cut := cutDocketEntry(section, 600, entry)
	if len(cut) > 600 || !strings.HasPrefix(cut, "  [stopped run] heading\n") ||
		!strings.HasSuffix(cut, "Next mover: you. `yoyo triage show run-heading` shows it whole]\n") {
		t.Fatalf("cut entry is %d bytes:\n%s", len(cut), cut)
	}
	if share := docketEntryShare([]string{"aa", "bbbbbbbbbb", "cccccccccc"}, 12); share != 5 {
		t.Fatalf("share = %d, want the short entry whole and the rest split evenly", share)
	}
}

// listedDocketItems is the work item each docket entry shown with its evidence
// is on, in the order the window lists them; namedDocketItems is the same for
// the entries named in one line.
func listedDocketItems(text string) []string {
	return docketItems(text, false)
}

func namedDocketItems(text string) []string {
	return docketItems(text, true)
}

func docketItems(text string, named bool) []string {
	var items []string
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "  [") || strings.HasSuffix(line, "` shows it whole") != named {
			continue
		}
		_, after, found := strings.Cut(line, " on ")
		if !found {
			continue
		}
		item, _, _ := strings.Cut(after, " ")
		items = append(items, item)
	}
	return items
}

// A caller comparing two assembled contexts section by section is told where
// each section starts by the package that wrote it. Every section the assembly
// opens is recognized, and a heading inside a document is not one.
func TestSectionHeadingRecognizesEverySectionTheAssemblyOpens(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/runs.md", wellFormed)
	writeProductFile(t, root, "docs/designs/harness.md", "# Harness\n\nHow it is built.\n\n## Goals\n\n- Built well.\n")
	writeProductFile(t, root, "README.md", "# Yoyodyne\n\nWhat the product ships.\n\n## Install\n\nRun it.\n")
	bundle, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
		ShippedDocumentation:    []string{"README.md"},
		RoleDocuments:           []DocumentSet{{Label: "Design", Directory: "docs/designs"}},
		CommandHelp:             "yoyo help",
		WorkItems: []beads.WorkItem{
			{ID: "yoyodyne-ifd.20", Title: "Carry what moved", Status: "open", Priority: 1, IssueType: "task"},
		},
	})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	lines := strings.Split(bundle.Text, "\n")
	recognized := map[string]bool{}
	for _, line := range lines {
		if SectionHeading(line) {
			recognized[line] = true
		}
	}
	for _, section := range []string{
		"# Product context",
		"## Recorded product intent",
		"## Authoritative product intent: docs/product/runs.md",
		"## Design: docs/designs/harness.md",
		"## What the product ships today",
		"### Command help",
		"### Shipped documentation: README.md",
		"## Beads work items",
	} {
		if !recognized[section] {
			t.Fatalf("SectionHeading(%q) = false, want the section recognized; recognized %v", section, recognized)
		}
	}
	for _, inside := range []string{"# Bounded runs", "## Goals", "## Install", "# Harness", "## Note: this is prose"} {
		if SectionHeading(inside) {
			t.Fatalf("SectionHeading(%q) = true, want a heading inside a document left alone", inside)
		}
	}
}

// The docket of 2026-10-01: eleven stopped runs the development manager decided
// to wait on, every one older than the three new stoppages beside them. The
// three are listed first, each waited run after them says until when, and a
// docket too long to list whole says how many of what it left out are waits.
func TestTheDocketWindowListsWaitedStoppagesAfterEveryUndecidedOne(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	waited := func(index int) triage.Entry {
		entry := docketEntry(fmt.Sprintf("run-waited-%026x", index), fmt.Sprintf("yoyodyne-waited-%d", index))
		entry.RecordedAt = now.AddDate(0, 0, -40+index)
		entry.Closed = &triage.Closure{Decision: "wait", DecidedBy: "the development manager", ClosedAt: now.AddDate(0, 0, -1), RevisitAfter: now.AddDate(0, 0, 2)}
		return entry
	}
	undecided := func(index int) triage.Entry {
		entry := docketEntry(fmt.Sprintf("run-%032x", index), fmt.Sprintf("yoyodyne-undecided-%d", index))
		entry.RecordedAt = now.AddDate(0, 0, -3).Add(time.Duration(index) * time.Minute)
		return entry
	}
	var entries []triage.Entry
	for index := range 11 {
		entries = append(entries, waited(index))
	}
	for index := range 3 {
		entries = append(entries, undecided(index))
	}

	rendered, position := TriageDocket(ProductRequest{TriageDocket: entries, TriageDocketAt: now, TriageDocketItems: []beads.WorkItem{}})
	listed := listedDocketItems(rendered)
	if len(listed) != 14 {
		t.Fatalf("listed %d entries, want all fourteen: %v", len(listed), listed)
	}
	for index, item := range listed {
		want := fmt.Sprintf("yoyodyne-undecided-%d", index)
		if index >= 3 {
			want = fmt.Sprintf("yoyodyne-waited-%d", index-3)
		}
		if item != want {
			t.Fatalf("entry %d is on %s, want %s: the new stoppages first, then the waits; listed %v", index, item, want, listed)
		}
	}
	if !strings.Contains(rendered, "to be looked at again after 2026-10-03T12:00:00Z") {
		t.Fatalf("a waited entry did not say until when:\n%s", rendered)
	}
	if strings.Contains(rendered, "not listed because a decision still standing settled them") {
		t.Fatalf("the waits were counted as settled:\n%s", rendered)
	}
	// The walk advanced over the new stoppages and not over the waits, which sit
	// outside it.
	if position == nil || position.Key != entries[13].Key {
		t.Fatalf("position = %+v, want past the last new stoppage", position)
	}

	// A docket too long to list whole leaves the waits out first and counts them.
	for index := 3; index < maxDocketEntries+3; index++ {
		entries = append(entries, undecided(index))
	}
	crowded, _ := TriageDocket(ProductRequest{TriageDocket: entries, TriageDocketAt: now, TriageDocketItems: []beads.WorkItem{}})
	if strings.Contains(strings.Join(listedDocketItems(crowded), " "), "yoyodyne-waited-") {
		t.Fatalf("a waited entry was listed while undecided ones were left out:\n%s", crowded)
	}
	if want := "14 further live docket entry(s) have no room here for their evidence, so each is named below in one line with who moves next and the command that shows it whole; the oldest of them stopped 40d ago. 3 of them nobody has decided, and 11 are stoppages you decided to wait on, each one undecided again once its wait runs out."; !strings.Contains(crowded, want) {
		t.Fatalf("the docket did not say what remains, want %q:\n%s", want, crowded)
	}
}

// A project that keeps its intent in a companion intent repository: the
// conversation's specifications and the role's own documents are read there,
// and nothing of them is looked for in the project's repository.
func TestAssembleProductReadsIntentFromTheCompanionRepository(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	intent := t.TempDir()
	for path, content := range map[string]string{
		"docs/product/brief.md": "# Brief\n\nThe companion brief.\n",
		"docs/designs/sums.md":  "# Sums\n\nThe companion design.\n",
	} {
		full := filepath.Join(intent, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	bundle, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          project,
		IntentRoot:              intent,
		SpecificationsDirectory: "docs/product",
		RoleDocuments:           []DocumentSet{{Label: "Design", Directory: "docs/designs"}},
	})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	for _, want := range []string{"The companion brief.", "The companion design."} {
		if !strings.Contains(bundle.Text, want) {
			t.Errorf("product context does not carry %q:\n%s", want, bundle.Text)
		}
	}
	if bundle.SpecificationsIncluded != 1 {
		t.Errorf("SpecificationsIncluded = %d, want 1", bundle.SpecificationsIncluded)
	}
}
