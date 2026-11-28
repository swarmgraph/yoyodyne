package orchestrator

// A candidate's checks cut off part-way are taken up again only after main is
// fetched: a candidate whose base main moved off on the remote meanwhile is
// built again on main as it stands, and one whose base main still stands on
// has only its cut-off checks run again. The pass's record says which.

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// cutOffFixture is a queue whose target has a remote, and whose worker
// fetches it, with entry 1's first candidate's checks cut off by a restart
// after the first check passed.
func cutOffFixture(t *testing.T) (*promotionFixture, runstate.MergeQueueGeneration) {
	t.Helper()
	f := newPromotionFixture(t, landUnprotected)
	f.worker.Fetch = f.worker.Pipeline.Worktrees.(*gitworktree.Manager)
	stages := &queueCheckStages{}
	ctx, cancel := context.WithTimeout(withQueueCheckStages(context.Background(), stages), 5*time.Minute)
	defer cancel()
	f.checks.finished = func(call int, result checks.Result) {
		if call == 1 && result.Command == queueChecks[0] {
			stages.close()
			cancel()
		}
	}
	if cut := f.driver().Pass(ctx, queueKey); cut.Outcome != runstate.MergeQueuePassWaiting {
		t.Fatalf("Pass() cut off by the restart = %#v, want it waiting on the next pass", cut)
	}
	f.checks.finished = nil
	cut := f.generations()[0]
	if cut.CheckRun == nil || !strings.Contains(cut.CheckRun.Problem, "cut off by the restart") {
		t.Fatalf("checks after the cut = %#v, want them recorded as cut off by the restart", cut.CheckRun)
	}
	return f, cut
}

// pushToRemoteMain lands a commit on the remote's main from another clone, as a
// merge made on GitHub does, leaving the local main where it was.
func pushToRemoteMain(t *testing.T, remote, file string) string {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "elsewhere")
	runPipelineGit(t, filepath.Dir(clone), "clone", remote, clone)
	runPipelineGit(t, clone, "config", "user.name", "Someone Else")
	runPipelineGit(t, clone, "config", "user.email", "someone@example.invalid")
	disablePipelineMaintenance(t, clone)
	moveTarget(t, clone, file)
	runPipelineGit(t, clone, "push", "origin", "HEAD:refs/heads/main")
	return gitLine(t, clone, "rev-parse", "HEAD")
}

func TestCutOffChecksWhoseBaseMainMovedOffOnTheRemoteAreNotRunAgainOnTheOldCandidate(t *testing.T) {
	t.Parallel()

	f, cut := cutOffFixture(t)
	moved := pushToRemoteMain(t, f.remote, "landed-elsewhere.txt")
	if f.local("main") == moved {
		t.Fatal("the local main followed the remote before the queue fetched it")
	}

	landed := f.passAgain(queueKey)
	if landed.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() on the new build = %#v, want the change landed", landed)
	}
	// The old candidate's cut-off check never ran again: the next checks were
	// the whole list, on a candidate built on the main the remote moved to.
	if ran := f.checks.ran(); len(ran) != 2 || !slices.Equal(ran[0], queueChecks) || !slices.Equal(ran[1], queueChecks) {
		t.Fatalf("checks ran %v, want every check on the first candidate and then every check on the rebuilt one", ran)
	}
	generations := f.generations()
	if len(generations) != 2 {
		t.Fatalf("generations = %#v, want the cut candidate and one rebuilt on the moved main", generations)
	}
	first, second := generations[0], generations[1]
	if first.Invalidated == nil || first.Invalidated.Reason != runstate.MergeQueueTargetMoved || first.Invalidated.ObservedTarget != moved {
		t.Fatalf("first generation invalidated = %#v, want main moving to %s recorded", first.Invalidated, moved)
	}
	if first.CheckRun == nil || first.CheckRun.Problem != cut.CheckRun.Problem {
		t.Fatalf("first generation checks = %#v, want the cut attempt left as it was, not run again", first.CheckRun)
	}
	if second.TargetBase != moved || len(second.Interruptions) != 0 {
		t.Fatalf("second generation = %#v, want it built on %s with nothing cut off", second, moved)
	}
	for _, says := range []string{"the checks of generation 1 of merge queue entry 1 had been cut off",
		"main had moved from " + shortCommit(cut.TargetBase) + " to " + shortCommit(moved),
		"as read from origin just now", "a new candidate was built on main as it stands"} {
		if !strings.Contains(landed.Says, says) {
			t.Errorf("the pass's record says %q, want it to say %q", landed.Says, says)
		}
	}
}

func TestCutOffChecksWhoseBaseMainStillStandsOnAreTakenUpAgainOnTheSameCandidate(t *testing.T) {
	t.Parallel()

	f, cut := cutOffFixture(t)

	landed := f.passAgain(queueKey)
	if landed.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() on the new build = %#v, want the change landed", landed)
	}
	if ran := f.checks.ran(); len(ran) != 2 || !slices.Equal(ran[0], queueChecks) || !slices.Equal(ran[1], queueChecks[1:]) {
		t.Fatalf("checks ran %v, want every check once and then only the one cut off", ran)
	}
	generations := f.generations()
	if len(generations) != 1 || generations[0].Candidate != cut.Candidate || len(generations[0].Interruptions) != 1 {
		t.Fatalf("generations = %#v, want the same candidate with its cut recorded once", generations)
	}
	for _, says := range []string{"the checks of generation 1 of merge queue entry 1 had been cut off",
		"main still stood at " + shortCommit(cut.TargetBase), "as read from origin just now",
		"taken up again on the same candidate"} {
		if !strings.Contains(landed.Says, says) {
			t.Errorf("the pass's record says %q, want it to say %q", landed.Says, says)
		}
	}
}

func TestOnlyAStandingGenerationWithCutOffChecksIsFetchedFor(t *testing.T) {
	t.Parallel()

	finished := time.Date(2026, 11, 18, 13, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name       string
		generation runstate.MergeQueueGeneration
		want       bool
	}{
		{"never checked", runstate.MergeQueueGeneration{}, false},
		{"checks running when the worker stopped", runstate.MergeQueueGeneration{CheckRun: &runstate.MergeQueueCheckEvidence{}}, true},
		{"checks ended without judging", runstate.MergeQueueGeneration{CheckRun: &runstate.MergeQueueCheckEvidence{FinishedAt: &finished, Problem: "cut off"}}, true},
		{"checks finished", runstate.MergeQueueGeneration{CheckRun: &runstate.MergeQueueCheckEvidence{FinishedAt: &finished}}, false},
		{"checks set aside and not started again", runstate.MergeQueueGeneration{Interruptions: []runstate.MergeQueueInterruption{{Stage: runstate.MergeQueueStageChecks}}}, true},
		{"only the review set aside", runstate.MergeQueueGeneration{Interruptions: []runstate.MergeQueueInterruption{{Stage: runstate.MergeQueueStageReview}}}, false},
		{"invalidated", runstate.MergeQueueGeneration{CheckRun: &runstate.MergeQueueCheckEvidence{}, Invalidated: &runstate.MergeQueueInvalidation{Reason: runstate.MergeQueueTargetMoved}}, false},
	} {
		if got := checksToResume(test.generation); got != test.want {
			t.Errorf("%s: checksToResume() = %t, want %t", test.name, got, test.want)
		}
	}
}
