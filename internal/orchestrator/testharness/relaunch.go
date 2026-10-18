package testharness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
)

// BaseTime is the instant every usage-limit test starts from, so a recorded
// deadline can be compared against an exact expectation. It trails the real
// clock slightly because the check runner timestamps its own events for real,
// and durable state refuses an update older than the run's start.
var BaseTime = time.Now().UTC().Add(-time.Minute).Truncate(time.Second)

// PausingClock is a clock the test moves by hand, so a pause can be driven to
// its deadline without spending the time. Sleeping advances it by exactly the
// span it was asked to wait and records that span, which is what makes "the run
// waited out the deadline, and never retried before it" checkable rather than
// assumed. Its time is Instant rather than Now, because Now is the method the
// pipeline reads it through.
type PausingClock struct {
	Instant time.Time
	Slept   []time.Duration
	// OnSleep observes durable state at the moment the wait begins, which is how
	// a test proves the deadline was recorded before the waiting started rather
	// than after it.
	OnSleep func()
}

func (c *PausingClock) Now() time.Time { return c.Instant }

func (c *PausingClock) Sleep(_ context.Context, duration time.Duration) error {
	c.Slept = append(c.Slept, duration)
	if c.OnSleep != nil {
		c.OnSleep()
	}
	c.Instant = c.Instant.Add(duration)
	return nil
}

// Waited is the whole span this clock was asked to sleep for. A wait is taken in
// slices short enough that an operator releasing it is noticed while the process
// is still asleep, so what a test can state about a wait is how long it lasted
// rather than how many pieces it arrived in.
func (c *PausingClock) Waited() time.Duration {
	var total time.Duration
	for _, slice := range c.Slept {
		total += slice
	}
	return total
}

// Waiting wires a pipeline to a hand-driven clock and the two pause bounds, so
// a test states exactly how long the harness may wait and how much of that it
// will spend holding this process open.
func Waiting(pipeline orchestrator.Pipeline, clock *PausingClock, maximum, inProcess time.Duration) orchestrator.Pipeline {
	pipeline.Clock = clock
	pipeline.Sleep = clock.Sleep
	pipeline.Config.Execution.UsageLimitMaxPause = config.Duration(maximum)
	pipeline.Config.Execution.UsageLimitInProcessPause = config.Duration(inProcess)
	return pipeline
}

// RefusingBackend refuses the developer's first refusals invocations and serves
// the work afterwards. refuse decorates the errored result the provider returns,
// which is what distinguishes one kind of refusal from another; everything else
// about a refused attempt — the error, the failed process, and the session it
// had already established — is the same whichever refused it.
func RefusingBackend(refusals int, refuse func(backend.RunResult) backend.RunResult, verdicts ...string) *orchestratortest.Backend {
	provider := &orchestratortest.Backend{DeveloperSession: "developer-session", ReviewerSession: "reviewer-session"}
	refused, reviews := 0, 0
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		switch request.Role {
		case domain.RoleDeveloper:
			if refused < refusals {
				refused++
				return refuse(backend.RunResult{
					Backend:   domain.BackendClaudeCode,
					SessionID: provider.DeveloperSession,
					IsError:   true,
					Process:   execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1},
					LastEvent: request.LastSequence,
				}), nil
			}
			if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
				return backend.RunResult{}, err
			}
			return backend.RunResult{
				Backend:       domain.BackendClaudeCode,
				SessionID:     provider.DeveloperSession,
				ResolvedModel: DeveloperResolved,
				FinalText:     "implemented the work item",
				Process:       execution.ProcessResult{Status: execution.ProcessSucceeded},
				LastEvent:     request.LastSequence,
			}, nil
		case domain.RoleReviewer:
			verdict := verdicts[len(verdicts)-1]
			if reviews < len(verdicts) {
				verdict = verdicts[reviews]
			}
			reviews++
			return backend.RunResult{
				Backend:       domain.BackendClaudeCode,
				SessionID:     provider.ReviewerSession,
				ResolvedModel: ReviewerResolved,
				FinalText:     verdict,
				Process:       execution.ProcessResult{Status: execution.ProcessSucceeded},
				LastEvent:     request.LastSequence,
			}, nil
		default:
			return backend.RunResult{}, fmt.Errorf("unexpected role %q", request.Role)
		}
	}
	return provider
}

// ConnectionClosedMessage is what the provider wrote on the run that died
// developing yoyodyne-ifd.68.2, kept whole so the relaunch a test observes is
// the one that message earns.
const ConnectionClosedMessage = "API Error: Connection closed mid-response. The response above may be incomplete."

// TransientDeathBackend kills the developer's first deaths invocations the way a
// provider that dropped the connection does, and serves the work afterwards. The
// death carries the session the dead attempt had already established, because
// that is what the relaunch continues in.
func TransientDeathBackend(deaths int, verdicts ...string) *orchestratortest.Backend {
	return RefusingBackend(deaths, func(result backend.RunResult) backend.RunResult {
		result.StopReason = "api_error"
		result.FinalText = ConnectionClosedMessage
		result.TransientFailure = &backend.TransientFailure{Detail: "api_error: " + ConnectionClosedMessage}
		return result
	}, verdicts...)
}
