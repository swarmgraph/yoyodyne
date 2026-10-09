package readmodel

// A claim with nothing behind it, which is the other state nothing in the record
// announces.
//
// The silence reading beside this one asks whether anything at all has started.
// It catches a line that has stopped over a queue with work in it, and it
// structurally cannot catch this one: an item the harness claimed has left the
// ready queue, so a machine whose only startable work is sitting under dead
// claims reads as a drained queue from every surface here — nothing held,
// nothing running, nothing ready, nothing wrong. That is what happened on four
// nights of the week of 2026-09-01, twice in one hour on the last of them, and
// each time the line was idle until somebody looked in the morning.
//
// So this reads the claims against the runs. Not "what does the tracker say is
// being worked on" but "does the harness actually have a run alive for it" — a
// question answered from the runs' own records rather than from the claim, which
// is why it survives the death of the process that made the claim. The claim is
// the one thing a dead run leaves behind that nothing else cleans up: the run's
// record stops moving and the item stays claimed forever.
//
// What is here is only the reading. Giving the claim back is the harness's, and
// saying so is the surface's, exactly as the silence reading beside it divides
// the same work.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// DefaultDeadClaimThreshold is how long a claim may have nothing alive behind it
// before that is a dead claim rather than the gap between a run ending and the
// tracker catching up.
//
// Half an hour is chosen against what it costs to be wrong in each direction, and
// the two costs are not symmetric. Acting early releases an item something is
// still finishing, which the scheduler then declines to start anyway — a run in
// flight for an item is what keeps it out of every pull — so the price is a
// message nobody needed. Acting late is the failure this exists to end, and the
// window that was actually paid for was a night at a time.
//
// It is longer than DefaultStallThreshold on purpose. A stall is read from an
// absence and acting on it changes nothing — it is said, and somebody looks —
// where acting on this writes: it ends a run's record and gives its item back.
// A run that ends gives its own claim back as the last thing it does, after
// the record has stopped moving, so the threshold is the room that write is
// given before the claim it has not yet taken back is read as one it never will.
const DefaultDeadClaimThreshold = 30 * time.Minute

// Claim is one item the tracker says somebody has already pulled: the identifier
// and what it is called. It is passed in rather than read here for the reason
// Activity's fields are — the caller has read the claimed slice already, and a
// second reading is a second chance for one pass to report one machine two ways.
type Claim struct {
	WorkItemID string
	Title      string
}

// DeadClaim is one claim with nothing alive behind it: the item, the run whose
// death left it, when that run last said anything, and what the record says
// became of it.
type DeadClaim struct {
	WorkItemID string `json:"work_item_id"`
	Title      string `json:"title,omitempty"`
	// RunID is the most recent run recorded for the item, which is the one whose
	// ending or whose silence left the claim behind.
	RunID string `json:"run_id,omitempty"`
	// Since is the last moment that run recorded anything, which is what the age of
	// the dead claim is measured from.
	Since time.Time `json:"since,omitempty"`
	// Because is what the record says about that run, as the clause a message is
	// written around. It is what tells a run the harness finished from a process
	// somebody killed, and the two want different things done about them.
	Because string `json:"because,omitempty"`
	// Found is what the repository held of that run's branch and worktree when
	// the audit read the claim, which the release says on the item: a release
	// that said only that nothing was working on the item was read, on
	// 2026-09-23, as run-838ffc48 having preserved nothing while its branch held
	// the approved change.
	Found triage.Found `json:"found"`
}

