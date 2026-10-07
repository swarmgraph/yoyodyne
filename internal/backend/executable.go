package backend

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// ExecutableError describes discovery separately from a provider that started
// and refused authentication or a request.
type ExecutableError struct {
	Binary   string
	Provider domain.Backend
	Path     string
	Cause    error
}

func (e *ExecutableError) Error() string {
	var detail string
	switch {
	case errors.Is(e.Cause, os.ErrPermission), errors.Is(e.Cause, syscall.EISDIR):
		detail = fmt.Sprintf("%q is not executable in this environment: %v", e.Binary, e.Cause)
	case errors.Is(e.Cause, exec.ErrNotFound), errors.Is(e.Cause, os.ErrNotExist):
		if strings.ContainsAny(e.Binary, `/\`) {
			detail = fmt.Sprintf("%q was not found in this environment", e.Binary)
		} else {
			detail = fmt.Sprintf("%q was not found on PATH %s", e.Binary, e.Path)
		}
	default:
		detail = fmt.Sprintf("cannot run executable %q in this environment: %v", e.Binary, e.Cause)
	}
	return fmt.Sprintf("%s; set providers.%s.binary to the absolute path of a usable CLI in the project configuration, or install it on this process's PATH", detail, e.Provider)
}

func (e *ExecutableError) Unwrap() error { return e.Cause }

// ExecutableUnavailable recognizes the shared resolver's refusal, including
// through a process-not-started wrapper, without calling it an auth failure.
func ExecutableUnavailable(err error) (Availability, bool) {
	var unavailable *ExecutableError
	if !errors.As(err, &unavailable) {
		return Availability{}, false
	}
	return Availability{Missing: unavailable.Error()}, true
}
