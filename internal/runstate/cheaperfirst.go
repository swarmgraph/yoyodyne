package runstate

// What a developer run recorded about starting on the cheaper model, moving
// once to the stronger, and the trial that compares the two
// (docs/designs/reducing-what-the-factory-costs.md, section 4;
// internal/orchestrator/cheaperfirst.go decides it).
//
// The trial keeps no store of its own. Each eligible run carries which arm it
// started in, and the trial — how many runs it has alternated, whether a
// defect found after landing has stopped it, and what each arm cost — is read
// back off the run records and the prices already kept for them.

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// CheaperFirstArm is which way an eligible run started.
type CheaperFirstArm string

const (
	// CheaperFirstCheaper started on the cheaper model, to move once to the
	// stronger after its first failed review or failed checks.
	CheaperFirstCheaper CheaperFirstArm = "cheaper"
	// CheaperFirstStronger started on the stronger model, as the trial's
	// comparison.
	CheaperFirstStronger CheaperFirstArm = "stronger"
	// CheaperFirstStopped was eligible after a defect found after landing had
	// stopped the trial, so it started on the developer's configured model and
	// counts toward neither arm.
	CheaperFirstStopped CheaperFirstArm = "stopped"
)

// CheaperFirst is one eligible developer run's record of the trial.
type CheaperFirst struct {
	Arm CheaperFirstArm `json:"arm"`
	// Ordinal is this run's place among the trial's runs, from 1. A stopped
	// run has none.
	Ordinal int `json:"ordinal,omitempty"`
	// Reason says why the item was eligible and which way it started.
	Reason        string `json:"reason"`
	StartModel    string `json:"start_model,omitempty"`
	StrongerModel string `json:"stronger_model,omitempty"`
	// FirstReview is the first independent review's decision on this run's
	// change, which is what the trial's first-review approval rate counts.
	FirstReview string `json:"first_review,omitempty"`
	// Move is the run's one move from the cheaper model to the stronger.
	Move *ModelMove `json:"move,omitempty"`
	// Attempts is the model each of the run's developer attempts asked for, in
	// the order they started, whether the repair loop started the attempt or a
	// hand-back continued the run (NoteAttemptModel).
	Attempts []AttemptModel `json:"attempts,omitempty"`
	// DefectAfterLanding is a defect somebody traced to this run's change after
	// it landed. On a run that started cheaper it stops the trial.
	DefectAfterLanding *LandedDefect `json:"defect_after_landing,omitempty"`
}

// ModelMove is a run's one move to the stronger model.
type ModelMove struct {
	At   time.Time `json:"at"`
	From string    `json:"from"`
	To   string    `json:"to"`
	// Reason is the failure that moved it: a failed review or failed checks.
	Reason string `json:"reason"`
	// RepairAttempt is the repair attempt the stronger model starts on.
	RepairAttempt int `json:"repair_attempt"`
	// SpentBeforeUSD is what the run had cost when it moved, priced from its
	// own event log (Store.RunSpentSoFar), so what the stronger model added is
	// the run's final cost less this. SpentKnown is false where the log could
	// not price the run.
	SpentBeforeUSD float64 `json:"spent_before_usd,omitempty"`
	SpentKnown     bool    `json:"spent_known,omitempty"`
}

// AttemptModel is the model one developer attempt asked for. Attempt is the
// run's repair count when the attempt started: 0 is the first attempt, and 1
// on is the repair attempt of that number.
type AttemptModel struct {
	Attempt int       `json:"attempt"`
	Model   string    `json:"model"`
	At      time.Time `json:"at"`
}

// NoteAttemptModel returns attempts with the attempt numbered attempt recorded
// as asking for model. An attempt made again after an interruption is the same
// attempt, so it replaces the entry already there rather than adding a second.
// The slice it is given is never written to.
func NoteAttemptModel(attempts []AttemptModel, attempt int, model string, at time.Time) []AttemptModel {
	noted := append([]AttemptModel{}, attempts...)
	entry := AttemptModel{Attempt: attempt, Model: model, At: at}
	if last := len(noted) - 1; last >= 0 && noted[last].Attempt == attempt {
		noted[last] = entry
		return noted
	}
	return append(noted, entry)
}

// MovedToStronger records a run's one move to the stronger model made before a
// repair attempt the run was resumed into, rather than one its repair loop
// starts and saves with the attempt (orchestrator's
// moveResumedRepairToStronger): the trial's record carrying the move, and the
// model and effort the run asks for from that attempt on. A stored run that
// already records a move, or records no trial, changes nothing.
type MovedToStronger struct {
	Record            CheaperFirst
	Model             string
	ModelReason       string
	Effort            string
	EffortDescription string
	At                time.Time
}

