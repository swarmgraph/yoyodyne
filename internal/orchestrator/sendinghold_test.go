package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// holdTracker is the tracker a conversation reads a hold's work item from and
// writes a finished document's notes into.
type holdTracker struct {
	chat.Tracker
	status map[string]string
	notes  map[string][]string
}

func (h *holdTracker) Show(_ context.Context, id string) (beads.WorkItem, error) {
	status, ok := h.status[id]
	if !ok {
		return beads.WorkItem{}, fmt.Errorf("no issue found matching %q", id)
	}
	return beads.WorkItem{ID: id, Title: id, Status: status}, nil
}

func (h *holdTracker) Update(_ context.Context, id string, change beads.WorkItemChange) (beads.WorkItem, error) {
	if _, ok := h.status[id]; !ok {
		return beads.WorkItem{}, fmt.Errorf("no issue found matching %q", id)
	}
	if h.notes == nil {
		h.notes = map[string][]string{}
	}
	h.notes[id] = append(h.notes[id], change.AppendNotes)
	return beads.WorkItem{ID: id, Status: h.status[id]}, nil
}

func heldDesign(id, body, answers, until string) string {
	return fmt.Sprintf("Finished the addendum; sending waits for the retry fix.\n%s\n{\"documents\":[{\"action\":\"create\",\"id\":%q,\"kind\":\"design\",\"title\":\"Callable contract addendum\",\"directory\":\"docs/designs\",\"body\":%q,\"reason\":\"the remaining contract fields, designed\",\"answers\":[%q],\"hold\":{\"until_closed\":%q,\"reason\":\"a stopped publication is not retried until that fix merges\"}}]}\n```", artifact.WriteFence, id, body, answers, until)
}

// A turn under a hold on sending finishes the unfinished design, the document
// is kept and noted on the item it answers, nothing is sent and nothing is put
// to the operator; the turn after the hold's item closes sends it from the kept
// text without the role writing it again.
func TestADocumentHeldForSendingIsKeptNotedAndSentOnceItsHoldLifts(t *testing.T) {
	repository, _, provider, pipeline, runs := automaticFixture(t)
	const body = "# Callable contract addendum\n\nThe remaining contract fields, decided."
	original := provider.Respond
	architectTurns := 0
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role != domain.RoleArchitect {
			return original(request)
		}
		architectTurns++
		text := "Nothing new to rule on."
		if architectTurns == 1 {
			text = heldDesign("callable-contract-addendum", body, "yoyodyne-ab2", "yoyodyne-ifd.433.21.2")
		}
		return backend.RunResult{Backend: domain.BackendClaudeCode, SessionID: "author-session", FinalText: text}, nil
	}
	tracker := &holdTracker{status: map[string]string{"yoyodyne-ab2": "open", "yoyodyne-ifd.433.21.2": "in_progress"}}
	options, store := publicationConversation(t, pipeline, provider)
	options.Tracker = tracker
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := session.Send(context.Background(), "Rule on the work waiting in your conversation.")
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.DocumentRefusals) != 0 {
		t.Fatalf("the held document was refused: %v", reply.DocumentRefusals)
	}
	if recorded, err := runs.Recorded(); err != nil || len(recorded) != 0 {
		t.Fatalf("runs = %+v, %v: a held document was sent", recorded, err)
	}
	if len(reply.Writes) != 0 || len(session.Writes()) != 0 {
		t.Fatal("a document held by its owner was put to the operator")
	}
	held := session.HeldDocuments()
	if len(held) != 1 || held[0].UntilClosed != "yoyodyne-ifd.433.21.2" || len(held[0].Answers) != 1 || held[0].Answers[0] != "yoyodyne-ab2" {
		t.Fatalf("held = %+v, want the addendum held until the retry fix closes", held)
	}
	// The ruling reaches the item it answers as it is written.
	notes := tracker.notes["yoyodyne-ab2"]
	if len(notes) != 1 || !strings.Contains(notes[0], body) || !strings.Contains(notes[0], "waits until yoyodyne-ifd.433.21.2 closes") || !strings.Contains(notes[0], held[0].WriteID) {
		t.Fatalf("notes on the answered item = %q, want the document's text and its hold", notes)
	}
	// The turn is told the document is kept, held, and why.
	saved, err := store.Load(runstate.ConversationIdentity{Role: domain.RoleArchitect, Agent: "architect"})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.PendingWrites) != 1 || !saved.PendingWrites[0].Held() {
		t.Fatalf("pending writes = %+v, want the document kept, held", saved.PendingWrites)
	}
	told := strings.Join(saved.PendingNotices, "\n") + saved.PendingTrackerResults
	if !strings.Contains(told, "held until yoyodyne-ifd.433.21.2 closes") || !strings.Contains(told, "held for sending, not ruled") {
		t.Fatalf("the role was not told the document is held and why: %q", told)
	}

	// A message while the hold stands sends nothing.
	if _, err := session.Send(context.Background(), "Anything new?"); err != nil {
		t.Fatal(err)
	}
	if recorded, err := runs.Recorded(); err != nil || len(recorded) != 0 {
		t.Fatalf("runs = %+v, %v: sent while the hold stood", recorded, err)
	}

	// The fix merges; the next message sends what was held, by nobody's hand.
	tracker.status["yoyodyne-ifd.433.21.2"] = "closed"
	session, err = chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Anything new?"); err != nil {
		t.Fatal(err)
	}
	recorded, err := runs.Recorded()
	if err != nil || len(recorded) != 1 || recorded[0].Status != runstate.StatusSucceeded || recorded[0].Document == nil {
		t.Fatalf("runs = %+v, %v: want the held document sent and landed", recorded, err)
	}
	content := publicationGit(t, repository, "show", "main:docs/designs/callable-contract-addendum.md")
	if !strings.Contains(content, body) || content != strings.TrimSpace(recorded[0].Document.Candidate.Content) {
		t.Fatalf("published = %q, want the kept text", content)
	}
	if architectTurns != 3 {
		t.Fatalf("architect turns = %d, want three: the document was not written again", architectTurns)
	}
	if len(session.HeldDocuments()) != 0 {
		t.Fatalf("held = %+v after the hold lifted", session.HeldDocuments())
	}
}

