package notify

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// SchemaVersion versions the envelope rather than any one producer. A new
// producer adds kinds without the envelope, the addressing, or anything reading
// it changing, so this moves only when what an envelope means moves.
const SchemaVersion = 1

// Kind is what happened. The set is the milestones an operator would otherwise
// have to be at a terminal to see, and each transition is said once: a thread is
// a narrative rather than an event log scrolling sideways.
type Kind string

const (
	// The backlog moving, which is the harness's steering wheel turning. An item
	// admitted says what it is for, because work in the queue that does not say
	// what it serves is exactly the work nobody can later decide to stop doing; a
	// decomposition is separate from an admission because they are different acts
	// by different roles, and recording one as the other would say a role admitted
	// work it has no authority to admit.
	KindItemAdmitted      Kind = "backlog.admitted"
	KindItemDecomposed    Kind = "backlog.decomposed"
	KindItemAttributed    Kind = "backlog.attributed"
	KindItemReprioritized Kind = "backlog.reprioritized"
	// A block of tracker actions the harness would not read, and therefore the
	// backlog not moving when a role meant it to. It is here beside the kinds that
	// say the queue moved because it is the same news photographed from the other
	// side: the actions are refused whole, so what a reader is being told is that
	// several admissions, closes, or reorderings they might have expected did not
	// happen and the role that asked for them believed they had.
	KindTrackerBlockRefused Kind = "tracker.block-refused"
	// The same news once the harness has tried to fix it itself and failed: a
	// refused block is woken for once, and a conversation whose next block was
	// refused too is one nothing else is going to correct. It is its own kind
	// because it is the opposite half of the same story — the self-correction was
	// attempted and did not take — and a reader shown the refusal line twice would
	// read two unrelated losses rather than one the harness has stopped working on.
	KindTrackerRefusalUnresolved Kind = "tracker.refusal-unresolved"
	// What the operator decided about work an agent proposed. It is two kinds
	// rather than one with a field, for the reason the reviewer's verdict is:
	// work entering the backlog and work turned down are different news, and
	// every persona says them differently.
	KindWorkApproved Kind = "proposed-work.approved"
	KindWorkDeclined Kind = "proposed-work.declined"
	// Work leaving the run queue for a role's conversation, that role starting
	// it, and that role finishing it. They are the transitions of the one class
	// of work no run ever touches, and before they existed a thread said nothing
	// at all about it: an item rerouted to the architect and worked to completion
	// there showed a run that failed, and then silence for the rest of its life.
	//
	// Three kinds rather than one with a field, for the reason the verdict is
	// two: handed to somebody, taken up by them, and done by them are different
	// news, and the middle one is the only thing that says the routing was acted
	// on rather than merely recorded. The completion is here and nowhere else
	// because closing an item is otherwise said by the run that finished it, and
	// this is the work that has no run to say it.
	KindWorkHandedOff  Kind = "work.handed-off"
	KindWorkPickedUp   Kind = "work.picked-up"
	KindWorkCarriedOut Kind = "work.carried-out"
	// A cap the development manager crossed on his own delegated authority. It is
	// here rather than left to the item's notes because the crossing is the whole
	// of what the delegation traded away: the operator no longer approves it in
	// advance, so what keeps the veto is that they read it at the moment it
	// happens, with the cap, which of the item's five crossings it was, and the
	// argument for it. A crossing nobody was told about is the delegation without
	// the condition it was granted under.
	KindCapCrossed Kind = "cap.crossed"
	// KindRunStarted carries the recorded selection reason with it, so the fact
	// the selected-work-passes-intake-and-records-why invariant makes durable is
	// the fact an operator actually reads.
	KindRunStarted Kind = "run.started"
	// The deterministic gate, said in both directions. Passing is progress and
	// failing is a repair attempt, and neither is a verdict on the change.
	KindChecksPassed Kind = "checks.passed"
	KindChecksFailed Kind = "checks.failed"
	// The gate in front of the checks: a change refused for touching a path its
	// work item does not grant. It is a repair attempt exactly as a failing check
	// is, and it was silent in the thread until it had a kind — the note on the
	// blocked item and `yoyo status` said why a round was spent, and the thread
	// showed a repair round happening with no stated reason. It carries the paths
	// and what the item grants, because the grant is what would admit them and
	// the one thing a reader of the thread can act on.
	KindPathRefused Kind = "paths.refused"
	// The reviewer's verdict is two kinds rather than one with a field, because
	// an approval and a request for repairs are different news and are said
	// differently by every persona that says them.
	KindReviewApproved Kind = "review.approved"
	KindReviewRepairs  Kind = "review.repairs"
	// The three separate facts about getting a change out: promoted locally,
	// published to a forge, and merged there. A queued merge is its own kind
	// because it is a run that finished with its publication still owed.
	// A promotion refused because the target branch moved, answered by replaying
	// the change onto where the target went. It is said at note severity and
	// never as a stoppage: nothing is wrong with the change, nothing is waiting
	// on anybody, and a replay that passes costs the item nothing. It is said at
	// all because a run that loses several races on a busy branch otherwise reads
	// as one whose review went round for no stated reason.
	KindRaceLost       Kind = "promotion.race-lost"
	KindPromoted       Kind = "promotion.made"
	KindPublished      Kind = "publication.opened"
	KindMergeQueued    Kind = "merge.queued"
	KindMergeCompleted Kind = "merge.completed"
	// A merge that is not going to happen: the forge refused it, or gave up on
	// one it had queued. It is the fourth fact about getting a change out and the
	// only one nobody finds out about on their own — the change is promoted, the
	// item usually reads as landed, and what is left is a publication waiting on a
	// person with nothing saying so. It said nothing at all until the moment it
	// happens became part of the record.
	KindMergeDropped Kind = "merge.dropped"
	// A merge the harness withdrew because its checks failed on the target itself
	// rather than on the change: the failure is filed as the target's, as a red
	// landing is, and the merge waits on the item filed for it. It is said where a
	// dropped merge is, because it is the same publication not landing, and in
	// the harness's words, because nobody is waited on but the filed item.
	KindMergeWaitingOnTarget Kind = "merge.waiting-on-target"
	// What the landing checks made of the commit a run landed, said once the run
	// is over: the suite the per-run gate ran narrowed, run whole over what
	// actually landed. Green is the ordinary case and stays in the thread. Red is
	// the target branch being broken by a change every gate passed, which nobody
	// finds out about on their own — every run after it is cut from that commit
	// — so it reaches the channel, and the item it filed is named in it. An
	// unverified landing is the checks not having run at all, which is neither
	// and is said as itself rather than folded into either.
	KindLandingGreen      Kind = "landing.green"
	KindLandingRed        Kind = "landing.red"
	KindLandingUnverified Kind = "landing.unverified"
	// A run that stopped and one that carried on. Both are said because a queue
	// that goes quiet at night is indistinguishable from a broken one until
	// something says which it is.
	KindRunParked    Kind = "run.parked"
	KindRunContinued Kind = "run.continued"
	// A blocker is work that stopped and stayed stopped, which is the one thing
	// nobody finds out about on their own.
	KindBlockerRecorded Kind = "blocker.recorded"
	// A run that ended without succeeding and without leaving anybody a blocker
	// to act on: one the harness failed to carry, one something stopped rather
	// than judged, one it stopped on time. It is separate from the blocker above
	// for the reason the outcome vocabulary is small and closed at all — "failed"
	// is accurate about the attempt and says nothing about the work, so a channel
	// that said one word over all four told an operator the attempt was over and
	// nothing about whether their change still existed. The ending is named in the
	// read model's own word and what remains of the change is stated beside it.
	KindRunEnded Kind = "run.ended"
	// A provider refusing the harness for want of capacity, somewhere that is not
	// a run: a conversation turn, an independent review. A run says it by parking,
	// and this is the same news from every other process — hours in which nothing
	// will happen, with a cause and, where the provider named one, an end.
	KindUsageLimitExhausted Kind = "usage-limit.exhausted"
	// The same refusal with the opposite outcome: the model an agent's turn asked
	// for would not serve it — its window was closed, or it was a pinned version
	// this provider has not got — and a model the operator stated took the turn
	// instead. It is a note rather than a warning because nothing stopped — that
	// is the entire point of it — and it is said at all because an agent quietly
	// answering on a different model is a change to what the work was produced by,
	// which the operator has to be able to see without going looking. It is said
	// once per window rather than again while it stands, because the second
	// message would be a reason to mute the channel that carries the first. Which
	// of the two moved the turn is in the cause rather than in a second kind: to a
	// reader it is the same news, and the answer to "why" is one field away.
	KindModelSubstituted Kind = "model.substituted"
	// What an agent said in its own words: a report at its severity, a proposed
	// change to a document it does not own, a turn of an ask exchange, and the
	// exchange closing — including closing unresolved at its round cap, which
	// escalates to the operator and is exactly what this channel exists for.
	KindReportFiled    Kind = "report.filed"
	KindProposalRaised Kind = "proposal.raised"
	// KindAmendmentDecided is an owning role deciding a change proposed to its
	// own document, from its conversation. It is said afterwards rather than
	// asked: the decision was the role's to make, and the operator is told so he
	// can see what was decided on his behalf.
	KindAmendmentDecided Kind = "amendment.decided"
	KindExchangeTurn     Kind = "exchange.turn"
	KindExchangeClosed   Kind = "exchange.closed"
	// What a reply in a topic's thread did. They are the acknowledgment the
	// inbound half owes every message it reads: the directive as recorded, the
	// resolution that lifted one, what came of one that held nothing up, or the
	// refusal with its reason. None of them says an identifier — the person being
	// answered typed the words being read back to them, and an identifier is the
	// one thing in an acknowledgment they would have to go and resolve.
	//
	// Four kinds rather than one, because they are the four different things an
	// operator has to know — the work is now steered, the work is moving again,
	// what they asked for was done, or nothing was recorded at all — and a reader
	// who could not tell them apart at a glance would have to open the record to
	// find out whether they had been heard.
	//
	// Carried out is separate from resolved because a resolution lifts a pause
	// and an outcome never held one. Said as a resolution, the commonest kind of
	// directive would be reported to the thread that asked for it in words about
	// work resuming that had never stopped.
	//
	// Withdrawn is the fifth, and it is the one that is not a settlement: the
	// operator took the directive back, so nothing was carried out and nothing was
	// answered. It is still said to the thread that asked, because a directive
	// recorded from a thread and later withdrawn otherwise wore the thinking face
	// forever — the thread was told it was heard and never told it was taken back.
	KindDirectiveRecorded   Kind = "directive.recorded"
	KindDirectiveResolved   Kind = "directive.resolved"
	KindDirectiveCarriedOut Kind = "directive.carried-out"
	KindDirectiveRefused    Kind = "directive.refused"
	KindDirectiveWithdrawn  Kind = "directive.withdrawn"
	// A reply that was a question rather than an instruction. Nothing is
	// recorded from it — a question in the directive record is a directive
	// nobody gave — and what the person is owed is an answer, which the product
	// manager gives in the same thread. This is the receipt that says so in the
	// meantime, and it is its own kind rather than a refusal because nothing was
	// refused: the question was heard, and the answer is on its way.
	KindQuestionHeard Kind = "question.heard"
	// The operator's two switches. They are about the whole line rather than any
	// one item, which is why they are addressed to the product rather than
	// buried in a thread that would misfile them.
	KindIntakeHeld     Kind = "intake.held"
	KindIntakeReleased Kind = "intake.released"
	// The brake's own hold escalated by the harness to the Lead Product Manager,
	// after its summons-and-probe loop went round the configured number of times
	// with the development manager not escalating it. It is a kind of its own
	// rather than the hold said again because it is the moment the hold leaves
	// the harness's loop: the trip was said when it happened, and this is the one
	// message that tells the operator the loop has ended. It is said once, tagged
	// to the operators and sent to them directly, naming the cycles spent and
	// what stopped the last probe; the hourly line carries it from there, in the
	// channel, as the Lead Product Manager's.
	KindIntakeEscalated Kind = "intake.escalated"
	KindHoldPlaced      Kind = "hold.placed"
	KindHoldLifted      Kind = "hold.lifted"
	// A finding only the operator can act on, recorded: a report handled as
	// needing his hand, or a report filed at the severity that means action. It
	// is said once per finding, to him directly and tagged by member id, because
	// it is both important and his to act on — the communication rule's own test
	// for a tag — and it is never said again: `yoyo status` names it until it is
	// done. Six such findings sat in the report pile from 2026-08-17 until a
	// sweep reached them on 2026-09-14, and then reached him only because he
	// asked.
	KindOperatorAction Kind = "operator-action.recorded"
	// What a watch session is doing. A session that stays open until it is told
	// to stop spends most of its life saying nothing, and an idle one and a dead
	// one are the same silence: these are what tell them apart in the record. Idle
	// and braked are separate because they need different things — idle needs work
	// admitted, braked needs somebody to look at what stopped the line — and
	// stopping is recorded because a session that ended quietly is the case the
	// rest of this exists to rule out.
	//
	// Only the braked one is posted, and the rest reach the durable log and no
	// further: see reaches, where they are 473 of the 2,250 posts the operator's
	// survey counted as narration. Nothing is lost from the channel by that,
	// because the states somebody acts on are said by KindWatchBraked, by
	// KindLineWaiting once there is work a stopped session would have started, and
	// by KindStallNoticed where nothing was recorded at all.
	KindWatchStarted Kind = "watch.started"
	KindWatchIdle    Kind = "watch.idle"
	KindWatchBraked  Kind = "watch.braked"
	KindWatchBlocked Kind = "watch.blocked"
	KindWatchResumed Kind = "watch.resumed"
	KindWatchStopped Kind = "watch.stopped"
	// A session stopping to be restarted into a build deployed over it. It is the
	// same recorded stop as the one above and it is said apart from it, because
	// the two mean opposite things to whoever reads them: a session that ended is
	// a line waiting on somebody to start another, and this is a session that has
	// waited out its runs and is coming straight back on the new build. Saying
	// them the same way would hand the operator a move they do not have, once per
	// deploy, which is the standing chore self-redeployment exists to end.
	KindWatchRedeploying Kind = "watch.redeploying"
	// A session that chose nothing because the harness's store could not be read,
	// and is reading it again. It is the same recorded idle poll as KindWatchIdle
	// and it is said apart from it, on the precedent above: a poll that read the
	// queue and found nothing and a poll that never read the queue ask different
	// things of whoever reads them, and saying the second as the first told a
	// reader for the whole of a store outage that the session had found nothing to
	// start.
	KindWatchReadRetrying Kind = "watch.read-retrying"
	// A line that is choosing nothing while work is ready to be chosen. Every
	// kind above is a transition said once; this one is a state said again while
	// it stands, because the fact somebody needs is not that it began but that it
	// is still true hours later. Silence has to mean nothing to do, so a state
	// that means waiting-on-you says so periodically until it clears.
	KindLineWaiting Kind = "line.waiting"
	// A session that is choosing work while running a binary the harness has moved
	// on from. Like the line above it this is a state said again while it stands
	// rather than a transition, and for a sharper reason: nothing in the record
	// says it at all. A stale session goes on pulling work and the runs it starts
	// go on looking ordinary, so the only visible symptom is rounds spent against
	// bugs that were fixed on the main line hours earlier — which reads as an agent
	// failing rather than as a process nobody restarted.
	KindResidentStale Kind = "resident.stale"
	// The harness having started nothing at all while work was ready to start.
	// It is the opposite reading from the line above it: that one is derived from
	// a record something wrote about itself, and this one from the absence of any
	// such record — nothing has started since a moment the runs themselves date,
	// and nothing accounts for it. That is why it exists separately. A scheduler
	// that crashes writes no stop and a wedged one goes on claiming to watch, so
	// the state where everything has quietly gone dead is precisely the state no
	// process is left to announce; on 2026-09-01 it ran seven and a half hours and
	// was found by a person rather than by anything here.
	//
	// It is said again while it stands, and louder as it ages. It used to be said
	// once, on the reasoning that a stopped machine is either acted on or it is
	// not; on 2026-09-07 it was not, and four stopped hours had produced one
	// message four hours old. A line that is completely stopped is the most
	// serious thing this vocabulary carries, so the sink repeats it on its
	// heartbeat, raises it to critical as it stands, and tags the operators each
	// time. How often and how loud are the sink's; this is what is said.
	KindStallNoticed Kind = "stall.noticed"
	// The harness waiting out the provider's usage window. It is the same silence
	// the stall above reports and the opposite fact about it: nothing has started,
	// nothing is wrong, and the reason is one nobody can do anything about. It is
	// said because the silence itself is not readable — on 2026-09-05 a session
	// waited a window out from 12:13Z to 13:43Z, the stall alarm fired over it
	// saying nothing accounted for it, and the operator was paged for a machine
	// doing exactly what the provider had told it to.
	//
	// It is a note and never a warning, and it is said once per window rather than
	// again while it stands. Nothing is degraded and nothing is waiting on
	// anybody, so a second message would be a reason to mute the channel that
	// carries the alarm this replaces.
	KindProviderWindow Kind = "provider.window"
	// The provider holding every configured role at once. It is the capacity half
	// of the stall: the window above is a session waiting out a window it met
	// itself, and this is every role refused on a known reset with nothing
	// configured to fail over to — which the session choosing work never records,
	// because it is not the thing being refused. Between 2026-09-08 and 09-13 the
	// harness wrote 134 refusals down, each said once in the channel, and the fact
	// they added up to reached the operator five days later from his assistant.
	//
	// It is a warning, said the first time it is seen and again while it stands,
	// and it goes to the operators directly rather than to the channel alone: a
	// line stopped on a known reset with a remedy in the configuration is the
	// sharpest case there is of the harness being degraded by something a person
	// can change.
	KindCapacityHold Kind = "capacity.hold"
	// The provider answering nobody: a login nobody has renewed, or an API nothing
	// reaches. It is the other half of the capacity hold — no reset, no window,
	// nothing the harness can wait out on a clock — and the one the operator has
	// to be told the moment it happens, because a person is the only thing that
	// ends the first kind and the record is the only thing that says either.
	// From 2026-09-17 18:17 local every dispatch was refused, every recurring
	// pass recorded 0 turns, and the brake tripped naming the wrong remedy; the
	// operator learned of it by asking, three days later.
	//
	// It is a warning, said once when it is seen and tagged to the operators by
	// member id, because it is both important and theirs to act on. It is not
	// said again while it stands: the line's own banner carries it, and what a
	// repeated message would buy is a reason to mute the channel.
	KindProviderOutage Kind = "provider.outage"
	// The provider answering again after an outage: the login was renewed, or the
	// network came back. It is said once, as a note, because the operator was told
	// the line had stopped and is owed being told it carried on — and because the
	// remedy is the harness's own: nothing was released and nothing was restarted.
	KindProviderRestored Kind = "provider.restored"
	// A recurring task whose firings keep failing before their first turn: the
	// harness refusing the message it composed for the pass, a conversation
	// nothing can open, a turn that will not assemble. From 06:39Z on 2026-09-26
	// every development manager sweep failed that way six times in a row, every
	// triage decision waited a day, and the only account was a line per firing
	// in the sweep log. It does not end by waiting — the next firing meets the
	// same refusal — so it is said from the second failure in a row, once as a
	// warning and once more as critical once it has stood two hours, and not
	// again: the attention line carries it while it stands, and the first firing
	// that takes a turn ends it.
	KindRecurringTaskFailing Kind = "recurring.failing"
	// A claim the harness gave back because nothing was working on it. It is the
	// same reading as the stall above, taken from the other end: that one asks
	// whether anything has started and this one asks whether what the tracker says
	// is running actually is. The difference is what each can see. An item the
	// harness claimed has left the ready queue, so a machine whose only startable
	// work is sitting under dead claims reads as a drained queue — nothing held,
	// nothing running, nothing ready — and the stall reading is silent about it by
	// construction. Four nights of throughput went that way in the week of
	// 2026-09-01.
	//
	// It is said once per release, which is once per stuck item, because the
	// release is what ends the state: what follows it is the item being pulled
	// again, and that has its own message.
	KindClaimReleased Kind = "claim.released"
	// One value the project's template has improved that this project has never
	// edited. It is the third state here rather than a crossing, and it is the
	// mildest thing this vocabulary carries: nothing is wrong, nothing is waiting,
	// and nothing changes until somebody adopts it. What it is for is the project
	// that materialized from a template and then never heard that the template
	// moved — every surface that could have told them is one somebody has to run,
	// and a fix to a persona nobody knows about is a fix nobody has.
	//
	// It is said exactly once per improvement and never again, which is the whole
	// of what admits it to a message somebody is sent rather than one they come
	// looking for: a fact that repeats is a fact somebody mutes.
	KindBundleImprovement Kind = "bundle.improvement"
	// Several such values found on one reading, said together: how many there
	// are and the first few by name. It is the same fact as the kind above at a
	// coarser grain, and it exists because the class is bounded to one message a
	// pass: a project several template revisions behind offers a dozen values on
	// the first reading, and a dozen direct messages is the wall the
	// communication rule is against, aimed at the one channel that reaches a
	// person as a notification. Each value it names is still marked as said on
	// its own, so nothing it stands for is ever said again.
	KindBundleImprovements Kind = "bundle.improvements"
	// What one topic gathered while nothing was posting it. Every kind above is
	// something the record says happened; this one is what a surface does with a
	// backlog it cannot say one message at a time — a long gap replayed in full
	// is hundreds of messages nobody scrolls, and the surface that carries them
	// starts dropping them. So the accumulation is said once per thread, naming
	// how much of it there is and the record that holds all of it.
	KindCatchUpDigest Kind = "catch-up.digest"
	// One line of a durable log the sink could not read, and read past. Every
	// other kind here is something the record says; this is the one place the
	// record itself could not be read, said so that a torn write or a schema the
	// sink's build does not know costs one message rather than every message
	// behind it. It is said once per line, because the reader keeps the line's
	// position and does not meet it again.
	KindLogLineSkipped Kind = "log.line-skipped"
)

