package readmodel

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type mainCheckRecord struct {
	runs []runstate.MainCheckRun
	err  error
}

func (r mainCheckRecord) List() ([]runstate.MainCheckRun, error) { return r.runs, r.err }

func mainRun(at time.Time, commit string, outcome runstate.MainCheckOutcome, failing ...string) runstate.MainCheckRun {
	check := runstate.MainCheckResult{Command: "make race", Outcome: outcome, FailingTests: failing}
	return runstate.MainCheckRun{Branch: "main", Commit: commit, StartedAt: at, EndedAt: at, Outcome: outcome, Checks: []runstate.MainCheckResult{check}}
}

func TestStatusSaysWhenMainLastPassedItsChecks(t *testing.T) {
	at := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	passed := mainRun(at, strings.Repeat("a", 40), runstate.MainCheckPassed)

	checks, problem := ReadMainChecks(mainCheckRecord{runs: []runstate.MainCheckRun{passed}})
	said := Standing{MainChecks: checks, MainChecksProblem: problem}.RenderMainChecks()
	if !strings.Contains(said, "Checks on main: last passed on main at aaaaaaaaaaaa, in the run started at "+runstate.LocalStamp(at)) {
		t.Fatalf("status says:\n%s", said)
	}
	if strings.Contains(said, "LATEST RUN") {
		t.Fatalf("a passing latest run is said twice:\n%s", said)
	}

	failed := mainRun(at.Add(4*time.Hour), strings.Repeat("b", 40), runstate.MainCheckFailed, "TestQueue")
	checks, problem = ReadMainChecks(mainCheckRecord{runs: []runstate.MainCheckRun{passed, failed}})
	said = Standing{MainChecks: checks, MainChecksProblem: problem}.RenderMainChecks()
	for _, want := range []string{"last passed on main at aaaaaaaaaaaa", "LATEST RUN FAILED on bbbbbbbbbbbb", "make race (TestQueue)"} {
		if !strings.Contains(said, want) {
			t.Fatalf("status does not say %q:\n%s", want, said)
		}
	}
}

func TestStatusSaysNothingAboutMainChecksThatNeverRan(t *testing.T) {
	checks, problem := ReadMainChecks(mainCheckRecord{})
	if said := (Standing{MainChecks: checks, MainChecksProblem: problem}).RenderMainChecks(); said != "" {
		t.Fatalf("status says %q for a product that never ran its checks on main", said)
	}
	checks, problem = ReadMainChecks(mainCheckRecord{err: errors.New("disk on fire")})
	if said := (Standing{MainChecks: checks, MainChecksProblem: problem}).RenderMainChecks(); !strings.Contains(said, "could not be read") {
		t.Fatalf("an unreadable record reads as %q", said)
	}
}
