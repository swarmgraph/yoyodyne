package orchestrator

// Asking the one decision on what may start (internal/maystart) beside the
// checks each path already makes. It replaces none of them: a yes lifts no hold.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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
	if e.Decision.Answer != maystart.MayNot {
		return "the record of the named epics can be read again"
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
