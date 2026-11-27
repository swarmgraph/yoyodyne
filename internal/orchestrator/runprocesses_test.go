package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// recordingStopper is a stand-in for execution.RunProcesses that records every
// request and answers each with what it is told is still running.
type recordingStopper struct {
	mu       sync.Mutex
	requests []execution.RunProcessesRequest
	left     []execution.LeftProcess
}

func (s *recordingStopper) Stop(_ context.Context, request execution.RunProcessesRequest) []execution.LeftProcess {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, request)
	return s.left
}

// What a run started is stopped once its developer session ends, once its
// reviewer session ends, and as the run ends, every time for this run and with
// its worktree named; a process the stop left running is written on the run,
// listed for status, and said in the run's record.
func TestWhatARunStartedIsStoppedAfterEachSessionAndAsTheRunEnds(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := orchestratortest.RoleBackend(writeFeature, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})
	stopper := &recordingStopper{left: []execution.LeftProcess{{PID: 4242, Command: "java", Listening: []string{"127.0.0.1:25565"}, Why: "the harness could not stop it: permission refused"}}}
	pipeline.RunProcesses = stopper
	if err := os.MkdirAll(store.Root(), 0o700); err != nil {
		t.Fatal(err)
	}
	held := filepath.Join(store.Root(), "run-0123456789abcdef0123456789abcdef.attempt-held.hold")
	if err := os.WriteFile(held, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("outcome = %#v, want the run to finish", outcome)
	}
	if len(stopper.requests) < 3 {
		t.Fatalf("the run's processes were stopped %d time(s), want after the developer session, after the reviewer session, and as the run ended", len(stopper.requests))
	}
	// A hold file still on disk for the run, as a launch leaves one while a
	// process still has it open, is handed to every stop.
	handedHold := true
	for _, request := range stopper.requests {
		if !slices.Contains(request.Holds, held) {
			handedHold = false
		}
	}
	if !handedHold {
		t.Fatalf("a stop was not handed the run's hold file %s: %+v", held, stopper.requests)
	}
	for _, request := range stopper.requests {
		if request.RunID != outcome.RunID {
			t.Fatalf("a stop asked for run %q, want %q", request.RunID, outcome.RunID)
		}
		if len(request.Folders) == 0 || request.Folders[0] != outcome.WorktreePath {
			t.Fatalf("a stop named folders %v, want the run's worktree %q first", request.Folders, outcome.WorktreePath)
		}
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.LeftProcesses) != 1 || state.LeftProcesses[0].PID != 4242 {
		t.Fatalf("the run records %+v left running, want process 4242", state.LeftProcesses)
	}
	listed, err := store.LeftRunning()
	if err != nil || len(listed) != 1 || listed[0].RunID != outcome.RunID {
		t.Fatalf("LeftRunning() = %d run(s), %v; want the ended run", len(listed), err)
	}
	events, err := store.LoadEvents(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	said := false
	for _, event := range events {
		if event.Source == "harness" && strings.Contains(string(event.Payload), "process 4242, java, listening on 127.0.0.1:25565") {
			said = true
		}
	}
	if !said {
		t.Fatal("the run's record does not say which process was left running")
	}
}

// A later stop that finds nothing takes the line off the run.
func TestALeftProcessLeavesTheRecordWhenAStopFindsItGone(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := orchestratortest.RoleBackend(writeFeature, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})
	stopper := &gonAfterFirst{}
	pipeline.RunProcesses = stopper

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.LeftProcesses) != 0 {
		t.Fatalf("the run still records %+v left running after a stop found nothing", state.LeftProcesses)
	}
	if listed, err := store.LeftRunning(); err != nil || len(listed) != 0 {
		t.Fatalf("LeftRunning() = %d run(s), %v; want none", len(listed), err)
	}
}

// gonAfterFirst reports a process left running at the first stop and nothing
// after.
type gonAfterFirst struct {
	mu    sync.Mutex
	calls int
}

func (g *gonAfterFirst) Stop(context.Context, execution.RunProcessesRequest) []execution.LeftProcess {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	if g.calls == 1 {
		return []execution.LeftProcess{{PID: 4242, Why: "the harness could not stop it: permission refused"}}
	}
	return nil
}
