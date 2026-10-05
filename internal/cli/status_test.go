package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// countingProcess is who a round these tests seed was charged by. A round is
// only ever given back to the process that charged it, and nothing here gives
// one back; what it needs is somebody to have charged it.
const countingProcess = "pid-1-000000000000000a"

func TestStatusCarriesStopCausesAndCountsTheWeekBeyondTheListing(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)
	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(-time.Minute)
	known := recordedRun(t, store, runstate.StatusTimedOut, "yoyodyne-known", now)
	known.StopClass = runstate.StopProviderIdle
	known.Failure = "the harness stopped the provider"
	saveRun(t, store, known)
	old := recordedRun(t, store, runstate.StatusFailed, "yoyodyne-old", now.Add(-time.Second))
	old.Failure = "make test failed" // Prose does not classify an old record.
	saveRun(t, store, old)

	stdout, stderr, code := runCLI(t, "status", "--config", configPath, "--failed", "--limit", "1", "--json")
	if code != 0 {
		t.Fatalf("status exited %d: %s", code, stderr)
	}
	var output statusOutput
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Runs) != 1 || output.Runs[0].StopClass != runstate.StopProviderIdle {
		t.Fatalf("limited runs = %+v", output.Runs)
	}
	if output.Throughput == nil {
		t.Fatal("status JSON omitted throughput")
	}
	for _, period := range output.Throughput.Windows {
		if period.Days == 7 && (len(period.StopsByCause) != 2 || period.StopsByCause[runstate.StopProviderIdle] != 1 || period.StopsByCause[runstate.StopUnknown] != 1) {
			t.Fatalf("weekly stops = %v, want both runs despite --limit 1", period.StopsByCause)
		}
	}
	stdout, stderr, code = runCLI(t, "status", "--config", configPath, "yoyodyne-old", "--json")
	if code != 0 || json.Unmarshal([]byte(stdout), &output) != nil || len(output.Runs) != 1 || output.Runs[0].StopClass != runstate.StopUnknown {
		t.Fatalf("old record's cause: code %d, stdout %s, stderr %s", code, stdout, stderr)
	}
	stdout, stderr, code = runCLI(t, "status", "--config", configPath, "--failed")
	if code != 0 || !strings.Contains(stdout, "stop cause: provider-idle") || !strings.Contains(stdout, "stop cause: unknown") {
		t.Fatalf("cause names missing from status: code %d, stdout %s, stderr %s", code, stdout, stderr)
	}
}

// The failure an operator is chasing is already recorded; what was missing was
// any way to read it back without going through the tracker item by item. So the
// verb has to reach the records a run actually wrote, name the item, and carry
// the reason.
func TestStatusReportsFailedRunsWithTheirReasons(t *testing.T) {
	// Not parallel: the state root the command addresses is set here, and the
	// records it reads are written under it.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	// Nothing recorded is an answer rather than an empty listing.
	stdout, stderr, code := runCLI(t, "status", "--config", configPath)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "no recorded runs") {
		t.Fatalf("stdout = %q", stdout)
	}

	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	started := time.Date(2026, 8, 16, 8, 0, 0, 0, time.UTC)
	// A review nobody repaired, recorded the way the harness records one: the
	// item carries a blocker, and the branch and worktree that hold the change
	// are still there. A fixture without them would be a run this listing cannot
	// produce, and asserting over it would enshrine the claim the vocabulary
	// exists to remove.
	failed := recordedRun(t, store, runstate.StatusFailed, "yoyodyne-ifd.2.7", started.Add(time.Hour))
	failed.Phase = runstate.PhaseReviewing
	failed.WorktreePath = "/state/worktrees/yoyodyne-ifd-2-7"
	failed.Branch = "yoyodyne/yoyodyne-ifd.2.7/0123abcd"
	failed.BaseCommit = strings.Repeat("a", 40)
	failed.ProviderSessionID = "session-developer"
	failed.Blocker = "Yoyodyne blocked this item: independent review requires repair"
	failed.Failure = "independent review requires repair after 2 of 2 permitted attempt(s):\nthe change does not do what the item asked"
	saveRun(t, store, failed)
	// A failed attempt spent real money, and the listing prices it from the same
	// event log the ledger prices from.
	appendRunCost(t, store, failed.RunID, 8.91)
	succeeded := recordedRun(t, store, runstate.StatusSucceeded, "yoyodyne-ifd.41", started)
	succeeded.Phase = runstate.PhaseComplete
	saveRun(t, store, succeeded)
	appendRunCost(t, store, succeeded.RunID, 19.02)

	stdout, stderr, code = runCLI(t, "status", "--config", configPath)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"2 of 2 shown",
		failed.RunID,
		"yoyodyne-ifd.2.7",
		// The listing looks in the repository rather than reading the run's
		// flags, and this fixture's branch is not one the repository would own,
		// so the look is refused and the listing says it could not check rather
		// than calling the change preserved on the record's word.
		"[stopped, reviewing, work possibly preserved, not checked]",
		"branch (not checked: the repository could not be asked",
		"): yoyodyne/yoyodyne-ifd.2.7/0123abcd",
		"): /state/worktrees/yoyodyne-ifd-2-7",
		"reason: independent review requires repair",
		"$8.91",
		"yoyodyne-ifd.41",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
	// A reason written over several lines is folded onto one, so a listing of
	// runs stays a listing.
	folded := "reason: independent review requires repair after 2 of 2 permitted attempt(s): the change does not do what the item asked"
	if !strings.Contains(stdout, folded) {
		t.Fatalf("a multi-line reason was not folded onto one line: %q", stdout)
	}

	// Asking for the failures leaves the successful run out, and reporting a
	// failure is not itself a failure of the command.
	stdout, stderr, code = runCLI(t, "status", "--config", configPath, "--failed")
	if code != 0 {
		t.Fatalf("status --failed code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(stdout, succeeded.RunID) {
		t.Fatalf("a succeeded run reached --failed: %q", stdout)
	}
	if !strings.Contains(stdout, "runs that ended without succeeding, 1 of 1 shown") {
		t.Fatalf("stdout = %q", stdout)
	}

	// A script reads the same records, with the whole reason rather than the
	// line the listing shows.
	stdout, stderr, code = runCLI(t, "status", "--config", configPath, "--failed", "--json")
	if code != 0 {
		t.Fatalf("status --json code = %d, stderr = %q", code, stderr)
	}
	var decoded statusOutput
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if decoded.Matched != 1 || decoded.Recorded != 2 || len(decoded.Runs) != 1 {
		t.Fatalf("decoded = %#v", decoded)
	}
	if decoded.Runs[0].Failure != failed.Failure {
		t.Fatalf("JSON reason = %q, want the whole recorded reason", decoded.Runs[0].Failure)
	}
}

// Naming an item reports its runs, and a limited listing has to say what it was
// limited from: a listing that reads as the whole record while being a fifth of
// it is worse than no listing.
func TestStatusNarrowsToOneItemAndSaysWhatALimitLeftOut(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	started := time.Date(2026, 8, 16, 8, 0, 0, 0, time.UTC)
	for index := 0; index < 3; index++ {
		run := recordedRun(t, store, runstate.StatusFailed, "yoyodyne-ifd.2.7", started.Add(time.Duration(index)*time.Hour))
		run.Failure = "developer reported failure: api_error"
		saveRun(t, store, run)
	}
	other := recordedRun(t, store, runstate.StatusFailed, "yoyodyne-ifd.41", started)
	other.Failure = "primary checkout is not ready for integration"
	saveRun(t, store, other)

	stdout, stderr, code := runCLI(t, "status", "--config", configPath, "--limit", "2")
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "2 of 4 shown") || !strings.Contains(stdout, "2 further run(s) are not listed here") {
		t.Fatalf("stdout = %q", stdout)
	}

	stdout, stderr, code = runCLI(t, "status", "--config", configPath, "yoyodyne-ifd.41")
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "runs of title unavailable (yoyodyne-ifd.41), 1 of 1 shown") {
		t.Fatalf("stdout = %q", stdout)
	}
	if strings.Contains(stdout, "api_error") {
		t.Fatalf("another item's run reached a listing of one item: %q", stdout)
	}

	// An item the harness has never run says so against the record it was read
	// out of, rather than reading as though nothing has ever run.
	stdout, stderr, code = runCLI(t, "status", "--config", configPath, "yoyodyne-ifd.99")
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "no runs of title unavailable (yoyodyne-ifd.99), of the 4 run(s) recorded") {
		t.Fatalf("stdout = %q", stdout)
	}
}

