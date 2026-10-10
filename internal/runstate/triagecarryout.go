package runstate

// What became of the harness's own attempt to carry a recorded decision out.
//
// The decision beside this says what the development manager decided. It says
// nothing about whether anything happened, and for as long as carrying one out
// was a person typing a verb there was nothing for it to say: nobody had
// attempted anything, so there was no attempt to record. The harness attempts it
// itself now, on every scheduling pass, and that gap has an occupant — a
// decision the harness tried to carry out and a gate refused.
//
// Silence is the one thing that occupant must not be. Thirty-three decided items
// stood unfired for days with nothing anywhere saying so, which is exactly what a
// carry-out that failed quietly would reproduce one item at a time. So a refusal
// is written where the decision is, on the item's own record, under the same lock
// the decision was written under: which gate refused, what it said, and what
// would clear it. The docket entry the development manager reads joins it, so a
// decision that cannot be carried out says so where she is already looking.
//
// One stands per stoppage, superseded rather than appended, for the reason the
// decision beside it is superseded: what a reader needs is what stands now. And
// it is cleared by a carry-out that succeeded, because a finding about an attempt
// that has since happened is a finding that sends somebody after nothing.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// The gates a carry-out is refused by. They are a closed vocabulary because a
// refusal has to name one: "the harness could not carry this out" is the silence
// this record exists to end, worded rather than absent, and a gate named from
// prose is one that changes when somebody rewrites a sentence.
//
// They are the gates themselves rather than the conditions inside them. What a
// reader does about a refusal depends on which switch is closed — a pause and an
// intake hold are lifted by different commands, and a budget by a third — and the
// detail of what each one said travels beside it in the refusal's own words.
const (
	// TriageGateSpendingPause is the operator's switch over everything the harness
	// spends on a provider.
	TriageGateSpendingPause = "the operator's pause on harness spending"
	// TriageGateIntakeHold is the operator's switch over the work the harness
	// chooses for itself, which a carry-out is: the development manager naming an
	// item is not the operator naming it.
	TriageGateIntakeHold = "the operator's hold on what the harness chooses"
	// TriageGateDirective is an unresolved directive standing over the item.
	TriageGateDirective = "an unresolved directive"
	// TriageGateCapacity is every developer slot being occupied. It is the one gate
	// nobody has to open at all: it clears as a run ends.
	TriageGateCapacity = "developer capacity"
	// TriageGateBudget is the item's own triage budget: a cap reached, or a
	// decision the harness has already carried out as far as it goes.
	TriageGateBudget = "the item's durable triage budget"
	// TriageGatePreservedWork is the branch and worktree the stopped run left
	// behind not being what a continued developer could be handed back.
	TriageGatePreservedWork = "the work the stopped run preserved"
	// TriageGateWorkItem is the item itself not being in a state a run may start
	// or resume on.
	TriageGateWorkItem = "the work item's own state"
	// TriageGateHarness is everything else: a record that would not be read, a
	// stoppage that is not over, a store that refused a write. The refusal's own
	// words are what a reader acts on here.
	TriageGateHarness = "the harness's own records"
)

// TriageGateVocabulary lists the gates in the order a carry-out asks them, so a
// record that names one names something this file declares.
func TriageGateVocabulary() []string {
	return []string{
		TriageGateSpendingPause, TriageGateIntakeHold, TriageGateDirective,
		TriageGateCapacity, TriageGateBudget, TriageGatePreservedWork,
		TriageGateWorkItem, TriageGateHarness,
	}
}

// The bounds one carry-out record is held to. The refusal is a gate's own words
// and is bounded like a decision's reasoning; what would clear it is a sentence.
const (
	MaxTriageCarryOutRefusalBytes = 4 << 10
	MaxTriageCarryOutClearsBytes  = 1 << 10
)

// MaxTriageCarryOuts bounds how many stoppages one item's record carries a
// carry-out finding about. It is the decisions' own bound because there is at
// most one finding per decision: a stoppage nobody decided anything about is one
// nothing ever attempted.
const MaxTriageCarryOuts = MaxTriageDecisions

// TriageCarryOutRetryDelay paces the attempts at a decision a gate refused for
// something that has to change. A pass reads the docket every poll interval, so
// an unpaced retry would be the same refusal recorded several times a minute and
// a developer slot spent on it every time — which crowds out every other decided
// stoppage behind it.
//
// It does not pace the gates that are shut for every decision at once. See
// Cooling, which is where that distinction is spent.
const TriageCarryOutRetryDelay = 15 * time.Minute