// Kinds is the whole reportable set, in the order work reaches them: the queue
// changing, then what a run does to one item of it, then what an agent says and
// what an operator does. A caller that has to cover every kind reads it from
// here rather than repeating the list.
func Kinds() []Kind {
	return []Kind{
		KindItemAdmitted,
		KindItemDecomposed,
		KindItemAttributed,
		KindItemReprioritized,
		KindTrackerBlockRefused,
		KindTrackerRefusalUnresolved,
		KindWorkApproved,
		KindWorkDeclined,
		KindWorkHandedOff,
		KindWorkPickedUp,
		KindWorkCarriedOut,
		KindCapCrossed,
		KindRunStarted,
		KindChecksPassed,
		KindChecksFailed,
		KindPathRefused,
		KindReviewApproved,
		KindReviewRepairs,
		KindRaceLost,
		KindPromoted,
		KindPublished,
		KindMergeQueued,
		KindMergeCompleted,
		KindMergeDropped,
		KindMergeWaitingOnTarget,
		KindLandingGreen,
		KindLandingRed,
		KindLandingUnverified,
		KindRunParked,
		KindRunContinued,
		KindBlockerRecorded,
		KindRunEnded,
		KindUsageLimitExhausted,
		KindModelSubstituted,
		KindReportFiled,
		KindProposalRaised,
		KindAmendmentDecided,
		KindExchangeTurn,
		KindExchangeClosed,
		KindDirectiveRecorded,
		KindDirectiveResolved,
		KindDirectiveCarriedOut,
		KindDirectiveRefused,
		KindDirectiveWithdrawn,
		KindQuestionHeard,
		KindIntakeHeld,
		KindIntakeReleased,
		KindIntakeEscalated,
		KindHoldPlaced,
		KindHoldLifted,
		KindOperatorAction,
		KindWatchStarted,
		KindWatchIdle,
		KindWatchBraked,
		KindWatchBlocked,
		KindWatchResumed,
		KindWatchStopped,
		KindWatchRedeploying,
		KindWatchReadRetrying,
		KindLineWaiting,
		KindResidentStale,
		KindStallNoticed,
		KindProviderWindow,
		KindCapacityHold,
		KindProviderOutage,
		KindProviderRestored,
		KindRecurringTaskFailing,
		KindClaimReleased,
		KindBundleImprovement,
		KindBundleImprovements,
		KindCatchUpDigest,
		KindLogLineSkipped,
	}
}

