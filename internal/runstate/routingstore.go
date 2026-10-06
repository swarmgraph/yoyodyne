package runstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// UpdateRouting applies one routing change to the run as it is stored, under
// the run's write lock, and returns the run as written. It is the only way a
// run's routing changes; see routing.go.
//
// change is applied to a copy of the stored routing (an empty record for a run
// that has none) and reports whether it changed anything. A change that
// changes nothing is a repeated request and returns the stored run whatever
// copy the caller held, so retrying after a restart or a lost reply is safe. A
// change that does change something is written only if the caller's copy holds
// the stored routing exactly: a copy read before another routing change is
// refused with StaleRoutingError and nothing is written.
//
// What is written is the stored run with the new routing, not the caller's
// copy, so no field the caller did not mean to change is touched — the repair
// and review counters among them. A caller holding unsaved changes to other
// fields saves them first.
func (s *Store) UpdateRouting(ctx context.Context, held State, change func(*RunRouting) (bool, error)) (State, error) {
	unlock, err := s.lockRunWrites(ctx, held.RunID)
	if err != nil {
		return State{}, err
	}
	defer unlock()
	stored, err := s.Load(held.RunID)
	if err != nil {
		return State{}, err
	}
	routing := stored.Routing.clone()
	changed, err := change(routing)
	if err != nil {
		return State{}, err
	}
	if !changed {
		return stored, nil
	}
	if !equalRouting(held.Routing, stored.Routing) {
		return State{}, StaleRoutingError{RunID: held.RunID, Held: held.Routing.generation(), Stored: stored.Routing.generation()}
	}
	routing.Version = RoutingVersion
	routing.Generation = stored.Routing.generation() + 1
	next := stored
	next.Routing = routing
	if err := s.validateState(next); err != nil {
		return State{}, err
	}
	if err := s.writeState(next); err != nil {
		return State{}, err
	}
	return next, nil
}

// SlotOccupiedError is a developer slot another run in flight already holds.
type SlotOccupiedError struct {
	Slot  int
	RunID string
}

func (e SlotOccupiedError) Error() string {
	return fmt.Sprintf("developer slot %d is occupied by run %s", e.Slot, e.RunID)
}

// ClaimSlot records the developer slot a run occupies, under the reservation
// lock so that two runs cannot record one slot. The slot has to lie within the
// configured capacity and be recorded by no other run in flight. The record is
// routing identity only: whether the run holds execution capacity is still
// what Reserve and ReclaimSlot count.
func (s *Store) ClaimSlot(ctx context.Context, held State, number, capacity int, at time.Time) (State, error) {
	if number < 1 || number > capacity {
		return State{}, fmt.Errorf("developer slot %d is outside the configured capacity of %d", number, capacity)
	}
	release, err := s.lockReservations(ctx)
	if err != nil {
		return State{}, err
	}
	defer release()
	active, err := s.Incomplete()
	if err != nil {
		return State{}, fmt.Errorf("discover incomplete runs while claiming a developer slot: %w", err)
	}
	for _, existing := range active {
		if existing.RunID != held.RunID && existing.RecordedSlot() == number {
			return State{}, SlotOccupiedError{Slot: number, RunID: existing.RunID}
		}
	}
	return s.UpdateRouting(ctx, held, func(routing *RunRouting) (bool, error) {
		return routing.ClaimSlot(number, RoutingClaimed, at)
	})
}

// MigrateRouting establishes routing for a run recorded before routing
// existed, at a point where nothing of the run can still be executing; the
// caller establishes that before calling. A slot the record already names is
// kept. Otherwise the lowest slot within capacity that no run in flight records
// is allocated now, under the reservation lock, and marked as migrated; when
// none is free the run waits with a CapacityError rather than becoming a worker
// beyond capacity. snapshotFor resolves the current validated pair for the
// slot. operation, where the run has an unfinished operation, is recorded with
// unknown history and no switch allowance.
func (s *Store) MigrateRouting(ctx context.Context, held State, capacity int, snapshotFor func(slot int) (RoutingSnapshot, error), operation *OperationRequest, at time.Time) (State, error) {
	if capacity < 1 {
		return State{}, errors.New("max concurrent developers must be greater than zero")
	}
	release, err := s.lockReservations(ctx)
	if err != nil {
		return State{}, err
	}
	defer release()
	active, err := s.Incomplete()
	if err != nil {
		return State{}, fmt.Errorf("discover incomplete runs while migrating routing: %w", err)
	}
	slot := held.RecordedSlot()
	if slot == 0 {
		taken := map[int]bool{}
		for _, existing := range active {
			if existing.RunID != held.RunID {
				taken[existing.RecordedSlot()] = true
			}
		}
		for number := 1; number <= capacity; number++ {
			if !taken[number] {
				slot = number
				break
			}
		}
		if slot == 0 {
			return State{}, CapacityError{Limit: capacity, Active: len(taken)}
		}
	}
	snapshot, err := snapshotFor(slot)
	if err != nil {
		return State{}, err
	}
	return s.UpdateRouting(ctx, held, func(routing *RunRouting) (bool, error) {
		return routing.migrate(slot, Migration{Snapshot: snapshot, Operation: operation, At: at})
	})
}

