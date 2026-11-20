package intakerules

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

// testGoals holds one active goal, one stated only in a document that has
// ended, and one identifier two goals in force share.
func testGoals() goal.Set {
	return goal.Set{
		Goals: []goal.Goal{
			{Identity: "nearly-autonomous", Statement: "Run development nearly autonomously.", InForce: true},
			{Identity: "old-goal", Statement: "Something no longer intended.", InForce: false},
			{Identity: "twice", Statement: "One.", InForce: true},
			{Identity: "twice", Statement: "Two.", InForce: true},
		},
		IdentityProblems: []goal.IdentityProblem{{Identity: "twice", Stated: []string{"a.md", "b.md"}}},
	}
}

// goodRule is the design's example rule, which every case below changes one
// part of.
const goodRule = `- [slack-bug-report] A person reports something in the product that is broken.
  - workflow: delivery
  - goal: nearly-autonomous
  - priority: 1
  - type: bug
  - label: bug
  - title: Fix the problem reported in a message
  - done: The reported behaviour no longer happens, and a test shows it.
  - from: the direction this rule came from
`

const frontmatter = `---
id: intake-rules
kind: rules
title: Intake rules
status: active
revisions:
    - action: created
      by: product-manager
      at: 2026-11-19T00:00:00Z
      reason: test
---
`

func document(rules ...string) []byte {
	return []byte(frontmatter + "# Intake rules\n\nWhat may start work.\n\n## Rules\n\n" + strings.Join(rules, "\n") + "\n## Afterwards\n\n- [not-a-rule] prose after the section is not read as a rule\n")
}

func TestAWellFormedRuleIsReadWithEveryPart(t *testing.T) {
	t.Parallel()

	parsed := Parse(document(goodRule), testGoals())
	if parsed.Unreadable != "" || len(parsed.Refused) != 0 || len(parsed.Rules) != 1 {
		t.Fatalf("Parse() = %#v, want the one rule read", parsed)
	}
	want := Rule{
		ID:          "slack-bug-report",
		Description: "A person reports something in the product that is broken.",
		Workflow:    "delivery",
		Goal:        "nearly-autonomous",
		Priority:    1,
		Type:        domain.WorkItemKindBug,
		Label:       "bug",
		Title:       "Fix the problem reported in a message",
		Done:        "The reported behaviour no longer happens, and a test shows it.",
		From:        "the direction this rule came from",
		Line:        18,
	}
	if parsed.Rules[0] != want {
		t.Fatalf("rule = %#v\nwant %#v", parsed.Rules[0], want)
	}
	if _, ok := parsed.Rule("not-a-rule"); ok {
		t.Fatal("an entry after the Rules section was read as a rule")
	}
	if rule, ok := parsed.Rule("slack-bug-report"); !ok || rule.Title != want.Title {
		t.Fatalf("Rule(slack-bug-report) = %#v, %v", rule, ok)
	}
}

func TestADocumentWithoutFrontmatterIsTheProjectsOwnAndStillReads(t *testing.T) {
	t.Parallel()

	parsed := Parse([]byte("## Rules\n\n"+goodRule), testGoals())
	if parsed.Unreadable != "" || len(parsed.Rules) != 1 || parsed.Rules[0].Line != 3 {
		t.Fatalf("Parse() = %#v, want the rule read on line 3", parsed)
	}
}

