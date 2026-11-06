package readmodel

// How long the project's heavy checks took, period by period: for each check
// only so many of may run at once on the machine (execution.heavy_checks), how
// many times it ran, the median time of the runs that passed, the median wait
// for a free place to run it, and how many times it was told to run the whole
// module and why. The delivery page shows it day by day and week by week, and
// `yoyo status --checks` week by week, both in the zone the reading names.
//
// It is read from the runs' event logs, which every check stage writes and
// which go back as far as the runs do, and nothing is kept for this reading
// alone. A check's start event is written as it asks for its place and its
// finish event as its process ends, carrying how long the process ran, so the
// launch is the finish less the time it ran and the wait is from the start to
// the launch. A check is counted where it launched, in the period it launched
// in; one stopped before it launched ran no time and is not counted.
//
// What a check was told about the Go packages the change touches is on its
// start event (checks.StartedWholeModule). An event written before the start
// event carried it is read against the run's record of its last check stage,
// which says what that stage was told; a check outside that stage, in an older
// log, is counted as not recorded rather than guessed. This is what the harness
// told the check. A project's own check may widen what it was told — this
// repository's Makefile runs the whole module for a change to the Makefile, say
// — and says so only in its own output, which the harness does not read.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// DeliveryCheckTimes is the heavy checks' times over one period, check by
// check in order of name.
type DeliveryCheckTimes struct {
	DeliveryMeasure
	Checks []CheckTimes `json:"checks,omitempty"`
}

// CheckTimes is one heavy check over one period.
type CheckTimes struct {
	Check string `json:"check"`
	// Launches is how many times it launched, and Passed how many of those
	// passed.
	Launches int `json:"launches"`
	Passed   int `json:"passed"`
	// PassedRan is the time the launches that passed ran, and Waited the time
	// every launch waited for its place before it.
	PassedRan *DurationSummary `json:"passed_ran,omitempty"`
	Waited    *DurationSummary `json:"waited,omitempty"`
	// WholeModule is how many launches were told to run the whole module, and
	// WholeModuleReasons why, most frequent first. Narrowed is how many were
	// told only the packages the change touches, and ScopeNotRecorded how many
	// launched where no record says what they were told.
	WholeModule        int             `json:"whole_module"`
	WholeModuleReasons []CountedReason `json:"whole_module_reasons,omitempty"`
	Narrowed           int             `json:"narrowed"`
	ScopeNotRecorded   int             `json:"scope_not_recorded,omitempty"`
}

// CountedReason is one reason and how many launches gave it.
type CountedReason struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// checkLaunch is one launch of one heavy check, as its run's event log and
// record say it.
type checkLaunch struct {
	check      string
	launchedAt time.Time
	ran        time.Duration
	waited     time.Duration
	passed     bool
	// scoped is whether any record says what the check was told; whole and
	// reason are what it says.
	scoped bool
	whole  bool
	reason string
}

// CheckLaunchMemo keeps what each finished run's event log said about its
// heavy checks, so a surface that reads the check times every minute reads
// each finished run's log once. It is a cache of what the logs say and holds
// nothing they do not: a run is kept only once it is over, and read again
// where its record has moved since. The zero value is ready to use, and a nil
// memo reads every log every time.
type CheckLaunchMemo struct {
	mu   sync.Mutex
	runs map[string]memoizedLaunches
}

type memoizedLaunches struct {
	updated  time.Time
	launches []checkLaunch
}

