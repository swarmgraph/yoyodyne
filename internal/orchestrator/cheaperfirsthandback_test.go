package orchestrator

// A run that started on the cheaper model and is handed back by the merge
// queue does not repair in its own repair loop: the hand-back stops it, and the
// development manager's repair decision continues it (RepairContinuer). That
// continued repair moves to the stronger model exactly as a repair the loop
// starts does, and the run's record says which model each attempt used. The
// same holds for a round answering changes a person asked for on the run's
// pull request, which publicationtests drives through its own fixture
// (TestARequestedChangeOnARunThatStartedCheaperIsRepairedOnTheStrongerModel).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestAMergeQueueHandBackOfARunThatStartedCheaperIsRepairedOnTheStrongerModel(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Add the feature", Status: "open", Priority: 2}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})
	pipeline.Config.Execution.DeveloperCheaperFirst = config.DeveloperCheaperFirst{Enabled: true}
	pipeline.Config.Execution.MergeQueue = true
	queue, err := runstate.NewMergeQueueStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	pipeline.MergeQueue = queue

	// The first attempt is approved on the cheaper model and admitted to the
	// queue, so the run's own loop never moved it.
	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || outcome.MergeQueue == nil {
		t.Fatalf("Run() = %#v, %v; want the approved change admitted to the queue", outcome, err)
	}
	admitted, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if record := admitted.CheaperFirst; record == nil || record.Arm != runstate.CheaperFirstCheaper || record.Move != nil {
		t.Fatalf("cheaper-first record = %+v, want a run started cheaper and not yet moved", record)
	}

	// The queue's candidate fails a check, and the queue hands the change back
	// to the run as a repair.
	entries, err := queue.Entries(outcome.MergeQueue.Key())
	if err != nil || len(entries) != 1 {
		t.Fatalf("queue entries = %#v, %v; want the run's one entry", entries, err)
	}
	entry := entries[0]
	docket := &memoryDocket{}
	handback := runstate.MergeQueueHandback{
		At: docketedNow, Class: runstate.MergeQueueCandidateDefect, Continuation: runstate.MergeQueueContinueRepair,
		Mover: ownership.MoverHarness, Reason: "make race failed on generation 1 of the queue's candidate",
		Generation: 1, Candidate: entry.ApprovedHead, Heads: []string{entry.ApprovedHead},
		Branch: admitted.Branch, ApprovedHead: entry.ApprovedHead,
		RepairAttempts: admitted.RepairAttempts, RepairBudget: admitted.RepairBudget(pipeline.Config.Execution.RepairAttemptsBeforeReplan),
	}
	handing := MergeQueueRunHandback{Store: store, Tracker: tracker, Docket: docketerOverStore(docket, store, pipeline.Config)}
	if err := handing.HandBack(context.Background(), entry, handback); err != nil {
		t.Fatalf("HandBack() error = %v", err)
	}
	stopped, err := store.Load(outcome.RunID)
	if err != nil || stopped.CheckFailure == nil || stopped.CheckFailure.Command != "make race" || stopped.Status != runstate.StatusFailed {
		t.Fatalf("handed-back run = %+v, %v; want it stopped on the failing check", stopped.CheckFailure, err)
	}

	// The development manager decides a repair, and the continuation carries it
	// out on the same run.
	if _, err := store.Triage().GrantRepair(context.Background(), tracker.Item.ID, triageDecided(runstate.TriageDecisionRepair, outcome.RunID), 2, docketedNow, handbackCaps); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	result, err := repairContinuerOver(t, pipeline, store, docket, tracker).
		Continue(context.Background(), RepairContinueRequest{Run: outcome.RunID})
	if err != nil || !result.Continued || result.Outcome.RunID != outcome.RunID {
		t.Fatalf("Continue() = %#v, %v; want the same run continued", result, err)
	}

	attempts := provider.RequestsForRole(domain.RoleDeveloper)
	if len(attempts) != 2 || attempts[0].Model != "sonnet" || attempts[1].Model != "opus" {
		models := make([]string, 0, len(attempts))
		for _, attempt := range attempts {
			models = append(models, attempt.Model)
		}
		t.Fatalf("developer attempts asked for %v, want the cheaper sonnet first and the repair on the stronger opus", models)
	}
	repaired, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	assertMovedForHandedBackRepair(t, repaired, "make race failed and the run was handed back for repair")
}

// assertMovedForHandedBackRepair is what a run that started cheaper owes the
// record after one handed-back repair: one move, for repair attempt 1, naming
// what was handed back, and the model of both attempts.
func assertMovedForHandedBackRepair(t *testing.T, state runstate.State, reason string) {
	t.Helper()
	record := state.CheaperFirst
	if record == nil || record.Move == nil {
		t.Fatalf("cheaper-first record = %+v, want the move to the stronger model recorded", record)
	}
	move := record.Move
	if move.From != "sonnet" || move.To != "opus" || move.RepairAttempt != 1 || !strings.Contains(move.Reason, reason) {
		t.Fatalf("move = %+v, want sonnet to opus for repair attempt 1 because %s", move, reason)
	}
	if state.DeveloperModel != "opus" || !strings.Contains(state.DeveloperModelReason, "moved once to the stronger model opus") {
		t.Fatalf("developer model = %q (%q), want opus and the move said", state.DeveloperModel, state.DeveloperModelReason)
	}
	want := []runstate.AttemptModel{{Attempt: 0, Model: "sonnet"}, {Attempt: 1, Model: "opus"}}
	if len(record.Attempts) != len(want) {
		t.Fatalf("attempt models = %+v, want %+v", record.Attempts, want)
	}
	for index, attempt := range record.Attempts {
		if attempt.Attempt != want[index].Attempt || attempt.Model != want[index].Model || attempt.At.IsZero() {
			t.Fatalf("attempt models = %+v, want %+v, each with its time", record.Attempts, want)
		}
	}
}
