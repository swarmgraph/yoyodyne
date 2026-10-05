package runstate

// When a recurring task last fired, and what came of it.
//
// Two records, one store, because they answer the two halves of the same
// question. The claim is what makes the cadence a cadence: a task fires when its
// interval has passed since the last time it fired, and the reading and the
// writing happen under one lock so two sessions polling the same schedule
// produce one firing rather than two. The log is what makes a firing worth
// having: a pass nobody watched reaches an operator only if what it found is
// written down somewhere they can read at leisure.
//
// The cadence is measured from the last firing rather than against a wall-clock
// grid, and that is a decision rather than an implementation detail. A harness
// that was off between two and five fires once when it comes back, not three
// times; a machine that slept through the night owes nobody eight sweeps. What a
// grid would buy is sweeps landing at the top of the hour, which is worth
// nothing to anybody reading these reports afterwards.
//
// A firing that failed still moves the clock. That is the other decision worth
// naming, and it is the opposite of the escalation record beside it: a stoppage
// that failed to reach the development manager is retried soon because it is one
// specific thing nobody has heard about, and a recurring pass that failed is
// simply run again at its next cadence, because the next pass looks at
// everything this one would have. Retrying it sooner would spend turns for
// nothing on a provider that is out of capacity, which on a busy poll loop is a
// firing every pull for as long as the outage lasts.
//
// Like the escalations beside it, it is one home per machine, for the same
// reason: what the coordination of two harnesses over one repository would take
// belongs to the team-mode epic rather than here.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
	"github.com/mason-bryant/yoyodyne/internal/terms"
)

// SweepSchemaVersion is 1 and has never changed. It is versioned independently
// of run and conversation state, for the reason a report is: a sweep outlives
// the session that fired it, has no phase and nothing to integrate, and is
// written once and never revised.
const SweepSchemaVersion = 1

// maxEncodedSweepBytes bounds one encoded sweep record, including the trailing
// newline. The writer and the reader share it, so a sweep that was written is
// always one that can be read back.
//
// It is sized against the largest account a firing can legitimately produce
// rather than against a round number, because a bound below that is a bound that
// throws the busiest passes' reports away as it writes them. One turn's block is
// capped at sweep.MaxBlockBytes and a firing folds at most sweep.MaxMergedTurns
// of them together, so the account cannot exceed that product before it is
// encoded. Encoding it again for the record can grow it by up to maxJSONGrowth,
// so the bound is sized against the encoded product rather than the decoded one:
// a pass that quoted code or markup in every finding is a busy pass, not a
// hostile one. What is here is comfortably above that, and a test in this
// package keeps the two in step.
const maxEncodedSweepBytes = 2 << 20

// maxJSONGrowth is the most bytes JSON encoding writes for one byte of text:
// "<", ">" and "&" are each escaped as six bytes, < and its kind. Every
// other character grows less.
const maxJSONGrowth = 6

// MaxSweepTextBytes bounds the prose a record carries — what stopped a firing,
// and what stopped the last one on its claim. It is exported because what writes
// that prose is outside this package and has to be able to hold itself to the
// same number: a record refused for a problem too long to store is a firing whose
// report is lost over the description of a smaller failure.
const MaxSweepTextBytes = 4 << 10

// MaxSweepModelBytes bounds the model a record names. A selector is a short
// name, and a served model on another provider is that name with the provider
// beside it, so this is far above either; it is exported so the writer can hold
// what it records to it rather than lose the pass's report over the model.
const MaxSweepModelBytes = 1 << 10

// MaxSweepCriticals bounds how many critical reports one pass records as
// delivered. It is exported so the writer holds one firing's delivery to it;
// the criticals past it are delivered on the next pull.
const MaxSweepCriticals = 10

// MaxSweepSavedWrites bounds how many saved writes one pass records. A reply
// asks for at most four memories and one lane report, and a pass takes a few
// turns of a few rounds each, so this is well above what one pass makes; it is
// exported so the writer holds the list to it rather than lose the pass's
// report over its length.
const MaxSweepSavedWrites = 64

// SavedWriteKind is which store a pass's saved write went into.
type SavedWriteKind string

const (
	// SavedMemory is a write into the agent's own memory.
	SavedMemory SavedWriteKind = "memory"
	// SavedLaneReport is a new version of a program manager's lane report.
	SavedLaneReport SavedWriteKind = "lane-report"
)

// SavedWrite is one memory or lane-report write a pass's turns made that its
// store recorded. Each is durable in its own store the moment it is made, so a
// pass that fails afterwards does not undo it; the record names it so the pass
// says which writes stood, and so the pass run again over the same events is
// told they are already saved rather than writing them twice. It names the
// write and its number, never its text, which is in the store.
type SavedWrite struct {
	Kind SavedWriteKind `json:"kind"`
	// Action is the memory operation — remember, compact, or retire — and empty
	// for a lane report.
	Action string `json:"action,omitempty"`
	// Memory is the memory's name, and empty for a lane report.
	Memory string `json:"memory,omitempty"`
	// Revision is the number the store gave the write: the memory's revision,
	// or the lane report's version.
	Revision int `json:"revision"`
}

// Describe says what the write was, in the words a pass's record and the
// message waking the next pass use.
func (w SavedWrite) Describe() string {
	if w.Kind == SavedLaneReport {
		return fmt.Sprintf("lane report version %d", w.Revision)
	}
	return fmt.Sprintf("memory %q (%s, revision %d)", w.Memory, w.Action, w.Revision)
}

// Validate reports what makes the write one the record cannot name.
func (w SavedWrite) Validate() error {
	var problems []error
	switch w.Kind {
	case SavedMemory:
		if strings.TrimSpace(w.Memory) == "" || len(w.Memory) > MaxSweepModelBytes {
			problems = append(problems, errors.New("a saved memory names the memory"))
		}
		if strings.TrimSpace(w.Action) == "" || len(w.Action) > MaxSweepModelBytes {
			problems = append(problems, errors.New("a saved memory names its action"))
		}
	case SavedLaneReport:
		if w.Memory != "" || w.Action != "" {
			problems = append(problems, errors.New("a saved lane report names no memory and no action"))
		}
	default:
		problems = append(problems, fmt.Errorf("kind %q is not %q or %q", w.Kind, SavedMemory, SavedLaneReport))
	}
	if w.Revision < 1 {
		problems = append(problems, fmt.Errorf("revision is %d, and a saved write is numbered from 1", w.Revision))
	}
	return errors.Join(problems...)
}

