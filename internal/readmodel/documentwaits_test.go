package readmodel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// documentWaitRuns is the run store with documents waiting for a slot.
type documentWaitRuns struct {
	fakeRuns
	waits []runstate.DocumentWait
	err   error
}

func (r documentWaitRuns) DocumentWaits() ([]runstate.DocumentWait, error) { return r.waits, r.err }

// A confirmed document waiting for a document landing to free is listed beside
// the ready work waiting for a developer slot, naming the document and the role
// that owns it, and saying the harness starts it as soon as a landing frees,
// without taking a developer slot.
func TestADocumentWaitingForASlotIsListedWithItsOwningRole(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Capacity = 1
	since := moment.Add(-40 * time.Minute)
	sources.Runs = documentWaitRuns{
		fakeRuns: fakeRuns{
			incomplete: []runstate.State{{RunID: "run-a", WorkItemID: "item-0", Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping, StartedAt: moment.Add(-time.Hour)}},
			prices:     map[string]runstate.ItemPrice{},
		},
		waits: []runstate.DocumentWait{{SchemaVersion: runstate.DocumentWaitSchemaVersion, Since: since, Document: runstate.DocumentPublication{
			ConversationID: "chat-architect", WriteID: "document-12.1", Turn: 12, Owner: domain.RoleArchitect,
			Candidate: artifact.ConfirmedDocument{Artifact: artifact.Artifact{ID: "recovery-design", Title: "Stopped-run recovery", Path: "docs/designs/recovery-design.md"}},
		}}},
	}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.DocumentsWaitingForSlot) != 1 {
		t.Fatalf("documents waiting = %+v, want the architect's design", standing.DocumentsWaitingForSlot)
	}
	wait := standing.DocumentsWaitingForSlot[0]
	if wait.Owner != domain.RoleArchitect || wait.WriteID != "document-12.1" || wait.Title != "Stopped-run recovery" || !wait.Since.Equal(since) || wait.Line != wait.Says() {
		t.Fatalf("document waiting = %+v", wait)
	}
	rendered := standing.Render()
	want := "  - document document-12.1 (Stopped-run recovery), the architect's, from conversation chat-architect, waiting for a document landing since " + localMoment(since) +
		" — the harness starts it as soon as a document landing frees, without taking a developer slot, and nothing is asked of anybody\n"
	if !strings.Contains(rendered, want) {
		t.Fatalf("rendered lacks %q:\n%s", want, rendered)
	}
}

// Waits nobody could read are said as a reading that came back partial, never
// as no document waiting.
func TestDocumentWaitsThatCannotBeReadAreSaid(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Runs = documentWaitRuns{fakeRuns: fakeRuns{prices: map[string]runstate.ItemPrice{}}, err: errors.New("disk unreadable")}
	standing := ReadStanding(context.Background(), sources)
	if !strings.Contains(standing.DocumentsWaitingProblem, "disk unreadable") || !strings.Contains(standing.Render(), "the documents waiting for a document landing could not be read: disk unreadable") {
		t.Fatalf("problem = %q, rendered:\n%s", standing.DocumentsWaitingProblem, standing.Render())
	}
}

// documentWaitAndCopyRuns is the run store with a document waiting and the runs
// of that document it lists.
type documentWaitAndCopyRuns struct {
	documentWaitRuns
	copies []runstate.State
}

func (r documentWaitAndCopyRuns) DocumentRuns(string) ([]runstate.State, error) {
	return r.copies, nil
}

// A confirmed document waiting behind an earlier copy of itself that is still
// in the merge queue says so, naming the copy, its queue entry and pull
// request, and says the harness starts it once that copy lands or is handed
// back; the dashboard prints the same line.
func TestADocumentWaitingBehindAnEarlierQueuedCopySaysWhatItWaitsFor(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	since := moment.Add(-10 * time.Minute)
	admitted := moment.Add(-20 * time.Minute)
	design := artifact.ConfirmedDocument{Artifact: artifact.Artifact{ID: "machine-home", Title: "Machine home", Path: "docs/designs/machine-home.md"}}
	waiting := runstate.DocumentPublication{ConversationID: "chat-architect", WriteID: "document-14.1", Turn: 14, Owner: domain.RoleArchitect, Candidate: design}
	earlier := runstate.DocumentPublication{ConversationID: "chat-other", WriteID: "document-9.1", Turn: 9, Owner: domain.RoleArchitect, Candidate: design}
	sources.Runs = documentWaitAndCopyRuns{
		documentWaitRuns: documentWaitRuns{
			fakeRuns: fakeRuns{prices: map[string]runstate.ItemPrice{}},
			waits:    []runstate.DocumentWait{{SchemaVersion: runstate.DocumentWaitSchemaVersion, Since: since, Document: waiting}},
		},
		copies: []runstate.State{{RunID: "run-earlier", Status: runstate.StatusSucceeded, Document: &earlier, StartedAt: admitted.Add(-time.Hour),
			MergeQueue:  &runstate.MergeQueueAdmissionRecord{EntryID: "mqe-1", AdmittedAt: admitted},
			PullRequest: &runstate.PullRequest{Number: 2162}}},
	}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.DocumentsWaitingForSlot) != 1 {
		t.Fatalf("documents waiting = %+v", standing.DocumentsWaitingForSlot)
	}
	wait := standing.DocumentsWaitingForSlot[0]
	for _, want := range []string{"document document-14.1 (Machine home), the architect's", "an earlier copy of this document, document-9.1, is waiting in the merge queue as entry mqe-1 (pull request #2162)", "from run run-earlier"} {
		if !strings.Contains(wait.Line, want) {
			t.Fatalf("line %q lacks %q", wait.Line, want)
		}
	}
	if strings.Contains(wait.Line, "waiting for a document landing") {
		t.Fatalf("line %q says it waits for a landing", wait.Line)
	}
	if rendered := standing.Render(); !strings.Contains(rendered, wait.Line+" — the harness starts it once that copy lands or is handed back") {
		t.Fatalf("rendered lacks the waiting line and what happens next:\n%s", rendered)
	}
}
