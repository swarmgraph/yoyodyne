package readmodel

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func waitingGoals(id string, age time.Duration) runstate.PendingWrite {
	return runstate.PendingWrite{
		ID: id, Turn: 4, WrittenAt: moment.Add(-age), Action: "create", Artifact: "v2-goals", Kind: "goals",
		Title: "V2 goals", Directory: "docs/product", Body: "# Goals", Reason: "the operator asked for the next goals",
	}
}

func waitingDesign(id string, age time.Duration) runstate.PendingWrite {
	return runstate.PendingWrite{
		ID: id, Turn: 7, WrittenAt: moment.Add(-age), Action: "create", Artifact: "intake-design", Kind: "design",
		Title: "Intake", Directory: "docs/designs", Body: "# Intake", Reason: "the work item asks for a design",
	}
}

func documentConversations(writes map[domain.AgentRole][]runstate.PendingWrite) fakeConversations {
	var recorded []runstate.Conversation
	for _, role := range []domain.AgentRole{domain.RoleProductManager, domain.RoleArchitect} {
		if pending, ok := writes[role]; ok {
			recorded = append(recorded, runstate.Conversation{
				ConversationID: "chat-" + string(role), Agent: string(role), Role: role, PendingWrites: pending,
			})
		}
	}
	return fakeConversations{recorded: recorded}
}

func documentEntries(standing Standing) []Attention {
	var found []Attention
	for _, entry := range standing.NeedsHuman {
		if entry.Kind == AttentionDocumentApproval {
			found = append(found, entry)
		}
	}
	return found
}

// A document a role wrote that waits for the operator's approval is on the
// needs-a-human line: what it is, which role wrote it, and since when, with
// the command that decides it. Goals are the operator's, for a change of
// fundamental intent; a design waits on him only because the project's
// approval setting asks for a person, so it stays the architect's, and the
// sentence still says he is who decides it.
func TestADocumentWaitingForApprovalIsOnTheNeedsAHumanLine(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.Conversations = documentConversations(map[domain.AgentRole][]runstate.PendingWrite{
		domain.RoleProductManager: {waitingGoals("document-4.1", time.Hour)},
		domain.RoleArchitect:      {waitingDesign("document-7.1", 3*time.Hour)},
	})
	standing := ReadStanding(context.Background(), sources)
	if standing.NeedsHumanProblem != "" {
		t.Fatalf("needs-a-human problem = %q", standing.NeedsHumanProblem)
	}
	found := documentEntries(standing)
	if len(found) != 2 {
		t.Fatalf("document entries = %#v, want two", found)
	}
	design, goals := found[0], found[1]
	if design.ID != "chat-architect/document-7.1" || goals.ID != "chat-product-manager/document-4.1" {
		t.Fatalf("entries = %q, %q, want the design that has waited longer first", design.ID, goals.ID)
	}
	if goals.Mover != ownership.MoverOperator || goals.Ownership.Reason != ownership.OperatorFundamentalIntent {
		t.Fatalf("goals owner = %+v, want the operator for a change of fundamental intent", goals.Ownership)
	}
	if design.Mover != MoverArchitect || design.Ownership.Reason != "" {
		t.Fatalf("design owner = %+v, want the architect with no reason naming the operator", design.Ownership)
	}
	if goals.Since() != moment.Add(-time.Hour) || design.Since() != moment.Add(-3*time.Hour) {
		t.Fatalf("since = %v and %v, want when each was written", goals.Since(), design.Since())
	}
	for _, want := range []string{"document-4.1", "v2-goals", `"V2 goals"`, domain.RoleProductManager.Title(), "chat-product-manager", "waits for the operator's approval", localMoment(moment.Add(-time.Hour))} {
		if !strings.Contains(goals.What(), want) {
			t.Errorf("goals what = %q, want it to say %q", goals.What(), want)
		}
	}
	if whose := goals.Whose(); !strings.HasPrefix(whose, goals.Mover.Possessive()+" — ") || !strings.Contains(whose, `yoyo chat --message "approve document-4.1"`) || !strings.Contains(whose, `"decline document-4.1 <reason>"`) {
		t.Errorf("goals whose = %q, want his possessive and the commands that decide it", whose)
	}
	if whose := design.Whose(); !strings.HasPrefix(whose, MoverArchitect.Possessive()+" — ") || !strings.Contains(whose, `yoyo agent chat architect --message "approve document-7.1"`) || !strings.Contains(whose, "waits for the operator") {
		t.Errorf("design whose = %q, want the architect's, saying the operator decides it and how", whose)
	}
	if label := goals.Label(); label != "document waiting for approval" {
		t.Errorf("label = %q", label)
	}
	// The terminal prints it, and the operator's own list carries the goals
	// alone.
	if rendered := standing.Render(); !strings.Contains(rendered, "document-4.1") || !strings.Contains(rendered, "document-7.1") {
		t.Errorf("rendered standing names neither document:\n%s", rendered)
	}
	if operator := standing.RenderOperatorWaits(moment); !strings.Contains(operator, "document-4.1") || strings.Contains(operator, "document-7.1") {
		t.Errorf("the operator's waits = %q, want the goals alone", operator)
	}
	// What the dashboard reads carries the record whole and reads back.
	encoded, err := json.Marshal(goals)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, want := range []string{`"kind":"document-approval"`, `"document_approval":`, `"write_id":"document-4.1"`, `"agent":"product-manager"`, `"product_intent":true`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("encoded entry %s does not carry %s", encoded, want)
		}
	}
	var decoded Attention
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.What() != goals.What() || decoded.Mover != goals.Mover {
		t.Fatalf("decoded entry = %#v, want it to read back as written", decoded)
	}
}

