package orchestrator

// The intake brake summoning the development manager and releasing itself.
//
// Every test here replays the shape of 2026-09-19T17:56Z: three runs blocked in
// a row, the brake tripped, and — until this — the line sat held for two hours
// with a free developer slot idle because a person had to notice. What the
// operator asked for was the development manager invoked at once and nothing
// waiting, and each test below is one of the ways the hold now ends without a
// person: her decision, or a probe run that lands. The one that does wait on a
// person is the one she escalated, and that is tested too.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// blockedStorm is the pipeline stopping every run on a reviewer's verdict, which
// is the storm the brake counts.
func blockedStorm(h *scheduleHarness, id string) (Outcome, error) {
	h.retire(id)
	return Outcome{
		RunID:      "run-" + id,
		WorkItemID: id,
		Status:     runstate.StatusFailed,
		Blocked:    true,
		Failure:    "independent review still required repair after every permitted attempt",
	}, nil
}

// The 17:56Z shape, replayed. Three runs block in a row, the brake trips, and
// within the same poll the development manager is summoned with the three runs
// and the reason each blocked in front of her. She decides to release, and the
// next poll lifts the hold and pulls the fourth item — with no person anywhere
// in it.
func TestTheBrakeSummonsTheDevelopmentManagerAtOnceAndReleasesOnHerDecision(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three", "yoyodyne-four")...)
	harness.blockedRuns = 3
	harness.run = blockedStorm
	harness.summon = func(h *scheduleHarness, summons BrakeSummons, _ int) (Fired, error) {
		// Her summoned turn: she reads the three entries and decides the line is
		// fine to release. The decision is what her conversation writes onto the
		// hold, which the scheduler reads at its next poll.
		h.decideBrake(runstate.BrakeDecisionRelease, "the three stops were verdicts on the changes, not the machine")
		return Fired{Task: "development-manager-sweep", Turns: 1, CostUSD: 0.5, Summoned: "the intake brake"}, nil
	}
	sessions := &recordedSessions{}
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 3 }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Sessions: sessions, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Braked == nil || schedule.Braked.Brake == nil {
		t.Fatalf("schedule = %s, want the brake to have held intake with its trip on the hold", schedule.Render())
	}
	// The trip names the three runs and why each blocked, in the order they
	// blocked: that is what rides the summons rather than a count.
	blocked := schedule.Braked.Brake.Blocked
	if len(blocked) != 3 || blocked[0].WorkItemID != "yoyodyne-one" || blocked[2].RunID != "run-yoyodyne-three" {
		t.Fatalf("trip names %#v, want the three blocked runs in order", blocked)
	}
	for _, entry := range blocked {
		if !strings.Contains(entry.Reason, "independent review still required repair") {
			t.Fatalf("trip entry %#v, want the reason the run blocked in the run's own words", entry)
		}
	}
	// She was summoned once, at the moment of the trip and with that hold.
	if len(harness.summonses) != 1 {
		t.Fatalf("summoned %d time(s), want once at the trip", len(harness.summonses))
	}
	if summoned := harness.summonses[0]; summoned.Brake == nil || len(summoned.Brake.Blocked) != 3 {
		t.Fatalf("summoned over %#v, want the hold carrying the three entries", summoned)
	}
	// The summoned firing is on the schedule as a firing, so the session's account
	// says it woke her and what it spent.
	if len(schedule.Fired) != 1 || schedule.Fired[0].Summoned == "" {
		t.Fatalf("fired = %#v, want the summoned pass on the schedule", schedule.Fired)
	}
	if schedule.SpentUSD < 0.5 {
		t.Fatalf("spent $%.2f, want the summoned turn counted against the session", schedule.SpentUSD)
	}
	// Her decision released the hold, and the fourth item was pulled after it.
	if len(harness.releases) != 1 {
		t.Fatalf("released %d time(s), want the hold lifted once on her decision", len(harness.releases))
	}
	if len(schedule.Released) != 1 || !strings.Contains(schedule.Released[0].Reason, "the development manager decided to release it") {
		t.Fatalf("released = %#v, want the release attributed to her decision", schedule.Released)
	}
	if order := harness.pullOrder(); len(order) != 4 || order[3] != "yoyodyne-four" {
		t.Fatalf("pulled %v, want the fourth item pulled once the hold was released", order)
	}
	if _, held, _ := harness.Held(); held {
		t.Fatal("intake is still held, want it released on her decision")
	}
	// Nothing about it was reported as a person's move.
	if reason := sessions.said(runstate.WatchBraked); strings.Contains(reason, "until somebody releases it") {
		t.Fatalf("braked reason = %q, want a brake hold never reported as waiting on a person", reason)
	}
	if mover := sessions.lastMover(runstate.WatchBraked); !strings.Contains(mover, "the development manager") && !strings.Contains(mover, "the harness's") {
		t.Fatalf("braked mover = %q, want the development manager or the harness named rather than the operator", mover)
	}
}

