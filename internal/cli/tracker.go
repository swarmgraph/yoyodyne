package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func runTracker(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printTrackerUsage(stderr)
		return 2
	}
	switch args[0] {
	case "export":
		return exportTracker(ctx, args[1:], stdout, stderr)
	case "slow-calls":
		return summarizeSlowTrackerCalls(args[1:], stdout, stderr)
	case "unassign":
		return clearLeftoverAssignees(ctx, args[1:], stdout, stderr)
	}
	printTrackerUsage(stderr)
	return 2
}

// summarizeSlowTrackerCalls reads every tracker call the product recorded as
// running out of time and says them together: which callers appear most
// often, how long their calls ran, and what held the store. It asks the
// tracker nothing.
func summarizeSlowTrackerCalls(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("tracker slow-calls", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "tracker slow-calls does not accept positional arguments")
		printTrackerUsage(stderr)
		return 2
	}
	resolved, err := loadConfiguration(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	store, err := runstate.NewTrackerSlowCallStore(stateRoot, resolved.Config.Product.ID)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	calls, err := store.Read()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	for _, line := range runstate.SummarizeSlowCalls(calls).Says() {
		fmt.Fprintln(stdout, line)
	}
	if len(calls) > 0 {
		fmt.Fprintf(stdout, "Each call's own record is in %s.\n", store.Path())
	}
	return 0
}

// clearLeftoverAssignees clears the assignees runs' claims left on unfinished
// items and says what it cleared and what it left, and why
// (orchestrator.AssigneeCleanup). --dry-run says the same without writing.
func clearLeftoverAssignees(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("tracker unassign", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	dryRun := flags.Bool("dry-run", false, "say what would be cleared and change nothing")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "tracker unassign does not accept positional arguments")
		printTrackerUsage(stderr)
		return 2
	}
	resolved, err := loadConfiguration(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	repository, err := resolvePath(config.ProjectDirectory(resolved.Path), resolved.Config.Product.Repository)
	if err != nil {
		fmt.Fprintf(stderr, "resolve product repository: %v\n", err)
		return 1
	}
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	store, err := runstate.NewStore(stateRoot, resolved.Config.Product.ID)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx = beads.OnBehalfOf(ctx, "clearing the assignees runs' claims left on unfinished items")
	cleanup := orchestrator.AssigneeCleanup{
		Tracker: beads.Client{Runner: execution.OSProcessRunner{}, Dir: repository},
		Runs:    store,
	}
	sweep, err := cleanup.Sweep(ctx, !*dryRun)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *jsonOutput {
		if code := writeJSON(stdout, stderr, sweep); code != 0 {
			return code
		}
	} else {
		printAssigneeSweep(stdout, sweep)
	}
	if len(sweep.Problems) > 0 {
		return 1
	}
	return 0
}

// printAssigneeSweep says what the clean-up did, item by item, in three
// groups: cleared, left because a person set the assignee, and left because a
// run holds the item.
func printAssigneeSweep(writer io.Writer, sweep orchestrator.AssigneeSweep) {
	cleared := "Cleared"
	if !sweep.Applied {
		cleared = "Would clear (nothing was changed)"
	}
	for _, group := range []struct {
		heading string
		entries []orchestrator.LeftoverAssignee
	}{
		{cleared, sweep.Cleared},
		{"Left, because a person set the assignee", sweep.ByPerson},
		{"Left, because a run holds the item", sweep.Held},
	} {
		fmt.Fprintf(writer, "%s: %d item(s)\n", group.heading, len(group.entries))
		for _, entry := range group.entries {
			fmt.Fprintf(writer, "  %s (%s) %s: assignee %s; %s\n", entry.WorkItemID, entry.Status, entry.Title, entry.Assignee, entry.Because)
		}
	}
	for _, problem := range sweep.Problems {
		fmt.Fprintf(writer, "Not cleared: %s\n", problem)
	}
}

// exportTracker takes a fresh export of the tracker into a checkout. It reads
// no configuration, because what asks for it is the repository's pre-commit
// hook, in whichever checkout the commit is being made in, and that checkout's
// export is the one the commit carries.
func exportTracker(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("tracker export", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dir := flags.String("dir", ".", "the checkout whose export is taken (default: this directory)")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "tracker export does not accept positional arguments")
		printTrackerUsage(stderr)
		return 2
	}
	checkout, err := filepath.Abs(*dir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	client := beads.Client{Runner: execution.OSProcessRunner{}, Dir: checkout}
	// Named so a call that does not answer in time says who it was for.
	ctx = beads.OnBehalfOf(ctx, "the export taken before a commit that carries it")
	if err := client.ExportBeforeCommit(ctx); err != nil {
		fmt.Fprintf(stderr, "export the tracker into %s: %v\n", filepath.Join(checkout, beads.ExportPath), err)
		return 1
	}
	fmt.Fprintf(stdout, "exported the tracker into %s\n", filepath.Join(checkout, beads.ExportPath))
	return 0
}

func printTrackerUsage(writer io.Writer) {
	fmt.Fprintln(writer, strings.TrimSpace(`
Usage: yoyo tracker export [options]
       yoyo tracker slow-calls [--config <path>]
       yoyo tracker unassign [--config <path>] [--dry-run] [--json]

export takes a fresh export of the tracker into this checkout's
.beads/issues.jsonl. The repository's pre-commit hook runs it before every
commit that carries the export, so what is committed is never older than the
tracker. The harness otherwise exports on its own, at most every five minutes
when the tracker changed, and no tracker write exports by itself.

slow-calls reads every tracker call the product recorded as running out of
time and says them together: which callers appear most often, how long their
calls ran, and what held the tracker's store while they waited. It asks the
tracker nothing.

unassign clears the assignee on every unfinished item where a run's
claim set it and no run is working on the item now, and says what it cleared
and what it left: an assignee on an item no run ever claimed was set by a
person and is left, and so is any item a run holds. Every run now clears its
own claim's assignee when it gives the item back; this clears the ones left
from before it did, and is safe to run again. --dry-run says what it would
clear and changes nothing.

Options for export:
  --dir <path>      the checkout whose export is taken (default: this directory)`))
}
