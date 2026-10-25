---
id: fresh-factory-health-and-preserved-run-recovery
kind: design
title: Fresh factory health and preserved-run recovery
supports:
    - management-and-supervision
    - program-manager
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-05T00:33:22.166145Z
      reason: 'yoyodyne-3xd: specify shared lifecycle recovery, fresh standing health for both managers and evidence-backed pass completion; preserve run ownership, interrupted phase and truthful workflow history without changing product intent.'
    - action: amended
      by: architect
      at: 2026-10-13T17:44:18.572813Z
      reason: 'yoyodyne-ab2: adds the callable evidence and recovery contract the development manager asked for, and the development-manager, architect and shared prompt rules the operator assigned on October 4, with the split between prompting and code, six bounded slices and outcome scenarios (a) to (h). Rewritten after the reviewer refused document-827.1: adoption of the prompt rules is a developer item that writes the live persona files and their templates through a reviewed run, never a step handed to the operator; the results section uses plain words only; every item is named by what it is.'
    - action: amended
      by: architect
      at: 2026-10-15T11:37:50.437694Z
      reason: 'Amendment promised by the stopped-run recovery design (yoyodyne-gtx, closed): the closed list of run.recover actions and of run.verify expected states becomes one list with the development manager''s decisions in stopped-run-recovery step 3, with one carrier, stop binding and the item''s recovery budget; the recovery-policy reconciliation line now says it was folded into stopped-run-recovery.'
    - action: amended
      by: architect
      at: 2026-10-17T07:52:00.052569Z
      reason: 'Answers the reviewer''s report on the recovery tool (yoyodyne-06m, report-4a9bc4d0): releasing a reservation applies only to a finished run with no worker that still holds a developer slot, the item''s claim or file reservations, and is refused otherwise; scenario (c) is recast as an in-flight run with no worker, answered by a wait, a continuation or an unresolved hold. Also folds in the worker identity correction (amendment-6a24ecf3: the build and the run and attempt come from a record beside the run lease, not from the process) and the program-manager grant decided for yoyodyne-nmj (every instance of the role holds the four read capabilities). This carries the substance of amendment-efb09026 and amendment-6a24ecf3.'
    - action: amended
      by: architect
      at: 2026-10-21T06:50:02.953263Z
      reason: 'Approves amendment-5e6136b8 and answers report-4cf5c3a6: a worker''s identity adds the time its program began, each program records its own start so readers in other processes can tell an earlier program in the same process from the current one, every reader applies one rule, and a starting program releases its earlier program''s leases through the ordinary lease path.'
    - action: amended
      by: architect
      at: 2026-10-22T17:09:26.990337Z
      reason: Replaces the retired phrase for the operator's pause in the preserved-run recovery section with the terms register's wording, as part of replacing the coined vocabulary in the governed documents (yoyodyne-ifd.437.21); nothing else in the design changes.
    - action: amended
      by: architect
      at: 2026-10-24T23:58:17.208001Z
      reason: 'Carries the revision the forward progress design (yoyodyne-ifd.428.112) and the review of lifecycle states (yoyodyne-ifd.428.113) both promised: health.observe reports every item''s state and the forward progress problems, the recovery tool names who acts and accepts the harness for its bounded acts while the development manager is unavailable, a third use of the same act on the same failure is refused, and the unresolved hold becomes a wait that keeps the stop listed.'
approvals:
    - revision: 0
      by: operator
      at: 2026-10-05T00:33:22.166145Z
      reason: approved by the operator in conversation chat-11558d325e9a214ebfd00bb4a0012750, turn 135, for the document the architect wrote there (document-135.1)
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-13T17:44:18.572813Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 832, for document-832.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-10-15T11:37:50.437694Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1006, for document-1006.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 3
      by: harness
      at: 2026-10-17T07:52:00.052569Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1134, for document-1134.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 4
      by: harness
      at: 2026-10-21T06:50:02.953263Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1397, for document-1397.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 5
      by: harness
      at: 2026-10-22T17:09:26.990337Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1500, for document-1500.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 6
      by: harness
      at: 2026-10-24T23:58:17.208001Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1665, for document-1665.1 under the automatic approval policy
---
# Purpose and scope

This design serves the approved management and supervision goals through the management-and-supervision and program-manager designs. It specifies Fresh factory-health evidence and accountable scheduled-pass completion (yoyodyne-3xd). It changes neither product intent nor role authority.

The harness supplies fresh standing health evidence to every scheduled development-manager and factory-flow program-manager pass. Recovery, health reporting and pass completion use one account of a run's lifecycle. Event deltas and cached specification context remain useful inputs, but neither establishes current health.

How a stopped run moves from its stop to going on, step by step, is [stopped-run-recovery](stopped-run-recovery.md). This design supplies the evidence and the one recovery capability those steps use, and the two share one closed list of recovery actions. What every admitted item's state is, and what the harness does on its own while the development manager is unavailable, is [forward progress](forward-progress.md). How every wait and hold becomes one follow-up record is [fewer lifecycle states](fewer-lifecycle-states.md). This design reports the first and carries the acts of both.

These are design requirements enforced in code, not new repository-wide invariants.

# Decision and alternatives

