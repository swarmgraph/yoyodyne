package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

const raisedWords = "put the MCP pieces ahead of everything else"

// raisedSession is a Lead Product Manager conversation whose first message is
// said by whoever "from" names ("" for a pass, which is nobody), answered with
// the actions given.
func raisedSession(t *testing.T, tracker *fakeTracker, from string, actions ...string) (*Session, Reply) {
	t.Helper()
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Recording it.", actions...)},
		{SessionID: "session-1", FinalText: "Done."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	session := openTestSession(t, options)
	if from != "" {
		session.FromPerson(from)
	}
	reply, err := session.Send(context.Background(), "Please "+raisedWords+", I need them first.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	return session, reply
}

func raisedQueue() *fakeTracker {
	created := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	items := []beads.WorkItem{
		{ID: "yoyodyne-mcp", Title: "MCP piece", Status: "open", Priority: 1, CreatedAt: created},
		{ID: "yoyodyne-later", Title: "Later work", Status: "open", Priority: 2, CreatedAt: created},
		{ID: "yoyodyne-first", Title: "Earlier work", Status: "open", Priority: 0, CreatedAt: created},
	}
	tracker := &fakeTracker{items: map[string]beads.WorkItem{}, open: items}
	for _, item := range items {
		tracker.items[item.ID] = item
	}
	return tracker
}

// Recording a raising quotes his own message by its turn, and puts the label
// and the fixed-form note on the item in one write.
func TestARaisingFromTheOperatorsOwnWordsWritesTheLabelAndNoteTogether(t *testing.T) {
	t.Parallel()

	tracker := raisedQueue()
	session, reply := raisedSession(t, tracker, "Slack",
		`{"action":"operator_raised","id":"yoyodyne-mcp","decision":"record","turn":0,"words":"`+raisedWords+`","reason":"he asked for the MCP pieces first"}`)

	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the raising recorded", reply.Actions)
	}
	if len(tracker.updates) != 1 {
		t.Fatalf("updates = %#v, want exactly one write", tracker.updates)
	}
	written := tracker.updates[0]
	if written.id != "yoyodyne-mcp" || len(written.change.AddLabels) != 1 || written.change.AddLabels[0] != domain.RaisedByOperatorLabel {
		t.Fatalf("write = %#v, want the label added", written)
	}
	note, found := domain.LatestOperatorRaising(written.change.AppendNotes)
	if !found || note.Ended {
		t.Fatalf("notes = %q, want a raising in the fixed form", written.change.AppendNotes)
	}
	if note.Words != raisedWords || note.AfterTurn != 0 || note.Conversation != session.state.ConversationID || note.At.IsZero() {
		t.Fatalf("raising = %#v", note)
	}
	if len(note.Above) != 1 || note.Above[0] != "yoyodyne-later" {
		t.Fatalf("raised above = %v, want only the item behind it", note.Above)
	}
	if !strings.Contains(written.change.AppendNotes, "he asked for the MCP pieces first") {
		t.Fatalf("notes = %q, want the reason", written.change.AppendNotes)
	}
}

// Words a pass sent, words he never wrote, and a turn with no message of his
// are each refused, and nothing is written.
func TestARaisingIsRefusedUnlessTheTurnIsHisAndHoldsTheWords(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		from, action, want string
	}{
		"a pass's message": {"",
			`{"action":"operator_raised","id":"yoyodyne-mcp","decision":"record","turn":0,"words":"` + raisedWords + `","reason":"r"}`,
			"holds no message the operator sent himself after turn 0"},
		"words he did not write": {"Slack",
			`{"action":"operator_raised","id":"yoyodyne-mcp","decision":"record","turn":0,"words":"drop everything","reason":"r"}`,
			"does not contain those words"},
		"another turn": {"the console",
			`{"action":"operator_raised","id":"yoyodyne-mcp","decision":"record","turn":4,"words":"` + raisedWords + `","reason":"r"}`,
			"His most recent messages here are: after turn 0"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tracker := raisedQueue()
			_, reply := raisedSession(t, tracker, tc.from, tc.action)
			if len(tracker.updates) != 0 {
				t.Fatalf("written = %#v, want nothing", tracker.updates)
			}
			if len(reply.Actions) != 1 || reply.Actions[0].Applied || !strings.Contains(reply.Actions[0].Failure, tc.want) {
				t.Fatalf("actions = %#v, want refused with %q", reply.Actions, tc.want)
			}
		})
	}
}

