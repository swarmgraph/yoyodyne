package runstate

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestTheChecksTheQueueRanAgainAreKeptPerEntryAndReadBack(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newMergeQueueStore(t, root)
	entry := "mqe-" + strings.Repeat("a", 32)
	other := "mqe-" + strings.Repeat("b", 32)
	if reruns, err := store.CheckReruns(mainQueue, entry); err != nil || len(reruns) != 0 {
		t.Fatalf("CheckReruns() before any = %#v, %v; want none", reruns, err)
	}
	at := time.Date(2026, 11, 9, 19, 4, 0, 0, time.UTC)
	if err := store.RecordCheckRerun(context.Background(), mainQueue, entry, MergeQueueCheckRerun{PullRequest: 1697, Check: "build", CheckRun: 77, Conclusion: "cancelled", At: at}); err != nil {
		t.Fatalf("RecordCheckRerun() error = %v", err)
	}
	reruns, err := newMergeQueueStore(t, root).CheckReruns(mainQueue, entry)
	if err != nil || len(reruns) != 1 || reruns[0].Check != "build" || reruns[0].PullRequest != 1697 || !reruns[0].At.Equal(at) {
		t.Fatalf("CheckReruns() = %#v, %v; want the build's re-run read back", reruns, err)
	}
	if others, err := store.CheckReruns(mainQueue, other); err != nil || len(others) != 0 {
		t.Fatalf("another entry's re-runs = %#v, %v; want none", others, err)
	}
	if err := store.RecordCheckRerun(context.Background(), mainQueue, "../escape", MergeQueueCheckRerun{PullRequest: 1, Check: "build", At: at}); err == nil {
		t.Fatal("a re-run was recorded under a name that is not an entry")
	}
	if err := store.RecordCheckRerun(context.Background(), mainQueue, entry, MergeQueueCheckRerun{At: at}); err == nil {
		t.Fatal("a re-run naming no pull request or check was recorded")
	}
}
