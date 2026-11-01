package orchestrator

// Putting a stopped run in front of the development manager, with nobody
// carrying it there.
//
// A run that fails independent review after every permitted attempt is docketed
// as it stops, and the docket is delivered into the development manager's
// conversation when she opens one. Between those two facts sat a person. The
// stoppage was durable and the docket was assembled and her authority to decide
// was wired, and none of it moved until somebody opened her conversation and
// told her a run had stopped — which is the operator standing in as the
// harness's messenger, in the one workflow that exists so they do not have to
// stand in as its eyes.
//
// So the harness delivers it. What that changes is the courier and nothing else:
// the evidence is the docket entry she would have read anyway, the decision is
// hers and is recorded where every triage decision is recorded, the caps still
// refuse what they refused, and nothing here carries a decision out. What carries
// one out is the same pull, a step later and under every gate the verbs already
// ask; see carryout.go, which is the other end of this loop.
//
// # What is delivered, and what is not
//
// Two entries, and both for the same reason: the next step is a judgment rather
// than a fix.
//
// The first is the 2-of-2 review stoppage — a run that ended on a durable blocker
// with its reviewer still requiring repair. A failing check, a refused path, and
// a replay conflict are all stoppages too, and each is a different question. They
// stay on the docket for her to read, exactly as before, rather than being
// delivered by a rule nobody has argued for yet.
//
// The second is an escalation: a developer or a reviewer having said, in the
// round it reached, that the work item cannot be met as it stands. It is the one
// entry that asks for a decision in so many words, so a rule that delivered the
// stoppage and left this to be noticed would be delivering the expensive half of
// the same question and holding back the cheap one — which is the whole of what
// yoyodyne-ifd.303 was for.
//
// Which of them a docket entry describes is read from the run's own record rather
// than from the entry's prose, for the reason every other triage action reads it
// there: the entry says what was true when it was written, and a classification
// made by matching words is one that changes when somebody rewrites a sentence.
//
// # One at a time, and only what nobody has judged
//
// A pass delivers one stopped run, and a run is delivered once however many
// entries it was docketed under: the docket she reads folds a run's entries into
// one (triage.Fold), and this walks the same fold, so a run docketed as the
// stoppage and as the escalation a role raised from it is put to her as one
// question rather than one per entry.
//
// A delivery is a conversation turn, and a pass
// that delivered a backlog of them at once would hold the queue closed for as
// long as the development manager took to answer all of them — so the oldest
// goes first, and the next pass takes the next. What that costs is that a
// morning with five stopped runs reaches her over five passes, which is the
// right trade for a loop that goes on choosing work while she reads.
//
// A delivery that failed is made again, up to the record's bound and no faster
// than its retry delay. The pacing is the half that is easy to leave out and
// makes the bound meaningless without it: whatever drives this decides how often
// it looks, and the loop that does today looks once per pull, so three attempts
// counted and not paced would be three attempts inside one command.
//
// And a stoppage she has already been given a repair grant or a re-run for is
// left alone, whether or not the harness has carried that decision out. Both are
// recorded against the item's budget the moment she decides, and acted on later,
// so between the two the stopped run still reads as untouched to anything looking
// only at the run — which is where every stoppage somebody carried to her by hand
// also sits. A decision costing nothing leaves no counter anywhere the harness
// reads, and is seen instead as the entry's own closure: every decision closes
// the entry it settles, and an entry a decision still holds over is nothing this
// delivers. A decision to wait holds only until the moment it names, so the
// stoppage it settled is delivered again after that — which is what waiting asked
// for.
//
// # Why the spending pause and not the intake hold
//
// A delivery is a provider invocation, so the operator's pause covers it exactly
// as it covers a run and a conversation turn: a paused harness delivers nothing,
// and nothing is claimed, so the stoppage keeps its delivery for after the pause
// is lifted.
//
// The intake hold is deliberately not read here, and the difference is what each
// switch is for. Holding intake stops the harness choosing work; this chooses
// nothing, claims nothing, and starts nothing. What it produces is the
// development manager's judgment about work that has already stopped — which is
// the thing a held queue is usually waiting on, and holding it back would be the
// hold answering a question nobody asked it.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// EscalationDocket is the docket the stoppages are read from. It is read and
// never written: an entry stands as the record that work stopped however many
// times it is delivered, and delivering it settles nothing about it.
type EscalationDocket interface {
	List() ([]triage.Entry, error)
}

// EscalationRuns is the durable run state one delivery reads. It reads and
// never writes: what a stopped run's record says is evidence about the
// stoppage, and nothing here acts on the run.
type EscalationRuns interface {
	Load(runID string) (runstate.State, error)
}

