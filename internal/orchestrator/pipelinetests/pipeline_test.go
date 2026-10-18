package pipelinetests

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/toolcatalog"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

func TestPipelineEndToEndWithFakeBackend(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
		ID:                 "yoyodyne-task",
		Title:              "Add feature",
		Description:        "Follow docs/design.md",
		Design:             "Make a bounded change",
		AcceptanceCriteria: "feature.txt exists",
		Status:             "open",
	}}
	// The scheduler kept a reason this item had not started; the run that
	// starts it clears that reason and leaves the notes as they were.
	tracker.Item.SchedulingWait = &beads.SchedulingWait{Class: "sequenced behind work in flight", Reason: "waiting behind run run-other", FirstPassedOver: time.Now().Add(-time.Minute), ReasonSince: time.Now().Add(-time.Minute)}
	tracker.Item.Notes = "Earlier notes"
	tracker.OnClaim = func() error {
		if err := os.MkdirAll(filepath.Join(repository, ".beads"), 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(repository, ".beads", "issues.jsonl"), []byte("claim control state\n"), 0o600)
	}
	provider := &orchestratortest.Backend{Respond: func(request backend.RunRequest) (backend.RunResult, error) {
		if tracker.Item.SchedulingWait != nil {
			t.Fatal("the developer was invoked with the scheduler's old reason still kept on the item")
		}
		if !strings.Contains(request.Prompt, "design content") {
			return backend.RunResult{}, errors.New("prompt did not contain referenced design")
		}
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
			return backend.RunResult{}, err
		}
		sequence := execution.NewSequence(request.LastSequence)
		for _, eventType := range []execution.EventType{execution.EventRunStarted, execution.EventAgentMessage, execution.EventRunCompleted} {
			event, err := execution.NewEvent(request.RunID, sequence.Next(), time.Now(), eventType, "fake", nil)
			if err != nil {
				return backend.RunResult{}, err
			}
			if err := request.EventSink(event); err != nil {
				return backend.RunResult{}, err
			}
		}
		return backend.RunResult{
			Backend:   domain.BackendClaudeCode,
			SessionID: "session-1",
			FinalText: "implemented feature",
			Process:   execution.ProcessResult{Status: execution.ProcessSucceeded, ExitCode: 0},
			LastEvent: sequence.Last(),
		}, nil
	}}
	pipeline, store := testharness.NewPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	testharness.AssertSavedStopClass(t, store, outcome.RunID, runstate.StopIntegrationPolicy)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Branch == "" || outcome.WorktreePath == "" || outcome.BaseCommit == "" || outcome.ProviderSessionID != "session-1" {
		t.Fatalf("Run() outcome = %#v", outcome)
	}
	if !strings.Contains(outcome.Changes.Status, "A feature.txt") {
		t.Fatalf("change summary = %#v", outcome.Changes)
	}
	if !tracker.Claimed || !strings.Contains(tracker.Notes, "bootstrap run succeeded") || strings.Contains(tracker.Notes, "closed") {
		t.Fatalf("tracker = %#v", tracker)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Status != runstate.StatusSucceeded || state.Branch != outcome.Branch || state.BaseCommit != outcome.BaseCommit || state.ProviderSessionID != "session-1" {
		t.Fatalf("state = %#v", state)
	}
	// What the run changed is in the durable record as well as in the outcome
	// this process happens to be holding. It has to be: the worktree it
	// describes is removed when the run is cleaned up, and a change nobody
	// recorded is one nobody can be shown afterwards.
	if state.Changes == nil || !strings.Contains(state.Changes.Files, "A feature.txt") {
		t.Fatalf("recorded changes = %#v, want the account of what the run changed", state.Changes)
	}
	events, err := store.LoadEvents(outcome.RunID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	if len(events) != 5 || events[len(events)-1].Type != execution.EventCommandCompleted {
		t.Fatalf("events = %#v", events)
	}
	if _, err := os.Stat(filepath.Join(outcome.WorktreePath, "feature.txt")); err != nil {
		t.Fatalf("preserved worktree change missing: %v", err)
	}
	if tracker.Item.SchedulingWait != nil || !strings.HasPrefix(tracker.Item.Notes, "Earlier notes") {
		t.Fatalf("starting the item left wait %+v, notes %q", tracker.Item.SchedulingWait, tracker.Item.Notes)
	}
}

// The run record says what the item is called and not only which item it is.
// Everything that reports on a run afterwards reads the durable record and never
// the tracker, so a title nothing wrote down while the item was in hand is a
// title no surface can name the work by.
func TestARunRecordsWhatTheItemIsCalled(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
		ID:     "yoyodyne-task",
		Title:  "Slack thread headers carry the item's title, not just its slug",
		Status: "open",
	}}
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, testharness.ApproveVerdict)
	pipeline, store := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.WorkItemTitle != "Slack thread headers carry the item's title, not just its slug" {
		t.Fatalf("recorded title = %q, want what the tracker called the item", state.WorkItemTitle)
	}
}

