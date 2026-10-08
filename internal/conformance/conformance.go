// Package conformance is the release-readiness assessment: whether the system
// still matches what it records about itself, asked of a whole repository and
// answered before a version is tagged.
//
// It composes checks that already existed and invents no judgement of its own.
// The canonical artifacts and the relationships between them are read the way
// `yoyo artifact` reads them; the documentation's own cross-references the way
// `internal/doclink` resolves them; the architectural invariants the way the
// store that delivers them into a run reads them; the goals and what every
// admitted work item says it serves the way `yoyo goals attribution` judges
// them; and what a change upstream left unanswered downstream the way `yoyo
// stale` surveys it. What is new is that they are asked together, in an order a
// workflow definition states rather than one Go control flow puts them in, and
// that the answer gates a tag.
//
// Nothing here writes to the repository or the tracker. Every check is a read of
// the checkout or of what was already read out of the tracker, which is what
// makes this safe to run as a gate: a cut that refuses leaves the checkout
// exactly as it found it, and the gate cannot be the thing that changed what it
// was about to judge.
//
// One thing is written, and it is outside both: the workflow instance, under the
// harness's own state root, recorded before the first check and again at every
// state boundary crossed. That is the runtime's guarantee rather than a check's
// side effect — see Assess in workflow.go — and it is what makes what a release
// was gated on readable afterwards instead of only having been printed once.
// Nothing prunes those records, so one accumulates per invocation.
//
// # What a divergence is, and what is only worth reading
//
// A finding carries the two apart. A mismatch is something the system records
// about itself that is no longer true — a document that is not the artifact it
// claims to be, a reference resolving to nothing, a work item naming a goal no
// goals document states — and any one of them refuses the tag. A note is a
// condition worth reading and worth nothing to fail on. Staleness is the whole
// of the second category and deliberately so: `yoyo stale` exits zero on
// purpose, because a harness that failed a build over an amendment would teach
// an operator not to amend, and a gate that quietly reversed that decision
// would be this package overruling one the product already took.
package conformance

import (
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/doclink"
	"github.com/mason-bryant/yoyodyne/internal/goal"
	"github.com/mason-bryant/yoyodyne/internal/invariant"
	"github.com/mason-bryant/yoyodyne/internal/staleness"
)

// The outcomes a check produces, by the names the definition maps. They are
// constants rather than strings written at each site so that an action naming an
// outcome and a definition routing one are naming the same thing, and so a
// definition that misspells one is refused by the executor rather than by
// nobody.
const (
	// OutcomeConforms is a check that found the system matching what it records.
	OutcomeConforms = "conforms"
	// OutcomeDiverges is a check that found something recorded and no longer
	// true. It is what refuses a tag.
	OutcomeDiverges = "diverges"
	// OutcomeNoted is a check that reports a condition and gates nothing. There
	// is one of these and it is the staleness survey; see the package comment for
	// why it is not permitted to become the first.
	OutcomeNoted = "noted"
)

// maxReportedMismatches bounds how many mismatches one finding lists. What
// somebody cutting a release needs is what to go and look at rather than an
// export, so the list is cut while the count stays exact — the same bound the
// staleness report already keeps for the same reason.
const maxReportedMismatches = 20

// Finding is what one check produced: the outcome the definition routes on, a
// line saying what was looked at and how it came out, and the detail behind it.
type Finding struct {
	// Step is the state the definition performed this check in, which is what a
	// reader matches against the definition they are holding. A definition names
	// its own states — `action:` is what selects the check — so this is whatever
	// the file called the state rather than anything this package decided, and
	// Outcome is what stamps it on. Until then it is the check's own name, which
	// is what a caller performing a check outside a sequence gets.
	Step string `json:"step"`
	// Check is the check that produced this finding, by the name this package
	// knows it under. It is carried beside the state because the two are not the
	// same fact: the state is the definition's, the check is the build's, and a
	// report that carried only one of them could not be read against both.
	Check   string `json:"check"`
	Outcome string `json:"outcome"`
	// Summary is what was checked and how it came out, in one line.
	Summary string `json:"summary"`
	// Mismatches are the things recorded and no longer true. A finding with any
	// of these diverges, and any diverging finding refuses the tag.
	Mismatches []string `json:"mismatches,omitempty"`
	// Notes are conditions worth reading that refuse nothing. They are kept apart
	// from the mismatches rather than folded in, because a report that mixed them
	// would make "what stopped the release" something a reader has to work out.
	Notes []string `json:"notes,omitempty"`
	// Truncated is how many mismatches are not listed, so a cut list never reads
	// as the whole answer.
	Truncated int `json:"truncated,omitempty"`
}

