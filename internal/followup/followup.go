// Package followup is the follow-up record: one wait somebody left for
// somebody else, with who waits, who has to act, what it is about and what it
// blocks, one check from the closed list (check.go), two deadlines, where it
// came from, and whether it is still open.
//
// It follows docs/designs/follow-ups.md, "The decision" and "What ends a
// follow-up". The record is kept by runstate.FollowUpStore; the owner is
// resolved by the ownership registry (ownership.KindFollowUp), which returns
// the owner recorded here. A follow-up leaves the open state only when its
// check passes, or when its owner closes it, whoever waits withdraws it, or the
// operator closes it, each of the last three with a reason.
package followup

import (
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
)

// SchemaVersion is 1 and has never changed.
const SchemaVersion = 1

// MaxTextBytes bounds a reason or a check's result.
const MaxTextBytes = 4 << 10

// FollowUp is one follow-up record.
type FollowUp struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"id"`
	// Waits is who waits for the outcome and is told when it comes: a role, or
	// the operator.
	Waits ownership.Mover `json:"waits"`
	// Owner is who has to act for the check to pass, as the registry resolved
	// it when the follow-up was written. OwnerReason is why the owner is the
	// operator, from his closed list, and empty for anybody else.
	Owner       ownership.Mover          `json:"owner"`
	OwnerReason ownership.OperatorReason `json:"owner_reason,omitempty"`
	// About is what the follow-up concerns, by identifier.
	About []Subject `json:"about"`
	// Blocks is what it holds back.
	Blocks Blocks `json:"blocks"`
	// KeepsStopListed is, on a wait on a stopped run only, whether the stop
	// stays on the development manager's list while the follow-up is open.
	KeepsStopListed bool `json:"keeps_stop_listed,omitempty"`
	// Check is the one check whose passing ends it.
	Check Check `json:"check"`
	// BackToOwnerAt is when it goes back to its owner, and ToManagerAt when it
	// goes to the owner's manager.
	BackToOwnerAt time.Time `json:"back_to_owner_at"`
	ToManagerAt   time.Time `json:"to_manager_at"`
	Origin        Origin    `json:"origin"`
	CreatedAt     time.Time `json:"created_at"`
	State         State     `json:"state"`
	// Passed is set once the check passed, and Closed once somebody closed or
	// withdrew it; never both.
	Passed *Passed `json:"passed,omitempty"`
	Closed *Closed `json:"closed,omitempty"`
	// LastResult is what the check runner last found while the follow-up was
	// open. It is written only when the finding changes, so it says when the
	// current answer began rather than when the check last ran.
	LastResult *CheckResult `json:"last_result,omitempty"`
}

// CheckResult is one finding of the check runner: whether the check passed,
// or, where it could not be run at all, why not. A check that could not be run
// has not passed.
type CheckResult struct {
	At          time.Time `json:"at"`
	Passed      bool      `json:"passed,omitempty"`
	CouldNotRun string    `json:"could_not_run,omitempty"`
	// Detail is what the check found, in a sentence: the command's own words
	// where it ran one.
	Detail string `json:"detail,omitempty"`
}

// Same reports two findings that say the same thing, whenever each was made.
func (r CheckResult) Same(other CheckResult) bool {
	return r.Passed == other.Passed && r.CouldNotRun == other.CouldNotRun && r.Detail == other.Detail
}

// Says is the finding in ordinary words.
func (r CheckResult) Says() string {
	switch {
	case r.Passed:
		return joinSaid("it passed", r.Detail)
	case r.CouldNotRun != "":
		return "it could not be run, so it has not passed: " + r.CouldNotRun
	}
	return joinSaid("it has not passed", r.Detail)
}

func joinSaid(said, detail string) string {
	if detail = strings.TrimSpace(detail); detail != "" {
		return said + ": " + detail
	}
	return said
}

func (r CheckResult) validate() error {
	var problems []error
	if r.At.IsZero() {
		problems = append(problems, errors.New("a check's result records when it was found"))
	}
	if r.Passed && r.CouldNotRun != "" {
		problems = append(problems, errors.New("a check that could not be run has not passed"))
	}
	if err := validText("result", r.Detail, false); err != nil {
		problems = append(problems, err)
	}
	if err := validText("reason it could not be run", r.CouldNotRun, false); err != nil {
		problems = append(problems, err)
	}
	return errors.Join(problems...)
}

// Holds is every hold the follow-up is about, by identifier: the holds its
// check passing may release.
func (f FollowUp) Holds() []string {
	var holds []string
	for _, subject := range f.About {
		if subject.Kind == SubjectHold {
			holds = append(holds, subject.ID)
		}
	}
	return holds
}

// Record is the follow-up with a new finding of its check, and whether that
// finding differs from the last one recorded. A finding that says what the last
// one said changes nothing, so the record keeps the time the answer began. Only
// an open follow-up's check is run.
func (f FollowUp) Record(result CheckResult) (FollowUp, bool, error) {
	if f.State != StateOpen {
		return FollowUp{}, false, fmt.Errorf("%w: it is %s", ErrNotOpen, f.State)
	}
	if err := result.validate(); err != nil {
		return FollowUp{}, false, err
	}
	if f.LastResult != nil && f.LastResult.Same(result) {
		return f, false, nil
	}
	result.At = result.At.UTC()
	f.LastResult = &result
	return f, true, f.Validate()
}

// SubjectKind is what sort of record a follow-up is about.
type SubjectKind string

const (
	SubjectWorkItem       SubjectKind = "work-item"
	SubjectRun            SubjectKind = "run"
	SubjectStop           SubjectKind = "stop"
	SubjectReport         SubjectKind = "report"
	SubjectExchange       SubjectKind = "exchange"
	SubjectPullRequest    SubjectKind = "pull-request"
	SubjectDocument       SubjectKind = "document"
	SubjectHold           SubjectKind = "hold"
	SubjectProposedChange SubjectKind = "proposed-change"
	SubjectProvider       SubjectKind = "provider"
)

// SubjectKinds is every kind of record a follow-up can be about.
func SubjectKinds() []SubjectKind {
	return []SubjectKind{SubjectWorkItem, SubjectRun, SubjectStop, SubjectReport, SubjectExchange,
		SubjectPullRequest, SubjectDocument, SubjectHold, SubjectProposedChange, SubjectProvider}
}

func (k SubjectKind) Valid() bool {
	for _, known := range SubjectKinds() {
		if k == known {
			return true
		}
	}
	return false
}

// Subject is one record a follow-up is about.
type Subject struct {
	Kind SubjectKind `json:"kind"`
	ID   string      `json:"id"`
}

// Blocks is what a follow-up holds back: the items, and whether it holds
// intake or leaves every developer slot empty, either of which makes it
// priority 0 whatever it is about.
type Blocks struct {
	Items                     []string `json:"items,omitempty"`
	HoldsIntake               bool     `json:"holds_intake,omitempty"`
	EmptiesEveryDeveloperSlot bool     `json:"empties_every_developer_slot,omitempty"`
}

// OriginKind is who wrote a follow-up.
type OriginKind string

const (
	OriginRole      OriginKind = "role"
	OriginHarness   OriginKind = "harness"
	OriginOperator  OriginKind = "operator"
	OriginAssistant OriginKind = "operator-assistant"
)

// Origin is who wrote a follow-up: a role, which it names, the harness, the
// operator, or his assistant acting as him.
type Origin struct {
	Kind OriginKind       `json:"kind"`
	Role domain.AgentRole `json:"role,omitempty"`
}

// ByOperator reports a follow-up the operator or his assistant recorded, which
// is never the operator's to own.
func (o Origin) ByOperator() bool {
	return o.Kind == OriginOperator || o.Kind == OriginAssistant
}

func (o Origin) validate() error {
	switch o.Kind {
	case OriginRole:
		if !o.Role.Valid() {
			return fmt.Errorf("origin names role %q, which is not one of the harness's roles", o.Role)
		}
		return nil
	case OriginHarness, OriginOperator, OriginAssistant:
		if o.Role != "" {
			return fmt.Errorf("origin %q names no role, and this one names %q", o.Kind, o.Role)
		}
		return nil
	}
	return fmt.Errorf("origin %q is not a role, the harness, the operator, or his assistant", o.Kind)
}

// State is where a follow-up is in its life.
type State string

const (
	StateOpen   State = "open"
	StatePassed State = "passed"
	StateClosed State = "closed"
)

// Passed is the check passing: when, and what it found.
type Passed struct {
	At     time.Time `json:"at"`
	Result string    `json:"result,omitempty"`
}

// Closer is who ended a follow-up other than by its check passing.
type Closer string

const (
	// ClosedByOwner is its owner closing it, such as because it is no longer
	// needed.
	ClosedByOwner Closer = "owner"
	// ClosedByWaiter is whoever waits withdrawing it, because they no longer
	// need the outcome.
	ClosedByWaiter Closer = "waiter"
	// ClosedByOperator is the operator closing it, as an override.
	ClosedByOperator Closer = "operator"
)

// Closers is every way somebody can end a follow-up.
func Closers() []Closer {
	return []Closer{ClosedByOwner, ClosedByWaiter, ClosedByOperator}
}

func (c Closer) Valid() bool {
	for _, known := range Closers() {
		if c == known {
			return true
		}
	}
	return false
}

// Closed is somebody ending a follow-up: who, why, and when.
type Closed struct {
	By     Closer    `json:"by"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// idPattern is the identifier NewID issues. It is a plain name, so the store
