# yoyodyne-ifd.428.95: why ready top-priority fixes went days without a run

The development manager reported on October 13 at 02:51 PDT that three ready
fixes had gone days without a run: the re-run fix (yoyodyne-ifd.428.94), the fix
for stopped publications that have no work item (yoyodyne-ifd.433.21.3), and the
fix to the done-condition check (yoyodyne-c4i). Later notes on the item add four
more top-priority items that waited the same day — this investigation itself,
the publication failure investigation (yoyodyne-ifd.433.36), this repository
declaring its document checks (yoyodyne-fp3.4), and the terms check fix
(yoyodyne-ifd.437.45) — and, from about 15:50 PDT, the maintenance reconcile fix
(yoyodyne-ifd.428.96).

All times are Pacific (PDT). The records read are the harness's own, as they
stood on October 14 at about 01:35: every run record under the product's `runs`
directory, the watch session's record of each pull (`watch.jsonl`), the
scheduler's log of what each pull passed over and why (`scheduler.log`, which
carries no times of its own; each line is dated here by the run the same pull
started), and the tracker export copied into this run's worktree.

## The answer

- **Two of the three were not top-priority work for nearly all of the time.**
  The re-run fix (yoyodyne-ifd.428.94) was at priority 1, about 123rd in the Lead
  Product Manager's order, from October 10 until the Lead Product Manager raised
  it to priority 0 after the development manager's report recorded at 01:46 PDT
  on October 13. No slot freed between then and 03:08 PDT, and the pull at 03:08
  took it. The done-condition check fix (yoyodyne-c4i) was admitted at priority 1
  ("P1, so nothing at P0 moves back") and is still at priority 1, about 130th in
  the order. Below priority 0 both waited their turn in the order, and slot 2's
  label preference reordered the work around them, which is what it is
  configured to do. The item's title and the report call them ready
  priority-0 fixes; the tracker does not.
- **A defect let lower-priority preserved work take slots ahead of ready
  priority-0 work, and it is fixed in this change.** A decided repair or re-run
  of a stopped run whose branch or worktree is still there was outranked by
  nothing (yoyodyne-ifd.428.81). The run of queued messages in yoyo chat
  (yoyodyne-99z, priority 1) used that three times: at 19:46 PDT October 12
  ahead of one priority-0 item, at 00:47 PDT October 13 ahead of three — the
  publications fix (yoyodyne-ifd.433.21.3) first among them — and at 12:47 PDT
  October 13 ahead of seven, this investigation among them. The 00:47 slot is
  the one the publications fix would have had; it started at 07:02 instead.