// Which gate stopped a run is the first word of its reason, so a run the
// provider killed with a check failure still on its record reads as the
// provider's stop, and a succeeded run whose completion record arrived late
// says so as its reason without the same words printed twice.
func TestStatusLeadsTheReasonWithTheClassThatStoppedTheRun(t *testing.T) {
	t.Parallel()

	completedAt := time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	printRunHistory(&out, runstate.RunHistory{
		Matched:  2,
		Recorded: 2,
		Runs: []runstate.RunSummary{
			{
				RunID:       "run-0123456789abcdef0123456789abcdef",
				WorkItemID:  "yoyodyne-ifd.90",
				Status:      runstate.StatusFailed,
				Outcome:     runstate.OutcomeStopped,
				Phase:       runstate.PhaseDeveloping,
				StartedAt:   completedAt,
				CompletedAt: &completedAt,
				Failure:     "the provider ended this run without judging the work after 2 of 2 permitted relaunch(es): signal: killed",
				StopClass:   runstate.StopProvider,
				FailingCheck: &runstate.CheckFailure{
					Command:  "make test",
					ExitCode: 2,
				},
			},
			{
				RunID:                      "run-fedcba9876543210fedcba9876543210",
				WorkItemID:                 "yoyodyne-ifd.173",
				Status:                     runstate.StatusSucceeded,
				Outcome:                    runstate.OutcomeSucceeded,
				Phase:                      runstate.PhaseComplete,
				StartedAt:                  completedAt,
				CompletedAt:                &completedAt,
				Integrated:                 true,
				WorktreeRemoved:            true,
				BranchRemoved:              true,
				CompletionRecordingFailure: "save completed run state after cleanup: disk full",
				StopClass:                  runstate.StopRecording,
			},
		},
	}, "", false)
	rendered := out.String()
	for _, want := range []string{
		"reason: provider: the provider ended this run without judging the work",
		"failing check: make test exited 2",
		"reason: recording: save completed run state after cleanup: disk full",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered = %q, want it to contain %q", rendered, want)
		}
	}
	if strings.Contains(rendered, "completion recorded late:") {
		t.Fatalf("the late completion record was printed again beside the reason it already is: %q", rendered)
	}
}

// The bookkeeping failures are recorded apart from the run's own so neither can
// masquerade as the other, and the listing has to keep them apart: an
// outstanding cleanup on an integrated run is not a piece of work that failed.
func TestStatusNamesEachRecordedReasonForWhatItIs(t *testing.T) {
	t.Parallel()

	completedAt := time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	printRunHistory(&out, runstate.RunHistory{
		Matched:  2,
		Recorded: 2,
		Runs: []runstate.RunSummary{
			{
				RunID:                      "run-0123456789abcdef0123456789abcdef",
				WorkItemID:                 "yoyodyne-ifd.41",
				Status:                     runstate.StatusSucceeded,
				Outcome:                    runstate.OutcomeSucceeded,
				Phase:                      runstate.PhaseCleaningUp,
				StartedAt:                  completedAt,
				CompletedAt:                &completedAt,
				Integrated:                 true,
				Outstanding:                true,
				CleanupFailure:             "remove worktree: directory is busy",
				PublishFailure:             "push branch: remote rejected",
				CompletionRecordingFailure: "save completed run state after cleanup: disk full",
				CostUSD:                    19.02,
				Invocations:                2,
			},
			{
				RunID:      "run-fedcba9876543210fedcba9876543210",
				WorkItemID: "yoyodyne-ifd.2.7",
				Status:     runstate.StatusRunning,
				// A run that has not reached a terminal status keeps its own
				// status word, which is already the fact a reader wants there.
				Outcome:   runstate.RunOutcome(runstate.StatusRunning),
				Phase:     runstate.PhaseChecking,
				StartedAt: completedAt,
				// Every run still in flight owes its own remaining steps, so
				// saying so of this one would say nothing.
				Outstanding: true,
				FailingCheck: &runstate.CheckFailure{
					Command:  "make test",
					ExitCode: 2,
					Output:   "FAIL",
				},
				CostUSD: 3.5,
			},
		},
	}, "", false)
	rendered := out.String()
	for _, want := range []string{
		"[succeeded, cleaning_up, integrated, outstanding] $19.02",
		"outstanding publication: push branch: remote rejected",
		"outstanding cleanup: remove worktree: directory is busy",
		"completion recorded late: save completed run state after cleanup: disk full",
		// The marker in the brackets is never left for a reader to interpret:
		// what the run owes is said under it.
		"outstanding: its work is promoted, and cleaning up after it is not recorded as finished",
		// A run still going has not finished spending, so its figure says so.
		"[running, checking] $3.50 so far",
		"failing check: make test exited 2",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered = %q, want it to contain %q", rendered, want)
		}
	}
	// Nothing here failed, so nothing may be printed as the run's own reason.
	if strings.Contains(rendered, "reason:") {
		t.Fatalf("a bookkeeping failure was reported as a run failure: %q", rendered)
	}

	// A run whose evidence is gone is stated as unpriceable rather than as free.
	out.Reset()
	printRunHistory(&out, runstate.RunHistory{
		Matched:  1,
		Recorded: 1,
		Runs: []runstate.RunSummary{{
			RunID:       "run-0123456789abcdef0123456789abcdef",
			WorkItemID:  "yoyodyne-ifd.41",
			Status:      runstate.StatusFailed,
			Outcome:     runstate.OutcomeFailed,
			StartedAt:   completedAt,
			CompletedAt: &completedAt,
			UnknownCost: "the run's event log is no longer recorded",
		}},
	}, "", true)
	if !strings.Contains(out.String(), "cost unknown") {
		t.Fatalf("rendered = %q", out.String())
	}
}

// The declarative soak is counted off the divergences its runs record, so a run
// that recorded one has to say so where the run is read. It is not one of the
// reasons a run ended and must never render as one: the work landed, and what
// diverged is the observation standing beside it.
func TestStatusNamesADeclarativeDivergenceWithoutCallingItAFailure(t *testing.T) {
	t.Parallel()

	completedAt := time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	printRunHistory(&out, runstate.RunHistory{
		Matched:  1,
		Recorded: 1,
		Runs: []runstate.RunSummary{{
			RunID:              "run-0123456789abcdef0123456789abcdef",
			WorkItemID:         "yoyodyne-ifd.209.6",
			Status:             runstate.StatusSucceeded,
			Outcome:            runstate.OutcomeSucceeded,
			Phase:              runstate.PhaseComplete,
			StartedAt:          completedAt,
			CompletedAt:        &completedAt,
			Integrated:         true,
			WorkflowInstanceID: "run-0123456789abcdef0123456789abcdef-delivery",
			WorkflowDivergence: `the run performed "check" and produced "passed", and its instance stands in "review"`,
			CostUSD:            1.5,
		}},
	}, "", false)
	rendered := out.String()
	if !strings.Contains(rendered, `workflow divergence: the run performed "check" and produced "passed", and its instance stands in "review"`) {
		t.Fatalf("rendered = %q, want the divergence named", rendered)
	}
	if strings.Contains(rendered, "reason:") {
		t.Fatalf("a divergence was reported as the reason a run ended: %q", rendered)
	}
}

// The run nothing observed is the one the same count reads as clean, because it
// has no divergence and never could have had one. It is named here for that
// reason and, like a divergence, is not one of the reasons a run ended.
func TestStatusNamesARunNothingObserved(t *testing.T) {
	t.Parallel()

	completedAt := time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	printRunHistory(&out, runstate.RunHistory{
		Matched:  1,
		Recorded: 1,
		Runs: []runstate.RunSummary{{
			RunID:       "run-0123456789abcdef0123456789abcdef",
			WorkItemID:  "yoyodyne-ifd.209.28",
			Status:      runstate.StatusSucceeded,
			Outcome:     runstate.OutcomeSucceeded,
			Phase:       runstate.PhaseComplete,
			StartedAt:   completedAt,
			CompletedAt: &completedAt,
			Integrated:  true,
			WorkflowUnobserved: "the instance this run would be observed through could not be created: " +
				"create workflow instance: workflow instance already exists",
			CostUSD: 1.5,
		}},
	}, "", false)
	rendered := out.String()
	if !strings.Contains(rendered, "workflow unobserved: the instance this run would be observed through could not be created") {
		t.Fatalf("rendered = %q, want the run said to be unobserved, with the cause", rendered)
	}
	if strings.Contains(rendered, "reason:") {
		t.Fatalf("an unobserved run was reported as the reason a run ended: %q", rendered)
	}
}

// A run the check stage's bound stopped says so under its reason, with what the
// stage spent and the check it was on; a run in its checks says how much of the
// bound has gone; and a run that landed says what its landing checks made of
// the commit, because a red landing is the one fact about a landed change that
// the run's own ending does not carry.
func TestStatusSaysWhereTheCheckStageStoodAndWhatTheLandingMade(t *testing.T) {
	t.Parallel()

	completedAt := time.Date(2026, 9, 19, 9, 0, 0, 0, time.UTC)
	stopped := completedAt
	landed := completedAt.Add(20 * time.Minute)
	var out bytes.Buffer
	printRunHistory(&out, runstate.RunHistory{
		Matched:  3,
		Recorded: 3,
		Runs: []runstate.RunSummary{{
			RunID:       "run-0123456789abcdef0123456789abcdef",
			WorkItemID:  "yoyodyne-ifd.389",
			Status:      runstate.StatusTimedOut,
			Outcome:     runstate.OutcomeTimedOut,
			Phase:       runstate.PhaseChecking,
			StartedAt:   completedAt.Add(-time.Hour),
			CompletedAt: &completedAt,
			Failure:     "the check stage reached its 30m0s execution.check_stage_timeout bound during make race",
			CheckStage: &runstate.CheckStage{
				StartedAt: completedAt.Add(-30 * time.Minute), BoundSeconds: 1800, Command: "make race",
				FinishedAt: &stopped, ElapsedSeconds: 1800, StoppedAtBound: true,
			},
		}, {
			RunID:      "run-1123456789abcdef0123456789abcdef",
			WorkItemID: "yoyodyne-ifd.401",
			Status:     runstate.StatusRunning,
			Phase:      runstate.PhaseChecking,
			StartedAt:  time.Now().Add(-time.Hour),
			CheckStage: &runstate.CheckStage{StartedAt: time.Now().Add(-14 * time.Minute), BoundSeconds: 1800, Command: "make test"},
		}, {
			RunID:       "run-2123456789abcdef0123456789abcdef",
			WorkItemID:  "yoyodyne-ifd.400",
			Status:      runstate.StatusSucceeded,
			Outcome:     runstate.OutcomeSucceeded,
			Phase:       runstate.PhaseComplete,
			StartedAt:   completedAt.Add(-time.Hour),
			CompletedAt: &completedAt,
			Integrated:  true,
			LandingChecks: &runstate.LandingChecks{
				Commit: strings.Repeat("d", 40), StartedAt: completedAt, FinishedAt: &landed, BoundSeconds: 1800, Ran: true,
				Checks:        []runstate.LandingCheckResult{{Command: "make race", ExitCode: 2}},
				FiledWorkItem: "yoyodyne-ifd.402",
			},
		}},
	}, "", false)
	rendered := out.String()
	for _, want := range []string{
		"  checks: 30m of 30m, stopped at the bound during make race\n",
		"  checks: 14m of 30m, on make test\n",
		"  red landing: make race exited 2 over dddddddddddd; filed as yoyodyne-ifd.402\n",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered = %q, want %q", rendered, want)
		}
	}
}

