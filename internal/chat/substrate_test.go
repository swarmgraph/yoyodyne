package chat

// The case this exists for, replayed.
//
// On 2026-08-23 a child was carved from a failed run's deferral. It assumed two
// files that existed only on that run's branch — the pull request for it open,
// the item's repair budget spent — and nothing on the child recorded the
// prerequisite, so the tracker reported it ready. On 2026-09-25 the answer to
// that went the other way: the tracker refused to link each child of
// yoyodyne-ifd.429.13 to its own parent, and the harness blocked all six on the
// item itself with a blocker nothing cleared, though they superseded the
// parent's pull request rather than building on it. These are both of those
// and the states around them: the change that landed, the reparenting, and the
// records that could not be read.

import (
	"context"
	"errors"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// The parent of the founding case: the item whose run stopped with its change
// on a preserved branch and its pull request still open.
const substrateParent = "yoyodyne-ifd.100"

// substrateCommitID is the commit the parent's change is on.
const substrateCommitID = "abcdef0123456789abcdef0123456789abcdef01"

// carvedChild is the decomposition the 2026-09-25 case produced: a child that
// supersedes the parent's change, and says nothing about building on it.
const carvedChild = `{"action":"create","kind":"feature","title":"Carry the write through the document path","description":"Rewrites the document path from the target branch, superseding the parent's pull request.","goal":"` + recordedGoal + `","parent":"` + substrateParent + `","priority":2,"reason":"the reviewer refused this half as out of scope"}`

// buildingChild is the founding case's decomposition: a child that says in its
// own text that it builds on the parent's files.
const buildingChild = `{"action":"create","kind":"feature","title":"Carry the write through the document path","description":"Builds on ` + substrateParent + `'s change: assumes internal/artifact/write.go and internal/chat/document.go from its branch.","goal":"` + recordedGoal + `","parent":"` + substrateParent + `","priority":2,"reason":"the reviewer refused this half as out of scope"}`

// What the item is for: a re-scope of work whose change never landed leaves the
// child open, with where the parent's change is recorded on it as guidance, and
// never links it or blocks it — the tracker would refuse the link, and a
// blocked status is the blocker nobody clears.
func TestAChildCarvedFromAnUnlandedChangeIsLeftOpenWithGuidance(t *testing.T) {
	t.Parallel()

	tracker := substrateTracker()
	// The tracker refuses a child waiting on its own parent, as bd does.
	tracker.linkErr = errors.New("bd dep add failed: the child already depends on its parent")
	reply := substrateReply(t, tracker, unlandedParent(), trackerReply("Splitting out the refused half.", carvedChild))
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	created := reply.Actions[0].WorkItemID
	if len(tracker.links) != 0 || len(tracker.blocked) != 0 {
		t.Fatalf("the child was linked or blocked: links %#v, blocked %#v", tracker.links, tracker.blocked)
	}
	if len(tracker.updates) != 1 || tracker.updates[0].id != created {
		t.Fatalf("updates = %#v, want the guidance on %s alone", tracker.updates, created)
	}
	notes := tracker.updates[0].change.AppendNotes
	for _, want := range []string{
		"Guidance on " + substrateParent + "'s change",
		"never reached main",
		"is on yoyodyne/ifd-100",
		"at commit " + substrateCommitID,
		"pull request #174",
		"Nothing here holds this item",
		`builds on ` + substrateParent + `'s change`,
	} {
		if !strings.Contains(notes, want) {
			t.Fatalf("the child's notes are missing %q:\n%s", want, notes)
		}
	}
	rendered := renderTrackerOutcomes(domain.RoleDevelopmentManager, reply.Actions)
	for _, want := range []string{
		"is open and not held",
		`put "builds on ` + substrateParent + `'s change" in its description`,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the outcome is missing %q:\n%s", want, rendered)
		}
	}
}

// A child that says it builds on the parent's files is held, and held by the
// scheduler reading that rather than by anything written onto the child.
func TestAChildThatSaysItBuildsOnTheParentsChangeIsHeldByTheScheduler(t *testing.T) {
	t.Parallel()

	tracker := substrateTracker()
	tracker.linkErr = errors.New("bd dep add failed: the child already depends on its parent")
	reply := substrateReply(t, tracker, unlandedParent(), trackerReply("Splitting out the refused half.", buildingChild))
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if len(tracker.links) != 0 || len(tracker.blocked) != 0 {
		t.Fatalf("the child was linked or blocked: links %#v, blocked %#v", tracker.links, tracker.blocked)
	}
	if summary := reply.Actions[0].Summary; !strings.Contains(summary, "the scheduler holds it until that change lands") {
		t.Fatalf("the outcome did not say the child is held: %s", summary)
	}
}

