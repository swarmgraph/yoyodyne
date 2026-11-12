package runstate

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// A stop recorded on a run in flight that has not taken effect is on the run's
// summary, which is what the status listing prints, and says when it was asked
// for in the operator's local time with the zone named, and the latest it takes
// effect. A run that has ended carries none.
func TestAPendingStopIsOnTheSummaryOfARunInFlight(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	running := testState(t, StatusRunning)
	ended := testState(t, StatusCancelled)
	completed := ended.StartedAt
	ended.CompletedAt = &completed
	for _, state := range []State{running, ended} {
		if err := store.Create(state); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}
	asked := time.Date(2026, 11, 11, 12, 57, 0, 0, time.UTC)
	for _, state := range []State{running, ended} {
		if err := store.RecordStop(StopRequest{
			SchemaVersion: StopSchemaVersion,
			ProductID:     domain.ProductID("yoyodyne"),
			RunID:         state.RunID,
			WorkItemID:    state.WorkItemID,
			RequestedAt:   asked,
			RequestedBy:   "the development manager",
			Reason:        "superseded",
		}); err != nil {
			t.Fatalf("RecordStop() error = %v", err)
		}
	}
	history, err := store.History(RunQuery{})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	for _, run := range history.Runs {
		switch run.RunID {
		case running.RunID:
			if run.PendingStop == nil {
				t.Fatalf("summary = %#v, want the stop not yet taken effect on it", run)
			}
			says := run.PendingStop.Pending()
			for _, want := range []string{
				"the development manager asked this run to stop at " + asked.Local().Format("2006-01-02 15:04 MST"),
				"has not taken effect yet",
				"no later than " + asked.Add(StopTakesEffectWithin).Local().Format("2006-01-02 15:04 MST"),
				": superseded",
			} {
				if !strings.Contains(says, want) {
					t.Fatalf("Pending() = %q, want it to say %q", says, want)
				}
			}
		case ended.RunID:
			if run.PendingStop != nil {
				t.Fatalf("summary of an ended run = %#v, want no stop said to be pending", run.PendingStop)
			}
		}
	}
}

// A late stop is recorded through the store's narrow update, read back whole,
// and recorded once however many times it is noticed.
func TestALateStopIsRecordedOnce(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	asked := time.Date(2026, 11, 11, 12, 57, 0, 0, time.UTC)
	late := NewLateStop(StopRequest{RequestedAt: asked, Reason: " superseded "}, asked.Add(time.Hour), asked.Add(90*time.Minute))
	written, err := store.Update(context.Background(), state.RunID, LateStopRecorded{Stop: late, At: late.NoticedAt})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if written.LateStop == nil || *written.LateStop != late || late.RequestedBy != "the operator" || late.Reason != "superseded" {
		t.Fatalf("late stop = %#v, want %#v as asked by the operator", written.LateStop, late)
	}
	again, err := store.Update(context.Background(), state.RunID, LateStopRecorded{Stop: late, At: late.NoticedAt.Add(time.Minute)})
	if err != nil || again.Revision != written.Revision {
		t.Fatalf("second Update() = revision %d, %v, want nothing written (revision %d)", again.Revision, err, written.Revision)
	}
	if says := late.Says(); !strings.Contains(says, "after its checks had passed at "+LocalStamp(late.ChecksPassedAt)) ||
		!strings.Contains(says, "went on to its review rather than being cancelled") {
		t.Fatalf("Says() = %q, want when the checks passed and what became of the change", says)
	}
	if _, err := store.Update(context.Background(), state.RunID, LateStopRecorded{Stop: LateStop{}, At: asked}); err == nil {
		t.Fatal("Update() with an empty late stop error = nil, want it refused")
	}
}
