package orchestrator

// What a listing says became of a run is derived from the durable record, and
// the whole derivation turns on one field: a run that ended on a blocker reads
// as stopped, and one nothing blocked does not. That is a claim about what this
// package writes, so it is checked here rather than over states a test built by
// hand — a rendering test that assigns Blocker itself would pass just as happily
// if nothing ever recorded one.
//
// The surfaces document five endings as `stopped`, so all five are driven
// through the pipeline here rather than the one that was easiest to reach. They
// are genuinely different code paths — two spend the repair budget, one is
// refused before any check runs, one loses a race for its target branch, and one
// is the provider refusing to carry the run at all — and any of them that blocked
// the item without writing the run's own blocker would print `failed` over
// preserved work, which is the defect this vocabulary was introduced for.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Every ending the harness hands to a person is read back as one stoppage with
// its work intact. What separates them in a listing is the phase and the reason;
// what they share is the fact the operator was asking about.
func TestEveryEndingTheHarnessHandsToAPersonIsReadBackAsStopped(t *testing.T) {
	t.Parallel()

	for _, ending := range []struct {
		name string
		// reason is a fragment of the run's own recorded account, so a case that
		// silently took a different path out of the pipeline fails here rather
		// than passing on the strength of some other stoppage.
		reason string
		build  func(t *testing.T) (Pipeline, *runstate.Store, *orchestratortest.Tracker)
	}{
		{
			name:   "a review nobody repaired",
			reason: "independent review requires repair",
			build: func(t *testing.T) (Pipeline, *runstate.Store, *orchestratortest.Tracker) {
				tracker := newOutcomeTracker()
				provider := orchestratortest.RoleBackend(writeFeature, repairVerdict)
				pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})
				pipeline.Config.Execution.RepairAttemptsBeforeReplan = 1
				return pipeline, store, tracker
			},
		},
		{
			name:   "a check that kept failing",
			reason: "verification failed after",
			build: func(t *testing.T) (Pipeline, *runstate.Store, *orchestratortest.Tracker) {
				tracker := newOutcomeTracker()
				provider := orchestratortest.RoleBackend(writeFeature, approveVerdict)
				pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider,
					[]string{`echo "the suite is still red" >&2; exit 3`})
				pipeline.Config.Execution.RepairAttemptsBeforeReplan = 1
				return pipeline, store, tracker
			},
		},
		{
			name:   "protected paths the item never granted",
			reason: "protected paths refused after",
			build: func(t *testing.T) (Pipeline, *runstate.Store, *orchestratortest.Tracker) {
				tracker := newOutcomeTracker()
				provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
					return writeUpstream(t, request.WorkingDirectory,
						"docs/decisions/invariants/new-invariant.md", "an invariant this run wrote for itself\n")
				}, approveVerdict)
				pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})
				pipeline.Config.Execution.RepairAttemptsBeforeReplan = 1
				return pipeline, store, tracker
			},
		},
		{
			// One of the operator's own brake runs, and the ending that used to be
			// indistinguishable from a provider death in the listing.
			name:   "a replay the target branch outran",
			reason: "cannot be replayed onto the moved target branch",
			build: func(t *testing.T) (Pipeline, *runstate.Store, *orchestratortest.Tracker) {
				repository := pipelineRepository(t)
				tracker := newOutcomeTracker()
				provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
					if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "docs", "design.md"),
						[]byte("this run's answer\n"), 0o600); err != nil {
						return err
					}
					// The target moves onto the same line, so replaying is a
					// decision rather than something the harness may take.
					writePipelineFile(t, repository, filepath.Join("docs", "design.md"), "somebody else's answer\n")
					runPipelineGit(t, repository, "add", "docs/design.md")
					runPipelineGit(t, repository, "commit", "-m", "conflicting target change")
					return nil
				}, approveVerdict)
				pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
				// A conflict goes to its own developer while an attempt is left, so the
				// ending handed to a person is the one with none.
				pipeline.Config.Execution.RepairAttemptsBeforeReplan = 0
				return pipeline, store, tracker
			},
		},
		{
			name:   "a provider that would not carry the run",
			reason: "the provider ended this run without judging the work",
			build: func(t *testing.T) (Pipeline, *runstate.Store, *orchestratortest.Tracker) {
				tracker := newOutcomeTracker()
				// More deaths than the budget can pay for, and of something the
				// harness cannot classify, so what stops the run is the budget rather
				// than the provider recovering or the recovery window running out.
				provider := opaqueDeathBackend(10, approveVerdict)
				pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})
				pipeline.Config.Execution.TransientRelaunchesBeforeBlocking = 2
				return pipeline, store, tracker
			},
		},
	} {
		t.Run(ending.name, func(t *testing.T) {
			t.Parallel()
			pipeline, store, tracker := ending.build(t)

			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			if err == nil {
				t.Fatal("Run() error = nil, want the stoppage reported as what ended the run")
			}
			if !tracker.Blocked || !outcome.Blocked {
				t.Fatalf("the ending left no blocker: tracker = %t, outcome = %t", tracker.Blocked, outcome.Blocked)
			}

			reported := onlyRecordedRun(t, store)
			if !strings.Contains(reported.Failure, ending.reason) {
				t.Fatalf("recorded reason = %q, want it to name %q: this case took a different path out of the pipeline",
					reported.Failure, ending.reason)
			}
			// The durable status is still what it always was; what changed is the
			// word a listing says over it.
			if reported.Status == runstate.StatusSucceeded {
				t.Fatalf("status = %q, want a run that did not land its work", reported.Status)
			}
			if reported.Outcome != runstate.OutcomeStopped {
				t.Fatalf("outcome = %q, want %q: the item carries a blocker and a person decides next",
					reported.Outcome, runstate.OutcomeStopped)
			}
			// And the claim beside the word: the change is still there, at the
			// paths the listing sends a reader to.
			if !reported.Preserved() {
				t.Fatalf("run = %#v, want its change reported as preserved", reported)
			}
			if reported.Branch != outcome.Branch || reported.WorktreePath != outcome.WorktreePath {
				t.Fatalf("run names branch %q and worktree %q, want the run's own %q and %q",
					reported.Branch, reported.WorktreePath, outcome.Branch, outcome.WorktreePath)
			}
			if _, err := os.Stat(reported.WorktreePath); err != nil {
				t.Fatalf("the listing reported a preserved worktree that is not there: %v", err)
			}
			// Selecting the runs that went wrong still finds it: the vocabulary
			// separates them within that selection rather than taking one out of it.
			failed, err := store.History(runstate.RunQuery{FailedOnly: true})
			if err != nil {
				t.Fatalf("History() error = %v", err)
			}
			if len(failed.Runs) != 1 || failed.Runs[0].Outcome != runstate.OutcomeStopped {
				t.Fatalf("failed listing = %#v, want the stoppage still selected", failed.Runs)
			}
		})
	}
}

