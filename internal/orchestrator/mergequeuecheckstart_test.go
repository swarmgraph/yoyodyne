package orchestrator

// A required GitHub check that never starts, or that GitHub skips, is asked
// for again once, with the entry kept at the head of the queue, and the queue
// acts only once GitHub has answered or has left the check unstarted through
// one more wait: the candidate's next generation runs the full local checks,
// or a held merge is withdrawn and handed back as a failure outside the
// change. A change withdrawn a second time is set aside for the development
// manager rather than handed back to be admitted again.

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// askingForge records each request to run a check again.
type askingForge struct {
	mu    sync.Mutex
	asked []string
}

func (a *askingForge) AskChecksAgain(_ context.Context, commit string, checkRun int64) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.asked = append(a.asked, commit)
	return "asked GitHub to run workflow run 9 (ci) again", nil
}

func (a *askingForge) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.asked)
}

// switchingGitHub answers the merge-wait question with whatever reply it holds
// now.
type switchingGitHub struct {
	mu    sync.Mutex
	reply string
}

func (s *switchingGitHub) set(reply string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reply = reply
}

func (s *switchingGitHub) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	s.mu.Lock()
	reply := s.reply
	s.mu.Unlock()
	return (&recordedGitHub{reply: reply}).Run(ctx, command, observer)
}

// heldReply is GitHub's account of a merge blocked on its required build: the
// build as given, or no build reported at all where build is empty.
func heldReply(build string) string {
	contexts := `{"__typename":"CheckRun","name":"race","status":"COMPLETED","conclusion":"SUCCESS","databaseId":5522,"isRequired":true}`
	if build != "" {
		contexts = `{"__typename":"CheckRun","name":"build","status":"` + build + `","conclusion":null,"databaseId":5521,"isRequired":true},` + contexts
	}
	return `{"data":{"repository":{"pullRequest":{"mergeable":"MERGEABLE","mergeStateStatus":"BLOCKED","isDraft":false,"reviewDecision":null,"mergeQueueEntry":null,"commits":{"nodes":[{"commit":{"statusCheckRollup":{"contexts":{"nodes":[` +
		contexts + `]}}}}]}}}}}`
}

// unstartedFixture is a merge GitHub holds for the queue's first entry, whose
// required build GitHub answers for as github says, with the harness able to
// ask for it again.
func unstartedFixture(t *testing.T, build string) (*promotionFixture, *withdrawingForge, *recordingRepair, *switchingGitHub, *askingForge) {
	t.Helper()
	github := &switchingGitHub{reply: heldReply(build)}
	f, withdrawer, _ := queuedMergeFixture(t)
	repair := &recordingRepair{}
	asker := &askingForge{}
	f.promoter.Repair, f.promoter.Reruns, f.promoter.Asker = repair, f.queue, asker
	f.promoter.Waits, f.promoter.Readings = publish.GitHub{Runner: github}, f.queue
	f.worker.Pipeline.Config.Execution.CIChecks.Required = []string{"build", "race"}
	return f, withdrawer, repair, github, asker
}

func currentCandidate(f *promotionFixture) string {
	attempt, _ := f.landing().Current()
	return attempt.Candidate
}

// passAhead runs a pass with every clock read as now plus ahead, and the limit
// on a held merge's wait set to an hour.
func (f *promotionFixture) passAhead(ahead time.Duration) runstate.MergeQueuePass {
	f.t.Helper()
	f.worker.Pipeline.Config.Triage.StuckMergeAge = config.Duration(time.Hour)
	f.worker.Pipeline.Clock = aheadClock{by: ahead}
	driver := f.driver()
	driver.Now = func() time.Time { return time.Now().Add(ahead) }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return driver.Pass(ctx, queueKey)
}

