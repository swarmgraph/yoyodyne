package readmodel

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type MachineHistory interface {
	MachineHistory() ([]runstate.MachineObservation, error)
}

// GapCause is the observed account of one interval, including whether the
// harness's own serial pass held it. Empty Why means the cause is unknown.
type GapCause struct {
	Why        string
	Waiting    bool
	WaitingFor time.Duration
	// Problem describes incomplete evidence, never an established cause.
	Problem string
	// Checked means durable history was consulted. A caller must not then
	// assume this session's start proves no earlier session was running.
	Checked bool
}

// WatchAvailability is the shared reading used by missed passes, stopped
// responses and services. All causes are derived from recorded observations.
type WatchAvailability struct {
	Observed           bool          `json:"observed"`
	LastSleep          time.Time     `json:"last_sleep,omitempty"`
	LastWake           time.Time     `json:"last_wake,omitempty"`
	LastGap            time.Duration `json:"last_gap,omitempty"`
	Problem            string        `json:"problem,omitempty"`
	ObservationProblem string        `json:"observation_problem,omitempty"`
	sleeps             []watchGap
	down               []watchGap
	passes             []watchGap
	unobserved         []watchGap
}

type watchGap struct {
	from, to time.Time
	task     string
	known    bool
	problem  string
}

type passMoment struct {
	task string
	at   int64
}

