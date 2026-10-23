package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// submittedRules is the Lead Product Manager writing the operating rules, with
// the intent mark she gives it.
func submittedRules(intent artifact.Intent) string {
	mark := ""
	if intent != "" {
		mark = fmt.Sprintf(",\"intent\":%q", intent)
	}
	return fmt.Sprintf("Recorded.\n%s\n{\"documents\":[{\"action\":\"create\",\"id\":\"operating-rules\",\"kind\":\"rules\",\"title\":\"Operating rules\",\"directory\":\"docs/product\",\"body\":%q,\"reason\":\"yoyodyne-ifd.433.17 - the standing rules the operator already gave\"%s}]}\n```",
		artifact.WriteFence, "# Operating rules\n\n## Rules\n\n- A work item's notes are append-only.", mark)
}

func rulesConversation(t *testing.T, pipeline Pipeline, provider chat.Backend) chat.Options {
	t.Helper()
	options, _ := publicationConversation(t, pipeline, provider)
	options.Role, options.Agent = domain.RoleProductManager, "product-manager"
	options.DocumentPolicy = artifact.Policy{Brief: domain.ApprovalHuman, Goals: domain.ApprovalHuman, Designs: domain.ApprovalAutomatic, SpecificationsHome: pipeline.Config.Product.Specifications}
	return options
}

// Recording rules the operator already gave is the Lead Product Manager's to
// approve. A rules document she writes marked consistent with intent, naming the
// item that directed it, is not put to the operator although approvals.goals is
// human: it lands through a reviewed run, carrying an approval recorded as hers
// that names her and that item.
func TestARulesDocumentMarkedConsistentIsNotPutToTheOperator(t *testing.T) {
	repository, _, provider, pipeline, runs := automaticFixture(t)
	pipeline.Config.Approvals.Goals = domain.ApprovalHuman
	original := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleProductManager {
			return backend.RunResult{Backend: domain.BackendClaudeCode, SessionID: "author-session", FinalText: submittedRules(artifact.IntentConsistent)}, nil
		}
		return original(request)
	}
	session, err := chat.Open(rulesConversation(t, pipeline, provider))
	if err != nil {
		t.Fatal(err)
	}
	reply, err := session.Send(context.Background(), "Record the operating rules.")
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Writes) != 0 || len(session.Writes()) != 0 {
		t.Fatalf("the rules were put to the operator: %+v", session.Writes())
	}
	recorded, err := runs.Recorded()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("runs = %+v, %v", recorded, err)
	}
	state := recorded[0]
	if state.Status != runstate.StatusSucceeded || state.Document == nil || state.ReviewSessionID == "" {
		t.Fatalf("run = %+v", state)
	}
	if state.Selection == nil || !strings.Contains(state.Selection.Reason, "approved by the Lead Product Manager") {
		t.Fatalf("selection does not say whose approval it carries: %+v", state.Selection)
	}
	// Her mark is why the operator was not asked, so the reviewer is told to
	// test it and to refuse the change where it is wrong.
	reviews := provider.RequestsForRole(domain.RoleReviewer)
	if len(reviews) == 0 {
		t.Fatal("no review was asked for")
	}
	for _, want := range []string{"its last revision is marked consistent with intent", "so test the mark against the change", "adding or removing a goal always is one", "Where the mark is wrong, refuse the change"} {
		if !strings.Contains(reviews[len(reviews)-1].Prompt, want) {
			t.Errorf("the reviewer was not told %q", want)
		}
	}
	content := publicationGit(t, repository, "show", "main:docs/product/operating-rules.md")
	if !strings.Contains(content, "by: product-manager") || strings.Contains(content, "by: operator") || strings.Contains(content, "by: harness") ||
		!strings.Contains(content, "reason: yoyodyne-ifd.433.17 - approved by the Lead Product Manager") || !strings.Contains(content, "intent: consistent") {
		t.Fatalf("the landed rules do not carry the Lead Product Manager's approval:\n%s", content)
	}
	set, err := artifact.StoreFor(repository, pipeline.Config.Product).Load()
	if err != nil {
		t.Fatal(err)
	}
	if rules, found := set.Find("operating-rules"); !found || rules.ApprovalState() != artifact.ApprovalApproved {
		t.Fatalf("rules = %+v, found %v", rules, found)
	}
}

