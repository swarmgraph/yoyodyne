---
id: approved-changes-that-no-longer-merge
kind: design
title: Getting an approved change that no longer merges back onto main with less spending
supports:
    - v1-goals
    - operations-goals
    - milestone-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-23T20:05:43.10317Z
      reason: 'yoyodyne-ifd.429.294: resends the design for getting a conflicting approved change back onto main, answering the review of run-107db6b2. It names the three rules on main it touches, says which document decides until each is revised and which build piece waits, records the approved and resolved heads apart, keeps a run that moved to the stronger model on it, and names every item by what it is. Estimates and the five pieces are otherwise unchanged.'
    - action: amended
      by: architect
      at: 2026-11-25T21:47:08.965918Z
      reason: 'yoyodyne-32of.2: answers the review of run-2c2f03f4. One returned change at a time holds a file, in the order handed back, so two never hold each other and no attempt starts on files another returned change holds; the hold begins before the wait for a candidate being worked, so that wait is not extended by new entries; an entry the change depends on is never held; a hold also ends when the change goes to the development manager or its run ends. Decision 1, 2, 3 and the reviewer list agree.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-23T20:05:43.10317Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3733, for document-3733.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-11-25T21:47:08.965918Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 4072, for document-4072.1 under the automatic approval policy
---
# Purpose

This design answers how an approved change that no longer merges gets back onto main with less repair and review spending (yoyodyne-ifd.429.294). It serves the goal that spend follows the work, and keeps the goal that every change is reviewed, by a role that did not write it, before it is integrated. Every landed change keeps its checks and independent review on the exact revision that lands.

It was revised for the operator's direction of November 25, 2026, given about 13:15 PST through the operator's assistant: 'Yes, I think once you rebase a change, I think it should get merged as soon as possible. What I really don't want to have to do is rebase things multiple times.' Decisions 1 and 2 are new in this revision. The architect had given an earlier answer to that direction in an exchange whose text no role could supply when this was written (exchange-79ee2db99ead45b1cc24deb362f4793e), so the rules below are decided afresh from the direction.

It changes rules that three designs on main state: the order and the admission rule in [integration through a merge queue](integration-through-a-merge-queue.md), and the single attempt after a conflict in [stopped run recovery](stopped-run-recovery.md). It leaves the rule on moving to the stronger model in [reducing what the factory costs](reducing-what-the-factory-costs.md) as it is. Which document decides until each is revised, and which build piece waits, is stated in each decision and gathered under which document decides, below.

# What was found

The operator's assistant read the merge queue's records and the spend record for midnight to 10:20 PST on November 23, 2026. The queue landed 34 entries and handed back 29, of which 21 no longer merged onto main. Developer repairs cost $49.11 across 23 runs, and the reviews of those runs after their first repair $37.21: about $86 of $384.52, or 22%. The architect did not read those records.

The architect read the tracker's records of four items handed back more than once:

- listing a document approved automatically as never waiting on the operator (yoyodyne-ifd.432.52);
- listing a finished design that waits for a person to copy it as the operator's step (yoyodyne-4f8u);
- repairing a run that started on the cheaper model on the stronger one (yoyodyne-ifd.435.91);
- the merge queue asking GitHub to merge a candidate directly, after reading main (yoyodyne-ifd.429.242).

They show four causes of repeated spending:

- **The change goes back to the end of the line.** In all four, the one attempt to bring the change up to date was made between 03:39 and 04:49 PST, the change rejoined the queue, and it conflicted again about five to six hours later. The development manager then decided a new run from the kept branch, which costs a decision and a new developer session.
- **Other changes to the same files land while it is being brought up to date.** The first two of those items both conflict in `internal/readmodel/documentapprovals.go`.
- **A change that rejoins is a new entry.** It gets a new place and a new admission time, so the rule that takes an entry waiting four hours first counts from zero.
- **Two reviews follow each repair.** The run's change is reviewed before it rejoins the queue, and the queue then has the exact candidate reviewed.

