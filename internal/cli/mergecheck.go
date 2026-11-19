package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// mergeCheck is `yoyo merge-check`: the check the harness makes after each
// landing through a pull request (orchestrator/mergequeueconfirm.go), made over
// the landings already on the target branch since a time. It prints each
// landing and how it came out, and files each difference as a critical report
// exactly as the check after a landing does.
func mergeCheck(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("merge-check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	since := flags.String("since", "", "check each landing after this time, as RFC 3339 with its zone (required)")
	ref := flags.String("ref", "", "the target branch as this repository holds it (default: the remote's copy of main)")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 || *since == "" {
		printMergeCheckUsage(stderr)
		return 2
	}
	from, err := time.Parse(time.RFC3339, *since)
	if err != nil {
		fmt.Fprintf(stderr, "merge-check: --since %q is not an RFC 3339 time with its zone: %v\n", *since, err)
		return 2
	}
	parts, err := buildComponents(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "merge-check failed: %v\n", err)
		return 1
	}
	target := *ref
	if target == "" {
		target = parts.config.Execution.Remote + "/main"
	}
	manager, err := gitworktree.New(gitworktree.Options{
		Runner:         parts.runner,
		RepositoryRoot: parts.repository,
		WorktreeRoot:   parts.worktreeRoot,
		Remote:         parts.config.Execution.Remote,
	})
	if err != nil {
		fmt.Fprintf(stderr, "merge-check failed: %v\n", err)
		return 1
	}
	reports, err := runstate.NewReportStore(parts.stateRoot, parts.config.Product.ID)
	if err != nil {
		fmt.Fprintf(stderr, "merge-check failed: %v\n", err)
		return 1
	}
	pipeline := orchestrator.Pipeline{Config: parts.config, Reports: reports, Build: buildinfo.Commit()}
	states, err := parts.store.Recorded()
	if err != nil {
		fmt.Fprintf(stderr, "merge-check failed: read the runs' records: %v\n", err)
		return 1
	}
	entries, auditErr := pipeline.AuditLandedMerges(ctx, manager, orchestrator.RunLandings(states), target, from)
	if entries == nil && auditErr != nil {
		fmt.Fprintf(stderr, "merge-check failed: %v\n", auditErr)
		return 1
	}
	differing := 0
	for _, entry := range entries {
		fmt.Fprintln(stdout, entry.Says())
		if entry.Unchecked == "" && !entry.Matches {
			differing++
		}
	}
	fmt.Fprintf(stdout, "%d landings on %s since %s checked; %d differ", len(entries), target, from.Format(time.RFC3339), differing)
	if differing > 0 && auditErr == nil {
		fmt.Fprint(stdout, " and each is filed as a critical report")
	}
	fmt.Fprintln(stdout)
	if auditErr != nil {
		fmt.Fprintf(stderr, "merge-check: a report could not be filed, so the differences above are not on the record: %v\n", auditErr)
		return 1
	}
	return 0
}

func printMergeCheckUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo merge-check --since <time> [--ref <branch>] [--config <path>]

Checks each landing through a pull request on the target branch since a time,
the way the harness checks one right after it lands: that the merge commit's
first parent is the base the candidate was checked on, and that the merge
changed exactly the files the candidate changed. Each difference is filed as one
critical report naming the merge. Prints every landing and how it came out.

A merge queue candidate is checked against the base it was built on, and any
other pull request against the base and change its run's review recorded. A
landing the harness recorded nothing for, such as a pull request a person
opened, is listed as not checked, with the reason. It reads Git in this repository, so fetch first to
include landings the local copy of the remote does not have.

Options:
  --since <time>    check each landing after this time, as RFC 3339 with its zone
  --ref <branch>    the target branch as this repository holds it (default: the
                    remote's main)
  --config <path>   configuration file path (default: the nearest project
                    configuration)`)
}
