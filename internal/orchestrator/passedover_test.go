package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// waitingWork is the work waiting in a role's conversation as a test hands it
// to every turn.
type waitingWork struct {
	items []runstate.DeliveredWork
	err   error
}

func (w waitingWork) Read(context.Context, domain.AgentRole) (ConversationWorkReading, error) {
	if w.err != nil {
		return ConversationWorkReading{Section: "## Work waiting in your conversation\n\nunreadable"}, w.err
	}
	var section strings.Builder
	section.WriteString("## Work waiting in your conversation\n\n")
	for _, item := range w.items {
		fmt.Fprintf(&section, "- (%s) [P%d]\n", item.ID, item.Priority)
	}
	return ConversationWorkReading{Section: section.String(), Items: w.items}, nil
}

// itemNotes is the tracker as the note on a passed-over item reaches it.
type itemNotes struct {
	notes map[string][]string
	err   error
}

func (n *itemNotes) AppendNote(_ context.Context, id, note string) error {
	if n.err != nil {
		return n.err
	}
	if n.notes == nil {
		n.notes = map[string][]string{}
	}
	n.notes[id] = append(n.notes[id], note)
	return nil
}

func architectPass() map[string]config.RecurringTask {
	return map[string]config.RecurringTask{"architect-pass": {
		Role: domain.RoleArchitect, Every: config.Duration(45 * time.Minute), Enabled: true,
		Prompt: "Rule on the work waiting in your conversation.", MaxTurns: 1,
	}}
}

// The dashboard token ruling first, then two more P0 rulings and a P1 design.
func threeTopItems() waitingWork {
	return waitingWork{items: []runstate.DeliveredWork{
		{ID: "yoyodyne-ifd.414.1", Priority: 0},
		{ID: "yoyodyne-ab2", Priority: 0},
		{ID: "yoyodyne-ifd.428.75.1", Priority: 0},
		{ID: "yoyodyne-gtx", Priority: 1},
	}}
}

func firePassedOver(t *testing.T, work RecurringConversationWork, role *wokenRole, notes *itemNotes) runstate.Sweep {
	t.Helper()
	store := sweepStore(t)
	trigger := Trigger{Tasks: architectPass(), Claims: store, Reports: store, Roles: role,
		ConversationWork: work, ItemNotes: notes, Clock: recurringClock{}}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	recorded, _, err := store.List()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("recorded = %+v (%v), want one pass", recorded, err)
	}
	return recorded[0]
}

func TestAPassThatTakesTheFirstItemRecordsItAndNotesNothing(t *testing.T) {
	t.Parallel()
	role := &wokenRole{answers: []scriptedTurn{{result: complete("Ruled on the token delivery."), actedOn: []string{"yoyodyne-ifd.414.1"}}}}
	notes := &itemNotes{}
	recorded := firePassedOver(t, threeTopItems(), role, notes)

	if len(recorded.Delivered) != 4 {
		t.Fatalf("delivered = %+v, want all four in the order handed", recorded.Delivered)
	}
	for index, want := range []string{"yoyodyne-ifd.414.1", "yoyodyne-ab2", "yoyodyne-ifd.428.75.1", "yoyodyne-gtx"} {
		if recorded.Delivered[index].ID != want {
			t.Fatalf("delivered[%d] = %+v, want %s: the order handed is the order recorded", index, recorded.Delivered[index], want)
		}
	}
	if recorded.Delivered[3].Priority != 1 || recorded.Delivered[0].Priority != 0 {
		t.Errorf("priorities = %+v, want each item's own", recorded.Delivered)
	}
	if !recorded.Delivered[0].Taken || recorded.Delivered[1].Taken {
		t.Errorf("delivered = %+v, want only the first taken", recorded.Delivered)
	}
	if len(notes.notes) != 0 {
		t.Errorf("notes = %v, want none: the first item was taken", notes.notes)
	}
	if !strings.Contains(role.messages[0], `"left"`) {
		t.Errorf("a pass handed waiting work is told how to say what it left:\n%s", role.messages[0])
	}
}

