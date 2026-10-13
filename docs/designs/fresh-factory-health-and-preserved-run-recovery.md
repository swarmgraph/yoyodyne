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
      at: 2026-10-13T17:38:15.682146Z
      reason: 'yoyodyne-ab2: adds the callable evidence and recovery contract the development manager found missing, the development-manager, architect and shared prompt clauses with where each goes, the split between what prompts ask and what code enforces, scenario acceptance, and the bounded slices for shared health delivery (yoyodyne-e7c), lifecycle recovery (yoyodyne-06m) and manager coverage validation (yoyodyne-5eu). The existing text is unchanged.'
approvals:
    - revision: 0
      by: operator
      at: 2026-10-05T00:33:22.166145Z
      reason: approved by the operator in conversation chat-11558d325e9a214ebfd00bb4a0012750, turn 135, for the document the architect wrote there (document-135.1)
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-13T17:38:15.682146Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 827, for document-827.1 under the automatic approval policy
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

If unfinished dependencies prevent continuation, record the dependency wait under the run lease without advancing the interrupted phase. Only a successfully established durable wait releases capacity. The duplicate-run reservation stays active. When dependencies clear, reacquire capacity and clear the wait under the same reservation lock used by fresh reservations, while retaining the run lease. A capacity refusal leaves the durable wait and interrupted phase intact.

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

# Addendum: the callable contract and the management prompts

This addendum answers the prompt rules and recovery contract item (yoyodyne-ab2). It adds what a manager can call, what each call promises, what the development manager's and the architect's prompts say, and which of those rules code enforces rather than asks for. It changes no role's authority and adds no store: every capability below reads or writes the records the sections above already name, and every one is a tool under [the tool interface](tool-interface.md), with its descriptor, grant, redaction, framing and audit as that design states for every tool.

The division of work stays as above. Shared health delivery (yoyodyne-e7c) owns inspection and verification. Lifecycle recovery (yoyodyne-06m) owns mutation. Manager coverage validation (yoyodyne-5eu) owns disposition and completion validation.

## The capabilities

`health.observe`, class read, built by shared health delivery. Request: the product, and optionally up to twenty runs or work items to narrow it. Result: an observation identifier, the observation time, each evidence source with whether it was read whole, partly, or not at all and why, and one entry per run with every field the shared lifecycle account lists. It also carries:

- each anomaly with its identity, first and latest observation, and how many passes it has recurred on;
- the count of ready priority 0 items that have had no run for more than a day, each named;
- for each run, how many times each check has been started on it;
- for each harness service, the environment it was actually started with: the path to the binary it runs and that binary's build, its search path for programs, and each configuration file it names with whether that file exists. This is how a pass establishes configuration health rather than guessing it, and it covers the development manager's report of a supervisor configuration pointing at a file missing from its test directory.

A result larger than the rendering bound returns exact totals, the identities it left out, and a cursor.

`health.follow`, class read. Request: an observation identifier and a cursor. Result: the next part of what that observation left out. An observation older than fifteen minutes is refused with the reason, and the caller observes again.

`run.inspect`, class read, built by shared health delivery. Request: one run. Result: the run record, cut at 48 KiB with the cut declared; the lease holder; the worker's identity as defined below and whether it is live, absent, or uncertain; whether the branch and worktree exist; the reservation; any wait and its release condition; the last continuation attempt and refusal.

`run.verify`, class read, built by shared health delivery. Request: one run and one expected state, from this closed list: worker live, durable wait recorded, reservation released, continuation started, integration completed, unresolved hold recorded. Result: met, not met, or unknown, with the evidence read and its observation time. Unknown is never reported as met.

`run.recover`, class act, built by lifecycle recovery. Request: a request identifier the caller makes up and never reuses, one run, the observation identifier the decision rests on, the saved status and phase the caller expects, one action, and a reason. The actions are a closed list: continue the same run; record a dependency wait; complete the steps after a recorded integration; release the reservation of a terminal run with no worker; record an unresolved hold naming its owner, next action and follow-up. Result: applied, refused, or unknown, as defined below.

Dispositions are recorded where they are recorded now, on the pass record, and manager coverage validation checks them. No new capability is added for them.

## What binds a request

Every `run.recover` request carries the product, the run, the observation identifier and the expected status and phase. The harness takes the run lease, reloads the record strictly and compares. If anything differs from what the caller expected, the request is refused as state moved, nothing is changed, and the result names what differs. An observation older than fifteen minutes is refused for any act.