// TriageCarryOut is the harness's last attempt to carry one recorded decision
// out, and what stopped it.
//
// It exists only where an attempt failed. A carry-out that succeeded clears it,
// because what it would then describe is an attempt that has since happened —
// and a finding standing over work that is running is the worst kind, since it
// reads exactly like the condition this whole mechanism exists to report.
type TriageCarryOut struct {
	// Cause is permanent when nonempty; DecidedAt binds it to the decision
	// attempted rather than to a later decision about the same run.
	Cause     triage.CarryOutCause `json:"cause,omitempty"`
	DecidedAt time.Time            `json:"decided_at,omitempty"`
	// RunID is the stopped run whose decision was being carried out, which is what
	// makes this one per stoppage rather than one per item.
	RunID string `json:"run_id"`
	// Decision is the word from the decision vocabulary that was being carried
	// out. It is written down rather than read back from the decision beside it,
	// because a decision superseded after this was written would otherwise make
	// this finding read as being about something nobody ever attempted.
	Decision string `json:"decision"`
	// Gate is which of the gates above refused it.
	Gate string `json:"gate"`
	// Refusal is what that gate said, in its own words. It is carried verbatim for
	// the reason a reviewer's findings are: what a development manager decides next
	// turns on the detail, and a summary of a refusal is a refusal she has to go
	// and read anyway.
	Refusal string `json:"refusal"`
	// Clears is what would make the same carry-out succeed, in the harness's own
	// words. It is the half a refusal alone does not always carry, and it is the
	// whole of what a reader can act on: a finding that says only "refused" is the
	// silence worded rather than ended.
	Clears string `json:"clears"`
	// Waiting marks a gate that stops everything the harness would do rather than
	// this decision in particular — the operator's pause, the intake hold, a full
	// harness. It is not a refusal of the decision and must not be read as one:
	// nothing was spent, the decision still stands, and the next pass carries it
	// out.
	//
	// What decides it is who the gate is shut for rather than who opens it. All
	// three above are opened by somebody, and they are the waiting kind because
	// they are shut for every recorded decision at once: asking again at the next
	// pass starves nothing, since every decision behind this one is standing at the
	// same switch, and pacing them would leave a lifted hold unnoticed for the
	// whole delay. A gate shut for this item alone is the other kind however
	// promptly it will be opened — a directive that pauses it, work it waits on, a
	// worktree somebody has been in — because an unpaced retry of one of those
	// takes a developer slot on every poll and crowds out every decision behind
	// it. See Cooling, which is where that distinction is spent.
	Waiting bool `json:"waiting,omitempty"`
	// Attempts is how many times the harness has tried to carry this decision out
	// and been stopped. It is what tells a gate that is about to clear from one
	// that has been closed for a week.
	Attempts  int       `json:"attempts,omitempty"`
	RefusedAt time.Time `json:"refused_at"`
	// Unattempted marks a decision no pass has attempted at all, a poll interval
	// or more after it was recorded, rather than one a gate refused. Refusal then
	// says why the pass did not reach it and Gate is the gate that kept it from
	// the attempt, and Attempts is zero, because none was made. It is its own
	// kind rather than a refusal with a different word in it because the two
	// send a reader to different places: a refusal to the gate, and this to the
	// pass that never asked the gate anything.
	//
	// It is what ends the one silence a refusal record could not: on 2026-09-19
	// the re-runs decided for yoyodyne-ifd.192 and .187 were passed over by every
	// pull for a week, and because nothing was attempted nothing was refused and
	// nothing was written. See
	// docs/diagnoses/yoyodyne-ifd-428-39-unattempted-carry-outs.md.
	Unattempted bool `json:"unattempted,omitempty"`
}

