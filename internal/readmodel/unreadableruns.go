package readmodel

import (
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Unreadable run records.
//
// The standing reads the run store through a reading that reads past a record
// it cannot read (runstate.Store.OneReadingPast), and lists the runs in flight
// through the listing that does the same (runstate.Store.IncompleteReadable),
// so one record a build cannot read costs the standing that record rather than
// every section that lists runs. What it read past is not dropped: each record
// is said among the factory problems, and on the attention line `yoyo status`
// prints, as an unreadable run, by its run, its file, and what refused it.

// RunsReadable is the listing of runs in flight that reads past a record it
// cannot read and names it. It is an optional capability of Runs: a store that
// cannot answer it is read through Incomplete, which refuses the whole listing
// over one such record, as every reading did before. It is satisfied by
// *runstate.Store.
type RunsReadable interface {
	IncompleteReadable() ([]runstate.State, []runstate.Unreadable, error)
}

// UnreadableRun is one run record the runs in flight were read past: the run,
// its file, and what refused it.
type UnreadableRun struct {
	RunID  string `json:"run_id"`
	Path   string `json:"path,omitempty"`
	Reason string `json:"reason"`
}

// Says is the record, where it is, and why it could not be read.
func (u UnreadableRun) Says() string {
	where := ""
	if u.Path != "" {
		where = " (" + u.Path + ")"
	}
	return "the run record " + u.RunID + where + " could not be read, so it is left out of the runs shown here: " + singleLine(u.Reason, maxRefusalBytes)
}

// unreadableRunWhose is what settles an unreadable run, after its mover.
const unreadableRunWhose = "the harness never acts on the run while its record does not read, and never takes it for a run that finished; it reads the record again as soon as its file changes, so the record put right, or a build that can read it, clears this"

func unreadableRunAttention(run UnreadableRun) Attention {
	return resolved(Attention{Kind: AttentionUnreadableRun, ID: run.RunID, UnreadableRun: &run})
}

// unreadableRunsOf is what the run store said it read past, as the read model
// carries it.
func unreadableRunsOf(unreadable []runstate.Unreadable) []UnreadableRun {
	runs := make([]UnreadableRun, 0, len(unreadable))
	for _, record := range unreadable {
		reason := "no reason was recorded"
		if record.Err != nil {
			reason = strings.TrimSpace(record.Err.Error())
		}
		runs = append(runs, UnreadableRun{RunID: record.Record, Path: record.Path, Reason: reason})
	}
	return runs
}

// readPastRuns is every run record the reading read past: what its store
// remembers reading past across every section (runstate.Store.ReadPast), and
// what the listing of runs in flight named, once each, in run order.
func readPastRuns(runs Runs, inFlight []UnreadableRun) []UnreadableRun {
	byRun := map[string]UnreadableRun{}
	for _, run := range inFlight {
		byRun[run.RunID] = run
	}
	if reading, ok := runs.(interface{ ReadPast() []runstate.Unreadable }); ok {
		for _, run := range unreadableRunsOf(reading.ReadPast()) {
			byRun[run.RunID] = run
		}
	}
	ids := make([]string, 0, len(byRun))
	for runID := range byRun {
		ids = append(ids, runID)
	}
	sort.Strings(ids)
	past := make([]UnreadableRun, 0, len(ids))
	for _, runID := range ids {
		past = append(past, byRun[runID])
	}
	return past
}

// readInFlight is the runs in flight, and the records read past to list them
// where the store can read past one.
func readInFlight(runs Runs) ([]runstate.State, []UnreadableRun, error) {
	if readable, ok := runs.(RunsReadable); ok {
		states, unreadable, err := readable.IncompleteReadable()
		if err != nil {
			return nil, nil, err
		}
		return states, unreadableRunsOf(unreadable), nil
	}
	states, err := runs.Incomplete()
	return states, nil, err
}
