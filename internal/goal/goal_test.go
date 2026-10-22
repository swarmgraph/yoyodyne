package goal

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/governeddoc"
)

const goalsHome = "docs/product"

func TestTheGoalsAGoalsDocumentStatesAreWhatWorkCanBeAttributedTo(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// The shape Yoyodyne's own goals are in: an introduction, a `Goals` heading,
	// and one entry per goal with prose underneath saying what it supports.
	write(t, root, "docs/product/goals/v1-goals.md", `---
id: v1-goals
---

# V1 goals

These are the outcomes the first version is built to reach.

## Goals

- Maintain a traceable chain from the product brief through goals, designs, work, code changes, and verification.
  *Supports: every change traces to intent somebody approved.*
- Isolate implementation tasks in harness-managed Git worktrees.
  *Supports: intent goes in and merged software comes out.*
`)

	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	if len(set.Problems) != 0 {
		t.Fatalf("problems = %v", set.Problems)
	}
	if statements := stated(set.Goals); len(statements) != 2 {
		t.Fatalf("goals = %q", statements)
	}
	first := set.Goals[0]
	// The document that states a goal travels with it: knowing the words matched
	// is not knowing which document they were agreed in.
	if first.ArtifactID != "v1-goals" || first.Path != "docs/product/goals/v1-goals.md" || !first.InForce {
		t.Fatalf("goal = %#v", first)
	}
	// The prose under an entry describes the goal above it rather than being a
	// second goal.
	if strings.Contains(first.Statement, "Supports:") {
		t.Fatalf("goal statement swallowed the prose under it: %q", first.Statement)
	}
}

func TestAGoalIsResolvedThroughWordingThatDiffersOnlyInHowItWasTyped(t *testing.T) {
	t.Parallel()

	set := setWithGoals(t, "Maintain a traceable chain from the brief through to verification.")
	for _, named := range []string{
		"Maintain a traceable chain from the brief through to verification.",
		"maintain a traceable chain from the brief through to verification",
		"  Maintain a traceable chain   from the brief through to  verification  ",
	} {
		attribution := set.Attribute(named)
		if !attribution.Resolved() {
			t.Fatalf("Attribute(%q) = %#v", named, attribution)
		}
		if attribution.Goal.ArtifactID != "v1-goals" {
			t.Fatalf("Attribute(%q) resolved to %#v", named, attribution.Goal)
		}
	}
}

func TestAGoalNoDocumentStatesIsUnresolvedRatherThanApproximatelyRight(t *testing.T) {
	t.Parallel()

	set := setWithGoals(t, "Maintain a traceable chain from the brief through to verification.")
	// One word different is a different claim. Deciding it was near enough is
	// exactly the inference an attribution that resolves is supposed to replace.
	attribution := set.Attribute("Maintain a traceable chain from the brief through to review.")
	if attribution.State != StateUnresolved {
		t.Fatalf("attribution = %#v", attribution)
	}
	if !strings.Contains(attribution.Reason, "v1-goals") {
		t.Fatalf("reason does not say where it looked: %q", attribution.Reason)
	}
	if attribution.Named == "" {
		t.Fatalf("attribution does not carry what was named: %#v", attribution)
	}
}

func TestAGoalStatedOnlyByADocumentNoLongerInForceIsRefusedWithTheReasonNamed(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	write(t, root, "docs/product/goals/v0-goals.md", goalsDocument("Ship the prototype by the end of the quarter."))
	write(t, root, "docs/product/goals/v1-goals.md", goalsDocument("Maintain a traceable chain from the brief through to verification."))
	set := Collect(root, setOf(
		recorded("v0-goals", artifact.KindGoals, artifact.StatusSuperseded, "docs/product/goals/v0-goals.md"),
		recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md"),
	))

	attribution := set.Attribute("Ship the prototype by the end of the quarter.")
	// Not the same answer as a goal nobody ever wrote: this one was agreed and
	// then replaced, and whoever attributed work to it needs to be told which.
	if attribution.State != StateUnresolved {
		t.Fatalf("attribution = %#v", attribution)
	}
	if !strings.Contains(attribution.Reason, "v0-goals") || !strings.Contains(attribution.Reason, "no longer applies") {
		t.Fatalf("reason = %q", attribution.Reason)
	}
}

func TestAnAttributionIsUncheckableRatherThanWrongWhenThereAreNoGoalsToCheckIt(t *testing.T) {
	t.Parallel()

	// A repository that has not written its goals down yet, and one whose goals
	// could not be read, are different things to be told and neither of them is
	// "this attribution is wrong".
	empty := Set{}.Attribute("Maintain a traceable chain.")
	if empty.State != StateUncheckable || !strings.Contains(empty.Reason, "records no goals artifact") {
		t.Fatalf("attribution = %#v", empty)
	}
	unreadable := Unreadable("the artifact homes are outside the repository").Attribute("Maintain a traceable chain.")
	if unreadable.State != StateUncheckable || !strings.Contains(unreadable.Reason, "outside the repository") {
		t.Fatalf("attribution = %#v", unreadable)
	}
}

func TestAnItemThatRecordsNoGoalIsUnattributedRatherThanWrong(t *testing.T) {
	t.Parallel()

	set := setWithGoals(t, "Maintain a traceable chain from the brief through to verification.")
	attribution := set.AttributionOf("Admitted to the backlog by the product manager.\n\nReason: the operator asked for it.", Witness{})
	// Work admitted before attributions were checked says nothing, and nothing is
	// not a false claim. Telling the two apart is what lets legacy work be
	// grandfathered without also excusing an attribution that is wrong.
	if attribution.State != StateUnattributed {
		t.Fatalf("attribution = %#v", attribution)
	}
	if attribution.Named != "" {
		t.Fatalf("attribution names something the item did not: %#v", attribution)
	}
}

func TestAnItemWitnessedToHaveHadAGoalHasLostItRatherThanNeverHadOne(t *testing.T) {
	t.Parallel()

	statement := "Maintain a traceable chain from the brief through to verification."
	set := setWithGoals(t, statement)
	// The same notes an item has after something replaced them: the provenance
	// and the goal that were written at creation are gone, and what is left says
	// nothing about either. Read against the tracker's witness that a goal was
	// written here, that is a record destroyed rather than a record never made —
	// and it must not land in the one state that is deliberately not failed.
	replaced := "Constraints from the architect, recorded 2026-08-19."
	lost := set.AttributionOf(replaced, Witness{Recorded: true, Statement: statement})
	if lost.State != StateLost {
		t.Fatalf("attribution = %#v", lost)
	}
	if !strings.Contains(lost.Reason, "written over") {
		t.Fatalf("the reason does not say what happened: %#v", lost)
	}
	// The words the tracker kept are what a restoration puts back, so they are
	// carried rather than left for somebody to find: an item told only that it
	// lost a goal is an item somebody has to go and re-derive one for.
	if lost.Recorded != statement || !strings.Contains(lost.Reason, statement) {
		t.Fatalf("the lost attribution does not say which goal to put back: %#v", lost)
	}
	// It is still not an answer. The state is decided from the notes, so an item
	// whose notes lost their goal reads as lost however much the tracker
	// remembers — a loss that resolved out of the metadata would report as intact
	// while the item stayed empty.
	if lost.Resolved() || lost.Goal.Statement != "" {
		t.Fatalf("the witness answered for the item: %#v", lost)
	}
	// A witness from before the words were kept says what it knows and no more.
	bare := set.AttributionOf(replaced, Witness{Recorded: true})
	if bare.State != StateLost || bare.Recorded != "" {
		t.Fatalf("attribution = %#v", bare)
	}
	if !strings.Contains(bare.Reason, "does not hold which goal") {
		t.Fatalf("the reason claims a record the tracker does not have: %#v", bare)
	}
	// The same notes with no witness are the legacy item they look like, which is
	// what keeps the check from failing a backlog nobody has attributed yet.
	if unwitnessed := set.AttributionOf(replaced, Witness{}); unwitnessed.State != StateUnattributed {
		t.Fatalf("attribution = %#v", unwitnessed)
	}
	// A witness on an item that still carries its goal changes nothing: the goal
	// in the notes is the attribution, and the witness only ever says what was
	// written.
	kept := set.AttributionOf(replaced+"\n\n"+Note(statement), Witness{Recorded: true, Statement: statement})
	if !kept.Resolved() {
		t.Fatalf("attribution = %#v", kept)
	}
}

