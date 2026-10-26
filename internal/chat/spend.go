package chat

// What a turn spends, attributed so it can be read beside what a run spends.
//
// A conversation turn is a provider invocation and costs what it costs, and
// until now the only account of that was a number shown to whoever happened to
// be at the terminal. That is enough to notice an expensive afternoon and no use
// at all afterwards: the session ends, the numbers go with it, and nothing
// durable says the management conversation cost anything.
//
// So a turn lands in the same cost log a run's invocations do, one line each. It
// is charged to the conversation rather than to a work item, deliberately: a
// conversation that discussed five items is not attributable to any one of them,
// and putting it on whichever came up last would be a guess dressed as a join.

import (
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/spend"
	"github.com/mason-bryant/yoyodyne/internal/usage"
)

// spendAttribution is what this turn's invocation is charged to. The role and
// the requested model are on the request the meter already sees; this is the
// rest — which conversation, which agent, and on whose account.
func (s *Session) spendAttribution() spend.Attribution {
	return spend.Attribution{
		ProductID:      s.options.ProductID,
		Agent:          s.options.Agent,
		Phase:          runstate.SpendPhaseConversation,
		AccountAlias:   s.options.AccountAlias,
		ConfigRevision: s.options.ConfigRevision,
		Backend:        s.options.Provider,
		ConversationID: s.state.ConversationID,
		Billing:        s.billing(s.options.AccountAlias),
	}
}

// billing is how the named account is paid for, as the configuration says.
func (s *Session) billing(alias string) usage.BillingMode {
	if s.options.AccountBilling == nil {
		return usage.BillingNotRecorded
	}
	return s.options.AccountBilling(alias)
}

// countSpend takes one invocation's own cost off the line the meter has just
// recorded. It is the only thing that moves these three figures, so what the
// operator is shown, what a budgeted session counts, and what the cost log holds
// are one number rather than three readings of the provider's.
//
// An invocation nobody was told the price of adds nothing rather than adding a
// zero it would be indistinguishable from: the line says which it is, and these
// figures are a running total with no room to say so. What says so is the cost
// log, where the classification is kept.
func (s *Session) countSpend(line runstate.Spend) {
	if !line.Known() {
		return
	}
	s.lastInvocationCostUSD += line.AmountUSD
	s.turnCostUSD += line.AmountUSD
	s.sessionCostUSD += line.AmountUSD
}

// failoverAttribution is the same, for a turn served on the endpoint this
// conversation fails over to. The account and the provider are that endpoint's
// rather than this conversation's, because they are what the money was actually
// spent on: a crossing is a different subscription, and a line charging it to the
// account whose window closed would attribute the spend to the one place it did
// not happen.
func (s *Session) failoverAttribution() spend.Attribution {
	attribution := s.spendAttribution()
	attribution.AccountAlias = s.options.FailoverEndpoint.AccountAlias
	attribution.Backend = s.options.FailoverEndpoint.Provider
	attribution.Billing = s.billing(s.options.FailoverEndpoint.AccountAlias)
	return attribution
}
