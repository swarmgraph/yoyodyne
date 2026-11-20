package notify

// What a conversation did to the backlog, read from the log it wrote while
// doing it.
//
// Runs are the first producer of reportable events; conversations are the
// second, and they arrive the way the design said a producer would — a selection
// function here, and nothing whatever in the sink, the threading, or the
// envelope. The shape is the same as a run's: a durable record written by a
// process that is long gone, read afterwards by something that decides nothing
// about it.
//
// What is worth saying about a conversation is not what was said in it. Most of
// a conversation's log is the turn itself — the provider's messages, the tools
// it asked for, the reply as it was written — and none of that is a milestone.
// What is a milestone is the queue moving: work admitted, work decomposed, a
// goal recorded on an item, an item's place in the order changed, and what the
// operator decided about work an agent proposed. Those are the harness's
// steering wheel turning, and an operator who cannot see them is watching a
// factory whose direction changes silently.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The tracker operations this reports on. They are named here rather than
// imported so that what is reported stays independent of how a conversation
// carries an action out — the same reason a run's milestones are read by a rule
// stated in this package rather than by the pipeline's own.
//
// The rest of the vocabulary is deliberately absent. Reading an item and
// surveying the queue change nothing; a note, a link, and a reparenting are the
// structure around work rather than the work arriving or moving; and closing an
// item is already said by the run that finished it. A channel that reported
// every tracker call would be the event log this exists not to be.
//
// The exception is work no run carries, and it is the reason two of these are
// named at all. An item marked for a conversation is never selected, so there is
// no run to say any of it: the note the architect writes on it is the only sign
// the work started, and the close is the only sign it finished. For that item the
// rule above inverts — the actions that are silent everywhere else are the whole
// of its narrative, and without them its thread ends on a failed run and says
// nothing for the rest of the item's life.
//
// The inversion is exactly that narrow. It covers the two actions that are a role
// doing the work, and it does not touch the ones that are the queue being kept
// around the work: an item marked for a conversation still gets attributed to a
// goal and still gets reordered, those are still what they always were, and
// reading either of them as somebody starting the work would report a milestone
// that did not happen and lose the one that did.
const (
	trackerCreate       = "create"
	trackerAttribute    = "attribute"
	trackerReprioritize = "reprioritize"
	trackerUpdate       = "update"
	trackerClose        = "close"
	// The one triage decision that is reported, and it is reported because of what
	// it changes rather than because it is a decision. Every other decision about a
	// stopped run is the development manager doing the job the docket was delivered
	// for, and a channel that narrated each of them would be the event log this
	// exists not to be. A crossing is different in kind: it is the role giving an
	// item more room than the project configured, on authority the operator
	// delegated on condition of hearing about it, and the hearing is the veto.
	trackerTriage = "triage"
	triageCross   = "cross"
	// The Lead Product Manager's grant of a further raise past the development
	// manager's five, reported for the reason a crossing is: the harness carries
	// it out without asking the operator, so he is told afterwards.
	trackerGrant = "grant"
)

// exchangeUnresolved is the outcome an ask exchange records when it reaches the
// round limit it was opened with. It is named here rather than imported for the
// reason the tracker operations above are: what is reported stays independent of
// how the channel writes its own record down.
const exchangeUnresolved = "unresolved-after-rounds"

