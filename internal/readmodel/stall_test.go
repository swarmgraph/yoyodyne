package readmodel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// held is the whole log as one condition, for the cases that have it in hand.
func held(sessions ...runstate.WatchTransition) func() ([]runstate.WatchTransition, error) {
	return func() ([]runstate.WatchTransition, error) { return sessions, nil }
}

// Every reason says whose move it is. This is what makes an unnamed reason
// impossible rather than unlikely: a reason added to the set without an answer
// here would be a state the harness can report and nobody can act on, which is
// the whole failure the taxonomy exists to end.
func TestEveryReasonSaysWhoseMoveItIs(t *testing.T) {
	t.Parallel()
	for _, reason := range Reasons() {
		if strings.TrimSpace(reason.Whose()) == "" {
			t.Fatalf("reason %q says whose move it is: %q", reason, reason.Whose())
		}
	}
	// And nothing outside the set has an answer, so a reason invented at a call
	// site cannot borrow one.
	if Reason("something-nobody-named").Whose() != "" {
		t.Fatalf("a reason outside the taxonomy was given a whose-move")
	}
}

// The order is the order an operator acts in, and each state is named as itself.
// A hold placed over a full machine is still the hold: it is what a person would
// do something about, and the slots are a fact about a harness that is working.
func TestTheStallIsNamedInTheOrderAnOperatorActsIn(t *testing.T) {
	t.Parallel()
	stopped := runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchStopped, At: moment.Add(-time.Hour)}
	idle := runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchIdle, At: moment.Add(-time.Hour)}
	watching := runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchWatching, At: moment.Add(-time.Hour)}

	for _, testCase := range []struct {
		name       string
		conditions Conditions
		want       Reason
	}{
		{
			name: "the operator's hold outranks everything",
			conditions: Conditions{
				OperatorHold: runstate.OperatorHold{HeldAt: moment.Add(-time.Hour)}, OperatorHeld: true,
				IntakeHold: runstate.IntakeHold{HeldAt: moment}, IntakeHeld: true,
				Running: 2, Capacity: 2, Sessions: held(stopped),
			},
			want: ReasonOperatorHold,
		},
		{
			name: "then the intake hold",
			conditions: Conditions{
				IntakeHold: runstate.IntakeHold{HeldAt: moment}, IntakeHeld: true,
				Running: 2, Capacity: 2, Sessions: held(stopped),
			},
			want: ReasonIntakeHold,
		},
		{
			name:       "then a machine with every slot taken",
			conditions: Conditions{Running: 2, Capacity: 2, Sessions: held(stopped)},
			want:       ReasonNoCapacity,
		},
		{
			name:       "a live session choosing nothing is idle, not absent",
			conditions: Conditions{Sessions: held(watching, idle)},
			want:       ReasonSessionIdle,
		},
		{
			name:       "a log whose every session ended is nobody choosing",
			conditions: Conditions{Sessions: held(watching, stopped)},
			want:       ReasonNoWatchSession,
		},
		{
			name:       "a product nobody has ever watched is its own state",
			conditions: Conditions{Sessions: held()},
			want:       ReasonUnwatched,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			stall := WhyNothingStarts(testCase.conditions)
			if stall.Reason != testCase.want {
				t.Fatalf("reason = %q (%q), want %q", stall.Reason, stall.Says, testCase.want)
			}
			if strings.TrimSpace(stall.Says) == "" {
				t.Fatalf("reason %q said nothing", stall.Reason)
			}
		})
	}
}

