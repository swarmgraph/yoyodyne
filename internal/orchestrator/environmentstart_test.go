package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// None of these starts attempted the work. Every poll owes the unchanged item
// another try, with no failed-attempt docket entry and no charge to the brake.
func TestEnvironmentalStartsLeaveNoItemMemoryOrFailureStorm(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		err  error
	}{
		{"dirty checkout", gitworktree.PrimaryDirtyError{Paths: []string{".yoyodyne/config.yaml"}}},
		{"capacity", runstate.CapacityError{Active: 1, Limit: 1}},
		{"sandbox", fmt.Errorf("%w: fork/exec /bin/zsh: argument list too long", execution.ErrProcessNotStarted)},
		{"other readiness", refusedByEnvironment("the repository is not ready for an isolated run", errors.New("git status could not read the index"))},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			harness := newScheduleHarness(readyItems("yoyodyne-one")...)
			harness.blockedRuns = 1
			harness.run = func(*scheduleHarness, string) (Outcome, error) { return Outcome{}, test.err }
			harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 3 }
			sessions := &recordedSessions{}
			schedule, err := (Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}).Schedule(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if starts := len(harness.pullOrder()); starts != 3 {
				t.Fatalf("starts = %d, want a retry on each of three polls", starts)
			}
			if attempts := harness.recordedAttempts(); len(attempts) != 0 {
				t.Fatalf("failed-attempt memory = %+v, want none", attempts)
			}
			if schedule.BlockedInARow != 0 || schedule.Braked != nil || schedule.AttemptProblem != "" {
				t.Fatalf("refusal charged the item or the brake: %+v", schedule)
			}
			if test.name == "dirty checkout" {
				const want = "runs cannot start: uncommitted changes in the primary checkout (.yoyodyne/config.yaml); commit or stash to release"
				if got := sessions.said(runstate.WatchBlocked); got != want {
					t.Fatalf("blocked line = %q, want %q", got, want)
				}
			}
		})
	}
}

type readinessFailureWorktrees struct {
	WorktreeManager
	cause error
}

func (w readinessFailureWorktrees) ValidateReady(context.Context) error { return w.cause }

// Exercise the actual Run and resumeRun wraps: the marker must hold the bare
// cause, while Error still delegates verbatim and the outer context stays intact.
func TestReadinessWrapsKeepTheBeginningOfALongCause(t *testing.T) {
	t.Parallel()
	for _, resume := range []bool{false, true} {
		name := "Run"
		if resume {
			name = "resumeRun"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repository := pipelineRepository(t)
			item := beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}
			tracker := &orchestratortest.Tracker{Item: item}
			provider := &orchestratortest.Backend{}
			pipeline, _ := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
			cause := errors.New("the index could not be read: " + strings.Repeat("x", maxBlockedDetailBytes*3))
			pipeline.Worktrees = readinessFailureWorktrees{WorktreeManager: pipeline.Worktrees, cause: cause}
			condition := "the repository is not ready for an isolated run"
			prefix := "repository is not ready for an isolated run: "
			var err error
			if resume {
				item.Status = "in_progress"
				state := runstate.State{RunID: pipelineRunID, WorkItemID: item.ID}
				_, err = pipeline.resumeRun(context.Background(), state, item, false, "", false)
				condition = "the repository is not ready to resume a run"
				prefix = "repository is not ready to resume run " + pipelineRunID + ": "
			} else {
				_, err = pipeline.Run(context.Background(), item.ID)
			}
			var refused EnvironmentRefusedError
			if !errors.As(err, &refused) || !errors.Is(err, cause) {
				t.Fatalf("error = %v, want the marked cause", err)
			}
			if refused.Error() != cause.Error() || err.Error() != prefix+cause.Error() {
				t.Fatalf("Error changed the original refusal: marker %q, error %q", refused.Error(), err.Error())
			}
			want := "runs cannot start: " + condition + ": " + singleLine(cause.Error(), maxBlockedDetailBytes)
			if got := blockedReason(err); got != want || strings.Count(got, condition) != 1 {
				t.Fatalf("operator line = %q, want %q", got, want)
			}
			if tracker.Claimed {
				t.Fatal("readiness refusal claimed work")
			}
		})
	}
}
