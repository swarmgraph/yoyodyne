package cli

import (
	"context"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/goal"
	"github.com/mason-bryant/yoyodyne/internal/goalreview"
)

// goalReviewSource reads what the daily unmet-goal review goes through
// (orchestrator/goalreview.go): the goals in force, from the same goals
// documents `yoyo goals list` reads, and every work item with the goal its
// attribution resolves to, through the tracker work is admitted into.
type goalReviewSource struct {
	repository string
	product    config.Product
	items      interface {
		List(ctx context.Context, status string) ([]beads.WorkItem, error)
	}
}

func (s goalReviewSource) Read(ctx context.Context) (goalreview.Reading, error) {
	goals, err := loadGoals(s.repository, s.product)
	if err != nil {
		return goalreview.Reading{}, err
	}
	if reason, uncheckable := goals.Uncheckable(); uncheckable {
		return goalreview.Reading{}, fmt.Errorf("no goal that still applies can be read: %s", reason)
	}
	items, err := s.items.List(ctx, "")
	if err != nil {
		return goalreview.Reading{}, fmt.Errorf("list the work items: %w", err)
	}
	return goalReviewReading(goals, items), nil
}

// goalReviewReading is the goals in force and every item, each carrying the
// goal its attribution resolves to.
func goalReviewReading(goals goal.Set, items []beads.WorkItem) goalreview.Reading {
	var reading goalreview.Reading
	for _, stated := range goals.Goals {
		if !stated.InForce {
			continue
		}
		reading.Goals = append(reading.Goals, goalreview.Goal{Reference: stated.Reference(), Statement: stated.Statement})
	}
	for _, item := range items {
		read := goalreview.Item{
			ID:       item.ID,
			Title:    item.Title,
			Status:   item.Status,
			Priority: item.Priority,
			ClosedAt: item.ClosedAt,
			Labels:   append([]string(nil), item.Labels...),
		}
		if attribution := goals.AttributionOf(item.Notes, item.GoalWitness); attribution.Resolved() {
			read.Serves = attribution.Goal.Reference()
		}
		reading.Items = append(reading.Items, read)
	}
	return reading
}
