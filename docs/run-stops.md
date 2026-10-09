# What stops a run or the carrying out of a decision

*For an operator or development manager reading why work cannot continue. Part of
[working on yoyo itself](developing-yoyo.md).*


This is the inventory of the bounds, fences, and guards that can end a developer
run, and of the limits beside them that stop work before or after a run. The
figures below are harness defaults, not this project's overrides; use
`yoyo config show --effective` to read a project's configured figures.
[Configuration](configuration.md) explains the settings, and the
[delivery baseline](delivery-pipeline-baseline.md) records the paths through them.

A **repair attempt** is another developer attempt, charged before the developer
is invoked. A **review round** is a substantive verdict sending a nonempty
change back; approvals, escalations, and findings consisting only of matters
outside the change spend none. A **repair grant** buys more attempts through
triage. Provider cost is real in every case where a provider ran, even when no
attempt, round, or grant is charged. A stop spends no additional repair attempt
unless the table says another attempt was invoked; already-spent attempts and
rounds stand unless [the rules for causes outside the work](#causes-outside-the-work)
return them.

A failed or cancelled run does not clean up its change. The harness checks and
reports whether the branch and checkout still exist rather than promising
preservation without looking. Successful integration permits cleanup only on
proof that the change landed; a queued forge merge keeps the artifacts until
that proof arrives. A later retirement of stopped artifacts is separate from
the stop itself.

## Bounds during a run

| Bound or guard | Setting and default | What it bounds | How the run ends | What it spends | Change |
| --- | --- | --- | --- | --- | --- |
| One check's time | `execution.check_timeout`: 30 minutes | Each configured check, including a path check. No load scaling of this individual budget. | `timed_out`, class `check-timeout`; no verdict on the change and no automatic check-stage continuation for this cause. | No repair for the timeout; earlier spend stands. | Preserved. |
| Whole check stage's time | `execution.check_stage_timeout`: 30 minutes | All checks in one stage, scaled upward for load, at most ten times the configured figure. The heaviest reading can raise the bound during the stage. | `timed_out`, class `check-stage-bound`. The harness can continue the same run at its checks twice, hard-coded by `MaxCheckStageContinuations`; after that the development manager gets the stop. | No review round, repair attempt, grant, or re-run for the bound or its continuation. | Branch, checkout, and developer session preserved. |
| Developer invocation's total time | Hard-coded: four hours in both provider adapters | One invocation, including its tool work; another invocation starts another clock. | A process times out. With resumable state the run stays `running`, awaiting continuation; without it the run ends `timed_out`, class `provider-budget`. An uncontinued stop is settled after the grace below, and a settled first stop is continued by the harness itself under the same `MaxHarnessStallContinuations` bound as a stall; a second is docketed. | Continuation re-enters the same attempt; no repair, grant, review round, or re-run. | Preserved, including the recorded session. |
| Provider silence | Hard-coded: five minutes in both adapters | Gap between output events while the provider has not written its final reply. | The process is `stalled`; resumable state leaves the run `running` with `ProviderStop`. With nothing to continue it ends `timed_out`, class `provider-idle`. One automatic continuation of a settled stall is allowed by `MaxHarnessStallContinuations`, shared with the total-time stop above; a second stall is docketed. | Continuation re-enters the same attempt; no repair, grant, review round, or re-run. | Preserved, including the recorded session. |
| Reviewer invocation's total time | Hard-coded: 15 minutes in `review.Reviewer` | One independent review, overriding the adapter's four-hour total; the five-minute limit on a session producing no output still applies. | A process timeout follows the same provider-stop continuation rule; a review with no resumable state ends on the provider failure. | No repair attempt for an unfinished verdict. | Preserved. |
| Transient provider deaths | `execution.transient_relaunches_before_blocking`: 2 | Relaunches shared by developer and reviewer in one run. Zero permits none. | After the budget, an unclassified transient death blocks and fails the run, class `relaunch-budget`. A recognized transport death can still wait under the recovery window. | Each relaunch increments `TransientRelaunches`; not a repair attempt or review round. | Same checkout and session preserved. |
| Transport recovery | Hard-coded: two hours of committed waits per boundary in `recovery.Window`; Fibonacci delays start at one second and cap at 30 minutes | Recognized recoverable failures at provider, tracker, Git, and forge boundaries that use recovery. The wait is durable across process restarts; this is not a deadline on the whole run. | The original boundary's failure is returned with class `recovery-window` and the retry account when another delay would exceed the window; integration can record that transport failure ended the run, and publication after local promotion may be a warning on success. | Recovery waits and attempts, not repairs. The failed operation determines any other budget cost, under the rules for causes outside the work. | Preserved; any promotion already made stands. |
| Repeated refusal for usage or overload | `execution.usage_limit_max_pause`: six hours | Aggregate committed pauses across the run, not one refusal. Zero waits for none. Usage probes default to 30 minutes (`usage_limit_unknown_reset_pause`); overload probes to 90 seconds (`server_overload_pause`). | A usage window beyond the remaining allowance ends `cancelled`, cause `usage-window`, and gives the claim back for a later pull. An overload that cannot fit the next wait, or another unusable wait, blocks and fails with class `usage-pause`. | The pause allowance, not a repair or relaunch. A usage-window ending returns the current round/grant where owed even if the checkout has a change. | Preserved; nothing the refused invocation left is treated as a judged delivery. |
| Document publication returns | Hard-coded: three, `MaxDocumentReturns` | Failed publications of one document in its owning conversation. | Each failure returns findings, the failing check, or conflicting paths to the owner. At three returns, further submissions are retained in conversation history but open no run. The owner must resolve the recorded cause before starting again in a new conversation. | No developer invocation or repair attempt. A revised submission below the bound opens a fresh reviewed run. | Failed branch and checkout preserved; primary checkout unchanged. |
| Shared repair budget | `execution.repair_attempts_before_replan`: 2, plus a granted continuation | Check failures, protected-path refusals, missing verification, review findings, and replay conflicts all draw from this one counter. Zero hands none back. | At the limit, the outstanding refusal or findings block the item and fail the run with class `repair-budget`. A check that cannot run, is cancelled, or times out ends immediately rather than buying a repair. | Each actual handback increments `RepairAttempts` before invocation. Substantive review verdicts also spend a review round; path, verification, and check refusals do not by themselves spend one. | Preserved. |
| Replays that fail on the change | `execution.integration_retries_before_reconciliation`: 2 | `ChargedReplays`, not every replay: the first conflict, check refusal, or repair verdict on each new base charges it. A target moving, and a replay that passes, cost none. | The replay that raises the count above the bound blocks and fails with class `integration-budget`. Within the bound a conflict can use the shared repair budget. | Charged replay count; a repair only if invoked, and a review round only for a chargeable verdict. | Preserved on its branch, with a conflict recorded where applicable. |
| Promotion queue wait | Hard-coded: 15 minutes, `promotionQueueWait` in `runstate.Store` | Waiting for another run to release the promotion lease for the same target branch. | Exhausting the wait refuses integration and ends the run `failed`, class `promotion-wait`; it is not a check timeout. | No repair attempt, review round, grant, relaunch, or charged replay for the wait; earlier spend stands. | Branch and checkout preserved; this run made no promotion. |
| Operator or development manager stop | Durable stop request; no timeout or configured default | A live run reads it at the next developer or reviewer invocation boundary; the sweep honours it when no process holds the run. A run already integrating is past these boundaries. | `cancelled`, class `operator-stop` or `manager-stop`; a manager's decided stop is docketed with that decision. | No new repair, grant, relaunch, or review round for the request; earlier spend stands. | Preserved. |
| Cancelled run context | Hosting process's context; no project default | Cancellation propagated through a process, wait, or harness step. Redeploy has the exception below. | `cancelled`; no check verdict is manufactured from cancellation. | No new repair for cancellation; existing spend stands. | Preserved. |
| Redeploy drain | `execution.redeploy_drain_limit`: 15 minutes; final attempt commit hard-coded to one minute | How long a watch waits for its hosted runs before restarting into the deployed build. | Normally stays `running` with a durable redeploy stop for the next watch to adopt. If the phase cannot be resumed or that marker cannot be saved, ends `cancelled` with class `redeploy-drain`. | Every counter kept; no new repair or grant for the drain. | Preserved. |
| Parked run whose process never returned | Hard-coded: `DefaultVanishedGrace`, 30 minutes | A provider stop, expired usage probe, directive/tracker pause, or lifted spending hold left without a lease holder and without a recorded ending. A usage pause deliberately exited at its in-process allowance is excluded. The grace runs from the recorded park or eligibility, not the duration of a live invocation. | The sweep ends it `failed` with `process-vanished`, and dockets or continues an eligible stall. A live lease or recorded recovery prevents this settlement. | No new repair; settling the vanished process returns an unspent grant for an empty delivery. Previously charged rounds remain charged to their process. | Branch and checkout left as found. |
| Dead claim audit | Hard-coded: 30-minute dead-claim threshold; one-hour run-activity window | A claim with no living work behind it, verified under the run lease. Awaiting continuation and integrated runs are excluded. | `cancelled`, class `dead-claim`; claim returned to the queue. | No new work budget; it does not judge the change. | Untouched. |
| Local Git and forge command times | Hard-coded: local Git 30 seconds scaled for load, at most ten times; checkout adds 50 milliseconds per file before scaling (2,000 files if counting fails); push five minutes; forge CLI one minute | Each command around creation, recording, replay, promotion, or publication. | The caller's failure; a killed checkout records `worktree-checkout-killed`, a killed replay `replay-killed`. Eligible transport failures recover before ending. | No repair for the command's clock; recorded causes outside the work determine any refund. | Existing artifacts retained; a failed creation may have no usable checkout. |
| Missing developer account | Hard-coded: one additional request for an account, then stop on a second clean but unaccounted reply | A final response that reports only future work or an interim status. | Fails with class `developer-account`; does not treat an interim line as delivery evidence. | Two invocations can cost provider money; the re-ask spends no repair attempt. | Preserved. |
| Unreadable or incomplete review | Hard-coded: one additional request, shared by unreadable verdicts, missing landing disposition, and missing accounting for omitted test data | A review reply the verdict contract cannot accept as a complete judgment. | The second refusal ends the run with class `review-account`; an absent independent identity also refuses promotion. | No repair for the re-ask; an unreadable reply is not a chargeable verdict. | Preserved. |
| Scope, evidence, and independence fences | Hard-coded enforcement; configured artifact homes select protected paths, and only the item's admitted grant can lift an eligible path refusal | Protected configuration, upstream artifacts, tracker exports, invariant ownership, required verification, revision-bound check/review evidence, and distinct developer/reviewer invocations. | Repairable path and verification refusals use the shared repair budget; missing approval or independence refuses integration. Configuration and workflow topology cannot bypass these gates. | Shared repairs only when handed back; no review round merely for refusing a path or missing verification. | Preserved. |
| Context and schema fences | Hard-coded: work-item context 256 KiB, complete review input 768 KiB; durable state must satisfy the run-state schema | Required context that cannot fit, a review request that cannot fit, and malformed durable state. Optional excerpts may be omitted rather than ending the run. Encoded record sizes have their own bounds below. | Context assembly or review fails before the invocation it prevents. A terminal state refused by the schema is salvaged once into the last valid record; if that also fails the sweep must settle the interrupted run. | No new repair just for assembly or storage; already-made invocations and judgments retain their costs. | Preserved; the refused record's full evidence may survive only on the item and docket. |
| Durable state size | Hard-coded: 1 MiB (1,048,576 bytes), `maxEncodedStateBytes` in `runstate.Store` | Each encoded JSON state record, including its trailing newline, on creation, save, and load. | Refuses the record and fails the active operation. Failure handling ends the run `failed` when its terminal record fits; an oversized terminal save leaves the last stored state for reconciliation. This size refusal does not use the schema-only salvage. | No new repair attempt, review round, grant, or relaunch for the refusal; earlier spend stands. | Existing branch and checkout preserved. A refused save does not replace the previously stored state. |
| Durable event size | Hard-coded: 1 MiB (1,048,576 bytes), `maxEncodedEventBytes` in `runstate.Store` | Each encoded JSON event, including its trailing newline; not the total event log. The reader shares the same per-event bound. | Refuses an oversized event before appending it. The event-recording error returns through the provider or check operation and ends the run `failed` when the terminal state can be saved. | No new repair attempt, review round, grant, or relaunch for the refusal; earlier spend and provider cost stand. | Existing branch and checkout preserved; earlier events and the last saved state remain. |
| Harness, integration, publication, or completion refusal | No single configured default; the operation's own validation and error | Claiming, scratch creation, context loading, saves, commits, check infrastructure, promotion lease, remote target agreement, publication recording, item outcome/closure, and cleanup. | Ordinary step errors fail the run in that phase. A pull request not merged or queued cannot close its item. An integrated change's unfinished publication or cleanup can instead leave a succeeded run with an outstanding warning. | No extra repair solely for the harness error; spend already recorded stands unless the rules for causes outside the work return it. | Preserved unless integration was proved and cleanup already removed it; a promoted change is not undone. |
| Work item escalated by a role | Typed `yoyodyne-landing` or review escalation; no configured bound | A work item the role says cannot be met as written. | `succeeded` without integration, class `work-item-escalated`; item parked for the development manager. This is a successful account, not a failed implementation. | No extra repair and no chargeable review round for the escalation. | Branch and checkout kept for the decision. |

## Causes outside the work

Every terminal run that did not land records one `stop_class`. This extends the
existing gate vocabulary: specific clocks and budget bounds have their own
names, and the causes outside the work below are converted into the same field.
The refusal record still decides accounting; a stop class grants nothing.
A settled provider silence keeps `provider-idle`, and a settled invocation time
limit keeps `provider-budget`, rather than losing that cause to `process-vanished`.
An older record with no class reads as `unknown`; no reader reconstructs it from
prose. Context size refusals use `context-bound`, durable state size refusals
`state-bound`, and durable event size refusals `event-bound`. The configured integration policy
can end a successful run before promotion,
recorded as `integration-policy`. Other step and scope refusals keep the existing
gate names.

The set of causes outside the work currently has fourteen values. A named cause alone does not forgive a
round: normally the settlement must also find an empty delivery. With a change
present it spends as an ordinary round does. `usage-window` and
`check-stage-bound` are the exceptions: they stopped before judgment, so they
settle without treating the preserved change as delivery. A round charged by a
previous process is not returned by this process; a return that could not be
written is reported explicitly. These causes are classifications, not fourteen
more timers. Stops keep what remains of the branch and checkout; the table says
when a usable checkout may never have existed.

| Cause | What stopped work | Ending and accounting |
| --- | --- | --- |
| `handback-missing-change` | A continuation was handed a checkout holding none of the change. | Blocks and fails; an empty delivery returns this process's round and consumed grant where owed. |
| `dirty-primary` | Unowned changes in the primary checkout prevented creating or using the required repository state. | Fails before that operation; empty delivery refunds where owed. A new checkout may not exist. |
| `worktree-checkout-killed` | The harness's checkout budget killed Git before creation finished. | Fails before a developer runs; nothing of the work was spent, and a consumed grant is returned where owed. No usable checkout is promised. |
| `sandbox-spawn-failure` | The provider or the developer's execution probe could not start inside the sandbox. | Fails; an empty round returns the owed round/grant. A probe that ran and failed is not this cause. |
| `stale-binary-dispatch` | Dispatch used an older harness than the decision relied on. | Vocabulary reserved for the cause; no code currently records it. It must not be inferred from a run's age. |
| `transport-failure` | An approved integration was stopped by an unanswered tracker, Git, forge, or network operation. | Failed integration can resume under its retained approval; accounting for the transport failure uses the normal empty-delivery rule. Recovery at supported boundaries is tried first. |
| `process-vanished` | No live lease holder, no ending, and no continuation before the park's grace elapsed. | Sweep fails the run without judging it; an empty delivery returns the unspent grant, and existing judgment stays spent. |
| `usage-window` | The provider reset or next probe would exceed the remaining maximum pause. | Cancelled, claim returned; owed current round and grant returned regardless of preserved diff. |
| `replay-killed` | Git's replay was stopped on time or cancellation and the original branch was restored. | Failed integration with approval retained; no conflict is invented from an interrupted rebase. Normal empty-delivery accounting. |
| `diverged-target` | Local and remote target histories disagree, or unowned primary state prevents fast-forward. | Integration stops with approval retained until the target can be reconciled. Normal empty-delivery accounting. |
| `remote-auth-refused` | SSH or forge refused or lacked the required credential. | Integration stops with approval retained; no transport backoff on a credential refusal. A person renews the credential. Normal empty-delivery accounting. |
| `queued-head-behind` | A queued merge fell behind its target and failed checks outside the change. | Sweep withdraws the queued merge and resumes promotion, replaying and earning fresh checks/review on the new base. It is not a finding against the old change. |
| `check-stage-bound` | All checks together reached the load-scaled stage limit without a verdict. | Timed out; owed current round/grant returned regardless of diff, with at most two automatic continuations at checks. |
| `developer-settings-not-applied` | A resumed run's developer provider did not apply the sandbox, the notes guard, or the settings that keep personal configuration out. | Recorded on the run before any developer is relaunched; nothing ran, nothing is charged, and the run stays as it was, resumable once the hold on the provider lifts. A fresh dispatch refused the same way has no run to record it on; see the developer settings check below. |

## Pauses that keep a run in flight

A directive or unfinished dependency parks the run before another gate or
promotion. An unreadable dependency read can park it for a tracker retry;
a spending hold parks it at the next provider boundary. Provider authentication
or availability failures are probed without spending the repair, relaunch, or
usage-pause budgets. These are waits, not terminal verdicts, and keep the branch,
checkout, and session. The grace for an abandoned park above is a different
rule, applied only after the lease says no process is serving it.

`execution.usage_limit_in_process_pause` defaults to six hours, the same as the
maximum usage pause. Lowering it lets a process exit with the run still
`running` and its next probe recorded; a later invocation resumes that run.
It does not create a second terminal timeout. `usage_limit_unknown_reset_pause`
and `server_overload_pause` set probe intervals, not budgets of their own.

## Before a run, and after it

| Limit or gate | Setting and default | What stops, what it costs, and what happens to the change |
| --- | --- | --- |
| Intake hold and developer capacity | `execution.max_concurrent_developers`: 1; durable intake hold has no timeout | Refuse autonomous selection or reservation before another run starts. Named operator work can bypass the intake hold, not capacity. Existing runs and their changes continue. |
| Developer settings check | Hard-coded: 30 seconds per question to the CLI; the hold is checked again after `execution.usage_limit_unknown_reset_pause` (30 minutes) | Before any developer is started or resumed, the harness asks the provider whether the sandbox, the notes guard, and the settings that keep personal configuration out took effect. A provider that did not apply them refuses the dispatch before anything is claimed, naming its version and what did not take; the first refusal files one critical report and holds that provider: no developer slot that would start on it is filled, while slots whose endpoint pair names another provider carry on, and the session stops choosing only when every slot is on the held provider. Nothing is docketed, charged, or counted toward the brake. The hold lifts when a check finds the settings applied or on a new harness build. See [provider plugins](provider-plugins.md#checking-that-a-developers-settings-took-effect). |
| Failure-storm brake and spending hold | `execution.blocked_runs_before_intake_hold`: 3; `brake_cooldown`: 30 minutes; `brake_escalation_cycles`: 4; durable spending hold | Stop new activity under the scheduler's rules. The brake summons the development manager and can probe after cooldown. A spending hold also parks existing runs at invocation boundaries; neither is itself a terminal judgment on their changes. |
| Account weekly allowance | `accounts.<alias>.weekly_budget_usd`: absent means unbounded | Scheduler admits no new dispatch on an account past the allowance. It does not kill a run already affined to that account; provider-reported usage windows are the separate run bound above. |
| Triage review-round ceiling | `triage.review_rounds_cap`: 4 | Refuses another repair grant or re-run after substantive rounds across runs exhaust the allowance. `triage.repair_grant_attempts` defaults to the execution repair budget, floored at one; grants are limited by remaining rounds. No new action is spent on a refusal; stopped artifacts stay as found. This is not a second automatic repair counter inside a live run. |
| Triage action ceilings | Hard-coded: one repair grant and one re-run per item, one merge re-arm per publication | Refuse a further carry-out without a valid crossing. Delegated crossings are one step each, at most five per item; at most sixteen override records fit. A refusal spends no new grant or re-run and leaves the stopped change alone. |
| Permanent recovery refusals | Closed typed causes in the section below; no retry interval | Prevent carrying out a recorded decision; they do not create or end a developer run. Ordinary holds have their own retry discipline. |
| Tracker command time | Hard-coded: 30 seconds per `bd` command, not load-scaled | A listing timing out can fail a recurring pass before its first turn, or a pull before it chooses work. A failed firing loses every decision it would have made, rather than spending a developer repair. Pull reads retry for five minutes with delays starting at two seconds, doubling to 30 seconds; dispatch reads can use the two-hour transport window. An in-flight run's transient dependency read can park under its tracker pause. No failed listing is treated as an empty backlog. |
| Recurring task message size | Hard-coded: `chat.MaxPassMessageBytes`, 256 KiB; an operator message is separately 32 KiB | Refuses a scheduled message before its role's first turn. A docket that would not fit loses that firing's decisions, not a run, repair attempt, or change. The task's configured prompt is limited to 16 KiB; the pass message includes the evidence beside it. |
| Recurring turn and conversation time | `recurring_tasks.<name>.max_turns`: 3 by default, at most 10; conversation invocation hard-coded to 15 minutes | Stops one firing from taking further role turns; provider cost of turns already served stands. A recurring pass is not a developer run, and its missed decisions can stall intake or recovery. |
| Reviewer patch limits | Hard-coded: 256 KiB total patch, 64 KiB per untracked file, 200 patch files; changed-path listing at most 1,000 | Limit the evidence quoted to the reviewer. Omitted content is named; the patch being truncated is not itself a terminal stop. An approval still has to account for omitted test data, and assembly failures follow the harness-error rule above. |
| Final-reply background drain | Hard-coded: five minutes in both provider adapters | Once a terminal reply arrives, wait for background tool work and then end that work. The terminal reply completes the invocation; it is not recorded as provider silence or a failed run. |
| Watchdog and stall alarm | Run-activity window hard-coded to one hour; `execution.factory_stall_after`: two hours | Report missing activity or a factory stall. They do not end developer runs. The claim audit and vanished-process settlement, which do, are listed separately above. |
| Landing checks | `execution.landing_check_timeout`: two hours per check; no whole-stage bound | Run after the developer run is terminal and its item settled. A clock stop is unverified, a failing check is red; neither changes the run's result or spends its repair budget. A red landing files separate work. |
| Artifact cleanup and later reconciliation | No project time default beyond the command budgets above | Cleanup requires proof of integration. Queued merges and unfinished publication can be reconciled later. A cleanup refusal keeps surviving artifacts and is reported on success; it does not retroactively spend repairs. |

## How the inventory stays true

`internal/orchestrator/run_stops_inventory_test.go` reads this document under
`make test`. It walks every non-test Go source, skipping test fixtures and build
output. In the package that owns run endings it finds calls to `fail`, `stop`,
`finish`, `complete`, and `escalate`, constructions of `phaseError`, calls to
`stoppedBy`, and every assignment to a `Status` field. Elsewhere it finds direct
terminal run-status assignments and literals, including imported aliases. It
also finds the typed constants for causes outside the work and stop classes.

The tables below pin the file, declaration, signal, and number of sites, with
an account of each. A new site in an already-listed function fails just as a
new function does. Missing, renamed, duplicate, and stale rows fail too. A
separate table pins the source expressions of the defaults and hard-coded
bounds; changing a figure requires updating its account here.

Some recognized syntax does not end a run: a status can be a projection, a
continuation can write `running`, and a scheduler's `stop` can end its own pull.
Those are listed separately with a reason, rather than silently filtered out.
This is a mechanical floor like the [authority inventory](authority-inventory.md):
it does not prove that every error returned through an existing ending has
been explained, or recognize a new ending primitive with unrelated syntax.
Those semantic changes still need review. The operator tables above explain
those errors by the operation that failed; the source tables let a reviewer
find every path into their settlement. These machine-read tables are code
blocks so their literal Go identifiers and paths remain exact.

## Permanent carry-out refusals

These gates act before the harness carries out a recorded repair, re-run, or
merge re-arm. They do not end a new developer run. The refusal keeps its own
words on the item's triage record and notes and on the development manager's
docket. It is delivered once as a new stoppage, including when she has already
been shown the original stopped run. No later pull retries that decision until
she changes it. There is no retry interval for these causes and no configuration
can make the same decision retry them.

The exact note owed to the item is saved with the refusal. A tracker that cannot
take the note leaves it pending for later pulls, which retry the note alone and
check for an append that already landed before writing it again. A new decision
or a cleared finding does not discard a pending note.

| Recorded cause | What the harness found | What can move it |
| --- | --- | --- |
| `worktree-gone` | The preserved checkout was retired or is missing, or the run recorded none to continue. | A re-run from the target branch, or an escalation about the change that was lost. |
| `branch-gone` | The stopped run's branch was checked and is missing. | A re-run or an escalation; a repair cannot continue the deleted branch. |
| `head-moved` | The checkout's HEAD differs from the commit the harness recorded. | A re-run or an escalation about what changed; the harness does not reset it to get past the gate. |
| `decision-superseded` | The durable decision is no longer the repair or re-run being attempted. | Carry out the current decision; record a new one if a further attempt is intended. |
| `decision-missing` | No durable decision authorizes the requested repair or re-run, including a grant made before decisions were recorded. | The development manager records it again, with an override where its budget requires one, or escalates. |
| `stoppage-missing` | The action needs a stopped run or a docketed stoppage that its records do not hold. | A re-run of a recorded run that can take it, or an escalation; the refusal names the applicable run and decision. |
| `publication-unmakeable` | The publication cannot describe a merge a re-arm can make, reported as `UnrearmablePublicationError`. | A re-run or an escalation; no later merge request of the same decision can repair the record. |
| `backend-unavailable` | The stopped run's developer worked on a backend this harness can no longer start — the project no longer describes it, or this build cannot launch it — so its session cannot be carried on, reported as `RecordedBackendError`. A run's developer is always invoked on the backend the run recorded, never on the one the developer is configured for now. | A re-run, which starts the item again on the configured backend; the run's session stays on its record. |

Classification reads typed refusals and confirmed repository findings, never a
match on the refusal's prose. An unreadable branch or checkout is not proof that
it has gone, and a failed reading keeps the ordinary paced retry. A newly recorded
decision, even of the same kind about the same run, releases the permanent gate
for another attempt; changing the item's notes or waiting longer does not.

A re-run asks nothing of the stopped run except that it has ended. A run that
ended with no durable blocker and no branch or worktree left is started again
from the target branch like any other re-run, and until the next pull with a
free developer slot does that, the item's hold says it waits on the harness. If
such a re-run is refused by one of the causes above, the decision stops holding
the item: nothing remains for a fresh run to start beside, so the next pull may
take it like any other ready item. The note written onto the item carries the
refusal and says the hold was released.

A refusal before the action starts spends no repair round, repair grant, or
re-run claim. The stopped run and whatever remains of its change stay as found.
Where the forge is asked after a re-arm has already been recorded as spent, that
spend stands; the carry-out record does not undo the action's accounting.

Other gates keep their existing behavior: a directive, unfinished dependency,
or checkout somebody is using is retried after fifteen minutes; a spending
pause, intake hold, or full harness is attempted once while shut and again on
the first eligible pull after it opens. [Carrying out a decision](work.md#letting-the-harness-choose-the-work)
describes the pass that applies those rules.

## Run-ending sites

```text
| File | Declaration | Signal | Count | Meaning |
| --- | --- | --- | --- | --- |
| `internal/orchestrator/documentpublication.go` | `(Pipeline).PublishDocument` | `call:fail` | 1 | Ends a document preparation failure and returns its cause to the owning conversation. |
| `internal/orchestrator/documentpublication.go` | `(Pipeline).PublishDocument` | `call:finish` | 1 | Completes an already recorded document integration through ordinary settlement. |
| `internal/orchestrator/documentpublication.go` | `(*activeRun).reviewDocument` | `call:fail` | 1 | Returns an independent review refusal to the document owner without invoking a developer. |
| `internal/orchestrator/documentpublication.go` | `(*activeRun).reviewDocument` | `classified-stop` | 1 | Classifies the document review refusal as a review failure. |
| `internal/orchestrator/documentpublication.go` | `(*activeRun).reviewDocument` | `call:stop` | 3 | Settles a directive, check, or review operation that stopped; failed publication returns to the document owner. |
| `internal/orchestrator/actions.go` | `deliverySteps` | `call:complete` | 1 | Registered completion action uses the ordinary terminal settlement. |
| `internal/orchestrator/claims.go` | `(ClaimAuditor).settle` | `status-write` | 1 | Cancels a dead claim verified under its lease; returns the item and touches no artifacts. |
| `internal/orchestrator/recordedbackend.go` | `(Pipeline).developerBackendFor` | `classified-stop` | 1 | Classifies a run whose recorded developer backend this harness cannot start as a harness stop, before any provider call and with the run's session left on its record; the run ends through the shared dispatcher. |
| `internal/orchestrator/developerrouting.go` | `(*activeRun).reconcileDeveloperOperation` | `classified-stop` | 1 | Classifies as a harness stop a run whose earlier developer attempt, on a slot with an endpoint pair, may still be running after two minutes of waiting for it, so no second attempt is started beside it; the run ends preserved through the shared dispatcher. |
| `internal/orchestrator/developerrouting.go` | `(*activeRun).reconcileDeveloperOperation` | `phase-error` | 1 | The same stop, reported as failed with the attempt and the reason its stop could not be confirmed. |
| `internal/orchestrator/pipeline.go` | `(Pipeline).Run` | `call:fail` | 7 | Claim, context, worktree, scratch, and state failures, a developer slot and endpoint pair that could not be recorded, or development ending through the shared dispatcher. |
| `internal/orchestrator/pipeline.go` | `(Pipeline).Run` | `call:stop` | 1 | Claim, context, worktree, scratch, and state failures, or development ending through the shared dispatcher. |
| `internal/orchestrator/pipeline.go` | `(Pipeline).resumeRun` | `call:fail` | 7 | Continuation setup and invocation or gate endings, retaining the existing change. |
| `internal/orchestrator/pipeline.go` | `(Pipeline).resumeRun` | `call:stop` | 4 | Continuation setup and invocation or gate endings, retaining the existing change. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).verifyReviewAndFinish` | `call:stop` | 4 | Check and hold endings, or success after the gate; manual integration keeps the checkout. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).verifyReviewAndFinish` | `call:finish` | 1 | Check and hold endings, or success after the gate; manual integration keeps the checkout. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).promoteApproved` | `call:fail` | 1 | Refuses missing independence, honours late holds, or finishes the approved promotion. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).promoteApproved` | `call:stop` | 2 | Refuses missing independence, honours late holds, or finishes the approved promotion. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).promoteApproved` | `call:finish` | 1 | Refuses missing independence, honours late holds, or finishes the approved promotion. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).promoteApproved` | `classified-stop` | 1 | Refuses missing independence, honours late holds, or finishes the approved promotion. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).endPromotion` | `call:fail` | 1 | Settles promotion failure or redeploy cancellation; any promotion already made stands. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).endPromotion` | `call:stop` | 1 | Settles promotion failure or redeploy cancellation; any promotion already made stands. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).blockOnChargedReplay` | `classified-stop` | 2 | Blocks on the charged replay budget, including a blocker write that failed. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).blockOnRebaseConflict` | `classified-stop` | 2 | Blocks on replay conflict after repairs are unavailable, including a failed blocker write. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).blockOnDivergedTarget` | `classified-stop` | 2 | Blocks before promotion on target disagreement, including a failed blocker write. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).blockOnPromotedDivergence` | `classified-stop` | 2 | Blocks on target disagreement after local promotion; the promotion stands. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).blockOnUnresolvedFindings` | `classified-stop` | 2 | Blocks on review findings after shared repairs, including a failed blocker write. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).blockOnFailingCheck` | `classified-stop` | 2 | Blocks on the failed check after shared repairs, including a failed blocker write. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).blockOnMissingPreservedChange` | `classified-stop` | 2 | Refuses a continuation holding none of its change; records cause and blocker. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).blockOnRefusedPaths` | `classified-stop` | 2 | Blocks on refused paths after shared repairs, including a failed blocker write. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).develop` | `classified-stop` | 2 | Stops on two unaccounted replies or failed publication of an accepted attempt. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).blockOnSpentRelaunchBudget` | `classified-stop` | 2 | Stops on provider death after relaunches and applicable recovery. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).blockOnSpentRelaunchBudget` | `phase-error` | 1 | Stops on provider death after relaunches and applicable recovery. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).recordDevelopment` | `classified-stop` | 3 | Reports a refused probe, provider clock stop without resumable state, or developer failure. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).recordDevelopment` | `phase-error` | 3 | Reports a refused probe, provider clock stop without resumable state, or developer failure. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).blockOnUsageLimit` | `classified-stop` | 2 | Blocks on an unusable provider wait, including a failed blocker write. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).endOnUsageWindow` | `call:fail` | 1 | Cancels and returns the claim for a later window without judging the change. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).reviewChange` | `classified-stop` | 2 | Records the invocation clock or a second reply the review contract cannot read. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).verify` | `classified-stop` | 3 | Check infrastructure failure, failed check, stage bound, individual timeout, or cancellation. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).verify` | `phase-error` | 1 | Check infrastructure failure, failed check, stage bound, individual timeout, or cancellation. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).gateProtectedPaths` | `phase-error` | 1 | Creates a path refusal before checks; shared repairs decide handback or terminal stop. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).integrate` | `classified-stop` | 4 | Refuses promotion lease, target settlement, or integration failure; any completed promotion stands. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).finish` | `call:complete` | 1 | Completes before proof-based cleanup and separate landing checks. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).complete` | `call:fail` | 6 | Refuses unrecorded publication, an unsaved landing configuration comparison, or outcome, item, or state write failures; otherwise succeeds. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).complete` | `classified-stop` | 6 | Refuses unrecorded publication, an unsaved landing configuration comparison, or outcome, item, or state write failures; otherwise succeeds. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).complete` | `status-write` | 2 | Refuses unrecorded publication, an unsaved landing configuration comparison, or outcome, item, or state write failures; otherwise succeeds. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).stop` | `call:fail` | 2 | Dispatches pauses separately from terminal usage windows, escalation, stops, and ordinary failure. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).stop` | `call:escalate` | 1 | Dispatches pauses separately from terminal usage windows, escalation, stops, and ordinary failure. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).escalate` | `call:fail` | 4 | Succeeds without promotion and parks the item; failures of that account end failed. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).escalate` | `status-write` | 2 | Succeeds without promotion and parks the item; failures of that account end failed. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).fail` | `status-write` | 2 | Shared terminal state and outcome writes, accounting for causes outside the work, preservation, and docketing; no cleanup. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).recordEndingAfterRefusedSave` | `status-write` | 1 | One salvage of the ending into the last valid durable state after schema refusal. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).attemptReview` | `classified-stop` | 2 | Rejects a failed review invocation or a reviewer that ran on the wrong model. |
| `internal/orchestrator/publish.go` | `(*activeRun).blockOnUnlandedPullRequest` | `classified-stop` | 2 | Blocks on a forge landing neither merged nor queued, including a failed blocker write. |
| `internal/orchestrator/reconcile.go` | `(Reconciler).settleInterruptedLanding` | `status-write` | 1 | Records success for a confirmed queued forge landing; other answers reconcile or block. |
| `internal/orchestrator/reconcile.go` | `(Reconciler).completeIntegrated` | `status-write` | 1 | Records success after proving integration; cleanup requires that proof. |
| `internal/orchestrator/reconcile.go` | `(Reconciler).settleStopRequest` | `status-write` | 1 | Cancels an unheld run on its stop request; branch and checkout untouched. |
| `internal/orchestrator/reconcile.go` | `(Reconciler).saveTerminalFailure` | `status-write` | 1 | Fails an unfinishable interrupted run; already-terminal status retains its account. |
| `internal/orchestrator/runretirement.go` | `(RunRetirer).Retire` | `status-write` | 1 | Cancels a run whose closed item has a later settled confirmed merge through another run; releases its slot and reservations, preserves its artifacts and history, and notes retirement once without reopening the item. |
| `internal/orchestrator/redeploydrain.go` | `(*activeRun).pauseForRedeploy` | `call:fail` | 2 | Cancels without resumable state or durable marker; otherwise leaves the run in flight. |
| `internal/orchestrator/selfcheck.go` | `(*activeRun).gateSelfVerification` | `phase-error` | 1 | Creates missing-verification refusal before checks; shared repairs decide its ending. |
| `internal/orchestrator/selfcheck.go` | `(*activeRun).blockOnMissingVerification` | `classified-stop` | 2 | Blocks on missing verification after shared repairs, including a failed blocker write. |
| `internal/runstate/environmental.go` | `CauseHandbackMissingChange` | `EnvironmentalCause` | 1 | Closed cause listed under Causes outside the work; changing the vocabulary requires its account. |
| `internal/runstate/environmental.go` | `CauseDirtyPrimary` | `EnvironmentalCause` | 1 | Closed cause listed under Causes outside the work; changing the vocabulary requires its account. |
| `internal/runstate/environmental.go` | `CauseWorktreeCheckoutKilled` | `EnvironmentalCause` | 1 | Closed cause listed under Causes outside the work; changing the vocabulary requires its account. |
| `internal/runstate/environmental.go` | `CauseSandboxSpawnFailure` | `EnvironmentalCause` | 1 | Closed cause listed under Causes outside the work; changing the vocabulary requires its account. |
| `internal/runstate/environmental.go` | `CauseStaleBinaryDispatch` | `EnvironmentalCause` | 1 | Closed cause listed under Causes outside the work; changing the vocabulary requires its account. |
| `internal/runstate/environmental.go` | `CauseTransportFailure` | `EnvironmentalCause` | 1 | Closed cause listed under Causes outside the work; changing the vocabulary requires its account. |
| `internal/runstate/environmental.go` | `CauseProcessVanished` | `EnvironmentalCause` | 1 | Closed cause listed under Causes outside the work; changing the vocabulary requires its account. |
| `internal/runstate/environmental.go` | `CauseUsageWindow` | `EnvironmentalCause` | 1 | Closed cause listed under Causes outside the work; changing the vocabulary requires its account. |
| `internal/runstate/environmental.go` | `CauseReplayKilled` | `EnvironmentalCause` | 1 | Closed cause listed under Causes outside the work; changing the vocabulary requires its account. |
| `internal/runstate/environmental.go` | `CauseDivergedTarget` | `EnvironmentalCause` | 1 | Closed cause listed under Causes outside the work; changing the vocabulary requires its account. |
| `internal/runstate/environmental.go` | `CauseRemoteAuthRefused` | `EnvironmentalCause` | 1 | Closed cause listed under Causes outside the work; changing the vocabulary requires its account. |
| `internal/runstate/environmental.go` | `CauseQueuedHeadBehind` | `EnvironmentalCause` | 1 | Closed cause listed under Causes outside the work; changing the vocabulary requires its account. |
| `internal/runstate/environmental.go` | `CauseCheckStageBound` | `EnvironmentalCause` | 1 | Closed cause listed under Causes outside the work; changing the vocabulary requires its account. |
| `internal/runstate/environmental.go` | `CauseDeveloperSettingsNotApplied` | `EnvironmentalCause` | 1 | Closed cause listed under Causes outside the work; changing the vocabulary requires its account. |
| `internal/runstate/stopclass.go` | `StopChecks` | `StopClass` | 1 | Closed recorded class: checks. Identifies the stopping operation; grants no action. |
| `internal/runstate/stopclass.go` | `StopReview` | `StopClass` | 1 | Closed recorded class: review. Identifies the stopping operation; grants no action. |
| `internal/runstate/stopclass.go` | `StopIntegration` | `StopClass` | 1 | Closed recorded class: integration. Identifies the stopping operation; grants no action. |
| `internal/runstate/stopclass.go` | `StopPublish` | `StopClass` | 1 | Closed recorded class: publish. Identifies the stopping operation; grants no action. |
| `internal/runstate/stopclass.go` | `StopCleanup` | `StopClass` | 1 | Closed recorded class: cleanup. Identifies the stopping operation; grants no action. |
| `internal/runstate/stopclass.go` | `StopRecording` | `StopClass` | 1 | Closed recorded class: recording. Identifies the stopping operation; grants no action. |
| `internal/runstate/stopclass.go` | `StopProvider` | `StopClass` | 1 | Closed recorded class: provider. Identifies the stopping operation; grants no action. |
| `internal/runstate/stopclass.go` | `StopOutside` | `StopClass` | 1 | Closed recorded class: outside. Identifies the stopping operation; grants no action. |
| `internal/runstate/stopclass.go` | `StopCancelled` | `StopClass` | 1 | Closed recorded class: cancelled. Identifies the stopping operation; grants no action. |
| `internal/runstate/stopclass.go` | `StopHarness` | `StopClass` | 1 | Closed recorded class: harness. Identifies the stopping operation; grants no action. |
| `internal/runstate/stopclass.go` | `StopUnknown` | `StopClass` | 1 | Closed recorded cause: `unknown`; grants no action. |
| `internal/runstate/stopclass.go` | `StopCheckTimeout` | `StopClass` | 1 | Closed recorded cause: `check-timeout`; grants no action. |
| `internal/runstate/stopclass.go` | `StopProviderIdle` | `StopClass` | 1 | Closed recorded cause: `provider-idle`; grants no action. |
| `internal/runstate/stopclass.go` | `StopProviderBudget` | `StopClass` | 1 | Closed recorded cause: `provider-budget`; grants no action. |
| `internal/runstate/stopclass.go` | `StopRelaunchBudget` | `StopClass` | 1 | Closed recorded cause: `relaunch-budget`; grants no action. |
| `internal/runstate/stopclass.go` | `StopRepairBudget` | `StopClass` | 1 | Closed recorded cause: `repair-budget`; grants no action. |
| `internal/runstate/stopclass.go` | `StopIntegrationBudget` | `StopClass` | 1 | Closed recorded cause: `integration-budget`; grants no action. |
| `internal/runstate/stopclass.go` | `StopPromotionWait` | `StopClass` | 1 | Closed recorded cause: `promotion-wait`; grants no action. |
| `internal/runstate/stopclass.go` | `StopUsagePause` | `StopClass` | 1 | Closed recorded cause: `usage-pause`; grants no action. |
| `internal/runstate/stopclass.go` | `StopOperator` | `StopClass` | 1 | Closed recorded cause: `operator-stop`; grants no action. |
| `internal/runstate/stopclass.go` | `StopManager` | `StopClass` | 1 | Closed recorded cause: `manager-stop`; grants no action. |
| `internal/runstate/stopclass.go` | `StopRedeploy` | `StopClass` | 1 | Closed recorded cause: `redeploy-drain`; grants no action. |
| `internal/runstate/stopclass.go` | `StopDeadClaim` | `StopClass` | 1 | Closed recorded cause: `dead-claim`; grants no action. |
| `internal/runstate/stopclass.go` | `StopDeveloperAccount` | `StopClass` | 1 | Closed recorded cause: `developer-account`; grants no action. |
| `internal/runstate/stopclass.go` | `StopReviewAccount` | `StopClass` | 1 | Closed recorded cause: `review-account`; grants no action. |
| `internal/runstate/stopclass.go` | `StopEscalated` | `StopClass` | 1 | Closed recorded cause: `work-item-escalated`; grants no action. |
| `internal/runstate/stopclass.go` | `StopContextBound` | `StopClass` | 1 | Closed recorded cause: `context-bound`; grants no action. |
| `internal/runstate/stopclass.go` | `StopStateBound` | `StopClass` | 1 | Closed recorded cause: `state-bound`; grants no action. |
| `internal/runstate/stopclass.go` | `StopEventBound` | `StopClass` | 1 | Closed recorded cause: `event-bound`; grants no action. |
| `internal/runstate/stopclass.go` | `StopIntegrationPolicy` | `StopClass` | 1 | The configured policy leaves promotion outside this run; grants no action. |
| `internal/runstate/stopclass.go` | `StopRecoveryWindow` | `StopClass` | 1 | A boundary spent its bounded recovery waits; grants no action. |
```

## Sites that do not end a run

```text
| File | Declaration | Signal | Count | Meaning |
| --- | --- | --- | --- | --- |
| `internal/orchestrator/documentpublication.go` | `(Pipeline).PublishDocument` | `status-write` | 2 | Marks the document run and its temporary bookkeeping as running; changes no backlog item. |
| `internal/orchestrator/documentpublication.go` | `(*documentTracker).Block` | `status-write` | 1 | Changes temporary document bookkeeping; ordinary durable run state records the failure. |
| `internal/orchestrator/documentpublication.go` | `(*documentTracker).Claim` | `status-write` | 1 | Changes temporary document bookkeeping; creates no backlog claim. |
| `internal/orchestrator/documentpublication.go` | `(*documentTracker).Complete` | `status-write` | 1 | Changes temporary document bookkeeping; ordinary durable run state records completion. |
| `internal/orchestrator/documentpublication.go` | `(*documentTracker).Release` | `status-write` | 1 | Changes temporary document bookkeeping; returns no backlog item. |
| `internal/orchestrator/checkstagecontinue.go` | `continuedAtChecks` | `status-write` | 1 | Re-enters the same run at checks with running status; no terminal outcome. |
| `internal/orchestrator/integrationresume.go` | `(IntegrationResumer).supersedeOnRun` | `status-write` | 1 | Resumes preserved repair or approved integration with running status. |
| `internal/orchestrator/pipeline.go` | `(Pipeline).Run` | `status-write` | 2 | Initial pending-to-running transition and its outcome projection; no terminal ending. |
| `internal/orchestrator/pipeline.go` | `(Pipeline).holdWorkItem` | `status-write` | 1 | Copies an existing run status into the held-item answer; no terminal write. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).pause` | `status-write` | 1 | Reports the pause as running, with change and session preserved; no terminal ending. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).pauseForProviderStop` | `status-write` | 1 | Reports the pause as running, with change and session preserved; no terminal ending. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).pauseForDirective` | `status-write` | 1 | Reports the pause as running, with change and session preserved; no terminal ending. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).pauseForDependency` | `status-write` | 1 | Reports the pause as running, with change and session preserved; no terminal ending. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).pauseForTracker` | `status-write` | 1 | Reports the pause as running, with change and session preserved; no terminal ending. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).pauseForOperatorHold` | `status-write` | 1 | Reports the pause as running, with change and session preserved; no terminal ending. |
| `internal/orchestrator/queuedchecks.go` | `(Reconciler).updateQueuedHead` | `status-write` | 1 | Resumes promotion after a queued head fell behind with running status. |
| `internal/orchestrator/recurring.go` | `(Trigger).run` | `call:finish` | 1 | Finishes a recurring firing or sets its sweep account to more; no developer run ends. |
| `internal/orchestrator/recurring.go` | `(Trigger).run` | `status-write` | 3 | Sets a sweep account to more for unhandled critical reports or docket entries not yet delivered, including at the pass's ending; no developer run ends. |
| `internal/orchestrator/redeploydrain.go` | `(*activeRun).pauseForRedeploy` | `status-write` | 1 | Reports the pause as running, with change and session preserved; no terminal ending. |
| `internal/orchestrator/repaircontinue.go` | `(RepairContinuer).supersedeOnRun` | `status-write` | 1 | Resumes preserved repair or approved integration with running status. |
| `internal/orchestrator/schedule.go` | `(Scheduler).Schedule` | `call:stop` | 2 | Stops the drain and watch session or delivers escalations; no developer-run ending. |
| `internal/orchestrator/schedule.go` | `(Scheduler).Schedule` | `call:escalate` | 1 | Stops the drain and watch session or delivers escalations; no developer-run ending. |
| `internal/orchestrator/stallcontinue.go` | `continuedAfterStall` | `status-write` | 1 | Re-enters the same attempt after a stall with running status; no terminal outcome. |
```

## Bound source values

These are the Go expressions behind the figures above, held by the value test.

```text
| File | Declaration | Expression |
| --- | --- | --- |
| `internal/config/config.go` | `defaultCheckTimeout` | `Duration(30 * time.Minute)` |
| `internal/config/config.go` | `defaultCheckStageTimeout` | `Duration(30 * time.Minute)` |
| `internal/config/config.go` | `defaultLandingCheckTimeout` | `Duration(2 * time.Hour)` |
| `internal/config/config.go` | `defaultUsageLimitMaxPause` | `Duration(6 * time.Hour)` |
| `internal/config/config.go` | `defaultUsageLimitInProcessPause` | `defaultUsageLimitMaxPause` |
| `internal/config/config.go` | `defaultUsageLimitUnknownResetPause` | `Duration(30 * time.Minute)` |
| `internal/config/config.go` | `defaultServerOverloadPause` | `Duration(90 * time.Second)` |
| `internal/config/config.go` | `defaultRedeployDrainLimit` | `Duration(15 * time.Minute)` |
| `internal/config/config.go` | `defaultFactoryStallAfter` | `Duration(2 * time.Hour)` |
| `internal/config/config.go` | `defaultBlockedRunsBeforeIntakeHold` | `3` |
| `internal/config/config.go` | `defaultBrakeCooldown` | `Duration(30 * time.Minute)` |
| `internal/config/config.go` | `defaultBrakeEscalationCycles` | `4` |
| `internal/config/config.go` | `defaultReviewRoundsCap` | `4` |
| `internal/backend/claudecode/backend.go` | `defaultTimeout` | `4 * time.Hour` |
| `internal/backend/claudecode/backend.go` | `defaultIdleTimeout` | `5 * time.Minute` |
| `internal/backend/claudecode/backend.go` | `defaultAfterReplyTimeout` | `5 * time.Minute` |
| `internal/backend/codex/backend.go` | `defaultTimeout` | `4 * time.Hour` |
| `internal/backend/codex/backend.go` | `defaultIdleTimeout` | `5 * time.Minute` |
| `internal/backend/codex/backend.go` | `defaultAfterReplyTimeout` | `5 * time.Minute` |
| `internal/checks/runner.go` | `defaultTimeout` | `30 * time.Minute` |
| `internal/checks/runner.go` | `DefaultStageTimeout` | `30 * time.Minute` |
| `internal/checks/runner.go` | `DefaultLandingCheckTimeout` | `2 * time.Hour` |
| `internal/review/reviewer.go` | `defaultReviewTimeout` | `15 * time.Minute` |
| `internal/review/reviewer.go` | `MaxReviewInputBytes` | `768 << 10` |
| `internal/recovery/recovery.go` | `Window` | `2 * time.Hour` |
| `internal/recovery/recovery.go` | `MaxInterval` | `30 * time.Minute` |
| `internal/orchestrator/reconcile.go` | `DefaultVanishedGrace` | `readmodel.DefaultDeadClaimThreshold` |
| `internal/readmodel/claims.go` | `DefaultDeadClaimThreshold` | `30 * time.Minute` |
| `internal/readmodel/silence.go` | `DefaultRunActivityWindow` | `time.Hour` |
| `internal/runstate/document.go` | `MaxDocumentReturns` | `3` |
| `internal/runstate/stallcontinue.go` | `MaxHarnessStallContinuations` | `1` |
| `internal/runstate/checkstagecontinue.go` | `MaxCheckStageContinuations` | `2` |
| `internal/runstate/promotion.go` | `promotionQueueWait` | `15 * time.Minute` |
| `internal/runstate/store.go` | `maxEncodedStateBytes` | `1 << 20` |
| `internal/runstate/store.go` | `maxEncodedEventBytes` | `1 << 20` |
| `internal/orchestrator/redeploydrain.go` | `redeployCommitTimeout` | `time.Minute` |
| `internal/orchestrator/triagecaps.go` | `triageActsAlone` | `1` |
| `internal/runstate/triageoverride.go` | `MaxDelegatedCapCrossings` | `5` |
| `internal/runstate/triageoverride.go` | `MaxTriageOverrides` | `16` |
| `internal/orchestrator/schedule.go` | `firstReadRetryDelay` | `2 * time.Second` |
| `internal/orchestrator/schedule.go` | `longestReadRetryDelay` | `30 * time.Second` |
| `internal/orchestrator/schedule.go` | `readRetryWindow` | `5 * time.Minute` |
| `internal/beads/client.go` | `defaultTimeout` | `30 * time.Second` |
| `internal/chat/chat.go` | `MaxOperatorMessageBytes` | `32 << 10` |
| `internal/chat/chat.go` | `MaxPassMessageBytes` | `256 << 10` |
| `internal/chat/chat.go` | `DefaultTurnTimeout` | `15 * time.Minute` |
| `internal/config/recurring.go` | `DefaultRecurringTurns` | `3` |
| `internal/config/recurring.go` | `MaxRecurringTurns` | `10` |
| `internal/config/recurring.go` | `MaxRecurringPromptLen` | `16 << 10` |
| `internal/contextbundle/bundle.go` | `defaultMaxBytes` | `256 << 10` |
| `internal/gitworktree/manager.go` | `defaultTimeout` | `30 * time.Second` |
| `internal/gitworktree/manager.go` | `maxLoadFactor` | `10` |
| `internal/gitworktree/manager.go` | `checkoutFileBudget` | `50 * time.Millisecond` |
| `internal/gitworktree/manager.go` | `uncountedCheckoutFiles` | `2000` |
| `internal/gitworktree/manager.go` | `pushTimeout` | `5 * time.Minute` |
| `internal/gitworktree/manager.go` | `DefaultMaxDiffBytes` | `256 << 10` |
| `internal/gitworktree/manager.go` | `DefaultMaxDiffFileBytes` | `64 << 10` |
| `internal/gitworktree/manager.go` | `DefaultMaxDiffFiles` | `200` |
| `internal/gitworktree/manager.go` | `DefaultMaxListedFiles` | `1000` |
| `internal/publish/github.go` | `defaultTimeout` | `60 * time.Second` |
| `internal/config/resolve.go` | `newResolution:Execution.MaxConcurrentDevelopers` | `1` |
| `internal/config/resolve.go` | `newResolution:Execution.RepairAttemptsBeforeReplan` | `2` |
| `internal/config/resolve.go` | `newResolution:Execution.IntegrationRetriesBeforeReconciliation` | `2` |
| `internal/config/resolve.go` | `newResolution:Execution.TransientRelaunchesBeforeBlocking` | `2` |
```
