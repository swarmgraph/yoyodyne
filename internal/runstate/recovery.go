package runstate

// What the development manager's recovery tool, run.recover, writes on a run.
//
// The tool is specified in "The five capabilities" and the sections after it of
// docs/designs/fresh-factory-health-and-preserved-run-recovery.md, and the code
// that carries a request out is internal/runrecover. What is here is only the
// part that has to outlive the conversation that asked: the request itself,
// recorded on the run under the run's lease in the same write as its effect, so
// a repeat of the request, a restart, or a response that never reached its
// caller can always be settled by reading the run; the unresolved hold a request
// can leave; and the continuation a request asks the work session to carry out.
//
// The request and its effect are one write on purpose. Applied is the request's
// entry on the record with its effect beside it, so a reader that finds the
// entry has found the effect, and one that does not find it has found that
// nothing happened. A request whose entry is recorded and whose outcome is not
// is one whose writer could not read its own write back; the next request
// under the lease reads it back and says so.

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// RecoveryAction is one action of run.recover. The list is closed, and it is
// the list of the development manager's decisions in step 3 of
// docs/designs/stopped-run-recovery.md with the release of a finished run's
// reservation added, in the design's order.
type RecoveryAction string

const (
	RecoverContinue    RecoveryAction = "continue"
	RecoverRepair      RecoveryAction = "repair"
	RecoverRerun       RecoveryAction = "rerun"
	RecoverFinishMerge RecoveryAction = "finish the merge"
	RecoverWait        RecoveryAction = "wait"
	RecoverGiveBack    RecoveryAction = "give the item back"
	RecoverHold        RecoveryAction = "record an unresolved hold"
	RecoverAsk         RecoveryAction = "ask the lead product manager"
	RecoverRelease     RecoveryAction = "release the reservation"
)

var recoveryActions = []RecoveryAction{
	RecoverContinue, RecoverRepair, RecoverRerun, RecoverFinishMerge, RecoverWait,
	RecoverGiveBack, RecoverHold, RecoverAsk, RecoverRelease,
}

// RecoveryActions is the closed list, in the design's order.
func RecoveryActions() []RecoveryAction { return append([]RecoveryAction(nil), recoveryActions...) }

// Known reports an action on the closed list.
func (a RecoveryAction) Known() bool {
	for _, known := range recoveryActions {
		if a == known {
			return true
		}
	}
	return false
}

// MaxRecoveryRequests is how many requests one run keeps. The oldest is let go
// first. A request older than any observation it could have rested on is
// refused for its observation's age before its identifier is looked for, so
// letting one go loses nothing a repeat could still have been told.
const MaxRecoveryRequests = 16

// RecoveryOutcome is what a recorded request came to. A request is recorded
// only when it reaches its write, so a refusal is never one of these: it
// changed nothing, including this record.
type RecoveryOutcome string

const (
	// RecoveryPending is a request written with its effect whose writer had not
	// yet read the write back. Its effect is on the record, because the two are
	// one write; what is not known is only whether the caller was told.
	RecoveryPending RecoveryOutcome = ""
	RecoveryApplied RecoveryOutcome = "applied"
)

// RecoveryRequest is one run.recover request as the run recorded it.
type RecoveryRequest struct {
	// ID is the identifier the caller made up, and Digest is the request's
	// whole content, so a repeat with the same identifier and other content is
	// told apart from a repeat of the same request.
	ID     string         `json:"id"`
	Digest string         `json:"digest"`
	Action RecoveryAction `json:"action"`
	// Observation is the health observation the decision rested on.
	Observation string `json:"observation"`
	// By is the role that asked.
	By          domain.AgentRole `json:"by"`
	Reason      string           `json:"reason"`
	RequestedAt time.Time        `json:"requested_at"`
	Outcome     RecoveryOutcome  `json:"outcome,omitempty"`
	SettledAt   *time.Time       `json:"settled_at,omitempty"`
}

