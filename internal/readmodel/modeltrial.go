package readmodel

// The trial of starting developer runs on the cheaper model
// (docs/designs/reducing-what-the-factory-costs.md, section 4), as every
// surface reads it: `yoyo model-trial`, the "model-trial" query, and the
// section every development manager pass opens with while the trial has
// anything to say. One reading, so the figures the development manager decides
// on are the figures a person sees.

import (
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// QueryModelTrial is the read-model query for the trial.
const QueryModelTrial = "model-trial"

// ModelTrialRuns is the part of the run store the trial is read from. It is an
// optional capability of Runs, satisfied by *runstate.Store.
type ModelTrialRuns interface {
	CheaperFirstTrialFigures(target int) (runstate.CheaperFirstTrialFigures, error)
}

// ModelTrial is the trial as the read model carries it.
type ModelTrial struct {
	ObservedAt time.Time                         `json:"observed_at"`
	Enabled    bool                              `json:"enabled"`
	Figures    runstate.CheaperFirstTrialFigures `json:"figures"`
	Problem    string                            `json:"problem,omitempty"`
}

// ReadModelTrial reads the trial from the runs that took part in it, against
// the project's execution.developer_cheaper_first.
func ReadModelTrial(sources Sources) ModelTrial {
	setting := sources.CheaperFirst
	trial := ModelTrial{ObservedAt: sources.now(), Enabled: setting.Enabled, Figures: runstate.CheaperFirstTrialFigures{Target: setting.TrialTarget()}}
	runs, ok := sources.Runs.(ModelTrialRuns)
	if !ok {
		trial.Problem = "nothing was wired to read the runs that took part in the trial"
		return trial
	}
	figures, err := runs.CheaperFirstTrialFigures(setting.TrialTarget())
	if err != nil {
		trial.Problem = fmt.Sprintf("the runs that took part in the trial could not be read: %v", err)
		return trial
	}
	trial.Figures = figures
	return trial
}

// Speaks reports a trial with anything to say: the setting is on, or runs
// took part in it while it was.
func (t ModelTrial) Speaks() bool {
	return t.Enabled || t.Figures.Runs > 0 || t.Problem != ""
}

// Render is the trial in ordinary words, the comparison as a table.
func (t ModelTrial) Render() string {
	var out strings.Builder
	state := "off"
	if t.Enabled {
		state = "on"
	}
	figures := t.Figures
	fmt.Fprintf(&out, "Developers starting on the cheaper model: %s. %d of at least %d trial runs so far.\n", state, figures.Runs, figures.Target)
	if t.Problem != "" {
		fmt.Fprintf(&out, "Not everything could be read: %s.\n", t.Problem)
	}
	if figures.StoppedBy != "" {
		fmt.Fprintf(&out, "The trial is stopped: run %s started on the cheaper model and its change had a defect after landing (%s). Every eligible run since starts on the developer's configured model.\n", figures.StoppedBy, figures.StopNote)
	} else if figures.Runs >= figures.Target {
		out.WriteString("The trial has reached its run count; the comparison below is due for a decision.\n")
	}
	out.WriteString("\n")
	row := func(label, cheaper, stronger string) {
		fmt.Fprintf(&out, "%-26s %12s %12s\n", label, cheaper, stronger)
	}
	c, s := figures.Cheaper, figures.Stronger
	row("", "cheaper", "stronger")
	row("runs", fmt.Sprint(c.Runs), fmt.Sprint(s.Runs))
	row("changes merged", fmt.Sprint(c.Merged), fmt.Sprint(s.Merged))
	row("cost per merged change", trialDollars(c), trialDollars(s))
	row("first review approved", trialShare(c.FirstReviewApproved, c.Reviewed), trialShare(s.FirstReviewApproved, s.Reviewed))
	row("repair rounds", fmt.Sprint(c.RepairRounds), fmt.Sprint(s.RepairRounds))
	row("moved to stronger", fmt.Sprint(c.Moves), "-")
	row("defects after landing", fmt.Sprint(c.DefectsAfterLanding), fmt.Sprint(s.DefectsAfterLanding))
	if c.Unpriced+s.Unpriced > 0 {
		fmt.Fprintf(&out, "\n%d run(s) could not be priced and are left out of the cost.\n", c.Unpriced+s.Unpriced)
	}
	for _, defect := range figures.Defects {
		fmt.Fprintf(&out, "Defect after landing: run %s (%s, started %s): %s\n", defect.RunID, defect.WorkItemID, defect.Arm, defect.Note)
	}
	return strings.TrimRight(out.String(), "\n")
}

// PassSection is what a development manager pass opens with while the trial
// has anything to say, and empty otherwise.
func (t ModelTrial) PassSection() string {
	if !t.Speaks() {
		return ""
	}
	return "# The trial of starting developers on the cheaper model\n\n" +
		"Read as this pass was taken, from the run records. Keeping execution.developer_cheaper_first on once the trial has its run count, or turning it off, is your decision on these figures. A defect traced to a landed run's change is recorded with the block that records a defect after landing, which stops the trial at once when that run started on the cheaper model.\n\n" +
		"```\n" + t.Render() + "\n```"
}

func trialDollars(arm runstate.CheaperFirstArmFigures) string {
	if arm.Merged == 0 {
		return "-"
	}
	return fmt.Sprintf("$%.2f", arm.CostPerMergedUSD)
}

func trialShare(part, whole int) string {
	if whole == 0 {
		return "-"
	}
	return fmt.Sprintf("%d of %d", part, whole)
}