## What cannot be told apart

The share of the $86 that conflicts caused is not established. The four items were handed back ten times that morning by the assistant's count, and the rest of the 23 runs were not read. If conflicts cost the same as other hand backs, 21 of 29 makes the share about $62, or 16% of the day's spending. That is an estimate and not a measurement. The tracker notes the time of each attempt to bring a change up to date, but the architect did not establish that a spend record says which attempt it paid for. Build piece 1 records it, so the share is measured from then on.

# Decision

Decisions 1 and 2 are about code entries. A document whose publication conflicts goes back to the role that wrote it, as the document landing route says, and gets neither the next turn nor the hold. Below, a returned change is a code change handed back for a conflict, and only for a conflict, that has not yet landed. It stops being one when it is put to the development manager, when its run ends, or when its candidate fails a check or its review.

## 1. A change brought up to date takes the queue's next turn

A returned change takes the queue's next turn when it rejoins. It is chosen ahead of every other waiting entry: entries waiting four hours or more, priority 0 code, documents, and everything else. Where more than one waits in the queue, the one handed back earliest goes first, by its latest hand back for a conflict.

Three rules of the queue stand whatever this says: a candidate already being checked or reviewed is never stopped for it; an entry whose work item depends on another admitted entry's item waits for that one; and the candidate's checks and its one independent review are earned on the exact revision that lands.

It goes ahead of priority 0 code because it costs that code one landing, while going behind can cost another attempt, another review and another wait. Its priority does not matter because the reason is the work already paid for and not the item's urgency.

The turn is kept through any repair made before the change rejoins, such as one after a failed check or a refused review of the resolved change. It is spent when the change's candidate is built. If that candidate conflicts, the hand back gives a new turn. If that candidate fails a check or its review, the change itself was at fault, and it rejoins under the ordinary order. A repair the development manager decides for a fourth conflict makes the change a returned change again from that decision, with the same turn and the same hold.

This changes the order in the merge queue design. Until that design's own revision lands, its order decides, and build piece 2 waits for it.

## 2. Entries that change the same files are held while it is brought up to date

**One returned change at a time holds a file.** A returned change's hold covers every file it changes. It begins when the change is handed back, except that it waits while either of these is so:

- another returned change holds any of the same files;
- another returned change that shares any of its files, and goes before it, is itself waiting for its hold.

One returned change goes before another when the other's work item depends on its item, directly or through other items in the queue, and otherwise when it was handed back earlier. Where the dependencies go round in a circle, the time of the hand back decides. Because a later change waits for an earlier one even while that one is waiting, two returned changes never wait for each other. If a change that is holding files comes to depend on a returned change that shares any of them, its hold ends and it waits behind that one.

A file that only the resolution adds is held from then on, unless another returned change holds it already.

**What the hold does.** While it lasts, the queue does not choose another waiting entry that changes any of the held files. Entries that share no file with it go on landing, because Git merges them without help. Four kinds of entry are never held:

- a returned change that has rejoined, the one the hold is for included;
- an entry the held change's item depends on, directly or through other items in the queue, because the change cannot land before it;
- priority 0 code, because the hold can last through a whole developer session and not just one landing;
- an entry whose files cannot be read, and the record says so.

An entry waiting four hours or more is held like any other, so one hold adds at most two hours to its wait.

**When the hold ends.** It ends at the first of these: the change lands; it is handed back again, for any reason; it is put to the development manager; its run ends; or two hours have passed since the hold began. A change handed back again for a conflict waits for a new hold by the rules above. When two hours end it, the change keeps its next turn, but makes nothing else wait.

**When the attempt starts.** It starts once the change's hold has begun, ahead of fresh work, as work on a kept change does. Within the hold it waits for two things only: a candidate already being built, checked or reviewed when the hold began, or an entry the hold does not stop and the queue has chosen since, that changes any of the same files; and a waiting entry its item depends on that changes any of the same files. Because the hold has already begun, no other entry on those files can be chosen meanwhile, so the wait does not grow. If the hold ends by its two hours first, the attempt starts then. A wait holds no developer slot and spends nothing. The run's record and yoyo status name what it waits for.