// A document leaves the line once the operator decides it, because the
// conversation's record keeps a document only until it is decided: an
// approval hands it to a reviewed run, which is the harness's to land, or
// writes it and drops it from the record; a decline drops it. A document its
// owner held for sending is the owner's until its hold lifts, and is not on
// the line either.
func TestADecidedOrHeldDocumentIsNotOnTheNeedsAHumanLine(t *testing.T) {
	t.Parallel()

	approvedForARun := waitingGoals("document-4.1", time.Hour)
	approvedForARun.Publication = &runstate.DocumentPublication{WriteID: "document-4.1", ConversationID: "chat-product-manager", Owner: domain.RoleProductManager}
	held := waitingDesign("document-7.2", time.Hour)
	held.Hold = &artifact.SendingHold{UntilClosed: "yoyodyne-x", Reason: "the design waits on its spike"}
	for name, writes := range map[string]map[domain.AgentRole][]runstate.PendingWrite{
		"approved and handed to a reviewed run": {domain.RoleProductManager: {approvedForARun}},
		"approved and written, or declined":     {domain.RoleProductManager: nil},
		"held for sending by its owner":         {domain.RoleArchitect: {held}},
	} {
		sources := quietSources()
		sources.Conversations = documentConversations(writes)
		if found := documentEntries(ReadStanding(context.Background(), sources)); len(found) != 0 {
			t.Errorf("%s: document entries = %#v, want none", name, found)
		}
	}
}

// A revision the harness is holding back until another copy of the same
// document finishes publishing waits for nobody's approval: it is confirmed, as
// the Lead Product Manager's where she marked it consistent, once that copy
// lands. So it is neither on the line nor among the operator's decisions,
// however long the copy ahead takes. Once nothing holds it back and it does
// wait for him, it is on both.
func TestARevisionHeldBackBehindAnotherCopyIsNotTheOperatorsToDecide(t *testing.T) {
	t.Parallel()

	rules := runstate.PendingWrite{
		ID: "document-5636.1", Turn: 5636, WrittenAt: moment.Add(-14 * time.Hour), Action: "revise", Artifact: "operating-rules",
		DocumentKind: "rules", Body: "# Rules", Reason: "yoyodyne-q2l: records the operator's rule", Intent: "consistent",
		WaitsBehind: "run run-e8a4c80d is still publishing docs/product/operating-rules.md",
	}
	sources := quietSources()
	sources.Conversations = documentConversations(map[domain.AgentRole][]runstate.PendingWrite{domain.RoleProductManager: {rules}})
	if found := documentEntries(ReadStanding(context.Background(), sources)); len(found) != 0 {
		t.Fatalf("document entries = %#v, want none for a revision the harness holds back", found)
	}
	if decisions, problem := ReadOperatorDecisions(sources); problem != "" || len(decisions) != 0 {
		t.Fatalf("operator decisions = %#v, %q, want none", decisions, problem)
	}

	rules.WaitsBehind = ""
	sources.Conversations = documentConversations(map[domain.AgentRole][]runstate.PendingWrite{domain.RoleProductManager: {rules}})
	found := documentEntries(ReadStanding(context.Background(), sources))
	if len(found) != 1 || found[0].Mover != ownership.MoverOperator {
		t.Fatalf("document entries = %#v, want the revision waiting for the operator", found)
	}
	if decisions, _ := ReadOperatorDecisions(sources); len(decisions) != 1 {
		t.Fatalf("operator decisions = %#v, want the revision", decisions)
	}
}

