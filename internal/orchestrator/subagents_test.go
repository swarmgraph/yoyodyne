package orchestrator

// A developer run asks for sub-agents only where its configuration turned them
// on and the endpoint serving it offers them; it settles the setting on its
// record before its first launch and keeps it through a repair; it records
// what each model did as the provider reported it; and one on a provider
// without sub-agents runs without them and says so.

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
)

// modelUsageReportingBackend reports the developer's usage split between the
// developer's model and its sub-agents', as Claude Code's terminal does, and
// answers the sub-agent launch check with notConfirmed, which is empty for an
// installed provider that confirms everything.
type modelUsageReportingBackend struct {
	backend.Backend
	notConfirmed []string
	checked      *[]backend.SubagentRequest
}

func (b modelUsageReportingBackend) CheckSubagentLaunch(_ context.Context, _, _ string, subagents backend.SubagentRequest) (backend.LaunchSettingsCheck, error) {
	if b.checked != nil {
		*b.checked = append(*b.checked, subagents)
	}
	return backend.LaunchSettingsCheck{Version: "2.2.0 (Claude Code)", NotInForce: b.notConfirmed}, nil
}

// uncheckableBackend is a provider whose adapter offers sub-agents and cannot
// be asked whether the installed provider applies them.
type uncheckableBackend struct{ backend.Backend }

var reportedModelUsage = []backend.ModelUsage{
	{Model: "claude-opus-5-5", CostUSD: 1.25, InputTokens: 10, OutputTokens: 20},
	{Model: "claude-sonnet-5-5", CostUSD: 0.25, InputTokens: 1, OutputTokens: 2},
}

func (b modelUsageReportingBackend) Run(ctx context.Context, request backend.RunRequest) (backend.RunResult, error) {
	result, err := b.Backend.Run(ctx, request)
	if request.Role == domain.RoleDeveloper {
		result.CostUSD, result.CostReported = 1.5, true
		result.ModelUsage, result.ModelUsageReported = reportedModelUsage, true
	}
	return result, err
}

func subagentRun(t *testing.T, configure func(*Pipeline, map[string]config.AgentConfig)) (*orchestratortest.Backend, *recordingSpendLog, Outcome, *Pipeline) {
	t.Helper()
	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Work", Status: "open"}}
	var agents map[string]config.AgentConfig
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if configure != nil {
			// What configure does with no pipeline is an edit made while an attempt
			// is under way.
			configure(nil, agents)
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, repairVerdict, approveVerdict)
	built, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline := &built
	log := &recordingSpendLog{}
	pipeline.Spend = log
	pipeline.Backend = modelUsageReportingBackend{Backend: provider}
	agents = pipeline.Config.Agents
	if configure != nil {
		configure(pipeline, agents)
	}
	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	return provider, log, outcome, pipeline
}

func withDeveloper(agents map[string]config.AgentConfig, change func(*config.AgentConfig)) {
	for name, agent := range agents {
		if agent.Role == domain.RoleDeveloper {
			change(&agent)
			agents[name] = agent
		}
	}
}

// With nothing configured no invocation asks for sub-agents and the run
// records nothing about them, exactly as before they existed.
func TestARunWithSubagentsOffAsksForNoneAndRecordsNothing(t *testing.T) {
	t.Parallel()

	provider, _, outcome, pipeline := subagentRun(t, nil)
	for _, request := range provider.RequestsMade() {
		if request.Subagents != nil {
			t.Fatalf("the %s was given sub-agents nobody configured: %+v", request.Role, request.Subagents)
		}
	}
	state, err := pipeline.Store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Subagents != nil {
		t.Fatalf("a run with sub-agents off recorded %+v", state.Subagents)
	}
}

// With sub-agents on, every developer attempt — the first and its repair —
// asks for them on the configured model and effort, the reviewer never does,
// and the run and every developer cost line record what each model did.
func TestARunWithSubagentsOnAsksForThemOnEveryAttemptAndRecordsEachModel(t *testing.T) {
	t.Parallel()

	provider, log, outcome, pipeline := subagentRun(t, func(pipeline *Pipeline, agents map[string]config.AgentConfig) {
		if pipeline == nil {
			return
		}
		withDeveloper(agents, func(agent *config.AgentConfig) {
			agent.Subagents = config.Subagents{Enabled: true, Effort: "medium"}
		})
	})
	attempts := provider.RequestsForRole(domain.RoleDeveloper)
	if len(attempts) < 2 {
		t.Fatalf("the run made %d developer attempt(s), want the first and its repair", len(attempts))
	}
	for index, request := range attempts {
		if request.Subagents == nil || *request.Subagents != (backend.SubagentRequest{Model: config.DefaultSubagentModel, Effort: "medium"}) {
			t.Fatalf("developer attempt %d asked for sub-agents %+v, want sonnet at medium", index, request.Subagents)
		}
	}
	for _, request := range provider.RequestsForRole(domain.RoleReviewer) {
		if request.Subagents != nil {
			t.Fatalf("the reviewer was given sub-agents: %+v", request.Subagents)
		}
	}
	state, err := pipeline.Store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Subagents == nil || !state.Subagents.Enabled || state.Subagents.Model != "sonnet" || state.Subagents.Unavailable != "" {
		t.Fatalf("recorded sub-agents %+v", state.Subagents)
	}
	if !reflect.DeepEqual(state.ProviderModelUsage, reportedModelUsage) {
		t.Fatalf("recorded model usage %+v", state.ProviderModelUsage)
	}
	says := state.SubagentsSays()
	for _, want := range []string{"sub-agents on sonnet at medium effort", "claude-opus-5-5 $1.25", "claude-sonnet-5-5 $0.25"} {
		if !strings.Contains(says, want) {
			t.Fatalf("the run says %q, want it to say %q", says, want)
		}
	}
	var developerLines int
	for _, line := range log.lines {
		if line.Role != domain.RoleDeveloper {
			continue
		}
		developerLines++
		if !reflect.DeepEqual(line.Models, reportedModelUsage) {
			t.Fatalf("a developer cost line does not carry what each model did: %+v", line)
		}
	}
	if developerLines < 2 {
		t.Fatalf("%d developer cost lines, want one per attempt", developerLines)
	}
}

