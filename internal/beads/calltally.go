package beads

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// What a command's tracker calls cost, told as they end.
//
// bd lets one process at a time into its store, reads included, so a call
// waits for every other bd process on the machine before it is answered. On a
// copy of the store over 1 GB with nothing else running, every read the
// reconcile sweep makes answers in under 7 seconds, and the same reads on the
// live store have waited minutes behind other processes
// (docs/diagnoses/yoyodyne-ifd-428-126-4-14-1-reconcile-tracker-waits.md). A
// command bounded as a whole, as the maintenance pass bounds `yoyo reconcile`,
// ends as timed out with nothing said about where its time went unless each
// call is accounted for as it ends. A CallTally is that account.

// TrackerCall is one bd invocation, once it has ended.
type TrackerCall struct {
	// Call is what it was asked, in words.
	Call string
	// Took is how long it ran, and Limit the limit it was given.
	Took  time.Duration
	Limit time.Duration
	// StoppedAtLimit says it was stopped at that limit, or at its caller's
	// deadline, without an answer.
	StoppedAtLimit bool
}

// CallObserver is told of every bd invocation a client makes, once it has
// ended. It is satisfied by *CallTally.
type CallObserver interface {
	ObserveCall(TrackerCall)
}

// slowestKept is how many of the longest calls a tally names.
const slowestKept = 3

// CallTally adds up a command's tracker calls, and says each slow one as it
// ends. It is safe for concurrent use, since a command's steps may call the
// tracker from several goroutines.
type CallTally struct {
	// Slow is where each call that ran SlowAfter or longer, or was stopped at
	// its limit, is said as soon as it ends; nil says nothing until Describe.
	Slow      io.Writer
	SlowAfter time.Duration

	mu      sync.Mutex
	calls   int
	took    time.Duration
	stopped int
	slowest []TrackerCall
}

// ObserveCall adds one call to the tally.
func (t *CallTally) ObserveCall(call TrackerCall) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls++
	t.took += call.Took
	if call.StoppedAtLimit {
		t.stopped++
	}
	t.slowest = append(t.slowest, call)
	sort.SliceStable(t.slowest, func(i, j int) bool { return t.slowest[i].Took > t.slowest[j].Took })
	if len(t.slowest) > slowestKept {
		t.slowest = t.slowest[:slowestKept]
	}
	if t.Slow != nil && (call.StoppedAtLimit || (t.SlowAfter > 0 && call.Took >= t.SlowAfter)) {
		fmt.Fprintln(t.Slow, call.Says())
	}
}

// Says is one call as a line: what it asked, and how long it took or that it
// was stopped without an answer.
func (c TrackerCall) Says() string {
	if c.StoppedAtLimit {
		return fmt.Sprintf("the tracker did not answer %s: it was stopped after %s, at its limit of %s", c.Call, roundedSpan(c.Took), roundedSpan(c.Limit))
	}
	return fmt.Sprintf("the tracker took %s to answer %s", roundedSpan(c.Took), c.Call)
}

// Describe is the whole tally as one line: how many calls, how long they took
// together, how many were stopped without an answer, and the longest.
func (t *CallTally) Describe() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.calls == 0 {
		return "asked the tracker nothing"
	}
	line := fmt.Sprintf("asked the tracker %d time(s), which took %s in all", t.calls, roundedSpan(t.took))
	if t.stopped > 0 {
		line += fmt.Sprintf(", %d of them stopped at their limit without an answer", t.stopped)
	}
	longest := make([]string, 0, len(t.slowest))
	for _, call := range t.slowest {
		longest = append(longest, fmt.Sprintf("%s (%s)", call.Call, roundedSpan(call.Took)))
	}
	return line + "; the longest: " + strings.Join(longest, ", ")
}

// observedCall names a bd invocation for a tally. It is describeCall, with the
// statuses a listing was narrowed to, since a listing of closed work and one of
// open work are different reads.
func observedCall(args []string) string {
	call := describeCall(args)
	if args[0] != "list" && args[0] != "ready" {
		return call
	}
	if args[0] == "ready" {
		return "listing the work items ready to start"
	}
	for _, arg := range args[1:] {
		switch {
		case strings.HasPrefix(arg, "--status="):
			return call + " with status " + strings.TrimPrefix(arg, "--status=")
		case arg == everyStatus:
			return call + " of every status"
		}
	}
	return call + " not closed"
}
