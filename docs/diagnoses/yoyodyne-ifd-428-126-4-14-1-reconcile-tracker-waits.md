# yoyodyne-ifd.428.126.4.14.1: why the scheduled reconcile runs out of time

The maintenance pass runs `yoyo reconcile` and stops it after fifteen minutes.
From November 1 to the morning of November 4 it was stopped 41 times out of
469 passes. The investigation of those timeouts
(yoyodyne-ifd.428.126.4.14) guessed that reconcile lists every work item and
that this listing runs past its limit once the tracker's store is over 1 GB.
This item was to time each tracker read reconcile makes on a copy of the store
before changing anything.

The timings say the listing is not the cause. Every read answers in seconds on
the copy. What is slow is waiting for the store: bd lets one process into it at
a time, and on the live store reconcile's reads wait behind every other bd
process on the machine.

All times are Pacific (PST) unless marked Z, which is UTC.

## The tracker reads reconcile makes

Traced from `reconcileRuns` in `internal/cli/reconcile.go`:

| Read | When it is made |
| --- | --- |
| `bd list --status=closed`, one listing shared by two steps | every pass with a standing docket entry or a run recorded as merged |
| `bd export` | when the tracker's export is due a refresh |
| `bd ready` | only when the stall check cannot otherwise explain a quiet line |
| `bd list --all` | only when an exchange between roles runs out of rounds |
| `bd list --status=open`, `in_progress`, `blocked` | only when a queued merge's checks are red on its target |
| `bd show <id> <id> …`, every outstanding run's item in one call | every pass with an outstanding run (since the change below) |
| `bd show <id>`, one item at a time | for each person merge, red target, escalation, and finding the pass settles, and for a run's item read again or after a write |

Nothing else reconcile does lists the tracker. The runs a pass hosts after its
report, continued developer attempts, make their own reads like any run.

## How long each read takes on a copy of the store

At 05:16 on November 4 the live store under `.beads/embeddeddolt` was 1.1 GB
and held 1,601 work items, 1,234 of them closed. It was copied while nothing
wrote to it: every file's size and modification time were the same after the
3-second copy as before. Each read below was run once with bd pointed at the
copy, with the machine's load average at about 14 on 16 cores:

| Read | Took | Items |
| --- | --- | --- |
| `bd show yoyodyne-ifd.428.126.4.14.1 --json` | 0.6s | 1 |
| `bd show yoyodyne-ifd.428.126.4.14 --json` | 1.1s | 1 |
| `bd list --json --limit=0 --status=closed` | 1.7s | 1,234 |
| `bd list --json --limit=0 --all` | 1.5s | 1,601 |
| `bd list --json --limit=0` (not closed) | 0.6s | 367 |
| `bd list --json --limit=0 --status=open` | 0.5s to 1.3s | |
| `bd list --json --limit=0 --status=in_progress` | 0.8s | |
| `bd list --json --limit=0 --status=blocked` | 0.5s | |
| `bd list --json --limit=0 --status=open,in_progress,blocked` | 0.9s | 367 |
| `bd ready --json --limit=0` | 0.5s | 297 |
| `bd export` | 6.3s | |

The tracker's limit for each call is two minutes. None of these came near it:
the slowest, the full export, took about a twentieth of it.

## What makes them slow on the live store

Eight listings started together on the copy, with one read of a single item
started 0.3 seconds after them, finished one after another at 0.7, 1.0, 1.6,
2.2, 2.7, 3.3, 4.0 and 6.3 seconds; the single read, which takes 0.6 seconds
alone, finished last at 10.1 seconds. bd takes one exclusive lock on the store
for every command, reads included, so each command waits for the ones holding
it, and a command that arrived earlier is not served first.

