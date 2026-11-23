package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// earlierEntry is the merge queue entry the earlier copy waits as.
const earlierEntry = "mqe-0123456789abcdef0123456789abcdef"

// earlierQueuedCopy records a run, from another conversation, that published
// a copy of the recovery design, passed, and waits in the merge queue as entry
// earlierEntry with pull request #2158.
func earlierQueuedCopy(t *testing.T, repository string, pipeline Pipeline, store *runstate.Store) runstate.State {
	t.Helper()
	candidate, automatic, err := artifact.StoreFor(repository, pipeline.Config.Product).PrepareConfirmation(domain.RoleArchitect, artifact.Write{Action: artifact.WriteCreate, ID: "recovery-design", Kind: artifact.KindDesign, Title: "Recovery design", Directory: "docs/designs", Body: "# Recovery\n\nThe earlier copy.", Reason: "written by the architect"}, artifact.Policy{Designs: domain.ApprovalAutomatic}, "confirmed by harness", time.Now())
	if err != nil || !automatic {
		t.Fatalf("PrepareConfirmation() = %v, %v", automatic, err)
	}
	earlier := runstate.DocumentPublication{ConversationID: "chat-earlier", WriteID: "document-1.1", Turn: 1, Owner: domain.RoleArchitect, AuthorBackend: string(domain.BackendClaudeCode), AuthorModel: "opus", Candidate: candidate}
	now := newScheduleHarness().now
	admitted := now.Add(time.Minute)
	state := runstate.State{SchemaVersion: runstate.StateSchemaVersion, RunID: earlier.RunID(), ProductID: pipeline.Config.Product.ID, RepositoryID: string(pipeline.Config.Product.RepositoryID), WorkItemID: earlier.RunID(), WorkItemTitle: "Publishing Recovery design", Backend: domain.BackendClaudeCode, Status: runstate.StatusSucceeded, Phase: runstate.PhaseCleaningUp, Document: &earlier, StartedAt: now, UpdatedAt: admitted, CompletedAt: &admitted,
		MergeQueue:  &runstate.MergeQueueAdmissionRecord{EntryID: earlierEntry, Order: 1, Repository: "yoyodyne", TargetBranch: "main", Mode: runstate.MergeQueueHarness, ApprovedHead: strings.Repeat("a", 40), AdmittedAt: admitted},
		PullRequest: &runstate.PullRequest{Remote: "origin", Branch: "yoyodyne/earlier", Number: 2158, URL: "https://example.invalid/pull/2158", HeadCommit: strings.Repeat("a", 40)}}
	pending := state
	pending.Status, pending.Phase, pending.CompletedAt, pending.MergeQueue, pending.PullRequest, pending.UpdatedAt = runstate.StatusPending, "", nil, nil, nil, now
	lease, err := store.ReserveDocumentLanding(context.Background(), pending, pipeline.Config.Execution.DocumentLandingLimit())
	if err != nil {
		t.Fatalf("ReserveDocumentLanding() the earlier copy: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(state); err != nil {
		t.Fatalf("Save() the earlier queued copy: %v", err)
	}
	return state
}

// heldBack fails unless the conversation's saved record holds one write, not
// confirmed, held back behind the copy of its document ahead of it and so
// waiting for nobody's decision.
func heldBack(t *testing.T, conversations *runstate.ConversationStore, identity runstate.ConversationIdentity, earlierWrite string) {
	t.Helper()
	saved, err := conversations.Load(identity)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.PendingWrites) != 1 {
		t.Fatalf("saved writes = %+v, want the revision", saved.PendingWrites)
	}
	write := saved.PendingWrites[0]
	if write.Publication != nil || write.AwaitsDecision() || !strings.Contains(write.WaitsBehind, earlierWrite) {
		t.Fatalf("saved write = %+v, want it held back behind %s and waiting for no decision", write, earlierWrite)
	}
}

// documentRunsOf is every run recorded for the recovery design except skip.
func documentRunsOf(t *testing.T, store *runstate.Store, skip string) []runstate.State {
	t.Helper()
	recorded, err := store.Recorded()
	if err != nil {
		t.Fatal(err)
	}
	var runs []runstate.State
	for _, state := range recorded {
		if state.Document != nil && state.Document.Candidate.Artifact.ID == "recovery-design" && state.RunID != skip {
			runs = append(runs, state)
		}
	}
	return runs
}

