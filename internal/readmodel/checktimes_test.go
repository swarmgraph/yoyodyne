package readmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The commands as this repository's configuration writes them, so a heavy
// check is named the way execution.heavy_checks names it from a command that
// carries more than its name.
const (
	raceCommand = `make race CHANGED_GO_PACKAGES="${YOYODYNE_CHANGED_GO_PACKAGES-./...}"`
	testCommand = `make test CHANGED_GO_PACKAGES="${YOYODYNE_CHANGED_GO_PACKAGES-./...}"`
)

var heavyChecks = map[string]int{"make race": 1, "make test": 2}

// started and finished are a check's start and finish events as the check
// runner writes them. told is the start event's whole-module fields, empty for
// an event written before it carried them.
func started(at time.Time, command, told string) execution.Event {
	return event(at, execution.EventCommandStarted, fmt.Sprintf(`{"command":%q,"kind":"check"%s}`, command, told))
}

func finished(at time.Time, command string, passed bool, elapsed time.Duration) execution.Event {
	return event(at, execution.EventCommandCompleted, fmt.Sprintf(
		`{"command":%q,"could_not_run":"","elapsed":%q,"exit_code":0,"kind":"check","passed":%t,"stage_elapsed":"1m0s","stage_timeout":"1h30m0s","status":"succeeded","stopped_by_caller":false,"stopped_by_stage":false,"timeout":"45m0s"}`,
		command, elapsed.String(), passed))
}

const (
	toldWhole    = `,"whole_module":true,"whole_module_reason":"go.mod changed, which every package depends on"`
	toldNarrowed = `,"whole_module":false`
)

// checkTimesFixture is three runs' event logs around the week of Monday 2
// November in Pacific time:
//
//   - old, a run whose log was written before a check's start said what it was
//     told: a make race before its last check stage, whose scope nothing
//     records, then that stage over the merge candidate, which its record says
//     was told the whole module; a make fmtcheck, which is not heavy, between;
//   - fresh, a run whose starts say it: a make race told the whole module for a
//     change to go.mod, a make test told only the packages that failed, and a
//     make race its run stopped before it launched;
//   - earlier, a run in the week before, told only the packages.
func checkTimesFixture() (fakeDeliveryRuns, time.Time) {
	zone := DeliveryZone()
	monday := time.Date(2026, 11, 2, 0, 0, 0, 0, zone)
	now := monday.Add(3*24*time.Hour + 12*time.Hour)
	at := func(day int, hour, minute int) time.Time {
		return monday.AddDate(0, 0, day).Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
	}

	stageStart, stageEnd := at(1, 10, 0), at(1, 11, 0)
	old := runstate.State{
		RunID: "run-old", WorkItemID: "yoyodyne-ifd.1", Status: runstate.StatusSucceeded,
		StartedAt: at(1, 8, 0), UpdatedAt: at(1, 11, 30),
		CheckStage: &runstate.CheckStage{
			StartedAt: stageStart, FinishedAt: &stageEnd,
			Narrowed: "the whole module (the merge candidate is checked whole before it is promoted); before review: ./internal/dashboard; make getting-started added because the change touches internal/dashboard/assets/backlog-count.js, which scripts/walk-adoption.paths lists",
		},
	}
	fresh := runstate.State{RunID: "run-fresh", WorkItemID: "yoyodyne-ifd.2", Status: runstate.StatusRunning, StartedAt: at(2, 8, 0), UpdatedAt: at(2, 10, 0)}
	earlier := runstate.State{RunID: "run-earlier", WorkItemID: "yoyodyne-ifd.3", Status: runstate.StatusFailed, StartedAt: at(-3, 8, 0), UpdatedAt: at(-3, 10, 0)}

	return fakeDeliveryRuns{
		recorded: []runstate.State{old, fresh, earlier},
		events: map[string][]execution.Event{
			// Waited 0, ran 25 minutes, before the stage the record describes.
			"run-old": timeline("run-old",
				started(at(1, 8, 30), raceCommand, ""),
				finished(at(1, 8, 55), raceCommand, true, 25*time.Minute),
				started(at(1, 10, 0), "make fmtcheck", ""),
				finished(at(1, 10, 1), "make fmtcheck", true, time.Minute),
				// Waited 3 minutes for its place, then ran 20.
				started(at(1, 10, 1), raceCommand, ""),
				finished(at(1, 10, 24), raceCommand, true, 20*time.Minute),
				// Launched at once and ran 18.
				started(at(1, 10, 24), testCommand, ""),
				finished(at(1, 10, 42), testCommand, true, 18*time.Minute),
			),
			"run-fresh": timeline("run-fresh",
				// Waited a minute, ran 10.
				started(at(2, 9, 0), raceCommand, toldWhole),
				finished(at(2, 9, 11), raceCommand, true, 10*time.Minute),
				// Failed after 5 minutes.
				started(at(2, 9, 11), testCommand, toldNarrowed),
				finished(at(2, 9, 16), testCommand, false, 5*time.Minute),
				// Stopped by its run while it waited: it never launched.
				started(at(2, 9, 16), raceCommand, toldNarrowed),
				finished(at(2, 9, 20), raceCommand, false, 0),
			),
			"run-earlier": timeline("run-earlier",
				started(at(-3, 9, 0), raceCommand, toldNarrowed),
				finished(at(-3, 9, 30), raceCommand, true, 30*time.Minute),
			),
		},
	}, now
}

