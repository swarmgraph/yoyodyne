package runstate

// The operator's request that one run stop.
//
// Cancelling a run this process started is a context away, and that is all
// stopping ever needed while the operator started every run themselves. It is
// not enough once work runs without them: the run they want stopped is usually
// in another process, and this one has no handle on it. Killing that process is
// the verb they would otherwise be left with, and it is the wrong one — it
// leaves a claimed item, a worktree, and a branch that nothing accounts for.
//
// So a stop is asked for rather than performed. It is written beside the run,
// for the reason a released wait is: the process working on the run holds that
// run's state under a lease and is the only thing entitled to write it, so the
// operator states the fact in a file of their own and the run reads it. The run
// honors it at its next step boundary, and ends itself the way a cancelled run
// ends — terminal, artifacts preserved, the item noted — so what it leaves
// behind is settled by the same reconciliation that settles everything else.
//
// The development manager asks for a stop too, through the same file. A run
// whose work she has decided is superseded, narrowed, or mis-launched is one she
// records a "stop" triage decision about, and the harness writes the request
// on her behalf with who asked and the decision it carries out beside the
// reason. The run cannot tell the two apart and does not need to: it stops the
// same way, and only the words it ends on and the docket entry it leaves differ.
//
// It does not interrupt a provider invocation the moment it is recorded. That
// generation is already paid for, and throwing it away would leave the run
// needing the same work again, which is the cost that made killing processes the
// wrong verb in the first place. A stop therefore takes effect at the next step
// boundary rather than instantly, and whoever asked for it is told that it has
// not landed yet rather than told it has. A step that runs on past
// StopTakesEffectWithin is cut short all the same, and a stop that reaches a run
// only after its checks passed keeps the change for its review instead of
// cancelling it (LateStop, and internal/orchestrator/promptstop.go).

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// StopSchemaVersion is 1 and has never changed.
const StopSchemaVersion = 1

// MaxStopReasonBytes bounds the reason an operator gives. It is the same order
// as a selection's, and for the same reason: enough to say why, never enough to
// crowd out the run it is about.
const MaxStopReasonBytes = 4 << 10

// StopRequest is one operator statement that a run should stop. It names the
// work item as well as the run because that is what the operator typed, and a
// record nobody can trace back to the item it was about is a record nobody can
// read afterwards.
type StopRequest struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	RunID         string           `json:"run_id"`
	WorkItemID    string           `json:"work_item_id"`
	RequestedAt   time.Time        `json:"requested_at"`
	// Reason is what the operator said, and is optional: an operator who stops
	// something in a hurry owes nobody an explanation, and a stop with no reason
	// is still a stop.
	Reason string `json:"reason,omitempty"`
	// RequestedBy is who asked, in words a reader recognises, and is absent on the
	// operator's own stop — which is every request written before anybody else
	// could make one. A stop somebody other than the operator asked for names
	// them, because the run's record and the item's notes say who stopped it.
	RequestedBy string `json:"requested_by,omitempty"`
	// Decision is the triage decision this request carries out, and is set only
	// on a stop the development manager decided. It is what tells the run that
	// the stoppage it is about to leave has already been decided, so it is
	// docketed as settled rather than put back to her as a question.
	Decision string `json:"decision,omitempty"`
}

// MaxStopRequestedByBytes bounds who a stop names as having asked for it.
const MaxStopRequestedByBytes = 512

// StoppedBy is who a stop request names as having asked for it, which is the
// operator wherever nobody else is named.
func (r StopRequest) StoppedBy() string {
	if by := strings.TrimSpace(r.RequestedBy); by != "" {
		return by
	}
	return "the operator"
}

