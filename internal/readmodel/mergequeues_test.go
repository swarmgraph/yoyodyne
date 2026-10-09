package readmodel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A change is shown as landed only once the queue confirmed it on the target:
// an approved change waiting its turn, a candidate that passed its gate, and a
// merge asked of the target whose outcome nothing established are each shown
// as what they are, and every line names the work by identifier and title.
func TestTheQueueIsShownWithoutReadingApprovalAsALanding(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewMergeQueueStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	key := runstate.MergeQueueKey{Repository: "yoyodyne", TargetBranch: "main"}
	admitted := time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)
	admit := func(title string, head byte) runstate.MergeQueueEntry {
		runID, err := runstate.NewRunID()
		if err != nil {
			t.Fatal(err)
		}
		entry, _, err := store.Admit(context.Background(), runstate.MergeQueueAdmission{
			Key: key, WorkItemID: "yoyodyne-ifd.9", WorkItemTitle: title, RunID: runID,
			ApprovedHead: strings.Repeat(string(head), 40), IntegrationPolicy: "automatic", Mode: runstate.MergeQueueHarness,
			ModeEvidence: runstate.MergeQueueModeEvidence{ObservedAt: admitted, Explanation: "The harness runs the queue itself."}, At: admitted,
		})
		if err != nil {
			t.Fatal(err)
		}
		return entry
	}
	waiting := admit("Waiting its turn", 'a')
	verified := admit("Passed its gate", 'b')
	asked := admit("Merge asked for", 'c')

	lease, held, err := store.LeaseWorker(context.Background(), key)
	if err != nil || !held {
		t.Fatalf("LeaseWorker() = %t, %v", held, err)
	}
	defer lease.Release()
	for _, entry := range []runstate.MergeQueueEntry{verified, asked} {
		generation := verifiedQueueGeneration(entry)
		if err := store.RecordGeneration(lease, key, generation); err != nil {
			t.Fatal(err)
		}
	}
	// The third entry's local move was asked for and its answer never written.
	generation := verifiedQueueGeneration(asked)
	attempt := runstate.MergeQueuePromotionAttempt{
		Number: 1, Path: runstate.MergeQueueLandLocally, Generation: generation.Number, Binding: generation.Binding(),
		TargetBase: generation.TargetBase, Candidate: generation.Candidate, IntendedAt: admitted,
	}
	attempt.Key = runstate.MergeQueuePromotionKey(asked.EntryID, attempt.Number, attempt.Path, attempt.Binding, attempt.Candidate)
	attempt.Mutations = []runstate.MergeQueueMutationRecord{{
		Mutation: runstate.MergeQueueMoveTarget, Key: attempt.MutationKey(runstate.MergeQueueMoveTarget),
		Commit: attempt.Candidate, Expected: attempt.TargetBase, RequestedAt: admitted,
	}}
	landing := runstate.NewMergeQueueLanding(asked)
	landing.Attempts = []runstate.MergeQueuePromotionAttempt{attempt}
	if err := store.RecordLanding(lease, key, landing); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordRefusal(context.Background(), key, runstate.MergeQueueRefusal{
		At: admitted, WorkItemID: "yoyodyne-ifd.9", RunID: waiting.RunID,
		Requirement: "main lands changes only through the forge's merge queue.", Step: "remove the merge queue requirement from main's protection rules", PersonOnly: true,
	}); err != nil {
		t.Fatal(err)
	}

	queues, problem := ReadMergeQueues(store)
	if problem != "" || len(queues) != 1 {
		t.Fatalf("ReadMergeQueues() = %#v, %q", queues, problem)
	}
	states := map[string]QueueEntryState{}
	for _, entry := range queues[0].Entries {
		states[entry.Title] = entry.State
	}
	want := map[string]QueueEntryState{"Waiting its turn": QueueEntryWaiting, "Passed its gate": QueueEntryVerified, "Merge asked for": QueueEntryUncertain}
	for title, state := range want {
		if states[title] != state {
			t.Fatalf("%q is shown as %q, want %q (states %v)", title, states[title], state, states)
		}
	}
	rendered := Standing{MergeQueues: queues}.RenderMergeQueues()
	for _, line := range []string{"yoyodyne-ifd.9 Waiting its turn", "Passed its gate — verified", "uncertain", "0 finished", "not in use: main lands changes only through the forge's merge queue."} {
		if !strings.Contains(rendered, line) {
			t.Fatalf("rendered queues lack %q:\n%s", line, rendered)
		}
	}
	if strings.Contains(rendered, "landed") {
		t.Fatalf("nothing has landed, and the queue says something has:\n%s", rendered)
	}
}

// verifiedQueueGeneration is a generation of an entry that passed every part
// of its gate.
func verifiedQueueGeneration(entry runstate.MergeQueueEntry) runstate.MergeQueueGeneration {
	at := time.Date(2026, 10, 9, 9, 5, 0, 0, time.UTC)
	generation := runstate.MergeQueueGeneration{
		Number: 1, EntryID: entry.EntryID, EntryOrder: entry.Order, TargetBranch: entry.TargetBranch,
		TargetBase: strings.Repeat("d", 40), Heads: []string{entry.ApprovedHead}, Candidate: strings.Repeat("e", 39) + entry.ApprovedHead[:1],
		Content: strings.Repeat("f", 40), Checks: runstate.NewMergeQueueCheckConfiguration([]string{"./check.sh"}),
		AuthorSession: "developer-session", CreatedAt: at,
	}
	binding := generation.Binding()
	generation.Paths = &runstate.MergeQueuePathEvidence{Binding: binding, CheckedAt: at, Changed: 1}
	generation.CheckRun = &runstate.MergeQueueCheckEvidence{Binding: binding, StartedAt: at, FinishedAt: &at,
		Results: []runstate.MergeQueueCheckResult{{Command: "./check.sh", Passed: true, Status: "succeeded"}}}
	generation.Review = &runstate.MergeQueueReviewEvidence{Binding: binding, StartedAt: at, FinishedAt: &at,
		Decision: "approve", SessionID: "reviewer-session", Model: "reviewer-model"}
	return generation
}
