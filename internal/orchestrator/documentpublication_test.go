package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/terms"
)

func publicationConversation(t *testing.T, pipeline Pipeline, provider chat.Backend) (chat.Options, *runstate.ConversationStore) {
	t.Helper()
	store, err := runstate.NewConversationStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	options := chat.Options{Role: domain.RoleArchitect, Agent: "architect", Backend: provider, Store: store, Model: "opus", Provider: domain.BackendClaudeCode, AccountAlias: config.DefaultAccountAlias, Repository: pipeline.Repository, ProductID: "yoyodyne", RepositoryID: "yoyodyne", Briefing: chat.Briefing{Text: "# Product context\n", GatheredAt: time.Now()}, Documents: artifact.StoreFor(pipeline.Repository, pipeline.Config.Product), DocumentPolicy: artifact.Policy{Brief: domain.ApprovalHuman, Goals: domain.ApprovalHuman, Designs: domain.ApprovalAutomatic}, DocumentPublisher: pipeline}
	return options, store
}

func submittedDesign(id, body string) string {
	return fmt.Sprintf("Written.\n%s\n{\"documents\":[{\"action\":\"create\",\"id\":%q,\"kind\":\"design\",\"title\":\"Recovery design\",\"directory\":\"docs/designs\",\"body\":%q,\"reason\":\"drafted by the architect\"}]}\n```", artifact.WriteFence, id, body)
}

func TestAutomaticDocumentFromConversationLandsThroughIndependentReview(t *testing.T) {
	t.Parallel()

	repository, _, provider, pipeline, runs := automaticFixture(t)
	original := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleArchitect {
			return backend.RunResult{Backend: domain.BackendClaudeCode, SessionID: "author-session", FinalText: submittedDesign("recovery-design", "# Recovery\n\nThe owner's exact words.")}, nil
		}
		return original(request)
	}
	options, _ := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := session.Send(context.Background(), "Write the design.")
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Writes) != 0 || len(session.Writes()) != 0 {
		t.Fatal("automatic document was put to the operator")
	}
	recorded, err := runs.Recorded()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("runs = %+v, %v", recorded, err)
	}
	state := recorded[0]
	if state.Status != runstate.StatusSucceeded || state.Document == nil || state.ReviewSessionID == "" || state.ReviewSessionID == state.Document.AuthorSession || state.ProviderSessionID != "" {
		t.Fatalf("run = %+v", state)
	}
	if len(provider.RequestsForRole(domain.RoleDeveloper)) != 0 || len(provider.RequestsForRole(domain.RoleReviewer)) != 1 {
		t.Fatal("publication did not use exactly an independent review")
	}
	content := publicationGit(t, repository, "show", "main:docs/designs/recovery-design.md")
	if content != strings.TrimSpace(state.Document.Candidate.Content) {
		t.Fatalf("published different bytes: %s", content)
	}
	if !strings.Contains(content, "by: harness") || !strings.Contains(content, "policy: approvals.designs") || !strings.Contains(content, "by: architect") {
		t.Fatal("confirmation provenance missing")
	}
	if state.Selection == nil || state.Selection.By != runstate.SelectedByConversation || !strings.Contains(state.Selection.Reason, state.Document.ConversationID) {
		t.Fatalf("selection does not name the owning conversation: %+v", state.Selection)
	}
	if status := publicationGit(t, repository, "status", "--porcelain"); status != "" {
		t.Fatalf("primary checkout left dirty: %s", status)
	}
}

func TestAlreadyWaitingAutomaticDocumentsKeepTheirStoredIdentityAndContent(t *testing.T) {
	t.Parallel()

	for _, writeID := range []string{"document-45.1", "document-54.1", "document-135.1", "document-147.1"} {
		t.Run(writeID, func(t *testing.T) {
			repository, _, provider, pipeline, runs := automaticFixture(t)
			options, store := publicationConversation(t, pipeline, provider)
			session, err := chat.Open(options)
			if err != nil {
				t.Fatal(err)
			}
			identity := runstate.ConversationIdentity{Role: domain.RoleArchitect, Agent: "architect"}
			prior, err := store.Load(identity)
			if err != nil {
				t.Fatal(err)
			}
			var turn int
			fmt.Sscanf(writeID, "document-%d.1", &turn)
			prior.PendingWrites = []runstate.PendingWrite{{ID: writeID, Turn: turn, Action: "create", Artifact: "waiting-design", Kind: "design", Title: "Saved design", Directory: "docs/designs", Body: "# Saved\n\nWritten before this build.", Reason: "the architect's saved decision"}}
			if err := store.Save(prior); err != nil {
				t.Fatal(err)
			}
			session, err = chat.Open(options)
			if err != nil {
				t.Fatal(err)
			}
			if err := session.PublishDocuments(context.Background()); err != nil {
				t.Fatal(err)
			}
			records, err := runs.Recorded()
			if err != nil || len(records) != 1 {
				t.Fatalf("records: %v, %v", records, err)
			}
			if records[0].Status != runstate.StatusSucceeded || records[0].Integration == nil {
				t.Fatalf("waiting document did not land: %+v", records[0])
			}
			doc := records[0].Document
			if publicationGit(t, repository, "show", "main:docs/designs/waiting-design.md") != strings.TrimSpace(doc.Candidate.Content) {
				t.Fatal("waiting document content did not reach target")
			}
			if doc.WriteID != writeID || doc.Turn != turn || !strings.Contains(doc.Candidate.Content, prior.PendingWrites[0].Body) {
				t.Fatalf("stored document changed: %+v", doc)
			}
			if len(provider.RequestsForRole(domain.RoleArchitect)) != 0 || len(provider.RequestsForRole(domain.RoleDeveloper)) != 0 {
				t.Fatal("author or developer asked to write it again")
			}
			if status := publicationGit(t, repository, "status", "--porcelain"); status != "" {
				t.Fatal(status)
			}
		})
	}
}

// A document saved by a build older than the document checks was never held to
// them when it was written. It is held to them when the harness confirms it, so
// a coined word in it goes back to its owner instead of failing the run that
// would have landed it.
func TestAWaitingDocumentTheDocumentChecksRefuseGoesBackToItsOwnerWithNoRun(t *testing.T) {
	t.Parallel()

	repository, _, provider, pipeline, runs := automaticFixture(t)
	register := "# Terms\n\n" + terms.RegisterHeading + "\n\n| Term | In plain words | Where it is used |\n| --- | --- | --- |\n"
	if err := os.WriteFile(filepath.Join(repository, filepath.FromSlash(terms.RegisterPath)), []byte(register), 0o600); err != nil {
		t.Fatal(err)
	}
	publicationGit(t, repository, "add", ".")
	publicationGit(t, repository, "commit", "-m", "register")
	options, store := publicationConversation(t, pipeline, provider)
	identity := runstate.ConversationIdentity{Role: domain.RoleArchitect, Agent: "architect"}
	if _, err := chat.Open(options); err != nil {
		t.Fatal(err)
	}
	prior, err := store.Load(identity)
	if err != nil {
		t.Fatal(err)
	}
	prior.PendingWrites = []runstate.PendingWrite{{ID: "document-773.1", Turn: 773, Action: "create", Artifact: "waiting-design", Kind: "design", Title: "Saved design", Directory: "docs/designs", Body: "# Saved\n\nWritten before this build.\n\nThe run is wedged.", Reason: "the architect's saved decision"}}
	if err := store.Save(prior); err != nil {
		t.Fatal(err)
	}
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.PublishDocuments(context.Background()); err != nil {
		t.Fatal(err)
	}
	if records, err := runs.Recorded(); err != nil || len(records) != 0 {
		t.Fatalf("runs = %+v, %v, want none opened for a document the checks refuse", records, err)
	}
	saved, err := store.Load(identity)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.PendingWrites) != 0 || len(session.Writes()) != 0 {
		t.Fatalf("waiting = %+v, want the refused document settled", saved.PendingWrites)
	}
	for _, want := range []string{"document-773.1 (waiting-design) was not confirmed", `line 5, coined words: "wedged"`} {
		if !strings.Contains(saved.PendingTrackerResults, want) {
			t.Errorf("the architect was told %q, want it to say %q", saved.PendingTrackerResults, want)
		}
	}
	if _, err := os.Stat(filepath.Join(repository, "docs", "designs", "waiting-design.md")); !os.IsNotExist(err) {
		t.Fatalf("a refused document reached the checkout: %v", err)
	}
}

