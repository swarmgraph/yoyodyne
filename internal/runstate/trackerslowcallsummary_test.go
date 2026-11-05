package runstate

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// Sample records, as the tracker client leaves them, read together: which
// callers appear most often, how long their calls ran, and what held the
// store.
func TestRecordedSlowCallsAreSummarizedInOrdinaryWords(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	listing := beads.StoreHolder{PID: 10, Command: "/usr/local/bin/bd list --json --limit=0 --status=open"}
	export := beads.StoreHolder{PID: 11, Command: "bd export -o .beads/issues.jsonl"}
	call := func(minutes int, who, verb string, ran time.Duration, holders ...beads.StoreHolder) TrackerSlowCall {
		return TrackerSlowCall{SchemaVersion: TrackerSlowCallSchemaVersion, ProductID: "calc", SlowCall: beads.SlowCall{
			Call: "a call", Verb: verb, For: who, StartedAt: start.Add(time.Duration(minutes) * time.Minute),
			Ran: ran, Limit: 2 * time.Minute, StoreBytes: int64(900+minutes) << 20, Holders: holders,
		}}
	}
	calls := []TrackerSlowCall{
		call(0, "the development manager's recurring pass development-manager-sweep#804", "list", 2*time.Minute, listing, export),
		call(10, "the development manager's recurring pass development-manager-sweep#805", "list", 90*time.Second, listing, listing),
		call(20, "developer run run-1 for calc-1", "show", 2*time.Minute, export),
		call(30, "the development manager's recurring pass development-manager-sweep#806", "ready", 2*time.Minute),
		call(40, "", "export", 2*time.Minute, listing),
	}
	deadlined := call(50, "yoyo reconcile", "list", 20*time.Second)
	deadlined.CallerDeadline = 20 * time.Second
	unread := call(60, "yoyo reconcile", "list", 2*time.Minute)
	unread.HoldersUnread = "lsof could not be run"
	calls = append(calls, deadlined, unread)

	summary := SummarizeSlowCalls(calls)
	if summary.Calls != 7 || summary.Held != 4 || summary.Alone != 1 || summary.ByCaller != 1 || summary.Unknown != 1 {
		t.Fatalf("summary = %+v, want seven calls: four held, one alone, one by its caller's deadline, one unknown", summary)
	}
	first := summary.Callers[0]
	if first.Caller != "the development manager's recurring pass development-manager-sweep" || first.Calls != 3 || first.Shortest != 90*time.Second || first.Longest != 2*time.Minute {
		t.Errorf("first caller = %+v, want the development manager's three firings together", first)
	}
	if len(summary.Holders) != 2 || summary.Holders[0] != (SlowHolder{Process: "bd list", Calls: 3}) || summary.Holders[1] != (SlowHolder{Process: "bd export", Calls: 2}) {
		t.Errorf("holders = %+v, want bd list in three calls' way and bd export in two", summary.Holders)
	}
	said := strings.Join(summary.Says(), "\n")
	for _, want := range []string{
		"7 tracker calls ran out of time between",
		"4 of them found another process holding the store",
		"1 found none, so the tracker itself was too slow",
		"the development manager's recurring pass development-manager-sweep: 3 calls; they ran from 1m30s to 2m0s",
		"developer runs: 1 call; it ran 2m0s",
		"a call that named no caller (bd export): 1 call",
		"yoyo reconcile: 2 calls",
		"bd list, while 3 calls waited",
		"bd export, while 2 calls waited",
		"the largest store any of them saw was 960 MB",
	} {
		if !strings.Contains(said, want) {
			t.Errorf("Says() =\n%s\nwant %q in it", said, want)
		}
	}
	if got := SummarizeSlowCalls(nil).Says(); len(got) != 1 || !strings.Contains(got[0], "No tracker call") {
		t.Errorf("Says() of nothing = %v", got)
	}
}
