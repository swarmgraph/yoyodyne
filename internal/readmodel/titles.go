package readmodel

// Every work item a person reads, shown with its priority, labels, and title
// as the tracker holds them when the text is shown.
//
// The operator directed on 2026-09-30 that whenever a surface names a work
// item it carries the id, the title, the priority, and the labels. How a
// reference is kept, found, and shown is fixed by the "Work item references"
// section of docs/designs/observability-and-dashboard.md; this is the one
// renderer every surface takes its text from — the four lines and the
// needs-a-human line under them, the dashboard, a lane report and its card, a
// pass's account in `yoyo sweeps`, the report pile, and every message the
// channel posts — so no surface searches text of its own.
//
// Two forms, for two kinds of text:
//
//   - In text a role wrote, and in the harness's own sentences, each reference
//     is kept where it is and followed by the item's current state:
//     "ex-7 (P1, reliability: Retry the merge)". Every other byte is kept,
//     including a title the role wrote beside the id.
//   - Where the harness lists items itself, each is named in the operator's
//     form: "(P1, reliability) Retry the merge (ex-7)". That is Name.
//
// An id the tracker does not hold is shown as "ex-9 (not in the tracker)", and
// when the tracker could not be read every reference is shown as
// "ex-7 (tracker unreadable at 09:40 PDT)": a reference is never dropped and
// never fails the text it is in.
//
// References are found by internal/workref. Text recorded with its references
// is rendered from them (CiteRecorded), which is the only way a short id such
// as "432.28" is shown expanded; text recorded without them is read for full
// ids only (Cite), and a short id in it is left as written and counted
// (Unresolved), because whether it named exactly one item when it was written
// cannot be known now.

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/workref"
)

// maxCitedTitleBytes bounds the title put beside an id. Titles in this tracker
// run to a paragraph; the item's own card has whatever is cut.
const maxCitedTitleBytes = 200

// notInTracker is what an id the tracker holds nothing under is shown with.
const notInTracker = "not in the tracker"

// untitledWorkItem is what an item the tracker holds with no title is shown
// with, which is a different fact from the tracker not holding it.
const untitledWorkItem = "no title recorded"

// trackerUnreadable opens what every reference is shown with when the tracker
// could not be read; the time of the reading follows it.
const trackerUnreadable = "tracker unreadable at "

// citedItem is what the tracker holds of one item, as it is shown.
type citedItem struct {
	title    string
	priority int
	labels   []string
}

// WorkItemTitles is one reading of the tracker, as every surface renders the
// work items it names against it. Nil knows nothing and leaves text exactly as
// it was: it is what a surface with no tracker wired at all holds. A tracker
// that was wired and could not be read is UnreadableWorkItemTitles, which marks
// every reference rather than leaving it bare.
type WorkItemTitles struct {
	items map[string]citedItem
	index *workref.Index
	// unreadable is set where the tracker could not be read, at observedAt.
	unreadable bool
	observedAt time.Time
	// recorded is set where the only title known is the one a run recorded at
	// its claim, with no tracker reading behind it: the id is followed by that
	// title alone, since a priority nobody read is not shown.
	recorded bool
}

// NewWorkItemTitles indexes a listing of the tracker's items. The prefixes are
// the tracker's id prefixes beyond those its items carry, so an empty tracker
// still recognises its own ids.
func NewWorkItemTitles(items []beads.WorkItem, prefixes ...string) *WorkItemTitles {
	titles := &WorkItemTitles{items: make(map[string]citedItem, len(items))}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		labels := slices.Clone(item.Labels)
		slices.Sort(labels)
		titles.items[id] = citedItem{title: strings.TrimSpace(item.Title), priority: item.Priority, labels: labels}
		ids = append(ids, id)
	}
	titles.index = workref.NewIndex(ids, prefixes...)
	return titles
}

// UnreadableWorkItemTitles is a tracker that could not be read at the moment
// given. Every full id with one of the prefixes is shown marked as unreadable.
func UnreadableWorkItemTitles(at time.Time, prefixes ...string) *WorkItemTitles {
	return &WorkItemTitles{index: workref.NewPrefixIndex(prefixes...), unreadable: true, observedAt: at}
}

