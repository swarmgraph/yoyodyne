package orchestrator

// What a developer claims its change does to the work item is read here, and it
// is the one channel out of a developer that decides something about the run:
// closure follows the claim rather than the integration. A run that lands
// evidence integrates exactly as any other run does and leaves its item open.

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/landing"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// maxLandingProblemBytes keeps an unreadable claim to a readable line of the
// record, the same bound a lost report is held to.
const maxLandingProblemBytes = 512

// claimLanding takes the developer's landing claim out of what it said and
// records it, returning the rest for the channels that read the reply after it.
//
// Every attempt overwrites what the last one claimed rather than accumulating:
// the claim describes the change as it now stands, and a repair attempt that
// finished the work must not be closed against the previous attempt's evidence
// claim — nor the reverse.
func (a *activeRun) claimLanding(ctx context.Context, text string) string {
	rest, claim, err := landing.Extract(text)
	a.state.LandingOutcome = ""
	a.state.LandingReason = ""
	a.state.LandingBlockedBy = ""
	a.state.LandingImpedimentProblem = ""
	a.state.LandingProblem = ""
	a.outcome.Landing = ""
	a.outcome.LandingReason = ""
	a.outcome.LandingBlockedBy = ""
	a.outcome.LandingImpedimentProblem = ""
	a.outcome.LandingProblem = ""
	if err != nil {
		// An unreadable claim is not swallowed the way an unreadable report is. A
		// report decides nothing, so losing one costs the operator a note; this
		// decides whether the item closes, and the developer that wrote a block was
		// trying to say something about that. Recording the problem withholds the
		// closure, which leaves an item open for somebody to settle rather than
		// closing one on a claim nobody could read.
		a.state.LandingProblem = singleLine(err.Error(), maxLandingProblemBytes)
		a.outcome.LandingProblem = a.state.LandingProblem
		return rest
	}
	if !claim.Made() {
		return rest
	}
	a.state.LandingOutcome = string(claim.Outcome)
	a.state.LandingReason = claim.Why
	a.outcome.Landing = claim.Outcome
	a.outcome.LandingReason = claim.Why
	// The marker is recorded only where it decides something. A discharging claim
	// that carried one is refused before it reaches here, and storing it against a
	// closure that then happens anyway would leave the record saying the item both
	// closed and waits.
	//
	// It is resolved against the tracker here rather than acted on at the
	// settlement, because everything between the two reads it: the notes recorded
	// on the item, what the reviewer is shown, and what a surface says became of
	// the item are all written before the settlement runs. Resolving once, at the
	// point the untrusted text is read, is what keeps all of them saying where the
	// item actually went.
	if !claim.Discharges() {
		impediment, problem := a.resolveImpediment(ctx, claim.Impediment())
		a.state.LandingBlockedBy = impediment
		a.state.LandingImpedimentProblem = problem
		a.outcome.LandingBlockedBy = impediment
		a.outcome.LandingImpedimentProblem = problem
	}
	return rest
}

// resolveImpediment decides whether the work a landing named is work this item
// can be made to wait on, and says why where it is not. An empty answer is the
// parking, which is the disposition that holds an item back without needing
// anything to be true of the tracker.
//
// The marker arrives having been checked for shape and nothing else, and the
// shape is not the part that matters here. Two different things go wrong with a
// name, and both end in the same place. A marker the tracker refuses a
// dependency for — work it does not have, work that already waits on this item —
// fails the settlement of a run whose change is already integrated, leaving the
// item claimed with nothing watching it. A marker the tracker accepts and that
// holds nothing back — work that is already closed — is worse than that, because
// it looks like it worked: the item goes back unparked behind a dependency that
// is already satisfied, and the next pull selects it for another run of the same
// diagnosis, which is the loop the parking default exists to close.
//
// So the marker is only usable where the tracker has the work, the work is not
// this item, the work is not finished, and the work does not already wait on this
// item. Anything else takes the parking, which holds the item back whatever is
// true of the tracker.
//
// A tracker that could not answer is treated as a tracker that has no such item,
// and the wording says only that: the harness cannot tell an item that is absent
// from a store it could not reach, and both are reasons not to write a dependency
// on the strength of the name.
func (a *activeRun) resolveImpediment(ctx context.Context, impediment string) (string, string) {
	if impediment == "" {
		return "", ""
	}
	named := "its landing named " + impediment + " as the impediment"
	if impediment == a.state.WorkItemID {
		return "", "its landing named this item itself as the impediment, and nothing waits on itself"
	}
	item, err := a.pipeline.Tracker.Show(ctx, impediment)
	if err != nil {
		return "", named + " and the tracker did not confirm that item"
	}
	// Finished work holds nothing back. Every dependency gate in the harness reads
	// a closed blocker as no blocker, so a marker naming one is a marker that
	// leaves the item in the queue — the answer the parking has to take instead.
	if item.Status == closedStatus {
		return "", named + " and that work is already closed, so waiting on it would hold this item back for nothing"
	}
	// Work that already waits on this item cannot also be waited on by it. The
	// tracker refuses the second edge as a cycle, and follow-on items that depend
	// on the item they follow make this shape ordinary rather than exotic.
	if waitsOn(item, a.state.WorkItemID) {
		return "", named + " and that work already waits on this item, so the two would wait on each other"
	}
	return impediment, ""
}

