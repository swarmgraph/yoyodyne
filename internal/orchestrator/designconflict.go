package orchestrator

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A confirmed design can wait a long time before it lands: held for want of
// access to the companion intent repository, held by the intake hold, waiting
// for another document's landing, or with a run in flight. While it waits, the
// Lead Product Manager may admit a work item that relies on what the landed
// design says and the architect's revision no longer says it, and neither role
// would be told. flagDesignConflicts is the check that tells both. It runs each
// time a confirmed design is offered for publication, before anything else is
// decided about it, and it never stops or changes the publication: any failure
// in it is ignored.
//
// What it finds is deliberately narrow. An item disagrees with the held design
// only when the item names the design (its artifact id as a whole word, or its
// path) and quotes a passage of the landed copy that the held copy does not
// contain. It finds only disagreements an item states by quoting the design, and
// nothing subtler: an item that paraphrases, or that contradicts the design
// without quoting it, is not found.

// unfinishedLister is the part of a work tracker that lists every item not yet
// closed. beads.Client has it; a tracker without it is not checked against.
type unfinishedLister interface {
	ListUnfinished(ctx context.Context) ([]beads.WorkItem, error)
}

// reportLister is the part of a report collector that reads back what it holds.
// runstate.ReportStore has it. Without it the check cannot tell a conflict it
// has already reported from a new one, so it reports nothing rather than
// repeating itself at every message.
type reportLister interface {
	List() ([]report.Report, error)
}

// minQuotedPassage is the fewest characters, after whitespace is collapsed, that
// a quotation must have to count as an item relying on the design's wording. A
// shorter one is a phrase that turns up in any document.
const minQuotedPassage = 20

// maxConflictPassage cuts the passage shown in a report.
const maxConflictPassage = 200

var quotedPassages = []*regexp.Regexp{
	regexp.MustCompile(`"([^"]+)"`),
	regexp.MustCompile(`“([^”]+)”`),
	regexp.MustCompile("`([^`]+)`"),
}