// Validate reports every contract violation in the record at once.
func (t TriageCarryOut) Validate() error {
	var problems []error
	if t.Cause != "" && (!t.Cause.Valid() || t.Waiting || t.Unattempted) {
		problems = append(problems, errors.New("a permanent carry-out cause must name a known refusal, never a wait or an unattempted decision"))
	}
	switch run := strings.TrimSpace(t.RunID); {
	case run == "":
		problems = append(problems, errors.New("a carry-out record names the stopped run whose decision it was carrying out"))
	case !ValidRunID(run):
		problems = append(problems, fmt.Errorf("%q is not a run identifier; a carry-out record names the run its docket entry is about", t.RunID))
	}
	if err := validTriageDecision(t.Decision); err != nil {
		problems = append(problems, err)
	}
	if !validTriageGate(t.Gate) {
		problems = append(problems, fmt.Errorf("%q is not a gate; the gates are %s", t.Gate, strings.Join(TriageGateVocabulary(), ", ")))
	}
	switch refusal := strings.TrimSpace(t.Refusal); {
	case refusal == "":
		problems = append(problems, errors.New("a carry-out record carries what the gate said, because a refusal nobody can read is one nobody can answer"))
	case len(refusal) > MaxTriageCarryOutRefusalBytes:
		problems = append(problems, fmt.Errorf("the refusal is %d bytes, limit is %d", len(refusal), MaxTriageCarryOutRefusalBytes))
	}
	switch clears := strings.TrimSpace(t.Clears); {
	case clears == "":
		problems = append(problems, errors.New("a carry-out record says what would clear the gate, which is the whole of what a reader can act on"))
	case len(clears) > MaxTriageCarryOutClearsBytes:
		problems = append(problems, fmt.Errorf("what would clear it is %d bytes, limit is %d", len(clears), MaxTriageCarryOutClearsBytes))
	}
	switch {
	case t.Unattempted && t.Attempts != 0:
		problems = append(problems, fmt.Errorf("a carry-out record of a decision no pass attempted counts %d attempt(s); it records that none was made", t.Attempts))
	case !t.Unattempted && t.Attempts < 1:
		problems = append(problems, fmt.Errorf("attempt %d is not an attempt; a carry-out record exists because one was made", t.Attempts))
	}
	if t.RefusedAt.IsZero() {
		problems = append(problems, errors.New("a carry-out record records when the gate refused it"))
	}
	return errors.Join(problems...)
}

// Cooling reports an attempt made too recently to be worth repeating yet.
//
// It is asked only of the gates that are shut for this decision in particular.
// The operator's pause, the intake hold, and a full harness are shut for every
// recorded decision at once, so retrying one of them starves nothing and pacing
// it would leave a decision uncarried for a quarter of an hour after the switch
// was already open — which is the latency this whole mechanism exists to remove.
// Other temporary gates are paced, because the pass attempts every decision it
// has a slot for on every pull and an unpaced retry spends a slot on every one
// of them. BlocksDecision separately suppresses permanent refusals.
func (t TriageCarryOut) Cooling(now time.Time) bool {
	// A decision nobody attempted is not paced: pacing is what stops a refusal
	// being repeated, and there is no refusal here to repeat.
	if t.Waiting || t.Unattempted {
		return false
	}
	return now.Before(t.RefusedAt.Add(TriageCarryOutRetryDelay))
}

// BlocksDecision leaves a permanent refusal alone until its decision changes,
// and paces other refusals. Older records have no decision timestamp and are
// matched by the decision's word and the refusal's time instead.
func (t TriageCarryOut) BlocksDecision(decision string, decidedAt, now time.Time) bool {
	return t.AboutDecision(decision, decidedAt) && (t.Cause != "" || t.Cooling(now))
}

// AboutDecision reports a finding made about this decision, not a later one
// recorded against the same stopped run.
func (t TriageCarryOut) AboutDecision(decision string, decidedAt time.Time) bool {
	if t.Cause == triage.CarryOutDecisionMissing && t.DecidedAt.IsZero() && !decidedAt.IsZero() {
		return false
	}
	return t.Decision == decision && !t.RefusedAt.Before(decidedAt) &&
		(t.DecidedAt.IsZero() || t.DecidedAt.Equal(decidedAt))
}

