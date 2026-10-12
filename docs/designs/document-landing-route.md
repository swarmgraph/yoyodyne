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
      at: 2026-10-12T11:11:16.489777Z
      reason: Records in the governed designs the October 11 ruling on the document landing route (yoyodyne-eek), which was copied only into that item's notes because the architect could not publish while her two waiting places were held; the item closed with the design file still unwritten
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-12T11:11:16.489777Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 744, for document-744.1 under the automatic approval policy
---
# A route of its own for landing confirmed documents

## What this is for

Mason directed on October 11, as a P0 defect, that the architect's output stop being stuck behind code work. Until this design, a confirmed document landed through a run reserved from the developer pool and ran every project check, including the full test suite and the race check, about 25 to 30 minutes under load for a one-file Markdown change, and only two documents could wait at once. That stalled the architect through full developer capacity, check timeouts, and stale publishing pull requests. This design gives document landings a route of their own. It serves the autonomy goal, and it keeps the goals that every change is reviewed by a role that did not write it before it is integrated, and that the chain from brief to verification stays traceable.

The ruling was made on October 11 and recorded on yoyodyne-eek; this document is its home in the governed designs.

## The rules

1. **Its own count.** A document landing reserves against `execution.document_landings`, default 2, and never against `execution.max_concurrent_developers`. A landing never waits on, takes, or delays a developer slot, and no developer run waits on a landing. A heavy check inside a landing takes a machine-wide permit under the heavy-check capacity design (yoyodyne-ifd.429.47), like any run.
2. **Which changes qualify.** Only a candidate whose every changed path is inside `docs/product`, `docs/designs`, or `docs/decisions` (including its invariants), or one of their directory indexes. Any other candidate is refused from this route before any check runs, and goes the ordinary way.
3. **Checks.** The project declares `document_checks`. In this repository the development manager sets them under section 6 of portable agent configuration: `internal/terms`, `scripts/vocabulary`, `internal/doclink`, `internal/artifact`, `internal/conformance`, and any other test that reads a governed document. The race check is never one of them, carrying forward yoyodyne-au1. A project that declares no `document_checks` runs its full checks. Evidence is bound to the exact candidate and to the digest of the document check set, as the invariant on evidence bound to the reviewed revision requires. A test that reads documents but is missing from the list is caught by the full checks on the target branch: that is a failure on the target branch, the development manager adds the test to the list, and the landing is not reversed.
4. **Unchanged: review and the protected merge.** An independent reviewer judges every document against the intent it claims to serve. The merge goes through the pull request, and through the merge queue when it is switched on. No document lands on the say of the role that wrote it.
5. **What replaces the two-document limit.**
   - One waiting revision per document per role: a new revision replaces an unstarted waiting one, and otherwise waits behind the one already started.
   - At most eight waiting documents per role.
   - A typed withdraw action, with which a role takes back its own unstarted waiting document.
   - Every write is answered by the end of the writing role's next pass: confirmed, refused with the reason, or waiting with the reason. A write left unanswered past that is reported as a harness defect.
6. **Its own deadline.** A landing's time limit is set by its document check set, and is never inherited from the message that confirmed the document (built under yoyodyne-trt).
7. **Unchanged: the stop after three runs.** Three runs in a row that end without a verdict still stop automatic publication and return the document to its author.

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
