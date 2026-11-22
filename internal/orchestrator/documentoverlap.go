package orchestrator

// The unpublished documents that overlap an open work item, listed in the
// architect's and the Lead Product Manager's passes until they are answered
// (docs/designs/unpublished-designs-and-admitted-work.md, sections 1 to 3 and
// 5).
//
// A document waits in a role's conversation until its revision is on the branch
// it is filed on, and while it waits it can contradict a decision recorded on a
// work item admitted meanwhile. Finding the overlap takes only the document
// records (what each names and revises, and when it was written) and the
// tracker. Nothing here is stored: every pass works the list out afresh, so a
// restart loses nothing, and nothing here is put to the operator.

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// RecurringUnpublishedDocuments lists every document a role has written that is
// not yet published, as the conversation records keep them. It is satisfied in
// internal/cli over the conversation store.
type RecurringUnpublishedDocuments interface {
	Unpublished() ([]runstate.PendingWrite, error)
}

// RecurringOpenWork lists the tracker's work items. It changes nothing.
type RecurringOpenWork interface {
	List(ctx context.Context, status string) ([]beads.WorkItem, error)
}

// DocumentOverlap is one unpublished document revision and one open work item it
// overlaps, and why.
type DocumentOverlap struct {
	// Document is the document's identifier, Revision the identifier of this
	// revision's record, and Written when it was written, which is the zero time
	// for a record written before that was kept.
	Document string
	Revision string
	Written  time.Time
	Item     beads.WorkItem
	// Why says in words why the two overlap.
	Why string
}

// AgreementNotePrefix opens the note the harness writes on an item when the
// architect records that a revision agrees with it. Only a note that opens with
// it and has exactly the shape AgreementNote writes answers an overlap.
const AgreementNotePrefix = "Design agreement: "

// agreementSettle is how long after an agreement note's own time the tracker's
// last-changed time for the item may stand and still be the note's own write
// rather than a later change: the note is stamped before the tracker records it.
const agreementSettle = time.Minute

var agreementShape = regexp.MustCompile(`^Design agreement: the architect checked revision (\S+) of (\S+) against this item as it stood at (\S+), and found that they agree\. Recorded (\S+)\.$`)

// AgreementNote is the note recording that the architect checked the revision
// of the document against the item as it stood at a time, and found that they
// agree, written at recorded. It is the one wording AgreementNote's readers
// accept, so whatever writes the note writes it with this.
func AgreementNote(document, revision string, stood, recorded time.Time) string {
	return fmt.Sprintf("%sthe architect checked revision %s of %s against this item as it stood at %s, and found that they agree. Recorded %s.",
		AgreementNotePrefix, revision, document, stood.UTC().Format(time.RFC3339), recorded.UTC().Format(time.RFC3339))
}

// agreement is one agreement note read back from an item.
type agreement struct {
	revision string
	document string
	recorded time.Time
}

func readAgreement(text string) (agreement, bool) {
	found := agreementShape.FindStringSubmatch(strings.TrimSpace(text))
	if found == nil {
		return agreement{}, false
	}
	recorded, err := time.Parse(time.RFC3339, found[4])
	if err != nil {
		return agreement{}, false
	}
	return agreement{revision: found[1], document: found[2], recorded: recorded}, true
}

// overlapReason is how the document and the item overlap, or empty where they
// do not: the document names the item, or revises a document the item cites.
func overlapReason(write runstate.PendingWrite, item beads.WorkItem) string {
	if slices.Contains(write.Names, item.ID) {
		return "the document names this item"
	}
	text := strings.Join([]string{item.Description, item.Design, item.AcceptanceCriteria, item.Notes}, "\n")
	for _, revised := range write.Revises {
		if path := strings.TrimSpace(revised.Path); path != "" && strings.Contains(text, path) {
			return fmt.Sprintf("the document revises %s (%s), which this item cites by its file", revised.ID, path)
		}
		if citesDocumentID(text, revised.ID) {
			return fmt.Sprintf("the document revises %s, which this item cites by its identifier", revised.ID)
		}
	}
	return ""
}

