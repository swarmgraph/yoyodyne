package orchestrator

// Whether a recovery about an older run still applies once its item's work is
// settled.
//
// A decision to repair or re-run a stopped run, and an item budget that records
// a repair without one, stay on the item's triage record after the item closes.
// So do the docket entries for that run, settled or not. The carry-out walks all
// of them on every pass, and before this it offered each one to the action that
// carries it out: a closed item whose later run had merged long ago had its
// older run's repair attempted, refused, and written onto the item and the
// development manager's docket as a decision she had to make again. A run that
// had itself merged was presented the same way, as a stoppage nobody could
// recover. Nothing about either is anybody's to decide.
//
// So the carry-out asks, before it offers a recovery and again before it
// executes one, whether the recovery still applies. It does not where the item
// is closed and one of three records says its work is settled:
//
//   - the run itself succeeded, and its merge is confirmed and complete;
//   - a later run of the item has a confirmed, complete merge into the same
//     target, and no newer publication of the item is still unsettled;
//   - nobody has decided anything about the run, and the harness already
//     settled its docket entry: closed with its item, or with its publication.
//
// Each is a record of settled work rather than the item's status, which is
// asked as well and never alone: an item closed by hand with no settled work
// keeps the recovery the action would have refused anyway, and an item that is
// open again — reopened explicitly — keeps every recovery, so a reopening is
// never mistaken for obsolete work. The confirmed merge is the one
// runretirement.go reads (confirmedCompletedPublication), so a later run
// retired from its integration and an older run's recovery found not to apply
// are judged by the same evidence.
//
// What is recorded is a carry-out finding with its own cause
// (triage.CarryOutNoLongerApplies), on the item's triage record where every
// other finding about the run is, with a note on the item. It removes nothing:
// no branch, worktree, run record, decision, or earlier note. The docket and
// the holds read it as settled rather than as a question, and a decision
// recorded after it — which only a reopening makes sense of — is not answered
// by it.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// settledWork is the record that says a recovery about one run no longer
// applies, in words: which run, which merge or which settlement.
type settledWork struct {
	account string
}

// settledWorkOf reads the run records and the run's docket closure for what
// settled the work a recovery about runID would redo. It does not read the
// item: the caller asks that the item is closed as well, because none of these
// records says the item was not reopened since.
//
// decided says a decision about the run stands on the item's triage record; a
// settlement of the docket entry answers only an undecided run, since a
// decision recorded after a closure is somebody deciding past it.
func settledWorkOf(runID, workItemID string, recorded []runstate.State, closure *triage.Closure, decided bool, now time.Time) (settledWork, bool) {
	var run runstate.State
	known := false
	for _, state := range recorded {
		if state.RunID == runID {
			run, known = state, true
			break
		}
	}
	if known && run.WorkItemID != workItemID {
		return settledWork{}, false
	}
	if known && confirmedCompletedPublication(run) && run.CompletedAt != nil {
		return settledWork{account: fmt.Sprintf(
			"run %s itself succeeded: its change merged through pull request #%d at %s into %s and its publication settled at %s, so there is nothing of it to repair or run again",
			run.RunID, run.PullRequest.Number, run.PullRequest.MergeCommit, run.Integration.TargetBranch, run.CompletedAt.UTC().Format(time.RFC3339))}, true
	}
	if known {
		if later, found := laterSettledPublication(run, recorded); found {
			return settledWork{account: fmt.Sprintf(
				"a later run of the item, %s, merged its change through pull request #%d at %s into %s and its publication settled at %s, so recovering the older run %s would redo finished work",
				later.RunID, later.PullRequest.Number, later.PullRequest.MergeCommit, later.Integration.TargetBranch, later.CompletedAt.UTC().Format(time.RFC3339), run.RunID)}, true
		}
	}
	if !decided && closure != nil && closure.Holds(now) && harnessSettlement(closure.Decision) {
		account := fmt.Sprintf("nobody has decided anything about run %s, and the harness already settled its docket entry at %s (%s)",
			runID, closure.ClosedAt.UTC().Format(time.RFC3339), strings.TrimSpace(closure.Decision))
		if reason := strings.TrimSpace(closure.Reason); reason != "" {
			account += ": " + singleLine(reason, 512)
		}
		return settledWork{account: account}, true
	}
	return settledWork{}, false
}