func TestTheNewestAttributionOnAnItemIsTheOneThatCounts(t *testing.T) {
	t.Parallel()

	set := setWithGoals(t, "Maintain a traceable chain from the brief through to verification.")
	// An item acquires an attribution by having one appended to notes nothing
	// rewrites, so the record holds both the wrong one and the correction.
	notes := strings.Join([]string{
		"Admitted to the backlog by the product manager.",
		"",
		Note("Ship the prototype by the end of the quarter."),
		"",
		"Attributed to a goal by the product manager.",
		"",
		Note("Maintain a traceable chain from the brief through to verification."),
	}, "\n")
	attribution := set.AttributionOf(notes, Witness{Recorded: true})
	if !attribution.Resolved() {
		t.Fatalf("attribution = %#v", attribution)
	}
	if attribution.Named != "Maintain a traceable chain from the brief through to verification." {
		t.Fatalf("attribution = %#v", attribution)
	}
}

func TestWhatIsWrittenAsAnAttributionIsWhatIsReadBack(t *testing.T) {
	t.Parallel()

	statement := "Maintain a traceable chain from the brief through to verification."
	named, found := NamedIn("some provenance\n\n" + Note(statement))
	if !found || named != statement {
		t.Fatalf("NamedIn() = %q, %v", named, found)
	}
}

// The whole of what identity buys, in one test: the goals document is reworded,
// and the work attributed under the old wording is still attributed. Three
// amendments broke this before identity existed — the last one refused four
// admissions at intake, and the operator found out by watching them fail.
func TestAGoalRewordedKeepsTheWorkAttributedToItAndTheAdmissionsNamingIt(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	write(t, root, "docs/product/goals/v1-goals.md",
		goalsDocument("[configurability] Keep roles, policies, and provider selection configurable without making safety invariants optional."))
	artifacts := setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md"))

	// The item is attributed as the harness attributes it: by the goal's identity,
	// with the words the document stated at the time carried along for reading.
	before := Collect(root, artifacts)
	note := before.NoteFor("Keep roles, policies, and provider selection configurable without making safety invariants optional.")
	if !strings.Contains(note, "[configurability]") {
		t.Fatalf("the attribution written does not name the goal's identity: %q", note)
	}
	named, records := NamedIn("Admitted by the product manager.\n\n" + note)
	if !records {
		t.Fatalf("NamedIn() found no attribution in %q", note)
	}

	// The operator directs a re-wording, and the product manager amends the
	// sentence. The identity is untouched, because it is not part of the sentence.
	write(t, root, "docs/product/goals/v1-goals.md",
		goalsDocument("[configurability] Keep roles, policies, and provider selection configurable without making safety invariants optional, and support multiple providers behind one adapter contract."))
	after := Collect(root, artifacts)

	attribution := after.Attribute(named)
	if !attribution.Resolved() {
		t.Fatalf("a re-wording orphaned the item: %#v", attribution)
	}
	if attribution.Identity != "configurability" {
		t.Fatalf("the attribution did not match on the identity: %#v", attribution)
	}
	// What the item resolves to is the goal as it now reads, so a reader is shown
	// the current sentence rather than the copy the item happens to carry.
	if !strings.Contains(attribution.Goal.Statement, "multiple providers") {
		t.Fatalf("the resolved goal is not the goal as it now reads: %#v", attribution.Goal)
	}
	// The other half of the same failure: an admission quoting the goal as it now
	// reads is not refused either, whichever wording the person naming it used.
	for _, quoted := range []string{
		"[configurability] Keep roles, policies, and provider selection configurable without making safety invariants optional, and support multiple providers behind one adapter contract.",
		"[configurability]",
		"[Configurability] whatever wording the caller happened to have",
	} {
		if admitted := after.Attribute(quoted); !admitted.Resolved() {
			t.Fatalf("Attribute(%q) = %#v", quoted, admitted)
		}
	}
}

// An attribution that names an identity is judged on the identity and never on
// the words beside it, and one that names no identity is judged on the words —
// which is the older arrangement, still reachable, and still what a re-wording
// breaks.
func TestAnAttributionNamingAnIdentityIsJudgedOnItRatherThanOnTheWordsBesideIt(t *testing.T) {
	t.Parallel()

	set := setWithGoals(t, "[traceable-chain] Maintain a traceable chain from the brief through to verification.")

	// The statement carries no identifier: it is the goal's words, and the
	// identifier is a thing the goal has rather than something it says.
	if set.Goals[0].Statement != "Maintain a traceable chain from the brief through to verification." {
		t.Fatalf("the identifier was read as part of the goal: %#v", set.Goals[0])
	}
	if set.Goals[0].Identity != "traceable-chain" {
		t.Fatalf("goal = %#v", set.Goals[0])
	}
	if reference := set.Goals[0].Reference(); reference != "[traceable-chain] Maintain a traceable chain from the brief through to verification." {
		t.Fatalf("Reference() = %q", reference)
	}

	// Named by identity: resolved, and reported as having matched on it.
	byIdentity := set.Attribute("[traceable-chain] a wording nobody ever wrote")
	if !byIdentity.Resolved() || byIdentity.Identity != "traceable-chain" || byIdentity.ResolvedByWording() {
		t.Fatalf("attribution = %#v", byIdentity)
	}
	// Named by the words alone: resolved too, and reported as the thing the next
	// amendment orphans.
	byWording := set.Attribute("Maintain a traceable chain from the brief through to verification.")
	if !byWording.Resolved() || byWording.Identity != "" || !byWording.ResolvedByWording() {
		t.Fatalf("attribution = %#v", byWording)
	}
	// An identity no document states is a claim that is wrong, and it is wrong
	// about the identity rather than about the words: falling back to the wording
	// would be the prose key coming back in through the failure path.
	unknown := set.Attribute("[traceability] Maintain a traceable chain from the brief through to verification.")
	if unknown.State != StateUnresolved {
		t.Fatalf("attribution = %#v", unknown)
	}
	if !strings.Contains(unknown.Reason, "traceability") || !strings.Contains(unknown.Reason, "v1-goals") {
		t.Fatalf("reason = %q", unknown.Reason)
	}
}

// Square brackets are only an identifier where they hold one. Everything else a
// goal might legitimately open with is prose, and reading it as an identifier
// would make a goal unnameable by its own words.
func TestBracketsThatHoldNoIdentifierAreReadAsTheProseTheyAre(t *testing.T) {
	t.Parallel()

	for _, opening := range []string{
		"[not an identifier] the goal",
		"[docs/product/brief.md] the goal",
		"[] the goal",
		"[" + strings.Repeat("a", MaxIdentityBytes+1) + "] the goal",
	} {
		if identity, statement := SplitIdentity(opening); identity != "" || statement != opening {
			t.Fatalf("SplitIdentity(%q) = %q, %q", opening, identity, statement)
		}
	}
	// Case is how it was typed rather than which goal is meant, and an item may
	// name the identifier and nothing else.
	if identity, statement := SplitIdentity("[Traceable-Chain]"); identity != "traceable-chain" || statement != "" {
		t.Fatalf("SplitIdentity() = %q, %q", identity, statement)
	}
}