// A revision of the recovery design written while an earlier copy of it, from
// another conversation, waits in the merge queue is not confirmed and no run
// is started for it: the architect is told what it waits for, in those words,
// and so is her list of waiting documents. Once the queue hands the earlier
// copy back, her next message confirms the revision and publishes it once,
// through one reviewed run.
func TestARevisionWrittenWhileAnEarlierCopyWaitsInTheMergeQueueWaitsAndIsPublishedOnceItIsHandedBack(t *testing.T) {
	t.Parallel()

	repository, _, provider, pipeline, store := documentPipeline(t)
	pipeline.Config.Execution.DocumentLandings = 2
	earlier := earlierQueuedCopy(t, repository, pipeline, store)
	options, conversations := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Write the design."); err != nil {
		t.Fatal(err)
	}
	if runs := documentRunsOf(t, store, earlier.RunID); len(runs) != 0 {
		t.Fatalf("runs = %+v, want no second publication started while the earlier copy waits in the merge queue", runs)
	}
	if len(provider.RequestsForRole(domain.RoleReviewer)) != 0 {
		t.Fatal("a review was spent on a revision whose earlier copy still waits in the merge queue")
	}
	// The revision is recorded and not confirmed, and it waits for the copy
	// ahead of it rather than for anybody's approval, so the operator is not
	// shown it as a decision.
	if writes := session.Writes(); len(writes) != 0 {
		t.Fatalf("writes = %+v, want nothing put to the operator while the revision waits behind the earlier copy", writes)
	}
	heldBack(t, conversations, runstate.ConversationIdentity{Agent: "architect", Role: domain.RoleArchitect}, "document-1.1")

	if _, err := session.Send(context.Background(), "What is waiting?"); err != nil {
		t.Fatal(err)
	}
	architect := provider.RequestsForRole(domain.RoleArchitect)
	told := architect[len(architect)-1].Prompt
	for _, want := range []string{"an earlier copy of this document, document-1.1, is waiting in the merge queue as entry " + earlierEntry + " (pull request #2158)", "not published until that copy lands or is handed back"} {
		if !strings.Contains(told, want) {
			t.Fatalf("the architect was not told %q:\n%s", want, told)
		}
	}
	if runs := documentRunsOf(t, store, earlier.RunID); len(runs) != 0 {
		t.Fatalf("runs = %+v, want still no second publication", runs)
	}

	handedBack := earlier.CompletedAt.Add(time.Minute)
	if _, err := store.Update(context.Background(), earlier.RunID, runstate.MergeQueueEntryLost{Blocker: "the merge queue handed the earlier copy back", Failure: "its group check failed", At: handedBack}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Carry on."); err != nil {
		t.Fatal(err)
	}
	runs := documentRunsOf(t, store, earlier.RunID)
	if len(runs) != 1 || runs[0].Status != runstate.StatusSucceeded || runs[0].Integration == nil {
		t.Fatalf("runs = %+v, want the waiting revision published once the earlier copy was handed back", runs)
	}
	if len(provider.RequestsForRole(domain.RoleReviewer)) != 1 {
		t.Fatalf("reviews = %d, want exactly one", len(provider.RequestsForRole(domain.RoleReviewer)))
	}
	if _, err := session.Send(context.Background(), "Anything else?"); err != nil {
		t.Fatal(err)
	}
	if runs := documentRunsOf(t, store, earlier.RunID); len(runs) != 1 {
		t.Fatalf("runs = %+v, want the revision published once and not again", runs)
	}
}

