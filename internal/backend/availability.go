package backend

// What an availability check says when the provider did not answer it.
//
// An adapter asks its executable for its version before anything else, and for
// a long time every way that question could go unanswered came back as the one
// word "not installed". On 2026-09-27 at 00:21:34Z the first pass of the
// program manager factory-flow-pm was refused with "the claude-code backend is
// not installed" on a machine where `claude` was installed, signed in, and on
// the scheduler's own PATH. What had happened was that the operator's
// maintenance job stopped the scheduler in the same second the pass began: the
// pull's context was cancelled, `claude --version` was cancelled with it, and
// the cancelled result read as a missing executable. Everything else in that
// pull was reported as cancelled, in so many words, and the one line that said
// otherwise was recorded as a failed firing whose next firing "meets the same
// refusal until its cause is fixed" -- a cause that did not exist.
//
// So the two are kept apart here, once, for every adapter. An executable that
// was not found is Installed false, and says where it was looked for, because a
// scheduler started by launchd searches a PATH the operator's shell does not
// have and that is the first thing anybody checking needs to see. An executable
// that was found and did not answer is an error saying how: out of time, after
// how long; cancelled, carrying the cancellation so a caller that treats a
// stopped scheduler as a stop rather than a fault can still tell; or exited,
// with its status and what it wrote to stderr.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// NotFound is the availability of an executable that is not on PATH, naming the
// PATH it was looked for on. The lookup is the harness process's own -- the
// process runner resolves a bare name against its PATH, not against the
// environment it hands the child -- so that is the PATH named.
func NotFound(binary string) Availability {
	return Availability{
		Installed: false,
		Missing:   fmt.Sprintf("%s was not found on PATH %s", binary, os.Getenv("PATH")),
	}
}

// NotInstalled is the refusal for a provider whose executable was not found,
// with where it was looked for when the adapter said.
func (a Availability) NotInstalled(named domain.Backend) string {
	refused := fmt.Sprintf("the %s backend is not installed", named)
	if missing := strings.TrimSpace(a.Missing); missing != "" {
		refused = fmt.Sprintf("the %s backend cannot run in this environment: %s", named, missing)
	}
	return refused
}

// VersionCheckFailed says what became of a version check that ran and did not
// succeed. ctx is the context the check was run under, so a check stopped
// because its caller was stopped says that rather than blaming the executable,
// and carries the caller's own error for errors.Is. timeout is the bound the
// check was given, which a check that ran out of it names.
func VersionCheckFailed(ctx context.Context, binary string, timeout time.Duration, result execution.ProcessResult) error {
	command := "`" + binary + " --version`"
	if cause := ctx.Err(); cause != nil {
		return fmt.Errorf("%s was stopped before it answered, because what asked for it was stopped: %w", command, cause)
	}
	switch result.Status {
	case execution.ProcessTimedOut:
		return fmt.Errorf("%s did not answer and timed out after %s", command, timeout)
	case execution.ProcessCancelled:
		return fmt.Errorf("%s was cancelled before it answered", command)
	case execution.ProcessFailed:
		stderr := oneline.Fold(strings.TrimSpace(result.Stderr), maxFailureDetailBytes)
		if stderr == "" {
			return fmt.Errorf("%s exited with status %d and wrote nothing to stderr", command, result.ExitCode)
		}
		return fmt.Errorf("%s exited with status %d: %s", command, result.ExitCode, stderr)
	default:
		return fmt.Errorf("%s ended %s with exit code %d", command, result.Status, result.ExitCode)
	}
}