// SweepClaim is the durable record of one recurring task's cadence: when it last
// fired, and what stopped that firing where something did.
//
// It is written before the turns are taken, so a process that dies mid-pass has
// recorded a firing that produced nothing rather than left a cadence that fires
// again on the next pull for as long as the deaths continue.
type SweepClaim struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// Task is the configured name the cadence belongs to. A task renamed in the
	// configuration is a new cadence, which is the honest answer: nothing knows
	// the new name is the old one.
	Task string `json:"task"`
	// FiredAt is when the most recent firing was claimed. The next is due from
	// it, subject to the adoption time below. Firings counts them, which is what
	// a report of a schedule is read for: a task that has fired forty times and
	// a task nothing has ever woken look identical without it.
	FiredAt   time.Time `json:"fired_at"`
	Firings   int       `json:"firings"`
	UpdatedAt time.Time `json:"updated_at"`
	// Every and CadenceAt record the interval the harness adopted and when it
	// first read it. A new interval never owes a pass before that observation.
	Every     time.Duration `json:"every,omitempty"`
	CadenceAt time.Time     `json:"cadence_at,omitempty"`
	// CadenceUncertain marks an older claim that carried no adoption evidence.
	CadenceUncertain bool `json:"cadence_uncertain,omitempty"`
	// Problem is what stopped the last firing, and is cleared by one that worked.
	// A claim carrying one is a cadence that is running and producing nothing,
	// which is a state somebody has to be able to find without reading a log of
	// reports that were never written.
	Problem string `json:"problem,omitempty"`
	// Summoned marks a firing claimed out of its cadence — the brake's summons
	// of the development manager's sweep, or an event wake of a program manager
	// instance — and is cleared by a firing the cadence claimed. It is what says
	// which trigger a firing that never recorded its ending was taken by.
	Summoned bool `json:"summoned,omitempty"`
}

// Settled reports a firing whose ending has been written against its claim.
// A claim is written stamped with the moment it fired and settling stamps it
// again later, so a claim still carrying its firing's own stamp is one nothing
// settled: a firing still in flight, or one whose process stopped mid-pass.
func (c SweepClaim) Settled() bool {
	return c.UpdatedAt.After(c.FiredAt)
}

// Due reports a task whose interval has passed. A task that has never fired is
// due at once rather than one interval from now: a schedule turned on at nine
// that produced nothing until ten would look broken for an hour, and the first
// pass is the one most worth having.
func (c SweepClaim) Due(every time.Duration, now time.Time) bool {
	if c.FiredAt.IsZero() {
		return true
	}
	return !now.Before(c.NextDue(every))
}

// NextDue is when this task fires again, for a report that says what a schedule
// is going to do rather than only what it has done.
func (c SweepClaim) NextDue(every time.Duration) time.Time {
	due := c.FiredAt.Add(every)
	if c.Every == every && c.CadenceAt.After(due) {
		due = c.CadenceAt
	}
	return due
}