// A revision written after the earlier copy has landed is published at once:
// nothing is still publishing, so nothing holds it. The earlier copy here lands
// through its own reviewed run, and the revision is written over what landed.
func TestARevisionWrittenAfterTheEarlierCopyLandsIsPublishedAtOnce(t *testing.T) {
	t.Parallel()

	repository, _, _, pipeline, store := documentPipeline(t)
	pipeline.Config.Execution.DocumentLandings = 2
	documents := artifact.StoreFor(repository, pipeline.Config.Product)
	first, _, err := documents.PrepareConfirmation(domain.RoleArchitect, artifact.Write{Action: artifact.WriteCreate, ID: "recovery-design", Kind: artifact.KindDesign, Title: "Recovery design", Directory: "docs/designs", Body: "# Recovery\n\nThe earlier copy.", Reason: "written by the architect"}, artifact.Policy{Designs: domain.ApprovalAutomatic}, "confirmed by harness", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	earlier := runstate.DocumentPublication{ConversationID: "chat-earlier", WriteID: "document-1.1", Turn: 1, Owner: domain.RoleArchitect, AuthorBackend: string(domain.BackendClaudeCode), AuthorModel: "opus", Candidate: first}
	if _, err := pipeline.PublishWaitingDocument(context.Background(), earlier); err != nil {
		t.Fatalf("PublishWaitingDocument(earlier) = %v", err)
	}
	if landed, err := store.Load(earlier.RunID()); err != nil || landed.Status != runstate.StatusSucceeded || landed.Integration == nil || landed.PublishingCopy() {
		t.Fatalf("earlier copy = %+v, %v; want it landed and no longer publishing", landed, err)
	}

	second, _, err := documents.PrepareConfirmation(domain.RoleArchitect, artifact.Write{Action: artifact.WriteRevise, ID: "recovery-design", Body: "# Recovery\n\nThe newer copy.", Reason: "revised by the architect"}, artifact.Policy{Designs: domain.ApprovalAutomatic}, "confirmed by harness", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	newer := runstate.DocumentPublication{ConversationID: "chat-newer", WriteID: "document-2.1", Turn: 2, Owner: domain.RoleArchitect, AuthorBackend: string(domain.BackendClaudeCode), AuthorModel: "opus", Candidate: second}
	if _, err := pipeline.PublishWaitingDocument(context.Background(), newer); err != nil {
		t.Fatalf("PublishWaitingDocument(newer) = %v, want it published at once", err)
	}
	if published, err := store.Load(newer.RunID()); err != nil || published.Status != runstate.StatusSucceeded || published.Integration == nil {
		t.Fatalf("newer copy = %+v, %v; want it published at once", published, err)
	}
}

// A handoff already confirmed — here one the scheduler starts — is held by
// the pipeline itself while another copy of its document waits in the merge
// queue: no run starts, the scheduler is told it was declined with what it
// waits for, it stays waiting where the scheduler finds it, and the read model
// says what it waits behind. Once the earlier copy is handed back the
// scheduler's next start publishes it.
func TestAConfirmedHandoffWaitsBehindAnEarlierQueuedCopyAndTheSchedulerStartsItOnceThatCopyIsHandedBack(t *testing.T) {
	t.Parallel()

	repository, _, _, pipeline, store := documentPipeline(t)
	pipeline.Config.Execution.DocumentLandings = 2
	earlier := earlierQueuedCopy(t, repository, pipeline, store)
	candidate, automatic, err := artifact.StoreFor(repository, pipeline.Config.Product).PrepareConfirmation(domain.RoleArchitect, artifact.Write{Action: artifact.WriteCreate, ID: "recovery-design", Kind: artifact.KindDesign, Title: "Recovery design", Directory: "docs/designs", Body: "# Recovery\n\nThe newer copy.", Reason: "written by the architect"}, artifact.Policy{Designs: domain.ApprovalAutomatic}, "confirmed by harness", time.Now())
	if err != nil || !automatic {
		t.Fatalf("PrepareConfirmation() = %v, %v", automatic, err)
	}
	newer := runstate.DocumentPublication{ConversationID: "chat-newer", WriteID: "document-2.1", Turn: 2, Owner: domain.RoleArchitect, AuthorBackend: string(domain.BackendClaudeCode), AuthorModel: "opus", Candidate: candidate}

	outcome, err := pipeline.PublishWaitingDocument(context.Background(), newer)
	var behind DocumentBehindError
	if !errors.As(err, &behind) || behind.Ahead.RunID != earlier.RunID || !behind.Ahead.Queued || behind.Ahead.PullRequest != 2158 {
		t.Fatalf("PublishWaitingDocument() = %+v, %v; want it declined behind the earlier queued copy", outcome, err)
	}
	if outcome.RunID != "" {
		t.Fatalf("outcome = %+v, want no run named", outcome)
	}
	started := Started{}
	started.record(completed{outcome: outcome, err: err, document: true})
	if started.Failure != "" || !strings.Contains(started.Declined, "waiting in the merge queue") {
		t.Fatalf("schedule entry = %+v, want it declined saying what it waits for", started)
	}
	if runs := documentRunsOf(t, store, earlier.RunID); len(runs) != 0 {
		t.Fatalf("runs = %+v, want no second publication", runs)
	}
	waits, err := store.DocumentWaits()
	if err != nil || len(waits) != 1 || waits[0].Document.WriteID != "document-2.1" {
		t.Fatalf("waits = %+v, %v; want the newer copy kept waiting for the scheduler", waits, err)
	}
	documents := waitingDocumentsOver{store: store}
	if ahead, found, err := documents.PublishingAhead(newer); err != nil || !found || ahead.RunID != earlier.RunID {
		t.Fatalf("PublishingAhead() = %+v, %v, %v; want the earlier copy named", ahead, found, err)
	}

	if _, err := store.Update(context.Background(), earlier.RunID, runstate.MergeQueueEntryLost{Blocker: "the merge queue handed the earlier copy back", Failure: "its group check failed", At: earlier.CompletedAt.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := documents.PublishingAhead(newer); err != nil || found {
		t.Fatalf("PublishingAhead() after the hand-back = %v, %v; want nothing ahead", found, err)
	}
	if _, err := pipeline.PublishWaitingDocument(context.Background(), newer); err != nil {
		t.Fatalf("PublishWaitingDocument() after the hand-back = %v", err)
	}
	runs := documentRunsOf(t, store, earlier.RunID)
	if len(runs) != 1 || runs[0].Status != runstate.StatusSucceeded || runs[0].Integration == nil {
		t.Fatalf("runs = %+v, want the newer copy published once", runs)
	}
	if waits, err := store.DocumentWaits(); err != nil || len(waits) != 0 {
		t.Fatalf("waits = %+v, %v; want nothing left waiting", waits, err)
	}
}

// waitingDocumentsOver is the scheduler's view of the waiting documents over a
// real run store, as the command line wires it.
type waitingDocumentsOver struct {
	store *runstate.Store
}

func (d waitingDocumentsOver) DocumentWaits() ([]runstate.DocumentWait, error) {
	return d.store.DocumentWaits()
}

func (d waitingDocumentsOver) Publish(context.Context, runstate.DocumentPublication) (Outcome, error) {
	return Outcome{}, errors.New("not started in this test")
}

func (d waitingDocumentsOver) PublishingAhead(document runstate.DocumentPublication) (runstate.DocumentAhead, bool, error) {
	runs, err := d.store.DocumentRuns(document.Candidate.Artifact.ID)
	if err != nil {
		return runstate.DocumentAhead{}, false, err
	}
	ahead, found := runstate.PublishingAhead(document, runs)
	return ahead, found, nil
}

// A pull passes over a waiting document whose earlier copy is still in the
// merge queue, saying what it waits for, and starts it once nothing is ahead.
func TestAPullPassesOverAWaitingDocumentWhoseEarlierCopyIsStillInTheMergeQueue(t *testing.T) {
	t.Parallel()

	repository, _, _, pipeline, store := documentPipeline(t)
	earlier := earlierQueuedCopy(t, repository, pipeline, store)
	wait := waitingDesign("document-2.1")
	candidate, _, err := artifact.StoreFor(repository, pipeline.Config.Product).PrepareConfirmation(domain.RoleArchitect, artifact.Write{Action: artifact.WriteCreate, ID: "recovery-design", Kind: artifact.KindDesign, Title: "Recovery design", Directory: "docs/designs", Body: "# Recovery\n\nThe newer copy.", Reason: "written by the architect"}, artifact.Policy{Designs: domain.ApprovalAutomatic}, "confirmed by harness", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	wait.Document.Candidate, wait.Document.AuthorBackend, wait.Document.AuthorModel = candidate, string(domain.BackendClaudeCode), "opus"
	if _, _, err := store.WaitForSlot(context.Background(), wait.Document, 0, wait.Since); err != nil {
		t.Fatal(err)
	}

	scheduler := Scheduler{Now: func() time.Time { return newScheduleHarness().now }}
	pull := Pull{Documents: waitingDocumentsOver{store: store}, DocumentLandings: 2}
	var passed []string
	starting, err := scheduler.nextDocuments(pull, map[string]runstate.State{}, map[string]int{}, map[string]refusedDocument{}, 0, func(_, reason string) { passed = append(passed, reason) })
	if err != nil || len(starting) != 0 {
		t.Fatalf("nextDocuments() = %+v, %v; want nothing started", starting, err)
	}
	if len(passed) != 1 || !strings.Contains(passed[0], "waiting in the merge queue as entry "+earlierEntry+"") {
		t.Fatalf("passed over = %q, want the reason naming the earlier copy's queue entry", passed)
	}

	if _, err := store.Update(context.Background(), earlier.RunID, runstate.MergeQueueEntryLost{Blocker: "handed back", Failure: "its group check failed", At: earlier.CompletedAt.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	starting, err = scheduler.nextDocuments(pull, map[string]runstate.State{}, map[string]int{}, map[string]refusedDocument{}, 0, func(string, string) {})
	if err != nil || len(starting) != 1 {
		t.Fatalf("nextDocuments() after the hand-back = %+v, %v; want the document started", starting, err)
	}
}
