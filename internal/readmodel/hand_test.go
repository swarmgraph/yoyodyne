package readmodel

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// handEntries is one of each record the read model derives a role's decision
// needing the operator's hand from, beside one entry that is his to decide: a
// report the Lead Product Manager handled as her configuration decision, the
// development manager's escalation of a cap crossing past her own, and a design
// the architect wrote from a conversation.
func handEntries(t *testing.T) (operator Attention, hands []Attention) {
	t.Helper()
	stopped := moment.Add(-24 * time.Hour)
	reported := report.Report{ID: "report-0123456789abcdef0123456789abcdef", Role: domain.RoleDeveloper, RunID: "run-a", Severity: report.SeverityWarning, Message: "the race check is too slow"}
	handled := report.Handling{
		ReportID: reported.ID, Role: domain.RoleProductManager, RunID: "chat-pm", RecordedAt: stopped,
		Reason: "set checks.race to the narrowed line in .yoyodyne/config.yaml", Hand: ownership.HandOperationalConfiguration,
	}
	configured := OperatorActions([]report.Report{reported}, []report.Handling{handled})
	if len(configured) != 1 || configured[0].Hand != ownership.HandOperationalConfiguration || configured[0].DecidedBy != domain.RoleProductManager {
		t.Fatalf("OperatorActions() = %#v, want the handling as the Lead Product Manager's decision needing a hand", configured)
	}

	run := heldRun("run-451a", "yoyodyne-ifd.451", stopped)
	decisions := recordedDecisions{"yoyodyne-ifd.451": {Decisions: []runstate.TriageDecision{{
		Decision: runstate.TriageDecisionEscalate, RunID: run.RunID, Reason: "cross the review-rounds cap to 8: the last round found the cause",
		DecidedBy: "development-manager", Conversation: "chat-dm", Turn: 3, DecidedAt: stopped.Add(time.Hour),
		Hand: ownership.HandCapCrossing,
	}}}}
	standing, ended, problem := Escalations([]runstate.State{run}, decisions, nil, nil)
	if problem != "" || len(ended) != 0 || len(standing) != 1 || standing[0].Hand != ownership.HandCapCrossing {
		t.Fatalf("Escalations() = %#v, %#v, %q, want the cap crossing standing and not sent back for giving no reason", standing, ended, problem)
	}

	design := documentApprovalAttention(DocumentApproval{
		WriteID: "document-7.1", ConversationID: "chat-arch", Agent: "architect", Owner: domain.RoleArchitect,
		Action: "revise", Document: "ownership-and-the-operator", Reason: "the hand field", Turn: 7, WrittenAt: stopped,
	})
	hands = []Attention{operatorActionAttention(configured[0]), operatorActionAttention(standing[0]), design}
	operator = operatorHoldAttention(runstate.OperatorHold{SchemaVersion: runstate.OperatorHoldSchemaVersion, HeldAt: stopped})
	return operator, hands
}

// A role's decision that needs the operator's hand is printed under its own
// head on the needs-a-human line, naming the role that decided, what to type,
// and the item that ends the need, and never under "Yours to decide".
func TestAnEntryCarryingAHandNeverRendersUnderYoursToDecide(t *testing.T) {
	t.Parallel()
	operator, hands := handEntries(t)
	wantOwners := []Mover{MoverProductManager, MoverDevelopmentManager, MoverArchitect}
	for i, entry := range hands {
		if entry.Mover != wantOwners[i] || entry.Mover.IsOperator() || !entry.Ownership.NeedsHand() {
			t.Fatalf("entry %s %s resolves to %s with %+v, want the deciding role's with a hand", entry.Kind, entry.ID, entry.Mover, entry.Ownership)
		}
	}
	rendered := Standing{NeedsHuman: append([]Attention{hands[0], operator}, hands[1:]...)}.renderNeedsHuman()

	yours := strings.Index(rendered, "  "+YoursToDecide+" (1):\n")
	hand := strings.Index(rendered, "  "+NeedsYourHand+" (3)")
	if !strings.HasPrefix(rendered, "Needs a human (1, and 3 decisions roles made that need your hand, counted as the harness's missing capability):\n") || yours < 0 || hand < yours {
		t.Fatalf("rendered:\n%s\nwant the operator's count alone on the line, then the two heads in order", rendered)
	}
	underYours := rendered[yours:hand]
	if !strings.Contains(underYours, "the operator's") || strings.Count(underYours, "\n    ") != 1 {
		t.Fatalf("under %q:\n%s\nwant only the operator's own entry", YoursToDecide, underYours)
	}
	underHand := rendered[hand:]
	for i, entry := range hands {
		if strings.Contains(underYours, entry.What()) {
			t.Fatalf("rendered:\n%s\nshows %s under %q", rendered, entry.ID, YoursToDecide)
		}
		line := "    " + entry.What() + " — " + entry.Ownership.Hand.Says(entry.Mover) + "\n"
		if !strings.Contains(underHand, line) {
			t.Fatalf("rendered:\n%s\nmissing %q under the hand head", rendered, line)
		}
		if !strings.HasPrefix(entry.Ownership.Hand.Says(entry.Mover), wantOwners[i].Possessive()+" decision") {
			t.Fatalf("hand %q does not name the deciding role", entry.Ownership.Hand.Says(entry.Mover))
		}
	}
	for _, want := range []string{"yoyodyne-ifd.434.8", "yoyodyne-ifd.433.21", "yoyo triage override", "yoyo agent chat architect --message \"approve document-7.1\""} {
		if !strings.Contains(underHand, want) {
			t.Fatalf("rendered:\n%s\nthe hand head does not carry %q", rendered, want)
		}
	}
	// Each is printed once: not again under its role's head.
	for _, head := range []string{"Waiting on the Lead Product Manager", "Waiting on the development manager", "Waiting on the architect"} {
		if strings.Contains(rendered, head) {
			t.Fatalf("rendered:\n%s\nprints a hand entry again under %q", rendered, head)
		}
	}
}

