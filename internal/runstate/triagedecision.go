package runstate

// What the development manager decided about one stoppage, as a record rather
// than as prose.
//
// The counters beside this have always said how much triage spent on an item,
// and that is not the same thing as what it decided. A spent re-run says a
// decision was made; it does not say which stoppage it was about, who recorded
// it, or what the argument was. Those lived in the item's notes, which is prose
// nothing reads, so the harness that carried a decision out had to be handed the
// reasoning again — as words on a command line, attributed to a role that never
// saw them. A run could therefore carry a development-manager attribution nobody
// in that role wrote, which is exactly what
// `selected-work-passes-intake-and-records-why` exists to make impossible.
//
// So the decision is written where the budget it spends is written: on the
// item's own counter record, under the same lock and in the same update, at the
// moment the development manager's conversation records it. The verb that
// carries a decision out reads it from here and takes no words from anybody.
//
// One decision stands per stopped run. A run is decided about as one thing, so a
// later decision about the same run supersedes the earlier one rather than
// standing beside it: what a reader and a carry-out both need is the decision
// that holds now. What that leaves is a record bounded by the runs an item has
// had, which is bounded in turn by every cap the counters enforce.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// The decisions triage may record. They are the development manager's own
// vocabulary, declared here because this is where a decision becomes durable:
// the conversation that records one and the action that carries one out are two
// packages, and a word each of them spelled for itself is a word they could
// spell differently.
const (
	// TriageDecisionRepair hands the item another bounded go at the change it
	// already has.
	TriageDecisionRepair = "repair"
	// TriageDecisionRerun runs the item again from the start.
	TriageDecisionRerun = "rerun"
	// TriageDecisionRescope splits what was out of scope into a child of the item.
	TriageDecisionRescope = "rescope"
	// TriageDecisionRearm repeats an authorized merge request the forge dropped.
	TriageDecisionRearm = "rearm"
	// TriageDecisionWait is the decision that nothing is to be done yet.
	TriageDecisionWait = "wait"
	// TriageDecisionEscalate hands the entry to the operator.
	TriageDecisionEscalate = "escalate"
	// TriageDecisionStop stops a run still in flight whose work the development
	// manager has decided is superseded, narrowed, or mis-launched. It is the one
	// decision about a run that has not stopped: the harness asks the run to stop
	// exactly as the operator's stop does, at its next boundary with its change
	// preserved, and the stoppage that leaves is already decided.
	TriageDecisionStop = "stop"
	// TriageDecisionProceed lets a run in flight finish. It answers the Lead
	// Product Manager's decision that the run's item is superseded, narrowed, or
	// to be retired, where the development manager judges the run worth finishing
	// anyway: nothing is asked of the run, and the decision is the record that she
	// looked and chose not to stop it. Like a stop, it decides no stoppage.
	TriageDecisionProceed = "proceed"
	// TriageDecisionRetireRaise ends an item a role raised as unmeetable where the
	// item's owner amended it so that what the raising run left is moot: the item
	// goes back to the queue to be started from the target branch, and nothing
	// lifts the raising run's change into the run that does it. It answers a raise
	// and nothing else — a stopped run has no raise to retire — and it spends
	// nothing, because it buys no attempt.
	TriageDecisionRetireRaise = "retire-raise"
)

// TriageDecisionVocabulary lists the decisions in the order the development
// manager's contract states them, so a refusal names exactly what was available.
func TriageDecisionVocabulary() []string {
	return []string{
		TriageDecisionRepair, TriageDecisionRerun, TriageDecisionRescope,
		TriageDecisionRearm, TriageDecisionWait, TriageDecisionEscalate,
		TriageDecisionStop, TriageDecisionProceed, TriageDecisionRetireRaise,
	}
}

// The bounds one decision is held to. The reasoning is the whole of what makes a
// decision answerable later and is bounded like an override's; the role and the
// conversation are identifiers rather than arguments.
const (
	MaxTriageDecisionReasonBytes       = 4 << 10
	MaxTriageDecisionByBytes           = 256
	MaxTriageDecisionConversationBytes = 256
	MaxTriageDecisionSupersededBytes   = 256
)

