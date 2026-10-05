// Package backlog is the ordered work the product manager has admitted, and
// the one thing a development manager pulls from.
//
// The ordering is a product decision rather than a convenience: what is worth
// doing, and in what order, is the expression of product intent the product
// manager already owns. Decomposition, dependency structure, and assignment are
// not here, because those stay the development manager's. A role that disagrees
// with the order proposes a change to it, exactly as a downstream role proposes
// a change to a goal, and this package is deliberately read-only so that nothing
// which merely reads the backlog can quietly rewrite it.
//
// The order itself lives in Beads, as the priority the tracker already holds.
// Nothing here stores a second copy of it: a queue assembled from the tracker
// and a queue the tracker reports cannot then disagree.
package backlog

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/humangate"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// The tracker statuses admitted work can be in. Open work is waiting to be
// pulled; blocked work has been admitted and is not finished, so it is still in
// the order. Everything else has left the backlog: claimed work has been pulled
// already, and closed work is over, whether it was finished or retired.
//
// Neither of them decides whether an item can be pulled, and the blocked one
// deliberately does not. A status is a field somebody sets and nothing
// maintains: it is written when the work stops and never rewritten when what
// stopped it clears, so an item whose every blocker closed months ago still
// reads as blocked forever. On 2026-09-04 that was two-thirds of this backlog,
// two p0 items among it, and the line sat idle for a morning with nothing
// reporting why. What decides here is the dependencies themselves and the holds
// below, which are records of the thing rather than a description of it.
const (
	statusOpen    = "open"
	statusBlocked = "blocked"
)

// AdmittedStatuses is the tracker slices the backlog is assembled from, in the
// order they are read. It is exported because the read model assembles the queue
// from the tracker and every operator surface projects that reading, and a reader
// naming a wider or narrower set than this one would be a surface answering the
// same question differently. Which is the disagreement only the operator could
// adjudicate, and the one thing a single derivation is for.
func AdmittedStatuses() []string { return []string{statusOpen, statusBlocked} }

// maxRenderedEntries bounds how many entries a rendered backlog lists. What an
// operator needs from it is what happens next, not an export of the tracker, so
// the list is cut while the counts stay exact.
const maxRenderedEntries = 20

// maxRenderedTitleBytes keeps one tracker-supplied title to one line.
const maxRenderedTitleBytes = 120

// Hold is what somebody has to do before one admitted item is pulled: what is
// holding it in the harness's own words, and whether what it waits for is a
// decision or the carrying out of one already made.
//
// The two are kept apart because they have different next movers and only one of
// them is anybody's to decide. On 2026-09-07 thirty-three items read as a
// backlog of undecided stoppages for days while the development manager had
// decided every one of them and what was missing was the harness acting on them.
// One word covering both is what sent the operator's attention to the role that
// had already done its job.
type Hold struct {
	// Reason is what is holding the item, in the harness's own words.
	Reason string
	// Decided reports the harness rather than the development manager being the
	// next mover on the stoppage that holds this item: a decision already
	// recorded and not yet carried out, or an approved change the environment
	// stopped short of its promotion, which nobody has to decide anything about
	// and the harness resumes. It is false where nobody has decided anything, and
	// false where what was decided could not be read — the reason says which,
	// because a record nobody could open is not a decision nobody made.
	Decided bool
	// Since is when the item came to be held, read from the record that holds
	// it: the run's stop, the stoppage's docketing, or the decision that stopped
	// it. It is zero where that record names no moment. It is carried so a
	// surface can say how long a hold has stood, which is what tells a stoppage
	// from yesterday apart from one three weeks old.
	Since time.Time
	// RunID is the run the hold is about: the stopped run, the one whose
	// publication is unfinished, or the one whose stoppage was escalated. It is
	// empty where the record behind the hold names none. It is carried apart from
	// the reason, which names it too, because the reason is prose a reader may
	// cut, and a decision about the stoppage has to name the run.
	RunID string
}

// Holds is the admitted work somebody has to release before anything pulls it,
// keyed by item, with what each one is waiting for. It is what separates a
// governance hold — a stoppage whose change is still preserved, an escalation
// nobody has answered — from a dependency block, which clears on its own as the
// work it names lands.
//
// It is a type rather than a bare map so that "nothing is held" and "what is
// held could not be read" are different values rather than one empty map. The
// zero value is the second, and it holds back every item whose status is
// blocked: a reader that cannot tell the two apart must not release either,
// because releasing a stoppage starts a fresh run over a change a branch is
// still holding, while holding a releasable item costs one pull.
type Holds struct {
	held map[string]Hold
	read bool
	// unlanded is the work whose own change the harness recorded and never saw
	// reach the integration target, keyed by item, with where that change is. It
	// is not a hold on those items — their own stoppages say what holds them — but
	// the substrate a child carved out of one of them may say it builds on; see
	// OnUnlandedParents.
	unlanded map[string]string
}

