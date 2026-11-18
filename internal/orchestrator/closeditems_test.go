package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// earlierRun is a run that published a pull request and ended without landing
// anything, which is what every case below starts from.
func earlierRun(number int) runstate.State {
	completed := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	return runstate.State{
		RunID:        "run-11112222333344445555666677778888",
		WorkItemID:   "yoyodyne-task",
		Status:       runstate.StatusFailed,
		StartedAt:    completed.Add(-time.Hour),
		CompletedAt:  &completed,
		TargetBranch: "main",
		PullRequest:  &runstate.PullRequest{Number: number, Branch: "yoyodyne/task/11112222", HeadCommit: strings.Repeat("a", 40), State: "OPEN"},
	}
}

// waitingForPerson is a run that finished and waits for a person to merge its
// pull request.
func waitingForPerson(number int) runstate.State {
	state := earlierRun(number)
	state.Status = runstate.StatusSucceeded
	state.PersonMerge = &runstate.PersonMerge{Number: number, Since: *state.CompletedAt}
	return state
}

func closedItem(reason string) *beads.WorkItem {
	return &beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "closed", CloseReason: reason}
}

func landedThrough(number int) *gitworktree.ChangeLanding {
	return &gitworktree.ChangeLanding{TargetBranch: "main", Landed: true, Commit: strings.Repeat("c", 40), Number: number}
}

// Which requests the step may decide about at all: an open request of a run
// that has ended. A run still going, a merged, closed, queued or handed-back
// request, a document's request, and the request of a run that succeeded and
// waits for a person or a queue are all left alone.
func TestClosedItemCandidatesAreOnlyEndedRunsWithAnOpenRequest(t *testing.T) {
	t.Parallel()
	if !closedItemCandidate(earlierRun(5)) {
		t.Fatal("an ended run's open request is not a candidate")
	}
	cases := map[string]func(*runstate.State){
		"a run still going":            func(s *runstate.State) { s.Status = runstate.StatusRunning },
		"a request already merged":     func(s *runstate.State) { s.PullRequest.Merged = true },
		"a request already closed":     func(s *runstate.State) { s.PullRequest.State = "CLOSED" },
		"a request queued to merge":    func(s *runstate.State) { s.PullRequest.MergeQueued = true },
		"a request already superseded": func(s *runstate.State) { s.PullRequest.Superseded = "pull request #9" },
		"a handed-back request": func(s *runstate.State) {
			s.PullRequest.HandedBack = &runstate.PublicationHandBack{}
		},
		"a document's request":  func(s *runstate.State) { s.Document = &runstate.DocumentPublication{} },
		"a run that integrated": func(s *runstate.State) { s.Integration = &runstate.Integration{TargetBranch: "main"} },
		"a run admitted to the harness's merge queue": func(s *runstate.State) {
			s.Status = runstate.StatusSucceeded
			s.MergeQueue = &runstate.MergeQueueAdmissionRecord{}
		},
		"a run with no request": func(s *runstate.State) { s.PullRequest = nil },
	}
	for name, change := range cases {
		state := earlierRun(5)
		change(&state)
		if closedItemCandidate(state) {
			t.Errorf("%s is a candidate", name)
		}
	}
	waiting := earlierRun(5)
	waiting.Status = runstate.StatusSucceeded
	if !closedItemCandidate(waiting) {
		t.Error("a finished run waiting for a person to merge is not a candidate")
	}
	retired := earlierRun(5)
	retired.Status = runstate.StatusCancelled
	retired.Retirement = &runstate.RunRetirement{RunID: "run-99998888777766665555444433332222", Number: 7}
	if !closedItemCandidate(retired) {
		t.Error("a run retired after its item merged is not a candidate")
	}
}

