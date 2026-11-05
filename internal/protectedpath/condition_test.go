package protectedpath

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// defaultHomes is the homes a project on the recommended layout gets, owning
// the two documents the incidents behind this gate were about.
func defaultHomes() Homes {
	return ArtifactHomes(config.Config{Product: config.Product{
		Specifications: config.DefaultSpecifications,
		Designs:        config.DefaultDesigns,
		Decisions:      config.DefaultDecisions,
		Invariants:     config.DefaultInvariants,
	}},
		Document{ID: "slack-reporting-design", Path: "docs/designs/slack-reporting-design.md"},
		Document{ID: "observability-and-dashboard", Path: "docs/designs/observability-and-dashboard.md"},
		Document{ID: "brief", Path: "docs/product/brief.md"},
	)
}

// The three incidents, as their done-conditions were written. Each names a
// document the run may not write in the clause that says what done means, and
// each is refused with that clause quoted and the fix named.
func TestADoneConditionNamingAnOwnedDocumentIsRefusedWithTheClauseQuoted(t *testing.T) {
	t.Parallel()

	for _, incident := range []struct {
		item        string
		description string
		wantPath    string
		wantClause  string
	}{
		{
			item:        "yoyodyne-ifd.68.25",
			description: "Operator-requested capability. The boundary stays as-is.\n\nDone means her ruling is recorded on the slack-reporting design, the sink renders role prose per it, and the token never reaches an agent process.",
			wantPath:    "docs/designs/slack-reporting-design.md",
			wantClause:  "Done means her ruling is recorded on the slack-reporting design, the sink renders role prose per it, and the token never reaches an agent process.",
		},
		{
			item:        "yoyodyne-ifd.63",
			description: "Fold the script into the verb.\n\nDone means: yoyo status covers what bin/yoyo-status does today; documentation reconciles, including docs/designs/v1-harness-design.md's yoyo status entry, which this finally builds.\n\nRelation: none.",
			wantPath:    "docs/designs/v1-harness-design.md",
			wantClause:  "documentation reconciles, including docs/designs/v1-harness-design.md's yoyo status entry, which this finally builds.",
		},
		{
			item:        "yoyodyne-ifd.141.1",
			description: "First of three children. Done means: the read model exposes that state; and the observability-and-dashboard design's query list marks the query as existing.",
			wantPath:    "docs/designs/observability-and-dashboard.md",
			wantClause:  "and the observability-and-dashboard design's query list marks the query as existing.",
		},
	} {
		conditions := defaultHomes().Ungranted(incident.description, "", nil)
		if len(conditions) != 1 {
			t.Fatalf("%s: Ungranted() = %#v, want exactly the one clause", incident.item, conditions)
		}
		if conditions[0].Path != incident.wantPath {
			t.Fatalf("%s: Ungranted() reached %q, want %q", incident.item, conditions[0].Path, incident.wantPath)
		}
		if conditions[0].Clause != incident.wantClause {
			t.Fatalf("%s: Ungranted() quoted %q, want %q", incident.item, conditions[0].Clause, incident.wantClause)
		}
		// The refusal has three jobs: quote the clause, name the document, and say
		// what to do instead — both fixes, because a role told only "no" writes the
		// same clause again with the document named differently.
		refusal := conditions[0].Refusal()
		for _, want := range []string{incident.wantClause, incident.wantPath, "owner", "architect", GrantMarker, "governed path"} {
			if !strings.Contains(refusal, want) {
				t.Fatalf("%s: refusal %q never says %q", incident.item, refusal, want)
			}
		}
	}
}