func TestAHeldMergeWhoseCheckNeverStartedIsAskedForAgainAndWaitsWhileItRuns(t *testing.T) {
	t.Parallel()

	f, withdrawer, _, github, asker := unstartedFixture(t, "QUEUED")

	// Not started yet, but GitHub has had little time: nothing is asked.
	if pass := f.passAhead(5 * time.Minute); pass.Outcome != runstate.MergeQueuePassWaiting || asker.count() != 0 {
		t.Fatalf("Pass() = %#v after %d requests, want an ordinary wait", pass, asker.count())
	}
	// Past the time a check is given to start: GitHub is asked once, the
	// request recorded with why, and the entry stays at the head of the queue.
	pass := f.passAhead(25 * time.Minute)
	if pass.Outcome != runstate.MergeQueuePassWaiting || pass.EntryID != f.entry.EntryID || !strings.Contains(pass.Says, "asked for it again") || asker.count() != 1 {
		t.Fatalf("Pass() = %#v after %d requests, want the build asked for again with the entry still waiting", pass, asker.count())
	}
	recorded, err := f.queue.CheckReruns(queueKey, f.entry.EntryID)
	if err != nil || len(recorded) != 1 || recorded[0].Conclusion != notStartedConclusion || recorded[0].Check != "build" ||
		!strings.Contains(recorded[0].Why, "had not started the required check build") || recorded[0].Commit != currentCandidate(f) {
		t.Fatalf("recorded requests = %#v, %v; want the request for the build on the candidate, saying why", recorded, err)
	}

	// The second request started it. Past the hour since the merge was asked
	// for, but inside the hour since the request, the merge waits on.
	github.set(heldReply("IN_PROGRESS"))
	if pass := f.passAhead(80 * time.Minute); pass.Outcome != runstate.MergeQueuePassWaiting || withdrawer.count() != 0 || asker.count() != 1 {
		t.Fatalf("Pass() = %#v with %d withdrawals and %d requests, want the merge waiting on the running build", pass, withdrawer.count(), asker.count())
	}
}

func TestAHeldMergeWhoseCheckNeverStartsIsHandedBackAsAFailureOutsideTheChange(t *testing.T) {
	t.Parallel()

	// GitHub reports nothing at all for the required build, as on November 11.
	f, withdrawer, repair, _, asker := unstartedFixture(t, "")
	if pass := f.passAhead(25 * time.Minute); pass.Outcome != runstate.MergeQueuePassWaiting || asker.count() != 1 {
		t.Fatalf("Pass() = %#v after %d requests, want the build asked for again", pass, asker.count())
	}
	// Past the first hour, but not past an hour since the request.
	if pass := f.passAhead(70 * time.Minute); pass.Outcome != runstate.MergeQueuePassWaiting || withdrawer.count() != 0 {
		t.Fatalf("Pass() = %#v with %d withdrawals, want the merge still waiting the limit once more", pass, withdrawer.count())
	}
	// An hour past the request and still not started: withdrawn, and handed
	// back saying in plain words that the change is not at fault.
	pass := f.passAhead(90 * time.Minute)
	if pass.Outcome != runstate.MergeQueuePassHandedBack || withdrawer.count() != 1 || repair.count() != 1 || asker.count() != 1 {
		t.Fatalf("Pass() = %#v with %d withdrawals, %d hand-backs and %d requests; want one of each", pass, withdrawer.count(), repair.count(), asker.count())
	}
	reason := f.landing().Handback.Reason
	for _, want := range []string{outsideFailureLead, "GitHub never started the required check build", "no repair attempt is charged", "not at fault"} {
		if !strings.Contains(reason, want) {
			t.Errorf("hand-back reason = %q, want it to say %q", reason, want)
		}
	}
	if !outsideFailure(reason) {
		t.Errorf("hand-back reason = %q is not read as a failure outside the change", reason)
	}
}

// withdrawAndPass withdraws the entry's queued merge with the reason given and
// runs a pass, which hands the change back or sets it aside.
func (f *promotionFixture) withdrawAndPass(entryID, why string) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if withdrawn, err := f.promoter.Withdraw(ctx, queueKey, entryID, why); err != nil || !withdrawn.Withdrawn {
		f.t.Fatalf("Withdraw() = %#v, %v; want the merge withdrawn", withdrawn, err)
	}
	f.passNow()
}

// admitAgain admits the same run's change at head behind its earlier entry,
// verifies it, and has the forge queue its merge.
func (f *promotionFixture) admitAgain(head string) runstate.MergeQueueEntry {
	f.t.Helper()
	again, _, err := f.queue.Admit(context.Background(), runstate.MergeQueueAdmission{
		Key: queueKey, WorkItemID: f.entry.WorkItemID, WorkItemTitle: f.entry.WorkItemTitle, RunID: f.entry.RunID,
		ApprovedHead: head, IntegrationPolicy: f.entry.IntegrationPolicy, Mode: f.entry.Mode, ModeEvidence: f.entry.ModeEvidence,
	})
	if err != nil || again.Supersedes != f.entry.EntryID {
		f.t.Fatalf("Admit() = %#v, %v; want the change admitted again after its first entry", again, err)
	}
	f.entry = again
	f.verify()
	if waiting, err := f.promote(); err != nil || waiting.Waiting == "" {
		f.t.Fatalf("Promote() = %#v, %v; want the merge queued with the forge again", waiting, err)
	}
	return again
}