// A run says which provider account it spent and which configuration set it up.
// Both are read back from the record long after the configuration has been
// edited and, one day, after there is more than one account to have spent: a run
// that named neither could not be attributed to either afterwards.
func TestARunRecordsTheAccountAndConfigurationItRanUnder(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Work", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, testharness.ApproveVerdict)
	pipeline, store := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.AccountAlias != pipeline.Config.AccountAlias() {
		t.Fatalf("recorded account = %q, want the configured %q", state.AccountAlias, pipeline.Config.AccountAlias())
	}
	if state.ConfigRevision != pipeline.Config.Revision() {
		t.Fatalf("recorded configuration = %q, want the revision in force %q", state.ConfigRevision, pipeline.Config.Revision())
	}
}

// And which harness dispatched it. A process runs whatever binary it was started
// with while the harness moves on underneath it, so a run that cannot name the
// build that reserved it is a run whose behaviour cannot be told apart from a
// fix that was merged and never deployed — which is what left four substituted
// repair dispatches undiagnosable in the week of 2026-08-27.
func TestARunRecordsTheHarnessBuildThatDispatchedIt(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Work", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, testharness.ApproveVerdict)
	pipeline, store := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Build = "9870df6a1b2c3d4e5f60718293a4b5c6d7e8f900"

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Build != pipeline.Build {
		t.Fatalf("recorded build = %q, want the build that dispatched it %q", state.Build, pipeline.Build)
	}
	// A binary that carries no revision of its own records none rather than
	// something invented for it: a comparison nobody can make is an answer, and a
	// comparison made against the wrong commit is not.
	unstamped, unstampedStore := testharness.NewPipeline(t, testharness.PipelineRepository(t), &orchestratortest.Tracker{
		Item: beads.WorkItem{ID: "yoyodyne-other", Title: "Work", Status: "open"},
	}, orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, testharness.ApproveVerdict), []string{"exit 0"})
	second, err := unstamped.Run(context.Background(), "yoyodyne-other")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	quiet, err := unstampedStore.Load(second.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if quiet.Build != "" {
		t.Fatalf("recorded build = %q, want nothing where the binary stamped nothing", quiet.Build)
	}
}

// A run ending is when the item it served gets its price, whichever way the run
// went: a failed attempt spent real money, and an item priced only by the run
// that finished it would be recorded at less than it cost.
func TestPipelinePricesTheWorkItemWhenARunEnds(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		provide func(backend.RunRequest) (backend.RunResult, error)
		failed  bool
	}{
		{
			name: "a run that succeeded",
			provide: func(request backend.RunRequest) (backend.RunResult, error) {
				if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "done.txt"), []byte("done"), 0o600); err != nil {
					return backend.RunResult{}, err
				}
				return backend.RunResult{
					SessionID: "session-developer",
					FinalText: "implemented the work item",
					Process:   execution.ProcessResult{Status: execution.ProcessSucceeded},
				}, nil
			},
		},
		{
			name: "a run that failed",
			provide: func(backend.RunRequest) (backend.RunResult, error) {
				return backend.RunResult{
					SessionID:  "session-developer",
					FinalText:  "provider failed",
					IsError:    true,
					StopReason: "provider_error",
					Process:    execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1},
				}, nil
			},
			failed: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			repository := testharness.PipelineRepository(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Price it", Status: "open"}}
			pipeline, _ := testharness.NewPipeline(t, repository, tracker, &orchestratortest.Backend{Respond: test.provide}, []string{"exit 0"})
			prices := &orchestratortest.Pricer{Cost: beads.Cost{TotalUSD: 27.93, Runs: 2, UnknownRuns: 1}}
			pipeline.Prices = prices

			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			if test.failed == (err == nil) {
				t.Fatalf("Run() error = %v", err)
			}
			if len(prices.Priced) != 1 || prices.Priced[0] != tracker.Item.ID {
				t.Fatalf("priced %#v, want the item the run served", prices.Priced)
			}
			// The price is of the item across every run made for it, not of this
			// run, so what the outcome reports is what the ledger holds.
			if outcome.Cost == nil || *outcome.Cost != prices.Cost || outcome.CostProblem != "" {
				t.Fatalf("Run() cost = %#v, problem = %q", outcome.Cost, outcome.CostProblem)
			}
		})
	}
}

