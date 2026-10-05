package readmodel

import (
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// SchedulingWaitSays is shared by the terminal and the dashboard's work lists.
func SchedulingWaitSays(wait beads.SchedulingWait) string {
	return fmt.Sprintf("first passed over at %s; current reason since %s: %s",
		wait.FirstPassedOver.In(time.Local).Format("2006-01-02 15:04 MST"),
		wait.ReasonSince.In(time.Local).Format("2006-01-02 15:04 MST"), wait.Reason)
}

func waitingWorkRef(entry backlog.Entry) WorkItemRef {
	ref := WorkItemRef{WorkItemID: entry.ID, Title: entry.Title, SchedulingWait: entry.SchedulingWait}
	if entry.SchedulingWait != nil {
		ref.WaitReason = SchedulingWaitSays(*entry.SchedulingWait)
	}
	return ref
}

func schedulingWaitProlonged(wait *beads.SchedulingWait, now time.Time, after time.Duration) bool {
	if after == 0 {
		after = time.Hour
	}
	return wait != nil && wait.Valid() && now.Sub(wait.ReasonSince) >= after
}

func markSchedulingWait(ref *WorkItemRef, now time.Time, after time.Duration) {
	ref.SchedulingWaitProlonged = schedulingWaitProlonged(ref.SchedulingWait, now, after)
	if ref.SchedulingWaitProlonged {
		ref.WaitReason = "same scheduling reason has stood beyond the configured time; " + ref.WaitReason
	}
}

func waitingWorkReason(entry backlog.Entry, now time.Time, after time.Duration) string {
	ref := waitingWorkRef(entry)
	markSchedulingWait(&ref, now, after)
	return ref.WaitReason
}
