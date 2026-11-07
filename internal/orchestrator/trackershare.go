package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The tracker between machines, in team mode (docs/designs/
// working-alongside-other-contributors.md, section 5). Each forge check reads
// the export committed on the shared target branch and merges it with this
// machine's tracker, item by item (beads.MergeExports). What can be merged comes
// into this machine's tracker. An item whose two versions cannot be merged is
// held for the development manager, naming both, and neither is edited: nothing
// is imported for it, nothing here ever writes to the shared export, and the
// hold is kept in this machine's own state rather than on the item
// (runstate.TrackerMergeStore).
//
// The check's two rules hold (forgecheck.go). A remote that cannot be read
// changes nothing. A merge that cannot finish records nothing as read: what the
// next merge compares against moves last, so the next check reads the same
// change and makes the same writes, and an import written twice changes nothing.

// TrackerShareRemote names the branch work lands on, which is the one the primary
// checkout is on, and reads a file as the remote has that branch. It is satisfied
// by *gitworktree.Manager.
type TrackerShareRemote interface {
	CurrentBranch(ctx context.Context) (string, error)
	SharedFile(ctx context.Context, targetBranch, path string) ([]byte, bool, error)
}

// TrackerShareClient is this machine's tracker. It is satisfied by beads.Client.
type TrackerShareClient interface {
	ExportRows(ctx context.Context) ([]byte, error)
	ImportRows(ctx context.Context, rows [][]byte) (beads.ImportAnswer, error)
}

// TrackerShare merges the shared export into this machine's tracker. It is wired
// only with team mode on.
type TrackerShare struct {
	Remote  TrackerShareRemote
	Tracker TrackerShareClient
	// Store is nil where it could not be opened, and StoreProblem says why. Every
	// check then says so, because a merge that silently stopped would be a tracker
	// quietly drifting apart.
	Store        *runstate.TrackerMergeStore
	StoreProblem error
	Reports      ReportCollector
	ProductID    domain.ProductID
	RepositoryID string
	Build        string
	Now          func() time.Time
}

// TrackerShareResult is what one merge found and did.
type TrackerShareResult struct {
	// NoSharedExport says the target branch carries no export to merge.
	NoSharedExport bool `json:"no_shared_export,omitempty"`
	// Unchanged says the shared export is the one the last merge read and no
	// hold stands, so the tracker was not asked for anything.
	Unchanged bool `json:"unchanged,omitempty"`
	// FromShared and Combined are the items this machine's tracker took, and
	// Kept the ones of them a newer write on this machine beat, which the next
	// check reads again.
	FromShared []string `json:"from_shared,omitempty"`
	Combined   []string `json:"combined,omitempty"`
	Kept       []string `json:"kept,omitempty"`
	// Held is every item held now, and Ended the holds this merge ended.
	Held  []string `json:"held,omitempty"`
	Ended []string `json:"ended,omitempty"`
}

// Says is the result in one line.
func (r TrackerShareResult) Says() string {
	switch {
	case r.NoSharedExport:
		return "the shared branch carries no tracker export, so there was nothing to merge"
	case r.Unchanged:
		return "the shared tracker export has not changed since the last check"
	}
	said := fmt.Sprintf("the shared tracker export was merged: %d item(s) taken from it, %d combined with this machine's version", len(r.FromShared), len(r.Combined))
	if len(r.Held) > 0 {
		said += fmt.Sprintf(", %d held for the development manager because the two versions cannot be merged", len(r.Held))
	}
	if len(r.Ended) > 0 {
		said += fmt.Sprintf(", %d hold(s) ended", len(r.Ended))
	}
	return said
}

// Check is one merge.
func (s TrackerShare) Check(ctx context.Context) (TrackerShareResult, error) {
	if s.Store == nil {
		return TrackerShareResult{}, fmt.Errorf("the record of what was merged could not be opened: %w", s.StoreProblem)
	}
	target, err := s.Remote.CurrentBranch(ctx)
	if err != nil {
		return TrackerShareResult{}, fmt.Errorf("name the shared branch: %w", err)
	}
	shared, found, err := s.Remote.SharedFile(ctx, target, beads.ExportPath)
	if err != nil {
		return TrackerShareResult{}, fmt.Errorf("read the shared tracker export: %w", err)
	}
	if !found {
		return TrackerShareResult{NoSharedExport: true}, nil
	}
	holds, err := s.Store.Holds()
	if err != nil {
		return TrackerShareResult{}, fmt.Errorf("read the merge holds: %w", err)
	}
	// With nothing new on the shared side and nothing held, a change on this
	// machine has nothing to meet, so the store's whole export is not read.
	if len(holds) == 0 && s.Store.AlreadyRead(shared) {
		return TrackerShareResult{Unchanged: true}, nil
	}
	base, hasBase, err := s.Store.Base()
	if err != nil {
		return TrackerShareResult{}, fmt.Errorf("read what was last taken from the shared export: %w", err)
	}
	ours, err := s.Tracker.ExportRows(ctx)
	if err != nil {
		return TrackerShareResult{}, fmt.Errorf("export this machine's tracker: %w", err)
	}
	settled := map[string]string{}
	decided := map[string]bool{}
	for _, hold := range holds {
		if hold.Decision == nil {
			continue
		}
		decided[hold.WorkItemID] = true
		// The version decided against becomes the earlier one, so only the kept
		// version reads as changed.
		settled[hold.WorkItemID] = hold.ThisMachine
		if hold.Decision.Keep == runstate.TrackerVersionThisMachine {
			settled[hold.WorkItemID] = hold.Shared
		}
	}
	merge, err := beads.MergeExports(beads.ExportMergeInput{Base: base, HasBase: hasBase, ThisMachine: ours, Shared: shared, Settled: settled})
	if err != nil {
		return TrackerShareResult{}, err
	}
	result := TrackerShareResult{FromShared: merge.FromShared, Combined: merge.Combined}
	conflicted := map[string]bool{}
	for _, conflict := range merge.Conflicts {
		conflicted[conflict.ID] = true
	}
	// Holds first, so nothing starts on an item this merge found it cannot merge
	// while the rest of the merge is still being written.
	if err := s.recordHolds(ctx, target, merge.Conflicts); err != nil {
		return result, err
	}
	for _, conflict := range merge.Conflicts {
		result.Held = append(result.Held, conflict.ID)
	}
	answer, err := s.Tracker.ImportRows(ctx, merge.Import)
	if err != nil {
		return result, fmt.Errorf("write what was merged into this machine's tracker: %w", err)
	}
	result.Kept = answer.Kept
	// A row a newer write here beat is still to be taken, so this shared export
	// is not recorded as read and the next check does not pass it over.
	read := shared
	if len(answer.Kept) > 0 {
		read = nil
	}
	if err := s.Store.SaveBase(merge.Base(answer.Kept...), read); err != nil {
		return result, err
	}
	// A decided item's hold ends only now that what it decided has been carried
	// out, and any other only once the two versions have come to agree.
	for _, hold := range holds {
		if conflicted[hold.WorkItemID] {
			continue
		}
		if err := s.endHold(ctx, hold.WorkItemID); err != nil {
			return result, err
		}
		result.Ended = append(result.Ended, hold.WorkItemID)
	}
	return result, nil
}

// recordHolds keeps a hold for each conflict and tells the development manager
// about any two versions she has not been told about. A hold on the same two
// versions keeps its record, and a decision on it. Two versions that have moved
// since are new to her, and any earlier decision was not about them.
func (s TrackerShare) recordHolds(ctx context.Context, target string, conflicts []beads.ExportConflict) error {
	release, err := s.Store.Lock(ctx)
	if err != nil {
		return err
	}
	defer release()
	now := s.now()
	for _, conflict := range conflicts {
		hold := runstate.TrackerMergeHold{
			WorkItemID:           conflict.ID,
			Title:                conflict.Title,
			HeldAt:               now,
			TargetBranch:         target,
			ThisMachine:          conflict.ThisMachine,
			Shared:               conflict.Shared,
			ThisMachineChangedAt: conflict.ThisMachineChangedAt,
			SharedChangedAt:      conflict.SharedChangedAt,
		}
		for _, difference := range conflict.Differences {
			hold.Differences = append(hold.Differences, runstate.TrackerMergeDifference(difference))
		}
		standing, found, err := s.Store.Hold(conflict.ID)
		if err != nil {
			return err
		}
		if found && standing.SameVersions(hold) {
			if standing.Reported {
				continue
			}
			hold = standing
		}
		if err := s.Store.SaveHold(hold); err != nil {
			return err
		}
		if err := s.tell(hold, now); err != nil {
			return err
		}
		hold.Reported = true
		if err := s.Store.SaveHold(hold); err != nil {
			return err
		}
	}
	return nil
}

func (s TrackerShare) endHold(ctx context.Context, workItemID string) error {
	release, err := s.Store.Lock(ctx)
	if err != nil {
		return err
	}
	defer release()
	return s.Store.EndHold(workItemID)
}

// tell puts a hold on the development manager's list, as a report addressed to
// her naming both versions and how she settles it.
func (s TrackerShare) tell(hold runstate.TrackerMergeHold, now time.Time) error {
	if s.Reports == nil {
		return errors.New("this check has nowhere to put reports")
	}
	title := ""
	if hold.Title != "" {
		title = " (" + oneline.Bound(hold.Title, 200) + ")"
	}
	message := fmt.Sprintf("Two machines changed %s%s in ways that cannot be merged, so it is held and neither version has been changed. %s Which stands is yours to decide: record it with the keep_version action, with the decision here to keep this machine's version or shared to keep the other, and the next forge check carries it out.",
		hold.WorkItemID, title, hold.DescribeVersions())
	entry := report.Entry{Severity: report.SeverityWarning, Message: oneline.Fold(message, report.MaxMessageBytes), For: domain.RoleDevelopmentManager}
	collected, err := report.Collect([]report.Entry{entry}, report.Attribution{
		Role: report.HarnessReporter, RunID: "tracker-merge@" + hold.HeldAt.UTC().Format(time.RFC3339), WorkItemID: hold.WorkItemID, Build: s.Build, ProductID: s.ProductID, RepositoryID: s.RepositoryID,
	}, now)
	if err != nil {
		return err
	}
	for _, reported := range collected {
		if err := s.Reports.Append(reported); err != nil {
			return err
		}
	}
	return nil
}

func (s TrackerShare) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
