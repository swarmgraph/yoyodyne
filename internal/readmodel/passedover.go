package readmodel

// What a poll that started nothing left behind, derived once for the line that
// says it at a terminal and the alarm that says it on somebody's phone.
//
// The two used to answer the same question separately, and on 2026-09-06 they
// answered it differently: the stall alarm woke the operator with "nothing
// started for an hour, work is ready, nothing accounting for it" while the watch
// session's own idle line, one surface over, held the whole accounting — a third
// of the queue waiting on triage decisions and the rest carried in conversations
// or sequenced behind runs. Nothing was missing from the harness's knowledge. It
// was missing from the surface that woke somebody, because that surface derived
// its own answer from an absence rather than reading the answer the pull had
// already written down.
//
// So the accounting is derived here, once, from the record the pull leaves: the
// classes it passed items over in, how many fell in each, and the size of the
// queue they were read from. The idle line renders all of it, because it is read
// by somebody who asked. The alarm renders the dominant class and whose move it
// is, because it is read by somebody who did not ask and is holding a phone.
//
// # The cause is the poll's, not this reading's
//
// Nothing here reads a queue or a tracker. It folds what the session recorded,
// which is what lets the alarm state a cause for a session that has since died:
// the last poll's account is the last thing anybody knows, and it is stated as
// of that poll rather than as of now. A reading that went back to the tracker
// would be a second derivation of the same question, which is the thing this
// file exists to remove.

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// MaxPassedOverNamed bounds how many items one class names before the rest are
// counted rather than listed. The count stays exact either way, for the reason
// every listing here is bounded: forty deferred items would otherwise put forty
// identifiers into a line whose whole job is to be read at a glance.
const MaxPassedOverNamed = 5

// PassedOverItem is one item a poll did not start: which item, which class it
// was left in, and the conversation that carries it where the class names one.
// The role is empty for every class but the conversation-carried one, which is
// the only class whose answer is a person rather than a wait.
//
// The reason is empty for every class but the already-tried one, which is the
// only class whose cause is an event rather than a state: every other exclusion
// names something a reader can go and read for themselves, and this one names an
// attempt only the session remembers. See runstate.PassedOverGroup.Reasons.
type PassedOverItem struct {
	ID     string
	Class  runstate.PassedOverClass
	Role   domain.AgentRole
	Reason string
}

// GroupPassedOver gathers what one poll passed over into one group per class, in
// the order the pull met them, against the size of the queue it read.
//
// Grouping is what gives the account something to act on: a reader takes a class
// and knows what to do about all of it, where a list of items one per line is a
// list they have to classify themselves. It is also what makes the record small
// enough to keep — the names are bounded here rather than at each renderer, so
// what is written down and what is read back are the same account.
func GroupPassedOver(passed []PassedOverItem, admitted int) runstate.PassedOver {
	type key struct {
		class runstate.PassedOverClass
		role  domain.AgentRole
	}
	var order []key
	counted := make(map[key]int, len(passed))
	named := make(map[key][]string, len(passed))
	because := make(map[key][]string, len(passed))
	for _, item := range passed {
		at := key{class: item.Class, role: item.Role}
		if _, met := counted[at]; !met {
			order = append(order, at)
		}
		counted[at]++
		if len(named[at]) < MaxPassedOverNamed {
			named[at] = append(named[at], item.ID)
			because[at] = append(because[at], boundedReason(item.Reason))
		}
	}
	account := runstate.PassedOver{Admitted: admitted}
	for _, at := range order {
		account.Groups = append(account.Groups, runstate.PassedOverGroup{
			Class:   at.class,
			Role:    at.role,
			Count:   counted[at],
			Items:   named[at],
			Reasons: reasonsIfAny(because[at]),
		})
	}
	return account
}

