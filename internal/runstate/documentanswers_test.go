package runstate

import (
	"slices"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
)

func TestADocumentAnswersWhatItsOwnerListedAndWhatItsRevisionNames(t *testing.T) {
	t.Parallel()
	document := DocumentPublication{Answers: []string{"yoyodyne-a.1", "yoyodyne-b"}}
	document.Candidate.Artifact.Revisions = []artifact.Revision{{Reason: "yoyodyne-c: ruled"}}
	if got := document.AnsweredItems(); !slices.Equal(got, []string{"yoyodyne-a.1", "yoyodyne-b", "yoyodyne-c"}) {
		t.Fatalf("AnsweredItems() = %v", got)
	}
	document.Candidate.Artifact.Revisions[0].Reason = "yoyodyne-b: ruled"
	if got := document.AnsweredItems(); !slices.Equal(got, []string{"yoyodyne-a.1", "yoyodyne-b"}) {
		t.Fatalf("AnsweredItems() = %v, want each once", got)
	}
}

func TestTheSameHandoffMayDifferOnlyInWhatItAnswers(t *testing.T) {
	t.Parallel()
	document := DocumentPublication{ConversationID: "chat-a", WriteID: "document-1.1"}
	other := document
	other.Answers = []string{"yoyodyne-a.1"}
	if !document.SameHandoff(other) {
		t.Error("a handoff that also says what it answers reads as a different one")
	}
	other.WriteID += "-other"
	if document.SameHandoff(other) {
		t.Error("another write reads as the same handoff")
	}
}
