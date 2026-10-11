# yoyodyne-hea: continued Codex runs are launched with no model, so Codex refuses them

Two decided repairs failed at launch, seconds after they started, with the same
error:

```
developer backend failed: Codex model "" does not accept effort "low"; accepted values: no effort level
```

- the fix that routes failing scheduled passes (yoyodyne-ifd.428.76), run
  `run-88804f0e11b1336a78d1821c1b334e91`, continued at 13:46 PDT October 8 and
  failed at 13:47 PDT;
- the item that makes the scheduler record why it passes over ready work
  (yoyodyne-37h), run `run-a33c597d05937cb24222068fd34f2c6a`, continued at
  21:23 PDT October 10 and failed four seconds later, its change kept on pull
  request #1027.

The work item asking for this diagnosis says both runs began on Claude and were
moved to Codex. The records say otherwise: both runs began on Codex, every one
of their invocations was on Codex, and the provider never changed. What changed
is the model. Since backend pinning (yoyodyne-ifd.428.88) landed on October 7, a
repair of a run whose developer worked on a backend other than the one the
developer is configured for now asks that backend for the model the provider
*reported* serving, and Codex never reports one. The model comes out empty, and
the Codex adapter refuses any effort level for a model it cannot name. The
effort "low" was not carried over from Claude: it is the Codex default for
`gpt-6.1-sol`, the model both runs worked on, recorded when each run started.

## What the records show

The run records are `runs/<run id>.json` under the product's state directory
(`~/Library/Application Support/Yoyodyne/state/products/yoyodyne`), with their
event logs beside them. The per-invocation records are `spend.jsonl` in the same
directory.

### Both runs began on Codex and stayed there

Both records name Codex as the run's backend, and both were reserved under the
configuration revision in force on the morning of October 5, when the developer
was configured as Codex `gpt-6.1-sol` with no effort set:

```
run-88804f0e…  backend "codex"  config_revision "cfg-74fd222fa5f3"  started 06:17 PDT October 5, developer slot 4
run-a33c597d…  backend "codex"  config_revision "cfg-74fd222fa5f3"  started 00:29 PDT October 5
```

The configuration moved the developer to Claude Code (`opus`, effort `medium`)
later that day, at 15:39 PDT (commit `a04888b5`), after both runs had stopped.

Every developer and reviewer invocation either run made is recorded in
`spend.jsonl` as Codex `gpt-6.1-sol` at effort `low`, and every developer
session id in the event logs is a Codex thread id
(`01a10c37-729e-7f22-8821-315f2280a8bc` and
`01a10af8-2b40-74a0-88da-d6ba347a8c3c`). The last working attempts were at
07:48 PDT and 02:15 PDT October 5.

The two failing attempts are recorded in `spend.jsonl` as Codex with effort
`low` and **no model at all**:

```
2026-10-08T20:47:10Z  developer  repair  backend codex  (no model)  effort low  build 9f1f1c00
2026-10-11T04:23:08Z  developer  repair  backend codex  (no model)  effort low  build c95f6af8
```

Both builds contain backend pinning (commit `d5eb27e3`, 04:36 PDT October 7).
Both continuations were decided repairs (each record's `repair_continuations`
entry, `continued_at` 2026-10-08T20:46:48Z and 2026-10-11T04:23:05Z).

### Why the model came out empty

`activeRun.developerModel` in `internal/orchestrator/pipeline.go` chooses the
model for every developer invocation of a run:

1. the run's recorded `developer_model`, which is written only by a project's
   `execution.developer_models` label mapping or by a developer-slot endpoint
   pair. This project configures neither, so neither run has one;
2. otherwise, for a run whose backend differs from the developer's configured
   backend, the provider's reported model, `provider_resolved_model`. This
   branch was added by backend pinning;
3. otherwise, the developer's configured model.

Both runs fall into the second branch: recorded backend `codex`, configured
backend `claude-code`. The Codex adapter fills the reported model only from the
older Codex event `session_configured`. The Codex CLI this project runs
(0.159.2) writes `thread.started`, which carries the session id and no model;
both runs' `run.started` events carry only `session_id`. So no Codex run in this
project has ever had a reported model, and the second branch always answers
empty for one.

The model the run actually asked for *was* on its record until the failing
attempt. `activeRun.attemptDevelopment` writes the model it is about to ask for
into `provider_model` before it calls the provider. Every October 5 attempt
wrote `gpt-6.1-sol` there; the failing attempt overwrote it with the empty
string before the adapter refused. Both records now carry no model anywhere.
The only surviving record of `gpt-6.1-sol` for these two runs is `spend.jsonl`.

### Why the effort was "low", and why it does not matter

Each run's record says `provider_effort "low"` with `effort_settled true`. The
developer had no effort configured when the runs started, and the harness then
resolved an unset Codex effort to the model's advertised default, which for
`gpt-6.1-sol` is `low` (`internal/backend/effort.go`). `activeRun.developerEffort`
reads a settled effort back off the record, so every attempt, including the two
failures, asked for `low`. Codex accepts `low` for `gpt-6.1-sol`.

The Codex adapter narrows the accepted levels to the model's catalog entry, and
an empty model has none, so it refuses every level. A throwaway test against
this build's descriptor confirmed it: for model `""`, the efforts `low`,
`medium`, and none at all are each refused; for `gpt-6.1-sol`, `low` and none
(resolved to `low`) are both accepted. The effort is named in the error only
because it is the check that runs first. Clearing the effort would not have let
either run launch.

### What did not choose Codex