// A run whose developer is on a provider that offers no sub-agents runs
// without them, and its record and status say why in plain words. The
// developer's failover is on Claude Code, which does offer them, so the
// configuration is not refused for a setting that could never take effect.
func TestARunOnAProviderWithoutSubagentsGoesWithoutThemAndSaysSo(t *testing.T) {
	t.Parallel()

	provider, _, outcome, pipeline := subagentRun(t, func(pipeline *Pipeline, agents map[string]config.AgentConfig) {
		if pipeline == nil {
			return
		}
		withDeveloper(agents, func(agent *config.AgentConfig) {
			agent.Backend, agent.Model = domain.BackendCodex, "gpt-6.1-sol"
			agent.Failover = config.Failover{Enabled: true, Provider: domain.BackendClaudeCode, Model: "opus"}
			agent.Subagents = config.Subagents{Enabled: true}
		})
	})
	for _, request := range provider.RequestsForRole(domain.RoleDeveloper) {
		if request.Subagents != nil {
			t.Fatalf("a Codex developer was asked for sub-agents: %+v", request.Subagents)
		}
	}
	state, err := pipeline.Store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Subagents == nil || state.Subagents.Unavailable != "Codex does not offer them" {
		t.Fatalf("recorded sub-agents %+v", state.Subagents)
	}
	if says := state.SubagentsSays(); !strings.Contains(says, "sub-agents off for this run: Codex does not offer them") {
		t.Fatalf("the run says %q", says)
	}
}

// Turning sub-agents on while a run is going reaches the next run, not this
// one: its repair runs without them, as its first attempt did.
func TestSubagentsTurnedOnMidRunReachNoRunInFlight(t *testing.T) {
	t.Parallel()

	provider, _, outcome, pipeline := subagentRun(t, func(pipeline *Pipeline, agents map[string]config.AgentConfig) {
		if pipeline != nil {
			return
		}
		withDeveloper(agents, func(agent *config.AgentConfig) {
			agent.Subagents = config.Subagents{Enabled: true}
		})
	})
	attempts := provider.RequestsForRole(domain.RoleDeveloper)
	if len(attempts) < 2 {
		t.Fatalf("the run made %d developer attempt(s), want the first and its repair", len(attempts))
	}
	for index, request := range attempts {
		if request.Subagents != nil {
			t.Fatalf("developer attempt %d was given sub-agents turned on after the run started", index)
		}
	}
	state, err := pipeline.Store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Subagents != nil {
		t.Fatalf("recorded %+v", state.Subagents)
	}
}

