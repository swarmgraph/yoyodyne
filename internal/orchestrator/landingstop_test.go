package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// These hold that every way an approved change can stop while landing ends the
// notes it writes on its item with the step it stopped at and what the forge
// said there — on the blocker the development manager is handed and on the
// failure note recorded after it — so nobody reading the item has to find the
// reason inside the review and the protection line, and a stop the forge had no
// part in says so rather than reading as one whose answer was lost.

// lastLine is the line a reader of a note sees last.
func lastLine(notes string) string {
	notes = strings.TrimRight(notes, "\n")
	return notes[strings.LastIndexByte(notes, '\n')+1:]
}

// assertLandingStop holds that the blocker, the run's record of it, and the
// failure note each end on the landing's stop line, and that the line says
// everything in want.
func assertLandingStop(t *testing.T, tracker *orchestratortest.Tracker, store *runstate.Store, runID string, want ...string) {
	t.Helper()
	if !tracker.Blocked {
		t.Fatal("the stopped landing was not handed over as a blocker")
	}
	state, err := store.Load(runID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(tracker.NoteRecords) == 0 {
		t.Fatal("the stopped landing recorded no note on its item")
	}
	for name, notes := range map[string]string{
		"blocker":          tracker.BlockReason,
		"recorded blocker": state.Blocker,
		"failure note":     tracker.NoteRecords[len(tracker.NoteRecords)-1],
	} {
		last := lastLine(notes)
		if !strings.HasPrefix(last, "Landing stopped at: ") {
			t.Errorf("%s does not end on the step the landing stopped at; it ends:\n%s", name, last)
			continue
		}
		for _, phrase := range want {
			if !strings.Contains(last, phrase) {
				t.Errorf("%s's last line does not say %q:\n%s", name, phrase, last)
			}
		}
	}
}

// The shape all three stops of October 11 had: the change was approved on a
// protected main, another change merged into the same file first, and the
// replay conflicted with no repair left. Nothing was asked of the forge, and the
// note says so last instead of ending on the protection line.
func TestALandingStoppedByAReplayConflictSaysTheForgeWasNeverAsked(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote, TargetProtection: publish.BranchProtection{Protected: true, By: "branch protection"}}
	forge.OnEnsure = func() { mergeConflictingRemoteChange(t, remote, "feature.txt") }
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("this run's answer\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 0

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), "cannot be replayed onto the moved target branch") {
		t.Fatalf("Run() error = %v, want the replay conflict", err)
	}
	if len(forge.Merges) != 0 {
		t.Fatalf("merges = %v, want the forge never asked", forge.Merges)
	}
	assertLandingStop(t, tracker, store, outcome.RunID,
		"replaying the change onto main",
		"never asked to merge pull request #1",
		"the failure above is this repository's")
	if !strings.Contains(tracker.BlockReason, "main is protected on the forge") {
		t.Errorf("blocker lost the protection line:\n%s", tracker.BlockReason)
	}
}

// The forge refusing the merge is the stop the forge did speak for, and its own
// words are on the last line.
func TestALandingTheForgeRefusedEndsOnWhatTheForgeSaid(t *testing.T) {
	t.Parallel()

	run := newProtectedRun(t, &orchestratortest.Forge{
		TargetProtection: publish.BranchProtection{Protected: true, By: "branch protection"},
		MergeErr: publish.MergeRefused{
			Number: 1,
			Method: publish.MergeCommit,
			Status: "BLOCKED",
			Reason: "At least 1 approving review is required by reviewers with write access",
		},
	})
	outcome, err := run.pipeline.Run(context.Background(), "yoyodyne-task")
	if err == nil {
		t.Fatalf("Run() = %#v, want the run stopped on the refused merge", outcome)
	}
	assertLandingStop(t, run.tracker, run.store, outcome.RunID,
		"asking the forge to merge pull request #1 into main",
		"What the forge said: ",
		"approving review is required")
}

