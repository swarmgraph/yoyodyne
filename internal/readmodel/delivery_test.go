package readmodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/intervention"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// pacific is the delivery zone, loaded the way the reading loads it.
var pacific = DeliveryZone()

// pdt is a moment on the operator's clock in October 2026, daylight time.
func pdt(day, hour, minute int) time.Time {
	return time.Date(2026, time.October, day, hour, minute, 0, 0, pacific)
}

// deliveryNow is Tuesday 20 October 2026 at noon Pacific time.
var deliveryNow = pdt(20, 12, 0)

type fakeDeliveryTracker struct {
	items []beads.WorkItem
	fail  error
}

func (f fakeDeliveryTracker) List(_ context.Context, status string) ([]beads.WorkItem, error) {
	if status != "" {
		return nil, fmt.Errorf("the delivery reading asked for %q rather than every item", status)
	}
	return f.items, f.fail
}

type fakeDeliveryRuns struct {
	recorded []runstate.State
	events   map[string][]execution.Event
	fail     error
}

func (f fakeDeliveryRuns) Recorded() ([]runstate.State, error) { return f.recorded, f.fail }

func (f fakeDeliveryRuns) LoadEvents(runID string) ([]execution.Event, error) {
	return f.events[runID], nil
}

type fakeDeliveryUsage struct{ rows []runstate.Spend }

func (f fakeDeliveryUsage) List() ([]runstate.Spend, error) { return f.rows, nil }

type fakeDeliveryHandSteps struct {
	steps []intervention.Event
	fail  error
}

func (f fakeDeliveryHandSteps) List() ([]intervention.Event, error) { return f.steps, f.fail }

type fakeDeliveryStalls struct{ stalls []runstate.StallEvent }

func (f fakeDeliveryStalls) List() ([]runstate.StallEvent, error) { return f.stalls, nil }

type fakeDeliveryFactoryStalls struct{ stalls []runstate.FactoryStallEvent }

func (f fakeDeliveryFactoryStalls) List() ([]runstate.FactoryStallEvent, error) { return f.stalls, nil }

type fakeDeliveryReverts struct {
	reverts map[string][]gitworktree.Revert
}

func (f fakeDeliveryReverts) RevertsOn(_ context.Context, branch string, _ time.Time) ([]gitworktree.Revert, error) {
	return f.reverts[branch], nil
}

// timeline is a run's event log, one event per moment.
func timeline(runID string, steps ...execution.Event) []execution.Event {
	for index := range steps {
		steps[index].RunID = runID
		steps[index].Sequence = uint64(index + 1)
	}
	return steps
}

func event(at time.Time, kind execution.EventType, payload string) execution.Event {
	return execution.Event{Timestamp: at, Type: kind, Payload: json.RawMessage(payload)}
}

func checkStarted(at time.Time) execution.Event {
	return event(at, execution.EventCommandStarted, `{"command":"make test","kind":"check"}`)
}

func checkEnded(at time.Time, passed bool) execution.Event {
	return event(at, execution.EventCommandCompleted, fmt.Sprintf(`{"command":"make test","kind":"check","passed":%t}`, passed))
}

func reviewStarted(at time.Time) execution.Event {
	return event(at, execution.EventReviewStarted, `{}`)
}

func reviewEnded(at time.Time, decision string) execution.Event {
	return event(at, execution.EventReviewCompleted, `{"decision":"`+decision+`"}`)
}

func invoked(at time.Time) execution.Event {
	return event(at, execution.EventRunStarted, `{"model":"claude-opus-5-5"}`)
}

// mergedRun is a run that promoted its item onto main.
func mergedRun(id, item string, started, merged time.Time, commit string) runstate.State {
	return runstate.State{
		RunID: id, WorkItemID: item, WorkItemTitle: "the change " + item,
		Status: runstate.StatusSucceeded, StartedAt: started, CompletedAt: &merged,
		Integration: &runstate.Integration{TargetBranch: "main", SourceCommit: commit, TargetCommit: commit, PreviousTargetCommit: strings.Repeat("0", 40)},
	}
}

func landing(commit string, green bool) *runstate.LandingChecks {
	finished := deliveryNow
	return &runstate.LandingChecks{Commit: commit, StartedAt: finished, FinishedAt: &finished, BoundSeconds: 60, Ran: true, Green: green}
}