// ReadHolds is the holds as a reader actually found them, which may be none at
// all. It is the constructor rather than a literal because an empty Holds and an
// unread one mean opposite things, and only a reader that got an answer may say
// the first.
func ReadHolds(held map[string]Hold) Holds {
	return Holds{held: held, read: true}
}

// unreadHold is what an entry says when nothing could read the holds. It names
// the gap rather than the item, because there is nothing wrong with the item:
// what is missing is the reading that would say whether anybody still has to
// release it. It is deliberately not one of the reasons carried on the entry,
// which are facts about an item somebody goes and acts on; this is a fact about
// the reading, and reporting it against each item would send somebody after a
// decision nobody owes.
const unreadHold = "blocked, and what is holding it could not be read, so nothing releases it here"

// Reason is what somebody has to release before one named item is pulled, and
// whether anything is holding it at all. It answers for what was read and no
// more: an unread Holds holds every blocked item, and that refusal belongs to
// the entry rather than here, because it is a fact about the reading rather than
// about the item.
func (h Holds) Reason(workItemID string) (string, bool) {
	reason := strings.TrimSpace(h.held[workItemID].Reason)
	return reason, reason != ""
}

// Decided reports one named item held for the carrying out of a decision
// already recorded rather than for a decision. It answers false for an item
// nothing is holding and for one whose decisions could not be read, which
// Reason above says in words: what it must never do is name the harness as the
// next mover on the strength of a record nobody opened.
func (h Holds) Decided(workItemID string) bool {
	held := h.held[workItemID]
	return held.Decided && strings.TrimSpace(held.Reason) != ""
}

// RunID is the run one named item's hold is about, and empty where nothing
// holds the item or the record holding it names no run.
func (h Holds) RunID(workItemID string) string {
	held := h.held[workItemID]
	if strings.TrimSpace(held.Reason) == "" {
		return ""
	}
	return strings.TrimSpace(held.RunID)
}

// Read reports holds a reader actually got an answer about. It is exported
// because "nothing is held" and "nothing could say what is held" are opposite
// answers to anything deciding whether an item may be touched, and only the
// reader that got an answer may give the first: an unread Holds says nothing
// about any item, which is exactly why the queue holds every blocked one.
func (h Holds) Read() bool { return h.read }

// OnUnlandedParents is these holds with the work whose change never landed,
// keyed by item and saying where each change is. It is what lets the queue hold
// a child that says in its own text that it builds on its parent's change, for
// as long as that change has not landed.
//
// That is the only way a re-scope's child is held for its parent's change. The
// tracker refuses a dependency from a child onto its own parent, because the
// child already hangs on it, and the fallback that blocked the child instead set
// a status nothing ever cleared: on 2026-09-25 all six children of
// yoyodyne-ifd.429.13 were blocked that way, and they superseded the parent's
// pull request rather than building on its files, so none of them should have
// waited at all. So the child says which it is, in its own words, and this
// reads that; a child that says nothing is not held.
func (h Holds) OnUnlandedParents(unlanded map[string]string) Holds {
	h.unlanded = unlanded
	return h
}

// hold is what somebody has to do before this item is pulled, with an empty
// reason for an item nothing is holding.
func (h Holds) hold(item beads.WorkItem) Hold {
	held := h.held[item.ID]
	held.Reason = strings.TrimSpace(held.Reason)
	return held
}

// substrate is the hold on a child that builds on its parent's change while that
// change has not landed, and whether there is one. It clears by itself, however
// the change lands: a later run of the parent promoting it, which the harness's
// own records then say, or the parent leaving the backlog closed — the preserved
// branch cherry-picked, the pull request revived and merged, or the substrate
// rebuilt and the parent closed on it.
//
// The parent is read as still standing where it is in the backlog, or where the
// child's own edge to it carries a status other than closed. A parent that has
// left the backlog with nothing saying it is unfinished is read as closed: a
// hold that outlived its parent's closing would be the blocker nobody clears
// that this replaced.
func (h Holds) substrate(item beads.WorkItem, unfinished map[string]struct{}) (string, bool) {
	parent := item.DecomposedFrom()
	if parent == "" {
		return "", false
	}
	where, unlanded := h.unlanded[parent]
	if !unlanded || !BuildsOnParent(item, parent) || !standing(item, parent, unfinished) {
		return "", false
	}
	return fmt.Sprintf("it says it builds on %s's change, and %s; it is pulled once that change lands, however it lands, or once %s closes",
		parent, strings.TrimSpace(where), parent), true
}

