package checks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// installed puts executables of the given names in one fresh directory at the
// front of this process's search path, the way Homebrew puts a provider CLI
// beside the toolchain, and returns the directory.
func installed(t *testing.T, names ...string) string {
	t.Helper()
	directory := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	t.Setenv("PATH", directory+string(filepath.ListSeparator)+os.Getenv("PATH"))
	return directory
}

// A check that passes only because a provider CLI is installed where it runs
// fails in the check stage, while the tools installed beside that CLI are still
// found. The same check without the provider hidden passes, so the failure is
// the hiding and nothing else.
func TestACheckThatNeedsAnInstalledProviderCLIFails(t *testing.T) {
	// t.Setenv is this process's environment, so this cannot run in parallel.
	installed(t, "claude", "codex", "beside-the-provider")
	check := "command -v claude || command -v codex"

	results, _, err := (Runner{Process: execution.OSProcessRunner{}}).Run(
		context.Background(),
		Request{RunID: "run-0123456789abcdef0123456789abcdef", Directory: t.TempDir(), Commands: []string{check}},
		nil,
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(results) != 1 || !results[0].Passed {
		t.Fatalf("with the provider on the path the check should pass, so the case is real: %#v", results)
	}

	results, _, err = (Runner{Process: execution.OSProcessRunner{}, HiddenExecutables: []string{"claude", "codex"}}).Run(
		context.Background(),
		Request{RunID: "run-0123456789abcdef0123456789abcdef", Directory: t.TempDir(), Commands: []string{check, "exit 0"}},
		nil,
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(results) != 1 || results[0].Passed {
		t.Fatalf("a check that needs an installed provider CLI passed: %#v", results)
	}

	results, _, err = (Runner{Process: execution.OSProcessRunner{}, HiddenExecutables: []string{"claude", "codex"}}).Run(
		context.Background(),
		Request{RunID: "run-0123456789abcdef0123456789abcdef", Directory: t.TempDir(), Commands: []string{"command -v beside-the-provider && command -v sh"}},
		nil,
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(results) != 1 || !results[0].Passed {
		t.Fatalf("hiding the provider hid the tools beside it: %#v", results)
	}
}

// A provider configured by path is hidden by the name a lookup would find it
// under, and the directory made for the stage is gone once the stage is over.
func TestAConfiguredProviderPathIsHiddenAndTheStagesPathRemoved(t *testing.T) {
	directory := installed(t, "my-claude")

	results, _, err := (Runner{Process: execution.OSProcessRunner{}, HiddenExecutables: []string{filepath.Join(directory, "my-claude")}}).Run(
		context.Background(),
		Request{RunID: "run-0123456789abcdef0123456789abcdef", Directory: t.TempDir(), Commands: []string{`printf '%s' "$PATH"; ! command -v my-claude >/dev/null`}},
		nil,
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(results) != 1 || !results[0].Passed {
		t.Fatalf("a provider configured by path was still found by its name: %#v", results)
	}
	stage := filepath.SplitList(results[0].Process.Stdout)[0]
	if stage == directory || !strings.Contains(stage, "yoyodyne-check-path-") {
		t.Fatalf("the provider's directory was not replaced on the search path: %s", results[0].Process.Stdout)
	}
	if _, err := os.Lstat(stage); !os.IsNotExist(err) {
		t.Fatalf("the stage's search path directory %s is still there after the stage: %v", stage, err)
	}
}

// A search path holding no provider is passed through untouched, and nothing is
// written for it.
func TestASearchPathWithoutAProviderIsLeftAlone(t *testing.T) {
	t.Parallel()

	environment := []string{"HOME=/home/someone", "PATH=" + t.TempDir() + string(filepath.ListSeparator) + "/usr/bin"}
	rewritten, cleanup, err := withoutExecutables(environment, t.TempDir(), []string{"claude", "codex"})
	defer cleanup()
	if err != nil {
		t.Fatalf("withoutExecutables() error = %v", err)
	}
	if strings.Join(rewritten, "\n") != strings.Join(environment, "\n") {
		t.Fatalf("environment = %v, want it unchanged %v", rewritten, environment)
	}
}
