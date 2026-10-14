# yoyodyne-6u7: what the tracker timeouts of October 12 and 13 were waiting on

The factory-flow program manager reported five tracker failures over October 12
and 13: two reads of the document word check's item (yoyodyne-7my) that failed
three tries each while a merge was being settled, a full listing during a
briefing at about 18:40 PDT on October 12, a read of the load-scaled check
limits item (yoyodyne-ifd.429.41) that failed the maintenance pass on October 13,
and two writes recording configuration comparisons that failed the same pass.

The harness's records say these are two different things under one message. Most
of the "timed out" tracker calls in those two days were never given thirty
seconds: the harness gave them five or ten. A smaller number did wait their full
thirty seconds three times, and for those the store really was unavailable for
over a minute and a half. All times are PDT unless marked Z, which is UTC.

## Most of them were the caller's deadline, not the tracker's limit

The process runner reports a bd it stopped as `timed_out` in two cases: bd ran
past the client's own thirty-second limit, or the context the caller passed ran
out first (`internal/execution/process.go`, and before bd even starts if the
deadline has already passed). Both came out as
`bd update failed with status timed_out and exit code -1:`. The tracker client
retries a call stopped at its own limit, but it deliberately does not retry once
the caller's deadline has passed, so the bare message with nothing after the
colon is how a call stopped by its caller reads.

These callers gave the tracker less than its limit:

- **The note on a passed-over item** (`notePassedOver`), and the note after
  several passes in a row left one, were written under `recordContext`: five
  seconds, sized for writing one small local file. These account for nearly all
  of the 186 `bd update … timed_out` lines in the state from October 12 to 14.
  The notes on the development manager's top item (yoyodyne-9j7) show the cost:
  the passes whose records carry the failure — #740, #742 and #744 on the
  evening of October 11 — are exactly the ones whose notes are missing from the
  item. A write that is stopped is lost.
- **The configuration comparison note** (`configreaders.go`) and **the read of an
  item's earlier settlement notes** (`reconcilefinding.go`) had ten seconds.
  The two writes that failed the maintenance pass at about 18:11 PDT on
  October 13 were these.
- **About twenty more run and settlement writes** — blockers, releases, outcome
  notes, pause notes, preserved-work notes — had ten seconds each, written from
  a fresh context in `pipeline.go`, `converge.go`, `escalationended.go`,
  `redeploydrain.go` and `documentpublication.go`.

What a tracker call normally takes, measured with bd 1.1.2 on a copy of the live
store taken at 01:32 PDT on October 14 (1,221 items, a 20 MB export):

| Call | Measured, one call at a time |
| --- | --- |
| read one item (`bd show`) | 0.7–1.9 s |
| claim or append a note (`bd update`) | 0.7–1.8 s |
| list every item | 1.5–4.3 s |
| export every item | 1.8–3.5 s |

The spread is the machine's other work at the time of each measurement.

The size of an item makes no difference: the largest item (187 KB) reads as fast
as a 17 KB one. A write also waits for the harness's own write lock, and every bd
process waits for bd's exclusive lock on the store, so a write queued behind one
listing and one export is already past five seconds. That is why the five-second
note failed on alternate passes rather than on every one.

The claim failures that appear every 45 minutes on October 12 for two items
(yoyodyne-ifd.435.1 and yoyodyne-ifd.435.18) are not new failures. They are the
development manager's docket restating two runs that stopped on October 10.

## Some were the store unavailable for over a minute and a half

These ended with the client's own retry message —
`did not answer within its 30s bound on any of 3 attempts over 1m40s` — so each
waited thirty seconds three times:

- the two reads of yoyodyne-7my on October 12, while the merge was settled;
- the full listing at about 18:40 PDT on October 12;
- the read of yoyodyne-ifd.429.41 by `yoyo reconcile` at about 18:01 and 21:15
  PDT on October 13;
- two reads at 22:42 PDT on October 13 by two different roles at once: the
  development manager's of yoyodyne-ifd.435.14 and the Lead Product Manager's
  of yoyodyne-ifd.429.63.

The last case rules out a queue of ordinary calls. With every call taking under
five seconds, two callers failing three thirty-second tries together means one
holder kept the store for well over a minute.

**What held the store is not in the harness's records.** The harness does not
record which other bd processes were running when a call ran out of time, and bd
keeps no log of its own. What could be checked:

- **Not bd's git hooks.** The primary checkout runs bd's hooks on every commit,
  checkout, merge and push in every worktree, each allowed 300 seconds. A commit
  with them took 0.35 s against 0.32 s without.
- **Not a push to the tracker's remote.** bd can push its database after writes,
  but the copy it keeps of the remote has not changed since August 19.
- **Not memory.** The machine had 95% of its memory free.
- **bd's own backup is a possibility, not shown.** bd copies the whole store to
  `.beads/backup` about every fifteen minutes, from inside whichever bd command
  comes due. A backup with little to copy took 0.4 s. But the gaps between
  backups bracket two of the failures — 00:56 to 01:34 PDT, and 22:11 to 22:45
  PDT on October 13, against a usual fifteen minutes — and 34 of 190 gaps in the
  record are over twenty minutes. A backup that bd was killed in the middle of
  would look like that.
- **The store has never been cleaned up.** Every write since August is still in
  one 885 MB journal file (17,599 commits). Running bd's garbage collection on
  the copy took 9 seconds and left 526 MB. With the history kept, a read went
  from 0.7 s to 0.15 s, a full listing from 3–4 s to 0.6 s, and an export from
  2.4–3.5 s to about 1 s. Every bd process opens the whole store, so this cost
  is paid by every call and lengthens every wait behind the lock.

The re-run of the concurrent tracker access item (yoyodyne-ifd.271) measured bd
1.1.2 holding a call for 170 seconds to 40 minutes in 3 of 9 batches. That is
the kind of hold these cases need, and nothing recorded here tells it apart from
a backup that ran long.

## What changed

- Every tracker call that was given five or ten seconds is now bounded by the
  tracker client's own limits. A write that outlives its caller still does, as
  before, with a five-minute bound (`trackerRecordContext`), longer than the
  most the client's own retries can take. A call that keeps its caller's
  cancellation uses `trackerCallContext`.
- A tracker call that runs out of time says what it was doing, for whom, how long
  it ran, and whether the tracker's limit or its caller's deadline stopped it.
  It is named for a role's recurring pass, a developer run, a role's
  conversation, or `yoyo reconcile`. The words the recovery code recognises
  (`exit code -1:`) are kept at the front.
  [The operations guide](../operations.md#a-tracker-that-does-not-answer-a-listing)
  shows both forms.

## What is left

- **Clean up the tracker's store.** Running bd's garbage collection
  (`bd gc --skip-decay`, which deletes no items and keeps the history) on the
  primary checkout's store made every call on the copy between one and a half and
  five times faster, which shortens every wait behind bd's lock. A developer run cannot do it,
  because its sandbox cannot write the store. It needs either a person or a
  maintenance step that runs it when nothing else holds the store, repeated as
  the journal grows.
- **Record what held the store.** For the store being unavailable for over a
  minute and a half, the cause can only be named next time if the harness
  records what else was using the tracker when a call ran out of time: which bd
  processes were running and since when, and whether bd's backup was in
  progress.