// A session restarting into a build deployed over it is not idle and not
// absent: it is stopping the runs it hosts and is seconds from coming back, and
// telling an operator to look at it — or to start one — is the chore the
// self-redeploy exists to end. It is named as itself whether the log's last
// word is the drain's bound running out or the stop the session recorded as a
// restart, and it waits on nobody.
func TestASessionRestartingIntoADeployedBuildIsNeitherIdleNorAbsent(t *testing.T) {
	t.Parallel()
	since := moment.Add(-20 * time.Minute)
	drained := runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchIdle, At: moment.Add(-time.Minute),
		Draining: &runstate.WatchDrain{Since: since, BoundSeconds: 900, Until: since.Add(15 * time.Minute), Hosting: 1, BoundReached: true}}
	stall := WhyNothingStarts(Conditions{Sessions: held(drained), Now: moment})
	if stall.Reason != ReasonRedeploying || !strings.Contains(stall.Says, "bounded at 15m0s") || !stall.Since.Equal(since) {
		t.Fatalf("stall = %+v, want the drain named with its bound, since the deploy was found", stall)
	}
	if _, waiting := stall.Waiting(); waiting {
		t.Fatalf("a session restarting on its own was put on the attention line: %+v", stall)
	}
	if strings.Contains(stall.Refusal(), "yoyo work --watch") {
		t.Fatalf("a session on its way back was told to start a session: %q", stall.Refusal())
	}
	// A session still waiting out a promotion past its bound writes nothing while
	// the wait is unchanged, so its bound-reached line is read as the restart for
	// a while past the bound rather than at once.
	overrunAt := drained.Draining.Until.Add(DrainOverrunAfter)
	if stall := WhyNothingStarts(Conditions{Sessions: held(drained), Now: overrunAt.Add(-time.Second)}); stall.Reason != ReasonRedeploying {
		t.Fatalf("stall = %+v, want a bound-reached line shortly past the bound read as the session restarting", stall)
	}
	// But past that it is a session that should have restarted and has not —
	// stuck, or killed while it waited — and not one on its way back.
	if stall := WhyNothingStarts(Conditions{Sessions: held(drained), Now: overrunAt}); stall.Reason != ReasonDrainOverrun {
		t.Fatalf("stall = %+v, want a bound-reached line long past the bound read as a session draining past its bound", stall)
	}
	// A session waiting out a check stage past its bound names when that stage
	// can run to, and is read as restarting until then even past the grace, and
	// says so with the moment named.
	checksUntil := drained.At.Add(DrainOverrunGrace + 10*time.Minute)
	checking := drained
	checking.Draining = &runstate.WatchDrain{Since: since, BoundSeconds: 900, Until: since.Add(15 * time.Minute), Hosting: 1, BoundReached: true, Checking: 1, ChecksUntil: checksUntil}
	if stall := WhyNothingStarts(Conditions{Sessions: held(checking), Now: checksUntil.Add(-time.Minute)}); stall.Reason != ReasonRedeploying ||
		!strings.Contains(stall.Says, "waiting out a check stage") || !strings.Contains(stall.Says, checksUntil.UTC().Format(time.RFC3339)) {
		t.Fatalf("stall = %+v, want a check stage waited out read as the session restarting, with its end named", stall)
	}
	if stall := WhyNothingStarts(Conditions{Sessions: held(checking), Now: checksUntil.Add(RestartGrace + time.Second)}); stall.Reason != ReasonSessionIdle {
		t.Fatalf("stall = %+v, want a check wait past the stage's end read as what the line otherwise says", stall)
	}

	// A poll that declined to pull into a free seat because the bound was under
	// a poll away is the session's own decision, not a queue nobody is pulling.
	nearSince := moment.Add(-14 * time.Minute)
	skipped := runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchIdle, At: moment.Add(-time.Second),
		Draining: &runstate.WatchDrain{Since: nearSince, BoundSeconds: 900, Until: nearSince.Add(15 * time.Minute), Hosting: 1, PullSkipped: true}}
	if stall := WhyNothingStarts(Conditions{Sessions: held(skipped), Now: moment}); stall.Reason != ReasonRedeploying || !strings.Contains(stall.Says, "less than one poll away") {
		t.Fatalf("stall = %+v, want the skipped pull named as the session restarting", stall)
	}
	// A skip whose bound is long past belongs to a session that has since
	// stopped or died, and reads as the idle line it is.
	if stall := WhyNothingStarts(Conditions{Sessions: held(skipped), Now: moment.Add(time.Hour)}); stall.Reason != ReasonSessionIdle {
		t.Fatalf("stall = %+v, want a stale skip read as an idle session", stall)
	}

	restarting := runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchStopped, At: moment.Add(-time.Minute), Restarting: true}
	stall = WhyNothingStarts(Conditions{Sessions: held(drained, restarting), Now: moment})
	if stall.Reason != ReasonRedeploying {
		t.Fatalf("reason = %q (%q), want a stop recorded as a restart read as the session coming back", stall.Reason, stall.Says)
	}
	// A restart that took longer than a restart takes is not a restart: a new
	// build that died in its own startup after the exec writes nothing, and the
	// reader is owed the dead session rather than one forever on its way back.
	stale := runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchStopped, At: moment.Add(-RestartGrace - time.Second), Restarting: true}
	stall = WhyNothingStarts(Conditions{Sessions: held(stale), Now: moment})
	if stall.Reason != ReasonNoWatchSession {
		t.Fatalf("reason = %q, want a stale restart read as no session running", stall.Reason)
	}
	// A restart that then did not happen writes a later stop that is an ending,
	// and that one is what the log says.
	ended := runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchStopped, At: moment}
	stall = WhyNothingStarts(Conditions{Sessions: held(drained, restarting, ended), Now: moment})
	if stall.Reason != ReasonNoWatchSession {
		t.Fatalf("reason = %q, want the restart that did not happen read as no session", stall.Reason)
	}
	// And a drain still inside its bound is a session choosing work, drain or no
	// drain: it pulls into free seats and fires its tasks right up to the restart.
	watching := runstate.WatchTransition{SessionID: "watch-2", State: runstate.WatchWatching, At: moment,
		Draining: &runstate.WatchDrain{Since: since, BoundSeconds: 900, Until: since.Add(15 * time.Minute), Hosting: 1}}
	if stall := WhyNothingStarts(Conditions{Sessions: held(watching)}); stall.Stopped() {
		t.Fatalf("stall = %+v, want a draining session inside its bound read as choosing", stall)
	}
}

