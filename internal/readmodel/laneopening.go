package readmodel

// The three lists every program manager's lane report opens with, as the read
// model derives and carries them.
//
// `docs/designs/program-manager.md`, under "The lane report", is the rule.
// Completed and Handed off cover the last 24 hours and are derived here from
// the instance's recorded acts — the work items its passes admitted, the
// reports it filed and what became of them, the amendments it proposed and
// their decisions, the exchanges it asked and how they closed — and from the
// tracker; the instance writes neither. Blocked on a human is the one list the
// instance writes, each entry with a reason from the ownership registry's
// closed list and the waiting record the operator's act is recorded on. The
// lane report store carries an entry forward until that act is recorded; this
// shows each entry while its record says it is still waiting, and puts each on
// the attention line once, under the mover the registry names.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// LaneOpeningWindow is how far back Completed and Handed off reach: the
// operator's "recently", which he set as within 24 hours.
const LaneOpeningWindow = 24 * time.Hour

// LaneEntry is one line of Completed or Handed off: the work item it is about,
// where it is about one, or the record otherwise; what happened; when; and, for
// a handoff, the agent it went to.
type LaneEntry struct {
	// Item is the work item, and Title what the tracker calls it, filled in by
	// the reading so a person reads the item by what it is.
	Item  string `json:"item,omitempty"`
	Title string `json:"title,omitempty"`
	// Record is the request the entry is about — a report, an amendment, or an
	// exchange — where it is about one.
	Record string `json:"record,omitempty"`
	// What is what happened, in a few words: "closed", "admitted", "report
	// handled by the Lead Product Manager".
	What string    `json:"what"`
	At   time.Time `json:"at"`
	// To is the agent a handoff went to, by role: never a person.
	To string `json:"to,omitempty"`
}

// Says is the entry as one line a person reads: the item by what it is, then
// what happened and to whom.
func (e LaneEntry) Says() string {
	var said string
	switch {
	case e.Item != "" && e.Title != "":
		said = fmt.Sprintf("%q (%s)", e.Title, e.Item)
	case e.Item != "":
		said = e.Item
	default:
		said = e.Record
	}
	said += " — " + e.What
	if e.To != "" {
		said += ", to " + e.To
	}
	return said + ", " + localMoment(e.At)
}

// ProgramManagerHumanBlock is one Blocked on a human entry as the read model
// carries it: the entry the instance wrote, its item by what it is, and what the
// record it cites says.
type ProgramManagerHumanBlock struct {
	Item   string                   `json:"item,omitempty"`
	Title  string                   `json:"title,omitempty"`
	What   string                   `json:"what"`
	Reason ownership.OperatorReason `json:"reason"`
	// ReasonSays is the reason in the registry's words, so a surface says it as
	// the terminal does.
	ReasonSays string `json:"reason_says"`
	Cites      string `json:"cites"`
	// Record is which kind of record the citation resolved to, and empty where
	// none could be found; Unresolved then says why, and the entry is still
	// shown, because an entry is never dropped on a question nobody answered.
	Record     CitedRecord `json:"record,omitempty"`
	Unresolved string      `json:"unresolved,omitempty"`
	Since      time.Time   `json:"since"`
	// Carried says a later pass left the entry out and the harness carried it.
	Carried bool `json:"carried,omitempty"`
	// Mover is who moves it, as the ownership registry resolves its reason.
	Mover Mover `json:"mover"`
}

// Says is the entry as one line a person reads.
func (b ProgramManagerHumanBlock) Says() string {
	var said string
	switch {
	case b.Item != "" && b.Title != "":
		said = fmt.Sprintf("%q (%s): ", b.Title, b.Item)
	case b.Item != "":
		said = b.Item + ": "
	}
	said += singleLine(b.What, maxRefusalBytes)
	said += " — " + b.Reason.Says() + "; since " + localMoment(b.Since) + "; cleared when the act is recorded on " + b.Cites
	if b.Unresolved != "" {
		said += " (" + b.Unresolved + ")"
	}
	return said
}

// Key is what makes two entries one on the attention line: the item, or the
// record it cites where it names none.
func (b ProgramManagerHumanBlock) Key() string {
	return runstate.LaneReportHumanBlock{Item: b.Item, Cites: b.Cites}.Key()
}

// laneActs is every recorded act of every instance the opening lists are read
// from, read once for all of them.
type laneActs struct {
	items     map[string]beads.WorkItem
	itemsRead bool
	problems  []string
}

