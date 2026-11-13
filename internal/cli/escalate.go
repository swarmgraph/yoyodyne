package cli

// Where a stopped run reaches the development manager.
//
// The docket has always been assembled for her and delivered into her
// conversation when she opened one. What was missing was the opening: a run
// stopped, the entry was written, and nothing happened until a person told her.
// This is that person, replaced by the harness — the same conversation, the same
// evidence, the same decisions recorded the same way, with the courier taken
// out.
//
// It is wired into the pull rather than into the run that stops. A delivery is a
// conversation turn, and a run holding a developer slot open while it waits for
// the development manager to read something would be a stoppage that costs the
// harness capacity on its way out. The pull is where the harness is already
// choosing what to do next, and it is the loop every stoppage eventually passes
// through however the run that made it ended.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// escalatorFrom wires the delivery of stopped work over parts that are already
// built, so the docket it reads is the one the runs write and the one she reads
// in her own conversation.
func escalatorFrom(parts components, configPath string, stderr io.Writer) *orchestrator.Escalator {
	return &orchestrator.Escalator{
		Docket: parts.docket,
		// Which stoppage an entry describes is read from the run's own record, so
		// the classification is the run's account of how it ended rather than a
		// reading of the entry's prose.
		Runs: parts.store,
		// Where the delivery is claimed, so one stoppage reaches her once however
		// many passes walk past it.
		Records: parts.store.Escalations(),
		// What triage has already decided about the item and what has been carried
		// out of it — the same two records the docket she reads joins onto every
		// entry — so a stoppage she settled an hour ago is not put to her again
		// while the harness has yet to act on it.
		Decisions: parts.store.Triage(),
		Reruns:    parts.store.Reruns(),
		Manager:   developmentManagerConversation{configPath: configPath, stderr: stderr},
		// The same pause every run and every turn reads. A delivery is a provider
		// invocation, so `yoyo pause` covers it exactly as it covers them.
		Holds: parts.holds,
	}
}

// developmentManagerConversation is the development manager's own conversation,
// reached the way an operator reaches it: the recorded conversation resumed, one
// message sent, the reply read. Nothing about the turn is special — it is held
// under the same lease, recorded in the same log, and charged to the same
// account as the conversation an operator opens by hand.
type developmentManagerConversation struct {
	configPath string
	// stderr is where opening the conversation says what it could not read. It is
	// the command's own error stream, so a session that started with a warning
	// says so where the operator running it can see it.
	stderr io.Writer
}

// Judge puts one docketed stoppage in front of her and reports what she recorded
// about it.
//
// A conversation that could not be opened is reported as unreachable rather than
// as a failed delivery, and the difference is what the caller does about it:
// nothing was asked of her, so the attempt is given back and a later pass makes
// it. That covers the ordinary reasons opening fails — the operator is mid-turn
// with her, the provider is not signed in, no agent fills the role — none of
// which is a reason to spend a stoppage's delivery.
//
// Mid-turn is the one this does not wait out, which is why it opens without
// waiting. An operator sitting at her prompt is no longer a reason at all: the
// console puts the conversation down between turns, so what is left is a turn
// actually being taken, and coming back later is cheaper than holding a
// delivery's lease and budget open until it ends.
func (d developmentManagerConversation) Judge(ctx context.Context, entry triage.Entry) (orchestrator.Judgment, error) {
	session, lease, err := openChat(ctx, domain.RoleDevelopmentManager, "", d.configPath, false, false, d.errors())
	if err != nil {
		return orchestrator.Judgment{}, fmt.Errorf("%w: %w", orchestrator.ErrConversationUnreachable, err)
	}
	defer lease.Release()

	reply, err := session.Send(ctx, escalationMessage(entry))
	// The conversation and what the turn cost are carried whichever way it went: a
	// turn that failed still happened in a conversation somebody can go and read,
	// and the provider charged for it exactly as it charges for one that answered.
	judgment := orchestrator.Judgment{
		ConversationID: session.Evidence().ConversationID,
		CostUSD:        session.TurnCostUSD(),
	}
	if err != nil {
		return judgment, notReached(err)
	}
	// What she decided is read from what was actually recorded against the item
	// rather than from what the reply says, for the reason the conversation
	// records decisions at all: a decision is worth something because it was
	// carried out against the item's durable triage budget.
	judgment.Decision, judgment.Reason, _ = reply.TriageDecision(entry.RunID)
	return judgment, nil
}