// laterSettledPublication is the newest publication of the run's item that
// follows the run, where it is a confirmed, complete merge into the run's own
// target. A newer publication that has not settled is no such evidence, and
// neither is an older one standing behind it, exactly as RunRetirer.Retire
// refuses to substitute one for the other.
func laterSettledPublication(run runstate.State, recorded []runstate.State) (runstate.State, bool) {
	var latest runstate.State
	for _, candidate := range recorded {
		if candidate.RunID == run.RunID || candidate.WorkItemID != run.WorkItemID || candidate.Retirement != nil ||
			handedBack(candidate) || !publicationFollowsRun(candidate, run) {
			continue
		}
		if latest.RunID == "" || laterPublication(candidate, latest) {
			latest = candidate
		}
	}
	if latest.RunID == "" || !confirmedCompletedPublication(latest) || latest.CompletedAt == nil ||
		(run.TargetBranch != "" && latest.Integration.TargetBranch != run.TargetBranch) {
		return runstate.State{}, false
	}
	return latest, true
}

// harnessSettlement reports a closure the harness made because the question an
// entry asked was answered elsewhere: its item closed, or its publication
// settled. Every other closure is somebody's decision.
func harnessSettlement(decision string) bool {
	switch strings.TrimSpace(decision) {
	case triage.ItemClosedDecision, settledPublicationDecision:
		return true
	default:
		return false
	}
}

// preservedAccount says what the run's own record says of its branch and
// worktree. It is read from the record rather than assumed, so a run whose
// cleanup removed them is never described as having them kept.
func preservedAccount(runID string, recorded []runstate.State) string {
	for _, run := range recorded {
		if run.RunID != runID {
			continue
		}
		branch, worktree := "not removed", "not removed"
		if run.BranchRemoved {
			branch = "removed"
		}
		if run.WorktreeRemoved {
			worktree = "removed"
		}
		return fmt.Sprintf("run %s's record says its branch was %s and its worktree was %s", runID, branch, worktree)
	}
	return fmt.Sprintf("the harness holds no record of run %s to say what became of its branch and worktree", runID)
}

// noLongerAppliesClears is what a finding that a recovery does not apply says
// would change that. Nobody has to do anything; only a reopening does.
const noLongerAppliesClears = "Nobody needs to do anything about it. Only reopening the item and the development manager recording a new decision about this run would make a recovery of it apply again"

// recoverySettled reports a recovery about task's run that no longer applies:
// the item is closed and settledWorkOf finds its work settled. An item or a
// record that cannot be read establishes nothing, and the recovery is left to
// the action, whose own gates refuse what they always refused.
func (c CarryOut) recoverySettled(ctx context.Context, task CarryOutTask, closure *triage.Closure, decided bool, recorded []runstate.State, item func(context.Context, string) (beads.WorkItem, bool)) (string, bool) {
	if task.Harness {
		return "", false
	}
	settled, found := settledWorkOf(task.RunID, task.WorkItemID, recorded, closure, decided, c.now())
	if !found {
		return "", false
	}
	read, ok := item(ctx, task.WorkItemID)
	if !ok || read.ID != task.WorkItemID || read.Status != "closed" {
		return "", false
	}
	title := strings.TrimSpace(read.Title)
	if title == "" {
		title = read.ID
	}
	return fmt.Sprintf("%s (%s) is closed and %s. Nothing was started and nothing was spent, and nothing was removed: %s, and its history and the item's notes are kept. The merge says the change landed; it does not say the item's acceptance criteria were met, and anything the item records as unfinished still stands",
		title, read.ID, settled.account, preservedAccount(task.RunID, recorded)), true
}

