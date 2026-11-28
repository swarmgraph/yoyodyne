package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// epicMerged gives the forge check the members of the epics the operator named
// and has not ended, with whether each epic is delivered as status shows it, and
// records feedback on a member's merged pull request in the epic's progress
// (orchestrator's epicmergedfeedback.go): a correction owed while the epic is not
// delivered, and once it is, one parked item for the Lead Product Manager.
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
			if open.State != "" {
				delivered[open.WorkItemID] = open.Delivered()
			}
		}
	}
	return epicMembers(record, view, delivered), nil
}

// epicMembers is each item of each open naming whose progress can be read and
// whose state status placed, with whether it is delivered. An epic status could
// not place is skipped for this reading: its comments are neither acted on nor
// recorded as read, and are read again by the next check.
func epicMembers(record epicscope.Record, view epicprogress.View, delivered map[string]bool) []orchestrator.EpicMember {
	var members []orchestrator.EpicMember
	for _, epic := range record.OpenEpics() {
		naming := epicprogress.Of(epic)
		if _, placed := delivered[epic.WorkItemID]; !placed || view.Unreadable(naming) != "" {
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
	missing := fmt.Sprintf("%s said on merged pull request #%d of %s%s: %s", item.Author, comment.PullRequest, comment.Member.WorkItemID, whereSaid(item), oneline.Bound(item.Body, 1500))
	naming := epicprogress.Naming{WorkItemID: comment.Member.Epic, NamedAt: comment.Member.NamedAt.UTC()}
	_, made, err := store.MergedCommentRead(naming, epicprogress.Comment{ID: item.ID, Member: comment.Member.WorkItemID, PullRequest: comment.PullRequest}, missing)
	return made, err
}

func (e epicMerged) AfterDelivery(ctx context.Context, comment orchestrator.MergedComment) (bool, error) {
	store, err := e.parts.epicProgress()
	if err != nil {
		return false, err
	}
	return fileDeliveredComment(ctx, store, e.parts.tracker(), comment)
}

// fileDeliveredComment writes a comment read once its epic was delivered into the
// epic's progress, and then files the one parked item it becomes and writes that
// item into the comment. The item carries a marker line in its notes, so a stop
// between filing it and writing its identifier finds that item again rather than
// filing a second. It reports whether this call filed the item.
func fileDeliveredComment(ctx context.Context, store *epicprogress.Store, filer orchestrator.WorkFiler, comment orchestrator.MergedComment) (bool, error) {
	item, epic := comment.Item, comment.Member.Epic
	naming := epicprogress.Naming{WorkItemID: epic, NamedAt: comment.Member.NamedAt.UTC()}
	says := fmt.Sprintf("%s said%s: %s", item.Author, whereSaid(item), oneline.Bound(item.Body, 1500))
	kept, err := store.DeliveredCommentRead(naming, epicprogress.Comment{ID: item.ID, Member: comment.Member.WorkItemID, PullRequest: comment.PullRequest}, says)
	if err != nil || kept.Later == nil || kept.Later.Item != "" {
		return false, err
	}
	marker := epicprogress.LaterMarker(epic, item.ID)
	filed, err := markedItem(ctx, filer, marker)
	if err != nil {
		return false, fmt.Errorf("read whether an item was already filed for it: %w", err)
	}
	made := filed == ""
	if made {
		add := fmt.Sprintf("yoyo epic add %s <this item>", epic)
		created, err := filer.Create(ctx, beads.NewWorkItem{
			Title: oneline.Bound(fmt.Sprintf("Feedback after epic %s was delivered: %s on pull request #%d of %s", epic, item.Author, comment.PullRequest, comment.Member.WorkItemID), 200),
			Description: fmt.Sprintf("%s commented on pull request #%d of %s after it merged, once epic %s, which the operator named, was delivered:\n\n%s\n\n%s\n\n"+
				"This item is parked and starts nothing. The Lead Product Manager decides what it needs: where it should be done, she asks the operator whether to add it to the epic, for the reason %s, with `%s`, which opens the epic again, and unparks it once it is added; where it needs nothing, she closes it.",
				item.Author, comment.PullRequest, comment.Member.WorkItemID, epic, quoteLines(item.Body), item.URL, ownership.OperatorOwnHold, add),
			Type:    "task",
			Notes:   fmt.Sprintf("Filed by the harness for feedback on a member of epic %s after the epic was delivered.\n%s", epic, marker),
			Parking: domain.WorkItemParking(fmt.Sprintf("feedback on epic %s after it was delivered: it waits for the Lead Product Manager to decide whether to ask the operator to add it with %s, and is unparked once added", epic, add)),
			Origin:  domain.WorkItemOrigin{Asker: domain.AskerHarness},
		})
		if err != nil {
			return false, fmt.Errorf("file the item for review comment %s: %w", item.ID, err)
		}
		filed = created.ID
	}
	return made, store.DeliveredCommentFiled(naming, item.ID, filed)
}

// markedItem is the unfinished item whose notes carry the marker, where one does.
func markedItem(ctx context.Context, filer orchestrator.WorkFiler, marker string) (string, error) {
	for _, status := range []string{"open", "in_progress", "blocked"} {
		items, err := filer.List(ctx, status)
		if err != nil {
			return "", err
		}
		for _, item := range items {
			for _, line := range strings.Split(item.Notes, "\n") {
				if strings.TrimSpace(line) == marker {
					return item.ID, nil
				}
			}
		}
	}
	return "", nil
}

// whereSaid is the file and line a comment was left on, where it was.
func whereSaid(item runstate.ForgeFeedbackItem) string {
	if item.Path == "" {
		return ""
	}
	if item.Line > 0 {
		return fmt.Sprintf(" on %s line %d", item.Path, item.Line)
	}
	return " on " + item.Path
}

// maxQuotedBytes bounds what of a comment its item quotes; the forge keeps the
// whole of it, at the link the item gives.
const maxQuotedBytes = 4000

// quoteLines quotes what a person wrote, so no line of it reads as the harness's.
func quoteLines(body string) string {
	if len(body) > maxQuotedBytes {
		body = strings.ToValidUTF8(body[:maxQuotedBytes], "") + " [cut]"
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	for at, line := range lines {
		lines[at] = "> " + line
	}
	return strings.Join(lines, "\n")
}

// epicFeedbackOf is the feedback left after delivery on the open namings, as the
// read model words it for the Lead Product Manager's turns. Each item it became is
// read from the tracker, so one she closed is no longer shown; one that cannot be
// read is shown.
type epicFeedbackOf struct{ parts components }

func (e epicFeedbackOf) FeedbackAfterDelivery() ([]string, error) {
	record, err := epicscope.Read(e.parts.stateRoot, e.parts.config.Product.ID)
	if err != nil || !record.On() || len(record.OpenEpics()) == 0 {
		return nil, err
	}
	view, err := epicprogress.Read(e.parts.stateRoot, e.parts.config.Product.ID, record)
	if err != nil {
		return nil, err
	}
	items := map[string]beads.WorkItem{}
	for _, epic := range record.OpenEpics() {
		entry, _ := view.Entry(epicprogress.Of(epic))
		for _, comment := range entry.Comments {
			if comment.Later == nil || comment.Later.Item == "" {
				continue
			}
			if item, err := e.parts.tracker().Show(context.Background(), comment.Later.Item); err == nil {
				items[item.ID] = item
			}
		}
	}
	return readmodel.EpicFeedbackWaiting(record, view, items), nil
}
