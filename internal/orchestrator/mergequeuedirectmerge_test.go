package orchestrator

// The merge step of "When main moves after a candidate was sent to GitHub"
// (docs/designs/integration-through-a-merge-queue.md): a candidate's merge is
// asked for directly, never with merging when ready, under the promotion lease
// and only after main was read at the candidate's base; GitHub refusing a head
// behind main is a moved main, charged to nothing.

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// newDirectMergeFixture is a protected target whose forge reports the
// candidate's pull request ready to merge.
func newDirectMergeFixture(t *testing.T) *promotionFixture {
	t.Helper()
	f := newPromotionFixture(t, landProtected)
	f.forge.MergeStatus = "CLEAN"
	f.promoter.Mergeable = f.forge.Forge
	return f
}

func TestTheQueueAsksGitHubToMergeACandidateDirectlyAfterReadingMainAtItsBase(t *testing.T) {
	t.Parallel()

	f := newDirectMergeFixture(t)
	generation := f.verify()
	base := f.local("main")
	f.forge.OnMerge = func() {
		if !f.promotionLeaseHeld() {
			t.Error("the merge was asked for outside the promotion lease")
		}
		if remote := gitLine(t, f.remote, "rev-parse", "main"); remote != base {
			t.Errorf("the merge was asked for with the remote main at %s, want the candidate's base %s", remote, base)
		}
		if f.forge.MergeStates != 1 {
			t.Errorf("GitHub was asked whether it would merge %d times before the merge, want once", f.forge.MergeStates)
		}
	}

	landed, err := f.promote()
	if err != nil || !landed.Landed || !landed.Completed {
		t.Fatalf("Promote() = %#v, %v; want the candidate merged directly and landed", landed, err)
	}
	merges := f.forge.MergeRequests()
	if len(merges) != 1 || !merges[0].Directly || merges[0].HeadCommit != generation.Candidate {
		t.Fatalf("merges = %#v, want one direct merge pinned to the candidate", merges)
	}
	if f.forge.Queued {
		t.Fatal("the forge holds a merge for later, want none asked for")
	}
	f.assertLandedOnce(generation)
}

func TestTheQueueAsksForNoMergeWhenMainHasMovedOffTheCandidatesBase(t *testing.T) {
	t.Parallel()

	f := newDirectMergeFixture(t)
	f.verify()
	pushUnrelatedCommit(t, f.remote)

	drifted, err := f.promote()
	if err != nil || !drifted.Drift || drifted.Landed {
		t.Fatalf("Promote() = %#v, %v; want the moved main read as drift", drifted, err)
	}
	if merges := f.forge.MergeRequests(); len(merges) != 0 {
		t.Fatalf("merges = %#v, want none asked for on a moved main", merges)
	}
	for _, attempt := range f.landing().Attempts {
		if _, asked := attempt.Mutation(runstate.MergeQueueRequestMerge); asked {
			t.Fatalf("attempt = %#v, want no merge written down", attempt)
		}
	}
	if f.generations()[0].Invalidated == nil {
		t.Fatal("generation 1 stands on a main that moved")
	}
	if f.run.saveCount() != 0 {
		t.Fatal("the moved main wrote to the run, which is where a repair would be charged")
	}
}