// The spending already happened and the run is already over, so a price nobody
// could write down is reported rather than turned into a failed run.
func TestPipelineReportsAPriceItCouldNotRecordWithoutFailingTheRun(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Price it", Status: "open"}}
	provider := &orchestratortest.Backend{Respond: func(request backend.RunRequest) (backend.RunResult, error) {
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "done.txt"), []byte("done"), 0o600); err != nil {
			return backend.RunResult{}, err
		}
		return backend.RunResult{
			SessionID: "session-developer",
			FinalText: "implemented the work item",
			Process:   execution.ProcessResult{Status: execution.ProcessSucceeded},
		}, nil
	}}
	pipeline, _ := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Prices = &orchestratortest.Pricer{Err: errors.New("bd update failed")}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("Run() status = %q, want a run the price did not fail", outcome.Status)
	}
	if !strings.Contains(outcome.CostProblem, "bd update failed") {
		t.Fatalf("Run() cost problem = %q", outcome.CostProblem)
	}
}

func TestPipelineCapturesChangesWhenBackendReturnsInfrastructureError(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Fail", Status: "open"}}
	provider := &orchestratortest.Backend{Respond: func(request backend.RunRequest) (backend.RunResult, error) {
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "partial.txt"), []byte("partial"), 0o600); err != nil {
			return backend.RunResult{}, err
		}
		return backend.RunResult{}, errors.New("malformed terminal stream")
	}}
	pipeline, _ := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), "developer backend failed") {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(outcome.Changes.Status, "A partial.txt") {
		t.Fatalf("Run() change summary = %#v", outcome.Changes)
	}
	if !strings.Contains(tracker.Notes, "Changes when the run ended:\nA partial.txt") {
		t.Fatalf("failure notes omitted the change the run had made: %q", tracker.Notes)
	}
	// The change is only worth naming because somebody can go and get it, and the
	// note says where from — checked, rather than assumed from the record having
	// no removal on it.
	if preservation := outcome.Preservation; preservation == nil || !preservation.Verified() ||
		!preservation.BranchPresent || !preservation.WorktreePresent || preservation.Lost() {
		t.Fatalf("Run() preservation = %#v, want the branch and the worktree checked and found", outcome.Preservation)
	}
	if !strings.Contains(tracker.Notes, "Worktree: "+outcome.WorktreePath+" (checked and there)") ||
		!strings.Contains(tracker.Notes, "Branch: "+outcome.Branch+" (checked and there)") {
		t.Fatalf("failure notes claim preservation without the check behind it: %q", tracker.Notes)
	}
}

