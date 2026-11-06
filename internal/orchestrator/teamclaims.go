package orchestrator

// Claims on the shared remote, in team mode
// (docs/designs/working-alongside-other-contributors.md, section 5).
//
// A fresh run takes its item's claim reference on the remote before it claims
// the item in the tracker, and an item another factory holds is skipped this
// turn with the holder named. The team pause is read in the same fetch, and
// while it is set no work the harness chose is claimed; an item the operator
// named is still claimed, as the intake hold lets it start. The watch loop then keeps every claim this
// machine holds: it renews each one every fifteen minutes while its run holds
// the item, and releases it once the run no longer does. What a claim is, and
// the leases every push names, are internal/teamclaim's; this file is only when
// the pipeline and the scheduler ask.
//
// A project with team mode off wires none of this, and nothing here waits on the
// remote.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/teamclaim"
)

// TeamClaims is what a run starting in team mode asks of the shared remote. It
// is satisfied by teamclaim.Claims.
type TeamClaims interface {
	Take(ctx context.Context, workItemID, runID string) (runstate.TeamClaim, error)
	TakeNamed(ctx context.Context, workItemID, runID string) (runstate.TeamClaim, error)
	Release(ctx context.Context, workItemID, runID string) error
}

// ScheduleTeamClaims keeps the claims this machine holds on the shared remote.
// It is satisfied by teamclaim.Claims, and a pull wired without one is a
// project with team mode off.
type ScheduleTeamClaims interface {
	Tend(ctx context.Context, holding teamclaim.Holding) (teamclaim.Upkeep, error)
}

// takeTeamClaim claims the item on the shared remote for a run about to start
// it, where team mode is on. It is asked after the intake hold and every
// question answerable here, and before anything is reserved or claimed in the
// tracker, so an item another factory holds costs nothing but the push.
//
// An item the operator named is his exception to the team pause, as it is to
// the intake hold (holdIntake), so it is claimed whatever the pause says.
func (p *Pipeline) takeTeamClaim(ctx context.Context, workItemID, runID string) (runstate.TeamClaim, error) {
	if p.TeamClaims == nil {
		return runstate.TeamClaim{}, nil
	}
	if !p.Selection.SelectedByHarness() {
		return p.TeamClaims.TakeNamed(ctx, workItemID, runID)
	}
	return p.TeamClaims.Take(ctx, workItemID, runID)
}

// giveBackTeamClaim releases the claim a run took and then never started under,
// so another factory need not wait for the next upkeep to have the item. One
// that cannot be given back now is released by that upkeep, because nothing on
// this machine holds the item.
func (p *Pipeline) giveBackTeamClaim(ctx context.Context, workItemID, runID string) {
	if p.TeamClaims == nil {
		return
	}
	_ = p.TeamClaims.Release(ctx, workItemID, runID)
}

// noteTeamTakeover records on the item that its claim was taken over from
// another factory, naming that factory, so whoever reads the item learns that
// another machine had it first and may have left work on it. A note that cannot
// be written leaves the run as it is: the claim on the remote names the earlier
// holder too.
func (a *activeRun) noteTeamTakeover(ctx context.Context, claim runstate.TeamClaim) {
	if claim.PreviousHolder == "" {
		return
	}
	note := fmt.Sprintf("Run %s took over the claim on this item from %s, which had not renewed it for two hours.", a.state.RunID, claim.PreviousHolder)
	_, _ = a.pipeline.Tracker.RecordOutcome(ctx, a.state.WorkItemID, note)
}

// teamClaimHeld reads a refused start that was another factory holding the
// item, the team pause, or the shared remote not answering, as the declined
// start it is, in words naming the holder or whoever set the pause where one is
// known.
func teamClaimHeld(err error) (string, bool) {
	var held teamclaim.HeldError
	if errors.As(err, &held) {
		return held.Error() + ", so this machine did not start it", true
	}
	var paused teamclaim.PausedError
	if errors.As(err, &paused) {
		return paused.Error(), true
	}
	var unreachable teamclaim.UnreachableError
	if errors.As(err, &unreachable) {
		return unreachable.Error(), true
	}
	return "", false
}

// tendTeamClaims renews and releases the claims this machine holds, and says on
// the schedule what it did and what it could not. Nothing here stops the pass.
//
// A claim is held while its run is in flight, or while its item is still
// claimed or blocked in this machine's tracker, which is a run waiting for a
// person's merge or stopped and kept for a decision. Every other claim's run has
// ended, and it is released.
func (s Scheduler) tendTeamClaims(ctx context.Context, schedule *Schedule, pull Pull) {
	if pull.TeamClaims == nil {
		return
	}
	claimed, claimedErr := pull.list(ctx, claimedStatus)
	blocked, blockedErr := pull.list(ctx, "blocked")
	listingErr := errors.Join(claimedErr, blockedErr)
	held := map[string]bool{}
	for _, item := range slices.Concat(claimed, blocked) {
		held[item.ID] = true
	}
	inFlight := map[string]bool{}
	if runs, err := pull.Runs.Incomplete(); err == nil {
		for _, run := range runs {
			inFlight[run.RunID] = true
		}
	} else {
		listingErr = errors.Join(listingErr, err)
	}
	upkeep, err := pull.TeamClaims.Tend(ctx, func(claim runstate.TeamClaim) (bool, error) {
		if inFlight[claim.RunID] || held[claim.WorkItemID] {
			return true, nil
		}
		return false, listingErr
	})
	schedule.TeamClaims = &upkeep
	var problems []string
	if err != nil {
		problems = append(problems, fmt.Sprintf("the claims this machine holds on the shared remote could not be looked after: %v", err))
	}
	problems = append(problems, upkeep.Problems...)
	schedule.TeamClaimProblem = strings.Join(problems, "; ")
}

func teamClaimDeclined(err error) bool {
	_, declined := teamClaimHeld(err)
	return declined
}
