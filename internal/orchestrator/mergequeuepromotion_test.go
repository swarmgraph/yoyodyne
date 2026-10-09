package orchestrator

// The merge queue's promotion: only the verified candidate lands, a target
// that moves is drift and nothing else, every mutation is on the record before
// it is asked for, and a process that stops anywhere leaves a record the next
// one finishes from without asking for anything twice.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/queuemode"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// errStopped is a process stopping where a test says, with whatever it was
// doing either not begun or done and not yet written down.
var errStopped = errors.New("the process stopped here")

type promotionFixture struct {
	*queueFixture
	promoter MergeQueuePromoter
	landings *flakyLandings
	lander   *stoppingLander
	forge    *stoppingForge
	tracker  *orchestratortest.Tracker
	run      *recordingRun
	remote   string
	catchUps int
}

// landingWay is how the fixture's target lands.
type landingWay int

const (
	landLocally landingWay = iota
	landUnprotected
	landProtected
)

func newPromotionFixture(t *testing.T, way landingWay, verdicts ...string) *promotionFixture {
	t.Helper()
	f := &promotionFixture{queueFixture: newQueueFixture(t, verdicts...)}
	pipeline := f.worker.Pipeline
	f.tracker = pipeline.Tracker.(*orchestratortest.Tracker)
	admitted := f.runs.(*admittedRun)
	f.run = &recordingRun{admittedRun: admitted}
	pipeline.Store = f.run
	f.landings = &flakyLandings{flakyGenerations: f.records}
	f.lander = &stoppingLander{Manager: pipeline.Worktrees.(*gitworktree.Manager), stop: map[runstate.MergeQueueMutation]string{}}
	if way != landLocally {
		f.remote = addBareRemote(t, f.repository)
		forge := &orchestratortest.Forge{Remote: f.remote}
		if way == landProtected {
			forge.TargetProtection = publish.BranchProtection{Protected: true, By: "a ruleset"}
		}
		f.forge = &stoppingForge{Forge: forge, stop: map[runstate.MergeQueueMutation]string{}}
		*pipeline = publishing(*pipeline, f.forge)
	}
	f.worker.Waiting = MergeQueueWaiting(f.landings)
	f.lander.onCatchUp = func() {
		f.catchUps++
		if !f.promotionLeaseHeld() {
			t.Error("the local target was caught up without the promotion lease")
		}
	}
	f.promoter = MergeQueuePromoter{
		Pipeline: pipeline, Queue: f.landings, Lander: f.lander,
		Completion: MergeQueueRunCompletion{Store: f.run, Tracker: f.tracker},
	}
	return f
}

func (f *promotionFixture) verify() runstate.MergeQueueGeneration {
	f.t.Helper()
	verification, err := f.work()
	if err != nil || !verification.Verified {
		f.t.Fatalf("Work() = %#v, %v; want the entry verified", verification, err)
	}
	return verification.Generation
}

func (f *promotionFixture) promote() (MergeQueuePromotion, error) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return f.promoter.Promote(ctx, queueKey)
}

func (f *promotionFixture) landing() runstate.MergeQueueLanding {
	f.t.Helper()
	landing, _, err := f.queue.Landing(queueKey, f.entry.EntryID)
	if err != nil {
		f.t.Fatalf("Landing() error = %v", err)
	}
	return landing
}

func (f *promotionFixture) local(branch string) string {
	return gitLine(f.t, f.repository, "rev-parse", branch)
}

// promotionLeaseHeld reports whether something holds the target's promotion
// lease now, by trying to take it for a moment.
func (f *promotionFixture) promotionLeaseHeld() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	lease, err := f.run.LeasePromotion(ctx, "main")
	if err != nil {
		return true
	}
	_ = lease.Release()
	return false
}

// assertLandedOnce checks the entry finished with exactly one landing, of the
// candidate whose checks and review authorized it, recorded once on the run
// and once on the work item.
func (f *promotionFixture) assertLandedOnce(generation runstate.MergeQueueGeneration) runstate.MergeQueueLanding {
	f.t.Helper()
	landing := f.landing()
	completion := landing.Completion
	if completion == nil || !completion.Whole() {
		f.t.Fatalf("completion = %#v, want every part recorded", completion)
	}
	if completion.Landed != generation.Candidate || completion.Binding != generation.Binding() || completion.Generation != generation.Number {
		f.t.Fatalf("completion = %#v, want the candidate of generation %d and its binding", completion, generation.Number)
	}
	if err := generation.Gate(f.configured()); err != nil {
		f.t.Fatalf("the landed generation's gate: %v", err)
	}
	landed := 0
	for _, attempt := range landing.Attempts {
		if attempt.Landed != nil {
			landed++
		}
	}
	if landed != 1 {
		f.t.Fatalf("%d attempts landed, want exactly one", landed)
	}
	if landing.RunID != f.entry.RunID || landing.Publication != f.entry.Publication {
		f.t.Fatalf("landing names run %s and publication %q, want the entry's own", landing.RunID, landing.Publication)
	}
	if f.run.saveCount() != 1 {
		f.t.Fatalf("the run was saved %d times, want its integration recorded once", f.run.saveCount())
	}
	if saved := f.run.latest(); saved.Integration == nil || saved.Integration.TargetCommit != generation.Candidate || saved.Integration.SourceCommit != generation.Candidate {
		f.t.Fatalf("run integration = %#v, want the candidate landed, recorded as a run's fast-forward is", saved.Integration)
	}
	if completes := strings.Count(strings.Join(f.tracker.Calls, " "), "complete"); completes != 1 || !f.tracker.Closed {
		f.t.Fatalf("work item completed %d times (closed %v), want once", completes, f.tracker.Closed)
	}
	// Asked again, the queue has nothing left to land and asks nothing of
	// anybody.
	again, err := f.promote()
	if err != nil || again.Entry.EntryID != "" {
		f.t.Fatalf("Promote() after completion = %#v, %v; want nothing left to do", again, err)
	}
	return landing
}