// Describe says what one carry-out finding is, for whoever is reading the item's
// record. It leads with the gate because that is what a reader acts on.
func (t TriageCarryOut) Describe() string {
	if t.Cause == triage.CarryOutNoLongerApplies {
		return fmt.Sprintf("the %q recorded about run %s no longer applies, as found at %s: %s. %s",
			t.Decision, t.RunID, t.RefusedAt.UTC().Format(time.RFC3339),
			strings.TrimSpace(t.Refusal), strings.TrimSpace(t.Clears))
	}
	if t.Unattempted {
		return fmt.Sprintf("the %q decided about the stoppage of run %s has not been attempted by any pass, as of %s, held back by %s: %s. What clears it: %s",
			t.Decision, t.RunID, t.RefusedAt.UTC().Format(time.RFC3339), t.Gate,
			strings.TrimSpace(t.Refusal), strings.TrimSpace(t.Clears))
	}
	held := "refused by"
	if t.Cause != "" {
		held = "stopped by a gate that will not clear on its own:"
	}
	if t.Waiting {
		held = "waiting on"
	}
	return fmt.Sprintf("the %q decided about the stoppage of run %s is %s %s after %d attempt(s), last at %s: %s. What clears it: %s",
		t.Decision, t.RunID, held, t.Gate, t.Attempts, t.RefusedAt.UTC().Format(time.RFC3339),
		strings.TrimSpace(t.Refusal), strings.TrimSpace(t.Clears))
}

// CarryOutOf is the finding standing about one stoppage's carry-out, and whether
// there is one at all. An item with a decision and no finding is one whose
// decision the harness has either carried out or not yet reached.
func (c TriageCounters) CarryOutOf(runID string) (TriageCarryOut, bool) {
	run := strings.TrimSpace(runID)
	if run == "" {
		return TriageCarryOut{}, false
	}
	for index := len(c.CarryOuts) - 1; index >= 0; index-- {
		if c.CarryOuts[index].RunID == run {
			return c.CarryOuts[index], true
		}
	}
	return TriageCarryOut{}, false
}

// RefusedCarryOut is the refusal standing about the decision recorded about one
// stopped run, and whether there is one: a finding about that same decision,
// written since it was made, by a gate that refused the attempt rather than one
// it waits on or one no pass reached. It is the finding CarryOutFindings counts
// as refused, asked of one run.
//
// A finding that the recovery no longer applies is not a refusal: it asks
// nobody to do anything (NoLongerApplies).
func (c TriageCounters) RefusedCarryOut(runID string) (TriageCarryOut, bool) {
	decision, decided := c.DecisionOf(runID)
	finding, found := c.CarryOutOf(runID)
	if found && finding.Cause == triage.CarryOutNoLongerApplies {
		return TriageCarryOut{}, false
	}
	if !decided && found && finding.Cause == triage.CarryOutDecisionMissing && !finding.Waiting && !finding.Unattempted {
		return finding, true
	}
	if !decided || !found || !finding.AboutDecision(decision.Decision, decision.DecidedAt) ||
		finding.Waiting || finding.Unattempted {
		return TriageCarryOut{}, false
	}
	return finding, true
}

// NoLongerApplies is the finding that the recovery recorded about one run does
// not apply, because its item is closed with the run's work settled, and
// whether one stands. It stands for the decision it was found about, or for an
// item budget with no decision at all; a decision recorded since, after an
// explicit reopening of the item, is a new question and the finding does not
// answer it.
func (c TriageCounters) NoLongerApplies(runID string) (TriageCarryOut, bool) {
	finding, found := c.CarryOutOf(runID)
	if !found || finding.Cause != triage.CarryOutNoLongerApplies {
		return TriageCarryOut{}, false
	}
	if decision, decided := c.DecisionOf(runID); decided && !finding.AboutDecision(decision.Decision, decision.DecidedAt) {
		return TriageCarryOut{}, false
	}
	return finding, true
}

// CarryOutFindings counts the findings standing about this item's decisions as
// they stand now: how many a gate refused, and how many no pass attempted. A
// finding about a decision since superseded is not counted, and neither is one
// waiting on a gate shut for everything at once, which is not a refusal of the
// decision and says so.
func (c TriageCounters) CarryOutFindings() (refused, unattempted int) {
	for _, finding := range c.CarryOuts {
		if finding.Cause == triage.CarryOutNoLongerApplies {
			continue
		}
		decision, found := c.DecisionOf(finding.RunID)
		if !found && finding.Cause == triage.CarryOutDecisionMissing && !finding.Waiting && !finding.Unattempted {
			refused++
			continue
		}
		if !found || !finding.AboutDecision(decision.Decision, decision.DecidedAt) {
			continue
		}
		switch {
		case finding.Unattempted:
			unattempted++
		case !finding.Waiting:
			refused++
		}
	}
	return refused, unattempted
}

