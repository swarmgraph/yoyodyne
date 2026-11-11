package orchestrator

// Checking the next merge queue candidate while the current one is still being
// checked (mergequeueahead.go): a candidate checked ahead lands only once the
// candidate before it landed as exactly the commit it was built on, and is
// thrown away and built again on the real target, with nothing charged to its
// change, when the one before it fails, is withdrawn, or lands as another
// commit. The project's checks here are shell commands over its own files.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// aheadFixture is a queue landing locally with two admitted changes, the
// fixture's own first and another behind it, checked by checks expects.
func aheadFixture(t *testing.T, checks ...string) (*promotionFixture, runstate.MergeQueueEntry) {
	t.Helper()
	f := newPromotionFixture(t, landLocally)
	f.worker.CheckAhead = true
	f.worker.Pipeline.Config.Checks = checks
	second := f.admitAnother(queueKey, "second", "second.txt", "the second change\n")
	return f, second
}

func (f *promotionFixture) generationsOf(entry runstate.MergeQueueEntry) []runstate.MergeQueueGeneration {
	f.t.Helper()
	generations, err := f.queue.Generations(queueKey, entry.EntryID)
	if err != nil {
		f.t.Fatalf("Generations() error = %v", err)
	}
	return generations
}

func (f *promotionFixture) landingOf(entry runstate.MergeQueueEntry) runstate.MergeQueueLanding {
	f.t.Helper()
	landing, _, err := f.queue.Landing(queueKey, entry.EntryID)
	if err != nil {
		f.t.Fatalf("Landing() error = %v", err)
	}
	return landing
}

// assertRebuiltUncharged checks the entry's candidate checked ahead was thrown
// away as a base that did not land, a candidate built on the real target
// landed in its place, and nothing about it was handed back or charged.
func (f *promotionFixture) assertRebuiltUncharged(entry runstate.MergeQueueEntry, ahead string) {
	f.t.Helper()
	generations := f.generationsOf(entry)
	if len(generations) != 2 {
		f.t.Fatalf("generations of the entry behind = %d, want the one checked ahead and its rebuild", len(generations))
	}
	first, second := generations[0], generations[1]
	if first.BuiltAhead == nil || first.TargetBase != ahead || first.Invalidated == nil || first.Invalidated.Reason != runstate.MergeQueueBaseNotLanded {
		f.t.Fatalf("generation 1 = built ahead %#v on %s, invalidated %#v; want it built on %s and thrown away because that did not land", first.BuiltAhead, first.TargetBase, first.Invalidated, ahead)
	}
	if second.BuiltAhead != nil || second.TargetBase == ahead {
		f.t.Fatalf("generation 2 = built ahead %#v on %s, want it built on the real target", second.BuiltAhead, second.TargetBase)
	}
	landing := f.landingOf(entry)
	if landing.Handback != nil {
		f.t.Fatalf("handback = %#v, want nothing handed back for a base that did not land", landing.Handback)
	}
	if landing.Completion == nil || !landing.Completion.Whole() || landing.Completion.Landed != second.Candidate || landing.Completion.TargetBase != second.TargetBase {
		f.t.Fatalf("completion = %#v, want the rebuilt candidate landed", landing.Completion)
	}
	if run := f.run.admittedRun.others[entry.RunID]; run.RepairAttempts != 0 {
		f.t.Fatalf("the run behind spent %d repair attempts, want none", run.RepairAttempts)
	}
}

