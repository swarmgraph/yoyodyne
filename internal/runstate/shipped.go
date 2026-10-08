package runstate

// What shipped lately, and what it took. An item's price is a join this package
// already makes (price.go); this is the same join read for the items whose
// work the harness promoted, newest promotion first, with the wall clock beside
// the money. The join exists once: a ledger that re-summed the runs its own way
// would be a second figure for the one number an operator budgets against.
//
// The wall clock is two numbers rather than one. Elapsed is first claim to
// promotion, and it includes every hour the item spent parked on a provider
// usage limit or the operator's hold; paused is those hours on their own. A
// four-hour item that spent three of them parked reads as what it was only when
// both are beside each other, which is why neither is ever folded into the
// other.
//
// Every moment here is one the records already keep. Elapsed is read from the
// runs' own started and completed times, paused from the wait seconds each run
// commits as it waits, so nothing is bookkept for this ledger alone. A record
// written before something was recorded reports what it can and says the rest
// is unknown, never zero: an elapsed time of nothing and an elapsed time nobody
// can compute are opposite facts, and only one of them is a duration.

import (
	"sort"
	"time"
)

// ShippedItem is one work item the harness promoted, priced across every run
// made for it and timed from the first claim to the promotion.
type ShippedItem struct {
	WorkItemID string `json:"work_item_id"`
	// Title is what the item was called when its newest run that recorded a title
	// claimed it, and is empty where no run of the item recorded one — every run
	// written before titles were carried. The run record is the source rather
	// than the tracker, for the reason the price is: this is read from the runs
	// alone, and answers wherever they are.
	Title string `json:"title,omitempty"`
	// ShippedAt is when the run that promoted the item recorded completing, which
	// is the nearest moment the record keeps to the promotion itself. A promoting
	// run that has not recorded completing — still cleaning up, or dead in it —
	// is placed by its last update instead, and Elapsed says it is unknown.
	ShippedAt time.Time `json:"shipped_at"`
	// ShippedRunID is the run whose promotion this is: the newest integrated run
	// of the item, where an item was promoted more than once.
	ShippedRunID string `json:"shipped_run_id"`
	// ClaimedAt is the first claim: the earliest run's recorded claim, or where
	// the earliest run predates claims being recorded, its start, which is the
	// nearest moment before the claim the record keeps.
	ClaimedAt time.Time `json:"claimed_at"`
	// ElapsedSeconds is ShippedAt less ClaimedAt, and is meaningful only while
	// ElapsedUnknown is empty. It counts the paused seconds beside it, because it
	// is the wall clock: the item took this long, whatever it spent it on.
	ElapsedSeconds int64 `json:"elapsed_seconds,omitempty"`
	// ElapsedUnknown says why the elapsed time could not be read, and is empty on
	// an item whose could.
	ElapsedUnknown string `json:"elapsed_unknown,omitempty"`
	// Price is ifd.41's join: every run made for the item, the failed and
	// discarded attempts included, at the cost the provider reported, with the
	// waits every run committed. Paused is Price.Phases.Waits.
	Price ItemPrice `json:"price"`
}

// Elapsed is the wall clock from the first claim to the promotion, or zero with
// false where the record cannot say.
func (i ShippedItem) Elapsed() (time.Duration, bool) {
	if i.ElapsedUnknown != "" {
		return 0, false
	}
	return time.Duration(i.ElapsedSeconds) * time.Second, true
}

// Paused is the part of the elapsed time the item spent parked: on a provider
// that refused it for want of capacity, and on the operator's hold. It is
// summed across every run of the item, the runs that could not be priced
// included, because a wait is read from a run's own state rather than from the
// log that may be gone.
func (i ShippedItem) Paused() time.Duration {
	return i.Price.Phases.Waits.Total()
}

// ShippedLedger is the n most recently promoted items, and how many there are
// in all, so a bounded listing says what it is a part of.
type ShippedLedger struct {
	Items []ShippedItem `json:"items"`
	// Shipped is how many items the harness has promoted altogether, which is
	// what tells "the ten most recent" from "all ten".
	Shipped int `json:"shipped"`
}

// Shipped reports the items whose work the harness promoted, most recent
// promotion first, at most limit of them (0 reports every one). An item is
// shipped when a run of it recorded an integration: the durable evidence of a
// completed promotion, written by the run that made it. The tracker's own
// closed status is not consulted, because an item can close for reasons that
// are not a promotion and a promotion is what this ledger prices the time to.
//
// Only the items actually reported are priced, because pricing one reads every
// event log of every run made for it: the ten most recent must not cost a scan
// of every log the product has ever written.
func (s *Store) Shipped(limit int) (ShippedLedger, error) {
	states, err := s.scan("recorded", func(State) bool { return true })
	if err != nil {
		return ShippedLedger{}, err
	}
	byItem := make(map[string][]State)
	for _, state := range states {
		byItem[state.WorkItemID] = append(byItem[state.WorkItemID], state)
	}
	type promotion struct {
		item      string
		shippedAt time.Time
		runID     string
	}
	promotions := make([]promotion, 0, len(byItem))
	for item, runs := range byItem {
		latest, promoted := latestPromotion(runs)
		if !promoted {
			continue
		}
		promotions = append(promotions, promotion{item: item, shippedAt: shippedAt(latest), runID: latest.RunID})
	}
	sort.SliceStable(promotions, func(first, second int) bool {
		if !promotions[first].shippedAt.Equal(promotions[second].shippedAt) {
			return promotions[first].shippedAt.After(promotions[second].shippedAt)
		}
		return promotions[first].item < promotions[second].item
	})
	ledger := ShippedLedger{Items: []ShippedItem{}, Shipped: len(promotions)}
	if limit > 0 && len(promotions) > limit {
		promotions = promotions[:limit]
	}
	for _, promoted := range promotions {
		ledger.Items = append(ledger.Items, s.ship(promoted.item, byItem[promoted.item]))
	}
	return ledger, nil
}

