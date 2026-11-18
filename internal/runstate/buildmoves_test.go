package runstate

import (
	"testing"
	"time"
)

// Each move onto a new build is reckoned from the moment its session first
// held work back to the first line of the session that replaced it; a drain
// recorded before sessions marked that moment is reckoned from the end of its
// bound, and a move whose next session has not written yet runs to now.
func TestBuildMovesReckonEachMoveFromWhenWorkWasFirstHeldBack(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 11, 17, 20, 0, 0, 0, time.UTC)
	transitions := []WatchTransition{
		{SessionID: "watch-one", State: WatchWatching, At: at, Build: "aaa"},
		{SessionID: "watch-one", State: WatchStopped, At: at.Add(20 * time.Minute), Build: "aaa", Restarting: true,
			Draining: &WatchDrain{Since: at.Add(5 * time.Minute), BoundSeconds: 900, Until: at.Add(20 * time.Minute), PausedSince: at.Add(19 * time.Minute)}},
		{SessionID: "watch-two", State: WatchWatching, At: at.Add(21 * time.Minute), Build: "bbb"},
		// A drain from before the moment was recorded, whose bound ran out.
		{SessionID: "watch-two", State: WatchStopped, At: at.Add(60 * time.Minute), Build: "bbb", Restarting: true,
			Draining: &WatchDrain{Since: at.Add(40 * time.Minute), BoundSeconds: 900, Until: at.Add(55 * time.Minute), BoundReached: true}},
		{SessionID: "watch-three", State: WatchWatching, At: at.Add(61 * time.Minute), Build: "ccc"},
		// An ordinary stop is not a move.
		{SessionID: "watch-three", State: WatchStopped, At: at.Add(70 * time.Minute), Build: "ccc"},
		{SessionID: "watch-four", State: WatchWatching, At: at.Add(80 * time.Minute), Build: "ccc"},
		// A move whose next session has not written yet.
		{SessionID: "watch-four", State: WatchStopped, At: at.Add(90 * time.Minute), Build: "ccc", Restarting: true},
	}
	moves := BuildMoves(transitions)
	if len(moves) != 3 {
		t.Fatalf("BuildMoves() = %#v, want three moves", moves)
	}
	now := at.Add(93 * time.Minute)
	want := []struct {
		cost      time.Duration
		nextBuild string
	}{
		{2 * time.Minute, "bbb"},
		{6 * time.Minute, "ccc"},
		{3 * time.Minute, ""},
	}
	for index, move := range moves {
		if got := move.Cost(now); got != want[index].cost || move.NextBuild != want[index].nextBuild {
			t.Fatalf("move %d = %#v costing %s, want %s onto %q", index, move, got, want[index].cost, want[index].nextBuild)
		}
	}
	if !moves[0].Found.Equal(at.Add(5 * time.Minute)) {
		t.Fatalf("first move found the build at %s, want when its drain began", moves[0].Found)
	}
}