// A merge the forge was asked for and never confirmed is a stop at the
// confirmation, with the forge's answer to the question.
func TestALandingWhoseMergeWasNeverConfirmedEndsOnTheConfirmation(t *testing.T) {
	t.Parallel()

	run := newProtectedRun(t, &orchestratortest.Forge{
		TargetProtection: publish.BranchProtection{Protected: true, By: "branch protection"},
		StateErr:         errors.New("gh: Could not resolve to a PullRequest with the number of 1"),
	})
	outcome, err := run.pipeline.Run(context.Background(), "yoyodyne-task")
	if err == nil {
		t.Fatalf("Run() = %#v, want the run stopped on the unconfirmed merge", outcome)
	}
	assertLandingStop(t, run.tracker, run.store, outcome.RunID,
		"confirming the forge merged pull request #1",
		"Could not resolve to a PullRequest")
}

// A remote target rewritten under the run stops the landing before anything is
// promoted, at the check of where the remote stands, and says what was found.
func TestALandingStoppedByADivergedRemoteEndsOnTheRemoteCheck(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote, TargetProtection: publish.BranchProtection{Protected: true, By: "ruleset"}}
	forge.OnEnsure = func() { rewriteRemoteTarget(t, remote, "main") }
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil {
		t.Fatalf("Run() = %#v, want the run stopped on the diverged remote", outcome)
	}
	if len(forge.Merges) != 0 {
		t.Fatalf("merges = %v, want the forge never asked", forge.Merges)
	}
	assertLandingStop(t, tracker, store, outcome.RunID,
		"checking where origin's main stands before landing",
		"What the forge said: ")
}

// A run that stops anywhere but its landing says nothing about landing.
func TestAStopBeforeLandingWritesNoLandingLine(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 1"})
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 0

	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil {
		t.Fatal("Run() succeeded over a failing check")
	}
	if strings.Contains(tracker.Notes+tracker.BlockReason, "Landing stopped at") {
		t.Fatalf("a run stopped at its checks says it stopped landing:\n%s\n%s", tracker.Notes, tracker.BlockReason)
	}
}

