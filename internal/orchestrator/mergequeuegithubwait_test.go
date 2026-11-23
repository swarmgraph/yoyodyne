package orchestrator

// The merge queue waiting out GitHub being unreachable in its own landing and
// recovery passes (mergequeuegithubwait.go): each step that meets GitHub waits
// on the paced intervals and then carries on, a wait outlives the process that
// began it, a refusal about the change is not waited out, and a wait that goes
// on long enough is reported to the development manager.

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/recovery"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// gitHubWaitClock is a clock a test moves by hand.
type gitHubWaitClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *gitHubWaitClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *gitHubWaitClock) advance(by time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(by)
}

// gitHubOutage is which of the queue's GitHub steps fail as unreachable, and
// how many more times each will.
type gitHubOutage struct {
	mu    sync.Mutex
	left  map[string]int
	calls map[string]int
}

func newGitHubOutage() *gitHubOutage {
	return &gitHubOutage{left: map[string]int{}, calls: map[string]int{}}
}

// fails reports the error a step meets on this call, or nil where GitHub
// answers it.
func (o *gitHubOutage) fails(step string, unreachable func() error) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls[step]++
	if o.left[step] > 0 {
		o.left[step]--
		return unreachable()
	}
	return nil
}

func (o *gitHubOutage) set(step string, failures int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.left[step] = failures
}

func (o *gitHubOutage) count(step string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.calls[step]
}

func connectionReset(step string) func() error {
	return func() error { return orchestratortest.ConnectionReset(step) }
}

// outageForge is the fixture's forge with GitHub unreachable for the steps the
// outage names.
type outageForge struct {
	*stoppingForge
	outage *gitHubOutage
}

func (f outageForge) Ensure(ctx context.Context, request publish.Request) (publish.PullRequest, error) {
	if err := f.outage.fails("open", connectionReset("open pull request")); err != nil {
		return publish.PullRequest{}, err
	}
	return f.stoppingForge.Ensure(ctx, request)
}

func (f outageForge) Merge(ctx context.Context, request publish.MergeRequest) (publish.MergeResult, error) {
	if err := f.outage.fails("merge", connectionReset("merge pull request")); err != nil {
		return publish.MergeResult{}, err
	}
	return f.stoppingForge.Merge(ctx, request)
}

func (f outageForge) State(ctx context.Context, head string) (publish.PullRequest, error) {
	if err := f.outage.fails("state", connectionReset("read pull request")); err != nil {
		return publish.PullRequest{}, err
	}
	return f.stoppingForge.State(ctx, head)
}

// outageLander is the fixture's lander with GitHub unreachable for the steps
// the outage names.
type outageLander struct {
	*stoppingLander
	outage *gitHubOutage
}

func (l outageLander) QueueCandidateBranchCommit(ctx context.Context, branch string) (string, bool, error) {
	if err := l.outage.fails("branch", connectionReset("ls-remote "+branch)); err != nil {
		return "", false, err
	}
	return l.stoppingLander.QueueCandidateBranchCommit(ctx, branch)
}

func (l outageLander) PublishQueueCandidate(ctx context.Context, branch, candidate, expected string) error {
	unanswered := func() error {
		return fmt.Errorf("%w: publish %s on %s over %q ended failed with exit code 128 and no answer from the remote: fatal: unable to access: Could not resolve host: github.com",
			gitworktree.ErrRemotePushInterrupted, candidate, branch, expected)
	}
	if err := l.outage.fails("push", unanswered); err != nil {
		return err
	}
	return l.stoppingLander.PublishQueueCandidate(ctx, branch, candidate, expected)
}

func (l outageLander) VerifyRemoteTarget(ctx context.Context, integration gitworktree.Integration) error {
	if err := l.outage.fails("verify", connectionReset("fetch origin")); err != nil {
		return err
	}
	return l.stoppingLander.VerifyRemoteTarget(ctx, integration)
}

func (l outageLander) ConfirmRemoteTarget(ctx context.Context, integration gitworktree.Integration, mergeCommit string) (string, error) {
	if err := l.outage.fails("confirm", connectionReset("fetch origin")); err != nil {
		return "", err
	}
	return l.stoppingLander.ConfirmRemoteTarget(ctx, integration, mergeCommit)
}

