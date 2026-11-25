package cli

// Where the wakeup a refused tracker block earns meets the role's own
// conversation.
//
// It is wired into the pull for the reason the recurring schedule and the
// stopped-work delivery are: the wakeup is a conversation turn, the pull is where
// the harness is already deciding what to do next, and the harness is the only
// thing that invokes a role. A separate daemon, a cron entry, or a launchd job
// would each be a second invoker of one.

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// correctorFrom wires the wakeup over parts that are already built, so the turn
// happens in the role's own recorded conversation and the claim that bounds it to
// one lands beside the record the refusal itself is on.
func correctorFrom(parts components, configPath string, stderr io.Writer) (orchestrator.ScheduleCorrections, error) {
	conversations, err := runstate.NewConversationStore(parts.stateRoot, parts.config.Product.ID)
	if err != nil {
		return nil, err
	}
	return &orchestrator.Corrector{
		// The refusals are read from the conversation records themselves, which is
		// where the refusal machinery already wrote them, and the claim is taken on
		// the same record under the same lease — so one refusal is woken for once
		// however many sessions are polling.
		Conversations: conversations,
		Claims:        conversations,
		Roles:         roleCorrection{configPath: configPath, stderr: stderr},
		// The same pause every run, turn, delivery, and firing reads. A wakeup is a
		// provider invocation, so `yoyo pause` covers it exactly as it covers them.
		Holds:    parts.holds,
		MayStart: parts.mayStart(),
	}, nil
}

// roleCorrection is a role's own conversation, reached the way an operator
// reaches it: the recorded conversation resumed, one message sent, the reply
// read. Nothing about the turn is special — it is held under the same lease,
// recorded in the same log, charged to the same account, and reads the same
// persona as the conversation an operator opens by hand.
//
// That is the whole of why waking a role to correct its own mistake is safe to do
// at all. A turn that held a different authority because the harness rather than
// a person started it would be a second version of a role nobody configured.
type roleCorrection struct {
	configPath string
	// stderr is where opening the conversation says what it could not read, so a
	// session that started with a warning says so where the operator running it can
	// see it.
	stderr io.Writer
}

// Wake puts the correction message into the conversation whose block was refused,
// and reads what the turn did about it.
//
// It addresses the agent rather than only the role, because a refusal belongs to
// one conversation: a project with two agents on one role has two of them, and
// waking the other would hand the refusal to a conversation that never sent the
// block.
//
// A conversation that could not be opened is reported as unreachable rather than
// as a failed wakeup, and the difference is what the caller records: nothing was
// asked, so the refusal is exactly where it was. The ordinary reasons opening
// fails — the operator is mid-turn with the role, no agent fills the role — are
// all of that kind. A provider nobody is signed in to is reported as the provider
// answering nobody instead, because that one is retried; see notOpened.
//
// A woken turn whose own block is refused is not a failed turn either. The role
// answered, and what it answered with was refused a second time — which is the
// ending that goes to the operator, and is already recorded against the
// conversation by the refusal machinery itself. It is carried back so the pass can
// say so rather than reporting a turn that failed.
func (r roleCorrection) Wake(ctx context.Context, identity runstate.ConversationIdentity, message string) (orchestrator.CorrectionTurn, error) {
	session, lease, err := openChat(ctx, identity.Role, identity.Agent, r.configPath, false, false, r.errors())
	if err != nil {
		return orchestrator.CorrectionTurn{}, notOpened(err)
	}
	defer lease.Release()

	reply, err := session.Send(ctx, message)
	// The conversation and what the turn cost are carried whichever way it went: a
	// turn that failed still happened in a conversation somebody can go and read,
	// and the provider charged for it exactly as it charges for one that answered.
	turn := orchestrator.CorrectionTurn{
		ConversationID: session.Evidence().ConversationID,
		CostUSD:        session.TurnCostUSD(),
	}
	// What the correction actually put back. Only the actions that were carried out
	// count: an action re-issued and refused by the tracker is still a thing the
	// role has to be told about, and counting it here would report a queue that
	// moved when it did not.
	for _, action := range reply.Actions {
		if action.Applied {
			turn.Actions++
		}
	}
	if err != nil {
		var refused *chat.TrackerError
		if errors.As(err, &refused) {
			turn.Refused = refused.Error()
			return turn, nil
		}
		return turn, notCorrected(err)
	}
	return turn, nil
}

// notCorrected marks the failures where the turn put nothing in front of the
// role, so what is recorded about the wakeup says so rather than claiming a turn
// the role could have answered.
//
// It is the recurring trigger's mapping with two endings pulled out of it. A
// provider with no capacity and a provider answering nobody — down, or its login
// lapsed — are the failures worth making again on a timer: no model saw the
// message, and each ends without anything about the refusal changing, so the
// wakeup is given back rather than spent — which is what keeps a refusal recorded
// during a window or an outage from falling back to waiting on a person. The
// operator's pause and a cancellation are the harness's own doing and clear the
// same way, but the corrector reads the pause at the top of every pass and a
// cancellation is the harness stopping, so neither is worth a timer of its own;
// they are unreachable like the rest.
func notCorrected(err error) error {
	if errors.Is(err, chat.ErrProviderCapacity) {
		return fmt.Errorf("%w: %w", orchestrator.ErrProviderWindow, err)
	}
	if errors.Is(err, chat.ErrProviderAway) {
		return fmt.Errorf("%w: %w", orchestrator.ErrProviderAway, err)
	}
	return notWoken(err)
}

// notOpened marks a conversation that could not be opened. Nearly every reason is
// something a person has to change, and it is unreachable. The exception is an
// account whose login has lapsed, which opening checks before any turn and which
// is the provider answering nobody exactly as an outage met mid-turn is: it ends
// when somebody signs in, so the wakeup is given back and made again rather than
// spent on finding that out.
func notOpened(err error) error {
	if errors.Is(err, chat.ErrProviderAway) {
		return fmt.Errorf("%w: %w", orchestrator.ErrProviderAway, err)
	}
	return fmt.Errorf("%w: %w", orchestrator.ErrRoleUnreachable, err)
}

func (r roleCorrection) errors() io.Writer {
	if r.stderr == nil {
		return io.Discard
	}
	return r.stderr
}
