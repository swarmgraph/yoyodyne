package review

import (
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// invocationRecorder is where the review about to be made keeps what it is
// sent: the run's own recorder, at the commit under review. It is nil for a
// reviewer wired with no record of invocations and for a branch review, which
// belongs to no run.
func (r Reviewer) invocationRecorder(request Request) backend.InvocationRecorder {
	if r.Invocations == nil || request.scope() == ScopeBranch {
		return nil
	}
	return r.Invocations.Recorder(request.RunID, domain.RoleReviewer, request.Changes.HeadCommit, r.clock().Now)
}
