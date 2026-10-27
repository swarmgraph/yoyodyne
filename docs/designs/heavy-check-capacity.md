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
---
# Capacity for heavy checks: one count per machine home, durable waiting, and recovery

Some of a project's declared checks are heavy: in this repository `make race` and `make test` each take most of the machine for many minutes. When several developer runs reach their checks at once, those checks run side by side, each takes longer than its time limit allows, and runs stop for reasons that have nothing to do with their work. This design bounds how many of each heavy check run at once on one machine, separately from how many developer runs there are, and says how a run waits, how its place is released, and how all of it survives a restart.

It serves the v1 goal to isolate implementation tasks in harness-managed Git worktrees and integrate successful work automatically, and the goal to run development nearly autonomously: a check stopped by load judges nothing and costs a person's attention to recover.

It applies two guarantees of [the guarantees design](one-owner-for-each-guarantee.md):

- permits are taken through the same reservation as developer slots (guarantee 3);
- every heavy check is launched by the runner in `internal/checks` (guarantee 9).

Nothing else about permits changed with them. The four states of a permit, the release rule, the order of granting and the recovery after a restart are as they were.

## The setting

`execution.heavy_checks` names a declared check and how many copies of it may run at once on this machine home. Each key is a check command exactly as the project declares it, so in this repository the keys are `make race` and `make test`, not shortened names. This repository declares:

```yaml
execution:
  heavy_checks:
    "make race": 1
    "make test": 2
```

The keys are quoted because each contains a space; quoting keeps them unambiguous whatever characters a project's check commands use. A check not named there is not heavy and runs as it does today. Each count must be a whole number of at least one, and each key must match a check the project declares, character for character; anything else is refused when the configuration is read. The setting is the development manager's to change, under section 6 of the portable agent configuration design, through the reviewed path. It is separate from developer slots: changing one never changes the other, and a check waiting for capacity does not count as an idle slot or a busy one.

## Where permits are counted

Permits are counted in the same reservation store as developer slots, owned by `internal/runstate` and read and written only under the shared reservation lock. Every harness process on the machine reads the same count, so two processes can never each believe they hold the last place.

Permits are a count of their own beside the slots, with a limit for each heavy check. Taking a permit never takes or frees a slot, and taking a slot never takes or frees a permit.

Each entry is one permit: the check's name, the run and work item it belongs to, its state, the process group the check was launched in, and when it entered each state.

Until permits move into the reservation (migration item 6 of the guarantees design), they are kept as they were built: one record per machine home under the state root, read and written only while holding a file lock beside it, with the same entries and states.

A permit is in one of four states:

- **waiting**: asked for, not yet granted;
- **running**: granted, and the check is launched in its own process group;
- **exited**: the check's main process has ended, and the harness has not yet confirmed its process group is empty;
- **released**: the place is free. Released entries are removed when the count is next written.

## Asking, holding and releasing

Only the runner in `internal/checks` asks for a permit, just before it launches a heavy check. No other code launches a configured check or takes a permit.

Granting a permit takes the reservation lock only for the act itself: checking that a place is free and recording it as taken by the named run. No run holds the lock while it waits. So the lock never stands between a landing lease and a permit, and a waiting run never holds up a slot reservation.

A run holds at most one permit at a time. A check never asks for a second permit while it holds one, so no run can wait on itself and no two runs can each hold what the other needs.

A run waiting for a permit keeps the developer slot it already holds, so a slot and a permit are never asked for in the same act. While it waits, the run is shown as waiting for a permit, not as idle.

A permit is released only when the check's process group is confirmed to hold no processes. The release is written through the reservation in the same act as that confirmation. The main process ending is not enough. A check whose child process outlives the main process keeps its permit, and the run is shown as a problem naming the check, the run and the process group, because those processes are still using the machine.

A run that is cancelled, stopped, or fails releases its permit by the same rule. Success, failure and interruption are treated alike.

## Waiting

A run waiting for a permit shows the waiting state on the run, with the time waited, on the dashboard and in `yoyo status`. A wait longer than the greater of one hour and the check's time limit is listed under problems with the runs holding the permits, and the run keeps its place.

Permits are granted in this order:

1. checks for a landing or promotion, which already hold the target branch's landing lease;
2. checks for a decided repair or rerun holding preserved work;
3. every other check, in the order the permits were asked for. This includes the full race check the harness runs on main every four hours, under point 4 of how this repository applies guarantee 9.

Within each group the order is the order of asking, so a run that asked first is never passed over by a later run of the same group.

A landing run takes the landing lease first and its permit second, never the other way round, so no two runs can each hold one and wait for the other.

