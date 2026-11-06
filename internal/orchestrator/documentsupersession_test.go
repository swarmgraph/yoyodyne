package orchestrator

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// copyRuns is a run store holding a handful of document runs.
type copyRuns struct {
	runs map[string]runstate.State
}

func (c *copyRuns) DocumentRuns(id string) ([]runstate.State, error) {
	var listed []runstate.State
	for _, state := range c.runs {
		if state.Document != nil && state.Document.Candidate.Artifact.ID == id {
			listed = append(listed, state)
		}
	}
	slices.SortFunc(listed, func(a, b runstate.State) int { return strings.Compare(a.RunID, b.RunID) })
	return listed, nil
}

func (c *copyRuns) Load(runID string) (runstate.State, error) { return c.runs[runID], nil }

func (c *copyRuns) Update(_ context.Context, runID string, update runstate.RunUpdate) (runstate.State, error) {
	next, _, err := runstate.ApplyUpdate(c.runs[runID], update)
	if err == nil {
		c.runs[runID] = next
	}
	return next, err
}

type deletedBranches struct{ deleted []string }

func (d *deletedBranches) DeleteRemoteBranch(_ context.Context, worktree gitworktree.Worktree, _ string) error {
	d.deleted = append(d.deleted, worktree.Branch)
	return nil
}

var copiesWrittenAt = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

// documentCopy is one run of a document, its text written some hours after
// copiesWrittenAt, holding pull request number open on its own branch.
func documentCopy(runID, document, text string, hours, number int, status runstate.Status) runstate.State {
	return runstate.State{
		RunID:        runID,
		Status:       status,
		TargetBranch: "main",
		StartedAt:    copiesWrittenAt.Add(time.Duration(hours) * time.Hour),
		Document: &runstate.DocumentPublication{Candidate: artifact.ConfirmedDocument{
			Artifact: artifact.Artifact{ID: document, Kind: artifact.KindDesign, Title: "Design " + document, Revisions: []artifact.Revision{{At: copiesWrittenAt.Add(time.Duration(hours) * time.Hour)}}},
			Content:  text,
		}},
		PullRequest: &runstate.PullRequest{Branch: "yoyodyne/" + runID, Number: number, URL: fmt.Sprintf("https://example.invalid/pull/%d", number), HeadCommit: "head-" + runID, State: "OPEN"},
	}
}

func holdingForge(states ...runstate.State) *orchestratortest.Forge {
	forge := &orchestratortest.Forge{}
	for _, state := range states {
		forge.Hold(state.PullRequest.Branch, publish.PullRequest{Number: state.PullRequest.Number, State: state.PullRequest.State, Merged: state.PullRequest.Merged})
	}
	return forge
}

func runsOf(states ...runstate.State) *copyRuns {
	runs := &copyRuns{runs: map[string]runstate.State{}}
	for _, state := range states {
		runs.runs[state.RunID] = state
	}
	return runs
}

// A newer revision's request closes the stopped first revision's, naming
// itself, and the branch the first one carried goes with it.
func TestANewerRevisionClosesTheEarlierRevisionsPullRequest(t *testing.T) {
	t.Parallel()

	first := documentCopy("run-first", "plan", "# First", 0, 1082, runstate.StatusFailed)
	second := documentCopy("run-second", "plan", "# Second", 2, 1097, runstate.StatusRunning)
	runs, forge, branches := runsOf(first, second), holdingForge(first), &deletedBranches{}

	closed := closeEarlierDocumentCopies(context.Background(), runs, forge, branches, second, copiesWrittenAt)
	if len(closed) != 1 || !closed[0].Closed || !closed[0].BranchDeleted || closed[0].Failure != "" {
		t.Fatalf("closed = %+v, want the first revision's request closed and its branch deleted", closed)
	}
	requests := forge.ClosedRequests()
	if len(requests) != 1 || requests[0].Number != 1082 || !strings.Contains(requests[0].Comment, "newer revision") || !strings.Contains(requests[0].Comment, "pull request #1097") {
		t.Fatalf("closed requests = %+v, want #1082 closed naming #1097", requests)
	}
	recorded := runs.runs[first.RunID].PullRequest
	if recorded.State != "CLOSED" || !strings.Contains(recorded.Superseded, "#1097") {
		t.Fatalf("first run's request = %+v, want it recorded closed and replaced by #1097", recorded)
	}
	if !slices.Equal(branches.deleted, []string{first.PullRequest.Branch}) {
		t.Fatalf("deleted = %v, want the first run's branch", branches.deleted)
	}
	// Running again has nothing left to close, so nobody gets a second comment.
	if again := closeEarlierDocumentCopies(context.Background(), runs, forge, branches, second, copiesWrittenAt); len(again) != 0 || len(forge.ClosedRequests()) != 1 {
		t.Fatalf("second pass closed %+v", again)
	}
}