Use the existing run records, presence observation, reservation lock, run leases, workflow observations, pass records, follow-ups and manager reports. Reject a parallel monitoring database, timestamp-only declarations of process death, unconditional claim-audit exemptions, blind cancellation of preserved work and completion inferred from an empty event delta. Those alternatives either create conflicting accounts or conceal work that still occupies capacity.

Separate saved status, observed worker presence, pending wait, continuation eligibility, capacity occupancy and workflow observation. No single field substitutes for the others.

# Shared lifecycle account

Every health observation identifies the product, work item by title and identifier, run, saved status and phase, workflow instance where present, observation time, evidence sources and completeness. It includes worker presence with uncertainty, last recorded activity, last meaningful progress where distinguishable, occupied capacity, pending waits and their release conditions, preserved artifacts, the latest continuation attempt and refusal, and the responsible role and supported remedy for an actionable anomaly.

Streaming activity is not automatically meaningful progress. A missing progress measurement is reported as unknown rather than replaced silently with a file modification time.

The shared classification has these outcomes:

- A confirmed live worker retains ownership. Age alone does not authorize intervention.
- Missing ownership evidence within the existing presence grace remains uncertain. Read failures remain explicit uncertainty.
- A durable dependency wait keeps the item reserved against duplicate work and holds no developer slot. It is eligible for continuation only after fresh dependency evidence says the wait has cleared.
- Other legitimate waits retain their existing pause policy and capacity semantics. Health names the condition and the follow-up that will revisit it.
- A workerless preserved run without a legitimate wait requires same-run recovery or a wait that keeps its stop on the development manager's list, naming a responsible role, supported next step and follow-up check.
- A recorded integration requires completion or reconciliation of remaining steps, not fresh development.
- A terminal run may still owe cleanup or carry preserved work. Its ending does not establish that its artifacts are disposable.

Scheduler selection, reconciliation, claim auditing, capacity reporting and manager health delivery consume this shared classification. None of them decides from an item's tracker status of in progress or its assignee, which are display copies under point 3 of [fewer lifecycle states](fewer-lifecycle-states.md). A timestamp-based snapshot may identify a candidate for inspection; only ownership acquired through the existing lease permits mutation.

# Preserved-run recovery

Recover the same run, branch, worktree, provider session and counters. Preserve the recorded definition binding and interrupted phase. Development interrupted before completion resumes development; a dependency pause must not move it to checking. An interrupted review re-earns the gate from checks according to the existing delivery policy. No incomplete check or verdict receives credit.

A recovery actor takes the existing run lease and strictly reloads its record. It then reads current dependencies, the operator's pause (set by `yoyo pause`), directives and applicable gates before invoking a provider or advancing delivery. A pre-adoption refusal is not a durable dependency wait.

If unfinished dependencies prevent continuation, record the dependency wait under the run lease without advancing the interrupted phase, and write its follow-up in the same act, with the check that the item's dependencies have closed. Only a successfully established durable wait releases capacity. The duplicate-run reservation stays active. When dependencies clear, reacquire capacity and clear the wait under the same reservation lock used by fresh reservations, while retaining the run lease. A capacity refusal leaves the durable wait and interrupted phase intact.

A redeploy stop describes why execution ended; it is not by itself a perpetual audit exemption or proof of a legitimate wait. The shared classification must lead to continuation, a durable wait, completion of already integrated work, or a wait that keeps the stop listed for the development manager. Claim auditing must not cancel useful preserved work merely to free capacity.

After a save reports an uncertain result, strictly reload under the lease before another transition. A replacement may have succeeded before a directory-sync error was returned. Neither success nor absence may be inferred from that error alone. If durable state cannot be established, stop further invocation and report uncertainty, the last attempted operation and the recovery owner. Do not attempt blind cancellation as a substitute for reconciliation.

Re-entry must observe artifacts and any previously attempted mutation before repeating it. Exclusive local ownership prevents concurrent workers; it does not prove that a remote or provider action never happened. Ambiguous effects require observation or a wait that keeps the stop listed rather than an assertion of exactly-once execution.

# Workflow observation and checkpoints

The inspected declarative delivery path observes the Go pipeline and does not perform its actions. The run record owns delivery facts; the workflow instance owns observed topology. Recovery must preserve that division.

Classify redeploy interruption as a pause before recording an ordinary stopped outcome. Preserve the observation at the last truthful boundary. Reload the run and its observation before resuming; report a missing, unreadable, terminal or divergent observation explicitly.

A terminal observation is not authority to cancel a nonterminal, recoverable delivery run. Preserve its terminal history and record the disagreement on the run. Continue through the supported delivery path when the run's ownership, artifacts, phase and gates establish that continuation is safe; do not fabricate graph transitions or reopen a terminal observation. Where those delivery facts cannot be established, record a wait that keeps the stop listed for the development manager.

Do not migrate an in-flight run to the current project definition. A changed or unavailable definition is explicit evidence of observation failure. A mismatch must not be reported as a clean workflow trace.

Run and observation writes are separate durable operations. Crash reconciliation must inspect both and distinguish a missing write from a completed transition. Validation remains strict for mutation. No requirement here authorizes weakening validation, resetting counters or silently deleting incompatible markers. Any necessary schema extension must preserve old record readability and make unsupported mutation fail explicitly; strict writers must be upgraded before they encounter fields they cannot preserve.

# Fresh health delivery

