package slack

// A recurring task whose firings keep failing before their first turn, said
// once as a warning and once more as critical.
//
// From 06:39Z on 2026-09-26 every development manager sweep failed before its
// first turn, six times in a row, and every triage decision the sweeps would
// have made waited a day. The only account was a line per firing in the sweep
// log, and the operator's assistant found it by reading the log.
//
// The failure is read from the same derivation the attention line reads, so
// the channel says a task is failing exactly when `yoyo status` lists it. It is
// said twice at most per failure: as a warning when it is first seen, from the
// second failed firing in a row, and as critical once it has stood two hours,
// taken to the operators directly then. It is not repeated beyond that — the
// attention line carries it while it stands — and it stops when a firing takes
// a turn, because the derivation then no longer lists it and its marks are
// dropped, so a later failure is said afresh.

import (
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// failingTaskStream is the recurring tasks failing before their first turn.
// It is one stream for every task, holding one mark per severity said per
// failure, for the reason the improvements stream holds one per improvement.
const failingTaskStream = "failing-tasks"

// DefaultFailingTaskEscalation is how long a failing task stands on the
// attention line before it is said again as critical.
const DefaultFailingTaskEscalation = 2 * time.Hour

const (
	failingWarnedPrefix   = "warning:"
	failingCriticalPrefix = "critical:"
)

// failingTaskDeliveries says each recurring task failing before its first
// turn: once as a warning, and once as critical when it has stood past the
// escalation. A log that cannot be read is said where the sink says everything
// else about itself and leaves the cursor alone, because a failure nobody
// could read about is not a failure that ended.
func (f *HarnessFeed) failingTaskDeliveries(cursor Cursor, streams map[string]struct{}) []Delivery {
	if f.Standing == nil || f.Standing.Passes == nil {
		return nil
	}
	streams[failingTaskStream] = struct{}{}
	failing, problem := readmodel.ReadFailingTasks(*f.Standing)
	if problem != "" {
		f.say("whether a recurring task is failing before its first turn could not be read in full: %s", problem)
		if len(failing) == 0 {
			return nil
		}
	}
	now := f.now()

	// The marks still standing are kept; a mark whose failure has ended — a
	// firing took a turn — is dropped, so the next failure of that task is said
	// as a new one.
	standing := map[string]bool{}
	for _, task := range failing {
		standing[failingWarnedPrefix+task.Mark()] = true
		standing[failingCriticalPrefix+task.Mark()] = true
	}
	var kept []string
	for _, mark := range cursor.Delivered {
		if standing[mark] {
			kept = append(kept, mark)
		}
	}
	marked := Cursor{Delivered: kept}

	var deliveries []Delivery
	for _, task := range failing {
		mark := task.Mark()
		warned := marked.Has(failingWarnedPrefix + mark)
		escalated := marked.Has(failingCriticalPrefix + mark)
		overdue := now.Sub(task.RaisedAt) >= f.failingTaskEscalation()
		var severity report.Severity
		switch {
		case !warned && overdue:
			// Seen for the first time already past the escalation — a sink that
			// was down while it stood — so it is said once, as what it is now.
			severity = report.SeverityCritical
			marked.Delivered = append(marked.Delivered, failingWarnedPrefix+mark, failingCriticalPrefix+mark)
		case !warned:
			severity = report.SeverityWarning
			marked.Delivered = append(marked.Delivered, failingWarnedPrefix+mark)
		case !escalated && overdue:
			severity = report.SeverityCritical
			marked.Delivered = append(marked.Delivered, failingCriticalPrefix+mark)
		default:
			continue
		}
		attention := readmodel.FailingTaskAttention(task)
		deliveries = append(deliveries, Delivery{
			Stream: failingTaskStream,
			Cursor: Cursor{Delivered: append([]string(nil), marked.Delivered...)},
			Direct: severity == report.SeverityCritical,
			Notification: notify.FromRecurringTaskFailing(notify.RecurringTaskFailing{
				Says:  task.Says(),
				Since: task.FirstAt,
				Mover: attention.Whose(),
			}, severity, now),
		})
	}
	if len(deliveries) == 0 && strings.Join(kept, "\n") != strings.Join(cursor.Delivered, "\n") {
		// Nothing to say, and a failure ended: the cursor forgets it.
		return []Delivery{{Stream: failingTaskStream, Cursor: marked}}
	}
	return deliveries
}

// failingTaskEscalation is how long a failing task stands before it is said
// as critical.
func (f *HarnessFeed) failingTaskEscalation() time.Duration {
	if f.FailingTaskEscalation > 0 {
		return f.FailingTaskEscalation
	}
	return DefaultFailingTaskEscalation
}
