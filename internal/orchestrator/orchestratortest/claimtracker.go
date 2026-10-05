package orchestratortest

import (
	"context"
	"sync"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// ClaimTracker records every released item and the note the audit wrote on it.
// Mu is shared with the fixture's run store and release log.
type ClaimTracker struct {
	Mu          *sync.Mutex
	Released    map[string]string
	Order       []string
	FailRelease error
}

func (h *ClaimTracker) Release(_ context.Context, id, reason string) (beads.WorkItem, error) {
	h.Mu.Lock()
	defer h.Mu.Unlock()
	if h.FailRelease != nil {
		return beads.WorkItem{}, h.FailRelease
	}
	h.Released[id] = reason
	h.Order = append(h.Order, id)
	return beads.WorkItem{ID: id, Status: "open"}, nil
}
