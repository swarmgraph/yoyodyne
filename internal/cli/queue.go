package cli

// `yoyo queue`: moving a queued change between the harness's merge queue and
// the forge's own. Reading the queues is `yoyo status`, which shows each queue
// beside everything else the harness is doing from the shared read model.
//
// A transfer is the one thing that moves an entry between modes. Turning
// execution.merge_queue off does not, and neither does the forge answering
// differently later: an entry drains in the mode it was admitted in unless a
// person asks for this. What the transfer does is the orchestrator's
// (mergequeuetransfer.go): the old mode's merge is withdrawn and the withdrawal
// confirmed first, the change is admitted again in the place it had, and its
// candidate is built and verified afresh.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func runQueue(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printQueueUsage(stdout)
		return 0
	}
	switch args[0] {
	case "transfer":
		return transferQueueEntry(ctx, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown queue command %q\n\n", args[0])
		printQueueUsage(stderr)
		return 2
	}
}

func printQueueUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo queue transfer <entry> --mode <harness|forge> [--config <path>] [--json]

Moves one queued change between the harness's merge queue and the forge's own.
The entry is named as `+"`yoyo status`"+` names it (mqe-...).

Whatever the change's current queue was asked for is withdrawn first, and the
withdrawal has to be confirmed: while the forge has not said it took a queued
merge back, the change may still land, and nothing is moved. Once it is
confirmed, the change is admitted again in the other queue in the place it had,
and its candidate is built, checked and reviewed again from nothing. Asking
again after an interruption finishes the same transfer rather than making a
second one.

The forge's queue is moved to only where it enforces everything the harness's
queue gates on, exactly as at admission. Turning execution.merge_queue off moves
nothing: what is queued drains in the queue it was admitted to.`)
}

type queueTransferOutput struct {
	Entry *runstate.MergeQueueEntry `json:"entry,omitempty"`
	Error string                    `json:"error,omitempty"`
}

func transferQueueEntry(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("queue transfer", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	mode := flags.String("mode", "", "the queue to move the change to: harness or forge")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 || *mode == "" {
		fmt.Fprintln(stderr, "queue transfer takes one entry and --mode harness or --mode forge")
		return 2
	}
	fail := func(err error) int {
		if *jsonOutput {
			if code := writeJSON(stdout, stderr, queueTransferOutput{Error: err.Error()}); code != 0 {
				return code
			}
		} else {
			fmt.Fprintln(stderr, err)
		}
		return 1
	}
	parts, err := buildComponents(*path)
	if err != nil {
		return fail(err)
	}
	key, found, err := queueHolding(parts.mergeQueues, positional[0])
	if err != nil {
		return fail(err)
	}
	if !found {
		return fail(fmt.Errorf("no merge queue of this product admits an entry %s", positional[0]))
	}
	driver := mergeQueueDriverFrom(parts)
	moved, err := driver.Promoter.Transfer(ctx, key, positional[0], runstate.MergeQueueMode(*mode))
	if errors.Is(err, orchestrator.ErrMergeQueueWorkerBusy) {
		return fail(errors.New("another process is working this queue right now; ask again once its pass ends"))
	}
	if err != nil {
		return fail(err)
	}
	if *jsonOutput {
		return writeJSON(stdout, stderr, queueTransferOutput{Entry: &moved})
	}
	fmt.Fprintf(stdout, "%s: %s is now entry %d (%s) in the %s queue for %s, in the place it had; its candidate is built and verified afresh\n",
		moved.WorkItemID, moved.WorkItemTitle, moved.Order, moved.EntryID, moved.Mode, moved.TargetBranch)
	return 0
}

// queueHolding is the queue an entry was admitted to.
func queueHolding(store *runstate.MergeQueueStore, entryID string) (runstate.MergeQueueKey, bool, error) {
	keys, err := store.Keys()
	if err != nil {
		return runstate.MergeQueueKey{}, false, err
	}
	for _, key := range keys {
		entries, err := store.Entries(key)
		if err != nil {
			return runstate.MergeQueueKey{}, false, err
		}
		for _, entry := range entries {
			if entry.EntryID == entryID {
				return key, true, nil
			}
		}
	}
	return runstate.MergeQueueKey{}, false, nil
}