// A session that has been draining past its bound without restarting is a
// factory problem, said in plain words with since when, and the harness's to
// move. It is said even over a full machine, because the runs it stopped keep
// their seats in flight: on 2026-10-05 the slots read as taken by runs that had
// no process behind them, and nothing said the session was stuck.
func TestASessionDrainingPastItsBoundIsAFactoryProblem(t *testing.T) {
	t.Parallel()
	since := moment.Add(-time.Hour)
	until := since.Add(15 * time.Minute)
	stuck := runstate.WatchTransition{SessionID: "watch-8b54", State: runstate.WatchWatching, At: until.Add(time.Minute),
		Draining: &runstate.WatchDrain{Since: since, BoundSeconds: 900, Until: until, Hosting: 3, BoundReached: true}}
	// A recurring pass the session took since is a note, and leaves it stuck.
	pass := runstate.WatchTransition{SessionID: "watch-8b54", State: runstate.WatchWatching, At: until.Add(2 * time.Minute),
		RecurringPass: &runstate.WatchPass{Task: "architect-pass", At: until.Add(2 * time.Minute)}}
	stall := WhyNothingStarts(Conditions{Running: 3, Capacity: 3, Sessions: held(stuck, pass), Now: moment})
	if stall.Reason != ReasonDrainOverrun || !stall.Since.Equal(until) {
		t.Fatalf("stall = %+v, want the session named as draining past its bound since the bound ran out", stall)
	}
	for _, want := range []string{"watch-8b54", "draining past its bound since " + localMoment(until), "has not restarted", "pulls no new work"} {
		if !strings.Contains(stall.Says, want) {
			t.Fatalf("says = %q, want %q in it", stall.Says, want)
		}
	}
	entry, waiting := stall.Waiting()
	if !waiting || entry.Mover != MoverHarness || entry.Kind != AttentionStall {
		t.Fatalf("entry = %+v, want a factory problem for the harness to move", entry)
	}
	if said := entry.What(); strings.Count(said, "since") != 1 {
		t.Fatalf("entry says = %q, want since when said once, in local time", said)
	}
	if whose := entry.Whose(); !strings.HasPrefix(whose, "the harness's") {
		t.Fatalf("whose = %q, want the harness's move", whose)
	}

	// And the standing reading carries it under its factory problems and on the
	// attention line, once, with nothing admitted for it to hold back.
	sources := quietSources()
	sources.Sessions = fakeSessions{transitions: []runstate.WatchTransition{stuck, pass}}
	standing := ReadStanding(context.Background(), sources)
	found := 0
	for _, problem := range standing.FactoryProblems {
		if problem.Stall != nil && problem.Stall.Reason == ReasonDrainOverrun {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("factory problems = %+v, want the session draining past its bound among them", standing.FactoryProblems)
	}
	found = 0
	for _, need := range standing.NeedsHuman {
		if need.Stall != nil && need.Stall.Reason == ReasonDrainOverrun {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("attention = %+v, want the session draining past its bound said once", standing.NeedsHuman)
	}

	// A session that restarted is no problem at all: the new session's line is
	// the latest live one.
	restarted := runstate.WatchTransition{SessionID: "watch-f0bd", State: runstate.WatchWatching, At: moment.Add(-time.Minute)}
	if _, over := DrainOverrunOf([]runstate.WatchTransition{stuck, pass, restarted}, moment); over {
		t.Fatal("a session that has restarted was read as one stuck draining")
	}
}

// A session watching settles it. The harness would start the next pullable item,
// which is what makes a startable item's absence from the not-startable line
// mean something.
func TestASessionChoosingWorkIsNoStall(t *testing.T) {
	t.Parallel()
	stall := WhyNothingStarts(Conditions{Sessions: held(
		runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchIdle, At: moment.Add(-2 * time.Hour)},
		runstate.WatchTransition{SessionID: "watch-2", State: runstate.WatchWatching, At: moment.Add(-time.Hour)},
	)})
	if stall.Stopped() {
		t.Fatalf("stall = %+v, want nothing stopping the choosing", stall)
	}
	if stall.Refusal() != "" || stall.Mark() != "" {
		t.Fatalf("a stall that is not stopping anything said something: %q / %q", stall.Refusal(), stall.Mark())
	}
}

// An idle session is not an absent one. Telling an operator to start a watch
// session that is already running and sitting idle is the disagreement between
// two derivations of this that made it one, and it is the answer that sends
// somebody to the wrong place.
func TestAnIdleSessionIsNotToldToStartOne(t *testing.T) {
	t.Parallel()
	stall := WhyNothingStarts(Conditions{Sessions: held(
		runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchIdle, At: moment.Add(-time.Hour)},
	)})
	if stall.Reason != ReasonSessionIdle {
		t.Fatalf("reason = %q, want the idle session named as itself", stall.Reason)
	}
	if strings.Contains(stall.Refusal(), "yoyo work --watch") {
		t.Fatalf("an idle session was told to start a session: %q", stall.Refusal())
	}
}