func usage(runID, model string, input, output int64) runstate.Spend {
	return runstate.Spend{RunID: runID, ResolvedModel: model, Usage: json.RawMessage(fmt.Sprintf(`{"input_tokens":%d,"output_tokens":%d}`, input, output))}
}

// deliveryFixture is five changes merged around the start of the week of
// Monday 19 October, the tracker items behind them, and every other record the
// reading takes.
//
//   - f: a feature merged at 23:45 on Sunday the 18th, which is already Monday
//     in UTC, so it is the last week's in Pacific time;
//   - a: a feature filed 08:00 on the 19th, whose one run passed its checks and
//     its review first time and merged at 12:30;
//   - b: a bug filed 20:00 on the 18th, whose checks failed once and whose first
//     review asked for a repair, merged at 12:10 on the 19th; main went red on
//     its merge commit, and two hand steps named it;
//   - c: an item with no kind, merged at 23:30 on Monday the 19th, which is
//     already the 20th in UTC, and reverted the next morning; its run left no
//     event log and no usage;
//   - d: a feature merged at 00:30 on Tuesday the 20th, which a bug filed at
//     10:00 cites; a second bug cites only a part split from it.
func deliveryFixture() DeliverySources {
	commitA, commitB, commitC, commitD := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40), strings.Repeat("d", 40)
	runA := mergedRun("run-a", "yoyodyne-ifd.a", pdt(19, 10, 0), pdt(19, 12, 30), commitA)
	runA.LandingChecks = landing(commitA, true)
	runB := mergedRun("run-b", "yoyodyne-ifd.b", pdt(19, 9, 0), pdt(19, 12, 10), commitB)
	runB.LandingChecks = landing(commitB, false)
	runC := mergedRun("run-c", "yoyodyne-ifd.c", pdt(19, 21, 0), pdt(19, 23, 30), commitC)
	runD := mergedRun("run-d", "yoyodyne-ifd.d", pdt(19, 23, 50), pdt(20, 0, 30), commitD)
	runD.LandingChecks = landing(commitD, true)
	runF := mergedRun("run-f", "yoyodyne-ifd.f", pdt(18, 23, 0), pdt(18, 23, 45), strings.Repeat("f", 40))
	runF.LandingChecks = landing(strings.Repeat("f", 40), true)
	// A run that never promoted is no merge.
	failed := runstate.State{RunID: "run-e", WorkItemID: "yoyodyne-ifd.e", Status: runstate.StatusFailed, StartedAt: pdt(19, 9, 0), CompletedAt: ptr(pdt(19, 9, 30))}

	return DeliverySources{
		Tracker: fakeDeliveryTracker{items: []beads.WorkItem{
			{ID: "yoyodyne-ifd.a", Title: "a", IssueType: "feature", CreatedAt: pdt(19, 8, 0)},
			{ID: "yoyodyne-ifd.b", Title: "b", IssueType: "bug", CreatedAt: pdt(18, 20, 0)},
			{ID: "yoyodyne-ifd.c", Title: "c", IssueType: "task", CreatedAt: pdt(12, 9, 0)},
			{ID: "yoyodyne-ifd.d", Title: "d", IssueType: "feature", CreatedAt: pdt(19, 22, 0)},
			{ID: "yoyodyne-ifd.f", Title: "f", IssueType: "feature", CreatedAt: pdt(18, 20, 0)},
			{ID: "yoyodyne-ifd.x", Title: "Main fails after yoyodyne-ifd.d", IssueType: "bug", CreatedAt: pdt(20, 10, 0), Description: "Since yoyodyne-ifd.d merged the build is broken."},
			{ID: "yoyodyne-ifd.y", Title: "A part of d is wrong", IssueType: "bug", CreatedAt: pdt(20, 10, 0), Description: "yoyodyne-ifd.d.1 is wrong."},
			// A bug that cites a change but was filed before it merged is not
			// the change failing.
			{ID: "yoyodyne-ifd.z", Title: "z", IssueType: "bug", CreatedAt: pdt(19, 7, 0), Description: "yoyodyne-ifd.a will need care."},
		}},
		TrackerTimeout: time.Minute,
		Runs: fakeDeliveryRuns{
			recorded: []runstate.State{runA, runB, runC, runD, runF, failed},
			events: map[string][]execution.Event{
				"run-a": timeline("run-a",
					invoked(pdt(19, 10, 0)),
					checkStarted(pdt(19, 11, 0)), checkEnded(pdt(19, 11, 30), true),
					reviewStarted(pdt(19, 11, 40)), reviewEnded(pdt(19, 12, 0), "approve"),
				),
				"run-b": timeline("run-b",
					invoked(pdt(19, 9, 0)),
					checkStarted(pdt(19, 9, 30)), checkEnded(pdt(19, 9, 40), false),
					invoked(pdt(19, 9, 50)),
					checkStarted(pdt(19, 10, 30)), checkEnded(pdt(19, 10, 40), true),
					reviewStarted(pdt(19, 10, 45)), reviewEnded(pdt(19, 11, 0), "repair"),
					checkStarted(pdt(19, 11, 30)), checkEnded(pdt(19, 11, 40), true),
					reviewStarted(pdt(19, 11, 45)), reviewEnded(pdt(19, 12, 0), "approve"),
				),
				"run-d": timeline("run-d",
					checkStarted(pdt(20, 0, 0)), checkEnded(pdt(20, 0, 5), true),
					reviewStarted(pdt(20, 0, 10)), reviewEnded(pdt(20, 0, 20), "approve"),
				),
			},
		},
		Usage: fakeDeliveryUsage{rows: []runstate.Spend{
			usage("run-a", "claude-opus-5-5", 1000, 100),
			usage("run-a", "claude-sonnet-5-5", 400, 40),
			usage("run-b", "claude-opus-5-5", 3000, 300),
			usage("run-d", "claude-opus-5-5", 500, 50),
		}},
		HandSteps: fakeDeliveryHandSteps{steps: []intervention.Event{
			{Kind: intervention.Kind("repair"), At: pdt(19, 10, 0), Items: []string{"yoyodyne-ifd.b"}},
			{Kind: intervention.Kind("rerun"), At: pdt(19, 10, 5), Run: "run-b"},
			{Kind: intervention.Kind("restart"), At: pdt(19, 10, 5), Observed: true, Subject: "the scheduler"},
		}},
		Passes: fakeSweeps{recorded: failingThenWorking("maintenance", pdt(19, 1, 0), pdt(19, 4, 0))},
		Stalls: fakeDeliveryStalls{stalls: []runstate.StallEvent{
			{EventID: "stall-1", Since: pdt(19, 5, 0), OpenedAt: pdt(19, 5, 30), ClosedAt: ptr(pdt(19, 7, 0))},
		}},
		FactoryStalls: fakeDeliveryFactoryStalls{stalls: []runstate.FactoryStallEvent{
			{EventID: "factory-stall-1", Since: pdt(20, 11, 0), OpenedAt: pdt(20, 11, 30)},
		}},
		Reverts: fakeDeliveryReverts{reverts: map[string][]gitworktree.Revert{
			"main": {{Commit: strings.Repeat("9", 40), At: pdt(20, 8, 0), Reverts: []string{commitC}}},
		}},
		Now: func() time.Time { return deliveryNow },
	}
}

