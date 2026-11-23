# yoyodyne-ifd.435.95: what second document reviews cost, read at 58db0aec0f

The item corrects how `internal/readmodel/accomplishment.go` reads the cost of
a second document review. It cannot be finished yet, because the second review
is not on main. Building it is the second-document-review item
(yoyodyne-ifd.435.93), and that change is still in the merge queue.

## What was found

Read on November 23, 2026, at main `58db0aec0f` (12:49 PST), without a fetch:
the run has no network.

- No file on main names `compare_documents`. `internal/config/reviewermodels.go`
  holds only `documents` and `code`. No run on main can make a second review,
  so no test on main can run a publication with the comparison on.
- The first version of the second-document-review change, `a2ce501ef8`, has the
  fault this item describes. The merge queue's reviewer handed it back for it.
- The second version, `39bb590fb8` (11:54 PST, recorded in the tracker's copy
  as queue entry 713), is approved and waiting. `git merge-tree` merges it onto
  `58db0aec0f` with no conflict.

## What the queued change already does

- Each second review's line in the spend log carries `comparison: true`, set
  when the review is made (`internal/runstate/spend.go`,
  `internal/spend/spend.go`).
- `accomplishment.go` takes as reviews only the lines without that mark, so a
  second review's cost is no longer counted under the next verdict or as a
  review with no verdict. The total is unchanged. Tests cover both
  cases.

So the wrong figures should not reach main. The item's premise, that they are
wrong while the comparison runs, was true of the first version only.

## What is left of this item once that change lands

1. **Matching by identity, and a cost of its own** (condition 1). The queued
   change leaves the second reviews' cost out of the reviews and shows it
   nowhere except inside the reviewer's total. `ReviewComparison` on the run
   record holds no identity a spend line also holds, so one second review's
   cost cannot be matched to it; a run and a commit are not enough when one
   run is reviewed twice. Every spend line already carries `session_id`, and
   the review result returns it. Recording that session on `ReviewComparison`,
   failed second reviews included, gives the match. The session is the second
   review's own, because it is started fresh.
2. **Costs already recorded** (condition 2). There should be none without the
   `comparison` mark: no build from main has made a second review. Lines
   recorded between that change landing and the session being recorded can be
   counted as the comparison's by their mark, but not tied to one review where
   a run had several, and the reading must say so as unattributed.
3. **The test's remaining checks** (condition 3). The queued test
   (`internal/orchestrator/reviewcomparison_test.go`) does run a publication
   with the comparison on and put its spend lines through the reading, and
   checks that one review is counted. It does not check that the second
   review's cost is shown once in its own place, or the total.
4. **The documentation.** `docs/reporting.md`, in the queued change, says the
   cost "stays in the reviewer's spend", and will need to say where it is shown.

Building any of this now would mean copying the queued change's fields, or the
whole change, into this one, and the two would conflict in the merge queue.

## A duplicate

The item "The weekly reading of what reviews achieved counts a second document
review's cost under that review, not under the run's next verdict"
(yoyodyne-ifd.432.59) is open, asks for the same thing, and already waits on
the second-document-review item. One of the two should be retired.
