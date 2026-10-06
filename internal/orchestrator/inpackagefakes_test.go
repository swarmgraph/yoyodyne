package orchestrator

import (
	"context"
	"errors"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
)

// The old names below are kept while a test file that has not yet moved onto
// orchestratortest's fakes uses one: the files from publish_test.go on, and the
// fixtures they declare that the moved files share. Each goes when nothing uses
// it, and this file goes when the last of those files has moved.

// recordingFiler records created items and lists them as open work.
type recordingFiler struct {
	filed  []beads.NewWorkItem
	open   []beads.WorkItem
	refuse error
}

func (f *recordingFiler) Create(_ context.Context, item beads.NewWorkItem) (beads.WorkItem, error) {
	if f.refuse != nil {
		return beads.WorkItem{}, f.refuse
	}
	f.filed = append(f.filed, item)
	created := beads.WorkItem{ID: fmt.Sprintf("yoyodyne-red-%d", len(f.filed)), Title: item.Title, Notes: item.Notes, Status: "open"}
	f.open = append(f.open, created)
	return created, nil
}

func (f *recordingFiler) List(_ context.Context, status string) ([]beads.WorkItem, error) {
	if status != "open" {
		return nil, nil
	}
	return f.open, nil
}

// Release gives a claimed item back to the harness's queue, exactly as the
// tracker does: the item is open, and pullable again.
func (h *scheduleHarness) Release(_ context.Context, id, _ string) (beads.WorkItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for index, item := range h.items {
		if item.ID == id {
			h.items[index].Status = "open"
			h.ready[id] = true
			return h.items[index], nil
		}
	}
	return beads.WorkItem{}, fmt.Errorf("no such work item %s", id)
}

// looked is a repository that answers for the one stopped run: whether its
// branch and its checkout are there.
type looked struct{ survival gitworktree.Survival }

func (l *looked) Survives(context.Context, gitworktree.Worktree) (gitworktree.Survival, error) {
	return l.survival, nil
}

type answeringForge struct {
	answer publish.PullRequest
	err    error
	asked  int
	heads  []string
}

func (f *answeringForge) State(_ context.Context, head string) (publish.PullRequest, error) {
	f.asked++
	f.heads = append(f.heads, head)
	if f.err != nil {
		return publish.PullRequest{}, f.err
	}
	return f.answer, nil
}

// Merge is never reached from a refresh or a finish: only the recovery of a
// promotion that recorded no request arms a merge, and none of the records these
// tests write is one.
func (f *answeringForge) Merge(context.Context, publish.MergeRequest) (publish.MergeResult, error) {
	return publish.MergeResult{}, errors.New("answeringForge merges nothing: a refresh only asks")
}

// Close is the write the refresh never makes. A refresh that reached it would
// be closing a request on the strength of an answer, which is the orphan
// sweep's decision and not this one's.
func (f *answeringForge) Close(context.Context, publish.CloseRequest) (publish.Closure, error) {
	return publish.Closure{}, errors.New("a refresh closes nothing")
}

var _ ReconcilePullRequests = (*answeringForge)(nil)

type publicationAnswers = orchestratortest.PublicationAnswers
