---
id: integration-through-a-merge-queue
kind: design
title: Integration through a merge queue
supports:
    - v1-goals
    - milestone-goals
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
    - action: amended
      by: architect
      at: 2026-11-07T20:28:01.619458Z
      reason: 'Approved amendment-59a0a032 from the developer: the queue checks a confirmed document''s candidate with the declared document checks, as the document landing route''s rule 3 says, and every other candidate with the configured checks run whole; chosen by what the candidate changes, with the check set''s digest in the evidence. Read whole at a98c5e10 before revising.'
    - action: amended
      by: architect
      at: 2026-11-12T02:57:42.623649Z
      reason: 'yoyodyne-ifd.429.154: adds the order the queue takes waiting entries in, starting from the operator''s direction that priority 0 code goes ahead of documents; and, for yoyodyne-ifd.429.160, when GitHub''s required checks on the exact candidate commit stand as the queue''s full check. Carries approved amendments 29000396 (checks and review side by side, spend charged to the entry), 57e91f68 (a check ended by its runner is run once more), 5f963776 (passed checks kept across a restart only with commit and check set recorded) and 9414b909 (checking one entry ahead), and removes the word ''gate'' throughout. Read whole at 69141ebf before revising.'
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
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-11-07T20:28:01.619458Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2450, for document-2450.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 3
      by: harness
      at: 2026-11-12T02:57:42.623649Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2689, for document-2689.1 under the automatic approval policy
---
# Purpose

This design serves the goal to isolate implementation in harness-managed worktrees and integrate successful work automatically, and the milestone goal that measures useful software per hour of the owner's attention ([milestone goals](../product/milestone-goals.md)): a queue that holds urgent code behind documents, or checks one change twice, spends developer time and his. It records integration through a merge queue (yoyodyne-ifd.429.22), the order the queue takes waiting entries in (yoyodyne-ifd.429.154), and when GitHub's required checks on a candidate stand as the queue's full check (yoyodyne-ifd.429.160). The queue prevents the target moving between a change's final checks and its landing from invalidating that work.

# Decision

Use one configuration switch, execution.merge_queue, default false. False preserves existing integration, including pre-publication replay and its checks and independent review. True admits approved changes to a durable queue and omits the harness's pre-publication replay. Existing integration and publishing approval policies still apply; enabling the queue grants no authority.

Provide a harness-run queue on every supported forge. Use a forge-native queue only when its adapter can establish and enforce the same checks and review bound to the candidate, described below. Queue availability alone is insufficient. Reject permanent dependence on GitHub's queue, checks bound only to a pull-request head, and a second switch controlling replay: each violates either portability, verification or the operator's one-switch decision.

# Durable admission and ownership

One queue exists per repository and target branch. Admission records an immutable entry identity and monotonically assigned order under a short queue-record lock. Deduplicate repeated admission of the same run and head. Record product, repository, work item title and identifier, run, publication, approved head, integration policy, chosen queue mode, admission time and predecessor order. Priority is not recorded at admission: it is read from the tracker each time the queue chooses, as the next section says.

Only one worker holds the queue-worker lease for that repository and target. This lease owns selection and candidate generation; it is separate from the existing target promotion lease. Checks and review run outside the promotion lease. The promotion lease covers only fresh target validation, the guarded mutation and its durable handoff. Never retain it through provider work or a long remote wait.

Queue waiting alone does not occupy a developer slot. Any developer or reviewer invocation uses its existing capacity and spending rules. Queue-worker exclusivity must not be implemented by permanently occupying a developer slot.

# The order the queue takes entries in

The operator directed that priority 0 code goes ahead of documents. The queue chooses its next entry each time it builds a candidate, never at admission, so a priority changed after admission counts. It chooses by these rules, the first that applies deciding:

1. An entry waiting eight hours or more since admission goes first, oldest first, whatever its priority, so no entry waits without limit.
2. Then priority 0 code entries, together with the architect's oldest waiting document entry, in admission order. A design or decision record is what the development manager's next decomposition waits on, so one of them travels with the most urgent code instead of behind all of it.
3. Then the other document entries, in admission order.
4. Then the other code entries, by priority, then admission order.
5. Last, code entries whose work item's priority cannot be read, in admission order.