So a later returned change on the same files is brought up to date only after the earlier one has landed or no longer holds them, and starts from a main that includes it if it landed. Two attempts on the same files can run together only where a hold ran out of its two hours, or where a resolution came to change a file another returned change holds.

The attempt starts from main as it stands at that moment. Where the change now merges without conflict, no developer session starts: it rejoins with the same approved head and the next turn, its hold goes on, and this uses none of its attempts.

**What is shown.** The files of an entry are the paths its change modifies, adds, deletes or renames, worked out by the harness from Git. The record of each choice, yoyo status and the dashboard name each held entry, the change it is held for, the shared files, and the latest time the hold ends.

The hold is decided by the merge queue design once revised. Until then no entry is held, and the part of build piece 2 that does this waits for it. This decision replaces the earlier rule that an attempt waits up to two hours for entries ahead of it.

## 3. Up to three attempts, each in a fresh session

Step 1 of stopped run recovery says an approved change that conflicts with the target branch when it lands gets exactly one attempt to bring it level, outside the repair attempts, and that a second conflict stops the run as about the work. This decision replaces that number: a run gets up to three such attempts, one for each hand back for a conflict, and a fourth conflict stops the run as about the work. With decisions 1 and 2 a second attempt should be rare, so the second and third are a backstop and not an ordinary path. The rest of that rule stays: an attempt is not charged as a repair attempt, it is recorded as a reconciliation, and a failing check or a refused review after it is about the work and goes on as it does today, through the remaining repair attempts and then the development manager. The limit of three is held in code, and a restart or a new build does not reset the count.

Each attempt after the first records the commit on main it conflicted with, and why the hold did not prevent it: the hold had run out, the entry was priority 0, the change's item depends on the entry, the entry was another returned change, its files could not be read, it was already being worked when the hold began, the change landed outside the queue, or there was no hold. The development manager can read it.

Until stopped run recovery's own revision lands, its one attempt decides, and the part of build piece 3 that allows the second and third waits for it. The fresh session below changes nothing that design says and does not wait.

Each attempt starts a new developer session rather than resuming the long one. After hours in the queue the provider no longer keeps the old session's text, so resuming it pays to write all of it again. The new session starts from:

- the work item, and the goals and invariants a run is always given;
- the approved change, as a diff, and the reviewer's approval;
- the conflicts as Git leaves them in the checkout;
- the commits on main since the change's base that touch the conflicting files.

It is told to resolve the conflicts, keeping what main and the change each do, and to change nothing else unless the resolved code would not otherwise build or pass its tests. The session that wrote the change and each session that resolved a conflict are all recorded as authors, and no reviewer may be any of them.

## 4. A resolution confined to the conflicting files is reviewed once, as the candidate

The harness compares the resolved change with what Git produces by merging the approved head onto the same main. The resolution is confined when every file outside the conflicting ones is exactly as that merge leaves it. The comparison is made by the harness, never by asking the developer.

- **Confined.** Once the checks made before review pass, the change rejoins the queue with no review in between. The candidate the queue builds is then reviewed whole, by an independent reviewer, as every candidate is. That reviewer is also given the conflicts and how each was resolved, and is told which files had them.
- **Not confined.** It is reviewed before it rejoins, as today.
- **Anything else that approved the head by name.** A person's approval the project requires, or an acceptance of a protected path, is earned again on the new head as today; this decision removes only the review before admission.

**The rule it changes.** The merge queue design says the queue admits approved changes, that admission records the approved head, and that approval of the original head authorizes admission. Under this decision a second way in exists: the approval of the original head also authorizes admission of a confined resolution of that head, for the same run only. Such an entry records three things: as its approved head, the head the reviewer approved, with that review; as its resolved head, the commit the developer's session produced; and the comparison that found it confined. The candidate is built from the resolved head, and an entry is told apart from another by its run and its resolved head. Nothing shows the resolved head as approved by a reviewer, on any surface, until the candidate's own review approves it. Approval of the original head still never authorizes landing a different revision.