// A live session whose last poll could not read the store is not one that found
// nothing to start. The queue was never read, so the line says the read is being
// retried and names the harness, whose move it is, rather than sending the
// operator to a queue nobody has seen.
func TestASessionRetryingAFailedReadIsNotSaidAsIdle(t *testing.T) {
	t.Parallel()
	outage := runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchIdle, At: moment.Add(-time.Hour), Unreadable: true}
	stall := WhyNothingStarts(Conditions{Sessions: held(
		runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchWatching, At: moment.Add(-2 * time.Hour)},
		outage,
	)})
	if stall.Reason != ReasonStoreUnreadable {
		t.Fatalf("reason = %q (%q), want the failing read named as itself", stall.Reason, stall.Says)
	}
	if strings.Contains(stall.Says, "found nothing") || !strings.Contains(stall.Says, "reading it again") {
		t.Fatalf("says %q, want the retried read rather than an empty queue", stall.Says)
	}
	if !stall.Since.Equal(outage.At) {
		t.Fatalf("since = %s, want when the failing read was recorded", stall.Since)
	}
	if !strings.HasPrefix(stall.Reason.Whose(), "the harness's") {
		t.Fatalf("whose = %q, want the harness's", stall.Reason.Whose())
	}
	if _, waiting := stall.Waiting(); waiting {
		t.Fatal("a read the harness is retrying was put on the attention line as waiting on a person")
	}

	// Once a read succeeds and finds nothing, the idle line is back.
	recovered := WhyNothingStarts(Conditions{Sessions: held(
		outage,
		runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchIdle, At: moment, Reason: "the backlog is empty"},
	)})
	if recovered.Reason != ReasonSessionIdle {
		t.Fatalf("reason = %q, want an idle session once the read succeeded", recovered.Reason)
	}
}

