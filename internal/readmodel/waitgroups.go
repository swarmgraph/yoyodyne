package readmodel

// What the not-startable work waits on, counted by who moves it.
//
// On 2026-09-27 the operator read "140 admitted items nothing will pull, of
// 140 admitted items" and asked what it meant and what he was supposed to do
// with it. Forty-five of them were ready and waited only for a developer slot,
// filed as stalled; the rest waited on six different things, none of them his.
// The line said neither. So the ready work waiting for a slot is counted on a
// line of its own and never inside the not-startable count, and the rest is
// counted by what it waits on, each group with its next step and whose it is,
// and one sentence says whether any of it is the operator's.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// SlotWait is ready work that nothing refuses and that waits only for a
// developer slot, because every slot is taken. It is not refused work: the
// harness starts the next of these the moment a run in flight finishes, and
// nothing is asked of anybody.
type SlotWait struct {
	// Ready is how many admitted items are ready and waiting for a slot.
	Ready int `json:"ready"`
	// Slots is the configured developer slots, and InFlight how many runs hold
	// them — which can exceed Slots where runs were started beyond it.
	Slots    int `json:"slots"`
	InFlight int `json:"in_flight"`
	// Items names them, in the Lead Product Manager's order: the order the
	// harness pulls them in as slots free.
	Items []WorkItemRef `json:"items"`
	// Line and Next are Says and Next as the reading worded them, carried so the
	// dashboard prints the terminal's words rather than composing its own.
	Line string `json:"says"`
	Next string `json:"next"`
}

// said fills in the carried sentences once the wait is counted.
func (w *SlotWait) said() {
	w.Line = w.Says()
	w.Next = slotWaitNext
}

// slotWaitNext is what happens to the slot wait, and whose move it is: nobody's.
const slotWaitNext = "not counted as not startable: the harness starts the next one as a run in flight finishes, and nothing is asked of anybody"

// Says is the slot wait in the words the not-startable line and the dashboard
// print: "45 ready, waiting for a developer slot; 3 slots, all taken".
func (w SlotWait) Says() string {
	taken := "all taken"
	if w.Slots == 1 {
		taken = "taken"
	}
	return fmt.Sprintf("%d ready, waiting for a developer slot; %s, %s", w.Ready, count(w.Slots, "slot"), taken)
}

// WaitGroup is the not-startable items that wait on one thing, counted, with
// the next step and whose move it is. The groups are the queue's own piles —
// Kind is the backlog's vocabulary — with the held pile split by which of its
// two waits it is in and the conversation pile split by the role that carries
// it, because each of those is a different person to go to.
type WaitGroup struct {
	Kind backlog.HoldKind `json:"kind"`
	// Awaiting is which wait a held group is in, and empty for every other.
	Awaiting HeldWait `json:"awaiting,omitempty"`
	Count    int      `json:"count"`
	// WaitsOn is what the items wait on, in the words the line prints after the
	// count, the verb agreeing with it: "wait on the development manager's
	// decision", "waits on" for one.
	WaitsOn string `json:"waits_on"`
	// Next is the next step, and Mover whose it is.
	Next  string `json:"next"`
	Mover Mover  `json:"mover"`
	// Items names the items in the group, in the Lead Product Manager's order.
	Items []WorkItemRef `json:"items"`
}

// Says is the group as one line prints it: the count, what it waits on, the
// next step, and whose it is.
func (g WaitGroup) Says() string {
	return fmt.Sprintf("%s — next: %s; whose: %s", g.counted(), g.Next, g.Mover.Possessive())
}

// counted is the count and what it waits on.
func (g WaitGroup) counted() string {
	return fmt.Sprintf("%d %s", g.Count, g.WaitsOn)
}

// agree makes a plural phrase's verb agree with a count of one: "wait on" is
// "waits on", and "are" is "is".
func agree(number int, phrase string) string {
	if number != 1 {
		return phrase
	}
	switch {
	case strings.HasPrefix(phrase, "wait "):
		return "waits " + strings.TrimPrefix(phrase, "wait ")
	case strings.HasPrefix(phrase, "are "):
		return "is " + strings.TrimPrefix(phrase, "are ")
	}
	return phrase
}

