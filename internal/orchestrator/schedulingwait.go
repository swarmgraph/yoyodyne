package orchestrator

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

type schedulingWaitRecorder interface {
	RecordSchedulingWait(context.Context, string, string, time.Time) (beads.WorkItem, error)
}

var schedulingReasonIDs = regexp.MustCompile(`[A-Za-z0-9_.-]+`)

// Expand whole item identifiers once, leaving run identifiers untouched.
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

// Clear only after the pipeline has accepted a fresh or continued run.
func acceptSchedulingWait(ctx context.Context, tracker any, id string, now time.Time) error {
	if recorder, ok := tracker.(schedulingWaitRecorder); ok {
		_, err := recorder.RecordSchedulingWait(ctx, id, "", now)
		return err
	}
	return nil
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
