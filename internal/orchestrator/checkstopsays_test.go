package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A case of a check stopped on time: how long each check takes, the configured
// limits, the machine's load, what the stop must say, and what it must never.
type checkStopCase struct {
	name  string
	takes map[string]time.Duration
	// check and stage are execution.check_timeout and
	// execution.check_stage_timeout as configured, before any load scales them.
	check, stage time.Duration
	load         float64
	limitOf      checks.LimitOwner
	limit        time.Duration
	says         []string
	never        []string
}

// The three ways a check is stopped on time, each said the same way whichever
// run it was: by its own limit, by the stage's bound when the check had run
// only briefly, and by limits the machine's load had scaled. Every message says
// how long the check ran from when it started and names the limit that stopped
// it, and never a limit the check had not reached.
var checkStopCases = []checkStopCase{
	{
		name:  "its own limit",
		takes: map[string]time.Duration{"make test": time.Minute, "make race": 40 * time.Minute},
		check: 30 * time.Minute, stage: 3 * time.Hour, load: 8,
		limitOf: checks.LimitOfCheck, limit: 30 * time.Minute,
		says:  []string{"make race ran for 30m0s from when it started and was stopped at its own 30m0s execution.check_timeout limit"},
		never: []string{"check_stage_timeout bound"},
	},
	{
		// The stage had spent all but 2m20s of its thirty minutes on make test,
		// so make race was given 2m20s and stopped there: the stop names the
		// stage's bound and the 2m20s, never the thirty-minute limit of its own.
		name:  "the stage's bound while the check had run only briefly",
		takes: map[string]time.Duration{"make test": 27*time.Minute + 40*time.Second, "make race": 40 * time.Minute},
		check: 30 * time.Minute, stage: 30 * time.Minute, load: 8,
		limitOf: checks.LimitOfStage, limit: 2*time.Minute + 20*time.Second,
		says:  []string{"the check stage reached its 30m0s execution.check_stage_timeout bound (not scaled: a one-minute load average of 8.0 on 16 cores is at or under one per core) during make race, which had run for 2m20s from when it started", "the stage had spent 30m0s across 2 check(s)"},
		never: []string{"execution.check_timeout limit", "its own 30m"},
	},
	{
		name:  "its own limit scaled for load",
		takes: map[string]time.Duration{"make test": time.Minute, "make race": 2 * time.Hour},
		check: 30 * time.Minute, stage: 3 * time.Hour, load: 48,
		limitOf: checks.LimitOfCheck, limit: 90 * time.Minute,
		says:  []string{"make race ran for 1h30m0s from when it started and was stopped at its own 1h30m0s execution.check_timeout limit (the configured 30m0s scaled for a one-minute load average of 48.0 on 16 cores)"},
		never: []string{"its own 30m0s", "check_stage_timeout bound"},
	},
	{
		// The stage's thirty minutes scaled to ninety, and make test spent
		// eighty-five of them, so make race ran five minutes of the ninety its own
		// scaled limit would have given it.
		name:  "the stage's bound scaled for load while the check had run only briefly",
		takes: map[string]time.Duration{"make test": 85 * time.Minute, "make race": 2 * time.Hour},
		check: 30 * time.Minute, stage: 30 * time.Minute, load: 48,
		limitOf: checks.LimitOfStage, limit: 5 * time.Minute,
		says:  []string{"the check stage reached its 1h30m0s execution.check_stage_timeout bound (the configured 30m0s scaled for a one-minute load average of 48.0 on 16 cores) during make race, which had run for 5m0s from when it started"},
		never: []string{"execution.check_timeout limit", "its own 1h30m"},
	},
}

// checkStopPipeline is a pipeline whose checks take the case's times on a clock
// of the test's own, under the case's configured limits and load.
func checkStopPipeline(pipeline Pipeline, c checkStopCase, process execution.ProcessRunner, clock *steppingClock) Pipeline {
	pipeline.Config.Checks = []string{"make test", "make race"}
	pipeline.Config.Execution.CheckTimeout = config.Duration(c.check)
	pipeline.Config.Execution.CheckStageTimeout = config.Duration(c.stage)
	pipeline.Checks = checks.Runner{Process: process, Clock: clock, Timeout: c.check, StageTimeout: c.stage}
	load := c.load
	pipeline.Load = func() (float64, int, bool) { return load, 16, true }
	return pipeline
}

func assertCheckStopSays(t *testing.T, said string, c checkStopCase) {
	t.Helper()
	for _, want := range c.says {
		if !strings.Contains(said, want) {
			t.Fatalf("stop said as %q, want %q", said, want)
		}
	}
	for _, unwanted := range c.never {
		if strings.Contains(said, unwanted) {
			t.Fatalf("stop said as %q, naming %q, a limit the check had not reached", said, unwanted)
		}
	}
}

