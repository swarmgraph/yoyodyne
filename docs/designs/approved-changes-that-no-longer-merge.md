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
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-23T20:05:43.10317Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3733, for document-3733.1 under the automatic approval policy
---
# Purpose

This design answers how an approved change that no longer merges gets back onto main with less repair and review spending (yoyodyne-ifd.429.294). It serves the goal that spend follows the work, and keeps the goal that every change is reviewed, by a role that did not write it, before it is integrated. Every landed change keeps its checks and independent review on the exact revision that lands.

It changes rules that three designs on main state: the order and the admission rule in [integration through a merge queue](integration-through-a-merge-queue.md), and the single attempt after a conflict in [stopped run recovery](stopped-run-recovery.md). It leaves the rule on moving to the stronger model in [reducing what the factory costs](reducing-what-the-factory-costs.md) as it is. Which document decides until each is revised, and which build piece waits, is stated in each decision and gathered under which document decides, below.

# What was found

The operator's assistant read the merge queue's records and the spend record for midnight to 10:20 PST on November 23, 2026. The queue landed 34 entries and handed back 29, of which 21 no longer merged onto main. Developer repairs cost $49.11 across 23 runs, and the reviews of those runs after their first repair $37.21: about $86 of $384.52, or 22%. The architect did not read those records.

The architect read the tracker's records of four items handed back more than once:

- listing a document approved automatically as never waiting on the operator (yoyodyne-ifd.432.52);
- listing a finished design that waits for a person to copy it as the operator's step (yoyodyne-4f8u);
- repairing a run that started on the cheaper model on the stronger one (yoyodyne-ifd.435.91);
- the merge queue asking GitHub to merge a candidate directly, after reading main (yoyodyne-ifd.429.242).

They show four causes of repeated spending:

- **The one attempt is used early.** A run gets one attempt to bring its change up to date with main. In all four, that attempt was made between 03:39 and 04:49 PST, the change rejoined the queue, and it conflicted again about five to six hours later. The development manager then decided a new run from the kept branch, which costs a decision and a new developer session.
- **A change is repaired while another that touches the same file is still ahead of it.** The first two of those items both conflict in `internal/readmodel/documentapprovals.go`.
- **A change that rejoins is a new entry.** It gets a new place and a new admission time, so the rule that takes an entry waiting four hours first counts from zero.
- **Two reviews follow each repair.** The run's change is reviewed before it rejoins the queue, and the queue then has the exact candidate reviewed.

## What cannot be told apart

The share of the $86 that conflicts caused is not established. The four items were handed back ten times that morning by the assistant's count, and the rest of the 23 runs were not read. If conflicts cost the same as other hand backs, 21 of 29 makes the share about $62, or 16% of the day's spending. That is an estimate and not a measurement. The tracker notes the time of each attempt to bring a change up to date, but the architect did not establish that a spend record says which attempt it paid for. Build piece 1 records it, so the share is measured from then on.

# Decision

## 1. The repair waits for entries ahead that touch the same files

The check of every waiting entry when main moves (yoyodyne-ifd.429.229) finds a conflict at once and hands the entry back. The attempt to bring that change up to date does not start until no waiting entry touching any of its conflicting files stands ahead of it. An entry is ahead when the queue's order would take it first, with the returned change placed by its age under decision 2 below once that is built, and by the order on main until then. Only an entry still waiting in the queue counts; one that was itself handed back does not, so two returned changes never wait for each other.

The wait is at most two hours from the hand back. It holds no developer slot and no candidate, and spends nothing. The run's record and yoyo status name the files and the entries waited for, and when the wait ends at the latest. When it ends, the attempt goes ahead of fresh work, as work on a kept change does.

The attempt then starts from main as it stands at that moment, never from main as it stood at the hand back. Where the change now merges without conflict, no developer session starts: it rejoins the queue with the same approved head, and this uses none of its attempts.

## 2. A change that comes back for a conflict keeps its age

An entry that rejoins after being handed back only for a conflict counts its wait from the run's first admission to the queue, so the four hour rule applies to it as if it had never left. Its new entry and admission time are recorded as before, with the first admission time beside them. A change handed back for a failing check or a refused review does not keep its age, because the change itself was at fault.

This changes rule 1 of the order in the merge queue design. Until that design's own revision lands, its order decides, and the part of build piece 2 that does this waits for it.

## 3. Up to three attempts, each in a fresh session

Step 1 of stopped run recovery says an approved change that conflicts with the target branch when it lands gets exactly one attempt to bring it level, outside the repair attempts, and that a second conflict stops the run as about the work. This decision replaces that number: a run gets up to three such attempts, one for each hand back for a conflict, and a fourth conflict stops the run as about the work. The rest of that rule stays: an attempt is not charged as a repair attempt, it is recorded as a reconciliation, and a failing check or a refused review after it is about the work and goes on as it does today, through the remaining repair attempts and then the development manager. The limit of three is held in code, and a restart or a new build does not reset the count.

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
| An entry's wait is counted from its own admission | merge queue design, rule 1 of the order | that design decides | the age part of build piece 2 |
| One attempt after a conflict at landing | stopped run recovery, step 1 | that design decides | the second and third attempts in build piece 3 |
| Only an approved head is admitted | merge queue design, durable admission and the harness's own queue | that design decides | build piece 4 |
| A run moves once to the stronger model for the rest of its attempts | reducing what the factory costs, section 4 | that design decides, then and afterwards | nothing; decision 5 keeps to it |

The architect writes the merge queue and stopped run recovery revisions once this design is on main.

# Expected saving

