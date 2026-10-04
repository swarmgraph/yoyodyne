package readmodel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A developer whose session has written its final reply and is still running
// on work it backgrounded is said as that, in place of "developing": the turn
// is over, and nothing about it is the provider's. Once the wait has ended the
// line says what the run is doing now.
func TestARunWaitingOutBackgroundWorkAfterItsReplySaysSoOnTheRunningLine(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Runs = fakeRuns{
		incomplete: []runstate.State{{
			RunID:      "run-a",
			WorkItemID: "yoyodyne-ifd.435.6",
			Status:     runstate.StatusRunning,
			Phase:      runstate.PhaseDeveloping,
			StartedAt:  moment.Add(-time.Hour),
			AfterReply: &execution.AfterReply{
				RepliedAt:    moment.Add(-2 * time.Minute),
				BoundSeconds: int64((5 * time.Minute) / time.Second),
			},
		}},
		prices: map[string]runstate.ItemPrice{
			"yoyodyne-ifd.435.6": {Runs: []runstate.RunPrice{{RunID: "run-a", CostUSD: 4.00}}},
		},
	}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.Running) != 1 || standing.Running[0].AfterReply != "reply written, waiting for background processes: 2m of 5m" {
		t.Fatalf("running = %+v, want the wait after the reply said as it stands", standing.Running)
	}
	rendered := standing.Render()
	want := "  title unavailable (yoyodyne-ifd.435.6) — reply written, waiting for background processes: 2m of 5m, 1h00m elapsed, $4.00 so far\n"
	if !strings.Contains(rendered, want) {
		t.Fatalf("rendered:\n%s\nmissing: %q", rendered, want)
	}

	sources.Runs.(fakeRuns).incomplete[0].AfterReply.Outcome = execution.AfterReplyEnded
	standing = ReadStanding(context.Background(), sources)
	if standing.Running[0].AfterReply != "" {
		t.Fatalf("running = %+v, want nothing said of a wait that has ended", standing.Running)
	}
}