The live store has many more processes asking: the dashboard, Slack, the
scheduler, every developer run and conversation. The tracker client keeps a
record of each call stopped at its limit (`tracker-slow-calls` in the
product's state). Of the 1,000 calls it held from November 1 to November 4,
the ones made for reconcile were nearly all reads of one item, `bd show`,
stopped at the limit while two to five other bd processes had the store open,
each of those started seconds before. For example, at 23:17 on November 3
(07:18:41Z on November 4) `bd show yoyodyne-ifd.432.25.9` ran its full two
minutes while `bd ready`, `bd list` and another `bd show` held the store. A
single-item read that is never let in is tried three times, so it can cost a
pass more than six minutes. A closed-work listing that is not answered cost
the same, and the docket step then asked for it again.

Two figures show that this is spread across the whole pass rather than in any
one read. Of the 41 timed-out passes, only 12 had any call made for reconcile
stopped at its limit; in the other 29, every call answered, and the pass ran
out of time on the sum of them. And passes that finished took a median of 5.9
minutes, with one in ten taking more than 12.1 and the longest 14.8, while
asking the forge about 76 publications took under 2 seconds of that.

A timed-out pass recorded none of this. Reconcile writes its report when it
finishes, so a pass stopped at fifteen minutes left only the first thing it
had written to standard error, a notice about where the harness keeps its
state.

## What was changed

- Reconcile accounts for every tracker call. Its report ends with how many
  calls it made, how long they took together, and the three longest, before
  the line about the forge. A call that takes 30 seconds or more, or is stopped
  at its limit, is written to standard error the moment it ends, naming the
  read and how long it ran. A pass stopped at fifteen minutes therefore has a
  line at the end of its record for each such call; where every call answered
  in under 30 seconds and only their total ran out the clock, it has none, and
  the total is printed only by a pass that finishes.
- A listing of closed work the tracker did not answer is not asked again by
  the second step that needs it in the same pass. That saves up to six minutes
  on a pass that has already lost six.

## The same items, read one at a time, every pass

The tracker client logs every call it makes, by process, under
`tracker-calls` in the product's state. Read on November 15, those logs held
264 reconcile passes from the two days before. Their single-item reads,
`bd show <id>`, numbered 2,971 and took 23,653 seconds between them; the
closed-work listing, made once a pass, took 3,123 seconds; nothing else came
close. The slowest passes each read the same eleven items one after another,
every one of them closed (yoyodyne-ifd.429.96, 428.86, 432.25.9, 429.73,
435.43, 428.126.1.1, yoyodyne-qzj, 428.99.11, 428.116, yoyodyne-wj4l.1 and
428.130), each read taking 5 to 120 seconds, and the longest pass spent 818
seconds of its fifteen minutes on fifteen tracker calls.

Those eleven are the work items of finished runs whose clean-up is refused
because their worktree has changes in it (for example run-83a4620a, whose
record says `refusing to remove a dirty worktree`). Such a run stays
outstanding, so every pass settles it again, and settling it reads its item
to see whether the item is closed. The run itself is a separate matter; what
it costs reconcile is one turn in the queue for the store per run per pass.

Timed on a copy of the store at 02:22 PST on November 15 (1.5 GB, 1,333 closed
items; the live store was being written throughout, so the copy was taken
while it changed, and it opened and answered normally), with the machine's
load average about 12 on 16 cores:

| Read | Took |
| --- | --- |
| the eleven items, one `bd show` each | 5.7s to 6.7s |
| the eleven items in one `bd show` naming all of them | 4.2s to 4.7s |
| the eleven items in one `bd list --id=…` | 1.5s |
| `bd list --json --limit=0 --status=closed` | 2.0s |
| `bd show yoyodyne-qzj --json` | 1.8s |

With four closed-work listings looping on the copy at the same time, the
eleven single reads took 76 seconds and the one batched read 45. bd does not
let commands in in the order they arrive, so a batch is not eleven times
faster under contention, but it waits for the store once where the single
reads wait eleven times, and on the live store each of those waits was what
cost 5 to 120 seconds. None of these reads came near the two-minute limit on
the copy; on the live store the reads that ran past it did so waiting.

## The change: the runs' items are read together

Before it settles anything, the sweep now reads the work items of every
outstanding run in one `bd show` naming all of them (`beads.Client.ShowMany`,
fifty to a call), and each run's first read of its own item in that pass is
answered from that one call. Nothing else changes about how a run is settled:
every later read of the same item, every read a write makes to check or
confirm itself, and every read after anything in the pass wrote to the item
asks the tracker as before, and nothing read ahead is given out more than five
minutes after it was read or after the settling is over. A batch the tracker
does not answer holds nothing, and each item is then read on its own as
before; it is asked once rather than three times, so it adds at most one
limit's wait. Runs that publish a document or bring in a file are settled
against a stand-in for their item and are not read. `bd show` was kept rather
than `bd list --id`, which is faster, because `bd show` answers each item
exactly as it answers it alone (TestShowManyConformance) and every settling
step was written against that answer.

`TestReconcileReadsItsRunsItemsInOneCallAndListsNothing` in
`internal/orchestrator` holds the sweep to this: over a stand-in for bd that
times out every listing, two finished runs whose clean-up is refused are kept
exactly as before, with one read of both items and no listing; the same sweep
without the read ahead makes one read per item.

## What this does not fix

The waiting itself. Each bd command still waits its turn for a store every
other process keeps busy; this change cuts how many turns reconcile takes, by
one for every outstanding run after the first. The closed-work listing is
still one call a pass, of every closed item rather than only the ones the
pass asks about; it answers in about two seconds on the copy and took a median
of under a second on the live store, so it is not what runs the pass out of
time. The fixes that would shorten each wait are outside reconcile: fewer bd
processes polling the store, or a store that lets readers in together.