// Every case the work item names, decided from what was read.
func TestClosedItemDecisions(t *testing.T) {
	t.Parallel()
	shownRetired := earlierRun(5)
	shownRetired.ReconcileFindings = []runstate.ReconcileFinding{{
		Step:    runstate.ReconcileSuperseded,
		Problem: "the work item was retired without being done, and nothing shows its change on main or names what replaced it; pull request #5 " + ownership.PublicationKeptForDecision,
	}}
	cases := []struct {
		name        string
		state       runstate.State
		item        *beads.WorkItem
		landing     *gitworktree.ChangeLanding
		replacement string
		action      closedItemAction
		release     bool
		says        []string
	}{
		{name: "a closed item with an earlier open request and a named replacement", state: earlierRun(5), item: closedItem("Reviewed and integrated by Yoyodyne run run-x"),
			replacement: "pull request #9, which the forge merged for run run-x", action: closeRequest, release: true, says: []string{"#9", "work item is closed"}},
		{name: "a closed item whose change is on the target with no pull request named", state: earlierRun(5), item: closedItem("done"),
			landing: &gitworktree.ChangeLanding{Landed: true, Commit: strings.Repeat("d", 40)}, action: closeRequest, release: true, says: []string{"already on main", "dddddddddddd"}},
		{name: "a closed item whose work is not on main and nothing replaced it", state: earlierRun(5), item: closedItem("Closed as done by the Lead Product Manager"),
			action: keepRequest, says: []string{"only copy", ownership.PublicationKeptForDecision}},
		{name: "a retired item not yet shown to the development manager", state: earlierRun(5), item: closedItem(retiredWithoutBeingDone + " by the Lead Product Manager"),
			action: keepRequest, says: []string{"retired without being done", ownership.PublicationKeptForDecision}},
		{name: "a retired item once shown", state: shownRetired, item: closedItem(retiredWithoutBeingDone + " by the Lead Product Manager"),
			action: closeRequest, release: false, says: []string{"retired without being done", "has been shown"}},
		{name: "a retired item whose rebuild landed", state: earlierRun(5), item: closedItem(retiredWithoutBeingDone),
			replacement: "pull request #12", action: closeRequest, release: true, says: []string{"#12"}},
		{name: "an open item held for the development manager", state: earlierRun(5), item: nil,
			landing: &gitworktree.ChangeLanding{}, action: leaveRequest},
		{name: "an open item with a replacement on record is still left", state: earlierRun(5), item: nil,
			replacement: "pull request #9", action: leaveRequest},
		{name: "an open item whose change landed through another pull request", state: earlierRun(5), item: nil,
			landing: landedThrough(1535), action: closeRequest, release: true, says: []string{"#1535", "already on main"}},
		{name: "a closed item whose change landed through another pull request", state: earlierRun(5), item: closedItem("done"),
			landing: landedThrough(1535), action: closeRequest, release: true, says: []string{"#1535"}},
		{name: "a finished run waiting for a person, its item open and its change landed through another pull request", state: waitingForPerson(5), item: nil,
			landing: landedThrough(1535), action: closeRequest, release: true, says: []string{"#1535"}},
		{name: "a finished run waiting for a person, its item open and its change not on the target", state: waitingForPerson(5), item: nil,
			landing: &gitworktree.ChangeLanding{}, action: leaveRequest},
		{name: "a finished run waiting for a person whose item closed with its work replaced", state: waitingForPerson(5), item: closedItem("done"),
			replacement: "pull request #9", action: closeRequest, release: true, says: []string{"#9"}},
		{name: "an open item whose change landed through its own pull request is not closed", state: earlierRun(5), item: nil,
			landing: landedThrough(5), action: leaveRequest},
	}
	for _, c := range cases {
		decision := decideClosedItemPublication(c.state, c.item, c.landing, c.replacement)
		if decision.action != c.action || decision.release != c.release {
			t.Errorf("%s: decision = %#v, want action %d, release %v", c.name, decision, c.action, c.release)
			continue
		}
		for _, said := range c.says {
			if !strings.Contains(decision.why+" "+decision.replacedBy, said) {
				t.Errorf("%s: %q does not say %q", c.name, decision.why, said)
			}
		}
	}
}

