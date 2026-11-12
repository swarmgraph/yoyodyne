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
	// Folders are the usual install folders also looked in, because nothing
	// was configured and the program was not on the search path. Empty where
	// none were searched: a configured binary is never looked for anywhere else.
	Folders []string
	Cause   error
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
			if len(e.Folders) > 0 {
				detail += " or in the usual install folders " + strings.Join(e.Folders, ", ")
			}
		}
	default:
		detail = fmt.Sprintf("cannot run executable %q in this environment: %v", e.Binary, e.Cause)
	}
	return fmt.Sprintf("%s; set providers.%s.binary to the absolute path of a usable CLI in the project configuration, or install it on this process's PATH", detail, e.Provider)
}

func (e *ExecutableError) Unwrap() error { return e.Cause }

// ProgramMissing reports a program that was found nowhere it was looked for,
// as opposed to one that was found and could not be run.
func (e *ExecutableError) ProgramMissing() bool {
	return errors.Is(e.Cause, exec.ErrNotFound) || errors.Is(e.Cause, os.ErrNotExist)
}

// ProgramMissingOutage reads an invocation the shared resolver refused because
// the provider's program is found nowhere, and reports it as the wait it is: a
// provider outage that spends nothing, rather than a process the machine could
// not start. Nothing was started, so nothing about the work was judged, and
// the run waits for the program to be found again instead of being stopped as
// a sandbox that could not be entered. A program that was found and is not
// executable is not this: that is a setup fault reported as it always was.
func ProgramMissingOutage(err error) (*ProviderOutage, bool) {
	var unavailable *ExecutableError
	if !errors.As(err, &unavailable) || !unavailable.ProgramMissing() {
		return nil, false
	}
	return &ProviderOutage{Cause: domain.ProviderProgramMissing, Detail: unavailable.Error(), Provider: unavailable.Provider}, true
}

// ExecutableResolver is a process runner that resolves the provider's program
// itself, and can say which file it resolved to. The adapters ask it so that
// availability and each invocation record the path actually used.
type ExecutableResolver interface {
	ResolveExecutable() (string, error)
}

// ResolvedExecutable is the path a runner resolved the provider's program to,
// and empty for a runner that does not resolve one or could not.
func ResolvedExecutable(runner any) string {
	resolver, ok := runner.(ExecutableResolver)
	if !ok {
		return ""
	}
	path, err := resolver.ResolveExecutable()
	if err != nil {
		return ""
	}
	return path
}

// ExecutableUnavailable recognizes the shared resolver's refusal, including
// through a process-not-started wrapper, without calling it an auth failure.
func ExecutableUnavailable(err error) (Availability, bool) {
	var unavailable *ExecutableError
	if !errors.As(err, &unavailable) {
		return Availability{}, false
	}
	return Availability{Missing: unavailable.Error(), ProgramMissing: unavailable.ProgramMissing()}, true
}
