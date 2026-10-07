package runstate

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// This file is the durable half of execution routing
// (docs/designs/claude-execution-and-account-routing.md): which developer slot
// a run occupies, the endpoint pair it was pinned to, and every logical
// operation, invocation attempt, and endpoint switch made under that pair. It
// holds state and the rules for changing it; choosing an endpoint, launching
// one, and reconciling a process are the callers'.
//
// The record lives inside the run record rather than beside it, so a run and
// its routing are written, read, and lost together. Every change to it goes
// through Store.UpdateRouting, which serializes on the run's write lock and
// advances Generation; Store.Save, the whole-record writer every older caller
// uses, refuses a record whose routing differs from what is stored. So a caller
// holding a copy read before a routing change is refused rather than allowed to
// write the change away, and nothing but the operations here can alter routing.

// RoutingVersion is the version of the routing record this build reads and
// writes. A record carrying a later version is one this build does not
// understand: Load refuses it, so nothing acts on it, and no write replaces it.
const RoutingVersion = 1

const (
	// The bounds keep a routing record inside the run record's size bound
	// however long a run lives: a run that needs more operations than this is a
	// run something is looping on, and refusing the next one is the visible
	// answer.
	maxRoutedOperations        = 128
	maxOperationAttempts       = 32
	maxRoutingReconfigurations = 32
	maxRoutingText             = 2048
	maxContextReferences       = 32
)