An entry is a document entry when every path its candidate changes against its recorded base lies under `docs/product`, `docs/designs` or `docs/decisions` or their directory indexes: the same test that chooses its checks below. It is the architect's when those paths lie only under `docs/designs` or `docs/decisions`. Every other entry is a code entry. The recorded kind of an entry never decides which it is.

Four further rules hold whatever the order says:

- nothing already being checked or reviewed is stopped for a later entry that ranks higher; the order chooses only the next candidate;
- an entry whose work item depends on another admitted entry's work item waits until that one lands, wherever it ranks;
- checking one entry ahead, below, takes the next entry in this same order;
- when the tracker cannot be read at the moment of choosing, that choice is made in admission order, and the record says the tracker could not be read.

Each choice is recorded: the entry chosen, the rule that chose it, and the entries it passed over. yoyo status and the dashboard show the waiting entries in this order, with the rule that put the first one first.

Alternatives rejected: fixing the order at admission, because a priority raised while an entry waits would be ignored; strict priority with no age rule, because a document or low priority change could wait without limit; a separate queue for documents, because two queues landing on one target would need two promotions at once, which [one promotion per target branch](../decisions/invariants/one-promotion-per-target-branch.md) forbids.

# Harness-run queue

Process entries in the order the previous section gives. Construct a candidate in a harness-managed checkout from the current authoritative target and the admitted head. Keep the source branch and original approval evidence intact. Record a generation containing the exact target base, contributing heads in order, candidate commit, content fingerprint and check configuration.

Run deterministic checks and obtain an independent reviewer verdict on that exact candidate. Which checks run follows what the candidate changes:

- a confirmed document's entry, whose candidate changes nothing against its recorded base outside `docs/product`, `docs/designs` or `docs/decisions` and their directory indexes, runs the project's declared `document_checks`, as rule 3 of [the document landing route](document-landing-route.md) says, and so never the race check; a project that declares no `document_checks` runs its configured checks whole;
- every other entry, including a run that brings in [pinned outside files](pinned-outside-files.md), runs the configured checks whole, unless GitHub's required checks stand in for them as the section on them below says.

Where [the merge queue pace design](merge-queue-pace.md) declares which checks a candidate runs, that declared list is what the configured checks mean here. An entry recorded as a document whose candidate changes any other path is checked like every other entry. The evidence is bound to the exact candidate and to the digest of the check set that ran, so a pass of the document checks never counts for a candidate that changes code.

The checks and the review run side by side on the candidate, not one after the other. A failing check stops the review. What each costs is charged to the entry's own run and work item, never to the queue.

A check that the machine running it ended, rather than one that failed on its own, is run once more on the same candidate. If it is ended a second time, the entry is withdrawn from the queue with a report naming that check, and no repair attempt is spent.

After a restart, checks that passed on a generation are kept for that same generation only where the record holds both the candidate commit and the check set's digest. Where either is missing, every check runs again.

Original head approval authorizes admission; it does not authorize a different combined revision. Missing or incomplete checks and verdicts receive no credit.

Alternatives rejected: running the race check on every document candidate, because the landing route ruled it out and it would put document landings back behind code work; letting the entry's recorded kind choose the checks, because what a candidate changes is the fact and the kind is only a label.

Before promotion, acquire the target promotion lease and reread the target and applicable holds, directives, dependencies and approval policy. Promote only if the target still equals the recorded base and the candidate's checks and review are valid for it. Use compare-and-swap or an equivalent protected publication operation; never force the target. Target movement invalidates the generation and its checks and review. Construct and verify a new generation without charging a repair merely for drift.

For a protected target, land through its authorized pull-request path and follow the confirmed remote result locally by fast-forward. For an unprotected target, retain the existing local promotion and publication authority. Neither path may rewrite an authoritative branch or bypass repository protection.

# Checking one entry ahead

