package readmodel

// A developer slot the scheduler left empty while ready work waited, read once
// for every surface that says it: the running line `yoyo status` prints, and the
// health check.
//
// The scheduler names why each time it leaves a slot empty and writes it onto
// the watch log when its account changes (runstate.EmptySlots,
// orchestrator/emptyslots.go). This reads the latest account back, keeps it
// only while it still describes the machine — the session that wrote it still
// watching, a slot still free by this reading, and ready work still waiting —
// and asks the ownership registry who moves it.

import (
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// EmptySlots is the scheduler's standing account of the developer slots it
// left empty while ready work waited, with who moves it.
type EmptySlots struct {
	runstate.EmptySlots
	// SessionID is the watch session that wrote it.
	SessionID string `json:"session_id"`
	// Owner is the ownership registry's answer for it.
	Owner ownership.Answer `json:"owner"`
}

// Whose is who moves it and what they do, in the words a surface prints.
func (e EmptySlots) Whose() string {
	return e.Owner.Owner.Possessive() + " to move: " + e.Owner.Remedy
}

// Problem is the account as the one sentence the running line and the health
// check say, with since when.
func (e EmptySlots) Problem(now time.Time) string {
	return fmt.Sprintf("%s. This has stood since %s (%s), and it is %s",
		e.Says(), localMoment(e.Since), agoSaid(now.Sub(e.Since)), e.Whose())
}

// LatestEmptySlots is the latest account of empty slots on the watch log that
// still stands: not cleared, and written by a session that has not stopped
// since. It answers nothing where no session ever wrote one.
func LatestEmptySlots(sessions []runstate.WatchTransition) (runstate.EmptySlots, string, bool) {
	var latest *runstate.WatchTransition
	stopped := make(map[string]time.Time)
	for index := range sessions {
		transition := &sessions[index]
		if transition.State == runstate.WatchStopped && transition.At.After(stopped[transition.SessionID]) {
			stopped[transition.SessionID] = transition.At
		}
		if transition.EmptySlots == nil {
			continue
		}
		if latest == nil || !transition.EmptySlots.At.Before(latest.EmptySlots.At) {
			latest = transition
		}
	}
	if latest == nil || latest.EmptySlots.Cleared {
		return runstate.EmptySlots{}, "", false
	}
	if end, ended := stopped[latest.SessionID]; ended && !end.Before(latest.EmptySlots.At) {
		return runstate.EmptySlots{}, "", false
	}
	return *latest.EmptySlots, latest.SessionID, true
}

// emptySlotsReading is what the standing reads beside the account to decide
// whether it still describes the machine: how many developer slots this
// reading finds free, or -1 where the runs could not be read, and how many
// items it finds startable, or -1 where the queue could not be read.
type emptySlotsReading struct {
	free      int
	startable int
	intake    runstate.IntakeHold
	held      bool
}

// readEmptySlots is the standing account of empty slots, or nil where none
// stands. An account the reading contradicts — every slot taken now, or no
// startable work where the scheduler could not read the queue — is dropped,
// because the scheduler writes again only once it next polls; a slot still
// counted as taken is kept whatever the count says, because the count is what
// it is about.
func readEmptySlots(sources Sources, reading emptySlotsReading) (*EmptySlots, string) {
	if sources.Sessions == nil {
		return nil, ""
	}
	sessions, err := sources.Sessions.List()
	if err != nil {
		return nil, fmt.Sprintf("why a developer slot stands empty could not be read: %v", err)
	}
	account, session, standing := LatestEmptySlots(sessions)
	if !standing {
		return nil, ""
	}
	if account.Cause != ownership.EmptySlotStillCounted && reading.free == 0 {
		return nil, ""
	}
	if account.ReadyUnread && reading.startable == 0 {
		return nil, ""
	}
	record := ownership.Record{EmptySlot: account.Cause}
	if account.Cause == ownership.EmptySlotHold && reading.held {
		record = intakeHoldRecord(reading.intake)
		record.EmptySlot = account.Cause
	}
	return &EmptySlots{EmptySlots: account, SessionID: session, Owner: ownership.Resolve(ownership.KindEmptySlot, record)}, ""
}

// renderEmptySlots is the lines the running line says the account in: the
// problem itself, and under it each ready item the scheduler passed over and
// why. It opens on "problem:" so the line stands out on a terminal that shows
// no emphasis.
func (s Standing) renderEmptySlots() string {
	var rendered strings.Builder
	if s.EmptySlots != nil {
		fmt.Fprintf(&rendered, "  problem: %s\n", s.EmptySlots.Problem(s.ObservedAt))
		for _, named := range s.EmptySlotsCountedFor() {
			fmt.Fprintf(&rendered, "    the slot is still counted for %s\n", named)
		}
		for _, item := range s.EmptySlots.Passed {
			fmt.Fprintf(&rendered, "    passed over %s: %s\n", s.named(item.WorkItemID, ""), item.Why)
		}
	}
	if s.EmptySlotsProblem != "" {
		fmt.Fprintf(&rendered, "%s%s\n", partialRead, s.EmptySlotsProblem)
	}
	return rendered.String()
}

// EmptySlotsCountedFor is each item a slot still counted as taken is counted
// for, named with its priority, labels, and title as this reading of the
// tracker holds them, so no surface shows the item by its identifier alone.
func (s Standing) EmptySlotsCountedFor() []string {
	if s.EmptySlots == nil {
		return nil
	}
	named := make([]string, 0, len(s.EmptySlots.CountedFor))
	for _, id := range s.EmptySlots.CountedFor {
		named = append(named, s.named(id, ""))
	}
	return named
}