func TestAPassThatTakesTheSecondItemNotesTheFirstWithItsReason(t *testing.T) {
	t.Parallel()
	result := complete("Took the factory health addendum.")
	result.Left = []sweep.Left{{Item: "yoyodyne-ifd.414.1", Reason: "the ruling waits on the operator's choice of secret store, which I have asked the Lead Product Manager about"}}
	role := &wokenRole{answers: []scriptedTurn{{result: result, actedOn: []string{"yoyodyne-ab2"}}}}
	notes := &itemNotes{}
	recorded := firePassedOver(t, threeTopItems(), role, notes)

	first, second := recorded.Delivered[0], recorded.Delivered[1]
	if first.Taken || !strings.Contains(first.Reason, "secret store") || !second.Taken || second.Reason != "" {
		t.Fatalf("delivered = %+v, want the first left with its reason and the second taken", recorded.Delivered)
	}
	written := notes.notes["yoyodyne-ifd.414.1"]
	if len(written) != 1 || len(notes.notes) != 1 {
		t.Fatalf("notes = %v, want one note, on the first item only", notes.notes)
	}
	note := written[0]
	at := recurringNow.Local().Format("15:04 MST on January 2, 2006")
	for _, want := range []string{"architect's recurring pass architect-pass#1", at, "(P0) was first of the 4 items it was handed", "it took 1 of the others", "Reason: the ruling waits on the operator's choice of secret store"} {
		if !strings.Contains(note, want) {
			t.Errorf("note = %q, want it to say %q", note, want)
		}
	}
}

func TestAPassThatGivesNoReasonSaysSoOnTheItem(t *testing.T) {
	t.Parallel()
	role := &wokenRole{answers: []scriptedTurn{{result: complete("Took the scheduled-pass design correction."), actedOn: []string{"yoyodyne-ifd.428.75.1"}}}}
	notes := &itemNotes{}
	recorded := firePassedOver(t, threeTopItems(), role, notes)

	if recorded.Delivered[0].Taken || recorded.Delivered[0].Reason != "" || !recorded.Delivered[2].Taken {
		t.Fatalf("delivered = %+v, want the first left with no reason and the third taken", recorded.Delivered)
	}
	written := notes.notes["yoyodyne-ifd.414.1"]
	if len(written) != 1 || !strings.HasSuffix(written[0], "Reason: no reason given.") {
		t.Fatalf("notes = %v, want the first item noted with no reason given", notes.notes)
	}
}

// Several top-priority items handed, none taken: the note reaches the item
// first in the order and only that one.
func TestAPassThatTakesNoneOfSeveralTopItemsNotesTheFirst(t *testing.T) {
	t.Parallel()
	role := &wokenRole{answers: []scriptedTurn{{result: complete("Read the queue and argued proposals instead.")}}}
	notes := &itemNotes{}
	recorded := firePassedOver(t, threeTopItems(), role, notes)

	for _, item := range recorded.Delivered {
		if item.Taken {
			t.Fatalf("delivered = %+v, want none taken", recorded.Delivered)
		}
	}
	if len(notes.notes) != 1 || len(notes.notes["yoyodyne-ifd.414.1"]) != 1 {
		t.Fatalf("notes = %v, want one note on the first item only", notes.notes)
	}
	if note := notes.notes["yoyodyne-ifd.414.1"][0]; !strings.Contains(note, "it took none of the others") {
		t.Errorf("note = %q, want it to say the pass took none of the others", note)
	}
}

// The account's findings are read too: a finding the role acted on names an
// item it took, and a finding it left names one it did not, with that finding
// as its reason. A "left" entry stands over a finding, and a tracker action
// stands over both.
func TestTheAccountsFindingsSayWhatWasTakenAndWhy(t *testing.T) {
	t.Parallel()
	result := complete("Looked at both.",
		sweep.Finding{Issue: "yoyodyne-ifd.414.1 needs the operator's secret store first", Disposition: sweep.DispositionLeft},
		sweep.Finding{Issue: "the factory health addendum", Disposition: sweep.DispositionFiled, Filed: []string{"yoyodyne-ab2"}},
	)
	result.Left = []sweep.Left{{Item: "yoyodyne-gtx", Reason: "behind the token ruling"}}
	role := &wokenRole{answers: []scriptedTurn{{result: result, actedOn: []string{"yoyodyne-gtx"}}}}
	notes := &itemNotes{}
	recorded := firePassedOver(t, threeTopItems(), role, notes)

	want := []runstate.DeliveredWork{
		{ID: "yoyodyne-ifd.414.1", Priority: 0, Reason: "yoyodyne-ifd.414.1 needs the operator's secret store first"},
		{ID: "yoyodyne-ab2", Priority: 0, Taken: true},
		{ID: "yoyodyne-ifd.428.75.1", Priority: 0},
		{ID: "yoyodyne-gtx", Priority: 1, Taken: true},
	}
	for index := range want {
		if recorded.Delivered[index] != want[index] {
			t.Errorf("delivered[%d] = %+v, want %+v", index, recorded.Delivered[index], want[index])
		}
	}
	if note := notes.notes["yoyodyne-ifd.414.1"]; len(note) != 1 || !strings.Contains(note[0], "needs the operator's secret store first") {
		t.Errorf("notes = %v, want the finding's words as the reason", notes.notes)
	}
}

