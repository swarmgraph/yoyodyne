package orchestrator

// A run's developer and reviewer invocations each ask for their own agent's
// effort level, and the run's record, its outcome, and every cost line say what
// was asked.
//
// The level is validated in internal/config and put on the command line in the
// adapter. Neither says the wiring between them holds, so this replays a run
// whose first verdict sends the change back -- the repair is where a level lost
// between attempts would show -- and reads the level off the requests the fake
// provider was handed and off every record the run wrote.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/review"
)

type effortReportingBackend struct{ backend.Backend }

func (b effortReportingBackend) Run(ctx context.Context, request backend.RunRequest) (backend.RunResult, error) {
	result, err := b.Backend.Run(ctx, request)
	if request.Role == domain.RoleDeveloper {
		result.ResolvedEffort, result.EffortReported = "medium", true
	}
	return result, err
}

func TestARunAsksEachRoleForItsOwnEffortAndRecordsIt(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
		ID: "yoyodyne-task", Title: "Work", Status: "open", Labels: []string{"docs"},
	}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, repairVerdict, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Backend = effortReportingBackend{provider}
	// The item's label maps it onto another model, which moves the model and not
	// the level: the level is the developer agent's.
	pipeline.Config.Execution.DeveloperModels = []config.DeveloperModelRule{{Label: "docs", Model: "sonnet"}}
	for name, agent := range pipeline.Config.Agents {
		if agent.Role == domain.RoleDeveloper {
			agent.Effort = "high"
			pipeline.Config.Agents[name] = agent
		}
	}
	log := &recordingSpendLog{}
	pipeline.Spend = log
	pipeline.Reviewer = review.Reviewer{Backend: effortReportingBackend{provider}, Model: testReviewerModel, Effort: "xhigh", Spend: log}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	attempts := provider.RequestsForRole(domain.RoleDeveloper)
	if len(attempts) < 2 {
		t.Fatalf("the run made %d developer attempt(s), want the first and its repair", len(attempts))
	}
	for index, request := range attempts {
		if request.Effort != "high" || request.Model != "sonnet" {
			t.Fatalf("developer attempt %d asked for %q at %q, want sonnet at the developer agent's high", index, request.Model, request.Effort)
		}
	}
	reviews := provider.RequestsForRole(domain.RoleReviewer)
	if len(reviews) == 0 {
		t.Fatal("the run obtained no verdict")
	}
	for index, request := range reviews {
		if request.Effort != "xhigh" {
			t.Fatalf("verdict %d was asked at %q, want the reviewer agent's xhigh", index, request.Effort)
		}
	}

	if state.ProviderEffort != "high" || outcome.ProviderEffort != "high" {
		t.Fatalf("recorded developer effort = %q/%q, want high", state.ProviderEffort, outcome.ProviderEffort)
	}
	if state.ReviewEffort != "xhigh" || outcome.ReviewEffort != "xhigh" {
		t.Fatalf("recorded review effort = %q/%q, want xhigh", state.ReviewEffort, outcome.ReviewEffort)
	}
	if state.ProviderResolvedEffort != "medium" || !state.ProviderEffortReported || outcome.ProviderResolvedEffort != "medium" || !outcome.ProviderEffortReported {
		t.Fatalf("provider-reported effort was lost: state=%+v outcome=%+v", state, outcome)
	}
	if state.ReviewResolvedEffort != "" || state.ReviewEffortReported || outcome.ReviewResolvedEffort != "" || outcome.ReviewEffortReported {
		t.Fatal("an unreported review effort must not repeat the requested effort")
	}
	if len(log.lines) == 0 {
		t.Fatal("the run recorded no cost line")
	}
	for _, line := range log.lines {
		want := "high"
		if line.Role == domain.RoleReviewer {
			want = "xhigh"
		}
		if line.Effort != want {
			t.Fatalf("the %s cost line records effort %q, want %q", line.Role, line.Effort, want)
		}
		if line.EffortReported != (line.Role == domain.RoleDeveloper) {
			t.Fatalf("cost line lost effort reporting status: %+v", line)
		}
	}
}

