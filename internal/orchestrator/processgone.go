package orchestrator

// Settling the runs whose process is gone, wherever runs are read.
//
// A run's record says "running" until something writes its ending, and a
// process that dies writes nothing. With a watch running, its maintenance pass
// runs `yoyo reconcile` and settles such a run; a project run by hand has no
// watch, so the run stood as running until somebody typed `yoyo reconcile`.
// So the commands that read runs, `yoyo status` first among them, find the runs
// whose worker's process is shown to be gone and settle each one through the
// same per-run settlement the sweep uses (reconcileRun), under the run's lease:
// nothing here decides anything about a run the sweep would not decide the
// same way, and a run some live process holds is left exactly as it is.
//
// Only a process shown to be gone is taken (runstate.WorkerReading.ProcessGone):
// it exited, its number belongs to another process now, or the program it ran
// was replaced. A run whose worker cannot be shown either way, or whose program
// is still running, is left to the sweep, because a reading that only waits
// longer is the sweep's to judge and not a status listing's.

import (
	"context"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ProcessGoneRun is a run in flight whose worker's process is shown to be gone.
type ProcessGoneRun struct {
	State runstate.State
	// Says is what showed the process gone, in a sentence a person reads.
	Says string
	// FoundAt is when it was looked at.
	FoundAt time.Time
}

// ProcessGoneRuns lists the runs in flight whose worker's process is shown to be
// gone. It takes nothing, so a caller with nothing to settle pays only for the
// listing and a look at each worker's process.
func ProcessGoneRuns(store *runstate.Store, now time.Time) ([]ProcessGoneRun, error) {
	incomplete, err := store.Incomplete()
	if err != nil {
		return nil, fmt.Errorf("discover runs in flight: %w", err)
	}
	var gone []ProcessGoneRun
	for _, state := range incomplete {
		if !state.Status.InFlight() {
			continue
		}
		reading := store.Worker(state, now)
		if !reading.ProcessGone() {
			continue
		}
		gone = append(gone, ProcessGoneRun{State: state, Says: reading.Says, FoundAt: now})
	}
	return gone, nil
}

// SettleProcessGone settles each run exactly as `yoyo reconcile` settles it:
// under its lease, through the sweep's own settlement, with the sweep's finding
// recorded beside it. results[i] is the settlement of runs[i].
func (r Reconciler) SettleProcessGone(ctx context.Context, runs []ProcessGoneRun) ([]Reconciliation, error) {
	if len(runs) == 0 {
		return nil, nil
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	recorded := make([]runstate.State, len(runs))
	for index, run := range runs {
		recorded[index] = run.State
	}
	defer r.prefetchItems(ctx, recorded)()
	results := make([]Reconciliation, len(recorded))
	for index, state := range recorded {
		results[index] = r.reconcileRun(ctx, state)
	}
	r.recordRunFindings(ctx, recorded, results)
	return results, ctx.Err()
}
