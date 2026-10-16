package orchestrator

// Why a ready item has not started, kept on the item.
//
// A pass that leaves a ready item unstarted, and a carry-out that leaves a
// decided repair or re-run unstarted, already work out why; this keeps that
// reason as one value per item in the tracker's metadata (beads.SchedulingWait),
// beside the notes and never in them, so `yoyo status` and the dashboard can say
// it (readmodel/schedulingwait.go). A changed reason replaces the value, and the
// run that starts the item clears it. Nothing the scheduler decides reads it.

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type schedulingWaitRecorder interface {
	RecordSchedulingWait(ctx context.Context, id, class, reason string, at time.Time) (beads.WorkItem, error)
}

// schedulingReasonIDs matches a word that may be an item identifier: dots only
// between characters, so a sentence's closing period is not part of it.
var schedulingReasonIDs = regexp.MustCompile(`[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)*`)

// schedulingWaitReason names each item the reason names by what it is, its title
// with its identifier after it, once: an identifier already written that way is
// left alone. A run identifier is no item's and is left as it is.
func schedulingWaitReason(reason string, items map[string]beads.WorkItem) string {
	var result strings.Builder
	end := 0
	for _, span := range schedulingReasonIDs.FindAllStringIndex(reason, -1) {
		id := reason[span[0]:span[1]]
		result.WriteString(reason[end:span[0]])
		title := strings.TrimSpace(items[id].Title)
		if title != "" && !strings.HasSuffix(reason[:span[0]], title+" (") {
			result.WriteString(title + " (" + id + ")")
		} else {
			result.WriteString(id)
		}
		end = span[1]
	}
	result.WriteString(reason[end:])
	return result.String()
}

// recordSchedulingWait keeps one item's current reason on a tracker that can
// hold it, and does nothing on one that cannot.
func recordSchedulingWait(ctx context.Context, tracker any, id string, class runstate.PassedOverClass, reason string, now time.Time) error {
	if recorder, ok := tracker.(schedulingWaitRecorder); ok {
		_, err := recorder.RecordSchedulingWait(ctx, id, string(class), reason, now)
		return err
	}
	return nil
}

// Clear only after the pipeline has accepted a fresh or continued run.
func acceptSchedulingWait(ctx context.Context, tracker any, id string, now time.Time) error {
	return recordSchedulingWait(ctx, tracker, id, "", "", now)
}

func carryOutWaitReason(ctx context.Context, tracker interface {
	Show(context.Context, string) (beads.WorkItem, error)
}, reason string) string {
	if tracker == nil {
		return reason
	}
	items := make(map[string]beads.WorkItem)
	for _, id := range schedulingReasonIDs.FindAllString(reason, -1) {
		if !strings.Contains(id, "-") || strings.HasPrefix(id, "run-") {
			continue
		}
		if item, err := tracker.Show(ctx, id); err == nil && item.ID == id {
			items[id] = item
		}
	}
	return schedulingWaitReason(reason, items)
}

// A failed reporting write never stops accepted execution. The run keeps the
// obligation so reconciliation can retry it after the process releases its lease.
func (a *activeRun) clearAcceptedSchedulingWait(ctx context.Context) {
	previous := a.state.SchedulingWaitClearFailure
	now := a.pipeline.clock().Now()
	err := acceptSchedulingWait(ctx, a.pipeline.Tracker, a.state.WorkItemID, now)
	a.state.SchedulingWaitClearFailure = ""
	a.state.SchedulingWaitClearAt = nil
	if err != nil {
		a.state.SchedulingWaitClearAt = &now
		a.state.SchedulingWaitClearFailure = fmt.Sprintf("accepted work is executing, but its old scheduling reason could not be cleared: %v; reconciliation retries the reporting write", err)
	}
	a.outcome.SchedulingWaitClearFailure = a.state.SchedulingWaitClearFailure
	if previous == "" && err == nil {
		return
	}
	if err := a.pipeline.Store.Save(a.state); err != nil {
		a.outcome.SchedulingWaitClearFailure = joinProblem(a.outcome.SchedulingWaitClearFailure, fmt.Sprintf("record that clearing the old scheduling reason is still owed: %v", err))
	}
}

func (r Reconciler) retrySchedulingWaitClear(ctx context.Context, state *runstate.State) error {
	if state.SchedulingWaitClearFailure == "" {
		return nil
	}
	// A later scheduling pass may have recorded a new wait after this run ended.
	// Never clear that later decision while retrying an older reporting write.
	item, err := r.Tracker.Show(ctx, state.WorkItemID)
	if err != nil {
		return err
	}
	newer := state.SchedulingWaitClearAt != nil && item.SchedulingWait != nil && item.SchedulingWait.ReasonSince.After(*state.SchedulingWaitClearAt)
	if !newer {
		if err := acceptSchedulingWait(ctx, r.Tracker, state.WorkItemID, r.clock().Now()); err != nil {
			return fmt.Errorf("retry clearing the accepted item's scheduling reason: %w", err)
		}
	}
	previous := state.SchedulingWaitClearFailure
	previousAt := state.SchedulingWaitClearAt
	state.SchedulingWaitClearAt = nil
	state.SchedulingWaitClearFailure = ""
	if err := r.Store.Save(*state); err != nil {
		state.SchedulingWaitClearFailure = previous
		state.SchedulingWaitClearAt = previousAt
		return err
	}
	return nil
}
