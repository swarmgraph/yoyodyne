package orchestrator

// Leaving out of the architect's pass the items whose ruling stands.
//
// An item the architect has written a design or ruling for stays open, and so
// stays in the work waiting in her conversation, until the document lands. A
// document can wait a long time to land — behind a full waiting limit, a hold
// on sending, or a run under review — and every recurring pass in between was
// handed the item again and ruled on it again, rewriting a finished design whole
// and paying for each pass. So a pass is no longer handed an item whose ruling
// stands.
//
// # When a ruling stands
//
// The document a role writes for an item leaves a note on it
// (runstate.RulingNoteOpening). Once a pass's turns are over, the harness takes
// a snapshot of every waiting item whose newest note is the architect's ruling,
// leaving out of "newest" only the harness's own notes about her passes and her
// recorded agreements, which say what she did rather than anything new. The
// snapshot is a digest of the item's text, notes, status, priority, labels,
// when it last changed, and the documents it cites by file.
//
// The task's next pass leaves the item out where its digest now is the one the
// last completed pass recorded and no report naming the item has been filed
// since that snapshot. Any new note, any answer written on it, any edit or
// status change, any change to a document it cites, and any report naming it
// puts it back in front of her. Anything that cannot be read is a change: the
// item is handed to her as before.
//
// # What the record says
//
// The pass's record names the items it left out (runstate.Sweep.RulingsStanding),
// `yoyo sweeps` says so beside what the pass was handed, and the pass's own
// message tells her which items were left out and what puts one back.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// KeepsStandingRulings reports whether a role's recurring pass leaves out the
// items whose ruling stands. It is the architect's, whose rulings these are.
func KeepsStandingRulings(role domain.AgentRole) bool {
	return role == domain.RoleArchitect
}

// StandingRulings is the snapshots the task's last completed pass took, by
// item, less those a report filed since has named: the rulings that stand as
// long as the item still reads the same.
type StandingRulings map[string]runstate.RulingSnapshot

// Holds reports whether the item's snapshot now is the one recorded, so its
// ruling stands and the pass leaves it out.
func (s StandingRulings) Holds(now runstate.RulingSnapshot) bool {
	before, recorded := s[now.ID]
	return recorded && before.Digest != "" && before.Digest == now.Digest
}

var citedDocumentPath = regexp.MustCompile(`docs/[A-Za-z0-9_./-]+\.md`)

