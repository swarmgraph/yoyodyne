package chat

// What the development manager decides about work that has stopped moving.
//
// The docket is delivered into this role's conversation because deciding is
// this role's, and until now that was the whole of the loop: an entry arrived,
// a decision was reasoned out in prose, and the prose went wherever the
// conversation went. Nothing carried the decision to the item it was about, so
// the next reader of a stopped run — a later conversation, a later operator —
// found the same evidence and none of the reasoning, and decided it again.
//
// So a decision is a recorded act rather than a paragraph. It names the
// stoppage it settles, it lands on the work item where anybody who reads the
// item finds it, and it lands on the item's durable triage record where the
// harness can read it — the decision, the reasoning, the role, and the turn it
// was recorded on — beside whatever budget it spends and in the same write. The
// three decisions that buy another attempt spend that budget as they are
// recorded, which is what makes the guards real: a repair grant, a re-run, and a
// re-arm each go through the same gate the counters already enforce, so an item
// nothing was ever going to stop is stopped by the cap rather than by whoever
// happens to be reading.
//
// The durable half is what an action carrying a decision out reads, and what the
// scheduling pass finds when it goes looking for decisions nobody has acted on.
//
// A decision also closes the entry it settled. The docket is rebuilt from
// durable records at every scan, so an entry nothing closed came back for ever,
// and the three decisions that spend no counter — waiting, re-scoping,
// escalating — left nothing behind that the harness could read as "somebody has
// looked at this". What guarded against deciding it a second time was prose in
// this role's contract telling it to go and read the item's notes.
//
// One decision moves a cap rather than spending one. Every override recorded in
// the week to 2026-09-06 was a cap refusing this role, an escalation, and an
// operator granting it within minutes under their own standing direction — so
// the operator step was latency rather than judgement, and the crossing is this
// role's now. It is narrow on purpose: far enough for the one decision that was
// refused, five times per item, and only with the argument for it, which lands on
// the item and reaches the operator in the channel as the crossing happens. That
// is a veto by reading rather than a permission to ask for. Past the five, and
// for any ceiling beyond the one that permits the decision, the caps are the
// operator's again and the refusal says so.
//
// Escalation is the one decision that asks the operator for something, and it is
// deliberately more than prose: a durable blocker on the item, so the item
// itself says it is waiting on a person, and a report at warning severity or
// above, so it reaches the pile the operator reads. A conversation that only
// said "somebody should look at this" is how the four hand surgeries this
// workflow exists to replace were found in the first place — late, and by
// accident.
//
// What this package does not do is carry the decision out. Nothing here starts a
// run, hands a developer a grant, or asks a forge for anything: this is a role
// deciding, and causing work is the harness's own hand. The record and the budget
// are what that hand acts on, and there are two acts, opposite to each other: a
// re-run starts the item over and records this decision as why the fresh run
// exists, and a repair re-enters the stopped run's own repair loop on the grant
// recorded here. Both read the intake hold and prove the stoppage is over first.
//
// The one decision about a run that has not stopped is carried out as it is
// recorded. A stop names a run still in flight whose work she has decided is
// superseded, narrowed, or mis-launched, and what carries it out is the same
// request the operator's stop writes, made on her behalf: the run reads it at
// its next boundary and ends itself cancelled with its change preserved, and
// the stoppage it leaves is docketed already settled by her decision. Asking a
// run to stop is not starting work, so it is not held behind the gates a repair
// or a re-run waits on; what it is held to is the run still being in flight.
//
// The hand is no longer a person's. Recording the decision is what causes it: the
// scheduling pass reads this record, fires one decision per pass under every gate
// either act already asks, and writes onto the item any gate that stopped it — so
// a decision that cannot be carried out says so on the docket rather than
// silently. `yoyo triage rerun` and `yoyo triage repair` are unchanged and are
// what an operator uses to fire one now rather than at the next pass. What made
// that possible is the durable half below: until it existed, the verb had to be
// handed the reasoning again as words on a command line and recorded them as the
// development manager's — an attribution anybody at a terminal could write, on the
// one record `selected-work-passes-intake-and-records-why` exists to make
// trustworthy.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// The decisions triage may record. They are the two decision trees the
// development manager works through — one for a run that stopped, one for a
// publication that did not finish — written as a closed vocabulary, because the
// harness acts on three of them and cannot act on prose. They are the durable
// record's own words rather than a second spelling of them: a decision recorded
// here is read back by the action that carries it out, so the two packages must
// not be able to disagree about what a decision is called.
const (
	// decisionRepair hands the item another bounded go at the change it already
	// has, on the branch and worktree the stopped run preserved.
	decisionRepair = runstate.TriageDecisionRepair
	// decisionRerun runs the item again from the start, which is what a correct
	// change whose ground moved needs.
	decisionRerun = runstate.TriageDecisionRerun
	// decisionRescope splits what was out of scope into a child of the item. The
	// parent's own criteria are not narrowed here: they are the product
	// manager's, and what this records is the decision plus the argument for
	// narrowing them.
	decisionRescope = runstate.TriageDecisionRescope
	// decisionRearm repeats an authorized merge request the forge dropped for a
	// transient cause.
	decisionRearm = runstate.TriageDecisionRearm
	// decisionWait is the decision that nothing is to be done yet: the forge
	// still has the merge, or the stopped run is waiting on something that will
	// move without a decision. It is recorded rather than left unsaid so the next
	// reader knows somebody looked.
	decisionWait = runstate.TriageDecisionWait
	// decisionEscalate hands the entry to the operator, which is the only
	// decision that asks a person for anything.
	decisionEscalate = runstate.TriageDecisionEscalate
	// decisionRetireRaise ends an item a role raised as unmeetable where the
	// owner's amendment made the raising run's change moot: the item goes back to
	// the queue to start from the target branch, and nothing lifts that change.
	// It answers a raise and nothing else.
	decisionRetireRaise = runstate.TriageDecisionRetireRaise
	// decisionStop stops a run still in flight whose work is superseded,
	// narrowed, or mis-launched, with its change preserved. It names a run that
	// has not stopped, which is the one way it differs from every decision above.
	decisionStop = runstate.TriageDecisionStop
	// decisionProceed lets a run in flight finish, answering the Lead Product
	// Manager's decision that its item is superseded, narrowed, or to be retired
	// where the run is worth finishing anyway. It asks nothing of the run.
	decisionProceed = runstate.TriageDecisionProceed
	// decisionCross raises one of the item's caps to just past what the item has
	// spent against it, on this role's own delegated authority, so a decision the
	// cap refused becomes one that can be recorded. It buys no attempt and spends
	// no budget of its own; what it spends is one of the five crossings the item
	// gets, and the reason it carries is reported to the operator as it is
	// recorded.
	//
	// It is this package's word rather than the durable record's, because it is
	// not a decision about a stoppage: the record holds one decision per stopped
	// run, and a crossing settles no run and supersedes nothing — what it writes
	// is an override, on the same record, beside the decisions rather than among
	// them.
	decisionCross = "cross"
)

// triageDecisions lists the vocabulary in the order the contract states it, so a
// refusal names exactly what was available.
var triageDecisions = append(runstate.TriageDecisionVocabulary(), decisionCross)

