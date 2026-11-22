---
id: needs-design-gate
kind: design
title: 'Holding an item until its design is readable: the needs-design label'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-12T18:47:06.923991Z
      reason: 'yoyodyne-pdu - records the October 11 ruling on holding an item until its design is readable: Mason''s eight points of September 30, the three practices from the case of the listing that timed out, the wake while the architect''s conversation is busy, the age report for P0 items, and how a known prerequisite is held before developer selection; resent with the joined words the word check refuses written as ordinary words'
    - action: amended
      by: architect
      at: 2026-11-19T16:07:41.070431Z
      reason: Replaces the retired word 'gate' in its one place in the prose, 'The gate protects only work that is marked', with 'The hold', the design's own name for it, under the operator's November 8 rule against the word (part of the coined-vocabulary replacement, yoyodyne-ifd.437.21). The file name and id keep the word because stored names are exempt. Nothing else changes.
    - action: amended
      by: architect
      at: 2026-11-22T03:11:22.563739Z
      reason: The design said the architect removes the needs-design label and writes an item's design field; the architect's tools can change no work item, and neither action was built. The label now comes off when a document naming the item among those it answers lands, recorded by the harness, with the development manager removing it by hand until that is built. The design field route is dropped. Written whole from a read at main 2e9db6e7d; part of the design review against the code.
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-12T18:47:06.923991Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 761, for document-761.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-11-19T16:07:41.070431Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3091, for document-3091.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-11-22T03:11:22.563739Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3376, for document-3376.1 under the automatic approval policy
---
# Holding an item until its design is readable

## What this is for

Some admitted work cannot be built well until a design question is answered, and a developer who starts it anyway either guesses the answer or spends a run discovering the question. The case of the tracker listing that timed out spent two hours on a wrong premise that way, and Atomic JSON replacement (yoyodyne-ifd.431.5) stayed selectable while it waited on a ruling described only in prose. This design gives such work an enforced hold through the `needs-design` label, says who may place it and how it is lifted, and says when to use it rather than a separate design item. It serves the goal that roles collaborate without a downstream role silently redefining upstream intent: a developer never decides a design question by building around it. It follows Mason's direction of September 30 and the ruling recorded on the item on October 11.

## The hold

An item carrying the `needs-design` label is held from developer selection exactly as a parked item is. The scheduler refuses to start it and records why: waiting on the architect's design since a named time. This is a selection rule enforced in code, not a convention.

Labelling an item never touches a run already going on it: the label blocks new starts only, and the running run finishes, stops or is repaired as it would have. A run the operator names explicitly is also not held, because naming one is an override and is recorded as one.

## Who sets the label

- The Lead Product Manager, when admitting or reordering work.
- The development manager, when breaking a design into work items.
- A program manager, on items in its own lane.
- A developer, during a run, through the outcome the run reports: the developer raises that the item needs a design decision, the run ends with the change kept, and the item comes back labelled with the developer's question attached. No repair attempt is spent.

Each setting records who set it, when, and the question the design must answer.

## How the label is lifted

No role removes the label by hand. The harness removes it when a design document whose write names the item among the items it answers lands on the target branch: a new design in `docs/designs`, or a revision of an existing one. The label comes off when that document lands, not when it is written, and the harness records on the item which document and which commit lifted it.

A ruling that exists only in an item's notes does not lift the label, and neither does a document that was written but has not landed. The architect writes the design as a document naming the item and lets it land.

Until the harness removes the label on landing, the development manager removes it once the architect's document naming the item has landed, and records which document and commit it was.

The revision of October 12 gave the architect two actions on items the architect does not own: writing an item's design field, and removing this label. Neither was built, and the architect's tools cannot change a work item at all, as the second reading of the design review against the code found (yoyodyne-ifd.437.84). Lifting the label when the document lands was chosen instead for three reasons: it ties the lift to the same reviewed landing that makes the design readable, it needs no new authority for any role, and a document that never landed cannot lift it. Two alternatives were rejected:

- Giving the architect a tracker write for this one label, because it would be the only tracker write the architect holds, and a label removed by a separate write can come off before the design it waits on has landed.
- Keeping a route through the item's design field for designs up to 8 KiB, because the architect cannot write that field and a short document lands the same way a long one does.

## Waking the architect

Setting the label wakes the architect's conversation, as admissions and stoppages wake a program manager. The wake travels the background path: when the conversation is busy with another turn, the wake is queued and delivered at the next turn, never refused. The architect's scheduled pass is the backstop for a wake that was lost, not the main route.

## Showing the wait

An item waiting on design appears in the factory problems section as a problem the architect owns: "waiting on the architect's design since" a time in local time with the zone, and its age. A P0 item still waiting more than a day is reported to the Lead Product Manager once, and the daily digest carries it after that until the label comes off.

## The label, or a separate design item

There are not two competing ways to wait on a design. The rule is:

- **Use the label** when the question belongs to one item: how this one change should behave.
- **Use a separate architect item, with the builds depending on it,** when the answer spans several items, changes a governed document, or several builds wait on the same answer.

Never both for the same question. When a separate design item closes because its design landed, the tracker releases the builds that depend on it. If the ruling was recorded on the design item but has not landed, the architect lands a document naming each waiting build among the items it answers before the dependency is released.

## A prerequisite must be held by the tracker, not only described

A known unresolved design prerequisite must be held by the tracker before developer selection can reach the item: either this label or a dependency on the design item. A sentence in the description saying the work waits is not a hold. Whoever admits the work (the Lead Product Manager) or breaks it down (the development manager) checks this when they do it.

The hold protects only work that is marked. It does not find a prerequisite nobody marked, and this design does not claim it does. A prerequisite discovered during a run is the developer's raise above, which marks it.

## The three practices from the case of the listing that timed out

- **An item whose conditions for done are choices about behaviour goes to the architect first.** The label carries this one: the admitting or decomposing role sets `needs-design` on it.
- **Diagnose before admitting a fix whose cause is unknown.** This stays practice, in the program manager's lane for reviewing what went wrong; the label does not carry it, because a diagnosis is not a design.
- **Split diagnosis from fix.** This stays practice, the admitting role's, for the same reason.

## Builds

For the development manager to break into bounded work items:

1. The selection rule: an item carrying the label is not started, with the recorded reason; a running run and an operator-named run are unaffected.
2. Setting the label from the Lead Product Manager, the development manager and a program manager, and from a developer's run outcome that keeps the change and returns the item labelled with the question.
3. The harness removes the label when a document whose write names the item among the items it answers lands on the target branch, and records the document and the commit on the item. No role's tools remove it directly.
4. The wake on the background path, queued while the conversation is busy.
5. The wait shown in the factory problems section with its age, and the P0 report after a day.
6. The admission and decomposition check, as a clause in the Lead Product Manager's and development manager's prompt rules: a known prerequisite is a label or a dependency before the item is selectable.

This revision checked only how the label is lifted against what is built. Whether builds 1, 2, 4, 5 and 6 are built as written is for the design review against the code to establish.

## How a reviewer checks an implementation

- An item carrying the label is never started by selection, and the record says why.
- Labelling an item with a run in flight leaves that run alone.
- The label comes off only when a document naming the item among the items it answers lands, and the item records which document and commit lifted it.
- A document that was written but has not landed, or a ruling only in the item's notes, leaves the label on.
- Setting the label while the architect's conversation is busy still reaches the architect at its next turn.
- A P0 item waiting more than a day produces one report to the Lead Product Manager.
