package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Every role that puts questions to the operator asks him one decision at a
// time, says what the answer unblocks, and offers to discuss it before he
// decides. Both the persona "yoyo init" ships and the copy this repository's
// roles read carry the same section.
func TestPersonasThatAskTheOperatorAskOneDecisionAndOfferDiscussion(t *testing.T) {
	t.Parallel()

	scaffold, err := NewScaffold(BuiltinV1, ScaffoldOptions{ProductID: "example", Repository: "."})
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []domain.AgentRole{
		domain.RoleProductManager,
		domain.RoleArchitect,
		domain.RoleDevelopmentManager,
		domain.RoleProgramManager,
	} {
		t.Run(string(role), func(t *testing.T) {
			path := "personas/" + string(role) + ".md"
			var shipped string
			for _, file := range scaffold.Personas {
				if file.Path == path {
					shipped = string(file.Content)
				}
			}
			live, err := os.ReadFile(filepath.Join("..", "..", ".yoyodyne", path))
			if err != nil {
				t.Fatal(err)
			}
			shippedSection := askingOperatorSection(t, "the shipped persona", shipped)
			liveSection := askingOperatorSection(t, "the live copy", string(live))
			for _, want := range []string{
				"put one decision to him per question",
				"say what his answer unblocks",
				"Always offer to discuss it, or to answer his follow-up questions, before he decides",
				"Never bundle approvals into one question",
				"never offer an answer that decides more than one thing",
			} {
				if !strings.Contains(shippedSection, want) {
					t.Errorf("the shipped persona's question section is missing %q", want)
				}
			}
			if shippedSection != liveSection {
				t.Errorf("the shipped and live question sections differ:\nshipped: %s\nlive:    %s", shippedSection, liveSection)
			}
		})
	}
}

// askingOperatorSection is a persona's "Putting a question to the operator"
// section with its line breaks folded, so a reflow is not a difference and a
// rewording is.
func askingOperatorSection(t *testing.T, which, text string) string {
	t.Helper()
	const heading = "## Putting a question to the operator"
	start := strings.Index(text, heading)
	if start < 0 {
		t.Fatalf("%s has no %q section", which, heading)
		return ""
	}
	section := text[start+len(heading):]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	return strings.Join(strings.Fields(section), " ")
}
