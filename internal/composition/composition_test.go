package composition

// Fixtures, which is what these can be: the census reads a real checkout and
// nothing else, but classification and every gate reads files, so a directory
// written here is enough to prove each of them fails on the thing it is for.
// The repository test beside this one is where they meet this project.

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fixtureLedger is a ledger small enough to reason about, in the shape the real
// one is: a class recognized by suffix, one by name, and one that records why
// nothing covers it.
var fixtureLedger = []Class{
	{ID: "go", Extensions: []string{".go"}, Checks: []string{"make test"}, Exercised: "run"},
	{ID: "makefile", Names: []string{"Makefile"}, Checks: []string{"make test"}, Exercised: "invoked"},
	{ID: "image", Extensions: []string{".png"}, Unexercised: "binary"},
}

func TestClassifyRecognizesContentBySuffixNameAndShebang(t *testing.T) {
	t.Parallel()

	root := writeFixture(t, map[string]string{
		"main.go":     "package main\n",
		"Makefile":    "build:\n\ttrue\n",
		"logo.png":    "\x89PNG\r\n",
		"bin/tool":    "#!/usr/bin/env bash\ntrue\n",
		"hooks/guard": "#!/bin/sh\ntrue\n",
	})
	members, unclassified, err := Classify(root, fixtureLedger, []string{"Makefile", "bin/tool", "hooks/guard", "logo.png", "main.go"})
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	if len(unclassified) != 0 {
		t.Errorf("unclassified = %v, want nothing", unclassified)
	}
	for id, want := range map[string][]string{
		"go":       {"main.go"},
		"makefile": {"Makefile"},
		"image":    {"logo.png"},
		// Neither of these says it is shell anywhere but its first line, which is
		// the whole reason the shebang is consulted at all.
		ShellClass: {"bin/tool", "hooks/guard"},
	} {
		if !slices.Equal(members[id], want) {
			t.Errorf("members[%q] = %v, want %v", id, members[id], want)
		}
	}
}

// TestClassifyLeavesContentNothingRecognizesUnclassified is the case the audit
// exists for: content arriving in a shape nobody has written down.
func TestClassifyLeavesContentNothingRecognizesUnclassified(t *testing.T) {
	t.Parallel()

	root := writeFixture(t, map[string]string{
		"main.go":      "package main\n",
		"tool.ts":      "export const greeting = 'hello'\n",
		"notes.rst":    "Notes\n=====\n",
		"CODEOWNERS":   "* @somebody\n",
		"quiet.script": "true\n",
	})
	_, unclassified, err := Classify(root, fixtureLedger, []string{"CODEOWNERS", "main.go", "notes.rst", "quiet.script", "tool.ts"})
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	// A file with no shebang and no recognized suffix or name is unclassified
	// rather than quietly shell, which is what keeps the shebang from being a
	// catch-all that swallows the next content class.
	want := []string{"CODEOWNERS", "notes.rst", "quiet.script", "tool.ts"}
	if !slices.Equal(unclassified, want) {
		t.Errorf("unclassified = %v, want %v", unclassified, want)
	}
}

func TestCheckReportsWhatTheLedgerAndTheRepositoryDisagreeAbout(t *testing.T) {
	t.Parallel()

	root := writeFixture(t, map[string]string{
		"main.go":  "package main\n",
		"Makefile": "build:\n\ttrue\n",
		"tool.ts":  "export const greeting = 'hello'\n",
	})
	problems, err := Check(root, fixtureLedger, []string{"Makefile", "main.go", "tool.ts"}, []string{"make test"})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	// A file nothing recognizes, and a class recognizing nothing, are the two
	// halves of the ledger going out of step with what is actually here.
	want := []string{
		"tool.ts: no content class recognizes it",
		`the "image" content class recognizes nothing this repository carries`,
	}
	assertProblems(t, problems, want)
}

