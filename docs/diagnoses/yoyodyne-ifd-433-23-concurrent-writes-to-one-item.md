# yoyodyne-ifd.433.23: overlapping writes to one tracker item lose one, silently

The concurrent tracker access run (yoyodyne-ifd.271, run-5680e6a2) stopped on
2026-09-29 at 17:50 PDT with its own new check, `TestConcurrentWriteConformance`,
failing. The check made four overlapping `--append-notes` writes to one item
and found one missing afterwards. The run's program manager judged the check
was at fault. It was not. The check found a real loss, and the loss is `bd`'s.

All times are Pacific (PDT).

## The answer

Against `bd version 1.1.2 (20e493e56)` on macOS, overlapping writes to one item
lose one now and then, and every write involved exits 0. The adapter now queues
its writes to any one item (`Client.write` in `internal/beads/writelock.go`),
and the same check passes with the queue in place and fails without it.

## What was run

Each batch is a fresh scratch store (`git init`, `bd init`) with one item,
and nine `bd update` commands started at once against it: six
`--append-notes` with a distinct line each, one `--priority=1`, and two
`--set-metadata` with different keys. Afterwards the item was read with
`bd show --json`. This was run on 2026-09-30 from 22:44 to 23:00 PDT.

| What | Batches | Writes | Exited non-zero | Missing on read-back |
| --- | --- | --- | --- | --- |
| Note appends to one item | 15 | 90 | 0 | 5, in 3 batches |
| Metadata keys on one item | 15 | 30 | 0 | 1 |
| Priority change on one item | 15 | 15 | 0 | 0 |
| Any loss in a batch | 15 | 135 | 0 | 4 of 15 batches |

In one batch, writer 4's note and the `k1` metadata key went missing together.
In another, writers 2 and 4 both lost their notes.

Writes to **different** items did not lose anything. In 8 batches, six items
each had a note appended and a metadata key set at the same moment. All 48
writes were present afterwards, and none exited non-zero.

## Why it happens

An append in `bd` reads the notes already on the item and writes them back with
the new line on the end. A metadata key is handled the same way: the metadata is
read, the key is added, and the whole thing is written back. If two of these run
at once, both read the same starting notes, and whichever writes second puts
back notes that do not contain the first one's line. Each command's own answer
shows the item as that command wrote it, with its own line present. So the
adapter's read-back in `confirmWritten` passes for both commands, and nothing
anywhere says a line was lost. A priority change sets a single value and reads
nothing first, which is why it was never lost.

## Why the concurrent tracker access run (yoyodyne-ifd.271) saw nothing on 2026-09-03

That run's exercise made six overlapping appends once and found all six. With
about one batch in four losing something, a single batch coming back clean is
expected. Its run on 2026-09-29 is the one that failed.

## The two live losses reported on 2026-09-30

The factory-flow program manager reported two possible live losses that day and
asked this item's run to check both. Neither was a lost write. Both were read
on 2026-10-01 from the harness's own records (the conversations' event logs and
the run's record under the state directory) and the tracker export copied into
this run's worktree. This run cannot open the store itself, so `bd`'s own
history was not read.

