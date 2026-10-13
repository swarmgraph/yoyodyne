package chat

import (
	"context"
	"fmt"
	"io"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A command the operator types while a turn is being answered is carried out
// at once. What it does to the harness — holding intake, stopping a run,
// recording a directive, starting work — happens there and then, because that
// is what the operator typed it for. What it would write into this
// conversation's own record waits until the turn has ended.
//
// It has to wait, and not only because two goroutines would otherwise share the
// record. A turn owns the conversation's event log while it runs: the
// provider's events are appended with consecutive numbers as they arrive, and
// the turn records the conversation as it stands when it finishes. An event a
// command appended in the middle would take a number the turn is about to use,
// and a note it left for the agent would be overwritten by the record the turn
// saves. So the command is carried out against a copy of the conversation
// taken as the turn began (commandView), the copy keeps what it would have
// written (keptWrites), and the writes are made in order, with numbers of their
// own, the moment the turn has ended (writeKept).

// turnInFlight is what commands typed during one turn share: the conversation
// as it stood when the turn began, and what they would have written into it.
// It lives on the goroutine that reads the operator's typing, and the turn's own
// goroutine never touches it.
type turnInFlight struct {
	began runstate.Conversation
	kept  keptWrites
}

// keptWrites is what the commands typed during a turn would have written into
// the conversation: the events for its log, the notes the agent is told on its
// next turn, and the item a /work started, which /diff names by default.
type keptWrites struct {
	events            []execution.Event
	notices           []string
	lastRunWorkItemID string
}

// keepingStore is the conversation store as a command typed during a turn sees
// it. Events and saves are kept rather than written; everything else is the
// store itself, because nothing a command reads from it is the turn's to change.
type keepingStore struct {
	Store
	kept *keptWrites
}

func (k keepingStore) AppendEvent(event execution.Event) error {
	k.kept.events = append(k.kept.events, event)
	return nil
}

func (keepingStore) Save(runstate.Conversation) error { return nil }

// commandView is the session a command typed during this turn is carried out
// on: the same harness, the same run this conversation started, and the
// conversation as it stood when the turn began, with every write to it kept.
func (s *Session) commandView(turn *turnInFlight) *Session {
	options := s.options
	options.Store = keepingStore{Store: s.options.Store, kept: &turn.kept}
	state := turn.began
	if turn.kept.lastRunWorkItemID != "" {
		state.LastRunWorkItemID = turn.kept.lastRunWorkItemID
	}
	return &Session{
		options:          options,
		state:            state,
		active:           s.active,
		theme:            s.theme,
		composing:        s.composing,
		shiftReturn:      s.shiftReturn,
		progressInterval: s.progressInterval,
	}
}

// dispatchDuringTurn carries out one command on a view of the conversation and
// keeps what it would have written. A run it started or stopped is this
// conversation's at once, because the conversation is what reports it and what
// ending stops it.
func (s *Session) dispatchDuringTurn(ctx context.Context, line string, out io.Writer) (bool, error) {
	view := s.commandView(s.turn)
	exit, err := view.dispatch(ctx, line, out)
	s.active = view.active
	if view.state.LastRunWorkItemID != s.turn.began.LastRunWorkItemID {
		s.turn.kept.lastRunWorkItemID = view.state.LastRunWorkItemID
	}
	s.turn.kept.notices = append(s.turn.kept.notices, view.notices...)
	return exit, err
}

// writeCommandsKept writes what the commands typed beside the turn that has just
// ended would have written, and tells the operator if it could not. What the
// commands did is done either way; only the conversation's record of it is
// missing then, which is no reason to fail the turn. A conversation this process
// no longer holds — a turn that ended while it was put down for a provider wait
// and could not take it back — is not written to at all, because the record is
// somebody else's.
func (s *Session) writeCommandsKept(screen io.Writer) {
	turn := s.turn
	s.turn = nil
	if turn == nil {
		return
	}
	if held, ok := s.options.Hold.(interface{ Held() bool }); ok && !held.Held() {
		if turn.kept.events != nil || turn.kept.notices != nil {
			fmt.Fprintf(s.theme.Harness(screen), "what the commands you typed during the answer did is done, but it could not be recorded in this conversation, because another process holds the conversation now.\n\n")
		}
		return
	}
	if err := s.writeKept(turn); err != nil {
		fmt.Fprintf(s.theme.Harness(screen), "%v\n\n", err)
	}
}

// writeKept writes what the commands typed during the turn would have written,
// now that the turn has ended and the conversation is this goroutine's again.
// Each event keeps the moment it happened and takes the next number in the log.
func (s *Session) writeKept(turn *turnInFlight) error {
	kept := turn.kept
	if len(kept.events) == 0 && len(kept.notices) == 0 && kept.lastRunWorkItemID == "" {
		return nil
	}
	for _, notice := range kept.notices {
		s.notice("%s", notice)
	}
	if kept.lastRunWorkItemID != "" {
		s.state.LastRunWorkItemID = kept.lastRunWorkItemID
	}
	for _, happened := range kept.events {
		s.state.LastSequence++
		event, err := execution.NewEvent(s.state.ConversationID, s.state.LastSequence, happened.Timestamp, happened.Type, happened.Source, happened.Payload)
		if err != nil {
			return fmt.Errorf("record what a command typed during the answer did: %w", err)
		}
		if err := s.options.Store.AppendEvent(event); err != nil {
			return fmt.Errorf("record what a command typed during the answer did: %w", err)
		}
	}
	return s.record()
}
