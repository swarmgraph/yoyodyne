package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/invariant"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// submittedInvariant is an architect's reply writing one invariant: a new one
// where action is create, an amendment of an existing one where it is revise.
func submittedInvariant(action, id, statement string) string {
	body := "## Must hold\n\n" + statement + "\n\n## Why\n\nA change whose own work is correct can still break it."
	fields := fmt.Sprintf(`"action":"revise","id":%q`, id)
	if action == "create" {
		fields = fmt.Sprintf(`"action":"create","id":%q,"kind":"invariant","title":"Checks that could not run are not passes","directory":"docs/decisions/invariants","established_by":["yoyodyne-1m3"]`, id)
	}
	return fmt.Sprintf("Ruled.\n%s\n{\"documents\":[{%s,\"body\":%q,\"reason\":\"yoyodyne-1m3: the architect's ruling on checks that could not run\"}]}\n```", artifact.WriteFence, fields, body)
}

// landedInvariant is the one run a conversation's invariant was published by,
// checked to have landed through an independent review and no developer, with
// the primary checkout left as it was.
func landedInvariant(t *testing.T, repository string, provider interface {
	RequestsForRole(domain.AgentRole) []backend.RunRequest
}, runs *runstate.Store, path string) (runstate.State, string) {
	t.Helper()
	recorded, err := runs.Recorded()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("runs = %+v, %v", recorded, err)
	}
	state := recorded[0]
	if state.Status != runstate.StatusSucceeded || state.Document == nil || state.Integration == nil || state.ReviewSessionID == "" || state.ReviewSessionID == state.Document.AuthorSession {
		t.Fatalf("run = %+v", state)
	}
	if len(provider.RequestsForRole(domain.RoleDeveloper)) != 0 || len(provider.RequestsForRole(domain.RoleReviewer)) != 1 {
		t.Fatal("publication did not use exactly an independent review")
	}
	if state.Document.Owner != domain.RoleArchitect || state.Document.Candidate.Artifact.Kind != artifact.KindInvariant || state.Document.Candidate.Artifact.Path != path {
		t.Fatalf("publication = %+v", state.Document)
	}
	approval, ok := state.Document.Candidate.Artifact.LatestApproval()
	if !ok || approval.By != artifact.ApproverHarness || approval.Policy != "approvals.designs" {
		t.Fatalf("confirmation = %+v, want the harness under approvals.designs", approval)
	}
	content := publicationGit(t, repository, "show", "main:"+path)
	if content != strings.TrimSpace(state.Document.Candidate.Content) {
		t.Fatalf("published different bytes: %s", content)
	}
	if status := publicationGit(t, repository, "status", "--porcelain"); status != "" {
		t.Fatalf("primary checkout left dirty: %s", status)
	}
	return state, content
}

// The architect records a new invariant from her conversation as a typed
// document write, and it lands through a reviewed run in the invariants
// directory's own form, readable by every reader of that directory.
func TestAnInvariantTheArchitectWritesLandsThroughIndependentReview(t *testing.T) {
	t.Parallel()

	repository, _, provider, pipeline, runs := automaticFixture(t)
	original := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleArchitect {
			return backend.RunResult{Backend: domain.BackendClaudeCode, SessionID: "author-session", FinalText: submittedInvariant("create", "checks-that-did-not-run-are-not-passes", "A check that could not start is never recorded as one that passed.")}, nil
		}
		return original(request)
	}
	options, _ := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := session.Send(context.Background(), "Record the ruling as an invariant.")
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Writes) != 0 || len(session.Writes()) != 0 {
		t.Fatal("an automatically confirmed invariant was put to the operator")
	}
	path := "docs/decisions/invariants/checks-that-did-not-run-are-not-passes.md"
	_, content := landedInvariant(t, repository, provider, runs, path)
	// The file keeps the invariants directory's scheme: no artifact kind and no
	// approvals in its frontmatter, and it parses as an invariant.
	if strings.Contains(content, "kind:") || strings.Contains(content, "approvals:") {
		t.Fatalf("the invariant's file carries artifact frontmatter:\n%s", content)
	}
	parsed, err := invariant.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.ID != "checks-that-did-not-run-are-not-passes" || parsed.Statement != "A check that could not start is never recorded as one that passed." ||
		len(parsed.EstablishedBy) != 1 || parsed.EstablishedBy[0] != "yoyodyne-1m3" || len(parsed.Revisions) != 1 || parsed.Revisions[0].By != domain.RoleArchitect {
		t.Fatalf("published invariant = %+v", parsed)
	}
}

