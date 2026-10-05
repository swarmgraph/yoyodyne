package beads

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const schedulingWaitKey = "yoyodyne_scheduling_wait"

// SchedulingWait is the scheduler's current account, not an eligibility gate.
// FirstPassedOver survives a change of reason; ReasonSince dates this reason.
type SchedulingWait struct {
	Reason          string    `json:"reason"`
	FirstPassedOver time.Time `json:"first_passed_over"`
	ReasonSince     time.Time `json:"reason_since"`
}

func (w SchedulingWait) Valid() bool {
	return strings.TrimSpace(w.Reason) != "" && !w.FirstPassedOver.IsZero() && !w.ReasonSince.Before(w.FirstPassedOver)
}

// RecordSchedulingWait updates one metadata value, leaving notes untouched.
// An empty reason clears it. Repeating a reason does not reset either clock.
func (c Client) RecordSchedulingWait(ctx context.Context, id, reason string, now time.Time) (WorkItem, error) {
	item, err := c.Show(ctx, id)
	if err != nil {
		return WorkItem{}, err
	}
	wait, changed := NextSchedulingWait(item.SchedulingWait, reason, now)
	if !changed {
		return item, nil
	}
	value := ""
	if wait != nil {
		encoded, err := json.Marshal(wait)
		if err != nil {
			return WorkItem{}, err
		}
		value = string(encoded)
	}
	data, err := c.write(ctx, id, "update", id, "--set-metadata="+schedulingWaitKey+"="+value, "--json")
	if err != nil {
		return WorkItem{}, err
	}
	stored, err := decodeSingleWorkItem(data)
	if err != nil {
		return WorkItem{}, err
	}
	if (wait == nil) != (stored.SchedulingWait == nil) || (wait != nil && *wait != *stored.SchedulingWait) {
		return WorkItem{}, fmt.Errorf("work item %s did not retain its scheduling wait", id)
	}
	return stored, nil
}

// NextSchedulingWait preserves the first wait across passes and restarts.
func NextSchedulingWait(previous *SchedulingWait, reason string, now time.Time) (*SchedulingWait, bool) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, previous != nil
	}
	if previous != nil && previous.Reason == reason {
		return previous, false
	}
	wait := &SchedulingWait{Reason: reason, FirstPassedOver: now.UTC(), ReasonSince: now.UTC()}
	if previous != nil {
		wait.FirstPassedOver = previous.FirstPassedOver
	}
	return wait, true
}