// citesDocumentID reports whether text holds the document identifier as a word
// of its own. An identifier of one plain word, such as "brief", is read in
// prose far more often than it is cited, so it counts only as a file path.
func citesDocumentID(text, id string) bool {
	id = strings.TrimSpace(id)
	if id == "" || !strings.ContainsAny(id, "-0123456789") {
		return false
	}
	return regexp.MustCompile(`(?:^|[^A-Za-z0-9_-])` + regexp.QuoteMeta(id) + `(?:$|[^A-Za-z0-9_-])`).MatchString(text)
}

// answered reports whether the overlap has been answered: the item carries an
// agreement note naming this revision, dated after everything else that has
// happened to the item, or a report of the conflict naming this revision and
// this item has been filed. Where the tracker did not say when the item last
// changed, an agreement cannot be placed after it and the overlap stands.
func (o DocumentOverlap) answered(reports []report.Report) bool {
	for _, filed := range reports {
		if strings.Contains(filed.Message, o.Revision) && strings.Contains(filed.Message, o.Item.ID) {
			return true
		}
	}
	records := beads.NoteRecords(o.Item.RecordedNotes())
	own, latest := -1, time.Time{}
	for i, record := range records {
		note, ok := readAgreement(record.Text)
		if !ok {
			continue
		}
		if note.recorded.After(latest) {
			latest = note.recorded
		}
		if note.revision == o.Revision && note.document == o.Document {
			own = i
		}
	}
	if own < 0 || o.Item.UpdatedAt.IsZero() {
		return false
	}
	// A note that is not an agreement, written after this one, is a newer entry.
	for _, record := range records[own+1:] {
		if _, ok := readAgreement(record.Text); !ok && strings.TrimSpace(record.Text) != "" {
			return false
		}
	}
	return !o.Item.UpdatedAt.After(latest.Add(agreementSettle))
}

// FindDocumentOverlaps is the unanswered overlaps of the unpublished documents
// with the open items, ordered by the revision's record and then the item. A
// closed item overlaps nothing. A document is listed against an item once however
// many ways they overlap.
func FindDocumentOverlaps(writes []runstate.PendingWrite, items []beads.WorkItem, reports []report.Report) []DocumentOverlap {
	var found []DocumentOverlap
	seen := map[string]bool{}
	for _, write := range writes {
		write = write.WithRelations()
		for _, item := range items {
			if item.Status == "closed" {
				continue
			}
			reason := overlapReason(write, item)
			key := write.ID + "|" + item.ID
			if reason == "" || seen[key] {
				continue
			}
			seen[key] = true
			overlap := DocumentOverlap{Document: strings.TrimSpace(write.Artifact), Revision: write.ID, Written: write.WrittenAt, Item: item, Why: reason}
			if !overlap.answered(reports) {
				found = append(found, overlap)
			}
		}
	}
	slices.SortStableFunc(found, func(a, b DocumentOverlap) int {
		if c := a.Written.Compare(b.Written); c != 0 {
			return c
		}
		if c := strings.Compare(a.Revision, b.Revision); c != 0 {
			return c
		}
		return strings.Compare(a.Item.ID, b.Item.ID)
	})
	return found
}

// maxListedOverlaps and maxOverlapItemBytes bound the listing a pass is handed.
const (
	maxListedOverlaps   = 20
	maxOverlapItemBytes = 160
	maxOverlapNoteBytes = 160
)

// documentOverlapRoles reports whether a pass of the role is handed the list:
// the architect's, whose documents they are, and the Lead Product Manager's,
// whose items they overlap. A program manager instance's pass is neither.
func documentOverlapRoles(role domain.AgentRole, agent string) bool {
	return agent == "" && (role == domain.RoleArchitect || role == domain.RoleProductManager)
}

