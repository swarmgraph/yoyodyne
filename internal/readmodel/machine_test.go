package readmodel

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type machineHistory struct{ observations []runstate.MachineObservation }

func (m machineHistory) MachineHistory() ([]runstate.MachineObservation, error) {
	return m.observations, nil
}

func TestMissedPassCausesComeFromTheGapRatherThanThePreviousFailure(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 30, 16, 52, 0, 0, time.UTC)
	from := now.Add(-3 * time.Hour)
	for _, test := range []struct {
		name         string
		observations []runstate.MachineObservation
		passes       []runstate.WatchTransition
		sweeps       []runstate.Sweep
		want         string
	}{
		{"asleep", []runstate.MachineObservation{{At: from, Watching: true, Power: []runstate.PowerEvent{{At: from, Source: "pmset: Sleep"}, {At: now.Add(-time.Minute), Awake: true, Source: "pmset: Wake"}}}}, nil, nil, "the machine was asleep"},
		{"down", []runstate.MachineObservation{{At: from}, {At: from.Add(time.Minute), Watching: true}}, nil, nil, "the harness was not watching"},
		{"waiting", []runstate.MachineObservation{{At: from, Watching: true}}, []runstate.WatchTransition{{At: from, SessionID: "one", RecurringPass: &runstate.WatchPass{Task: "another-pass", At: from}}}, []runstate.Sweep{{Task: "another-pass", StartedAt: from, EndedAt: now}}, "waiting its turn behind the recurring pass of another-pass"},
		{"unknown", []runstate.MachineObservation{{At: from, Watching: true}}, nil, []runstate.Sweep{{Task: "owed-pass", StartedAt: from.Add(-time.Hour), EndedAt: from, Problem: "previous pass failed"}}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			availability := ReadWatchAvailability(Sources{Machine: machineHistory{test.observations}, Sessions: fakeSessions{transitions: test.passes}, Sweeps: fakeSweeps{recorded: test.sweeps}, Now: func() time.Time { return now }})
			why := availability.Explain(from, now, "owed-pass")
			if test.want == "" && why != "" || test.want != "" && !strings.Contains(why, test.want) {
				t.Fatalf("cause = %q, want %q", why, test.want)
			}
			if strings.Contains(why, "previous pass failed") {
				t.Fatal("previous failure was blamed for this gap")
			}
			if test.name == "waiting" && !availability.WaitingBehindPass(from, now, "owed-pass") {
				t.Fatal("serial pass wait was not identified")
			}
		})
	}
}

func TestServicesSleepAndDowntimeAreCountedOnceAndUseLocalTime(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 30, 16, 52, 0, 0, time.UTC)
	from := now.Add(-3 * time.Hour)
	var observations []runstate.MachineObservation
	for at := from; !at.After(now); at = at.Add(time.Minute) {
		observations = append(observations, runstate.MachineObservation{At: at, Watching: at.Equal(now)})
	}
	observations[0].Power = []runstate.PowerEvent{{At: from}, {At: now.Add(-time.Hour), Awake: true}}
	availability := ReadWatchAvailability(Sources{Machine: machineHistory{observations}, Now: func() time.Time { return now }})
	if availability.LastGap != 3*time.Hour {
		t.Fatalf("overlapping sleep and downtime = %s, want 3h", availability.LastGap)
	}
	rendered := (Standing{Services: &Services{Recorded: true, SupervisorRunning: true, Availability: &availability}}).RenderServices()
	for _, want := range []string{"last machine sleep: " + localMoment(from), "last machine wake: " + localMoment(now.Add(-time.Hour)), "without the harness watching: 3h0m0s"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("services = %s, want %s", rendered, want)
		}
	}
	unknown := ReadWatchAvailability(Sources{})
	if unknown.Problem == "" || unknown.Explain(from, now, "owed-pass") != "" {
		t.Fatalf("missing history invented a cause: %+v", unknown)
	}
}

func TestACompletedPassDoesNotAccountForALaterMiss(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 30, 16, 52, 0, 0, time.UTC)
	start := now.Add(-4 * time.Hour)
	availability := ReadWatchAvailability(Sources{Machine: machineHistory{}, Sessions: fakeSessions{transitions: []runstate.WatchTransition{{At: start, RecurringPass: &runstate.WatchPass{Task: "earlier-pass", At: start}}}}, Sweeps: fakeSweeps{recorded: []runstate.Sweep{{Task: "earlier-pass", StartedAt: start, EndedAt: start.Add(time.Hour)}}}, Now: func() time.Time { return now }})
	if why := availability.Explain(now.Add(-time.Hour), now, "owed-pass"); why != "" {
		t.Fatalf("earlier pass blamed: %s", why)
	}
}