func TestTheQueueLandsOnlyTheVerifiedCandidateOnAnUnpublishedTarget(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	base := f.local("main")
	refused, err := f.promote()
	if err != nil || refused.Refusal == "" || refused.Landed {
		t.Fatalf("Promote() before verification = %#v, %v; want a refusal", refused, err)
	}
	if f.local("main") != base || len(f.landing().Attempts) != 0 {
		t.Fatal("a promotion with no verified candidate moved the target or intended an attempt")
	}

	generation := f.verify()
	checks, reviews := f.checks.calls(), f.reviewer.calls()
	promoted, err := f.promote()
	if err != nil || !promoted.Landed || !promoted.Completed || promoted.stopped() {
		t.Fatalf("Promote() = %#v, %v; want the candidate landed and completed", promoted, err)
	}
	if f.checks.calls() != checks || f.reviewer.calls() != reviews {
		t.Fatal("the promotion ran a check or asked for a review")
	}
	if f.local("main") != generation.Candidate {
		t.Fatalf("main = %s, want the verified candidate %s", f.local("main"), generation.Candidate)
	}
	if readQueueFile(t, f.repository, "feature.txt") != "implemented\n" {
		t.Fatal("the primary checkout was not moved with its branch")
	}
	if f.local("change") != f.head {
		t.Fatal("the change's own branch was moved")
	}
	landing := f.assertLandedOnce(generation)
	attempt := landing.Attempts[0]
	if attempt.Path != runstate.MergeQueueLandLocally || len(attempt.Mutations) != 1 || attempt.Mutations[0].Expected != base || attempt.Mutations[0].Commit != generation.Candidate {
		t.Fatalf("attempt = %#v, want one compare-and-swap from the base to the candidate", attempt)
	}
}

func TestATargetThatMovesIsDriftThatNeedsFreshVerificationAndChargesNothing(t *testing.T) {
	t.Parallel()

	for _, moment := range []string{"during the checks", "during the review", "after verification", "immediately before the move"} {
		t.Run(moment, func(t *testing.T) {
			t.Parallel()
			f := newPromotionFixture(t, landLocally)
			moved := ""
			move := func() {
				moveTarget(t, f.repository, "elsewhere.txt")
				moved = f.local("main")
			}
			switch moment {
			case "during the checks":
				f.checks.before = func(call int) {
					if call == 1 {
						move()
					}
				}
			case "during the review":
				f.reviewer.before = func(call int) {
					if call == 1 {
						move()
					}
				}
			}
			first := f.verify()
			if moment == "after verification" {
				move()
			}
			if moment == "immediately before the move" {
				f.lander.beforeMove = move
			}
			promoted, err := f.promote()
			if err != nil {
				t.Fatalf("Promote() error = %v", err)
			}
			if moment == "during the checks" || moment == "during the review" {
				// The worker already refused the stale candidate and verified a
				// fresh one on the moved target; that one is what lands.
				if first.Number != 2 || first.TargetBase != moved || !promoted.Landed {
					t.Fatalf("generation %d on %s, promotion %#v; want generation 2 on the moved target, landed", first.Number, first.TargetBase, promoted)
				}
				f.assertLandedOnce(first)
				return
			}
			if !promoted.Drift || promoted.Landed {
				t.Fatalf("Promote() = %#v, want drift and nothing landed", promoted)
			}
			if f.local("main") != moved {
				t.Fatalf("main = %s, want it left where it moved to (%s)", f.local("main"), moved)
			}
			if generation := f.generations()[0]; generation.Invalidated == nil || generation.Invalidated.Reason != runstate.MergeQueueTargetMoved || generation.Invalidated.ObservedTarget != moved {
				t.Fatalf("generation 1 = %#v, want it invalidated by the move", generation.Invalidated)
			}
			if f.run.saveCount() != 0 {
				t.Fatal("drift wrote to the run, which is where a repair would be charged")
			}
			// A stale verdict authorizes nothing: promoting again refuses until a
			// fresh generation is verified, and then lands that one.
			again, err := f.promote()
			if err != nil || again.Refusal == "" || again.Landed {
				t.Fatalf("Promote() with only a stale generation = %#v, %v; want a refusal", again, err)
			}
			fresh := f.verify()
			if fresh.Number != 2 || fresh.TargetBase != moved {
				t.Fatalf("fresh generation %d on %s, want 2 on %s", fresh.Number, fresh.TargetBase, moved)
			}
			if _, err := f.promote(); err != nil {
				t.Fatalf("Promote() of the fresh generation error = %v", err)
			}
			if f.local("main") != fresh.Candidate {
				t.Fatalf("main = %s, want the fresh candidate", f.local("main"))
			}
			f.assertLandedOnce(fresh)
		})
	}
}

