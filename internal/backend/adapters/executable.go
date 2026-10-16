package adapters

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// Binary is the configured executable, or the compiled adapter's default.
func Binary(descriptor backend.Descriptor) string {
	if descriptor.Binary != "" {
		return descriptor.Binary
	}
	switch descriptor.Adapter {
	case domain.BackendCodex:
		return "codex"
	case domain.BackendClaudeCode:
		return "claude"
	default:
		return ""
	}
}

// executableRunner resolves once successfully for this adapter. Availability,
// authentication, start and resume then use that absolute path, even when a
// read-only invocation changes directory or PATH changes between turns. Failed
// discovery is retried next time; installing a CLI can repair a waiting caller.
// Launch itself remains the existing runner's, with its bounds and environment.
type executableRunner struct {
	runner   execution.ProcessRunner
	binary   string
	provider domain.Backend
	lookup   func(string) (string, error)
	mu       sync.Mutex
	resolved string
}

func (r *executableRunner) resolve() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.resolved != "" {
		return r.resolved, nil
	}
	lookup := r.lookup
	if lookup == nil {
		lookup = exec.LookPath
	}
	path, err := lookup(r.binary)
	// LookPath skips non-executable PATH entries and returns ErrNotFound when
	// none can run. Preserve that distinction when an installation is present.
	if errors.Is(err, exec.ErrNotFound) && !strings.ContainsAny(r.binary, `/\`) {
		for _, directory := range filepath.SplitList(os.Getenv("PATH")) {
			candidate := filepath.Join(directory, r.binary)
			if info, statErr := os.Stat(candidate); statErr == nil && (info.IsDir() || info.Mode().Perm()&0o111 == 0) {
				err = &os.PathError{Op: "execute", Path: candidate, Err: os.ErrPermission}
				break
			}
		}
	}
	if err == nil {
		path, err = filepath.Abs(path)
	}
	if err != nil {
		return "", &backend.ExecutableError{Binary: r.binary, Provider: r.provider, Path: os.Getenv("PATH"), Cause: err}
	}
	r.resolved = path
	return path, nil
}

func (r *executableRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	// A gated launch that returns here never reaches the runner that would close
	// its hold, so it is closed here, as execution.LaunchGate says every launch
	// that started nothing closes it.
	notStarted := func() {
		if command.Gate != nil && command.Gate.Hold != nil {
			_ = command.Gate.Hold.Close()
		}
	}
	if err := ctx.Err(); err != nil {
		notStarted()
		return execution.ProcessResult{}, err
	}
	path, err := r.resolve()
	if err != nil {
		notStarted()
		return execution.ProcessResult{}, errors.Join(execution.ErrProcessNotStarted, err)
	}
	command.Name = path
	result, err := r.runner.Run(ctx, command, observer)
	if errors.Is(err, execution.ErrProcessNotStarted) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		err = errors.Join(err, &backend.ExecutableError{Binary: path, Provider: r.provider, Cause: err})
	}
	return result, err
}