// triageSettles is which docketed stoppage each decision is an answer to, and
// therefore which entry it closes. It is a map from the vocabulary rather than a
// rule about the run, because the two decision trees are about different things:
// a repair, a re-run, and a re-scope answer a run — one that stopped, one that
// died before it claimed, or one a role escalated as unmeetable, which are the
// three entries a run can put on the docket under its own identifier — and a
// re-arm answers a publication the forge did not finish.
//
// A wait answers either. It began as the answer to a merge the forge still had,
// and is recorded on stopped runs as well, where it says nothing is to be done
// about this run yet; answering only a publication left every such wait closing
// nothing, and on 2026-10-01 eleven stopped runs she had waited on led every
// docket as though nobody had looked. It closes for a while rather than for
// good, whichever entry it answers.
//
// Escalating answers either, and closes both where a run has both. An escalation
// blocks the work item and hands it to the operator, so nothing about that run is
// the development manager's to decide until they answer — leaving half of it on
// her docket would put a question to her that she has already passed on.
//
// These classes say which entries a decision answers, not the whole of what it
// closes: the docket folds a run's open entries into one live entry, and a
// decision that answers any of them settles every open entry of that run, so
// what was folded beneath is not put to her again on its own.
//
// A decision whose class the run has no open entry of closes nothing, which is
// the safe direction: the entry stands and is put to her again, exactly as every
// entry did before closing existed. An item the tree is not ready for names no
// run and is closed by nothing here: the pull that finds it ready takes it off.
// An attempt that never became a run names none either, and is answered by the
// runless decisions below.
var triageSettles = map[string]triageSettlement{
	decisionRepair: {classes: runEntryClasses},
	// A re-run also answers a publication: handing the change back for a fresh run
	// is one of the two answers to a request nothing ever asked the forge to merge.
	decisionRerun:    {classes: append(slices.Clone(runEntryClasses), triage.ClassPublication)},
	decisionRescope:  {classes: runEntryClasses},
	decisionRearm:    {classes: []triage.Class{triage.ClassPublication}},
	decisionWait:     {classes: append(slices.Clone(runEntryClasses), triage.ClassPublication), revisit: true},
	decisionEscalate: {classes: append(slices.Clone(runEntryClasses), triage.ClassPublication)},
	// The two decisions about a run in flight answer the Lead Product Manager's
	// decision about it, which is the one entry a run has before it stops.
	decisionStop:    {classes: []triage.Class{triage.ClassProductDecision}},
	decisionProceed: {classes: []triage.Class{triage.ClassProductDecision}},
	// Retiring a raise answers the raise, and only a raise: it is refused on a run
	// that raised nothing before it gets this far.
	decisionRetireRaise: {classes: []triage.Class{triage.ClassEscalation}},
}

// runlessDecisions are the decisions that may be recorded on an attempt that
// never became a run, naming the item and no run. The harness settles such an
// attempt itself once the item is dispatched again (orchestrator's
// docketredispatch.go), so what is left for her is the attempt nothing has
// overtaken: waiting says nothing is to be done about it yet, and escalating
// hands it to the operator — a dirty primary checkout is the common cause, and
// only a person can clean it. Every other decision acts on a run, and is refused
// without one.
//
// Neither is written to the item's durable triage record, which holds one
// decision per run and is read only by the carry-outs of the decisions that
// spend; neither of these spends or is carried out. The note or blocker on the
// item and the closure on the docket are the whole of the record.
var runlessDecisions = map[string]bool{decisionWait: true, decisionEscalate: true}

// runlessClasses are the entries a runless decision answers.
var runlessClasses = []triage.Class{triage.ClassUnstartedAttempt}

// runEntryClasses are the docket entries a decision about a run answers: what a
// run can be docketed as under its own identifier, other than the publication of
// its work.
var runEntryClasses = []triage.Class{triage.ClassStoppedRun, triage.ClassUnstartedRun, triage.ClassEscalation}

// triageSettlement is what one decision does to the entry it answers.
type triageSettlement struct {
	classes []triage.Class
	// revisit says the decision holds for a while rather than settling anything.
	// Waiting is the only one: its whole content is "not yet", so an entry it
	// closed for good would be a stuck merge or a stopped run nobody ever looks at
	// again on the strength of a decision to look again. How long the harness
	// leaves it alone is the harness's, not this role's.
	revisit bool
}

// triageVerbs is what each decision records about itself on the work item. The
// item's notes are read by people and by later conversations rather than by
// this package, so what lands there is a sentence rather than the vocabulary
// word that produced it.
var triageVerbs = map[string]string{
	decisionRepair:   "Triaged: handed back for one bounded repair of the change it already has",
	decisionRerun:    "Triaged: to be run again from the start",
	decisionRescope:  "Triaged: re-scoped, with what was out of scope split out",
	decisionRearm:    "Triaged: its dropped merge to be re-armed once",
	decisionWait:     "Triaged: waiting, with nothing to be done about it yet",
	decisionEscalate: "Escalated to the operator by triage",
	decisionStop:     "Triaged: stopped in flight, with its change preserved",
	decisionProceed:  "Triaged: left to finish in flight",
	// A retired raise is written in its own sentence where it is carried out,
	// because what it did to the item's parking and status is the half a reader
	// needs; this is the entry the vocabulary check reads.
	decisionRetireRaise: "Triaged: its unmeetable raise retired, the raising run's change moot under the amended item",
	// The crossing's own sentence is built where it is recorded rather than taken
	// from here, because which cap was crossed and which of the five crossings this
	// was are the whole of what makes the note answerable. This is the fallback
	// nothing writes and the entry the vocabulary check reads.
	decisionCross: "Triaged: one of its caps crossed on the development manager's own authority",
}

// TriageBudgets is what one work item has already been given and may still be
// given. It is the durable per-item record every triage action goes through,
// satisfied by a caller that supplies the configured caps alongside the store:
// what an item may spend is a decision an operator wrote down, and a
// conversation is not the place that invents one.
//
// A conversation without it can still decide anything that spends nothing.
// What it cannot do is grant, re-run, or re-arm, because a budget that cannot be
// read must never be spent through as though it were empty.
//
// Every operation takes the decision itself rather than only the item, because
// the decision is what the spend is authorized by and the two are one durable
// write: an item's record cannot come to say a budget was spent without saying
// what was decided, by whom, and about which stoppage.
type TriageBudgets interface {
	// GrantRepair records a repair grant and reports what it came to, truncated
	// to the review rounds the item's cap still has room for.
	GrantRepair(ctx context.Context, workItemID string, decision runstate.TriageDecision) (runstate.RepairGrant, error)
	// RecordRerun records that triage caused this item to be run again.
	RecordRerun(ctx context.Context, workItemID string, decision runstate.TriageDecision) (runstate.TriageCounters, error)
	// RecordMergeRearm records that triage re-armed the merge the forge dropped
	// for the publication of one run. The run is what the decision names; which
	// publication that is, is the harness's to resolve from its own records,
	// because the budget a re-arm spends is that publication's rather than the
	// item's and a conversation must not be the thing that says which one it was.
	RecordMergeRearm(ctx context.Context, workItemID string, decision runstate.TriageDecision) (runstate.MergeRearmDecision, error)
	// RecordDecision records a decision that spends nothing, which is the other
	// half of the vocabulary. It is a separate operation because there is no
	// budget to write it beside: what makes those three atomic is the counter they
	// move, and these move none.
	RecordDecision(ctx context.Context, workItemID string, decision runstate.TriageDecision) (runstate.TriageCounters, error)
	// CrossCap raises one of this item's caps to just past what the item has spent
	// against it, on the development manager's own delegated authority, and reports
	// what the crossing came to. It is the one operation here that takes no
	// decision, because it records none: what it writes is an override, and the
	// decision it makes recordable is still recorded afterwards through one of the
	// operations above.
	// Whose authority it is recorded under is the caller's rather than this
	// conversation's, exactly as the sizes and the clock are: a conversation that
	// could name the role could name any of them.
	CrossCap(ctx context.Context, workItemID, budget, reason string) (runstate.TriageCrossing, error)
}

// TriageEntries is the docket the decisions are about, written the one way a
// decision writes to it: a settled stoppage is closed, so it stops being put to
// this role again.
//
// It is the other half of the lifecycle the docket never had. An entry is
// created where work stops and the docket is rebuilt from durable records at
// every scan, so without this a stoppage decided once came back for ever — and
// three of the six decisions that settle a stoppage spend no counter, which
// leaves nothing else the harness can read to tell a settled stoppage from a
// fresh one. The seventh word, the crossing, settles nothing and closes nothing.
//
// It is optional like the rest, and a conversation without one records the
// decision and leaves the entry standing, rather than appearing to have taken it
// off the docket.
type TriageEntries interface {
	// Close settles the docket entries of one stoppage and reports how many it
	// closed. An entry already closed, and a run with no open entry of the classes
	// the decision answers, are both nothing to do rather than failures.
	Close(ctx context.Context, closure DocketClosure) (int, error)
}

// ClosedItemEntries closes the docket entries standing for one work item once
// the item is closed or retired, and reports how many it closed. The reason is
// what the entries are closed with.
type ClosedItemEntries interface {
	CloseForItem(ctx context.Context, workItemID, reason string) (int, error)
}