func (r StopRequest) Validate() error {
	var problems []error
	if r.SchemaVersion != StopSchemaVersion {
		problems = append(problems, fmt.Errorf("stop schema version %d is not supported", r.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(r.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if !runIDPattern.MatchString(r.RunID) {
		problems = append(problems, errors.New("run id is invalid"))
	}
	if strings.TrimSpace(r.WorkItemID) == "" {
		problems = append(problems, errors.New("work item id is required"))
	}
	if r.RequestedAt.IsZero() {
		problems = append(problems, errors.New("requested at is required"))
	}
	if len(r.Reason) > MaxStopReasonBytes {
		problems = append(problems, fmt.Errorf("stop reason is %d bytes, which exceeds the %d byte bound", len(r.Reason), MaxStopReasonBytes))
	}
	if len(r.RequestedBy) > MaxStopRequestedByBytes {
		problems = append(problems, fmt.Errorf("stop requester is %d bytes, which exceeds the %d byte bound", len(r.RequestedBy), MaxStopRequestedByBytes))
	}
	switch decision := strings.TrimSpace(r.Decision); {
	case decision == "":
	case decision != TriageDecisionStop:
		problems = append(problems, fmt.Errorf("a stop request carries out a %q decision or none, and this one names %q", TriageDecisionStop, decision))
	case strings.TrimSpace(r.RequestedBy) == "":
		problems = append(problems, errors.New("a stop request that carries out a decision names who decided it"))
	}
	return errors.Join(problems...)
}

// RecordStop makes one stop request durable. Asking twice is deliberately not an
// error: an operator who stops a run that has not given up yet means the same
// thing the second time, and refusing them would make the verb depend on timing
// they cannot see.
func (s *Store) RecordStop(request StopRequest) error {
	if request.ProductID != s.productID {
		return fmt.Errorf("stop product %q does not match store product %q", request.ProductID, s.productID)
	}
	if err := request.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create run state directory: %w", err)
	}
	path, err := s.stopPath(request.RunID)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(s.root, ".stop-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary stop request: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary stop request: %w", err)
	}
	if err := writeJSONFile(temporary, "stop request", request); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary stop request: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace stop request: %w", err)
	}
	return syncDirectory(s.root)
}

// StopRequested reports whether the operator has asked one run to stop. No
// record is the ordinary answer and means carry on, which is why it is reported
// as an absence rather than as a failure to look. A record that cannot be read is
// neither: it is reported as an error, because a stop nobody can read must never
// be spent through as though it were absent.
//
// It is never cleared. A request names one run, and a run identifier is used
// once, so a record left behind can only ever describe the run it stopped —
// which is exactly the evidence somebody reading that run afterwards wants.
func (s *Store) StopRequested(runID string) (StopRequest, bool, error) {
	path, err := s.stopPath(runID)
	if err != nil {
		return StopRequest{}, false, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return StopRequest{}, false, nil
	}
	if err != nil {
		return StopRequest{}, false, fmt.Errorf("open stop request: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var request StopRequest
	if err := decoder.Decode(&request); err != nil {
		return StopRequest{}, false, fmt.Errorf("decode stop request %s: %w", runID, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return StopRequest{}, false, fmt.Errorf("decode stop request %s: %w", runID, err)
	}
	if request.RunID != runID {
		return StopRequest{}, false, fmt.Errorf("stop request file %s belongs to run %s", runID, request.RunID)
	}
	if err := request.Validate(); err != nil {
		return StopRequest{}, false, err
	}
	return request, true, nil
}

// stopPath names where one run's stop request is recorded. It sits beside the
// run's own state rather than inside it because the working process holds that
// state's lease and the operator does not.
func (s *Store) stopPath(runID string) (string, error) {
	if !runIDPattern.MatchString(runID) {
		return "", errors.New("run id is invalid")
	}
	return filepath.Join(s.root, runID+".stop.json"), nil
}

// StopTakesEffectWithin is the longest a recorded stop waits for the step a run
// is in the middle of. A stop is honored at the run's next step boundary, which
// is usually soon; but a developer attempt or a check stage can run for an hour,
// and a stop that waited that long reached the run only once its checks had
// passed, which is the one moment stopping it throws away finished work. So the
// step in progress is let finish only within this bound of the stop being
// recorded, and is cut short once it passes.
const StopTakesEffectWithin = 10 * time.Minute

// StopBoundSays is StopTakesEffectWithin in the words a person reads it in,
// "10 minutes" rather than Go's "10m0s".
func StopBoundSays() string {
	return fmt.Sprintf("%d minutes", int(StopTakesEffectWithin/time.Minute))
}

// TakesEffectBy is the latest moment this stop takes effect on a run that is
// still working.
func (r StopRequest) TakesEffectBy() time.Time {
	return r.RequestedAt.Add(StopTakesEffectWithin)
}

// Pending says a stop that has been recorded and has not yet taken effect, for
// the status listing: who asked, when, in the operator's local time with the
// zone named, and when it will have taken effect at the latest.
func (r StopRequest) Pending() string {
	says := fmt.Sprintf("%s asked this run to stop at %s, and it has not taken effect yet; it takes effect at the run's next step, and no later than %s (%s after it was asked for), unless the run's checks have already passed, in which case the change is kept for its review",
		r.StoppedBy(), LocalStamp(r.RequestedAt), LocalStamp(r.TakesEffectBy()), StopBoundSays())
	if reason := strings.TrimSpace(r.Reason); reason != "" {
		says += ": " + reason
	}
	return says
}

// LateStop is a stop that reached a run only after its checks had passed on the
// change it holds. Such a stop is not carried out: cancelling there would throw
// away a change that has just earned its checks, which is the most expensive
// moment there is to stop a run. The change is kept and goes on to its review,
// and the run records the stop here so whoever asked for it is told it arrived
// late rather than finding the run carried on without a word.
type LateStop struct {
	RequestedAt time.Time `json:"requested_at"`
	// RequestedBy is who asked, as StopRequest.StoppedBy says it.
	RequestedBy string `json:"requested_by"`
	Reason      string `json:"reason,omitempty"`
	// ChecksPassedAt is when the checks passed on the change the stop would have
	// cancelled, and NoticedAt is when the run reached the stop.
	ChecksPassedAt time.Time `json:"checks_passed_at"`
	NoticedAt      time.Time `json:"noticed_at"`
}

// NewLateStop records request as reaching a run whose checks passed at
// checksPassedAt, noticed at noticedAt.
func NewLateStop(request StopRequest, checksPassedAt, noticedAt time.Time) LateStop {
	return LateStop{
		RequestedAt:    request.RequestedAt.UTC(),
		RequestedBy:    request.StoppedBy(),
		Reason:         strings.TrimSpace(request.Reason),
		ChecksPassedAt: checksPassedAt.UTC(),
		NoticedAt:      noticedAt.UTC(),
	}
}

func (l LateStop) Validate() error {
	var problems []error
	if l.RequestedAt.IsZero() {
		problems = append(problems, errors.New("requested at is required"))
	}
	if strings.TrimSpace(l.RequestedBy) == "" {
		problems = append(problems, errors.New("requested by is required"))
	}
	if l.NoticedAt.IsZero() {
		problems = append(problems, errors.New("noticed at is required"))
	}
	return errors.Join(problems...)
}

// Says is the late stop in a sentence, in the operator's local time.
func (l LateStop) Says() string {
	says := fmt.Sprintf("%s asked this run to stop at %s, but the stop reached the run only at %s, after its checks had passed at %s; the change was kept and went on to its review rather than being cancelled",
		l.RequestedBy, LocalStamp(l.RequestedAt), LocalStamp(l.NoticedAt), LocalStamp(l.ChecksPassedAt))
	if l.Reason != "" {
		says += " (the stop's reason: " + l.Reason + ")"
	}
	return says
}
