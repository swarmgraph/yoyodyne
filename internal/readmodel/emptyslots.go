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
func readEmptySlots(sources Sources, reading emptySlotsReading, waiting []RunningRun, now time.Time) (*EmptySlots, string) {
	if sources.Sessions == nil {
		return nil, ""
	}
	sessions, err := sources.Sessions.List()
	if err != nil {
		return nil, fmt.Sprintf("why a developer slot stands empty could not be read: %v", err)
	}
	account, session, standing := LatestEmptySlots(sessions)
	if !standing {
		return forgeWaitSlots(sessions, reading, waiting, now), ""
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

// forgeWaitStands is how long the scheduler may go without reporting a free slot
// empty, beside ready work and runs waiting on GitHub's checks, before the read
// model says so itself. It is several polls, so a slot the next poll fills is
// not reported.
const forgeWaitStands = 5 * time.Minute

// forgeWaitSlots is the account the read model makes itself where the scheduler
// reports no empty slot, yet this reading finds a developer slot free, ready work
// startable, and runs waiting on GitHub's checks. Such a run holds no slot
// (runstate.State.HoldsDeveloperSlot), so the slot is free and ready work should
// have been started into it; the scheduler saying nothing is the contradiction.
// It names the runs waiting so a reader sees what the slot was last used for. It
// answers nothing before the scheduler's last word has stood for
// forgeWaitStands, or where no scheduler word is on record to measure from.
func forgeWaitSlots(sessions []runstate.WatchTransition, reading emptySlotsReading, waiting []RunningRun, now time.Time) *EmptySlots {
	if reading.free < 1 || reading.startable < 1 || reading.held {
		return nil
	}
	var runs []string
	for _, run := range waiting {
		if run.WaitingForCI {
			runs = append(runs, titled(run.Title, run.WorkItemID))
		}
	}
	if len(runs) == 0 {
		return nil
	}
	var last *runstate.WatchTransition
	for index := range sessions {
		if sessions[index].EmptySlots != nil && (last == nil || !sessions[index].EmptySlots.At.Before(last.EmptySlots.At)) {
			last = &sessions[index]
		}
	}
	if last == nil || !last.EmptySlots.Cleared {
		return nil
	}
	// What the scheduler last said was that no slot stood empty; the reading
	// finding one free since then is the disagreement, and its age is how long
	// the scheduler's word has stood.
	if now.Sub(last.EmptySlots.At) < forgeWaitStands {
		return nil
	}
	shown := runs
	if len(shown) > runstate.MaxEmptySlotItems {
		shown = shown[:runstate.MaxEmptySlotItems]
	}
	account := runstate.EmptySlots{
		Cause: ownership.EmptySlotWaitingOnForge,
		Reason: fmt.Sprintf("no run is working in %s, and the scheduler has reported none empty since %s; the runs waiting on GitHub's checks hold no developer slot: %s",
			slotsFree(reading.free), localMoment(last.EmptySlots.At), strings.Join(shown, ", ")),
		FreeSlots: reading.free,
		Ready:     reading.startable,
		Since:     last.EmptySlots.At,
		At:        last.EmptySlots.At,
	}
	return &EmptySlots{EmptySlots: account, SessionID: last.SessionID, Owner: ownership.Resolve(ownership.KindEmptySlot, ownership.Record{EmptySlot: account.Cause})}
}

func slotsFree(free int) string {
	if free == 1 {
		return "1 developer slot"
	}
	return fmt.Sprintf("%d developer slots", free)
}

func titled(title, id string) string {
	if strings.TrimSpace(title) == "" {
		return id
	}
	return title + " (" + id + ")"
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
