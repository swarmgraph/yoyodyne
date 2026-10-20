package runstate

// The brake's own account of the hold it placed, and what it does next.
//
// A hold the operator places waits on the operator, which is the whole point
// of the switch. A hold the harness's own failure-storm brake places used to
// wait on a person too, and that is the stall this record ends: the brake
// tripped on 2026-09-02, 09-05, 09-13, 09-17, and 09-19, and every time the
// line sat held until somebody noticed — on the last of them for about two
// hours, with a free developer slot idle. The operator's decision, recorded as a
// directive the same day, was that the brake may trip so long as the
// development manager is always invoked at once to sort it out and nothing
// waits.
//
// So a brake hold carries this. It names the runs that tripped it, so the
// development manager is shown what blocked and why rather than told that
// something did. It records that she was summoned, what she decided, and when a
// probe starts by itself if she has not — because a hold that waits on a turn
// nobody can take is the same stall one layer over. And it records the probe:
// one run started under the hold, whose landing reopens intake and whose
// blocking keeps it held and puts the question to her again. A brake hold comes
// to wait on a person two ways, both recorded here as such: her deciding that it
// should — an escalation to the operator — or the harness deciding it after a
// bounded number of those summons-and-probe cycles have gone round without her
// escalating. The bound is what keeps "nothing waits" from becoming a loop that
// spends one of her turns and one probe run every cooldown on a broken machine
// for as long as nobody happens to look.
//
// The record is on the hold rather than beside it because every surface that
// says intake is held reads the hold, and what those surfaces were missing was
// exactly this: who is deciding, and what happens if nobody does.

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// IntakeBrakeDecision is what the development manager decided about a brake
// hold. It is a closed vocabulary because the harness acts on it: a release lifts
// the hold, a probe starts one run under it, and an escalation is the one
// decision that makes the hold a person's.
type IntakeBrakeDecision string

const (
	// BrakeDecisionRelease lifts the hold: the line is fine, or what stopped it
	// has been dealt with, and the harness may choose work again.
	BrakeDecisionRelease IntakeBrakeDecision = "release"
	// BrakeDecisionProbe keeps the hold and starts one probe run now rather than
	// at the cooldown: a landed probe reopens intake, a blocked one keeps it held.
	BrakeDecisionProbe IntakeBrakeDecision = "probe"
	// BrakeDecisionEscalate keeps the hold and hands it up: it is the Lead
	// Product Manager's to decide from then on, and the operator is told and may
	// release it. It is the one decision the development manager is told to be
	// sparing with.
	BrakeDecisionEscalate IntakeBrakeDecision = "escalate"
)

// IntakeBrakeDecisionVocabulary lists the decisions in the order the contract
// states them, so a refusal names exactly what was available.
func IntakeBrakeDecisionVocabulary() []string {
	return []string{string(BrakeDecisionRelease), string(BrakeDecisionProbe), string(BrakeDecisionEscalate)}
}

// Valid reports a decision this harness acts on.
func (d IntakeBrakeDecision) Valid() bool {
	switch d {
	case BrakeDecisionRelease, BrakeDecisionProbe, BrakeDecisionEscalate:
		return true
	}
	return false
}

// MaxBrakeTextBytes bounds each line of prose the brake record carries: the
// reason a run blocked, the reason for a decision, the problem that stopped a
// summons. A blocked run's reason can be a provider's whole output, and a hold
// refused for its length is a line nothing can read the state of.
const MaxBrakeTextBytes = 2 << 10

// MaxBrakeBlockedRuns bounds how many runs a trip names. The configured brake is
// three, and a trip records what tripped it rather than everything that ever
// blocked, so the bound is a sanity check rather than a working limit.
const MaxBrakeBlockedRuns = 16

// BrakeBlockedRun is one run that counted toward the trip: which run, over which
// item, and the reason it blocked, in the words the run's own record gave.
type BrakeBlockedRun struct {
	RunID      string `json:"run_id,omitempty"`
	WorkItemID string `json:"work_item_id"`
	Reason     string `json:"reason"`
}