// boundedReason is one item's reason cut to what the record holds. The cut is
// made here rather than at each caller for the reason the naming is bounded
// here: what is written down and what is read back have to be the same account,
// and a reason the store refuses would cost the session the whole transition
// rather than the sentence.
//
// The cut lands on a rune boundary and says it was cut, because a reason ending
// mid-character is one a later reader cannot decode and a clamped one read as
// complete is an account that stops where nothing stopped.
func boundedReason(reason string) string {
	if len(reason) <= runstate.MaxPassedOverReasonBytes {
		return reason
	}
	cut := runstate.MaxPassedOverReasonBytes - len(reasonCutNote)
	for cut > 0 && !utf8.RuneStart(reason[cut]) {
		cut--
	}
	return reason[:cut] + reasonCutNote
}

const reasonCutNote = " […]"

// reasonsIfAny drops a class's reasons where none of its named items had one, so the
// classes that say the whole of it in their own name carry no empty strings
// about with them. The slice is positional, so it is all or nothing: a class
// where some items carry a reason keeps the empties, which is what makes the
// position mean the item it names.
func reasonsIfAny(reasons []string) []string {
	for _, reason := range reasons {
		if reason != "" {
			return reasons
		}
	}
	return nil
}

// Carrier is the conversation an account is waiting on, which is the marker on
// the highest-priority item the poll passed over for one. It is empty where no
// such item was passed over, which is every poll whose answer is a wait rather
// than a person.
//
// The highest-priority one is named because that is the item an operator would
// open first, and because the line renders every one of them by role anyway:
// this decides whose move a message closes on, and the prose is where the rest
// of them are.
func Carrier(account runstate.PassedOver) domain.WorkItemExecutor {
	for _, group := range account.Groups {
		if group.Class == runstate.PassedOverCarriedInConversation && group.Role != "" {
			return domain.ConversationWith(group.Role)
		}
	}
	return ""
}

// IdleLine is the whole account as the session's own log says it: what is
// already running, and what this poll passed over and why.
//
// Both halves are said because either alone misleads, and both misled. The line
// said only that nothing was startable, which reads as a stopped machine while a
// run works on the other slot; and it named a count without naming the items or
// the reason, which reads as a queue that will move on its own while the only
// unstarted work is somebody's to carry in conversation. An operator acted on
// that reading three times.
func IdleLine(account runstate.PassedOver, inFlight int) string {
	if account.Admitted == 0 {
		return "the backlog is empty"
	}
	var said []string
	if inFlight > 0 {
		said = append(said, fmt.Sprintf("%s in flight", counted(inFlight, "run", "runs")))
	}
	if groups := passedOverGroups(account); len(groups) > 0 {
		said = append(said, fmt.Sprintf("%s passed over, of %d admitted: %s",
			counted(account.Passed(), "item", "items"), account.Admitted, strings.Join(groups, "; ")))
	}
	if len(said) == 0 {
		// The pull stopped before it read the queue through, which is what a session
		// at its own item limit does. Saying what it did not reach is the honest
		// answer; saying nothing was startable would be a claim about items nothing
		// looked at.
		return fmt.Sprintf("none of the %s admitted was reached at this poll", counted(account.Admitted, "item", "items"))
	}
	// Cut to what the session's log will actually take. The line is assembled from
	// a queue nothing here bounds — twelve classes, five names each, and now a
	// reason against the names that carry one — and a line the store refuses costs
	// the session the whole transition rather than the tail of a sentence, which is
	// the silence every one of these accounts exists to end.
	return boundedLine(strings.Join(said, "; "))
}

func boundedLine(line string) string {
	if len(line) <= runstate.MaxWatchReasonBytes {
		return line
	}
	cut := runstate.MaxWatchReasonBytes - len(reasonCutNote)
	for cut > 0 && !utf8.RuneStart(line[cut]) {
		cut--
	}
	return line[:cut] + reasonCutNote
}

// passedOverGroups is one clause per class, naming the conversation where the
// class names one and what excluded each item where the class carries that.
func passedOverGroups(account runstate.PassedOver) []string {
	groups := make([]string, 0, len(account.Groups))
	for _, group := range account.Groups {
		listed := strings.Join(namedWithReasons(group), ", ")
		if further := group.Count - len(group.Items); further > 0 {
			listed += fmt.Sprintf(", and %d further", further)
		}
		if group.Role != "" {
			groups = append(groups, fmt.Sprintf("%s (%s: %s)", group.Class, group.Role.Title(), listed))
			continue
		}
		groups = append(groups, fmt.Sprintf("%s (%s)", group.Class, listed))
	}
	return groups
}

