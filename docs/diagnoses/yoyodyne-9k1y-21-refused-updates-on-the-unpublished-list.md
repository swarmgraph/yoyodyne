# yoyodyne-9k1y.21: what the landed list of unpublished pull requests hides that the ruling says it must show

The change that leaves refused updates off the development manager's list of
unpublished pull requests (yoyodyne-9k1y.6.6, commit c202c94bcea4) was read
against the architect's ruling on that list, points 4 to 6 of
[naming an epic again](../designs/naming-an-epic-again.md), as published for
the item that corrects it (yoyodyne-9k1y.19). The code read is
`updateRefused` and `withoutRefusedUpdates` in
`internal/orchestrator/triage.go` at main e0cc684e66, which are unchanged since
that commit.

## Where the code and the ruling differ

The ruling hides a change only while an update of it is due and epic mode
refuses that update. It takes the refused start in `not-started.json` as the
only saved fact showing that an update is due, and hides a change only where
the file can be read, it has an entry for the item under the path that fits the
change, the entry names this run, the entry was last refused no more than an
hour ago, and `Decide` refuses the update now.

The landed code never asks whether an update is due. It hides every queued or
person-merged change whose item is outside the named epics, past the listing
age, not closed, and with no failed merge, as soon as `Decide` refuses the
update. So it also hides:

- a change queued on GitHub whose head is level with its target and whose
  merge was not dropped, which is stuck for another reason;
- a change waiting for a person's merge whose head is level with its target;
- a queued change whose GitHub checks could not be read, where the harness
  cannot tell whether an update is due.

It does not read `not-started.json` at all, and the entries in it carry no run
(`RefusedItem` in `internal/maystart/refusals.go` has no such field), so the
code cannot tell the cases apart from what is saved today.

## Why this item does not fix it

The ruling's fix is not confined to `triage.go`. It needs the refused start to
name its run, a removal function beside `KeepUpdateOf` in
`internal/maystart/refusals.go`, and every outcome of the checks in
`Reconciler.updateQueuedHead`, `replayDroppedLanding` and
`Reconciler.updatePersonMerge` that finds no update due, or cannot tell, to take
the entry away. That is the work of yoyodyne-9k1y.19, which the ruling was
written for and which names `triage.go` as its conflict surface. Building it
here as well would make two changes to the same code from two runs.

## Edits the landed change made that its item did not ask for

None were found. Every file the change touched belongs to its item: the listing
code and its wiring (`internal/orchestrator/triage.go`, `internal/cli/run.go`),
their tests (`internal/orchestrator/docketepics_test.go`,
`internal/cli/namedepics_test.go`), and the two documents that described the
listing (`docs/operations.md`, `docs/conversation.md`). The paragraph it added to
`docs/operations.md` describes the landed behaviour, so it will need the same
correction as the code when yoyodyne-9k1y.19 lands.
