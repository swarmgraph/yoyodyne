package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// The developer slots block: which slot prefers which label, held to the
// capacity and to the tracker's own label rule.

func TestDeveloperSlotsAreReadInSlotOrderAndLeaveTheRestPreferringNothing(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig+`execution:
  max_concurrent_developers: 3
  developer_slots:
    - prefer: [dashboard]
    - {}
agents:
  developer:
    instances: 3
`, nil)
	want := []domain.DeveloperSlot{{Prefer: []string{"dashboard"}}, {}}
	if got := resolved.Config.Execution.DeveloperSlots; !reflect.DeepEqual(got, want) {
		t.Fatalf("developer_slots = %+v, want %+v", got, want)
	}
	if origin := resolved.Origins["execution.developer_slots"]; !strings.Contains(origin, "config.yaml") {
		t.Fatalf("developer_slots origin = %q, want the project file", origin)
	}
}

func TestAProjectThatNamesNoSlotsHasEverySlotPreferringNothing(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig, nil)
	if got := resolved.Config.Execution.DeveloperSlots; len(got) != 0 {
		t.Fatalf("developer_slots = %+v, want none", got)
	}
}

func TestMoreSlotsThanTheCapacityAreRefused(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, minimalProjectConfig+`execution:
  max_concurrent_developers: 1
  developer_slots:
    - prefer: [dashboard]
    - prefer: [reliability]
`, nil)
	if err == nil {
		t.Fatal("two slots over a capacity of one loaded")
	}
	if !strings.Contains(err.Error(), "execution.developer_slots names 2 slot(s) and max_concurrent_developers is 1") {
		t.Fatalf("refusal = %v, want it to name the list and the capacity", err)
	}
}

func TestASlotPreferringALabelTheTrackerWouldNotCarryIsRefused(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, minimalProjectConfig+`execution:
  developer_slots:
    - prefer: ["the dashboard work", dashboard, dashboard]
`, nil)
	if err == nil {
		t.Fatal("a slot preferring a sentence loaded")
	}
	for _, want := range []string{
		`execution.developer_slots slot 1: label "the dashboard work" is not an identifier`,
		`execution.developer_slots slot 1: label "dashboard" is named twice`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal = %v, want it to say %q", err, want)
		}
	}
}

func TestADedicatedSlotIsReadBesideWhatItPrefers(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig+`execution:
  max_concurrent_developers: 3
  developer_slots:
    - prefer: [reliability]
    - {}
    - prefer: [team-development]
      dedicated: true
agents:
  developer:
    instances: 3
`, nil)
	want := []domain.DeveloperSlot{{Prefer: []string{"reliability"}}, {}, {Prefer: []string{"team-development"}, Dedicated: true}}
	if got := resolved.Config.Execution.DeveloperSlots; !reflect.DeepEqual(got, want) {
		t.Fatalf("developer_slots = %+v, want %+v", got, want)
	}
}

func TestADedicatedSlotThatNamesNoLabelIsRefused(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, minimalProjectConfig+`execution:
  max_concurrent_developers: 2
  developer_slots:
    - {}
    - dedicated: true
agents:
  developer:
    instances: 2
`, nil)
	if err == nil {
		t.Fatal("a dedicated slot naming no label loaded")
	}
	if want := "execution.developer_slots slot 2: it is marked dedicated and names no label under prefer"; !strings.Contains(err.Error(), want) {
		t.Fatalf("refusal = %v, want it to say %q", err, want)
	}
}

func TestEverySlotDedicatedIsRefused(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, minimalProjectConfig+`execution:
  max_concurrent_developers: 2
  developer_slots:
    - prefer: [reliability]
      dedicated: true
    - prefer: [team-development]
      dedicated: true
agents:
  developer:
    instances: 2
`, nil)
	if err == nil {
		t.Fatal("every slot dedicated loaded")
	}
	if want := "execution.developer_slots marks every developer slot dedicated (slot 1, slot 2, with max_concurrent_developers 2)"; !strings.Contains(err.Error(), want) {
		t.Fatalf("refusal = %v, want it to say %q", err, want)
	}

	// One slot of two left undedicated, or named at all, is enough.
	loadProject(t, minimalProjectConfig+`execution:
  max_concurrent_developers: 2
  developer_slots:
    - prefer: [team-development]
      dedicated: true
agents:
  developer:
    instances: 2
`, nil)
}

func TestASlotListInALaterLayerReplacesTheInheritedOneWhole(t *testing.T) {
	t.Parallel()

	// The bundle names no slots, so a project's list is the whole list; what
	// this holds is that the resolved list is the project's own copy rather
	// than an alias of the document's.
	resolved := loadProject(t, minimalProjectConfig+`execution:
  developer_slots:
    - prefer: [dashboard]
`, nil)
	slots := resolved.Config.Execution.DeveloperSlots
	if len(slots) != 1 || !slots[0].Prefers([]string{"docs", "dashboard"}) || slots[0].Prefers([]string{"Dashboard"}) {
		t.Fatalf("developer_slots = %+v, want one slot preferring exactly dashboard", slots)
	}
}