// namedWithReasons is a class's named items, each with what excluded it where
// the class recorded one. A class that says the whole of it in its own name
// reads exactly as it did before this existed.
func namedWithReasons(group runstate.PassedOverGroup) []string {
	named := make([]string, 0, len(group.Items))
	for index, item := range group.Items {
		if index < len(group.Reasons) && group.Reasons[index] != "" {
			named = append(named, fmt.Sprintf("%s — %s", item, group.Reasons[index]))
			continue
		}
		named = append(named, item)
	}
	return named
}

// counted says a count in words, so a line an operator reads says "1 run" rather
// than "1 run(s)". The parenthesised plural is what a status line looks like when
// nobody read it out loud.
func counted(count int, one, many string) string {
	if count == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", count, many)
}

// Cause is the one thing most accounting for a queue that is not moving, as the
// last poll that started nothing recorded it: the class most of what it passed
// over fell into, or the state that stopped it reading the queue at all.
//
// It is one cause rather than the whole account, and that is the difference
// between the two renderings. A terminal answers when it is asked and can afford
// every class; a message that wakes somebody has one sentence, and a sentence
// that lists ten classes is one nobody acts on.
type Cause struct {
	// Unreadable is the poll that could not read the queue at all. It answers
	// ahead of every class, because nothing a person admits, releases, or opens
	// reaches a store that will not answer — and because what was in the queue is
	// not what stopped the choosing.
	Unreadable bool
	// Window is the provider refusing to serve the harness, where the poll
	// recorded itself waiting one out. It answers ahead of every class for the
	// same reason: nothing in the queue is startable while it stands. It is the
	// state that woke the operator on 2026-09-05 with a machine that was behaving.
	Window ProviderWindow
	// Class is the class most of what the poll passed over fell into, with the
	// conversation that carries it where the class names one, how many items it
	// holds, and the size of the queue they were counted against.
	Class    runstate.PassedOverClass
	Role     domain.AgentRole
	Count    int
	Admitted int
}

// WhyThePollStartedNothing is the cause the last poll that started nothing
// recorded, or none where no poll has said anything a cause can be read from.
//
// It reads the live sessions rather than the log's last line, for the reason
// every fold here does: one log holds every session a product has had, and a
// last entry can be one session stopping while another carries on. A session
// whose latest word is that it is watching, braked, or resumed has no idle
// account to give, and a session that stopped cleanly is a line somebody has to
// start rather than a queue with something in the way of it — both are reported
// as no cause rather than as a stale one.
//
// # An account a start overtook is not the present cause
//
// "since" is when the silence being asked about began — for a stall, the moment
// the harness last started anything. A poll recorded before that is an account
// something has overtaken: work started after the poll and the line then went
// quiet, so the queue has not been read since it moved, and what the poll
// describes is a state nothing has checked against what happened next. It is
// refused rather than stated as the present cause, which leaves the message
// saying nothing accounts for the silence and pointing at the chooser.
//
// A live session idling over an unchanging queue writes one line and then
// nothing, so its account is old and current at once. That is the case the bound
// is careful to keep: the poll is what followed the last start, so it is not
// overtaken however long it has stood.
//
// What the bound does not do is tell a live session from a dead one, and nothing
// here can. A session that died while idle also polled after the last start, so
// its account stands and is named — which is the right answer about the queue,
// since those items are still held, still parked, still carried, and no answer at
// all about the process. What answers that is the chooser's last word, which
// every message says beside the cause rather than in place of it.
//
// A zero "since" asks the question with no silence attached and takes the latest
// poll whatever its age.
func WhyThePollStartedNothing(sessions []runstate.WatchTransition, since, now time.Time) (Cause, bool) {
	// Live is newest first, so the first entry is the latest word from a session
	// that has not stopped.
	live := Live(sessions)
	if len(live) == 0 || live[0].State != runstate.WatchIdle {
		return Cause{}, false
	}
	poll := live[0]
	if !since.IsZero() && poll.At.Before(since) {
		return Cause{}, false
	}
	if poll.Unreadable {
		return Cause{Unreadable: true}, true
	}
	if window := WaitingOnProvider(sessions); window.Standing(now) {
		return Cause{Window: window}, true
	}
	dominant, found := dominantGroup(poll.PassedOver)
	if !found {
		return Cause{}, false
	}
	return Cause{
		Class:    dominant.Class,
		Role:     dominant.Role,
		Count:    dominant.Count,
		Admitted: poll.PassedOver.Admitted,
	}, true
}

