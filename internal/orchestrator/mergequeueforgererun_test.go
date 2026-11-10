package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// These drive the merge queue's half of yoyodyne-ifd.429.87.7: a candidate
// whose GitHub checks fail only in tests outside its change has the failing job
// run once more at once; a pass lets the landing go ahead; a second failure,
// or any other failure, withdraws the entry at once, naming the failing test,
// rather than waiting out triage.stuck_merge_age.

// queueTestLog is entry 30's failure on November 10: one test failing in
// internal/orchestrator, which the candidate does not touch.
const queueTestLog = "--- FAIL: TestTheTestRunsDecidedRepairTakesTheHoldsStartAndItsLandingLiftsTheHold (12.01s)\n" +
	"    hold_test.go:211: hold not lifted\n" +
	"FAIL\n" +
	"FAIL\tgithub.com/mason-bryant/yoyodyne/internal/orchestrator\t88.2s\n" +
	"FAIL\n" +
	"##[error]Process completed with exit code 2."

// heldChecks is GitHub's checks of the candidate's pull request, the log of a
// failing job, and the further runs asked for. Before each further run is
// asked, it reads the landing record to see the request was written down.
type heldChecks struct {
	f        *promotionFixture
	reading  publish.CheckReading
	log      string
	refuse   error
	reruns   []int64
	recorded []bool
}

func (h *heldChecks) Checks(context.Context, int, string) (publish.CheckReading, error) {
	reading := h.reading
	attempts := h.f.landing().Attempts
	reading.HeadCommit = attempts[len(attempts)-1].Candidate
	return reading, nil
}

func (h *heldChecks) JobLogTail(context.Context, int64, int) (string, error) { return h.log, nil }

func (h *heldChecks) RerunCheck(_ context.Context, checkRun int64) error {
	attempts := h.f.landing().Attempts
	rerun := attempts[len(attempts)-1].ForgeRerun
	h.recorded = append(h.recorded, rerun != nil && rerun.AskedAt == nil)
	if h.refuse != nil {
		return h.refuse
	}
	h.reruns = append(h.reruns, checkRun)
	return nil
}

func failingBuild(id int64, paths ...string) publish.CheckReading {
	if len(paths) == 0 {
		paths = []string{".github"}
	}
	return publish.CheckReading{
		Files:   []string{"feature.txt"},
		Failing: []publish.FailedCheck{{Name: "build", Paths: paths, ID: id, Conclusion: "failure"}},
		Passing: 3,
	}
}

func heldChecksFixture(t *testing.T) (*promotionFixture, *withdrawingForge, *recordingRepair, *heldChecks) {
	t.Helper()
	f, withdrawer, _ := queuedMergeFixture(t)
	repair := &recordingRepair{}
	f.promoter.Repair = repair
	checks := &heldChecks{f: f, reading: failingBuild(9001), log: queueTestLog}
	f.promoter.Checks = checks
	return f, withdrawer, repair, checks
}

func TestAQueueCandidateFailingOnlyInUntouchedTestsHasGitHubsJobRunOnceMoreAndLandsWhenItPasses(t *testing.T) {
	t.Parallel()

	f, withdrawer, repair, checks := heldChecksFixture(t)
	pass := f.pass(queueKey)
	if pass.Outcome != runstate.MergeQueuePassWaiting || !strings.Contains(pass.Says, "run the failing job once more") {
		t.Fatalf("Pass() = %#v, want the queue waiting on GitHub's second run", pass)
	}
	if len(checks.reruns) != 1 || checks.reruns[0] != 9001 || len(checks.recorded) != 1 || !checks.recorded[0] {
		t.Fatalf("re-runs = %v, recorded first = %v; want one, written down before it was asked", checks.reruns, checks.recorded)
	}
	attempts := f.landing().Attempts
	rerun := attempts[len(attempts)-1].ForgeRerun
	if rerun == nil || !rerun.Asked() || rerun.HeadCommit != attempts[len(attempts)-1].Candidate || len(rerun.Tests.Tests) != 1 || rerun.Tests.Tests[0].Touched {
		t.Fatalf("further run = %#v, want it recorded on the attempt for the candidate", rerun)
	}

	// A reading still naming the failed run says nothing new.
	if again := f.pass(queueKey); again.Outcome != runstate.MergeQueuePassWaiting || len(checks.reruns) != 1 {
		t.Fatalf("Pass() = %#v with re-runs %v, want it still waiting and nothing asked again", again, checks.reruns)
	}

	// The second run passes and GitHub makes the merge.
	checks.reading = publish.CheckReading{Files: []string{"feature.txt"}, Passing: 4}
	f.forge.PerformQueuedMerge(t)
	if landed := f.pass(queueKey); landed.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want the landing to go ahead", landed)
	}
	if withdrawer.count() != 0 || repair.count() != 0 {
		t.Fatalf("withdrawn %d, handed back %d; want neither", withdrawer.count(), repair.count())
	}
	final := f.landing().Attempts
	if final[len(final)-1].ForgeRerun == nil {
		t.Fatal("the first failure's record was not kept beside the landing")
	}
}

