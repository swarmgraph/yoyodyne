package slack

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
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
		return nil, l.err
	}
	return readmodel.NewWorkItemTitles(l.items), nil
}

// A role that named work by its number alone — the lane report the operator
// read on 2026-09-26 said "434.9 and 434.3" — reaches the channel with each
// item's title beside its number, whatever the message is.
func TestEveryPostNamesEachWorkItemBesideItsTitle(t *testing.T) {
	t.Parallel()

	listing := &listedTitles{items: []beads.WorkItem{
		{ID: "yoyodyne-ifd.68.12", Title: "Replay a promotion onto the moved target"},
		{ID: "yoyodyne-ifd.434.9", Title: "Price a resumed session at what it moved by"},
		{ID: "yoyodyne-ifd.434.3", Title: "Say the provider's reset in local time"},
	}}
	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{
		filedReport(1, report.SeverityCritical, "blocked on 434.9 and 434.3, and yoyodyne-ifd.999.1 is gone"),
		filedReport(2, report.SeverityCritical, "434.9 still blocks it"),
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
		"(P0) Replay a promotion onto the moved target (yoyodyne-ifd.68.12)",
		"(P0) Price a resumed session at what it moved by (yoyodyne-ifd.434.9)",
		"(P0) Say the provider's reset in local time (yoyodyne-ifd.434.3)",
		"title unavailable (yoyodyne-ifd.999.1)",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("posts = %q, want one to carry %q", said, want)
		}
	}
	// Each message is read on its own: the second report is a message of its
	// own in the thread, and a reader of it alone is owed the title too.
	if last := said[len(said)-1]; !strings.Contains(last, "(P0) Price a resumed session at what it moved by (yoyodyne-ifd.434.9)") {
		t.Errorf("last post = %q, want its item titled in it as well", last)
	}
	if listing.asked != len(posts.requests) {
		t.Errorf("the tracker was listed %d times, want once per post", listing.asked)
	}
}

// An unreadable tracker does not prevent a message, or reuse stale fields.
func TestAnUnreadableTrackerPostsIdentifiersWithTitlesUnavailable(t *testing.T) {
	t.Parallel()
	listing := &listedTitles{err: errors.New("bd: database is locked")}
	index := &titleIndex{read: listing.read}
	for range 3 {
		if got := index.cite(context.Background(), "blocked on yoyodyne-ifd.999.1"); got != "blocked on title unavailable (yoyodyne-ifd.999.1)" {
			t.Fatalf("cite() = %q", got)
		}
	}
	if listing.asked != 3 {
		t.Errorf("tracker reads = %d, want one per message", listing.asked)
	}
}

func TestEachPostReadsTheCurrentPriorityLabelsAndTitle(t *testing.T) {
	t.Parallel()
	item := beads.WorkItem{ID: "yoyodyne-ifd.1", Title: "Original title", Priority: 2}
	listing := &listedTitles{items: []beads.WorkItem{item}}
	index := &titleIndex{read: listing.read}
	first := index.cite(context.Background(), item.ID)
	listing.items[0].Priority = 1
	listing.items[0].Labels = []string{"reliability"}
	listing.items[0].Title = "Current title"
	if got := index.cite(context.Background(), first); got != "(P1, reliability) Current title (yoyodyne-ifd.1)" {
		t.Fatalf("next message = %q, want current tracker fields", got)
	}
	listing.err = errors.New("bd: database is locked")
	if got := index.cite(context.Background(), first); got != "title unavailable (yoyodyne-ifd.1)" {
		t.Fatalf("unreadable tracker = %q, want no stale fields", got)
	}
}
