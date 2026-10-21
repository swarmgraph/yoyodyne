# yoyodyne-ifd.428.99.19: an approved change lost all five landing attempts, and every one of them had been replayed onto main as it then stood

The program manager standing-goals rule (yoyodyne-ifd.430.24, run-cad03c37,
pull request #1391) was approved five times and lost all five landing attempts.
The run stopped at 00:29 PDT on 2026-10-21 with its recorded base at 7a0f0dfd
while main had reached a854f641. That looked like a run that never moved its
change onto the new main between attempts.

The run's own record says otherwise. Every attempt that lost was followed by a
replay onto the tip main stood at in that moment, and every replay was checked
again in full and reviewed again by a fresh reviewer. The recorded base is the
base of the last replay. It is updated on every replay. What the run lost to was
time: each replay took about an hour to check and review, and on that evening
main took another pull request inside every one of those hours.

## What the records say

All times are PDT, from the product's state directory: `runs/run-cad03c37….json`,
its `events.jsonl`, the scheduler log, and the primary checkout's reflog for
`main`. "Caught up" is the moment the local `main` was fast-forwarded onto what
the forge had.

| When | What |
| --- | --- |
| Oct 20 18:07 | The run claims the item on build de570b66, and its change is based on main at de570b66. The forge had merged 7d1d9076 a minute earlier. |
| 18:22 | The developer finishes; checks start. |
| 18:31 | main caught up to 7d1d9076 (pull request #1389). |
| 18:48 | The scheduler restarts into a new build and takes the run over during its checks. |
| 19:17 | Review 1 approves the change on base de570b66. The landing catches main up, finds it at 7d1d9076, and refuses before asking the forge. Replayed onto 7d1d9076. |
| 20:13, 20:18 | main caught up to e124b62d, then to 34564fac (pull requests #1395 and #1390). |
| 20:18 | Review 2 approves on 7d1d9076. Refused the same way. Replayed onto 34564fac. |
| 20:47 | Second takeover by a restarted scheduler, during checks. |
| 21:40 | `make race` fails on a timing test; the developer is sent the failure and answers by 21:48. Checks start again. |
| 22:33 | Review 3 approves on 34564fac. main caught up to 8c6b1088, which is where the forge had it at that moment. Refused, replayed onto 8c6b1088. |
| 22:38 | main caught up to 7a0f0dfd (pull request #1396), five minutes after that replay. |
| 22:58 | Third takeover, during checks, onto build 7a0f0dfd. |
| 23:29 | Review 4 approves on 8c6b1088. Refused, replayed onto 7a0f0dfd. |
| Oct 21 00:16, 00:27 | main caught up to f098ce9a, then a854f641 (pull requests #1392, #1399 and #1400). |
| 00:25 | Review 5 approves on 7a0f0dfd. Refused: the fifth lost race, which is where a run stops replaying and gives back its developer slot. |

Five reviews, five different bases: de570b66, 7d1d9076, 34564fac, 8c6b1088,
7a0f0dfd. Each one is the tip main had when the attempt before it lost.

## Which step stopped each attempt

All five stopped at the same step, in `activeRun.integrate`
(`internal/orchestrator/pipeline.go`). Under the promotion lease the landing
first checks where the remote's `main` stands (`settleRemoteTarget`). Finding it
moved, it fast-forwards the local `main` onto it. Then
`Manager.PrepareLanding` (`internal/gitworktree/manager.go`) compares the local
`main` with the base the change was checked and reviewed on, and refuses with
"target branch moved away from the recorded base commit". The forge is never
asked to merge a change written against a main it no longer has.

That refusal does not skip the replay. It is what starts it:
`prepareIntegrationRetry` counts the lost race and replays the change onto the
local `main`, which the landing has just brought onto the forge's tip. It then
updates the recorded base, republishes the pull request, and clears the
approval so the gate is earned again. Only the fifth refusal ends without a
replay, because it is the fifth lost race.

## Which build ran it

The run started on build de570b66 and ended on build 7a0f0dfd. Both include the
earlier fix that replays a refused landing onto main as it stands and resets the
lost-race count after a repair the development manager decides
(yoyodyne-ifd.428.99.16, merged at 13:55 PDT on October 20 as 036690a4). An
older build was not the cause.

## Whether the takeovers shortened the run

No. All three takeovers came during checks, never at a landing, and each carried
the run's counters over as they were, which is right: the five lost races were
five real races, each lost to pull requests the forge merged during that
attempt's window. The reset added by the earlier fix is for a run that a repair
resumes at the landing step, whose first refusal is a base that went stale while
nothing was trying to land it. None of these refusals was that. The takeovers
did cost time: each cancelled `make race` and started it again, up to about
five minutes each.

## Binding role definitions, October 19

The run for binding role definitions (yoyodyne-ifd.434.6, run-9af62945) shows
the same pattern on build 26f5e3d0, before the earlier fix. Its seven reviews
stood on six different bases, each the tip main had reached. It landed at
17:24 PDT on October 19, on its seventh review.

## What this leaves

No landing path skips the replay, so there is no replay to add. The test
`TestEveryLandingAttemptOnAProtectedTargetIsPreparedOnTheTipTheLastOneLostTo`
(`internal/orchestrator/landingtests`) reproduces this path. It moves the
forge's `main` while each of three approvals is given, and asserts that each
attempt stopped before the forge was asked to merge and that the next attempt was
checked and reviewed on the tip the last one lost to.

What does cost these runs their landing is that a replay is checked in full and
reviewed again before it can try to land. On a loaded machine that took 55 to
60 minutes each time here, and over two hours once a check failed and the
developer was asked about it. At busy times main takes another pull request
inside that window. Here five lost races held a developer slot for about six
hours, on a change that was approved every time. Making that window shorter, or
landing through a queue that tests the change on top of what is ahead of it, is
work this item did not cover.
