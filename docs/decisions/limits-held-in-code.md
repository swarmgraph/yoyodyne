---
id: limits-held-in-code
kind: decision
title: Which limits stay in code and which a project sets
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-15T04:41:59.938525Z
      reason: Records the November 11 ruling on which limits held in code stay there and which a project sets inside bounds held in Go, checked against the per-agent time limits work that has since landed, the per-agent counts and reviewer copy items, and the run stops guide at eaa9f53c.
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-15T04:41:59.938525Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2766, for document-2766.1 under the automatic approval policy
---
# Which limits stay in code and which a project sets

Decided by the architect on November 11, 2026 at 10:25 PST and recorded on November 13, 2026, for the ruling on limits held in code (yoyodyne-ifd.429.157). On November 11 the operator learned that the reviewer's 256 KiB limit is fixed in code. He asked whether other values like it are. This record answers limit by limit, from [what stops a run](../run-stops.md) as it stood at commit eaa9f53c.

## The rule

A limit stays in code when either of these is true:

- it protects review, evidence, safety, or the integrity of the harness's records;
- it bounds how many times the harness or a role may decide the same thing.

Changing one of those changes what the harness promises, not how one project runs, so no configuration reaches it.

Any other time or size that suits one project and not another is set by the project, inside a lower and an upper bound held in Go:

- An unset value is exactly today's value.
- A value outside its bounds is refused when it is written and when the configuration loads. The refusal names the setting, the value and the bound.
- The development manager may change these values through the same typed write and reviewed path that section 6 of [portable agent configuration](../designs/portable-agent-configuration.md) gives every configurable value.
- No run carries limits of its own.

