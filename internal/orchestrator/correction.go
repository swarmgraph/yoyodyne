package orchestrator

// Waking the role whose tracker block the harness refused, with nobody standing
// there to do the waking.
//
// A refused block already reaches the role that sent it: the refusal is durable,
// and the harness's own words open that conversation's next turn, so the role can
// re-issue the actions itself rather than waiting to be told. What was missing was
// the turn. Nothing started one, so the correction waited on a person opening the
// conversation — and both of this week's refused batches, the oversized park
// reason and the malformed handle id, needed the operator's assistant to prompt
// the re-issue of actions the product manager could have corrected herself.
//
// So the harness starts it. What that changes is the courier and nothing else: the
// refusal is the one the loud-refusal machinery already wrote, the correction is
// the role's own reply, and the actions happen only if it issues them again.
//
// This is the fallback rather than the first answer. A refusal earned with
// rounds still left in its message is handed straight back as the next of them
// (chat.Session.Send), so the role corrects it before its reply ends and no
// wakeup is ever owed. What reaches this pass is a refusal on a message's last
// round, and one whose hand-back round never came back.
//
// # One turn per refusal, and then the operator
//
// A refusal is put to the role once. The claim is durable and is taken before the
// turn, so a pass that dies between the two has recorded a wakeup nobody made
// rather than made one nobody recorded — the second is what would fire again on
// the next pass, and on every pass after it. The wakeups that put nothing in
// front of the role are the ones the provider never took — refused for want of
// capacity, or answering nobody because it is down or the account's login has
// lapsed: no model saw the message, so the turn it never took comes back and is
// made again. The attempt does not come back with it — that is what keeps the
// retry bounded rather than a wakeup every quarter of an hour for ever — and the
// last attempt spent that way hands the refusal to the operator, said where every
// other unanswered refusal is said, rather than leaving it to go quiet.
//
// What a turn that fails to put the actions back earns is the operator rather
// than another wakeup. A role that sends a block refused again has shown that
// another copy of the same message will not fix it, and one that answers without
// asking for any tracker action has ended the correction with the actions exactly
// as lost; a harness that kept waking either would spend a turn a pass on a
// conversation that is not going to answer. Both endings are recorded where the
// refusal is, by the conversation itself, and both are what the operator is told;
// see chat.Session.recordRefusedTrackerBlock and settleRefusedTrackerBlock.
//
// # The same trigger class as the schedule
//
// This is fired from the pull, beside the recurring tasks and the stopped-work
// delivery, and for the same three reasons. The harness is the only thing that
// invokes a role, so a wakeup living in a cron entry or a launchd job would be a
// second invoker of one. The pull is where the harness is already deciding what to
// do next, so a refusal recorded at any hour is woken for at the next interval
// rather than at the next time somebody looks. Its turn is taken beside the
// pull, on the path the recurring firings take theirs, so a watch goes on pulling
// while the role answers, and a refusal in a conversation one of that session's
// passes is using waits for the pass rather than being woken into it; see
// Scheduler.correct. And it runs on the non-model side,
// so a provider window or outage that stops turns does not stop the wakeup being
// scheduled or silently spend it: a turn the provider never took is given back
// and made again once the delay has passed, a fixed number of times.
//
// # The pause and not the intake hold
//
// A wakeup is a provider invocation, so the operator's pause covers it exactly as
// it covers a run, a conversation turn, a delivery, and a firing. The intake hold
// is deliberately not read: holding intake stops the harness choosing work, and
// this chooses nothing, claims no work item, and starts no run — what it produces
// is a role putting right its own lost actions, which is usually part of what a
// held queue is waiting on.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// CorrectionConversations is where the refusals are read from. It is read and
// never written here: what a conversation records about a refused block is that
// conversation's own, and this only asks which of them is owed a turn.
//
// It is satisfied by *runstate.ConversationStore.
type CorrectionConversations interface {
	Recorded() ([]runstate.Conversation, error)
}

// CorrectionClaims is where one wakeup is claimed before it is made. It is what
// makes the wakeup at most once per refusal, and a trigger wired without it is
// one that would wake the same conversation on every pull.
//
// It is satisfied by *runstate.ConversationStore.
type CorrectionClaims interface {
	ClaimRefusalWakeup(identity runstate.ConversationIdentity, at time.Time) (runstate.TrackerRefusal, error)
	WithdrawRefusalWakeup(ctx context.Context, identity runstate.ConversationIdentity, turn int, why string) (runstate.TrackerRefusal, error)
}