func TestACandidateReachingAProtectedPathTheItemDoesNotGrantLandsNothing(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	generation := f.verify()
	base := f.local("main")
	// The project keeps a protected home where the candidate's change lands.
	f.worker.Pipeline.Config.Product.Designs = "feature.txt"
	refused, err := f.promote()
	if err != nil || !strings.Contains(refused.Refusal, "feature.txt") || refused.Landed {
		t.Fatalf("Promote() = %#v, %v; want the protected path refused", refused, err)
	}
	attempt, _ := f.landing().Current()
	if f.local("main") != base || len(attempt.Mutations) != 0 {
		t.Fatalf("main %s, attempt %#v; want nothing moved or asked for", f.local("main"), attempt)
	}
	// The item granting the path is what lets the same candidate land.
	f.tracker.Item.Description += "\nprotected-path grant: feature.txt"
	landed, err := f.promote()
	if err != nil || !landed.Landed {
		t.Fatalf("Promote() with the path granted = %#v, %v", landed, err)
	}
	f.assertLandedOnce(generation)
}

func TestADriftLeftUnrecordedIsRecordedUnderThePromotionLease(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landUnprotected)
	generation := f.verify()
	base := f.local("main")
	// The target moves between the last read and the move, so the move is
	// refused; the record of the drift that follows is then lost.
	f.lander.beforeMove = func() { moveTarget(t, f.repository, "elsewhere.txt") }
	lost := false
	f.records.fail = func(g runstate.MergeQueueGeneration) (bool, error) {
		if g.Invalidated != nil && !lost {
			lost = true
			return false, errors.New("the disk refused the write")
		}
		return false, nil
	}
	if _, err := f.promote(); err == nil {
		t.Fatal("Promote() = nil, want the lost drift record reported")
	}
	attempt, _ := f.landing().Current()
	if record, found := attempt.Mutation(runstate.MergeQueueMoveTarget); !found || record.Settled == nil || record.Settled.Result != runstate.MergeQueueMutationNotMade || attempt.SetAside != nil {
		t.Fatalf("attempt = %#v, want the refused move recorded and the attempt still standing", attempt)
	}
	// The target comes back to the base, so taking up the drift catches the
	// local target up onto the remote, which it may do only under the lease.
	runPipelineGit(t, f.repository, "reset", "--hard", base)
	catchUps := f.catchUps
	drifted, err := f.promote()
	if err != nil || !drifted.Drift || drifted.Landed {
		t.Fatalf("Promote() = %#v, %v; want the drift recorded and nothing landed", drifted, err)
	}
	if f.catchUps == catchUps {
		t.Fatal("taking up the drift did not catch the target up, so the lease was not exercised")
	}
	if f.generations()[0].Invalidated == nil || f.local("main") != base || f.local("main") == generation.Candidate {
		t.Fatal("want generation 1 invalidated and main left at its base")
	}
}

func TestAProtectedTargetLandsThroughThePullRequestAndFollowsOnlyAfterConfirmation(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landProtected)
	generation := f.verify()
	base := f.local("main")
	f.forge.QueueMerge = true
	f.forge.OnMerge = func() {
		if f.local("main") != base {
			t.Errorf("main moved to %s before the forge was asked to merge", f.local("main"))
		}
		if !f.promotionLeaseHeld() {
			t.Error("the merge was asked for outside the promotion lease")
		}
	}

	waiting, err := f.promote()
	if err != nil || waiting.Waiting == "" || waiting.Landed {
		t.Fatalf("Promote() = %#v, %v; want the queued merge waited for", waiting, err)
	}
	// The forge's wait is not held under the promotion lease, and nothing local
	// moved while the forge has not merged.
	if f.promotionLeaseHeld() {
		t.Fatal("the promotion lease is held while the forge holds the merge queued")
	}
	if f.local("main") != base {
		t.Fatal("the protected target moved before its landing was confirmed")
	}
	// An entry whose promotion is under way is not verified again beside it.
	if verification, err := f.work(); err != nil || verification.Entry.EntryID != "" {
		t.Fatalf("Work() during the promotion = %#v, %v; want the entry left to its promotion", verification, err)
	}
	opened := f.forge.OpenedRequests()
	if len(opened) != 1 || opened[0].Head != runstate.MergeQueueCandidateBranch(f.entry.EntryID) || opened[0].Base != "main" {
		t.Fatalf("opened = %#v, want one pull request carrying the candidate branch", opened)
	}
	if merges := f.forge.MergeRequests(); len(merges) != 1 || merges[0].HeadCommit != generation.Candidate || merges[0].Method != publish.MergeCommit {
		t.Fatalf("merges = %#v, want one pinned to the candidate", merges)
	}
	if published, _, _ := f.lander.QueueCandidateBranchCommit(context.Background(), opened[0].Head); published != generation.Candidate {
		t.Fatalf("candidate branch carries %s, want the candidate", published)
	}

	f.forge.PerformQueuedMerge(t)
	landed, err := f.promote()
	if err != nil || !landed.Landed || !landed.Completed {
		t.Fatalf("Promote() after the merge = %#v, %v; want it landed and completed", landed, err)
	}
	remote := gitLine(t, f.remote, "rev-parse", "main")
	if parents := gitLine(t, f.remote, "rev-list", "--parents", "-n", "1", remote); parents != remote+" "+base+" "+generation.Candidate {
		t.Fatalf("the forge's merge has parents %q, want the base then the candidate itself", parents)
	}
	if f.local("main") != remote {
		t.Fatalf("main = %s, want it fast-forwarded onto the forge's landing %s", f.local("main"), remote)
	}
	if len(f.forge.MergeRequests()) != 1 {
		t.Fatal("the merge was asked for again")
	}
	completion := f.assertLandedOnce(generation).Completion
	if completion.Path != runstate.MergeQueueLandThroughPullRequest || completion.RemoteMerge != remote || completion.PullRequest == "" {
		t.Fatalf("completion = %#v, want the pull-request path and the forge's merge named", completion)
	}
}

