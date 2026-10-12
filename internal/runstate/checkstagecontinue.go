package runstate

// A check the harness stopped on time, and the harness running it again.
//
// Two time limits can stop a check before it reaches a verdict:
// execution.check_stage_timeout, which bounds the whole stage, and
// execution.check_timeout, which bounds each check. Both are scaled for the
// machine's load by the same rule, and a check either one stopped is
// unfinished: it neither passed nor failed, so it earns the change no credit at
// the gate and hands the developer nothing to repair. What stops a check there
// is often the machine — three runs' race suites beside each other on one
// laptop — but load cannot prove the change did not make the check hang, so the
// reruns are bounded.
//
// The architect's ruling recorded on yoyodyne-ifd.429.41 sets the bounds: on
// one candidate, an unfinished check is run again from the start at most
// MaxCheckStageContinuations times, within CheckRerunAllowanceFactor times its
// own limit in total. Both bounds are shared by every path that runs it again —
// the harness's own continuation after either limit, and a continuation the
// development manager decided — and each rerun is written onto the run before
// it starts, so neither a restart, a redeploy, nor a change of path resets
// them. Lengthening a limit under load adds no reruns. When a bound is spent
// the change is kept with its branch, worktree, and developer session, and the
// stoppage is the development manager's, never a failure handed to the
// developer. A new candidate — another attempt or another commit — starts the
// bounds again. A check that fails outright is never run again by any of this.
//
// This is what the run's record says about that: the reruns it has had, the
// bounds on them, and the one sentence every surface says the stop in.

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// MaxCheckStageContinuations bounds how many times an unfinished check is run
// again on one candidate, by every path together. It is small on purpose: a
// check its limit stops again and again is either a suite that does not fit
// its limit or a change that makes it hang, and another try settles neither.
const MaxCheckStageContinuations = 2

// CheckRerunAllowanceFactor bounds the time an unfinished check may spend on
// one candidate, across its first run and every rerun: this many times its own
// limit as it stood when the check first stopped unfinished. A rerun is given
// no more of its limit than the allowance has left.
const CheckRerunAllowanceFactor = 3

// CheckStageContinuationWait is how long an eligible continuation may wait
// before the harness must record the remaining gate on its item.
const CheckStageContinuationWait = 30 * time.Minute

// CheckStageContinuation is one rerun of this run's checks after a time limit
// left one of them unfinished.
type CheckStageContinuation struct {
	// Command is the check the limit stopped.
	Command     string    `json:"command,omitempty"`
	ContinuedAt time.Time `json:"continued_at"`
	// Reason is the harness's own account of why the run is going again.
	Reason string `json:"reason"`
	// SupersededFailure is what the stopped run ended on, in the words it was
	// recorded in.
	SupersededFailure string `json:"superseded_failure,omitempty"`
	// CheckLimit reports a check stopped at its own execution.check_timeout
	// limit rather than at the stage's bound.
	CheckLimit bool `json:"check_limit,omitempty"`
	// ByDecision reports a rerun the development manager decided, carried out
	// as a repair continuation, rather than one the harness made by itself.
	ByDecision bool `json:"by_decision,omitempty"`
	// Attempt and Commit are the candidate the check was stopped on: the repair
	// attempt and the harness commit the checks were judging. Only reruns of the
	// candidate the run holds count against its bounds.
	Attempt int    `json:"attempt,omitempty"`
	Commit  string `json:"commit,omitempty"`
	// ElapsedSeconds is what the stopped check ran for, LimitSeconds the limit
	// it was given, and AllowanceSeconds the total the check may spend on this
	// candidate, fixed when it first stopped unfinished.
	ElapsedSeconds   int64 `json:"elapsed_seconds,omitempty"`
	LimitSeconds     int64 `json:"limit_seconds,omitempty"`
	AllowanceSeconds int64 `json:"allowance_seconds,omitempty"`
}

// Validate reports every contract violation in the record at once.
func (c CheckStageContinuation) Validate() error {
	var problems []error
	if c.ContinuedAt.IsZero() {
		problems = append(problems, errors.New("continued_at is required"))
	}
	if strings.TrimSpace(c.Reason) == "" {
		problems = append(problems, errors.New("the reason the harness continued this run is required"))
	}
	if len(c.Reason) > MaxSelectionReasonBytes {
		problems = append(problems, fmt.Errorf("reason is %d bytes, which exceeds the %d byte bound", len(c.Reason), MaxSelectionReasonBytes))
	}
	if len(c.SupersededFailure) > MaxBlockerBytes {
		problems = append(problems, fmt.Errorf("superseded_failure is %d bytes, which exceeds the %d byte bound", len(c.SupersededFailure), MaxBlockerBytes))
	}
	if c.Attempt < 0 || c.ElapsedSeconds < 0 || c.LimitSeconds < 0 || c.AllowanceSeconds < 0 {
		problems = append(problems, errors.New("attempt, elapsed_seconds, limit_seconds, and allowance_seconds cannot be negative"))
	}
	return errors.Join(problems...)
}