func ptr(moment time.Time) *time.Time { return &moment }

// failingThenWorking is three failed passes of a harness task an hour apart
// from start, and one that worked at cleared.
func failingThenWorking(task string, start, cleared time.Time) []runstate.Sweep {
	var passes []runstate.Sweep
	for index := range 3 {
		at := start.Add(time.Duration(index) * time.Hour)
		passes = append(passes, runstate.Sweep{Task: task, StartedAt: at, EndedAt: at.Add(time.Minute), Problem: "the step failed",
			Steps: []runstate.SweepStep{{Name: "sweep", Outcome: runstate.StepFailed, Detail: "it failed"}}})
	}
	return append(passes, runstate.Sweep{Task: task, StartedAt: cleared, EndedAt: cleared,
		Steps: []runstate.SweepStep{{Name: "sweep", Outcome: runstate.StepRan}}})
}

func deliveryDay(t *testing.T, reading Delivery, label string) DeliveryPeriod {
	t.Helper()
	for _, period := range reading.Days {
		if period.Label == label {
			return period
		}
	}
	t.Fatalf("no day %s in the reading", label)
	return DeliveryPeriod{}
}

func deliveryWeek(t *testing.T, reading Delivery, label string) DeliveryPeriod {
	t.Helper()
	for _, period := range reading.Weeks {
		if period.Label == label {
			return period
		}
	}
	t.Fatalf("no week %s in the reading", label)
	return DeliveryPeriod{}
}

