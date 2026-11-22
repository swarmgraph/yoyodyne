package runstate

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

const invocationTestRun = "run-0123456789abcdef0123456789abcdef"

func newInvocationStore(t *testing.T) (*InvocationStore, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(root, "product")
	if err != nil {
		t.Fatal(err)
	}
	return store.Invocations(), filepath.Join(filepath.Dir(store.Root()), "invocations")
}

func sentWith(system, workItem string) backend.SentInvocation {
	return backend.SentInvocation{
		Backend: "claude-code", ProviderVersion: "2.1.286", Model: "claude-test", Posture: "worktree-write",
		Messages: []backend.SentMessage{
			{Role: backend.SentSystem, Parts: []backend.SentPart{{Name: "system instructions", Origin: "the harness", Text: system + "\n\n"}, {Name: "persona", Origin: "the project", Text: "be careful"}}},
			{Role: backend.SentUser, Parts: []backend.SentPart{{Name: "first message", Origin: "built for this invocation", Text: workItem}}},
		},
	}
}

func partFiles(t *testing.T, directory string) []string {
	t.Helper()
	var found []string
	_ = filepath.WalkDir(filepath.Join(directory, "parts"), func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			found = append(found, path)
		}
		return nil
	})
	return found
}

// What is read back is exactly what was recorded, and a part two invocations
// share is stored once.
func TestAnInvocationIsReadBackAsItWasSentAndSharedPartsAreStoredOnce(t *testing.T) {
	t.Parallel()
	store, directory := newInvocationStore(t)
	started := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
	first, err := store.Record(invocationTestRun, domain.RoleDeveloper, "abc123", started, sentWith("the contract", "work item, as read at nine\nwith a second line"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Record(invocationTestRun, domain.RoleReviewer, "def456", started.Add(time.Hour), sentWith("the contract", "the change under review"))
	if err != nil {
		t.Fatal(err)
	}
	if first.Number != 1 || second.Number != 2 {
		t.Fatalf("numbers = %d, %d, want 1, 2", first.Number, second.Number)
	}
	// Contract, persona, and two different first messages: four distinct texts.
	if files := partFiles(t, directory); len(files) != 4 {
		t.Fatalf("%d part files stored, want 4 (the contract and persona shared): %v", len(files), files)
	}
	listed, err := store.List(invocationTestRun)
	if err != nil || len(listed) != 2 || listed[0].Role != "developer" || listed[1].Role != "reviewer" || listed[1].Commit != "def456" {
		t.Fatalf("list = %+v, %v", listed, err)
	}
	got, err := store.Text(invocationTestRun, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := sentWith("the contract", "work item, as read at nine\nwith a second line")
	if len(got.Messages) != 2 {
		t.Fatalf("messages = %+v", got.Messages)
	}
	for index, message := range want.Messages {
		if got.Messages[index].Role != message.Role || got.Messages[index].Text != message.Text() {
			t.Fatalf("message %d = %q, sent %q", index, got.Messages[index].Text, message.Text())
		}
	}
	if got.Entry.WholeSize != len(want.Messages[0].Text())+len(want.Messages[1].Text()) || got.Entry.ProviderVersion != "2.1.286" {
		t.Fatalf("entry = %+v", got.Entry)
	}
}

// A run with nothing recorded lists nothing, and a number it never had is said.
func TestARunWithNoInvocationRecordedReadsAsNone(t *testing.T) {
	t.Parallel()
	store, _ := newInvocationStore(t)
	if listed, err := store.List(invocationTestRun); err != nil || len(listed) != 0 {
		t.Fatalf("list = %+v, %v", listed, err)
	}
	if _, err := store.Text(invocationTestRun, 3); !errors.Is(err, ErrNoInvocation) {
		t.Fatalf("error = %v, want ErrNoInvocation", err)
	}
	if _, err := store.Record("../outside", domain.RoleDeveloper, "", time.Now(), sentWith("a", "b")); err == nil {
		t.Fatal("a run id that is not a run id was accepted")
	}
}

// A part that is gone, or that no longer matches its digest, is said plainly and
// nothing is shown in its place.
func TestAMissingOrAlteredPartIsReportedAndNeverFilledIn(t *testing.T) {
	t.Parallel()
	for _, damage := range []string{"missing", "altered"} {
		store, directory := newInvocationStore(t)
		if _, err := store.Record(invocationTestRun, domain.RoleDeveloper, "", time.Now(), sentWith("the contract", "the work item")); err != nil {
			t.Fatal(err)
		}
		files := partFiles(t, directory)
		switch damage {
		case "missing":
			if err := os.Remove(files[0]); err != nil {
				t.Fatal(err)
			}
		case "altered":
			other := filepath.Join(directory, "other.gz")
			if err := os.WriteFile(other, []byte("not gzip"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(other, files[0]); err != nil {
				t.Fatal(err)
			}
		}
		got, err := store.Text(invocationTestRun, 1)
		var broken InvocationRecordError
		if !errors.As(err, &broken) || len(got.Messages) != 0 {
			t.Fatalf("%s: err = %v, messages = %+v, want a plain report and no text", damage, err, got.Messages)
		}
		if damage == "missing" && !strings.Contains(err.Error(), "is missing from the store") {
			t.Fatalf("%s: %v", damage, err)
		}
	}
}

// Text swapped for other text under the same name is caught by the digest.
func TestAPartWhoseContentChangedNoLongerMatchesItsDigest(t *testing.T) {
	t.Parallel()
	store, directory := newInvocationStore(t)
	if _, err := store.Record(invocationTestRun, domain.RoleDeveloper, "", time.Now(), sentWith("the contract", "the work item")); err != nil {
		t.Fatal(err)
	}
	// Put the stored text of one part under the name of another.
	files := partFiles(t, directory)
	first, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(files[1]); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(files[1], first, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = store.Text(invocationTestRun, 1)
	if err == nil || !strings.Contains(err.Error(), "no longer matches its digest") {
		t.Fatalf("error = %v, want the mismatch said", err)
	}
}

// A record that cannot be written is an error, so the caller does not start.
func TestAnInvocationThatCannotBeWrittenIsAnError(t *testing.T) {
	t.Parallel()
	store, directory := newInvocationStore(t)
	if err := os.MkdirAll(filepath.Dir(directory), 0o700); err != nil {
		t.Fatal(err)
	}
	// A file where the store's directory goes.
	if err := os.WriteFile(directory, []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Record(invocationTestRun, domain.RoleDeveloper, "", time.Now(), sentWith("a", "b")); err == nil {
		t.Fatal("a record that could not be written was reported as written")
	}
	err := store.Recorder(invocationTestRun, domain.RoleDeveloper, "", nil).RecordInvocation(sentWith("a", "b"))
	if err == nil {
		t.Fatal("the recorder reported an unwritable record as written")
	}
}

// A person reading why a run stopped on its own record is told what that was,
// and not only that a run finished.
func TestTheRecordingStopSaysItCoversWhatWasAboutToBeSent(t *testing.T) {
	t.Parallel()
	says := StopSays(StopRecording)
	if !strings.Contains(says, "what it was about to send the AI provider") || !strings.Contains(says, "did not go on") {
		t.Fatalf("the recording stop says %q", says)
	}
}
