package cli

import (
	"context"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

// epicMerged gives the forge check the members of the epics the operator named
// and has not ended, with whether each epic is delivered as status shows it, and
// records feedback on a member's merged pull request in the epic's progress
// (orchestrator's epicmergedfeedback.go).
type epicMerged struct{ parts components }

func epicMergedFrom(parts components) orchestrator.EpicMergedFeedback {
	return epicMerged{parts}
}

// Members is empty with epic mode off, so nothing is read for a project that
// names no epic.
func (e epicMerged) Members(ctx context.Context) ([]orchestrator.EpicMember, error) {
	record, err := epicscope.Read(e.parts.stateRoot, e.parts.config.Product.ID)
	if err != nil || !record.On() || len(record.OpenEpics()) == 0 {
		return nil, err
	}
	view, err := epicprogress.Read(e.parts.stateRoot, e.parts.config.Product.ID, record)
	if err != nil {
		return nil, err
	}
	delivered := map[string]bool{}
	if standing := readmodel.ReadStanding(ctx, standingSources(e.parts.configPath)); standing.Epics != nil {
		for _, open := range standing.Epics.Open {
			delivered[open.WorkItemID] = open.Delivered()
		}
	}
	return epicMembers(record, view, delivered), nil
}

// epicMembers is each item of each open naming whose progress can be read. An
// epic status could not place is read as not delivered.
func epicMembers(record epicscope.Record, view epicprogress.View, delivered map[string]bool) []orchestrator.EpicMember {
	var members []orchestrator.EpicMember
	for _, epic := range record.OpenEpics() {
		naming := epicprogress.Of(epic)
		if view.Unreadable(naming) != "" {
			continue
		}
		for _, item := range view.Items(epic) {
			members = append(members, orchestrator.EpicMember{Epic: epic.WorkItemID, NamedAt: naming.NamedAt, WorkItemID: item, Delivered: delivered[epic.WorkItemID]})
		}
	}
	return members
}

func (e epicMerged) Owe(_ context.Context, comment orchestrator.MergedComment) (bool, error) {
	store, err := e.parts.epicProgress()
	if err != nil {
		return false, err
	}
	return oweMergedComment(store, comment)
}

// oweMergedComment writes one comment and the correction it owes, saying what the
// comment asks in the words the development manager is shown.
func oweMergedComment(store *epicprogress.Store, comment orchestrator.MergedComment) (bool, error) {
	item := comment.Item
	where := ""
	if item.Path != "" {
		where = " on " + item.Path
		if item.Line > 0 {
			where += fmt.Sprintf(" line %d", item.Line)
		}
	}
	missing := fmt.Sprintf("%s said on merged pull request #%d of %s%s: %s", item.Author, comment.PullRequest, comment.Member.WorkItemID, where, oneline.Bound(item.Body, 1500))
	naming := epicprogress.Naming{WorkItemID: comment.Member.Epic, NamedAt: comment.Member.NamedAt.UTC()}
	_, made, err := store.MergedCommentRead(naming, epicprogress.Comment{ID: item.ID, Member: comment.Member.WorkItemID, PullRequest: comment.PullRequest}, missing)
	return made, err
}