func summary(seconds ...int64) *DurationSummary {
	spans := make([]time.Duration, 0, len(seconds))
	for _, each := range seconds {
		spans = append(spans, time.Duration(each)*time.Second)
	}
	return summarizeDurations(spans)
}

func TestCheckTimesAreReadWeekByWeekFromEveryRunsEventLog(t *testing.T) {
	t.Parallel()
	runs, now := checkTimesFixture()
	reading := ReadCheckTimes(CheckTimesSources{Runs: runs, HeavyChecks: heavyChecks, Now: func() time.Time { return now }})
	if reading.NotRecorded != "" || len(reading.NotRead) != 0 {
		t.Fatalf("reading = %+v, want every source read", reading)
	}
	if reading.Zone != DeliveryZoneName || len(reading.Weeks) != deliveryWeeks {
		t.Fatalf("reading covers %d weeks in %s, want %d in %s", len(reading.Weeks), reading.Zone, deliveryWeeks, DeliveryZoneName)
	}
	current, before := reading.Weeks[len(reading.Weeks)-1], reading.Weeks[len(reading.Weeks)-2]
	if current.Label != "2026-11-02" || !current.Current || before.Label != "2026-10-26" {
		t.Fatalf("the last two weeks are %s and %s, want the week of 26 October and this one", before.Label, current.Label)
	}

	// make race: three launches this week, every one passed; the median of
	// 10, 20, and 25 minutes is 20, and of waits of 0, 1, and 3 minutes is 1.
	// One was told the whole module by its start, one by its run's record of
	// the stage, and one ran before that stage, where nothing records it.
	// make test: the one that passed took 18 minutes; the one that failed is
	// counted as a launch and waited nothing.
	want := []CheckTimes{
		{
			Check: "make race", Launches: 3, Passed: 3,
			PassedRan: summary(600, 1200, 1500), Waited: summary(0, 60, 180),
			WholeModule: 2,
			WholeModuleReasons: []CountedReason{
				{Reason: "go.mod changed, which every package depends on", Count: 1},
				{Reason: "the merge candidate is checked whole before it is promoted", Count: 1},
			},
			ScopeNotRecorded: 1,
		},
		{
			Check: "make test", Launches: 2, Passed: 1,
			PassedRan: summary(1080), Waited: summary(0, 0),
			WholeModule:        1,
			WholeModuleReasons: []CountedReason{{Reason: "the merge candidate is checked whole before it is promoted", Count: 1}},
			Narrowed:           1,
		},
	}
	if !reflect.DeepEqual(current.Checks, want) {
		got, _ := json.MarshalIndent(current.Checks, "", "  ")
		t.Fatalf("this week's check times:\n%s", got)
	}
	if current.Checks[0].PassedRan.MedianSeconds != 1200 || current.Checks[0].Waited.MedianSeconds != 60 || current.Checks[1].PassedRan.MedianSeconds != 1080 {
		t.Fatalf("medians = %+v / %+v, want 20 minutes run and 1 minute waited for make race, 18 minutes for make test", current.Checks[0].PassedRan, current.Checks[0].Waited)
	}
	if !reflect.DeepEqual(before.Checks, []CheckTimes{{Check: "make race", Launches: 1, Passed: 1, PassedRan: summary(1800), Waited: summary(0), Narrowed: 1}}) {
		t.Fatalf("last week's check times = %+v, want the one narrowed make race", before.Checks)
	}
	for _, week := range reading.Weeks[:len(reading.Weeks)-2] {
		if len(week.Checks) != 0 {
			t.Fatalf("the week of %s has check times %+v, and no check ran in it", week.Label, week.Checks)
		}
	}

	// The delivery page reads the same launches into its own days and weeks.
	delivery := ReadDelivery(context.Background(), DeliverySources{Runs: runs, HeavyChecks: heavyChecks, Now: func() time.Time { return now }})
	week := delivery.Weeks[len(delivery.Weeks)-1].CheckTimes
	if week.NotRecorded != "" || !reflect.DeepEqual(week.Checks, want) {
		t.Fatalf("the delivery reading's week = %+v, want the check times' own", week)
	}
	var tuesday DeliveryPeriod
	for _, day := range delivery.Days {
		if day.Label == "2026-11-03" {
			tuesday = day
		}
	}
	if len(tuesday.CheckTimes.Checks) != 2 || tuesday.CheckTimes.Checks[0].Launches != 2 {
		t.Fatalf("Tuesday's check times = %+v, want the old run's two make race launches and its make test", tuesday.CheckTimes.Checks)
	}
}

