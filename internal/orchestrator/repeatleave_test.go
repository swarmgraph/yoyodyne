package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

const capacityDesign = "yoyodyne-ifd.429.43"

const spreadReason = "open points on it are spread over several earlier replies"

// capacityQueue is the heavy-check capacity design first in the architect's
// order with one item behind it, as each pass is handed them.
func capacityQueue(heldOutside string) waitingWork {
	return waitingWork{items: []runstate.DeliveredWork{
		{ID: capacityDesign, Priority: 0, HeldOutside: heldOutside},
		{ID: "yoyodyne-gtx", Priority: 1},
	}}
}

// leftAgainTurn is a turn that leaves the design for the reason the
// architect's passes kept giving.
func leftAgainTurn(summary string) scriptedTurn {
	result := complete(summary)
	result.Left = []sweep.Left{{Item: capacityDesign, Reason: spreadReason}}
	return scriptedTurn{result: result}
}

// fireArchitectPasses fires the architect's pass once an hour, n times, over
// one log, and returns what the log recorded.
func fireArchitectPasses(t *testing.T, work RecurringConversationWork, role *wokenRole, notes *itemNotes, store *runstate.SweepStore, n int) []runstate.Sweep {
	t.Helper()
	clock := &movingRecurringClock{now: recurringNow}
	trigger := Trigger{Tasks: architectPass(), Claims: store, Reports: store, Roles: role,
		ConversationWork: work, ItemNotes: notes, Clock: clock}
	for range n {
		if _, err := trigger.Fire(context.Background()); err != nil {
			t.Fatalf("Fire() error = %v", err)
		}
		clock.now = clock.now.Add(time.Hour)
	}
	recorded, _, err := store.List()
	if err != nil || len(recorded) != n {
		t.Fatalf("recorded = %+v (%v), want %d passes", recorded, err, n)
	}
	return recorded
}

func TestAnItemLeftTwiceIsDecidedWithItsAssumptionsOnTheNextPass(t *testing.T) {
	t.Parallel()
	decided := complete("Settled both open points on the capacity design.")
	decided.Settled = []sweep.Settled{{Item: capacityDesign,
		Decision:    "two heavy checks run at once, and a third waits",
		Assumptions: []string{"the machine has eight cores to give checks", "no project asks for more than two at once"}}}
	role := &wokenRole{answers: []scriptedTurn{
		leftAgainTurn("Took nothing."), leftAgainTurn("Took nothing."),
		{result: decided, actedOn: []string{capacityDesign}},
	}}
	notes := &itemNotes{}
	recorded := fireArchitectPasses(t, capacityQueue(""), role, notes, sweepStore(t), 3)

	if got := []int{recorded[0].Delivered[0].LeftInARow, recorded[1].Delivered[0].LeftInARow}; got[0] != 1 || got[1] != 2 {
		t.Fatalf("left in a row = %v, want 1 then 2: each pass counts the passes in a row that left the item", got)
	}
	for _, earlier := range role.messages[:2] {
		if strings.Contains(earlier, "This pass does not leave it again") {
			t.Fatalf("a pass before the second leave was told to settle the item:\n%s", earlier)
		}
	}
	told := role.messages[2]
	for _, want := range []string{"Your passes have left " + capacityDesign + ", first in your order, 2 times in a row", "- " + spreadReason, `"settled"`, "decision", "assumptions", "one question"} {
		if !strings.Contains(told, want) {
			t.Errorf("third pass's message does not say %q:\n%s", want, told)
		}
	}
	if len(role.messages) != 3 {
		t.Errorf("turns = %d, want 3: a pass that decided is not sent back", len(role.messages))
	}
	first := recorded[2].Delivered[0]
	if !first.Taken || first.Settled != runstate.SettledDecided || first.SentBack || first.LeftInARow != 0 {
		t.Errorf("first = %+v, want it taken and recorded as decided", first)
	}
	written := notes.notes[capacityDesign]
	if len(written) != 3 {
		t.Fatalf("notes = %v, want two passed-over notes and one saying what was decided", written)
	}
	if !strings.Contains(written[1], "2nd pass in a row") || !strings.Contains(written[1], "the next pass is told to decide it") {
		t.Errorf("second note = %q, want it to say the next pass must decide or ask", written[1])
	}
	for _, want := range []string{"Decided by the architect's recurring pass", "two heavy checks run at once", "Assumptions it made: the machine has eight cores to give checks; no project asks for more than two at once."} {
		if !strings.Contains(written[2], want) {
			t.Errorf("decision note = %q, want %q", written[2], want)
		}
	}
}

