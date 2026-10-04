package orchestratortest

import (
	"context"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/publish"
)

// RerunServices records the tracker, worktree, and forge requests a rerun makes.
type RerunServices struct {
	// Item is the tracker's current answer; ItemErr refuses that reading.
	Item    beads.WorkItem
	ItemErr error
	// Released records every claim release note; ReleaseErr refuses the write.
	Released   []string
	ReleaseErr error
	// Retirement answers removal, and Retired counts requests for it.
	Retirement gitworktree.Retirement
	Retired    int
	RetireErr  error
	// Closed and Deleted record the publication and remote branch removals.
	Closed    []publish.CloseRequest
	CloseErr  error
	Deleted   []string
	DeleteErr error
}

func (h *RerunServices) RetirePreserved(context.Context, gitworktree.Worktree, string) (gitworktree.Retirement, error) {
	h.Retired++
	return h.Retirement, h.RetireErr
}

func (h *RerunServices) DeleteRemoteBranch(_ context.Context, worktree gitworktree.Worktree, _ string) error {
	if h.DeleteErr != nil {
		return h.DeleteErr
	}
	h.Deleted = append(h.Deleted, worktree.Branch)
	return nil
}

func (h *RerunServices) Close(_ context.Context, request publish.CloseRequest) (publish.Closure, error) {
	if h.CloseErr != nil {
		return publish.Closure{}, h.CloseErr
	}
	h.Closed = append(h.Closed, request)
	return publish.Closure{Closed: true, State: "CLOSED"}, nil
}

// Show is the tracker's answer about the item, read and never written. A harness
// leaves the item open; the sequences that are about the item's own state are the
// ones that move it.
func (h *RerunServices) Show(context.Context, string) (beads.WorkItem, error) {
	return h.Item, h.ItemErr
}

// Release is the one write a re-run makes to the item: giving back a claim the
// stopped run left on it. Each note is kept so a test can read what the item was
// told, and releaseErr is a tracker that would not take the write.
func (h *RerunServices) Release(_ context.Context, _ string, reason string) (beads.WorkItem, error) {
	if h.ReleaseErr != nil {
		return beads.WorkItem{}, h.ReleaseErr
	}
	h.Released = append(h.Released, reason)
	h.Item.Status = "open"
	return h.Item, nil
}
