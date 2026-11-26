package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type gatedJoins struct {
	owed  bool
	calls chan struct{}
	gate  chan struct{}
	err   error
}

func (g *gatedJoins) Owed(context.Context) (bool, error) { return g.owed, nil }

func (g *gatedJoins) Judge(context.Context) (float64, error) {
	g.calls <- struct{}{}
	<-g.gate
	return 2, g.err
}

// The judgments are made beside the pull, one round at a time, and what they cost
// and what went wrong are on the schedule once the round is collected.
func TestPiecesOfANamedEpicAreJudgedBesideThePull(t *testing.T) {
	t.Parallel()
	joins := &gatedJoins{calls: make(chan struct{}, 2), gate: make(chan struct{}), err: errors.New("no capacity")}
	firings := newRecurringFirings(context.Background())
	defer firings.cancel(nil)
	scheduler := Scheduler{Now: time.Now}
	var schedule Schedule
	judge := func(pull Pull, starting bool) {
		scheduler.judgeEpicPieces(context.Background(), &schedule, pull, firings, starting)
	}
	judge(Pull{}, true)
	judge(Pull{EpicJoins: joins}, true)
	joins.owed = true
	judge(Pull{EpicJoins: joins}, false)
	if firings.taking(epicJoinsTask) {
		t.Fatal("a round was started with nothing owed, or while the session drains")
	}
	judge(Pull{EpicJoins: joins}, true)
	judge(Pull{EpicJoins: joins}, true)
	select {
	case <-joins.calls:
	case <-time.After(30 * time.Second):
		t.Fatal("no round was started")
	}
	close(joins.gate)
	scheduler.collectFirings(&schedule, firings, &recurringWatch{}, true)
	if len(joins.calls) != 0 || schedule.SpentUSD != 2 || firings.taking(epicJoinsTask) || !strings.Contains(schedule.Render(), "no capacity") {
		t.Errorf("%d more rounds, spent %v, and the schedule says\n%s", len(joins.calls), schedule.SpentUSD, schedule.Render())
	}
}