func (u MovedToStronger) apply(state *State) (bool, error) {
	if u.Record.Move == nil {
		return false, errors.New("a move to the stronger model must say what moved")
	}
	if state.CheaperFirst == nil || state.CheaperFirst.Move != nil {
		return false, nil
	}
	record := u.Record
	state.CheaperFirst = &record
	state.DeveloperModel, state.DeveloperModelReason = u.Model, u.ModelReason
	state.ProviderEffort, state.ProviderEffortDescription = u.Effort, u.EffortDescription
	state.UpdatedAt = u.At
	return true, nil
}

// LandedDefect is a defect traced to a run's change after it landed.
type LandedDefect struct {
	At   time.Time `json:"at"`
	Note string    `json:"note"`
}

// MaxLandedDefectNoteBytes bounds what a recorded defect says.
const MaxLandedDefectNoteBytes = 2048

// ErrNotInCheaperFirstTrial refuses a defect recorded on a run that took no
// part in the trial.
var ErrNotInCheaperFirstTrial = errors.New("the run took no part in the trial of starting developers on the cheaper model")

// RecordDefectAfterLanding records a defect traced to a run's change after it
// landed. It is refused on a run that took no part in the trial and on one
// that did not land, and recording the same note again changes nothing.
type RecordDefectAfterLanding struct {
	Note string
	At   time.Time
}

func (u RecordDefectAfterLanding) apply(state *State) (bool, error) {
	note := strings.TrimSpace(u.Note)
	if note == "" {
		return false, errors.New("a defect after landing must say what the defect is")
	}
	if len(note) > MaxLandedDefectNoteBytes {
		return false, fmt.Errorf("a defect after landing is %d bytes; the limit is %d", len(note), MaxLandedDefectNoteBytes)
	}
	if state.CheaperFirst == nil || state.CheaperFirst.Arm == CheaperFirstStopped {
		return false, fmt.Errorf("run %s: %w", state.RunID, ErrNotInCheaperFirstTrial)
	}
	if state.Integration == nil {
		return false, fmt.Errorf("run %s records no landing of its change, and only a change that landed can have a defect after landing", state.RunID)
	}
	if existing := state.CheaperFirst.DefectAfterLanding; existing != nil && existing.Note == note {
		return false, nil
	}
	record := *state.CheaperFirst
	record.DefectAfterLanding = &LandedDefect{At: u.At, Note: note}
	state.CheaperFirst = &record
	return true, nil
}

// CheaperFirstTrialRuns lists every run that took part in the trial, read
// through the run index. A record that would not read is left out rather than
// refusing the listing: the trial's place is a comparison, not a claim on
// work.
func (s *Store) CheaperFirstTrialRuns() ([]State, error) {
	return s.indexed("in the trial of starting developers on the cheaper model",
		func(entry runIndexEntry) bool { return entry.CheaperFirst },
		func(unreadableRun) bool { return false },
		func(state State) bool { return state.CheaperFirst != nil })
}

// CheaperFirstTrialState is what the run records say of the trial before a new
// eligible run starts: how many runs it has alternated, which arm the next
// takes, and the run whose defect stopped it, if one has.
type CheaperFirstTrialState struct {
	Runs      int
	NextArm   CheaperFirstArm
	StoppedBy string
	StopNote  string
}

// ReadCheaperFirstTrial reads the trial off the run records. Runs alternate by
// their order in the trial, cheaper first; the trial stops at the first defect
// found after landing on a run that started cheaper.
func ReadCheaperFirstTrial(runs []State) CheaperFirstTrialState {
	var trial CheaperFirstTrialState
	for _, run := range runs {
		record := run.CheaperFirst
		if record == nil || record.Arm == CheaperFirstStopped {
			continue
		}
		trial.Runs++
		if record.Arm == CheaperFirstCheaper && record.DefectAfterLanding != nil && trial.StoppedBy == "" {
			trial.StoppedBy, trial.StopNote = run.RunID, record.DefectAfterLanding.Note
		}
	}
	switch {
	case trial.StoppedBy != "":
		trial.NextArm = CheaperFirstStopped
	case trial.Runs%2 == 0:
		trial.NextArm = CheaperFirstCheaper
	default:
		trial.NextArm = CheaperFirstStronger
	}
	return trial
}

// CheaperFirstArmFigures is one arm of the trial, as the comparison reads it.
type CheaperFirstArmFigures struct {
	Runs int `json:"runs"`
	// Merged counts runs whose change landed.
	Merged int `json:"merged"`
	// CostUSD is what every run of the arm cost, landed or not, and
	// CostPerMergedUSD that divided by the changes it landed. Unpriced counts
	// runs whose cost could not be read, which CostUSD leaves out.
	CostUSD          float64 `json:"cost_usd"`
	CostPerMergedUSD float64 `json:"cost_per_merged_usd,omitempty"`
	Unpriced         int     `json:"unpriced,omitempty"`
	// Reviewed counts runs whose change reached a first review, and
	// FirstReviewApproved how many of those it approved.
	Reviewed            int `json:"reviewed"`
	FirstReviewApproved int `json:"first_review_approved"`
	// RepairRounds is every repair attempt the arm's runs made.
	RepairRounds int `json:"repair_rounds"`
	// Moves counts runs that moved to the stronger model.
	Moves               int `json:"moves,omitempty"`
	DefectsAfterLanding int `json:"defects_after_landing"`
}