// ErrProviderWindow reports a wakeup the provider refused for want of capacity.
// It is its own sentinel because it provably put nothing in front of the role and
// provably clears: no model saw the message, and the window ends.
var ErrProviderWindow = errors.New("the provider refused the wakeup for want of capacity")

// ErrProviderAway reports a wakeup the provider refused because nobody is logged
// into it or nobody can reach it. It put nothing in front of the role exactly as
// a window does, and it is told apart from one because what ends it is different:
// a window resets on its own, and an outage or a lapsed login ends when the
// network or a person does something — so what the pass says names that.
//
// These two are the failures worth asking again on a timer. Every other way a
// wakeup fails either reached the role or is something about the configuration a
// person has to change, and neither is.
var ErrProviderAway = errors.New("the provider answered nobody, so the wakeup was never taken")

// neverTaken reports a wakeup failure the provider never took, whose turn is
// given back and made again, paced and bounded.
func neverTaken(err error) bool {
	return errors.Is(err, ErrProviderWindow) || errors.Is(err, ErrProviderAway)
}

// CorrectionRole is a role's conversation as the harness reaches it: one message
// sent into it, and what came back.
//
// Nothing here decides anything on the role's behalf and nothing carries its
// decisions out. The refusal the woken turn answers is the one already waiting in
// that conversation's own record, so what is sent is why it was woken and nothing
// the harness invented about what was wrong.
type CorrectionRole interface {
	Wake(ctx context.Context, identity runstate.ConversationIdentity, message string) (CorrectionTurn, error)
}

// CorrectionTurn is what one woken turn came to.
type CorrectionTurn struct {
	ConversationID string `json:"conversation_id,omitempty"`
	// CostUSD is what the provider charged for the turn, as it reported it. It is
	// carried back because a wakeup is a spend the caller made rather than one a
	// run made, so a session counting what it has spent has no other way to see
	// it. A turn that failed carries what it cost too: the provider charged for it
	// exactly as it charges for one that answered.
	CostUSD float64 `json:"cost_usd,omitempty"`
	// Actions is how many tracker actions the woken turn got carried out, which is
	// the whole of what says the correction landed. A turn that answered in prose
	// re-issued nothing and reports none.
	Actions int `json:"actions,omitempty"`
	// Refused is the refusal the woken turn's own block earned, where it earned
	// one. It is not a failed turn — the role answered — and it is the ending that
	// goes to the operator rather than earning another wakeup.
	Refused string `json:"refused,omitempty"`
}

// Corrected is one refusal this pass woke a role for, and what came back. It
// reports a wakeup that did not happen as carefully as one that did.
type Corrected struct {
	ConversationID string           `json:"conversation_id,omitempty"`
	Role           domain.AgentRole `json:"role"`
	// Agent is which of a role's conversations was woken, because a project may
	// configure two agents for one role and the refusal belongs to one of them.
	Agent string `json:"agent,omitempty"`
	// Turn is the conversation turn whose block was refused, so what this pass says
	// names the loss rather than only the correction.
	Turn int `json:"turn"`
	// Woken reports a turn actually taken. A wakeup that never reached the role is
	// never reported as one that did.
	Woken bool `json:"woken"`
	// Attempt is which of the refusal's MaxRefusalWakeups wakeups this was, and
	// Exhausted says it was the last one the provider never took, so the refusal is
	// the operator's now rather than one a later pass will wake for.
	Attempt   int  `json:"attempt,omitempty"`
	Exhausted bool `json:"exhausted,omitempty"`
	// Actions is how many the woken turn got carried out, and CostUSD what the turn
	// cost.
	Actions int     `json:"actions,omitempty"`
	CostUSD float64 `json:"cost_usd,omitempty"`
	// Problem is what stopped or spoiled the wakeup, including the woken turn's own
	// block being refused again.
	Problem string `json:"problem,omitempty"`
}

// CorrectionSweep is what one pass did about the refusals. A pass that found none
// reports nothing, which is almost every pass.
type CorrectionSweep struct {
	Corrected []Corrected `json:"corrected,omitempty"`
	// Paused is the operator's pause, when one is what stopped this. Nothing was
	// claimed and every refusal keeps the wakeup it is owed.
	Paused *runstate.OperatorHold `json:"paused,omitempty"`
}

