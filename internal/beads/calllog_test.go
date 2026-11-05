package beads

import (
	"context"
	"sync"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

type keptCalls struct {
	mu    sync.Mutex
	calls []LoggedCall
}

func (k *keptCalls) LogCall(call LoggedCall) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.calls = append(k.calls, call)
}

// Once a process names a log, every client in it hands the log each call it
// made to the live store, with who it was for; a client pinned to a
// measurement's copy hands it nothing.
//
// Not parallel: the log is the whole process's.
func TestEveryClientInAProcessLogsItsCallsWithWhoTheyWereFor(t *testing.T) {
	kept := &keptCalls{}
	LogCallsTo(kept)
	t.Cleanup(func() { LogCallsTo(nil) })
	runner := &recordingRunner{answer: func(execution.Command) execution.ProcessResult {
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "[]"}
	}}
	ctx := OnBehalfOf(context.Background(), "the architect's recurring pass architect-pass#313")
	if _, err := (Client{Runner: runner}).run(ctx, "ready", "--json"); err != nil {
		t.Fatal(err)
	}
	if _, err := (Client{Runner: runner, beadsDir: t.TempDir()}).run(ctx, "list", "--json"); err != nil {
		t.Fatal(err)
	}
	if len(kept.calls) != 1 {
		t.Fatalf("logged %+v, want the one call to the live store", kept.calls)
	}
	call := kept.calls[0]
	if call.For != "the architect's recurring pass architect-pass#313" || call.Verb != "ready" || call.Call != "listing the work items ready to start" || call.At.IsZero() || call.StoppedAtLimit {
		t.Errorf("logged %+v", call)
	}

	LogCallsTo(nil)
	if _, err := (Client{Runner: runner}).run(ctx, "ready", "--json"); err != nil {
		t.Fatal(err)
	}
	if len(kept.calls) != 1 {
		t.Errorf("logged %d calls after the log was taken away, want none more", len(kept.calls))
	}
}