// DocketClosure is one recorded triage decision as the docket takes it: which
// stoppage was decided, which of that run's entries the decision answers, and
// the reasoning to keep beside them. Who decided is filled in by the session,
// because a closure attributed to the harness rather than to the conversation
// that made it is a decision nobody can be asked about.
type DocketClosure struct {
	RunID string
	// WorkItemID names the item a decision naming no run is about, and is read only
	// where RunID is empty: such a decision answers the item's open entries of the
	// given classes that name no run.
	WorkItemID string
	Classes    []triage.Class
	Decision   string
	Reason     string
	// Revisit says this decision holds for a while rather than settling the
	// stoppage, which is what waiting is. How long is the harness's to decide, so
	// what travels from here is that the decision lapses and not when.
	Revisit bool
	// WaitsOn is the admitted work item a decision to wait depends on, where one
	// was named. Such a wait takes no window: it holds until that item is closed
	// or retired, and the harness puts the entry back once it is.
	WaitsOn   string
	DecidedBy string
}

// Stoppages is what the harness durably recorded about the runs triage decides
// about. Two things are read from it: the work item a run was made for, and
// where a work item's own change actually is.
//
// A decision names two things that have to agree — the item it lands on, and
// the run whose stoppage it settles — and a conversation working a docket of
// several entries is exactly where they come apart. Two entries transposed
// write each decision's reasoning onto the other item, and both then read as
// decided, which is worse than either reading as undecided: the reasoning is
// about a change nobody looking at that item can see.
//
// The second is the same records read for the other half of deciding about a
// stoppage, which is what gets carved out of it: a child written against a
// change that is still on a preserved branch has no substrate, and nothing in
// the tracker has ever known where a change is. See substrate.go.
//
// It is optional like the rest, and a conversation without one records a
// decision unchecked rather than appearing to have checked it, and decomposes
// without the substrate gate rather than appearing to have applied it.
type Stoppages interface {
	// WorkItemOf reports the work item the named run was made for.
	WorkItemOf(ctx context.Context, runID string) (string, error)
	// Raised reports the named run having ended by raising its item as one that
	// cannot be met as it stands. Such a run did not stop — raising is what it was
	// for — so the decisions that answer it are not the ones that answer a
	// stoppage, and this is what tells the two apart.
	Raised(ctx context.Context, runID string) (bool, error)
	// UnlandedChange reports the change one work item's own runs made that never
	// reached the integration target, and whether there is one at all. Work the
	// harness never ran, and work whose change is on the target branch, both
	// report none: neither leaves a child of it standing on anything missing.
	UnlandedChange(ctx context.Context, workItemID string) (UnlandedChange, bool, error)
}

// EscalationError reports an escalation that carried no report. It is the
// harness refusing rather than the provider failing, exactly as an authority
// refusal is: nothing in the block was carried out, the item was not blocked,
// and the prose the role wrote is still the operator's to read.
type EscalationError struct {
	WorkItemID string
}

func (e *EscalationError) Error() string {
	return fmt.Sprintf(
		"the development manager escalated %s without reporting it at %q severity or above; nothing was carried out, because an escalation the operator never sees is not an escalation",
		e.WorkItemID, report.SeverityWarning)
}

// triageProblems checks a triage decision as far as one action can be checked:
// it names a decision from the vocabulary, and it names the run whose stoppage
// it settles. Whether that run is on the docket needs the docket rather than the
// action, and is deliberately not asked: the entry the decision is about may
// have been cut from a bounded listing, and refusing a decision for that would
// refuse exactly the oldest stoppages nobody has got to yet. Whether the run is
// this item's own stopped work is asked, but where the run record can be read
// rather than here — see refuseTransposedStoppage.
func (a TrackerAction) triageProblems() []error {
	var problems []error
	switch decision := strings.TrimSpace(a.Decision); {
	case decision == "":
		problems = append(problems, fmt.Errorf("triage requires \"decision\", one of %s", strings.Join(triageDecisions, ", ")))
	case triageVerbs[decision] == "":
		problems = append(problems, fmt.Errorf("triage decision %q is not a decision; the decisions are %s",
			decision, strings.Join(triageDecisions, ", ")))
	}
	switch run := strings.TrimSpace(a.Run); {
	case run == "" && !runlessDecisions[strings.TrimSpace(a.Decision)]:
		problems = append(problems, fmt.Errorf("triage requires \"run\", the run the docket entry names; only %q and %q may leave it out, for an attempt that never became a run, which names none",
			decisionWait, decisionEscalate))
	case run == "":
	case !runstate.ValidRunID(run):
		problems = append(problems, fmt.Errorf("triage run %q is not a run identifier; a docket entry names the run it is about", run))
	}
	// The reasoning is required because it is what the decision is: it is recorded
	// as part of the durable decision, and a re-run of this stoppage carries it as
	// the fresh run's own account of why it exists. A decision recorded without it
	// would leave the harness with nothing to attribute the run to but its own
	// summary of somebody else's judgement. A crossing is held to it in its own
	// words below, because what its reason is for is different.
	if strings.TrimSpace(a.Reason) == "" && strings.TrimSpace(a.Decision) != decisionCross {
		problems = append(problems, errors.New("triage requires \"reason\", the reasoning the decision is recorded with; it is what a carry-out records as why the run it starts exists"))
	}
	problems = append(problems, a.crossingProblems()...)
	problems = append(problems, a.supersededProblems()...)
	problems = append(problems, a.waitsOnProblems()...)
	return problems
}

// waitsOnProblems holds the work item a wait names to the one decision that
// takes it. It is optional, since most waits are on something no work item
// stands for — a merge the forge still has — and where it is given it names an
// item other than the one the wait is about: an item's own closing already takes
// its entries off the docket.
func (a TrackerAction) waitsOnProblems() []error {
	waitsOn := strings.TrimSpace(a.WaitsOn)
	if waitsOn == "" {
		return nil
	}
	if strings.TrimSpace(a.Decision) != decisionWait {
		return []error{fmt.Errorf(
			"only the %q decision names \"waits_on\", and this one is %q; it is the admitted work item the wait depends on",
			decisionWait, strings.TrimSpace(a.Decision))}
	}
	if err := beads.ValidateIssueID(waitsOn); err != nil {
		return []error{fmt.Errorf("triage waits_on: %w", err)}
	}
	if waitsOn == strings.TrimSpace(a.ID) {
		return []error{errors.New("a wait does not wait on its own item, whose closing already takes its entries off the docket; name the item it depends on, or leave \"waits_on\" out")}
	}
	return nil
}

// refuseFinishedWaitsOn refuses a wait naming work the tracker does not hold as
// unfinished. A wait on an item that is already closed would come straight back
// on the next sweep, and one on an item that does not exist would never come
// back at all, so neither is recorded. Nothing has been spent when this is asked.
func (s *Session) refuseFinishedWaitsOn(ctx context.Context, waitsOn string) error {
	if waitsOn == "" {
		return nil
	}
	item, err := s.options.Tracker.Show(ctx, waitsOn)
	if err != nil {
		return fmt.Errorf("the work item the wait names, %s, could not be read, so nothing says it is unfinished work the wait can end on; nothing was recorded: %w", waitsOn, err)
	}
	if status := strings.TrimSpace(item.Status); status == closedWorkItemStatus {
		return fmt.Errorf("%s is already closed, so a wait on it would end at once; nothing was recorded. Decide the stoppage as it now stands, or name the unfinished item the wait depends on", waitsOn)
	}
	return nil
}

// waitVerb is the sentence a wait records on its item, naming the work it waits
// on where it names any.
func waitVerb(waitsOn string) string {
	if waitsOn == "" {
		return triageVerbs[decisionWait]
	}
	return "Triaged: waiting on " + waitsOn + ", with nothing to be done about it until that item is closed"
}

// supersededProblems holds the item a stop names as superseding its run to the
// one decision that takes it. It is optional there, since a run narrowed or
// launched by mistake was superseded by nothing, and where it is given it has to
// be an item other than the one whose run is being stopped.
func (a TrackerAction) supersededProblems() []error {
	superseded := strings.TrimSpace(a.SupersededBy)
	if superseded == "" {
		return nil
	}
	if strings.TrimSpace(a.Decision) != decisionStop {
		return []error{fmt.Errorf(
			"only the %q decision names \"superseded_by\", and this one is %q; it is the item doing a stopped run's work instead",
			decisionStop, strings.TrimSpace(a.Decision))}
	}
	if err := beads.ValidateIssueID(superseded); err != nil {
		return []error{fmt.Errorf("triage superseded_by: %w", err)}
	}
	if superseded == strings.TrimSpace(a.ID) {
		return []error{errors.New("a run is not superseded by its own item; name the item doing the work instead, or leave \"superseded_by\" out")}
	}
	return nil
}