// waitingState is what the record one Blocked on a human entry cites says of
// the operator's act: a report is cleared once its latest handling no longer
// says it needs him, an amendment once it is decided, a directive once it is
// resolved, withdrawn, or resolved into what carries it.
func (c citable) waitingState(cites string, directives []directive.Directive, directivesRead bool) (runstate.WaitingRecordState, CitedRecord, error) {
	if _, found := c.reports[cites]; found {
		if handling, handled := c.handled[cites]; handled && !handling.NeedsOperator {
			return runstate.WaitingRecordCleared, CitedReport, nil
		}
		return runstate.WaitingRecordOpen, CitedReport, nil
	}
	if _, found := amendment.Find(c.amendments, cites); found {
		if _, decided := amendment.DecisionOn(c.amendments, cites); decided {
			return runstate.WaitingRecordCleared, CitedAmendment, nil
		}
		return runstate.WaitingRecordOpen, CitedAmendment, nil
	}
	for _, recorded := range directives {
		if recorded.ID != cites {
			continue
		}
		if recorded.Resolved() || recorded.Withdrawn() || recorded.ResolvedInto() {
			return runstate.WaitingRecordCleared, CitedDirective, nil
		}
		return runstate.WaitingRecordOpen, CitedDirective, nil
	}
	if len(c.unread) > 0 || !directivesRead {
		unread := append([]string{}, c.unread...)
		if !directivesRead {
			unread = append(unread, "directives")
		}
		return runstate.WaitingRecordUnknown, "", fmt.Errorf("no record that could be read holds %s; the %s could not be read", cites, strings.Join(unread, " and the "))
	}
	return runstate.WaitingRecordUnknown, "", nil
}

// WaitingRecordCheck is the lane report store's answer about the record a
// Blocked on a human entry cites, read from these sources each time it is
// asked, so a write and the carrying forward behind it read the records as
// they stand at the write.
func WaitingRecordCheck(sources Sources) runstate.LaneReportWaitingCheck {
	return func(cites string) (runstate.WaitingRecordState, error) {
		records := readCitable(sources)
		directives, read := readDirectives(sources)
		state, _, err := records.waitingState(cites, directives, read)
		return state, err
	}
}

func readDirectives(sources Sources) ([]directive.Directive, bool) {
	if sources.Directives == nil {
		return nil, false
	}
	recorded, err := sources.Directives.List()
	return recorded, err == nil
}

// humanBlocks is the instance's Blocked on a human entries whose records do not
// say they are cleared. An entry the store carried is shown exactly as one the
// latest pass wrote; one whose record is cleared since the last write is not.
func humanBlocks(report runstate.LaneReportContent, records citable, directives []directive.Directive, directivesRead bool) []ProgramManagerHumanBlock {
	blocks := []ProgramManagerHumanBlock{}
	for _, entry := range report.BlockedOnAHuman {
		state, record, err := records.waitingState(entry.Cites, directives, directivesRead)
		if state == runstate.WaitingRecordCleared {
			continue
		}
		block := ProgramManagerHumanBlock{
			Item: entry.Item, What: entry.What, Reason: entry.Reason, Cites: entry.Cites, Record: record,
			Since: entry.Since, Carried: entry.Carried, Mover: ownership.ResolveBlockedOnAHuman(entry.Reason), ReasonSays: entry.Reason.Says(),
		}
		switch {
		case err != nil:
			block.Unresolved = err.Error()
		case state == runstate.WaitingRecordUnknown:
			block.Unresolved = "no report, amendment, or directive on record is " + entry.Cites
		}
		blocks = append(blocks, block)
	}
	return blocks
}

// readLaneActs indexes the tracker's items for the opening lists.
func readLaneActs(items []beads.WorkItem, itemsErr error, tracked bool) laneActs {
	acts := laneActs{items: map[string]beads.WorkItem{}}
	switch {
	case !tracked:
		acts.problems = append(acts.problems, "nothing was wired to read the tracker, so no program manager's Completed list names the items that closed")
	case itemsErr != nil:
		acts.problems = append(acts.problems, fmt.Sprintf("the tracker could not be listed, so no program manager's Completed list names the items that closed: %v", itemsErr))
	default:
		acts.itemsRead = true
		for _, item := range items {
			acts.items[item.ID] = item
		}
	}
	return acts
}