// applyUndischargedDisposition takes back onto the run what the settlement
// decided about where its item goes. The marker is resolved when the claim is
// read, and the one thing that can still move it afterwards is a tracker that
// refuses the dependency at the settlement itself. Both halves of the record take
// the correction, because a run whose fields still named the marker would tell
// every surface deriving a disposition from them that the item waits on work it
// was in fact parked for.
func (a *activeRun) applyUndischargedDisposition(settled runstate.State) {
	a.state.LandingBlockedBy = settled.LandingBlockedBy
	a.state.LandingImpedimentProblem = settled.LandingImpedimentProblem
	a.outcome.LandingBlockedBy = settled.LandingBlockedBy
	a.outcome.LandingImpedimentProblem = settled.LandingImpedimentProblem
}

// judgingDischarge is what a reviewer does with a landing that closes the item,
// and it is here rather than beside the claim because it is a direction to one
// reader. The same claim is written into the work item's notes, where a sentence
// telling somebody to ask for changes would be an instruction nobody is there to
// take — so what the claim says about itself is the vocabulary's, and what to do
// about it belongs to the prompt that asks.
//
// It is said for both discharging landings — the claim a developer wrote out and
// the default no reply carries — because the two carry the same risk: a change
// that is not the work, approved under a claim saying it is, records as done
// exactly what the change itself says was not.
//
// It deliberately never says "does not discharge". That phrase is how a reviewer
// is told the other landing, and a discharging claim carrying it would read as
// its own opposite.
//
// What it directs changed with yoyodyne-ifd.209.27, because the reviewer now has
// somewhere to put the judgement. It used to ask for changes so the developer
// could claim the evidence itself, which costs a repair round on a change that
// needs no work and depends on the next attempt remembering the block. The
// approval carries the kind now, so the reviewer settles it in one verdict — and
// the account the item is left with is the reviewer's own, which is the sentence
// it was already writing into its summary when yoyodyne-ifd.284 closed.
const judgingDischarge = "Judge the change against that claim: it closes the work item unless your own approval says otherwise. " +
	"A change offered as evidence that the work is not doable yet — a diagnosis, a record of what has to land first — is not the work, " +
	"and approving it as an implementation closes the item against its own evidence. Approve it as evidence instead, which keeps the " +
	"change and leaves the item open with your summary as the reason it is not to be started again."

// claimedLanding rebuilds the claim from the durable record, which is what the
// reviewer is shown and what a work item's notes are written from. It is read
// back rather than carried alongside so that a repair round, a resumed run, and
// a later sweep all describe the same claim from the same place.
func claimedLanding(state runstate.State) landing.Claim {
	return landing.Claim{
		Outcome: landing.Outcome(state.LandingOutcome),
		Why:     state.LandingReason,
		// The marker as the settlement reads it rather than as it was written, so a
		// reviewer told the item is left waiting is looking at an item that is.
		BlockedBy: state.LandingImpediment(),
	}
}

