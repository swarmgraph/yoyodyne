package orchestrator

// Auditing the claims the tracker holds against the runs the harness actually
// has, and giving back the ones with nothing alive behind them.
//
// The harness claims a work item at the start of every run and nothing gives the
// claim back when the process holding it dies. What that leaves is an item the
// tracker calls in progress, a run record nobody is writing to, and a scheduler
// that will never choose the item again — because it chooses from what the
// tracker calls ready, and a claimed item is not ready. On four nights of the
// week of 2026-09-01 that was the whole of the line's throughput, and on the last
// of them both developer slots went that way inside an hour and the machine sat
// idle until morning. Nothing reported it, because there was nothing to report:
// every surface here reads a claimed item as work in progress.
//
// So the audit reads the claims against the runs and gives back the ones nothing
// is working on. It is here rather than in a surface because it writes: giving a
// claim back is directing work, which is the harness's, and a surface that did it
// would be a second control plane. It is called from the watch loop rather than
// from a person because the failure it catches is precisely the one nobody is
// there for.
//
// It decides two things and neither of them is what a message says. Which claims
// are dead is the read model's — the same derivation `yoyo status` would read —
// and saying so once is the durable record's, exactly as it is for a stall.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// ClaimTracker is the tracker access one claim audit needs: giving one item
// back to the queue, with the reason on its notes. It is satisfied by
// beads.Client.
type ClaimTracker interface {
	Release(ctx context.Context, id, reason string) (beads.WorkItem, error)
}

// ClaimRuns is the durable run state one claim audit reads and settles.
//
// Recorded is every run the harness holds rather than only the ones in flight,
// because the case this exists for is a claim whose run is over: a listing of
// what is still going cannot tell an item nothing ever ran from an item whose run
// ended without giving its claim back.
//
// AdoptRun and Save are what make a release worth making. Giving the claim back
// is only half of freeing an item whose process was killed: the run's own record
// still says it is in flight, so it goes on filling a developer slot and the
// scheduler goes on passing the item over as already running. The record has to
// be settled too, and it is settled under the run's own lease — which is also the
// only test of liveness that is not a guess, because a lease belongs to a live
// process and is dropped by the operating system when that process dies.
//
// It is satisfied by runstate.Store.
type ClaimRuns interface {
	Recorded() ([]runstate.State, error)
	AdoptRun(ctx context.Context, runID string) (runstate.State, *runstate.Lease, error)
	Save(state runstate.State) error
}

// ClaimReleases is where a release is written down and read back. The reading is
// what makes "once" a property of the record rather than of a process's memory:
// a tracker that has not caught up, or a release the store accepted and the
// tracker refused, must not produce a second release and a second message.
type ClaimReleases interface {
	Append(released runstate.ReleasedClaim) error
	List() ([]runstate.ReleasedClaim, error)
}

// ClaimAuditor gives back the claims with nothing alive behind them.
type ClaimAuditor struct {
	Tracker  ClaimTracker
	Runs     ClaimRuns
	Releases ClaimReleases
	// ProductID is what a record this audit writes has to name. The store knows
	// its own product, and a record it will not accept from the wrong one is
	// exactly why this is carried rather than assumed.
	ProductID domain.ProductID
	// Threshold is how long a claim may have nothing alive behind it before it is
	// dead rather than a tracker catching up. Zero takes
	// readmodel.DefaultDeadClaimThreshold.
	Threshold time.Duration
	// Window is how long a run's own record may go unmoved before it stops
	// counting as alive. Zero takes readmodel.DefaultRunActivityWindow.
	Window time.Duration
	// Remains is the repository the audit asks whether the run behind a claim
	// left its change on a branch or in a checkout, which is both what keeps the
	// claim standing and what the release says on the item. Nil answers from the
	// run's own record, and the release says nothing looked.
	Remains readmodel.Remains
	Clock   execution.Clock
}