// standing reports the parent an item was carved out of as unfinished.
func standing(item beads.WorkItem, parent string, unfinished map[string]struct{}) bool {
	if _, queued := unfinished[parent]; queued {
		return true
	}
	for _, dependency := range item.Dependencies {
		if strings.TrimSpace(dependency.ID) != parent {
			continue
		}
		if status := strings.TrimSpace(dependency.Status); status != "" {
			return status != "closed"
		}
	}
	return false
}

// buildsOn is the one phrasing in which a child says it builds on its parent's
// change: "builds on the parent's change", or the parent named — "builds on
// yoyodyne-ifd.100's files". It is closed and narrow on purpose. "builds on the"
// alone fires on any item describing what it builds on, which is why the
// readiness reading dropped it; this one only matters on a child whose parent's
// change never landed, and there it has to be something the development manager
// wrote deliberately, because it is the whole of what holds the child.
var buildsOn = regexp.MustCompile(`(?i)\bbuilds? on (the parent|[a-z0-9][a-z0-9._-]*)['’]s (?:change|files|branch)\b`)

// BuildsOnParent reports an item saying in its own words — title, description,
// design guidance, or acceptance criteria, never the notes the harness writes
// into — that it builds on the named parent's change. It is exported because a
// creation says whether the child it made will be held, and that answer has to
// be this one.
func BuildsOnParent(item beads.WorkItem, parent string) bool {
	parent = strings.TrimSpace(parent)
	for _, text := range []string{item.Title, item.Description, item.Design, item.AcceptanceCriteria} {
		for _, match := range buildsOn.FindAllStringSubmatch(text, -1) {
			named := match[1]
			if strings.EqualFold(named, "the parent") || (parent != "" && strings.EqualFold(named, parent)) {
				return true
			}
		}
	}
	return false
}

// Entry is one admitted work item at the position the product manager's
// ordering puts it in.
type Entry struct {
	SchedulingWait *beads.SchedulingWait `json:"scheduling_wait,omitempty"`
	// Position is where this item sits in the order, counting from one. It is
	// the backlog's own numbering rather than anything the tracker stores, so it
	// describes this reading of the queue and no more.
	Position int    `json:"position"`
	ID       string `json:"id"`
	Title    string `json:"title"`
	Priority int    `json:"priority"`
	Status   string `json:"status"`
	// Executor is what carries this item's execution where that is not a
	// developer run, and is empty for the ordinary work that is. It is on the
	// entry rather than left in the tracker because the queue is where anybody
	// decides what to pull: an item nobody can run has to say so where the order
	// is read, not only where the item is opened.
	Executor domain.WorkItemExecutor `json:"executor,omitempty"`
	// Parking is why this item is deliberately not to be pulled, and is empty for
	// the ordinary work that is. It is on the entry for the reason the executor
	// is, and against a failure that has already happened once: a parking that is
	// not in the queue is a parking the queue's readers do not share, and what
	// they do instead is pull the work.
	Parking domain.WorkItemParking `json:"parking,omitempty"`
	// Landing is the revision the harness already closed this item on, where it
	// did and somebody has since reopened it, and is empty for everything else.
	// It is on the entry because the sweep that closes conversation-carried work
	// reads the queue, and an entry that did not carry it would be closed again
	// on the same revision at the next pull.
	Landing string `json:"landing,omitempty"`
	// HumanGates is the steps this item declares that only a person can take and
	// that nobody has recorded taking yet. It is on the entry for the reason the
	// parking is: a gate that is not in the queue is a gate the queue's readers do
	// not share, and what they do instead is pull the work past it.
	//
	// It is the undischarged ones rather than all of them, because what the queue
	// is answering is what is holding this item back now. A gate somebody has
	// already passed holds nothing, and listing it would read as an outstanding
	// step that is in fact done.
	//
	// It carries the declarations nothing could read as well as the gates, and
	// holds the item for either. An author who mistyped the name of the step they
	// were reserving must not thereby reserve nothing: a typo that let the work
	// through would be this machinery's own version of the failure it replaced.
	HumanGates humangate.Reading `json:"human_gates,omitzero"`
	// Ready reports that nothing is holding this item back, which is what
	// separates the next item to pull from the next item in the order.
	Ready bool `json:"ready"`
	// Awaiting is what somebody has to release before this item is pulled, and is
	// empty for the ordinary work nobody is holding. It is on the entry for the
	// reason the parking is: a hold that is not in the queue is a hold the
	// queue's readers do not share, and a hold nobody can read off the item is
	// exactly the state a blocked status left this backlog in.
	Awaiting string `json:"awaiting,omitempty"`
	// AwaitingCarryOut reports a held item whose decision is already recorded and
	// has not been carried out, so what it waits for is the harness acting rather
	// than a person deciding. It is meaningless on an entry nothing is holding,
	// and it is a fact about the wait rather than a second reason: the two waits
	// are one queue entry apiece and different people to go to, which is the
	// distinction a single "held for a person" hid for days.
	AwaitingCarryOut bool `json:"awaiting_carry_out,omitempty"`
	// AwaitingSince is when the hold Awaiting names began, from the record that
	// holds the item, and is nil where nothing is holding it or that record names
	// no moment. Like AwaitingCarryOut it is a fact about the wait.
	AwaitingSince *time.Time `json:"awaiting_since,omitempty"`
	// AwaitingLanding reports a held item whose hold is its parent's change
	// landing rather than anybody's decision: a child that says it builds on a
	// change the harness recorded and never saw reach the target branch. It is a
	// wait, like WaitingOn, and clears by itself; the reason says what on.
	AwaitingLanding bool `json:"awaiting_landing,omitempty"`
	// WaitingOn names the unfinished work this item waits for. It explains an
	// unready open entry and decides a blocked one, which is not two behaviours
	// but one rule applied where each answer comes from: an open item's readiness
	// is the tracker's own, so this only annotates it, and a blocked item is
	// missing from that answer by construction — the tracker computes it from the
	// same status — so nothing named here is what makes it startable.
	//
	// What that asks of a listing is asked in both directions, because neither
	// half of one is reliable alone. A listing may record only that a dependency
	// exists, reading the same after the blocker closed as before, so a dependency
	// is named when the depended-on item is itself still in the backlog. A listing
	// that does carry a status is believed on it, so work the tracker reports as
	// unfinished is named whether or not it is still queued — which is the same
	// reading the harness's own run-start gate makes, so an entry this calls
	// startable is not one the pipeline then refuses at the door. Order says why
	// deciding a blocked entry from this beats deciding it from a status field
	// nothing maintains.
	WaitingOn []string `json:"waiting_on,omitempty"`
}

