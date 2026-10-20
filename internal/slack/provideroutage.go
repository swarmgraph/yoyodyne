package slack

// The provider answering nobody, said to the people who end it at once and
// again each hour it stands, and once more when it answers again.
//
// A lapsed login stops every role, and only a person ends it. So it is not left
// to the channel or to the stall alarm, which waits hours before it fires: the
// first pass that reads the outage says it in the channel, tagged to the
// operators by member id, and sends it to each of them as a direct message,
// naming the provider, the account, and the exact command that logs it in
// again. While it stands it is said again, directly, at most once an hour —
// often enough that an operator who missed the first message is reached before
// the morning, and no more often than that, because a message repeated every
// pass is a reason to mute the app. When the provider answers again the
// operators are told directly as well, in a short message, because they were
// told the line had stopped and are owed being told it carried on by itself.

import (
	"context"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// OutageRepeat is how long an outage stands between one direct message about
// it and the next. It is a bound rather than a setting: the message is the
// operator being woken, and nothing a project configures should make that more
// frequent than hourly.
const OutageRepeat = time.Hour

// outageDeliveries says the provider answering nobody when it is first read and
// again each OutageRepeat it stands, and the provider answering again, once.
//
// The outage is read from the same reading of the record the switches were
// read with, so this pass cannot say the provider is away while its heartbeat
// derives a line nothing is stopping. It reports no error: a record that could
// not be read already failed the pass where the switches were read.
func (f *HarnessFeed) outageDeliveries(ctx context.Context, cursor Cursor, held switches, streams map[string]struct{}) []Delivery {
	if f.Outages == nil {
		return nil
	}
	streams[providerStream] = struct{}{}
	now := f.now()

	if !held.away {
		// The provider is answering. If this cursor was standing on an outage,
		// that outage ended, and the people who were told it began are told it is
		// over — with what it was, read back off the mark, because the record of
		// it is gone.
		if cursor.Standing == "" {
			return nil
		}
		cause, since, marked := runstate.ParseProviderOutageMark(cursor.Standing)
		if !marked {
			return []Delivery{{Stream: providerStream, Cursor: Cursor{}}}
		}
		return []Delivery{{
			Stream:       providerStream,
			Cursor:       Cursor{},
			Direct:       true,
			Notification: notify.FromProviderRestored(runstate.DescribeProviderOutage(cause), since, now),
		}}
	}

	mark := held.outage.Mark()
	first := cursor.Standing != mark
	if !first && now.Sub(cursor.Said) < OutageRepeat {
		// Said within the hour. The banner above the four lines carries it in the
		// meantime.
		return nil
	}
	// Whose move it is, and the command that ends a lapsed login, are the
	// attention line's words for the same outage, so the message and `yoyo
	// status` hand the operator the same command.
	mover := readmodel.Stall{Reason: readmodel.ReasonProviderAway, Cause: held.outage.Cause}.Whose()
	if f.Standing != nil {
		mover = f.Standing.OutageAttention(held.outage).Whose()
	}
	return []Delivery{{
		Stream: providerStream,
		Cursor: Cursor{Standing: mark, Said: now},
		Direct: true,
		// The channel tags the operators the first time only: the direct message
		// is what reaches them each hour after.
		Tag: first,
		Notification: notify.FromProviderOutage(notify.ProviderOutage{
			Says:  held.outage.Says(),
			Since: held.outage.Since,
			Mover: mover,
			// The four lines without the banner, which this message's own first
			// sentence already is.
			Standing: f.standingLines(ctx),
		}, now),
	}}
}
