package orchestrator

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// handedBackForAConflict is a run whose approved change the queue handed back
// because the target moved, put back for one of its attempts.
func handedBackForAConflict(t *testing.T) (*queueRun, *orchestratortest.Backend, Outcome, runstate.State) {
	t.Helper()
	r, provider := newCatchUpRun(t, resolveBothSides)
	admitted := r.run(t)
	if admitted.MergeQueue == nil {
		t.Fatalf("Run() = %#v, want the approved change admitted to the queue", admitted)
	}
	conflictOnTarget(t, r.repository, "something else\n")
	if pass := r.catchUpPass(t, admitted.MergeQueue.Key()); pass.Outcome != runstate.MergeQueuePassHandedBack {
		t.Fatalf("Pass() = %#v, want the conflict handed back", pass)
	}
	put, err := r.runs.Load(admitted.RunID)
	if err != nil {
		t.Fatal(err)
	}
	return r, provider, admitted, put
}

func continueRun(t *testing.T, r *queueRun, runID string) (Outcome, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return r.pipeline.Continue(ctx, "yoyodyne-task", runID)
}

func TestAHandedBackChangeIsResolvedInANewSessionGivenTheChangeAndTheConflict(t *testing.T) {
	t.Parallel()

	r, provider, admitted, put := handedBackForAConflict(t)
	if !put.AuthoredBy("developer-session") || len(put.AuthorSessions) != 1 || put.AuthorSessions[0].Work != runstate.AuthorWroteChange {
		t.Fatalf("author sessions = %#v, want the session that wrote the change recorded with the hand-back", put.AuthorSessions)
	}
	approvedHead, approval := put.HarnessCommit, put.ReviewSummary
	provider.DeveloperSession = "resolving-session"
	rejoined, err := continueRun(t, r, admitted.RunID)
	if err != nil || rejoined.MergeQueue == nil {
		t.Fatalf("Continue() = %#v, %v; want the resolved change back in the queue", rejoined, err)
	}
	developers := provider.RequestsForRole(domain.RoleDeveloper)
	if len(developers) != 2 || developers[1].SessionID != "" {
		t.Fatalf("developer requests = %d; want the second in a new session rather than %q", len(developers), developers[len(developers)-1].SessionID)
	}
	for _, want := range []string{
		"Add the feature", // the work item
		"keeping what the target branch does and what the change does",
		"Change nothing else, unless the resolved code would not otherwise build or pass its tests",
		"- feature.txt\n",
		"the target's own feature.txt", // the target's commit
		approvedHead,
		"+implemented", // the approved change's diff
		"in session reviewer-session",
		approval,
		"# The development manager's decision", // what every repair is given beside its failure
		"# The work item as it stands now",
	} {
		if want == "" || !strings.Contains(developers[1].Prompt, want) {
			t.Fatalf("the resolving session's prompt does not say %q:\n%s", want, developers[1].Prompt)
		}
	}
	state, err := r.runs.Load(admitted.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.ProviderSessionID != "resolving-session" || len(state.AuthorSessions) != 2 ||
		state.AuthorSessions[1].SessionID != "resolving-session" || state.AuthorSessions[1].Work != runstate.AuthorResolvedConflict ||
		state.AuthorSessions[1].Handback != state.ConflictHandbacks[0].ID {
		t.Fatalf("session %q, author sessions %#v; want both sessions recorded as authors", state.ProviderSessionID, state.AuthorSessions)
	}
	if pass := r.catchUpPass(t, rejoined.MergeQueue.Key()); pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want the resolved change landed", pass)
	}
	generations, err := r.queue.Generations(rejoined.MergeQueue.Key(), rejoined.MergeQueue.EntryID)
	if err != nil || len(generations) == 0 {
		t.Fatalf("Generations() = %v, %v", generations, err)
	}
	if last := generations[len(generations)-1]; last.AuthorSession != "resolving-session" || !slices.Equal(last.OtherAuthors, []string{"developer-session"}) {
		t.Fatalf("candidate authors = %q and %v, want both sessions", last.AuthorSession, last.OtherAuthors)
	}
}

func TestAResolvedChangeIsNotLandedOnAReviewByTheSessionThatWroteIt(t *testing.T) {
	t.Parallel()

	r, provider, admitted, _ := handedBackForAConflict(t)
	provider.DeveloperSession, provider.ReviewerSession = "resolving-session", "developer-session"
	outcome, err := continueRun(t, r, admitted.RunID)
	if err == nil || !strings.Contains(err.Error(), "wrote part of the change") || outcome.MergeQueue != nil && outcome.MergeQueue.EntryID != admitted.MergeQueue.EntryID {
		t.Fatalf("Continue() = %#v, %v; want the writer's session refused as the reviewer", outcome, err)
	}
}

// The queue's own review of the candidate is held to the same authors.
func TestAQueueCandidateIsNotLandedOnAReviewByTheSessionThatWroteTheChange(t *testing.T) {
	t.Parallel()

	r, provider, admitted, _ := handedBackForAConflict(t)
	provider.DeveloperSession = "resolving-session"
	rejoined, err := continueRun(t, r, admitted.RunID)
	if err != nil || rejoined.MergeQueue == nil {
		t.Fatalf("Continue() = %#v, %v", rejoined, err)
	}
	provider.ReviewerSession = "developer-session"
	if pass := r.catchUpPass(t, rejoined.MergeQueue.Key()); pass.Outcome == runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want nothing landed on the writer's review", pass)
	}
	if r.tracker.Closed {
		t.Fatal("the item was closed on a review by a session that wrote the change")
	}
}

func TestAnInterruptedAttemptResumesTheResolvingSessionAndNeverTheWriters(t *testing.T) {
	t.Parallel()

	r, provider, admitted, put := handedBackForAConflict(t)
	put.RecordResolvingSession("resolving-session", time.Now())
	put.ProviderSessionID = "resolving-session"
	if err := r.runs.Save(put); err != nil {
		t.Fatal(err)
	}
	provider.DeveloperSession = "resolving-session"
	if _, err := continueRun(t, r, admitted.RunID); err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if developers := provider.RequestsForRole(domain.RoleDeveloper); len(developers) != 2 || developers[1].SessionID != "resolving-session" {
		t.Fatalf("developer requests = %d, the last in %q; want the resolving session resumed", len(developers), developers[len(developers)-1].SessionID)
	}
}
