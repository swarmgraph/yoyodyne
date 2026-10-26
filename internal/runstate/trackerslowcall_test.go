package runstate

import (
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// Each slow call is its own record, read back oldest first with everything it
// found, and the oldest are removed past the number kept.
func TestSlowTrackerCallsAreRecordedReadInOrderAndBounded(t *testing.T) {
	t.Parallel()
	store, err := NewTrackerSlowCallStore(t.TempDir(), "calc")
	if err != nil {
		t.Fatal(err)
	}
	if calls, err := store.Read(); err != nil || len(calls) != 0 {
		t.Fatalf("Read() of a product with no slow calls = %+v, %v, want nothing", calls, err)
	}
	store.keep = 2
	started := time.Date(2026, 10, 25, 15, 27, 0, 0, time.UTC)
	held := beads.SlowCall{
		Call: "exporting every work item", Verb: "export", For: "yoyo reconcile",
		StartedAt: started, Ran: 30 * time.Second, Limit: 30 * time.Second,
		Store: "/repo/.beads/embeddeddolt", StoreBytes: 885 << 20,
		Holders: []beads.StoreHolder{{PID: 38601, Command: "bd show yoyodyne-ifd --json"}},
	}
	alone := held
	alone.StartedAt, alone.Holders = started.Add(time.Minute), nil
	unmeasured := alone
	unmeasured.StartedAt, unmeasured.StoreBytes, unmeasured.StoreUnmeasured = started.Add(2*time.Minute), 0, "no tracker store was found"
	for _, call := range []beads.SlowCall{alone, held, unmeasured} {
		if err := store.RecordSlowCall(call); err != nil {
			t.Fatalf("RecordSlowCall() error = %v", err)
		}
	}

	calls, err := store.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("Read() = %d calls, want the newest 2", len(calls))
	}
	if !calls[0].StartedAt.Equal(alone.StartedAt) || calls[0].Held() || calls[0].StoreBytes != 885<<20 || calls[0].ProductID != "calc" {
		t.Errorf("first call = %+v, want the one nothing held", calls[0])
	}
	if calls[1].StoreUnmeasured != "no tracker store was found" || calls[1].Ran != 30*time.Second {
		t.Errorf("second call = %+v, want the unmeasured one", calls[1])
	}
	if err := store.RecordSlowCall(beads.SlowCall{Verb: "list"}); err == nil {
		t.Error("RecordSlowCall() of a call with no start time = nil, want it refused")
	}
}