func TestNonautomaticDocumentStillWaitsForOperatorConfirmation(t *testing.T) {
	t.Parallel()

	_, _, provider, pipeline, runs := automaticFixture(t)
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{SessionID: "author", FinalText: submittedDesign("human-design", "# Waiting")}, nil
	}
	options, _ := publicationConversation(t, pipeline, provider)
	options.DocumentPolicy.Designs = domain.ApprovalHuman
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := session.Send(context.Background(), "Write it.")
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Writes) != 1 || len(session.Writes()) != 1 {
		t.Fatal("human confirmation skipped")
	}
	records, err := runs.Recorded()
	if err != nil || len(records) != 0 {
		t.Fatalf("runs = %v, %v", records, err)
	}
}

// A document the operator approves lands the way a confirmed one does: through
// a run with its own checks and an independent review, with the approval in
// the file, and never as an uncommitted change in the primary checkout.
func TestOperatorApprovedDocumentLandsThroughReviewedRunAndLeavesCheckoutClean(t *testing.T) {
	t.Parallel()

	repository, _, provider, pipeline, runs := automaticFixture(t)
	original := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleArchitect {
			return backend.RunResult{Backend: domain.BackendClaudeCode, SessionID: "author-session", FinalText: submittedDesign("approved-design", "# Approved\n\nThe owner's exact words.")}, nil
		}
		return original(request)
	}
	options, _ := publicationConversation(t, pipeline, provider)
	options.DocumentPolicy.Designs = domain.ApprovalHuman
	pipeline.Config.Approvals.Designs = domain.ApprovalHuman
	options.DocumentPublisher = pipeline
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Write the design."); err != nil {
		t.Fatal(err)
	}
	waiting := session.Writes()
	if len(waiting) != 1 {
		t.Fatalf("document was not put to the operator: %+v", waiting)
	}
	outcomes, decided, err := session.DecideWrites("approve " + waiting[0].ID)
	if err != nil || !decided || len(outcomes) != 1 {
		t.Fatalf("DecideWrites = %+v, %v, %v", outcomes, decided, err)
	}
	outcome := outcomes[0]
	if outcome.Publishing == "" || outcome.PendingCommit != "" || !outcome.Approval || outcome.Path != "docs/designs/approved-design.md" {
		t.Fatalf("approval outcome = %+v", outcome)
	}
	rendered := outcome.Render()
	if strings.Contains(rendered, "commit it") || strings.Contains(rendered, "uncommitted") || !strings.Contains(rendered, "nothing is yours to commit") {
		t.Fatalf("approval asks the operator to commit, or does not say it need not:\n%s", rendered)
	}
	// Nothing reaches the checkout at the moment of approval.
	if status := publicationGit(t, repository, "status", "--porcelain"); status != "" {
		t.Fatalf("approval wrote into the primary checkout: %s", status)
	}
	if len(session.Writes()) != 0 {
		t.Fatal("an approved document is still put to the operator")
	}
	told, err := session.PublishApproved(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(told) == "" {
		t.Fatal("the operator was not told what the approved document's run came to")
	}
	recorded, err := runs.Recorded()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("runs = %+v, %v", recorded, err)
	}
	state := recorded[0]
	if state.Status != runstate.StatusSucceeded || state.Document == nil || state.Integration == nil || state.ReviewSessionID == "" || state.ReviewSessionID == state.Document.AuthorSession {
		t.Fatalf("run = %+v", state)
	}
	if !strings.Contains(outcome.Publishing, state.RunID) {
		t.Fatalf("approval outcome does not name the run %s: %s", state.RunID, outcome.Publishing)
	}
	if len(provider.RequestsForRole(domain.RoleDeveloper)) != 0 || len(provider.RequestsForRole(domain.RoleReviewer)) != 1 {
		t.Fatal("publication did not use exactly an independent review")
	}
	if state.Selection == nil || !strings.Contains(state.Selection.Reason, "approved by the operator") {
		t.Fatalf("selection does not say the operator approved it: %+v", state.Selection)
	}
	content := publicationGit(t, repository, "show", "main:docs/designs/approved-design.md")
	if content != strings.TrimSpace(state.Document.Candidate.Content) {
		t.Fatalf("published different bytes: %s", content)
	}
	if !strings.Contains(content, "by: operator") || strings.Contains(content, "by: harness") || !strings.Contains(content, waiting[0].ID) {
		t.Fatalf("the operator's approval is not what the published document records:\n%s", content)
	}
	if status := publicationGit(t, repository, "status", "--porcelain"); status != "" {
		t.Fatalf("primary checkout left dirty: %s", status)
	}
}

// Where the project does not integrate automatically, a document the operator
// approves still goes through a run with its own checks and an independent
// review, and is left on its branch for the operator to merge, as every change
// there is. Nothing is written into the primary checkout.
func TestOperatorApprovedDocumentIsReviewedAndHeldForMergeWhereIntegrationIsNotAutomatic(t *testing.T) {
	t.Parallel()

	repository, _, provider, pipeline, runs := automaticFixture(t)
	original := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleArchitect {
			return backend.RunResult{Backend: domain.BackendClaudeCode, SessionID: "author", FinalText: submittedDesign("manual-design", "# Manual")}, nil
		}
		return original(request)
	}
	pipeline.Config.Approvals.Integration = domain.ApprovalHuman
	pipeline.Config.Approvals.Designs = domain.ApprovalHuman
	options, _ := publicationConversation(t, pipeline, provider)
	options.DocumentPolicy.Designs = domain.ApprovalHuman
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Write it."); err != nil {
		t.Fatal(err)
	}
	waiting := session.Writes()
	if len(waiting) != 1 {
		t.Fatalf("waiting = %+v", waiting)
	}
	outcomes, _, err := session.DecideWrites("approve " + waiting[0].ID)
	if err != nil || len(outcomes) != 1 || outcomes[0].Publishing == "" || outcomes[0].PendingCommit != "" {
		t.Fatalf("outcomes = %+v, %v", outcomes, err)
	}
	told, err := session.PublishApproved(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := runs.Recorded()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("runs = %+v, %v", recorded, err)
	}
	state := recorded[0]
	if state.Status != runstate.StatusSucceeded || state.Integration != nil || state.ReviewSessionID == "" || state.Branch == "" || state.WorkflowDivergence != "" {
		t.Fatalf("run = %+v", state)
	}
	if len(provider.RequestsForRole(domain.RoleDeveloper)) != 0 || len(provider.RequestsForRole(domain.RoleReviewer)) != 1 {
		t.Fatal("publication did not use exactly an independent review")
	}
	if !strings.Contains(told, "for the operator to merge") || !strings.Contains(told, state.Branch) {
		t.Fatalf("the operator was not told the document is waiting for their merge: %q", told)
	}
	if status := publicationGit(t, repository, "status", "--porcelain", "--untracked-files=all"); status != "" {
		t.Fatalf("approval left the primary checkout dirty: %s", status)
	}
	content := publicationGit(t, repository, "show", state.Branch+":docs/designs/manual-design.md")
	if !strings.Contains(content, "by: operator") {
		t.Fatalf("the branch does not carry the operator's approval:\n%s", content)
	}
	if len(session.Writes()) != 0 {
		t.Fatal("the document was put to the operator again")
	}
}