This is the pattern [the reviewer's size limits](review-size-limits.md) set on October 31.

## Already set by the project

These need no new work:

- each check's time and the whole check stage's time;
- the developer's provider call and the reviewer's invocation (`time_limits.provider_call` and `time_limits.review`);
- every conversation role's turn and message times;
- relaunches after a provider dies;
- the usage pause and the probe intervals;
- the shared repair budget and charged replays;
- the redeploy drain;
- developer capacity;
- the failure brake;
- account allowances;
- the triage review-round ceiling;
- recurring task turns;
- landing check time;
- the factory stall alarm.

## Newly set by the project

| Limit | Today | Setting | Bounds | Why these bounds |
| --- | --- | --- | --- | --- |
| Work item context | 256 KiB | `execution.work_item_context_kib` | 128 to 512 KiB | Below 128 KiB an item's design and notes stop fitting. Above 512 KiB the opening context crowds out the developer's room to work. |
| Tracker command time | 120 seconds | `execution.tracker_command_timeout` | 30 seconds to 10 minutes | Below 30 seconds an ordinary listing fails on a large store. Above ten minutes a command that hangs holds a pass or a pull too long. The retries around it stay in code. |
| Local Git command time, before scaling for load | 30 seconds | `execution.git_command_timeout` | 10 seconds to 5 minutes | Scaling for load, the time added per file and per commit, and the three retries of a replay stay in code and apply to the value set. |
| Push time | 5 minutes | `execution.push_timeout` | 1 to 30 minutes | A large first push needs more than a minute. A push still running after half an hour has failed. |
| Forge command time | 1 minute | `execution.forge_command_timeout` | 30 seconds to 10 minutes | Slow forges answer in tens of seconds. Longer than ten minutes holds the merge queue. |
| Provider silence | 5 minutes, in both adapters | `agents.<name>.time_limits.silence` on the developer and the reviewer | 2 to 30 minutes, and shorter than that agent's call limit | Below two minutes a long tool call reads as silence. Above thirty minutes a session that has stopped holds a slot. |
| Dead claim | 30 minutes | `execution.dead_claim_after` | 15 minutes to 4 hours | Shorter releases claims on work that is only slow. Longer leaves ready work unclaimed. |
| Grace for a parked run whose process never returned | 30 minutes | `execution.vanished_run_grace` | 15 minutes to 4 hours | As for the dead claim. |
| Wait for the promotion lease | 15 minutes | `execution.promotion_wait` | 5 minutes to 2 hours | A project with long merge checks waits longer before failing a run. |
| Transport recovery window | 2 hours | `execution.transport_recovery_window` | 2 to 24 hours, and it may only be raised | The operating rule that a run is never killed for a flaky network or a sleeping laptop rests on this window, so no project may shorten it. The delays inside it stay in code. |

## Stay in code

| Limit | Value | Why it stays |
| --- | --- | --- |
| Everything the reviewer is shown | 768 KiB | Protects review: the evidence must arrive whole. |
| Changed paths listed to the reviewer | 1,000 | Protects review, inside the same 768 KiB. |
| Run state record and run event | 1 MiB each | Integrity of the harness's records. |
| Run state schema | as written | Integrity of the harness's records. |
| One more request for a developer's account, and for a readable review | one each | Bounds a decision: a second refusal is the answer. |
| Triage actions | one repair grant and one re-run per item, one merge re-arm per publication, five delegated crossings, sixteen override records | Bound decisions. |
| Document publication returns | three | Bounds a decision loop for the owning role. |
| Check reruns and harness continuations | `MaxCheckStageContinuations` two, `CheckRerunAllowanceFactor` three, `MaxHarnessStallContinuations` one | Bound what the harness decides on its own before a role is asked. |
| Tries of an install step | three, five minutes apart | Bounds what the harness decides on its own before the development manager is asked. |
| Background work after a final reply | 5 minutes | Ends work the sandbox started. A safety limit, not a pace. |
| Window that counts a run as active | 1 hour | Evidence the dead claim check reads. Moving it would let a claim be judged on less evidence. |
| Developer settings check | 30 seconds a question | Safety: the sandbox and the notes guard must be confirmed before a developer starts. |
| Pass message and operator message | 256 KiB and 32 KiB | Keep a pass inside the provider window, so the role contract, intent and memory are never dropped to make room. |
| Recurring task prompt | 16 KiB | Same reason. |
| MCP call limits | 32 KiB a result, 64 KiB a reply, 5 calls a reply, 60 seconds a call; for developers, 8 replies that ask and 20 calls an attempt | Bound outside data handed to a session. Configuration may only lower them, as it can today. |
| Scope, evidence and independence checks | as enforced | Safety. No configuration or workflow reaches them. |

## Covered by other admitted work

- **Per-agent time limits (yoyodyne-hv1w), landed:** the developer's call, the reviewer's invocation, and each conversation role's turn, message, Slack turn, exchange answer and side thread. Provider silence joins those settings here.
- **Per-agent counts (yoyodyne-aexm):** tool rounds per message, actions per reply, the Lead Product Manager's sweep caps, side threads, and the needs-design wake. Counts in it that bound a safety rule keep that rule whole, as that item already requires.
- **The reviewer's copy limits (yoyodyne-ifd.429.116):** the 256 KiB diff, 64 KiB per file and 200 files. The 768 KiB total stays fixed.

## Limits nobody has listed yet

The run stops guide is the only list of these limits, and nobody has searched the code for limits outside it. Build piece 4 does that search:

- It classifies each limit it finds by the rule above.
- It adds each one to the guide's tables and to the guide's table of pinned source expressions.
- It names any limit the rule leaves unclear in a report to the architect, rather than deciding it.

## Alternatives rejected

- **Make every limit configurable.** That lets a project switch off what review and the records depend on, which the configurability goal forbids.
- **Keep every limit in code.** Projects differ in tracker size, network speed and repository size. A limit set for this repository then fails work on another, against the goal of working on other people's projects.
- **Make the bounds themselves configurable.** That is a new question. The operator's ask was answered by values inside bounds.
- **One project-wide provider silence value.** Silence is set per agent, beside the call limits, because a reviewer and a developer wait on different kinds of work.

## Build pieces

1. The four command times (tracker, local Git, push, forge) are read from configuration with the bounds above, refused when written and loaded, and changeable through the development manager's typed write.
2. The dead claim, the parked run grace, the promotion lease wait and the transport recovery window are set the same way. The window refuses any value below two hours.
3. Provider silence is read from each agent's time limits in both adapters. It is refused when it is not shorter than that agent's call limit.
4. The code search above. It adds every limit it finds to the run stops guide, classified by this rule.
5. Work item context size is read from configuration. A test measures the largest work item context this repository has sent against the lower bound.

Each piece updates the run stops guide row it changes, from fixed in code to its setting, and the configuration guide entry giving the key, its default and its bounds. Tests cover the default, a set value, and the refusal at each bound.