// A failed close is the development manager's, and one only somebody with
// rights on the repository can lift is the operator's, as a repository setting.
func TestAFailedCloseIsTheDevelopmentManagersUnlessTheForgeRefusedTheAccount(t *testing.T) {
	t.Parallel()
	plain := closeFailure(5, errors.New("gh: timed out"))
	refused := closeFailure(5, errors.New("GraphQL: Resource not accessible by integration (closePullRequest)"))
	if got := ownership.Resolve(ownership.KindOwedStep, ownership.Record{UnresolvedFindings: []string{plain}}); got.Owner != ownership.MoverDevelopmentManager {
		t.Errorf("a timed-out close is %+v, want the development manager's", got)
	}
	got := ownership.Resolve(ownership.KindOwedStep, ownership.Record{UnresolvedFindings: []string{refused}})
	if !got.Owner.IsOperator() || got.Reason != ownership.OperatorRepositorySetting {
		t.Errorf("a refused close is %+v, want the operator's, as a repository setting", got)
	}
	if !strings.Contains(refused, "close #5 by hand") {
		t.Errorf("refusal %q does not say what to do", refused)
	}
}

// closedWork is a tracker listing of closed work.
type closedWork []beads.WorkItem

func (c closedWork) List(context.Context, string) ([]beads.WorkItem, error) { return c, nil }
func (c closedWork) Show(_ context.Context, id string) (beads.WorkItem, error) {
	for _, item := range c {
		if item.ID == id {
			return item, nil
		}
	}
	return beads.WorkItem{}, fmt.Errorf("no item %s", id)
}

