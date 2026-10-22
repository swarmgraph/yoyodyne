package chat

import (
	"context"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// waitingDocuments is the documents the needs-a-human line names, read from
// the conversation store as `yoyo status` and the dashboard read it.
func waitingDocuments(t *testing.T, store *runstate.ConversationStore) []readmodel.Attention {
	t.Helper()

	standing := readmodel.ReadStanding(context.Background(), readmodel.Sources{Conversations: store})
	var found []readmodel.Attention
	for _, entry := range standing.NeedsHuman {
		if entry.Kind == readmodel.AttentionDocumentApproval {
			found = append(found, entry)
		}
	}
	return found
}

// A document a conversation wrote is on the needs-a-human line from the moment
// it is recorded, read from the conversation's saved record by a reader that
// is not the conversation, and it leaves the line when the operator approves
// it and when he declines it.
func TestAWaitingDocumentIsListedUntilTheOperatorDecidesIt(t *testing.T) {
	t.Parallel()

	for _, decision := range []string{"approve", "decline"} {
		t.Run(decision, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			provider := &fakeBackend{results: []backendapi.RunResult{{
				SessionID: "session-1", ResolvedModel: "claude-opus-5",
				FinalText: documentReply("create", "v2-goals", "goals", "docs/product", "# Goals\\n\\nShip the thing."),
			}}}
			options, _ := documentOptions(t, provider)
			options.Store = newTestStore(t, root)
			session := openTestSession(t, options)
			reply, err := session.Send(context.Background(), "Write the goals up.")
			if err != nil || len(reply.Writes) != 1 {
				t.Fatalf("Send() = %#v, %v, want one document", reply.Writes, err)
			}
			writeID := reply.Writes[0].ID

			listed := waitingDocuments(t, newTestStore(t, root))
			if len(listed) != 1 || listed[0].DocumentApproval == nil || listed[0].DocumentApproval.WriteID != writeID {
				t.Fatalf("listed = %#v, want %s", listed, writeID)
			}
			if waiting := listed[0].DocumentApproval; waiting.Document != "v2-goals" || waiting.WrittenAt.IsZero() || !waiting.ProductIntent {
				t.Fatalf("listed document = %#v, want the goals with when they were written", waiting)
			}

			// The decision is sent as the message the command line and the
			// dashboard send, which is what saves the conversation's record.
			answer := decision + " " + writeID
			if decision == "decline" {
				answer += " not what I meant"
			}
			if _, decided, err := session.DecideWrites(answer); !decided || err != nil {
				t.Fatalf("DecideWrites(%q) = %v, %v", answer, decided, err)
			}
			if listed := waitingDocuments(t, newTestStore(t, root)); len(listed) != 0 {
				t.Fatalf("after %s, listed = %#v, want none", decision, listed)
			}
		})
	}
}
