---
id: heavy-check-capacity
kind: design
title: 'Capacity for heavy checks: one count per machine home, durable waiting, and recovery'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-13T13:00:28.052475Z
      reason: 'yoyodyne-ifd.429.43: the first copy (document-779.1) was refused by the word check for two words I wrote, a joined name for the measurement build and a figurative word for a run never granted a permit. This copy writes both in plain words, states that a run holds one permit at a time, and records the alternatives rejected. The rules are unchanged from the ruling already recorded on the permits build (yoyodyne-ifd.429.47) and the measurement build (yoyodyne-ifd.429.48).'
    - action: amended
      by: architect
      at: 2026-10-14T21:50:12.195604Z
      reason: 'yoyodyne-ifd.437.51: the configuration example named the heavy checks race and test, which the configuration loader refuses because they match no declared check. The example now names them make race and make test, as this repository declares them, quoted because each contains a space; the setting section says in one sentence that the keys are the declared check commands written exactly as the project declares them; the reviewer''s first check uses the same name. No rule changes.'
    - action: amended
      by: architect
      at: 2026-10-27T01:23:53.921609Z
      reason: Brings the capacity design into line with the guarantees design. Permits are taken through the same reservation and lock as developer slots, as a separate count, keeping their own record and lock until migration item 6 lands. Only the runner in internal/checks asks for a permit. No lock is held while waiting. A waiting run keeps its slot, so a slot and a permit are never asked for in the same act. Measured limits apply on every path through the runner. The full race check on main is in the third group. The states, the release rule, the order of granting and recovery are unchanged. Written from a read at main 6640e265.
    - action: amended
      by: architect
      at: 2026-11-16T18:21:24.636498Z
      reason: 'yoyodyne-naoj and yoyodyne-ifd.429.195: while the merge queue holds an entry that needs a heavy check, one place for that check, and one place of the total, is kept free for the landing check, except where the check''s count is one. A new setting caps heavy checks of all kinds at once, defaulting to the sum of the counts. This repository is set to two race checks, two test checks and three in total, so ordinary race checks stop waiting hours for one place. Written from a read at main 279b89fc; every other rule is unchanged.'
    - action: amended
      by: architect
      at: 2026-11-22T06:29:48.28271Z
      reason: 'Brings the capacity design into line with the code read at main 848d86190: the total, the shared store and the long wait listing are marked not built; the kept place is read from the merge queue before the permits lock, which is now the rule, with the reason; the narrowed race check on the GitHub path is named; release is decided from the inherited lock file; keys may match any declared command. One build item added for the long wait listing. Written from a read at main cf4fb292a833.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-13T13:00:28.052475Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 797, for document-797.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-14T21:50:12.195604Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 964, for document-964.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-10-27T01:23:53.921609Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1825, for document-1825.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 3
      by: harness
      at: 2026-11-16T18:21:24.636498Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2901, for document-2901.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 4
      by: harness
      at: 2026-11-22T06:29:48.28271Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3415, for document-3415.1 under the automatic approval policy
---
# Capacity for heavy checks: one count per machine home, durable waiting, and recovery

Some of a project's declared checks are heavy: in this repository `make race` and `make test` each take most of the machine for many minutes. When several developer runs reach their checks at once, those checks run side by side, each takes longer than its time limit allows, and runs stop for reasons that have nothing to do with their work. This design bounds how many of each heavy check, and how many heavy checks in all, run at once on one machine, separately from how many developer runs there are. It says how a run waits, how a place is kept free for a landing check, how a place is released, and how all of it survives a restart.

It serves the v1 goal to isolate implementation tasks in harness-managed Git worktrees and integrate successful work automatically, and the goal to run development nearly autonomously: a check stopped by load judges nothing and costs a person's attention to recover, and a landing check stuck behind ordinary checks holds up every approved change behind it.

It applies two guarantees of [the guarantees design](one-owner-for-each-guarantee.md):

- permits are to be taken through the same reservation as developer slots (guarantee 3);
- every heavy check is launched by the runner in `internal/checks` (guarantee 9).

## What is built today

This section says plainly which parts of this design the code has and which it does not, so that no later section reads as shipped when it is not. It was checked against the code at main 848d86190 on 2026-11-21.

Built:

- the count for each heavy check under `execution.heavy_checks`, its validation, and the four states of a permit;
- the permits' own record and lock under the state root;
- the release rule, decided from a lock file every process of the check inherits;
- the three groups in the order of granting, and the order of asking within each;
- the place kept for a landing check in each check's own count, with no place kept where the count is one, worked out from the merge queue's record on every poll;
- time limits measured from launch, and the limit on reruns;
- recovery after a restart, and the waiting state on the dashboard and in `yoyo status`.

Not built:

- `execution.heavy_checks_total` and the place kept in the total;
- permits held in the reservation store shared with developer slots (migration item 6 of the guarantees design);
- listing a long wait as a problem;
- this repository's counts of two race checks, two test checks and three in all.

## The setting

`execution.heavy_checks` names a declared command and how many copies of it may run at once on this machine home. Each key is a command exactly as the project declares it, so in this repository the keys are `make race` and `make test`, not shortened names. A key may match a command the project declares among its checks, landing checks, document checks, checks on main, intent checks, path checks or install steps.

`execution.heavy_checks_total` is how many heavy checks, of any kinds together, may run at once on this machine home. When it is not set, it is the sum of the counts under `execution.heavy_checks`, which allows exactly what those counts allowed before this setting existed. This setting is not built yet.

Once the total is built, the development manager sets this repository's configuration to:

```yaml
execution:
  heavy_checks:
    "make race": 2
    "make test": 2
  heavy_checks_total: 3
```

Two race places mean an ordinary race check no longer waits hours behind one other race check. A total of three means the machine never runs four heavy checks at once, which is what overloaded it before. This repository's configuration does not carry these values today, and must not until the total is built: two race places and two test places with no total would allow the overload this design exists to prevent. The measurement build may later propose different numbers.

The keys are quoted because each contains a space; quoting keeps them unambiguous whatever characters a project's commands use. A command not named there is not heavy and runs as it does today. Each count must be a whole number of at least one, and each key must match a declared command, character for character. The total must be a whole number of at least one and no more than the sum of the counts. Anything else is refused when the configuration is read, and the refusal names the setting. The settings are the development manager's to change, under section 6 of the portable agent configuration design, through the reviewed path. They are separate from developer slots: changing one never changes the other, and a check waiting for capacity does not count as an idle slot or a busy one.

## Where permits are counted

Today permits are kept as they were built: one record per machine home under the state root, read and written only while holding a file lock beside it. Every harness process on the machine reads the same record, so two processes can never each believe they hold the last place.

Once migration item 6 of the guarantees design lands, permits move into the reservation store owned by `internal/runstate`, read and written only under the shared reservation lock, as a count of their own beside the slots, and their own record and lock are removed. Until then nothing in this design depends on the shared store.

There is a limit for each heavy check and, once built, a limit for all heavy checks together. A permit is granted only when its own check's count, and the total, have a free place. Taking a permit never takes or frees a slot, and taking a slot never takes or frees a permit.

Each entry is one permit: the check's name, the run and work item it belongs to, its state, the process group the check was launched in, and when it entered each state.

A permit is in one of four states:

- **waiting**: asked for, not yet granted;
- **running**: granted, and the check is launched in its own process group;
- **exited**: the check's main process has ended, and the harness has not yet confirmed that none of its processes remain;
- **released**: the place is free. Released entries are removed when the record is next written.

A running or exited permit counts against its check's count and the total.

## Asking, holding and releasing

Only the runner in `internal/checks` asks for a permit, just before it launches a heavy check. No other code launches a configured check or takes a permit.

Granting a permit takes the lock only for the act itself: checking that a place is free and recording it as taken by the named run. No run holds the lock while it waits. So the lock never stands between a landing lease and a permit, and a waiting run never holds up a slot reservation.

A run holds at most one permit at a time. A check never asks for a second permit while it holds one, so no run can wait on itself and no two runs can each hold what the other needs.

A run waiting for a permit keeps the developer slot it already holds, so a slot and a permit are never asked for in the same act. While it waits, the run is shown as waiting for a permit, not as idle.

Each permit has a lock file of its own, opened when the check launches and inherited by every process the check starts. A permit is released only when that lock file can be taken, which shows that no process of the check is left. The release is written in the same act as that confirmation. The main process ending is not enough. A check whose child process outlives the main process keeps its permit, and the run is shown as a problem naming the check, the run and the process group, because those processes are still using the machine.

A run that is cancelled, stopped, or fails releases its permit by the same rule. Success, failure and interruption are treated alike. A waiting entry whose run has stopped asking for six hours is released, so a run that ended without withdrawing its request holds no place in the order.

## Waiting

A run waiting for a permit shows the waiting state on the run, with the time waited, on the dashboard and in `yoyo status`.

A wait longer than the greater of one hour and the check's time limit is to be listed under problems with the runs holding the permits, and the run keeps its place. This listing is not built: today only a kept permit, whose check left processes behind, is listed as a problem.

Permits are granted in this order:

1. checks for a landing or promotion, which already hold the target branch's landing lease, including the checks the harness's merge queue runs on a candidate;
2. checks for a decided repair or rerun holding preserved work;
3. every other check, in the order the permits were asked for. This includes the race check the harness runs on main every four hours.