// Corrector wakes roles whose tracker blocks were refused. It has no tracker, no
// worktree access, and no forge access, and it starts nothing: what it does is
// put a role in front of a refusal that is already in its own conversation, and
// write down that it did.
type Corrector struct {
	// Conversations is where the refusals are read from. Required.
	Conversations CorrectionConversations
	// Claims is what makes the wakeup at most once per refusal. Required.
	Claims CorrectionClaims
	// Roles is how the harness reaches a role's conversation. Required.
	Roles CorrectionRole
	// Holds is the operator's pause over everything the harness would spend.
	// Optional, and a corrector wired without one is one nothing can pause, which
	// is what every provider invocation was before the switch existed.
	Holds OperatorHolds
	Clock execution.Clock
}

// Correct wakes the role whose refusal has waited longest, and reports what came
// of it. It is Start with no conversation held, and the turn taken before it
// returns: what a caller that has nothing else to do while the turn is taken
// asks for. A watching session asks Start instead, and takes the turn beside
// its poll.
//
// One per pass, for the reason the delivery and the firing beside it are bounded
// the same way: a wakeup is a conversation turn, and a pass that woke four
// conversations would hold the queue closed for as long as all four took. The next
// pass takes the next refusal, and on a poll loop that is an interval later.
func (c Corrector) Correct(ctx context.Context) (CorrectionSweep, error) {
	start, err := c.Start(ctx, nil)
	sweep := CorrectionSweep{Paused: start.Paused}
	if start.Started != nil {
		sweep.Corrected = []Corrected{start.Started.Take(ctx)}
	}
	return sweep, err
}

// CorrectionStart is what one pass claimed: the wakeup whose turn is still to be
// taken, where there was one, or the operator's pause, when that is what stopped
// it.
type CorrectionStart struct {
	Started *StartedCorrection
	Paused  *runstate.OperatorHold
}

// StartedCorrection is one wakeup claimed and waiting for its turn to be taken.
type StartedCorrection struct {
	Identity runstate.ConversationIdentity
	// Conversation is what the wakeup holds while its turn is taken, keyed as the
	// recurring firings key theirs; see correctionConversationKey.
	Conversation string
	take         func(context.Context) Corrected
}

// Take takes the wakeup's turn and reports what came of it. Like a firing's, it
// never returns an error: a turn that failed is said in what it reports.
func (c StartedCorrection) Take(ctx context.Context) Corrected {
	if c.take == nil {
		return Corrected{Role: c.Identity.Role, Agent: c.Identity.Agent}
	}
	return c.take(ctx)
}

// correctionConversationKey is the conversation a wakeup holds, keyed as the
// recurring firings key the conversations they hold: a program manager
// instance's own where the refusal is on one, and its role's otherwise. Two
// agents configured for one role other than the program manager share the
// role's key, which keeps a wakeup out of the role while a pass of the role's is
// taking turns, at the price of waiting on one that is in the other agent's
// conversation.
func correctionConversationKey(identity runstate.ConversationIdentity) string {
	if identity.Role == domain.RoleProgramManager && identity.Agent != "" && identity.Agent != string(identity.Role) {
		return instanceConversationKey(identity.Agent)
	}
	return roleConversationKey(identity.Role)
}

// Start claims the wakeup owed to the refusal that has waited longest, among the
// conversations busy does not hold, and hands back its turn to be taken. A
// refusal whose conversation is busy waits for a later pass rather than being
// woken into a conversation a turn is already being taken in.
//
// The order is the order the guarantees need. The pause is read before anything
// is claimed, so a paused harness costs no refusal its wakeup; the wakeup is
// claimed before the turn is taken, so a process that dies between the two has
// recorded a wakeup that produced nothing rather than made one nothing paces.
func (c Corrector) Start(ctx context.Context, busy map[string]bool) (CorrectionStart, error) {
	if err := c.validate(); err != nil {
		return CorrectionStart{}, err
	}
	hold, held, err := c.paused()
	if err != nil {
		return CorrectionStart{}, err
	}
	if held {
		return CorrectionStart{Paused: &hold}, nil
	}
	conversations, err := c.Conversations.Recorded()
	if err != nil {
		return CorrectionStart{}, fmt.Errorf("read the recorded conversations: %w", err)
	}
	for _, conversation := range awaitingCorrection(conversations, c.now()) {
		identity := conversation.Identity()
		key := correctionConversationKey(identity)
		if busy[key] {
			continue
		}
		corrected, claimed, woken, err := c.claim(conversation)
		if err != nil {
			// A claim this pass could not make is this pass's one wakeup spent on
			// finding that out: trying the next conversation would be a pass that woke
			// two roles because the first attempt failed, which is the bound this is
			// under going away exactly when something is already wrong.
			return CorrectionStart{}, err
		}
		if !woken {
			// Another process claimed this refusal between the reading and the claim,
			// which is the record doing its job. The next one is this pass's to look at.
			continue
		}
		return CorrectionStart{Started: &StartedCorrection{
			Identity:     identity,
			Conversation: key,
			take: func(ctx context.Context) Corrected {
				return c.wake(ctx, identity, claimed, corrected)
			},
		}}, nil
	}
	return CorrectionStart{}, nil
}