// Valid reports whether a name is one of the reportable kinds. An unrecognized
// kind has no voice line in any persona, so it is refused rather than posted as
// something nobody wrote words for.
func (k Kind) Valid() bool {
	switch k {
	case KindItemAdmitted, KindItemDecomposed, KindItemAttributed, KindItemReprioritized,
		KindTrackerBlockRefused, KindTrackerRefusalUnresolved, KindWorkApproved, KindWorkDeclined,
		KindWorkHandedOff, KindWorkPickedUp, KindWorkCarriedOut, KindCapCrossed,
		KindRunStarted, KindChecksPassed, KindChecksFailed, KindPathRefused,
		KindReviewApproved, KindReviewRepairs,
		KindRaceLost, KindPromoted, KindPublished, KindMergeQueued, KindMergeCompleted, KindMergeDropped, KindMergeWaitingOnTarget,
		KindLandingGreen, KindLandingRed, KindLandingUnverified,
		KindRunParked, KindRunContinued, KindBlockerRecorded, KindRunEnded, KindUsageLimitExhausted,
		KindModelSubstituted,
		KindReportFiled, KindProposalRaised, KindAmendmentDecided, KindExchangeTurn, KindExchangeClosed,
		KindDirectiveRecorded, KindDirectiveResolved, KindDirectiveCarriedOut, KindDirectiveRefused,
		KindDirectiveWithdrawn, KindQuestionHeard,
		KindIntakeHeld, KindIntakeReleased, KindIntakeEscalated, KindHoldPlaced, KindHoldLifted, KindOperatorAction,
		KindWatchStarted, KindWatchIdle, KindWatchBraked, KindWatchBlocked, KindWatchResumed, KindWatchStopped,
		KindWatchRedeploying, KindWatchReadRetrying, KindLineWaiting, KindResidentStale, KindStallNoticed,
		KindProviderWindow, KindCapacityHold, KindProviderOutage, KindProviderRestored,
		KindRecurringTaskFailing, KindClaimReleased,
		KindBundleImprovement, KindBundleImprovements, KindCatchUpDigest, KindLogLineSkipped:
		return true
	default:
		return false
	}
}