// Where no independent reviewer is configured no reviewed run can be made, so
// the approved document is written into the working tree as before.
func TestOperatorApprovedDocumentIsWrittenWhereNoReviewerIsConfigured(t *testing.T) {
	t.Parallel()

	repository, _, provider, pipeline, runs := automaticFixture(t)
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{SessionID: "author", FinalText: submittedDesign("manual-design", "# Manual")}, nil
	}
	pipeline.Config.Approvals.Integration = domain.ApprovalHuman
	pipeline.Reviewer = nil
	options, _ := publicationConversation(t, pipeline, provider)
	options.DocumentPolicy.Designs = domain.ApprovalHuman
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Write it."); err != nil {
		t.Fatal(err)
	}
	waiting := session.Writes()
	if len(waiting) != 1 {
		t.Fatalf("waiting = %+v", waiting)
	}
	outcomes, _, err := session.DecideWrites("approve " + waiting[0].ID)
	if err != nil || len(outcomes) != 1 || outcomes[0].Publishing != "" || outcomes[0].PendingCommit == "" {
		t.Fatalf("outcomes = %+v, %v", outcomes, err)
	}
	if status := publicationGit(t, repository, "status", "--porcelain", "--untracked-files=all"); !strings.Contains(status, "manual-design.md") {
		t.Fatalf("status = %q", status)
	}
	if records, err := runs.Recorded(); err != nil || len(records) != 0 {
		t.Fatalf("runs = %v, %v", records, err)
	}
}

// An approval given with `yoyo artifact approve` lands through a reviewed run
// that changes only that document, and never as an uncommitted change in the
// primary checkout.
func TestStandingApprovalLandsThroughReviewedRunAndLeavesCheckoutClean(t *testing.T) {
	t.Parallel()

	repository, _, provider, pipeline, runs := automaticFixture(t)
	store := artifact.StoreFor(repository, pipeline.Config.Product)
	if _, err := store.Create(domain.RoleArchitect, artifact.Draft{ID: "standing-design", Kind: artifact.KindDesign, Title: "Standing design", Directory: "docs/designs", Body: "# Standing\n\nThe owner's words.", Reason: "written by the architect"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	publicationGit(t, repository, "add", ".")
	publicationGit(t, repository, "commit", "-m", "add a design")
	before := publicationGit(t, repository, "show", "main:docs/designs/standing-design.md")

	publication, delivery, err := pipeline.PublishStandingApproval(context.Background(), "standing-design", "approved by the operator on the command line", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !publication.StandingApproval() || !delivery.Settled || !delivery.Landed {
		t.Fatalf("publication = %+v, delivery = %+v", publication, delivery)
	}
	recorded, err := runs.Recorded()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("runs = %+v, %v", recorded, err)
	}
	state := recorded[0]
	if state.Status != runstate.StatusSucceeded || state.Integration == nil || state.ReviewSessionID == "" {
		t.Fatalf("run = %+v", state)
	}
	if len(provider.RequestsForRole(domain.RoleDeveloper)) != 0 || len(provider.RequestsForRole(domain.RoleReviewer)) != 1 {
		t.Fatal("publication did not use exactly an independent review")
	}
	after := publicationGit(t, repository, "show", "main:docs/designs/standing-design.md")
	if !strings.Contains(after, "by: operator") || !strings.Contains(after, "approved by the operator on the command line") || strings.Contains(before, "by: operator") {
		t.Fatalf("the operator's approval did not land:\n%s", after)
	}
	if !strings.Contains(after, "The owner's words.") {
		t.Fatalf("the document's text changed:\n%s", after)
	}
	if status := publicationGit(t, repository, "status", "--porcelain", "--untracked-files=all"); status != "" {
		t.Fatalf("primary checkout left dirty: %s", status)
	}
	// Approving it again as it now stands is refused, because it already is.
	if _, _, err := pipeline.PublishStandingApproval(context.Background(), "standing-design", "again", time.Now()); err == nil {
		t.Fatal("a document already approved as it stands was approved again")
	}
}

type interruptedPublication struct{ chat.DocumentPublisher }

func (p interruptedPublication) PublishDocument(context.Context, runstate.DocumentPublication) (runstate.DocumentDelivery, error) {
	return runstate.DocumentDelivery{}, errors.New("process ended after confirmation")
}

func TestDocumentConfirmationSurvivesRestartAndNeverLandsTwice(t *testing.T) {
	t.Parallel()

	_, _, provider, pipeline, runs := automaticFixture(t)
	original := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleArchitect {
			if len(provider.RequestsForRole(domain.RoleArchitect)) > 1 {
				return backend.RunResult{SessionID: "author", FinalText: "Noted."}, nil
			}
			return backend.RunResult{SessionID: "author", FinalText: submittedDesign("durable-design", "# Durable")}, nil
		}
		return original(request)
	}
	options, store := publicationConversation(t, pipeline, provider)
	options.DocumentPublisher = interruptedPublication{pipeline}
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	// A publication that could not finish never stops the operator's message:
	// the confirmation is kept and the owning role is told what holds it.
	if _, err := session.Send(context.Background(), "Write it."); err != nil {
		t.Fatalf("publication failure stopped the message: %v", err)
	}
	if _, err := session.Send(context.Background(), "And carry on."); err != nil {
		t.Fatalf("conversation wedged after a publication failure: %v", err)
	}
	if got := len(provider.RequestsForRole(domain.RoleArchitect)); got != 2 {
		t.Fatalf("architect turns = %d, want both messages delivered", got)
	}
	saved, err := store.Load(runstate.ConversationIdentity{Role: domain.RoleArchitect, Agent: "architect"})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.PendingWrites) != 1 || saved.PendingWrites[0].Publication == nil {
		t.Fatal("confirmation lost")
	}
	if strings.Count(provider.RequestsForRole(domain.RoleArchitect)[1].Prompt+saved.PendingTrackerResults, "process ended after confirmation") != 1 {
		t.Fatal("the owning role was not told once what holds its publication")
	}
	doc := *saved.PendingWrites[0].Publication
	options.DocumentPublisher = pipeline
	session, err = chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.PublishDocuments(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := pipeline.PublishDocument(context.Background(), doc)
	if err != nil || !result.Landed {
		t.Fatalf("repeated handoff = %+v, %v", result, err)
	}
	records, err := runs.Recorded()
	if err != nil || len(records) != 1 {
		t.Fatalf("runs = %v, %v", records, err)
	}
	if len(provider.RequestsForRole(domain.RoleReviewer)) != 1 {
		t.Fatal("duplicate handoff reviewed or landed again")
	}
}

func TestHumanIntegrationKeepsOperatorConfirmationAndTheConversationWorking(t *testing.T) {
	t.Parallel()

	_, _, provider, pipeline, runs := automaticFixture(t)
	pipeline.Config.Approvals.Integration = domain.ApprovalHuman
	turn := 0
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		turn++
		if turn == 1 {
			return backend.RunResult{SessionID: "author", FinalText: submittedDesign("human-integration-design", "# Waiting")}, nil
		}
		return backend.RunResult{SessionID: "author", FinalText: "Noted."}, nil
	}
	options, store := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := session.Send(context.Background(), "Write it.")
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Writes) != 1 || len(session.Writes()) != 1 {
		t.Fatal("the operator was not asked where nothing could land the document")
	}
	if _, err := session.Send(context.Background(), "Something else."); err != nil {
		t.Fatalf("conversation stopped working: %v", err)
	}
	saved, err := store.Load(runstate.ConversationIdentity{Role: domain.RoleArchitect, Agent: "architect"})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.PendingWrites) != 1 || saved.PendingWrites[0].Publication != nil {
		t.Fatal("a publication was saved where integration is not automatic")
	}
	if records, err := runs.Recorded(); err != nil || len(records) != 0 {
		t.Fatalf("runs = %v, %v", records, err)
	}
}

