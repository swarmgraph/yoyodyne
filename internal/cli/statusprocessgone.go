package cli

// Settling the runs whose process is gone before a command reads runs.
//
// A project run by hand has no watch, and so nothing running `yoyo reconcile`:
// a run whose process died stood as "running" until somebody typed it. So every
// command that reads runs looks at the runs in flight first, and each one whose
// worker's process is shown to be gone is settled exactly as `yoyo reconcile`
// settles it (orchestrator.Reconciler.SettleProcessGone). `yoyo status` does it
// itself and says so at the top of its answer; every other such command has it
// done for it by the dispatcher (settleBeforeCommand) and says so on standard
// error, so its own output is unchanged. The tracker export a commit's hook runs
// is the exception: it runs inside a commit and writes nothing
// (subcommandsNeverSettling). Everything else the sweep does — the
// forge, the docket build, convergence — is still the sweep's.

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

// commandsSettlingFirst are the commands, besides `yoyo status`, that read runs
// and have the runs whose process is gone settled before they do.
//
// Left out are the commands that act on a run themselves — `run`, `resume`,
// `stop`, `work`, and `start` — because settling first would change what they
// were asked to do: `yoyo run` on an item whose run died continues that run, and
// `yoyo stop` on one ends it cancelled, where a settlement would have ended it
// first. `reconcile` is the sweep this settlement is a part of, and `status`
// settles for itself. Commands that never read runs, such as `goals guard`,
// which runs before every shell command an agent makes, are left out so they
// cost nothing more than they did. A subcommand that runs inside a Git commit
// settles nothing either (subcommandsNeverSettling).
var commandsSettlingFirst = map[string]bool{
	"agent":        true,
	"chat":         true,
	"conformance":  true,
	"cost":         true,
	"dashboard":    true,
	"doctor":       true,
	"epic":         true,
	"exchange":     true,
	"forge-check":  true,
	"gate":         true,
	"intervention": true,
	"invocations":  true,
	"landing":      true,
	"merge-check":  true,
	"model-trial":  true,
	"project":      true,
	"pull-request": true,
	"queue":        true,
	"slack":        true,
	"tracker":      true,
	"triage":       true,
}

// subcommandsNeverSettling are the subcommands of a command settling first that
// run inside a Git commit, and so settle nothing.
//
// `yoyo tracker export` is what the repository's pre-commit hook runs
// (.beads/hooks/pre-commit), and it only reads the tracker. Settling a run
// writes the tracker, and a write waits on that item's write lock; a process
// that holds the lock while it commits would have its own commit's hook wait
// for it until the hook's time limit refused the commit. So the export settles
// nothing, and the runs it would have settled are settled by the next command
// that reads runs outside a commit.
var subcommandsNeverSettling = map[string]map[string]bool{
	"tracker": {"export": true},
}

// settleBeforeCommand settles the runs whose process is gone before a command
// that reads runs is carried out. A command asked only for its help, or one
// whose project cannot be found, is carried out exactly as before: the command
// says itself what it cannot find.
func settleBeforeCommand(ctx context.Context, args []string, stderr io.Writer) {
	if len(args) == 0 || !commandsSettlingFirst[args[0]] || asksForHelp(args[1:]) {
		return
	}
	if len(args) > 1 && subcommandsNeverSettling[args[0]][args[1]] {
		return
	}
	configPath := configFlagOf(args[1:])
	store, _, err := recordedRunStore(configPath)
	if err != nil {
		return
	}
	settleRunsWithNoProcessAside(ctx, configPath, store, stderr)
}

// asksForHelp reports arguments that ask a command for its usage.
func asksForHelp(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "help", "-h", "-help", "--help":
			return true
		}
	}
	return false
}

// configFlagOf is the configuration a command was pointed at with --config,
// wherever among its arguments it was given, and empty for the nearest project
// configuration, which is what the command itself reads then.
func configFlagOf(args []string) string {
	for index, arg := range args {
		for _, name := range []string{"--config", "-config"} {
			if arg == name && index+1 < len(args) {
				return args[index+1]
			}
			if value, found := strings.CutPrefix(arg, name+"="); found {
				return value
			}
		}
	}
	return ""
}

// settleRunsWithNoProcessAside is the settlement for a command whose standard
// output is its own answer: what it settled is said on standard error, so that
// answer and its JSON stay as they are. The status modes that list finished
// work (--shipped, --checks) use it too.
func settleRunsWithNoProcessAside(ctx context.Context, configPath string, store *runstate.Store, stderr io.Writer) {
	settlements, failure := settleRunsWithNoProcess(ctx, configPath, store, time.Now())
	printProcessGone(stderr, settlements)
	if failure != "" {
		fmt.Fprintln(stderr, failure)
	}
}

// printProcessGone says each run found with no process behind it, when, and
// what became of it, ahead of everything else the status says.
func printProcessGone(writer io.Writer, settlements []processGoneSettlement) {
	for _, settled := range settlements {
		fmt.Fprintf(writer, "Run %s for %s had no process behind it: %s. This was found at %s.\n",
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