// itemReader reads each item once per sweep through the tracker the carry-out
// writes its notes with. A carry-out wired with no tracker reads nothing, and
// so finds no recovery settled.
func (c CarryOut) itemReader() func(context.Context, string) (beads.WorkItem, bool) {
	read := make(map[string]beads.WorkItem)
	failed := make(map[string]bool)
	return func(ctx context.Context, id string) (beads.WorkItem, bool) {
		if c.Notes == nil || failed[id] {
			return beads.WorkItem{}, false
		}
		if item, seen := read[id]; seen {
			return item, true
		}
		item, err := c.Notes.Show(ctx, id)
		if err != nil {
			failed[id] = true
			return beads.WorkItem{}, false
		}
		read[id] = item
		return item, true
	}
}

// docketClosure is the closure standing over the docket entry a task was
// offered from, where the docket holds one.
func docketClosure(entries []triage.Entry, key string) *triage.Closure {
	for _, entry := range entries {
		if entry.Key == key {
			return entry.Closed
		}
	}
	return nil
}

// settledTask is a recovery the sweep found no longer applies, with the account
// the finding records.
type settledTask struct {
	task    CarryOutTask
	account string
}

// recordNoLongerApplies writes the finding that one recovery does not apply onto
// the item's triage record, with a note on the item, and returns the account of
// it. It is written under a context detached from the caller's, as every other
// finding is (stopped).
func (c CarryOut) recordNoLongerApplies(ctx context.Context, task CarryOutTask, account string) CarriedOut {
	carried := CarriedOut{
		WorkItemID: task.WorkItemID,
		RunID:      task.RunID,
		DocketKey:  task.DocketKey,
		Decision:   task.Decision,
		Cause:      triage.CarryOutNoLongerApplies,
		Gate:       runstate.TriageGateWorkItem,
		Problem: fmt.Sprintf("the %q recorded about run %s was not carried out because it no longer applies: %s. %s",
			task.Decision, task.RunID, strings.TrimSpace(account), noLongerAppliesClears),
	}
	if harnessOwnTask(task.Decision) {
		// Nobody decided a harness continuation, so there is no decision to write a
		// finding against; the pass says it and the next pass asks again.
		return carried
	}
	write, stopWriting := recordContext(ctx)
	defer stopWriting()
	if _, err := c.Decisions.RecordCarryOutRefusal(write, task.WorkItemID, runstate.TriageCarryOut{
		Cause:     triage.CarryOutNoLongerApplies,
		DecidedAt: task.DecidedAt,
		RunID:     task.RunID,
		Decision:  task.Decision,
		Gate:      runstate.TriageGateWorkItem,
		Refusal:   singleLine(account, runstate.MaxTriageCarryOutRefusalBytes),
		Clears:    noLongerAppliesClears,
	}, c.now()); err != nil {
		carried.RecordProblem = fmt.Sprintf("and that could not be written onto %s's triage record, so the next pass finds it again: %v", task.WorkItemID, err)
		return carried
	}
	if c.Notes != nil {
		if err := c.deliverItemNotes(write, task.WorkItemID); err != nil {
			carried.RecordProblem = fmt.Sprintf("the finding is on the triage record but could not be appended to the item's notes; its note remains pending for a later pull: %v", err)
		}
	}
	return carried
}

// settledNow asks recoverySettled of the records as they stand at execution,
// for one task. A record that cannot be read establishes nothing.
func (c CarryOut) settledNow(ctx context.Context, task CarryOutTask, decided bool) (string, bool) {
	recorded, err := c.Runs.Recorded()
	if err != nil {
		return "", false
	}
	var closure *triage.Closure
	if entries, err := c.Docket.List(); err == nil {
		closure = docketClosure(entries, task.DocketKey)
	}
	return c.recoverySettled(ctx, task, closure, decided, recorded, c.itemReader())
}