// waitGroupRank is the order the groups are listed in: the ones a person moves
// first, then the harness's, then the ones that clear on their own or that
// nothing here can explain.
var waitGroupRank = map[backlog.HoldKind]int{
	backlog.HeldForAPerson:     0,
	backlog.HeldByDirective:    1,
	backlog.HeldForAGate:       2,
	backlog.HeldByStall:        3,
	backlog.HeldWaitingOn:      4,
	backlog.HeldInherited:      5,
	backlog.HeldByConversation: 6,
	backlog.HeldParked:         7,
	backlog.HeldCovered:        8,
	backlog.HeldUnread:         9,
}

// waitGroups gathers not-startable items into their groups as the reading
// meets them, so the groups are counted from the same entries the refusals are.
type waitGroups struct {
	groups map[string]*WaitGroup
	stall  Stall
	held   switches
	// unfinished is the work a decision to wait may still be waiting on.
	unfinished map[string]bool
}

func newWaitGroups(stall Stall, held switches) *waitGroups {
	return &waitGroups{groups: map[string]*WaitGroup{}, stall: stall, held: held}
}

// add puts one refused entry in its group. The entry is the queue's own, so a
// held entry's wait and a conversation entry's role are read off it rather
// than off the refusal's sentence.
func (w *waitGroups) add(entry backlog.Entry, kind backlog.HoldKind) {
	group := w.shape(entry, kind)
	key := string(group.Kind) + "\x00" + string(group.Awaiting) + "\x00" + string(group.Mover)
	if group.Kind == backlog.HeldInherited {
		key += "\x00" + group.WaitsOn
	}
	existing, ok := w.groups[key]
	if !ok {
		existing = &group
		w.groups[key] = existing
	}
	existing.Count++
	existing.Items = append(existing.Items, WorkItemRef{WorkItemID: entry.ID, Title: entry.Title})
}

// shape is the group one entry belongs to, with everything but its count and
// items: what it waits on, the next step, and whose move that is.
func (w *waitGroups) shape(entry backlog.Entry, kind backlog.HoldKind) WaitGroup {
	switch kind {
	case backlog.HeldForAPerson:
		if _, carryOut := entry.Awaits(); carryOut {
			return WaitGroup{Kind: kind, Awaiting: HeldAwaitingCarryOut, Mover: MoverHarness,
				WaitsOn: "wait on the harness carrying out a decision already recorded",
				Next:    "the harness acts on the recorded decision — a repair, a re-run, or a re-armed merge — at its next pull"}
		}
		if waitingOnWork(entry, w.unfinished) {
			return WaitGroup{Kind: kind, Awaiting: HeldAwaitingWork, Mover: MoverHarness,
				WaitsOn: "wait on admitted work the development manager decided to wait for",
				Next:    "once the work each waits on is closed, the harness puts the stoppage back on her docket for a decision"}
		}
		return WaitGroup{Kind: kind, Awaiting: HeldAwaitingDecision, Mover: MoverDevelopmentManager,
			WaitsOn: "wait on the development manager's decision about a stopped run",
			Next:    "she decides what becomes of each stopped run: a repair, a re-run, a wait, a re-scope, or an escalation"}
	case backlog.HeldByDirective:
		return WaitGroup{Kind: kind, Mover: MoverOperator,
			WaitsOn: "wait on an unresolved directive",
			Next:    "`yoyo directive resolve` settles it, and the work it pauses is pulled"}
	case backlog.HeldForAGate:
		return WaitGroup{Kind: kind, Mover: MoverOperator,
			WaitsOn: "wait on a step only a person can take",
			Next:    "the operator records each act with `yoyo gate record <name> --for <item>`, and closing an item never passes one; a declaration nothing could read waits on its author correcting it"}
	case backlog.HeldByStall:
		return WaitGroup{Kind: kind, Mover: stallMover(w.stall, w.held),
			WaitsOn: "are ready and nothing is choosing work: " + w.stall.Says,
			Next:    stallNext(w.stall)}
	case backlog.HeldWaitingOn:
		return WaitGroup{Kind: kind, Mover: MoverHarness,
			WaitsOn: "wait on other items",
			Next:    "the harness pulls each once the work it waits on lands"}
	case backlog.HeldInherited:
		// Grouped by the ancestor that waits, so the line names it and what it
		// waits on rather than saying only that something above the items does.
		waits := "an item above them that waits on other work"
		if len(entry.InheritedBlocks) > 0 {
			block := entry.InheritedBlocks[0]
			waits = fmt.Sprintf("%s, which waits on %s and holds back the items under it",
				block.Name(block.Waiting(), ""), inheritedWaits(block))
		}
		return WaitGroup{Kind: kind, Mover: MoverHarness,
			WaitsOn: "are blocked through " + waits,
			Next:    "the tracker offers each once that work lands; each item's own line names the parents between it and the waiting item"}
	case backlog.HeldByConversation:
		role := entry.Executor.Role()
		carrier := "a role's conversation"
		if role.Valid() {
			carrier = "the " + role.Title() + "'s conversation"
		}
		return WaitGroup{Kind: kind, Mover: MoverOf(role),
			WaitsOn: "are done in " + carrier + ", not by a run",
			Next:    "the role does the work in its conversation, and the harness closes each item when the role's revision lands"}
	case backlog.HeldParked:
		return WaitGroup{Kind: kind, Mover: MoverProductManager,
			WaitsOn: "are parked by the " + domain.RoleProductManager.Title(),
			Next:    "she releases each once what it was parked for is settled"}
	case backlog.HeldCovered:
		return WaitGroup{Kind: kind, Mover: MoverHarness,
			WaitsOn: "are covered by other work: their own unfinished children",
			Next:    "the harness runs the children; nothing pulls the covering item itself"}
	default:
		return WaitGroup{Kind: kind, Mover: MoverNobody,
			WaitsOn: "are not offered by the tracker, and nothing here can say why",
			Next:    "the refusal beside each item says what could not be read"}
	}
}

