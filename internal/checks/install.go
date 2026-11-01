package checks

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// A project's install step (config.InstallStep) is run the way its declared
// checks are, by this runner: as the harness's own user, through the runner's
// shell in the checkout, under the same environment built from the allowlist
// with the provider CLIs hidden, in a process group killed when it ends, and
// behind a place among the machine's heavy checks where execution.heavy_checks
// names its command. Nothing here is an agent and nothing here is sandboxed, so
// the step's command reaches the network as a check's does; no agent session is
// given anything by it.
//
// What differs is what it is told and how long it may take. It alone is told
// the project's package cache folder, in InstallCacheVariable, and it is bound
// by its own time limit rather than by a check stage's.

// InstallCacheVariable is the environment variable an install step's command,
// and only that command, is told the project's package cache folder in.
const InstallCacheVariable = "YOYODYNE_INSTALL_CACHE"

// InstallRequest is one attempt at one install step in one checkout.
type InstallRequest struct {
	RunID        string
	Directory    string
	Command      string
	LastSequence uint64
	// TimeLimit bounds the attempt. Zero is the runner's check budget.
	TimeLimit time.Duration
	// Cache is the package cache folder the command is told, and empty for a
	// caller that has none to give.
	Cache  string
	Permit PermitRequest
	// Gate is the launch gate the command is started behind, as Request.Gate
	// is a check's, and nil for the ordinary start.
	Gate func(command string) (*execution.LaunchGate, error)
}

// RunInstall runs one attempt at an install step and returns its result.
func (r Runner) RunInstall(ctx context.Context, request InstallRequest, sink func(execution.Event) error) (Result, uint64, error) {
	return RunInstallWith(ctx, r, request, sink)
}

// CheckRequest is the request a check runner is given for one attempt at an
// install step: the step's command alone, its own time limit with no stage
// bound, and the cache folder in InstallCacheVariable.
func (request InstallRequest) CheckRequest() Request {
	var env []string
	if request.Cache != "" {
		env = append(env, InstallCacheVariable+"="+request.Cache)
	}
	return Request{
		RunID:        request.RunID,
		Directory:    request.Directory,
		Commands:     []string{request.Command},
		LastSequence: request.LastSequence,
		Env:          env,
		Timeout:      request.TimeLimit,
		Unbounded:    true,
		Permit:       request.Permit,
		Gate:         request.Gate,
	}
}

// CheckRun is what RunInstallWith runs an install step through: Runner, or
// anything a caller stands in for it.
type CheckRun interface {
	Run(ctx context.Context, request Request, sink func(execution.Event) error) ([]Result, uint64, error)
}

// RunInstallWith runs one attempt at an install step through runner and
// returns its result.
func RunInstallWith(ctx context.Context, runner CheckRun, request InstallRequest, sink func(execution.Event) error) (Result, uint64, error) {
	if strings.TrimSpace(request.Command) == "" {
		return Result{}, request.LastSequence, errors.New("install step command cannot be empty")
	}
	results, last, err := runner.Run(ctx, request.CheckRequest(), sink)
	if len(results) == 0 {
		if err == nil {
			err = errors.New("install step produced no result")
		}
		return Result{}, last, err
	}
	return results[0], last, err
}
