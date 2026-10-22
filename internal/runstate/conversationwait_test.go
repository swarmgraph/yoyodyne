package runstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
	"github.com/mason-bryant/yoyodyne/internal/repowrite/writertest"
)

func TestWaitingConversationTurnsAreIndependentOfTheConversationAndEachOther(t *testing.T) {
	t.Parallel()
	store := newConversationStore(t, t.TempDir())
	conversation := testConversation(t)
	if err := store.Save(conversation); err != nil {
		t.Fatal(err)
	}
	var clear []func() error
	for _, reason := range []string{"waiting out seven_day", "waiting for the provider to answer"} {
		hold, err := store.Claim(context.Background(), conversation.Identity())
		if err != nil {
			t.Fatal(err)
		}
		end, err := hold.Waiting(conversation.ConversationID, reason, time.Now())
		if err != nil {
			hold.Release()
			t.Fatal(err)
		}
		clear = append(clear, end)
		if err := hold.Release(); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		for _, end := range clear {
			end()
		}
	}()
	if recorded, err := store.Recorded(); err != nil || len(recorded) != 1 {
		t.Fatalf("waits were mistaken for conversations: %+v, %v", recorded, err)
	}
	if held, err := store.InFlight(conversation.Identity()); err != nil || held {
		t.Fatalf("a waiting turn still holds the conversation: %v, %v", held, err)
	}
	if waits, err := store.WaitingTurns(); err != nil || len(waits) != 2 || waits[0].PID != os.Getpid() {
		t.Fatalf("independent waits = %+v, %v", waits, err)
	}
	if err := clear[0](); err != nil {
		t.Fatal(err)
	}
	if waits, err := store.WaitingTurns(); err != nil || len(waits) != 1 {
		t.Fatalf("ending one wait ended another: %+v, %v", waits, err)
	}
	if err := clear[1](); err != nil {
		t.Fatal(err)
	}
	if waits, err := store.WaitingTurns(); err != nil || len(waits) != 0 {
		t.Fatalf("ended waits remain: %+v, %v", waits, err)
	}
}

func TestADeadProcessDoesNotLeaveAConversationTurnWaiting(t *testing.T) {
	t.Parallel()
	const gone = 2147483647
	if running, err := processIsRunning(gone); err != nil || running {
		t.Skip("this platform cannot establish the absence of the test's process")
	}
	store := newConversationStore(t, t.TempDir())
	conversation := testConversation(t)
	wait := ConversationWait{Agent: conversation.Identity().Agent, Role: conversation.Role,
		ConversationID: conversation.ConversationID, PID: gone, Since: time.Now(), Reason: "waiting for the provider"}
	path := filepath.Join(store.Root(), ".waiting-"+conversation.ConversationID+".json")
	if err := replaceJSONFile(stateRoot{given: store.stateRoot, confined: store.stateRoot, anchor: store.anchor}, path, "conversation wait", wait); err != nil {
		t.Fatal(err)
	}
	if waits, err := store.WaitingTurns(); err != nil || len(waits) != 0 {
		t.Fatalf("the dead process still reads as waiting: %+v, %v", waits, err)
	}
}

func TestWaitingConversationTurnsTolerateFutureFieldsAndRefuseCorruptRecords(t *testing.T) {
	t.Parallel()
	store := newConversationStore(t, t.TempDir())
	conversation := testConversation(t)
	wait := ConversationWait{Agent: conversation.Identity().Agent, Role: conversation.Role,
		ConversationID: conversation.ConversationID, PID: os.Getpid(), Since: time.Now(), Reason: "waiting for the provider"}
	path := filepath.Join(store.Root(), ".waiting-"+conversation.ConversationID+".json")
	encoded, err := json.Marshal(wait)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(encoded, &record); err != nil {
		t.Fatal(err)
	}
	record["future_field"] = true
	if err := replaceJSONFile(stateRoot{given: store.stateRoot, confined: store.stateRoot, anchor: store.anchor}, path, "conversation wait", record); err != nil {
		t.Fatal(err)
	}
	if waits, err := store.WaitingTurns(); err != nil || len(waits) != 1 || waits[0].ConversationID != wait.ConversationID || waits[0].Reason != wait.Reason {
		t.Fatalf("future field waits = %+v, %v", waits, err)
	}
	for _, corrupt := range []string{"{", string(encoded) + " {}", strings.Replace(string(encoded), `"pid":`, `"pid":"invalid","ignored_pid":`, 1), strings.Repeat(" ", maxEncodedStateBytes+1)} {
		if err := os.WriteFile(path, []byte(corrupt), 0o600); err != nil {
			t.Fatal(err)
		}
		if waits, err := store.WaitingTurns(); err == nil || len(waits) != 0 {
			t.Fatalf("corrupt waits = %+v, %v", waits, err)
		}
	}
}

