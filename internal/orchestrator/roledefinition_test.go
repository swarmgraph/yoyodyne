package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/rolecapability"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The developer invocation carries the tool access the developer agent is held
// to: its role's own, or read-only where the agent fills a role definition that
// removed the worktree write.
func TestTheDeveloperInvocationCarriesTheAgentsToolAccess(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name       string
		definition bool
		want       backend.Posture
	}{
		{name: "the shipped developer writes its worktree", want: backend.PostureWorktreeWrite},
		{name: "a definition without the worktree write is held read-only", definition: true, want: backend.PostureReadOnly},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			repository := pipelineRepository(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Work", Status: "open"}}
			provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
			}, approveVerdict)
			pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
			if testCase.definition {
				pipeline.Config.Agents = withNarrowedDeveloper(t, pipeline.Config.Agents)
			}

			if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			attempts := provider.RequestsForRole(domain.RoleDeveloper)
			if len(attempts) == 0 {
				t.Fatal("the run made no developer attempt")
			}
			for _, request := range attempts {
				if request.Posture != testCase.want {
					t.Fatalf("developer invocation posture = %q, want %q", request.Posture, testCase.want)
				}
			}
		})
	}
}

// withNarrowedDeveloper is the agents with the developer filling a role
// definition that removed the worktree write, as the configuration loader binds
// one that a person activated.
func withNarrowedDeveloper(t *testing.T, agents map[string]config.AgentConfig) map[string]config.AgentConfig {
	t.Helper()
	bundle, _ := rolecapability.MustDefault().Bundle(domain.RoleDeveloper)
	narrowed := make(map[string]config.AgentConfig, len(agents))
	found := false
	for name, agent := range agents {
		if agent.Role == domain.RoleDeveloper {
			agent.Capabilities = slices.DeleteFunc(slices.Clone(bundle.Holds), func(held capability.Capability) bool {
				return held == capability.WorktreeMutate
			})
			agent.Definition = &config.AgentDefinition{Name: "narrow", Digest: "digest", Source: "/project/.yoyodyne/roles/narrow.yaml"}
			found = true
		}
		narrowed[name] = agent
	}
	if !found {
		t.Fatal("the fixture configures no developer agent")
	}
	return narrowed
}

// A landing whose project has an agent filling a role definition compares the
// configuration with the running parts as it compares any other: the name is a
// value under `role`, so a part on this build reads the file and nothing is
// said of it.
func TestALandingReadsAnAgentOnARoleDefinitionLikeAnyOther(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"true"})
	pipeline.Config.Agents = withNarrowedDeveloper(t, pipeline.Config.Agents)

	stateRoot := t.TempDir()
	configPath := filepath.Join(stateRoot, "config.yaml")
	if err := os.WriteFile(configPath, []byte("agents:\n  developer:\n    role: narrow\n    backend: claude-code\n    model: opus\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := runstate.NewConfigReaderStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	store = store.WithProcessCheck(func(int) (bool, error) { return true, nil })
	if err := store.Record(runstate.ConfigReader{Service: "scheduler", PID: 4343, Build: "9870df6a1b2c3d4e", ConfigPath: configPath, StartedAt: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC), Keys: config.SchemaKeys()}); err != nil {
		t.Fatal(err)
	}
	pipeline.ConfigReaders = store

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("outcome = %#v, want the run landed", outcome)
	}
	if len(outcome.ConfigMismatches) != 0 || strings.Contains(strings.Join(tracker.NoteRecords, "\n"), "cannot read the configuration") {
		t.Fatalf("outcome names %+v and notes %v, want an agent on a definition read like any other", outcome.ConfigMismatches, tracker.NoteRecords)
	}
}