// The reason a run ended is cut to the record's own bound as it is written, so a
// provider that folded its whole output into the error that killed the run still
// leaves a record the store takes and every reader can carry. It used to be the
// one recorded reason nothing bounded: the record validated, and then each reader
// with a bound of its own discovered by refusing it.
func TestAnOversizedFailureIsCutSoTheRecordStoresAndCarries(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Fail", Status: "open"}}
	verbose := strings.Repeat("the provider said this and then said it again. ", 1000)
	provider := &orchestratortest.Backend{Respond: func(request backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{}, errors.New(verbose)
	}}
	pipeline, store := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})
	docket := &testharness.MemoryDocket{}
	pipeline.Docket = testharness.DocketerOverStore(docket, store, pipeline.Config)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), "developer backend failed") {
		t.Fatalf("Run() error = %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The store validated this record on the way in, so a bound it broke would
	// have cost the run its ending rather than the tail of one message. Asked
	// again here, because that is the claim the bound is here to make.
	if err := state.Validate(); err != nil {
		t.Fatalf("the record of a verbose failure does not validate: %v", err)
	}
	if len(state.Failure) > runstate.MaxBlockerBytes {
		t.Fatalf("failure is %d bytes, which the record's own bound refuses", len(state.Failure))
	}
	if !strings.Contains(state.Failure, "developer backend failed") ||
		!strings.Contains(state.Failure, "the rest of this failure was not recorded") {
		t.Fatalf("a cut failure lost what stopped the run or did not say it was cut: %q", state.Failure)
	}
	// What the run reported and what it recorded are the same words, so the note
	// on the work item does not say something the record cannot.
	if outcome.Failure != state.Failure {
		t.Fatalf("the reported failure and the recorded one differ:\n%q\n%q", outcome.Failure, state.Failure)
	}
	// The docket is the reader that found this: an entry refused for its length is
	// a stoppage the development manager never hears about.
	if len(docket.Entries) != 1 {
		t.Fatalf("the stoppage of a verbose run reached nobody: %#v", docket.Entries)
	}
	if entry := docket.Entries[0]; strings.TrimSpace(entry.Failure) == "" {
		t.Fatalf("the entry carries no reason for the death: %#v", entry)
	}
	// And the read model every surface prints from carries it too.
	history, err := store.History(runstate.RunQuery{WorkItemID: tracker.Item.ID})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(history.Runs) != 1 || history.Runs[0].Failure != state.Failure {
		t.Fatalf("the run history does not carry the recorded reason: %#v", history.Runs)
	}
}

// What the reviewer said is cut to the record's own bound as it is written, for
// the reason the failure beside it is: a reviewer writes at whatever length it
// likes, and the docket entry that tells the development manager a run stopped
// bounds its summary to 4 KiB. Unbounded on the record, that entry was refused
// for its length and the stopped run reached nobody.
func TestAnOversizedReviewSummaryIsCutSoTheRecordStoresAndDockets(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	verbose := fmt.Sprintf(`{"decision":"repair","summary":%q,"findings":[{"severity":"blocker","message":"add the missing file","location":{"file":"feature.txt","line":1}}]}`,
		"the change misses the acceptance criteria: "+strings.Repeat("x", runstate.MaxReviewSummaryBytes*2))
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, verbose)
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	// No repair is permitted, so the first verdict is already the end of the
	// budget and the run stops with the reviewer's words on its record.
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 0
	docket := &testharness.MemoryDocket{}
	pipeline.Docket = testharness.DocketerOverStore(docket, store, pipeline.Config)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), "independent review requires repair") {
		t.Fatalf("Run() error = %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The store validated this record on the way in, so a bound it broke would
	// have cost the run its ending rather than the tail of one summary. Asked
	// again here, because that is the claim the bound is here to make.
	if err := state.Validate(); err != nil {
		t.Fatalf("the record of a verbose review does not validate: %v", err)
	}
	if len(state.ReviewSummary) > runstate.MaxReviewSummaryBytes {
		t.Fatalf("review summary is %d bytes, which the record's own bound refuses", len(state.ReviewSummary))
	}
	if !strings.HasPrefix(state.ReviewSummary, "the change misses the acceptance criteria: ") ||
		!strings.HasSuffix(state.ReviewSummary, "the rest of this summary was not recorded]") {
		t.Fatalf("a cut summary lost what the reviewer said or did not say it was cut: %q", state.ReviewSummary)
	}
	// What the run reported and what it recorded are the same words, so nothing
	// reading the outcome sees a summary the record cannot hold.
	if outcome.ReviewSummary != state.ReviewSummary {
		t.Fatalf("the reported summary and the recorded one differ: %d bytes against %d", len(outcome.ReviewSummary), len(state.ReviewSummary))
	}
	// The docket is the reader that found this: an entry refused for its length is
	// a stopped run the development manager never hears about.
	if len(docket.Entries) != 1 {
		t.Fatalf("the stoppage of a verbosely reviewed run reached nobody: %#v", docket.Entries)
	}
	if entry := docket.Entries[0]; strings.TrimSpace(entry.Summary) == "" || len(entry.Summary) > triage.MaxMessageBytes {
		t.Fatalf("the entry carries no usable review summary: %d bytes", len(entry.Summary))
	}
}

// A failure note never says work is preserved on the strength of the record
// alone. The record of a run that made a branch and a worktree carries a removal
// flag for each, and both are false for every run nothing cleaned up — including
// a run whose checkout something else took — so a note derived from them promises
// a directory it has never looked at. That is the note run-48216ea9 wrote, and
// the developer who followed it reported 23 files destroyed.
func TestPipelineFailureNoteReportsArtifactsThatAreNotThere(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Fail", Status: "open"}}
	provider := &orchestratortest.Backend{Respond: func(request backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{}, errors.New("process output exceeded 8388608 bytes")
	}}
	pipeline, _ := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})
	// The checkout goes out from under the run, which is what the convergence
	// sweep does to a settled run's checkout and what an operator does by hand.
	// Nothing about the run's own record changes when it happens.
	pipeline.Worktrees = vanishedWorktrees{WorktreeManager: pipeline.Worktrees}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil {
		t.Fatal("Run() error = nil, want the developer failure")
	}
	if outcome.Preservation == nil || !outcome.Preservation.Lost() {
		t.Fatalf("Run() preservation = %#v, want the missing worktree found", outcome.Preservation)
	}
	if !strings.Contains(tracker.Notes, "PRESERVATION FAILED") ||
		!strings.Contains(tracker.Notes, "(checked and NOT there)") {
		t.Fatalf("failure notes did not report the preservation that did not happen: %q", tracker.Notes)
	}
	// Reported loudly means reported where the run's own caller reads, not only
	// in a note somebody has to go and find.
	if !strings.Contains(err.Error(), "is already gone") {
		t.Fatalf("Run() error = %v, want the lost artifact named in the run's own failure", err)
	}
}

// vanishedWorktrees is a repository in which this run's checkout is gone by the
// time anybody asks. It answers every other question the way the manager it
// wraps does, so the run reaches its ending exactly as it otherwise would.
type vanishedWorktrees struct {
	orchestrator.WorktreeManager
}

func (w vanishedWorktrees) Observe(ctx context.Context, worktree gitworktree.Worktree) (gitworktree.Observation, error) {
	observed, err := w.WorktreeManager.Observe(ctx, worktree)
	if err != nil {
		return observed, err
	}
	observed.WorktreeRegistered = false
	observed.WorktreePresent = false
	return observed, nil
}