// TopicKind is what a thread is about. The three are the whole set: an item of
// work, an ask exchange that concerns no item, and the product line itself.
type TopicKind string

const (
	TopicWorkItem TopicKind = "work-item"
	TopicExchange TopicKind = "exchange"
	TopicProduct  TopicKind = "product"
)

// MaxTopicIDBytes bounds the identifier half of a topic key. A key is a map key
// and a thread's identity, so it is a name rather than a payload.
const MaxTopicIDBytes = 128

// MaxTopicTitleBytes bounds the title carried beside a topic, and titleCut says
// where one was cut. A title is a line somebody scans rather than a record they
// read — the whole of what an item says is in the tracker — so a title long
// enough to push the identifier off a reader's screen is cut to the part that
// names it.
const (
	MaxTopicTitleBytes = 160
	titleCut           = "…"
)

// Topic is the thread key. The primary one is the work item, because the item is
// what a narrative is about: an exchange concerning an item is addressed to that
// item rather than to itself, and only an exchange with no item gets a thread of
// its own. What belongs to the whole line and to no item is addressed to the
// product, which is the top of the channel rather than any thread.
type Topic struct {
	Kind TopicKind `json:"kind"`
	// ID names the item or exchange, and is empty for the product, which is the
	// one topic there is only ever one of.
	ID string `json:"id,omitempty"`
	// Title is what the topic is called, in the words the durable record the
	// message was read from carried. It is what makes a thread header a subject a
	// reader recognizes rather than an identifier they have to go and resolve, and
	// it is deliberately not part of the key: the same topic is the same thread
	// whatever it was called when each message was said, so an item somebody
	// renamed never opens a second one. Empty is ordinary — a record that carried
	// no title leaves the identifier to name the topic on its own.
	Title string `json:"title,omitempty"`
}

// WorkItem addresses a topic to one item of work.
func WorkItem(id string) (Topic, error) {
	topic := Topic{Kind: TopicWorkItem, ID: strings.TrimSpace(id)}
	return topic, topic.Validate()
}

// Exchange addresses a topic to an ask exchange that concerns no work item. An
// exchange that does concern one belongs in that item's thread instead.
func Exchange(id string) (Topic, error) {
	topic := Topic{Kind: TopicExchange, ID: strings.TrimSpace(id)}
	return topic, topic.Validate()
}

// Product addresses a topic to the whole line: the operator's holds, and
// anything else that is about every item rather than one of them.
func Product() Topic {
	return Topic{Kind: TopicProduct}
}

// WithTitle names a topic in words, from what the record a message was read
// from calls it. It sanitizes rather than refuses, which is the whole reason it
// is separate from the constructors: the identifier is already exact, so a title
// is what a header adds to it, and refusing an awkward one would take a work
// item's entire narrative out of the channel over the way somebody phrased its
// name. So it is folded onto one line — a header is a line — cut to the bound,
// and left absent where the record carried nothing.
func (t Topic) WithTitle(title string) Topic {
	// The product is the whole line rather than a subject somebody named, and it
	// opens no thread to head, so it is left as the one topic that is only ever
	// itself.
	if t.Kind == TopicProduct {
		return t
	}
	t.Title = boundTitle(title)
	return t
}

