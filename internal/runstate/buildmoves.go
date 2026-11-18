package runstate

import (
	"time"
)

// BuildMove is one move of a watch session onto a build deployed over it, as
// the watch log records it: the session that found the deploy, when it first
// held work back for the move, when it restarted, and when the session that
// replaced it wrote its first line.
//
// What the move cost is the time between the first two of those last three:
// from the moment the session first declined a pull into a free seat, did not
// start a recurring pass or a merge queue pass, or stopped a run, until the
// session on the new build was working. A session that held nothing back
// before it restarted cost only the restart itself.
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
}

// Cost is the time the move held work back: from HeldBack to Resumed, or to
// now for a move whose next session has not yet written a line.
func (m BuildMove) Cost(now time.Time) time.Duration {
	end := m.Resumed
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
// the drain that line carries, and when the move ended from the first line of
// any later session.
//
// A drain recorded before sessions marked when they first held work back
// carries no such moment. For one of those the move is reckoned from the end
// of its drain bound where the bound ran out, because from then the session
// pulled nothing into a free seat, and otherwise from the restart.
func BuildMoves(transitions []WatchTransition) []BuildMove {
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
		moves = append(moves, move)
	}
	return moves
}
