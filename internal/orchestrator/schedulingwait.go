package orchestrator

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

type schedulingWaitRecorder interface {
	RecordSchedulingWait(context.Context, string, string, time.Time) (beads.WorkItem, error)
}

func clearSchedulingWait(ctx context.Context, tracker any, id string, now time.Time, schedule *Schedule) {
	if recorder, ok := tracker.(schedulingWaitRecorder); ok {
		if _, err := recorder.RecordSchedulingWait(ctx, id, "", now); err != nil {
			schedule.CarryOutReadProblem = joinProblem(schedule.CarryOutReadProblem, fmt.Sprintf("clear the scheduling wait of %s: %v", id, err))
		}
	}
}

// Name whole identifiers only; a run identifier may contain an item identifier.
// Name longer identifiers first so an epic's identifier does not replace the
// beginning of a child's identifier. Titles are inserted only once.
func schedulingWaitReason(reason string, items map[string]beads.WorkItem) string {
	return regexp.MustCompile(`[A-Za-z0-9_.-]+`).ReplaceAllStringFunc(reason, func(id string) string {
		if title := strings.TrimSpace(items[id].Title); title != "" {
			return title + " (" + id + ")"
		}
		return id
	})
}