// DeadClaims reports the claims that have nothing alive behind them.
//
// A claim with no recorded run at all is never one of them, and that is a
// boundary rather than an oversight. The harness reserves a run before it claims
// anything, so every claim it makes has a record; a claim with none was made by
// somebody else — a person at a terminal, an agent working by hand — and giving
// back work a person took is not this pass's to do.
//
// A claim is kept wherever the run behind it is working or waiting; see
// holdsItsClaim, which is the whole of the safety of this. Being wrong in that
// direction leaves an item stuck for somebody to notice, and being wrong in the
// other puts a second developer on work the first is still holding.
//
// It is kept, too, wherever the run behind it ended holding its change — see
// stillHeld. A claim there is not a claim nothing is working on: it is the one
// thing saying an item whose change is on a branch is spoken for, and a release
// buys a fresh run started over the top of it.
//
// Whether the change is still there is look's answer: the repository, asked as
// the audit reads, which is the answer the pull's hold gives about the same run.
// A nil look answers from the run's own record and says so on the claim.
func DeadClaims(claims []Claim, runs []runstate.State, now time.Time, threshold, within time.Duration, look Look) []DeadClaim {
	if look == nil {
		look = Looking(context.Background(), nil, func() time.Time { return now })
	}
	if threshold <= 0 {
		threshold = DefaultDeadClaimThreshold
	}
	if within <= 0 {
		within = DefaultRunActivityWindow
	}
	recorded := map[string][]runstate.State{}
	for _, run := range runs {
		recorded[run.WorkItemID] = append(recorded[run.WorkItemID], run)
	}
	var dead []DeadClaim
	for _, claim := range claims {
		behind := recorded[claim.WorkItemID]
		if len(behind) == 0 {
			continue
		}
		latest, since, held := readClaim(behind, now, within)
		if held || since.IsZero() || now.Sub(since) < threshold {
			continue
		}
		// An item whose latest run already promoted its change is not work to be
		// started again, whatever its claim says. The run got as far as putting the
		// change on the target branch and stopped somewhere after it — a tracker
		// write that failed is the way there — so the item wants closing rather than
		// developing a second time, and `yoyo reconcile` is what closes it from the
		// promotion the record holds. Giving this claim back would buy the same
		// change twice and a conflict at the end of it.
		if latest.Integration != nil {
			continue
		}
		// Nor is an item whose latest run handed its approved change to the
		// merge queue: the queue lands it and settles the item, or hands it back
		// to that run, and a claim given back would start the change again
		// beside the one waiting to land.
		if latest.AwaitingMergeQueue() {
			continue
		}
		// And an item whose latest run ended holding its change is not work to be
		// started again either. The claim is the one thing saying so, and the pull's
		// hold reads the same endings: see stillHeld.
		found := look(latest)
		if stillHeld(latest, found) {
			continue
		}
		dead = append(dead, DeadClaim{
			WorkItemID: claim.WorkItemID,
			Title:      claim.Title,
			RunID:      latest.RunID,
			Since:      since,
			Because:    whatBecameOfIt(latest, since),
			Found:      found,
		})
	}
	return dead
}

// stillHeld reports a run whose ending left its item held rather than free, so
// that giving the claim back says something about the item that is not true.
//
// It is the same fact the pull's hold reads, asked here so that the two cannot
// disagree about one run. A claim is the harness saying an item is spoken for,
// and these endings are exactly the ones where it still is: the change is on a
// branch, somebody or the harness owes it a move, and what a release buys is a
// fresh run started over the top of it. That is what run-b0b6d18d cost on
// 2026-09-22 — an approved change stopped at the promotion by a tracker read
// that timed out, released as a dead claim half an hour later, and re-derived
// by a fresh pull that spent a developer run and a review to reach the change
// that was already sitting on the branch.
//
// Four endings qualify, and the first is the one that had no other marker at
// all. An integration stop is an approved change the environment stopped short
// of the target branch, which `yoyo triage resume` carries the rest of the way.
// A recorded blocker is the harness having handed the item to somebody — a
// replay that conflicted against a moved target first among them. And a run that
// died inside its own process hands nobody a blocker on purpose, so its record
// ends `failed` while its change sits on a branch exactly as a stoppage's does.
// A run whose check stage its bound stopped is the fourth: it ends `timed out`
// with its finished change on the branch, and the harness continues it at its
// checks — a claim given back here is a fresh run that redoes the development.
//
// All four ask whether the change survived, and none of them holds a claim
// without it: a stoppage whose branch is gone leaves nothing for a fresh run to
// strand, and an integration stop whose branch is gone is one nothing can resume
// at all. It is asked of the repository, as the pull's hold asks it, and a look
// that could not be made holds the claim — so a repository nobody could read
// costs a claim left standing rather than a change run over.
//
// A run that has not ended is never one of these. Its record is what fills a
// developer slot and its claim is what keeps the item out of every pull, and
// leaving both standing on a process that is gone is the failure the audit
// exists for.
func stillHeld(run runstate.State, found triage.Found) bool {
	if !run.Status.Terminal() || !found.Holds() {
		return false
	}
	return run.IntegrationStop != nil ||
		strings.TrimSpace(run.Blocker) != "" ||
		run.DiedInItsOwnProcess() ||
		run.StoppedAtStageBound()
}

// readClaim reads one item's runs: the most recent of them, the last moment that
// run recorded anything, and whether any of them still holds the claim.
//
// The most recent is by the same ordering the run store keeps — the start orders
// them, and the last update settles a tie — because what is true of an item now
// is decided by the last run that said anything about it.
//
// The age is that run's alone rather than the newest stamp across all of them,
// and the difference is not academic: a run this audit has already ended carries
// the moment it was ended, so a reading that took the newest stamp anywhere would
// date an item from the audit's own writing and call every later death of it
// fresh for the length of the threshold.
//
// Whether the claim is held is asked of every run rather than only the latest,
// because a run is only ever started for an item nothing else is running — so a
// second one that is alive means the ordering here disagrees with the reservation
// that let it start, and the safe reading of that disagreement is the one that
// keeps the claim.
func readClaim(runs []runstate.State, now time.Time, within time.Duration) (latest runstate.State, since time.Time, held bool) {
	for _, run := range runs {
		if holdsItsClaim(run, now, within) {
			held = true
		}
		if latest.RunID == "" || laterRun(run, latest) {
			latest = run
		}
	}
	return latest, lastWord(latest), held
}

