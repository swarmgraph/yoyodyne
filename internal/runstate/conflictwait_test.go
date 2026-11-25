package runstate

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A wait nobody can read costs the wait and never the run's record.
func TestAWaitThatCannotBeReadLeavesTheRunReadable(t *testing.T) {
	t.Parallel()

	state := testState(t, StatusRunning)
	at := time.Date(2026, 11, 25, 18, 0, 0, 0, time.UTC)
	entry := "mqe-" + strings.Repeat("a", 32)
	state.ConflictWait = &ConflictWait{EntryID: entry, Since: at, Until: at.Add(MaxConflictWait), Ahead: []ConflictWaitEntry{{
		EntryID: "mqe-" + strings.Repeat("b", 32), Order: 4, WorkItemID: "yoyodyne-1", Title: "Another change", Paths: []string{"a.go"},
	}}}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var kept State
	if err := json.Unmarshal(encoded, &kept); err != nil || kept.ConflictWait.Says() != state.ConflictWait.Says() {
		t.Fatalf("read back %#v, %v", kept.ConflictWait, err)
	}
	for _, broken := range []string{`"until":7`, `"until":"2030-01-01T00:00:00Z"`} {
		var read State
		changed := strings.Replace(string(encoded), `"until":"2026-11-25T20:00:00Z"`, broken, 1)
		if err := json.Unmarshal([]byte(changed), &read); err != nil || read.ConflictWait.Unreadable == "" || read.Validate() != nil {
			t.Fatalf("%s: wait %#v, %v; want it unreadable and the run valid", broken, read.ConflictWait, err)
		}
		if wait, recorded := read.ConflictWaitFor(entry); !recorded || wait.Says() != "" {
			t.Fatalf("%s: ConflictWaitFor() = %#v, %t", broken, wait, recorded)
		}
		again, err := json.Marshal(read)
		if err != nil || json.Unmarshal(again, &read) != nil || read.ConflictWait.Unreadable == "" {
			t.Fatalf("%s: an unreadable wait saved again reads as %#v, %v", broken, read.ConflictWait, err)
		}
	}
}
