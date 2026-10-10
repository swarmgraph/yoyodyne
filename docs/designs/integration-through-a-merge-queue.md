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
      at: 2026-10-09T13:55:08.84697Z
      reason: 'yoyodyne-jj8: with the merge queue switched on, the target branch must not require GitHub''s own merge queue; the harness refuses queue mode, holds waiting changes naming the setting, never falls back to replay, reports once and changes no settings. Also states how merging stays safe with the switch off. Sent again whole after the earlier run (document-633.1) was stopped during make race without a reviewer judging it.'
approvals:
    - revision: 0
      by: operator
      at: 2026-10-05T00:33:49.641499Z
      reason: approved by the operator in conversation chat-11558d325e9a214ebfd00bb4a0012750, turn 147, for the document the architect wrote there (document-147.1)
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-09T13:55:08.84697Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 645, for document-645.1 under the automatic approval policy
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

No adapter today meets the requirements in Forge-native queue for GitHub's own merge queue: GitHub builds its own combined commit and lands it, and the harness cannot bind its configured checks and an independent review to that commit. So with execution.merge_queue true, the target branch must not require GitHub's merge queue.

With the switch on, the harness reads the target branch's protection before it admits anything. If that protection requires GitHub's merge queue, or the protection cannot be read, the harness:

- does not run in queue mode for that repository and target, and admits nothing to its queue;
- holds every change waiting to be admitted, and the hold names the setting that caused it: the branch rule that requires GitHub's merge queue, or the protection read that failed and why;
- never falls back to replay for those changes while the switch is on, because the operator chose the queue by that switch, and replay would land work through the path they turned off;
- files one report for the repository and target when the condition is first found, and no further report until it has cleared and been found again;
- never changes repository settings itself. Removing the requirement, or granting the access the read needs, is for a person.

The hold lifts by itself at the first later read that finds the protection readable and GitHub's merge queue not required; the waiting changes are then admitted in the order they were approved. Changes already admitted before the condition appeared are not landed while it stands: their candidates wait, with their gates kept, and a gate that the target has since moved past is earned again as usual.

Alternatives rejected: running the harness queue and then handing its candidate to GitHub's queue, because GitHub regroups and lands a commit no harness check or review judged; falling back to replay without saying so, because it overrides the one switch the operator set; changing branch protection, because no agent may change repository settings.

# Merging with the switch off

With execution.merge_queue false, integration is the existing path, and it still guards against the target moving between the final gate and landing:

- the approved change is replayed onto the current target, and configured checks and an independent review run on the replayed revision;
- promotion happens under the target promotion lease, with compare and swap against the target the replay was built on, so a moved target sends the change back to replay instead of landing;
- a merge through the forge names the exact reviewed commit, and the forge refuses it if the pull request's head has moved, so nothing other than the reviewed revision is merged.

On GitHub, the target branch's protection should require the configured checks and require a branch to be up to date before merging. yoyo doctor reports either one missing, naming the setting, and the harness never changes it. A missing setting does not stop integration, because the lease and the pinned merge still hold; it is reported so a person can add the forge's own guard as well.

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

Verification must also cover GitHub's own merge queue and the switch off:

- switch on and the target requires GitHub's merge queue: nothing is admitted, each waiting change is held with the setting named, exactly one report is filed, no change is replayed, and no repository setting is changed;
- switch on and the protection cannot be read: the same, with the failed read named;
- the requirement removed: the next read lifts the hold and the waiting changes are admitted in approval order;
- an admitted change whose candidate waits while the condition stands: it is not landed, and its gate is earned again if the target moved;
- switch off: a change lands only as the replayed, checked and reviewed revision; a moved target sends it back to replay; a merge naming a reviewed commit is refused when the pull request's head has moved;
- yoyo doctor reports a target branch that does not require the configured checks or an up to date branch, and changes nothing.

The development manager owns decomposition; the Lead Product Manager owns implementation admission and order. This document creates no work items.

# Evidence and publication limits

Inspected queuedchecks.go, integrationresume.go and internal/publish/checks.go in full at 1c880b64631b. The existing forge check reader queries pull-request-head checks and is not a native queue candidate gate. Existing withdrawal disables auto-merge and dequeues. This document was read whole at bdc6ca7adbe6 before this revision. The code that chooses queue mode (yoyodyne-ifd.429.59.3) and the doctor and status checks were not read for this revision, so whether they already refuse a required GitHub merge queue and report missing protection is not established. V1's relevant integration sections were truncated in supplied reads, so their companion revisions remain to reconcile and publish. This design does not claim implementation, successful checks or completion of the parent work item.