// awaitingCorrection is every conversation owed a wakeup, oldest refusal first.
//
// The order is by when the block was refused rather than by which conversation it
// is, because what the harness owes is a correction and the one that has been
// unanswered longest is the one whose actions have been missing from the queue
// longest.
func awaitingCorrection(conversations []runstate.Conversation, now time.Time) []runstate.Conversation {
	var owed []runstate.Conversation
	for _, conversation := range conversations {
		if conversation.RefusedBlock == nil || !conversation.RefusedBlock.AwaitingWakeup(now) {
			continue
		}
		owed = append(owed, conversation)
	}
	sort.SliceStable(owed, func(i, j int) bool {
		return owed[i].RefusedBlock.RefusedAt.Before(owed[j].RefusedBlock.RefusedAt)
	})
	return owed
}

// claim claims one refusal's wakeup, reporting whether a claim was made at all so
// a refusal another process took leaves this pass looking at the next one rather
// than reporting a wakeup nobody made.
func (c Corrector) claim(conversation runstate.Conversation) (Corrected, runstate.TrackerRefusal, bool, error) {
	identity := conversation.Identity()
	corrected := Corrected{
		ConversationID: conversation.ConversationID,
		Role:           conversation.Role,
		Agent:          identity.Agent,
		Turn:           conversation.RefusedBlock.Turn,
	}
	claimed, err := c.Claims.ClaimRefusalWakeup(identity, c.now())
	if err != nil {
		// A refusal another process woke for between the reading above and this
		// claim, and a conversation somebody is mid-turn with, mean the same thing
		// here: this is not the pass that wakes it. Neither is a failure to report.
		if errors.Is(err, runstate.ErrNoRefusalAwaitingWakeup) || errors.Is(err, runstate.ErrConversationHeld) {
			return Corrected{}, runstate.TrackerRefusal{}, false, nil
		}
		return Corrected{}, runstate.TrackerRefusal{}, false, fmt.Errorf("claim the wakeup owed to %s for the tracker block refused on turn %d: %w",
			identity, conversation.RefusedBlock.Turn, err)
	}
	corrected.Attempt = claimed.Attempts
	return corrected, claimed, true, nil
}