- **What kept every slot busy was runs replaying lost races, and that has no
  limit.** When main moves between a change's approval and its promotion, the
  run replays the change onto main, runs its checks again, and asks for a new
  review, all while it holds its developer slot. The code says losing the race
  "never stops the run", and "the run replays for as long as the target keeps
  moving" (`prepareIntegrationRetry` in `internal/orchestrator/pipeline.go`).
  The yoyo chat run lost sixteen races in all. Between 02:51 and 14:31 PDT on
  October 13 it was approved fourteen times, and only the last approval reached
  the forge. That is the twelve and a half hours the Lead
  Product Manager read as one run sitting in its checks: no single check stage
  overran its bound, so no check limit had anything to end. Most runs since
  October 11 lost between one and seven races; the re-run fix lost seven and the
  publications fix six. Fixing this is larger than this run; see
  [the fix this needs](#the-fix-the-lost-races-need).

## Each of the three, decision by decision

A pull walks the order and gives each free slot to the first item it may start,
so it records the item it chose and only the items it walked past and had
something to say about. Where an item below is not named by a pull, that pull
gave its slot to an item ahead of it in the order.

### Before any of them could run: October 10

- From 03:40 to 04:31 the scheduler started about seventy runs. Each was refused
  by the provider's exhausted seven-day usage limit, which reset at 20:00. The
  done-condition check fix ran from 03:43 to 03:50 and the re-run fix from 04:05
  to 04:25; both were given back to the queue with nothing charged.
- From 04:32 to 20:00 the scheduler was paused on that usage limit and every
  item was passed over as waiting on it.
- From 20:00 to 01:39 on October 11 intake was held by the harness's own brake
  after runs blocked in a row; only its probe runs started.

### The re-run fix (yoyodyne-ifd.428.94)

At priority 1 until about 01:50 on October 13. The pulls that named it:

| Pull (PDT) | What it started | What it said of the re-run fix |
|---|---|---|
| Oct 10, before 04:05 | (usage-limit storm) | held back: it would race run-5d62f75e of the orchestrator test limit (yoyodyne-ifd.429.68) |
| Oct 10, 21:23 or 23:00 | a probe run | held back: it would race run-7bb4ffa4 of yoyodyne-ifd.433.25 |
| Oct 11, 01:44 | yoyodyne-oq3 | held back: it would race run-a6631283 of yoyodyne-ifd.432.26 |
| Oct 11, 05:50 | yoyodyne-ifd.209.31 | held back: it would race the run just started for yoyodyne-ifd.209.31 |
| Oct 11, 07:10 | yoyodyne-ifd.437.23 | held back: it would race run-1442124d of yoyodyne-ifd.209.31 |
| Oct 12, 00:15 | yoyodyne-kuw | left for another slot: slot 2 prefers the usability label and took kuw |
| Oct 12, 04:30 | yoyodyne-403 | held back: it would race run-66106222 of yoyodyne-fp3.1 |
| Oct 12, 06:12 | yoyodyne-ede | held back: it would race run-66106222 of yoyodyne-fp3.1 |
| Oct 12, 12:59 | yoyodyne-q4q | left for another slot: slot 2 took q4q |
| Oct 12, 15:53 | yoyodyne-99z | left for another slot: slot 2 took 99z |
| Oct 13, 03:08 | the re-run fix | started, at priority 0, position 24 |

Every other pull from 01:43 on October 11 to 01:50 on October 13 started an
item ahead of it in the order: hea, 5ob, au1, 4qw, 66h, 7my, xtx (twice), ur1,
the load-scaled check limits (yoyodyne-ifd.429.41), 8i0, fp3.1, trt, bhb, 48j,
bhb.1, yoyodyne-ifd.436.2, yoyodyne-ifd.430.48, yoyodyne-ifd.429.47 and fp3.1.1,
plus the development manager's decisions she had the harness carry out. Its run
then took from 03:08 to 11:15, losing seven races to land.

The seventeen holds behind the run stopped for a redeploy on October 2
(yoyodyne-ifd.428.37) that the program manager's note points to are all in the
log before October 10; that cause was fixed by yoyodyne-j2u and does not recur
here.

### The publications fix (yoyodyne-ifd.433.21.3)

Admitted at 17:50 on October 12 at priority 1, under a parent that was held;
the program manager asked at 17:53 for it to be moved out so it could be
pulled, and the Lead Product Manager moved it and raised it to priority 0 the
same evening. The records read here do not give the minute it became ready.

| Pull (PDT) | What took the slot | Why not the publications fix |
|---|---|---|
| Oct 12, 18:12 | yoyodyne-ifd.436.2, priority 1, in a slot with no preference | not yet ready at priority 0: a slot with no preference takes the first startable item in the order |
| Oct 12, 19:46 | the yoyo chat run's checks, continued as preserved work | that continuation named only yoyodyne-ifd.430.48 as ready priority-0 work it went ahead of, so the fix was not yet ready |
| Oct 12, 20:36 | yoyodyne-ifd.430.48, priority 0 | ahead of it in the order |
| Oct 12, 22:47 | yoyodyne-ifd.429.47, priority 0 | ahead of it in the order |
| Oct 13, 00:13 | fp3.1.1, priority 0 | ahead of it in the order |
| Oct 13, 00:47 | the yoyo chat run's repair, priority 1, as preserved work | **the defect**: the run's reason names the publications fix first among three ready priority-0 items it went ahead of |
| Oct 13, 03:08 | the re-run fix, priority 0 | ahead of it in the order |
| Oct 13, 07:02 | the publications fix | started, at priority 0, position 26 |

No slot freed between those pulls. Its run then took until 14:40, losing six
races to land and being stopped once for a redeploy.

### The done-condition check fix (yoyodyne-c4i)

At priority 1 throughout, about 130th in the order. After its usage-limited run
on October 10 the pulls that named it all said the same thing: it was left for
another slot, because slot 2 prefers the usability, product or performance
labels and took labelled work instead — yoyodyne-ifd.209.31 (05:50 October 11),
yoyodyne-ifd.437.23 (07:10 October 11), kuw (00:15 October 12), 403 (04:30),
ede (06:12), q4q (12:59) and 99z (15:53). Every other pull started an item ahead
of it in the order. Nothing has started it since, and nothing will until the
priority-1 work ahead of it is done or it is raised.

## The later instances

- **09:10 to 14:32 on October 13** (this investigation, yoyodyne-ifd.433.36,
  fp3.4, yoyodyne-ifd.437.45): all three slots held live runs — the yoyo chat
  run replaying, the re-run fix replaying, and the publications fix replaying.
  The slot the re-run fix freed at 11:15 went to a decided repair of
  yoyodyne-ifd.429.47, which is priority 0 and ahead in the order. At 12:47 the
  yoyo chat run's continuation went ahead of seven ready priority-0 items (the
  defect). The slots that freed from 14:31 went to priority-0 work at once:
  yoyodyne-ifd.433.36 at 14:32, yoyodyne-ifd.430.52 at 14:40, fp3.3 at 14:41,
  fp3.4 at 17:53. A development-manager decision for yoyodyne-ifd.430.19 also
  started at 14:31 and failed at once.