func (r RecoveryRequest) Validate() error {
	var problems []error
	if !ValidRecoveryRequestID(r.ID) {
		problems = append(problems, fmt.Errorf("id %q is not a plain request identifier", r.ID))
	}
	if strings.TrimSpace(r.Digest) == "" {
		problems = append(problems, errors.New("digest is required"))
	}
	if !r.Action.Known() {
		problems = append(problems, fmt.Errorf("action %q is not one of the recovery actions", r.Action))
	}
	if !ValidHealthObservationID(r.Observation) {
		problems = append(problems, errors.New("observation must name a health observation"))
	}
	if !r.By.Valid() {
		problems = append(problems, fmt.Errorf("by %q is not a role", r.By))
	}
	if r.RequestedAt.IsZero() {
		problems = append(problems, errors.New("requested_at is required"))
	}
	switch r.Outcome {
	case RecoveryPending:
		if r.SettledAt != nil {
			problems = append(problems, errors.New("a pending request has no settled_at"))
		}
	case RecoveryApplied:
		if r.SettledAt == nil {
			problems = append(problems, errors.New("an applied request records when it was read back"))
		}
	default:
		problems = append(problems, fmt.Errorf("outcome %q is not one a recorded request can have", r.Outcome))
	}
	return errors.Join(problems...)
}

// ValidRecoveryRequestID is an identifier a caller may make up: a plain name.
func ValidRecoveryRequestID(id string) bool {
	return id != "" && len(id) <= 96 && !strings.ContainsAny(id, "/\\ \t\r\n")
}

// RecoveryRequest is the recorded request with this identifier.
func (s State) RecoveryRequest(id string) (RecoveryRequest, bool) {
	for _, request := range s.RecoveryRequests {
		if request.ID == id {
			return request, true
		}
	}
	return RecoveryRequest{}, false
}

// PendingRecoveryRequests are the recorded requests whose writer did not read
// its write back.
func (s State) PendingRecoveryRequests() []RecoveryRequest {
	var pending []RecoveryRequest
	for _, request := range s.RecoveryRequests {
		if request.Outcome == RecoveryPending {
			pending = append(pending, request)
		}
	}
	return pending
}

// RecoveryHold is an unresolved recovery hold: a run nobody can continue yet,
// with the role whose move it is, what that move is, and when or on what it is
// looked at again. It is the development manager's account standing on the
// run, so a sweep does not settle the run as one nothing serves while it
// stands, and every health observation reports it until something later
// replaces it.
type RecoveryHold struct {
	RequestID string `json:"request_id"`
	// Owner is the role whose move it is. It is never the operator: a hold is a
	// recovery decision, and none of those is his (internal/ownership).
	Owner      domain.AgentRole `json:"owner"`
	NextAction string           `json:"next_action"`
	// FollowUp is the time or the event it is looked at again on; FollowUpAt is
	// that time where it is one.
	FollowUp   string     `json:"follow_up"`
	FollowUpAt *time.Time `json:"follow_up_at,omitempty"`
	At         time.Time  `json:"at"`
}

func (h RecoveryHold) Validate() error {
	var problems []error
	if !ValidRecoveryRequestID(h.RequestID) {
		problems = append(problems, errors.New("request_id is required"))
	}
	if !RecoveryHoldOwner(h.Owner) {
		problems = append(problems, fmt.Errorf("owner %q is not a role a recovery hold may name", h.Owner))
	}
	if strings.TrimSpace(h.NextAction) == "" {
		problems = append(problems, errors.New("next_action is required"))
	}
	if strings.TrimSpace(h.FollowUp) == "" {
		problems = append(problems, errors.New("follow_up is required"))
	}
	if h.At.IsZero() {
		problems = append(problems, errors.New("at is required"))
	}
	return errors.Join(problems...)
}

