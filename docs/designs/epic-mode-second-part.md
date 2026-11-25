---
id: epic-mode-second-part
kind: design
title: 'Epic mode, second part: how an epic ends, what joins it, review comments, cost, and when each role runs'
supports:
    - v1-goals
    - operations-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-25T07:50:01.30235Z
      reason: 'yoyodyne-9k1y.9: sends document-3982.1 again with the reviewer''s five findings answered. A closed pull request and a cancelled member go to the Lead Product Manager; every step a role owes counts as working and has an event that wakes the role; what is lost when both progress files go is stated whole; what piece 13 narrows is said here.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-25T07:50:01.30235Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3985, for document-3985.1 under the automatic approval policy
---
# Epic mode, second part

Epic mode (yoyodyne-9k1y) lets the operator name the epics the factory works on, and starts nothing outside them. The first part of the design covers naming an epic, the one decision asked before anything starts, and what status says is held out. It was not published when this was written; what it settled is built, and [the operations guide](../operations.md) describes it. This part covers the rest: what the harness remembers about an epic, how an epic ends, what may join it, how it is checked whole, what happens to review comments, what it costs, and when each role runs. It serves the autonomy goal, because the operator's one act of naming is enough to see an epic through, and the goal that spend follows the work.

The pieces are numbered from 7, after the six pieces of the first part, which are built. Nothing here is built when this is written.

## What it rests on

The record of the epics the operator named (`internal/epicscope`) and the one decision (`internal/maystart`) were read at commit 37ea054b6ee3, except the file of the operator's acts. The code that splits an item, the repair from pull request feedback, the cost records and the code around recurring passes were not read. A builder who finds that code differing from this text stops and asks the architect.

## The progress record: piece 7 (yoyodyne-9k1y.9)

The record of what the operator named is written only by the operator's commands, and stays so. The harness keeps a second file beside it, `epic-progress.json`. It is written under a lock, whole, to a temporary file that replaces the old one. A file that cannot be read whole is never read as no file.

**One entry for each naming.** An entry is found by the work item and the time it was named, both as the operator's record holds them. An epic that ended and was named again starts with an empty entry. The old entry is kept, and nothing is carried over from it.

For each naming it holds:

- the pieces that joined under the joining rule, each with the member or check it came from, the judgment that let it join, and that judgment's session;
- review comments already acted on, by the forge's identifier, with what became of each;
- each check of the whole epic, with the commit it read and what it found;
- that the epic was delivered, when, and on what evidence;
- each start and each paid call reserved for the epic, what it is for, and its result once known. Where an existing durable record already holds that reservation, such as a run's record, the entry points to it and no second record is made.

### What shows that an entry once existed

Nothing outside the file in today's code can show it, so a third small file does. It lists the namings whose entry has been begun, and nothing else. Only the package that keeps the progress record writes it, under the same lock and in the same whole way.

1. Before the harness first acts on a naming under these pieces, it writes the empty entry, which notes whether the epic was named before this piece landed as far as the harness can tell, then lists the naming as begun, and only then acts. Each write is finished before the next.
2. A naming with neither an entry nor a place in the list has not been begun. It has no joined pieces and no reservations. The one decision judges it from the operator's record alone, and writes nothing.
3. An empty entry that is not in the list is what a stop between the two writes leaves. The harness lists it and goes on.
4. A naming in the list with no entry, or an entry that is not empty and is not in the list, is unreadable. That epic lets nothing start. Other open epics carry on.
5. Either file being there and not readable whole means the decision cannot be made, as with the operator's record. Nothing the decision is asked about starts until the file is mended. The operator's own conversation is still never held.
6. Both files missing together reads as not begun. What that loses is below.

Losing both files loses everything the harness remembered of every naming:

- joined pieces are outside their epic again, so they do not start, and another epic may take one;
- the count toward the limit of twelve and the rounds of correction start again;
- a review comment may be judged again, at the cost of a call. The existing repair keeps its own record of the comments it acted on, and that record is not in these files;
- a delivered epic owes its check of the whole again, and shows Working until it is made;
- a paid call that only these files held may be made twice.

This is accepted. The harness never removes either file, so it can happen only by a person removing both or restoring the folder without them. That person's reach already covers the operator's record beside them, whose loss turns epic mode off. Nothing starts because of it that the operator's record does not name, and every piece that joins afterwards is judged as before. Where the runs of a named epic are older than any entry for it, status says that its progress is missing, so the loss is seen.

Status names an unreadable epic and the file. `yoyo epic end` followed by `yoyo epic start` mends rule 4, because the new naming starts empty. Both commands work on an epic whose progress cannot be read, where the files themselves can be. While epic mode is off neither file is read to decide a start.

### What the decision and the commands read