// The other way the hold ends without a person. She was summoned and recorded
// nothing, so once the cooldown runs out the brake probes the line with one run
// started under its own hold, and the probe landing reopens intake.
func TestTheBrakeProbesTheLineWhenNobodyDecidesAndReleasesOnALanding(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three", "yoyodyne-four", "yoyodyne-five")...)
	harness.blockedRuns = 3
	harness.cooldown = 10 * time.Minute
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		if id == "yoyodyne-four" || id == "yoyodyne-five" {
			landed := h.complete(id)
			landed.RunID = "run-" + id
			return landed, nil
		}
		return blockedStorm(h, id)
	}
	harness.summon = func(*scheduleHarness, BrakeSummons, int) (Fired, error) {
		// Her summoned turn answered and decided nothing about the hold.
		return Fired{Task: "development-manager-sweep", Turns: 1, Summoned: "the intake brake"}, nil
	}
	sessions := &recordedSessions{}
	// Each poll is a minute; the cooldown is ten. The session waits it out
	// braked and then probes.
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 14 }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Sessions: sessions, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Braked == nil {
		t.Fatalf("schedule = %s, want the brake to have held intake", schedule.Render())
	}
	// The hold was summoned over once and then left to the cooldown.
	if len(harness.summonses) != 1 {
		t.Fatalf("summoned %d time(s), want once", len(harness.summonses))
	}
	// The probe: one run, started under the hold, selected as the brake's probe
	// rather than as the scheduler's ordinary choice, and marked as such on the
	// schedule.
	var probe *Started
	for index := range schedule.Started {
		if schedule.Started[index].Probe {
			probe = &schedule.Started[index]
		}
	}
	if probe == nil || probe.WorkItemID != "yoyodyne-four" {
		t.Fatalf("started = %s, want the fourth item started as the brake's probe", schedule.Render())
	}
	if selection := harness.selectionFor("yoyodyne-four"); selection.By != runstate.SelectedByBrake || !strings.Contains(selection.Reason, "probe run") {
		t.Fatalf("probe selected by %q for %q, want the intake brake's probe recorded as why the run exists", selection.By, selection.Reason)
	}
	// Its landing released the hold, and the fifth item followed as ordinary work.
	if len(harness.releases) != 1 {
		t.Fatalf("released %d time(s), want the hold lifted once on the probe landing", len(harness.releases))
	}
	if len(schedule.Released) != 1 || !strings.Contains(schedule.Released[0].Reason, "probe run run-yoyodyne-four of yoyodyne-four landed") {
		t.Fatalf("released = %#v, want the release attributed to the probe landing", schedule.Released)
	}
	if order := harness.pullOrder(); len(order) != 5 || order[4] != "yoyodyne-five" {
		t.Fatalf("pulled %v, want the fifth item pulled once the probe landed", order)
	}
	// The probe was recorded as landed on the hold that was lifted.
	lifted := harness.releases[0]
	if lifted.Brake == nil || lifted.Brake.Probe == nil || !lifted.Brake.Probe.Landed || lifted.Brake.Probe.RunID != "run-yoyodyne-four" {
		t.Fatalf("lifted hold = %#v, want the landed probe on its record", lifted.Brake)
	}
	// While the probe ran the session said so rather than saying it was choosing
	// again.
	found := false
	sessions.mu.Lock()
	for _, transition := range sessions.transitions {
		if transition.state == runstate.WatchBraked && strings.Contains(transition.reason, "a probe run of yoyodyne-four is in flight") {
			found = true
		}
	}
	sessions.mu.Unlock()
	if !found {
		t.Fatalf("recorded reasons never named the probe in flight: %v", sessions.states())
	}
}

