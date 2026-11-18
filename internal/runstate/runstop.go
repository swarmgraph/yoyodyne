package runstate

// The stop record: one entry on the run for every time the run stops.
//
// Step 1 of the stopped-run recovery design (docs/designs/stopped-run-recovery.md)
// asks that every stop write exactly one stop on the run record, carrying the
// attempt, the stop time, the step, the candidate commit, whether the change
// is kept, the evidence, and one of three causes, and that the cause be
// decided when the run stops and changed by nothing later.
//
// The stop is written by the store rather than by each place that stops a
// run, because a run is stopped in some twenty places and every one of them
// ends by writing the record through save, Update or Create. The store compares
// the record being written with the one stored: a record that is stopped now,
// and was not stopped at the same stop time before, gets a new stop appended.
// The list itself is always the stored one, whatever the caller's copy holds,
// so no later write can change or drop a stop already recorded; a run that is
// picked up again and stops again has a new stop time and gets a new stop.

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// StopCause is which of step 1's three causes a stop is.
type StopCause string

const (
	// StopCauseWork is a stop about the change: a failing check, a reviewer's
	// repair verdict, a conflict the developer can resolve, a review refusal, a
	// budget spent on those, and any stop reason this build does not recognise.
	StopCauseWork StopCause = "work"
	// StopCauseOutside is a stop on step 1's fixed list of causes outside the
	// work, together with the harness's own failures around the run, which the
	// hold on new work still counts (stopsTheHoldCounts) but which judged
	// nothing about the change.
	StopCauseOutside StopCause = "outside"
	// StopCauseUncertain is a run whose change may have merged with nothing
	// confirming it (MergeDisagreement); only finishing the merge acts on it.
	StopCauseUncertain StopCause = "uncertain"
)

var stopCauses = []StopCause{StopCauseWork, StopCauseOutside, StopCauseUncertain}

// Valid reports one of the three causes.
func (c StopCause) Valid() bool { return slices.Contains(stopCauses, c) }

// Name is the wire name, for the docket, which cannot import this package.
func (c StopCause) Name() string { return string(c) }

// Describe says the cause in the words a person reads.
func (c StopCause) Describe() string {
	switch c {
	case StopCauseWork:
		return "about the work"
	case StopCauseOutside:
		return "outside the work"
	case StopCauseUncertain:
		return "uncertain: the change may have merged and nothing confirmed it"
	default:
		return "stop cause " + UnknownValueShown(string(c))
	}
}

// Charges reports whether a stop of this cause counts against the item's
// repair attempts and review rounds. Only a stop about the work does: one
// outside the work judged nothing about the change, and an uncertain one is
// for finishing the merge to settle.
func (c StopCause) Charges() bool { return c == StopCauseWork }

// StopCauseOf decides a stopped run's cause from the stop reason its record
// names, by the fixed lists in stopcause.go: a cause the hold on new work
// keeps out, or one StopSideOf names as outside the work, is outside it, and
// everything else — the change's own failures, the bounds spent on them, and a
// reason this build does not recognise — is about the work. A run whose merge
// is in doubt is uncertain whatever its reason. It is called once, as the stop
// is written; nothing reads a recorded stop's cause from here again.
func StopCauseOf(state State) StopCause {
	if state.MergeDisagreement != nil {
		return StopCauseUncertain
	}
	class := state.RecordedStopClass()
	if class == "" {
		class = StopUnknown
	}
	if StopKeptOutOfHoldAfter(class, state.Retries) {
		return StopCauseOutside
	}
	if side, listed := stopSideListed(class); listed && side == StopSideOutside {
		return StopCauseOutside
	}
	return StopCauseWork
}

const (
	// MaxRunStops bounds the stops one record keeps. A run that has stopped this
	// often keeps its latest stops and counts the ones it let go
	// (State.StopsDropped), so the record stays inside its size bound.
	MaxRunStops = 16
	// MaxStopFindings bounds the reviewer's findings one stop copies.
	MaxStopFindings = 20
	// MaxStopEvidenceBytes bounds each free-text piece of a stop's evidence.
	MaxStopEvidenceBytes = 4 << 10
	stopEvidenceCutNote  = "\n[cut; the run's own record carried the rest when it stopped]"
)

