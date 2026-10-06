package orchestrator

import (
	"context"
	"errors"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/publish"
)

// Old names still used by test files from publish_test.go on, kept until those
// files move onto orchestratortest's fakes.

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
