package slack

// The operator's hand steps, each said once in the thread of what it unblocked.
//
// A hand step is a person's act rather than anything the harness noticed, and it
// is recorded in its own log by whoever took it or saw it taken (see
// internal/intervention). What the channel adds is the step beside the work it
// unblocked, so a thread about an item says that a person stepped in, and the
// count `yoyo status` prints has its entries where a reader looks.

import (
	"time"

	"github.com/mason-bryant/yoyodyne/internal/intervention"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// InterventionLog is where the hand steps are read from, by position with the
// lines that would not decode holding theirs, as every log the sink reads is.
// It is satisfied by *runstate.InterventionStore.
type InterventionLog interface {
	Scan() ([]intervention.Event, []runstate.SkippedLine, error)
}

// interventionDeliveries says each recorded step once. It advances by position
// like the reports pile, so a restart says nothing twice. A step that unblocked
// a run is said in the thread of that run's item, read from the run's record
// where the step does not name the item itself.
func (f *HarnessFeed) interventionDeliveries(cursor Cursor, since time.Time, streams map[string]struct{}) ([]Delivery, error) {
	if f.Interventions == nil {
		return nil, nil
	}
	streams[interventionStream] = struct{}{}
	events, skipped, err := f.Interventions.Scan()
	if err != nil {
		return nil, err
	}
	return f.logDeliveries(interventionStream, "hand steps", cursor, len(events), skipped, since,
		func(index int) (time.Time, notify.Notification, error) {
			event := events[index]
			notification, err := notify.FromIntervention(event, f.runItem(event))
			return event.RecordedAt, notification, err
		})
}

// runItem is the work item of the run a step unblocked, or empty where the step
// unblocked no run or the run's record cannot be read.
func (f *HarnessFeed) runItem(event intervention.Event) string {
	if event.Unblocked == nil || event.Unblocked.Kind != intervention.UnblockedRun {
		return ""
	}
	if len(event.Items) > 0 {
		return event.Items[0]
	}
	if f.Runs == nil {
		return ""
	}
	state, err := f.Runs.Load(event.Unblocked.ID)
	if err != nil {
		return ""
	}
	return state.WorkItemID
}