// A proposal or a report the harness could not keep is named on the run, because
// this record is the only place it survives: the outcome that used to carry it
// alone was printed by `yoyo run` and gone, so a refused proposal read afterwards
// as one never made. Neither is a reason the run ended.
func TestStatusNamesWhatARunCouldNotKeepOfItsReportsAndProposals(t *testing.T) {
	t.Parallel()

	completedAt := time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	printRunHistory(&out, runstate.RunHistory{
		Matched:  1,
		Recorded: 1,
		Runs: []runstate.RunSummary{{
			RunID:            "run-0123456789abcdef0123456789abcdef",
			WorkItemID:       "yoyodyne-ifd.255",
			Status:           runstate.StatusSucceeded,
			Outcome:          runstate.OutcomeSucceeded,
			Phase:            runstate.PhaseComplete,
			StartedAt:        completedAt,
			CompletedAt:      &completedAt,
			Integrated:       true,
			ReportProblem:    "a developer report was not collected: severity \"loud\" is not one of critical, warning, note",
			AmendmentProblem: "a change the developer proposed was not recorded: invented: no artifact answers to that id",
			CostUSD:          1.5,
		}},
	}, "", false)
	rendered := out.String()
	if !strings.Contains(rendered, "report not kept: a developer report was not collected: severity") {
		t.Fatalf("rendered = %q, want the lost report named", rendered)
	}
	if !strings.Contains(rendered, "proposal not kept: a change the developer proposed was not recorded: invented: no artifact answers to that id") {
		t.Fatalf("rendered = %q, want the refused proposal named", rendered)
	}
	if strings.Contains(rendered, "reason:") {
		t.Fatalf("a lost report or proposal was reported as the reason a run ended: %q", rendered)
	}
}

// A restatement the amendment channel dropped is kept on the run's record and
// nowhere else, so it is printed under the run with what it was folded into and
// how alike the two read — the raised proposal beside it is not, since that one
// is in the amendment log already.
func TestStatusNamesTheRestatementsARunDropped(t *testing.T) {
	t.Parallel()

	completedAt := time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	printRunHistory(&out, runstate.RunHistory{
		Matched:  1,
		Recorded: 1,
		Runs: []runstate.RunSummary{{
			RunID:       "run-0123456789abcdef0123456789abcdef",
			WorkItemID:  "yoyodyne-ifd.429.19",
			Status:      runstate.StatusSucceeded,
			Outcome:     runstate.OutcomeSucceeded,
			Phase:       runstate.PhaseComplete,
			StartedAt:   completedAt,
			CompletedAt: &completedAt,
			Integrated:  true,
			Amendments: []runstate.RunAmendment{
				{Role: domain.RoleDeveloper, Artifact: "v1-design", Change: "say which ordering holds", ID: "amendment-0123456789abcdef0123456789abcdef"},
				{Role: domain.RoleDeveloper, Artifact: "v1-design", Change: "state which of the orderings holds", FoldedInto: "amendment-0123456789abcdef0123456789abcdef", Likeness: 0.5},
			},
		}},
	}, "", false)
	rendered := out.String()
	want := "  restatement dropped: the developer's change to v1-design was folded into amendment-0123456789abcdef0123456789abcdef (likeness 0.50): state which of the orderings holds\n"
	if !strings.Contains(rendered, want) {
		t.Fatalf("rendered = %q, want %q", rendered, want)
	}
	if strings.Count(rendered, "restatement dropped:") != 1 {
		t.Fatalf("the raised proposal was printed as a dropped one: %q", rendered)
	}
}

// A stale blocked status the claim cleared is said on the run whichever way the
// clear ended, in the read model's own words, because the ending that matters
// is the one the reason line alone reads as a run that died at the claim: no
// read confirmed the clear and the item was left for the next pull.
func TestStatusSaysWhichEndingAStaleBlockClearHad(t *testing.T) {
	t.Parallel()

	completedAt := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	printRunHistory(&out, runstate.RunHistory{
		Matched:  1,
		Recorded: 1,
		Runs: []runstate.RunSummary{{
			RunID:           "run-0123456789abcdef0123456789abcdef",
			WorkItemID:      "yoyodyne-ifd.415",
			Status:          runstate.StatusFailed,
			Outcome:         runstate.OutcomeFailed,
			StartedAt:       completedAt,
			CompletedAt:     &completedAt,
			Failure:         "claim work item: the clear of the stale blocked status on yoyodyne-ifd.415 was never confirmed",
			StaleBlockClear: &runstate.StaleBlockClear{Outcome: domain.StaleBlockClearUnconfirmed, Reads: 5, Status: "blocked"},
		}},
	}, "", false)
	rendered := out.String()
	if !strings.Contains(rendered, `stale blocked status at the claim: no read confirmed the clear: 5 read(s) returned status "blocked" rather than open, and the item was left for the next pull`) {
		t.Fatalf("rendered = %q, want the unconfirmed clear said with what the tracker returned", rendered)
	}
	// Never as cleared: the line's label must not assert the clear the tracker
	// never confirmed.
	if strings.Contains(rendered, "cleared") {
		t.Fatalf("rendered = %q, reports an unconfirmed clear as cleared", rendered)
	}
}

// A run marked outstanding with nothing under it is the "go and read the run's
// JSON" case this verb exists to remove, and the marker has two causes worth
// telling apart: cleanup that never finished, and a merge the forge queued and
// has not performed. The second outlives the run by minutes or hours, so a run
// carrying it looks finished in every other respect.
func TestStatusSaysWhatAnOutstandingRunStillOwes(t *testing.T) {
	t.Parallel()

	completedAt := time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)
	finished := runstate.RunSummary{
		RunID:       "run-0123456789abcdef0123456789abcdef",
		WorkItemID:  "yoyodyne-ifd.41",
		Status:      runstate.StatusSucceeded,
		Outcome:     runstate.OutcomeSucceeded,
		Phase:       runstate.PhaseComplete,
		StartedAt:   completedAt,
		CompletedAt: &completedAt,
		Integrated:  true,
		Outstanding: true,
		MergeQueued: true,
		CostUSD:     19.02,
	}
	var out bytes.Buffer
	printRunHistory(&out, runstate.RunHistory{Matched: 1, Recorded: 1, Runs: []runstate.RunSummary{finished}}, "", false)
	if !strings.Contains(out.String(), "outstanding: the forge queued the merge of its pull request") {
		t.Fatalf("rendered = %q", out.String())
	}
	// Cleanup is finished on this one, so nothing may claim otherwise.
	if strings.Contains(out.String(), "cleaning up after it is not recorded as finished") {
		t.Fatalf("a complete cleanup was reported as unfinished: %q", out.String())
	}

	// A run that owes something the rendering cannot name is still reported as
	// owing something, rather than marked outstanding and left bare.
	unexplained := finished
	unexplained.Integrated = false
	unexplained.MergeQueued = false
	out.Reset()
	printRunHistory(&out, runstate.RunHistory{Matched: 1, Recorded: 1, Runs: []runstate.RunSummary{unexplained}}, "", false)
	if !strings.Contains(out.String(), "outstanding: it still owes a step") {
		t.Fatalf("rendered = %q", out.String())
	}

	// Every run still in flight owes its remaining steps, so saying so of one
	// would be noise rather than news.
	running := finished
	running.Status = runstate.StatusRunning
	running.Outcome = runstate.RunOutcome(runstate.StatusRunning)
	running.CompletedAt = nil
	running.Phase = runstate.PhaseDeveloping
	out.Reset()
	printRunHistory(&out, runstate.RunHistory{Matched: 1, Recorded: 1, Runs: []runstate.RunSummary{running}}, "", false)
	if strings.Contains(out.String(), "outstanding") {
		t.Fatalf("an in-flight run was reported as outstanding: %q", out.String())
	}
}

