package ownership

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// A document in the product home waiting for approval is the operator's,
// because it says what the product is for; a document any other role owns is
// routed to nobody here.
func TestAPendingDocumentIsTheOperatorsOnlyWhereItStatesIntent(t *testing.T) {
	t.Parallel()
	if mover, named := ResolvePendingDocument(domain.RoleProductManager); !named || mover != MoverOperator {
		t.Fatalf("a product-home document = %q, %v; want the operator", mover, named)
	}
	for _, role := range []domain.AgentRole{domain.RoleArchitect, domain.RoleDevelopmentManager, domain.RoleProgramManager, ""} {
		if mover, named := ResolvePendingDocument(role); named || mover != "" {
			t.Fatalf("a document the %q owns = %q, %v; want nobody named", role, mover, named)
		}
	}
}
