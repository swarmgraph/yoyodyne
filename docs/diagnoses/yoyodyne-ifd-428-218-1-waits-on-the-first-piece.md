# yoyodyne-ifd.428.218.1: the second piece of stopping a run's processes waits on the first

The second piece of stopping every process a run started (yoyodyne-ifd.428.218.1)
adds the scheduler's start and every maintenance pass to the places that stop a
run's leftover processes, and the report for a process still running after three
passes. It builds on the first piece (yoyodyne-ifd.428.218), as
[the design](../designs/stopping-what-a-run-started.md) splits it.

## Why it was not built on November 27, 2026

At 13:56 AKST on November 27 the first piece had not reached main. Its change
is on branch `yoyodyne/yoyodyne-ifd-428-218/662c7687` (last commit 09ff8f47dc,
13:23 AKST), and main at 5ed934562f carries none of it: nothing on main sets
`YOYODYNE_RUN_ID`, and `internal/execution` has no search or stop. Every part of
the second piece calls code only that branch adds. Copying the branch's files in
would put the first piece's 1,600 or so changed lines into this change too, and the two
changes would then conflict wherever either lands second.

The second piece can be built once the first piece is on main.

## What the second piece uses from the first

Named here so the run that builds it can start from them. All are on the
first piece's branch.

- `execution.RunProcesses.Stop(ctx, RunProcessesRequest{RunID, Holds, Folders})`
  returns `[]execution.LeftProcess` (number, command, listening addresses,
  `Unmarked` for a process named only because it is inside the run's folders,
  and why it was left). It searches for one run's identifier, so the pass needs
  either a way to list every identifier found on the machine or to call it once
  per run record. The design's point 3 asks for the first: a process of a run
  whose record has since been removed is still the harness's to stop.
- `runstate.State.LeftProcesses` and the update `runstate.LeftProcessesFound`,
  which replaces the list. The three-pass count needs a field of its own on each
  entry, or beside the list, counting the passes it has survived, and a mark
  that its report was filed so a fourth pass files nothing.
- `orchestrator.activeRun.stopWhatItStarted`, which the session and run ends
  call. The scheduler's start and the maintenance pass have no active run, so
  they need their own caller, outside the active run, which reads each run
  record and stops only where it shows no developer session, reviewer session
  or check going.
- The maintenance pass is `maintain.Pass`; each of its steps returns a
  `runstate.SweepStep`, so the new step is one more of those.
- The report is filed to the development manager, naming the run, the process
  number, its command and its port. An entry with `Unmarked` set is never
  reported.

## Built

The first piece reached main at 20:56 PST on November 27, 2026, and the second
piece was then built on it. The stop for runs with nothing going is
`maintain.Leftovers` (`internal/maintain/leftovers.go`), taken as the
`leftovers` step of every maintenance pass and once as a `yoyo work` session
starts. It tells a run with nothing going by taking the run's lease without
waiting (`runstate.Store.TryIdleRun`), and counts passes and the report filed
on each entry of the run's record (`execution.LeftProcess.Passes` and
`Reported`).
