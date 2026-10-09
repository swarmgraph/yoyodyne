package runstate

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// The hold is met by one dispatch and read by every pull after it, so it has to
// survive the process that noticed it. A second finding on the same provider,
// version, and build is the same hold and does not open it again, which is what
// keeps it to one report; a finding on another version is a different cause and
// opens afresh; and clearing it leaves nothing standing.
func TestALaunchSettingsHoldOpensOncePerCauseAndClears(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := NewLaunchSettingsHoldStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewLaunchSettingsHoldStore() error = %v", err)
	}
	if _, standing, err := store.Standing(); err != nil || standing {
		t.Fatalf("Standing() = %t, %v; want nothing on a fresh state root", standing, err)
	}

	since := time.Date(2026, 10, 9, 15, 0, 0, 0, time.UTC)
	observed := LaunchSettingsObservation{
		Provider: domain.BackendClaudeCode, Version: "2.2.0 (Claude Code)", Build: "4d7e805",
		NotInForce: []string{"the notes guard is not among the hooks it will run"},
		Waiting:    "the dispatch of yoyodyne-one", At: since,
	}
	first, opened, err := store.Notice(observed)
	if err != nil || !opened || first.Refusals != 1 || !first.Since.Equal(since) {
		t.Fatalf("Notice() = %#v, %t, %v; want a hold opened now", first, opened, err)
	}

	observed.At = since.Add(time.Hour)
	observed.Waiting = "the dispatch of yoyodyne-two"
	reopened, err := NewLaunchSettingsHoldStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	again, opened, err := reopened.Notice(observed)
	if err != nil || opened || again.Refusals != 2 || !again.Since.Equal(since) || !again.LastSeen.Equal(observed.At) || again.Waiting != "the dispatch of yoyodyne-one" {
		t.Fatalf("Notice() = %#v, %t, %v; want the same hold confirmed, not opened", again, opened, err)
	}
	if said := again.Says(); !strings.Contains(said, "claude-code") || !strings.Contains(said, "2.2.0 (Claude Code)") || !strings.Contains(said, "notes guard") {
		t.Fatalf("Says() = %q, want the provider, its version, and what did not take", said)
	}

	observed.Version = "2.2.1 (Claude Code)"
	if _, opened, err := reopened.Notice(observed); err != nil || !opened {
		t.Fatalf("Notice() on another version opened = %t, %v; want a new hold", opened, err)
	}

	cleared, found, err := reopened.Clear()
	if err != nil || !found || cleared.Version != "2.2.1 (Claude Code)" {
		t.Fatalf("Clear() = %#v, %t, %v; want the standing hold lifted", cleared, found, err)
	}
	if _, standing, err := store.Standing(); err != nil || standing {
		t.Fatalf("Standing() after Clear() = %t, %v; want nothing", standing, err)
	}
	if _, found, err := store.Clear(); err != nil || found {
		t.Fatalf("Clear() of nothing = %t, %v; want no error and nothing found", found, err)
	}
}

// A hold names what did not take; one that names nothing is refused.
func TestALaunchSettingsHoldNamesWhatDidNotTake(t *testing.T) {
	t.Parallel()
	store, err := NewLaunchSettingsHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Notice(LaunchSettingsObservation{Provider: domain.BackendClaudeCode, Version: "2.2.0"}); err == nil {
		t.Fatal("a hold naming nothing that did not take was recorded")
	}
}

// Dispatches refused at the same moment open the hold once between them, so
// one cause is one report however many slots met it.
func TestConcurrentRefusalsOpenALaunchSettingsHoldOnce(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const dispatches = 8
	opened := make(chan bool, dispatches)
	var wait sync.WaitGroup
	for range dispatches {
		wait.Add(1)
		go func() {
			defer wait.Done()
			store, err := NewLaunchSettingsHoldStore(root, "yoyodyne")
			if err != nil {
				t.Error(err)
				return
			}
			_, fresh, err := store.Notice(LaunchSettingsObservation{
				Provider: domain.BackendClaudeCode, Version: "2.2.0 (Claude Code)",
				NotInForce: []string{"the sandbox is not running"},
			})
			if err != nil {
				t.Error(err)
				return
			}
			opened <- fresh
		}()
	}
	wait.Wait()
	close(opened)
	count := 0
	for fresh := range opened {
		if fresh {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("%d refusals opened the hold, want exactly one", count)
	}
}