// notReached marks the failures where the turn produced no answer of hers, so
// the stoppage keeps the delivery it is owed rather than spending one on a turn
// that decided nothing.
//
// Three of them. A provider that declined the turn for want of capacity never
// put the message in front of her, and the limit it met is recorded where every
// process outside a run records one — so the right answer is to ask again once
// it resets, which a later pass does. A pause the operator placed between the
// escalator reading it and this turn is the same fact a moment later: the turn
// was refused before the provider was reached, and the stoppage is delivered
// after the pause lifts.
//
// The third is a turn a cancellation killed before her reply existed, which is
// the harness's own death rather than anything about her: a signal, a shutdown,
// a process-group teardown taken out from under the pull. Where that one's
// boundary falls is stated on the Withdraw contract it is spent against, and it
// is narrower than the other two — it does not claim she never saw the message,
// only that the turn ended before a reply was parsed, so no action of hers was
// applied and the item's durable triage record never moved. A cancellation that
// landed after her answer arrived is not this and keeps its attempt: she
// answered, and asking again would put the same evidence to her twice.
//
// None of the three is a reason to ask again immediately, and the record is what
// stops that: the attempt comes back and the pacing does not, so the next
// delivery waits out the same delay a failed one does. A refusal and a hold both
// last minutes or hours, and a pull that asked again at once would meet the same
// one several times a minute for the whole of it.
//
// Everything else is left as it is. A turn that answered and then failed is one
// nothing here can prove she did not take, and the bounded retry is what keeps
// that honesty from becoming a loop.
func notReached(err error) error {
	var held *chat.OperatorHoldError
	if errors.Is(err, chat.ErrProviderCapacity) || errors.As(err, &held) {
		return fmt.Errorf("%w: %w", orchestrator.ErrConversationUnreachable, err)
	}
	if errors.Is(err, chat.ErrTurnAbandoned) {
		return fmt.Errorf("%w: %w", orchestrator.ErrDeliveryCancelled, err)
	}
	return err
}

func (d developmentManagerConversation) errors() io.Writer {
	if d.stderr == nil {
		return io.Discard
	}
	return d.stderr
}

// escalationMessage is what the harness says when it puts a stoppage in front of
// her. It is deliberately thin: the docket entry is already in the ground this
// conversation is built on, with the reviewer's findings, what the run
// preserved, and what the item has already spent, so repeating any of it here
// would be a second copy of evidence that can disagree with the first.
//
// What it does say is the two things the docket cannot: which entry this message
// is about, and that nobody has decided anything. A delivery that read as a
// recommendation would be the harness having an opinion about work it is only
// carrying.
func escalationMessage(entry triage.Entry) string {
	item := entry.WorkItemID
	if title := strings.TrimSpace(entry.WorkItemTitle); title != "" {
		item += " — " + title
	}
	return strings.Join([]string{
		fmt.Sprintf("Run %s of %s stopped: independent review still required repair after every permitted attempt, and the change it made is preserved.", entry.RunID, item),
		"The harness delivered this to you because the run stopped, and for no other reason: nothing has been decided about it, and nothing happens until you record a decision.",
		"Recording one is what causes it. A decision you record here that asks for work is carried out by the harness itself on a later scheduling pass, under the checks and limits that already bound it, with nobody typing anything — so record what you want to happen rather than what you would ask somebody for.",
		"Its entry is on the triage docket above, with the reviewer's findings, what the run preserved, and what the item has already spent against its caps.",
		fmt.Sprintf("What becomes of it is yours to judge. Record a triage decision naming run %s, or say what you are waiting on and leave it where it is.", entry.RunID),
	}, "\n")
}
