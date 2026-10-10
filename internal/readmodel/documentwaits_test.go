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

// A confirmed document waiting for a developer slot is listed where the ready
// work waiting for one is, naming the document and the role that owns it, and
// saying the harness starts it in the next slot ahead of new development work.
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
	want := "  - document document-12.1 (Stopped-run recovery), the architect's, from conversation chat-architect, waiting for a developer slot since " + localMoment(since) +
		" — the harness starts it in the next developer slot that frees, ahead of any new development run, and nothing is asked of anybody\n"
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
	if !strings.Contains(standing.DocumentsWaitingProblem, "disk unreadable") || !strings.Contains(standing.Render(), "the documents waiting for a developer slot could not be read: disk unreadable") {
		t.Fatalf("problem = %q, rendered:\n%s", standing.DocumentsWaitingProblem, standing.Render())
	}
}
