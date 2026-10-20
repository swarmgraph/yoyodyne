package readmodel

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Finished-run entries are dated from the run's ending. The operator's section
// carries only his entries: never the cleanup the harness still has to finish,
// and never a publication, which the ownership registry gives to the harness,
// the forge, or the development manager.
func TestOwedStepsAndPublicationsAreDatedFromTheRunsEndingAndNeitherIsTheOperators(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	ended := now.Add(-5 * time.Hour)
	owed := owedStepAttention(runstate.State{RunID: "run-owed", WorkItemID: "yoyodyne-ifd.1", Status: runstate.StatusSucceeded, CompletedAt: &ended})
	if !owed.Since().Equal(ended) {
		t.Errorf("owed step since = %s, want the run's ending %s", owed.Since(), ended)
	}

	promotedEnded := now.Add(-26 * time.Hour)
	publication := awaitingForgeAttention(runstate.State{
		RunID: "run-published", WorkItemID: "yoyodyne-ifd.2", Status: runstate.StatusSucceeded, CompletedAt: &promotedEnded,
		Branch:         "yoyodyne/yoyodyne-ifd-2/abc",
		Integration:    &runstate.Integration{TargetBranch: "main"},
		PullRequest:    &runstate.PullRequest{Number: 42, URL: "https://example.test/pull/42"},
		PublishFailure: "the forge refused the merge request",
	})
	if publication.Mover != ownership.MoverDevelopmentManager {
		t.Fatalf("publication mover = %s, want the development manager's", publication.Mover)
	}
	if !publication.Since().Equal(promotedEnded) {
		t.Errorf("publication since = %s, want the run's ending %s", publication.Since(), promotedEnded)
	}

	rendered := Standing{NeedsHuman: []Attention{owed, publication}}.RenderOperatorWaits(now)
	if !strings.Contains(rendered, "Nothing on the needs-a-human line is the operator's.") {
		t.Errorf("the section carries entries that are not the operator's:\n%s", rendered)
	}
	for _, entry := range []string{"run-owed", "run-published"} {
		if strings.Contains(rendered, entry) {
			t.Errorf("%s reached the operator's section: %s", entry, rendered)
		}
	}
}
