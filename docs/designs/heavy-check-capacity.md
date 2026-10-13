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
      at: 2026-10-13T11:28:49.928797Z
      reason: 'yoyodyne-ifd.429.43 - capacity for heavy checks designed whole at Mason''s direction of October 13: one count per machine home with sharing across homes deferred, defaults of one make race and two make test until the measured-limits build sets numbers, time limits from launch, release only on proven cleanup, landing lease before permit, durable waiting and recovery after restart'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-13T11:28:49.928797Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 779, for document-779.1 under the automatic approval policy
---
# Capacity for heavy checks: one count per machine home, durable waiting, and recovery

## What this is for

Every developer run, document landing and promotion runs the project's checks on the same machine, and nothing counts how many heavy ones run at once. On October 13 a chat item with one line of work took nearly five hours, almost all of it full test suites competing on a machine at load 20 to 27, and five of seven document runs between October 7 and 10 timed out at load 13 to 20 on 16 cores. Scaling a check's time limit for load only lets an overloaded machine run longer. This design bounds the load instead: a heavy check runs only when it holds a permit from one count kept for the machine home, and waits for one otherwise. It serves the autonomy goal: a run should end for a reason about the work, never because its own checks were starved by everyone else's.

## What counts as heavy

The project declares its heavy checks and how many of each may run at once:

```yaml
execution:
  heavy_checks:
    race: 1
    test: 2
```

Each key is the name of a check the project already declares; each value is the most of that check that may run at once across the machine home. A declared heavy check with no number gets 1. A check not listed is not heavy and runs as it does today. A project that declares no heavy checks gets exactly today's behaviour.

The development manager sets these values under section 6 of portable agent configuration, through the reviewed path. For this repository the values the permits build ships with are the ones above: at most one `make race` and two `make test` at a time. They are deliberately conservative; the measured-limits build (yoyodyne-ifd.429.48) replaces them with numbers from measurement.

## Scope: one machine home

The count is kept in the machine home's state root and covers every heavy check the harness launches from that home, for every project and run in it: developer runs, repair runs, document landings, and the checks before promotion. Sharing one count between two machine homes on the same machine is a later extension, recorded here and not built. Nothing about safety needs it now: the machine home design already refuses a second home on the same machine, and the count governs load, never correctness.

## The record

One capacity record under the machine home's state root, changed only under a file lock, holds for each heavy check name its limit and every permit held or asked for. A permit records the run, the item, the check, the candidate commit, when it was asked for, when it was granted, the process group the check runs in, and its state.

A permit moves through four states, each recorded before the step it names:

- **waiting**: asked for and not yet granted;
- **running**: granted, and the check launched in its own process group;
- **exited**: the check's main process has ended with its result recorded;
- **released**: every process in the check's process group is confirmed gone, and the permit's place is free.

A permit is released only from exited, and only once the whole process group is confirmed gone. A check whose processes cannot be confirmed gone keeps its permit and shows as a broken factory problem naming the check and the run, so a leaked test process can never let the machine run more heavy checks than the count allows. The development manager may release such a permit by hand once the processes are dealt with; that act is recorded.

## Waiting

A run that needs a heavy check asks for a permit and waits. While it waits it keeps its developer slot, because the run is part way through its work. Waiting is shown as its own state on the run, with the time waited, on the dashboard and in `yoyo status`.

Permits are granted in this order:

1. checks for a landing or promotion, which already hold the target branch's landing lease;
2. checks for a decided repair or rerun holding preserved work;
3. every other check, in the order the permits were asked for.

A landing run takes the landing lease first and its permit second, never the other way round, so no two runs can each hold one and wait for the other.

## Time limits

A check's time limit starts when the check launches with its permit, never while it waits. Scaling a check's limit for machine load still applies at launch. A check stopped by its limit is unfinished, not failed, under the recovery design: its reruns, at most two within three times its limit, each take a permit like any check.

## Recovery after a restart

When the harness starts, it reads the capacity record before granting anything:

- a running or exited permit whose process group still has processes is kept, and the run that owns it is reattached as any preserved run is;
- a running or exited permit whose process group is confirmed gone is released, and the run is recovered as a check stopped from outside the work, spending no repair attempt;
- waiting permits keep their place in the order.

A restart never resets a count, never grants a permit twice, and never loses a waiting run's place.

## What the count covers, and what it does not

The count covers heavy checks the harness itself launches, because only those run in a process group the harness started and can watch. A full suite a developer chooses to run inside its own session is outside it; the developer contract keeps asking for the declared checks to be run by the harness, and the measured-limits build reports such runs, from machine load and process listings, rather than this design claiming to count them.

In CI mode (checks on CI) the full suite runs on the forge and takes no permit; only the short local check runs here, and it is not heavy unless the project declares it so. A fallback to local checks takes permits like any local check. A document landing's declared document checks take a permit only if one of them is declared heavy.

## What the measured-limits build measures

The numbers are the measured-limits build's (yoyodyne-ifd.429.48), not this design's. For each heavy check it records the time waited for a permit, the time run, the machine load and free memory at launch, and the outcome; and for the machine, load against the number of heavy checks running. From that it proposes limits, which the development manager applies under section 6. Until then the defaults above stand.

## Builds

- **Permits with durable waiting and recovery** (yoyodyne-ifd.429.47): the setting and its validation, the capacity record and its lock, the four states, the release rule with process group confirmation, the waiting order, time limits from launch, recovery after a restart, and the waiting state on the dashboard and in `yoyo status`.
- **Measured limits** (yoyodyne-ifd.429.48): the measurements above and the proposed limits.

## How a reviewer checks an implementation

- With `race: 1` declared, two runs reaching `make race` at once run it one after the other, and the second shows as waiting.
- A check's time limit is measured from its launch, not from when it asked for a permit.
- A check whose child process survives the main process keeps its permit and shows as a broken problem.
- A landing run waiting for a permit already holds the landing lease, and is granted before a development run that asked earlier.
- After a restart with a check still running, its permit is kept and no second copy of the check starts.
- A project with no heavy checks declared behaves exactly as before.
