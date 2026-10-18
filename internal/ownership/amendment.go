package ownership

import "github.com/mason-bryant/yoyodyne/internal/domain"

// ResolveAmendment is who moves an undecided proposed change: the role that owns
// the document it is against, which decides it from its own conversation. The
// operator is named only for a change of fundamental intent — a change to the
// brief or the goals that changes what they admit or refuse, or
// does not say whether it does — which the caller reports from the proposal's
// record (amendment.Proposal.ReservedForOperator), never from prose.
func ResolveAmendment(owner domain.AgentRole, fundamentalIntent bool) Mover {
	if fundamentalIntent {
		return MoverOperator
	}
	return MoverOf(owner)
}