func TestPipelineRecordsPartialIdentityWhenWorktreeCreationFailsAfterAdd(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := &orchestratortest.Backend{}
	pipeline, store := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})
	partial := gitworktree.Worktree{
		RunID:      testharness.PipelineRunID,
		WorkItemID: tracker.Item.ID,
		Path:       "/preserved/worktree",
		Branch:     "yoyodyne/yoyodyne-task/01234567",
		BaseRef:    "HEAD",
		BaseCommit: strings.Repeat("a", 40),
	}
	pipeline.Worktrees = orchestratortest.PartialWorktreeManager{Worktree: partial, Err: errors.New("post-create inspection failed")}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), "post-create inspection failed") {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.WorktreePath != partial.Path || outcome.Branch != partial.Branch || outcome.BaseCommit != partial.BaseCommit {
		t.Fatalf("Run() discarded partial worktree identity: %#v", outcome)
	}
	state, loadErr := store.Load(outcome.RunID)
	if loadErr != nil {
		t.Fatalf("Load() error = %v", loadErr)
	}
	if state.WorktreePath != partial.Path || !strings.Contains(tracker.Notes, partial.Path) {
		t.Fatalf("state = %#v, notes = %q", state, tracker.Notes)
	}
}

func TestPipelineRefusesUnauthenticatedOrDuplicateRunBeforeClaim(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := &orchestratortest.Backend{ReportedAvailability: backend.Availability{Installed: true, Authenticated: false, AuthMethod: "none"}}
	pipeline, _ := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})
	// The refusal names the backend the developer is configured for, because
	// sending the operator to log into a provider that is not the one this run
	// would have used is a remedy for a machine that is not theirs.
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil ||
		!strings.Contains(err.Error(), `the claude-code backend is not authenticated`) {
		t.Fatalf("Run() auth error = %v", err)
	}
	if tracker.Claimed {
		t.Fatal("unauthenticated run claimed work")
	}

	provider.ReportedAvailability = backend.Availability{Installed: true, Authenticated: true}
	pipeline, store := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})
	now := time.Now().UTC()
	if err := store.Create(runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         testharness.PipelineRunID,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    tracker.Item.ID,
		Backend:       domain.BackendClaudeCode,
		Status:        runstate.StatusRunning,
		StartedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("Create() state error = %v", err)
	}
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil {
		t.Fatal("Run() duplicate error = nil")
	} else {
		var existing orchestrator.ExistingRunError
		if !errors.As(err, &existing) {
			t.Fatalf("Run() error = %T %v, want ExistingRunError", err, err)
		}
	}
	if tracker.Claimed {
		t.Fatal("duplicate run claimed work")
	}
}

func TestPipelineEnforcesConfiguredDeveloperCapacityBeforeClaim(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := &orchestratortest.Backend{ReportedAvailability: backend.Availability{Installed: true, Authenticated: true}}
	pipeline, store := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})
	now := time.Now().UTC()
	wait := &beads.SchedulingWait{Reason: "waiting for a slot", FirstPassedOver: now, ReasonSince: now}
	tracker.Item.SchedulingWait = wait
	active := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         "run-fedcba9876543210fedcba9876543210",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    "yoyodyne-other",
		Backend:       domain.BackendClaudeCode,
		Status:        runstate.StatusRunning,
		StartedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.Create(active); err != nil {
		t.Fatalf("Create() active state error = %v", err)
	}

	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil || !strings.Contains(err.Error(), "developer capacity is full") {
		t.Fatalf("Run() capacity error = %v", err)
	}
	if tracker.Item.SchedulingWait != wait {
		t.Fatal("refused dispatch cleared the scheduling wait")
	}
	if tracker.Claimed {
		t.Fatal("capacity-limited run claimed work")
	}
}

// An item waiting on unfinished work is paused before it is claimed rather than
// failed. It is a pause because closing the blocker is all that stands between
// here and the work proceeding, and because the same fact reaching a run already
// in flight parks that run: one dependency must not read as a wait in one place
// and as a broken run in another. Only the relation that blocks counts — a
// parent-child link says nothing about whether this work may proceed.
func TestPipelinePausesBlockedItemBeforeClaim(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
		ID:     "yoyodyne-task",
		Title:  "Blocked task",
		Status: "open",
		Dependencies: []beads.Dependency{
			{ID: "yoyodyne-blocker", Type: "blocks", Status: "open"},
			{ID: "yoyodyne-parent", Type: "parent-child", Status: "open"},
		},
	}}
	provider := &orchestratortest.Backend{ReportedAvailability: backend.Availability{Installed: true, Authenticated: true}}
	pipeline, store := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !outcome.Paused || outcome.PausedByDependency == nil {
		t.Fatalf("outcome = %#v, want the item paused for what it waits on", outcome)
	}
	if got := outcome.PausedByDependency.Blockers; len(got) != 1 || got[0] != "yoyodyne-blocker" {
		t.Fatalf("blockers = %v, want only the blocking dependency named", got)
	}
	if tracker.Claimed {
		t.Fatal("blocked run claimed work")
	}
	states, err := store.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete() error = %v", err)
	}
	if len(states) != 0 {
		t.Fatalf("blocked run created state: %#v", states)
	}
}