// FromConversation says what the event at one position of a conversation's log
// did to the backlog, and says nothing at all — the zero notification — for the
// events that did nothing to it, which is most of them.
//
// It is given the whole log rather than the one event because one milestone
// needs an earlier record to be said in full: the creation of an approved
// proposal names the item but not what the item is for, and the goal is on the
// proposal the operator approved. Reading backwards for it is exact rather than
// a guess, since a creation is only ever recorded after the proposal it came
// from.
func FromConversation(conversation runstate.Conversation, events []execution.Event, index int) (Notification, error) {
	if index < 0 || index >= len(events) {
		return Notification{}, fmt.Errorf("conversation %s has no event at position %d", conversation.ConversationID, index)
	}
	event := events[index]
	switch event.Type {
	case execution.EventTrackerActionApplied:
		// The earlier records are read for the same reason a creation reads them:
		// one milestone is about where in a sequence the action falls rather than
		// about the action itself. A role's first act on work handed to it is that
		// role picking it up, and its second is that role working — and only the log
		// says which this was.
		return fromTrackerAction(conversation, events[:index], event)
	case execution.EventTrackerBlockRefused:
		// The one tracker event here that is about actions nobody carried out. It is
		// said for the reason the applied ones are not enough: a block the harness
		// would not read produces no per-action record at all, so without this the
		// channel reports a queue that did not move as a queue nobody touched.
		return fromRefusedTrackerBlock(conversation, event)
	case execution.EventTrackerRefusalUnresolved:
		// And the same news once the harness has spent its one attempt at having the
		// block re-issued. It is said separately because the refusal above is now
		// only half the story: what a reader needs is that the correction was tried
		// and did not take, which is the difference between waiting and acting.
		return fromUnresolvedTrackerRefusal(conversation, event)
	case execution.EventProposalCreated:
		// The approval that precedes this is deliberately not reported beside it.
		// They are one decision, and the creation is the half that names the item
		// the thread belongs to; saying both would say one decision twice, in two
		// threads. What is lost is an approval whose creation then failed, which
		// the operator is told about in the conversation they made it in.
		return fromApprovedWork(conversation, events[:index], event)
	case execution.EventProposalRejected:
		return fromDeclinedWork(conversation, event)
	case execution.EventExchangeRound, execution.EventExchangeClosed:
		// An ask exchange is the second thing a conversation does that an operator
		// cannot otherwise see. It is addressed to the exchange rather than to the
		// product or to a work item, so a thread of its own carries it and the
		// operator can follow one conversation between two roles without reading
		// past everything else the asking conversation did.
		return fromExchange(conversation, event)
	default:
		return Notification{}, nil
	}
}

// exchangeRound is the part of a recorded ask round this reads. It is a narrow
// local shape for the reason the tracker action's is: what is read here is a
// durable record that outlives the code that wrote it.
type exchangeRound struct {
	Exchange string  `json:"exchange"`
	Asked    string  `json:"asked"`
	Round    int     `json:"round"`
	Rounds   int     `json:"rounds"`
	Outcome  string  `json:"outcome"`
	CostUSD  float64 `json:"cost_usd"`
	Question string  `json:"question"`
	Text     string  `json:"text"`
}

// fromExchange says what one round of one role asking another came to.
//
// A round and a closing are separate kinds because they ask different things of
// a reader, and they are said in different voices for the same reason. A round
// carries the answering role's own words, so that role speaks it: agent-authored
// text is posted as its author wrote it, and putting an architect's judgement in
// the product manager's voice would attribute an opinion to a persona that did
// not hold it. A closing is the harness saying an exchange is over, which is
// nobody's opinion at all. The closing that settled nothing is the one worth
// interrupting for, so it alone is said at warning severity.
func fromExchange(conversation runstate.Conversation, event execution.Event) (Notification, error) {
	var recorded exchangeRound
	if err := json.Unmarshal(event.Payload, &recorded); err != nil {
		return Notification{}, fmt.Errorf("read an ask exchange recorded in %s: %w", conversation.ConversationID, err)
	}
	topic, err := Exchange(recorded.Exchange)
	if err != nil {
		return Notification{}, fmt.Errorf("address the ask exchange recorded in %s: %w", conversation.ConversationID, err)
	}
	answering := domain.AgentRole(strings.TrimSpace(recorded.Asked))
	if !answering.Valid() {
		return Notification{}, fmt.Errorf("the ask exchange recorded in %s names %q as the role that answered", conversation.ConversationID, recorded.Asked)
	}
	kind := KindExchangeTurn
	speaker := Persona(answering, "")
	severity := report.SeverityNote
	detail := Detail{
		Round:  recorded.Round,
		Rounds: recorded.Rounds,
	}
	text := strings.TrimSpace(recorded.Text)
	if event.Type == execution.EventExchangeClosed {
		kind = KindExchangeClosed
		speaker = Harness()
		// What an exchange left unsettled is what the voice reads to say it closed
		// unresolved; an exchange that settled leaves it empty, which is the
		// ordinary ending.
		if recorded.Outcome == exchangeUnresolved {
			severity = report.SeverityWarning
			detail.Unresolved = strings.TrimSpace(recorded.Question)
		}
		text = ""
	}
	return Notification{
		Topic:   topic,
		Speaker: speaker,
		Event: Event{
			Kind:     kind,
			At:       event.Timestamp,
			Severity: severity,
			Refs: Refs{
				ConversationID: conversation.ConversationID,
				ExchangeID:     recorded.Exchange,
			},
			Text:   text,
			Detail: detail,
		},
	}, nil
}

