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
// (runstate.RulingNoteOpening). A ruling stands only on an item whose newest
// note is the architect's ruling, leaving out of "newest" only the harness's own notes about her passes and her
// recorded agreements, which say what she did rather than anything new. A
// snapshot of such an item is a digest of the item's text, notes, status, priority, labels,
// when it last changed, and the documents it cites by file.
//
// Once a pass's turns are over, the harness records the snapshots the next pass
// is compared with (closingRulings). It never records a change nobody dealt
// with: an item the pass was not handed keeps the snapshot it had; an item it
// was handed and left gets none, so it is handed to every pass until one deals
// with it; and an item it took gets one only where nothing arrived while the
// pass ran that the role's own turns did not produce.
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
	"context"
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
	"github.com/mason-bryant/yoyodyne/internal/sweep"
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

// WaitingState is one waiting item as a reading of the waiting work found it:
// its snapshot where its newest note is the role's ruling, and the parts a later
// reading in the same pass is compared with.
type WaitingState struct {
	Snapshot runstate.RulingSnapshot
	Ruled    bool

	text  string
	notes []string
	cited map[string]string
}

// WaitingStateOf is the item's state. cited is each document the item cites
// (CitedDocuments) with what it holds now, read by the caller.
func WaitingStateOf(item beads.WorkItem, role domain.AgentRole, cited map[string]string) WaitingState {
	lines := make([]string, 0, len(cited))
	for path, holds := range cited {
		lines = append(lines, path+"|"+holds)
	}
	slices.Sort(lines)
	state := WaitingState{
		text: DigestLines([]string{
			item.Status, item.Title, item.Description, item.Design, item.AcceptanceCriteria,
			strconv.Itoa(item.Priority), strings.Join(item.Labels, ","),
		}),
		cited: cited,
	}
	for _, record := range beads.NoteRecords(item.RecordedNotes()) {
		if text := strings.TrimSpace(record.Text); text != "" {
			state.notes = append(state.notes, text)
		}
	}
	state.Snapshot, state.Ruled = RulingSnapshotOf(item, role, DigestLines(lines))
	state.Snapshot.ID = item.ID
	return state
}

// ruledSince reports whether the role ruled on the item after it was handed,
// and changedSince whether anything about it changed that the role's own notes
// do not account for. A document only the new notes cite is not compared: there
// is nothing earlier to compare it with.
func (now WaitingState) ruledSince(handed WaitingState, role domain.AgentRole) bool {
	return len(now.notes) > len(handed.notes) && slices.ContainsFunc(now.notes[len(handed.notes):], func(text string) bool {
		return isRulingNote(text, role)
	})
}

func (now WaitingState) changedSince(handed WaitingState, role domain.AgentRole) bool {
	if now.text != handed.text || len(now.notes) < len(handed.notes) || !slices.Equal(now.notes[:len(handed.notes)], handed.notes) {
		return true
	}
	for path, held := range handed.cited {
		if now.cited[path] != held {
			return true
		}
	}
	return slices.ContainsFunc(now.notes[len(handed.notes):], func(text string) bool {
		return !passNoteOf(text, role) && !ownNote(text, role)
	})
}

// ownNote reports a note the role's own conversation wrote.
func ownNote(text string, role domain.AgentRole) bool {
	first, _, _ := strings.Cut(text, "\n")
	return strings.Contains(first, " by the "+role.Title()+" in conversation ")
}

// rulingWatch is what one pass keeps of the waiting work it was shown, so its
// closing snapshots absorb only what the role dealt with.
type rulingWatch struct {
	standing StandingRulings
	handed   map[string]handedItem
}

type handedItem struct {
	state WaitingState
	at    time.Time
}