These are estimates from the figures above, they overlap, and none is a measurement. They take the conflict share to be about $62 for the period.

| Change | What it should save | How it is measured |
| --- | --- | --- |
| Waiting for entries ahead, and keeping age | some of the six repeat hand backs among the ten; up to about a quarter of the conflict share, $18 | conflict hand backs for each landed code change, and how many runs have more than one |
| Three attempts, fresh sessions | the development manager's decisions, the new runs, and part of the cost of writing the old session again | runs started by a decision after a conflict; median cost of one attempt |
| One review in place of two | about half of the review cost after a confined resolution, perhaps $13 | reviews for each resolved conflict; how often the candidate's review refuses a confined resolution |
| The cheaper model | not estimated; the trial says | cost and refusals for each side of the trial |

The measure for the whole is the cost of conflicts for each landed code change, over one week before and one week after pieces 1 to 4 are on main. If it has not fallen by a third, the architect revises this design from what the records show.

# Alternatives rejected

- **Resolving conflicts by a tool.** Keeping both sides of a conflict by rule can build and still be wrong, in code and in a guide, and the queue already lets Git merge everything it can. No record read says how many of the 21 such a rule would have settled. Build piece 1 records the files and size of each conflict, so this can be weighed on evidence.
- **Reviewing only the resolution and not the whole candidate.** What lands is the whole candidate on a main the change has not met, and a resolution can be right line by line and break the change's purpose. Dropping the review before admission saves as much and leaves the review that matters whole.
- **Recording the resolved head as approved.** No reviewer has judged it, and a record that says one did would be false evidence.
- **Letting a run that moved to the stronger model go back to the cheaper one for a conflict.** Its earlier failure is the evidence the cost design moves it on, and a small saving is not worth two designs disagreeing about one run.
- **Refusing to start items that touch the same files at once.** The files an item will change are only a forecast, the shared files are in a large part of the work, and holding such items would leave developer slots idle with ready work waiting, which the internal week does not allow.
- **Repairing a conflict the moment it is found.** With about twenty entries ahead, the next landing in the same file undoes it.
- **Attempts without limit.** A change that keeps conflicting is a sign its files need splitting or its work ordering, which is the development manager's to decide.

# Who owns the work, and restarts

The harness owns a returned change from the hand back until it rejoins the queue or is put to the development manager. The wait, the count of attempts, the first admission time and the comparison are written to the run's record before the step they decide, so a restart reads them and goes on. Nothing is kept only in memory between passes of the queue. A wait whose record cannot be read ends and the attempt starts, because starting costs one attempt and never loses work. Changes already handed back and waiting on the development manager when this lands stay with the development manager's decisions; nothing is moved back automatically.

# What a reviewer can check

- A returned change whose conflicting file is touched by an entry ahead of it starts no attempt until that entry lands or is handed back, or two hours pass; two returned changes never wait for each other.
- A returned change that merges again when its wait ends rejoins with no developer session and no attempt used.
- An entry that rejoins after a conflict is taken by its first admission time; one handed back for a failing check is not.
- The third conflict still gets an attempt and the fourth goes to the development manager; a restart does not reset the count.
- An attempt starts a new session, and the reviewer of its candidate is none of the sessions recorded as authors.
- A resolution that changes a file outside the conflicting ones is reviewed before it rejoins; a confined one is not, and its candidate is reviewed whole before it lands.
- An entry for a confined resolution records the reviewed head as approved, the resolved head apart from it, and the comparison; no surface shows the resolved head as approved before its candidate is.
- A required approval by a person is earned again on the new head.
- With `conflict_resolution_model` not set, the model is chosen exactly as before; with it set, a run already moved to the stronger model stays on it.
- Each attempt's spend, and the files and size of its conflict, can be read from the records as a conflict's and nothing else's.

# Build pieces, in order

Each is sized to [the size of a work item](../decisions/work-item-size.md), 300 to 600 changed lines. The Lead Product Manager owns admission and order; this design creates no items.

1. **Record what conflicts cost.** Each attempt and the reviews after it name the hand back that caused them; each hand back for a conflict records its files and conflicting lines; the weekly figures show the cost of conflicts for each landed code change and the files named most. Needs nothing first.
2. **The wait, and the age kept.** Decisions 1 and 2. Needs the check of every waiting entry when main moves (yoyodyne-ifd.429.229); the age part also needs the merge queue design's revision.
3. **Three attempts in fresh sessions.** Decision 3. The second and third attempts need stopped run recovery's revision; the fresh session does not. Does not need the check of every waiting entry when main moves (yoyodyne-ifd.429.229).
4. **The comparison, and rejoining without the earlier review.** Decision 4. Needs piece 3 and the merge queue design's revision, and does not need the check of every waiting entry when main moves (yoyodyne-ifd.429.229).
5. **The setting and its trial.** Decision 5. Needs pieces 1 and 3, and repairing on the stronger model a run that started on the cheaper one (yoyodyne-ifd.435.91).

# What was read

At commit 7e34c434dba6: the merge queue design whole, the cost design whole, the decision on central code, the invariant on evidence, and the first part of `internal/orchestrator/mergequeueworker.go`, which shows a conflict reported with no candidate built and the candidate's checks and review bound to its generation. At commit 6e93792dea31: stopped run recovery from its first step to its end except the opening lines of that step, whose single attempt is quoted from the reviewer's finding and agrees with what was read of the step, and the merge queue design from its decision to its end. The cost design was not read again there; its section 4 is taken from the reviewer's finding and the earlier reading. The code that starts an attempt to bring a change up to date, the code that admits an entry, and the spend record were not read; each build piece reads the code it changes first.