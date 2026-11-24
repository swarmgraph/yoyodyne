package orchestrator

// Putting a set-aside change back in the merge queue: the development
// manager's "readmit" answer for an entry she set aside (setAsideStopped).
//
// A set-aside writes nothing to the run's record, so the run still holds the
// approved commit its review approved. Readmitting admits that same commit to
// the same queue again through the queue's own admission, which takes a run
// whose earlier entry left without landing as the change's next entry, behind
// everything already waiting. The new entry's candidate is built, checked and
// reviewed by the queue from nothing, as every entry's is; the run's own work
// and review are not redone, and nothing is charged to it.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// MergeQueueReadmission is the development manager putting the change of an
// entry she set aside back in its queue.
type MergeQueueReadmission struct {
	// EntryID is the entry that was set aside.
	EntryID string
	// WorkItemID is the item the entry has to be for, where the caller names
	// one, as a release's is.
	WorkItemID string
	// Reason is why, and By who decided it, in words a person reads on the work
	// item and on her list.
	Reason string
	By     string
}

// MergeQueueReadmitted is what a readmission did.
type MergeQueueReadmitted struct {
	// SetAside is the entry that was set aside, and Entry the one that now
	// holds the same change in the queue.
	SetAside runstate.MergeQueueEntry
	Entry    runstate.MergeQueueEntry
	// Unrecorded says, in plain words, what of the readmission could not be
	// written beside the queue's own record: the run naming its new entry, the
	// note on the work item, or the run leaving her list. The change is in the
	// queue either way, because the queue's record is what lands it.
	Unrecorded []string
}

// MergeQueueSetAsideListing takes a set-aside run off the development
// manager's list once its change is back in the queue. MergeQueueRunHandback
// is the harness's.
type MergeQueueSetAsideListing interface {
	Readmitted(ctx context.Context, setAside, admitted runstate.MergeQueueEntry, by, reason string) error
}

// ReadmitSetAside admits the approved commit of an entry somebody set aside to
// its queue again. It refuses an entry the queue does not have, one that was
// not set aside by a decision, one whose change has landed, one whose run has
// moved on from that commit, and one whose change the target already carries.
// Asking again for a change already readmitted reports the entry that holds it
// rather than admitting it twice.
func (p MergeQueuePromoter) ReadmitSetAside(ctx context.Context, key runstate.MergeQueueKey, readmit MergeQueueReadmission) (MergeQueueReadmitted, error) {
	if err := p.validate(); err != nil {
		return MergeQueueReadmitted{}, err
	}
	by, reason := strings.TrimSpace(readmit.By), strings.TrimSpace(readmit.Reason)
	if by == "" || reason == "" {
		return MergeQueueReadmitted{}, errors.New("putting a set-aside change back in the merge queue says who decided it and why")
	}
	refuse := func(format string, args ...any) (MergeQueueReadmitted, error) {
		return MergeQueueReadmitted{}, MergeQueueEntryRefusal{EntryID: readmit.EntryID, Reason: fmt.Sprintf(format, args...)}
	}
	worker, held, err := p.Queue.LeaseWorker(ctx, key)
	if err != nil {
		return MergeQueueReadmitted{}, err
	}
	if !held {
		return MergeQueueReadmitted{}, ErrMergeQueueWorkerBusy
	}
	defer func() { _ = worker.Release() }()
	entry, landing, err := p.entry(key, readmit.EntryID)
	if err != nil {
		return MergeQueueReadmitted{}, err
	}
	if want := strings.TrimSpace(readmit.WorkItemID); want != "" && want != entry.WorkItemID {
		return refuse("it is the entry of %s, not of %s, the item named; nothing was admitted", MergeQueueEntryItem(entry), want)
	}
	switch handback := landing.Handback; {
	case landing.Completion != nil:
		return refuse("its change has landed")
	case handback == nil || !handback.DecidedSetAside():
		return refuse("it was not set aside, so there is no kept change to put back; only an entry set aside with the \"set-aside\" answer is readmitted")
	}
	done := MergeQueueReadmitted{SetAside: entry}
	entries, err := p.Queue.Entries(key)
	if err != nil {
		return MergeQueueReadmitted{}, err
	}
	for _, later := range entries {
		if later.Supersedes == entry.EntryID {
			// Already readmitted: what follows the admission is finished again,
			// which is how a readmission cut short is completed.
			done.Entry = later
			p.afterReadmission(ctx, &done, by, reason)
			return done, nil
		}
	}
	run, err := p.Pipeline.Store.Load(entry.RunID)
	if err != nil {
		return MergeQueueReadmitted{}, fmt.Errorf("read run %s, whose change entry %d holds: %w", entry.RunID, entry.Order, err)
	}
	switch {
	case !run.Status.Terminal():
		return refuse("run %s is running again, so its change is not the one that was set aside", entry.RunID)
	case run.MergeQueue == nil || run.MergeQueue.EntryID != entry.EntryID || run.MergeQueue.ApprovedHead != entry.ApprovedHead:
		return refuse("run %s has moved on from the change entry %d held, so that change is not put back", entry.RunID, entry.Order)
	}
	if carried, err := p.Lander.TargetHolds(ctx, key.TargetBranch, entry.ApprovedHead); err != nil {
		return MergeQueueReadmitted{}, fmt.Errorf("read whether %s carries the approved commit %s: %w", key.TargetBranch, entry.ApprovedHead, err)
	} else if carried {
		return refuse("%s already carries its approved commit %s, so there is nothing to land", key.TargetBranch, entry.ApprovedHead)
	}
	evidence, refusal, err := p.transferEvidence(ctx, key.TargetBranch, entry.Mode)
	if err != nil {
		return MergeQueueReadmitted{}, err
	}
	if refusal != "" {
		return refuse("%s", refusal)
	}
	admitted, _, err := p.Queue.Admit(ctx, runstate.MergeQueueAdmission{
		Key: key, WorkItemID: entry.WorkItemID, WorkItemTitle: entry.WorkItemTitle, RunID: entry.RunID,
		Publication: entry.Publication, ApprovedHead: entry.ApprovedHead, IntegrationPolicy: entry.IntegrationPolicy,
		Mode: entry.Mode, ModeEvidence: evidence, At: p.Pipeline.clock().Now(),
	})
	if err != nil {
		return MergeQueueReadmitted{}, fmt.Errorf("admit the change of entry %d to the merge queue for %s again: %w", entry.Order, key.TargetBranch, err)
	}
	if admitted.Supersedes != entry.EntryID {
		return MergeQueueReadmitted{}, fmt.Errorf("the merge queue for %s holds run %s's change as entry %d, which does not follow entry %d", key.TargetBranch, entry.RunID, admitted.Order, entry.Order)
	}
	done.Entry = admitted
	p.afterReadmission(ctx, &done, by, reason)
	return done, nil
}