func TestAnItemLeftTwiceIsAskedAboutAsOneQuestionAfterTheTurnIsSentBack(t *testing.T) {
	t.Parallel()
	// The third pass does other work and leaves the design again; sent back, it
	// puts its one question.
	other := complete("Took the scheduled-pass correction.")
	other.Left = []sweep.Left{{Item: capacityDesign, Reason: spreadReason}}
	asked := complete("Asked about the capacity design.")
	asked.Settled = []sweep.Settled{{Item: capacityDesign, Question: "May a project ask for more than two heavy checks at once?", AskedOf: "the Lead Product Manager"}}
	role := &wokenRole{answers: []scriptedTurn{
		leftAgainTurn("Took nothing."), leftAgainTurn("Took nothing."),
		{result: other, actedOn: []string{"yoyodyne-gtx"}, admitted: []string{"yoyodyne-new"}},
		{result: asked},
		leftAgainTurn("Waiting on the answer."), leftAgainTurn("Still waiting on the answer."),
		{result: settledBy(sweep.Settled{Item: capacityDesign, Decision: "two heavy checks at once", Assumptions: []string{"the answer will not change the limit"}})},
	}}
	notes := &itemNotes{}
	recorded := fireArchitectPasses(t, capacityQueue(""), role, notes, sweepStore(t), 6)

	if len(role.messages) != 7 {
		t.Fatalf("turns = %d, want 7: the third pass is sent back once past its one-turn bound", len(role.messages))
	}
	if back := role.messages[3]; !strings.Contains(back, "This pass is ending having left "+capacityDesign+" again") || !strings.Contains(back, "Everything else this pass did stands") {
		t.Errorf("sent-back message = %q", back)
	}
	third := recorded[2]
	if third.Failed || third.Result == nil {
		t.Fatalf("third pass = %+v, want it recorded with its account, not failed", third)
	}
	if first := third.Delivered[0]; !first.Taken || first.Settled != runstate.SettledAsked || !first.SentBack {
		t.Errorf("first = %+v, want it asked about after being sent back", first)
	}
	if !third.Delivered[1].Taken || len(third.Admitted) != 1 || third.Result.Summary != "Asked about the capacity design." {
		t.Errorf("third pass = %+v, want the work its first turn did kept", third)
	}
	if !strings.Contains(third.Problem, "sent the pass back once") {
		t.Errorf("problem = %q, want the send-back said", third.Problem)
	}
	written := notes.notes[capacityDesign]
	if len(written) < 3 || !strings.Contains(written[2], "Asked about by the architect's recurring pass") || !strings.Contains(written[2], "put one question to the Lead Product Manager: May a project ask") {
		t.Fatalf("notes = %v, want the question written on the item", written)
	}
	// A question excuses the item for two passes at most: the leaves after it
	// are counted again from one, and the pass after two of them is told again.
	if strings.Contains(role.messages[4], "This pass does not leave it again") || strings.Contains(role.messages[5], "This pass does not leave it again") {
		t.Errorf("a pass within two of the question was told to settle the item again:\n%s", role.messages[4])
	}
	if got := []int{recorded[3].Delivered[0].LeftInARow, recorded[4].Delivered[0].LeftInARow}; got[0] != 1 || got[1] != 2 {
		t.Errorf("left in a row after the question = %v, want 1 then 2", got)
	}
	if told := role.messages[6]; !strings.Contains(told, "first in your order, 2 times in a row") {
		t.Errorf("the pass after two leaves following the question was not told again:\n%s", told)
	}
	if first := recorded[5].Delivered[0]; first.Settled != runstate.SettledDecided {
		t.Errorf("sixth pass first = %+v, want it decided", first)
	}
}

func settledBy(settled sweep.Settled) *sweep.Result {
	result := complete("Settled the capacity design.")
	result.Settled = []sweep.Settled{settled}
	return result
}

