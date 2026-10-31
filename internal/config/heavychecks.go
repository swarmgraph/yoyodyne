package config

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// Heavy checks: the configured checks only so many of may run at once on one
// machine, execution.heavy_checks.
//
// A heavy check is named the way the check lists name it — "make race" — and
// a configured command is that check where it is the name, or begins with the
// name and then a space: "make race RACE_PACKAGES=..." is "make race". The
// longest name that matches is the one a command counts under. A command no
// name matches is not heavy and starts as soon as its turn in its own stage
// comes, which is every check of a project that names none.
//
// It selects how many may run at once and nothing else: a heavy check runs
// exactly what it would have run, under the same budget and the same reviewer,
// once it starts. Its budget and its stage's bound start when it launches, never
// while it waits for a place.

// DefaultHeavyCheckLimit is how many of a heavy check may run at once where the
// configuration names the check and gives no number.
const DefaultHeavyCheckLimit = 1

// HeavyCheck is the heavy check a command counts under, and how many of it may
// run at once on this machine; heavy is false for a command no name matches.
func (e Execution) HeavyCheck(command string) (name string, limit int, heavy bool) {
	command = strings.TrimSpace(command)
	for candidate, places := range e.HeavyChecks {
		key := strings.TrimSpace(candidate)
		if !heavyCheckMatches(key, command) || len(key) <= len(name) {
			continue
		}
		name, limit, heavy = key, places, true
	}
	if heavy && limit < 1 {
		limit = DefaultHeavyCheckLimit
	}
	return name, limit, heavy
}

func heavyCheckMatches(name, command string) bool {
	if name == "" || !strings.HasPrefix(command, name) {
		return false
	}
	rest := command[len(name):]
	return rest == "" || unicode.IsSpace(rune(rest[0]))
}

// heavyCheckProblems refuses a heavy check that names no configured check, and
// a number of places that would let none run. A name nothing matches would
// limit nothing and read as though it did.
func heavyCheckProblems(c Config) []string {
	var commands []string
	commands = append(commands, c.Checks...)
	commands = append(commands, c.LandingChecks...)
	commands = append(commands, c.DocumentChecks...)
	for _, check := range c.PathChecks {
		commands = append(commands, check.Command)
	}
	for _, step := range c.Install {
		commands = append(commands, step.Run)
	}
	names := make([]string, 0, len(c.Execution.HeavyChecks))
	for name := range c.Execution.HeavyChecks {
		names = append(names, name)
	}
	sort.Strings(names)
	var problems []string
	for _, name := range names {
		key := strings.TrimSpace(name)
		if key == "" {
			problems = append(problems, "execution.heavy_checks names a check with no name")
			continue
		}
		if places := c.Execution.HeavyChecks[name]; places < 1 {
			problems = append(problems, fmt.Sprintf("execution.heavy_checks gives %q %d places; at least one has to be able to run, and a check named with no number gets %d", key, places, DefaultHeavyCheckLimit))
		}
		matched := false
		for _, command := range commands {
			if heavyCheckMatches(key, strings.TrimSpace(command)) {
				matched = true
				break
			}
		}
		if !matched {
			problems = append(problems, fmt.Sprintf("execution.heavy_checks names %q, which is no configured check: name a check the way checks, landing_checks, document_checks, path_checks, or install write it, such as %q for \"make race RACE_PACKAGES=...\"", key, "make race"))
		}
	}
	return problems
}