Within each group the order is the order of asking, so a run that asked first is never passed over by a later run of the same group.

A landing run takes the landing lease first and its permit second, never the other way round, so no two runs can each hold one and wait for the other.

## Keeping a place for landing checks

The order of granting puts a landing check first among those waiting, but it cannot help a landing check that arrives when every place is already running ordinary checks. That landing check then waits for a whole ordinary check to finish, and every approved change behind it in the merge queue waits too. So one place is kept free for it ahead of time.

While the harness's merge queue holds an entry whose landing checks include a heavy check that has not yet been granted a permit, checks in groups 2 and 3 are granted:

- at most one fewer permit for that check than its count; and
- once the total is built, at most one fewer heavy checks in all than the total.

Checks in group 1 are granted up to the full count and the full total.

The harness reads the merge queue's record on every poll for a permit, before it takes the permits lock, and works out from it whether a place is kept. Nothing extra is stored, and nothing is left behind when the entry lands, is handed back or is withdrawn. The queue's record is never read while the permits lock is held, so the permits lock and the merge queue's own lock are never taken in opposite orders by two processes. The cost of reading first is that the queue may change between the read and the grant. At worst one ordinary check is granted the place in that moment; the rule below, that the next place released goes to the landing check, then puts it right. A place kept for an entry that has just left the queue is given up at the next poll.

Where a check's count is one, no place is kept for that check: keeping its only place would stop every ordinary copy of it whenever the queue held an entry. The landing check is still granted first among those waiting. Where the total is one, no place is kept in the total, for the same reason.

A rerun of a landing check under guarantee 9 keeps the entry's claim: the place stays kept until the entry's heavy checks have all been granted or the entry leaves the queue.

No running check is ever stopped to free the kept place. If the place is already taken when an entry joins the queue, the next place that is released goes to the landing check, and no ordinary check is granted in the meantime.

The cost is stated so it can be weighed. While the queue holds such an entry and its landing check has not started, one fewer ordinary copy of that check, and one fewer ordinary heavy check in all, can run. With the numbers this repository will use once the total is built, ordinary work can run one race check and two heavy checks in all during that time, instead of two and three. When the queue is empty, nothing is kept and the full counts apply.

When checks run on GitHub, a merge queue candidate runs the short local check, and also the narrowed race check (`merge_queue_race_check`) where one is owed and no check GitHub requires is a race check. A place is kept only for whichever of these the project declares heavy. In this repository GitHub requires the race check, so a candidate runs no narrowed race check here, and the short local check is not declared heavy, so no place is kept for such an entry.

## Time limits

The runner applies a check's time limit. It starts when the check launches with its permit, never while it waits, and scaling it for machine load still applies at launch.

A check stopped by its limit is unfinished, not failed, under the recovery design. Whether it runs again is decided under guarantee 9: at most two reruns within three times its limit, each taking its own permit like any check.

## Recovery after a restart

When the harness starts, it reads the permits before granting anything, under the same lock and from the same record as every grant:

- a running or exited permit with a process of its check still alive is kept, and the run that owns it is reattached as any preserved run is;
- a running or exited permit with no process of its check left is released, and the run is recovered as a check stopped from outside the work, spending no repair attempt;
- waiting permits keep their place in the order.

Whether a place is kept for a landing check is worked out again from the merge queue's record at the first poll, as at every poll.

A restart never resets a count, never grants a permit twice, and never loses a waiting run's place.

## What the count covers, and what it does not

The count covers heavy checks the harness itself launches through the runner, because only those run in a process group the harness started and can watch. A full suite a developer chooses to run inside its own session is outside it. The developer contract keeps asking for the declared checks to be run by the harness, and the measurement build reports such runs from machine load and process listings, rather than this design claiming to count them.

When checks run on GitHub, the full suite runs there and takes no permit. Only the checks that still run here take one, and only where the project declares them heavy. A fallback to local checks takes permits like any local check. A document landing's declared document checks take a permit only if one of them is declared heavy.

## What the measurement build measures

The numbers are the measurement build's (yoyodyne-ifd.429.48), not this design's. For each heavy check it records the time waited for a permit, the time run, the machine load and free memory at launch, and the outcome: passed, failed, or stopped by its limit. For the machine it records load against the number of heavy checks running. For landing checks it also records the time waited for a permit, so the effect of the kept place can be seen.

From that it proposes counts and a total that keep checks finishing inside their time limits. The development manager applies them under section 6, and they then apply on every execution path, because every path launches heavy checks through the runner (migration item 20 of the guarantees design). Its measurements are taken on a machine that is not already heavily loaded, because limits set from an overloaded machine would rest on misleading numbers. Until then the values above stand.

## Alternatives rejected

