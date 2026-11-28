package orchestrator

// Asking the one decision on what may start (internal/maystart) beside the
// checks each path already makes. It replaces none of them: a yes lifts no hold.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// StartDecision asks maystart.Decide for one product. Every command wires it
// (cli's components.mayStart). Nil is a caller that names no epic, such as a
// test, and refuses nothing.
type StartDecision func(maystart.Start) maystart.Decision

// refuseRun is why a run of the item may not start by this path, and nil where
// it may.
func (d StartDecision) refuseRun(path maystart.Path, workItemID string) *OutsideNamedEpicsError {
	if d == nil {
		return nil
	}
	decision := d(maystart.Run(path, workItemID))
	if decision.MayStart() {
		return nil
	}
	return &OutsideNamedEpicsError{WorkItemID: workItemID, Decision: decision}
}

// refuseUpdate is refuseRun for bringing a run's change up to date, naming the
// run so that a refusal is kept naming it.
func (d StartDecision) refuseUpdate(path maystart.Path, state runstate.State) *OutsideNamedEpicsError {
	if d == nil {
		return nil
	}
	decision := d(maystart.Run(path, state.WorkItemID).OfRun(state.RunID))
	if decision.MayStart() {
		return nil
	}
	return &OutsideNamedEpicsError{WorkItemID: state.WorkItemID, Decision: decision}
}

// updateNotDue says that a check of the run found no update of its change due by
// the path, could not tell, or found the run cannot be put back.
func (r Reconciler) updateNotDue(path maystart.Path, state runstate.State) {
	if r.UpdateNotDue != nil {
		r.UpdateNotDue(path, state.WorkItemID, state.RunID)
	}
}

// refuseDocument is why a run that publishes the document may not start, and nil
// where it may.
func (d StartDecision) refuseDocument(document runstate.DocumentPublication) *OutsideNamedEpicsError {
	if d == nil {
		return nil
	}
	written := document.Candidate.Artifact
	decision := d(maystart.DocumentRun(maystart.PublishDocument, document.AnsweredItems()...).Of(maystart.Document{
		ID: written.ID, Title: written.Title, WriteID: document.WriteID, ConfirmedAt: written.ConfirmedAt(),
	}))
	if decision.MayStart() {
		return nil
	}
	return &OutsideNamedEpicsError{Document: fmt.Sprintf("document %s (%s)", document.WriteID, written.Title), Decision: decision}
}

// EpicStarts reserves a run's start for the epics the operator named that it is
// for, before the run's record is made. The harness wires it to the one writer
// of their progress (cli's components.epicStarts). withdraw takes it back where
// the record was refused. Nil is a caller that names no epic.
type EpicStarts func(runID string, items []string) (withdraw func() error, err error)

// reserve reserves the start of one run. A start that cannot be written down is
// not made.
func (e EpicStarts) reserve(state runstate.State) (func() error, error) {
	if e == nil {
		return func() error { return nil }, nil
	}
	items := []string{state.WorkItemID}
	switch {
	case state.Document != nil:
		items = state.Document.AnsweredItems()
	case state.OutsideFile != nil:
		items = []string{state.OutsideFile.For}
	}
	withdraw, err := e(state.RunID, items)
	if err != nil {
		refused := &OutsideNamedEpicsError{WorkItemID: state.WorkItemID, Decision: maystart.Decision{Answer: maystart.CouldNotTell,
			Why: fmt.Sprintf("its start could not be written down in its epic's progress (%v)", err), Clears: "that progress can be written, and the epic is open"}}
		if state.Document != nil {
			refused.Document = fmt.Sprintf("document %s (%s)", state.Document.WriteID, state.Document.Candidate.Artifact.Title)
		}
		return nil, refused
	}
	return withdraw, nil
}

// PieceTexts is why a piece that joined a named epic may not start a developer on
// the item as read, and empty where it may (epicprogress.TextWaits). It is asked
// of the same reading the developer is given, beside the one decision, which is
// given only an identifier and reads no tracker. The harness wires it (cli's
// components.pieceTexts). Nil is a caller that names no epic.
type PieceTexts func(item beads.WorkItem) (string, error)