// refusedTrackerBlock is the part of a recorded refusal this reads. It is a
// narrow local shape for the reason the tracker action's below is: what is read
// here is a durable record that outlives the code that wrote it.
type refusedTrackerBlock struct {
	Role string `json:"role"`
	// Actions is how many the block asked for, and is zero where the harness could
	// not count them: a payload it never decoded says nothing about how much was
	// in it. It is read as an absence rather than as none, which is why what is
	// carried on is negative where this is zero.
	Actions int    `json:"actions"`
	Problem string `json:"problem"`
}

// fromRefusedTrackerBlock says that a role asked the tracker for several things
// and got none of them.
//
// It is addressed to the product rather than to any item, because a refused
// block names no item that changed: the actions were refused together, and which
// items they were about is in the reply nobody kept. The harness speaks it for
// the reason it speaks a refused directive — the refusal is the harness's own act
// — so the role that asked is carried in the message instead of narrating it,
// which is the whole of what says whose work was lost.
//
// It is a warning rather than a note. Nobody chose it, several changes an
// operator may be expecting did not happen, and until the role's next turn the
// only sign of it is a queue that quietly did not move.
func fromRefusedTrackerBlock(conversation runstate.Conversation, event execution.Event) (Notification, error) {
	var recorded refusedTrackerBlock
	if err := json.Unmarshal(event.Payload, &recorded); err != nil {
		return Notification{}, fmt.Errorf("read a refused tracker block recorded in %s: %w", conversation.ConversationID, err)
	}
	// The role the record names is preferred over the conversation's own, because
	// the record is what was true when the block was refused; the conversation's
	// role is what it is now, and a record written without one has nothing else to
	// fall back on.
	asking := domain.AgentRole(strings.TrimSpace(recorded.Role))
	if !asking.Valid() {
		asking = conversation.Role
	}
	// A count the record did not carry is stated as absent rather than read as no
	// actions at all, exactly as a priority the record omitted is.
	refused := recorded.Actions
	if refused <= 0 {
		refused = -1
	}
	return Notification{
		Topic:   Product(),
		Speaker: Harness(),
		Event: Event{
			Kind:     KindTrackerBlockRefused,
			At:       event.Timestamp,
			Severity: report.SeverityWarning,
			Refs:     Refs{ConversationID: conversation.ConversationID},
			Detail: Detail{
				Refused:  refused,
				Asking:   asking.Title(),
				Reason:   strings.TrimSpace(recorded.Problem),
				Priority: -1,
			},
		},
	}, nil
}

// unresolvedTrackerRefusal is the part of a recorded unresolved refusal this
// reads, kept narrow for the reason the refusal's own shape above is: what is
// read here is a durable record that outlives the code that wrote it.
type unresolvedTrackerRefusal struct {
	Role    string `json:"role"`
	Actions int    `json:"actions"`
	Problem string `json:"problem"`
	// Previous is the refusal this one failed to answer, and Woken says the
	// harness had started a turn for it. Together they are what makes this
	// different news from a second unrelated refusal: the correction was
	// attempted, by the harness, and it did not take.
	Previous string `json:"previous"`
	Woken    bool   `json:"woken"`
	// HandedBack says the harness handed the refusal back to the role as a further
	// round of the same message, which is the correction attempted before any
	// wakeup was owed. A record written before that existed carries none.
	HandedBack bool `json:"handed_back"`
	// RefusedAgain says the role sent a block back and that block was refused too,
	// as against a turn that sent none at all. Both leave the actions exactly as
	// lost and both end the trying, which is why they are one record; they are told
	// apart because one is a role getting an action wrong twice and the other is a
	// role that stopped issuing it, and only the second means nothing was refused
	// on the turn this record is about.
	RefusedAgain bool `json:"refused_again"`
	// Attempts and NeverTaken are the fourth way in: every wakeup the harness made
	// for the refusal met a provider that never took the turn, and NeverTaken is
	// what the last one met. Neither the role nor its block is at fault on this
	// path, and a reader told so knows the fix is the provider or its login.
	Attempts   int    `json:"attempts"`
	NeverTaken string `json:"never_taken"`
}