// An identity is assigned once and never reused, so two goals in force carrying
// one is a document defect. It is reported beside a set that still holds both
// goals, and work naming it is refused rather than given to whichever document
// the store happened to read first.
func TestAnIdentityTwoGoalsInForceCarryIsReportedAndNamesNoGoal(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	write(t, root, "docs/product/goals/v1-goals.md", goalsDocument("[cost] The operator can see what the harness spends."))
	write(t, root, "docs/product/goals/operations-goals.md", goalsDocument("[cost] A deploy happens only with the operator's approval."))
	set := Collect(root, setOf(
		recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md"),
		recorded("operations-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/operations-goals.md"),
	))

	if len(set.Goals) != 2 {
		t.Fatalf("a goal was dropped over an identity two documents state: %#v", set.Goals)
	}
	if len(set.IdentityProblems) != 1 || set.IdentityProblems[0].Identity != "cost" {
		t.Fatalf("identity problems = %#v", set.IdentityProblems)
	}
	// What has to be fixed is a pair of files, so the report names both.
	for _, document := range []string{"v1-goals", "operations-goals"} {
		if !strings.Contains(set.IdentityProblems[0].Reason, document) {
			t.Fatalf("reason = %q, want it to name %q", set.IdentityProblems[0].Reason, document)
		}
	}
	attribution := set.Attribute("[cost] The operator can see what the harness spends.")
	if attribution.State != StateUnresolved || !strings.Contains(attribution.Reason, "more than one active goal") {
		t.Fatalf("attribution = %#v", attribution)
	}
	// A goal stated twice by documents that have not both got the identity is not
	// this: the same identity in a superseded document is a goal carried forward,
	// and holding that against the goal in force would report every replacement.
	replaced := Collect(root, setOf(
		recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md"),
		recorded("operations-goals", artifact.KindGoals, artifact.StatusSuperseded, "docs/product/goals/operations-goals.md"),
	))
	if len(replaced.IdentityProblems) != 0 {
		t.Fatalf("identity problems = %#v", replaced.IdentityProblems)
	}
	if carried := replaced.Attribute("[cost] anything"); !carried.Resolved() || carried.Goal.ArtifactID != "v1-goals" {
		t.Fatalf("attribution = %#v", carried)
	}
}

// An identity stated only by a document that was replaced is told apart from one
// nobody ever wrote, for the same reason the wording is: whoever attributed work
// to it needs to know which.
func TestAnIdentityOnlyAReplacedDocumentCarriesIsRefusedWithTheReasonNamed(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	write(t, root, "docs/product/goals/v0-goals.md", goalsDocument("[prototype] Ship the prototype by the end of the quarter."))
	write(t, root, "docs/product/goals/v1-goals.md", goalsDocument("[traceable-chain] Maintain a traceable chain from the brief through to verification."))
	set := Collect(root, setOf(
		recorded("v0-goals", artifact.KindGoals, artifact.StatusSuperseded, "docs/product/goals/v0-goals.md"),
		recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md"),
	))

	attribution := set.Attribute("[prototype] Ship the prototype by the end of the quarter.")
	if attribution.State != StateUnresolved {
		t.Fatalf("attribution = %#v", attribution)
	}
	if !strings.Contains(attribution.Reason, "v0-goals") || !strings.Contains(attribution.Reason, "no longer applies") {
		t.Fatalf("reason = %q", attribution.Reason)
	}
}

// What the harness writes onto an item is the goal named by the thing that does
// not move. Nothing is invented: a name that resolves to nothing, and a goal
// whose document states no identifier, are written down as they were given.
func TestTheAttributionWrittenNamesTheIdentityWhereTheGoalHasOneAndTheWordsWhereItDoesNot(t *testing.T) {
	t.Parallel()

	set := setWithGoals(t,
		"[traceable-chain] Maintain a traceable chain from the brief through to verification.",
		"Isolate implementation tasks in harness-managed worktrees.",
	)
	for named, want := range map[string]string{
		"Maintain a traceable chain from the brief through to verification.": Note("[traceable-chain] Maintain a traceable chain from the brief through to verification."),
		"[traceable-chain]": Note("[traceable-chain] Maintain a traceable chain from the brief through to verification."),
		"Isolate implementation tasks in harness-managed worktrees.": Note("Isolate implementation tasks in harness-managed worktrees."),
		"A goal nobody wrote.": Note("A goal nobody wrote."),
	} {
		if got := set.NoteFor(named); got != want {
			t.Fatalf("NoteFor(%q) = %q, want %q", named, got, want)
		}
	}
}

func TestADocumentThatStatesNoGoalsIsReportedRatherThanReadAsFewerGoals(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// A non-goals document states its content under its own heading. It is a
	// goals artifact by kind in no repository that files it correctly, but a
	// document whose goals cannot be found must be named either way: a set that
	// silently shrank is a set that starts refusing correct attributions.
	write(t, root, "docs/product/goals/v1-goals.md", "# V1 goals\n\nThe goals are still to be written.\n")
	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	if len(set.Goals) != 0 || len(set.Problems) != 1 {
		t.Fatalf("set = %#v", set)
	}
	if !strings.Contains(set.Problems[0].Reason, "`Goals` heading") {
		t.Fatalf("problem = %v", set.Problems[0])
	}
	// The document was still read, so the set says where it looked.
	if len(set.Sources) != 1 || set.Sources[0] != "v1-goals" {
		t.Fatalf("sources = %v", set.Sources)
	}
}

func TestAGoalsSectionIsReadToItsEndAndNoFurther(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	write(t, root, "docs/product/goals/v1-goals.md", `# V1 goals

An introduction.

## Goals

- Maintain a traceable chain.

### Goals added after the brief was written

- Review every change independently.

## Non-goals

- Support every provider.

`+"```"+`
- Not a goal; a fenced example.
`+"```"+`
`)

	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	statements := stated(set.Goals)
	// A heading below the goals divides them rather than ending them; a heading
	// at the same level ends the section, so a non-goal is never collected as
	// something work can be attributed to.
	if len(statements) != 2 || statements[0] != "Maintain a traceable chain." || statements[1] != "Review every change independently." {
		t.Fatalf("goals = %q", statements)
	}
}

func TestANonGoalIsNeverReadAsSomethingWorkMayServe(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// The case a level test alone cannot catch: the document's title is the
	// goals heading, so everything after it is nested below level 1 and no
	// heading can end the section by level. Filed this way, every non-goal would
	// be collected as a goal work may be admitted under — the opposite of what
	// the document says, and worse than reading no goals at all.
	write(t, root, "docs/product/goals/v1-goals.md", `# Goals

- Maintain a traceable chain.

## Non-goals

- Support every provider.
- Replace the operator's judgement.
`)

	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	if statements := stated(set.Goals); len(statements) != 1 || statements[0] != "Maintain a traceable chain." {
		t.Fatalf("goals = %q", statements)
	}
	if set.Attribute("Support every provider.").State != StateUnresolved {
		t.Fatalf("a non-goal resolved as a goal work may serve")
	}
}

