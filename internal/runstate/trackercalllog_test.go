package runstate

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

func TestTrackerCallsAreKeptPerProcessAndCountedByCaller(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	watch, err := NewTrackerCallLogStore(root, "calc", "yoyo watch")
	if err != nil {
		t.Fatal(err)
	}
	chat, err := NewTrackerCallLogStore(root, "calc", "yoyo chat")
	if err != nil {
		t.Fatal(err)
	}
	chat.pid = watch.pid + 1
	hour := time.Date(2026, 11, 4, 14, 0, 0, 0, time.UTC)
	watch.LogCall(beads.LoggedCall{At: hour.Add(5 * time.Minute), For: "developer run run-1 for calc-1", Call: "listing the work items ready to start", Verb: "ready", Took: 3 * time.Second})
	watch.LogCall(beads.LoggedCall{At: hour.Add(6 * time.Minute), For: "the developer run for calc-2", Call: "reading calc-2", Verb: "show", Took: time.Second})
	watch.LogCall(beads.LoggedCall{At: hour.Add(7 * time.Minute), Call: "listing the work items ready to start", Verb: "ready", Took: 2 * time.Minute, StoppedAtLimit: true})
	chat.LogCall(beads.LoggedCall{At: hour.Add(8 * time.Minute), For: "the architect's recurring pass architect-pass#313", Call: "reading calc-3", Verb: "show", Took: 4 * time.Second})
	chat.LogCall(beads.LoggedCall{At: hour.Add(9 * time.Minute), For: "the architect's recurring pass architect-pass#314", Call: "reading calc-4", Verb: "show", Took: 6 * time.Second})
	// Outside the hour read.
	chat.LogCall(beads.LoggedCall{At: hour.Add(61 * time.Minute), For: "the architect's recurring pass architect-pass#315", Call: "reading calc-5", Verb: "show", Took: time.Second})
	if err := errorsOf(watch, chat); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(watch.Path())
	if err != nil || len(entries) != 3 {
		t.Fatalf("files = %v, %v, want one per process for each hour", entries, err)
	}
	// A line a process was still writing when it was read.
	half := filepath.Join(watch.Path(), "20261104T14-"+strconv.Itoa(watch.pid)+".jsonl")
	appendTo(t, half, `{"schema_version":1,"product_id":"calc","process":"yoyo wa`)

	records, unreadable, err := watch.Read(hour, hour.Add(time.Hour))
	if err != nil || len(records) != 5 || unreadable != 1 {
		t.Fatalf("Read() = %d records, %d unreadable, %v; want five and the half line", len(records), unreadable, err)
	}
	count := CountTrackerCalls(records, hour, hour.Add(time.Hour), unreadable)
	if count.Calls != 5 || len(count.Callers) != 3 {
		t.Fatalf("count = %+v, want five calls from three callers", count)
	}
	runs, architect, watched := count.Callers[0], count.Callers[1], count.Callers[2]
	if runs.Caller != "developer runs" || runs.Calls != 2 || runs.Took != 4*time.Second || runs.Longest != 3*time.Second {
		t.Errorf("first caller = %+v, want both developer runs counted together", runs)
	}
	if architect.Caller != "the architect's recurring pass architect-pass" || architect.Calls != 2 {
		t.Errorf("second caller = %+v, want every firing of the architect's pass counted together", architect)
	}
	if watched.Caller != "yoyo watch" || watched.Stopped != 1 {
		t.Errorf("third caller = %+v, want a call that named nobody counted against its process", watched)
	}
	said := count.Says()
	for _, want := range []string{"asked the tracker 5 times", "developer runs 2", "yoyo watch 1", "1 stopped at their limit", "1 logged lines could not be read"} {
		if !strings.Contains(said, want) {
			t.Errorf("Says() = %q, want %q in it", said, want)
		}
	}
}

func TestOldTrackerCallFilesAreRemovedWhenAProcessStartsANewHour(t *testing.T) {
	t.Parallel()
	store, err := NewTrackerCallLogStore(t.TempDir(), "calc", "yoyo watch")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 4, 14, 0, 0, 0, time.UTC)
	store.LogCall(beads.LoggedCall{At: now.Add(-50 * time.Hour), Call: "old", Verb: "show"})
	store.LogCall(beads.LoggedCall{At: now.Add(-47 * time.Hour), Call: "kept", Verb: "show"})
	store.LogCall(beads.LoggedCall{At: now, Call: "new", Verb: "show"})
	if err := store.Failed(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(store.Path())
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if len(names) != 2 || strings.HasPrefix(names[0], "20261102T12") {
		t.Errorf("files = %v, want the one older than two days removed", names)
	}
}

func TestANamelessCallIsCountedAgainstItsProcess(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ who, process, want string }{
		{"", "yoyo dashboard", "yoyo dashboard"},
		{"", "", "a call that named no caller"},
		{"yoyo reconcile", "yoyo reconcile", "yoyo reconcile"},
		{"developer run run-abc for calc-1", "yoyo watch", "developer runs"},
		{"the development manager's recurring pass development-manager-sweep#804", "", "the development manager's recurring pass development-manager-sweep"},
	} {
		if got := CallerGroup(test.who, test.process); got != test.want {
			t.Errorf("CallerGroup(%q, %q) = %q, want %q", test.who, test.process, got, test.want)
		}
	}
}

func errorsOf(stores ...*TrackerCallLogStore) error {
	for _, store := range stores {
		if err := store.Failed(); err != nil {
			return err
		}
	}
	return nil
}

func appendTo(t *testing.T, path, text string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(text); err != nil {
		t.Fatal(err)
	}
}
