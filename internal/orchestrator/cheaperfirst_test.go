package orchestrator

// A developer run over an eligible item starts on the cheaper model and moves
// once to the stronger after its first failed review; a priority 0 item, or
// one carrying a high risk label, never starts cheaper. Which arm of the trial
// a run takes, and the stop on a defect after landing, are read off the run
// records and tested in internal/runstate; what is tested here is that the
// model the provider is actually asked for follows the record.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func cheaperFirstPipeline(t *testing.T, item beads.WorkItem) (Pipeline, *runstate.Store, *orchestratortest.Tracker, *orchestratortest.Backend) {
	t.Helper()
	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: item}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, repairVerdict, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Config.Execution.DeveloperCheaperFirst = config.DeveloperCheaperFirst{Enabled: true}
	return pipeline, store, tracker, provider
}

func TestAnEligibleRunStartsCheaperAndMovesOnceToTheStrongerAfterAFailedReview(t *testing.T) {
	t.Parallel()

	pipeline, store, tracker, provider := cheaperFirstPipeline(t, beads.WorkItem{
		ID: "yoyodyne-task", Title: "Work", Status: "open", Priority: 2, Labels: []string{"docs"},
	})
	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	record := state.CheaperFirst
	if record == nil || record.Arm != runstate.CheaperFirstCheaper || record.Ordinal != 1 {
		t.Fatalf("cheaper-first record = %+v, want the first run of the trial, started cheaper", record)
	}
	if record.StartModel != "sonnet" || record.StrongerModel != "opus" {
		t.Fatalf("record names start %q and stronger %q, want sonnet and opus", record.StartModel, record.StrongerModel)
	}
	if record.FirstReview != "repair" {
		t.Fatalf("first review = %q, want repair", record.FirstReview)
	}
	move := record.Move
	if move == nil || move.From != "sonnet" || move.To != "opus" || move.RepairAttempt != 1 ||
		!strings.Contains(move.Reason, "independent review asked for repair") {
		t.Fatalf("move = %+v, want sonnet to opus for repair attempt 1 after the review", move)
	}
	if state.DeveloperModel != "opus" || !strings.Contains(state.DeveloperModelReason, "moved once to the stronger model opus") {
		t.Fatalf("developer model = %q (%q), want opus and the move said", state.DeveloperModel, state.DeveloperModelReason)
	}

	attempts := provider.RequestsForRole(domain.RoleDeveloper)
	if len(attempts) < 2 {
		t.Fatalf("the run made %d developer attempt(s), want the first and its repair", len(attempts))
	}
	if attempts[0].Model != "sonnet" {
		t.Fatalf("the first attempt asked for %q, want the cheaper sonnet", attempts[0].Model)
	}
	for index, request := range attempts[1:] {
		if request.Model != "opus" {
			t.Fatalf("attempt %d after the move asked for %q, want the stronger opus", index+2, request.Model)
		}
	}
	// The reviewer is never moved: the setting chooses the developer's model.
	for index, request := range provider.RequestsForRole(domain.RoleReviewer) {
		if request.Model != testReviewerModel {
			t.Fatalf("review %d was asked of %q, want the configured reviewer's %q", index, request.Model, testReviewerModel)
		}
	}
}

func TestPriorityZeroAndHighRiskWorkNeverStartsOnTheCheaperModel(t *testing.T) {
	t.Parallel()

	for _, replay := range []struct {
		name     string
		priority int
		labels   []string
		added    []string
	}{
		{name: "priority 0", priority: 0, labels: []string{"docs"}},
		{name: "security", priority: 2, labels: []string{"security"}},
		{name: "migration", priority: 2, labels: []string{"migration"}},
		{name: "concurrency", priority: 3, labels: []string{"concurrency"}},
		{name: "public interface", priority: 1, labels: []string{"public-interface"}},
		{name: "a label the project added", priority: 2, labels: []string{"billing"}, added: []string{"billing"}},
	} {
		t.Run(replay.name, func(t *testing.T) {
			t.Parallel()

			pipeline, store, tracker, provider := cheaperFirstPipeline(t, beads.WorkItem{
				ID: "yoyodyne-task", Title: "Work", Status: "open", Priority: replay.priority, Labels: replay.labels,
			})
			pipeline.Config.Execution.HighRiskLabelsAdded = replay.added
			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			state, err := store.Load(outcome.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if state.CheaperFirst != nil {
				t.Fatalf("cheaper-first record = %+v, want none on work that is never eligible", state.CheaperFirst)
			}
			for index, request := range provider.RequestsForRole(domain.RoleDeveloper) {
				if request.Model != testDeveloperModel {
					t.Fatalf("attempt %d asked for %q, want the configured %q", index+1, request.Model, testDeveloperModel)
				}
			}
		})
	}
}

// A run in the trial's stronger arm starts on the stronger model and never
// moves, so the comparison sees the same work done the way it always was.
func TestTheTrialsSecondRunStartsOnTheStrongerModelAndDoesNotMove(t *testing.T) {
	t.Parallel()

	pipeline, store, tracker, provider := cheaperFirstPipeline(t, beads.WorkItem{
		ID: "yoyodyne-task", Title: "Work", Status: "open", Priority: 2,
	})
	at := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
	earlier := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion, RunID: "run-00000000000000000000000000000001",
		ProductID: pipeline.Config.Product.ID, RepositoryID: string(pipeline.Config.Product.RepositoryID),
		WorkItemID: "yoyodyne-other", Backend: domain.BackendClaudeCode, Status: runstate.StatusFailed,
		StartedAt: at, UpdatedAt: at, CompletedAt: &at,
		CheaperFirst: &runstate.CheaperFirst{Arm: runstate.CheaperFirstCheaper, Ordinal: 1, Reason: "earlier"},
	}
	if err := store.Create(earlier); err != nil {
		t.Fatalf("Save(earlier run) error = %v", err)
	}
	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if record := state.CheaperFirst; record == nil || record.Arm != runstate.CheaperFirstStronger || record.Ordinal != 2 || record.Move != nil {
		t.Fatalf("cheaper-first record = %+v, want run 2 of the trial, started stronger, never moved", record)
	}
	for index, request := range provider.RequestsForRole(domain.RoleDeveloper) {
		if request.Model != "opus" {
			t.Fatalf("attempt %d asked for %q, want the stronger opus", index+1, request.Model)
		}
	}
}
