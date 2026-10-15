package testharness

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Expected classes belong only in the assertion. The scenarios using this
// helper ask the pipeline or sweep to produce and persist the ending itself.
func AssertSavedStopClass(t *testing.T, store interface {
	Load(string) (runstate.State, error)
}, runID string, want runstate.StopClass) {
	t.Helper()
	state, err := store.Load(runID)
	if err != nil {
		t.Fatalf("Load(%s) error = %v", runID, err)
	}
	if !state.Status.Terminal() || state.CompletedAt == nil || state.StopClass != want {
		t.Fatalf("saved ending = %q, completed at %v, stop_class = %q; want a terminal run with %q", state.Status, state.CompletedAt, state.StopClass, want)
	}
}