func TestCheckReportsAClassWhoseDeclaredCheckIsGone(t *testing.T) {
	t.Parallel()

	root := writeFixture(t, map[string]string{
		"main.go":  "package main\n",
		"Makefile": "build:\n\ttrue\n",
		"logo.png": "\x89PNG\r\n",
	})
	// The project still runs something; it no longer runs the check two classes
	// name, which is coverage removed in the configuration and nowhere else.
	problems, err := Check(root, fixtureLedger, []string{"Makefile", "logo.png", "main.go"}, []string{"make vet"})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	assertProblems(t, problems, []string{
		`the "go" content class says "make test" exercises it, and the project declares no such check`,
		`the "makefile" content class says "make test" exercises it, and the project declares no such check`,
	})
}

// TestEveryClassRecordsWhatExercisesItOrWhyNothingDoes holds the real ledger to
// its own shape. A class with neither is one somebody added without answering
// the question the ledger is for, and a class with both is one whose sentence
// nobody can act on.
func TestEveryClassRecordsWhatExercisesItOrWhyNothingDoes(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool, len(Classes))
	for _, class := range Classes {
		if seen[class.ID] {
			t.Errorf("two content classes are called %q", class.ID)
		}
		seen[class.ID] = true
		exercised := len(class.Checks) > 0 && strings.TrimSpace(class.Exercised) != ""
		unexercised := strings.TrimSpace(class.Unexercised) != ""
		if exercised == unexercised {
			t.Errorf("the %q content class records %d checks, exercised %q, unexercised %q; it has to record either the declared checks that exercise it and what they do, or why nothing does",
				class.ID, len(class.Checks), class.Exercised, class.Unexercised)
		}
		if len(class.Extensions) == 0 && len(class.Names) == 0 && class.ID != ShellClass {
			t.Errorf("the %q content class recognizes nothing, so no file can ever reach it", class.ID)
		}
	}
}

func TestShellSyntaxReportsAScriptAShellWillNotParse(t *testing.T) {
	t.Parallel()

	requireTool(t, "bash")
	root := writeFixture(t, map[string]string{
		"good.sh": "#!/usr/bin/env bash\nset -euo pipefail\nif true; then echo yes; fi\n",
		"bad.sh":  "#!/usr/bin/env bash\nif true; then echo yes\n",
	})
	assertProblems(t, ShellSyntax(root, []string{"bad.sh", "good.sh"}), []string{"bad.sh: a shell will not parse it"})
}

// TestShellSyntaxRunsNothing is the property that makes it safe to point at
// every shell file in the repository, several of which tag repositories and
// build releases when they are actually run.
func TestShellSyntaxRunsNothing(t *testing.T) {
	t.Parallel()

	requireTool(t, "bash")
	root := writeFixture(t, map[string]string{
		"effect.sh": "#!/usr/bin/env bash\ntouch ran\n",
	})
	if problems := ShellSyntax(root, []string{"effect.sh"}); len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
	if _, err := os.Stat(filepath.Join(root, "ran")); err == nil {
		t.Fatal("the script ran; parsing a shell file must not execute it")
	}
}

func TestStructuredDataReportsWhatDoesNotDecode(t *testing.T) {
	t.Parallel()

	root := writeFixture(t, map[string]string{
		"good.yaml": "version: 1\nchecks:\n  - make test\n",
		"good.json": `{"hooks": {}}`,
		"bad.yaml":  "version: 1\nchecks: [make test\n",
		"bad.json":  `{"hooks": }`,
	})
	assertProblems(t, StructuredData(root, []string{"bad.json", "bad.yaml", "good.json", "good.yaml"}), []string{
		"bad.json: it does not decode",
		"bad.yaml: it does not decode",
	})
}

func TestWorkflowsAreTheOnesUnderTheWorkflowDirectory(t *testing.T) {
	t.Parallel()

	got := Workflows([]string{".beads/config.yaml", ".github/release-notes-preamble.md", ".github/workflows/ci.yml", "docs/slack/manifest.yaml"})
	if want := []string{".github/workflows/ci.yml"}; !slices.Equal(got, want) {
		t.Errorf("Workflows() = %v, want %v", got, want)
	}
}

