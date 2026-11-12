package chat

// The development manager releasing the entry at the head of the harness's
// merge queue.
//
// A queue lands its entries in order, so an entry it cannot get past holds
// every landing behind it — the fix for that stop included, when the fix is
// itself queued behind it. Until this action, the only ways past were commands
// at a terminal and a person merging a pull request by hand. This is the same
// release as `yoyo queue hand-back` (orchestrator's mergequeuestopped.go),
// made from her conversation, under her name, with her reason, and with one
// more answer the command does not have: an entry whose approved commit is
// already on the target branch is recorded as landed outside the queue rather
// than handed back to a run that has nothing left to do.
//
// It is carried out as it is recorded, as a stop is. Releasing an entry starts
// no work and asks nothing of a provider; what it changes is the queue's own
// record and what that record writes onto the run and the work item, all of it
// done by the harness's own queue code.

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// The two things that can become of a released entry.
const (
	// queueReturn hands the entry back to its work item: its change is kept on
	// its run, the item is blocked with the reason, and the stopped run comes to
	// her docket like any other.
	queueReturn = "return"
	// queueLanded records the entry as landed outside the queue, which the
	// harness refuses unless the target branch already holds its approved
	// commit.
	queueLanded = "landed"
)

var queueDecisions = []string{queueReturn, queueLanded}

// QueueHeads releases the entry at the head of a merge queue. It reports, in
// the words the reply carries, what became of the entry, or why nothing did.
type QueueHeads interface {
	ReleaseHead(ctx context.Context, release QueueHeadRelease) (string, error)
}

// QueueHeadRelease is one release as the harness asks the queue for it.
type QueueHeadRelease struct {
	EntryID    string
	WorkItemID string
	Landed     bool
	Reason     string
	// By is who released it, written by the conversation from its own facts
	// rather than taken from the reply, so it cannot name anybody else.
	By string
}

var errNoQueueHeads = errors.New("this conversation cannot reach the merge queue, so nothing was released; the development manager's conversation is the one that can")

// queueProblems checks a release as far as the action alone can be checked.
func (a TrackerAction) queueProblems() []error {
	var problems []error
	switch entry := strings.TrimSpace(a.Entry); {
	case entry == "":
		problems = append(problems, errors.New("queue requires \"entry\", the merge queue entry at the head of the queue, as the status names it (mqe-...)"))
	case !strings.HasPrefix(entry, "mqe-"):
		problems = append(problems, fmt.Errorf("queue entry %q is not a merge queue entry; name it as the status does (mqe-...)", entry))
	}
	switch decision := strings.TrimSpace(a.Decision); {
	case decision == "":
		problems = append(problems, fmt.Errorf("queue requires \"decision\", one of %s", strings.Join(queueDecisions, ", ")))
	case decision != queueReturn && decision != queueLanded:
		problems = append(problems, fmt.Errorf("queue decision %q is not a decision; the decisions are %s", decision, strings.Join(queueDecisions, ", ")))
	}
	return problems
}

// releaseQueueHead carries a release out and reports what became of it. The
// queue writes the release onto the entry and the item itself, naming who
// released it and why, so nothing more is written from here.
func (s *Session) releaseQueueHead(ctx context.Context, outcome *TrackerOutcome) {
	if s.options.QueueHeads == nil {
		outcome.fail(errNoQueueHeads)
		return
	}
	action := outcome.Action
	said, err := s.options.QueueHeads.ReleaseHead(ctx, QueueHeadRelease{
		EntryID:    strings.TrimSpace(action.Entry),
		WorkItemID: strings.TrimSpace(action.ID),
		Landed:     strings.TrimSpace(action.Decision) == queueLanded,
		Reason:     strings.TrimSpace(action.Reason),
		By:         fmt.Sprintf("the %s in conversation %s, turn %d", RoleTitle(s.state.Role), s.state.ConversationID, s.state.Turns),
	})
	if err != nil {
		outcome.fail(err)
		return
	}
	outcome.applied("%s", said)
}