// A probe that blocks says the line is still broken. The hold stays, the
// cooldown restarts, and the development manager is summoned again — this
// time with the probe's own stoppage beside the three that tripped the brake —
// rather than the line waiting on a person or probing every poll.
func TestABlockedProbeKeepsTheHoldAndSummonsHerAgain(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three", "yoyodyne-four", "yoyodyne-five")...)
	harness.blockedRuns = 3
	harness.cooldown = 5 * time.Minute
	harness.run = blockedStorm
	harness.summon = func(*scheduleHarness, BrakeSummons, int) (Fired, error) {
		return Fired{Task: "development-manager-sweep", Turns: 1, Summoned: "the intake brake"}, nil
	}
	sessions := &recordedSessions{}
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 8 }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Sessions: sessions, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(harness.releases) != 0 {
		t.Fatalf("released %d time(s), want a blocked probe to keep the hold", len(harness.releases))
	}
	hold, held, _ := harness.Held()
	if !held || hold.Brake == nil || hold.Brake.Probe == nil || !hold.Brake.Probe.Blocked {
		t.Fatalf("hold = %#v, want the blocked probe on its record", hold.Brake)
	}
	// Two summonses: the trip, and the blocked probe. The second carries the
	// probe's stoppage.
	if len(harness.summonses) != 2 {
		t.Fatalf("summoned %d time(s), want once at the trip and once more after the probe blocked", len(harness.summonses))
	}
	if second := harness.summonses[1]; second.Brake == nil || second.Brake.Probe == nil || !second.Brake.Probe.Blocked || second.Brake.Probe.WorkItemID != "yoyodyne-four" {
		t.Fatalf("second summons over %#v, want the blocked probe in front of her", second.Brake)
	}
	// One probe in eight polls: the cooldown, not the poll, paces it.
	probes := 0
	for _, started := range schedule.Started {
		if started.Probe {
			probes++
		}
	}
	if probes != 1 {
		t.Fatalf("%d probe(s) in eight polls under a five-minute cooldown, want one", probes)
	}
	if hold.Brake.Probes != 1 {
		t.Fatalf("hold counts %d probe(s), want one", hold.Brake.Probes)
	}
}

