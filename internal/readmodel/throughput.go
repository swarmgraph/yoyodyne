package readmodel

// What the harness got done over a day and a week.
//
// The standing status answers "where does the harness stand right now"; this
// answers the other question an operator glancing at a page asks, which is
// whether anything is getting done. It derives nothing of its own about it: the
// endings are runstate.State.Outcome, the word `yoyo status` prints for each
// run, with a `succeeded` run counted as landed exactly where it carries a
// promotion, and the days are runstate.LocalDay, the spend report's own. So a
// run counts as landed here exactly when the terminal says its work landed.
//
// What it cost is the spend reading beside this one and is not repeated here.
// The money moved out when the page grew a spend box of its own, because a page
// carrying "today" in one section and "the last 24 hours" in another is a page
// with two cost figures a reader has to reconcile — and because the throughput
// then needs no ledger at all, which is what keeps the page at one read of the
// event logs rather than two.
//
// Every figure names its window. A window is local calendar days, today
// counting as the first of them, because that is the day an operator's own
// clock is keeping and the day the spend report already groups by; the two
// windows here are today, and today with the six days before it.

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ThroughputSources are the records one throughput reading is assembled from.
// The run state is an interface so the derivation can be exercised without a
// state directory, which is the only way a figure nobody may recompute per
// surface gets a fixture that holds it.
type ThroughputSources struct {
	// Items supplies the current tracker fields for the work named by a run.
	Items Sources
	// Runs is the durable run state, read for what each recorded run became and
	// when. A reading without one says so rather than reporting nothing landed,
	// and says RunsProblem where the caller carries the reason it could not open
	// the store: "permission denied" is what somebody acts on, and "nothing was
	// wired" is not it.
	Runs        Runs
	RunsProblem string
	// Now stamps the reading and anchors the windows. It defaults to the wall
	// clock and is injected so a test can pin a day.
	Now func() time.Time
}

func (s ThroughputSources) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// windows are the two windows every reading covers, in days: today, and today
// with the six days before it. They are the design's daily and weekly windows,
// counted the way the spend report counts a window so that the two agree.
var windows = []struct {
	label string
	days  int
}{
	{"today", 1},
	{"last 7 days", 7},
}

// Window is what happened in one window of local days, every figure labeled
// with what it covers.
type Window struct {
	// Label is the window in words, and Days how many local days it covers,
	// today counting as the first. Since is the first local day covered.
	Label string `json:"label"`
	Days  int    `json:"days"`
	Since string `json:"since"`

	// Started counts the runs that started inside the window, whatever became of
	// them. It is the attempt volume the endings below are read against.
	Started int `json:"started"`
	// The endings, by the outcome the run history prints for each run, counted
	// by when the run completed. They are five fields rather than a map so that
	// the vocabulary here is the run history's whole vocabulary and a reader can
	// see that it is: a run that ended is in exactly one of them.
	//
	// Landed is the run whose work reached the target branch — `succeeded` with a
	// promotion recorded — which is what the design's integrated-work total
	// counts. A run that succeeded without promoting anything, which is what an
	// escalation or a bootstrap run looks like in the record, is counted under
	// Succeeded and not here, because nothing reached the branch. An evidence
	// landing is not one of those: its change integrates exactly as a discharge
	// does, so it is counted here, and that the item stayed open is the tracker's
	// to say rather than this figure's.
	Landed    int `json:"landed"`
	Succeeded int `json:"succeeded"`
	Stopped   int `json:"stopped"`
	Cancelled int `json:"cancelled"`
	TimedOut  int `json:"timed_out"`
	Failed    int `json:"failed"`
	// LandedItems names each run Landed counts — the work item it landed, and
	// when — newest first, so a surface that opens the landed grouping lists the
	// runs the count was taken over rather than a second reading of the records.
	// It is nil where the runs could not be read, as the endings are nothing
	// then, and empty rather than absent otherwise.
	LandedItems []LandedRun `json:"landed_items"`
	// StopsByCause counts every terminal run that did not land, including a
	// successful escalation. Old records count as unknown, never an inferred cause.
	StopsByCause map[runstate.StopClass]int `json:"stops_by_cause"`
}

// LandedRun is one run whose work reached the target branch inside a window:
// the item it landed, by id and by the title the run recorded at its claim, and
// when it ended.
type LandedRun struct {
	RunID      string    `json:"run_id"`
	WorkItemID string    `json:"work_item_id"`
	Title      string    `json:"title,omitempty"`
	Citation   string    `json:"citation"`
	LandedAt   time.Time `json:"landed_at"`
}

// Throughput is the reading: the two windows, and what could not be read. It is
// carried whole for the surfaces that project the model — the dashboard's
// throughput section and the tiles above it — and it never fails as a whole: a
// source that cannot be read costs its own figures, saying so in its problem
// rather than reporting zero.
type Throughput struct {
	ObservedAt time.Time `json:"observed_at"`
	Windows    []Window  `json:"windows"`
	// RunsProblem is set where the run records could not be read; the endings
	// in every window are then nothing rather than zero, and this says why.
	RunsProblem string `json:"runs_problem,omitempty"`
}