// note keeps each item the reading handed over, as first handed and when. at is
// from before the reading, so nothing that arrived during it is missed.
func (w *rulingWatch) note(work ConversationWorkReading, at time.Time) {
	if w.handed == nil {
		w.handed = map[string]handedItem{}
	}
	states := make(map[string]WaitingState, len(work.States))
	for _, state := range work.States {
		states[state.Snapshot.ID] = state
	}
	for _, item := range work.Items {
		if _, seen := w.handed[item.ID]; seen {
			continue
		}
		if state, read := states[item.ID]; read {
			w.handed[item.ID] = handedItem{state: state, at: at}
		}
	}
}

// closing is the snapshots the pass leaves the next one (see the file's opening
// comment). took says whether the pass dealt with an item by something other
// than a new ruling. at is when the reports were read; reportsRead is false
// where they could not be, and then a snapshot is dated when its item was
// handed, so a report filed since still puts the item back.
func (w rulingWatch) closing(work ConversationWorkReading, role domain.AgentRole, took func(id string) bool, reports []report.Report, reportsRead bool, at time.Time) []runstate.RulingSnapshot {
	var kept []runstate.RulingSnapshot
	for _, now := range work.States {
		id := now.Snapshot.ID
		snapshot, stood := w.standing[id]
		if handed, shown := w.handed[id]; shown {
			if !now.Ruled || now.changedSince(handed.state, role) {
				continue
			}
			if !now.ruledSince(handed.state, role) && !took(id) {
				continue
			}
			snapshot = now.Snapshot
			snapshot.At = handed.at
			if reportsRead {
				// A report the role itself filed in the pass is not news to it.
				others := slices.DeleteFunc(slices.Clone(reports), func(filed report.Report) bool { return filed.Role == role })
				if reportedSince(others, snapshot) {
					continue
				}
				snapshot.At = at
			}
		} else if !stood {
			continue
		}
		if len(kept) < runstate.MaxSweepRulings {
			kept = append(kept, snapshot)
		}
	}
	return kept
}

// tookRuled reports whether the pass dealt with an item it was handed, judged
// as judgeDelivered judges it except that a document already held for sending
// counts only where the role's account says the item is held.
func tookRuled(id string, delivered []runstate.DeliveredWork, actedOn []string, account *sweep.Result) bool {
	if slices.ContainsFunc(actedOn, func(acted string) bool { return strings.TrimSpace(acted) == id }) {
		return true
	}
	for _, item := range delivered {
		if item.ID == id && item.Taken {
			return item.HeldForSending == "" || item.Settled != "" || (account != nil && account.HeldItem(id))
		}
	}
	return false
}

// closingRulings reads the waiting work once the pass's own notes are written,
// and is the snapshots the pass records with any problem doing so.
func (t Trigger) closingRulings(ctx context.Context, role domain.AgentRole, watch rulingWatch, took func(id string) bool) ([]runstate.RulingSnapshot, string) {
	at := t.now()
	work, err := t.ConversationWork.Read(ctx, role, nil)
	if err != nil {
		return nil, fmt.Sprintf("the work waiting in the %s's conversation could not be read again once the pass was over, so the next pass is handed every item, its rulings included: %v", role.Title(), err)
	}
	var reports []report.Report
	var problem string
	if t.Pile != nil {
		if reports, err = t.Pile.List(); err != nil {
			problem = fmt.Sprintf("the reports could not be read once the pass was over, so a report filed while it ran is judged at the next pass: %v", err)
		}
	}
	return watch.closing(work, role, took, reports, err == nil, at), problem
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

// standingRulingsSaid is what the pass's message says of the items it left out,
// and nothing where it left none.
func standingRulingsSaid(role domain.AgentRole, ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return fmt.Sprintf("Left out of this list because your ruling on each stands (%d): %s. Each one's newest note is your ruling, and nothing about it, the documents it cites, or the reports naming it has changed since you dealt with it, so there is nothing to rule on again; the harness keeps a document waiting for room and sends it then, so it does not need writing again. A new note, an edit, a report naming it, or a change to a document it cites puts it back in front of the %s.",
		len(ids), strings.Join(ids, ", "), role.Title())
}
