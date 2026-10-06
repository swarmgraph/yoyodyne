package orchestrator

// A run's record says what each of its invocations was given beside its prompt
// -- skills, plugins, and instruction files, by name and source -- and says
// "none" where that was nothing. The adapter works out what was loaded; this
// replays a run and reads it back off the record the run wrote.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/review"
)

var namedSkill = backend.LoadedItem{Name: "careful-review", Source: backend.LoadedFromProjectConfiguration, Path: "/repository/.yoyodyne/skills/review/SKILL.md"}

type loadedReportingBackend struct{ backend.Backend }

func (b loadedReportingBackend) Run(ctx context.Context, request backend.RunRequest) (backend.RunResult, error) {
	result, err := b.Backend.Run(ctx, request)
	if request.Role == domain.RoleReviewer {
		result.Loaded = backend.NewLoaded([]backend.LoadedItem{namedSkill}, nil, nil)
	} else {
		result.Loaded = backend.NewLoaded(nil, nil, nil)
	}
	return result, err
}

func TestARunRecordsWhatEachInvocationLoaded(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Work", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Backend = loadedReportingBackend{provider}
	pipeline.Reviewer = review.Reviewer{Backend: loadedReportingBackend{provider}, Model: testReviewerModel}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.ProviderLoaded == nil || state.ProviderLoaded.Summary != "skills: none; plugins: none; instruction files: none" {
		t.Fatalf("developer loaded = %+v, want none of each said", state.ProviderLoaded)
	}
	if state.ReviewLoaded == nil || len(state.ReviewLoaded.Skills) != 1 || state.ReviewLoaded.Skills[0] != namedSkill {
		t.Fatalf("review loaded = %+v", state.ReviewLoaded)
	}
	if want := "skills: careful-review (project configuration, /repository/.yoyodyne/skills/review/SKILL.md); plugins: none; instruction files: none"; state.ReviewLoaded.Summary != want {
		t.Fatalf("review summary = %q, want %q", state.ReviewLoaded.Summary, want)
	}
}
