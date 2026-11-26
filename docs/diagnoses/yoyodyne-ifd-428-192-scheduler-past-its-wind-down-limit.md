# yoyodyne-ifd.428.192: why the scheduler did not restart itself at its wind-down limit

When a new build is installed and a move onto it is due, the scheduler winds
down for at most `execution.redeploy_drain_limit` (15 minutes), then stops the
runs it is still carrying, keeps them for the session that comes back, and
restarts itself on the new build. Several times it went past that limit without
restarting, until the supervisor stopped it five minutes later. This records
why, from the code and from the harness's own records. All times are PST.

## The records it rests on

- The watch log, `products/yoyodyne/watch.jsonl` in the harness's state
  directory: every line each scheduler session wrote, with the build it ran
  and, while winding down, when the wind-down began and when its limit fell.
- The supervisor's log, `products/yoyodyne/supervisor/supervisor.log`: when it
  found the scheduler past its limit and restarted it.
- The tracker's record of slow calls, `products/yoyodyne/tracker-slow-calls/`.

## What the records show

The supervisor stopped a scheduler that was winding down five times between
November 19 and November 22: 18:26 and 22:15 on November 19, 06:08 on
November 20, 15:37 on November 21, and 06:02 and 09:26 on November 22. Those
stops are logged as "the scheduler was cancelled". Since 09:26 on November 22,
every move in the log was the scheduler restarting itself, within seconds of its
limit or while it waited for a promotion or a running check that it is meant
to wait for.

Three of the forced stops show the same thing:

| Session (build) | Wind-down began | Limit | Last line before the stop | Stopped by the supervisor |
| --- | --- | --- | --- | --- |
| 80719ab9 (697f01f0), Nov 21 | 15:17:24 | 15:32:24 | 15:37:35: "filled 2 of 2 free developer slots" | 15:37:56 |
| eef98698 (3afa6d33), Nov 22 | 05:41:37 | 05:56:37 | 06:01:59: "filled 2 of 2 free developer slots"; 06:02:09: a recurring pass taken | 06:02:38 |
| c4742b3b (60d981f8), Nov 22 | 09:05:39 | 09:20:39 | 09:20:08: a recurring pass taken; then nothing until the stop cancelled a start partway | 09:26:33 |

In every one, the session never wrote the line it writes when it finds its
limit has run out. Two of them started new developer runs five minutes after
their limit, which the scheduler is built never to do. The supervisor's log
says the same from its side, for example at 06:01:53 on November 22: "the
scheduler service has been past its limit for winding down to move onto a new
build for 5m15s, since 05:56 PST, without restarting itself".

The tracker was slow in those minutes. At 05:59:38 on November 22 a tracker
write ran its whole two-minute limit, waiting on another tracker command holding
the store.

## The cause

The scheduler's loop applies the wind-down limit in one place: at the top of the
loop, before it opens the next pull (`Scheduler.Schedule` in
`internal/orchestrator/schedule.go`). Within a pull it asked the drain once per
kind of start whether starting work was still worth it. That question compares
the time left before the limit with one poll (a minute). A pull that asked with
more than a minute left went on to start whatever it found.

One pull can take longer than the whole limit. It reads the queue and the
backlog from the tracker, whose calls are each allowed two minutes. It writes a
reason onto each item it passes over, and asks of each entry it reaches whether
it may start, which reads the repository. It waits up to two minutes for each
decision it carries out to begin its run. While the tracker is contended, these
add up to several minutes. During that time the session is not at the top of the
loop, so the limit is not applied. Once the pull has asked, nothing in it looks
at the clock again. So the pull starts work found past the limit, and gets back
to the top of the loop only when it is done. If that is more than five minutes
past the limit, the supervisor has already stopped the session.

The wait at the end of a pull is not the cause. Every wait the session makes
returns at the next poll, or when the limit's timer fires, so the session gets
back to the top of the loop within a minute of leaving a pull. The time is lost
inside the pull.

## The fix

The pull now asks the drain again, against the current clock, before every
start it makes:

- before each further round of decisions it carries out;
- after those rounds, before it continues paused runs or takes repairs decided
  from pull request feedback;
- before each entry of the queue a free slot reaches, and before handing a slot
  to a held-back decision at the end of the queue.

Once the drain declines, the pull starts nothing more. It leaves the rest to the
session that comes back, as a pull the drain declines at the outset already
does. It then goes back to the top of the loop within a poll, where the limit is
applied and the session stops its runs and restarts.

The test `TestAPullStillReadingWhenTheWindDownBoundRunsOutStartsNothingAndRestarts`
(`internal/orchestrator/drainmidpull_test.go`) reproduces the cause. In it, a
move has begun, the free slot's work has just become ready, and a write the pull
makes on an item it passes over takes the clock past the limit. Without the fix
the old build starts the newly ready work after its limit. With the fix it
starts nothing, and restarts at the next look with its running run kept for the
new session.

## What this does not change

- **A move that begins late.** The move due at 21:02 on November 22 began at
  21:23:40, because that session was inside one pull from about 21:09 until
  21:23:40 and finds a newly installed build only at the top of its loop. The
  mechanism is the same, a pull longer than the time the scheduler is meant to
  react within, but on the side of beginning the move. This fix does not touch
  it.
- **The supervisor's backstop for a move that has not begun.** The guess on the
  work item was that each supervisor restart starts the scheduler's wind-down
  clock over. For a scheduler already past its limit, it does not: the
  supervisor reads that from the watch log, which outlives it
  (`readmodel.DrainRestartDue`), and both forced stops on November 22 came from
  that reading. For a move that is due and has not begun, it does. The
  supervisor keeps when it first found the scheduler behind, and when it first
  found the move overdue, only in memory (`behindSince` and `overdueSince` in
  `internal/supervise/deploy.go`). On a restart it counts the scheduler as
  behind only since the newest binary was written. The supervisor moves onto
  every newly installed build, so its grace for a move that has not begun (the
  wind-down limit and five minutes) can start over at each one.
