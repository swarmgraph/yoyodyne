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

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// AssigneeTracker is the tracker access the clean-up needs: the unfinished
// items, and the write that clears one's assignee with a note saying why. It is
// satisfied by beads.Client.
type AssigneeTracker interface {
	ListStatuses(ctx context.Context, statuses []string) ([]beads.WorkItem, error)
	ClearLeftoverAssignee(ctx context.Context, id, note string) (beads.WorkItem, error)
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
// the clean-up made of it.
type LeftoverAssignee struct {
	WorkItemID string `json:"work_item_id"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	Assignee   string `json:"assignee"`
	// Because is why the clean-up cleared it or left it, in a reader's words.
	Because string `json:"because"`
}

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
		switch {
		case working[item.ID] != "":
			entry.Because = fmt.Sprintf("run %s is working on it now", working[item.ID])
			sweep.Held = append(sweep.Held, entry)
			continue
		case item.Status == "in_progress":
			entry.Because = "the tracker has it claimed with no run working on it; the claim audit gives it back"
			sweep.Held = append(sweep.Held, entry)
			continue
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
