package ownership_test

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/protectedpath"
)

func TestPersonOnlyFilesAreBeyondAnyProtectedPathGrant(t *testing.T) {
	for _, target := range []string{".claude/settings.json", ".claude/settings.local.json", ".yoyodyne/roles/developer.yaml"} {
		remedy := ownership.PersonOnlyRemedy{Reason: ownership.PersonProtectedFile, Target: target, Step: "change the named file by hand"}
		if err := remedy.Validate(); err != nil {
			t.Fatal(err)
		}
		if len(protectedpath.BeyondGrant([]string{target})) == 0 && len(protectedpath.RoleDefinitionsAmong([]string{target})) == 0 {
			t.Fatalf("%s was named as person-only but the protected-path gate permits a grant", target)
		}
	}
}

func TestPersonOnlyReasonsAndStepsAreClosedAndRequired(t *testing.T) {
	for _, remedy := range []ownership.PersonOnlyRemedy{
		{Target: "maintenance", Step: "repair the harness"},
		{Reason: "repair", Target: "maintenance", Step: "repair the harness"},
		{Reason: ownership.PersonCredential, Step: "log in"},
		{Reason: ownership.PersonCredential, Target: "provider login"},
		{Reason: ownership.PersonProtectedFile, Target: "internal/maintain/maintain.go", Step: "fix the pass"},
		{Reason: ownership.PersonProtectedFile, Target: ".CLAUDE/SETTINGS.json", Step: "fix the pass"},
		{Reason: ownership.PersonProtectedFile, Target: ".yoyodyne/roles/../../internal/maintain.go", Step: "fix the pass"},
	} {
		if err := remedy.Validate(); err == nil {
			t.Fatalf("unpermitted remedy accepted: %+v", remedy)
		}
	}
}
