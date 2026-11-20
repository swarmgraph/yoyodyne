package config

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/checks"
)

// Failing tests: how the project's checks name their failing tests, each with
// the file it is in, declared under failing_tests. The reading itself, and what
// it records, is internal/checks/failingtests.go. It describes a failed check
// and decides nothing: a check fails or passes exactly as it would without it.

// failingTestProblems refuses a reading that cannot be used, and one naming a
// check nothing in the configuration runs, which would read nothing and look as
// though it did.
func failingTestProblems(c Config) []string {
	commands := configuredCommands(c)
	var problems []string
	for index, reading := range c.FailingTests {
		where := fmt.Sprintf("failing_tests[%d]", index)
		for _, problem := range reading.Problems() {
			problems = append(problems, where+" "+problem)
		}
		for _, name := range reading.Checks {
			if strings.TrimSpace(name) == "" {
				continue
			}
			matched := false
			for _, command := range commands {
				if checks.ReadingNames(name, command) {
					matched = true
					break
				}
			}
			if !matched {
				problems = append(problems, fmt.Sprintf("%s names %q, which is no configured check: name a check the way checks, landing_checks, document_checks, main_checks, merge_queue_race_check, intent.checks, or path_checks write it, such as %q for \"make test CHANGED_GO_PACKAGES=...\"", where, strings.TrimSpace(name), "make test"))
			}
		}
	}
	return problems
}

// configuredCommands is every check command the configuration runs.
func configuredCommands(c Config) []string {
	var commands []string
	commands = append(commands, c.Checks...)
	commands = append(commands, c.LandingChecks...)
	commands = append(commands, c.DocumentChecks...)
	commands = append(commands, c.MainChecks...)
	commands = append(commands, c.Intent.Checks...)
	if c.MergeQueueRaceCheck != "" {
		commands = append(commands, c.MergeQueueRaceCheck)
	}
	for _, check := range c.PathChecks {
		commands = append(commands, check.Command)
	}
	return commands
}