// A grant covering the path is what admits the condition, exactly as it admits
// the diff: one naming the document, or one naming the home it sits in.
func TestAGrantAdmitsTheConditionItCovers(t *testing.T) {
	t.Parallel()

	description := "Done means the ruling is recorded on the slack-reporting design as the architect's revision, which the operator approved on 2026-09-02."
	if conditions := defaultHomes().Ungranted(description, "", []string{"docs/designs/slack-reporting-design.md"}); len(conditions) != 0 {
		t.Fatalf("Ungranted() with the document granted = %#v, want nothing", conditions)
	}
	if conditions := defaultHomes().Ungranted(description, "", []string{"docs/designs"}); len(conditions) != 0 {
		t.Fatalf("Ungranted() with the home granted = %#v, want nothing", conditions)
	}
	// A grant of a different document admits nothing here.
	if conditions := defaultHomes().Ungranted(description, "", []string{"docs/designs/v1-harness-design.md"}); len(conditions) != 1 {
		t.Fatalf("Ungranted() with another document granted = %#v, want the condition refused", conditions)
	}
	// And the grant is read as the item writes it, through Grants, so the two
	// halves of the item agree about what was admitted.
	granted := description + "\n\n" + GrantMarker + " docs/designs/slack-reporting-design.md\n"
	if problems := defaultHomes().ConditionProblems(Subject{Description: granted, Granted: Grants(granted)}); len(problems) != 0 {
		t.Fatalf("ConditionProblems() on a granted item = %v, want nothing", problems)
	}
}

// Only the done-conditions are read. An item cites the design it builds
// against, the decision that rules something out, and the invariant it is held
// to, in nearly every description, and a citation is not a condition.
func TestProseOutsideTheDoneConditionsIsNotACondition(t *testing.T) {
	t.Parallel()

	description := "The observability-and-dashboard design requires one read-model query that does not exist, per docs/designs/observability-and-dashboard.md and the ruling in docs/decisions/entanglement-and-merge-affinity.md.\n\n" +
		"Done means: the read model exposes that state through the same server-side projection every operator surface reads, and a test pins the shape.\n\n" +
		"The design's query list is the architect's document and is not touched by this run; she amends it through the governed path, and the run's summary names the query so she can. See docs/designs/observability-and-dashboard.md for the list."
	if conditions := defaultHomes().Ungranted(description, "", nil); len(conditions) != 0 {
		t.Fatalf("Ungranted() on citations outside the done-means = %#v, want nothing", conditions)
	}
	// The whole of the acceptance criteria is a condition by construction.
	if conditions := defaultHomes().Ungranted("", "The query is exposed. docs/designs/observability-and-dashboard.md lists it as existing.", nil); len(conditions) != 1 {
		t.Fatalf("Ungranted() on acceptance criteria = %#v, want the one clause", conditions)
	}
	// The done-means paragraph ends at the blank line, so a citation in the
	// paragraph after it is a citation.
	description = "Done means the query exists.\n\nSee docs/designs/observability-and-dashboard.md."
	if conditions := defaultHomes().Ungranted(description, "", nil); len(conditions) != 0 {
		t.Fatalf("Ungranted() past the done-means paragraph = %#v, want nothing", conditions)
	}
	// And the other two phrasings this backlog writes done-conditions in are read.
	for _, phrasing := range []string{"DONE: docs/product/goals/v1-goals.md states the goal.", "The item is done when docs/product/goals/v1-goals.md states the goal."} {
		if conditions := defaultHomes().Ungranted(phrasing, "", nil); len(conditions) != 1 {
			t.Fatalf("Ungranted() on %q = %#v, want the one clause", phrasing, conditions)
		}
	}
}

