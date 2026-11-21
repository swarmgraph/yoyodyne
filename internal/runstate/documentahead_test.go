package runstate

import (
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
)

// A document run is still publishing its copy while it goes and while its
// copy waits in a merge queue, the harness's or the forge's; it stops once the
// copy lands, is handed back, or the run is retired. A run of the same handoff
// never holds that handoff, and a run of another document holds nothing.
func TestOnlyAnotherCopyStillGoingOrQueuedHoldsADocument(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 19, 15, 53, 0, 0, time.UTC)
	design := artifact.ConfirmedDocument{Artifact: artifact.Artifact{ID: "machine-home"}}
	waiting := DocumentPublication{ConversationID: "chat-a", WriteID: "document-2.1", Candidate: design}
	other := DocumentPublication{ConversationID: "chat-b", WriteID: "document-1.1", Candidate: design}
	elsewhere := DocumentPublication{ConversationID: "chat-b", WriteID: "document-3.1", Candidate: artifact.ConfirmedDocument{Artifact: artifact.Artifact{ID: "cost-reduction"}}}
	queue := &MergeQueueAdmissionRecord{EntryID: "mqe-1", AdmittedAt: at}
	cases := []struct {
		name  string
		run   State
		holds bool
	}{
		{"going", State{RunID: "run-1", Status: StatusRunning, Document: &other, StartedAt: at}, true},
		{"in the harness's merge queue", State{RunID: "run-1", Status: StatusSucceeded, Document: &other, MergeQueue: queue}, true},
		{"in the forge's merge queue", State{RunID: "run-1", Status: StatusSucceeded, Document: &other, PullRequest: &PullRequest{Number: 7, MergeQueued: true}}, true},
		{"landed", State{RunID: "run-1", Status: StatusSucceeded, Document: &other, MergeQueue: queue, Integration: &Integration{TargetBranch: "main"}}, false},
		{"handed back by the queue", State{RunID: "run-1", Status: StatusFailed, Document: &other, MergeQueue: queue}, false},
		{"retired", State{RunID: "run-1", Status: StatusSucceeded, Document: &other, MergeQueue: queue, Retirement: &RunRetirement{}}, false},
		{"the same handoff", State{RunID: waiting.RunID(), Status: StatusRunning, Document: &waiting}, false},
		{"another document", State{RunID: "run-1", Status: StatusRunning, Document: &elsewhere}, false},
	}
	for _, c := range cases {
		ahead, found := PublishingAhead(waiting, []State{c.run})
		if found != c.holds {
			t.Errorf("%s: PublishingAhead() = %+v, %v; want %v", c.name, ahead, found, c.holds)
		}
		if found && ahead.RunID != "run-1" {
			t.Errorf("%s: ahead = %+v, want run-1 named", c.name, ahead)
		}
	}
	if _, found := PublishingCopyOf("machine-home", []State{{RunID: "run-1", Status: StatusRunning, Document: &waiting}}); !found {
		t.Error("PublishingCopyOf() passed over a running copy for a revision with no handoff yet")
	}
}