// The loop the blocked probe makes has a bound. On a machine that stays broken
// the brake goes round — summons, cooldown, probe, blocked, summons — spending
// one of her turns and one run per cooldown, and before this nothing about it
// got louder unless she escalated it. Driven past execution.brake_escalation_cycles
// with her deciding nothing, the harness escalates the hold to the operator
// itself: the summonses stop, no further probe starts, the record names the
// cycles spent and what stopped the last probe, and every surface names the
// operator and the harness's escalation rather than hers.
func TestASummonsAndProbeLoopEscalatesToTheOperatorAtTheBound(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three", "yoyodyne-four", "yoyodyne-five", "yoyodyne-six", "yoyodyne-seven")...)
	harness.blockedRuns = 3
	harness.cooldown = 5 * time.Minute
	harness.cycleBound = 2
	harness.run = blockedStorm
	var loops []string
	harness.summon = func(h *scheduleHarness, summons BrakeSummons, _ int) (Fired, error) {
		// Her summoned turn answers every time and decides nothing about the
		// hold, which is the shape of a loop nobody is escalating.
		if summons.Hold.Brake != nil {
			loops = append(loops, summons.Hold.Brake.Loop())
		}
		return Fired{Task: "development-manager-sweep", Turns: 1, Summoned: "the intake brake"}, nil
	}
	sessions := &recordedSessions{}
	// Each poll is a minute and the cooldown five: two cycles are twelve or so
	// polls, and the rest of the thirty are the hold standing escalated.
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 30 }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Sessions: sessions, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	// Two probes and no more: the second blocked probe is the second cycle, which
	// is the bound.
	probes := 0
	for _, started := range schedule.Started {
		if started.Probe {
			probes++
		}
	}
	if probes != 2 {
		t.Fatalf("%d probe(s) in thirty polls under a bound of two cycles, want two and then none: %s", probes, schedule.Render())
	}
	// Two summonses: the trip and the first blocked probe. The cycle that
	// reached the bound was not put to her again — it was put to the operator.
	if len(harness.summonses) != 2 {
		t.Fatalf("summoned %d time(s), want the trip and the first blocked probe and nothing after the bound", len(harness.summonses))
	}
	// Each summons named where the loop stood, so she was told the harness would
	// stop asking.
	if len(loops) != 2 || !strings.Contains(loops[0], "cycle 1 of at most 2") || !strings.Contains(loops[1], "cycle 2 of at most 2") {
		t.Fatalf("summonses named the loop as %q, want each to say which cycle of at most two it was", loops)
	}
	if len(harness.releases) != 0 {
		t.Fatalf("released %d time(s), want an escalated hold left for the operator", len(harness.releases))
	}
	// The record: escalated by the harness, after two cycles, naming the last
	// probe and why it blocked.
	hold, held, _ := harness.Held()
	if !held || hold.Brake == nil || !hold.Brake.EscalatedByHarness() {
		t.Fatalf("hold = %#v, want the harness's escalation on its record", hold.Brake)
	}
	escalation := hold.Brake.Escalation
	if escalation.Cycles != 2 || escalation.Probe != "yoyodyne-five" || !strings.Contains(escalation.Reason, "independent review still required repair") {
		t.Fatalf("escalation = %#v, want two cycles, the fifth item as the last probe, and its stop reason", escalation)
	}
	if !hold.WaitsOnAPerson() {
		t.Fatal("the escalated hold does not wait on a person, want it the operator's")
	}
	if schedule.BrakeEscalated == nil || schedule.BrakeEscalated.Cycles != 2 {
		t.Fatalf("schedule.BrakeEscalated = %#v, want the escalation on the session's own account", schedule.BrakeEscalated)
	}
	if rendered := schedule.Render(); !strings.Contains(rendered, "escalated to the operator") || !strings.Contains(rendered, "2 summons-and-probe cycle(s)") {
		t.Fatalf("schedule = %s, want the escalation rendered with the cycles spent", rendered)
	}
	// Every surface reads the hold's own words: the operator's move, by the
	// harness's escalation, with the cycles and the last probe's stoppage.
	mover := sessions.lastMover(runstate.WatchBraked)
	for _, want := range []string{"the operator's", "the harness escalated it after 2 summons-and-probe cycles", "yoyodyne-five", "independent review still required repair", "yoyo release"} {
		if !strings.Contains(mover, want) {
			t.Fatalf("braked mover = %q, want it to carry %q", mover, want)
		}
	}
	if strings.Contains(mover, "the development manager escalated it") {
		t.Fatalf("braked mover = %q, want the escalation attributed to the harness rather than to her", mover)
	}
}

// The bound is a count of blocked probes and not of polls or of summonses. A
// probe that ended neither landed nor blocked decides nothing about the line
// and is not a cycle, and a hold configured with no bound goes round as it did
// before the bound existed.
func TestOnlyABlockedProbeCountsAsACycleAndNoBoundNeverEscalates(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three", "yoyodyne-four", "yoyodyne-five", "yoyodyne-six", "yoyodyne-seven")...)
	harness.blockedRuns = 3
	harness.cooldown = 5 * time.Minute
	harness.run = blockedStorm
	harness.summon = func(*scheduleHarness, BrakeSummons, int) (Fired, error) {
		return Fired{Task: "development-manager-sweep", Turns: 1, Summoned: "the intake brake"}, nil
	}
	sessions := &recordedSessions{}
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 30 }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Sessions: sessions, Now: harness.clock}
	if _, err := scheduler.Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	hold, held, _ := harness.Held()
	if !held || hold.Brake == nil || hold.Brake.EscalatedByHarness() {
		t.Fatalf("hold = %#v, want a loop with no bound never escalated by the harness", hold.Brake)
	}
	if hold.Brake.Cycles < 3 {
		t.Fatalf("cycles = %d, want the unbounded loop to have gone round at least three times in thirty polls", hold.Brake.Cycles)
	}
	if len(harness.summonses) != hold.Brake.Cycles+1 {
		t.Fatalf("summoned %d time(s) over %d cycle(s), want her summoned at the trip and after every blocked probe", len(harness.summonses), hold.Brake.Cycles)
	}
	// The loop is still named, so a reader of the hold can see it has no bound.
	if mover := sessions.lastMover(runstate.WatchBraked); !strings.Contains(mover, "no bound configured") {
		t.Fatalf("braked mover = %q, want the unbounded loop named", mover)
	}
}

