package runstate

// Freeing the developer slot of a run nothing is working on any more.
//
// A run's record says it is in flight from its reservation until something
// writes its ending, and a worker that dies writes nothing. Such a run went on
// counting against the developer slots, by number (ClaimSlot) and in the count
// (developerSlotsFull), with no process behind it and nothing in the harness
// able to say so or let the slot go, so every slot could read taken while
// nothing ran.
//
// ReleaseSlotsWithNoLiveWorker is the harness letting such a slot go, and
// SlotRelease is what it writes on the run when it does: which slot, which
// provider attempts it ended, and why, in words a person reads. A released run
// holds no developer slot until a new worker attempt takes it up again (a
// repair or a continuation adopting it), which is what HoldsDeveloperSlotNow
// reads off the record.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// UncertainWorkerQuiet is how long a run whose worker can be shown neither
// alive nor gone has to have gone unwritten, with no process holding its lease,
// before its slot is let go. It is the bound the status and the health check
// already use to call a run's claim dead (readmodel.DefaultDeadClaimThreshold),
// so a run is released exactly when those surfaces have been saying for that
// long that nothing is behind it.
const UncertainWorkerQuiet = 30 * time.Minute

// UnheldRunQuiet is how long a run whose worker's program is still running,
// with nothing holding the run's lease, has to have gone unwritten before its
// slot is let go. The lease is what says whether anything works on a run: the
// pipeline holds it for the whole of a run it is executing, so a live program
// holding no lease is either between one step and the next — a continuation
// that has given the lease up and not yet been adopted by the pipeline — or
// done with the run without writing its ending, as a continuation refused after
// it made the run live is. The second lasts until somebody restarts the
// program, which is what held every developer slot while ready work waited.
// The bound only has to outlast the first, whose length is a few tracker
// reads: one is allowed beads.DefaultTimeout, two minutes, before it is
// stopped, so ten minutes outlasts several reads that each run to that limit
// (TestUnheldRunQuietOutlastsSlowTrackerReads). A run whose last write is a
// refusal does not wait for it (refusedLast).
//
// A release that does land inside such a gap costs nothing it should not: the
// pipeline takes the run up as a new worker attempt, which holds a slot again
// under the reservation's own count (TestAReleasedRunTakenUpAgainHoldsASlot).
const UnheldRunQuiet = 10 * time.Minute

// refusalWriteSlack is how much later than a refusal the run may last have
// been written for the refusal still to count as the last thing that happened
// to it: the save that carries the refusal, and the event that says so, land a
// moment after the refusal is stamped.
const refusalWriteSlack = time.Minute

// SlotRelease is the harness letting go of the developer slot of a run with no
// live worker behind it.
type SlotRelease struct {
	At time.Time `json:"at"`
	// Slot is the slot number the run recorded, and zero where it recorded none;
	// a run with no number still counted against the slots.
	Slot int `json:"slot,omitempty"`
	// WorkerAttempt is the worker attempt the release was made against. A later
	// attempt — the run taken up again by a repair or a continuation — holds a
	// slot again, because that worker is alive.
	WorkerAttempt int `json:"worker_attempt"`
	// EndedAttempts is the provider attempts this release ended as interrupted.
	EndedAttempts []string `json:"ended_attempts,omitempty"`
	// OpenAttempts is the provider attempts that could not be shown stopped and
	// were left as they are, with why.
	OpenAttempts []string `json:"open_attempts,omitempty"`
	// Refusal is the cause of the refusal the slot was given back for, where the
	// last thing written on the run was the harness refusing to go on with it —
	// the main checkout not being the harness's to work from, for one — and
	// empty where the slot was given back only because nothing works on the run.
	Refusal EnvironmentalCause `json:"refusal,omitempty"`
	// Says is what was released and why, in a sentence a person reads.
	Says string `json:"says"`
}

// releasesWorker reports a release made against the worker the record names
// now, which is what keeps the run from counting against the slots.
func (r *SlotRelease) releasesWorker(worker *WorkerIdentity) bool {
	if r == nil {
		return false
	}
	attempt := 0
	if worker != nil {
		attempt = worker.Attempt
	}
	return r.WorkerAttempt == attempt
}

// SlotReleased reports a run whose developer slot the harness released for the
// worker its record names now, so it holds no slot until a new worker attempt
// takes it up.
func (s State) SlotReleased() bool {
	return s.SlotRelease.releasesWorker(s.Worker)
}

// ReleasedSlot is one run whose developer slot ReleaseSlotsWithNoLiveWorker let
// go.
type ReleasedSlot struct {
	RunID      string
	WorkItemID string
	Release    SlotRelease
}