// IntakeProbe is one run started under the brake's hold to find out whether the
// line is fine. It is recorded before the run starts, so the hold says a probe
// is in flight while it is, and settled when the run ends, so the hold says
// what became of it.
type IntakeProbe struct {
	WorkItemID string    `json:"work_item_id"`
	RunID      string    `json:"run_id,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	// EndedAt is when the probe settled, and Landed says whether its work reached
	// the target branch. Blocked is a probe that stopped on a durable blocker or
	// failed outright, which is the ending that keeps the hold; a probe that ended
	// neither way — declined to another process, parked on a provider window —
	// says so in Reason and decides nothing about the line.
	EndedAt *time.Time `json:"ended_at,omitempty"`
	Landed  bool       `json:"landed,omitempty"`
	Blocked bool       `json:"blocked,omitempty"`
	Reason  string     `json:"reason,omitempty"`
}

// InFlight reports a probe that has started and not settled.
func (p IntakeProbe) InFlight() bool { return p.EndedAt == nil }

// BrakeEscalation is the harness escalating its own hold to the operator,
// which it does when the configured number of summons-and-probe cycles have
// gone round with the development manager not escalating it herself. It
// records how many cycles were spent and what stopped the last probe, because
// those two facts are the whole of what the operator is told and the whole of
// what a reader of the hold afterwards wants to know.
type BrakeEscalation struct {
	At     time.Time `json:"at"`
	Cycles int       `json:"cycles"`
	// Probe is the item the last probe ran, and Reason is why that probe blocked,
	// in the run's own words.
	Probe  string `json:"probe,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// IntakeBrake is the brake's record on the hold it placed.
type IntakeBrake struct {
	// Blocked is the runs that tripped it, in the order they blocked.
	Blocked []BrakeBlockedRun `json:"blocked"`
	// SummonedAt is when the development manager's sweep was last summoned to
	// decide about this hold, and SummonProblem is what stopped the last summons
	// where one did. A summons that could not be made is not a hold that waits:
	// the cooldown below starts a probe whether or not she was reached.
	SummonedAt    *time.Time `json:"summoned_at,omitempty"`
	SummonProblem string     `json:"summon_problem,omitempty"`
	// Decision is what she decided, DecidedAt when, DecidedBy which conversation,
	// and DecisionReason her argument. All absent until she records one.
	Decision       IntakeBrakeDecision `json:"decision,omitempty"`
	DecidedAt      *time.Time          `json:"decided_at,omitempty"`
	DecidedBy      string              `json:"decided_by,omitempty"`
	DecisionReason string              `json:"decision_reason,omitempty"`
	// CooldownEndsAt is when a probe starts by itself if no decision has been
	// recorded by then. It is restarted by a probe that blocked, so a broken
	// machine is probed once per cooldown rather than continuously.
	CooldownEndsAt time.Time `json:"cooldown_ends_at"`
	// Probe is the probe run this trip has going or last made, and Probes counts
	// every probe the trip has made.
	Probe  *IntakeProbe `json:"probe,omitempty"`
	Probes int          `json:"probes,omitempty"`
	// Cycles counts the summons-and-probe cycles the trip has spent: each is a
	// probe that blocked and put the question to the development manager again.
	// A probe that landed ends the hold, and one that ended neither way decided
	// nothing, so neither is a cycle. CycleBound is execution.brake_escalation_cycles
	// as the harness last read it — the number of cycles after which the hold
	// escalates to the Lead Product Manager by itself — and zero is no bound at all.
	Cycles     int `json:"cycles,omitempty"`
	CycleBound int `json:"cycle_bound,omitempty"`
	// Escalation is the harness having escalated the hold to the Lead Product Manager at
	// that bound. It is the second way a brake hold comes to wait on a person,
	// and it is recorded apart from her decision because every surface that
	// says whose move the hold is has to say which of the two it was.
	Escalation *BrakeEscalation `json:"escalation,omitempty"`
}

// Validate reports every contract violation in the record at once.
func (b IntakeBrake) Validate() error {
	var problems []error
	if len(b.Blocked) == 0 {
		problems = append(problems, errors.New("a brake trip names the runs that tripped it"))
	}
	if len(b.Blocked) > MaxBrakeBlockedRuns {
		problems = append(problems, fmt.Errorf("a brake trip names %d runs, limit is %d", len(b.Blocked), MaxBrakeBlockedRuns))
	}
	for index, blocked := range b.Blocked {
		if strings.TrimSpace(blocked.WorkItemID) == "" {
			problems = append(problems, fmt.Errorf("blocked[%d]: work item id is required", index))
		}
		if len(blocked.Reason) > MaxBrakeTextBytes {
			problems = append(problems, fmt.Errorf("blocked[%d]: reason is %d bytes, limit is %d", index, len(blocked.Reason), MaxBrakeTextBytes))
		}
	}
	if b.Decision != "" && !b.Decision.Valid() {
		problems = append(problems, fmt.Errorf("brake decision %q is not one this harness acts on", b.Decision))
	}
	if b.Decision != "" && b.DecidedAt == nil {
		problems = append(problems, errors.New("a brake decision records when it was made"))
	}
	if b.CooldownEndsAt.IsZero() {
		problems = append(problems, errors.New("a brake trip records when its cooldown ends"))
	}
	for _, text := range []struct{ name, value string }{
		{"summon problem", b.SummonProblem},
		{"decision reason", b.DecisionReason},
		{"decided by", b.DecidedBy},
	} {
		if len(text.value) > MaxBrakeTextBytes {
			problems = append(problems, fmt.Errorf("%s is %d bytes, limit is %d", text.name, len(text.value), MaxBrakeTextBytes))
		}
	}
	if b.Probe != nil {
		if strings.TrimSpace(b.Probe.WorkItemID) == "" {
			problems = append(problems, errors.New("a probe names the work item it runs"))
		}
		if b.Probe.StartedAt.IsZero() {
			problems = append(problems, errors.New("a probe records when it started"))
		}
		if b.Probe.Landed && b.Probe.Blocked {
			problems = append(problems, errors.New("a probe cannot have both landed and blocked"))
		}
		if (b.Probe.Landed || b.Probe.Blocked) && b.Probe.EndedAt == nil {
			problems = append(problems, errors.New("a probe that ended records when"))
		}
		if len(b.Probe.Reason) > MaxBrakeTextBytes {
			problems = append(problems, fmt.Errorf("probe reason is %d bytes, limit is %d", len(b.Probe.Reason), MaxBrakeTextBytes))
		}
	}
	if b.Cycles < 0 {
		problems = append(problems, fmt.Errorf("a brake trip cannot have spent %d cycles", b.Cycles))
	}
	if b.CycleBound < 0 {
		problems = append(problems, fmt.Errorf("a cycle bound of %d describes no number of cycles anybody could count", b.CycleBound))
	}
	if b.Escalation != nil {
		if b.Escalation.At.IsZero() {
			problems = append(problems, errors.New("the harness's escalation records when it was made"))
		}
		if b.Escalation.Cycles <= 0 {
			problems = append(problems, errors.New("the harness's escalation names the cycles it was made after"))
		}
		if len(b.Escalation.Reason) > MaxBrakeTextBytes {
			problems = append(problems, fmt.Errorf("escalation reason is %d bytes, limit is %d", len(b.Escalation.Reason), MaxBrakeTextBytes))
		}
	}
	return errors.Join(problems...)
}

// Escalated reports the hold having been escalated past the development
// manager — to the Lead Product Manager, with the operator told — by her
// decision, or by the harness at the cycle bound.
func (b IntakeBrake) Escalated() bool {
	return b.Decision == BrakeDecisionEscalate || b.Escalation != nil
}

// EscalatedByHarness reports the hold having been escalated by the harness
// itself, at the cycle bound, rather than by her decision.
func (b IntakeBrake) EscalatedByHarness() bool { return b.Escalation != nil }

// CycleBoundReached reports the trip having spent every cycle the bound allows,
// which is the moment the harness escalates rather than summoning her again. A
// trip with no bound never reaches it.
func (b IntakeBrake) CycleBoundReached() bool {
	return b.CycleBound > 0 && b.Cycles >= b.CycleBound
}

// Probing reports a probe in flight.
func (b IntakeBrake) Probing() bool { return b.Probe != nil && b.Probe.InFlight() }

// ProbeDue reports that a probe should start now: the development manager
// decided on one, or the cooldown has run out with no decision recorded. A hold
// she escalated, a hold she released, and a hold with a probe already going are
// never due one.
func (b IntakeBrake) ProbeDue(now time.Time) bool {
	if b.Escalated() || b.Decision == BrakeDecisionRelease || b.Probing() {
		return false
	}
	if b.Decision == BrakeDecisionProbe {
		return true
	}
	return !now.Before(b.CooldownEndsAt)
}

// Whose is whose move the hold is, in the words every surface uses beside a
// held intake: the development manager's while she is deciding, the harness's
// while a probe runs or a decision waits to be carried out, and the Lead
// Product Manager's once it has been escalated — by her, or by the harness at
// the bound — with the operator told, under the operating rule that the brake
// never summons him (789c052c) and the ownership registry's rule for the hold.
// While the harness is working the hold the clause names the loop it is in,
// because a line that says the same thing every hour for a whole night is
// exactly what this bound exists to end, and a reader should be able to see
// from any one of those lines how much longer it goes on.
func (b IntakeBrake) Whose() string {
	switch {
	case b.Decision == BrakeDecisionRelease:
		return "the harness's — the development manager decided to release it, and the watching session lifts it at its next poll"
	case b.Decision == BrakeDecisionEscalate:
		return "the Lead Product Manager's — the development manager escalated it, and she decides whether intake reopens; the operator is told and may release it, and nothing new is chosen until `yoyo release` lifts it"
	case b.EscalatedByHarness():
		return fmt.Sprintf("the Lead Product Manager's — the harness escalated it after %s, and she decides whether intake reopens; the operator is told and may release it, and nothing new is chosen until `yoyo release` lifts it", b.escalationAccount())
	case b.Probing():
		return b.loop(fmt.Sprintf("the harness's — a probe run of %s is in flight; intake reopens if it lands, and the hold stays with the development manager asked again if it blocks", b.Probe.WorkItemID))
	case b.Decision == BrakeDecisionProbe:
		return b.loop("the harness's — the development manager decided on a probe run, and the watching session starts it at its next poll")
	default:
		return b.loop(fmt.Sprintf("the development manager's — she decides what happens to it, and a probe run starts by itself at %s if she has not", b.CooldownEndsAt.UTC().Format(time.RFC3339)))
	}
}

// Standing is what happens to the hold next, as the clause a banner or a
// session's account puts after the hold's cause. It says the same thing Whose
// says without opening on whose move it is, because the surfaces that print it
// have already said that or do not ask.
func (b IntakeBrake) Standing() string {
	switch {
	case b.Decision == BrakeDecisionRelease:
		return "the development manager decided to release it, and the watching session lifts it at its next poll"
	case b.Decision == BrakeDecisionEscalate:
		return "the development manager escalated it to the Lead Product Manager, and the operator is told, so it stays held until somebody releases it"
	case b.EscalatedByHarness():
		return fmt.Sprintf("the harness escalated it to the Lead Product Manager after %s, and the operator is told, so it stays held until somebody releases it", b.escalationAccount())
	case b.Probing():
		return b.loop(fmt.Sprintf("a probe run of %s is in flight: intake reopens if it lands, and the hold stays if it blocks", b.Probe.WorkItemID))
	case b.Decision == BrakeDecisionProbe:
		return b.loop("the development manager decided on a probe run, which the watching session starts at its next poll")
	case b.SummonedAt != nil:
		return b.loop(fmt.Sprintf("the development manager was summoned at %s to decide what happens to it, and a probe run starts by itself at %s if she has not",
			b.SummonedAt.UTC().Format(time.RFC3339), b.CooldownEndsAt.UTC().Format(time.RFC3339)))
	case strings.TrimSpace(b.SummonProblem) != "":
		return b.loop(fmt.Sprintf("the development manager could not be summoned (%s), and a probe run starts by itself at %s",
			singleLineBrakeText(b.SummonProblem), b.CooldownEndsAt.UTC().Format(time.RFC3339)))
	default:
		return b.loop(fmt.Sprintf("the development manager is being summoned to decide what happens to it, and a probe run starts by itself at %s if she has not",
			b.CooldownEndsAt.UTC().Format(time.RFC3339)))
	}
}

// Loop names where the summons-and-probe loop stands, for every surface that
// says the harness is still working the hold: which cycle this is, and at what
// cycle the harness escalates it to the Lead Product Manager itself. Nothing is named for a
// trip with no bound and no cycle spent, which is the first cooldown of a hold
// configured never to escalate on its own — the one case with no loop to speak
// of yet.
func (b IntakeBrake) Loop() string {
	switch {
	case b.CycleBound > 0:
		return fmt.Sprintf("summons-and-probe cycle %d of at most %d; the harness escalates it to the Lead Product Manager itself after %d probe%s blocked",
			b.Cycles+1, b.CycleBound, b.CycleBound, plural(b.CycleBound))
	case b.Cycles > 0:
		return fmt.Sprintf("summons-and-probe cycle %d, with no bound configured on how many there may be", b.Cycles+1)
	default:
		return ""
	}
}

// loop closes a clause about the harness working the hold on where the loop
// stands, where there is a loop to name.
func (b IntakeBrake) loop(clause string) string {
	if named := b.Loop(); named != "" {
		return clause + " (" + named + ")"
	}
	return clause
}

// escalationAccount is the harness's escalation in one clause: the cycles it
// spent and what stopped the last probe, which are the two facts the operator
// is owed about a loop that ran without them.
func (b IntakeBrake) escalationAccount() string {
	escalation := b.Escalation
	account := fmt.Sprintf("%d summons-and-probe cycle%s with the development manager not escalating it", escalation.Cycles, plural(escalation.Cycles))
	probe := "the last probe run"
	if strings.TrimSpace(escalation.Probe) != "" {
		probe = "the last probe run, of " + strings.TrimSpace(escalation.Probe) + ","
	}
	if strings.TrimSpace(escalation.Reason) != "" {
		return fmt.Sprintf("%s (%s blocked: %s)", account, probe, clauseBrakeText(escalation.Reason))
	}
	return account
}

// MaxBrakeClauseBytes bounds a stop reason quoted inside a clause every hourly
// line and attention line carries. The record keeps the whole reason, and the
// summons quotes it whole; a clause said every hour has room for the start of
// it and where to read the rest.
const MaxBrakeClauseBytes = 240

// clauseBrakeText folds a stop reason to the one bounded clause a repeated
// line can carry.
func clauseBrakeText(text string) string {
	return oneline.Fold(text, MaxBrakeClauseBytes)
}

func plural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

// Entries says what tripped the brake, one line per run, for the message that
// puts the trip in front of the development manager and for whoever reads the
// hold afterwards.
func (b IntakeBrake) Entries() []string {
	lines := make([]string, 0, len(b.Blocked))
	for _, blocked := range b.Blocked {
		run := "a run"
		if strings.TrimSpace(blocked.RunID) != "" {
			run = "run " + strings.TrimSpace(blocked.RunID)
		}
		lines = append(lines, fmt.Sprintf("%s of %s: %s", run, blocked.WorkItemID, singleLineBrakeText(blocked.Reason)))
	}
	return lines
}

// singleLineBrakeText folds prose to one bounded line for a sentence that has
// room for one. The record keeps the whole of it.
func singleLineBrakeText(text string) string {
	return oneline.Fold(text, MaxBrakeTextBytes-len(oneline.Marker))
}

// BoundBrakeText holds one line of brake prose to what the record accepts,
// cutting rather than refusing: a trip refused for the length of a provider's
// error message is a line nobody can read the state of. The cut falls on a rune
// boundary and is marked, because a provider's error is as likely as anything to
// carry multi-byte text, and half a rune stored is a record that is not text.
func BoundBrakeText(text string) string {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) <= MaxBrakeTextBytes {
		return trimmed
	}
	const marker = " […]"
	cut := MaxBrakeTextBytes - len(marker)
	for cut > 0 && !utf8.RuneStart(trimmed[cut]) {
		cut--
	}
	return strings.TrimSpace(trimmed[:cut]) + marker
}

