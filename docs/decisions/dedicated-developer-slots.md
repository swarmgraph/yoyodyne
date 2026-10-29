---
id: dedicated-developer-slots
kind: decision
title: A developer slot may be dedicated to its label, taking that label's work ahead of priority 0 work elsewhere
supports:
    - operations-goals
    - milestone-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-29T02:40:20.979266Z
      reason: 'yoyodyne-jsml: the operator asked on October 28 for a developer slot that works on its label. Rules that a slot may be dedicated to its label ahead of priority 0 work elsewhere. With nothing on its label it takes the ordinary order. Work on a change already written still goes first, under the operating rule. The status line says what the slot does, and a configuration with every slot dedicated is refused. Names one build piece. Rewritten from the refused draft, with the label name marked as a name.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-29T02:40:20.979266Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1928, for document-1928.1 under the automatic approval policy
---
# A developer slot may be dedicated to its label

This answers the operator's request of October 28, 2026, for a developer slot that works on the `team-development` label (yoyodyne-jsml). It serves the goal that spend follows the work. It keeps whole the goals on running nearly autonomously, on configuration that leaves the safety rules in place, and on plain language.

## The problem

The configuration guide for checks and scheduling (`docs/configuration/runs.md`) describes a developer slot that prefers a label. That slot still takes any ready priority 0 item before its own label's work of lower priority. The exception was deliberate: urgent work is never walked past for a label.

About eight priority 0 developer items are ready today. The `team-development` items are priority 1 to 3. A slot that only prefers `team-development` would spend a day or more on other work before reaching its label.

Raising every `team-development` item to priority 0 would pull those items into every slot. It would also break the operator's rule that priority 0 means the factory is stopped or at risk. So the answer belongs in how a slot chooses work, not in the priorities.

## The decision

**1. A slot may be dedicated to its label.** A developer slot that prefers a label may also be marked `dedicated: true`. A dedicated slot takes ready work carrying its label ahead of priority 0 work that does not carry it. Every other slot keeps the guide's rule unchanged, so urgent work is still taken, by the other slots.

**2. With no ready work on its label, it takes work in the ordinary order.** It does not sit idle. The milestone goals ask for a week in which no hour passes with ready work waiting and a developer slot idle, and an idle dedicated slot would break that. The cost is accepted: a run the slot starts outside its label finishes before the slot returns to its label. Each run is one item, so the slot is never away for longer than one run.

**3. Work on a change a run has already written still goes first.** The operating rule says any freed developer slot takes such work ahead of fresh work of any priority, the reliability slot included. That rule is the operator's and is not changed here, so a dedicated slot is held to it. Within that rule, such work carrying the slot's label comes before such work carrying other labels.

The whole order for a freed dedicated slot is:

1. work on a change a run has already written, carrying its label;
2. work on a change a run has already written, carrying any other label;
3. fresh ready work carrying its label, highest priority first, then oldest admitted;
4. the ordinary order every other slot uses.

**4. The status line says what the slot is doing, in plain words.** The three cases read like these:

- "slot 3, kept for `team-development`: working on yoyodyne-xyz, a `team-development` item";
- "slot 3, kept for `team-development`: working outside its label, because no `team-development` work is ready";
- "the priority 0 item yoyodyne-abc is waiting for one of the other slots; slot 3 is kept for `team-development`".

The third line appears only while a priority 0 item is ready and the dedicated slot has just taken its own label's work instead.

**5. Two configurations are refused when the configuration is read.**

- Every developer slot dedicated. Nothing would be left to take urgent work outside the dedicated labels.
- A slot marked dedicated that names no label.

The refusal names the slot and says why, in the words above.

## Who decides

Which slot is dedicated, and to which label, is a choice about the order work is pulled in. That order is the Lead Product Manager's, so she decides it. The development manager applies it through the typed configuration write that section 6 of portable agent configuration gives her for developer slots. The operator is not asked. The reliability slot the operating rules name may use the same setting; whether it does is also the Lead Product Manager's to decide.

## Alternatives rejected

- **Raising the label's items to priority 0.** It would pull them into every slot and wear down the meaning of priority 0.
- **Dropping the priority 0 exception for every slot that prefers a label.** It would let any preference hold urgent work back, when the operator asked for one slot.
- **Leaving a dedicated slot idle when its label has nothing ready.** It wastes a slot against the milestone goal on idle slots.
- **Letting a dedicated slot skip work on a change a run has already written.** It would break an operating rule the operator gave, which no design may do.

## Build piece, for the Lead Product Manager to admit

One piece. It covers:

- the `dedicated` key on a developer slot, with its place among the slot keys stated by the piece itself, since the guide's section on slots was not read for this record;
- the four-step order above in the scheduler's choice of work for a freed slot;
- the two refusals when the configuration is read;
- the three status lines;
- the configuration guide's section on a slot that prefers a label, saying what `dedicated` changes.

A reviewer can check it as follows:

- With no slot dedicated, which work each slot takes is exactly today's.
- A dedicated slot with ready label work and ready priority 0 work elsewhere takes its label's work.
- A non-dedicated slot in the same state takes the priority 0 work.
- A dedicated slot with no ready label work takes the ordinary order.
- Work on a change a run has already written is taken first by every slot.
- Both refused configurations are refused, naming the slot.

## Consequences

Priority 0 work may wait a little longer while a slot is dedicated, because one fewer slot takes it. The status line says so whenever it happens, so the operator can see what the dedication costs and the Lead Product Manager can undo it.
