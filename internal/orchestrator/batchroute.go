package orchestrator

// The batch route's own record keeping on the forge check: saying on each item's
// notes where it stands on the harness's own batch pull request, and ending the
// record when the pull request merges or closes. Opening, growing and merging
// the batch are not here. With execution.batch_pull_request off, or
// execution.upstream_branch unset, Reconciler.BatchRoute is nil and none of
// this runs. The words are the ones `yoyo status` and the dashboard use
// (runstate.BatchRecord.StateOf, runstate.WaitingOn).

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// BatchRecordKeeper is where the batch record is read and written, satisfied by
// *runstate.BatchRecordStore.
type BatchRecordKeeper interface {
	Load(targetBranch string) (runstate.BatchRecord, bool, error)
	Save(record runstate.BatchRecord) error
}

// BatchRoute is the route's settings and its record. A nil route is off.
type BatchRoute struct {
	Settings config.BatchPullRequest
	Records  BatchRecordKeeper
}

// batchNotePrefix starts every line this route writes on an item's notes, so
// the latest one can be found and a repeat is not written.
const batchNotePrefix = "Batch pull request:"

// BatchNoteSettlement is what a check did about one item's batch note.
type BatchNoteSettlement struct {
	WorkItemID string                  `json:"work_item_id"`
	State      runstate.BatchItemState `json:"state"`
	// Written is set where the item's notes now say the state; false where they
	// already ended on it.
	Written bool   `json:"written"`
	Failure string `json:"failure,omitempty"`
}

// batchNote is the line an item's notes carry for its state.
func batchNote(state runstate.BatchItemState, record *runstate.BatchRecord, wait runstate.BatchWait) string {
	switch state {
	case runstate.BatchUnpublished:
		return fmt.Sprintf("%s unpublished: it is on the target branch and in no open batch. %s", batchNotePrefix, wait.Says)
	case runstate.BatchMerged:
		return fmt.Sprintf("%s merged as pull request #%d.", batchNotePrefix, record.PullRequest.Number)
	}
	return fmt.Sprintf("%s %s: pull request #%d carries it at %s. %s", batchNotePrefix, state, record.PullRequest.Number, shortBatchCommit(record.Head), wait.Says)
}

func shortBatchCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

// lastBatchNote is the item's latest line written by this route, and "" where
// there is none. Only the part up to the sentence about what the batch waits on
// is compared, so a changed wait does not write a note of its own.
func lastBatchNote(notes string) string {
	last := ""
	for _, line := range strings.Split(strings.ReplaceAll(notes, "\r\n", "\n"), "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, batchNotePrefix) {
			last = line
		}
	}
	return batchStateOfNote(last)
}

// batchStateOfNote is a note's state and pull request, without the sentence
// after it.
func batchStateOfNote(note string) string {
	if cut := strings.Index(note, ". "); cut >= 0 {
		return note[:cut]
	}
	return note
}

// NoteBatchStates says on each waiting item's notes where it stands on the batch
// route, when that is not what its notes already end on. The state is read from
// the batch record, and an item the record does not carry is unpublished.
func (r Reconciler) NoteBatchStates(ctx context.Context) ([]BatchNoteSettlement, error) {
	if r.BatchRoute == nil {
		return nil, nil
	}
	waiting, err := r.waitingForTeam()
	if err != nil {
		return nil, err
	}
	items := runstate.WaitingForTeamMergeOf(waiting)
	records := map[string]*runstate.BatchRecord{}
	for _, item := range items {
		if _, read := records[item.TargetBranch]; read {
			continue
		}
		record, found, err := r.BatchRoute.Records.Load(item.TargetBranch)
		if err != nil {
			return nil, fmt.Errorf("read the batch record for %s: %w", item.TargetBranch, err)
		}
		if found {
			records[item.TargetBranch] = &record
		} else {
			records[item.TargetBranch] = nil
		}
	}
	unpublished := map[string][]runstate.WaitingForTeamMergeItem{}
	states := make([]runstate.BatchItemState, len(items))
	for index, item := range items {
		states[index] = runstate.BatchUnpublished
		if record := records[item.TargetBranch]; record != nil {
			states[index] = record.StateOf(item.WorkItemID, item.LandingCommit)
		}
		if states[index] == runstate.BatchUnpublished {
			unpublished[item.TargetBranch] = append(unpublished[item.TargetBranch], item)
		}
	}
	now := r.clock().Now()
	var settled []BatchNoteSettlement
	for index, item := range items {
		record := records[item.TargetBranch]
		wait := runstate.WaitingOn(record, readmodel.BatchWaitInputFor(r.BatchRoute.Settings, now, unpublished[item.TargetBranch]))
		result := BatchNoteSettlement{WorkItemID: item.WorkItemID, State: states[index]}
		note := batchNote(states[index], record, wait)
		shown, err := r.Tracker.Show(ctx, item.WorkItemID)
		switch {
		case err != nil:
			result.Failure = fmt.Errorf("load work item %s: %w", item.WorkItemID, err).Error()
		case shown.Status == closedStatus || lastBatchNote(shown.Notes) == batchStateOfNote(note):
		default:
			if _, err := r.Tracker.RecordOutcome(ctx, item.WorkItemID, note); err != nil {
				result.Failure = fmt.Errorf("record the batch state on %s: %w", item.WorkItemID, err).Error()
			} else {
				result.Written = true
			}
		}
		settled = append(settled, result)
	}
	return settled, nil
}

// batchPullRequestIs reports whether the pull request is the batch the record
// of this target branch holds. A route that is off holds none.
func (r Reconciler) batchPullRequestIs(targetBranch string, number int) (runstate.BatchRecord, bool) {
	if r.BatchRoute == nil {
		return runstate.BatchRecord{}, false
	}
	record, found, err := r.BatchRoute.Records.Load(targetBranch)
	if err != nil || !found || record.PullRequest == nil || record.PullRequest.Number != number {
		return runstate.BatchRecord{}, false
	}
	return record, true
}

// endBatchRecord writes that the batch pull request merged or closed, once, when
// the pull request is the batch's. It is written before the items are settled,
// so a restart that settles the rest still finds the batch ended.
func (r Reconciler) endBatchRecord(targetBranch string, number int, merged bool, commit string) error {
	record, ours := r.batchPullRequestIs(targetBranch, number)
	if !ours || record.Merged != nil || record.Closed != nil {
		return nil
	}
	at := r.clock().Now()
	ended := &runstate.BatchEnded{At: at, Commit: commit}
	if merged {
		record.Merged = ended
	} else {
		record.Closed = ended
	}
	record.UpdatedAt = at
	if err := r.BatchRoute.Records.Save(record); err != nil {
		return fmt.Errorf("record that the batch pull request #%d ended: %w", number, err)
	}
	return nil
}
