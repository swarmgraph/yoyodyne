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

// executableRunner resolves the provider's program for this adapter.
// Availability, authentication, start and resume then use that absolute path,
// even when a read-only invocation changes directory or PATH changes between
// turns. The path is checked again before each use, so a program that moved
// after it was resolved is looked for afresh rather than launched from where it
// used to be. Failed discovery is retried next time; installing a CLI can
// repair a waiting caller. Launch itself remains the existing runner's, with
// its bounds and environment.
//
// Where nothing was configured for the provider and its program is not on the
// search path, the usual install folders are looked in too (usualInstallFolders).
// A service started with a fixed search path does not see a program its
// installer moved to ~/.local/bin, and without this every turn needing that
// provider stops. A configured binary is never looked for anywhere else: it
// wins, found or not.
type executableRunner struct {
	runner   execution.ProcessRunner
	binary   string
	provider domain.Backend
	lookup   func(string) (string, error)
	// fallback is whether the usual install folders may be searched: true only
	// when no binary was configured for the provider.
	fallback bool
	folders  func() []string
	mu       sync.Mutex
	resolved string
}

// usualInstallFolders is the short fixed list of folders a provider's program
// is looked for in when it is not on the search path and nothing was
// configured: where Claude Code's own installer and the npm prefix it falls
// back to put it, and Homebrew's two prefixes. Nothing outside this list is
// searched. It is a variable so a test can point it at folders it made.
var usualInstallFolders = func() []string {
	var folders []string
	if home, err := os.UserHomeDir(); err == nil && filepath.IsAbs(home) {
		folders = append(folders, filepath.Join(home, ".local", "bin"))
	}
	return append(folders, "/opt/homebrew/bin", "/usr/local/bin")
}

// UsualInstallFolders is the list the adapters search, for a surface that
// looks for a provider's program the way they do.
func UsualInstallFolders() []string { return usualInstallFolders() }

// ResolveExecutable is the program this runner would start, resolved now. It
// lets the adapter record which path it used (backend.ResolvedExecutable).
func (r *executableRunner) ResolveExecutable() (string, error) {
	return r.resolve()
}

func (r *executableRunner) resolve() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.resolved != "" {
		if info, err := os.Stat(r.resolved); err == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0 {
			return r.resolved, nil
		}
		// The program moved or was removed after it was found: look again.
		r.resolved = ""
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
	var searched []string
	if errors.Is(err, exec.ErrNotFound) && r.fallback && !strings.ContainsAny(r.binary, `/\`) {
		folders := r.folders
		if folders == nil {
			folders = usualInstallFolders
		}
		for _, folder := range folders() {
			searched = append(searched, folder)
			// The same lookup is asked about the folder's file, so a diagnosis
			// that stands in its own lookup is answered by it here too.
			if found, foundErr := lookup(filepath.Join(folder, r.binary)); foundErr == nil {
				path, err = found, nil
				break
			}
		}
	}
	if err == nil {
		path, err = filepath.Abs(path)
	}
	if err != nil {
		return "", &backend.ExecutableError{Binary: r.binary, Provider: r.provider, Path: os.Getenv("PATH"), Folders: searched, Cause: err}
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