// publishedRun records a run that committed one file on its own branch,
// published it as pull request number, and ended without landing it, with its
// checkout still on disk.
func (f queuedFixture) publishedRun(t *testing.T, workItemID, runID string, number int, file string) runstate.State {
	t.Helper()
	short := runID[len("run-"):][:8]
	branch := fmt.Sprintf("yoyodyne/%s/%s", workItemID, short)
	base := strings.TrimSpace(gitOutput(t, f.repository, "rev-parse", "refs/heads/main"))
	if err := os.MkdirAll(f.worktreeRoot, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	root, err := filepath.EvalSymlinks(f.worktreeRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	worktreePath := filepath.Join(root, workItemID+"-"+short)
	runPipelineGit(t, f.repository, "branch", branch, base)
	runPipelineGit(t, f.repository, "worktree", "add", worktreePath, branch)
	if err := os.WriteFile(filepath.Join(worktreePath, file), []byte("the change of "+runID+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runPipelineGit(t, worktreePath, "add", file)
	runPipelineGit(t, worktreePath, "-c", "user.name=Run", "-c", "user.email=run@example.invalid", "commit", "-m", "the change")
	head := strings.TrimSpace(gitOutput(t, worktreePath, "rev-parse", "HEAD"))
	runPipelineGit(t, f.repository, "push", "origin", head+":refs/heads/"+branch)
	started := time.Now().UTC().Add(-3 * time.Hour)
	completed := started.Add(time.Minute)
	state := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         runID,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    workItemID,
		WorkItemTitle: "Task",
		Backend:       "claude-code",
		Status:        runstate.StatusFailed,
		Phase:         runstate.PhaseDeveloping,
		StartedAt:     started,
		UpdatedAt:     completed,
		CompletedAt:   &completed,
		WorktreePath:  worktreePath,
		Branch:        branch,
		BaseCommit:    base,
		TargetBranch:  "main",
		Failure:       "the run stopped for the development manager",
		PullRequest: &runstate.PullRequest{
			Remote: "origin", Branch: branch, Number: number, HeadCommit: head, State: "OPEN",
			URL: fmt.Sprintf("https://example.invalid/pull/%d", number),
		},
	}
	if err := f.store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	f.forge.Hold(branch, publish.PullRequest{Number: number, State: "OPEN"})
	return state
}

// squashOnRemote lands the change at head on the remote's main as one new
// commit, as the forge does for another pull request carrying the same change.
func (f queuedFixture) squashOnRemote(t *testing.T, head, subject string) string {
	t.Helper()
	tip := strings.TrimSpace(gitOutput(t, f.remote, "rev-parse", "refs/heads/main"))
	base := strings.TrimSpace(gitOutput(t, f.remote, "merge-base", tip, head))
	tree := strings.Fields(gitOutput(t, f.remote, "merge-tree", "--write-tree", "--merge-base="+base, tip, head))[0]
	commit := strings.TrimSpace(gitOutput(t, f.remote, "-c", "user.name=Forge", "-c", "user.email=forge@example.invalid", "commit-tree", tree, "-p", tip, "-m", subject))
	runPipelineGit(t, f.remote, "update-ref", "refs/heads/main", commit, tip)
	return commit
}

func (f queuedFixture) convergeWithClosed(t *testing.T, closed closedWork) Convergence {
	t.Helper()
	reconciler := f.reconciler(t)
	reconciler.ClosedListing = &ClosedListing{Tracker: closed}
	convergence, err := reconciler.Converge(context.Background())
	if err != nil {
		t.Fatalf("Converge() error = %v", err)
	}
	return convergence
}

func onlyClosedItem(t *testing.T, convergence Convergence) ClosedItemPublication {
	t.Helper()
	if len(convergence.ClosedItems) != 1 {
		t.Fatalf("closed items = %#v, want exactly one", convergence.ClosedItems)
	}
	return convergence.ClosedItems[0]
}

// The case on #1496 to #1501: an earlier run's request whose change landed on
// main through another pull request is closed naming that pull request, while
// its own item is still open, and the run's branches and checkout are let go.
// A request the harness did not open is never touched.
func TestConvergeClosesARequestWhoseChangeLandedThroughAnotherPullRequest(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	earlier := fixture.publishedRun(t, "yoyodyne-task", "run-11112222333344445555666677778888", 1496, "chart.js")
	squash := fixture.squashOnRemote(t, earlier.PullRequest.HeadCommit, "Bring in Chart.js (#1535)")
	// Somebody else's request on the same forge, which no run opened.
	fixture.forge.Hold("someone/else", publish.PullRequest{Number: 1600, State: "OPEN"})

	convergence := fixture.convergeWithClosed(t, closedWork{})
	result := onlyClosedItem(t, convergence)
	if !result.Closed || result.Failure != "" || !result.BranchDeleted {
		t.Fatalf("result = %#v, want the request closed and its branch deleted", result)
	}
	if !strings.Contains(result.ReplacedBy, "#1535") || !strings.Contains(result.ReplacedBy, squash) {
		t.Errorf("replaced by = %q, want #1535 and %s", result.ReplacedBy, squash)
	}
	closed := fixture.forge.ClosedRequests()
	if len(closed) != 1 || closed[0].Number != 1496 {
		t.Fatalf("closed = %#v, want only #1496", closed)
	}
	for _, said := range []string{"#1535", "Task (`yoyodyne-task`)", earlier.RunID, "without merging"} {
		if !strings.Contains(closed[0].Comment, said) {
			t.Errorf("comment does not say %q:\n%s", said, closed[0].Comment)
		}
	}
	if commit := publishedCommit(t, fixture.remote, earlier.Branch); commit != "" {
		t.Errorf("remote branch still at %s", commit)
	}
	if commit := publishedCommit(t, fixture.repository, earlier.Branch); commit != "" {
		t.Errorf("local branch still at %s", commit)
	}
	if _, err := os.Stat(earlier.WorktreePath); !os.IsNotExist(err) {
		t.Errorf("checkout %s still there: %v", earlier.WorktreePath, err)
	}
	recorded, err := fixture.store.Load(earlier.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.PullRequest.State != "CLOSED" || !strings.Contains(recorded.PullRequest.Superseded, "#1535") || !recorded.WorktreeRemoved || !recorded.BranchRemoved {
		t.Errorf("record = %#v, want the close and the release written on the run", recorded.PullRequest)
	}
	if notes := fixture.tracker.Record().Notes; !strings.Contains(notes, "Yoyodyne closed pull request #1496") || !strings.Contains(notes, "#1535") {
		t.Errorf("item notes = %q, want the close recorded on the item", notes)
	}
	// The next pass has nothing to do and says nothing.
	again := fixture.convergeWithClosed(t, closedWork{})
	if len(again.ClosedItems) != 0 || len(fixture.forge.ClosedRequests()) != 1 {
		t.Errorf("second pass = %#v, closed = %d, want nothing more", again.ClosedItems, len(fixture.forge.ClosedRequests()))
	}
}

// The case on #732: a run retired after its item merged kept its request,
// branch and checkout. Its item is closed and the retirement names the merged
// pull request, so the request is closed naming it and all of it is let go.
func TestConvergeClosesARetiredRunsRequestAndLetsItsWorkGo(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	earlier := fixture.publishedRun(t, "yoyodyne-task", "run-11112222333344445555666677778888", 732, "fix.txt")
	earlier.Status = runstate.StatusCancelled
	earlier.Retirement = &runstate.RunRetirement{
		RunID: "run-99998888777766665555444433332222", Commit: strings.Repeat("e", 40), TargetBranch: "main",
		Number: 1184, At: time.Now().UTC(), PriorStatus: runstate.StatusFailed,
	}
	if err := fixture.store.Save(earlier); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	convergence := fixture.convergeWithClosed(t, closedWork{{ID: "yoyodyne-task", Title: "Task", Status: "closed", CloseReason: "Reviewed and integrated"}})
	result := onlyClosedItem(t, convergence)
	if !result.Closed || result.Failure != "" || !strings.Contains(result.ReplacedBy, "#1184") {
		t.Fatalf("result = %#v, want #732 closed naming #1184", result)
	}
	if len(result.Released) != 2 || result.ReleaseKept != "" {
		t.Errorf("released = %v, kept = %q, want the checkout and the branch let go", result.Released, result.ReleaseKept)
	}
	if commit := publishedCommit(t, fixture.repository, earlier.Branch); commit != "" {
		t.Errorf("local branch still at %s", commit)
	}
}

// Condition 7: a closed item whose work is not on main and that nothing
// replaced keeps its request, branch and checkout, says why on the item, and
// is the development manager's to decide.
func TestConvergeKeepsAClosedItemsRequestWhoseWorkIsNowhere(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	earlier := fixture.publishedRun(t, "yoyodyne-task", "run-11112222333344445555666677778888", 863, "job.txt")

	convergence := fixture.convergeWithClosed(t, closedWork{{ID: "yoyodyne-task", Title: "Task", Status: "closed", CloseReason: "Closed as done by the Lead Product Manager"}})
	result := onlyClosedItem(t, convergence)
	if result.Closed || !result.Kept || !strings.Contains(result.Why, "only copy") {
		t.Fatalf("result = %#v, want the request kept as the only copy", result)
	}
	if len(fixture.forge.ClosedRequests()) != 0 {
		t.Fatalf("closed = %#v, want nothing closed", fixture.forge.ClosedRequests())
	}
	if commit := publishedCommit(t, fixture.repository, earlier.Branch); commit != earlier.PullRequest.HeadCommit {
		t.Errorf("local branch at %q, want it kept", commit)
	}
	if commit := publishedCommit(t, fixture.remote, earlier.Branch); commit != earlier.PullRequest.HeadCommit {
		t.Errorf("remote branch at %q, want it kept", commit)
	}
	if result.Finding == nil || result.Finding.Mover != readmodel.MoverDevelopmentManager {
		t.Errorf("finding = %#v, want it the development manager's", result.Finding)
	}
	if notes := fixture.tracker.Record().Notes; !strings.Contains(notes, ownership.PublicationKeptForDecision) {
		t.Errorf("item notes = %q, want why it is kept", notes)
	}
	// Said once: a second pass keeps it without another line.
	if again := fixture.convergeWithClosed(t, closedWork{{ID: "yoyodyne-task", Title: "Task", Status: "closed"}}); len(again.ClosedItems) != 0 {
		t.Errorf("second pass = %#v, want nothing more said", again.ClosedItems)
	}
}

// A kept request somebody then closes by hand is settled: the refresh records
// the forge's answer, and the finding leaves the development manager's list.
func TestAKeptRequestClosedByHandLeavesTheDevelopmentManagersList(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	earlier := fixture.publishedRun(t, "yoyodyne-task", "run-11112222333344445555666677778888", 863, "job.txt")
	closed := closedWork{{ID: "yoyodyne-task", Title: "Task", Status: "closed", CloseReason: "Closed as done"}}
	if result := onlyClosedItem(t, fixture.convergeWithClosed(t, closed)); !result.Kept {
		t.Fatalf("result = %#v, want the request kept", result)
	}
	// What the refresh records once a person has closed it on the forge.
	recorded, err := fixture.store.Load(earlier.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !hasReconcileFinding(recorded, runstate.ReconcileSuperseded) {
		t.Fatalf("findings = %#v, want the kept request's finding", recorded.ReconcileFindings)
	}
	recorded.PullRequest.State = "CLOSED"
	if err := fixture.store.Save(recorded); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	again := fixture.convergeWithClosed(t, closed)
	if len(again.ClosedItems) != 0 || len(fixture.forge.ClosedRequests()) != 0 {
		t.Fatalf("second pass = %#v, want nothing closed by the harness", again.ClosedItems)
	}
	settled, err := fixture.store.Load(earlier.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if hasReconcileFinding(settled, runstate.ReconcileSuperseded) {
		t.Errorf("findings = %#v, want the finding cleared once the request is closed", settled.ReconcileFindings)
	}
}

// Condition 5: a close the forge refused is recorded on the run and the item
// and put in front of the development manager, and nothing is released.
func TestConvergeRecordsACloseTheForgeRefused(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	earlier := fixture.publishedRun(t, "yoyodyne-task", "run-11112222333344445555666677778888", 1496, "chart.js")
	fixture.squashOnRemote(t, earlier.PullRequest.HeadCommit, "Bring in Chart.js (#1535)")
	fixture.forge.CloseErr = errors.New("gh: the forge did not answer")

	result := onlyClosedItem(t, fixture.convergeWithClosed(t, closedWork{}))
	if result.Closed || !strings.Contains(result.Failure, ownership.PublicationNotClosed) {
		t.Fatalf("result = %#v, want the refusal recorded", result)
	}
	if result.Finding == nil || result.Finding.Mover != readmodel.MoverDevelopmentManager {
		t.Errorf("finding = %#v, want it the development manager's", result.Finding)
	}
	if commit := publishedCommit(t, fixture.repository, earlier.Branch); commit == "" {
		t.Error("the local branch was released although the request is still open")
	}
	if notes := fixture.tracker.Record().Notes; !strings.Contains(notes, ownership.PublicationNotClosed) {
		t.Errorf("item notes = %q, want the refusal recorded on the item", notes)
	}
}

// A finished run waiting for a person to merge its pull request is closed once
// its change landed through another pull request, and stops waiting, so the
// forge check does not read the close as the change being declined. While its
// change is not on the target and its item is open, it is left exactly as it is.
func TestConvergeClosesAWaitingRequestWhoseChangeLandedElsewhereAndLeavesOneThatDidNot(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	landed := fixture.waitingRun(t, "run-11112222333344445555666677778888", 1496, "chart.js")
	pending := fixture.waitingRun(t, "run-aaaa1111aaaa1111aaaa1111aaaa1111", 1497, "other.txt")
	fixture.squashOnRemote(t, landed.PullRequest.HeadCommit, "Bring in Chart.js (#1535)")

	result := onlyClosedItem(t, fixture.convergeWithClosed(t, closedWork{}))
	if result.RunID != landed.RunID || !result.Closed || result.Failure != "" || !strings.Contains(result.ReplacedBy, "#1535") {
		t.Fatalf("result = %#v, want #1496 closed naming #1535", result)
	}
	closed, err := fixture.store.Load(landed.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if closed.WaitingForPersonMerge() || closed.PullRequest.State != "CLOSED" {
		t.Errorf("record = %#v, %#v, want the request closed and the wait ended", closed.PullRequest, closed.PersonMerge)
	}
	untouched, err := fixture.store.Load(pending.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !untouched.WaitingForPersonMerge() || untouched.PullRequest.State != "OPEN" || untouched.PullRequest.Superseded != "" {
		t.Errorf("record = %#v, want the request whose change is not on the target left waiting", untouched.PullRequest)
	}
	if got := fixture.forge.ClosedRequests(); len(got) != 1 || got[0].Number != 1496 {
		t.Errorf("closed = %#v, want only #1496", got)
	}
}

// waitingRun is publishedRun finished and waiting for a person to merge.
func (f queuedFixture) waitingRun(t *testing.T, runID string, number int, file string) runstate.State {
	t.Helper()
	state := f.publishedRun(t, "yoyodyne-task", runID, number, file)
	state.Status = runstate.StatusSucceeded
	state.Phase = runstate.PhaseComplete
	state.Failure = ""
	state.PersonMerge = &runstate.PersonMerge{Number: number, Since: *state.CompletedAt}
	if err := f.store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := f.store.Load(runID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return loaded
}
