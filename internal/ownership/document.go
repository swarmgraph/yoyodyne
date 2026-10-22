package ownership

import "github.com/mason-bryant/yoyodyne/internal/domain"

// ResolvePendingDocument is who decides a document an owning role wrote in its
// conversation that is waiting for approval before it is written. A document
// the Lead Product Manager owns is filed in the product home — the brief, the
// goals, the non-goals, the operating rules — and says what the product is
// for, so approving or declining one is a change of fundamental intent: the
// operator's, recorded with the harness's own approve verb rather than by
// anybody's hand. For any other document this names no owner, and the second
// result is false: who approves a design or a decision record is not the
// operator's under the rule of 2026-09-26, and nothing here routes one to him.
func ResolvePendingDocument(owner domain.AgentRole) (Mover, bool) {
	if owner == domain.RoleProductManager {
		return MoverOperator, true
	}
	return "", false
}