Until the merge queue design's own revision lands, its admission rule decides: every resolved change is reviewed before it rejoins, and build piece 4 waits for it whole.

Every landed change therefore keeps its checks and independent review on the exact revision that lands, so [integration requires revision bound evidence](../decisions/invariants/integration-requires-revision-bound-evidence.md) holds unchanged. If that review refuses the candidate, the change is handed back as any refused candidate is, and this decision is not used again for that run.

## 5. Small conflicts are tried on the cheaper model

A setting, `conflict_resolution_model`, names a kind of model as reducing what the factory costs describes. By default it is not set, and the run's own rules choose the model. Where it is set, it applies only to an attempt for which all of these hold:

- the conflicts lie in at most three files and 200 conflicting lines;
- the item has no high risk label and is not priority 0;
- it is the run's first attempt at a conflict; a second or third uses the stronger model;
- the run has not already moved to the stronger model.

The last is what section 4 of reducing what the factory costs requires: after its first failed review or failed checks a run moves once to the stronger model for the rest of its attempts. A run that has moved is never moved back, so that rule decides and this setting does not change it. The same rule applies after the attempt: where the resolved change fails its checks or its review, the run moves to the stronger model as section 4 says.

Repairing a run that started on the cheaper model on the stronger one when it is handed back (yoyodyne-ifd.435.91) stays the rule wherever the setting is not set, and for every hand back that is not a conflict. Where the setting is set and every point above holds, an attempt at a conflict is the one exception. A conflict is not a failure of the work, so it says nothing about the model that wrote it. Build piece 5 makes that exception on top of that item's code.

In this repository the development manager tries it on 20 eligible attempts, alternating the two models. It is kept only where cost for each landed change falls and the candidate's review refuses no more often. It stops at once on a defect after landing traced to a resolution made on the cheaper model.

## 6. Work on the same files is ordered by the existing decision

[Central code stays shared](../decisions/entanglement-and-merge-affinity.md) already has the development manager link items expected to change the same files. No new rule is added. Build piece 1 gives that practice evidence: each week, the files named in the most hand backs, with the items, are shown to the development manager, who links open items that will touch them or proposes splitting the file.

# Which document decides until each is revised

| Rule | Where it is now | Until its revision lands | What waits for it |
| --- | --- | --- | --- |
| The order the queue takes entries in, with no turn for a returned change and no entry held for one | merge queue design, the order | that design decides | build piece 2 |
| One attempt after a conflict at landing | stopped run recovery, step 1 | that design decides | the second and third attempts in build piece 3 |
| Only an approved head is admitted | merge queue design, durable admission and the harness's own queue | that design decides | build piece 4 |
| A run moves once to the stronger model for the rest of its attempts | reducing what the factory costs, section 4 | that design decides, then and afterwards | nothing; decision 5 keeps to it |

The architect writes the merge queue and stopped run recovery revisions next, in that order; the second does not wait for the first.

# Expected saving

These are estimates from the figures above, they overlap, and none is a measurement. They take the conflict share to be about $62 for the period.

| Change | What it should save | How it is measured |
| --- | --- | --- |
| The next turn, and the hold | most of the six repeat hand backs among the ten; up to about a quarter of the conflict share, $18 | conflict hand backs for each landed code change, how many runs have more than one, and the time from rejoining to landing |
| Three attempts, fresh sessions | the development manager's decisions, the new runs, and part of the cost of writing the old session again | runs started by a decision after a conflict; median cost of one attempt |
| One review in place of two | about half of the review cost after a confined resolution, perhaps $13 | reviews for each resolved conflict; how often the candidate's review refuses a confined resolution |
| The cheaper model | not estimated; the trial says | cost and refusals for each side of the trial |