The one decision reads all three files while epic mode is on. No block a role can write reaches any of them. A role's answer is evidence, and the harness writes the entry only where the rules allow it. The operator's commands never write the progress file or the list.

An item belongs to at most one open epic. `yoyo epic start` and `yoyo epic add` refuse an item that another open epic holds, as a member or as a joined piece, and name that epic. A prerequisite two epics share is placed in one of them by the operator.

The check for that and the write that follows are made under one lock. The lock is the one the operator's record has. An operator's act takes it as now, and reads the progress file while holding it. A join takes it too, so that no act of the operator comes between, then the progress lock, always in that order. A join takes the lock without being able to write the operator's record: the record's package offers a function that holds the lock and gives only a reading.

Epics that already overlap when this lands are reported, and their shared items are not started until the operator places them.

The files are read again at a restart. Each start and each paid call is reserved before it is made, and a reservation with no result is looked up before anything is sent again. Where the result cannot be known, the epic says so and the development manager decides, as for any run whose result is unknown.

A project that names no epic has none of the three files, and nothing here runs.

## Where an epic stands: piece 8 (yoyodyne-9k1y.10)

The read model works out one state from the records each time. The first that fits applies.

1. **Cancelled:** the operator ended it before it was delivered.
2. **Delivered, integrated:** every member's change is on the target branch, and the check of the whole epic passed.
3. **Delivered, handed over:** the same, for a project where a person merges. Each change is approved and its pull request is open, but at least one is not merged. It becomes integrated when they all are. Where a person closes one unmerged, the epic is open again and Working: the item goes back to the Lead Product Manager, parked, as [the publishing guide](../configuration/publishing.md) says, and that role decides whether to drop it, revise it or admit it again.
4. **Working:** any member is running or ready, the check of the whole epic is owed, or a role owes any step: a decision about a stopped member, a cancelled or given up member, a closed pull request, a question, missing evidence, a third failed check, or a piece to be written. A member that is ready only because the budget refuses its start does not count as ready here.
5. **Out of budget:** nothing under way remains, and the next start is refused by the epic's budget.
6. **Waiting on the operator:** everything left waits on an answer or a step only the operator can give, for a reason on the closed list.
7. **Blocked outside the factory:** everything left waits on something else: a provider, the forge, another person, or an item outside the members.

While it is Working, status names each step owed and the role that owes it.

Only the two delivered states and cancelled end an epic. A member closed without a landed change counts only where its item is judged by its summary and the reviewer approved it, or where the operator ended that member in words the record keeps. A member cancelled or given up by a role, or with an unanswered question, leaves the epic open and is named. It is never shown as delivered.

**A cancelled or given up member is the Lead Product Manager's to decide**, and so is one whose pull request a person closed. That role admits the item again, or has the development manager write a piece in its place, which joins by the joining rule as a split of that member. Such a member counts as done when that piece is. Where the epic should be delivered without the item, only the operator can take it out, because the operator named it and the epic holds everything else back until it ends. The operator is then asked, for the reason of lifting a hold the operator placed, with the command that does it.

Until piece 10 lands, an epic whose members are all done shows Working and says the check of the whole epic is not built yet.

The delivered entry in the progress record is evidence, and the state is still worked out from all the records. An epic opens again when the operator adds a member after it was delivered, or when a person closes one of its pull requests unmerged. The old delivered entry is then kept as history and no longer counts. If another open epic holds one of its items by then, both are named, and that item is not started until the operator places it.

A suggestion found during an epic is filed and admitted as today. It starts nothing, wakes nobody and is asked of nobody. Its record names the epic during which it was found, and `yoyo status` counts such items beside the work held out. A critical report still goes where it goes today. No second list is kept.

## What may join an epic: piece 9 (yoyodyne-9k1y.11)

A new piece joins an open epic without asking the operator only when all of these hold.

1. It was made by one of three acts: the development manager splitting or replacing a member, a correction from the check of the whole epic, or a correction asked for in review of a member's change. The development manager writes the piece in all three.
2. Its record names the member or the check it came from.
3. A reviewer session that did not write the piece, and did not make the check or the review it came from, was given the copied description and the piece's text. It answered that the piece asks only for what the description already asks.
4. Fewer than twelve pieces have joined that naming.

Twelve is a limit in code, and no command or setting raises it. The operator may still put any further item in with `yoyo epic add`. That is the operator's own act, so it does not count toward the twelve.

Anything else stays outside: an item attached under the epic, made a prerequisite of a member, moved under it, or made by any other act. Where the reviewer answers that a piece is outside, or cannot tell, or twelve have joined, it does not join, starts nothing, and is a question about scope.

The judgment is a paid call. It is reserved before it is made, and looked up before it is sent again. This piece builds the judgment, its limit, and the split that uses it. Pieces 10 and 11 use it for the other two acts.

