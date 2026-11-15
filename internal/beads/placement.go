package beads

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// placedKey is where the tracker records that the Lead Product Manager placed an
// item first among work of its own priority (docs/designs/placing-an-item-first.md).
// It is kept on the item rather than in one harness's own state, so every
// harness sharing the repository reads the same order, and it is metadata
// rather than notes for the reason the parking is: selection reads it, and notes
// are what the next writer appends to.
//
// The value is one JSON object, and an empty value is no placement, so placing
// and removing are one write with one shape, as parking and releasing are.
const placedKey = "yoyodyne_placed_first"

// MaxPlacedPerPriority is how many items may be placed first in one priority at
// once. Placement is for promises, not a second scale of priorities.
const MaxPlacedPerPriority = 3

// MaxPlacementReasonBytes bounds the reason a placement keeps, for the reason a
// parking's is bounded: it is one line of every listing of the queue.
const MaxPlacementReasonBytes = domain.MaxWorkItemParkingBytes

// Placement records that an item was placed first among work of its own
// priority: who placed it, when, why, and the priority it was placed among. The
// priority is kept because the promise was about the item's place among that
// priority, so a placement whose item has since moved to another priority is
// over whatever the metadata still says.
type Placement struct {
	By       domain.AgentRole `json:"by"`
	At       time.Time        `json:"at"`
	Priority int              `json:"priority"`
	Reason   string           `json:"reason"`
}

// Valid reports a placement the harness could have written.
func (p Placement) Valid() bool {
	return p.By != "" && !p.At.IsZero() && strings.TrimSpace(p.Reason) != "" && p.Priority >= 0 && p.Priority <= MaxPriority
}

// Validate refuses a placement before it is written.
func (p Placement) Validate() error {
	var problems []error
	if !p.By.Valid() {
		problems = append(problems, fmt.Errorf("placement names no role it was placed by (%q)", p.By))
	}
	if p.At.IsZero() {
		problems = append(problems, errors.New("placement has no time"))
	}
	if p.Priority < 0 || p.Priority > MaxPriority {
		problems = append(problems, fmt.Errorf("placement priority %d is outside 0 to %d", p.Priority, MaxPriority))
	}
	reason := strings.TrimSpace(p.Reason)
	switch {
	case reason == "":
		problems = append(problems, errors.New("placement has no reason"))
	case len(reason) > MaxPlacementReasonBytes:
		problems = append(problems, fmt.Errorf("placement reason is %d bytes, more than the %d a placement keeps", len(reason), MaxPlacementReasonBytes))
	case strings.ContainsAny(reason, "\r\n"):
		problems = append(problems, errors.New("placement reason must be one line"))
	}
	return errors.Join(problems...)
}

// encode is the metadata value a placement is stored as; nil stores nothing,
// which is no placement.
func (p *Placement) encode() (string, error) {
	if p == nil || *p == (Placement{}) {
		return "", nil
	}
	stored := *p
	stored.At = stored.At.UTC()
	stored.Reason = strings.TrimSpace(stored.Reason)
	encoded, err := json.Marshal(stored)
	if err != nil {
		return "", fmt.Errorf("encode placement: %w", err)
	}
	return string(encoded), nil
}

// placementIn reads the placement the tracker records. An absent key, an empty
// value, and a value the harness could not have written all read as no
// placement: a placement nobody can account for must not move an item ahead of
// work that was promised nothing.
func placementIn(metadata map[string]json.RawMessage) *Placement {
	saved := metadataString(metadata, placedKey)
	if saved == "" {
		return nil
	}
	var placement Placement
	if json.Unmarshal([]byte(saved), &placement) != nil || !placement.Valid() {
		return nil
	}
	return &placement
}

// PlacedFirst is the placement that orders this item, and nil where none does.
// A placement orders an item only while the item is unfinished and still at
// the priority it was placed among: closing the item or changing its priority
// ends it, whether or not the write that records the ending has been made yet.
func (w WorkItem) PlacedFirst() *Placement {
	if w.Placement == nil || w.Status == statusClosed || w.Placement.Priority != w.Priority {
		return nil
	}
	return w.Placement
}

// Line is the one line every listing of the queue shows beside a placed item,
// with the time in the operator's local zone and the zone named.
func (p Placement) Line() string {
	return p.LineIn(time.Local)
}

// LineIn is Line with the zone named, for a caller that knows the operator's.
func (p Placement) LineIn(zone *time.Location) string {
	if zone == nil {
		zone = time.Local
	}
	return fmt.Sprintf("placed first among priority %d by the %s at %s: %s",
		p.Priority, p.By.Title(), p.At.In(zone).Format("15:04 MST on January 2"), strings.TrimSpace(p.Reason))
}

// Before reports whether p was placed ahead of other in the order placements
// keep: the earlier placement first, so a later promise never overtakes an
// earlier one.
func (p Placement) Before(other Placement) bool {
	return p.At.Before(other.At)
}

// PlacedAmong lists the items placed first among one priority, earliest
// placement first, leaving out the item named by except. It is what the limit
// of MaxPlacedPerPriority is counted from and what its refusal names.
func PlacedAmong(items []WorkItem, priority int, except string) []WorkItem {
	var placed []WorkItem
	for _, item := range items {
		if item.ID == except {
			continue
		}
		if placement := item.PlacedFirst(); placement != nil && placement.Priority == priority {
			placed = append(placed, item)
		}
	}
	for i := 1; i < len(placed); i++ {
		for j := i; j > 0 && placed[j].Placement.Before(*placed[j-1].Placement); j-- {
			placed[j], placed[j-1] = placed[j-1], placed[j]
		}
	}
	return placed
}