// ClaimSweep is what one audit did: the claims it gave back, and the ones it
// could not.
//
// Both are reported because they are different things for an operator. A release
// is the harness having found a stuck item and freed it, which is worth being
// told once; a release that failed is an item still stuck, which is worth being
// told about for as long as it stays that way.
type ClaimSweep struct {
	Released []runstate.ReleasedClaim `json:"released,omitempty"`
	// Problems name the claims this audit found dead and could not give back. They
	// are named rather than counted because what a person does about one is
	// specific to the item.
	Problems []string `json:"problems,omitempty"`
}

func (a ClaimAuditor) validate() error {
	var problems []error
	if a.Tracker == nil {
		problems = append(problems, errors.New("a claim audit requires a work tracker"))
	}
	if a.Runs == nil {
		problems = append(problems, errors.New("a claim audit requires the durable run state"))
	}
	if a.Releases == nil {
		problems = append(problems, errors.New("a claim audit requires somewhere to record what it released"))
	}
	if err := domain.ValidateIdentifier("product id", string(a.ProductID)); err != nil {
		problems = append(problems, err)
	}
	return errors.Join(problems...)
}

// Audit reads the claimed items against the recorded runs and gives back the
// ones with nothing alive behind them.
//
// One claim that cannot be given back is reported and never stops the sweep: an
// item the tracker refuses must not hide the others from the operator reading
// the report, and it is exactly the pass where several claims died at once that
// this matters in.
func (a ClaimAuditor) Audit(ctx context.Context, claimed []beads.WorkItem) (ClaimSweep, error) {
	if err := a.validate(); err != nil {
		return ClaimSweep{}, err
	}
	if len(claimed) == 0 {
		// Nothing is claimed, so nothing can be stuck. It is answered before the
		// records are read because this is the ordinary case on a quiet product and
		// the reading costs a directory walk.
		return ClaimSweep{}, nil
	}
	runs, err := a.Runs.Recorded()
	if err != nil {
		return ClaimSweep{}, fmt.Errorf("read the recorded runs: %w", err)
	}
	alreadyReleased, err := a.Releases.List()
	if err != nil {
		return ClaimSweep{}, fmt.Errorf("read the claims already given back: %w", err)
	}
	claims := make([]readmodel.Claim, 0, len(claimed))
	for _, item := range claimed {
		claims = append(claims, readmodel.Claim{WorkItemID: item.ID, Title: item.Title})
	}
	// The runs by identifier, so the settlement below can act on the record the
	// derivation actually read rather than reading it again. What it needs from it
	// is whether the run already ended, which is the one fact about a run that
	// cannot become untrue: nothing takes a terminal record back to running.
	recorded := make(map[string]runstate.State, len(runs))
	for _, run := range runs {
		recorded[run.RunID] = run
	}
	now := a.clock().Now()
	look := readmodel.Looking(ctx, a.Remains, func() time.Time { return now })
	var sweep ClaimSweep
	for _, dead := range readmodel.DeadClaims(claims, runs, now, a.Threshold, a.Window, look) {
		if saidAlready(alreadyReleased, dead) {
			// This claim has been given back once already and the tracker still shows
			// it claimed. Releasing it again would be the same act said twice — and,
			// since a message is written from each record, a second message about one
			// stuck item. What a claim that came straight back needs is a person, and
			// the record they need is the one already written.
			continue
		}
		released := runstate.ReleasedClaim{
			SchemaVersion: runstate.ReleasedClaimSchemaVersion,
			ProductID:     a.ProductID,
			WorkItemID:    dead.WorkItemID,
			WorkItemTitle: dead.Title,
			RunID:         dead.RunID,
			Since:         dead.Since,
			Because:       dead.Because,
			ReleasedAt:    now,
		}
		if dead.Found.Recorded() {
			found := dead.Found
			released.Found = &found
		}
		// The run's own record is settled first, because it is the half that
		// actually frees the item: a record still saying in flight fills a developer
		// slot and keeps the scheduler passing the item over however open the tracker
		// says it is. A release made without it would be a fix that reads as one and
		// leaves the line exactly as idle as it was.
		settled, problem := a.settle(ctx, dead, recorded[dead.RunID], now)
		if problem != "" {
			sweep.Problems = append(sweep.Problems, problem)
			continue
		}
		if !settled {
			// A live process holds the run after all. The reading that called this
			// claim dead was a snapshot and the lease is the authority, so the claim
			// is left exactly where it is and nothing is said about it.
			continue
		}
		if _, err := a.Tracker.Release(ctx, dead.WorkItemID, releaseNotes(released)); err != nil {
			sweep.Problems = append(sweep.Problems, fmt.Sprintf(
				"%s is claimed with nothing working on it and the claim could not be given back, so nothing will pull it: %v",
				dead.WorkItemID, err))
			continue
		}
		// The record is written after the tracker took the release rather than
		// before, because what it is a record of is the release having happened. A
		// process that dies between the two leaves the item back in the queue and no
		// record of why, which the next audit finds as a claim that is no longer
		// claimed and says nothing about — a message lost, rather than an item.
		if err := a.Releases.Append(released); err != nil {
			sweep.Problems = append(sweep.Problems, fmt.Sprintf(
				"%s was given back to the queue and the release could not be recorded, so nobody will be told about it: %v",
				dead.WorkItemID, err))
			continue
		}
		sweep.Released = append(sweep.Released, released)
	}
	return sweep, nil
}

