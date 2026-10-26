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

// A change no configured check could run on is not integrated. Whether the
// project declares one check or several, and whether each check said so itself
// or the machine refused to start its tool (make exiting 69 with the Xcode
// licence message on every target), nothing verified the change: it is not
// reviewed and not promoted, nothing is handed back to the developer, no repair
// attempt or review round is spent, the item is blocked naming every check and
// why it could not run, the stoppage is on the development manager's docket,
// and the branch and worktree are kept.
func TestAChangeNoCheckCouldRunOnIsHeldForTheDevelopmentManager(t *testing.T) {
	t.Parallel()

	licence := "You have not agreed to the Xcode license agreements. Please run 'sudo xcodebuild -license' from within a Terminal window to review and agree to the Xcode and/or iOS license."
	refusedByMachine := `echo "` + licence + `" >&2; exit 69`
	saysSo := `echo "` + checks.CouldNotRunPrefix + ` codex is not installed on this machine" >&2; exit 2`
	for _, project := range []struct {
		name   string
		checks []string
		want   []runstate.CheckCouldNotRun
	}{
		{
			name:   "a single check that says it could not run",
			checks: []string{saysSo},
			want:   []runstate.CheckCouldNotRun{{Command: saysSo, Reason: "codex is not installed on this machine"}},
		},
		{
			name:   "every check refused by the machine",
			checks: []string{refusedByMachine + " # make test", refusedByMachine + " # make race"},
			want: []runstate.CheckCouldNotRun{
				{Command: refusedByMachine + " # make test", Reason: "the machine refused to start it: " + licence},
				{Command: refusedByMachine + " # make race", Reason: "the machine refused to start it: " + licence},
			},
		},
	} {
		t.Run(project.name, func(t *testing.T) {
			t.Parallel()

			tracker := newOutcomeTracker()
			provider := orchestratortest.RoleBackend(writeFeature, approveVerdict)
			pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, project.checks)
			docket := &memoryDocket{}
			pipeline.Docket = docketerOverStore(docket, store, pipeline.Config)

			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			if err == nil || !strings.Contains(err.Error(), "no check verified the change") {
				t.Fatalf("Run() error = %v, want the change refused because no check verified it", err)
			}
			if outcome.Integration != nil || tracker.Closed || !tracker.Blocked {
				t.Fatalf("integration = %#v, closed = %t, blocked = %t; want nothing integrated and the item blocked", outcome.Integration, tracker.Closed, tracker.Blocked)
			}
			if developers, reviewers := len(provider.RequestsForRole(domain.RoleDeveloper)), len(provider.RequestsForRole(domain.RoleReviewer)); developers != 1 || reviewers != 0 {
				t.Fatalf("developer invocations = %d, reviewer invocations = %d; want one attempt, nothing handed back, and no review", developers, reviewers)
			}
			assertSavedStopClass(t, store, outcome.RunID, runstate.CauseNoCheckRan.StopClass())
			state, err := store.Load(outcome.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if state.RepairAttempts != 0 || state.ChecksPassed != nil || state.CheckFailure != nil || state.ReviewDecision != "" {
				t.Fatalf("repairs %d, checks passed %#v, failure %#v, review %q; want nothing spent and no evidence earned",
					state.RepairAttempts, state.ChecksPassed, state.CheckFailure, state.ReviewDecision)
			}
			if state.CheckStage == nil || !reflect.DeepEqual(state.CheckStage.CouldNotRun, project.want) || len(state.CheckStage.Ran) != 0 {
				t.Fatalf("check stage = %#v, want every check recorded as could not run", state.CheckStage)
			}
			refused := state.Environmental
			if refused == nil || refused.Cause != runstate.CauseNoCheckRan || !refused.Settled || !refused.Refused {
				t.Fatalf("environmental record = %#v, want a settled %s stop that spent nothing", refused, runstate.CauseNoCheckRan)
			}
			counters, err := store.Triage().Counters(tracker.Item.ID)
			if err != nil {
				t.Fatal(err)
			}
			if counters.ReviewRounds != 0 {
				t.Fatalf("review rounds = %d, want none charged", counters.ReviewRounds)
			}
			if len(docket.entries) != 1 {
				t.Fatalf("docket = %+v, want one entry for the development manager", docket.entries)
			}
			for name, text := range map[string]string{"item": tracker.BlockReason, "docket entry": docket.entries[0].Blocker, "run failure": state.Failure} {
				if !strings.Contains(text, "no check verified") {
					t.Errorf("%s = %q, want it to say no check verified the change", name, text)
				}
				for _, check := range project.want {
					if !strings.Contains(text, check.Says()) {
						t.Errorf("%s = %q, want it to name %q", name, text, check.Says())
					}
				}
			}
			if outcome.Preservation == nil || outcome.Preservation.Lost() || state.WorktreeRemoved || state.BranchRemoved {
				t.Fatalf("preservation = %+v, want the branch and worktree kept", outcome.Preservation)
			}
			if _, statErr := os.Stat(filepath.Join(state.WorktreePath, "feature.txt")); statErr != nil {
				t.Fatalf("the change is gone from its worktree: %v", statErr)
			}
		})
	}
}

// One check that ran and passed is enough: beside checks the machine refused to
// start, the change is reviewed and integrated exactly as it is beside a check
// that said it could not run.
func TestOneCheckThatRanLetsAChangeOnBesideChecksTheMachineRefused(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := orchestratortest.RoleBackend(writeFeature, approveVerdict)
	refused := `echo "You have not agreed to the Xcode license agreements." >&2; exit 69`
	pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{refused, `test -f feature.txt`})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || outcome.Integration == nil || !tracker.Closed || outcome.RepairAttempts != 0 {
		t.Fatalf("Run() = %#v, %v; want the change integrated on its first attempt", outcome, err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.ChecksPassed == nil || !reflect.DeepEqual(state.ChecksPassed.Commands, []string{`test -f feature.txt`}) || len(state.ChecksPassed.CouldNotRun) != 1 {
		t.Fatalf("checks passed = %#v, want the check that ran as evidence and the refused one named", state.ChecksPassed)
	}
}