## Grants and bounds

The development manager's bundle holds all five. The factory-flow program manager's bundle holds `health.observe`, `health.follow`, `run.inspect` and `run.verify`, and never `run.recover`, as the authority section above already says. The architect's bundle holds `health.observe`, `health.follow` and `run.inspect`, so a design can rest on how runs actually behave. A grant is a row in the bundle under the tool interface's grant rules; a prompt naming a capability grants nothing.

One turn or pass may make at most five `run.recover` requests, and at most one for any one run. A refusal for that bound says so and names the next pass.

## Which worker is which

A worker's identity is its process number together with the process start time, the identity of the machine boot it started under, and the run attempt number recorded when it was launched. A worker is live only when all four match a running process. A process number alone never establishes it, because the number can be reused. Before any write, `run.recover` checks under the lease that the attempt number still matches; a replacement worker started meanwhile makes the request refused, never applied to the wrong worker. Ownership is checked again under the lease before each durable write, not only at the start.

## Request identity and repeats

The request identifier and run are written on the run record before any change is made. A repeat of the same identifier returns the first result and changes nothing. The same identifier with a different run or action is refused.

## Applied, refused and unknown

Applied means the change is durable and was read back. Refused means nothing was changed; the result carries the reason, and, where another role or rung can act, which one. Unknown means a write was attempted and reading it back could not establish whether it happened. On unknown, the harness reloads under the lease as the preserved-run recovery section requires. If it still cannot tell, it records an unresolved hold owned by the development manager, with the last attempted operation, and her next pass verifies it.

Applied is not an outcome. A disposition of verified resolution needs a `run.verify` result of met, observed after the act. Manager coverage validation refuses any resolution without one.

## The same on every path

Every capability is reached the same way from a turn in the owning conversation and from a scheduled pass, through the harness's typed request blocks, on Claude Code and on Codex. None of them is a provider's own tool. Shared health delivery carries a test that makes each call on both providers and from both kinds of turn and gets the same result.

## Who says what

- The tool descriptor, in the tool interface's registry, names each capability's class and which bundles hold it.
- The fixed role contract, assembled in `internal/chat/role.go`, states each request and result shape exactly as above.
- The scheduled evidence is the fresh health observation delivered at the start of each pass.
- The persona says when to reach for each capability, in the clauses below.

## The prompt clauses

### Shared, for every role

Location: the shared ground every role conversation is built from, in `internal/chat/role.go`, as one named text so it is written once.

> Before you add a dependency or ask another role or the operator to decide something, name the exact question still open and say why your own authority and the evidence you hold cannot answer it. Before you report progress, say which you have: a diagnosis, a decision, an action carried out, or a verified outcome. Claim only that one.

### The development manager

Location: the development manager's persona, the shipped template and the live copy the role is started with.

> 1. When work stops, find the exact step that failed, the current evidence for it, and the supported next action. That an existing item covers it is not a recovery decision.
> 2. A decision is not finished until it has produced a running worker, a reservation released and verified, or a wait or refusal that names its owner and when it will be tried again. Verify it with run.verify before you call it resolved.
> 3. Read the recurrence counts in your health observation. When a failure keeps returning, priority 0 work keeps waiting, or a check keeps being restarted, say so as a pattern and act on it, even when each single event looks handled.
> 4. When a service or role fails, look at the environment it actually ran with, from your health observation, not at a workaround that works in a terminal.
> 5. Choose the smallest supported action that restores progress while keeping the work and every check it still owes.

### The architect

Location: the architect's persona, the shipped template and the live copy the role is started with.

> 1. If a capability needs a design that cannot be published without that capability, say so and solve the order of the two in the design.
> 2. Separate what can ship under current policy from what truly needs a new ruling, and design the first so it can ship now.
> 3. Every design says how the thing recovers, who owns it when it stops, what happens on restart, what a person can see of its outcome, and how records already broken are repaired.
> 4. A missing tool or a limit of the code as built is an engineering problem to design for, not a reason to ask a person. A real safety boundary stays where it is.
> 5. Count what the design costs in throughput: checks repeated, work interrupted by a deploy, the scheduler held up, as well as whether one part works alone.

## What prompts ask and what code enforces

Code enforces these, and a prompt that said otherwise would change nothing:

- a resolution needs a verify result of met, observed after the act;
- a stale observation is refused for acts and follow reads;
- a worker is identified by all four parts of its identity;
- a request is bound to the state it expected and refused when that state moved;
- a repeated request identifier returns its first result;
- the grants above, and the five acts a pass;
- missing, stale or partial evidence is never shown as healthy.

Prompts carry the judgment code cannot make: tracing a stop to its step, seeing a pattern, preferring the smallest action, looking at the real environment, solving the order of a design and its capability, and counting throughput.

## Scenarios that test outcomes

Each is tested by what happens, not by whether a prompt contains a sentence.

- (a) Maintenance failures recorded but not delivered: a maintenance failure recurring on three passes reaches the development manager's observation with its recurrence count, and her pass cannot complete without a disposition for it. The delivery fix is the recurring maintenance failure item (yoyodyne-ifd.428.72).
- (b) A repair decided and never carried out: a decided repair with no worker after the decision shows as an anomaly, and resolving it needs run.verify to report continuation started. Owners: shared health delivery (yoyodyne-e7c) and the preserved-run recovery item (yoyodyne-9j7).
- (c) A stale reservation holding the fix that would release it: run.recover can release the reservation of a terminal run with no worker, and verify reports it released. Owners: lifecycle recovery (yoyodyne-06m) and the stale reservation item (yoyodyne-ifd.428.68).
- (d) Publication waiting on a ruling it cannot publish: the architect's clause 1 names the order; no code test, since the fix is the publication items (yoyodyne-ifd.433.21.1, yoyodyne-ifd.437.14).
- (e) A search path supplied by hand hiding a broken service: the observation shows the service's real search path and binary, and a pass reporting the service healthy while its binary is missing from that path is refused by coverage validation. Owner of the service fix: yoyodyne-ifd.435.12.
- (f) Deploy drains restarting long checks: the observation's restart count per check shows it; the capacity fix is the heavy check capacity design for yoyodyne-ifd.429.43.
- (g) A worker starting, or an evidence-only pull request merging, mistaken for recovery: a disposition of verified resolution resting on either is refused by coverage validation (yoyodyne-5eu) unless run.verify reports the expected state met.
- (h) Dashboard test state returning after a restart and old service copies running: the observation names each service's build, and a copy on an older build than the product's is an anomaly. Owner of the fix: yoyodyne-ifd.434.19.

## Bounded slices

These are for the Lead Product Manager to release or re-scope the three parked items against, and for the development manager to decompose. Each can be verified on its own.

1. Shared health delivery, part one: `health.observe` and `health.follow`, with recurrence counts, waiting priority 0 work, check restart counts and service environment. Needs nothing new.
2. Shared health delivery, part two: `run.inspect` and `run.verify`, with the four-part worker identity, and the test that both providers and both kinds of turn get the same result. Needs nothing new.
3. Lifecycle recovery, part one: `run.recover` with request binding, request identity, the closed action list, and applied, refused and unknown. Needs slice 2 for worker identity.
4. Lifecycle recovery, part two: reading back an unknown write and recording the unresolved hold, and reconciling stops at redeploy with no worker. Needs slice 3.
5. Manager coverage validation: refusing a resolution without a later verify result, and refusing a healthy claim the observation contradicts. Needs slices 1 and 2.
6. The prompts: the shared text in `internal/chat/role.go` and the clauses in the two shipped persona templates, with a record on each turn and pass of which version of the shared text and persona it was started with. Needs nothing; the clauses are written to work with today's tracker and report evidence and name the capabilities that do not yet exist only as what to use once present.

## The shortest path under current policy

Slices 1, 2 and 6 need no new ruling and can ship now. Slice 6 alone already changes how both managers judge and report. Copying the clauses into the live persona files the roles are started with is a person's hand, because the harness may not write those files; the operator does it once slice 6 lands. Adoption is verified by the record on each turn and pass showing the new persona version, not by the template alone.

# Evidence and limits

Repository inspection used commit dfc0079ad98c. The continuation, redeploy, reconciliation, declarative workflow and dependency-test files were read in full. Earlier scheduler and state-schema reads were truncated, so their unseen portions are not verification evidence. Supplied tracker incident accounts are evidence of reported behavior, not execution verified by the architect. No tests were run during design inspection.

The addendum was written from this design as it stood at commit 968d33ed and from the prompt rules and recovery contract item (yoyodyne-ab2) read in full on 13 October. The paths of the persona templates and live persona files were not read; slice 6 names them and shows the clauses in each role's assembled prompt.