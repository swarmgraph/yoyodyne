package slack

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The 2026-09-17 shape: the Claude Code login expired at 18:17 local, every
// dispatch was refused from then on, and nothing told the operator.
var septemberLoginExpired = time.Date(2026, 9, 17, 15, 17, 0, 0, time.UTC)

// loginExpires gives the feed the outage record and the sources the lines are
// read through, and records the first refused dispatch at the moment the login
// expired.
func (h *testHarness) loginExpires(t *testing.T) *runstate.ProviderOutageStore {
	t.Helper()
	outages, err := runstate.NewProviderOutageStore(h.root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewProviderOutageStore() error = %v", err)
	}
	h.feed.Outages = outages
	h.feed.Standing = &readmodel.Sources{
		Runs:            h.runs,
		Conversations:   h.chats,
		Tracker:         standingTracker{},
		Directives:      h.directives,
		Amendments:      h.amend,
		OperatorHolds:   h.holds,
		IntakeHolds:     h.intake,
		Sessions:        h.watch,
		UsageLimits:     h.limits,
		ProviderOutages: outages,
		Capacity:        3,
		Now:             func() time.Time { return h.now },
		LoginCommand: func(provider domain.Backend, account string) string {
			return "claude auth login # " + string(provider) + " " + account
		},
	}
	h.now = septemberLoginExpired
	if _, err := outages.Notice(runstate.ProviderOutageObservation{
		Cause:        domain.ProviderUnauthenticated,
		Provider:     domain.BackendClaudeCode,
		AccountAlias: "default",
		Detail:       "the claude-code backend is not authenticated for account \"default\"",
		Waiting:      "the dispatch of yoyodyne-ifd.377",
		At:           h.now,
	}); err != nil {
		t.Fatalf("Notice() error = %v", err)
	}
	return outages
}

// provider makes one pass and returns what it said about the provider, posting
// or not.
func (h *testHarness) provider(t *testing.T, cursors Cursors) (Delivery, bool) {
	t.Helper()
	batch, err := h.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	for _, delivery := range batch.Deliveries {
		if delivery.Stream == providerStream {
			return delivery, true
		}
	}
	return Delivery{}, false
}

// A lapsed login replayed: the first pass that reads it says it in the channel,
// tagged, and to the operators directly, naming the provider, the account, and
// the command to run; it is said again directly no more than once an hour while
// it stands; and once the provider answers, a short message says so, directly
// as well. Nothing in any of it prescribes a release.
func TestALapsedLoginReachesTheOperatorsDirectlyAtOnceHourlyAndWhenItEnds(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	outages := harness.loginExpires(t)
	harness.now = septemberLoginExpired.Add(15 * time.Second)

	said, found := harness.provider(t, harness.start())
	if !found || !said.Tag || !said.Direct {
		t.Fatalf("delivery = %+v, found %v; want the outage tagged in the channel and sent to the operators directly the first time it is seen", said, found)
	}
	if severity := said.Notification.Event.Severity; severity != report.SeverityWarning {
		t.Fatalf("severity = %q, want a warning", severity)
	}
	message, err := notify.Render(said.Notification.Topic, said.Notification.Speaker, said.Notification.Event)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	for _, fact := range []string{
		"The provider is not authenticated; the operator must log in",
		"claude-code, account default",
		"Next: the operator's",
		"by running `claude auth login # claude-code default`",
		"nothing is released or restarted",
	} {
		if !strings.Contains(message.Body, fact) {
			t.Fatalf("body %q does not carry %q", message.Body, fact)
		}
	}
	if strings.Contains(message.Body, "yoyo release") {
		t.Fatalf("body %q prescribes a release, which lifts nothing here", message.Body)
	}
	if strings.Count(message.Body, "The provider is not authenticated") != 1 {
		t.Fatalf("body %q says the banner twice", message.Body)
	}
	cursors := harness.poll(t, harness.start(), notify.KindProviderOutage)
	if standing := cursors.Streams[providerStream].Standing; standing != "provider:unauthenticated:2026-09-17T15:17:00Z" {
		t.Fatalf("standing = %q, want the outage marked by its cause and when it began", standing)
	}

	// Within the hour nothing more is said, however many passes are made.
	harness.now = septemberLoginExpired.Add(15*time.Second + OutageRepeat - time.Second)
	if again, more := harness.provider(t, cursors); more {
		t.Fatalf("the outage was said again within the hour: %+v", again)
	}

	// An hour after it was said, it is said again, directly, without tagging the
	// channel a second time.
	harness.now = septemberLoginExpired.Add(15*time.Second + OutageRepeat)
	repeated, found := harness.provider(t, cursors)
	if !found || !repeated.Direct || repeated.Tag || repeated.Notification.Event.Kind != notify.KindProviderOutage {
		t.Fatalf("delivery = %+v, found %v; want the outage said to the operators again directly after an hour", repeated, found)
	}
	cursors = harness.poll(t, cursors, notify.KindProviderOutage)
	if _, more := harness.provider(t, cursors); more {
		t.Fatal("the outage was said twice in the same hour")
	}

	// Three days of it standing is one message an hour and no more.
	repeats := 0
	for at := harness.now; at.Before(septemberLoginExpired.Add(3 * 24 * time.Hour)); at = at.Add(15 * time.Minute) {
		harness.now = at
		if delivery, said := harness.provider(t, cursors); said {
			repeats++
			cursors = harness.poll(t, cursors, notify.KindProviderOutage)
			if !delivery.Direct {
				t.Fatalf("delivery = %+v, want every repeat sent directly", delivery)
			}
		}
	}
	if repeats > 3*24 {
		t.Fatalf("the outage was said %d times in three days, want at most once an hour", repeats)
	}

	// The operator logs in. The next pass says the line carried on, once, in a
	// short message sent directly, and the cursor forgets the outage so the next
	// one is said afresh.
	if _, _, err := outages.Clear(); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	harness.now = septemberLoginExpired.Add(3 * 24 * time.Hour)
	restored, found := harness.provider(t, cursors)
	if !found || restored.Notification.Event.Kind != notify.KindProviderRestored {
		t.Fatalf("delivery = %+v, found %v; want the provider answering again said once", restored, found)
	}
	if !restored.Direct || restored.Tag {
		t.Fatalf("delivery = %+v, want the restoration sent to the operators directly without tagging the channel", restored)
	}
	message, err = notify.Render(restored.Notification.Topic, restored.Notification.Speaker, restored.Notification.Event)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if !strings.Contains(message.Body, "The provider is answering again after 3 days") || !strings.Contains(message.Body, "not authenticated") {
		t.Fatalf("body %q does not say what was restored and how long it stood", message.Body)
	}
	if restored.Cursor.Standing != "" {
		t.Fatalf("cursor = %+v, want the outage forgotten once it ended", restored.Cursor)
	}
	cursors = harness.poll(t, cursors, notify.KindProviderRestored)
	if _, more := harness.provider(t, cursors); more {
		t.Fatal("the restoration was said twice")
	}
}

// The operators are named by member id in the channel on the one message that
// is theirs to act on, and on nothing else.
func TestTheOutageMessageNamesEveryOperatorByMemberID(t *testing.T) {
	t.Parallel()

	if got := taggedAll([]string{"U1", " U2 ", ""}, "the line has stopped"); got != "<@U1> <@U2> the line has stopped" {
		t.Fatalf("taggedAll() = %q", got)
	}
	if got := taggedAll(nil, "as rendered"); got != "as rendered" {
		t.Fatalf("taggedAll() with nobody = %q, want the text as rendered", got)
	}
}
