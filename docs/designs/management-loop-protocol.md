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
approvals:
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-14T08:53:06.354907Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 920, for document-920.1 under the automatic approval policy
---
# The management-loop protocol: request kinds, readiness, outcome summaries, and execution profiles

## What this is for

[Management-and-supervision](management-and-supervision.md) governs the contract: typed persisted requests, the harness as only invoker, wakeups, bounded cycles, advisory readiness. This design specifies the protocol that runs under it — what the requests say, what readiness records hold, what comes back after integration, and how execution profiles are chosen — so the loop's implementations build against a vocabulary somebody ratified rather than one that accretes. It serves the autonomy goal: the operator governs outcomes, and routine coordination stops passing through them.

## Request kinds

Three, deliberately: **consult** — ask another role for a judgment within that role's authority; **clarify** — request information the caller needs to complete its own judgment; **escalate** — ask the Lead Product Manager to obtain an operator decision because delegated authority or policy is insufficient. The existing inter-role ask exchange is the transport for consult and clarify, keeping its ratified properties — judgment-only, decisionless, durable and visible, round-capped with unresolved-at-cap escalation and per-exchange cost. Later kinds (`review`, `handoff`, `notify`) arrive only for demonstrated workflows with distinct semantics, never to mirror conversational phrasing. A request is accepted only when the source–target pairing, kind, scope, and requested decision fit both roles' authority; it is persisted before the target is invoked, supplied referenced evidence rather than an unbounded transcript, recorded against its id, and completed by waking the source — all per the umbrella contract.

## Readiness

Three independently owned judgments, each revision-aware, advisory first and a scheduler gate only after the umbrella's promotion criterion is met:

- **Product readiness** — Lead Product Manager: intended outcome, approved goal, priority, product acceptance, source revisions.
- **Architecture readiness** — architect, with a four-value disposition: **clear**, **design-needed**, **cross-cutting**, or **investigation-needed**, naming affected designs, decisions, invariants, interfaces, and revisions. Investigation is commissioned as bounded, read-only work — the architect gains no write-shell authority from needing evidence.
- **Delivery readiness** — development manager: one item or a child graph, dependencies, sequential and parallel boundaries, conflict surfaces, complexity, risk, required capabilities, execution profile, source revisions.

A stale judgment blocks only the item that depends on it and wakes its owning role. Planning applies to a **bounded horizon** — the next admitted epic or the top group of ready candidates, never the whole backlog — with shared context batched across related items; the horizon and the batching are the loop's cost discipline, stated as design so no implementation treats backlog-wide review as thoroughness.

## Outcome summaries

After integration the harness derives role-specific summaries from the accepted diff, check evidence, verdict, and recorded plan: the **Lead Product Manager** receives user-visible behavior, acceptance evidence, omissions, release implications, and divergence from the requested outcome; the **architect** receives changed components, interfaces, schemas, dependencies, invariants touched, and any implementation decision not represented in the design; the **development manager** receives actual effort, conflicts, repairs, failure modes, routing results, and whether the decomposition held. **Material divergence reopens the applicable readiness judgment** rather than resting in a run note.

## Escalation shape

One coherent escalation from the Lead Product Manager, whatever roles contributed: what outcome is blocked; why the operator rather than an agent; the options in nontechnical terms; impact, cost or delay, and risk per option; the recommendation with the contributing role's concern; and what continues meanwhile. This refines the umbrella's conflict-handling section, which owns when escalation happens.

## Execution profiles

The development manager selects among five named profiles — **mechanical-low-risk**, **standard**, **high-judgment**, **architecture-sensitive**, **specialist** — never a provider model string; operator policy maps profiles to configured agents and models, changeable without rewriting work items. Risk rules may **promote a profile and never silently demote one**; a failed low-cost attempt escalates once to a stronger profile with reason and added cost recorded; high-risk work — security-sensitive, migrations, concurrency, public interfaces, protected architecture areas — never enters the lowest-cost lane. Independent review and every deterministic gate are identical across profiles, per the routing clause the Claude execution design already binds. Routing is enforced only after phase-level cost, repair, latency, and outcome evidence exists (yoyodyne-ifd.83's line).

## Role boundaries, condensed

The authority tables in code and in the harness design remain the enforcement; this restates the planning-level boundary each role holds in the loop: the Lead Product Manager owns intent, priority, admission, acceptance, and the planning horizon, and does not own decomposition or model choice; the architect owns designs, decisions, invariants, and the architecture disposition, and does not set priority or delivery plans; the development manager owns decomposition, shape, conflict surfaces, and profile selection, and does not redefine intent or settle architecture; the harness validates, persists, invokes, and records, and invents no judgment.

## The demonstration bar

The loop is done when the brief's nine scenarios demonstrably pass — restart-surviving consults, cross-cutting dispositions that block only affected work, decomposition the scheduler respects, clarifications that refresh readiness, divergence reopening judgments, plain-language escalations, low-cost routing under unchanged gates, single bounded promotion on failure, and idempotency under duplicates, crashes, and open consoles. Those scenarios are this design's acceptance criteria, and the hardening step makes the autonomous path the default while direct role chat stays as inspection and recovery.
