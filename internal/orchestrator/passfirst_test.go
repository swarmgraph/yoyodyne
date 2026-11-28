package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

const namedLine = "named first for the architect's next pass by the Lead Product Manager at 11:12 PST on November 27: lift the design hold by Saturday"

// namedWork is the waiting work with the Lead Product Manager's named item
// leading it, as the command line's reader hands it.
type namedWork struct{ waitingWork }

func (w namedWork) Read(ctx context.Context, role domain.AgentRole, standing StandingRulings) (ConversationWorkReading, error) {
	reading, err := w.waitingWork.Read(ctx, role, standing)
	reading.Named = "## Take these first\n\n- yoyodyne-9k1y.22: " + namedLine + "\n"
	return reading, err
}

func namedFirstItems() namedWork {
	return namedWork{waitingWork{items: []runstate.DeliveredWork{
		{ID: "yoyodyne-9k1y.22", Priority: 0, NamedFirst: namedLine},
		{ID: "yoyodyne-ab2", Priority: 0},
	}}}
}

// firstNotes is the tracker as the passed-over note and the ending of a
// request reach it.
type firstNotes struct {
	itemNotes
	ended map[string]string
}

func (n *firstNotes) EndFirstInPass(_ context.Context, id string, role domain.AgentRole, note string) error {
	if n.ended == nil {
		n.ended = map[string]string{}
	}
	n.ended[id] = string(role) + ": " + note
	return nil
}

func fireNamed(t *testing.T, role *wokenRole, notes *firstNotes) runstate.Sweep {
	t.Helper()
	store := sweepStore(t)
	trigger := Trigger{Tasks: architectPass(), Claims: store, Reports: store, Roles: role,
		ConversationWork: namedFirstItems(), ItemNotes: notes, Clock: recurringClock{}}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	recorded, _, err := store.List()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("recorded = %+v (%v), want one pass", recorded, err)
	}
	return recorded[0]
}

// The item named for the pass is said above everything else its turn carries,
// is first in what the pass is recorded as handed, and once the pass takes it
// the request ends with a note naming the pass.
func TestAnItemNamedForThePassIsTakenFirstAndTheRequestEnds(t *testing.T) {
	t.Parallel()
	role := &wokenRole{answers: []scriptedTurn{{result: complete("Lifted the design hold."), actedOn: []string{"yoyodyne-9k1y.22"}}}}
	notes := &firstNotes{}
	recorded := fireNamed(t, role, notes)

	if !strings.HasPrefix(role.messages[0], "## Take these first") {
		t.Fatalf("the turn does not open with the named item:\n%s", role.messages[0])
	}
	if named, queue := strings.Index(role.messages[0], "## Take these first"), strings.Index(role.messages[0], "## Work waiting in your conversation"); named > queue {
		t.Fatalf("the named item is said after the queue:\n%s", role.messages[0])
	}
	first := recorded.Delivered[0]
	if first.ID != "yoyodyne-9k1y.22" || !first.Taken || first.NamedFirst != namedLine {
		t.Fatalf("delivered = %+v, want the named item first, taken, and saying who named it", recorded.Delivered)
	}
	ended := notes.ended["yoyodyne-9k1y.22"]
	if len(notes.ended) != 1 || !strings.HasPrefix(ended, "architect: Taken first by the architect's pass architect-pass#1") || !strings.Contains(ended, namedLine) {
		t.Fatalf("ended = %v, want the named item's request ended by this pass", notes.ended)
	}
	if len(notes.notes) != 0 {
		t.Fatalf("notes = %v, want no passed-over note: the named item was taken", notes.notes)
	}
}

// A pass that leaves the named item ends nothing, so the next pass is handed it
// first again; its record keeps the item as not taken with the role's reason,
// and the item's note says it was passed over.
func TestAnItemNamedForThePassThatIsLeftStaysNamedWithTheReason(t *testing.T) {
	t.Parallel()
	result := complete("Took the other item.")
	result.Left = []sweep.Left{{Item: "yoyodyne-9k1y.22", Reason: "the design it waits on has not landed on main"}}
	role := &wokenRole{answers: []scriptedTurn{{result: result, actedOn: []string{"yoyodyne-ab2"}}}}
	notes := &firstNotes{}
	recorded := fireNamed(t, role, notes)

	first := recorded.Delivered[0]
	if first.Taken || first.NamedFirst != namedLine || !strings.Contains(first.Reason, "has not landed on main") {
		t.Fatalf("delivered = %+v, want the named item left with its reason", recorded.Delivered)
	}
	if len(notes.ended) != 0 {
		t.Fatalf("ended = %v, want the request kept for the next pass", notes.ended)
	}
	if written := notes.notes["yoyodyne-9k1y.22"]; len(written) != 1 || !strings.Contains(written[0], "has not landed on main") {
		t.Fatalf("notes = %v, want the passed-over note on the named item", notes.notes)
	}
}
