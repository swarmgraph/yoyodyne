package runstate

// A run that exited on its in-process usage-limit bound, and the sweep that
// continued it.
//
// A run waiting out a provider's refusal sleeps its probes inside one process
// until that process has spent execution.usage_limit_in_process_pause on it,
// and then exits with the run still in flight and its deadline recorded. Until
// yoyodyne-ifd.428.5 the only thing that continued such a run was somebody
// typing `yoyo run` on its item, and a run nobody typed it for had the shape
// of the phantom 428.4 settled for a provider stopped on time: no live process,
// no ending, a developer slot held, and the in-flight guard refusing every item
// beside it — indefinitely, because unlike the stopped provider nothing about
// it was wrong. The reconcile sweep now continues one itself once its recorded
// deadline has passed and nothing holds its lease, in the same worktree and
// developer session `yoyo run` would use, and this is what the run's record
// says about that.

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// MaxSweepContinuations bounds how many times one run's record says the sweep
// continued it. Nothing in the harness spends toward it — a continuation is the
// wait the run already committed to being served, charged to no budget of the
// item's — so it is the record's own bound: a provider that refuses the same
// run on sixty-four consecutive deadlines is a run the pause budget has stopped
// long before, and a state file must not grow past that on the sweep's account.
const MaxSweepContinuations = 64

// SweepContinuation is one continuation of this run by the reconcile sweep
// after the process serving its wait had exited on the in-process bound. It is
// the run's own account of who picked it up: the sweep, at what moment, out of
// which wait, and past which deadline — which is what tells a reader of the
// record that the run came back without a person rather than that somebody
// typed `yoyo run` for it.
type SweepContinuation struct {
	// Cause is the pause the run had exited on, one of PauseUsageLimit or
	// PauseServerOverload. The outage causes never appear here: an outage
	// wait has no in-process bound, so a run in one is not left behind by its
	// process the way this record describes.
	Cause string `json:"cause"`
	// Deadline is the recorded deadline that had passed when the sweep took the
	// run up, which is the condition the continuation was decided on.
	Deadline    time.Time `json:"deadline"`
	ContinuedAt time.Time `json:"continued_at"`
	// Reason is the sweep's own account of why it continued the run rather
	// than leaving it as the wait it was.
	Reason string `json:"reason"`
}

// Validate reports every contract violation in the record at once.
func (c SweepContinuation) Validate() error {
	var problems []error
	if c.Cause != PauseUsageLimit && c.Cause != PauseServerOverload {
		problems = append(problems, fmt.Errorf("cause %q is not a wait a process exits on its in-process bound from", c.Cause))
	}
	if c.Deadline.IsZero() {
		problems = append(problems, errors.New("deadline is required"))
	}
	if c.ContinuedAt.IsZero() {
		problems = append(problems, errors.New("continued_at is required"))
	}
	if strings.TrimSpace(c.Reason) == "" {
		problems = append(problems, errors.New("the reason the sweep continued this run is required"))
	}
	if len(c.Reason) > MaxSelectionReasonBytes {
		problems = append(problems, fmt.Errorf("reason is %d bytes, which exceeds the %d byte bound", len(c.Reason), MaxSelectionReasonBytes))
	}
	return errors.Join(problems...)
}

// validateSweepContinuations reports every contract violation in the record's
// account of the sweep having continued it.
func (s State) validateSweepContinuations() []error {
	var problems []error
	if len(s.SweepContinuations) > MaxSweepContinuations {
		problems = append(problems, fmt.Errorf("%d sweep continuations are recorded, which exceeds the bound of %d", len(s.SweepContinuations), MaxSweepContinuations))
	}
	for index, continuation := range s.SweepContinuations {
		if err := continuation.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("sweep_continuations[%d]: %w", index, err))
		}
	}
	return problems
}

// LastSweepContinuation is the most recent time the sweep continued this run,
// and whether it ever has.
func (s State) LastSweepContinuation() (SweepContinuation, bool) {
	if len(s.SweepContinuations) == 0 {
		return SweepContinuation{}, false
	}
	return s.SweepContinuations[len(s.SweepContinuations)-1], true
}
