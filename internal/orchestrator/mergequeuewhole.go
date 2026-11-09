package orchestrator

// The merge queue standing in for a run's whole check of its merge candidate.
//
// A run whose checks before review were narrowed checks its approved change
// over the whole module before it is promoted (checkCandidateWhole), except
// where the change goes to the harness's own merge queue: the queue checks the
// candidate it lands over the whole module, so the run leaves that pass to it
// (runstate.ChecksPassed.WholeLeftToQueue). That is safe only where the queue's
// check is shown to be the whole pass the run did not make, so the queue lands
// such a change only where it is shown, and says on the run which of its
// checks it was once the change lands:
//
//   - the queue's own checks ran on the candidate it lands, which the forge's
//     own queue does not do, so a change in that queue is not landed;
//   - every check the run's narrowed evidence names, those that could not run
//     before review included, ran there to its own end and passed, rather than
//     being missing from the queue's checks, failing, or saying it could not
//     run. A path check is the exception: it joins a run's checks for what the
//     change touches, is told nothing narrower than it always runs over, and
//     the queue does not run it, so its result before review is already its
//     whole one;
//   - the commit those checks ran on is the commit that lands, which is the
//     candidate on every path but the forge's queue.
//
// Where any of it is not shown, nothing lands and the refusal names what was
// not shown: the change is not landed on a check that did not cover it. The
// queue's own gate (runstate's MergeQueueGeneration.Gate) already refuses a
// candidate any configured check failed, did not finish, or could not run on;
// what this adds is the run's own list of checks, and the record.

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
	wanted := append([]string(nil), passed.Commands...)
	for _, skipped := range passed.CouldNotRun {
		wanted = append(wanted, skipped.Command)
	}
	pathCheck := make(map[string]bool, len(pathChecks))
	for _, check := range pathChecks {
		pathCheck[check.Command] = true
	}
	var unmet []string
	for _, command := range wanted {
		if pathCheck[command] {
			continue
		}
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
	return fmt.Sprintf("the merge queue's checks of entry %d (%s), generation %d: every configured check, told the whole module, passed on candidate %s, the commit that landed on %s",
		entry.Order, entry.EntryID, completion.Generation, completion.Landed, entry.TargetBranch)
}