// documentOverlaps reads the unanswered overlaps, and says what could not be
// read. Without the documents or the tracker there is nothing to list, and the
// problem says so.
func (t Trigger) documentOverlaps(ctx context.Context) ([]DocumentOverlap, string) {
	if t.Unpublished == nil || t.OpenWork == nil {
		return nil, ""
	}
	writes, err := t.Unpublished.Unpublished()
	if err != nil {
		return nil, fmt.Sprintf("the unpublished documents could not be read, so none was checked against the open work: %v", err)
	}
	if len(writes) == 0 {
		return nil, ""
	}
	items, err := t.OpenWork.List(ctx, "")
	if err != nil {
		return nil, fmt.Sprintf("the work items could not be read, so no unpublished document was checked against them: %v", err)
	}
	var reports []report.Report
	problem := ""
	if t.Pile != nil {
		if reports, err = t.Pile.List(); err != nil {
			problem = fmt.Sprintf("the reports could not be read, so a conflict already reported is listed again: %v", err)
		}
	}
	return FindDocumentOverlaps(writes, items, reports), problem
}

// documentOverlapSection is the list handed to a pass of the role, which is
// nothing where no overlap stands.
func documentOverlapSection(role domain.AgentRole, overlaps []DocumentOverlap) string {
	if len(overlaps) == 0 {
		return ""
	}
	var section strings.Builder
	section.WriteString("## Unpublished documents that overlap open work\n\n")
	fmt.Fprintf(&section, "%d overlap(s) between a document written and not yet published and an open work item, oldest first. Each stays here until it is answered.", len(overlaps))
	if role == domain.RoleArchitect {
		section.WriteString(" Check each item's recorded decisions and its done condition against the document: they disagree where landing the document would make the done condition false or impossible to meet, would require work a recorded decision refuses, or would refuse work the item needs. Where they do, revise the document so they agree, or report the conflict to the Lead Product Manager naming the revision and the item. Wording that differs while both say the same thing is not a disagreement. Recording that they agree is built separately, so an overlap you find in agreement stays listed until it is.")
	} else {
		section.WriteString(" The architect judges whether each disagrees. Where she reports a conflict to you, deciding the item's side is yours: revise the item, record a new decision, or ask her to change the design. Nothing here is for the operator.")
	}
	section.WriteString("\n\n")
	for i, overlap := range overlaps {
		if i == maxListedOverlaps {
			fmt.Fprintf(&section, "%d more are not listed here and stay listed on later passes.\n", len(overlaps)-i)
			break
		}
		fmt.Fprintf(&section, "- Document %s, revision %s, %s; item %s (%s): %s. %s\n",
			overlap.Document, overlap.Revision, writtenAtSaid(overlap.Written),
			overlap.Item.ID, oneline.Fold(overlap.Item.Title, maxOverlapItemBytes), overlap.Why, newestEntrySaid(overlap))
	}
	return section.String()
}

func writtenAtSaid(written time.Time) string {
	if written.IsZero() {
		return "written at a time not recorded"
	}
	return "written " + localStamp(written)
}

// newestEntrySaid is the item's newest entry with its time, and whether it is
// newer than the document.
func newestEntrySaid(overlap DocumentOverlap) string {
	updated := overlap.Item.UpdatedAt
	if updated.IsZero() {
		return "The tracker does not say when the item last changed."
	}
	said := "The item last changed " + localStamp(updated)
	if !overlap.Written.IsZero() {
		if updated.After(overlap.Written) {
			said += ", after the document was written"
		} else {
			said += ", before the document was written"
		}
	}
	said += "."
	records := beads.NoteRecords(overlap.Item.RecordedNotes())
	for i := len(records) - 1; i >= 0; i-- {
		if text := strings.TrimSpace(records[i].Text); text != "" {
			return said + " Its newest note begins: " + oneline.Fold(text, maxOverlapNoteBytes)
		}
	}
	return said
}

// documentOverlapDigest is the lines the check before a scheduled pass compares:
// one per overlap and the item's last change, so a new overlap or a newer entry
// on an overlapping item starts the pass.
func documentOverlapDigest(overlaps []DocumentOverlap) string {
	lines := make([]string, 0, len(overlaps))
	for _, overlap := range overlaps {
		lines = append(lines, overlap.Revision+"|"+overlap.Item.ID+"|"+overlap.Item.UpdatedAt.UTC().Format(time.RFC3339))
	}
	return digestLines(lines)
}