// Naming an item runs it, whatever carries it. The executor marker steers what
// the harness chooses for itself and never what the operator may ask for, which
// is the same exemption the intake hold makes: naming an item is the operator
// deciding it is the exception.
//
// It is asserted rather than left to the absence of a check, because the marker
// withholds readiness in backlog.Order — shared state this path does not consult
// today and could be made to consult by accident. If that ever changed, an
// operator would be refused the one item they had deliberately reached for, and
// the two documents that promise otherwise would be silently false.
func TestPipelineRunsAConversationExecutedItemTheOperatorNamed(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	item := beads.WorkItem{
		ID:       "yoyodyne-ifd.138",
		Title:    "Promote the brief",
		Status:   "open",
		Executor: domain.WorkItemExecutorConversation,
	}
	tracker := &orchestratortest.Tracker{Item: item}
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, testharness.ApproveVerdict)
	pipeline, store := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want a named item to run whatever carries it", err)
	}
	if outcome.Status != runstate.StatusSucceeded || !tracker.Claimed {
		t.Fatalf("Run() outcome = %#v, claimed = %v, want the named run carried out", outcome, tracker.Claimed)
	}
	if _, err := store.Load(outcome.RunID); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// The contrast, in one place: the same item the harness would never have
	// chosen for itself.
	queue := backlog.Order([]beads.WorkItem{item}, []string{item.ID}, backlog.ReadHolds(nil), nil)
	if _, ok := queue.Next(); ok {
		t.Fatal("the backlog offered an item no run carries, so the two paths no longer differ")
	}
}

func TestPipelineRevalidatesBlockersReturnedByClaim(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	tracker.OnClaim = func() error {
		tracker.Item.Dependencies = []beads.Dependency{{ID: "late-blocker", Type: "blocks", Status: "open"}}
		return nil
	}
	providerCalled := false
	provider := &orchestratortest.Backend{Respond: func(backend.RunRequest) (backend.RunResult, error) {
		providerCalled = true
		return backend.RunResult{}, nil
	}}
	pipeline, _ := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})

	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil || !strings.Contains(err.Error(), "late-blocker") {
		t.Fatalf("Run() late blocker error = %v", err)
	}
	if !tracker.Claimed || providerCalled {
		t.Fatalf("claimed = %t, provider called = %t", tracker.Claimed, providerCalled)
	}
	if !strings.Contains(tracker.Notes, "bootstrap run failed") {
		t.Fatalf("failure notes = %q", tracker.Notes)
	}
}

// An item whose notes name a path this repository does not hold still runs. The
// notes are append-only, so a reference nobody can remove from them would
// otherwise be a run nobody could ever start: the developer is told the
// reference was left out and gets on with the work.
func TestPipelineRunsAnItemWhoseNotesNameAnUnresolvableReference(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
		ID:                 "yoyodyne-task",
		Title:              "Repair the documentation",
		Description:        "Follow docs/design.md",
		AcceptanceCriteria: "The documentation matches the code",
		Status:             "open",
		Notes:              "Triage recorded a reviewer citing ../README.md, and notes are append-only.",
	}}
	var prompt string
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if request.Role == domain.RoleDeveloper {
			prompt = request.Prompt
		}
		return nil
	}, testharness.ApproveVerdict)
	pipeline, _ := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want the run to start", err)
	}
	if outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("Run() outcome = %#v, want a run that finished", outcome)
	}
	for _, want := range []string{
		"## Referenced file: ../README.md (omitted)",
		"does not resolve to a file in this repository",
		"design content",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("developer prompt omitted %q: %s", want, prompt)
		}
	}
}