// boundTitle is a title as a header can carry it: one line, and short enough
// that the identifier beside it is still the first thing read.
func boundTitle(title string) string {
	folded := strings.Join(strings.Fields(title), " ")
	if len(folded) <= MaxTopicTitleBytes {
		return folded
	}
	cut := MaxTopicTitleBytes - len(titleCut)
	for cut > 0 && !utf8.RuneStart(folded[cut]) {
		cut--
	}
	return strings.TrimRight(folded[:cut], " ") + titleCut
}

// Key is the topic as one string, which is what a thread map is keyed by and
// what an inbound reply is correlated back through. The colon separator is why
// an identifier may not contain one.
func (t Topic) Key() string {
	if t.Kind == TopicProduct {
		return string(TopicProduct)
	}
	return string(t.Kind) + ":" + t.ID
}

// ParseTopic reads a key back into the topic it names, so a durable thread map
// says what each thread is about rather than only that it exists.
func ParseTopic(key string) (Topic, error) {
	trimmed := strings.TrimSpace(key)
	if trimmed == string(TopicProduct) {
		return Product(), nil
	}
	kind, id, found := strings.Cut(trimmed, ":")
	if !found {
		return Topic{}, fmt.Errorf("topic key %q names no topic", key)
	}
	topic := Topic{Kind: TopicKind(kind), ID: id}
	if err := topic.Validate(); err != nil {
		return Topic{}, err
	}
	return topic, nil
}

// Validate rejects a topic that could not name a thread. An identifier is held
// to being a name — no whitespace, no separator, bounded — because it is half of
// a key that has to round-trip.
func (t Topic) Validate() error {
	var problems []error
	switch t.Kind {
	case TopicProduct:
		if t.ID != "" {
			problems = append(problems, errors.New("the product topic names no identifier"))
		}
	case TopicWorkItem, TopicExchange:
		switch {
		case strings.TrimSpace(t.ID) == "":
			problems = append(problems, fmt.Errorf("a %s topic requires an identifier", t.Kind))
		case len(t.ID) > MaxTopicIDBytes:
			problems = append(problems, fmt.Errorf("topic identifier is %d bytes, limit is %d", len(t.ID), MaxTopicIDBytes))
		case strings.ContainsAny(t.ID, ": \t\n"):
			problems = append(problems, fmt.Errorf("topic identifier %q must not contain a separator or whitespace", t.ID))
		}
	default:
		problems = append(problems, fmt.Errorf("topic kind %q must be %q, %q, or %q",
			t.Kind, TopicWorkItem, TopicExchange, TopicProduct))
	}
	return errors.Join(problems...)
}

// HarnessSpeaker is what the speaker key says for what no persona did. It is a
// word rather than an empty role because "the harness promoted this" is a real
// account of a real act, and the one role that must never be able to claim it is
// any of the agents.
const HarnessSpeaker = "harness"

// Speaker is whose account a message is. It is the role the work was done under
// and the configured agent that filled it, or the harness for what no persona
// did — a promotion, a merge, the operator's own switches.
type Speaker struct {
	// Role is empty for the harness. Both halves are kept because a project may
	// configure more than one agent for a role, and "which developer said this"
	// is a different question from "a developer said this".
	Role  domain.AgentRole `json:"role,omitempty"`
	Agent string           `json:"agent,omitempty"`
}

// Harness is the speaker for what no persona did.
func Harness() Speaker {
	return Speaker{}
}

// Persona is the speaker for what a role did, naming the configured agent that
// filled it where the project recorded one.
func Persona(role domain.AgentRole, agent string) Speaker {
	return Speaker{Role: role, Agent: strings.TrimSpace(agent)}
}

// IsHarness reports the speaker that is not a persona at all.
func (s Speaker) IsHarness() bool {
	return s.Role == ""
}

// Key names the speaker in the envelope: a role, or the harness.
func (s Speaker) Key() string {
	if s.IsHarness() {
		return HarnessSpeaker
	}
	return string(s.Role)
}

// Validate rejects a speaker nothing has a voice for. An unrecognized role is
// refused rather than posted in some default voice, because a message in a voice
// nobody wrote is a message attributed to a persona that did not speak it.
func (s Speaker) Validate() error {
	if s.IsHarness() {
		if s.Agent != "" {
			return errors.New("the harness is not a configured agent")
		}
		return nil
	}
	if !s.Role.Valid() {
		return fmt.Errorf("speaker role %q is not one of the harness's roles", s.Role)
	}
	return nil
}

