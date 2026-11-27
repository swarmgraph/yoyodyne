package orchestrator

// Continuing a run that holds its item, whatever status the item reads.
//
// A run waiting on GitHub's checks keeps its branch, checkout and session, and
// the store gives its lease to it alone; an item that reads open while it waits
// has only had its claim released. The continuation claims it again rather than
// refusing. A closed item and one somebody blocked are still refused, and that
// refusal counts toward nothing the hold on new work counts.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// heldRunResume resumes a run holding an item that reads status, in a pipeline
// whose repository readiness check refuses, so a resume that gets past the
// item's status stops at the readiness check before any developer is asked.
func heldRunResume(t *testing.T, status string) (*orchestratortest.Tracker, error) {
	t.Helper()
	repository := pipelineRepository(t)
	item := beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: status}
	tracker := &orchestratortest.Tracker{Item: item}
	pipeline, _ := newPipeline(t, repository, tracker, &orchestratortest.Backend{}, []string{"exit 0"})
	pipeline.Worktrees = readinessFailureWorktrees{WorktreeManager: pipeline.Worktrees, cause: errors.New("past the item's status")}
	state := runstate.State{RunID: pipelineRunID, WorkItemID: item.ID, Branch: "yoyodyne/yoyodyne-task/run"}
	_, err := pipeline.resumeRun(context.Background(), state, item, false, "", false)
	return tracker, err
}

func TestAContinuedRunWhoseItemReadsOpenClaimsItAgain(t *testing.T) {
	t.Parallel()
	tracker, err := heldRunResume(t, "open")
	var refused EnvironmentRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("resumeRun() error = %v, want it past the item's status and stopped at the readiness check", err)
	}
	var status ItemStatusRefusal
	if errors.As(err, &status) {
		t.Fatalf("resumeRun() error = %v, want no refusal over the item's status", err)
	}
	if !tracker.Claimed || tracker.Item.Status != "in_progress" {
		t.Fatalf("claimed = %t, status = %q, want the item claimed again for the run holding it", tracker.Claimed, tracker.Item.Status)
	}
	if got := strings.Join(tracker.Calls, ","); !strings.HasPrefix(got, "record,claim") {
		t.Fatalf("tracker calls = %q, want the note written before the claim", got)
	}
	if !strings.Contains(tracker.Notes, "read open rather than in progress") || !strings.Contains(tracker.Notes, "no repair attempt, no review round, and no re-run") {
		t.Fatalf("notes = %q, want the item told why it was claimed again and that nothing was charged", tracker.Notes)
	}
}

func TestAContinuedRunWhoseItemIsClosedOrBlockedIsRefused(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"closed", "blocked"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			tracker, err := heldRunResume(t, status)
			var refused ItemStatusRefusal
			if !errors.As(err, &refused) || refused.Status != status {
				t.Fatalf("resumeRun() error = %v, want a refusal over the item reading %s", err, status)
			}
			if tracker.Claimed {
				t.Fatalf("the %s item was claimed, want it left as it reads", status)
			}
		})
	}
}

// A continuation refused over its item's status, again and again, holds
// nothing: with the brake at two, three such refusals leave no hold and no
// count, while three ordinary failures in the same shape trip it.
func TestARefusalOverTheItemsStatusLeavesTheHoldsCountUnchanged(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		err    error
		counts bool
	}{
		{"item status", ItemStatusRefusal{RunID: "run-x", WorkItemID: "yoyodyne-x", Status: "closed", Err: errors.New("closed with no later merge")}, false},
		{"ordinary failure", errors.New("the developer's change failed its checks"), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")...)
			harness.blockedRuns = 2
			harness.run = func(*scheduleHarness, string) (Outcome, error) { return Outcome{}, test.err }
			harness.onSleep = func(*scheduleHarness, int) bool { return false }
			scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Now: harness.clock}
			schedule, err := scheduler.Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
			if test.counts {
				if schedule.Braked == nil {
					t.Fatalf("schedule = %s, want ordinary failures to trip the hold", schedule.Render())
				}
				return
			}
			if schedule.Braked != nil || schedule.BlockedInARow != 0 || len(harness.summonses) != 0 {
				t.Fatalf("braked = %#v, blocked in a row = %d, summoned %d, want refusals over the item's status counted toward nothing",
					schedule.Braked, schedule.BlockedInARow, len(harness.summonses))
			}
		})
	}
}
