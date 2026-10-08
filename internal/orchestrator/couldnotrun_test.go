package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A check that says it could not run judged nothing about the change, so it is
// neither repair input nor a stop: the change goes to review and lands on its
// first attempt, the checks after it still run, and the run, its evidence, and
// the item's notes each say which check it went without and why.
func TestPipelineLetsAChangeOnWithoutACheckThatCouldNotRunAndSpendsNoRepair(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	unrunnable := `echo "` + checks.CouldNotRunPrefix + ` codex is not installed on this machine" >&2; exit 2`
	after := `test -f feature.txt`
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{unrunnable, after})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || !tracker.Closed || tracker.Blocked {
		t.Fatalf("the change did not land: %#v, closed = %t, blocked = %t", outcome.Integration, tracker.Closed, tracker.Blocked)
	}
	if outcome.RepairAttempts != 0 || len(provider.RequestsForRole(domain.RoleDeveloper)) != 1 {
		t.Fatalf("repair attempts = %d, developer invocations = %d, want none spent on a check that could not run",
			outcome.RepairAttempts, len(provider.RequestsForRole(domain.RoleDeveloper)))
	}
	if reviews := provider.RequestsForRole(domain.RoleReviewer); len(reviews) != 1 || !strings.Contains(reviews[0].Prompt, "could not run, so it judged nothing about this change: codex is not installed on this machine") {
		t.Fatalf("the reviewer was not told the check could not run: %d review(s)", len(reviews))
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := []runstate.CheckCouldNotRun{{Command: unrunnable, Reason: "codex is not installed on this machine"}}
	if state.CheckStage == nil || !reflect.DeepEqual(state.CheckStage.CouldNotRun, want) || !reflect.DeepEqual(state.CheckStage.Ran, []string{after}) {
		t.Fatalf("check stage = %#v, want the check that could not run apart from the one that ran", state.CheckStage)
	}
	if state.ChecksPassed == nil || !reflect.DeepEqual(state.ChecksPassed.CouldNotRun, want) || !reflect.DeepEqual(state.ChecksPassed.Commands, []string{after}) {
		t.Fatalf("checks passed = %#v, want the evidence to say which check it is short of", state.ChecksPassed)
	}
	if state.CheckFailure != nil {
		t.Fatalf("check failure = %#v, want none", state.CheckFailure)
	}
	if !strings.Contains(tracker.Notes, "Check: "+unrunnable+" could not run, so it judged nothing and spent no repair attempt: codex is not installed on this machine") {
		t.Fatalf("the item's notes do not say the check could not run:\n%s", tracker.Notes)
	}
}

// A real failure beside a check that could not run is still a failure: it is
// handed back to the developer and spends an attempt exactly as it would alone.
func TestPipelineStillHandsBackARealFailureBesideACheckThatCouldNotRun(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	attempts := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempts++
		if attempts == 1 {
			return nil
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	unrunnable := `echo "` + checks.CouldNotRunPrefix + ` codex is not installed on this machine"; exit 1`
	failing := `test -f feature.txt || { echo "feature.txt is missing" >&2; exit 3; }`
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{unrunnable, failing})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || outcome.RepairAttempts != 1 {
		t.Fatalf("outcome = %#v, want the real failure repaired with one attempt and the change landed", outcome)
	}
	repair := provider.RequestsForRole(domain.RoleDeveloper)[1]
	if !strings.Contains(repair.Prompt, "Command: "+failing) || strings.Contains(repair.Prompt, "Command: "+unrunnable) {
		t.Fatalf("repair prompt should name the failing check and not the one that could not run:\n%s", repair.Prompt)
	}
}