## Time limits

The runner applies a check's time limit. It starts when the check launches with its permit, never while it waits, and scaling it for machine load still applies at launch.

A check stopped by its limit is unfinished, not failed, under the recovery design. Whether it runs again is the checks plan's decision under guarantee 9: at most two reruns within three times its limit, each taking its own permit like any check.

## Recovery after a restart

When the harness starts, it reads the permits before granting anything, under the same lock and from the same store as the developer slots:

- a running or exited permit whose process group still has processes is kept, and the run that owns it is reattached as any preserved run is;
- a running or exited permit whose process group is confirmed gone is released, and the run is recovered as a check stopped from outside the work, spending no repair attempt;
- waiting permits keep their place in the order.

A restart never resets a count, never grants a permit twice, and never loses a waiting run's place.

## What the count covers, and what it does not

The count covers heavy checks the harness itself launches through the runner, because only those run in a process group the harness started and can watch. A full suite a developer chooses to run inside its own session is outside it. The developer contract keeps asking for the declared checks to be run by the harness, and the measurement build reports such runs from machine load and process listings, rather than this design claiming to count them.

In CI mode (checks on CI) the full suite runs on the forge and takes no permit. Only the short local check runs here, and it is not heavy unless the project declares it so. A fallback to local checks takes permits like any local check. A document landing's declared document checks take a permit only if one of them is declared heavy.

## What the measurement build measures

The numbers are the measurement build's (yoyodyne-ifd.429.48), not this design's. For each heavy check it records the time waited for a permit, the time run, the machine load and free memory at launch, and the outcome: passed, failed, or stopped by its limit. For the machine it records load against the number of heavy checks running.

From that it proposes limits that keep checks finishing inside their time limits. The development manager applies them under section 6, and they then apply on every execution path, because every path launches heavy checks through the runner (migration item 20 of the guarantees design). Its measurements are taken on a machine that is not already heavily loaded, because limits set from an overloaded machine would rest on misleading numbers. Until then the defaults above stand.

## Alternatives rejected

- **Counting checks within developer slots.** Rejected because it ties two different limits together: lowering slots to protect checks leaves runs idle that could be writing code, and raising them brings the overload back. Permits share the slots' reservation and lock, never their count.
- **Keeping the permits' own lock for good, beside the slot reservation.** Rejected once the guarantees design gave every start one reservation. Two locks over two counts on one machine is the kind of copied rule that design ends, and a later path could take one and forget the other.
- **A count held in one process's memory.** Rejected because two harness processes on one machine would each grant places, and a restart would forget who holds what.
- **An operating system semaphore.** Rejected because it does not survive a restart with its holders known, and cannot show a person which run is waiting and for how long.
- **Short names for checks, such as `race` for `make race`.** Rejected because they would need a second list mapping each short name to a command, kept beside the checks the project already declares, and could drift from it.

## Builds

- **Permits with durable waiting and recovery** (yoyodyne-ifd.429.47): the setting and its validation, the record and its lock, the four states, the release rule with process group confirmation, the waiting order, time limits from launch, recovery after a restart, and the waiting state on the dashboard and in `yoyo status`.
- **Permits taken through the shared reservation** (migration item 6 of the guarantees design): the permits move into the reservation store under the shared reservation lock, as a separate count. Their own record and lock are then removed.
- **Heavy checks launched only by the runner** (migration item 16 of the guarantees design): the path that runs heavy checks moves onto the runner in `internal/checks`.
- **The measurement build** (yoyodyne-ifd.429.48): the measurements above and the proposed limits, applied on every path once migration item 20 has landed.

## How a reviewer checks an implementation

- With `"make race": 1` declared, two runs reaching `make race` at once run it one after the other, and the second shows as waiting.
- A key that matches no declared check command, such as `race` where the project declares `make race`, is refused when the configuration is read, and the refusal names the key.
- A check's time limit is measured from its launch, not from when it asked for a permit.
- A check whose child process survives the main process keeps its permit and shows as a problem.
- A landing run waiting for a permit already holds the landing lease, and is granted before a development run that asked earlier.
- A run never holds two permits at once.
- After a restart with a check still running, its permit is kept and no second copy of the check starts.
- Once permits are in the reservation, a test has two processes race a permit and a slot reservation at the same time, and each count ends right.
- No run holds the reservation lock while it waits for a permit.
- A run waiting for a permit keeps its slot and is not shown as idle.
- No code outside `internal/checks` takes a permit or launches a heavy check.
- A project with no heavy checks declared behaves exactly as before.
