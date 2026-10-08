package supervise

// Stopping one process that holds a lease, and waiting for it to be gone.
//
// Every child answers whether it is running from its own lease, so the wait
// after a stop signal is on that lease first: a child that has let go of its
// lease has stopped doing anything the supervisor starts it for. It is not yet
// gone, though. A process can let its lease go a moment before it exits, and a
// process that has exited stays in the process table until its parent collects
// it — which for every part the supervisor started is the supervisor, since the
// launcher lets the child go without waiting on it. Left uncollected, a stopped
// part reads as still running to everything that asks about its process, which
// is how the landing records came to list dozens of Slack sinks on old builds
// when one was running. So the stop goes on to the process: it collects it where
// this process is its parent, and waits, within the same bound, for it to leave
// the process table where it is not.

import (
	"context"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/shutdown"
)

// StopGrace bounds how long a stop waits for one child to let go of its lease.
// It is the grace every harness process gives itself to stop and a margin over
// it, because the scheduler asked to stop cancels the runs it hosts and waits
// out each one's teardown, and a stop that gave up before the process did
// would report a child running that was on its way down.
const StopGrace = shutdown.Grace + 30*time.Second

// stopInterval is how often the lease is asked again while waiting.
const stopInterval = 250 * time.Millisecond

// lookProcess and collect are the operating system's answers, replaceable so a
// test drives a stop against a process table it controls.
var (
	lookProcess = runstate.LookProcess
	collect     = reap
)

// StopHolder asks the process holding a lease to stop, waits for the lease to
// go, and then for the process itself to be gone. It reports a wait that ran
// out in the detail rather than as a failure, because the signal was sent and
// the process is stopping on its own schedule; what the caller cannot say is
// that it has stopped.
func StopHolder(ctx context.Context, pid int, held func() (bool, error)) (Stopped, error) {
	if pid <= 0 {
		return Stopped{}, fmt.Errorf("the record names no process to stop")
	}
	// Which process it is, read before it is asked to stop, so the process
	// waited for afterwards is that one and not another that took its number.
	before, _ := lookProcess(pid)
	if err := terminate(pid); err != nil {
		return Stopped{}, fmt.Errorf("ask pid %d to stop: %w", pid, err)
	}
	stopped := Stopped{WasRunning: true, PID: pid}
	deadline := time.Now().Add(StopGrace)
	for {
		holding, err := held()
		if err != nil {
			return stopped, fmt.Errorf("ask whether pid %d has let go of its lease: %w", pid, err)
		}
		if !holding {
			break
		}
		if time.Now().After(deadline) {
			stopped.Detail = fmt.Sprintf("it was still holding its lease %s after being asked to stop, so it is stopping on its own schedule", StopGrace)
			return stopped, nil
		}
		if !pause(ctx) {
			stopped.Detail = "the wait for it to stop was interrupted; it was asked, and is stopping on its own schedule"
			return stopped, nil
		}
	}
	for {
		if gone(pid, before) {
			return stopped, nil
		}
		if time.Now().After(deadline) {
			stopped.Detail = fmt.Sprintf("it let go of its lease and was still running %s after being asked to stop, so it is stopping on its own schedule", StopGrace)
			return stopped, nil
		}
		if !pause(ctx) {
			stopped.Detail = "it let go of its lease, and the wait for it to exit was interrupted; it is stopping on its own schedule"
			return stopped, nil
		}
	}
}

// gone reports whether the process asked to stop has left: nothing has its
// number, another process does, or it has exited — collected here where this
// process is its parent, and left to its own parent otherwise, since an exited
// process runs nothing. A platform that cannot be asked has only the lease's
// answer, which has already been given.
func gone(pid int, before runstate.ProcessLook) bool {
	look, err := lookProcess(pid)
	if err != nil || !look.Exists {
		return true
	}
	if !before.StartedAt.IsZero() && !look.StartedAt.IsZero() && !look.StartedAt.Equal(before.StartedAt) {
		return true
	}
	if look.Exited {
		_, _ = collect(pid)
		return true
	}
	return false
}

func pause(ctx context.Context) bool {
	timer := time.NewTimer(stopInterval)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
