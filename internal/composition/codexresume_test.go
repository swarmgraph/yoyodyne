package composition

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The Codex resume test's list chooses it only for a change touching the Codex
// adapter or what it runs in, and a change elsewhere does not run it. Where
// this project's configuration keeps the provider CLIs visible to a check, it
// is that check alone, over that list; every other check keeps them hidden.
func TestTheCodexResumeCheckRunsOnlyForTheCodexAdapter(t *testing.T) {
	t.Parallel()

	declared, err := config.Load(repositoryConfigPath)
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	for _, check := range declared.PathChecks {
		if check.NeedsProviderCLIs && (check.Command != "make codex-resume" || check.Paths != "scripts/codex-resume.paths") {
			t.Errorf("path check %#v keeps the provider CLIs visible; only make codex-resume over scripts/codex-resume.paths may", check)
		}
	}
	patterns, err := checks.ReadPathPatterns(repositoryRoot, "scripts/codex-resume.paths")
	if err != nil {
		t.Fatalf("ReadPathPatterns() error = %v", err)
	}
	for _, test := range []struct {
		changed string
		runs    bool
	}{
		{changed: "internal/backend/codex/args.go", runs: true},
		{changed: "internal/backend/codex/native_resume_test.go", runs: true},
		{changed: "internal/execution/process.go", runs: true},
		{changed: "internal/backend/backend.go", runs: true},
		{changed: "internal/backend/claude/args.go", runs: false},
		{changed: "internal/cli/status.go", runs: false},
		{changed: "docs/configuration/runs.md", runs: false},
	} {
		if _, runs := checks.Touching(patterns, []string{test.changed}); runs != test.runs {
			t.Errorf("a change to %s runs make codex-resume = %v, want %v", test.changed, runs, test.runs)
		}
	}
}

// Run the repository's codex-resume target through the harness's own check
// runner over a stand-in go command, so the recipe and the runner's reading of
// it are what these cases hold to account. A test that passed passes the check;
// a test that ran and failed fails it; a test that skipped — no Codex here, or
// a developer run's sandbox — is a check that could not run, carrying the
// test's own reason, never a pass and never a failure that spends a repair.
func TestTheCodexResumeTargetSaysWhenItCouldNotRun(t *testing.T) {
	t.Parallel()

	requireTool(t, "make")
	makefile, err := filepath.Abs(filepath.Join(repositoryRoot, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	const test = "TestNativeResumeReplacesSavedDirectoryGrants"
	for _, scenario := range []struct {
		name        string
		output      string
		exit        int
		passed      bool
		couldNotRun string
	}{
		{
			name:   "passed",
			output: "=== RUN   " + test + "\n--- PASS: " + test + " (4.20s)\nPASS\nok  \tgithub.com/mason-bryant/yoyodyne/internal/backend/codex\t4.3s\n",
			passed: true,
		},
		{
			name:   "ran and failed",
			output: "=== RUN   " + test + "\n    native_resume_test.go:300: the resumed turn wrote outside its grants\n--- FAIL: " + test + " (4.20s)\nFAIL\nFAIL\tgithub.com/mason-bryant/yoyodyne/internal/backend/codex\t4.3s\n",
			exit:   1,
		},
		{
			name:        "no Codex installed",
			output:      "=== RUN   " + test + "\n    native_resume_test.go:81: native resume requires an installed Codex CLI: exec: \"codex\": executable file not found in $PATH\n--- SKIP: " + test + " (0.00s)\nPASS\nok  \tgithub.com/mason-bryant/yoyodyne/internal/backend/codex\t0.1s\n",
			couldNotRun: "the Codex native-resume test did not run here, so nothing was checked: native resume requires an installed Codex CLI: exec: \"codex\": executable file not found in $PATH",
		},
		{
			name:        "skipped without saying why",
			output:      "=== RUN   " + test + "\n--- SKIP: " + test + " (0.00s)\nPASS\n",
			couldNotRun: "the Codex native-resume test did not run here, so nothing was checked: the test neither passed nor said why",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			transcript := filepath.Join(root, "transcript.txt")
			if err := os.WriteFile(transcript, []byte(scenario.output), 0o600); err != nil {
				t.Fatal(err)
			}
			cache := filepath.Join(root, "cache")
			stand := "#!/bin/sh\nif [ \"$1\" = env ]; then echo '" + cache + "'; exit 0; fi\ncat '" + transcript + "'\nexit " + strconv.Itoa(scenario.exit) + "\n"
			goCommand := filepath.Join(root, "go")
			if err := os.WriteFile(goCommand, []byte(stand), 0o700); err != nil {
				t.Fatal(err)
			}
			results, _, err := (checks.Runner{Process: execution.OSProcessRunner{}}).Run(
				context.Background(),
				checks.Request{
					RunID:     "run-0123456789abcdef0123456789abcdef",
					Directory: root,
					Commands:  []string{"make --no-print-directory -f '" + makefile + "' codex-resume GO='" + goCommand + "' VERSION=codex-resume-test"},
					Env:       []string{"MAKEFLAGS=", "MFLAGS=", "MAKELEVEL=0"},
				},
				func(execution.Event) error { return nil },
			)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if len(results) != 1 {
				t.Fatalf("results = %#v, want one", results)
			}
			result := results[0]
			if result.Passed != scenario.passed || result.CouldNotRun != scenario.couldNotRun {
				t.Fatalf("result passed = %v, could not run = %q; want %v, %q\noutput:\n%s", result.Passed, result.CouldNotRun, scenario.passed, scenario.couldNotRun, checks.FailureOutput(result))
			}
			if !scenario.passed && scenario.couldNotRun == "" && !strings.Contains(checks.FailureOutput(result), "--- FAIL: "+test) {
				t.Fatalf("failure output = %q, want the failing test named", checks.FailureOutput(result))
			}
		})
	}
}