// fromUnresolvedTrackerRefusal says that a role lost a block of tracker actions
// and the turn after it did not put them back.
//
// It is critical where the refusal it follows is a warning, and the step between
// them is what earns it: a refusal on its own is a loss the harness is about to
// try to repair by itself, and this is the same loss with the repair spent. What
// is left needs the operator, which is the whole of what the severity says.
//
// It is addressed to the product for the reason the refusal is: the actions were
// refused together and which items they were about is in a reply nobody kept.
func fromUnresolvedTrackerRefusal(conversation runstate.Conversation, event execution.Event) (Notification, error) {
	var recorded unresolvedTrackerRefusal
	if err := json.Unmarshal(event.Payload, &recorded); err != nil {
		return Notification{}, fmt.Errorf("read an unresolved tracker refusal recorded in %s: %w", conversation.ConversationID, err)
	}
	asking := domain.AgentRole(strings.TrimSpace(recorded.Role))
	if !asking.Valid() {
		asking = conversation.Role
	}
	refused := recorded.Actions
	if refused <= 0 {
		refused = -1
	}
	return Notification{
		Topic:   Product(),
		Speaker: Harness(),
		Event: Event{
			Kind:     KindTrackerRefusalUnresolved,
			At:       event.Timestamp,
			Severity: report.SeverityCritical,
			Refs:     Refs{ConversationID: conversation.ConversationID},
			Detail: Detail{
				Refused: refused,
				Asking:  asking.Title(),
				Reason:  strings.TrimSpace(recorded.Problem),
				// What the harness already did about it, said as the cause a reader is
				// owed: a refusal it woke the conversation for and one it never reached
				// are the same ending by two different routes, and only the record can
				// say which this was.
				Cause:    unansweredRefusalCause(recorded),
				Priority: -1,
			},
		},
	}, nil
}

// unansweredRefusalCause says what the harness had already done when this
// refusal arrived, and whether the block came back with the same defect.
//
// Both halves are load-bearing, because there are two ways into this record and
// they are not the same news. One is the turn the harness itself started being
// refused, which is the self-correction spent. The other is a second block
// refused with the first still unanswered and no wakeup ever made — two
// unreadable blocks in a conversation somebody was driving by hand, before any
// pass looked. A message that claimed the harness had woken the role would be
// wrong on that path, and it is the path the record is most likely to take on a
// busy morning.
//
// There is a third way in, and it is the quietest: a turn the harness woke that
// answered in prose and asked for no tracker action at all. Nothing was refused
// on it, so a message written around a second refusal would describe something
// that did not happen — and this is exactly the ending that would otherwise reach
// nobody, since the wakeup is spent and no second refusal is coming.
//
// A fourth needs no turn at all: every wakeup the harness made met a provider
// that never took it — down, out of capacity, or a lapsed login — until the
// attempts ran out. That one says the provider was what failed, because the
// thing to fix is the provider or its login rather than anything the role wrote;
// what the provider said is on the durable event and on the product's own
// outage record, so it is not quoted a second time here.
//
// It compares the two refusals rather than quoting the earlier one. The earlier
// refusal's words are on the durable event, and a channel line carrying two
// error messages is one nobody reads to the end; what a reader needs from it is
// whether the same thing went wrong twice, which is the difference between a
// role that cannot get one action right and one making a fresh mistake.
func unansweredRefusalCause(recorded unresolvedTrackerRefusal) string {
	if strings.TrimSpace(recorded.NeverTaken) != "" {
		return fmt.Sprintf("the harness tried %d times to wake this conversation to re-issue them and the provider never took the turn",
			recorded.Attempts)
	}
	if !recorded.RefusedAgain {
		if recorded.Woken {
			return "the harness woke this conversation to re-issue them and the turn it took asked for no tracker action at all"
		}
		if recorded.HandedBack {
			return "the harness handed the refusal back within the same message and the round it took asked for no tracker action at all"
		}
		return "the turn after the refusal asked for no tracker action at all"
	}
	previous := strings.TrimSpace(recorded.Previous)
	repeated := previous != "" && previous == strings.TrimSpace(recorded.Problem)
	switch {
	case recorded.Woken && repeated:
		return "the harness woke this conversation to correct the refusal before it and got the same refusal back"
	case recorded.Woken:
		return "the harness woke this conversation to correct the refusal before it, and the block it sent back was refused too"
	case recorded.HandedBack && repeated:
		return "the harness handed the refusal back within the same message and got the same refusal back"
	case recorded.HandedBack:
		return "the harness handed the refusal back within the same message, and the block it sent back was refused too"
	case repeated:
		return "the refusal before this one was never answered, and this block earned exactly the same one"
	default:
		return "the refusal before this one was never answered by any turn"
	}
}