// Validate reports every contract violation in the claim at once.
func (c SweepClaim) Validate() error {
	var problems []error
	if c.SchemaVersion != SweepSchemaVersion {
		problems = append(problems, fmt.Errorf("sweep schema version %d is not supported", c.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(c.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if err := domain.ValidateIdentifier("recurring task name", c.Task); err != nil {
		problems = append(problems, err)
	}
	if c.FiredAt.IsZero() {
		problems = append(problems, errors.New("fired at is required"))
	}
	if c.Firings < 1 {
		problems = append(problems, fmt.Errorf("firings is %d, and a recorded claim is at least one firing", c.Firings))
	}
	if c.UpdatedAt.IsZero() {
		problems = append(problems, errors.New("updated at is required"))
	}
	if c.Every < 0 || (c.Every > 0 && c.CadenceAt.IsZero()) || (c.Every == 0 && !c.CadenceAt.IsZero()) {
		problems = append(problems, errors.New("a recorded cadence requires a positive interval and its adoption time together"))
	}
	if len(c.Problem) > MaxSweepTextBytes {
		problems = append(problems, fmt.Errorf("problem is %d bytes, limit is %d", len(c.Problem), MaxSweepTextBytes))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid recurring task claim: %w", err)
	}
	return nil
}

// Sweep is one firing's durable report: what the harness woke, what it cost, and
// the account the role gave of the pass.
//
// It is written once and never revised, which is why the account is stored as
// the role gave it rather than as a set of columns somebody would later want to
// correct. What the harness knows — which task, which role, which conversation,
// how many turns, what it cost — is the harness's and is never the role's to
// assert.
//
// The product's maintenance pass records its passes here too, under
// config.MaintenanceTaskName, because it is a pass on a cadence that nobody
// watches and the question asked of it — did it run, and if it skipped a step,
// why — is the question this log exists to answer. It wakes no role, so it
// names none, and what it carries instead is each step it took with what became
// of it.
type Sweep struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	Task          string           `json:"task"`
	// Role is who the pass woke. It is empty on the harness's own maintenance
	// pass, which wakes nobody and carries Steps instead.
	Role domain.AgentRole `json:"role,omitempty"`
	// Agent distinguishes program manager instances; empty wakes the role's
	// configured agent, as ordinary recurring tasks do.
	Agent string `json:"agent,omitempty"`
	// ConversationID is the role's own durable conversation the pass happened in,
	// so what was actually said can be read from the conversation record rather
	// than only from this.
	ConversationID string `json:"conversation_id,omitempty"`
	// ReportRetried records the one request for a missing closing block.
	ReportRetried bool `json:"report_retried,omitempty"`
	// MissingReport marks a pass that still lacked its block after that request.
	MissingReport           bool                          `json:"missing_report,omitempty"`
	ConversationReplacement *SweepConversationReplacement `json:"conversation_replacement,omitempty"`
	StartedAt               time.Time                     `json:"started_at"`
	EndedAt                 time.Time                     `json:"ended_at"`
	// Turns is how many answered turns the pass took, including its one request
	// for a missing closing report, and CostUSD what the provider charged. The
	// work-turn bound excludes that request; Problem names a bound that ended work.
	Turns   int     `json:"turns"`
	CostUSD float64 `json:"cost_usd,omitempty"`
	// Model is the model the pass's turns ran on, as the provider served them:
	// the task's own where it names one, the role's configured model where it
	// does not, and the alternate where a failover answered instead. It is what
	// the pass's cost is attributed to, as a run's record names the model its
	// developer ran on. It is absent on a pass that took no turn, and on every
	// record written before passes named one.
	Model string `json:"model,omitempty"`
	// Effort is the effort level the pass's turns asked the provider for: the
	// role's agent's, whichever model served them. It is absent on a pass that
	// took no turn, on one whose agent configured none, and on every record
	// written before passes named one.
	Effort string `json:"effort,omitempty"`
	// ResolvedEffort is provider-reported; EffortReported is false when not reported.
	ResolvedEffort    string `json:"resolved_effort,omitempty"`
	EffortDescription string `json:"effort_description,omitempty"`
	EffortReported    bool   `json:"effort_reported"`
	// Result is the account the role gave, merged across the turns of this
	// firing. It is absent where the pass produced none — a turn that failed, or
	// one that answered in prose without the block — and Problem then says why.
	// A development manager pass may instead carry the harness's forge findings,
	// including when its conversation wait missed the role's first turn.
	Result *sweep.Result `json:"result,omitempty"`
	// Problem is what went wrong with the firing: the turn that failed, or the
	// account that could not be read. A sweep record carrying one is a pass that
	// spent a turn and told nobody anything, which must never be indistinguishable
	// from a quiet pass that found nothing.
	Problem string `json:"problem,omitempty"`
	// PullRequests are the open pull requests the harness itself noticed on this
	// pass, each also stated as a finding in the account. They are kept beside
	// the account as the harness's own record for one reason: a request is
	// reported once, and this is the key a later pass reads to know which ones
	// already were. The findings say it in words; this says it in numbers.
	PullRequests []ForgeNotice `json:"pull_requests,omitempty"`
	// Summoned is what fired this pass out of its cadence, where something did:
	// the intake brake, naming what tripped it. It is absent on a pass the
	// cadence fired, which is nearly every pass, and it is on the record so a
	// reader of the log can tell a summoned pass from the hourly one beside it.
	Summoned string `json:"summoned,omitempty"`
	// Criticals are the critical reports this pass was fired to deliver, by
	// identifier, where a critical report is what fired it. They are the key a
	// later pull reads to know which criticals have already been put in front of
	// the Lead Product Manager as a turn of their own, so each is delivered that
	// way once; what keeps one in front of her after that is the pass refusing to
	// end complete while it stands unhandled. Absent on every other pass.
	Criticals []string `json:"criticals,omitempty"`
	// NotStarted is why the firing failed before its first turn was put to the
	// provider, where it did: the harness refused its own message, could not
	// open the role's conversation, or could not assemble what the turn would
	// have carried. Such a firing is a failed firing rather than a partial pass —
	// nothing was asked, nothing was spent, and the next firing fails the same way
	// until somebody changes something — and a run of them is what the attention
	// line reads to say so. It is absent on every firing that took a turn, and on
	// one the provider refused, which is the provider's own wait rather than this.
	NotStarted PreTurnCause `json:"not_started,omitempty"`
	// Events is how many events of each class a program manager instance's pass
	// was handed — landings, admissions, stoppages — counted from past its
	// cursor to the moment the pass was taken. It is absent on every pass that
	// was handed none, which is every recurring task's and a scheduled pass over
	// a quiet lane, and it is on the record so a burst that woke an instance once
	// reads as one pass carrying the burst rather than as a pass nobody can
	// account for.
	Events map[string]int `json:"events,omitempty"`
	// Missed marks a record that is a missed pass rather than a pass: a trigger
	// fired and no pass followed it, or a pass was taken and cancelled before it
	// completed, or its wait behind a held conversation ended before its turn.
	// It names which trigger and how it was missed, and Problem says
	// the cause where one is known. It is absent on every pass that completed or
	// failed on its own terms, and on the misses recorded before it existed,
	// which are read by their shape instead.
	// Independent forge findings do not complete the role's missed pass.
	Missed *MissedPass `json:"missed,omitempty"`
	// Failed marks a pass a turn of which failed or omitted its account: it did
	// not complete, so a program manager instance's cursor was not moved and the
	// next pass carries the same events. It is absent on a pass whose turns were
	// answered with their accounts, and on every record written before it existed.
	Failed bool `json:"failed,omitempty"`
	// Saved is every memory and lane-report write the pass's turns made that
	// its store recorded, in the order they were made — on a pass that failed as
	// much as on one that completed, because a write is kept whatever happens to
	// the pass after it. The next pass after one that did not complete is told
	// these, so it does not write them again.
	Saved []SavedWrite `json:"saved,omitempty"`
	// ReportsFiled is how many reports the pass's turns filed that the pile
	// kept, and Admitted the work items they admitted to the tracker, by
	// identifier. With Saved they are the four traces a pass can leave outside
	// its own account: a memory written, a lane report changed, a report filed,
	// and work admitted. Each is absent on a pass that made none of that kind.
	ReportsFiled int      `json:"reports_filed,omitempty"`
	Admitted     []string `json:"admitted,omitempty"`
	// Untraced marks a pass whose account reported findings of the role's own
	// and whose turns left none of those traces. A finding that lives only in
	// the account and the conversation is lost to the role at the conversation's
	// next compaction, so the record says so, the role's next pass is told which
	// findings they were, and the attention line carries it with the role as the
	// one to move. It is absent on every other pass, and on every record written
	// before it existed.
	Untraced bool `json:"untraced,omitempty"`
	// Wording is the read model's findings about what this pass wrote for a person.
	// The next pass is told these corrections; Result and the lane report stay intact.
	Wording []terms.Finding `json:"wording,omitempty"`
	// Docket records what this pass delivered and what the next pass must put
	// first. It is absent on older passes and on roles that read no docket.
	Docket *DocketDelivery `json:"docket,omitempty"`
	// Steps is what the harness's own maintenance pass did, one entry per step in
	// the order it took them, each saying whether it ran, was skipped, or failed,
	// and why. A step that was skipped says so rather than being left out,
	// because a pass that quietly did less than it was meant to is the failure
	// this record exists to make visible. It is empty on a role's pass.
	Steps []SweepStep `json:"steps,omitempty"`
}

// SweepConversationReplacement records a new conversation opened after repeated
// missing reports. The old conversation and its events remain in their store.
type SweepConversationReplacement struct {
	Previous string `json:"previous"`
	Reason   string `json:"reason"`
}

// LeftATrace reports a pass whose turns left at least one trace outside its
// own account: a memory write or lane report version it saved, a report it
// filed, or work it admitted.
func (s Sweep) LeftATrace() bool {
	return len(s.Saved) > 0 || s.ReportsFiled > 0 || len(s.Admitted) > 0
}

// MaxSweepSteps bounds how many steps one harness pass records. The pass has a
// handful, and this is well above them.
const MaxSweepSteps = 32

// SweepStepOutcome is what became of one step of a harness pass.
type SweepStepOutcome string

const (
	// StepRan is a step that was carried out, whatever it found.
	StepRan SweepStepOutcome = "ran"
	// StepSkipped is a step the pass deliberately did not take, with the reason
	// in the detail: the provider is not answering, nothing changed, the part is
	// not one this supervisor hosts.
	StepSkipped SweepStepOutcome = "skipped"
	// StepFailed is a step that was taken and did not succeed.
	StepFailed SweepStepOutcome = "failed"
)

// Valid reports whether an outcome is one of the vocabulary's.
func (o SweepStepOutcome) Valid() bool {
	switch o {
	case StepRan, StepSkipped, StepFailed:
		return true
	}
	return false
}

// SweepStep is one step of a harness pass and what became of it.
type SweepStep struct {
	Name    string           `json:"name"`
	Outcome SweepStepOutcome `json:"outcome"`
	// Detail is what the step did, or why it was skipped, or how it failed.
	Detail string `json:"detail,omitempty"`
}

// Validate refuses a step with no name, an outcome this harness does not name,
// or a skip or failure with no reason.
func (s SweepStep) Validate() error {
	var problems []error
	if err := domain.ValidateIdentifier("step name", s.Name); err != nil {
		problems = append(problems, err)
	}
	if !s.Outcome.Valid() {
		problems = append(problems, fmt.Errorf("step outcome %q must be %q, %q, or %q", s.Outcome, StepRan, StepSkipped, StepFailed))
	}
	if s.Outcome != StepRan && strings.TrimSpace(s.Detail) == "" {
		problems = append(problems, fmt.Errorf("a step that was %s says why", s.Outcome))
	}
	if len(s.Detail) > MaxSweepTextBytes {
		problems = append(problems, fmt.Errorf("step detail is %d bytes, limit is %d", len(s.Detail), MaxSweepTextBytes))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid step: %w", err)
	}
	return nil
}

// HarnessPass reports a record the harness wrote about a pass of its own rather
// than about a role it woke.
func (s Sweep) HarnessPass() bool {
	return s.Role == "" && len(s.Steps) > 0
}

// Unfinished reports a record of this task's that did not end the pass owed:
// one whose turn failed, or a firing that took no turn at all — refused before
// it, waited out on the provider, missed, or cancelled. The next pass is still
// owed what this one was, so what it saved is what that pass is told of.
func (s Sweep) Unfinished() bool {
	return s.Failed || s.Turns == 0
}

// PassTrigger is what fires a pass: the cadence, the events a program manager
// instance watches, or the intake brake's summons.
type PassTrigger string

const (
	PassTriggerSchedule PassTrigger = "schedule"
	PassTriggerEvents   PassTrigger = "events"
	PassTriggerSummons  PassTrigger = "summons"
)

// Valid reports whether a trigger is one of the vocabulary's.
func (t PassTrigger) Valid() bool {
	switch t {
	case PassTriggerSchedule, PassTriggerEvents, PassTriggerSummons:
		return true
	}
	return false
}

// Describe is the trigger in the words a sentence about a pass uses.
func (t PassTrigger) Describe() string {
	switch t {
	case PassTriggerSchedule:
		return "scheduled pass"
	case PassTriggerEvents:
		return "pass its events woke"
	case PassTriggerSummons:
		return "summoned pass"
	default:
		return "pass"
	}
}

// MissKind is how a pass was missed.
type MissKind string

const (
	// MissUnfired is a trigger that fired with no pass following it for a whole
	// interval.
	MissUnfired MissKind = "unfired"
	// MissCancelled is a pass that was taken and stopped before it completed:
	// its process cancelled it, or died carrying it and recorded nothing.
	MissCancelled MissKind = "cancelled"
	// MissConversationHeld is a due pass whose bounded wait behind a turn ended
	// before it could ask the role anything.
	MissConversationHeld MissKind = "conversation-held"
)

// Valid reports whether a kind is one of the vocabulary's.
func (k MissKind) Valid() bool {
	return k == MissUnfired || k == MissCancelled || k == MissConversationHeld
}

// MissedPass is which trigger a missed pass was owed by, and how it was missed.
type MissedPass struct {
	Trigger PassTrigger `json:"trigger"`
	How     MissKind    `json:"how"`
}

// IsMiss reports a record marked as a missed pass. A miss recorded before the
// mark existed is not one of these: its shape — no turn, no account, starting
// when the task fell due — is shared with a firing the provider refused, so
// nothing reading the log can tell it apart, which is why the mark was added.
func (s Sweep) IsMiss() bool {
	return s.Missed != nil
}

// PreTurnCause is why a recurring task's firing failed before its first turn.
// The set is closed, because each cause is somebody's move and a cause nobody
// named is one no surface can say whose.
type PreTurnCause string

const (
	// PreTurnMessageRefused is the harness refusing the message it composed for
	// the pass, before sending it: on 2026-09-26 every development manager sweep
	// was refused as "operator message is 47768 bytes, limit is 32768", six times
	// in a row.
	PreTurnMessageRefused PreTurnCause = "message-refused"
	// PreTurnConversationUnopened is the role's conversation not opening at all:
	// no agent fills the role, its record will not load, or another process holds
	// it.
	PreTurnConversationUnopened PreTurnCause = "conversation-unopened"
	// PreTurnContextUnassembled is the turn's input refusing to assemble: what
	// the turn would carry is past the bound on one turn, or the picture it rests
	// on could not be measured.
	PreTurnContextUnassembled PreTurnCause = "context-unassembled"
)

// PreTurnCauses is the whole vocabulary.
func PreTurnCauses() []PreTurnCause {
	return []PreTurnCause{PreTurnMessageRefused, PreTurnConversationUnopened, PreTurnContextUnassembled}
}

// Valid reports whether a cause is one of the vocabulary's.
func (c PreTurnCause) Valid() bool {
	for _, known := range PreTurnCauses() {
		if c == known {
			return true
		}
	}
	return false
}

// Describe is the cause in the words a sentence about the firing uses.
func (c PreTurnCause) Describe() string {
	switch c {
	case PreTurnMessageRefused:
		return "the harness refused the message it composed for the pass"
	case PreTurnConversationUnopened:
		return "the role's conversation could not be opened"
	case PreTurnContextUnassembled:
		return "what the turn would carry could not be assembled"
	default:
		return "it failed before its first turn for a reason the record does not name"
	}
}

// ForgeNotice is one open pull request the harness noticed a reason to report:
// the work it was opened for is closed, or its branch is already carried by the
// branch it targets. Either is a request the forge holds open for nothing, and
// noticing is the whole of what the harness does about it — closing one is a
// decision, and this records none.
type ForgeNotice struct {
	Number int    `json:"number"`
	URL    string `json:"url,omitempty"`
	// HeadBranch and BaseBranch are the request's own, as the forge names them.
	HeadBranch string `json:"head_branch"`
	BaseBranch string `json:"base_branch,omitempty"`
	// WorkItemID is the work the request was opened for, where the harness could
	// say: from its own record of the run that opened it, or failing that from
	// the branch's name. It is empty for a request nothing here opened.
	WorkItemID string `json:"work_item_id,omitempty"`
	// ItemClosed and Contained are the two conditions, and a notice carries at
	// least one of them. A request can meet both.
	ItemClosed bool `json:"item_closed,omitempty"`
	Contained  bool `json:"contained,omitempty"`
}

// Validate refuses a notice that reports no request or no reason.
func (n ForgeNotice) Validate() error {
	var problems []error
	if n.Number <= 0 {
		problems = append(problems, errors.New("pull request number must be positive"))
	}
	if strings.TrimSpace(n.HeadBranch) == "" {
		problems = append(problems, errors.New("head branch is required"))
	}
	if !n.ItemClosed && !n.Contained {
		problems = append(problems, errors.New("a noticed pull request names at least one of its two conditions"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid pull request notice: %w", err)
	}
	return nil
}

// Finding is the notice as the account states it: which request, which item,
// and which of the two conditions holds. Its disposition is "left", because
// noticing is all the pass did about it — closing an open request is
// yoyodyne-ifd.69's — and the detail says so, so a reader does not go looking
// for a fix that was never made.
func (n ForgeNotice) Finding() sweep.Finding {
	request := fmt.Sprintf("pull request #%d", n.Number)
	if strings.TrimSpace(n.URL) != "" {
		request += " (" + strings.TrimSpace(n.URL) + ")"
	}
	item := "no work item the harness can name"
	if strings.TrimSpace(n.WorkItemID) != "" {
		item = "work item " + strings.TrimSpace(n.WorkItemID)
	}
	var conditions []string
	if n.ItemClosed {
		conditions = append(conditions, "its "+item+" is closed")
	}
	if n.Contained {
		base := n.BaseBranch
		if strings.TrimSpace(base) == "" {
			base = "its target branch"
		}
		conditions = append(conditions, fmt.Sprintf("its head branch %s is already contained in %s", n.HeadBranch, base))
	}
	issue := request + " is open and " + strings.Join(conditions, ", and ")
	if !n.ItemClosed {
		issue += " (" + item + ")"
	}
	return sweep.Finding{
		Issue:       issue,
		Disposition: sweep.DispositionLeft,
		Detail:      "noticed by the harness's own reading of the forge on this pass; it closes nothing, and the request is reported this once",
	}
}

// FoundNothing reports the quiet pass: an account that was given and carried no
// findings. It is the ordinary result on a healthy harness, and it is a
// different fact from a pass that produced no account at all.
func (s Sweep) FoundNothing() bool {
	return s.Result != nil && len(s.Result.Findings) == 0
}

// Validate reports every contract violation in the record at once.
func (s Sweep) Validate() error {
	var problems []error
	if s.Docket != nil {
		if s.Role != domain.RoleDevelopmentManager {
			problems = append(problems, errors.New("only a development manager's pass carries docket delivery"))
		}
		if err := s.Docket.Validate(); err != nil {
			problems = append(problems, err)
		}
	}
	if s.SchemaVersion != SweepSchemaVersion {
		problems = append(problems, fmt.Errorf("sweep schema version %d is not supported", s.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(s.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if err := domain.ValidateIdentifier("recurring task name", s.Task); err != nil {
		problems = append(problems, err)
	}
	if s.Agent != "" {
		if err := domain.ValidateIdentifier("recurring pass agent", s.Agent); err != nil {
			problems = append(problems, err)
		}
	}
	// A role's pass names a role this harness has; the harness's own pass names
	// none and carries its steps instead, and a record that does neither says
	// nothing about who or what the pass was.
	switch {
	case s.Role != "" && !s.Role.Valid():
		problems = append(problems, fmt.Errorf("role %q is not one this harness has", s.Role))
	case s.Role == "" && len(s.Steps) == 0:
		problems = append(problems, errors.New("a sweep names the role it woke, or carries the steps the harness took"))
	case s.Role != "" && len(s.Steps) > 0:
		problems = append(problems, errors.New("a sweep of a role's pass carries no harness steps"))
	}
	if len(s.Steps) > MaxSweepSteps {
		problems = append(problems, fmt.Errorf("%d steps in one pass, limit is %d", len(s.Steps), MaxSweepSteps))
	}
	for i, step := range s.Steps {
		if err := step.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("steps[%d]: %w", i, err))
		}
	}
	if s.StartedAt.IsZero() {
		problems = append(problems, errors.New("started at is required"))
	}
	if s.EndedAt.IsZero() {
		problems = append(problems, errors.New("ended at is required"))
	}
	if s.Turns < 0 {
		problems = append(problems, fmt.Errorf("turns is %d, and a pass cannot take a negative number of them", s.Turns))
	}
	if s.MissingReport && !s.Failed {
		problems = append(problems, errors.New("a pass missing its closing report must be failed"))
	}
	if r := s.ConversationReplacement; r != nil {
		if strings.TrimSpace(r.Previous) == "" || strings.TrimSpace(r.Reason) == "" || s.ConversationID == "" || r.Previous == s.ConversationID || len(r.Reason) > MaxSweepTextBytes {
			problems = append(problems, errors.New("a conversation replacement must name distinct previous and current conversations and a bounded reason"))
		}
	}
	// A pass that produced neither an account nor a problem would be a firing the
	// record can say nothing at all about, which is the one thing this must not be
	// able to hold: a reader would see a sweep that happened and no way to tell
	// whether it found nothing or failed.
	if s.Result == nil && strings.TrimSpace(s.Problem) == "" {
		problems = append(problems, errors.New("a sweep with no result must say what stopped it"))
	}
	if s.Result != nil {
		if err := s.Result.Validate(); err != nil {
			problems = append(problems, err)
		}
	}
	if len(s.Problem) > MaxSweepTextBytes {
		problems = append(problems, fmt.Errorf("problem is %d bytes, limit is %d", len(s.Problem), MaxSweepTextBytes))
	}
	if len(s.Summoned) > MaxSweepTextBytes {
		problems = append(problems, fmt.Errorf("summoned is %d bytes, limit is %d", len(s.Summoned), MaxSweepTextBytes))
	}
	classes := make([]string, 0, len(s.Events))
	for class := range s.Events {
		classes = append(classes, class)
	}
	sort.Strings(classes)
	for _, class := range classes {
		count := s.Events[class]
		if err := domain.ValidateIdentifier("event class", class); err != nil {
			problems = append(problems, err)
		}
		if count < 1 {
			problems = append(problems, fmt.Errorf("events of class %q is %d, and a class is recorded only when the pass carried one", class, count))
		}
	}
	if len(s.Criticals) > MaxSweepCriticals {
		problems = append(problems, fmt.Errorf("%d critical reports delivered by one pass, limit is %d", len(s.Criticals), MaxSweepCriticals))
	}
	for i, id := range s.Criticals {
		if strings.TrimSpace(id) == "" || len(id) > MaxSweepModelBytes {
			problems = append(problems, fmt.Errorf("criticals[%d] must name a report", i))
		}
	}
	if len(s.Model) > MaxSweepModelBytes {
		problems = append(problems, fmt.Errorf("model is %d bytes, limit is %d", len(s.Model), MaxSweepModelBytes))
	}
	if len(s.Effort) > MaxSweepModelBytes {
		problems = append(problems, fmt.Errorf("effort is %d bytes, limit is %d", len(s.Effort), MaxSweepModelBytes))
	}
	if s.NotStarted != "" {
		if !s.NotStarted.Valid() {
			problems = append(problems, fmt.Errorf("not started %q is not one of %v", s.NotStarted, PreTurnCauses()))
		}
		// A firing that took a turn started, whatever went wrong after it.
		if s.Turns != 0 {
			problems = append(problems, fmt.Errorf("a firing that failed before its first turn took %d turn(s)", s.Turns))
		}
		if strings.TrimSpace(s.Problem) == "" {
			problems = append(problems, errors.New("a firing that failed before its first turn must say what stopped it"))
		}
	}
	if s.Missed != nil {
		if !s.Missed.Trigger.Valid() {
			problems = append(problems, fmt.Errorf("missed trigger %q is not one this harness has", s.Missed.Trigger))
		}
		if !s.Missed.How.Valid() {
			problems = append(problems, fmt.Errorf("missed how %q is not one this harness has", s.Missed.How))
		}
		// The role's account completes its pass. An independent reading of the
		// forge does not: the held conversation still owes its first turn.
		if s.Result != nil && (s.Missed.How != MissConversationHeld || !s.forgeOnlyAccount()) {
			problems = append(problems, errors.New("a missed pass carries no account except the harness's forge findings on a conversation-held miss"))
		}
	}
	// A noticed request is stated as a finding, so a record naming requests and
	// carrying no account would be one whose findings are nowhere to be read.
	if len(s.PullRequests) > 0 && s.Result == nil {
		problems = append(problems, errors.New("a sweep that noticed pull requests carries the account that states them"))
	}
	if len(s.PullRequests) > sweep.MaxPassFindings {
		problems = append(problems, fmt.Errorf("%d pull requests noticed in one pass, limit is %d", len(s.PullRequests), sweep.MaxPassFindings))
	}
	for i, noticed := range s.PullRequests {
		if err := noticed.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("pull_requests[%d]: %w", i, err))
		}
	}
	if len(s.Saved) > MaxSweepSavedWrites {
		problems = append(problems, fmt.Errorf("%d saved writes in one pass, limit is %d", len(s.Saved), MaxSweepSavedWrites))
	}
	for i, saved := range s.Saved {
		if err := saved.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("saved[%d]: %w", i, err))
		}
	}
	if s.ReportsFiled < 0 {
		problems = append(problems, fmt.Errorf("reports filed is %d, and a pass cannot file a negative number of them", s.ReportsFiled))
	}
	if len(s.Admitted) > MaxSweepSavedWrites {
		problems = append(problems, fmt.Errorf("%d admitted items in one pass, limit is %d", len(s.Admitted), MaxSweepSavedWrites))
	}
	for i, id := range s.Admitted {
		if strings.TrimSpace(id) == "" || len(id) > MaxSweepModelBytes {
			problems = append(problems, fmt.Errorf("admitted[%d] must name a work item", i))
		}
	}
	// An untraced pass is one that left nothing, so a record claiming both is
	// one whose flag nobody could act on.
	if s.Untraced && s.LeftATrace() {
		problems = append(problems, errors.New("a pass marked untraced left a trace"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid sweep: %w", err)
	}
	return nil
}

// forgeOnlyAccount recognizes the independent findings a pass may record
// without reaching the development manager. No role findings travel with them.
func (s Sweep) forgeOnlyAccount() bool {
	if s.Role != domain.RoleDevelopmentManager || s.Turns != 0 || s.Result == nil ||
		s.Result.Status != sweep.StatusComplete || len(s.PullRequests) == 0 ||
		len(s.Result.Findings) != len(s.PullRequests) || len(s.Result.Questions) != 0 || len(s.Result.Recommendations) != 0 {
		return false
	}
	for i, notice := range s.PullRequests {
		finding, want := s.Result.Findings[i], notice.Finding()
		if finding.Issue != want.Issue || finding.Detail != want.Detail || finding.Disposition != want.Disposition || len(finding.Filed) != 0 {
			return false
		}
	}
	return true
}

// ErrSweepNotDue is what a firing claimed before its interval has passed unwraps
// to, so a caller can tell "this task is not due" from a store that could not be
// read without matching on the words of either. It is the ordinary answer on
// almost every pull, which is why it is a typed refusal rather than a failure.
var ErrSweepNotDue = errors.New("this recurring task is not due to fire yet")

// SweepNotDueError names the claim that refused and when the next firing is due,
// so a caller that meets it can say which task it was.
type SweepNotDueError struct {
	Existing SweepClaim
	NextDue  time.Time
}

func (e SweepNotDueError) Error() string {
	return fmt.Sprintf("the recurring task %s last fired at %s, and the next firing is not due until %s",
		e.Existing.Task,
		e.Existing.FiredAt.UTC().Format(time.RFC3339),
		e.NextDue.UTC().Format(time.RFC3339))
}

func (e SweepNotDueError) Unwrap() error { return ErrSweepNotDue }

// SweepStore is where the recurring tasks' cadence and their reports live: one
// directory under the product, beside the escalations, with one claim file per
// task and one append-only log of what the firings produced.
type SweepStore struct {
	root      string
	productID domain.ProductID
}

func NewSweepStore(root string, productID domain.ProductID) (*SweepStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &SweepStore{
		root:      filepath.Join(filepath.Clean(root), "products", string(productID), "sweeps"),
		productID: productID,
	}, nil
}

// Sweeps is the recurring-task record for this run store's product, reached from
// here for the reason the escalations are: whoever can read what became of an
// item's runs can read what the harness has been doing on a cadence, without
// being told the state root a second time.
func (s *Store) Sweeps() *SweepStore {
	return &SweepStore{
		root:      filepath.Join(filepath.Dir(s.root), "sweeps"),
		productID: s.productID,
	}
}

func (s *SweepStore) Root() string { return s.root }

// Path names the log itself, so a failure can say where the recorded sweeps
// actually are.
func (s *SweepStore) Path() string { return filepath.Join(s.root, "sweeps.jsonl") }

// Adopt records when a running harness first reads a changed cadence. It does
// not claim a firing or settle one in flight. Reading the same cadence after a
// restart retains the original adoption time.
func (s *SweepStore) Adopt(ctx context.Context, task string, every time.Duration, at time.Time) (SweepClaim, bool, error) {
	if err := domain.ValidateIdentifier("recurring task name", task); err != nil {
		return SweepClaim{}, false, err
	}
	if every <= 0 || at.IsZero() {
		return SweepClaim{}, false, errors.New("adopting a cadence requires its interval and observation time")
	}
	release, err := s.lock(ctx, task)
	if err != nil {
		return SweepClaim{}, false, err
	}
	defer release()
	claim, found, err := s.load(task)
	if err != nil || !found || claim.Every == every {
		return claim, found, err
	}
	claim.CadenceUncertain = claim.Every == 0
	claim.Every, claim.CadenceAt = every, at.UTC()
	return claim, true, s.save(task, claim)
}

// Claim records that a task is firing now, and refuses one whose interval has
// not passed. It is written before the turns are taken, which is what makes the
// refusal mean anything: a claim recorded afterwards would leave the window
// where two sessions fire the same task at once wide open.
func (s *SweepStore) Claim(ctx context.Context, task string, every time.Duration, now time.Time) (SweepClaim, error) {
	name := strings.TrimSpace(task)
	if err := domain.ValidateIdentifier("recurring task name", name); err != nil {
		return SweepClaim{}, err
	}
	if every <= 0 {
		return SweepClaim{}, fmt.Errorf("the recurring task %s has no interval, so nothing can say when it is due", name)
	}
	at := now
	if at.IsZero() {
		at = time.Now()
	}
	at = at.UTC()

	release, err := s.lock(ctx, name)
	if err != nil {
		return SweepClaim{}, err
	}
	defer release()

	claimed, found, err := s.load(name)
	if err != nil {
		return SweepClaim{}, err
	}
	if found {
		// Refused here rather than only where the interval is read, because here is
		// where the lock is. A caller that checked the cadence itself and then
		// claimed would be two reads and a write with a window between them, which
		// is exactly the window two concurrent sessions land in.
		if !claimed.Due(every, at) {
			return SweepClaim{}, SweepNotDueError{Existing: claimed, NextDue: claimed.NextDue(every)}
		}
	} else {
		claimed = SweepClaim{Task: name}
	}
	claimed.SchemaVersion = SweepSchemaVersion
	claimed.ProductID = s.productID
	claimed.Task = name
	claimed.Firings++
	claimed.FiredAt = at
	claimed.UpdatedAt = at
	if claimed.Every != every {
		claimed.Every = every
		claimed.CadenceAt = at
	}
	claimed.CadenceUncertain = false
	// Cleared as the firing starts rather than as it ends, so a claim carrying a
	// problem is always the most recent firing's and never one left behind by a
	// firing two cadences ago.
	claimed.Problem = ""
	claimed.Summoned = false
	if err := claimed.Validate(); err != nil {
		return SweepClaim{}, err
	}
	if err := s.save(name, claimed); err != nil {
		return SweepClaim{}, err
	}
	return claimed, nil
}

// Summon records that a task is firing now whether or not its interval has
// passed. It is what the intake brake takes when it trips: the development
// manager's sweep at once rather than at her next scheduled pass. It is a
// firing like any other — counted, stamped, and settled the same way — so the
// cadence runs on from the summons, and a summoned pass is never followed a
// minute later by the scheduled one over the same ground.
func (s *SweepStore) Summon(ctx context.Context, task string, now time.Time) (SweepClaim, error) {
	name := strings.TrimSpace(task)
	if err := domain.ValidateIdentifier("recurring task name", name); err != nil {
		return SweepClaim{}, err
	}
	at := now
	if at.IsZero() {
		at = time.Now()
	}
	at = at.UTC()

	release, err := s.lock(ctx, name)
	if err != nil {
		return SweepClaim{}, err
	}
	defer release()

	claimed, found, err := s.load(name)
	if err != nil {
		return SweepClaim{}, err
	}
	if !found {
		claimed = SweepClaim{Task: name}
	}
	claimed.SchemaVersion = SweepSchemaVersion
	claimed.ProductID = s.productID
	claimed.Task = name
	claimed.Firings++
	claimed.FiredAt = at
	claimed.UpdatedAt = at
	claimed.Problem = ""
	claimed.Summoned = true
	if err := claimed.Validate(); err != nil {
		return SweepClaim{}, err
	}
	if err := s.save(name, claimed); err != nil {
		return SweepClaim{}, err
	}
	return claimed, nil
}

// Settle records what became of the firing that is claimed: nothing on one that
// produced a report, and what stopped it on one that did not. The clock is
// deliberately not moved back — see this file's opening on why a failed pass
// waits for its next cadence rather than being retried at once.
func (s *SweepStore) Settle(ctx context.Context, task, problem string) (SweepClaim, error) {
	name := strings.TrimSpace(task)
	if err := domain.ValidateIdentifier("recurring task name", name); err != nil {
		return SweepClaim{}, err
	}

	release, err := s.lock(ctx, name)
	if err != nil {
		return SweepClaim{}, err
	}
	defer release()

	settled, found, err := s.load(name)
	if err != nil {
		return SweepClaim{}, err
	}
	if !found {
		return SweepClaim{}, fmt.Errorf("no firing of the recurring task %s is recorded, so there is nothing to settle", name)
	}
	settled.Problem = boundedSweepText(problem)
	settled.UpdatedAt = time.Now().UTC()
	if err := s.save(name, settled); err != nil {
		return SweepClaim{}, err
	}
	return settled, nil
}

// Find reports one task's claim. A task that has never fired is the ordinary
// answer rather than a failure to look, and a claim that cannot be read is
// neither: it is an error, because a cadence nobody can read must never be fired
// through as though it were absent.
func (s *SweepStore) Find(task string) (SweepClaim, bool, error) {
	name := strings.TrimSpace(task)
	if err := domain.ValidateIdentifier("recurring task name", name); err != nil {
		return SweepClaim{}, false, err
	}
	return s.load(name)
}

// Append records one firing's report durably. It is an append rather than a
// rewrite for the reason the collected reports are: a sweep is written once and
// never revised, and two sessions finishing a pass at the same time must not
// overwrite each other's record.
func (s *SweepStore) Append(recorded Sweep) error {
	recorded.SchemaVersion = SweepSchemaVersion
	recorded.ProductID = s.productID
	if err := recorded.Validate(); err != nil {
		return err
	}
	encoded, err := json.Marshal(recorded)
	if err != nil {
		return fmt.Errorf("encode sweep: %w", err)
	}
	encoded = append(encoded, '\n')
	if len(encoded) > maxEncodedSweepBytes {
		return fmt.Errorf("encoded sweep is %d bytes, limit is %d", len(encoded), maxEncodedSweepBytes)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create sweep directory: %w", err)
	}
	path := s.Path()
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return fmt.Errorf("inspect the sweep log: %w", statErr)
	}
	// Opened for reading as well as appending, so what is already at the end of
	// the log can be looked at before adding to it. O_APPEND still decides where
	// the write lands.
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open the sweep log: %w", err)
	}
	// A record is written with one Write, and a Write of this size is not atomic:
	// a process killed partway through one leaves a fragment with no newline on
	// it. Appending straight onto that fragment would join it to this record and
	// make the two of them a single line nothing can decode — so the crash would
	// cost the report after it as well as the one it interrupted, and the one
	// after is the report nobody has seen yet.
	//
	// A newline first closes the fragment off, which bounds what a crash costs to
	// the record it actually interrupted. The reader names that line and carries
	// on, so what is lost is one pass rather than one pass and its successor.
	if err := closeOffATornFragment(file, &encoded); err != nil {
		file.Close()
		return err
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append sweep: %w", err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append sweep: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync the sweep log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close the sweep log: %w", err)
	}
	if created {
		return syncDirectory(s.root)
	}
	return nil
}

// UnreadableSweep is one line of the log that would not decode, and why.
//
// It exists because of what this log is: appended to once per firing, never
// rewritten, and read from one surface. A write of a record this size is not
// atomic, so a process killed partway through one leaves a torn line — and a
// reader that failed the whole listing on the first line it could not decode
// would make one interrupted write cost every report before it, permanently, on
// the only surface those reports are read from. So a line that will not decode is
// set aside and named rather than fatal, and the reports around it stay
// reachable. Named rather than skipped, because a listing that quietly dropped
// records would be a worse answer than the failure it replaced.
type UnreadableSweep struct {
	// Line is the 1-based line of the log, so somebody can go and look at it.
	Line    int    `json:"line"`
	Problem string `json:"problem"`
}

// closeOffATornFragment puts a newline in front of the record about to be
// appended where the log does not already end in one, so a fragment a crash left
// behind is terminated rather than joined to.
//
// Reading the last byte is racy against another process appending at the same
// moment, and benignly so: every complete append ends in a newline, so the only
// way the last byte is not one is that whoever wrote it died partway through.
// A concurrent healthy writer therefore cannot make this add a newline that was
// not wanted, and the blank line an unnecessary one would leave is skipped by
// the reader anyway.
func closeOffATornFragment(file *os.File, encoded *[]byte) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect the sweep log: %w", err)
	}
	if info.Size() == 0 {
		return nil
	}
	last := make([]byte, 1)
	if _, err := file.ReadAt(last, info.Size()-1); err != nil {
		return fmt.Errorf("read the end of the sweep log: %w", err)
	}
	if last[0] != '\n' {
		*encoded = append([]byte{'\n'}, *encoded...)
	}
	return nil
}

