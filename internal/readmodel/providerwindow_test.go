package readmodel

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The 2026-09-05 window, as the record holds it: a session that entered the
// provider's usage window at 12:13Z and said the provider would serve again at
// 13:43Z. Read from the log, that is the whole of what tells this silence from
// the one the watchdog exists to catch.
func TestASessionWaitingOnTheProviderIsReadAsWaitingRatherThanIdle(t *testing.T) {
	t.Parallel()

	opened := time.Date(2026, 9, 5, 12, 13, 0, 0, time.UTC)
	lifts := time.Date(2026, 9, 5, 13, 43, 0, 0, time.UTC)
	sessions := []runstate.WatchTransition{
		{SessionID: "watch-1", State: runstate.WatchWatching, At: opened.Add(-time.Hour)},
		{
			SessionID: "watch-1", State: runstate.WatchIdle, At: opened,
			ProviderWindow: true, ProviderWindowResetsAt: &lifts,
		},
	}

	stall := WhyNothingStarts(Conditions{Sessions: held(sessions...), Now: opened.Add(30 * time.Minute)})
	if stall.Reason != ReasonProviderWindow {
		t.Fatalf("reason = %q, want the provider's window named as itself", stall.Reason)
	}
	// The operator's own acceptance, word for word: the cause is the first words,
	// with the time the provider named.
	if stall.Says != "Paused on the provider's usage limit until 13:43Z" {
		t.Fatalf("says = %q, want the operator's sentence with the reset time", stall.Says)
	}
	if !stall.Since.Equal(opened) {
		t.Fatalf("since = %s, want the moment the session entered the window", stall.Since)
	}
	// Nobody has to do anything about it, which is the difference between this and
	// every other reason a queue is not being pulled from.
	if !strings.HasPrefix(stall.Reason.Whose(), "nobody's") {
		t.Fatalf("whose = %q, want nobody's move", stall.Reason.Whose())
	}
	if waiting, attention := stall.Waiting(); attention {
		t.Fatalf("Waiting() = %+v, want a window nobody is waited on for kept off the attention line", waiting)
	}

	// Once the window has lifted the session is choosing nothing for no reason
	// anybody recorded, which is the ordinary idle and is said as one.
	lifted := WhyNothingStarts(Conditions{Sessions: held(sessions...), Now: lifts.Add(time.Minute)})
	if lifted.Reason != ReasonSessionIdle {
		t.Fatalf("reason = %q, want a lifted window to stop accounting for anything", lifted.Reason)
	}
}

// A window is the last word of the session that recorded it, and a session that
// has gone on to do something else is not inside one. Both directions matter:
// the window must not survive the session leaving it, and it must not be found
// on a session that has stopped.
func TestOnlyALiveSessionsLatestWordCarriesAWindow(t *testing.T) {
	t.Parallel()

	lifts := moment.Add(time.Hour)
	window := runstate.WatchTransition{
		SessionID: "watch-1", State: runstate.WatchIdle, At: moment,
		ProviderWindow: true, ProviderWindowResetsAt: &lifts,
	}
	for name, sessions := range map[string][]runstate.WatchTransition{
		"the session went back to choosing work": {
			window,
			{SessionID: "watch-1", State: runstate.WatchResumed, At: moment.Add(10 * time.Minute)},
		},
		"the session idled again for something else": {
			window,
			{SessionID: "watch-1", State: runstate.WatchIdle, At: moment.Add(10 * time.Minute), Reason: "the backlog is empty"},
		},
		"the session stopped": {
			window,
			{SessionID: "watch-1", State: runstate.WatchStopped, At: moment.Add(10 * time.Minute)},
		},
	} {
		if found := WaitingOnProvider(sessions); found.Waiting {
			t.Fatalf("%s: WaitingOnProvider() = %+v, want no window", name, found)
		}
	}

	if found := WaitingOnProvider([]runstate.WatchTransition{window}); !found.Waiting {
		t.Fatal("WaitingOnProvider() found no window on the session's own latest word")
	}
}

// A window the provider named no reset time for stops accounting once the
// session's word is stale, rather than standing forever. The record is a
// session's last word, so a session that died inside an untimed wait would
// otherwise account for every hour after it — which is a way to switch the
// watchdog off rather than to inform it.
//
// The bound is how long an unrefreshed record is still evidence rather than the
// operator's alarm bar, which is a different question and now a much smaller
// number. Untimed windows are real — the monthly overage allowance reports
// exactly that way — so bounding one at the alarm bar would page somebody ten
// minutes into a provider behaving normally.
func TestAnUntimedWindowStopsAccountingOnceTheSessionsWordIsStale(t *testing.T) {
	t.Parallel()

	untimed := ProviderWindow{Waiting: true, Since: moment}
	if !untimed.Standing(moment.Add(DefaultRunActivityWindow - time.Minute)) {
		t.Fatal("an untimed window accounted for nothing while the session's word was still fresh")
	}
	if untimed.Standing(moment.Add(DefaultRunActivityWindow + time.Minute)) {
		t.Fatal("an untimed window went on accounting for the quiet past a stale record")
	}
	if untimed.Standing(moment.Add(DefaultStallThreshold+time.Minute)) != true {
		t.Fatal("an untimed window stopped accounting at the alarm bar, which would page somebody over an overage allowance")
	}
	if says := untimed.Says(); strings.Contains(says, "until") {
		t.Fatalf("says = %q, want no reset time invented for a provider that named none", says)
	}
	// A window nothing recorded accounts for nothing and says nothing, which is
	// what keeps the zero value from silencing anything.
	none := ProviderWindow{}
	if none.Standing(moment) || none.Says() != "" {
		t.Fatalf("the zero window accounted for the quiet or said something: %+v / %q", none, none.Says())
	}
}