// A developer agent that names no level asks for none, and nothing records one,
// which is exactly what every run did before the level was configurable.
func TestARunWhoseAgentsNameNoEffortAsksForNone(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Work", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, request := range provider.RequestsMade() {
		if request.Effort != "" {
			t.Fatalf("the %s was asked at %q, want no level where none is configured", request.Role, request.Effort)
		}
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.ProviderEffort != "" || state.ReviewEffort != "" {
		t.Fatalf("recorded %q/%q, want nothing recorded", state.ProviderEffort, state.ReviewEffort)
	}
}

// A branch review asks for the reviewer's level too, and its outcome and its
// durable record say what was asked.
func TestABranchReviewAsksForTheReviewersEffortAndRecordsIt(t *testing.T) {
	t.Parallel()

	repository := accumulatedRepository(t)
	provider := branchProvider(`{"decision":"approve","summary":"the three commits agree with one another"}`)
	reviewer, reviews, _ := newBranchReviewer(t, repository, provider)
	wired := reviewer.Reviewer.(review.Reviewer)
	wired.Effort = "max"
	reviewer.Reviewer = wired

	outcome, err := reviewer.Review(context.Background(), BranchReviewRequest{Branch: "milestone", BaseRef: "main"})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	requests := provider.RequestsForRole(domain.RoleReviewer)
	if len(requests) != 1 || requests[0].Effort != "max" {
		t.Fatalf("reviewer requests = %#v, want one at max", requests)
	}
	if outcome.Effort != "max" {
		t.Fatalf("outcome effort = %q, want max", outcome.Effort)
	}
	recorded, err := reviews.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 || recorded[0].Effort != "max" {
		t.Fatalf("recorded reviews = %#v, want one recording max", recorded)
	}
}

// A level settled at reservation is the run's for its whole life, an empty one
// included: an agent that named none when the run started does not start asking
// for one on the repair because somebody added it to the file mid-run.
func TestAnEffortEditedMidRunReachesNoRunInFlight(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Work", Status: "open"}}
	var agents map[string]config.AgentConfig
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		// The operator edits the level while the first attempt is under way.
		for name, agent := range agents {
			if agent.Role == domain.RoleDeveloper {
				agent.Effort = "high"
				agents[name] = agent
			}
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, repairVerdict, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	agents = pipeline.Config.Agents

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	attempts := provider.RequestsForRole(domain.RoleDeveloper)
	if len(attempts) < 2 {
		t.Fatalf("the run made %d developer attempt(s), want the first and its repair", len(attempts))
	}
	for index, request := range attempts {
		if request.Effort != "" {
			t.Fatalf("developer attempt %d asked at %q, want the level settled at reservation, which was none", index, request.Effort)
		}
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !state.EffortSettled || state.ProviderEffort != "" {
		t.Fatalf("recorded settled=%v effort=%q, want the empty level recorded as settled", state.EffortSettled, state.ProviderEffort)
	}
}

func TestCodexRunPinsAnExplicitDefaultThroughRepair(t *testing.T) {
	t.Parallel()
	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Work", Status: "open"}}
	var agents map[string]config.AgentConfig
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		for name, agent := range agents {
			if agent.Role == domain.RoleDeveloper {
				agent.Effort = "high"
				agents[name] = agent
			}
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, repairVerdict, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	for name, agent := range pipeline.Config.Agents {
		if agent.Role == domain.RoleDeveloper {
			agent.Backend, agent.Model, agent.Effort = domain.BackendCodex, "gpt-6.1-sol", ""
			pipeline.Config.Agents[name] = agent
		}
	}
	agents = pipeline.Config.Agents
	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	attempts := provider.RequestsForRole(domain.RoleDeveloper)
	if len(attempts) < 2 {
		t.Fatal("the test needs a first attempt and a repair")
	}
	for _, request := range attempts {
		if request.Model != "gpt-6.1-sol" || request.Effort != "low" {
			t.Fatalf("the configured default changed during a live run: %+v", request)
		}
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !state.EffortSettled || state.ProviderEffort != "low" || outcome.ProviderEffort != "low" {
		t.Fatalf("default was not recorded on the run: %+v", state)
	}
}