func TestMissingWakeAndPassEndingLeaveTheirDurationsUnknown(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 30, 16, 52, 0, 0, time.UTC)
	start := now.Add(-3 * time.Hour)
	availability := ReadWatchAvailability(Sources{
		Machine:  machineHistory{[]runstate.MachineObservation{{At: now, Watching: true, Power: []runstate.PowerEvent{{At: start, Source: "pmset: Sleep"}}}}},
		Sessions: fakeSessions{transitions: []runstate.WatchTransition{{At: start, RecurringPass: &runstate.WatchPass{Task: "unfinished-pass", At: start}}}},
		Now:      func() time.Time { return now },
	})
	if !availability.LastSleep.Equal(start) || availability.LastGap != 0 || !strings.Contains(availability.Problem, "duration is unknown") {
		t.Fatalf("missing wake invented a duration: %+v", availability)
	}
	cause := availability.Cause(start, now, "owed-pass")
	if cause.Why != "" || !strings.Contains(cause.Problem, "ending is unrecorded") || cause.Waiting {
		t.Fatalf("missing pass ending invented a cause: %+v", cause)
	}
}

func TestStaleSchedulerSamplesDoNotEstablishDowntimeThroughAnObservationGap(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 9, 30, 13, 52, 0, 0, time.UTC)
	now := start.Add(3 * time.Hour)
	for _, test := range []struct {
		name         string
		observations []runstate.MachineObservation
		wantGap      time.Duration
	}{
		{"stale down sample", []runstate.MachineObservation{{At: start}}, 0},
		{"stale consecutive down samples", []runstate.MachineObservation{{At: start}, {At: start.Add(time.Minute)}}, time.Minute},
		{"supervisor restarted after a long gap", []runstate.MachineObservation{{At: start}, {At: start.Add(time.Minute)}, {At: now, Watching: true}}, time.Minute},
		{"supervisor restarted and still found the scheduler down", []runstate.MachineObservation{{At: start}, {At: now}}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			availability := ReadWatchAvailability(Sources{Machine: machineHistory{test.observations}, Now: func() time.Time { return now }})
			if availability.LastGap != test.wantGap {
				t.Fatalf("downtime = %s, want only %s supported by adjacent samples", availability.LastGap, test.wantGap)
			}
			cause := availability.Cause(now.Add(-time.Hour), now, "owed-pass")
			if cause.Why != "" || !strings.Contains(cause.Problem, "whether the harness was watching is unknown") {
				t.Fatalf("a later miss was blamed on a stale sample: %+v", cause)
			}
			rendered := (Standing{Services: &Services{Recorded: true, SupervisorRunning: true, Availability: &availability}}).RenderServices()
			if !strings.Contains(rendered, "scheduler observations incomplete:") || strings.Contains(rendered, "without the harness watching: 3h") {
				t.Fatalf("services overstated the gap: %s", rendered)
			}
		})
	}
}

func TestSchedulerDowntimeAllowsTheSupervisorsPollingDelay(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 9, 30, 13, 52, 0, 0, time.UTC)
	for _, interval := range []time.Duration{
		runstate.MachineObservationInterval + time.Second,
		runstate.MachineObservationInterval + 5*time.Second + 250*time.Millisecond,
		runstate.MachineObservationWindow,
	} {
		t.Run(interval.String(), func(t *testing.T) {
			now := start.Add(2 * interval)
			availability := ReadWatchAvailability(Sources{Machine: machineHistory{[]runstate.MachineObservation{
				{At: start}, {At: start.Add(interval)}, {At: now, Watching: true},
			}}, Now: func() time.Time { return now }})
			if availability.LastGap != 2*interval || availability.ObservationProblem != "" {
				t.Fatalf("consecutive delayed looks lost downtime: %+v", availability)
			}
			cause := availability.Cause(start, now, "owed-pass")
			if !strings.Contains(cause.Why, "scheduler was observed down") || cause.Problem != "" {
				t.Fatalf("missed pass lost observed downtime: %+v", cause)
			}
			rendered := (Standing{Services: &Services{Recorded: true, SupervisorRunning: true, Availability: &availability}}).RenderServices()
			if !strings.Contains(rendered, "last interval without the harness watching: "+(2*interval).Round(time.Second).String()) {
				t.Fatalf("services lost observed downtime: %s", rendered)
			}
		})
	}
}