// Diverges reports a finding that refuses the tag.
func (f Finding) Diverges() bool { return f.Outcome == OutcomeDiverges }

// Sources is everything the checks judge, read once before the sequence runs.
//
// It is a value the caller assembles rather than something this package goes
// and fetches, for two reasons. The tracker is read through the client the rest
// of the harness reads it through, and which statuses count as admitted is a
// decision the backlog already owns — a second copy of it here would be a second
// answer to "what is in the backlog". And gathering once means one tracker read
// for a sequence that asks about the backlog twice.
type Sources struct {
	// Repository is the repository root every file check is rooted at.
	Repository string
	// IntentRepository is the repository the governed documents are read from:
	// the companion intent repository where the project keeps one, and
	// Repository otherwise.
	IntentRepository string
	// InvariantsDirectory is where the architectural invariants live, relative to
	// the intent repository's root, as the product's configuration states it.
	InvariantsDirectory string
	// Artifacts is every canonical document the repository records, with whatever
	// could not be read as one and whatever is wrong with the ones that were.
	Artifacts artifact.Set
	// Goals is what the goals documents state, collected against those artifacts.
	Goals goal.Set
	// Admitted is the work the tracker holds as admitted and unfinished.
	Admitted []beads.WorkItem
	// ArtifactsUnreadable and TrackerUnreadable say why a source is absent, so a
	// check judging nothing never reports as a check that found nothing wrong.
	// Both are empty on an ordinary read.
	ArtifactsUnreadable string
	TrackerUnreadable   string
}

// Gather assembles the sources from a repository and the work already read out
// of the tracker.
//
// A tracker nobody could read is passed in as the reason rather than as an empty
// backlog, because the two lead to opposite conclusions about the same release:
// one is a product with nothing outstanding and the other is a gate that checked
// nothing and must say so.
func Gather(repository string, product config.Product, admitted []beads.WorkItem, trackerUnreadable string) Sources {
	sources := Sources{
		Repository:          repository,
		IntentRepository:    product.IntentRoot(repository),
		InvariantsDirectory: product.Invariants,
		Admitted:            admitted,
		TrackerUnreadable:   trackerUnreadable,
	}
	artifacts, err := artifact.StoreFor(repository, product).Load()
	if err != nil {
		// The goals are collected out of the artifacts, so an unreadable artifact
		// set costs both. Saying why beats an empty set that would report every
		// admitted item as attributed to nothing in particular.
		sources.ArtifactsUnreadable = err.Error()
		sources.Goals = goal.Unreadable(err.Error())
		return sources
	}
	sources.Artifacts = artifacts
	sources.Goals = goal.Collect(sources.IntentRepository, artifacts)
	return sources
}

// intentRepository is where the governed documents are read from. Sources
// assembled by hand rather than by Gather name no intent repository, and read
// them in the project's repository, as every project did before one could be
// kept elsewhere.
func (s Sources) intentRepository() string {
	if s.IntentRepository != "" {
		return s.IntentRepository
	}
	return s.Repository
}

// Assessment is one release-readiness assessment in progress: what the checks
// read, and what each of them has found so far.
//
// It is the subject the registered actions act on and the workflow executor
// carries, which is why the findings accumulate on it rather than being returned
// from each action: an action's door returns an error and not yet an outcome, so
// what a step produced is read off the subject afterwards. See Outcome.
type Assessment struct {
	sources  Sources
	findings []Finding
	// read is how many findings have been read as an outcome. It is here so that
	// a step whose action recorded nothing is refused rather than silently handed
	// the outcome of the step before it.
	read int
}

