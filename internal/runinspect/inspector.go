package runinspect

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// Runs is the run store an inspector reads. *runstate.Store is one.
type Runs interface {
	Read(runID string) (runstate.State, error)
	Holder(runID string) (*runstate.LeaseHolder, error)
	Worker(state runstate.State, now time.Time) runstate.WorkerReading
	Incomplete() ([]runstate.State, error)
}

// Inspector answers both tools for one product.
type Inspector struct {
	Runs Runs
	// Remains is what is asked whether a run's branch and worktree are there;
	// nil reports what the record says, marked as not checked.
	Remains readmodel.Remains
	// RedactValues are removed from everything handed back.
	RedactValues []string
	Now          func() time.Time
}

func (i Inspector) now() time.Time {
	if i.Now == nil {
		return time.Now().UTC()
	}
	return i.Now().UTC()
}

// Inspection is one run as it stood when it was read.
type Inspection struct {
	ObservedAt time.Time `json:"observed_at"`
	// Record is the run record as JSON, cut at MaxRecordBytes where it is
	// longer; RecordCut declares the cut and RecordBytes is the whole size.
	Record      string `json:"record,omitempty"`
	RecordBytes int    `json:"record_bytes"`
	RecordCut   bool   `json:"record_cut"`
	// RecordProblem is why the record could not be read, and every answer below
	// that depends on it is then missing or uncertain.
	RecordProblem string          `json:"record_problem,omitempty"`
	Status        runstate.Status `json:"status,omitempty"`
	Phase         runstate.Phase  `json:"phase,omitempty"`
	// LeaseHolder is the stamp beside the run's lease, absent where nothing
	// holds it; LeaseHolderProblem is why it could not be read.
	LeaseHolder        *runstate.LeaseHolder  `json:"lease_holder,omitempty"`
	LeaseHolderProblem string                 `json:"lease_holder_problem,omitempty"`
	Worker             runstate.WorkerReading `json:"worker"`
	// Remains is whether the run's branch and worktree exist, as the repository
	// was asked.
	Remains          *triage.Found `json:"remains,omitempty"`
	Reservation      *Reservation  `json:"reservation,omitempty"`
	Wait             *Wait         `json:"wait,omitempty"`
	LastContinuation *Continuation `json:"last_continuation,omitempty"`
	LastRefusal      *Refusal      `json:"last_refusal,omitempty"`
}

// Reservation is what the run holds that another run could otherwise take: a
// run in flight holds its work item, and a developer slot or a document
// landing unless it is paused on work its item waits on.
type Reservation struct {
	Held            bool   `json:"held"`
	DeveloperSlot   bool   `json:"developer_slot"`
	DocumentLanding bool   `json:"document_landing"`
	Says            string `json:"says"`
}

// Wait is what a run in flight is parked on and what releases it.
type Wait struct {
	Kind    string     `json:"kind"`
	Since   *time.Time `json:"since,omitempty"`
	Release string     `json:"release"`
}

// Continuation is one time the harness took a stopped run up again.
type Continuation struct {
	Kind   string    `json:"kind"`
	At     time.Time `json:"at"`
	Reason string    `json:"reason,omitempty"`
	// Pending is a continuation recorded and not yet accepted by the pipeline.
	Pending bool `json:"pending,omitempty"`
}

// Refusal is the harness declining to continue a run by itself.
type Refusal struct {
	Kind string `json:"kind"`
	Says string `json:"says"`
}

// Inspect reads one run. It never fails: what could not be read is said where
// the answer would have been.
func (i Inspector) Inspect(ctx context.Context, runID string) Inspection {
	now := i.now()
	inspection := Inspection{ObservedAt: now}
	holder, err := i.Runs.Holder(runID)
	inspection.LeaseHolder = holder
	if err != nil {
		inspection.LeaseHolderProblem = err.Error()
	}
	state, err := i.Runs.Read(runID)
	if err != nil {
		inspection.RecordProblem = err.Error()
		inspection.Worker = runstate.WorkerReading{Liveness: runstate.WorkerUncertain, Says: "the run record could not be read, so its worker cannot be named", ObservedAt: now}
		return i.redactInspection(inspection)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		inspection.RecordProblem = fmt.Sprintf("encode the run record: %v", err)
	} else {
		inspection.RecordBytes = len(encoded)
		inspection.Record, inspection.RecordCut = cut(string(encoded), MaxRecordBytes)
	}
	inspection.Status, inspection.Phase = state.Status, state.Phase
	inspection.Worker = i.Runs.Worker(state, now)
	found := readmodel.Looking(ctx, i.Remains, func() time.Time { return now })(state)
	inspection.Remains = &found
	inspection.Reservation = reservationOf(state)
	inspection.Wait = WaitOf(state)
	inspection.LastContinuation = lastContinuation(state)
	inspection.LastRefusal = lastRefusal(state)
	return i.redactInspection(inspection)
}

