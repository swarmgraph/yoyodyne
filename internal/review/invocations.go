package review

import (
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// invocationRecorder is where the review about to be made keeps what it is
// sent: the run's own recorder, at the commit under review. It is nil for a
// reviewer wired with no record of invocations and for a branch review, which
// belongs to no run. A merge-queue candidate review is kept with the run its
// queue entry names (Request.InvocationRunID), at the candidate's commit.
func (r Reviewer) invocationRecorder(request Request) backend.InvocationRecorder {
	if r.Invocations == nil || request.scope() == ScopeBranch {
		return nil
	}
	commit := request.Changes.HeadCommit
	if request.InvocationCommit != "" {
		commit = request.InvocationCommit
	}
	return r.Invocations.Recorder(request.invocationRunID(), domain.RoleReviewer, commit, r.clock().Now)
}

func (request Request) invocationRunID() string {
	if request.InvocationRunID != "" {
		return request.InvocationRunID
	}
	return request.RunID
}
