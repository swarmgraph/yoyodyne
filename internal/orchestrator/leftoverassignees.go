package orchestrator

// Clearing the assignees runs' claims left on unfinished items.
//
// Before claims recorded the assignee they set (beads/claimassignee.go), every
// run left this machine's Git name on its item, and nothing that gave the item
// back cleared it, so unfinished items named a person nobody had said was
// working on them (docs/diagnoses/yoyodyne-ifd-433-35-assignees-left-by-claims.md).
// Give-backs now clear what their claim set; this clears what was left before
// they did, and anything a give-back could not clear.
//
// Nothing on the item says who set an unmarked assignee, so the run records
// decide: an item a recorded run claimed had its assignee set by that claim,
// which the diagnosis found true of every item with a run record, and an item
// no run ever claimed had its assignee set by a person.
// An item a run is working on now is left alone, whoever set its assignee.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// AssigneeTracker is the tracker access the clean-up needs: the unfinished
// items, and the write that clears one's assignee with a note saying why. It is
// satisfied by beads.Client.
type AssigneeTracker interface {
	ListStatuses(ctx context.Context, statuses []string) ([]beads.WorkItem, error)
	ClearLeftoverAssignee(ctx context.Context, id, note string) (beads.WorkItem, error)
	MarkClaimAssignee(ctx context.Context, id string) (beads.WorkItem, error)
}

// AssigneeRuns is every run the harness has recorded, read for which items a
// run ever claimed and which one is working on now. It is satisfied by
// *runstate.Store.
type AssigneeRuns interface {
	Recorded() ([]runstate.State, error)
}

// AssigneeCleanup clears the assignees runs' claims left on unfinished items.
type AssigneeCleanup struct {
	Tracker AssigneeTracker
	Runs    AssigneeRuns
}

// LeftoverAssignee is one unfinished item that carries an assignee, and what
// the clean-up made of it. It is the record's own type, so the sweep and the
// record of the harness's one run of it say each item the same way.
type LeftoverAssignee = runstate.LeftoverAssignee

// AssigneeSweep is what one clean-up found: the assignees it cleared, or would
// clear when it is only reporting, the ones it left because a person set them,
// the ones it left because a run is working on the item, and the clears the
// tracker refused.
type AssigneeSweep struct {
	Cleared  []LeftoverAssignee `json:"cleared"`
	ByPerson []LeftoverAssignee `json:"left_set_by_a_person"`
	Held     []LeftoverAssignee `json:"left_held_by_a_run"`
	Problems []string           `json:"problems,omitempty"`
	// Applied is whether the clears were written, rather than only reported.
	Applied bool `json:"applied"`
}

// unfinishedAssigneeStatuses are the statuses an item that still matters is in.
// A closed item's assignee misleads nobody about who is working on it, so it is
// left.
var unfinishedAssigneeStatuses = []string{"open", "blocked", "in_progress"}

// Sweep reads every unfinished item that carries an assignee and clears the
// ones a run's claim set where no run is working on the item now. With apply
// false it writes nothing and reports what it would clear.
//
// An item the tracker holds as claimed with no run working on it is left too:
// the claim audit gives it back, and the give-back clears an assignee its claim
// marked. A claim older than the marker leaves one the next clean-up clears.
func (c AssigneeCleanup) Sweep(ctx context.Context, apply bool) (AssigneeSweep, error) {
	if c.Tracker == nil || c.Runs == nil {
		return AssigneeSweep{}, errors.New("clearing the assignees runs left behind requires the work tracker and the run records")
	}
	items, err := c.Tracker.ListStatuses(ctx, unfinishedAssigneeStatuses)
	if err != nil {
		return AssigneeSweep{}, fmt.Errorf("list the unfinished work items: %w", err)
	}
	runs, err := c.Runs.Recorded()
	if err != nil {
		return AssigneeSweep{}, fmt.Errorf("read the recorded runs: %w", err)
	}
	claimed := make(map[string]bool)
	working := make(map[string]string)
	for _, run := range runs {
		claimed[run.WorkItemID] = true
		if run.Status.InFlight() {
			working[run.WorkItemID] = run.RunID
		}
	}
	sweep := AssigneeSweep{Applied: apply}
	for _, item := range items {
		assignee := strings.TrimSpace(item.Assignee)
		if assignee == "" {
			continue
		}
		entry := LeftoverAssignee{WorkItemID: item.ID, Title: item.Title, Status: item.Status, Assignee: assignee}
		held := ""
		switch {
		case working[item.ID] != "":
			held = fmt.Sprintf("run %s is working on it now", working[item.ID])
		case item.Status == "in_progress":
			held = "the tracker has it claimed with no run working on it; the claim audit gives it back"
		}
		if held != "" {
			// The name is not cleared from under the run that holds the item. Where
			// a recorded run's claim set it before claims marked theirs, it is
			// marked as the claim's instead, so the give-back that ends the hold
			// clears it (beads.Client.MarkClaimAssignee) and nobody has to.
			if claimed[item.ID] && !item.ClaimAssignee {
				held += "; the name is now marked as a run's claim, so giving the item back clears it"
				if apply {
					if _, err := c.Tracker.MarkClaimAssignee(ctx, item.ID); err != nil {
						sweep.Problems = append(sweep.Problems, fmt.Sprintf("%s still names %s with nothing saying a run's claim set it, because marking it failed: %v", item.ID, assignee, err))
						continue
					}
				}
			} else if item.ClaimAssignee {
				held += "; the name is marked as a run's claim, so giving the item back clears it"
			}
			entry.Because = held
			sweep.Held = append(sweep.Held, entry)
			continue
		}
		switch {
		case !item.ClaimAssignee && !claimed[item.ID]:
			entry.Because = "no run has ever claimed it, so a person set the assignee"
			sweep.ByPerson = append(sweep.ByPerson, entry)
			continue
		}
		entry.Because = "a run's claim set it and no run is working on the item now"
		if item.ClaimAssignee {
			entry.Because = "the claim that set it recorded so, and no run is working on the item now"
		}
		if apply {
			note := fmt.Sprintf("The harness cleared the assignee %s, which a run's claim set and nothing gave back when the run ended. Nobody is working on this item now.", assignee)
			if _, err := c.Tracker.ClearLeftoverAssignee(ctx, item.ID, note); err != nil {
				sweep.Problems = append(sweep.Problems, fmt.Sprintf("%s still names %s, because clearing it failed: %v", item.ID, assignee, err))
				continue
			}
		}
		sweep.Cleared = append(sweep.Cleared, entry)
	}
	return sweep, nil
}

