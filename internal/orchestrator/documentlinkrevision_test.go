package orchestrator

import (
	"context"
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
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// documentLinkClock is a conversation's clock the test moves by hand.
type documentLinkClock struct{ at time.Time }

func (c *documentLinkClock) Now() time.Time { return c.at }

func linkedDesignFile(revised time.Time) string {
	return fmt.Sprintf("---\nid: base-design\nkind: design\ntitle: Base design\nsupports:\n    - brief\nstatus: active\nrevisions:\n    - action: created\n      by: architect\n      at: %s\n      reason: the base design\n---\n# Base\n\nThe base design.\n", revised.UTC().Format(time.RFC3339))
}

func linkingDesign(id, body, reason string, action, extra string) string {
	return fmt.Sprintf("Written.\n%s\n{\"documents\":[{\"action\":%q,\"id\":%q,%s\"title\":\"Recovery design\",\"supports\":[\"base-design\"],\"body\":%q,\"reason\":%q}]}\n```", artifact.WriteFence, action, id, extra, body, reason)
}

// A confirmed document whose supported document was revised after the
// confirmation goes back to its author before any run is opened, naming the
// document and when it changed. The author's answer that it still agrees, the
// same text with a reason, is a later confirmation and is published.
func TestADocumentWhoseLinkedDocumentWasRevisedGoesBackToItsAuthorBeforeAnyRun(t *testing.T) {
	t.Parallel()

	repository, _, provider, pipeline, runs := automaticFixture(t)
	start := time.Now().UTC().Add(time.Minute).Truncate(time.Second)
	clock := &documentLinkClock{at: start}
	var reply string
	original := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleArchitect {
			return backend.RunResult{Backend: domain.BackendClaudeCode, SessionID: "author-session", FinalText: reply}, nil
		}
		return original(request)
	}
	// The supported document was revised an hour after the design is confirmed.
	revised := start.Add(time.Hour)
	if err := os.MkdirAll(filepath.Join(repository, "docs", "designs"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "docs", "designs", "base-design.md"), []byte(linkedDesignFile(revised)), 0o600); err != nil {
		t.Fatal(err)
	}
	publicationGit(t, repository, "add", ".")
	publicationGit(t, repository, "commit", "-m", "base design")
	options, store := publicationConversation(t, pipeline, provider)
	options.Clock = clock
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	body := "# Recovery\n\nThe owner's exact words, as the base design stood.\n"
	reply = linkingDesign("recovery-design", body, "drafted against the base design", "create", `"kind":"design","directory":"docs/designs",`)
	if _, err := session.Send(context.Background(), "Write the design."); err != nil {
		t.Fatal(err)
	}
	if records, err := runs.Recorded(); err != nil || len(records) != 0 {
		t.Fatalf("runs = %+v, %v, want none opened for a document whose linked document changed", records, err)
	}
	saved, err := store.Load(runstate.ConversationIdentity{Role: domain.RoleArchitect, Agent: "architect"})
	if err != nil {
		t.Fatal(err)
	}
	told := saved.PendingTrackerResults
	for _, want := range []string{"was not published and no run was opened", "base-design (docs/designs/base-design.md) was revised at " + revised.Local().Format("2006-01-02 15:04 MST"), "still agrees"} {
		if !strings.Contains(told, want) {
			t.Errorf("the architect was told %q, want it to say %q", told, want)
		}
	}
	if len(session.Writes()) != 0 || len(saved.HandedBack) != 1 {
		t.Fatalf("writes = %+v, handed back = %+v, want the document handed back to its author", session.Writes(), saved.HandedBack)
	}
	if _, err := os.Stat(filepath.Join(repository, "docs", "designs", "recovery-design.md")); !os.IsNotExist(err) {
		t.Fatalf("a document sent back reached the checkout: %v", err)
	}
	if saved.DocumentReturns["recovery-design"] != 0 {
		t.Fatalf("returns = %v, want none counted: no run judged the document", saved.DocumentReturns)
	}

	// The author says it still agrees: the same text again, with that as the reason.
	clock.at = start.Add(2 * time.Hour)
	reply = linkingDesign("recovery-design", body, "still agrees with base-design as revised", "create", `"kind":"design","directory":"docs/designs",`)
	if _, err := session.Send(context.Background(), "It still agrees."); err != nil {
		t.Fatal(err)
	}
	recorded, err := runs.Recorded()
	if err != nil || len(recorded) != 1 || recorded[0].Status != runstate.StatusSucceeded {
		after, _ := store.Load(runstate.ConversationIdentity{Role: domain.RoleArchitect, Agent: "architect"})
		t.Fatalf("told = %q; runs = %+v, %v, want the document published once the author said it still agrees", after.PendingTrackerResults, recorded, err)
	}
	content := publicationGit(t, repository, "show", "main:docs/designs/recovery-design.md")
	if !strings.Contains(content, "still agrees with base-design as revised") || !strings.Contains(content, "The owner's exact words") {
		t.Fatalf("published = %s", content)
	}
}

