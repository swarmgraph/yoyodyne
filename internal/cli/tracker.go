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
)

func runTracker(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "export" {
		printTrackerUsage(stderr)
		return 2
	}
	return exportTracker(ctx, args[1:], stdout, stderr)
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

Take a fresh export of the tracker into this checkout's .beads/issues.jsonl.
The repository's pre-commit hook runs it before every commit that carries the
export, so what is committed is never older than the tracker. The harness
otherwise exports on its own, at most every five minutes when the tracker
changed, and no tracker write exports by itself.

Options:
  --dir <path>      the checkout whose export is taken (default: this directory)`))
}
