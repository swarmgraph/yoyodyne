---
id: management-loop-protocol
kind: design
title: 'The management-loop protocol: request kinds, readiness, outcome summaries, and execution profiles'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-08-23T18:42:25Z
      reason: promoted from the operator's autonomous-management-loop brief of 2026-08-23 under the promotion mandate; a companion to management-and-supervision, which owns the contract this protocol runs under. Deviations recorded in the promotion
    - action: amended
      by: architect
      at: 2026-10-14T08:53:06.354907Z
      reason: 'Part 2 of 10 of renaming the product manager role to Lead Product Manager in governed documents (the rename item, yoyodyne-ifd.437.10): the role''s display name changes in the escalate request kind, product readiness, outcome summaries, the escalation shape, and the role boundaries; nothing else changes.'
    - action: amended
      by: architect
      at: 2026-11-19T16:00:58.157868Z
      reason: 'Coined-vocabulary replacement, one document of the architect''s part (yoyodyne-ifd.437.21): the retired word ''gate'' is replaced in its three places, each by what it meant - the scheduler holding work on a readiness judgment, and the checks a change must pass - following the register of terms. Nothing else changes.'
    - action: amended
      by: architect
      at: 2026-11-20T01:34:44.257803Z
      reason: 'Plain wording, one more document of the architect''s part of the vocabulary replacement: words a reader would have to look up are replaced by what they mean - the inter-role ask exchange, the umbrella contract, the promotion criterion, accretes, round-capped, write-shell, lane, promote and demote, hardening step, idempotency and open consoles. The round-limit sentence states how an unresolved thread ends today. Nothing else changes.'
    - action: amended
      by: architect
      at: 2026-11-22T21:17:49.508009Z
      reason: 'Design review against the code at 7120a8560: adds a section saying what is built. One kind of question between roles exists, with no kind field; the three owned readiness judgments, summaries after landing and execution profiles are not built; the readiness package is a narrower check of an item''s own citations and waiting sentences. The round limit sentence now says the harness tells the operator, as the code does.'
approvals:
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-14T08:53:06.354907Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 920, for document-920.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-11-19T16:00:58.157868Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3089, for document-3089.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 3
      by: harness
      at: 2026-11-20T01:34:44.257803Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3147, for document-3147.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 4
      by: harness
      at: 2026-11-22T21:17:49.508009Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3563, for document-3563.1 under the automatic approval policy
---
# The management-loop protocol: request kinds, readiness, outcome summaries, and execution profiles

## What this is for

[Management-and-supervision](management-and-supervision.md) governs the contract: typed persisted requests, the harness as only invoker, wakeups, bounded cycles, advisory readiness. This design specifies the protocol that runs under it — what the requests say, what readiness records hold, what comes back after integration, and how execution profiles are chosen — so the loop's implementations build against a vocabulary somebody agreed rather than one that grows by accident. It serves the autonomy goal: the operator governs outcomes, and routine coordination stops passing through them.

## Request kinds

Three, deliberately: **consult** — ask another role for a judgment within that role's authority; **clarify** — request information the caller needs to complete its own judgment; **escalate** — ask the Lead Product Manager to obtain an operator decision because delegated authority or policy is insufficient. The existing way one role puts a question to another, carried and recorded by the harness, is how consult and clarify are sent, and it keeps the properties already agreed for it: the answer is judgment only and decides nothing, every round is recorded and visible to the operator, the number of rounds is limited and the harness closes a thread that reaches the limit as unresolved and tells the operator, and each thread's cost is recorded. Later kinds (`review`, `handoff`, `notify`) arrive only for demonstrated workflows with distinct semantics, never to mirror conversational phrasing. A request is accepted only when the source–target pairing, kind, scope, and requested decision fit both roles' authority; it is persisted before the target is invoked, supplied referenced evidence rather than an unbounded transcript, recorded against its id, and completed by waking the source — all as the contract in management-and-supervision requires.

## Readiness

Three independently owned judgments, each revision-aware and advisory first; the scheduler holds work on them only after the condition management-and-supervision sets for that is met:

- **Product readiness** — Lead Product Manager: intended outcome, approved goal, priority, product acceptance, source revisions.
- **Architecture readiness** — architect, with a four-value disposition: **clear**, **design-needed**, **cross-cutting**, or **investigation-needed**, naming affected designs, decisions, invariants, interfaces, and revisions. Investigation is commissioned as bounded, read-only work — the architect gains no authority to run commands that write from needing evidence.
- **Delivery readiness** — development manager: one item or a child graph, dependencies, sequential and parallel boundaries, conflict surfaces, complexity, risk, required capabilities, execution profile, source revisions.

