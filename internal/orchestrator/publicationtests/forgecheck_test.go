package publicationtests

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/publish"
)

// forgeCheck is one check of the forge over the fixture's run, as `yoyo
// forge-check` makes it.
func (f personMergeFixture) forgeCheck(t *testing.T, forge orchestrator.ReconcilePullRequests, feedback orchestrator.ReconcileFeedback) orchestrator.ForgeCheck {
	t.Helper()
	check, err := orchestrator.Reconciler{
		Tracker:   f.tracker,
		Worktrees: testharness.NewObserver(t, f.repository, f.worktreeRoot),
		Store:     f.store,
		Publisher: forge,
		Closers:   &closers{},
		Feedback:  feedback,
		Reports:   &collectedReports{},
		// A backoff would take real time; a check never waits one out, so a test
		// that reached it would hang rather than pass.
		Sleep: func(context.Context, time.Duration) error {
			t.Fatal("the forge check waited out a failed write inside the check")
			return nil
		},
	}.CheckForge(context.Background())
	if err != nil {
		t.Fatalf("CheckForge() error = %v", err)
	}
	return check
}

// A check that cannot reach the forge changes nothing: the item is not
// touched, the run goes on waiting with its record as it was, and what failed
// is said. The next check, with the forge back, settles it.
func TestAForgeCheckThatCannotReachTheForgeChangesNothing(t *testing.T) {
	t.Parallel()
	fixture := newPersonMergeFixture(t)
	run := fixture.run
	calls := len(fixture.tracker.Calls)
	unreachable := errors.New(`Get "https://api.github.com/graphql": dial tcp: lookup api.github.com: no such host`)

	check := fixture.forgeCheck(t, &orchestratortest.AnsweringForge{Err: unreachable}, &feedbackReader{err: unreachable})
	if len(check.PersonMerges) != 1 || check.PersonMerges[0].Settled || !strings.Contains(check.PersonMerges[0].Failure, "no such host") {
		t.Fatalf("person merges = %#v, want the request's read failing and nothing settled", check.PersonMerges)
	}
	if len(check.Feedback) != 1 || !strings.Contains(check.Feedback[0].Failure, "no such host") {
		t.Fatalf("feedback = %#v, want the read failing", check.Feedback)
	}
	if check.Failures() != 2 {
		t.Fatalf("failures = %d, want both reads counted", check.Failures())
	}
	if len(fixture.tracker.Calls) != calls {
		t.Fatalf("the check made %v on the work item with the forge unreachable", fixture.tracker.Calls[calls:])
	}
	after := testharness.LoadRun(t, fixture.store, run.RunID)
	if !after.WaitingForPersonMerge() || !after.UpdatedAt.Equal(run.UpdatedAt) {
		t.Fatalf("run = %#v, want it still waiting and its record untouched", after.PersonMerge)
	}

	testharness.RunPipelineGit(t, fixture.repository, "push", "origin", run.PullRequest.HeadCommit+":refs/heads/main")
	merged := &orchestratortest.AnsweringForge{Answer: publish.PullRequest{
		Number: run.PullRequest.Number, URL: run.PullRequest.URL, State: "MERGED", Merged: true, MergeCommit: run.PullRequest.HeadCommit,
	}}
	next := fixture.forgeCheck(t, merged, &feedbackReader{})
	if len(next.PersonMerges) != 1 || !next.PersonMerges[0].Settled || next.Failures() != 0 {
		t.Fatalf("next check = %#v, want the merge settled once the forge answers", next)
	}
	if !fixture.tracker.Closed {
		t.Fatal("the item was left open after the forge came back")
	}
}

// A pull request that merged while the tracker write closing its item timed
// out has its item closed at the next check, with one note: whether the write
// had not gone through or had gone through before it timed out. The failed
// write is not waited out inside the check, and the run goes on waiting until
// the item is closed.
func TestAMergeWhoseClosingWriteTimedOutIsClosedAtTheNextCheckWithOneNote(t *testing.T) {
	t.Parallel()
	for _, landed := range []bool{false, true} {
		fixture := newPersonMergeFixture(t)
		run := fixture.run
		testharness.RunPipelineGit(t, fixture.repository, "push", "origin", run.PullRequest.HeadCommit+":refs/heads/main")
		forge := &orchestratortest.AnsweringForge{Answer: publish.PullRequest{
			Number: run.PullRequest.Number, URL: run.PullRequest.URL, State: "MERGED", Merged: true, MergeCommit: run.PullRequest.HeadCommit,
		}}
		fixture.tracker.CompleteFailures = 1
		fixture.tracker.TransientCompleteErr = errors.New("bd close on yoyodyne-task did not answer within its 30s bound, and whether the write landed is not known: bd close failed with status timed_out and exit code -1")
		fixture.tracker.LandsBeforeFailing = landed

		first := fixture.forgeCheck(t, forge, &feedbackReader{})
		if len(first.PersonMerges) != 1 || first.PersonMerges[0].Settled || !strings.Contains(first.PersonMerges[0].Failure, "timed_out") {
			t.Fatalf("landed %t: first check = %#v, want the closing write's timeout said and nothing marked settled", landed, first.PersonMerges)
		}
		if completes := strings.Count(strings.Join(fixture.tracker.Calls, " "), "complete"); completes != 1 {
			t.Fatalf("landed %t: the first check asked to close the item %d times, want once and no retry inside the check", landed, completes)
		}
		if waiting := testharness.LoadRun(t, fixture.store, run.RunID); !waiting.WaitingForPersonMerge() {
			t.Fatalf("landed %t: run = %#v, want it still waiting, so the next check reads the merge again", landed, waiting.PersonMerge)
		}

		second := fixture.forgeCheck(t, forge, &feedbackReader{})
		if len(second.PersonMerges) != 1 || !second.PersonMerges[0].Settled || second.Failures() != 0 {
			t.Fatalf("landed %t: second check = %#v, want the merge settled", landed, second.PersonMerges)
		}
		if !fixture.tracker.Closed {
			t.Fatalf("landed %t: the item was left open", landed)
		}
		headline := "was merged by a person, so the change this run published has landed"
		if notes := strings.Count(fixture.tracker.Item.Notes, headline); notes != 1 {
			t.Fatalf("landed %t: the item carries the merge's note %d times, want once:\n%s", landed, notes, fixture.tracker.Item.Notes)
		}
		completes := strings.Count(strings.Join(fixture.tracker.Calls, " "), "complete")
		if want := map[bool]int{false: 2, true: 1}[landed]; completes != want {
			t.Fatalf("landed %t: the item was asked to close %d times, want %d: a closure that went through is not asked for again", landed, completes, want)
		}
		after := testharness.LoadRun(t, fixture.store, run.RunID)
		if after.WaitingForPersonMerge() || after.PersonMerge == nil || !after.PersonMerge.Merged() {
			t.Fatalf("landed %t: wait = %#v, want it ended on the merge", landed, after.PersonMerge)
		}
		if third := fixture.forgeCheck(t, forge, &feedbackReader{}); len(third.PersonMerges) != 0 {
			t.Fatalf("landed %t: third check = %#v, want nothing left waiting", landed, third.PersonMerges)
		}
	}
}