// collapseSpace folds every run of whitespace to one space, so a passage wrapped
// differently in the item and the design is still the same passage.
func collapseSpace(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// designConflicts returns, for each item that disagrees with the held design,
// the first passage that makes it so. landed is empty for a design that has not
// landed, which no item can disagree with.
func designConflicts(design artifact.Artifact, landed, held string, items []beads.WorkItem) map[string]string {
	conflicts := map[string]string{}
	landed, held = collapseSpace(landed), collapseSpace(held)
	if landed == "" {
		return conflicts
	}
	for _, item := range items {
		text := strings.Join([]string{item.Title, item.Description, item.Design, item.AcceptanceCriteria, item.Notes}, "\n")
		if !namesDesign(text, design) {
			continue
		}
		for _, passage := range quotedIn(text) {
			if strings.Contains(landed, passage) && !strings.Contains(held, passage) {
				conflicts[item.ID] = passage
				break
			}
		}
	}
	return conflicts
}

// namesDesign reports whether text names the design: its artifact id as a whole
// word, or its path.
func namesDesign(text string, design artifact.Artifact) bool {
	if design.Path != "" && strings.Contains(text, design.Path) {
		return true
	}
	if design.ID == "" {
		return false
	}
	return regexp.MustCompile(`(^|[^A-Za-z0-9_-])` + regexp.QuoteMeta(design.ID) + `($|[^A-Za-z0-9_-])`).MatchString(text)
}

// quotedIn lists the passages in text between straight double quotes, curly
// quotes or backticks that are long enough to count, with their whitespace
// collapsed.
func quotedIn(text string) []string {
	var passages []string
	for _, pattern := range quotedPassages {
		for _, match := range pattern.FindAllStringSubmatch(text, -1) {
			if passage := collapseSpace(match[1]); utf8.RuneCountInString(passage) >= minQuotedPassage {
				passages = append(passages, passage)
			}
		}
	}
	return passages
}

// flagDesignConflicts files the two reports for each admitted item that
// disagrees with a held design, once for each handoff and item.
func (p Pipeline) flagDesignConflicts(ctx context.Context, document runstate.DocumentPublication) {
	design := document.Candidate.Artifact
	if design.Kind != artifact.KindDesign || p.Reports == nil {
		return
	}
	lister, ok := p.Tracker.(unfinishedLister)
	if !ok {
		return
	}
	filed, ok := p.Reports.(reportLister)
	if !ok {
		return
	}
	ctx, stop := trackerCallContext(ctx)
	defer stop()
	landed := p.landedDocumentCopy(ctx, design.Path)
	if collapseSpace(landed) == "" {
		return
	}
	items, err := lister.ListUnfinished(ctx)
	if err != nil {
		return
	}
	conflicts := designConflicts(design, landed, document.Candidate.Content, items)
	if len(conflicts) == 0 {
		return
	}
	existing, err := filed.List()
	if err != nil {
		return
	}
	for _, item := range items {
		passage, conflicting := conflicts[item.ID]
		if !conflicting {
			continue
		}
		message := designConflictMessage(document, item, passage)
		for _, addressee := range []domain.AgentRole{document.Owner, domain.RoleProductManager} {
			if alreadyReported(existing, item.ID, addressee, document.WriteID) {
				continue
			}
			id, err := report.NewID()
			if err != nil {
				return
			}
			filing := report.Report{
				SchemaVersion: report.SchemaVersion, ID: id, Role: report.HarnessReporter,
				RunID: document.RunID(), WorkItemID: item.ID, Build: p.Build,
				ProductID: p.Config.Product.ID, RepositoryID: string(p.Config.Product.RepositoryID),
				Severity: report.SeverityWarning, Message: message, For: addressee,
				RecordedAt: p.clock().Now(),
			}
			if filing.Validate() != nil || p.Reports.Append(filing) != nil {
				continue
			}
			existing = append(existing, filing)
		}
	}
}

func designConflictMessage(document runstate.DocumentPublication, item beads.WorkItem, passage string) string {
	design := document.Candidate.Artifact
	if utf8.RuneCountInString(passage) > maxConflictPassage {
		passage = string([]rune(passage)[:maxConflictPassage]) + "..."
	}
	return fmt.Sprintf("Design %s (%s), confirmed in conversation %s (document %s) and not yet landed, no longer says %q, which admitted work item %s (%s) quotes. One of them has to change: the %s revises the design, or the %s revises or withdraws the item.",
		design.ID, oneline.Fold(design.Title, 120), document.ConversationID, document.WriteID, passage,
		item.ID, oneline.Fold(item.Title, 120), document.Owner.Title(), domain.RoleProductManager.Title())
}

// alreadyReported reports whether a report for this item, addressed to this
// role, already names this handoff.
func alreadyReported(existing []report.Report, itemID string, addressee domain.AgentRole, writeID string) bool {
	for _, r := range existing {
		if r.WorkItemID == itemID && r.For == addressee && strings.Contains(r.Message, "(document "+writeID+")") {
			return true
		}
	}
	return false
}

// landedDocumentCopy is the document's file as the target branch of the
// repository its home is in holds it, or empty where it holds none or cannot be
// read. That repository is the companion intent repository where the project has
// one, and the project's own otherwise. A companion project whose pipeline has no
// manager for its repository is not checked: in production that happens only when
// the repository's folder is missing (buildIntentWorktrees in internal/cli), so
// there would be nothing to read. Only the branch's committed file is read, never
// a working tree.
func (p Pipeline) landedDocumentCopy(ctx context.Context, path string) string {
	tree := p.Worktrees
	if p.Config.Product.HasIntentRepository() {
		if p.IntentWorktrees == nil {
			return ""
		}
		tree = p.IntentWorktrees
	}
	if tree == nil {
		return ""
	}
	branch, err := tree.CurrentBranch(ctx)
	if err != nil {
		return ""
	}
	commit := branch
	if commits, ok := tree.(targetCommits); ok {
		if commit, err = commits.TargetCommit(ctx, branch); err != nil {
			return ""
		}
	}
	file, err := tree.FileAtCommit(ctx, commit, path, maxDocumentHistoryBytes)
	if err != nil {
		return ""
	}
	return string(file.Content)
}
