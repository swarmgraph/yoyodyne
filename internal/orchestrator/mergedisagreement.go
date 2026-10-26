package orchestrator

// A run is recorded as merged only once its commit has been found on the target
// branch, and this file is what answers the runs recorded before that held, or
// recorded some other way since: a merge disagreement is a read of the target
// branch that did not find the run's change while its record or the forge said
// it had merged. It is written on the run (runstate.MergeDisagreement) and put
// on the development manager's docket once, as a publication entry with an
// uncertain cause, keyed apart from the publication's other entries
// (triage.DisagreementKey).
//
// Nothing here acts on the run. It records and lists, and leaves the decision
// and the finishing of the merge to the steps that own them: it merges nothing,
// re-runs nothing, closes no pull request and touches no budget.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// MergeDisagreementReader is the one read the sweep makes of the repository:
// whether the remote target branch carries any of the commits named. It is
// satisfied by gitworktree.Manager.
type MergeDisagreementReader interface {
	RemoteTargetCarries(ctx context.Context, targetBranch string, commits ...string) (gitworktree.TargetReading, error)
}

// MergeDisagreementFinding is what the sweep found about one run recorded as
// merged whose item is still open.
type MergeDisagreementFinding struct {
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	Number     int    `json:"number,omitempty"`
	ForgeState string `json:"forge_state,omitempty"`
	// OnTarget reports the run's commit, or its merge commit, found on the target
	// branch, which is a run whose record is right and nothing to list.
	OnTarget bool `json:"on_target"`
	// Recorded reports this sweep wrote the disagreement on the run, and
	// Docketed that it is what put the run on the docket. A run found again by a
	// later sweep is neither: its disagreement and its entry already stand.
	Recorded bool `json:"recorded,omitempty"`
	Docketed bool `json:"docketed,omitempty"`
	// Settled reports this sweep closed the entry an earlier one listed, because
	// the run's change is now on the target branch.
	Settled bool   `json:"settled,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Kept    string `json:"kept,omitempty"`
	Failure string `json:"failure,omitempty"`
}

// disagreementCandidate reports a run the sweep reads the target branch for: a
// run recorded as merged whose change could only be on the remote target, which
// is a landing through the pull request or one the record says the forge merged.
// A local promotion whose request is unmerged is not one: its change is on the
// authoritative local branch already, and its unfinished publication is
// docketed as that. A document's landing is not one either; it is settled by its
// own path (documentpublication.go).
func disagreementCandidate(state runstate.State) bool {
	if !state.RecordedAsMerged() || state.HarnessAuthored() || state.PullRequest == nil {
		return false
	}
	return state.Integration.ThroughPullRequest || state.PullRequest.Merged
}

// FindMergeDisagreements reads the target branch for every run recorded as
// merged whose work item is still open, and for each one whose commit is not
// there writes the disagreement on the run and lists it on the development
// manager's docket. A run already carrying a disagreement is not written again,
// and its entry is recorded once by key and dated by the disagreement, so a
// second sweep adds nothing and leaves any decision recorded on the entry
// standing. A listed run whose change has since reached the branch has its entry
// closed.
//
// Which items are still open is read once for the whole sweep (closedItems),
// not once per run, so a long history costs one tracker listing and a git read
// only for the runs whose item is open.
//
// One run that cannot be read never stops the sweep; it is reported with its
// failure, and the next sweep asks again.
func (r Reconciler) FindMergeDisagreements(ctx context.Context) ([]MergeDisagreementFinding, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	reader, ok := r.Worktrees.(MergeDisagreementReader)
	if !ok {
		return nil, errors.New("finding runs recorded as merged that are not on their target branch requires reading the remote target branch, and this reconciler's repository access cannot")
	}
	recorded, err := r.Store.Recorded()
	if err != nil {
		return nil, fmt.Errorf("read the recorded runs to find runs recorded as merged that are not on their target branch: %w", err)
	}
	var candidates []runstate.State
	for _, state := range recorded {
		if disagreementCandidate(state) {
			candidates = append(candidates, state)
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	items := make([]string, 0, len(candidates))
	for _, state := range candidates {
		items = append(items, state.WorkItemID)
	}
	closed, unread, err := r.closedItems(ctx, items)
	if err != nil {
		return nil, &ClosedItemsUnread{Need: "find runs recorded as merged that are not on their target branch", Err: err}
	}
	var findings []MergeDisagreementFinding
	for _, state := range candidates {
		if problem, missed := unread[state.WorkItemID]; missed {
			findings = append(findings, MergeDisagreementFinding{RunID: state.RunID, WorkItemID: state.WorkItemID, Number: state.PullRequest.Number,
				Failure: fmt.Sprintf("whether work item %s is still open could not be read: %s", state.WorkItemID, problem)})
			continue
		}
		if closed[state.WorkItemID] {
			continue
		}
		findings = append(findings, r.findMergeDisagreement(ctx, reader, state))
	}
	return findings, nil
}

// closedItemLister is a tracker that can list its work items by status, which
// beads.Client is.
type closedItemLister interface {
	List(ctx context.Context, status string) ([]beads.WorkItem, error)
}

// maxItemReadsPastACut bounds how many items a cut listing left out are read
// one at a time: each is its own tracker call. An item past it is reported as
// unread, and the next sweep asks again.
const maxItemReadsPastACut = 40

// closedItems reads which of the named work items are closed, with one listing
// of the tracker's closed items. Where bd would not lift its default limit, the
// rows it returned are used and each named item they left out is read on its
// own, up to maxItemReadsPastACut; one that could not be read is returned in
// unread with why, because an item not known to be closed is not known to be
// open either. A tracker that cannot list is read one item at a time. Where the
// sweep shares one listing between its steps (ClosedListing), that is the
// listing read.
func (r Reconciler) closedItems(ctx context.Context, named []string) (map[string]bool, map[string]string, error) {
	closed := make(map[string]bool)
	unread := make(map[string]string)
	listed := make(map[string]bool)
	lister, canList := r.Tracker.(closedItemLister)
	if r.ClosedListing != nil {
		lister, canList = r.ClosedListing, true
	}
	limit := len(named)
	if canList {
		items, err := lister.List(ctx, "closed")
		var cut *beads.ListingCut
		switch {
		case err == nil:
		case errors.As(err, &cut):
			items = cut.Items
			limit = maxItemReadsPastACut
		default:
			return nil, nil, err
		}
		for _, item := range items {
			id := strings.TrimSpace(item.ID)
			closed[id] = strings.TrimSpace(item.Status) == "closed"
			listed[id] = true
		}
		if err == nil {
			return closed, unread, nil
		}
	}
	asked := 0
	for _, id := range named {
		if listed[id] {
			continue
		}
		listed[id] = true
		if asked >= limit {
			unread[id] = "the tracker's listing of closed work was cut short and this sweep already read as many items one at a time as it may"
			continue
		}
		asked++
		status, err := r.itemStatus(ctx, id)
		if err != nil {
			unread[id] = err.Error()
			continue
		}
		closed[id] = status == "closed"
	}
	return closed, unread, nil
}

// findMergeDisagreement reads one run's target branch and records what it found.
func (r Reconciler) findMergeDisagreement(ctx context.Context, reader MergeDisagreementReader, recorded runstate.State) MergeDisagreementFinding {
	published := *recorded.PullRequest
	finding := MergeDisagreementFinding{RunID: recorded.RunID, WorkItemID: recorded.WorkItemID, Number: published.Number, ForgeState: published.State}
	target := recorded.Integration.TargetBranch
	reading, err := reader.RemoteTargetCarries(ctx, target, recorded.Integration.TargetCommit, published.MergeCommit)
	if err != nil {
		finding.Failure = fmt.Errorf("read %s for run %s: %w", target, recorded.RunID, err).Error()
		return finding
	}
	if reading.Found != "" {
		finding.OnTarget = true
		// A run an earlier sweep listed whose change has since arrived is the
		// disagreement answered, so its entry is closed rather than left asking a
		// question the branch has settled. An entry a decision still holds is left
		// to it, and closed by the first sweep after that decision lapses.
		if recorded.MergeDisagreement != nil && r.Docket != nil {
			settled, err := r.Docket.SettlePublication(recorded,
				fmt.Sprintf("%s now carries %s, so the change run %s was recorded as merging is on its target branch", target, reading.Found, recorded.RunID))
			if err != nil {
				finding.Failure = fmt.Errorf("close the merge disagreement of run %s, whose change is now on %s: %w", recorded.RunID, target, err).Error()
			}
			finding.Settled = settled > 0
		}
		return finding
	}
	// The forge is asked what it says of the request now, because that is what
	// the entry names beside the branch. A forge that cannot be asked leaves the
	// state the record holds, which is still the forge's last word.
	if r.Publisher != nil {
		if observed, err := r.askState(ctx, published.Branch); err == nil && observed.Number == published.Number && strings.TrimSpace(observed.State) != "" {
			finding.ForgeState = observed.State
		}
	}

	state, lease, err := r.Store.AdoptRun(ctx, recorded.RunID)
	switch {
	case errors.Is(err, runstate.ErrRunHeld):
		finding.Kept = fmt.Sprintf("a live process holds run %s, so what its target branch shows is that process's to record", recorded.RunID)
		return finding
	case err != nil:
		finding.Failure = fmt.Errorf("adopt run %s to record that %s does not carry its change: %w", recorded.RunID, target, err).Error()
		return finding
	}
	defer lease.Release()
	if !disagreementCandidate(state) || state.PullRequest.Number != published.Number {
		finding.Kept = fmt.Sprintf("run %s changed while %s was read, so what it records now is what changed it", recorded.RunID, target)
		return finding
	}
	if state.MergeDisagreement == nil {
		state.MergeDisagreement = r.mergeDisagreement(state, reading, finding.ForgeState,
			fmt.Sprintf("run %s is recorded as merged and its work item %s is still open, but %s",
				state.RunID, state.WorkItemID, describeReading(reading, state.Integration.TargetCommit, published.MergeCommit)))
		state.UpdatedAt = r.clock().Now()
		if err := r.Store.Save(state); err != nil {
			finding.Failure = fmt.Errorf("record on run %s that %s does not carry its change: %w", state.RunID, target, err).Error()
			return finding
		}
		finding.Recorded = true
	}
	finding.Detail = state.MergeDisagreement.Detail
	if r.Docket != nil {
		docketed, err := r.Docket.RecordMergeDisagreement(state)
		if err != nil {
			finding.Failure = fmt.Errorf("docket the merge disagreement of run %s: %w", state.RunID, err).Error()
			return finding
		}
		finding.Docketed = docketed
	}
	return finding
}

// recordUnconfirmedMerge is what a reconcile sweep settling a landing through
// the pull request does when the forge says it merged and the target branch was
// not found to carry it: the run is left where it was, at its merge, with the
// forge's state on its request and the disagreement on the run, and listed on
// the docket. The next sweep asks the forge and reads the branch again.
//
// A read that failed rather than finding the commit absent is not a
// disagreement: nothing is known about the branch, so only the forge's state is
// written, and the failure is the sweep's to report. Neither writes the
// publication failure, which is the account of a publication that ended
// unfinished: this one has not ended, and the settlement that later confirms it
// reads that field as what is still outstanding.
func (r Reconciler) recordUnconfirmedMerge(state runstate.State, published runstate.PullRequest, forgeState string, failure error) (Reconciliation, error) {
	published.Merged = false
	published.State = forgeState
	state.PullRequest = &published
	if errors.Is(failure, gitworktree.ErrRemoteTargetMismatch) && state.MergeDisagreement == nil {
		state.MergeDisagreement = r.mergeDisagreement(state, gitworktree.TargetReading{TargetBranch: state.Integration.TargetBranch}, forgeState,
			fmt.Sprintf("the forge reports pull request %d %s, and %s", published.Number, nonEmpty(forgeState, "merged"), failure.Error()))
	}
	state.UpdatedAt = r.clock().Now()
	result := reconciliationOf(state, ActionUnsettled)
	result.Detail = fmt.Sprintf("the forge reports pull request %d merged into %s, and the branch was not found to carry it, so run %s is left at its merge: %s",
		published.Number, state.Integration.TargetBranch, state.RunID, failure)
	if err := r.Store.Save(state); err != nil {
		return result, fmt.Errorf("record that the merge of run %s was not found on %s: %w", state.RunID, state.Integration.TargetBranch, err)
	}
	if state.MergeDisagreement != nil && r.Docket != nil {
		if _, err := r.Docket.RecordMergeDisagreement(state); err != nil {
			result.DocketProblem = err.Error()
		}
	}
	return result, nil
}

// mergeDisagreement builds the record of one read.
func (r Reconciler) mergeDisagreement(state runstate.State, reading gitworktree.TargetReading, forgeState, detail string) *runstate.MergeDisagreement {
	disagreement := &runstate.MergeDisagreement{
		FoundAt:      r.clock().Now(),
		TargetBranch: state.Integration.TargetBranch,
		Commit:       state.Integration.TargetCommit,
		TargetTip:    reading.Tip,
		ForgeState:   forgeState,
		Cause:        runstate.MergeCauseUncertain,
		Detail:       oneline.Bound(detail, runstate.MaxRecordedTextBytes),
	}
	if state.PullRequest != nil {
		disagreement.PullRequest = state.PullRequest.Number
		disagreement.MergeCommit = state.PullRequest.MergeCommit
	}
	return disagreement
}

// describeReading says what one read of the target branch found, in a clause.
func describeReading(reading gitworktree.TargetReading, commit, mergeCommit string) string {
	if !reading.Exists {
		return fmt.Sprintf("the remote has no branch %s, so it carries neither the run's commit %s nor any merge of it", reading.TargetBranch, commit)
	}
	looked := "the run's commit " + commit
	if mergeCommit != "" {
		looked += " or the pull request's merge commit " + mergeCommit
	}
	return fmt.Sprintf("%s on the remote is at %s, and its history carries neither %s", reading.TargetBranch, reading.Tip, looked)
}

// RecordMergeDisagreement dockets one run whose disagreement is on its record,
// and reports whether this call is what created the entry. The entry is keyed to
// the run and its pull request apart from every other publication entry, and
// dated by the disagreement rather than by now (disagreementEntry), so it is
// recorded once however many sweeps find it, a decision recorded on it stands
// over every later sweep, and a docket rebuilt from the records derives the same
// entry (entriesFor).
func (d Docketer) RecordMergeDisagreement(state runstate.State) (bool, error) {
	if err := d.validate(); err != nil {
		return false, err
	}
	if state.MergeDisagreement == nil {
		return false, nil
	}
	entry, err := d.disagreementEntry(state, d.now())
	if err != nil {
		return false, err
	}
	return d.Docket.RecordOnce(entry)
}

// publicationNumber is the run's pull request number, and zero where it holds
// none, which is what the publication keys are built from.
func publicationNumber(state runstate.State) int {
	if state.PullRequest == nil {
		return 0
	}
	return state.PullRequest.Number
}

// disagreementEntry is the publication entry a merge disagreement is listed as:
// the request, the forge's state, what was read of the target branch, and the
// uncertain cause.
func (d Docketer) disagreementEntry(state runstate.State, now time.Time) (triage.Entry, error) {
	entry, err := d.unvalidatedPublicationEntry(state, now)
	if err != nil {
		return triage.Entry{}, err
	}
	disagreement := state.MergeDisagreement
	entry.Key = triage.DisagreementKey(state.RunID, entry.Publication.Number)
	// The entry is dated by the disagreement on the run rather than by the sweep
	// that lists it, because every sweep re-derives it from that one record. Dated
	// by the sweep, each one after a decision would read as a newer stoppage than
	// the decision (DocketStore.RecordOnce), list the run again with a new time,
	// and drop the wait the development manager had recorded on it. A run whose
	// record carries a later disagreement is the one that is listed again.
	if !disagreement.FoundAt.IsZero() {
		entry.RecordedAt = disagreement.FoundAt.UTC()
	}
	if forgeState := strings.TrimSpace(disagreement.ForgeState); forgeState != "" {
		entry.Publication.State = forgeState
	}
	entry.Publication.Message = oneline.Bound(disagreement.Detail, triage.MaxBlockerBytes)
	entry.Publication.Disagreement = &triage.MergeDisagreement{
		FoundAt:      disagreement.FoundAt.UTC(),
		TargetBranch: disagreement.TargetBranch,
		Commit:       disagreement.Commit,
		TargetTip:    disagreement.TargetTip,
		Cause:        triage.MergeDisagreementCause,
		Detail:       oneline.Bound(disagreement.Detail, triage.MaxBlockerBytes),
	}
	if err := entry.Validate(); err != nil {
		return triage.Entry{}, fmt.Errorf("docket the merge disagreement of run %s: %w", state.RunID, err)
	}
	return entry, nil
}
