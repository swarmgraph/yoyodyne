---
id: integration-through-a-merge-queue
kind: design
title: Integration through a merge queue
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-05T00:33:49.641499Z
      reason: 'yoyodyne-ifd.429.22: record portable ordered integration under one switch, exact candidate checks and independent review, separate worker and promotion leases, confirmed withdrawal and restart-safe reconciliation.'
approvals:
    - revision: 0
      by: operator
      at: 2026-10-05T00:33:49.641499Z
      reason: approved by the operator in conversation chat-11558d325e9a214ebfd00bb4a0012750, turn 147, for the document the architect wrote there (document-147.1)
---

# Purpose

This design serves the goal to isolate implementation in harness-managed worktrees and integrate successful work automatically. It records integration through a merge queue (yoyodyne-ifd.429.22). The queue prevents target movement from invalidating work between its final gate and landing.

# Decision

Use one configuration switch, execution.merge_queue, default false. False preserves existing integration, including pre-publication replay and its checks and independent review. True admits approved changes to a durable queue and omits the harness's pre-publication replay. Existing integration and publishing approval policies still apply; enabling the queue grants no authority.

Provide a harness-run queue on every supported forge. Use a forge-native queue only when its adapter can establish and enforce the same candidate-bound gate below. Queue availability alone is insufficient. Reject permanent dependence on GitHub's queue, checks bound only to a pull-request head, and a second switch controlling replay: each violates either portability, verification or the operator's one-switch decision.

# Durable admission and ownership

One queue exists per repository and target branch. Admission records an immutable entry identity and monotonically assigned order under a short queue-record lock. Deduplicate repeated admission of the same run and head. Record product, repository, work item title and identifier, run, publication, approved head, integration policy, chosen queue mode, admission time and predecessor order.

Only one worker holds the queue-worker lease for that repository and target. This lease owns selection and candidate generation; it is separate from the existing target promotion lease. Checks and review run outside the promotion lease. The promotion lease covers only fresh target validation, the guarded mutation and its durable handoff. Never retain it through provider work or a long remote wait.

Queue waiting alone does not occupy a developer slot. Any developer or reviewer invocation uses its existing capacity and spending rules. Queue-worker exclusivity must not be implemented by permanently occupying a developer slot.

# Harness-run queue

Process entries in admission order. Construct a candidate in a harness-managed checkout from the current authoritative target and the admitted head. Keep the source branch and original approval evidence intact. Record a generation containing the exact target base, contributing heads in order, candidate commit, content fingerprint and check configuration.

Run configured deterministic checks and obtain an independent reviewer verdict on that exact candidate. Original head approval authorizes admission; it does not authorize a different combined revision. Missing or incomplete checks and verdicts receive no credit.

Before promotion, acquire the target promotion lease and reread the target and applicable holds, directives, dependencies and approval policy. Promote only if the target still equals the recorded base and the candidate gate is valid. Use compare-and-swap or an equivalent protected publication operation; never force the target. Target movement invalidates the generation and its gate. Construct and verify a new generation without charging a repair merely for drift.

For a protected target, land through its authorized pull-request path and follow the confirmed remote result locally by fast-forward. For an unprotected target, retain the existing local promotion and publication authority. Neither path may rewrite an authoritative branch or bypass repository protection.

# Forge-native queue

The adapter must expose queue entry and group identity, exact target base, contributing heads, combined candidate commit, required checks and their configuration, independent approval evidence, queue timeout and confirmed landing identity. GitHub merge_group checks belong to the combined group commit, not the pull-request head. All-green grouping does not prove that configured harness checks and independent review occurred.

Use native mode only when those requirements are enforced before landing. Otherwise select the harness-run queue before admission and report why native mode is unsupported. If neither mode can satisfy repository protection, retain an actionable hold rather than bypassing it. A native queue timeout is recorded from the forge's actual policy; the reported sixty-minute setting is evidence about this repository, not a universal constant.

# Failure, withdrawal and continuation

Distinguish candidate conflict or failing work from infrastructure refusal, target failure, target drift and unreadable evidence. A file annotation is evidence, not sufficient proof of causation. Reuse target-failure work and existing triage records rather than creating duplicate repairs.

Before rewriting a queued head, handing it to repair or transferring queue mode, establish that its previous merge authority was withdrawn. Reuse the existing disable-auto-merge plus dequeue operation. On ambiguous withdrawal, read queue and merge state before proceeding. A request already landed goes to completion reconciliation, not replay.

Preserve the same run and its artifacts on a recoverable stop. Reuse supported integration continuation where its preconditions hold. Record the refusal and next mover when continuation is not yet possible. Apply existing finite retry and continuation budgets durably; restarts and mode changes do not reset them. Genuine candidate changes re-earn checks and independent review.

# Crash recovery and disabling

Persist intended mutations before requesting them, with pinned identities or idempotency keys. After restart, observe target, publication and queue state before repeating a mutation. Separate tracker, run, queue and docket writes require explicit partial-handoff reconciliation; no ordering makes them an atomic transaction. Uncertain saves require durable readback. A missing worker does not authorize deleting useful preserved work.

Disabling the switch blocks new queue admissions. Entries already admitted drain in their recorded mode; disabling does not silently transfer or re-arm them. An explicit transfer first establishes withdrawal from the old mode, preserves ordering and history, then earns a fresh candidate gate. New integration uses the existing replay path when the switch is false.

# Existing mechanisms and acceptance

The target promotion lease and shared build cache are reused. Post-landing checks supplement verification but do not replace the pre-landing candidate gate. Existing publication reconciliation, withdrawal, integration continuation, ownership resolution and recovery bounds remain the mechanisms for their respective duties.

Verification must cover two concurrent admissions, competing workers, target movement during checks and review, candidate or configuration changes, restart before and after each remote mutation, uncertain saves, partial tracker/run writes, confirmed withdrawal, queue drops and timeouts, target failures, exhausted budgets, protected targets, grouped forge candidates, and disable/drain/transfer behavior. Assert that the landed revision is the gated candidate, no stale verdict authorizes a new generation, and neither duplicate execution nor lost preserved work follows a restart.

The development manager owns decomposition; the Lead Product Manager owns implementation admission and order. This document creates no work items.

# Evidence and publication limits

Inspected queuedchecks.go, integrationresume.go and internal/publish/checks.go in full at 1c880b64631b. The existing forge check reader queries pull-request-head checks and is not a native queue candidate gate. Existing withdrawal disables auto-merge and dequeues. V1's relevant integration sections were truncated in supplied reads, so their companion revisions remain to reconcile and publish. This design does not claim implementation, successful checks or completion of the parent work item.