// gitHubOutageFixture is a protected target landing through a pull request,
// with the queue's wait kept in the real store, a clock moved by hand, and
// GitHub unreachable wherever the outage says.
func gitHubOutageFixture(t *testing.T) (*promotionFixture, *gitHubOutage, *gitHubWaitClock, runstate.MergeQueueGeneration) {
	t.Helper()
	f := newPromotionFixture(t, landProtected)
	generation := f.verify()
	outage := newGitHubOutage()
	clock := &gitHubWaitClock{now: time.Now().UTC()}
	f.promoter.Pipeline.Clock = clock
	f.promoter.Pipeline.Publisher = outageForge{stoppingForge: f.forge, outage: outage}
	f.promoter.Lander = outageLander{stoppingLander: f.lander, outage: outage}
	f.promoter.GitHubWaits = f.queue
	return f, outage, clock, generation
}

func (f *promotionFixture) gitHubWait() (runstate.MergeQueueGitHubWait, bool) {
	f.t.Helper()
	wait, found, err := f.queue.GitHubWait(queueKey)
	if err != nil {
		f.t.Fatalf("GitHubWait() error = %v", err)
	}
	return wait, found
}

// assertWaitingForGitHub promotes and checks the promotion ended waiting for GitHub after
// tries failed tries, with nothing handed back, refused or left unresolved,
// and the wait recorded beside the queue.
func (f *promotionFixture) assertWaitingForGitHub(tries int) runstate.MergeQueueGitHubWait {
	f.t.Helper()
	promoted, err := f.promote()
	if err != nil || promoted.GitHubWait == nil || promoted.HandedBack != nil || promoted.Refusal != "" || promoted.Unresolved != "" || promoted.Landed {
		f.t.Fatalf("Promote() = %#v, %v; want it waiting for GitHub with nothing handed back or refused", promoted, err)
	}
	if !strings.Contains(promoted.Waiting, "the merge queue is waiting for GitHub to be reachable again") || !strings.Contains(promoted.Waiting, "next try at") {
		f.t.Fatalf("Waiting = %q, want it to say the queue waits for GitHub and when it next tries", promoted.Waiting)
	}
	wait, found := f.gitHubWait()
	if !found || wait.EntryID != f.entry.EntryID || wait.Wait.Tries != tries || !reflect.DeepEqual(*promoted.GitHubWait, wait) {
		f.t.Fatalf("recorded wait = %#v (found %t), outcome's = %#v; want %d tries for entry %s", wait, found, promoted.GitHubWait, tries, f.entry.EntryID)
	}
	if landing := f.landing(); landing.Handback != nil || landing.Completion != nil {
		f.t.Fatalf("landing = %#v, want the entry still in the queue", landing)
	}
	return wait
}

// Each step of the queue's landing that meets GitHub waits for it when it
// cannot be reached, asks nothing before the next try is due, tries again on
// the paced intervals, and lands the change once GitHub answers.
func TestTheMergeQueueWaitsOutGitHubAtEachLandingStepAndThenLands(t *testing.T) {
	t.Parallel()

	for _, step := range []struct{ name, fails string }{
		{"reading the candidate branch", "branch"},
		{"pushing the candidate", "push"},
		{"opening the pull request", "open"},
		{"checking the remote target before the merge", "verify"},
		{"asking for the merge", "merge"},
		{"reading the pull request after its merge", "state"},
		{"confirming the remote target carries the merge", "confirm"},
	} {
		t.Run(step.name, func(t *testing.T) {
			t.Parallel()
			f, outage, clock, generation := gitHubOutageFixture(t)
			outage.set(step.fails, 2)

			first := f.assertWaitingForGitHub(1)
			if want := clock.Now().Add(recovery.Interval(1)); !first.Wait.NextTryAt.Equal(want) {
				t.Fatalf("next try at %s, want %s, the first paced interval", first.Wait.NextTryAt, want)
			}
			asked := outage.count(step.fails)

			// Before the next try is due nothing is asked of GitHub.
			gated := f.assertWaitingForGitHub(1)
			if outage.count(step.fails) != asked || gated.Wait.Tries != 1 {
				t.Fatalf("GitHub was asked %d times before the next try was due, want %d", outage.count(step.fails), asked)
			}

			clock.advance(time.Hour)
			second := f.assertWaitingForGitHub(2)
			if !second.Wait.Since.Equal(first.Wait.Since) {
				t.Fatalf("the wait began again at %s, want it kept from %s", second.Wait.Since, first.Wait.Since)
			}

			clock.advance(time.Hour)
			landed, err := f.promote()
			if err != nil || !landed.Landed || !landed.Completed || landed.GitHubWait != nil {
				t.Fatalf("Promote() once GitHub answers = %#v, %v; want the change landed", landed, err)
			}
			if _, found := f.gitHubWait(); found {
				t.Fatal("the wait for GitHub is still recorded after GitHub answered")
			}
			f.assertLandedOnce(generation)
		})
	}
}