// CheaperFirstTrialFigures is the trial's comparison.
type CheaperFirstTrialFigures struct {
	Target    int                    `json:"target"`
	Runs      int                    `json:"runs"`
	Cheaper   CheaperFirstArmFigures `json:"cheaper"`
	Stronger  CheaperFirstArmFigures `json:"stronger"`
	StoppedBy string                 `json:"stopped_by,omitempty"`
	StopNote  string                 `json:"stop_note,omitempty"`
	// Defects lists each defect after landing, by run.
	Defects []CheaperFirstDefect `json:"defects,omitempty"`
}

// CheaperFirstDefect is one recorded defect after landing.
type CheaperFirstDefect struct {
	RunID      string          `json:"run_id"`
	WorkItemID string          `json:"work_item_id"`
	Arm        CheaperFirstArm `json:"arm"`
	Note       string          `json:"note"`
}

// SummarizeCheaperFirstTrial compares the trial's two arms from the run
// records and what each run cost, keyed by run ID. A run with no price is
// counted as unpriced rather than as costing nothing.
func SummarizeCheaperFirstTrial(runs []State, costs map[string]float64, target int) CheaperFirstTrialFigures {
	trial := ReadCheaperFirstTrial(runs)
	figures := CheaperFirstTrialFigures{Target: target, Runs: trial.Runs, StoppedBy: trial.StoppedBy, StopNote: trial.StopNote}
	for _, run := range runs {
		record := run.CheaperFirst
		if record == nil || record.Arm == CheaperFirstStopped {
			continue
		}
		arm := &figures.Stronger
		if record.Arm == CheaperFirstCheaper {
			arm = &figures.Cheaper
		}
		arm.Runs++
		if run.Integration != nil {
			arm.Merged++
		}
		if cost, priced := costs[run.RunID]; priced {
			arm.CostUSD += cost
		} else {
			arm.Unpriced++
		}
		if record.FirstReview != "" {
			arm.Reviewed++
			if record.FirstReview == "approve" {
				arm.FirstReviewApproved++
			}
		}
		arm.RepairRounds += run.RepairAttempts
		if record.Move != nil {
			arm.Moves++
		}
		if record.DefectAfterLanding != nil {
			arm.DefectsAfterLanding++
			figures.Defects = append(figures.Defects, CheaperFirstDefect{RunID: run.RunID, WorkItemID: run.WorkItemID, Arm: record.Arm, Note: record.DefectAfterLanding.Note})
		}
	}
	for _, arm := range []*CheaperFirstArmFigures{&figures.Cheaper, &figures.Stronger} {
		if arm.Merged > 0 {
			arm.CostPerMergedUSD = arm.CostUSD / float64(arm.Merged)
		}
	}
	sort.Slice(figures.Defects, func(i, j int) bool { return figures.Defects[i].RunID < figures.Defects[j].RunID })
	return figures
}

// CheaperFirstTrialFigures compares the trial's two arms from the runs that
// took part in it, each priced from its own record as `yoyo cost` prices it.
func (s *Store) CheaperFirstTrialFigures(target int) (CheaperFirstTrialFigures, error) {
	runs, err := s.CheaperFirstTrialRuns()
	if err != nil {
		return CheaperFirstTrialFigures{}, err
	}
	costs := make(map[string]float64, len(runs))
	for _, run := range runs {
		if price := s.priceRun(run); price.Known() {
			costs[run.RunID] = price.CostUSD
		}
	}
	return SummarizeCheaperFirstTrial(runs, costs, target), nil
}

// PriceRun is what one recorded run has cost, read through the tolerant door
// and priced from its own event log exactly as `yoyo cost` prices it. A run
// whose record cannot be read is an error; one whose log cannot price it is a
// price that says it is unknown.
func (s *Store) PriceRun(runID string) (RunPrice, error) {
	state, err := s.Read(runID)
	if err != nil {
		return RunPrice{}, err
	}
	return s.priceRun(state), nil
}

// RunSpentSoFar is what a run has cost up to now, priced from the run's own
// event log exactly as `yoyo cost` prices it (priceRun). It is what a developer
// run records when it moves to the stronger model. The read is the one run's
// log, so it stays the same size however much history the product keeps.
// False where the log cannot price the run, which leaves the figure unknown.
func (s *Store) RunSpentSoFar(state State) (float64, bool) {
	price := s.priceRun(state)
	if !price.Known() {
		return 0, false
	}
	return price.CostUSD, true
}
