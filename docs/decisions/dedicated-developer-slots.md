---
id: dedicated-developer-slots
kind: decision
title: A developer slot that prefers a label takes that label's work first, ahead of priority 0 work elsewhere
supports:
    - operations-goals
    - milestone-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-29T02:40:20.979266Z
      reason: 'yoyodyne-jsml: the operator asked on October 28 for a developer slot that works on its label. Rules that a slot may be dedicated to its label ahead of priority 0 work elsewhere. With nothing on its label it takes the ordinary order. Work on a change already written still goes first, under the operating rule. The status line says what the slot does, and a configuration with every slot dedicated is refused. Names one build piece. Rewritten from the refused draft, with the label name marked as a name.'
    - action: amended
      by: architect
      at: 2026-11-19T05:37:16.004949Z
      reason: 'yoyodyne-ifd.437.74: step 3 of the order for a freed dedicated slot now says that, within a priority, items the Lead Product Manager placed first come first, earliest placement first, then the rest oldest admitted, pointing to the design for placing an item first (reviewer report 5dca189a). One clause and one sentence; the decision is otherwise unchanged.'
    - action: amended
      by: architect
      at: 2026-11-21T23:07:16.703833Z
      reason: 'yoyodyne-ifd.428.189: the operator asked on Nov 21 to remove the rule letting priority 0 work go ahead of a labelled slot''s own work. Every slot that prefers labels now takes its labelled work first. A ready priority 0 item no slot prefers is taken by the next freed slot once it has waited a bounded time, one hour by default, 15 minutes to 24 hours. The refusal of a file with every slot dedicated is removed. Written work still goes first. Two build pieces named.'
    - action: amended
      by: architect
      at: 2026-11-22T00:14:59.727805Z
      reason: 'Records the order among work on a change already written that the Lead Product Manager decided for runs whose checks have passed (one list by label, priority, longest wait), which existed only in code with repairs ahead of paused runs whatever their priority. Corrects how the settings are applied: the typed configuration write is not built (design review group 8, finding G8-16), so until it is they change by a reviewed change to the configuration file. Written from main fa4be0f71108.'
    - action: amended
      by: architect
      at: 2026-11-22T00:56:38.683041Z
      reason: 'The operator declined the one hour wait on Nov 21 at 16:55 PST: no bounded wait for priority 0 work no slot prefers; the Lead Product Manager tells him when such work is not moving and she thinks it should, and may label it. Removes the setting, its record, the order step and the two waiting status lines; keeps one plain status line. A file with every slot dedicated stays accepted. Written from main 87c2de1e.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-29T02:40:20.979266Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1928, for document-1928.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-11-19T05:37:16.004949Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3040, for document-3040.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-11-21T23:07:16.703833Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3350, for document-3350.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 3
      by: harness
      at: 2026-11-22T00:14:59.727805Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3359, for document-3359.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 4
      by: harness
      at: 2026-11-22T00:56:38.683041Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3364, for document-3364.1 under the automatic approval policy
---
# A developer slot that prefers a label takes that label's work first

This record answers three things the operator asked. On October 28, 2026 he asked for a developer slot that works on the `team-development` label (yoyodyne-jsml). On November 21, 2026 he asked that the rule letting any ready priority 0 item go ahead of a labelled slot's own work be removed outright (yoyodyne-ifd.428.189). Later that day he declined the bounded wait this record first added for priority 0 work no slot prefers, and asked instead that the Lead Product Manager tell him when such work is not moving and she thinks it should. It also records the order among work on a change a run has already written, which the Lead Product Manager decided for runs whose checks have passed (yoyodyne-ifd.428.188) and which until now existed only in code. It serves the goal that development runs nearly autonomously, and the goal that spend follows the work. It keeps whole the goal on configuration that leaves the safety rules in place, the goal on plain language, and the milestone goal that no hour passes with ready work waiting and a developer slot idle.

## The problem

Until November 21 a slot that preferred a label still took any ready priority 0 item before its own label's work of lower priority. Only a slot marked `dedicated: true` skipped that exception. The exception was the code's `outranksPreference` in `internal/orchestrator/scheduleslots.go`. It came from a condition for finishing the fix for priority 0 items that went unselected (landed October 9). It was never a decision put to the operator.

