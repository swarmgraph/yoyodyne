# yoyodyne-06m: a run parked on a tracker read was ended for sitting still

Preserved-run recovery (yoyodyne-06m) carries a case from the run of stopped-run
delivery (yoyodyne-5v6), run-03ae7d4a3c34be22ed780be3fbd9c080. At 07:16 PDT on
October 3 it parked because the tracker did not answer the read of what its
item waits on (the read was cancelled). At 07:49 PDT reconciliation ended it,
with its saved phase at reviewing and its branch and worktree kept. The item's
notes ask that a wait like this not be ended on age alone, and that the actual
transition be traced before anything changes. This note is that trace.

## What ended it

A run that cannot get an answer from the tracker at one of its gates records a
park (`recordTrackerPause` in `internal/orchestrator/pipeline.go`) and its
process exits. Nothing in the harness took such a park up again: the scheduler
chooses from what the tracker calls ready, and a claimed item is not, and the
watching session re-adopted only runs a redeploy had put down. So
reconciliation treated it as a park nothing serves (`parkNothingServes` in
`internal/orchestrator/reconcile.go`) and, once its record had not moved for the
grace of 30 minutes (`DefaultVanishedGrace`), ended it as a run whose process
vanished (`settleVanished`). 07:16 to 07:49 PDT is that grace and the next sweep.

It is the same shape the dependency wait had until a watching session's pull
was made to continue it (yoyodyne-ifd.428.51), and not the cause of the
redeploy incident this item also covers: there, the run was tried at every
pull and refused before anything was written.

## What changed

A watching `yoyo work` session now re-adopts a run parked on a tracker read at
its next pull, beside the runs a redeploy put down (`schedule.go`). The
dispatch reads the tracker before it takes the run, so a tracker that still does
not answer leaves the run parked, to be tried again at the next pull, with
nothing written. A tracker that answers continues the same run from its
recorded phase, with every counter as it was. Reconciliation no longer ends such
a park on age; it reports it as resumable and leaves it to the pull. A park on
a tracker write is unchanged: the process holding the run waits on it, and one
whose process died is still settled after the grace.

`TestTheSweepLeavesATrackerReadParkToThePull` holds the sweep to this, and
`TestAWatchingSessionReadoptsARunParkedOnATrackerRead` the pull. Where a run
cannot be continued even once the tracker answers, the development manager
records an unresolved hold on it with `run.recover`, and neither reconciliation
nor the claim audit ends a run carrying one.

The run of stopped-run delivery itself was already ended before this change,
and its recovery stays with the development manager on that item.