// ReadThroughput assembles the two windows from the durable records.
func ReadThroughput(ctx context.Context, sources ThroughputSources) Throughput {
	now := sources.now()
	reading := Throughput{ObservedAt: now, Windows: make([]Window, 0, len(windows))}
	for _, window := range windows {
		reading.Windows = append(reading.Windows, Window{
			Label: window.label,
			Days:  window.days,
			Since: firstLocalDay(now, window.days),
		})
	}

	var recorded []runstate.State
	switch {
	case sources.Runs == nil:
		reading.RunsProblem = absent("the recorded runs", sources.RunsProblem)
	default:
		states, err := sources.Runs.Recorded()
		if err != nil {
			reading.RunsProblem = fmt.Sprintf("the recorded runs could not be read: %v", err)
		} else {
			recorded = states
		}
	}

	if reading.RunsProblem != "" {
		return reading
	}
	for index := range reading.Windows {
		countEndings(&reading.Windows[index], recorded, now)
	}
	titles, _ := ReadWorkItemTitles(ctx, sources.Items)
	for index := range reading.Windows {
		for item := range reading.Windows[index].LandedItems {
			landed := &reading.Windows[index].LandedItems[item]
			landed.Citation = titles.Name(landed.WorkItemID)
		}
	}
	return reading
}

// absent is what a reading says about a source it was not given: the reason
// the caller could not open it where the caller carried one, and that nothing
// was wired otherwise. The two are different things to do about.
func absent(what, problem string) string {
	if problem != "" {
		return what + " could not be opened: " + problem
	}
	return "nothing was wired to read " + what
}

// countEndings counts the recorded runs into one window: by start for the
// attempt volume, and by completion for the endings, in the run history's own
// vocabulary. A run still in flight has not ended and is counted nowhere here;
// the standing status is what counts it.
func countEndings(window *Window, recorded []runstate.State, now time.Time) {
	since := startOfLocalDay(now, window.Days)
	window.LandedItems = []LandedRun{}
	window.StopsByCause = map[runstate.StopClass]int{}
	for _, state := range recorded {
		if !state.StartedAt.Before(since) && !state.StartedAt.After(now) {
			window.Started++
		}
		if !state.Status.Terminal() {
			continue
		}
		ended := EndedAt(state)
		if ended.Before(since) || ended.After(now) {
			continue
		}
		if state.Outcome() != runstate.OutcomeSucceeded || state.Integration == nil {
			window.StopsByCause[state.RecordedStopClass()]++
		}
		switch state.Outcome() {
		case runstate.OutcomeSucceeded:
			if state.Integration != nil {
				window.Landed++
				window.LandedItems = append(window.LandedItems, LandedRun{RunID: state.RunID, WorkItemID: state.WorkItemID, Title: state.WorkItemTitle, LandedAt: ended})
			} else {
				window.Succeeded++
			}
		case runstate.OutcomeStopped:
			window.Stopped++
		case runstate.OutcomeCancelled:
			window.Cancelled++
		case runstate.OutcomeTimedOut:
			window.TimedOut++
		default:
			window.Failed++
		}
	}
	// Newest first, by identifier where two landed at one instant, so two
	// readings of one directory list the landed work in one order.
	sort.SliceStable(window.LandedItems, func(first, second int) bool {
		if !window.LandedItems[first].LandedAt.Equal(window.LandedItems[second].LandedAt) {
			return window.LandedItems[first].LandedAt.After(window.LandedItems[second].LandedAt)
		}
		return window.LandedItems[first].RunID < window.LandedItems[second].RunID
	})
}

// EndedAt is when a terminal run ended: its completion where the record has
// one, and the last time the record moved otherwise, which is what a run that
// died without writing a completion leaves.
func EndedAt(state runstate.State) time.Time {
	if state.CompletedAt != nil && !state.CompletedAt.IsZero() {
		return *state.CompletedAt
	}
	if !state.UpdatedAt.IsZero() {
		return state.UpdatedAt
	}
	return state.StartedAt
}

// startOfLocalDay is the first instant of the window: local midnight at the
// start of the oldest day it covers, stepping back by calendar days rather than
// by multiples of twenty-four hours so a daylight-saving shift cannot move it.
func startOfLocalDay(now time.Time, days int) time.Time {
	local := now.Local()
	return time.Date(local.Year(), local.Month(), local.Day()-(days-1), 0, 0, 0, 0, local.Location())
}

// firstLocalDay is the same day as the spend report names it.
func firstLocalDay(now time.Time, days int) string {
	return runstate.LocalDay(startOfLocalDay(now, days))
}