// recordedBeforeCandidates reports a continuation written before reruns were
// bound to a candidate. It names none, so it is counted against whichever
// candidate the run holds: a bound that cannot tell is kept rather than reset.
func (c CheckStageContinuation) recordedBeforeCandidates() bool {
	return c.Commit == "" && c.Attempt == 0 && c.LimitSeconds == 0 && c.ElapsedSeconds == 0
}

// onCandidate reports a rerun of the candidate this run holds now.
func (s State) onCandidate(c CheckStageContinuation) bool {
	return c.recordedBeforeCandidates() || (c.Attempt == s.RepairAttempts && c.Commit == s.HarnessCommit)
}

// CheckReruns are the reruns recorded against the candidate the run holds.
func (s State) CheckReruns() []CheckStageContinuation {
	var reruns []CheckStageContinuation
	for _, continuation := range s.CheckStageContinuations {
		if s.onCandidate(continuation) {
			reruns = append(reruns, continuation)
		}
	}
	return reruns
}

// UnfinishedCheck is the stop on the record, as the rerun that continues it
// will record it: which check, which limit, what it ran for, and on which
// candidate. It reports false for a run that did not stop on an unfinished
// check, and for one already going again, whose stop is already a rerun.
func (s State) UnfinishedCheck() (CheckStageContinuation, bool) {
	if !s.StoppedUnfinishedAtChecks() {
		return CheckStageContinuation{}, false
	}
	stop := CheckStageContinuation{
		Command:        s.CheckStage.Command,
		CheckLimit:     s.CheckStage.CheckStoppedAtLimit && !s.CheckStage.StoppedAtBound,
		Attempt:        s.RepairAttempts,
		Commit:         s.HarnessCommit,
		ElapsedSeconds: s.CheckStage.CheckElapsedSeconds,
		LimitSeconds:   s.CheckStage.CheckLimitSeconds,
	}
	_, stop.AllowanceSeconds = s.checkSpend(stop.Command, &stop)
	return stop, true
}

// checkSpend is what one check has spent unfinished on the candidate the run
// holds, in seconds, and the allowance it is held to: CheckRerunAllowanceFactor
// times the limit it had when it first stopped unfinished. An allowance of
// zero is one no record names a limit for. A stop not yet continued is counted
// where one is given.
func (s State) checkSpend(command string, stop *CheckStageContinuation) (spent, allowance int64) {
	entries := s.CheckReruns()
	if stop != nil {
		entries = append(entries, *stop)
	}
	for _, entry := range entries {
		if entry.Command != command {
			continue
		}
		spent += entry.ElapsedSeconds
		if allowance == 0 {
			if entry.AllowanceSeconds > 0 {
				allowance = entry.AllowanceSeconds
			} else if entry.LimitSeconds > 0 {
				allowance = CheckRerunAllowanceFactor * entry.LimitSeconds
			}
		}
	}
	return spent, allowance
}

// CheckAllowance is what a check has spent unfinished on the candidate the run
// holds, counting a stop of it not yet on the record — elapsed under limit —
// and the allowance it is held to. It is what a stop says as it is recorded.
func (s State) CheckAllowance(command string, elapsed, limit time.Duration) (spent, allowance time.Duration) {
	stop := CheckStageContinuation{Command: command, ElapsedSeconds: int64(elapsed / time.Second), LimitSeconds: int64(limit / time.Second)}
	spentSeconds, allowanceSeconds := s.checkSpend(command, &stop)
	return time.Duration(spentSeconds) * time.Second, time.Duration(allowanceSeconds) * time.Second
}

// CheckRerunRemaining is what a check may still spend on the candidate the run
// holds, and whether its allowance bounds it at all: a check that has never
// stopped unfinished on this candidate has no allowance yet, and is given its
// whole limit.
func (s State) CheckRerunRemaining(command string) (time.Duration, bool) {
	spent, allowance := s.checkSpend(command, nil)
	if allowance == 0 {
		return 0, false
	}
	remaining := allowance - spent
	if remaining < 0 {
		remaining = 0
	}
	return time.Duration(remaining) * time.Second, true
}

// CheckRerunSpent says why the check the run stopped on may not be run again
// on this candidate, and is empty where it may: its reruns are spent, or the
// time it has spent unfinished has reached its allowance.
func (s State) CheckRerunSpent() string {
	stop, ok := s.UnfinishedCheck()
	if !ok {
		return ""
	}
	if reruns := len(s.CheckReruns()); reruns >= MaxCheckStageContinuations {
		return fmt.Sprintf("its checks have already been run again %d times on this candidate, which is the bound every path shares", reruns)
	}
	spent, allowance := s.checkSpend(stop.Command, &stop)
	if allowance > 0 && spent >= allowance {
		return fmt.Sprintf("%s has spent %s unfinished on this candidate, which reaches its allowance of %s (%d times its %s limit)",
			stop.Command, describeSpan(time.Duration(spent)*time.Second), describeSpan(time.Duration(allowance)*time.Second),
			CheckRerunAllowanceFactor, describeSpan(time.Duration(allowance/CheckRerunAllowanceFactor)*time.Second))
	}
	return ""
}

