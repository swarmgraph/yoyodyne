package runstate

import (
	"sort"
	"time"
)

// BuildMove is one move of a watch session onto a build deployed over it, as
// the watch log records it: the session that found the deploy, when it first
// held work back for the move, when it restarted, and when the session that
// replaced it wrote its first line.
//
// What the move cost is from the moment the session first declined a pull
// into a free seat, did not start a recurring pass, or stopped a run, until
// the session on the new build was working — and, where the restart set a
// merge queue's work aside, until that queue was working again too: until a
// pass on the new build started a candidate's checks or review, landed or
// handed back an entry, or found nothing to move. A session that held nothing
// back before it restarted cost only the restart itself.
type BuildMove struct {
	SessionID string
	// Build is the build the session was running, and NextBuild the one the
	// session that replaced it runs, where either was recorded.
	Build     string
	NextBuild string
	// Found is when the session found the deploy, where it recorded it.
	Found time.Time
	// HeldBack is when the session first held work back for the move.
	HeldBack time.Time
	// Restarted is the session's last line, and Resumed the first line of the
	// session that replaced it; zero where none has been written yet.
	Restarted time.Time
	Resumed   time.Time
	// QueuesHeld is the target branches of the merge queues whose work the
	// restart set aside, and QueuesMoving when the last of them was working
	// again on the new build; zero where one has not been yet.
	QueuesHeld   []string
	QueuesMoving time.Time
}

// End is when the move stopped costing anything: when the session on the new
// build was working, and every merge queue the restart set aside was working
// again. Zero is a move still costing something.
func (m BuildMove) End() time.Time {
	if m.Resumed.IsZero() {
		return time.Time{}
	}
	if len(m.QueuesHeld) == 0 {
		return m.Resumed
	}
	if m.QueuesMoving.IsZero() {
		return time.Time{}
	}
	if m.QueuesMoving.After(m.Resumed) {
		return m.QueuesMoving
	}
	return m.Resumed
}

// Cost is the time the move held work back: from HeldBack to End, or to now
// for a move that has not ended.
func (m BuildMove) Cost(now time.Time) time.Duration {
	end := m.End()
	if end.IsZero() {
		end = now
	}
	if end.Before(m.HeldBack) {
		return 0
	}
	return end.Sub(m.HeldBack)
}

// BuildMoves is every move onto a new build the watch log records, in the
// order the log has them. A move is the line a session wrote as it stopped to
// restart into a deployed build; when it first held work back is read from
// the drain that line carries, and when the session on the new build was
// working from the first line of any later session. queues is each merge
// queue's recent passes by target branch, oldest first: a queue with a pass
// that ended for the restart while the session drained had its work set
// aside, and was working again at the first pass after the restart that got
// it moving.
//
// A drain recorded before sessions marked when they first held work back
// carries no such moment. For one of those the move is reckoned from the end
// of its drain bound where the bound ran out, because from then the session
// pulled nothing into a free seat, and otherwise from the restart.
func BuildMoves(transitions []WatchTransition, queues map[string][]MergeQueuePass) []BuildMove {
	var moves []BuildMove
	for index, transition := range transitions {
		if transition.State != WatchStopped || !transition.Restarting {
			continue
		}
		move := BuildMove{
			SessionID: transition.SessionID,
			Build:     transition.Build,
			HeldBack:  transition.At,
			Restarted: transition.At,
		}
		if drain := transition.Draining; drain != nil {
			move.Found = drain.Since
			switch {
			case !drain.PausedSince.IsZero():
				move.HeldBack = drain.PausedSince
			case drain.BoundReached && !drain.Until.IsZero() && drain.Until.Before(transition.At):
				move.HeldBack = drain.Until
			}
		}
		for _, later := range transitions[index+1:] {
			if later.SessionID != transition.SessionID && !later.At.Before(transition.At) {
				move.Resumed, move.NextBuild = later.At, later.Build
				break
			}
		}
		move.QueuesHeld, move.QueuesMoving = queuesAcross(move, queues)
		moves = append(moves, move)
	}
	return moves
}

// queuesAcross is the merge queues whose work a move set aside, sorted, and
// when the last of them was working again; zero where one has not been yet.
// A pass ended for the restart from when the session found the deploy until a
// minute after it restarted — the restart gives the passes still in flight
// half a minute to end — is the restart setting the queue's work aside.
func queuesAcross(move BuildMove, queues map[string][]MergeQueuePass) ([]string, time.Time) {
	from := move.HeldBack
	if !move.Found.IsZero() && move.Found.Before(from) {
		from = move.Found
	}
	until := move.Restarted.Add(time.Minute)
	var held []string
	var moving time.Time
	stillHeld := false
	for queue, passes := range queues {
		setAside := false
		for _, pass := range passes {
			if pass.ForRestart && !pass.At.Before(from) && !pass.At.After(until) {
				setAside = true
				break
			}
		}
		if !setAside {
			continue
		}
		held = append(held, queue)
		var again time.Time
		for _, pass := range passes {
			if pass.Moved != nil && !pass.Moved.Before(move.Restarted) {
				again = *pass.Moved
				break
			}
		}
		if again.IsZero() {
			stillHeld = true
		} else if again.After(moving) {
			moving = again
		}
	}
	sort.Strings(held)
	if stillHeld {
		return held, time.Time{}
	}
	return held, moving
}
