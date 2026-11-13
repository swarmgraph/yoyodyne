---
id: unmet-goal-review
kind: design
title: 'Reviewing unmet goals: when developers have nothing to start, and once a day'
supports:
    - v1-goals
    - milestone-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-11T06:57:42.648367Z
      reason: 'yoyodyne-mis9: records the review the operator asked for on November 10 and the Lead Product Manager widened. One goal-by-goal review is started when developers have had nothing to start for 30 minutes and once a day at 09:00 local. The architect goes first and the Lead Product Manager reorders and admits. The operator hears only steps only he can take. Work switched off, in dry run or on a staging branch does not meet a goal. Items he raised on purpose carry a label and a harness note, stay put, and are always visible. Unchecked assumptions about outside systems hold their build items. Seven build pieces. Read at main 0c4273a0.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-11T06:57:42.648367Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2638, for document-2638.1 under the automatic approval policy
---
# Reviewing unmet goals

## What this is for

On November 9 and again on November 10, 2026, another project Yoyodyne builds sat idle for hours. Every role was healthy, every recurring pass said truthfully that nothing was waiting for it, and none of that project's eight goals was met. Each pass looks at its own inbox, and nothing looked outward from the goals. The notice that developers had nothing to start went only to the operator, 105 times over that project's life. When he asked goal by goal, the Lead Product Manager admitted five items and the architect corrected three designs in about 45 minutes. Checks that only read things cleared seven of ten entries said to need a person. They also found a design that assumed an outside workflow step that does not exist, and named a developer to confirm it, which no developer run can do.

On a project with a long backlog the same fault shows as priority drift rather than an empty queue. Priority is set once for each item, mostly in reaction to an incident, and nothing checks the order of admitted work against what each unmet goal most needs.

This design answers both with one review, started two ways. It serves the autonomy goal in [the v1 goals](../product/goals/v1-goals.md): the operator's routine interface is the Lead Product Manager, and he should not be the one who notices a factory has stopped. It also serves the measure and the internal week in [the milestone goals](../product/milestone-goals.md): no hour with ready work waiting and a developer slot idle, and nothing asked of the owner that a role could have done. It must not break the plain-language goal or the traceable chain.

The operator directed on November 10, about 07:57 PST, that work priority be checked against the goals. Items he raised on purpose are reported as exceptions, and items that are simply in the wrong place are fixed. At about 07:59 PST he agreed to go ahead and asked how the list of exceptions stays visible. The Lead Product Manager's answer to that question is taken into section 6 as she decided it.

## What it rests on, and what was not read

Read: the item asking for this design (yoyodyne-mis9) with its notes, the goals documents, and the operating rules, all at main 0c4273a0. Read as delivered in this conversation's briefing: the titles of [the design for holding work that needs design](needs-design-gate.md), [placing an item first](placing-an-item-first.md), and [ownership and the operator](ownership-and-the-operator.md).

Not read: the code that sends today's notice that developers have nothing to start, the one-label design, the text of the three designs just named, and the dashboard design past its first part. Each build piece reads the code and designs it changes first. Where they say something this design does not account for, the piece stops and asks the architect.

## The decision in one paragraph

One review goes through every unmet goal in turn. For each it says how far the goal is from met and which admitted work would move it most. It then says whether that work can start now, and makes it start or is in front where it can. Where nothing serves the goal, or nothing can start, that is itself a finding, and it produces work. The review runs when developer slots have sat with nothing to start for 30 minutes and some goal is unmet, once in each such stretch, and every day at 09:00 in the operator's local time. The architect's pass goes first and the Lead Product Manager's follows. The operator hears only the steps that only he can take. Items he raised on purpose stay where he put them and are reported every time. A goal whose nothing has changed since the last review is said in one line and not gone through again.

## 1. When the review runs

**When developers have nothing to start.** The harness counts a stretch of idleness from the first moment at which at least one developer slot is free and the scheduler has nothing it may start for it. It ends when a developer run starts. When a stretch reaches 30 minutes and at least one goal is unmet, the harness starts the review once for that stretch. A stretch that goes on does not start it again. A new stretch starts it again only where something the review reads has changed since the last review, as section 5 says. The 30 minutes is one of the numeric limits the development manager decides under [portable agent configuration](portable-agent-configuration.md).

**Every day.** The review also runs once a day, starting at 09:00 in the operator's local time, whether or not anyone is idle. This is the run that catches drift in the order of admitted work on a project that is never idle.

A goal counts as unmet for starting the review unless the Lead Product Manager's last review recorded it as met and nothing it rests on has changed since.

## 2. Who does what, in what order

**The architect first.** The harness wakes the architect's recurring pass with the review's reason. Her pass does not stop at nothing waiting in her own inbox. For each unmet goal she reads the designs that serve it and says which of these holds:

- a design is wrong or missing, and she corrects or writes it in the same pass where a place to write is free, or says when she will;
- a design rests on an unchecked assumption about an outside system (section 7), and she says who can check it;
- every build item a design named has closed and the goal is still unmet, so the design missed something, and she says what;
- nothing about the designs holds the goal back.

She records this as her pass's account, one entry for each goal, and the harness hands it to the Lead Product Manager's review.

**The Lead Product Manager next.** Her review, goal by goal:

1. States how far the goal is from met, in a sentence, counting by section 3.
2. Names the admitted work that would move it most.
3. Checks that this work stands at the front of the order. If it does not, and no item the operator raised is in the way, she moves it, by priority or by [placing an item first](placing-an-item-first.md). That is her decision, reported afterwards, never put to the operator.
4. Where no admitted work serves the goal, or none can start, admits what can, from the architect's account and her own reading.
5. Where what blocks the goal is something only a person can do, names it under a reason on the operator's closed list.

She may ask the architect a question about a goal through an ask between roles. She does not wait on the architect's pass where it has not finished within an hour of the review's start. In that case she goes on, and her report says which goals she went through without it.

**The development manager** is not woken by this review. Items admitted by it reach her as any admitted item does.

## 3. When a goal counts as met

A goal is met only when the work serving it has landed on the target branch and is switched on in the product as it runs. The following count as not yet met, and the review says which applies:

- built but switched off;
- built and running in a dry run, or only reporting what it would do;
- landed only on a branch other than the target, such as a staging branch.

For work in any of these states, the next step is to make the switch decision ready. That means gathering the evidence of what the dry run or the staging branch showed, and stating what makes leaving it on safe and what turns it off again. The Lead Product Manager admits that as work where it is not done. She then names who decides the switch: the role whose decision it is, or the operator where the switch is a step only he can take, under the closed list. Waiting for the switch is never recorded as nothing to do.

## 4. What the operator is told

The notice that developers have nothing to start no longer goes to the operator first. It starts the review instead. What he is told comes after, from the Lead Product Manager's report:

- **Where the review found work and started or admitted it:** one line in the regular summaries saying developers had nothing to start from when to when, and what was started or admitted. Nothing is sent to him directly.
- **Where it found a step only he can take:** one request for each step, under its closed-list reason, as any request to him is made. Each says what the step unblocks and which goal it serves.
- **Where the review could not run,** because its pass failed or was refused: one warning report to the Lead Product Manager naming why. Where developers are still idle an hour after the stretch reached its limit and no review has finished, one warning report is shown in the regular summaries. Nothing about this is repeated while the stretch lasts.

The daily review's report reaches him in the regular summaries. It says what was reordered and why, and lists the items he raised on purpose apart from that, as section 6 says.

## 5. Not repeating itself

For each goal the review records a fingerprint of what it read: the goal's wording, the open and recently closed items serving it with their status and priority, and the architect's entry for it. On the next review a goal whose fingerprint is unchanged gets one line, 'unchanged since', with the time of the review that went through it. It is not gone through again, and no provider call is spent on it.

An idle stretch whose goals are all unchanged since the last review does not start one. The harness records that it did not start, and why. A day on which every goal is unchanged still runs the daily review, so that the exceptions in section 6 are reported, but the review is the list of unchanged goals and the exceptions, and nothing else.

## 6. Items the operator raised on purpose

**The record.** When the operator raises an item on purpose, the Lead Product Manager records it through one harness action. That action names the item and quotes his words from a turn of his own, and the harness checks that the quoted turn is his. The harness then does two things in one act:

- it puts the label `raised-by-operator` on the item;
- it writes a note on the item in a fixed form the review reads: his words, the turn they came from, when, and the items it was raised above at that moment.

The label and that note are the only record. Every view reads from them, so no second list can disagree with the tracker. Items he has already raised, such as the MCP pieces he asked to be sped up, are recorded the same way once this is built, each from his own words in the record.

**In the review.** The review leaves these items where he put them and does not move other work ahead of them. Every review lists them apart from what it reordered. Each entry gives the item by its title, the day he raised it, his words, and what it sits ahead of, so he can see its cost.

**Kept visible.** Status and the dashboard carry a standing section listing every item that carries the label, whether or not a review is running. There is one line for each, giving its title, the day he raised it, his words, what it sits ahead of, and how long since he last said anything about it or acted on it.

**Ending.** His raising ends when he says so, in ordinary words, with no special step. The Lead Product Manager records it through the same action, quoting his words, and the harness removes the label and writes a closing note in the same form. The item then returns to the ordinary check.

**When nobody has looked.** Where three weeks pass with nothing from the operator about a raised item, the Lead Product Manager puts it to him once, in plain words, asking whether it still stands. This is a hold he placed himself, so it is asked under that closed-list reason. It is not asked again until another three weeks pass after his answer, or after the question if he gave none. The three weeks is one of the numeric limits the development manager decides.

## 7. Assumptions about outside systems