// CapacityReductionProblem refuses a developer capacity that would leave a run
// in flight recording a slot beyond it. Lowering capacity under such a run
// would otherwise have to renumber it or let it overflow silently; the change
// waits until that run has finished instead.
func CapacityReductionProblem(inFlight []State, capacity int) error {
	var beyond []string
	for _, state := range inFlight {
		if slot := state.RecordedSlot(); slot > capacity && state.Status.InFlight() {
			beyond = append(beyond, fmt.Sprintf("run %s occupies developer slot %d", state.RunID, slot))
		}
	}
	if len(beyond) == 0 {
		return nil
	}
	sort.Strings(beyond)
	return fmt.Errorf("developer capacity %d is below a slot in use: %s", capacity, strings.Join(beyond, "; "))
}

// lockRunWrites serializes every write of one run record within and across
// processes. It is held only for the read-compare-write of one record, never
// while anything else is locked after it, so it cannot deadlock against the
// reservation lock taken before it.
func (s *Store) lockRunWrites(ctx context.Context, runID string) (func(), error) {
	if !runIDPattern.MatchString(runID) {
		return nil, errors.New("run id is invalid")
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create run state directory: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(s.root, runID+".write.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open run write lock: %w", err)
	}
	if err := lockStateFile(ctx, lock); err != nil {
		lock.Close()
		return nil, fmt.Errorf("lock run %s for writing: %w", runID, err)
	}
	return func() { _ = releaseStateFile(lock) }, nil
}

// storedRouting reads only what a whole-record write has to compare: the
// stored record's schema version and routing. It reads past every other field,
// so a write can still replace a record it could not otherwise decode, as it
// always could; what it refuses is a record whose routing or schema this build
// does not understand.
func (s *Store) storedRouting(runID string) (*RunRouting, error) {
	path, err := s.statePath(runID)
	if err != nil {
		return nil, err
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load existing run state before save: %w", err)
	}
	var stored struct {
		SchemaVersion int         `json:"schema_version"`
		Routing       *RunRouting `json:"routing"`
	}
	if err := json.Unmarshal(encoded, &stored); err != nil {
		return nil, RefusedStateError{Problem: fmt.Errorf("the stored run %s cannot be read to compare its routing: %w", runID, err)}
	}
	if stored.SchemaVersion > StateSchemaVersion {
		return nil, RefusedStateError{Problem: fmt.Errorf("the stored run %s has schema version %d, newer than this build's %d", runID, stored.SchemaVersion, StateSchemaVersion)}
	}
	if stored.Routing != nil && stored.Routing.Version > RoutingVersion {
		return nil, RefusedStateError{Problem: fmt.Errorf("%w: run %s has routing version %d and this build reads %d", ErrUnsupportedRouting, runID, stored.Routing.Version, RoutingVersion)}
	}
	return stored.Routing, nil
}

// checkRoutingWrite refuses a whole-record write whose routing is not what is
// stored: a stale copy, or a caller that changed routing other than through
// UpdateRouting.
func (s *Store) checkRoutingWrite(state State) error {
	stored, err := s.storedRouting(state.RunID)
	if err != nil {
		return err
	}
	if equalRouting(stored, state.Routing) {
		return nil
	}
	if stored.generation() != state.Routing.generation() || (stored == nil) != (state.Routing == nil) {
		return StaleRoutingError{RunID: state.RunID, Held: state.Routing.generation(), Stored: stored.generation()}
	}
	return RefusedStateError{Problem: fmt.Errorf("%w: run %s routing changes only through UpdateRouting", ErrRoutingConflict, state.RunID)}
}
