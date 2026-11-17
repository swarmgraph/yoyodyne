package readmodel

// The checks run on the head of the target branch (internal/maincheck), as
// every surface reads them: when the branch last passed them, and what the
// latest run came to where that is anything else.

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// MainCheckRecord is the record of runs on the branch. It is satisfied by
// *runstate.MainCheckStore.
type MainCheckRecord interface {
	List() ([]runstate.MainCheckRun, error)
}

// MainChecks is where the branch stands with its checks.
type MainChecks struct {
	// LastPassed is the latest run that passed, and nil where none has.
	LastPassed *runstate.MainCheckRun `json:"last_passed,omitempty"`
	// Latest is the latest run, whatever it came to.
	Latest runstate.MainCheckRun `json:"latest"`
}

// ReadMainChecks reads the record. A product with no record wired, or one
// that has never run its checks on the branch, reads nil and says nothing; a
// record that cannot be read says so rather than reading as no runs.
func ReadMainChecks(record MainCheckRecord) (*MainChecks, string) {
	if record == nil {
		return nil, ""
	}
	runs, err := record.List()
	if err != nil {
		return nil, fmt.Sprintf("the record of checks on the branch could not be read: %v", err)
	}
	standing := runstate.ReadMainCheckStanding(runs)
	if standing.Latest == nil {
		return nil, ""
	}
	return &MainChecks{LastPassed: standing.LatestPassed, Latest: *standing.Latest}, ""
}

// RenderMainChecks is the checks on the branch as `yoyo status` prints them
// under the four lines: when the branch last passed, and the latest run where
// it did not pass.
func (s Standing) RenderMainChecks() string {
	if s.MainChecksProblem != "" {
		return "\nChecks on main: could not be read: " + s.MainChecksProblem + "\n"
	}
	if s.MainChecks == nil {
		return ""
	}
	checks := *s.MainChecks
	var text strings.Builder
	if passed := checks.LastPassed; passed != nil {
		fmt.Fprintf(&text, "\nChecks on main: last passed on %s at %s, in the run started at %s\n", passed.Branch, shortCommit(passed.Commit), runstate.LocalStamp(passed.StartedAt))
	} else {
		fmt.Fprintf(&text, "\nChecks on main: no run has passed yet\n")
	}
	latest := checks.Latest
	if latest.Outcome == runstate.MainCheckPassed {
		return text.String()
	}
	on := ""
	if latest.Commit != "" {
		on = " on " + shortCommit(latest.Commit)
	}
	fmt.Fprintf(&text, "  LATEST RUN %s%s, started at %s", strings.ToUpper(latest.Outcome.Words()), on, runstate.LocalStamp(latest.StartedAt))
	var failing []string
	for _, check := range latest.Checks {
		if check.Outcome == runstate.MainCheckFailed {
			named := check.Command
			if len(check.FailingTests) > 0 {
				named += " (" + strings.Join(check.FailingTests, ", ") + ")"
			}
			failing = append(failing, named)
		}
	}
	if len(failing) > 0 {
		fmt.Fprintf(&text, ": %s", strings.Join(failing, "; "))
	} else if latest.Detail != "" {
		fmt.Fprintf(&text, ": %s", latest.Detail)
	}
	text.WriteString("\n")
	return text.String()
}

func shortCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}
