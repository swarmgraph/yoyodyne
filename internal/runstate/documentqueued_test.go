package runstate

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Where a passed change waits to land is said naming the entry and the pull
// request, for the harness's queue and the forge's; a run that ended any other
// way waits in no queue, whatever its pull request last said.
func TestMergeQueueSaysWhereAPassedChangeWaits(t *testing.T) {
	t.Parallel()

	admitted := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	harness := State{Status: StatusSucceeded, PullRequest: &PullRequest{Number: 1899}, MergeQueue: &MergeQueueAdmissionRecord{EntryID: "mqe-" + strings.Repeat("a", 32), Order: 170, TargetBranch: "main", AdmittedAt: admitted}}
	for _, want := range []string{"waits in the merge queue for main", "entry 170", harness.MergeQueue.EntryID, "pull request #1899"} {
		if says := harness.MergeQueueSays(); !strings.Contains(says, want) {
			t.Fatalf("MergeQueueSays = %q, want %q", says, want)
		}
	}
	forge := State{Status: StatusSucceeded, PullRequest: &PullRequest{Number: 12, MergeQueued: true}}
	if says := forge.MergeQueueSays(); !strings.Contains(says, "forge queued the merge of pull request #12") {
		t.Fatalf("MergeQueueSays = %q for a merge queued with the forge", says)
	}
	for name, state := range map[string]State{
		"failed with a leftover queued merge": {Status: StatusFailed, PullRequest: &PullRequest{Number: 12, MergeQueued: true}},
		"landed":                              {Status: StatusSucceeded, Integration: &Integration{}, MergeQueue: harness.MergeQueue},
		"never queued":                        {Status: StatusSucceeded},
	} {
		if says := state.MergeQueueSays(); says != "" {
			t.Fatalf("MergeQueueSays = %q for a run %s", says, name)
		}
	}
}

// A document run is recorded against the item its revision names, or the one
// filed for it; only a document run may record a filed item.
func TestADocumentRunNamesTheItemItIsRecordedAgainst(t *testing.T) {
	t.Parallel()

	document := DocumentPublication{Owner: domain.RoleArchitect}
	document.Candidate.Artifact.Revisions = []artifact.Revision{{Reason: "yoyodyne-fp3.8: says where it waits"}}
	if got := (State{Document: &document}).DocumentItemSays(); got != "yoyodyne-fp3.8" {
		t.Fatalf("DocumentItemSays = %q, want the item the revision names", got)
	}
	unnamed := DocumentPublication{Owner: domain.RoleArchitect}
	unnamed.Candidate.Artifact.Revisions = []artifact.Revision{{Reason: "drafted by the architect"}}
	if got := (State{Document: &unnamed, DocumentItem: "yoyodyne-pub1"}).DocumentItemSays(); got != "yoyodyne-pub1" {
		t.Fatalf("DocumentItemSays = %q, want the filed item", got)
	}
	if err := (State{DocumentItem: "yoyodyne-pub1"}).Validate(); err == nil || !strings.Contains(err.Error(), "document_item") {
		t.Fatalf("Validate = %v, want a filed item refused on a run that publishes no document", err)
	}
}