func merged(period DeliveryPeriod) []string {
	var ids []string
	for _, change := range period.Merges.Changes {
		ids = append(ids, change.WorkItemID)
	}
	return ids
}

func stage(t *testing.T, lead DeliveryLeadTime, name LeadStage) DeliveryStage {
	t.Helper()
	for _, each := range lead.Stages {
		if each.Stage == name {
			return each
		}
	}
	t.Fatalf("no stage %s in %+v", name, lead.Stages)
	return DeliveryStage{}
}

// The periods are Pacific days and Pacific weeks from Monday to Sunday, oldest
// first, the current one last: a change merged at 23:45 on Sunday is that
// Sunday's and that week's, one merged at 23:30 on Monday is Monday's, and one
// merged at 00:30 on Tuesday is Tuesday's — whatever day each is in UTC.
func TestDeliveryCountsMergesIntoPacificDaysAndWeeks(t *testing.T) {
	t.Parallel()
	reading := ReadDelivery(context.Background(), deliveryFixture())
	if reading.Zone != DeliveryZoneName || len(reading.Days) != deliveryDays || len(reading.Weeks) != deliveryWeeks {
		t.Fatalf("reading is in %s with %d days and %d weeks", reading.Zone, len(reading.Days), len(reading.Weeks))
	}
	today, thisWeek := reading.Days[len(reading.Days)-1], reading.Weeks[len(reading.Weeks)-1]
	if today.Label != "2026-10-20" || !today.Current || !today.Start.Equal(pdt(20, 0, 0)) || !today.End.Equal(pdt(21, 0, 0)) {
		t.Fatalf("today is %+v", today)
	}
	if thisWeek.Label != "2026-10-19" || !thisWeek.Current || !thisWeek.Start.Equal(pdt(19, 0, 0)) || !thisWeek.End.Equal(pdt(26, 0, 0)) {
		t.Fatalf("this week is %+v", thisWeek)
	}
	if reading.Weeks[0].Label != "2026-07-27" || reading.Days[0].Label != "2026-09-21" {
		t.Fatalf("the oldest week is %s and the oldest day %s", reading.Weeks[0].Label, reading.Days[0].Label)
	}

	monday := deliveryDay(t, reading, "2026-10-19")
	if got := merged(monday); *monday.Merges.Count != 3 || strings.Join(got, " ") != "yoyodyne-ifd.b yoyodyne-ifd.a yoyodyne-ifd.c" {
		t.Fatalf("the 19th merged %v", got)
	}
	if got := merged(today); *today.Merges.Count != 1 || got[0] != "yoyodyne-ifd.d" {
		t.Fatalf("the 20th merged %v", got)
	}
	if got := merged(deliveryDay(t, reading, "2026-10-18")); len(got) != 1 || got[0] != "yoyodyne-ifd.f" {
		t.Fatalf("the 18th merged %v", got)
	}
	if empty := deliveryDay(t, reading, "2026-10-17"); *empty.Merges.Count != 0 || empty.Merges.NotRecorded != "" {
		t.Fatalf("a day nothing merged on reads %+v", empty.Merges)
	}
	// The week before this one holds f; this one holds a, b, c and d.
	if got := merged(deliveryWeek(t, reading, "2026-10-12")); len(got) != 1 || got[0] != "yoyodyne-ifd.f" {
		t.Fatalf("the week of the 12th merged %v", got)
	}
	if got := merged(thisWeek); len(got) != 4 || got[3] != "yoyodyne-ifd.d" {
		t.Fatalf("this week merged %v", got)
	}

	// Counted in UTC instead, f's merge is this week's and c's is the 20th's:
	// the zone decides, so naming it is what keeps the reading the same anywhere.
	sources := deliveryFixture()
	sources.Zone = time.UTC
	utc := ReadDelivery(context.Background(), sources)
	if got := merged(utc.Weeks[len(utc.Weeks)-1]); len(got) != 5 {
		t.Fatalf("counted in UTC, this week merged %v", got)
	}
	if got := merged(utc.Days[len(utc.Days)-1]); len(got) != 2 {
		t.Fatalf("counted in UTC, the 20th merged %v", got)
	}
}