// Where the project does not integrate automatically her approval still goes
// through a reviewed run, left on its branch for the operator to merge as every
// change there is, rather than being put to him as an approval.
func TestTheLeadProductManagersApprovalIsReviewedAndHeldForMergeWhereIntegrationIsNotAutomatic(t *testing.T) {
	repository, _, provider, pipeline, runs := automaticFixture(t)
	pipeline.Config.Approvals.Integration = domain.ApprovalHuman
	original := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleProductManager {
			return backend.RunResult{Backend: domain.BackendClaudeCode, SessionID: "author-session", FinalText: submittedRules(artifact.IntentConsistent)}, nil
		}
		return original(request)
	}
	session, err := chat.Open(rulesConversation(t, pipeline, provider))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Record the operating rules."); err != nil {
		t.Fatal(err)
	}
	if len(session.Writes()) != 0 {
		t.Fatalf("the rules were put to the operator: %+v", session.Writes())
	}
	recorded, err := runs.Recorded()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("runs = %+v, %v", recorded, err)
	}
	state := recorded[0]
	if state.Status != runstate.StatusSucceeded || state.Integration != nil || state.Branch == "" {
		t.Fatalf("run = %+v", state)
	}
	if content := publicationGit(t, repository, "show", state.Branch+":docs/product/operating-rules.md"); !strings.Contains(content, "by: product-manager") {
		t.Fatalf("the branch does not carry her approval:\n%s", content)
	}
}

// A creation that says nothing about intent, or says it is fundamental, is new
// product intent and the operator's, whatever approvals.goals says.
func TestARulesDocumentNotMarkedConsistentWaitsForTheOperator(t *testing.T) {
	for _, intent := range []artifact.Intent{"", artifact.IntentFundamental} {
		t.Run(string(intent), func(t *testing.T) {
			_, _, provider, pipeline, runs := automaticFixture(t)
			pipeline.Config.Approvals.Goals = domain.ApprovalAutomatic
			provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
				return backend.RunResult{Backend: domain.BackendClaudeCode, SessionID: "author-session", FinalText: submittedRules(intent)}, nil
			}
			options := rulesConversation(t, pipeline, provider)
			options.DocumentPolicy.Goals, options.DocumentPolicy.Brief = domain.ApprovalAutomatic, domain.ApprovalAutomatic
			session, err := chat.Open(options)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := session.Send(context.Background(), "Record the operating rules."); err != nil {
				t.Fatal(err)
			}
			if len(session.Writes()) != 1 {
				t.Fatalf("the rules were not put to the operator: %+v", session.Writes())
			}
			if records, err := runs.Recorded(); err != nil || len(records) != 0 {
				t.Fatalf("runs = %v, %v", records, err)
			}
		})
	}
}

// A goals document is the operator's to approve however the Lead Product
// Manager marks it: every goal it states is new. Created marked consistent, it
// waits for him under an automatic approvals.goals, no run is opened for it, and
// she is told why so she can tell him it is his to decide.
func TestAGoalsDocumentCreatedConsistentWaitsForTheOperator(t *testing.T) {
	_, _, provider, pipeline, runs := automaticFixture(t)
	pipeline.Config.Approvals.Goals = domain.ApprovalAutomatic
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{Backend: domain.BackendClaudeCode, SessionID: "author-session", FinalText: fmt.Sprintf("Recorded.\n%s\n{\"documents\":[{\"action\":\"create\",\"id\":\"v2-goals\",\"kind\":\"goals\",\"title\":\"V2 goals\",\"directory\":\"docs/product\",\"body\":%q,\"reason\":\"yoyodyne-ifd.500 - the goals the operator described\",\"intent\":\"consistent\"}]}\n```",
			artifact.WriteFence, "# V2 goals\n\n## Goals\n\n- Ship on Fridays.")}, nil
	}
	options, store := publicationConversation(t, pipeline, provider)
	options.Role, options.Agent = domain.RoleProductManager, "product-manager"
	options.DocumentPolicy = artifact.Policy{Brief: domain.ApprovalAutomatic, Goals: domain.ApprovalAutomatic, Designs: domain.ApprovalAutomatic, SpecificationsHome: pipeline.Config.Product.Specifications}
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Write the v2 goals."); err != nil {
		t.Fatal(err)
	}
	if len(session.Writes()) != 1 {
		t.Fatalf("the goals were not put to the operator: %+v", session.Writes())
	}
	if records, err := runs.Recorded(); err != nil || len(records) != 0 {
		t.Fatalf("runs = %v, %v", records, err)
	}
	saved, err := store.Load(runstate.ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"(v2-goals) is marked consistent with intent and waits for the operator's approval all the same", "it creates a goals document"} {
		if !strings.Contains(saved.PendingTrackerResults, want) {
			t.Errorf("the Lead Product Manager was told %q, want it to say %q", saved.PendingTrackerResults, want)
		}
	}
}