func TestGitHubRefusingACandidateBehindMainIsAMovedMainChargedNothing(t *testing.T) {
	t.Parallel()

	f := newDirectMergeFixture(t)
	f.verify()
	// Another merge lands in the moment between the read of main and the
	// merge, and GitHub refuses the candidate as not up to date.
	f.forge.OnMerge = func() {
		f.forge.OnMerge = nil
		pushUnrelatedCommit(t, f.remote)
		f.forge.MergeErr = publish.MergeRefused{Number: 1, Method: publish.MergeCommit, Status: "BEHIND", Behind: true,
			Reason: "Head branch is not up to date with the base branch"}
	}

	drifted, err := f.promote()
	if err != nil || !drifted.Drift || drifted.Landed || drifted.Unresolved != "" {
		t.Fatalf("Promote() = %#v, %v; want the refusal read as a moved main", drifted, err)
	}
	moved := gitLine(t, f.remote, "rev-parse", "main")
	landing := f.landing()
	attempt := landing.Attempts[len(landing.Attempts)-1]
	record, asked := attempt.Mutation(runstate.MergeQueueRequestMerge)
	if !asked || record.Settled == nil || record.Settled.Result != runstate.MergeQueueMutationNotMade {
		t.Fatalf("merge record = %#v, want the refused merge settled as not made", record)
	}
	if attempt.SetAside == nil || !attempt.SetAside.Drift {
		t.Fatalf("attempt set aside = %#v, want it set aside as drift", attempt.SetAside)
	}
	if generation := f.generations()[0]; generation.Invalidated == nil || generation.Invalidated.ObservedTarget != moved {
		t.Fatalf("generation 1 invalidation = %#v, want it naming the main it moved to (%s)", generation.Invalidated, moved)
	}
	if f.run.saveCount() != 0 || landing.Handback != nil {
		t.Fatal("the refusal wrote to the run or handed the entry back, which is where a repair would be charged")
	}

	// The candidate is built again on main as it stands, and that one lands.
	f.forge.MergeErr = nil
	fresh := f.verify()
	if fresh.Number != 2 || fresh.TargetBase != moved {
		t.Fatalf("fresh generation %d on %s, want 2 on the moved main %s", fresh.Number, fresh.TargetBase, moved)
	}
	if landed, err := f.promote(); err != nil || !landed.Landed {
		t.Fatalf("Promote() of the rebuilt candidate = %#v, %v; want it landed", landed, err)
	}
	for _, merge := range f.forge.MergeRequests() {
		if !merge.Directly {
			t.Fatalf("merges = %#v, want every one asked for directly", f.forge.MergeRequests())
		}
	}
}

// A pull request GitHub would not merge yet is waited for with nothing asked
// for or written down, and one it reads as behind main is a moved main.
func TestTheQueueAsksForNoMergeGitHubWouldNotMakeNow(t *testing.T) {
	t.Parallel()

	t.Run("checks still running", func(t *testing.T) {
		t.Parallel()
		f := newDirectMergeFixture(t)
		generation := f.verify()
		f.forge.MergeStatus = "BLOCKED"
		waiting, err := f.promote()
		if err != nil || !strings.Contains(waiting.Waiting, "BLOCKED") || waiting.Landed || waiting.Drift {
			t.Fatalf("Promote() = %#v, %v; want it waiting on GitHub", waiting, err)
		}
		attempt, _ := f.landing().Current()
		if _, asked := attempt.Mutation(runstate.MergeQueueRequestMerge); asked || len(f.forge.MergeRequests()) != 0 {
			t.Fatalf("attempt = %#v, merges %#v; want no merge asked for or written down", attempt, f.forge.MergeRequests())
		}
		f.forge.MergeStatus = "CLEAN"
		if landed, err := f.promote(); err != nil || !landed.Landed {
			t.Fatalf("Promote() once GitHub would merge = %#v, %v; want it landed", landed, err)
		}
		f.assertLandedOnce(generation)
	})

	t.Run("behind main", func(t *testing.T) {
		t.Parallel()
		f := newDirectMergeFixture(t)
		f.verify()
		f.forge.MergeStatus = "BEHIND"
		drifted, err := f.promote()
		if err != nil || !drifted.Drift || drifted.Landed {
			t.Fatalf("Promote() = %#v, %v; want GitHub's reading taken as a moved main", drifted, err)
		}
		if len(f.forge.MergeRequests()) != 0 || f.run.saveCount() != 0 {
			t.Fatal("a merge was asked for, or the run charged, on a pull request behind main")
		}
	})
}