func TestACandidateCheckedAheadLandsOnceTheOneBeforeItLandsAsItself(t *testing.T) {
	t.Parallel()

	f, second := aheadFixture(t, "test -f other.txt")
	// The first check stage waits for the second to start, so the two can only
	// both finish if they run side by side.
	together := make(chan struct{})
	overlapped := false
	f.checks.before = func(call int) {
		switch call {
		case 1:
			select {
			case <-together:
				overlapped = true
			case <-time.After(30 * time.Second):
			}
		case 2:
			close(together)
		}
	}
	base := f.local("main")
	verification, err := f.work()
	if err != nil || !verification.Verified || verification.Ahead == nil || !verification.Ahead.Verified || verification.Ahead.Entry.EntryID != second.EntryID {
		t.Fatalf("Work() = %#v (ahead %#v), %v; want the first verified and the second verified ahead of it", verification, verification.Ahead, err)
	}
	if !overlapped {
		t.Fatal("the candidate checked ahead did not run beside the current one")
	}
	current := verification.Generation
	ahead := verification.Ahead.Generation
	if current.TargetBase != base || ahead.TargetBase != current.Candidate || ahead.BuiltAhead == nil ||
		ahead.BuiltAhead.Candidate != current.Candidate || ahead.BuiltAhead.OnTarget != base || ahead.BuiltAhead.EntryID != f.entry.EntryID {
		t.Fatalf("ahead generation base %s built ahead %#v, want it on the current candidate %s while the target is at %s", ahead.TargetBase, ahead.BuiltAhead, current.Candidate, base)
	}
	if parents := gitLine(t, f.repository, "rev-list", "--parents", "-n", "1", ahead.Candidate); !strings.HasPrefix(parents, ahead.Candidate+" "+current.Candidate+" ") {
		t.Fatalf("ahead candidate parents = %q, want the current candidate first", parents)
	}
	if !strings.Contains(f.reviewer.last().Context, "built ahead") && !strings.Contains(f.reviewer.requests[0].Context, "built ahead") {
		t.Fatal("the reviewer of the candidate checked ahead was not told what it stands on")
	}

	pass := f.pass(queueKey)
	if pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want both changes landed", pass)
	}
	// The candidate checked ahead landed as it was checked: nothing was built,
	// checked or reviewed again.
	if f.checks.calls() != 2 || f.reviewer.calls() != 2 {
		t.Fatalf("checks ran %d times and reviews %d, want one of each per change", f.checks.calls(), f.reviewer.calls())
	}
	if f.local("main") != ahead.Candidate {
		t.Fatalf("main = %s, want the candidate checked ahead, %s", f.local("main"), ahead.Candidate)
	}
	landed := f.landingOf(second)
	if landed.Completion == nil || landed.Completion.Landed != ahead.Candidate || landed.Completion.Binding != ahead.Binding() || landed.Completion.TargetBase != current.Candidate {
		t.Fatalf("completion = %#v, want the candidate checked ahead landed on the one before it", landed.Completion)
	}
	if generations := f.generationsOf(second); len(generations) != 1 || generations[0].Invalidated != nil {
		t.Fatalf("generations of the entry behind = %#v, want the one checked ahead and nothing else", generations)
	}
}

func TestACandidateCheckedAheadIsRebuiltUnchargedWhenTheOneBeforeItFails(t *testing.T) {
	t.Parallel()

	// The first change adds feature.txt, which this check refuses; the second
	// does not, so its candidate built ahead fails too, only because of the
	// first.
	f, second := aheadFixture(t, "test ! -f feature.txt")
	f.run.admittedRun.state.Branch = "change"
	f.worker.Pipeline.Config.Execution.RepairAttemptsBeforeReplan = 2
	repair := &recordingRepair{}
	f.promoter.Repair = repair
	base := f.local("main")
	verification, err := f.work()
	if err != nil || verification.Verified || verification.Ahead == nil {
		t.Fatalf("Work() = %#v, %v; want the first refused and the second checked ahead", verification, err)
	}
	ahead := verification.Generation.Candidate

	pass := f.pass(queueKey)
	if pass.Outcome != runstate.MergeQueuePassHandedBack {
		t.Fatalf("Pass() = %#v, want the first handed back and the second landed", pass)
	}
	if repair.count() != 1 || repair.given[0].Continuation != runstate.MergeQueueContinueRepair {
		t.Fatalf("repair given %#v, want only the failing change handed back", repair.given)
	}
	if f.landing().Handback == nil {
		t.Fatal("the change whose own candidate failed was not handed back")
	}
	f.assertRebuiltUncharged(second, ahead)
	if rebuilt := f.generationsOf(second)[1]; rebuilt.TargetBase != base || f.local("main") != rebuilt.Candidate {
		t.Fatalf("the rebuilt candidate stands on %s and main is %s, want it on %s and landed", rebuilt.TargetBase, f.local("main"), base)
	}
}