// holdsItsClaim reports a run that still owns the item it claimed: one that is
// visibly working, and one that is deliberately waiting.
//
// The first is the run's own UpdatedAt, which is the test ActiveRuns uses. The
// second is AwaitingContinuation below.
func holdsItsClaim(run runstate.State, now time.Time, within time.Duration) bool {
	if run.Status.Terminal() {
		return false
	}
	if !run.UpdatedAt.IsZero() && now.Sub(run.UpdatedAt) < within {
		return true
	}
	return AwaitingContinuation(run)
}

// AwaitingContinuation reports a run that stopped short on purpose and is owed
// the rest of what it was doing.
//
// It is the half of a claim's liveness that cannot be read from a clock at all. A
// run that stopped for a provider that refused it, for the operator's pause, for
// an unresolved directive, for work its item depends on, or for a tracker that
// would not answer the read a gate boundary makes — or that the reconciling
// sweep put back at its promotion to bring a queued head up to date — does not just go quiet
// — it returns, and its process exits, leaving a record nothing writes to again
// until somebody continues it. Its item stays claimed on purpose, with the
// worktree and the developer session that continuation needs, so a record that
// looks as still as a killed one's is the ordinary state of every one of these.
//
// Every field read here records a wait that is still pending, and that is the
// whole rule: a marker is only evidence of a continuation being owed while it
// still says one is being waited for. A count of rounds already taken is not one
// of them and must not be added — RepairAttempts is the example that was tried
// and is wrong, because it never goes back down, so a killed run that had been
// round once would be exempt from every audit for the rest of the product's life.
// Nothing is lost by leaving it out: a repair the harness would rather continue
// than replace is recognized from the run's recorded blocker, which the pipeline
// reads for itself before it starts anything fresh.
//
// It is exported because the audit re-asks it under the run's lease, where the
// answer is authoritative rather than a snapshot: a run that parked between the
// reading and the lease must not be settled by what the reading saw.
func AwaitingContinuation(run runstate.State) bool {
	if run.Status.Terminal() {
		return false
	}
	return run.UsageLimitResetsAt != nil ||
		run.PauseCause != "" ||
		run.ProviderStop != "" ||
		run.DirectivePause != nil ||
		run.DependencyPause != nil ||
		run.TrackerPause != nil ||
		run.OperatorHeldSince != nil ||
		run.UpdatingQueuedHead()
}

// laterRun reports the more recent of two runs on one item, in the order
// runstate.Store.Runs keeps: the start orders them, whatever either goes on to
// do, and the last update settles the tie two runs started at one instant leave.
func laterRun(run, held runstate.State) bool {
	if !run.StartedAt.Equal(held.StartedAt) {
		return run.StartedAt.After(held.StartedAt)
	}
	return run.UpdatedAt.After(held.UpdatedAt)
}

// lastWord is the last moment one run recorded anything. All three stamps are
// read rather than only the update, because a record that ended carries its
// ending on CompletedAt and a record that never moved past its reservation
// carries nothing but its start.
func lastWord(run runstate.State) time.Time {
	moment := run.UpdatedAt
	if run.CompletedAt != nil && run.CompletedAt.After(moment) {
		moment = *run.CompletedAt
	}
	if run.StartedAt.After(moment) {
		moment = run.StartedAt
	}
	return moment
}

// whatBecameOfIt is the clause a message about a dead claim is written around.
//
// The two cases are said apart because they are two different histories, and a
// reader acts on the difference: a run that ended left the claim behind by
// finishing without giving it back, and one still recorded as in flight is a
// process that was killed. What is done about them is the same either way — the
// claim goes back and the item is pullable again — so neither clause hands
// anybody a chore.
func whatBecameOfIt(latest runstate.State, since time.Time) string {
	stamp := since.UTC().Format(time.RFC3339)
	if latest.Status.Terminal() {
		return fmt.Sprintf("its run %s ended %s at %s and the claim outlived it",
			latest.RunID, latest.Outcome(), stamp)
	}
	return fmt.Sprintf("its run %s was recorded as still in flight and last wrote to its own record at %s, so the process holding it is gone",
		latest.RunID, stamp)
}