// trackerAction is the part of a recorded tracker outcome this reads. It is a
// narrow local shape rather than the conversation's own, because what is read
// here is a durable record that outlives the code that wrote it: a field the
// record does not carry is absent rather than an error, which is what lets an
// event recorded by an older harness still be said.
type trackerAction struct {
	Action struct {
		Action   string  `json:"action"`
		ID       string  `json:"id"`
		Title    string  `json:"title"`
		Goal     string  `json:"goal"`
		Parent   *string `json:"parent"`
		Priority *int    `json:"priority"`
		// Executor is a pointer for the reason the parent and the priority are: an
		// action that said nothing about what carries the item and one that said it
		// is carried by nothing are opposite requests, and a plain string reads them
		// the same. The tracker cannot express the second today — an empty executor
		// on an update means "leave it alone" all the way down to the beads client —
		// so what this buys is that the day it can, handing work back to the run
		// queue is not narrated as a role taking it up.
		Executor *string `json:"executor"`
		// Decision and Budget are the triage decision and the cap a crossing names.
		// They are read for the one decision that is reported and ignored for the
		// rest, exactly as every other field here is read only by the actions that
		// carry it.
		Decision string `json:"decision"`
		Budget   string `json:"budget"`
		Reason   string `json:"reason"`
	} `json:"action"`
	// Crossing is what the store said a delegated crossing came to, recorded beside
	// the action rather than in it: the ceiling and the count are the guard's own
	// figures, and the whole value of the message is that the operator is told what
	// was actually permitted rather than what was asked for. A record that carries
	// none is one written before crossings existed, or an action that is not one.
	Crossing *struct {
		Budget    string `json:"budget"`
		Cap       int    `json:"cap"`
		Crossing  int    `json:"crossing"`
		Crossings int    `json:"crossings"`
		// CostSoFar is what the item had cost when a grant was recorded, carried
		// on the Lead Product Manager's grant alone.
		CostSoFar *struct {
			TotalUSD    float64 `json:"total_usd"`
			Runs        int     `json:"runs"`
			UnknownRuns int     `json:"unknown_runs"`
		} `json:"cost_so_far"`
	} `json:"crossing"`
	WorkItemID string `json:"work_item_id"`
	// WorkItemTitle is what the tracker called the item as the action ran. It is
	// what names the topic for every action that carries no title of its own —
	// a reordering, an attribution — and a record written before it was carried
	// leaves the topic addressed exactly as it was before there was one.
	WorkItemTitle string `json:"work_item_title"`
	// WorkItemExecutor is what the tracker said carried the item as the action
	// ran, before the action changed anything. A record written before it was
	// carried says nothing, which reads as ordinary work — the same thing an
	// absent marker has always meant.
	WorkItemExecutor string `json:"work_item_executor"`
}

// handsOff is the executor this action gives the item, and is empty for the
// actions that say nothing about what carries the work, which is nearly all of
// them.
func (a trackerAction) handsOff() string {
	if a.Action.Executor == nil {
		return ""
	}
	return strings.TrimSpace(*a.Action.Executor)
}

// saysWhatCarriesIt reports an action that spoke about the item's executor at
// all, whatever it said. It is what keeps an action that took the marker off an
// item out of the pickup below: nothing reports work returning to the run queue
// yet, and saying nothing about it is a gap, where narrating it as a role
// starting the work would be the opposite of what happened.
func (a trackerAction) saysWhatCarriesIt() bool { return a.Action.Executor != nil }

// carriedBy is what the tracker already said carried the item when the action
// ran, and is empty for ordinary work a developer run carries.
func (a trackerAction) carriedBy() string { return strings.TrimSpace(a.WorkItemExecutor) }

// carriesTheWork reports an action that is a role doing work handed to it,
// rather than the queue being kept around that work. Recording what was done on
// the item and closing it are the whole of what carrying work in a conversation
// looks like from the tracker's side; the goal an item serves, where it sits in
// the order, what it depends on, and what it hangs under are all still the queue,
// and they are as much the queue on conversation work as on any other.
//
// It is deliberately narrower than "the action changed something". A reordering
// is not somebody picking work up, and reading it as one would both announce a
// pickup nobody performed and swallow the reordering that did happen.
func (a trackerAction) carriesTheWork() bool {
	switch a.Action.Action {
	case trackerUpdate, trackerClose:
		return true
	default:
		return false
	}
}