// validateCheckStageContinuations reports every contract violation in the
// record's account of its checks being run again.
func (s State) validateCheckStageContinuations() []error {
	var problems []error
	if reruns := len(s.CheckReruns()); reruns > MaxCheckStageContinuations {
		problems = append(problems, fmt.Errorf("%d check reruns are recorded on this candidate, which exceeds the bound of %d", reruns, MaxCheckStageContinuations))
	}
	for index, continuation := range s.CheckStageContinuations {
		if err := continuation.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("check_stage_continuations[%d]: %w", index, err))
		}
	}
	if len(s.CheckStageContinuationRefused) > MaxBlockerBytes {
		problems = append(problems, fmt.Errorf("check_stage_continuation_refused is %d bytes, which exceeds the %d byte bound", len(s.CheckStageContinuationRefused), MaxBlockerBytes))
	}
	if len(s.CheckStageContinuationWaitNoted) > MaxBlockerBytes {
		problems = append(problems, fmt.Errorf("check_stage_continuation_wait_noted is %d bytes, which exceeds the %d byte bound", len(s.CheckStageContinuationWaitNoted), MaxBlockerBytes))
	}
	return problems
}

// StoppedUnfinishedAtChecks reports a run that ended because a time limit left
// a check unfinished: timed out, at its checks, with the stage on the record
// saying the stage's bound or the check's own limit is what ended it, and
// nothing promoted. Whatever else the record carries, nothing judged the change
// this round.
func (s State) StoppedUnfinishedAtChecks() bool {
	return s.Status == StatusTimedOut &&
		s.Phase == PhaseChecking &&
		s.CheckStage != nil && (s.CheckStage.StoppedAtBound || s.CheckStage.CheckStoppedAtLimit) &&
		s.Integration == nil
}

// HarnessContinuesCheckStage reports a run a time limit stopped at its checks
// that the harness will run again by itself: its branch, checkout and
// developer session are recorded and its artifacts have not been superseded,
// the check's reruns and allowance on this candidate are not spent, and no
// earlier continuation was refused for the development manager to decide.
// Repository checks at continuation verify the artifacts and may restore a
// missing checkout; removal flags alone cannot cancel the obligation. Whether
// it may go now — a free slot and the operator's switches — is the moment's to
// answer rather than the record's.
func (s State) HarnessContinuesCheckStage() bool {
	if !s.StoppedUnfinishedAtChecks() {
		return false
	}
	if s.WorktreePath == "" || s.Branch == "" || s.BaseCommit == "" || s.TargetBranch == "" {
		return false
	}
	if s.ArtifactsRetiredBy != "" || strings.TrimSpace(s.ProviderSessionID) == "" {
		return false
	}
	if strings.TrimSpace(s.CheckStageContinuationRefused) != "" {
		return false
	}
	return s.CheckRerunSpent() == ""
}

// CheckStageStopSays is what every surface says about a run a time limit
// stopped at its checks: that the check is unfinished rather than failed, and
// what happens next. It is empty for every other run.
func (s State) CheckStageStopSays() string {
	if !s.StoppedUnfinishedAtChecks() {
		return ""
	}
	stopped := "the check stage was stopped by load at its execution.check_stage_timeout bound, not by the change: nothing was judged and nothing was handed back to the developer"
	if !s.CheckStage.StoppedAtBound {
		stopped = fmt.Sprintf("%s reached its own execution.check_timeout limit of %s and is unfinished, neither passed nor failed: nothing was judged and nothing was handed back to the developer",
			nonEmptyCommand(s.CheckStage.Command), describeSpan(time.Duration(s.CheckStage.CheckLimitSeconds)*time.Second))
	}
	if s.HarnessContinuesCheckStage() {
		return fmt.Sprintf(
			"%s; the harness continues it itself, re-running the checks on the change the run already has, on the same branch and worktree, at the next pull with a developer slot free, ahead of fresh work at equal or lower priority, without waiting on machine load; after 30 minutes waiting, the item names any remaining gate and what clears it — no developer is invoked and no review round, repair grant, or re-run is spent (rerun %d of %d on this candidate)",
			stopped, len(s.CheckReruns())+1, MaxCheckStageContinuations)
	}
	if refused := strings.TrimSpace(s.CheckStageContinuationRefused); refused != "" {
		return fmt.Sprintf("%s; the harness's continuation of it was refused — %s — so what happens to it next is the development manager's decision", stopped, refused)
	}
	if spent := s.CheckRerunSpent(); spent != "" {
		return fmt.Sprintf("%s; %s, so the check did not finish and what kept it from finishing is unresolved: the change, branch, worktree, and developer session are kept, and what happens to it next is the development manager's decision", stopped, spent)
	}
	return stopped + "; its recorded branch, checkout, or developer session is unavailable, or its work has been superseded, so what happens to it next is the development manager's decision"
}

func nonEmptyCommand(command string) string {
	if strings.TrimSpace(command) == "" {
		return "a check"
	}
	return command
}