// MaxTriageDecisions bounds how many stoppages one item's record carries
// decisions about. One decision stands per run, so reaching it would take an
// item more runs than every cap here permits between them.
const MaxTriageDecisions = 64

// TriageDecision is one decision the development manager recorded about one
// stopped run.
//
// Every field is required, which is the point of the record: a decision nobody
// is named for, or one that names no stoppage, is exactly the prose this exists
// to replace. Who recorded it and where are what make the attribution on a run
// citable — a re-run says which conversation and which turn decided it, and a
// reader can go and find that turn.
type TriageDecision struct {
	// Decision is the word from the vocabulary above.
	Decision string `json:"decision"`
	// RunID is the stopped run whose docket entry this settles. It is what makes
	// the record one per stoppage rather than one per item: an item decided about
	// twice was decided about twice, and each decision is about its own run.
	RunID string `json:"run_id"`
	// Reason is the development manager's own reasoning, in its own words. It is
	// what a carry-out records as why the run it starts exists, so it is kept
	// verbatim rather than summarized.
	Reason string `json:"reason"`
	// DecidedBy is the role that recorded it. It is written down rather than
	// assumed, because the whole value of the record is that the attribution on a
	// run is read from something the role itself wrote.
	DecidedBy string `json:"decided_by"`
	// Conversation and Turn are where it was recorded, which is what a citation
	// points at: an attribution naming neither is one nobody can go and check.
	Conversation string    `json:"conversation"`
	Turn         int       `json:"turn"`
	DecidedAt    time.Time `json:"decided_at"`
	// Rounds is what a repair decision reserved of the item's round budget when
	// it was recorded: the grant as the cap left it. It is written by the grant
	// and by nothing else, so every other decision carries zero, and it is what a
	// decision that supersedes the repair releases — the rounds this reservation
	// promised and the item never spent. A repair recorded before this was kept
	// carries zero too, and superseding it releases whatever the item stands
	// committed to beyond what it has cost, which is the one reservation such a
	// record can be holding.
	Rounds int `json:"rounds,omitempty"`
	// SupersededBy is the work item that supersedes the stopped run's work, on a
	// stop decision where there is one. It is taken by that decision and by no
	// other: what a stop answers is why the run should not go on, and the item
	// doing the work instead is the half of that a reader goes looking for.
	SupersededBy string `json:"superseded_by,omitempty"`
}

// Validate reports every contract violation in the decision at once.
func (d TriageDecision) Validate() error {
	var problems []error
	if err := validTriageDecision(d.Decision); err != nil {
		problems = append(problems, err)
	}
	switch run := strings.TrimSpace(d.RunID); {
	case run == "":
		problems = append(problems, errors.New("a triage decision names the stopped run it is about"))
	case !ValidRunID(run):
		problems = append(problems, fmt.Errorf("%q is not a run identifier; a triage decision names the run its docket entry is about", d.RunID))
	}
	switch reason := strings.TrimSpace(d.Reason); {
	case reason == "":
		problems = append(problems, errors.New("a triage decision records the reasoning it was made on, which is what a carry-out records as why the run it starts exists"))
	case len(reason) > MaxTriageDecisionReasonBytes:
		problems = append(problems, fmt.Errorf("the decision reason is %d bytes, limit is %d", len(reason), MaxTriageDecisionReasonBytes))
	}
	switch by := strings.TrimSpace(d.DecidedBy); {
	case by == "":
		problems = append(problems, errors.New("a triage decision names the role that recorded it; one nobody is named for is what this record exists to replace"))
	case len(by) > MaxTriageDecisionByBytes:
		problems = append(problems, fmt.Errorf("the deciding role is %d bytes, limit is %d", len(by), MaxTriageDecisionByBytes))
	}
	switch conversation := strings.TrimSpace(d.Conversation); {
	case conversation == "":
		problems = append(problems, errors.New("a triage decision names the conversation it was recorded in, which is what an attribution citing it points at"))
	case len(conversation) > MaxTriageDecisionConversationBytes:
		problems = append(problems, fmt.Errorf("the conversation is %d bytes, limit is %d", len(conversation), MaxTriageDecisionConversationBytes))
	}
	if d.Turn < 0 {
		problems = append(problems, fmt.Errorf("turn %d is not a turn", d.Turn))
	}
	if d.Rounds < 0 {
		problems = append(problems, fmt.Errorf("%d reserved round(s) is not a reservation", d.Rounds))
	}
	if d.Rounds > 0 && d.Decision != TriageDecisionRepair {
		problems = append(problems, fmt.Errorf("a %q decision reserves no rounds, and this one records %d", d.Decision, d.Rounds))
	}
	switch superseded := strings.TrimSpace(d.SupersededBy); {
	case superseded == "":
	case d.Decision != TriageDecisionStop:
		problems = append(problems, fmt.Errorf("only a %q decision names the item that supersedes a run, and this one is %q", TriageDecisionStop, d.Decision))
	case len(superseded) > MaxTriageDecisionSupersededBytes:
		problems = append(problems, fmt.Errorf("the superseding item is %d bytes, limit is %d", len(superseded), MaxTriageDecisionSupersededBytes))
	}
	if d.DecidedAt.IsZero() {
		problems = append(problems, errors.New("a triage decision records when it was made"))
	}
	return errors.Join(problems...)
}

