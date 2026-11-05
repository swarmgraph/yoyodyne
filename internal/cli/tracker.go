package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
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

export takes a fresh export of the tracker into this checkout's
.beads/issues.jsonl. The repository's pre-commit hook runs it before every
commit that carries the export, so what is committed is never older than the
tracker. The harness otherwise exports on its own, at most every five minutes
when the tracker changed, and no tracker write exports by itself.

slow-calls reads every tracker call the product recorded as running out of
time and says them together: which callers appear most often, how long their
calls ran, and what held the tracker's store while they waited. It asks the
tracker nothing.

Options for export:
  --dir <path>      the checkout whose export is taken (default: this directory)`))
}