// PromotedItem is one item the harness promoted and every run made for it, read
// without pricing anything: what a count joined to promoted changes needs, such
// as the operator's hand steps per merged change.
type PromotedItem struct {
	WorkItemID   string    `json:"work_item_id"`
	ShippedAt    time.Time `json:"shipped_at"`
	ShippedRunID string    `json:"shipped_run_id"`
	// RunIDs is every run of the item, oldest first, the failed and discarded
	// attempts included: a hand step that named any of them was a step on the
	// way to this change.
	RunIDs []string `json:"run_ids"`
}

// Promoted reports every item the harness promoted, oldest promotion first. It
// is Shipped's reading of which items shipped and when, without the price that
// makes Shipped read every run's event log.
func (s *Store) Promoted() ([]PromotedItem, error) {
	states, err := s.scan("recorded", func(State) bool { return true })
	if err != nil {
		return nil, err
	}
	byItem := make(map[string][]State)
	for _, state := range states {
		byItem[state.WorkItemID] = append(byItem[state.WorkItemID], state)
	}
	promoted := make([]PromotedItem, 0, len(byItem))
	for item, runs := range byItem {
		latest, ok := latestPromotion(runs)
		if !ok {
			continue
		}
		sort.SliceStable(runs, func(first, second int) bool {
			return runs[first].StartedAt.Before(runs[second].StartedAt)
		})
		runIDs := make([]string, 0, len(runs))
		for _, run := range runs {
			runIDs = append(runIDs, run.RunID)
		}
		promoted = append(promoted, PromotedItem{
			WorkItemID:   item,
			ShippedAt:    shippedAt(latest),
			ShippedRunID: latest.RunID,
			RunIDs:       runIDs,
		})
	}
	sort.SliceStable(promoted, func(first, second int) bool {
		if !promoted[first].ShippedAt.Equal(promoted[second].ShippedAt) {
			return promoted[first].ShippedAt.Before(promoted[second].ShippedAt)
		}
		return promoted[first].WorkItemID < promoted[second].WorkItemID
	})
	return promoted, nil
}

// latestPromotion is the newest run of an item that recorded an integration,
// or false where none did. Newest by when it shipped rather than by when it
// started, so an old run resumed into its promotion after a newer attempt is
// still the promotion that counts.
func latestPromotion(runs []State) (State, bool) {
	var latest State
	promoted := false
	for _, run := range runs {
		if run.Integration == nil {
			continue
		}
		if !promoted || shippedAt(run).After(shippedAt(latest)) {
			latest, promoted = run, true
		}
	}
	return latest, promoted
}

// shippedAt is the moment a promoting run is placed at: when it recorded
// completing, or its last update where it has not.
func shippedAt(run State) time.Time {
	if run.CompletedAt != nil {
		return *run.CompletedAt
	}
	return run.UpdatedAt
}

// ship joins one promoted item's runs into a ledger entry.
func (s *Store) ship(workItemID string, runs []State) ShippedItem {
	promoting, _ := latestPromotion(runs)
	item := ShippedItem{
		WorkItemID:   workItemID,
		ShippedAt:    shippedAt(promoting),
		ShippedRunID: promoting.RunID,
		// The price sorts the runs oldest first as it prices them, which is the
		// order the title is read in below; the claim scans for the earliest run
		// itself and does not lean on it.
		Price: s.price(workItemID, runs),
	}
	// The earliest run is the first claim. Its recorded claim moment is exact;
	// its start is what stands in for a run written before claims were recorded,
	// and is at most the claim's own duration earlier.
	first := runs[0]
	for _, run := range runs[1:] {
		if run.StartedAt.Before(first.StartedAt) {
			first = run
		}
	}
	item.ClaimedAt = first.StartedAt
	if first.WorkItemClaimedAt != nil {
		item.ClaimedAt = *first.WorkItemClaimedAt
	}
	// The title is the newest recorded one, so an item renamed between attempts
	// is named as it was when it shipped.
	for index := len(item.Price.Runs) - 1; index >= 0; index-- {
		if title := recordedTitle(runs, item.Price.Runs[index].RunID); title != "" {
			item.Title = title
			break
		}
	}
	if promoting.CompletedAt == nil {
		item.ElapsedUnknown = "the promoting run has not recorded completing, so when it shipped cannot be read"
		return item
	}
	item.ElapsedSeconds = int64(promoting.CompletedAt.Sub(item.ClaimedAt) / time.Second)
	return item
}

// recordedTitle is what one run recorded the item being called, by run id.
func recordedTitle(runs []State, runID string) string {
	for _, run := range runs {
		if run.RunID == runID {
			return run.WorkItemTitle
		}
	}
	return ""
}
