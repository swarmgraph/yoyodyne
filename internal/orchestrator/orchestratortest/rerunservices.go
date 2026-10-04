package orchestratortest

import (
	"context"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/publish"
)

// RerunServices records the tracker, worktree, and forge requests a rerun makes.
type RerunServices struct {
	// ItemErr refuses the item reading.
	Item    beads.WorkItem
	ItemErr error
	// Released records successful release notes.
	Released   []string
	ReleaseErr error
	// Retired counts removal requests.
	Retirement gitworktree.Retirement
	Retired    int
	RetireErr  error
	// Closed and Deleted record successful removals.
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

// Show returns the scripted item or reading error.
func (h *RerunServices) Show(context.Context, string) (beads.WorkItem, error) {
	return h.Item, h.ItemErr
}

// Release records the release note and opens the item unless refused.
func (h *RerunServices) Release(_ context.Context, _ string, reason string) (beads.WorkItem, error) {
	if h.ReleaseErr != nil {
		return beads.WorkItem{}, h.ReleaseErr
	}
	h.Released = append(h.Released, reason)
	h.Item.Status = "open"
	return h.Item, nil
}
