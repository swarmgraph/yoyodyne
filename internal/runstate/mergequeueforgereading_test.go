package runstate

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestTheLatestGitHubReadingIsKeptPerEntryAndReadBack(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newMergeQueueStore(t, root)
	entry := "mqe-" + strings.Repeat("c", 32)
	if _, found, err := store.ForgeReading(mainQueue, entry); err != nil || found {
		t.Fatalf("ForgeReading() before any = %t, %v; want none", found, err)
	}
	at := time.Date(2026, 11, 13, 14, 4, 0, 0, time.UTC)
	held := MergeQueueForgeReading{PullRequest: 1828, At: at, WaitingOn: `the required check "build" has not finished`, MergeState: "BLOCKED", PendingChecks: []string{"build"}}
	if err := store.RecordForgeReading(context.Background(), mainQueue, entry, held); err != nil {
		t.Fatalf("RecordForgeReading() error = %v", err)
	}
	unread := MergeQueueForgeReading{PullRequest: 1828, At: at.Add(time.Minute), Unread: "x509: certificate signed by unknown authority", UnreadFor: time.Minute}
	if err := store.RecordForgeReading(context.Background(), mainQueue, entry, unread); err != nil {
		t.Fatalf("RecordForgeReading() error = %v", err)
	}
	got, found, err := newMergeQueueStore(t, root).ForgeReading(mainQueue, entry)
	if err != nil || !found || got.Unread == "" || got.UnreadFor != time.Minute || !got.At.Equal(at.Add(time.Minute)) {
		t.Fatalf("ForgeReading() = %#v, %t, %v; want the newer, failed reading kept", got, found, err)
	}
	if says := got.Says(); !strings.Contains(says, "could not be asked about pull request 1828") || !strings.Contains(says, "x509") {
		t.Errorf("Says() = %q, want the failed read said with its reason", says)
	}
	if says := held.Says(); !strings.Contains(says, "GitHub said the merge of pull request 1828 was waiting on") || !strings.Contains(says, "build") {
		t.Errorf("Says() = %q, want what GitHub said", says)
	}
	for _, refused := range []MergeQueueForgeReading{
		{At: at, WaitingOn: "x"},
		{PullRequest: 1, WaitingOn: "x"},
		{PullRequest: 1, At: at},
		{PullRequest: 1, At: at, WaitingOn: "x", Unread: "y"},
	} {
		if err := store.RecordForgeReading(context.Background(), mainQueue, entry, refused); err == nil {
			t.Errorf("RecordForgeReading(%#v) = nil, want it refused", refused)
		}
	}
	if err := store.RecordForgeReading(context.Background(), mainQueue, "../escape", held); err == nil {
		t.Error("a reading was recorded under a name that is not an entry")
	}
}
