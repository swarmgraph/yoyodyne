# yoyodyne-gle: the repair of a run that lost its race re-entered its developer on the old base, and inherited the four races it had already lost

On October 15 the repository search tool for the technical-health program
manager (yoyodyne-ifd.430.59), run `run-91fbcfd0e78f71d5fbecdf5d2aecf8fc`,
pull request #1287, stopped twice with the same message: its approved change
"lost the race to land on main 5 times". The second stop came 41 seconds after
a repair's checks passed, and the reviewer of that repair had noted that the
branch was never brought up to date with main.

**The repair never asked for the change to be brought up to date.** A repair
of a run that lost its race was carried out as an ordinary repair of the
reviewer's findings: the developer was re-entered on the base the run stopped
on, the checks and a review were spent on that base, and the promotion then
found main where it had been for hours. **The count of lost races carried over
from the first stop**, so that one refusal was the fifth "race", and the run
stopped at once without replaying. **The stop called it a lost race** although
nothing landed between the repair starting and its landing: the base was stale
before the repair began.

All times below are PDT, read from the run's record and event log
(`runs/run-91fbcfd0….json` and `….events.jsonl` in the product's state
directory).

## What the record shows

| Time (PDT) | What happened |
|---|---|
| 04:37 | Run started. |
| 06:20 – 09:38 | Five approvals. After each of the first four the promotion found main moved and replayed the change (`integration_retries` 1 to 4); the fourth replay left the recorded base at `c2580dfe`. |
| 09:38 | Fifth approval, carrying one minor note the reviewer marked out of scope (the transcript header calls a search a path read). The promotion found main at `1ca86a02`, not `c2580dfe`. With four races already lost, the fifth gave back the slot: blocker "lost the race to land on main 5 times". |
| 09:40 | The development manager decided a repair: "limited to bringing the kept change up to date with current main and getting it re-checked and re-reviewed", with the minor note allowed alongside. |
| 11:17 | The repair was carried out. The continuation's reason reads "re-entered that run's repair loop on the change it already has"; phase put back to developing; `repair_attempts` 1 to 2; `integration_retries` still 4; base still `c2580dfe`. |
| 11:17 – 11:19 | The developer was invoked in its own session and fixed the minor note, committing `86223377` on top of `c2580dfe`. |
| 11:19 – 12:04 | Checks ran on `86223377` and passed (about 45 minutes). |
| 12:04 | A fresh review approved, "reviewed against base c2580dfe". |
| 12:05 | The promotion refused: main at `348e9a43`, recorded base `c2580dfe`. 4 + 1 = 5, so the run stopped again with the same blocker, without replaying. |

## Why the repair left the change on its old base

A decided repair is carried out by `RepairContinuer.Continue`
(`internal/orchestrator/repaircontinue.go`). Before this change only two of
its routes ever moved a kept change onto the target:

- a repair of an approved change the forge did not merge
  (`replaysDroppedMerge` in `stuckmerge.go`), which puts the run back at its
  promotion so the promotion replays it; and
- a repair of a replay that conflicted (`moveOntoTargetForRepair` in
  `pipeline.go`), which moves the change onto the target with the conflict
  left in it for the developer.

A run that lost its race carries neither: it records no dropped merge and no
conflict, only a failure beginning "integration lost its target branch"
(`State.LostItsRace`). So `continuableRepair` was asked instead, and it
accepted the repair only because the fifth review had left one finding on the
record (`State.HandedBack` is true for any recorded finding, even beside an
approval). The run was put back at developing, `resumeRun` handed the
developer that finding, and nothing between the developer and the checks asks
where the target is. `repairLoop` runs `verify` and the review on whatever the
worktree holds; the only place a moved target is noticed is `integrate`, after
both. Had the fifth review carried no finding, the repair would have been
refused outright with "no failure was ever returned to its developer", which
is the same gap seen from the other side: the one repair a lost race needs had
no route.

## Whether the landing count carried over

It did. `prepareIntegrationRetry` counts each refused promotion in
`State.IntegrationRetries` and stops the run when the next one would reach
`lostRacesBeforeGivingBackTheSlot` (5). `supersedeOnRun` clears the blocker,
the failure, the stop class and the environmental refusal when a repair makes
the run live again, but not that count. The repaired run therefore started
with four races already spent, and its first refusal — the one that only
needed the replay the repair was decided for — was its fifth.

## Whether a stale base was reported as a lost race

It was. `prepareIntegrationRetry` treats every `ErrTargetDrift` the same way,
and the blocker it writes says the change lost "because other work landed
first each time". At 12:05 nothing had raced the run: main had been past
`c2580dfe` since before 09:38, and the repair never tried to land on anything
newer.

## What changed

- A repair decided on a run that lost its race, whose approval still stands
  and whose branch is still there, is carried out as a replay rather than a
  developer attempt (`replaysLostRace` in `lostracerepair.go`). The run is put
  back at its promotion exactly as a dropped merge's repair puts it, and the
  races it lost are cleared with the stop they caused
  (`putBackAfterLostRace`): they bounded how long the stopped stint held a
  developer slot, and the repair is a new stint. No repair attempt is counted
  unless the replay conflicts, which goes to the developer as any conflict
  does.
- A promotion resumed after a stop replays the change when its first attempt
  finds the target moved, without counting or reporting that as a lost race
  (`resumedAtPromotion` and `staleBase` in `prepareIntegrationRetry`). The
  replayed change then earns the checks and a fresh review before it lands, as
  every replay does. This applies to every resumed promotion, a dropped
  merge's replay included, because in each the target moved while nothing was
  trying to land the change.
- `TestARepairOfARunThatLostItsRaceReplaysItOntoTheTargetBeforeItsChecksAndLands`
  (`lostrace_test.go`) reproduces the incident over a real repository: a run
  stops on its fifth lost race with an approval carrying an out-of-scope note,
  main moves again while it is stopped, the repair is carried out, and the
  change lands with no developer invoked, a check that passes only on current
  main, one fresh review, and no race recorded. With `replaysLostRace`
  disabled the same test invokes the developer, as the 11:17 repair did.

## What this does not change

A promotion refused in a run that was not resumed is still counted as a lost
race, including the first promotion of a fresh run whose development took long
enough for main to move: the base was current when that attempt began, so
other work did land first. How kept runs are resumed by hand or held for a
decision is the preserved-run recovery work (yoyodyne-06m), and replays that run
past their time limit are a separate investigation (yoyodyne-qi6).

The resumption the repair records reuses the cause a dropped merge's repair
already records (`queued-head-behind`), because the record's set of causes has
none for "put back to be brought up to date". Its title in the record
describes a queued merge, which a lost race never had.
