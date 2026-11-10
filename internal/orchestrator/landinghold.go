package orchestrator

// The hold an outside tool places on landing on one target branch
// (runstate/landinghold.go) is read here, under the promotion lease and
// immediately before the guarded change, by every path that lands: the run's
// own integrate, the merge queue's fresh, and the two that arm a forge merge
// (armRecoveredMerge, Rearmer.repeat). Catch-up and the recording of a landing
// that already happened read nothing here, because they follow the remote
// rather than land anything.

import (
	"context"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// LandingHolds reads whether landing on a target branch is held. A nil
// LandingHolds on a Pipeline, Reconciler or Rearmer reads no hold. It is
// satisfied by *runstate.LandingHoldStore.
type LandingHolds interface {
	Standing(targetBranch string, now time.Time) (runstate.LandingHold, bool, error)
}

var _ LandingHolds = (*runstate.LandingHoldStore)(nil)

// landingHoldProbe is the longest a run waits on a hold before reading it
// again, so a release is noticed within it rather than at the hold's expiry.
const landingHoldProbe = 30 * time.Second

// standingLandingHold reads the hold on target at now; none configured is none.
func standingLandingHold(holds LandingHolds, target string, now time.Time) (runstate.LandingHold, bool, error) {
	if holds == nil {
		return runstate.LandingHold{}, false, nil
	}
	return holds.Standing(target, now)
}

// awaitLandingHold returns with the promotion lease held and no hold standing
// on the target. While a hold stands the lease is let go and the run sleeps:
// other runs' checks and reviews never take the lease, but keeping it for hours
// would block every catch-up and reconciliation of the branch, and the wait is
// bounded by the hold's own expiry. Nothing of the run's phase or evidence is
// touched, so the change waits with its evidence kept. On an error the lease
// has been released and nil is returned; the caller marks the stop
// as an integration stop.
func (a *activeRun) awaitLandingHold(ctx context.Context, lease *runstate.Lease) (*runstate.Lease, error) {
	p := a.pipeline
	target := a.worktree.TargetBranch
	for {
		now := p.clock().Now()
		hold, held, err := standingLandingHold(p.LandingHolds, target, now)
		if err != nil {
			_ = lease.Release()
			return nil, fmt.Errorf("read whether landing on %s is held: %w", target, err)
		}
		if !held {
			return lease, nil
		}
		_ = lease.Release()
		a.landingAt("waiting for the hold on landing on "+target+" to end — "+hold.Says(), landingAsksNothing)
		wait := min(hold.Until.Sub(now), landingHoldProbe)
		if err := p.sleep(ctx, wait); err != nil {
			return nil, fmt.Errorf("wait for the hold on landing on %s to end: %w", target, err)
		}
		lease, err = p.Store.LeasePromotion(ctx, target)
		if err != nil {
			return nil, fmt.Errorf("wait for this run's turn to promote: %w", err)
		}
	}
}
