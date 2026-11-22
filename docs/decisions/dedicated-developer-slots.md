---
id: dedicated-developer-slots
kind: decision
title: A developer slot that prefers a label takes that label's work ahead of priority 0 work elsewhere, and priority 0 work no slot prefers waits a bounded time
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
---
# A developer slot that prefers a label takes that label's work first

This record answers two requests from the operator. On October 28, 2026 he asked for a developer slot that works on the `team-development` label (yoyodyne-jsml). On November 21, 2026 he asked that the rule letting any ready priority 0 item go ahead of a labelled slot's own work be removed outright (yoyodyne-ifd.428.189). It also records the order among work on a change a run has already written, which the Lead Product Manager decided for runs whose checks have passed (yoyodyne-ifd.428.188) and which until now existed only in code. It serves the goal that development runs nearly autonomously, and the goal that spend follows the work. It keeps whole the goal on configuration that leaves the safety rules in place, the goal on plain language, and the milestone goal that no hour passes with ready work waiting and a developer slot idle.

## The problem

Until November 21 a slot that preferred a label still took any ready priority 0 item before its own label's work of lower priority. Only a slot marked `dedicated: true` skipped that exception. The exception was the code's `outranksPreference` in `internal/orchestrator/scheduleslots.go`. It came from a condition for finishing the fix for priority 0 items that went unselected (landed October 9). It was never a decision put to the operator.

Today every developer slot in this repository carries labels. Slot 1 prefers `reliability`. Slot 2 is dedicated to `team-development`. Slot 3 prefers `usability`, `product`, `performance` and `cost`. The exception pulled slot 1 and slot 3 off their own work whenever any priority 0 item was ready. That is the opposite of what the operator asked of those slots.

Dedicating every slot does not answer it. The configuration refused a file with every slot dedicated, so that one slot was always left for urgent work.

Removing the exception alone is not safe either. Between October 6 and 9, priority 0 items that carried no label any slot preferred went unstarted. Without the exception, such an item starts only when some slot has none of its own labelled work ready. That may never happen.

A reading of the code on November 21 also found that, among work on a change a run has already written, decided repairs are taken ahead of paused runs whose checks have passed whatever their priority, and those paused runs are taken oldest pause first. No document recorded that, and it lets a priority 3 repair take a slot ahead of a priority 0 run whose checks have passed.

## The decision

**1. Every slot that prefers a label takes its own labelled work first.** The exception for priority 0 work is removed for every slot, dedicated or not. A slot that prefers labels takes ready work carrying any of its labels ahead of priority 0 work carrying none of them. `dedicated: true` is still accepted and changes nothing more; the configuration guide says so. A slot with no labels keeps the ordinary order.

**2. With no ready work on its labels, it takes work in the ordinary order.** It does not sit idle. An idle slot would break the milestone goal on idle slots. The cost is accepted: a run the slot starts outside its labels finishes before the slot returns to them. Each run is one item, so the slot is never away for longer than one run.

**3. Work on a change a run has already written still goes first, in one order.** The operating rule of October 25 says any freed developer slot takes such work ahead of fresh work of any priority. It is the operator's rule and nothing here changes it. Such work is a decided repair, a decided continuation, or a run whose checks have passed and which waits for a slot to be taken up again. All three are ordered together, in one list:

- work carrying the slot's labels before work carrying other labels;
- then by priority, highest first;
- then by the longest wait, counted from when the work became ready to be taken up: the decision recorded, or the checks passing.

A slot with no labels takes such work in the same order, without the first step. No kind of written work goes ahead of another kind for being that kind.

**4. A ready priority 0 item that no slot prefers waits a bounded time, then the next freed slot takes it.** This applies to a ready priority 0 item that carries none of the labels any developer slot prefers. Once it has waited `execution.unlabelled_priority_zero_wait`, the next developer slot to free takes it. That slot takes it whatever its labels, after any work on a change already written and before its own fresh labelled work.

- The setting defaults to one hour. A value below 15 minutes or above 24 hours is refused when the configuration is read, naming the setting and the bounds.
- Where several such items are overdue, the one that has waited longest is taken first. A freed slot takes at most one, so each overdue item costs one slot one run.
- The wait starts at the first scheduling pass that sees the item ready, not started, at priority 0 and carrying none of the slots' labels. The harness records that time in its own state for the product, keyed by the item. It clears the record when the item starts, stops being ready, leaves priority 0, or gains a label some slot prefers.
- A restart reads the record, so the wait carries on rather than starting again. If the record cannot be read, the wait counts from the item's admission time. A restart therefore never makes the item wait longer than it would have.

The bound covers only items no slot prefers. A priority 0 item that carries some slot's label waits for that slot, which takes it at the top of its own labelled work. Taking that item on another slot after the bound would break the Lead Product Manager's operating rule of October 28. That rule says a slot dedicated to one kind of work takes that work ahead of reliability work of any priority elsewhere.

The whole order for a freed slot that prefers labels is:

1. work on a change a run has already written, carrying its labels, ordered as decision 3 says;
2. work on a change a run has already written, carrying any other label, ordered the same way;
3. a ready priority 0 item that carries none of the slots' labels and has waited past the setting, longest wait first;
4. fresh ready work carrying its labels, highest priority first; within a priority, the items the Lead Product Manager placed first come first, earliest placement first, as [placing an item first](../designs/placing-an-item-first.md) describes, and then the rest, oldest admitted first;
5. the ordinary order every other slot uses.

The ordinary order in step 5 counts placements in the same way, so an item placed first keeps its place whichever step takes it.

**5. The status line says what each slot is doing, in plain words.** The cases read like these:

- "slot 3, kept for `usability`, `product`, `performance` and `cost`: working on yoyodyne-xyz, one of its labelled items";
- "slot 3, kept for `usability`, `product`, `performance` and `cost`: working outside its labels, because none of its labelled work is ready";
- "the priority 0 item yoyodyne-abc carries none of the slots' labels and has waited 25 minutes; the next free slot takes it once it has waited 1 hour";
- "the priority 0 item yoyodyne-abc waited over 1 hour, so slot 3 took it ahead of its own labelled work".

The third line appears only while such an item is ready and every slot has taken its own labelled work instead. The dashboard shows the same lines.

**6. What the configuration refuses.** A file with every developer slot dedicated is no longer refused: the bound in decision 4 now keeps priority 0 work that no slot prefers from waiting without limit, which was the refusal's only purpose. A slot marked dedicated that names no label is still refused, naming the slot and saying why.

## Who decides

Which labels a slot prefers, and the value of `execution.unlabelled_priority_zero_wait`, are choices about the order work is pulled in. That order is the Lead Product Manager's, so she decides both. The operator is not asked.

The typed configuration write that section 6 of [portable agent configuration](../designs/portable-agent-configuration.md) describes for the development manager is not built yet. Until it is, she applies the Lead Product Manager's decision by a reviewed change to `.yoyodyne/config.yaml`, made by a developer run admitted for it, whose work item grants that one file. The change goes through the configured checks and the independent reviewer like any other. Once the typed write is built, she applies these values through it instead, and nothing else about who decides changes.

## Alternatives rejected

- **Declaring every labelled slot dedicated and keeping the refusal.** In a project where every slot has a label, the file is refused, and that is this repository today.
- **Removing the exception with no bound.** Priority 0 work no slot prefers would start only when some slot ran out of its own labelled work. That is the stall of October 6 to 9.
- **Applying the bound to priority 0 items that carry a slot's label.** It would break the Lead Product Manager's operating rule of October 28 for dedicated slots.
- **Keeping one slot unlabelled for urgent work.** Which slots carry labels is the Lead Product Manager's choice, and the operator labelled all three; a design should not force a spare slot on every project.
- **Requiring every priority 0 item to carry some slot's label when admitted.** It rests on a habit at admission rather than on the scheduler, and one missed label brings the stall back.
- **Raising a label's items to priority 0.** It would pull them into every slot and wear down the meaning of priority 0.
- **Leaving a slot idle when its labels have nothing ready.** It wastes a slot against the milestone goal on idle slots.
- **Letting a slot skip work on a change a run has already written.** It would break the operator's rule of October 25.
- **Taking decided repairs ahead of runs whose checks have passed, whatever their priority.** It lets low priority work take a slot ahead of urgent work that only waits to be taken up, against the Lead Product Manager's decision for runs whose checks have passed.
- **Waiting for the typed configuration write before applying these settings.** The settings would then wait on unbuilt work; a reviewed change to the file is the path that exists today.

## Build pieces, for the development manager to carry out

**Piece 1: the scheduler's order and the bound.** It covers:

- removing the priority 0 exception in `outranksPreference`, so every slot that prefers labels uses the five step order above;
- the setting `execution.unlabelled_priority_zero_wait`, with its default of one hour and its bounds of 15 minutes and 24 hours;
- the recorded wait time for each priority 0 item no slot prefers, kept in the harness's own state for the product, read on restart, and falling back to the admission time when it cannot be read;
- the four status lines on status and the dashboard.

The bound and the removal ship together in this piece, because the removal alone brings back the stall of October 6 to 9.

**Piece 2: the configuration and the guide.** It comes after piece 1. It covers:

- removing the refusal of a configuration with every developer slot dedicated, in `internal/config/config.go`, while keeping the refusal of a dedicated slot that names no label;
- the configuration guide's section on a developer slot that prefers a label, wherever the guide now keeps it, saying that a preference now holds against priority 0 work, that `dedicated` adds nothing further, and what the new setting does;
- the comments on the developer slots in this repository's configuration, which still describe the old exception, changed by a developer run whose work item grants that file.

**The order among written work in decision 3** is built by the item already admitted for runs whose checks have passed (yoyodyne-ifd.428.188), and needs no piece of its own.

A reviewer can check them as follows:

- A slot that prefers a label, with its own labelled work ready and a priority 0 item carrying none of its labels also ready, takes its own labelled work, whether or not it is marked dedicated.
- A slot with no labels in the same state takes the priority 0 item.
- A slot with none of its labelled work ready takes the ordinary order.
- Work on a change a run has already written is taken first by every slot.
- Among such work, a priority 0 run whose checks have passed is taken ahead of a priority 3 decided repair, and two of the same priority are taken longest wait first.
- A priority 0 item carrying none of the slots' labels, once it has waited past the setting, is taken by the next freed slot ahead of that slot's own fresh labelled work, and after its work on a change already written.
- Of two overdue items, the one that has waited longer is taken first, and one freed slot takes only one.
- A priority 0 item carrying a slot's label is never taken by another slot under the bound.
- After a restart the recorded wait carries on. With the record unreadable, the wait counts from admission.
- A setting below 15 minutes or above 24 hours is refused, naming the setting and the bounds.
- A file with every slot dedicated loads. A dedicated slot naming no label is refused, naming the slot.

## Consequences

Priority 0 work that carries some slot's label waits for that slot. Priority 0 work that no slot prefers may wait up to the setting, plus the time until a slot frees. The status line says so whenever it happens, so the operator can see what the preferences cost and the Lead Product Manager can change the labels or the setting. Work of lower priority that carries no slot's label is taken only when some slot has none of its own labelled work ready, as before. Among work on a change already written, priority decides before the kind of work does.
