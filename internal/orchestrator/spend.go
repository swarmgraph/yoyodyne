package orchestrator

// What a run's provider invocations spend, attributed as each of them is made.
//
// The money itself comes from the provider and the classification from the
// meter; what this file supplies is the half neither of them can know — which
// work the invocation served, which configured agent made it, and on whose
// account. All of it is read from the run's own durable state rather than from
// the configuration in force right now, so a run resumed by a later process
// charges its spend to the account and the configuration it was set up under
// rather than to whatever the file says today.

import (
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/spend"
)

// SpendLog is the cost log one provider invocation lands in. It is satisfied by
// runstate.SpendStore, and it is spend.Log restated here so the pipeline's own
// wiring names a type of its own rather than reaching into the meter's.
type SpendLog interface {
	Append(line runstate.Spend) error
	ReportedSessionTotal(sessionID string) (float64, bool, error)
}

// spendAttribution is what one of this run's invocations is charged to.
func (a *activeRun) spendAttribution(role domain.AgentRole, phase runstate.SpendPhase) spend.Attribution {
	p := a.pipeline
	return spend.Attribution{
		ProductID:      p.Config.Product.ID,
		Agent:          p.agentNameForRole(role),
		Phase:          phase,
		AccountAlias:   a.state.AccountAlias,
		ConfigRevision: a.state.ConfigRevision,
		Backend:        p.agentForRole(role).Backend,
		RunID:          a.state.RunID,
		WorkItemID:     a.state.WorkItemID,
		Billing:        p.Config.AccountBilling(a.state.AccountAlias),
	}
}

// developmentPhase says which part of the work the developer invocation about to
// be made serves. It is the run's own repair count and nothing else: the first
// attempt is the development, every attempt after it is a repair, and an
// invocation reissued into an attempt the provider refused or killed is still
// that attempt. That is the same split a run's event log is priced by, so a line
// here and the run it came from never disagree about where the money went.
func (a *activeRun) developmentPhase() runstate.SpendPhase {
	if a.state.RepairAttempts > 0 {
		return runstate.SpendPhaseRepair
	}
	return runstate.SpendPhaseDevelopment
}

// spendAttribution is what a branch review's one invocation is charged to. It
// reads the configuration in force rather than a run's record because a branch
// review has no run: the review is being made now, so what is configured now is
// what makes it.
//
// It is charged to the review itself rather than to a run, and says so in the
// field that means that. A branch review is not a run and serves no work item,
// so a line that carried its identifier as a run id would read as evidence
// about a run nothing ever made.
func (b BranchReviewer) spendAttribution(reviewID string) spend.Attribution {
	// The account is the reviewer agent's own rather than the configuration's
	// single one, because a pooled configuration has no single one and a cost line
	// naming none is a line nothing can attribute.
	return spend.Attribution{
		ProductID:      b.Config.Product.ID,
		Agent:          b.agentName(),
		Phase:          runstate.SpendPhaseReview,
		AccountAlias:   b.account().Alias,
		ConfigRevision: b.Config.Revision(),
		Backend:        Pipeline{Config: b.Config}.agentForRole(domain.RoleReviewer).Backend,
		BranchReviewID: reviewID,
		Billing:        b.Config.AccountBilling(b.account().Alias),
	}
}