func TestWorkflowShapeReportsWhatIsNotAWorkflow(t *testing.T) {
	t.Parallel()

	// Four workflows of the same-length name so the fixture reads as a column:
	// one complete, one whose job has neither a runner nor steps, one with no
	// jobs at all, and one nothing ever triggers.
	root := writeFixture(t, map[string]string{
		".github/workflows/full.yml": "name: CI\non:\n  push:\n    branches: [main]\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: make check\n",
		".github/workflows/idle.yml": "name: CI\non:\n  push:\n    branches: [main]\njobs:\n  build:\n    steps: []\n",
		".github/workflows/bare.yml": "name: CI\non:\n  push:\n    branches: [main]\n",
		".github/workflows/mute.yml": "name: CI\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: make check\n",
	})
	assertProblems(t, WorkflowShape(root, []string{
		".github/workflows/idle.yml",
		".github/workflows/bare.yml",
		".github/workflows/full.yml",
		".github/workflows/mute.yml",
	}), []string{
		`.github/workflows/idle.yml: its "build" job names no ` + "`runs-on:`",
		`.github/workflows/idle.yml: its "build" job has no steps`,
		".github/workflows/bare.yml: it declares no jobs",
		".github/workflows/mute.yml: it declares no `on:` trigger",
	})
}

func TestWorkflowVersionPinsReportsWhatFloats(t *testing.T) {
	t.Parallel()

	// One workflow whose every install names a version, and one carrying each
	// spelling that does not: the module install this repository was actually
	// broken by, a branch tip, and the forge's moving release URL.
	root := writeFixture(t, map[string]string{
		".github/workflows/held.yml": "name: CI\non:\n  push:\n    branches: [main]\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v7\n      - name: Install the tracker\n        run: go install example.com/bd@v1.2.2\n",
		".github/workflows/free.yml": "name: CI\non:\n  push:\n    branches: [main]\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - name: Install the tracker\n        run: go install example.com/bd@latest\n      - run: go install example.com/other@main\n  walk:\n    runs-on: ubuntu-latest\n    steps:\n      - run: curl -fsSL https://example.com/bd/releases/latest/download/bd.tar.gz\n",
	})
	assertProblems(t, WorkflowVersionPins(root, []string{
		".github/workflows/free.yml",
		".github/workflows/held.yml",
	}), []string{
		`.github/workflows/free.yml: its "build" job runs "Install the tracker", which installs whatever the upstream released most recently`,
		`.github/workflows/free.yml: its "build" job runs step 2, which installs the upstream's branch tip`,
		`.github/workflows/free.yml: its "walk" job runs step 1, which installs whatever the upstream released most recently`,
	})
}

// TestWorkflowVersionPinsReadsCommandsRatherThanActions is the boundary between
// this gate and the forge: an action is fetched by `uses:`, whose reference the
// forge resolves and which this has no business reporting on.
func TestWorkflowVersionPinsReadsCommandsRatherThanActions(t *testing.T) {
	t.Parallel()

	root := writeFixture(t, map[string]string{
		".github/workflows/uses.yml": "name: CI\non:\n  push:\n    branches: [main]\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@main\n",
	})
	if problems := WorkflowVersionPins(root, []string{".github/workflows/uses.yml"}); len(problems) != 0 {
		t.Fatalf("problems = %v, want none: a `uses:` reference is the forge's to resolve", problems)
	}
}

// writeFixture lays out a directory of files and returns its root.
func writeFixture(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()
	for path, content := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}
	return root
}