// can use it as a file name and an identifier can never name a path.
var idPattern = regexp.MustCompile(`^follow-up-[a-z0-9]{16,32}$`)

// ValidID reports whether an identifier is one NewID could have issued.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// NewID is a fresh follow-up identifier.
func NewID() string {
	return "follow-up-" + strings.ToLower(rand.Text())
}

// Validate refuses a record that is not whole: every field the design names,
// an owner that names the operator only with a reason from his closed list, a
// check from the closed list, deadlines in order, and a state its fields agree
// with.
func (f FollowUp) Validate() error {
	var problems []error
	if f.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Errorf("follow-up schema version %d is not supported", f.SchemaVersion))
	}
	if !ValidID(f.ID) {
		problems = append(problems, fmt.Errorf("follow-up id %q is not one the store issues", f.ID))
	}
	if !f.Waits.IsOperator() && !domain.AgentRole(f.Waits).Valid() {
		problems = append(problems, fmt.Errorf("who waits is %q, and is a role or the operator", f.Waits))
	}
	problems = append(problems, f.validateOwner()...)
	if len(f.About) == 0 {
		problems = append(problems, errors.New("a follow-up says what it is about"))
	}
	aboutStop := false
	for _, subject := range f.About {
		if !subject.Kind.Valid() {
			problems = append(problems, fmt.Errorf("a follow-up cannot be about a %q", subject.Kind))
		}
		if err := validReference(subject.ID); err != nil {
			problems = append(problems, fmt.Errorf("what it is about: %w", err))
		}
		aboutStop = aboutStop || subject.Kind == SubjectStop
	}
	for _, item := range f.Blocks.Items {
		if err := validReference(item); err != nil {
			problems = append(problems, fmt.Errorf("an item it blocks: %w", err))
		}
	}
	if f.KeepsStopListed && !aboutStop {
		problems = append(problems, errors.New("only a wait on a stopped run keeps a stop listed, and this one is about no stop"))
	}
	if err := f.Check.Validate(); err != nil {
		problems = append(problems, err)
	}
	if f.CreatedAt.IsZero() {
		problems = append(problems, errors.New("a follow-up records when it was written"))
	}
	if f.BackToOwnerAt.IsZero() || f.ToManagerAt.IsZero() {
		problems = append(problems, errors.New("a follow-up has two deadlines"))
	} else {
		if f.BackToOwnerAt.Before(f.CreatedAt) {
			problems = append(problems, errors.New("its first deadline is before it was written"))
		}
		if !f.ToManagerAt.After(f.BackToOwnerAt) {
			problems = append(problems, errors.New("its second deadline, to the owner's manager, is not after its first, back to its owner"))
		}
	}
	if err := f.Origin.validate(); err != nil {
		problems = append(problems, err)
	}
	if f.LastResult != nil {
		if err := f.LastResult.validate(); err != nil {
			problems = append(problems, err)
		}
	}
	problems = append(problems, f.validateState()...)
	return errors.Join(problems...)
}