// fromTrackerAction says what one applied action did to the queue. Only actions
// the record says were applied reach here, so nothing said is an intention: an
// action that failed is recorded as a failure and is never readable as a change.
func fromTrackerAction(conversation runstate.Conversation, earlier []execution.Event, event execution.Event) (Notification, error) {
	var recorded trackerAction
	if err := json.Unmarshal(event.Payload, &recorded); err != nil {
		return Notification{}, fmt.Errorf("read a tracker action recorded in %s: %w", conversation.ConversationID, err)
	}
	// A priority the record did not carry is stated as absent rather than read as
	// the top of the queue, because zero is the highest priority there is.
	//
	// The reason a role gave the tracker is written for the item's record, where
	// it is read in full by somebody deciding whether the change was right. The
	// channel takes the first sentence of it and says where the rest is: a
	// paragraph justifying a reordering, under the one line saying where the item
	// went, is what a reader scrolls past — and the record loses nothing, because
	// nothing here is what holds it.
	detail := Detail{
		Title:    strings.TrimSpace(recorded.Action.Title),
		Goal:     strings.TrimSpace(recorded.Action.Goal),
		Reason:   oneSentence(recorded.Action.Reason),
		Priority: -1,
	}
	var kind Kind
	// A note unless something says otherwise, which is what every account of the
	// queue moving is: it happened, and nobody has to do anything about it.
	severity := report.SeverityNote
	switch {
	// Marking work already in the backlog with an executor is the handoff itself:
	// the item stops being a run's to carry, and from here nothing happens to it
	// until a role opens a conversation about it. A creation that names one is not
	// this — it is an admission that happens to be conversation work, and the
	// admission is where a thread learns what the item is for, so it stays an
	// admission with what carries it named beside it.
	case recorded.Action.Action != trackerCreate && recorded.handsOff() != "":
		kind = KindWorkHandedOff
		detail.Executor = recorded.handsOff()
	// Closing work a conversation carries is the only account there is of that
	// work finishing. It comes before the pickup below because a role that did the
	// work and then closed the item in one turn has done both, and what a reader
	// needs from a single message is the ending.
	case recorded.carriedBy() != "" && recorded.Action.Action == trackerClose:
		kind = KindWorkCarriedOut
		detail.Executor = recorded.carriedBy()
	// The first thing a conversation does to carry work that was handed to a
	// conversation is that role starting it. It is read from this conversation's
	// own log, so a second role taking the same item up later is a second pickup
	// rather than silence — which is the honest reading, since it is a second role
	// starting on it.
	//
	// What it is not is any first act on the item. Attributing a goal and
	// reordering the queue are below this and stay themselves, because they are
	// what they are whoever carries the item, and an arm here that swallowed them
	// would report a pickup that did not happen instead of the change that did.
	case recorded.carriedBy() != "" && recorded.carriesTheWork() && !recorded.saysWhatCarriesIt() &&
		!actedOn(earlier, recorded.WorkItemID):
		kind = KindWorkPickedUp
		detail.Executor = recorded.carriedBy()
	case recorded.Action.Action == trackerCreate:
		// Admitting work and decomposing it are two acts, and which one this was
		// follows from who did it: the product manager owns what is admitted to the
		// backlog, and every other role that may create at all creates underneath
		// something already admitted. Recording a decomposition as an admission
		// would say a role admitted work it has no authority to admit, which is the
		// one thing an account of the queue must never say.
		kind = KindItemDecomposed
		if conversation.Role == domain.RoleProductManager {
			kind = KindItemAdmitted
		}
		if recorded.Action.Parent != nil {
			detail.Parent = strings.TrimSpace(*recorded.Action.Parent)
		}
		detail.Executor = recorded.handsOff()
	case recorded.Action.Action == trackerAttribute:
		kind = KindItemAttributed
		detail.Executor = recorded.carriedBy()
	case recorded.Action.Action == trackerReprioritize:
		kind = KindItemReprioritized
		if recorded.Action.Priority != nil {
			detail.Priority = *recorded.Action.Priority
		}
		detail.Executor = recorded.carriedBy()
	// A cap crossed on delegated authority. It is a warning rather than a note,
	// and that is the delegation's own condition rather than an opinion about how
	// serious it is: the crossing is in force from the moment it was recorded, so
	// the only thing standing between it and an item given room nobody wanted it to
	// have is that the operator sees it while there is still something to undo. A
	// note is marked with nothing, and something read tomorrow is not a veto.
	//
	// A crossing whose figures the record does not carry is said all the same,
	// stating the absences. What the operator most needs to know is that a cap
	// moved, and a message withheld for want of a count would be the delegation
	// silently losing its condition to a schema change.
	case recorded.Action.Action == trackerTriage && strings.TrimSpace(recorded.Action.Decision) == triageCross:
		kind = KindCapCrossed
		severity = report.SeverityWarning
		detail.Budget = strings.TrimSpace(recorded.Action.Budget)
		if crossing := recorded.Crossing; crossing != nil {
			// The store's own figures win over the action's, because the action said
			// which cap to cross and the store said what crossing it actually was and
			// what the ceiling now is.
			detail.Budget = strings.TrimSpace(crossing.Budget)
			detail.Cap = crossing.Cap
			detail.Crossing = crossing.Crossing
			detail.Crossings = crossing.Crossings
		}
	case recorded.Action.Action == trackerGrant:
		kind = KindCapGranted
		severity = report.SeverityWarning
		detail.Budget = strings.TrimSpace(recorded.Action.Budget)
		if granted := recorded.Crossing; granted != nil {
			detail.Budget = strings.TrimSpace(granted.Budget)
			detail.Cap = granted.Cap
			detail.Crossing = granted.Crossing
			detail.Crossings = granted.Crossings
			if cost := granted.CostSoFar; cost != nil {
				detail.CostSoFar = runstate.TriageGrantCost{
					TotalUSD: cost.TotalUSD, Runs: cost.Runs, UnknownRuns: cost.UnknownRuns,
				}.Text()
			}
		}
	default:
		return Notification{}, nil
	}
	// An action that names no title of its own is named by the item the record
	// says it acted on. It is the same fallback for the thread's header and for
	// what the message says, because they are the same question: an item's first
	// appearance in the channel is as often a reordering as an admission, and a
	// role picking up work names no title at all — a thread headed by a bare
	// identifier, or a sentence saying the item has no name, would be the record
	// being read less carefully than it was written.
	detail.Title = namedItem(detail.Title, recorded.WorkItemTitle)
	topic, err := topicForItem(recorded.WorkItemID)
	if err != nil {
		return Notification{}, fmt.Errorf("address the %s recorded in %s: %w", kind, conversation.ConversationID, err)
	}
	return Notification{
		// An admission is usually the first thing said about an item, so this is
		// where most threads get the name their header carries.
		Topic: topic.WithTitle(detail.Title),
		// The role's own act, in its own voice: what is admitted, decomposed,
		// attributed, or reordered is a judgment the role made, and the harness
		// only carried it out on the role's behalf.
		Speaker: Persona(conversation.Role, conversation.Agent),
		Event: Event{
			Kind:     kind,
			At:       event.Timestamp,
			Severity: severity,
			Refs: Refs{
				ConversationID: conversation.ConversationID,
				WorkItemID:     strings.TrimSpace(recorded.WorkItemID),
			},
			Detail: detail,
		},
	}, nil
}

