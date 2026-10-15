package ownership

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func TestAnAmendmentIsItsOwnersUnlessItChangesFundamentalIntent(t *testing.T) {
	t.Parallel()

	if got := ResolveAmendment(domain.RoleArchitect, false); got != MoverArchitect {
		t.Fatalf("ResolveAmendment(architect) = %q, want the architect", got)
	}
	if got := ResolveAmendment(domain.RoleProductManager, false); got != MoverProductManager {
		t.Fatalf("ResolveAmendment(product manager) = %q, want the Lead Product Manager", got)
	}
	if got := ResolveAmendment(domain.RoleProductManager, true); got != MoverOperator {
		t.Fatalf("ResolveAmendment(fundamental intent) = %q, want the operator", got)
	}
}
