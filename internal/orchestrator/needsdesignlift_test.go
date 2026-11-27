package orchestrator

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// designHoldTracker holds a few work items carrying labels, answers every other
// read from the tracker it wraps, and takes the needs-design label off as the
// tracker's own lift does.
type designHoldTracker struct {
	WorkTracker
	mu    sync.Mutex
	items map[string]beads.WorkItem
	lifts []string
}

func newDesignHoldTracker(inner WorkTracker, items ...beads.WorkItem) *designHoldTracker {
	held := &designHoldTracker{WorkTracker: inner, items: map[string]beads.WorkItem{}}
	for _, item := range items {
		held.items[item.ID] = item
	}
	return held
}

func (t *designHoldTracker) Show(ctx context.Context, id string) (beads.WorkItem, error) {
	t.mu.Lock()
	item, held := t.items[id]
	t.mu.Unlock()
	if held {
		return item, nil
	}
	if t.WorkTracker == nil {
		return beads.WorkItem{}, fmt.Errorf("no work item %s", id)
	}
	return t.WorkTracker.Show(ctx, id)
}

func (t *designHoldTracker) RecordOutcome(ctx context.Context, id, notes string) (beads.WorkItem, error) {
	t.mu.Lock()
	item, held := t.items[id]
	if held {
		item.Notes += "\n" + notes
		t.items[id] = item
	}
	t.mu.Unlock()
	if held || t.WorkTracker == nil {
		return item, nil
	}
	return t.WorkTracker.RecordOutcome(ctx, id, notes)
}

func (t *designHoldTracker) LiftNeedsDesign(_ context.Context, id, note string) (beads.WorkItem, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	item, held := t.items[id]
	if !held {
		return beads.WorkItem{}, fmt.Errorf("no work item %s", id)
	}
	item.Labels = slices.DeleteFunc(slices.Clone(item.Labels), func(label string) bool { return label == domain.NeedsDesignLabel })
	item.Notes += "\n" + note
	t.items[id] = item
	t.lifts = append(t.lifts, id)
	return item, nil
}

func (t *designHoldTracker) item(id string) beads.WorkItem {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.items[id]
}

func heldForDesign(id string) beads.WorkItem {
	return beads.WorkItem{ID: id, Title: "Held " + id, Status: "open", Labels: []string{"cost", domain.NeedsDesignLabel}}
}

// An architect's design whose revision reason opens with an item's identifier
// takes that item's needs-design label off once it lands on main, and notes on
// the item which document, which revision, and which commit; an item the
// design does not name keeps its label and gets no note.
func TestALandedArchitectsDesignLiftsTheNeedsDesignLabelOfTheItemItNames(t *testing.T) {
	t.Parallel()

	_, _, provider, pipeline, runs := automaticFixture(t)
	holds := newDesignHoldTracker(pipeline.Tracker, heldForDesign("yoyodyne-held"), heldForDesign("yoyodyne-other"))
	pipeline.Tracker = holds
	original := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleArchitect {
			write := fmt.Sprintf("Written.\n%s\n{\"documents\":[{\"action\":\"create\",\"id\":\"held-design\",\"kind\":\"design\",\"title\":\"Held design\",\"directory\":\"docs/designs\",\"body\":%q,\"reason\":\"yoyodyne-held: answers how the held item behaves\"}]}\n```", artifact.WriteFence, "# Held\n\nThe answer.")
			return backend.RunResult{Backend: domain.BackendClaudeCode, SessionID: "author-session", FinalText: write}, nil
		}
		return original(request)
	}
	options, _ := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Write the design."); err != nil {
		t.Fatal(err)
	}
	recorded, err := runs.Recorded()
	if err != nil || len(recorded) != 1 || recorded[0].Status != runstate.StatusSucceeded || recorded[0].Integration == nil {
		t.Fatalf("runs = %+v, %v; want the design landed", recorded, err)
	}
	landed := recorded[0].Integration.TargetCommit

	held := holds.item("yoyodyne-held")
	if domain.NeedsDesign(held.Labels) || !slices.Contains(held.Labels, "cost") {
		t.Fatalf("labels = %v, want needs-design off and every other label kept", held.Labels)
	}
	for _, want := range []string{"took the needs-design label off", "held-design", "revision 0", landed} {
		if !strings.Contains(held.Notes, want) {
			t.Errorf("notes = %q, want them to name %q", held.Notes, want)
		}
	}
	other := holds.item("yoyodyne-other")
	if !domain.NeedsDesign(other.Labels) || strings.Contains(other.Notes, "label off") {
		t.Fatalf("an item the design does not name = %+v, want its label and notes untouched", other)
	}
	if !slices.Equal(holds.lifts, []string{"yoyodyne-held"}) {
		t.Fatalf("lifts = %v, want only the item the design names", holds.lifts)
	}
}

