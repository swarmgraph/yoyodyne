package cli

// `yoyo status` settling the runs whose process is gone before it reads them.
//
// A project run by hand has no watch, and so nothing running `yoyo reconcile`:
// a run whose process died stood as "running" until somebody typed it. So the
// status reads the runs in flight first, and each one whose worker's process is
// shown to be gone is settled exactly as `yoyo reconcile` settles it
// (orchestrator.Reconciler.SettleProcessGone), and said at the top of the answer
// with when it was found. Everything else the sweep does — the forge, the
// docket build, convergence — is still the sweep's.

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// statusReconciler builds what settles a run whose process is gone. It is a
// variable so a test can give the settlement a tracker of its own.
var statusReconciler = buildReconciler

// processGoneSettlement is one run `yoyo status` found with no process behind
// it, and what settling it did.
type processGoneSettlement struct {
	RunID      string    `json:"run_id"`
	WorkItemID string    `json:"work_item_id"`
	ProcessWas string    `json:"process_was"`
	FoundAt    time.Time `json:"found_at"`
	Action     string    `json:"action"`
	Detail     string    `json:"detail,omitempty"`
	Failure    string    `json:"failure,omitempty"`
}

// settleRunsWithNoProcess finds the runs whose process is gone and settles them.
// It never fails the status: a run it could not settle is said, and the listing
// below still reads every run as it stands.
func settleRunsWithNoProcess(ctx context.Context, configPath string, store *runstate.Store, now time.Time) ([]processGoneSettlement, string) {
	gone, err := orchestrator.ProcessGoneRuns(store, now)
	if err != nil {
		return nil, fmt.Sprintf("the runs whose process is gone could not be looked for: %v", err)
	}
	if len(gone) == 0 {
		return nil, ""
	}
	reconciler, err := statusReconciler(configPath)
	if err != nil {
		return nil, fmt.Sprintf("%d run(s) have no process behind them and could not be settled, because what settles them could not be set up: %v; `yoyo reconcile` settles them", len(gone), err)
	}
	results, err := reconciler.SettleProcessGone(ctx, gone)
	settlements := make([]processGoneSettlement, 0, len(gone))
	for index, run := range gone {
		settlement := processGoneSettlement{
			RunID:      run.State.RunID,
			WorkItemID: run.State.WorkItemID,
			ProcessWas: run.Says,
			FoundAt:    run.FoundAt,
		}
		if index < len(results) {
			settlement.Action = string(results[index].Action)
			settlement.Detail = results[index].Detail
			settlement.Failure = results[index].Failure
		}
		settlements = append(settlements, settlement)
	}
	if err != nil {
		return settlements, fmt.Sprintf("settling the runs whose process is gone did not finish: %v", err)
	}
	return settlements, ""
}

// settleRunsWithNoProcessAside is the same settlement for the status modes whose
// standard output is a listing of finished work (--shipped, --checks): what it
// settled is said on standard error, so that listing and its JSON stay as they
// are.
func settleRunsWithNoProcessAside(configPath string, store *runstate.Store, stderr io.Writer) {
	settlements, failure := settleRunsWithNoProcess(context.Background(), configPath, store, time.Now())
	printProcessGone(stderr, settlements)
	if failure != "" {
		fmt.Fprintln(stderr, failure)
	}
}

// printProcessGone says each run found with no process behind it, when, and
// what became of it, ahead of everything else the status says.
func printProcessGone(writer io.Writer, settlements []processGoneSettlement) {
	for _, settled := range settlements {
		fmt.Fprintf(writer, "Run %s for %s had no process behind it: %s. This status found that at %s.\n",
			settled.RunID, settled.WorkItemID, strings.TrimSuffix(settled.ProcessWas, "."), settled.FoundAt.Local().Format("2006-01-02 15:04 MST"))
		switch {
		case settled.Failure != "":
			fmt.Fprintf(writer, "  It could not be settled: %s. `yoyo reconcile` tries again.\n", settled.Failure)
		case settled.Action == string(orchestrator.ActionHeld):
			fmt.Fprintln(writer, "  A live process took the run up before it could be settled, so it was left alone.")
		case settled.Action == string(orchestrator.ActionResumable):
			fmt.Fprintf(writer, "  It was left as it stands, as `yoyo reconcile` would leave it: %s.\n", strings.TrimSuffix(settled.Detail, "."))
		case settled.Action == string(orchestrator.ActionBlocked):
			fmt.Fprintf(writer, "  The run was ended as `yoyo reconcile` ends it, and its work item now waits on the development manager: %s.\n", strings.TrimSuffix(settled.Detail, "."))
			fmt.Fprintln(writer, "  Its process going away was outside the work, so no repair attempt was charged, and its branch and worktree were left as they were.")
		default:
			fmt.Fprintf(writer, "  The run was settled as `yoyo reconcile` settles it (%s): %s.\n", settled.Action, strings.TrimSuffix(settled.Detail, "."))
		}
	}
	if len(settlements) > 0 {
		fmt.Fprintln(writer)
	}
}
