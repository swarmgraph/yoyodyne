package toolcatalog

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/rolecapability"
)

func TestEveryCapabilityIsAToolOrExplicitlyExcluded(t *testing.T) {
	t.Parallel()
	registry, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range capability.All() {
		entry, tool := registry.Lookup(string(id))
		reason, excluded := Excluded[id]
		if tool == excluded {
			t.Errorf("%s must be classified exactly once", id)
		}
		if tool && entry.Tool == nil {
			t.Errorf("%s has no descriptor", id)
		}
		if excluded && strings.TrimSpace(reason) == "" {
			t.Errorf("%s is excluded without a reason", id)
		}
	}
	for id := range Excluded {
		if !id.Known() {
			t.Errorf("unknown excluded capability %s", id)
		}
	}
}

func TestRoleToolsAreExactlyTheGrantedDescriptors(t *testing.T) {
	t.Parallel()
	grants := rolecapability.MustDefault()
	for _, role := range domain.Roles() {
		contract := Contract(role)
		for _, entry := range Registry().Actions() {
			held := true
			for _, required := range entry.Capabilities {
				held = held && grants.Holds(role, required)
			}
			heading := "## " + entry.Name + " ("
			count := strings.Count(contract, heading)
			if held && count != 1 || !held && count != 0 {
				t.Errorf("%s tool %s: count %d held %t", role, entry.Name, count, held)
			}
		}
	}
	if !strings.Contains(Contract(domain.RoleProgramManager), "## log.read (read)") || strings.Contains(Contract(domain.RoleDeveloper), "## log.read (") {
		t.Fatal("log.read grants drifted")
	}
}
