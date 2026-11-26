---
id: epic-mode-first-part
kind: design
title: 'Epic mode, first part: naming an epic, one decision asked before anything starts, and what status says'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-25T09:23:12.344538Z
      reason: 'yoyodyne-9k1y: sends document-4011.1 again with both review findings answered. States the decision on yoyo review here as well as pointing at the rulings, which now carry it on main; says what is kept for a refused work item and a refused team branch update, from refusals.go at 365b9591b2e9, and limits the sentence about the rulings to the kinds they cover; settles that turning epic mode off does not by itself clear a refused update.'
    - action: amended
      by: architect
      at: 2026-11-26T09:15:18.468737Z
      reason: 'yoyodyne-9k1y.11: approves amendment-fd4f3397e758a3caa03bb6cfd85d799f. Names the rule for the judgment of whether a piece joins a named epic, and the same rule for the check of the whole epic (yoyodyne-9k1y.12) and the judgment of a review comment (yoyodyne-9k1y.13), which the reviewer''s report-2dea6e6f says would meet the same gap. Read whole at 0f35eeff1e4a.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-25T09:23:12.344538Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 4014, for document-4014.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-11-26T09:15:18.468737Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 4084, for document-4084.1 under the automatic approval policy
---
# Epic mode, first part

The operator directed on November 23, 2026 that the factory can work only on the epics the operator names and start nothing else (Epic mode, yoyodyne-9k1y). This document covers how an epic is named, the one decision asked before anything starts, and what status says. [The second part](epic-mode-second-part.md) covers how an epic ends, what may join it, how an item is taken out, review comments, cost, and when each role runs. [The epic mode rulings](naming-an-epic-again.md) settle questions raised while this part was built, and hold the detail of each path they rule on.

It serves the autonomy goal: the operator authorizes an epic once. Goal mode is what the factory does otherwise, and a project that names no epic behaves exactly as it did before epic mode existed.

## What it rests on

At commit f29e37efdefc, `internal/maystart/paths.go` and `internal/maystart/maystart.go` were read whole for this text, and at commit 365b9591b2e9 `internal/maystart/refusals.go`, the rulings and the second part were. The record and the operator's commands were read whole for the second part, at commit 56a0190c2df1, and were not read again. The code that each path starts in was not read for this text. Where the rulings name such a place, they say what was read.

Earlier texts of this document were never published. Their closing sections could not be read back, so the sections on checks and on rejected choices are written from the rules below.

## The record of what is named

One file for the product, `epic-scope.json`, stands beside the file that records the hold on new work. The package `internal/epicscope` owns it. It is written under a lock, whole, to a temporary file that replaces the old one. A file that cannot be read is an error and starts nothing that the decision is asked about. It is never read as absent.

It holds whether epic mode is on, and one entry for each time an epic was named:

- the epic's work item;
- when the operator named it, from where, and the operator's words if any were given;
- the members: the work items in scope, by identifier, each with when it was added and by which act;
- a copy of the epic's description as it read when it was named, so that a later edit is not a wider authorization;
- when it was ended, and the operator's words, once it is.

At most one entry for an epic is open, and one that has ended is never changed or removed.

Epic mode stays on when the last epic ends. The operator turns it off by a separate act, so ending the last epic starts nothing unrelated.

## How the operator names an epic

- `yoyo epic start <item>` turns epic mode on where it is off and names the epic. It prints every open item below the epic, and records exactly that list as its members. The rulings say what it does for an epic that has ended or is still open.
- `yoyo epic add <epic> <item>` adds an item to the members.
- `yoyo epic end <epic>` ends it. The record is kept.
- `yoyo epic list` shows the record.
- `yoyo epic off` returns the factory to goal mode and ends no epic.

`start` and `add` refuse an item that another open epic holds, and name that epic. `start` is refused while any path on the closed list below is not wired.

Only the operator's own act writes the record. The commands are refused to a process the harness launched for a role or started itself. No block that a role can write reaches the record, and the scheduler has no call that writes it.

Membership is always read from the recorded list, never worked out from the tracker. So an item's type, a parent link made or moved afterwards, a prerequisite two epics share, and an item a role attaches all leave the scope as it was. The epic itself is not a member, so it is not run as work of its own.

A prerequisite outside the members is not started. The member waits on it, and status says so. Until the second part's rule on joining is built, a child created after the naming waits until the operator adds it.

## The one decision