// inheritedWaits names what a waiting ancestor waits on.
func inheritedWaits(block beads.InheritedBlock) string {
	waits := make([]string, 0, len(block.WaitsOn))
	for _, dependency := range block.WaitsOn {
		waits = append(waits, block.Name(dependency.ID, ""))
	}
	return strings.Join(waits, ", ")
}

// list is the groups in the order a reader acts on them, empty rather than nil.
func (w *waitGroups) list() []WaitGroup {
	listed := make([]WaitGroup, 0, len(w.groups))
	for _, group := range w.groups {
		agreed := *group
		agreed.WaitsOn = agree(agreed.Count, agreed.WaitsOn)
		listed = append(listed, agreed)
	}
	sort.SliceStable(listed, func(i, j int) bool {
		a, b := listed[i], listed[j]
		if waitGroupRank[a.Kind] != waitGroupRank[b.Kind] {
			return waitGroupRank[a.Kind] < waitGroupRank[b.Kind]
		}
		if a.Awaiting != b.Awaiting {
			return a.Awaiting == HeldAwaitingDecision
		}
		return a.Mover < b.Mover
	})
	return listed
}

// stallMover is whose move a pass-level stall is, in the attention line's
// vocabulary and by the same readings: the intake hold's mover is the one the
// attention line gives it.
func stallMover(stall Stall, held switches) Mover {
	switch stall.Reason {
	case ReasonIntakeHold:
		return intakeHoldAttention(held.intake).Mover
	case ReasonStoreUnreadable, ReasonDrainOverrun:
		return MoverHarness
	case ReasonProviderWindow, ReasonTrackerWait, ReasonNoCapacity, ReasonRedeploying:
		return MoverNobody
	default:
		return MoverOperator
	}
}

// stallNext is what ends a pass-level stall: the command that settles it where
// one does, and otherwise what the reason's own attribution says settles it.
func stallNext(stall Stall) string {
	if stall.Clears != "" {
		return stall.Clears
	}
	whose := stall.Reason.Whose()
	if _, after, found := strings.Cut(whose, " — "); found {
		return after
	}
	return whose
}

// ForOperator is the one sentence that says whether anything on the
// not-startable line, or the dashboard section that shows it, is the
// operator's. His rule of 2026-09-26 is that what is
// his is a change to the fundamental goals; the sentence names the groups the
// read model gives him, and says so plainly where there are none.
func ForOperator(groups []WaitGroup) string {
	var his []string
	items := 0
	for _, group := range groups {
		if group.Mover == MoverOperator {
			his = append(his, group.counted())
			items += group.Count
		}
	}
	if len(his) == 0 {
		return "nothing here is the operator's: under their rule of 2026-09-26 only a change to the fundamental goals is, and nothing here waits on one"
	}
	return fmt.Sprintf("%s here %s the operator's — %s; everything else is somebody else's to move",
		count(items, "item"), isAre(items), strings.Join(his, "; "))
}

func isAre(number int) string {
	if number == 1 {
		return "is"
	}
	return "are"
}