func TestSavedPublicationUnderHumanIntegrationReturnsToTheOperator(t *testing.T) {
	t.Parallel()

	_, _, provider, pipeline, runs := automaticFixture(t)
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleArchitect {
			if len(provider.RequestsForRole(domain.RoleArchitect)) > 1 {
				return backend.RunResult{SessionID: "author", FinalText: "Noted."}, nil
			}
			return backend.RunResult{SessionID: "author", FinalText: submittedDesign("lapsed-design", "# Lapsed")}, nil
		}
		return backend.RunResult{}, errors.New("no run should be opened")
	}
	// Confirmed while integration was automatic, and the process ended before
	// any run was opened for it.
	options, _ := publicationConversation(t, pipeline, provider)
	options.DocumentPublisher = interruptedPublication{pipeline}
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Write it."); err != nil {
		t.Fatal(err)
	}
	pipeline.Config.Approvals.Integration = domain.ApprovalHuman
	options.DocumentPublisher = pipeline
	session, err = chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Carry on."); err != nil {
		t.Fatalf("conversation stopped working: %v", err)
	}
	if writes := session.Writes(); len(writes) != 1 || writes[0].Publication != nil {
		t.Fatalf("lapsed confirmation was not put to the operator: %+v", writes)
	}
	if records, err := runs.Recorded(); err != nil || len(records) != 0 {
		t.Fatalf("runs = %v, %v", records, err)
	}
	if !strings.Contains(provider.RequestsForRole(domain.RoleArchitect)[1].Prompt, "put to the operator to confirm instead") {
		t.Fatal("the owning role was not told its document went to the operator")
	}
}

func TestDocumentReviewAndCheckFailuresReturnToOwnerAndRevisionUsesFreshRun(t *testing.T) {
	t.Parallel()

	for _, cause := range []string{"review", "check"} {
		t.Run(cause, func(t *testing.T) {
			_, _, provider, pipeline, runs := automaticFixture(t)
			original := provider.Respond
			authored := 0
			reviews := 0
			provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
				if request.Role == domain.RoleArchitect {
					authored++
					body := "# Bad"
					if authored > 1 {
						body = "# Corrected"
					}
					return backend.RunResult{SessionID: "author", FinalText: submittedDesign("returned-design", body)}, nil
				}
				if request.Role == domain.RoleReviewer {
					reviews++
					if cause == "review" && reviews == 1 {
						return backend.RunResult{SessionID: "independent", FinalText: `{"decision":"repair","summary":"fix the plan","findings":[{"severity":"blocker","message":"name the recovery step"}]}`}, nil
					}
				}
				return original(request)
			}
			if cause == "check" {
				pipeline.Config.Checks = []string{"! grep -q '# Bad' docs/designs/returned-design.md"}
			}
			options, store := publicationConversation(t, pipeline, provider)
			session, err := chat.Open(options)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := session.Send(context.Background(), "Write it."); err != nil {
				t.Fatal(err)
			}
			saved, err := store.Load(runstate.ConversationIdentity{Role: domain.RoleArchitect, Agent: "architect"})
			if err != nil {
				t.Fatal(err)
			}
			want := "name the recovery step"
			if cause == "check" {
				want = "Failing check:"
			}
			if !strings.Contains(saved.PendingTrackerResults, want) || !strings.Contains(saved.PendingTrackerResults, "architect must revise") {
				t.Fatalf("return = %s", saved.PendingTrackerResults)
			}
			records, _ := runs.Recorded()
			if len(records) != 1 || readmodel.StoppageMover(records[0], nil, false) != readmodel.MoverArchitect {
				t.Fatal("stop not owned by architect")
			}
			if _, err := session.Send(context.Background(), "Revise it."); err != nil {
				t.Fatal(err)
			}
			records, _ = runs.Recorded()
			if len(records) != 2 || records[0].RunID == records[1].RunID {
				t.Fatalf("fresh runs = %+v", records)
			}
			landed := false
			for _, record := range records {
				landed = landed || record.Status == runstate.StatusSucceeded
			}
			if !landed {
				t.Fatal("revision did not land")
			}
			if len(provider.RequestsForRole(domain.RoleDeveloper)) != 0 {
				t.Fatal("developer invoked")
			}
		})
	}
}

// Keep this seam at the promotion so the conflict is an actual Git replay,
// after a check and review passed on the exact confirmed file.
type documentConflictWorktrees struct {
	WorktreeManager
	change func()
}

func (w *documentConflictWorktrees) Integrate(ctx context.Context, tree gitworktree.Worktree, message string) (gitworktree.Integration, error) {
	if w.change != nil {
		change := w.change
		w.change = nil
		change()
	}
	return w.WorktreeManager.Integrate(ctx, tree, message)
}