func TestACandidateCheckedAheadIsRebuiltUnchargedWhenTheOneBeforeItIsWithdrawn(t *testing.T) {
	t.Parallel()

	f, second := aheadFixture(t, "test -f other.txt")
	base := f.local("main")
	verification, err := f.work()
	if err != nil || !verification.Verified || verification.Ahead == nil || !verification.Ahead.Verified {
		t.Fatalf("Work() = %#v, %v; want both verified", verification, err)
	}
	withdrawn, err := f.promoter.Withdraw(context.Background(), queueKey, f.entry.EntryID, "its head is to be rewritten")
	if err != nil || !withdrawn.Withdrawn {
		t.Fatalf("Withdraw() = %#v, %v; want the first entry withdrawn", withdrawn, err)
	}
	if pass := f.pass(queueKey); pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want the second landed", pass)
	}
	f.assertRebuiltUncharged(second, verification.Generation.Candidate)
	if rebuilt := f.generationsOf(second)[1]; rebuilt.TargetBase != base {
		t.Fatalf("the rebuilt candidate stands on %s, want the target the withdrawn one never moved, %s", rebuilt.TargetBase, base)
	}
}

func TestACandidateCheckedAheadIsRebuiltUnchargedWhenTheOneBeforeItLandsAsAnotherCommit(t *testing.T) {
	t.Parallel()

	f, second := aheadFixture(t, "test -f other.txt")
	verification, err := f.work()
	if err != nil || !verification.Verified || verification.Ahead == nil || !verification.Ahead.Verified {
		t.Fatalf("Work() = %#v, %v; want both verified", verification, err)
	}
	// The target comes to land through a pull request, which the forge merges
	// with a merge commit of its own above the candidate.
	f.remote = addBareRemote(t, f.repository)
	f.forge = &stoppingForge{Forge: &orchestratortest.Forge{Remote: f.remote, TargetProtection: publish.BranchProtection{Protected: true, By: "a ruleset"}}, stop: map[runstate.MergeQueueMutation]string{}}
	*f.worker.Pipeline = publishing(*f.worker.Pipeline, f.forge)

	if pass := f.pass(queueKey); pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want both landed", pass)
	}
	first := f.landing().Completion
	if first == nil || first.Landed != verification.Generation.Candidate {
		t.Fatalf("first completion = %#v, want the first candidate landed", first)
	}
	f.assertRebuiltUncharged(second, verification.Generation.Candidate)
	rebuilt := f.generationsOf(second)[1]
	if rebuilt.TargetBase == first.Landed || f.generationsOf(second)[0].Invalidated.ObservedTarget != rebuilt.TargetBase {
		t.Fatalf("rebuilt on %s, want it on the forge's merge commit rather than the candidate %s", rebuilt.TargetBase, first.Landed)
	}
}

func TestNothingIsCheckedAheadOnATargetThatLandsThroughAPullRequest(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landProtected)
	f.worker.CheckAhead = true
	f.worker.Pipeline.Config.Checks = []string{"test -f other.txt"}
	second := f.admitAnother(queueKey, "second", "second.txt", "the second change\n")
	verification, err := f.work()
	if err != nil || !verification.Verified || verification.Ahead == nil || !strings.Contains(verification.Ahead.Waiting, "merge commit of its own") {
		t.Fatalf("Work() = %#v (ahead %#v), %v; want the first verified and the reason nothing was checked ahead", verification, verification.Ahead, err)
	}
	if generations := f.generationsOf(second); len(generations) != 0 {
		t.Fatalf("generations of the entry behind = %#v, want none built", generations)
	}
}