// describeLanding is what a reviewer is told about the claim, so a diagnosis is
// judged as a diagnosis rather than as a missing implementation. A claim that
// could not be read is described as that: the reviewer is the one reader who can
// still tell from the change itself which of the two it was looking at.
//
// It answers for every run rather than only for the ones that claimed something.
// The default closes the item and nobody writes it, so a reviewer shown only the
// written claims is shown nothing on nearly every run — which is how a diagnosis
// came to be approved by a reviewer that never knew approving it would close the
// item. `docs/diagnoses/yoyodyne-ifd-209-26-closure-routes.md` audits the
// closure routes this feeds.
//
// The direction is added here rather than carried in the claim's own words, and
// only for the landings that close something. Every branch of this function ends
// in one, because a reviewer shown a claim and not told what turns on it is being
// asked to decide something nobody named to it — which is the whole of the defect
// above.
func describeLanding(state runstate.State) string {
	if state.LandingProblem != "" {
		return "The developer claimed a landing outcome that could not be read: " + state.LandingProblem +
			"\nJudge the change on what it is, and say in your summary which of the two it looks like."
	}
	described := claimedLanding(state).Describe()
	if state.LandingDischarges() {
		return described + "\n" + judgingDischarge
	}
	return described
}

// Discharges reports whether this run closes its work item, from the outcome a
// caller was handed rather than from the durable record. It is the outcome's
// half of runstate.State.Discharges and answers over the same facts, so a
// surface describing a run from what it returned and one describing it from its
// record cannot disagree about whether the item was discharged.
func (o Outcome) Discharges() bool {
	return o.LandingDischarges() && o.ApprovalDischarges() && !o.Escalated()
}

// Escalated is the outcome's half of runstate.State.Escalated, answered over the
// same two facts: either role having said the work item cannot be met as it
// stands. A surface describing a run from what it returned and one describing it
// from its record must not disagree about that, because what an escalated run
// left behind is a decision somebody has to take.
func (o Outcome) Escalated() bool {
	return o.Landing == landing.OutcomeEscalate || o.ReviewDecision == review.DecisionEscalate
}

// EscalatedBy names the role that raised it, and EscalationReason is that role's
// own account. Both answer as the record does, and for the reason the record
// answers that way: the reviewer is the later of the two readers, so a run
// carrying its verb is one the reviewer raised.
func (o Outcome) EscalatedBy() domain.AgentRole {
	switch {
	case o.ReviewDecision == review.DecisionEscalate:
		return domain.RoleReviewer
	case o.Landing == landing.OutcomeEscalate:
		return domain.RoleDeveloper
	default:
		return ""
	}
}

func (o Outcome) EscalationReason() string {
	switch {
	case o.ReviewDecision == review.DecisionEscalate:
		return o.ReviewSummary
	case o.Landing == landing.OutcomeEscalate:
		return o.LandingReason
	default:
		return ""
	}
}

// LandingDischarges is the developer's half of that question, and
// ApprovalDischarges the reviewer's. They are separate because what a surface
// says about an item left open depends on which of the two readers said so.
func (o Outcome) LandingDischarges() bool {
	return o.Landing != landing.OutcomeEvidence && o.Landing != landing.OutcomeEscalate && o.LandingProblem == ""
}

func (o Outcome) ApprovalDischarges() bool {
	return o.ReviewApproves != review.ApprovesEvidence
}

// UndischargedAccount is what a surface says about an item a run left open: the
// words of whichever reader said the change is not the work, and what went wrong
// with the claim where the developer's could not be read.
//
// The developer is answered for first where both said it. The claim is the
// developer's account of its own change and it was made before the review, so it
// is the account a reader of the item is looking for; the reviewer's is on the
// item too, in the notes.
func (o Outcome) UndischargedAccount() string {
	if o.LandingProblem != "" {
		return "the landing outcome it claimed could not be read (" + o.LandingProblem + ")"
	}
	// An escalation is answered for first, because it is a different statement
	// from the two below it: they say this change is not the work, and it says no
	// change would be until somebody decides about the item.
	if o.Escalated() {
		return "its " + o.EscalatedBy().Title() + " raised this item as one that cannot be met as it stands, for the development manager to decide: " +
			strings.Join(strings.Fields(o.EscalationReason()), " ")
	}
	if o.LandingDischarges() && !o.ApprovalDischarges() {
		return "its reviewer approved the change as evidence rather than as the work this item asked for: " +
			strings.Join(strings.Fields(o.ReviewSummary), " ")
	}
	return strings.Join(strings.Fields(o.LandingReason), " ")
}

// UndischargedDisposition is where an item a run did not discharge was left, in
// the words every surface says it in. It is derived here rather than at each
// surface because the two dispositions are what an operator acts on differently:
// a parking waits for a person, and a dependency releases itself.
func (o Outcome) UndischargedDisposition() string {
	if o.Escalated() {
		return "is parked until the development manager decides what happens to it"
	}
	if impediment := strings.TrimSpace(o.LandingBlockedBy); impediment != "" {
		return "stays open waiting on " + impediment
	}
	return "is parked until somebody releases it"
}