Before every scheduled development-manager and factory-flow program-manager invocation, gather a new shared health observation independently of the event cursor and specification cache. Include nonterminal workerless runs, stranded capacity, unresolved recovery attempts and overdue follow-ups even when no landing, stoppage or admission event occurred.

The observation also reports what [forward progress](forward-progress.md) asks of it:

- the state of every admitted item that is not a parent of open items, worked out from records as point 1 of forward progress says, with the count in each state and the oldest entry of each;
- an item that fits none of those states, as a problem owned by the development manager, naming the item by title and identifier, its tracker status and the records read;
- the latest pull's reason for each ready item it did not start, from the closed list in point 2 of forward progress;
- a developer slot whose provider can be used left empty beside ready work it accepts for `execution.idle_slot_after` with no reason recorded, as a problem, with the bug the harness filed or noted for it;
- the same failure on three or more items within twenty four hours, shown once as one problem naming the items, the failing check or tests and when it was first seen;
- whether each management role is unavailable, since when and why, by the test in point 4 of forward progress;
- a difference between an item's tracker copy of in progress or its assignee and its run records, as a problem the harness owns and repairs.

Give the pass an observation identifier and observation time, source coverage, individual read failures and every actionable anomaly. Bound rendering without silently dropping anomalies: include exact totals and omitted identities through a supported bounded follow-up read. Missing, stale or partial evidence must never be rendered as an empty healthy set.

An anomaly identity is based on product, run and condition; it must distinguish a newer run from an older stoppage on the same item. An anomaly about an item rather than a run, such as an item in no state, uses the item in place of the run. Retain first observation, latest observation, recurrence, resolution and reopening through existing durable pass/report records. First observation does not reset when a manager starts a new conversation. A reopened condition is recorded explicitly. No separate anomaly database is introduced.

Health observation is read-only. Program managers retain their existing authority: they may identify patterns and work within their lane, but cannot stop runs, release claims, alter capacity or decide triage. The development manager remains accountable for recovery decisions within her authority; the harness performs supported operations, and takes the bounded acts of its own that the section below on who acts allows.

# Accountable pass completion

Every delivered actionable anomaly must have a durable disposition citing its identity and evidence:

1. Verified resolution, with fresh evidence establishing what changed.
2. An evidenced wait, naming the release condition and the follow-up that will revisit it; include a deadline when time bounds the wait.
3. An unresolved condition, naming the responsible role, supported next action and follow-up time or trigger.

Owner assignment alone, generic memory, an empty event delta and a repeated summary do not discharge an anomaly. Reuse existing work and dispositions instead of admitting duplicates. Repeated refusal and overdue follow-up remain visible on later passes until fresh evidence establishes resolution or a valid wait.

Coverage and health are separate results. A pass can account for every anomaly while reporting unresolved problems. Stale or unreadable evidence, omitted actionable identities or unsupported resolution claims prevent an unqualified complete result. Validate dispositions against the delivered observation and cited records before recording completion. Validation does not execute recovery or grant additional authority.

# Acceptance and existing work

Lifecycle recovery (yoyodyne-06m) must demonstrate a developer interrupted by redeploy after a dependency is added, absent worker ownership, durable dependency wait at the original development phase, freed capacity for other work, dependency closure, capacity reacquisition and continuation of the same run. Preserve provider session, artifacts and all counters. Add concurrent adoption, crash between durable writes, invalid-record refusal and uncertain-save readback cases. A live worker and uncertain presence must never be cancelled by age alone.

Shared health delivery (yoyodyne-e7c) must demonstrate fresh delivery to both managers with an empty event delta and old specification cache. Include workerless nonterminal runs, distinct newer-run identity on an item with an older stoppage, read failures, rendering limits and legitimate waits. Both managers must receive the same classification and evidence coverage.

Manager coverage validation (yoyodyne-5eu) must reject unsupported healthy or complete claims, missing anomaly dispositions and resolution supported only by owner assignment or memory. It must accept honest complete coverage with unresolved conditions, retain first observation across conversations, expose overdue recurrence and record resolution followed by reopening.

These are acceptance interfaces for already admitted work, not a decomposition or pull order. Immediate preserved-run recovery (yoyodyne-9j7) remains separate operational work. Bounded reruns of a check stopped by its time limit are those of [recoverable-and-terminal-failures](recoverable-and-terminal-failures.md), applied in steps 1 and 5 of stopped-run-recovery, which folds in the recovery-policy reconciliation (yoyodyne-ifd.429.42); this design does not bypass dependencies or spend limits.

# Evidence and limits

Repository inspection used commit dfc0079ad98c. The continuation, redeploy, reconciliation, declarative workflow and dependency-test files were read in full. Earlier scheduler and state-schema reads were truncated, so their unseen portions are not verification evidence. Supplied tracker incident accounts are evidence of reported behavior, not execution verified by the architect. No tests were run during design inspection.

# Addendum: what managers can call, and the rules that use it

This addendum answers the development manager and architect prompt rules and the evidence and recovery contract behind them (yoyodyne-ab2). It carries two things forward. The first is the development manager's finding that the guidance above is not enough to build from, because it says what must be true without saying what a manager can actually ask the harness for. The second is the operator's assignment of October 4: concrete rules for how the development manager and the architect decide, and the evidence and tools that let them follow those rules.