// RecordCarryOutRefusal writes down that the harness tried to carry one recorded
// decision out and a gate stopped it, in place of whatever it last recorded about
// the same stoppage.
//
// It spends nothing and refuses nothing. The gate has already refused; this is
// the record of it, and the whole reason it exists is that a refusal nobody
// records is a decision that reads as never attempted — which is the state
// thirty-three items were in when nothing attempted them at all.
//
// The attempt count carries forward from whatever stood about the same stoppage,
// so a gate closed for a week says so rather than reading as a first try on every
// pass.
func (s *TriageStore) RecordCarryOutRefusal(ctx context.Context, workItemID string, refusal TriageCarryOut, at time.Time) (TriageCounters, error) {
	when := at
	if when.IsZero() {
		when = time.Now()
	}
	prepared := refusal
	prepared.RunID = strings.TrimSpace(prepared.RunID)
	prepared.Decision = strings.TrimSpace(prepared.Decision)
	prepared.Refusal = strings.TrimSpace(prepared.Refusal)
	prepared.Clears = strings.TrimSpace(prepared.Clears)
	prepared.RefusedAt = when.UTC()
	return s.update(ctx, workItemID, when, func(counters *TriageCounters) error {
		standing := make([]TriageCarryOut, 0, len(counters.CarryOuts)+1)
		attempts := 0
		for _, existing := range counters.CarryOuts {
			if existing.RunID == prepared.RunID {
				if existing.Decision == prepared.Decision &&
					(existing.DecidedAt.Equal(prepared.DecidedAt) ||
						(existing.DecidedAt.IsZero() && !existing.RefusedAt.Before(prepared.DecidedAt))) {
					attempts = existing.Attempts
				}
				continue
			}
			standing = append(standing, existing)
		}
		if len(standing) >= MaxTriageCarryOuts {
			return fmt.Errorf(
				"%s already carries carry-out findings about %d stoppages, which is the bound: an item this many of whose decisions could not be carried out has something no further attempt settles",
				counters.WorkItemID, len(standing))
		}
		prepared.Attempts = attempts + 1
		if err := prepared.Validate(); err != nil {
			return fmt.Errorf("invalid triage carry-out record: %w", err)
		}
		counters.CarryOuts = append(standing, prepared)
		if prepared.Cause == triage.CarryOutNoLongerApplies {
			note := "Yoyodyne did not carry out a recovery that no longer applies: " + prepared.Describe()
			counters.PendingCarryOutNotes = append(counters.PendingCarryOutNotes, note)
		} else if prepared.Cause != "" {
			note := "Yoyodyne stopped carrying out the development manager's decision: " + prepared.Describe()
			counters.PendingCarryOutNotes = append(counters.PendingCarryOutNotes, note)
		}
		return nil
	})
}

// DeliverCarryOutNotes holds the item's record lock while delivering its pending
// notes, so concurrent pulls cannot append the same note. A failure leaves the
// queue intact. The caller checks for notes already on the tracker before each
// append, covering a write that landed but whose confirmation or local save failed.
func (s *TriageStore) DeliverCarryOutNotes(ctx context.Context, workItemID string, at time.Time, deliver func(context.Context, string) error) error {
	_, err := s.update(ctx, workItemID, at, func(counters *TriageCounters) error {
		if len(counters.PendingCarryOutNotes) == 0 {
			return errNoTriageChange
		}
		for _, note := range counters.PendingCarryOutNotes {
			if err := deliver(ctx, note); err != nil {
				return err
			}
		}
		counters.PendingCarryOutNotes = nil
		return nil
	})
	return err
}

func validatePendingCarryOutNotes(notes []string) []error {
	var problems []error
	if len(notes) > MaxTriageCarryOuts {
		problems = append(problems, fmt.Errorf("%d pending carry-out notes exceed the bound of %d", len(notes), MaxTriageCarryOuts))
	}
	seen := make(map[string]bool)
	for index, note := range notes {
		if strings.TrimSpace(note) == "" || len(note) > MaxTriageCarryOutRefusalBytes+MaxTriageCarryOutClearsBytes+1024 {
			problems = append(problems, fmt.Errorf("pending carry-out note %d is empty or exceeds its refusal and clearing text bounds", index))
		}
		if seen[note] {
			problems = append(problems, fmt.Errorf("pending carry-out note %d is already queued", index))
		}
		seen[note] = true
	}
	return problems
}