// A hold the harness escalated is still hers to release. The bound stops the
// probing, and it does not stop the one decision that says the line is fine.
func TestHerReleaseStillLiftsAHoldTheHarnessEscalated(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three", "yoyodyne-four", "yoyodyne-five", "yoyodyne-six")...)
	harness.blockedRuns = 3
	harness.cooldown = 2 * time.Minute
	harness.cycleBound = 1
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		if id == "yoyodyne-five" {
			return h.complete(id), nil
		}
		return blockedStorm(h, id)
	}
	harness.summon = func(*scheduleHarness, BrakeSummons, int) (Fired, error) {
		return Fired{Task: "development-manager-sweep", Turns: 1, Summoned: "the intake brake"}, nil
	}
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		// Once the harness has escalated, she reads the line as fine and releases.
		if hold, held, _ := h.Held(); held && hold.Brake != nil && hold.Brake.EscalatedByHarness() && hold.Brake.Decision == "" {
			h.decideBrake(runstate.BrakeDecisionRelease, "the machine was fixed by hand")
		}
		return sleeps < 12
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.BrakeEscalated == nil {
		t.Fatalf("schedule = %s, want the harness to have escalated at one cycle", schedule.Render())
	}
	if len(harness.releases) != 1 || !strings.Contains(schedule.Released[0].Reason, "the development manager decided to release it") {
		t.Fatalf("releases = %#v, released = %#v, want her release honoured over the harness's escalation", harness.releases, schedule.Released)
	}
	// Four was the probe; five and six were chosen after her release lifted it.
	if order := harness.pullOrder(); len(order) != 6 || order[4] != "yoyodyne-five" {
		t.Fatalf("pulled %v, want the line choosing again after her release", order)
	}
}

// The one brake hold that waits on a person: she escalated it. No probe starts
// however long the cooldown has run out, and every surface names the operator.
func TestAnEscalatedBrakeHoldWaitsOnTheOperator(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three", "yoyodyne-four")...)
	harness.blockedRuns = 3
	harness.cooldown = 2 * time.Minute
	harness.run = blockedStorm
	harness.summon = func(h *scheduleHarness, _ BrakeSummons, _ int) (Fired, error) {
		h.decideBrake(runstate.BrakeDecisionEscalate, "the reviewer's findings dispute the design; the operator has to rule")
		return Fired{Task: "development-manager-sweep", Turns: 1, Summoned: "the intake brake"}, nil
	}
	sessions := &recordedSessions{}
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 6 }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Sessions: sessions, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 3 {
		t.Fatalf("started = %s, want nothing started under an escalated hold", schedule.Render())
	}
	if len(harness.releases) != 0 {
		t.Fatalf("released %d time(s), want an escalated hold left for the operator", len(harness.releases))
	}
	if mover := sessions.lastMover(runstate.WatchBraked); !strings.Contains(mover, "the operator's") || !strings.Contains(mover, "escalated") {
		t.Fatalf("braked mover = %q, want the operator named because she escalated it", mover)
	}
	if reason := sessions.said(runstate.WatchBraked); !strings.Contains(reason, "the harness's own brake placed it") {
		t.Fatalf("braked reason = %q, want the brake still named as what placed the hold", reason)
	}
}