// crossingProblems holds the one decision that names a budget to the budget
// vocabulary, and holds every other decision to naming none.
//
// The budget is required rather than inferred from whatever last refused, because
// two of the three decisions that spend a budget stand behind two of them: a
// crossing that guessed would raise one cap while the other went on refusing the
// same decision, which is the two-sittings-per-item failure the refusals were
// already reworded to end.
//
// The names are the store's own rather than a second spelling of them here. A
// refusal prints the list, so what a development manager types is the words the
// refusal used.
func (a TrackerAction) crossingProblems() []error {
	var problems []error
	budget := strings.TrimSpace(a.Budget)
	if strings.TrimSpace(a.Decision) != decisionCross {
		if budget != "" {
			problems = append(problems, fmt.Errorf(
				"only the %q decision names a \"budget\", and this one is %q; a cap is crossed by crossing it rather than as an argument to another decision",
				decisionCross, strings.TrimSpace(a.Decision)))
		}
		return problems
	}
	switch {
	case budget == "":
		problems = append(problems, fmt.Errorf("triage %q requires \"budget\", the cap being crossed: %s",
			decisionCross, strings.Join(runstate.TriageOverrideBudgets(), ", ")))
	case !slices.Contains(runstate.TriageOverrideBudgets(), budget):
		problems = append(problems, fmt.Errorf("triage budget %q is not a cap; the caps are %s",
			budget, strings.Join(runstate.TriageOverrideBudgets(), ", ")))
	}
	// The reason is required on every action that changes something, and it is
	// required again here in the crossing's own words. What makes this delegation
	// answerable is that the argument reaches the operator at the moment the cap is
	// crossed, so a crossing that carried none would be the one thing the operator
	// agreed to on condition it could not happen.
	if strings.TrimSpace(a.Reason) == "" {
		problems = append(problems, fmt.Errorf(
			"triage %q requires \"reason\", the justification for crossing the cap: it is recorded on the item and reported to the operator as the crossing happens, and a crossing nobody argued for is refused outright",
			decisionCross))
	}
	return problems
}

// TriageDecision reports what this reply recorded about the stoppage of one
// run: the decision, in the vocabulary above, and the reasoning recorded with
// it. It reports nothing for a turn that decided nothing about that stoppage,
// which is an answer rather than a failure — a development manager who read a
// stoppage and left it alone has answered.
//
// It is exported because the harness now delivers a stoppage into this
// conversation rather than waiting for somebody to carry it here, and what it
// has to write down afterwards is what came back. Reading the recorded actions
// is the only honest way to know: the prose of a reply can say anything, and
// what a decision is worth is that it was carried out against the item's durable
// triage budget. Only applied actions are read, so nothing here can report a
// decision the tracker refused.
func (r Reply) TriageDecision(runID string) (decision, reason string, found bool) {
	run := strings.TrimSpace(runID)
	if run == "" {
		return "", "", false
	}
	for _, outcome := range r.Actions {
		action := outcome.Action
		if !outcome.Applied || action.Action != actionTriage || strings.TrimSpace(action.Run) != run {
			continue
		}
		// A crossing is not what became of the stoppage. It raises a cap so that a
		// decision can be recorded, and the decision is a separate act in the same
		// reply or a later one — so reporting it here would say a stoppage had been
		// settled by the step taken before settling it. A turn that crossed and
		// decided nothing else reports nothing, which is the honest answer: the item
		// has more room and still has nothing decided about it.
		if strings.TrimSpace(action.Decision) == decisionCross {
			continue
		}
		return strings.TrimSpace(action.Decision), strings.TrimSpace(action.Reason), true
	}
	return "", "", false
}

// escalates reports a decision that asks a person for something, which is the
// one decision the harness holds to a further condition than the action itself
// carries.
func (a TrackerAction) escalates() bool {
	return a.Action == actionTriage && strings.TrimSpace(a.Decision) == decisionEscalate
}

// refuseUnreportedEscalation refuses an escalation the operator would never see.
// An escalation is a durable blocker and a report, and the report half cannot be
// checked where the action is validated, because it is in a different block of
// the same reply.
//
// The severity floor is warning rather than note for the reason the report
// vocabulary draws that line: a note asks for nothing, and an escalation asks
// for a person.
func refuseUnreportedEscalation(parsed parsedReply) error {
	// One escalating reply may cover several docket entries, and each blocked
	// item needs its own account: a single report satisfying every escalation
	// would leave the rest blocked with nothing reaching the operator about
	// them. So the count of warning-or-above reports must cover the count of
	// escalations, and the refusal names every item that would go unaccounted.
	var escalated []string
	for _, action := range parsed.Actions {
		if action.escalates() {
			escalated = append(escalated, strings.TrimSpace(action.ID))
		}
	}
	if len(escalated) == 0 {
		return nil
	}
	reported := 0
	for _, entry := range parsed.Reports {
		if entry.Severity == report.SeverityWarning || entry.Severity == report.SeverityCritical {
			reported++
		}
	}
	if reported >= len(escalated) {
		return nil
	}
	return &EscalationError{WorkItemID: strings.Join(escalated[reported:], ", ")}
}

// carryOutTriage records one triage decision. Whether the run it names is this
// item's stopped work is asked first, because a decision landing on the wrong
// item is a decision that should never have been paid for; the decision is then
// written to the item's durable record together with whatever budget it spends,
// in one write, and the note onto the tracker comes after both.
//
// The durable record is the decision and the note is its account for people. A
// note is prose nothing reads, which is why the harness carrying a decision out
// once had to be handed the reasoning again as words on a command line; what it
// reads now is what the role recorded here.
//
// A refusal from the budget is the gate doing its job rather than a failure of
// the conversation: the development manager is told which cap refused it and
// what it has left, which is exactly the evidence for escalating instead.
func (s *Session) carryOutTriage(ctx context.Context, outcome *TrackerOutcome) {
	action := outcome.Action
	id := strings.TrimSpace(action.ID)
	decision := strings.TrimSpace(action.Decision)
	run := strings.TrimSpace(action.Run)
	if run == "" {
		s.carryOutRunlessTriage(ctx, outcome, id, decision)
		return
	}
	if err := s.refuseTransposedStoppage(ctx, id, run); err != nil {
		outcome.fail(err)
		return
	}
	// A crossing is not one of the decisions the budget bounds — it is what raises
	// one of those budgets — so it is carried out on its own path rather than
	// through the record below. It still lands on the item, in a note naming the
	// cap, the crossing number, and the argument, which is the half of the
	// delegation that outlives the channel message beside it.
	if decision == decisionCross {
		s.carryOutCapCrossing(ctx, outcome, id, run)
		return
	}
	if decision == decisionStop {
		s.carryOutStop(ctx, outcome, id, run)
		return
	}
	if decision == decisionProceed {
		s.carryOutProceed(ctx, outcome, id, run)
		return
	}
	raised, err := s.refuseMisfitRaiseDecision(ctx, decision, run)
	if err != nil {
		outcome.refused(err)
		return
	}
	waitsOn := strings.TrimSpace(action.WaitsOn)
	if err := s.refuseFinishedWaitsOn(ctx, waitsOn); err != nil {
		outcome.refused(err)
		return
	}
	spent, err := s.recordTriageDecision(ctx, id, runstate.TriageDecision{
		Decision: decision,
		RunID:    run,
		Reason:   action.Reason,
		// Who decided it and where, which is what an attribution citing this
		// decision points at. They are the conversation's own facts rather than
		// anything the reply asserted: a role that could name itself could name
		// another.
		DecidedBy:    RoleTitle(s.state.Role),
		Conversation: s.state.ConversationID,
		Turn:         s.state.Turns,
		WaitsOn:      waitsOn,
	})
	if err != nil {
		outcome.refused(refusedPastCap(err))
		return
	}
	// The spend is durable from here on, and everything below it is a write to a
	// tracker that can fail. So it is written onto the outcome before the write
	// rather than after it: an action that fails now is an action with a spend
	// behind it, and a report saying it changed nothing is a report inviting the
	// spend to be made twice.
	if spent.landed != "" {
		outcome.noteLanded("%s", spent.landed)
	}
	verb := triageVerbs[decision]
	if decision == decisionWait {
		verb = waitVerb(waitsOn)
	}
	note := s.trackerProvenance(verb+", on the stopped work of run "+run, action.Reason)
	if decision == decisionEscalate {
		// An escalation is recorded as a blocker rather than as a note, because
		// the item itself has to say it is waiting on a person: a note leaves the
		// item looking like work in flight, which is the state this whole workflow
		// exists to stop somebody discovering by accident.
		if _, err := s.options.Tracker.Block(ctx, id, note); err != nil {
			outcome.fail(err)
			s.settleTrackerBlock(ctx, outcome, id, note)
			return
		}
		outcome.applied("escalated %s to the operator and blocked it, on the stopped work of run %s%s",
			id, run, s.closeDocketEntry(ctx, decision, run, action.Reason))
		return
	}
	if decision == decisionRetireRaise {
		s.carryOutRetiredRaise(ctx, outcome, id, run, note)
		return
	}
	if _, err := s.options.Tracker.Update(ctx, id, beads.WorkItemChange{AppendNotes: note}); err != nil {
		outcome.fail(err)
		s.settleTrackerNote(ctx, outcome, id, note, "the decision recorded on the item")
		return
	}
	lifted := ""
	if raised && decision == decisionRerun {
		lifted = "; it is a re-run of a raise, so it starts from the raising run's preserved change where its branch still stands, once the item's owner has amended the item and released the raise's parking"
	}
	outcome.applied("triaged %s as %q%s, on the stopped work of run %s%s%s%s",
		id, decision, waitsOnClause(waitsOn), run, spent.clause, lifted, s.closeDocketEntryWaiting(ctx, decision, run, action.Reason, waitsOn))
}

