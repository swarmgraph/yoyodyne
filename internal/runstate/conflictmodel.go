package runstate

// What a run recorded where execution.conflict_resolution_model reached its
// first attempt at a conflict, and the trial that compares the two models on
// such attempts (docs/designs/approved-changes-that-no-longer-merge.md,
// decision 5; internal/orchestrator/conflictmodel.go decides it).
//
// As with the trial of starting developers on the cheaper model
// (cheaperfirst.go), the trial keeps no store of its own: each attempt the
// setting reached carries which side it took, and the trial is read back off
// the run records and the prices already kept for them.

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ConflictModelArm is which model an attempt the setting reached asked for.
type ConflictModelArm string

const (
	// ConflictModelCheaper asked for the cheaper model, as one side of the trial.
	ConflictModelCheaper ConflictModelArm = "cheaper"
	// ConflictModelStronger asked for the stronger model, as the trial's other
	// side.
	ConflictModelStronger ConflictModelArm = "stronger"
	// ConflictModelStopped came after a defect found after landing on a
	// resolution made on the cheaper model stopped the trial, so it asked for
	// the model the run would have asked for anyway.
	ConflictModelStopped ConflictModelArm = "stopped"
	// ConflictModelSetting asked for the kind the setting names, outside any
	// trial: the setting names the stronger kind, or the trial has had all its
	// attempts.
	ConflictModelSetting ConflictModelArm = "setting"
)

