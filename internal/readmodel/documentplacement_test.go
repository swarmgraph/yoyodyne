package readmodel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// placementSources is a project whose architect finished the design for a
// labelled item, confirmed, and which the harness cannot land because it cannot
// reach the companion intent repository; placed says whether the conversation
// has since found it there, which takes the write off its record.
func placementSources(t *testing.T, placed bool) Sources {
	t.Helper()
	item := beads.WorkItem{ID: "yoyodyne-item", Title: "Needs a design", Status: "open", Priority: 1, Labels: []string{domain.NeedsDesignLabel}}
	sources := designWaitSources([]beads.WorkItem{item}, map[string]time.Time{"yoyodyne-item": moment.Add(-30 * time.Hour)})
	sources.Now = func() time.Time { return moment }
	// Its kind is confirmed automatically, which must not make a document
	// already confirmed and waiting for a person's copy the harness's again.
	sources.DocumentConfirmations = fakeConfirmations{confirms: map[string]bool{"document-12.1": true}}
	architects := runstate.Conversation{ConversationID: "chat-arch", Agent: "architect", Role: domain.RoleArchitect}
	if !placed {
		architects.PendingWrites = []runstate.PendingWrite{{
			ID: "document-12.1", Turn: 12, WrittenAt: moment.Add(-20 * time.Hour), Action: "create", Artifact: "item-design", Kind: "design",
			Title: "Item design", Directory: "docs/designs", Body: "# Item design", Reason: "the design", Answers: []string{"yoyodyne-item"},
			Publication:     &runstate.DocumentPublication{Candidate: artifact.ConfirmedDocument{Artifact: artifact.Artifact{ID: "item-design", Path: "docs/designs/item-design.md"}}},
			AwaitsPlacement: &runstate.DocumentPlacement{Repository: "/work/intent", Path: "docs/designs/item-design.md", Since: moment.Add(-14*time.Hour - 15*time.Minute)},
		}}
	}
	sources.Conversations = fakeConversations{recorded: []runstate.Conversation{architects}}
	return sources
}

// A finished design that waits only for a person to put it in a repository the
// harness cannot reach is on the needs-a-human line as the step the operator's
// hand takes, naming the document, the repository and file it goes to, and how
// long it has waited. It is not under the architect's head, and the item it
// answers is no longer listed as waiting on her design.
func TestAFinishedDesignWaitingForAPersonsCopyIsTheOperatorsStepNotTheArchitects(t *testing.T) {
	t.Parallel()

	standing := ReadStanding(context.Background(), placementSources(t, false))
	var placement *Attention
	for i, entry := range standing.NeedsHuman {
		if entry.Kind == AttentionDocumentApproval && entry.DocumentApproval != nil && entry.DocumentApproval.WriteID == "document-12.1" {
			placement = &standing.NeedsHuman[i]
		}
	}
	if placement == nil {
		t.Fatalf("needs a human = %+v, want the finished design waiting to be put in place", standing.NeedsHuman)
	}
	if placement.Mover != MoverArchitect || placement.Mover.IsOperator() || !placement.Ownership.NeedsHand() || placement.Ownership.Hand.Need != ownership.HandDocumentPlacement {
		t.Fatalf("the entry resolves to %s with %+v, want the architect's finished work carrying the operator's hand to place it", placement.Mover, placement.Ownership)
	}
	what := placement.What()
	for _, want := range []string{"document-12.1", "item-design", "yoyodyne-item", "docs/designs/item-design.md", "/work/intent", localMoment(moment.Add(-14*time.Hour - 15*time.Minute)), "14 hours 15 minutes ago"} {
		if !strings.Contains(what, want) {
			t.Fatalf("what = %q, want it to name %q", what, want)
		}
	}
	if strings.Contains(what, "approval") || placement.DocumentApproval.HarnessConfirms || placement.Label() != "document waiting to be put in place" {
		t.Fatalf("what = %q, label = %q, want a placement rather than an approval", what, placement.Label())
	}
	if !strings.Contains(placement.Ownership.Hand.Types, "/landed document-12.1") || !strings.HasPrefix(placement.Whose(), MoverArchitect.Possessive()) {
		t.Fatalf("hand %+v, whose %q, want how to record the commit and the architect's possessive", placement.Ownership.Hand, placement.Whose())
	}
	if waits := designWaitsIn(standing.NeedsHuman); len(waits) != 0 {
		t.Fatalf("attention line waiting on design = %+v, want the item a finished design answers left off it", waits)
	}
	if waits := designWaitsIn(standing.FactoryProblems); len(waits) != 0 {
		t.Fatalf("factory problems waiting on design = %+v, want the item a finished design answers left off them", waits)
	}

	rendered := standing.renderNeedsHuman()
	hand := strings.Index(rendered, "  "+NeedsYourHand)
	if hand < 0 || !strings.Contains(rendered[hand:], "document-12.1") {
		t.Fatalf("rendered:\n%s\nwant the placement under %q", rendered, NeedsYourHand)
	}
	if strings.Contains(rendered, "Waiting on the architect") {
		t.Fatalf("rendered:\n%s\nstill lists something as waiting on the architect", rendered)
	}
	// It is no decision of the operator's, so it is not put to him on the
	// decisions page beside the documents he approves.
	for _, decision := range standing.OperatorDecisions {
		if decision.ID == "document-12.1" {
			t.Fatalf("the decisions page carries the placement: %+v", decision)
		}
	}

	// The dashboard's JSON carries where it goes and reads back whole.
	encoded, err := json.Marshal(*placement)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Attention
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("the entry does not read back: %v\n%s", err, encoded)
	}
	if decoded.DocumentApproval == nil || decoded.DocumentApproval.Placement == nil || decoded.DocumentApproval.Placement.Repository != "/work/intent" {
		t.Fatalf("decoded = %+v, want the placement carried", decoded.DocumentApproval)
	}
}

// Once the document is found in place its conversation settles it, and the
// entry goes by itself; the item's label still on, it waits on the architect
// again, which is hers to take off.
func TestThePlacementEntryClearsOnceTheDocumentIsFoundInPlace(t *testing.T) {
	t.Parallel()

	standing := ReadStanding(context.Background(), placementSources(t, true))
	for _, entry := range standing.NeedsHuman {
		if entry.Kind == AttentionDocumentApproval {
			t.Fatalf("a document found in place is still on the line: %s", entry.What())
		}
	}
	if waits := designWaitsIn(standing.NeedsHuman); len(waits) != 1 || waits[0].WorkItemID != "yoyodyne-item" {
		t.Fatalf("attention line waiting on design = %+v, want the labelled item back as the architect's", waits)
	}
}

// A confirmed document a reviewed run can carry is the harness's, as before,
// and is on neither the operator's line nor in place of the design wait.
func TestAConfirmedDocumentARunCanCarryIsNotAPlacement(t *testing.T) {
	t.Parallel()

	sources := placementSources(t, false)
	conversations := sources.Conversations.(fakeConversations)
	conversations.recorded[0].PendingWrites[0].AwaitsPlacement = nil
	standing := ReadStanding(context.Background(), sources)
	for _, entry := range standing.NeedsHuman {
		if entry.Kind == AttentionDocumentApproval {
			t.Fatalf("a document a run can carry is on the line: %s", entry.What())
		}
	}
	if waits := designWaitsIn(standing.NeedsHuman); len(waits) != 1 {
		t.Fatalf("attention line waiting on design = %+v, want the labelled item still listed", waits)
	}
}
