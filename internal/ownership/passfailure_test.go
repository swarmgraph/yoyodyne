package ownership

import (
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"testing"
)

func TestPassFailureOwnershipRequiresAPermittedPersonOnlyRemedy(t *testing.T) {
	for _, agent := range []string{"factory-flow-pm", ""} {
		for _, tc := range []struct {
			remedy   *PersonOnlyRemedy
			operator bool
		}{
			{},
			{remedy: &PersonOnlyRemedy{Step: "repair the harness and rerun its tests"}},
			{remedy: &PersonOnlyRemedy{Reason: "role-owned", Target: "maintenance", Step: "repair the harness"}},
			{remedy: &PersonOnlyRemedy{Reason: PersonProtectedFile, Target: "internal/maintain/maintain.go", Step: "fix the maintenance pass"}},
			{remedy: &PersonOnlyRemedy{Reason: PersonCredential, Target: "provider login", Step: "renew the provider credential by hand"}, operator: true},
			{remedy: &PersonOnlyRemedy{Reason: PersonRepositorySetting, Target: "forge branch protection", Step: "enable the required check in branch protection"}, operator: true},
			{remedy: &PersonOnlyRemedy{Reason: PersonProtectedFile, Target: ".claude/settings.json", Step: "put the notes-writer hook in .claude/settings.json by hand"}, operator: true},
		} {
			remedy := tc.remedy
			answer := ResolvePassFailure(agent, remedy)
			if (answer.Mover == MoverOperator) != tc.operator || answer.Fallback != (agent == "") {
				t.Fatalf("ownership = %+v", answer)
			}
			if tc.operator && (answer.PersonReason != remedy.Reason || answer.PersonTarget != remedy.Target || answer.PersonStep != remedy.Step) {
				t.Fatalf("the exact person-only remedy was lost: %+v", answer)
			}
			if !tc.operator && (answer.PersonStep != "" || answer.PersonReason != "" || answer.PersonTarget != "") {
				t.Fatalf("ordinary repair was assigned to a person: %+v", answer)
			}
		}
	}
}

func TestFailedRoleOwnershipKeepsPersonOnlyRemediesAndOtherRolesUnchanged(t *testing.T) {
	remedy := &PersonOnlyRemedy{Reason: PersonCredential, Target: "provider login", Step: "renew the credential"}
	for _, role := range []domain.AgentRole{domain.RoleArchitect, domain.RoleDevelopmentManager, domain.RoleProgramManager} {
		for _, watcher := range []string{"factory-watch", ""} {
			answer := ResolvePassFailureForRole(watcher, role, "factory-watch", remedy)
			if answer.Mover != MoverOperator || answer.PersonStep != remedy.Step {
				t.Fatalf("person-only remedy lost: %+v", answer)
			}
			if role == domain.RoleArchitect && ResolvePassFailureForRole(watcher, role, "", nil) != ResolvePassFailure(watcher, nil) {
				t.Fatal("another role's routing changed")
			}
		}
	}
}

func TestAnotherProgramManagersPassKeepsItsExistingOwner(t *testing.T) {
	if got, want := ResolvePassFailureForRole("factory-watch", domain.RoleProgramManager, "other-manager", nil), ResolvePassFailure("factory-watch", nil); got != want {
		t.Fatalf("ownership = %+v, want %+v", got, want)
	}
}