// Environmental stops are verdicts on nothing, so they count toward nothing.
// Two of the three stops that tripped the brake on 2026-09-19 were of this
// class, and the same three replayed with them classified do not trip it.
func TestEnvironmentalStopsDoNotCountTowardTheBrake(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three", "yoyodyne-four")...)
	harness.blockedRuns = 3
	now := time.Date(2026, 9, 19, 17, 56, 0, 0, time.UTC)
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		h.retire(id)
		outcome := Outcome{RunID: "run-" + id, WorkItemID: id, Status: runstate.StatusFailed, Blocked: true, Failure: "stopped"}
		switch id {
		case "yoyodyne-two":
			// An approved change the environment stopped short of its promotion.
			outcome.IntegrationStop = &runstate.IntegrationStop{
				Cause: runstate.CauseTransportFailure, Phase: runstate.PhaseIntegrating, RecordedAt: now,
			}
		case "yoyodyne-three":
			// A round the settle refused as environmental.
			outcome.Environmental = &runstate.EnvironmentalRefusal{
				Cause: runstate.CauseDirtyPrimary, RecordedAt: now, Settled: true, Refused: true,
			}
		}
		return outcome, nil
	}
	harness.summon = func(*scheduleHarness, BrakeSummons, int) (Fired, error) {
		return Fired{}, errors.New("nobody should be summoned")
	}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Braked != nil {
		t.Fatalf("schedule braked on %#v, want environmental stops counted toward nothing", schedule.Braked)
	}
	if len(harness.summonses) != 0 {
		t.Fatalf("summoned %d time(s), want nobody summoned over a brake that did not trip", len(harness.summonses))
	}
	if len(schedule.Started) != 4 {
		t.Fatalf("started = %d run(s), want every item pulled: %s", len(schedule.Started), schedule.Render())
	}
	// Two verdicts with an environmental stop between them are still two, not
	// three: the environmental stop neither counts nor clears.
	if schedule.BlockedInARow != 2 {
		t.Fatalf("blocked in a row = %d, want the two verdicts counted and the environmental stops passed over", schedule.BlockedInARow)
	}
}

// A promotion refused because the target branch diverged from the remote's is a
// catch-up the harness will not make, which is a stop the environment made and
// a verdict on nothing. On 2026-09-21 three identical ones tripped the brake
// over a divergence each run's own blocker had already put in front of a
// person; replayed here, the same three leave the brake standing open and the
// line choosing, while every one of them still stops on the item as it did.
func TestDivergedTargetRefusalsDoNotCountTowardTheBrake(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three", "yoyodyne-four")...)
	harness.blockedRuns = 3
	// The one refusal, in the words the catch-up gives for it, met by every run
	// that reaches integration until somebody unwedges the branch.
	held := gitworktree.Catchup{
		TargetBranch: "main",
		LocalCommit:  "4d7e805",
		RemoteCommit: "9f1c2ab",
		Held:         "main on origin is at 9f1c2ab, which does not contain the local main at 4d7e805; only a person can say which history is right",
	}
	var blocked []Outcome
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		h.retire(id)
		if id == "yoyodyne-four" {
			return h.complete(id), nil
		}
		catchup := held
		outcome := Outcome{
			RunID:          "run-" + id,
			WorkItemID:     id,
			Status:         runstate.StatusFailed,
			Blocked:        true,
			Failure:        "main cannot be brought onto origin before promoting: " + held.Held,
			DivergedTarget: &catchup,
		}
		h.mu.Lock()
		blocked = append(blocked, outcome)
		h.mu.Unlock()
		return outcome, errors.New(outcome.Failure)
	}
	harness.summon = func(*scheduleHarness, BrakeSummons, int) (Fired, error) {
		return Fired{}, errors.New("nobody should be summoned over a diverged target")
	}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	// Three refusals in a row, exactly the configured trip, and no hold.
	if schedule.Braked != nil {
		t.Fatalf("schedule braked on %#v, want a diverged target counted toward nothing", schedule.Braked)
	}
	if _, held, _ := harness.Held(); held {
		t.Fatal("intake is held, want the brake standing open over diverged-target refusals")
	}
	if len(harness.summonses) != 0 {
		t.Fatalf("summoned %d time(s), want nobody summoned over a brake that did not trip", len(harness.summonses))
	}
	if schedule.BlockedInARow != 0 {
		t.Fatalf("blocked in a row = %d, want none of the three refusals counted", schedule.BlockedInARow)
	}
	// The line kept choosing: the fourth item was pulled behind the three.
	if order := harness.pullOrder(); len(order) != 4 || order[3] != "yoyodyne-four" {
		t.Fatalf("pulled %v, want every item pulled with the brake standing open", order)
	}
	// And the refusals reached the person the same way they always did — each
	// run stopped on its item, with the divergence as its blocker — because the
	// brake standing open is not the escalation going quiet.
	if len(blocked) != 3 {
		t.Fatalf("%d run(s) stopped on the divergence, want three", len(blocked))
	}
	for _, outcome := range blocked {
		if !outcome.Blocked || !strings.Contains(outcome.Failure, "only a person can say which history is right") {
			t.Fatalf("outcome %#v, want the run still stopped on the item with the divergence as its blocker", outcome)
		}
	}
	for _, started := range schedule.Started {
		if started.WorkItemID != "yoyodyne-four" && !strings.Contains(started.Failure, "cannot be brought onto origin") {
			t.Fatalf("started %#v, want the refusal reported on the schedule as the run's own failure", started)
		}
	}
}

