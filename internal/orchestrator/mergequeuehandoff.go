package orchestrator

// The wait from one merge queue entry landing to the queue's next check
// starting, written beside the queue (runstate's MergeQueueHandoff) so the
// queue's weekly figures can count how long the queue sat with work and
// checked nothing. The worker writes it as a check begins; a landing has one.

import (
	"context"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// recordHandoff writes the wait from the queue's latest landing to the check
// generation's entry has just started at started, where no check has been
// recorded against that landing yet. It is a measurement: a record it cannot
// read or write leaves the figure missing and the check going, so it reports
// nothing back.
func (w MergeQueueWorker) recordHandoff(ctx context.Context, key runstate.MergeQueueKey, generation runstate.MergeQueueGeneration, started time.Time) {
	entries, err := w.Queue.Entries(key)
	if err != nil {
		return
	}
	var next, landedEntry runstate.MergeQueueEntry
	var landed *runstate.MergeQueueLanded
	for _, entry := range entries {
		if entry.EntryID == generation.EntryID {
			next = entry
			continue
		}
		landing, found, err := w.Queue.Landing(key, entry.EntryID)
		if err != nil || !found {
			continue
		}
		for _, attempt := range landing.Attempts {
			if attempt.Landed == nil || attempt.Landed.ConfirmedAt.After(started) {
				continue
			}
			if landed == nil || attempt.Landed.ConfirmedAt.After(landed.ConfirmedAt) {
				confirmed := *attempt.Landed
				landed, landedEntry = &confirmed, entry
			}
		}
	}
	if landed == nil || next.EntryID == "" {
		return
	}
	handoff := runstate.NewMergeQueueHandoff(landedEntry, landed.LandedAt(), landed.ConfirmedAt, next, started)
	_, _ = w.Queue.RecordHandoff(context.WithoutCancel(ctx), key, handoff)
}