// renderLandingNote is the claim as one line of a work item's notes, or nothing
// where the developer claimed nothing. It is folded to a single line because the
// notes are a list of them and a paragraph here would read as the run's summary.
func renderLandingNote(outcome Outcome) string {
	if outcome.LandingProblem != "" {
		return "Landing claim: unreadable (" + outcome.LandingProblem + "); this item was not closed against it"
	}
	if outcome.Landing == "" {
		return ""
	}
	if outcome.Landing == landing.OutcomeEscalate {
		return "Landing claim: escalate — " + strings.Join(strings.Fields(outcome.LandingReason), " ") +
			" (raised to the development manager to decide; nothing was integrated)"
	}
	line := fmt.Sprintf("Landing claim: %s — %s", outcome.Landing,
		strings.Join(strings.Fields(outcome.LandingReason), " "))
	// Where the claim asked for the item to wait on something rather than be
	// parked, the note says so: the two leave the item in different places, and
	// only one of them releases itself. A request the harness could not honour is
	// recorded too, because an item parked with no trace of it reads afterwards as
	// a developer that asked for nothing.
	switch {
	case strings.TrimSpace(outcome.LandingBlockedBy) != "":
		line += " (left open waiting on " + strings.TrimSpace(outcome.LandingBlockedBy) + ")"
	case strings.TrimSpace(outcome.LandingImpedimentProblem) != "":
		line += " (parked rather than left waiting: " + strings.TrimSpace(outcome.LandingImpedimentProblem) + ")"
	}
	return line
}

// undischargedLandingReason is what is written onto an item a run did not
// discharge. It names the run, so the evidence that landed can be found from the
// item, and the account of why, so the item does not read afterwards as work
// somebody abandoned.
//
// Whose account that is depends on which reader withheld the closure. A
// reviewer's approval of evidence is answered for in the reviewer's own words —
// its summary is the only account of that decision anybody wrote — and it is
// checked after the developer's claim because a run whose developer also said so
// is a developer's landing that the review agreed with rather than a second
// decision.
func undischargedLandingReason(state runstate.State) string {
	// The escalation is answered for before either of the two below, because it is
	// the only one of the three where nothing was integrated and where what the
	// item is waiting for is a person rather than a release. A reader told this in
	// the words of a landing would go looking for a change on the target branch.
	if state.Escalated() {
		return fmt.Sprintf("Yoyodyne run %s raised this item as one that cannot be met as it stands, so nothing was integrated and the item is parked with the escalation in front of the development manager to decide — replan, park, resequence, or redirect. The %s's account: %s",
			state.RunID, state.EscalatedBy().Title(), state.EscalationReason())
	}
	if state.LandingDischarges() && !state.ApprovalDischarges() {
		return fmt.Sprintf("Yoyodyne run %s integrated a change its independent reviewer approved as evidence rather than as the work this item asked for, so the item stays open with its change integrated and parked. The reviewer's account: %s",
			state.RunID, state.ReviewSummary)
	}
	if state.LandingProblem != "" {
		return fmt.Sprintf("Yoyodyne run %s integrated its change and claimed a landing outcome that could not be read, so this item was not closed against it: %s. The change is on %s; whether it discharges this item is a person's call.",
			state.RunID, state.LandingProblem, state.TargetBranch)
	}
	if impediment := state.LandingImpediment(); impediment != "" {
		return fmt.Sprintf("Yoyodyne run %s landed evidence for this item and did not discharge it, so the item stays open with its change integrated, waiting on %s. The developer's account: %s",
			state.RunID, impediment, state.LandingReason)
	}
	parked := fmt.Sprintf("Yoyodyne run %s landed evidence for this item and did not discharge it, so the item stays open with its change integrated and parked. The developer's account: %s",
		state.RunID, state.LandingReason)
	// A landing that asked to wait on something and could not says so here. It is
	// the operator's line: the request was the developer's, and by the time
	// anybody reads this the run that made it has ended.
	if problem := strings.TrimSpace(state.LandingImpedimentProblem); problem != "" {
		parked += fmt.Sprintf(" It was parked rather than left waiting because %s.", problem)
	}
	return parked
}