func ReadWatchAvailability(sources Sources) WatchAvailability {
	var availability WatchAvailability
	now := time.Now()
	if sources.Now != nil {
		now = sources.Now()
	}
	if sources.Machine == nil {
		availability.Problem = "OS sleep history and scheduler observations are unavailable"
		return availability
	}
	observations, err := sources.Machine.MachineHistory()
	if err != nil {
		availability.Problem = err.Error()
	}
	availability.Observed = len(observations) > 0
	if !availability.Observed {
		availability.Problem = joinProblems(availability.Problem, "no supervisor observation of machine sleep or scheduler presence has been recorded")
	}
	observations = append([]runstate.MachineObservation(nil), observations...)
	sort.SliceStable(observations, func(i, j int) bool { return observations[i].At.Before(observations[j].At) })
	var events []runstate.PowerEvent
	for i, observation := range observations {
		events = append(events, observation.Power...)
		if i > 0 {
			previous := observations[i-1]
			gap := watchGap{from: previous.At, to: observation.At}
			switch {
			case observation.At.Sub(previous.At) > runstate.MachineObservationWindow:
				availability.unobserved = append(availability.unobserved, gap)
			case !previous.Watching && observation.At.After(previous.At):
				availability.down = append(availability.down, gap)
			}
		}
	}
	// A sample establishes presence at that look. Only consecutive looks within
	// the shared observation window bound an interval, allowing the collector's
	// poll and processing delays after its minimum interval. A missing next look
	// cannot keep a down sample in effect through the present or a restart.
	if len(observations) > 0 {
		last := observations[len(observations)-1]
		if now.After(last.At) && (!last.Watching || now.Sub(last.At) > runstate.MachineObservationWindow) {
			availability.unobserved = append(availability.unobserved, watchGap{from: last.At, to: now})
		}
	}
	if fresh, ok := sources.Machine.(interface {
		PowerHistory() ([]runstate.PowerEvent, error)
	}); ok {
		power, err := fresh.PowerHistory()
		if err != nil {
			availability.Problem = joinProblems(availability.Problem, err.Error())
		}
		events = append(events, power...)
	}
	if len(observations) > 0 {
		availability.Problem = joinProblems(availability.Problem, observations[len(observations)-1].PowerProblem)
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].At.Before(events[j].At) })
	var asleep time.Time
	seen := map[string]bool{}
	for _, event := range events {
		key := fmt.Sprintf("%s/%t", event.At.Format(time.RFC3339Nano), event.Awake)
		if seen[key] {
			continue
		}
		seen[key] = true
		if event.Awake {
			availability.LastWake = event.At
			if !asleep.IsZero() {
				availability.sleeps = append(availability.sleeps, watchGap{from: asleep, to: event.At})
				asleep = time.Time{}
			}
		} else {
			availability.LastSleep = event.At
			if asleep.IsZero() {
				asleep = event.At
			}
		}
	}
	if !asleep.IsZero() {
		availability.Problem = joinProblems(availability.Problem, "the OS recorded sleep at "+localMoment(asleep)+" without a following wake; its duration is unknown")
	}
	// A pass's ending comes from its own sweep, rather than assuming the next
	// watch note ended it. Notes can be absent while a session remains idle.
	if sources.Sessions != nil {
		transitions, err := sources.Sessions.List()
		if err != nil {
			availability.Problem = joinProblems(availability.Problem, err.Error())
		}
		transitions = append([]runstate.WatchTransition(nil), transitions...)
		sort.SliceStable(transitions, func(i, j int) bool { return transitions[i].At.Before(transitions[j].At) })
		// Stop/start pairs have exact boundaries unless a supervisor look proves
		// a missing opening: transition writes can fail without stopping a watch.
		var stopped time.Time
		var stoppedSession string
		for _, transition := range transitions {
			if transition.Note() && (stopped.IsZero() || transition.SessionID == stoppedSession) {
				continue
			}
			if transition.State == runstate.WatchStopped {
				if stopped.IsZero() {
					stopped = transition.At
					stoppedSession = transition.SessionID
				}
			} else if !stopped.IsZero() {
				availability.recordStoppedInterval(stopped, transition.At, true, observations)
				stopped = time.Time{}
			}
		}
		if !stopped.IsZero() {
			availability.recordStoppedInterval(stopped, now, false, observations)
		}
		var alive, stops []time.Time
		var openings []runstate.WatchTransition
		opened := map[string]bool{}
		for _, transition := range transitions {
			if transition.State == runstate.WatchStopped {
				stops = append(stops, transition.At)
			}
			if transition.State != runstate.WatchStopped && (!transition.Note() || transition.RecurringPass != nil) {
				alive = append(alive, transition.At)
				if !opened[transition.SessionID] {
					openings = append(openings, transition)
					opened[transition.SessionID] = true
				}
			}
		}
		// A watch opening is finer evidence than the next supervisor sample.
		// This also prevents a stale down sample extending across a restart.
		for i := range availability.down {
			next := sort.Search(len(alive), func(j int) bool { return alive[j].After(availability.down[i].from) })
			if next < len(alive) && alive[next].Before(availability.down[i].to) {
				availability.down[i].to = alive[next]
			}
		}
		endings := map[passMoment]time.Time{}
		if sources.Sweeps != nil {
			sweeps, unreadable, err := sources.Sweeps.List()
			if err != nil || len(unreadable) > 0 {
				availability.Problem = joinProblems(availability.Problem, "pass endings could not be read whole")
			}
			for _, sweep := range sweeps {
				if !sweep.IsMiss() {
					endings[passMoment{sweep.Task, sweep.StartedAt.UnixNano()}] = sweep.EndedAt
				}
			}
		}
		for _, transition := range transitions {
			if pass := transition.RecurringPass; pass != nil && !pass.Concurrent {
				end, known := endings[passMoment{pass.Task, pass.At.UnixNano()}]
				if !known {
					end = now
				}
				// A session's stop or replacement bounds an unfinished pass.
				next := sort.Search(len(stops), func(i int) bool { return stops[i].After(pass.At) })
				if next < len(stops) && stops[next].Before(end) {
					end = stops[next]
				}
				next = sort.Search(len(openings), func(i int) bool { return openings[i].At.After(pass.At) })
				if next < len(openings) && openings[next].SessionID != transition.SessionID && openings[next].At.Before(end) {
					end = openings[next].At
				}
				availability.passes = append(availability.passes, watchGap{from: pass.At, to: end, task: pass.Task, known: known})
			}
		}
	}
	if len(availability.unobserved) > 0 {
		sort.SliceStable(availability.unobserved, func(i, j int) bool {
			return availability.unobserved[i].to.Before(availability.unobserved[j].to)
		})
		availability.ObservationProblem = unobservedPresence(availability.unobserved[len(availability.unobserved)-1])
	}
	// Union the intervals so sleep while the scheduler was down is counted once.
	gaps := append(append([]watchGap{}, availability.sleeps...), availability.down...)
	sort.Slice(gaps, func(i, j int) bool { return gaps[i].from.Before(gaps[j].from) })
	var last watchGap
	for _, gap := range gaps {
		if last.to.Before(gap.from) {
			last = gap
		} else if gap.to.After(last.to) {
			last.to = gap.to
		}
	}
	if !last.from.IsZero() {
		availability.LastGap = last.to.Sub(last.from)
	}
	return availability
}

