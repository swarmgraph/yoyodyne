package runstate

// A run record has one writer at a time, and no change to it is lost: guarantee
// 1 of docs/designs/one-owner-for-each-guarantee.md, as the architect ruled it
// for the run record (yoyodyne-ifd.428.116).
//
// Two ways in, and nothing else:
//
//   - A narrow update (Store.Update) changes only its own fields, on the record
//     as it is stored at that moment, under the run's write lock. It is how a
//     writer that does not hold the run — the merge queue, a transfer,
//     reconciliation's cleanup — changes a run, so a field some other writer
//     changed in between is kept rather than overwritten. Every one is declared
//     here, by name, because only this package declares a RunUpdate.
//   - A whole save (Store.Save) carries the revision its copy was read at. Each
//     narrow update moves the stored revision on, so a save from a copy read
//     before one is refused with StaleRunError naming the run and the fields
//     that differ, and nothing is written. The writer reads the run again and
//     makes its change again.
//
// A whole save does not move the revision: it is the run's own holder writing
// its own copy, and moving the revision there would refuse that holder's next
// save of the very copy it just wrote. Routing keeps a generation of its own
// that Save already compares (checkRoutingWrite), so a routing change does not
// move the revision either. The writers outside this package that still save a
// whole record are listed in runrecordwriters_audit_test.go, and the list only
// shrinks.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// RunUpdate is one named change to a run record. apply makes the change to the
// record as stored and reports whether it changed anything; an error refuses
// the change and nothing is written. It replaces whatever it changes behind a
// pointer rather than writing through it, so the copy it was handed is left as
// it was.
type RunUpdate interface {
	apply(state *State) (bool, error)
}

// Update applies one narrow change to the run as it is stored, under the run's
// write lock, and returns the run as written. A change that changes nothing
// writes nothing and returns the stored run, so making it again after a lost
// reply is safe.
func (s *Store) Update(ctx context.Context, runID string, update RunUpdate) (State, error) {
	unlock, err := s.lockRunWrites(ctx, runID)
	if err != nil {
		return State{}, err
	}
	defer unlock()
	stored, err := s.Load(runID)
	if err != nil {
		return State{}, err
	}
	next, changed, err := ApplyUpdate(stored, update)
	if err != nil || !changed {
		return stored, err
	}
	if !stored.Status.InFlight() && next.HoldsDeveloperSlot() {
		return State{}, RefusedStateError{Problem: fmt.Errorf("%w: run %s is recorded %s and this update would make it %s", ErrMadeLiveWithoutASlot, runID, stored.Status, next.Status)}
	}
	next.Worker = s.workerToWrite(runID, stored.Worker)
	if next, err = next.withRecordedTextsBounded(); err != nil {
		return State{}, err
	}
	if err := s.validateState(next); err != nil {
		return State{}, err
	}
	if err := s.writeState(next); err != nil {
		return State{}, err
	}
	return next, nil
}

// ApplyUpdate is what Update does to the stored record, made on a copy held in
// memory: the change, and the revision moved on where it changed anything. It
// writes nothing; it is for a stand-in for the store, which has no file to lock.
func ApplyUpdate(state State, update RunUpdate) (State, bool, error) {
	next := state
	changed, err := update.apply(&next)
	if err != nil || !changed {
		return state, false, err
	}
	next.Revision = state.Revision + 1
	return next, true, nil
}

// StaleRunError is a whole save made from a copy of the run read before a
// narrow update changed it. Nothing is written; the writer reads the run again
// and makes its change again. Fields names what the copy would have
// overwritten.
type StaleRunError struct {
	RunID  string
	Held   uint64
	Stored uint64
	Fields []string
}

func (e StaleRunError) Error() string {
	differs := "nothing this build can name"
	if len(e.Fields) > 0 {
		differs = strings.Join(e.Fields, ", ")
	}
	return fmt.Sprintf("run %s changed since this copy was read (it is at revision %d and the copy at %d), so saving the copy would overwrite %s; read the run again and make the change again", e.RunID, e.Stored, e.Held, differs)
}

