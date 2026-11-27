package orchestrator

// Taking the needs-design label off once the design has landed.
//
// docs/designs/needs-design-gate.md says how the label is lifted: no role
// removes it by hand, and the harness removes it when an architect's document
// that answers the item lands on the target branch. An item answers to a
// document when the document's write names it among the items it answers, or
// when the reason of the revision that landed opens with its identifier
// (runstate.DocumentPublication.AnsweredItems reads both). The label comes off
// when the document lands, never when it is written, so a document handed back
// or still waiting leaves it on. A ruling only in the item's notes never
// reaches this code, so it leaves the label on too.
//
// Taking the label off is all it takes for the item to be pulled again: the
// backlog reads the label from the tracker at every pull, and the architect's
// wake drops an item that no longer carries it.

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// NeedsDesignLifter takes the needs-design label off a work item and appends
// note to its notes, in one write. It is satisfied by beads.Client, and by
// HarnessRunItems over a tracker that satisfies it. A tracker that does not
// satisfy it lifts nothing.
type NeedsDesignLifter interface {
	LiftNeedsDesign(ctx context.Context, id, note string) (beads.WorkItem, error)
}

// LiftNeedsDesign passes the lift to the tracker it wraps, where that tracker
// can make one. The bookkeeping items of the harness's own runs never carry
// the label, so they are never asked.
func (t HarnessRunItems) LiftNeedsDesign(ctx context.Context, id, note string) (beads.WorkItem, error) {
	lifter, lifts := t.WorkTracker.(NeedsDesignLifter)
	if !lifts {
		return beads.WorkItem{}, fmt.Errorf("the tracker cannot take the %s label off %s", domain.NeedsDesignLabel, id)
	}
	return lifter.LiftNeedsDesign(ctx, id, note)
}

// liftDesignHolds takes the needs-design label off every open work item the
// landed document answers and still carries it, noting on each which document,
// which revision of it, and where it landed. landed says where, naming the
// commit. Only an architect's document lifts the label, because the label
// holds an item for the architect's design.
//
// Like every write the recorder makes, it is bounded and never stops the run:
// an item the tracker would not change keeps its label, and the development
// manager's pass and the architect's daily pass still list it.
func (r documentItemRecorder) liftDesignHolds(ctx context.Context, landed string) {
	if r.tracker == nil || r.document.Owner != domain.RoleArchitect {
		return
	}
	lifter, lifts := r.tracker.(NeedsDesignLifter)
	if !lifts {
		return
	}
	note := designLandedNote(r.document, landed)
	for _, id := range r.document.AnsweredItems() {
		if !beads.ValidIssueID(id) {
			continue
		}
		liftDesignHold(ctx, r.tracker, lifter, id, note)
	}
}

// liftDesignHold lifts one item's label where it is open and carries it, so an
// item that never waited on this design is left without a note.
func liftDesignHold(ctx context.Context, tracker WorkTracker, lifter NeedsDesignLifter, id, note string) {
	bounded, cancel := trackerRecordContext(ctx)
	defer cancel()
	item, err := tracker.Show(bounded, id)
	if err != nil || item.Status == "closed" || !domain.NeedsDesign(item.Labels) {
		return
	}
	_, _ = lifter.LiftNeedsDesign(bounded, id, note)
}

// designLandedNote is what an item whose label came off is told: the document,
// its revision, and where it landed.
func designLandedNote(document runstate.DocumentPublication, landed string) string {
	artifact := document.Candidate.Artifact
	// Counted from zero, as the document's approvals count its revisions.
	revision := fmt.Sprintf("revision %d", max(len(artifact.Revisions)-1, 0))
	return fmt.Sprintf("The harness took the %s label off: the architect's document %s (%s) at %s, %s, which answers this item, has landed: %s. Unless something else holds it, it can be started at the next pull.",
		domain.NeedsDesignLabel, artifact.ID, strings.Join(strings.Fields(artifact.Title), " "), artifact.Path, revision, strings.TrimSuffix(strings.TrimSpace(landed), "."))
}
