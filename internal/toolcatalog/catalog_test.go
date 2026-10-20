package toolcatalog

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/rolecapability"
	"github.com/mason-bryant/yoyodyne/internal/terms"
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
	if !strings.Contains(Contract(domain.RoleProgramManager), "## log.read (reads") || strings.Contains(Contract(domain.RoleDeveloper), "## log.read (") {
		t.Fatal("log.read grants drifted")
	}
}

// A role copies its instructions' wording into what people read, so the tools
// section says what each tool's limits and checks mean in ordinary words: no
// word the register of terms does not permit, and none of the harness's own
// names for the descriptor's fields that the section used to print.
func TestTheToolsSectionUsesOrdinaryWords(t *testing.T) {
	t.Parallel()
	checker, err := terms.ReadTextChecker("../..")
	if err != nil {
		t.Fatal(err)
	}
	replaced := []string{
		"descriptor", "bypasses", "Gates:", "Scope:", "redacted", "evidence", "typed",
		"trusted harness actions", "remit", "handler", "audited", "canonical", "slice",
		"dispatch", "request(s)", "round(s)", "prompt space", "prompt bytes", "provider turns",
		"(read)", "(speak)", "(act)", "exchange.answer", "Git tree", "state root",
	}
	for _, role := range domain.Roles() {
		for name, section := range map[string]string{"conversation": Contract(role), "run": RunContract(role)} {
			for _, finding := range checker.Find(section) {
				t.Errorf("%s %s tools section: %s", role, name, finding.Warning())
			}
			lower := strings.ToLower(section)
			for _, word := range replaced {
				if strings.Contains(lower, strings.ToLower(word)) {
					t.Errorf("%s %s tools section still says %q:\n%s", role, name, word, section)
				}
			}
		}
	}
	// The limits are said as a sentence, and a tool that returns nothing says so.
	run := RunContract(domain.RoleDeveloper)
	for _, want := range []string{
		"Limits: at most 5 requests in one reply, and at most 4 rounds of requests and results for one message. It returns nothing for you to read.",
		"What the harness still checks: the same things it checks without the tool",
	} {
		if !strings.Contains(run, want) {
			t.Errorf("developer run tools section does not say %q:\n%s", want, run)
		}
	}
}

// A capability set narrower than a role's bundle is granted exactly the tools
// whose every required capability it holds, and its whole bundle is granted
// exactly the role's tools.
func TestHeldToolsAreTheToolsTheSetHolds(t *testing.T) {
	t.Parallel()
	bundle, _ := rolecapability.MustDefault().Bundle(domain.RoleArchitect)
	if strings.Join(NamesHeld(bundle.Holds), ",") != strings.Join(Names(domain.RoleArchitect), ",") ||
		ContractHeld(bundle.Holds) != Contract(domain.RoleArchitect) {
		t.Fatal("the architect's whole bundle is not granted exactly the architect's tools")
	}
	var narrowed []capability.Capability
	for _, held := range bundle.Holds {
		if held != capability.ExchangeAsk {
			narrowed = append(narrowed, held)
		}
	}
	names := NamesHeld(narrowed)
	if len(names) != len(Names(domain.RoleArchitect))-1 || strings.Contains(strings.Join(names, ","), string(capability.ExchangeAsk)) {
		t.Fatalf("NamesHeld(without the ask) = %v", names)
	}
	if strings.Contains(ContractHeld(narrowed), "## "+string(capability.ExchangeAsk)+" ") {
		t.Fatal("the narrowed contract still describes the ask")
	}
}