// Every surface that names where a session got to says a retried read as one,
// rather than as the idle state it is recorded under.
func TestSessionSaysARetriedReadAndARestartAsThemselves(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		transition runstate.WatchTransition
		want       string
	}{
		{runstate.WatchTransition{State: runstate.WatchIdle}, "idle"},
		{runstate.WatchTransition{State: runstate.WatchIdle, Unreadable: true}, "retrying a failed read of the harness's store"},
		{runstate.WatchTransition{State: runstate.WatchStopped, Unreadable: true}, "stopped"},
		{runstate.WatchTransition{State: runstate.WatchStopped, Restarting: true}, "stopped to restart into the build deployed over it"},
	} {
		if said := SessionSays(testCase.transition); said != testCase.want {
			t.Fatalf("SessionSays(%+v) = %q, want %q", testCase.transition, said, testCase.want)
		}
	}
	last := LastWord([]runstate.WatchTransition{{SessionID: "watch-1", State: runstate.WatchIdle, At: moment, Unreadable: true}})
	if !strings.Contains(last, "retrying a failed read") {
		t.Fatalf("last word = %q, want the retried read", last)
	}
}

// A watch log that cannot be read is not a stall. A reason invented over a
// record nobody could open is the confident emptiness every answer here is
// written to avoid, so what is reported is that the question could not be asked.
func TestAnUnreadableWatchLogInventsNoReason(t *testing.T) {
	t.Parallel()
	unreadable := WhyNothingStarts(Conditions{
		Sessions: func() ([]runstate.WatchTransition, error) { return nil, errors.New("the state directory is gone") },
	})
	if unreadable.Stopped() {
		t.Fatalf("stall = %+v, want no reason at all", unreadable)
	}
	if !strings.Contains(unreadable.Problem, "the state directory is gone") {
		t.Fatalf("problem = %q", unreadable.Problem)
	}
	unwired := WhyNothingStarts(Conditions{})
	if unwired.Stopped() || !strings.Contains(unwired.Problem, "nothing was wired") {
		t.Fatalf("stall = %+v, want the missing source said", unwired)
	}
}

// The watch log is read only where the question reaches it. The switches and the
// machine's own capacity are answered from records the caller already holds, and
// a surface that would spend a read to answer this spends it only when it has to.
func TestTheWatchLogIsReadOnlyWhenItIsNeeded(t *testing.T) {
	t.Parallel()
	reads := 0
	conditions := Conditions{
		IntakeHold: runstate.IntakeHold{HeldAt: moment}, IntakeHeld: true,
		Sessions: func() ([]runstate.WatchTransition, error) {
			reads++
			return nil, nil
		},
	}
	if stall := WhyNothingStarts(conditions); stall.Reason != ReasonIntakeHold {
		t.Fatalf("reason = %q", stall.Reason)
	}
	if reads != 0 {
		t.Fatalf("the watch log was read %d times behind a held switch", reads)
	}
	conditions.IntakeHeld = false
	if stall := WhyNothingStarts(conditions); stall.Reason != ReasonUnwatched {
		t.Fatalf("reason = %q", stall.Reason)
	}
	if reads != 1 {
		t.Fatalf("the watch log was read %d times, want once", reads)
	}
}

// A different state re-arms the clock a surface repeats itself on, and the same
// state standing keeps it. The mark is what carries that across a restart, so it
// names the reason and when it began and nothing else.
func TestTheMarkNamesTheStateAndItsAge(t *testing.T) {
	t.Parallel()
	idle := WhyNothingStarts(Conditions{Sessions: held(
		runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchIdle, At: moment.Add(-time.Hour)},
	)})
	same := WhyNothingStarts(Conditions{Sessions: held(
		runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchIdle, At: moment.Add(-time.Hour)},
	)})
	later := WhyNothingStarts(Conditions{Sessions: held(
		runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchIdle, At: moment},
	)})
	if idle.Mark() != same.Mark() {
		t.Fatalf("one standing state marked two ways: %q and %q", idle.Mark(), same.Mark())
	}
	if idle.Mark() == later.Mark() {
		t.Fatalf("a state that began at a different time kept the last one's mark: %q", later.Mark())
	}
	if !strings.HasPrefix(idle.Mark(), string(ReasonSessionIdle)+":") {
		t.Fatalf("mark = %q, want it to name the reason", idle.Mark())
	}
}

