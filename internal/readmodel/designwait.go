package readmodel

// Work waiting on the architect's design, as the factory problems carry it.
//
// "Showing the wait" in docs/designs/needs-design-gate.md: an item carrying the
// needs-design label appears among the factory problems as a problem the
// architect owns, waiting on her design since a time in local time with the
// zone, with its age. Each item is its own entry, P0 first and then the
// longest wait, so a P0 cannot sit unseen in her queue behind lower work. The
// entry is a held-work entry, the kind the ownership registry already gives the
// architect for this label, and is on the attention line too, which is where
// `yoyo status` prints it.
//
// The time is the harness's own record (runstate.NeedsDesignLabelled), written
// by a watching session's pulls, because the tracker keeps no time for a label
// being added. An item that record does not yet name is still shown, saying
// that since when is not recorded, rather than left out.

import (
	"fmt"
	"sort"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// NeedsDesignLabelled is since when each item has carried the needs-design
// label. It is satisfied by *runstate.NeedsDesignLabelledStore, whose Load reads
// past a field a newer build added.
type NeedsDesignLabelled interface {
	Load() (runstate.NeedsDesignLabelled, bool, error)
}

// readNeedsDesignSince is the record's answer for one item, or nothing where
// no record was wired or it could not be read, with why it could not.
func readNeedsDesignSince(sources Sources) (func(string) (time.Time, bool), string) {
	if sources.NeedsDesignLabelled == nil {
		return nil, ""
	}
	record, _, err := sources.NeedsDesignLabelled.Load()
	if err != nil {
		return nil, fmt.Sprintf("since when each item has waited on the architect's design could not be read: %v", err)
	}
	return record.Since, ""
}

// DesignWaits is every admitted item in the queue carrying the needs-design
// label, one entry each, P0 first, then the longest wait, then by id. The queue
// is expected to carry since when each has waited (backlog.Queue.
// WithNeedsDesignSince); an item it does not is said as not yet recorded.
func DesignWaits(queue backlog.Queue, now time.Time) []Attention {
	var waiting []backlog.Entry
	for _, entry := range queue.Entries {
		if entry.NeedsDesign {
			waiting = append(waiting, entry)
		}
	}
	sort.SliceStable(waiting, func(i, j int) bool {
		a, b := waiting[i], waiting[j]
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		// A wait whose start is known is longer than one nobody recorded yet,
		// which began at the latest pull.
		aKnown, bKnown := !a.NeedsDesignSince.IsZero(), !b.NeedsDesignSince.IsZero()
		if aKnown != bKnown {
			return aKnown
		}
		if !a.NeedsDesignSince.Equal(b.NeedsDesignSince) {
			return a.NeedsDesignSince.Before(b.NeedsDesignSince)
		}
		return a.ID < b.ID
	})
	attention := make([]Attention, 0, len(waiting))
	for _, entry := range waiting {
		attention = append(attention, designWaitAttention(entry, now))
	}
	return attention
}

// designWaitAttention is one item waiting on the architect's design.
func designWaitAttention(entry backlog.Entry, now time.Time) Attention {
	priority := entry.Priority
	held := &HeldWork{Awaiting: HeldAwaitingDesign, Count: 1, Priority: &priority}
	if !entry.NeedsDesignSince.IsZero() {
		since := entry.NeedsDesignSince
		held.Since = &since
		held.Waited = max(now.Sub(since), 0)
	}
	return resolved(Attention{Kind: AttentionHeldWork, ID: entry.ID, WorkItemID: entry.ID, HeldWork: held})
}

// designWaitSays is the entry's what: the item, its priority, and since when
// it has waited, from the fields alone.
func (h HeldWork) designWaitSays(item string) string {
	named := item
	if h.Priority != nil {
		named = fmt.Sprintf("p%d %s", *h.Priority, item)
	}
	if h.Since == nil {
		return fmt.Sprintf("%s is waiting on the architect's design, labelled %s; since when is not recorded yet, because no watching session has read the label since it was put on",
			named, domain.NeedsDesignLabel)
	}
	return fmt.Sprintf("%s waiting on the architect's design since %s, %s ago, labelled %s",
		named, localMoment(*h.Since), spanSaid(h.Waited), domain.NeedsDesignLabel)
}
