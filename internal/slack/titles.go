package slack

// Every message the sink posts names each work item with its priority, labels,
// and title.
//
// The records the sink reads carry what roles and the harness wrote, and a role
// that names work by its id alone would otherwise put that id in the channel
// exactly as it wrote it. So text no rendering has passed through is rendered
// by the read model's own renderer (readmodel.WorkItemTitles) on its way out:
// a notification's record text before the message is composed, a thread's
// heading, a role's answer in a thread, and what the sink says to a person
// directly. Text the read model has already rendered — where the harness
// stands — is posted as it is, so nothing is rendered twice. The sink has no
// formatter of its own; a message is rendered as the tracker stands when it is
// posted, and is not edited later.
//
// The tracker is listed at most once a minute rather than once a message. A
// catch-up posts hundreds of messages in a row and titles do not change under
// it. A listing that fails is kept as the unreadable reading for the same
// minute, so every item in what is posted meanwhile is marked as unreadable
// rather than left bare, with the prefixes the last good listing carried.

import (
	"context"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

// titleIndexLife is how long one listing of the tracker's titles is used for.
const titleIndexLife = time.Minute

// titleIndex is the sink's listing of what the tracker calls each item. It is
// shared by every goroutine that posts, so it holds its own lock.
type titleIndex struct {
	mu     sync.Mutex
	read   func(ctx context.Context) (*readmodel.WorkItemTitles, error)
	now    func() time.Time
	titles *readmodel.WorkItemTitles
	at     time.Time
	tried  bool
	// prefixes are the tracker prefixes the last good listing carried, kept for
	// a listing that fails.
	prefixes []string
}

// cite is text with every work item in it shown with its state, as the latest
// listing has it. The known ids are items the message is about, shown whatever
// their shape.
func (t *titleIndex) cite(ctx context.Context, text string, known ...string) string {
	if t == nil || t.read == nil || text == "" {
		return text
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reading(ctx).CiteKnowing(text, known...)
}

// name is one work item as a thread is headed with it, in the operator's form.
func (t *titleIndex) name(ctx context.Context, id string) string {
	if t == nil || t.read == nil {
		return id
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reading(ctx).Name(id)
}

// reading is the listing in use, taken again once it is a minute old. The
// caller holds the lock.
func (t *titleIndex) reading(ctx context.Context) *readmodel.WorkItemTitles {
	now := time.Now()
	if t.now != nil {
		now = t.now()
	}
	if !t.tried || now.Sub(t.at) >= titleIndexLife {
		// A failed listing is kept for the same minute a good one is, so a
		// tracker that will not answer is not asked once per message of a
		// catch-up.
		titles, err := t.read(ctx)
		if err != nil || !titles.Readable() {
			prefixes := append(titles.Index().Prefixes(), t.prefixes...)
			titles = readmodel.UnreadableWorkItemTitles(now, prefixes...)
		} else {
			t.prefixes = titles.Index().Prefixes()
		}
		t.titles, t.at, t.tried = titles, now, true
	}
	return t.titles
}

// sourcesTitles lists the titles from the read model's sources.
func sourcesTitles(sources *readmodel.Sources) func(ctx context.Context) (*readmodel.WorkItemTitles, error) {
	if sources == nil || sources.Tracker == nil {
		return nil
	}
	return func(ctx context.Context) (*readmodel.WorkItemTitles, error) {
		return readmodel.ReadWorkItemTitles(ctx, *sources)
	}
}
