package orchestrator

import (
	"context"
	"sync"
	"time"
)

// buildMoveCheckEvery is how often a watching session looks, on a timer of its
// own, at whether a move onto a newly installed build has fallen due. The top
// of the loop looks too, but one pull can be far longer than this: a pull
// slowed by the tracker ran fourteen minutes, and the move that fell due inside
// it began only when it ended. The timer keeps the moment the move fell due
// whatever the pull is doing, and the pull asks it before every start it makes
// (moveWatch, Scheduler.Schedule).
const buildMoveCheckEvery = time.Minute

// moveWatch is what the timer found: the first moment it saw a newer build
// installed, which is the moment the session's move onto it fell due, since a
// move is due as soon as a newer build is installed
// (docs/designs/management-and-supervision.md, yoyodyne-ifd.428.225). The timer
// runs on a goroutine of its own and the session's loop reads what it found,
// so both go through the lock. The timer only notices; beginning the move, and
// everything the session says about it, stays on the session's own goroutine.
type moveWatch struct {
	mu    sync.Mutex
	found time.Time
}

// due is the first moment the timer found the move due, and whether it has.
func (w *moveWatch) due() (time.Time, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.found, !w.found.IsZero()
}

// look is one look by the timer, and reports whether the move is due. A
// deployment that cannot be read is left to the top of the loop, which says
// so on the schedule.
func (w *moveWatch) look(deployment ScheduleDeployment, now time.Time) bool {
	w.mu.Lock()
	found := w.found
	w.mu.Unlock()
	if !found.IsZero() {
		return true
	}
	replaced, err := deployment.Replaced()
	if err != nil || !replaced {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.found.IsZero() {
		w.found = now
	}
	return true
}

// watchForMove starts the timer for a watching session that can tell when a
// build has been installed over it, and returns how to stop it. Stopping it
// waits for the timer to end, so nothing it started outlives the session.
func (s Scheduler) watchForMove(ctx context.Context, w *moveWatch) (stop func()) {
	if !s.Watching || s.Deployment == nil {
		return func() {}
	}
	every := s.moveCheckEvery
	if every <= 0 {
		every = buildMoveCheckEvery
	}
	watching, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-watching.Done():
				return
			case <-ticker.C:
			}
			if w.look(s.Deployment, s.now()) {
				return
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// movedInPullSays is what a pull that stops starting work because a move fell
// due inside it says of a decision or continuation it leaves: this session may
// still take it up at its next pull while it winds down, so it is not said to
// be left for the session that replaces it.
const movedInPullSays = "a move onto a newly installed build fell due while this pull was under way, so the pull starts nothing more; the session winds down for the move from its next pull, and it is attempted then or by the session that replaces it"