func TestATitleThatOpensWithTheWordIsATitleRatherThanTheGoalsSection(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// `# Goals for V1` is what the document is called. Reading it as the section
	// would open the goals at level 1 and collect every top-level entry below,
	// whatever heading it was written under.
	write(t, root, "docs/product/goals/v1-goals.md", `# Goals for V1

An introduction.

## Goals

- Maintain a traceable chain.

## Open questions

- Whether to support a second tracker.
`)

	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	if statements := stated(set.Goals); len(statements) != 1 || statements[0] != "Maintain a traceable chain." {
		t.Fatalf("goals = %q", statements)
	}

	// A document with no such section at all states no goals, and is reported
	// rather than read as though its title were the heading.
	titleOnly := newRepository(t)
	write(t, titleOnly, "docs/product/goals/v1-goals.md", "# Goals for V1\n\n- Maintain a traceable chain.\n")
	reported := Collect(titleOnly, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	if len(reported.Goals) != 0 || len(reported.Problems) != 1 {
		t.Fatalf("set = %#v", reported)
	}
	if !strings.Contains(reported.Problems[0].Reason, "whole text is `Goals`") {
		t.Fatalf("problem = %v", reported.Problems[0])
	}
}

func TestOnlyTopLevelEntriesAreGoals(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	write(t, root, "docs/product/goals/v1-goals.md", `# V1 goals

An introduction.

## Goals

- Maintain a traceable chain.
  - which includes the tracker
  *Supports: every change traces to intent somebody approved.*
`)

	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	if statements := stated(set.Goals); len(statements) != 1 || statements[0] != "Maintain a traceable chain." {
		t.Fatalf("goals = %q", statements)
	}
}

func TestAGoalHardWrappedAcrossLinesIsRecordedWhole(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// Markdown is normally hard-wrapped, and a goal written that way is the
	// ordinary case rather than the odd one. Recording only its first physical
	// line would record a fragment of every goal in a repository whose editor
	// wraps, and the attribution naming the words the document states would then
	// resolve to nothing.
	write(t, root, "docs/product/goals/v1-goals.md", `# V1 goals

An introduction.

## Goals

- Run development nearly autonomously. The human's routine interface is the
  product manager: they state intent, approve the brief and goals, and answer
  questions the product manager escalates.
  *Supports: the human's attention goes only where it is needed.*
- Maintain a traceable chain.
`)

	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	if len(set.Problems) != 0 {
		t.Fatalf("problems = %v", set.Problems)
	}
	whole := "Run development nearly autonomously. The human's routine interface is the product manager: they state intent, approve the brief and goals, and answer questions the product manager escalates."
	statements := stated(set.Goals)
	// The wrapping is how the document was typed, not what it says: the entry
	// states one goal, and the entry after it is still a second one.
	if len(statements) != 2 || statements[0] != whole || statements[1] != "Maintain a traceable chain." {
		t.Fatalf("goals = %q", statements)
	}
	// The trailer names what the goal supports upstream. Joined into the
	// statement it would corrupt the goal exactly as truncation does.
	if strings.Contains(statements[0], "Supports:") {
		t.Fatalf("goal statement swallowed the trailer under it: %q", statements[0])
	}
	if !set.Attribute(whole).Resolved() {
		t.Fatalf("the whole statement the document makes does not resolve: %#v", set.Attribute(whole))
	}
	// The fragment is not a goal the document states, and reading it as one is
	// what let a truncated goal look like it resolved.
	if set.Attribute("Run development nearly autonomously. The human's routine interface is the").State != StateUnresolved {
		t.Fatalf("a fragment of a wrapped goal resolved as a goal")
	}
}

func TestAGoalHardWrappedAcrossLinesIsReportedThoughItIsStillRecorded(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// Rejoining the wrap is a reading of the file rather than something the file
	// says. The goal it produces is what an attribution has to match word for
	// word, so a goal that only exists once the wrap is rejoined can be changed
	// into a different goal by an edit that changes none of its words.
	write(t, root, "docs/product/goals/v1-goals.md", frontmatter("v1-goals", "goals", "V1 goals", []string{"brief"})+`
# V1 goals

An introduction.

## Goals

- Run development nearly autonomously. The human's routine interface is the
  product manager: they state intent, approve the brief and goals, and answer
  questions the product manager escalates.
  *Supports: the human's attention goes only where it is needed.*
- Maintain a traceable chain.
  *Supports: every change traces to intent somebody approved.*
`)

	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	if len(set.Problems) != 0 {
		t.Fatalf("problems = %v", set.Problems)
	}
	// Nothing is dropped over one: the wrapped goal is still a goal work can be
	// attributed to, exactly as a goal with a broken link upstream is.
	if statements := stated(set.Goals); len(statements) != 2 {
		t.Fatalf("goals = %q", statements)
	}
	if len(set.WrapProblems) != 1 {
		t.Fatalf("wrap problems = %v", set.WrapProblems)
	}
	problem := set.WrapProblems[0]
	if problem.Lines != 3 || problem.ArtifactID != "v1-goals" || problem.Path != "docs/product/goals/v1-goals.md" {
		t.Fatalf("wrap problem = %#v", problem)
	}
	// The line is counted from the top of the file rather than from the end of
	// the frontmatter, because what is reported has to name a place to open.
	if got := lineOf(t, root, "docs/product/goals/v1-goals.md", problem.Line); !strings.HasPrefix(got, "- Run development nearly autonomously.") {
		t.Fatalf("line %d is %q, want the entry the goal opens on", problem.Line, got)
	}
	if problem.Statement != set.Goals[0].Statement {
		t.Fatalf("wrap problem names %q, want the goal as it was rejoined", problem.Statement)
	}
}

func TestAGoalOnOneLineIsNotReportedForWhatIsWrittenUnderIt(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// The trailer, a wrapped trailer, and a paragraph about the goal are all
	// written under the entry and none of them is the statement. Counting any of
	// them as a wrap would report every goal in this repository, which is a check
	// nobody could satisfy without deleting what the documents say.
	write(t, root, "docs/product/goals/v1-goals.md", `# V1 goals

An introduction.

## Goals

- Maintain a traceable chain from the product brief through goals, designs, work, code changes, and verification.
  *Supports: every change traces to intent somebody approved, from the brief
  through the goals and down to the work.*

  This matters because a change nobody can trace is a change nobody approved.

- Review every change independently.
  *Supports: nothing lands unreviewed by someone other than its author.*
`)

	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	if len(set.WrapProblems) != 0 {
		t.Fatalf("wrap problems = %v", set.WrapProblems)
	}
}

func TestAWrappedGoalInADocumentNoLongerInForceIsNotReported(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// The same rule the link upstream is judged by: a goal in a superseded
	// document is not one work can name, and reporting how it is written would
	// leave a permanent finding against a file nobody is going to open again.
	write(t, root, "docs/product/goals/v0-goals.md", `# V0 goals

An introduction.

## Goals

- Run development nearly autonomously. The human's routine interface is the
  product manager.
`)

	set := Collect(root, setOf(recorded("v0-goals", artifact.KindGoals, artifact.StatusSuperseded, "docs/product/goals/v0-goals.md")))
	if len(set.Goals) != 1 {
		t.Fatalf("goals = %q", stated(set.Goals))
	}
	if len(set.WrapProblems) != 0 {
		t.Fatalf("wrap problems = %v", set.WrapProblems)
	}
}

func TestProseUnderAGoalIsNotJoinedIntoIt(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// A wrapped statement and a paragraph about the goal are told apart the way
	// Markdown tells them apart: the statement is the entry's opening paragraph,
	// and a blank line starts prose that describes it.
	write(t, root, "docs/product/goals/v1-goals.md", `# V1 goals

An introduction.

## Goals

- Maintain a traceable chain from the product brief through goals, designs,
  work, code changes, and verification.

  This matters because a change nobody can trace is a change nobody approved.

- Review every change independently.
  **Supports: nothing lands unreviewed by someone other than its author.**
Not indented, so not part of the entry above it.
`)

	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	statements := stated(set.Goals)
	if len(statements) != 2 {
		t.Fatalf("goals = %q", statements)
	}
	if statements[0] != "Maintain a traceable chain from the product brief through goals, designs, work, code changes, and verification." {
		t.Fatalf("goal = %q", statements[0])
	}
	// A trailer bolded rather than italicized means the same thing by it, and
	// swallowing it corrupts the goal exactly as truncation does.
	if statements[1] != "Review every change independently." {
		t.Fatalf("goal = %q", statements[1])
	}
}

func TestATrailerWrappedAcrossLinesIsNotJoinedIntoTheGoal(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// A trailer long enough to wrap is as ordinary as a goal long enough to wrap,
	// and joining one into the statement corrupts the recorded goal exactly as
	// truncating the statement does: the words the document states as the goal
	// then resolve to nothing.
	write(t, root, "docs/product/goals/v1-goals.md", `# V1 goals

An introduction.

## Goals

- Publish that work as pull requests the harness opens, and has the forge
  merge, on the roles' behalf.
  *Supports: safety invariants hold whatever the configuration says, and no
  agent pushes or merges.*
- Maintain a traceable chain.
`)

	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	if len(set.Problems) != 0 {
		t.Fatalf("problems = %v", set.Problems)
	}
	statements := stated(set.Goals)
	whole := "Publish that work as pull requests the harness opens, and has the forge merge, on the roles' behalf."
	if len(statements) != 2 || statements[0] != whole || statements[1] != "Maintain a traceable chain." {
		t.Fatalf("goals = %q", statements)
	}
	if !set.Attribute(whole).Resolved() {
		t.Fatalf("the whole statement the document makes does not resolve: %#v", set.Attribute(whole))
	}
}

func TestAWrappedLineThatOpensWithEmphasisStillContinuesTheStatement(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// The trailer is told from prose by whether the emphasis closes and the
	// sentence carries on. A continuation that opens with an emphasized phrase —
	// and closes with one, so that the line begins and ends in a marker — is the
	// rest of the statement, not an annotation of it.
	write(t, root, "docs/product/goals/v1-goals.md", `# V1 goals

An introduction.

## Goals

- Run development nearly autonomously. The human's routine interface is the
  *product manager*: they state intent and answer *questions*
  the product manager escalates.
  *Supports: the human's attention goes only where it is needed.*
`)

	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	whole := "Run development nearly autonomously. The human's routine interface is the *product manager*: they state intent and answer *questions* the product manager escalates."
	if statements := stated(set.Goals); len(statements) != 1 || statements[0] != whole {
		t.Fatalf("goals = %q", stated(set.Goals))
	}
}

func TestAGoalTooLongToNameOnAWorkItemIsReportedRatherThanOffered(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	write(t, root, "docs/product/goals/v1-goals.md", goalsDocument(strings.Repeat("a", MaxStatementBytes+1)))
	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	// Collecting it would offer an attribution that is refused every time it is
	// used, which reads as the harness disagreeing with itself.
	if len(set.Goals) != 0 || len(set.Problems) != 1 {
		t.Fatalf("set = %#v", set)
	}
	if !strings.Contains(set.Problems[0].Reason, "limit is") {
		t.Fatalf("problem = %v", set.Problems[0])
	}
}

func TestAGoalsDocumentThatCannotBeReadIsReportedBesideTheGoalsThatWere(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	write(t, root, "docs/product/goals/v1-goals.md", goalsDocument("Maintain a traceable chain."))
	set := Collect(root, setOf(
		recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md"),
		recorded("v2-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v2-goals.md"),
	))
	if len(set.Goals) != 1 {
		t.Fatalf("goals = %q", stated(set.Goals))
	}
	if len(set.Problems) != 1 || set.Problems[0].Path != "docs/product/goals/v2-goals.md" {
		t.Fatalf("problems = %v", set.Problems)
	}
}

func TestOnlyGoalsDocumentsAreReadForGoals(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// A brief and a design both state goals in prose, and neither is what work is
	// attributed to: the goals are the product manager's document, and reading
	// intent out of anything that happens to have a `Goals` heading is how a
	// design comes to authorize its own work.
	write(t, root, "docs/product/brief.md", goalsDocument("Be a harness somebody would use."))
	write(t, root, "docs/designs/v1-harness.md", goalsDocument("Keep the pipeline resumable."))
	set := Collect(root, setOf(
		recorded("brief", artifact.KindBrief, artifact.StatusActive, "docs/product/brief.md"),
		recorded("v1-harness", artifact.KindDesign, artifact.StatusActive, "docs/designs/v1-harness.md"),
	))
	if len(set.Goals) != 0 || len(set.Sources) != 0 || len(set.Problems) != 0 {
		t.Fatalf("set = %#v", set)
	}
}

func TestTheGoalsAreReadFromTheArtifactsAsTheStoreLoadsThem(t *testing.T) {
	t.Parallel()

	// The two halves meet here: the store decides which documents are goals, and
	// this reads what those documents state. A test that constructed the set by
	// hand would never notice the two disagreeing about a file.
	root := newRepository(t)
	write(t, root, "docs/product/brief.md", frontmatter("brief", "brief", "Product brief", nil)+"\n# Brief\n")
	write(t, root, "docs/product/goals/v1-goals.md",
		frontmatter("v1-goals", "goals", "V1 goals", []string{"brief"})+"\n"+goalsDocument("Maintain a traceable chain."))
	store := artifact.Store{RepositoryRoot: root, Homes: []string{goalsHome}}
	artifacts, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	set := Collect(root, artifacts)
	if len(set.Goals) != 1 || set.Goals[0].ArtifactID != "v1-goals" {
		t.Fatalf("set = %#v", set)
	}
	if !set.Attribute("Maintain a traceable chain.").Resolved() {
		t.Fatalf("the goal the document states does not resolve")
	}
}

func TestAGoalNamesTheBriefGoalItSupportsRatherThanOnlySayingSoInProse(t *testing.T) {
	t.Parallel()

	// The link upstream is what the frontmatter cannot carry: `supports: brief`
	// says the document serves the brief and says nothing about which of the
	// brief's goals any one entry in it reaches.
	set := setLinkedToBrief(t, `# V1 goals

An introduction.

## Goals

- Maintain a traceable chain from the brief through to verification.
  *Supports: every change traces to intent somebody approved.*
- Review every change independently.
  **Supports: nothing lands unreviewed by someone other than its author.**
`)
	if len(set.LinkProblems) != 0 {
		t.Fatalf("link problems = %v", set.LinkProblems)
	}
	if len(set.BriefGoals) != 2 {
		t.Fatalf("brief goals = %#v", set.BriefGoals)
	}
	// The brief's goal is named by the claim it opens with, not by the paragraph
	// enlarging on it.
	if set.BriefGoals[0].Name != "Every change traces to intent somebody approved." {
		t.Fatalf("brief goal = %#v", set.BriefGoals[0])
	}
	// Either spelling of the emphasis means the same thing by it, and the
	// trailing marker is markup rather than part of what is named.
	if set.Goals[0].Supports != "every change traces to intent somebody approved." {
		t.Fatalf("supports = %q", set.Goals[0].Supports)
	}
	if set.Goals[1].Supports != "nothing lands unreviewed by someone other than its author." {
		t.Fatalf("supports = %q", set.Goals[1].Supports)
	}
}

func TestATrailerWrappedAcrossLinesNamesTheWholeBriefGoal(t *testing.T) {
	t.Parallel()

	// A trailer long enough to wrap is as ordinary as a goal long enough to wrap.
	// Reading only its first physical line would name a fragment, which resolves
	// to nothing and reports the fragment rather than the truncation.
	set := setLinkedToBrief(t, `# V1 goals

An introduction.

## Goals

- Publish that work as pull requests the harness opens.
  *Supports: nothing lands unreviewed by someone other than
  its author.*
`)
	if len(set.LinkProblems) != 0 {
		t.Fatalf("link problems = %v", set.LinkProblems)
	}
	if set.Goals[0].Supports != "nothing lands unreviewed by someone other than its author." {
		t.Fatalf("supports = %q", set.Goals[0].Supports)
	}
	// The trailer still has to stay out of the goal itself.
	if strings.Contains(set.Goals[0].Statement, "Supports:") {
		t.Fatalf("goal = %q", set.Goals[0].Statement)
	}
}

func TestAGoalNamingABriefGoalTheBriefDoesNotStateIsReported(t *testing.T) {
	t.Parallel()

	set := setLinkedToBrief(t, `# V1 goals

An introduction.

## Goals

- Maintain a traceable chain from the brief through to verification.
  *Supports: every change is cheap to make.*
`)
	if len(set.LinkProblems) != 1 || set.LinkProblems[0].Kind != LinkDangling {
		t.Fatalf("link problems = %#v", set.LinkProblems)
	}
	problem := set.LinkProblems[0]
	if problem.ArtifactID != "v1-goals" || problem.Path != "docs/product/goals/v1-goals.md" {
		t.Fatalf("link problem = %#v", problem)
	}
	// What is reported has to name the claim that does not resolve, because that
	// is the string somebody has to correct.
	if !strings.Contains(problem.Reason, "every change is cheap to make.") {
		t.Fatalf("reason = %q", problem.Reason)
	}
	// Nothing is dropped over a broken link: the goal is still what the document
	// states, and work naming it still resolves.
	if !set.Attribute("Maintain a traceable chain from the brief through to verification.").Resolved() {
		t.Fatalf("a goal with a broken link upstream stopped resolving")
	}
}

func TestAGoalThatNamesNothingUpstreamIsReportedAsAnOrphan(t *testing.T) {
	t.Parallel()

	// A goal with no trailer at all, and one whose trailer annotates the goal
	// rather than linking it, are the same thing: neither says what the goal is
	// for.
	set := setLinkedToBrief(t, `# V1 goals

An introduction.

## Goals

- Maintain a traceable chain from the brief through to verification.
- Review every change independently.
  *Added when the backlog was checked against the brief.*
`)
	if len(set.LinkProblems) != 2 {
		t.Fatalf("link problems = %#v", set.LinkProblems)
	}
	for _, problem := range set.LinkProblems {
		if problem.Kind != LinkUnstated {
			t.Fatalf("link problem = %#v", problem)
		}
	}
	if set.Goals[1].Supports != "" {
		t.Fatalf("a trailer that names no brief goal was read as naming one: %q", set.Goals[1].Supports)
	}
}

func TestABriefStatingNoGoalsIsReportedOnceRatherThanAgainstEveryGoal(t *testing.T) {
	t.Parallel()

	// Naming the missing root beats reporting every goal as separately unlinked:
	// what somebody has to fix is the brief, and it is one thing rather than one
	// per goal below it.
	root := newRepository(t)
	write(t, root, "docs/product/brief.md", "# Product brief\n\nWhat this is for, with no goals stated under a heading.\n")
	write(t, root, "docs/product/goals/v1-goals.md", `# V1 goals

An introduction.

## Goals

- Maintain a traceable chain from the brief through to verification.
  *Supports: every change traces to intent somebody approved.*
- Review every change independently.
  *Supports: nothing lands unreviewed by someone other than its author.*
`)
	set := Collect(root, setOf(
		recorded("brief", artifact.KindBrief, artifact.StatusActive, "docs/product/brief.md"),
		recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md"),
	))
	if len(set.LinkProblems) != 1 || set.LinkProblems[0].Kind != LinkNoBriefGoals {
		t.Fatalf("link problems = %#v", set.LinkProblems)
	}
	if !strings.Contains(set.LinkProblems[0].Reason, "brief") {
		t.Fatalf("reason = %q", set.LinkProblems[0].Reason)
	}
	// A brief that states no goals is not a goals document that could not be
	// read. It is the root either way.
	if len(set.Problems) != 0 {
		t.Fatalf("problems = %v", set.Problems)
	}
}

func TestAGoalNoLongerInForceIsNotHeldToItsLinkUpstream(t *testing.T) {
	t.Parallel()

	// A superseded document states intent that was replaced. Reporting its links
	// would leave a permanent finding against a decision somebody already made.
	root := newRepository(t)
	write(t, root, "docs/product/brief.md", briefDocument())
	write(t, root, "docs/product/goals/v0-goals.md", `# V0 goals

An introduction.

## Goals

- Something the product no longer intends.
  *Supports: a brief goal nobody ever wrote.*
`)
	set := Collect(root, setOf(
		recorded("brief", artifact.KindBrief, artifact.StatusActive, "docs/product/brief.md"),
		recorded("v0-goals", artifact.KindGoals, artifact.StatusSuperseded, "docs/product/goals/v0-goals.md"),
	))
	if len(set.LinkProblems) != 0 {
		t.Fatalf("link problems = %#v", set.LinkProblems)
	}
}

func TestABriefNoLongerInForceStatesNoGoalAGoalCanName(t *testing.T) {
	t.Parallel()

	// Both ends of the link are held to the same rule. A goal resolving against a
	// brief goal the product replaced would be traceability pointing at intent
	// nobody holds any more, which is worse than reporting the link as unmet.
	root := newRepository(t)
	write(t, root, "docs/product/brief.md", briefDocument())
	write(t, root, "docs/product/goals/v1-goals.md", `# V1 goals

An introduction.

## Goals

- Maintain a traceable chain from the brief through to verification.
  *Supports: every change traces to intent somebody approved.*
`)
	set := Collect(root, setOf(
		recorded("brief", artifact.KindBrief, artifact.StatusSuperseded, "docs/product/brief.md"),
		recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md"),
	))
	if len(set.BriefGoals) != 0 {
		t.Fatalf("a brief no longer in force offered link targets: %#v", set.BriefGoals)
	}
	if len(set.LinkProblems) != 1 || set.LinkProblems[0].Kind != LinkNoBriefGoals {
		t.Fatalf("link problems = %#v", set.LinkProblems)
	}
	// What is reported has to say which of the three things to do about it, so it
	// names the brief that ended rather than reading as a brief that states none.
	if !strings.Contains(set.LinkProblems[0].Reason, "no longer applies") {
		t.Fatalf("reason = %q", set.LinkProblems[0].Reason)
	}
}

// TestYoyodynesOwnArtifactsLoadAndItsGoalsAreReadFromThem runs the real store
// and the real collector over this repository's own documents, which is the only
// place either of them meets a document somebody wrote rather than one a test
// constructed. The homes come from the project's own configuration rather than
// from constants written beside this test, because a test naming the directories
// itself would be checking the set it chose rather than the set the harness
// reads, and would keep passing after the project moved them.
//
// What this refuses and what it tolerates is a decided boundary rather than an
// oversight, and yoyodyne-ifd.326 moved that line. It used to fall between a
// contradiction and an absence, which put a goals document asserting something
// the brief does not say on the refusing side. It now falls between the
// harness's own record and the product's prose: nothing a person writes in a
// goals document reddens this build, and what is held here is the identity the
// harness validates and the reports it produces about that prose.
//
// The reason is the one the reviewer gave for the states that were already
// tolerated, followed the rest of the way. A `*Supports: ...*` trailer stops
// naming a brief goal the moment somebody rewords that claim in the brief, and
// both documents are the product manager's to write; a build that goes red on
// the edit makes rewording the document to suit the checker the cheapest way out,
// which is exactly the pressure the tolerated states exist to avoid. So the
// check moved to the surfaces that can afford to be right about it — `yoyo goals
// list` names it on stderr and in `--json`, and `internal/conformance`'s goals
// check carries it into a release assessment, where a goals document nobody can
// read refuses the cut and a broken link upstream is a note beside it.
//
// Refused, because each is the harness's own reading wrong rather than a
// document part-way written:
//
//   - The store failing to load, and a recorded document it could not read: a
//     loader that stopped accepting what this repository ships is code, which
//     internal/cli's governed_documents_repository_test.go fails on as well.
//
// Escalated to the owning role rather than refused: a `supports:` naming an
// artifact nobody records, an artifact nothing connects to the brief, and a
// revision recorded under a role that does not own the file. Each is a defect
// in a document in an artifact home, which no developer's change may edit, so
// failing on it here turned every developer's build red over a file none of
// them could open (internal/governeddoc). Each is written with its owner and
// route, `make test` prints it after the suite (the Makefile's
// GOVERNED_DOCUMENT_TESTS names this test), and `yoyo artifact check` fails on
// it for whoever can put it right.
//
//   - A report of any of the tolerated states below that names no file to open,
//     and a recorded brief the collected goals do not say where to find. A
//     legitimate state described by a report nobody can act on is ours to fix
//     rather than the product manager's, and it is the whole of what is left
//     here about the live documents.
//
// Tolerated, because each is the product's own prose rather than the harness
// contradicting itself, and each is already reported by `yoyo goals list` on
// stderr and carried into `yoyo release`'s goals check:
//
//   - A goals document stating no goals, and no goal in force at all. A goals
//     set is legitimately empty before the goals are written, and Collect
//     reporting it is the correct report rather than a defect.
//
//   - A goal naming nothing upstream. The goal has yet to say what it is for,
//     which is a sentence somebody still has to write and not a false one.
//
//   - A goal naming a brief goal the brief does not state. It is still a broken
//     link and still worth fixing — it is what silently orphans the work
//     attributed under it — and what changed is where it is judged: the release
//     assessment reads it in front of the person who owns both documents, rather
//     than every developer's `make test` reading it in front of somebody who
//     may not edit either.
//
//   - A brief stating no goals for anything to name.
func TestYoyodynesOwnArtifactsLoadAndItsGoalsAreReadFromThem(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	cfg := configuration(t, root)
	artifacts, err := artifact.StoreFor(root, cfg.Product).Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(artifacts.Problems) != 0 {
		t.Fatalf("problems = %v", artifacts.Problems)
	}
	defects := make([]governeddoc.Defect, 0, len(artifacts.ReferenceProblems))
	for _, problem := range artifacts.ReferenceProblems {
		defects = append(defects, governeddoc.Defect{Path: problem.Path, Detail: string(problem.Kind) + " — " + problem.Reason})
	}
	governeddoc.Report(cfg, defects, t.Errorf, governeddoc.Escalate)

	set := Collect(root, artifacts)
	for _, problem := range set.Problems {
		if problem.Path == "" {
			t.Errorf("a goals document was not read and the report does not say which: %q", problem.Reason)
		}
		t.Logf("goals not read: %s", problem)
	}
	for _, problem := range set.LinkProblems {
		switch problem.Kind {
		case LinkDangling, LinkUnstated:
			// Naming the document is this package's job whatever the goal says, so
			// it is refused even though the state being reported is tolerated.
			// LinkNoBriefGoals is about the brief rather than about any one goal and
			// carries no path, which is why it is not held to this.
			if problem.Path == "" || problem.ArtifactID == "" {
				t.Errorf("a goal whose link upstream does not hold does not name the document it is written in: %#v", problem)
			}
			t.Logf("goal not linked to the brief: %s", problem)
		default:
			t.Logf("goal not linked to the brief: %s", problem)
		}
	}
	// A brief that is recorded at all has to be somewhere a reader can be sent,
	// whether or not it states a goal: the goals listing closes by sending them
	// there. This is about what Collect carries rather than about what the brief
	// says, which is why it is refused where the contents of the brief are not.
	if len(artifacts.OfKind(artifact.KindBrief)) != 0 && set.BriefPath == "" {
		t.Errorf("a brief is recorded and the collected goals do not say where to open it")
	}
}

func TestNoDocumentUnderDocsCarriesIdentityOutsideAConfiguredHome(t *testing.T) {
	t.Parallel()

	// Identity outside a configured home is identity nothing reads. The store
	// never walks the file, so it is neither an artifact nor a reported problem,
	// and a document can look governed to a reader while nothing downstream can
	// refer to it — which is how the v1 harness design sat for as long as it did.
	// The load being clean is therefore not evidence on its own, and this is the
	// check that makes it so.
	root := repositoryRoot(t)
	store := configuredStore(t, root)
	homes := append([]string(nil), store.Homes...)

	documents := filepath.Join(root, "docs")
	// A project that has written nothing down carries identity nowhere, which is
	// the same legitimate state as a home nobody has created: there is nothing
	// here to be outside a home.
	if _, err := os.Stat(documents); errors.Is(err, os.ErrNotExist) {
		return
	}
	err := filepath.WalkDir(documents, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			return walkErr
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(strings.TrimPrefix(string(content), "\ufeff"), "---") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		slashed := filepath.ToSlash(relative)
		for _, home := range homes {
			if strings.HasPrefix(slashed, home+"/") {
				return nil
			}
		}
		t.Errorf("%s carries artifact frontmatter but is inside none of the configured homes (%s), so nothing reads its identity",
			slashed, strings.Join(homes, ", "))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", documents, err)
	}
}

// setLinkedToBrief is a repository whose brief states the goals a goals document
// links up to, so a test about that link says only that.
func setLinkedToBrief(t *testing.T, goals string) Set {
	t.Helper()
	root := newRepository(t)
	write(t, root, "docs/product/brief.md", briefDocument())
	write(t, root, "docs/product/goals/v1-goals.md", goals)
	set := Collect(root, setOf(
		recorded("brief", artifact.KindBrief, artifact.StatusActive, "docs/product/brief.md"),
		recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md"),
	))
	if len(set.Problems) != 0 {
		t.Fatalf("problems = %v", set.Problems)
	}
	return set
}

// briefDocument is a brief in the shape Yoyodyne's own is written in: each goal
// a bolded claim followed by a paragraph enlarging on it.
func briefDocument() string {
	return `# Product brief

What the product is for.

## Goals

- **Every change traces to intent somebody approved.** A reader can follow any
  merged change back through the work, the design, and the goal to the brief.
- **Nothing lands unreviewed by someone other than its author.**
`
}

// configuredStore is the artifact store this project actually reads, built from
// its own configuration: the three homes and the invariants directory excluded
// from them, exactly as the commands assemble it.
func configuredStore(t *testing.T, root string) artifact.Store {
	t.Helper()
	product := configuration(t, root).Product
	// The store is assembled through the same path the commands use, so a home
	// added to the production assembly is covered here without this list being
	// maintained by hand.
	//
	// Nothing here checks that the homes exist. A home the configuration names and
	// nobody has created yet is a legitimate state — a project has no designs
	// before its first one is written — and the store already reads it as the
	// empty set rather than an error, so demanding the directory here would fail a
	// repository the harness itself is perfectly happy with. A home that exists
	// and is not a directory is still refused, by `Load` rather than by this: the
	// store cannot walk it, and says so.
	//
	// What the check that used to stand here was reaching for is a
	// governed-looking document that no home covers, and that is found directly by
	// TestNoDocumentUnderDocsCarriesIdentityOutsideAConfiguredHome — which looks
	// for the document itself, wherever it sits, instead of inferring one from an
	// absent directory.
	return artifact.StoreFor(root, product)
}

// configuration is this project's own configuration, discovered and loaded the
// way the commands do it.
func configuration(t *testing.T, root string) config.Config {
	t.Helper()
	path, err := config.Discover(root)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return loaded
}

// repositoryRoot is the checkout these tests run in, so the seeded artifacts are
// read where they actually live rather than from a copy.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("Abs() error = %v", err)
	}
	if root, err = filepath.EvalSymlinks(root); err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	return root
}

