package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Compatibility evidence in the developer's final account used to be absent
// from the review request even though the run returned it in its outcome.
func TestReviewReceivesTheDeveloperSummaryOfEachAttempt(t *testing.T) {
	t.Parallel()
	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	attempts := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempts++
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte(strings.Repeat("implemented\n", attempts)), 0o600)
	}, repairVerdict, approveVerdict)
	provider.DeveloperFinalTextByAttempt = []string{
		"Implemented the configuration. Compatibility with the previous build was checked before adding the key.",
		"Repaired the configuration. Compatibility with the previous build was checked again after the repair.",
	}
	respond := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		result, err := respond(request)
		if err != nil || request.Role != domain.RoleDeveloper {
			return result, err
		}
		payload := execution.ReplyPayload(result.FinalText)
		payload["role"] = request.Role
		event, err := execution.NewEvent(request.RunID, request.LastSequence+1, time.Now(), execution.EventAgentMessage, "fake", payload)
		if err != nil {
			return result, err
		}
		if err := request.EventSink(event); err != nil {
			return result, err
		}
		result.LastEvent = event.Sequence
		return result, nil
	}
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})
	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	reviews := provider.RequestsForRole(domain.RoleReviewer)
	if len(reviews) != 2 {
		t.Fatalf("reviews = %d, want the original and repaired change", len(reviews))
	}
	for i, request := range reviews {
		if !strings.Contains(request.Prompt, provider.DeveloperFinalTextByAttempt[i]) {
			t.Errorf("review %d lost its developer's compatibility account:\n%s", i, request.Prompt)
		}
		if strings.Contains(request.Prompt, provider.DeveloperFinalTextByAttempt[1-i]) {
			t.Errorf("review %d received the other attempt's summary", i)
		}
		if strings.Contains(request.Prompt, "yoyodyne-verification") {
			t.Errorf("review %d received the execution block as summary prose", i)
		}
		if !strings.Contains(request.Prompt, "# Check results") || !strings.Contains(request.Prompt, "test -f feature.txt") {
			t.Errorf("review %d lost the harness's check results", i)
		}
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.DeveloperSummary == nil || state.DeveloperSummary.Attempt != 1 || state.DeveloperSummary.Text != outcome.Summary {
		t.Fatalf("durable summary = %#v, want the repaired attempt's account %q", state.DeveloperSummary, outcome.Summary)
	}
	events, err := store.LoadEvents(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var recorded []string
	for _, event := range events {
		if event.Type != execution.EventAgentMessage {
			continue
		}
		var reply struct{ Text string }
		if err := json.Unmarshal(event.Payload, &reply); err != nil {
			t.Fatal(err)
		}
		for _, summary := range provider.DeveloperFinalTextByAttempt {
			if reply.Text == summary {
				recorded = append(recorded, summary)
			}
		}
	}
	if len(recorded) != 2 {
		t.Fatalf("the event log retained %d developer accounts, want both accounts the review must receive", len(recorded))
	}
}

func TestResumedReviewReceivesOnlyACurrentDeveloperSummary(t *testing.T) {
	for _, mode := range []string{"current", "missing", "earlier attempt", "changed content"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			repository, worktreeRoot, store := restartableFixture(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			first := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
			}, approveVerdict)
			first.DeveloperFinalText = "Implemented the configuration and checked compatibility with the previous build."
			first.Respond = stoppingReviewer(first.Respond, first.ReviewerSession)
			pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"exit 0"}), first)
			paused, err := pipeline.Run(context.Background(), tracker.Item.ID)
			if err != nil || !paused.Paused {
				t.Fatalf("Run() = %#v, %v, want a stopped reviewer", paused, err)
			}
			state, err := store.Load(paused.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if state.DeveloperSummary == nil {
				t.Fatal("the completed developer account was not saved before review")
			}
			switch mode {
			case "missing":
				// A record written before summaries were retained has no binding.
				state.DeveloperSummary = nil
			case "earlier attempt":
				state.RepairAttempts++
			case "changed content":
				if err := os.WriteFile(filepath.Join(state.WorktreePath, "feature.txt"), []byte("changed after the account\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.Save(state); err != nil {
				t.Fatal(err)
			}
			second := orchestratortest.RoleBackend(func(backend.RunRequest) error {
				t.Error("resuming a stopped review invoked the developer")
				return nil
			}, approveVerdict)
			second.Respond = stoppingReviewer(second.Respond, second.ReviewerSession)
			pipeline = automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second)
			if resumed, err := pipeline.Run(context.Background(), tracker.Item.ID); err != nil || !resumed.Paused {
				t.Fatalf("resumed Run() = %#v, %v, want a stopped reviewer", resumed, err)
			}
			reviews := second.RequestsForRole(domain.RoleReviewer)
			if len(reviews) != 1 {
				t.Fatalf("resumed reviews = %d, want one", len(reviews))
			}
			prompt := reviews[0].Prompt
			if mode == "current" {
				if !strings.Contains(prompt, first.DeveloperFinalText) {
					t.Fatal("resumption lost the summary for the unchanged candidate")
				}
			} else if strings.Contains(prompt, first.DeveloperFinalText) || !strings.Contains(prompt, "No developer final summary is available for this attempt and change.") {
				t.Fatalf("resumption presented a stale summary or concealed its absence:\n%s", prompt)
			}
		})
	}
}

func TestADeveloperInvocationClearsThePreviousSummaryBeforeItRuns(t *testing.T) {
	t.Parallel()
	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		state, err := store.Load(request.RunID)
		if err != nil {
			return err
		}
		if state.DeveloperSummary != nil {
			t.Errorf("a running invocation still carries the earlier summary: %#v", state.DeveloperSummary)
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, repairVerdict, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err != nil {
		t.Fatal(err)
	}
	if provider.DeveloperAttempts != 2 {
		t.Fatalf("developer attempts = %d, want a first invocation and a repair", provider.DeveloperAttempts)
	}
}

func TestReviewReceivesAVisiblyBoundedDeveloperSummary(t *testing.T) {
	t.Parallel()
	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	provider.DeveloperFinalText = "Implemented and checked compatibility. " + strings.Repeat("é", runstate.MaxRecordedTextBytes)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	want := runstate.RecordDeveloperSummary(provider.DeveloperFinalText)
	if state.DeveloperSummary == nil || state.DeveloperSummary.Text != want {
		t.Fatalf("recorded summary = %#v, want a bounded account", state.DeveloperSummary)
	}
	if !strings.Contains(provider.RequestsForRole(domain.RoleReviewer)[0].Prompt, want) {
		t.Fatal("review lost the summary's visible truncation marker")
	}
}