The package `internal/maystart` holds the only code that answers whether something may start. It is given what is about to start and by which path. It returns that it may, that it may not and why, or that it could not tell. Not being able to tell starts nothing. It reads only `epic-scope.json`; piece 7 of the second part adds the files it describes.

The operator's own conversation with a role always may start, and so may a question one role asks another inside a turn that was allowed to start. Both are answered before the record is read, so a record that cannot be read never keeps the operator from talking to a role about it.

Then, in order:

1. The record cannot be read: it could not tell.
2. Epic mode is off: it may start.
3. What is about to start is not said, is on no path in the closed list, or is not what that path starts: it could not tell.
4. A developer run, a repair, a continuation or any other run for a work item: only where the item is a member of an epic that has not ended. The refusal names the open epics that the operator could add it to.
5. A run that publishes a document: only where the work items it answers include a member of an open epic, or the open epic itself. The epic itself counts because its own designs name it. A document that answers no work item waits.
6. A pass of the Lead Product Manager, the development manager or the architect: while some named epic has not ended. Piece 13 of the second part narrows this.
7. A program manager's pass, the review of unmet goals, and reading outside messages for work: never.
8. Bringing the target branch up to date with the team's branch: while some named epic has not ended.

The decision is asked beside the hold on new work, the hold for design and every other existing check. It replaces none of them, so naming an epic does not bypass its design.

A run the operator names on the command line is asked too. The hold on new work lets such a run through, but here it is refused for an item outside the members. A review of a branch that a person asks for with `yoyo review` is different. It is a person's own command naming one branch, so it always starts, it does not ask, and nothing about it is kept as refused. It is on the closed list, marked as always starting, so that the check over the whole module knows of it. Nothing the harness starts by itself may use that path. The rulings say the same.

### What is already under way

- A session running when epic mode is turned on finishes the step it is in. A run of an item outside the members then waits before its next call to a provider. Its record says it waits for its epic, and it gives back its developer slot. It keeps its claim, branch and checkout, and is picked up when the item becomes a member or epic mode is turned off.
- A change already in the merge queue carries on to landing, its review included, because nothing new is begun for it. If it is handed back, the attempt that would repair it asks the decision first.
- A document run already started carries on to its end, because its review is all that is left. A document that may not start stays confirmed and waiting, and nothing is counted against it.
- A run already bringing in a pinned outside file carries on to its end, as the rulings say.
- A role's pass already claimed takes all its turns.

## Every path that starts work

The closed list is `Paths` in `internal/maystart/paths.go`. At f29e37efdefc it holds these, and marks every one as wired, which means that its code asks the decision first or, for the last, does not ask.

| Path | Rule above |
| --- | --- |
| the scheduler pulling ready work | 4 |
| a run the operator names | 4 |
| the test run that checks whether a hold can lift | 4 |
| a fresh run the development manager decides after a stop | 4 |
| a repair the development manager decides | 4 |
| finishing an interrupted merge | 4 |
| continuing a silent run or one whose checks ran out of time | 4 |
| picking up a waiting or interrupted run | 4 |
| a repair from feedback on a pull request | 4 |
| publishing a document | 5 |
| the attempt to bring a handed back change up to date | 4 |
| the run that resolves a conflict with the team's branch | 4 |
| the run that brings in a pinned outside file | 4 |
| bringing a change queued to merge on GitHub up to date with its target branch | 4 |
| bringing a change that waits for a person to merge it up to date with its target branch | 4 |
| taking up the checks of a run that stopped because GitHub refused the harness's login | 4 |
| a role's recurring pass | 6 and 7 |
| a pass started by an event, a label or a request | 6 and 7 |
| the review of unmet goals | 7 |
| reading outside messages for work | 7 |
| bringing the target branch up to date with the team's branch | 8 |
| a review of a branch that a person asks for with `yoyo review` | always starts |

A path in the fourth rule may also start a run that publishes a document, and that run is judged by the fifth.

Code that starts work in a way not listed here adds its path to the list, and the architect rules on which rule judges it. While any path is marked as not wired, `yoyo epic start` is refused.

### The paid sessions the second part adds

The second part adds three reviewer sessions that judge an epic and belong to no run. Each adds its own path to the closed list in the piece that builds it, and one further rule judges all three. It stands after the third rule, beside the five that follow.