// A confirmed document whose linked documents have not changed is published as
// it always was, including one that links to a document by a Markdown link.
func TestADocumentWhoseLinkedDocumentsAreUnchangedIsPublishedAsBefore(t *testing.T) {
	t.Parallel()

	repository, _, provider, pipeline, runs := automaticFixture(t)
	start := time.Now().UTC().Add(time.Minute).Truncate(time.Second)
	original := provider.Respond
	reply := ""
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleArchitect {
			return backend.RunResult{Backend: domain.BackendClaudeCode, SessionID: "author-session", FinalText: reply}, nil
		}
		return original(request)
	}
	// Revised before the design is confirmed, so nothing it says is out of date.
	if err := os.MkdirAll(filepath.Join(repository, "docs", "designs"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "docs", "designs", "base-design.md"), []byte(linkedDesignFile(start.Add(-time.Hour))), 0o600); err != nil {
		t.Fatal(err)
	}
	publicationGit(t, repository, "add", ".")
	publicationGit(t, repository, "commit", "-m", "base design")
	options, _ := publicationConversation(t, pipeline, provider)
	options.Clock = &documentLinkClock{at: start}
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	reply = linkingDesign("recovery-design", "# Recovery\n\nSee [the base design](base-design.md).\n", "drafted", "create", `"kind":"design","directory":"docs/designs",`)
	if _, err := session.Send(context.Background(), "Write the design."); err != nil {
		t.Fatal(err)
	}
	recorded, err := runs.Recorded()
	if err != nil || len(recorded) != 1 || recorded[0].Status != runstate.StatusSucceeded {
		t.Fatalf("runs = %+v, %v, want one published run", recorded, err)
	}
}

// A retry the development manager decided is held by no conversation, so where a
// document it links to was revised after it was confirmed, its author's
// conversation is told directly, no run is opened, and the wait is cleared.
func TestARetriedDocumentWhoseLinkedDocumentWasRevisedIsPutToItsAuthor(t *testing.T) {
	t.Parallel()

	repository, _, provider, pipeline, runs := automaticFixture(t)
	start := time.Now().UTC().Add(time.Minute).Truncate(time.Second)
	if err := os.MkdirAll(filepath.Join(repository, "docs", "designs"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "docs", "designs", "base-design.md"), []byte(linkedDesignFile(start.Add(time.Hour))), 0o600); err != nil {
		t.Fatal(err)
	}
	publicationGit(t, repository, "add", ".")
	publicationGit(t, repository, "commit", "-m", "base design")
	options, store := publicationConversation(t, pipeline, provider)
	if _, err := chat.Open(options); err != nil {
		t.Fatal(err)
	}
	identity := runstate.ConversationIdentity{Role: domain.RoleArchitect, Agent: "architect"}
	conversation, err := store.Load(identity)
	if err != nil {
		t.Fatal(err)
	}
	write := artifact.Write{Action: artifact.WriteCreate, ID: "recovery-design", Kind: artifact.KindDesign, Title: "Recovery design", Directory: "docs/designs", Supports: []string{"base-design"}, Body: "# Recovery\n\nWritten against the base design.\n", Reason: "drafted by the architect"}
	candidate, automatic, err := artifact.StoreFor(repository, pipeline.Config.Product).PrepareConfirmation(domain.RoleArchitect, write, options.DocumentPolicy, "confirmed for the test", start)
	if err != nil || !automatic {
		t.Fatalf("confirmation = %v, automatic %v", err, automatic)
	}
	original := runstate.DocumentPublication{Candidate: candidate, ConversationID: conversation.ConversationID, WriteID: "document-1.1", Turn: 1, Owner: domain.RoleArchitect, AuthorBackend: string(domain.BackendClaudeCode), AuthorModel: "opus"}
	stopped, err := runstate.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	document := original.RetryOf(stopped, runstate.DocumentRetried{FromRun: stopped, Reason: "the time limit was the cause", DecidedBy: "the development manager", DecidedAt: start})
	pipeline.Authors = store
	delivery, err := pipeline.PublishDocument(context.Background(), document)
	if err != nil {
		t.Fatal(err)
	}
	if records, err := runs.Recorded(); err != nil || len(records) != 0 {
		t.Fatalf("runs = %+v, %v, want none opened", records, err)
	}
	saved, err := store.Load(identity)
	if err != nil {
		t.Fatal(err)
	}
	for _, told := range []string{delivery.Detail, saved.PendingTrackerResults} {
		for _, want := range []string{"retried by the development manager", "base-design (docs/designs/base-design.md) was revised at " + start.Add(time.Hour).Local().Format("2006-01-02 15:04 MST"), "still agrees"} {
			if !strings.Contains(told, want) {
				t.Errorf("told %q, want it to say %q", told, want)
			}
		}
	}

	// Where nobody can be told, the document is not dropped: the refusal is an
	// error and the wait stays for the scheduler to try again.
	pipeline.Authors = nil
	if _, err := pipeline.PublishDocument(context.Background(), document); err == nil {
		t.Fatal("a retry whose author cannot be told was accepted")
	}
}
