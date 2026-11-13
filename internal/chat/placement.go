package chat

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// placeFirst carries out "place": the Lead Product Manager records that an item
// comes first among work of its own priority, and the scheduler then pulls it
// ahead of the unplaced work of that priority, earliest placement first
// (docs/designs/placing-an-item-first.md). Only BacklogOrder reaches here, which
// only she holds; the action has no lane-scoped name.
//
// A placement is refused, and nothing is written, where the item is already
// placed among its priority — placing it again would move it behind promises
// made after its own — and where its priority already has the most placements
// it may carry, naming those so she can remove one.
func (s *Session) placeFirst(ctx context.Context, outcome *TrackerOutcome) {
	id := outcome.WorkItemID
	if outcome.target == nil {
		outcome.fail(fmt.Errorf("cannot place %s first without reading its priority: %s", id, outcome.TargetUnread))
		return
	}
	target := *outcome.target
	if placed := target.PlacedFirst(); placed != nil {
		outcome.Failure = fmt.Sprintf("%s is already %s; nothing was changed. To place it again, remove its placement with \"unplace\" first, which puts it behind every placement made before the new one", id, placed.Line())
		return
	}
	placed, err := s.placedAmong(ctx, target.Priority, id)
	if err != nil {
		outcome.fail(fmt.Errorf("cannot count the items already placed first among priority %d: %w", target.Priority, err))
		return
	}
	if len(placed) >= beads.MaxPlacedPerPriority {
		outcome.Failure = placementLimitRefusal(id, target.Priority, placed)
		return
	}
	placement := beads.Placement{
		By:       s.state.Role,
		At:       s.options.clock().Now(),
		Priority: target.Priority,
		Reason:   strings.TrimSpace(outcome.Action.Reason),
	}
	change := beads.WorkItemChange{
		Placement:   &placement,
		AppendNotes: s.trackerProvenance(fmt.Sprintf("Placed first among priority %d", target.Priority), outcome.Action.Reason),
	}
	if _, err := s.options.Tracker.Update(ctx, id, change); err != nil {
		outcome.fail(err)
		return
	}
	ahead := "no other item is placed first among it"
	if len(placed) > 0 {
		ahead = fmt.Sprintf("it goes after the %s placed there before it", plural(len(placed), "item", "items"))
	}
	outcome.applied("placed %s first among priority %d, so it is pulled before the unplaced work of that priority; %s", id, target.Priority, ahead)
}

// removePlacement carries out "unplace": the Lead Product Manager takes back a
// placement, and the item goes back among the unplaced work of its priority,
// oldest admitted first. An item with no placement is refused rather than
// written to, so a stale picture of the queue cannot record a removal of
// something that is not there.
func (s *Session) removePlacement(ctx context.Context, outcome *TrackerOutcome) {
	id := outcome.WorkItemID
	if outcome.target == nil {
		outcome.fail(fmt.Errorf("cannot remove the placement of %s without reading it: %s", id, outcome.TargetUnread))
		return
	}
	target := *outcome.target
	placed := target.PlacedFirst()
	if placed == nil {
		outcome.Failure = fmt.Sprintf("%s is not placed first among its priority, so there was no placement to remove; nothing was changed", id)
		return
	}
	ended := beads.Placement{}
	change := beads.WorkItemChange{
		Placement: &ended,
		AppendNotes: s.trackerProvenance(fmt.Sprintf("Placement first among priority %d removed", placed.Priority), outcome.Action.Reason) +
			"\n\nIt had been " + placed.Line() + ".",
	}
	if _, err := s.options.Tracker.Update(ctx, id, change); err != nil {
		outcome.fail(err)
		return
	}
	outcome.applied("removed the placement of %s first among priority %d; it is pulled in the order of the unplaced work of its priority, oldest admitted first", id, placed.Priority)
}

// placedAmong reads the admitted items placed first among one priority, as the
// tracker holds them now, earliest placement first. Open and blocked work are
// both read because both are in the backlog's order.
func (s *Session) placedAmong(ctx context.Context, priority int, except string) ([]beads.WorkItem, error) {
	var admitted []beads.WorkItem
	for _, status := range []string{openWorkItemStatus, blockedWorkItemStatus} {
		items, err := s.options.Tracker.List(ctx, status)
		if err != nil {
			return nil, err
		}
		admitted = append(admitted, items...)
	}
	return beads.PlacedAmong(admitted, priority, except), nil
}

// placementLimitRefusal says why a placement past the limit was refused, naming
// every item already placed among that priority and when, so the Lead Product
// Manager can choose one to remove.
func placementLimitRefusal(id string, priority int, placed []beads.WorkItem) string {
	named := make([]string, 0, len(placed))
	for _, item := range placed {
		named = append(named, fmt.Sprintf("%s (%s), placed at %s", item.ID, singleLine(item.Title, maxSurveyTitleBytes),
			item.Placement.At.Local().Format("15:04 MST on January 2")))
	}
	return fmt.Sprintf("%s was not placed: priority %d already has %d items placed first, which is the most one priority may carry, so placement stays a promise rather than a second order of priorities. Already placed, earliest first: %s. Remove one with \"unplace\" before placing another",
		id, priority, beads.MaxPlacedPerPriority, strings.Join(named, "; "))
}

// placementEndedByPriority is the note a priority change leaves on an item
// whose placement it ended.
func placementEndedByPriority(placement beads.Placement, priority int) string {
	return fmt.Sprintf("Placement first among priority %d ended: the item's priority changed to %d. It had been %s.",
		placement.Priority, priority, placement.Line())
}