func TestCheckTimesSayWhyThereAreNone(t *testing.T) {
	t.Parallel()
	runs, now := checkTimesFixture()
	clock := func() time.Time { return now }

	none := ReadCheckTimes(CheckTimesSources{Runs: runs, Now: clock})
	if !strings.Contains(none.NotRecorded, "names no heavy checks") || len(none.Weeks) != deliveryWeeks {
		t.Fatalf("with no heavy checks named, reading = %+v", none)
	}
	unread := ReadCheckTimes(CheckTimesSources{Runs: fakeDeliveryRuns{fail: fmt.Errorf("permission denied")}, HeavyChecks: heavyChecks, Now: clock})
	if !strings.Contains(unread.NotRecorded, "permission denied") {
		t.Fatalf("with the run records unreadable, reading = %+v", unread)
	}
	delivery := ReadDelivery(context.Background(), DeliverySources{Runs: runs, Now: clock})
	if measure := delivery.Weeks[0].CheckTimes; !strings.Contains(measure.NotRecorded, "names no heavy checks") || measure.Checks != nil {
		t.Fatalf("the delivery reading with no heavy checks named = %+v", measure)
	}
}

func TestWholeModuleReasonIsReadFromAStagesRecord(t *testing.T) {
	t.Parallel()
	for narrowed, want := range map[string]string{
		"the whole module (go.mod changed, which every package depends on)":                                                      "go.mod changed, which every package depends on",
		"the whole module (the merge candidate is checked whole before it is promoted); before review: ./internal/checks":        "the merge candidate is checked whole before it is promoted",
		"the whole module (the merge candidate is checked whole before it is promoted); before review: ./a; make adoption added": "the merge candidate is checked whole before it is promoted",
		"./internal/checks ./internal/readmodel":                                                                                 "",
		"the document checks, in place of the configured checks, because the change is a confirmed document":                     "",
	} {
		whole, reason := wholeModuleReason(narrowed)
		if whole != (want != "") || reason != want {
			t.Errorf("wholeModuleReason(%q) = %v, %q, want %q", narrowed, whole, reason, want)
		}
	}
}

// countingRuns counts how often each run's event log is read, through the
// reader that leaves check output undecoded.
type countingRuns struct {
	fakeDeliveryRuns
	mu    sync.Mutex
	reads map[string]int
}