// ErrNoBrakeHold reports a brake operation on a hold the brake did not place, or
// on no hold at all. A decision about the operator's own hold is refused rather
// than recorded onto it: the operator's hold waits on the operator, and a record
// saying the development manager decided about it would be a decision about a
// switch she does not hold.
var ErrNoBrakeHold = errors.New("intake is not held by the harness's own brake, so there is no brake decision to make")

// Brake places the brake's hold with its trip attached. Like Hold it never
// restamps a hold already in force — the brake tripping over the operator's own
// hold leaves theirs, which is the truth about who stopped the line — and it
// reports the hold in force either way.
func (s *IntakeHoldStore) Brake(trip IntakeBrake, reason string, at time.Time) (IntakeHold, error) {
	if err := trip.Validate(); err != nil {
		return IntakeHold{}, fmt.Errorf("invalid brake trip: %w", err)
	}
	release, err := s.lock()
	if err != nil {
		return IntakeHold{}, err
	}
	defer release()
	if existing, held, err := s.Held(); err != nil || held {
		return existing, err
	}
	recorded := IntakeHold{
		SchemaVersion: IntakeHoldSchemaVersion,
		ProductID:     s.productID,
		HeldAt:        at.UTC(),
		HeldBy:        IntakeHolderBrake,
		Reason:        strings.TrimSpace(reason),
		Brake:         &trip,
	}
	if err := s.write(recorded); err != nil {
		return IntakeHold{}, err
	}
	return recorded, nil
}