func TestDocumentTargetConflictReturnsPathsToOwnerAndFreshRevisionLands(t *testing.T) {
	t.Parallel()

	repository, _, provider, pipeline, runs := automaticFixture(t)
	original := provider.Respond
	authored := 0
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleArchitect {
			authored++
			text := submittedDesign("conflicting-design", "# Our design")
			if authored > 1 {
				text = fmt.Sprintf("Revised.\n%s\n{\"documents\":[{\"action\":\"revise\",\"id\":\"conflicting-design\",\"body\":\"# Both decisions reconciled\",\"reason\":\"revised by the architect after the conflict\"}]}\n```", artifact.WriteFence)
			}
			return backend.RunResult{SessionID: "author", FinalText: text}, nil
		}
		return original(request)
	}
	pipeline.Worktrees = &documentConflictWorktrees{WorktreeManager: pipeline.Worktrees, change: func() {
		store := artifact.StoreFor(repository, pipeline.Config.Product)
		if _, err := store.Create(domain.RoleArchitect, artifact.Draft{ID: "conflicting-design", Kind: artifact.KindDesign, Title: "Another design", Directory: "docs/designs", Body: "# The target's decision", Reason: "another architect revision"}, time.Now()); err != nil {
			t.Fatal(err)
		}
		runPipelineGit(t, repository, "add", "docs/designs/conflicting-design.md")
		runPipelineGit(t, repository, "commit", "-m", "another design reached the target")
	}}
	options, conversations := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Write it."); err != nil {
		t.Fatal(err)
	}
	saved, err := conversations.Load(runstate.ConversationIdentity{Role: domain.RoleArchitect, Agent: "architect"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(saved.PendingTrackerResults, "Conflicting paths: docs/designs/conflicting-design.md") || !strings.Contains(saved.PendingTrackerResults, "architect must revise") {
		t.Fatalf("return: %s", saved.PendingTrackerResults)
	}
	records, _ := runs.Recorded()
	if len(records) != 1 || records[0].ReplayConflict == nil || readmodel.StoppageMover(records[0], nil, false) != readmodel.MoverArchitect {
		t.Fatal("conflict not returned to owner")
	}
	if _, err := session.Send(context.Background(), "Reconcile the document."); err != nil {
		t.Fatal(err)
	}
	records, _ = runs.Recorded()
	if len(records) != 2 {
		t.Fatal("revision did not open a fresh run")
	}
	if content := publicationGit(t, repository, "show", "main:docs/designs/conflicting-design.md"); !strings.Contains(content, "# Both decisions reconciled") {
		t.Fatal(content)
	}
	if len(provider.RequestsForRole(domain.RoleDeveloper)) != 0 {
		t.Fatal("developer invoked on conflict")
	}
}

func TestDocumentReturnsStopAtBoundAndTellOwner(t *testing.T) {
	t.Parallel()

	_, _, provider, pipeline, runs := automaticFixture(t)
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleArchitect {
			return backend.RunResult{SessionID: "author", FinalText: submittedDesign("bounded-design", "# Needs revision")}, nil
		}
		return backend.RunResult{SessionID: "independent", FinalText: `{"decision":"repair","summary":"revise the document","findings":[{"severity":"blocker","message":"state the missing decision"}]}`}, nil
	}
	options, store := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < runstate.MaxDocumentReturns+1; i++ {
		if _, err := session.Send(context.Background(), "Revise it."); err != nil {
			t.Fatal(err)
		}
	}
	records, err := runs.Recorded()
	if err != nil || len(records) != runstate.MaxDocumentReturns {
		t.Fatalf("runs: %d, %v", len(records), err)
	}
	saved, err := store.Load(runstate.ConversationIdentity{Role: domain.RoleArchitect, Agent: "architect"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(saved.PendingTrackerResults, "automatic publication stopped after 3 returned runs") {
		t.Fatal(saved.PendingTrackerResults)
	}
	if len(session.Writes()) != 0 || len(provider.RequestsForRole(domain.RoleDeveloper)) != 0 {
		t.Fatal("bound asked the operator or developer")
	}
}

func TestDocumentPublicationRefusesExtraFilesAndChangedContent(t *testing.T) {
	t.Parallel()

	for _, tamper := range []string{"extra-file", "changed-content"} {
		t.Run(tamper, func(t *testing.T) {
			repository, _, _, pipeline, _ := automaticFixture(t)
			store := artifact.StoreFor(repository, pipeline.Config.Product)
			candidate, automatic, err := store.PrepareConfirmation(domain.RoleArchitect, artifact.Write{Action: artifact.WriteCreate, ID: "scope-design", Kind: artifact.KindDesign, Title: "Scope", Directory: "docs/designs", Body: "# Scope", Reason: "written by the architect"}, artifact.Policy{Designs: domain.ApprovalAutomatic}, "confirmed by harness", time.Now())
			if err != nil || !automatic {
				t.Fatal(err)
			}
			tree, err := pipeline.Worktrees.Create(context.Background(), gitworktree.CreateRequest{RunID: pipelineRunID, WorkItemID: "document", BaseRef: "main", TargetBranch: "main"})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(tree.Path, "docs/designs"), 0700); err != nil {
				t.Fatal(err)
			}
			content := candidate.Content
			if tamper == "changed-content" {
				content += "\nInjected content"
			}
			if err := os.WriteFile(filepath.Join(tree.Path, candidate.Artifact.Path), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if tamper == "extra-file" {
				if err := os.WriteFile(filepath.Join(tree.Path, "extra"), []byte("extra"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			a := &activeRun{pipeline: pipeline, state: runstate.State{Document: &runstate.DocumentPublication{Candidate: candidate}}, worktree: tree}
			if _, err := a.gateProtectedPaths(context.Background()); err == nil {
				t.Fatal("tampering passed the exact document gate")
			}
		})
	}
}

func publicationGit(t *testing.T, repository string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

// A panic simulates the process disappearing without the pipeline getting a
// chance to settle a failure. Its durable handoff and run remain resumable.
type interruptedDocumentWorktrees struct {
	WorktreeManager
	stopped bool
}

func (w *interruptedDocumentWorktrees) Integrate(ctx context.Context, tree gitworktree.Worktree, message string) (gitworktree.Integration, error) {
	landed, err := w.WorktreeManager.Integrate(ctx, tree, message)
	if err == nil && !w.stopped {
		w.stopped = true
		panic("process disappeared after target moved")
	}
	return landed, err
}

func TestDocumentRestartDuringReviewOrAfterTargetMoved(t *testing.T) {
	t.Parallel()

	for _, step := range []string{"checkout created", "document committed", "review", "target moved"} {
		t.Run(step, func(t *testing.T) {
			repository, _, provider, pipeline, runs := automaticFixture(t)
			original := provider.Respond
			interrupted := false
			provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
				if request.Role == domain.RoleArchitect {
					return backend.RunResult{SessionID: "author", FinalText: submittedDesign("restart-design", "# Saved exactly")}, nil
				}
				if step == "review" && request.Role == domain.RoleReviewer && !interrupted {
					interrupted = true
					panic("process disappeared during review")
				}
				return original(request)
			}
			if step == "target moved" {
				pipeline.Worktrees = &interruptedDocumentWorktrees{WorktreeManager: pipeline.Worktrees}
			}
			if step == "checkout created" || step == "document committed" {
				pipeline.Worktrees = &interruptedDocumentCheckpoint{WorktreeManager: pipeline.Worktrees, step: step}
			}
			options, _ := publicationConversation(t, pipeline, provider)
			session, err := chat.Open(options)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer func() {
					if recover() == nil {
						t.Fatal("interruption did not happen")
					}
				}()
				_, _ = session.Send(context.Background(), "Write it.")
			}()
			before, _ := runs.Recorded()
			if len(before) != 1 || before[0].Status.Terminal() {
				t.Fatalf("run not preserved: %+v", before)
			}
			session, err = chat.Open(options)
			if err != nil {
				t.Fatal(err)
			}
			if err := session.PublishDocuments(context.Background()); err != nil {
				t.Fatal(err)
			}
			after, _ := runs.Recorded()
			if len(after) != 1 || after[0].Status != runstate.StatusSucceeded {
				t.Fatalf("run not resumed: %+v", after)
			}
			if publicationGit(t, repository, "show", "main:docs/designs/restart-design.md") != strings.TrimSpace(after[0].Document.Candidate.Content) {
				t.Fatal("target differs from confirmation")
			}
			if len(provider.RequestsForRole(domain.RoleArchitect)) != 1 || len(provider.RequestsForRole(domain.RoleDeveloper)) != 0 {
				t.Fatal("document was rewritten")
			}
			commits := publicationGit(t, repository, "log", "main", "--format=%s")
			if strings.Count(commits, "Publishing Recovery design") != 1 {
				t.Fatalf("document committed more than once: %s", commits)
			}
		})
	}
}

func TestDocumentQueuedPublicationSettlesWithoutBacklogOrDeveloper(t *testing.T) {
	t.Parallel()

	for _, protected := range []bool{false, true} {
		t.Run(fmt.Sprint(protected), func(t *testing.T) {
			fixture := newQueuedFixture(t)
			if protected {
				fixture.forge.SetTargetProtection(publish.BranchProtection{Protected: true, By: "ruleset"})
			}
			provider := orchestratortest.RoleBackend(nil, approveVerdict)
			original := provider.Respond
			provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
				if request.Role == domain.RoleArchitect {
					return backend.RunResult{SessionID: "author", FinalText: submittedDesign("queued-design", "# Queued document")}, nil
				}
				return original(request)
			}
			pipeline := publishing(automatic(newSharedPipeline(t, fixture.repository, fixture.worktreeRoot, fixture.store, fixture.tracker, provider, []string{"exit 0"}), provider), fixture.forge)
			options, _ := publicationConversation(t, pipeline, provider)
			session, err := chat.Open(options)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = session.Send(context.Background(), "Write it."); err != nil {
				t.Fatal(err)
			}
			records, _ := fixture.store.Recorded()
			if len(records) != 1 || records[0].PullRequest == nil || !records[0].PullRequest.MergeQueued {
				t.Fatalf("not queued: %+v", records)
			}
			runID := records[0].RunID
			fixture.forge.PerformQueuedMerge(t)
			results, err := fixture.reconciler(t).Reconcile(context.Background())
			if err != nil || len(results) != 1 || results[0].Failure != "" {
				t.Fatalf("settlement: %+v, %v", results, err)
			}
			session, err = chat.Open(options)
			if err != nil {
				t.Fatal(err)
			}
			if err = session.PublishDocuments(context.Background()); err != nil {
				t.Fatal(err)
			}
			state, err := fixture.store.Load(runID)
			if err != nil || !documentDelivery(state).Landed || !documentDelivery(state).Settled {
				t.Fatalf("not landed: %+v, %v", state, err)
			}
			if len(provider.RequestsForRole(domain.RoleDeveloper)) != 0 || len(fixture.tracker.Record().Calls) != 0 {
				t.Fatal("document touched a developer or backlog item")
			}
			if publicationGit(t, fixture.repository, "show", "main:docs/designs/queued-design.md") != strings.TrimSpace(state.Document.Candidate.Content) {
				t.Fatal("document not on target")
			}
		})
	}
}

type interruptedDocumentCheckpoint struct {
	WorktreeManager
	step    string
	stopped bool
}

func (w *interruptedDocumentCheckpoint) Create(ctx context.Context, request gitworktree.CreateRequest) (gitworktree.Worktree, error) {
	tree, err := w.WorktreeManager.Create(ctx, request)
	if err == nil && w.step == "checkout created" && !w.stopped {
		w.stopped = true
		panic("process disappeared after checkout creation")
	}
	return tree, err
}
func (w *interruptedDocumentCheckpoint) CommitAttempt(ctx context.Context, tree gitworktree.Worktree, message string) (string, error) {
	commit, err := w.WorktreeManager.CommitAttempt(ctx, tree, message)
	if err == nil && w.step == "document committed" && !w.stopped {
		w.stopped = true
		panic("process disappeared after document commit")
	}
	return commit, err
}

func TestDocumentReviewerCannotReuseAuthorSession(t *testing.T) {
	t.Parallel()

	repository, _, provider, pipeline, runs := automaticFixture(t)
	original := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleArchitect {
			return backend.RunResult{SessionID: "author", FinalText: submittedDesign("shared-session-design", "# Owner's document")}, nil
		}
		result, err := original(request)
		result.SessionID = "author"
		return result, err
	}
	options, conversations := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = session.Send(context.Background(), "Write it."); err != nil {
		t.Fatal(err)
	}
	records, _ := runs.Recorded()
	if len(records) != 1 || records[0].Status != runstate.StatusFailed || records[0].Integration != nil {
		t.Fatalf("reused session landed: %+v", records)
	}
	saved, err := conversations.Load(runstate.ConversationIdentity{Role: domain.RoleArchitect, Agent: "architect"})
	if err != nil || !strings.Contains(saved.PendingTrackerResults, "reused the document author's provider session") {
		t.Fatalf("return: %s, %v", saved.PendingTrackerResults, err)
	}
	if publicationGit(t, repository, "status", "--porcelain") != "" || len(provider.RequestsForRole(domain.RoleDeveloper)) != 0 {
		t.Fatal("failed review changed primary or invoked developer")
	}
}

func TestDocumentReconciliationNamesItsOwner(t *testing.T) {
	t.Parallel()

	state := runstate.State{RunID: "run-document", Document: &runstate.DocumentPublication{Owner: domain.RoleArchitect, WriteID: "document-4.1", ConversationID: "chat-owner", Candidate: artifact.ConfirmedDocument{Artifact: artifact.Artifact{Title: "Recovery design"}}}}
	notes := renderReconcileBlockerNotes(state, gitworktree.Observation{}, "the forge refused the merge")
	if !strings.Contains(notes, "The architect must revise document document-4.1 (Recovery design) in conversation chat-owner") || strings.Contains(notes, "operator") {
		t.Fatal(notes)
	}
}

// A project that keeps its intent in a companion intent repository: a design
// its automatic policy confirms is not put to the operator, is not landed in
// the project's repository, and opens no run; it stays confirmed and saved, and
// the owning role is told once what holds it.
func TestCompanionIntentDocumentIsHeldConfirmedRatherThanPutToTheOperator(t *testing.T) {
	t.Parallel()

	repository, _, provider, pipeline, runs := automaticFixture(t)
	intent := t.TempDir()
	pipeline.Config.Intent.Repository = "intent"
	pipeline.Config.Product.IntentRepository = intent
	if !pipeline.PublishesDocuments() {
		t.Fatal("a companion-intent project stopped confirming by its automatic policy")
	}
	original := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleArchitect {
			if len(provider.RequestsForRole(domain.RoleArchitect)) > 1 {
				return backend.RunResult{SessionID: "author", FinalText: "Noted."}, nil
			}
			return backend.RunResult{SessionID: "author", FinalText: submittedDesign("held-design", "# Held")}, nil
		}
		return original(request)
	}
	options, store := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := session.Send(context.Background(), "Write the design.")
	if err != nil {
		t.Fatalf("a held document stopped the message: %v", err)
	}
	if len(reply.Writes) != 0 {
		t.Fatal("a design under an automatic policy was put to the operator")
	}
	if _, err := session.Send(context.Background(), "And carry on."); err != nil {
		t.Fatalf("conversation wedged on a held document: %v", err)
	}
	recorded, err := runs.Recorded()
	if err != nil || len(recorded) != 0 {
		t.Fatalf("runs = %+v, %v; want none opened", recorded, err)
	}
	if len(provider.RequestsForRole(domain.RoleReviewer)) != 0 {
		t.Fatal("a held document was reviewed")
	}
	if _, err := os.Stat(filepath.Join(repository, "docs", "designs", "held-design.md")); !os.IsNotExist(err) {
		t.Fatal("the document landed in the project's repository")
	}
	saved, err := store.Load(runstate.ConversationIdentity{Role: domain.RoleArchitect, Agent: "architect"})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.PendingWrites) != 1 || saved.PendingWrites[0].Publication == nil {
		t.Fatalf("the confirmed document was not kept: %+v", saved.PendingWrites)
	}
	told := provider.RequestsForRole(domain.RoleArchitect)[1].Prompt + saved.PendingTrackerResults
	if strings.Count(told, "companion intent repository") != 1 {
		t.Fatalf("the owning role was not told once what holds its document:\n%s", told)
	}
}