// List returns every recorded sweep in the order it was written, and beside them
// the lines that would not decode. A log that does not exist yet is a product
// nothing has swept, which is not a failure to read.
//
// The error is for a log that could not be read at all. A line that will not
// decode is not one: it comes back in the second return value, and the sweeps
// around it come back with it — see UnreadableSweep for why that is the
// direction this fails in.
//
// A line carrying a field this build does not know is not a line that will not
// decode. It is a record a newer build wrote, and it is read without that field
// rather than set aside: this is the tolerant door, and the sweep log is only
// ever listed, never written back from a listing. See tolerantread.go.
func (s *SweepStore) List() ([]Sweep, []UnreadableSweep, error) {
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("open the sweep log: %w", err)
	}
	defer file.Close()

	var recorded []Sweep
	var unreadable []UnreadableSweep
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), maxEncodedSweepBytes)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var entry Sweep
		unknown, err := decodeTolerating([]byte(text), &entry)
		if err != nil {
			unreadable = append(unreadable, UnreadableSweep{Line: line, Problem: err.Error()})
			continue
		}
		noteUnknownFields("recurring task record", unknown)
		if entry.ProductID != s.productID {
			unreadable = append(unreadable, UnreadableSweep{
				Line:    line,
				Problem: fmt.Sprintf("this record belongs to product %q, not %q", entry.ProductID, s.productID),
			})
			continue
		}
		recorded = append(recorded, entry)
	}
	if err := scanner.Err(); err != nil {
		// The scan itself failing is different from a line that will not decode:
		// nothing after the failure was read at all, so what came before it is
		// returned with the failure rather than instead of it, and the caller says
		// the listing is partial.
		return recorded, unreadable, fmt.Errorf("read the sweep log: %w", err)
	}
	return recorded, unreadable, nil
}