// A waiting change to one of the product's documents says which document it
// would change and why it waits, rather than calling every one of them a
// change to the fundamental goals; the kind of a revision is the one recorded
// with it, since a revision names none of its own.
func TestAWaitingIntentDocumentNamesItsKindAndWhyItWaits(t *testing.T) {
	t.Parallel()

	for kind, names := range map[string]string{
		"rules":     "a change to the operating rules",
		"goals":     "a change to the goals",
		"non-goals": "a change to the non-goals",
		"brief":     "a change to the brief",
		"":          "a change to a document of the product's intent",
	} {
		revision := runstate.PendingWrite{ID: "document-1.1", Turn: 1, WrittenAt: moment, Action: "revise", Artifact: "some-document", DocumentKind: kind, Body: "# Text", Reason: "reworded"}
		sources := quietSources()
		sources.Conversations = documentConversations(map[domain.AgentRole][]runstate.PendingWrite{domain.RoleProductManager: {revision}})
		found := documentEntries(ReadStanding(context.Background(), sources))
		if len(found) != 1 {
			t.Fatalf("%q: document entries = %#v", kind, found)
		}
		says := found[0].Ownership.ReasonSays()
		if !strings.HasPrefix(says, names) || !strings.Contains(says, "the Lead Product Manager could not approve as consistent with intent, so it waits for the operator to decide") || strings.Contains(says, "fundamental goals") {
			t.Errorf("%q: owner reason = %q, want it to begin %q and say why it waits", kind, says, names)
		}
		// The dashboard reads the same words back.
		encoded, err := json.Marshal(found[0])
		if err != nil {
			t.Fatal(err)
		}
		var decoded Attention
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("%q: Unmarshal() error = %v", kind, err)
		}
		if decoded.Ownership.ReasonSays() != says {
			t.Errorf("%q: decoded reason = %q, want %q", kind, decoded.Ownership.ReasonSays(), says)
		}
	}
}

// A revision that names no kind is the product's intent where the role that
// owns the brief wrote it, and not otherwise; a record from before the moment
// was kept is said as such and sorts after the ones that say when.
func TestARevisionAndAnUndatedDocumentAreSaidPlainly(t *testing.T) {
	t.Parallel()

	revision := runstate.PendingWrite{ID: "document-9.1", Turn: 9, Action: "revise", Artifact: "brief", Body: "# Brief", Reason: "a sharper sentence"}
	dated := waitingDesign("document-2.1", time.Minute)
	sources := quietSources()
	sources.Conversations = documentConversations(map[domain.AgentRole][]runstate.PendingWrite{
		domain.RoleProductManager: {revision},
		domain.RoleArchitect:      {dated},
	})
	found := documentEntries(ReadStanding(context.Background(), sources))
	if len(found) != 2 || found[0].DocumentApproval.WriteID != "document-2.1" {
		t.Fatalf("document entries = %#v, want the dated design first", found)
	}
	undated := found[1]
	if undated.Mover != ownership.MoverOperator || !undated.DocumentApproval.ProductIntent {
		t.Fatalf("revision of the brief = %+v, want the operator's", undated.Ownership)
	}
	if what := undated.What(); !strings.Contains(what, "a revision of brief") || !strings.Contains(what, "since a time its record does not hold") {
		t.Fatalf("what = %q", what)
	}
	if !undated.Since().IsZero() {
		t.Fatalf("since = %v, want none", undated.Since())
	}
}

// Conversations that cannot be read are said on the line rather than read as
// no document waiting.
func TestUnreadableConversationsAreSaidOnTheNeedsAHumanLine(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.Conversations = fakeConversations{fail: errors.New("permission denied")}
	standing := ReadStanding(context.Background(), sources)
	if !strings.Contains(standing.NeedsHumanProblem, "the documents waiting for the operator's approval could not be read: permission denied") {
		t.Fatalf("needs-a-human problem = %q", standing.NeedsHumanProblem)
	}
}

// fakeConfirmations answers which waiting documents the harness confirms by
// itself, by write, and fails for the writes it is told to.
type fakeConfirmations struct {
	confirms map[string]bool
	fail     map[string]bool
}

func (f fakeConfirmations) HarnessConfirms(_ domain.AgentRole, _ string, write runstate.PendingWrite) (bool, error) {
	if f.fail[write.ID] {
		return false, errors.New("the document could not be read")
	}
	return f.confirms[write.ID], nil
}