// A path outside the artifact homes is a path the run may write, however it is
// named, and a document is named by its id only where the id could not be an
// ordinary word.
func TestOnlyTheArtifactHomesAndTheirDocumentsAreConditions(t *testing.T) {
	t.Parallel()

	homes := defaultHomes()
	// docs/work.md is documentation the developer updates as part of the work,
	// and the configuration directory is protected in a diff but not here: a
	// condition naming it is about the harness's settings, not another role's
	// document.
	description := "Done means docs/work.md states the rule, .yoyodyne/config.yaml names the check, and internal/protectedpath/condition.go carries it; the brief's acceptance criteria hold."
	if conditions := homes.Ungranted(description, "", nil); len(conditions) != 0 {
		t.Fatalf("Ungranted() on ordinary paths and a one-word id = %#v, want nothing", conditions)
	}
	// The separator is required, so a sibling of a home is not inside it.
	if conditions := homes.Ungranted("Done means docs/products/index.md lists it.", "", nil); len(conditions) != 0 {
		t.Fatalf("Ungranted() on a sibling of the home = %#v, want nothing", conditions)
	}
	// A home named bare, with or without its trailing slash, is the home and names
	// no document, and so is a folder inside one; a file inside one is a document
	// whether or not the store knows it yet.
	for _, named := range []string{"docs/designs", "docs/designs/", "docs/product/goals/", "docs/decisions/invariants"} {
		if conditions := homes.Ungranted("Done means a design under "+named+" records it.", "", nil); len(conditions) != 0 {
			t.Fatalf("Ungranted() on %q = %#v, want nothing", named, conditions)
		}
	}
	if conditions := homes.Ungranted("Done means docs/designs/new-thing.md records it.", "", nil); len(conditions) != 1 || conditions[0].Path != "docs/designs/new-thing.md" {
		t.Fatalf("Ungranted() on a new file in a home = %#v, want it refused", conditions)
	}
	// An id inside a longer identifier is not the id, and the id is found however
	// the prose joined its words.
	if conditions := homes.Ungranted("Done means slack-reporting-design-v2 is superseded.", "", nil); len(conditions) != 0 {
		t.Fatalf("Ungranted() on a longer identifier = %#v, want nothing", conditions)
	}
	for _, named := range []string{"the Slack-Reporting design", "slack_reporting_design", "`slack-reporting-design`"} {
		if conditions := homes.Ungranted("Done means "+named+" records the rule.", "", nil); len(conditions) != 1 {
			t.Fatalf("Ungranted() on %q = %#v, want the document named", named, conditions)
		}
	}
	// A project that keeps its designs somewhere else has them checked there.
	moved := ArtifactHomes(config.Config{Product: config.Product{Designs: "architecture/designs"}})
	if conditions := moved.Ungranted("Done means architecture/designs/v1.md records it and docs/designs/v1.md does not exist.", "", nil); len(conditions) != 1 || conditions[0].Path != "architecture/designs/v1.md" {
		t.Fatalf("Ungranted() on a moved home = %#v, want the moved design alone", conditions)
	}
	// Homes nobody configured refuse nothing, which is what a caller wired none
	// gets.
	if conditions := (Homes{}).Ungranted("Done means docs/designs/v1.md records it.", "", nil); len(conditions) != 0 || !(Homes{}).Empty() {
		t.Fatalf("Ungranted() on empty homes = %#v, want nothing", conditions)
	}
}

// A clause is quoted once however many times it names a document, and a
// reading is bounded so the refusal stays readable.
func TestConditionsAreDeduplicatedAndBounded(t *testing.T) {
	t.Parallel()

	description := "Done means docs/designs/a.md, docs/designs/a.md, and docs/designs/b.md each record it; docs/designs/c.md does; docs/designs/d.md does; docs/designs/e.md does; docs/designs/f.md does; docs/designs/g.md does."
	conditions := defaultHomes().Ungranted(description, "", nil)
	if len(conditions) != maxConditions {
		t.Fatalf("Ungranted() = %d conditions, want the bound of %d", len(conditions), maxConditions)
	}
	if conditions[0].Path != "docs/designs/a.md" || conditions[1].Path != "docs/designs/b.md" {
		t.Fatalf("Ungranted() = %#v, want a.md once and then b.md", conditions[:2])
	}
	long := "Done means " + strings.Repeat("the design at docs/designs/a.md records it and ", 20) + "so on."
	conditions = defaultHomes().Ungranted(long, "", nil)
	if len(conditions) != 1 || len(conditions[0].Clause) > maxClauseBytes+len("...") || !strings.HasSuffix(conditions[0].Clause, "...") {
		t.Fatalf("Ungranted() on a long clause = %#v, want it folded to %d bytes", conditions, maxClauseBytes)
	}
}

