package orchestrator

// The stopped runs a program manager's pass is handed, grouped by cause.
//
// "Repeated stop causes" in docs/designs/program-manager.md: an instance
// post-mortems every stopped run its pass is handed, one report per cause, with
// the count kept in its own record rather than in the report. The harness reads
// each run's cause off its record as it hands the stop over, so the section
// below says, for every cause the pass touches, how many stops it will have
// once this pass's are counted, the report it already cites, and whether it is
// owed a report on this pass. The stops themselves are handed to the turn
// (RecurringTurnOptions.Stops), which is what lets the instance's block count
// them and nothing else.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// PassStopCauses reads one instance's account of stopped runs. It is satisfied
// by *runstate.StopCauseStore.
type PassStopCauses interface {
	List(agent string) ([]runstate.StopCauseEntry, error)
}

// maxStopCauseLines bounds how many causes the section lists; the stops are
// listed whole in the block the instance copies.
const maxStopCauseLines = 20

// stops is the stopped runs among a wake's events, in the order they stopped.
func (w passWake) stops() []runstate.StopEvent {
	var stops []runstate.StopEvent
	for _, event := range w.events {
		if event.Stop != nil {
			stops = append(stops, *event.Stop)
		}
	}
	return stops
}

// stopCauseSection is what a pass handed stopped runs is told about them, and
// empty for a pass handed none.
func (t Trigger) stopCauseSection(agent string, wake passWake) string {
	stops := wake.stops()
	if len(stops) == 0 {
		return ""
	}
	var problems []string
	var entries []runstate.StopCauseEntry
	if t.StopCauses == nil {
		problems = append(problems, "the record of earlier stops is not wired here, so the counts below are this pass's alone")
	} else if listed, err := t.StopCauses.List(agent); err != nil {
		problems = append(problems, fmt.Sprintf("the record of earlier stops could not be read, so the counts below are this pass's alone: %v", err))
	} else {
		entries = listed
	}
	// The account as it will stand once this pass's stops are counted, which is
	// what the reports filed on this pass carry.
	preview := runstate.FoldStopCauses(entries).StopEntries(stops)
	account := runstate.FoldStopCauses(append(entries, preview...))
	handled, problem := t.handledReports()
	if problem != "" {
		problems = append(problems, problem)
	}
	touched := map[string]int{}
	var order []string
	for _, stop := range stops {
		id := stop.Cause.ID()
		if _, seen := touched[id]; !seen {
			order = append(order, id)
		}
		touched[id]++
	}

	var section strings.Builder
	fmt.Fprintf(&section, "\n\n## Stopped runs, by cause\n\nThis pass was handed %d stopped run(s). Post-mortem every one, grouped by cause. Each cause below is read off the run records — the stop cause, the class it was stopped in, and the step — and counts the stops already recorded with this pass's:\n\n", len(stops))
	for index, id := range order {
		if index == maxStopCauseLines {
			fmt.Fprintf(&section, "- and %d further cause(s); the result of your block lists every one\n", len(order)-maxStopCauseLines)
			break
		}
		tally, _ := account.Cause(id)
		fmt.Fprintf(&section, "- %s. %d of them handed to this pass.\n", tally.Line(tally.Standing(handled, 0)), touched[id])
	}
	for _, problem := range problems {
		fmt.Fprintf(&section, "\n%s.\n", problem)
	}
	handed := make([]map[string]string, 0, len(stops))
	for _, stop := range stops {
		handed = append(handed, map[string]string{"run": stop.Run, "stopped_at": stop.StoppedAt.UTC().Format(time.RFC3339Nano)})
	}
	encoded, _ := json.Marshal(handed)
	fmt.Fprintf(&section, "\nCount them with this as the \"stops\" of your stop causes block, and add a report for each cause owed one:\n\n\"stops\":%s\n", encoded)
	return section.String()
}

// handledReports is when each report on the pile was handled. A pile that
// cannot be read reads every report as not yet handled, which owes no report
// early, and says so.
func (t Trigger) handledReports() (func(string) (time.Time, bool), string) {
	if t.Pile == nil {
		return nil, ""
	}
	handlings, err := t.Pile.Handlings()
	if err != nil {
		return nil, fmt.Sprintf("what became of the reports could not be read, so every cause's report is taken as not yet handled: %v", err)
	}
	handled := report.Handled(handlings)
	return func(id string) (time.Time, bool) {
		handling, found := handled[id]
		return handling.RecordedAt, found
	}, ""
}
