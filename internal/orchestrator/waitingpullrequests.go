package orchestrator

// The limit on approved changes waiting for a person to merge them.
//
// Under a merge setting that leaves the merge to a person, every approved
// change the harness publishes waits on that person, and a harness that went on
// starting new work would only pile up more of them, each built on a target
// that has moved further from the rest. So at execution.max_waiting_pull_requests
// the pull chooses nothing more from the queue. It is read after everything
// that is not new work: decided repairs and re-runs carried out, runs paused on
// other work continued, runs re-adopted after a restart, documents landed, and
// outside files brought in all go ahead as they would have, and reviews and the
// bringing up to date of the waiting pull requests happen inside runs and the
// forge check rather than here. The count is read again at every pull, so a
// merge or a closing the forge check settles starts new work again at the next
// one with nothing to release. Zero is no limit.

import (
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// waitingPullRequestsReached reads whether the limit stands, and records the
// reading on the schedule where it does. A pull wired without the limit or the
// record reads none. A record that cannot be read is said on the schedule and
// the pull is made as though the limit were not reached: a limit nobody can
// show is reached must not stop the line.
func (s Scheduler) waitingPullRequestsReached(schedule *Schedule, pull Pull) (runstate.WaitingPullRequests, bool) {
	schedule.WaitingPullRequests = nil
	if pull.MaxWaitingPullRequests <= 0 || pull.PersonMerges == nil {
		schedule.WaitingPullRequestsProblem = ""
		return runstate.WaitingPullRequests{}, false
	}
	states, err := pull.PersonMerges.WaitingForPersonMerge()
	if err != nil {
		schedule.WaitingPullRequestsProblem = fmt.Sprintf("the pull requests waiting for a person to merge could not be read, so new work was started as though fewer than %d were waiting: %v", pull.MaxWaitingPullRequests, err)
		return runstate.WaitingPullRequests{}, false
	}
	schedule.WaitingPullRequestsProblem = ""
	reading := runstate.WaitingPullRequestsOf(pull.MaxWaitingPullRequests, states)
	if !reading.Reached() {
		return runstate.WaitingPullRequests{}, false
	}
	schedule.WaitingPullRequests = &reading
	return reading, true
}