// namedItem is what to call the item an action was about: what the action itself
// named, and otherwise what the tracker called the item when the action ran. The
// action comes first because it is the more specific of the two — a creation
// names the item it is admitting, and the reading beside it is of the item as it
// already was.
func namedItem(named, recorded string) string {
	if strings.TrimSpace(named) != "" {
		return named
	}
	return strings.TrimSpace(recorded)
}

// actedOn reports this conversation having already carried one item's work
// earlier in its own log. It is what makes a pickup the first such act rather
// than every act: a role carrying work in conversation writes on the item
// repeatedly, and a thread that said it had been picked up each time would say
// the one thing that matters so often it stopped meaning anything.
//
// It asks the same narrow question the pickup does, so the queue being kept
// around an item never consumes its pickup: a conversation that reorders an item
// and then starts working on it has started working on it, and a reading that
// counted the reordering would leave the thread's most important message unsaid
// on the grounds that something unrelated happened first.
//
// A record it cannot read is not an earlier act. Skipping it would be the safe
// direction for a message nobody wants twice, and this is the opposite case: the
// pickup is the message the thread is missing, and losing it to one unreadable
// record is worse than saying it once more than necessary.
func actedOn(earlier []execution.Event, workItemID string) bool {
	wanted := strings.TrimSpace(workItemID)
	if wanted == "" {
		return false
	}
	for _, event := range earlier {
		if event.Type != execution.EventTrackerActionApplied {
			continue
		}
		var recorded trackerAction
		if err := json.Unmarshal(event.Payload, &recorded); err != nil {
			continue
		}
		if strings.TrimSpace(recorded.WorkItemID) == wanted && recorded.carriesTheWork() {
			return true
		}
	}
	return false
}

