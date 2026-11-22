package cli

import (
	"bytes"
	"strings"
	"testing"

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

func TestTheTrialComparisonSaysEachArmAndAStop(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	printTrialFigures(&out, runstate.CheaperFirstTrialFigures{
		Target: 40, Runs: 3, StoppedBy: "run-a", StopNote: "broke the status line",
		Cheaper:  runstate.CheaperFirstArmFigures{Runs: 2, Merged: 1, CostPerMergedUSD: 1.5, Reviewed: 2, FirstReviewApproved: 1, RepairRounds: 2, Moves: 1, DefectsAfterLanding: 1},
		Stronger: runstate.CheaperFirstArmFigures{Runs: 1, Merged: 1, CostPerMergedUSD: 2.5, Reviewed: 1, FirstReviewApproved: 1},
		Defects:  []runstate.CheaperFirstDefect{{RunID: "run-a", WorkItemID: "item-a", Arm: runstate.CheaperFirstCheaper, Note: "broke the status line"}},
	}, true)
	for _, want := range []string{
		"Developers starting on the cheaper model: on. 3 of at least 40 trial runs so far.",
		"The trial is stopped: run run-a started on the cheaper model",
		"$1.50", "$2.50", "1 of 2", "1 of 1",
		"Defect after landing: run run-a (item-a, started cheaper): broke the status line",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output does not say %q:\n%s", want, out.String())
		}
	}
}