A stale judgment blocks only the item that depends on it and wakes its owning role. Planning applies to a limited set of work — the next admitted epic or the top group of ready candidates, never the whole backlog — with shared context batched across related items; that limit and the batching are how the loop keeps its cost down, stated as design so no implementation treats backlog-wide review as thoroughness.

## Outcome summaries

After integration the harness derives role-specific summaries from the accepted diff, check evidence, verdict, and recorded plan: the **Lead Product Manager** receives user-visible behavior, acceptance evidence, omissions, release implications, and divergence from the requested outcome; the **architect** receives changed components, interfaces, schemas, dependencies, invariants touched, and any implementation decision not represented in the design; the **development manager** receives actual effort, conflicts, repairs, failure modes, routing results, and whether the decomposition held. **Material divergence reopens the applicable readiness judgment** rather than resting in a run note.

## Escalation shape

One coherent escalation from the Lead Product Manager, whatever roles contributed: what outcome is blocked; why the operator rather than an agent; the options in nontechnical terms; impact, cost or delay, and risk per option; the recommendation with the contributing role's concern; and what continues meanwhile. This refines the conflict-handling section of management-and-supervision, which owns when escalation happens.

## Execution profiles

The development manager selects among five named profiles — **mechanical-low-risk**, **standard**, **high-judgment**, **architecture-sensitive**, **specialist** — never a provider model string; operator policy maps profiles to configured agents and models, changeable without rewriting work items. Risk rules may **move work to a stronger profile and never silently to a weaker one**; a failed low-cost attempt moves once to a stronger profile with reason and added cost recorded; high-risk work — security-sensitive, migrations, concurrency, public interfaces, protected architecture areas — never runs under the lowest-cost profile. Independent review and every deterministic check are identical across profiles, per the routing clause the Claude execution design already binds. Routing is enforced only after phase-level cost, repair, latency, and outcome evidence exists, the condition recorded on work item yoyodyne-ifd.83.

## Role boundaries, condensed

The authority tables in code and in the harness design remain the enforcement; this restates the planning-level boundary each role holds in the loop: the Lead Product Manager owns intent, priority, admission, acceptance, and which work is planned next, and does not own decomposition or model choice; the architect owns designs, decisions, invariants, and the architecture disposition, and does not set priority or delivery plans; the development manager owns decomposition, shape, conflict surfaces, and profile selection, and does not redefine intent or settle architecture; the harness validates, persists, invokes, and records, and invents no judgment.

## What is built

Checked against the code on main at commit 7120a8560 on November 22, 2026. Most of this protocol is not built. What exists, and where it differs from the sections above:

- **Request kinds.** One kind of request is built: a role puts a question to another role through the harness, which records every round, attaches the records the asker names, refuses an answer that carries any action, limits the rounds, and closes a thread that reaches the limit as unresolved and tells the operator. The request carries no kind, so consult and clarify are not told apart; both are sent as the same question. Escalation is not a request kind: a role that needs the operator files a report whose message opens with one of the operator's closed list of reasons, and the Lead Product Manager records it as his. None of the later kinds exists.
- **Readiness.** None of the three owned readiness judgments is recorded anywhere. What the harness has under the name readiness, in internal/readiness, is a narrower check: before an item is pulled, it reads the four fields somebody wrote for the item and reports a cited file and line or a qualified Go name that the code no longer holds, and any sentence in which the item says it must wait for something. Each finding names who releases it. It holds nothing back by itself and records nothing; the caller decides what to do with it. Holding an item until its design is readable is the subject of [the needs-design label design](needs-design-gate.md), not of this one.
- **Outcome summaries.** Not built. Nothing derives a summary for any role after a change lands, and nothing reopens a judgment on divergence.
- **Escalation shape.** No code checks it; this review found nothing that does. It is guidance the Lead Product Manager follows when she writes to the operator.
- **Execution profiles.** Not built. No profile names exist anywhere in the code; work runs on the agent and model configured for its role or developer slot. The nearer plan for running some work on a cheaper model first is in [reducing what the factory costs](reducing-what-the-factory-costs.md).

The demonstration bar below is therefore not met. This design stays the agreed vocabulary for when the loop is built, and work that builds any part of it starts from this section rather than from the description above.

## The demonstration bar

The loop is done when the brief's nine scenarios demonstrably pass — consults that survive a restart, cross-cutting dispositions that block only affected work, decomposition the scheduler respects, clarifications that refresh readiness, divergence reopening judgments, plain-language escalations, low-cost routing under unchanged checks, a single move to a stronger profile on failure, and the same result when a request arrives twice, when a process crashes, or while a conversation is open. Those scenarios are this design's acceptance criteria, and the last step makes the autonomous path the default while direct role chat stays as inspection and recovery.