// ReviseBrake rewrites the brake's record on its own hold under the store's
// lock: the summons made, the probe started or settled, the cooldown restarted.
// It refuses on a hold the brake did not place and on no hold, because the only
// record it may revise is the brake's own.
func (s *IntakeHoldStore) ReviseBrake(revise func(*IntakeBrake) error) (IntakeHold, error) {
	release, err := s.lock()
	if err != nil {
		return IntakeHold{}, err
	}
	defer release()
	held, found, err := s.Held()
	if err != nil {
		return IntakeHold{}, err
	}
	if !found || held.HeldBy != IntakeHolderBrake || held.Brake == nil {
		return held, ErrNoBrakeHold
	}
	revised := *held.Brake
	if err := revise(&revised); err != nil {
		return held, err
	}
	if err := revised.Validate(); err != nil {
		return held, fmt.Errorf("invalid brake record: %w", err)
	}
	held.Brake = &revised
	if err := s.write(held); err != nil {
		return IntakeHold{}, err
	}
	return held, nil
}

// ReleaseBrake lifts the brake's own hold and no other, under the store's
// lock. It is what the watching session lifts a hold with — on the development
// manager's decision, or on a probe that landed — and the reason it is not
// Release is the window between reading the hold and lifting it: a hold the
// operator placed in that window is theirs, and this leaves it exactly where
// it is, reporting nothing lifted. by is what moved the harness to lift it,
// recorded as the release's author.
func (s *IntakeHoldStore) ReleaseBrake(by string, at time.Time) (IntakeHold, bool, error) {
	release, err := s.lock()
	if err != nil {
		return IntakeHold{}, false, err
	}
	defer release()
	held, found, err := s.Held()
	if err != nil {
		return IntakeHold{}, false, err
	}
	if !found || !held.Braked() {
		return held, false, nil
	}
	return s.lift(held, by, at)
}