- **Developer-slot endpoint pairs.** This project's `developer_slots` name only
  label preferences, no endpoint pairs, and neither run's record carries a
  routing snapshot. Nothing in the continuation path read a slot.
- **The slot-number change.** The developer slots went from four to three on
  October 6 (commit `cce65e8a`), and the first run was in slot 4. The backend
  and model for a continuation are read off the run's record, not its slot, so
  the change had no part in either failure.
- **A configuration reload.** The current configuration was read for one thing:
  whether the run's backend differs from the developer's configured backend. It
  does, since October 5 at 15:39 PDT, and that is what sent the model choice to
  the reported-model branch. The configuration did not choose Codex. The run's
  own record did, which is what backend pinning is for.

### Why the backend-pinning test did not catch it

The test in `internal/orchestrator/recordedbackend_test.go` records a Codex run,
reconfigures the developer to Claude Code, carries out a repair, and checks that
Codex is asked rather than Claude Code, and that Codex is not asked for the
Claude Code model. It never checks which model Codex *is* asked for, and its
fake Codex backend accepts an empty model. The real adapter refuses one.

## Established and not established

Established from the records and the code:

- both runs began, worked, and were continued on Codex; the provider never
  changed;
- the model was empty because a run on a backend other than the configured one
  takes the provider's reported model, which the Codex CLI in use never
  reports, and no project mapping or slot pair recorded one;
- the failing attempt erased the model the run had asked for on every earlier
  attempt, so both records now carry none;
- the effort `low` is the Codex default for `gpt-6.1-sol`, recorded at the
  start of each run, and the launch would have been refused at any effort.

Not established:

- where the work item's statement that both runs began on Claude came from. No
  record of either run names Claude Code; the status view showing the developer
  as configured for Claude Code since October 5 may have been read as the runs'
  provider, but nothing records that reading;
- whether Codex would resume these sessions under `gpt-6.1-sol` today. Nothing
  has tried since October 5. The developer was moved off Codex because its
  weekly limit was near, so a continuation on Codex also spends that limit.

## Other runs that would fail the same way

Any repair or continuation of a run that recorded backend `codex`, no
`developer_model`, and no `provider_resolved_model` asks Codex for no model and
is refused, as long as the developer is configured for Claude Code. That is
102 of the 105 run records that name Codex; the other three record a reported
model of `claude-opus-5-5`, so Claude Code served them whatever their record
says, and all three succeeded. Those still worth continuing
are the ones whose work item is unfinished and whose latest run is that Codex
run, with the change kept on an open pull request:

| Work item | Item status | Run | Pull request |
| --- | --- | --- | --- |
| The fix that routes failing scheduled passes (yoyodyne-ifd.428.76) | in progress | `run-88804f0e…` (already failed this way) | #1035 |
| The scheduler records why it passes over ready work (yoyodyne-37h) | in progress | `run-a33c597d…` (already failed this way) | #1027 |
| A repair that stops again is classified afresh (yoyodyne-ifd.428.74) | open | `run-9b58b617…` (stopped by the development manager) | #1025 |
| Separate scheduled passes do not wait behind one another (yoyodyne-ifd.428.75) | blocked | `run-5d9005f6…` | #1033 |
| Revisions marked consistent with intent (yoyodyne-ifd.433.19) | blocked | `run-1463eb20…` | #981 |
| Confined writers survive replacement races (yoyodyne-xdj) | blocked | `run-1953fb46…` | #994 |
| One ownership registry and resolver (yoyodyne-ifd.432.25.1) | blocked | `run-31b45611…` | #963 |
| A reply with one malformed block loses only that block (yoyodyne-ifd.430.17) | blocked | `run-7c1f7426…` | #959 |

A continuation of the stopped check run for checks held at their own time
limit (yoyodyne-ifd.428.37, `run-7d92c67b…`, cancelled, worktree removed, no
pull request) would fail the same way, though it holds little to continue.

Three unfinished items have an older Codex run like these but a newer Claude
Code run (yoyodyne-ifd.435.10, yoyodyne-ifd.434.6, yoyodyne-ifd.433.25); they
are exposed only if somebody chooses to continue the older run. A re-run of any
item is not affected: it starts a fresh run on the configured developer.

Every run in the table except the two that already failed still has
`gpt-6.1-sol` in its `provider_model`, until an attempt like these two
overwrites it.

## The bounded fix

It belongs to backend pinning (yoyodyne-ifd.428.88), which is closed. Its done
statement says a continuation uses "the backend and model … the run's record
names", and the model half is what failed. It does not belong to the Codex
effort item (yoyodyne-ifd.435.18): no effort, given or omitted, gets an empty
Codex model past the adapter. Nor to per-slot routing during continuation
(yoyodyne-ifd.435.14.4): that covers runs with a recorded endpoint pair, and
these runs have none, so it would leave them on the path that fails.

The fix, as one follow-up to backend pinning:

- a run on a backend other than the configured developer's asks for the model
  it was last invoked with on that backend: its `provider_model` where that is
  set, or else the model on its own most recent `spend.jsonl` line for that
  backend, which is the only place the two failed runs still name
  `gpt-6.1-sol`;
- an attempt does not write an empty model over a recorded one, so a refusal
  cannot erase what a later continuation needs;
- where neither source names a model, the continuation is refused before the
  repair is spent, the way backend pinning already refuses a backend this build
  cannot launch, so the development manager can choose a re-run;
- the backend-pinning test asserts the model Codex is asked for, against a fake
  that refuses an empty Codex model the way the real adapter does.
