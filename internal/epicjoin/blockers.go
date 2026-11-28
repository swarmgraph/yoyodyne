package epicjoin

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// An open item outside a named epic can still come to block a member though the
// harness refuses such a link when a role writes it: a link from before that
// rule, one a person wrote to the tracker directly, or a closed item opened again.
// Each scheduler pass follows the links from every open epic, and each such item
// is one question about scope to the Lead Product Manager, naming the add that
// would bring it in. It never joins and starts nothing.
// docs/designs/epic-mode-second-part.md, "An item outside the epic that could
// block a member", point 5, states the rule.

// outsideBlocker is one such item, in the naming it could block a member of.
type outsideBlocker struct {
	naming  epicprogress.Naming
	entry   epicprogress.Entry
	blocker beads.OutsideBlocker
}

// outsideBlockers is every open item outside an open epic that could block one of
// its members, its joined pieces or its own item, read from the tracker now. An
// epic whose progress cannot be read is passed over, as it is by every other step.
func (j Joiner) outsideBlockers(ctx context.Context) ([]outsideBlocker, error) {
	view, err := j.read()
	if err != nil || !view.Record.On() || len(view.Record.OpenEpics()) == 0 {
		return nil, err
	}
	items, err := j.Items(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the tracker for what could block a member: %w", err)
	}
	var found []outsideBlocker
	for _, epic := range view.Record.OpenEpics() {
		naming := epicprogress.Of(epic)
		if view.Unreadable(naming) != "" {
			continue
		}
		entry, _ := view.Entry(naming)
		inside := append([]string{epic.WorkItemID}, view.Items(epic)...)
		for _, blocker := range beads.OutsideBlockers(inside, func(id string) bool { return slices.Contains(inside, id) }, items) {
			found = append(found, outsideBlocker{naming, entry, blocker})
		}
	}
	return found, nil
}

// blockersOwed reports whether a question about such an item is owed: none is
// kept for it, or the one kept was not filed.
func (j Joiner) blockersOwed(ctx context.Context) (bool, error) {
	if j.Evidence.Reports == nil {
		return false, nil
	}
	found, err := j.outsideBlockers(ctx)
	if err != nil || len(found) == 0 {
		return false, err
	}
	filed, _, err := j.reportsRead()
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(found, func(one outsideBlocker) bool {
		kept, asked := one.entry.Blocker(one.blocker.ID)
		return !asked || !filed[kept.Report]
	}), nil
}

// askBlockers files the question owed for each such item. Its identifier is
// written into the epic's progress before it is filed, so a restart between the
// two files that same report and no second, and an item asked about once is
// never asked about again in that naming.
func (j Joiner) askBlockers(ctx context.Context) error {
	if j.Evidence.Reports == nil {
		return nil
	}
	found, err := j.outsideBlockers(ctx)
	if err != nil || len(found) == 0 {
		return err
	}
	filed, _, err := j.reportsRead()
	if err != nil {
		return err
	}
	var problems []error
	for _, one := range found {
		kept, asked := one.entry.Blocker(one.blocker.ID)
		if asked && filed[kept.Report] {
			continue
		}
		if !asked {
			id, err := report.NewID()
			if err == nil {
				kept = epicprogress.Blocker{WorkItemID: one.blocker.ID, Member: one.blocker.Member, Link: one.blocker.Link(), Report: id, At: j.Now().UTC()}
				kept.Report, err = j.Progress.BlockerAsked(one.naming, kept)
			}
			if err != nil {
				problems = append(problems, fmt.Errorf("keep the question about %s, which could block a member of epic %s: %w", one.blocker.ID, one.naming.WorkItemID, err))
				continue
			}
		}
		problems = append(problems, j.Evidence.Reports.Append(j.blockerQuestion(one, kept)))
	}
	return errors.Join(problems...)
}

// blockerQuestion is the report that asks about one such item.
func (j Joiner) blockerQuestion(one outsideBlocker, kept epicprogress.Blocker) report.Report {
	epic, item := one.naming.WorkItemID, kept.WorkItemID
	message := fmt.Sprintf("A question about scope: %s is open and outside epic %s, which the operator named, and could block its member %s: %s. "+
		"Nothing outside the named epics starts, so %s waits on it, and it does not join on its own. "+
		"Record your handling of this report as the answer: have the work rewritten so %s no longer needs %s, or ask the operator whether to add it, "+
		"for the reason %s, with `yoyo epic add %s %s`.",
		describeBlocker(one.blocker), epic, kept.Member, kept.Link, kept.Member, kept.Member, item, ownership.OperatorOwnHold, epic, item)
	return report.Report{
		SchemaVersion: report.SchemaVersion, ID: kept.Report, Role: report.HarnessReporter, RunID: report.OutsideEpicBlockerRun + epic,
		WorkItemID: item, DuringEpic: epic, Build: j.Evidence.Build, ProductID: j.Product, RepositoryID: j.Evidence.RepositoryID,
		Severity: report.SeverityWarning, For: domain.RoleProductManager,
		Message: oneline.Fold(message, report.MaxMessageBytes-len(oneline.Marker)), RecordedAt: kept.At,
	}
}

func describeBlocker(blocker beads.OutsideBlocker) string {
	if title := strings.TrimSpace(blocker.Title); title != "" {
		return fmt.Sprintf("work item %s (%s)", blocker.ID, title)
	}
	return "work item " + blocker.ID
}
