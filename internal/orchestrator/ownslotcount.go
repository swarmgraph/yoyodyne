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
//     checks.
//
// A start whose goroutine has already handed its ending back (ended) is left
// as it is: the session settles that ending at its next wait, which is what
// the brake and the failure count are read from (idleStarts).
func ownStartsTheRecordsFree(runs ScheduleRuns, counted map[string]runstate.State, mine map[string]int, started []Started, ended map[int]bool, history ScheduleRunHistory, now time.Time) map[string]string {
	freed := map[string]string{}
	for id, state := range counted {
		index, ours := mine[id]
		if !ours || index < 0 || index >= len(started) || ended[index] {
			continue
		}
		start := started[index]
		if state.RunID != "" {
			release := state.SlotRelease
			if !state.SlotReleased() || holdsDeveloperSlot(runs, state, now) || release.At.Before(start.began) {
				continue
			}
			freed[id] = fmt.Sprintf("its record says the harness freed its developer slot at %s, because nothing had worked on it for a while",
				release.At.Local().Format("2006-01-02 15:04:05 MST"))
			continue
		}
		if start.onRecord == "" || history == nil {
			continue
		}
		latest, err := history.Latest(id)
		if err != nil || latest.RunID != start.onRecord || latest.HoldsDeveloperSlot() {
			continue
		}
		freed[id] = holdsNoSlotBecause(latest)
	}
	return freed
}

// holdsNoSlotBecause is why a run on record holds no developer slot, in words
// a person reads.
func holdsNoSlotBecause(state runstate.State) string {
	switch {
	case state.AwaitingMergeQueue():
		return "its approved change is in the merge queue"
	case state.CIWait.Waiting():
		return "it is waiting on GitHub's checks"
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
	if start.countCorrected == start.onRecord && start.onRecord != "" {
		return
	}
	run := start.onRecord
	if run == "" {
		run = "its run"
	} else {
		run = "run " + run
	}
	line := fmt.Sprintf("the scheduler had counted a developer slot as taken by %s of %s, which it started, but %s; the run records say the slot is free, so the scheduler now counts it free",
		run, workItemID, why)
	start.countCorrected = start.onRecord
	schedule.SlotCountCorrections = append(schedule.SlotCountCorrections, line)
	if log != nil {
		fmt.Fprintln(log, line)
	}
}