Rules in a prompt cannot supply evidence the harness does not gather or actions it does not offer. So the addendum first defines five capabilities, then the rules that rely on them, then which parts are enforced in code and which are left to the prompt. Everything here reuses the run records, leases, reservation lock, pass records, follow-ups, reports, read model and role authority already described above. No parallel monitoring or recovery service is added, and nothing in a prompt grants authority.

## The five capabilities

Each is registered as a tool under [the tool interface design](tool-interface.md): a descriptor, its mark in the registry, and an audit event for every call. The harness performs every call itself. An owning conversation turn and a scheduled pass reach the same capability with the same request and result fields, on both supported backends, in the form the tool interface design gives each backend.

`health.observe`, class read, built by shared health delivery (yoyodyne-e7c). Request: the product, and optionally a list of runs to restrict it to. Result: an observation identifier and its time; the sources read and how complete each was; each read failure; every actionable anomaly with its identity (product, run or item, and condition), first seen, last seen, how many times it has recurred, and its classification under the shared lifecycle account above. It also reports each supervised service: every running copy with its build and start time and whether that build is the current one; the search path the service runs with and whether each provider command it needs is found on it; maintenance failures recorded since the last pass and whether they were delivered; and each configuration file it names with whether that file exists. This is how a pass establishes configuration health rather than guessing it, and it covers the development manager's report of a supervisor configuration pointing at a file missing from its test directory. It reports the forward progress conditions listed in the fresh health delivery section above, each as the forward progress piece that builds it lands.

A result larger than the rendering bound returns exact totals, the identities it left out, and a cursor.

`health.follow`, class read, built by shared health delivery. Request: an observation identifier and a cursor. Result: the next part of what that observation left out. An observation older than fifteen minutes is refused with the reason, and the caller observes again.

`run.inspect`, class read, built by shared health delivery. Request: one run. Result: the run record, cut at 48 KiB with the cut declared; the lease holder; the worker's identity as defined below and whether it is live, absent, or uncertain; whether the branch and worktree exist; the reservation; any wait and its release condition, with its follow-up; the last continuation attempt and refusal.

`run.verify`, class read, built by shared health delivery. Request: one run and one expected state, from this closed list: worker live, wait recorded (keeping the stop listed, or taking it off the list), reservation released, continuation started, repair started, rerun holds the item, integration completed, item given back, question recorded. Result: met, not met, or unknown, with the evidence read and its observation time. Unknown is never reported as met. A request for wait recorded names which of its two kinds it expects, and a wait of the other kind is not met.

`run.recover`, class act, built by lifecycle recovery (yoyodyne-06m), with the four actions marked below built by the slices of stopped-run-recovery. Request: a request identifier the caller makes up and never reuses, one run, the observation identifier the decision rests on, the saved status and phase the caller expects, the stop it answers by attempt number and stop time where the run is on the development manager's list of stopped runs, one action, who is acting, and a reason. The actions are one closed list. It is the list of the development manager's decisions in step 3 of stopped-run-recovery, with one action added for a run that has no stop to decide:

- continue the same run from the step it stopped at, spending nothing;
- repair: the same run goes back to its developer with a stated number of repair attempts (built by stopped-run-recovery);
- rerun: a fresh run of the item from the current target branch (built by stopped-run-recovery);
- finish the merge: complete the steps after a recorded or possible integration;
- wait, naming its owner, next step, check from the closed list of [follow-ups](follow-ups.md) and deadlines, and whether it keeps the stop on the development manager's list or takes it off while its follow-up is open and before its first deadline; a dependency wait is a wait whose check is the item's dependencies closing, recorded as the preserved-run recovery section says;
- give the item back: retire the run with its change kept, release the item's claim and developer slot, and return the item to the queue with the stop named in a note (built by stopped-run-recovery);
- ask the Lead Product Manager, recording the question and the stop it concerns on the run, when the decision needs a priority or a scope she owns (built by stopped-run-recovery);
- release what a finished run with no worker still holds: its developer slot, the item's claim, or its file reservations. This is not a decision on a stop. It applies only to a finished run that still holds at least one of those three. A finished run that holds none of them has nothing to release, and the request is refused as not allowed for its saved status and phase. A run with no worker that is still saved as in flight is not answered by a release: it is answered by a wait, a continuation of the same run, or a wait that keeps its stop listed.

The list has no separate action for an unresolved hold. What that action recorded, an owner, a next step and a time to look again, is a wait that keeps the stop listed, as point 2 of [fewer lifecycle states](fewer-lifecycle-states.md) says. Until build piece 4 of that design lands, the build may still record an unresolved hold; it is read as a wait that keeps the stop listed, and no new code records one.

Result: applied, refused, or unknown, as defined below.

There is one carrier. A decision the development manager records on her list of stopped runs is carried out through `run.recover` and nothing else, and a `run.recover` call that answers a stop is that decision. An act the harness takes on its own, under the section on who acts below, goes through `run.recover` the same way. No second path carries a recovery act, so none can skip a check the others make.

Dispositions are recorded where they are recorded now, on the pass record, and manager coverage validation (yoyodyne-5eu) checks them. No new capability is added for them.

## Who acts

A `run.recover` request names who is acting: the development manager, or the harness. The harness acts only while the development manager is unavailable, by the test in point 4 of [forward progress](forward-progress.md), and only for these:

- continue the same run, where the stop is outside the work and its cause has demonstrably cleared, for exactly the causes point 4 of forward progress lists;
- finish the merge, for a run recorded as merged or whose merge is uncertain, by step 8 of stopped-run-recovery; where the commit is not on the target branch it records a wait for her instead;
- release what a finished run with no worker still holds;
- wait, owned by the development manager, keeping the stop listed, with the check that the stop has left the list.

Releasing a hold whose follow-up check has passed, the other act forward progress lets the harness take, is not a `run.recover` call: the follow-up check runner closes that follow-up as [follow-ups](follow-ups.md) says.

A request from the harness for any other action, or with a reason other than the development manager's unavailability, is refused as not allowed for the one acting. So is one past the bounds of point 4 of forward progress: a second act of the harness's own on the same stop; a second automatic continue after the same failure; more automatic continues in an hour than `execution.harness_recovery_per_hour`; or any act on a stop whose failure is shown as the same failure across items. The refusal names the bound. Every act of the harness's own is recorded before and after with the harness as the one who acted and the time since which the development manager has been unavailable, is checked with `run.verify` like hers, and is listed at the start of her next completed pass.

The harness never repairs, reruns, gives an item back or asks the Lead Product Manager through this tool. Those are judgments, and they wait for her.

## What binds a request

Every `run.recover` request carries the product, the run, the observation identifier and the expected status and phase. The harness takes the run lease, reloads the record strictly and compares. If anything differs from what the caller expected, the request is refused as state moved, nothing is changed, and the result names what differs. Where the request answers a stop, the stop is compared too: a request for a stop that a later stop of the same run has replaced is refused as state moved. An observation older than fifteen minutes is refused for any act.

A repair grant or a rerun is taken from the item's recovery budget when the request is recorded, under the lease, in the units stopped-run-recovery sets: repair attempts, and one rerun for each stop. A request for more than the budget holds is refused, naming what is left. No action widens the budget.

A continue, a repair or a rerun whose act was already carried out twice on the same failure of the same item, as point 3 of [forward progress](forward-progress.md) defines the same failure, is refused, naming the two earlier stops and the acts taken on them. This counts acts by both the development manager and the harness.

The checks that matter for safety are made at the moment the harness acts, under the lease, and not at the moment the caller observed. An action that needs no worker is refused if a worker is live or uncertain when the harness acts, whatever the observation said. A release is checked at that moment against what the finished run still holds, not against what the observation said it held.

## How a worker is identified

A worker is identified by five things together: its process number, the start time the operating system reports for that process, the time the program running in that process began, the build it runs, and the run and attempt number it recorded when it started. The operating system can confirm only the first two. They are not enough on their own, because the scheduler restarts by replacing its own program inside the same process: the process number and the operating system's start time stay the same, so those two cannot tell an earlier program in the process from the one running now. The time the program began is what tells them apart.

Two records carry the rest. First, each program, as it starts, writes a record of its own start under the harness's state directory, one for each process, naming its process number, the operating system's start time, the time the program began, and its build; it replaces any earlier record for the same process. Second, a worker writes a record beside the run lease when it starts, naming its process number, the operating system's start time, the time its program began, its build, and its run and attempt number.

A worker is live only when all three of these hold: a running process matches the process number and the operating system's start time; the program start record for that process names the same program start time as the record beside the lease; and the record beside the lease matches the run record's run and attempt. A record beside the lease whose program start time is older than the one in the program start record for that process names an earlier program, so that worker is absent, whatever the operating system says about the process. A missing or unreadable program start record leaves the worker uncertain, never live and never absent.

Only the program itself knows when it began, so a reader in another process can learn it only from the program start record. Every reader applies the rule above in the same way, through one shared function: the scheduler, `run.inspect`, `run.verify`, `health.observe`, `yoyo status` and the dashboard. None of them decides liveness from the process alone.

When a program starts, after writing its own start record, it looks for records beside run leases that name its process with an earlier program start time, and releases each lease through the ordinary lease path, recording on the run that its worker was an earlier program in the same process. A replaced program cannot be carrying the earlier program's work in memory, so the release loses nothing. A run released this way is then a run with no worker, answered as the shared lifecycle account above says, and never cancelled because of the release.

The build is reported, and is never checked against the process. A process number alone never establishes a worker, because the operating system reuses numbers. A replacement worker for the same run carries a new attempt number, so a request decided against the earlier attempt is refused as state moved.

Two alternatives were rejected. Releasing the earlier program's leases when a program starts, with nothing else, leaves a gap before the release finishes in which every other reader takes those leases for live workers. Comparing the records beside leases with nothing else gives a reader in another process no way to learn when the current program began.

## Request identity and repeats

The harness records a `run.recover` request on the run record, under the lease, before it acts, and records the result after. A repeat with the same identifier and the same content returns the recorded result without acting again. A repeat with the same identifier and different content is refused. If the first result was unknown, a repeat does not act again: it reads the run first, as the next section says.

## Applied, refused and unknown

Applied means the change was written, and the harness read the record back afterwards and found the change there.