// openingLists is one instance's Completed and Handed off, each newest first.
func openingLists(instance ProgramManagerInstance, acts laneActs, records citable, passes completedPasses, now time.Time) ([]LaneEntry, []LaneEntry) {
	since := now.Add(-LaneOpeningWindow)
	within := func(at time.Time) bool { return !at.IsZero() && !at.Before(since) && !at.After(now) }
	completed, handedOff := []LaneEntry{}, []LaneEntry{}

	admitted := map[string]bool{}
	for _, admission := range passes.admitted[instance.Agent] {
		admitted[admission.item] = true
		if within(admission.at) {
			to := "a developer"
			if item, known := acts.items[admission.item]; known && !item.Executor.DeveloperRun() && item.Executor.Role().Valid() {
				to = "the " + item.Executor.Role().Title()
			}
			handedOff = append(handedOff, LaneEntry{Item: admission.item, What: "admitted", At: admission.at, To: to})
		}
	}
	if acts.itemsRead {
		for _, item := range acts.items {
			if item.ClosedAt.IsZero() || !within(item.ClosedAt) {
				continue
			}
			if admitted[item.ID] || (instance.Lane != "" && hasLabel(item.Labels, instance.Lane)) {
				completed = append(completed, LaneEntry{Item: item.ID, What: "closed", At: item.ClosedAt})
			}
		}
	}

	for _, filed := range records.reports {
		if filed.Agent != instance.Agent || filed.Role != domain.RoleProgramManager {
			continue
		}
		if within(filed.RecordedAt) {
			to := domain.RoleProductManager
			if filed.For.Valid() {
				to = filed.For
			}
			handedOff = append(handedOff, LaneEntry{Item: filed.WorkItemID, Record: filed.ID, What: "report: " + singleLine(filed.ExpandedMessage(), 160), At: filed.RecordedAt, To: "the " + to.Title()})
		}
		if handling, handled := records.handled[filed.ID]; handled && within(handling.RecordedAt) {
			completed = append(completed, LaneEntry{Item: filed.WorkItemID, Record: filed.ID, What: "its report handled by the " + handling.Role.Title(), At: handling.RecordedAt})
		}
	}
	for _, record := range records.amendments {
		proposal := record.Proposal
		if proposal == nil || proposal.Agent != instance.Agent {
			continue
		}
		if within(proposal.RaisedAt) {
			handedOff = append(handedOff, LaneEntry{Item: proposal.WorkItemID, Record: proposal.ID, What: "a change proposed to " + proposal.Artifact, At: proposal.RaisedAt, To: "the " + proposal.Owner.Title()})
		}
		if decision, decided := amendment.DecisionOn(records.amendments, proposal.ID); decided && within(decision.DecidedAt) {
			completed = append(completed, LaneEntry{Item: proposal.WorkItemID, Record: proposal.ID, What: fmt.Sprintf("its proposed change to %s decided (%s)", proposal.Artifact, decision.Verdict), At: decision.DecidedAt})
		}
	}
	for _, asked := range records.exchanges {
		if asked.Asker.Agent != instance.Agent {
			continue
		}
		if within(asked.OpenedAt) {
			to := "the " + asked.Answerer.Role.Title()
			if asked.Answerer.Agent != "" && asked.Answerer.Agent != string(asked.Answerer.Role) {
				to += " " + asked.Answerer.Agent
			}
			handedOff = append(handedOff, LaneEntry{Record: asked.ID, What: "asked: " + singleLine(asked.Question, 160), At: asked.OpenedAt, To: to})
		}
		if asked.ClosedAt != nil && within(*asked.ClosedAt) {
			completed = append(completed, LaneEntry{Record: asked.ID, What: "its question answered (" + string(asked.Outcome) + ")", At: *asked.ClosedAt})
		}
	}
	newestFirst := func(entries []LaneEntry) {
		sort.SliceStable(entries, func(i, j int) bool {
			if !entries[i].At.Equal(entries[j].At) {
				return entries[i].At.After(entries[j].At)
			}
			return entries[i].Item+entries[i].Record < entries[j].Item+entries[j].Record
		})
	}
	newestFirst(completed)
	newestFirst(handedOff)
	return completed, handedOff
}

func hasLabel(labels []string, lane string) bool {
	for _, label := range labels {
		if label == lane {
			return true
		}
	}
	return false
}

// titleLaneEntries fills in what the tracker calls each item an entry names.
func titleLaneEntries(entries []LaneEntry, titles *WorkItemTitles) []LaneEntry {
	for index := range entries {
		if title, known := titles.Title(entries[index].Item); known && entries[index].Item != "" {
			entries[index].Title = title
		}
	}
	return entries
}

func titleHumanBlocks(blocks []ProgramManagerHumanBlock, titles *WorkItemTitles) []ProgramManagerHumanBlock {
	for index := range blocks {
		if title, known := titles.Title(blocks[index].Item); known && blocks[index].Item != "" {
			blocks[index].Title = title
		}
	}
	return blocks
}

// LaneHumanBlock is one Blocked on a human entry on the attention line: the
// entry, and the instance whose report carries it.
type LaneHumanBlock struct {
	Agent string                   `json:"agent"`
	Entry ProgramManagerHumanBlock `json:"entry"`
}

// Says is the entry as the attention line prints it.
func (b LaneHumanBlock) Says() string {
	about := b.Entry.Item
	if about == "" {
		about = "a question citing " + b.Entry.Cites
	}
	return fmt.Sprintf("%s is blocked on a human, says the program manager %s: %s (%s; since %s)",
		about, b.Agent, singleLine(b.Entry.What, maxRefusalBytes), b.Entry.Reason.Says(), b.Entry.Since.UTC().Format(time.RFC3339))
}

// laneHumanBlockAttention is every instance's Blocked on a human entries on the
// attention line, once per item however many instances or passes name it.
func laneHumanBlockAttention(instances []ProgramManager) []Attention {
	seen := map[string]bool{}
	var entries []Attention
	for _, instance := range instances {
		for _, block := range instance.BlockedOnAHuman {
			if seen[block.Key()] {
				continue
			}
			seen[block.Key()] = true
			id := block.Item
			if id == "" {
				id = block.Cites
			}
			entries = append(entries, resolved(Attention{
				Kind: AttentionLaneHumanBlock, ID: id, WorkItemID: block.Item,
				LaneHumanBlock: &LaneHumanBlock{Agent: instance.Agent, Entry: block},
			}))
		}
	}
	return entries
}