// settle ends the record of the run that left the claim, so the developer slot it
// was filling comes back with the item. It reports whether the claim may now be
// given back, and the sentence to say where it may not.
//
// A run that already ended is not taken up at all: its slot is free the moment it
// went terminal, so there is nothing here to settle and nothing to take a lease
// for. That is decided from the record the derivation read rather than from a
// fresh one, and it is the one fact about a run that may be decided that way —
// nothing takes a terminal record back to running, so a snapshot saying a run
// ended cannot go stale. Adopting it anyway would rest the whole
// run-ended-without-giving-the-claim-back case on how the store answers for a
// record nobody holds, which is a question this has no reason to ask.
//
// The rest are settled under the run's own lease, which is doing two jobs at
// once. It is the exclusion — nothing else may be deciding about this run while
// this writes to it — and it is the liveness test that is not a guess: a lease is
// an advisory lock a live holder owns and the operating system drops when that
// holder dies, so a lease this pass can take is a process that is gone. That is
// why a claim the reading called dead is still left alone when the lease refuses:
// the reading was a snapshot of timestamps, and this is the answer.
//
// The ending recorded is cancelled rather than failed, in the read model's own
// vocabulary: nothing here judged the change. The worktree and the branch are
// untouched, so whatever the killed run produced is exactly where it left it, and
// the fresh run this frees the item for is a fresh run rather than a repair —
// which is what a process nobody can resume leaves either way.
//
// This settles a narrower set than `yoyo reconcile` does and never overlaps it:
// it moves no refs, removes nothing, closes nothing, and touches only runs that
// have gone quiet past the audit's threshold with no wait recorded on them.
func (a ClaimAuditor) settle(ctx context.Context, dead readmodel.DeadClaim, recorded runstate.State, now time.Time) (bool, string) {
	if recorded.Status.Terminal() {
		return true, ""
	}
	state, lease, err := a.Runs.AdoptRun(ctx, dead.RunID)
	switch {
	case errors.Is(err, runstate.ErrRunHeld):
		return false, ""
	case err != nil:
		return false, fmt.Sprintf(
			"%s is claimed with nothing working on it and its run %s could not be taken up to be settled, so the slot it holds is not free and nothing will pull the item: %v",
			dead.WorkItemID, dead.RunID, err)
	}
	defer lease.Release()

	// Re-read under the lease, because only now is this process the one entitled
	// to act on what it reads. Everything the snapshot decided on is asked again
	// here: a run that ended, parked, or promoted between the reading and the
	// lease is owed exactly what its record now says it is, and the last of those
	// is the one that would cost the most — an item whose change has landed and is
	// developed a second time is the same change bought twice and a conflict at the
	// end of it.
	if state.Status.Terminal() {
		return true, ""
	}
	if readmodel.AwaitingContinuation(state) || state.Integration != nil {
		return false, ""
	}
	completedAt := now
	// A stop for a redeploy promises that the next session continues the run, and
	// a terminal record cannot carry that promise: the store refuses to save one
	// that does. A run reaching here has gone quiet past the audit's threshold
	// with nothing holding its lease, so no session kept that promise and the
	// ending is recorded without it. Left on the record it would refuse this ending
	// at every audit and keep the run counted as in flight — holding its developer
	// slot and holding back every item that shares a file with it.
	state.RedeployStop = nil
	state.StopClass = runstate.StopDeadClaim
	state.Status = runstate.StatusCancelled
	state.SettledQuietSince = settledQuietSince(state, completedAt)
	state.UpdatedAt = completedAt
	state.CompletedAt = &completedAt
	state.Failure = settledRunFailure(dead)
	if err := a.Runs.Save(state); err != nil {
		return false, fmt.Sprintf(
			"%s is claimed with nothing working on it and the ending of its run %s could not be recorded, so the slot it holds is not free and nothing will pull the item: %v",
			dead.WorkItemID, dead.RunID, err)
	}
	return true, ""
}

