package runstate

// Whether a process a record names is still that process.
//
// A process id is only an address. When the process behind one exits, the id
// goes on answering in two ways that are not the process: it stays taken, as an
// exited process its parent has not yet collected, until the parent waits for
// it; and once it is collected the operating system hands the same number to the
// next process started, which on a machine starting thousands of commands an
// hour is soon. Either way a record that asks only whether something has the id
// reads a part that is gone as one still running. That is how the landing records
// came to list dozens of Slack sinks on old builds when exactly one was running:
// every sink the supervisor stopped on a deploy stayed behind as an exited
// process nothing collected.
//
// So a look says three things: whether anything has the id, whether that is an
// exited process waiting to be collected, and when it started, where the
// platform tells. A record written as its process started is that process only
// while the process now behind the id started no later than the record.

import "time"

// RecordedStartSlack is how much later than the record a process may have
// started and still be the process that wrote it. A record is written after its
// process started, so a process starting later is another one with the same id;
// the slack covers a clock read at a coarser grain than the record's.
const RecordedStartSlack = 2 * time.Second

// ProcessLook is what the operating system says about one process id.
type ProcessLook struct {
	// Exists is a process having the id, exited or not.
	Exists bool
	// Exited is that process having exited without its parent collecting it. It
	// runs nothing and holds nothing; only its parent can make it go away.
	Exited bool
	// StartedAt is when it started, and zero where the platform does not say.
	StartedAt time.Time
}

// Running is a process that has the id and has not exited.
func (l ProcessLook) Running() bool { return l.Exists && !l.Exited }

// Is reports whether the process behind the id can be the one a record written
// at recorded names: it exists, and it did not start after the record was
// written. Where the platform gives no start time, existing is all there is to
// go on.
func (l ProcessLook) Is(recorded time.Time) bool {
	if !l.Exists {
		return false
	}
	if l.StartedAt.IsZero() || recorded.IsZero() {
		return true
	}
	return !l.StartedAt.After(recorded.Add(RecordedStartSlack))
}