// setWithGoals is a repository whose only goals document states what a test
// names, so a test about attribution says only that.
func setWithGoals(t *testing.T, statements ...string) Set {
	t.Helper()
	root := newRepository(t)
	write(t, root, "docs/product/goals/v1-goals.md", goalsDocument(statements...))
	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	if len(set.Problems) != 0 {
		t.Fatalf("problems = %v", set.Problems)
	}
	return set
}

func goalsDocument(statements ...string) string {
	var rendered strings.Builder
	rendered.WriteString("# The goals of some product\n\nAn introduction saying what this covers.\n\n## Goals\n\n")
	for _, statement := range statements {
		rendered.WriteString("- " + statement + "\n")
	}
	return rendered.String()
}

func frontmatter(id, kind, title string, supports []string) string {
	var rendered strings.Builder
	rendered.WriteString("---\nid: " + id + "\nkind: " + kind + "\ntitle: " + title + "\n")
	rendered.WriteString("supports:\n")
	for _, reference := range supports {
		rendered.WriteString("    - " + reference + "\n")
	}
	rendered.WriteString("status: active\nrevisions:\n")
	rendered.WriteString("    - action: created\n      by: product-manager\n      at: 2026-08-17T12:00:00Z\n      reason: recorded when identity arrived\n")
	rendered.WriteString("---\n")
	return rendered.String()
}

