package runstate

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/intervention"
)

func handStepFor(t *testing.T, kind intervention.Kind, at time.Time) intervention.Event {
	t.Helper()
	id, err := intervention.NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	event := intervention.Event{
		SchemaVersion: intervention.SchemaVersion,
		ID:            id,
		ProductID:     "calc",
		Kind:          kind,
		At:            at,
		Items:         []string{"calc-1"},
		Said:          fmt.Sprintf("a %s step", kind),
		Via:           "the command line",
		RecordedAt:    at,
	}
	if kind.OutsideOnly() {
		event.Via = ""
		event.Observed = true
		event.By = "Mason"
		event.RecordedBy = "Mason"
	}
	return event
}

// The record is durable state: what one process wrote is what a process
// started afterwards reads, in the order the steps were taken, and writing more
// leaves every line already there exactly as it was.
func TestHandStepsSurviveARestartAndAreNeverRewritten(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	base := time.Date(2026, 10, 6, 16, 0, 0, 0, time.UTC)

	first, err := NewInterventionStore(root, "calc")
	if err != nil {
		t.Fatalf("NewInterventionStore() error = %v", err)
	}
	if listed, err := first.List(); err != nil || len(listed) != 0 {
		t.Fatalf("List() on a product nobody recorded = %v, %v; want nothing and no error", listed, err)
	}
	run := handStepFor(t, intervention.KindRun, base)
	stop := handStepFor(t, intervention.KindStop, base.Add(time.Minute))
	for _, event := range []intervention.Event{stop, run} {
		if err := first.Record(event); err != nil {
			t.Fatalf("Record(%s) error = %v", event.Kind, err)
		}
	}
	before, err := os.ReadFile(first.Path())
	if err != nil {
		t.Fatalf("read the log: %v", err)
	}

	// A process started afterwards: nothing is shared but the state root.
	restarted, err := NewInterventionStore(root, "calc")
	if err != nil {
		t.Fatalf("NewInterventionStore() error = %v", err)
	}
	restart := handStepFor(t, intervention.KindRestart, base.Add(2*time.Minute))
	if err := restarted.Record(restart); err != nil {
		t.Fatalf("Record(restart) error = %v", err)
	}
	listed, err := restarted.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	var kinds []string
	for _, event := range listed {
		kinds = append(kinds, string(event.Kind))
	}
	if got := strings.Join(kinds, ","); got != "run,stop,restart" {
		t.Fatalf("listed %s, want the three steps in the order they were taken", got)
	}
	if !listed[2].Observed || listed[2].By != "Mason" {
		t.Errorf("the restart read back as %+v, want it marked observed and taken by Mason", listed[2])
	}
	after, err := os.ReadFile(restarted.Path())
	if err != nil {
		t.Fatalf("read the log: %v", err)
	}
	if !bytes.HasPrefix(after, before) {
		t.Errorf("recording another step rewrote what was already there:\nbefore %s\nafter  %s", before, after)
	}
}

func TestTheStoreRefusesAStepItCannotStandBehind(t *testing.T) {
	t.Parallel()
	store, err := NewInterventionStore(t.TempDir(), "calc")
	if err != nil {
		t.Fatalf("NewInterventionStore() error = %v", err)
	}
	other := handStepFor(t, intervention.KindRun, time.Now().UTC())
	other.ProductID = "other"
	if err := store.Record(other); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("Record(another product's step) error = %v", err)
	}
	unsaid := handStepFor(t, intervention.KindRun, time.Now().UTC())
	unsaid.Said = ""
	if err := store.Record(unsaid); err == nil {
		t.Error("Record(a step nobody described) was accepted")
	}
	if _, err := os.Stat(store.Path()); !os.IsNotExist(err) {
		t.Errorf("a refused step left a log behind: %v", err)
	}
}

// Two processes recording at once each write a whole line.
func TestConcurrentHandStepsAreEachRecordedWhole(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const writers = 16
	var wait sync.WaitGroup
	errs := make(chan error, writers)
	for index := 0; index < writers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			store, err := NewInterventionStore(root, "calc")
			if err != nil {
				errs <- err
				return
			}
			errs <- store.Record(handStepFor(t, intervention.KindSettle, time.Now().UTC()))
		}(index)
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Record() error = %v", err)
		}
	}
	store, _ := NewInterventionStore(root, "calc")
	listed, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != writers {
		t.Fatalf("listed %d steps, want %d", len(listed), writers)
	}
}

// What a merged change is, read without pricing anything: the items with a run
// that integrated, and every run each one had.
func TestPromotedNamesEveryRunOfAnItemThatShipped(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	base := time.Date(2026, 10, 6, 16, 0, 0, 0, time.UTC)

	failed := testState(t, StatusFailed)
	failed.WorkItemID = "calc-1"
	failed.StartedAt, failed.UpdatedAt = base, base
	shipped := integratedState(t, PhaseCleaningUp)
	shipped.WorkItemID = "calc-1"
	shipped.StartedAt, shipped.UpdatedAt = base.Add(time.Hour), base.Add(time.Hour)
	unshipped := testState(t, StatusFailed)
	unshipped.WorkItemID = "calc-2"
	for _, state := range []State{failed, shipped, unshipped} {
		if err := store.Create(state); err != nil {
			t.Fatalf("Create(%s) error = %v", state.RunID, err)
		}
	}
	promoted, err := store.Promoted()
	if err != nil {
		t.Fatalf("Promoted() error = %v", err)
	}
	if len(promoted) != 1 || promoted[0].WorkItemID != "calc-1" || promoted[0].ShippedRunID != shipped.RunID {
		t.Fatalf("Promoted() = %+v, want calc-1 alone, shipped by %s", promoted, shipped.RunID)
	}
	if got := strings.Join(promoted[0].RunIDs, ","); got != failed.RunID+","+shipped.RunID {
		t.Errorf("runs of calc-1 = %s, want both of its runs, oldest first", got)
	}
}