// EscalationRecords is where the delivery of one docketed stoppage is claimed
// and settled. It is what makes the delivery at most once: a stoppage put in
// front of the development manager twice is the same evidence in front of her
// twice, which is how one authorized recovery becomes two decisions.
//
// It is satisfied by runstate.EscalationStore.
type EscalationRecords interface {
	Attempt(ctx context.Context, escalation runstate.Escalation) (runstate.Escalation, error)
	Settle(ctx context.Context, docketKey string, delivery runstate.Delivery) (runstate.Escalation, error)
	Withdraw(ctx context.Context, docketKey, problem string) error
	Find(docketKey string) (runstate.Escalation, bool, error)
}

// EscalationDecisions is the durable per-item record of what triage has already
// decided, which is the record the triage guards spend and refuse against. It is
// read to leave alone a stoppage the development manager has already given a
// repair grant or a re-run for — the two decisions that leave a mark the harness
// can read.
//
// Deciding and carrying out are two acts with a gap between them, and that gap
// is where this matters, and the gap is a scheduling interval rather than a
// person now without being any narrower. A repair grant is recorded the moment
// she decides, and the stopped run's blocker is not cleared until a carry-out
// acts on it, so a stoppage she settled an hour ago still looks exactly like one nobody
// has seen — to anything that reads the run alone. Delivering it again is the
// same evidence in front of her twice under two decisions, which is the harm
// this whole record exists to prevent, and it is the state every stoppage
// somebody carries to her by hand passes through.
//
// It is satisfied by runstate.TriageStore.
type EscalationDecisions interface {
	Counters(workItemID string) (runstate.TriageCounters, error)
}

// EscalationReruns is what the harness has already carried out of those
// decisions: the re-runs claimed for one work item. It is the other half of the
// same question — a decision recorded and a decision acted on are both reasons
// to leave a stoppage alone, and only the two records together tell either from
// a stoppage nobody has looked at.
//
// The repair carry-out needs neither: it continues the stopped run itself and
// clears its blocker as it does, so a repaired stoppage stops being one this
// delivers by the same rule that decides every other run.
//
// It is satisfied by runstate.RerunStore.
type EscalationReruns interface {
	Claimed(workItemID string) ([]runstate.Rerun, error)
}

// DevelopmentManager is her conversation as the harness reaches it: one stoppage
// delivered into it, and what she recorded about it in answer.
//
// Nothing here decides anything on her behalf and nothing carries her decision
// out. What comes back is read from what she actually recorded, so a delivery
// she answered in prose and decided nothing about reports exactly that.
type DevelopmentManager interface {
	Judge(ctx context.Context, entry triage.Entry) (Judgment, error)
}