func (f FollowUp) validateOwner() []error {
	if f.Owner.IsOperator() {
		switch {
		case f.Origin.ByOperator():
			return []error{errors.New("a follow-up the operator or his assistant records is never the operator's to own; it is the Lead Product Manager's unless it names another role")}
		case !f.OwnerReason.Valid():
			return []error{fmt.Errorf("the operator owns a follow-up only for a reason on his closed list, and %q is not one", f.OwnerReason)}
		}
		return nil
	}
	if !f.Owner.Valid() || f.Owner == ownership.MoverUnnamed {
		return []error{fmt.Errorf("owner %q is nobody the registry names", f.Owner)}
	}
	if f.OwnerReason != "" {
		return []error{fmt.Errorf("owner %s carries the operator's reason %q", f.Owner, f.OwnerReason)}
	}
	return nil
}

func (f FollowUp) validateState() []error {
	switch f.State {
	case StateOpen:
		if f.Passed != nil || f.Closed != nil {
			return []error{errors.New("an open follow-up has neither passed nor been closed")}
		}
	case StatePassed:
		if f.Passed == nil || f.Closed != nil {
			return []error{errors.New("a passed follow-up records its check passing, and nothing else ending it")}
		}
		if f.Passed.At.IsZero() {
			return []error{errors.New("a passed follow-up records when its check passed")}
		}
		if err := validText("result", f.Passed.Result, false); err != nil {
			return []error{err}
		}
	case StateClosed:
		if f.Closed == nil || f.Passed != nil {
			return []error{errors.New("a closed follow-up records who closed it and why, and nothing else ending it")}
		}
		var problems []error
		if !f.Closed.By.Valid() {
			problems = append(problems, fmt.Errorf("closed by %q, and a follow-up is closed by its owner, whoever waits, or the operator", f.Closed.By))
		}
		if err := validText("reason", f.Closed.Reason, true); err != nil {
			problems = append(problems, err)
		}
		if f.Closed.At.IsZero() {
			problems = append(problems, errors.New("a closed follow-up records when it was closed"))
		}
		return problems
	default:
		return []error{fmt.Errorf("state %q is not open, passed, or closed", f.State)}
	}
	return nil
}