func TestAPassThatStillLeavesTheItemIsRecordedAndTheDevelopmentManagerTold(t *testing.T) {
	t.Parallel()
	store := sweepStore(t)
	role := &wokenRole{answers: []scriptedTurn{
		leftAgainTurn("Took nothing."), leftAgainTurn("Took nothing."),
		leftAgainTurn("Took nothing."), leftAgainTurn("Still took nothing."),
	}}
	notes := &itemNotes{}
	recorded := fireArchitectPasses(t, capacityQueue(""), role, notes, store, 3)

	third := recorded[2]
	first := third.Delivered[0]
	if third.Failed || third.Result == nil || first.Taken || first.LeftInARow != 3 || !first.SentBack {
		t.Fatalf("third pass = %+v, want it kept, the item left a 3rd time after being sent back", third)
	}
	if !strings.Contains(third.Problem, "left "+capacityDesign+" for the 3rd time in a row") {
		t.Errorf("problem = %q, want the third leave said plainly", third.Problem)
	}
	written := notes.notes[capacityDesign]
	if note := written[len(written)-1]; !strings.Contains(note, "3rd pass in a row") || !strings.Contains(note, "it did neither") || !strings.Contains(note, "development manager is told") {
		t.Errorf("note = %q, want the third leave said on the item", note)
	}

	// The development manager's next pass is told.
	manager := &wokenRole{answers: []scriptedTurn{{result: complete("Looked.")}}}
	trigger := Trigger{Tasks: map[string]config.RecurringTask{"manager-pass": {
		Role: domain.RoleDevelopmentManager, Every: config.Duration(time.Hour), Enabled: true, Prompt: "Look.", MaxTurns: 1,
	}}, Claims: store, Reports: store, Roles: manager, Clock: &movingRecurringClock{now: recurringNow.Add(4 * time.Hour)}}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if told := manager.messages[0]; !strings.Contains(told, "- "+capacityDesign+": left 3 times in a row by the architect's recurring pass architect-pass") || !strings.Contains(told, spreadReason) {
		t.Errorf("development manager's message does not name the item:\n%s", told)
	}
}

func TestABlockedItemLeftRepeatedlyIsNotForced(t *testing.T) {
	t.Parallel()
	role := &wokenRole{answers: []scriptedTurn{
		leftAgainTurn("Took nothing."), leftAgainTurn("Took nothing."),
		leftAgainTurn("Took nothing."), leftAgainTurn("Took nothing."),
	}}
	notes := &itemNotes{}
	recorded := fireArchitectPasses(t, capacityQueue("it depends on yoyodyne-7o2, not yet finished"), role, notes, sweepStore(t), 4)

	if len(role.messages) != 4 {
		t.Fatalf("turns = %d, want one per pass: a held item is never sent back", len(role.messages))
	}
	for index, message := range role.messages {
		if strings.Contains(message, "This pass does not leave it again") {
			t.Errorf("pass %d was told to settle an item held outside the role:\n%s", index+1, message)
		}
	}
	for index, pass := range recorded {
		first := pass.Delivered[0]
		if first.LeftInARow != 0 || first.SentBack || first.HeldOutside == "" {
			t.Errorf("pass %d first = %+v, want it left uncounted with what holds it recorded", index+1, first)
		}
	}
	for _, note := range notes.notes[capacityDesign] {
		if strings.Contains(note, "in a row") {
			t.Errorf("note = %q, want no count on a held item", note)
		}
	}
}

// A note that a pass left an item is written once per reason: a later pass
// leaving it for the same reason writes nothing and is marked a repeat on its
// record, and a pass giving a different reason writes a new note.
func TestAPassedOverNoteIsWrittenOncePerReason(t *testing.T) {
	t.Parallel()
	changed := complete("Took nothing.")
	changed.Left = []sweep.Left{{Item: capacityDesign, Reason: "the ruling waits on the measured figures"}}
	role := &wokenRole{answers: []scriptedTurn{
		leftAgainTurn("Took nothing."), leftAgainTurn("Took nothing."), leftAgainTurn("Took nothing."),
		{result: changed}, {result: changed},
	}}
	notes := &itemNotes{}
	// Held outside the role, so the passes are never told to decide it and every
	// leave stands at the same stage.
	recorded := fireArchitectPasses(t, capacityQueue("depends on yoyodyne-ifd.429.40, which is open"), role, notes, sweepStore(t), 5)

	written := notes.notes[capacityDesign]
	if len(written) != 2 {
		t.Fatalf("notes = %v, want one note for each of the two reasons", written)
	}
	if !strings.Contains(written[0], "Reason: "+spreadReason) || !strings.Contains(written[1], "Reason: the ruling waits on the measured figures") {
		t.Errorf("notes = %v, want the first reason and then the changed one", written)
	}
	for index, want := range []bool{false, true, true, false, true} {
		if first := recorded[index].Delivered[0]; first.NoteRepeated != want || first.NoteUnwritten {
			t.Errorf("pass %d first = %+v, want note repeated %v", index+1, first, want)
		}
	}
	since, repeated := runstate.PassedOverRepeats(recorded, "architect-pass", capacityDesign)
	if !repeated || since.Times != 1 || !since.NotedAt.Equal(recorded[3].StartedAt) || !since.Latest.Equal(recorded[4].StartedAt) {
		t.Errorf("repeats = %+v (%v), want the changed reason's note left again once since", since, repeated)
	}
}

