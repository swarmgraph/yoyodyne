package ownership

import (
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Naming the operator passes only for a reason on the closed list, and a
// refusal names the rung the role uses instead, whichever role it is.
func TestNamingTheOperatorNeedsAClosedListReasonAndARefusalNamesTheRung(t *testing.T) {
	for _, role := range append(domain.Roles(), "") {
		for _, reason := range OperatorReasons() {
			if err := CheckOperatorRouting(role, reason); err != nil {
				t.Fatalf("CheckOperatorRouting(%q, %q) = %v, want it to pass", role, reason, err)
			}
		}
		for _, reason := range []OperatorReason{"", "approval", "decision", "exchange-closed"} {
			err := CheckOperatorRouting(role, reason)
			if err == nil {
				t.Fatalf("CheckOperatorRouting(%q, %q) passed, want it refused", role, reason)
			}
			if !strings.Contains(err.Error(), Rung(role)) {
				t.Fatalf("the refusal for %q does not name its rung: %v", role, err)
			}
		}
	}
}

// The ladder as the design states it: the developer and the reviewer go to the
// development manager, the development manager to the Lead Product Manager and
// the architect, the program manager and the architect to the Lead Product
// Manager, who is the top rung. Nobody's rung is the operator.
func TestTheLadderGoesUpThroughTheRolesAndNeverToTheOperator(t *testing.T) {
	for role, want := range map[domain.AgentRole][]domain.AgentRole{
		domain.RoleDeveloper:          {domain.RoleDevelopmentManager},
		domain.RoleReviewer:           {domain.RoleDevelopmentManager},
		domain.RoleDevelopmentManager: {domain.RoleProductManager, domain.RoleArchitect},
		domain.RoleProgramManager:     {domain.RoleProductManager},
		domain.RoleArchitect:          {domain.RoleProductManager},
		domain.RoleProductManager:     nil,
	} {
		if got := EscalatesTo(role); !slices.Equal(got, want) {
			t.Fatalf("EscalatesTo(%q) = %v, want %v", role, got, want)
		}
		if strings.Contains(Rung(role), "operator") {
			t.Fatalf("the rung for %q names the operator: %q", role, Rung(role))
		}
	}
	if !NamesOperator(" operator ") || NamesOperator("architect") || NamesOperator("") {
		t.Fatal("NamesOperator reads the operator's name wrongly")
	}
}

// A valid person-only remedy stands for the reason of the same name, and an
// invalid one stands for none.
func TestAPersonOnlyRemedyStandsForItsClosedListReason(t *testing.T) {
	valid := PersonOnlyRemedy{Reason: PersonCredential, Target: "the forge login", Step: "log in again"}
	if got := OperatorReasonOf(valid); got != OperatorCredential {
		t.Fatalf("OperatorReasonOf(%+v) = %q", valid, got)
	}
	if got := OperatorReasonOf(PersonOnlyRemedy{Reason: "repair", Target: "x", Step: "y"}); got != "" {
		t.Fatalf("an invalid remedy stood for %q", got)
	}
}
