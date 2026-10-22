package readmodel

// Why an admitted item has not started, as the scheduler last said it.
//
// The scheduler keeps one current reason per item in the tracker's metadata
// (beads.SchedulingWait): the pass that passes a ready item over writes it, a
// changed reason replaces it, and the run that starts the item clears it. An
// item the tracker leaves out of its ready set because it waits on other work
// keeps one too, cleared by the first pull that finds it no longer waiting.
// Every surface reads it from here, so `yoyo status` and the dashboard's
// waiting-work lists say the same sentence. A reason that has stood unchanged
// past execution.scheduling_wait_problem_after is a problem, and whose move it
// is comes from passedOverMoves — the same answer the idle line and the stall
// alarm give for the class the scheduler put the item in — rather than from an
// owner table of its own.
//
// A wait on other work is never a problem by standing: it lasts as long as the
// work it names, and that work is where any problem is shown.

import (
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// DefaultSchedulingWaitProblemAfter is how long one reason may stand before the
// wait is a problem, where the configuration names no time.
const DefaultSchedulingWaitProblemAfter = time.Hour

// SchedulingWaitSays is shared by the terminal and the dashboard's work lists.
func SchedulingWaitSays(wait beads.SchedulingWait) string {
	return schedulingWaitDates(wait) + ": " + wait.Reason
}

func schedulingWaitDates(wait beads.SchedulingWait) string {
	return fmt.Sprintf("first passed over at %s; current reason since %s",
		wait.FirstPassedOver.In(time.Local).Format("2006-01-02 15:04 MST"),
		wait.ReasonSince.In(time.Local).Format("2006-01-02 15:04 MST"))
}

// waitsOnOtherWork reports a reason kept on an item the tracker left out of its
// ready set because it waits on other work.
func waitsOnOtherWork(wait *beads.SchedulingWait) bool {
	return wait != nil && wait.Class == string(runstate.PassedOverWaitingOnOtherWork)
}

// keptWait is the entry's kept reason where it still says something: a wait on
// other work kept on an entry the queue now reads as ready is one the next pull
// clears, and is not said meanwhile.
func keptWait(entry backlog.Entry) *beads.SchedulingWait {
	if entry.Ready && waitsOnOtherWork(entry.SchedulingWait) {
		return nil
	}
	return entry.SchedulingWait
}

// SchedulingWaitWhose is whose move ends a wait, read off the class the
// scheduler recorded it under. A wait recorded with no class known to the
// pass-over vocabulary answers with nothing rather than a guess.
func SchedulingWaitWhose(wait beads.SchedulingWait) string {
	return passedOverMoves[runstate.PassedOverClass(wait.Class)]
}

func waitingWorkRef(entry backlog.Entry) WorkItemRef {
	ref := WorkItemRef{WorkItemID: entry.ID, Title: entry.Title, SchedulingWait: keptWait(entry)}
	if ref.SchedulingWait != nil {
		ref.WaitReason = SchedulingWaitSays(*ref.SchedulingWait)
	}
	return ref
}

func schedulingWaitProlonged(wait *beads.SchedulingWait, now time.Time, after time.Duration) bool {
	if after <= 0 {
		after = DefaultSchedulingWaitProblemAfter
	}
	return wait != nil && wait.Valid() && !waitsOnOtherWork(wait) && now.Sub(wait.ReasonSince) >= after
}

// markSchedulingWait marks a wait that has stood past the configured time as a
// problem, naming whose move it is.
func markSchedulingWait(ref *WorkItemRef, now time.Time, after time.Duration) {
	ref.SchedulingWaitProlonged = schedulingWaitProlonged(ref.SchedulingWait, now, after)
	ref.SchedulingWaitWhose = ""
	if !ref.SchedulingWaitProlonged {
		return
	}
	if after <= 0 {
		after = DefaultSchedulingWaitProblemAfter
	}
	ref.SchedulingWaitWhose = SchedulingWaitWhose(*ref.SchedulingWait)
	ref.WaitReason = fmt.Sprintf("problem: the same reason has stood for more than %s; %s", after, SchedulingWaitSays(*ref.SchedulingWait))
	if ref.SchedulingWaitWhose != "" {
		ref.WaitReason += "; the next move is " + ref.SchedulingWaitWhose
	}
}

// waitingWorkReason is what a refusal adds of the entry's kept reason: its dates
// alone where the refusal already says the same reason, so it is not said twice.
func waitingWorkReason(entry backlog.Entry, refusal string, now time.Time, after time.Duration) string {
	ref := waitingWorkRef(entry)
	if ref.SchedulingWait == nil {
		return ""
	}
	markSchedulingWait(&ref, now, after)
	if !ref.SchedulingWaitProlonged && ref.SchedulingWait.Reason == refusal {
		return schedulingWaitDates(*ref.SchedulingWait)
	}
	return ref.WaitReason
}