// undischargedParking is why an item a run did not discharge is not to be pulled
// again yet, in the one field selection actually reads. It is the parking
// default's whole substance: an item returned to the backlog bare is one the next
// pull selects, for another run and another diagnosis of the impediment the last
// one just diagnosed, and the run that made it is recorded as succeeded so no
// brake counts it either.
//
// It carries the account of whoever withheld the closure, because what a parking
// reason is for is the person deciding whether to release the item, and that
// account is the one sentence in the record that names what would release it. The
// reason is bounded to what the tracker holds as one value on one line; the whole
// of it is on the item as notes either way, which is where a truncated account
// can be read in full.
func undischargedParking(state runstate.State) domain.WorkItemParking {
	if !state.Parks() {
		return ""
	}
	reason := fmt.Sprintf("yoyodyne run %s landed evidence for this item rather than the work, and did not discharge it: %s",
		state.RunID, state.LandingReason)
	// An escalated item is parked in the words of whichever role raised it, and
	// says who releases it. That last part is what makes this parking different
	// from the others: every other one waits for anybody who reads it to decide,
	// and this one is already in front of somebody.
	if state.Escalated() {
		return domain.WorkItemParking(singleLine(
			runstate.RaiseParking(state.RunID, state.EscalationReason()), domain.MaxWorkItemParkingBytes))
	}
	// An item its reviewer left open is parked in the reviewer's words, because
	// they are the only account of that decision: the developer claimed the
	// ordinary landing, and the sentence a person deciding whether to pick this item
	// up needs is the one that says why the change was not the work.
	if state.LandingDischarges() && !state.ApprovalDischarges() {
		reason = fmt.Sprintf("yoyodyne run %s landed a change its independent reviewer approved as evidence rather than the work, so it does not discharge this item: %s",
			state.RunID, state.ReviewSummary)
	}
	// A landing that asked to wait on something and could not is parked with the
	// request named, so the parking does not read as one nobody asked anything
	// about. It goes after the account because the account is what a release is
	// decided on, and the whole of both is on the item as notes where a truncated
	// line can be read in full.
	if problem := strings.TrimSpace(state.LandingImpedimentProblem); problem != "" {
		reason += "; parked rather than left waiting because " + problem
	}
	if state.LandingProblem != "" {
		reason = fmt.Sprintf("yoyodyne run %s integrated a change for this item and claimed a landing outcome that could not be read (%s); a person has to say whether the change discharges it",
			state.RunID, state.LandingProblem)
	}
	return domain.WorkItemParking(singleLine(reason, domain.MaxWorkItemParkingBytes))
}

// settleUndischarged puts an item back in the backlog in the state its run's
// landing calls for: parked under the reason above, or open and waiting on the
// impediment the landing named. It is one function because both settlement sites
// — the run's own completion and the sweep that finishes an interrupted one —
// have to leave the item in the same place, and because neither of the two
// dispositions is the same act as the other.
//
// The dependency is recorded before the status, and that order is the whole of
// what stops the leave-open path being the bare openness it replaces: between
// making an item open and making it wait, a watch session polling the queue pulls
// it. The item is still claimed while the dependency is added, so there is no
// such window.
//
// Settling twice settles once, which is not a nicety here: the reopen is retried
// where the tracker was busy, and a sweep re-runs the whole settlement of a run
// whose reopen never landed. So the dependency is added only where the item does
// not already carry it, rather than added again and refused as a duplicate.
//
// A dependency the tracker refuses for any other reason takes the parking rather
// than failing the settlement. The reasons that can be seen were decided when the
// claim was read — work the tracker does not have, work already finished, work
// that already waits on this item — but a cycle further round the graph than one
// step is not visible from one read, and neither is whatever the tracker refuses
// next year. Failing here is the worst of the answers available: the change is
// already promoted, so the run cannot be retried into a better state, and the
// item is left claimed with nothing watching it. Parking it holds it back and
// says on the item what the tracker would not do, which is something a person can
// act on. The run's own record takes the same correction, so a surface reading
// the run and a person reading the item are told the same disposition.
//
// A parking the item already carried is never lifted, and never lost. The
// leave-open path adds a dependency, which is not a release, and a settlement
// that cleared an operator's parking would retire their decision on the way past
// — so what the item already says is passed back through. The parking path does
// replace the reason, because both are parkings and the run's names what would
// release the item now; the one it superseded goes into the notes, so a decision
// somebody took is still readable off the item rather than only off whatever the
// tracker keeps of its own history.
//
// The settled state is returned because the fallback moves the run's own landing
// fields, and every surface derives the disposition from those. A caller holding
// a state it is about to record has to record the one this answers with, or it
// says the item stays open waiting on work the item was in fact parked for.
func settleUndischarged(ctx context.Context, tracker WorkTracker, state runstate.State) (runstate.State, error) {
	settled, item, err := arrangeUndischarged(ctx, tracker, state)
	if err != nil {
		return state, err
	}
	return settled, reopenUndischarged(ctx, tracker, settled, item)
}