// A day a clock change falls on is the operator's calendar day, twenty-five
// hours long, rather than twenty-four hours from midnight.
func TestDeliveryDaysFollowTheCalendarAcrossAClockChange(t *testing.T) {
	t.Parallel()
	sources := deliveryFixture()
	sources.Now = func() time.Time { return time.Date(2026, time.November, 2, 12, 0, 0, 0, pacific) }
	reading := ReadDelivery(context.Background(), sources)
	changed := deliveryDay(t, reading, "2026-11-01")
	if changed.End.Sub(changed.Start) != 25*time.Hour || changed.Start.In(pacific).Hour() != 0 {
		t.Fatalf("the day the clocks went back runs %s from %s", changed.End.Sub(changed.Start), changed.Start)
	}
	week := reading.Weeks[len(reading.Weeks)-1]
	if week.Label != "2026-11-02" || reading.Weeks[len(reading.Weeks)-2].End != week.Start {
		t.Fatalf("the weeks around the clock change are %+v", reading.Weeks[len(reading.Weeks)-2:])
	}
}

// Lead time runs from filing to merge, and its stages are what the event logs
// say each run was doing.
func TestDeliveryLeadTimeSplitsEachChangeIntoItsStages(t *testing.T) {
	t.Parallel()
	reading := ReadDelivery(context.Background(), deliveryFixture())
	lead := deliveryDay(t, reading, "2026-10-19").LeadTime
	if lead.NotRecorded != "" || lead.FilingUnknown != 0 || lead.StagesUnknown != 1 {
		t.Fatalf("lead time over the 19th: %+v", lead)
	}
	// a took 4h30m, b 16h10m, c a week and 14h30m: the median is b's.
	if lead.Total == nil || lead.Total.Count != 3 || lead.Total.MedianSeconds != int64((16*time.Hour+10*time.Minute)/time.Second) ||
		lead.Total.MinSeconds != int64((4*time.Hour+30*time.Minute)/time.Second) || lead.Total.MaxSeconds != int64((7*24*time.Hour+14*time.Hour+30*time.Minute)/time.Second) {
		t.Fatalf("lead time totals: %+v", lead.Total)
	}
	// Queued is known for all three; the stages after it only for a and b,
	// because c's run left no event log.
	if queued := stage(t, lead, LeadQueued).Duration; queued == nil || queued.Count != 3 {
		t.Fatalf("queued: %+v", queued)
	}
	want := map[LeadStage][2]time.Duration{
		LeadDeveloping: {time.Hour, 30 * time.Minute},
		LeadChecks:     {40 * time.Minute, 50 * time.Minute},
		LeadReview:     {20 * time.Minute, 30 * time.Minute},
		LeadRepairing:  {0, 70 * time.Minute},
		LeadMerging:    {30 * time.Minute, 10 * time.Minute},
	}
	for name, spans := range want {
		duration := stage(t, lead, name).Duration
		low, high := spans[0], spans[1]
		if low > high {
			low, high = high, low
		}
		if duration == nil || duration.Count != 2 || duration.MinSeconds != int64(low/time.Second) || duration.MaxSeconds != int64(high/time.Second) {
			t.Fatalf("%s: %+v, want %s to %s", name, duration, low, high)
		}
	}
	if lead.ReviewRounds == nil || lead.ReviewRounds.Max != 2 || lead.RepairRounds == nil || lead.RepairRounds.Max != 2 || lead.RepairRounds.Median != 0 {
		t.Fatalf("rounds: review %+v, repair %+v", lead.ReviewRounds, lead.RepairRounds)
	}
	// A day nothing merged on says so for each stage rather than giving a figure.
	idle := deliveryDay(t, reading, "2026-10-17").LeadTime
	if idle.Total != nil || stage(t, idle, LeadChecks).NotRecorded != "nothing merged" {
		t.Fatalf("an idle day's lead time: %+v", idle)
	}
}