Refused means nothing was changed. The reason is one of: state moved; observation too old; a worker is live or uncertain; the action is not allowed for the run's saved status and phase, which includes a release asked for on a finished run that holds nothing; the request asks for more than the item's recovery budget holds; the same act was already carried out twice on the same failure of this item; the action is not allowed for the one acting, or passes a bound on the harness's own acts, naming which; the action is not yet built, naming the slice that builds it; the caller does not hold the capability; the request identifier was used before with different content.

Unknown means the harness tried to write and afterwards could not tell whether the write happened, for example because the file system reported an error after the file may already have been replaced. On unknown, nobody acts on that run again until the harness has reloaded it under the lease and established what it holds. Until then the run carries a wait that keeps its stop listed, owned by the development manager, whose check is that the run has been reloaded and what it holds established, naming the attempted action and the request identifier. The next health observation reports it.

## Checking the outcome

Applied is not the same as recovered. After an applied result, the caller checks the outcome with `run.verify`, using the expected state that matches the action:

- continue the same run: continuation started, and then worker live;
- repair: repair started, and then worker live;
- rerun: rerun holds the item;
- finish the merge: integration completed;
- wait, including a dependency wait: wait recorded, of the kind the request asked for;
- give the item back: item given back;
- ask the Lead Product Manager: question recorded;
- release what a finished run holds: reservation released.

A disposition of verified resolution must cite a `run.verify` result of met from an observation taken after the action. A worker that started, an action that was applied, or a pull request that merged is not enough on its own. The harness checks its own acts the same way.

## Who holds what

The development manager's bundle holds all five. The program manager's bundle, and so every instance of it, holds `health.observe`, `health.follow`, `run.inspect` and `run.verify`, and never `run.recover`, as the authority section above already says. The architect's bundle holds `health.observe`, `health.follow` and `run.inspect`, so a design can rest on how runs actually behave. A grant is registered by the slice that builds the capability, and a call without the grant is refused. The harness's own acts are not a grant to any role: they are the bounded acts of the section on who acts, taken by the harness's code with no model.

## What is enforced in code and what is left to the prompt

Enforced in code, never left to a prompt: the fifteen minute limit on observations; the binding of a request to its observation, expected state and stop; the checks made at the moment of acting; the recovery budget; the refusal of a third use of the same act on the same failure; who may act, and the bounds on the harness's own acts; worker identity, including the program start record, the one rule every reader applies to it, and the release of an earlier program's leases when a program starts; request identity and repeats; the closed lists of actions and expected states, and that they agree with the decisions in stopped-run-recovery; the one carrier; the grants; that unknown blocks further action on the run; that verified resolution needs a later met result; and that missing, stale or partial evidence is never a healthy or complete result.

Left to the prompt: judgment about which obstruction matters, whether a pattern is recurring, which supported action is smallest, and how a design is ordered. The prompt rules below tell a role how to use what the code guarantees. They grant nothing, and a role that ignores one is held by the checks above, not by the wording.

The responsibilities sit as follows. The descriptor of each capability is in the tool registry. The fixed contract every role conversation is built on carries the shared rule and a short account of the three results. The scheduled pass message carries a fresh `health.observe` result, as the fresh health delivery section already requires. Each role's persona carries that role's rules.

## The rules, word for word

Shared, for every role, in the shared ground of every role conversation, built in `internal/chat/role.go`:

> Before adding a dependency or asking for another decision, name the specific question that is still open and say why existing authority and evidence cannot answer it. Before reporting progress, keep four things apart: what you found, what you decided, what was carried out, and what you checked afterwards.

The development manager, in `.yoyodyne/personas/development-manager.md` and the shipped template it is generated from:

> 1. Trace the obstruction. When work stops, name the exact step that failed, the evidence for it from a fresh observation, and the supported next action. Saying the problem is covered by an existing item is not a recovery decision: say what that item does for this run, and when.
>
> 2. A decision is not finished until it has an effect you have checked. That means a worker `run.verify` reports live, a reservation it reports released, or a wait or refusal recorded with an owner and a condition for trying again.
>
> 3. Look for repetition. Across passes, count repeated failures of the same kind, P0 work that waited while developer slots were free, and checks repeated on the same revision. Raise a pattern that persists as a pattern, even when each event looks handled.
>
> 4. Check the real path. When a service or a role fails, look at the environment that service or role actually runs with, as `health.observe` reports it, not at one set up by hand to make it work.
>
> 5. Prefer the smallest supported action that restores progress and keeps the work and its checks. Continue the same run before starting a new one, and never cancel preserved work to free a slot.

The architect, in `.yoyodyne/personas/architect.md` and the shipped template it is generated from:

> 1. When a capability needs a design that cannot be published without that capability, say so and settle that first: name what can ship before it, and in what order.
>
> 2. Separate what this design needs from a wider redesign. Say what can ship under existing rules and what needs a new ruling, and rule only on the second.
>
> 3. Design for finishing. Say who owns the work while it runs, what happens on a restart, what anyone can observe once it is done, and how records already broken are mended.
>
> 4. A missing tool or a limit of the current build is an engineering problem to design, not a reason to hand work to a person. Real safety boundaries stay; say which kind each constraint is.
>
> 5. Count the cost to throughput: checks repeated, checks restarted by deploys, and work held in the scheduler, not only whether one part is correct on its own.

The development manager's second rule names `run.verify`, so it goes in only after that capability exists. Every other rule names only what exists today and can go in now. That is the minimum path that ships under existing policy.

## Adoption