func TestAnUnprotectedPublishedTargetMovesLocallyAndThenMergesThePullRequest(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landUnprotected)
	generation := f.verify()
	f.forge.OnMerge = func() {
		if f.local("main") != generation.Candidate {
			t.Errorf("main is at %s when the forge is asked to merge, want the candidate already promoted", f.local("main"))
		}
	}
	promoted, err := f.promote()
	if err != nil || !promoted.Landed || !promoted.Completed {
		t.Fatalf("Promote() = %#v, %v; want it landed", promoted, err)
	}
	remote := gitLine(t, f.remote, "rev-parse", "main")
	if f.local("main") != remote || gitLine(t, f.remote, "rev-parse", remote+"^2") != generation.Candidate {
		t.Fatalf("main = %s and the remote %s, want both on the forge's merge of the candidate", f.local("main"), remote)
	}
	if path := f.assertLandedOnce(generation).Completion.Path; path != runstate.MergeQueueLandLocallyThenPullRequest {
		t.Fatalf("path = %s", path)
	}
}

func TestAPromotionStoppedAroundAnyMutationIsFinishedWithoutAskingTwice(t *testing.T) {
	t.Parallel()

	cases := []struct {
		way      landingWay
		mutation runstate.MergeQueueMutation
		when     string
		// stuck is a mutation whose outcome observation cannot establish, which
		// is kept as it is rather than asked for again.
		stuck bool
	}{
		{landLocally, runstate.MergeQueueMoveTarget, "before", false},
		{landLocally, runstate.MergeQueueMoveTarget, "after", false},
		{landUnprotected, runstate.MergeQueuePushCandidate, "before", false},
		{landProtected, runstate.MergeQueuePushCandidate, "before", false},
		{landProtected, runstate.MergeQueuePushCandidate, "after", false},
		{landProtected, runstate.MergeQueueOpenPullRequest, "before", false},
		{landProtected, runstate.MergeQueueOpenPullRequest, "after", false},
		{landProtected, runstate.MergeQueueRequestMerge, "before", true},
		{landProtected, runstate.MergeQueueRequestMerge, "after", false},
		{landProtected, runstate.MergeQueueFollowTarget, "before", false},
		{landProtected, runstate.MergeQueueFollowTarget, "after", false},
	}
	for _, c := range cases {
		t.Run(string(c.mutation)+" "+c.when+map[landingWay]string{landLocally: " locally", landUnprotected: " unprotected", landProtected: " protected"}[c.way], func(t *testing.T) {
			t.Parallel()
			f := newPromotionFixture(t, c.way)
			generation := f.verify()
			if c.mutation == runstate.MergeQueueOpenPullRequest || c.mutation == runstate.MergeQueueRequestMerge {
				f.forge.stop[c.mutation] = c.when
			} else {
				f.lander.stop[c.mutation] = c.when
			}
			if _, err := f.promote(); !errors.Is(err, errStopped) {
				t.Fatalf("Promote() error = %v, want the stop", err)
			}
			// The record says the mutation was asked for, and not how it went.
			attempt, _ := f.landing().Current()
			record, unsettled := attempt.Unsettled()
			if !unsettled || record.Mutation != c.mutation {
				t.Fatalf("attempt = %#v, want the %s recorded and unsettled", attempt, c.mutation)
			}

			// The next process finishes it.
			again, err := f.promote()
			if c.stuck {
				if err != nil || again.Unresolved == "" || again.Landed {
					t.Fatalf("Promote() after the stop = %#v, %v; want the merge kept unresolved", again, err)
				}
				if len(f.forge.MergeRequests()) != 0 {
					t.Fatal("a merge whose request may have been lost was asked for again")
				}
				if third, err := f.promote(); err != nil || third.Unresolved == "" || len(f.forge.MergeRequests()) != 0 {
					t.Fatalf("Promote() again = %#v, %v; want it still kept as it is", third, err)
				}
				return
			}
			if err != nil || !again.Landed || !again.Completed {
				t.Fatalf("Promote() after the stop = %#v, %v; want it landed and completed", again, err)
			}
			landing := f.assertLandedOnce(generation)
			if len(landing.Recoveries) == 0 || landing.Recoveries[0].Mutation != c.mutation {
				t.Fatalf("recoveries = %#v, want the %s's recovery recorded", landing.Recoveries, c.mutation)
			}
			if f.forge != nil {
				if merges := f.forge.MergeRequests(); len(merges) != 1 {
					t.Fatalf("the forge was asked to merge %d times, want once", len(merges))
				}
				if opened := f.forge.OpenedRequests(); len(opened) > 2 {
					t.Fatalf("pull requests asked for %d times", len(opened))
				}
			}
		})
	}
}

