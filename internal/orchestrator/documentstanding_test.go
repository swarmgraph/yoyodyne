package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A confirmed document waiting for a landing has not started: its owner may
// withdraw it, which takes it off the wait so the scheduler never starts it.
// Once a run of it is recorded and going it may not be withdrawn, and once
// that run has ended without landing it, it no longer holds its document's
// place.
func TestADocumentsStandingFollowsItsRunAndDecidesWhetherItMayBeWithdrawn(t *testing.T) {
	t.Parallel()

	fixture := waitingForALanding(t)
	waits := fixture.waits(t, fixture.store)
	if len(waits) != 1 {
		t.Fatalf("waits = %+v, want the design waiting", waits)
	}
	document := waits[0].Document
	standing, err := fixture.pipeline.DocumentStanding(document)
	if err != nil || standing.Started {
		t.Fatalf("DocumentStanding(waiting) = %+v, %v; want it not started", standing, err)
	}

	fixture.freeTheLanding(t)
	inFlight := runstate.State{SchemaVersion: runstate.StateSchemaVersion, RunID: document.RunID(), ProductID: fixture.pipeline.Config.Product.ID, RepositoryID: string(fixture.pipeline.Config.Product.RepositoryID), WorkItemID: document.RunID(), WorkItemTitle: "Publishing " + document.Candidate.Artifact.Title, Backend: domain.BackendClaudeCode, Status: runstate.StatusPending, Document: &document, StartedAt: newScheduleHarness().now, UpdatedAt: newScheduleHarness().now}
	lease, err := fixture.store.ReserveDocumentLanding(context.Background(), inFlight, 1)
	if err != nil {
		t.Fatal(err)
	}
	standing, err = fixture.pipeline.DocumentStanding(document)
	if err != nil || !standing.Running() || standing.RunID != document.RunID() {
		t.Fatalf("DocumentStanding(in flight) = %+v, %v; want it running", standing, err)
	}
	if err := fixture.pipeline.WithdrawDocument(document); err == nil || !strings.Contains(err.Error(), document.RunID()) {
		t.Fatalf("WithdrawDocument(in flight) = %v, want it refused naming the run", err)
	}

	inFlight.Status, inFlight.Failure = runstate.StatusFailed, "the publishing run ended in clean-up without reaching the target branch"
	completed := newScheduleHarness().now
	inFlight.CompletedAt = &completed
	if err := fixture.store.Save(inFlight); err != nil {
		t.Fatal(err)
	}
	lease.Release()
	standing, err = fixture.pipeline.DocumentStanding(document)
	if err != nil || !standing.HandedBack() || standing.Delivery.Passed {
		t.Fatalf("DocumentStanding(ended) = %+v, %v; want it handed back", standing, err)
	}
	if err := fixture.pipeline.WithdrawDocument(document); err != nil {
		t.Fatalf("WithdrawDocument(handed back) = %v, want it allowed", err)
	}
}

// The architect takes back her waiting design from her own conversation, and
// the scheduler is left nothing to start.
func TestAnOwnerWithdrawsItsWaitingDocumentFromItsConversation(t *testing.T) {
	t.Parallel()

	fixture := waitingForALanding(t)
	if waits := fixture.waits(t, fixture.store); len(waits) != 1 {
		t.Fatalf("waits = %+v, want the design waiting", waits)
	}
	writeID := fixture.waits(t, fixture.store)[0].Document.WriteID
	original := fixture.provider.Respond
	fixture.provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleArchitect && strings.Contains(request.Prompt, "Take the design back.") {
			return backend.RunResult{Backend: domain.BackendClaudeCode, SessionID: "author-session", FinalText: "Taking it back.\n```yoyodyne-artifact\n" + `{"withdraw":[{"write":"` + writeID + `","reason":"the design is being rethought"}]}` + "\n```\n"}, nil
		}
		return original(request)
	}
	if _, err := fixture.session.Send(context.Background(), "Take the design back."); err != nil {
		t.Fatal(err)
	}
	if waits := fixture.waits(t, fixture.store); len(waits) != 0 {
		t.Fatalf("waits = %+v, want the withdrawn design off the wait", waits)
	}
	saved, err := fixture.options.Store.Load(runstate.ConversationIdentity{Role: domain.RoleArchitect, Agent: "architect"})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.PendingWrites) != 0 {
		t.Fatalf("pending = %+v, want the design withdrawn", saved.PendingWrites)
	}
}

// A new revision of a design waiting for a landing replaces it under the
// automatic policy: the earlier revision is taken off the wait, so no queued
// start can publish it, and the one waiting is the new revision, confirmed in
// its own right.
func TestANewRevisionReplacesADesignWaitingForALandingUnderTheAutomaticPolicy(t *testing.T) {
	t.Parallel()

	fixture := waitingForALanding(t)
	waits := fixture.waits(t, fixture.store)
	if len(waits) != 1 {
		t.Fatalf("waits = %+v, want the design waiting", waits)
	}
	earlier := waits[0].Document.WriteID
	original := fixture.provider.Respond
	fixture.provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleArchitect && strings.Contains(request.Prompt, "Revise the design.") {
			return backend.RunResult{Backend: domain.BackendClaudeCode, SessionID: "author-session", FinalText: submittedDesign("recovery-design", "# Recovery\n\nThe owner's better words.")}, nil
		}
		return original(request)
	}
	if _, err := fixture.session.Send(context.Background(), "Revise the design."); err != nil {
		t.Fatal(err)
	}
	waits = fixture.waits(t, fixture.store)
	if len(waits) != 1 || waits[0].Document.WriteID == earlier || !strings.Contains(waits[0].Document.Candidate.Content, "better words") {
		t.Fatalf("waits = %+v, want only the new revision waiting", waits)
	}
	if approval, _ := waits[0].Document.Candidate.Artifact.LatestApproval(); approval.Policy == "" {
		t.Fatalf("the new revision carries no confirmation under the automatic policy: %+v", approval)
	}
	saved, err := fixture.options.Store.Load(runstate.ConversationIdentity{Role: domain.RoleArchitect, Agent: "architect"})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.PendingWrites) != 1 || saved.PendingWrites[0].ID == earlier {
		t.Fatalf("pending = %+v, want only the new revision", saved.PendingWrites)
	}
}
