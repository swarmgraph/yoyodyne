package runstate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/home"
)

func newPassCursorStore(t *testing.T) (*PassCursorStore, string) {
	t.Helper()
	root := t.TempDir()
	store, err := NewPassCursorStore(root, "example")
	if err != nil {
		t.Fatalf("NewPassCursorStore() error = %v", err)
	}
	return store, root
}

// A cursor is written under the instance's own directory beside its lane
// report, read back as it was written, and moves only the streams it is given.
func TestAPassCursorIsWrittenAndReadBackPerStream(t *testing.T) {
	t.Parallel()

	store, root := newPassCursorStore(t)
	at := time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)

	if _, found, err := store.Load("reliability-pm"); err != nil || found {
		t.Fatalf("Load() = %v, %v; want an instance never watched to have no cursor", found, err)
	}
	if _, err := store.Advance(context.Background(), "reliability-pm", map[string]time.Time{PassStreamRuns: at, PassStreamTracker: at}, nil, at); err != nil {
		t.Fatalf("Advance() error = %v", err)
	}
	if _, err := store.Advance(context.Background(), "reliability-pm", map[string]time.Time{PassStreamRuns: at.Add(time.Hour)}, nil, at.Add(time.Hour)); err != nil {
		t.Fatalf("Advance() error = %v", err)
	}
	cursor, found, err := store.Load("reliability-pm")
	if err != nil || !found {
		t.Fatalf("Load() = %v, %v", found, err)
	}
	if !cursor.Streams[PassStreamRuns].Equal(at.Add(time.Hour)) || !cursor.Streams[PassStreamTracker].Equal(at) {
		t.Errorf("streams = %v, want the runs moved and the tracker left", cursor.Streams)
	}
	if _, err := os.Stat(filepath.Join(home.ProductDirectory(root, "example"), "program-managers", "reliability-pm", "cursor.json")); err != nil {
		t.Errorf("the cursor is not beside the instance's lane report: %v", err)
	}
	if store.Path("reliability-pm") != filepath.Join(home.ProductDirectory(root, "example"), "program-managers", "reliability-pm", "cursor.json") {
		t.Errorf("Path() = %s", store.Path("reliability-pm"))
	}
}

// A position is never moved backwards: a later pass settling first must not be
// undone by an earlier one settling after it.
func TestAPassCursorNeverMovesBackwards(t *testing.T) {
	t.Parallel()

	store, _ := newPassCursorStore(t)
	at := time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)
	if _, err := store.Advance(context.Background(), "reliability-pm", map[string]time.Time{PassStreamRuns: at}, nil, at); err != nil {
		t.Fatalf("Advance() error = %v", err)
	}
	cursor, err := store.Advance(context.Background(), "reliability-pm", map[string]time.Time{PassStreamRuns: at.Add(-time.Hour)}, nil, at)
	if err != nil {
		t.Fatalf("Advance() error = %v", err)
	}
	if !cursor.Streams[PassStreamRuns].Equal(at) {
		t.Errorf("runs = %s, want it kept at %s", cursor.Streams[PassStreamRuns], at)
	}
}

// A stream the pass machinery does not read is refused rather than kept as a
// position nothing moves.
func TestAPassCursorRefusesAStreamNothingReads(t *testing.T) {
	t.Parallel()

	store, _ := newPassCursorStore(t)
	at := time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)
	if _, err := store.Advance(context.Background(), "reliability-pm", map[string]time.Time{"forge": at}, nil, at); err == nil || !strings.Contains(err.Error(), "not one a pass reads") {
		t.Fatalf("Advance() error = %v, want the stream refused", err)
	}
}

// A cursor that cannot be read is an error rather than an absent cursor: read
// past as absent, it would hand the next pass nothing or everything.
func TestAnUnreadablePassCursorIsAnError(t *testing.T) {
	t.Parallel()

	store, _ := newPassCursorStore(t)
	path := store.Path("reliability-pm")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Load("reliability-pm"); err == nil || found {
		t.Fatalf("Load() = %v, %v; want the unreadable cursor reported", found, err)
	}
}

// A stream is read again from PassLateness behind its position, never from
// before it began to be watched, and what a completed pass carried is kept
// while it is inside that reach and dropped once it falls out of it.
func TestAPassCursorRemembersWhatWasCarriedInsideTheReach(t *testing.T) {
	t.Parallel()

	store, _ := newPassCursorStore(t)
	watched := time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)
	if _, err := store.Advance(context.Background(), "reliability-pm", map[string]time.Time{PassStreamTracker: watched}, nil, watched); err != nil {
		t.Fatalf("Advance() error = %v", err)
	}
	cursor, _, _ := store.Load("reliability-pm")
	if !cursor.ReadFrom(PassStreamTracker).Equal(watched) {
		t.Errorf("ReadFrom() = %s, want no reach back before the stream was watched", cursor.ReadFrom(PassStreamTracker))
	}

	passed := watched.Add(time.Hour)
	cursor, err := store.Advance(context.Background(), "reliability-pm", map[string]time.Time{PassStreamTracker: passed},
		map[string]map[string]time.Time{PassStreamTracker: {"yoyodyne-ifd.1": passed.Add(-time.Minute)}}, passed)
	if err != nil {
		t.Fatalf("Advance() error = %v", err)
	}
	if !cursor.ReadFrom(PassStreamTracker).Equal(passed.Add(-PassLateness)) || !cursor.WasCarried(PassStreamTracker, "yoyodyne-ifd.1") {
		t.Fatalf("cursor = %+v, want the reach behind the position and the carried entry remembered", cursor)
	}
	if !cursor.WatchedFrom[PassStreamTracker].Equal(watched) {
		t.Errorf("watched from = %s, want it kept at %s", cursor.WatchedFrom[PassStreamTracker], watched)
	}

	later := passed.Add(time.Hour)
	cursor, err = store.Advance(context.Background(), "reliability-pm", map[string]time.Time{PassStreamTracker: later}, nil, later)
	if err != nil {
		t.Fatalf("Advance() error = %v", err)
	}
	if cursor.WasCarried(PassStreamTracker, "yoyodyne-ifd.1") {
		t.Errorf("carried = %v, want an entry outside the reach dropped", cursor.Carried)
	}
}