func TestSchedulerObservationsBeyondTheWindowRemainUnknown(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 9, 30, 13, 52, 0, 0, time.UTC)
	now := start.Add(runstate.MachineObservationWindow + time.Nanosecond)
	availability := ReadWatchAvailability(Sources{Machine: machineHistory{[]runstate.MachineObservation{
		{At: start}, {At: now, Watching: true},
	}}, Now: func() time.Time { return now }})
	cause := availability.Cause(start, now, "owed-pass")
	if availability.LastGap != 0 || cause.Why != "" || !strings.Contains(cause.Problem, "whether the harness was watching is unknown") {
		t.Fatalf("a gap beyond the observation window became downtime: %+v, %+v", availability, cause)
	}
}

func TestSchedulerPresenceOverridesAStopWhoseRestartWasNotRecorded(t *testing.T) {
	t.Parallel()
	stopped := time.Date(2026, 9, 30, 13, 52, 0, 0, time.UTC)
	watching := stopped.Add(3 * time.Hour)
	now := watching.Add(time.Minute)
	for _, test := range []struct {
		name         string
		observations []runstate.MachineObservation
		laterOpening bool
		wantGap      time.Duration
	}{
		{
			name: "missing opening",
			observations: []runstate.MachineObservation{
				{At: watching, Watching: true}, {At: now, Watching: true},
			},
		},
		{
			name: "down samples before the missing opening",
			observations: []runstate.MachineObservation{
				{At: stopped}, {At: stopped.Add(time.Minute)},
				{At: watching, Watching: true}, {At: now, Watching: true},
			},
			wantGap: time.Minute,
		},
		{
			name: "opening recorded after the scheduler was observed running",
			observations: []runstate.MachineObservation{
				{At: watching, Watching: true}, {At: now, Watching: true},
			},
			laterOpening: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			transitions := []runstate.WatchTransition{{At: stopped, SessionID: "old-watch", State: runstate.WatchStopped}}
			if test.laterOpening {
				transitions = append(transitions, runstate.WatchTransition{At: now, SessionID: "new-watch", State: runstate.WatchIdle})
			}
			availability := ReadWatchAvailability(Sources{
				Machine: machineHistory{test.observations}, Sessions: fakeSessions{transitions: transitions},
				Now: func() time.Time { return now },
			})
			if availability.LastGap != test.wantGap {
				t.Fatalf("downtime = %s, want only %s supported by observations", availability.LastGap, test.wantGap)
			}
			before := availability.Cause(watching.Add(-time.Hour), watching, "owed-pass")
			if before.Why != "" || !strings.Contains(before.Problem, "restart time was not recorded and is unknown") {
				t.Fatalf("missing restart became established downtime: %+v", before)
			}
			after := availability.Cause(watching, now, "owed-pass")
			if after.Why != "" || after.Problem != "" {
				t.Fatalf("a pass while the scheduler was observed running was blamed on the stop: %+v", after)
			}
			rendered := (Standing{Services: &Services{Recorded: true, SupervisorRunning: true, Availability: &availability}}).RenderServices()
			if !strings.Contains(rendered, "restart time was not recorded and is unknown") || strings.Contains(rendered, "without the harness watching: 3h") {
				t.Fatalf("services failed to distinguish downtime from an unknown restart: %s", rendered)
			}
		})
	}
}