// A reason is prose somebody wrote, and a reviewer's verdict runs to
// paragraphs. The listing bounds it so a run stays one row, and cuts on a rune
// boundary: this repository's own reasons are full of em dashes, and half a
// rune is not a shorter reason but a broken one.
func TestStatusBoundsAReasonWithoutBreakingARune(t *testing.T) {
	t.Parallel()

	completedAt := time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)
	// Em dashes laid so that the byte the bound falls on is inside one.
	verdict := "independent review requires repair " + strings.Repeat("—", 200)
	var out bytes.Buffer
	printRunHistory(&out, runstate.RunHistory{Matched: 1, Recorded: 1, Runs: []runstate.RunSummary{{
		RunID:       "run-0123456789abcdef0123456789abcdef",
		WorkItemID:  "yoyodyne-ifd.41",
		Status:      runstate.StatusFailed,
		Outcome:     runstate.OutcomeFailed,
		StartedAt:   completedAt,
		CompletedAt: &completedAt,
		Failure:     verdict,
	}}}, "", true)
	rendered := out.String()
	if !utf8.ValidString(rendered) {
		t.Fatalf("the listing is not valid UTF-8: %q", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "reason:") {
			continue
		}
		if !strings.HasSuffix(line, "...") {
			t.Fatalf("an unbounded reason was listed: %q", line)
		}
		if len(line) > maxSingleLineBytes+32 {
			t.Fatalf("the listed reason is %d bytes: %q", len(line), line)
		}
	}
}

// The operator read three stopped runs' lines and asked whether the runs had
// been discarded. Every one of them said "failed", which is true of the attempt
// and says nothing about the work: their branches, worktrees, and sessions were
// all still there and their items were back in his hands. So this replays that
// listing — three stoppages, an operator cancel, and a run that broke before it
// made anything — and holds it to saying what happened and what remains.
func TestStatusSaysWhatBecameOfTheWorkRatherThanOneWordForEveryEnding(t *testing.T) {
	t.Parallel()

	completedAt := time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC)
	stopped := func(runID, item string, phase runstate.Phase, reason string) runstate.RunSummary {
		return runstate.RunSummary{
			RunID:        runID,
			WorkItemID:   item,
			Status:       runstate.StatusFailed,
			Outcome:      runstate.OutcomeStopped,
			Phase:        phase,
			StartedAt:    completedAt,
			CompletedAt:  &completedAt,
			Branch:       "yoyodyne/" + item,
			WorktreePath: "/state/worktrees/" + item,
			Failure:      reason,
		}
	}
	review := stopped("run-1111111111111111111111111111aa", "yoyodyne-ifd.90", runstate.PhaseReviewing,
		"independent review requires repair after 2 of 2 permitted attempt(s)")
	review.ProviderSessionID = "session-developer"
	review.ReviewFindings = 3
	replay := stopped("run-2222222222222222222222222222bb", "yoyodyne-ifd.91", runstate.PhaseIntegrating,
		"the change cannot be replayed onto main")
	provider := stopped("run-3333333333333333333333333333cc", "yoyodyne-ifd.92", runstate.PhaseDeveloping,
		"the provider ended this run without judging the work after 3 of 3 permitted relaunch(es)")
	cancelled := stopped("run-4444444444444444444444444444dd", "yoyodyne-ifd.93", runstate.PhaseDeveloping,
		"the operator stopped this run")
	cancelled.Status = runstate.StatusCancelled
	cancelled.Outcome = runstate.OutcomeCancelled
	broke := runstate.RunSummary{
		RunID:       "run-5555555555555555555555555555ee",
		WorkItemID:  "yoyodyne-ifd.94",
		Status:      runstate.StatusFailed,
		Outcome:     runstate.OutcomeFailed,
		StartedAt:   completedAt,
		CompletedAt: &completedAt,
		Failure:     "create isolated worktree: primary checkout is not ready",
	}

	var out bytes.Buffer
	printRunHistory(&out, runstate.RunHistory{
		Matched:  5,
		Recorded: 5,
		Runs:     []runstate.RunSummary{review, replay, provider, cancelled, broke},
	}, "", true)
	rendered := out.String()
	for _, want := range []string{
		// The three stoppages read as stoppages, each saying where it stopped,
		// and each saying the work is still there.
		"[stopped, reviewing, work preserved]",
		"[stopped, integrating, work preserved]",
		"[stopped, developing, work preserved]",
		// An operator cancel is its own word: nothing judged the change.
		"[cancelled, developing, work preserved]",
		// And the one run that really did fail keeps the bare word. What it left
		// behind is stated as an absence in the record rather than as a promise
		// that nothing was made, which is a claim two empty fields cannot carry.
		"[failed, no artifacts recorded]",
		// What remains is named rather than left to the run's JSON.
		"preserved branch: yoyodyne/yoyodyne-ifd.90",
		"preserved worktree: /state/worktrees/yoyodyne-ifd.90",
		"preserved developer session: session-developer",
		"3 review finding(s) recorded against the preserved change",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered = %q, want it to contain %q", rendered, want)
		}
	}
	// The word that started this must not describe a run whose work is preserved
	// and whose item is back with a person.
	for _, line := range strings.Split(rendered, "\n") {
		if !strings.Contains(line, "run-1111") && !strings.Contains(line, "run-4444") {
			continue
		}
		if strings.Contains(line, "[failed") {
			t.Fatalf("a preserved stoppage still reads as a failure: %q", line)
		}
	}
	// A run that left nothing behind is never sent anywhere to look at it.
	if strings.Contains(rendered, "preserved branch: yoyodyne/yoyodyne-ifd.94") {
		t.Fatalf("a run with no artifacts was reported as preserving some: %q", rendered)
	}

	// An artifact the harness recorded removing is named as removed rather than
	// dropped: telling somebody nothing was preserved and sending them to a
	// worktree that is gone are the same failure in opposite directions.
	retired := review
	retired.RunID = "run-6666666666666666666666666666ff"
	retired.BranchRemoved = true
	retired.WorktreeRemoved = true
	out.Reset()
	printRunHistory(&out, runstate.RunHistory{Matched: 1, Recorded: 1, Runs: []runstate.RunSummary{retired}}, "", true)
	rendered = out.String()
	for _, want := range []string{
		"[stopped, reviewing, work removed]",
		"branch already removed: yoyodyne/yoyodyne-ifd.90",
		"worktree already removed: /state/worktrees/yoyodyne-ifd.90",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered = %q, want it to contain %q", rendered, want)
		}
	}
	// Continuing in a session whose change is gone continues nothing, so it is
	// not offered.
	if strings.Contains(rendered, "preserved developer session") {
		t.Fatalf("a removed change was offered its session back: %q", rendered)
	}

	// A run that landed its work is not described in this vocabulary at all: it
	// removes what it made on purpose, and reporting that as a loss would report
	// the harness working as something to look at.
	landed := runstate.RunSummary{
		RunID:       "run-7777777777777777777777777777aa",
		WorkItemID:  "yoyodyne-ifd.95",
		Status:      runstate.StatusSucceeded,
		Outcome:     runstate.OutcomeSucceeded,
		Phase:       runstate.PhaseComplete,
		StartedAt:   completedAt,
		CompletedAt: &completedAt,
		Integrated:  true,
		Branch:      "yoyodyne/yoyodyne-ifd.95",
	}
	out.Reset()
	printRunHistory(&out, runstate.RunHistory{Matched: 1, Recorded: 1, Runs: []runstate.RunSummary{landed}}, "", false)
	rendered = out.String()
	if !strings.Contains(rendered, "[succeeded, complete, integrated]") {
		t.Fatalf("rendered = %q, want a landed run described as itself", rendered)
	}
	if strings.Contains(rendered, "preserve") || strings.Contains(rendered, "removed") {
		t.Fatalf("a successful run was described in the stopped-work vocabulary: %q", rendered)
	}
}

// An approved change the environment stopped is said beside its reason, naming
// the one verb that resumes it at no cost; and the run promoting again after
// that says so in the read model's own phrase in place of the bare phase.
func TestStatusNamesAnIntegrationStopAndARunResumingItsIntegration(t *testing.T) {
	t.Parallel()

	completedAt := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)
	stopped := runstate.RunSummary{
		RunID:        "run-152784a8226c6b4d3601b858dd546acf",
		WorkItemID:   "yoyodyne-ifd.309",
		Status:       runstate.StatusFailed,
		Outcome:      runstate.OutcomeFailed,
		Phase:        runstate.PhaseIntegrating,
		StartedAt:    completedAt,
		CompletedAt:  &completedAt,
		Branch:       "yoyodyne/yoyodyne-ifd-309/152784a8",
		WorktreePath: "/state/worktrees/yoyodyne-ifd-309-152784a8",
		Failure:      "integrate approved change: primary checkout is not ready for integration: primary repository has uncommitted changes: AGENTS.md",
		IntegrationStop: &runstate.IntegrationStop{
			Cause: runstate.CauseDirtyPrimary, Phase: runstate.PhaseIntegrating, RecordedAt: completedAt,
		},
	}
	resuming := runstate.RunSummary{
		RunID:               "run-152784a8226c6b4d3601b858dd546acf",
		WorkItemID:          "yoyodyne-ifd.309",
		Status:              runstate.StatusRunning,
		Outcome:             runstate.RunOutcome(runstate.StatusRunning),
		Phase:               runstate.PhaseIntegrating,
		StartedAt:           completedAt,
		ResumingIntegration: true,
	}

	var out bytes.Buffer
	printRunHistory(&out, runstate.RunHistory{Matched: 1, Recorded: 1, Runs: []runstate.RunSummary{stopped}}, "", true)
	rendered := out.String()
	for _, want := range []string{
		"[failed, integrating, work preserved]",
		"integration stop: approved, then stopped at the integrating phase by the environment: dirty-primary (the primary checkout carried state the harness does not own); `yoyo triage resume run-152784a8226c6b4d3601b858dd546acf` resumes it at no cost once the cause has cleared",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered = %q, want it to contain %q", rendered, want)
		}
	}

	out.Reset()
	printRunHistory(&out, runstate.RunHistory{Matched: 1, Recorded: 1, Runs: []runstate.RunSummary{resuming}}, "", false)
	rendered = out.String()
	if !strings.Contains(rendered, "[running, "+runstate.ResumingIntegrationSays+"]") {
		t.Fatalf("rendered = %q, want the resumed run said in the read model's phrase", rendered)
	}
	if strings.Contains(rendered, "[running, integrating]") {
		t.Fatalf("rendered = %q, want the bare phase replaced rather than repeated", rendered)
	}
}

