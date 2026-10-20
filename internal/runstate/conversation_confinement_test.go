package runstate

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// The conversation store writes its lease, holder stamp, record, pictures and
// event log through the confined writer, with the state root as the root. These
// are the design's races run against each of those writers. The tests that set
// a hook do not run in parallel, because the hook sees every confined write in
// the process.

type conversationFixture struct {
	store         *ConversationStore
	conversation  Conversation
	identity      ConversationIdentity
	conversations string
	outside       string
}

// newConversationFixture is a store whose conversation directory already holds
// a record, a pending picture, a released lease and one event.
func newConversationFixture(t *testing.T) conversationFixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state, outside := filepath.Join(base, "state"), filepath.Join(base, "outside")
	for _, directory := range []string{state, outside} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	store := newConversationStore(t, state)
	conversation := testConversation(t)
	identity := conversation.Identity()
	if err := store.Save(conversation); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.SavePendingPictureText(identity, "picture\n"); err != nil {
		t.Fatalf("SavePendingPictureText() error = %v", err)
	}
	lease, err := store.Hold(identity)
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if err := store.AppendEvent(conversationEvent(t, conversation, 1, "")); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	return conversationFixture{store: store, conversation: conversation, identity: identity, conversations: store.Root(), outside: outside}
}

func conversationEvent(t *testing.T, conversation Conversation, sequence uint64, text string) execution.Event {
	t.Helper()
	var payload map[string]any
	if text != "" {
		payload = map[string]any{"text": text}
	}
	event, err := execution.NewEvent(conversation.ConversationID, sequence, time.Now().UTC(), execution.EventAgentMessage, "provider.claude-code", payload)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	return event
}

type conversationWriter struct {
	name  string
	write func(t *testing.T, f conversationFixture) error
}

func conversationWriters() []conversationWriter {
	return []conversationWriter{
		{"take the lease and stamp its holder", func(t *testing.T, f conversationFixture) error {
			lease, err := f.store.Hold(f.identity)
			if err == nil {
				err = lease.Release()
			}
			return err
		}},
		{"save the record", func(t *testing.T, f conversationFixture) error {
			return f.store.Save(f.conversation)
		}},
		{"write the pending picture", func(t *testing.T, f conversationFixture) error {
			return f.store.SavePendingPictureText(f.identity, "another picture\n")
		}},
		{"write the delivered picture", func(t *testing.T, f conversationFixture) error {
			return f.store.SaveDeliveredPictureText(f.identity, "delivered\n")
		}},
		{"clear the pending picture", func(t *testing.T, f conversationFixture) error {
			return f.store.ClearPendingPictureText(f.identity)
		}},
		{"append an event", func(t *testing.T, f conversationFixture) error {
			return f.store.AppendEvent(conversationEvent(t, f.conversation, 2, ""))
		}},
	}
}

func conversationListing(t *testing.T, directory string) string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(directory, func(candidate string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(directory, candidate)
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := os.ReadFile(candidate)
		if err != nil {
			return err
		}
		found = append(found, relative+"="+string(content))
		return nil
	})
	if err != nil {
		t.Fatalf("list %s: %v", directory, err)
	}
	sort.Strings(found)
	return strings.Join(found, "\n")
}

func hookConversationOnce(t *testing.T, point repowrite.Point, directory string, act func()) *bool {
	t.Helper()
	ran := new(bool)
	t.Cleanup(repowrite.SetTestHook(func(event repowrite.HookEvent) {
		if *ran || event.Point != point || event.Directory != directory {
			return
		}
		*ran = true
		act()
	}))
	return ran
}

func wantConversationRefusal(t *testing.T, err error, reason repowrite.Reason) {
	t.Helper()
	var refused *repowrite.RefusalError
	if !errors.As(err, &refused) || refused.Reason != reason {
		t.Fatalf("error = %v, want a refusal because %s", err, reason)
	}
}

// The conversation directory moved out of the state root on either side of a
// change: every conversation writer refuses, and the directory that is now
// outside holds exactly what it did before.
func TestConversationWritersRefuseAndUndoWhenTheirDirectoryLeavesTheStateRoot(t *testing.T) {
	for _, point := range []repowrite.Point{repowrite.PointBeforeChange, repowrite.PointAfterChange} {
		for _, writer := range conversationWriters() {
			t.Run(string(point)+"/"+writer.name, func(t *testing.T) {
				f := newConversationFixture(t)
				before := conversationListing(t, f.conversations)
				moved := filepath.Join(f.outside, "conversations")
				ran := hookConversationOnce(t, point, f.conversations, func() {
					if err := os.Rename(f.conversations, moved); err != nil {
						t.Errorf("move the conversation directory out: %v", err)
					}
				})
				err := writer.write(t, f)
				if !*ran {
					t.Fatalf("%s never reached %s (error %v)", writer.name, point, err)
				}
				wantConversationRefusal(t, err, repowrite.ReasonMoved)
				if after := conversationListing(t, moved); after != before {
					t.Fatalf("the conversation directory outside the state root changed:\n before: %s\n after:  %s", before, after)
				}
			})
		}
	}
}