// afterReadmission writes what follows a readmission beside the queue's own
// record: the run naming its new entry, a note on the work item, and the run
// taken off the development manager's list. Each is asked once however often
// the readmission is, and what could not be written is said rather than
// undoing the admission.
func (p MergeQueuePromoter) afterReadmission(ctx context.Context, done *MergeQueueReadmitted, by, reason string) {
	setAside, admitted := done.SetAside, done.Entry
	if err := p.recordReadmission(ctx, admitted); err != nil {
		done.Unrecorded = append(done.Unrecorded, fmt.Sprintf("run %s's record could not be made to name entry %d (%v); the queue's record is what lands the change", admitted.RunID, admitted.Order, err))
	}
	marker := fmt.Sprintf("Merge queue entry %s readmitted as %s", setAside.EntryID, admitted.EntryID)
	note := oneline.Fold(fmt.Sprintf("%s put the change set aside as merge queue entry %d back in the queue for %s because %s. It is entry %d now, at the same approved commit %s; the queue builds, checks and reviews its candidate again, and nothing is charged to run %s. %s.",
		by, setAside.Order, setAside.TargetBranch, reason, admitted.Order, admitted.ApprovedHead, admitted.RunID, marker), 4000)
	if item, err := p.Pipeline.Tracker.Show(ctx, admitted.WorkItemID); err != nil {
		done.Unrecorded = append(done.Unrecorded, fmt.Sprintf("%s could not be read to note the readmission on it (%v)", admitted.WorkItemID, err))
	} else if !strings.Contains(item.Notes, marker) {
		if _, err := p.Pipeline.Tracker.RecordOutcome(ctx, admitted.WorkItemID, note); err != nil {
			done.Unrecorded = append(done.Unrecorded, fmt.Sprintf("the readmission could not be noted on %s (%v)", admitted.WorkItemID, err))
		}
	}
	if listing, ok := p.Repair.(MergeQueueSetAsideListing); ok {
		if err := listing.Readmitted(ctx, setAside, admitted, by, reason); err != nil {
			done.Unrecorded = append(done.Unrecorded, fmt.Sprintf("run %s could not be taken off the development manager's list (%v)", admitted.RunID, err))
		}
	}
}

// Readmitted settles the set-aside run's entry on the development manager's
// list with her decision, so the list no longer asks her about a change that
// is back in the queue.
func (h MergeQueueRunHandback) Readmitted(_ context.Context, setAside, admitted runstate.MergeQueueEntry, by, reason string) error {
	if h.Docket == nil || h.Docket.Docket == nil {
		return nil
	}
	_, err := h.Docket.Docket.Close(triage.Closure{
		SchemaVersion: triage.ClosureSchemaVersion,
		Key:           triage.Key(triage.ClassStoppedRun, setAside.RunID),
		ProductID:     setAside.ProductID,
		RunID:         setAside.RunID,
		WorkItemID:    setAside.WorkItemID,
		Decision:      "readmit",
		Reason: oneline.Fold(fmt.Sprintf("%s; the change is back in the merge queue for %s as entry %d, at the same approved commit %s",
			reason, admitted.TargetBranch, admitted.Order, admitted.ApprovedHead), triage.MaxMessageBytes),
		DecidedBy: oneline.Fold(by, triage.MaxMessageBytes),
		ClosedAt:  h.now().UTC(),
	})
	return err
}

var _ MergeQueueSetAsideListing = MergeQueueRunHandback{}