// The same stop once the repository says its branch is gone: the resume would
// refuse, so the line names no verb that refuses and says what is gone and that
// a re-run is the way on, by the rule the docket and the pull's hold ask.
func TestStatusSendsNobodyToTheResumeOnceAnIntegrationStopsBranchIsGone(t *testing.T) {
	t.Parallel()

	completedAt := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)
	stopped := runstate.RunSummary{
		RunID:        "run-152784a8226c6b4d3601b858dd546acf",
		WorkItemID:   "yoyodyne-ifd.309",
		Status:       runstate.StatusFailed,
		Outcome:      runstate.OutcomeFailed,
		Phase:        runstate.PhaseIntegrating,
		StartedAt:    completedAt,
		CompletedAt:  &completedAt,
		Branch:       "yoyodyne/yoyodyne-ifd-309/152784a8",
		WorktreePath: "/state/worktrees/yoyodyne-ifd-309-152784a8",
		Found: &triage.Found{
			At:           completedAt.Add(time.Hour),
			Branch:       "yoyodyne/yoyodyne-ifd-309/152784a8",
			WorktreePath: "/state/worktrees/yoyodyne-ifd-309-152784a8",
		},
		IntegrationStop: &runstate.IntegrationStop{
			Cause: runstate.CauseDirtyPrimary, Phase: runstate.PhaseIntegrating, RecordedAt: completedAt,
		},
	}

	var out bytes.Buffer
	printRunHistory(&out, runstate.RunHistory{Matched: 1, Recorded: 1, Runs: []runstate.RunSummary{stopped}}, "", true)
	rendered := out.String()
	if strings.Contains(rendered, "triage resume") {
		t.Fatalf("rendered = %q, want no resume named once the branch is gone", rendered)
	}
	for _, want := range []string{
		"integration stop: approved, then stopped at the integrating phase by the environment",
		"branch is gone (work removed, checked), so a re-run is the way on",
		"a re-run is the way on",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered = %q, want it to contain %q", rendered, want)
		}
	}
}

// The one run whose durable status and whose outcome disagree, listed. A sweep
// that finds the target does not carry a promotion hands the item back and
// leaves the status the run recorded for itself, so the listing is reading a
// record that says "succeeded" and has to say "stopped" over it — with the
// reason, the phase, and what remains of the change, exactly as every other
// stoppage is said.
func TestStatusSaysAContradictedPromotionIsAStoppageRatherThanASuccess(t *testing.T) {
	t.Parallel()

	completedAt := time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC)
	contradicted := runstate.RunSummary{
		RunID:        "run-8888888888888888888888888888bb",
		WorkItemID:   "yoyodyne-ifd.233",
		Status:       runstate.StatusSucceeded,
		Outcome:      runstate.OutcomeStopped,
		Phase:        runstate.PhaseCleaningUp,
		StartedAt:    completedAt,
		CompletedAt:  &completedAt,
		Branch:       "yoyodyne/yoyodyne-ifd.233",
		WorktreePath: "/state/worktrees/yoyodyne-ifd.233",
		Failure:      "reconciled after an interrupted run: main does not contain it",
	}

	var out bytes.Buffer
	printRunHistory(&out, runstate.RunHistory{Matched: 1, Recorded: 1, Runs: []runstate.RunSummary{contradicted}}, "", true)
	rendered := out.String()
	for _, want := range []string{
		"[stopped, cleaning_up, work preserved]",
		"reason: reconciled after an interrupted run: main does not contain it",
		"preserved worktree: /state/worktrees/yoyodyne-ifd.233",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered = %q, want it to contain %q", rendered, want)
		}
	}
	if strings.Contains(rendered, string(runstate.OutcomeSucceeded)) {
		t.Fatalf("a run a person was handed still reads as a success: %q", rendered)
	}
}

// A run that ended without succeeding and whose record gives no reason still has
// a reason line, and it says the absence in the channel's words: leaving the line
// out reads as a run with nothing to explain. A run that succeeded, one still
// going, and one whose account is an outstanding publication get no such line.
func TestStatusSaysARecordWithNoReasonNamesNone(t *testing.T) {
	t.Parallel()

	completedAt := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	ended := func(status runstate.Status, outcome runstate.RunOutcome) runstate.RunSummary {
		return runstate.RunSummary{
			RunID:       "run-9999999999999999999999999999cc",
			WorkItemID:  "yoyodyne-ifd.428.1",
			Status:      status,
			Outcome:     outcome,
			Phase:       runstate.PhaseChecking,
			StartedAt:   completedAt,
			CompletedAt: &completedAt,
		}
	}
	// A record that gives a blocker and no failure is said by its blocker, which
	// is the stoppage settled onto an already-terminal run before the sweep wrote
	// its own reason there. Saying the absence over it would be false.
	blocked := ended(runstate.StatusCancelled, runstate.OutcomeStopped)
	blocked.Blocker = "no attempt of the harness can finish it"
	var out bytes.Buffer
	printRunHistory(&out, runstate.RunHistory{Matched: 1, Recorded: 1, Runs: []runstate.RunSummary{blocked}}, "", true)
	if rendered := out.String(); !strings.Contains(rendered, "reason: no attempt of the harness can finish it") ||
		strings.Contains(rendered, runstate.NoReasonSays) {
		t.Fatalf("stopped run with a blocker and no failure rendered = %q, want the blocker as its reason", rendered)
	}

	want := "reason: " + runstate.NoReasonSays
	for _, run := range []runstate.RunSummary{
		ended(runstate.StatusCancelled, runstate.OutcomeStopped),
		ended(runstate.StatusCancelled, runstate.OutcomeCancelled),
		ended(runstate.StatusFailed, runstate.OutcomeFailed),
	} {
		var out bytes.Buffer
		printRunHistory(&out, runstate.RunHistory{Matched: 1, Recorded: 1, Runs: []runstate.RunSummary{run}}, "", true)
		if rendered := out.String(); !strings.Contains(rendered, want) {
			t.Fatalf("%s run with no reason rendered = %q, want it to contain %q", run.Outcome, rendered, want)
		}
	}

	publishing := ended(runstate.StatusFailed, runstate.OutcomeFailed)
	publishing.PublishFailure = "push refused"
	running := ended(runstate.StatusRunning, runstate.RunOutcome(runstate.StatusRunning))
	running.CompletedAt = nil
	for _, run := range []runstate.RunSummary{
		ended(runstate.StatusSucceeded, runstate.OutcomeSucceeded),
		running,
		publishing,
	} {
		var out bytes.Buffer
		printRunHistory(&out, runstate.RunHistory{Matched: 1, Recorded: 1, Runs: []runstate.RunSummary{run}}, "", true)
		if rendered := out.String(); strings.Contains(rendered, runstate.NoReasonSays) {
			t.Fatalf("%s run rendered = %q, want no absent reason said", run.Status, rendered)
		}
	}
}

func TestStatusRefusesArgumentsItCannotHonor(t *testing.T) {
	t.Parallel()

	_, stderr, code := runCLI(t, "status", "yoyodyne-ifd.41", "yoyodyne-ifd.42")
	if code != 2 {
		t.Fatalf("code = %d, want 2; stderr = %q", code, stderr)
	}
	if !strings.Contains(stderr, "at most one id") {
		t.Fatalf("stderr = %q", stderr)
	}

	_, stderr, code = runCLI(t, "status", "--limit", "-1")
	if code != 2 {
		t.Fatalf("code = %d, want 2; stderr = %q", code, stderr)
	}
	if !strings.Contains(stderr, "limit cannot be negative") {
		t.Fatalf("stderr = %q", stderr)
	}
}

// The verb reads the records without building the repository-facing components,
// so that the two still address one store is asserted rather than assumed. A
// status verb pointed at a different root would report "no recorded runs" over a
// directory full of them, which is the one failure it cannot afford: it is the
// surface an operator reaches for when something has gone wrong.
func TestStatusReadsTheSameStoreRunsAreRecordedIn(t *testing.T) {
	// Not parallel: the state root both paths resolve is set here.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig)

	parts, err := buildComponents(configPath)
	if err != nil {
		t.Fatalf("buildComponents() error = %v", err)
	}
	store, caps, err := recordedRunStore(configPath)
	if err != nil {
		t.Fatalf("recordedRunStore() error = %v", err)
	}
	if store.Root() != parts.store.Root() {
		t.Fatalf("status reads %s, but runs are recorded in %s", store.Root(), parts.store.Root())
	}
	// And the triage counters it reports come out of the same product's records,
	// so the budget an operator is shown is the budget a triage action would be
	// refused against rather than a second one beside it.
	if store.Triage().Root() != parts.store.Triage().Root() {
		t.Fatalf("status reads triage at %s, but runs record it at %s", store.Triage().Root(), parts.store.Triage().Root())
	}
	// And the caps it measures them against are the configured ones rather than
	// whatever a zero value would be, so a listing never reports every item as
	// out of budget.
	configured := mustLoadConfig(t, configPath)
	if want := orchestrator.TriageCaps(configured.Execution, configured.Triage); caps != want {
		t.Fatalf("status measures against %+v, want the configured %+v", caps, want)
	}
}

