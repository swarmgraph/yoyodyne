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
	moves := BuildMoves(transitions, nil)
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

// A move whose restart set a merge queue's work aside is not over when the
// session on the new build writes its first line: it ends when that queue is
// working again. Here, as on November 17, the session restarts at 16:52, the
// new session writes at 16:53, and the queue only waits until 17:24, when a
// pass starts the candidate's checks; the move costs all of it. A queue that
// was only waiting on the forge across the move, with no pass ended for the
// restart, is not counted.
func TestABuildMoveRunsUntilTheQueueItSetAsideIsWorkingAgain(t *testing.T) {
	t.Parallel()

	pst := time.FixedZone("PST", -8*60*60)
	at := func(hour, minute int) time.Time { return time.Date(2026, 11, 17, hour, minute, 0, 0, pst) }
	transitions := []WatchTransition{
		{SessionID: "watch-old", State: WatchWatching, At: at(16, 0)},
		{SessionID: "watch-old", State: WatchStopped, At: at(16, 52), Restarting: true,
			Draining: &WatchDrain{Since: at(16, 26), BoundSeconds: 900, Until: at(16, 41), BoundReached: true, PausedSince: at(16, 50)}},
		{SessionID: "watch-new", State: WatchWatching, At: at(16, 53)},
	}
	checksBegan := at(17, 24)
	queues := map[string][]MergeQueuePass{
		"main": {
			{At: at(16, 52), Outcome: MergeQueuePassWaiting, ForRestart: true},
			{At: at(16, 54), Outcome: MergeQueuePassWaiting, Says: "waiting"},
			{At: at(17, 10), Outcome: MergeQueuePassWaiting, Says: "waiting"},
			{At: at(17, 40), Outcome: MergeQueuePassWaiting, Moved: &checksBegan},
		},
		"release": {
			{At: at(16, 30), Outcome: MergeQueuePassWaiting, Says: "the forge holds its merge"},
		},
	}
	moves := BuildMoves(transitions, queues)
	if len(moves) != 1 {
		t.Fatalf("BuildMoves() = %#v, want one move", moves)
	}
	move := moves[0]
	if len(move.QueuesHeld) != 1 || move.QueuesHeld[0] != "main" || !move.QueuesMoving.Equal(checksBegan) {
		t.Fatalf("move = %#v, want only the main queue held, working again at %s", move, checksBegan)
	}
	if got, want := move.Cost(at(18, 0)), 34*time.Minute; got != want {
		t.Fatalf("Cost() = %s, want %s: from 16:50 PST until the queue was working again at 17:24 PST", got, want)
	}

	// Before the queue has moved again, the move is still costing.
	queues["main"] = queues["main"][:3]
	still := BuildMoves(transitions, queues)[0]
	if !still.End().IsZero() || still.Cost(at(17, 20)) != 30*time.Minute {
		t.Fatalf("move = %#v costing %s, want it still costing while the queue has not moved", still, still.Cost(at(17, 20)))
	}
}

// The last move is counted from a restart line, and also from a session that
// began on a different build from the one before it: a move the supervisor
// made by stopping a session stuck winding down leaves "the scheduler was
// cancelled" rather than a restart line, and a session counting from the
// restart line before it would move again minutes later, as the scheduler did
// at 22:34 PST on November 19, 2026, eighteen minutes after such a move. A
// session that came back on the same build, and a session asking as it starts,
// are each read as they are.
func TestLastBuildMoveCountsAMoveTheSessionDidNotMakeItself(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 11, 20, 2, 55, 0, 0, time.UTC)
	restarted := []WatchTransition{
		{SessionID: "watch-one", State: WatchWatching, At: at.Add(-time.Hour), Build: "aaa"},
		{SessionID: "watch-one", State: WatchStopped, At: at, Build: "aaa", Restarting: true},
		{SessionID: "watch-two", State: WatchWatching, At: at.Add(time.Minute), Build: "bbb"},
	}
	if got := LastBuildMove(restarted, "", time.Time{}); !got.Equal(at.Add(time.Minute)) {
		t.Fatalf("LastBuildMove() = %s, want the first line of the session on the new build, %s", got, at.Add(time.Minute))
	}

	cancelled := append(restarted,
		WatchTransition{SessionID: "watch-two", State: WatchWatching, At: at.Add(3 * time.Hour), Build: "bbb", Draining: &WatchDrain{Since: at.Add(3 * time.Hour), BoundSeconds: 900, Until: at.Add(3*time.Hour + 15*time.Minute)}},
		WatchTransition{SessionID: "watch-two", State: WatchStopped, At: at.Add(3*time.Hour + 20*time.Minute), Build: "bbb", Reason: "the scheduler was cancelled"},
		WatchTransition{SessionID: "watch-three", State: WatchWatching, At: at.Add(3*time.Hour + 21*time.Minute), Build: "ccc"},
	)
	if got, want := LastBuildMove(cancelled, "", time.Time{}), at.Add(3*time.Hour+21*time.Minute); !got.Equal(want) {
		t.Fatalf("LastBuildMove() = %s, want the session the supervisor started on the new build, %s", got, want)
	}

	sameBuild := append(cancelled,
		WatchTransition{SessionID: "watch-three", State: WatchStopped, At: at.Add(4 * time.Hour), Build: "ccc", Reason: "the scheduler was cancelled"},
		WatchTransition{SessionID: "watch-four", State: WatchWatching, At: at.Add(4*time.Hour + time.Minute), Build: "ccc"},
	)
	if got, want := LastBuildMove(sameBuild, "", time.Time{}), at.Add(3*time.Hour+21*time.Minute); !got.Equal(want) {
		t.Fatalf("LastBuildMove() = %s, want a session back on the same build not counted as a move, %s", got, want)
	}

	starting := at.Add(5 * time.Hour)
	if got := LastBuildMove(sameBuild, "ddd", starting); !got.Equal(starting) {
		t.Fatalf("LastBuildMove() = %s, want a session starting on a new build to count its own start, %s", got, starting)
	}
	if got, want := LastBuildMove(sameBuild, "ccc", starting), at.Add(3*time.Hour+21*time.Minute); !got.Equal(want) {
		t.Fatalf("LastBuildMove() = %s, want a session starting on the same build to count the last move, %s", got, want)
	}
	if got := LastBuildMove(nil, "ddd", starting); !got.IsZero() {
		t.Fatalf("LastBuildMove() = %s, want nothing for a log that records no session", got)
	}
}
