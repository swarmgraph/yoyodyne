package readmodel

// The operator's hand steps per merged change, counted once here.
//
// The events are internal/intervention's and the merged changes are the items
// the harness promoted (runstate.Store.Promoted). A step counts toward a change
// when it names the change's work item or any run made for it; a step naming
// nothing merged — a reconcile, a decision on a document, a restart — is counted
// as unattributed rather than spread across changes it did not name.
//
// The number is a floor and says so on every reading. A step the operator took
// outside the harness is in it only if somebody recorded it afterwards, so a
// change can have cost more than this and never less.

import (
	"sort"

	"github.com/mason-bryant/yoyodyne/internal/intervention"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// InterventionsFloor is what every reading of the count says about itself.
const InterventionsFloor = "a floor: a hand step taken outside the harness is counted only if somebody recorded it afterwards, so the true number can be higher and never lower"

// ChangeInterventions is the hand steps that named one merged change.
type ChangeInterventions struct {
	WorkItemID   string `json:"work_item_id"`
	ShippedRunID string `json:"shipped_run_id"`
	// Performed is the steps the harness carried out on the operator's word, and
	// Observed the steps taken outside it and recorded afterwards.
	Performed int `json:"performed"`
	Observed  int `json:"observed"`
	// Kinds is how many of each kind there were.
	Kinds map[intervention.Kind]int `json:"kinds,omitempty"`
}

// Total is every step that named the change.
func (c ChangeInterventions) Total() int { return c.Performed + c.Observed }

// InterventionCount is the hand steps counted against a set of merged changes.
type InterventionCount struct {
	// Changes is one entry per merged change, in the order the changes were
	// given, including the changes no step named.
	Changes []ChangeInterventions `json:"changes"`
	// Merged is how many changes were counted against.
	Merged int `json:"merged"`
	// Attributed is the steps that named at least one of those changes, each
	// counted once however many changes it named; Unattributed is the rest.
	Attributed   int `json:"attributed"`
	Unattributed int `json:"unattributed"`
	// PerChange is Attributed over Merged, and is zero where nothing merged.
	PerChange float64 `json:"per_change"`
	// Floor says the count is a lower bound, and why.
	Floor string `json:"floor"`
}

// CountInterventions counts events against merged changes. The caller chooses
// both sets — the changes merged in a day or a week, and the events it read — so
// a period is a filter on what is passed in rather than something decided here.
func CountInterventions(events []intervention.Event, merged []runstate.PromotedItem) InterventionCount {
	count := InterventionCount{
		Changes: make([]ChangeInterventions, 0, len(merged)),
		Merged:  len(merged),
		Floor:   InterventionsFloor,
	}
	byItem := make(map[string]int, len(merged))
	byRun := make(map[string]int)
	for index, change := range merged {
		count.Changes = append(count.Changes, ChangeInterventions{
			WorkItemID:   change.WorkItemID,
			ShippedRunID: change.ShippedRunID,
		})
		byItem[change.WorkItemID] = index
		for _, runID := range change.RunIDs {
			byRun[runID] = index
		}
		if change.ShippedRunID != "" {
			byRun[change.ShippedRunID] = index
		}
	}
	for _, event := range events {
		named := map[int]bool{}
		for _, item := range event.Items {
			if index, ok := byItem[item]; ok {
				named[index] = true
			}
		}
		if index, ok := byRun[event.Run]; ok && event.Run != "" {
			named[index] = true
		}
		if len(named) == 0 {
			count.Unattributed++
			continue
		}
		count.Attributed++
		indexes := make([]int, 0, len(named))
		for index := range named {
			indexes = append(indexes, index)
		}
		sort.Ints(indexes)
		for _, index := range indexes {
			change := &count.Changes[index]
			if event.Observed {
				change.Observed++
			} else {
				change.Performed++
			}
			if change.Kinds == nil {
				change.Kinds = map[intervention.Kind]int{}
			}
			change.Kinds[event.Kind]++
		}
	}
	if count.Merged > 0 {
		count.PerChange = float64(count.Attributed) / float64(count.Merged)
	}
	return count
}
