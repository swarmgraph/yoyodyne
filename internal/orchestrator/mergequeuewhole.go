package orchestrator

// The merge queue standing in for a run's whole check of its merge candidate.
//
// A run whose checks before review were narrowed checks its approved change
// over the whole module before it is promoted (checkCandidateWhole), except
// where the change goes to the harness's own merge queue: the queue checks the
// candidate it lands over the whole module, so the run leaves that pass to it
// (runstate.ChecksPassed.WholeLeftToQueue). Three things keep that safe, and
// none of them stops the queue:
//
//   - The queue runs every check the run left to it. Its check set for the
//     entry is the configured checks and, where they no longer name one of the
//     run's narrowed checks because they changed after admission, that check
//     too (leftToQueue, Pipeline.mergeQueueChecks). The gate a candidate lands
//     on (runstate's MergeQueueGeneration.Gate) requires every check in that
//     set to have run on that exact candidate and passed, rather than failed,
//     stopped, or said it could not run; a candidate that fails is handed back
//     the way any failing candidate is. A path check is the exception: it joins
//     a run's checks for what the change touches, is told nothing narrower than
//     it always runs over, and the queue never runs one, so its result before
//     review is already its whole one.
//   - Such a change is admitted to the harness's queue or not at all. Where
//     admission would choose the forge's own queue, which lands a commit this
//     harness never checks, nothing is admitted: the run checks the change
//     whole on this machine first and is then admitted as any change is
//     (promoteApproved). An explicit transfer to the forge's queue is refused
//     for the same reason (MergeQueuePromoter.Transfer).
//   - Once the change lands, the run's record names the queue's check that
//     stood in: the entry, the generation, and the commit checked, which is
//     the commit that landed (mergeQueueWholeBy).
//
// mergeQueueStandsIn reads the same conditions at the promotion, once more,
// from the run's record and the generation the promotion intends to land. With
// the two above holding, nothing reaches it uncovered; it is there so that a
// record nothing current writes — an entry moved to the forge's queue before
// transfers refused it, or a run record that could not be read when the
// candidate's checks were chosen — lands nothing on a check that did not cover
// it, and the queue's pass stops naming what was not shown.

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// mergeQueueStandsIn is why the queue's check of generation does not stand in
// for the whole check run left to it, and empty where it does or the run left
// it nothing. mode is the entry's mode, and pathChecks the project's path
// checks, which the queue does not run.
func mergeQueueStandsIn(run runstate.State, mode runstate.MergeQueueMode, generation runstate.MergeQueueGeneration, pathChecks []config.PathCheck) string {
	passed := run.ChecksPassed
	if passed == nil || !passed.WholeLeftToQueue {
		return ""
	}
	if mode == runstate.MergeQueueForge {
		return fmt.Sprintf("run %s left checking its change over the whole module to this harness's merge queue, and the change is now in the forge's own merge queue, which lands a commit this harness never checks; it lands once it is back in the harness's queue, or is checked whole by its run", run.RunID)
	}
	checked := generation.CheckRun
	if checked == nil || checked.FinishedAt == nil || checked.Problem != "" || checked.Binding != generation.Binding() {
		return fmt.Sprintf("run %s left checking its change over the whole module to this harness's merge queue, and generation %d has no finished check of its own candidate", run.RunID, generation.Number)
	}
	results := make(map[string]runstate.MergeQueueCheckResult, len(checked.Results))
	for _, result := range checked.Results {
		results[result.Command] = result
	}
	wanted := leftToQueue(run, nil, pathChecks)
	var unmet []string
	for _, command := range wanted {
		result, ran := results[command]
		switch {
		case !ran:
			unmet = append(unmet, command+" is not among the queue's checks")
		case result.CouldNotRun != "":
			unmet = append(unmet, command+" could not run: "+result.CouldNotRun)
		case !result.Passed:
			unmet = append(unmet, command+" did not pass")
		}
	}
	if len(unmet) > 0 {
		return fmt.Sprintf("run %s left checking its change over the whole module to this harness's merge queue, and the queue's check of generation %d's candidate %s does not cover it: %s",
			run.RunID, generation.Number, shortCommit(generation.Candidate), strings.Join(unmet, "; "))
	}
	return ""
}

// mergeQueueWholeBy is what a landed run that left its whole check to the
// queue records as the check that stood in for it: the entry, the generation,
// and the commit checked, which is the commit that landed. It is empty for a
// landing through the forge's own queue, whose commit this harness did not
// check, and for a completion that names no generation.
func mergeQueueWholeBy(entry runstate.MergeQueueEntry, completion runstate.MergeQueueCompletion) string {
	if completion.Path == runstate.MergeQueueLandThroughForgeQueue || completion.Generation == 0 || completion.Landed == "" {
		return ""
	}
	return fmt.Sprintf("the merge queue's checks of entry %d (%s), generation %d: every check the queue ran, the ones this run narrowed among them, passed told the whole module on candidate %s, the commit that landed on %s",
		entry.Order, entry.EntryID, completion.Generation, completion.Landed, entry.TargetBranch)
}

// leftToQueue is the checks a run left to the merge queue's whole check of its
// candidate that are not among have: every check its narrowed evidence names,
// those that could not run before review included, in the order it ran them,
// with its path checks left out. A run that checked its change whole itself
// left nothing.
func leftToQueue(run runstate.State, have []string, pathChecks []config.PathCheck) []string {
	passed := run.ChecksPassed
	if passed == nil || !passed.WholeLeftToQueue {
		return nil
	}
	skip := make(map[string]bool, len(have)+len(pathChecks))
	for _, command := range have {
		skip[command] = true
	}
	for _, check := range pathChecks {
		skip[check.Command] = true
	}
	commands := append([]string(nil), passed.Commands...)
	for _, unrun := range passed.CouldNotRun {
		commands = append(commands, unrun.Command)
	}
	var left []string
	for _, command := range commands {
		if !skip[command] {
			skip[command] = true
			left = append(left, command)
		}
	}
	return left
}
