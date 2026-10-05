package readmodel

// A recurring task whose firings keep failing before their first turn.
//
// From 06:39Z on 2026-09-26 every development manager sweep was refused before
// its first turn — "operator message is 47768 bytes, limit is 32768", six times
// in a row — and every triage decision those sweeps would have made waited a
// day. Nothing said so anywhere a person reads: the sweep log held a line per
// firing reading like a partial pass, and the operator's assistant found it by
// reading the log.
//
// A firing like that is not the provider's wait, which the outage and capacity
// records already say; it is the harness refusing its own message, or unable
// to open the conversation or assemble the turn, and it does not end by
// waiting. So from the second such firing in a row it is an entry on the
// attention line, read from the sweep log alone — the firings record their own
// cause, and this counts them.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// FailingTaskThreshold is how many firings in a row have to fail before their
// first turn before the task is said to be failing. One is a firing that
// failed; two is a task that will go on failing, which is the thing a person
// has to be told.
const FailingTaskThreshold = 2

// FailingTask is a task whose firings have failed repeatedly: which task, the
// latest cause, how many in a row, and since when. ProductPass marks the broader
// finding that waits for a successful pass, with its watching and resolving roles.
type FailingTask struct {
	ProductPass bool                   `json:"product_pass,omitempty"`
	Ownership   *ownership.PassFailure `json:"ownership,omitempty"`
	ReportID    string                 `json:"report_id,omitempty"`
	Task        string                 `json:"task"`
	Role        domain.AgentRole       `json:"role"`
	// Cause is the latest failed firing's, which is the one standing now.
	Cause runstate.PreTurnCause `json:"cause"`
	// Problem is what the latest failed firing's record says stopped it, which
	// carries the refusal's own words.
	Problem       string `json:"problem"`
	FailureOutput string `json:"failure_output,omitempty"`
	// Failures is how many firings in a row failed before their first turn,
	// counted back to the last firing that took one.
	Failures int `json:"failures"`
	// FirstAt is the first of those firings, RaisedAt the one that made it an
	// entry — the FailingTaskThreshold'th — and LatestAt the most recent.
	FirstAt  time.Time `json:"first_at"`
	RaisedAt time.Time `json:"raised_at"`
	LatestAt time.Time `json:"latest_at"`
}

// Mover is whose move the failure is, read off its cause. A conversation that
// will not open is the operator's: what stops it opening is a role no agent
// fills, a record that will not load, or a session somebody else is holding,
// and a person resolves each. A message the harness refused, or a turn it
// could not assemble, is the harness's: it composed what it then refused,
// which is a defect in the harness rather than anything a person configured.
func (f FailingTask) Mover() Mover {
	if f.Ownership != nil {
		return f.Ownership.Mover
	}
	if f.Cause == runstate.PreTurnConversationUnopened {
		return MoverOperator
	}
	return MoverHarness
}

// Says is the failure as a sentence: the task, what failed, and how many
// times.
func (f FailingTask) Says() string {
	if f.ProductPass {
		return (runstate.PassFailure{Task: f.Task, Failures: f.Failures, FirstAt: f.FirstAt, Problem: f.Problem, FailureOutput: f.FailureOutput}).Says()
	}
	return fmt.Sprintf("the recurring task %s has failed before its first turn %d times in a row since %s: %s; latest: %s",
		f.Task, f.Failures, f.FirstAt.UTC().Format(time.RFC3339), f.Cause.Describe(), singleLine(f.Problem, maxRefusalBytes))
}

// Mark identifies one standing failure across readings: the task and the
// first firing of the run. A later firing that fails the same way is the same
// failure; one after a firing that took a turn is a new one.
func (f FailingTask) Mark() string {
	return f.Task + "@" + f.FirstAt.UTC().Format(time.RFC3339Nano)
}

// FailingTasksOf reads the recurring tasks that are failing before their first
// turn, from the sweep log: for each task, the firings since the last one that
// took a turn, counting those that recorded a pre-turn cause. A firing the
// provider refused, or that the outage wait recorded, neither counts nor ends
// the run — it asked nothing either way — and only a firing that took a turn
// ends it. The result is in task name order.
func FailingTasksOf(passes []runstate.Sweep) []FailingTask {
	byTask := map[string][]runstate.Sweep{}
	for _, pass := range passes {
		byTask[pass.Task] = append(byTask[pass.Task], pass)
	}
	var failing []FailingTask
	for task, recorded := range byTask {
		sort.SliceStable(recorded, func(first, second int) bool {
			return recorded[first].StartedAt.Before(recorded[second].StartedAt)
		})
		var run []runstate.Sweep
		for _, pass := range recorded {
			switch {
			case pass.Turns > 0:
				run = nil
			case pass.NotStarted != "":
				run = append(run, pass)
			}
		}
		if len(run) < FailingTaskThreshold {
			continue
		}
		latest := run[len(run)-1]
		failing = append(failing, FailingTask{
			Task:     task,
			Role:     latest.Role,
			Cause:    latest.NotStarted,
			Problem:  strings.TrimSpace(latest.Problem),
			Failures: len(run),
			FirstAt:  run[0].StartedAt,
			RaisedAt: run[FailingTaskThreshold-1].StartedAt,
			LatestAt: latest.StartedAt,
		})
	}
	sort.Slice(failing, func(first, second int) bool { return failing[first].Task < failing[second].Task })
	return failing
}

// ReadFailingTasks reads the sweep log and derives the failing tasks from it.
// A reading with no log wired says nothing; a log that cannot be read says so
// rather than reporting no task failing.
func ReadFailingTasks(sources Sources) ([]FailingTask, string) {
	if sources.Passes == nil {
		return nil, ""
	}
	passes, _, err := sources.Passes.List()
	if err != nil && len(passes) == 0 {
		return nil, fmt.Sprintf("the recurring tasks' firings could not be read: %v", err)
	}
	var problem string
	if err != nil {
		problem = fmt.Sprintf("the recurring tasks' firings could only be read in part: %v", err)
	}
	return FailingTasksOf(passes), problem
}

// failingTaskAttention is a task failing before its first turn, as the
// attention line carries it.
func failingTaskAttention(failing FailingTask) Attention {
	return Attention{Kind: AttentionFailingTask, ID: failing.Task, Mover: failing.Mover(), FailingTask: &failing}
}