// mustLoadConfig resolves the configuration a test wrote, for the assertions
// that have to compare against what it actually says rather than against a
// number repeated in the test.
func mustLoadConfig(t *testing.T, configPath string) config.Config {
	t.Helper()
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		t.Fatalf("loadConfiguration() error = %v", err)
	}
	return resolved.Config
}

// Why the harness was running an item is the one thing a listing of runs cannot
// infer once something other than the operator does the choosing, so it is
// reported for every run -- including, in those words, for a run that recorded
// no reason at all. A missing line would read as a reason already read.
func TestStatusSaysWhyEachRunWasChosenAndNamesTheRunsNothingAccountsFor(t *testing.T) {
	// Not parallel: the state root the command addresses is set here, and the
	// records it reads are written under it.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)
	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	started := time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC)

	chosen := recordedRun(t, store, runstate.StatusSucceeded, "yoyodyne-chosen", started.Add(time.Hour))
	chosen.Selection = &runstate.Selection{
		By:     runstate.SelectedByScheduler,
		Reason: "the scheduler pulled yoyodyne-chosen from the backlog: position 1 of 4 admitted item(s)",
		At:     started.Add(time.Hour),
	}
	saveRun(t, store, chosen)
	// A run recorded before selections existed accounts for nothing, which is
	// exactly the case worth seeing.
	unaccounted := recordedRun(t, store, runstate.StatusSucceeded, "yoyodyne-unaccounted", started)
	saveRun(t, store, unaccounted)

	stdout, stderr, code := runCLI(t, "status", "--config", configPath)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"selected by the scheduler: the scheduler pulled yoyodyne-chosen from the backlog",
		"selected: no reason recorded",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}

	stdout, stderr, code = runCLI(t, "status", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("status --json code = %d, stderr = %q", code, stderr)
	}
	var output struct {
		Runs []struct {
			WorkItemID string              `json:"work_item_id"`
			Selection  *runstate.Selection `json:"selection"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", stdout, err)
	}
	if len(output.Runs) != 2 {
		t.Fatalf("runs = %d, want both recorded runs", len(output.Runs))
	}
	for _, run := range output.Runs {
		switch run.WorkItemID {
		case "yoyodyne-chosen":
			if run.Selection == nil || run.Selection.By != runstate.SelectedByScheduler {
				t.Fatalf("selection = %#v, want the scheduler's own record", run.Selection)
			}
		case "yoyodyne-unaccounted":
			if run.Selection != nil {
				t.Fatalf("selection = %#v, want nothing invented for a run that recorded none", run.Selection)
			}
		}
	}
}

// The listing says which account a run spent, which configuration set it up, and
// which harness dispatched it. There is one account today, so the line is written
// for the single-account case and still says something on the day there is a
// second one; a run recorded before any of the three was carried says so rather
// than showing a blank. The build is on the same line because it answers the same
// question an operator is asking when they read the others — whether what ran was
// what was merged.
func TestStatusSaysWhichAccountConfigurationAndBuildEachRunRanUnder(t *testing.T) {
	// Not parallel: the state root the command addresses is set here, and the
	// records it reads are written under it.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)
	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	started := time.Date(2026, 8, 22, 8, 0, 0, 0, time.UTC)

	attributed := recordedRun(t, store, runstate.StatusSucceeded, "yoyodyne-attributed", started.Add(time.Hour))
	attributed.AccountAlias = "default"
	attributed.ConfigRevision = "cfg-0123456789ab"
	attributed.Build = "9870df6a1b2c3d4e5f60718293a4b5c6d7e8f900"
	saveRun(t, store, attributed)
	unattributed := recordedRun(t, store, runstate.StatusSucceeded, "yoyodyne-unattributed", started)
	saveRun(t, store, unattributed)

	stdout, stderr, code := runCLI(t, "status", "--config", configPath)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"ran under default, configuration cfg-0123456789ab, harness 9870df6a1b2c",
		"ran under an account the record does not name, configuration a configuration the record does not name, harness a build the record does not name",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}

	stdout, stderr, code = runCLI(t, "status", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("status --json code = %d, stderr = %q", code, stderr)
	}
	var output struct {
		Runs []struct {
			WorkItemID     string `json:"work_item_id"`
			AccountAlias   string `json:"account_alias"`
			ConfigRevision string `json:"config_revision"`
			Build          string `json:"build"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", stdout, err)
	}
	for _, run := range output.Runs {
		if run.WorkItemID != "yoyodyne-attributed" {
			continue
		}
		if run.AccountAlias != "default" || run.ConfigRevision != "cfg-0123456789ab" {
			t.Fatalf("run = %#v, want the account and configuration the record holds", run)
		}
		// --json carries the whole object name rather than the prefix the listing
		// prints, because what somebody automating this hands to Git is the record's
		// own value.
		if run.Build != attributed.Build {
			t.Fatalf("run = %#v, want the whole build the record holds", run)
		}
	}
}

// recordedRun creates one run record, so a test can then set what it is about
// and save it.
func recordedRun(t *testing.T, store *runstate.Store, status runstate.Status, workItemID string, startedAt time.Time) runstate.State {
	t.Helper()
	runID, err := runstate.NewRunID()
	if err != nil {
		t.Fatalf("NewRunID() error = %v", err)
	}
	state := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         runID,
		ProductID:     domain.ProductID("yoyodyne"),
		RepositoryID:  "yoyodyne",
		WorkItemID:    workItemID,
		Backend:       domain.BackendClaudeCode,
		Status:        status,
		StartedAt:     startedAt,
		UpdatedAt:     startedAt,
	}
	if status.Terminal() {
		completedAt := startedAt
		state.CompletedAt = &completedAt
	}
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return state
}

// appendRunCost records what one provider invocation of a run cost, which is
// the evidence the listing prices a run from.
func appendRunCost(t *testing.T, store *runstate.Store, runID string, cost float64) {
	t.Helper()
	event, err := execution.NewEvent(runID, 1, time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC),
		execution.EventRunCompleted, "claude-code", map[string]any{
			"session_id":     "session-developer",
			"total_cost_usd": cost,
		})
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	if err := store.AppendEvent(event); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
}

func saveRun(t *testing.T, store *runstate.Store, state runstate.State) {
	t.Helper()
	if err := store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
}

// The status command, reading a stored record rather than a summary built by
// hand: a stoppage settled onto a run a killed process had already left
// cancelled carries the blocker and no failure, and `yoyo status` says the
// blocker as the reason, which is what the channel line says of the same run.
func TestStatusSaysTheBlockerOfAStoppageSettledOntoATerminalRun(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	stopped := recordedRun(t, store, runstate.StatusCancelled, "yoyodyne-ifd.428.1", time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC))
	stopped.Phase = runstate.PhaseChecking
	stopped.Blocker = runstate.RecordBlocker("the run was interrupted in the checking phase, and no attempt of the harness can finish it")
	saveRun(t, store, stopped)

	stdout, stderr, code := runCLI(t, "status", "--config", configPath, "yoyodyne-ifd.428.1")
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "reason: the run was interrupted in the checking phase, and no attempt of the harness can finish it") {
		t.Fatalf("stdout = %q, want the blocker said as the reason", stdout)
	}
	if strings.Contains(stdout, runstate.NoReasonSays) {
		t.Fatalf("stdout = %q, says the record names no reason over a record that names one", stdout)
	}

	// Where the record names the class that stopped the run, the reason opens
	// with it, and the blocker is still the words after it.
	stopped.StopClass = runstate.StopOutside
	saveRun(t, store, stopped)
	stdout, stderr, code = runCLI(t, "status", "--config", configPath, "yoyodyne-ifd.428.1")
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "reason: outside: the run was interrupted in the checking phase, and no attempt of the harness can finish it") {
		t.Fatalf("stdout = %q, want the blocker said as the reason after the stop class", stdout)
	}
}