| Session | Piece |
| --- | --- |
| the judgment of whether a piece joins a named epic | 9 (yoyodyne-9k1y.11) |
| the check of the whole epic | 10 (yoyodyne-9k1y.12) |
| the judgment of a review comment | 11 (yoyodyne-9k1y.13) |

- Such a session may start only for a naming that has not ended and whose progress can be read. Where the naming has ended, it may not start. Where its progress cannot be read, the decision could not tell.
- Being allowed to start is not the reservation. The call is still reserved in the epic's progress before it is made, as the second part says. So with epic mode off, where the second rule answers first, nothing is paid for a naming that has ended or whose progress cannot be read.
- Nothing is kept of a refusal. A naming that has ended owes no judgment, and status already names an epic whose progress cannot be read. The piece stays outside the epic, the check stays owed, or the comment starts no repair.
- A path is marked as wired by the piece that builds it, in the same change.

Ruled on November 25, 2026, approving the change proposed by the developer of piece 9 (amendment-fd4f3397e758a3caa03bb6cfd85d799f) and extending it to the other two sessions. The code of the three pieces was not on main when this was written, and was not read.

Rejected: judging them by the fourth rule, because they belong to no run and the epic itself is not a member; judging them by the sixth, because an open epic elsewhere says nothing about the one being judged; keeping each refusal, because nothing would be waiting for it to clear.

A reviewer checks, for each of the three: with the naming open and its progress readable it may start; with the naming ended it may not; with the progress unreadable it could not tell; in the last two no call is made and nothing is kept, whether epic mode is on or off.

## What status says

`yoyo status` says whether epic mode is on, which epics are open, and what is held out because of it.

A refused start is kept in one record, `not-started.json`, with one writer, `internal/maystart/refusals.go`. A failed write is reported and never permits a start. What is kept depends on what was refused:

- **A run of a work item** has one entry for each item and path, with the reason last given and when it was first and last refused. The time of the last refusal may be up to ten minutes behind, because the same refusal is not written again sooner. The item's entries are cleared when a run of it is allowed to start, when the item closes, or when the operator's act lets it start: adding it to an epic, naming one that includes it, or turning epic mode off.
- **Bringing the target branch up to date with the team's branch** has one entry for each target branch, with the same reason and times. It is cleared when the update begins, or when a check finds that none is due for that branch. Being allowed to start does not clear it, and neither does the act of turning epic mode off: the next check begins the update or finds none due, and that clears it. Where the rulings say that turning epic mode off clears it, this is what is meant.
- **A document, a role's pass, the review of unmet goals and the reading of outside messages** are as the rulings say. The last two keep nothing: status asks the decision about them.

A refused start is not a report, spends no attempt, and is not shown as a failure of the run or the item.

## Who carries it on

The record is the operator's. The decision and the record of refused starts are the harness's, and need no role. At a restart both files are read again and each path asks again, so nothing is lost by a stop except a refused summons after an automatic hold, which the rulings accept.

## Rejected

- **Working out membership from the tracker.** A parent link a role makes would then widen what the operator authorized.
- **Turning epic mode off when the last epic ends.** Everything unrelated would start because one epic was finished.
- **Letting a role or the scheduler write the record.** The operator's act would no longer be the authorization.
- **A check of its own in each path.** The paths would come to disagree, and a new one could be forgotten. One closed list lets `start` refuse while any is missing.
- **Reading a missing answer as a yes.** A record that cannot be read would turn epic mode off without anybody saying so.
- **Holding out a document that answers the epic itself.** The designs of an epic could not publish until it was over.
- **Clearing a refused update as soon as it may start.** It is still owed until it begins, and status would stop saying so.

## How a reviewer checks it

- With no record, every existing test passes unchanged and everything may start.
- A record that cannot be read gives could not tell for everything except the operator's conversation and a role's question.
- A test for each of the eight rules, and one that a path not on the list, or a start of the wrong kind for its path, cannot be told.
- For each path in the table: with epic mode on and its item outside the members nothing is started or spent, and once the item is a member it starts. For the last, it starts in every case and nothing is kept.
- An item reparented or attached under a named epic does not become a member.
- A run outside the members waits when epic mode is turned on, gives back its slot, keeps its claim, branch and checkout, and is picked up when epic mode is turned off.
- A refused item's entries are cleared by each of the things listed above. A refused update stays recorded when epic mode is turned off, and is cleared when it begins or when none is due.
- The record's only writers are the operator's commands, and each refuses a process the harness started.
