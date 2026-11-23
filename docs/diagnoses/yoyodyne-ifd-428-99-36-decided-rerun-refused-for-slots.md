# yoyodyne-ifd.428.99.36: why the guides re-run read as refused for full developer slots

The development manager decided a re-run of the guides that list every reason
for naming the operator (yoyodyne-ifd.432.25.15) about the stopped run
run-380d6f6a. From 02:44 PST on November 21 it read as refused with "every
developer slot is occupied: 4 active, limit 3", and it still read that way on
November 22, after the configured limit had dropped to 2 and while health
readings counted a free slot. The work item asked where the 4 came from and for
the re-run to be refused only when the run records show every slot full. All
times are PST.

## Where "4 active, limit 3" came from

The words are written by `capacityWaitSays` in `internal/orchestrator/rerun.go`.
The figures come from the re-run action's own reading of the slots,
`Rerunner.Rerun` calling `slotIsFree`, which counts the runs in the run records
that hold a developer slot, by the same rule the reservation counts by, against
the configured limit. At 02:44 on November 21 that reading found 4 runs holding
slots against the limit then configured, 3, so the action reported a full
harness, and the scheduler's carry-out wrote that refusal onto the item's triage
record.

That was the only time the re-run was asked about slots. The refusal still says
"limit 3" on November 22 because nothing has asked since, not because some count
still reads 4.

## Why nothing asked again

The re-run decision stopped standing. Between 02:44 and 04:11 the development
manager recorded two waits about the same run, at turns 4103 and 4108 of her
conversation, each naming the work it waited on, the slot-count fix
(yoyodyne-ifd.428.99.31). She meant them for the run's older pull request,
which has its own docket entry; her notes say the re-run decision "stands and
is not yet carried out".

The item's triage record keeps one decision per run, whichever of the run's
docket entries it answers (`recordDecision` in
`internal/runstate/triagedecision.go`). A wait that names no work item leaves a
repair or re-run still to be carried out standing, but a wait that names one
replaces it (`Store.RecordTriageDecision`). So the second wait replaced the
re-run. The record now holds two waits and no re-run, beside the leftover
refusal from 02:44, and the scheduler has logged on every pass since that the
item is held because she decided to wait. She was told only that she was
waiting; nothing told her the re-run was gone.

## What already holds about counting the slots

- The re-run action counts from the run records each time it is asked
  (`slotIsFree`), and the reservation that would refuse the run it starts
  counts from the same records.
- The scheduler holds back a decision a full harness refused only while its own
  count shows no free slot and the run records also show every slot held
  (`capacityGateShut` and `recordedSlotsFull` in
  `internal/orchestrator/schedule.go`), from the fresh run from a kept change
  fix (yoyodyne-ifd.428.99.24), in the build the scheduler ran.
  `TestADecidedRerunRefusedForAFullHarnessIsTriedOnceTheRecordsFreeASlot`
  covers it.

So no count is kept apart from the one place that admits starts, and nothing
needed moving there.

## What this change does

- `TestADecidedRerunStartsWhenTheRecordsShowAFreeSlotDespiteAStaleRefusal`
  (`internal/orchestrator/rerunstalecount_test.go`) leaves "4 active, limit 3"
  on the item's record and one of two slots held on record, and the re-run
  starts. Its neighbour shows a refusal with both slots held counting today's
  figures, and a third shows the wait naming work leaving no re-run to carry
  out.
- When a decision she records replaces a repair or re-run not yet carried out,
  the store now says which (`TriageCounters.Replaced`, never saved), and what
  she is told says the harness will not carry it out and that she decides it
  again if she meant it to stand (`replacedStanding` in
  `internal/chat/triage.go`; `docs/conversation.md`).

## What is still open

- The guides re-run starts only if the development manager decides it again.
  The item's re-run budget reads 2 of 2 spent with none carried out, because a
  replaced re-run keeps what it spent, so that decision meets the cap.
- Whether decisions should be kept per docket entry rather than per run, so
  that an answer about a run's pull request cannot end a decision about its
  stoppage, is a design question for the architect.
