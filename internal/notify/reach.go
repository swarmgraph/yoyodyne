package notify

// How far one message is carried.
//
// Every kind in the vocabulary is worth recording, and that is not the same
// question as whether it is worth putting in front of somebody who has opened
// nothing. The operator's rule is that the channel level carries what is
// important — something that matters is broken or has materially changed — or
// what needs his action or decision, and that a message which is neither does
// not post at all: it lands in the durable record, and the regular summaries are
// built from there.
//
// The measurement that produced the rule is what makes it concrete. Of 2,250
// posts, roughly three quarters were per-event progress narration — a report
// pushed on its own, a poll that started nothing, a session starting, checks
// passing, a review approving — against under forty of the kinds somebody
// actually has to act on. The important ones were not hidden; they were drowned,
// which costs exactly as much.
//
// So each kind says how far it goes, once, here. It is beside the vocabulary
// rather than in the surface that posts for the reason the status symbols and
// the voice table are: a surface that decided this for itself would be a second
// posting policy nobody ratified, and two of them would disagree about one
// event in front of the one person who would have to adjudicate it.

import (
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/report"
)

// Reach is how far one event is carried.
type Reach string

const (
	// ReachChannel is the channel level: seen by somebody who has opened no
	// threads. It is what is important or what needs the operator, and nothing
	// else.
	ReachChannel Reach = "channel"
	// ReachThread is the topic's own thread, where the narrative is. A reader
	// following one item sees it; a reader scanning the channel does not.
	ReachThread Reach = "thread"
	// ReachRecord is no further than the durable record the event was read from.
	// It is not posted anywhere, because there is nowhere it would be worth
	// putting: the record holds it, `yoyo status` reads it, and the summaries are
	// built from it.
	ReachRecord Reach = "record"
)

// Valid reports whether a name is one of the three.
func (r Reach) Valid() bool {
	switch r {
	case ReachChannel, ReachThread, ReachRecord:
		return true
	default:
		return false
	}
}

