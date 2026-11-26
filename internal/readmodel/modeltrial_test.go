package readmodel

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type trialRuns struct {
	Runs
	figures runstate.CheaperFirstTrialFigures
	err     error
	target  int
}

func (r *trialRuns) CheaperFirstTrialFigures(target int) (runstate.CheaperFirstTrialFigures, error) {
	r.target = target
	return r.figures, r.err
}

func stoppedTrial() runstate.CheaperFirstTrialFigures {
	return runstate.CheaperFirstTrialFigures{
		Target: 40, Runs: 3, StoppedBy: "run-a", StopNote: "broke the status line",
		Cheaper:  runstate.CheaperFirstArmFigures{Runs: 2, Merged: 1, CostPerMergedUSD: 1.5, Reviewed: 2, FirstReviewApproved: 1, RepairRounds: 2, Moves: 1, DefectsAfterLanding: 1},
		Stronger: runstate.CheaperFirstArmFigures{Runs: 1, Merged: 1, CostPerMergedUSD: 2.5, Reviewed: 1, FirstReviewApproved: 1},
		Defects:  []runstate.CheaperFirstDefect{{RunID: "run-a", WorkItemID: "item-a", Arm: runstate.CheaperFirstCheaper, Note: "broke the status line"}},
	}
}

func TestTheModelTrialIsReadAgainstTheConfiguredRunCountAndSaysEachArmAndAStop(t *testing.T) {
	t.Parallel()

	runs := &trialRuns{figures: stoppedTrial()}
	trial := ReadModelTrial(Sources{Runs: runs, CheaperFirst: config.DeveloperCheaperFirst{Enabled: true, TrialRuns: 60}})
	if runs.target != 60 {
		t.Fatalf("the trial was read against %d runs, want the configured 60", runs.target)
	}
	if !trial.Speaks() {
		t.Fatal("a trial that is on says nothing")
	}
	rendered := trial.Render()
	for _, want := range []string{
		"Developers starting on the cheaper model: on. 3 of at least 40 trial runs so far.",
		"The trial is stopped: run run-a started on the cheaper model",
		"$1.50", "$2.50", "1 of 2", "1 of 1",
		"Defect after landing: run run-a (item-a, started cheaper): broke the status line",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the trial does not say %q:\n%s", want, rendered)
		}
	}
	section := trial.PassSection()
	if !strings.Contains(section, "your decision on these figures") || !strings.Contains(section, "records a defect after landing") {
		t.Fatalf("the pass section does not tell the development manager what is hers:\n%s", section)
	}
}

func TestATrialThatIsOffWithNoRunsSaysNothingToAPass(t *testing.T) {
	t.Parallel()

	trial := ReadModelTrial(Sources{Runs: &trialRuns{}})
	if trial.Speaks() || trial.PassSection() != "" {
		t.Fatalf("a trial that is off with no runs said %q", trial.PassSection())
	}
	broken := ReadModelTrial(Sources{Runs: &trialRuns{err: errors.New("disk gone")}})
	if !strings.Contains(broken.Problem, "disk gone") || !broken.Speaks() {
		t.Fatalf("an unreadable trial = %+v, want its problem said", broken)
	}
}

func TestTheModelTrialIsANamedQuery(t *testing.T) {
	t.Parallel()

	if !KnownQuery(QueryModelTrial) || !strings.Contains(QueryList(), `"model-trial"`) {
		t.Fatalf("the model-trial query is not listed: %s", QueryList())
	}
	result, err := Query(context.Background(), Sources{Runs: &trialRuns{figures: stoppedTrial()}}, ThroughputSources{}, QueryModelTrial, "")
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	var answer ModelTrial
	if err := json.Unmarshal([]byte(result.JSON), &answer); err != nil {
		t.Fatalf("decode the answer: %v", err)
	}
	if answer.Figures.StoppedBy != "run-a" || answer.Figures.Cheaper.Runs != 2 {
		t.Fatalf("answer = %+v", answer.Figures)
	}
}

type conflictTrialRuns struct {
	trialRuns
	conflicts runstate.ConflictModelTrialFigures
	target    int
}

func (r *conflictTrialRuns) ConflictModelTrialFigures(target int) (runstate.ConflictModelTrialFigures, error) {
	r.target = target
	return r.conflicts, nil
}

func TestTheConflictTrialIsShownForEachSideBesideTheOtherTrial(t *testing.T) {
	t.Parallel()

	runs := &conflictTrialRuns{conflicts: runstate.ConflictModelTrialFigures{
		Target: 20, Attempts: 3, StoppedBy: "run-c", StopNote: "lost a side",
		Cheaper:  runstate.ConflictModelSideFigures{Attempts: 2, Landed: 2, CostPerLandedUSD: 0.75, ChecksFailed: 1, Reviewed: 2, Refused: 1, DefectsAfterLanding: 1},
		Stronger: runstate.ConflictModelSideFigures{Attempts: 1, Landed: 1, CostPerLandedUSD: 1.25, Reviewed: 1},
	}}
	trial := ReadModelTrial(Sources{Runs: runs, ConflictResolutionModel: config.ModelKindCheaper})
	if runs.target != config.ConflictModelTrialAttempts {
		t.Fatalf("the conflict trial was read against %d attempts, want %d", runs.target, config.ConflictModelTrialAttempts)
	}
	if !trial.Speaks() {
		t.Fatal("a conflict trial that is set says nothing")
	}
	rendered := trial.Render()
	for _, want := range []string{
		"First attempts at a small conflict (execution.conflict_resolution_model): the cheaper model. 3 of 20 trial attempts so far.",
		"The trial is stopped: run run-c resolved its conflict on the cheaper model",
		"cost per landed change", "$0.75", "$1.25",
		"review refused", "1 of 2", "0 of 1",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the trial does not say %q:\n%s", want, rendered)
		}
	}
	if section := trial.PassSection(); !strings.Contains(section, "--resolution") {
		t.Fatalf("the pass section does not say how a resolution's defect is recorded:\n%s", section)
	}

	quiet := ReadModelTrial(Sources{Runs: &conflictTrialRuns{conflicts: runstate.ConflictModelTrialFigures{Target: 20}}})
	if quiet.Speaks() {
		t.Fatalf("a conflict trial that was never set, with no attempts, said %q", quiet.Render())
	}
}
