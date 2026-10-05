package orchestrator

import (
	"context"
	"fmt"
	"sort"
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

// Name longer identifiers first so an epic's identifier does not replace the
// beginning of a child's identifier. Titles are inserted only once.
func schedulingWaitReason(reason string, items map[string]beads.WorkItem) string {
	var ids []string
	for id := range items {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return len(ids[i]) > len(ids[j]) })
	var replacements []string
	for _, id := range ids {
		if title := strings.TrimSpace(items[id].Title); title != "" {
			replacements = append(replacements, id, title+" ("+id+")")
		}
	}
	return strings.NewReplacer(replacements...).Replace(reason)
}
