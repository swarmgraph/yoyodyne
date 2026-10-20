package readmodel

import (
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A task is failing from its second pre-turn failure since the last firing
// that took a turn. A firing that asked nothing for another reason — the
// provider refusing it, the outage wait — neither counts toward it nor ends
// it; only a turn ends it. The mover is read off the latest cause.
func TestAFailingTaskIsCountedFromTheLastFiringThatTookATurn(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 26, 6, 39, 0, 0, time.UTC)
	pass := func(task string, hour int, turns int, cause runstate.PreTurnCause) runstate.Sweep {
		return runstate.Sweep{
			Task: task, Role: domain.RoleDevelopmentManager, StartedAt: at.Add(time.Duration(hour) * time.Hour),
			Turns: turns, NotStarted: cause, Problem: "problem at hour " + time.Duration(hour).String(),
		}
	}
	passes := []runstate.Sweep{
		// dm: failed, took a turn, then failed, provider-refused, failed, failed.
		pass("dm", 0, 0, runstate.PreTurnMessageRefused),
		pass("dm", 1, 1, ""),
		pass("dm", 2, 0, runstate.PreTurnMessageRefused),
		pass("dm", 3, 0, ""),
		pass("dm", 4, 0, runstate.PreTurnMessageRefused),
		pass("dm", 5, 0, runstate.PreTurnConversationUnopened),
		// pm: one failure only.
		pass("pm", 0, 1, ""),
		pass("pm", 1, 0, runstate.PreTurnContextUnassembled),
		// arch: two failures, then a turn.
		pass("arch", 0, 0, runstate.PreTurnContextUnassembled),
		pass("arch", 1, 0, runstate.PreTurnContextUnassembled),
		pass("arch", 2, 2, ""),
	}
	failing := FailingTasksOf(passes)
	if len(failing) != 1 {
		t.Fatalf("failing = %+v, want only dm", failing)
	}
	dm := failing[0]
	if dm.Task != "dm" || dm.Failures != 3 {
		t.Fatalf("dm = %+v, want 3 failures since the turn at hour 1", dm)
	}
	if !dm.FirstAt.Equal(at.Add(2*time.Hour)) || !dm.RaisedAt.Equal(at.Add(4*time.Hour)) || !dm.LatestAt.Equal(at.Add(5*time.Hour)) {
		t.Fatalf("dm moments = first %s raised %s latest %s", dm.FirstAt, dm.RaisedAt, dm.LatestAt)
	}
	// A conversation that will not open is the harness's to fix: none of what
	// stops one opening is on the operator's closed list.
	if mover := FailingTaskAttention(dm).Mover; dm.Cause != runstate.PreTurnConversationUnopened || mover != MoverHarness {
		t.Fatalf("dm cause %q mover %q, want the latest cause and the harness's move", dm.Cause, mover)
	}
}