// carryOutRunlessTriage records a decision about an attempt that never became a
// run: onto the item, as a note or, for an escalation, a blocker, and then onto
// the docket, closing the item's open attempts. Validation has already held it
// to the decisions in runlessDecisions.
func (s *Session) carryOutRunlessTriage(ctx context.Context, outcome *TrackerOutcome, id, decision string) {
	action := outcome.Action
	waitsOn := strings.TrimSpace(action.WaitsOn)
	if err := s.refuseFinishedWaitsOn(ctx, waitsOn); err != nil {
		outcome.refused(err)
		return
	}
	verb := triageVerbs[decision]
	if decision == decisionWait {
		verb = waitVerb(waitsOn)
	}
	note := s.trackerProvenance(verb+", on an attempt to dispatch it that never became a run", action.Reason)
	if decision == decisionEscalate {
		if _, err := s.options.Tracker.Block(ctx, id, note); err != nil {
			outcome.fail(err)
			s.settleTrackerBlock(ctx, outcome, id, note)
			return
		}
		outcome.applied("escalated %s to the operator and blocked it, on an attempt to dispatch it that never became a run%s",
			id, s.closeRunlessDocketEntries(ctx, id, decision, action.Reason, ""))
		return
	}
	if _, err := s.options.Tracker.Update(ctx, id, beads.WorkItemChange{AppendNotes: note}); err != nil {
		outcome.fail(err)
		s.settleTrackerNote(ctx, outcome, id, note, "the decision recorded on the item")
		return
	}
	outcome.applied("triaged %s as %q%s, on an attempt to dispatch it that never became a run%s",
		id, decision, waitsOnClause(waitsOn), s.closeRunlessDocketEntries(ctx, id, decision, action.Reason, waitsOn))
}

// closeRunlessDocketEntries is closeDocketEntry for a decision that names an item
// and no run.
func (s *Session) closeRunlessDocketEntries(ctx context.Context, workItemID, decision, reason, waitsOn string) string {
	if s.options.Docket == nil {
		return ""
	}
	closed, err := s.options.Docket.Close(ctx, DocketClosure{
		WorkItemID: workItemID,
		Classes:    runlessClasses,
		Decision:   decision,
		Reason:     reason,
		Revisit:    triageSettles[decision].revisit,
		WaitsOn:    waitsOn,
		DecidedBy:  fmt.Sprintf("the %s in conversation %s", RoleTitle(s.state.Role), s.state.ConversationID),
	})
	settled := ""
	if closed > 0 {
		settled = fmt.Sprintf("; %d docket entry(s) of attempts at that item are closed", closed)
	}
	if err != nil {
		return settled + fmt.Sprintf("; a docket entry could not be closed and will be put to you again: %v", err)
	}
	if closed == 0 {
		// Said rather than left out, because a decision about an attempt that closed
		// nothing is one the docket will put to her again or never had: the attempt
		// was already settled, or the item has none.
		return settled + "; no open attempt at that item was on the docket, so nothing on it was closed"
	}
	return settled
}

// raiseDecisions is the sentence that names what answers a raise, said wherever
// a decision that does not is refused on one.
func raiseDecisions(runID string) string {
	return fmt.Sprintf(
		"run %s raised its item as one that cannot be met as it stands rather than stopping, so there is no stopped run here for a repair to continue; the two decisions that apply to an unmeetable raise are %q, once the item's owner has amended the item and released the raise's parking, which starts the item again from the raising run's preserved change where one stands, and %q, where the amendment makes that change moot and the item is to start from the target branch",
		runID, decisionRerun, decisionRetireRaise)
}

// refuseMisfitRaiseDecision refuses the decision that cannot be carried out on
// a raise, and the one that means nothing on anything else, and reports whether
// the run raised its item.
//
// A repair continues a run that stopped, and a run that raised its item did not
// stop: it succeeded at saying the item cannot be met. Recording one spends a
// repair grant nothing will ever carry out, which is what yoyodyne-ifd.437.13's
// repair of 2026-09-27 cost, so it is refused before anything is spent, in the
// sentence that names the two decisions that do apply. Retiring a raise is the
// converse: on a run that raised nothing there is nothing to retire.
//
// A conversation with no run records wired cannot tell, and records the
// decision unchecked, exactly as it records every other decision unchecked; the
// carry-out then refuses what it cannot act on.
func (s *Session) refuseMisfitRaiseDecision(ctx context.Context, decision, runID string) (bool, error) {
	if s.options.Stoppages == nil {
		return false, nil
	}
	if decision != decisionRepair && decision != decisionRetireRaise && decision != decisionRerun {
		return false, nil
	}
	raised, err := s.options.Stoppages.Raised(ctx, runID)
	if err != nil {
		return false, fmt.Errorf("whether run %s raised its item as unmeetable could not be read, so nothing was recorded and nothing was spent: %w", runID, err)
	}
	switch {
	case raised && decision == decisionRepair:
		return raised, fmt.Errorf("%s; nothing was recorded and nothing was spent", raiseDecisions(runID))
	case !raised && decision == decisionRetireRaise:
		return raised, fmt.Errorf("run %s did not raise its item as unmeetable, so there is no raise to retire; nothing was recorded", runID)
	}
	return raised, nil
}

