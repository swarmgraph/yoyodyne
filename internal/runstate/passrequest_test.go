package runstate

import (
	"testing"
	"time"
)

func askedRequest(t *testing.T, store *PassRequestStore, at time.Time) PassRequest {
	t.Helper()
	id, err := NewPassRequestID()
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := store.Request(PassRequest{
		SchemaVersion: PassRequestSchemaVersion,
		ProductID:     "example",
		ID:            id,
		Agent:         "technical-health-pm",
		AskedBy:       PassAskerProductManager,
		RequestedAt:   at,
	})
	if err != nil {
		t.Fatalf("Request() error = %v", err)
	}
	return recorded
}

// A request reads as waiting until a pass is taken from it, as taken while
// that pass runs, and as the outcome its pass ended with once it ends, each
// with when; a request that joined another reads as joined.
func TestAPassRequestSaysWhatBecameOfIt(t *testing.T) {
	t.Parallel()

	store, err := NewPassRequestStore(t.TempDir(), "example")
	if err != nil {
		t.Fatal(err)
	}
	asked := time.Date(2026, 10, 18, 4, 22, 23, 0, time.UTC)
	request := askedRequest(t, store, asked)
	if state, since := request.State(); state != PassRequestStateWaiting || !since.Equal(asked) {
		t.Fatalf("State() = %q, %s, want waiting since it was asked", state, since)
	}
	joined := askedRequest(t, store, asked.Add(time.Minute))
	if state, _ := joined.State(); state != PassRequestStateJoined || joined.JoinedTo != request.ID {
		t.Fatalf("State() = %q joined to %q, want joined to %s", state, joined.JoinedTo, request.ID)
	}

	taken, err := store.Take(request.Agent, request.ID, "technical-health-pm#2", asked.Add(30*time.Second))
	if err != nil {
		t.Fatalf("Take() error = %v", err)
	}
	if state, since := taken.State(); state != PassRequestStateTaken || !since.Equal(asked.Add(30*time.Second)) || !taken.Open() {
		t.Fatalf("State() = %q, %s, open %v; want taken, still open, since the pass was taken", state, since, taken.Open())
	}

	closed, err := store.Close(request.Agent, request.ID, "technical-health-pm#2", PassRequestFailed, "a turn of the pass was not answered with its account", asked.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if state, since := closed.State(); state != PassRequestStateFailed || !since.Equal(asked.Add(5*time.Minute)) {
		t.Fatalf("State() = %q, %s, want failed since the pass ended", state, since)
	}
	if _, err := store.Take(request.Agent, request.ID, "technical-health-pm#3", asked.Add(time.Hour)); err == nil {
		t.Error("Take() of a closed request succeeded, want it refused")
	}
}

// A wait recorded on a request a stopped pass had taken puts it back to
// waiting: the pass that never ended is not what it waits on.
func TestAWaitOnATakenRequestReadsAsWaiting(t *testing.T) {
	t.Parallel()

	store, err := NewPassRequestStore(t.TempDir(), "example")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 18, 4, 0, 0, 0, time.UTC)
	request := askedRequest(t, store, at)
	if _, err := store.Take(request.Agent, request.ID, "technical-health-pm#1", at); err != nil {
		t.Fatal(err)
	}
	waiting, err := store.Wait(request.Agent, request.ID, "the provider has answered nobody", at.Add(time.Hour))
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if state, _ := waiting.State(); state != PassRequestStateWaiting || waiting.Pass != "" || waiting.TakenAt != nil {
		t.Errorf("request = %+v (%q), want it waiting with no pass named", waiting, state)
	}
}