// Ending quotes him the same way, and takes the label off in the same write
// as the closing note.
func TestEndingARaisingRemovesTheLabelWithItsClosingNote(t *testing.T) {
	t.Parallel()

	tracker := raisedQueue()
	raised := tracker.items["yoyodyne-mcp"]
	raised.Labels = []string{"product", domain.RaisedByOperatorLabel}
	tracker.items["yoyodyne-mcp"] = raised
	_, reply := raisedSession(t, tracker, "the console",
		`{"action":"operator_raised","id":"yoyodyne-mcp","decision":"end","turn":0,"words":"`+raisedWords+`","reason":"he says it is done"}`)

	if len(reply.Actions) != 1 || !reply.Actions[0].Applied || len(tracker.updates) != 1 {
		t.Fatalf("actions = %#v, updates = %#v, want one write", reply.Actions, tracker.updates)
	}
	change := tracker.updates[0].change
	if len(change.RemoveLabels) != 1 || change.RemoveLabels[0] != domain.RaisedByOperatorLabel || len(change.AddLabels) != 0 {
		t.Fatalf("change = %#v, want the label removed", change)
	}
	note, found := domain.LatestOperatorRaising(change.AppendNotes)
	if !found || !note.Ended || note.Words != raisedWords {
		t.Fatalf("notes = %q, want the closing note in the fixed form", change.AppendNotes)
	}
}

// No other action writes the label: creating with it and labelling with it, on
// or off, are refused before anything runs.
func TestOnlyTheRaisingActionWritesTheRaisedLabel(t *testing.T) {
	t.Parallel()

	for name, action := range map[string]TrackerAction{
		"label add":    {Action: actionLabel, ID: "yoyodyne-mcp", Add: domain.RaisedByOperatorLabel, Reason: "r"},
		"label remove": {Action: actionLabel, ID: "yoyodyne-mcp", Remove: domain.RaisedByOperatorLabel, Reason: "r"},
		"create": {Action: actionCreate, Title: "t", Description: "d", Goal: "g", Kind: domain.WorkItemKindFeature,
			Labels: []string{"product", domain.RaisedByOperatorLabel}, Reason: "r"},
	} {
		if err := action.Validate(); err == nil || !strings.Contains(err.Error(), `only by "operator_raised"`) {
			t.Errorf("%s: Validate() = %v, want the raised label refused", name, err)
		}
	}
	other := TrackerAction{Action: actionLabel, ID: "yoyodyne-mcp", Add: "product", Reason: "r"}
	if err := other.Validate(); err != nil {
		t.Fatalf("an ordinary label: Validate() = %v", err)
	}
}

