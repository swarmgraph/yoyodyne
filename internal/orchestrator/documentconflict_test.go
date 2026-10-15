package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// changedBeforeCut changes the target branch once, just before a run cuts its
// checkout from it, which is a later revision of the document landing after the
// text being published was written.
type changedBeforeCut struct {
	WorktreeManager
	change func()
}

func (w *changedBeforeCut) Create(ctx context.Context, request gitworktree.CreateRequest) (gitworktree.Worktree, error) {
	if w.change != nil {
		change := w.change
		w.change = nil
		change()
	}
	return w.WorktreeManager.Create(ctx, request)
}

const contestedDesign = "contested-design"

// contestedFixture is a design already on main, and an architect whose first
// message revises it. It returns the commit that made main's copy, which is
// the copy the revision is written against.
func contestedFixture(t *testing.T) (string, *orchestratortest.Backend, Pipeline, *runstate.Store, string) {
	t.Helper()
	repository, _, provider, pipeline, runs := automaticFixture(t)
	store := artifact.StoreFor(repository, pipeline.Config.Product)
	if _, err := store.Create(domain.RoleArchitect, artifact.Draft{ID: contestedDesign, Kind: artifact.KindDesign, Title: "Contested design", Directory: "docs/designs", Body: "# The first decision", Reason: "drafted by the architect"}, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	runPipelineGit(t, repository, "add", ".")
	runPipelineGit(t, repository, "commit", "-m", "the first copy of the design")
	written := strings.TrimSpace(publicationGit(t, repository, "rev-parse", "main"))
	original := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleArchitect {
			if len(provider.RequestsForRole(domain.RoleArchitect)) > 1 {
				return backend.RunResult{SessionID: "author", FinalText: "Noted."}, nil
			}
			return backend.RunResult{SessionID: "author", FinalText: fmt.Sprintf("Revised.\n%s\n{\"documents\":[{\"action\":\"revise\",\"id\":%q,\"body\":\"# The revision this conversation wrote\",\"reason\":\"revised by the architect\"}]}\n```", artifact.WriteFence, contestedDesign)}, nil
		}
		return original(request)
	}
	return repository, provider, pipeline, runs, written
}

// landLaterRevision commits a later revision of the design onto main, confirmed
// by the operator where confirmed is set.
func landLaterRevision(t *testing.T, repository string, pipeline Pipeline, confirmed bool) {
	t.Helper()
	store := artifact.StoreFor(repository, pipeline.Config.Product)
	body := "# Her later whole rewrite"
	later := time.Now().Add(time.Hour)
	if _, err := store.Amend(domain.RoleArchitect, contestedDesign, artifact.Amendment{Body: &body, Reason: "rewritten whole by the architect"}, later); err != nil {
		t.Fatal(err)
	}
	if confirmed {
		if _, err := store.Approve(contestedDesign, "approved by the operator", later); err != nil {
			t.Fatal(err)
		}
	}
	runPipelineGit(t, repository, "add", ".")
	runPipelineGit(t, repository, "commit", "-m", "a later revision of the design")
}

