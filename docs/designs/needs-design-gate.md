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
      at: 2026-10-12T11:58:03.096248Z
      reason: 'yoyodyne-pdu - records the October 11 ruling on holding an item until its design is readable: Mason''s eight points of September 30, the three practices from the listing-timeout case, the wake while the architect''s conversation is busy, the age report for P0 items, and how a known prerequisite is held before developer selection'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-12T11:58:03.096248Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 746, for document-746.1 under the automatic approval policy
---
# Holding an item until its design is readable: the needs-design label

## What this is for

Some admitted work cannot be built well until a design question is answered, and a developer who starts it anyway either guesses the answer or spends a run discovering the question. The listing-timeout case spent two hours on a wrong premise that way, and Atomic JSON replacement (yoyodyne-ifd.431.5) stayed selectable while it waited on a ruling described only in prose. This design gives such work an enforced hold, says who may place it and who lifts it, and says when to use it rather than a separate design item. It serves the goal that roles collaborate without a downstream role silently redefining upstream intent: a developer never decides a design question by building around it. It follows Mason's direction of September 30 and the ruling recorded on the item on October 11.

## The hold

An item carrying the `needs-design` label is held from developer selection exactly as a parked item is. The scheduler refuses to start it and records why: waiting on the architect's design since a named time. This is a selection rule enforced in code, not a convention.

Labelling an item never touches a run already going on it: the label blocks new starts only, and the running run finishes, stops or is repaired as it would have. A run the operator names explicitly is also not held, because naming one is an override and is recorded as one.

## Who sets the label

- The Lead Product Manager, when admitting or reordering work.
- The development manager, when breaking a design into work items.
- A program manager, on items in its own lane.
- A developer, during a run, through the outcome the run reports: the developer raises that the item needs a design decision, the run ends with the change kept, and the item comes back labelled with the developer's question attached. No repair attempt is spent.

Each setting records who set it, when, and the question the design must answer.

## Who removes it, and when

Only the architect removes the label, and only when the design is readable by the developer who will pull the item:

- a small design, up to 8 KiB, written into the item's own design field, which every run of the item already receives; or
- a larger one landed in `docs/designs` (or as a revision of an existing design) whose revision reason names the item; the label comes off when that revision lands, not when it is written.

Removal records which of the two it was. A ruling that exists only in an item's notes is not enough to lift the label: the architect copies it into the design field or lands it first.

The architect gains exactly two actions on items she does not own, through the tool interface and the item ownership design: writing an item's design field, and removing this one label. Nothing else about an item another role owns becomes hers.

## Waking the architect

Setting the label wakes the architect's conversation, as admissions and stoppages wake a program manager. The wake travels the background path: when her conversation is busy with another turn, the wake is queued and delivered at the next turn, never refused. Her scheduled pass is the backstop for a wake that was lost, not the main route.

## Showing the wait

An item waiting on design appears in the factory problems section as a problem the architect owns: "waiting on the architect's design since" a time in local time with the zone, and its age. A P0 item still waiting more than a day is reported to the Lead Product Manager once, and the daily digest carries it after that until the label comes off.

## The label, or a separate design item

There are not two competing ways to wait on a design. The rule is:

- **Use the label** when the question belongs to one item and its answer will fit in that item's design field: how this one change should behave.
- **Use a separate architect item, with the builds depending on it,** when the answer spans several items, changes a governed document, or several builds wait on the same answer.

Never both for the same question. When a separate design item closes because its design landed, the tracker releases the builds that depend on it. If the ruling was recorded on the design item but has not landed, the architect writes it into the design field of each waiting build before the dependency is released.

## A prerequisite must be held by the tracker, not only described

A known unresolved design prerequisite must be held by the tracker before developer selection can reach the item: either this label or a dependency on the design item. A sentence in the description saying the work waits is not a hold. Whoever admits the work (the Lead Product Manager) or breaks it down (the development manager) checks this when they do it.

The gate protects only work that is marked. It does not find a prerequisite nobody marked, and this design does not claim it does. A prerequisite discovered during a run is the developer's raise above, which marks it.

## The three practices from the listing-timeout case

- **An item whose conditions for done are choices about behaviour goes to the architect first.** The label carries this one: the admitting or decomposing role sets `needs-design` on it.
- **Diagnose before admitting a fix whose cause is unknown.** This stays practice, in the program manager's lane for reviewing what went wrong; the label does not carry it, because a diagnosis is not a design.
- **Split diagnosis from fix.** This stays practice, the admitting role's, for the same reason.

## Builds

For the development manager to break into bounded work items:

1. The selection rule: an item carrying the label is not started, with the recorded reason; a running run and an operator-named run are unaffected.
2. Setting the label from the Lead Product Manager, the development manager and a program manager, and from a developer's run outcome that keeps the change and returns the item labelled with the question.
3. The architect's two actions on items she does not own: writing the design field and removing the label, with the removal recording which form the design took.
4. The wake on the background path, queued while the conversation is busy.
5. The wait shown in the factory problems section with its age, and the P0 report after a day.
6. The admission and decomposition check, as a clause in the Lead Product Manager's and development manager's prompt rules: a known prerequisite is a label or a dependency before the item is selectable.

## How a reviewer checks an implementation

- An item carrying the label is never started by selection, and the record says why.
- Labelling an item with a run in flight leaves that run alone.
- Only the architect's action removes the label, and only with a design field or a landed revision naming the item.
- Setting the label while the architect's conversation is busy still reaches her at its next turn.
- A P0 item waiting more than a day produces one report to the Lead Product Manager.