// carryOutRetiredRaise ends a raise the development manager judged moot under
// the owner's amendment, and puts the item back in the queue to start from the
// target branch.
//
// The parking it lifts is the raise's own and no other: the raise placed it to
// hold the item for her decision, and this is that decision. A parking anybody
// else placed since is theirs, and is left exactly as it is with the item saying
// so. A blocked status the raise left — the escalation that followed it, most
// often — is cleared with it, because the raise is what it was holding the item
// for, and an item left reading blocked on nobody's account reads to every
// surface as waiting on somebody.
func (s *Session) carryOutRetiredRaise(ctx context.Context, outcome *TrackerOutcome, id, run, note string) {
	item, err := s.options.Tracker.Show(ctx, id)
	if err != nil {
		outcome.fail(fmt.Errorf("read %s before retiring the raise: %w", id, err))
		return
	}
	change := beads.WorkItemChange{AppendNotes: note}
	released := ""
	if raisedBy, raisedHere := runstate.RaisedBy(item.Parking.Reason()); raisedHere && raisedBy == run {
		unparked := domain.WorkItemParking("")
		change.Parking = &unparked
		released = "; the parking the raise placed is lifted"
	} else if item.Parking.Parked() {
		released = fmt.Sprintf("; its parking is not the raise's and was left as it is, so it is not selected until that parking is released: %s",
			singleLine(item.Parking.Reason(), maxTrackerFailureBytes))
	}
	if _, err := s.options.Tracker.Update(ctx, id, change); err != nil {
		outcome.fail(err)
		s.settleTrackerNote(ctx, outcome, id, note, "the retired raise recorded on the item")
		return
	}
	if strings.TrimSpace(item.Status) == blockedWorkItemStatus {
		cleared := s.trackerProvenance(fmt.Sprintf("Blocked status cleared: it was left by the unmeetable raise of run %s, which the development manager retired", run), "the raise it held the item for is retired")
		if _, err := s.options.Tracker.Unblock(ctx, id, cleared); err != nil {
			outcome.fail(fmt.Errorf("the raise is retired and recorded on %s, and its blocked status could not be cleared: %w", id, err))
			return
		}
		released += "; the blocked status it left is cleared"
	}
	outcome.applied("retired the unmeetable raise of run %s on %s%s, so the item starts from the target branch when it is next pulled and nothing lifts that run's change%s",
		run, id, released, s.closeDocketEntry(ctx, decisionRetireRaise, run, outcome.Action.Reason))
}

// settleTrackerNote asks the tracker whether a write it reported as failed
// nonetheless reached the item, and records the answer as something known to
// have landed or as something not known to have.
//
// A timeout is what it is for. The harness stopping its wait on bd says nothing
// about whether bd finished, so the failure it reports covers both a write that
// never happened and one that happened after nobody was listening — and on
// 2026-09-06 it was the second, on the triage note for yoyodyne-ifd.142. Reading
// the item back is one further bd call at a point where the action has already
// failed, which is cheap against what guessing costs: a decision reported as
// unrecorded is a decision asked for again, and the budget behind it spent twice.
//
// It never turns a failure into a success. What it settles is what the outcome
// says about the write, not whether the action was applied — the caller has
// already failed the outcome, and a write nobody can confirm is not a decision
// this conversation may report as recorded.
//
// The note is found by looking for it, which works because a provenance line
// names the conversation and the turn: the same decision recorded on an earlier
// turn does not match, so what is found is this write and not its predecessor.
func (s *Session) settleTrackerNote(ctx context.Context, outcome *TrackerOutcome, id, note, what string) {
	item, err := s.settlingRead(ctx, id)
	if err != nil {
		outcome.noteUnknown("%s; the tracker would not say whether the write reached %s: %s", what, id, err)
		return
	}
	if carriesNote(item, note) {
		outcome.noteLanded("%s; %s carries it, so the write landed and asking for it again would record it twice", what, id)
		return
	}
	outcome.noteUnknown("%s; reading %s afterwards does not find it, so it has to be recorded before anything reads the item as decided", what, id)
}

// settleTrackerBlock is settleTrackerNote for the write an escalation makes,
// which is not a note. Blocking is one bd invocation that does two things —
// `bd update --status=blocked --append-notes=<reason>`, in beads.Client.Block —
// so a blocker that landed leaves both marks on the item, and either of them is
// evidence the invocation ran. Settling this against the note alone would rest
// the whole answer on the half of that command whose absence proves least, on
// the one decision whose failure has a person waiting behind it.
//
// The status is only evidence where the item was not already blocked when the
// action started, which is what the reading taken before the write is for: an
// item somebody else blocked yesterday is blocked now for a reason that is not
// this escalation, and an escalation that never landed would then be reported as
// having done. A prior reading that failed says nothing either way, and the
// status is passed over rather than guessed at.
func (s *Session) settleTrackerBlock(ctx context.Context, outcome *TrackerOutcome, id, note string) {
	const what = "the blocker naming the operator"
	item, err := s.settlingRead(ctx, id)
	if err != nil {
		outcome.noteUnknown("%s; the tracker would not say whether the write reached %s: %s", what, id, err)
		return
	}
	blockedNow := strings.TrimSpace(item.Status) == blockedWorkItemStatus
	blockedBefore := outcome.TargetStatus == "" || outcome.TargetStatus == blockedWorkItemStatus
	if carriesNote(item, note) || (blockedNow && !blockedBefore) {
		outcome.noteLanded("%s; %s is blocked and carries it, so the write landed and escalating again would block it twice over", what, id)
		return
	}
	outcome.noteUnknown("%s; reading %s afterwards finds neither the blocker nor the reason, so nothing on the item yet says a person is waiting on it", what, id)
}

// carriesNote reports an item whose notes hold the write one action was making.
// It is a search because that is all the tracker offers, and it is exact enough
// to be one: a provenance line names the conversation and the turn, so the same
// decision recorded on an earlier turn does not match it.
func carriesNote(item beads.WorkItem, note string) bool {
	return strings.Contains(item.Notes, strings.TrimSpace(note))
}

// settlingRead reads an item back after a write to it failed, under a context of
// its own rather than the one the write ran under. Dropping the cancellation is
// the point: where the failure was this conversation's own deadline running out,
// a read taken under it fails before it reaches the tracker and settles nothing —
// which is the timeout case the settling exists for, answered by the one thing
// that cannot answer it. What bounds the read instead is the tracker client's own
// per-command timeout, which is what bounds every other call it makes.
func (s *Session) settlingRead(ctx context.Context, id string) (beads.WorkItem, error) {
	return s.options.Tracker.Show(context.WithoutCancel(ctx), id)
}

// closeDocketEntry takes the stoppage this decision settled off the docket, and
// says what that came to.
//
// It happens after the decision has landed on the work item rather than before,
// which is the opposite order to the budget above and is the same reasoning: an
// entry closed on a decision the item never recorded is a stoppage nobody is
// looking at any more and nothing saying what was decided about it, while a
// decision recorded whose entry stayed open is a stoppage put to somebody twice —
// and the second of those is the state every entry was in before closing existed.
//
// A closure that could not be written is said in the outcome rather than failing
// the action. The decision is recorded, the budget is spent, and what is left is
// an entry that will be asked about again; reporting the action as failed would
// invite exactly the second decision the closure exists to prevent.
//
// What was closed is said beside what failed rather than instead of it. One run
// can carry two entries, so a decision that closed one of them and could not
// close the other has done half of what it was going to, and a reader told only
// about the failure would go looking for the entry that is no longer there.
func (s *Session) closeDocketEntry(ctx context.Context, decision, runID, reason string) string {
	return s.closeDocketEntryWaiting(ctx, decision, runID, reason, "")
}

// closeDocketEntryWaiting is closeDocketEntry for a decision that may name the
// work item a wait depends on.
func (s *Session) closeDocketEntryWaiting(ctx context.Context, decision, runID, reason, waitsOn string) string {
	if s.options.Docket == nil {
		return ""
	}
	settles := triageSettles[decision]
	closed, err := s.options.Docket.Close(ctx, DocketClosure{
		RunID:    runID,
		Classes:  settles.classes,
		Decision: decision,
		Reason:   reason,
		Revisit:  settles.revisit,
		WaitsOn:  waitsOn,
		// The conversation the decision was made in, in the words the item's own
		// notes attribute it with, so a closure and the note beside it name the same
		// answerable thing.
		DecidedBy: fmt.Sprintf("the %s in conversation %s", RoleTitle(s.state.Role), s.state.ConversationID),
	})
	settled := ""
	if closed > 0 {
		settled = fmt.Sprintf("; %d docket entry(s) of that run are closed", closed)
	}
	if err != nil {
		return settled + fmt.Sprintf("; a docket entry could not be closed and will be put to you again: %v", err)
	}
	return settled
}

// waitsOnClause names the work a wait depends on in what the role is told it
// did, so the answer says the entry is off the docket until that work closes
// rather than for the ordinary window.
func waitsOnClause(waitsOn string) string {
	if waitsOn == "" {
		return ""
	}
	return fmt.Sprintf(" until %s is closed, when the harness puts the entry back on your docket once", waitsOn)
}