// The unrepaired review is the one the operator actually read, so it is held to
// the rest of what the listing says about it: the session a continuation would
// resume in, and the findings recorded against the change that survives.
func TestAnUnrepairedReviewIsReadBackAsStoppedWithItsWorkPreserved(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := newOutcomeTracker()
	provider := orchestratortest.RoleBackend(writeFeature, repairVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 1

	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil {
		t.Fatal("Run() error = nil, want the spent repair budget reported as what ended the run")
	}

	reported := onlyRecordedRun(t, store)
	if reported.Status != runstate.StatusFailed || reported.Outcome != runstate.OutcomeStopped {
		t.Fatalf("run = %#v, want a failed status read back as a stoppage", reported)
	}
	if _, err := os.Stat(filepath.Join(reported.WorktreePath, "feature.txt")); err != nil {
		t.Fatalf("the listing reported preserved work that is not there: %v", err)
	}
	if reported.ProviderSessionID == "" || reported.ReviewFindings != 1 {
		t.Fatalf("run = %#v, want the preserved session and the finding recorded against it", reported)
	}
	if reported.Phase != runstate.PhaseReviewing {
		t.Fatalf("phase = %q, want the phase that says where this stoppage happened", reported.Phase)
	}
}

// An operator stop is the other half of the same claim. Nothing judged the
// change and nothing was handed to anybody, so no blocker is recorded — which is
// exactly what keeps this run out of the stopped vocabulary while its work is
// preserved just as thoroughly.
func TestAnOperatorStopIsReadBackAsCancelledRatherThanStopped(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := newOutcomeTracker()
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if err := store.RecordStop(runstate.StopRequest{
			SchemaVersion: runstate.StopSchemaVersion,
			ProductID:     "yoyodyne",
			RunID:         request.RunID,
			WorkItemID:    tracker.Item.ID,
			RequestedAt:   baseTime,
			Reason:        "it is rewriting the wrong file",
		}); err != nil {
			return err
		}
		return writeFeature(request)
	}, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil {
		t.Fatal("Run() error = nil, want the stop reported as what ended the run")
	}
	if outcome.Blocked || tracker.Blocked {
		t.Fatalf("an operator stop blocked the item: outcome = %t, tracker = %t", outcome.Blocked, tracker.Blocked)
	}

	reported := onlyRecordedRun(t, store)
	if reported.Status != runstate.StatusCancelled {
		t.Fatalf("status = %q, want a stopped run recorded as cancelled", reported.Status)
	}
	if reported.Outcome != runstate.OutcomeCancelled {
		t.Fatalf("outcome = %q, want %q: nothing judged the change and nobody was handed anything",
			reported.Outcome, runstate.OutcomeCancelled)
	}
	// The distinction is worth nothing if a cancel is preserved less thoroughly
	// than a stoppage: the operator's question is the same over both.
	if !reported.Preserved() || reported.Branch == "" || reported.WorktreePath == "" {
		t.Fatalf("run = %#v, want the cancelled run's artifacts named and preserved", reported)
	}
	if _, err := os.Stat(filepath.Join(reported.WorktreePath, "feature.txt")); err != nil {
		t.Fatalf("the listing reported preserved work that is not there: %v", err)
	}
	// The field the whole derivation turns on: a cancel records none, which is
	// what stops it reading as work waiting on a decision nobody made.
	state, err := store.Load(reported.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if strings.TrimSpace(state.Blocker) != "" {
		t.Fatalf("an operator stop recorded a blocker: %q", state.Blocker)
	}
}

// The listing has one phrase for a record that names no artifact, and the whole
// case for it is that a run which reached any phase has a worktree — the phase is
// only written once the worktree exists. So this pins both halves: a run that
// broke before either records neither and no phase, and it is the only shape that
// can, because every stoppage above carried both.
func TestARunThatBrokeBeforeItsWorktreeRecordsNoPhaseAndNoArtifacts(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := newOutcomeTracker()
	// The claim is the last thing before the worktree is created, so refusing it
	// ends the run where nothing has been made yet.
	tracker.OnClaim = func() error { return errors.New("the tracker is not answering") }
	provider := orchestratortest.RoleBackend(writeFeature, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})

	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil {
		t.Fatal("Run() error = nil, want the refused claim to end the run")
	}
	reported := onlyRecordedRun(t, store)
	if reported.Phase != "" {
		t.Fatalf("phase = %q, want a run that never reached one", reported.Phase)
	}
	if reported.Branch != "" || reported.WorktreePath != "" {
		t.Fatalf("run = %#v, want a record that names no artifact", reported)
	}
	if reported.Preserved() {
		t.Fatalf("run = %#v, want nothing claimed as preserved", reported)
	}
	// Nobody was handed anything to decide, so this is the one ending that keeps
	// the bare word.
	if reported.Outcome != runstate.OutcomeFailed {
		t.Fatalf("outcome = %q, want %q", reported.Outcome, runstate.OutcomeFailed)
	}
}

