package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/rolecapability"
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
