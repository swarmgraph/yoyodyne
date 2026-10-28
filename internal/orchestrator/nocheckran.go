package orchestrator

import (
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/followup/checkrun"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// noCheckRan is a change every one of whose configured checks could not run at
// all (checks.CouldNotRunPrefix), so nothing verified it. A check that could not
// run lets the change go on only beside a check that did: with none, the change
// is not integrated and goes to the development manager instead, because a
// review is not a substitute for the checks the project declared.
//
// It is not repair input. What stopped every check is the machine — a tool not
// installed where the checks run, a service they cannot reach, developer tools
// the machine refuses to start — and no change the developer could make starts
// them, so it spends no repair attempt and the round it ends is given back
// (runstate.CauseNoCheckRan).
type noCheckRan struct {
	couldNotRun []runstate.CheckCouldNotRun
}

func (n noCheckRan) Error() string {
	said := make([]string, 0, len(n.couldNotRun))
	for _, check := range n.couldNotRun {
		said = append(said, check.Says())
	}
	return "no check verified the change, because every configured check could not run: " + strings.Join(said, "; ")
}

// noCheckVerified reports a check stage none of whose checks ran, every one of
// them having said it could not run, with each check and its reason. A stage
// with no checks at all is not one: a project that declares none has chosen to
// be judged by review alone, and that is unchanged.
func noCheckVerified(results []checks.Result) (noCheckRan, bool) {
	if len(results) == 0 {
		return noCheckRan{}, false
	}
	var refused noCheckRan
	for _, result := range results {
		if result.CouldNotRun == "" {
			return noCheckRan{}, false
		}
		refused.couldNotRun = append(refused.couldNotRun, runstate.CheckCouldNotRun{Command: result.Command, Reason: result.CouldNotRun})
	}
	return refused, true
}

// blockOnNoCheckRan ends a run no check could run on. The cause is recorded as
// the machine's before the item is blocked, so the round is given back where it
// settles and the session that hosted the run reads the stop as the machine's
// (machineChecksHold); the blocker names each check and why it could not run,
// which is what the development manager's docket entry carries; and the branch
// and worktree are kept for whatever she decides.
func (a *activeRun) blockOnNoCheckRan(refused noCheckRan) error {
	a.recordEnvironmentalRefusal(runstate.CauseNoCheckRan, refused.Error(), ranAnyway)
	a.state.ChecksPassed = nil
	class := runstate.CauseNoCheckRan.StopClass()
	if err := a.block(renderNoCheckRanBlockerNotes(a.outcome, refused)); err != nil {
		return stoppedBy(class, withFailedRecord(refused, fmt.Errorf("record that no check could run on the change as a blocker: %w", err)))
	}
	return stoppedBy(class, refused)
}

// renderNoCheckRanBlockerNotes describes a run stopped because no configured
// check could run on its change.
func renderNoCheckRanBlockerNotes(outcome Outcome, refused noCheckRan) string {
	lines := []string{
		"Yoyodyne did not integrate this item's change: every configured check could not run, so no check verified it.",
		"Nothing was handed back to the developer and no repair attempt or review round was spent, because what stopped the checks is the machine they run on rather than the change.",
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
		"The branch and worktree are preserved. Once the checks can run again, the change can be run again from where it stopped.",
		"Checks that could not run:",
	}
	for _, check := range refused.couldNotRun {
		lines = append(lines, "- "+check.Says())
	}
	return strings.Join(lines, "\n")
}

// defaultMachineChecksProbe is how long a session holds new work over checks
// that could not run where its pull names no probe interval: the default of
// execution.usage_limit_unknown_reset_pause, which is the interval Pull.OutageProbe
// carries.
const defaultMachineChecksProbe = 30 * time.Minute

// machineChecksHold is a session having met a run no configured check could run
// on. What stopped those checks is the machine, so every other ready item would
// meet it too: the session says so once and chooses nothing until the probe
// interval has passed since it was met. Then it starts exactly one run, whatever
// the free developer places, and starts nothing else until that run ends. A run
// that meets it again holds again for another interval; one that does not has
// found it cleared, and the session chooses as usual. Only the runs that met it
// are put in front of the development manager, rather than one run for every
// ready item.
//
// It is the session's own, like the refusals it already keeps between pulls:
// the run that met it is on the durable record and the docket, and a session
// started afterwards finds out with one run of its own.
type machineChecksHold struct {
	runID      string
	workItemID string
	// title is the work item's title as the pull that started the run read it,
	// and empty where it read none.
	title  string
	detail string
	since  time.Time
	// probeStarted is the one run started once the interval passed, and
	// probeIndex its entry on the schedule, which is how its ending is told
	// apart from any other run's.
	probeStarted bool
	probeIndex   int
	// followUp is the follow-up the hold waits on where the session recognised
	// the cause and wrote one, hold the hold's name on it, and check what its
	// check confirms in ordinary words. A hold with a follow-up starts no run to
	// find out: the check runner is what re-checks its cause (followups.go).
	followUp string
	hold     string
	check    string
}

// machineChecksState is what a session does about the hold at one pull.
type machineChecksState int

const (
	// machineChecksClear chooses as usual.
	machineChecksClear machineChecksState = iota
	// machineChecksWaiting chooses nothing: the interval has not passed, or the
	// one run started to find out is still going.
	machineChecksWaiting
	// machineChecksProbe starts one run and no more.
	machineChecksProbe
)

// machineChecksHeldBy is the hold a finished run places, where it stopped
// because no configured check could run on its change. title is the item's
// title as the session read it when it started the run.
func machineChecksHeldBy(outcome Outcome, title string, at time.Time) (machineChecksHold, bool) {
	refused := outcome.Environmental
	if refused == nil || refused.Cause != runstate.CauseNoCheckRan {
		return machineChecksHold{}, false
	}
	return machineChecksHold{runID: outcome.RunID, workItemID: outcome.WorkItemID, title: strings.TrimSpace(title), detail: refused.Detail, since: at}, true
}

// item names the work item the way a person reads it: its title with its
// identifier after it, or the identifier alone where no title was read.
func (h machineChecksHold) item() string {
	if h.title == "" {
		return h.workItemID
	}
	return h.title + " (" + h.workItemID + ")"
}

// state is what the hold asks of the pull at now, with what the session says
// while it waits, for a probe interval of probe (defaultMachineChecksProbe where
// it is not positive).
func (h machineChecksHold) state(now time.Time, probe time.Duration) (string, machineChecksState) {
	if h.since.IsZero() {
		return "", machineChecksClear
	}
	if probe <= 0 {
		probe = defaultMachineChecksProbe
	}
	met := fmt.Sprintf("on run %s of %s, %s; the stopped run is with the development manager",
		h.runID, h.item(), singleLine(h.detail, runstate.MaxEnvironmentalDetailBytes))
	if h.followUp != "" {
		return fmt.Sprintf("runs are not being started: %s, and the harness checks every %d minutes whether %s and starts work again once it has (follow-up %s)",
			met, int(checkrun.DefaultCommandInterval/time.Minute), h.check, h.followUp), machineChecksWaiting
	}
	if h.probeStarted {
		return "runs are not being started: " + met + ", and the one run started to find out whether the checks can run again is still going", machineChecksWaiting
	}
	next := h.since.Add(probe)
	if !now.Before(next) {
		return "", machineChecksProbe
	}
	return fmt.Sprintf("runs are not being started: %s, and one run will be started at %s to find out whether the checks can run again",
		met, next.Local().Format("Jan 2 15:04 MST")), machineChecksWaiting
}

// settled is the hold once the schedule's entry at index has ended with
// outcome, where the outcome did not hold again. The run started to find out
// clears it by ending any other way; an entry that never became a run found
// nothing out, so the next pull starts one again.
func (h machineChecksHold) settled(index int, outcome Outcome) machineChecksHold {
	if !h.probeStarted || index != h.probeIndex {
		return h
	}
	if outcome.RunID == "" {
		h.probeStarted = false
		return h
	}
	return machineChecksHold{}
}