// dominantGroup is the class holding most of what the poll passed over. A tie is
// settled by the order the pull met them, which is the product manager's own
// order: two classes holding the same number are reported as the one the queue
// puts first, rather than as whichever the map iterated to.
func dominantGroup(account runstate.PassedOver) (runstate.PassedOverGroup, bool) {
	var dominant runstate.PassedOverGroup
	found := false
	for _, group := range account.Groups {
		if !found || group.Count > dominant.Count {
			dominant, found = group, true
		}
	}
	return dominant, found
}

// Says is the cause as one clause, said as a fraction of the queue it was
// counted against: "33 of the 47 admitted items are awaiting carry-out of
// decisions already recorded".
//
// The fraction is the whole point of it. "Some items are held" is a fact a
// reader can do nothing with; "most of the queue is held" is the difference
// between a machine that has died and a machine with nothing it is allowed to
// start, which is the difference the alarm was getting wrong.
func (c Cause) Says() string {
	switch {
	case c.Unreadable:
		return "the last poll could not read the queue at all"
	case c.Window.Waiting:
		// The provider's own sentence, carried whole rather than reworded. It is the
		// one clause here that is already a sentence, and it is the operator's
		// acceptance that it stays one wherever it is said.
		return c.Window.Says()
	}
	clause, named := passedOverClauses[c.Class]
	if !named {
		return ""
	}
	if c.Class == runstate.PassedOverCarriedInConversation && c.Role != "" {
		clause += " by the " + c.Role.Title()
	}
	noun := "items"
	if c.Admitted == 1 {
		noun = "item"
	}
	return fmt.Sprintf("%d of the %d admitted %s %s %s", c.Count, c.Admitted, noun, are(c.Count), clause)
}

// are agrees the clause with the count, because a sentence that says "1 of the
// 47 admitted items are held" is one a reader stops trusting the arithmetic of.
func are(count int) string {
	if count == 1 {
		return "is"
	}
	return "are"
}

// passedOverClauses is what each class says about the items in it. Every class
// has one: a class nobody wrote a clause for is a state the alarm would report
// as an empty cause, which is the confident emptiness this whole file exists to
// remove, and a test holds the table to the taxonomy.
var passedOverClauses = map[runstate.PassedOverClass]string{
	runstate.PassedOverCarriedInConversation: "carried in conversation",
	runstate.PassedOverParked:                "parked, and no pull selects a parked item however far the queue drains",
	runstate.PassedOverHeldForAPerson:        "stopped, waiting on the development manager's decision or the harness carrying it out",
	runstate.PassedOverAwaitingDecision:      "awaiting the development manager's decision",
	runstate.PassedOverAwaitingCarryOut:      "awaiting carry-out of decisions already recorded",
	runstate.PassedOverWaitingOnAPerson:      "waiting on a step only a person can take, which no item's closure passes",
	runstate.PassedOverWaitingOnOtherWork:    "waiting on work that has not landed yet",
	runstate.PassedOverAlreadyTried:          "already tried by this session and waiting out its cooling",
	runstate.PassedOverAlreadyInFlight:       "already carried by a run in flight",
	runstate.PassedOverCoveredByChildren:     "covered by the children the work was broken out into",
	runstate.PassedOverPausedByDirective:     "paused by a directive nobody has resolved",
	runstate.PassedOverSequencedBehindWork:   "sequenced behind work in flight that they would race",
	runstate.PassedOverPrerequisiteUnmet:     "asking for something the tree does not have",
	runstate.PassedOverLeftForAnotherSlot:    "left for a developer slot with no preference, because every free slot pulled its preferred label's work ahead of them",
	runstate.PassedOverWaitingOnUsageWindow:  "refused by the provider's usage limit and waiting for it to reset",
}

