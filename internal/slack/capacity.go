package slack

// The provider holding every role, said again while it stands and louder as it
// ages.
//
// The stall beside this reports a line stopped for reasons the record cannot
// name, and the window reports a session waiting out a limit it met itself.
// Neither said anything between 2026-09-08 and 09-13. The session choosing work
// was idle over items waiting on a decision, so it recorded no window; the role
// that would have decided was refused twenty times a day, and every refusal was
// said once in the channel as itself. Nothing said the thing they added up to:
// every role held, on a reset five days off, with nothing configured to move
// the work elsewhere. The operator heard about it from his assistant.
//
// This is the capacity half of that alarm, and it is shaped the opposite way
// from the window on purpose. A window is the provider's ordinary behaviour and
// is said once, as a note, in the channel. A hold over every role is the
// harness stopped by something a person can change — enabling failover on the
// agents is what would have moved the work — so it goes to the operators
// directly the first time it is seen, is said again in the channel every
// heartbeat it stands, and once it has stood long enough that no timer ends it
// it is said as critical and taken to them directly again with each repetition.
// The reading is the read model's; what this decides is only that it is worth
// somebody's phone, and how often.

import (
	"context"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// DefaultCapacityEscalation is how long the provider may hold every role before
// the hold stops being a warning said in the channel and becomes a critical
// one. Whether a repetition also goes to the operators directly is the read
// model's answer for the hold, not this bar's.
//
// Six hours is the longest a run itself will wait out a limit — the project's
// `usage_limit_max_pause` ships at that, and its comment says why: the maximum
// covers the five-hour limit and deliberately stops short of the seven-day one,
// so a capacity problem that needs a person reaches one instead of a timer. A
// hold that has outlasted the longest wait any run makes is that problem, and
// the same bar applies to saying so.
const DefaultCapacityEscalation = 6 * time.Hour

// capacityDeliveries says the provider holding every role, again while it
// stands.
//
// It is read through the same sources the four lines are read through, because
// the hold is one of them now — the banner above the lines, and an entry on the
// attention line — and a sink deriving it a second way would be a channel and a
// terminal disagreeing about whether every role is held.
func (f *HarnessFeed) capacityDeliveries(ctx context.Context, cursor Cursor, streams map[string]struct{}) ([]Delivery, error) {
	if f.Standing == nil || (f.Standing.UsageLimits == nil && f.Standing.Runs == nil) || len(f.Standing.Agents) == 0 {
		return nil, nil
	}
	streams[capacityStream] = struct{}{}

	now := f.now()
	hold, problem := readmodel.CapacityHoldOf(*f.Standing, now)
	if problem != "" {
		// A record that cannot be read is said where the sink says everything
		// else about itself, and asked again at the next pass. A hold the other
		// record still shows is said all the same — the two only ever add
		// refusals, so a hold read from one stands whatever the other holds —
		// but no hold over a record nobody could read is not a lifted hold, and
		// the sink must not guess in that direction: the cursor is left as it is.
		f.say("whether the provider is holding every role could not be read in full: %s", problem)
		if !hold.Holding {
			return nil, nil
		}
	}
	if !hold.Holding {
		// The hold lifted, or there never was one. What lifted it says so itself —
		// the turn that was served, the run that started — and the cursor forgets
		// it so the next hold to stand is said afresh rather than inheriting this
		// one's clock.
		if cursor.Standing != "" {
			return []Delivery{{Stream: capacityStream, Cursor: Cursor{}}}, nil
		}
		return nil, nil
	}

	mark := hold.Mark()
	if cursor.Standing == mark && now.Sub(cursor.Said) < f.heartbeat() {
		return nil, nil
	}
	first := cursor.Standing != mark
	armed := Cursor{Standing: mark, Said: now}

	// A warning while it is young, and critical once it has stood past the bar
	// above. The first sighting goes to the operators directly whatever its age,
	// once, so they are told: nothing else in the record says this, and the
	// channel is somewhere somebody chooses to look. Whose the hold is after that
	// is the read model's answer (the ownership registry's rule for the hold),
	// not this sink's: a repetition goes to the operators directly only where
	// that answer names him, and otherwise stays in the channel, louder as it
	// stands.
	severity := report.SeverityWarning
	direct := first
	if now.Sub(hold.Since) >= f.capacityEscalation() {
		severity = report.SeverityCritical
		if waiting, ok := hold.Attention(); ok && waiting.Mover.IsOperator() {
			direct = true
		}
	}
	return []Delivery{{
		Stream: capacityStream,
		Cursor: armed,
		Direct: direct,
		Notification: notify.FromCapacityHold(notify.CapacityHold{
			Says:  hold.Says(),
			Since: hold.Since,
			Mover: hold.Whose(),
			// The four lines without the banner, which this message's own first
			// sentence already is.
			Standing: f.standingLines(ctx),
		}, severity, now),
	}}, nil
}

// capacityEscalation is how long a hold stands before it is said as critical.
func (f *HarnessFeed) capacityEscalation() time.Duration {
	if f.CapacityEscalation > 0 {
		return f.CapacityEscalation
	}
	return DefaultCapacityEscalation
}
