package testharness

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// MemoryDocket is the durable docket without the disk: it enforces the two
// properties the store guarantees, which are that a key is recorded once and
// that a decision recorded against a key is joined onto its entry.
type MemoryDocket struct {
	Entries []triage.Entry
	Closed  map[string]triage.Closure
	FailOn  string
}

// CloseKey settles one entry the way the store does: the entry stays on the log
// and the decision is recorded beside it, at a moment the caller names because
// what a build does about a settled key turns on when it was settled.
func (d *MemoryDocket) CloseKey(key, decision string, at time.Time) {
	if d.Closed == nil {
		d.Closed = make(map[string]triage.Closure)
	}
	d.Closed[key] = triage.Closure{
		SchemaVersion: triage.ClosureSchemaVersion,
		Key:           key,
		ProductID:     "yoyodyne",
		RunID:         DocketedRunID,
		WorkItemID:    DocketedItem,
		Decision:      decision,
		DecidedBy:     "the development manager in conversation chat-0123456789abcdef",
		ClosedAt:      at,
	}
}

// WaitOn settles one entry with a decision that holds only until the moment it
// names, which is what waiting on a forge is.
func (d *MemoryDocket) WaitOn(key string, at, revisitAfter time.Time) {
	d.CloseKey(key, "wait", at)
	settled := d.Closed[key]
	settled.RevisitAfter = revisitAfter
	d.Closed[key] = settled
}

// Close is the store's closure, reduced to what these tests read of it: the
// latest decision about a key is what stands, and one recorded while an earlier
// one still holds changes nothing.
func (d *MemoryDocket) Close(closure triage.Closure) (bool, error) {
	if err := closure.Validate(); err != nil {
		return false, err
	}
	if d.Closed == nil {
		d.Closed = make(map[string]triage.Closure)
	}
	if standing, decided := d.Closed[closure.Key]; decided && standing.Holds(closure.ClosedAt) {
		return false, nil
	}
	for _, entry := range d.Entries {
		if entry.Key == closure.Key {
			d.Closed[closure.Key] = closure
			return true, nil
		}
	}
	return false, fmt.Errorf("no docket entry keyed %s is recorded", closure.Key)
}

func (d *MemoryDocket) RecordOnce(entry triage.Entry) (bool, error) {
	if d.FailOn != "" && entry.Class == triage.Class(d.FailOn) {
		return false, errors.New("the docket is unwritable")
	}
	if err := entry.Validate(); err != nil {
		return false, err
	}
	for index, existing := range d.Entries {
		if existing.Key != entry.Key {
			continue
		}
		// A key whose entry a decision settled carries the stoppage that happened
		// after it, exactly as the durable store does: the same run can stop twice,
		// and the second time is not the first arriving again.
		closure, decided := d.Closed[entry.Key]
		if !decided || !entry.RecordedAt.After(closure.ClosedAt) {
			return false, nil
		}
		d.Entries[index] = entry
		return true, nil
	}
	d.Entries = append(d.Entries, entry)
	return true, nil
}

// List hands back a copy, as the durable store does by decoding the log afresh:
// what a reader joins onto the entries it was given must not travel back into
// the log, which is written once and never revised.
func (d *MemoryDocket) List() ([]triage.Entry, error) {
	listed := slices.Clone(d.Entries)
	for index := range listed {
		closure, found := d.Closed[listed[index].Key]
		// A decision settles the stoppage it was made about, and not one docketed
		// after it.
		if !found || listed[index].RecordedAt.After(closure.ClosedAt) {
			continue
		}
		settled := closure
		listed[index].Closed = &settled
	}
	return listed, nil
}

func (d *MemoryDocket) Keys() []string {
	keys := make([]string, 0, len(d.Entries))
	for _, entry := range d.Entries {
		keys = append(keys, entry.Key)
	}
	return keys
}

const (
	DocketedRunID = "run-0123456789abcdef0123456789abcdef"
	DocketedItem  = "yoyodyne-task"
	DocketedTitle = "Slack thread headers carry the item's title"
)

// DocketedTriage is the configuration a docket is built against in these tests,
// and DocketedCaps the ceilings assembled from it — the same ones the guards are
// refused against, which is the whole point of the docket reporting them.
var (
	DocketedTriage = config.Triage{
		StuckMergeAge:       config.Duration(2 * time.Hour),
		ReviewRoundsCap:     4,
		RepairGrantAttempts: 2,
	}
	DocketedCaps = orchestrator.TriageCaps(config.Execution{IntegrationRetriesBeforeReconciliation: 1}, DocketedTriage)
)

// DocketerOverStore is a docket over a real run store, reading the same two
// durable records the triage guards spend: the item's counters and the re-runs
// claimed against its stoppages.
func DocketerOverStore(docket orchestrator.Docket, store *runstate.Store, cfg config.Config) *orchestrator.Docketer {
	return &orchestrator.Docketer{
		Docket:    docket,
		Runs:      store,
		Decisions: store.Triage(),
		Reruns:    store.Reruns(),
		Stops:     store,
		Caps:      orchestrator.TriageCaps(cfg.Execution, cfg.Triage),
		Triage:    cfg.Triage,
	}
}
