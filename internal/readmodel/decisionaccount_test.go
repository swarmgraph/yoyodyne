package readmodel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// decisionSources is a document revision, a proposed change to the goals, and
// a concern, each waiting on the operator, over a repository holding the
// document as it stands.
func decisionSources(t *testing.T) (Sources, amendment.Proposal) {
	t.Helper()
	repository := t.TempDir()
	path := filepath.Join(repository, "docs", "product", "goals", "v1-non-goals.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	current := "---\nid: v1-non-goals\nkind: non-goals\n---\n# V1 non-goals\n\nThe dashboard is read-only.\n\nNo hosted control plane.\n"
	if err := os.WriteFile(path, []byte(current), 0o644); err != nil {
		t.Fatal(err)
	}
	goals := goalsProposal(2, 2*time.Hour)
	conversation := runstate.Conversation{
		ConversationID: "chat-1", Agent: "product-manager", Role: domain.RoleProductManager,
		PendingWrites: []runstate.PendingWrite{{
			ID: "document-4.1", Turn: 4, Action: "revise", Artifact: "v1-non-goals", Kind: "non-goals", Title: "V1 non-goals",
			Body:    "# V1 non-goals\n\nThe dashboard may take the operator's own decisions.\n\nNo hosted control plane.\n",
			Reason:  "the operator decided the dashboard may take his decisions",
			Intent:  "fundamental",
			Answers: []string{"yoyodyne-ifd.432.8"},
			Revises: []runstate.DocumentRef{{ID: "v1-non-goals", Path: "docs/product/goals/v1-non-goals.md"}},
		}},
		PendingConcerns: []runstate.PendingConcern{{
			ID: "c5.1", Turn: 5, Kind: "unplaceable", Subject: "A billing page",
			Detail: "no goal names billing", Question: "Should the goals name billing?", Options: []string{"Add a goal", "Leave it out"},
		}},
	}
	return Sources{
		Repository:    repository,
		Now:           func() time.Time { return moment },
		Amendments:    fakeAmendments{records: []amendment.Record{{Proposal: &goals}}},
		Conversations: fakeConversations{recorded: []runstate.Conversation{conversation}},
	}, goals
}

// Every entry on the operator's decision list — a document, a proposed change,
// and a concern — says what changes, why, what approving and declining each
// lead to, and why it is his, on every surface that lists it: the dashboard's
// decisions, the four lines `yoyo status` and the channel print, and the
// summary's "Needs your decision".
func TestEveryDecisionSaysWhatChangesWhyWhatEachChoiceLeadsToAndWhyItIsTheOperators(t *testing.T) {
	t.Parallel()
	sources, goals := decisionSources(t)
	standing := ReadStanding(context.Background(), sources)

	// The dashboard's list carries all three, each with its whole account.
	byKind := map[DecisionKind]OperatorDecision{}
	for _, decision := range standing.OperatorDecisions {
		byKind[decision.Kind] = decision
	}
	if len(byKind) != 3 {
		t.Fatalf("the operator's decisions = %+v (%q), want a document, a proposed change, and a concern", standing.OperatorDecisions, standing.OperatorDecisionsProblem)
	}
	for kind, decision := range byKind {
		account := decision.Account
		if account.Changes == "" || account.Why == "" || account.IfApproved == "" || account.IfDeclined == "" || account.WhyYours == "" || len(account.Missing) != 0 {
			t.Errorf("%s: account = %+v", kind, account)
		}
		// It closes on the reason the ownership registry gave the entry: a
		// waiting document's names the document, the others the closed list's.
		reason := "a change to the fundamental goals, which only the operator decides"
		if kind == DecisionDocument {
			reason = "a change to the non-goals that the Lead Product Manager could not approve as consistent with intent"
		}
		if !strings.Contains(account.WhyYours, ", which makes it "+reason) {
			t.Errorf("%s: why it is his = %q, want the registry's reason %q", kind, account.WhyYours, reason)
		}
	}
	document := byKind[DecisionDocument].Account
	if document.Before != "The dashboard is read-only." || document.After != "The dashboard may take the operator's own decisions." {
		t.Errorf("the document's before and after = %q / %q, want the one paragraph that changes", document.Before, document.After)
	}
	if len(document.Serves) != 1 || document.Serves[0] != "yoyodyne-ifd.432.8" || !strings.Contains(document.WhyYours, "recorded as changing what the goals admit or refuse") {
		t.Errorf("the document's account = %+v", document)
	}
	if byKind[DecisionDocument].Detail == "" {
		t.Error("the document's decision does not carry the whole text the dashboard opens")
	}
	change := byKind[DecisionAmendment].Account
	if !strings.Contains(change.Changes, goals.Change) || change.Why != goals.Why || !strings.Contains(change.IfDeclined, "stays as it is") {
		t.Errorf("the proposed change's account = %+v", change)
	}
	concern := byKind[DecisionConcern]
	if concern.Answer != `yoyo chat --message "answer c5.1 <your answer>"` || !strings.Contains(concern.Account.IfApproved, "1. Add a goal; 2. Leave it out") ||
		!strings.Contains(concern.Account.Changes, "Should the goals name billing?") || !strings.Contains(concern.Detail, "It asks: Should the goals name billing?") {
		t.Errorf("the concern's decision = %+v", concern)
	}

	// The four lines print each account under its entry, and the brief
	// rendering a message carries keeps the head.
	lines := standing.Render()
	summary := Summarize(SummaryInputs{Standing: &standing, Now: moment}).Render(false)
	for _, said := range []string{
		"What changes: replaces the text of v1-non-goals",
		"Before: The dashboard is read-only.",
		"After: The dashboard may take the operator's own decisions.",
		"Why: the operator decided the dashboard may take his decisions (serves yoyodyne-ifd.432.8)",
		"If you approve: it is published",
		"What changes: a change to v1-goals: " + goals.Change,
		"If you decline: v1-goals stays as it is",
		"What changes: whether the " + domain.RoleProductManager.Title() + " goes on to propose: A billing page",
		"If you decline: answer that it should not",
		"Why it is yours: the work serves no goal",
	} {
		if !strings.Contains(lines, said) {
			t.Errorf("the four lines do not say %q:\n%s", said, lines)
		}
		if !strings.Contains(summary, said) {
			t.Errorf("the summary does not say %q:\n%s", said, summary)
		}
	}
	if strings.Contains(standing.RenderBrief(), "What changes:") {
		t.Errorf("the brief rendering carries the accounts it drops with their entries:\n%s", standing.RenderBrief())
	}

	// Each decision is a line of its own in the summary, never a count, and
	// names the decision the dashboard opens whole.
	answer := Summarize(SummaryInputs{Standing: &standing, Now: moment}).Decisions
	keys := map[string]bool{}
	for _, entry := range answer.Entries {
		if entry.Account != nil {
			keys[entry.Decision] = entry.Count == 1
		}
	}
	for _, decision := range standing.OperatorDecisions {
		if !keys[decision.Key()] {
			t.Errorf("the summary has no line of its own opening %s: %+v", decision.Key(), answer.Entries)
		}
	}
}

// A record lacking a part of the account says which part, rather than leaving
// it out silently.
func TestADecisionWhoseRecordLacksAPartSaysWhichPart(t *testing.T) {
	t.Parallel()
	document := documentAccount(DocumentApproval{
		WriteID: "document-4.1", Owner: domain.RoleProductManager, Action: "revise", Document: "brief",
		Body: "new text", ProductIntent: true, BeforeUnread: "the repository was not read here",
	}, true, "a change to the fundamental goals, which only the operator decides")
	missing := strings.Join(document.Missing, "; ")
	for _, said := range []string{"why it was written", "the document as it stands now, so no before is shown (the repository was not read here)"} {
		if !strings.Contains(missing, said) {
			t.Errorf("the document's missing parts = %q, want %q", missing, said)
		}
	}
	if lines := strings.Join(document.Lines(), "\n"); !strings.Contains(lines, "Not in its record: ") || !strings.Contains(lines, "; why it was written") {
		t.Errorf("the lines do not say what is missing: %q", document.Lines())
	}

	change := amendmentAccount(amendment.Proposal{ID: "amendment-1", Artifact: "brief", Role: domain.RoleDeveloper, Owner: domain.RoleProductManager}, "")
	if missing := strings.Join(change.Missing, "; "); !strings.Contains(missing, "what the change is") || !strings.Contains(missing, "why it was proposed") {
		t.Errorf("the proposed change's missing parts = %q", missing)
	}

	concern := concernAccount(ConcernWait{ConcernID: "c5.1", Agent: "product-manager", Role: domain.RoleProductManager, Subject: "A billing page"}, "")
	if missing := strings.Join(concern.Missing, "; "); !strings.Contains(missing, "the question it asks") || !strings.Contains(missing, "reason for asking") {
		t.Errorf("the concern's missing parts = %q", missing)
	}
}

// The before and after are the part of the document that changes, and a
// section revision is read against its own section.
func TestTheBeforeAndAfterAreThePartThatChanges(t *testing.T) {
	t.Parallel()
	before, after := beforeAndAfter("# A\n\none\n\ntwo\n\nthree\n", "# A\n\none\n\n2\n\nthree\n")
	if before != "two" || after != "2" {
		t.Fatalf("beforeAndAfter = %q / %q", before, after)
	}
	if before, after := beforeAndAfter("one\n", "one\n\ntwo\n"); before != "nothing: the revision only adds text" || after != "two" {
		t.Fatalf("an addition = %q / %q", before, after)
	}
	if before, _ := beforeAndAfter("one\n", "\none\n"); before != "the text is the same as the document now" {
		t.Fatalf("no change = %q", before)
	}
	section, ok := sectionText("# Doc\n\n## Goals\n\n- one\n\n### Detail\n\nx\n\n## Non-goals\n\n- none\n", "Goals")
	if !ok || !strings.Contains(section, "- one") || !strings.Contains(section, "### Detail") || strings.Contains(section, "Non-goals") {
		t.Fatalf("sectionText = %q, %t", section, ok)
	}
	if _, unread := readBefore(t.TempDir(), "../outside.md", ""); !strings.Contains(unread, "not a file inside the repository") {
		t.Fatalf("a path outside the repository = %q", unread)
	}
}

// An excerpt past its bound is cut on a character, never inside one, and says
// it was cut.
func TestALongExcerptIsCutOnACharacterAndSaysSo(t *testing.T) {
	t.Parallel()
	said := excerpt(strings.Repeat("é", maxExcerptBytes))
	if !utf8.ValidString(said) || !strings.HasSuffix(said, " …") || len(said) > maxExcerptBytes+len(" …") {
		t.Fatalf("excerpt = %q", said)
	}
}