// A repeat attempt at the same text closes the earlier attempt's request, and
// says it is the same text rather than a newer revision.
func TestARepeatAttemptAtTheSameTextClosesTheEarlierAttemptsPullRequest(t *testing.T) {
	t.Parallel()

	first := documentCopy("run-first", "plan", "# Same", 0, 1150, runstate.StatusTimedOut)
	again := documentCopy("run-again", "plan", "# Same", 0, 1151, runstate.StatusRunning)
	again.StartedAt = first.StartedAt.Add(time.Hour)
	runs, forge := runsOf(first, again), holdingForge(first)

	closed := closeEarlierDocumentCopies(context.Background(), runs, forge, &deletedBranches{}, again, copiesWrittenAt)
	requests := forge.ClosedRequests()
	if len(closed) != 1 || len(requests) != 1 || requests[0].Number != 1150 || !strings.Contains(requests[0].Comment, "same text") || !strings.Contains(requests[0].Comment, "#1151") {
		t.Fatalf("closed = %+v, requests %+v; want #1150 closed as an earlier attempt naming #1151", closed, requests)
	}
}

// Every pull request that is not an earlier copy of the same document still
// being shown open is left exactly as it is.
func TestOnlyEarlierCopiesOfTheSameDocumentAreClosed(t *testing.T) {
	t.Parallel()

	current := documentCopy("run-current", "plan", "# Plan", 4, 1200, runstate.StatusRunning)
	other := documentCopy("run-other", "other-plan", "# Other", 0, 1201, runstate.StatusFailed)
	running := documentCopy("run-running", "plan", "# Plan, still going", 1, 1202, runstate.StatusRunning)
	merged := documentCopy("run-merged", "plan", "# Plan, merged", 1, 1204, runstate.StatusFailed)
	merged.PullRequest.Merged, merged.PullRequest.State = true, "MERGED"
	closedAlready := documentCopy("run-closed", "plan", "# Plan, closed", 1, 1205, runstate.StatusFailed)
	closedAlready.PullRequest.State = "CLOSED"
	newer := documentCopy("run-newer", "plan", "# Plan, newer", 6, 1206, runstate.StatusFailed)
	landed := documentCopy("run-landed", "plan", "# Plan, landed", 1, 1207, runstate.StatusSucceeded)
	waiting := documentCopy("run-waiting", "plan", "# Plan, waiting for a person", 1, 1208, runstate.StatusSucceeded)
	elsewhere := documentCopy("run-elsewhere", "plan", "# Plan, other target", 1, 1209, runstate.StatusFailed)
	elsewhere.TargetBranch = "release"
	all := []runstate.State{current, other, running, merged, closedAlready, newer, landed, waiting, elsewhere}
	runs, forge := runsOf(all...), holdingForge(all...)

	closed := closeEarlierDocumentCopies(context.Background(), runs, forge, &deletedBranches{}, current, copiesWrittenAt)
	if len(closed) != 0 || len(forge.ClosedRequests()) != 0 {
		t.Fatalf("closed = %+v, requests %+v; want nothing closed", closed, forge.ClosedRequests())
	}
	for _, state := range all {
		if got := runs.runs[state.RunID].PullRequest; *got != *state.PullRequest {
			t.Fatalf("run %s's request = %+v, want it untouched", state.RunID, got)
		}
	}
}

// Two documents publishing side by side close nothing of each other's.
func TestTwoDocumentsPublishingSideBySideLeaveEachOthersPullRequestsOpen(t *testing.T) {
	t.Parallel()

	design := documentCopy("run-design", "design", "# Design", 0, 1300, runstate.StatusRunning)
	goals := documentCopy("run-goals", "goals", "# Goals", 0, 1301, runstate.StatusRunning)
	stoppedGoals := documentCopy("run-goals-stopped", "goals", "# Goals, stopped", 1, 1302, runstate.StatusFailed)
	runs, forge := runsOf(design, goals, stoppedGoals), holdingForge(design, goals, stoppedGoals)

	if closed := closeEarlierDocumentCopies(context.Background(), runs, forge, &deletedBranches{}, design, copiesWrittenAt); len(closed) != 0 {
		t.Fatalf("the design's run closed %+v", closed)
	}
	// The goals' own earlier copy is still closed by the goals' newer run, and
	// the design's request is not.
	goals.StartedAt = stoppedGoals.StartedAt.Add(time.Hour)
	goals.Document.Candidate.Artifact.Revisions[0].At = goals.StartedAt
	closed := closeEarlierDocumentCopies(context.Background(), runs, forge, &deletedBranches{}, goals, copiesWrittenAt)
	if len(closed) != 1 || closed[0].Number != 1302 || runs.runs[design.RunID].PullRequest.State != "OPEN" {
		t.Fatalf("closed = %+v, design %+v; want only the goals' stopped copy closed", closed, runs.runs[design.RunID].PullRequest)
	}
}

