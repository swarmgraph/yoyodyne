package runstate

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ForgeWait is a run whose approved change is waiting at its merge step for
// GitHub to be reachable again. The step it was making — reading where the
// target branch stands, or asking GitHub to merge the pull request — could not
// reach GitHub at all, so the run keeps its change, its pull request and its
// approval, and asks again on a paced schedule until GitHub answers, then
// carries on from that step.
//
// It is the operator's rule that a run is never ended for a flaky network, and
// this is that rule at the merge step. Nothing is charged for the wait: no
// repair attempt, no review round, and nothing is put to the development
// manager, because what stopped the step was the network rather than the work.
// While it waits the run holds no developer slot (HoldsDeveloperSlot), because
// no developer is working on it and none will be.
//
// The wait is made by the process holding the run and is not resumed by a later
// one from this record alone: it is here so every surface can say what the run
// is waiting for and when it next tries. It is cleared once the step answers,
// whatever it answers.
type ForgeWait struct {
	// Step is what the run was doing when GitHub could not be reached, in the
	// words the run's landing uses for it.
	Step string `json:"step"`
	// Since is when the first try failed, and Tries how many have failed since.
	Since time.Time `json:"since"`
	Tries int       `json:"tries"`
	// NextTryAt is when the step is asked again.
	NextTryAt time.Time `json:"next_try_at"`
	// Failure is the last thing git or the GitHub client printed, bounded like
	// every other recorded failure.
	Failure string `json:"failure,omitempty"`
}

// Says is the wait in one line, for a person: that the run is waiting for GitHub
// to be reachable again, what it was doing, and when it next tries, in local
// time with the zone named.
func (w ForgeWait) Says() string {
	step := strings.TrimSpace(w.Step)
	if step == "" {
		step = "landing its change"
	}
	return fmt.Sprintf("waiting for GitHub to be reachable again: GitHub could not be reached while %s, %s since %s; next try at %s",
		step, triesSaid(w.Tries), localClock(w.Since), localClock(w.NextTryAt))
}

func triesSaid(tries int) string {
	if tries == 1 {
		return "1 try"
	}
	return fmt.Sprintf("%d tries", tries)
}

// localClock is a moment as the operator reads it: the time of day in local
// time with the zone named, and the date too where it is not today's.
func localClock(moment time.Time) string {
	local := moment.Local()
	if now := time.Now().Local(); local.Year() != now.Year() || local.YearDay() != now.YearDay() {
		return local.Format("2006-01-02 15:04 MST")
	}
	return local.Format("15:04 MST")
}

// Validate rejects a wait that cannot say when it began or when it next tries.
func (w ForgeWait) Validate() error {
	var problems []error
	if strings.TrimSpace(w.Step) == "" {
		problems = append(problems, errors.New("step is required"))
	}
	if len(w.Step) > MaxRecordedTextBytes {
		problems = append(problems, fmt.Errorf("step is %d bytes, which exceeds the %d byte bound", len(w.Step), MaxRecordedTextBytes))
	}
	if w.Since.IsZero() {
		problems = append(problems, errors.New("since is required"))
	}
	if w.NextTryAt.IsZero() {
		problems = append(problems, errors.New("next_try_at is required"))
	}
	if w.Tries <= 0 {
		problems = append(problems, errors.New("tries must name at least one try that failed"))
	}
	if len(w.Failure) > MaxRetryFailureBytes {
		problems = append(problems, fmt.Errorf("failure is %d bytes, which exceeds the %d byte bound", len(w.Failure), MaxRetryFailureBytes))
	}
	return errors.Join(problems...)
}
