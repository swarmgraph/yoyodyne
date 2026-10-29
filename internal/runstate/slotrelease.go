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
// shown to be gone (WorkerReading.ProcessGone), or when its worker can be shown
// neither alive nor gone and nothing has held or written the run for
// UncertainWorkerQuiet (Presence). A run whose worker is alive, or may be, is
// left alone, and so is one its session stopped for a redeploy, whose slot is
// kept for the session that comes back for it.
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
		if why, ok := s.noLiveWorker(state, now); !ok || why == "" {
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

// noLiveWorker says why a run counts against the developer slots with no live
// worker behind it, and reports false for every other run.
func (s *Store) noLiveWorker(state State, now time.Time) (string, bool) {
	if !state.HoldsDeveloperSlot() || state.RedeployStop != nil || state.SlotRelease.releasesWorker(state.Worker) {
		return "", false
	}
	reading := s.Worker(state, now)
	if reading.ProcessGone() {
		return reading.Says, true
	}
	if reading.Liveness != WorkerUncertain {
		return "", false
	}
	presence, err := s.Presence(state, UncertainWorkerQuiet, now)
	if err != nil || presence.Found {
		return "", false
	}
	return fmt.Sprintf("whether its worker is alive cannot be shown (%s), and %s", reading.Says, presence.Says), true
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
	release.Says = release.sentence(why)
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
	// Slot is the number the run records, and zero for a run counted with no
	// number recorded.
	Slot       int    `json:"slot"`
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
