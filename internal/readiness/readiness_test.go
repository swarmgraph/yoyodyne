package readiness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// tree writes a small repository and returns the Tree a pull would read it
// through. It is a real checkout rather than a stub for the reads that matter
// here: what this package is for is answering from files, and a fake that
// answers from a map would be testing the fake. The standard library it is
// read against is a small Go installation of the test's own, so what the
// machine running the test has installed changes no answer here.
func tree(t *testing.T, files map[string]string) *Repository {
	t.Helper()
	root := t.TempDir()
	for path, content := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("make %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return &Repository{Root: root, GoRoot: goInstallation(t, standardAPI)}
}

func kindsOf(unmet []Unmet) []string {
	found := make([]string, 0, len(unmet))
	for _, one := range unmet {
		found = append(found, string(one.Kind))
	}
	return found
}

func check(t *testing.T, item beads.WorkItem, repository *Repository) []Unmet {
	t.Helper()
	unmet, err := Check(item, repository)
	if err != nil {
		t.Fatalf("check %s: %v", item.ID, err)
	}
	return unmet
}

// The yoyodyne-ifd.291 shape: an item admitted citing a symbol that a commit
// weeks earlier deleted, for a gap another item had already closed. The tree
// still has a type called Backend and still has a method called SupportsRole,
// which is exactly why a reading of either half alone would call the citation
// live.
func TestAPinpointNamingCodeTheTreeNoLongerHasIsUnmet(t *testing.T) {
	repository := tree(t, map[string]string{
		"internal/domain/types.go": "package domain\n\ntype Backend string\n\nconst BackendClaudeCode Backend = \"claude-code\"\n",
		"internal/backend/registry.go": "package backend\n\ntype Descriptor struct{ Roles []string }\n\n" +
			"func (d Descriptor) SupportsRole(role string) bool { return false }\n",
	})
	item := beads.WorkItem{
		ID:    "yoyodyne-ifd.291",
		Title: "Configuration fails closed on an unrecognized role",
		Description: "The implementation pinpoint: domain.Backend.SupportsRole returns true for every role " +
			"on claude-code, so the only role check is the five-role list.",
	}

	unmet := check(t, item, repository)

	if len(unmet) != 1 || unmet[0].Kind != KindStalePinpoint {
		t.Fatalf("want one stale pinpoint, got %v", kindsOf(unmet))
	}
	if !strings.Contains(unmet[0].Missing, "domain.Backend.SupportsRole") {
		t.Errorf("the refusal must name what is missing, got %q", unmet[0].Missing)
	}
	if unmet[0].Decides == "" || unmet[0].Evidence == "" {
		t.Errorf("the refusal must say who decides and what read says so, got %+v", unmet[0])
	}
}

// The same symbol once the code is where the item says it is. This is the half
// that has to hold for the guard to be worth having: a citation the tree meets
// must not hold the item back, and it clears without anybody editing anything.
func TestAPinpointTheTreeStillHasIsMet(t *testing.T) {
	repository := tree(t, map[string]string{
		"internal/domain/types.go": "package domain\n\ntype Backend struct{}\n\n" +
			"func (b Backend) SupportsRole(role string) bool { return true }\n",
	})
	item := beads.WorkItem{
		ID:          "yoyodyne-ifd.291",
		Description: "domain.Backend.SupportsRole returns true for every role.",
	}

	if unmet := check(t, item, repository); len(unmet) != 0 {
		t.Fatalf("want nothing unmet, got %v", kindsOf(unmet))
	}
}

// A method is never written package-qualified in Go — it is called on a value —
// so the qualified form an item uses in prose is absent from the source of every
// tree, including the ones that have the method. Reporting those would fire on
// eight items in this repository's own backlog that named a method somebody
// never renamed.
func TestAMethodNamedTheWayProseNamesItIsMet(t *testing.T) {
	repository := tree(t, map[string]string{
		"internal/gitworktree/manager.go": "package gitworktree\n\ntype Manager struct{}\n\n" +
			"func (m Manager) Create(request string) error { return nil }\n",
	})
	item := beads.WorkItem{ID: "item", Description: "gitworktree.Manager.Create cuts the worktree."}

	if unmet := check(t, item, repository); len(unmet) != 0 {
		t.Fatalf("want nothing unmet, got %v", kindsOf(unmet))
	}
}

// A `file:line` pinpoint has two ways to be stale and they are different facts
// to act on: the file is gone, or the file is there and shorter than the
// citation.
func TestAPathPinpointIsReadAgainstTheFile(t *testing.T) {
	repository := tree(t, map[string]string{"internal/config/config.go": "package config\n\nfunc Load() {}\n"})

	absent := check(t, beads.WorkItem{ID: "item", Description: "the list at internal/config/gone.go:12"}, repository)
	if len(absent) != 1 || absent[0].Kind != KindSubjectNotInRepository {
		t.Fatalf("want the missing file reported as an absent subject, got %v", kindsOf(absent))
	}

	past := check(t, beads.WorkItem{ID: "item", Description: "the list at internal/config/config.go:376"}, repository)
	if len(past) != 1 || past[0].Kind != KindStalePinpoint {
		t.Fatalf("want a line past the end reported as a stale pinpoint, got %v", kindsOf(past))
	}
	if !strings.Contains(past[0].Missing, "3 line(s)") {
		t.Errorf("the refusal must say what the tree has, got %q", past[0].Missing)
	}

	within := check(t, beads.WorkItem{ID: "item", Description: "the load at internal/config/config.go:3"}, repository)
	if len(within) != 0 {
		t.Fatalf("want a line the file has to be met, got %v", kindsOf(within))
	}
}

// A bare path is not a pinpoint. Three documentation items in this repository's
// backlog name the files they exist to create, and reading those as citations
// would refuse every one of them for the tree not yet holding their output.
func TestAPathTheItemAsksForIsNotAPinpoint(t *testing.T) {
	repository := tree(t, map[string]string{"docs/configuration.md": "# Configuration\n"})
	item := beads.WorkItem{
		ID:          "yoyodyne-ifd.117.1",
		Description: "Split the configuration reference: docs/configuration/setup.md and docs/configuration/agents.md.",
	}

	if unmet := check(t, item, repository); len(unmet) != 0 {
		t.Fatalf("want a path the item asks for to be met, got %v", kindsOf(unmet))
	}
}

// The yoyodyne-ifd.284 shape: an item whose first sentence names the work it
// inherits, where that work is whole and approved on a branch nothing has
// merged. Nothing in the tracker records the sequencing, so the queue reads the
// item as ready and a run is spent finding out that none of it compiles.
func TestAnItemThatSaysItInheritsUnlandedMachineryIsUnmet(t *testing.T) {
	repository := tree(t, map[string]string{"internal/orchestrator/schedule.go": "package orchestrator\n"})
	item := beads.WorkItem{
		ID:    "yoyodyne-ifd.284",
		Title: "The product manager's coherence scan: twice daily, contradictions surfaced, questions on top",
		Description: "Operator-directed, 2026-09-05, the second recurring-task consumer, shipping after the DM " +
			"hourly sweep and inheriting its machinery. Twice daily the harness wakes the product manager.",
	}

	unmet := check(t, item, repository)

	if len(unmet) != 1 || unmet[0].Kind != KindMachineryOnABranch {
		t.Fatalf("want the stated inheritance caught, got %v", kindsOf(unmet))
	}
	if !strings.Contains(unmet[0].Missing, "shipping after the DM") {
		t.Errorf("the refusal must quote the item's own sentence, got %q", unmet[0].Missing)
	}
}

// The yoyodyne-ifd.209.14 shape: an anchor item that states its own activation
// conditions in prose and was pulled into a developer run twice regardless, each
// time spending a run to rediscover that its subject is absent.
func TestAnItemThatStatesItsOwnActivationConditionsIsUnmet(t *testing.T) {
	repository := tree(t, map[string]string{"internal/workflow/execute.go": "package workflow\n"})
	item := beads.WorkItem{
		ID:    "yoyodyne-ifd.209.14",
		Title: "Convert the coordination slice to the workflow runtime at the management-requests conversion",
		Description: "Scope when it activates: re-express the coordination slice's workflow on the declarative " +
			"runtime. Not decomposable further until the runtime exists and the management-conversion design lands; " +
			"this item is the durable anchor.",
	}

	unmet := check(t, item, repository)

	if len(unmet) != 2 {
		t.Fatalf("want both stated conditions caught, got %v", kindsOf(unmet))
	}
	for _, one := range unmet {
		if one.Kind != KindSubjectNotInRepository {
			t.Errorf("want the subject reported as absent, got %q", one.Kind)
		}
	}
}

// The yoyodyne-ifd.100.1 shape: an item written blocked on a design answer that
// did not exist yet. The answer came and negated every one of its done
// conditions, and two runs delivered empty trees before a third wrote that down.
func TestAnItemThatSaysItIsGatedOnADecisionIsUnmet(t *testing.T) {
	repository := tree(t, map[string]string{"internal/artifact/artifact.go": "package artifact\n"})
	item := beads.WorkItem{
		ID:    "yoyodyne-ifd.100.1",
		Title: "Commit and publish an approved artifact write",
		Description: "Done means: after the operator's y, the harness commits the document and opens a pull " +
			"request. Blocked until the architect's answer exists; the answer is argued in the architect's " +
			"conversation and recorded by the operator.",
	}

	unmet := check(t, item, repository)

	if len(unmet) != 1 || unmet[0].Kind != KindForbiddenByRuling {
		t.Fatalf("want the stated gate caught once, got %v", kindsOf(unmet))
	}
	if !strings.Contains(unmet[0].Missing, "Blocked until the architect's answer exists") {
		t.Errorf("the refusal must quote the gate, got %q", unmet[0].Missing)
	}
}

// yoyodyne-ifd.298's product manager removed the sentence from the description;
// a copy in a field she does not rewrite would have gone on refusing the item.
// So every sentence is read out of its own field and the refusal names it, and a
// field her update cannot reach says that too.
func TestASentenceIsNamedWithTheFieldItIsIn(t *testing.T) {
	repository := tree(t, nil)
	for _, test := range []struct {
		item       beads.WorkItem
		field      string
		outOfReach bool
	}{
		{beads.WorkItem{ID: "a", Description: "It does not start before 282's design lands."}, FieldDescription, false},
		{beads.WorkItem{ID: "b", Title: "Blocked until the answer exists"}, FieldTitle, false},
		{beads.WorkItem{ID: "c", Design: "It does not start before 282's design lands."}, FieldDesignGuidance, true},
		{beads.WorkItem{ID: "d", AcceptanceCriteria: "Gated on the architect's ruling."}, FieldAcceptanceCriteria, true},
	} {
		unmet := check(t, test.item, repository)
		if len(unmet) != 1 {
			t.Fatalf("%s: want one sentence found, got %v", test.item.ID, kindsOf(unmet))
		}
		if unmet[0].Field != test.field || !strings.HasPrefix(unmet[0].Missing, "its "+test.field) {
			t.Fatalf("%s: unmet = %+v, want the refusal to name the %s", test.item.ID, unmet[0], test.field)
		}
		if said := strings.Contains(unmet[0].Missing, "the Lead Product Manager's update does not rewrite"); said != test.outOfReach {
			t.Fatalf("%s: missing = %q, want out of reach said only of a field her update cannot rewrite", test.item.ID, unmet[0].Missing)
		}
	}
}

// The reading is the item as handed in and nothing remembered: the same item
// with the sentence removed meets everything, which is what lets the next pull
// take it.
func TestARemovedSentenceNoLongerRefuses(t *testing.T) {
	repository := tree(t, nil)
	item := beads.WorkItem{ID: "yoyodyne-ifd.298", Description: "The reading surface. This item does not start before 282's design lands."}
	if unmet := check(t, item, repository); len(unmet) != 1 {
		t.Fatalf("want the sentence refused, got %v", kindsOf(unmet))
	}
	item.Description = "The reading surface."
	if unmet := check(t, item, repository); len(unmet) != 0 {
		t.Fatalf("want nothing unmet once the sentence is gone, got %v", kindsOf(unmet))
	}
}

// The ordinary item, which is nearly all of them: it cites what the tree has and
// states no gate, and the check says nothing about it at all.
func TestAnItemThatStatesNothingUnmetIsReady(t *testing.T) {
	repository := tree(t, map[string]string{"internal/backlog/backlog.go": "package backlog\n\nfunc Order() {}\n"})
	item := beads.WorkItem{
		ID:                 "yoyodyne-ifd.304",
		Title:              "Unmeetable items do not dispatch: readiness checks prerequisites against the tree",
		Description:        "Dispatch readiness checks the item's prerequisites against the tree as it stands, each a read instead of a run.",
		AcceptanceCriteria: "A failed readiness check routes the item to triage naming the unmet prerequisite, never to a run.",
	}

	if unmet := check(t, item, repository); len(unmet) != 0 {
		t.Fatalf("want an ordinary item to be ready, got %v", kindsOf(unmet))
	}
}

// The notes are where the harness appends each run's record and where a
// development manager writes the implementation plan, so they name the files the
// work will create and the types it will declare. Reading them would fire on 20
// of the 76 unfinished items this backlog held, nearly every one of them on its
// own output.
func TestTheNotesAreNotReadForPrerequisites(t *testing.T) {
	repository := tree(t, map[string]string{"internal/orchestrator/schedule.go": "package orchestrator\n"})
	item := beads.WorkItem{
		ID:    "item",
		Title: "Re-arm a merge the forge dropped",
		Notes: "Plan: add internal/orchestrator/rearm.go:352 and publish.AwaitsOnlyAPerson.\n" +
			"Run: run-9ad1799e\nChanges:\n?? internal/orchestrator/gone.go\n",
	}

	if unmet := check(t, item, repository); len(unmet) != 0 {
		t.Fatalf("want the notes left out of the reading, got %v", kindsOf(unmet))
	}
}

// One clause matched by two phrasings is one gate, not two things to settle.
func TestOneClauseIsReportedOnce(t *testing.T) {
	repository := tree(t, nil)
	item := beads.WorkItem{ID: "item", Description: "Blocked until the architect's answer exists."}

	if unmet := check(t, item, repository); len(unmet) != 1 {
		t.Fatalf("want one gate reported once, got %v", kindsOf(unmet))
	}
}

// A citation that walks out of the tree resolves to nothing rather than to the
// machine the harness is running on.
func TestACitationOutsideTheTreeIsAbsent(t *testing.T) {
	repository := tree(t, map[string]string{"internal/config/config.go": "package config\n"})

	unmet := check(t, beads.WorkItem{ID: "item", Description: "see ../../etc/passwd.go:1"}, repository)

	if len(unmet) != 1 || unmet[0].Kind != KindSubjectNotInRepository {
		t.Fatalf("want a citation outside the tree reported absent, got %v", kindsOf(unmet))
	}
}

// A reading that could not be made says so as a failure rather than as an item
// with nothing wrong with it. A caller that could not tell the two apart would
// dispatch everything the moment the repository became unreadable.
func TestATreeThatCannotBeReadIsAFailureRatherThanAReadyItem(t *testing.T) {
	unmet, err := Check(beads.WorkItem{ID: "item", Description: "domain.Backend.SupportsRole"}, &Repository{})
	if err == nil {
		t.Fatal("want a reading that could not be made to be reported")
	}
	if len(unmet) != 0 {
		t.Fatalf("want nothing claimed about the item, got %v", kindsOf(unmet))
	}
	if _, err := Check(beads.WorkItem{ID: "item"}, nil); err == nil {
		t.Fatal("want a check with no tree to be refused")
	}
}

// What is recorded about an unready item is keyed by the kinds it was found
// unready for, so the same finding twice is one record.
func TestKindsAreDeduplicatedAndOrdered(t *testing.T) {
	kinds := Kinds([]Unmet{
		{Kind: KindStalePinpoint}, {Kind: KindForbiddenByRuling}, {Kind: KindStalePinpoint},
	})

	if strings.Join(kinds, ",") != "forbidden-by-ruling,stale-pinpoint" {
		t.Fatalf("want the kinds deduplicated and ordered, got %v", kinds)
	}
	if Describe(nil) != "" {
		t.Error("want an item that meets everything to be described as nothing")
	}
}

// A description is bounded and stays one line, whatever the tracker holds.
func TestDescriptionIsBoundedAndOneLine(t *testing.T) {
	repository := tree(t, nil)
	item := beads.WorkItem{
		ID:          "item",
		Description: "Blocked until the architect's answer exists" + strings.Repeat(" and it goes on and on", 40) + ".",
	}

	described := Describe(check(t, item, repository))

	if strings.Contains(described, "\n") {
		t.Errorf("want one line, got %q", described)
	}
	if !strings.Contains(described, "...") {
		t.Errorf("want the clause bounded, got %q", described)
	}
}