// carryOutCapCrossing raises one of the item's caps on this role's own delegated
// authority and writes the crossing onto the item.
//
// The cap is raised before the note is written, which is the order every decision
// here keeps: a process that dies between the two has crossed a cap it did not
// write down, rather than written down a crossing nothing permits. What that
// costs is a crossing the item's notes do not explain, and the durable triage
// record carries the reason either way.
//
// Nothing is spent and nothing is bought. The decision the crossing was for is
// still a separate decision, recorded afterwards and refused by everything it was
// always refused by — the crossing only moves the one cap it names.
func (s *Session) carryOutCapCrossing(ctx context.Context, outcome *TrackerOutcome, workItemID, runID string) {
	action := outcome.Action
	budget := strings.TrimSpace(action.Budget)
	if s.options.Triage == nil {
		outcome.fail(errors.New("no triage budget is wired to this conversation, so a cap cannot be crossed and nothing was recorded"))
		return
	}
	crossing, err := s.options.Triage.CrossCap(ctx, workItemID, budget, strings.TrimSpace(action.Reason))
	if err != nil {
		// A crossing past the bound names the operator's command, so it is the same
		// kind of refusal as the one that sent the role here: what it says is what
		// to do instead, and cutting it to a line is what loses that.
		outcome.refused(err)
		return
	}
	// The crossing is durable from here on, and the note below is a write to a
	// tracker that can fail. So it is written onto the outcome before the write,
	// for the reason a spend is: a crossing reported as having changed nothing is
	// a crossing asked for again, and the second one costs the item another of its
	// five and the operator another message about a cap that had already moved.
	outcome.noteLanded("the %s cap for %s is crossed to %d on the item's durable triage record, crossing %d of %d, and crossing it again would spend another",
		crossing.Budget, workItemID, crossing.Cap, crossing.Number, crossing.Bound)
	verb := fmt.Sprintf("Triaged: the %s cap crossed to %d on the development manager's own authority, which is crossing %d of %d for this item, on the stopped work of run %s",
		crossing.Budget, crossing.Cap, crossing.Number, crossing.Bound, runID)
	note := s.trackerProvenance(verb, action.Reason)
	if _, err := s.options.Tracker.Update(ctx, workItemID, beads.WorkItemChange{AppendNotes: note}); err != nil {
		outcome.fail(err)
		s.settleTrackerNote(ctx, outcome, workItemID, note, "the crossing recorded on the item")
		return
	}
	// The crossing travels with the outcome so that what the operator is told in
	// the channel is the record's own figures rather than a second count of them.
	// It is the whole of the veto: the crossing is in force already, and what keeps
	// it answerable is that the operator reads it at the moment it happens.
	outcome.Crossing = &CapCrossing{
		Budget:    crossing.Budget,
		Cap:       crossing.Cap,
		Crossing:  crossing.Number,
		Crossings: crossing.Bound,
	}
	outcome.applied("crossed the %s cap for %s to %d on your own authority, which is crossing %d of %d for this item and is reported to the operator as it stands; nothing was spent and no attempt was bought, so the decision it permits is still one to record",
		crossing.Budget, workItemID, crossing.Cap, crossing.Number, crossing.Bound)
}

// refuseTransposedStoppage refuses a decision whose run was made for some other
// work item. It is weaker than asking whether the run is on the docket, and
// deliberately so: an entry may have been cut from a bounded listing, and
// refusing a decision for that would refuse exactly the oldest stoppages nobody
// has got to yet. What this catches is the failure a docket of several entries
// actually produces — two of them transposed — where both items end up carrying
// reasoning about a change that is not theirs.
//
// A run the store cannot answer for is refused too. The decision is written onto
// an item as settled fact about a particular stoppage, and a stoppage nothing
// can be found out about is not one this conversation has established anything
// against; refusing costs the decision nothing, because nothing is spent before
// this is asked.
func (s *Session) refuseTransposedStoppage(ctx context.Context, workItemID, runID string) error {
	if s.options.Stoppages == nil {
		return nil
	}
	stoppedFor, err := s.options.Stoppages.WorkItemOf(ctx, runID)
	if err != nil {
		return fmt.Errorf("run %s could not be read, so nothing says it is %s's stopped work; nothing was recorded and nothing was spent: %w",
			runID, workItemID, err)
	}
	stopped := strings.TrimSpace(stoppedFor)
	if stopped == "" {
		return fmt.Errorf("run %s records no work item, so nothing says it is %s's stopped work; nothing was recorded and nothing was spent",
			runID, workItemID)
	}
	if stopped != workItemID {
		return fmt.Errorf("run %s was made for %s rather than for %s, so this decision would land on an item whose stopped work it is not about; nothing was recorded and nothing was spent",
			runID, stopped, workItemID)
	}
	return nil
}

// refusedPastCap says what a cap refusal leaves available, and leaves every other
// failure exactly as it was.
//
// A refusal is the gate working. It was also, until a cap could be crossed, the
// end of the road: the decision could not be recorded, so nothing could carry it
// out, and escalating recorded nothing either — which left a cap-exhausted item
// unrunnable by every path the harness keeps a record of. A development manager
// who is refused without knowing the remedy exists escalates into the same
// silence the escalation is meant to break.
//
// It names the remedy that is this role's own first, because it is the one that
// costs nobody a wait: every override recorded in the week to 2026-09-06 was
// granted, most within minutes, and the operator step was latency rather than
// judgement. So what the refusal offers is the crossing verb, bounded and
// justified; the operator's command is what it offers after that, for the item
// that has already had its five and for the raise nobody delegated.
//
// It names commands rather than remedies, and that is the whole of what this text
// got wrong twice. "The operator can record an override against the item" was
// read as the item's notes — the only place a conversation writes to an item at
// all — so the operator answered the escalation there, exactly as the words
// directed, the same decision was asked for again, and the identical refusal came
// back. No guard reads a note, and nothing in the sentence named the verb that
// does. So the refusal prints what to write, with the budget that refused and the
// item already in it, and says plainly that a note is not one.
func refusedPastCap(err error) error {
	if !errors.Is(err, runstate.ErrTriageCapReached) {
		return err
	}
	return fmt.Errorf("%w. Nothing written into the item's notes crosses that cap — no guard reads prose. What crosses it from here is %s, up to %d times per item and only with the reason it is being crossed for, which is recorded on the item and reported to the operator as you record it; once the crossing is recorded, asking for this same decision again records it. Past those %d, or for a ceiling beyond the one that permits it, escalate and the operator crosses it themselves with %s",
		err, crossingDecisions(err), runstate.MaxDelegatedCapCrossings, runstate.MaxDelegatedCapCrossings, overrideCommands(err))
}

// crossingDecisions are the crossings that would clear one refusal, written as
// the blocks that record them, with the budget and the item already in each.
//
// One per budget that refused, for the reason the operator's commands beside them
// are one per budget: an action can stand behind two of them, and crossing one
// leaves the same decision refused by the other. Both in one turn is one sitting
// rather than two, which is exactly what cost two override ceremonies minutes
// apart on each of two items on 2026-09-05.
//
// The run is left as a placeholder rather than filled in. Everything else here is
// a figure the refusal already holds, and the run is the one thing it does not:
// the decision that was refused named it, and naming the wrong one is what the
// transposition guard exists to catch.
func crossingDecisions(err error) string {
	var capped runstate.TriageCapError
	if !errors.As(err, &capped) || len(capped.Refusals) == 0 {
		return `a triage action of ` + "`" + `{"action":"triage","id":"<work item>","run":"<run>","decision":"cross","budget":"<budget>","reason":"<why>"}` + "`"
	}
	crossings := make([]string, 0, len(capped.Refusals))
	for _, refusal := range capped.Refusals {
		crossings = append(crossings, fmt.Sprintf("`{\"action\":\"triage\",\"id\":%q,\"run\":\"<the run the entry names>\",\"decision\":%q,\"budget\":%q,\"reason\":\"<why>\"}`",
			capped.WorkItemID, decisionCross, refusal.Budget))
	}
	if len(crossings) == 1 {
		return crossings[0]
	}
	return strings.Join(crossings[:len(crossings)-1], ", ") + " and " + crossings[len(crossings)-1] + " — both are needed, since either budget alone still refuses it"
}

