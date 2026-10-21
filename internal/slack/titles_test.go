package slack

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// listedTitles is a tracker listing for the sink, counting how often it was
// asked, so a catch-up that listed the tracker once per message is visible.
type listedTitles struct {
	items []beads.WorkItem
	err   error
	asked int
}

func (l *listedTitles) read(context.Context) (*readmodel.WorkItemTitles, error) {
	l.asked++
	if l.err != nil {
		return readmodel.UnreadableWorkItemTitles(time.Now()), l.err
	}
	return readmodel.NewWorkItemTitles(l.items), nil
}

// A role that names work by its id reaches the channel with each item's
// priority, labels, and title beside it, whatever the message is, and a work
// item's thread is headed in the operator's form.
func TestEveryPostNamesEachWorkItemWithItsState(t *testing.T) {
	t.Parallel()

	listing := &listedTitles{items: []beads.WorkItem{
		{ID: "yoyodyne-ifd.68.12", Title: "Replay a promotion onto the moved target", Priority: 1, Labels: []string{"reliability"}},
		{ID: "yoyodyne-ifd.434.9", Title: "Price a resumed session at what it moved by", Priority: 2},
		{ID: "yoyodyne-ifd.434.3", Title: "Say the provider's reset in local time", Priority: 3, Labels: []string{"cost"}},
	}}
	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{
		filedReport(1, report.SeverityCritical, "blocked on yoyodyne-ifd.434.9 and yoyodyne-ifd.434.3, and yoyodyne-ifd.999.1 is gone"),
		filedReport(2, report.SeverityCritical, "yoyodyne-ifd.434.9 still blocks it"),
	}}, posts)
	sink.citing = &titleIndex{read: listing.read}

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	var said []string
	for _, request := range posts.requests {
		said = append(said, request.Text)
	}
	all := strings.Join(said, "\n---\n")
	for _, want := range []string{
		"*(P1, reliability) Replay a promotion onto the moved target (yoyodyne-ifd.68.12)*",
		"yoyodyne-ifd.434.9 (P2: Price a resumed session at what it moved by)",
		"yoyodyne-ifd.434.3 (P3, cost: Say the provider's reset in local time)",
		"yoyodyne-ifd.999.1 (not in the tracker)",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("posts = %q, want one to carry %q", said, want)
		}
	}
	// Each message is rendered on its own: a reader of the last alone is owed
	// the item's state too.
	if last := said[len(said)-1]; !strings.Contains(last, "yoyodyne-ifd.434.9 (P2: Price a resumed session at what it moved by)") {
		t.Errorf("last post = %q, want its item shown with its state as well", last)
	}
	if listing.asked != 1 {
		t.Errorf("the tracker was listed %d times for one pass, want once", listing.asked)
	}
}

// A tracker that cannot be listed marks every item in what is posted as
// unreadable, including a root id a message is about, which has no shape that
// says it is an item; and the tracker is not asked again for every message
// behind it.
func TestATrackerThatCannotBeListedMarksEveryItemPosted(t *testing.T) {
	t.Parallel()

	listing := &listedTitles{err: errors.New("bd: database is locked")}
	now := time.Date(2026, 9, 26, 16, 0, 0, 0, time.UTC)
	index := &titleIndex{read: listing.read, now: func() time.Time { return now }, prefixes: []string{"yoyodyne", "calc"}}
	said := "tracker unreadable at " + now.Local().Format("15:04 MST")
	for range 3 {
		if got, want := index.cite(context.Background(), "blocked on yoyodyne-ifd.999.1"), "blocked on yoyodyne-ifd.999.1 ("+said+")"; got != want {
			t.Fatalf("cite() = %q, want %q", got, want)
		}
	}
	if got, want := index.cite(context.Background(), "calc-wja is stuck", "calc-wja"), "calc-wja ("+said+") is stuck"; got != want {
		t.Errorf("cite(known root id) = %q, want %q", got, want)
	}
	if listing.asked != 1 {
		t.Errorf("a failing tracker was asked %d times within a minute, want once", listing.asked)
	}
	now = now.Add(titleIndexLife)
	index.cite(context.Background(), "yoyodyne-ifd.1")
	if listing.asked != 2 {
		t.Errorf("the tracker was asked %d times after the listing expired, want it asked again", listing.asked)
	}
}

// A message about a root item whose tracker could not be read is posted with
// that item marked, through the sink's own path.
func TestAPostAboutAnUnreadableRootItemMarksIt(t *testing.T) {
	t.Parallel()

	listing := &listedTitles{err: errors.New("bd: database is locked")}
	delivery := filedReport(1, report.SeverityCritical, "calc-wja is stuck at its checks")
	delivery.Notification.Topic = notify.Topic{Kind: notify.TopicProduct}
	delivery.Notification.Event.Refs.WorkItemID = "calc-wja"
	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{delivery}}, posts)
	sink.citing = &titleIndex{read: listing.read}

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	var all []string
	for _, request := range posts.requests {
		all = append(all, request.Text)
	}
	if joined := strings.Join(all, "\n"); !strings.Contains(joined, "calc-wja (tracker unreadable at ") {
		t.Errorf("posts = %q, want the root item marked as unreadable", all)
	}
}

// A thread is headed with its item in the operator's form, and a title that
// names another item is posted as the tracker holds it: text read from the
// tracker is never searched for references.
func TestAThreadHeadingWhoseTitleNamesAnotherItemIsPostedAsTheTrackerHoldsIt(t *testing.T) {
	t.Parallel()

	listing := &listedTitles{items: []beads.WorkItem{
		{ID: "yoyodyne-ifd.68.12", Title: "Fix yoyodyne-ifd.434.9's timeout", Priority: 0, Labels: []string{"bug"}},
		{ID: "yoyodyne-ifd.434.9", Title: "Price a resumed session at what it moved by", Priority: 1},
	}}
	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{
		filedReport(1, report.SeverityCritical, "still blocked"),
	}}, posts)
	sink.citing = &titleIndex{read: listing.read}

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) == 0 {
		t.Fatal("nothing was posted")
	}
	if got, want := posts.requests[0].Text, "*(P0, bug) Fix yoyodyne-ifd.434.9's timeout (yoyodyne-ifd.68.12)*"; got != want {
		t.Errorf("thread heading = %q, want %q", got, want)
	}
}
