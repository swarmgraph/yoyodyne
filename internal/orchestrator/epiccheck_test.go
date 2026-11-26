package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type gatedChecks struct {
	owed  bool
	calls chan struct{}
	gate  chan struct{}
	err   error
}

func (g *gatedChecks) Owed(context.Context) (bool, error) { return g.owed, nil }

func (g *gatedChecks) Check(context.Context) (float64, error) {
	g.calls <- struct{}{}
	<-g.gate
	return 3, g.err
}

// The check of the whole epic is made beside the pull, one round at a time, only
// once it is owed, and what it cost and what went wrong are on the schedule once
// the round is collected.
func TestTheCheckOfTheWholeEpicIsMadeBesideThePull(t *testing.T) {
	t.Parallel()
	owing := &gatedChecks{calls: make(chan struct{}, 2), gate: make(chan struct{}), err: errors.New("no capacity")}
	firings := newRecurringFirings(context.Background())
	defer firings.cancel(nil)
	scheduler := Scheduler{Now: time.Now}
	var schedule Schedule
	check := func(pull Pull, starting bool) {
		scheduler.checkWholeEpics(context.Background(), &schedule, pull, firings, starting)
	}
	check(Pull{}, true)
	check(Pull{EpicChecks: owing}, true)
	owing.owed = true
	check(Pull{EpicChecks: owing}, false)
	if firings.taking(epicChecksTask) {
		t.Fatal("a check was started with nothing owed, or while the session drains")
	}
	check(Pull{EpicChecks: owing}, true)
	check(Pull{EpicChecks: owing}, true)
	select {
	case <-owing.calls:
	case <-time.After(30 * time.Second):
		t.Fatal("no check was started")
	}
	close(owing.gate)
	scheduler.collectFirings(&schedule, firings, &recurringWatch{}, true)
	if len(owing.calls) != 0 || schedule.SpentUSD != 3 || firings.taking(epicChecksTask) || !strings.Contains(schedule.Render(), "the check of the whole of a named epic could not be made") {
		t.Errorf("%d more rounds, spent %v, and the schedule says\n%s", len(owing.calls), schedule.SpentUSD, schedule.Render())
	}
}
