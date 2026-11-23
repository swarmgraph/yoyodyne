package orchestrator

// Developer runs that start on the cheaper model and move once to the stronger
// (docs/designs/reducing-what-the-factory-costs.md, section 4).
//
// With execution.developer_cheaper_first on, a run over an eligible item
// (config.Execution.CheaperFirstEligible) is settled when it is reserved: it
// starts on its provider's cheaper model or its stronger one, alternating by
// its place in the trial, and the choice is written on the run beside the
// model and read back by every invocation, as any developer model is. A run
// that started cheaper moves once to the stronger model at its first failed
// review or failed checks, before the repair attempt that answers it, and
// keeps the stronger for the rest of its attempts; the move, its reason and
// what the run had spent when it moved are recorded on the run.
//
// Three kinds of run are left exactly as they were: one whose item
// execution.developer_models maps to a model (somebody chose that model for
// that work), one pinned to a developer slot's endpoint pair (the slot's
// endpoint names its model), and one whose provider names no cheaper model.
//
// The trial is read off the run records (runstate.ReadCheaperFirstTrial). A
// defect found after landing on a run that started cheaper stops it at once:
// every eligible run after that starts on the developer's configured model and
// is recorded as such, for as long as the setting stays on.

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// runSpender is the part of the run store a run reads what it has spent so
// far from: the run's own event log, priced as `yoyo cost` prices it. The
// harness's store has it; a store without it leaves the figure unknown.
type runSpender interface {
	RunSpentSoFar(state runstate.State) (float64, bool)
}

// cheaperFirstRuns is the part of the run store the trial is read from. The
// harness's store has it; a store without it starts every run on the model it
// always did.
type cheaperFirstRuns interface {
	CheaperFirstTrialRuns() ([]runstate.State, error)
}

// cheaperFirstApplies reports a developer run the setting could settle: it is
// over a tracker item, its item is eligible, and execution.developer_models
// sends it to no model of its own.
func (p Pipeline) cheaperFirstApplies(state runstate.State) (config.CheaperFirstEligibility, bool) {
	if state.Document != nil || state.OutsideFile != nil {
		return config.CheaperFirstEligibility{}, false
	}
	if _, readable := p.Store.(cheaperFirstRuns); !readable {
		return config.CheaperFirstEligibility{}, false
	}
	eligibility := p.Config.Execution.CheaperFirstEligible(state.WorkItemLabels, state.WorkItemPriority)
	if !eligibility.Eligible {
		return eligibility, false
	}
	if config.MatchesDeveloperModelRule(p.Config.Execution.DeveloperModels, state.WorkItemLabels) {
		return eligibility, false
	}
	return eligibility, true
}

// settleCheaperFirst decides which model an eligible run starts on and writes
// it on the run. The caller holds the rotation lease, so two runs starting at
// once cannot both read the same place in the trial.
func (p Pipeline) settleCheaperFirst(state *runstate.State, eligibility config.CheaperFirstEligibility) error {
	provider := state.Backend
	if provider == "" {
		provider = p.developer().Backend
	}
	cheaper, named := p.Config.Execution.ModelOfKind(provider, config.ModelKindCheaper)
	if !named {
		// A provider naming no cheaper model keeps its configured model, and the
		// run records nothing about a trial it took no part in.
		return nil
	}
	stronger, named := p.Config.Execution.ModelOfKind(provider, config.ModelKindStronger)
	if !named {
		stronger = strings.TrimSpace(p.developer().Model)
	}
	runs, err := p.Store.(cheaperFirstRuns).CheaperFirstTrialRuns()
	if err != nil {
		return refusedByEnvironment("the runs that took part in the trial of starting developers on the cheaper model could not be read", err)
	}
	trial := runstate.ReadCheaperFirstTrial(runs)
	record := runstate.CheaperFirst{Arm: trial.NextArm, StrongerModel: stronger}
	switch trial.NextArm {
	case runstate.CheaperFirstStopped:
		record.StrongerModel = ""
		record.Reason = fmt.Sprintf("%s, but the trial of starting developers on the cheaper model stopped on a defect found after landing on run %s (%s), so it starts on the developer's configured model",
			eligibility.Reason, trial.StoppedBy, trial.StopNote)
		state.CheaperFirst = &record
		return nil
	case runstate.CheaperFirstCheaper:
		record.Ordinal = trial.Runs + 1
		record.StartModel = cheaper
		record.Reason = fmt.Sprintf("%s, so under execution.developer_cheaper_first it starts on the cheaper model %s as run %d of the trial, and moves once to %s after its first failed review or failed checks",
			eligibility.Reason, cheaper, record.Ordinal, stronger)
	default:
		record.Ordinal = trial.Runs + 1
		record.StartModel = stronger
		record.Reason = fmt.Sprintf("%s, so under execution.developer_cheaper_first it starts on the stronger model %s as run %d of the trial, for comparison with the runs that start on %s",
			eligibility.Reason, stronger, record.Ordinal, cheaper)
	}
	state.CheaperFirst = &record
	state.DeveloperModel, state.DeveloperModelReason = record.StartModel, record.Reason
	return nil
}

// moveToStronger moves a run that started on the cheaper model to the stronger
// one, once, before the repair attempt that answers its first failed review or
// failed checks. failure says which, in words the record carries. The caller's
// repair saves the run, so the move is on the record before the stronger model
// is asked for anything.
func (a *activeRun) moveToStronger(failure string) {
	record := a.state.CheaperFirst
	if record == nil || record.Arm != runstate.CheaperFirstCheaper || record.Move != nil {
		return
	}
	if a.routedDeveloper() || a.onAnotherBackend() {
		return
	}
	from := a.developerModel()
	to := strings.TrimSpace(record.StrongerModel)
	if to == "" || to == from {
		return
	}
	move := runstate.ModelMove{
		At:            a.pipeline.clock().Now(),
		From:          from,
		To:            to,
		Reason:        failure,
		RepairAttempt: a.state.RepairAttempts + 1,
	}
	if spender, ok := a.pipeline.Store.(runSpender); ok {
		// An event log that cannot price the run leaves the cost unknown rather
		// than stopping a repair over a figure.
		if spent, known := spender.RunSpentSoFar(a.state); known {
			move.SpentBeforeUSD, move.SpentKnown = spent, true
		}
	}
	moved := *record
	moved.Move = &move
	a.state.CheaperFirst = &moved
	a.state.DeveloperModel = to
	a.state.DeveloperModelReason = fmt.Sprintf("started on the cheaper model %s under execution.developer_cheaper_first and moved once to the stronger model %s after %s, for repair attempt %d and every attempt after it",
		from, to, failure, move.RepairAttempt)
	agent := a.pipeline.developer()
	a.state.ProviderEffort = a.pipeline.Config.InvocationEffort(agent, to)
	a.state.ProviderEffortDescription = a.pipeline.Config.DescribeInvocationEffort(agent, a.state.ProviderEffort, "", false)
}

// noteFirstReview records the first independent review's decision on an
// eligible run's change, which the trial's first-review approval rate counts.
func (a *activeRun) noteFirstReview(decision review.Decision) {
	record := a.state.CheaperFirst
	if record == nil || record.Arm == runstate.CheaperFirstStopped || record.FirstReview != "" || decision == "" {
		return
	}
	noted := *record
	noted.FirstReview = string(decision)
	a.state.CheaperFirst = &noted
}