// Each malformed or disallowed rule is refused with its reason, and the good
// rule beside it still applies.
func TestARefusedRuleLeavesTheOtherRulesApplying(t *testing.T) {
	t.Parallel()

	with := func(id, old, replacement string) string {
		rule := strings.Replace(goodRule, "[slack-bug-report]", "["+id+"]", 1)
		if !strings.Contains(rule, old) {
			t.Fatalf("the example rule has no %q", old)
		}
		return strings.Replace(rule, old, replacement, 1)
	}
	for _, test := range []struct {
		name string
		rule string
		want []string
	}{
		{"an inactive goal", with("ended", "goal: nearly-autonomous", "goal: old-goal"), []string{`goal "old-goal"`, "not in force", "not an active goal"}},
		{"a goal nobody states", with("unknown", "goal: nearly-autonomous", "goal: no-such-goal"), []string{`goal "no-such-goal" is not an active goal`}},
		{"a goal two goals carry", with("shared", "goal: nearly-autonomous", "goal: twice"), []string{`goal "twice" is carried by more than one goal in force`, "a.md, b.md"}},
		{"a goal written as words", with("worded", "goal: nearly-autonomous", "goal: Run development nearly autonomously"), []string{"is not a goal identifier"}},
		{"priority 0", with("urgent", "priority: 1", "priority: 0"), []string{"sets priority 0", "ahead of reliability work", "1 to 4"}},
		{"priority past 4", with("late", "priority: 1", "priority: 5"), []string{"priority 5 is not from 1 to 4"}},
		{"a priority that is not a number", with("high", "priority: 1", "priority: high"), []string{`priority "high" is not a whole number`}},
		{"another workflow", with("research", "workflow: delivery", "workflow: research"), []string{`workflow "research"`, `only "delivery"`}},
		{"no workflow", with("noflow", "  - workflow: delivery\n", ""), []string{"states no workflow"}},
		{"no goal and no done", with("bare", "  - goal: nearly-autonomous\n", ""), []string{"states no goal"}},
		{"an unknown type", with("chore", "type: bug", "type: chore"), []string{`type "chore" is not one of bug or feature`}},
		{"a part nobody has", with("extra", "  - from:", "  - owner: somebody\n  - from:"), []string{`part "owner"`}},
		{"a part twice", with("doubled", "  - label: bug\n", "  - label: bug\n  - label: other\n"), []string{"states its label twice"}},
		{"a part with no value", with("empty", "  - label: bug\n", "  - label:\n"), []string{"empty label"}},
		{"a part not written as name and value", with("unparted", "  - label: bug\n", "  - just words\n"), []string{"is not a part written as `- name: value`"}},
		{"a label with a comma", with("labels", "label: bug", "label: bug, urgent"), []string{"holds a space or a comma"}},
		{"no identifier", strings.Replace(goodRule, "[slack-bug-report] ", "", 1), []string{"does not open with a rule identifier in square brackets"}},
		{"no description", strings.Replace(goodRule, "[slack-bug-report] A person reports something in the product that is broken.", "[silent]", 1), []string{"says nothing after its identifier"}},
		{"an indented line among the parts", with("loose", "  - from:", "  stray words\n  - from:"), []string{"is not a part written as `- name: value`"}},
	} {
		parsed := Parse(document(goodRule, test.rule), testGoals())
		if parsed.Unreadable != "" {
			t.Errorf("%s: Unreadable = %q, want the document still read", test.name, parsed.Unreadable)
			continue
		}
		if len(parsed.Rules) != 1 || parsed.Rules[0].ID != "slack-bug-report" {
			t.Errorf("%s: rules = %#v, want the good rule still applying", test.name, parsed.Rules)
		}
		if len(parsed.Refused) != 1 {
			t.Errorf("%s: refused = %#v, want one refusal", test.name, parsed.Refused)
			continue
		}
		for _, want := range test.want {
			if !strings.Contains(parsed.Refused[0].Reason, want) {
				t.Errorf("%s: refusal = %q, want it to say %q", test.name, parsed.Refused[0].Reason, want)
			}
		}
		if parsed.Refused[0].Line <= parsed.Rules[0].Line {
			t.Errorf("%s: refusal names line %d, want the second rule's line", test.name, parsed.Refused[0].Line)
		}
	}
}

func TestTwoRulesSharingAnIdentifierAreBothRefused(t *testing.T) {
	t.Parallel()

	other := strings.Replace(goodRule, "[slack-bug-report]", "[other]", 1)
	parsed := Parse(document(goodRule, goodRule, other), testGoals())
	if len(parsed.Rules) != 1 || parsed.Rules[0].ID != "other" {
		t.Fatalf("rules = %#v, want only the rule with its own identifier", parsed.Rules)
	}
	if len(parsed.Refused) != 2 || !strings.Contains(parsed.Refused[0].Reason, "2 rules in this document open with [slack-bug-report]") {
		t.Fatalf("refused = %#v, want both sharing rules refused", parsed.Refused)
	}
}

// A document that cannot be read as rules holds no rules at all, which is what
// stops the query's readers.
func TestADocumentThatCannotBeReadAsRulesHoldsNone(t *testing.T) {
	t.Parallel()

	ended := strings.Replace(frontmatter, "status: active", "status: retired", 1)
	for _, test := range []struct {
		name    string
		content []byte
		want    string
	}{
		{"no Rules heading", []byte(frontmatter + "# Rules of thumb\n\n" + goodRule), "has no `## Rules` heading"},
		{"an empty Rules section", []byte(frontmatter + "## Rules\n\nNone yet.\n"), "states no rules"},
		{"every rule refused", document(strings.Replace(goodRule, "priority: 1", "priority: 0", 1)), "none of its 1 rules can be used"},
		{"another kind of document", []byte(strings.Replace(frontmatter, "kind: rules", "kind: design", 1) + "## Rules\n\n" + goodRule), `kind "design"`},
		{"a document no longer in force", []byte(ended + "## Rules\n\n" + goodRule), `status is "retired"`},
		{"frontmatter never closed", []byte("---\nid: x\n## Rules\n\n" + goodRule), "frontmatter"},
		{"not text", []byte{0xff, 0xfe, 0x00}, "not UTF-8"},
		{"too large", append(document(goodRule), []byte(strings.Repeat("x", MaxDocumentBytes))...), "read only up to"},
	} {
		parsed := Parse(test.content, testGoals())
		if !strings.Contains(parsed.Unreadable, test.want) {
			t.Errorf("%s: Unreadable = %q, want it to say %q", test.name, parsed.Unreadable, test.want)
		}
		if len(parsed.Rules) != 0 {
			t.Errorf("%s: rules = %#v, want none from a document that cannot be read", test.name, parsed.Rules)
		}
	}
}

func TestARuleInsideACodeFenceIsNotARule(t *testing.T) {
	t.Parallel()

	fenced := "```markdown\n" + strings.Replace(goodRule, "[slack-bug-report]", "[example]", 1) + "```\n"
	parsed := Parse(document(fenced, goodRule), testGoals())
	if len(parsed.Rules) != 1 || parsed.Rules[0].ID != "slack-bug-report" || len(parsed.Refused) != 0 {
		t.Fatalf("Parse() = %#v, want only the rule outside the fence", parsed)
	}
}