// settledRunFailure is what the ended record says about itself, in the words
// somebody reading the run afterwards needs: not that it failed at anything, but
// that the process carrying it stopped existing and this is who noticed.
func settledRunFailure(dead readmodel.DeadClaim) string {
	return fmt.Sprintf("the process carrying this run stopped writing to its record at %s and did not come back, so the claim audit ended the run and gave %s back to the queue. Nothing about the change was judged, and the branch and worktree it left are untouched.",
		dead.Since.UTC().Format(time.RFC3339), dead.WorkItemID)
}

// saidAlready reports a claim already given back over this same dead run.
//
// The key is the run rather than the item, and that is what keeps the two cases
// apart. A release the tracker did not take — an item that reads as claimed on
// the next pull anyway — is the same run's death said twice, and is skipped; a
// fresh run that was started for the item and died in its turn is a different
// death, and is said again. Keying on the item alone would swallow the second
// forever, and keying on a moment cannot separate them at all: a release is
// always recorded after the run it is about last spoke.
func saidAlready(released []runstate.ReleasedClaim, dead readmodel.DeadClaim) bool {
	for _, record := range released {
		if record.WorkItemID == dead.WorkItemID && record.RunID == dead.RunID {
			return true
		}
	}
	return false
}

// releaseNotes is what the item's own notes record about having been given back.
// It is written onto the item because that is where the next attempt at the work
// reads: a developer who finds a worktree from a run nobody finished should be
// told the harness gave the item back rather than left to infer it.
//
// It says what the run left, as the repository had it when the claim was given
// back, because "nothing was working on it" is a statement about processes and
// was read as one about the change: on 2026-09-23 the development manager
// crossed a cap on yoyodyne-ifd.432.10 reasoning that run-838ffc48 held no
// preserved change, from a release that said nothing either way while the run's
// branch held the approved change.
func releaseNotes(released runstate.ReleasedClaim) string {
	note := fmt.Sprintf("The harness gave this item back to the queue at %s: %s. Nothing was working on it, so it was released to be pulled again.",
		released.ReleasedAt.UTC().Format(time.RFC3339), released.Because)
	if released.Found == nil {
		return note
	}
	return note + "\nWhat the run left: " + released.Found.Describe() + "." + releasedChangeAdvice(*released.Found)
}

// releasedChangeAdvice is the one sentence a release adds where the run's change
// is still there: that a fresh run should start from it rather than beside it.
func releasedChangeAdvice(found triage.Found) string {
	if !found.Holds() {
		return ""
	}
	return " That change is still there; a run started for this item should pick it up rather than derive it again."
}

func (a ClaimAuditor) clock() execution.Clock {
	if a.Clock == nil {
		return execution.RealClock{}
	}
	return a.Clock
}