// ErrBrakeEscalatedByHarness refuses a probe decision on a hold the harness has
// already escalated to the Lead Product Manager: the bound ended the loop, and a probe
// would start it again with nothing to stop it the second time. A release is
// still hers to record — the line being fine is news whoever finds it out.
var ErrBrakeEscalatedByHarness = errors.New("the harness escalated this hold to the Lead Product Manager at the cycle bound, so no further probe is started under it; release it if the line is fine, or leave it to her")

// DecideBrake records the development manager's decision about the brake's
// hold. A decision already recorded is replaced: she may change her mind, and
// the later decision is the one the harness acts on. It is the one write here a
// conversation makes; everything else is the watching session's.
func (s *IntakeHoldStore) DecideBrake(decision IntakeBrakeDecision, reason, by string, at time.Time) (IntakeHold, error) {
	if !decision.Valid() {
		return IntakeHold{}, fmt.Errorf("brake decision %q is not one this harness acts on; the decisions are %s",
			decision, strings.Join(IntakeBrakeDecisionVocabulary(), ", "))
	}
	return s.ReviseBrake(func(brake *IntakeBrake) error {
		if decision == BrakeDecisionProbe && brake.EscalatedByHarness() {
			return ErrBrakeEscalatedByHarness
		}
		decidedAt := at.UTC()
		brake.Decision = decision
		brake.DecidedAt = &decidedAt
		brake.DecidedBy = BoundBrakeText(by)
		brake.DecisionReason = BoundBrakeText(reason)
		return nil
	})
}