While a candidate is being checked, the queue may build the next entry's candidate on top of the current candidate and check it at the same time. That second candidate lands only if, when its turn comes, the target equals the base it was built on. Otherwise it is built again on the target as it stands and checked again, and the entry is not charged a repair for it.

No entry is checked ahead where landing goes through a pull request. GitHub lands a merge commit above the candidate, so the next candidate's base is never the target that results, and checking ahead there would spend checks that never count. Checking ahead helps only a project that lands locally.

# GitHub's required checks as the queue's full check

Where the project has switched on checks on CI, as [checks on CI](checks-on-ci.md) describes, GitHub's required checks run on every commit the harness pushes, including a queue candidate. Running the full local checks on that candidate as well would check the same commit twice. Under that switch, the required GitHub checks on the candidate commit stand as the queue's full check, but only when all of these hold:

- the harness itself read the check results for the exact candidate commit, not for the pull request's head or any other commit;
- every check the target branch requires passed; one missing, pending, failed or neutral counts as not passed;
- the digest of each workflow file the required checks ran from is recorded with the result, so a later reader can tell what ran;
- the target stayed at the candidate's recorded base from the push until the merge.

The local run then keeps only the quick checks GitHub does not run: formatting, vet, build, the tests that need macOS, and, while the target requires no race check of its own, the race check narrowed to what the candidate touches as [choosing checks by change](choosing-checks-by-change.md) describes.

The review runs beside the wait for GitHub's checks, and a failing required check stops the review. A required check that never started, was cancelled or was skipped is asked to run again once. If it still has not passed, the harness runs the full local checks on the candidate instead, records why, and spends no repair attempt.

After the merge, the harness confirms that the merge commit's first parent is the recorded base and that its files are exactly the candidate's. If either is not so, it files a critical report naming the merge, because the landed revision is then not the one that was checked.

This repository's CI jobs pass at once for a pull request that changes only documents: each job's first step checks the changed paths and ends the job as passed. A path filter on the workflow is not used, because GitHub then leaves a required check missing and the pull request can never merge.

The evidence stays bound to one exact commit, so [integration requires revision-bound evidence](../decisions/invariants/integration-requires-revision-bound-evidence.md) holds unchanged.

Alternatives rejected: accepting the checks on the pull request's head, because that head is not the candidate; keeping the full local run as well, because it checks the same commit twice and holds the queue for it; trusting a check GitHub skipped, because a skipped check proves nothing about the candidate.

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

The hold lifts by itself at the first later read that finds the protection readable and GitHub's merge queue not required; the waiting changes are then admitted in the order they were approved. Changes already admitted before the condition appeared are not landed while it stands: their candidates wait with their checks and reviews kept, and checks the target has since moved past are run again as usual.

Alternatives rejected: running the harness queue and then handing its candidate to GitHub's queue, because GitHub regroups and lands a commit no harness check or review judged; falling back to replay without saying so, because it overrides the one switch the operator set; changing branch protection, because no agent may change repository settings.

# Merging with the switch off

With execution.merge_queue false, integration is the existing path, and it still guards against the target moving between the final checks and landing:

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

Disabling the switch blocks new queue admissions. Entries already admitted drain in their recorded mode; disabling does not silently transfer or re-arm them. An explicit transfer first establishes withdrawal from the old mode, preserves ordering and history, then earns fresh checks and review on a new candidate. New integration uses the existing replay path when the switch is false.

# Existing mechanisms and acceptance

The target promotion lease and shared build cache are reused. Post-landing checks supplement verification but do not replace the checks and review on the candidate before landing. Existing publication reconciliation, withdrawal, integration continuation, ownership resolution and recovery bounds remain the mechanisms for their respective duties.

Verification must cover two concurrent admissions, competing workers, target movement during checks and review, candidate or configuration changes, restart before and after each remote mutation, uncertain saves, partial tracker/run writes, confirmed withdrawal, queue drops and timeouts, target failures, exhausted budgets, protected targets, grouped forge candidates, and disable/drain/transfer behavior. Assert that the landed revision is the checked and reviewed candidate, no stale verdict authorizes a new generation, and neither duplicate execution nor lost preserved work follows a restart.