- **From about 15:50 on October 13** (the maintenance reconcile fix,
  yoyodyne-ifd.428.96): every pull after it was admitted took priority-0 work
  ahead of it in the order — the heavyweight-check limits (yoyodyne-ifd.429.48)
  at 17:49, fp3.4 at 17:53, yoyodyne-ifd.430.13.20 at 20:34, and three more at
  01:29 to 01:31 on October 14 — apart from a development-manager decision for
  yoyodyne-ifd.430.19 at 17:44. No slot freed from 20:34 to 01:29: fp3.4 lost six
  races and yoyodyne-ifd.430.13.20 three. The heavyweight-check limits run the
  development manager asked about was pulled by the scheduler, at priority 0,
  position 11; nobody started it by hand.

## Causes ruled in and out

- **A refusal when work is started, before any run is reserved**
  (yoyodyne-ifd.434.14): ruled out. The scheduler's records hold no failed start
  for any of the three items.
- **The read of ready work timing out** (yoyodyne-6u7): ruled out for this
  window. The scheduler recorded one tracker timeout after October 9, at 03:55
  on October 10, on another item (`bd show` for yoyodyne-4qw), and waited it out.
  The log's other tracker timeouts are warnings that the pull continued past.
- **Slots held by runs recorded as running**: ruled in, but every such run was
  alive. Each long-held slot belonged to a run producing events — replaying lost
  races, mostly. Three runs that look like they ran for fifteen to twenty-three
  hours on October 10 and 11 (yoyodyne-ifd.435.10, yoyodyne-ifd.433.25 and
  yoyodyne-oq3) ended at about 22:53 on October 10; their records were only
  edited later.
- **Files reserved by runs still recorded as active**: ruled in for the re-run
  fix while it was at priority 1 (eight pulls above), never once it was at
  priority 0. None of the reservations was held by a dead run in this window.
- **The dead-run audit's quiet period against the redeploy wait** (the concern
  left at `internal/orchestrator/claims.go`): the audit waits 30 minutes
  (`readmodel.DefaultDeadClaimThreshold`), longer than the 15-minute drain before
  a redeploy, so it does not end a run the next session is about to pick up.
  Every run stopped for a redeploy in this window was picked up by the session
  that came back.
- **A scheduler wait left set**: ruled out. The usage-limit pause lifted at
  20:00 on October 10 and the intake brake at 01:39 on October 11, both on their
  own. The watch session restarted onto a new build twenty-nine times from
  October 10 to 13, each after a drain of up to 15 minutes in which nothing new
  is pulled — several hours in all, and a real but smaller cost.
- **The leftover assignee** (yoyodyne-819): ruled out. The re-run fix carried
  an assignee from an earlier claim and was pulled at 03:08 on October 13; the
  publications fix carried none and waited all the same. As
  [the assignee diagnosis](yoyodyne-ifd-433-35-assignees-left-by-claims.md)
  found, the ready listing does not read the assignee.
- **Slot 2's label preference**: ruled out for priority-0 work. Since
  yoyodyne-j2u (October 9) a preferring slot takes ready priority-0 work ahead of
  its label (`outranksPreference`), and every priority-0 pull since says so. It
  did reorder priority-1 work, which is what it is for.

## What this change fixes

A decision about preserved work is now outranked by ready priority-0 work where
its own item is below priority 0, the same rule a slot's label preference
follows. It is held back while such work stands ready, and the item's record
says which priority-0 items went first. It is fired as soon as the walk of the
order is past priority 0, so it still goes ahead of every fresh pull at its own
priority and below. Preserved work on a priority-0 item, and preserved work when
nothing at priority 0 is ready, still takes the first free slot, as before. The
code is the `outranked` check in `Scheduler.Schedule` and `outrankedCarryOut` in
`internal/orchestrator/schedule.go`. The test
`TestAReadyPriorityZeroItemTakesTheFreeSlotAheadOfLowerPriorityPreservedWork`
replays the 00:47 case: a ready priority-0 item and a free slot start that
item, and the preserved repair's item records the refusal and its reason.

## The fix the lost races need

This is larger than this run and is named here for admission. A run whose
change keeps losing the race to main holds a developer slot for as long as main
keeps moving. Each replay costs its checks and a review, about fifty minutes
under this machine's load, and main moved about that often on October 13. So a
run can lose many times in a row, and with three slots, two such runs halve the
factory's throughput. The fix has to change one of two things:

- **Stop a replay from losing again.** Prepare the replay against a main that
  cannot move under it, for example by reserving the next landing on the target
  branch for a run once its replay starts, so that its checks and review
  finish against the main it will land on. This touches the single-promotion
  lease (the one-promotion-per-target-branch invariant) and how the forge's
  merges are ordered.
- **Release the slot after a bounded number of lost races**, keeping the work,
  so ready priority-0 work can start. The run would then wait for a slot like
  any decided repair of preserved work, which after this change goes behind
  ready priority-0 work. The bound and the way the run comes back need a design:
  each replay must still be checked and reviewed on its own revision (the
  integration-requires-revision-bound-evidence invariant), so releasing the slot
  saves slot time only where the run is not simply replayed again at once.

The Lead Product Manager asked for a run that overruns its checks to release
its slot with its work kept. No check stage overran in the case that prompted
it, so the ask belongs to this second option rather than to the check limits.