## The check of the whole epic: piece 10 (yoyodyne-9k1y.12)

When every member is done, the harness starts one reviewer session that did none of the work. It is given the epic's description as it was copied when the operator named it; each member's landed change and review; the target branch at one recorded commit, or the approved heads where changes were handed over; and the results of the project's declared checks on that commit.

It answers for each thing the description asks: met, with where it looked; not met, with what is missing; or cannot be told, with why.

- **All met:** the epic is delivered.
- **Not met:** the development manager writes one correcting piece for what is missing. It joins under the joining rule, judged by a different session. The check is made again when it lands.
- **Cannot be told:** where the description itself reserves the step for a person, the epic waits on the operator for that step. Otherwise the Lead Product Manager says what evidence is missing, and the development manager writes the piece that gives it.

After two rounds of correction, a third answer of not met goes to the Lead Product Manager, who decides on one more round or asks the operator about scope.

`yoyo epic start` refuses an epic whose description is empty, and says so. It refuses no other description, because no code can tell whether words ask for something that can be checked. A description that asks for nothing checkable shows itself as cannot be told, and the Lead Product Manager then says what evidence would settle it.

## Review comments and questions about scope: piece 11 (yoyodyne-9k1y.13)

Piece 11 waits for piece 10 as well as piece 9, because adding to a delivered epic needs a delivered epic.

- A change request on a member's open pull request is judged before its repair starts. The harness records the forge's identifier of the comment first, then reserves the judgment. The reviewer session of the joining rule is given the copied description, the member and the comment. Judged to ask only for what they already ask, it goes through the repair from pull request feedback that exists, on the same pull request, once. Otherwise no repair starts and it is a question about scope. Whose comments are accepted is as the existing repair decides.
- Feedback after a member closed, while the epic is open, becomes a new visible piece naming the comment and the member, written by the development manager. It joins only by the joining rule.
- Feedback after the epic was delivered becomes a visible item that starts nothing. It is put to the Lead Product Manager, who asks the operator whether to add it. Adding it with `yoyo epic add` opens the epic again. While any epic is open, such feedback starts a pass of that role. With none open it waits: status shows it beside the delivered epic, and it is put in front of that role's next turn of any kind, including one the operator's own message starts.
- A request for something new is a question about scope, however it was worded. The harness decides nothing by reading words.

The Lead Product Manager answers a question about scope first. The answer may be that the epic can be delivered without the piece, or that the piece should be rewritten to fit, in which case it is judged again. Adding what the description does not cover is the operator's. Naming an epic is a hold the operator placed on everything else, and adding an item lifts it for that item, so the reason is lifting a hold the operator placed, and the closed list does not change. The operator is asked only where the epic cannot be delivered without the piece, or where a person asked for it in review. One question is asked for one piece, with the command that adds it, and the operator may ask about it before answering. The other members carry on meanwhile. Anything else is a suggestion.

## What an epic costs: piece 12 (yoyodyne-9k1y.14)

An epic's cost is the sum of what is attributed to it: every provider call of its members' runs and reviews, its checks and joining judgments, and each role pass it woke. A pass woken for two epics is shared equally between them. Calls whose cost is unknown are counted and shown as unknown, never as nothing. Nothing is counted twice.

`yoyo epic budget <epic> <amount>` sets a limit, and there is none unless it is set. It is kept in the record only the operator's commands write. It is measured in provider reported cost, which status names. Calls with unknown cost are shown beside it and do not count toward it. The limit stops new starts only. Work already started finishes its repairs, its review and its landing, and a check of the whole epic that is owed is made. The epic may therefore end above its limit, and status shows by how much. At four fifths of the limit status says so. Raising it is the operator's command.

Role passes are counted once piece 13 lands, and checks and joining judgments once pieces 10 and 9 land. The tests for those costs belong to those pieces.

## When each role runs: piece 13 (yoyodyne-9k1y.15)

Today, while epic mode is on, the one decision lets a pass of the Lead Product Manager, the development manager or the architect start whenever any epic is open, so the clock still starts them. It refuses those passes when no epic is open, and refuses a program manager's pass always. This narrows the first case only, and that rule applies as it is until this piece lands. With this piece, a role's pass is not started by the clock. It is started by an event of an open epic that the role owes something for.