func TestAMergeTheForgeMadeIsCompletedWhenItsRecordWasLost(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landProtected)
	generation := f.verify()
	// The forge merges, and the record of that answer never saves.
	lost := false
	f.landings.fail = func(landing runstate.MergeQueueLanding) (bool, error) {
		attempt, _ := landing.Current()
		record, found := attempt.Mutation(runstate.MergeQueueRequestMerge)
		if !lost && found && record.Settled != nil {
			lost = true
			return false, errors.New("the disk refused the write")
		}
		return false, nil
	}
	if _, err := f.promote(); err == nil {
		t.Fatal("Promote() = nil, want the failed record reported")
	}
	if len(f.forge.MergeRequests()) != 1 || !f.forge.Merged {
		t.Fatal("the forge did not merge")
	}
	landed, err := f.promote()
	if err != nil || !landed.Landed {
		t.Fatalf("Promote() = %#v, %v; want the merge found and the landing completed", landed, err)
	}
	f.assertLandedOnce(generation)
	if len(f.forge.MergeRequests()) != 1 {
		t.Fatal("the merge was asked for again")
	}
}

func TestAnUncertainSaveIsReadBackBeforeAnythingIsAskedFor(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	generation := f.verify()
	base := f.local("main")
	// The move is written down, the write says it may not have landed, and
	// nothing is moved on the strength of it.
	uncertain := false
	f.landings.fail = func(landing runstate.MergeQueueLanding) (bool, error) {
		attempt, _ := landing.Current()
		if _, found := attempt.Mutation(runstate.MergeQueueMoveTarget); found && !uncertain {
			uncertain = true
			return true, runstate.ErrMergeQueueLandingSaveUncertain
		}
		return false, nil
	}
	if _, err := f.promote(); !errors.Is(err, runstate.ErrMergeQueueLandingSaveUncertain) {
		t.Fatalf("Promote() error = %v, want the uncertain save", err)
	}
	if f.local("main") != base {
		t.Fatal("the target moved on a record that may not have saved")
	}
	landed, err := f.promote()
	if err != nil || !landed.Landed {
		t.Fatalf("Promote() = %#v, %v", landed, err)
	}
	landing := f.assertLandedOnce(generation)
	if len(landing.Attempts) != 2 || landing.Attempts[0].SetAside == nil || landing.Attempts[0].SetAside.Drift {
		t.Fatalf("attempts = %#v, want the interrupted one set aside, not as drift, and a second that landed", landing.Attempts)
	}
}

func TestAPartialCompletionMakesOnlyWhatIsMissing(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	generation := f.verify()
	f.run.failSaves = 1
	if _, err := f.promote(); err == nil {
		t.Fatal("Promote() = nil, want the run's refusal reported")
	}
	queued := f.landing().Completion
	if queued == nil || queued.RunAt != nil || queued.WorkItemAt != nil {
		t.Fatalf("completion = %#v, want only the queue's part made", queued)
	}
	// The run is recorded, and the record that says so is lost.
	lost := false
	f.landings.fail = func(landing runstate.MergeQueueLanding) (bool, error) {
		if c := landing.Completion; c != nil && c.RunAt != nil && !lost {
			lost = true
			return false, errors.New("the disk refused the write")
		}
		return false, nil
	}
	if _, err := f.promote(); err == nil {
		t.Fatal("Promote() = nil, want the lost record reported")
	}
	f.tracker.CompleteFailures, f.tracker.TransientCompleteErr = 1, errors.New("the tracker is busy")
	if _, err := f.promote(); err == nil {
		t.Fatal("Promote() = nil, want the work item's refusal reported")
	}
	// The refused closure changed nothing on the item; only the one that goes
	// through is counted.
	f.tracker.Calls = nil
	done, err := f.promote()
	if err != nil || !done.Completed {
		t.Fatalf("Promote() = %#v, %v", done, err)
	}
	landing := f.assertLandedOnce(generation)
	if !landing.Completion.QueueAt.Equal(queued.QueueAt) || len(landing.Attempts) != 1 {
		t.Fatalf("landing = %#v, want the one attempt and the first completion record kept", landing)
	}
}

func TestOnlyOneProcessPromotesAQueue(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	generation := f.verify()
	held, ok, err := f.queue.LeaseWorker(context.Background(), queueKey)
	if err != nil || !ok {
		t.Fatalf("LeaseWorker() = %v, %v", ok, err)
	}
	base := f.local("main")
	if _, err := f.promote(); !errors.Is(err, ErrMergeQueueWorkerBusy) {
		t.Fatalf("Promote() error = %v, want the queue reported busy", err)
	}
	if f.local("main") != base || len(f.landing().Attempts) != 0 {
		t.Fatal("a promotion that does not hold the queue moved or recorded something")
	}
	_ = held.Release()

	var wg sync.WaitGroup
	results := make([]error, 2)
	for index := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, results[index] = f.promoter.Promote(context.Background(), queueKey)
		}(index)
	}
	wg.Wait()
	for _, err := range results {
		if err != nil && !errors.Is(err, ErrMergeQueueWorkerBusy) {
			t.Fatalf("Promote() error = %v", err)
		}
	}
	f.assertLandedOnce(generation)
}

