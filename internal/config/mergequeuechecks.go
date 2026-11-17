package config

import (
	"fmt"
	"slices"
	"strings"
)

// mergeQueueCheckProblems is what is wrong with the checks the harness's merge
// queue is told to run. Every command it names must be one of Checks, written
// the same way, so a project cannot have the queue run what its own changes were
// never held to; the race check is the one the queue adds rather than lists, so
// it is refused on the list it is meant to stay off.
func (c Config) mergeQueueCheckProblems() []string {
	var problems []string
	seen := map[string]bool{}
	for index, check := range c.MergeQueueChecks {
		trimmed := strings.TrimSpace(check)
		switch {
		case trimmed == "":
			problems = append(problems, fmt.Sprintf("merge queue check %d cannot be empty", index))
		case !slices.Contains(c.Checks, check):
			problems = append(problems, fmt.Sprintf("merge_queue_checks names %q, which is no configured check: name a command exactly as checks writes it", check))
		case seen[check]:
			problems = append(problems, fmt.Sprintf("merge_queue_checks names %q more than once", check))
		}
		seen[check] = true
	}
	if race := c.MergeQueueRaceCheck; race != "" {
		switch {
		case !slices.Contains(c.Checks, race):
			problems = append(problems, fmt.Sprintf("merge_queue_race_check names %q, which is no configured check: name a command exactly as checks writes it", race))
		case len(c.MergeQueueChecks) == 0:
			problems = append(problems, "merge_queue_race_check needs merge_queue_checks: without a declared list the queue already runs every configured check whole")
		case seen[race]:
			problems = append(problems, fmt.Sprintf("merge_queue_race_check names %q, which merge_queue_checks already lists; leave it off that list so the queue adds it only where the approved head has no passing result", race))
		}
	}
	return problems
}

// DeclaresMergeQueueChecks reports whether the project names the checks its
// merge queue runs; where it does not, the queue runs every configured check
// whole.
func (c Config) DeclaresMergeQueueChecks() bool { return len(c.MergeQueueChecks) > 0 }