Verification must also cover the order:

- a priority 0 code entry admitted after a waiting document entry is chosen first, and the architect's oldest waiting document is chosen with the priority 0 code, ahead of other documents;
- an entry waiting eight hours or more is chosen before any priority 0 code;
- a priority raised after admission changes the next choice;
- a candidate already being checked is not stopped when a higher entry arrives;
- an entry whose item depends on another admitted entry's item waits for it;
- an entry whose item's priority cannot be read goes last among code; a tracker that cannot be read gives admission order, recorded as such;
- every choice is recorded with its rule and the entries passed over, and status and the dashboard show the same order.

And the running of checks:

- checks and review run at once, a failing check stops the review, and both are charged to the entry's run and item;
- a check ended by the machine running it is run once more, and a second ending withdraws the entry with a report naming the check and spends no repair;
- after a restart, passed checks are kept only where commit and check set digest are both recorded;
- a candidate checked ahead lands only on the base it was built on, is rebuilt without charge otherwise, and nothing is checked ahead where landing goes through a pull request.

And GitHub's required checks as the full check:

- with checks on CI on, a candidate whose every required check passed on its exact commit, with workflow digests recorded and the target unmoved, lands with only the quick local checks run;
- a check read for the pull request's head, a missing, pending, failed or neutral required check, or a target that moved, does not count;
- a required check never started, cancelled or skipped is asked to run once more, then the full local checks run, the reason is recorded and no repair is spent;
- a merge whose first parent is not the recorded base, or whose files differ from the candidate's, files a critical report;
- a pull request changing only documents passes this repository's CI jobs at once and is never left with a required check missing.

And GitHub's own merge queue and the switch off:

- switch on and the target requires GitHub's merge queue: nothing is admitted, each waiting change is held with the setting named, exactly one report is filed, no change is replayed, and no repository setting is changed;
- switch on and the protection cannot be read: the same, with the failed read named;
- the requirement removed: the next read lifts the hold and the waiting changes are admitted in approval order;
- an admitted change whose candidate waits while the condition stands: it is not landed, and its checks run again if the target moved;
- switch off: a change lands only as the replayed, checked and reviewed revision; a moved target sends it back to replay; a merge naming a reviewed commit is refused when the pull request's head has moved;
- yoyo doctor reports a target branch that does not require the configured checks or an up to date branch, and changes nothing.

# Pieces of work this implies

The development manager owns decomposition and the Lead Product Manager owns admission and order; this document creates no work items. These are pieces each of which can be built and checked on its own:

1. The order: choosing the next entry by the five rules and four further rules, with the tracker read at choosing time.
2. Recording each choice, and showing the waiting order and the rule for the first entry on yoyo status and the dashboard.
3. Reading GitHub's required checks for the exact candidate commit, with the workflow file digests and the base recorded, and counting them as the full check only when every condition above holds.
4. Under checks on CI, the local run dropping to the quick checks, the review running beside the wait, and a failing required check stopping the review.
5. A required check never started, cancelled or skipped: one request to run it again, then the full local checks, with the reason recorded and no repair spent.
6. After a merge, confirming the first parent and the files, and the critical report when either differs.
7. This repository's CI workflow: each job's first step passing at once for a pull request that changes only documents, with no path filter.

# Evidence and publication limits

Inspected queuedchecks.go, integrationresume.go and internal/publish/checks.go in full at 1c880b64631b. The existing forge check reader queries pull-request-head checks and is not a check reader for a native queue's candidate. Existing withdrawal disables auto-merge and dequeues. This document was read whole at 69141ebffb2d before this revision. The code that chooses queue mode (yoyodyne-ifd.429.59.3), the doctor and status checks, the code that reads GitHub's checks today and this repository's workflow files under .github were not read for this revision, so how much of the reading of required checks already exists is not established. V1's relevant integration sections were cut short in supplied reads, so their companion revisions (yoyodyne-ifd.437.33) remain to reconcile and publish. This design does not claim implementation, successful checks or completion of the parent work item.