func TestWhatStopsARunsPromotionStopsTheQueues(t *testing.T) {
	t.Parallel()

	stops := map[string]func(f *promotionFixture){
		"the operator's pause": func(f *promotionFixture) {
			if _, err := f.worker.Pipeline.Holds.(*runstate.OperatorHoldStore).Hold(baseTime); err != nil {
				t.Fatal(err)
			}
		},
		"a dependency added since": func(f *promotionFixture) {
			f.tracker.Item.Dependencies = []beads.Dependency{{ID: "yoyodyne-other", Type: "blocks", Status: "open"}}
		},
		"integration approved by a person now": func(f *promotionFixture) {
			f.worker.Pipeline.Config.Approvals.Integration = domain.ApprovalHuman
		},
		"somebody's unsaved work in the primary checkout": func(f *promotionFixture) {
			writeQueueFile(t, f.repository, "other.txt", "somebody's unsaved edit\n")
		},
	}
	for name, stop := range stops {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newPromotionFixture(t, landLocally)
			f.verify()
			base := f.local("main")
			stop(f)
			refused, err := f.promote()
			if err != nil || refused.Refusal == "" || refused.Landed {
				t.Fatalf("Promote() = %#v, %v; want a refusal", refused, err)
			}
			attempt, _ := f.landing().Current()
			if f.local("main") != base || len(attempt.Mutations) != 0 {
				t.Fatalf("main %s, attempt %#v; want nothing moved or asked for", f.local("main"), attempt)
			}
			// Asking again refuses again, without intending another attempt.
			if again, err := f.promote(); err != nil || again.Refusal == "" || len(f.landing().Attempts) != 1 {
				t.Fatalf("Promote() again = %#v, %v with %d attempts; want the same refusal and one attempt", again, err, len(f.landing().Attempts))
			}
		})
	}
}

func TestTheForgesQueueIsHandedOnlyAnEntryItStillGatesOnTheCombinedCommit(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landProtected)
	// The fixture's own entry, in the harness's queue, lands first.
	first := f.verify()
	if done, err := f.promote(); err != nil || !done.Completed {
		t.Fatalf("Promote() of the harness entry = %#v, %v", done, err)
	}
	f.assertLandedOnce(first)
	f.forge.Merged, f.forge.Queued = false, false

	// A second change is admitted to the forge's own queue, through the
	// selector, which chooses it only because the forge gates the combined
	// commit it lands.
	runPipelineGit(t, f.repository, "switch", "-c", "second", "main")
	writeQueueFile(t, f.repository, "second.txt", "second change\n")
	runPipelineGit(t, f.repository, "add", "second.txt")
	runPipelineGit(t, f.repository, "commit", "-m", "the second approved change")
	head := gitLine(t, f.repository, "rev-parse", "HEAD")
	runPipelineGit(t, f.repository, "switch", "main")
	runPipelineGit(t, f.repository, "push", "origin", "refs/heads/second:refs/heads/second")
	harness := queuemode.Harness{PullRequests: true, CheckConfiguration: "the project's checks"}
	forgeQueue := &nativeQueue{capabilities: qualifyingQueue(harness)}
	f.promoter.Forge, f.promoter.Harness = forgeQueue, harness
	runID, _ := runstate.NewRunID()
	admitted, err := (queuemode.Admitter{Queue: f.queue, Forge: forgeQueue, Harness: harness}).Admit(context.Background(), runstate.MergeQueueAdmission{
		Key: queueKey, WorkItemID: "yoyodyne-task", WorkItemTitle: "Add the feature", RunID: runID,
		ApprovedHead: head, IntegrationPolicy: "automatic",
	})
	if err != nil || admitted.Entry.Mode != runstate.MergeQueueForge {
		t.Fatalf("Admit() = %#v, %v; want the forge's queue chosen", admitted, err)
	}
	f.run.admittedRun.state = runstate.State{RunID: runID, WorkItemID: "yoyodyne-task", Branch: "second"}
	f.run.saves = nil
	f.tracker.Item.Status, f.tracker.Closed, f.tracker.Calls = "in_progress", false, nil

	// The forge now approves only the pull request's head: nothing is handed to
	// it, and nothing is asked of it.
	forgeQueue.capabilities = headBoundQueue(harness)
	merges := len(f.forge.MergeRequests())
	refused, err := f.promote()
	if err != nil || refused.Refusal == "" || refused.Entry.EntryID != admitted.Entry.EntryID {
		t.Fatalf("Promote() = %#v, %v; want the forge's queue refused", refused, err)
	}
	if len(f.forge.MergeRequests()) != merges {
		t.Fatal("a merge was asked of a queue that approves only the pull request's head")
	}

	forgeQueue.capabilities = qualifyingQueue(harness)
	f.forge.QueueMerge = true
	waiting, err := f.promote()
	if err != nil || waiting.Waiting == "" {
		t.Fatalf("Promote() = %#v, %v; want the forge's queue waited on", waiting, err)
	}
	last := f.forge.MergeRequests()[len(f.forge.MergeRequests())-1]
	if last.HeadCommit != head || f.forge.OpenedRequests()[len(f.forge.OpenedRequests())-1].Head != "second" {
		t.Fatalf("merge %#v, want the run's own branch handed over pinned to the approved head", last)
	}
	f.forge.PerformQueuedMerge(t)
	landed, err := f.promote()
	if err != nil || !landed.Landed || !landed.Completed || landed.Attempt.Path != runstate.MergeQueueLandThroughForgeQueue {
		t.Fatalf("Promote() = %#v, %v; want the forge's landing confirmed and completed", landed, err)
	}
	if held, err := f.lander.TargetHolds(context.Background(), "main", head); err != nil || !held {
		t.Fatalf("main holds the forge's landing: %v, %v", held, err)
	}
	// What landed is the combined commit the forge's queue built and gated,
	// never the head it was handed, and the record and the item say so.
	forgeLanded := gitLine(t, f.remote, "rev-parse", "main")
	forgeLanding, _, err := f.queue.Landing(queueKey, admitted.Entry.EntryID)
	if err != nil {
		t.Fatal(err)
	}
	completion := forgeLanding.Completion
	if forgeLanded == head || completion == nil || completion.Landed != forgeLanded || completion.RemoteMerge != forgeLanded {
		t.Fatalf("completion = %#v, want the forge's landed commit %s rather than the head %s", completion, forgeLanded, head)
	}
	if integration := f.run.latest().Integration; integration == nil || integration.TargetCommit != forgeLanded || integration.SourceCommit != forgeLanded {
		t.Fatalf("run integration = %#v, want the forge's commit landed", integration)
	}
	if !strings.Contains(f.tracker.CloseReason, forgeLanded) || !strings.Contains(f.tracker.CloseReason, "forge's merge queue") {
		t.Fatalf("close reason = %q, want it to name the forge's landed commit", f.tracker.CloseReason)
	}
	if f.run.saveCount() != 1 || !f.tracker.Closed {
		t.Fatal("the forge's landing was not recorded on the run and the item")
	}
}