func (m *CheckLaunchMemo) lookup(state runstate.State) ([]checkLaunch, bool) {
	if m == nil || !state.Status.Terminal() {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	kept, found := m.runs[state.RunID]
	if !found || !kept.updated.Equal(state.UpdatedAt) {
		return nil, false
	}
	return kept.launches, true
}

func (m *CheckLaunchMemo) keep(state runstate.State, launches []checkLaunch) {
	if m == nil || !state.Status.Terminal() {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runs == nil {
		m.runs = map[string]memoizedLaunches{}
	}
	m.runs[state.RunID] = memoizedLaunches{updated: state.UpdatedAt, launches: launches}
}

// checkEventLogs is a run store that reads only the events of the named types,
// leaving every line of check output undecoded. *runstate.Store satisfies it;
// a store that does not is read whole.
type checkEventLogs interface {
	EventsOfType(runID string, types ...execution.EventType) ([]execution.Event, []runstate.SkippedLine, error)
}

// heavyCheckLaunches reads every heavy check that launched in a run active
// since the given moment, from each run's event log, oldest first. It returns
// the problem of each log that could not be read.
func heavyCheckLaunches(runs DeliveryRuns, states []runstate.State, heavy map[string]int, since time.Time, memo *CheckLaunchMemo) ([]checkLaunch, []string) {
	var launches []checkLaunch
	var problems []string
	named := config.Execution{HeavyChecks: heavy}
	for _, state := range states {
		if EndedAt(state).Before(since) && state.Status.Terminal() {
			continue
		}
		if kept, found := memo.lookup(state); found {
			launches = append(launches, kept...)
			continue
		}
		var events []execution.Event
		var err error
		if logs, ok := runs.(checkEventLogs); ok {
			events, _, err = logs.EventsOfType(state.RunID, execution.EventCommandStarted, execution.EventCommandCompleted)
		} else {
			events, err = runs.LoadEvents(state.RunID)
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("the event log of %s could not be read: %v", state.RunID, err))
			continue
		}
		found := runHeavyCheckLaunches(state, events, named)
		memo.keep(state, found)
		launches = append(launches, found...)
	}
	sort.SliceStable(launches, func(first, second int) bool { return launches[first].launchedAt.Before(launches[second].launchedAt) })
	return launches, problems
}

// checkStartEvent and checkFinishEvent are the parts of a check's start and
// finish events the launches are read from.
type checkStartEvent struct {
	Kind        string `json:"kind"`
	Command     string `json:"command"`
	WholeModule *bool  `json:"whole_module"`
	WholeReason string `json:"whole_module_reason"`
}

type checkFinishEvent struct {
	Kind    string `json:"kind"`
	Command string `json:"command"`
	Passed  bool   `json:"passed"`
	Elapsed string `json:"elapsed"`
}

// runHeavyCheckLaunches is every heavy check one run's events say launched.
// Each finish event is paired with the start of the same command before it.
func runHeavyCheckLaunches(state runstate.State, events []execution.Event, heavy config.Execution) []checkLaunch {
	events = append([]execution.Event(nil), events...)
	sort.SliceStable(events, func(first, second int) bool { return events[first].Sequence < events[second].Sequence })
	var launches []checkLaunch
	starts := map[string]execution.Event{}
	told := map[string]checkStartEvent{}
	for _, event := range events {
		switch event.Type {
		case execution.EventCommandStarted:
			var payload checkStartEvent
			if json.Unmarshal(event.Payload, &payload) != nil || payload.Kind != "check" {
				continue
			}
			starts[payload.Command], told[payload.Command] = event, payload
		case execution.EventCommandCompleted:
			var payload checkFinishEvent
			if json.Unmarshal(event.Payload, &payload) != nil || payload.Kind != "check" {
				continue
			}
			start, started := starts[payload.Command]
			delete(starts, payload.Command)
			name, _, isHeavy := heavy.HeavyCheck(payload.Command)
			if !started || !isHeavy {
				continue
			}
			ran, err := time.ParseDuration(payload.Elapsed)
			if err != nil || (ran <= 0 && !payload.Passed) {
				// A check stopped before it launched ran no time.
				continue
			}
			launched := event.Timestamp.Add(-ran)
			waited := launched.Sub(start.Timestamp)
			if waited < 0 {
				waited = 0
			}
			launch := checkLaunch{check: name, launchedAt: launched, ran: ran, waited: waited, passed: payload.Passed}
			if said := told[payload.Command]; said.WholeModule != nil {
				launch.scoped, launch.whole, launch.reason = true, *said.WholeModule, said.WholeReason
			} else {
				launch.scoped, launch.whole, launch.reason = stageScope(state.CheckStage, start.Timestamp)
			}
			launches = append(launches, launch)
		}
	}
	return launches
}

// stageScope is what the run's record of its last check stage says a check
// that started at the given moment was told, where the moment falls inside
// that stage.
func stageScope(stage *runstate.CheckStage, at time.Time) (scoped, whole bool, reason string) {
	if stage == nil || stage.Narrowed == "" || at.Before(stage.StartedAt) || (stage.FinishedAt != nil && at.After(*stage.FinishedAt)) {
		return false, false, ""
	}
	whole, reason = wholeModuleReason(stage.Narrowed)
	return true, whole, reason
}

// wholeModuleReason reads a stage's record of what it was told, written by
// checks.Narrowing.Describe and followed by what the stage adds to it: "the
// whole module (why)", then "; before review: ..." or a path check's note.
func wholeModuleReason(narrowed string) (bool, string) {
	const lead = "the whole module ("
	if !strings.HasPrefix(narrowed, lead) {
		return false, ""
	}
	rest := narrowed[len(lead):]
	for at := 0; at < len(rest); at++ {
		if rest[at] == ')' && (at+1 == len(rest) || rest[at+1] == ';') {
			return true, rest[:at]
		}
	}
	return true, strings.TrimSuffix(rest, ")")
}

// checkTimesOf is the heavy checks' times over the launches in one period.
func checkTimesOf(launches []checkLaunch, start, end time.Time) []CheckTimes {
	byCheck := map[string][]checkLaunch{}
	for _, launch := range launches {
		if launch.launchedAt.Before(start) || !launch.launchedAt.Before(end) {
			continue
		}
		byCheck[launch.check] = append(byCheck[launch.check], launch)
	}
	var times []CheckTimes
	for _, name := range deliveryKeys(byCheck) {
		one := CheckTimes{Check: name, Launches: len(byCheck[name])}
		var ran, waited []time.Duration
		reasons := map[string]int{}
		for _, launch := range byCheck[name] {
			waited = append(waited, launch.waited)
			if launch.passed {
				one.Passed++
				ran = append(ran, launch.ran)
			}
			switch {
			case !launch.scoped:
				one.ScopeNotRecorded++
			case launch.whole:
				one.WholeModule++
				reasons[launch.reason]++
			default:
				one.Narrowed++
			}
		}
		one.PassedRan, one.Waited = summarizeDurations(ran), summarizeDurations(waited)
		for _, reason := range deliveryKeys(reasons) {
			one.WholeModuleReasons = append(one.WholeModuleReasons, CountedReason{Reason: reason, Count: reasons[reason]})
		}
		sort.SliceStable(one.WholeModuleReasons, func(first, second int) bool {
			return one.WholeModuleReasons[first].Count > one.WholeModuleReasons[second].Count
		})
		times = append(times, one)
	}
	return times
}

// CheckTimesReading is the heavy checks' times week by week, as `yoyo status
// --checks` prints them.
type CheckTimesReading struct {
	ObservedAt time.Time `json:"observed_at"`
	// Zone is the zone every week is counted in, by name.
	Zone string `json:"zone"`
	// Weeks are the weeks from Monday to Sunday, oldest first, the current one
	// last.
	Weeks []CheckTimesWeek `json:"weeks"`
	// NotRecorded is why there is no figure at all, and NotRead each event log
	// that could not be read, which makes every figure a floor.
	NotRecorded string   `json:"not_recorded,omitempty"`
	NotRead     []string `json:"not_read,omitempty"`
}

// CheckTimesWeek is one week of it.
type CheckTimesWeek struct {
	Label   string       `json:"label"`
	Start   time.Time    `json:"start"`
	End     time.Time    `json:"end"`
	Current bool         `json:"current,omitempty"`
	Checks  []CheckTimes `json:"checks,omitempty"`
}

// CheckTimesSources are the records the check times are read from.
type CheckTimesSources struct {
	Runs        DeliveryRuns
	RunsProblem string
	// HeavyChecks is execution.heavy_checks as configured: which checks are
	// timed, by the name every command of one counts under.
	HeavyChecks map[string]int
	// Memo, where set, keeps what finished runs' logs said between readings.
	Memo *CheckLaunchMemo
	// Zone is the zone the weeks are counted in. Nil is DeliveryZoneName.
	Zone *time.Location
	Now  func() time.Time
}

// ReadCheckTimes reads the heavy checks' times over the weeks the delivery
// page covers.
func ReadCheckTimes(sources CheckTimesSources) CheckTimesReading {
	now := time.Now()
	if sources.Now != nil {
		now = sources.Now()
	}
	zone := sources.Zone
	if zone == nil {
		zone = DeliveryZone()
	}
	reading := CheckTimesReading{ObservedAt: now, Zone: zone.String()}
	weeks := deliveryWeekPeriods(now, zone)
	for _, week := range weeks {
		reading.Weeks = append(reading.Weeks, CheckTimesWeek{Label: week.Label, Start: week.Start, End: week.End, Current: week.Current})
	}
	if len(sources.HeavyChecks) == 0 {
		reading.NotRecorded = noHeavyChecks
		return reading
	}
	// The run records are read the way the delivery page reads them, with no
	// other source named, so this is the delivery reading's one read of every
	// record rather than a second one of its own; every source not named is
	// left unread.
	read := readDeliverySources(context.Background(), DeliverySources{Runs: sources.Runs, RunsProblem: sources.RunsProblem}, weeks[0].Start)
	if read.runsErr != "" {
		reading.NotRecorded = read.runsErr
		return reading
	}
	launches, problems := heavyCheckLaunches(sources.Runs, read.runs, sources.HeavyChecks, weeks[0].Start, sources.Memo)
	reading.NotRead = problems
	for index := range reading.Weeks {
		reading.Weeks[index].Checks = checkTimesOf(launches, reading.Weeks[index].Start, reading.Weeks[index].End)
	}
	return reading
}

// noHeavyChecks is why a project that names no heavy check has no check times.
const noHeavyChecks = "the project names no heavy checks (execution.heavy_checks), so no check waits for a free place and none is timed here"

// deliveryCheckLaunches is the heavy checks' launches as the delivery reading
// read them, or why it could not.
type deliveryCheckLaunches struct {
	launches    []checkLaunch
	notRecorded string
	notRead     []string
}

// readDeliveryCheckLaunches reads the launches once for every period of the
// delivery reading, from the run records it already read.
func readDeliveryCheckLaunches(sources DeliverySources, read deliveryRead, since time.Time) deliveryCheckLaunches {
	switch {
	case len(sources.HeavyChecks) == 0:
		return deliveryCheckLaunches{notRecorded: noHeavyChecks}
	case read.runsErr != "":
		return deliveryCheckLaunches{notRecorded: read.runsErr}
	}
	launches, problems := heavyCheckLaunches(sources.Runs, read.runs, sources.HeavyChecks, since, sources.CheckLaunches)
	return deliveryCheckLaunches{launches: launches, notRead: problems}
}

// of is the check times over one period.
func (d deliveryCheckLaunches) of(period DeliveryPeriod) DeliveryCheckTimes {
	measure := DeliveryCheckTimes{DeliveryMeasure: DeliveryMeasure{ReadFrom: []string{sourceRuns, sourceEvents}}}
	if d.notRecorded != "" {
		measure.NotRecorded = d.notRecorded
		return measure
	}
	switch {
	case len(d.notRead) == 1:
		measure.NotRead = d.notRead
	case len(d.notRead) > 1:
		measure.NotRead = []string{fmt.Sprintf("%d event logs could not be read; the first: %s", len(d.notRead), d.notRead[0])}
	}
	measure.Checks = checkTimesOf(d.launches, period.Start, period.End)
	return measure
}

// Render is the check times as `yoyo status --checks` prints them: the zone
// first, then each week newest first from the oldest one any heavy check ran
// in, each heavy check on its own lines.
func (r CheckTimesReading) Render() string {
	var rendered strings.Builder
	zone := r.Zone
	if location, err := time.LoadLocation(r.Zone); err == nil {
		zone += " (" + r.ObservedAt.In(location).Format("MST") + ")"
	}
	fmt.Fprintf(&rendered, "How long the heavy checks took, week by week from Monday to Sunday in %s.\n", zone)
	if r.NotRecorded != "" {
		fmt.Fprintf(&rendered, "Not recorded: %s.\n", r.NotRecorded)
		return rendered.String()
	}
	first := len(r.Weeks)
	for index, week := range r.Weeks {
		if len(week.Checks) > 0 {
			first = index
			break
		}
	}
	if first == len(r.Weeks) {
		fmt.Fprintln(&rendered, "No heavy check ran in any of these weeks.")
	}
	for index := len(r.Weeks) - 1; index >= first; index-- {
		week := r.Weeks[index]
		label := "week of " + week.Start.Format("Mon 2 Jan 2006")
		if week.Current {
			label += ", so far"
		}
		fmt.Fprintln(&rendered)
		fmt.Fprintln(&rendered, label)
		if len(week.Checks) == 0 {
			fmt.Fprintln(&rendered, "  no heavy check ran")
		}
		for _, check := range week.Checks {
			for _, line := range check.lines() {
				fmt.Fprintln(&rendered, "  "+line)
			}
		}
	}
	fmt.Fprintln(&rendered)
	fmt.Fprintln(&rendered, "A run counts in the week it launched in. What a run was told is what the harness told it; a project's own check may still run the whole module for a reason of its own, which only its output says.")
	for _, problem := range r.NotRead {
		fmt.Fprintf(&rendered, "Not read, so every figure is a floor: %s.\n", problem)
	}
	return rendered.String()
}

// lines is one check's week in words: what ran and how long it took, then what
// it was told to run.
func (c CheckTimes) lines() []string {
	head := fmt.Sprintf("%s: ran %s, %d passed", c.Check, count(c.Launches, "time"), c.Passed)
	if c.PassedRan != nil {
		head += fmt.Sprintf("; the runs that passed took %s (median)", checkSpanSaid(c.PassedRan.MedianSeconds))
	}
	if c.Waited != nil {
		head += fmt.Sprintf("; each waited %s (median) for a free place", checkSpanSaid(c.Waited.MedianSeconds))
	}
	lines := []string{head}
	if c.WholeModule > 0 {
		reasons := make([]string, 0, len(c.WholeModuleReasons))
		for _, reason := range c.WholeModuleReasons {
			reasons = append(reasons, fmt.Sprintf("%d because %s", reason.Count, reason.Reason))
		}
		lines = append(lines, fmt.Sprintf("  told to run the whole module %s: %s", count(c.WholeModule, "time"), strings.Join(reasons, "; ")))
	}
	if c.Narrowed > 0 {
		lines = append(lines, fmt.Sprintf("  told to run only the packages the change touches %s", count(c.Narrowed, "time")))
	}
	if c.ScopeNotRecorded > 0 {
		lines = append(lines, fmt.Sprintf("  what it was told to run is not recorded for %s", count(c.ScopeNotRecorded, "run")))
	}
	return lines
}

// checkSpanSaid is a check's time in words, to the second: "45 s", "9 min 30
// s", "1 h 5 min".
func checkSpanSaid(seconds int64) string {
	switch {
	case seconds < 60:
		return fmt.Sprintf("%d s", seconds)
	case seconds < 3600:
		if seconds%60 == 0 {
			return fmt.Sprintf("%d min", seconds/60)
		}
		return fmt.Sprintf("%d min %d s", seconds/60, seconds%60)
	}
	minutes := seconds / 60
	if minutes%60 == 0 {
		return fmt.Sprintf("%d h", minutes/60)
	}
	return fmt.Sprintf("%d h %d min", minutes/60, minutes%60)
}
