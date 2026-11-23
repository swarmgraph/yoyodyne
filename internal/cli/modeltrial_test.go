package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestModelTrialDefectRefusesAMissingRunOrNote(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"model-trial", "defect", "--note", "broke the status line"},
		{"model-trial", "defect", "run-1"},
	} {
		var stdout, stderr bytes.Buffer
		if code := Run(args, &stdout, &stderr, "test"); code != 2 {
			t.Fatalf("Run(%v) code = %d, want 2", args, code)
		}
		if !strings.Contains(stderr.String(), "takes one run id and a --note") {
			t.Fatalf("Run(%v) stderr = %q", args, stderr.String())
		}
	}
}

// trialRunInStore records a run that took part in the trial in the arm given,
// landed or not. An empty arm is a run that took no part in it.
func trialRunInStore(t *testing.T, store *runstate.Store, workItemID string, arm runstate.CheaperFirstArm, landed bool) runstate.State {
	t.Helper()
	state := recordedRun(t, store, runstate.StatusSucceeded, workItemID, time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC))
	if arm != "" {
		state.CheaperFirst = &runstate.CheaperFirst{Arm: arm, Reason: "eligible for the trial"}
		if arm != runstate.CheaperFirstStopped {
			state.CheaperFirst.Ordinal = 1
		}
	}
	if landed {
		state.WorktreePath = "/state/worktree"
		state.Branch = "yoyodyne/" + workItemID + "/01234567"
		state.BaseCommit = strings.Repeat("a", 40)
		state.TargetBranch = "main"
		state.Phase = runstate.PhaseIntegrating
		state.ProviderSessionID = "developer-session"
		state.ProviderModel = "sonnet"
		state.ReviewSessionID = "reviewer-session"
		state.ReviewModel = "opus"
		state.ReviewDecision = runstate.ReviewApprove
		state.Integration = &runstate.Integration{TargetBranch: "main", SourceCommit: strings.Repeat("b", 40), TargetCommit: strings.Repeat("b", 40), PreviousTargetCommit: strings.Repeat("a", 40)}
	}
	saveRun(t, store, state)
	loaded, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return loaded
}