// overrideCommands are the commands that cross the caps one refusal came from,
// with the budget, the work item, and the ceiling already filled in, so what
// reaches the operator is something to run rather than something to reconstruct.
//
// One per budget that refused, because an action can stand behind two of them and
// crossing one leaves the same decision refused by the other. Both in one message
// is one sitting rather than two: the alternative is what actually happened on
// 2026-09-05, when two items each took two override ceremonies minutes apart
// because the first refusal named only the first budget.
//
// The ceiling is filled in rather than left as a placeholder for the same reason
// the budget is. An operator handed `--cap <n>` has to go and find what the item
// has spent before they can type a number, and the refusal beside this sentence
// is the only place that figure appears.
//
// A refusal carrying no budget to name falls back to the shape of the command
// rather than to a description of it. Nothing produces one today — every cap
// refusal here is a TriageCapError — but a refusal that lost the detail must
// still leave the operator holding a verb.
func overrideCommands(err error) string {
	var capped runstate.TriageCapError
	if !errors.As(err, &capped) || len(capped.Refusals) == 0 {
		return "`yoyo triage override --budget \"<budget>\" --cap <n> --by \"<operator>\" --reason \"<why>\" <work item>`"
	}
	commands := make([]string, 0, len(capped.Refusals))
	for _, refusal := range capped.Refusals {
		commands = append(commands, fmt.Sprintf("`yoyo triage override --budget %q --cap %d --by \"<operator>\" --reason \"<why>\" %s`",
			refusal.Budget, refusal.Permits(), capped.WorkItemID))
	}
	if len(commands) == 1 {
		return commands[0]
	}
	// Both are required rather than either, and the sentence says so: a decision
	// two budgets refuse is permitted by neither override alone.
	return strings.Join(commands[:len(commands)-1], ", ") + " and " + commands[len(commands)-1] + " — both are needed, since either budget alone still refuses it"
}

// triageSpend is what a decision cost the item's durable budget, said twice: as
// the clause a recorded decision appends to its own summary, and as the standing
// fact a failure after the spend has to report on its own.
//
// Two forms rather than one because they are read in opposite situations. The
// clause continues a sentence about a decision that was recorded; the standing
// fact is all there is to say to somebody deciding whether to ask for the same
// decision again, and it has to name the item, since the summary that would have
// named it was never written.
type triageSpend struct {
	clause string
	landed string
}

// recordTriageDecision writes the decision to the item's durable record, spends
// what it costs, and says what the spend came to — or says nothing about a spend
// for a decision that costs nothing. Three of the six decisions about a stoppage
// buy another attempt at work that already failed once, and those are the three
// the durable budget bounds — the crossing never reaches here, since it decides
// nothing about a stoppage;
// re-scoping, waiting, and escalating buy no attempt at all and are never refused
// for budget.
//
// Two of the three are budgets of the work item's. The third is not: a re-arm
// repeats one merge request the reviewer's verdict already authorized, so it is
// bounded per publication, and what it says it recorded names that publication
// rather than the item — a development manager told "1 re-arm of this item is
// recorded" would read a second publication's untouched budget as a spent one.
//
// A conversation with no record wired can still decide the three that spend
// nothing, exactly as it always could, and its decision reaches the item's notes
// and no further. What it cannot do is grant, re-run, or re-arm: a budget that
// cannot be read must never be spent through as though it were empty, and a
// decision the harness will act on must be one the harness can read back.
//
// Only a spend is reported as landed afterwards, and that is the right line: a
// decision that spends nothing and then fails to reach the item leaves a record
// that asking again supersedes rather than duplicates, so there is nothing there
// for a second attempt to spend twice.
func (s *Session) recordTriageDecision(ctx context.Context, workItemID string, decided runstate.TriageDecision) (triageSpend, error) {
	if !decided.Spends() {
		if s.options.Triage == nil {
			return triageSpend{}, nil
		}
		if _, err := s.options.Triage.RecordDecision(ctx, workItemID, decided); err != nil {
			return triageSpend{}, err
		}
		return triageSpend{}, nil
	}
	if s.options.Triage == nil {
		return triageSpend{}, errors.New("no triage budget is wired to this conversation, so a repair, a re-run, or a re-arm cannot be bounded and was not recorded")
	}
	switch decided.Decision {
	case decisionRepair:
		granted, err := s.options.Triage.GrantRepair(ctx, workItemID, decided)
		if err != nil {
			return triageSpend{}, err
		}
		spent := triageSpend{
			clause: fmt.Sprintf("; it is granted %d further review round(s)", granted.Rounds),
			landed: fmt.Sprintf("the repair grant is spent against %s's durable budget: %d further review round(s) are granted to it", workItemID, granted.Rounds),
		}
		if granted.Truncated {
			spent.clause = fmt.Sprintf("; the grant was cut from %d round(s) to the %d the cap still had room for",
				granted.Requested, granted.Rounds)
		}
		return spent, nil
	case decisionRerun:
		counters, err := s.options.Triage.RecordRerun(ctx, workItemID, decided)
		if err != nil {
			return triageSpend{}, err
		}
		return triageSpend{
			clause: fmt.Sprintf("; %d re-run(s) of it are now recorded", counters.Reruns),
			landed: fmt.Sprintf("the re-run is spent against %s's durable budget: %d re-run(s) of it are now recorded", workItemID, counters.Reruns),
		}, nil
	default:
		rearmed, err := s.options.Triage.RecordMergeRearm(ctx, workItemID, decided)
		if err != nil {
			return triageSpend{}, err
		}
		return triageSpend{
			clause: fmt.Sprintf("; %d merge re-arm(s) of publication %s are now recorded", rearmed.Rearms(), rearmed.Publication),
			landed: fmt.Sprintf("the merge re-arm is spent against publication %s's durable budget: %d re-arm(s) of it are now recorded", rearmed.Publication, rearmed.Rearms()),
		}, nil
	}
}

// releasedRaise is what an item's parking said about an unmeetable raise at the
// moment its owner released it: the run that raised it, and whether the item
// read blocked then.
type releasedRaise struct {
	runID   string
	blocked bool
	// unread is why the item could not be read before the release, which leaves
	// the release carried out and the raise not ended by it.
	unread string
}

// raiseBeingReleased reads the item a release names for the raise its parking
// records, if it records one.
func (s *Session) raiseBeingReleased(ctx context.Context, id string) releasedRaise {
	item, err := s.options.Tracker.Show(ctx, id)
	if err != nil {
		return releasedRaise{unread: err.Error()}
	}
	runID, raised := runstate.RaisedBy(item.Parking.Reason())
	if !raised {
		return releasedRaise{}
	}
	return releasedRaise{runID: runID, blocked: strings.TrimSpace(item.Status) == blockedWorkItemStatus}
}

// endReleasedRaise ends an unmeetable raise whose parking the item's owner has
// just released, and says what that came to.
//
// The release is the owner saying the item has been amended so that it can be
// met: the raise said it could not be as it stood, and the parking the raise
// placed was waiting on exactly that. So the release ends the raise. What the
// raise left on the item goes with it — a blocked status written while it stood,
// most often the escalation that followed it, which nothing else rewrites and
// which would otherwise leave the amended item reading blocked on nobody's
// account, as yoyodyne-ifd.437.13 did after its release on 2026-09-27.
//
// What it does not do is decide what becomes of the raising run's change. The
// development manager decides that — a re-run that starts from it, or retiring
// the raise where the amendment made it moot — and until she does, a pull that
// reaches the item starts it from the target branch like any other.
func (s *Session) endReleasedRaise(ctx context.Context, id string, raise releasedRaise) (string, error) {
	if raise.unread != "" {
		return fmt.Sprintf("; whether its parking was an unmeetable raise's could not be read beforehand (%s), so any raise it ended is not said here", raise.unread), nil
	}
	if raise.runID == "" {
		return "", nil
	}
	ended := fmt.Sprintf("; that ends the unmeetable raise of run %s", raise.runID)
	if raise.blocked {
		note := s.trackerProvenance(fmt.Sprintf("Blocked status cleared: it was left while the unmeetable raise of run %s stood, and the item's owner has amended the item and released the raise's parking", raise.runID),
			"the release ends the raise the status was holding the item for")
		if _, err := s.options.Tracker.Unblock(ctx, id, note); err != nil {
			return "", fmt.Errorf("%s is released from the raise's parking, and the blocked status the raise left could not be cleared, so it still reads blocked: %w", id, err)
		}
		ended += ", and the blocked status it left is cleared"
	}
	return ended + fmt.Sprintf("; what becomes of that run's preserved change is the development manager's, as %q to start from it or %q where it is moot", decisionRerun, decisionRetireRaise), nil
}