// What a run cost is on the run; what the item cost is not on any of them, and
// an operator deciding whether to keep spending on a piece of work is asking the
// second question. So naming an item reports what triage has given it and what
// it has cost in review rounds, against the caps those are measured by, from the
// item's record alone.
func TestStatusReportsWhatTriageHasSpentOnANamedItem(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	started := time.Date(2026, 8, 16, 8, 0, 0, 0, time.UTC)
	failed := recordedRun(t, store, runstate.StatusFailed, "yoyodyne-ifd.2.7", started)
	failed.Phase = runstate.PhaseReviewing
	saveRun(t, store, failed)

	// An item triage has given nothing says that, rather than printing zeroes
	// that read as a budget somebody has been spending. It says it of the budget
	// rather than of triage: a decision that spends nothing reaches no counter
	// here, so this line must not be read as "nobody has looked".
	stdout, stderr, code := runCLI(t, "status", "--config", configPath, "yoyodyne-ifd.2.7")
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"triage of title unavailable (yoyodyne-ifd.2.7): triage has spent nothing on it",
		"waiting, re-scoping, and escalating spend nothing and stay available; a re-arm spends only its own budget, whatever the rounds say",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}

	// The caps the harness defaults give this configuration: four rounds in
	// total, and one merge re-arm per publication.
	caps := runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}
	triage := store.Triage()
	for _, attempt := range []string{"run-a#0", "run-a#1", "run-a#2"} {
		if _, err := triage.RecordReviewRound(context.Background(), "yoyodyne-ifd.2.7", attempt, countingProcess, started); err != nil {
			t.Fatalf("RecordReviewRound() error = %v", err)
		}
	}
	// Two attempts asked for against the one round the cap has left, which is the
	// truncation the listing then reports.
	granted, err := triage.GrantRepair(context.Background(), "yoyodyne-ifd.2.7", triageDecided(runstate.TriageDecisionRepair, decidedRunID), 2, time.Now(), caps)
	if err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	if granted.Rounds != 1 || !granted.Truncated {
		t.Fatalf("GrantRepair() = %+v, want one round of the two asked for", granted)
	}
	if _, err := triage.RecordMergeRearm(context.Background(), "yoyodyne-ifd.2.7", "publication:run-a#92", triageDecided(runstate.TriageDecisionRearm, decidedRunID), time.Now(), caps); err != nil {
		t.Fatalf("RecordMergeRearm() error = %v", err)
	}

	stdout, stderr, code = runCLI(t, "status", "--config", configPath, "yoyodyne-ifd.2.7")
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		// The second pass is the fact somebody is looking for, and it is said
		// first.
		"triage of title unavailable (yoyodyne-ifd.2.7): triage has spent 2 passes on it",
		"review rounds: 3 spent across every run of this item, under the cap of 4",
		"repair grants: 1 of 1 permitted; re-runs: 0 of 1; each is refused by its own budget or once no round remains",
		"merge re-arms: 1 across every publication of this item, 1 permitted per publication",
		// The publication that spent it is named, because the cap bounds that
		// publication rather than the item and a total alone says nothing about
		// whether the next re-arm will be refused.
		"publication:run-a#92: 1 of 1 permitted",
		"1 grant(s) were cut down to the rounds the cap still had room for",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}

	// The listing over every run is about runs rather than about one item, so it
	// carries no item's counters at all.
	stdout, stderr, code = runCLI(t, "status", "--config", configPath)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(stdout, "triage of") {
		t.Fatalf("an unnamed listing reported one item's triage: %q", stdout)
	}

	// A script reads the counters and the caps together, because neither says
	// anything on its own about how close the item is to being refused.
	stdout, stderr, code = runCLI(t, "status", "--config", configPath, "--json", "yoyodyne-ifd.2.7")
	if code != 0 {
		t.Fatalf("status --json code = %d, stderr = %q", code, stderr)
	}
	var reported statusOutput
	if err := json.Unmarshal([]byte(stdout), &reported); err != nil {
		t.Fatalf("Unmarshal() error = %v, stdout = %q", err, stdout)
	}
	if reported.Triage == nil || reported.TriageCaps == nil {
		t.Fatalf("status --json = %q, want the item's counters and the caps", stdout)
	}
	if reported.Triage.ReviewRounds != 3 || reported.Triage.RepairGrants != 1 || reported.Triage.TruncatedGrants != 1 {
		t.Fatalf("reported counters = %+v", reported.Triage)
	}
	if *reported.TriageCaps != caps {
		t.Fatalf("reported caps = %+v, want the configured %+v", *reported.TriageCaps, caps)
	}
}

// An unreadable triage record costs this answer a line rather than the runs it
// found. The three halves of that contract are pinned together because each one
// alone reads as a bug in one of the others: the runs are still listed, the exit
// status is the one a readable record gets, and the failure is carried in a key
// of its own so `error` goes on meaning the command failed.
//
// The never-spend-an-unreadable-budget rule is about spending, and this is a
// read-only answer; the store's own refusal is what keeps the budget safe.
func TestStatusReportsAnUnreadableTriageRecordBesideTheRunsItFound(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	started := time.Date(2026, 8, 16, 8, 0, 0, 0, time.UTC)
	failed := recordedRun(t, store, runstate.StatusFailed, "yoyodyne-ifd.2.7", started)
	failed.Phase = runstate.PhaseReviewing
	saveRun(t, store, failed)

	// A round recorded is what puts a counter file there to corrupt; the file is
	// found by listing rather than by name, because what names it is the store's
	// own business.
	triage := store.Triage()
	if _, err := triage.RecordReviewRound(context.Background(), "yoyodyne-ifd.2.7", "run-a#0", countingProcess, started); err != nil {
		t.Fatalf("RecordReviewRound() error = %v", err)
	}
	entries, err := os.ReadDir(triage.Root())
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	corrupted := false
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if err := os.WriteFile(filepath.Join(triage.Root(), entry.Name()), []byte("{not json"), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		corrupted = true
	}
	if !corrupted {
		t.Fatal("no recorded counter file was found to corrupt")
	}

	stdout, stderr, code := runCLI(t, "status", "--config", configPath, "yoyodyne-ifd.2.7")
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, failed.RunID) {
		t.Fatalf("stdout = %q, want the run listed beside the unreadable record", stdout)
	}
	if strings.Contains(stdout, "triage of") {
		t.Fatalf("stdout = %q, want no counters claimed from a record that could not be read", stdout)
	}
	if !strings.Contains(stderr, "the item's triage record could not be read") {
		t.Fatalf("stderr = %q, want the failure reported beside the listing", stderr)
	}

	stdout, stderr, code = runCLI(t, "status", "--config", configPath, "--json", "yoyodyne-ifd.2.7")
	if code != 0 {
		t.Fatalf("status --json code = %d, stderr = %q", code, stderr)
	}
	var reported statusOutput
	if err := json.Unmarshal([]byte(stdout), &reported); err != nil {
		t.Fatalf("Unmarshal() error = %v, stdout = %q", err, stdout)
	}
	if len(reported.Runs) != 1 || reported.Runs[0].RunID != failed.RunID {
		t.Fatalf("status --json = %q, want the run it found still listed", stdout)
	}
	if reported.TriageError == "" {
		t.Fatalf("status --json = %q, want the unreadable record reported in triage_error", stdout)
	}
	// error is the key that says the command failed, and it did not: a script
	// reading it must not start treating an unreadable counter file as one.
	if reported.Error != "" {
		t.Fatalf("status --json error = %q, want it left to mean the command failed", reported.Error)
	}
	if reported.Triage != nil || reported.TriageCaps != nil {
		t.Fatalf("status --json = %q, want no counters and no caps beside the failure", stdout)
	}
	// The key itself is the commitment, independent of the Go type the payload
	// decodes into.
	if !strings.Contains(stdout, `"triage_error":`) {
		t.Fatalf("status --json = %q, want the failure under triage_error", stdout)
	}
}

// The JSON keys are a machine commitment: this fails if any caps key drifts
// from snake_case, independent of the Go type the payload decodes into. Each
// cap carries a distinct value so a key rendered from the wrong field is a
// failure rather than a coincidence.
func TestTriageCapsSerializeWithSnakeCaseKeys(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 2, MergeRearms: 3})
	if err != nil {
		t.Fatalf("marshal caps: %v", err)
	}
	for _, want := range []string{"\"review_rounds\":4", "\"repair_grants\":1", "\"reruns\":2", "\"merge_rearms\":3"} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("caps json = %s, want it to carry %s", payload, want)
		}
	}
}

// The boundary the gate refuses at is the boundary the line reports: an item
// at exactly the cap is not under it, and this fails if the two predicates
// ever drift apart again.
func TestStatusSaysAtTheCapExactlyThatNothingMayBeHandedBack(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	printItemTriage(&out, runstate.TriageCounters{WorkItemID: "yoyodyne-ifd.90", ReviewRounds: 4}, runstate.TriageCaps{ReviewRounds: 4, MergeRearms: 2})
	rendered := out.String()
	if !strings.Contains(rendered, "at or past the cap of 4, so no decision that buys a round remains") {
		t.Fatalf("rendered = %q, want the at-the-cap line", rendered)
	}
	if strings.Contains(rendered, "under the cap of 4") {
		t.Fatalf("rendered = %q, want no under-cap claim at the boundary", rendered)
	}
	// And the way out, beside the dead end. An operator told only that nothing
	// remains is the operator who answers the escalation in the item's notes,
	// where no guard reads it.
	if !strings.Contains(rendered,
		"`yoyo triage override --budget \"review round\" --cap <n> --by \"<you>\" --reason \"<why>\" yoyodyne-ifd.90` crosses it to any ceiling") {
		t.Fatalf("rendered = %q, want the command that crosses the cap named beside it", rendered)
	}
	// And that the item may move without them, which is what the delegation
	// traded for the wait it removed: an operator reading a cap at its limit is
	// entitled to know the development manager can cross it by one himself.
	if !strings.Contains(rendered, "the development manager may also cross it far enough for one more decision, 5 times per item") {
		t.Fatalf("rendered = %q, want the delegated crossing said beside the operator's command", rendered)
	}
	// It is said where it applies and nowhere else: an item with rounds to spare
	// has no cap to cross.
	var under bytes.Buffer
	printItemTriage(&under, runstate.TriageCounters{WorkItemID: "yoyodyne-ifd.90", ReviewRounds: 3}, runstate.TriageCaps{ReviewRounds: 4, MergeRearms: 2})
	if strings.Contains(under.String(), "yoyo triage override") {
		t.Fatalf("rendered = %q, want no override advice on an item that is not at its cap", under.String())
	}
}