// Judgment is what came back from one delivery: her conversation, the triage
// decision she recorded about the stoppage where she recorded one, and what the
// turn cost.
type Judgment struct {
	ConversationID string `json:"conversation_id,omitempty"`
	// CostUSD is what the provider charged for the turn, as it reported it. It is
	// carried back because the delivery is a spend the caller made rather than one
	// a run made, so a session counting what it has spent has no other way to see
	// it. A turn that failed carries what it cost too: the provider charged for it
	// exactly as it charges for one that answered.
	CostUSD float64 `json:"cost_usd,omitempty"`
	// Decision is the triage decision she recorded about this stoppage, in the
	// vocabulary the conversation records decisions in, and Reason the reasoning
	// she gave for it. Both are empty where she decided nothing, which is an
	// answer rather than a failure.
	Decision string `json:"decision,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// ErrConversationUnreachable reports a delivery that failed before the
// development manager was asked anything: her conversation could not be opened
// at all. It is its own error because it is a failure whose attempt is provably
// worth giving back — nothing was said to her, and nothing was spent.
var ErrConversationUnreachable = errors.New("the development manager's conversation could not be opened")

// ErrDeliveryCancelled reports a delivery whose turn a cancellation killed
// before any answer of hers existed — a signal, a shutdown, a process-group
// teardown taken out from under the pull. It is separate from an unreachable
// conversation because it is a different fact about the same delivery and both
// are recorded in words a person reads: her conversation opened perfectly well,
// and what stopped the delivery was the harness's own death.
//
// It is the second failure whose attempt is worth giving back, and it is worth
// giving back for the reason stated on the Withdraw contract it is spent
// against: the turn ended before a reply existed, so nothing of hers was parsed,
// no action was applied, and the item's durable triage record never moved. A
// second delivery of it therefore cannot become a second decision, which is the
// harm the attempt record exists to prevent.
//
// What it does not claim is that she never saw the message. The provider had the
// prompt from the moment the invocation started. The claim is the narrower one
// the give-back actually rests on: this turn decided nothing and carried nothing
// out.
var ErrDeliveryCancelled = errors.New("the delivery was cancelled before the development manager answered")

// notTaken reports a delivery failure whose attempt is given back rather than
// spent. Both members are failures that produced no answer of hers, and every
// other ending is left as an attempt she may have been asked.
func notTaken(err error) bool {
	return errors.Is(err, ErrConversationUnreachable) || errors.Is(err, ErrDeliveryCancelled)
}

// undelivered says what became of a delivery whose attempt is being given back,
// in the words each of the two failures actually earns.
//
// The two are not the same sentence, and writing them as one would put the claim
// ErrDeliveryCancelled explicitly refuses to make into the record a person reads:
// a cancelled turn is not a stoppage that was never put to her — the provider
// had the prompt from the moment the invocation started — it is a turn that
// ended before she answered.
func undelivered(entry triage.Entry, judgeErr error) string {
	if errors.Is(judgeErr, ErrDeliveryCancelled) {
		return fmt.Sprintf("the turn putting %s to the development manager ended before she answered", putToHer(entry))
	}
	return fmt.Sprintf("%s was not put to the development manager", putToHer(entry))
}

// undeliveredAndWaiting is that sentence with what happens next on it, which is
// the same promise either way: the attempt came back, and the entry is put to
// her once the delay the record keeps has passed.
func undeliveredAndWaiting(entry triage.Entry, judgeErr error, delay time.Duration) string {
	if errors.Is(judgeErr, ErrDeliveryCancelled) {
		return fmt.Sprintf("%s, and it will be put to her again once %s has passed", undelivered(entry, judgeErr), delay)
	}
	return fmt.Sprintf("%s and will be once %s has passed", undelivered(entry, judgeErr), delay)
}

// putToHer names what one entry puts in front of the development manager, in the
// words its class earns. The two are not the same thing and must not be reported
// as one: a stoppage is a run that spent its budget and failed, and an escalation
// is a role saying in the round it reached that the item cannot be met — an
// operator told the second in the words of the first would go looking for a
// failure nobody had.
func putToHer(entry triage.Entry) string {
	if permanentRefusal(entry) {
		return "a carry-out refused by a gate that will not clear on its own"
	}
	if entry.Class == triage.ClassEscalation {
		return "the escalation raised by run " + entry.RunID
	}
	return "the stoppage of run " + entry.RunID
}

// recordGrace bounds a record write that is deliberately outliving the delivery
// it belongs to. The write it makes is one small file and a lock nothing holds
// for long, so what this is really bounding is the wait for a lock another
// process is holding: a detached context with no bound of its own would be a
// shutdown that cannot finish because something else in it has not.
const recordGrace = 5 * time.Second

// recordContext is the context a delivery writes its own record under: the
// delivery's own, with its cancellation dropped and a bound of its own put on.
//
// Two writes need it, and they need it for the same death. The give-back exists
// for a turn a cancellation killed; the settle writes down the turn that
// answered. In a shutdown that cancellation is the delivery's own context being
// cancelled — the largest class of the deaths either write covers, and the one
// where each matters most. The record's read-modify-write is serialized by a
// lock whose wait honours the context it is given, so either write running under
// the delivery's context is one that fails the moment another process holds the
// record: the give-back takes its fallback branch and the attempt the harness's
// own death took is spent after all, and the settle leaves a delivery she
// answered with no moment of delivery on it, which the next pass puts to her a
// second time. Both are exactly what the writes exist to prevent. Detaching
// keeps everything the delivery's context carried and drops only the
// cancellation.
//
// A process-group teardown, where the child is killed and the harness lives, is
// covered either way — nothing cancelled the pull's context there. This is what
// makes the shutdown covered too rather than covered by luck about whether
// anything else wanted the record at that moment.
func recordContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), recordGrace)
}

// trackerRecordGrace bounds a tracker write that is deliberately outliving the
// pass or decision it belongs to. recordGrace is sized for one small file, and
// a tracker write is not one: the tracker client gives each bd call
// beads.DefaultTimeout and, where one is stopped, reads the item back and asks
// again (beads.Client), and a busy store takes several seconds to answer even a
// call that succeeds. Five seconds decided those writes before the tracker had
// had its turn and reported them timed out. This is longer than the most a
// write can take with the client's own retries — three calls and two
// read-backs at that limit each, the waits between them, and the write lock
// before each call, eight limits and ten seconds in all — so the client's
// limits are what decide, and this only keeps a detached write from outliving
// them.
const trackerRecordGrace = 10 * beads.DefaultTimeout

// trackerRecordContext is recordContext for a write that goes to the work
// tracker, for the reason recordContext detaches and bounded by
// trackerRecordGrace rather than recordGrace.
func trackerRecordContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), trackerRecordGrace)
}

// trackerCallContext bounds one tracker call, or one read and the write that
// follows it, that is given a deadline of its own: by trackerRecordGrace, so
// the tracker client's limits decide when it has failed rather than a deadline
// shorter than a busy store takes to answer. Unlike trackerRecordContext it
// keeps ctx's cancellation.
func trackerCallContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, trackerRecordGrace)
}

// Escalator delivers docketed stoppages to the development manager. It has no
// tracker, no worktree access, and no forge access, and it starts nothing: what
// it does is put evidence somebody already recorded in front of the role whose
// judgment it is, and write down that it did.
type Escalator struct {
	Docket EscalationDocket
	Runs   EscalationRuns
	// Records is what makes a delivery at most once. Required: a delivery nothing
	// records is one every pass makes again.
	Records EscalationRecords
	// Decisions and Reruns are what triage has already decided about the item and
	// what has been carried out of it. Both required: a delivery made without
	// reading them is one made to a development manager who settled this stoppage
	// an hour ago, which is the failure this is here to avoid rather than one to
	// degrade gracefully into.
	Decisions EscalationDecisions
	Reruns    EscalationReruns
	Manager   DevelopmentManager
	// Holds is the operator's pause over everything the harness would spend.
	// Optional, and an escalator wired without one is one nothing can pause,
	// which is what every provider invocation was before the switch existed.
	Holds OperatorHolds
	Clock execution.Clock
}

// Escalated is one stoppage this pass put in front of the development manager,
// and what came back. It reports a delivery that did not happen as carefully as
// one that did: a stoppage nobody heard about is the state this exists to end,
// so it is never reported as a pass that did nothing.
type Escalated struct {
	WorkItemID string `json:"work_item_id"`
	RunID      string `json:"run_id"`
	DocketKey  string `json:"docket_key"`
	// Class is which kind of entry was put to her, so what a pass reports says
	// what she was actually shown. A run that spent its budget and failed and a
	// role saying the item cannot be met are opposite facts about the same item,
	// and an operator told one in the words of the other goes looking for
	// something nobody recorded.
	Class     triage.Class `json:"class,omitempty"`
	Delivered bool         `json:"delivered"`
	// Decision is what she recorded about the stoppage, and is empty where she
	// recorded nothing.
	Decision string `json:"decision,omitempty"`
	// CostUSD is what the turn cost, as the provider reported it. It is on the
	// sweep so that whatever drives the delivery can count it: a delivery is a
	// spend made by the caller rather than by a run, and a session bounded by a
	// budget must not spend past it on turns nothing counted.
	CostUSD float64 `json:"cost_usd,omitempty"`
	// Problem is what stopped a delivery, and says whether the stoppage will be
	// tried again.
	Problem string `json:"problem,omitempty"`
}

// EscalationSweep is what one pass did. A pass that found nothing to deliver
// reports nothing, which is every pass on a harness whose work is landing.
type EscalationSweep struct {
	Escalated []Escalated `json:"escalated,omitempty"`
	// Paused is the operator's pause, when one is what stopped this. Nothing was
	// claimed and the stoppage keeps its delivery.
	Paused *runstate.OperatorHold `json:"paused,omitempty"`
}

// Escalate delivers the oldest stoppage the development manager has not been
// shown, and reports what came of it.
//
// The order is the order the guarantees need. The pause is read before anything
// is claimed, so a paused harness costs the stoppage nothing; the attempt is
// recorded before the turn is taken, so a process that dies between the two has
// recorded a delivery nobody made rather than made one nobody recorded; and what
// came back is written down after, because until she has answered there is
// nothing to write.
//
// A run whose record cannot be read is skipped rather than failing the pass: a
// sweep that refuses to run because one run is odd is a sweep that delivers
// nothing, and the stoppages beside it are exactly the ones somebody needs.
func (e Escalator) Escalate(ctx context.Context) (EscalationSweep, error) {
	if err := e.validate(); err != nil {
		return EscalationSweep{}, err
	}
	hold, held, err := e.paused()
	if err != nil {
		return EscalationSweep{}, err
	}
	if held {
		return EscalationSweep{Paused: &hold}, nil
	}
	entries, err := e.Docket.List()
	if err != nil {
		return EscalationSweep{}, fmt.Errorf("read the triage docket: %w", err)
	}
	var sweep EscalationSweep
	var problems []error
	// A permanent carry-out refusal is a new question even if this stoppage
	// was already delivered and decided. Join the live record before deciding
	// whether the entry's closure still settles it.
	var live []triage.Entry
	docketedRuns := docketedRunsOf(entries)
	attached := make(map[string]bool)
	for _, entry := range entries {
		counters, readErr := e.Decisions.Counters(entry.WorkItemID)
		if readErr != nil {
			problems = append(problems, fmt.Errorf("read what triage has already decided about %s and any carry-out refusals: %w", entry.WorkItemID, readErr))
			continue
		}
		if refusal, found := counters.RefusedCarryOut(entry.RunID); found && refusal.Cause != "" {
			entry.CarryOut = docketedCarryOut(entry, counters)
		} else if latest := undocketedCarryOut(entry, counters, docketedRuns); latest != nil && latest.Cause != "" && !attached[entry.WorkItemID] {
			// A finding about a run without a docket entry is shown on the item's
			// other entries, just as it is on the manager's docket.
			if decision, found := counters.LatestDecision(); found {
				if refusal, refused := counters.RefusedCarryOut(decision.RunID); refused && refusal.Cause != "" {
					entry.CarryOut = latest
					attached[entry.WorkItemID] = true
				}
			}
		}
		live = append(live, entry)
	}
	entries = live
	// delivering is this pass still having its one delivery to make. It goes false
	// as soon as one is made, and on a store that would not record one: the
	// delivery this pass would make next is the one the next pass makes, and a
	// turn spent on a record nothing bounds is what the record exists to prevent.
	// The scan carries on either way, because the stoppages behind it may need
	// saying even where none of them is being delivered.
	delivering := true
	for _, run := range e.perRun(entries) {
		standing, entry, err := e.runStandingOf(run)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		switch standing {
		case standingAwaiting:
			if !delivering {
				continue
			}
			escalated, attempted, err := e.deliver(ctx, entry, run)
			if err != nil {
				problems = append(problems, err)
				delivering = false
				continue
			}
			if !attempted {
				// Another process delivered this stoppage between the reading and the
				// claim, which is the record doing its job. The next entry is this
				// pass's to look at.
				continue
			}
			sweep.Escalated = append(sweep.Escalated, escalated)
			delivering = false
		case standingSettled, standingDelivered, standingCooling, standingAbandoned:
			// Nothing to do and nothing to say. A settled stoppage is somebody
			// else's business now, and a cooling one was reported by the pass that
			// attempted it — saying it again on every pull between here and its next
			// attempt would bury what the pass actually did.
			//
			// An abandoned one was said on every pass until yoyodyne-ifd.288, on the
			// reasoning that nothing else would mention it. Something else does: the
			// read model holds the item for a person and says its stoppage could not
			// be put to the development manager, so every operator surface carries it
			// for as long as it is true — readmodel.heldForAPerson derives that from
			// the escalation record alone, and its test pins the spent case as "needs
			// a person". What restating it here bought was a session
			// start that opened with twelve sentences about stoppages nothing was
			// going to do anything about, which is the standing fact drowning out
			// what the pass did rather than the silence this exists to end.
		}
	}
	return sweep, errors.Join(problems...)
}

// What one docket entry is to this pass.
type escalationStanding int

const (
	// standingSettled is a stoppage this pass has nothing to do or say about:
	// not the stoppage this delivers, one she has already been shown, or one she
	// has already judged.
	standingSettled escalationStanding = iota
	// standingAwaiting is a stoppage nobody has put to her and the harness still
	// will.
	standingAwaiting
	// standingCooling is one whose last attempt failed too recently to be worth
	// repeating yet. The pass that made that attempt is what said so; repeating
	// the sentence every pull would bury it.
	standingCooling
	// standingAbandoned is one the harness tried and stopped trying. This pass
	// makes no further attempt at it and says nothing about it: what it needs is a
	// person, and the surface that asks for one is the read model's account of
	// work held for somebody rather than a line on every pull.
	standingAbandoned
	// standingDelivered is a stoppage already put to her and answered, or put to
	// her with the answer not yet recorded. It is settled as far as this pass is
	// concerned, and it is told apart from the rest of settled for one reason: a
	// run docketed twice is one question, so either of its entries having been
	// delivered is the run having been delivered.
	standingDelivered
)

// perRun is the docket as this pass walks it: the open entries, one per stopped
// run, as the docket she reads folds them (triage.Fold). A run docketed as a
// stoppage and then as the escalation a role raised from it is one question to
// her, and walking the entries one at a time put it to her once per entry.
func (e Escalator) perRun(entries []triage.Entry) []triage.Entry {
	now := e.now()
	open := make([]triage.Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.Closed != nil && entry.Closed.Holds(now) && !permanentRefusal(entry) {
			continue
		}
		open = append(open, entry)
	}
	return triage.Fold(open)
}

// runStandingOf reports what one stopped run is to this pass, from every open
// entry it was docketed under, and which of them a delivery is recorded against.
//
// The run is delivered once. Any of its entries delivered already settles it,
// and any attempt still cooling or given up on holds it back, because another
// entry of the same run delivered beside those would be the same stoppage put to
// her a second time under a key the first delivery was not recorded on. Where
// nothing holds it back, the delivery is recorded against the first entry owed
// one — the latest, which is the one the docket lists it by, where that is owed
// one — and she is shown the whole run, every entry folded beneath it.
func (e Escalator) runStandingOf(run triage.Entry) (escalationStanding, triage.Entry, error) {
	head := run
	head.Earlier = nil
	members := append([]triage.Entry{head}, run.Earlier...)
	// A permanent refusal is a new event even when folding leaves it beneath
	// a newer entry whose original stoppage was already delivered. Ask about
	// that event first, under the key of the entry carrying it, so the original
	// delivery cannot suppress it and a later fold cannot deliver it again.
	for _, member := range members {
		if permanentRefusal(member) {
			standing, err := e.standingOf(member)
			return standing, member, err
		}
	}
	var awaiting *triage.Entry
	cooling, abandoned := false, false
	var problems []error
	for index := range members {
		standing, err := e.standingOf(members[index])
		if err != nil {
			problems = append(problems, err)
			continue
		}
		switch standing {
		case standingDelivered:
			return standingDelivered, members[index], nil
		case standingCooling:
			cooling = true
		case standingAbandoned:
			abandoned = true
		case standingAwaiting:
			if awaiting == nil {
				awaiting = &members[index]
			}
		}
	}
	// A run whose record could not be read for one of its entries is not
	// delivered on the others: whether that entry was already put to her is what
	// could not be read.
	if len(problems) > 0 {
		return standingSettled, head, errors.Join(problems...)
	}
	switch {
	case cooling:
		return standingCooling, head, nil
	case abandoned:
		return standingAbandoned, head, nil
	case awaiting != nil:
		return standingAwaiting, *awaiting, nil
	default:
		return standingSettled, head, nil
	}
}

// standingOf reports what one docket entry is to this pass.
//
// It asks the run's own record rather than the entry, for the reason every
// triage action does: the entry says what was true when it was written, and what
// decides whether this is the 2-of-2 stoppage is what the run recorded about how
// it ended.
func (e Escalator) standingOf(entry triage.Entry) (escalationStanding, error) {
	if entry.Class != triage.ClassStoppedRun && entry.Class != triage.ClassEscalation && !permanentRefusal(entry) {
		return standingSettled, nil
	}
	// A decided entry is settled whatever the decision was, which is the half
	// alreadyJudged below cannot see: waiting, re-scoping, and escalating spend no
	// counter, so a stoppage settled that way used to be delivered again the next
	// time a pass reached it. The closure is what says somebody looked — for as
	// long as it holds, which on a decision to wait is until the moment it names.
	if entry.Closed != nil && entry.Closed.Holds(e.now()) && !permanentRefusal(entry) {
		return standingSettled, nil
	}
	recorded, found, err := e.Records.Find(entry.Key)
	if err != nil {
		return standingSettled, fmt.Errorf("read whether the stoppage of run %s has been put to the development manager: %w", entry.RunID, err)
	}
	// A record about an earlier stoppage under this key says nothing about this
	// one. The key names the run, a repair continues the run that stopped, and a
	// repaired run that dies again is docketed afresh under it — so what was
	// delivered and decided last time is the last stoppage's, and this one is
	// still owed its delivery.
	if found && !recorded.AboutEvent(deliveryMoment(entry), carryOutRefusedAt(entry)) {
		found = false
	}
	if found && recorded.Delivered() {
		return standingDelivered, nil
	}
	if !permanentRefusal(entry) {
		state, err := e.Runs.Load(entry.RunID)
		if err != nil {
			return standingSettled, fmt.Errorf("read the run the docket entry is about: %w", err)
		}
		if !deliveredToTheManager(entry.Class, state) {
			return standingSettled, nil
		}
		// Asked before the record's own state, so a stoppage she has since judged
		// stops being this pass's business whether the harness delivered it, gave up
		// delivering it, or never reached it at all.
		judged, err := e.alreadyJudged(entry)
		if err != nil {
			return standingSettled, err
		}
		if judged {
			return standingSettled, nil
		}
	}
	switch {
	case found && recorded.Spent():
		return standingAbandoned, nil
	case found && recorded.Cooling(e.now()):
		return standingCooling, nil
	default:
		return standingAwaiting, nil
	}
}

// A refusal is delivered once as its own event, under the existing stoppage's
// key. Its time distinguishes it from the original stoppage's delivery and
// from a later decision refused on the same run.
func permanentRefusal(entry triage.Entry) bool {
	return entry.CarryOut != nil && entry.CarryOut.Cause != ""
}

func deliveryMoment(entry triage.Entry) time.Time {
	if permanentRefusal(entry) {
		return entry.CarryOut.RefusedAt
	}
	return entry.RecordedAt
}

func carryOutRefusedAt(entry triage.Entry) time.Time {
	if permanentRefusal(entry) {
		return entry.CarryOut.RefusedAt
	}
	return time.Time{}
}

// alreadyJudged reports a stoppage the development manager has settled, whether
// or not the harness has carried her decision out.
//
// Three states say she has, and the item's own durable triage record is where
// all three live. A repair grant recorded and not yet spent is a decision
// standing: she gave the item another round, and until a carry-out takes it the
// stopped run still carries the blocker that makes it look untouched. A re-run recorded and not yet claimed is the same fact for the
// other carry-out. And a re-run already claimed against this entry is her
// decision acted on, which the run's record cannot say either, because a re-run
// starts a fresh run and leaves the stopped one exactly as it was.
//
// What it cannot see is a decision that spends nothing — an escalation to the
// operator, a re-scope, a wait. Those leave no counter anywhere this reads, and
// what says somebody looked is the decision recorded on the entry, which
// standingOf asks before it asks this.
//
// None of the three holds the same run stopping again after the decision. A
// grant is outstanding only until the attempts it bought are judged, and the one
// stoppage this delivers is the run's reviewer still requiring repair after every
// permitted attempt — so a repaired run that dies again that way has spent the
// grant that continued it, and the counters no longer say a decision is
// standing. A re-run starts a fresh run under its own identifier and leaves the
// stopped one settled, so the run a re-run was claimed against never stops
// again, and a claim under this key is always about the stoppage on the docket.
func (e Escalator) alreadyJudged(entry triage.Entry) (bool, error) {
	counters, err := e.Decisions.Counters(entry.WorkItemID)
	if err != nil {
		return false, fmt.Errorf("read what triage has already decided about %s: %w", entry.WorkItemID, err)
	}
	if counters.GrantOutstanding() {
		return true, nil
	}
	claimed, err := e.Reruns.Claimed(entry.WorkItemID)
	if err != nil {
		return false, fmt.Errorf("read the re-runs already taken of %s: %w", entry.WorkItemID, err)
	}
	for _, existing := range claimed {
		if existing.DocketKey == entry.Key {
			return true, nil
		}
	}
	return counters.Reruns > len(claimed), nil
}

// deliveredToTheManager reports one docket entry being one this pass puts in
// front of the development manager, asked of the run's own record rather than of
// the entry.
//
// The two classes ask different questions of it and neither answers for the
// other. A stoppage has to be the 2-of-2 review one, because the docket carries
// several kinds of stoppage and only that one's next step is a judgment. An
// escalation needs no such narrowing: a role said in so many words that the item
// cannot be met, so what is asked is only that the record still says so.
func deliveredToTheManager(class triage.Class, state runstate.State) bool {
	if class == triage.ClassEscalation {
		return state.Escalated()
	}
	return reviewRepairStoppage(state)
}

// reviewRepairStoppage reports the stoppage this delivers: a run that ended on a
// durable blocker with its independent reviewer still requiring repair after
// every permitted attempt.
//
// The last two conditions are what keep it to that stoppage. A run handed back a
// failing check or a refused path never reached a reviewer on the change it
// stopped with, and its record says so — the check failure and the path refusal
// each clear the review evidence beside them as they are recorded, so a run
// carrying either stopped on something other than a reviewer's judgment however
// many reviews it had before.
func reviewRepairStoppage(state runstate.State) bool {
	return stoppedRun(state) &&
		state.ReviewDecision == runstate.ReviewRepair &&
		state.CheckFailure == nil &&
		state.PathRefusal == nil
}

// deliver puts one docketed entry in front of the development manager and
// records what came back.
//
// A delivery that produced no answer of hers gives the attempt back — her
// conversation could not be opened, or a cancellation killed the turn before a
// reply existed — so the entry keeps the delivery it is owed and the next
// pass makes it. Every other failure keeps the attempt, because a turn that
// answered is one this cannot claim did not — and the record is bounded, so a
// delivery that goes on failing stops rather than spending every pass on it.
// It reports whether an attempt was made at all, so an entry another process
// claimed between the reading and the claim leaves this pass looking at the next
// one rather than reporting a delivery nobody made.
//
// The delivery is recorded against one entry and put to her as the whole run:
// run is the run's live entry with every other open docketing of it folded
// beneath, which is what she is asked to decide, and one decision settles all
// of it.
func (e Escalator) deliver(ctx context.Context, entry, run triage.Entry) (Escalated, bool, error) {
	escalated := Escalated{WorkItemID: entry.WorkItemID, RunID: entry.RunID, DocketKey: entry.Key, Class: entry.Class}
	attempted, err := e.Records.Attempt(ctx, runstate.Escalation{
		DocketKey:  entry.Key,
		RunID:      entry.RunID,
		WorkItemID: entry.WorkItemID,
		// Which stoppage under this key is being delivered, so a record left by an
		// earlier one — delivered, decided, and the work then stopped again — starts
		// a fresh delivery rather than refusing this one as already made.
		DocketedAt:        deliveryMoment(entry),
		CarryOutRefusedAt: carryOutRefusedAt(entry),
		FirstAttemptedAt:  e.now(),
	})
	if err != nil {
		// An entry another process claimed between the reading above and this one
		// is not a failure of either: the record refused the second, which is what
		// it is for. Both refusals mean the same thing here — one of them delivered
		// it, or one of them is about to — and neither is this pass's to report.
		if errors.Is(err, runstate.ErrEscalationSpent) || errors.Is(err, runstate.ErrEscalationCooling) {
			return Escalated{}, false, nil
		}
		return Escalated{}, false, fmt.Errorf("record that %s is being put to the development manager: %w", putToHer(entry), err)
	}
	judgment, judgeErr := e.Manager.Judge(ctx, run)
	// What the turn cost is carried whichever way it went, because the provider
	// charges for a turn that failed exactly as for one that answered.
	escalated.CostUSD = judgment.CostUSD
	if notTaken(judgeErr) {
		escalated.Problem = fmt.Sprintf("%s: %v",
			undeliveredAndWaiting(entry, judgeErr, runstate.EscalationRetryDelay), judgeErr)
		// Under a context detached from this delivery's, because a shutdown
		// cancels the very context the delivery ran under: giving the attempt back
		// under it would fail in the case the give-back is most for.
		giveBack, stopGivingBack := recordContext(ctx)
		defer stopGivingBack()
		if err := e.Records.Withdraw(giveBack, entry.Key, judgeErr.Error()); err != nil {
			escalated.Problem = fmt.Sprintf("%s, and the attempt taken for it could not be given back, so it has spent one of %d on a turn that decided nothing: %v",
				undelivered(entry, judgeErr), runstate.MaxEscalationAttempts, err)
		}
		return escalated, true, nil
	}
	delivery := runstate.Delivery{At: e.now(), ConversationID: judgment.ConversationID, Decision: judgment.Decision, Reason: judgment.Reason}
	if judgeErr != nil {
		// The turn may have been taken and may have cost money, and nothing here
		// can tell. So the attempt stands and the delivery does not: the record
		// says the development manager may not have been asked, which is the state
		// somebody has to be able to find, and the bounded retry is what stops that
		// honesty from becoming a loop.
		delivery = runstate.Delivery{Problem: judgeErr.Error()}
		escalated.Problem = fmt.Sprintf("putting %s to the development manager failed on attempt %d of %d: %v",
			putToHer(entry), attempted.Attempts, runstate.MaxEscalationAttempts, judgeErr)
	} else {
		escalated.Delivered = true
		escalated.Decision = judgment.Decision
	}
	// Under a context detached from this delivery's, for the reason the give-back
	// above is: a shutdown cancels the very context the delivery ran under, and it
	// can land between her answer arriving and this write. A settle that failed
	// there would leave the attempt standing with no delivery recorded against it,
	// so a later pass puts an entry she has already answered to her a second time
	// — and nothing else would catch it, because a decision that spends no counter
	// is one alreadyJudged cannot see.
	settle, stopSettling := recordContext(ctx)
	defer stopSettling()
	if _, err := e.Records.Settle(settle, entry.Key, delivery); err != nil {
		escalated.Problem = strings.TrimSpace(escalated.Problem + fmt.Sprintf(
			"; what became of putting the stoppage of run %s to the development manager could not be recorded, so the record still says it may not have reached her: %v",
			entry.RunID, err))
	}
	return escalated, true, nil
}

// paused reports the operator's pause over everything the harness spends. A
// pause that cannot be read refuses the pass rather than being spent through,
// exactly as it does everywhere else it is read.
func (e Escalator) paused() (runstate.OperatorHold, bool, error) {
	if e.Holds == nil {
		return runstate.OperatorHold{}, false, nil
	}
	hold, held, err := e.Holds.Held()
	if err != nil {
		return runstate.OperatorHold{}, false, fmt.Errorf("read whether the operator has paused harness activity: %w", err)
	}
	return hold, held, nil
}

func (e Escalator) validate() error {
	var problems []error
	if e.Docket == nil {
		problems = append(problems, errors.New("escalating requires the triage docket the stoppages are on"))
	}
	if e.Runs == nil {
		problems = append(problems, errors.New("escalating requires the durable run state, because which stoppage an entry describes is read from the run's own record"))
	}
	if e.Records == nil {
		problems = append(problems, errors.New("escalating requires the record that bounds it to one delivery per docketed stoppage"))
	}
	if e.Decisions == nil {
		problems = append(problems, errors.New("escalating requires the item's durable triage record, because a stoppage the development manager has already decided must not be put to her again"))
	}
	if e.Reruns == nil {
		problems = append(problems, errors.New("escalating requires the re-runs already carried out, which is the other half of what says a stoppage has been judged"))
	}
	if e.Manager == nil {
		problems = append(problems, errors.New("escalating requires the development manager's conversation to deliver into"))
	}
	return errors.Join(problems...)
}

func (e Escalator) now() time.Time {
	if e.Clock == nil {
		return execution.RealClock{}.Now().UTC()
	}
	return e.Clock.Now().UTC()
}

// Render describes what one pass did, for whoever asked for it.
func (s EscalationSweep) Render() string {
	var rendered strings.Builder
	if s.Paused != nil {
		fmt.Fprintf(&rendered, "PAUSED: no stopped work was put to the development manager, since %s\n",
			s.Paused.HeldAt.UTC().Format(time.RFC3339))
	}
	for _, escalated := range s.Escalated {
		if escalated.Delivered {
			put := putToHer(triage.Entry{Class: escalated.Class, RunID: escalated.RunID})
			if decision := strings.TrimSpace(escalated.Decision); decision != "" {
				fmt.Fprintf(&rendered, "put %s to the development manager, who triaged %s as %q\n",
					put, escalated.WorkItemID, decision)
			} else {
				fmt.Fprintf(&rendered, "put %s to the development manager, who recorded no decision about %s\n",
					put, escalated.WorkItemID)
			}
		}
		if escalated.Problem != "" {
			fmt.Fprintln(&rendered, escalated.Problem)
		}
	}
	return rendered.String()
}