// The reviewer is told to test the mark only where the mark is why the operator
// was not asked: a design is not product intent, and a revision the operator
// approved himself, or one not marked consistent, is his already.
func TestTheReviewerTestsTheConsistentMarkOnlyWhereItStandsInForTheOperator(t *testing.T) {
	marked := artifact.Artifact{Kind: artifact.KindGoals, Revisions: []artifact.Revision{{Action: artifact.ActionAmended, Intent: artifact.IntentConsistent}}}
	approval := func(by artifact.Approver) []artifact.Approval {
		return []artifact.Approval{{Revision: 0, By: by}}
	}
	for name, test := range map[string]struct {
		candidate artifact.ConfirmedDocument
		told      bool
	}{
		"goals approved as hers":      {artifact.ConfirmedDocument{Artifact: withApprovals(marked, approval(artifact.ApproverProductManager))}, true},
		"goals the operator approved": {artifact.ConfirmedDocument{Artifact: withApprovals(marked, approval(artifact.ApproverOperator))}, false},
		"goals marked fundamental":    {artifact.ConfirmedDocument{Artifact: artifact.Artifact{Kind: artifact.KindGoals, Revisions: []artifact.Revision{{Action: artifact.ActionAmended, Intent: artifact.IntentFundamental}}}}, false},
		"a design":                    {artifact.ConfirmedDocument{Artifact: artifact.Artifact{Kind: artifact.KindDesign, Revisions: marked.Revisions}}, false},
	} {
		if told := consistentMarkReview(test.candidate) != ""; told != test.told {
			t.Errorf("%s: told = %v, want %v", name, told, test.told)
		}
	}
}

func withApprovals(recorded artifact.Artifact, approvals []artifact.Approval) artifact.Artifact {
	recorded.Approvals = approvals
	return recorded
}

// A document marked consistent whose reason names no work item waits for the
// operator as before, but the Lead Product Manager is not told to take it to
// him: the gap is in her own record and is hers to put right.
func TestAConsistentMarkShortOfADirectingItemIsNotSentToTheOperator(t *testing.T) {
	_, _, provider, pipeline, runs := automaticFixture(t)
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{Backend: domain.BackendClaudeCode, SessionID: "author-session", FinalText: fmt.Sprintf("Recorded.\n%s\n{\"documents\":[{\"action\":\"create\",\"id\":\"operating-rules\",\"kind\":\"rules\",\"title\":\"Operating rules\",\"directory\":\"docs/product\",\"body\":%q,\"reason\":\"the standing rules the operator already gave\",\"intent\":\"consistent\"}]}\n```",
			artifact.WriteFence, "# Operating rules\n\n## Rules\n\n- A work item's notes are append-only.")}, nil
	}
	options, store := publicationConversation(t, pipeline, provider)
	options.Role, options.Agent = domain.RoleProductManager, "product-manager"
	options.DocumentPolicy = artifact.Policy{Brief: domain.ApprovalHuman, Goals: domain.ApprovalHuman, Designs: domain.ApprovalAutomatic, SpecificationsHome: pipeline.Config.Product.Specifications}
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Record the operating rules."); err != nil {
		t.Fatal(err)
	}
	if len(session.Writes()) != 1 {
		t.Fatalf("waiting = %+v", session.Writes())
	}
	if records, err := runs.Recorded(); err != nil || len(records) != 0 {
		t.Fatalf("runs = %v, %v", records, err)
	}
	saved, err := store.Load(runstate.ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(saved.PendingTrackerResults, "Tell the operator") || strings.Contains(saved.PendingTrackerResults, "all the same") {
		t.Fatalf("the Lead Product Manager was told to take her own gap to the operator: %q", saved.PendingTrackerResults)
	}
}