// RunStop is one stop of the run, written once when it stopped.
type RunStop struct {
	// Attempt is the attempt of the process that stopped the run, as its worker
	// identity numbers it; zero where the record carried no worker.
	Attempt   int       `json:"attempt"`
	StoppedAt time.Time `json:"stopped_at"`
	Step      Phase     `json:"step,omitempty"`
	// Candidate is the commit of the change the run had when it stopped: the
	// last commit the harness made, or the commit last reviewed where it made
	// none.
	Candidate string `json:"candidate,omitempty"`
	// Kept says the change was still on its branch, in its worktree, or on a
	// preserved reference when the run stopped.
	Kept bool `json:"kept"`
	// Reason is the stop reason the run recorded, and Cause the one of three
	// causes decided from it.
	Reason StopClass `json:"reason"`
	Cause  StopCause `json:"cause"`
	// Charged says the stop counts against the item's repair attempts and
	// review rounds (StopCause.Charges).
	Charged bool `json:"charged"`
	// Unrecognised says Reason is one this build does not recognise, which is
	// why it was decided as about the work. A reader is told so each time.
	Unrecognised bool         `json:"unrecognised,omitempty"`
	Evidence     StopEvidence `json:"evidence"`
}

// StopEvidence is what the run carried about why it stopped, copied as it
// stood at the stop, so a later attempt that clears the run's own fields does
// not take it away.
type StopEvidence struct {
	// Says is the failure or blocker the run recorded, in its own words.
	Says string `json:"says,omitempty"`
	// Check is the failing check, with its bounded output.
	CheckCommand  string `json:"check_command,omitempty"`
	CheckExitCode int    `json:"check_exit_code,omitempty"`
	CheckOutput   string `json:"check_output,omitempty"`
	// ReviewSummary and Findings are the reviewer's, including those of a
	// review a later failing check came after (CheckFailure.ReviewBefore).
	ReviewSummary   string    `json:"review_summary,omitempty"`
	Findings        []Finding `json:"findings,omitempty"`
	FindingsDropped int       `json:"findings_dropped,omitempty"`
	// Provider is how the AI provider's part ended: the harness's stop of it on
	// time, or the environment's refusal, with what it said.
	Provider string `json:"provider,omitempty"`
}

// stoppedNow reports a record that is a stop: ended, with a stop reason. A run
// that integrated and succeeded carries none.
func (s State) stoppedNow() bool {
	return s.Status.Terminal() && s.RecordedStopClass() != ""
}

// stopOf is the stop a stopped record writes.
func stopOf(state State) RunStop {
	class := state.RecordedStopClass()
	if class == "" {
		class = StopUnknown
	}
	cause := StopCauseOf(state)
	stop := RunStop{
		StoppedAt:    state.UpdatedAt.UTC(),
		Step:         state.Phase,
		Candidate:    state.HarnessCommit,
		Kept:         state.Artifacts().Preserved() || state.PreservedWorkRef != "",
		Reason:       class,
		Cause:        cause,
		Charged:      cause.Charges(),
		Unrecognised: !StopRecognised(class),
		Evidence:     stopEvidenceOf(state),
	}
	if state.CompletedAt != nil {
		stop.StoppedAt = state.CompletedAt.UTC()
	}
	if state.Worker != nil {
		stop.Attempt = state.Worker.Attempt
	}
	if stop.Candidate == "" {
		stop.Candidate = state.ReviewHeadCommit
	}
	return stop
}

func stopEvidenceOf(state State) StopEvidence {
	cut := func(text string) string {
		return boundRecordedText(strings.TrimSpace(text), MaxStopEvidenceBytes, stopEvidenceCutNote)
	}
	evidence := StopEvidence{Says: cut(state.Failure)}
	if evidence.Says == "" {
		evidence.Says = cut(state.Blocker)
	}
	findings, summary := state.ReviewFindingDetails, state.ReviewSummary
	if failure := state.CheckFailure; failure != nil {
		evidence.CheckCommand = cut(failure.Command)
		evidence.CheckExitCode = failure.ExitCode
		evidence.CheckOutput = cut(failure.Output)
		if before := failure.ReviewBefore; before != nil && len(findings) == 0 {
			findings, summary = before.Findings, before.Summary
		}
	}
	evidence.ReviewSummary = cut(summary)
	if len(findings) > MaxStopFindings {
		evidence.FindingsDropped = len(findings) - MaxStopFindings
		findings = findings[:MaxStopFindings]
	}
	evidence.Findings = slices.Clone(findings)
	switch {
	case state.Environmental != nil:
		evidence.Provider = cut(strings.TrimSpace(string(state.Environmental.Cause) + ": " + state.Environmental.Detail))
	case state.ProviderStop != "":
		evidence.Provider = cut(state.ProviderStop)
	}
	return evidence
}