// Passes in a row leaving an item for the same reason still write the note
// that says the next pass is told to decide it or ask, and the one that says a
// pass told so left it anyway; past that, they repeat it.
func TestALeaveReachingANewStageWritesItsNoteThoughTheReasonIsUnchanged(t *testing.T) {
	t.Parallel()
	role := &wokenRole{answers: []scriptedTurn{
		leftAgainTurn("Took nothing."), leftAgainTurn("Took nothing."),
		leftAgainTurn("Took nothing."), leftAgainTurn("Still nothing."),
		leftAgainTurn("Took nothing."), leftAgainTurn("Still nothing."),
	}}
	notes := &itemNotes{}
	recorded := fireArchitectPasses(t, capacityQueue(""), role, notes, sweepStore(t), 4)

	written := notes.notes[capacityDesign]
	if len(written) != 3 {
		t.Fatalf("notes = %v, want the first leave, the second in a row, and the third after being told", written)
	}
	if !strings.Contains(written[2], "3rd pass in a row") {
		t.Errorf("third note = %q, want it to say the pass told to decide left it anyway", written[2])
	}
	if first := recorded[3].Delivered[0]; !first.NoteRepeated || first.LeftInARow != 4 {
		t.Errorf("fourth pass first = %+v, want it a repeat of the third pass's note", first)
	}
}

// A note that could not be written is not repeated: the next pass leaving the
// item for the same reason writes it.
func TestANoteThatCouldNotBeWrittenIsWrittenByTheNextPass(t *testing.T) {
	t.Parallel()
	role := &wokenRole{answers: []scriptedTurn{leftAgainTurn("Took nothing."), leftAgainTurn("Took nothing.")}}
	notes := &failingOnceNotes{}
	store := sweepStore(t)
	clock := &movingRecurringClock{now: recurringNow}
	trigger := Trigger{Tasks: architectPass(), Claims: store, Reports: store, Roles: role,
		ConversationWork: capacityQueue("blocked in the tracker"), ItemNotes: notes, Clock: clock}
	for range 2 {
		if _, err := trigger.Fire(context.Background()); err != nil {
			t.Fatalf("Fire() error = %v", err)
		}
		clock.now = clock.now.Add(time.Hour)
	}
	recorded, _, err := store.List()
	if err != nil || len(recorded) != 2 {
		t.Fatalf("recorded = %+v (%v), want two passes", recorded, err)
	}
	if first := recorded[0].Delivered[0]; !first.NoteUnwritten || first.NoteRepeated {
		t.Errorf("first pass first = %+v, want its note marked unwritten", first)
	}
	if first := recorded[1].Delivered[0]; first.NoteRepeated || first.NoteUnwritten || len(notes.notes[capacityDesign]) != 1 {
		t.Errorf("second pass first = %+v, notes = %v, want the note written by the second pass", first, notes.notes)
	}
}

// failingOnceNotes refuses the first note and takes every one after it.
type failingOnceNotes struct {
	itemNotes
	refused bool
}

func (n *failingOnceNotes) AppendNote(ctx context.Context, id, note string) error {
	if !n.refused {
		n.refused = true
		return errors.New("bd update refused")
	}
	return n.itemNotes.AppendNote(ctx, id, note)
}

// A trigger with no tracker to write notes to records each leave's note as
// unwritten, so no pass counts itself a repeat of a note nobody wrote.
func TestALeaveWithNoTrackerIsNeverCountedAsARepeat(t *testing.T) {
	t.Parallel()
	role := &wokenRole{answers: []scriptedTurn{leftAgainTurn("Took nothing."), leftAgainTurn("Took nothing.")}}
	store := sweepStore(t)
	clock := &movingRecurringClock{now: recurringNow}
	trigger := Trigger{Tasks: architectPass(), Claims: store, Reports: store, Roles: role,
		ConversationWork: capacityQueue("blocked in the tracker"), Clock: clock}
	for range 2 {
		if _, err := trigger.Fire(context.Background()); err != nil {
			t.Fatalf("Fire() error = %v", err)
		}
		clock.now = clock.now.Add(time.Hour)
	}
	recorded, _, err := store.List()
	if err != nil || len(recorded) != 2 {
		t.Fatalf("recorded = %+v (%v), want two passes", recorded, err)
	}
	for index, pass := range recorded {
		if first := pass.Delivered[0]; !first.NoteUnwritten || first.NoteRepeated {
			t.Errorf("pass %d first = %+v, want its note marked unwritten and not a repeat", index+1, first)
		}
	}
}
