---
id: unpublished-designs-and-admitted-work
kind: design
title: A design not yet published is checked against the admitted work it touches
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-20T19:30:37.522534Z
      reason: 'yoyodyne-ifd.78.8: the ruling on how a design not yet published is checked against admitted work. Overlap is found by the harness, listed in the architect''s and the Lead Product Manager''s passes until answered, and judged by the architect, who revises, records agreement, or reports the conflict to the Lead Product Manager. Names four build pieces.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-20T19:30:37.522534Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3211, for document-3211.1 under the automatic approval policy
---
# A design not yet published is checked against the admitted work it touches

Serves the goal that configurable roles collaborate without a downstream role silently redefining upstream intent, and the goal of a traceable chain from goals through designs to work. It answers the item asking how a design or ruling waiting to be copied into a companion intent repository is checked against admitted work, and who is told when they disagree (yoyodyne-ifd.78.8).

On November 9, 2026, on another project the harness builds, an architect's correction to a design sat in her conversation waiting for a person to copy it into the project's intent repository. It contradicted a decision the Lead Product Manager had already recorded on an admitted item, and nothing noticed. A document can wait in several places before it reaches the repository, and the wait for a person to copy it is only the longest of them. So this design covers every document that is written and not yet published, wherever it waits.

The standing goals apply throughout. Everything below is said in plain words where a role or the operator reads it, and nothing in it is put to the operator: deciding about a design is the architect's, and deciding about an admitted item is the Lead Product Manager's.

## 1. Which documents are covered

A document is unpublished when a role has written it and the branch it is filed on does not yet hold that revision. That is a document:

- waiting for confirmation under its kind's approval setting;
- waiting for its publishing run, or in that run;
- held until a named work item closes;
- handed back by its publishing run and not yet revised or withdrawn;
- finished and waiting for a person to copy it into the project's companion intent repository.

A document leaves this list when its revision is on the branch it is filed on, or when it is withdrawn.

## 2. When a document overlaps an item

An unpublished document overlaps an open work item when either is true:

- the document names the item: in the items it answers, or by the item's identifier anywhere in its body or reason;
- the document revises a document the item cites: by path or by document id, in the item's description or in any of its notes.

Overlap is found by the harness from these records alone. No role is asked to search for it.

## 3. When the harness lists an overlap

The harness lists an overlap:

- once, when the document's revision is written, for every open item it overlaps, so the writer checks each item's recorded decisions against what was just written;
- again, whenever an overlapping item gains an entry newer than the document's revision: a note, a change to its description or done condition, or a change to its status or priority.

The list goes into both the architect's pass and the Lead Product Manager's pass, and it is listed on every pass until it is answered. Each entry names the document and its revision, the item by what it is and its identifier, why they overlap, and the newest item entry since the revision, with its time in the operator's local zone.

## 4. What counts as disagreeing

The test is judgment, and it is the architect's, because the document is hers. A document disagrees with an item when landing it would:

- make the item's done condition false, or impossible to meet as written;
- require work that a decision recorded on the item refuses;
- refuse work the item needs to meet its done condition.

Wording that differs while both say the same thing is not a disagreement.

## 5. How an overlap is answered

The architect answers each listed overlap in one of three ways:

- **Revise the document** so it agrees. The new revision is checked again under section 3.
- **Record that it agrees.** The harness writes a note on the item saying that the architect checked this revision of this document against the item as it stood at a named time, and found that they agree.
- **Report the conflict** to the Lead Product Manager, naming the document, the item, and which part of section 4 applies. Deciding the item's side, such as revising the item, recording a new decision, or asking the architect to change the design, is hers. Where she decides the design changes, the architect revises it.

An overlap is answered when the item carries an agreement note naming the document's current revision and dated after the item's newest entry, or when a report of the conflict naming that revision has been filed. A newer item entry or a newer revision lists it again.

An overlap does not hold the document back from being sent. The architect may withdraw or revise a document she finds in conflict, as she may any of her waiting documents.

## 6. Restarts and records already in place

The list is worked out afresh from the document records and the tracker on every pass, and it is not stored, so a restart loses nothing and nothing is left half done. The agreement notes are ordinary item notes, only ever added to. Documents already waiting when this is built are covered from the first pass after it lands: each is listed once against every open item it overlaps.

## Alternatives rejected

- **Holding every overlapping document until it is answered.** A cited design is often cited by many items, so any note on any of them would stop publishing, and work that waits on that design would wait with it.
- **Listing only item entries newer than the document.** The November 9 conflict was with a decision recorded before the correction was written, so it would not have been listed.
- **The harness deciding disagreement on its own.** Whether a document contradicts a recorded decision is a reading of both, not a text match, so the harness finds the overlap and a role judges it.
- **Telling the operator.** Both sides of a disagreement already have an owner among the roles, so putting it to him would route a role's decision to a person.
- **Covering only documents waiting for a person to copy them.** A document waiting for its publishing run, or held behind an item, can contradict a decision in the same way, only for less time.

## What is enforced in code

- Every written document's record keeps the time it was written, the items it names, and the documents it revises.
- The overlap list is worked out from records on every architect and Lead Product Manager pass, and an overlap leaves the list only as section 5 says.
- An agreement note is written only by the harness, from the architect's action, and names the document's revision and the time of the item entry it was checked against.

## What a reviewer can check

- A document written while an open item it names carries a decision is listed against that item in both passes at once.
- A note added to that item afterwards lists the overlap again.
- After the architect records agreement, the overlap leaves the list, and returns when either side changes.
- A document waiting for a person to copy it into the companion intent repository is listed exactly as one waiting for its publishing run.
- Nothing in this route asks the operator anything.

## Build pieces, for the Lead Product Manager to admit

1. Each written document's record keeps its written time, the work items it names, and the documents it revises, including documents written before this lands.
2. The overlap list, worked out from the document records and the tracker, put in the architect's and the Lead Product Manager's pass briefings, with the answered test of section 5. It needs piece 1.
3. An action the architect can take that has the harness write the agreement note of section 5 on an item. Today the architect can read items but write nothing on them.
4. A test that replays the November 9 case: a held correction contradicting a decision already on an admitted item is listed in both passes, and leaves the list only once answered.