// load is one task's claim as it sits on disk, and it is the strict door: a
// claim is read to decide whether the task is due and then written back with the
// firing counted on it, so a field stepped over on the way in is a field lost on
// the way out. The refusal is an error the caller reports; the cadence stops
// rather than quietly losing what a newer build recorded about it.
func (s *SweepStore) load(task string) (SweepClaim, bool, error) {
	path := s.path(task)
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return SweepClaim{}, false, nil
	}
	if err != nil {
		return SweepClaim{}, false, fmt.Errorf("open recurring task claim: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var claimed SweepClaim
	if err := decoder.Decode(&claimed); err != nil {
		return SweepClaim{}, false, fmt.Errorf("decode recurring task claim at %s: %w", path, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return SweepClaim{}, false, fmt.Errorf("decode recurring task claim at %s: %w", path, err)
	}
	if err := claimed.Validate(); err != nil {
		return SweepClaim{}, false, err
	}
	if claimed.ProductID != s.productID {
		return SweepClaim{}, false, fmt.Errorf("recurring task claim belongs to product %q, not %q", claimed.ProductID, s.productID)
	}
	if claimed.Task != task {
		return SweepClaim{}, false, fmt.Errorf("recurring task claim at %s belongs to task %q, not %q", path, claimed.Task, task)
	}
	return claimed, true, nil
}

// save replaces one task's claim durably, as a temporary file and a rename, so a
// process that dies mid-write leaves the previous claim rather than a truncated
// file nothing can read — which for a cadence would be a task that never fires
// again.
func (s *SweepStore) save(task string, claimed SweepClaim) error {
	if err := claimed.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create sweep directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".claim-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary recurring task claim: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary recurring task claim: %w", err)
	}
	if err := writeJSONFile(temporary, "recurring task claim", claimed); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary recurring task claim: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path(task)); err != nil {
		return fmt.Errorf("replace recurring task claim: %w", err)
	}
	return syncDirectory(s.root)
}