**The priority on the re-arms investigation (yoyodyne-8ff).** The report said a
note and a change to priority 0, sent in one block at about 22:00 UTC, both
reported success but only the note survived. The program manager's event log
shows the two were not concurrent. The note was applied at 22:12:13.29 UTC, and
the priority change was requested 0.02 seconds after that and applied at
22:12:15.84 UTC, so the second started after the first had finished. Then at
22:30:48 UTC the Lead Product Manager set the same item to priority 1, in
conversation chat-91253e0e070c17b0663651cc48602122, during the operator's
priority-0 cleanup that evening ("The periodic pass (413) now has its re-run
decided and in hand..."). The program manager read the item at 22:58 UTC, found
priority 1, and found nothing on the item saying who had changed it. That last
part is the actual gap: a `reprioritize` action sets the priority and appends
no note (`internal/chat/tracker.go`, `case actionReprioritize`), so its reason
lives only in the conversation that made it. A deliberate change by another
role therefore looks the same as a lost write to anyone reading the item.

**The missing reason for the 21:14 UTC stop on the supervisor's periodic pass
(yoyodyne-ifd.413).** The report said the item's notes ended at the approval
and carried no reason for the stop. The reason is on the item. The run
(run-05654a9d) was approved at 21:14:02 UTC, its replay onto main at 40937bf2
conflicted in `internal/cli/product.go`, and the docket recorded the stop at
21:14:11 UTC. The item's notes hold that blocker ("Yoyodyne stopped this item:
its target branch moved, and this change conflicts with what the target now
holds", target 40937bf2, replay stopped on `internal/cli/product.go`),
followed by a "Yoyodyne blocked this item" note for the same run. Together
those two notes are about 20 KB, because each repeats the run's whole
reviewer summary and ends with it. The tracker read a role is given shows only
the last 4 to 8 KB of the notes (`maxTrackerItemBytes` and
`minTrackerNotesBytes` in `internal/chat/tracker.go`). So the program manager's
read at 21:18 UTC showed the end of the blocked note, which is the approval
text, and the line saying why the run stopped was above the cut.

Neither case involved two writes to one item at the same moment, so neither is
evidence for or against the loss measured above. Both are gaps in what a reader
of the item is shown, and they are listed as discovered work in this run's
summary rather than fixed here.

## The fix and how it is checked

`Client.write` takes an exclusive advisory lock on
`yoyodyne-writes/<item>.lock` inside the store directory (`.beads` in an
ordinary checkout) for as long as `bd` runs, around every command that changes
an existing item: update, claim, close, adding or removing a dependency, and
each record and status write built on them. The operating system drops the lock
if the process holding it dies, so a crashed writer leaves nothing held. The
lock file is opened through `repowrite.Root.OpenLock` (originally
`OpenAppend`, which no longer exists), confined to the store directory. `.beads/.gitignore` already ignores `*.lock`.

The store directory is found the way `bd` finds it from the same directory and
environment (`storeDirectory`, following `FindBeadsDir` in `bd`'s own source):
`BEADS_DIR`; a `.beads` in the client's directory or any directory above it up
to the checkout's root, following a `redirect` file; and from a linked Git
worktree, the main repository's shared store unless the worktree has a database
of its own. It is resolved through symlinks. So two clients reaching one store
by different paths (a subdirectory, a symlink, a redirect, a worktree) lock the
same file. Where no store is found, `bd` run from there finds none either and
fails on its own, so there is nothing to queue against. `bd` also handles a
Jujutsu secondary workspace and a detached snapshot worktree specially; the
harness writes from neither, and the lock does not follow `bd` into them.
Every harness client points at the primary checkout, which holds the store
directly. A client that names no directory at all takes no lock: no harness
client is built that way, and the ones that are, tests scripting `bd`'s
answers, would otherwise take locks in the live store of whatever checkout the
test suite runs in.

Writes to different items take different locks, because nothing was lost
between them. That also means a `bd` command that stalls (see below) only
holds up writes to its own item. The wait for a lock is limited to the
client's limit on one command. A write that waits that long fails with an
error naming the item, and is not made without the lock. Creating a new item
takes no lock, because there is nothing for it to overwrite.

Checks:

- `TestConcurrentWriteConformance` runs against a real `bd`. Ten clients write
  to one item at once: eight note appends, a price, and a landing. Then every
  write is read back. Without the lock, 1 of 6 runs failed with an append
  missing. With the lock, 7 of 7 passed. One run catches the loss only some of
  the time, so this test can only catch a regression some of the time.
- `TestWritesToOneItemQueue` needs no `bd` and gives the same answer every
  time. A write to an item whose lock is held waits until it gives up, and a
  write to a different item goes ahead.
- Both real-`bd` results say whether the check ran. A run logs
  `RAN against bd version …`. A skip says `SKIPPED, not passed` and gives the
  reason. With `YOYODYNE_REQUIRE_BD` set, a machine without `bd` fails every
  real-`bd` check instead of skipping it, so a green suite there means the
  checks ran.

## What this does not cover

- **Writes outside the harness.** A `bd update` typed in a terminal, or run by
  a script, does not take the lock and can still overlap a harness write. The
  product manager's conversation writes through the harness client, so it is
  covered.
- **The stall.** yoyodyne-ifd.271 also measured `bd` sometimes stalling one
  command for minutes under concurrent load: 3 of 10 batches of six creates at
  once had one command that took between 170 seconds and about 40 minutes, and
  one of those batches was already running behind an exclusive lock. A
  command that stalls while holding an item's lock makes the other writes to
  that item wait up to the client's limit, and then fail. That is a loud
  failure rather than a silent loss, and it affects one item rather than the
  whole tracker.