// Whose is whose move it is, and what settles it. It is the other half of what a
// cause is for: an alarm that names what is holding the queue without saying who
// releases it has told the reader something they can do nothing with, which is
// the state the alarm was in.
//
// Every cause answers. One that did not would be a state named and then left
// unattributed, so the zero answer belongs to no cause and a test holds the set
// to it.
func (c Cause) Whose() string {
	switch {
	case c.Unreadable:
		return "the harness's — the queue could not be read, and it is read again until it answers or the session gives up on it"
	case c.Window.Waiting:
		return "nobody's — the window lifts on the provider's clock, and the queue is read again when it does"
	case c.Class == runstate.PassedOverCarriedInConversation && c.Role != "":
		return "the " + c.Role.Title() + "'s, in conversation — the work this poll passed over is carried there, and no run will ever start it"
	}
	return passedOverMoves[c.Class]
}

// passedOverMoves is whose move follows each class. The ones that name a role
// are the ones that never clear on their own: a parking, a stoppage nobody has
// decided about, and an item asking the tree for something nobody has put there.
// Only the one that names the operator needs a human, so no sentence here says
// "a person" of a role's move: on 2026-09-27 the dashboard said "held for a
// person" over thirty-four items the development manager was moving.
// Everything else clears as work lands, which is a wait rather than a move, and
// saying otherwise would send somebody to release a queue that is releasing
// itself.
//
// A decision already recorded and not carried out names the harness, and it is
// the one class here whose next mover is neither a person nor a wait. That is
// the whole of what separating it bought: an operator reading "waiting on triage
// decisions" goes to the development manager, and for thirty-three items on
// 2026-09-07 she had made every one of them.
var passedOverMoves = map[runstate.PassedOverClass]string{
	runstate.PassedOverCarriedInConversation: "the role that carries them, in conversation — no run will ever start them",
	runstate.PassedOverParked:                "the Lead Product Manager's — a parked item is passed over at every pull until it is released",
	runstate.PassedOverHeldForAPerson:        "the development manager's, or the harness's where she has decided — nothing pulls a stopped item until her decision about it is made and carried out",
	runstate.PassedOverAwaitingDecision:      "the development manager's — nothing pulls a stopped item until she decides what happens to it",
	runstate.PassedOverAwaitingCarryOut:      "the harness's — the decisions are recorded, and what is outstanding is the harness acting on them",
	runstate.PassedOverWaitingOnAPerson:      "a person's — the item declares a human gate, and `yoyo gate record` is the only thing that passes it",
	runstate.PassedOverWaitingOnOtherWork:    "nobody's — the work they wait on lands or does not, and the queue is read again either way",
	runstate.PassedOverAlreadyTried:          "nobody's — the session tries them again once they have cooled",
	runstate.PassedOverAlreadyInFlight:       "nobody's — the runs carrying them finish, and the queue is read again as each of them does",
	runstate.PassedOverCoveredByChildren:     "nobody's — the children are the work, and what covers them closes as they land",
	runstate.PassedOverPausedByDirective:     "the operator's — the work stays paused until the directive is resolved",
	runstate.PassedOverSequencedBehindWork:   "nobody's — each is pulled at the first pull where the run it would have raced has ended",
	runstate.PassedOverPrerequisiteUnmet:     "the development manager's — the item asks for something the tree does not have, and it is docketed rather than dispatched",
	runstate.PassedOverLeftForAnotherSlot:    "nobody's — a developer slot with no preference takes them in the Lead Product Manager's order, and a preferring slot falls back to them once its label's work is exhausted",
	runstate.PassedOverWaitingOnUsageWindow:  "nobody's — the window lifts on the provider's clock, and the session pulls each of them again once its reset has passed",
}