// A wait under way when the process working the queue stops is taken up by a
// promoter in the next process: it asks nothing before the try that was due,
// and counts on from the tries already made.
func TestAWaitForGitHubOutlivesTheProcessThatBeganIt(t *testing.T) {
	t.Parallel()

	f, outage, clock, generation := gitHubOutageFixture(t)
	outage.set("merge", 2)
	f.assertWaitingForGitHub(1)

	// The next process builds its promoter afresh over the same records; the
	// wait is in the queue's records, not the promoter.
	restarted := f.promoter
	f.promoter = restarted
	f.assertWaitingForGitHub(1)
	if asked := outage.count("merge"); asked != 1 {
		t.Fatalf("the merge was asked for %d times, want once before the next try was due", asked)
	}
	clock.advance(time.Hour)
	f.assertWaitingForGitHub(2)
	clock.advance(time.Hour)
	if landed, err := f.promote(); err != nil || !landed.Landed || !landed.Completed {
		t.Fatalf("Promote() once GitHub answers = %#v, %v; want the change landed", landed, err)
	}
	f.assertLandedOnce(generation)
	if merges := len(f.forge.MergeRequests()); merges != 1 {
		t.Fatalf("the forge made %d merges, want one", merges)
	}
}

// A refusal from GitHub about the change itself is an answer, not GitHub being
// away: it is handled as it always was and nothing waits.
func TestARefusalAboutTheChangeIsNotWaitedOutByTheMergeQueue(t *testing.T) {
	t.Parallel()

	t.Run("a push refused for its content", func(t *testing.T) {
		t.Parallel()
		f, lander := pushingFixture(t)
		f.promoter.GitHubWaits = f.queue
		f.verify()
		lander.set(func(_ context.Context, branch, candidate, expected string) error {
			return fmt.Errorf("%w: publish %s on %s over %q failed with exit code 1: ! [remote rejected] (pre-receive hook declined)",
				gitworktree.ErrRemotePushRejected, candidate, branch, expected)
		})
		refused, err := f.promote()
		if err != nil || refused.HandedBack == nil || refused.GitHubWait != nil {
			t.Fatalf("Promote() = %#v, %v; want the entry handed back and nothing waiting", refused, err)
		}
		if _, found := f.gitHubWait(); found {
			t.Fatal("a refused push was recorded as a wait for GitHub")
		}
	})

	t.Run("a merge GitHub refuses", func(t *testing.T) {
		t.Parallel()
		f, _, _, _ := gitHubOutageFixture(t)
		f.forge.MergeErr = publish.MergeRefused{Number: 1, Reason: "a required check failed"}
		refused, err := f.promote()
		if err != nil || refused.GitHubWait != nil || refused.Unresolved == "" {
			t.Fatalf("Promote() = %#v, %v; want the refused merge kept as it is and nothing waiting", refused, err)
		}
		if _, found := f.gitHubWait(); found {
			t.Fatal("a refused merge was recorded as a wait for GitHub")
		}
	})
}

// Withdrawing a queued merge that cannot reach GitHub waits for it, with the
// withdrawal left standing, and is settled once GitHub answers.
func TestWithdrawingAMergeWaitsOutGitHub(t *testing.T) {
	t.Parallel()

	f, withdrawer, _ := queuedMergeFixture(t)
	clock := &gitHubWaitClock{now: time.Now().UTC()}
	f.promoter.Pipeline.Clock = clock
	f.promoter.GitHubWaits = f.queue
	withdrawer.answer = func(call int) error {
		if call == 1 {
			return orchestratortest.ConnectionReset("disable auto-merge")
		}
		return nil
	}
	waiting, err := f.withdraw()
	if err != nil || waiting.Withdrawn || waiting.GitHubWait == nil || waiting.Unresolved != "" {
		t.Fatalf("Withdraw() = %#v, %v; want it waiting for GitHub with the withdrawal standing", waiting, err)
	}
	f.assertStillArmed()
	if gated, err := f.withdraw(); err != nil || gated.GitHubWait == nil || withdrawer.count() != 1 {
		t.Fatalf("Withdraw() before the next try = %#v, %v (%d asks); want it waiting without asking GitHub", gated, err, withdrawer.count())
	}
	clock.advance(time.Hour)
	withdrawn, err := f.withdraw()
	if err != nil || !withdrawn.Withdrawn || withdrawn.GitHubWait != nil {
		t.Fatalf("Withdraw() once GitHub answers = %#v, %v; want the merge withdrawn", withdrawn, err)
	}
	if _, found := f.gitHubWait(); found {
		t.Fatal("the wait for GitHub is still recorded after the withdrawal was settled")
	}
}