// Queue is the backlog in the product manager's order.
type Queue struct {
	Entries []Entry `json:"entries,omitempty"`
}

// Order arranges the work items a tracker holds into the backlog. It keeps the
// admitted, unfinished ones — an item somebody is already working on has been
// pulled, and a closed one has left — and puts them in priority order, highest
// priority first.
//
// Items at the same priority are taken oldest-admitted first, and that is
// deliberately not presented as a decision: the product manager says which of
// two items comes first by giving one a higher priority, and until it does, the
// only thing the tie-break has to be is fair — an item already waiting is not
// passed over for one admitted after it. See Sort.
//
// ready names the items the tracker itself reports as pullable, and an open item
// missing from it is unready however clean its listing looks. For open work
// readiness is taken rather than inferred, because neither thing a listing
// offers can decide it. A listing that carries no dependencies at all is
// indistinguishable from work with none, so inferring "nothing listed, therefore
// nothing is in the way" would name a blocked item as the next thing to pull.
// And the dependencies a listing does carry say what an item depends on without
// saying whether that work is done, so inferring the opposite from their
// presence would hold an item back for a blocker that finished long ago. The
// tracker answers this from its own dependency graph; this only asks.
//
// Blocked work is the one thing the tracker cannot be asked, because it answers
// by the same status field nobody maintains: an item whose blockers all closed
// is still missing from that list, forever. So its readiness is computed here
// from what the item actually waits on — the blocking dependencies its listing
// records, judged the way every dependency gate in the harness judges them —
// with held naming what somebody still has to release. That is the whole of the
// difference: a dependency block clears itself as the work it names lands, and a
// governance hold is somebody's to lift and is never lifted here.
//
// What the tracker is not asked is what could carry the work, or whether the
// work is to be started at all. An item whose executor is a persona conversation
// is admitted work in the order like any other and is never the next thing to
// pull, because there is no run that could take it. An item somebody parked is
// admitted work in the order too, and is never the next thing to pull because
// somebody decided it is not to be started yet. The tracker answers neither: it
// knows about dependencies, and not about the harness's roles or the product
// manager's deferrals, so it reports both as ready forever.
//
// discharged is the human gates a person has recorded taking, by the work item
// each act was recorded against, from the harness's own store. It is keyed by
// item rather than a flat set of names because a name is a word somebody chose
// and the useful words recur — `release-signed` describes a step taken once per
// release — so a flat set would have one recorded act pass every later
// declaration of that word, which is this whole mechanism's own failure arriving
// through the namespace. It is asked for rather than read here for the reason
// readiness is taken rather than inferred: a gate is passed by a durable record
// of somebody's act, and a package that both read an item's declared gates and
// decided they were satisfied would be holding both halves of the thing this
// separates. A caller that knows of no discharged gates passes none, and every
// declared gate then holds — which is the direction this fails in on purpose.
// The tracker is not asked about gates at all and could not answer: the only
// completion it knows is an item being closed, and an item's closure passing a
// gate that reserved a person's step is the exact failure that put this here.
func Order(items []beads.WorkItem, ready []string, held Holds, discharged map[string][]string) Queue {
	pullable := make(map[string]struct{}, len(ready))
	for _, id := range ready {
		pullable[id] = struct{}{}
	}
	admitted := make([]beads.WorkItem, 0, len(items))
	for _, item := range items {
		if item.Status == statusOpen || item.Status == statusBlocked {
			admitted = append(admitted, item)
		}
	}
	Sort(admitted)
	// Unfinished work is what is still in the backlog, which is what makes a
	// dependency worth naming: an item this one depends on that is no longer
	// queued has been done or pulled, and is not what anybody is waiting for.
	unfinished := make(map[string]struct{}, len(admitted))
	for _, item := range admitted {
		unfinished[item.ID] = struct{}{}
	}

	queue := Queue{Entries: make([]Entry, 0, len(admitted))}
	for position, item := range admitted {
		_, reportedReady := pullable[item.ID]
		holding := held.hold(item)
		awaiting := holding.Reason
		// A child building on its parent's unlanded change is held only where
		// nothing else is holding it: a stoppage of its own is somebody's to
		// release, and is the thing that would still refuse it once the parent's
		// change landed.
		landing := false
		if awaiting == "" {
			awaiting, landing = held.substrate(item, unfinished)
		}
		waiting := waitingOn(item, unfinished)
		var since *time.Time
		if awaiting != "" && !landing && !holding.Since.IsZero() {
			began := holding.Since
			since = &began
		}
		gates := humangate.Of(item).Pending(discharged[item.ID])
		queue.Entries = append(queue.Entries, Entry{
			SchedulingWait: item.SchedulingWait,
			Position:       position + 1,
			ID:             item.ID,
			Title:          item.Title,
			Priority:       item.Priority,
			Status:         item.Status,
			Executor:       item.Executor,
			Parking:        item.Parking,
			Landing:        item.Landing,
			Awaiting:       awaiting,
			// Only ever true beside a reason: a decision recorded about an item
			// nothing is holding says nothing about why it is not being pulled, and
			// carrying it here would put an item on the held count that no hold is on.
			AwaitingCarryOut: awaiting != "" && holding.Decided && !landing,
			AwaitingLanding:  landing,
			AwaitingSince:    since,
			// An item whose execution is not a developer run is never the next thing
			// to pull, however clear the dependency answer about it is, and neither is
			// one somebody parked or one somebody is holding. All three are a
			// different axis from the dependency reading rather than a second opinion
			// on it: what an item waits for says nothing about what could carry the
			// work, about whether the work is wanted now, or about whether somebody
			// has yet to decide what happens to it. Deciding all of them here is what
			// keeps the answer the same everywhere the order is read, instead of one
			// for each thing that reads it — which is exactly what parking-by-priority
			// was, and what it cost.
			//
			// An undischarged human gate is a fourth such axis, and the only one
			// nothing about the tracker's state can ever clear: it is passed by a
			// person's recorded act and by nothing else, so an item carrying one is
			// not pullable however ready the tracker calls it and however many of the
			// items it depends on have been closed.
			Ready: startable(item, reportedReady, waiting, held) && awaiting == "" &&
				item.Executor.DeveloperRun() && !item.Parking.Parked() && !gates.Holds(),
			HumanGates: gates,
			WaitingOn:  waiting,
		})
	}
	return queue
}

