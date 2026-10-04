package slack

// Every message the sink posts names each work item beside its title.
//
// The records the sink reads carry what roles and the harness wrote, and a role
// that names work by its number alone — "434.9 and 434.3" — would otherwise put
// that number in the channel exactly as it wrote it. So the text of every post
// is read through the read model's own resolution on its way out, which is the
// one place every message passes: a thread's opening message, a milestone, a
// digest, the hourly line and the needs-a-human line under it, a direct message,
// and an answer in a thread.
//
// The tracker is read for each message after pacing, so changing an item's
// priority or labels is visible in the next post, including a catch-up.

import (
	"context"

	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

// titleIndex uses the shared reading for every post rather than holding a
// surface-owned copy of the tracker's fields between messages.
type titleIndex struct {
	read func(ctx context.Context) (*readmodel.WorkItemTitles, error)
}

func (t *titleIndex) cite(ctx context.Context, text string) string {
	var titles *readmodel.WorkItemTitles
	if t != nil && t.read != nil && text != "" {
		read, err := t.read(ctx)
		if err == nil {
			titles = read
		}
	}
	return titles.Cite(text)
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