// The run's record says which ending the clear of a stale blocked status had.
// The one that matters most is the one that fails: a clear no read confirmed
// leaves the item for the next pull and the run dead at the claim, which on
// 2026-09-20 is how yoyodyne-ifd.415's re-run tripped on its own correction
// with nothing in the record saying so beyond bd's refusal. Both halves are
// pinned: the account is on the record beside the failure, and the read model
// every surface projects carries it through.
func TestARunWhoseStaleBlockClearWasNeverConfirmedRecordsSo(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := newOutcomeTracker()
	tracker.Item.Status = "blocked"
	tracker.StaleBlockClear = &beads.StaleBlockClear{Outcome: domain.StaleBlockClearUnconfirmed, Reads: 5, Status: "blocked"}
	tracker.OnClaim = func() error {
		return errors.New("the clear of the stale blocked status on yoyodyne-task was never confirmed: 5 read(s) over 4s returned status \"blocked\" rather than open, so the item is left for the next pull rather than claimed")
	}
	provider := orchestratortest.RoleBackend(writeFeature, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})

	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil {
		t.Fatal("Run() error = nil, want the unconfirmed clear to end the run")
	}
	recorded, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.WorkItemClaimedAt != nil {
		t.Fatalf("run = %#v, want a run that took nothing", recorded)
	}
	want := &runstate.StaleBlockClear{Outcome: domain.StaleBlockClearUnconfirmed, Reads: 5, Status: "blocked"}
	if !reflect.DeepEqual(recorded.StaleBlockClear, want) {
		t.Fatalf("recorded clear = %#v, want %#v", recorded.StaleBlockClear, want)
	}
	if !strings.Contains(recorded.Failure, "was never confirmed") || strings.Contains(recorded.Failure, "cleared") {
		t.Fatalf("failure = %q, want the unconfirmed clear reported and never as cleared", recorded.Failure)
	}
	reported := onlyRecordedRun(t, store)
	if !reflect.DeepEqual(reported.StaleBlockClear, want) {
		t.Fatalf("summary clear = %#v, want %#v", reported.StaleBlockClear, want)
	}
	if !strings.Contains(reported.StaleBlockClear.Describe(), "left for the next pull") {
		t.Fatalf("Describe() = %q, want it to say the item was left for the next pull", reported.StaleBlockClear.Describe())
	}
}