func TestMissingWatchOpeningRetainsBoundedDowntimeAndMarksTheRestartUncertain(t *testing.T) {
	t.Parallel()
	stopped := time.Date(2026, 9, 30, 13, 52, 0, 0, time.UTC)
	interval := runstate.MachineObservationInterval + 5*time.Second
	watching := stopped.Add(3 * interval)
	now := watching.Add(interval)
	availability := ReadWatchAvailability(Sources{
		Machine: machineHistory{[]runstate.MachineObservation{
			{At: stopped.Add(interval)}, {At: stopped.Add(2 * interval)},
			{At: watching, Watching: true}, {At: now, Watching: true},
		}},
		Sessions: fakeSessions{transitions: []runstate.WatchTransition{{At: stopped, State: runstate.WatchStopped}}},
		Now:      func() time.Time { return now },
	})
	if availability.LastGap != 3*interval {
		t.Fatalf("bounded downtime after the stop = %s, want %s", availability.LastGap, 3*interval)
	}
	cause := availability.Cause(stopped.Add(2*interval), watching, "owed-pass")
	if !strings.Contains(cause.Why, "scheduler was observed down") || !strings.Contains(cause.Problem, "restart time was not recorded and is unknown") {
		t.Fatalf("bounded samples lost downtime or invented an exact restart: %+v", cause)
	}
	if cause := availability.Cause(watching, now, "owed-pass"); cause.Why != "" || cause.Problem != "" {
		t.Fatalf("stop was extended past the observed restart: %+v", cause)
	}
}

func TestAStopWithoutFurtherPresenceEvidenceDoesNotProveDowntimeThroughNow(t *testing.T) {
	t.Parallel()
	stopped := time.Date(2026, 9, 30, 13, 52, 0, 0, time.UTC)
	now := stopped.Add(3 * time.Hour)
	availability := ReadWatchAvailability(Sources{
		Machine:  machineHistory{[]runstate.MachineObservation{{At: stopped.Add(-time.Minute), Watching: true}}},
		Sessions: fakeSessions{transitions: []runstate.WatchTransition{{At: stopped, State: runstate.WatchStopped}}},
		Now:      func() time.Time { return now },
	})
	cause := availability.Cause(now.Add(-time.Hour), now, "owed-pass")
	if availability.LastGap != 0 || cause.Why != "" || !strings.Contains(cause.Problem, "whether the harness was watching is unknown") {
		t.Fatalf("a lone stop was treated as continuous downtime: %+v, %+v", availability, cause)
	}
}

func TestRecordedWatchStopAndOpeningStillGiveExactDowntime(t *testing.T) {
	t.Parallel()
	stopped := time.Date(2026, 9, 30, 13, 52, 0, 0, time.UTC)
	watching := stopped.Add(3 * time.Hour)
	now := watching.Add(time.Minute)
	availability := ReadWatchAvailability(Sources{
		Machine: machineHistory{[]runstate.MachineObservation{{At: watching, Watching: true}, {At: now, Watching: true}}},
		Sessions: fakeSessions{transitions: []runstate.WatchTransition{
			{At: stopped, SessionID: "old-watch", State: runstate.WatchStopped},
			{At: watching, SessionID: "new-watch", State: runstate.WatchWatching},
		}},
		Now: func() time.Time { return now },
	})
	if availability.LastGap != 3*time.Hour || availability.ObservationProblem != "" {
		t.Fatalf("recorded opening lost exact downtime: %+v", availability)
	}
	if cause := availability.Cause(stopped, watching, "owed-pass"); !strings.Contains(cause.Why, "scheduler was observed down") || cause.Problem != "" {
		t.Fatalf("recorded stop/opening did not explain the gap: %+v", cause)
	}
}

func TestConcurrentPassDoesNotExplainAMissedPass(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	availability := ReadWatchAvailability(Sources{
		Sessions: fakeSessions{transitions: []runstate.WatchTransition{{At: start, RecurringPass: &runstate.WatchPass{Task: "other-pass", At: start, Concurrent: true}}}},
		Sweeps:   fakeSweeps{recorded: []runstate.Sweep{{Task: "other-pass", StartedAt: start, EndedAt: end}}},
		Now:      func() time.Time { return end },
	})
	cause := availability.Cause(start, end, "owed-pass")
	if cause.Waiting || cause.WaitingFor != 0 || strings.Contains(cause.Why, "waiting") {
		t.Fatalf("cause: %+v", cause)
	}
}
func TestWaitingDurationCountsOverlappingPassesOnce(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	availability := WatchAvailability{passes: []watchGap{
		{from: start.Add(-time.Hour), to: start.Add(20 * time.Minute), task: "a", known: true},
		{from: start.Add(10 * time.Minute), to: start.Add(40 * time.Minute), task: "b", known: true},
	}}
	if got := availability.waitingFor(start, start.Add(30*time.Minute), "owed"); got != 30*time.Minute {
		t.Fatalf("wait: %s", got)
	}
}