func (i Inspector) redactInspection(inspection Inspection) Inspection {
	redactor := execution.NewRedactor(i.RedactValues...)
	inspection.Record = redactor.Redact(inspection.Record)
	inspection.RecordProblem = redactor.Redact(inspection.RecordProblem)
	inspection.LeaseHolderProblem = redactor.Redact(inspection.LeaseHolderProblem)
	inspection.Worker.Says = redactor.Redact(inspection.Worker.Says)
	if inspection.Remains != nil {
		inspection.Remains.Unchecked = redactor.Redact(inspection.Remains.Unchecked)
	}
	if inspection.Wait != nil {
		inspection.Wait.Release = redactor.Redact(inspection.Wait.Release)
	}
	if inspection.LastContinuation != nil {
		inspection.LastContinuation.Reason = redactor.Redact(inspection.LastContinuation.Reason)
	}
	if inspection.LastRefusal != nil {
		inspection.LastRefusal.Says = redactor.Redact(inspection.LastRefusal.Says)
	}
	return inspection
}

// cut keeps at most limit bytes of text, ending on a whole character.
func cut(text string, limit int) (string, bool) {
	if len(text) <= limit {
		return text, false
	}
	end := limit
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end], true
}

func reservationOf(state runstate.State) *Reservation {
	reservation := &Reservation{Held: state.Status.InFlight(), DeveloperSlot: state.HoldsDeveloperSlot(), DocumentLanding: state.HoldsDocumentLanding()}
	switch {
	case reservation.DeveloperSlot:
		reservation.Says = "the run is in flight: it holds its work item and a developer slot"
	case reservation.DocumentLanding:
		reservation.Says = "the run is in flight: it holds its work item and a document landing"
	case reservation.Held:
		reservation.Says = "the run is in flight and holds its work item, but no developer slot while it waits on work its item depends on"
	default:
		reservation.Says = fmt.Sprintf("the run is %s and holds nothing another run could take", state.Status)
	}
	return reservation
}

// WaitOf is the wait a run in flight records, in the order the sweep reads
// them (DeadRunRemedy in internal/runstate).
func WaitOf(state runstate.State) *Wait {
	if !state.Status.InFlight() {
		return nil
	}
	switch {
	case state.OperatorHeldSince != nil:
		return &Wait{Kind: "the operator's pause", Since: state.OperatorHeldSince, Release: "the operator lifting the pause"}
	case state.DependencyPause != nil:
		return &Wait{Kind: "work its item waits on", Release: fmt.Sprintf("that work closing (%s)", state.DependencyPause.Summary())}
	case state.UsageLimitResetsAt != nil || state.UsageLimitResetUnknown:
		wait := &Wait{Kind: runstate.DescribePause(state.PauseCause, state.UsageLimitKind), Since: state.UsageLimitPausedSince}
		if state.UsageLimitResetsAt != nil {
			wait.Release = "the time passing: " + state.UsageLimitResetsAt.UTC().Format(time.RFC3339)
		} else {
			wait.Release = "the provider accepting work again; no time was given"
		}
		return wait
	case state.DirectivePause != nil:
		return &Wait{Kind: "a directive", Release: fmt.Sprintf("directive %s being resolved: %s", state.DirectivePause.DirectiveID, state.DirectivePause.Unresolved)}
	case state.TrackerPause != nil:
		return &Wait{Kind: "the tracker not answering", Release: "the tracker answering " + state.TrackerPause.Boundary}
	}
	return nil
}

// continuations are every continuation the record names, oldest first.
func continuations(state runstate.State) []Continuation {
	var all []Continuation
	for _, c := range state.SweepContinuations {
		all = append(all, Continuation{Kind: "the sweep continued a wait it served", At: c.ContinuedAt, Reason: c.Reason})
	}
	for _, c := range state.CheckStageContinuations {
		all = append(all, Continuation{Kind: "continued at its checks", At: c.ContinuedAt, Reason: c.Reason})
	}
	for _, c := range state.IntegrationResumptions {
		all = append(all, Continuation{Kind: "integration resumed", At: c.ResumedAt, Reason: c.Reason})
	}
	for _, c := range state.RepairContinuations {
		all = append(all, Continuation{Kind: "repair", At: c.ContinuedAt, Reason: c.Reason, Pending: c.DispatchPending})
	}
	sort.SliceStable(all, func(a, b int) bool { return all[a].At.Before(all[b].At) })
	return all
}

func lastContinuation(state runstate.State) *Continuation {
	all := continuations(state)
	if len(all) == 0 {
		return nil
	}
	return &all[len(all)-1]
}

func lastRefusal(state runstate.State) *Refusal {
	switch {
	case state.CheckStageContinuationRefused != "":
		return &Refusal{Kind: "continuing at its checks", Says: state.CheckStageContinuationRefused}
	case state.StallContinuationRefused != "":
		return &Refusal{Kind: "continuing after a silent stream", Says: state.StallContinuationRefused}
	}
	return nil
}