// New is an assessment over gathered sources, before any check has run.
func New(sources Sources) *Assessment {
	return &Assessment{sources: sources}
}

// Findings is every check that has run, in the order the sequence ran them.
func (a *Assessment) Findings() []Finding {
	return append([]Finding(nil), a.findings...)
}

// Conforms reports whether nothing found so far refuses a tag. An assessment
// that has run no checks conforms trivially, which is why what a caller acts on
// is the terminal the workflow reached rather than this.
func (a *Assessment) Conforms() bool {
	for _, finding := range a.findings {
		if finding.Diverges() {
			return false
		}
	}
	return true
}

// Outcome is what the state just performed produced, in the form the workflow
// executor asks for it, and where the state's own name is put on the finding it
// produced.
//
// It is a function over the subject rather than a return value from the action
// because that is the shape the runtime has: a registered action performs and
// returns an error, and the descriptor that would let it declare and return a
// typed outcome is a later milestone. Reading it here keeps the outcome the
// definition branches on the same value the report carries.
//
// Stamping the state rather than checking it is the whole of what makes a
// definition free to name its states. A state selects a check with `action:`,
// and what it is called is the definition's business: a project whose sequence
// says `check-artifacts: {action: conformance.artifacts}` gets a report that
// says `check-artifacts`, because that is the word in the file the reader is
// holding. Comparing the two instead would have made every state name in every
// project's definition a value this package had to have agreed to in advance.
func Outcome(state string, assessment *Assessment) (string, error) {
	if assessment.read >= len(assessment.findings) {
		// Every check records exactly one finding, so this is a defect in this
		// package rather than in any definition — and a stale outcome silently
		// reused would send an instance somewhere on the strength of the step
		// before it.
		return "", fmt.Errorf("the state %q performed its check and recorded no finding", state)
	}
	assessment.findings[assessment.read].Step = state
	finding := assessment.findings[assessment.read]
	assessment.read++
	return finding.Outcome, nil
}

// record appends one finding, deciding its outcome from whether anything
// mismatched. The decision is here rather than at each check so that "a mismatch
// refuses the tag" is one sentence in one place.
//
// The step starts as the check's own name and stays that way only for a caller
// performing a check outside a sequence; inside one, Outcome replaces it with
// the state the definition actually performed it in.
func (a *Assessment) record(check, summary string, mismatches, notes []string) {
	finding := Finding{Step: check, Check: check, Summary: summary, Notes: notes, Outcome: OutcomeConforms}
	if len(mismatches) > 0 {
		finding.Outcome = OutcomeDiverges
		finding.Mismatches = mismatches
		if len(mismatches) > maxReportedMismatches {
			finding.Mismatches = mismatches[:maxReportedMismatches]
			finding.Truncated = len(mismatches) - maxReportedMismatches
		}
	}
	a.findings = append(a.findings, finding)
}

// The checks, by the names this package knows them under. They are what a
// finding carries as its check and what the shipped definition happens to name
// its states after; a project's own definition may call its states anything, and
// what selects a check is the registered action rather than either of these.
const (
	CheckArtifacts  = "artifacts"
	CheckReferences = "references"
	CheckInvariants = "invariants"
	CheckGoals      = "goals"
	CheckStaleness  = "staleness"
)

// checkArtifacts holds the canonical documents to what the chain requires of
// them: that every file in an artifact home is an artifact, that every
// relationship one declares reaches something, and that every change recorded in
// one was recorded by the role that owns it.
//
// A set that could not be read at all is a mismatch rather than an error. The
// question the gate asks is whether the system can demonstrate it matches what it
// records, and a record nobody could read is a no — reporting it as a broken
// check instead would truncate the report at the first unreadable thing and hide
// everything after it.
func (a *Assessment) checkArtifacts() error {
	if reason := a.sources.ArtifactsUnreadable; reason != "" {
		a.record(CheckArtifacts, "the recorded artifacts could not be read, so nothing was held against them",
			[]string{"the artifacts could not be read: " + reason}, nil)
		return nil
	}
	set := a.sources.Artifacts
	var mismatches []string
	for _, problem := range set.Problems {
		mismatches = append(mismatches, "not an artifact: "+problem.String())
	}
	for _, problem := range set.ReferenceProblems {
		mismatches = append(mismatches, string(problem.Kind)+": "+problem.String())
	}
	summary := fmt.Sprintf("%d artifact(s) across %d home(s)", len(set.Artifacts), len(set.Homes))
	if len(mismatches) == 0 {
		summary += ", every reference resolving and every revision recorded by the role that owns it"
	}
	a.record(CheckArtifacts, summary, mismatches, nil)
	return nil
}