// A change failed when it was reverted, when main went red on its merge
// commit, or when a bug filed within 48 hours cites it — and not when a bug
// cites only a part split from it.
func TestDeliveryChangeFailureReadsRevertsRedLandingsAndCitingBugs(t *testing.T) {
	t.Parallel()
	reading := ReadDelivery(context.Background(), deliveryFixture())
	sunday := deliveryDay(t, reading, "2026-10-19").ChangeFailure
	if sunday.NotRecorded != "" || sunday.Merges != 3 || *sunday.Failed != 2 || sunday.Reverted != 1 || sunday.RedAfter != 1 || sunday.Defects != 0 {
		t.Fatalf("change failure on the 19th: %+v", sunday)
	}
	if *sunday.Rate != 2.0/3.0 || sunday.Settling != 3 || sunday.LandingUnchecked != 1 {
		t.Fatalf("change failure on the 19th: rate %v, settling %d, unchecked %d", *sunday.Rate, sunday.Settling, sunday.LandingUnchecked)
	}
	today := deliveryDay(t, reading, "2026-10-20").ChangeFailure
	if *today.Failed != 1 || today.Defects != 1 || len(today.FailedChanges) != 1 || !strings.Contains(today.FailedChanges[0].Why[0], "yoyodyne-ifd.x") {
		t.Fatalf("change failure on the 20th: %+v", today)
	}
	// A day with no merges has no rate, rather than a rate of nothing.
	if idle := deliveryDay(t, reading, "2026-10-17").ChangeFailure; idle.Rate != nil || *idle.Failed != 0 {
		t.Fatalf("an idle day's change failure: %+v", idle)
	}
}

func TestCitesItemTakesTheIdWholeAndNotAPartSplitFromIt(t *testing.T) {
	t.Parallel()
	for text, want := range map[string]bool{
		"after yoyodyne-ifd.d merged":      true,
		"(yoyodyne-ifd.d).":                true,
		"yoyodyne-ifd.d":                   true,
		"yoyodyne-ifd.d.1 is wrong":        false,
		"yoyodyne-ifd.dx":                  false,
		"xyoyodyne-ifd.d and nothing else": false,
		"see yoyodyne-ifd.d.":              true,
	} {
		if got := citesItem(text, "yoyodyne-ifd.d"); got != want {
			t.Errorf("citesItem(%q) = %t, want %t", text, got, want)
		}
	}
	change := DeliveredChange{WorkItemID: "yoyodyne-ifd.d", PullRequest: 907}
	for text, want := range map[string]bool{"pull request 907 broke it": true, "PR #907": true, "see #907.": true, "pull request 9070": false} {
		if got := citesChange(text, change, runstate.State{}); got != want {
			t.Errorf("citesChange(%q) = %t, want %t", text, got, want)
		}
	}
}

// Recovery is how long each breakage lasted, counted where it cleared, and the
// current period lists what is still broken.
func TestDeliveryRecoveryTimesEachBreakageWhereItCleared(t *testing.T) {
	t.Parallel()
	reading := ReadDelivery(context.Background(), deliveryFixture())
	sunday := deliveryDay(t, reading, "2026-10-19").Recovery
	if sunday.NotRecorded != "" || len(sunday.NotRead) != 0 || len(sunday.Breakages) != 2 || sunday.Recovered == nil {
		t.Fatalf("recovery on the 19th: %+v", sunday)
	}
	// The passes failed from 01:00 and cleared at 04:00; the stall ran 05:00 to 07:00.
	if sunday.Breakages[0].Kind != BreakageFailingPasses || sunday.Breakages[0].Seconds != 3*3600 ||
		sunday.Breakages[1].Kind != BreakageSchedulerStall || sunday.Breakages[1].Seconds != 2*3600 {
		t.Fatalf("breakages on the 19th: %+v", sunday.Breakages)
	}
	today := deliveryDay(t, reading, "2026-10-20").Recovery
	if today.Recovered != nil || len(today.StillBroken) != 1 || today.StillBroken[0].Kind != BreakageFactoryStall || today.StillBroken[0].Seconds != 3600 {
		t.Fatalf("recovery today: %+v", today)
	}
	if earlier := deliveryDay(t, reading, "2026-10-19"); len(earlier.Recovery.StillBroken) != 0 {
		t.Fatalf("a past day lists what is broken now: %+v", earlier.Recovery.StillBroken)
	}
}

// The rework rate is bugs over merges, labelled a minimum beside the share and
// count of merges whose item has no kind.
func TestDeliveryReworkIsAMinimumBesideTheMergesWithNoKind(t *testing.T) {
	t.Parallel()
	reading := ReadDelivery(context.Background(), deliveryFixture())
	sunday := deliveryDay(t, reading, "2026-10-19").Rework
	if sunday.Merges != 3 || sunday.Bugs != 1 || sunday.Features != 1 || sunday.Untyped != 1 || *sunday.Rate != 1.0/3.0 || *sunday.UntypedShare != 1.0/3.0 {
		t.Fatalf("rework on the 19th: %+v", sunday)
	}
	if !strings.HasPrefix(sunday.Minimum, "a minimum") {
		t.Fatalf("rework on the 19th is not labelled a minimum: %q", sunday.Minimum)
	}
	if today := deliveryDay(t, reading, "2026-10-20").Rework; today.Minimum != "" || *today.Rate != 0 {
		t.Fatalf("rework on the 20th, every merge typed: %+v", today)
	}
}