func recorded(id string, kind artifact.Kind, status artifact.Status, path string) artifact.Artifact {
	return artifact.Artifact{ID: id, Kind: kind, Title: id, Status: status, Path: path}
}

func setOf(artifacts ...artifact.Artifact) artifact.Set {
	return artifact.Set{Artifacts: artifacts}
}

// lineOf is what a document says on one physical line, counted from one, so a
// test about a reported position checks where the position lands rather than
// restating the arithmetic that produced it.
func lineOf(t *testing.T, root, relative string, line int) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	lines := strings.Split(string(content), "\n")
	if line < 1 || line > len(lines) {
		t.Fatalf("line %d is outside %s, which has %d lines", line, relative, len(lines))
	}
	return lines[line-1]
}

func stated(goals []Goal) []string {
	statements := make([]string, 0, len(goals))
	for _, recorded := range goals {
		statements = append(statements, recorded.Statement)
	}
	return statements
}

func newRepository(t *testing.T) string {
	t.Helper()
	// A temporary directory on macOS is a symlink, and the artifact store
	// resolves the root through symlinks, so both halves are given what it
	// resolves to.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	return root
}

func write(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func TestTheSupportsTrailerIsReadWhicheverSideOfAnAnnotationItSits(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// Two trailers under one goal, in both orders. The link reads the Supports
	// trailer; the annotation is prose beside it, not part of the link and not
	// something that may swallow it.
	write(t, root, "docs/product/goals/v1-goals.md", `---
id: v1-goals
---

# V1 goals

An introduction.

## Goals

- Maintain a traceable chain from intent to verification.
  *Supports: every change traces to intent somebody approved.*
  *Added when the backlog was checked against the brief.*
- Isolate implementation tasks in harness-managed worktrees.
  *Added when the backlog was checked against the brief.*
  *Supports: intent goes in and merged software comes out.*
`)

	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	if len(set.Problems) != 0 {
		t.Fatalf("problems = %v", set.Problems)
	}
	want := []string{
		"every change traces to intent somebody approved.",
		"intent goes in and merged software comes out.",
	}
	for i, goal := range set.Goals {
		if goal.Supports != want[i] {
			t.Fatalf("goal %d supports = %q, want %q (annotation must not swallow or precede away the link)", i, goal.Supports, want[i])
		}
	}
}

func TestAnUnreadableGoalsDocumentDoesNotReportTheBriefAsUnreadable(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// The brief reads fine and legitimately states no goals; a goals document
	// beside it cannot be read at all. The link report must point at the brief's
	// real condition, not at a read failure belonging to a different document.
	write(t, root, "docs/product/brief.md", `---
id: brief
---

# Product brief

An introduction with no Goals heading.
`)
	write(t, root, "docs/product/goals/v1-goals.md", `---
id: v1-goals
---

# V1 goals

An introduction.

## Goals

- Maintain a traceable chain from intent to verification.
`)

	set := Collect(root, setOf(
		recorded("brief", artifact.KindBrief, artifact.StatusActive, "docs/product/brief.md"),
		recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md"),
		recorded("v2-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/missing.md"),
	))
	if len(set.Problems) != 1 || !strings.Contains(set.Problems[0].Path, "missing.md") {
		t.Fatalf("problems = %v, want exactly the unreadable goals document", set.Problems)
	}
	if len(set.LinkProblems) != 1 {
		t.Fatalf("link problems = %v, want the brief reported once", set.LinkProblems)
	}
	reason := set.LinkProblems[0].Reason
	if strings.Contains(reason, "could not be read") || !strings.Contains(reason, "states no goals") {
		t.Fatalf("link reason = %q: the brief states no goals and was read fine; a goals document failing to read must not be pinned on the brief", reason)
	}
}

// A goal carries the operator's approval of the document stating it, because
// that is now what decides whether work serving it reaches the queue without a
// person. An approval given against an earlier revision is deliberately not
// carried forward: the approval still stands for what it was given for, and the
// goal as the document now reads is not what anybody saw.
func TestAGoalCarriesWhetherTheOperatorApprovedTheDocumentStatingIt(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	write(t, root, "docs/product/goals/v1-goals.md", `---
id: v1-goals
---

# V1 goals

## Goals

- Run development nearly autonomously.
`)
	approved := recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")
	approved.Revisions = []artifact.Revision{{}}
	approved.Approvals = []artifact.Approval{{Revision: 0}}

	set := Collect(root, setOf(approved))
	if len(set.Goals) != 1 || !set.Goals[0].Approved() {
		t.Fatalf("goals = %#v", set.Goals)
	}
	if gap := set.Attribute("Run development nearly autonomously.").ApprovalGap(); gap != "" {
		t.Fatalf("an approved goal reports a gap: %q", gap)
	}

	// The same document with a revision recorded after the approval.
	amended := approved
	amended.Revisions = []artifact.Revision{{}, {}}
	amendedSet := Collect(root, setOf(amended))
	if len(amendedSet.Goals) != 1 || amendedSet.Goals[0].Approved() {
		t.Fatalf("an amended document still reads as approved: %#v", amendedSet.Goals)
	}
	if gap := amendedSet.Attribute("Run development nearly autonomously.").ApprovalGap(); !strings.Contains(gap, "amended since") {
		t.Fatalf("gap = %q, want it to say the document moved since the approval", gap)
	}

	// A rewording the Lead Product Manager recorded as consistent with intent,
	// naming the item that directed it, is delegated: the approval stands and work
	// naming the goal is admitted exactly as before. One recorded as a change of
	// fundamental intent, and one that does not say which it is, are the
	// operator's, and put admissions back to them.
	for _, testCase := range []struct {
		intent   artifact.Intent
		approved bool
	}{
		{intent: artifact.IntentConsistent, approved: true},
		{intent: artifact.IntentFundamental},
		{intent: ""},
	} {
		revised := approved
		revised.Revisions = []artifact.Revision{{}, {
			Action: artifact.ActionAmended, By: domain.RoleProductManager,
			Reason: "yoyodyne-ifd.437.11 - the autonomy goal names the Lead Product Manager", Intent: testCase.intent,
		}}
		revisedSet := Collect(root, setOf(revised))
		gap := revisedSet.Attribute("Run development nearly autonomously.").ApprovalGap()
		if testCase.approved {
			if len(revisedSet.Goals) != 1 || !revisedSet.Goals[0].Approved() || gap != "" {
				t.Fatalf("intent %q: goals = %#v, gap = %q; want the approval standing", testCase.intent, revisedSet.Goals, gap)
			}
			continue
		}
		if !strings.Contains(gap, "amended since") {
			t.Fatalf("intent %q: gap = %q, want the admission put to the operator", testCase.intent, gap)
		}
	}

	// And one nobody ever approved.
	unapproved := recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")
	unapprovedSet := Collect(root, setOf(unapproved))
	if len(unapprovedSet.Goals) != 1 || unapprovedSet.Goals[0].Approved() {
		t.Fatalf("an unapproved document reads as approved: %#v", unapprovedSet.Goals)
	}
	gap := unapprovedSet.Attribute("Run development nearly autonomously.").ApprovalGap()
	if !strings.Contains(gap, "records no approval") || !strings.Contains(gap, "v1-goals") {
		t.Fatalf("gap = %q, want it to name the unapproved document", gap)
	}
}

// Giving an approved goals document's goals identifiers is not an amendment of
// what the operator approved, so work serving those goals is admitted exactly as
// it was before: named by the words it always named, or by the identity the
// document now states. yoyodyne-ifd.344's identifiers waited on the operator
// because the harness read this as a change of intent.
func TestIdentifiersAddedToAnApprovedGoalsDocumentLeaveAdmissionUnaffected(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	store := artifact.Store{RepositoryRoot: root, Homes: []string{goalsHome}}
	moment := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	if _, err := store.Create(domain.RoleProductManager, artifact.Draft{
		ID: "v1-goals", Kind: artifact.KindGoals, Title: "V1 goals",
		Directory: goalsHome + "/goals", Body: goalsDocument("Run development nearly autonomously."),
		Reason: "the goals",
	}, moment); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.Approve("v1-goals", "approved in conversation", moment.Add(time.Hour)); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if _, err := store.Identify(domain.RoleProductManager, "v1-goals",
		goalsDocument("[run-development-nearly-autonomously] Run development nearly autonomously."),
		"yoyodyne-ifd.344 - goal identifiers recorded", moment.Add(2*time.Hour)); err != nil {
		t.Fatalf("Identify() error = %v", err)
	}
	artifacts, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	set := Collect(root, artifacts)
	if len(set.Goals) != 1 || set.Goals[0].Identity != "run-development-nearly-autonomously" {
		t.Fatalf("goals = %#v", set.Goals)
	}
	if set.Goals[0].Approval != artifact.ApprovalApproved {
		t.Fatalf("approval = %q, want the document still approved as it stands", set.Goals[0].Approval)
	}
	for _, named := range []string{
		"Run development nearly autonomously.",
		"[run-development-nearly-autonomously] Run development nearly autonomously.",
	} {
		if gap := set.Attribute(named).ApprovalGap(); gap != "" {
			t.Fatalf("admission naming %q is put to the operator: %q", named, gap)
		}
	}
}

// An attribution that does not resolve reports the gap it already reports, in
// the same words the rest of the harness reports it in. There is no second
// vocabulary for the same failure.
func TestAnUnresolvedAttributionReportsItsOwnReasonAsTheApprovalGap(t *testing.T) {
	t.Parallel()

	for _, attribution := range []Attribution{
		{State: StateUnattributed, Reason: "it names no goal"},
		{State: StateUnresolved, Named: "Grow the ecosystem.", Reason: "no goal recorded in v1-goals is stated in those words"},
		{State: StateUncheckable, Named: "Anything.", Reason: "the repository records no goals artifact"},
	} {
		if gap := attribution.ApprovalGap(); gap != attribution.Reason {
			t.Fatalf("%s gap = %q, want %q", attribution.State, gap, attribution.Reason)
		}
	}
}
