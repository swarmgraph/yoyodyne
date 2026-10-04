package slack

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// readsReleasedClaims points the feed at a claim log and hands it back, the way
// watchesForStalls does for the stalls beside it.
func (h *testHarness) readsReleasedClaims(t *testing.T) *runstate.ClaimStore {
	t.Helper()
	claims, err := runstate.NewClaimStore(h.root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewClaimStore() error = %v", err)
	}
	h.feed.Claims = claims
	return claims
}

func recordedRelease(at time.Time) runstate.ReleasedClaim {
	return runstate.ReleasedClaim{
		SchemaVersion: runstate.ReleasedClaimSchemaVersion,
		ProductID:     "yoyodyne",
		WorkItemID:    "yoyodyne-ifd.264",
		WorkItemTitle: "Recoverable failures retry with backoff",
		RunID:         "run-264",
		Since:         at.Add(-9 * time.Hour),
		Because:       "its run run-264 is recorded as still in flight and last said anything at " + at.Add(-9*time.Hour).UTC().Format(time.RFC3339),
		ReleasedAt:    at,
	}
}

// The night this exists for, from the reporting side: an item that sat claimed
// with nothing working on it, the harness gave it back, and the operators are
// told once — directly, because a channel is somewhere somebody chooses to look
// and an overnight is not.
func TestAReleasedClaimIsTakenToTheOperatorsOnce(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	claims := harness.readsReleasedClaims(t)
	released := recordedRelease(moment)
	if err := claims.Append(released); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	harness.now = moment.Add(time.Minute)
	batch, err := harness.feed.Poll(t.Context(), harness.start())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	var said *Delivery
	for index, delivery := range batch.Deliveries {
		if delivery.Stream == claimStream && !delivery.Silent() {
			said = &batch.Deliveries[index]
		}
	}
	if said == nil {
		t.Fatalf("nothing was said about the released claim: %+v", batch.Deliveries)
	}
	if !said.Direct {
		t.Fatal("the release was posted to the channel alone, want the operators told directly")
	}
	if said.Notification.Event.Severity != report.SeverityWarning {
		t.Fatalf("severity = %q, want a degraded harness rather than a note", said.Notification.Event.Severity)
	}
	if said.Notification.Event.Kind != notify.KindClaimReleased {
		t.Fatalf("kind = %q, want the release said as itself", said.Notification.Event.Kind)
	}
	// It lands in the item's own thread, where the run that died already said
	// everything it said.
	if said.Notification.Topic.Kind != notify.TopicWorkItem || said.Notification.Topic.ID != released.WorkItemID {
		t.Fatalf("topic = %+v, want the item's own thread", said.Notification.Topic)
	}
	message, err := notify.Render(said.Notification.Topic, said.Notification.Speaker, said.Notification.Event)
	if err != nil {
		t.Fatalf("the release could not be said: %v", err)
	}
	// The notification supplies the identifier; the outgoing message resolves
	// it from the tracker, even when the title saved at release is outdated.
	if !strings.Contains(message.Body, released.WorkItemID) || !strings.Contains(message.Body, released.Because) {
		t.Fatalf("the message does not name the item and what became of its run:\n%s", message.Body)
	}
	listing := &listedTitles{items: []beads.WorkItem{{
		ID: released.WorkItemID, Title: "Retry recoverable failures", Priority: 1, Labels: []string{"reliability"},
	}}}
	index := &titleIndex{read: listing.read}
	body := index.cite(t.Context(), message.Body)
	if !strings.Contains(body, "(P1, reliability) Retry recoverable failures (yoyodyne-ifd.264)") ||
		!strings.Contains(body, released.Because) || strings.Contains(body, released.WorkItemTitle) {
		t.Fatalf("the outgoing message does not carry the current item and its run:\n%s", body)
	}
	if message.Refs.RunID != released.RunID || message.Refs.WorkItemID != released.WorkItemID {
		t.Fatalf("refs = %+v, want the run that left the claim and the item it was on", message.Refs)
	}

	// And it is said once. Every pass after the first re-says nothing, which is
	// the log's position rather than any memory of this process.
	cursors := harness.poll(t, harness.start(), notify.KindClaimReleased)
	for pass := 0; pass < 20; pass++ {
		harness.now = harness.now.Add(7 * time.Minute)
		cursors = harness.poll(t, cursors)
	}
}

// A release older than the moment this sink began reporting is history: it is
// read past rather than announced, exactly as every other log here is.
func TestAReleaseFromBeforeTheSinkBeganIsReadPast(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, moment)
	claims := harness.readsReleasedClaims(t)
	if err := claims.Append(recordedRelease(moment.Add(-time.Hour))); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	harness.now = moment.Add(time.Minute)
	harness.poll(t, harness.start())
}

// A feed with no claim log says everything else and never mentions a release,
// which is what a sink assembled without one has always done.
func TestASinkWithNoClaimLogSaysEverythingElse(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.now = moment.Add(time.Minute)
	batch, err := harness.feed.Poll(t.Context(), harness.start())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if _, carried := batch.Streams[claimStream]; carried {
		t.Fatal("a feed with no claim log declared the stream, so its cursor would be kept for a log nobody reads")
	}
}