- **Counting checks within developer slots.** Rejected because it ties two different limits together: lowering slots to protect checks leaves runs idle that could be writing code, and raising them brings the overload back. Permits are to share the slots' reservation and lock, never their count.
- **Keeping the permits' own lock for good, beside the slot reservation.** Rejected once the guarantees design gave every start one reservation. Two locks over two counts on one machine is the kind of copied rule that design ends, and a later path could take one and forget the other.
- **A count held in one process's memory.** Rejected because two harness processes on one machine would each grant places, and a restart would forget who holds what.
- **An operating system semaphore.** Rejected because it does not survive a restart with its holders known, and cannot show a person which run is waiting and for how long.
- **Short names for checks, such as `race` for `make race`.** Rejected because they would need a second list mapping each short name to a command, kept beside the checks the project already declares, and could drift from it.
- **Raising the race count to two with no total.** Rejected because two race checks and two test checks could then run at once, which is the overload this design exists to prevent.
- **A place used only by landing checks at all times.** Rejected because it would sit empty whenever the merge queue is empty, which is most of the time, while ordinary checks wait.
- **Stopping a running ordinary check to make room for a landing check.** Rejected because it throws away work already paid for, and a stopped check has to run again from the start.
- **Relying on the order of granting alone.** Rejected because order helps only among checks already waiting; it cannot free a place that ordinary checks took before the landing check asked.
- **Reading the merge queue's record while holding the permits lock.** Rejected because it puts one lock inside another, and any path that takes them the other way round could leave two processes each waiting for the other. Reading first costs at most one ordinary grant into the kept place, which the next release puts right.

## Builds

- **Permits with durable waiting and recovery** (yoyodyne-ifd.429.47): built, apart from the long wait listing below.
- **Permits taken through the shared reservation** (migration item 6 of the guarantees design): the permits move into the reservation store under the shared reservation lock, as a separate count. Their own record and lock are then removed. Not built.
- **Heavy checks launched only by the runner** (migration item 16 of the guarantees design): the path that runs heavy checks moves onto the runner in `internal/checks`.
- **The measurement build** (yoyodyne-ifd.429.48): the measurements above and the proposed limits, applied on every path once migration item 20 has landed.
- **The total across heavy checks** (yoyodyne-ifd.429.227): `execution.heavy_checks_total`, its default and validation, and granting a permit only when both its check's count and the total have room. Not built.
- **The place kept for landing checks** (yoyodyne-ifd.429.193): the half for each check's own count is built; the half for the total depends on the total build.
- **Listing a long wait as a problem**, to be admitted by the development manager: a wait longer than the greater of one hour and the check's time limit is listed under problems with the runs holding the permits. Not built.
- **This repository's configuration** (yoyodyne-ifd.429.228): the development manager sets `make race` to 2 and the total to 3 through the reviewed path, after the total build has landed.

## How a reviewer checks an implementation

- With `"make race": 1` declared, two runs reaching `make race` at once run it one after the other, and the second shows as waiting.
- A key that matches no declared command, such as `race` where the project declares `make race`, is refused when the configuration is read, and the refusal names the key.
- A total of zero, or more than the sum of the counts, is refused when the configuration is read; a missing total behaves as the sum of the counts.
- With counts of two and two and a total of three, a fourth heavy check waits even though its own check has a free place.
- With `"make race": 2` and a merge queue entry that needs `make race` and has not started it, an ordinary race check is not granted the second place, and the landing check is granted it when it asks.
- With `"make race": 1` and the same entry waiting, an ordinary race check is still granted the place when it is free and nothing in group 1 is waiting.
- When the entry lands, is handed back or is withdrawn, the kept place is given to the next ordinary check without anything being cleared by hand.
- The merge queue's record is read before the permits lock is taken, never while it is held.
- With checks on GitHub and the race check required there, a candidate keeps no place; where no required check is a race check and the narrowed race check is declared heavy, a place is kept for it.
- No running check is stopped when an entry joins the queue.
- A check's time limit is measured from its launch, not from when it asked for a permit.
- A check whose child process survives the main process keeps its permit and shows as a problem.
- A wait longer than the greater of one hour and the check's time limit is listed as a problem, and the run keeps its place.
- A landing run waiting for a permit already holds the landing lease, and is granted before a development run that asked earlier.
- A run never holds two permits at once.
- After a restart with a check still running, its permit is kept and no second copy of the check starts.
- Once permits are in the reservation, a test has two processes race a permit and a slot reservation at the same time, and each count ends right.
- No run holds the lock while it waits for a permit.
- A run waiting for a permit keeps its slot and is not shown as idle.
- No code outside `internal/checks` takes a permit or launches a heavy check.
- A project with no heavy checks declared behaves exactly as before.
