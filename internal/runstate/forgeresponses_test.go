package runstate

import (
	"os"
	"testing"
	"time"
)

// Answers kept by one check are what the next check opens, and an answer
// nobody has asked about for a day is dropped when the file is written.
func TestForgeResponsesAreKeptBetweenChecksAndDroppedOnceUnused(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	now := time.Date(2026, 11, 3, 16, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	store, afresh, err := OpenForgeResponses(root, "calc", clock)
	if err != nil || afresh != "" {
		t.Fatalf("OpenForgeResponses() = %q, %v", afresh, err)
	}
	store.Keep("repos/{owner}/{repo}/pulls/7 | .state", `W/"a"`, `{"state":"open"}`)
	store.Keep("repos/{owner}/{repo}/pulls/8 | .state", `W/"b"`, `{"state":"open"}`)
	if err := store.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	now = now.Add(23 * time.Hour)
	next, _, err := OpenForgeResponses(root, "calc", clock)
	if err != nil {
		t.Fatal(err)
	}
	if tag, body, found := next.Cached("repos/{owner}/{repo}/pulls/7 | .state"); !found || tag != `W/"a"` || body != `{"state":"open"}` {
		t.Fatalf("Cached() = %q, %q, %t; want the kept answer", tag, body, found)
	}
	now = now.Add(2 * time.Hour)
	if err := next.Save(); err != nil {
		t.Fatal(err)
	}
	last, _, err := OpenForgeResponses(root, "calc", clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, found := last.Cached("repos/{owner}/{repo}/pulls/8 | .state"); found {
		t.Fatal("an answer unused for over a day was kept")
	}
	if _, _, found := last.Cached("repos/{owner}/{repo}/pulls/7 | .state"); !found {
		t.Fatal("an answer used within the day was dropped")
	}
}

// A file that cannot be read starts with nothing kept and says why, rather
// than refusing the check: it costs full reads and nothing else.
func TestUnreadableForgeResponsesStartAfresh(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store, _, err := OpenForgeResponses(root, "calc", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	again, afresh, err := OpenForgeResponses(root, "calc", nil)
	if err != nil || afresh == "" || again.Len() != 0 {
		t.Fatalf("OpenForgeResponses() = %d kept, %q, %v; want none kept and the reason said", again.Len(), afresh, err)
	}
}