// startable reports nothing unfinished standing between this item and a run.
//
// The two statuses are asked differently because only one of them has an
// authority to defer to. Open work is what the tracker's own ready list
// describes, so that answer is taken. Blocked work is missing from that list by
// construction — the list is computed from the same status — so what is left is
// the item's own account of what it waits for, and an item waiting on nothing
// unfinished is startable whatever its status says.
//
// Blocked work is startable only where the holds were actually read, and that is
// the one place the reading itself decides something. Without it a dependency
// block and a stoppage somebody has to release are the same item to this, and
// releasing the second starts a run over a change that is still there.
func startable(item beads.WorkItem, reportedReady bool, waiting []string, held Holds) bool {
	if item.Status == statusBlocked {
		return held.read && len(waiting) == 0
	}
	return reportedReady
}

// Sort puts work items in the backlog's order in place, highest priority first,
// and items of equal priority oldest-admitted first. It is exported because more
// than one place shows the same work — the queue an operator reads and the
// tracker state the product manager reasons over — and a listing that disagreed
// with the order would be a listing that misinforms whoever is setting it.
//
// The tie-break is the harness's and not a decision about the work, but it has
// to be one that cannot starve an item. It used to be the order the tracker
// listed them in, which is newest first, so every item admitted at a priority
// went ahead of every item already waiting at it. On 2026-09-29 and 30 the
// red-check misfiling fix (yoyodyne-c02) sat at priority 0 through two pulls
// that each took a priority-0 item admitted hours after it, and would have gone
// on sitting there for as long as priority-0 work kept arriving. An item whose
// admission time the tracker did not give goes after the ones it did, in the
// order it arrived, rather than being read as the oldest of all.
func Sort(items []beads.WorkItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Priority != items[j].Priority {
			return items[i].Priority < items[j].Priority
		}
		left, right := items[i].CreatedAt, items[j].CreatedAt
		switch {
		case left.IsZero() || right.IsZero():
			return !left.IsZero() && right.IsZero()
		default:
			return left.Before(right)
		}
	})
}

