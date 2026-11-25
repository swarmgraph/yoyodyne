package orchestrator

import (
	"fmt"
	"io"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Every pull counts a developer slot for each start of this session's own that
// it has not collected yet (see mine in Scheduler.Schedule). The run records
// are what every other count reads — the reservation, the re-run action, the
// health check — so where a start's own run is on record as holding no slot,
// the records win and the start is left out of this session's count too.
//
// idleStarts covers a start whose run began after the start did. What it could
// not see is a start that took up a run already on record — a re-adoption, a
// continuation after GitHub's checks answered, a decided recovery continuing
// the stopped run — whose run began long before the start; and a run of this
// session's own whose slot the harness freed because nothing had held the run
// for a while (runstate.Store.ReleaseSlotsWithNoLiveWorker), which workersGone
// leaves to the session because the worker it names is this process. Either
// kept the slot counted until the session collected the start, which a start
// whose process was still going could put off for an hour or more while ready
// work waited and the health check read the slot free.
//
// So each pull remembers the run it has seen on record for each start
// (noteOwnRunsOnRecord), and once that same run is on record as holding no
// slot, the start stops counting (ownStartsTheRecordsFree). A start whose run
// it has never seen is not touched: a start still on its way to reserving
// keeps its slot for the reasons stalledStarts gives. A run that continues
// afterwards takes a slot again through its own reservation, and its start is
// a new one, counted once from its record.
//
// That left one start out. A start that takes up a run already on record can
// see the run end within a minute — a continuation after GitHub's checks
// passed has only to enter the merge queue — so no pull in between saw the run
// in flight, nothing was remembered, and the start kept its slot until the
// session collected it. So a start that takes up a run names that run when it
// is made (Started.tookUp), and once the item's latest run is that run and it
// has ended since the start was made, the start stops counting as well. The
// run's record already said it had ended before the start, for a stopped run
// a decision continues, so an ending is only this start's if it came after.

// noteOwnRunsOnRecord records, on each start of this session's own, the run on
// record over its item: in flight and holding a slot (occupied) or in flight
// and waiting (waiting). One item never has two runs in flight, and nothing is
// started over an item that has one, so a run on record over an item this
// session has a start for is that start's run.
func noteOwnRunsOnRecord(occupied, waiting map[string]runstate.State, mine map[string]int, started []Started) {
	for id, index := range mine {
		if index < 0 || index >= len(started) {
			continue
		}
		if state, ok := occupied[id]; ok && state.RunID != "" {
			started[index].onRecord = state.RunID
			continue
		}
		if state, ok := waiting[id]; ok && state.RunID != "" {
			started[index].onRecord = state.RunID
		}
	}
}

// ownStartsTheRecordsFree is the starts of this session's own, still counted
// against a slot, whose own run the records say holds none, each with why in
// words a person reads. counted is occupied less what the pull already leaves
// out. Two cases:
//
//   - the start's run is in flight and holds a slot by its record's own fields,
//     but the harness freed that slot after the start was made
//     (runstate.State.SlotReleased), so the reservation no longer counts it;
//   - the start's run is no longer in flight holding a slot, and the item's
//     latest run is the run this session saw on record for the start: it ended,
//     its approved change entered the merge queue, or it waits on GitHub's
//     checks;
//   - the item's latest run is the run the start took up (Started.tookUp), and
//     its record says it completed after the start was made: it landed, its
//     approved change entered the merge queue, it stopped, or it failed.
//
// A start whose goroutine has already handed its ending back (ended) is left
// as it is unless its run succeeded: the session settles that ending at its
// next wait, and a stop or a failure settled before the next start is what the
// brake and the failure count are read from (idleStarts). A success decides
// neither, so its slot is given back without waiting for the session.
func ownStartsTheRecordsFree(runs ScheduleRuns, counted map[string]runstate.State, mine map[string]int, started []Started, ended map[int]bool, history ScheduleRunHistory, now time.Time) map[string]string {
	freed := map[string]string{}
	for id, state := range counted {
		index, ours := mine[id]
		if !ours || index < 0 || index >= len(started) {
			continue
		}
		start := started[index]
		if ended[index] {
			if state.RunID != "" || history == nil {
				continue
			}
			if latest, err := history.Latest(id); err == nil && start.ownRun(latest) && latest.Status == runstate.StatusSucceeded && endedSince(latest, start.began) {
				freed[id] = holdsNoSlotBecause(latest)
			}
			continue
		}
		if state.RunID != "" {
			release := state.SlotRelease
			if !state.SlotReleased() || holdsDeveloperSlot(runs, state, now) || release.At.Before(start.began) {
				continue
			}
			freed[id] = fmt.Sprintf("its record says the harness freed its developer slot at %s, because nothing had worked on it for a while",
				release.At.Local().Format("2006-01-02 15:04:05 MST"))
			continue
		}
		if (start.onRecord == "" && start.tookUp == "") || history == nil {
			continue
		}
		latest, err := history.Latest(id)
		if err != nil || latest.HoldsDeveloperSlot() {
			continue
		}
		seen := start.onRecord != "" && latest.RunID == start.onRecord
		if !seen && !(start.ownRun(latest) && endedSince(latest, start.began)) {
			continue
		}
		freed[id] = holdsNoSlotBecause(latest)
	}
	return freed
}

// ownRun reports the run on record as this start's: the run a pull saw over
// its item, or the run it took up when it was made.
func (s Started) ownRun(state runstate.State) bool {
	if state.RunID == "" {
		return false
	}
	return state.RunID == s.onRecord || state.RunID == s.tookUp
}

// recordedRun is the run this start is known by: the one a pull saw on record
// over its item, or else the one it took up when it was made.
func (s Started) recordedRun() string {
	if s.onRecord != "" {
		return s.onRecord
	}
	return s.tookUp
}

// endedSince reports a run on record as ended at or after since: no longer in
// flight, and completed no earlier than since. A run whose record ended before
// since ended before the start made then, and says nothing about it; one with
// no completion time is not taken as ended, because the time it was last
// written moves for things other than its ending.
func endedSince(state runstate.State, since time.Time) bool {
	if state.Status.InFlight() || since.IsZero() || state.CompletedAt == nil {
		return false
	}
	return !state.CompletedAt.Before(since)
}

// holdsNoSlotBecause is why a run on record holds no developer slot, in words
// a person reads.
func holdsNoSlotBecause(state runstate.State) string {
	switch {
	case state.AwaitingMergeQueue():
		return "its approved change is in the merge queue"
	case state.CIWait.Waiting():
		return "it is waiting on GitHub's checks"
	case state.EpicWait != nil:
		return "it waits for its epic"
	case state.DependencyPause != nil:
		return "it is paused on work its item waits on"
	case state.ForgeWait != nil:
		return "it is waiting for GitHub to be reachable again"
	case !state.Status.InFlight():
		return fmt.Sprintf("it has ended (%s)", state.Status)
	default:
		return "its record says it holds no developer slot"
	}
}

// sayOwnSlotCountCorrected writes, once for each start and run, that this
// session had counted a slot for a run the records say holds none, and that
// it now goes by the records. It names the run and the work item.
func sayOwnSlotCountCorrected(log io.Writer, schedule *Schedule, start *Started, workItemID, why string) {
	known := start.recordedRun()
	if start.countCorrected == known && known != "" {
		return
	}
	run := known
	if run == "" {
		run = "its run"
	} else {
		run = "run " + run
	}
	line := fmt.Sprintf("the scheduler had counted a developer slot as taken by %s of %s, which it started, but %s; the run records say the slot is free, so the scheduler now counts it free",
		run, workItemID, why)
	start.countCorrected = known
	schedule.SlotCountCorrections = append(schedule.SlotCountCorrections, line)
	if log != nil {
		fmt.Fprintln(log, line)
	}
}

// runsTheRecordsFree is the counted entries that name a run, other than this
// session's own starts, whose item's latest record is that same run holding no
// developer slot, each with why in words a person reads. The list of runs in
// flight a pull counts from is read once at its top, and a run can stop holding
// a slot between that read and the count: it goes back to waiting on GitHub's
// checks, its approved change enters the merge queue, or it ends. The run's own
// record is the authority every other count reads (holdsDeveloperSlot), so the
// pull reads it again for each run it is about to count against a slot and
// goes by it. The session's own starts are ownStartsTheRecordsFree's, which
// keeps a start counted while it is still taking its run up again.
func runsTheRecordsFree(runs ScheduleRuns, counted map[string]runstate.State, mine map[string]int, history ScheduleRunHistory, now time.Time) map[string]string {
	freed := map[string]string{}
	if history == nil {
		return freed
	}
	for id, state := range counted {
		if _, ours := mine[id]; ours || state.RunID == "" {
			continue
		}
		latest, err := history.Latest(id)
		if err != nil || latest.RunID != state.RunID || holdsDeveloperSlot(runs, latest, now) {
			continue
		}
		freed[id] = holdsNoSlotBecause(latest)
	}
	return freed
}

// sayRunCountCorrected writes, once for each run, that the pull had counted a
// developer slot for a run its own record says holds none, and that it now goes
// by the record. said is the runs already said, kept for the session.
func sayRunCountCorrected(log io.Writer, schedule *Schedule, said map[string]bool, runID, workItemID, why string) {
	if said[runID] {
		return
	}
	said[runID] = true
	line := fmt.Sprintf("the scheduler had counted a developer slot as taken by run %s of %s, but %s; the run records say the slot is free, so the scheduler now counts it free",
		runID, workItemID, why)
	schedule.SlotCountCorrections = append(schedule.SlotCountCorrections, line)
	if log != nil {
		fmt.Fprintln(log, line)
	}
}

// continuationsNotInTheirSlot is the continuations this session started that
// are still counted against a slot under their run while their dispatch has yet
// to take it back (taking), and have been for countedGrace or more, each with
// why in words a person reads. A continuation keeps the slot it is about to
// take from new work, so the slot is not given away in the moment before its
// dispatch adopts the run; but its run's record still says it waits and holds
// no slot, so the hold is bounded the way a fresh start's is (stalledStarts):
// a dispatch the tracker or a loaded machine keeps waiting past the grace no
// longer keeps a slot empty while ready work waits. If it then takes its slot
// back and finds none free, it is refused for capacity and tried again at the
// next pull, ahead of new work.
func continuationsNotInTheirSlot(taking map[string]bool, mine map[string]int, started []Started, now time.Time) map[string]string {
	freed := map[string]string{}
	for id := range taking {
		index, ours := mine[id]
		if !ours || index < 0 || index >= len(started) {
			continue
		}
		began := started[index].began
		if began.IsZero() || now.Sub(began) < countedGrace {
			continue
		}
		freed[id] = fmt.Sprintf("its run has not taken its developer slot back since this session began taking it up at %s, %s ago, and its record still says it holds none",
			began.Local().Format("15:04 MST"), waitedFor(now.Sub(began)))
	}
	return freed
}