// An event log that has a link from outside the state root is not appended to,
// and one that gains a link between two batches of a large event stops after
// the batch written before the link appeared.
func TestConversationEventAppendsStopAtALinkFromOutside(t *testing.T) {
	f := newConversationFixture(t)
	log, err := f.store.eventPathForConversation(f.conversation.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	stolen := filepath.Join(f.outside, "stolen.jsonl")
	ran := hookConversationOnce(t, repowrite.PointBetweenBatches, f.conversations, func() {
		if err := os.Link(log, stolen); err != nil {
			t.Errorf("link the event log from outside: %v", err)
		}
	})
	before, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	err = f.store.AppendEvent(conversationEvent(t, f.conversation, 2, strings.Repeat("x", 200<<10)))
	if !*ran {
		t.Fatal("a large event was appended in one batch")
	}
	wantConversationRefusal(t, err, repowrite.ReasonLinked)
	seen, err := os.ReadFile(stolen)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(seen), len(before)+64<<10; got != want {
		t.Fatalf("the outside link sees %d bytes, want the log before and one batch (%d)", got, want)
	}
	if err := f.store.AppendEvent(conversationEvent(t, f.conversation, 3, "")); err == nil {
		t.Fatal("an event was appended to a log linked from outside")
	}
	if after, err := os.ReadFile(stolen); err != nil || len(after) != len(seen) {
		t.Fatalf("the outside link grew after the append was stopped: %d bytes, %v", len(after), err)
	}
}

// The lease is a lock file: one with a second link is refused rather than
// shared with whatever holds the other link.
func TestConversationLeaseWithASecondLinkIsRefused(t *testing.T) {
	t.Parallel()
	f := newConversationFixture(t)
	lease, err := f.store.leaseFile(f.identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Link(lease, filepath.Join(f.outside, "lease")); err != nil {
		t.Fatal(err)
	}
	held, err := f.store.Hold(f.identity)
	if err == nil {
		held.Release()
	}
	wantConversationRefusal(t, err, repowrite.ReasonLinked)
}

// A record, holder stamp or picture linked from outside the state root is
// replaced by a new file, so the outside link keeps exactly what it had.
func TestConversationRecordsLinkedFromOutsideKeepTheirOutsideContent(t *testing.T) {
	t.Parallel()
	f := newConversationFixture(t)
	record, err := f.store.statePathFor(f.identity)
	if err != nil {
		t.Fatal(err)
	}
	picture, err := f.store.pendingPictureFile(f.identity)
	if err != nil {
		t.Fatal(err)
	}
	keptRecord, keptPicture := filepath.Join(f.outside, "record"), filepath.Join(f.outside, "picture")
	for from, to := range map[string]string{record: keptRecord, picture: keptPicture} {
		if err := os.Link(from, to); err != nil {
			t.Fatal(err)
		}
	}
	before := conversationListing(t, f.outside)
	f.conversation.UpdatedAt = f.conversation.UpdatedAt.Add(time.Hour)
	if err := f.store.Save(f.conversation); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := f.store.SavePendingPictureText(f.identity, "replaced\n"); err != nil {
		t.Fatalf("SavePendingPictureText() error = %v", err)
	}
	if after := conversationListing(t, f.outside); after != before {
		t.Fatalf("the outside links changed:\n before: %s\n after:  %s", before, after)
	}
	if text, err := f.store.PendingPictureText(f.identity); err != nil || text != "replaced\n" {
		t.Fatalf("PendingPictureText() = %q, %v", text, err)
	}
}

// Clearing a picture removes only the file it checked: one swapped in at the
// name before the removal is put back.
func TestConversationPictureClearRemovesOnlyTheFileItChecked(t *testing.T) {
	f := newConversationFixture(t)
	picture, err := f.store.pendingPictureFile(f.identity)
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(f.conversations, "other")
	if err := os.WriteFile(other, []byte("swapped in\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ran := hookConversationOnce(t, repowrite.PointBeforeRemove, f.conversations, func() {
		if err := os.Rename(other, picture); err != nil {
			t.Errorf("swap another file in: %v", err)
		}
	})
	err = f.store.ClearPendingPictureText(f.identity)
	if !*ran {
		t.Fatalf("the clear never reached its removal (error %v)", err)
	}
	wantConversationRefusal(t, err, repowrite.ReasonChanged)
	if content, err := os.ReadFile(picture); err != nil || string(content) != "swapped in\n" {
		t.Fatalf("what was swapped in reads %q, %v", content, err)
	}
}

// A conversation directory reached through a link out of the state root is
// refused by every writer, and nothing is written outside.
func TestConversationWritersRefuseAConversationDirectoryLinkedOutOfTheStateRoot(t *testing.T) {
	t.Parallel()
	for _, writer := range conversationWriters() {
		t.Run(writer.name, func(t *testing.T) {
			t.Parallel()
			f := newConversationFixture(t)
			escape := filepath.Join(f.outside, "conversations")
			if err := os.Rename(f.conversations, escape); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(escape, f.conversations); err != nil {
				t.Fatal(err)
			}
			before := conversationListing(t, escape)
			wantConversationRefusal(t, writer.write(t, f), repowrite.ReasonAbsoluteLink)
			if after := conversationListing(t, escape); after != before {
				t.Fatalf("a refused write changed the directory outside:\n before: %s\n after:  %s", before, after)
			}
		})
	}
}