// approvedWork is the part of a created item's record this reads.
type approvedWork struct {
	ProposalID string `json:"proposal_id"`
	WorkItemID string `json:"work_item_id"`
	Title      string `json:"title"`
	Parent     string `json:"parent"`
}

// proposedWork is the part of a proposal's record this reads. It is the shape
// both the proposal as recorded and the operator's refusal of it are written in,
// because a refusal keeps the whole proposal beside the reason.
type proposedWork struct {
	ID       string `json:"id"`
	Proposal struct {
		Title string `json:"title"`
		Goal  string `json:"goal"`
	} `json:"proposal"`
	Reason string `json:"reason"`
}

// fromApprovedWork says that work an agent proposed was approved and is now in
// the backlog. The harness speaks: the judgment was the operator's, and the
// operator is not a persona, so no persona narrates a decision it did not make.
func fromApprovedWork(conversation runstate.Conversation, earlier []execution.Event, event execution.Event) (Notification, error) {
	var created approvedWork
	if err := json.Unmarshal(event.Payload, &created); err != nil {
		return Notification{}, fmt.Errorf("read the work item created in %s: %w", conversation.ConversationID, err)
	}
	topic, err := topicForItem(created.WorkItemID)
	if err != nil {
		return Notification{}, fmt.Errorf("address the work item created in %s: %w", conversation.ConversationID, err)
	}
	return Notification{
		Topic:   topic.WithTitle(created.Title),
		Speaker: Harness(),
		Event: Event{
			Kind:     KindWorkApproved,
			At:       event.Timestamp,
			Severity: report.SeverityNote,
			Refs: Refs{
				ConversationID: conversation.ConversationID,
				WorkItemID:     strings.TrimSpace(created.WorkItemID),
			},
			Detail: Detail{
				Title:    strings.TrimSpace(created.Title),
				Goal:     goalProposed(earlier, created.ProposalID),
				Parent:   strings.TrimSpace(created.Parent),
				Priority: -1,
			},
		},
	}, nil
}

// fromDeclinedWork says that work an agent proposed was turned down. It is
// addressed to the product rather than to any item, because there is no item:
// nothing was created, which is the whole of what a decline means.
//
// That is also why reaches classifies the kind ReachRecord outright rather than
// leaving it to the no-thread rule: this is the one producer that addresses the
// product every time, so "its thread" is never an answer for it, and a silence
// nobody chose is how a reader comes to be told one thing while another happens.
func fromDeclinedWork(conversation runstate.Conversation, event execution.Event) (Notification, error) {
	var declined proposedWork
	if err := json.Unmarshal(event.Payload, &declined); err != nil {
		return Notification{}, fmt.Errorf("read the proposal declined in %s: %w", conversation.ConversationID, err)
	}
	return Notification{
		Topic:   Product(),
		Speaker: Harness(),
		Event: Event{
			Kind:     KindWorkDeclined,
			At:       event.Timestamp,
			Severity: report.SeverityNote,
			Refs:     Refs{ConversationID: conversation.ConversationID},
			Detail: Detail{
				Title:    strings.TrimSpace(declined.Proposal.Title),
				Goal:     strings.TrimSpace(declined.Proposal.Goal),
				Reason:   strings.TrimSpace(declined.Reason),
				Priority: -1,
			},
		},
	}, nil
}

// goalProposed is what the proposal an item was created from said the work was
// for. It is read from the record of the proposal itself, which is where the
// goal is: the creation names the item and the proposal names the intent, and an
// admission that could not say what the work serves would be exactly the item
// nobody can later decide to stop doing.
//
// A proposal the log does not hold leaves the goal absent, which the voice
// states as an absence rather than rendering as a blank.
func goalProposed(earlier []execution.Event, proposalID string) string {
	wanted := strings.TrimSpace(proposalID)
	if wanted == "" {
		return ""
	}
	for index := len(earlier) - 1; index >= 0; index-- {
		event := earlier[index]
		switch event.Type {
		case execution.EventProposalApproved, execution.EventProposalRecorded:
		default:
			continue
		}
		var proposed proposedWork
		if err := json.Unmarshal(event.Payload, &proposed); err != nil {
			continue
		}
		if strings.TrimSpace(proposed.ID) == wanted {
			return strings.TrimSpace(proposed.Proposal.Goal)
		}
	}
	return ""
}