// arrangeUndischarged is the half of the settlement that decides where the item
// goes: it makes the item wait on the impediment its landing named, and says why
// it could not where the tracker refused. It answers with the run's landing
// fields as they now stand and with the item as it was read.
//
// It is separable from the reopen below because the disposition is what a run's
// outcome notes are written from, and those are recorded before the item's status
// is settled. A caller that arranges first records a note saying where the item
// went; one that arranges as part of the settlement has already written the note
// by the time the tracker's refusal is known.
func arrangeUndischarged(ctx context.Context, tracker WorkTracker, state runstate.State) (runstate.State, beads.WorkItem, error) {
	settled := state
	impediment := state.LandingImpediment()
	// The item is read for what it already says about itself. The leave-open path
	// cannot proceed without that — both the duplicate check and the parking it
	// must not lift come from here — while the parking path uses it only to carry
	// a superseded reason into the notes, so a read that failed costs that sentence
	// rather than the settlement of an already-promoted change.
	item, shown := tracker.Show(ctx, state.WorkItemID)
	if shown != nil && impediment != "" {
		return state, beads.WorkItem{}, fmt.Errorf("read work item %s before making it wait on %s: %w", state.WorkItemID, impediment, shown)
	}
	if impediment != "" && !waitsOn(item, impediment) {
		if err := tracker.AddBlocker(ctx, state.WorkItemID, impediment); err != nil {
			settled.LandingBlockedBy = ""
			settled.LandingImpedimentProblem = fmt.Sprintf(
				"its landing named %s as the impediment and the tracker would not make this item wait on it (%v)", impediment, err)
		}
	}
	return settled, item, nil
}

// reopenUndischarged is the other half: the item goes back to the backlog in the
// disposition the arrangement above settled on. It takes the item that
// arrangement read rather than reading it again, so the parking it must not lift
// and the dependency it must not duplicate are decided from one reading.
func reopenUndischarged(ctx context.Context, tracker WorkTracker, settled runstate.State, item beads.WorkItem) error {
	reason, parking := undischargedReopening(settled, item)
	_, err := tracker.Reopen(ctx, settled.WorkItemID, reason, parking)
	return err
}

// undischargedReopening is what reopenUndischarged writes: the reason appended
// to the notes and the parking the item is left under.
func undischargedReopening(settled runstate.State, item beads.WorkItem) (string, domain.WorkItemParking) {
	reason := undischargedLandingReason(settled)
	parking := undischargedParking(settled)
	switch superseded := item.Parking.Reason(); {
	case !parking.Parked():
		// An empty parking here is the leave-open disposition rather than a release,
		// so the item keeps whatever parking it already had.
		parking = item.Parking
	case superseded != "" && superseded != parking.Reason():
		reason += " It replaces the parking reason this item already carried, which was: " + superseded
	}
	return reason, parking
}

// reopenUndischargedOnce is reopenUndischarged as a run makes it, under the
// run's window and read back before it is made again (recoveringTrackerWrite):
// a reopening appends its reason, so one made twice is the reason twice.
func (a *activeRun) reopenUndischargedOnce(ctx context.Context, item beads.WorkItem) error {
	reason, parking := undischargedReopening(a.state, item)
	tracker := a.pipeline.Tracker
	_, err := recoveringTrackerWrite(ctx, a,
		itemLanded(tracker, a.state.WorkItemID, func(reopened beads.WorkItem) bool {
			return reopened.Status == "open" && reopened.Parking.Reason() == parking.Reason() && beads.NotesEndWith(reopened.Notes, reason)
		}),
		func(ctx context.Context) (beads.WorkItem, error) {
			return tracker.Reopen(ctx, a.state.WorkItemID, reason, parking)
		})
	return err
}

// waitsOn reports a blocking dependency the item already carries on the named
// work. It reads the same relation the backlog's own readiness reads, so an item
// this calls settled is one the queue holds back.
func waitsOn(item beads.WorkItem, blockerID string) bool {
	for _, dependency := range item.Dependencies {
		if dependency.Type == blocksDependency && dependency.ID == blockerID {
			return true
		}
	}
	return false
}
