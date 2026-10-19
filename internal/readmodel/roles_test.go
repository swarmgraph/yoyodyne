package readmodel

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestRoleDefinitionsUseTheLatestActivationAndSortByName(t *testing.T) {
	definitions := map[string]config.RoleDefinition{
		"zebra": {Name: "zebra", Digest: "old"},
		"alpha": {Name: "alpha", Digest: "current"},
		"never": {Name: "never", Digest: "unactivated"},
	}
	history := []runstate.RoleActivation{
		{Name: "zebra", Digest: "new"},
		{Name: "alpha", Digest: "current"},
		{Name: "zebra", Digest: "old"},
	}
	roles := RoleDefinitions(definitions, history)
	if len(roles) != 3 || roles[0].Definition.Name != "alpha" || roles[1].Definition.Name != "never" || roles[2].Definition.Name != "zebra" {
		t.Fatalf("definition order = %#v", roles)
	}
	if !roles[0].Activated || roles[0].AmendedSince || roles[1].Activated || roles[1].AmendedSince || roles[1].Activation != nil {
		t.Fatalf("current and unactivated definitions = %#v", roles)
	}
	if roles[2].Activated || !roles[2].AmendedSince || roles[2].Activation.Digest != "new" {
		t.Fatalf("an older matching digest authorized the current file: %#v", roles[2])
	}
}

// A definition supplies authority only while the person's latest activation
// names the file's exact content where it now stands.
func TestActivationCheckRefusesEveryDefinitionNotActivatedAsItStands(t *testing.T) {
	history := []runstate.RoleActivation{
		{Name: "current", Digest: "d1", Source: "/p/.yoyodyne/roles/current.yaml", Person: "Ada"},
		{Name: "amended", Digest: "old", Source: "/p/.yoyodyne/roles/amended.yaml", Person: "Ada"},
		{Name: "moved", Digest: "d3", Source: "/p/.yoyodyne/roles/moved.yaml", Person: "Ada"},
	}
	check := ActivationCheck(history)
	if err := check(config.RoleDefinition{Name: "current", Digest: "d1", Source: "/p/.yoyodyne/roles/current.yaml"}); err != nil {
		t.Fatalf("activated definition refused: %v", err)
	}
	for _, testCase := range []struct {
		definition config.RoleDefinition
		want       string
	}{
		{config.RoleDefinition{Name: "amended", Digest: "d2", Source: "/p/.yoyodyne/roles/amended.yaml"}, "has changed since Ada activated digest old"},
		{config.RoleDefinition{Name: "moved", Digest: "d3", Source: "/p/roles/moved.yaml"}, "now sits at /p/roles/moved.yaml"},
		{config.RoleDefinition{Name: "never", Digest: "d4", Source: "/p/.yoyodyne/roles/never.yaml"}, "nobody has activated it"},
	} {
		err := check(testCase.definition)
		if err == nil || !strings.Contains(err.Error(), testCase.want) || !strings.Contains(err.Error(), "yoyo role activate "+testCase.definition.Name) {
			t.Errorf("check(%s) = %v, want %q and the activate command", testCase.definition.Name, err, testCase.want)
		}
	}
}
