---
id: human-merge-keeps-review
kind: decision
title: Human merge approval keeps checks, independent review and repair rounds
supports:
    - v1-goals
    - v1-harness-design
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-30T20:22:02.114676Z
      reason: 'yoyodyne-xps.1: records, word for word, the decision that human merge approval keeps checks, independent review and repair rounds, and the exact correction to the table in ''How the two approvals compose'', because the amendment the item cited is not in the amendment log and the table lies past what the architect can read.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-30T20:22:02.114676Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2008, for document-2008.1 under the automatic approval policy
---
# Human merge approval keeps checks, independent review and repair rounds

## Decision

The setting `approvals.integration` decides who merges a change, and nothing else. Under `human` and under `automatic` alike, every change goes through the same configured checks, the same independent reviewer, and the same repair rounds with the same budgets. Failed checks and review findings go back to the developer in both cases.

Under `human`, the harness pushes the branch and opens the pull request only after the reviewer approves. It leaves the pull request open for a person to merge and never merges it itself. An open pull request therefore always carries a change the reviewer has approved.

## Why

The review goal says every change is reviewed against the intent it claims to serve, by a role that did not write it, before it is integrated. It does not depend on who presses the merge button. Before this decision, choosing human merging also skipped the reviewer and every repair round, so a change could reach a person's merge button unreviewed. The operator directed on October 9, 2026 that human merge approval should stop only the merge.

## Alternatives rejected

- **Leave review to the person who merges.** Rejected because it makes the review goal depend on a setting. A project that wants a person to merge has not asked to lose the independent reviewer.
- **Open the pull request early and mark it not ready until the reviewer approves.** The work item allowed this, but it was not chosen. An early pull request is visible outside the machine before anyone has judged the change, and a reader would have to check its ready state to know whether it was approved. Opening it only after approval means an open pull request always means an approved change.

## What the v1 harness design says

The passage on approvals in the section "User Interaction, Directives, and Approval" of [the v1 harness design](../designs/v1-harness-design.md) was revised to this decision and landed on October 30, 2026, as pull request #1548.

The table in its section "How the two approvals compose" is corrected as follows. The two rows where publishing and integration are both automatic, or where integration is automatic, are unchanged.

- **Publishing automatic, integration human.** What happens: "The change is checked and independently reviewed, and failed checks and review findings go back to the developer within the same repair budgets as automatic integration. Once the reviewer approves, the harness pushes the branch and opens the pull request, and leaves it open for a person to merge. The harness never merges it."
- **Publishing human, integration human.** What happens: "The change is checked and independently reviewed, with the same repair rounds. Once the reviewer approves, the run stops. Nothing is pushed, and the reviewed change's branch and worktree are kept for the operator."
- **The paragraph after the table.** Any sentence saying that the developer phase pushes and opens the pull request and the run stops there is replaced by: "Under either integration setting, every change passes the same checks, the same independent reviewer and the same repair rounds before anything is pushed. An open pull request carries only an approved change, so a person who merges it is merging a change the reviewer has already approved." The rest of that paragraph stays as it is unless it says otherwise than this decision.

That part of the design lies past what the architect can read from her conversation today. The edit is applied either by the re-run of the fix that keeps review for changes a person merges (yoyodyne-xps), if the development manager allows that run to write that one section, or by the architect once roles can read long files in parts (yoyodyne-t2j). Whoever applies it copies the wording above; the reviewer checks the section against this record.