// designLanding is a document run recorded under its own bookkeeping item, as
// the merge queue completes one.
func designLanding(owner domain.AgentRole, reason string, answers ...string) runstate.State {
	runID := "run-" + strings.Repeat("ab", 16)
	return runstate.State{
		RunID:      runID,
		WorkItemID: runID,
		Status:     runstate.StatusSucceeded,
		Document: &runstate.DocumentPublication{
			Candidate: artifact.ConfirmedDocument{Artifact: artifact.Artifact{
				ID:    "queued-design",
				Title: "Queued design",
				Path:  "docs/designs/queued-design.md",
				Revisions: []artifact.Revision{
					{Action: artifact.ActionCreated, By: owner, Reason: "drafted"},
					{Action: artifact.ActionAmended, By: owner, Reason: reason},
				},
			}},
			WriteID: "document-9.1",
			Owner:   owner,
			Answers: answers,
		},
	}
}

type oneRun struct{ state runstate.State }

func (r oneRun) Load(string) (runstate.State, error) { return r.state, nil }

// Through the merge queue, the landing lifts the label of every open item the
// document answers, by its answers list or its reason, and only where the item
// carries it; a document another role owns lifts nothing.
func TestADesignLandingThroughTheQueueLiftsOnlyTheItemsItAnswers(t *testing.T) {
	t.Parallel()

	answered := beads.WorkItem{ID: "yoyodyne-unheld", Status: "open"}
	closed := heldForDesign("yoyodyne-closed")
	closed.Status = "closed"
	holds := newDesignHoldTracker(nil, heldForDesign("yoyodyne-held"), heldForDesign("yoyodyne-listed"), heldForDesign("yoyodyne-other"), answered, closed)
	state := designLanding(domain.RoleArchitect, "yoyodyne-held: the ruling", "yoyodyne-listed", "yoyodyne-unheld", "yoyodyne-closed")
	items := HarnessRunItems{WorkTracker: holds, Runs: oneRun{state}}
	reason := "Reviewed by Yoyodyne run " + state.RunID + " and landed on main through the merge queue as " + strings.Repeat("c", 40) + "."
	if _, err := items.Complete(context.Background(), state.WorkItemID, reason); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if !slices.Equal(holds.lifts, []string{"yoyodyne-listed", "yoyodyne-held"}) && !slices.Equal(holds.lifts, []string{"yoyodyne-held", "yoyodyne-listed"}) {
		t.Fatalf("lifts = %v, want the two labelled open items the document answers", holds.lifts)
	}
	for _, id := range []string{"yoyodyne-held", "yoyodyne-listed"} {
		item := holds.item(id)
		if domain.NeedsDesign(item.Labels) || !strings.Contains(item.Notes, "queued-design") || !strings.Contains(item.Notes, "revision 1") || !strings.Contains(item.Notes, strings.Repeat("c", 40)) {
			t.Errorf("%s = %+v, want the label off and a note naming the document, its revision and the commit", id, item)
		}
	}
	if !domain.NeedsDesign(holds.item("yoyodyne-other").Labels) {
		t.Error("an item the document does not answer lost its label")
	}
	if strings.Contains(holds.item("yoyodyne-unheld").Notes, "label off") || !domain.NeedsDesign(holds.item("yoyodyne-closed").Labels) {
		t.Error("an item not carrying the label, or a closed one, was written to")
	}

	productHolds := newDesignHoldTracker(nil, heldForDesign("yoyodyne-held"))
	product := designLanding(domain.RoleProductManager, "yoyodyne-held: goals")
	if _, err := (HarnessRunItems{WorkTracker: productHolds, Runs: oneRun{product}}).Complete(context.Background(), product.WorkItemID, reason); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(productHolds.lifts) != 0 {
		t.Fatalf("lifts = %v, want none for a document the architect does not own", productHolds.lifts)
	}
}
