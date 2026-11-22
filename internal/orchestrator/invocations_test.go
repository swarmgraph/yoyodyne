package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// recordsLikeAnAdapter makes the stand-in provider do what every adapter does
// with a request's recorder: record what it is about to send, and send nothing
// if that fails.
func recordsLikeAnAdapter(provider *orchestratortest.Backend) {
	inner := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		sent := backend.SentInvocation{Backend: "claude-code", Messages: []backend.SentMessage{
			{Role: backend.SentSystem, Parts: []backend.SentPart{{Name: "system instructions", Text: request.SystemPrompt}}},
			{Role: backend.SentUser, Parts: []backend.SentPart{{Name: "first message", Text: request.Prompt}}},
		}}
		if err := backend.RecordInvocation(request, sent); err != nil {
			return backend.RunResult{}, err
		}
		return inner(request)
	}
}

type unwritableInvocations struct{}

func (unwritableInvocations) Recorder(string, domain.AgentRole, string, func() time.Time) backend.InvocationRecorder {
	return refusingRecorder{}
}

type refusingRecorder struct{}

func (refusingRecorder) RecordInvocation(backend.SentInvocation) error {
	return errors.New("the state directory is full")
}

func withInvocations(pipeline Pipeline, log backend.InvocationLog) Pipeline {
	pipeline.Invocations = log
	reviewer := pipeline.Reviewer.(review.Reviewer)
	reviewer.Invocations = log
	pipeline.Reviewer = reviewer
	return pipeline
}

// Every developer attempt and every review of a run is kept before it is made:
// the first attempt, a repair that starts a new one, and each review.
func TestEveryDeveloperAndReviewerInvocationOfARunIsKept(t *testing.T) {
	t.Parallel()
	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Work", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, repairVerdict, approveVerdict)
	recordsLikeAnAdapter(provider)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline = withInvocations(pipeline, store.Invocations())

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	kept, err := store.Invocations().List(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	developers, reviewers := len(provider.RequestsForRole(domain.RoleDeveloper)), len(provider.RequestsForRole(domain.RoleReviewer))
	if developers < 2 || reviewers < 2 || len(kept) != developers+reviewers {
		t.Fatalf("%d developer and %d reviewer invocations made, %d kept", developers, reviewers, len(kept))
	}
	// What was kept is what each invocation's request carried.
	byRole := map[string][]string{}
	for _, entry := range kept {
		text, err := store.Invocations().Text(outcome.RunID, entry.Number)
		if err != nil {
			t.Fatalf("invocation %d: %v", entry.Number, err)
		}
		byRole[entry.Role] = append(byRole[entry.Role], text.Messages[len(text.Messages)-1].Text)
	}
	for index, request := range provider.RequestsForRole(domain.RoleDeveloper) {
		if byRole["developer"][index] != request.Prompt {
			t.Fatalf("developer invocation %d kept %q, was sent %q", index+1, byRole["developer"][index], request.Prompt)
		}
	}
	for index, request := range provider.RequestsForRole(domain.RoleReviewer) {
		if byRole["reviewer"][index] != request.Prompt {
			t.Fatalf("reviewer invocation %d kept a different message than it was sent", index+1)
		}
	}
}

// A developer invocation that cannot be recorded is never started, and the run
// stops on its own store without spending a repair attempt.
func TestADeveloperInvocationThatCannotBeRecordedIsNeverStarted(t *testing.T) {
	t.Parallel()
	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Work", Status: "open"}}
	started := false
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		started = true
		return nil
	}, approveVerdict)
	recordsLikeAnAdapter(provider)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline = withInvocations(pipeline, unwritableInvocations{})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil {
		t.Fatal("a run whose developer could not be recorded went on")
	}
	if started {
		t.Fatal("the developer was started although nothing was recorded")
	}
	state, loadErr := store.Load(outcome.RunID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if state.RecordedStopClass() != runstate.StopRecording || state.RepairAttempts != 0 {
		t.Fatalf("stop class %q, repair attempts %d; want a stop on the record and no repair spent", state.RecordedStopClass(), state.RepairAttempts)
	}
}