// reaches is how far each kind goes. Every kind in the vocabulary has an entry,
// and a kind added without one reaches nothing rather than defaulting into the
// channel: the failure that asked for this table was noise, so the way to be
// wrong here is to be quiet and be corrected, not to be loud and be muted.
var reaches = map[Kind]Reach{
	// The backlog moving is the harness's steering wheel turning, and it is
	// narration: the item it concerns has a thread, the queue's own order is what
	// the tracker holds, and nobody acts on an admission as it happens.
	KindItemAdmitted:      ReachThread,
	KindItemDecomposed:    ReachThread,
	KindItemAttributed:    ReachThread,
	KindItemReprioritized: ReachThread,
	// A block of tracker actions the harness would not read is the opposite:
	// several admissions or closes did not happen and the role that asked believes
	// they did, so the queue is not what anybody thinks it is until somebody looks.
	KindTrackerBlockRefused: ReachChannel,
	// And louder still once the harness has tried: a refusal it woke a role to
	// correct and got another refused block from is one nothing else in the
	// harness will fix. It is the operator's, which is the definition of the
	// channel level.
	KindTrackerRefusalUnresolved: ReachChannel,
	// What the operator decided about proposed work. He decided it, so neither is
	// news to him. An approval created an item and belongs in that item's thread,
	// where the rest of its narrative will be.
	KindWorkApproved: ReachThread,
	// A decline is the one kind here that is addressed to the product every time
	// rather than sometimes: nothing was created, so there is no item and there
	// will never be a thread — see fromDeclinedWork, which says so. That makes it
	// the one kind whose answer cannot be "its thread", so it is stated here
	// rather than left to fall out of the no-thread rule in reachOf. The record
	// holds it, `yoyo` reads it back, and what the channel would gain is a message
	// about work that does not exist, telling the operator what he just decided.
	KindWorkDeclined: ReachRecord,
	// Work a conversation carries. It waits on a role rather than on the operator,
	// and the wait is legible in the item's thread where the handoff was said.
	KindWorkHandedOff:  ReachThread,
	KindWorkPickedUp:   ReachThread,
	KindWorkCarriedOut: ReachThread,
	// A cap the development manager crossed on his own authority. The delegation
	// was made on the condition that the operator reads each crossing as it
	// happens, so that a veto by reading is a veto at all: a crossing carried only
	// to the item's thread is one he learns about from the work it bought.
	KindCapCrossed: ReachChannel,
	// One run's own arc, which is the largest block of narration there is. A run
	// starting, its checks passing or failing, a verdict, a promotion, a
	// publication and its merge are the thread's whole story and none of them is a
	// move of the operator's: a failing check is the developer's next attempt, a
	// refused path is the developer's next attempt with the path taken back out,
	// and repairs asked for are the developer's too.
	KindRunStarted:     ReachThread,
	KindRunContinued:   ReachThread,
	KindChecksPassed:   ReachThread,
	KindChecksFailed:   ReachThread,
	KindPathRefused:    ReachThread,
	KindReviewApproved: ReachThread,
	KindReviewRepairs:  ReachThread,
	KindRaceLost:       ReachThread,
	KindPromoted:       ReachThread,
	KindPublished:      ReachThread,
	KindMergeQueued:    ReachThread,
	KindMergeCompleted: ReachThread,
	// A merge that is not going to happen is the one publication fact nobody finds
	// out about on their own: the change is promoted, the item reads as landed, and
	// the request waits on the development manager's decision while reading as done.
	KindMergeDropped: ReachChannel,
	// A merge waiting on its target's red check reaches the channel for the same
	// reason: the change reads as done and the publication is not landing.
	KindMergeWaitingOnTarget: ReachChannel,
	// A landing that went red is the target branch broken by a change every
	// gate passed, which is the other publication fact nobody finds out about on
	// their own; one that went green is the thread's own story. One the checks
	// could not run over is neither, and reaches the channel because a landing
	// nobody verified reads as green to anybody who was not told.
	KindLandingGreen:      ReachThread,
	KindLandingRed:        ReachChannel,
	KindLandingUnverified: ReachChannel,
	// Work that stopped. A park waits on something outside the run, a blocker is
	// a decision somebody has to make and moves nothing until it is made, and a
	// run that ended without succeeding has materially changed what exists. A
	// blocker the environment caused and a role or the harness moves next is the
	// exception, answered from the event in reachOf.
	KindRunParked:       ReachChannel,
	KindBlockerRecorded: ReachChannel,
	KindRunEnded:        ReachChannel,
	// Capacity that ran out somewhere that is not a run: hours in which nothing
	// will happen, which look exactly like a healthy quiet queue.
	KindUsageLimitExhausted: ReachChannel,
	// Capacity that ran out and was worked around. It reaches the channel beside
	// the refusal above rather than below it, because what an operator has to be
	// able to see is that an agent is answering on a model they permitted rather
	// than the one they configured — and a substitution nobody is told about is a
	// change to what the work was produced by that leaves no trace anybody reads.
	KindModelSubstituted: ReachChannel,
	// What an agent said in its own words. A report is the single largest source of
	// individual pushes and asks for nothing by design, so it lives in its item's
	// thread and is carried to the channel by the summaries built from the report
	// store — except a critical one, which reaches the channel through the
	// promotion below because something already wrong is exactly what the operator
	// wants told.
	//
	// The other three are all waiting on somebody to decide, and the exchange is
	// both halves of one wait: nextMoves answers "the operator's, until the
	// exchange is answered" for a turn, and the product manager's line for it says
	// the operator is being asked something. A question shown only inside a thread
	// and an answer shown at the top of the channel would be the two ends of one
	// ask surfaced opposite ways round, with the half that needs somebody being
	// the hidden one — which is the silence this whole change is against.
	KindReportFiled:    ReachThread,
	KindProposalRaised: ReachChannel,
	KindExchangeTurn:   ReachChannel,
	KindExchangeClosed: ReachChannel,
	// What a reply in a thread did, and what later became of it. All five are
	// addressed to the person who typed the words being read back to them, and all
	// five reach that person by name wherever they are posted — so the channel
	// level buys them nothing, and the four that answer something belong in the
	// thread that asked. The one exception is a directive that left something
	// unresolved, which pauses the work it affects until somebody settles it: that
	// one is promoted below, from what the record left unsettled rather than from
	// the kind. A withdrawal is the answer the thread's reader asked for, in the
	// communication rule's sense, and nothing about it is anybody's to act on.
	KindDirectiveRecorded:   ReachThread,
	KindDirectiveResolved:   ReachThread,
	KindDirectiveCarriedOut: ReachThread,
	KindDirectiveRefused:    ReachThread,
	KindDirectiveWithdrawn:  ReachThread,
	// A question's receipt is the same shape: addressed by name to whoever asked,
	// in the thread they asked in, with the answer following it there.
	KindQuestionHeard: ReachThread,
	// The operator's two switches, which are about the whole line and are his own
	// to lift.
	KindIntakeHeld:     ReachChannel,
	KindIntakeReleased: ReachChannel,
	KindHoldPlaced:     ReachChannel,
	KindHoldLifted:     ReachChannel,
	// The brake's hold handed to him by the harness at its bound. It is the one
	// message about a brake hold that asks a person for something, and the sink
	// sends it to the operators directly as well as posting it here.
	KindIntakeEscalated: ReachChannel,
	// A finding only the operator can act on is the channel level by the rule's
	// own definition — what needs his action — and it is also said to him
	// directly, which is the surface's to do.
	KindOperatorAction: ReachChannel,
	// What a watch session is doing. These are the poll-by-poll narration of a
	// process that spends most of its life saying nothing, and they were 473 of the
	// measured posts on their own. The watch log holds every one of them and
	// `yoyo status` reads it back. What the channel needs from that log is only
	// what somebody has to act on, and three messages below say exactly that from
	// the same log: the braked session, the waiting line — which names a session
	// that has stopped, once there is work it would have started — and the stall,
	// which is the case where the session died without recording anything at all.
	KindWatchStarted:     ReachRecord,
	KindWatchIdle:        ReachRecord,
	KindWatchResumed:     ReachRecord,
	KindWatchStopped:     ReachRecord,
	KindWatchRedeploying: ReachRecord,
	// A session retrying a read of the store it could not make. The waiting line
	// says the same outage, attributed to the harness, once there is work a
	// stopped line would have started.
	KindWatchReadRetrying: ReachRecord,
	// A braked line has stopped and stays stopped until intake is released.
	KindWatchBraked: ReachChannel,
	// A machine refusing every start needs its cause visible beside the hold.
	KindWatchBlocked: ReachChannel,
	// The three readings of a line that is choosing nothing: the state said again
	// while it stands, the silence nothing accounts for, and the silence the
	// provider accounts for. Every one of them is what somebody scanning a quiet
	// channel is actually checking for, and the last is a note that still belongs
	// at the top because the operator asked that a pause name its cause where he
	// will read it.
	KindLineWaiting:    ReachChannel,
	KindStallNoticed:   ReachChannel,
	KindProviderWindow: ReachChannel,
	// Every role refused at once on a known reset. It is the one silence here that
	// has both a cause and a remedy, and it is the operator's: the window is the
	// provider's, and the configuration that let it hold every role is his.
	KindCapacityHold: ReachChannel,
	// The provider answering nobody, and answering again. The first is the one
	// silence whose remedy is a person and nothing else, and the second is that
	// person being told the line carried on: both belong at the top, where the
	// operator asked that a pause name its cause.
	KindProviderOutage:   ReachChannel,
	KindProviderRestored: ReachChannel,
	// A recurring task failing before its first turn, firing after firing. It is
	// the harness degraded in a way no record but the sweep log says, and what
	// ends it is a fix rather than a clock, so it belongs at the top.
	KindRecurringTaskFailing: ReachChannel,
	// A claim the harness gave back because nothing was working on it. It is the
	// stall's blind spot said from the other end — an item that had left the ready
	// queue under a run that died, with the line idle behind it — and it is said
	// once per release, in the item's thread and at the top, because the harness
	// was degraded for as long as the claim stood and a second run for the item is
	// something a reader has to be able to account for afterwards.
	KindClaimReleased: ReachChannel,
	// A session dispatching work on a binary the harness has moved past. Nothing in
	// the record says it at all, and what it costs is rounds spent against bugs
	// that were fixed hours earlier.
	KindResidentStale: ReachChannel,
	// One value the project's template has improved. It asks for a decision and it
	// is said exactly once per improvement, ever. Several found on one reading are
	// the same fact said together, and go the same distance.
	KindBundleImprovement:  ReachChannel,
	KindBundleImprovements: ReachChannel,
	// What a topic gathered while nothing was posting it. It stands for that
	// topic's narrative, so it belongs where the narrative is — and it is exempted
	// from the no-thread rule in reachOf, because a digest is only ever produced in
	// place of messages that were going to post.
	KindCatchUpDigest: ReachThread,
	// A line of a durable log the sink could not read. It is the harness degraded
	// rather than any item's news — whatever that line recorded is not going to be
	// said, and the file is a person's to look at — and it is said once per line,
	// so the channel level costs one message and buys the operator the one fact
	// no thread would carry.
	KindLogLineSkipped: ReachChannel,
}

