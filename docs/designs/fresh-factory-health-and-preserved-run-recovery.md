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
approvals:
    - revision: 0
      by: operator
      at: 2026-10-05T00:33:22.166145Z
      reason: approved by the operator in conversation chat-11558d325e9a214ebfd00bb4a0012750, turn 135, for the document the architect wrote there (document-135.1)
---

# Purpose and scope

This design serves the approved management and supervision goals through the management-and-supervision and program-manager designs. It specifies Fresh factory-health evidence and accountable scheduled-pass completion (yoyodyne-3xd). It changes neither product intent nor role authority.

The harness supplies fresh standing health evidence to every scheduled development-manager and factory-flow program-manager pass. Recovery, health reporting and pass completion use one account of a run's lifecycle. Event deltas and cached specification context remain useful inputs, but neither establishes current health.

These are design requirements enforced in code, not new repository-wide invariants.

# Decision and alternatives

Use the existing run records, presence observation, reservation lock, run leases, workflow observations, pass records and manager reports. Reject a parallel monitoring database, timestamp-only declarations of process death, unconditional claim-audit exemptions, blind cancellation of preserved work and completion inferred from an empty event delta. Those alternatives either create conflicting accounts or conceal work that still occupies capacity.

Separate saved status, observed worker presence, pending wait, continuation eligibility, capacity occupancy and workflow observation. No single field substitutes for the others.

# Shared lifecycle account

Every health observation identifies the product, work item by title and identifier, run, saved status and phase, workflow instance where present, observation time, evidence sources and completeness. It includes worker presence with uncertainty, last recorded activity, last meaningful progress where distinguishable, occupied capacity, pending waits and their release conditions, preserved artifacts, the latest continuation attempt and refusal, and the responsible role and supported remedy for an actionable anomaly.

Streaming activity is not automatically meaningful progress. A missing progress measurement is reported as unknown rather than replaced silently with a file modification time.

The shared classification has these outcomes:

- A confirmed live worker retains ownership. Age alone does not authorize intervention.
- Missing ownership evidence within the existing presence grace remains uncertain. Read failures remain explicit uncertainty.
- A durable dependency wait keeps the item reserved against duplicate work and holds no developer slot. It is eligible for continuation only after fresh dependency evidence says the wait has cleared.
- Other legitimate waits retain their existing pause policy and capacity semantics. Health names the condition and the mechanism that will revisit it.
- A workerless preserved run without a legitimate wait requires same-run recovery or an unresolved recovery hold with a responsible role, supported remedy and follow-up condition.
- A recorded integration requires completion or reconciliation of remaining steps, not fresh development.
- A terminal run may still owe cleanup or carry preserved work. Its ending does not establish that its artifacts are disposable.

Scheduler selection, reconciliation, claim auditing, capacity reporting and manager health delivery consume this shared classification. A timestamp-based snapshot may identify a candidate for inspection; only ownership acquired through the existing lease permits mutation.

# Preserved-run recovery

Recover the same run, branch, worktree, provider session and counters. Preserve the recorded definition binding and interrupted phase. Development interrupted before completion resumes development; a dependency pause must not move it to checking. An interrupted review re-earns the gate from checks according to the existing delivery policy. No incomplete check or verdict receives credit.

A recovery actor takes the existing run lease and strictly reloads its record. It then reads current dependencies, operator holds, directives and applicable gates before invoking a provider or advancing delivery. A pre-adoption refusal is not a durable dependency wait.

If unfinished dependencies prevent continuation, record the dependency wait under the run lease without advancing the interrupted phase. Only a successfully established durable wait releases capacity. The duplicate-run reservation remains in force. When dependencies clear, reacquire capacity and clear the wait under the same reservation lock used by fresh reservations, while retaining the run lease. A capacity refusal leaves the durable wait and interrupted phase intact.

A redeploy stop describes why execution ended; it is not by itself a perpetual audit exemption or proof of a legitimate wait. The shared classification must lead to continuation, a durable wait, completion of already integrated work, or an accountable unresolved hold. Claim auditing must not cancel useful preserved work merely to free capacity.

After a save reports an uncertain result, strictly reload under the lease before another transition. A replacement may have succeeded before a directory-sync error was returned. Neither success nor absence may be inferred from that error alone. If durable state cannot be established, stop further invocation and report uncertainty, the last attempted operation and the recovery owner. Do not attempt blind cancellation as a substitute for reconciliation.

Re-entry must observe artifacts and any previously attempted mutation before repeating it. Exclusive local ownership prevents concurrent workers; it does not prove that a remote or provider action never happened. Ambiguous effects require observation or an unresolved hold rather than an assertion of exactly-once execution.

# Workflow observation and checkpoints

The inspected declarative delivery path observes the Go pipeline and does not perform its actions. The run record owns delivery facts; the workflow instance owns observed topology. Recovery must preserve that division.