// nativeQueue is a forge adapter whose own queue answers as the test says.
type nativeQueue struct {
	capabilities func(branch string) publish.QueueCapabilities
}

func (n *nativeQueue) QueueCapabilities(_ context.Context, branch string) (publish.QueueCapabilities, error) {
	return n.capabilities(branch), nil
}

func qualifyingQueue(harness queuemode.Harness) func(string) publish.QueueCapabilities {
	return func(branch string) publish.QueueCapabilities {
		observed := publish.QueueCapabilities{Forge: "forge", TargetBranch: branch, ObservedAt: time.Now(), Protected: true, QueueAvailable: true, TimeoutMinutes: 60}
		for _, requirement := range publish.QueueRequirements() {
			evidence := publish.QueueRequirementEvidence{Requirement: requirement, Established: true, Enforced: true}
			if requirement == publish.QueueRequiredChecks || requirement == publish.QueueIndependentApproval {
				evidence.BoundTo = publish.BoundToCandidate
			}
			if requirement == publish.QueueRequiredChecks {
				evidence.Configuration = harness.CheckConfiguration
			}
			observed.Requirements = append(observed.Requirements, evidence)
		}
		return observed
	}
}

func headBoundQueue(harness queuemode.Harness) func(string) publish.QueueCapabilities {
	return func(branch string) publish.QueueCapabilities {
		observed := qualifyingQueue(harness)(branch)
		for index := range observed.Requirements {
			if observed.Requirements[index].Requirement == publish.QueueIndependentApproval {
				observed.Requirements[index].BoundTo = publish.BoundToPullRequestHead
			}
		}
		return observed
	}
}

func TestTheRunCompletionMakesItsRecordsOnce(t *testing.T) {
	t.Parallel()

	runID, _ := runstate.NewRunID()
	run := &recordingRun{admittedRun: &admittedRun{state: runstate.State{RunID: runID, WorkItemID: "yoyodyne-task"}}}
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Status: "in_progress"}}
	completion := MergeQueueRunCompletion{Store: run, Tracker: tracker}
	entry := runstate.MergeQueueEntry{RunID: runID, WorkItemID: "yoyodyne-task", TargetBranch: "main", ApprovedHead: strings.Repeat("a", 40)}
	landed := runstate.MergeQueueCompletion{Path: runstate.MergeQueueLandThroughPullRequest, TargetBase: strings.Repeat("b", 40), Landed: strings.Repeat("c", 40)}
	for range 3 {
		if err := completion.RecordRun(context.Background(), entry, landed); err != nil {
			t.Fatal(err)
		}
		if err := completion.RecordWorkItem(context.Background(), entry, landed); err != nil {
			t.Fatal(err)
		}
	}
	// The landing is confirmed on the target before it is recorded, so the
	// run's integration is recorded settled rather than waiting on a forge.
	if run.saveCount() != 1 || run.latest().Integration.ThroughPullRequest || run.latest().Integration.TargetCommit != landed.Landed {
		t.Fatalf("run saved %d times as %#v, want once, recorded settled", run.saveCount(), run.latest().Integration)
	}
	if strings.Count(strings.Join(tracker.Calls, " "), "complete") != 1 {
		t.Fatalf("tracker calls = %v, want one completion", tracker.Calls)
	}
}