// refuse is why a developer may not be given this reading of the item, and nil
// where it may. A record that cannot be read starts nothing.
func (t PieceTexts) refuse(item beads.WorkItem, runID string) *OutsideNamedEpicsError {
	if t == nil {
		return nil
	}
	why, err := t(item)
	switch {
	case err != nil:
		return &OutsideNamedEpicsError{WorkItemID: item.ID, RunID: runID, Decision: maystart.Decision{Answer: maystart.CouldNotTell,
			Why: fmt.Sprintf("whether its text was judged inside its epic could not be read (%v)", err)}}
	case why != "":
		return &OutsideNamedEpicsError{WorkItemID: item.ID, RunID: runID, Decision: maystart.Decision{Answer: maystart.MayNot, Why: why,
			Clears: "a reviewer session judges its text as it reads now inside the epic, or its text is put back to one judged inside"}}
	}
	return nil
}

// refuseContinuing is refuse for a run already started, which is asked where the
// one decision asks it.
func (t PieceTexts) refuseContinuing(state runstate.State, item beads.WorkItem) *OutsideNamedEpicsError {
	if state.HarnessAuthored() || state.Integration != nil || state.Document != nil || state.OutsideFile != nil {
		return nil
	}
	return t.refuse(item, state.RunID)
}

// allows is whether a start that is not a work item's run may go ahead.
func (d StartDecision) allows(start maystart.Start) bool {
	return d == nil || d(start).MayStart()
}

// OutsideNamedEpicsError is a start declined because the factory works only on
// the epics the operator named and the item is in none of them, or because the
// record of them could not be read. Nothing was reserved, claimed or spent. With
// a run named, it is a run already started that waits with its work kept.
type OutsideNamedEpicsError struct {
	WorkItemID string `json:"work_item_id,omitempty"`
	RunID      string `json:"run_id,omitempty"`
	// Document is the document, in words, where it is one that was to be
	// published rather than a run of a work item.
	Document string            `json:"document,omitempty"`
	Decision maystart.Decision `json:"decision"`
	// Says is the refusal already in words, where only those were kept.
	Says string `json:"-"`
}

func (e *OutsideNamedEpicsError) Error() string {
	if e.Says != "" {
		return e.Says
	}
	if e.Document != "" {
		return fmt.Sprintf("the run publishing %s was not started: %s; it starts after %s", e.Document, e.Decision.Why, e.Clears())
	}
	if e.RunID != "" {
		return fmt.Sprintf("run %s of %s waits for its epic: %s; it goes on after %s", e.RunID, e.WorkItemID, e.Decision.Why, e.Clears())
	}
	return fmt.Sprintf("%s was not started: %s; it starts after %s", e.WorkItemID, e.Decision.Why, e.Clears())
}

// Clears is what lets the same start go ahead, with the commands that do it.
func (e *OutsideNamedEpicsError) Clears() string {
	if e.Decision.Clears != "" {
		return e.Decision.Clears
	}
	if e.Decision.Answer != maystart.MayNot {
		return "the record of the named epics, and of their progress, can be read again"
	}
	if e.Document != "" {
		return "it answers a member of an open epic or the open epic itself, or the operator runs `yoyo epic off`"
	}
	var ways []string
	for _, epic := range e.Decision.Open {
		if epic != e.WorkItemID { // an epic is not one of its own members
			ways = append(ways, fmt.Sprintf("`yoyo epic add %s %s`", epic, e.WorkItemID))
		}
	}
	if len(ways) == 0 {
		return "the operator names an epic holding it with `yoyo epic start`, or runs `yoyo epic off`"
	}
	return "the operator adds it with " + strings.Join(ways, " or ") + ", or runs `yoyo epic off`"
}

// passDue is a pass found due, ahead of anything being claimed for it.
type passDue struct {
	start maystart.Start
	// claim claims it and is nil where nothing is due. The claim makes its own
	// due check again, because another process may claim in between.
	claim func(ctx context.Context) (claimedFiring, bool, error)
	// unasked is a pass another check keeps from starting, whose claim only
	// records that; the decision is asked of a start every other check allows.
	unasked bool
	// problem is what went wrong reading the pass that it can be taken despite. A
	// pass that is claimed carries it in its own record; it is said beside the
	// pull only where the pass is not taken.
	problem error
}