func TestAChangeWithdrawnFromTheQueueASecondTimeIsSetAsideForTheDevelopmentManager(t *testing.T) {
	t.Parallel()

	f, _, _ := queuedMergeFixture(t)
	repair := &recordingRepair{}
	f.promoter.Repair = repair

	// The first withdrawal is handed back to the run, as before.
	f.withdrawAndPass(f.entry.EntryID, "GitHub held the merge too long")
	if repair.count() != 1 || len(repair.reported) != 0 {
		t.Fatalf("handed back %d and reported %d, want the first withdrawal handed back", repair.count(), len(repair.reported))
	}

	// The same change, at the same approved head, admitted again, verified,
	// queued with the forge, and withdrawn a second time: set aside for the
	// development manager.
	again := f.admitAgain(f.entry.ApprovedHead)
	f.withdrawAndPass(again.EntryID, "GitHub held the merge too long")
	if repair.count() != 1 || len(repair.reported) != 1 {
		t.Fatalf("handed back %d and reported %d, want the second withdrawal reported rather than handed back", repair.count(), len(repair.reported))
	}
	handback := f.landing().Handback
	if handback == nil || !handback.IsSetAside() || !strings.Contains(handback.SetAside, "same approved head, 2 times") || !strings.Contains(handback.SetAside, "development manager") {
		t.Fatalf("hand-back = %#v, want it set aside for the development manager as the second withdrawal", handback)
	}
}

// A change whose required check started and failed is handed back for a
// repair; the repaired change carries a new approved head, so when its check
// fails again it is handed back for a repair again rather than set aside.
func TestARepairedChangeWithdrawnAgainAfterAFailedCheckIsHandedBackForRepair(t *testing.T) {
	t.Parallel()

	f, _, _ := queuedMergeFixture(t)
	repair := &recordingRepair{}
	f.promoter.Repair = repair
	failed := "GitHub's checks on pull request 41 failed: the required check test ended in failure"
	f.withdrawAndPass(f.entry.EntryID, failed)
	if repair.count() != 1 {
		t.Fatalf("handed back %d, want the failed change handed back for a repair", repair.count())
	}

	// The run's repair commits on top of the change, and the repaired head is
	// admitted again.
	runPipelineGit(t, f.repository, "switch", "-c", "repaired", f.entry.ApprovedHead)
	writeQueueFile(t, f.repository, "repaired.txt", "repaired\n")
	runPipelineGit(t, f.repository, "add", "repaired.txt")
	runPipelineGit(t, f.repository, "commit", "-m", "the repair")
	repaired := gitLine(t, f.repository, "rev-parse", "HEAD")
	runPipelineGit(t, f.repository, "switch", "main")
	again := f.admitAgain(repaired)

	f.withdrawAndPass(again.EntryID, failed)
	if repair.count() != 2 || len(repair.reported) != 0 {
		t.Fatalf("handed back %d and reported %d, want the repaired change handed back for a repair again", repair.count(), len(repair.reported))
	}
	if handback := f.landing().Handback; handback == nil || handback.IsSetAside() {
		t.Fatalf("hand-back = %#v, want it given to the run, not set aside", handback)
	}
}

// A candidate whose required check GitHub skipped has it asked for again, and
// lands on GitHub's checks once the request runs it, with no full local run.
func TestASkippedRequiredCheckIsAskedForAgainAndTheRequestRunsIt(t *testing.T) {
	t.Parallel()

	asker := &askingForge{}
	f, _ := quickFixture(t, func(commit, name string, _ int) publish.CheckResult {
		if name == "test" && asker.count() == 0 {
			return publish.CheckSkipped
		}
		return publish.CheckPassed
	})
	f.worker.Asker, f.worker.Reruns = asker, f.queue
	verification, err := f.work()
	if err != nil || verification.Verified || !strings.Contains(verification.Waiting, "asked it to run test") || asker.count() != 1 {
		t.Fatalf("Work() = %#v, %v after %d requests; want the skipped test asked for again", verification, err, asker.count())
	}
	if recorded, err := f.queue.CheckReruns(queueKey, f.entry.EntryID); err != nil || len(recorded) != 1 || recorded[0].Check != "test" || !strings.Contains(recorded[0].Why, "skipped") {
		t.Fatalf("recorded requests = %#v, %v; want the request for test, saying it was skipped", recorded, err)
	}
	verification, err = f.work()
	if err != nil || !verification.Verified || asker.count() != 1 {
		t.Fatalf("Work() = %#v, %v after %d requests; want the candidate verified on GitHub's checks", verification, err, asker.count())
	}
	generations := f.generations()
	if len(generations) != 1 || generations[0].CIChecks == nil || generations[0].CIChecks.FellBack != "" {
		t.Fatalf("generations = %#v, want one candidate settled by GitHub's checks", generations)
	}
}

