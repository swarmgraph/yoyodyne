package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// reservingStore is the run store with what its reservation does replaced.
type reservingStore struct {
	*runstate.Store
	reserve func(ctx context.Context, state runstate.State, limit int) (*runstate.Lease, error)
}

func (s reservingStore) Reserve(ctx context.Context, state runstate.State, limit int) (*runstate.Lease, error) {
	return s.reserve(ctx, state, limit)
}

// memberPipeline is a pipeline over one member of a named epic, with its epic's
// progress kept as the harness wires it.
func memberPipeline(t *testing.T) (root string, pipeline Pipeline, store *runstate.Store, provider *orchestratortest.Backend, reserved func() []epicprogress.Reservation) {
	t.Helper()
	root = t.TempDir()
	operator, err := epicscope.ForOperator(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operator.Start(epicscope.Naming{Epic: namedEpic, From: "terminal", Members: []string{"yoyodyne-task"}}, baseTime); err != nil {
		t.Fatal(err)
	}
	progress, err := epicprogress.Open(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider = orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "change.txt"), []byte("changed\n"), 0o600)
	}, approveVerdict)
	pipeline, store = newPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})
	pipeline.MayStart = func(start maystart.Start) maystart.Decision { return maystart.Decide(root, "yoyodyne", start) }
	pipeline.EpicStarts = progress.ReserveRun
	return root, pipeline, store, provider, func() []epicprogress.Reservation {
		t.Helper()
		record, err := epicscope.Read(root, "yoyodyne")
		if err != nil {
			t.Fatal(err)
		}
		view, err := epicprogress.Read(root, "yoyodyne", record)
		if err != nil {
			t.Fatal(err)
		}
		entry, _ := view.Entry(epicprogress.Of(record.Epics[0]))
		return entry.Reservations
	}
}

func TestAMembersStartIsReservedBeforeItsRunIs(t *testing.T) {
	t.Parallel()
	_, pipeline, store, _, reserved := memberPipeline(t)
	seen := false
	pipeline.Store = reservingStore{store, func(ctx context.Context, state runstate.State, limit int) (*runstate.Lease, error) {
		seen = true
		recorded, err := store.Recorded()
		got := reserved()
		if err != nil || len(recorded) != 0 || len(got) != 1 || got[0].HeldBy != "the record of run "+state.RunID || got[0].Result != nil {
			t.Errorf("as the run was reserved the epic held %+v beside %d run(s), %v", got, len(recorded), err)
		}
		return store.Reserve(ctx, state, limit)
	}}
	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil || !seen {
		t.Fatalf("Run() = %+v, %v, reserved %t", outcome, err, seen)
	}
	if got := reserved(); len(got) != 1 || got[0].HeldBy != "the record of run "+outcome.RunID {
		t.Fatalf("afterwards the epic holds %+v", got)
	}
}

func TestARunThatWasRefusedLeavesNoReservation(t *testing.T) {
	t.Parallel()
	for name, refused := range map[string]error{
		"no slot":     runstate.CapacityError{Limit: 1, Active: 1},
		"a run of it": runstate.ExistingWorkItemError{},
		"a failure":   errors.New("the disk is full"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, pipeline, store, _, reserved := memberPipeline(t)
			pipeline.Store = reservingStore{store, func(context.Context, runstate.State, int) (*runstate.Lease, error) {
				if len(reserved()) != 1 {
					t.Error("the start was not reserved first")
				}
				return nil, refused
			}}
			if _, err := pipeline.Run(context.Background(), "yoyodyne-task"); err == nil {
				t.Fatal("the run started")
			}
			if got := reserved(); len(got) != 0 {
				t.Fatalf("the refused run left %+v", got)
			}
		})
	}
	// A failure that left the run's record leaves what points to it.
	_, pipeline, store, _, reserved := memberPipeline(t)
	pipeline.Store = reservingStore{store, func(ctx context.Context, state runstate.State, limit int) (*runstate.Lease, error) {
		lease, err := store.Reserve(ctx, state, limit)
		if err != nil {
			t.Fatal(err)
		}
		return nil, errors.Join(errors.New("the lease was lost"), lease.Release())
	}}
	if _, err := pipeline.Run(context.Background(), "yoyodyne-task"); err == nil || len(reserved()) != 1 {
		t.Fatalf("error = %v, reserved %+v", err, reserved())
	}
}

func TestAStartThatCannotBeWrittenDownIsNotMade(t *testing.T) {
	t.Parallel()
	root, pipeline, store, provider, _ := memberPipeline(t)
	// The epic's progress stops being readable after the decision was made.
	decide := pipeline.MayStart
	pipeline.MayStart = func(start maystart.Start) maystart.Decision {
		decision := decide(start)
		path := filepath.Join(home.ProductDirectory(root, "yoyodyne"), "epic-progress.json")
		if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
			t.Error(err)
		}
		return decision
	}
	_, err := pipeline.Run(context.Background(), "yoyodyne-task")
	var outside *OutsideNamedEpicsError
	if !errors.As(err, &outside) || !strings.Contains(err.Error(), "could not be written down") || !strings.Contains(err.Error(), "epic-progress.json") {
		t.Fatalf("error = %v", err)
	}
	if recorded, err := store.Recorded(); err != nil || len(recorded) != 0 || len(provider.Requests) != 0 {
		t.Fatalf("%d run(s) and %d request(s), %v", len(recorded), len(provider.Requests), err)
	}
}
