package readmodel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

func TestSharedStandingCarriesTheSchedulersCurrentReason(t *testing.T) {
	wait := &beads.SchedulingWait{Reason: "waiting behind Later work (yoyodyne-later)", FirstPassedOver: moment.Add(-2 * time.Hour), ReasonSince: moment.Add(-time.Hour)}
	item := beads.WorkItem{ID: "yoyodyne-earlier", Title: "Earlier work", Status: "open", SchedulingWait: wait}
	tracker := statusTracker{fakeTracker{byStatus: map[string][]beads.WorkItem{"open": {item}}, ready: []beads.WorkItem{item}}}
	standing := ReadStanding(context.Background(), Sources{Tracker: tracker, Runs: fakeRuns{}, Now: func() time.Time { return moment }})
	if len(standing.SchedulingWaits) != 1 || standing.SchedulingWaits[0].SchedulingWait != wait {
		t.Fatalf("waits = %+v", standing.SchedulingWaits)
	}
	if !strings.Contains(standing.Render(), wait.Reason) {
		t.Fatalf("terminal omitted reason: %s", standing.Render())
	}
	encoded, err := json.Marshal(standing)
	if err != nil || !strings.Contains(string(encoded), wait.Reason) || !strings.Contains(string(encoded), "first_passed_over") {
		t.Fatalf("dashboard reading = %s, %v", encoded, err)
	}
}
