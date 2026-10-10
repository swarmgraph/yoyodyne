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
    - action: amended
      by: architect
      at: 2026-10-09T04:07:34.174597Z
      reason: 'yoyodyne-jj8 - record the ruling of October 8: with execution.merge_queue on, the target must not require GitHub''s merge queue, the harness''s queue alone orders merges, and the harness refuses queue mode and holds waiting changes when the requirement is present or protection cannot be read; state what protects merging with the switch off once GitHub''s requirement is removed'
approvals:
    - revision: 0
      by: operator
      at: 2026-10-05T00:33:49.641499Z
      reason: approved by the operator in conversation chat-11558d325e9a214ebfd00bb4a0012750, turn 147, for the document the architect wrote there (document-147.1)
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-09T04:07:34.174597Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 633, for document-633.1 under the automatic approval policy
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

# GitHub's own merge queue

With execution.merge_queue true, the target branch must not require GitHub's merge queue. GitHub's queue builds a group commit and merges it once the checks GitHub runs on that commit pass. The harness never checked or reviewed that commit, so a change would land on a revision no gate of the harness approved. The harness's queue is therefore the only thing that orders merges into the target. Native mode on GitHub is not selected, whatever the adapter can read, until a later revision of this design names an adapter that enforces the candidate gate above.

The harness reads the target branch's protection, including rulesets, when it reads the switch at start and again before each admission. Where it finds GitHub's merge queue required, or cannot read the protection at all, it refuses to run in queue mode:

- It admits nothing and starts no queue worker.
- Approved changes waiting for integration are held. The hold names the target branch, the setting, and what clears it: a person with administration rights on the repository removing the requirement, or the switch being turned off.
- Entries already admitted stay as recorded. They are not transferred and not asked to merge again, and they continue once the requirement is gone.
- The refusal is shown by `yoyo doctor` and `yoyo status` and reported once, not on every poll.

The harness never turns the switch off by itself, never falls back to the replay path while the switch is on, and never changes the repository's protection. Removing GitHub's requirement is a repository setting that a person changes before the switch is turned on, and `yoyo doctor` names it.

# Merging with the switch off

With the switch off and GitHub's merge queue no longer required, four things protect what merges into the target:

1. The existing replay of the approved change onto the current target, with the configured checks and an independent review of that replayed revision before it is published.
2. The promotion lease of the target branch, so the harness's own promotions into one target happen one at a time, each a compare-and-swap from the recorded base.
3. A merge request pinned to the replayed commit and confirmed by containment, so the forge cannot merge a different head under the harness's name.
4. Branch protection on the target that still requires the configured status checks and requires a pull request's branch to be up to date with the target before it merges. A head that fell behind is refused by the forge and comes back through the existing replay of a lost race, so it never lands untested against a newer base.

The fourth is a repository setting. `yoyo doctor` reports when it is missing, and the harness does not set it. A merge somebody makes by hand outside the harness is recorded as an intervention, and none of these protects it.

# Failure, withdrawal and continuation

Distinguish candidate conflict or failing work from infrastructure refusal, target failure, target drift and unreadable evidence. A file annotation is evidence, not sufficient proof of causation. Reuse target-failure work and existing triage records rather than creating duplicate repairs.

Before rewriting a queued head, handing it to repair or transferring queue mode, establish that its previous merge authority was withdrawn. Reuse the existing disable-auto-merge plus dequeue operation. On ambiguous withdrawal, read queue and merge state before proceeding. A request already landed goes to completion reconciliation, not replay.

Preserve the same run and its artifacts on a recoverable stop. Reuse supported integration continuation where its preconditions hold. Record the refusal and next mover when continuation is not yet possible. Apply existing finite retry and continuation budgets durably; restarts and mode changes do not reset them. Genuine candidate changes re-earn checks and independent review.

# Crash recovery and disabling

Persist intended mutations before requesting them, with pinned identities or idempotency keys. After restart, observe target, publication and queue state before repeating a mutation. Separate tracker, run, queue and docket writes require explicit partial-handoff reconciliation; no ordering makes them an atomic transaction. Uncertain saves require durable readback. A missing worker does not authorize deleting useful preserved work.

Disabling the switch blocks new queue admissions. Entries already admitted drain in their recorded mode; disabling does not silently transfer or re-arm them. An explicit transfer first establishes withdrawal from the old mode, preserves ordering and history, then earns a fresh candidate gate. New integration uses the existing replay path when the switch is false.

# Existing mechanisms and acceptance

The target promotion lease and shared build cache are reused. Post-landing checks supplement verification but do not replace the pre-landing candidate gate. Existing publication reconciliation, withdrawal, integration continuation, ownership resolution and recovery bounds remain the mechanisms for their respective duties.

Verification must cover two concurrent admissions, competing workers, target movement during checks and review, candidate or configuration changes, restart before and after each remote mutation, uncertain saves, partial tracker/run writes, confirmed withdrawal, queue drops and timeouts, target failures, exhausted budgets, protected targets, grouped forge candidates, and disable/drain/transfer behavior. It must also cover a target that requires GitHub's merge queue while the switch is on, and a target whose protection cannot be read: in both, nothing is admitted, waiting changes are held with the hold named, and nothing falls back to replay. Assert that the landed revision is the gated candidate, no stale verdict authorizes a new generation, and neither duplicate execution nor lost preserved work follows a restart.

The development manager owns decomposition; the Lead Product Manager owns implementation admission and order. This document creates no work items.

# Evidence and publication limits

Inspected queuedchecks.go, integrationresume.go and internal/publish/checks.go in full at 1c880b64631b. The existing forge check reader queries pull-request-head checks and is not a native queue candidate gate. Existing withdrawal disables auto-merge and dequeues. V1's relevant integration sections were truncated in supplied reads, so their companion revisions remain to reconcile and publish. The sections on GitHub's own merge queue and on merging with the switch off record the architect's ruling of October 8 (yoyodyne-jj8), made against this design as read at 1260156da66c; the code for queue mode selection was not read for this revision. This design does not claim implementation, successful checks or completion of the parent work item.