// ReleaseSlotsWithNoLiveWorker lets go of the developer slot of every run in
// flight that counts against the slots with no live worker behind it, and
// returns what it released. A run is released when its worker's process is
// shown to be gone (WorkerReading.ProcessGone); when that process is still
// running the program that took the run but nothing holds the run's lease, and
// nothing has written the run for UnheldRunQuiet; or when its worker can be
// shown neither alive nor gone and nothing has held or written the run for
// UncertainWorkerQuiet (Presence). A run whose worker holds its lease, or may,
// is left alone, and so is one its session stopped for a redeploy, whose slot
// is kept for the session that comes back for it.
//
// Each release is made under the run's lease, so a run some process holds is
// never touched: a lease that cannot be taken is a live holder, and the run is
// left as it is. Under the lease, every provider attempt that may still be
// executing is settled the way a resumed run settles it (ReconcileLaunch): one
// whose process is shown stopped is ended as interrupted, and one that cannot
// be shown stopped is left open and named. The release is then written on the
// run (State.SlotRelease), and from that moment the run holds no slot, by
// number or in the count. A run already released for its current worker is not
// released again.
//
// What could not be read or written for one run is returned joined, and does
// not stop the rest.
func (s *Store) ReleaseSlotsWithNoLiveWorker(ctx context.Context, now time.Time) ([]ReleasedSlot, error) {
	active, err := s.Incomplete()
	if err != nil {
		return nil, fmt.Errorf("discover incomplete runs while freeing developer slots with no live worker: %w", err)
	}
	var released []ReleasedSlot
	var problems []error
	for _, state := range active {
		if why, ok := s.noLiveWorker(state, now); !ok || why.says == "" {
			continue
		}
		release, done, err := s.releaseSlot(ctx, state.RunID, now)
		if err != nil {
			problems = append(problems, fmt.Errorf("free the developer slot of run %s: %w", state.RunID, err))
			continue
		}
		if done {
			released = append(released, ReleasedSlot{RunID: state.RunID, WorkItemID: state.WorkItemID, Release: release})
		}
	}
	return released, errors.Join(problems...)
}

// releaseWhy is why a run's slot is given back: in words, and the refusal it
// is given back for where there is one.
type releaseWhy struct {
	says    string
	refusal *EnvironmentalRefusal
}

// noLiveWorker says why a run counts against the developer slots with no live
// worker behind it, and reports false for every other run.
//
// A run nothing holds whose last write is a refusal (refusedLast) is given
// back at once and for that refusal, whether or not the program that took it is
// still running: the refusal is the run's own record that nothing went on with
// it, so there is no step to wait out.
func (s *Store) noLiveWorker(state State, now time.Time) (releaseWhy, bool) {
	if !state.HoldsDeveloperSlot() || state.RedeployStop != nil || state.SlotRelease.releasesWorker(state.Worker) {
		return releaseWhy{}, false
	}
	reading := s.Worker(state, now)
	if reading.Liveness == WorkerAbsent {
		if refusal, ok := s.refusedLast(state); ok {
			return releaseWhy{says: refusalSays(*refusal), refusal: refusal}, true
		}
	}
	if reading.ProcessGone() {
		return releaseWhy{says: reading.Says}, true
	}
	if reading.Liveness == WorkerAbsent {
		// The program that took the run is still running and nothing holds the
		// run's lease (UnheldRunQuiet): the lease decides, not the program.
		presence, err := s.presence(state, UnheldRunQuiet, now, false)
		if err != nil || presence.Found {
			return releaseWhy{}, false
		}
		return releaseWhy{says: fmt.Sprintf("%s, and nothing has been written to the run since %s", reading.Says, presence.LastMoved.Local().Format(personTime))}, true
	}
	if reading.Liveness != WorkerUncertain {
		return releaseWhy{}, false
	}
	presence, err := s.presence(state, UncertainWorkerQuiet, now, false)
	if err != nil || presence.Found {
		return releaseWhy{}, false
	}
	return releaseWhy{says: fmt.Sprintf("whether its worker is alive cannot be shown (%s), and %s", reading.Says, presence.Says)}, true
}

// LastWriteRefusal reports the refusal on a run in flight where it is the
// last thing written on the run's record: the harness turned the run away from
// outside the work — the main checkout carrying files the harness does not own
// is the common one — and settled it as refused, and nothing has written the
// record since. A continuation refused that way after it made its run live
// leaves exactly this, still counted against the slots. A refusal some later
// write has overtaken is an earlier round's, and is not read as this one's.
// The vanished-process account is never one: it is the reconciling sweep's,
// written as the run is ended.
func (s State) LastWriteRefusal() (*EnvironmentalRefusal, bool) {
	refusal := s.Environmental
	if !s.Status.InFlight() || refusal == nil || !refusal.Refused || !refusal.Settled || !refusal.Cause.Valid() ||
		refusal.Cause == CauseProcessVanished || refusal.RecordedAt.IsZero() {
		return nil, false
	}
	if s.UpdatedAt.After(refusal.RecordedAt.Add(refusalWriteSlack)) {
		return nil, false
	}
	return refusal, true
}

