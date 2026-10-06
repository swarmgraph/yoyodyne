# yoyodyne-hfi: the decided repairs failed because a Codex session was handed to Claude Code to resume

The first two decided repairs the harness carried out after the ordering rule
landed (yoyodyne-ifd.428.81, 23:52 PDT October 5) both failed at developing
seconds after they started:

- removing unused orchestrator fakes (yoyodyne-ifd.429.13.6), run
  `run-2bc819a2dd6233b5daf3393e4768b961`, failed at 01:29 PDT October 6;
- Codex token usage display (yoyodyne-ifd.435.16), run
  `run-9b9cf80b1c288b8adc65419e9e1b2242`, failed at 02:48 PDT October 6.

Both have the same cause. Each run's developer had worked in a Codex session.
On October 5 at 15:39 PDT the project's configuration moved the developer from
Codex to Claude Code (commit `a04888b5`, "developers and reviewers on Opus
(medium), off Codex Sol"). When the repair was carried out, the harness asked
Claude Code to resume the Codex session's id. Claude Code has no session by
that id, said so, and exited with status 1. Nothing about the work, the kept
session, or the sandbox was involved.

## What ended each run

### Removing unused orchestrator fakes (yoyodyne-ifd.429.13.6)

The run record is `runs/run-2bc819a2dd6233b5daf3393e4768b961.json` under the
product's state directory, with its event log beside it.

The record says the run's developer worked on Codex:

```
backend : "codex"
config_revision : "cfg-b72de31c9551"
```

Every developer attempt in the event log is the same Codex session:

```
3   2026-10-04T11:20:37Z run.started codex {"session_id": "01a106a4-f7b1-7ff2-8622-808291c0c587"}
339 2026-10-04T12:32:44Z run.started codex {"session_id": "01a106a4-f7b1-7ff2-8622-808291c0c587"}
863 2026-10-04T13:44:31Z run.started codex {"session_id": "01a106a4-f7b1-7ff2-8622-808291c0c587"}
```

The run stopped on October 4 after its third review asked for repair. The
development manager decided one more repair, and the harness carried it out at
08:29:39 UTC October 6 (01:29 PDT), as the record's `repair_continuations`
entry shows (`"continued_at": "2026-10-06T08:29:39.200017Z"`). The next two
events, eight seconds later, are the whole of that attempt, and both come from
Claude Code, not Codex:

```
1445 2026-10-06T08:29:47.479Z process.output claude-code {"stream": "stderr", "text": "No conversation found with session ID: 01a106a4-f7b1-7ff2-8622-808291c0c587"}
1446 2026-10-06T08:29:47.561Z process.output claude-code {"is_error": true, "provider_subtype": "error_during_execution", "provider_type": "result", "total_cost_usd": 0, ...}
```

The record then ends:

```
failure : "developer reported failure: process_exit_1"
stop_class : "provider"
repair_attempts : 3
```

The scheduler's log records the carry-out the same way:

```
yoyodyne-ifd.429.13.6: failed
  chosen because Triaged: the development manager's triage decided a repair of the stopped work of run run-2bc819a2dd6233b5daf3393e4768b961, ...
  failed: developer reported failure: process_exit_1
carried out the "repair" the development manager decided about yoyodyne-ifd.429.13.6, on the stopped work of run run-2bc819a2dd6233b5daf3393e4768b961
```

### Codex token usage display (yoyodyne-ifd.435.16)

The run record is `runs/run-9b9cf80b1c288b8adc65419e9e1b2242.json`.

```
backend : "codex"
config_revision : "cfg-74fd222fa5f3"
```

Its developer attempts were all one Codex session:

```
3   2026-10-05T07:58:11Z run.started codex {"session_id": "01a10b11-fe19-78d2-a2c2-08b29dc27352"}
462 2026-10-05T08:42:50Z run.started codex {"session_id": "01a10b11-fe19-78d2-a2c2-08b29dc27352"}
845 2026-10-05T09:17:23Z run.started codex {"session_id": "01a10b11-fe19-78d2-a2c2-08b29dc27352"}
```

The repair was carried out at 09:48:00 UTC October 6 (02:48 PDT;
`"continued_at": "2026-10-06T09:48:00.514055Z"`), and five seconds later:

```
1207 2026-10-06T09:48:05.418Z process.output claude-code {"stream": "stderr", "text": "No conversation found with session ID: 01a10b11-fe19-78d2-a2c2-08b29dc27352"}
1208 2026-10-06T09:48:05.437Z process.output claude-code {"is_error": true, "provider_subtype": "error_during_execution", "provider_type": "result", "total_cost_usd": 0, ...}
```

```
failure : "developer reported failure: process_exit_1"
stop_class : "provider"
```

The scheduler's log has `yoyodyne-ifd.435.16: failed` with the same
`failed: developer reported failure: process_exit_1` line, followed by
`carried out the "repair" the development manager decided about yoyodyne-ifd.435.16`.

## Both runs had a session, and it was still there

Both Codex sessions were kept and are still on disk where Codex keeps them:

```
~/.codex/sessions/2026/10/04/rollout-2026-10-04T04-20-37-01a106a4-f7b1-7ff2-8622-808291c0c587.jsonl
~/.codex/sessions/2026/10/05/rollout-2026-10-05T00-58-10-01a10b11-fe19-78d2-a2c2-08b29dc27352.jsonl
```

So the session was not lost and was not refused by the provider that owns it.
It was offered to the wrong provider.

## Where the cause is in the code

A developer attempt is made through `Pipeline.Backend`
(`attemptDevelopment`, `internal/orchestrator/pipeline.go`). That backend is
built once, when the process starts, from the developer agent in the
configuration then current (`pipelineFrom`, `internal/cli/run.go`:
`providerBackend(cfg, agentForRole(cfg, domain.RoleDeveloper).Backend, ...)`).
The run's own record of which backend it ran on (`State.Backend`) is read only
to settle the effort level (`developerEffort`), never to choose who is invoked.

A repair continuation passes the run's recorded session id to that backend. The
check that decides whether a continuation can go ahead (`continuableRepair`,
`internal/orchestrator/repaircontinue.go`) refuses a run that recorded no
session, but never asks whether the session belongs to the backend the harness
is about to invoke. So any run whose developer ran on one provider, continued
after the configuration moved the developer to another, sends one provider's
session id to the other.

## It also erased the session from the record

When the failed attempt returned, `recordDevelopment` (`pipeline.go`) wrote the
attempt's reported session over the run's record:

```go
a.state.ProviderSessionID = providerResult.SessionID
```

Claude Code failed before opening a session, so it reported none, and the
run's record lost the Codex session id. Both records now carry no
`provider_session_id`, though the session itself is intact.

That is what the "recorded no developer session" refusals were. Three more runs
hit the same Claude Code error, and two of them are the refusals the program
manager's note treats as a separate kind:

| Work item | Run | Failed (PDT) | Session it tried |
|---|---|---|---|
| validating developer-slot and reviewer pairs (yoyodyne-ifd.435.14.1) | `run-d8a35b0a994ac33839db6447b95db488` | 16:24 October 5 | `01a10e32-a7d0-7163-a08f-ddfa5cfa2284` |
| automatically approved documents from a conversation (yoyodyne-ifd.433.21.1) | `run-8fd6e02f8e757a146de6244431c796d3` | 19:54 October 5 | `01a10d83-d867-7cf2-aede-ac27d4c1588b` |
| removing unused orchestrator fakes (yoyodyne-ifd.429.13.6) | `run-2bc819a2dd6233b5daf3393e4768b961` | 01:29 October 6 | `01a106a4-f7b1-7ff2-8622-808291c0c587` |
| Codex token usage display (yoyodyne-ifd.435.16) | `run-9b9cf80b1c288b8adc65419e9e1b2242` | 02:48 October 6 | `01a10b11-fe19-78d2-a2c2-08b29dc27352` |
| the stoppage stream carries every stopped run (yoyodyne-5v6) | `run-03ae7d4a3c34be22ed780be3fbd9c080` | 04:01 October 6 | `01a101ff-cd2d-7263-8bc1-8d4856d732c4` |

Each is a Codex run, each event log ends on `No conversation found with session
ID` from `claude-code`, and each record now has no session id. Every later
decision to repair `run-d8a35b0a…` and `run-8fd6e02f…` was then refused with
"recorded no developer session, so a continuation could not be the same
developer carrying on with the change it made", which is true of the record and
false of the world. The refusal of yoyodyne-5v6 at 05:05 PDT followed its 04:01
failure the same way. These five are the only event logs under `runs/` that
contain the error.

## Whether the ordering change caused it

No. The first of the five, validating developer-slot and reviewer pairs
(yoyodyne-ifd.435.14.1),
failed this way at 16:24 PDT October 5, seven and a half hours before the
ordering rule landed. The ordering rule made decided repairs run sooner, so
more of them reached the defect in one night; it did not add it. The kept
sessions are not at fault either: both are intact. The trigger was outside the
harness, a configuration change the operator directed, but the defect is the
harness's: a continuation does not check, or follow, the backend its session
belongs to.

Every Codex run that stopped before 15:39 PDT October 5 with its session kept
will fail the same way when a repair or stall continuation is carried out for
it, and each failure spends the repair attempt it was granted
(`repair_attempts : 3` on the fakes run) and erases the session id. Runs
started after the change ran on Claude Code and are not affected, so this ends
on its own once those Codex runs are settled, and comes back on the next change
of developer backend.

## What the records do not show

What ended these runs is in each run's event log and nowhere else. Checked
against recording the cause for runs that fail before their first check
(yoyodyne-ifd.428.85):

- The run record's `failure` is `developer reported failure: process_exit_1`.
  The line that explains it, Claude Code's `No conversation found with session
  ID: …` on standard error, is not in the run record, the scheduler's log, or
  anything the development manager is shown. She read the exit as "the stop
  came from outside the work" and spent a re-run on the fakes item as a result.
- The run record does not say which backend the failed attempt was made on. Its
  `backend` still reads `codex`, and the only sign that Claude Code was invoked
  is the `source` field of the last two events.
- The run record no longer says which session the attempt tried to resume,
  because the failure erased it (above).
- The run record's `provider_model` reads `opus` on a Codex run. A run that
  recorded no model of its own asks for the configured developer's model
  (`developerModel`), and the attempt writes that into the record before it
  runs, so the record names the model the failed Claude Code attempt asked for
  under a backend that was never invoked.

A record that answers these would carry, for the failed attempt, the backend
invoked, the session it was asked to resume, and the provider's last error
line.

## Fixes this points to

Both are bounded, and neither is made here.

1. Before a continuation resumes a session, compare the run's recorded backend
   with the backend the harness would invoke. Where they differ, either invoke
   the backend the run recorded (if this build can launch it), or refuse the
   continuation before anything is spent, saying the session belongs to the
   other provider and that a fresh run is what the item can have. The check
   belongs beside the session check in `continuableRepair`, and the stall and
   check-stage continuations need the same guard. Per-slot developer routing
   with cross-provider failover (yoyodyne-ifd.435.14) will make backend changes
   between attempts routine, so it needs this too.
2. In `recordDevelopment`, keep the recorded session when an attempt reports
   none, as `carrySession` already does elsewhere in `pipeline.go`, so a failed
   resume cannot erase a session that still exists. The five records above
   would then need their session ids restored from their own event logs before
   their repairs could be carried out.
