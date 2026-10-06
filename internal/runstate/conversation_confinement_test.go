package runstate

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func TestConversationEventAppendRefusesHardLinkReplacement(t *testing.T) {
	t.Parallel()
	store := newConversationStore(t, t.TempDir())
	conversation := testConversation(t)
	event, err := execution.NewEvent(conversation.ConversationID, 1, conversation.StartedAt, execution.EventAgentMessage, "test", map[string]string{"message": "changed"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(event); err != nil {
		t.Fatal(err)
	}
	path, err := store.eventPathForConversation(conversation.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := conversationTreeSnapshot(t, outside)
	called := false
	store.beforeMutation = func() {
		called = true
		replaced := make(chan error)
		go func() {
			if err := os.Rename(path, path+"-held"); err != nil {
				replaced <- err
				return
			}
			replaced <- os.Link(sentinel, path)
		}()
		if err := <-replaced; err != nil {
			t.Fatal(err)
		}
	}
	err = store.AppendEvent(event)
	if !called {
		t.Fatal("event append did not reach the replacement barrier")
	}
	if err == nil || !strings.Contains(err.Error(), "hard link") {
		t.Errorf("append error = %v, want refusal of the shared inode", err)
	}
	if after := conversationTreeSnapshot(t, outside); !reflect.DeepEqual(before, after) {
		t.Errorf("event append escaped through a hard link: before = %v, after = %v", before, after)
	}
	if held, err := os.ReadFile(path + "-held"); err != nil || string(held) != string(original) {
		t.Errorf("original event log changed: %q, %v", held, err)
	}
}

func TestConversationMutationsKeepThePinnedDirectoryAfterReplacement(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"record", "pending picture", "delivered picture", "clear pending", "clear delivered", "event", "lease and holder"} {
		for _, existing := range []bool{false, true} {
			name := operation + "/missing directory"
			if existing {
				name = operation + "/existing directory"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				store := newConversationStore(t, t.TempDir())
				conversation := testConversation(t)
				identity := conversation.Identity()
				event, err := execution.NewEvent(conversation.ConversationID, 1, conversation.StartedAt, execution.EventAgentMessage, "test", map[string]string{"message": "changed"})
				if err != nil {
					t.Fatal(err)
				}
				if existing {
					if err := store.Save(conversation); err != nil {
						t.Fatal(err)
					}
					if err := store.SavePendingPictureText(identity, "before"); err != nil {
						t.Fatal(err)
					}
					if err := store.SaveDeliveredPictureText(identity, "before"); err != nil {
						t.Fatal(err)
					}
				}
				outside := t.TempDir()
				for _, file := range []string{identity.Agent + ".json", identity.Agent + ".picture", identity.Agent + ".delivered", identity.Agent + ".lease", identity.Agent + ".holder", conversation.ConversationID + ".events.jsonl"} {
					if err := os.WriteFile(filepath.Join(outside, file), []byte("keep"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				before := conversationTreeSnapshot(t, outside)
				called := false
				store.beforeMutation = func() {
					if called {
						t.Fatal("operation unexpectedly reopened its pathname")
					}
					called = true
					replaced := make(chan error)
					go func() {
						if err := os.Rename(store.Root(), store.Root()+"-held"); err != nil {
							replaced <- err
							return
						}
						replaced <- os.Symlink(outside, store.Root())
					}()
					if err := <-replaced; err != nil {
						t.Fatal(err)
					}
				}
				switch operation {
				case "record":
					err = store.Save(conversation)
				case "pending picture":
					err = store.SavePendingPictureText(identity, "changed")
				case "delivered picture":
					err = store.SaveDeliveredPictureText(identity, "changed")
				case "clear pending":
					err = store.ClearPendingPictureText(identity)
				case "clear delivered":
					err = store.ClearDeliveredPictureText(identity)
				case "event":
					err = store.AppendEvent(event)
				case "lease and holder":
					var lease *Lease
					lease, err = store.Hold(identity)
					if err == nil {
						err = lease.Release()
					}
				}
				if err != nil {
					t.Fatalf("mutation of held directory: %v", err)
				}
				if !existing && (operation == "clear pending" || operation == "clear delivered") {
					if called {
						t.Fatal("removing a missing picture created its directory")
					}
					if _, err := os.Lstat(store.Root()); !errors.Is(err, fs.ErrNotExist) {
						t.Fatalf("missing conversation directory was created: %v", err)
					}
					return
				}
				if !called {
					t.Fatal("operation did not reach the replacement barrier")
				}
				if operation == "lease and holder" {
					if _, err := os.Stat(filepath.Join(store.Root()+"-held", identity.Agent+".holder")); !errors.Is(err, fs.ErrNotExist) {
						t.Fatalf("holder survived release in its original directory: %v", err)
					}
					if _, err := os.Stat(filepath.Join(store.Root()+"-held", identity.Agent+".lease")); err != nil {
						t.Fatalf("lease inode did not outlive release: %v", err)
					}
				}
				if after := conversationTreeSnapshot(t, outside); !reflect.DeepEqual(before, after) {
					t.Fatalf("conversation mutation escaped: before = %v, after = %v", before, after)
				}
				entries, err := os.ReadDir(store.Root() + "-held")
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					if entry.Name()[0] == '.' {
						t.Fatalf("temporary file left in held directory: %s", entry.Name())
					}
				}
			})
		}
	}
}

func TestQueuedConversationClaimAndReleaseUseTheSameDirectoryAfterReplacement(t *testing.T) {
	t.Parallel()
	stateRoot := t.TempDir()
	store := newConversationStore(t, stateRoot)
	identity := testConversation(t).Identity()
	first, err := store.Hold(identity)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	leaseInfo, err := first.file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	contender := newConversationStore(t, stateRoot)
	queued := make(chan struct{})
	contender.queued = func() { close(queued) }
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	type result struct {
		lease *Lease
		err   error
	}
	claimed := make(chan result)
	finished := make(chan struct{})
	t.Cleanup(func() { <-finished })
	go func() {
		defer close(finished)
		lease, err := contender.take(ctx, identity, waitingForConversation)
		select {
		case claimed <- result{lease, err}:
		case <-ctx.Done():
			lease.Release()
		}
	}()
	select {
	case <-queued:
	case <-ctx.Done():
		t.Fatal("second claim never queued")
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, identity.Agent+".holder"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := conversationTreeSnapshot(t, outside)
	if err := os.Rename(store.Root(), store.Root()+"-held"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, store.Root()); err != nil {
		t.Fatal(err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	var second result
	select {
	case second = <-claimed:
	case <-ctx.Done():
		t.Fatal("queued claim did not acquire the released lease")
	}
	if second.err != nil {
		t.Fatal(second.err)
	}
	defer second.lease.Release()
	secondInfo, err := second.lease.file.Stat()
	if err != nil || !os.SameFile(leaseInfo, secondInfo) {
		t.Fatalf("queued claim changed lock inode: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.Root()+"-held", identity.Agent+".holder")); err != nil {
		t.Fatalf("second holder was not stamped in its lease directory: %v", err)
	}
	if err := second.lease.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(store.Root()+"-held", identity.Agent+".holder")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("holder survived release: %v", err)
	}
	if after := conversationTreeSnapshot(t, outside); !reflect.DeepEqual(before, after) {
		t.Fatalf("claim or release escaped: before = %v, after = %v", before, after)
	}
}

func conversationTreeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			result[relative] = "directory"
		} else {
			content, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			result[relative] = string(content)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