var routingIDPattern = regexp.MustCompile(`^(op|att|sw)-[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ErrRoutingConflict is a routing request the record cannot take: an identity
// already used for different contents, or a change the operation's state does
// not allow. It is never retried into success, because the record will say the
// same thing next time.
var ErrRoutingConflict = errors.New("routing request conflicts with the recorded routing")

// ErrUnsupportedRouting is a routing record written by a later build. This
// build cannot know what its fields mean, so it neither resumes the run nor
// replaces the record.
var ErrUnsupportedRouting = errors.New("run routing was written by a newer version of the harness")

// StaleRoutingError is a write made from a copy of the run read before its
// routing last changed. The write is refused whole, so nothing the copy did not
// know about is lost; the caller reads the run again and decides again.
type StaleRoutingError struct {
	RunID  string
	Held   uint64
	Stored uint64
}

func (e StaleRoutingError) Error() string {
	return fmt.Sprintf("run %s changed since it was read: its routing is at generation %d and this copy holds %d; read the run again", e.RunID, e.Stored, e.Held)
}

func conflict(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrRoutingConflict, fmt.Sprintf(format, args...))
}

// RoutingOrigin says how a routing fact came to be recorded: when the run
// claimed its slot, by migrating a run recorded before routing existed, or by an
// explicit reconfiguration.
type RoutingOrigin string

const (
	RoutingClaimed      RoutingOrigin = "claimed"
	RoutingMigrated     RoutingOrigin = "migrated"
	RoutingReconfigured RoutingOrigin = "reconfigured"
)

// RunRouting is a run's routing identity and operation history. A run recorded
// without it predates routing: its slot, pair, and switch history are unknown,
// not empty, and only MigrateRouting establishes them.
type RunRouting struct {
	Version int `json:"version"`
	// Generation advances with every change to this record and with nothing
	// else. It is what a write compares to find out whether its copy is stale.
	Generation uint64           `json:"generation"`
	Slot       *RoutedSlot      `json:"slot,omitempty"`
	Developer  *RoutingSnapshot `json:"developer,omitempty"`
	Reviewer   *RoutingSnapshot `json:"reviewer,omitempty"`
	// Reconfigurations are the explicit replacements of a recorded snapshot,
	// oldest first. Reload and resume never add one.
	Reconfigurations []RoutingReconfiguration `json:"reconfigurations,omitempty"`
	Operations       []RoutedOperation        `json:"operations,omitempty"`
}

// RoutedSlot is the developer slot a run occupies. It is a positive number in
// the configured developer capacity and is never re-derived from labels, list
// order, or which processes are alive.
type RoutedSlot struct {
	Number     int           `json:"number"`
	Origin     RoutingOrigin `json:"origin"`
	RecordedAt time.Time     `json:"recorded_at"`
}

// RoutingSnapshot is the non-secret endpoint pair a run's role was pinned to,
// with where each choice came from. Digest identifies its contents, so two
// snapshots resolved from the same configuration agree and a reload that
// changed nothing for this run is recognisably the same pair.
type RoutingSnapshot struct {
	Role            domain.AgentRole `json:"role"`
	Slot            int              `json:"slot,omitempty"`
	Primary         RoutedEndpoint   `json:"primary"`
	Alternate       *RoutedEndpoint  `json:"alternate,omitempty"`
	FallbackEnabled bool             `json:"fallback_enabled"`
	FallbackOrigin  string           `json:"fallback_origin,omitempty"`
	Explicit        bool             `json:"explicit"`
	ConfigRevision  string           `json:"config_revision"`
	Source          string           `json:"source,omitempty"`
	// SwitchLimit is how many automatic endpoint switches one logical operation
	// under this snapshot may make. The design fixes it at one.
	SwitchLimit int           `json:"switch_limit"`
	Origin      RoutingOrigin `json:"origin"`
	Digest      string        `json:"digest"`
	RecordedAt  time.Time     `json:"recorded_at"`
}

// RoutedEndpoint is one endpoint of a pinned pair: provider, adapter, account
// alias, model and effort, and the configuration key each came from. It names
// an account by alias and never carries a credential.
type RoutedEndpoint struct {
	Provider       domain.Backend `json:"provider"`
	AdapterVersion string         `json:"adapter_version,omitempty"`
	AccountAlias   string         `json:"account_alias"`
	Model          string         `json:"model"`
	ModelVersion   string         `json:"model_version,omitempty"`
	Effort         string         `json:"effort,omitempty"`
	Origins        []RoutedOrigin `json:"origins,omitempty"`
}

// RoutedOrigin is the configuration key, and the file it was read from, that
// one field of an endpoint came from.
type RoutedOrigin struct {
	Field  string `json:"field"`
	Origin string `json:"origin"`
}

// RoutingReconfiguration is one explicit replacement of a recorded snapshot.
type RoutingReconfiguration struct {
	Role       domain.AgentRole `json:"role"`
	FromDigest string           `json:"from_digest"`
	ToDigest   string           `json:"to_digest"`
	FromConfig string           `json:"from_config_revision"`
	ToConfig   string           `json:"to_config_revision"`
	Reason     string           `json:"reason"`
	At         time.Time        `json:"at"`
}

// OperationKind is what a logical operation is for. A logical operation is one
// developer work request, one repair answering recorded findings, or one
// required review judgment; its identity survives reissue, waits, restarts,
// and endpoint switches.
type OperationKind string

const (
	OperationDevelop OperationKind = "develop"
	OperationRepair  OperationKind = "repair"
	OperationReview  OperationKind = "review"
)

func (k OperationKind) role() domain.AgentRole {
	if k == OperationReview {
		return domain.RoleReviewer
	}
	return domain.RoleDeveloper
}

// EndpointChoice names one half of a pinned pair.
type EndpointChoice string

const (
	EndpointPrimary   EndpointChoice = "primary"
	EndpointAlternate EndpointChoice = "alternate"
)

// RoutedOperation is one logical operation and everything spent on it.
type RoutedOperation struct {
	ID             string           `json:"id"`
	Kind           OperationKind    `json:"kind"`
	Role           domain.AgentRole `json:"role"`
	SnapshotDigest string           `json:"snapshot_digest"`
	// Candidate is the commit a review judges or a repair starts from, and
	// Findings the reference to the findings a repair answers.
	Candidate string `json:"candidate,omitempty"`
	Findings  string `json:"findings,omitempty"`
	// Budget is where the run's existing repair and review counters stood when
	// the operation opened. They are references only: routing never changes
	// those counters.
	Budget   OperationBudget `json:"budget"`
	Selected EndpointChoice  `json:"selected"`
	// SwitchAllowance is how many automatic switches the operation has left.
	// It is never raised once the operation is open.
	SwitchAllowance int `json:"switch_allowance"`
	// HistoryUnknown marks an operation established by migration from a run
	// whose earlier spending was never recorded. It gets no switch allowance,
	// because missing history is not evidence that none was spent.
	HistoryUnknown bool `json:"history_unknown,omitempty"`
	// TransientRelaunches counts relaunches after something outside the work
	// stopped an attempt, across both endpoints; nil is unknown.
	TransientRelaunches *int `json:"transient_relaunches,omitempty"`
	// RecoveryDeadline is set once and spans the whole operation: a switch does
	// not restart it.
	RecoveryDeadline *time.Time           `json:"recovery_deadline,omitempty"`
	Switch           *EndpointSwitch      `json:"switch,omitempty"`
	Attempts         []InvocationAttempt  `json:"attempts,omitempty"`
	Waiting          *OperationWait       `json:"waiting,omitempty"`
	OpenedAt         time.Time            `json:"opened_at"`
	Completed        *OperationCompletion `json:"completed,omitempty"`
}

// OperationBudget references the run's existing counters; nil is unknown.
type OperationBudget struct {
	RepairAttempts *int `json:"repair_attempts,omitempty"`
	ReviewRounds   *int `json:"review_rounds,omitempty"`
}

// OperationWait is an operation waiting on capacity for its selected endpoint.
// It records what it waits for and never changes the selection.
type OperationWait struct {
	Reason    string         `json:"reason"`
	Endpoint  EndpointChoice `json:"endpoint"`
	ResetAt   *time.Time     `json:"reset_at,omitempty"`
	NextCheck *time.Time     `json:"next_check,omitempty"`
	Since     time.Time      `json:"since"`
}

// OperationCompletion is how an operation ended.
type OperationCompletion struct {
	Outcome string    `json:"outcome"`
	At      time.Time `json:"at"`
}

// SessionMode is how an attempt starts its provider session.
type SessionMode string

const (
	SessionFresh          SessionMode = "fresh"
	SessionNativeResume   SessionMode = "native_resume"
	SessionReconstruction SessionMode = "reconstruction"
)

// SessionEvidence is what an adapter established about resuming a native
// session: which session, on which provider, account and model, and whether
// it judged the session compatible with the attempt's endpoint.
type SessionEvidence struct {
	SessionID    string         `json:"session_id"`
	Provider     domain.Backend `json:"provider"`
	AccountAlias string         `json:"account_alias"`
	Model        string         `json:"model"`
	Compatible   bool           `json:"compatible"`
	Reason       string         `json:"reason,omitempty"`
}

// AttemptState is how far an invocation attempt got.
type AttemptState string

const (
	AttemptPrepared AttemptState = "prepared"
	AttemptLaunched AttemptState = "launched"
	AttemptEnded    AttemptState = "ended"
)

// Termination is whether an attempt's execution is known to have stopped.
type Termination string

const (
	TerminationConfirmed Termination = "confirmed"
	TerminationUncertain Termination = "uncertain"
)

// UsageLimitClassification is the classification that permits a switch from
// the attempt it ended.
const UsageLimitClassification = "usage_limit"

// InvocationAttempt is one provider launch under an operation. Its endpoint is
// copied from the snapshot for the operation's selection when the attempt is
// prepared, so an attempt cannot name an endpoint the run was not pinned to.
type InvocationAttempt struct {
	ID           string           `json:"id"`
	Predecessor  string           `json:"predecessor,omitempty"`
	Choice       EndpointChoice   `json:"choice"`
	Endpoint     RoutedEndpoint   `json:"endpoint"`
	LaunchDigest string           `json:"launch_digest,omitempty"`
	Mode         SessionMode      `json:"mode"`
	Session      *SessionEvidence `json:"session,omitempty"`
	Inputs       string           `json:"inputs,omitempty"`
	Transient    bool             `json:"transient,omitempty"`
	State        AttemptState     `json:"state"`
	PreparedAt   time.Time        `json:"prepared_at"`
	LaunchedAt   *time.Time       `json:"launched_at,omitempty"`
	Ended        *AttemptEnding   `json:"ended,omitempty"`
}

// AttemptEnding is how an attempt ended and whether its execution is known to
// have stopped.
type AttemptEnding struct {
	Classification string      `json:"classification"`
	Termination    Termination `json:"termination"`
	Result         string      `json:"result,omitempty"`
	At             time.Time   `json:"at"`
}

func (a InvocationAttempt) active() bool { return a.State != AttemptEnded }

// SwitchTrigger is what a switch was made on. A classified usage limit is the
// only one the design admits.
type SwitchTrigger string

const (
	// SwitchUsageLimit is a primary attempt that ended on a classified usage
	// limit.
	SwitchUsageLimit SwitchTrigger = "usage_limit"
	// SwitchPrimaryLimited is a primary already known to be usage-limited before
	// launch: the alternate is used without recording a primary attempt that
	// never happened.
	SwitchPrimaryLimited SwitchTrigger = "primary_known_limited"
)

// TransitionProgress is how far a switch has got, in the order the design
// fixes.
type TransitionProgress string

const (
	TransitionPlanned             TransitionProgress = "planned"
	TransitionSourceReconciled    TransitionProgress = "source_reconciled"
	TransitionDestinationPrepared TransitionProgress = "destination_prepared"
	TransitionDestinationLaunched TransitionProgress = "destination_launched"
	TransitionOutcomeRecorded     TransitionProgress = "outcome_recorded"
)

var transitionOrder = map[TransitionProgress]int{
	TransitionPlanned: 1, TransitionSourceReconciled: 2, TransitionDestinationPrepared: 3,
	TransitionDestinationLaunched: 4, TransitionOutcomeRecorded: 5,
}

// EndpointSwitch is an operation's one committed endpoint switch.
type EndpointSwitch struct {
	ID                 string             `json:"id"`
	SourceAttempt      string             `json:"source_attempt,omitempty"`
	From               EndpointChoice     `json:"from"`
	To                 EndpointChoice     `json:"to"`
	Trigger            SwitchTrigger      `json:"trigger"`
	Evidence           string             `json:"evidence"`
	ConfigRevision     string             `json:"config_revision"`
	ContextReferences  []string           `json:"context_references,omitempty"`
	DestinationAttempt string             `json:"destination_attempt"`
	Progress           TransitionProgress `json:"progress"`
	Steps              []TransitionStep   `json:"steps"`
}

// TransitionStep is when a switch reached one stage of its progress.
type TransitionStep struct {
	Progress TransitionProgress `json:"progress"`
	At       time.Time          `json:"at"`
}

// NewRoutingID mints an identity for an operation ("op"), attempt ("att"), or
// switch ("sw"). Callers mint it once and persist it before acting, so a retry
// of the same request carries the same identity.
func NewRoutingID(kind string) (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate %s id: %w", kind, err)
	}
	id := kind + "-" + hex.EncodeToString(bytes)
	if !routingIDPattern.MatchString(id) {
		return "", fmt.Errorf("routing identity kind %q is not op, att, or sw", kind)
	}
	return id, nil
}

// RoutingSnapshotOf records a resolved endpoint pair as a run's snapshot. Only
// the non-secret parts of the resolution are kept.
func RoutingSnapshotOf(pair config.ResolvedEndpointPair, origin RoutingOrigin, at time.Time) RoutingSnapshot {
	endpoint := func(routed config.RoutedEndpoint) RoutedEndpoint {
		origins := make([]RoutedOrigin, 0, len(routed.Origins))
		for field, origin := range routed.Origins {
			origins = append(origins, RoutedOrigin{Field: field, Origin: origin})
		}
		sort.Slice(origins, func(i, j int) bool { return origins[i].Field < origins[j].Field })
		return RoutedEndpoint{
			Provider: routed.Endpoint.Provider, AdapterVersion: routed.Endpoint.AdapterVersion,
			AccountAlias: routed.Endpoint.AccountAlias, Model: routed.Model, ModelVersion: routed.ModelVersion,
			Effort: routed.Effort, Origins: origins,
		}
	}
	snapshot := RoutingSnapshot{
		Role: pair.Role, Slot: pair.Slot, Primary: endpoint(pair.Primary),
		FallbackEnabled: pair.Enabled, FallbackOrigin: pair.EnabledOrigin, Explicit: pair.Explicit,
		ConfigRevision: pair.Revision, Source: pair.Origin, SwitchLimit: 1, Origin: origin, RecordedAt: at.UTC(),
	}
	if pair.Alternate != nil {
		alternate := endpoint(*pair.Alternate)
		snapshot.Alternate = &alternate
	}
	snapshot.Digest = snapshot.contentDigest()
	return snapshot
}

// contentDigest identifies what the snapshot routes to, leaving out when and
// how it was recorded.
func (s RoutingSnapshot) contentDigest() string {
	s.Digest, s.Origin, s.RecordedAt = "", "", time.Time{}
	// The snapshot is strings, numbers, booleans and lists of them in a fixed
	// order, so this cannot fail and is stable.
	encoded, _ := json.Marshal(s)
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (s RoutingSnapshot) endpoint(choice EndpointChoice) (RoutedEndpoint, bool) {
	if choice == EndpointAlternate {
		if s.Alternate == nil {
			return RoutedEndpoint{}, false
		}
		return *s.Alternate, true
	}
	return s.Primary, true
}

// switchable reports whether operations under this snapshot may switch at all.
func (s RoutingSnapshot) switchable() bool {
	return s.FallbackEnabled && s.Alternate != nil
}

// RecordedSlot is the developer slot a run's record names, and zero where none
// is recorded: a run from before routing, or one that has not claimed one.
func (s State) RecordedSlot() int {
	if s.Routing == nil || s.Routing.Slot == nil {
		return 0
	}
	return s.Routing.Slot.Number
}

func (r *RunRouting) snapshot(role domain.AgentRole) **RoutingSnapshot {
	if role == domain.RoleReviewer {
		return &r.Reviewer
	}
	return &r.Developer
}

// Operation finds an operation by identity.
func (r *RunRouting) Operation(id string) (*RoutedOperation, bool) {
	if r == nil {
		return nil, false
	}
	for index := range r.Operations {
		if r.Operations[index].ID == id {
			return &r.Operations[index], true
		}
	}
	return nil, false
}

func (o *RoutedOperation) attempt(id string) (*InvocationAttempt, bool) {
	for index := range o.Attempts {
		if o.Attempts[index].ID == id {
			return &o.Attempts[index], true
		}
	}
	return nil, false
}

// executingAttempt finds an attempt of the operation whose execution may still
// be running: one not ended, or ended without its stop confirmed. While there
// is one, nothing else may launch for the operation, because the design allows
// one execution per operation and an unconfirmed stop is not proof of none.
func (o *RoutedOperation) executingAttempt() (*InvocationAttempt, bool) {
	for index := range o.Attempts {
		if o.Attempts[index].mayBeExecuting() {
			return &o.Attempts[index], true
		}
	}
	return nil, false
}

// ClaimSlot records the developer slot the run occupies. Claiming the slot
// already recorded changes nothing; claiming any other is refused, because a
// run is never renumbered.
func (r *RunRouting) ClaimSlot(number int, origin RoutingOrigin, at time.Time) (bool, error) {
	if number < 1 {
		return false, fmt.Errorf("developer slot %d is not a positive slot number", number)
	}
	if r.Slot != nil {
		if r.Slot.Number == number {
			return false, nil
		}
		return false, conflict("the run occupies developer slot %d and is not renumbered to %d", r.Slot.Number, number)
	}
	r.Slot = &RoutedSlot{Number: number, Origin: origin, RecordedAt: at.UTC()}
	return true, nil
}

// RecordSnapshot pins the run's role to a snapshot. Recording the same pair
// again changes nothing, whatever reload produced it; a different pair is
// refused, because replacing a pinned pair is Reconfigure's alone.
func (r *RunRouting) RecordSnapshot(snapshot RoutingSnapshot) (bool, error) {
	if err := snapshot.validate(); err != nil {
		return false, err
	}
	if snapshot.Role == domain.RoleDeveloper {
		if r.Slot == nil {
			return false, errors.New("a developer routing snapshot is recorded only for a run that occupies a slot")
		}
		if snapshot.Slot != r.Slot.Number {
			return false, conflict("the snapshot is for developer slot %d and the run occupies slot %d", snapshot.Slot, r.Slot.Number)
		}
	}
	held := r.snapshot(snapshot.Role)
	if *held != nil {
		if (*held).Digest == snapshot.Digest {
			return false, nil
		}
		return false, conflict("the run's %s routing is pinned to %s; replacing it needs an explicit reconfiguration", snapshot.Role, (*held).Digest)
	}
	*held = &snapshot
	return true, nil
}

// Reconfigure explicitly replaces a recorded snapshot. It is refused while any
// invocation could still be executing, records both revisions and the reason,
// and leaves every open operation's selection and allowances as they were.
func (r *RunRouting) Reconfigure(snapshot RoutingSnapshot, reason string, at time.Time) (bool, error) {
	if err := snapshot.validate(); err != nil {
		return false, err
	}
	if strings.TrimSpace(reason) == "" || len(reason) > maxRoutingText {
		return false, errors.New("a reconfiguration states a reason within its bound")
	}
	held := r.snapshot(snapshot.Role)
	if *held == nil {
		return false, conflict("the run has no %s routing to reconfigure", snapshot.Role)
	}
	if (*held).Digest == snapshot.Digest {
		return false, nil
	}
	if snapshot.Role == domain.RoleDeveloper && (r.Slot == nil || snapshot.Slot != r.Slot.Number) {
		return false, conflict("a reconfiguration keeps the run's developer slot")
	}
	for _, operation := range r.Operations {
		if operation.Role != snapshot.Role {
			continue
		}
		if attempt, ok := operation.executingAttempt(); ok {
			return false, conflict("attempt %s may still be executing; reconcile it before reconfiguring", attempt.ID)
		}
	}
	if len(r.Reconfigurations) >= maxRoutingReconfigurations {
		return false, conflict("the run has reached its bound of %d reconfigurations", maxRoutingReconfigurations)
	}
	r.Reconfigurations = append(r.Reconfigurations, RoutingReconfiguration{
		Role: snapshot.Role, FromDigest: (*held).Digest, ToDigest: snapshot.Digest,
		FromConfig: (*held).ConfigRevision, ToConfig: snapshot.ConfigRevision, Reason: reason, At: at.UTC(),
	})
	snapshot.Origin = RoutingReconfigured
	*held = &snapshot
	return true, nil
}

// OperationRequest opens a logical operation.
type OperationRequest struct {
	ID        string
	Kind      OperationKind
	Candidate string
	Findings  string
	Budget    OperationBudget
}

func (q OperationRequest) matches(operation RoutedOperation) bool {
	return operation.Kind == q.Kind && operation.Candidate == q.Candidate && operation.Findings == q.Findings &&
		reflect.DeepEqual(operation.Budget, q.Budget)
}

// OpenOperation starts a genuinely new logical operation, which selects its
// primary and receives its own switch allowance. Opening an operation that
// already exists with the same contents changes nothing, so a retried, resumed,
// or restarted request is the same operation and never a fresh allowance.
func (r *RunRouting) OpenOperation(request OperationRequest, at time.Time) (bool, error) {
	if existing, ok := r.Operation(request.ID); ok {
		if request.matches(*existing) {
			return false, nil
		}
		return false, conflict("operation %s is already recorded with different contents", request.ID)
	}
	if !routingIDPattern.MatchString(request.ID) || !strings.HasPrefix(request.ID, "op-") {
		return false, fmt.Errorf("operation identity %q is invalid", request.ID)
	}
	role := request.Kind.role()
	snapshot := *r.snapshot(role)
	if snapshot == nil {
		return false, conflict("the run has no %s routing to open an operation under", role)
	}
	for _, operation := range r.Operations {
		if operation.Role == role && operation.Completed == nil {
			return false, conflict("operation %s is still open for the %s", operation.ID, role)
		}
	}
	if len(r.Operations) >= maxRoutedOperations {
		return false, conflict("the run has reached its bound of %d operations", maxRoutedOperations)
	}
	allowance := 0
	if snapshot.switchable() {
		allowance = snapshot.SwitchLimit
	}
	relaunches := 0
	r.Operations = append(r.Operations, RoutedOperation{
		ID: request.ID, Kind: request.Kind, Role: role, SnapshotDigest: snapshot.Digest,
		Candidate: request.Candidate, Findings: request.Findings, Budget: request.Budget,
		Selected: EndpointPrimary, SwitchAllowance: allowance, TransientRelaunches: &relaunches, OpenedAt: at.UTC(),
	})
	return true, nil
}

// AttemptRequest prepares one invocation attempt under an operation.
type AttemptRequest struct {
	ID           string
	Predecessor  string
	LaunchDigest string
	Mode         SessionMode
	Session      *SessionEvidence
	Inputs       string
	// Transient marks a relaunch after something outside the work stopped the
	// previous attempt; it is counted against the operation's relaunches.
	Transient bool
}

func (q AttemptRequest) matches(attempt InvocationAttempt) bool {
	return attempt.Predecessor == q.Predecessor && attempt.LaunchDigest == q.LaunchDigest && attempt.Mode == q.Mode &&
		reflect.DeepEqual(attempt.Session, q.Session) && attempt.Inputs == q.Inputs && attempt.Transient == q.Transient
}

// PrepareAttempt reserves an attempt identity on the operation's selected
// endpoint before anything launches. Nothing is prepared while an earlier
// attempt of the operation may still be executing — unfinished, or ended
// without its stop confirmed; while a switch is under way, only its reserved
// destination may be prepared, and only once the source is reconciled.
func (r *RunRouting) PrepareAttempt(operationID string, request AttemptRequest, at time.Time) (bool, error) {
	operation, ok := r.Operation(operationID)
	if !ok {
		return false, conflict("operation %s is not recorded", operationID)
	}
	if existing, ok := operation.attempt(request.ID); ok {
		if request.matches(*existing) {
			return false, nil
		}
		return false, conflict("attempt %s is already recorded with different contents", request.ID)
	}
	if r.attemptRecorded(request.ID) {
		return false, conflict("attempt %s belongs to another operation", request.ID)
	}
	if !routingIDPattern.MatchString(request.ID) || !strings.HasPrefix(request.ID, "att-") {
		return false, fmt.Errorf("attempt identity %q is invalid", request.ID)
	}
	if operation.Completed != nil {
		return false, conflict("operation %s is complete", operationID)
	}
	if executing, ok := operation.executingAttempt(); ok {
		return false, conflict("attempt %s of operation %s may still be executing", executing.ID, operationID)
	}
	if request.Predecessor != "" {
		if _, ok := operation.attempt(request.Predecessor); !ok {
			return false, conflict("predecessor %s is not an attempt of operation %s", request.Predecessor, operationID)
		}
	}
	if len(operation.Attempts) >= maxOperationAttempts {
		return false, conflict("operation %s has reached its bound of %d attempts", operationID, maxOperationAttempts)
	}
	advance := false
	if sw := operation.Switch; sw != nil && transitionOrder[sw.Progress] < transitionOrder[TransitionDestinationPrepared] {
		if request.ID != sw.DestinationAttempt {
			return false, conflict("switch %s reserved attempt %s as its destination", sw.ID, sw.DestinationAttempt)
		}
		if sw.Progress != TransitionSourceReconciled {
			return false, conflict("switch %s has not reconciled its source attempt", sw.ID)
		}
		advance = true
	}
	snapshot := *r.snapshot(operation.Role)
	endpoint, ok := snapshot.endpoint(operation.Selected)
	if !ok {
		return false, conflict("the %s routing has no %s endpoint", operation.Role, operation.Selected)
	}
	if err := sessionProblem(request, endpoint); err != nil {
		return false, err
	}
	if request.Transient && operation.TransientRelaunches != nil {
		*operation.TransientRelaunches++
	}
	operation.Attempts = append(operation.Attempts, InvocationAttempt{
		ID: request.ID, Predecessor: request.Predecessor, Choice: operation.Selected, Endpoint: endpoint,
		LaunchDigest: request.LaunchDigest, Mode: request.Mode, Session: request.Session, Inputs: request.Inputs,
		Transient: request.Transient, State: AttemptPrepared, PreparedAt: at.UTC(),
	})
	if advance {
		operation.Switch.advance(TransitionDestinationPrepared, at)
	}
	return true, nil
}

// sessionProblem refuses a native resume the adapter did not establish as
// compatible with exactly this endpoint. Crossing provider, account, or model
// always starts a new session.
func sessionProblem(request AttemptRequest, endpoint RoutedEndpoint) error {
	switch request.Mode {
	case SessionFresh, SessionReconstruction:
		return nil
	case SessionNativeResume:
	default:
		return fmt.Errorf("session mode %q is not fresh, native_resume, or reconstruction", request.Mode)
	}
	session := request.Session
	if session == nil || strings.TrimSpace(session.SessionID) == "" {
		return conflict("a native resume names the session it resumes")
	}
	if !session.Compatible {
		return conflict("session %s was not established as compatible", session.SessionID)
	}
	if session.Provider != endpoint.Provider || session.AccountAlias != endpoint.AccountAlias || session.Model != endpoint.Model {
		return conflict("session %s belongs to %s/%s/%s and cannot be resumed on %s/%s/%s", session.SessionID,
			session.Provider, session.AccountAlias, session.Model, endpoint.Provider, endpoint.AccountAlias, endpoint.Model)
	}
	return nil
}

func (r *RunRouting) attemptRecorded(id string) bool {
	for index := range r.Operations {
		if _, ok := r.Operations[index].attempt(id); ok {
			return true
		}
	}
	return false
}

// MarkLaunched records that a prepared attempt's execution was started.
func (r *RunRouting) MarkLaunched(operationID, attemptID string, at time.Time) (bool, error) {
	operation, attempt, err := r.find(operationID, attemptID)
	if err != nil {
		return false, err
	}
	switch attempt.State {
	case AttemptLaunched, AttemptEnded:
		return false, nil
	}
	attempt.State = AttemptLaunched
	launched := at.UTC()
	attempt.LaunchedAt = &launched
	if sw := operation.Switch; sw != nil && sw.DestinationAttempt == attemptID {
		sw.advance(TransitionDestinationLaunched, at)
	}
	return true, nil
}

// EndAttempt records how an attempt ended. Ending it again the same way changes
// nothing; a different ending is refused, so a late or replayed report cannot
// rewrite an attempt's outcome.
func (r *RunRouting) EndAttempt(operationID, attemptID string, ending AttemptEnding) (bool, error) {
	operation, attempt, err := r.find(operationID, attemptID)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(ending.Classification) == "" || len(ending.Classification) > maxRoutingText || len(ending.Result) > maxRoutingText {
		return false, errors.New("an attempt ending states its classification within its bound")
	}
	if ending.Termination != TerminationConfirmed && ending.Termination != TerminationUncertain {
		return false, fmt.Errorf("termination %q is not confirmed or uncertain", ending.Termination)
	}
	ending.At = ending.At.UTC()
	if attempt.Ended != nil {
		if attempt.Ended.Classification == ending.Classification && attempt.Ended.Result == ending.Result && attempt.Ended.Termination == ending.Termination {
			return false, nil
		}
		// Uncertain termination may later be confirmed; nothing else changes.
		if attempt.Ended.Termination == TerminationUncertain && ending.Termination == TerminationConfirmed &&
			attempt.Ended.Classification == ending.Classification && attempt.Ended.Result == ending.Result {
			attempt.Ended.Termination = TerminationConfirmed
			return true, nil
		}
		return false, conflict("attempt %s already ended as %s", attemptID, attempt.Ended.Classification)
	}
	attempt.State = AttemptEnded
	attempt.Ended = &ending
	if sw := operation.Switch; sw != nil && sw.DestinationAttempt == attemptID && transitionOrder[sw.Progress] >= transitionOrder[TransitionDestinationPrepared] {
		sw.advance(TransitionOutcomeRecorded, ending.At)
	}
	return true, nil
}

// mayBeExecuting reports an attempt whose execution may still be running: one
// not ended, or ended without confirmed termination.
func (a InvocationAttempt) mayBeExecuting() bool {
	return a.active() || (a.Ended != nil && a.Ended.Termination != TerminationConfirmed)
}

func (r *RunRouting) find(operationID, attemptID string) (*RoutedOperation, *InvocationAttempt, error) {
	operation, ok := r.Operation(operationID)
	if !ok {
		return nil, nil, conflict("operation %s is not recorded", operationID)
	}
	attempt, ok := operation.attempt(attemptID)
	if !ok {
		return nil, nil, conflict("attempt %s is not an attempt of operation %s", attemptID, operationID)
	}
	return operation, attempt, nil
}

// SwitchRequest plans an operation's endpoint switch.
type SwitchRequest struct {
	ID                 string
	SourceAttempt      string
	Trigger            SwitchTrigger
	Evidence           string
	ConfigRevision     string
	ContextReferences  []string
	DestinationAttempt string
}

func (q SwitchRequest) matches(sw EndpointSwitch) bool {
	return sw.ID == q.ID && sw.SourceAttempt == q.SourceAttempt && sw.Trigger == q.Trigger && sw.Evidence == q.Evidence &&
		sw.ConfigRevision == q.ConfigRevision && slicesEqual(sw.ContextReferences, q.ContextReferences) &&
		sw.DestinationAttempt == q.DestinationAttempt
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

// PlanSwitch commits the operation's one automatic switch from its primary to
// its alternate, consuming the allowance and reserving the destination attempt
// in the same write. A primary already known to be limited is skipped with no
// source attempt; otherwise the source is a primary attempt that ended on a
// classified usage limit. Planning the same switch again changes nothing; any
// other switch on the operation is refused, and so is one with no allowance
// left, which is how a migrated operation with unknown history is held to none.
func (r *RunRouting) PlanSwitch(operationID string, request SwitchRequest, at time.Time) (bool, error) {
	operation, ok := r.Operation(operationID)
	if !ok {
		return false, conflict("operation %s is not recorded", operationID)
	}
	if sw := operation.Switch; sw != nil {
		if request.matches(*sw) {
			return false, nil
		}
		return false, conflict("operation %s already committed switch %s", operationID, sw.ID)
	}
	if !routingIDPattern.MatchString(request.ID) || !strings.HasPrefix(request.ID, "sw-") {
		return false, fmt.Errorf("switch identity %q is invalid", request.ID)
	}
	if !routingIDPattern.MatchString(request.DestinationAttempt) || !strings.HasPrefix(request.DestinationAttempt, "att-") || r.attemptRecorded(request.DestinationAttempt) {
		return false, conflict("switch %s reserves a new attempt identity as its destination", request.ID)
	}
	if strings.TrimSpace(request.Evidence) == "" || len(request.Evidence) > maxRoutingText || len(request.ContextReferences) > maxContextReferences {
		return false, errors.New("a switch states its evidence, and its context references, within their bounds")
	}
	if operation.Completed != nil {
		return false, conflict("operation %s is complete", operationID)
	}
	if operation.SwitchAllowance < 1 {
		return false, conflict("operation %s has no endpoint switch left", operationID)
	}
	if operation.Selected != EndpointPrimary {
		return false, conflict("operation %s is not on its primary", operationID)
	}
	snapshot := *r.snapshot(operation.Role)
	if !snapshot.switchable() {
		return false, conflict("the %s routing has no enabled alternate", operation.Role)
	}
	progress := TransitionPlanned
	switch request.Trigger {
	case SwitchUsageLimit:
		source, ok := operation.attempt(request.SourceAttempt)
		if !ok || source.Choice != EndpointPrimary {
			return false, conflict("a usage-limit switch names the primary attempt that reached the limit")
		}
		// The classification is what permits the switch, so it has to be known
		// before the allowance is spent: a source still running has not been
		// classified, and nothing after this point asks why it stopped.
		if source.Ended == nil {
			return false, conflict("attempt %s has not ended, so nothing has classified it as a usage limit", source.ID)
		}
		if source.Ended.Classification != UsageLimitClassification {
			return false, conflict("attempt %s ended as %s, which is not a usage limit", source.ID, source.Ended.Classification)
		}
	case SwitchPrimaryLimited:
		if request.SourceAttempt != "" {
			return false, conflict("a switch past a primary known to be limited names no source attempt")
		}
		if executing, ok := operation.executingAttempt(); ok {
			return false, conflict("attempt %s of operation %s may still be executing", executing.ID, operationID)
		}
		progress = TransitionSourceReconciled
	default:
		return false, conflict("switch trigger %q is not a classified usage limit", request.Trigger)
	}
	operation.SwitchAllowance--
	operation.Selected = EndpointAlternate
	sw := &EndpointSwitch{
		ID: request.ID, SourceAttempt: request.SourceAttempt, From: EndpointPrimary, To: EndpointAlternate,
		Trigger: request.Trigger, Evidence: request.Evidence, ConfigRevision: request.ConfigRevision,
		ContextReferences: append([]string(nil), request.ContextReferences...), DestinationAttempt: request.DestinationAttempt,
	}
	sw.advance(TransitionPlanned, at)
	if progress != TransitionPlanned {
		sw.advance(progress, at)
	}
	operation.Switch = sw
	return true, nil
}

func (s *EndpointSwitch) advance(progress TransitionProgress, at time.Time) {
	if transitionOrder[progress] <= transitionOrder[s.Progress] {
		return
	}
	s.Progress = progress
	s.Steps = append(s.Steps, TransitionStep{Progress: progress, At: at.UTC()})
}

// ReconcileSource records that a switch's source execution is confirmed
// stopped, which is what lets its destination be prepared. A source that ended
// with uncertain termination, or has not ended, is refused: the switch waits.
func (r *RunRouting) ReconcileSource(operationID, switchID string, at time.Time) (bool, error) {
	operation, ok := r.Operation(operationID)
	if !ok || operation.Switch == nil || operation.Switch.ID != switchID {
		return false, conflict("switch %s is not recorded on operation %s", switchID, operationID)
	}
	sw := operation.Switch
	if transitionOrder[sw.Progress] >= transitionOrder[TransitionSourceReconciled] {
		return false, nil
	}
	source, ok := operation.attempt(sw.SourceAttempt)
	if !ok || source.mayBeExecuting() {
		return false, conflict("source attempt %s of switch %s may still be executing", sw.SourceAttempt, switchID)
	}
	sw.advance(TransitionSourceReconciled, at)
	return true, nil
}

// SetWaiting records that the operation waits on capacity for its selected
// endpoint. The selection is never changed by waiting: a committed alternate
// stays selected.
func (r *RunRouting) SetWaiting(operationID string, wait OperationWait) (bool, error) {
	operation, ok := r.Operation(operationID)
	if !ok {
		return false, conflict("operation %s is not recorded", operationID)
	}
	if operation.Completed != nil {
		return false, conflict("operation %s is complete", operationID)
	}
	if strings.TrimSpace(wait.Reason) == "" || len(wait.Reason) > maxRoutingText {
		return false, errors.New("a wait states its reason within its bound")
	}
	if wait.Endpoint != operation.Selected {
		return false, conflict("operation %s has %s selected and cannot wait on its %s", operationID, operation.Selected, wait.Endpoint)
	}
	wait.Since = wait.Since.UTC()
	if operation.Waiting != nil && reflect.DeepEqual(*operation.Waiting, wait) {
		return false, nil
	}
	operation.Waiting = &wait
	return true, nil
}

// ClearWaiting records that the operation is no longer waiting.
func (r *RunRouting) ClearWaiting(operationID string) (bool, error) {
	operation, ok := r.Operation(operationID)
	if !ok {
		return false, conflict("operation %s is not recorded", operationID)
	}
	if operation.Waiting == nil {
		return false, nil
	}
	operation.Waiting = nil
	return true, nil
}

// SetRecoveryDeadline sets the operation's recovery deadline once. It spans
// the operation across both endpoints, so a different deadline is refused.
func (r *RunRouting) SetRecoveryDeadline(operationID string, deadline time.Time) (bool, error) {
	operation, ok := r.Operation(operationID)
	if !ok {
		return false, conflict("operation %s is not recorded", operationID)
	}
	deadline = deadline.UTC()
	if operation.RecoveryDeadline != nil {
		if operation.RecoveryDeadline.Equal(deadline) {
			return false, nil
		}
		return false, conflict("operation %s already has its recovery deadline", operationID)
	}
	operation.RecoveryDeadline = &deadline
	return true, nil
}

// CompleteOperation ends an operation. It is refused while any of its attempts
// may still be executing.
func (r *RunRouting) CompleteOperation(operationID, outcome string, at time.Time) (bool, error) {
	operation, ok := r.Operation(operationID)
	if !ok {
		return false, conflict("operation %s is not recorded", operationID)
	}
	if strings.TrimSpace(outcome) == "" || len(outcome) > maxRoutingText {
		return false, errors.New("an operation's completion states its outcome within its bound")
	}
	if operation.Completed != nil {
		if operation.Completed.Outcome == outcome {
			return false, nil
		}
		return false, conflict("operation %s already completed as %s", operationID, operation.Completed.Outcome)
	}
	for _, attempt := range operation.Attempts {
		if attempt.mayBeExecuting() {
			return false, conflict("attempt %s of operation %s may still be executing", attempt.ID, operationID)
		}
	}
	operation.Waiting = nil
	operation.Completed = &OperationCompletion{Outcome: outcome, At: at.UTC()}
	return true, nil
}

// Migration is what a quiescent legacy run's routing is established from: the
// slot it is given now, the current validated snapshot, and the unfinished
// operation's identity where it has one.
type Migration struct {
	Snapshot  RoutingSnapshot
	Operation *OperationRequest
	At        time.Time
}

// migrate establishes routing for a run recorded before routing existed.
// Everything it records is a fact established now and marked so: the slot and
// snapshot carry the migrated origin, and an unfinished operation is recorded
// with unknown history, no switch allowance, and unknown relaunches, because
// what the run spent before cannot be read back from records that never held it.
func (r *RunRouting) migrate(slot int, migration Migration) (bool, error) {
	snapshot := migration.Snapshot
	snapshot.Origin = RoutingMigrated
	if r.Developer != nil {
		// A repeated migration is the same request answered again.
		if r.Slot != nil && r.Slot.Number == slot && r.Developer.Digest == snapshot.Digest {
			if migration.Operation == nil {
				return false, nil
			}
			if existing, ok := r.Operation(migration.Operation.ID); ok && migration.Operation.matches(*existing) {
				return false, nil
			}
		}
		return false, conflict("the run already has routing; migration only establishes what is unknown")
	}
	if len(r.Operations) > 0 {
		return false, conflict("the run already has operations; migration only establishes what is unknown")
	}
	changed, err := r.ClaimSlot(slot, RoutingMigrated, migration.At)
	if err != nil {
		return false, err
	}
	if more, err := r.RecordSnapshot(snapshot); err != nil {
		return false, err
	} else if more {
		changed = true
	}
	if request := migration.Operation; request != nil {
		if _, err := r.OpenOperation(*request, migration.At); err != nil {
			return false, err
		}
		operation, _ := r.Operation(request.ID)
		operation.HistoryUnknown = true
		operation.SwitchAllowance = 0
		operation.TransientRelaunches = nil
		changed = true
	}
	return changed, nil
}

func (s RoutingSnapshot) validate() error {
	var problems []string
	if s.Role != domain.RoleDeveloper && s.Role != domain.RoleReviewer {
		problems = append(problems, fmt.Sprintf("role %q is not developer or reviewer", s.Role))
	}
	if s.Role == domain.RoleDeveloper && s.Slot < 1 {
		problems = append(problems, "a developer snapshot names its positive slot")
	}
	if s.Role == domain.RoleReviewer && s.Slot != 0 {
		problems = append(problems, "a reviewer snapshot names no developer slot")
	}
	for _, endpoint := range append([]RoutedEndpoint{s.Primary}, derefEndpoint(s.Alternate)...) {
		problems = append(problems, endpoint.problems()...)
	}
	if s.FallbackEnabled && s.Alternate == nil {
		problems = append(problems, "fallback is enabled with no alternate")
	}
	if s.SwitchLimit != 1 {
		problems = append(problems, "the switch limit is one switch per operation")
	}
	if s.ConfigRevision != "" && !configRevisionPattern.MatchString(s.ConfigRevision) {
		problems = append(problems, "config_revision is invalid")
	}
	if len(s.Source) > maxRoutingText || len(s.FallbackOrigin) > maxRoutingText {
		problems = append(problems, "a snapshot origin exceeds its bound")
	}
	switch s.Origin {
	case RoutingClaimed, RoutingMigrated, RoutingReconfigured:
	default:
		problems = append(problems, fmt.Sprintf("snapshot origin %q is unknown", s.Origin))
	}
	if s.Digest != s.contentDigest() {
		problems = append(problems, "the snapshot digest does not match its contents")
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid routing snapshot: %s", strings.Join(problems, "; "))
	}
	return nil
}

func (e RoutedEndpoint) problems() []string {
	var problems []string
	if err := domain.ValidateIdentifier("provider", string(e.Provider)); err != nil {
		problems = append(problems, err.Error())
	}
	if !accountAliasPattern.MatchString(e.AccountAlias) {
		problems = append(problems, fmt.Sprintf("account alias %q is invalid", e.AccountAlias))
	}
	if strings.TrimSpace(e.Model) == "" || len(e.Model)+len(e.ModelVersion)+len(e.Effort)+len(e.AdapterVersion) > maxRoutingText {
		problems = append(problems, "an endpoint names its model, and its model, version, effort, and adapter fit their bound")
	}
	for _, origin := range e.Origins {
		if len(origin.Field)+len(origin.Origin) > maxRoutingText {
			problems = append(problems, "an endpoint origin exceeds its bound")
		}
	}
	return problems
}

func derefEndpoint(endpoint *RoutedEndpoint) []RoutedEndpoint {
	if endpoint == nil {
		return nil
	}
	return []RoutedEndpoint{*endpoint}
}

// Validate checks a routing record as the durable schema holds it. A version
// newer than this build's is not refused here, so a listing can still read the
// rest of the run; Load and every write refuse it instead.
func (r *RunRouting) Validate() error {
	if r == nil {
		return nil
	}
	var problems []string
	if r.Version < 1 {
		problems = append(problems, "version must be at least 1")
	}
	if r.Version > RoutingVersion {
		return nil
	}
	if r.Slot != nil {
		if r.Slot.Number < 1 {
			problems = append(problems, "slot must be positive")
		}
		if r.Slot.Origin != RoutingClaimed && r.Slot.Origin != RoutingMigrated {
			problems = append(problems, fmt.Sprintf("slot origin %q is unknown", r.Slot.Origin))
		}
	}
	for _, snapshot := range []*RoutingSnapshot{r.Developer, r.Reviewer} {
		if snapshot == nil {
			continue
		}
		if err := snapshot.validate(); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if r.Developer != nil && (r.Developer.Role != domain.RoleDeveloper || r.Slot == nil || r.Developer.Slot != r.Slot.Number) {
		problems = append(problems, "the developer snapshot is for the run's developer slot")
	}
	if r.Reviewer != nil && r.Reviewer.Role != domain.RoleReviewer {
		problems = append(problems, "the reviewer snapshot is for the reviewer")
	}
	for _, change := range r.Reconfigurations {
		if change.Role != domain.RoleDeveloper && change.Role != domain.RoleReviewer {
			problems = append(problems, fmt.Sprintf("reconfiguration role %q is not developer or reviewer", change.Role))
		}
		for _, revision := range []string{change.FromConfig, change.ToConfig} {
			if revision != "" && !configRevisionPattern.MatchString(revision) {
				problems = append(problems, "a reconfiguration's config revision is invalid")
			}
		}
	}
	if len(r.Reconfigurations) > maxRoutingReconfigurations {
		problems = append(problems, "too many reconfigurations")
	}
	if len(r.Operations) > maxRoutedOperations {
		problems = append(problems, "too many operations")
	}
	seen := map[string]bool{}
	open := map[domain.AgentRole]bool{}
	for _, operation := range r.Operations {
		problems = append(problems, operation.problems(seen)...)
		if operation.Completed == nil {
			if open[operation.Role] {
				problems = append(problems, fmt.Sprintf("more than one open %s operation", operation.Role))
			}
			open[operation.Role] = true
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid routing: %s", strings.Join(problems, "; "))
	}
	return nil
}

func (o RoutedOperation) problems(seen map[string]bool) []string {
	var problems []string
	claim := func(id, prefix string) {
		if !routingIDPattern.MatchString(id) || !strings.HasPrefix(id, prefix) {
			problems = append(problems, fmt.Sprintf("identity %q is invalid", id))
		}
		if seen[id] {
			problems = append(problems, fmt.Sprintf("identity %s is recorded twice", id))
		}
		seen[id] = true
	}
	claim(o.ID, "op-")
	if o.Kind.role() != o.Role || (o.Kind != OperationDevelop && o.Kind != OperationRepair && o.Kind != OperationReview) {
		problems = append(problems, fmt.Sprintf("operation %s has kind %q for role %q", o.ID, o.Kind, o.Role))
	}
	if o.Candidate != "" && !commitPattern.MatchString(o.Candidate) {
		problems = append(problems, fmt.Sprintf("operation %s candidate is not a commit", o.ID))
	}
	if o.Waiting != nil && o.Waiting.Endpoint != o.Selected {
		problems = append(problems, fmt.Sprintf("operation %s waits on an endpoint it has not selected", o.ID))
	}
	if o.Selected != EndpointPrimary && o.Selected != EndpointAlternate {
		problems = append(problems, fmt.Sprintf("operation %s selects %q", o.ID, o.Selected))
	}
	if o.SwitchAllowance < 0 || o.SwitchAllowance > 1 || (o.HistoryUnknown && o.SwitchAllowance != 0) {
		problems = append(problems, fmt.Sprintf("operation %s has switch allowance %d", o.ID, o.SwitchAllowance))
	}
	if (o.Switch != nil) != (o.Selected == EndpointAlternate) {
		problems = append(problems, fmt.Sprintf("operation %s selects its alternate only through a recorded switch", o.ID))
	}
	if len(o.Attempts) > maxOperationAttempts {
		problems = append(problems, fmt.Sprintf("operation %s has too many attempts", o.ID))
	}
	active := 0
	for _, attempt := range o.Attempts {
		claim(attempt.ID, "att-")
		if attempt.active() {
			active++
		}
		if (attempt.State == AttemptEnded) != (attempt.Ended != nil) {
			problems = append(problems, fmt.Sprintf("attempt %s state %q disagrees with its ending", attempt.ID, attempt.State))
		}
		problems = append(problems, attempt.Endpoint.problems()...)
		switch attempt.State {
		case AttemptPrepared, AttemptLaunched, AttemptEnded:
		default:
			problems = append(problems, fmt.Sprintf("attempt %s state %q is unknown", attempt.ID, attempt.State))
		}
		switch attempt.Mode {
		case SessionFresh, SessionNativeResume, SessionReconstruction:
		default:
			problems = append(problems, fmt.Sprintf("attempt %s session mode %q is unknown", attempt.ID, attempt.Mode))
		}
		if attempt.Choice != EndpointPrimary && attempt.Choice != EndpointAlternate {
			problems = append(problems, fmt.Sprintf("attempt %s choice %q is unknown", attempt.ID, attempt.Choice))
		}
		if attempt.Ended != nil && attempt.Ended.Termination != TerminationConfirmed && attempt.Ended.Termination != TerminationUncertain {
			problems = append(problems, fmt.Sprintf("attempt %s termination %q is unknown", attempt.ID, attempt.Ended.Termination))
		}
		if attempt.Predecessor != "" && !routingIDPattern.MatchString(attempt.Predecessor) {
			problems = append(problems, fmt.Sprintf("attempt %s predecessor %q is invalid", attempt.ID, attempt.Predecessor))
		}
	}
	if active > 1 {
		problems = append(problems, fmt.Sprintf("operation %s has more than one active attempt", o.ID))
	}
	if sw := o.Switch; sw != nil {
		claim(sw.ID, "sw-")
		if transitionOrder[sw.Progress] == 0 {
			problems = append(problems, fmt.Sprintf("switch %s progress %q is unknown", sw.ID, sw.Progress))
		}
		if sw.Trigger != SwitchUsageLimit && sw.Trigger != SwitchPrimaryLimited {
			problems = append(problems, fmt.Sprintf("switch %s trigger %q is not a classified usage limit", sw.ID, sw.Trigger))
		}
		if sw.From != EndpointPrimary || sw.To != EndpointAlternate {
			problems = append(problems, fmt.Sprintf("switch %s is not from the primary to the alternate", sw.ID))
		}
		if sw.SourceAttempt != "" && !routingIDPattern.MatchString(sw.SourceAttempt) {
			problems = append(problems, fmt.Sprintf("switch %s source attempt is invalid", sw.ID))
		}
		if sw.ConfigRevision != "" && !configRevisionPattern.MatchString(sw.ConfigRevision) {
			problems = append(problems, fmt.Sprintf("switch %s config_revision is invalid", sw.ID))
		}
		for _, step := range sw.Steps {
			if transitionOrder[step.Progress] == 0 {
				problems = append(problems, fmt.Sprintf("switch %s step %q is unknown", sw.ID, step.Progress))
			}
		}
		if !strings.HasPrefix(sw.DestinationAttempt, "att-") {
			problems = append(problems, fmt.Sprintf("switch %s reserves no destination attempt", sw.ID))
		}
	}
	return problems
}

// equalRouting reports two routing records that say the same thing.
func equalRouting(a, b *RunRouting) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(left) == string(right)
}

func (r *RunRouting) generation() uint64 {
	if r == nil {
		return 0
	}
	return r.Generation
}

func (r *RunRouting) clone() *RunRouting {
	if r == nil {
		return &RunRouting{Version: RoutingVersion}
	}
	encoded, _ := json.Marshal(r)
	var copied RunRouting
	_ = json.Unmarshal(encoded, &copied)
	return &copied
}

// recordedTexts names the routing record's free text for State.recordedTexts,
// which bounds it. Everything else in the record is an identity, an
// enumeration, a commit, a digest, or a configuration key and source that
// Validate holds to its shape.
func (r *RunRouting) recordedTexts(add func(key, path string, text *string)) {
	if r == nil {
		return
	}
	for index := range r.Reconfigurations {
		add("routing.reconfigurations[].reason", fmt.Sprintf("routing.reconfigurations[%d].reason", index), &r.Reconfigurations[index].Reason)
	}
	for index := range r.Operations {
		operation := &r.Operations[index]
		at := func(field string) string { return fmt.Sprintf("routing.operations[%d].%s", index, field) }
		add("routing.operations[].findings", at("findings"), &operation.Findings)
		if operation.Waiting != nil {
			add("routing.operations[].waiting.reason", at("waiting.reason"), &operation.Waiting.Reason)
		}
		if operation.Completed != nil {
			add("routing.operations[].completed.outcome", at("completed.outcome"), &operation.Completed.Outcome)
		}
		if sw := operation.Switch; sw != nil {
			add("routing.operations[].switch.evidence", at("switch.evidence"), &sw.Evidence)
			for reference := range sw.ContextReferences {
				add("routing.operations[].switch.context_references[]", at(fmt.Sprintf("switch.context_references[%d]", reference)), &sw.ContextReferences[reference])
			}
		}
		for attemptIndex := range operation.Attempts {
			attempt := &operation.Attempts[attemptIndex]
			field := func(name string) string { return at(fmt.Sprintf("attempts[%d].%s", attemptIndex, name)) }
			add("routing.operations[].attempts[].launch_digest", field("launch_digest"), &attempt.LaunchDigest)
			add("routing.operations[].attempts[].inputs", field("inputs"), &attempt.Inputs)
			if attempt.Session != nil {
				add("routing.operations[].attempts[].session.reason", field("session.reason"), &attempt.Session.Reason)
			}
			if attempt.Ended != nil {
				add("routing.operations[].attempts[].ended.classification", field("ended.classification"), &attempt.Ended.Classification)
				add("routing.operations[].attempts[].ended.result", field("ended.result"), &attempt.Ended.Result)
			}
		}
	}
}