// Cite names the record itself, for an attribution that has to be checkable
// rather than only plausible. What it points at is the turn the decision was
// recorded on, which is where the reasoning beside it was written.
func (d TriageDecision) Cite() string {
	return fmt.Sprintf("recorded by the %s in conversation %s after turn %d, at %s",
		strings.TrimSpace(d.DecidedBy), strings.TrimSpace(d.Conversation), d.Turn, d.DecidedAt.UTC().Format(time.RFC3339))
}

// Describe says what one decision was, for whoever is reading an item's record.
func (d TriageDecision) Describe() string {
	if d.InFlight() {
		described := fmt.Sprintf("%q of run %s in flight, %s: %s",
			d.Decision, d.RunID, d.Cite(), strings.TrimSpace(d.Reason))
		if superseded := strings.TrimSpace(d.SupersededBy); superseded != "" {
			described += "; superseded by " + superseded
		}
		return described
	}
	return fmt.Sprintf("%q on the stopped work of run %s, %s: %s",
		d.Decision, d.RunID, d.Cite(), strings.TrimSpace(d.Reason))
}

// InFlight reports a decision made about a run that had not stopped: a stop, or
// letting the run finish. Neither decides a stoppage, because neither was made
// about one: a stop decides only the stoppage it causes, which is docketed
// already closed by it, and a run let finish that stops anyway reached a
// stoppage nobody has looked at.
func (d TriageDecision) InFlight() bool {
	return d.Decision == TriageDecisionStop || d.Decision == TriageDecisionProceed
}

// Spends reports a decision that buys another attempt at work that already
// failed once, which is the half of the vocabulary the durable budgets bound.
// The rest buy no attempt and are never refused for budget.
func (d TriageDecision) Spends() bool {
	switch d.Decision {
	case TriageDecisionRepair, TriageDecisionRerun, TriageDecisionRearm:
		return true
	default:
		return false
	}
}

// DecisionOf is the decision standing about one stopped run, and whether there
// is one at all. It is what a carry-out reads: an item with a spent budget and
// no decision naming this stoppage is an item whose decision was about some
// other run of it.
func (c TriageCounters) DecisionOf(runID string) (TriageDecision, bool) {
	run := strings.TrimSpace(runID)
	if run == "" {
		return TriageDecision{}, false
	}
	for index := len(c.Decisions) - 1; index >= 0; index-- {
		if c.Decisions[index].RunID == run {
			return c.Decisions[index], true
		}
	}
	return TriageDecision{}, false
}

// LatestDecision is the decision recorded last about any of the item's runs,
// and whether there is one.
func (c TriageCounters) LatestDecision() (TriageDecision, bool) {
	var latest TriageDecision
	found := false
	for _, decision := range c.Decisions {
		if !found || !decision.DecidedAt.Before(latest.DecidedAt) {
			latest, found = decision, true
		}
	}
	return latest, found
}