// RecoveryHoldOwner reports a role a hold may name: one of the managing
// roles. A developer or reviewer acts only inside a run, and the operator is
// named only by the ownership registry, for reasons none of which is a
// recovery decision.
func RecoveryHoldOwner(role domain.AgentRole) bool {
	switch role {
	case domain.RoleDevelopmentManager, domain.RoleProductManager, domain.RoleArchitect, domain.RoleProgramManager:
		return true
	}
	return false
}

// RecoveryContinuation is a continuation a run.recover request asked the work
// session to carry out: the same run continued from its recorded phase, or the
// steps after an approved change's stopped integration finished. The
// conversation that asked cannot run either, because each is a run of the
// pipeline, so the request is written here and the work session's next pull
// takes it up, under the run's lease and through the same path that would
// have taken the run up anyway.
type RecoveryContinuation struct {
	RequestID   string         `json:"request_id"`
	Action      RecoveryAction `json:"action"`
	Reason      string         `json:"reason"`
	RequestedAt time.Time      `json:"requested_at"`
	// TakenUpAt is when the pipeline took the run up for it. A continuation
	// taken up is kept, as the account of why the run went on.
	TakenUpAt *time.Time `json:"taken_up_at,omitempty"`
	// Refused is why the work session could not carry it out and will not try
	// again by itself; a new request replaces it.
	Refused   string     `json:"refused,omitempty"`
	RefusedAt *time.Time `json:"refused_at,omitempty"`
}

func (c RecoveryContinuation) Validate() error {
	var problems []error
	if !ValidRecoveryRequestID(c.RequestID) {
		problems = append(problems, errors.New("request_id is required"))
	}
	if c.Action != RecoverContinue && c.Action != RecoverFinishMerge {
		problems = append(problems, fmt.Errorf("action %q is not one the work session carries out", c.Action))
	}
	if c.RequestedAt.IsZero() {
		problems = append(problems, errors.New("requested_at is required"))
	}
	if c.TakenUpAt != nil && c.Refused != "" {
		problems = append(problems, errors.New("a continuation taken up was not refused"))
	}
	if (c.Refused == "") != (c.RefusedAt == nil) {
		problems = append(problems, errors.New("refused and refused_at are recorded together"))
	}
	return errors.Join(problems...)
}

// Outstanding reports a continuation nothing has taken up or refused yet.
func (c *RecoveryContinuation) Outstanding() bool {
	return c != nil && c.TakenUpAt == nil && c.Refused == ""
}

// RecoveryContinuationOutstanding reports a run whose record asks the work
// session for this action and has not been answered.
func (s State) RecoveryContinuationOutstanding(action RecoveryAction) bool {
	return s.RecoveryContinuation.Outstanding() && s.RecoveryContinuation.Action == action
}

// TakeUpRecoveryContinuation marks an outstanding continuation of this kind
// taken up, as the pipeline picks the run up for it. The caller saves the run.
func (s *State) TakeUpRecoveryContinuation(action RecoveryAction, at time.Time) bool {
	if !s.RecoveryContinuationOutstanding(action) {
		return false
	}
	at = at.UTC()
	s.RecoveryContinuation.TakenUpAt = &at
	return true
}

func (s State) validateRecovery() []error {
	var problems []error
	if len(s.RecoveryRequests) > MaxRecoveryRequests {
		problems = append(problems, fmt.Errorf("%d recovery requests are recorded, which exceeds the bound of %d", len(s.RecoveryRequests), MaxRecoveryRequests))
	}
	seen := make(map[string]bool, len(s.RecoveryRequests))
	for index, request := range s.RecoveryRequests {
		if err := request.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("recovery_requests[%d]: %w", index, err))
		}
		if seen[request.ID] {
			problems = append(problems, fmt.Errorf("recovery_requests[%d]: request %s is recorded twice", index, request.ID))
		}
		seen[request.ID] = true
	}
	if s.RecoveryHold != nil {
		if err := s.RecoveryHold.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("recovery_hold: %w", err))
		}
	}
	if s.RecoveryContinuation != nil {
		if err := s.RecoveryContinuation.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("recovery_continuation: %w", err))
		}
	}
	return problems
}