// A candidate whose required check GitHub never starts, even after it is asked
// for again, has its next candidate run the full local checks.
func TestARequiredCheckThatNeverStartsIsAskedForOnceThenReplacedByTheFullLocalChecks(t *testing.T) {
	t.Parallel()

	asker := &askingForge{}
	f, _ := quickFixture(t, func(_, name string, _ int) publish.CheckResult {
		if name == "test" {
			return publish.CheckNotStarted
		}
		return publish.CheckPassed
	})
	f.worker.Asker, f.worker.Reruns = asker, f.queue
	limit := f.worker.Pipeline.Config.Execution.CIChecks.WaitLimitInForce()
	at := func(ahead time.Duration) (MergeQueueVerification, error) {
		f.worker.Pipeline.Clock = aheadClock{by: ahead}
		return f.work()
	}
	if verification, err := at(0); err != nil || verification.Verified || asker.count() != 0 {
		t.Fatalf("Work() = %#v, %v after %d requests; want an ordinary wait", verification, err, asker.count())
	}
	asked := startLimit(limit) + time.Minute
	if verification, err := at(asked); err != nil || verification.Verified || asker.count() != 1 || !strings.Contains(verification.Waiting, "head of the queue") {
		t.Fatalf("Work() = %#v, %v after %d requests; want test asked for again once", verification, err, asker.count())
	}
	// Past the wait limit since the push, but not since the request.
	if verification, err := at(limit + time.Minute); err != nil || verification.Verified || asker.count() != 1 {
		t.Fatalf("Work() = %#v, %v after %d requests; want it still waiting the limit once more", verification, err, asker.count())
	}
	verification, err := at(asked + limit + time.Minute)
	if err != nil || !verification.Verified || asker.count() != 1 {
		t.Fatalf("Work() = %#v, %v after %d requests; want the full local checks run in GitHub's place", verification, err, asker.count())
	}
	generations := f.generations()
	if len(generations) != 2 || !strings.Contains(generations[0].CIChecks.FellBack, "asking GitHub to run it again") || !slices.Equal(generations[1].Checks.Commands, queueChecks) {
		t.Fatalf("generations = %#v, want the first set aside after the request and the second checked locally", generations)
	}
}

// A merge handed back because GitHub never started its check judged nothing
// about the change: the run keeps the checks it passed, no check failure is
// written against it, and the item's note says so in plain words.
func TestAMergeHandedBackBecauseItsCheckNeverStartedLeavesTheChangeNotAtFault(t *testing.T) {
	t.Parallel()

	f, runs, handback := releasedRunHandback(t)
	f.tracker.Item.Status = "in_progress"
	handback.Reason = "its queued merge was withdrawn: " + outsideFailureLead + "GitHub never started the required check build on pull request 41; no repair attempt is charged for it, and the change is not at fault"
	if err := (MergeQueueRunHandback{Store: runs, Tracker: f.tracker}).HandBack(context.Background(), f.entry, handback); err != nil {
		t.Fatalf("HandBack() error = %v", err)
	}
	saved := f.run.latest()
	if saved.CheckFailure != nil || saved.ChecksPassed == nil {
		t.Fatalf("check failure %#v, checks passed %#v; want no failure written and the passed checks kept", saved.CheckFailure, saved.ChecksPassed)
	}
	if saved.Status != runstate.StatusFailed || saved.StopClass != runstate.StopIntegration || !strings.Contains(saved.Failure, "not at fault") {
		t.Fatalf("run = status %q, stop %q, failure %q; want it stopped saying the change is not at fault", saved.Status, saved.StopClass, saved.Failure)
	}
	if !f.tracker.Blocked || !strings.Contains(f.tracker.BlockReason, "no repair attempt is charged") {
		t.Fatalf("item blocked %v with %q, want the item told no repair attempt is charged", f.tracker.Blocked, f.tracker.BlockReason)
	}
}