func TestADeveloperRunsCheckStoppedOnTimeNamesTheLimitThatStoppedIt(t *testing.T) {
	t.Parallel()
	for _, c := range checkStopCases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			repository := pipelineRepository(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
			}, approveVerdict)
			pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, nil)
			clock := &steppingClock{now: time.Now().UTC().Add(time.Hour)}
			pipeline = checkStopPipeline(pipeline, c, &timedChecks{clock: clock, takes: c.takes}, clock)

			outcome, runErr := pipeline.Run(context.Background(), tracker.Item.ID)
			if runErr == nil {
				t.Fatal("Run() error = nil, want make race stopped on time")
			}
			race := outcome.Checks[len(outcome.Checks)-1]
			if race.Command != "make race" || race.LimitOf != c.limitOf || race.Limit != c.limit {
				t.Fatalf("make race = %#v, want it given %s by the %s's limit", race, c.limit, c.limitOf)
			}
			assertCheckStopSays(t, runErr.Error(), c)
			state, err := store.Load(outcome.RunID)
			if err != nil {
				t.Fatal(err)
			}
			assertCheckStopSays(t, state.Failure, c)
			if state.Environmental == nil {
				t.Fatalf("state = %#v, want the stop recorded as one from outside the work", state)
			}
			assertCheckStopSays(t, state.Environmental.Detail, c)
		})
	}
}

// slowFirst is timedChecks with one check that takes its full time only the
// first time it runs and a minute after that, which is a suite stopped by load
// and then let finish.
type slowFirst struct {
	*timedChecks
	command string
	once    bool
}

func (r *slowFirst) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	result, err := r.timedChecks.Run(ctx, command, observer)
	if command.Args[len(command.Args)-1] == r.command && !r.once {
		r.once = true
		r.takes[r.command] = time.Minute
	}
	return result, err
}

func TestADocumentRunsCheckStoppedOnTimeNamesTheLimitThatStoppedIt(t *testing.T) {
	t.Parallel()
	for _, c := range checkStopCases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, _, provider, pipeline, runs := automaticFixture(t)
			retryDesign(provider, "timed-design")
			clock := &steppingClock{now: time.Now().UTC().Add(time.Hour)}
			takes := make(map[string]time.Duration, len(c.takes))
			for command, took := range c.takes {
				takes[command] = took
			}
			pipeline = checkStopPipeline(pipeline, c, &slowFirst{timedChecks: &timedChecks{clock: clock, takes: takes}, command: "make race"}, clock)
			options, _ := publicationConversation(t, pipeline, provider)
			session, err := chat.Open(options)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := session.Send(context.Background(), "Write it."); err != nil {
				t.Fatal(err)
			}
			if err := session.PublishDocuments(context.Background()); err != nil {
				t.Fatal(err)
			}
			recorded := documentRuns(t, runs)
			if len(recorded) == 0 || recorded[0].Document == nil {
				t.Fatalf("runs = %#v, want the document's runs", recorded)
			}
			first := runByID(t, runs, recorded[0].Document.RunIDFor(0))
			if first.Status == runstate.StatusSucceeded || first.DocumentJudged() {
				t.Fatalf("first run = %s %q, want it stopped on time without judging the document", first.Status, first.Failure)
			}
			assertCheckStopSays(t, first.Failure, c)
			second := runByID(t, runs, recorded[0].Document.RunIDFor(1))
			if second.Status != runstate.StatusSucceeded {
				t.Fatalf("second run = %s %q, want the same text landed once the check could finish", second.Status, second.Failure)
			}
			// What the next attempt says about the stop it follows is the same
			// account, so it names no limit the check had not reached either.
			if second.Selection == nil {
				t.Fatal("second run has no selection reason")
			}
			for _, unwanted := range c.never {
				if strings.Contains(second.Selection.Reason, unwanted) {
					t.Fatalf("second run's reason = %q, naming %q, a limit the check had not reached", second.Selection.Reason, unwanted)
				}
			}
		})
	}
}

