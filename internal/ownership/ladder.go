package ownership

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// The escalation ladder (`docs/designs/ownership-and-the-operator.md`, under
// "The escalation ladder"): every escalation names a role, and each rung goes to
// the one above it. The operator is on no rung. He is named only for a reason
// on OperatorReasons, and an escalation, a report handling, or an ask that names
// him for anything else is refused here, with the rung to use instead.

// NamesOperator reports whether a name a role wrote — an escalation's target, an
// ask's addressee, a handling's "needs" — is the operator. It is here so the
// word for him is spelled once, in the package that decides when he is named.
func NamesOperator(name string) bool {
	return strings.TrimSpace(name) == string(MoverOperator)
}

// EscalatesTo is the roles one rung above a role: where it takes what it cannot
// decide itself. The Lead Product Manager is the top rung among the roles and
// has none; a role the harness does not recognize goes to her, because she is
// who classifies what nobody else owns.
func EscalatesTo(role domain.AgentRole) []domain.AgentRole {
	switch role {
	case domain.RoleDeveloper, domain.RoleReviewer:
		return []domain.AgentRole{domain.RoleDevelopmentManager}
	case domain.RoleDevelopmentManager:
		return []domain.AgentRole{domain.RoleProductManager, domain.RoleArchitect}
	case domain.RoleProductManager:
		return nil
	default:
		return []domain.AgentRole{domain.RoleProductManager}
	}
}

// Rung is the rung a role uses instead of the operator, in the ladder's own
// words, for a refusal to quote.
func Rung(role domain.AgentRole) string {
	switch role {
	case domain.RoleDeveloper, domain.RoleReviewer:
		return "take it to the development manager, by stopping the run or raising the item as one that cannot be met"
	case domain.RoleDevelopmentManager:
		return "escalate to the Lead Product Manager on the item's intent, or to the architect on its design"
	case domain.RoleProgramManager:
		return "take it to the Lead Product Manager, for anything outside your lane"
	case domain.RoleArchitect:
		return "take it to the Lead Product Manager, on anything touching intent"
	case domain.RoleProductManager:
		return "you are the top rung among the roles: leave it with the role whose remedy it is, or admit a defect naming the missing capability and the entry that met it"
	default:
		return "take it to the Lead Product Manager, who classifies what no other role owns"
	}
}

// CheckOperatorRouting refuses a role naming the operator for a reason that is
// not on the closed list, and names the rung that role uses instead. A reason
// on the list passes.
func CheckOperatorRouting(role domain.AgentRole, reason OperatorReason) error {
	if reason.Valid() {
		return nil
	}
	names := make([]string, 0, len(OperatorReasons()))
	for _, known := range OperatorReasons() {
		names = append(names, string(known))
	}
	given := "no reason"
	if strings.TrimSpace(string(reason)) != "" {
		given = fmt.Sprintf("reason %q, which is not on the list", reason)
	}
	return fmt.Errorf("the operator is named only for a reason on the closed list (%s), and this names him for %s; a decision a role could make is not his, so %s",
		strings.Join(names, ", "), given, Rung(role))
}

// OperatorReasonOf is the closed-list reason a validated person-only remedy
// stands for: each physical act is one of the reasons the operator is named
// for.
func OperatorReasonOf(remedy PersonOnlyRemedy) OperatorReason {
	if remedy.Validate() != nil {
		return ""
	}
	return OperatorReason(remedy.Reason)
}