// Hand entries are counted as the harness's missing capability, not in the
// operator's count: with nothing of his own, the line's count is nothing, and
// the brief rendering the channel carries keeps both counts in the head.
func TestHandEntriesAreNotCountedAsTheOperators(t *testing.T) {
	t.Parallel()
	_, hands := handEntries(t)
	standing := Standing{NeedsHuman: hands}
	rendered := standing.renderNeedsHuman()
	if !strings.HasPrefix(rendered, "Needs a human (0, and 3 decisions roles made that need your hand, counted as the harness's missing capability):\n  "+YoursToDecide+": nothing\n") {
		t.Fatalf("rendered:\n%s\nwant nothing of the operator's counted, and the hands counted apart", rendered)
	}
	brief := brief(rendered)
	if brief != "Needs a human (0, and 3 decisions roles made that need your hand, counted as the harness's missing capability)\n" {
		t.Fatalf("brief = %q, want the head with both counts and no entries", brief)
	}
	for _, entry := range hands {
		if entry.Mover.IsOperator() {
			t.Fatalf("%s is counted as the operator's", entry.ID)
		}
	}
}

// The JSON carries the hand inside the registry's answer and its sentence
// beside it, and reads both back held to the registry.
func TestTheHandTravelsInTheEntrysJSON(t *testing.T) {
	t.Parallel()
	_, hands := handEntries(t)
	for _, entry := range hands {
		encoded, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			Ownership ownership.Answer `json:"ownership"`
			HandSays  string           `json:"hand_says"`
		}
		if err := json.Unmarshal(encoded, &wire); err != nil {
			t.Fatal(err)
		}
		if wire.Ownership.Hand == nil || wire.HandSays != entry.Ownership.Hand.Says(entry.Mover) {
			t.Fatalf("JSON %s does not carry the hand and its sentence", encoded)
		}
		var decoded Attention
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("reading %s back: %v", encoded, err)
		}
		forged := strings.Replace(string(encoded), `"hand_says":"`, `"hand_says":"forged `, 1)
		if err := json.Unmarshal([]byte(forged), &decoded); err == nil {
			t.Fatalf("a hand sentence disagreeing with its answer was read back: %s", forged)
		}
	}
}

// The item that ends the need is named as every other item on the line is:
// with its priority, labels, and title, on the terminal and in the JSON the
// dashboard shows, and never as a bare identifier.
func TestTheItemThatEndsTheNeedIsNamedWithItsTitle(t *testing.T) {
	t.Parallel()
	_, hands := handEntries(t)
	titles := NewWorkItemTitles([]beads.WorkItem{
		{ID: "yoyodyne-ifd.433.21", Title: "Confirm a conversation's document as its kind's approval setting says", Priority: 1},
		{ID: "yoyodyne-ifd.434.8", Title: "Apply recommended configuration through a run", Priority: 2},
	})
	for i := range hands {
		hands[i].titles = titles
	}
	rendered := Standing{NeedsHuman: hands}.renderNeedsHuman()
	for _, want := range []string{
		"yoyodyne-ifd.433.21 (P1: Confirm a conversation's document as its kind's approval setting says) lands",
		"yoyodyne-ifd.434.8 (P2: Apply recommended configuration through a run) lands",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered:\n%s\nwant the item that ends the need named as %q", rendered, want)
		}
	}
	design := hands[2]
	encoded, err := json.Marshal(design)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		SaidHand string `json:"said_hand"`
		HandEnds string `json:"hand_ends"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(wire.SaidHand, "Confirm a conversation's document") || wire.HandEnds != "(P1) Confirm a conversation's document as its kind's approval setting says (yoyodyne-ifd.433.21)" {
		t.Fatalf("JSON %s does not carry the ending item with its title for the dashboard", encoded)
	}
	var decoded Attention
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("reading %s back: %v", encoded, err)
	}
}