// An item whose notes have outgrown the context budget still runs, and the loss
// is on the run record rather than only inside the text the developer was
// handed. Every run appends to an item's notes and nothing removes them, so the
// run where a long-lived item first stops fitting is an ordinary run nobody
// decided anything about — and if the record does not say so, nobody finds out
// that the item is being worked from part of what was recorded on it.
func TestPipelineRecordsWhatAnOversizedItemLostToTheContextBudget(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
		ID:                 "yoyodyne-task",
		Title:              "A long-lived item",
		Description:        "Add the feature",
		AcceptanceCriteria: "feature.txt exists",
		Status:             "open",
		Notes: strings.Join([]string{
			"the oldest note, from a run nobody remembers",
			strings.Repeat("a later run recorded what it did, at length. ", 8000),
			"the newest note, which is what this item is being worked from",
		}, "\n\n"),
	}}
	var delivered string
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if request.Role == domain.RoleDeveloper {
			delivered = request.Prompt
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("Run() outcome = %#v, want an oversized item to run rather than be refused", outcome)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.ContextTruncation == nil {
		t.Fatal("the run record says nothing about the notes the context dropped")
	}
	if state.ContextTruncation.DroppedNotes != 2 || state.ContextTruncation.DroppedBytes < 1 {
		t.Fatalf("recorded truncation = %#v, want the two oldest notes and what they cost", *state.ContextTruncation)
	}
	// And the developer was handed the item saying so, with the newest of the
	// notes still on it.
	if !strings.Contains(delivered, "[the oldest 2 note(s) on yoyodyne-task,") {
		t.Fatal("the developer's context does not say which notes were dropped")
	}
	if !strings.Contains(delivered, "the newest note, which is what this item is being worked from") {
		t.Fatal("the developer's context dropped the newest note")
	}
}

func TestPipelineIntegratesReviewedWorkAndClosesTheItem(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
		ID:                 "yoyodyne-task",
		Title:              "Add feature",
		Description:        "Follow docs/design.md",
		AcceptanceCriteria: "feature.txt exists",
		Status:             "open",
		RelevantGoals:      []string{"Keep the work traceable.", "Use ordinary words."},
	}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Phase != runstate.PhaseComplete || !outcome.WorkItemClosed {
		t.Fatalf("Run() outcome = %#v", outcome)
	}
	if outcome.ReviewDecision != review.DecisionApprove || outcome.ReviewSessionID != "reviewer-session" || outcome.ProviderSessionID != "developer-session" {
		t.Fatalf("Run() review evidence = %#v", outcome)
	}
	if outcome.Integration == nil || outcome.Integration.TargetBranch != "main" || outcome.Integration.PreviousTargetCommit != outcome.BaseCommit {
		t.Fatalf("Run() integration = %#v", outcome.Integration)
	}
	if outcome.Integration.SourceCommit != outcome.Integration.TargetCommit || outcome.Integration.SourceCommit == outcome.BaseCommit {
		t.Fatalf("integration did not advance the target: %#v", outcome.Integration)
	}

	// The harness, not the developer, produced the commit that main now carries.
	if head := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main"); head != outcome.Integration.TargetCommit {
		t.Fatalf("main = %q, want %q", head, outcome.Integration.TargetCommit)
	}
	if _, err := os.Stat(filepath.Join(repository, "feature.txt")); err != nil {
		t.Fatalf("integrated change is missing from the primary checkout: %v", err)
	}
	if subject := testharness.GitLine(t, repository, "log", "-1", "--format=%s", "refs/heads/main"); !strings.Contains(subject, "yoyodyne-task") {
		t.Fatalf("integration commit subject = %q", subject)
	}

	// A proven-integrated worktree and its branch are the only ones removed.
	if _, err := os.Stat(outcome.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("integrated worktree survived cleanup: %v", err)
	}
	if branches := testharness.GitOutput(t, repository, "branch", "--list", outcome.Branch); strings.TrimSpace(branches) != "" {
		t.Fatalf("integrated branch survived cleanup: %q", branches)
	}

	// Completion ordering: claim, record, close, and only then cleanup.
	if got := strings.Join(tracker.Calls, ","); got != "claim,record,complete" {
		t.Fatalf("tracker calls = %q", got)
	}
	if !tracker.Closed || !strings.Contains(tracker.CloseReason, outcome.Integration.TargetCommit) {
		t.Fatalf("tracker = %#v", tracker)
	}
	for _, want := range []string{"integrated automatically", "Reviewer session: reviewer-session", "Review decision: approve", "Integrated commit: " + outcome.Integration.SourceCommit} {
		if !strings.Contains(tracker.Notes, want) {
			t.Fatalf("notes are missing %q: %q", want, tracker.Notes)
		}
	}

	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Status != runstate.StatusSucceeded || state.Phase != runstate.PhaseComplete {
		t.Fatalf("state = %#v", state)
	}
	if state.ReviewDecision != runstate.ReviewApprove || state.ReviewSessionID != "reviewer-session" || state.ProviderSessionID != "developer-session" {
		t.Fatalf("durable review evidence = %#v", state)
	}
	if state.Integration == nil || state.Integration.TargetCommit != outcome.Integration.TargetCommit || state.Integration.SourceCommit != outcome.Integration.SourceCommit {
		t.Fatalf("durable integration evidence = %#v", state.Integration)
	}
	events, err := store.LoadEvents(outcome.RunID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	if !hasEvent(events, execution.EventReviewStarted) || !hasEvent(events, execution.EventReviewCompleted) {
		t.Fatalf("events = %#v", events)
	}

	// The reviewer is a second, independent invocation: its own session, its own
	// contract, and no ability to edit what it is judging.
	developerRequests := provider.RequestsForRole(domain.RoleDeveloper)
	reviewerRequests := provider.RequestsForRole(domain.RoleReviewer)
	if len(developerRequests) != 1 || len(reviewerRequests) != 1 {
		t.Fatalf("invocations: developer = %d, reviewer = %d", len(developerRequests), len(reviewerRequests))
	}
	for _, request := range []backend.RunRequest{developerRequests[0], reviewerRequests[0]} {
		for _, want := range append([]string{"goals the change must not break"}, tracker.Item.RelevantGoals...) {
			if !strings.Contains(request.Prompt, want) {
				t.Fatalf("%s briefing omitted %q", request.Role, want)
			}
		}
	}
	if reviewerRequests[0].SessionID != "" || len(reviewerRequests[0].AllowedTools) != 0 {
		t.Fatalf("reviewer invocation = %#v", reviewerRequests[0])
	}
	if reviewerRequests[0].Prompt == developerRequests[0].Prompt || !strings.Contains(reviewerRequests[0].Prompt, "feature.txt") {
		t.Fatalf("reviewer prompt = %q", reviewerRequests[0].Prompt)
	}
}

