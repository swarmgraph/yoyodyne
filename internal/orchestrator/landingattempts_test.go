package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// An approved change that conflicts with main at landing, on a run whose review
// rounds have left it no repair attempt, is not stopped: it gets its one
// reconciliation attempt, which is recorded as that and charges no repair
// attempt, and the reconciled change is checked again and reviewed again before
// it lands.
func TestAConflictAtLandingWithNoRepairAttemptLeftIsReconciledAndLands(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	var reconciling string
	attempts := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempts++
		design := filepath.Join(request.WorkingDirectory, "docs", "design.md")
		if attempts == 1 {
			if err := os.WriteFile(design, []byte("this run's answer\n"), 0o600); err != nil {
				return err
			}
			writePipelineFile(t, repository, filepath.Join("docs", "design.md"), "somebody else's answer\n")
			runPipelineGit(t, repository, "add", "docs/design.md")
			runPipelineGit(t, repository, "commit", "-m", "conflicting target change")
			return nil
		}
		reconciling = request.Prompt
		return os.WriteFile(design, []byte("both answers, reconciled\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 0

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want the conflict reconciled and the change landed", err)
	}
	if outcome.Blocked || outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("Run() outcome = %#v, closed = %t; want the reconciled change landed", outcome, tracker.Closed)
	}
	if content := readPipelineFile(t, repository, filepath.Join("docs", "design.md")); content != "both answers, reconciled\n" {
		t.Fatalf("target content = %q, want the developer's reconciliation", content)
	}
	if !strings.Contains(reconciling, "not counted against your repair attempts") {
		t.Fatalf("the reconciliation prompt does not say it is not a repair attempt:\n%s", reconciling)
	}
	// Nothing about the gate was loosened: the reconciled change was reviewed by
	// its own invocation.
	if developers, reviews := provider.RequestsForRole(domain.RoleDeveloper), provider.RequestsForRole(domain.RoleReviewer); len(developers) != 2 || len(reviews) != 2 {
		t.Fatalf("developer invoked %d time(s), reviewer %d; want one reconciliation and a fresh verdict on it", len(developers), len(reviews))
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.RepairAttempts != 0 || state.Reconciliation == nil || state.Reconciliation.TargetBranch != "main" {
		t.Fatalf("state = attempts %d, reconciliation %#v; want the reconciliation recorded and no repair attempt spent", state.RepairAttempts, state.Reconciliation)
	}
	if strings.Join(state.Reconciliation.Paths, ",") != "docs/design.md" {
		t.Fatalf("reconciliation paths = %v, want the conflicted file", state.Reconciliation.Paths)
	}
	if len(state.LostLandings) != 1 || state.LostLandings[0].Replay != runstate.ReplayConflicted || state.CleanReplays != 0 {
		t.Fatalf("lost landings = %#v, clean %d; want the one landing lost, its replay conflicted, and it counted", state.LostLandings, state.CleanReplays)
	}
	// The run's summary says the reconciliation was used and what the lost
	// landing made of the change.
	for _, want := range []string{
		"Reconciliation attempt: used.",
		"that attempt was not counted against the repair attempts",
		"Landing attempts lost to other work: 1. 0 of them were replayed cleanly",
		"Lost landing 1: other work landed on main first",
		"it conflicted with what other work had changed there",
	} {
		if !strings.Contains(tracker.Notes, want) {
			t.Fatalf("notes do not say %q:\n%s", want, tracker.Notes)
		}
	}
}

// A landing lost to other work is followed by a replay onto main as it then
// stands, and a replay that applies cleanly and passes its checks is not
// counted as a lost landing: a run that loses more landings than
// lostRacesBeforeGivingBackTheSlot, every one of them answered by a clean
// replay, still lands, and its summary says what each one changed.
func TestLandingsLostToOtherWorkWhoseReplaysAreCleanAreNotCountedAndTheChangeLands(t *testing.T) {
	t.Parallel()

	races := lostRacesBeforeGivingBackTheSlot + 1
	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(implementsFeature, approveVerdict)
	movingTargetReviewer(t, repository, provider, races)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})
	original := strings.TrimSpace(gitOutput(t, repository, "rev-parse", "main"))

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want clean replays never to stop the run", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil || !tracker.Closed || tracker.Blocked {
		t.Fatalf("Run() outcome = %#v, want the change landed and the item closed", outcome)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.IntegrationRetries != races || state.CleanReplays != races || state.CountedLostLandings() != 0 || state.ChargedReplays != 0 {
		t.Fatalf("durable record: lost %d, clean %d, counted %d, charged %d; want every lost landing answered by a clean replay and none counted",
			state.IntegrationRetries, state.CleanReplays, state.CountedLostLandings(), state.ChargedReplays)
	}
	if len(state.LostLandings) != races {
		t.Fatalf("lost landings = %d, want one account per landing lost", len(state.LostLandings))
	}
	// Each attempt after a lost landing started from main as it stood then, not
	// from the base the change was last checked on.
	from := original
	for index, lost := range state.LostLandings {
		if lost.FromCommit != from || lost.OntoCommit == "" || lost.OntoCommit == lost.FromCommit || lost.Replay != runstate.ReplayClean {
			t.Fatalf("lost landing %d = %#v; want it replayed from %s onto the newer main, cleanly", index+1, lost, from)
		}
		from = lost.OntoCommit
	}
	if state.BaseCommit != from || state.Integration == nil || state.Integration.PreviousTargetCommit != from {
		t.Fatalf("landed from base %s onto %v, want the last replay's base %s", state.BaseCommit, state.Integration, from)
	}
	// Nothing about the gate was loosened: every replay was checked and reviewed.
	if reviews := provider.RequestsForRole(domain.RoleReviewer); len(reviews) != races+1 {
		t.Fatalf("reviewer invocations = %d, want the first verdict and one per replay", len(reviews))
	}
	for _, want := range []string{
		"Reconciliation attempt: not used.",
		"Landing attempts lost to other work: " + strconv.Itoa(races) + ". " + strconv.Itoa(races) + " of them were replayed cleanly onto the newer main and passed their checks, so they are not counted as lost.",
		"Lost landing 1: other work landed on main first, so the change was moved from " + original,
		"Lost landing " + strconv.Itoa(races) + ":",
		"it applied cleanly and passed its checks, so this landing is not counted as lost.",
	} {
		if !strings.Contains(tracker.Notes, want) {
			t.Fatalf("notes do not say %q:\n%s", want, tracker.Notes)
		}
	}
}

// A landing nothing contended reads exactly as it did before: no lost landing,
// no reconciliation, and none of their lines in the run's summary.
func TestACleanLandingSaysNothingAboutLandingAttempts(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(implementsFeature, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || !tracker.Closed || outcome.RepairAttempts != 0 {
		t.Fatalf("Run() outcome = %#v, want the change landed with nothing handed back", outcome)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.IntegrationRetries != 0 || state.CleanReplays != 0 || len(state.LostLandings) != 0 || state.Reconciliation != nil {
		t.Fatalf("durable record = lost %d, clean %d, accounts %d, reconciliation %#v; want none", state.IntegrationRetries, state.CleanReplays, len(state.LostLandings), state.Reconciliation)
	}
	for _, absent := range []string{"Reconciliation attempt", "Landing attempts lost", "Lost landing"} {
		if strings.Contains(tracker.Notes, absent) {
			t.Fatalf("notes of a clean landing say %q:\n%s", absent, tracker.Notes)
		}
	}
}
