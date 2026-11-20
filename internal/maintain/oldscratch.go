package maintain

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// StepOldScratch is the step that clears the scratch folders runs were given
// under the repository's .git directory before scratch moved to the state
// directory.
const StepOldScratch = "scratch"

// RunRecords reads one run's record, for the step that has to know whether
// the run an old scratch folder was cut for has ended. It is satisfied by
// *runstate.Store.
type RunRecords interface {
	Read(runID string) (runstate.State, error)
}

// clearOldScratch removes the scratch folders that runs which have ended left
// under the repository's .git directory, and says what it removed and what it
// left (execution.ClearOldScratchFolders). It is the clearing the ruling on
// scratch outside Git names as its second piece
// (docs/decisions/developer-run-scratch-outside-git.md): no build that carries
// the move makes a folder there, so the first pass on such a build clears what
// is there, and each pass after it finds nothing and says so, apart from the
// folders of runs still going when the first pass looked, which the first pass
// after their runs end clears.
//
// A folder is removed only when its run's record reads and says the run is
// over. One whose run is still in flight, which covers every run that holds a
// developer slot, is left; so is one whose record cannot be read, or whose run
// has no record, because neither can be shown to have ended. It runs off the
// supervisor's goroutine, after reconcile, so a run reconcile has just settled
// is read as settled.
func (p *Pass) clearOldScratch() runstate.SweepStep {
	step := runstate.SweepStep{Name: StepOldScratch}
	if p.Runs == nil {
		step.Outcome, step.Detail = runstate.StepSkipped, "no run store is wired, so no old scratch folder under the repository's .git directory can be shown to belong to a run that has ended, and none is removed"
		return step
	}
	clearing, err := execution.ClearOldScratchFolders(p.Repository, p.oldScratchEnded)
	said := saysOldScratch(clearing)
	switch {
	case err != nil:
		step.Outcome = runstate.StepFailed
		step.Detail = fmt.Sprintf("old scratch folders under the repository's .git directory could not all be cleared: %v", err)
		if said != "" {
			step.Detail += "; " + said
		}
	case said == "":
		step.Outcome, step.Detail = runstate.StepSkipped, "no scratch folder is left under the repository's .git directory"
	default:
		step.Outcome, step.Detail = runstate.StepRan, said
	}
	if said != "" {
		// The step's detail is cut to a bound; the log keeps every folder.
		p.log("maintenance: %s: %s", StepOldScratch, said)
	}
	return step
}

// oldScratchEnded answers, for the run an old scratch folder is named for,
// whether that run has ended, and where it has not, why its folder is left.
func (p *Pass) oldScratchEnded(runID string) (bool, string) {
	state, err := p.Runs.Read(runID)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, fmt.Sprintf("no record of run %s is kept, so it cannot be shown to have ended", runID)
	case err != nil:
		return false, fmt.Sprintf("run %s's record could not be read, so it cannot be shown to have ended: %v", runID, err)
	case state.HoldsDeveloperSlot():
		return false, fmt.Sprintf("run %s still holds a developer slot", runID)
	case state.Status.InFlight():
		return false, fmt.Sprintf("run %s is still going, though it holds no developer slot while it waits", runID)
	}
	return true, ""
}

// saysOldScratch is what a clearing removed and what it left, in words, and
// empty where it found no folder at all.
func saysOldScratch(clearing execution.OldScratchClearing) string {
	var said []string
	if len(clearing.Removed) > 0 {
		paths := make([]string, 0, len(clearing.Removed))
		for _, folder := range clearing.Removed {
			paths = append(paths, folder.Path)
		}
		said = append(said, fmt.Sprintf("removed %d scratch folder(s) that ended runs left under the repository's .git directory: %s", len(clearing.Removed), strings.Join(paths, ", ")))
	}
	if len(clearing.Left) > 0 {
		left := make([]string, 0, len(clearing.Left))
		for _, folder := range clearing.Left {
			left = append(left, fmt.Sprintf("%s, because %s", folder.Path, folder.Why))
		}
		said = append(said, fmt.Sprintf("left %d: %s; a later pass removes each one once its run is shown to have ended", len(clearing.Left), strings.Join(left, "; ")))
	}
	return strings.Join(said, "; ")
}