func TestAnItemIsNamedAsAWholeIdentifier(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		text string
		want bool
	}{
		{"yoyodyne-ifd.414.1 waits", true},
		{"ruled on yoyodyne-ifd.414.1.", true},
		{"(yoyodyne-ifd.414.1)", true},
		{"yoyodyne-ifd.414.12 waits", false},
		{"yoyodyne-ifd.414.1.3 waits", false},
		{"xyoyodyne-ifd.414.1", false},
	} {
		if got := namesItem(test.text, "yoyodyne-ifd.414.1"); got != test.want {
			t.Errorf("namesItem(%q) = %v, want %v", test.text, got, test.want)
		}
	}
}

func TestAPassThatDidNotRunOrWasHandedNothingNotesNothing(t *testing.T) {
	t.Parallel()
	t.Run("did not run", func(t *testing.T) {
		role := &wokenRole{failure: fmt.Errorf("%w: the provider is not signed in", ErrRoleUnreachable)}
		notes := &itemNotes{}
		recorded := firePassedOver(t, threeTopItems(), role, notes)
		if len(notes.notes) != 0 {
			t.Errorf("notes = %v, want none: the pass asked the role nothing", notes.notes)
		}
		if recorded.Turns != 0 {
			t.Errorf("turns = %d, want none", recorded.Turns)
		}
	})
	t.Run("gave no account", func(t *testing.T) {
		role := &wokenRole{answers: []scriptedTurn{{problem: "the architect answered in prose without a sweep block"}}}
		notes := &itemNotes{}
		firePassedOver(t, threeTopItems(), role, notes)
		if len(notes.notes) != 0 {
			t.Errorf("notes = %v, want none: a pass with no account is a failed pass, not a passing-over", notes.notes)
		}
	})
	t.Run("handed nothing", func(t *testing.T) {
		role := &wokenRole{answers: []scriptedTurn{{result: complete("Nothing was waiting.")}}}
		notes := &itemNotes{}
		recorded := firePassedOver(t, waitingWork{}, role, notes)
		if len(notes.notes) != 0 || len(recorded.Delivered) != 0 {
			t.Errorf("notes = %v, delivered = %+v, want neither", notes.notes, recorded.Delivered)
		}
		if strings.Contains(role.messages[0], `"left"`) {
			t.Errorf("a pass handed nothing is not told about left items:\n%s", role.messages[0])
		}
	})
	t.Run("queue unreadable", func(t *testing.T) {
		role := &wokenRole{answers: []scriptedTurn{{result: complete("Could not read the queue.")}}}
		notes := &itemNotes{}
		recorded := firePassedOver(t, waitingWork{err: errors.New("tracker listing failed")}, role, notes)
		if len(notes.notes) != 0 || len(recorded.Delivered) != 0 {
			t.Errorf("notes = %v, delivered = %+v, want neither", notes.notes, recorded.Delivered)
		}
	})
}

// A note that could not be written costs the note and not the pass's record,
// which says so.
func TestANoteThatCouldNotBeWrittenIsOnThePassRecord(t *testing.T) {
	t.Parallel()
	role := &wokenRole{answers: []scriptedTurn{{result: complete("Took nothing.")}}}
	notes := &itemNotes{err: errors.New("bd update refused")}
	recorded := firePassedOver(t, threeTopItems(), role, notes)
	if !strings.Contains(recorded.Problem, "yoyodyne-ifd.414.1 was first in the order") || !strings.Contains(recorded.Problem, "bd update refused") {
		t.Errorf("problem = %q, want the unwritten note named", recorded.Problem)
	}
	if recorded.Result == nil || len(recorded.Delivered) != 4 {
		t.Errorf("recorded = %+v, want the pass's account and delivery kept", recorded)
	}
}