// Sub-agents are given only where the installed provider confirms it applies
// what they are launched with. Where it does not — a CLI upgraded to a version
// that dropped a flag or no longer loads the run's sub-agent — every attempt
// runs without them and the record says why in the check's own words; where
// the adapter cannot be asked at all, the same.
func TestARunGoesWithoutSubagentsTheInstalledProviderDoesNotConfirm(t *testing.T) {
	t.Parallel()

	on := func(agent *config.AgentConfig) { agent.Subagents = config.Subagents{Enabled: true} }
	var checked []backend.SubagentRequest
	provider, _, outcome, pipeline := subagentRun(t, func(pipeline *Pipeline, agents map[string]config.AgentConfig) {
		if pipeline == nil {
			return
		}
		withDeveloper(agents, on)
		pipeline.Backend = modelUsageReportingBackend{
			Backend:      pipeline.Backend.(modelUsageReportingBackend).Backend,
			notConfirmed: []string{"the flag --forward-subagent-text is not in its help (it was read from 2.1.286)"},
			checked:      &checked,
		}
	})
	attempts := provider.RequestsForRole(domain.RoleDeveloper)
	if len(attempts) < 2 || len(checked) != len(attempts) {
		t.Fatalf("%d developer attempts and %d checks, want a check before each attempt", len(attempts), len(checked))
	}
	for index, request := range attempts {
		if request.Subagents != nil {
			t.Fatalf("developer attempt %d was given sub-agents the installed provider did not confirm", index)
		}
	}
	state, err := pipeline.Store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	want := "sub-agents off for this run: the installed claude-code (2.2.0 (Claude Code)) did not confirm what sub-agents are launched with: the flag --forward-subagent-text is not in its help"
	if says := state.SubagentsSays(); !strings.Contains(says, want) {
		t.Fatalf("the run says %q, want %q", says, want)
	}

	provider, _, outcome, pipeline = subagentRun(t, func(pipeline *Pipeline, agents map[string]config.AgentConfig) {
		if pipeline == nil {
			return
		}
		withDeveloper(agents, on)
		pipeline.Backend = uncheckableBackend{pipeline.Backend}
	})
	for index, request := range provider.RequestsForRole(domain.RoleDeveloper) {
		if request.Subagents != nil {
			t.Fatalf("developer attempt %d was given sub-agents nothing could confirm", index)
		}
	}
	state, err = pipeline.Store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if says := state.SubagentsSays(); !strings.Contains(says, "nothing in this build can confirm that the installed claude-code applies what sub-agents are launched with") {
		t.Fatalf("the run says %q", says)
	}
}

// subagentLimitingBackend reports, on the first developer attempt, a usage
// limit on the sub-agents' model alone, as the Claude Code adapter does for a
// weekly Sonnet limit on a developer running on Opus.
type subagentLimitingBackend struct {
	modelUsageReportingBackend
	limit    backend.UsageLimit
	attempts *int
}

func (b subagentLimitingBackend) Run(ctx context.Context, request backend.RunRequest) (backend.RunResult, error) {
	result, err := b.modelUsageReportingBackend.Run(ctx, request)
	if request.Role == domain.RoleDeveloper {
		if *b.attempts == 0 {
			limit := b.limit
			result.SubagentLimit = &limit
		}
		*b.attempts++
	}
	return result, err
}

// A usage limit on the sub-agents' model alone does not stop the run, wait it
// out, or move it to another endpoint: the attempt that met it is taken as
// served, the repair after it runs on the same endpoint without sub-agents,
// and the record says why and until when.
func TestALimitOnTheSubagentsModelLeavesTheRunOnItsEndpointWithoutThem(t *testing.T) {
	t.Parallel()

	resets := time.Date(2030, 1, 2, 3, 4, 0, 0, time.UTC)
	attempts := 0
	provider, _, outcome, pipeline := subagentRun(t, func(pipeline *Pipeline, agents map[string]config.AgentConfig) {
		if pipeline == nil {
			return
		}
		withDeveloper(agents, func(agent *config.AgentConfig) { agent.Subagents = config.Subagents{Enabled: true} })
		pipeline.Backend = subagentLimitingBackend{
			modelUsageReportingBackend: pipeline.Backend.(modelUsageReportingBackend),
			limit:                      backend.UsageLimit{Kind: "seven_day_sonnet", ResetsAt: resets},
			attempts:                   &attempts,
		}
	})
	developer := provider.RequestsForRole(domain.RoleDeveloper)
	if len(developer) != 2 {
		t.Fatalf("%d developer attempts, want the first and its repair and no wait or relaunch between them", len(developer))
	}
	if developer[0].Subagents == nil || developer[1].Subagents != nil {
		t.Fatalf("sub-agents on the first attempt %+v and the repair %+v, want them on the first only", developer[0].Subagents, developer[1].Subagents)
	}
	if developer[0].Model != developer[1].Model || developer[0].AccountAlias != developer[1].AccountAlias {
		t.Fatalf("the repair moved from %s on %s to %s on %s", developer[0].Model, developer[0].AccountAlias, developer[1].Model, developer[1].AccountAlias)
	}
	state, err := pipeline.Store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Subagents == nil || state.Subagents.Limit == nil || state.Subagents.Limit.Kind != "seven_day_sonnet" || !state.Subagents.Limit.ResetsAt.Equal(resets) {
		t.Fatalf("recorded sub-agents %+v", state.Subagents)
	}
	want := "sub-agents off for this run: the sub-agents' model has reached the provider's Sonnet weekly limit, so the developer works without them until it resets at " + resets.Local().Format("Jan 2 15:04 MST")
	if says := state.SubagentsSays(); !strings.Contains(says, want) {
		t.Fatalf("the run says %q, want %q", says, want)
	}
	if state.UsageLimitResetsAt != nil || state.UsageLimitPausedSince != nil || state.UsageLimitPausedSeconds != 0 {
		t.Fatalf("the run waited on the sub-agents' limit: resets %v, paused since %v for %ds", state.UsageLimitResetsAt, state.UsageLimitPausedSince, state.UsageLimitPausedSeconds)
	}
}
