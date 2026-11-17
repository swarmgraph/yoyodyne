# yoyodyne-ifd.429.170: why a relaunched merge queue check ran for hours past its 45 minute limit

On the evening of November 12, merge queue entry 61 (MCP calls, piece 8,
yoyodyne-ifd.435.49) held the whole queue from 19:57 to about 23:58 PST. Its
`make test` was stopped at its 45 minute `execution.check_timeout` at 20:44 PST,
launched again at once, and the second launch ran until 23:21 PST, when it
failed on its own. The work item asked where the second launch escaped its
limit.

All times are Pacific (PST). Everything here was read on November 13 from the
harness's own records, the queue's generation record for the entry
(`generations-mqe-5a1355c40dbd9ed15c17e97e9933c5e6.json`) and the event log of
its second generation, and from the machine's power log (`pmset -g log`).

## The answer

**The second launch did not escape its limit. The laptop went to sleep while it
ran, and time asleep does not count against a check's limit.**

- The second launch of `make test` started at 20:44:43 PST and was given the
  same limit as the first: its record says `"timeout": "45m0s"`,
  `"limit": "45m0s"`, `"limit_of": "check"`, under the same 1 h 30 min stage
  bound. Both launches go through the same code (`MergeQueueWorker.check` in
  `internal/orchestrator/mergequeueworker.go`, then `checks.Runner.Run`), which
  hands the limit to the process runner the same way every time.
- At 21:11:51 PST the machine entered sleep, on battery at 1% charge
  (`Entering Sleep state due to 'Low Power Sleep' ... Using Batt (Charge:1%)`).
  It woke from hibernation at 23:12:43 PST, on mains power.
- The check's output stops at 21:03 PST and resumes at 23:21 PST. The
  `internal/orchestrator` package, started before the sleep, reported
  `1946.584s` when it finished at 23:21: the test binary's own clock counted
  about 32 minutes for a stretch that was 2 h 18 min on the wall.
- The check therefore ran about 27 minutes before the sleep and about 9 minutes
  after it: about 36 minutes of the machine being awake, inside its 45 minute
  limit. It ended by failing (`TestAReplacementWaitsForTheSourceTreeAndSurvivesItsOwnLauncherDying`
  in `internal/runstate`), not by being stopped.
- The 35 minutes after that, 23:23 to 23:58 PST, were the queue checking the
  target branch itself at the candidate's base, a separate check that passed in
  34 minutes and is not a relaunch.

## Why time asleep is not counted

The process runner (`internal/execution/process.go`) ends a process with a Go
timer armed for its limit. On macOS that timer measures the time the machine
has been awake, so a laptop that sleeps pauses it, and it resumes on wake. The
recorded elapsed time is the difference between two wall clock readings, so the
same check reads as 2 h 36 min. That is why the record looks like a check that
ran three times past its limit.

Not counting sleep is what the operating rules ask for: a machine that sleeps is
waited out, and only a person or a bound on the work itself ends a run
([the operating rules](../product/operating-rules.md)). A limit that counted
time asleep would stop every check that was running when the lid closed.

## What holds now

- A relaunched queue check is held to its configured limit, and to the stage
  bound, exactly as a first launch is, and is recorded as stopped at its limit
  in the same words.
  `TestARelaunchedQueueCheckIsHeldToItsConfiguredLimit`
  (`internal/orchestrator/mergequeueworker_test.go`) runs a candidate whose slow
  check is stopped at a one second limit, lets the queue launch it again, and
  shows the second launch stopped at the same limit and recorded the same way.
  The test fails if a relaunch is given any longer limit.
- There is no bound on the total time all launches of one candidate's checks
  may take, so none applies to a relaunch; each launch has its own check limit
  and stage bound.

## What this leaves open

- A check's record gives its elapsed time on the wall clock and says nothing
  when the machine slept during it, so a check that slept reads as one that ran
  past its limit. Saying how long the machine was asleep during a check would
  have answered this item from the record alone.
- The queue's state for those two hours was right: nothing could run while the
  machine slept. What stopped the factory was the machine running down to 1%
  battery and sleeping, which no change to the queue reaches.
