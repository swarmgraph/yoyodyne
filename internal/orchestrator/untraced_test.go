package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// A pass that reports findings and leaves no trace of them — no memory, no
// lane report, no report filed, no work admitted — is recorded as untraced,
// shown on the attention line with its role as the one to move, and named on
// the task's next pass so the role can write the trace then. The next pass
// leaving a trace clears it, and the pass after that is told nothing.
func TestAPassThatFoundSomethingAndLeftNoTraceIsFlagged(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	clock := &movingRecurringClock{now: recurringNow}
	role := &wokenRole{answers: []scriptedTurn{
		{result: complete("two things", sweep.Finding{Issue: "reviews wait an hour for a slot", Disposition: sweep.DispositionLeft},
			sweep.Finding{Issue: "a claim nothing is working on", Disposition: sweep.DispositionFixed})},
		{result: complete("remembered them", sweep.Finding{Issue: "reviews wait an hour for a slot", Disposition: sweep.DispositionLeft}),
			saved: []runstate.SavedWrite{{Kind: runstate.SavedMemory, Action: "remember", Memory: "reviews-wait-for-slots", Revision: 1}}},
		{result: complete("nothing new")},
	}}
	trigger := Trigger{Tasks: hourlyTask("look"), Claims: store, Reports: store, Roles: role, Clock: clock}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if !fired.Fired[0].Untraced {
		t.Fatalf("fired = %+v, want the pass flagged untraced", fired.Fired[0])
	}
	if rendered := fired.Render(); !strings.Contains(rendered, "left no trace of what it found") {
		t.Errorf("rendered = %q, want the untraced pass said", rendered)
	}
	recorded, _, err := store.List()
	if err != nil || len(recorded) != 1 || !recorded[0].Untraced {
		t.Fatalf("recorded = %+v (%v), want one pass recorded as untraced", recorded, err)
	}
	untraced := readmodel.UntracedPassesOf(recorded)
	if len(untraced) != 1 || untraced[0].Task != "a-sweep" || untraced[0].Findings != 2 {
		t.Fatalf("untraced = %+v, want the task's pass with its two findings", untraced)
	}
	if mover := readmodel.MoverOf(untraced[0].Role); mover != readmodel.MoverDevelopmentManager {
		t.Errorf("mover = %q, want the role whose pass it was", mover)
	}

	// The next pass is told which findings it left no trace of.
	clock.now = clock.now.Add(time.Hour)
	fired, err = trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	told := role.messages[1]
	for _, want := range []string{"left no trace of them", "reviews wait an hour for a slot", "a claim nothing is working on", "leave its trace on this pass"} {
		if !strings.Contains(told, want) {
			t.Errorf("the next pass's message does not carry %q:\n%s", want, told)
		}
	}
	if fired.Fired[0].Untraced {
		t.Errorf("fired = %+v, a pass that saved a memory is flagged untraced", fired.Fired[0])
	}
	recorded, _, _ = store.List()
	if untraced := readmodel.UntracedPassesOf(recorded); len(untraced) != 0 {
		t.Errorf("untraced = %+v, want the flag cleared by a pass that left a trace", untraced)
	}

	// And the pass after one that left a trace is told nothing about it.
	clock.now = clock.now.Add(time.Hour)
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if strings.Contains(role.messages[2], "left no trace") {
		t.Errorf("the third pass was told of an untraced pass:\n%s", role.messages[2])
	}
	recorded, _, _ = store.List()
	if recorded[2].Untraced {
		t.Error("a pass that found nothing is flagged untraced")
	}
}

// Each of the four traces clears the flag on its own, and the harness's own
// reading of the forge is not the role's finding.
func TestEveryKindOfTraceCountsAndOnlyTheRolesFindingsAreChecked(t *testing.T) {
	t.Parallel()

	finding := sweep.Finding{Issue: "a stale report", Disposition: sweep.DispositionFiled}
	for name, turn := range map[string]scriptedTurn{
		"a memory":      {saved: []runstate.SavedWrite{{Kind: runstate.SavedMemory, Action: "remember", Memory: "stale", Revision: 2}}},
		"a lane report": {saved: []runstate.SavedWrite{{Kind: runstate.SavedLaneReport, Revision: 3}}},
		"a report":      {reports: 1},
		"admitted work": {admitted: []string{"yoyodyne-ifd.500"}},
	} {
		store := sweepStore(t)
		turn.result = complete("one", finding)
		role := &wokenRole{answers: []scriptedTurn{turn}}
		trigger := Trigger{Tasks: hourlyTask("look"), Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}
		fired, err := trigger.Fire(context.Background())
		if err != nil {
			t.Fatalf("%s: Fire() error = %v", name, err)
		}
		if fired.Fired[0].Untraced {
			t.Errorf("%s: a pass that left %s is flagged untraced", name, name)
		}
		recorded, _, _ := store.List()
		if len(recorded) != 1 || recorded[0].Untraced || !recorded[0].LeftATrace() {
			t.Errorf("%s: recorded = %+v, want a traced pass", name, recorded)
		}
	}
}

// A record that took no turn between an untraced pass and the next one — a
// firing refused before its turn — neither clears the flag nor tells the role
// anything, so the next pass that takes a turn is still told.
func TestAFiringWithNoTurnDoesNotSwallowTheUntracedPass(t *testing.T) {
	t.Parallel()

	earlier := []runstate.Sweep{
		{Task: "a-sweep", Turns: 1, StartedAt: recurringNow, Untraced: true, Result: complete("one", sweep.Finding{Issue: "x", Disposition: sweep.DispositionLeft})},
		{Task: "a-sweep", Turns: 0, StartedAt: recurringNow.Add(time.Hour), NotStarted: runstate.PreTurnMessageRefused, Problem: "refused"},
	}
	pass, found := lastUntracedPass(earlier)
	if !found || !pass.StartedAt.Equal(recurringNow) {
		t.Fatalf("lastUntracedPass = %+v, %v, want the untraced pass before the refused firing", pass, found)
	}
	if untraced := readmodel.UntracedPassesOf(earlier); len(untraced) != 1 {
		t.Errorf("untraced = %+v, want the flag standing over a firing that took no turn", untraced)
	}
}
