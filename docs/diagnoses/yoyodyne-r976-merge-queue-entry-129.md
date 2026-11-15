# yoyodyne-r976: why the pass time limit fix (entry 129) had not landed while later fixes did

The approved fix that makes a recurring pass's time limit count only running
time (yoyodyne-ifd.428.140.1, run `run-492ff1e8f660ff2ea9f4568fc202bfaf`, pull
request 1832, approved head `0499be81`) joined the harness's merge queue for
`main` as entry 129 at 10:12 PST on November 13. The Codex review read size fix
(yoyodyne-kuo) and the team pause (yoyodyne-ifd.429.105) landed after that, the
same day, and nothing recorded entry 129 being checked or handed back. This
diagnosis says why.

All times are Pacific (PST). Everything here was read on November 14 from the
harness's own records: the queue's record for `main`
(`merge-queues/7b7ce2687da502583fcd947c628da3df/queue.json`), its per entry
landing, generation and hold files beside it, the queue's last pass
(`pass.json`), the per entry event logs under `merge-queues/events`, the run
record for entry 129, and `usage-limits.jsonl`. Whether a change is already on
`main` was read with `git merge-base --is-ancestor`.

## The answer

**Nothing passed over entry 129. The queue had not reached it yet.**

- **The queue works entries strictly in the order they joined.**
  `MergeQueueWorker.next` in `internal/orchestrator/mergequeueworker.go` takes
  the first entry, in the order `runstate.MergeQueueWorkOrder`
  (`internal/runstate/mergequeue.go`) gives, that still waits. That order is by
  place, which is an entry's own order unless it moved its change from one way
  of landing to the other, and then by when it joined. No entry in this queue
  records a place of its own. The one exception `next` makes, an entry waiting
  for `main` to pass a check it fails at the candidate's base, did not apply to
  any entry here.
- **The two fixes that landed "after" entry 129 were earlier entries.** Each of
  those work items had been in the queue twice:

  | Work item | Entry that landed | Joined | Landed | Its other entry |
  |---|---|---|---|---|
  | yoyodyne-kuo | 83 | 15:09 PST, Nov 11 | 17:39 PST, Nov 13 | 128, joined 08:38 PST Nov 13 for run `run-743ec2a5`, still waiting |
  | yoyodyne-ifd.429.105 | 86 | 18:51 PST, Nov 11 | 20:19 PST, Nov 13 | 40, joined November 9 for an earlier run |

  Matching a landing to the queue by work item, rather than by entry, made it
  look as though entry 128's change had gone ahead of entry 129.
- **At 20:33 PST on November 14 the queue was at entry 92**
  (yoyodyne-ifd.429.146.1). Its merge was asked for at 20:18 PST, and the
  forge was holding it until its own requirements were met (pull request 1868).
  Entries 93 to 128 are ahead of entry 129. Five of their approved heads are
  already on `main` (entries 95 and 96 have been handed back), so 31 entries
  still have to be checked and reviewed or handed back before entry 129.
- **The queue lost most of a day to the weekly Opus usage limit.** Entry 88
  (yoyodyne-fp3.2) passed its checks at 21:33 PST on November 13. The account's
  weekly Opus limit had been recorded as reached at 21:08 PST, with a reset at
  19:00 PST on November 14. The queue does not ask for a review while the
  reviewer's account and model are known to be out of capacity
  (`MergeQueueWorker.reviewerReady`), and the queue works one entry at a time,
  so no entry moved for about 21 hours. Entry 88's review started 49 seconds
  after the reset, and it landed at 19:32 PST. Entries 89, 90 and 91 were then
  handed back within seven minutes because they no longer merge onto `main`.
  That wait was the queue holding to a usage limit, not a fault in the queue.
  It is also why nothing about entry 129 appeared: an entry waiting its turn
  has no record of its own until the queue reaches it.

## Entry 129's own state

- Its queue entry carries the approved head `0499be81`, the pull request, and
  run `run-492ff1e8f660ff2ea9f4568fc202bfaf`, with no landing, generation or
  hold file. That is what every entry not yet reached looks like.
- The run record reads `succeeded`, phase `cleaning_up`, with its worktree
  removed at 14:56 PST on November 13. The queue does not need the worktree. It
  builds its candidate from the approved head, and the commit and its branch
  `yoyodyne/yoyodyne-ifd-428-140-1/492ff1e8` are still in the repository and on
  `origin`. The worker reads the run only for its developer session, which the
  record has, so the candidate's reviewer can be shown to be someone else.
- The delivery workflow's record stands at `abandoned`. It moved there from the
  check step at 08:12 PST on November 13, when an earlier check attempt could
  not run, and the run went on to pass its checks and be approved without the
  workflow record following it. The run record notes this mismatch itself in
  `workflow_divergence`. The merge queue does not read the workflow record, so
  this does not hold entry 129 back. It does make the run look finished to
  anyone reading that record.

## What this means for the work item

Nothing in the queue skipped or lost an entry, so no change to how the queue
chooses its next entry follows from this case. Entry 129 lands, or is handed
back to its run with a reason, when the queue reaches it. At the pace it had on
November 13, when it settled an entry every one to two hours with handed back
entries taking minutes, that is about a day of the queue running.

Two things would shorten the wait, and neither is this item's to make:

- The queue choosing its next entry by priority and age
  (yoyodyne-ifd.429.183) would move entry 129, a priority 0 fix, ahead of any
  lower priority entries in front of it.
- The development manager may decide a fresh run is not needed, since the
  change is approved and waiting its turn.
