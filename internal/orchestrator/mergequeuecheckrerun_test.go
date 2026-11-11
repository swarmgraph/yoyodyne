package orchestrator

// A check on the queue's candidate pull request that a job's own time limit
// cancelled judged nothing about the change, and the forge holds the merge for
// good behind it. The queue takes that as an answer: it has the check run
// again once at once, and when it ends that way again, or cannot be run again,
// it withdraws the merge at once rather than waiting out the stuck-merge
// limit.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// candidateChecks is a forge whose candidate pull request reads as given, and
// that records each check it was asked to run again.
type candidateChecks struct {
	reading publish.CheckReading
	refuse  error
	reruns  []int64
}

func (c *candidateChecks) Checks(context.Context, int, string) (publish.CheckReading, error) {
	return c.reading, nil
}

func (c *candidateChecks) RerunCheck(_ context.Context, checkRun int64) error {
	if c.refuse != nil {
		return c.refuse
	}
	c.reruns = append(c.reruns, checkRun)
	return nil
}

// cancelledBuild is the case of pull request #1697: the build job cancelled by
// its own time limit on its last step, everything else passed.
func cancelledBuild() publish.CheckReading {
	return publish.CheckReading{Passing: 3, Failing: []publish.FailedCheck{{Name: "build", ID: 77, Conclusion: "cancelled"}}}
}

func (f *promotionFixture) passNow() runstate.MergeQueuePass {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return f.driver().Pass(ctx, queueKey)
}

func TestAQueuedCandidatesCancelledCheckIsRunAgainAndThenWithdrawnAtOnce(t *testing.T) {
	t.Parallel()

	f, withdrawer, _ := queuedMergeFixture(t)
	checks := &candidateChecks{reading: cancelledBuild()}
	repair := &recordingRepair{}
	f.promoter.Checks, f.promoter.Reruns, f.promoter.Repair = checks, f.queue, repair

	// The first cancelled build is run again at once, and the pass says so.
	pass := f.passNow()
	if pass.Outcome != runstate.MergeQueuePassWaiting || !strings.Contains(pass.Says, "asked the forge to run it again") || !strings.Contains(pass.Says, "build") {
		t.Fatalf("Pass() = %#v, want it waiting on the build it had run again", pass)
	}
	if len(checks.reruns) != 1 || checks.reruns[0] != 77 || withdrawer.count() != 0 {
		t.Fatalf("re-runs %v and %d withdrawals, want the build run again once and nothing withdrawn", checks.reruns, withdrawer.count())
	}
	if recorded, err := f.queue.CheckReruns(queueKey, f.entry.EntryID); err != nil || len(recorded) != 1 || recorded[0].Check != "build" {
		t.Fatalf("recorded re-runs = %#v, %v; want the build's", recorded, err)
	}
	// Recorded by the queue for status and the dashboard to read.
	if last, found, err := f.queue.LastPass(queueKey); err != nil || !found || last.Says != pass.Says {
		t.Fatalf("last pass = %#v, %t, %v; want what this pass found recorded", last, found, err)
	}

	// Cancelled again after its re-run: the merge is withdrawn at once, well
	// inside the stuck-merge limit, and the change handed back saying why.
	pass = f.passNow()
	if pass.Outcome != runstate.MergeQueuePassHandedBack {
		t.Fatalf("Pass() = %#v, want the merge withdrawn and the change handed back", pass)
	}
	if len(checks.reruns) != 1 || withdrawer.count() != 1 {
		t.Fatalf("re-runs %v and %d withdrawals, want no second re-run and one withdrawal", checks.reruns, withdrawer.count())
	}
	landing := f.landing()
	if landing.Handback == nil || !strings.Contains(landing.Handback.Reason, "again after the queue had it run again") || repair.count() != 1 {
		t.Fatalf("handback = %#v, handed back %d times; want the change handed back naming the cancelled build", landing.Handback, repair.count())
	}
}

func TestAQueuedCandidatesCancelledCheckTheForgeWillNotRunAgainIsWithdrawnAtOnce(t *testing.T) {
	t.Parallel()

	f, withdrawer, _ := queuedMergeFixture(t)
	checks := &candidateChecks{reading: cancelledBuild(), refuse: errors.New("HTTP 500")}
	f.promoter.Checks, f.promoter.Reruns, f.promoter.Repair = checks, f.queue, &recordingRepair{}
	if pass := f.passNow(); pass.Outcome != runstate.MergeQueuePassHandedBack || withdrawer.count() != 1 {
		t.Fatalf("Pass() = %#v with %d withdrawals, want the merge withdrawn at once", pass, withdrawer.count())
	}
	if landing := f.landing(); landing.Handback == nil || !strings.Contains(landing.Handback.Reason, "would not run it again") {
		t.Fatalf("handback = %#v, want it to say the forge would not run the build again", landing.Handback)
	}
}

func TestAQueuedCandidateIsNotWithdrawnWhileAnotherCheckIsStillRunning(t *testing.T) {
	t.Parallel()

	f, withdrawer, _ := queuedMergeFixture(t)
	reading := cancelledBuild()
	reading.Failing[0].ID = 0 // nothing to run again
	reading.Pending = []string{"race"}
	checks := &candidateChecks{reading: reading}
	f.promoter.Checks, f.promoter.Reruns = checks, f.queue
	if pass := f.passNow(); pass.Outcome != runstate.MergeQueuePassWaiting || withdrawer.count() != 0 {
		t.Fatalf("Pass() = %#v with %d withdrawals, want it waiting while a check still runs", pass, withdrawer.count())
	}
}

func TestAQueuedCandidateWithChecksThatJudgedTheChangeIsLeftToWait(t *testing.T) {
	t.Parallel()

	f, withdrawer, _ := queuedMergeFixture(t)
	checks := &candidateChecks{reading: publish.CheckReading{Pending: []string{"build"}, Failing: []publish.FailedCheck{{Name: "lint", ID: 5, Conclusion: "failure"}}}}
	f.promoter.Checks, f.promoter.Reruns = checks, f.queue
	if pass := f.passNow(); pass.Outcome != runstate.MergeQueuePassWaiting || len(checks.reruns) != 0 || withdrawer.count() != 0 {
		t.Fatalf("Pass() = %#v, re-runs %v, %d withdrawals; want an ordinary wait", pass, checks.reruns, withdrawer.count())
	}
}