// Every step of a landing renders as the line it ends on: the steps that ask
// the forge carry its answer, and the ones that do not say it was never asked.
func TestEveryLandingStepRendersItsStopLine(t *testing.T) {
	t.Parallel()

	open := &runstate.PullRequest{Number: 1181, State: "OPEN"}
	for _, tc := range []struct {
		name      string
		step      *LandingStep
		pull      *runstate.PullRequest
		published string
		failure   string
		want      string
	}{
		{
			name: "not landing",
			pull: open, failure: "make test exited 2",
			want: "",
		},
		{
			name: "began",
			step: &LandingStep{Doing: "recording that the landing onto main began"},
			pull: open, failure: "save integrating run state: disk full",
			want: "Landing stopped at: recording that the landing onto main began. The forge was never asked to merge pull request #1181, so nothing the forge said stopped this landing; the failure above is this repository's.",
		},
		{
			name: "waiting for the turn",
			step: &LandingStep{Doing: "waiting for this run's turn to land on main"},
			pull: open, failure: "wait for this run's turn to promote: context canceled",
			want: "Landing stopped at: waiting for this run's turn to land on main. The forge was never asked to merge pull request #1181, so nothing the forge said stopped this landing; the failure above is this repository's.",
		},
		{
			name: "checking the remote",
			step: &LandingStep{Doing: "checking where origin's main stands before landing", AsksForge: true},
			pull: open, failure: "check the remote target branch before promoting: fatal: could not read from remote repository",
			want: "Landing stopped at: checking where origin's main stands before landing. What the forge said: check the remote target branch before promoting: fatal: could not read from remote repository",
		},
		{
			name: "preparing the commit",
			step: &LandingStep{Doing: "preparing the commit to land on main through its pull request"},
			pull: open, failure: "integrate approved change: worktree has uncommitted changes",
			want: "Landing stopped at: preparing the commit to land on main through its pull request. The forge was never asked to merge pull request #1181, so nothing the forge said stopped this landing; the failure above is this repository's.",
		},
		{
			name:    "promoting locally, not publishing",
			step:    &LandingStep{Doing: "promoting the change onto the local main"},
			failure: "integrate approved change: fast-forward refused",
			want:    "Landing stopped at: promoting the change onto the local main.",
		},
		{
			name: "replaying",
			step: &LandingStep{Doing: "replaying the change onto main, which moved after this change was prepared on it"},
			pull: open, failure: "change cannot be replayed onto the moved target branch",
			want: "Landing stopped at: replaying the change onto main, which moved after this change was prepared on it. The forge was never asked to merge pull request #1181, so nothing the forge said stopped this landing; the failure above is this repository's.",
		},
		{
			name: "republishing",
			step: &LandingStep{Doing: "publishing the replayed change to pull request #1181", AsksForge: true},
			pull: open, failure: "republish the replayed developer branch: ! [rejected] (stale info)",
			want: "Landing stopped at: publishing the replayed change to pull request #1181. What the forge said: republish the replayed developer branch: ! [rejected] (stale info)",
		},
		{
			name: "wrong head",
			step: &LandingStep{Doing: "checking that pull request #1181 carries the approved commit"},
			pull: open, published: "pull request 1181 carries abc, but the promotion integrated def",
			want: "Landing stopped at: checking that pull request #1181 carries the approved commit. The forge was never asked to merge pull request #1181, so nothing the forge said stopped this landing; the failure above is this repository's.",
		},
		{
			name: "merge refused",
			step: &LandingStep{Doing: "asking the forge to merge pull request #1181 into main", AsksForge: true},
			pull: open, published: "pull request 1181 was not merged: BLOCKED", failure: "the change was not landed on main: pull request 1181 was not merged: BLOCKED",
			want: "Landing stopped at: asking the forge to merge pull request #1181 into main. What the forge said: pull request 1181 was not merged: BLOCKED",
		},
		{
			name: "merge confirmation",
			step: &LandingStep{Doing: "confirming the forge merged pull request #1181", AsksForge: true},
			pull: open, published: "confirm the pull request merged: gh: HTTP 404",
			want: "Landing stopped at: confirming the forge merged pull request #1181. What the forge said: confirm the pull request merged: gh: HTTP 404",
		},
		{
			name: "remote moved before the merge",
			step: &LandingStep{Doing: "bringing the local main onto origin's, which moved before the forge was asked to merge", AsksForge: true},
			pull: open,
			want: "Landing stopped at: bringing the local main onto origin's, which moved before the forge was asked to merge. The forge gave no answer this run could read.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			outcome := Outcome{LandingStep: tc.step, PullRequest: tc.pull, PublishFailure: tc.published, Failure: tc.failure}
			if got := renderLandingStop(outcome, tc.failure); got != tc.want {
				t.Fatalf("renderLandingStop() =\n%s\nwant\n%s", got, tc.want)
			}
			// The failure note a stopped run records ends on the same line.
			notes := renderFailureNotes(outcome)
			if tc.want == "" {
				if strings.Contains(notes, "Landing stopped at") {
					t.Fatalf("failure note of a run that was not landing says it was:\n%s", notes)
				}
				return
			}
			if last := lastLine(notes); last != tc.want {
				t.Fatalf("failure note ends:\n%s\nwant\n%s", last, tc.want)
			}
		})
	}
}

// mergeConflictingRemoteChange is another pull request merging into the remote
// target a change to the same file this run changes, which is what moved main
// under the three landings that stopped on October 11.
func mergeConflictingRemoteChange(t *testing.T, remote, file string) {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "other-pull-request")
	runPipelineGit(t, filepath.Dir(clone), "clone", remote, clone)
	runPipelineGit(t, clone, "config", "user.name", "Someone Else")
	runPipelineGit(t, clone, "config", "user.email", "someone@example.invalid")
	disablePipelineMaintenance(t, clone)
	if err := os.WriteFile(filepath.Join(clone, file), []byte("another change's answer\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runPipelineGit(t, clone, "add", ".")
	runPipelineGit(t, clone, "commit", "-m", "another pull request")
	runPipelineGit(t, clone, "push", "origin", "HEAD:refs/heads/main")
}