// A summons that cannot be made is not a hold that waits. It is written on the
// hold, and the cooldown's probe decides the line exactly as it would have.
func TestASummonsThatFailsLeavesTheHoldToTheCooldown(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three", "yoyodyne-four")...)
	harness.blockedRuns = 3
	harness.cooldown = 3 * time.Minute
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		if id == "yoyodyne-four" {
			return h.complete(id), nil
		}
		return blockedStorm(h, id)
	}
	harness.summon = func(*scheduleHarness, BrakeSummons, int) (Fired, error) {
		return Fired{}, errors.New("the development manager's conversation could not be opened")
	}
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 6 }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if !strings.Contains(schedule.BrakeProblem, "could not be summoned") {
		t.Fatalf("brake problem = %q, want the failed summons said out loud", schedule.BrakeProblem)
	}
	if len(harness.releases) != 1 || harness.releases[0].Brake == nil || !strings.Contains(harness.releases[0].Brake.SummonProblem, "could not be summoned") {
		t.Fatalf("releases = %#v, want the hold lifted by the probe with the failed summons on its record", harness.releases)
	}
}

// The brake never summons anybody over a hold it did not place. The operator's
// standing hold is theirs, and a summons over it would ask the development
// manager to decide about a switch she does not hold.
func TestTheBrakeSummonsNobodyOverTheOperatorsHold(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")...)
	harness.blockedRuns = 3
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		if id == "yoyodyne-three" {
			h.mu.Lock()
			h.held = &runstate.IntakeHold{
				SchemaVersion: runstate.IntakeHoldSchemaVersion,
				ProductID:     "yoyodyne",
				HeldAt:        time.Date(2026, 9, 19, 17, 0, 0, 0, time.UTC),
				HeldBy:        runstate.IntakeHolderOperator,
				Reason:        "reordering the queue",
			}
			h.mu.Unlock()
		}
		return blockedStorm(h, id)
	}
	harness.summon = func(*scheduleHarness, BrakeSummons, int) (Fired, error) {
		return Fired{}, errors.New("nobody should be summoned")
	}
	sessions := &recordedSessions{}
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 3 }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Sessions: sessions, Now: harness.clock}
	if _, err := scheduler.Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(harness.summonses) != 0 {
		t.Fatalf("summoned %d time(s), want nobody summoned over the operator's hold", len(harness.summonses))
	}
	if len(harness.releases) != 0 {
		t.Fatalf("released %d time(s), want the operator's hold never lifted by the harness", len(harness.releases))
	}
	if mover := sessions.lastMover(runstate.WatchBraked); mover != "" {
		t.Fatalf("braked mover = %q, want the operator's hold left to the surfaces' own clause", mover)
	}
	if reason := sessions.said(runstate.WatchBraked); !strings.Contains(reason, "it stays held until somebody releases it") {
		t.Fatalf("braked reason = %q, want the operator's hold reported as waiting on them", reason)
	}
}

