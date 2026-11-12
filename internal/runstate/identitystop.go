package runstate

// A run stopped while publishing its branch only because the harness could not
// reach GitHub as itself.
//
// Under automatic integration the run pushes its branch and opens its pull
// request before its checks run. When that push is refused because the harness
// has no GitHub identity right now (a key the remote refused, a login that is
// gone, a user the machine does not know), nothing is wrong with the change, and
// the record has to say so, or nothing can tell this stop from a push that failed
// for a reason in the work. The stop is written where the push fails, from the
// error that ended it. A later pass that finds GitHub answering again takes the
// run up at its checks with PublishOwed set, so the change is checked first and
// pushed after.

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// MaxIdentityTakeUps bounds how many times one run is taken up again after an
// identity stop. A run refused this many times is a machine someone has to
// look at; taking a run up refuses at the bound before it writes anything, and the
// record refuses past it, so the two say the same thing.
const MaxIdentityTakeUps = 8

// IdentityStop says the run stopped while publishing its branch only because the
// harness could not reach GitHub as itself.
type IdentityStop struct {
	// Detail is what refused, folded to one line. It is evidence for whoever reads
	// the record rather than a second classification of it.
	Detail     string    `json:"detail"`
	RecordedAt time.Time `json:"recorded_at"`
}

// Validate reports every contract violation in the record at once.
func (s IdentityStop) Validate() error {
	var problems []error
	if strings.TrimSpace(s.Detail) == "" {
		problems = append(problems, errors.New("detail is required"))
	}
	if len(s.Detail) > MaxEnvironmentalDetailBytes {
		problems = append(problems, fmt.Errorf("detail is %d bytes, which exceeds the %d byte bound", len(s.Detail), MaxEnvironmentalDetailBytes))
	}
	if s.RecordedAt.IsZero() {
		problems = append(problems, errors.New("recorded_at is required"))
	}
	return errors.Join(problems...)
}

// IdentityTakeUp is one time the harness took the run up again after an identity
// stop: why, when, and the failure it ended on. It is not an attempt; the repair
// count and the review evidence are untouched by it.
type IdentityTakeUp struct {
	Reason    string    `json:"reason"`
	TakenUpAt time.Time `json:"taken_up_at"`
	// SupersededFailure is what the run ended on, in the words it was recorded in.
	// Taking the run up clears the failure, and keeping the words here is what
	// stops the clearing losing the evidence of what stopped it.
	SupersededFailure string `json:"superseded_failure,omitempty"`
}

// Validate reports every contract violation in the record at once.
func (t IdentityTakeUp) Validate() error {
	var problems []error
	if strings.TrimSpace(t.Reason) == "" {
		problems = append(problems, errors.New("the reason this run was taken up again is required"))
	}
	if len(t.Reason) > MaxSelectionReasonBytes {
		problems = append(problems, fmt.Errorf("reason is %d bytes, which exceeds the %d byte bound", len(t.Reason), MaxSelectionReasonBytes))
	}
	if t.TakenUpAt.IsZero() {
		problems = append(problems, errors.New("taken_up_at is required"))
	}
	if len(t.SupersededFailure) > MaxBlockerBytes {
		problems = append(problems, fmt.Errorf("superseded_failure is %d bytes, which exceeds the %d byte bound", len(t.SupersededFailure), MaxBlockerBytes))
	}
	return errors.Join(problems...)
}

// IdentityTakeUpsLeft reports how many more times this run may be taken up after
// an identity stop before the bound above refuses it.
func (s State) IdentityTakeUpsLeft() int {
	if left := MaxIdentityTakeUps - len(s.IdentityTakeUps); left > 0 {
		return left
	}
	return 0
}

// validateIdentityStop reports every contract violation in the record's account
// of an identity stop and of each time it was taken up after it.
func (s State) validateIdentityStop() []error {
	var problems []error
	if s.IdentityStop != nil {
		if err := s.IdentityStop.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("identity_stop: %w", err))
		}
	}
	if len(s.IdentityTakeUps) > MaxIdentityTakeUps {
		problems = append(problems, fmt.Errorf("the run is recorded as taken up %d times after an identity stop, which exceeds the bound of %d", len(s.IdentityTakeUps), MaxIdentityTakeUps))
	}
	for index, takeUp := range s.IdentityTakeUps {
		if err := takeUp.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("identity_take_ups[%d]: %w", index, err))
		}
	}
	return problems
}

// TakenUpAtChecks is a run stopped by an identity stop made live again at its
// checks, owing the push it never made, which PublishOwed carries until the
// push is made after the checks pass. Nothing is counted: no attempt, no review
// round, no grant. The failure it ended on is kept on the record of taking it up rather than
// lost with the clearing.
func (s State) TakenUpAtChecks(reason string, now time.Time) State {
	live := s
	live.IdentityTakeUps = append(append([]IdentityTakeUp{}, s.IdentityTakeUps...), IdentityTakeUp{
		Reason:            reason,
		TakenUpAt:         now,
		SupersededFailure: s.Failure,
	})
	live.PublishOwed = true
	live.IdentityStop = nil
	live.Failure = ""
	live.Blocker = ""
	live.StopClass = ""
	live.Status = StatusRunning
	live.Phase = PhaseChecking
	live.CompletedAt = nil
	live.SettledQuietSince = nil
	live.WorktreeRemoved = false
	live.WorktreeSweptAt = nil
	live.BranchRemoved = false
	live.BranchSweptAt = nil
	live.UpdatedAt = now
	return live
}