// Standing is what this record says about one stopped run, in the shape the
// shared carry-out rule reads. It is the one place the ledger is reduced to that
// shape, so the docket's copy of the counters and a surface reading the ledger
// itself are reading the same facts rather than each deriving their own.
//
// A stop, or a decision to let a run finish, is not counted as deciding anything
// here. A stop is recorded against a run
// in flight, and the stoppage it decides is the one it causes, which is docketed
// already closed by it. A run that passed its last boundary before the stop was
// read and then stopped for another reason reached a stoppage the stop was never
// about, and that one is undecided whatever the record says about the run.
func (c TriageCounters) Standing(runID string) triage.Standing {
	decision, decided := c.DecisionOf(runID)
	_, refused := c.RefusedCarryOut(runID)
	return triage.Standing{
		Decided:          decided && !decision.InFlight(),
		Spends:           decision.Spends(),
		Repair:           decision.Decision == TriageDecisionRepair,
		GrantOutstanding: c.GrantOutstanding(),
		Refused:          refused,
	}
}

// StandingOf is Standing read with the stopped run's own record beside it, which
// is what answers whether a granted repair was handed back. The item's grant
// counter cannot: it turns into counted rounds only as the attempts it bought
// are judged, so a repaired run approved in fewer rounds than it was granted, and
// then stopped at its promotion, left the item committed to rounds nothing would
// ever spend — and the supervisor's periodic pass (yoyodyne-ifd.413) read as the
// harness's to carry out on 2026-09-30 after its repair had been carried out
// (yoyodyne-8ff). A continuation of the run made since the decision is the
// carry-out itself, and the carry-out reads it the same way: one decision buys
// one continuation and no more. Its rounds can be refunded without undoing that
// evidence; a later stop is no longer decided by the earlier repair.
func (c TriageCounters) StandingOf(run State) triage.Standing {
	standing := c.Standing(run.RunID)
	if !standing.Repair {
		return standing
	}
	decision, _ := c.DecisionOf(run.RunID)
	if run.RepairContinuedSince(decision.DecidedAt) {
		standing.Decided = false
		standing.CarriedOut = true
		standing.GrantOutstanding = false
		standing.Refused = false
	}
	return standing
}

// AwaitingCarryOut reports a decision standing about one stopped run that the
// harness has still to act on. It is triage.AwaitingCarryOut over this record,
// and exists so that a caller holding the ledger asks the question in one call
// rather than assembling the standing itself.
func (c TriageCounters) AwaitingCarryOut(runID string) bool {
	return triage.AwaitingCarryOut(c.Standing(runID))
}

// AwaitingCarryOutOf is AwaitingCarryOut with the run's own record read beside
// the ledger, by StandingOf.
func (c TriageCounters) AwaitingCarryOutOf(run State) bool {
	return triage.AwaitingCarryOut(c.StandingOf(run))
}

// RecordDecision records a triage decision that spends nothing: a re-scope, a
// wait, an escalation, a stop, a proceed, or a raise retired. The three that buy another attempt are recorded by the
// operation that spends their budget, in the same write, so that a decision and
// the spend it authorizes can never be one without the other.
func (s *TriageStore) RecordDecision(ctx context.Context, workItemID string, decision TriageDecision, at time.Time) (TriageCounters, error) {
	if decision.Spends() {
		return TriageCounters{}, fmt.Errorf(
			"a %q decision spends the item's durable budget, so it is recorded by the operation that spends it rather than on its own",
			decision.Decision)
	}
	when := at
	if when.IsZero() {
		when = time.Now()
	}
	prepared, err := prepareTriageDecision(decision, when)
	if err != nil {
		return TriageCounters{}, err
	}
	return s.update(ctx, workItemID, when, func(counters *TriageCounters) error {
		return counters.recordDecision(prepared)
	})
}

