package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type fakeReturnedDocuments struct {
	conversation, write string
	decisions           []runstate.DocumentReturnsDecision
}

func (f *fakeReturnedDocuments) DecideReturnedDocument(_ context.Context, conversationID, writeID string, decision runstate.DocumentReturnsDecision) (runstate.PendingWrite, error) {
	f.conversation, f.write = conversationID, writeID
	f.decisions = append(f.decisions, decision)
	return runstate.PendingWrite{ID: writeID, Artifact: "artifact-contract", ReturnsHold: &runstate.DocumentReturnsHold{Decision: &decision}}, nil
}

// The development manager releases or declines a held document from her own
// conversation, with her reason and her turn recorded.
func TestTheDevelopmentManagerDecidesAHeldDocument(t *testing.T) {
	t.Parallel()

	for decision, want := range map[string]string{"release": "released document document-12.1", "decline": "declined document document-12.1"} {
		returned := &fakeReturnedDocuments{}
		options := triageOptions(t, &fakeTracker{items: map[string]beads.WorkItem{}}, nil,
			trackerReply("Deciding it.", `{"action":"held_document","document":"chat-abc/document-12.1","decision":"`+decision+`","reason":"it answers the missing decision"}`))
		options.Reports = &fakeReports{}
		options.ReturnedDocuments = returned
		reply := triageSend(t, options)
		if len(reply.Actions) != 1 || !reply.Actions[0].Applied || !strings.Contains(reply.Actions[0].Summary, want) {
			t.Fatalf("%s: actions = %#v", decision, reply.Actions)
		}
		if returned.conversation != "chat-abc" || returned.write != "document-12.1" || len(returned.decisions) != 1 {
			t.Fatalf("%s: decided %#v", decision, returned)
		}
		if recorded := returned.decisions[0]; recorded.Decision != decision || recorded.Reason != "it answers the missing decision" || !strings.HasPrefix(recorded.DecidedBy, "the development manager in conversation ") {
			t.Fatalf("%s: recorded %#v", decision, recorded)
		}
	}
}

func TestAHeldDocumentDecisionNamesItsDocumentDecisionAndReason(t *testing.T) {
	t.Parallel()

	for _, invalid := range []string{
		`{"action":"held_document","decision":"release","reason":"no document"}`,
		`{"action":"held_document","document":"document-12.1","decision":"release","reason":"no conversation"}`,
		`{"action":"held_document","document":"chat-abc/document-12.1","decision":"retry","reason":"not a decision"}`,
		`{"action":"held_document","document":"chat-abc/document-12.1","decision":"release"}`,
		`{"action":"held_document","id":"yoyodyne-1","document":"chat-abc/document-12.1","decision":"release","reason":"an item"}`,
	} {
		if _, _, err := decodeTrackerActions(`{"actions":[` + invalid + `]}`); err == nil {
			t.Errorf("%s validated, want it refused", invalid)
		}
	}
}
