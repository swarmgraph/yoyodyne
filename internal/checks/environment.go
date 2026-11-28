package checks

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// An environment's check (config.Environment.Check) is run by this runner, as
// a declared check is, wherever its environment says
// (docs/designs/checking-a-run-environment.md, section 3):
//
//   - where: harness runs it exactly as a declared check: as the harness's own
//     user, through the runner's shell in the checkout, behind a place among
//     the machine's heavy checks, with no more access than the declared checks
//     have.
//   - where: developer runs the same command through the same runner, but
//     started under the developer session's own restrictions
//     (DeveloperRestrictions): no network but localhost, no socket file, and
//     writes only where a developer may write. It is never given what an
//     install step is given, the package cache folder included.
//
// Either way it is bound by its environment's time limit rather than by a
// check stage's.

// EnvironmentRequest is one run of one environment's check, or of its test, in
// one checkout. The test runs exactly as the check does, in the same place.
type EnvironmentRequest struct {
	RunID        string
	Directory    string
	Command      string
	LastSequence uint64
	// TimeLimit bounds the check. Zero is the runner's check budget.
	TimeLimit time.Duration
	// Restrictions, where set, are the developer session's restrictions the
	// check runs under; nil runs it where the declared checks run.
	Restrictions *DeveloperRestrictions
	// TestScratch is the empty folder this one run of the command is given, as
	// TestScratchVariable, and empty where it is given none.
	TestScratch string
	Permit      PermitRequest
}

// TestScratchVariable names the empty folder each run of an environment's check
// or test is given, under the run's scratch space, and removed afterwards
// (docs/designs/checking-a-run-environment.md, section 6): a test that changes
// state works on data that is thrown away.
const TestScratchVariable = "YOYODYNE_TEST_SCRATCH"

// CheckRequest is the request a check runner is given for the environment's
// check: its command alone, its own time limit with no stage bound, wrapped
// in the developer's restrictions where it has them.
func (request EnvironmentRequest) CheckRequest() Request {
	checkRequest := Request{
		RunID:        request.RunID,
		Directory:    request.Directory,
		Commands:     []string{request.Command},
		LastSequence: request.LastSequence,
		Timeout:      request.TimeLimit,
		Unbounded:    true,
		Permit:       request.Permit,
	}
	if folder := strings.TrimSpace(request.TestScratch); folder != "" {
		checkRequest.Env = append(checkRequest.Env, TestScratchVariable+"="+folder)
	}
	if request.Restrictions != nil {
		restrictions := *request.Restrictions
		checkRequest.Wrap = restrictions.Wrap
		// The developer's temporary files go where it may write, as its own
		// session's do.
		if folder := strings.TrimSpace(restrictions.Scratch); folder != "" {
			checkRequest.Env = append(checkRequest.Env, "TMPDIR="+folder)
		}
	}
	return checkRequest
}

// RunEnvironmentCheckWith runs one environment's command, its check or its
// test, through runner and returns its result.
func RunEnvironmentCheckWith(ctx context.Context, runner CheckRun, request EnvironmentRequest, sink func(execution.Event) error) (Result, uint64, error) {
	if strings.TrimSpace(request.Command) == "" {
		return Result{}, request.LastSequence, errors.New("an environment's check command cannot be empty")
	}
	results, last, err := runner.Run(ctx, request.CheckRequest(), sink)
	if len(results) == 0 {
		if err == nil {
			err = errors.New("the environment's check produced no result")
		}
		return Result{}, last, err
	}
	return results[0], last, err
}