The measure for the whole is the cost of conflicts for each landed code change, over one week before and one week after pieces 1 to 4 are on main. If it has not fallen by a third, or if more than one returned change in ten needs a second attempt, the architect revises this design from what the records show.

# Alternatives rejected

- **Keeping the change's age instead of giving it the next turn.** The earlier form of this design did. A change handed back within four hours of its admission would still go behind others, which is the repeated work the operator directed against.
- **Placing it behind priority 0 code.** It saves that code one landing and risks another attempt and another review.
- **Stopping all landings while a change is brought up to date.** A developer session and its checks can take an hour or more, and only entries that change the same files can undo the work.
- **Holding only entries that touch the conflicting files.** The next conflict can be in any file the change modifies.
- **Holding priority 0 code as well.** A hold can last two hours, and urgent code is not kept waiting through a developer session.
- **A hold with no limit.** One stuck session would keep other work on a shared file from landing at all.
- **Starting the hold only when the attempt starts.** While the attempt waits for a candidate on the same files, the queue could choose another entry on them, and the wait could go on.
- **Letting a later returned change start as soon as the earlier one rejoins.** It would be brought up to date without the earlier change, and each change's hold could stop the other.
- **Waiting for every entry ahead before the attempt,** as the earlier form did. With the next turn nothing waiting stands ahead.
- **Giving a change that fails its candidate's checks or review another next turn.** A faulty change could then stay at the head of the queue.
- **Resolving conflicts by a tool.** Keeping both sides of a conflict by rule can build and still be wrong, in code and in a guide, and the queue already lets Git merge everything it can. No record read says how many of the 21 such a rule would have settled. Build piece 1 records the files and size of each conflict, so this can be weighed on evidence.
- **Reviewing only the resolution and not the whole candidate.** What lands is the whole candidate on a main the change has not met, and a resolution can be right line by line and break the change's purpose. Dropping the review before admission saves as much and leaves the review that matters whole.
- **Recording the resolved head as approved.** No reviewer has judged it, and a record that says one did would be false evidence.
- **Letting a run that moved to the stronger model go back to the cheaper one for a conflict.** Its earlier failure is the evidence the cost design moves it on, and a small saving is not worth two designs disagreeing about one run.
- **Refusing to start items that touch the same files at once.** The files an item will change are only a forecast, the shared files are in a large part of the work, and holding such items would leave developer slots idle with ready work waiting, which the internal week does not allow.
- **Attempts without limit.** A change that keeps conflicting is a sign its files need splitting or its work ordering, which is the development manager's to decide.

# Who owns the work, and restarts

The harness owns a returned change from the hand back until it lands, or is put to the development manager. The wait, the count of attempts, the times the hold and the attempt began, the turn and whether it is spent, and the comparison are written to the run's record before the step they decide, so a restart reads them and goes on. A hold is worked out from those records each time the queue chooses, never kept apart from them, so it cannot outlive the returned change it is for, and a restart does not lengthen it. A wait whose record cannot be read ends and the attempt starts, because starting costs one attempt and never loses work; a hold whose record cannot be read holds nothing and makes no other change wait. Changes already handed back and waiting on the development manager when this lands stay with the development manager's decisions; nothing is moved back automatically.

# What a reviewer can check