// A first revision whose request the forge has merged since its record was
// written is not closed, and nothing is written over its record: the sweep that
// refreshes publications records the merge.
func TestAnEarlierRevisionTheForgeMergedIsNotClosed(t *testing.T) {
	t.Parallel()

	first := documentCopy("run-first", "plan", "# First", 0, 1400, runstate.StatusFailed)
	second := documentCopy("run-second", "plan", "# Second", 2, 1401, runstate.StatusRunning)
	forge := &orchestratortest.Forge{}
	forge.Hold(first.PullRequest.Branch, publish.PullRequest{Number: 1400, State: "MERGED", Merged: true})
	runs, branches := runsOf(first, second), &deletedBranches{}

	closed := closeEarlierDocumentCopies(context.Background(), runs, forge, branches, second, copiesWrittenAt)
	if len(closed) != 1 || closed[0].Closed || closed[0].BranchDeleted || !strings.Contains(closed[0].Failure, "merged at the forge") {
		t.Fatalf("closed = %+v, want the merged request reported and left", closed)
	}
	if len(forge.ClosedRequests()) != 0 || len(branches.deleted) != 0 || runs.runs[first.RunID].PullRequest.State != "OPEN" {
		t.Fatalf("closed %+v, deleted %v, record %+v", forge.ClosedRequests(), branches.deleted, runs.runs[first.RunID].PullRequest)
	}
}

// Through a whole publishing run: the first revision of a design is handed back
// by its reviewer with its pull request open, and the corrected revision's run
// closes that request, naming its own, and deletes the branch it carried.
func TestARevisedDocumentsRunClosesTheReturnedRevisionsPullRequest(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	fixture.forge.Number = 1081
	fixture.forge.OnEnsure = func() { fixture.forge.Number++ }
	provider := orchestratortest.RoleBackend(nil, approveVerdict)
	original := provider.Respond
	authored, reviews := 0, 0
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		switch request.Role {
		case domain.RoleArchitect:
			authored++
			body := "# Bad"
			if authored > 1 {
				body = "# Corrected"
			}
			return backend.RunResult{SessionID: "author", FinalText: submittedDesign("revised-design", body)}, nil
		case domain.RoleReviewer:
			reviews++
			if reviews == 1 {
				return backend.RunResult{SessionID: "independent", FinalText: `{"decision":"repair","summary":"fix the plan","findings":[{"severity":"blocker","message":"name the recovery step"}]}`}, nil
			}
		}
		return original(request)
	}
	pipeline := publishing(automatic(newSharedPipeline(t, fixture.repository, fixture.worktreeRoot, fixture.store, fixture.tracker, provider, []string{"exit 0"}), provider), fixture.forge)
	options, _ := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Write it."); err != nil {
		t.Fatal(err)
	}
	records, _ := fixture.store.Recorded()
	if len(records) != 1 || records[0].PullRequest == nil || records[0].PullRequest.Number != 1082 {
		t.Fatalf("first revision = %+v, want it returned with pull request #1082 open", records)
	}
	first := records[0]
	fixture.forge.Hold(first.PullRequest.Branch, publish.PullRequest{Number: 1082, State: "OPEN", HeadCommit: first.PullRequest.HeadCommit})
	if remote := publicationGit(t, fixture.remote, "branch", "--list", first.PullRequest.Branch); remote == "" {
		t.Fatalf("branch %s was never published", first.PullRequest.Branch)
	}

	if _, err := session.Send(context.Background(), "Revise it."); err != nil {
		t.Fatal(err)
	}
	closed := fixture.forge.ClosedRequests()
	if len(closed) != 1 || closed[0].Number != 1082 || !strings.Contains(closed[0].Comment, "pull request #1083") {
		t.Fatalf("closed = %+v, want #1082 closed naming #1083", closed)
	}
	returned, err := fixture.store.Load(first.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if returned.PullRequest.State != "CLOSED" || !strings.Contains(returned.PullRequest.Superseded, "#1083") {
		t.Fatalf("returned run's request = %+v, want it recorded closed and replaced by #1083", returned.PullRequest)
	}
	if remote := publicationGit(t, fixture.remote, "branch", "--list", first.PullRequest.Branch); remote != "" {
		t.Fatalf("branch %s is still on the remote: %s", first.PullRequest.Branch, remote)
	}
}