// assertProblems holds the problems reported to the ones wanted, in order and
// by prefix: the reasons carry a decoder's own message and a suggestion, and
// pinning either would make these tests about the wording rather than about
// what was found.
func assertProblems(t *testing.T, problems []Problem, want []string) {
	t.Helper()

	if len(problems) != len(want) {
		t.Fatalf("problems = %v, want %d of them: %v", problems, len(want), want)
	}
	for index, problem := range problems {
		if !strings.HasPrefix(problem.String(), want[index]) {
			t.Errorf("problems[%d] = %s, want it to begin %q", index, problem, want[index])
		}
	}
}

func requireTool(t *testing.T, tool string) {
	t.Helper()

	if _, err := exec.LookPath(tool); err != nil {
		t.Skipf("this needs %s, which is not on PATH", tool)
	}
}

// The declared checks are what decides whether a change has to carry its
// developer's own record of running one. The question is asked of the ledger
// rather than of a list of file extensions written beside the bar, so a class
// that gains or loses coverage moves the bar without anything else being edited.
func TestWhetherTheDeclaredChecksReachWhatAChangeTouched(t *testing.T) {
	t.Parallel()

	declared := []string{"make fmtcheck", "make test", "make race", "make vet"}
	root := writeFixture(t, map[string]string{
		"internal/run.go":     "package run\n",
		"docs/work.md":        "# Work\n",
		"docs/slack/icon.png": "not really an image\n",
		"LICENSE":             "a licence\n",
		".beads/issues.jsonl": "{}\n",
		"bin/release":         "#!/bin/sh\necho released\n",
	})

	for _, asked := range []struct {
		name      string
		paths     []string
		exercised bool
	}{
		{"a change to Go", []string{"internal/run.go"}, true},
		{"a change to prose the suite reads", []string{"docs/work.md"}, true},
		{"a change to shell recognized by its first line", []string{"bin/release"}, true},
		{"an image nothing deterministic can read", []string{"docs/slack/icon.png"}, false},
		{"the licence and the tracker's export", []string{"LICENSE", ".beads/issues.jsonl"}, false},
		{"one exercised path among unexercised ones", []string{"LICENSE", "internal/run.go"}, true},
		{"content no class recognizes", []string{"notes.rst"}, true},
		{"a file the change deleted", []string{"internal/gone.go"}, true},
		{"nothing at all", nil, false},
	} {
		t.Run(asked.name, func(t *testing.T) {
			t.Parallel()
			if exercised := Exercised(root, declared, asked.paths); exercised != asked.exercised {
				t.Fatalf("Exercised(%v) = %t, want %t", asked.paths, exercised, asked.exercised)
			}
		})
	}
}

// And the leeway is available at all only where this ledger is an account of the
// project being asked about. A project declaring checks the ledger was not
// written against is asked for evidence on every change, which is the bar this
// repository held before any leeway existed.
func TestTheLedgerExcusesNothingInAProjectItDoesNotDescribe(t *testing.T) {
	t.Parallel()

	root := writeFixture(t, map[string]string{"docs/slack/icon.png": "not really an image\n"})
	if Describes([]string{"cargo test"}) {
		t.Fatal("the ledger claims to describe a project whose checks it does not name")
	}
	if !Exercised(root, []string{"cargo test"}, []string{"docs/slack/icon.png"}) {
		t.Fatal("a project this ledger does not describe was granted the leeway anyway")
	}
	if !Describes([]string{"make fmtcheck", "make test", "make race", "make vet"}) {
		t.Fatal("the ledger does not describe the project it was written for")
	}
	// A check named with what it passes is still that check, and a longer name
	// that only begins with one is not.
	if !Describes([]string{"make fmtcheck", `make test CHANGED_GO_PACKAGES="${YOYODYNE_CHANGED_GO_PACKAGES-./...}"`, `make race CHANGED_GO_PACKAGES="${YOYODYNE_CHANGED_GO_PACKAGES-./...}"`, "make vet"}) {
		t.Fatal("the ledger does not describe the project once its checks pass the packages a change touches")
	}
	if Describes([]string{"make fmtcheck", "make testall", "make race", "make vet"}) {
		t.Fatal("the ledger took make testall for make test")
	}
}