// AssigneeCleanupRecords is where the harness records that it has run the
// clean-up. It is satisfied by *runstate.AssigneeCleanupStore.
type AssigneeCleanupRecords interface {
	Load() (*runstate.AssigneeCleanupRecord, error)
	Record(runstate.AssigneeCleanupRecord) error
	Path() string
}

// OnceAssigneeCleanup is the clean-up as the scheduler runs it: once, by
// itself, on its first pass after claims began recording their assignee, so
// clearing the names older claims left is never a step left for a person.
// What it did is kept in the record, which is also what stops it running
// again; `yoyo tracker unassign` runs it again by hand where that is wanted.
type OnceAssigneeCleanup struct {
	Cleanup   AssigneeCleanup
	Records   AssigneeCleanupRecords
	ProductID domain.ProductID
	Now       func() time.Time
}

// RunOnce clears the names, records what it did, and answers with the record;
// it answers nil where the record says the clean-up has already run. A sweep
// that could not read the tracker or the runs, or that the tracker refused a
// clear or a mark in, records nothing, so the next pass tries again.
func (o OnceAssigneeCleanup) RunOnce(ctx context.Context) (*runstate.AssigneeCleanupRecord, error) {
	if o.Records == nil {
		return nil, errors.New("the clean-up of assignees runs left has nowhere to record that it ran")
	}
	done, err := o.Records.Load()
	if err != nil {
		return nil, err
	}
	if done != nil {
		return nil, nil
	}
	sweep, err := o.Cleanup.Sweep(ctx, true)
	if err != nil {
		return nil, err
	}
	// A sweep the tracker refused a write in is not recorded, so the next pass
	// runs it again: an item it could not clear or mark would otherwise keep a
	// name nothing ever clears. Everything that did land is not written twice,
	// because a cleared item no longer has a name and a marked one is left as it is.
	if len(sweep.Problems) > 0 {
		return nil, fmt.Errorf("%d item(s) could not be cleared or marked, so the next pass tries again: %s",
			len(sweep.Problems), strings.Join(sweep.Problems, "; "))
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	record := runstate.AssigneeCleanupRecord{
		SchemaVersion: runstate.AssigneeCleanupSchemaVersion,
		ProductID:     o.ProductID,
		RanAt:         now().UTC(),
		Cleared:       sweep.Cleared,
		ByPerson:      sweep.ByPerson,
		Held:          sweep.Held,
		Problems:      sweep.Problems,
	}
	if err := o.Records.Record(record); err != nil {
		if errors.Is(err, runstate.ErrAssigneeCleanupRecorded) {
			return nil, nil
		}
		return nil, fmt.Errorf("the assignees runs left were cleared and what was done could not be recorded, so the next pass runs the clean-up again: %w", err)
	}
	return &record, nil
}

// DescribeAssigneeCleanup is what a pass prints about a clean-up it ran: the counts, and where
// the whole list is kept.
func DescribeAssigneeCleanup(record runstate.AssigneeCleanupRecord, path string) string {
	said := fmt.Sprintf("cleared the assignee on %d unfinished item(s) where an ended run's claim had left it; left %d a person set and %d a run holds now, each of which has its name cleared when it is given back if a run's claim set it",
		len(record.Cleared), len(record.ByPerson), len(record.Held))
	return said + "; every item is listed in " + path
}