// ConflictModel is one run's record of the attempt the setting reached. Only a
// run's first attempt at a conflict can be reached, so a run has at most one.
type ConflictModel struct {
	// Handback is the hand-back for a conflict the attempt answers
	// (ConflictHandback).
	Handback string           `json:"handback"`
	Arm      ConflictModelArm `json:"arm"`
	// Ordinal is the attempt's place in the trial, from 1, on the trial's two
	// sides only.
	Ordinal int `json:"ordinal,omitempty"`
	// Model is the model the attempt asks for. A stopped attempt names none: it
	// asks for the run's own model.
	Model  string    `json:"model,omitempty"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
	// ChecksFailed says the resolved change failed its checks before any review
	// of it, and FirstReview is the decision of the first independent review of
	// the resolved change: the run's own review before it rejoins the merge
	// queue, or the queue's review of its candidate where it rejoined with none
	// (NoteConflictModelQueueReview). A failure or a refusal moves the run to
	// the stronger model.
	ChecksFailed bool   `json:"checks_failed,omitempty"`
	FirstReview  string `json:"first_review,omitempty"`
	// DefectAfterLanding is a defect somebody traced to the resolution after
	// the change landed. On the cheaper side it stops the trial.
	DefectAfterLanding *LandedDefect `json:"defect_after_landing,omitempty"`
}

// OnTrialSide reports an attempt on one of the trial's two sides.
func (m ConflictModel) OnTrialSide() bool {
	return m.Arm == ConflictModelCheaper || m.Arm == ConflictModelStronger
}

// ConflictModelAsked is the model the outstanding attempt at a conflict asks
// for because the setting reached it, and false where it did not or the
// attempt is over.
func (s State) ConflictModelAsked() (string, bool) {
	record := s.ConflictModel
	if record == nil || record.Model == "" || !s.ResolvingInNewSession() || s.ReplayConflict.Handback != record.Handback {
		return "", false
	}
	return record.Model, true
}

// ResolvingConflictModelAttempt reports the attempt the setting reached as the
// one outstanding, which is when what follows it is noted on the record.
func (s State) ResolvingConflictModelAttempt() bool {
	return s.ConflictModel != nil && s.ResolvingConflict() && s.ReplayConflict.Handback == s.ConflictModel.Handback
}

// NoteConflictModelQueueReview records the merge queue's review of the run's
// candidate as the first review of the resolution the setting reached, where
// nothing judged the resolution before it: no review of its own, as a
// resolution confined to the conflicting files rejoins the queue with none,
// and no failed checks; and no later attempt at a conflict has been made since.
// An approval is known from the queue's landing (IntegrationRecorded) and a
// refusal from its hand-back. It reports whether it noted anything.
func (s *State) NoteConflictModelQueueReview(decision string) bool {
	record := s.ConflictModel
	decision = strings.TrimSpace(decision)
	if record == nil || record.FirstReview != "" || record.ChecksFailed || decision == "" {
		return false
	}
	if s.Reconciliation == nil || s.Reconciliation.Handback != record.Handback {
		return false
	}
	noted := *record
	noted.FirstReview = decision
	s.ConflictModel = &noted
	return true
}

// ConflictModelTrialRuns lists every run the setting reached, read through the
// run index. A record that would not read is left out, as the other trial's
// listing leaves it out.
func (s *Store) ConflictModelTrialRuns() ([]State, error) {
	return s.indexed("whose first conflict execution.conflict_resolution_model reached",
		func(entry runIndexEntry) bool { return entry.ConflictModel },
		func(unreadableRun) bool { return false },
		func(state State) bool { return state.ConflictModel != nil })
}

// ConflictModelTrialState is what the run records say of the trial before an
// eligible attempt starts.
type ConflictModelTrialState struct {
	Attempts  int
	NextArm   ConflictModelArm
	StoppedBy string
	StopNote  string
}

// ReadConflictModelTrial reads the trial off the run records. Attempts
// alternate by their order in the trial, cheaper first, for target attempts;
// the trial stops at the first defect found after landing on a resolution made
// on the cheaper model.
func ReadConflictModelTrial(runs []State, target int) ConflictModelTrialState {
	var trial ConflictModelTrialState
	for _, run := range runs {
		record := run.ConflictModel
		if record == nil || !record.OnTrialSide() {
			continue
		}
		trial.Attempts++
		if record.Arm == ConflictModelCheaper && record.DefectAfterLanding != nil && trial.StoppedBy == "" {
			trial.StoppedBy, trial.StopNote = run.RunID, record.DefectAfterLanding.Note
		}
	}
	switch {
	case trial.StoppedBy != "":
		trial.NextArm = ConflictModelStopped
	case trial.Attempts >= target:
		trial.NextArm = ConflictModelSetting
	case trial.Attempts%2 == 0:
		trial.NextArm = ConflictModelCheaper
	default:
		trial.NextArm = ConflictModelStronger
	}
	return trial
}

// ConflictModelSideFigures is one side of the trial.
type ConflictModelSideFigures struct {
	Attempts int `json:"attempts"`
	// Landed counts runs whose change landed. CostUSD is what every run of the
	// side cost, landed or not, priced as `yoyo cost` prices it, and
	// CostPerLandedUSD that divided by the changes landed. Unpriced counts runs
	// left out of the cost because they could not be priced.
	Landed           int     `json:"landed"`
	CostUSD          float64 `json:"cost_usd"`
	CostPerLandedUSD float64 `json:"cost_per_landed_usd,omitempty"`
	Unpriced         int     `json:"unpriced,omitempty"`
	// ChecksFailed counts resolutions that failed their checks; Reviewed those
	// whose resolved change reached a review, and Refused how many of those the
	// first review did not approve.
	ChecksFailed        int `json:"checks_failed"`
	Reviewed            int `json:"reviewed"`
	Refused             int `json:"refused"`
	DefectsAfterLanding int `json:"defects_after_landing"`
}

// ConflictModelTrialFigures is the trial's comparison.
type ConflictModelTrialFigures struct {
	Target    int                      `json:"target"`
	Attempts  int                      `json:"attempts"`
	Cheaper   ConflictModelSideFigures `json:"cheaper"`
	Stronger  ConflictModelSideFigures `json:"stronger"`
	StoppedBy string                   `json:"stopped_by,omitempty"`
	StopNote  string                   `json:"stop_note,omitempty"`
}

// SummarizeConflictModelTrial compares the trial's two sides from the run
// records and what each run cost, keyed by run ID.
func SummarizeConflictModelTrial(runs []State, costs map[string]float64, target int) ConflictModelTrialFigures {
	trial := ReadConflictModelTrial(runs, target)
	figures := ConflictModelTrialFigures{Target: target, Attempts: trial.Attempts, StoppedBy: trial.StoppedBy, StopNote: trial.StopNote}
	for _, run := range runs {
		record := run.ConflictModel
		if record == nil || !record.OnTrialSide() {
			continue
		}
		side := &figures.Stronger
		if record.Arm == ConflictModelCheaper {
			side = &figures.Cheaper
		}
		side.Attempts++
		if run.Integration != nil {
			side.Landed++
		}
		if cost, priced := costs[run.RunID]; priced {
			side.CostUSD += cost
		} else {
			side.Unpriced++
		}
		if record.ChecksFailed {
			side.ChecksFailed++
		}
		if record.FirstReview != "" {
			side.Reviewed++
			if record.FirstReview != "approve" {
				side.Refused++
			}
		}
		if record.DefectAfterLanding != nil {
			side.DefectsAfterLanding++
		}
	}
	for _, side := range []*ConflictModelSideFigures{&figures.Cheaper, &figures.Stronger} {
		if side.Landed > 0 {
			side.CostPerLandedUSD = side.CostUSD / float64(side.Landed)
		}
	}
	return figures
}

// ConflictModelTrialFigures compares the trial's two sides from the runs that
// took part in it, each priced from its own record.
func (s *Store) ConflictModelTrialFigures(target int) (ConflictModelTrialFigures, error) {
	runs, err := s.ConflictModelTrialRuns()
	if err != nil {
		return ConflictModelTrialFigures{}, err
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].ConflictModel.At.Before(runs[j].ConflictModel.At) })
	costs := make(map[string]float64, len(runs))
	for _, run := range runs {
		if price := s.priceRun(run); price.Known() {
			costs[run.RunID] = price.CostUSD
		}
	}
	return SummarizeConflictModelTrial(runs, costs, target), nil
}

// ErrNoConflictModelAttempt refuses a defect traced to a resolution on a run
// whose first conflict the setting never reached.
var ErrNoConflictModelAttempt = errors.New("execution.conflict_resolution_model never reached this run's first conflict")

// RecordResolutionDefect records a defect traced, after the change landed, to
// the resolution of the run's first conflict. It is refused on a run the
// setting never reached and on one that did not land, and recording the same
// note again changes nothing.
type RecordResolutionDefect struct {
	Note string
	At   time.Time
}

func (u RecordResolutionDefect) apply(state *State) (bool, error) {
	note := strings.TrimSpace(u.Note)
	if note == "" {
		return false, errors.New("a defect after landing must say what the defect is")
	}
	if len(note) > MaxLandedDefectNoteBytes {
		return false, fmt.Errorf("a defect after landing is %d bytes; the limit is %d", len(note), MaxLandedDefectNoteBytes)
	}
	if state.ConflictModel == nil {
		return false, fmt.Errorf("run %s: %w", state.RunID, ErrNoConflictModelAttempt)
	}
	if state.Integration == nil {
		return false, fmt.Errorf("run %s records no landing of its change, and only a change that landed can have a defect after landing", state.RunID)
	}
	if existing := state.ConflictModel.DefectAfterLanding; existing != nil && existing.Note == note {
		return false, nil
	}
	record := *state.ConflictModel
	record.DefectAfterLanding = &LandedDefect{At: u.At, Note: note}
	state.ConflictModel = &record
	return true, nil
}