func validText(name, text string, required bool) error {
	if required && strings.TrimSpace(text) == "" {
		return fmt.Errorf("a %s is required", name)
	}
	if len(text) > MaxTextBytes {
		return fmt.Errorf("the %s is %d bytes, over the %d byte bound", name, len(text), MaxTextBytes)
	}
	return nil
}

// ErrNotOpen is a follow-up asked to end that has already ended.
var ErrNotOpen = errors.New("the follow-up is not open")

// Pass is the follow-up ended by its check passing, at a time, with what the
// check found.
func (f FollowUp) Pass(at time.Time, result string) (FollowUp, error) {
	if f.State != StateOpen {
		return FollowUp{}, fmt.Errorf("%w: it is %s", ErrNotOpen, f.State)
	}
	if at.IsZero() {
		return FollowUp{}, errors.New("a check passing records when it passed")
	}
	if err := validText("result", result, false); err != nil {
		return FollowUp{}, err
	}
	f.State = StatePassed
	f.Passed = &Passed{At: at.UTC(), Result: result}
	return f, f.Validate()
}

// Close is the follow-up ended by somebody other than its check: its owner
// closing it, whoever waits withdrawing it, or the operator closing it. Each
// gives a reason, and one without a reason is refused: being read,
// acknowledged, or answered with "I will look" ends nothing.
func (f FollowUp) Close(by Closer, reason string, at time.Time) (FollowUp, error) {
	if f.State != StateOpen {
		return FollowUp{}, fmt.Errorf("%w: it is %s", ErrNotOpen, f.State)
	}
	if !by.Valid() {
		return FollowUp{}, fmt.Errorf("a follow-up is closed by its owner, whoever waits, or the operator, not %q", by)
	}
	if err := validText("reason", reason, true); err != nil {
		return FollowUp{}, fmt.Errorf("closing a follow-up: %w", err)
	}
	if at.IsZero() {
		return FollowUp{}, errors.New("closing a follow-up records when")
	}
	f.State = StateClosed
	f.Closed = &Closed{By: by, Reason: strings.TrimSpace(reason), At: at.UTC()}
	return f, f.Validate()
}

// Ownership is the facts the registry reads of this follow-up: the owner it
// recorded, why that owner is the operator where it is, and whether the
// operator or his assistant wrote it.
func (f FollowUp) Ownership() ownership.Record {
	return ownership.Record{RecordedOwner: f.Owner, Reason: f.OwnerReason, RecordedByOperator: f.Origin.ByOperator()}
}
