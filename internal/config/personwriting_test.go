package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Every shipped persona and the copy this repository's roles read carry the
// same writing section, word for word, so the template and the live copy cannot
// drift apart on it; the pass prompts init writes and the factory-flow remit
// carry the rule too.
func TestPersonasPassPromptsAndRemitWriteForAPerson(t *testing.T) {
	t.Parallel()

	scaffold, err := NewScaffold(BuiltinV1, ScaffoldOptions{ProductID: "example", Repository: "."})
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range domain.Roles() {
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
			shippedSection := writingSection(t, "the shipped persona", shipped)
			liveSection := writingSection(t, "the live copy", string(live))
			assertPersonWriting(t, shippedSection, true)
			if shippedSection != liveSection {
				t.Errorf("the shipped and live writing sections differ:\nshipped: %s\nlive:    %s", shippedSection, liveSection)
			}
		})
	}
	resolved := loadScaffoldEdited(t, ScaffoldOptions{ProductID: "example", Repository: "."}, func(content string) string {
		return uncommentScaffoldBlock(t, content, "recurring_tasks:")
	})
	if len(resolved.Config.RecurringTasks) != 4 {
		t.Fatalf("got %d pass prompts, want four", len(resolved.Config.RecurringTasks))
	}
	// The pass prompts carry the model alone: the prompt is a string the
	// terms check reads, and it refuses the retired words the bad example
	// quotes.
	for name, task := range resolved.Config.RecurringTasks {
		t.Run(name, func(t *testing.T) {
			assertPersonWriting(t, task.Prompt, false)
		})
	}
	remit, err := os.ReadFile(filepath.Join("..", "..", ".yoyodyne", "remits", "factory-flow.md"))
	if err != nil {
		t.Fatal(err)
	}
	assertPersonWriting(t, string(remit), true)
}

// writingSection is a persona's "Writing for a person" section with its line
// breaks folded, so a reflow is not a difference and a rewording is.
func writingSection(t *testing.T, which, text string) string {
	t.Helper()
	const heading = "## Writing for a person"
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

func assertPersonWriting(t *testing.T, text string, quotesTheBadExample bool) {
	t.Helper()
	flat := strings.Join(strings.Fields(text), " ")
	wants := []string{
		"in ordinary words, and say what happened, not the harness's category",
		`"the AI session running the developer produced no output for five minutes, so the harness ended the run; the cause was outside the work, so no repair attempt was spent and the change was kept."`,
		"Coin no terms, and do not pass on the words the harness uses for itself",
		"local time with the zone named, such as 08:20 PDT, not UTC",
	}
	if quotesTheBadExample {
		wants = append(wants, `Not "stopped by the harness's idle bound when the provider's stream went silent, settled as an environmental stop"`)
	}
	for _, want := range wants {
		if !strings.Contains(flat, want) {
			t.Errorf("the rule for writing for a person is missing %q", want)
		}
	}
}
