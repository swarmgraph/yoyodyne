package chat

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
)

// Every role is told how to name the operator: the reasons on his closed list,
// each as the field takes it, and the field that carries it. The words are one
// constant the ownership package holds to the resolver's own list
// (TestTheContractsListIsTheResolversList), so what is held here is that each
// contract embeds it rather than a list of its own that could drift. The
// developer and reviewer run contracts are held to it in their own packages.
func TestEveryContractNamesTheReasonsTheResolverAccepts(t *testing.T) {
	t.Parallel()

	for _, role := range ConversationalRoles() {
		authority, _ := AuthorityFor(role)
		if !strings.Contains(authority.Contract, ownership.OperatorReasonsInContract) {
			t.Errorf("%s contract does not name the operator's closed list of reasons", role)
		}
		// The design's earlier names for two reasons are not ones the resolver
		// takes, so a contract teaching them teaches a refusal.
		for _, retired := range []string{"forge-setting", "beyond-grant"} {
			if strings.Contains(authority.Contract, retired) {
				t.Errorf("%s contract names %q, which the resolver does not accept", role, retired)
			}
		}
	}

	// Each role that names the operator in a block is told the field and the
	// list together, where it is told the block.
	manager, _ := AuthorityFor(domain.RoleDevelopmentManager)
	if !strings.Contains(manager.Contract, `"operator_reason": `+ownership.OperatorReasonsInContract) {
		t.Error(`the development manager's escalation is not told "operator_reason" with the closed list`)
	}
	if !strings.Contains(manager.Contract, `"to":"escalate only: product-manager, architect, or operator","operator_reason":`) {
		t.Error("the development manager's tracker example shows an escalation with no target or reason")
	}
	product, _ := AuthorityFor(domain.RoleProductManager)
	if !strings.Contains(product.Contract, `"operator_reason" naming which reason on his closed list it is — `+ownership.OperatorReasonsInContract) {
		t.Error(`the Lead Product Manager's report handling is not told "operator_reason" with the closed list`)
	}
	if !strings.Contains(exchange.AskingContract, `"operator_reason": `+ownership.OperatorReasonsInContract) {
		t.Error(`an ask naming the operator is not told "operator_reason" with the closed list`)
	}
	for _, reason := range ownership.OperatorReasons() {
		if !strings.Contains(laneReportContract, `"`+string(reason)+`"`) {
			t.Errorf("the lane report contract does not name %q for a Blocked on a human entry", reason)
		}
	}
}