// Next is the item to pull now: the first entry in the product manager's order
// that nothing is holding back. An unready entry is skipped rather than
// promoting the order past it, because the ordering says what matters most, not
// what happens to be startable.
func (q Queue) Next() (Entry, bool) {
	for _, entry := range q.Entries {
		if entry.Ready {
			return entry, true
		}
	}
	return Entry{}, false
}

// Ready counts the entries nothing is holding back.
func (q Queue) Ready() int {
	ready := 0
	for _, entry := range q.Entries {
		if entry.Ready {
			ready++
		}
	}
	return ready
}

// Parked counts the entries somebody has deliberately taken out of reach. It is
// counted apart from the unready rest because it is the one kind of unready that
// is a decision rather than a wait: an operator reading that nothing is pullable
// needs to know how much of that is work somebody parked, and how much is work
// waiting on something that will clear on its own.
func (q Queue) Parked() int {
	parked := 0
	for _, entry := range q.Entries {
		if entry.Parking.Parked() {
			parked++
		}
	}
	return parked
}

// Awaits reports a hold as the thing that actually stops this entry, and which
// of the two waits that hold is. It reads the precedence off the same derivation
// Hold and HoldKind read it from, rather than restating it: an executor no run
// can be and a parking both answer ahead of a hold, so an item that is both is
// not one of these — what its reader is told is the thing that would still
// refuse it once the hold was lifted, and counting it here would put it on a
// total nothing under the line accounts for.
//
// It is exported because more than one surface counts held work, and two of them
// counting it differently is the disagreement one derivation exists to prevent.
func (e Entry) Awaits() (held bool, carryOut bool) {
	if kind, _ := e.hold(); kind != HeldForAPerson {
		return false, false
	}
	return true, e.AwaitingCarryOut
}

// AwaitingDecision counts the held entries nobody has decided anything about
// yet, and AwaitingCarryOut the ones whose decision is recorded and has not been
// acted on. They are counted apart because they are two different people to go
// to: the first is the development manager's to settle and the second is the
// harness's to carry out, and a surface that adds them together tells an
// operator to go and chase decisions that have all been made.
func (q Queue) AwaitingDecision() int {
	return q.awaiting(func(carryOut bool) bool { return !carryOut })
}

func (q Queue) AwaitingCarryOut() int {
	return q.awaiting(func(carryOut bool) bool { return carryOut })
}

func (q Queue) awaiting(wanted func(carryOut bool) bool) int {
	awaiting := 0
	for _, entry := range q.Entries {
		if held, carryOut := entry.Awaits(); held && wanted(carryOut) {
			awaiting++
		}
	}
	return awaiting
}

// Gated counts the entries held by a step only a person can take. It is counted
// apart from the unready rest for the reason parking is, and for one more: it is
// the count that says how much of a stalled queue is waiting on the reader. An
// operator who sees that nothing is pullable and does not see that three items
// are waiting on them has been told the machine is idle rather than that it is
// their move.
func (q Queue) Gated() int {
	gated := 0
	for _, entry := range q.Entries {
		if entry.HumanGates.Holds() {
			gated++
		}
	}
	return gated
}

