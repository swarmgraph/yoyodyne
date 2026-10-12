package chat

import (
	"context"
	"slices"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
)

// TestTypedMessagesSurviveLeavingAndResumingTheConversation checks the half of
// recall the console cannot keep by itself: what the operator typed at the
// prompt is read back out of the conversation's record by the next process to
// open it, which hands it to the console for up to recall. What reached the
// role some other way — a recurring pass, an escalation, a single message from
// the command line — was not typed at the prompt and is not recalled there.
func TestTypedMessagesSurviveLeavingAndResumingTheConversation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "First answer."},
		{SessionID: "session-1", FinalText: "Second answer."},
		{SessionID: "session-1", FinalText: "Third answer."},
	}})
	options.Store = newTestStore(t, root)
	session := openTestSession(t, options)
	input := strings.NewReader("What is the brief?\n/help\nAnd the goals?\n/exit\n")
	var out strings.Builder
	if err := session.Converse(context.Background(), testConsole(input, &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	if _, err := session.Send(context.Background(), "Sweep the backlog."); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	resumedOptions := testOptions(t, &fakeBackend{})
	resumedOptions.Store = newTestStore(t, root)
	resumed := openTestSession(t, resumedOptions)
	if !resumed.Resumed() {
		t.Fatal("the recorded conversation was not resumed")
	}
	want := []string{"What is the brief?", "And the goals?"}
	if got := resumed.typedMessages(); !slices.Equal(got, want) {
		t.Fatalf("typed messages = %q, want %q", got, want)
	}
}