// Refs are the correlation identifiers a message carries: enough to get from
// what was said back to the record that said it. They are what makes a message
// an index into the durable account rather than a replacement for it.
type Refs struct {
	RunID          string `json:"run_id,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	ExchangeID     string `json:"exchange_id,omitempty"`
	WorkItemID     string `json:"work_item_id,omitempty"`
	DirectiveID    string `json:"directive_id,omitempty"`
	PullRequest    string `json:"pull_request,omitempty"`
}

// Record names the durable record this message came out of, as a reader would
// have to name it to go and find it. It is what a truncated body points at, so
// nobody reads a cut message as the whole of what was recorded.
//
// The conversation is not offered, though the refs carry it: this is rendered
// into a message a person reads, and a conversation identifier is not something
// that reader does anything with. A message whose only reference is one is
// pointed at the durable record in words instead, which is the same answer the
// refs already give a message with no reference at all.
func (r Refs) Record() string {
	switch {
	case strings.TrimSpace(r.RunID) != "":
		return r.RunID
	case strings.TrimSpace(r.ExchangeID) != "":
		return r.ExchangeID
	case strings.TrimSpace(r.WorkItemID) != "":
		return r.WorkItemID
	default:
		return "the durable record"
	}
}

// Detail is what a voice line has to work with beyond the kind itself. It is one
// flat record with optional fields rather than a shape per kind, for the reason
// durable run state is: every field says which kinds read it, absence is a fact
// rather than an error, and a producer that fills nothing still produces a
// message that says something true.
type Detail struct {
	// SelectedBy and SelectionReason are the recorded account of why the harness
	// is running this item, read by KindRunStarted. Absence is reported as no
	// reason recorded rather than as a blank, because an unaccounted run is
	// exactly what carrying the reason exists to make visible.
	SelectedBy      string `json:"selected_by,omitempty"`
	SelectionReason string `json:"selection_reason,omitempty"`
	// Account and Configuration are which provider account a run is spending and
	// which configuration set it up, read by KindRunStarted. They are said where
	// the thread opens because they hold for the whole of a run, and they are said
	// at all because there is one account today and the message that names it is
	// the one an operator will read on the day there are two.
	Account       string `json:"account,omitempty"`
	Configuration string `json:"configuration,omitempty"`
	// Command and ExitCode are the failing deterministic check, read by
	// KindChecksFailed. They are the check's own words rather than a summary of
	// them.
	Command  string `json:"command,omitempty"`
	ExitCode int    `json:"exit_code,omitempty"`
	// RefusedPaths, OmittedPaths, and Grants are the protected-path gate's
	// refusal, read by KindPathRefused: the paths the change touched that the
	// item never granted, how many further ones the record's bound dropped from
	// that list, and what the item did grant. They are the record's own three
	// fields rather than a sentence about them, because the refusal is read
	// beside the grants — a refusal that looks wrong is most often a grant that
	// named the path differently — and a reader given only the paths cannot see
	// that. Empty grants are the ordinary case and are said as the item granting
	// nothing, which is a fact about the item rather than an absence in the
	// record: the gate records every grant it read whenever it refuses.
	RefusedPaths []string `json:"refused_paths,omitempty"`
	OmittedPaths int      `json:"omitted_paths,omitempty"`
	Grants       []string `json:"grants,omitempty"`
	// Checks is where the check stage stood when it passed, read by
	// KindChecksPassed: what it spent of its bound, in the record's own words —
	// "14m of the 30m bound". It is carried already worded for the reason the
	// four lines are, and it is said at all because the bound is the number a
	// reader has to have to know whether a stage is slow.
	Checks string `json:"checks,omitempty"`
	// Landing is what the landing checks made of the commit a run landed, read
	// by the three landing kinds and carried already worded by the record: the
	// check that failed, the commit, and the item a red landing filed.
	Landing string `json:"landing,omitempty"`
	// Findings is how many the reviewer raised, read by KindReviewRepairs.
	Findings int `json:"findings,omitempty"`
	// Requested is what each of those findings asked for, one entry per finding
	// and in the order the reviewer raised them, read by KindReviewRepairs beside
	// the count. The count alone says how much work came back and nothing about
	// what it is, so an operator reading it could not tell a one-word correction
	// to a document from a problem with the design without leaving the channel.
	//
	// Each entry is carried already said rather than as the finding it came from,
	// for the reason Standing is: what a reader sees is the durable record's own
	// account, and a second rendering of one finding is a second way to say it.
	// The words are the reviewer's and are already redacted, because the record
	// they are read from was redacted before it was written.
	//
	// Absent is a record that counted findings without keeping them, which is
	// every run reviewed before the repair loop kept them. That is said as itself
	// rather than as a run whose reviewer asked for nothing.
	Requested []string `json:"requested,omitempty"`
	// TargetBranch and Commit are where a promotion put the change, read by
	// KindPromoted. TargetBranch is also the branch a race was lost for, read by
	// KindRaceLost.
	//
	// Commit is read a second time by KindResidentStale, where it is the revision
	// a running session's binary was built from: the same kind of fact — a place
	// in the repository's history — said about a process rather than about a
	// change, and a reader who has both is a reader who can check the count.
	TargetBranch string `json:"target_branch,omitempty"`
	Commit       string `json:"commit,omitempty"`
	// Races is how many races for its target branch a run has lost, read by
	// KindRaceLost beside TargetBranch. It is the run's own count, so the line
	// for the third race says it is the third.
	Races int `json:"races,omitempty"`
	// Behind is how many harness changes have landed since a running session's
	// binary was built, read by KindResidentStale. It is the count rather than the
	// elapsed time because what matters is what the session is missing, and an
	// afternoon with nothing merged in it costs nobody anything.
	Behind int `json:"behind,omitempty"`
	// PullRequest is how the published request is named to a reader, read by the
	// three publication kinds.
	PullRequest string `json:"pull_request,omitempty"`
	// Cause is what a parked run is waiting on, read by KindRunParked, and what a
	// provider refused for, read by KindUsageLimitExhausted. It is a phrase
	// rather than a code because what an operator needs is the answer to "waiting
	// on what", and the causes do not share a shape: a deadline, an overloaded
	// server, the operator themselves, an unresolved directive.
	//
	// It is read a third time by KindStallNoticed, where it is the dominant thing
	// accounting for a queue nothing is being pulled from — "33 of the 47 admitted
	// items are awaiting carry-out of decisions already recorded". It is carried
	// already worded, for the reason Standing is: the accounting is derived once
	// from what the last poll recorded, and a surface that worded it again could
	// come to say a different thing about one queue than the session's own idle
	// line does. That is not hypothetical — an alarm deriving its own answer beside
	// a session that had already found the accounting is what woke the operator on
	// 2026-09-06 with "nothing accounting for it".
	//
	// Mover is whose move follows that cause, worded by the same derivation and
	// carried for the same reason. It is the one whose-move clause that is not in
	// the table below, because the table answers per kind and this answers per
	// cause: a queue held for triage decisions and a queue carried in conversation
	// are one kind of message and two different people to go to. A stall with no
	// poll to read a cause from carries neither, and the table answers.
	//
	// KindRunEnded reads it for the same reason and without a Cause beside it. A
	// run that died before it claiming anything is docketed as it dies, where every
	// other ending under that kind recorded nothing for anybody to decide — one
	// kind of message, two different people again. A run that got as far as
	// claiming carries none, and the table answers.
	//
	// KindRunEnded and KindBlockerRecorded both read it for an approved change the
	// environment stopped short of its promotion: that ending is the harness's to
	// resume by `yoyo triage resume` once the cause has cleared, which is neither
	// the decision one table clause names nor the nothing the other does. The
	// sentence is the run record's own, the one the docket entry carries and the
	// repair verb refuses in — while the run's branch is there. Once it is gone
	// the sentence is the one those surfaces then say, naming the re-run.
	//
	// Both read it too for a run whose check stage its bound stopped: load stopped
	// it rather than the change, and the harness continues it at its checks, in
	// the run record's own sentence — the one the docket entry carries.
	//
	// KindCapacityHold reads Mover alone: whose move ends a hold early is worded by
	// the read model beside the hold itself, so the sentence a terminal prints on
	// its attention line and the clause this message ends on are one wording.
	// KindLineWaiting reads it the same way, for the intake hold above all: the
	// brake's hold is the development manager's while she decides and the Lead
	// Product Manager's once it is escalated, and the read model words which.
	Cause string `json:"cause,omitempty"`
	Mover string `json:"mover,omitempty"`
	// Waiting is what a provider's refusal stopped, read by
	// KindUsageLimitExhausted. A run names itself, so this is for everything that
	// is not one: which conversation, which review. It is what turns "the
	// provider is out of capacity" into news somebody can act on.
	Waiting string `json:"waiting,omitempty"`
	// Model and ServedBy are the model a provider would not serve and the one that
	// took the turn instead, read by KindModelSubstituted. They are two fields
	// rather than one sentence because they answer opposite halves of the question
	// a substitution raises — what would not serve, and what the work is being
	// produced by instead — and a reader given only the second cannot tell a
	// substitution from a configuration they do not remember making.
	Model    string `json:"model,omitempty"`
	ServedBy string `json:"served_by,omitempty"`
	// Round and Rounds are where an ask exchange has got to against its cap, read
	// by KindExchangeTurn and KindExchangeClosed.
	Round  int `json:"round,omitempty"`
	Rounds int `json:"rounds,omitempty"`
	// Unresolved is what an exchange closed without settling, read by
	// KindExchangeClosed. Empty means it closed resolved, which is the ordinary
	// way for one to end.
	//
	// It is read a second time by KindDirectiveRecorded, where it is what the
	// recorded directive left for somebody to settle — and therefore the whole of
	// what says whether the work it affects is paused. Empty there is the
	// operational directive, which is in force already and stops nothing.
	Unresolved string `json:"unresolved,omitempty"`
	// Artifact is the document a proposal is about, read by KindProposalRaised
	// and KindAmendmentDecided,
	// and the governed document an artifact-changing directive rewrites, read by
	// KindDirectiveRecorded.
	Artifact string `json:"artifact,omitempty"`
	// ReceivedBy is the role a directive was addressed to, read by
	// KindDirectiveRecorded. It is attribution rather than routing — the record
	// reaches every role whichever one is named — and it is said because an
	// operator who addressed the reviewer wants to see that the reviewer is who
	// the record says they told.
	ReceivedBy string `json:"received_by,omitempty"`
	// Title is what an item is called, read by the kinds that report one arriving
	// in the backlog. An identifier says which item; the title is what makes a
	// thread readable by somebody who has not read the tracker.
	Title string `json:"title,omitempty"`
	// Goal is what the work says it serves, read by the admission kinds and by
	// KindItemAttributed. It is the goal in the words the goals document states
	// it in, which is what makes the claim checkable rather than decorative.
	Goal string `json:"goal,omitempty"`
	// Parent is the admitted item a decomposition was created under. "Created
	// under what" is the whole of what makes a decomposition auditable, so the
	// envelope carries it for anybody reading the record afterwards.
	//
	// No voice line says it. What the record holds is the parent's identifier and
	// nothing that names it in words, and a message handing a reader an
	// identifier to resolve is what these messages stopped doing: the
	// decomposition is said as what it is, and the item it came out of is in the
	// record and in the tracker. Recording the parent's title beside its
	// identifier is what would let the channel name it.
	Parent string `json:"parent,omitempty"`
	// Priority is where in the queue a reprioritization put an item, read by
	// KindItemReprioritized. It is negative where the record did not say, because
	// zero is the highest priority rather than an unstated one.
	Priority int `json:"priority,omitempty"`
	// Executor is what carries an item where a developer run does not, read by
	// the three handoff kinds and by the admission kinds. It is also what decides
	// whose move follows an admission: work marked for a conversation is not
	// waiting for a run and never will be, so a thread that said it was waiting
	// for one would be telling the reader to expect something that cannot come.
	// Empty is the ordinary case, which is work a developer run carries.
	Executor string `json:"executor,omitempty"`
	// Stopped, Since, and Ready are read by KindLineWaiting: what has stopped the
	// harness choosing work, when it became that way, and how much admitted work
	// the tracker calls ready behind it. The age is rendered from Since against
	// the event's own moment rather than carried as a phrase, because a state
	// that is said again every hour has a different age every time it is said and
	// a timestamp a reader has to subtract from is not the fact they need.
	//
	// All three are read again by KindStallNoticed, where they say the same three
	// facts about the opposite reading: what the record last said about the thing
	// that chooses work, the moment the harness last started anything, and how
	// much was ready through the silence that followed. They are the same fields
	// rather than a second set because a reader is being told one thing — nothing
	// is happening, since when, over how much — and two vocabularies for it would
	// be two ways to say it differently.
	//
	// Stopped and Since are read a third time by KindProviderWindow, which is the
	// same silence again with the one thing that accounts for it: what the harness
	// is waiting on, and since when. It reads no ready count, because a window is
	// the provider's answer whatever is in the queue behind it. KindCapacityHold
	// reads the pair the same way for the provider holding every role: the hold as
	// the read model words it, and when its earliest refusal was recorded.
	//
	// KindClaimReleased reads the same pair about one item rather than about the
	// line: what the record said became of the run that left the claim behind, and
	// the last moment that run said anything. They are the same fields for the
	// same reason — one reader is being told one thing, nothing is working on
	// this, since when — and a vocabulary of its own would be a second way to say
	// it.
	//
	// Since is read once more by KindCatchUpDigest, where it is the first of the
	// events the digest stands for: the same subtraction against the event's own
	// moment says how much of a gap was digested away.
	Stopped string    `json:"stopped,omitempty"`
	Since   time.Time `json:"since,omitempty"`
	Ready   int       `json:"ready,omitempty"`
	// Outstanding is how many promoted changes are waiting on the forge to
	// publish them, read by KindLineWaiting beside the ready count. It is the
	// resurfacing half of a dropped merge: the drop is said once as it happens,
	// and a reader who was not there is told again, as a count, every time the
	// line reports itself — until the publication is settled. Zero is the
	// ordinary answer and is said as itself, because "no promotions waiting" is
	// the fact somebody scanning a quiet channel is actually checking for.
	Outstanding int `json:"outstanding,omitempty"`
	// Running is how many developer runs the session could see in flight, read by
	// KindWatchIdle. It is half of whose move follows a poll that started nothing:
	// a session idle on one slot while a run works on the other is the harness
	// working, and a message that said only "nothing is startable" was read three
	// times as a line that had stopped.
	Running int `json:"running,omitempty"`
	// Unreadable is a poll that chose nothing because the harness could not be
	// read. It is carried on KindWatchReadRetrying, which is what such a poll is
	// said as, and read by KindWatchIdle only where a caller built one by hand: it
	// is the other state whose next move is not an admission, since nothing a
	// person admits reaches a store that will not answer.
	Unreadable bool `json:"unreadable,omitempty"`
	// ProviderWindow is a poll that chose nothing because the provider is refusing
	// the harness for want of capacity, read by KindWatchIdle. It is the third such
	// state, and the one that most looks like an empty queue from outside: nothing
	// a person admits is startable until the window lifts, so a clause naming an
	// admission would be naming the one act that cannot help.
	ProviderWindow bool `json:"provider_window,omitempty"`
	// Standing is where the harness stands, already rendered into the four lines
	// the read model produces, and read by KindLineWaiting, KindStallNoticed,
	// KindProviderWindow and KindCapacityHold — every message that says nothing is
	// being chosen, since
	// a reader told that still wants to see what is. It is carried as the
	// rendered text rather than as the state it came from, because the format is
	// the contract: the same four lines are printed at a terminal and said here,
	// and a second rendering of one standing is the disagreement one derivation
	// exists to prevent. Absent means the surface was assembled without a way to
	// read it, which is stated rather than left as a blank.
	Standing string `json:"standing,omitempty"`
	// Setting and Improvement are which configuration value the project's template
	// has improved and what the comparison says about it, read by
	// KindBundleImprovement. KindBundleImprovements reads Improvement alone, worded
	// over the several values it stands for, and names no one setting.
	//
	// Improvement is carried already worded, for the reason Standing is: what
	// makes a value an improvement -- what the template supplied when the project
	// was generated, what the project holds, and what the template supplies now --
	// is a three-way comparison that lives in one place, and a surface that
	// re-derived it here could come to say a different thing about one value than
	// `yoyo config drift` does. The setting is carried beside it so a voice can
	// name the value without taking the sentence apart.
	Setting     string `json:"setting,omitempty"`
	Improvement string `json:"improvement,omitempty"`
	// Accumulated is how many events one topic gathered while nothing was posting
	// them, read by KindCatchUpDigest. It is the whole of what the digest claims:
	// how much there is, and therefore how much of the thread's narrative is in
	// the durable record rather than in the channel.
	Accumulated int `json:"accumulated,omitempty"`
	// Ending is what became of a run, in the read model's own fixed vocabulary,
	// read by KindRunEnded. It is carried as the word rather than derived here for
	// the reason Standing is carried rendered: the vocabulary is the contract, and
	// a surface that classified a run for itself is a surface that can come to say
	// a different word about it than `yoyo status` does.
	//
	// Remains is what the repository holds of that run's change, in the phrase
	// and the description the shared lookup renders, and is read by KindRunEnded and
	// by KindBlockerRecorded. Both kinds state it because "is my work gone" is the
	// question a run that did not succeed is actually read for, and a stoppage
	// somebody has to decide about is the case where the answer matters most.
	Ending  string `json:"ending,omitempty"`
	Remains string `json:"remains,omitempty"`
	// Refused is how many tracker actions a block the harness would not read asked
	// for, and Asking is the role that asked, both read by
	// KindTrackerBlockRefused. The count is the size of what did not happen, and
	// it is negative where the record did not carry one, exactly as the priority
	// above is: a block the harness could not decode says nothing about how much
	// was in it, and no actions and an uncounted number of them are different
	// things to be told. The role is carried because the harness speaks this
	// message: a refusal is the harness's own act, and the only thing that says
	// whose actions were lost is the record.
	Refused int    `json:"refused,omitempty"`
	Asking  string `json:"asking,omitempty"`
	// Budget, Cap, Crossing, and Crossings are the delegated cap crossing, read by
	// KindCapCrossed: which cap was crossed, the ceiling now in force for it, which
	// of the item's permitted crossings this was, and how many the role gets.
	//
	// All four are carried rather than derived, because the operator's veto rests
	// on them being the record's own figures: the guard raised that cap to that
	// number and counted that crossing, and a surface that recounted the item's
	// overrides for itself could tell the operator a different story about what it
	// just permitted. The count is said with its bound for the reason every count
	// in this vocabulary is — a third crossing means nothing to a reader who cannot
	// see there are five.
	Budget    string `json:"budget,omitempty"`
	Cap       int    `json:"cap,omitempty"`
	Crossing  int    `json:"crossing,omitempty"`
	Crossings int    `json:"crossings,omitempty"`
	// Needs is what the operator has to do about a finding, in the words of
	// whoever found it, and RecordedIn is where the finding is recorded, both
	// read by KindOperatorAction. They are two fields because they are the two
	// halves of the message the operator asked for: what is needed, and where to
	// go and read it. FoundBy is who found it and how, read by the same kind.
	// Ends is what ends the finding — a handling of the report, a later
	// decision on the run — worded by the read model, and read by the same kind.
	// Mover is read by it too, for whose move follows, so the terminal's
	// attention line and this message close on one wording.
	Needs      string `json:"needs,omitempty"`
	RecordedIn string `json:"recorded_in,omitempty"`
	FoundBy    string `json:"found_by,omitempty"`
	Ends       string `json:"ends,omitempty"`
	// Stops is the runs the brake counted on its way to tripping, already worded
	// by the record, read by KindIntakeHeld. It is empty on the operator's own
	// hold, which counts nothing, and the line leaves the clause out.
	Stops string `json:"stops,omitempty"`
	// Reason is why: why the operator held something, read by KindIntakeHeld; why
	// a role changed the backlog, read by the tracker kinds; why proposed work
	// was turned down, read by KindWorkDeclined; why a thread reply recorded
	// nothing, read by KindDirectiveRefused; and why a block of tracker actions
	// was refused whole, read by KindTrackerBlockRefused; and the justification a
	// cap was crossed on, read by KindCapCrossed. An operator who holds in a hurry
	// owes nobody an explanation, so absence is ordinary — except on a crossing,
	// where the harness refuses one carrying no reason before it is ever recorded,
	// so an absence there is a record written by something that should not exist.
	Reason string `json:"reason,omitempty"`
	// Log, Line, and Offset are which durable log a line could not be read from
	// and where in the file that line is, read by KindLogLineSkipped beside the
	// decoder's own words in Cause. The line is numbered from one and the offset
	// is the byte it begins at, so a reader can open the file at it; the log is
	// named as the sink names its streams rather than by path, because the path
	// is under the state root and the message is read somewhere that is not.
	Log    string `json:"log,omitempty"`
	Line   int    `json:"line,omitempty"`
	Offset int64  `json:"offset,omitempty"`
}

// Event is one reportable thing the record says happened. It carries no words of
// its own beyond what an agent actually wrote: the sentence is the persona's,
// rendered from the kind and the detail, so two events of the same kind from the
// same persona always read the same way.
type Event struct {
	Kind Kind `json:"kind"`
	// At is when the record says it happened, which is not when it was posted:
	// the sink catches up from its cursors, so a message can arrive long after
	// the moment it describes and must still date itself honestly.
	At time.Time `json:"at"`
	// Severity is how much attention this is asking for, in the reports
	// vocabulary. It is one vocabulary rather than two because an operator
	// reading a channel should not have to learn which words mean what depending
	// on which producer said them.
	Severity report.Severity `json:"severity"`
	Refs     Refs            `json:"refs"`
	Detail   Detail          `json:"detail,omitempty"`
	// Text is what an agent wrote, and it is carried into the message unchanged.
	// It is already redacted, because events are redacted before they are
	// persisted and nothing here reads anything else. A kind that carries no
	// authored text leaves it empty.
	Text string `json:"text,omitempty"`
}

// Validate rejects an event nothing could be said about. What it does not check
// is which fields a kind filled: absence is rendered as a stated absence rather
// than refused, because a record that is missing something is still a record
// somebody has to be told about.
func (e Event) Validate() error {
	var problems []error
	if !e.Kind.Valid() {
		problems = append(problems, fmt.Errorf("kind %q is not a reportable event", e.Kind))
	}
	if !e.Severity.Valid() {
		problems = append(problems, fmt.Errorf("severity %q must be %q, %q, or %q",
			e.Severity, report.SeverityCritical, report.SeverityWarning, report.SeverityNote))
	}
	if e.At.IsZero() {
		problems = append(problems, errors.New("at is required"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid event: %w", err)
	}
	return nil
}

// Message is the envelope, and it is the whole of what a poster is given: what
// happened, which thread it belongs in, whose account it is and how that persona
// appears, how much attention it is asking for, the words, and the way back to
// the record.
type Message struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          Kind   `json:"kind"`
	Topic         string `json:"topic"`
	// TopicTitle is what the topic is called, carried beside the key rather than
	// inside it so a surface opening a thread can name the subject in words while
	// still addressing it by the key alone. Absent where the record carried no
	// title, which reads as the identifier naming the topic by itself.
	TopicTitle string          `json:"topic_title,omitempty"`
	Speaker    string          `json:"speaker"`
	Identity   Identity        `json:"identity"`
	Severity   report.Severity `json:"severity"`
	// Reach is how far this message is carried: the channel level, its topic's
	// thread, or no further than the record it was read from. It is carried here
	// rather than worked out by whatever posts, for the reason the severity is —
	// how much attention a message asks for and how far it goes are both decided
	// by the record, and a surface that decided either for itself would be a
	// second policy disagreeing with the ratified one in front of the one person
	// who would have to adjudicate it.
	//
	// A message that reaches only the record is never rendered, because the
	// delivery pass declines to say it before anything renders. It is still a
	// value this field can hold: what refuses to post is one derivation, and a
	// caller rendering an event directly gets the same answer the pass got.
	Reach Reach     `json:"reach"`
	Body  string    `json:"body"`
	Refs  Refs      `json:"refs"`
	At    time.Time `json:"at"`
}

// Validate rejects a message that could not be posted as an account of anything.
func (m Message) Validate() error {
	var problems []error
	if m.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be %d", SchemaVersion))
	}
	if !m.Kind.Valid() {
		problems = append(problems, fmt.Errorf("kind %q is not a reportable event", m.Kind))
	}
	if _, err := ParseTopic(m.Topic); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(m.Speaker) == "" {
		problems = append(problems, errors.New("speaker is required"))
	}
	if err := m.Identity.Validate(); err != nil {
		problems = append(problems, err)
	}
	if !m.Severity.Valid() {
		problems = append(problems, fmt.Errorf("severity %q is invalid", m.Severity))
	}
	if !m.Reach.Valid() {
		problems = append(problems, fmt.Errorf("reach %q must be %q, %q, or %q",
			m.Reach, ReachChannel, ReachThread, ReachRecord))
	}
	if strings.TrimSpace(m.Body) == "" {
		problems = append(problems, errors.New("body is required"))
	}
	if m.At.IsZero() {
		problems = append(problems, errors.New("at is required"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid message: %w", err)
	}
	return nil
}