- **Lead Product Manager:** a question about scope; a check of the whole epic that could not tell, where the evidence is that role's to name; a third failed check; a cancelled or given up member; a member's pull request closed unmerged by a person; feedback on a delivered epic, while any epic is open; a member with no description to build from.
- **Development manager:** a stopped member; a member too large for one change; a piece to write, whether a correction, the missing evidence or a replacement; a paid call or a start whose result cannot be known; a follow-up of a member that fell due; an automatic hold on new work, whatever caused it.
- **Architect:** a member held for design or carried in the architect's conversation; a change proposed to a design a member needs; a document that answers a member and that a run handed back.
- **Each of those three:** a report or a question waiting on that role that names a member, and a member's ordinary comment where an existing rule gives it to that role.
- **Reviewer:** a candidate, a check of the whole epic, or a joining judgment, as each comes.
- **Program managers:** none.

Every step these pieces give a role is in this list. A builder who finds one that is not stops and asks the architect, because nothing else would start that step.

While any epic is open, a critical report starts a pass of the role it waits on even when it names no member, because waiting could cost more than the pass.

Events are gathered for ten minutes, so several at once cost one pass. An event is settled when its cause is: the stop decided, the design landed, the question answered. A pass that ends without settling one is started again after an hour. After two such passes the event goes to the next role up, and from the Lead Product Manager to status as a problem.

When every open epic waits on the operator or on something outside, no event is owed and nobody runs. A critical report or an automatic hold still starts its pass then. An answer, a landed change or the cleared cause is the event that wakes the roles. With no open epic nothing is started, the review of unmet goals included; what waits is shown in status. The harness's own detection of stops, waits and unhealthy services uses no model and runs as always. The operator's own conversation is never held.

## What a woken role is sent: piece 14 (yoyodyne-9k1y.16)

A provider's cached context lapses soon after a turn, so a role woken rarely pays the full price for everything it is sent. Continuing a long conversation would then be the most expensive choice, and a turn sent only to keep the cache would be spending with no work in it. So an event's pass starts in a session of its own, as a recurring pass does now. It is given the role's instructions and memory, the account of the role's previous pass, the product intent whole, the epic's description, the event, the members it concerns, and the reports that are the event. It is not given the rest of the queue or the list of shipped documentation, which it can read by name. The saving is measured by the epic's cost record. No figure is claimed before that.

## Order

Piece 8 waits for piece 7. Pieces 9 and 12 follow 7 and 8 as their items say. Piece 10 waits for 8 and 9. Piece 11 waits for 9 and 10. Piece 13 waits for 8, 10 and 11, and piece 14 for 13.

## Rejected

- **Marking in the operator's record that progress has begun.** It would give the harness a way to write the record that only the operator's commands write.
- **No proof at all, reading a missing entry as empty.** A lost entry would let a paid call or a repair be made again without anyone being told.
- **Protecting against losing both files together.** It needs a third place for the same fact, which the same removal could also take. What is lost is listed under piece 7: limits and rounds start again, joined pieces fall out and calls may be repeated, and status shows the loss.
- **Progress kept for each epic and not each naming.** An epic named again would inherit joined pieces the operator did not see.
- **Raising the limit of twelve by command.** Adding an item is already the operator's act, and a second command would authorize pieces nobody has seen.
- **The checking reviewer writing the correction.** The session that found a thing missing would then be setting the work that it judges.
- **A cancelled member counted as done by a role's word.** The operator named it, and an epic would be shown as delivered without something the operator asked for.
- **Asking the operator about every closed pull request or cancelled member.** Admitting the work again or replacing it is a role's decision.
- **Feedback on a delivered epic starting a pass while no epic is open.** With nothing named the operator has asked for nothing to run, and the feedback loses nothing by waiting where it is seen.
- **A cost limit that stops work under way.** It would leave paid work unfinished.

## How a reviewer checks it

- Piece 7: a test for each of the six rules on what shows an entry existed, including a stop between the two writes; status says progress is missing after both files are removed; a listing of every writer of each file; a test that a naming ended and made again has a new empty entry and the old one unchanged; a test that a join and an operator's add of the same item at once put it in one epic only.
- Piece 8: records for every state, in the order in which they apply; one cancelled member or one open question is never shown as delivered; an epic with only a cancelled member left, or only a pull request closed unmerged, is Working and names the Lead Product Manager; adding a member after delivery opens the epic again.
- Piece 9: a thirteenth piece does not join; an item the operator adds is not counted; the judging session wrote nothing it judges and gets the copied description.
- Piece 10: each answer and what follows; only all met delivers; an empty description is refused and no other is.
- Piece 11: the same comment read twice starts one judgment and at most one repair; one judged outside starts none; feedback on a delivered epic with none open starts nothing and is in status.
- Piece 12: past its limit an epic starts no member, and a run under way still lands; unknown cost is never nothing.
- Piece 13: no pass starts by the clock; one test for each listed event shows the pass it starts; a critical report that names no member starts one pass; a day with every epic waiting and no such report starts none.
- Piece 14: the pass is sent what the list says and nothing else.
- Every piece: with no epic named, every existing test passes unchanged.