// Reach is how far one kind goes on its own, before anything about a particular
// event is taken into account. A kind with no entry reaches the record and no
// further, which is the quiet way to be wrong about a kind nobody has classified.
func (k Kind) Reach() Reach {
	if reach, found := reaches[k]; found {
		return reach
	}
	return ReachRecord
}

// reachOf is how far one event addressed to one topic goes. It is the whole of
// the posting policy, and it is here rather than in a surface so that the sink
// that posts and the notifier that renders cannot come to disagree about one
// message.
//
// Two things move a kind off its own answer, and each is a rule the operator
// stated rather than a refinement of it.
//
// A critical event always reaches the channel, whatever its kind says. Severity
// is importance rather than actionability — his correction, in his words — so
// something already wrong that will cost somebody is told at the level he reads,
// and the kind's own reach decides only where the quieter ones go.
//
// A milestone with no thread to go in reaches the record instead. What is
// addressed to the product is posted unthreaded, which is the channel level: a
// report filed against no work item would otherwise be pushed to the top of the
// channel precisely because there was less to say about it. That is the whole of
// what this rule is for, and it is a rule rather than an entry per kind because
// most of the kinds it covers are addressed to an item every time in practice
// and reach the product topic only from a record that named no item — which has
// no thread and no identifier, so there is nothing in it a reader could follow.
//
// A kind that is addressed to the product every time answers in the table
// instead, so that its silence is a decision somebody took rather than a rule it
// happened to fall through. KindWorkDeclined is the one, and the conformance test
// beside this pins the pair together.
//
// The catch-up digest is exempt, and it is the one thing here that must never be
// swallowed: a digest exists only in place of messages that were going to post,
// so it goes wherever it is addressed. A product-topic digest stands for
// channel-level messages — a held intake, a stalled line — and dropping it would
// take the whole collapsed backlog with it, since the deliveries it replaced are
// suppressed either way.
//
// Two kinds answer from the record rather than from the table. The first, for
// the reason the whose-move clause does: a recorded directive that left
// something unsettled pauses the work it affects until somebody settles it, and
// one that settled nothing is in force already and stops nothing. They are
// opposite news, and the pausing one is what the operator's own survey counts
// among the kinds that must not be drowned.
//
// The second is a stoppage said as a note, which is one the environment caused
// and a role or the harness moves next — a lost race, a usage window, a tracker
// that did not answer. Nothing about it is the operator's, and nothing was
// judged, so it goes in the item's thread with the rest of that item's
// narrative rather than to the top of the channel. The severity is the
// selector's derivation (stoppageSeverity), so reading it here is reading the
// same account rather than classifying the stoppage a second time.
func reachOf(topic Topic, event Event) Reach {
	if event.Severity == report.SeverityCritical {
		return ReachChannel
	}
	reach := event.Kind.Reach()
	if event.Kind == KindDirectiveRecorded && strings.TrimSpace(event.Detail.Unresolved) != "" {
		reach = ReachChannel
	}
	if event.Kind == KindBlockerRecorded && event.Severity == report.SeverityNote {
		reach = ReachThread
	}
	if reach == ReachThread && topic.Kind == TopicProduct {
		// The digest is the exemption, and it says the channel rather than the
		// thread it asked for: a message addressed to the product is posted
		// unthreaded, and unthreaded is the channel level. Leaving it reading
		// "thread" would be the envelope naming a place this message does not go.
		if event.Kind == KindCatchUpDigest {
			return ReachChannel
		}
		return ReachRecord
	}
	return reach
}

// Reach is how far this notification goes, read before anything is rendered so
// the delivery pass can decline to post one at all.
func (n Notification) Reach() Reach { return reachOf(n.Topic, n.Event) }

// Posts reports a notification worth putting somewhere a person reads. It is
// false for the two cases that both end in a cursor advancing and nothing being
// said: selection that found no milestone at all, and an event whose reach is the
// durable record it was already read from.
func (n Notification) Posts() bool {
	return !n.Silent() && n.Reach() != ReachRecord
}