The live persona files under `.yoyodyne/personas` and the templates they are generated from are repository files. A developer run writes them like any other file, through review and integration, and the product moves itself onto the new build. No step in adoption falls to the operator. If at the time of the run a protection on those paths refuses a developer's write, the run is held for the development manager. She names the harness change needed, and the hold is not handed to a person.

Adoption is checked against what each role actually receives, not against the files alone. After the change lands, the next development manager pass, the next architect pass and one owning conversation turn of each are read as delivered, and each rule's text is found in the prompt that role received. Whether the rules work is then judged by the outcome scenarios below, never by the presence of the text.

## Slices

Each slice can be checked on its own. Decomposing them into items is the development manager's; the mapping below lets the Lead Product Manager release or re-scope the three delivery items already admitted.

1. `health.observe` and `health.follow`, including services, search paths, maintenance delivery and configuration files, with the scheduled pass message carrying a fresh observation and the grants for both. Shared health delivery (yoyodyne-e7c). Depends on nothing new.
2. Worker identity recorded at worker start, including the program start record, the one rule every reader applies to it, and the release of an earlier program's leases when a program starts; `run.inspect` and `run.verify`, with their grants. Shared health delivery (yoyodyne-e7c). Depends on nothing new.
3. `run.recover`: the request shape with the whole closed list of actions, binding to the observation, expected state and stop, the checks at the moment of acting, the recovery budget check, request identity and repeats, the three results, and the wait that keeps the stop listed on unknown, with its grant. It carries out continue, finish the merge, wait and release what a finished run holds. It refuses repair, rerun, give the item back and ask the Lead Product Manager as not yet built until slices 3, 4 and 6 of stopped-run-recovery build them on top of it. Its tests include a release asked for on a finished run that holds no developer slot, claim or file reservation, which is refused as not allowed for its saved status and phase with nothing changed. Lifecycle recovery (yoyodyne-06m). Depends on slice 2.
4. Disposition validation: verified resolution needs a later met result, unknown is never complete, and a start, an applied result or a merge alone is refused as resolution. Manager coverage validation (yoyodyne-5eu). Depends on slices 2 and 3.
5. The shared rule, the five architect rules and the development manager's rules 1, 3, 4 and 5, written into `internal/chat/role.go`, the two live persona files and their shipped templates, with the delivered prompts checked as the adoption section says. A new item. The developer names the template files before starting. Depends on nothing.
6. The development manager's rule 2, written into the same two files for her, and checked in the same way. A new item. Depends on slices 2 and 3.

Three changes are built by other designs' pieces, not by slices here, so none is admitted twice:

- the forward progress conditions in `health.observe`, by pieces 1, 2, 5 and 6 of [forward progress](forward-progress.md), each adding its report to `health.observe` as it lands;
- the section on who acts, the bounds on the harness's own acts and the refusal of a third use of the same act, by pieces 5 and 7 of forward progress, on top of slice 3;
- the unresolved hold folded into a wait that keeps the stop listed, across the action list, `run.verify` and the unknown result, by piece 4 of [fewer lifecycle states](fewer-lifecycle-states.md). Where slice 3 is built first, it is built to that shape.

## Outcome scenarios

Each scenario is checked by what happens, in a test home or on the live factory, never by finding prompt text.

(a) Maintenance failures recorded but never delivered (yoyodyne-ifd.428.72). A maintenance job fails after the last pass. The next `health.observe` reports the failure, and the pass record carries a disposition that cites it.

(b) A repair decision never carried out: assessing and recovering the preserved run a redeploy stopped (yoyodyne-9j7), shared health delivery (yoyodyne-e7c), and the case recorded under the orchestrator's fakes moving to a helper package (yoyodyne-ifd.429.13.6). The development manager decides to continue a stopped run, and carrying it out fails. The next observation shows the anomaly still open with its owner, and validation refuses a complete result for that pass.

(c) A stale reservation blocking the fix needed to release it: the reservation item named in the audit (yoyodyne-ifd.428.68), lifecycle recovery (yoyodyne-06m), and the run on holding a check stopped by its own time limit (yoyodyne-ifd.428.37). A run with no worker is still saved as in flight and holds the item and a developer slot. The development manager answers it with a wait naming its check, a continuation of the same run, or a wait that keeps its stop listed, and `run.verify` reports the matching expected state as met. Where she continues it, the run goes on and the blocked fix is then started; where she waits, the slot is freed as the preserved-run recovery section says while the item stays reserved. A release asked for on a finished run that holds no developer slot, claim or file reservation is refused as not allowed for its saved status and phase, and nothing changes.

(d) Automatic publication waiting on a ruling it cannot publish: the stopped design publication item named in the audit (yoyodyne-ifd.433.21.1) and the design of how the owning role decides an amendment (yoyodyne-ifd.437.14). A confirmed document whose publication depends on a capability not yet built appears in `health.observe` as waiting with an owner. The architect's design for it names what ships first and in what order.

(e) A search path supplied by hand hiding broken service and role invocation (yoyodyne-ifd.435.12). A service is started with a search path that lacks a provider command. `health.observe` reports the missing command against that service, even though a person's own shell finds it.

(f) Fifteen minute redeploy drains restarting checks that take 12 to 24 minutes, which the capacity rules for heavy checks (yoyodyne-ifd.429.43) address. Checks restarted by deploys appear as one anomaly with a recurrence count of two or more. The development manager's disposition raises it as a pattern rather than closing each event separately.