// recordStoppedInterval reconciles the watch log with independent lease looks.
// An unrecorded opening leaves the stop as one down observation, not proof of
// continuous downtime. The sample intervals are already accounted for above;
// only the portion before the first following look needs to be added here.
func (a *WatchAvailability) recordStoppedInterval(from, to time.Time, resumed bool, observations []runstate.MachineObservation) {
	first := sort.Search(len(observations), func(i int) bool { return !observations[i].At.Before(from) })
	running := first
	for running < len(observations) && !observations[running].At.After(to) {
		if observations[running].Watching {
			break
		}
		running++
	}
	foundRunning := running < len(observations) && !observations[running].At.After(to)
	if resumed && (!foundRunning || !observations[running].At.Before(to)) {
		a.down = append(a.down, watchGap{from: from, to: to})
		return
	}
	if first < len(observations) && !observations[first].At.After(to) {
		gap := watchGap{from: from, to: observations[first].At}
		if gap.to.After(gap.from) {
			if gap.to.Sub(gap.from) <= runstate.MachineObservationWindow {
				a.down = append(a.down, gap)
			} else {
				a.unobserved = append(a.unobserved, gap)
			}
		}
	} else if to.After(from) {
		a.unobserved = append(a.unobserved, watchGap{from: from, to: to})
	}
	if foundRunning && observations[running].At.After(from) {
		restartFrom := from
		if running > first {
			restartFrom = observations[running-1].At
		}
		a.unobserved = append(a.unobserved, watchGap{
			from: restartFrom, to: observations[running].At,
			problem: fmt.Sprintf("the scheduler was observed watching at %s after its stop at %s, but the restart time was not recorded and is unknown", localMoment(observations[running].At), localMoment(from)),
		})
	}
}

// Explain names only causes observed during this gap. The previous pass's
// failure is deliberately absent: it is not evidence about this missed pass.
func (a WatchAvailability) Explain(from, to time.Time, task string) string {
	var reasons []string
	for _, gap := range a.sleeps {
		if overlaps(gap, from, to) {
			reasons = append(reasons, fmt.Sprintf("the machine was asleep from %s to %s (%s), according to the OS", localMoment(gap.from), localMoment(gap.to), gap.to.Sub(gap.from).Round(time.Second)))
		}
	}
	for _, gap := range a.down {
		if overlaps(gap, from, to) {
			reasons = append(reasons, fmt.Sprintf("the harness was not watching: the scheduler was observed down from %s to %s", localMoment(gap.from), localMoment(gap.to)))
		}
	}
	for _, gap := range a.passes {
		if task != "" && gap.task != task && gap.known && overlaps(gap, from, to) {
			reasons = append(reasons, fmt.Sprintf("the pass was waiting its turn behind the recurring pass of %s, running from %s to %s", gap.task, localMoment(gap.from), localMoment(gap.to)))
		}
	}
	if len(reasons) == 0 {
		return ""
	}
	return strings.Join(reasons, "; ")
}

// Cause keeps incomplete reads and unsampled portions separate from causes
// established during the requested interval, for every caller of this model.
func (a WatchAvailability) Cause(from, to time.Time, task string) GapCause {
	problem := a.Problem
	for _, gap := range a.unobserved {
		if overlaps(gap, from, to) {
			problem = joinProblems(problem, unobservedPresence(gap))
		}
	}
	for _, gap := range a.passes {
		if task != "" && gap.task != task && !gap.known && overlaps(gap, from, to) {
			problem = joinProblems(problem, fmt.Sprintf("the session last recorded taking the recurring pass of %s at %s; its ending is unrecorded, so whether it held this pass is uncertain", gap.task, localMoment(gap.from)))
		}
	}
	return GapCause{Why: a.Explain(from, to, task), Waiting: task != "" && a.WaitingBehindPass(from, to, task), WaitingFor: a.waitingFor(from, to, task), Problem: problem, Checked: true}
}

func unobservedPresence(gap watchGap) string {
	if gap.problem != "" {
		return gap.problem
	}
	return fmt.Sprintf("the scheduler's presence was not observed from %s to %s; whether the harness was watching is unknown", localMoment(gap.from), localMoment(gap.to))
}

// WaitingBehindPass distinguishes the harness's own blocked schedule from
// machine sleep and observed downtime, without a caller parsing the prose.
func (a WatchAvailability) WaitingBehindPass(from, to time.Time, task string) bool {
	for _, gap := range a.passes {
		if gap.known && gap.task != task && overlaps(gap, from, to) {
			return true
		}
	}
	return false
}

func overlaps(gap watchGap, from, to time.Time) bool {
	return gap.from.Before(to) && gap.to.After(from)
}

// waitingFor counts the union of established waits, clipped to the missed gap.
func (a WatchAvailability) waitingFor(from, to time.Time, task string) time.Duration {
	var gaps []watchGap
	for _, gap := range a.passes {
		if task == "" || !gap.known || gap.task == task || !overlaps(gap, from, to) {
			continue
		}
		if gap.from.Before(from) {
			gap.from = from
		}
		if gap.to.After(to) {
			gap.to = to
		}
		gaps = append(gaps, gap)
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i].from.Before(gaps[j].from) })
	var total time.Duration
	end := from
	for _, gap := range gaps {
		start := gap.from
		if start.Before(end) {
			start = end
		}
		if gap.to.After(start) {
			total += gap.to.Sub(start)
			end = gap.to
		}
	}
	return total
}
