# yoyodyne-ifd.428.126.4.19: why the tracker store stays near 1.5 GB after the clean-up

The tracker store was 1497 MB on November 15 and 1565 MB on November 17, past
its 1024 MB limit, with the daily-or-128-MB clean-up running about every four
hours. This document names the cause from measurements of the store, shows the
remedy that works, and says what still stands between that remedy and the live
store. **The live store has not been shrunk by this change**: a developer run
cannot write to it, and the remedy needs a design ruling first (see
[What is left](#what-is-left)).

All sizes are the database directory, `.beads/embeddeddolt`, as maintenance
measures it. Times are Pacific (PST).

## What is in the store

- 1881 work items (every status), 33 MB when exported as text.
- 47,591 commits of history on November 12, about 1000 more every day.
- Disk: 50 archive files under `noms/oldgen`, 1.6 GB. One of them, written by
  the first clean-up on October 16, is 582 MB. Every later clean-up added a
  few small files beside it and removed none.

## Cause 1: no bd command gives back the old history's disk space

`bd gc --skip-decay` calls Dolt's garbage collection without its full option,
which is the only one that rewrites the old-generation files where everything
older than the first clean-up lives. So the clean-up deletes nothing of what the
history reached, and then, once the history is squashed, nothing of what the
squash made unreachable either.

Measured, from the records in `tracker-store-cleanups` and
`tracker-store-measurements` under the product's state, and repeated here on a
scratch copy of the live store:

| Step | Size |
| --- | --- |
| Live store, Nov 17 | 1565 MB |
| Each of the last 12 clean-ups, size taken back | 2 to 15 MB (every one under 1%) |
| Measurement of Nov 12, after `bd compact --days 30` (took 11 min 40 s) | 1498 MB before, 1605 MB after: it grew |
| Same, after `bd flatten` (which itself runs Dolt's collection) | 1605 MB: no change |
| Scratch copy, `bd flatten` then `bd gc`, and again with the `origin` remote removed | 1.5 GB: no change |
| Scratch copy, items exported, a fresh store made, items imported, `bd gc` | **14 MB** |

So the store is not large because of the 1881 items. Nearly all of it is
history that squashing cannot release.

## Why the listings are slow is not the size

On the same copy, listing open items takes 0.2 to 0.6 seconds at 1.5 GB, before
and after every step above, and the measurement of Nov 12 found listings within
10 seconds on the copy every time. On the live store, 917 tracker calls in one
hour took 10,751 seconds in total across callers, with callers stopped at their
2-minute limit 52 times. The listings are slow because many callers wait for
bd's one-process-at-a-time lock, which is the cause the investigation of the
scheduled reconcile (yoyodyne-ifd.428.126.4.14.1) found, not because the store
is big. Bringing the store under 1 GB removes the size report and the cost of
every call opening the whole store (about 0.2 to 0.4 s a call, from the
copies), but will not by itself end the queue; the shared tracker client
(yoyodyne-ifd.428.126.4.18) is what removes the repeat reads.

## Cause 2: the squash has never run on the live store

Maintenance decided a squash on Nov 15 at 23:08 PST ("the live store is 1528 MB
... past its limit") and recorded it as skipped: new runs were held for the
15-minute redeploy drain bound and two developer runs were still going when it
ended. Developer runs routinely last longer than 15 minutes, so the fourth
condition of [the store-size design](../designs/tracker-store-size.md) (no developer run in
flight) is rarely, if ever, met inside the bound. No squash of any kind has run
on the live store since the design landed.

## The remedy that was measured

Rebuilding the store from its own export, on the scratch copy:

1. `bd export` the live store to a file.
2. In a new empty directory, `bd init --prefix yoyodyne --non-interactive`,
   `bd import` the file (31 s for 1881 items), then `bd gc --skip-decay --force`.
   The result is 14 MB with 9 commits.
3. Put the new `embeddeddolt` directory in place of the old one, keeping the
   live `metadata.json` and `config.yaml`. `bd list`, `bd show` and `bd create`
   all work against it with the live project id.

Every record compared equal before and after on title, description, notes,
status, priority, design, acceptance criteria, comments, dependencies and
labels, for all 1881 items. Only history is lost, which the design allows.
The hold on the store is about a minute (export, build elsewhere, swap), not the
11 minutes 40 seconds of `bd compact`, and the build step runs on a separate
store so it holds no lock on the live one.

Do not use `bd init --reinit-local` on the live store for this. The store has a
configured git remote and a backup destination, and that command is the one
whose safety rules are about discarding remote history.

## What is left

1. **A design ruling.** Condition 4 of the store-size design stops a squash
   unless no developer run is in flight. A rebuild holds the store about a
   minute, and developer runs do not call the tracker themselves (the harness
   reads it for them), so whether a run in flight still has to be waited for is
   the architect's to rule. Proposed with this change.
2. **The rebuild as a maintenance step** in place of `bd compact` and
   `bd flatten` (which give back no disk), written through the harness's
   confined-write primitive, measured on the copy like the other stages, with
   the 14-day copy kept before the swap as the design already requires. To be
   admitted by the Lead Product Manager; it is not in this change.
3. **The backup destination.** `.beads/backup` is 3.8 GB, written by
   `bd backup sync` as recently as 19:14 PST on Nov 17, and holds the full old
   history. It is outside what maintenance measures. After a rebuild its history
   no longer matches the store, so it must be started again from the rebuilt
   store; deciding that, and whether 3.8 GB of backup is wanted, is for the
   architect.
4. **How often the store is measured** (asked in the development manager's note
   on this item): the measurement ran on Oct 28, Nov 4, Nov 5 and Nov 12 only,
   and the clean-up every four hours. The measurement is not what was missing;
   a remedy that works is. Once item 2 exists, measuring weekly is enough, and
   the size check that decides a squash already reads the disk each pass.

Until then, a person can do the three steps under "The remedy that was
measured" by hand with the harness stopped, which was verified here only on a
copy.
