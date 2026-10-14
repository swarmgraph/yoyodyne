package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// The contexts a tracker call is made under outside its caller's own leave the
// tracker client's limits to decide: each bd call is given thirty seconds and a
// stopped write is read back and asked again, so a bound under a few of those
// calls is the caller deciding first, which is how notes, blockers and settled
// findings came to be recorded as tracker timeouts on a store that took several
// seconds to answer.
func TestTrackerCallsAreBoundedByTheTrackersOwnLimits(t *testing.T) {
	t.Parallel()
	const atLeast = 3 * 30 * time.Second
	for name, open := range map[string]func() (context.Context, context.CancelFunc){
		"a call under its caller's context": func() (context.Context, context.CancelFunc) { return trackerCallContext(context.Background()) },
		"a write outliving its caller":      func() (context.Context, context.CancelFunc) { return trackerRecordContext(context.Background()) },
		"a run's call":                      func() (context.Context, context.CancelFunc) { return runTrackerContext("run-1", "yoyodyne-1") },
	} {
		ctx, cancel := open()
		deadline, bounded := ctx.Deadline()
		cancel()
		if !bounded || time.Until(deadline) < atLeast {
			t.Errorf("%s is bounded at %v from now, want at least %v", name, time.Until(deadline).Round(time.Second), atLeast)
		}
	}
}

// A write that outlives its caller keeps going when the caller is cancelled, as
// recordContext's writes do; a call under its caller's context does not.
func TestATrackerWriteOutlivingItsCallerIsNotCancelledWithIt(t *testing.T) {
	t.Parallel()
	caller, cancelCaller := context.WithCancel(context.Background())
	detached, stopDetached := trackerRecordContext(caller)
	defer stopDetached()
	attached, stopAttached := trackerCallContext(caller)
	defer stopAttached()
	cancelCaller()
	if detached.Err() != nil {
		t.Errorf("trackerRecordContext() was cancelled with its caller: %v", detached.Err())
	}
	if attached.Err() == nil {
		t.Error("trackerCallContext() outlived its caller's cancellation")
	}
}

// A run's tracker call says which run it is for.
func TestARunsTrackerCallNamesTheRun(t *testing.T) {
	t.Parallel()
	ctx, cancel := runTrackerContext("run-24448281", "yoyodyne-ifd.429.41")
	defer cancel()
	if got, want := beads.Caller(ctx), "developer run run-24448281 for yoyodyne-ifd.429.41"; got != want {
		t.Fatalf("caller = %q, want %q", got, want)
	}
}
