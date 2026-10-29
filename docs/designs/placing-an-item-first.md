---
id: placing-an-item-first
kind: design
title: The Lead Product Manager can place an item first among work of its own priority
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-29T13:53:35.059955Z
      reason: 'yoyodyne-364h: settles how the Lead Product Manager records that an item comes first among work of its own priority, and how the scheduler reads it. Kept on the item in the tracker, earliest placement first, at most three in one priority, ended by closing, a priority change or her removal, never ahead of higher priority or of work on a change already written, and shown in plain words in every listing. One build piece and the reviewer''s checks are named. Resent with two joined words the word check refused written as ordinary words.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-29T13:53:35.059955Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1951, for document-1951.1 under the automatic approval policy
---
# Placing an item first among work of its own priority

## What this is for

On October 28 the Lead Product Manager promised that the build letting a developer slot be dedicated to its label (yoyodyne-ifd.428.130) and the configuration change that uses it (yoyodyne-u6ei) would go first among fresh priority 0 work, on the operator's direction. At 04:21 PDT on October 29 a freed developer slot took an older priority 0 item instead. Within one priority, work is pulled oldest admitted first, and nothing a role can do changes that. The promise was made in a conversation, and the scheduler never reads one. The same gap stopped the sub-agent slot fix (yoyodyne-ifd.428.126.2.2) from being moved ahead the day before.

The order work is pulled in is the Lead Product Manager's to decide. This design gives her a way to write that order where the scheduler reads it. It serves the goal of running nearly autonomously: a promise about order is kept by the harness, not by somebody watching the queue. It keeps whole the goals on plain language, on a traceable chain, and on a team sharing one repository.

## The decision

**1. Placing an item first is a typed tracker action, and only the Lead Product Manager may take it.** She names the item and gives a reason. The action records who placed it, when, and why. Removing a placement is a second action of hers, recorded the same way. No other role may place or remove one, because the order is hers. The development manager and the program managers ask her, as they ask about any other ordering.

**2. The placement is kept on the item in the tracker.** It is not kept in one harness's own state directory. Every harness sharing the repository reads the same order, and the placement travels with the item as the tracker's other fields do. The build piece chooses the tracker field and names it.

**3. Within one priority, placed items come first, earliest placement first.** The order among fresh ready work becomes:

1. priority, highest first, exactly as today;
2. within one priority, placed items before unplaced ones;
3. among placed items, the one placed earliest first, so a later promise never overtakes an earlier one without the Lead Product Manager removing the earlier placement;
4. among unplaced items, oldest admitted first, exactly as today.

A placement never lifts an item above work of a higher priority. An item held from selection, such as a parked item or one waiting on the architect's design, stays held whatever its placement; the placement counts again once the hold comes off.

**4. Work on a change a run has already written still goes first.** The operating rule of October 25 puts such work ahead of fresh work of any priority, for every freed slot, the reliability slot and any dedicated slot included. That rule is the operator's and is not changed here. Placement orders fresh work only. Within each step of a dedicated slot's order (the decision record on dedicated developer slots), and within a slot's label preference, items of one priority follow the order in point 3.

**5. At most three items are placed in one priority at a time.** The Lead Product Manager may use placement for promises, not as a second scale of priorities. A fourth placement in the same priority is refused, and the refusal names the three already placed and when each was placed, so she can remove one.

**6. A placement ends on its own in three cases.** It ends when the item closes, when the item's priority changes, or when the Lead Product Manager removes it. A priority change ends it because the promise was about the item's place among its old priority. Each ending is recorded on the item with what ended it.

**7. Every listing of the queue says it in ordinary words.** A placed item carries one line, for example: "placed first among priority 0 by the Lead Product Manager at 06:50 PDT on October 29: promised first by the operator". The time is in the operator's local zone with the zone named. Items with no placement read exactly as before. This applies wherever the queue is listed: the briefings roles receive, the live survey a role asks for, `yoyo status`, and the dashboard.

## Alternatives rejected

- **A label.** A label says an item is placed but not in what order among several placed items, and it would mix ordering into the configured set of labels.
- **A priority above 0.** It would wear down what priority 0 means, the same objection the decision record on dedicated developer slots made to raising a label's work to priority 0.
- **Rewriting an item's admission time.** It falsifies the record of when the item was admitted, which the delivery figures read.
- **A list kept in the harness's own files.** Two collaborators' harnesses would disagree about the order, against the goal that a team shares one tracker.
- **No limit on placements.** Unlimited placement becomes a second priority scale that nobody reads as one.

## Build piece, for the development manager to break down

One piece, covering:

- the two typed tracker actions for the Lead Product Manager, placing and removing, refused for every other role, with the limit of three per priority and its refusal;
- the placement stored on the item in the tracker, and the three automatic endings recorded;
- the scheduler's order in point 3, inside every slot's choice of fresh work, with the rule for work on a change already written left above it;
- the line in every listing of the queue.

## How a reviewer checks an implementation

- With nothing placed, which work each slot takes is exactly today's.
- An item placed first among priority 0 is pulled before older unplaced priority 0 items.
- Of two placed items in one priority, the one placed earlier is pulled first.
- A placed priority 1 item is never pulled before ready priority 0 work.
- Work on a change a run has already written is still taken before any placed item.
- A fourth placement in one priority is refused, naming the three already placed.
- Closing an item or changing its priority ends its placement, and the ending is recorded.
- Only the Lead Product Manager's actions place or remove a placement.
- Every listing shows the placement line for a placed item and nothing new for an unplaced one.
