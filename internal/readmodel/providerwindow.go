package readmodel

// The provider refusing the harness for want of capacity, read once for every
// surface that says it.
//
// It is the accounting that was missing on 2026-09-05. A watch session waited
// out a usage window from 12:13Z to 13:43Z; nothing anywhere recorded that it
// was waiting, so the stall watchdog read ninety minutes of a ready queue with
// no run started, found nothing that accounted for it, and woke the operator
// over a machine that was behaving exactly as the provider had told it to. The
// pause was the accounting. What was missing was somewhere for it to be said.
//
// The clause is derived here rather than at each surface for the reason every
// other derivation in this package is: the watchdog, the four lines, and the
// channel all say this, and three wordings of one state is a disagreement only
// the operator can adjudicate.

import (
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ProviderWindow is a session waiting out a provider's usage window, as the
// session that met it recorded: that it is waiting, when it said so, and when
// the provider said the window lifts where it named a time.
type ProviderWindow struct {
	// Waiting is a session that recorded itself inside the window. A zero
	// ProviderWindow is no window at all rather than one of unknown length, so
	// nothing is accounted for by it.
	Waiting bool `json:"waiting,omitempty"`
	// ResetsAt is when the provider said the window lifts. It is zero where the
	// provider named no time, which is a different fact from a wait of unknown
	// length: the harness asks again rather than being told when.
	ResetsAt time.Time `json:"resets_at,omitempty"`
	// Since is when the session recorded itself waiting, which is what bounds a
	// window the provider named no reset time for and what says how long this has
	// been standing.
	Since time.Time `json:"since,omitempty"`
}

// Says is the window as the one sentence every surface states it in. It carries
// no remedy, because there is none: a usage window is not shortened by anything
// a person does.
//
// It is a sentence rather than a clause, and that is the operator's own
// acceptance rather than a stylistic choice. What he asked for, in his words, is
// that the cause is the first words of any message that reaches him — "Paused on
// the provider's usage window until HH:MM" opening the message, rather than an
// alarm with the cause left to archaeology. So every surface that says this
// state puts this first and writes its own sentences after it.
//
// The time is said as the hour and minute in UTC, which is how the operator
// writes one and is short enough to sit inside a sentence. A window the provider
// named no time for says so rather than inventing one, because "until" with
// nothing after it is the confident emptiness every answer here avoids.
func (w ProviderWindow) Says() string {
	if !w.Waiting {
		return ""
	}
	if w.ResetsAt.IsZero() {
		return "Paused on the provider's usage limit; the provider named no time it resets"
	}
	return "Paused on the provider's usage limit until " + w.ResetsAt.UTC().Format("15:04") + "Z"
}

// Standing reports a window that is still accounting for a quiet line at the
// moment asked about.
//
// A window the provider timed accounts for the silence until that time and not a
// second past it: a session still choosing nothing after its own recorded window
// lifted is a session that has stopped working, which is exactly what the stall
// watchdog exists to catch and must not be silenced for.
//
// A window the provider named no time for is bounded by how long the session's
// own word may stand unrefreshed, measured from when it said it was waiting. The
// alternative is a record that silences the watchdog forever — a session that
// died inside an untimed wait writes nothing further, and its last word would go
// on accounting for every hour after it.
//
// That bound is DefaultRunActivityWindow rather than the stall threshold, and
// the difference matters now that the threshold is minutes. The question here is
// not how long a gap an operator wants to hear about; it is how long an
// unrefreshed session record is still evidence, which is the same question a
// run's own record answers and gets the same hour. Bounding it by the alarm bar
// instead would page somebody ten minutes into an untimed wait — and untimed
// waits are real: the monthly overage allowance reports exactly that way, so it
// would be a page for the provider behaving normally, which is the thing this
// whole change exists to stop.
func (w ProviderWindow) Standing(now time.Time) bool {
	if !w.Waiting {
		return false
	}
	if !w.ResetsAt.IsZero() {
		return now.Before(w.ResetsAt)
	}
	if w.Since.IsZero() {
		return false
	}
	return now.Sub(w.Since) < DefaultRunActivityWindow
}

// WaitingOnProvider is the window the sessions that choose work last recorded
// themselves inside, or no window at all.
//
// It reads the live sessions rather than the log's last line, for the reason
// Choosing does: one log holds every session a product has had, and a last entry
// can be one session stopping while another carries on. A session that is
// choosing, braked, or resumed is not waiting on a provider whatever an earlier
// poll said, so the newest live transition is the whole of the answer — a window
// this session has already come out of is history.
func WaitingOnProvider(sessions []runstate.WatchTransition) ProviderWindow {
	// Live is newest first, so the first entry is the latest word from a session
	// that has not stopped.
	live := Live(sessions)
	if len(live) == 0 {
		return ProviderWindow{}
	}
	latest := live[0]
	if !latest.ProviderWindow {
		return ProviderWindow{}
	}
	window := ProviderWindow{Waiting: true, Since: latest.At}
	if latest.ProviderWindowResetsAt != nil {
		window.ResetsAt = latest.ProviderWindowResetsAt.UTC()
	}
	return window
}