func TestConversationWaitWritesStayInsideTheStateRoot(t *testing.T) {
	wait := conversationWaitFixture(t)
	name := ".waiting-" + wait.ConversationID + ".json"
	writertest.Run(t, writertest.Writer{
		Name: "conversation wait", Directory: "products/yoyodyne/conversations", File: name,
		Write: func(t *testing.T, directory string) error {
			store := newConversationStore(t, directory)
			root, err := store.pinWaitRoot()
			if err != nil {
				return err
			}
			defer root.Close()
			return store.recordWaitIn(root, name, wait)
		},
	})
}

func TestConversationWaitRefusesAReplacedWriteDirectory(t *testing.T) {
	for _, parent := range []bool{false, true} {
		for _, replacement := range []string{"symlink", "directory"} {
			t.Run(fmt.Sprintf("parent=%t/%s", parent, replacement), func(t *testing.T) {
				t.Parallel()
				store := newConversationStore(t, t.TempDir())
				root, err := store.pinWaitRoot()
				if err != nil {
					t.Fatal(err)
				}
				defer root.Close()
				held, current := replaceConversationWaitDirectory(t, store, parent, replacement)
				wait := conversationWaitFixture(t)
				// This is the creation after pinning and before publishing. Both
				// parent replacement and replacement of the directory itself fail.
				if err := store.recordWaitIn(root, ".waiting-"+wait.ConversationID+".json", wait); err == nil {
					t.Fatal("a replaced wait directory was accepted")
				}
				for _, path := range []string{held, current} {
					if entries, err := os.ReadDir(path); err != nil || len(entries) != 0 {
						t.Fatalf("records appeared after a refused replacement in %s: %v, %v", path, entries, err)
					}
				}
				if replacement == "symlink" {
					if pinned, err := store.pinWaitRoot(); err == nil {
						pinned.Close()
						t.Fatal("pinning followed the replacement symlink")
					}
				}
			})
		}
	}
}

func TestConversationWaitCleanupNeverReachesAReplacementDirectory(t *testing.T) {
	for _, parent := range []bool{false, true} {
		for _, replacement := range []string{"symlink", "directory"} {
			t.Run(fmt.Sprintf("parent=%t/%s", parent, replacement), func(t *testing.T) {
				t.Parallel()
				store := newConversationStore(t, t.TempDir())
				conversation := testConversation(t)
				hold, err := store.Claim(context.Background(), conversation.Identity())
				if err != nil {
					t.Fatal(err)
				}
				defer hold.Release()
				clear, err := hold.Waiting(conversation.ConversationID, "waiting for the provider", time.Now())
				if err != nil {
					t.Fatal(err)
				}
				defer clear()
				if err := hold.Release(); err != nil {
					t.Fatal(err)
				}
				entries, err := os.ReadDir(store.Root())
				if err != nil {
					t.Fatal(err)
				}
				var name string
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), ".waiting-chat-") {
						name = entry.Name()
					}
				}
				if name == "" {
					t.Fatal("no wait was recorded")
				}
				held, current := replaceConversationWaitDirectory(t, store, parent, replacement)
				sentinel := filepath.Join(current, name)
				if err := os.WriteFile(sentinel, []byte("keep this\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				// The pinned directory is no longer at the path beneath the state
				// root it was pinned by, so the cleanup is refused rather than made
				// in the replacement or wherever the original went: the wait stays
				// in the directory nothing reads any more, and nothing else moves.
				err = clear()
				var refused *repowrite.RefusalError
				if !errors.As(err, &refused) || refused.Reason != repowrite.ReasonMoved {
					t.Fatalf("cleanup after the directory was replaced = %v, want a refusal because it moved", err)
				}
				if content, err := os.ReadFile(sentinel); err != nil || string(content) != "keep this\n" {
					t.Fatalf("cleanup changed a replacement directory: %q, %v", content, err)
				}
				if _, err := os.Stat(filepath.Join(held, name)); err != nil {
					t.Fatalf("a refused cleanup removed the wait from its original directory: %v", err)
				}
				if repeated := clear(); repeated == nil || repeated.Error() != err.Error() {
					t.Fatalf("repeated cleanup = %v, want the same refusal", repeated)
				}
			})
		}
	}
}

func conversationWaitFixture(t *testing.T) ConversationWait {
	t.Helper()
	conversation := testConversation(t)
	return ConversationWait{Agent: conversation.Identity().Agent, Role: conversation.Role,
		ConversationID: conversation.ConversationID, PID: os.Getpid(), Since: time.Now(), Reason: "waiting for the provider"}
}

func replaceConversationWaitDirectory(t *testing.T, store *ConversationStore, parent bool, replacement string) (held, current string) {
	t.Helper()
	target := store.Root()
	if parent {
		target = filepath.Dir(target)
	}
	moved := target + "-held"
	if err := os.Rename(target, moved); err != nil {
		t.Fatal(err)
	}
	held, current = moved, target
	if replacement == "symlink" {
		current = t.TempDir()
		if err := os.Symlink(current, target); err != nil {
			t.Fatal(err)
		}
	}
	if parent {
		held = filepath.Join(held, filepath.Base(store.Root()))
		current = filepath.Join(current, filepath.Base(store.Root()))
	}
	if err := os.MkdirAll(current, 0o700); err != nil {
		t.Fatal(err)
	}
	return held, current
}