// Two waiting copies of one decision record, written before a decision record
// had an approval setting, are settled at the next turn with no hand step: the
// earlier copy is confirmed under approvals.designs and lands through review,
// and the later one gives its place back.
func TestWaitingCopiesOfADecisionRecordAreResolvedAtTheNextTurn(t *testing.T) {
	t.Parallel()
	repository, _, provider, pipeline, runs := automaticFixture(t)
	options, store := publicationConversation(t, pipeline, provider)
	if _, err := chat.Open(options); err != nil {
		t.Fatal(err)
	}
	prior := conversationRecord(t, store)
	copyOf := func(writeID string, turn int) runstate.PendingWrite {
		return runstate.PendingWrite{ID: writeID, Turn: turn, Action: "create", Artifact: "authority-by-capability", Kind: "decision", Title: "Authority by capability", Directory: "docs/decisions", Body: "# Authority by capability\n\nThe architect's ruling.", Reason: "the architect's ruling"}
	}
	prior.PendingWrites = []runstate.PendingWrite{copyOf("document-703.1", 703), copyOf("document-713.1", 713)}
	if err := store.Save(prior); err != nil {
		t.Fatal(err)
	}
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.PublishDocuments(context.Background()); err != nil {
		t.Fatal(err)
	}
	records := documentRuns(t, runs)
	if len(records) != 1 || records[0].Status != runstate.StatusSucceeded || records[0].Integration == nil {
		t.Fatalf("decision record runs = %+v", records)
	}
	if records[0].Document.WriteID != "document-703.1" {
		t.Fatalf("published %s, want the earlier copy document-703.1", records[0].Document.WriteID)
	}
	content := publicationGit(t, repository, "show", "main:docs/decisions/authority-by-capability.md")
	if !strings.Contains(content, "by: harness") || !strings.Contains(content, "policy: approvals.designs") {
		t.Fatalf("decision record was not confirmed under approvals.designs:\n%s", content)
	}
	saved := conversationRecord(t, store)
	if len(saved.PendingWrites) != 0 || len(session.Writes()) != 0 {
		t.Fatalf("documents still waiting: %+v", saved.PendingWrites)
	}
	if told := toldArchitect(provider, saved); !strings.Contains(told, "document-713.1 (authority-by-capability) was a second copy of document-703.1") {
		t.Fatalf("the architect was not told the copy's place was given back: %q", told)
	}
	if len(provider.RequestsForRole(domain.RoleDeveloper)) != 0 {
		t.Fatal("a developer was asked to write the decision record")
	}
}