func TestALandingThatDoesNotDischargeItsItemLeavesItOpen(t *testing.T) {
	t.Parallel()

	runID, _ := runstate.NewRunID()
	run := &recordingRun{admittedRun: &admittedRun{state: runstate.State{
		RunID: runID, WorkItemID: "yoyodyne-task",
		LandingOutcome: runstate.LandingEvidence, LandingReason: "the diagnosis is landed; the fix waits on the store's new schema",
	}}}
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Status: "in_progress"}}
	completion := MergeQueueRunCompletion{Store: run, Tracker: tracker}
	entry := runstate.MergeQueueEntry{RunID: runID, WorkItemID: "yoyodyne-task", TargetBranch: "main", ApprovedHead: strings.Repeat("a", 40)}
	if err := completion.RecordWorkItem(context.Background(), entry, runstate.MergeQueueCompletion{Landed: strings.Repeat("c", 40)}); err != nil {
		t.Fatalf("RecordWorkItem() error = %v", err)
	}
	if tracker.Closed || !tracker.Reopened {
		t.Fatalf("closed %v, reopened %v; want an item whose change is evidence put back rather than closed", tracker.Closed, tracker.Reopened)
	}
}

// recordingRun is the entry's run, recording each save of it and refusing the
// first failSaves.
type recordingRun struct {
	*admittedRun
	mu        sync.Mutex
	saves     []runstate.State
	failSaves int
}

func (r *recordingRun) Save(state runstate.State) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failSaves > 0 {
		r.failSaves--
		return errors.New("the run store refused the write")
	}
	if _, other := r.admittedRun.others[state.RunID]; other {
		r.admittedRun.others[state.RunID] = state
		return nil
	}
	r.saves = append(r.saves, state)
	r.admittedRun.state = state
	return nil
}

func (r *recordingRun) saveCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.saves)
}

func (r *recordingRun) latest() runstate.State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.admittedRun.state
}

// flakyLandings fails the landing writes fail picks: write is whether the
// record lands anyway, and the error is what the writer is told.
type flakyLandings struct {
	*flakyGenerations
	fail func(runstate.MergeQueueLanding) (write bool, err error)
}

func (f *flakyLandings) RecordLanding(lease *runstate.Lease, key runstate.MergeQueueKey, landing runstate.MergeQueueLanding) error {
	if f.fail != nil {
		if write, err := f.fail(landing); err != nil {
			if write {
				if saved := f.MergeQueueStore.RecordLanding(lease, key, landing); saved != nil {
					return saved
				}
			}
			return err
		}
	}
	return f.MergeQueueStore.RecordLanding(lease, key, landing)
}

// stoppingLander is the worktree manager with a process that stops before or
// after the mutation a test names, once.
type stoppingLander struct {
	*gitworktree.Manager
	stop       map[runstate.MergeQueueMutation]string
	beforeMove func()
	// onCatchUp runs whenever the local target is about to be caught up,
	// which is how every test checks that it is moved under the lease.
	onCatchUp func()
}

func (l *stoppingLander) at(mutation runstate.MergeQueueMutation) string {
	when := l.stop[mutation]
	delete(l.stop, mutation)
	return when
}

func (l *stoppingLander) PromoteQueueCandidate(ctx context.Context, branch, base, candidate string) error {
	if l.beforeMove != nil {
		l.beforeMove()
		l.beforeMove = nil
	}
	return stopAround(l.at(runstate.MergeQueueMoveTarget), func() error { return l.Manager.PromoteQueueCandidate(ctx, branch, base, candidate) })
}

func (l *stoppingLander) PublishQueueCandidate(ctx context.Context, branch, candidate, expected string) error {
	return stopAround(l.at(runstate.MergeQueuePushCandidate), func() error { return l.Manager.PublishQueueCandidate(ctx, branch, candidate, expected) })
}

func (l *stoppingLander) CatchUpTarget(ctx context.Context, branch string) (gitworktree.Catchup, error) {
	if l.onCatchUp != nil {
		l.onCatchUp()
	}
	var catchup gitworktree.Catchup
	err := stopAround(l.at(runstate.MergeQueueFollowTarget), func() error {
		var err error
		catchup, err = l.Manager.CatchUpTarget(ctx, branch)
		return err
	})
	return catchup, err
}

// stoppingForge is the forge with a process that stops before or after the
// request a test names, once.
type stoppingForge struct {
	*orchestratortest.Forge
	stop map[runstate.MergeQueueMutation]string
}

func (f *stoppingForge) at(mutation runstate.MergeQueueMutation) string {
	when := f.stop[mutation]
	delete(f.stop, mutation)
	return when
}

func (f *stoppingForge) Ensure(ctx context.Context, request publish.Request) (publish.PullRequest, error) {
	var opened publish.PullRequest
	err := stopAround(f.at(runstate.MergeQueueOpenPullRequest), func() error {
		var err error
		opened, err = f.Forge.Ensure(ctx, request)
		return err
	})
	return opened, err
}

func (f *stoppingForge) Merge(ctx context.Context, request publish.MergeRequest) (publish.MergeResult, error) {
	var result publish.MergeResult
	err := stopAround(f.at(runstate.MergeQueueRequestMerge), func() error {
		var err error
		result, err = f.Forge.Merge(ctx, request)
		return err
	})
	return result, err
}

func stopAround(when string, do func() error) error {
	switch when {
	case "before":
		return errStopped
	case "after":
		if err := do(); err != nil {
			return err
		}
		return errStopped
	}
	return do()
}