(g) A worker that started, or a merged pull request carrying only evidence, mistaken for completed recovery: shared health delivery (yoyodyne-e7c) and manager coverage validation (yoyodyne-5eu). Validation refuses a verified resolution that cites only a start, an applied result or a merge, and accepts one that cites a later `run.verify` met result.

(h) Dashboard test state left behind after a restart meant only to restore availability, and stale service copies left running on old builds (yoyodyne-ifd.434.19). A restart leaves an older copy running. The next `health.observe` lists both copies with their builds and reports the older one as an anomaly.

(i) A decision on a replaced stop. A run stops, the development manager decides a repair, and before it is carried out the run stops again. The `run.recover` request for the first stop is refused as state moved, the budget is unchanged, and the second stop stays on her list.

(j) A grant larger than the budget. The development manager asks for more repair attempts than the item's recovery budget holds. The request is refused, naming what is left, and nothing is taken from the budget.

(k) A program replaced inside the same process: a run's worker identified by process number and start time (yoyodyne-ifd.428.102). The scheduler holds a run's lease and then restarts by replacing its own program, keeping its process number and the operating system's start time. Before the new program has released anything, `yoyo status`, the dashboard and `run.inspect`, each in another process, report that run's worker as absent, because the program start record names a later program start time than the record beside the lease. Once the new program has started, the lease is released through the ordinary lease path, the run carries a note that its worker was an earlier program in the same process, and the run is answered as a run with no worker. With the program start record removed, the same readers report the worker as uncertain.

(l) The harness acting while the development manager is unavailable. Her passes have not completed for twice their interval. A run stopped at a provider's usage limit whose reset has passed is continued by the harness through `run.recover`, recorded with the harness as the one who acted, and `run.verify` reports the worker live. A stop about the work gets a wait owned by her that keeps the stop listed. A request from the harness to repair is refused as not allowed for the one acting, and a fifth automatic continue within an hour is refused naming the bound. Her next completed pass opens with both acts.

(m) A write whose result is unknown. A `run.recover` write reports an error after the file may have been replaced. The run carries a wait that keeps its stop listed, owned by the development manager, naming the attempted action and request identifier; `run.verify` for wait recorded, keeping the stop listed, is met; any other act on the run is refused until the run has been reloaded and what it holds established.

(n) An item in no state. A parked item loses its follow-up. The next `health.observe` reports it as an item with no recorded way forward, owned by the development manager, with its tracker status and the records read.

## Evidence and limits of this addendum

The design above was read at commit 968d33ed. The live persona directory `.yoyodyne/personas` was listed at the same commit and holds the architect and development manager files named here. Where the shipped templates live was not read, which is why slice 5 has its developer name them before starting. The tool interface design was not read again for this addendum. No code was read and no tests were run. No invariant is added: the rules marked as enforced in code are requirements of this design, held by its slices and their tests.

Revised at commit 13841dbd, after reading this design and stopped-run-recovery whole at that commit: the closed lists of `run.recover` actions and `run.verify` expected states were made one with the development manager's decisions in stopped-run-recovery, with one carrier, binding to the stop, and the recovery budget check. No code was read for the revision.

Revised at commit c84ec3fd, after reading this design whole at that commit, for the reviewer's report on the recovery tool (yoyodyne-06m) that in the current build a finished run holds no developer slot, claim or file reservation. The release action now applies only to a finished run that still holds one of those, and is refused otherwise; scenario (c) is an in-flight run with no worker, answered by a wait, a continuation or a wait that keeps its stop listed. The same revision says the build and the run and attempt come from a record beside the run lease rather than from the process, and grants the four read capabilities to every instance of the program manager. No code was read for this revision; the report is the evidence for what a finished run holds.

Revised at commit 7a0f0dfd, after reading this design whole at that commit, for the proposed change on how a worker is identified (amendment-5e6136b8) and the developer's warning beside it (report-4cf5c3a6). A worker's identity gains the time its program began, because a scheduler that replaces its own program keeps its process number and the operating system's start time. Each program records its own start where readers in other processes can compare a lease against it, every reader applies one rule, and a starting program releases its earlier program's leases. Scenario (k) checks it. The developer's account in the amendment and the warning is the evidence that the scheduler restarts this way and that the code records the program start time; no code was read for this revision.

Revised at commit f081d385, after reading this design whole at that commit together with the terms register: the preserved-run recovery section now names the operator's pause, set by `yoyo pause`, in the register's own wording. No other sentence changed.

Revised at commit 48d4541a, after reading this design, [forward progress](forward-progress.md) and [fewer lifecycle states](fewer-lifecycle-states.md) whole at that commit, for the revision both of them promised. `health.observe` reports each item's state, an item in no state, the reasons ready work was not started, a slot left empty beside ready work, the same failure across items, each management role's unavailability and a tracker copy that differs from the run records. `run.recover` names who is acting, accepts the harness for its bounded acts while the development manager is unavailable, and refuses a third use of the same act on the same failure. The unresolved hold becomes a wait that keeps the stop listed, across the action list, `run.verify`, the unknown result and the scenarios. Scenarios (l), (m) and (n) are added. No code was read for this revision.