// Hand steps are counted against the change they name, a change none named
// counts none, and the count is labelled a floor.
func TestDeliveryHandStepsCountPerMergedChangeAsAFloor(t *testing.T) {
	t.Parallel()
	reading := ReadDelivery(context.Background(), deliveryFixture())
	count := deliveryDay(t, reading, "2026-10-19").HandSteps.Count
	if count == nil || count.Merged != 3 || count.Attributed != 2 || count.Unattributed != 1 || count.PerChange != 2.0/3.0 || count.Floor != InterventionsFloor {
		t.Fatalf("hand steps on the 19th: %+v", count)
	}
	steps := map[string]int{}
	for _, change := range count.Changes {
		steps[change.WorkItemID] = change.Total()
	}
	if steps["yoyodyne-ifd.b"] != 2 || steps["yoyodyne-ifd.a"] != 0 {
		t.Fatalf("hand steps per change: %v", steps)
	}
}

// First pass is taken over the changes whose logs record a first check stage
// and a first review; the rest are counted apart.
func TestDeliveryFirstPassReadsTheFirstChecksAndTheFirstReview(t *testing.T) {
	t.Parallel()
	reading := ReadDelivery(context.Background(), deliveryFixture())
	sunday := deliveryDay(t, reading, "2026-10-19").FirstPass
	if sunday.Merges != 3 || sunday.Known != 2 || sunday.Passed != 1 || sunday.Unknown != 1 || *sunday.Rate != 0.5 {
		t.Fatalf("first pass on the 19th: %+v", sunday)
	}
	if today := deliveryDay(t, reading, "2026-10-20").FirstPass; today.Passed != 1 || *today.Rate != 1 {
		t.Fatalf("first pass on the 20th: %+v", today)
	}
}

// Tokens are summed by model over every run of each change and divided over
// the changes usage was recorded for; a change with none is counted apart.
func TestDeliveryTokensPerMergedChangeByModel(t *testing.T) {
	t.Parallel()
	reading := ReadDelivery(context.Background(), deliveryFixture())
	tokens := deliveryDay(t, reading, "2026-10-19").Tokens
	if tokens.Merges != 3 || tokens.Recorded != 2 || tokens.Unrecorded != 1 || len(tokens.ByModel) != 2 {
		t.Fatalf("tokens on the 19th: %+v", tokens)
	}
	opus, sonnet := tokens.ByModel[0], tokens.ByModel[1]
	if opus.Model != "claude-opus-5-5" || opus.Changes != 2 || opus.Tokens.InputTokens != 4000 || opus.PerChange.Input != 2000 || opus.PerChange.Total != 2200 {
		t.Fatalf("opus on the 19th: %+v", opus)
	}
	if sonnet.Model != "claude-sonnet-5-5" || sonnet.Changes != 1 || sonnet.PerChange.Output != 20 {
		t.Fatalf("sonnet on the 19th: %+v", sonnet)
	}
}

