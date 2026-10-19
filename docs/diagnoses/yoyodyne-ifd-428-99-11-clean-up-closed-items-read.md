# yoyodyne-ifd.428.99.11: why the clean-up could not read which work items are closed

The development manager reported that the scheduled clean-up failed at 13:50
PDT on October 17 and again at 04:44 PDT on October 18. Both times it could not
read which work items are closed, so it could not check runs recorded as
merged against main. The report suggested two things: that this is why the
merge of the build letting an owning role decide its own documents' amendments
(yoyodyne-i92, pull request #1281) is stuck, and that the store cleanup the
slow-tracker investigation (yoyodyne-6u7) named might be the cause, since
nothing recorded that a person had run it.

The records say neither. The read was not answered because other tracker
commands held the store, and the harness had already been running the store
cleanup itself, every day.

All times below are Pacific (PDT) unless marked Z, which is UTC.

## What the two failures recorded

The maintenance pass's records (`sweeps/sweeps.jsonl` under the product's
state) carry the reconcile step's failure in the tracker client's own words:

| Pass started | Step that failed | What the tracker client said |
| --- | --- | --- |
| 13:50 Oct 17 (20:50:39Z) | checking runs recorded as merged against their target branch | `bd list did not answer within its 30s bound on any of 3 attempts over 1m41s`; when the last attempt was stopped, the store was held open by `bd list --json --limit=0 --status=in_progress`, started 13:53:53 |
| 04:44 Oct 18 (11:44:03Z) | the same | the same bound and attempts; the store was held by `bd show yoyodyne-ifd --json`, started 04:46:34, and `bd list --json --limit=0 --status=open`, started 04:46:40 |
| 06:25 Oct 18 (13:25:33Z) | closing the docket entries of closed items | the same bound and attempts; the store was held by `bd show yoyodyne-i92 --json` and `bd ready --json --limit=0`, both started about 06:30 |

So for each failure: the read took 1 minute 41 seconds, three attempts of 30
seconds with waits of 2 and 8 seconds between them. What stopped it was the
30-second limit each tracker call is given. The tracker returned nothing: bd
was stopped before it printed anything, each time with other bd commands
holding the store. The third failure is the same read made by a different
step of the same sweep, and it is the one that put the closed-items read on the
record a second time on October 18.

Every other maintenance pass in those two days read the same listing and
passed: 11 passes between 09:53Z and 11:34Z on October 18 alone, and all but
one of the passes after. The read fails when the store is busy, not every time.

## The read is not slow on its own

The tracker held 1,313 items when this run started, 1,045 of them closed. Every
closed item, written as JSON, is 18.6 MB. That is the largest listing the
harness makes, so its size was the first suspect.

Measured against bd 1.1.2 on a fresh store built from the tracker's own export
(`bd init` then `bd import .beads/issues.jsonl`, in a private temporary
directory, never against the live store):

| Listing | Output | Time |
| --- | --- | --- |
| every closed item as JSON (what the clean-up asks) | 18.6 MB | 2.5 s, 2.7 s |
| the same, without labels | 18.7 MB | 2.9 s |
| every closed item's id only, through a template | 3.6 KB | 2.5 s |
| 40 named items as JSON | 0.7 MB | 3.7 s |

The time does not follow the size of the answer: a listing of ids alone takes
as long as the full one. What a bd command pays for is opening the store. The
live store is 619 MB against 165 MB for the fresh one, because it keeps every
change's history, so each command on it pays more. Still, the listing fits
well inside its 30-second limit on its own.

## What does stop it: every bd command locks the whole store

bd takes one exclusive lock on its store for every command, reads included
([the earlier diagnosis](yoyodyne-ifd-433-20-tracker-listing-timeouts.md)). A
listing that arrives while other commands are queued waits its turn, and the
30-second limit counts the wait. All three failures name bd commands holding
the store when the read was stopped, started by other parts of the harness:
the scheduler's listings of open and in-progress work, `bd ready`, and reads of
single items. Commands that started after the clean-up's attempt began still
held the store when the attempt was stopped, which is a queue behind one lock,
not one slow command.

The clean-up made this worse for itself in one way. Two of its steps each
asked for every closed item separately, minutes apart, so every pass took two
turns in that queue for the same answer, and either one failing failed the
pass.

## The store cleanup was not missing

The slow-tracker investigation (yoyodyne-6u7) named `bd gc --skip-decay` as a
cleanup only a person could run. Since the change that has the harness clean up
the tracker's store itself (yoyodyne-ifd.433.40), it is no longer a person's
step. The harness records each cleanup under `tracker-store-cleanups`:

| Cleanup started | Took | Store before | Store after |
| --- | --- | --- | --- |
| 17:08 Oct 16 | 14.6 s | 1,009 MB | 595 MB |
| 17:26 Oct 17 | 0.9 s | 642 MB | 621 MB |

The store was cleaned up about 21 hours before the first failure and about 11
hours before the second. The cleanup is not the cause, and nothing about these
failures needs a person.

## Pull request #1281 is not stuck on this

The run of yoyodyne-i92 that opened pull request #1281
(`run-5953714abad90e70959a0e06fc19aeb6`) is recorded as failed, with the pull
request open: its approved change lost the race to land on main five times
because other work landed first each time, so the run stopped replaying it and
gave back its developer slot, keeping its work. It is not recorded as merged,
so the clean-up's check of runs recorded as merged never looks at it, whether
the closed-items read answers or not. What becomes of it is the development
manager's decision about a run that stopped, not this read.

## Why nobody was told

A failed maintenance pass is raised as a finding to a role only once it has
failed three times in a row (`runstate.PassFailureThreshold`). The read fails
intermittently, so it never reached three in a row: each failure sat in the
pass's record, and in a health observation, until somebody read it.

## What changed

- The clean-up reads which work items are closed once per sweep, and both of
  its steps that need the answer use that reading
  (`orchestrator.ClosedListing`). A reading that failed is not kept, so the
  second step asks again rather than inheriting the failure. That halves the
  clean-up's turns in the store's queue for this answer. The second step's
  answer is now a few minutes old, so an item that closes between the two steps
  has its docket entry closed on the next sweep instead of this one.
- A step that cannot read which work items are closed fails as that
  (`orchestrator.ClosedItemsUnread`), with the tracker's own words. The sweep
  then files a report for the development manager on the first failure, saying
  what the clean-up could not do, that a read with no answer within its limit is
  usually waiting behind other tracker commands, and what the tracker said,
  including which commands held the store. While that report is unhandled,
  later failures file nothing more; once she handles it, the next failure files
  a new one.

## What this does not change

The queue behind bd's one lock is still there, and a busy enough store can
still keep the clean-up's read waiting past its limit. When that happens, the
development manager is now told the first time, and the next pass, ten minutes
later, asks again. Making fewer bd commands queue at once (the scheduler's
repeated listings of open and in-progress work are most of what the three
failures name) is separate work that has not been done.