// A usage window resetting past the maximum pause stops every run the session
// starts into it, and on 2026-09-23 three such stops in a row tripped the brake
// at 07:32 over a window nobody could act on. Replayed here with the stop the
// pipeline now makes of it — cancelled, its claim given back, and a settled
// usage-window refusal naming the reset — the same three count toward nothing:
// the brake stays open, no item is remembered as tried, and each is held only
// until the window resets and is then pulled again by itself.
func TestAUsageWindowStopCountsTowardNothingInAWatchingSession(t *testing.T) {
	t.Parallel()

	ids := []string{"yoyodyne-one", "yoyodyne-two", "yoyodyne-three"}
	harness := newScheduleHarness(readyItems(ids...)...)
	harness.blockedRuns = 3
	harness.capacity = 3
	reset := harness.now.Add(92 * time.Hour)
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		if !h.clock().Before(reset) {
			return h.complete(id), nil
		}
		// The item is not retired: its claim was given back, so it reads as ready
		// at every pull, and only the session's own hold keeps it from being
		// started straight back into the closed window.
		resetsAt := reset
		return Outcome{
			RunID:          "run-" + id,
			WorkItemID:     id,
			Status:         runstate.StatusCancelled,
			UsageLimitKind: "seven_day",
			Failure:        "this run was refused by an exhausted seven_day usage limit and the harness will not wait for it: waiting until " + reset.Format(time.RFC3339) + " would take this run past the 6h0m0s maximum pause",
			Environmental: &runstate.EnvironmentalRefusal{
				Cause: runstate.CauseUsageWindow, RecordedAt: h.clock(), ResetsAt: &resetsAt,
				Settled: true, Refused: true,
			},
		}, nil
	}
	harness.summon = func(*scheduleHarness, BrakeSummons, int) (Fired, error) {
		return Fired{}, errors.New("nobody should be summoned over a usage window")
	}
	var beforeReset int
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		switch sleeps {
		case 3:
			// Three quiet polls inside the window, then the window resets.
			beforeReset = len(h.pullOrder())
			h.mu.Lock()
			h.now = reset.Add(time.Minute)
			h.mu.Unlock()
		case 6:
			return false
		}
		return true
	}
	sessions := &recordedSessions{}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Sessions: sessions, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	// Three stops in a row at a trip of three, and no hold.
	if schedule.Braked != nil {
		t.Fatalf("schedule braked on %#v, want a usage window counted toward nothing", schedule.Braked)
	}
	if _, held, _ := harness.Held(); held {
		t.Fatal("intake is held over a usage window")
	}
	if len(harness.summonses) != 0 {
		t.Fatalf("summoned %d time(s), want nobody summoned", len(harness.summonses))
	}
	if schedule.BlockedInARow != 0 {
		t.Fatalf("blocked in a row = %d, want the window stops counted toward nothing", schedule.BlockedInARow)
	}
	if schedule.Failed() {
		t.Fatalf("the pass reports a failure over stops nothing judged: %s", schedule.Render())
	}
	// Each item started once into the window and not again inside it, then once
	// more after the reset, with nobody having changed anything about it.
	if beforeReset != len(ids) {
		t.Fatalf("starts inside the window = %d, want each item started once and then held: %v", beforeReset, harness.pullOrder())
	}
	if starts := len(harness.pullOrder()); starts != 2*len(ids) {
		t.Fatalf("starts = %d, want each item pulled again once the window reset: %v", starts, harness.pullOrder())
	}
	// Inside the window the session named the wait and the reset, and held the
	// items in the window's own class rather than as items already tried.
	held := 0
	for _, transition := range sessions.recorded() {
		for _, group := range transition.passedOver.Groups {
			// A run still going is passed over as already started, which is
			// ordinary; one the window ended must never be.
			if group.Class == runstate.PassedOverAlreadyTried {
				for _, reason := range group.Reasons {
					if !strings.Contains(reason, "has not ended yet") {
						t.Fatalf("an item stopped by the window was remembered as tried: %#v", group)
					}
				}
			}
			if group.Class != runstate.PassedOverWaitingOnUsageWindow {
				continue
			}
			held = max(held, group.Count)
			if !transition.window || transition.windowResetsAt == nil || !transition.windowResetsAt.Equal(reset) {
				t.Fatalf("transition window=%t resets=%v, want the session inside the window until %s", transition.window, transition.windowResetsAt, reset)
			}
			if !strings.Contains(transition.reason, reset.Format(time.RFC3339)) {
				t.Fatalf("idle reason = %q, want the reset named", transition.reason)
			}
		}
	}
	if held != len(ids) {
		t.Fatalf("at most %d item(s) held on the usage window, want all three once every run had ended: %#v", held, sessions.recorded())
	}
}
