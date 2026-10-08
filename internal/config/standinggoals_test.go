package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Check what init actually writes, including the optional pass prompts after
// uncommenting them, and the copies this repository's roles read.
func TestPersonasAndPassPromptsApplyStandingGoals(t *testing.T) {
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
			assertStandingGoals(t, shipped)
			live, err := os.ReadFile(filepath.Join("..", "..", ".yoyodyne", path))
			if err != nil {
				t.Fatal(err)
			}
			assertStandingGoals(t, string(live))
			if role == domain.RoleProgramManager {
				for _, text := range []string{shipped, string(live)} {
					if !strings.Contains(text, "your lane report, digest, pass summary, and post-mortems") {
						t.Error("the program manager does not apply the standing set to its lane report and post-mortems")
					}
				}
			}
		})
	}
	resolved := loadScaffoldEdited(t, ScaffoldOptions{ProductID: "example", Repository: "."}, func(content string) string {
		return uncommentScaffoldBlock(t, content, "recurring_tasks:")
	})
	if len(resolved.Config.RecurringTasks) != 5 {
		t.Fatalf("got %d pass prompts, want five", len(resolved.Config.RecurringTasks))
	}
	for name, task := range resolved.Config.RecurringTasks {
		t.Run(name, func(t *testing.T) {
			assertStandingGoals(t, task.Prompt)
		})
	}
}

func assertStandingGoals(t *testing.T, text string) {
	t.Helper()
	flat := strings.Join(strings.Fields(strings.ReplaceAll(text, "`", "")), " ")
	for _, want := range []string{
		"Apply the standing goals to everything you write and every decision you make",
		"whichever goal the work item or your lane serves",
		"Read the standing set in the goals documents' Standing goals section",
		"product.specifications",
		"delivered as authoritative product intent",
		"docs/product/goals/v1-goals.md",
		"plain-language and autonomy goals",
		"An output or decision that breaks a standing goal is a defect to report",
		"name the goal and where it was broken",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("standing-goals guidance is missing %q", want)
		}
	}
}
