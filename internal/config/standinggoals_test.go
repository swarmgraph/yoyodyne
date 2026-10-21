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

// The reviewer is the one gate every change passes, so both copies of its
// persona say it judges every change against the standing set beside the
// item's own goal, refuses a change that breaks one as a major finding naming
// the goal and the place, and names the goals it checked in its summary.
func TestReviewerPersonaJudgesEveryChangeAgainstStandingGoals(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		filepath.Join("builtin", "v1", bundlePersonaDirectory, "reviewer.md"),
		filepath.Join("..", "..", ".yoyodyne", "personas", "reviewer.md"),
	} {
		t.Run(path, func(t *testing.T) {
			text, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			flat := strings.Join(strings.Fields(string(text)), " ")
			for _, want := range []string{
				"judge every change against the standing goals",
				"the plain-language goal and the autonomy goal",
				"as well as against the goal the item serves",
				"A change that breaks a standing goal is a major finding",
				"name the goal and the place it is broken",
				"name each standing goal you checked the change against",
			} {
				if !strings.Contains(flat, want) {
					t.Errorf("the reviewer persona is missing %q", want)
				}
			}
		})
	}
}

// Every program manager remit this repository's instances read carries the
// rule too, naming the lane report it writes; a remit added without it fails
// here. No remit is shipped in the template, so the live copies are the whole
// set.
func TestRemitsApplyStandingGoals(t *testing.T) {
	t.Parallel()

	paths, err := filepath.Glob(filepath.Join("..", "..", ".yoyodyne", "remits", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	var sawFactoryFlow bool
	for _, path := range paths {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			remit, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			assertStandingGoals(t, string(remit))
			flat := strings.Join(strings.Fields(string(remit)), " ")
			if !strings.Contains(flat, "That includes your lane report") {
				t.Error("the remit does not apply the standing set to its lane report")
			}
			if name == "factory-flow.md" && !strings.Contains(flat, "your post-mortems on stopped runs") {
				t.Error("the factory-flow remit does not apply the standing set to its post-mortems on stopped runs")
			}
		})
		if name == "factory-flow.md" {
			sawFactoryFlow = true
		}
	}
	if !sawFactoryFlow {
		t.Fatal("found no factory-flow remit under .yoyodyne/remits")
	}
}
