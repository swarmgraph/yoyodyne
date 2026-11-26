package cli

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// Only the operator's own command sets or raises an epic's limit. A process the
// harness launched for any role, or started itself, is refused it and writes
// nothing; the record it is kept in is written by the operator's acts alone
// (epicscope's TestOnlyAnOperatorsCommandWritesTheRecord).
func TestOnlyTheOperatorsCommandSetsAnEpicsBudget(t *testing.T) {
	bench := newEpicBench(t)
	bench.does("start", "yoyodyne-a")
	for _, role := range []domain.AgentRole{domain.RoleProductManager, domain.RoleDevelopmentManager, domain.RoleArchitect, domain.RoleProgramManager, domain.RoleDeveloper, domain.RoleReviewer} {
		t.Setenv(execution.AgentRoleVariable, string(role))
		bench.refuses([]string{"budget", "yoyodyne-a", "25"}, "refused from a process the harness")
	}
	t.Setenv(execution.AgentRoleVariable, "")
	t.Setenv(execution.StartedByVariable, "the scheduler")
	bench.refuses([]string{"budget", "yoyodyne-a", "25"}, "refused from a process the harness")
	t.Setenv(execution.StartedByVariable, "")
	if epic, _ := bench.record().Named("yoyodyne-a"); epic.Budget != nil {
		t.Fatalf("a refused act set a budget: %+v", epic.Budget)
	}

	says(t, bench.does("budget", "yoyodyne-a", "25"), "may spend $25.00 in provider reported cost", "what is under way always finishes")
	if epic, _ := bench.record().Named("yoyodyne-a"); epic.Budget == nil || epic.Budget.LimitUSD != 25 {
		t.Fatalf("the budget is %+v, want $25", epic.Budget)
	}
	bench.does("budget", "yoyodyne-a", "$40.50")
	if epic, _ := bench.record().Named("yoyodyne-a"); epic.Budget.LimitUSD != 40.5 {
		t.Fatalf("the raised budget is %+v, want $40.50", epic.Budget)
	}
	says(t, bench.does("list"), "budget: $40.50 in provider reported cost")

	bench.refuses([]string{"budget", "yoyodyne-a", "lots"}, "not an amount of US dollars")
	bench.refuses([]string{"budget", "yoyodyne-a", "0"}, "above 0")
	bench.refuses([]string{"budget", "yoyodyne-b", "5"}, "never named")
	bench.does("end", "yoyodyne-a")
	bench.refuses([]string{"budget", "yoyodyne-a", "5"}, "already ended")
}
