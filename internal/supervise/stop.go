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
	"errors"
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

// lookProcess, collect and forceKill are the operating system's answers, replaceable so a
// test drives a stop against a process table it controls.
var (
	lookProcess = runstate.LookProcess
	collect     = reap
	forceKill   = killTree
)

// StopHolder asks the process holding a lease to stop, waits for the lease to
// go, and then for the process itself to be gone. It reports a wait that ran
// out in the detail rather than as a failure, because the signal was sent and
// the process is stopping on its own schedule; what the caller cannot say is
// that it has stopped.
func StopHolder(ctx context.Context, pid int, held func() (bool, error)) (Stopped, error) {
	return stopHolder(ctx, pid, held, StopGrace, nil)
}

// ForceWait bounds how long a forced stop waits for the killed process to let
// go of its lease and leave. A killed process cannot delay its end, so this is
// the operating system's time to release what it held and nothing more.
const ForceWait = 30 * time.Second

// Settle is what a forced stop does once the killed process is gone and before
// the stop returns: what the process could not do for itself because it was
// killed, such as recording a stop on the runs it was working. forced is the
// process as it was read before it was asked to stop, and at is when it was
// killed. What it returns is said on the stop, after what the stop did.
type Settle func(ctx context.Context, pid int, forced runstate.ProcessLook, at time.Time) (string, error)

// StopHolderForcing is StopHolder for a child that must not be left stopping on
// its own schedule: the scheduler, whose lease is what every build move waits
// on, so a scheduler that hangs while stopping would hold the product on the
// old build with nothing anybody could do about it. Where the process is still
// there when the wait runs out, it is killed with everything it started, and
// settle is run once it is gone. A wait cut short by ctx forces nothing.
func StopHolderForcing(ctx context.Context, pid int, held func() (bool, error), settle Settle) (Stopped, error) {
	return stopHolder(ctx, pid, held, StopGrace, &forcing{kill: forceKill, wait: ForceWait, settle: settle})
}

// forcing is how a stop whose wait ran out ends the process: kill ends it and
// what it started, wait bounds the time it is then given to be gone, and settle
// is run once it is.
type forcing struct {
	kill   func(pid int) error
	wait   time.Duration
	settle Settle
}

func stopHolder(ctx context.Context, pid int, held func() (bool, error), grace time.Duration, force *forcing) (Stopped, error) {
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
	deadline := time.Now().Add(grace)
	for {
		holding, err := held()
		if err != nil {
			return stopped, fmt.Errorf("ask whether pid %d has let go of its lease: %w", pid, err)
		}
		if !holding {
			break
		}
		if time.Now().After(deadline) {
			if force != nil {
				return force.end(ctx, stopped, before, held, fmt.Sprintf("it was still holding its lease %s after being asked to stop", grace))
			}
			stopped.Detail = fmt.Sprintf("it was still holding its lease %s after being asked to stop, so it is stopping on its own schedule", grace)
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
			if force != nil {
				return force.end(ctx, stopped, before, held, fmt.Sprintf("it let go of its lease and was still running %s after being asked to stop", grace))
			}
			stopped.Detail = fmt.Sprintf("it let go of its lease and was still running %s after being asked to stop, so it is stopping on its own schedule", grace)
			return stopped, nil
		}
		if !pause(ctx) {
			stopped.Detail = "it let go of its lease, and the wait for it to exit was interrupted; it is stopping on its own schedule"
			return stopped, nil
		}
	}
}

// end kills a process that did not stop when asked, waits within the force's
// bound for it to let go of its lease and leave, and settles what it left. The
// stop is reported as forced, with when and why, whatever settle then says; a
// process still there after the kill's own wait is said in the detail, and the
// caller starts nothing in its place.
func (f *forcing) end(ctx context.Context, stopped Stopped, before runstate.ProcessLook, held func() (bool, error), why string) (Stopped, error) {
	pid := stopped.PID
	at := time.Now()
	if err := f.kill(pid); err != nil {
		stopped.Detail = fmt.Sprintf("%s, and it could not be forced to stop: %v; it is asked again at the next look", why, err)
		return stopped, nil
	}
	stopped.ForcedAt = at
	stopped.Forced = fmt.Sprintf("%s, so the supervisor forced it and everything it had started to stop at %s", why, at.Local().Format("2006-01-02 15:04:05 MST"))
	deadline := at.Add(f.wait)
	for {
		holding, err := held()
		if err != nil {
			return stopped, fmt.Errorf("ask whether pid %d has let go of its lease after it was forced to stop: %w", pid, err)
		}
		if !holding && gone(pid, before) {
			break
		}
		if time.Now().After(deadline) {
			stopped.Detail = fmt.Sprintf("it was forced to stop and was still there %s later, so nothing is started in its place until it is gone", f.wait)
			return stopped, nil
		}
		// A context already cancelled must not cut this short: the process has
		// been killed, and what it left has to be settled before anything takes
		// its place. The wait is bounded by the force's own deadline instead.
		time.Sleep(stopInterval)
	}
	if f.settle != nil {
		said, err := f.settle(context.WithoutCancel(ctx), pid, before, at)
		if said != "" {
			stopped.Forced += "; " + said
		}
		if err != nil {
			return stopped, fmt.Errorf("settle what pid %d left when it was forced to stop: %w", pid, err)
		}
	}
	return stopped, nil
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
		// A child of this process that has exited but cannot be collected yet is
		// still in the process table, so it is not gone until a later look
		// collects it; the caller's wait is bounded by the force's deadline.
		if _, err := collect(pid); errors.Is(err, errNotCollectable) {
			return false
		}
		return true
	}
	return false
}

// errNotCollectable is what reap says of a child of this process that the
// system does not yet offer to be collected.
var errNotCollectable = errors.New("the child has not finished exiting, so it cannot be collected yet")

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