// refusedLast is LastWriteRefusal with the run's event log asked as well, so
// an event written after the refusal overtakes it too.
func (s *Store) refusedLast(state State) (*EnvironmentalRefusal, bool) {
	refusal, ok := state.LastWriteRefusal()
	if !ok {
		return nil, false
	}
	moved, err := s.lastMoved(state)
	if err != nil || moved.After(refusal.RecordedAt.Add(refusalWriteSlack)) {
		return nil, false
	}
	return refusal, true
}

// refusalSays is the refusal a slot is given back for, in words.
func refusalSays(refusal EnvironmentalRefusal) string {
	says := fmt.Sprintf("the harness refused to go on with the run at %s (%s)", refusal.RecordedAt.Local().Format(personTime), refusal.Cause.Title())
	if detail := strings.TrimSpace(refusal.Detail); detail != "" {
		says += ": " + strings.TrimSuffix(detail, ".")
	}
	return says + ", and nothing has held the run's lease or written to it since"
}

// releaseSlot is one release, under the run's lease.
func (s *Store) releaseSlot(ctx context.Context, runID string, now time.Time) (SlotRelease, bool, error) {
	lease, held, err := s.takeReleaseLease(ctx, runID)
	if err != nil || !held {
		return SlotRelease{}, false, err
	}
	defer lease.Release()
	// The run is read again under the lease, because only now is this process
	// the one entitled to act on what it reads.
	state, err := s.Load(runID)
	if err != nil {
		return SlotRelease{}, false, err
	}
	why, ok := s.noLiveWorker(state, now)
	if !ok {
		return SlotRelease{}, false, nil
	}
	release := SlotRelease{At: now.UTC(), Slot: state.RecordedSlot()}
	if why.refusal != nil {
		release.Refusal = why.refusal.Cause
	}
	if state.Worker != nil {
		release.WorkerAttempt = state.Worker.Attempt
	}
	if state.Routing != nil {
		for _, operation := range state.Routing.Operations {
			if operation.Completed != nil {
				continue
			}
			if _, executing := operation.executingAttempt(); !executing {
				continue
			}
			found, err := s.ReconcileLaunch(ctx, runID, operation.ID, LaunchRecovery{})
			if err != nil {
				return SlotRelease{}, false, err
			}
			switch found.Verdict {
			case LaunchRunning, LaunchUncertain:
				release.OpenAttempts = append(release.OpenAttempts, fmt.Sprintf("%s (%s)", found.Attempt, found.Reason))
			case LaunchInterrupted, LaunchFinished:
				release.EndedAttempts = append(release.EndedAttempts, found.Attempt)
			}
		}
		// What ReconcileLaunch wrote is the run's routing now, and the release
		// is saved over it.
		if state, err = s.Load(runID); err != nil {
			return SlotRelease{}, false, err
		}
	}
	sort.Strings(release.EndedAttempts)
	sort.Strings(release.OpenAttempts)
	release.Says = release.sentence(why.says)
	state.SlotRelease = &release
	if err := s.Save(state); err != nil {
		return SlotRelease{}, false, err
	}
	return release, true, nil
}

// takeReleaseLease takes the run's lease the way AdoptRun does, under the
// reservation lock, without becoming its worker: nothing is stamped beside the
// lease, so the record keeps the worker it names and every write below keeps it
// too (workerToWrite).
func (s *Store) takeReleaseLease(ctx context.Context, runID string) (*Lease, bool, error) {
	release, err := s.lockReservations(ctx)
	if err != nil {
		return nil, false, err
	}
	defer release()
	return s.takeLease(ctx, runID)
}

// sentence is the release said whole.
func (r SlotRelease) sentence(why string) string {
	slot := "the developer slot it counted against"
	if r.Slot != 0 {
		slot = fmt.Sprintf("developer slot %d", r.Slot)
	}
	says := fmt.Sprintf("The harness freed %s at %s because no live worker is behind the run: %s.", slot, r.At.Local().Format(personTime), why)
	if r.Refusal != "" {
		says = fmt.Sprintf("The harness freed %s at %s because the run was refused, not because its process vanished: %s.", slot, r.At.Local().Format(personTime), why)
	}
	if len(r.EndedAttempts) > 0 {
		says += fmt.Sprintf(" It ended provider attempt %s as interrupted, since its process had stopped.", strings.Join(r.EndedAttempts, ", "))
	}
	if len(r.OpenAttempts) > 0 {
		says += fmt.Sprintf(" It left provider attempt %s open, because it could not be shown stopped.", strings.Join(r.OpenAttempts, ", "))
	}
	return says + " The run keeps its claim, branch and worktree, and takes a slot again when a repair or a continuation picks it up."
}