// Only a change wholly inside the artifact homes takes the document landing
// route, a home's directory index included.
func TestTheDocumentRouteTakesOnlyChangesInsideTheArtifactHomes(t *testing.T) {
	t.Parallel()

	cfg := config.Config{Product: config.Product{Specifications: config.DefaultSpecifications, Designs: config.DefaultDesigns, Decisions: config.DefaultDecisions, Invariants: config.DefaultInvariants}}
	for _, inside := range [][]string{
		{"docs/designs/recovery-design.md"},
		{"docs/designs/README.md"},
		{"docs/product/goals/v1-goals.md"},
		{"docs/decisions/a-decision.md", "docs/decisions/invariants/an-invariant.md"},
	} {
		if err := documentRouteRefusal(cfg, inside); err != nil {
			t.Errorf("documentRouteRefusal(%v) = %v, want it on the route", inside, err)
		}
	}
	for _, outside := range [][]string{
		{"docs/designs/recovery-design.md", "internal/orchestrator/pipeline.go"},
		{"Makefile"},
		{"docs/operations.md"},
		{"docs/designs-old/recovery-design.md"},
		{},
	} {
		if err := documentRouteRefusal(cfg, outside); err == nil {
			t.Errorf("documentRouteRefusal(%v) = nil, want it refused from the route", outside)
		}
	}
}

// A document run whose change reaches outside the artifact homes is refused
// from the route before any check runs.
func TestAMixedChangeIsRefusedFromTheDocumentRouteBeforeAnyCheck(t *testing.T) {
	t.Parallel()

	repository, _, _, pipeline, _ := automaticFixture(t)
	marker := filepath.Join(t.TempDir(), "checks-ran")
	pipeline.Config.Checks = []string{"touch " + marker}
	pipeline.Config.DocumentChecks = []string{"touch " + marker}
	store := artifact.StoreFor(repository, pipeline.Config.Product)
	candidate, automatic, err := store.PrepareConfirmation(domain.RoleArchitect, artifact.Write{Action: artifact.WriteCreate, ID: "mixed-design", Kind: artifact.KindDesign, Title: "Mixed", Directory: "docs/designs", Body: "# Mixed", Reason: "written by the architect"}, artifact.Policy{Designs: domain.ApprovalAutomatic}, "confirmed by harness", time.Now())
	if err != nil || !automatic {
		t.Fatal(err)
	}
	tree, err := pipeline.Worktrees.Create(context.Background(), gitworktree.CreateRequest{RunID: pipelineRunID, WorkItemID: "document", BaseRef: "main", TargetBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tree.Path, "docs/designs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree.Path, candidate.Artifact.Path), []byte(candidate.Content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree.Path, "code.go"), []byte("package code\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a := &activeRun{pipeline: pipeline, state: runstate.State{Document: &runstate.DocumentPublication{Candidate: candidate}}, worktree: tree}
	err = a.verify(context.Background())
	if err == nil || !strings.Contains(err.Error(), "only a change wholly inside the artifact homes") || !strings.Contains(err.Error(), "code.go") {
		t.Fatalf("verify() = %v, want the change refused from the document route", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a check ran over a change refused from the route: %v", err)
	}
}

// documentCheckMarkers is a full check list standing for the race check and a
// document check list, each leaving a marker where it ran.
func documentCheckMarkers(t *testing.T) (full, document string, fullChecks, documentChecks []string) {
	t.Helper()
	directory := t.TempDir()
	full, document = filepath.Join(directory, "race-ran"), filepath.Join(directory, "document-checks-ran")
	return full, document, []string{"touch " + full}, []string{"touch " + document}
}

func landDesign(t *testing.T, pipeline Pipeline, provider *orchestratortest.Backend, runs *runstate.Store) runstate.State {
	t.Helper()
	original := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleArchitect {
			return backend.RunResult{Backend: domain.BackendClaudeCode, SessionID: "author-session", FinalText: submittedDesign("checked-design", "# Checked\n\nThe owner's exact words.")}, nil
		}
		return original(request)
	}
	options, _ := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Write the design."); err != nil {
		t.Fatal(err)
	}
	recorded, err := runs.Recorded()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("runs = %+v, %v", recorded, err)
	}
	return recorded[0]
}

