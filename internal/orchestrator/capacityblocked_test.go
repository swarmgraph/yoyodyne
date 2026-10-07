package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A run blockOnUsageLimit stops — a usage limit naming a reset that is not in
// the future, or an overload that outlasted the pause budget — is listed in the
// capacity-blocked state that `yoyo status --json` carries, read from the
// record the pipeline itself leaves on disk. The run's ending clears every
// pause, and until it kept this one cause the reading never found such a run:
// its only test used a hand-built record that kept the cause.
func TestAUsageLimitStopIsListedAsCapacityBlocked(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name      string
		provider  func() *orchestratortest.Backend
		maxPause  time.Duration
		cause     string
		refusedBy string
	}{
		{
			name: "a usage limit naming a reset that is not in the future",
			provider: func() *orchestratortest.Backend {
				limit := backend.UsageLimit{Kind: "five_hour", ResetsAt: baseTime.Add(-time.Minute)}
				return usageLimitBackend(1, &limit, approveVerdict)
			},
			maxPause:  6 * time.Hour,
			cause:     runstate.PauseUsageLimit,
			refusedBy: "an exhausted five_hour usage limit",
		},
		{
			name:      "an overload that outlasted the pause budget",
			provider:  func() *orchestratortest.Backend { return serverOverloadBackend(10, approveVerdict) },
			maxPause:  4 * time.Minute,
			cause:     runstate.PauseServerOverload,
			refusedBy: "a transient provider server overload",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			repository := pipelineRepository(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			provider := testCase.provider()
			pipeline, store := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
			clock := &pausingClock{now: baseTime}
			pipeline = waiting(automatic(pipeline, provider), clock, testCase.maxPause, testCase.maxPause)
			pipeline.Config.Execution.ServerOverloadPause = config.Duration(90 * time.Second)

			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			if err == nil || !outcome.Blocked {
				t.Fatalf("Run() = %+v, %v; want the run stopped with a blocker", outcome, err)
			}
			stopped, err := store.Load(outcome.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if !stopped.Status.Terminal() || stopped.StopClass != runstate.StopUsagePause || stopped.PauseCause != testCase.cause {
				t.Fatalf("stopped record: status %q, stop class %q, pause cause %q; want a terminal usage-limit stop keeping %q",
					stopped.Status, stopped.StopClass, stopped.PauseCause, testCase.cause)
			}
			if stopped.UsageLimitResetsAt != nil || stopped.UsageLimitPausedSince != nil {
				t.Fatalf("stopped record = %#v, want no deadline: the run is not waiting for anything", stopped)
			}

			blocked := readmodel.CapacityBlockedOf(readmodel.Sources{Runs: store}, clock.Now())
			if len(blocked.Runs) != 1 {
				t.Fatalf("capacity-blocked runs = %+v (%s), want the stopped run", blocked.Runs, blocked.RunsProblem)
			}
			entry := blocked.Runs[0]
			if entry.RunID != outcome.RunID || entry.WorkItemID != tracker.Item.ID || entry.State != readmodel.CapacityStateBlocked {
				t.Fatalf("entry = %+v, want run %s of %s capacity-blocked", entry, outcome.RunID, tracker.Item.ID)
			}
			if entry.RefusedBy != testCase.refusedBy {
				t.Fatalf("refused by %q, want %q", entry.RefusedBy, testCase.refusedBy)
			}
			if entry.ResetsAt != nil {
				t.Fatalf("resets at %s, want none: the run refused the wait rather than took it", entry.ResetsAt)
			}
			if stopped.CompletedAt == nil || !entry.Since.Equal(stopped.CompletedAt.UTC()) {
				t.Fatalf("since = %s, want the moment the run stopped (%v)", entry.Since, stopped.CompletedAt)
			}
			if !entry.Preserved || !strings.Contains(entry.Remedy, "development manager") {
				t.Fatalf("entry = %+v, want its change preserved and the item named as the development manager's", entry)
			}
		})
	}
}

// Only a usage-limit stop's capacity cause outlives the run's ending; every
// other pause, and every other ending, leaves the terminal record with none.
func TestKeptPauseCauseIsOnlyAUsageLimitStopsCapacityCause(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		cause string
		class runstate.StopClass
		want  string
	}{
		{runstate.PauseUsageLimit, runstate.StopUsagePause, runstate.PauseUsageLimit},
		{runstate.PauseServerOverload, runstate.StopUsagePause, runstate.PauseServerOverload},
		{runstate.PauseOperatorHold, runstate.StopUsagePause, ""},
		{runstate.PauseUsageLimit, runstate.StopCancelled, ""},
		{runstate.PauseUsageLimit, "", ""},
	} {
		if got := keptPauseCause(testCase.cause, testCase.class); got != testCase.want {
			t.Fatalf("keptPauseCause(%q, %q) = %q, want %q", testCase.cause, testCase.class, got, testCase.want)
		}
	}
}