// A design the harness confirms under its kind's automatic approval setting
// waits on nobody's decision until it is sent, whether its conversation has
// not had its next message yet or an earlier revision of it is still
// publishing. It is shown as the harness's, never as the operator's decision
// nor as a role's decision needing his hand, and it is on none of his lists.
// A change to the goals marked fundamental is still his, and so is a document
// whose answer could not be had.
func TestADocumentTheHarnessConfirmsIsNeverTheOperatorsDecision(t *testing.T) {
	t.Parallel()

	design := waitingDesign("document-7.1", 20*time.Minute)
	publishing := waitingDesign("document-6.1", time.Hour)
	publishing.Publication = &runstate.DocumentPublication{WriteID: "document-6.1", ConversationID: "chat-architect", Owner: domain.RoleArchitect}
	behind := waitingDesign("document-6.2", 30*time.Minute)
	unreadable := waitingDesign("document-8.1", 10*time.Minute)
	unreadable.Artifact = "other-design"
	goals := waitingGoals("document-4.1", time.Hour)
	goals.Intent = string(artifact.IntentFundamental)

	sources := quietSources()
	sources.Conversations = documentConversations(map[domain.AgentRole][]runstate.PendingWrite{
		domain.RoleProductManager: {goals},
		domain.RoleArchitect:      {publishing, behind, design, unreadable},
	})
	sources.DocumentConfirmations = fakeConfirmations{
		confirms: map[string]bool{"document-7.1": true, "document-6.2": true},
		fail:     map[string]bool{"document-8.1": true},
	}
	standing := ReadStanding(context.Background(), sources)
	if standing.NeedsHumanProblem != "" {
		t.Fatalf("needs-a-human problem = %q", standing.NeedsHumanProblem)
	}
	byWrite := map[string]Attention{}
	for _, entry := range documentEntries(standing) {
		byWrite[entry.DocumentApproval.WriteID] = entry
	}
	if len(byWrite) != 4 {
		t.Fatalf("document entries = %#v, want the four not yet sent", byWrite)
	}
	for _, id := range []string{"document-7.1", "document-6.2"} {
		entry := byWrite[id]
		if entry.Mover != ownership.MoverHarness || entry.Ownership.NeedsHand() || entry.Ownership.Reason != "" {
			t.Errorf("%s owner = %+v, want the harness's with no hand and no reason naming the operator", id, entry.Ownership)
		}
		if what := entry.What(); strings.Contains(what, "operator") || !strings.Contains(what, "waits for the harness to confirm it") {
			t.Errorf("%s what = %q, want it to say the harness confirms it and not name the operator", id, what)
		}
		if whose := entry.Whose(); !strings.HasPrefix(whose, ownership.MoverHarness.Possessive()+" — ") || strings.Contains(whose, "approve "+id) || !strings.Contains(whose, "nobody has to approve it") {
			t.Errorf("%s whose = %q, want the harness's, saying nobody approves it and giving no approve command", id, whose)
		}
		if label := entry.Label(); label != "document waiting to be published" {
			t.Errorf("%s label = %q", id, label)
		}
	}
	if entry := byWrite["document-4.1"]; entry.Mover != ownership.MoverOperator || entry.Ownership.Reason != ownership.OperatorFundamentalIntent {
		t.Errorf("goals owner = %+v, want the operator's for a change of fundamental intent", entry.Ownership)
	}
	if entry := byWrite["document-8.1"]; entry.Mover != MoverArchitect || !entry.Ownership.NeedsHand() {
		t.Errorf("unreadable design owner = %+v, want the architect's needing the operator's hand, as before the question was asked", entry.Ownership)
	}
	operator := standing.RenderOperatorWaits(moment)
	if !strings.Contains(operator, "document-4.1") || strings.Contains(operator, "document-7.1") || strings.Contains(operator, "document-6.2") {
		t.Errorf("the operator's waits = %q, want the goals and neither design the harness confirms", operator)
	}
	decisions, problem := ReadOperatorDecisions(sources)
	if problem != "" {
		t.Fatalf("operator decisions problem = %q", problem)
	}
	for _, decision := range decisions {
		if strings.Contains(decision.ID, "document-7.1") || strings.Contains(decision.ID, "document-6.2") {
			t.Errorf("the operator's decisions carry %+v, a document the harness confirms", decision)
		}
	}
	encoded, err := json.Marshal(byWrite["document-7.1"])
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"harness_confirms":true`) {
		t.Errorf("encoded entry %s does not say the harness confirms it", encoded)
	}
}