// A defect the development manager records from her conversation goes through
// landedDefectRecorder, which is what her conversation's block is wired to
// (preparedChat.open). It writes the same record `yoyo model-trial defect`
// writes, and that record stops the trial exactly as the command's does.
func TestADefectRecordedByTheDevelopmentManagerStopsTheTrialAsTheCommandDoes(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)
	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	const note = "broke the status line"

	byCommand := trialRunInStore(t, store, "yoyodyne-by-command", runstate.CheaperFirstCheaper, true)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"model-trial", "defect", byCommand.RunID, "--note", note, "--config", configPath}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("model-trial defect code = %d, stderr = %q", code, stderr.String())
	}
	commandRecord, err := store.Load(byCommand.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// The second record is made in a store of its own, so the trial it reads
	// was stopped by this record alone.
	ownRoot := t.TempDir()
	ownStore, err := runstate.NewStore(ownRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	byAction := trialRunInStore(t, ownStore, "yoyodyne-by-action", runstate.CheaperFirstCheaper, true)
	if trial := readTrial(t, ownStore); trial.StoppedBy != "" {
		t.Fatalf("before any defect the trial reads stopped: %+v", trial)
	}
	recorded, err := landedDefectRecorder{store: ownStore}.RecordDefectAfterLanding(context.Background(), byAction.RunID, note, time.Now())
	if err != nil {
		t.Fatalf("RecordDefectAfterLanding() error = %v", err)
	}
	if recorded.CheaperFirst == nil || recorded.CheaperFirst.Arm != runstate.CheaperFirstCheaper {
		t.Fatalf("the recorder answered %+v, want the cheaper run it recorded on", recorded.CheaperFirst)
	}
	actionRecord, err := ownStore.Load(byAction.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	commandDefect, actionDefect := commandRecord.CheaperFirst.DefectAfterLanding, actionRecord.CheaperFirst.DefectAfterLanding
	if commandDefect == nil || actionDefect == nil {
		t.Fatalf("defects recorded: command %+v, action %+v; want both", commandDefect, actionDefect)
	}
	if commandDefect.Note != actionDefect.Note || commandRecord.CheaperFirst.Arm != actionRecord.CheaperFirst.Arm {
		t.Fatalf("the action recorded %+v on a %s run, the command %+v on a %s run; want the same record",
			actionDefect, actionRecord.CheaperFirst.Arm, commandDefect, commandRecord.CheaperFirst.Arm)
	}
	for name, check := range map[string]struct {
		store *runstate.Store
		runID string
	}{"command": {store, byCommand.RunID}, "action": {ownStore, byAction.RunID}} {
		trial := readTrial(t, check.store)
		if trial.StoppedBy != check.runID || trial.StopNote != note || trial.NextArm != runstate.CheaperFirstStopped {
			t.Errorf("after the %s's record the trial reads %+v, want it stopped by run %s", name, trial, check.runID)
		}
		figures, err := check.store.CheaperFirstTrialFigures(40)
		if err != nil {
			t.Fatalf("CheaperFirstTrialFigures() error = %v", err)
		}
		if figures.StoppedBy != check.runID || figures.Cheaper.DefectsAfterLanding != 1 {
			t.Errorf("after the %s's record the comparison reads %+v, want one cheaper defect stopping the trial", name, figures)
		}
	}
}

func readTrial(t *testing.T, store *runstate.Store) runstate.CheaperFirstTrialState {
	t.Helper()
	runs, err := store.CheaperFirstTrialRuns()
	if err != nil {
		t.Fatalf("CheaperFirstTrialRuns() error = %v", err)
	}
	return runstate.ReadCheaperFirstTrial(runs)
}

// A run outside the trial — one that took no part in it, or one that started on
// the developer's configured model after the trial had stopped — is refused with
// a plain reason, and nothing is written to its record. The command is refused
// the same way. A run in the half that starts on the stronger model is accepted.
func TestTheDevelopmentManagersDefectIsRefusedOutsideTheTrialAndAcceptedOnAStrongerRun(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)
	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	recorder := landedDefectRecorder{store: store}

	for name, arm := range map[string]runstate.CheaperFirstArm{
		"outside the trial":    "",
		"started after a stop": runstate.CheaperFirstStopped,
	} {
		run := trialRunInStore(t, store, "yoyodyne-"+strings.ReplaceAll(name, " ", "-"), arm, true)
		_, err := recorder.RecordDefectAfterLanding(context.Background(), run.RunID, "broke the status line", time.Now())
		if !errors.Is(err, runstate.ErrNotInCheaperFirstTrial) {
			t.Fatalf("%s: error = %v, want the refusal for a run outside the trial", name, err)
		}
		if want := "the run took no part in the trial of starting developers on the cheaper model"; !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), run.RunID) {
			t.Errorf("%s: refusal %q does not name the run and say %q", name, err, want)
		}
		var stdout, stderr bytes.Buffer
		if code := Run([]string{"model-trial", "defect", run.RunID, "--note", "broke the status line", "--config", configPath}, &stdout, &stderr, "test"); code != 1 || !strings.Contains(stderr.String(), err.Error()) {
			t.Errorf("%s: the command answered %d %q, want the action's refusal %q", name, code, stderr.String(), err)
		}
		loaded, loadErr := store.Load(run.RunID)
		if loadErr != nil {
			t.Fatalf("Load() error = %v", loadErr)
		}
		if loaded.CheaperFirst != nil && loaded.CheaperFirst.DefectAfterLanding != nil {
			t.Errorf("%s: a refused defect was written: %+v", name, loaded.CheaperFirst.DefectAfterLanding)
		}
	}
	if trial := readTrial(t, store); trial.StoppedBy != "" {
		t.Fatalf("a refused defect stopped the trial: %+v", trial)
	}

	// The trial's comparison half, which started on the stronger model, is
	// counted against that half and does not stop the trial, as the design's
	// comparison of defects after landing in each half needs.
	stronger := trialRunInStore(t, store, "yoyodyne-stronger", runstate.CheaperFirstStronger, true)
	if _, err := recorder.RecordDefectAfterLanding(context.Background(), stronger.RunID, "a defect on the stronger half", time.Now()); err != nil {
		t.Fatalf("a defect on the stronger half: error = %v", err)
	}
	if trial := readTrial(t, store); trial.StoppedBy != "" {
		t.Fatalf("a defect on a run that started stronger stopped the trial: %+v", trial)
	}
}