// A measure whose records could not be read says so and carries no figure;
// one read from some of its records names the rest and is a floor.
func TestDeliveryNamesWhatItCouldNotReadRatherThanReportingZero(t *testing.T) {
	t.Parallel()
	sources := deliveryFixture()
	sources.HandSteps = fakeDeliveryHandSteps{fail: errors.New("permission denied")}
	sources.Usage, sources.UsageProblem = nil, "the state root is not readable"
	sources.Reverts = nil
	sources.Passes, sources.Stalls, sources.FactoryStalls = nil, nil, nil
	reading := ReadDelivery(context.Background(), sources)
	sunday := deliveryDay(t, reading, "2026-10-19")
	if sunday.HandSteps.Count != nil || !strings.Contains(sunday.HandSteps.NotRecorded, "permission denied") {
		t.Fatalf("hand steps with their record unreadable: %+v", sunday.HandSteps)
	}
	if sunday.Tokens.ByModel != nil || !strings.Contains(sunday.Tokens.NotRecorded, "could not be opened: the state root is not readable") {
		t.Fatalf("tokens with no usage record: %+v", sunday.Tokens)
	}
	if sunday.Recovery.Recovered != nil || sunday.Recovery.NotRecorded == "" {
		t.Fatalf("recovery with none of its records: %+v", sunday.Recovery)
	}
	// Without the repository's history the revert is not seen, and the rate
	// says which record it was not read from.
	if sunday.ChangeFailure.Reverted != 0 || len(sunday.ChangeFailure.NotRead) != 1 || !strings.Contains(sunday.ChangeFailure.NotRead[0], sourceReverts) {
		t.Fatalf("change failure without reverts: %+v", sunday.ChangeFailure)
	}
	unread := 0
	for _, source := range reading.Sources {
		if !source.Read {
			unread++
		}
	}
	if unread != 6 {
		t.Fatalf("sources: %+v", reading.Sources)
	}

	// Without the run records nothing merged can be said at all.
	sources = deliveryFixture()
	sources.Runs, sources.RunsProblem = nil, "no such directory"
	none := ReadDelivery(context.Background(), sources)
	for _, period := range append(none.Days, none.Weeks...) {
		if period.Merges.Count != nil || period.ChangeFailure.Failed != nil || period.FirstPass.Rate != nil || period.Rework.Rate != nil || period.Merges.NotRecorded == "" {
			t.Fatalf("a period over no run records: %+v", period)
		}
	}
	body, err := json.Marshal(none)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"count":0`) {
		t.Fatalf("a reading over no run records reports a count of nothing: %s", body)
	}
}

// fixedListing is a process runner that answers every command with one tracker
// listing, which is what the tracker client is driven over below.
type fixedListing struct{ stdout string }

func (f fixedListing) Run(context.Context, execution.Command, execution.OutputObserver) (execution.ProcessResult, error) {
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: f.stdout}, nil
}

// The tracker's listing, read through the tracker client with the writers'
// zone named, gives the same lead times — and the same dependency stamps — on
// a machine in UTC as on one in Pacific time. The listing carries a link bd
// stamped in a Pacific writer's wall clock, seven hours early.
//
// It changes the process's zone, so it does not run in parallel.
func TestDeliveryLeadTimesDoNotDependOnTheMachinesZone(t *testing.T) {
	listing := `[{"id":"yoyodyne-ifd.a","title":"a","status":"closed","priority":2,"issue_type":"feature","created_at":"2026-10-19T15:00:00Z",` +
		`"dependencies":[{"issue_id":"yoyodyne-ifd.a","depends_on_id":"yoyodyne-ifd.p","type":"blocks","created_at":"2026-10-19T08:00:00Z"}]},` +
		`{"id":"yoyodyne-ifd.b","title":"b","status":"closed","priority":2,"issue_type":"bug","created_at":"2026-10-19T03:00:00Z"}]`
	read := func(local *time.Location) (Delivery, []beads.WorkItem) {
		saved := time.Local
		time.Local = local
		defer func() { time.Local = saved }()
		client := beads.Client{Runner: fixedListing{stdout: listing}, Dir: t.TempDir(), WriterZone: pacific}
		items, err := client.List(context.Background(), "")
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		sources := deliveryFixture()
		sources.Tracker = client
		return ReadDelivery(context.Background(), sources), items
	}
	inUTC, utcItems := read(time.UTC)
	inPacific, pacificItems := read(pacific)

	linked := pdt(19, 8, 0)
	for _, items := range [][]beads.WorkItem{utcItems, pacificItems} {
		if len(items[0].Dependencies) != 1 || !items[0].Dependencies[0].CreatedAt.Equal(linked) {
			t.Fatalf("the link reads %v, want %v", items[0].Dependencies, linked)
		}
	}
	first, second := deliveryDay(t, inUTC, "2026-10-19").LeadTime, deliveryDay(t, inPacific, "2026-10-19").LeadTime
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) || first.Total == nil || first.Total.Count != 2 {
		t.Fatalf("lead times differ by the machine's zone:\nUTC:     %s\nPacific: %s", firstJSON, secondJSON)
	}
	if first.Total.MinSeconds != int64((4*time.Hour+30*time.Minute)/time.Second) {
		t.Fatalf("a's lead time read %ds", first.Total.MinSeconds)
	}
}
