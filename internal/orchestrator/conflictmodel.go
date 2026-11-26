package orchestrator

// The model a run's first attempt at a small conflict asks for
// (execution.conflict_resolution_model;
// docs/designs/approved-changes-that-no-longer-merge.md, decision 5).
//
// Left out, which is the default, nothing here runs and an attempt asks for the
// model the run would have asked for anyway (developerModel). Named, it is
// decided once, as the attempt is recorded (reconcile), and written on the run
// beside the attempt, so a resumed attempt asks for the same model. It reaches
// an attempt only where config.Execution.ConflictModelEligible says the item
// and the conflict allow it, the attempt is the run's first at a conflict on an
// approved change, and the run has not moved to the stronger model: a run that
// moved after a failed review or failed checks keeps the stronger for every
// attempt after (cheaperfirst.go), so it is never moved back. It never reaches
// a run whose model was chosen for it — by execution.developer_models, by a
// developer slot's endpoint, or by being recorded on another backend.
//
// The model asked for holds for that one attempt. Once the resolved change has
// been checked, the run asks for its own model again; where the resolution
// failed its checks or its review, that is the stronger model, moved to as any
// failed review or failed checks moves a run that started cheaper
// (moveToStronger), and the run's own model for every other run.
//
// With the cheaper kind named, eligible attempts alternate the cheaper and the
// stronger model, cheaper first, for config.ConflictModelTrialAttempts
// attempts, read off the run records (runstate.ReadConflictModelTrial); after
// that each asks for the cheaper. A defect traced to a resolution made on the
// cheaper model, recorded after it landed, stops the trial at once: every
// eligible attempt after it asks for the run's own model.

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// conflictModelRuns is the part of the run store the trial is read from. A
// store without it leaves every attempt on the model it always asked for.
type conflictModelRuns interface {
	ConflictModelTrialRuns() ([]runstate.State, error)
}

// settleConflictModel decides, for the attempt reconcile has just recorded,
// whether the setting reaches it and which model it asks for, and writes that
// on the run. The caller's save carries it.
func (a *activeRun) settleConflictModel() error {
	kind, named := a.pipeline.Config.Execution.ConflictModelKind()
	state := &a.state
	if !named || state.ConflictModel != nil || !state.ResolvingInNewSession() || state.Reconciliation == nil || state.Reconciliation.Attempt > 1 {
		return nil
	}
	if state.Document != nil || state.OutsideFile != nil || a.routedDeveloper() || a.onAnotherBackend() ||
		config.MatchesDeveloperModelRule(a.pipeline.Config.Execution.DeveloperModels, state.WorkItemLabels) {
		return nil
	}
	if state.CheaperFirst != nil && state.CheaperFirst.Move != nil {
		return nil
	}
	runs, readable := a.pipeline.Store.(conflictModelRuns)
	if !readable {
		return nil
	}
	conflict := *state.ReplayConflict
	size := config.ConflictModelConflict{Files: len(conflict.Paths), Omitted: conflict.Omitted}
	for _, handback := range state.ConflictHandbacks {
		if handback.ID == conflict.Handback {
			size.Lines = handback.Lines
		}
	}
	eligibility := a.pipeline.Config.Execution.ConflictModelEligible(state.WorkItemLabels, state.WorkItemPriority, size)
	if !eligibility.Eligible {
		return nil
	}
	provider := state.Backend
	if provider == "" {
		provider = a.pipeline.developer().Backend
	}
	stronger, hasStronger := a.pipeline.Config.Execution.ModelOfKind(provider, config.ModelKindStronger)
	if !hasStronger {
		stronger = strings.TrimSpace(a.pipeline.developer().Model)
	}
	record := runstate.ConflictModel{Handback: conflict.Handback, At: a.pipeline.clock().Now().UTC()}
	if kind == config.ModelKindStronger {
		record.Arm, record.Model = runstate.ConflictModelSetting, stronger
		record.Reason = fmt.Sprintf("%s, so this first attempt at the conflict asks for the stronger model %s", eligibility.Reason, stronger)
		state.ConflictModel = &record
		return nil
	}
	cheaper, _ := a.pipeline.Config.Execution.ModelOfKind(provider, config.ModelKindCheaper)
	if cheaper == "" {
		return nil
	}
	listed, err := runs.ConflictModelTrialRuns()
	if err != nil {
		return refusedByEnvironment("the runs that took part in the trial of resolving small conflicts on the cheaper model could not be read", err)
	}
	trial := runstate.ReadConflictModelTrial(listed, config.ConflictModelTrialAttempts)
	record.Arm = trial.NextArm
	switch trial.NextArm {
	case runstate.ConflictModelStopped:
		record.Reason = fmt.Sprintf("%s, but the trial of resolving small conflicts on the cheaper model stopped on a defect found after landing on run %s (%s), so this attempt asks for the run's own model",
			eligibility.Reason, trial.StoppedBy, trial.StopNote)
	case runstate.ConflictModelCheaper:
		record.Ordinal, record.Model = trial.Attempts+1, cheaper
		record.Reason = fmt.Sprintf("%s, so this first attempt at the conflict asks for the cheaper model %s as attempt %d of the %d in the trial",
			eligibility.Reason, cheaper, record.Ordinal, config.ConflictModelTrialAttempts)
	case runstate.ConflictModelStronger:
		record.Ordinal, record.Model = trial.Attempts+1, stronger
		record.Reason = fmt.Sprintf("%s, so this first attempt at the conflict asks for the stronger model %s as attempt %d of the %d in the trial, for comparison with the attempts on %s",
			eligibility.Reason, stronger, record.Ordinal, config.ConflictModelTrialAttempts, cheaper)
	default:
		record.Model = cheaper
		record.Reason = fmt.Sprintf("%s, and the trial has had its %d attempts, so this first attempt at the conflict asks for the cheaper model %s",
			eligibility.Reason, config.ConflictModelTrialAttempts, cheaper)
	}
	state.ConflictModel = &record
	return nil
}

// noteConflictModelChecksFailed records that the resolution the setting
// reached failed its checks. recordCheckFailure calls it before it clears the
// conflict the attempt answered.
func (a *activeRun) noteConflictModelChecksFailed() {
	if !a.state.ResolvingConflictModelAttempt() || a.state.ConflictModel.FirstReview != "" {
		return
	}
	noted := *a.state.ConflictModel
	noted.ChecksFailed = true
	a.state.ConflictModel = &noted
}

// noteConflictModelReview records the first independent review's decision on
// the resolved change, where the resolution passed its checks and no later
// attempt at a conflict has been made since.
func (a *activeRun) noteConflictModelReview(decision review.Decision) {
	record := a.state.ConflictModel
	if record == nil || record.FirstReview != "" || record.ChecksFailed || decision == "" {
		return
	}
	if a.state.Reconciliation == nil || a.state.Reconciliation.Handback != record.Handback {
		return
	}
	noted := *record
	noted.FirstReview = string(decision)
	a.state.ConflictModel = &noted
}