// prepareTriageDecision trims what a caller supplied and dates the decision from
// the update's own clock, so the decision and the record that carries it can
// never disagree about when it was made.
func prepareTriageDecision(decision TriageDecision, at time.Time) (TriageDecision, error) {
	decision.Decision = strings.TrimSpace(decision.Decision)
	decision.RunID = strings.TrimSpace(decision.RunID)
	decision.Reason = strings.TrimSpace(decision.Reason)
	decision.DecidedBy = strings.TrimSpace(decision.DecidedBy)
	decision.Conversation = strings.TrimSpace(decision.Conversation)
	decision.SupersededBy = strings.TrimSpace(decision.SupersededBy)
	decision.DecidedAt = at.UTC()
	if err := decision.Validate(); err != nil {
		return TriageDecision{}, fmt.Errorf("invalid triage decision: %w", err)
	}
	return decision, nil
}

// recordDecision puts one decision on the item's record, in place of whatever
// was standing about the same stoppage. Superseding rather than appending is
// what keeps the record answerable: a development manager who decided a wait and
// then a re-run about one run decided a re-run, and a carry-out that found both
// would have to guess which.
//
// A repair it supersedes takes its reservation with it. The grant reserved rounds
// against the cap for attempts the harness would hand the stopped run, and a
// decision to run the item again, escalate it, or wait instead is the decision
// that those attempts will not be made — so the rounds they reserved and the
// item never spent are released before the new decision is measured against the
// cap. That is the second of yoyodyne-ifd.391's regression cases: a repair on
// yoyodyne-ifd.309 reserved the cap's last round, the harness found the worktree
// retired and refused to carry it out, and the re-run recorded in its place was
// refused at 6 of 6 for a round that never ran. A repair superseding a repair
// releases nothing, because the earlier grant may still be the one carried out
// and the later one is recorded on top of it; what the later one records as
// reserved is both together, so a decision that supersedes it afterwards
// releases what the run was reserved in total.
func (c *TriageCounters) recordDecision(decision TriageDecision) error {
	standing := make([]TriageDecision, 0, len(c.Decisions)+1)
	for _, existing := range c.Decisions {
		if existing.RunID != decision.RunID {
			standing = append(standing, existing)
			continue
		}
		if existing.Decision != TriageDecisionRepair {
			continue
		}
		if decision.Decision == TriageDecisionRepair {
			decision.Rounds += existing.Rounds
		} else {
			c.releaseReservedRounds(existing.Rounds)
		}
	}
	if len(standing) >= MaxTriageDecisions {
		return fmt.Errorf(
			"%s already carries decisions about %d stoppages, which is the bound: an item that has stopped this many times has something no further decision settles",
			c.WorkItemID, len(standing))
	}
	c.Decisions = append(standing, decision)
	return nil
}

// validTriageDecision refuses a word the vocabulary does not have, and says what
// the words are.
func validTriageDecision(decision string) error {
	switch decision {
	case TriageDecisionRepair, TriageDecisionRerun, TriageDecisionRescope,
		TriageDecisionRearm, TriageDecisionWait, TriageDecisionEscalate,
		TriageDecisionStop, TriageDecisionProceed, TriageDecisionRetireRaise:
		return nil
	default:
		return fmt.Errorf("%q is not a triage decision; the decisions are %s",
			decision, strings.Join(TriageDecisionVocabulary(), ", "))
	}
}

// validateTriageDecisions reports every contract violation in one record's
// decisions at once. The one that is about the record rather than about any
// single decision is the stoppage: decisions supersede each other by run, so two
// standing about one run describe a record the only thing that writes them could
// not have written, and a carry-out reading it would have to choose between them.
func validateTriageDecisions(decisions []TriageDecision) []error {
	var problems []error
	if len(decisions) > MaxTriageDecisions {
		problems = append(problems, fmt.Errorf("decisions about %d stoppages are recorded, which exceeds the bound of %d", len(decisions), MaxTriageDecisions))
	}
	decided := map[string]bool{}
	for index, decision := range decisions {
		if err := decision.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("decisions[%d]: %w", index, err))
			continue
		}
		if decided[decision.RunID] {
			problems = append(problems, fmt.Errorf(
				"decisions[%d]: the stoppage of run %s already has a decision standing, and a later one supersedes it rather than standing beside it",
				index, decision.RunID))
		}
		decided[decision.RunID] = true
	}
	return problems
}
