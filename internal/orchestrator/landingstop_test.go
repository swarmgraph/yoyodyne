package orchestrator

import (
	"strings"
	"testing"

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
			step: &LandingStep{Doing: "checking where origin's main stands before landing", ReadsRemote: true},
			pull: open, failure: "check the remote target branch before promoting: fatal: could not read from remote repository",
			want: "Landing stopped at: checking where origin's main stands before landing. What the remote showed: check the remote target branch before promoting: fatal: could not read from remote repository. The forge was never asked to merge pull request #1181.",
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
			step: &LandingStep{Doing: "publishing the replayed change to pull request #1181", ReadsRemote: true},
			pull: open, failure: "republish the replayed developer branch: ! [rejected] (stale info)",
			want: "Landing stopped at: publishing the replayed change to pull request #1181. What the remote showed: republish the replayed developer branch: ! [rejected] (stale info). The forge was never asked to merge pull request #1181.",
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
			step: &LandingStep{Doing: "bringing the local main onto origin's, which moved before the forge was asked to merge", ReadsRemote: true},
			pull: open,
			want: "Landing stopped at: bringing the local main onto origin's, which moved before the forge was asked to merge. The forge was never asked to merge pull request #1181.",
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
