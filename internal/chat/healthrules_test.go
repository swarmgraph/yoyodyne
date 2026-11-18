package chat

import (
	"os"
	"strings"
	"testing"
)

func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

var developmentManagerHealthRules = []string{
	"Trace the obstruction. When work stops, name the exact step that failed, the evidence for it from a fresh observation, and the supported next action. Saying the problem is covered by an existing item is not a recovery decision: say what that item does for this run, and when.",
	"Look for repetition. Across passes, count repeated failures of the same kind, P0 work that waited while developer slots were free, and checks repeated on the same revision. Raise a pattern that persists as a pattern, even when each event looks handled.",
	"Check the real path. When a service or a role fails, look at the environment that service or role actually runs with, as health.observe reports it, not at one set up by hand to make it work.",
	"Prefer the smallest supported action that restores progress and keeps the work and its checks. Continue the same run before starting a new one, and never cancel preserved work to free a slot.",
}

var architectHealthRules = []string{
	"When a capability needs a design that cannot be published without that capability, say so and settle that first: name what can ship before it, and in what order.",
	"Separate what this design needs from a wider redesign. Say what can ship under existing rules and what needs a new ruling, and rule only on the second.",
	"Design for finishing. Say who owns the work while it runs, what happens on a restart, what anyone can observe once it is done, and how records already broken are mended.",
	"A missing tool or a limit of the current build is an engineering problem to design, not a reason to hand work to a person. Real safety boundaries stay; say which kind each constraint is.",
	"Count the cost to throughput: checks repeated, checks restarted by deploys, and work held in the scheduler, not only whether one part is correct on its own.",
}

func TestEveryRoleContractCarriesTheSharedDependencyAndProgressRule(t *testing.T) {
	t.Parallel()
	for _, role := range ConversationalRoles() {
		authority, _ := AuthorityFor(role)
		if !strings.Contains(squash(SystemPrompt(role, Admission{}, nil, "")), squash(sharedDependencyAndProgressRule)) {
			t.Errorf("%s contract lacks the shared rule: %q", role, authority.Contract)
		}
	}
}

func TestHealthRulesAreInLivePersonasTemplatesAndBuiltPrompts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		role  string
		rules []string
	}{
		{"development-manager", developmentManagerHealthRules},
		{"architect", architectHealthRules},
	}
	for _, c := range cases {
		for _, dir := range []string{"../../.yoyodyne/personas/", "../config/builtin/v1/personas/"} {
			raw, err := os.ReadFile(dir + c.role + ".md")
			if err != nil {
				t.Fatal(err)
			}
			persona := string(raw)
			for _, role := range ConversationalRoles() {
				if string(role) != c.role {
					continue
				}
				prompt := squash(SystemPrompt(role, Admission{}, nil, persona))
				for _, rule := range c.rules {
					if !strings.Contains(squash(persona), rule) {
						t.Errorf("%s%s lacks rule %q", dir, c.role, rule)
					}
					if !strings.Contains(prompt, rule) {
						t.Errorf("built %s prompt lacks rule %q", c.role, rule)
					}
				}
			}
		}
	}
}