// wake takes the turn a claimed wakeup is for.
//
// The claim is kept for every ending but one, which is deliberately narrower than
// the stopped-work delivery's give-back. A conversation nothing can open — no
// agent configured for the role, a provider nobody has signed in — is something a
// person has to change, so retrying it every pass would be a refused wakeup a
// minute and this pass's one turn taken from every refusal behind it. What such a
// refusal falls back to is what it had before this existed: the harness's own
// words opening the role's next turn, whenever one happens.
//
// The exceptions are the provider refusing the turn for want of capacity, and the
// provider answering nobody because it is down or the account's login has lapsed.
// Each provably put nothing in front of the role, and the wakeup running on the
// non-model side is worth nothing if an outage silently spends it — which is when
// batches are most likely to be refused — so the turn is given back and the
// refusal keeps it, bounded by MaxRefusalWakeups and paced by
// RefusalWakeupRetryDelay. The give-back that spends the last attempt hands the
// refusal to the operator instead, and the pass says so.
func (c Corrector) wake(ctx context.Context, identity runstate.ConversationIdentity, claimed runstate.TrackerRefusal, corrected Corrected) Corrected {
	turn, wakeErr := c.Roles.Wake(ctx, identity, correctionMessage(claimed))
	// What the turn cost is carried whichever way it went, because the provider
	// charges for a turn that failed exactly as for one that answered.
	corrected.CostUSD = turn.CostUSD
	if conversationID := strings.TrimSpace(turn.ConversationID); conversationID != "" {
		corrected.ConversationID = conversationID
	}
	if wakeErr != nil {
		corrected.Problem = describeFailedWakeup(identity, claimed, wakeErr)
		if neverTaken(wakeErr) {
			c.giveBack(ctx, identity, claimed, wakeErr, &corrected)
		}
		return corrected
	}
	corrected.Woken = true
	corrected.Actions = turn.Actions
	switch {
	case turn.Refused != "":
		corrected.Problem = fmt.Sprintf(
			"the %s was woken to re-issue the tracker block refused on turn %d and the block it sent back was refused too, so the actions are still lost and the operator has it: %s",
			identity, claimed.Turn, turn.Refused)
	case turn.Actions == 0:
		// A turn that answered and asked for nothing is not a failure and is not a
		// correction either. It is said out loud because the two look identical from
		// outside: a pass that woke a role and reported only that would read as a
		// refusal put right. The operator hears it from the conversation's own
		// record rather than from here — this line is what a session prints, and a
		// loss that reached only the terminal that ran the pass is the silence this
		// whole mechanism is against.
		corrected.Problem = fmt.Sprintf(
			"the %s was woken to re-issue the tracker block refused on turn %d and answered without asking for any tracker action, so nothing it lost has been put back",
			identity, claimed.Turn)
	}
	return corrected
}

// giveBack returns a wakeup that reached the role with nothing, so the refusal
// keeps the turn it is owed — or, where that was its last attempt, says that the
// harness has stopped trying and the operator has it.
//
// What comes next is read from the record the give-back left rather than worked
// out here from the count, because the store is what decides the refusal is
// spent and what tells the operator so.
//
// It is written under a context detached from the wakeup's own, for the reason
// the stopped-work delivery detaches its own records: a shutdown cancels the very
// context the wakeup ran under, and it lands between the failure and this write —
// which is the largest class of the deaths a give-back exists for. One that
// failed is said beside the wakeup rather than swallowed, because what it leaves
// behind is an attempt spent on a turn nobody was asked.
func (c Corrector) giveBack(ctx context.Context, identity runstate.ConversationIdentity, claimed runstate.TrackerRefusal, wakeErr error, corrected *Corrected) {
	write, stopWriting := recordContext(ctx)
	defer stopWriting()
	given, err := c.Claims.WithdrawRefusalWakeup(write, identity, claimed.Turn, wakeErr.Error())
	switch {
	case err != nil:
		corrected.Problem = fmt.Sprintf("%s; and the wakeup it never used could not be given back, so it has spent one of %d on a turn nobody was asked: %v",
			corrected.Problem, runstate.MaxRefusalWakeups, err)
	case given.Escalated != "":
		corrected.Exhausted = true
		corrected.Problem = fmt.Sprintf("%s; that was the last of %d wakeups, so the harness has stopped trying and the operator has it",
			corrected.Problem, runstate.MaxRefusalWakeups)
	case given.Turn != 0:
		corrected.Problem = fmt.Sprintf("%s; it will be woken again once %s has passed",
			corrected.Problem, runstate.RefusalWakeupRetryDelay)
	}
}

// describeFailedWakeup says what became of a turn that did not answer, in the
// words each failure earns. The four are different facts about the same wakeup:
// a provider with no capacity put nothing in front of the role and will have
// capacity again, a provider answering nobody put nothing in front of it either
// and comes back when the network or a login does, a conversation that could
// never be opened asked the role nothing and waits on somebody changing
// something, and a turn that started and failed inside is none of them.
//
// The two the provider never took say which attempt this was, and what happens
// next is added by the give-back, which is where that is decided.
func describeFailedWakeup(identity runstate.ConversationIdentity, claimed runstate.TrackerRefusal, err error) string {
	switch {
	case errors.Is(err, ErrProviderWindow):
		return fmt.Sprintf("the provider had no capacity for the turn waking the %s to re-issue the tracker block refused on turn %d, so nothing was asked on attempt %d of %d: %v",
			identity, claimed.Turn, claimed.Attempts, runstate.MaxRefusalWakeups, err)
	case errors.Is(err, ErrProviderAway):
		return fmt.Sprintf("the provider answered nobody — down, or its login lapsed — for the turn waking the %s to re-issue the tracker block refused on turn %d, so nothing was asked on attempt %d of %d: %v",
			identity, claimed.Turn, claimed.Attempts, runstate.MaxRefusalWakeups, err)
	case errors.Is(err, ErrRoleUnreachable):
		return fmt.Sprintf("the %s could not be woken to re-issue the tracker block refused on turn %d, so nothing was asked and the refusal waits on its own conversation's next turn: %v",
			identity, claimed.Turn, err)
	default:
		return fmt.Sprintf("the turn waking the %s to re-issue the tracker block refused on turn %d failed: %v",
			identity, claimed.Turn, err)
	}
}

