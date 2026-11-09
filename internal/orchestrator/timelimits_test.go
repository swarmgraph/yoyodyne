package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
)

// A developer's provider call is bounded by its own time_limits.provider_call,
// and by the adapters' four hours where it states none.
func TestADevelopersProviderCallTakesItsConfiguredLimit(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		stated config.Duration
		want   time.Duration
	}{
		{name: "unset", want: 4 * time.Hour},
		{name: "configured", stated: config.Duration(6 * time.Hour), want: 6 * time.Hour},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			repository := pipelineRepository(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Work", Status: "open"}}
			provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
			}, approveVerdict)
			pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
			for name, agent := range pipeline.Config.Agents {
				if agent.Role == domain.RoleDeveloper {
					agent.TimeLimits.ProviderCall = test.stated
					pipeline.Config.Agents[name] = agent
				}
			}
			if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			attempts := provider.RequestsForRole(domain.RoleDeveloper)
			if len(attempts) == 0 {
				t.Fatal("the run made no developer attempt")
			}
			for index, request := range attempts {
				if request.Timeout != test.want {
					t.Errorf("developer attempt %d timeout = %s, want %s", index, request.Timeout, test.want)
				}
			}
		})
	}
}