// A run that finds main's copy of its document changed since the text was
// written stops once, keeps a record that says what conflicted, and goes to the
// development manager: nothing starts the same text again on its own.
func TestADocumentWhoseCopyOnMainChangedIsStoppedOnceAndHeldForTheDevelopmentManager(t *testing.T) {
	repository, provider, pipeline, runs, written := contestedFixture(t)
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	pipeline.Docket = docketerOverStore(docket, runs, pipeline.Config)
	pipeline.Worktrees = &changedBeforeCut{WorktreeManager: pipeline.Worktrees, change: func() { landLaterRevision(t, repository, pipeline, false) }}
	options, conversations := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Revise the design."); err != nil {
		t.Fatal(err)
	}
	now := strings.TrimSpace(publicationGit(t, repository, "rev-parse", "main"))
	recorded := documentRuns(t, runs)
	if len(recorded) != 1 {
		t.Fatalf("runs = %d, want the one run that met the changed copy", len(recorded))
	}
	stopped := recorded[0]
	if err := stopped.Validate(); err != nil {
		t.Fatalf("the stopped run's record is not valid: %v", err)
	}
	conflict := stopped.DocumentConflict
	if conflict == nil || conflict.Path != "docs/designs/contested-design.md" || conflict.WrittenAgainst != written || conflict.TargetCommit != now || conflict.TargetBranch != "main" || conflict.RecordedAt.IsZero() {
		t.Fatalf("conflict = %+v, want the document, the commit %s it was written against and main's commit %s", conflict, written, now)
	}
	for _, want := range []string{"docs/designs/contested-design.md", written, now} {
		if !strings.Contains(stopped.Failure, want) {
			t.Errorf("the stop says %q, want it to name %s", stopped.Failure, want)
		}
	}
	if strings.Contains(stopped.Failure, "not its evidence") {
		t.Fatalf("the stop lost its evidence: %s", stopped.Failure)
	}
	if stopped.DocumentRetry == nil || !stopped.DocumentRetry.HandedOver || !strings.Contains(stopped.Blocker, written) {
		t.Fatalf("retry = %+v, blocker %q; want the run handed to the development manager naming the conflict", stopped.DocumentRetry, stopped.Blocker)
	}
	built, err := pipeline.Docket.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Entries) != 1 || built.Entries[0].RunID != stopped.RunID || built.Entries[0].Document == nil || !strings.Contains(built.Entries[0].Document.Stopped, now) {
		t.Fatalf("docket = %+v, want the stopped run naming main's commit", built.Entries)
	}
	saved, err := conversations.Load(runstate.ConversationIdentity{Role: domain.RoleArchitect, Agent: "architect"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(saved.PendingTrackerResults, "handed to the development manager") || !strings.Contains(saved.PendingTrackerResults, "does not start it again") {
		t.Fatalf("the architect was told %q", saved.PendingTrackerResults)
	}

	// Nothing offers it again: not the conversation, and not the scheduler.
	for i := 0; i < runstate.MaxDocumentAttempts; i++ {
		if err := session.PublishDocuments(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if again := documentRuns(t, runs); len(again) != 1 {
		t.Fatalf("runs = %d after the conversation's later messages, want no run started again", len(again))
	}
	if waits, err := runs.DocumentWaits(); err != nil || len(waits) != 0 {
		t.Fatalf("waits = %+v, %v, want nothing waiting to start it", waits, err)
	}
	if resumable, _, err := pipeline.ResumableDocuments(stopped.Document.ConversationID); err != nil || len(resumable) != 0 {
		t.Fatalf("resumable = %+v, %v", resumable, err)
	}

	// Her retry is allowed, because main's later copy was never confirmed, and
	// the retried run stops on the same copy once and is not started again.
	retried, err := DocumentDecider{Runs: runs, Copies: pipeline.Worktrees}.RetryDocument(context.Background(), stopped.RunID, developmentManagersDecision)
	if err != nil {
		t.Fatalf("retry refused: %v", err)
	}
	waits, err := runs.DocumentWaits()
	if err != nil || len(waits) != 1 {
		t.Fatalf("waits = %+v, %v (%s)", waits, err, retried)
	}
	if _, err := pipeline.PublishWaitingDocument(context.Background(), waits[0].Document); err != nil {
		t.Fatal(err)
	}
	fresh := runByID(t, runs, waits[0].RunID())
	if fresh.DocumentConflict == nil || fresh.DocumentRetry == nil || !fresh.DocumentRetry.HandedOver {
		t.Fatalf("retried run = %+v, want it stopped on the conflict and handed over", fresh)
	}
	if waits, err := runs.DocumentWaits(); err != nil || len(waits) != 0 {
		t.Fatalf("waits = %+v, %v, want no second attempt of the retry", waits, err)
	}
	if again := documentRuns(t, runs); len(again) != 2 {
		t.Fatalf("runs = %d, want the original and one retried run", len(again))
	}
}

// A retry of a stopped publication is refused before any run starts when main
// already carries a later confirmed revision of the same document, naming it.
func TestRetryingADocumentIsRefusedWhenMainCarriesALaterConfirmedRevision(t *testing.T) {
	repository, provider, pipeline, runs, _ := contestedFixture(t)
	pipeline.Worktrees = &changedBeforeCut{WorktreeManager: pipeline.Worktrees, change: func() { landLaterRevision(t, repository, pipeline, true) }}
	options, _ := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Revise the design."); err != nil {
		t.Fatal(err)
	}
	recorded := documentRuns(t, runs)
	if len(recorded) != 1 {
		t.Fatalf("runs = %d", len(recorded))
	}
	stopped := recorded[0]
	standing, err := artifact.ParseDocument([]byte(publicationGit(t, repository, "show", "main:docs/designs/contested-design.md")))
	if err != nil {
		t.Fatal(err)
	}
	approval, _ := standing.LatestApproval()
	_, err = DocumentDecider{Runs: runs, Copies: pipeline.Worktrees}.RetryDocument(context.Background(), stopped.RunID, developmentManagersDecision)
	if err == nil {
		t.Fatal("a retry of a text main has a later confirmed revision of was recorded")
	}
	for _, want := range []string{"later confirmed revision", fmt.Sprintf("revision %d", approval.Revision), approval.At.Local().Format("2006-01-02 15:04 MST"), "nothing was recorded"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not say %q", err, want)
		}
	}
	if after := runByID(t, runs, stopped.RunID); after.DocumentDecision != nil {
		t.Fatalf("decision = %+v, want none recorded", after.DocumentDecision)
	}
	if waits, err := runs.DocumentWaits(); err != nil || len(waits) != 0 {
		t.Fatalf("waits = %+v, %v, want no run waiting to start", waits, err)
	}
}

// A revision whose copy on main is the one it was written against lands as it
// always has, with no conflict recorded.
func TestADocumentWhoseCopyOnMainIsUnchangedIsPublished(t *testing.T) {
	repository, provider, pipeline, runs, _ := contestedFixture(t)
	options, _ := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Revise the design."); err != nil {
		t.Fatal(err)
	}
	recorded := documentRuns(t, runs)
	if len(recorded) != 1 || recorded[0].Status != runstate.StatusSucceeded || recorded[0].Integration == nil || recorded[0].DocumentConflict != nil {
		t.Fatalf("runs = %+v, want the revision landed", recorded)
	}
	if content := publicationGit(t, repository, "show", "main:docs/designs/contested-design.md"); !strings.Contains(content, "# The revision this conversation wrote") {
		t.Fatal(content)
	}
}