// Reordering that would move work ahead of a raised item is refused, whether
// it raises other work past it, places other work first among its priority,
// or moves the raised item itself back; reordering that passes nothing raised
// is carried out.
func TestReorderingDoesNotMoveWorkAheadOfARaisedItem(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		action  string
		refused bool
	}{
		"raising other work past it":   {`{"action":"reprioritize","id":"yoyodyne-later","priority":0,"reason":"r"}`, true},
		"placing other work before it": {`{"action":"place","id":"yoyodyne-peer","reason":"r"}`, true},
		"moving it back":               {`{"action":"reprioritize","id":"yoyodyne-mcp","priority":3,"reason":"r"}`, true},
		"moving other work up behind":  {`{"action":"reprioritize","id":"yoyodyne-later","priority":1,"reason":"r"}`, false},
		"moving it further up":         {`{"action":"reprioritize","id":"yoyodyne-mcp","priority":0,"reason":"r"}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tracker := raisedQueue()
			raised := tracker.items["yoyodyne-mcp"]
			raised.Labels = []string{domain.RaisedByOperatorLabel}
			tracker.items[raised.ID] = raised
			peer := beads.WorkItem{ID: "yoyodyne-peer", Title: "Peer", Status: "open", Priority: 1, CreatedAt: raised.CreatedAt.Add(time.Hour)}
			tracker.items[peer.ID] = peer
			tracker.open = []beads.WorkItem{raised, tracker.items["yoyodyne-later"], tracker.items["yoyodyne-first"], peer}
			_, reply := raisedSession(t, tracker, "", tc.action)

			if len(reply.Actions) != 1 {
				t.Fatalf("actions = %#v", reply.Actions)
			}
			if tc.refused {
				if reply.Actions[0].Applied || len(tracker.updates) != 0 || !strings.Contains(reply.Actions[0].Failure, "yoyodyne-mcp") {
					t.Fatalf("actions = %#v, updates = %#v, want refused naming the raised item", reply.Actions, tracker.updates)
				}
				return
			}
			if !reply.Actions[0].Applied || len(tracker.updates) != 1 {
				t.Fatalf("actions = %#v, want carried out", reply.Actions)
			}
		})
	}
}

// A blocked item and one in progress keep their place in the order, so a raised
// one in either state is not passed.
func TestReorderingDoesNotMoveWorkAheadOfARaisedItemThatIsNotOpen(t *testing.T) {
	t.Parallel()

	for _, status := range []string{"blocked", "in_progress"} {
		tracker := raisedQueue()
		raised := tracker.items["yoyodyne-mcp"]
		raised.Labels, raised.Status = []string{domain.RaisedByOperatorLabel}, status
		tracker.items[raised.ID] = raised
		tracker.open = []beads.WorkItem{tracker.items["yoyodyne-later"], tracker.items["yoyodyne-first"]}
		if status == "blocked" {
			tracker.blockedItems = []beads.WorkItem{raised}
		} else {
			tracker.claimedItems = []beads.WorkItem{raised}
		}
		_, reply := raisedSession(t, tracker, "", `{"action":"reprioritize","id":"yoyodyne-later","priority":0,"reason":"r"}`)
		assertRaisedRefusal(t, tracker, reply, "this would put yoyodyne-later ahead of yoyodyne-mcp")
	}
}

// One raised item is not moved past another, by priority or by placement, and
// one moved back behind another is refused as falling behind it.
func TestReorderingDoesNotMoveOneRaisedItemPastAnother(t *testing.T) {
	t.Parallel()

	for action, want := range map[string]string{
		`{"action":"reprioritize","id":"yoyodyne-second","priority":0,"reason":"r"}`: "this would put yoyodyne-second ahead of yoyodyne-mcp",
		`{"action":"place","id":"yoyodyne-second","reason":"r"}`:                     "this would put yoyodyne-second ahead of yoyodyne-mcp",
		`{"action":"reprioritize","id":"yoyodyne-mcp","priority":2,"reason":"r"}`:    "this would put yoyodyne-second ahead of it",
	} {
		tracker := raisedQueue()
		raised := tracker.items["yoyodyne-mcp"]
		raised.Labels = []string{domain.RaisedByOperatorLabel}
		second := raised
		second.ID, second.CreatedAt = "yoyodyne-second", raised.CreatedAt.Add(time.Hour)
		tracker.items[raised.ID], tracker.items[second.ID] = raised, second
		tracker.open = []beads.WorkItem{raised, second}
		_, reply := raisedSession(t, tracker, "", action)
		assertRaisedRefusal(t, tracker, reply, want)
	}
}

// Removing a raised item's placement would let the rest of its priority go
// ahead of it, so it is refused.
func TestUnplacingDoesNotMoveWorkAheadOfARaisedItem(t *testing.T) {
	t.Parallel()

	tracker := raisedQueue()
	raised := tracker.items["yoyodyne-mcp"]
	raised.Labels = []string{domain.RaisedByOperatorLabel}
	raised.Placement = placement(1, raised.CreatedAt.Add(time.Hour))
	older := beads.WorkItem{ID: "yoyodyne-older", Title: "Older", Status: "open", Priority: 1, CreatedAt: raised.CreatedAt.Add(-time.Hour)}
	tracker.items[raised.ID], tracker.items[older.ID] = raised, older
	tracker.open = []beads.WorkItem{raised, older}
	_, reply := raisedSession(t, tracker, "", `{"action":"unplace","id":"yoyodyne-mcp","reason":"r"}`)
	assertRaisedRefusal(t, tracker, reply, "this would put yoyodyne-older ahead of it")
}

func assertRaisedRefusal(t *testing.T, tracker *fakeTracker, reply Reply, want string) {
	t.Helper()
	if len(reply.Actions) != 1 || reply.Actions[0].Applied || len(tracker.updates) != 0 || !strings.Contains(reply.Actions[0].Failure, want) {
		t.Fatalf("actions = %#v, updates = %#v, want refused with %q and nothing written", reply.Actions, tracker.updates, want)
	}
}

// The prompt names the turn a person's message is filed under, and says nothing
// of the kind for text the harness wrote: a pass's message, or a prompt rebuilt
// for a later round of his own.
func TestOnlyThePersonsOwnMessageIsPresentedAsHis(t *testing.T) {
	t.Parallel()

	const line = "The operator sent this himself, after turn 0 of conversation "
	for from, want := range map[string]bool{"Slack": true, "": false} {
		provider := &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: "Noted."}}}
		session := openTestSession(t, testOptions(t, provider))
		session.FromPerson(from)
		if _, err := session.Send(context.Background(), "Hello."); err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		if got := strings.Contains(provider.requests[0].Prompt, line+session.state.ConversationID); got != want {
			t.Errorf("sent from %q: presented as the operator's = %t, want %t", from, got, want)
		}
		if strings.Contains(session.turnPrompt("Continue the interrupted turn", nil), "The operator sent this himself") {
			t.Errorf("sent from %q: a rebuilt later round is presented as the operator's", from)
		}
	}
}