// The documents are read as the artifact store identifies them, so what a
// condition is checked against is what every other reader of the homes sees: a
// file the store refused is still a document the home owns, an index is not,
// and an invariant is left to its path.
func TestOwnedDocumentsAreTheArtifactStores(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write := func(relative, content string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("docs/designs/README.md", "# Designs\n")
	write("docs/designs/slack-reporting-design.md", "---\nid: slack-reporting-design\nkind: design\nstatus: in-force\nowner: architect\nsupports: [v1-goals]\nrevisions: []\n---\n# Slack\n")
	write("docs/designs/malformed-design.md", "no frontmatter here\n")
	write("docs/decisions/invariants/one-promotion-per-target-branch.md", "---\nid: x\n---\n")
	product := config.Product{
		Specifications: config.DefaultSpecifications,
		Designs:        config.DefaultDesigns,
		Decisions:      config.DefaultDecisions,
		Invariants:     config.DefaultInvariants,
	}
	documents, err := OwnedDocuments(root, product)
	if err != nil {
		t.Fatalf("OwnedDocuments() error = %v", err)
	}
	var paths []string
	for _, document := range documents {
		paths = append(paths, document.ID+"="+document.Path)
	}
	slices.Sort(paths)
	want := []string{"malformed-design=docs/designs/malformed-design.md", "slack-reporting-design=docs/designs/slack-reporting-design.md"}
	if !slices.Equal(paths, want) {
		t.Fatalf("OwnedDocuments() = %v, want %v", paths, want)
	}
	// And a done-condition naming the malformed one by id is refused like any
	// other: its frontmatter being wrong makes it no more a run's to write.
	homes := ArtifactHomes(config.Config{Product: product}, documents...)
	if conditions := homes.Ungranted("Done means the malformed design records the rule.", "", nil); len(conditions) != 1 {
		t.Fatalf("Ungranted() naming a refused document = %#v, want it refused", conditions)
	}
}

// yoyodyne-ifd.330, as the product manager admitted it at turn 468: the
// architect's design work by its title and by its done-means, naming no
// document, and naming no executor. Nothing refused it, it read as ready, and
// the developer run it was handed could only report that the design had
// already landed. Read now, both the title and the done-condition say whose
// work it is, and an item that says so without the marker is refused with the
// marker named — and admitted as it stands once the marker is on it.
func TestConversationShapedWorkWithoutAnExecutorIsRefusedWithTheMarkerNamed(t *testing.T) {
	t.Parallel()

	item330 := Subject{
		Title:       "The architect designs side conversations with merge-back",
		Description: "Operator capability direction, 2026-09-07, design routed to the architect as directed: a persona can hold a side conversation concurrent with its main thread. Done means the design is recorded in the governed documents - the stream shape, the merge write, the action-authority answer, the config knob - and implementation items can cite it.",
	}
	problems := defaultHomes().ConditionProblems(item330)
	if len(problems) != 2 {
		t.Fatalf("ConditionProblems() on 330 = %v, want the done-condition and the title each refused", problems)
	}
	for _, problem := range problems {
		for _, want := range []string{`executor "conversation:architect"`, "never selected for a developer run", "closed by the harness once a revision"} {
			if !strings.Contains(problem.Error(), want) {
				t.Fatalf("refusal %q never says %q", problem, want)
			}
		}
	}
	if !strings.Contains(problems[0].Error(), "the design is recorded in the governed documents") {
		t.Fatalf("refusal %q never quotes the clause", problems[0])
	}
	if !strings.Contains(problems[1].Error(), "its title says the architect does the work") {
		t.Fatalf("refusal %q never names the title", problems[1])
	}

	// Marked, the same item is that conversation's work stated correctly.
	item330.Executor = domain.ConversationWith(domain.RoleArchitect)
	if problems := defaultHomes().ConditionProblems(item330); len(problems) != 0 {
		t.Fatalf("ConditionProblems() on 330 marked = %v, want nothing", problems)
	}

	// Each reading fires on its own: a ruling recorded with a plain title, and
	// an architect title with a done-means naming nothing of the kind.
	for _, shape := range []Subject{
		{Title: "Slack renders role prose", Description: "Done means her ruling is recorded on the design, the sink renders role prose per it, and the token never reaches an agent process."},
		{Title: "The architect ratifies the portable-configuration baseline", Description: "Done means the manual's claims match the baseline."},
		{Title: "The architect rules on the release push", AcceptanceCriteria: "The ruling is recorded where the release machinery can cite it."},
	} {
		if problems := defaultHomes().ConditionProblems(shape); len(problems) == 0 {
			t.Fatalf("ConditionProblems() on %q = nothing, want the shape refused", shape.Title)
		}
	}
}

// The readings are narrow on purpose: "decision" is what triage records on an
// item, "the design" is cited by nearly every developer item, and "The
// architect's" opens a developer item about the ruling rather than one the
// architect does. None of these is conversation work, and none is refused.
func TestOrdinaryDeveloperWorkIsNotReadAsConversationWork(t *testing.T) {
	t.Parallel()

	for _, shape := range []Subject{
		{Title: "Stopped work reaches the development manager", Description: "Done means stopped work reaches the development manager, its decision is recorded on the item, and the docket entry is settled."},
		{Title: "The architect's ruling is enforced by the gate", Description: "Done means the gate refuses what the design forbids, with a test pinning it."},
		{Title: "The architect loop: windowed drift review", Description: "Done means the loop runs on the recurring-tasks machinery."},
		{Title: "Add the capacity-blocked state", Description: "The design says lower-level gates are configurable. Done means the read model exposes the state and the design is cited in the test."},
		{Title: "Retire the design note", Description: "Done means the design is retired from the manual's table of contents."},
	} {
		if problems := defaultHomes().ConditionProblems(shape); len(problems) != 0 {
			t.Fatalf("ConditionProblems() on %q = %v, want nothing", shape.Title, problems)
		}
	}
	// A grant under a home is somebody's decision that a run writes there, so
	// the recorded ruling is the run's work and not the architect's.
	granted := Subject{
		Title:       "Record the ruling the operator approved",
		Description: "Done means the ruling is recorded on the slack-reporting design as the architect's revision.\n\n" + GrantMarker + " docs/designs/slack-reporting-design.md\n",
	}
	granted.Granted = Grants(granted.Description)
	if problems := defaultHomes().ConditionProblems(granted); len(problems) != 0 {
		t.Fatalf("ConditionProblems() on a granted recording = %v, want nothing", problems)
	}
	// And a project with no home the architect writes reads nothing as the
	// architect's: there is nowhere such work could land.
	productOnly := ArtifactHomes(config.Config{Product: config.Product{Specifications: config.DefaultSpecifications}})
	if problems := productOnly.ConditionProblems(Subject{Title: "The architect designs the thing", Description: "Done means the design is recorded."}); len(problems) != 0 {
		t.Fatalf("ConditionProblems() with no architect home = %v, want nothing", problems)
	}
}

// An item a conversation carries is judged as that conversation's work. A
// done-condition naming a document its role owns is admitted — that is the
// condition stated correctly, and what the landing is later read from — and one
// naming another role's document is refused as that role's, without the marker
// offered as the fix, because the item already carries one.
func TestAConversationsItemMayNameTheDocumentsItsRoleOwns(t *testing.T) {
	t.Parallel()

	ruling := Subject{
		Title:       "The architect rules on agent voices in threads",
		Description: "Done means the ruling is recorded on the slack-reporting design as her revision, and the implementation can cite it.",
		Executor:    domain.ConversationWith(domain.RoleArchitect),
	}
	if problems := defaultHomes().ConditionProblems(ruling); len(problems) != 0 {
		t.Fatalf("ConditionProblems() on the architect's own document = %v, want nothing", problems)
	}
	brief := Subject{
		Title:       "The architect rules on the brief",
		Description: "Done means docs/product/brief.md states the new scope.",
		Executor:    domain.ConversationWith(domain.RoleArchitect),
	}
	problems := defaultHomes().ConditionProblems(brief)
	if len(problems) != 1 {
		t.Fatalf("ConditionProblems() on another role's document = %v, want the one condition refused", problems)
	}
	for _, want := range []string{"docs/product/brief.md", "the product-manager's to write", "not the architect conversation's"} {
		if !strings.Contains(problems[0].Error(), want) {
			t.Fatalf("refusal %q never says %q", problems[0], want)
		}
	}
	if strings.Contains(problems[0].Error(), "mark the item") {
		t.Fatalf("refusal %q offers the marker to an item that carries one", problems[0])
	}
	// The product manager's conversation is judged the same way round.
	scope := Subject{
		Title:       "Scope team mode",
		Description: "Done means the document is on disk in docs/product, the operator has approved it, and the design child can cite it.",
		Executor:    domain.ConversationWith(domain.RoleProductManager),
	}
	if problems := defaultHomes().ConditionProblems(scope); len(problems) != 0 {
		t.Fatalf("ConditionProblems() on the product manager's own home = %v, want nothing", problems)
	}
	// An executor the harness does not recognize is judged as a developer run,
	// which is the direction that refuses rather than the one that waves through.
	unknown := ruling
	unknown.Executor = "conversation"
	if problems := defaultHomes().ConditionProblems(unknown); len(problems) == 0 {
		t.Fatal("ConditionProblems() with a bare conversation marker = nothing, want it judged as a run")
	}
}

// A clause that both names a document and reads as conversation work is one
// refusal, not two: the document refusal already names the marker among its
// fixes, and the same clause quoted twice is one instruction too many.
func TestAClauseRefusedForItsDocumentIsNotRefusedAgainForItsShape(t *testing.T) {
	t.Parallel()

	problems := defaultHomes().ConditionProblems(Subject{
		Title:       "Slack renders role prose",
		Description: "Done means her ruling is recorded on the slack-reporting design, and the sink renders role prose per it.",
	})
	if len(problems) != 1 {
		t.Fatalf("ConditionProblems() = %v, want the one clause refused once", problems)
	}
	for _, want := range []string{"docs/designs/slack-reporting-design.md", `executor "conversation:architect"`, GrantMarker} {
		if !strings.Contains(problems[0].Error(), want) {
			t.Fatalf("refusal %q never says %q", problems[0], want)
		}
	}
}

// docs/designs/program-manager.md is the design of the role whose name is its
// id, and the role is written in full on every surface, so a done-condition
// about the role says the design's id word for word. Such an id is the role
// unless the clause names the path or names it as a document; yoyodyne-ifd.432.11
// was refused dispatch on the role, and yoyodyne-ifd.433.5's own first admission
// on saying the role's name.
func TestAnIDThatIsARolesNameIsTheRoleUnlessTheClauseNamesTheDocument(t *testing.T) {
	t.Parallel()

	homes := ArtifactHomes(config.Config{Product: config.Product{
		Specifications: config.DefaultSpecifications,
		Designs:        config.DefaultDesigns,
		Decisions:      config.DefaultDecisions,
		Invariants:     config.DefaultInvariants,
	}}, Document{ID: "program-manager", Path: "docs/designs/program-manager.md"})

	for _, role := range []string{
		// yoyodyne-ifd.432.11's done-means as it was refused dispatch.
		"Done means: the standing lists each configured program manager with its name, its lane, and its status; the section's empty state says in a sentence that no program manager is configured; and docs/operations.md describes the section.",
		// yoyodyne-ifd.432.11's done-means as the tracker holds it now.
		"Done means: the standing the read model derives lists each configured instance of the sixth role with its name, its lane, its status as the design derives it, and where its current report is; the section has the four states every section has, with its empty state saying in a sentence that no instance of that role is configured.",
		"Done means the program-manager lane report is rewritten each pass, and a program manager's blocked status names its blocker.",
		"Done means a Program Manager admits only under its own label.",
		// A file of the design's name, written without the home's path, is a file
		// of that name somewhere else.
		"Done means program-manager.md in the harness's builtin personas says where the report lives.",
	} {
		if conditions := homes.Ungranted(role, "", nil); len(conditions) != 0 {
			t.Errorf("Ungranted(%q) = %#v, want the role read as the role", role, conditions)
		}
		if problems := homes.ConditionProblems(Subject{Description: role}); len(problems) != 0 {
			t.Errorf("ConditionProblems(%q) = %v, want the item admitted", role, problems)
		}
	}

	for _, document := range []struct {
		clause string
		named  string
	}{
		{"Done means the program manager design lists the lane report's endpoint.", "program manager"},
		{"Done means the program-manager design's status table names stale.", "program-manager"},
		{"Done means the program manager's design names stale.", "program manager"},
		{"Done means the program manager document says where the report lives.", "program manager"},
		{"Done means the design program-manager says where the report lives.", "program-manager"},
		{"Done means docs/designs/program-manager.md says where the report lives.", "docs/designs/program-manager.md"},
	} {
		conditions := homes.Ungranted(document.clause, "", nil)
		if len(conditions) != 1 || conditions[0].Path != "docs/designs/program-manager.md" || conditions[0].Named != document.named {
			t.Errorf("Ungranted(%q) = %#v, want the design named as %q", document.clause, conditions, document.named)
		}
	}

	// A done-condition saying a design is recorded is conversation work whatever
	// it names, and an item carrying it with no executor is refused as before.
	recorded := "Done means the program manager's lane rule is recorded; the design is ratified by the architect."
	problems := homes.ConditionProblems(Subject{Description: recorded})
	if len(problems) != 1 || !strings.Contains(problems[0].Error(), "a design or a ruling is recorded") {
		t.Fatalf("ConditionProblems(%q) = %v, want the recorded design refused for naming no executor", recorded, problems)
	}

	// An id that ends in a document word says it alone: slack-reporting-design
	// written as "the slack-reporting design" is already "the <name> design", so
	// nothing more beside it is needed.
	if conditions := defaultHomes().Ungranted("Done means the slack-reporting design names the sink.", "", nil); len(conditions) != 1 {
		t.Fatalf("Ungranted() on an id ending in a document word = %#v, want the document", conditions)
	}
}

// The over-matches reported against this check, each as its done-condition was
// written, and the references to the same documents it still refuses. A design's
// id used as words inside a phrase about something else is the words; the
// document is named by its path or by a phrase whose subject is the document.
func TestADesignsNameUsedAsOrdinaryWordsIsNotACondition(t *testing.T) {
	t.Parallel()

	homes := ArtifactHomes(config.Config{Product: config.Product{
		Specifications: config.DefaultSpecifications,
		Designs:        config.DefaultDesigns,
		Decisions:      config.DefaultDecisions,
		Invariants:     config.DefaultInvariants,
	}},
		Document{ID: "machine-home", Path: "docs/designs/machine-home.md"},
		Document{ID: "pinned-outside-files", Path: "docs/designs/pinned-outside-files.md"},
		Document{ID: "developer-sub-agents", Path: "docs/designs/developer-sub-agents.md"},
	)

	for _, clause := range []string{
		// The companion intent repository work (yoyodyne-ifd.78.1): a folder.
		"Done means the intent repository is created in the machine-home project directory and yoyo doctor reports it.",
		// The documentation fix: a section of another file.
		"Done means: The pinned outside files section of docs/configuration.md says what is pinned.",
		// A source file the harness builds in, sharing the design's name.
		"Done means developer-sub-agents.md is embedded in the harness and the developer contract reads it.",
		"Done means internal/config/builtin/developer-sub-agents.md is embedded in the harness.",
		// The document-landing update (yoyodyne-fp3): the homes listed as the rule
		// for which changes take the route, not as places the run writes.
		"Done means a change whose every path is under docs/product, docs/designs, docs/decisions, or docs/decisions/invariants/ takes the document route.",
	} {
		if conditions := homes.Ungranted(clause, "", nil); len(conditions) != 0 {
			t.Errorf("Ungranted(%q) = %#v, want nothing", clause, conditions)
		}
		// Admission and the start of a run ask the same predicate.
		if problems := homes.ConditionProblems(Subject{Description: clause}); len(problems) != 0 {
			t.Errorf("ConditionProblems(%q) = %v, want the item admitted", clause, problems)
		}
		if problems := homes.ConditionProblems(Subject{AcceptanceCriteria: clause}); len(problems) != 0 {
			t.Errorf("ConditionProblems() on acceptance criteria %q = %v, want the item admitted", clause, problems)
		}
	}

	for _, document := range []struct {
		clause string
		path   string
	}{
		{"Done means the machine-home design says where the intent repository lives.", "docs/designs/machine-home.md"},
		{"Done means the machine-home design document says where the intent repository lives.", "docs/designs/machine-home.md"},
		{"Done means the machine home design's layout lists intent/.", "docs/designs/machine-home.md"},
		{"Done means docs/designs/machine-home.md lists intent/.", "docs/designs/machine-home.md"},
		{"Done means the pinned-outside-files design names the new file.", "docs/designs/pinned-outside-files.md"},
		{"Done means docs/designs/developer-sub-agents.md describes the sub-agents.", "docs/designs/developer-sub-agents.md"},
		{"Done means the developer sub-agents design describes them.", "docs/designs/developer-sub-agents.md"},
	} {
		conditions := homes.Ungranted(document.clause, "", nil)
		if len(conditions) != 1 || conditions[0].Path != document.path {
			t.Errorf("Ungranted(%q) = %#v, want %s refused", document.clause, conditions, document.path)
		}
		if problems := homes.ConditionProblems(Subject{AcceptanceCriteria: document.clause}); len(problems) != 1 {
			t.Errorf("ConditionProblems() on acceptance criteria %q = %v, want it refused", document.clause, problems)
		}
	}
}

// The product's own documents are titled by their kind — v1-goals,
// operating-rules — so "the v1 goals" and "the operating rules" name them as
// surely as "the slack-reporting design" names that design, and a done-condition
// asking a run to change one is still refused wherever it is written. The same
// words beside an id, on either side, name the document too.
func TestTheProductDocumentsAreNamedByTheirKind(t *testing.T) {
	t.Parallel()

	homes := ArtifactHomes(config.Config{Product: config.Product{
		Specifications: config.DefaultSpecifications,
		Designs:        config.DefaultDesigns,
		Decisions:      config.DefaultDecisions,
		Invariants:     config.DefaultInvariants,
	}},
		Document{ID: "v1-goals", Path: "docs/product/goals/v1-goals.md"},
		Document{ID: "v1-non-goals", Path: "docs/product/goals/v1-non-goals.md"},
		Document{ID: "operating-rules", Path: "docs/product/operating-rules.md"},
		Document{ID: "merge-affinity", Path: "docs/decisions/merge-affinity.md"},
	)

	for _, document := range []struct {
		clause string
		path   string
	}{
		{"Done means the v1 goals state the new goal.", "docs/product/goals/v1-goals.md"},
		{"Done means the V1-Goals gain a standing goal.", "docs/product/goals/v1-goals.md"},
		{"Done means the v1 non-goals no longer exclude the dashboard.", "docs/product/goals/v1-non-goals.md"},
		{"Done means the operating rules say the seat is dedicated.", "docs/product/operating-rules.md"},
		{"Done means the merge-affinity decision record names the lease.", "docs/decisions/merge-affinity.md"},
		{"Done means the decision record merge-affinity names the lease.", "docs/decisions/merge-affinity.md"},
		{"Done means the merge affinity rules name the lease.", "docs/decisions/merge-affinity.md"},
	} {
		conditions := homes.Ungranted(document.clause, "", nil)
		if len(conditions) != 1 || conditions[0].Path != document.path {
			t.Errorf("Ungranted(%q) = %#v, want %s refused", document.clause, conditions, document.path)
		}
		if problems := homes.ConditionProblems(Subject{Description: document.clause}); len(problems) != 1 {
			t.Errorf("ConditionProblems(%q) = %v, want it refused", document.clause, problems)
		}
		if problems := homes.ConditionProblems(Subject{AcceptanceCriteria: document.clause}); len(problems) != 1 {
			t.Errorf("ConditionProblems() on acceptance criteria %q = %v, want it refused", document.clause, problems)
		}
	}

	// A single goal or rule is not the document: "the merge affinity rule" is
	// what a gate enforces, as often as it is a decision record.
	if conditions := homes.Ungranted("Done means the merge affinity rule holds under load.", "", nil); len(conditions) != 0 {
		t.Errorf("Ungranted() on a singular rule = %#v, want nothing", conditions)
	}
}
