package slack

// The provider answering nobody, said once to the people who end it, and once
// more when it answers again.
//
// From 2026-09-17 18:17 local the operator's Claude Code login had expired.
// Every dispatch was refused, every recurring pass recorded 0 turns, the runs
// already going spent their relaunch budgets and blocked, and the intake brake
// tripped over three of them — which was the one thing that did reach the
// channel, and it prescribed `yoyo release`, which lifts nothing here. The
// operator learned what had happened by asking, three days later, and the
// assistant lifted the brake by hand.
//
// This is shaped the opposite way from the capacity hold beside it, on purpose.
// A hold is repeated every heartbeat while it stands, because a person can end
// it early only by changing the configuration and needs reminding. An outage is
// said once, tagged to the operators by member id — it is both important and
// theirs to act on, which is the communication rule's own test for a tag — and
// then left alone: the line's own banner carries it while it stands, and what a
// repeated message would buy is a reason to mute the channel. When the provider
// answers again the operator is told once more, as a note, because they were
// told the line had stopped and are owed being told it carried on by itself.

import (
	"context"

	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// outageDeliveries says the provider answering nobody, once, and the provider
// answering again, once.
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
		// that outage ended, and the person who was told it began is told it is
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
			Notification: notify.FromProviderRestored(runstate.DescribeProviderOutage(cause), since, now),
		}}
	}

	mark := held.outage.Mark()
	if cursor.Standing == mark {
		// Said already, and said once. The banner above the four lines carries it
		// while it stands.
		return nil
	}
	return []Delivery{{
		Stream: providerStream,
		Cursor: Cursor{Standing: mark, Said: now},
		Tag:    true,
		Notification: notify.FromProviderOutage(notify.ProviderOutage{
			Says:  held.outage.Says(),
			Since: held.outage.Since,
			Mover: readmodel.Stall{Reason: readmodel.ReasonProviderAway, Cause: held.outage.Cause}.Whose(),
			// The four lines without the banner, which this message's own first
			// sentence already is.
			Standing: f.standingLines(ctx),
		}, now),
	}}
}