A design that rests on something about a system outside the repository says so in a section of its own, headed 'Assumptions about outside systems'. Examples are a forge setting, a workflow step on another service, or what a provider's command prints. Each assumption says:

- what is assumed;
- how to check it;
- who can reach it to check it: a role through a read it holds, a check the development manager runs, or a person, under a closed-list reason;
- whether it has been checked, when, and what was found.

A build item that rests on an unchecked assumption is held, under the hold in [the design for holding work that needs design](needs-design-gate.md), until the assumption is recorded as checked. The design names those items. A design never names a developer to confirm such an assumption, because a developer run reaches nothing outside its worktree. The reviewer refuses a design that does, naming the assumption.

The architect's pass in section 2 lists, for each unmet goal, every unchecked assumption that holds work toward it, so that an unchecked assumption cannot quietly leave a goal unmet.

## What it costs

The review is two provider passes, the architect's and the Lead Product Manager's, at most once for each idle stretch and once a day. Unchanged goals cost no call. Both passes are counted to the management roles' spend, which the internal week holds to at most 40% of all known spend. Its figures say what the review cost, so the operator can see it.

## Alternatives rejected

- **Sending the idle notice to the operator, as now.** It made him the one who notices, which is what the autonomy goal rules out, and he was sent it 105 times without anything changing.
- **Each recurring pass looking beyond its inbox every time.** That would spend a full look at every goal every 45 minutes on a factory that is busy. Two triggers, and skipping goals that have not changed, spend it only when it can find something.
- **A review only when developers are idle.** On a long backlog the fault shows as drift, not idleness, so a daily run is needed too.
- **The Lead Product Manager alone.** The other project's review needed three designs corrected, which is the architect's work, and an architect pass that ran second could not correct anything before work was admitted on a wrong design.
- **A separate list of raised items kept by the harness.** Two records of one fact can disagree; the label and its note are the only record.
- **Free text in a note as the record.** The review could not read it reliably, and nothing would say when the raising ended.
- **Moving raised items like any other.** He raised them on purpose. The review's job is to show what they cost, not to undo them.
- **Asking the operator to approve the review's reordering.** Ordering is the Lead Product Manager's, and asking him would be routing her decision to him.
- **Counting built work as meeting a goal.** Four of the other project's eight goals were built and switched off. Counting them as met is how the queue looked finished when it was not.

## Build pieces, for the Lead Product Manager to admit

1. **Starting the review when developers have nothing to start.** The idle stretch as section 1 defines it, the limit as a configured number, one review for each stretch, no review where every goal is unchanged with the reason recorded, and today's notice to the operator replaced by section 4. Reads today's notice code first.
2. **The daily run and the fingerprint.** The 09:00 local run, the fingerprint of each goal, the unchanged line, and the record of each review.
3. **The two passes.** The architect's pass taking the review's reason and writing one entry for each unmet goal. The Lead Product Manager's review reading that account and writing hers. The hour she waits, the warnings in section 4, and both personas and their templates. Needs 2.
4. **Recording items the operator raised.** The harness action that checks the quoted turn is his, adds the label, and writes the note. Ending it the same way, and recording items he has already raised. Reads the one-label design first, and stops to ask the architect if that design does not allow this label beside an item's own.
5. **Showing raised items.** The standing section on status and the dashboard, read from the label and its note through the read model, and the review's report listing them apart. Needs 4.
6. **The question after three weeks.** Counting from his last word or act on the item, asked once under the closed-list reason for his own hold. Needs 4.
7. **Outside assumptions.** The section in the artifact contract's description of a design, the hold on build items that rest on an unchecked one, and the reviewer's refusal of a design that names a developer to confirm one. Reads the design for holding work that needs design first.

Pieces 1, 4 and 7 can start at once and side by side. Pieces 5 and 6 follow 4, and 3 follows 2.

## What is enforced in code

- One review for each idle stretch, none where every goal is unchanged, and one each day.
- The operator is not sent the notice that developers have nothing to start. He is sent only requests under a closed-list reason.
- A raised item's label and note are written only by the harness's action, against a turn that is the operator's.
- The review does not move work ahead of a raised item.
- A build item resting on an unchecked outside assumption is held until it is recorded as checked.

Left to role instructions: how far each goal is from met, which work moves it most, and whether a design is wrong.

## What a reviewer can check

- With developers idle for 30 minutes and a goal unmet, one review runs and the operator is not sent the old notice.
- A second idle stretch with nothing changed starts no review, and the record says why.
- A goal whose fingerprint is unchanged costs no provider call and reads 'unchanged since' with a time.
- A goal served only by work switched off, in dry run, or on a staging branch is reported as not met, and the report names the switch decision to make ready.
- A raised item is not moved, appears in every review's report apart from what was reordered, and appears on status and the dashboard with his words.
- Ending a raised item removes the label in the same act as its closing note.
- A raised item with nothing from him for three weeks produces one question, not more.
- A design naming a developer to confirm something outside the repository is refused at review.
