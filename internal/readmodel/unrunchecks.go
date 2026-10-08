package readmodel

// A configured check that keeps saying it could not run.
//
// A check that could not run (checks.CouldNotRunPrefix) stops nothing and
// spends nothing, which is right for one change and wrong for a long gap: every
// change in it lands without the check, and each run says so only on itself.
// So once the same check has been unable to run on several changes in a row it
// is an entry on the attention line, naming the check, how many changes, and
// since when, read from the runs' own records of their check stages. The first
// change the check runs on ends it, and so does changing the check's command or
// taking it out of the configuration, because a check is known by its command.

import (
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// DefaultCouldNotRunBeforeStatus is how many changes in a row a check has to
// have been unable to run on before the attention line says so, where the
// configuration names none. It matches
// execution.could_not_run_before_status's default.
const DefaultCouldNotRunBeforeStatus = 3

// UnrunCheck is one configured check that could not run on the latest
// Changes runs that reached it: the check, how many, since when, and the
// latest reason it gave.
type UnrunCheck struct {
	Command string `json:"command"`
	// Changes is how many runs in a row the check could not run on, counted
	// back to the last run it ran on. A run whose check stage never reached the
	// check neither counts nor ends the count.
	Changes int `json:"changes"`
	// Since is when the check stage of the first of those runs began, and
	// LatestAt when the latest one's did; LatestRunID is that latest run.
	Since       time.Time `json:"since"`
	LatestAt    time.Time `json:"latest_at"`
	LatestRunID string    `json:"latest_run_id"`
	// Reason is what the check said on the latest of those runs.
	Reason string `json:"reason"`
}

// Says is the entry as a sentence: the check, how many changes, since when,
// and why, by the latest run's account.
func (u UnrunCheck) Says() string {
	return fmt.Sprintf("the check %s could not run on %d changes in a row, since %s, so each of them went on without it; latest reason: %s",
		u.Command, u.Changes, localMoment(u.Since), singleLine(u.Reason, maxRefusalBytes))
}

// UnrunChecksOf reads, from the runs' records, every check that could not run
// on at least threshold changes in a row. Each run counts once, by the last
// check stage it recorded, in the order those stages began. A threshold under
// one takes DefaultCouldNotRunBeforeStatus. Where configured is not nil, only
// the checks still among those commands are said: one changed or taken out of
// the configuration will not run again under its old command, and nothing is
// left to settle about it. The result is in command order.
func UnrunChecksOf(states []runstate.State, threshold int, configured []string) []UnrunCheck {
	if threshold < 1 {
		threshold = DefaultCouldNotRunBeforeStatus
	}
	stages := make([]runstate.State, 0, len(states))
	for _, state := range states {
		if state.CheckStage != nil && !state.CheckStage.Running() {
			stages = append(stages, state)
		}
	}
	sort.SliceStable(stages, func(first, second int) bool {
		return stages[first].CheckStage.StartedAt.Before(stages[second].CheckStage.StartedAt)
	})
	streaks := map[string]*UnrunCheck{}
	for _, state := range stages {
		stage := state.CheckStage
		for _, command := range stage.Ran {
			delete(streaks, command)
		}
		for _, unrun := range stage.CouldNotRun {
			streak := streaks[unrun.Command]
			if streak == nil {
				streak = &UnrunCheck{Command: unrun.Command, Since: stage.StartedAt}
				streaks[unrun.Command] = streak
			}
			streak.Changes++
			streak.LatestAt = stage.StartedAt
			streak.LatestRunID = state.RunID
			streak.Reason = unrun.Reason
		}
	}
	var unrun []UnrunCheck
	for _, streak := range streaks {
		if configured != nil && !slices.Contains(configured, streak.Command) {
			continue
		}
		if streak.Changes >= threshold {
			unrun = append(unrun, *streak)
		}
	}
	sort.Slice(unrun, func(first, second int) bool { return unrun[first].Command < unrun[second].Command })
	return unrun
}

// ReadUnrunChecks reads the runs' records and derives the checks that could
// not run on several changes in a row. A reading with no runs wired says
// nothing; records that cannot be read say so rather than reporting every check
// as running.
func ReadUnrunChecks(sources Sources) ([]UnrunCheck, string) {
	if sources.Runs == nil {
		return nil, ""
	}
	states, err := sources.Runs.Recorded()
	if err != nil && len(states) == 0 {
		return nil, fmt.Sprintf("the runs' check stages could not be read for checks that could not run: %v", err)
	}
	var problem string
	if err != nil {
		problem = fmt.Sprintf("the runs' check stages could only be read in part for checks that could not run: %v", err)
	}
	return UnrunChecksOf(states, sources.CouldNotRunBeforeStatus, sources.GateChecks), problem
}

// unrunCheckAttention is a check that keeps being unable to run, as the
// attention line carries it. It is the development manager's: the check
// running again needs whatever it lacks put where the harness runs its checks,
// or the check changed, and deciding which — and getting it done — is the run
// reliability she already decides about. It is not the operator's, because
// nothing about it is on the closed list of what only a person can do.
func unrunCheckAttention(unrun UnrunCheck) Attention {
	return Attention{Kind: AttentionUnrunCheck, ID: unrun.Command, Mover: MoverDevelopmentManager, UnrunCheck: &unrun}
}