// The effective persona reaches the developer, and the harness contract it can
// never remove still comes first.
func TestPipelineSendsTheEffectiveDeveloperPersona(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, _ := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})
	developer := pipeline.Config.Agents["developer"]
	developer.Persona = config.Persona{
		Version: "v1",
		Path:    "personas/developer.md",
		Source:  "builtin:v1/personas/developer.md",
		Text:    "# Developer persona\n\nPrefer the smallest change that satisfies the criteria.\n",
	}
	pipeline.Config.Agents["developer"] = developer

	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	requests := provider.RequestsForRole(domain.RoleDeveloper)
	if len(requests) != 1 {
		t.Fatalf("developer invocations = %d, want 1", len(requests))
	}
	prompt := requests[0].Prompt
	contract := strings.Index(prompt, "Work only inside the current assigned worktree")
	persona := strings.Index(prompt, "Prefer the smallest change")
	if contract < 0 || persona < 0 || contract > persona {
		t.Fatalf("persona did not follow the harness contract: contract = %d, persona = %d\n%s", contract, persona, prompt)
	}
	tools := toolcatalog.RunContract(domain.RoleDeveloper)
	if strings.Count(prompt, tools) != 1 || strings.Index(prompt, tools) > persona {
		t.Fatal("the developer invocation lost its generated tools or put them below its persona")
	}
}

// A run is told where to put its scratch files, and the directory it is told
// about is one the harness actually cut for it. What this pins is the whole of
// the arrangement: the path is in the prompt, it exists by the time the prompt
// is sent, it is outside the worktree so nothing in it can enter the change, and
// it belongs to this run rather than to the machine — which is what stops two
// runs reading each other's check output back as their own verdict.
func TestTheDeveloperIsGivenAScratchDirectoryCutForItsOwnRun(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, store := testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	requests := provider.RequestsForRole(domain.RoleDeveloper)
	if len(requests) != 1 {
		t.Fatalf("developer invocations = %d, want 1", len(requests))
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// Asked for again rather than derived here: preparing is idempotent, so this
	// is the same directory the run was handed, and asking the same way the run
	// did is what makes the assertion about the run rather than about a second
	// opinion of where scratch goes.
	scratch, err := execution.PrepareScratchDirectory(pipeline.Repository, state.WorktreePath, outcome.RunID)
	if err != nil {
		t.Fatalf("PrepareScratchDirectory() error = %v", err)
	}
	if !strings.Contains(requests[0].Prompt, scratch) {
		t.Fatalf("the contract never names the run's scratch directory %q:\n%s", scratch, requests[0].Prompt)
	}
	if requests[0].RepositoryRoot != pipeline.Repository {
		t.Fatalf("developer repository root = %q, want the harness's %q", requests[0].RepositoryRoot, pipeline.Repository)
	}
	if info, err := os.Stat(scratch); err != nil || !info.IsDir() {
		t.Fatalf("stat the named scratch directory: info = %v, err = %v", info, err)
	}
	if strings.HasPrefix(scratch, state.WorktreePath+string(filepath.Separator)) {
		t.Fatalf("scratch directory %q is inside the worktree %q", scratch, state.WorktreePath)
	}
	// The advisory naming convention this replaced is gone rather than left
	// standing beside it: two mechanisms for one hazard is the one outcome the
	// directory was cut to avoid.
	if strings.Contains(requests[0].Prompt, "put the id of the work item you were given into the name of every scratch path") {
		t.Fatalf("the contract still carries the naming convention the directory replaced:\n%s", requests[0].Prompt)
	}
}
