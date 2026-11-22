package landingtests

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/protectedpath"
)

const (
	configurationFile = ".yoyodyne/config.yaml"
	intakeAtBase      = "checks: [make test]\nintake:\n  queries:\n    slack-bugs:\n      tool: slack.search\n      questions: lead-product-manager\n"
	// otherSettingChanged changes a setting the grant does reach and leaves the
	// intake section as it was.
	otherSettingChanged = "checks: [make test, make vet]\nintake:\n  queries:\n    slack-bugs:\n      tool: slack.search\n      questions: lead-product-manager\n"
	// intakeRepointed sends the query's questions to the operator.
	intakeRepointed = "checks: [make test, make vet]\nintake:\n  queries:\n    slack-bugs:\n      tool: slack.search\n      questions: person\n"
)

// intakeRepository is a checkout whose configuration file carries an intake
// section at the base every run is cut from.
func intakeRepository(t *testing.T) string {
	t.Helper()
	repository := testharness.PipelineRepository(t)
	testharness.WritePipelineFile(t, repository, configurationFile, intakeAtBase)
	testharness.RunPipelineGit(t, repository, "add", configurationFile)
	testharness.RunPipelineGit(t, repository, "commit", "-m", "configure intake")
	return repository
}

// A grant of the configuration file, or of the directory around it, never
// reaches the intake section: a change that repoints a query is refused before
// any check or reviewer sees it, the refusal names the query, and the repaired
// change that leaves the section alone lands with its other setting.
func TestAChangeToTheIntakeSectionIsRefusedThoughTheItemGrantsTheConfiguration(t *testing.T) {
	t.Parallel()

	for _, grant := range []string{configurationFile, ".yoyodyne"} {
		t.Run(grant, func(t *testing.T) {
			t.Parallel()

			repository := intakeRepository(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
				ID:          "yoyodyne-task",
				Title:       "Add a check to the project configuration",
				Description: "The configuration needs a vet check.\n\n" + protectedpath.GrantMarker + " " + grant + "\n",
				Status:      "open",
			}}
			attempts := 0
			provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
				attempts++
				if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
					return err
				}
				if attempts == 1 {
					return testharness.WriteUpstream(t, request.WorkingDirectory, configurationFile, intakeRepointed)
				}
				return testharness.WriteUpstream(t, request.WorkingDirectory, configurationFile, otherSettingChanged)
			}, testharness.ApproveVerdict)
			pipeline, _ := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if outcome.Integration == nil || outcome.RepairAttempts != 1 {
				t.Fatalf("the repaired change was not integrated on one refusal: %#v", outcome)
			}
			if reviews := len(provider.RequestsForRole(domain.RoleReviewer)); reviews != 1 {
				t.Fatalf("reviews = %d, want only the attempt that left the intake section alone", reviews)
			}
			developerRequests := provider.RequestsForRole(domain.RoleDeveloper)
			if len(developerRequests) != 2 {
				t.Fatalf("developer invocations = %d, want the first attempt and its repair", len(developerRequests))
			}
			for _, want := range []string{"intake.queries.slack-bugs", configurationFile, "Granted by this work item: " + grant, protectedpath.IntakeInstruction} {
				if !strings.Contains(developerRequests[1].Prompt, want) {
					t.Fatalf("the refusal is missing %q:\n%s", want, developerRequests[1].Prompt)
				}
			}
			landed := testharness.GitOutput(t, repository, "show", "main:"+configurationFile)
			if landed != otherSettingChanged {
				t.Fatalf("landed configuration = %q, want the other setting changed and the intake section as it was", landed)
			}
		})
	}
}

// A granted change elsewhere in the configuration file is untouched by the
// intake comparison: it lands on its first attempt with no refusal.
func TestAGrantedChangeOutsideTheIntakeSectionLandsUnrefused(t *testing.T) {
	t.Parallel()

	repository := intakeRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
		ID:          "yoyodyne-task",
		Title:       "Add a check to the project configuration",
		Description: "The configuration needs a vet check.\n\n" + protectedpath.GrantMarker + " " + configurationFile + "\n",
		Status:      "open",
	}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
			return err
		}
		return testharness.WriteUpstream(t, request.WorkingDirectory, configurationFile, otherSettingChanged)
	}, testharness.ApproveVerdict)
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || outcome.RepairAttempts != 0 {
		t.Fatalf("the granted change was not integrated on its first attempt: %#v", outcome)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.PathRefusal != nil {
		t.Fatalf("a granted change outside the intake section was refused: %#v", state.PathRefusal)
	}
	if landed := testharness.GitOutput(t, repository, "show", "main:"+configurationFile); landed != otherSettingChanged {
		t.Fatalf("landed configuration = %q, want the granted change", landed)
	}
}