// A hold naming a work item the tracker does not hold could never lift, so the
// document is refused before anything is kept or noted.
func TestAHoldNamingNoWorkItemIsRefused(t *testing.T) {
	_, _, provider, pipeline, runs := automaticFixture(t)
	original := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role != domain.RoleArchitect {
			return original(request)
		}
		return backend.RunResult{Backend: domain.BackendClaudeCode, SessionID: "author-session", FinalText: heldDesign("addendum", "# A\n\nB.", "yoyodyne-ab2", "yoyodyne-nothing")}, nil
	}
	tracker := &holdTracker{status: map[string]string{"yoyodyne-ab2": "open"}}
	options, _ := publicationConversation(t, pipeline, provider)
	options.Tracker = tracker
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := session.Send(context.Background(), "Rule.")
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.DocumentRefusals) == 0 || !strings.Contains(reply.DocumentRefusals[0], "yoyodyne-nothing") {
		t.Fatalf("refusals = %v, want the hold's missing item named", reply.DocumentRefusals)
	}
	if len(session.HeldDocuments()) != 0 || len(tracker.notes) != 0 {
		t.Fatalf("held = %+v, notes = %v, want nothing kept or noted", session.HeldDocuments(), tracker.notes)
	}
	if recorded, _ := runs.Recorded(); len(recorded) != 0 {
		t.Fatalf("runs = %+v", recorded)
	}
}

// An item whose finished document is held is recorded taken and held for
// sending, never as ruled; an item the account calls held with no document
// behind it is recorded not taken, and the item first in the order is noted.
func TestAPassRecordsHeldItemsApartFromRuledOnes(t *testing.T) {
	t.Parallel()
	result := complete("Finished the addendum and held it; the dashboard ruling is held too.")
	result.Held = []string{"yoyodyne-ab2", "yoyodyne-ifd.414.1"}
	role := &wokenRole{answers: []scriptedTurn{{
		result:  result,
		actedOn: []string{"yoyodyne-gtx"},
		held:    map[string]string{"yoyodyne-ab2": "document-7.1"},
	}}}
	notes := &itemNotes{}
	recorded := firePassedOver(t, threeTopItems(), role, notes)

	first, addendum, gtx := recorded.Delivered[0], recorded.Delivered[1], recorded.Delivered[3]
	if first.Taken || !strings.Contains(first.Reason, "keeps no document held for sending") {
		t.Fatalf("first = %+v, want it not taken: held is claimed with no document behind it", first)
	}
	if !addendum.Taken || addendum.HeldForSending != "document-7.1" {
		t.Fatalf("addendum = %+v, want it taken and held for sending", addendum)
	}
	if !gtx.Taken || gtx.HeldForSending != "" {
		t.Fatalf("gtx = %+v, want it taken and not held", gtx)
	}
	if note := notes.notes["yoyodyne-ifd.414.1"]; len(note) != 1 || !strings.Contains(note[0], "keeps no document held for sending") {
		t.Fatalf("notes = %v, want the first item noted with why it is not held", notes.notes)
	}

	passes := readmodel.ConversationPasses([]runstate.Sweep{recorded})
	if len(passes) != 1 || passes[0].Held != 1 {
		t.Fatalf("passes = %+v, want one held item counted", passes)
	}
	if says := passes[0].Says(); !strings.Contains(says, "1 of them HELD FOR SENDING rather than ruled") {
		t.Errorf("status line = %q, want held items said apart", says)
	}
	considered, ok := readmodel.LastConsidered([]runstate.Sweep{recorded}, "yoyodyne-ab2")
	if !ok || !strings.Contains(considered.Sentence(), "HELD FOR SENDING as document-7.1, not yet ruled") {
		t.Errorf("consideration = %+v, want the item read as held for sending", considered)
	}
	// The account's held list is what a role is told to use.
	if !strings.Contains(sweep.LeftContract(), `"held":["beads-id"]`) {
		t.Error("the pass is not told how to list held items")
	}
}