// scheduleDue reads, changing nothing, whether a cadence has fallen due, with
// the same check the claim makes, and the time it fell due. A task that has
// never fired is due from no known time, so its refusal is counted once.
func (t Trigger) scheduleDue(name string, every time.Duration) (string, bool, error) {
	claim, found, err := t.Claims.Find(name)
	if err != nil {
		return "", false, err
	}
	if !found || claim.FiredAt.IsZero() {
		return "never fired", true, nil
	}
	now := t.now()
	if !claim.Due(every, now) {
		return "", false, nil
	}
	due := claim.NextDue(every)
	if now.Before(due) {
		return "owed since " + claim.FiredAt.UTC().Format(time.RFC3339Nano), true, nil
	}
	// A refusal leaves the schedule where it is, so each whole interval since is
	// another time the pass came due.
	if every > 0 {
		due = due.Add(now.Sub(due) / every * every)
	}
	return due.UTC().Format(time.RFC3339Nano), true, nil
}

// scheduleSource names one schedule among the things that can start a pass.
func scheduleSource(task string) string { return "schedule:" + task }

// eventDue names the oldest event outstanding.
func eventDue(event PassEvent) string {
	if event.Key != "" {
		return event.Stream + ":" + event.Key
	}
	return event.Stream + ":" + event.Subject + "@" + event.At.UTC().Format(time.RFC3339Nano)
}

// PassMayBeLate is whether a pass that is due may start, so that one the
// decision refuses is never recorded as missed, and when the operator last
// changed the epics named, so that a pass may have been refused only before
// then. It keeps nothing.
func (t Trigger) PassMayBeLate(due RecurringDue) (bool, time.Time) {
	path := maystart.RecurringPass
	if due.Trigger == runstate.PassTriggerEvents {
		path = maystart.PromptedPass
	}
	var changed time.Time
	if t.EpicsChanged != nil {
		changed = t.EpicsChanged()
	}
	return t.WouldStart.allows(maystart.Pass(path, due.Role)), changed
}

// ErrSummonsOutsideNamedEpics reports a summons declined by the decision on what
// may start. Nothing was claimed, and it comes round again while its hold stands.
var ErrSummonsOutsideNamedEpics = errors.New("the development manager may not be summoned yet, because of the epics the operator named")

// MaySummon asks whether the summons after an automatic hold may start. It is
// counted once for each hold.
func (t Trigger) MaySummon(summons BrakeSummons) error {
	if t.MayStart == nil {
		return nil
	}
	decision := t.MayStart(maystart.Pass(maystart.PromptedPass, domain.RoleDevelopmentManager).
		Due("hold", summons.Hold.HeldAt.UTC().Format(time.RFC3339Nano)))
	if decision.MayStart() {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrSummonsOutsideNamedEpics, decision.Why)
}

// summonsAsking is a summons that can say, with nothing started, whether it may
// be made. It is satisfied by Trigger.
type summonsAsking interface {
	MaySummon(summons BrakeSummons) error
}

// startPath is the path this pipeline's own start is on, read from who chose
// the work. Every path that begins a run is judged by the same rule, so this
// says which path it is and never what the answer is.
func (p Pipeline) startPath() maystart.Path {
	switch strings.TrimSpace(p.Selection.By) {
	case runstate.SelectedByOperator:
		return maystart.NamedRun
	case runstate.SelectedByBrake:
		return maystart.HoldTest
	case runstate.SelectedByDevelopmentManager:
		if runstate.ChosenForFeedbackRepair(p.Selection.Reason) {
			return maystart.FeedbackRepair
		}
		return maystart.DecidedRerun
	default:
		return maystart.ReadyWork
	}
}

// EpicHolding is the open epic an item is in while epic mode is on, as a member,
// a piece that joined, or the epic's own item, and whether another item is in
// that same epic; no epic while epic mode is off or the item is in none. The
// harness wires it to the progress of the named epics (epicprogress.Store).
type EpicHolding func(item string) (epic string, inside func(string) bool, err error)

// outsideEpic is why item may not be made to wait on open work, and empty where
// it may: item is in a named epic and the work is outside it, so nothing would
// start it without the operator (docs/designs/epic-mode-second-part.md, "An item
// outside the epic that could block a member", point 4).
func (e EpicHolding) outsideEpic(item, waitsOn string) string {
	if e == nil {
		return ""
	}
	epic, inside, err := e(item)
	if err != nil {
		return fmt.Sprintf("whether this item is in a named epic could not be read (%v), so it is not made to wait on that work", err)
	}
	if epic == "" || inside(waitsOn) {
		return ""
	}
	return fmt.Sprintf("this item is in epic %s and that work is open outside it, so it would wait until the operator adds it with `yoyo epic add %s %s`, "+
		"because nothing outside the epics he named starts", epic, epic, waitsOn)
}