// The other half of the same rule: ordinary decomposition is untouched. A parent
// whose change is on the target branch leaves its children standing on
// something, so nothing is added to them and the development manager's own
// dependency structure is the whole of what they carry.
func TestAChildOfWorkWhoseChangeLandedIsNotHeld(t *testing.T) {
	t.Parallel()

	tracker := substrateTracker()
	reply := substrateReply(t, tracker, &fakeStoppedRuns{}, trackerReply("Breaking the rest out.", carvedChild))
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if len(tracker.links) != 0 || len(tracker.updates) != 0 || len(tracker.blocked) != 0 {
		t.Fatalf("an ordinary decomposition was gated: links %#v, updates %#v, blocked %#v",
			tracker.links, tracker.updates, tracker.blocked)
	}
	if summary := reply.Actions[0].Summary; strings.Contains(summary, "waits for") {
		t.Fatalf("the operator was told an ungated child was held: %s", summary)
	}
}

// Reparenting names a parent too and records no guidance: what the guidance
// rests on is that the child's text was written just now against the change the
// role is looking at, and an item moved under a new parent was written earlier.
// This pins that as a decision rather than leaving it to read as an oversight
// somebody later closes without noticing what it would assert.
func TestReparentingUnderWorkWhoseChangeHasNotLandedIsNotGated(t *testing.T) {
	t.Parallel()

	const moved = "yoyodyne-ifd.101"
	tracker := substrateTracker()
	tracker.items[moved] = beads.WorkItem{ID: moved, Title: "work written before any of this", Status: "open"}
	reply := substrateReply(t, tracker, unlandedParent(), trackerReply("It belongs under the stopped item.",
		`{"action":"reparent","id":"`+moved+`","parent":"`+substrateParent+`","reason":"it is part of that work"}`))
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if len(tracker.links) != 0 || len(tracker.blocked) != 0 {
		t.Fatalf("a reparenting was gated: links %#v, blocked %#v", tracker.links, tracker.blocked)
	}
	// The move itself is the one update, and nothing wrote a substrate note beside
	// it: the harness has no evidence that this item assumes the parent's files.
	if len(tracker.updates) != 1 || tracker.updates[0].change.AppendNotes != "" {
		t.Fatalf("updates = %#v, want the move alone", tracker.updates)
	}
}

// Records that cannot be read establish nothing, so the gate says so rather than
// letting the creation read as one it examined and cleared. An unheld child
// nobody was told about is the failure this whole file is about.
func TestADecompositionWhoseSubstrateCannotBeReadSaysSo(t *testing.T) {
	t.Parallel()

	tracker := substrateTracker()
	stoppages := unlandedParent()
	stoppages.unreadable = substrateParent
	reply := substrateReply(t, tracker, stoppages, trackerReply("Splitting out the refused half.", carvedChild))
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if len(tracker.links) != 0 {
		t.Fatalf("links = %#v, want none from records nothing could be read from", tracker.links)
	}
	if len(tracker.blocked) != 0 {
		t.Fatalf("blocked = %#v, want none", tracker.blocked)
	}
	if summary := reply.Actions[0].Summary; !strings.Contains(summary, "is not held for it") {
		t.Fatalf("the summary did not say the child is unheld: %s", summary)
	}
}

// unlandedParent is the founding case's run records: ifd.100's change, on the
// branch its stopped run preserved, published and unmerged.
func unlandedParent() *fakeStoppedRuns {
	return &fakeStoppedRuns{unlanded: map[string]UnlandedChange{
		substrateParent: {
			RunID:        "run-0123456789abcdef0123456789abcdef",
			Branch:       "yoyodyne/ifd-100",
			Commit:       substrateCommitID,
			TargetBranch: "main",
			PullRequest:  174,
		},
	}}
}

func substrateTracker() *fakeTracker {
	return &fakeTracker{items: map[string]beads.WorkItem{
		substrateParent: {ID: substrateParent, Title: "the item whose run stopped", Status: "open"},
	}}
}

// substrateReply is a development manager decomposing, with the run records
// wired the way the command line wires them for that role and for no other.
func substrateReply(t *testing.T, tracker Tracker, stoppages Stoppages, answer string) Reply {
	t.Helper()

	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: answer},
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: "That is what I filed."},
	}})
	options.Role = domain.RoleDevelopmentManager
	options.Agent = string(domain.RoleDevelopmentManager)
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)
	options.Stoppages = stoppages
	reply, err := openTestSession(t, options).Send(context.Background(), "Decompose what the run left.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	return reply
}