func (c *countingRuns) EventsOfType(runID string, types ...execution.EventType) ([]execution.Event, []runstate.SkippedLine, error) {
	c.mu.Lock()
	c.reads[runID]++
	c.mu.Unlock()
	var kept []execution.Event
	for _, each := range c.events[runID] {
		for _, wanted := range types {
			if each.Type == wanted {
				kept = append(kept, each)
			}
		}
	}
	return kept, nil, nil
}

func TestAMemoReadsAFinishedRunsLogOnceUntilItsRecordMoves(t *testing.T) {
	t.Parallel()
	fixture, now := checkTimesFixture()
	runs := &countingRuns{fakeDeliveryRuns: fixture, reads: map[string]int{}}
	memo := &CheckLaunchMemo{}
	read := func() CheckTimesReading {
		return ReadCheckTimes(CheckTimesSources{Runs: runs, HeavyChecks: heavyChecks, Memo: memo, Now: func() time.Time { return now }})
	}
	first := read()
	second := read()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("a reading through the memo differs from the first:\n%+v\n%+v", first, second)
	}
	if runs.reads["run-old"] != 1 || runs.reads["run-earlier"] != 1 || runs.reads["run-fresh"] != 2 {
		t.Fatalf("reads = %v, want each finished run read once and the run in flight every time", runs.reads)
	}
	runs.recorded[0].UpdatedAt = runs.recorded[0].UpdatedAt.Add(time.Minute)
	read()
	if runs.reads["run-old"] != 2 {
		t.Fatalf("reads = %v, want a finished run read again once its record moved", runs.reads)
	}
}

func TestCheckTimesRenderEachWeekInWords(t *testing.T) {
	t.Parallel()
	runs, now := checkTimesFixture()
	rendered := ReadCheckTimes(CheckTimesSources{Runs: runs, HeavyChecks: heavyChecks, Now: func() time.Time { return now }}).Render()
	for _, words := range []string{
		"week by week from Monday to Sunday in America/Los_Angeles (PST)",
		"week of Mon 2 Nov 2026, so far\n  make race: ran 3 times, 3 passed; the runs that passed took 20 min (median); each waited 1 min (median) for a free place\n",
		"    told to run the whole module 2 times: 1 because go.mod changed, which every package depends on; 1 because the merge candidate is checked whole before it is promoted\n",
		"    what it was told to run is not recorded for 1 run\n",
		"  make test: ran 2 times, 1 passed; the runs that passed took 18 min (median); each waited 0 s (median) for a free place\n",
		"week of Mon 26 Oct 2026\n  make race: ran 1 time, 1 passed; the runs that passed took 30 min (median)",
		"which only its output says",
	} {
		if !strings.Contains(rendered, words) {
			t.Fatalf("the rendering does not say %q:\n%s", words, rendered)
		}
	}
	if strings.Contains(rendered, "Not read") {
		t.Fatalf("the rendering says a log was not read when every log was:\n%s", rendered)
	}
	if strings.Contains(rendered, "week of Mon 19 Oct") {
		t.Fatalf("the rendering goes back past the first week any check ran in:\n%s", rendered)
	}
	if strings.Index(rendered, "week of Mon 2 Nov") > strings.Index(rendered, "week of Mon 26 Oct") {
		t.Fatalf("the rendering is not newest first:\n%s", rendered)
	}
}

// An event log that could not be read is said in ordinary words: the true
// figures are at least the ones shown, never that a figure is a "floor".
func TestCheckTimesSayAnUnreadLogLeavesTheFiguresShort(t *testing.T) {
	t.Parallel()
	reading := CheckTimesReading{Zone: "UTC", NotRead: []string{"run-old: permission denied"}}
	rendered := reading.Render()
	if want := "Not read, so the true figures are at least these: run-old: permission denied.\n"; !strings.Contains(rendered, want) {
		t.Fatalf("the rendering does not say %q:\n%s", want, rendered)
	}
	if strings.Contains(rendered, "floor") {
		t.Fatalf("the rendering calls a figure a floor:\n%s", rendered)
	}
}