// A wait that keeps failing is reported once to the development manager, says
// so on status, and goes on waiting.
func TestALongWaitForGitHubIsToldToTheDevelopmentManagerOnce(t *testing.T) {
	t.Parallel()

	f, outage, clock, _ := gitHubOutageFixture(t)
	reports := &fakeReports{}
	f.promoter.Pipeline.Reports = reports
	outage.set("push", mergeQueueGitHubWaitReportAfter+1)
	for try := 1; try < mergeQueueGitHubWaitReportAfter; try++ {
		f.assertWaitingForGitHub(try)
		clock.advance(time.Hour)
	}
	if len(reports.appended) != 0 {
		t.Fatalf("%d reports filed before the wait had failed %d tries, want none", len(reports.appended), mergeQueueGitHubWaitReportAfter)
	}
	told := f.assertWaitingForGitHub(mergeQueueGitHubWaitReportAfter)
	if told.ReportedAt == nil || !strings.Contains(told.Says(), "the development manager has been told") {
		t.Fatalf("wait = %#v (%q), want it to say the development manager was told", told, told.Says())
	}
	if len(reports.appended) != 1 {
		t.Fatalf("%d reports filed, want one", len(reports.appended))
	}
	filed := reports.appended[0]
	if filed.For != domain.RoleDevelopmentManager || filed.Severity != report.SeverityWarning || !strings.Contains(filed.Message, "has not reached GitHub") {
		t.Fatalf("report = %#v, want a warning for the development manager saying the queue has not reached GitHub", filed)
	}
	clock.advance(time.Hour)
	f.assertWaitingForGitHub(mergeQueueGitHubWaitReportAfter + 1)
	if len(reports.appended) != 1 {
		t.Fatalf("%d reports filed after a further failed try, want still one", len(reports.appended))
	}
}

// A pass that ended waiting for GitHub goes round again when the next try is
// due, never sooner than a second and never later than the longest interval,
// and a pass waiting on anything else goes round at its usual pace.
func TestTheNextQueuePassIsPacedByTheWaitForGitHub(t *testing.T) {
	t.Parallel()

	again := time.Minute
	if got := nextPassIn(runstate.MergeQueuePass{Outcome: runstate.MergeQueuePassWaiting}, again); got != again {
		t.Fatalf("nextPassIn() with no wait for GitHub = %s, want %s", got, again)
	}
	waiting := func(in time.Duration) runstate.MergeQueuePass {
		return runstate.MergeQueuePass{Outcome: runstate.MergeQueuePassWaiting, GitHubWait: &runstate.MergeQueueGitHubWait{
			Wait: runstate.ForgeWait{NextTryAt: time.Now().Add(in)},
		}}
	}
	if got := nextPassIn(waiting(5*time.Minute), again); got < 4*time.Minute || got > 5*time.Minute {
		t.Fatalf("nextPassIn() five minutes before the try = %s, want about five minutes", got)
	}
	if got := nextPassIn(waiting(-time.Minute), again); got != time.Second {
		t.Fatalf("nextPassIn() past the try = %s, want a second", got)
	}
	if got := nextPassIn(waiting(24*time.Hour), again); got != recovery.MaxInterval {
		t.Fatalf("nextPassIn() a day before the try = %s, want %s", got, recovery.MaxInterval)
	}
}

// The driver ends a pass whose step could not reach GitHub waiting, with the
// wait on the pass record for status and the dashboard, rather than stopped.
func TestAQueuePassThatCannotReachGitHubEndsWaitingNotStopped(t *testing.T) {
	t.Parallel()

	f, outage, _, _ := gitHubOutageFixture(t)
	outage.set("merge", 1)
	driver := MergeQueueDriver{Worker: f.worker, Promoter: f.promoter, Queues: f.queue}
	pass := driver.Pass(context.Background(), queueKey)
	if pass.Outcome != runstate.MergeQueuePassWaiting || pass.GitHubWait == nil || !strings.Contains(pass.Says, "waiting for GitHub to be reachable again") {
		t.Fatalf("Pass() = %#v, want it waiting for GitHub", pass)
	}
	recorded, found, err := f.queue.LastPass(queueKey)
	if err != nil || !found || recorded.GitHubWait == nil || recorded.GitHubWait.Wait.Tries != 1 {
		t.Fatalf("LastPass() = %#v, %t, %v; want the wait for GitHub on the recorded pass", recorded, found, err)
	}
}