// An amendment of an invariant that already exists lands the same way, as a
// new revision of the file it replaces, with everything it did not change kept.
func TestAnInvariantAmendmentFromTheArchitectsConversationLandsThroughIndependentReview(t *testing.T) {
	t.Parallel()

	repository, _, provider, pipeline, runs := automaticFixture(t)
	store := invariant.StoreFor(repository, pipeline.Config.Product)
	if _, err := store.Create(domain.RoleArchitect, invariant.Draft{ID: "checks-are-honest", Title: "Checks report what they did", Statement: "A check reports its own result.", Rationale: "The reviewer judges evidence.", EstablishedBy: []string{"yoyodyne-ifd.183"}, Scope: []string{"internal/checks"}, Reason: "recorded by the architect"}, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	publicationGit(t, repository, "add", ".")
	publicationGit(t, repository, "commit", "-m", "invariant")
	original := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleArchitect {
			return backend.RunResult{Backend: domain.BackendClaudeCode, SessionID: "author-session", FinalText: submittedInvariant("revise", "checks-are-honest", "A check that could not start is never recorded as one that passed.")}, nil
		}
		return original(request)
	}
	options, _ := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Amend the invariant with the ruling."); err != nil {
		t.Fatal(err)
	}
	if len(session.Writes()) != 0 {
		t.Fatal("an automatically confirmed amendment was put to the operator")
	}
	state, content := landedInvariant(t, repository, provider, runs, "docs/decisions/invariants/checks-are-honest.md")
	if state.Document.Candidate.Before == "" {
		t.Fatal("the amendment is not bound to the file it replaces")
	}
	parsed, err := invariant.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Statement != "A check that could not start is never recorded as one that passed." || parsed.Title != "Checks report what they did" ||
		len(parsed.Scope) != 1 || parsed.Scope[0] != "internal/checks" || len(parsed.Revisions) != 2 || parsed.Revisions[1].Action != invariant.ActionAmended || parsed.Revisions[1].By != domain.RoleArchitect {
		t.Fatalf("amended invariant = %+v", parsed)
	}
}

// No other role writes an invariant: a Lead Product Manager's conversation that
// tries is refused before anything is recorded, and no run is opened.
func TestAnotherRolesInvariantWriteIsRefused(t *testing.T) {
	t.Parallel()

	repository, _, provider, pipeline, runs := automaticFixture(t)
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{Backend: domain.BackendClaudeCode, SessionID: "author-session", FinalText: submittedInvariant("create", "not-hers-to-write", "Only the architect records a constraint.")}, nil
	}
	options, store := publicationConversation(t, pipeline, provider)
	options.Role, options.Agent = domain.RoleProductManager, "product-manager"
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := session.Send(context.Background(), "Record it as an invariant.")
	var refused *chat.AuthorityError
	if err != nil && !errors.As(err, &refused) {
		t.Fatal(err)
	}
	if len(reply.Writes) != 0 || len(session.Writes()) != 0 {
		t.Fatal("another role's invariant was recorded")
	}
	saved, loadErr := store.Load(runstate.ConversationIdentity{Role: domain.RoleProductManager, Agent: "product-manager"})
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(saved.PendingWrites) != 0 {
		t.Fatalf("waiting = %+v", saved.PendingWrites)
	}
	if !strings.Contains(saved.PendingTrackerResults, "architect") {
		t.Fatalf("the role was not told the invariant is the architect's: %q (error %v)", saved.PendingTrackerResults, err)
	}
	if records, err := runs.Recorded(); err != nil || len(records) != 0 {
		t.Fatalf("runs = %+v, %v", records, err)
	}
	if _, err := os.Stat(filepath.Join(repository, "docs", "decisions", "invariants", "not-hers-to-write.md")); !os.IsNotExist(err) {
		t.Fatalf("a refused invariant reached the checkout: %v", err)
	}
	// The store refuses it too, whoever asks.
	if err := artifact.StoreFor(repository, pipeline.Config.Product).CheckWrite(domain.RoleProductManager, artifact.Write{Action: artifact.WriteCreate, ID: "not-hers-to-write", Kind: artifact.KindInvariant, Title: "Not hers", Directory: "docs/decisions/invariants", EstablishedBy: []string{"yoyodyne-1m3"}, Body: "## Must hold\n\nx\n\n## Why\n\ny", Reason: "tried"}); !errors.Is(err, artifact.ErrUnauthorized) {
		t.Fatalf("CheckWrite = %v, want ErrUnauthorized", err)
	}
}
