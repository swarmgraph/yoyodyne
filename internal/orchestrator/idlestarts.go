package orchestrator

import (
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ScheduleRunHistory reads the most recent run recorded for one work item,
// whatever became of it (runstate.Store.Latest). A pull asks it of its runs
// where they satisfy it, which *runstate.Store does, to tell a start of its own
// whose run has already stopped working from one still on its way to reserving
// (idleStarts). Runs that do not satisfy it leave such a start counted until
// the session collects it, which is what every pull did before this existed.
type ScheduleRunHistory interface {
	Latest(workItemID string) (runstate.State, error)
}

// idleStarts is the starts of this session's own that a pull counts against a
// developer slot for want of a run on record holding one, when what the
// session knows shows that nothing of theirs is working in a slot. A slot is
// counted taken only for a run working in it, and these are not:
//
//   - a start whose run is recorded as waiting, on GitHub's checks or on work
//     its item waits on (waiting), which holds no slot
//     (runstate.State.HoldsDeveloperSlot), whether or not its ending has been
//     handed back yet;
//   - a start whose run is on record and has ended, or holds no slot for any
//     other reason, while its process is still finishing what it does after,
//     such as writing to a slow tracker: a change approved and admitted to the
//     merge queue, a stopped run whose branch is kept, a landing being checked.
//
// The last is read from the item's latest run, and taken as this start's only
// where it is the run the start continued (Started.Readopted, or the run a
// continuation took up) or one recorded as starting no earlier than the start
// itself; an earlier run of the item is not this start's, and a start that has
// not reserved yet keeps its slot for the short time countedGrace allows.
//
// A start whose goroutine has already finished and is handing its ending back
// (ended) is left out the same way where its run is recorded waiting: the run
// holds no slot whether or not the session has collected the ending, which is
// the rule the health check reads the slots by, and a run that paused comes
// back paused, which neither the brake nor the failure count reads. The
// session collects one ending at each wait and none after a pull that started
// work, so on a slow tracker a run that paused on GitHub's checks could wait
// several pulls, minutes each, to be collected, and its slot stood empty for
// all of them while the health check read it free.
//
// Any other ending handed back stays counted, and so does a start the brake
// made under its hold (Started.Probe), whatever its run recorded: the session
// settles it at its next wait, which is at most a poll away, and settling such
// an ending before the next start is what the brake and the failure count are
// read from. The rest of this is for a run's process that is still going after
// its run stopped working, which nothing collects until it returns. Such a
// process can take minutes on a slow tracker, and the slot is given to ready
// work meanwhile rather than kept for it; the session still settles its ending
// when it comes back.
//
// Each start stays in occupied, so its item is still never given a second run
// beside it; it is only left out of the count of free slots. Leaving it out
// never gives one slot to two runs: the reservation counts the runs on record
// under its lock (runstate.Store.Reserve), and a run of this start's that
// could still take a slot would have to reserve or reclaim one there.
func idleStarts(occupied, waiting map[string]runstate.State, mine map[string]int, started []Started, ended map[int]bool, continuing map[int]string, history ScheduleRunHistory) map[string]bool {
	idle := map[string]bool{}
	for id, state := range occupied {
		index, ours := mine[id]
		if !ours || state.RunID != "" || index < 0 || index >= len(started) {
			continue
		}
		_, recorded := waiting[id]
		if ended[index] && (!recorded || started[index].Probe) {
			continue
		}
		if recorded {
			idle[id] = true
			continue
		}
		if history == nil {
			continue
		}
		latest, err := history.Latest(id)
		if err != nil || latest.RunID == "" || latest.HoldsDeveloperSlot() {
			continue
		}
		start := started[index]
		mineRun := latest.RunID == start.Readopted || latest.RunID == continuing[index] ||
			(!start.began.IsZero() && !latest.StartedAt.Before(start.began))
		if mineRun {
			idle[id] = true
		}
	}
	return idle
}

// runHistory is the pull's runs as a ScheduleRunHistory, where they are one.
func runHistory(runs ScheduleRuns) ScheduleRunHistory {
	if history, ok := runs.(ScheduleRunHistory); ok {
		return history
	}
	return nil
}