// A second failure, a failing test the change touches, and a GitHub that will
// not run the job again each withdraw the entry at once, without waiting out
// the stuck-merge age, and the handback names the failing test and whose it is.
func TestAQueueCandidateWhoseChecksFailIsWithdrawnAtOnceNamingTheFailingTest(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		setUp  func(*heldChecks)
		reruns int
		says   []string
	}{
		{"fails again", func(h *heldChecks) {
			if pass := h.f.pass(queueKey); pass.Outcome != runstate.MergeQueuePassWaiting {
				h.f.t.Fatalf("first Pass() = %#v, want it waiting on the second run", pass)
			}
			h.reading = failingBuild(9002)
		}, 1, []string{"TestTheTestRunsDecidedRepairTakesTheHoldsStartAndItsLandingLiftsTheHold", "and it failed again", "not put down to this change"}},
		{"touched", func(h *heldChecks) {
			h.reading = failingBuild(9001, "feature.txt")
		}, 0, []string{"feature.txt, which this change touches", "GitHub's own annotations put the failure on a file this change touches"}},
		{"refused", func(h *heldChecks) {
			h.refuse = errors.New("HTTP 403")
		}, 0, []string{"GitHub would not run it once more"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f, withdrawer, repair, checks := heldChecksFixture(t)
			// The limit on a queued merge's wait is far off: nothing here may
			// wait for it.
			f.worker.Pipeline.Config.Triage.StuckMergeAge = 0
			c.setUp(checks)
			pass := f.pass(queueKey)
			if pass.Outcome != runstate.MergeQueuePassHandedBack {
				t.Fatalf("Pass() = %#v, want the entry withdrawn and handed back at once", pass)
			}
			if withdrawer.count() != 1 || repair.count() != 1 || len(checks.reruns) != c.reruns {
				t.Fatalf("withdrawn %d, handed back %d, re-runs %v; want one withdrawal, one handback, %d re-run(s)", withdrawer.count(), repair.count(), checks.reruns, c.reruns)
			}
			handback := f.landing().Handback
			if handback == nil || handback.Continuation != runstate.MergeQueueReleased {
				t.Fatalf("handback = %#v, want the entry released, charging nothing", handback)
			}
			for _, want := range c.says {
				if !strings.Contains(handback.Reason, want) {
					t.Errorf("handback does not say %q:\n%s", want, handback.Reason)
				}
			}
		})
	}
}

// A process that asked GitHub for the further run and stopped before its
// answer was written down leaves the request recorded; the next pass never
// asks again for the same candidate.
func TestAQueueRestartAfterAskingNeverAsksGitHubASecondTime(t *testing.T) {
	t.Parallel()

	f, withdrawer, _, checks := heldChecksFixture(t)
	f.landings.fail = func(landing runstate.MergeQueueLanding) (bool, error) {
		attempts := landing.Attempts
		if rerun := attempts[len(attempts)-1].ForgeRerun; rerun != nil && rerun.AskedAt != nil {
			return false, errors.New("the process stopped")
		}
		return false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if _, err := f.promoter.Promote(ctx, queueKey); err == nil {
		t.Fatal("Promote() succeeded, want the stopped save reported")
	}
	f.landings.fail = nil
	if len(checks.reruns) != 1 {
		t.Fatalf("re-runs = %v, want the one asked before the stop", checks.reruns)
	}
	pass := f.pass(queueKey)
	if len(checks.reruns) != 1 {
		t.Fatalf("re-runs = %v, want no second request for the same candidate", checks.reruns)
	}
	if pass.Outcome != runstate.MergeQueuePassHandedBack || withdrawer.count() != 1 {
		t.Fatalf("Pass() = %#v, withdrawn %d; want the entry withdrawn since its one further run cannot be confirmed", pass, withdrawer.count())
	}
	if reason := f.landing().Handback.Reason; !strings.Contains(reason, "cut off before GitHub answered") {
		t.Errorf("handback does not say the request was cut off:\n%s", reason)
	}
}