// checkReferences resolves the links the documentation makes to itself: the
// relative paths and the heading fragments a reader would follow, which the
// artifact layer's own reference check says nothing about.
func (a *Assessment) checkReferences() error {
	problems, err := doclink.Check(a.sources.Repository)
	if err != nil {
		a.record(CheckReferences, "the repository's documents could not be walked, so no link was resolved",
			[]string{"the documentation could not be read: " + err.Error()}, nil)
		return nil
	}
	documents, err := doclink.Documents(a.sources.Repository)
	if err != nil {
		a.record(CheckReferences, "the repository's documents could not be counted",
			[]string{"the documentation could not be read: " + err.Error()}, nil)
		return nil
	}
	var mismatches []string
	for _, problem := range problems {
		mismatches = append(mismatches, problem.String())
	}
	summary := fmt.Sprintf("%d document(s)", len(documents))
	if len(mismatches) == 0 {
		summary += ", every link they make to each other resolving"
	}
	a.record(CheckReferences, summary, mismatches, nil)
	return nil
}

// checkInvariants reads the architectural invariants the way the harness reads
// them when it delivers them into a run, and reports whatever it could not read
// as one.
//
// What it answers for is the set rather than the code: whether every constraint
// the repository records is one a run would actually be given. Whether the code
// satisfies each constraint is a judgement no check in this repository makes —
// it is what a reviewer is asked, with the relevant invariants in front of them —
// and a gate claiming otherwise would be the most expensive kind of green.
func (a *Assessment) checkInvariants() error {
	set, err := invariant.Store{RepositoryRoot: a.sources.intentRepository(), Directory: a.sources.InvariantsDirectory}.Load()
	if err != nil {
		a.record(CheckInvariants, "the invariants could not be read, so nothing says which constraints a run would be delivered",
			[]string{"the invariants could not be read: " + err.Error()}, nil)
		return nil
	}
	var mismatches []string
	for _, problem := range set.Problems {
		mismatches = append(mismatches, "not an invariant: "+problem.Path+": "+problem.Reason)
	}
	summary := fmt.Sprintf("%d active and %d retired invariant(s) in %s", len(set.Active), len(set.Retired), set.Directory)
	if len(mismatches) == 0 {
		summary += ", every one of them readable"
	}
	a.record(CheckInvariants, summary, mismatches, nil)
	return nil
}