// SlotHolder is one run the harness reads against the developer slots: the
// slot number it records, whether it counts against the slots now, and since
// when it has held what it holds. It is what the health check and the
// development manager's view list, so a slot that reads taken can be traced to
// the run, or the provider attempt, the harness counts in it.
type SlotHolder struct {
	// Slot is the number the run records, and zero — left out of what is
	// written, so no reader takes it for a slot called 0 — for a run counted
	// with no number recorded. Every developer run records one at its start, or
	// when it is next taken up again, where a number is free; Says says so of a
	// counted run that records none.
	Slot       int    `json:"slot,omitempty"`
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	// Counted is whether the run counts against the developer slots now, by the
	// rule every reservation and claim counts by (HoldsDeveloperSlotNow).
	Counted bool `json:"counted"`
	// Since is when the run recorded its slot number, or when the run started
	// where it recorded none.
	Since time.Time `json:"since"`
	// Attempt is the provider attempt of the run that may still be executing,
	// and AttemptSince when it was launched, or prepared where it never was.
	Attempt      string     `json:"attempt,omitempty"`
	AttemptSince *time.Time `json:"attempt_since,omitempty"`
	// Says is why the run is or is not counted, in a sentence a person reads.
	Says string `json:"says"`
}

// SlotHolders is every run in flight among states that counts against the
// developer slots or records a slot number, by slot number and then run. A
// harness-authored run — a document's landing, a file brought in — is counted
// on its own and is not here.
func (s *Store) SlotHolders(states []State, now time.Time) []SlotHolder {
	var holders []SlotHolder
	for _, state := range states {
		if !state.Status.InFlight() || state.HarnessAuthored() {
			continue
		}
		if !state.HoldsDeveloperSlot() && state.RecordedSlot() == 0 {
			continue
		}
		holder := SlotHolder{Slot: state.RecordedSlot(), RunID: state.RunID, WorkItemID: state.WorkItemID,
			Counted: s.HoldsDeveloperSlotNow(state, now), Since: state.StartedAt}
		if state.Routing != nil && state.Routing.Slot != nil {
			holder.Since = state.Routing.Slot.RecordedAt
		}
		if state.Routing != nil {
			for index := range state.Routing.Operations {
				operation := &state.Routing.Operations[index]
				if operation.Completed != nil {
					continue
				}
				if attempt, executing := operation.executingAttempt(); executing {
					since := attempt.PreparedAt
					if attempt.LaunchedAt != nil {
						since = *attempt.LaunchedAt
					}
					holder.Attempt, holder.AttemptSince = attempt.ID, &since
				}
			}
		}
		holder.Says = s.holderSays(state, holder.Counted, now)
		if holder.Counted && holder.Slot == 0 {
			holder.Says += "; its record names no slot number, so which numbered slot it holds is not known"
		}
		holders = append(holders, holder)
	}
	sort.SliceStable(holders, func(first, second int) bool {
		if holders[first].Slot != holders[second].Slot {
			return holders[first].Slot < holders[second].Slot
		}
		return holders[first].RunID < holders[second].RunID
	})
	return holders
}

// holderSays is why one run is or is not counted against the slots.
func (s *Store) holderSays(state State, counted bool, now time.Time) string {
	switch {
	case state.DependencyPause != nil:
		return "not counted: the run is paused on work its item waits on, and takes a slot again when it continues"
	case state.CIWait.Waiting() && state.CIWait.Overdue(now):
		limit := time.Duration(state.CIWait.WaitLimitSeconds) * time.Second
		return fmt.Sprintf("not counted: the run's wait for the forge's checks on the commit it pushed passed its limit of %s at %s, and the scheduler continues it at its first pull with a slot it can take, ahead of new work",
			limit, state.CIWait.PushedAt.Add(limit).UTC().Format(time.RFC3339))
	case state.CIWait.Waiting():
		return "not counted: the run is waiting for the forge's checks on the commit it pushed, and takes a slot again when it continues"
	case state.SlotRelease.releasesWorker(state.Worker):
		return "not counted: " + state.SlotRelease.Says
	case state.RedeployStop != nil:
		return "counted: the run was stopped for a redeploy, and its slot is kept for the session that comes back for it"
	}
	reading := s.Worker(state, now)
	if !counted {
		return "not counted, because its worker is gone: " + reading.Says
	}
	if reading.Liveness == WorkerUncertain {
		return "counted, because whether its worker is alive cannot be shown: " + reading.Says
	}
	return "counted: " + reading.Says
}
