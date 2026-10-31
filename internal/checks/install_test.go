package checks

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// An install step is run as a check is — the runner's shell, in the checkout,
// with the environment a check gets — and is told the cache folder that no
// check is told, under its own time limit rather than a stage's.
func TestAnInstallStepRunsAsACheckRunsAndAloneIsToldTheCache(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	install := &recordingRunner{}
	result, _, err := (Runner{Process: install, StageTimeout: time.Minute}).RunInstall(context.Background(), InstallRequest{
		RunID:     "run-0123456789abcdef0123456789abcdef",
		Directory: directory,
		Command:   `npm ci --cache "$YOYODYNE_INSTALL_CACHE"`,
		TimeLimit: 10 * time.Minute,
		Cache:     "/state/cache/example",
	}, nil)
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}
	if !result.Passed || result.Timeout != 10*time.Minute {
		t.Fatalf("result = %#v, want a pass under the step's own ten minutes", result)
	}
	if install.command.Name != "/bin/sh" || !reflect.DeepEqual(install.command.Args, []string{"-c", `npm ci --cache "$YOYODYNE_INSTALL_CACHE"`}) || install.command.Dir != directory {
		t.Fatalf("command = %#v, want the step through the check shell in the checkout", install.command)
	}
	if install.command.Timeout != 10*time.Minute {
		t.Fatalf("timeout = %s, want the step's limit and not the stage's minute", install.command.Timeout)
	}
	if !slices.Contains(install.command.Env, InstallCacheVariable+"=/state/cache/example") {
		t.Fatalf("env = %q, want the cache named", install.command.Env)
	}

	check := &recordingRunner{}
	if _, _, err := (Runner{Process: check}).Run(context.Background(), Request{RunID: "run-0123456789abcdef0123456789abcdef", Directory: directory, Commands: []string{"npm test"}}, nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, entry := range check.command.Env {
		if strings.HasPrefix(entry, InstallCacheVariable+"=") {
			t.Fatalf("a check was told %s", entry)
		}
	}
	without := slices.DeleteFunc(slices.Clone(install.command.Env), func(entry string) bool {
		return strings.HasPrefix(entry, InstallCacheVariable+"=")
	})
	if !reflect.DeepEqual(without, check.command.Env) {
		t.Fatalf("install env = %q\ncheck env = %q\nwant the same but for the cache", without, check.command.Env)
	}
}

// A real step runs, writes in the checkout, and reports a failure as a check
// reports one.
func TestAnInstallStepWritesItsOutputInTheCheckout(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	runner := Runner{Process: execution.OSProcessRunner{}}
	result, _, err := runner.RunInstall(context.Background(), InstallRequest{
		RunID:     "run-0123456789abcdef0123456789abcdef",
		Directory: directory,
		Command:   `mkdir -p node_modules && printf '%s' "$YOYODYNE_INSTALL_CACHE" > node_modules/cache`,
		TimeLimit: time.Minute,
		Cache:     "/state/cache/example",
	}, nil)
	if err != nil || !result.Passed {
		t.Fatalf("RunInstall() = %#v, %v, want a pass", result, err)
	}
	written, err := os.ReadFile(filepath.Join(directory, "node_modules", "cache"))
	if err != nil || string(written) != "/state/cache/example" {
		t.Fatalf("node_modules/cache = %q, %v, want the cache folder", written, err)
	}
	failed, _, err := runner.RunInstall(context.Background(), InstallRequest{
		RunID: "run-0123456789abcdef0123456789abcdef", Directory: directory, Command: "exit 7", TimeLimit: time.Minute,
	}, nil)
	if err != nil || failed.Passed || failed.Process.ExitCode != 7 {
		t.Fatalf("RunInstall(exit 7) = %#v, %v, want a failure", failed, err)
	}
}