// checkGoals holds the last link of the chain: that the goals are readable, and
// that every admitted work item's attribution resolves to one of them.
//
// The rule for which attribution refuses is the audit's own, in
// goal.Attribution.Divergent — an item naming a goal no goals document states,
// and an item whose recorded attribution was destroyed. Work admitted before
// attributions were checked names none, is grandfathered there, and is
// grandfathered here for the same reason: a gate that started failing legacy
// items would refuse every release until a backlog nobody has had the chance to
// attribute is attributed.
//
// What is not grandfathered is having checked nothing. A tracker nobody could
// read, or goals nobody could read, means no attribution was judged, and a
// release cut on that would be claiming a chain it never followed.
func (a *Assessment) checkGoals() error {
	var mismatches, notes []string
	if reason := a.sources.TrackerUnreadable; reason != "" {
		mismatches = append(mismatches, "the work tracker could not be read, so no admitted item's attribution was checked: "+reason)
	}
	goals := a.sources.Goals
	if reason, uncheckable := goals.Uncheckable(); uncheckable {
		mismatches = append(mismatches, "no attribution could be checked: "+reason)
	}
	for _, problem := range goals.Problems {
		mismatches = append(mismatches, "goals not read: "+problem.String())
	}
	// A goal that does not link back to the brief, and one the file states across
	// two lines, are reported where the audit reports them: beside the goals
	// rather than instead of them. The goal is still stated and work still traces
	// to it, so neither refuses a release — what is wrong is the chain above it or
	// the way the file writes it down.
	for _, problem := range goals.LinkProblems {
		notes = append(notes, "goal not linked to the brief: "+problem.String())
	}
	for _, problem := range goals.WrapProblems {
		notes = append(notes, "goal not written on one line: "+problem.String())
	}
	// An identity two goals in force share is reported here for the same reason
	// and in the same place. Every attribution already made still resolves, so it
	// refuses no release; what it costs is that work naming that identity from now
	// on is refused, which is the owner of those documents' to fix.
	for _, problem := range goals.IdentityProblems {
		notes = append(notes, "goal identity stated twice: "+problem.String())
	}

	attributed := 0
	for _, item := range a.sources.Admitted {
		attribution := goals.AttributionOf(item.Notes, item.GoalWitness)
		if attribution.Resolved() {
			attributed++
		}
		if attribution.Divergent() {
			mismatches = append(mismatches, fmt.Sprintf("%s [p%d, %s] %s: %s",
				item.ID, item.Priority, item.Status, item.Title, attribution.Reason))
		}
	}
	summary := fmt.Sprintf("%d admitted item(s), %d serving a recorded goal", len(a.sources.Admitted), attributed)
	if len(mismatches) == 0 {
		summary += ", none naming a goal the goals do not state or having lost the one it recorded"
	}
	a.record(CheckGoals, summary, mismatches, notes)
	return nil
}

// surveyStaleness reports what a change to an upstream document left unanswered
// downstream, and refuses nothing.
//
// It is in the sequence because a release is exactly when somebody wants to know
// which designs and which admitted work were pulled under wording that has since
// moved, and it is the one step that cannot produce a mismatch. See the package
// comment: `yoyo stale` exits zero on purpose and this does not overrule it.
func (a *Assessment) surveyStaleness() error {
	report := staleness.Survey(a.sources.Artifacts, a.sources.Goals, a.sources.Admitted)
	var notes []string
	for _, document := range report.Documents {
		notes = append(notes, fmt.Sprintf("%s [%s] %s is downstream of %d change(s) nobody has answered",
			document.ID, document.Kind, document.Path, len(document.Changes)))
	}
	for _, item := range report.WorkItems {
		notes = append(notes, fmt.Sprintf("%s [p%d, %s] %s was admitted under wording that has moved since",
			item.ID, item.Priority, item.Status, item.Title))
	}
	for _, unjudged := range report.Unjudged {
		notes = append(notes, fmt.Sprintf("%s could not be judged for staleness: %s", unjudged.WorkItemID, unjudged.Reason))
	}
	for _, contradiction := range report.Contradictions {
		notes = append(notes, "contradiction: "+contradiction.Reason)
	}
	summary := fmt.Sprintf("%d of %d admitted item(s) judged; %d document(s) and %d item(s) are downstream of a change nobody has answered",
		report.Judged, report.Admitted, len(report.Documents), len(report.WorkItems))
	if len(report.Contradictions) > 0 {
		summary += fmt.Sprintf("; %d contradiction(s) between documents of the product's intent", len(report.Contradictions))
	}
	if !report.Anything() {
		summary = fmt.Sprintf("%d of %d admitted item(s) judged; nothing is downstream of a change nobody has answered",
			report.Judged, report.Admitted)
	}
	// Recorded here rather than through record, which decides between conforming
	// and diverging: this is the one check that does neither, and routing it
	// through the decision would have it report as conforming rather than as
	// having reported.
	a.findings = append(a.findings, Finding{
		Step:    CheckStaleness,
		Check:   CheckStaleness,
		Outcome: OutcomeNoted,
		Summary: summary,
		Notes:   notes,
	})
	return nil
}
