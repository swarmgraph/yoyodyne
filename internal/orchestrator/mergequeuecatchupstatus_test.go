package orchestrator

// Bringing a handed-back change up to date, whatever status its item reads.
//
// An approved change waiting in the merge queue keeps its run, and the store
// gives that run's lease to it alone; an item that reads open while it waits has
// only had its claim released. The attempt to bring the change up to date claims
// it again rather than refusing. A closed item is still refused, and is told
// so and left closed.

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// handBackWithItemReading admits an approved change, has the item read status,
// moves the target under the change, and passes the queue over it.
func handBackWithItemReading(t *testing.T, status string) (*queueRun, Outcome) {
	t.Helper()
	r, _ := newCatchUpRun(t, resolveBothSides)
	admitted := r.run(t)
	if admitted.MergeQueue == nil {
		t.Fatalf("Run() = %#v, want the approved change admitted to the queue", admitted)
	}
	r.tracker.SetItemStatus(status)
	conflictOnTarget(t, r.repository, "something else\n")
	if pass := r.catchUpPass(t, admitted.MergeQueue.Key()); pass.Outcome != runstate.MergeQueuePassHandedBack {
		t.Fatalf("Pass() = %#v, want the conflicting change handed back", pass)
	}
	return r, admitted
}

func TestAHandedBackChangeWhoseItemReadsOpenIsBroughtUpToDate(t *testing.T) {
	t.Parallel()
	r, admitted := handBackWithItemReading(t, "open")
	put, err := r.runs.Load(admitted.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if put.Status != runstate.StatusRunning || put.Phase != runstate.PhaseDeveloping || strings.TrimSpace(put.Blocker) != "" || !catchUpNotTakenUp(put) {
		t.Fatalf("run = status %q, phase %q, blocker %q; want it put back live to bring its change up to date", put.Status, put.Phase, put.Blocker)
	}
	record := r.tracker.Record()
	if claims := strings.Count(strings.Join(record.Calls, ","), "claim"); record.Blocked || claims != 2 || record.Item.Status != "in_progress" {
		t.Fatalf("blocked = %t, claims = %d, status = %q; want the item claimed again for the run, not blocked", record.Blocked, claims, record.Item.Status)
	}
	if !strings.Contains(record.Notes, "read open rather than in progress when the harness came to bring the change up to date") || !strings.Contains(record.Notes, "gets one of its 3 attempts to bring the change up to date") {
		t.Fatalf("notes:\n%s\nwant the item told why it was claimed again and that its run is catching up", record.Notes)
	}
}

func TestAHandedBackChangeWhoseItemIsClosedIsStillRefused(t *testing.T) {
	t.Parallel()
	r, admitted := handBackWithItemReading(t, "closed")
	put, err := r.runs.Load(admitted.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if put.Status == runstate.StatusRunning || catchUpNotTakenUp(put) {
		t.Fatalf("run = status %q, phase %q; want no attempt to bring the change up to date for a closed item", put.Status, put.Phase)
	}
	// The item is told why, once, and left closed.
	record := r.tracker.Record()
	if record.Item.Status != "closed" || strings.Contains(record.Notes, "claimed it again") || strings.Contains(record.Notes, "gets one of its 3 attempts") {
		t.Fatalf("status = %q, notes:\n%s\nwant the closed item left as it reads and not claimed", record.Item.Status, record.Notes)
	}
	if !strings.Contains(record.Notes, "because its item is closed rather than in progress") || !strings.Contains(record.Notes, "so it is left closed") {
		t.Fatalf("notes:\n%s\nwant the item told the attempt was refused because it is closed", record.Notes)
	}
}