Classify redeploy interruption as a pause before recording an ordinary stopped outcome. Preserve the observation at the last truthful boundary. Reload the run and its observation before resuming; report a missing, unreadable, terminal or divergent observation explicitly.

A terminal observation is not authority to cancel a nonterminal, recoverable delivery run. Preserve its terminal history and record the disagreement on the run. Continue through the supported delivery path when the run's ownership, artifacts, phase and gates establish that continuation is safe; do not fabricate graph transitions or reopen a terminal observation. Where those delivery facts cannot be established, record an unresolved recovery hold.

Do not migrate an in-flight run to the current project definition. A changed or unavailable definition is explicit evidence of observation failure. A mismatch must not be reported as a clean workflow trace.

Run and observation writes are separate durable operations. Crash reconciliation must inspect both and distinguish a missing write from a completed transition. Validation remains strict for mutation. No requirement here authorizes weakening validation, resetting counters or silently deleting incompatible markers. Any necessary schema extension must preserve old record readability and make unsupported mutation fail explicitly; strict writers must be upgraded before they encounter fields they cannot preserve.

# Fresh health delivery

Before every scheduled development-manager and factory-flow program-manager invocation, gather a new shared health observation independently of the event cursor and specification cache. Include nonterminal workerless runs, stranded capacity, unresolved recovery attempts and overdue follow-ups even when no landing, stoppage or admission event occurred.

Give the pass an observation identifier and observation time, source coverage, individual read failures and every actionable anomaly. Bound rendering without silently dropping anomalies: include exact totals and omitted identities through a supported bounded follow-up read. Missing, stale or partial evidence must never be rendered as an empty healthy set.

An anomaly identity is based on product, run and condition; it must distinguish a newer run from an older stoppage on the same item. Retain first observation, latest observation, recurrence, resolution and reopening through existing durable pass/report records. First observation does not reset when a manager starts a new conversation. A reopened condition is recorded explicitly. No separate anomaly database is introduced.

Health observation is read-only. Program managers retain their existing authority: they may identify patterns and work within their lane, but cannot stop runs, release claims, alter capacity or decide triage. The development manager remains accountable for recovery decisions within her authority; the harness performs supported operations.

# Accountable pass completion

Every delivered actionable anomaly must have a durable disposition citing its identity and evidence:

1. Verified resolution, with fresh evidence establishing what changed.
2. An evidenced wait, naming the release condition and the mechanism that will revisit it; include a deadline when time bounds the wait.
3. An unresolved condition, naming the responsible role, supported next action and follow-up time or trigger.

Owner assignment alone, generic memory, an empty event delta and a repeated summary do not discharge an anomaly. Reuse existing work and dispositions instead of admitting duplicates. Repeated refusal and overdue follow-up remain visible on later passes until fresh evidence establishes resolution or a valid wait.

Coverage and health are separate results. A pass can account for every anomaly while reporting unresolved problems. Stale or unreadable evidence, omitted actionable identities or unsupported resolution claims prevent an unqualified complete result. Validate dispositions against the delivered observation and cited records before recording completion. Validation does not execute recovery or grant additional authority.

# Acceptance and existing work

Lifecycle recovery (yoyodyne-06m) must demonstrate a developer interrupted by redeploy after a dependency is added, absent worker ownership, durable dependency wait at the original development phase, freed capacity for other work, dependency closure, capacity reacquisition and continuation of the same run. Preserve provider session, artifacts and all counters. Add concurrent adoption, crash between durable writes, invalid-record refusal and uncertain-save readback cases. A live worker and uncertain presence must never be cancelled by age alone.

Shared health delivery (yoyodyne-e7c) must demonstrate fresh delivery to both managers with an empty event delta and old specification cache. Include workerless nonterminal runs, distinct newer-run identity on an item with an older stoppage, read failures, rendering limits and legitimate waits. Both managers must receive the same classification and evidence coverage.

Manager coverage validation (yoyodyne-5eu) must reject unsupported healthy or complete claims, missing anomaly dispositions and resolution supported only by owner assignment or memory. It must accept honest complete coverage with unresolved conditions, retain first observation across conversations, expose overdue recurrence and record resolution followed by reopening.

These are acceptance interfaces for already admitted work, not a decomposition or pull order. Immediate preserved-run recovery (yoyodyne-9j7) remains separate operational work. Recovery-policy reconciliation (yoyodyne-ifd.429.42) continues to own finite check-continuation policy; this design does not bypass dependencies or spend limits.

# Evidence and limits

Repository inspection used commit dfc0079ad98c. The continuation, redeploy, reconciliation, declarative workflow and dependency-test files were read in full. Earlier scheduler and state-schema reads were truncated, so their unseen portions are not verification evidence. Supplied tracker incident accounts are evidence of reported behavior, not execution verified by the architect. No tests were run during design inspection.