// The four lines are a contract rather than a layout: all four print, every
// time, and a line with nothing in it says so in words. An operator who sees a
// line missing cannot tell a quiet machine from a broken one, which is the whole
// failure this format exists to end.
func TestStatusAlwaysPrintsAllFourLines(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	stdout, stderr, code := runCLI(t, "status", "--config", configPath)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	for _, label := range []string{"Running", "Working", "Not startable", "Needs a human"} {
		if !strings.Contains(stdout, label+":") && !strings.Contains(stdout, label+" (") {
			t.Fatalf("stdout does not carry the %q line:\n%s", label, stdout)
		}
	}
	// Nothing has run and nothing is being held, so two of them say "nothing" in
	// words rather than printing a blank somebody would read as a bug.
	if !strings.Contains(stdout, "Running: nothing") {
		t.Fatalf("stdout = %q", stdout)
	}
	if !strings.Contains(stdout, "Working: nothing") {
		t.Fatalf("stdout = %q", stdout)
	}
	// There is no residual category: nothing in the output offers a bucket for
	// whatever the four lines did not cover.
	for _, residual := range []string{"Other:", "Unknown:", "Remaining:"} {
		if strings.Contains(stdout, residual) {
			t.Fatalf("stdout carries the residual category %q:\n%s", residual, stdout)
		}
	}
}

// The four lines are about the product, so a question about one item is answered
// without them: an operator asking what became of one run must not be handed a
// screen of everything else first.
func TestStatusLeavesTheFourLinesOutWhenAnItemIsNamed(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	stdout, stderr, code := runCLI(t, "status", "--config", configPath, "yoyodyne-ifd.1")
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(stdout, "Needs a human") {
		t.Fatalf("an item-scoped status carried the product's four lines:\n%s", stdout)
	}
}

// The same four lines a terminal prints are in the machine-readable answer, so a
// second surface reads the derivation rather than parsing the rendering.
func TestStatusCarriesTheFourLinesInJSON(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	stdout, stderr, code := runCLI(t, "status", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	var decoded statusOutput
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("decode status JSON: %v (%q)", err, stdout)
	}
	if decoded.Standing == nil {
		t.Fatalf("status JSON carries no standing:\n%s", stdout)
	}
	if decoded.Standing.ObservedAt.IsZero() {
		t.Fatal("the standing does not say when it was read")
	}
	if decoded.Standing.Running == nil || decoded.Standing.NeedsHuman == nil {
		t.Fatalf("a line is absent rather than empty: %+v", decoded.Standing)
	}
}

// What is parked or held on provider capacity is carried under the standing
// too, read from the same run records and refusal log, so the capacity panel
// and a script read one derivation of it. Nothing held is two empty lists
// rather than an absent key, and a run asleep on a reset is listed as waiting
// while it still counts as running.
func TestStatusCarriesTheCapacityBlockedStateInJSON(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	stdout, stderr, code := runCLI(t, "status", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, `"capacity_blocked":{"runs":[],"conversations":[]}`) {
		t.Fatalf("status JSON with nothing held does not carry two empty lists:\n%s", stdout)
	}

	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	started := time.Date(2026, 9, 18, 8, 0, 0, 0, time.UTC)
	parked := recordedRun(t, store, runstate.StatusRunning, "yoyodyne-ifd.140", started)
	parked.Phase = runstate.PhaseDeveloping
	parked.WorktreePath = "/state/worktrees/yoyodyne-ifd-140"
	parked.Branch = "yoyodyne/yoyodyne-ifd.140/0123abcd"
	parked.BaseCommit = strings.Repeat("a", 40)
	resetsAt := started.Add(5 * time.Hour)
	parked.UsageLimitResetsAt = &resetsAt
	parked.UsageLimitKind = "five_hour"
	parked.UsageLimitPausedSeconds = 1800
	parked.PauseCause = runstate.PauseUsageLimit
	saveRun(t, store, parked)

	stdout, stderr, code = runCLI(t, "status", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	var decoded statusOutput
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("decode status JSON: %v (%q)", err, stdout)
	}
	blocked := decoded.Standing.CapacityBlocked
	if len(blocked.Runs) != 1 || blocked.Runs[0].RunID != parked.RunID || blocked.Runs[0].State != readmodel.CapacityStateWaiting {
		t.Fatalf("capacity blocked = %+v, want the parked run listed as waiting", blocked)
	}
	if blocked.Runs[0].ResetsAt == nil || !blocked.Runs[0].ResetsAt.Equal(resetsAt) {
		t.Fatalf("capacity blocked = %+v, want the recorded reset carried", blocked)
	}
	if blocked.Conversations == nil || blocked.ConversationsProblem != "" {
		t.Fatalf("capacity blocked = %+v, want an empty conversation list read from an empty log", blocked)
	}
	if len(decoded.Standing.Running) != 1 {
		t.Fatalf("running = %+v, want the parked run still counted as running", decoded.Standing.Running)
	}
}

// A stall is the one history nothing else keeps: the process that would have
// recorded it is the process a stall means has died. So `yoyo status` reads it
// back — the stretch, what was waiting through it, and what the thing that
// chooses work last said before it went silent.
func TestStatusReadsBackWhatTheProductRecordedAboutGoingQuiet(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	stalls, err := runstate.NewStallStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStallStore() error = %v", err)
	}
	dead := time.Date(2026, 9, 1, 6, 5, 0, 0, time.UTC)
	if _, err := stalls.Reconcile(runstate.StallObservation{
		Stalled: true,
		Since:   dead,
		Ready:   3,
		Chooser: "the session choosing work last recorded watching at 2026-09-01T06:05:00Z, and has said nothing since",
		At:      dead.Add(35 * time.Minute),
	}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if _, err := stalls.Reconcile(runstate.StallObservation{
		Explains: "1 developer run(s) are in flight",
		At:       dead.Add(7*time.Hour + 30*time.Minute),
	}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	stdout, stderr, code := runCLI(t, "status", "--config", configPath)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	for _, fact := range []string{
		"nothing started on this product for 7h30m0s",
		"2026-09-01T06:05:00Z",
		"3 items",
		"has said nothing since",
		"cleared by: 1 developer run(s) are in flight",
	} {
		if !strings.Contains(stdout, fact) {
			t.Fatalf("stdout does not carry %q:\n%s", fact, stdout)
		}
	}

	// And the same record is in the machine-readable answer, so a second surface
	// reads it rather than parsing the rendering.
	encoded, stderr, code := runCLI(t, "status", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	var decoded statusOutput
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatalf("decode status JSON: %v (%q)", err, encoded)
	}
	if len(decoded.Stalls) != 1 || decoded.Stalls[0].Open() {
		t.Fatalf("status JSON carries %+v, want the one closed stall", decoded.Stalls)
	}

	// A question about one item is a different question, and a stall is about the
	// product rather than about any piece of work.
	scoped, stderr, code := runCLI(t, "status", "--config", configPath, "yoyodyne-ifd.1")
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(scoped, "nothing started on this product") {
		t.Fatalf("an item-scoped status carried the product's stalls:\n%s", scoped)
	}
}

// A product that has never gone quiet says nothing at all about it: the absence
// is the ordinary state, and a line asserting it on every reading is one every
// reader learns to skip.
func TestStatusSaysNothingAboutStallsOnAProductThatHasHadNone(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	stdout, stderr, code := runCLI(t, "status", "--config", configPath)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(stdout, "gone quiet") || strings.Contains(stdout, "nothing started on this product") {
		t.Fatalf("stdout asserts something about stalls on a product that has had none:\n%s", stdout)
	}
}

// The endpoints a recorded window is read against are the developer's own and
// one per model a label is mapped to, each failing over as the developer does,
// and nothing any other role asks for.
func TestDeveloperEndpointsAreTheDeveloperAndItsMappedModels(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		Agents: map[string]config.AgentConfig{
			"architect": {Role: domain.RoleArchitect, Backend: "claude-code", Model: "fable"},
			"developer": {Role: domain.RoleDeveloper, Backend: "claude-code", Model: "opus",
				Failover: config.Failover{Enabled: true, Model: "sonnet"}},
		},
		Execution: config.Execution{DeveloperModels: []config.DeveloperModelRule{{Label: "docs", Model: "haiku"}}},
	}
	endpoints := developerEndpoints(cfg)
	if len(endpoints) != 2 {
		t.Fatalf("endpoints = %+v, want the developer and its one mapped model", endpoints)
	}
	if endpoints[0].Model != "opus" || endpoints[0].Alternate != "sonnet" {
		t.Fatalf("developer endpoint = %+v, want opus failing over to sonnet", endpoints[0])
	}
	if endpoints[1].Model != "haiku" || endpoints[1].Alternate != "sonnet" {
		t.Fatalf("mapped endpoint = %+v, want haiku failing over as the developer does", endpoints[1])
	}
	if got := developerEndpoints(config.Config{}); got != nil {
		t.Fatalf("endpoints with no developer = %+v, want none", got)
	}
}