// RecordCarryOutUnattempted writes down that no pass has attempted one recorded
// decision, a poll interval or more after it was recorded, and why, in place of
// whatever was last recorded about the same stoppage.
//
// It is written by the pass that noticed, and only where nothing has been
// recorded about the decision since it was made: a refusal made after the
// decision is an attempt, and an attempt that fired clears the record. A second
// writing that says exactly what the standing one says is not made, so a
// decision passed over on every pull costs one write rather than one a poll.
func (s *TriageStore) RecordCarryOutUnattempted(ctx context.Context, workItemID string, unattempted TriageCarryOut, at time.Time) (TriageCounters, error) {
	when := at
	if when.IsZero() {
		when = time.Now()
	}
	prepared := unattempted
	prepared.RunID = strings.TrimSpace(prepared.RunID)
	prepared.Decision = strings.TrimSpace(prepared.Decision)
	prepared.Refusal = strings.TrimSpace(prepared.Refusal)
	prepared.Clears = strings.TrimSpace(prepared.Clears)
	prepared.Unattempted = true
	prepared.Waiting = false
	prepared.Attempts = 0
	prepared.RefusedAt = when.UTC()
	if err := prepared.Validate(); err != nil {
		return TriageCounters{}, fmt.Errorf("invalid triage carry-out record: %w", err)
	}
	return s.update(ctx, workItemID, when, func(counters *TriageCounters) error {
		standing := make([]TriageCarryOut, 0, len(counters.CarryOuts)+1)
		for _, existing := range counters.CarryOuts {
			if existing.RunID == prepared.RunID {
				if existing.Unattempted && existing.Decision == prepared.Decision &&
					existing.Gate == prepared.Gate && existing.Refusal == prepared.Refusal {
					return errNoTriageChange
				}
				continue
			}
			standing = append(standing, existing)
		}
		if len(standing) >= MaxTriageCarryOuts {
			return fmt.Errorf(
				"%s already carries carry-out findings about %d stoppages, which is the bound: an item this many of whose decisions could not be carried out has something no further attempt settles",
				counters.WorkItemID, len(standing))
		}
		counters.CarryOuts = append(standing, prepared)
		return nil
	})
}

// ClearCarryOut removes the finding about one stoppage, which is what a carry-out
// that succeeded does.
//
// An item with no finding about that stoppage is left exactly as it is rather
// than treated as an error: the ordinary carry-out succeeds first time and has
// nothing to clear, and a caller that had to know which it was would be a caller
// reading the record twice.
func (s *TriageStore) ClearCarryOut(ctx context.Context, workItemID, runID string, at time.Time) (TriageCounters, error) {
	run := strings.TrimSpace(runID)
	if run == "" {
		return TriageCounters{}, errors.New("a stopped run is required to clear the carry-out finding about it")
	}
	return s.update(ctx, workItemID, at, func(counters *TriageCounters) error {
		standing := make([]TriageCarryOut, 0, len(counters.CarryOuts))
		for _, existing := range counters.CarryOuts {
			if existing.RunID != run {
				standing = append(standing, existing)
			}
		}
		if len(standing) == len(counters.CarryOuts) {
			return errNoTriageChange
		}
		counters.CarryOuts = standing
		return nil
	})
}

// validTriageGate reports a word the gate vocabulary has.
func validTriageGate(gate string) bool {
	for _, known := range TriageGateVocabulary() {
		if gate == known {
			return true
		}
	}
	return false
}

// validateTriageCarryOuts reports every contract violation in one record's
// carry-out findings at once. The one that is about the record rather than about
// any single finding is the stoppage: findings supersede each other by run, so two
// standing about one run describe a record the only thing that writes them could
// not have written.
func validateTriageCarryOuts(carryOuts []TriageCarryOut) []error {
	var problems []error
	if len(carryOuts) > MaxTriageCarryOuts {
		problems = append(problems, fmt.Errorf("carry-out findings about %d stoppages are recorded, which exceeds the bound of %d", len(carryOuts), MaxTriageCarryOuts))
	}
	recorded := map[string]bool{}
	for index, carryOut := range carryOuts {
		if err := carryOut.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("carry_outs[%d]: %w", index, err))
			continue
		}
		if recorded[carryOut.RunID] {
			problems = append(problems, fmt.Errorf(
				"carry_outs[%d]: the stoppage of run %s already has a carry-out finding standing, and a later one supersedes it rather than standing beside it",
				index, carryOut.RunID))
		}
		recorded[carryOut.RunID] = true
	}
	return problems
}
