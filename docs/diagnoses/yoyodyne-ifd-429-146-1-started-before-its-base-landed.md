# yoyodyne-ifd.429.146.1: started before the change it builds on landed

The item adds a decided run that resolves a conflict met while bringing the
target branch up to date with the team's branch, limited to three rounds of its
own. Its description says it builds on the change for bringing a waiting item's
change current with the team's target (yoyodyne-ifd.429.146), and names what it
extends there: `internal/orchestrator/teambranchupdate.go` and the update
record in `internal/runstate`.

## What the run found

The run was cut from `main` at `2222232c`. That commit carries neither file:

- `internal/orchestrator/teambranchupdate.go` and
  `internal/runstate/teambranchupdate.go` do not exist on it;
- neither the parent's tip `65811c67` nor the commit the harness recorded for
  it, `dc9dcecd`, is an ancestor of it (`git merge-base --is-ancestor` exits 1
  for both);
- the parent change is still only on `yoyodyne/yoyodyne-ifd-429-146/8e4b340e`,
  pull request #1736, about 3,000 added lines over 25 files, and the parent item
  is still in progress in the tracker's export.

What does exist on `main` is the three-round count this item is to copy:
`runstate.MaxUpToDateConflicts` and `State.UpToDateConflicts` in
`internal/runstate/personmerge.go`, used by
`internal/orchestrator/personmergeupdate.go`. That is the pattern, not the
record the new count has to sit beside.

## Why the run did not build it anyway

Every part of the item attaches to the parent's code: the decision is recorded
on the conflict that code reports, the count sits beside its update record, and
the merge lands through its update path. Doing the work here would mean carrying
the parent's whole change in this one, which is the change the development
manager split this item out of because it was already too large for one review.

The item's own notes say an item whose text says it builds on the parent's
change is held until that change lands. The run started anyway, so that hold
did not apply to it.

## What releases it

The parent change landing on `main`, however it lands: its pull request merged,
its branch cherry-picked, or the work rebuilt and the parent closed. A run cut
after that can do the item as written.