// Render describes the backlog for an operator: the order the product manager
// set, what is holding each unready item back, and what would be pulled next. An
// empty backlog is stated rather than printed as nothing at all, because "there
// is no admitted work" is an answer and a blank space is not.
func (q Queue) Render() string {
	if len(q.Entries) == 0 {
		return "backlog: nothing is admitted.\n"
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "backlog (%d admitted, %d ready to pull", len(q.Entries), q.Ready())
	// Parked work is counted in the header rather than left to be inferred from
	// the entries, because the whole point of parking is that it is a decision
	// somebody can see they made. A backlog with none says nothing, which keeps
	// the line about the queue rather than about a state most queues are not in.
	if parked := q.Parked(); parked > 0 {
		fmt.Fprintf(&rendered, ", %d parked", parked)
	}
	// Counted in the header for the same reason parking is, and because this is
	// the count that is about the reader: a queue where nothing is pullable and
	// two items are waiting on them is not a quiet machine.
	if gated := q.Gated(); gated > 0 {
		fmt.Fprintf(&rendered, ", %d waiting on a person", gated)
	}
	rendered.WriteString("):\n")
	listed := q.Entries
	if len(listed) > maxRenderedEntries {
		listed = listed[:maxRenderedEntries]
	}
	for _, entry := range listed {
		// A parked entry is marked on its own line rather than only explained
		// underneath, so parking is visible as parking while the list is being
		// skimmed. Reading it off the priority is exactly what stopped working.
		parked := ""
		if entry.Parking.Parked() {
			parked = " parked"
		}
		fmt.Fprintf(&rendered, "  %d. [%s] p%d%s %s\n", entry.Position, entry.ID, entry.Priority, parked,
			singleLine(entry.Title, maxRenderedTitleBytes))
		if entry.Ready {
			continue
		}
		fmt.Fprintf(&rendered, "     %s\n", entry.Hold())
	}
	if len(q.Entries) > len(listed) {
		fmt.Fprintf(&rendered, "  %d further admitted item(s) are not listed here.\n", len(q.Entries)-len(listed))
	}
	if next, ok := q.Next(); ok {
		fmt.Fprintf(&rendered, "next to be pulled: %s\n", next.ID)
		return rendered.String()
	}
	rendered.WriteString("nothing is ready to be pulled; every admitted item is held back by something, and each entry above says by what.\n")
	return rendered.String()
}

// Hold says what is keeping an unready entry from being pulled. The seven
// answers are different things to act on: an executor no run can be, a parking
// somebody decided, a hold somebody has to release, a step only a person can
// take, named work it waits for, a blocked item whose holds nothing could read,
// and the tracker simply not offering it, which is what a dependency the listing
// did not carry looks like from here.
//
// The first three answer before the rest because they are the ones that are not
// waiting for anything. Named work is an item that will be pulled once
// something clears; these three never will be until a person acts, and reading
// "waiting on" against any of them would send somebody looking for a blocker to
// release. Among the three, the executor answers first: an item that is both is
// one no run could take even after the parking is lifted, so naming the parking
// there would offer a release that changes nothing. The hold answers last of the
// three for the same reason in the other direction — it is the one somebody can
// actually act on today, so it must not shadow the two that would still refuse
// the item afterwards.
//
// A human gate answers next, ahead of everything that is a wait on other work,
// because it is the one hold that no amount of other work finishing will pass.
// An item that is gated and also waiting on a blocker is told about the gate:
// the blocker will clear on its own and the gate will not, so naming the blocker
// would have somebody watch for a moment that changes nothing. It answers after
// the governance hold rather than before it because the hold is the one with a
// next mover already named — a decision the development manager owes or the
// harness has yet to carry out — and an item stopped on both is settled in that
// order: the stoppage first, and the gate still standing once it is.
//
// It is exported because the refusal is the same fact wherever the queue is
// read, and the standing status names it per item: a second surface wording the
// same refusal differently is the disagreement one derivation exists to prevent.
func (e Entry) Hold() string {
	_, reason := e.hold()
	return reason
}

// HoldKind is which of the refusals Hold explains this entry carries, in a
// closed vocabulary a surface can group by. It exists for the surface that
// shows where admitted work accumulates: the sentence says what holds one
// item, and the kind says which pile it is in, so a pipeline is counted from
// the same reading the refusals are worded from rather than from a second
// parse of the sentences.
//
// It is owned here, by the queue, so that no surface redeclares it. Three of
// its values are refusals an entry's own hold never makes — the item's children
// covering it, which Coverage reads over the whole queue; a directive pausing
// the item; and the harness choosing nothing at all — and they are here all the
// same, because a vocabulary for why an admitted item is not pulled that left
// three of the reasons to another package would be two vocabularies.
type HoldKind string

