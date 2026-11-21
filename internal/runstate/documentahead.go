package runstate

// One publication of a document at a time.
//
// Every revision of a document a role writes is published by a reviewed run of
// its own, which opens its own pull request and enters the merge queue as its
// own entry. A revision started while an earlier copy of the same document is
// still going, or still waiting in the merge queue, spends a document landing,
// a run and a review on a copy the earlier one is about to change under it, and
// leaves one more pull request for somebody to close. So no handoff of a
// document starts a run while another handoff's run of that document is still
// publishing its copy (State.PublishingCopy); it waits until that copy lands or
// is handed back. This file is the one test of that, read by the pipeline that
// refuses to start the run, the conversation that holds the revision, the
// scheduler that passes the waiting document over, and the read model that says
// what it waits for.

import (
	"fmt"
	"sort"
	"time"
)

// PublishingCopy reports a document run still publishing its copy: going, or
// passed and waiting in a merge queue — the harness's, or the forge's — with
// nothing having landed it, handed it back, or retired it. It is the same test
// the run's own owning conversation reads as not settled yet.
func (s State) PublishingCopy() bool {
	if s.Document == nil || s.Retirement != nil {
		return false
	}
	if s.Status.InFlight() {
		return true
	}
	return s.AwaitingMergeQueue() || (s.Status == StatusSucceeded && s.PullRequest != nil && s.PullRequest.MergeQueued)
}

// DocumentAhead is the run of another handoff of the same document that is
// still publishing its copy, which a handoff waits behind: which run, which
// handoff it carries, and where it stands.
type DocumentAhead struct {
	RunID          string `json:"run_id"`
	WriteID        string `json:"write_id"`
	ConversationID string `json:"conversation_id,omitempty"`
	// Queued says the run passed and its copy waits in a merge queue; false is
	// a run still going.
	Queued bool `json:"queued,omitempty"`
	// QueueEntry is the harness's merge queue entry, where the copy waits in it.
	QueueEntry  string `json:"queue_entry,omitempty"`
	PullRequest int    `json:"pull_request,omitempty"`
	// Since is when the copy entered the merge queue, or when its run started.
	Since time.Time `json:"since"`
}

// Says is where the earlier copy stands and what the waiting revision waits
// for, in the words the owning role, the schedule and the dashboard print.
func (a DocumentAhead) Says() string {
	where := fmt.Sprintf("is being published by run %s, started %s", a.RunID, a.Since.Local().Format("Jan 2 15:04 MST"))
	if a.Queued {
		where = "is waiting in the merge queue"
		if a.QueueEntry != "" {
			where += " as entry " + a.QueueEntry
		}
		if a.PullRequest > 0 {
			where += fmt.Sprintf(" (pull request #%d)", a.PullRequest)
		}
		where += fmt.Sprintf(" since %s, from run %s", a.Since.Local().Format("Jan 2 15:04 MST"), a.RunID)
	}
	return fmt.Sprintf("an earlier copy of this document, %s, %s, and this one is not published until that copy lands or is handed back", a.WriteID, where)
}

// PublishingAhead finds, among runs, the run of another handoff of the same
// document that is still publishing its copy, where there is one. A run of the
// same handoff — an earlier attempt at the same stored text — is that
// handoff's own, and never holds it. Where more than one is publishing, the
// latest started is named, because it is the copy that lands last.
func PublishingAhead(document DocumentPublication, runs []State) (DocumentAhead, bool) {
	own := document.RunID()
	id := document.Candidate.Artifact.ID
	var ahead []State
	for _, run := range runs {
		if run.Document == nil || run.Document.Candidate.Artifact.ID != id || run.Document.RunID() == own {
			continue
		}
		if run.PublishingCopy() {
			ahead = append(ahead, run)
		}
	}
	if len(ahead) == 0 {
		return DocumentAhead{}, false
	}
	sort.SliceStable(ahead, func(i, j int) bool {
		if !ahead[i].StartedAt.Equal(ahead[j].StartedAt) {
			return ahead[i].StartedAt.After(ahead[j].StartedAt)
		}
		return ahead[i].RunID < ahead[j].RunID
	})
	run := ahead[0]
	found := DocumentAhead{RunID: run.RunID, WriteID: run.Document.WriteID, ConversationID: run.Document.ConversationID, Since: run.StartedAt}
	if !run.Status.InFlight() {
		found.Queued = true
		if run.CompletedAt != nil {
			found.Since = *run.CompletedAt
		}
		if run.MergeQueue != nil {
			found.QueueEntry = run.MergeQueue.EntryID
			if !run.MergeQueue.AdmittedAt.IsZero() {
				found.Since = run.MergeQueue.AdmittedAt
			}
		}
		if run.PullRequest != nil {
			found.PullRequest = run.PullRequest.Number
		}
	}
	return found, true
}