// Every way a check's result can read as stopped on time is said by one rule:
// the limit is named only where the time measured against it reached it.
func TestACheckStopNamesOnlyALimitItsTimeReached(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 10, 9, 7, 0, 0, 0, time.UTC)
	ran := func(took time.Duration) execution.ProcessResult {
		return execution.ProcessResult{Status: execution.ProcessTimedOut, ExitCode: -1, StartedAt: started, FinishedAt: started.Add(took)}
	}
	for _, test := range []struct {
		name   string
		result checks.Result
		says   string
		never  string
	}{
		{
			name:   "own limit reached",
			result: checks.Result{Command: "make race", Timeout: 30 * time.Minute, Limit: 30 * time.Minute, LimitOf: checks.LimitOfCheck, Process: ran(30 * time.Minute)},
			says:   "make race ran for 30m0s from when it started and was stopped at its own 30m0s execution.check_timeout limit",
		},
		{
			name:   "stage bound reached after a brief run",
			result: checks.Result{Command: "make race", Timeout: 30 * time.Minute, Limit: 2*time.Minute + 20*time.Second, LimitOf: checks.LimitOfStage, StageTimeout: 30 * time.Minute, StageElapsed: 30 * time.Minute, StoppedByStage: true, Process: ran(2*time.Minute + 20*time.Second)},
			says:   "make race ran for 2m20s from when it started and was stopped when the check stage reached its 30m0s execution.check_stage_timeout bound, which had left this check 2m20s",
			never:  "its own 30m0s",
		},
		{
			name:   "stage bound reached before the check started",
			result: checks.Result{Command: "make vet", Timeout: 30 * time.Minute, StageTimeout: 30 * time.Minute, StageElapsed: 30 * time.Minute, StoppedByStage: true, Process: ran(0)},
			says:   "make vet never started: the check stage had already reached its 30m0s execution.check_stage_timeout bound",
			never:  "ran for",
		},
		{
			name:   "the run's deadline reached",
			result: checks.Result{Command: "make race", Timeout: 30 * time.Minute, Limit: 2*time.Minute + 20*time.Second, LimitOf: checks.LimitOfRun, StoppedByCaller: true, Process: ran(2*time.Minute + 20*time.Second)},
			says:   "make race ran for 2m20s from when it started and was stopped when the deadline of the work that started this run passed, which had left this check 2m20s",
			never:  "30m0s",
		},
		{
			name:   "stopped short of every limit",
			result: checks.Result{Command: "make race", Timeout: 30 * time.Minute, Limit: 30 * time.Minute, LimitOf: checks.LimitOfCheck, Process: ran(2*time.Minute + 20*time.Second)},
			says:   "make race ran for 2m20s from when it started and was stopped on time before any limit it was given had run out",
			never:  "30m0s",
		},
		{
			name:   "a stage said to have stopped it short of its bound",
			result: checks.Result{Command: "make race", Timeout: 30 * time.Minute, Limit: 2*time.Minute + 20*time.Second, LimitOf: checks.LimitOfStage, StageTimeout: 30 * time.Minute, StageElapsed: 10 * time.Minute, StoppedByStage: true, Process: ran(2*time.Minute + 20*time.Second)},
			says:   "before any limit it was given had run out",
			never:  "30m0s",
		},
	} {
		said := checkStopSays(test.result, "execution.check_timeout", runstate.CheckStage{})
		if !strings.Contains(said, test.says) || (test.never != "" && strings.Contains(said, test.never)) {
			t.Fatalf("%s: said as %q, want %q and never %q", test.name, said, test.says, test.never)
		}
	}
}

// The stage's record says a check stopped short of its own limit was stopped
// after what it ran for, wherever the record is read, and never at the limit.
func TestTheStageRecordNeverSaysACheckReachedALimitItStoppedShortOf(t *testing.T) {
	t.Parallel()
	stage := runstate.CheckStage{
		StartedAt: time.Now(), BoundSeconds: 1800, ConfiguredSeconds: 1800, Command: "make race",
		CheckStoppedAtLimit: true, CheckLimitSeconds: 1800, CheckElapsedSeconds: 140,
	}
	finished := stage.StartedAt.Add(30 * time.Minute)
	stage.FinishedAt, stage.ElapsedSeconds = &finished, 1800
	state := runstate.State{RunID: "run-short", Status: runstate.StatusTimedOut, Phase: runstate.PhaseChecking, CheckStage: &stage}
	for name, said := range map[string]string{
		"description": stage.Describe(finished),
		"stop":        state.CheckStageStopSays(),
		"rerun":       unfinishedCheckSays(state),
		"notes":       strings.Join(renderCheckNotes(Outcome{CheckStage: &stage}), "\n"),
	} {
		if strings.Contains(said, "limit of 30m") || strings.Contains(said, "own 30m") || strings.Contains(said, "reached its own") {
			t.Fatalf("%s says %q, a limit make race stopped short of", name, said)
		}
		if !strings.Contains(said, "2m") {
			t.Fatalf("%s says %q, want what make race ran for", name, said)
		}
	}
	stage.CheckElapsedSeconds = 1800
	if said := state.CheckStageStopSays(); !strings.Contains(said, "make race reached its own execution.check_timeout limit of 30m") {
		t.Fatalf("stop says %q, want the limit make race ran for the whole of", said)
	}
}