Today every developer slot in this repository carries labels. Slot 1 prefers `reliability`. Slot 2 is dedicated to `team-development`. Slot 3 prefers `usability`, `product`, `performance` and `cost`. The exception pulled slot 1 and slot 3 off their own work whenever any priority 0 item was ready. That is the opposite of what the operator asked of those slots.

A reading of the code on November 21 also found that, among work on a change a run has already written, decided repairs are taken ahead of paused runs whose checks have passed whatever their priority, and those paused runs are taken oldest pause first. No document recorded that, and it lets a priority 3 repair take a slot ahead of a priority 0 run whose checks have passed.

## The decision

**1. Every slot that prefers a label takes its own labelled work first.** The exception for priority 0 work is removed for every slot, dedicated or not. A slot that prefers labels takes ready work carrying any of its labels ahead of priority 0 work carrying none of them. `dedicated: true` is still accepted and changes nothing more; the configuration guide says so. A slot with no labels keeps the ordinary order.

**2. With no ready work on its labels, it takes work in the ordinary order.** It does not sit idle. An idle slot would break the milestone goal on idle slots. The cost is accepted: a run the slot starts outside its labels finishes before the slot returns to them. Each run is one item, so the slot is never away for longer than one run.

**3. Work on a change a run has already written still goes first, in one order.** The operating rule of October 25 says any freed developer slot takes such work ahead of fresh work of any priority. It is the operator's rule and nothing here changes it. Such work is a decided repair, a decided continuation, or a run whose checks have passed and which waits for a slot to be taken up again. All three are ordered together, in one list:

- work carrying the slot's labels before work carrying other labels;
- then by priority, highest first;
- then by the longest wait, counted from when the work became ready to be taken up: the decision recorded, or the checks passing.

A slot with no labels takes such work in the same order, without the first step. No kind of written work goes ahead of another kind for being that kind.

**4. Priority 0 work no slot prefers has no special rule in the scheduler.** A ready priority 0 item that carries none of the labels any developer slot prefers is taken by a slot only in the ordinary order: by a slot with no labels, or by a slot whose labelled work has run out. Nothing in the scheduler bounds how long it waits. The operator decided this on November 21, 2026, for consistency: labels and priorities are the rules for order, and a time window beside them is one more exception to keep in mind.

The safeguard is the Lead Product Manager. Where such an item is not moving and she thinks it should, she tells the operator. She may also give it a label some slot prefers, which moves it under the ordinary label rules. Recording that duty is hers, in her own documents.

The whole order for a freed slot that prefers labels is:

1. work on a change a run has already written, carrying its labels, ordered as decision 3 says;
2. work on a change a run has already written, carrying any other label, ordered the same way;
3. fresh ready work carrying its labels, highest priority first; within a priority, the items the Lead Product Manager placed first come first, earliest placement first, as [placing an item first](../designs/placing-an-item-first.md) describes, and then the rest, oldest admitted first;
4. the ordinary order every other slot uses.

The ordinary order in step 4 counts placements in the same way, so an item placed first keeps its place whichever step takes it.

**5. The status line says what each slot is doing, in plain words.** The cases read like these:

- "slot 3, kept for `usability`, `product`, `performance` and `cost`: working on yoyodyne-xyz, one of its labelled items";
- "slot 3, kept for `usability`, `product`, `performance` and `cost`: working outside its labels, because none of its labelled work is ready";
- "the priority 0 item yoyodyne-abc carries none of the slots' labels, and every slot has its own labelled work ready, so no slot is taking it".

The third line appears only while such an item is ready and every slot has taken its own labelled work instead. It names no time limit, because there is none; it is there so the Lead Product Manager can see the item. The dashboard shows the same lines.

**6. What the configuration refuses.** A file with every developer slot dedicated is not refused. Refusing it would be one more special case of the kind the operator asked to remove, and the Lead Product Manager has the remedies in decision 4. A slot marked dedicated that names no label is still refused, naming the slot and saying why.

