package ownership

import "github.com/mason-bryant/yoyodyne/internal/domain"

type Mover string

const (
	MoverOperator Mover = "operator"
	// MoverHarness is the harness acting at its next sweep or poll: held work
	// whose decision is recorded and not yet carried out, or a promotion whose
	// record holds no pull request for the next reconcile to look up.
	MoverHarness Mover = "harness"
	// MoverForge is the forge merging a request it has queued.
	MoverForge Mover = "forge"
	// MoverProvider is the model provider answering again. No attention entry is
	// attributed to it — a usage window lifts on the provider's clock, which is
	// nobody's move — and it is here because a program manager's lane report may
	// name it as what a blocker is waiting on, and that report's movers are this
	// vocabulary's.
	MoverProvider Mover = "provider"
	// MoverNobody is a wait nobody ends: a provider's usage window lifts on the
	// provider's clock.
	MoverNobody Mover = "nobody"
	// MoverUnnamed is the role a conversation-carried item names where the
	// harness cannot read which: a bare marker, or one it does not recognize.
	// It is a token rather than an absence so a surface counting by mover has
	// something to count it under.
	MoverUnnamed Mover = "unnamed-role"

	MoverProductManager     = Mover(domain.RoleProductManager)
	MoverArchitect          = Mover(domain.RoleArchitect)
	MoverDevelopmentManager = Mover(domain.RoleDevelopmentManager)
	// MoverProgramManager is a program manager instance, on an entry about its
	// own passes: the one role whose agents are many, so the entry's record names
	// which instance.
	MoverProgramManager = Mover(domain.RoleProgramManager)
)

// MoverOf is the mover for one of the harness's roles, and the unnamed mover
// for a role the harness does not recognize — which includes the empty role a
// conversation marker yields when it names none.
func MoverOf(role domain.AgentRole) Mover {
	if !role.Valid() {
		return MoverUnnamed
	}
	return Mover(role)
}

// Movers is the whole vocabulary, in the order a surface lists them: the
// operator first, because the line is called "needs a human" and his is the
// count that says whether it needs him, and his are the only entries that line
// prints — every other mover's are printed under a line naming it; then the roles in the hierarchy's
// order; then the movers that are not people.
func Movers() []Mover {
	return []Mover{
		MoverOperator,
		MoverProductManager,
		MoverArchitect,
		MoverDevelopmentManager,
		MoverProgramManager,
		Mover(domain.RoleDeveloper),
		Mover(domain.RoleReviewer),
		MoverHarness,
		MoverForge,
		MoverProvider,
		MoverNobody,
		MoverUnnamed,
	}
}

// Valid reports whether a token is one of the movers.
func (m Mover) Valid() bool {
	for _, known := range Movers() {
		if m == known {
			return true
		}
	}
	return false
}

// Possessive is the mover as every sentence on the attention line opens: "the
// operator's", "the development manager's", "nobody's". It is worded once here
// so a surface grouping the line by mover and a terminal printing it name the
// same person the same way.
func (m Mover) Possessive() string {
	switch m {
	case MoverOperator:
		return "the operator's"
	case MoverHarness:
		return "the harness's"
	case MoverForge:
		return "the forge's"
	case MoverProvider:
		return "the provider's"
	case MoverNobody:
		return "nobody's"
	case MoverUnnamed:
		return "the role it names"
	default:
		return "the " + domain.AgentRole(m).Title() + "'s"
	}
}

// WaitingOn is the head the fourth line prints over the entries a mover other
// than the operator moves: "Waiting on the development manager", "Waiting on
// the harness". It names the mover rather than a person, because none of these
// movers is the operator, and the words "a person" and "a human" are kept for
// what he moves.
func (m Mover) WaitingOn() string {
	switch m {
	case MoverOperator:
		return "Waiting on the operator"
	case MoverHarness:
		return "Waiting on the harness"
	case MoverForge:
		return "Waiting on the forge"
	case MoverProvider:
		return "Waiting on the provider"
	case MoverNobody:
		return "Waiting on nobody's move"
	case MoverUnnamed:
		return "Waiting on a role the harness cannot name"
	}
	if domain.AgentRole(m).Valid() {
		return "Waiting on the " + domain.AgentRole(m).Title()
	}
	return "Waiting on " + string(m)
}