// stopHistory is what the stored record says about the run's stops, which a
// write keeps rather than taking the caller's copy.
type stopHistory struct {
	stopped     bool
	completedAt *time.Time
	stops       []RunStop
	dropped     int
}

func stopHistoryOf(state State) stopHistory {
	return stopHistory{stopped: state.stoppedNow(), completedAt: state.CompletedAt, stops: state.Stops, dropped: state.StopsDropped}
}

// withStopRecorded is the record about to be written, carrying the stored
// stops and, where it stops the run afresh, one more. A record that was
// already stopped at the same stop time writes no new stop, which is what
// keeps the settling writes after a stop — a note, a cleanup, a reconcile —
// from deciding its cause again.
func (s State) withStopRecorded(prior stopHistory) State {
	s.Stops, s.StopsDropped = slices.Clone(prior.stops), prior.dropped
	if !s.stoppedNow() {
		return s
	}
	if prior.stopped && sameStopTime(prior.completedAt, s.CompletedAt) {
		return s
	}
	stop := stopOf(s)
	// One stop time is one stop, however the stored record's state was read.
	if latest := len(s.Stops) - 1; latest >= 0 && s.Stops[latest].StoppedAt.Equal(stop.StoppedAt) {
		return s
	}
	s.Stops = append(s.Stops, stop)
	if extra := len(s.Stops) - MaxRunStops; extra > 0 {
		s.Stops = slices.Clone(s.Stops[extra:])
		s.StopsDropped += extra
	}
	return s
}

func sameStopTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// LatestStop is the run's most recent stop, where it has one.
func (s State) LatestStop() (RunStop, bool) {
	if len(s.Stops) == 0 {
		return RunStop{}, false
	}
	return s.Stops[len(s.Stops)-1], true
}

// CurrentStop is the stop the run is stopped on now: its latest, where the run
// is stopped and that stop was written for this stop time. A run picked up
// again after its last stop has none.
func (s State) CurrentStop() (RunStop, bool) {
	stop, ok := s.LatestStop()
	if !ok || !s.stoppedNow() {
		return RunStop{}, false
	}
	if s.CompletedAt != nil && !stop.StoppedAt.Equal(*s.CompletedAt) {
		return RunStop{}, false
	}
	return stop, true
}

// Validate reports every contract violation in a recorded stop but an unknown
// cause, which State.Validate reports as an unknown value.
func (r RunStop) Validate() error {
	var problems []error
	if r.StoppedAt.IsZero() {
		problems = append(problems, errors.New("stopped_at is required"))
	}
	if r.Attempt < 0 {
		problems = append(problems, errors.New("attempt cannot be negative"))
	}
	if strings.TrimSpace(string(r.Reason)) == "" {
		problems = append(problems, errors.New("reason is required"))
	}
	// A cause this build does not know is State.Validate's to report, as an
	// unknown value a reading for showing can keep.
	if r.Cause.Valid() && r.Charged != r.Cause.Charges() {
		problems = append(problems, fmt.Errorf("charged is %t, and a stop %s is charged only when it is about the work", r.Charged, r.Cause.Describe()))
	}
	if r.Candidate != "" && !commitPattern.MatchString(r.Candidate) {
		problems = append(problems, fmt.Errorf("candidate %q is not a full commit id", r.Candidate))
	}
	if len(r.Evidence.Findings) > MaxStopFindings {
		problems = append(problems, fmt.Errorf("evidence carries %d findings, more than %d", len(r.Evidence.Findings), MaxStopFindings))
	}
	for index, finding := range r.Evidence.Findings {
		if err := finding.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("evidence finding %d: %w", index+1, err))
		}
	}
	for _, text := range []string{r.Evidence.Says, r.Evidence.CheckCommand, r.Evidence.CheckOutput, r.Evidence.ReviewSummary, r.Evidence.Provider} {
		if len(text) > MaxStopEvidenceBytes {
			problems = append(problems, fmt.Errorf("evidence text is %d bytes, more than %d", len(text), MaxStopEvidenceBytes))
		}
	}
	return errors.Join(problems...)
}