// A stall over an empty queue is a state of the machine rather than something
// waiting on a person. The attention line is what an operator reads to find what
// will wait forever without them, and it is worth nothing if a quiet machine
// fills it.
func TestAStallHoldingNothingBackIsNotAttention(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Sessions = fakeSessions{transitions: []runstate.WatchTransition{
		{SessionID: "watch-1", State: runstate.WatchStopped, At: moment.Add(-time.Hour)},
	}}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.NeedsHuman) != 0 {
		t.Fatalf("needs a human = %+v, want nothing waiting on anybody", standing.NeedsHuman)
	}
	// The same stall over admitted work is waiting on somebody, and says who.
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": {{ID: "item-1", Status: "open"}}},
		ready:    []beads.WorkItem{{ID: "item-1"}},
	}}
	standing = ReadStanding(context.Background(), sources)
	if len(standing.NeedsHuman) != 1 || standing.NeedsHuman[0].Whose() != ReasonNoWatchSession.Whose() {
		t.Fatalf("needs a human = %+v", standing.NeedsHuman)
	}
	// Since-when is said once, on the line whose job it is. The attention line
	// carries it beside whose move it is, and the refusal beside the item does not
	// repeat it: one timestamp twice in four lines is what stops them being read at
	// a glance.
	if !strings.Contains(standing.NeedsHuman[0].What(), "since "+moment.Add(-time.Hour).Format(time.RFC3339)) {
		t.Fatalf("needs a human = %q, want it to say since when", standing.NeedsHuman[0].What())
	}
	if len(standing.NotStartable) != 1 || strings.Contains(standing.NotStartable[0].Reason, "since ") {
		t.Fatalf("not startable = %+v, want the refusal without a timestamp", standing.NotStartable)
	}
}

// What the stall could not read is said even when the queue is empty. It is a
// gap in the reading rather than a fact about the work, and a line that reported
// a quiet backlog while it could not tell whether anything was choosing from it
// would be the confident emptiness this whole format is written against.
func TestAnEmptyQueueDoesNotSwallowAnUnreadableStall(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Sessions = fakeSessions{fail: errors.New("the state directory is gone")}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.NotStartable) != 0 {
		t.Fatalf("not startable = %+v, want nothing over an empty queue", standing.NotStartable)
	}
	if !strings.Contains(standing.NotStartableProblem, "the state directory is gone") {
		t.Fatalf("not-startable problem = %q", standing.NotStartableProblem)
	}
}

// A held switch is on the attention line once. It is there in its own right,
// because a hold waits on the operator whether or not it is currently what stops
// the choosing, and a stall that repeated it would be one state said twice in a
// reading whose whole value is that it can be trusted.
func TestAHeldSwitchIsSaidOnceOnTheAttentionLine(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.IntakeHolds = fakeIntakeHolds{
		hold: runstate.IntakeHold{HeldAt: moment.Add(-time.Hour), Reason: "the overnight looked wrong"},
		held: true,
	}
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": {{ID: "item-1", Status: "open"}}},
		ready:    []beads.WorkItem{{ID: "item-1"}},
	}}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.NeedsHuman) != 1 {
		t.Fatalf("needs a human = %+v, want the hold said once", standing.NeedsHuman)
	}
}

// The watch owns the refusal's cause; every surface receives the same line.
func TestABlockedSessionCarriesItsCauseAheadOfAnIdleSession(t *testing.T) {
	t.Parallel()
	const cause = "runs cannot start: uncommitted changes in the primary checkout (.yoyodyne/config.yaml); commit or stash to release"
	blocked := runstate.WatchTransition{SessionID: "blocked", State: runstate.WatchBlocked, At: moment.Add(-time.Hour), Reason: cause}
	idle := runstate.WatchTransition{SessionID: "idle", State: runstate.WatchIdle, At: moment}
	stall := WhyNothingStarts(Conditions{Sessions: held(blocked, idle), Now: moment})
	if stall.Reason != ReasonSessionBlocked || stall.Says != cause || !stall.Since.Equal(blocked.At) {
		t.Fatalf("stall = %+v, want the original refusal and when it started", stall)
	}
	if len(Choosing([]runstate.WatchTransition{blocked})) != 0 || len(Live([]runstate.WatchTransition{blocked})) != 1 {
		t.Fatal("a blocked session is alive but choosing nothing")
	}
	watching := runstate.WatchTransition{SessionID: "working", State: runstate.WatchWatching, At: moment}
	if got := WhyNothingStarts(Conditions{Sessions: held(blocked, watching), Now: moment}); got.Stopped() {
		t.Fatalf("a session choosing work was reported as stopped: %+v", got)
	}
}
