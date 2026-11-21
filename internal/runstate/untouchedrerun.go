package runstate

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/checks"
)

// The record of the one rerun a check stage may make of a failure confined to
// tests the change does not touch (checks.PlanUntouchedRerun). It is written
// before the rerun starts, so a harness that restarts part way through finds
// the rerun already spent for that stage and candidate commit, and it keeps
// the first failure with its output beside how the rerun ended.

// The check stages a rerun is counted in.
const (
	// UntouchedRerunBeforeReview is the run's checks over its change, before
	// the reviewer sees it.
	UntouchedRerunBeforeReview = "before review"
	// UntouchedRerunBeforePromotion is the run's checks over the merge
	// candidate, told the whole module, before it is promoted.
	UntouchedRerunBeforePromotion = "before promotion"
)

// UntouchedRerunOutcome is how a rerun ended. The empty value is a rerun
// recorded and not known to have ended: the harness stopped during it.
type UntouchedRerunOutcome string

const (
	// UntouchedRerunPassed is every rerun test passing: the check counts as
	// passed, and no repair attempt or review round is spent.
	UntouchedRerunPassed UntouchedRerunOutcome = "passed"
	// UntouchedRerunFailed is a rerun test failing again: the first failure is
	// an ordinary failed check.
	UntouchedRerunFailed UntouchedRerunOutcome = "failed"
	// UntouchedRerunUnfinished is a rerun stopped by its time limit, or one
	// that could not be started: the first failure is an ordinary failed check.
	UntouchedRerunUnfinished UntouchedRerunOutcome = "unfinished"
)

// MaxUntouchedRerunRecords bounds how many reruns a run record keeps, the
// latest last. Each stage gets one per candidate commit, so this holds every
// rerun of the last eight candidates.
const MaxUntouchedRerunRecords = 16

// UntouchedRerun is one rerun of a failure confined to untouched tests.
type UntouchedRerun struct {
	// Stage is the check stage it was made in, one of the UntouchedRerun stage
	// names, and Commit the candidate commit the check failed on. The two are
	// what the bound is counted by.
	Stage  string `json:"stage"`
	Commit string `json:"commit"`
	// Command is the check, as configured.
	Command string `json:"command"`
	// Tests are the failing tests run once more, each with its file.
	Tests []checks.FailingTest `json:"tests"`
	// FirstExitCode and FirstOutput are the first failure: its exit code and
	// its bounded output.
	FirstExitCode int       `json:"first_exit_code"`
	FirstOutput   string    `json:"first_output,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	// Outcome is how the rerun ended, empty until it has.
	Outcome UntouchedRerunOutcome `json:"outcome,omitempty"`
	EndedAt *time.Time            `json:"ended_at,omitempty"`
	// Output is the rerun's own bounded output, and Detail says in ordinary
	// words why a rerun that did not pass did not count.
	Output string `json:"output,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// UntouchedRerunsSpent is how many reruns this run's record holds for one
// stage and candidate commit, a rerun recorded and never finished included.
func (s State) UntouchedRerunsSpent(stage, commit string) int {
	spent := 0
	for _, rerun := range s.UntouchedReruns {
		if rerun.Stage == stage && rerun.Commit == commit {
			spent++
		}
	}
	return spent
}

// BeginUntouchedRerun adds a rerun to the record, dropping the oldest past
// the bound, and returns its index. The caller saves the record before the
// rerun starts.
func (s *State) BeginUntouchedRerun(rerun UntouchedRerun) int {
	s.UntouchedReruns = append(s.UntouchedReruns, rerun)
	if over := len(s.UntouchedReruns) - MaxUntouchedRerunRecords; over > 0 {
		s.UntouchedReruns = append([]UntouchedRerun(nil), s.UntouchedReruns[over:]...)
	}
	return len(s.UntouchedReruns) - 1
}

// PassedUntouchedReruns are the reruns on commit that passed, oldest first:
// the checks whose failing tests failed and then passed on the candidate the
// reviewer is shown.
func (s State) PassedUntouchedReruns(commit string) []UntouchedRerun {
	var passed []UntouchedRerun
	for _, rerun := range s.UntouchedReruns {
		if rerun.Commit == commit && rerun.Outcome == UntouchedRerunPassed {
			passed = append(passed, rerun)
		}
	}
	return passed
}

// TestNames is the rerun's tests, each with its file, in one line.
func (r UntouchedRerun) TestNames() string {
	parts := make([]string, 0, len(r.Tests))
	for _, test := range r.Tests {
		parts = append(parts, fmt.Sprintf("%s (%s)", test.Name, test.File))
	}
	return strings.Join(parts, ", ")
}

// Validate checks the values a reader decides by.
func (r UntouchedRerun) Validate() error {
	var problems []error
	switch r.Stage {
	case UntouchedRerunBeforeReview, UntouchedRerunBeforePromotion:
	default:
		problems = append(problems, fmt.Errorf("stage %s is not a check stage", UnknownValueShown(r.Stage)))
	}
	if !commitPattern.MatchString(r.Commit) {
		problems = append(problems, errors.New("commit is invalid"))
	}
	if strings.TrimSpace(r.Command) == "" {
		problems = append(problems, errors.New("command is required"))
	}
	if len(r.Tests) == 0 || len(r.Tests) > checks.MaxUntouchedFailingTests {
		problems = append(problems, fmt.Errorf("a rerun names between 1 and %d tests", checks.MaxUntouchedFailingTests))
	}
	if r.StartedAt.IsZero() {
		problems = append(problems, errors.New("started_at is required"))
	}
	switch r.Outcome {
	case "":
		if r.EndedAt != nil {
			problems = append(problems, errors.New("a rerun not known to have ended has no ended_at"))
		}
	case UntouchedRerunPassed, UntouchedRerunFailed, UntouchedRerunUnfinished:
		if r.EndedAt == nil {
			problems = append(problems, errors.New("a rerun that ended has an ended_at"))
		}
	default:
		problems = append(problems, fmt.Errorf("outcome %s is not how a rerun ends", UnknownValueShown(string(r.Outcome))))
	}
	return errors.Join(problems...)
}

// untouchedRerunTexts names the free text of the reruns.
func (s *State) untouchedRerunTexts(unstated func(key, path string, text *string, limit int)) {
	for index := range s.UntouchedReruns {
		rerun := &s.UntouchedReruns[index]
		at := fmt.Sprintf("untouched_reruns[%d]", index)
		unstated("untouched_reruns[].first_output", at+".first_output", &rerun.FirstOutput, MaxCheckOutputBytes)
		unstated("untouched_reruns[].output", at+".output", &rerun.Output, MaxCheckOutputBytes)
		unstated("untouched_reruns[].detail", at+".detail", &rerun.Detail, MaxRecordedTextBytes)
		for testIndex := range rerun.Tests {
			test := &rerun.Tests[testIndex]
			testAt := fmt.Sprintf("%s.tests[%d]", at, testIndex)
			unstated("untouched_reruns[].tests[].name", testAt+".name", &test.Name, MaxRecordedTextBytes)
			unstated("untouched_reruns[].tests[].file", testAt+".file", &test.File, MaxRecordedTextBytes)
		}
	}
}