// lock serializes the read-modify-write on one task's claim across every
// Yoyodyne process, and the lock file outlives the write for the reason the
// escalations' does: removing it while another process held it would let a third
// take a lock on a file nobody else can see.
func (s *SweepStore) lock(ctx context.Context, task string) (func(), error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create sweep directory: %w", err)
	}
	file, err := os.OpenFile(s.path(task)+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open recurring task lock: %w", err)
	}
	if err := lockStateFile(ctx, file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock the recurring task %s: %w", task, err)
	}
	return func() { _ = releaseStateFile(file) }, nil
}

// path names one task's claim. A task name is a validated identifier — lowercase
// letters, digits, and single hyphens — so unlike a docket key it is already a
// file name, and the record is named for what an operator would look for.
func (s *SweepStore) path(task string) string {
	return filepath.Join(s.root, "claim-"+task+".json")
}

// boundedSweepText holds prose to what the record accepts, cut on a rune
// boundary and marked as cut, so a long problem is stored as shorter text rather
// than as broken text or as a whole account.
func boundedSweepText(text string) string {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) > MaxSweepTextBytes {
		const marker = " […]"
		cut := MaxSweepTextBytes - len(marker)
		for cut > 0 && !utf8.RuneStart(trimmed[cut]) {
			cut--
		}
		return strings.TrimSpace(trimmed[:cut]) + marker
	}
	return trimmed
}