- A code change that rejoins after a conflict is chosen before an entry waiting four hours, before priority 0 code and before documents; two of them in the queue go in the order they were handed back.
- A candidate already being checked is not stopped for it, and an entry its item depends on still goes first.
- A change whose candidate fails a check or its review rejoins under the ordinary order; one whose candidate conflicts again takes the next turn again.
- Two returned changes that share a file never hold at once: the later one's hold begins only when the earlier one has landed, been handed back again, been put to the development manager, ended its run, or had its two hours. It waits for the earlier one even while that one waits itself, and neither ever waits for the other.
- With three returned changes, where the second shares one file with the first and a different one with the third, they hold in the order handed back.
- A hold never stops a returned change that has rejoined, an entry the held change's item depends on, or priority 0 code; it stops any other entry that shares a file, and none that shares no file.
- An entry on the same files that is chosen after the hold begins is one of those kinds; an attempt waiting for a candidate is not made to wait by any other.
- A returned change whose item another returned change depends on goes before it, whichever was handed back first.
- A hold ends when the change lands, is handed back, is put to the development manager or its run ends, and at the latest two hours after it began; a restart does not lengthen it.
- A returned change that merges again when its attempt would start rejoins with no developer session, the next turn, and no attempt used.
- Each held entry is named with the change, the files and the latest time, on status and the dashboard.
- The third conflict still gets an attempt and the fourth goes to the development manager; a restart does not reset the count; each attempt after the first names the commit it conflicted with and why the hold did not prevent it.
- An attempt starts a new session, and the reviewer of its candidate is none of the sessions recorded as authors.
- A resolution that changes a file outside the conflicting ones is reviewed before it rejoins; a confined one is not, and its candidate is reviewed whole before it lands.
- An entry for a confined resolution records the reviewed head as approved, the resolved head apart from it, and the comparison; no surface shows the resolved head as approved before its candidate is.
- A required approval by a person is earned again on the new head.
- With `conflict_resolution_model` not set, the model is chosen exactly as before; with it set, a run already moved to the stronger model stays on it.
- Each attempt's spend, and the files and size of its conflict, can be read from the records as a conflict's and nothing else's.

# Build pieces, in order

Each is sized to [the size of a work item](../decisions/work-item-size.md), 300 to 600 changed lines. The Lead Product Manager owns admission and order; this design creates no items. Where code for an earlier form of a piece is already on main, such as the wait for entries ahead, the piece changes that code rather than adding beside it.

1. **Record what conflicts cost.** Each attempt and the reviews after it name the hand back that caused them; each hand back for a conflict records its files and conflicting lines; the weekly figures show the cost of conflicts for each landed code change and the files named most. Needs nothing first.
2. **The next turn and the hold.** Decisions 1 and 2, as two items: the turn first, then the hold with the start of the attempt. Both need the merge queue design's revision; the check of every waiting entry when main moves (yoyodyne-ifd.429.229) is needed by neither, and finds conflicts sooner where it is on main.
3. **Three attempts in fresh sessions.** Decision 3. The second and third attempts (yoyodyne-ifd.429.298) need stopped run recovery's revision; the fresh session does not. The reason a hold did not prevent a conflict is recorded as there being no hold until piece 2 is on main.
4. **The comparison, and rejoining without the earlier review.** Decision 4 (yoyodyne-ifd.429.300). Needs piece 3's fresh session and the merge queue design's revision.
5. **The setting and its trial.** Decision 5. Needs pieces 1 and 3, and repairing on the stronger model a run that started on the cheaper one (yoyodyne-ifd.435.91).

# What was read

For the first form of this design, at commit 7e34c434dba6: the merge queue design whole, the cost design whole, the decision on central code, the invariant on evidence, and the first part of `internal/orchestrator/mergequeueworker.go`, which shows a conflict reported with no candidate built and the candidate's checks and review bound to its generation. At commit 6e93792dea31: stopped run recovery from its first step to its end except the opening lines of that step, whose single attempt is quoted from the reviewer's finding and agrees with what was read of the step, and the merge queue design from its decision to its end. The cost design was not read again there; its section 4 is taken from the reviewer's finding and the earlier reading.

For this revision, at commit 0f35eeff1e4a: this document and the merge queue design whole, stopped run recovery from part way through its step 1 to its end, and `internal/runstate/mergequeue.go` and `internal/orchestrator/mergequeueorder.go` whole. The second chooses the next entry, and already holds back an entry whose item waits on another admitted entry's item. The code that starts an attempt to bring a change up to date and the spend record were not read; each build piece reads the code it changes first.