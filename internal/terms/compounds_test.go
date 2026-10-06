package terms

import (
	"strings"
	"testing"
)

// compoundRegister is a register with one row, one replaced term, and one
// ordinary compound, which is every kind of entry a compound can be accounted
// for by.
func compoundRegister() string {
	return "# Terms\n\n## The register\n\n| Term | In plain words | Where it is used |\n| --- | --- | --- |\n" +
		"| `hand-off` | giving a run to another role | `yoyo work` |\n\n" +
		"## Replaced rather than registered\n\n| Term | Write instead | Still written in |\n| --- | --- | --- |\n" +
		"| `whose-move` | waiting on you | |\n\n" +
		"## Ordinary compounds\n\n`trade-off` `round-trip`\n"
}

// The case the item asks for: a coinage added to a string a command prints is
// refused, naming where it is written and what would register it.
func TestCompoundsRefuseACoinageAddedToAPrintedString(t *testing.T) {
	t.Parallel()

	directory := root(t, compoundRegister(), map[string]string{
		"internal/cli/status.go": "package cli\n\nconst said = \"the run is in a merge-limbo until a person looks\"\n",
	})
	problems, err := compoundProblems(directory, nil)
	if err != nil {
		t.Fatalf("compoundProblems() error = %v", err)
	}
	if len(problems) != 1 {
		t.Fatalf("compoundProblems() reported %d problems, want 1: %v", len(problems), problems)
	}
	problem := problems[0]
	if problem.Path != "internal/cli/status.go" || problem.Line != 3 || problem.Term != "merge-limbo" {
		t.Errorf("problem = %+v, want merge-limbo at internal/cli/status.go:3", problem)
	}
	for _, want := range []string{RegisterPath, "Ordinary compounds"} {
		if !strings.Contains(problem.Reason, want) {
			t.Errorf("reason %q does not say %q, which is what would answer it", problem.Reason, want)
		}
	}
}

// Ordinary English passes: by form, by the register's list, by being a row or
// a replaced term or a term the inventory is waiting on, and by being a name.
func TestCompoundsPassOrdinaryEnglish(t *testing.T) {
	t.Parallel()

	sentence := "Re-run the read-only, repository-wide check; a hand-edited file, a two-hour wait, an e-mail, " +
		"a trade-off, a round-trip, the hand-off, Claude-Code and yoyodyne-report, the docket-entry wait, " +
		"and the observability-and-dashboard design."
	directory := root(t, compoundRegister(), map[string]string{
		"internal/cli/status.go":                      "package cli\n\nconst said = \"" + sentence + "\"\n",
		"docs/operations.md":                          "# Operations\n\n" + sentence + "\n",
		"docs/designs/observability-and-dashboard.md": "# Observability\n\nNothing.\n",
	})
	problems, err := compoundProblems(directory, nil)
	if err != nil {
		t.Fatalf("compoundProblems() error = %v", err)
	}
	for _, problem := range problems {
		t.Errorf("an ordinary compound was refused: %s", problem)
	}
}

// What is not prose is not read: a flag, a path, a file name, a quoted value, a
// string with no space in it, a code span, a link's target, a fenced block,
// frontmatter, and a test file.
func TestCompoundsReadOnlyProse(t *testing.T) {
	t.Parallel()

	directory := root(t, compoundRegister(), map[string]string{
		"internal/cli/flags.go": "package cli\n\nconst (\n\tflag = \"stall-after\"\n" +
			"\thelp = \"pass --stall-after or read docs/run-stops.md or ask for \\\"beads-id\\\" by run-stops.md\"\n)\n",
		"internal/cli/flags_test.go": "package cli\n\nconst said = \"a merge-limbo here\"\n",
		"docs/operations.md": "---\ntitle: a merge-limbo\n---\n\n# Operations\n\nSee `merge-limbo` and [the guide](designs/merge-limbo.md).\n\n" +
			"```\nmerge-limbo\n```\n",
	})
	problems, err := compoundProblems(directory, nil)
	if err != nil {
		t.Fatalf("compoundProblems() error = %v", err)
	}
	for _, problem := range problems {
		t.Errorf("something that is not prose was refused: %s", problem)
	}
}

// The personas are read, the shipped templates and the live copies both.
func TestCompoundsReadThePersonas(t *testing.T) {
	t.Parallel()

	directory := root(t, compoundRegister(), map[string]string{
		"internal/config/builtin/v1/personas/developer.md": "# Developer\n\nAvoid a merge-limbo.\n",
		".yoyodyne/personas/developer.md":                  "# Developer\n\nAvoid a merge-limbo.\n",
	})
	problems, err := compoundProblems(directory, nil)
	if err != nil {
		t.Fatalf("compoundProblems() error = %v", err)
	}
	reported := make(map[string]bool)
	for _, problem := range problems {
		reported[problem.Path] = true
	}
	for _, path := range []string{"internal/config/builtin/v1/personas/developer.md", ".yoyodyne/personas/developer.md"} {
		if !reported[path] {
			t.Errorf("compoundProblems() did not read %s; reported %v", path, problems)
		}
	}
}

// A word listed as unread is allowed by name while it is written, and refused
// as a stale entry once nothing writes it.
func TestAnUnreadCompoundIsAllowedByNameUntilNothingWritesIt(t *testing.T) {
	t.Parallel()

	directory := root(t, compoundRegister(), map[string]string{
		"docs/operations.md": "# Operations\n\nA merge-limbo.\n",
	})
	problems, err := compoundProblems(directory, []string{"merge-limbo", "gone-word"})
	if err != nil {
		t.Fatalf("compoundProblems() error = %v", err)
	}
	if len(problems) != 1 || problems[0].Term != "gone-word" || problems[0].Path != UnreadPath {
		t.Fatalf("compoundProblems() = %v, want only gone-word reported as no longer written", problems)
	}
}

func TestOrdinaryReadsOnlyItsOwnSection(t *testing.T) {
	t.Parallel()

	ordinary := ordinaryIn(compoundRegister())
	if !ordinary["trade-off"] || !ordinary["round-trip"] || ordinary["hand-off"] || ordinary["whose-move"] {
		t.Errorf("ordinaryIn() = %v, want the two listed under its heading and nothing from the tables", ordinary)
	}
}
