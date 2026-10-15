# yoyodyne-06m: the development manager's recovery operation cannot be built before the run inspection it rests on

Preserved-run recovery (yoyodyne-06m) is to give the development manager
`run.recover`, the one operation that carries out her recovery decisions:
continue the same run, wait on dependencies, finish a merge, release the
reservation of a finished run with no worker, and record an unresolved hold.
The design that defines it,
[fresh-factory-health-and-preserved-run-recovery](../designs/fresh-factory-health-and-preserved-run-recovery.md),
places it third of six pieces and says it depends on the second: worker
identity recorded when a worker starts, with `run.inspect` and `run.verify`.
Those, and the first piece, `health.observe`, belong to shared health delivery
(yoyodyne-e7c), which is open. This note records what the build found on main at
e9ad26da, so whoever picks the item up again starts from it.

## What `run.recover` needs that does not exist yet

Every part below is a rule the design says is enforced in code, and each one
reads something only the second piece (or the first) produces.

- **A worker is live only when a process matches four things**: its process
  number, the start time the operating system reports for it, the build it
  runs, and the run and attempt number it recorded when it started. The
  record a run's holder writes today, `runHolder` in
  `internal/runstate/presence.go`, carries the process number and the time the
  lease was taken, and nothing else. A check made from it cannot tell a
  worker from an unrelated process that was given the same number later, which
  is the case the design forbids acting on. Refusing every act "while a worker
  is live or uncertain" is therefore not something `run.recover` can decide
  honestly until the worker records the other three.
- **A request is bound to an observation**, by the identifier
  `health.observe` returns, and refused when that observation is older than
  fifteen minutes. No code mints an observation identifier or keeps its time,
  so there is nothing for a request to name and nothing to compare against.
- **Applied means read back, and outcome is checked with `run.verify`.** The
  design defines "applied" through the same reading `run.verify` makes, and
  the development manager's follow-up after any applied result is a
  `run.verify` call. Building the reading inside `run.recover` first would be
  the second account of a run's state the design rejects, and it would have to
  be thrown away when shared health delivery builds the real one.
- **The grant** puts `run.recover` in the development manager's bundle beside
  the four reads. None of the five capabilities is registered in
  `internal/capability` today, and the tool interface's first slice (log
  reader, yoyodyne-ifd.430.19, closed) is the only conversation tool of the act
  or read classes built so far.

## What does not wait

Two parts of the item's notes do not read worker identity or an observation,
and could be built before shared health delivery lands if the development
manager splits them out:

- **The dependency wait that keeps the interrupted phase.** The path that
  records a dependency pause (`recordDependencyPause` in
  `internal/orchestrator/pipeline.go`) moves a run at developing on to
  checking, which is right when the developer's attempt finished and wrong for
  an interrupted one. And, as the item's notes record from the incident, the
  dependency check made before an existing run is adopted refuses it without
  writing the pause, so the run keeps counting against developer capacity. The
  notes ask for the wait to be written at the exact interrupted phase, and
  capacity released only once it is written. This note did not re-trace that
  refusal in code; whoever builds the piece diagnoses it first.
- **One decision on whether a redeploy-stopped run may continue**, shared by the
  scheduler, reconciliation and the claim audit. Stopped-run recovery names
  this as its own piece (one function deciding whether a paused run can resume,
  yoyodyne-0rm), so it belongs with the build of stopped-run recovery
  (yoyodyne-ifd.428.99) rather than here.

## What releases the item

Shared health delivery (yoyodyne-e7c) landing its first two pieces: a worker
that records its four-part identity at start, `health.observe` with observation
identifiers and their times, and `run.inspect` and `run.verify`. `run.recover`
is then built on top of them as the design's third piece, with repair, rerun,
giving the item back and asking the Lead Product Manager refused as not yet
built until the build of stopped-run recovery adds them.