// CitedDocuments is the repository documents an item cites by file, in its
// description, design, acceptance criteria and notes, each once and sorted. A
// path that climbs out of the directory it names is not one.
func CitedDocuments(item beads.WorkItem) []string {
	text := strings.Join([]string{item.Description, item.Design, item.AcceptanceCriteria, beads.PlainNotes(item.RecordedNotes())}, "\n")
	var paths []string
	for _, path := range citedDocumentPath.FindAllString(text, -1) {
		if !strings.Contains(path, "..") && !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	return paths
}

// RulingSnapshotOf is the item's snapshot where its newest note is the role's
// ruling on it, and false where it is not. cited is the digest of the documents
// the item cites (CitedDocuments), read by the caller. The snapshot's time is
// left for the pass to set.
func RulingSnapshotOf(item beads.WorkItem, role domain.AgentRole, cited string) (runstate.RulingSnapshot, bool) {
	if !rulingIsNewest(item, role) {
		return runstate.RulingSnapshot{}, false
	}
	sum := sha256.New()
	for _, part := range []string{
		item.Status, item.Title, item.Description, item.Design, item.AcceptanceCriteria,
		item.RecordedNotes(), strconv.Itoa(item.Priority), strings.Join(item.Labels, ","),
		item.UpdatedAt.UTC().Format(time.RFC3339Nano), cited,
	} {
		sum.Write([]byte(strconv.Itoa(len(part))))
		sum.Write([]byte{':'})
		sum.Write([]byte(part))
	}
	return runstate.RulingSnapshot{ID: item.ID, Digest: hex.EncodeToString(sum.Sum(nil))[:32]}, true
}

// rulingIsNewest reports whether the item's newest note, leaving out the
// harness's own notes about the role's passes, is the role's ruling.
func rulingIsNewest(item beads.WorkItem, role domain.AgentRole) bool {
	records := beads.NoteRecords(item.RecordedNotes())
	for i := len(records) - 1; i >= 0; i-- {
		text := strings.TrimSpace(records[i].Text)
		switch {
		case text == "":
			continue
		case !records[i].Framed:
			// Older notes whose boundaries were not recorded cannot say which
			// note is newest, so they are never read as a standing ruling.
			return false
		case passNoteOf(text, role):
			continue
		}
		return isRulingNote(text, role)
	}
	return false
}

// isRulingNote reports whether a note is the one a document of the role's
// leaves on an item it answers.
func isRulingNote(text string, role domain.AgentRole) bool {
	if !strings.HasPrefix(text, runstate.RulingNoteOpening) {
		return false
	}
	first, _, _ := strings.Cut(text, "\n")
	return strings.Contains(first, " by the "+role.Title()+" in conversation ")
}

// passNoteOf reports a note the harness writes about one of the role's passes,
// or the role's recorded agreement, which says what the role did rather than
// anything new about the item.
func passNoteOf(text string, role domain.AgentRole) bool {
	pass := " by the " + role.Title() + "'s recurring pass "
	for _, opening := range []string{"Passed over", "Decided", "Asked about"} {
		if strings.HasPrefix(text, opening+pass) {
			return true
		}
	}
	if role == domain.RoleArchitect {
		_, agreed := readAgreement(text)
		return agreed
	}
	return false
}

// standingRulings is the rulings that stand for this pass of the task: the
// snapshots its last completed pass took, less any item a report filed since
// names. Nothing stands where either could not be read, and the problem says
// why every item was handed as before.
func (t Trigger) standingRulings(name string) (StandingRulings, string) {
	if t.Reports == nil {
		return nil, ""
	}
	last, found, err := t.lastCompletedPass(name)
	if err != nil {
		return nil, fmt.Sprintf("the earlier passes could not be read, so no item was left out for its ruling standing: %v", err)
	}
	if !found || len(last.Rulings) == 0 {
		return nil, ""
	}
	var reports []report.Report
	if t.Pile != nil {
		if reports, err = t.Pile.List(); err != nil {
			return nil, fmt.Sprintf("the reports could not be read, so no item was left out for its ruling standing: %v", err)
		}
	}
	standing := StandingRulings{}
	for _, ruling := range last.Rulings {
		if !reportedSince(reports, ruling) {
			standing[ruling.ID] = ruling
		}
	}
	return standing, ""
}

// reportedSince reports whether a report naming the item was filed after its
// snapshot was taken.
func reportedSince(reports []report.Report, ruling runstate.RulingSnapshot) bool {
	for _, filed := range reports {
		if !filed.RecordedAt.After(ruling.At) {
			continue
		}
		if strings.TrimSpace(filed.WorkItemID) == ruling.ID || namesItem(filed.Message, ruling.ID) {
			return true
		}
	}
	return false
}

// boundedRulingIDs is the items left out, held to what the record keeps.
func boundedRulingIDs(ids []string) []string {
	var kept []string
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" && len(kept) < runstate.MaxSweepRulings {
			kept = append(kept, id)
		}
	}
	return kept
}

// stampedRulings is the snapshots a reading took, stamped with when the pass
// took them and held to what the record keeps.
func stampedRulings(rulings []runstate.RulingSnapshot, at time.Time) []runstate.RulingSnapshot {
	var kept []runstate.RulingSnapshot
	for _, ruling := range rulings {
		if strings.TrimSpace(ruling.ID) == "" || ruling.Digest == "" || len(kept) == runstate.MaxSweepRulings {
			continue
		}
		ruling.At = at
		kept = append(kept, ruling)
	}
	return kept
}

// standingRulingsSaid is what the pass's message says of the items it left out,
// and nothing where it left none.
func standingRulingsSaid(role domain.AgentRole, ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return fmt.Sprintf("Left out of this list because your ruling on each stands (%d): %s. Each one's newest note is your ruling, and nothing about it, the documents it cites, or the reports naming it has changed since your last pass, so there is nothing to rule on again; the harness keeps a document waiting for room and sends it then, so it does not need writing again. A new note, an edit, a report naming it, or a change to a document it cites puts it back in front of the %s.",
		len(ids), strings.Join(ids, ", "), role.Title())
}