// checkRevision refuses a whole save of a copy read at a revision other than
// the stored one. It is wrapped as a RefusedStateError because no later save of
// the same copy closes the gap: a run that gets it back ends over the record
// that is actually stored (orchestrator's recordEndingAfterRefusedSave).
func (s *Store) checkRevision(state State, stored uint64) error {
	if state.Revision == stored {
		return nil
	}
	stale := StaleRunError{RunID: state.RunID, Held: state.Revision, Stored: stored}
	if current, err := s.Read(state.RunID); err == nil {
		stale.Fields = differingFields(state, current)
	}
	return RefusedStateError{Problem: stale}
}

// differingFields names the record's fields, as they are written, whose values
// differ between two copies of one run. What every write changes is left out.
func differingFields(held, stored State) []string {
	encode := func(state State) map[string]json.RawMessage {
		fields := map[string]json.RawMessage{}
		if encoded, err := json.Marshal(state); err == nil {
			_ = json.Unmarshal(encoded, &fields)
		}
		return fields
	}
	a, b := encode(held), encode(stored)
	var names []string
	for name := range a {
		if _, also := b[name]; !also {
			b[name] = nil
		}
	}
	for name, value := range b {
		switch name {
		case "revision", "updated_at", "worker":
			continue
		}
		if string(a[name]) != string(value) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// PullRequestSuperseded records that the run's pull request was closed because
// its change landed by Vehicle, as the merge queue's retirement of it does. It
// changes nothing where the run's request is no longer Number, has merged, or
// already names what superseded it.
type PullRequestSuperseded struct {
	Number  int
	Vehicle string
	State   string
	At      time.Time
}

func (u PullRequestSuperseded) apply(state *State) (bool, error) {
	published := state.PullRequest
	if published == nil || published.Number != u.Number || published.Merged || strings.TrimSpace(published.Superseded) != "" {
		return false, nil
	}
	superseded := *published
	superseded.Superseded = u.Vehicle
	superseded.State = u.State
	state.PullRequest = &superseded
	state.UpdatedAt = u.At
	return true, nil
}

// MergeQueueAdmitted records the merge queue entry that holds the run's
// change. Readmission is a transfer naming the entry the change moved to, which
// is recorded only on a run that already records an admission.
type MergeQueueAdmitted struct {
	Record      MergeQueueAdmissionRecord
	Readmission bool
	At          time.Time
}

func (u MergeQueueAdmitted) apply(state *State) (bool, error) {
	if u.Readmission && state.MergeQueue == nil {
		return false, nil
	}
	if state.MergeQueue != nil && *state.MergeQueue == u.Record {
		return false, nil
	}
	record := u.Record
	state.MergeQueue = &record
	state.UpdatedAt = u.At
	return true, nil
}

// QueuedRunCompleted ends a run whose change the merge queue admitted: it
// succeeds, owing the cleanup its landing will earn, and every pause it was
// carrying is over. It is refused on a run that records no admission, or that
// has already ended some other way.
type QueuedRunCompleted struct {
	At time.Time
}

func (u QueuedRunCompleted) apply(state *State) (bool, error) {
	if state.MergeQueue == nil {
		return false, fmt.Errorf("run %s records no merge queue admission, so it does not end as queued", state.RunID)
	}
	if state.Status == StatusSucceeded && state.Phase == PhaseCleaningUp {
		return false, nil
	}
	if state.Status.Terminal() {
		return false, fmt.Errorf("run %s has already ended %s, so it does not end again as queued", state.RunID, state.Status)
	}
	state.ProviderStop = ""
	state.RedeployStop = nil
	state.OperatorHeldSince = nil
	state.Status = StatusSucceeded
	// Cleaning up is what is owed once the queue lands the change and records
	// the landing as the run's integration; until then the run owes nothing,
	// and its branch and worktree are what a handback for repair continues.
	state.Phase = PhaseCleaningUp
	state.UpdatedAt = u.At
	completed := u.At
	state.CompletedAt = &completed
	return true, nil
}

// RetryRecorded records one waited-out failure at a boundary, which the run's
// own process writes before it waits. It is the narrow form for a process
// whose copy may have gone out of date while another writer recorded on the
// run: an admitted change waiting on the tracker, while the merge queue lands
// it and closes its pull request.
type RetryRecorded struct {
	Retry Retry
}

func (u RetryRecorded) apply(state *State) (bool, error) {
	retries := make([]Retry, 0, len(state.Retries)+1)
	state.Retries = append(append(retries, state.Retries...), u.Retry)
	state.UpdatedAt = u.Retry.At
	return true, nil
}

// InstallRecorded adds one run of one of the project's declared install steps
// to the run's record of them (InstallRun).
type InstallRecorded struct {
	Install InstallRun
	At      time.Time
}

func (u InstallRecorded) apply(state *State) (bool, error) {
	installs := make([]InstallRun, 0, len(state.Install)+1)
	state.Install = append(append(installs, state.Install...), u.Install)
	state.UpdatedAt = u.At
	return true, nil
}

// InstallWaitRecorded puts a wait to try an install step again on the run, or
// takes it off where Wait is nil (InstallWait).
type InstallWaitRecorded struct {
	Wait *InstallWait
	At   time.Time
}

func (u InstallWaitRecorded) apply(state *State) (bool, error) {
	if u.Wait == nil && state.InstallWait == nil {
		return false, nil
	}
	if u.Wait != nil {
		wait := *u.Wait
		state.InstallWait = &wait
	} else {
		state.InstallWait = nil
	}
	state.UpdatedAt = u.At
	return true, nil
}

// IntegrationRecorded records the run's integration: the merge queue's landing
// of its change, which the queue records as a run's own promotion would be. It
// changes nothing where the run already records the same landing.
type IntegrationRecorded struct {
	Integration Integration
	At          time.Time
}

func (u IntegrationRecorded) apply(state *State) (bool, error) {
	if state.Integration != nil && *state.Integration == u.Integration {
		return false, nil
	}
	integration := u.Integration
	state.Integration = &integration
	state.UpdatedAt = u.At
	return true, nil
}

// CleanupRecorded records how far cleaning up an integrated run got: which of
// its artifacts are gone, and Failure where some are left, which keeps the run
// owing the rest. With nothing left it completes the run. It is refused on a
// run that is not cleaning up, because something moved it on while its
// artifacts were being removed.
type CleanupRecorded struct {
	WorktreeRemoved bool
	BranchRemoved   bool
	Failure         string
	At              time.Time
}

func (u CleanupRecorded) apply(state *State) (bool, error) {
	if state.Phase != PhaseCleaningUp {
		if state.Phase == PhaseComplete && u.Failure == "" {
			return false, nil
		}
		return false, fmt.Errorf("run %s is %s rather than cleaning up, so its cleanup is not recorded on it", state.RunID, nonEmptyPhase(state.Phase))
	}
	state.WorktreeRemoved = u.WorktreeRemoved
	state.BranchRemoved = u.BranchRemoved
	state.UpdatedAt = u.At
	if u.Failure != "" {
		state.CleanupFailure = u.Failure
		return true, nil
	}
	// Nothing is left to remove, so the run is complete and an outstanding
	// cleanup marker is no longer true.
	state.CleanupFailure = ""
	if state.StopClass == StopCleanup {
		state.StopClass = ""
	}
	state.Phase = PhaseComplete
	return true, nil
}

func nonEmptyPhase(phase Phase) string {
	if phase == "" {
		return "at no recorded step"
	}
	return "at " + string(phase)
}