const (
	// HeldForAPerson is a stoppage somebody has to release: a decision still
	// to be made, or one recorded and not yet carried out.
	HeldForAPerson HoldKind = "held"
	// HeldForAGate is an item declaring a step only a person can take, which
	// nobody has recorded taking, or a declaration nothing could read. It is a
	// pile of its own rather than part of the held one because its mover is
	// different: a held item is the development manager's to decide or the
	// harness's to carry out, and a gated one waits on the operator's recorded
	// act and on nothing any role or run can do.
	HeldForAGate HoldKind = "gated"
	// HeldParked is an item somebody deliberately took out of reach.
	HeldParked HoldKind = "parked"
	// HeldWaitingOn is an item waiting on other unfinished work, which clears on
	// its own as that work lands.
	HeldWaitingOn HoldKind = "waiting"
	// HeldByConversation is work no run carries out; a conversation does.
	HeldByConversation HoldKind = "conversation"
	// HeldUnread is an item the tracker does not offer and nothing here can
	// say why: blocked work whose holds could not be read, or a dependency the
	// listing did not carry.
	HeldUnread HoldKind = "unread"
	// HeldCovered is an item whose unfinished children already carry its
	// execution, so the scheduling pass never pulls it: the children are the
	// work. An entry's own hold does not make this refusal, because it is read
	// over the whole queue and one status wider — Cover and CoveredReason do —
	// and the standing status names it in those words.
	HeldCovered HoldKind = "covered"
	// HeldByDirective is an item an unresolved directive pauses. The queue does
	// not make this refusal; the pipeline does, and the standing status names it.
	HeldByDirective HoldKind = "directive"
	// HeldByStall is a pullable item nothing is choosing: a switch, a full
	// machine, or no session pulling. The queue does not make this refusal
	// either; it is the pass-level stall said once against each item it stops.
	HeldByStall HoldKind = "stalled"
)

// HoldKind is the kind of the refusal Hold returns, from the same reading.
func (e Entry) HoldKind() HoldKind {
	kind, _ := e.hold()
	return kind
}

// hold is the one derivation behind Hold and HoldKind, so the sentence and the
// pile it is counted in cannot come apart.
func (e Entry) hold() (HoldKind, string) {
	switch {
	case !e.Executor.DeveloperRun():
		return HeldByConversation, fmt.Sprintf("its executor is %q rather than a developer run, so no run carries it out; the item says which conversation does", e.Executor)
	case e.Parking.Parked():
		return HeldParked, "parked, so no pull selects it however far the queue drains: " + e.Parking.Reason()
	case e.Awaiting != "" && e.AwaitingLanding:
		// A wait rather than a hold for a person: nobody decides anything, and it
		// clears as the parent's change lands.
		return HeldWaitingOn, e.Awaiting
	case e.Awaiting != "":
		return HeldForAPerson, e.Awaiting
	case e.HumanGates.Holds():
		return HeldForAGate, e.HumanGates.Describe(e.ID)
	case len(e.WaitingOn) > 0:
		return HeldWaitingOn, "waiting on " + strings.Join(e.WaitingOn, ", ")
	case e.Status == statusBlocked:
		// Blocked work with nothing waiting and no hold is pullable, so this is only
		// reached where nothing could read the holds. Saying the tracker did not
		// offer it would be true and useless: the tracker never offers blocked work,
		// and what actually held it is the reading that did not happen.
		return HeldUnread, unreadHold
	default:
		return HeldUnread, "the tracker does not report it as ready to pull"
	}
}

// waitingOn names the unfinished work an item depends on: the blocking
// dependencies its listing recorded whose own item is still in the backlog.
//
// The membership test is what keeps this honest where the listing says nothing.
// A dependency in a Beads listing may record only that the relation exists — it
// then reads the same after the blocker is closed as before — so a dependency
// alone says nothing about whether anybody is still waiting. What does say so is
// whether the depended-on item is still queued.
//
// A status the listing does carry is believed either way, and that is what makes
// this the same reading the run-start gate makes. Work the tracker reports as
// closed is nobody's wait; work it reports as anything else is a wait even when
// the item has left this backlog, which is exactly what a blocker somebody is
// running right now looks like from here. Believing only the closed half would
// call an item startable that the pipeline then refuses at the door.
//
// The reading itself is the item's own, and is deliberately not a second copy of
// it here. What the queue calls startable and what the claim will accept have to
// be one answer: they were two, and the twenty-nine dispatches of
// yoyodyne-ifd.285 that died at the claim in twenty hours are what that cost.
func waitingOn(item beads.WorkItem, unfinished map[string]struct{}) []string {
	return item.WaitingOn(unfinished)
}

// singleLine folds a value into one bounded line, so tracker prose stays a list
// entry whatever it contains. It is cut on a rune boundary: a line truncated
// mid-rune is not text.
func singleLine(value string, limit int) string {
	return oneline.Fold(value, limit)
}