## Who decides

Which labels a slot prefers is a choice about the order work is pulled in. That order is the Lead Product Manager's, so she decides it. The operator is not asked.

The typed configuration write that section 6 of [portable agent configuration](../designs/portable-agent-configuration.md) describes for the development manager is not built yet. Until it is, she applies the Lead Product Manager's decision by a reviewed change to `.yoyodyne/config.yaml`, made by a developer run admitted for it, whose work item grants that one file. The change goes through the configured checks and the independent reviewer like any other. Once the typed write is built, she applies these values through it instead, and nothing else about who decides changes.

## Alternatives rejected

- **A bounded wait, after which the next freed slot takes a priority 0 item no slot prefers.** This record held it from 15:07 to 16:55 PST on November 21, 2026. The operator declined it as a special case with an arbitrary time window beside the rules for labels and priorities, and chose the Lead Product Manager's notice to him instead.
- **Refusing a configuration with every slot dedicated.** It keeps a slot free for urgent work by a special rule; the Lead Product Manager's notice and labels do the same with the rules that exist.
- **Keeping one slot unlabelled for urgent work.** Which slots carry labels is the Lead Product Manager's choice, and the operator labelled all three; a design should not force a spare slot on every project.
- **Requiring every priority 0 item to carry some slot's label when admitted.** It is one more rule at admission; the Lead Product Manager may label an item when she sees it is not moving.
- **Raising a label's items to priority 0.** It would pull them into every slot and wear down the meaning of priority 0.
- **Leaving a slot idle when its labels have nothing ready.** It wastes a slot against the milestone goal on idle slots.
- **Letting a slot skip work on a change a run has already written.** It would break the operator's rule of October 25.
- **Taking decided repairs ahead of runs whose checks have passed, whatever their priority.** It lets low priority work take a slot ahead of urgent work that only waits to be taken up, against the Lead Product Manager's decision for runs whose checks have passed.
- **Waiting for the typed configuration write before applying these settings.** The settings would then wait on unbuilt work; a reviewed change to the file is the path that exists today.

## Build pieces, for the development manager to carry out

**Piece 1: the scheduler's order.** It covers:

- removing the priority 0 exception in `outranksPreference`, so every slot that prefers labels uses the four step order above;
- the three status lines on status and the dashboard.

**Piece 2: the configuration and the guide.** It comes after piece 1. It covers:

- removing the refusal of a configuration with every developer slot dedicated, in `internal/config/config.go`, while keeping the refusal of a dedicated slot that names no label;
- the configuration guide's section on a developer slot that prefers a label, wherever the guide now keeps it, saying that a preference now holds against priority 0 work and that `dedicated` adds nothing further;
- the comments on the developer slots in this repository's configuration, which still describe the old exception, changed by a developer run whose work item grants that file.

**The order among written work in decision 3** is built by the item already admitted for runs whose checks have passed (yoyodyne-ifd.428.188), and needs no piece of its own.

A reviewer can check them as follows:

- A slot that prefers a label, with its own labelled work ready and a priority 0 item carrying none of its labels also ready, takes its own labelled work, whether or not it is marked dedicated, however long the priority 0 item has waited.
- A slot with no labels in the same state takes the priority 0 item.
- A slot with none of its labelled work ready takes the ordinary order.
- Work on a change a run has already written is taken first by every slot.
- Among such work, a priority 0 run whose checks have passed is taken ahead of a priority 3 decided repair, and two of the same priority are taken longest wait first.
- While a priority 0 item no slot prefers is ready and every slot has its own labelled work, the status line and the dashboard name it, with no time limit.
- A file with every slot dedicated loads. A dedicated slot naming no label is refused, naming the slot.

## Consequences

Priority 0 work that carries some slot's label waits for that slot. Priority 0 work that no slot prefers waits until some slot has none of its own labelled work ready, or until the Lead Product Manager labels it, and may wait without limit in between. That is the stall of October 6 to 9, accepted by the operator with the Lead Product Manager watching for it. The status line names such an item while it waits, so she can see it and tell him. Among work on a change already written, priority decides before the kind of work does.