// A project that declares document checks has its document landings run
// exactly those and never its full checks, the race check among them. The
// evidence names the check set, and the landing is still independently
// reviewed.
func TestADocumentLandingRunsTheDeclaredDocumentChecksAndNeverTheRaceCheck(t *testing.T) {
	t.Parallel()

	_, _, provider, pipeline, runs := automaticFixture(t)
	full, document, fullChecks, documentChecks := documentCheckMarkers(t)
	pipeline.Config.Checks, pipeline.Config.DocumentChecks = fullChecks, documentChecks
	state := landDesign(t, pipeline, provider, runs)
	if state.Status != runstate.StatusSucceeded || state.Integration == nil {
		t.Fatalf("run = %+v, want the design landed", state)
	}
	if _, err := os.Stat(document); err != nil {
		t.Fatalf("the declared document checks did not run: %v", err)
	}
	if _, err := os.Stat(full); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the full checks, race check among them, ran over a document: %v", err)
	}
	if passed := state.ChecksPassed; passed == nil || !slices.Equal(passed.Commands, documentChecks) || passed.CheckSet != config.CheckSetDigest(documentChecks) {
		t.Fatalf("evidence = %+v, want the document checks named and their set's digest", passed)
	}
	if state.ReviewDecision != runstate.ReviewApprove || len(provider.RequestsForRole(domain.RoleReviewer)) != 1 {
		t.Fatal("the document landed without its independent review")
	}
}

// A project that declares no document checks has its document landings run the
// full checks, as every other run does.
func TestADocumentLandingRunsTheFullChecksWhereNoneAreDeclared(t *testing.T) {
	t.Parallel()

	_, _, provider, pipeline, runs := automaticFixture(t)
	full, document, fullChecks, _ := documentCheckMarkers(t)
	pipeline.Config.Checks = fullChecks
	state := landDesign(t, pipeline, provider, runs)
	if state.Status != runstate.StatusSucceeded || state.Integration == nil {
		t.Fatalf("run = %+v, want the design landed", state)
	}
	if _, err := os.Stat(full); err != nil {
		t.Fatalf("the full checks did not run: %v", err)
	}
	if _, err := os.Stat(document); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("document checks ran where none are declared: %v", err)
	}
	if passed := state.ChecksPassed; passed == nil || passed.CheckSet != config.CheckSetDigest(fullChecks) {
		t.Fatalf("evidence = %+v, want the full checks' set digest", passed)
	}
}

// In a Go project that declares no document checks, a document landing's full
// checks are told the whole module: the document is the whole change, and
// nothing after its review checks it again, so checks narrowed to the Go
// packages it touches (none) would leave the promotion with nothing it can
// land on.
func TestADocumentLandingInAGoModuleRunsTheFullChecksWhole(t *testing.T) {
	t.Parallel()

	repository, _, provider, pipeline, runs := automaticFixture(t)
	writeCommitted(t, repository, "go.mod", "module example.com/project\n")
	writeCommitted(t, repository, "internal/checks/runner.go", "package checks\n")
	told := filepath.Join(t.TempDir(), "told.txt")
	pipeline.Config.Checks = []string{`printf '%s\n' "$` + checks.ChangedGoPackagesVariable + `" >> ` + told}
	state := landDesign(t, pipeline, provider, runs)
	if state.Status != runstate.StatusSucceeded || state.Integration == nil {
		t.Fatalf("run = %+v, want the design landed", state)
	}
	read, err := os.ReadFile(told)
	if err != nil {
		t.Fatalf("the full checks did not run: %v", err)
	}
	if got := strings.Fields(string(read)); !slices.Equal(got, []string{"./..."}) {
		t.Fatalf("the document's checks were told %q, want the whole module once", got)
	}
	if passed := state.ChecksPassed; passed == nil || passed.Narrowed != "" {
		t.Fatalf("evidence = %+v, want checks that were told the whole module", passed)
	}
}

// The document checks narrow the checks and nothing else: a reviewer that
// refuses the document still keeps it from landing.
func TestADocumentUnderDeclaredChecksStillNeedsItsReviewersApproval(t *testing.T) {
	t.Parallel()

	repository, _, provider, pipeline, runs := automaticFixture(t)
	_, _, fullChecks, documentChecks := documentCheckMarkers(t)
	pipeline.Config.Checks, pipeline.Config.DocumentChecks = fullChecks, documentChecks
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleReviewer {
			return backend.RunResult{SessionID: "independent", FinalText: `{"decision":"repair","summary":"revise the document","findings":[{"severity":"blocker","message":"state the missing decision"}]}`}, nil
		}
		return backend.RunResult{}, errors.New("unexpected invocation")
	}
	state := landDesign(t, pipeline, provider, runs)
	if state.Status == runstate.StatusSucceeded || state.Integration != nil || !state.DocumentJudged() {
		t.Fatalf("run = %+v, want the reviewer's refusal to keep the document from landing", state)
	}
	if _, err := os.Stat(filepath.Join(repository, "docs/designs/checked-design.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the refused document reached the target: %v", err)
	}
}

// The check evidence is bound to the document check set: a list changed after
// the checks passed and before the promotion leaves the landing without
// evidence for the list in force, and nothing lands.
func TestADocumentCheckSetChangedBeforeThePromotionRefusesIt(t *testing.T) {
	t.Parallel()

	repository, _, provider, pipeline, runs := automaticFixture(t)
	_, _, fullChecks, documentChecks := documentCheckMarkers(t)
	pipeline.Config.Checks, pipeline.Config.DocumentChecks = fullChecks, documentChecks
	original := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleReviewer {
			// The pipeline is held by value, and its list shares this array.
			documentChecks[0] = "exit 0"
		}
		return original(request)
	}
	state := landDesign(t, pipeline, provider, runs)
	if state.Status == runstate.StatusSucceeded || state.Integration != nil || !strings.Contains(state.Failure, "check set") {
		t.Fatalf("run = %+v, want the promotion refused for evidence under another check set", state)
	}
	if _, err := os.Stat(filepath.Join(repository, "docs/designs/checked-design.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the document reached the target without evidence for its check set: %v", err)
	}
}

// Under declared document checks, runs that keep handing the document back to
// its owner still stop automatic publication at the bound and tell the owner.
func TestDocumentReturnsUnderDocumentChecksStillStopAtBound(t *testing.T) {
	t.Parallel()

	_, _, provider, pipeline, runs := automaticFixture(t)
	full, _, fullChecks, _ := documentCheckMarkers(t)
	pipeline.Config.Checks = fullChecks
	pipeline.Config.DocumentChecks = []string{"! grep -q '# Needs revision' docs/designs/bounded-design.md"}
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleArchitect {
			return backend.RunResult{SessionID: "author", FinalText: submittedDesign("bounded-design", "# Needs revision")}, nil
		}
		return backend.RunResult{SessionID: "independent", FinalText: approveVerdict}, nil
	}
	options, store := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < runstate.MaxDocumentReturns+1; i++ {
		if _, err := session.Send(context.Background(), "Revise it."); err != nil {
			t.Fatal(err)
		}
	}
	records, err := runs.Recorded()
	if err != nil || len(records) != runstate.MaxDocumentReturns {
		t.Fatalf("runs: %d, %v", len(records), err)
	}
	saved, err := store.Load(runstate.ConversationIdentity{Role: domain.RoleArchitect, Agent: "architect"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(saved.PendingTrackerResults, "automatic publication stopped after 3 returned runs") {
		t.Fatal(saved.PendingTrackerResults)
	}
	for _, record := range records {
		if record.CheckFailure == nil || !record.DocumentJudged() {
			t.Fatalf("run = %+v, want each returned on its failing document check", record)
		}
	}
	if _, err := os.Stat(full); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the full checks ran over a document: %v", err)
	}
}