// correctionMessage is what the harness says when it wakes a role to correct a
// refused block.
//
// It deliberately does not restate what was wrong. The refusal is already in this
// conversation's own record, in the harness's own words, and it opens the turn
// this message arrives at the end of — written there by the same machinery that
// refused the block, and written in the same save that recorded the refusal this
// wakeup was claimed against, so a wakeup exists only where those words do. A
// second copy worded here would be the harness paraphrasing its own refusal, and
// a paraphrase is exactly what a role correcting a block must not be given.
//
// What it does say is the three things the refusal cannot: that the harness woke
// this turn rather than a person, that being woken grants no authority, and that
// this is the only turn the harness will start for it.
func correctionMessage(refused runstate.TrackerRefusal) string {
	return strings.Join([]string{
		fmt.Sprintf("The harness woke you because the tracker block in your reply on turn %d was refused whole and nothing in it happened. Nobody is waiting at a terminal for this.", refused.Turn),
		"The refusal is above this message, in the harness's own words. Answer it: issue the actions you still want again, in a block that fixes what it says was wrong with the one before it.",
		"Your authority here is exactly the authority your role already holds — this turn grants you nothing extra, and being woken to correct something widens nothing about what you may decide or change.",
		"This is the one turn the harness starts for this refusal. A block refused again goes to the operator rather than earning another.",
	}, "\n")
}

// paused reports the operator's pause over everything the harness spends. A pause
// that cannot be read refuses the pass rather than being spent through, exactly as
// it does everywhere else it is read.
func (c Corrector) paused() (runstate.OperatorHold, bool, error) {
	if c.Holds == nil {
		return runstate.OperatorHold{}, false, nil
	}
	hold, held, err := c.Holds.Held()
	if err != nil {
		return runstate.OperatorHold{}, false, fmt.Errorf("read whether the operator has paused harness activity: %w", err)
	}
	return hold, held, nil
}

func (c Corrector) validate() error {
	var problems []error
	if c.Conversations == nil {
		problems = append(problems, errors.New("waking a role to correct a refused tracker block requires the recorded conversations the refusals are on"))
	}
	if c.Claims == nil {
		problems = append(problems, errors.New("waking a role to correct a refused tracker block requires the durable claim that bounds it to one wakeup per refusal"))
	}
	if c.Roles == nil {
		problems = append(problems, errors.New("waking a role to correct a refused tracker block requires the role's conversation to wake"))
	}
	return errors.Join(problems...)
}

func (c Corrector) now() time.Time {
	if c.Clock == nil {
		return execution.RealClock{}.Now().UTC()
	}
	return c.Clock.Now().UTC()
}

// Render describes what one pass did about the refusals, for whoever asked.
func (s CorrectionSweep) Render() string {
	var rendered strings.Builder
	if s.Paused != nil {
		fmt.Fprintf(&rendered, "PAUSED: no role was woken to correct a refused tracker block, since %s\n",
			s.Paused.HeldAt.UTC().Format(time.RFC3339))
	}
	for _, corrected := range s.Corrected {
		switch {
		case !corrected.Woken:
			fmt.Fprintf(&rendered, "the %s was not woken for the tracker block refused on turn %d\n", corrected.Role, corrected.Turn)
		case corrected.Actions > 0:
			fmt.Fprintf(&rendered, "the %s was woken for the tracker block refused on turn %d and re-issued %d action(s)\n",
				corrected.Role, corrected.Turn, corrected.Actions)
		default:
			fmt.Fprintf(&rendered, "the %s was woken for the tracker block refused on turn %d\n", corrected.Role, corrected.Turn)
		}
		if corrected.Problem != "" {
			fmt.Fprintf(&rendered, "  %s\n", corrected.Problem)
		}
	}
	return rendered.String()
}