// A clear that landed, late or at once, is on the record of the run that went
// on to claim the item, for the same reason: a tracker slower than the claim is
// worth seeing before the day it is slower than the wait.
func TestARunWhoseStaleBlockClearLandedLateRecordsSo(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := newOutcomeTracker()
	tracker.StaleBlockClear = &beads.StaleBlockClear{Outcome: domain.StaleBlockClearConfirmedLate, Reads: 3, Status: "open"}
	provider := orchestratortest.RoleBackend(writeFeature, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})

	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	recorded, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.WorkItemClaimedAt == nil {
		t.Fatalf("run = %#v, want a run that claimed its item", recorded)
	}
	want := &runstate.StaleBlockClear{Outcome: domain.StaleBlockClearConfirmedLate, Reads: 3, Status: "open"}
	if !reflect.DeepEqual(recorded.StaleBlockClear, want) {
		t.Fatalf("recorded clear = %#v, want %#v", recorded.StaleBlockClear, want)
	}
	if reported := onlyRecordedRun(t, store); !reflect.DeepEqual(reported.StaleBlockClear, want) {
		t.Fatalf("summary clear = %#v, want %#v", reported.StaleBlockClear, want)
	}
}

// newOutcomeTracker is the work item every run here is made for.
func newOutcomeTracker() *orchestratortest.Tracker {
	return &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
}

// writeFeature is the change a developer attempt makes, so a preserved worktree
// has something in it to be preserved.
func writeFeature(request backend.RunRequest) error {
	return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
}

// onlyRecordedRun reads the history back the way the listing does and returns
// the single run it holds, so a test asserts over what an operator would be
// shown rather than over the state file behind it.
func onlyRecordedRun(t *testing.T, store *runstate.Store) runstate.RunSummary {
	t.Helper()
	history, err := store.History(runstate.RunQuery{})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(history.Runs) != 1 {
		t.Fatalf("history = %#v, want the one run this test made", history.Runs)
	}
	return history.Runs[0]
}