// RecordedWorkItemTitle is the title a run recorded for its item at its claim,
// as text that is written into a record — a note on the item — is titled where
// nothing read the tracker. It shows the id with that title and nothing else:
// a priority or labels written into a note would go stale, and none were read.
func RecordedWorkItemTitle(id, title string) *WorkItemTitles {
	return &WorkItemTitles{
		items:    map[string]citedItem{strings.TrimSpace(id): {title: strings.TrimSpace(title)}},
		index:    workref.NewIndex([]string{id}),
		recorded: true,
	}
}

// ReadWorkItemTitles lists every item the tracker holds, closed work included,
// for the titles: one tracker command however many references are rendered,
// bounded by the sources' tracker timeout. A listing that fails gives the
// unreadable reading and the error, so a caller that only renders can ignore
// the error and still show every reference marked.
func ReadWorkItemTitles(ctx context.Context, sources Sources) (*WorkItemTitles, error) {
	if sources.Tracker == nil {
		return nil, nil
	}
	items, err := sources.list(ctx, "")
	if err != nil {
		return UnreadableWorkItemTitles(sources.now(), sources.TrackerPrefixes...), err
	}
	return NewWorkItemTitles(items, sources.TrackerPrefixes...), nil
}

// Index is the matcher's knowledge of this reading, which is what text being
// recorded now is searched with for its references (workref.Index.Find).
func (w *WorkItemTitles) Index() *workref.Index {
	if w == nil {
		return nil
	}
	return w.index
}

// Readable reports whether this reading came from a listing the tracker gave.
func (w *WorkItemTitles) Readable() bool {
	return w != nil && !w.unreadable
}

// Title is what the tracker calls one item, and whether it holds the item.
func (w *WorkItemTitles) Title(id string) (string, bool) {
	if w == nil {
		return "", false
	}
	item, known := w.items[id]
	return item.title, known
}

// Name is one item as a list the harness builds shows it, in the operator's
// form: "(P1, reliability) Retry the merge (ex-7)", or "(P2) Name the stop
// (ex-7.2)" for an item with no labels. An item the tracker does not hold, or
// one it could not be read for, is its id with that said beside it.
func (w *WorkItemTitles) Name(id string) string {
	id = strings.TrimSpace(id)
	if w == nil || id == "" {
		return id
	}
	item, known := w.items[id]
	if w.unreadable || !known || w.recorded {
		return id + " (" + w.state(id) + ")"
	}
	return w.listHead(item) + " (" + id + ")"
}

// Names is each of the ids named as Name names it, keyed by id, for a surface
// that lists items from fields: nil where no tracker was wired, and with each
// id once however often it is given.
func (w *WorkItemTitles) Names(ids ...string) map[string]string {
	if w == nil {
		return nil
	}
	names := map[string]string{}
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			names[id] = w.Name(id)
		}
	}
	return names
}

// Cite is text recorded without its references — the harness's own sentences,
// and text a role wrote before references were kept — with every full id in it
// followed by the item's current state. A short id in it is left as written.
func (w *WorkItemTitles) Cite(text string) string {
	return w.CiteKnowing(text)
}

// CiteKnowing is Cite for text a surface built around work items it already
// knows are work items — a run's item, an attention entry's item — which are
// shown with their state whatever their shape, so a root id such as "ex-abc"
// is marked when the tracker could not be read rather than left bare.
func (w *WorkItemTitles) CiteKnowing(text string, known ...string) string {
	if w == nil || text == "" {
		return text
	}
	found, _ := w.index.Knowing(known...).FindFull(text)
	if len(found) == 0 {
		return text
	}
	// A name this reading rendered in the operator's form already says
	// everything, and its title is text read from the tracker, which is never
	// searched: every reference inside one is left as it is, the title's as well
	// as the id's at its end.
	var listed [][2]int
	for _, reference := range found {
		if start, ok := w.listedAt(text, reference); ok {
			listed = append(listed, [2]int{start, reference.End + 1})
		}
	}
	var cited strings.Builder
	last := 0
	for _, reference := range found {
		if reference.Start < last {
			// Inside what an earlier rendering put after an id: a title is never
			// searched for references.
			continue
		}
		id := reference.ID
		if within(listed, reference.Start) {
			continue
		}
		state := " (" + w.state(id) + ")"
		if strings.HasPrefix(text[reference.End:], state) {
			// Exactly what this reading would put there is there already: the
			// text passed through this renderer once on its way here. It is left
			// as it is, and nothing inside it — a title read from the tracker —
			// is searched. Anything else after an id is the writer's own words
			// and is kept as written, with the state put in before it.
			cited.WriteString(text[last : reference.End+len(state)])
			last = reference.End + len(state)
			continue
		}
		cited.WriteString(text[last:reference.End])
		cited.WriteString(state)
		last = reference.End
	}
	if last == 0 {
		return text
	}
	cited.WriteString(text[last:])
	return cited.String()
}

