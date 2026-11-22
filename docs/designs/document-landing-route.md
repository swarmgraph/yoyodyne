---
id: document-landing-route
kind: design
title: A route of its own for landing confirmed documents
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-12T18:00:26.594693Z
      reason: Records in the governed designs the October 11 ruling on the document landing route (yoyodyne-eek), which was copied only into that item's notes; resent with the two joined words the word check refuses written as ordinary words
    - action: amended
      by: architect
      at: 2026-11-22T18:16:23.513665Z
      reason: 'Point 7 said three runs without a verdict, unlike the artifact contract. It now states the stop after three returned runs and the fresh count for a newer copy on the target branch, and adds a second way to lift the stop: the development manager releases it for one document, after the writer''s next revision is held for her. Needed because a document only the stopped writer changes, such as the artifact contract stopped today, can never be sent again otherwise. The release is marked not built.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-12T18:00:26.594693Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 758, for document-758.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-11-22T18:16:23.513665Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3501, for document-3501.1 under the automatic approval policy
---
# A route of its own for landing confirmed documents

## What this is for

Mason directed on October 11, as a P0 defect, that the architect's output stop being stuck behind code work. Until this design, a confirmed document landed through a run reserved from the developer pool and ran every project check, including the full test suite and the race check, about 25 to 30 minutes under load for a change to one Markdown file, and only two documents could wait at once. That stalled the architect through full developer capacity, check timeouts, and stale publishing pull requests. This design gives document landings a route of their own. It serves the autonomy goal, and it keeps the goals that every change is reviewed by a role that did not write it before it is integrated, and that the chain from brief to verification stays traceable.

The ruling was made on October 11 and recorded on yoyodyne-eek; this document is its home in the governed designs.

## The rules

1. **Its own count.** A document landing reserves against `execution.document_landings`, default 2, and never against `execution.max_concurrent_developers`. A landing never waits on, takes, or delays a developer slot, and no developer run waits on a landing. A heavy check inside a landing takes a machine-wide permit under the design for capacity for heavy checks (yoyodyne-ifd.429.47), like any run.
2. **Which changes qualify.** Only a candidate whose every changed path is inside `docs/product`, `docs/designs`, or `docs/decisions` (including its invariants), or one of their directory indexes. Any other candidate is refused from this route before any check runs, and goes the ordinary way.
3. **Checks.** The project declares `document_checks`. In this repository the development manager sets them under section 6 of portable agent configuration: `internal/terms`, `scripts/vocabulary`, `internal/doclink`, `internal/artifact`, `internal/conformance`, and any other test that reads a governed document. The race check is never one of them, carrying forward yoyodyne-au1. A project that declares no `document_checks` runs its full checks. Evidence is bound to the exact candidate and to the digest of the document check set, as the invariant on evidence bound to the reviewed revision requires. A test that reads documents but is missing from the list is caught by the full checks on the target branch: that is a failure on the target branch, the development manager adds the test to the list, and the landing is not reversed.
4. **Unchanged: review and the protected merge.** An independent reviewer judges every document against the intent it claims to serve. The merge goes through the pull request, and through the merge queue when it is switched on. No document lands on the say of the role that wrote it.
5. **What replaces the two-document limit.**
   - One waiting revision per document per role: a new revision replaces an unstarted waiting one, and otherwise waits behind the one already started.
   - At most eight waiting documents per role.
   - A typed withdraw action, with which a role takes back its own unstarted waiting document.
   - Every write is answered by the end of the writing role's next pass: confirmed, refused with the reason, or waiting with the reason. A write left unanswered past that is reported as a harness defect.
6. **Its own deadline.** A landing's time limit is set by its document check set, and is never inherited from the message that confirmed the document (built under yoyodyne-trt).
7. **The stop after three returned runs, and how it is lifted.** A run is returned to its writer when its checks fail, when its reviewer refuses it, or when it conflicts with the target branch while it is being landed, as point 5 of the [artifact contract](artifact-contract.md) says. A run stopped by something outside the work is not a return. After three returned runs for one document in one conversation, automatic publication of that document stops and the writer revises its plan. Two things start a fresh count:
   - a revision written against a newer copy of the document on the target branch than the last returned run used, as built under yoyodyne-fp3.6;
   - the development manager's release of the stop for that one document, recorded on the document's write with her reason.

   The release exists because the first way cannot happen for a document that only the stopped writer changes: its copy on the target branch never moves, so without a release that document could never be published again. The artifact contract itself was stopped this way on November 22, 2026.

   After a stop, the writer's next revision of that document is held for the development manager, never sent and never refused outright. She either releases it, naming what in it answers the findings of the returned runs, or declines it with a reason the writer reads in its next turn. A release starts one run of that revision. If that run is returned, the stop applies again at once. The stop is never lifted on its own, never after a waiting time, and never by the operator: whether a document is ready to try again is a judgment about the work, and the development manager is the role that judges stopped work.

   Rejected: a fresh count whenever the revision's text differs from the returned runs' text, because a trivial edit would then loop without limit, which is what the stop prevents; a fresh count after a waiting time, because it retries with nobody looking at why the runs failed; handing the document to a developer, because no developer rewrites a document; asking the operator, because it is not his decision and would break the autonomy goal.

   Not built: the release, and holding the writer's next revision for it. Until they are built, a document stopped this way whose copy on the target branch has not moved stays unpublished, and its writer says so in its pass account rather than writing it again.

## Code this makes wrong

Named from the item's description; the architect has not read these files.

- `publishDocumentAttempt` reserving developer capacity, in `internal/orchestrator/documentpublication.go` and `pipeline.go`.
- Document runs executing the full check set.
- `MaxPendingWrites = 2` in `internal/runstate/conversation.go`.
- The missing withdraw action.

## How a reviewer checks an implementation

- With every developer slot taken, a confirmed document still starts landing.
- A candidate touching any path outside the document homes is refused from the route before any check runs.
- A document landing runs the declared document checks and not the race check; with none declared, it runs the full checks.
- The evidence names the candidate commit and the document check set's digest.
- Every landing has an independent review and merges through the pull request.
- A role can hold up to eight waiting documents, one per document, and can withdraw an unstarted one.
- A write left unanswered past the role's next pass produces a report.