// CiteRecorded is text a role wrote, rendered from the references recorded with
// it: each reference's bytes are replaced with the full id it was resolved to
// when the text was recorded, and then shown as Cite shows it. Text with no
// references recorded is Cite's.
func (w *WorkItemTitles) CiteRecorded(text string, references []workref.Reference) string {
	return w.Cite(workref.Expand(text, references))
}

// References is every reference in text a role has just written, as it is
// recorded beside the text, with how many short ids it left as written. A
// reading that could not list the tracker resolves no short id.
func (w *WorkItemTitles) References(text string) ([]workref.Reference, int) {
	if w == nil {
		return nil, 0
	}
	if w.unreadable {
		return w.index.FindFull(text)
	}
	return w.index.Find(text)
}

// Unresolved is how many short ids in text recorded without its references are
// left as written because some item the tracker holds ends in them.
func (w *WorkItemTitles) Unresolved(text string) int {
	if w == nil {
		return 0
	}
	_, unresolved := w.index.FindFull(text)
	return unresolved
}

// UnresolvedSays is the line a surface prints under text it showed with short
// ids left as written, and empty where there were none. It covers both ways
// text comes to have no references: recorded before references were kept, or
// recorded while the tracker could not be listed.
func UnresolvedSays(unresolved int) string {
	if unresolved <= 0 {
		return ""
	}
	return count(unresolved, "short work item id") + " above " + plural(unresolved, "was", "were") +
		" recorded with no tracker listing to resolve " + plural(unresolved, "it", "them") +
		" against — before work item references were kept, or while the tracker could not be read — so " +
		plural(unresolved, "it is", "they are") + " shown as written rather than guessed at"
}

// plural is the singular form for one and the plural form otherwise.
func plural(number int, one, several string) string {
	if number == 1 {
		return one
	}
	return several
}

// state is what follows an id: its priority, labels, and title, or why there
// are none.
func (w *WorkItemTitles) state(id string) string {
	if w.unreadable {
		return trackerUnreadable + w.observedAt.Local().Format("15:04 MST")
	}
	item, known := w.items[id]
	if !known {
		return notInTracker
	}
	if w.recorded {
		return w.titleOf(item)
	}
	return w.priorityAndLabels(item) + ": " + w.titleOf(item)
}

// listHead is the part of Name before the id.
func (w *WorkItemTitles) listHead(item citedItem) string {
	return "(" + w.priorityAndLabels(item) + ") " + w.titleOf(item)
}

func (w *WorkItemTitles) priorityAndLabels(item citedItem) string {
	said := "P" + strconv.Itoa(item.priority)
	if len(item.labels) > 0 {
		said += ", " + strings.Join(item.labels, ", ")
	}
	return said
}

func (w *WorkItemTitles) titleOf(item citedItem) string {
	if item.title == "" {
		return untitledWorkItem
	}
	return singleLine(item.title, maxCitedTitleBytes)
}

// listedAt reports whether the reference is the id at the end of a Name this
// reading rendered, "(P1, reliability) Retry the merge (ex-7)", and where that
// name starts.
func (w *WorkItemTitles) listedAt(text string, reference workref.Reference) (int, bool) {
	if reference.Start < 1 || text[reference.Start-1] != '(' || reference.End >= len(text) || text[reference.End] != ')' {
		return 0, false
	}
	item, known := w.items[reference.ID]
	if !known || w.unreadable || w.recorded {
		return 0, false
	}
	head := w.listHead(item) + " "
	if !strings.HasSuffix(text[:reference.Start-1], head) {
		return 0, false
	}
	return reference.Start - 1 - len(head), true
}

// within reports whether offset falls inside one of the spans.
func within(spans [][2]int, offset int) bool {
	for _, span := range spans {
		if offset >= span[0] && offset < span[1] {
			return true
		}
	}
	return false
}
