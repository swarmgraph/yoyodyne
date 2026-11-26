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
      at: 2026-11-25T07:58:37.824799Z
      reason: 'yoyodyne-9k1y.1: sends document-3985.1 again with the reviewer''s three findings answered. Piece 8 builds yoyo epic remove, the operator''s act that takes one item out, kept in the operator''s own record; shared items are corrected to match what the commands already refuse; a critical report waiting on a program manager starts the Lead Product Manager''s pass; the list of events names the rewrite, later feedback and the judgment of a comment.'
    - action: amended
      by: architect
      at: 2026-11-25T19:16:46.277714Z
      reason: 'yoyodyne-9k1y.10: settles the three cases the developer of piece 8 reported: which state a mixed wait takes, how an item judged by its summary is recognised, and how an ended epic treats a question put to the operator whose record holds no time. It adds the reviewer''s checks for them. Read whole at main 529de40314b7.'
    - action: amended
      by: architect
      at: 2026-11-25T21:35:41.557476Z
      reason: 'yoyodyne-9k1y.12: adds the rule for a piece edited after it joined a named epic, answering report-dd30c959 and report-38f40667: it starts a developer only on text a reviewer session judged inside the epic. Piece 10 builds it. The rest of the section is copied unchanged from main 0f35eeff1e4a, read whole. An earlier send was refused for lack of room.'
    - action: amended
      by: architect
      at: 2026-11-26T11:06:05.815236Z
      reason: 'yoyodyne-9k1y.12: rules the three questions the development manager''s split of piece 10 left open (report-3245043b, report-3ad72097). The harness runs the configured checks on the commit the check reads where no result for it exists. A correcting piece joins by an owed correction in the progress record, linked by the action that creates it, never by a note''s words. The answer on scope is the Lead Product Manager''s recorded handling of a report the harness files. The rest of the section is copied unchanged from main deaa4fd73275, read whole.'
    - action: amended
      by: architect
      at: 2026-11-26T15:21:04.932043Z
      reason: 'Two reviewer notes on piece 10b (report-27a85a17a1883639a00ad95379887b60, report-ee8afe0e8d903a4dcea74ead97df0090): the first sentence of this section says status calls the check of the whole epic not built until piece 10 lands, which piece 10b makes untrue. It now says what status shows before and after that piece lands. The other two paragraphs are copied unchanged from main e8814efbc71c, read whole.'
    - action: amended
      by: architect
      at: 2026-11-26T16:09:49.149605Z
      reason: 'yoyodyne-9k1y.12.3: two developer reports (report-2db8e2cca12b151d744c55a1c21cb574, report-ef627770dde2315ea4f97f96e3124f3b) say the design never says what records the Lead Product Manager''s answer naming missing evidence. It is her recorded handling of a report the harness files, as for scope; a handling naming the operator''s reason waits on him. The rest of the section is copied unchanged from main add5a7c11521.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-25T07:58:37.824799Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3992, for document-3992.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-11-25T19:16:46.277714Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 4048, for document-4048.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-11-25T21:35:41.557476Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 4067, for document-4067.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 3
      by: harness
      at: 2026-11-26T11:06:05.815236Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 4119, for document-4119.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 4
      by: harness
      at: 2026-11-26T15:21:04.932043Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 4137, for document-4137.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 5
      by: harness
      at: 2026-11-26T16:09:49.149605Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 4140, for document-4140.1 under the automatic approval policy
---
# Epic mode, second part

Epic mode (yoyodyne-9k1y) lets the operator name the epics the factory works on, and starts nothing outside them. The first part of the design covers naming an epic, the one decision asked before anything starts, and what status says is held out. It was not published when this was written; what it settled is built, and [the operations guide](../operations.md) describes it. This part covers the rest: what the harness remembers about an epic, how an epic ends, what may join it, how it is checked whole, what happens to review comments, what it costs, and when each role runs. It serves the autonomy goal, because the operator's one act of naming is enough to see an epic through, and the goal that spend follows the work.

The pieces are numbered from 7, after the six pieces of the first part, which are built. Nothing here is built when this is written.

## What it rests on

The record of the epics the operator named (`internal/epicscope`) and the operator's commands (`internal/cli/epic.go`) were read whole at commit 56a0190c2df1, and the one decision (`internal/maystart`) at commit 37ea054b6ee3. The code that splits an item, the repair from pull request feedback, the cost records and the code around recurring passes were not read. A builder who finds that code differing from this text stops and asks the architect.

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

An item belongs to at most one epic that the operator has not ended, and an epic that was delivered keeps its items until the operator ends it. `yoyo epic start` and `yoyo epic add` already refuse an item that is a member of such an epic. With this piece they refuse one that joined it too, and name that epic. A prerequisite two epics share goes in whichever of them the operator puts it in. Once piece 8 lands it can be moved with `yoyo epic remove` and then `yoyo epic add`.

The check for that and the write that follows are made under one lock. The lock is the one the operator's record has. An operator's act takes it as now, and reads the progress file while holding it. A join takes it too, so that no act of the operator comes between, then the progress lock, always in that order. A join takes the lock without being able to write the operator's record: the record's package offers a function that holds the lock and gives only a reading.

The commands therefore leave no item in two open epics. A file changed or restored by a person could. An item found in two does not start, and status names it and both epics. Ending one of the two epics settles it, or, once piece 8 lands, taking the item out of one.

The files are read again at a restart. Each start and each paid call is reserved before it is made, and a reservation with no result is looked up before anything is sent again. Where the result cannot be known, the epic says so and the development manager decides, as for any run whose result is unknown.

A project that names no epic has none of the three files, and nothing here runs.

## Where an epic stands: piece 8 (yoyodyne-9k1y.10)

The read model works out one state from the records each time. The first that fits applies.

1. **Cancelled:** the operator ended it before it was delivered.
2. **Delivered, integrated:** every member's change is on the target branch, and the check of the whole epic passed.
3. **Delivered, handed over:** the same, for a project where a person merges. Each change is approved and its pull request is open, but at least one is not merged. It becomes integrated when they all are. Where a person closes one unmerged, the epic is open again and Working: the item goes back to the Lead Product Manager, parked, as [the publishing guide](../configuration/publishing.md) says, and that role decides whether to drop it, revise it or admit it again.
4. **Working:** any member is running or ready, the check of the whole epic is owed, or a role owes any step: a decision about a stopped member, a cancelled or given up member, a closed pull request, a question, missing evidence, a third failed check, or a piece to be written or rewritten. A member that is ready only because the budget refuses its start does not count as ready here.
5. **Out of budget:** nothing under way remains, and the next start is refused by the epic's budget.
6. **Waiting on the operator:** everything left waits on an answer or a step only the operator can give, for a reason on the closed list.
7. **Blocked outside the factory:** everything left waits, and at least one wait is on something other than the operator: a provider, the forge, another person, or an item outside the members. Where some of what is left waits on the operator as well, this is still the state, because the operator's answer alone would not let the epic finish. Status names every wait, the operator's included, so the answer owed is not hidden.

While it is Working, status names each step owed and the role that owes it. Here and below, a member is an item in the epic, whether the operator named it or it joined.

Only the two delivered states and cancelled finish an epic. A member closed without a landed change counts as done only where its item is judged by its summary and the reviewer approved it. Nothing in the tracker marks such an item. It is read from the item's run: the run ended complete, the reviewer approved it as that item's work, and it recorded no change and no pull request. A member closed with no such run is not done on that ground. An item the operator took out is no longer a member and is not waited for. A member cancelled or given up by a role, or with an unanswered question, leaves the epic open and is named. It is never shown as delivered.

**A cancelled or given up member is the Lead Product Manager's to decide**, and so is one whose pull request a person closed. That role admits the item again, or has the development manager write a piece in its place, which joins by the joining rule as a split of that member. Such a member counts as done when that piece is. Where the epic should be delivered without the item, only the operator can take it out, because the operator named the epic and it holds everything else back until it ends. The operator is then asked, for the reason of lifting a hold the operator placed, with the command that does it.

### An epic the operator has ended

The state of an ended epic is worked out from what stood when the operator ended it. Nothing recorded later changes it.

A question to the operator is recorded without a time. The time is in the log of the conversation that put it, and the read model may look there while an ended epic with a delivered entry is shown, and at no other time. A question whose time cannot be found is left out, because nothing shows that it stood when the epic was ended. This applies to an ended epic only: an unanswered question counts in an open epic whether or not its time is known.

### Taking one item out

`yoyo epic remove <epic> <item> --words <text>` is the operator's act and is built in this piece. A new work item for it would be outside the named epic and could not start without the operator.

- **Who.** It is refused to a process the harness launched for a role or started itself, as the other acts are.
- **What it writes.** The epic's entry in the operator's record gains a list of removed items. Each has the item, when it was taken out, the words, and, where it was a member in that record, when and by which act it had been put in. A member in that record leaves its list of members in the same write. The words are required and bounded as the other words are, because the check of the whole epic is given them.
- **A piece that joined.** The command writes the removed list and not the progress file. A joined piece is in the epic unless it is in the removed list, and one that is there cannot join again. It still counts toward the twelve.
- **Putting one back.** `yoyo epic add` puts a removed item back as a member. The removed list keeps the earlier act as history, and the item is in the epic because it is a member.
- **What it refuses.** An epic never named or already ended, an item that is not in the epic, and empty words. Where the epic's progress cannot be read, a member in the operator's record can still be taken out, and any other item is refused with that reason.
- **What it changes.** The item may be taken by another epic. A run under way for it is treated as any run outside the named epics is: it waits with its work kept the next time it asks the one decision. What it already cost stays counted. Pieces that came from it stay, and the command prints each with the command that takes it out.
- **The state.** A check of the whole epic that did not pass before the removal is made again, and that one does not count as a round of correction. A delivered epic stays delivered.

A record without removals is written exactly as today. A build older than this piece refuses a record that has one, so it starts nothing, which is the safe side.

### The rest of piece 8

Until piece 10b (yoyodyne-9k1y.12.2) lands, an epic whose members are all done shows Working and says the check of the whole epic is not built yet. Once it lands, status names that check as owed or under way instead.

The delivered entry in the progress record is evidence, and the state is still worked out from all the records. An epic opens again when the operator adds a member after it was delivered, or when a person closes one of its pull requests unmerged. The old delivered entry is then kept as history and no longer counts.

A suggestion found during an epic is filed and admitted as today. It starts nothing, wakes nobody and is asked of nobody. Its record names the epic during which it was found, and `yoyo status` counts such items beside the work held out. A critical report still goes where it goes today. No second list is kept.

### What else a reviewer checks in piece 8

Beside the list for piece 8 at the end of this document: an epic with one wait on the operator and one outside is blocked outside the factory and both waits are named; a member closed with no landed change is done where its approved, complete run recorded no change and no pull request, and not otherwise; an ended epic keeps the state it had when it was ended, and a question whose time cannot be found does not change it.

## What may join an epic: piece 9 (yoyodyne-9k1y.11)

A new piece joins an open epic without asking the operator only when all of these hold.

1. It was made by one of three acts: the development manager splitting or replacing a member, a correction from the check of the whole epic, or a correction asked for in review of a member's change. The development manager writes the piece in all three. Evidence the check lacked, and feedback on a closed member, are written as corrections of the second and third kind.
2. Its record names the member or the check it came from.
3. A reviewer session that did not write the piece, and did not make the check or the review it came from, was given the copied description, the removed items with the operator's words, and the piece's text. It answered that the piece asks only for what the description already asks and not for what the operator took out.
4. Fewer than twelve pieces have joined that naming.

Twelve is a limit in code, and no command or setting raises it. The operator may still put any further item in with `yoyo epic add`. That is the operator's own act, so it does not count toward the twelve.

Anything else stays outside: an item attached under the epic, made a prerequisite of a member, moved under it, or made by any other act. Where the reviewer answers that a piece is outside, or cannot tell, or twelve have joined, it does not join, starts nothing, and is a question about scope.

The judgment is a paid call. It is reserved before it is made, and looked up before it is sent again. This piece builds the judgment, its limit, and the split that uses it. Pieces 10 and 11 use it for the other two acts.

### A piece edited after it joined

Built by piece 10 (yoyodyne-9k1y.12), so that the building of piece 9 is not disturbed. A piece that joined may start a developer only on text that a reviewer session has judged inside the epic. Without this, an edit made after the judgment could widen the work without the operator.

1. The text judged is the piece's title, description and acceptance criteria. Notes, priority and labels are not part of it. The judgment's entry in the progress record keeps a digest of that text.
2. The harness compares the digest before each developer session that is given the piece's text, the first and any later one. The code that starts the session compares it, from the same reading of the item that it gives to the developer. The one decision is not changed: it is given an item's identifier and reads no tracker.
3. Where the text differs from every text judged inside, no developer session starts. The new text is judged as rule 3 above says, and that call is reserved as any judgment is. Judged inside, its digest is added and the piece goes on. A run already under way waits with its work kept.
4. Where it is judged outside, or the reviewer cannot tell, the piece waits and is a question about scope. It is not judged again until the development manager has rewritten it after the Lead Product Manager's answer. An edit made in between starts no judgment.
5. One text is judged once for one naming. A piece put back to a text already judged inside goes on with no new call.
6. The piece keeps its one place among the twelve, however often it is judged.
7. An item the operator named or added is not compared, because the operator's own act put it in. A joined piece the operator then adds with `yoyo epic add` is such an item.
8. A piece that joined before this rule landed has no digest. Its text as it stands is judged once, before its next developer session.
9. Where the item cannot be read, no session starts, and the reading is tried again as any tracker reading is.

Status names a piece that waits for such a judgment, and one that was judged outside. The code that starts a developer session and the change that builds piece 9 were not read for this rule. A builder who finds either differing from it stops and asks the architect.

A reviewer can check: a piece edited after joining starts no developer until it is judged; judged inside it goes on, and judged outside it is a question about scope and a further edit makes no call; a piece put back to a judged text makes no call; a change to its notes or priority makes none; an edit during a run holds the next session and keeps the work; an item the operator added is never compared; a restart between the reservation and the answer makes no second call before looking the first one up.

## The check of the whole epic: piece 10 (yoyodyne-9k1y.12)

When every member is done, the harness starts one reviewer session that did none of the work. It is given the epic's description as it was copied when the operator named it; the items the operator took out, with the words; each member's landed change and review; the target branch at one recorded commit, or the approved heads where changes were handed over; and the results of the project's declared checks on that commit, as the first subsection below says.

It answers for each thing the description asks: met, with where it looked; taken out by the operator, naming the removal; not met, with what is missing; or cannot be told, with why. Taken out is an answer only for what a removed item would have given, and it does not keep the epic from being delivered.

- **All met or taken out:** the epic is delivered, and what was taken out is shown beside it.
- **Not met:** the development manager writes one correcting piece for what is missing, as the second subsection below says. It joins under the joining rule, judged by a different session. The check is made again when it lands.
- **Cannot be told:** where the description itself reserves the step for a person, the epic waits on the operator for that step. Otherwise the Lead Product Manager says what evidence is missing, and the development manager writes the piece that gives it.

After two rounds of correction, a third answer of not met goes to the Lead Product Manager, who decides on one more round or asks the operator about scope.

`yoyo epic start` refuses an epic whose description is empty, and says so. It refuses no other description, because no code can tell whether words ask for something that can be checked. A description that asks for nothing checkable shows itself as cannot be told, and the Lead Product Manager then says what evidence would settle it.

### Check results for the commit it reads

Ruled on November 26, 2026, for piece 10b (yoyodyne-9k1y.12.2).

The session is never started without results for the commit it reads. Before it starts, the harness looks for results of the project's configured checks, run whole, on that exact commit and recorded with the digest of the check set: from a merge queue candidate that landed as that commit, or from the scheduled check of the target branch on it. Where every configured check has such a result, those are given. Where any has none, the harness first runs the configured checks whole on that commit, in a checkout of its own, under the same limits on how many checks run at once and how long one may take as any other check. Where changes were handed over and the session reads approved heads, the same is done for each head.

- A check that fails is a result, given to the session as it is. It does not stop the check of the whole epic and starts no repair.
- A check ended by the machine running it is run once more. Ended again, or unable to start, the session is told that it could not run and why, and whatever the description asks that rests on it is answered cannot be told.
- Running them is part of the check of the whole epic. It is reserved in the epic's progress before it starts and looked up before it is started again after a restart, it is judged by the same rule as that session in [the first part](epic-mode-first-part.md), it is charged to the epic, and it is made even past the epic's budget.
- A project that declares no checks gives none, and the session is told so.

Rejected: answering without results, because most of what a description asks is shown only by running the code, so the check would answer cannot be told and the Lead Product Manager would be asked for evidence the harness could have made; giving the last results of each member's own change, because those ran on other commits and say nothing about the members together.

### How a correcting piece names the check it came from

Ruled on November 26, 2026, for piece 10c (yoyodyne-9k1y.12.3). The joining code reads the progress record, never the words of a note.

1. When a check answers not met, the harness writes in the epic's progress entry, beside that check, that one correction is owed for it, naming what is missing. When a check answers cannot be told and the description does not reserve the step for a person, the harness asks the Lead Product Manager for the missing evidence as point 2 says, and writes the owed correction only once her answer is recorded.
2. The question is a report addressed to the Lead Product Manager, filed by the harness, naming the epic, the check, its commit and what could not be told. Before it is filed, its identifier is written beside the check in the progress entry, as for a question about scope. Her answer is her recorded handling of that report, and nothing else counts, whatever a note or the item says. Where the handling records that only the operator can act, for a reason on the closed list, the epic waits on the operator for that step and no correction is owed. Otherwise the harness writes the owed correction beside the check, naming the evidence as her handling's reason states it and when the handling was recorded. Until the handling is recorded, or while it cannot be read, nothing is owed, and the epic is Working with that step named as hers. One check files one such report.
3. The development manager writes the piece with the action that creates an item under a parent, naming the epic as the parent and the check the piece corrects. The action gains one optional field for that. The harness refuses it, and says why, where no correction is owed for that check or one has already been written for it.
4. On creating the piece, the harness writes its identifier into the owed correction in the progress entry first, and then writes on the item a note of one fixed form naming the epic, the check and its commit, as it writes the split note. A stop between the two writes leaves the entry naming the piece and no note, and the note is written at the next reading.
5. The joining code treats a piece as a correction only where an owed correction in the progress entry names it. The note is for people to read. A note that no entry matches makes nothing join.
6. A correction asked for in review of a member's change, in piece 11, works the same way: the owed correction names the member and the forge's identifier of the comment, and the action's field names the comment.

The action that creates items and the code that writes the split note were not read for this ruling. A builder who finds either differing from it stops and asks the architect.

A reviewer can also check: a check that cannot be told, with no step reserved for a person, files one report to the Lead Product Manager and owes nothing until its handling is recorded; a handling that names the operator's reason makes the epic wait on the operator and owes nothing; any other handling owes one correction naming its reason, and the development manager's piece for it joins only after its judgment.

Rejected: reading the check from the item's words, because anyone who can write an item could then make it join; the harness writing the piece itself, because the development manager writes every piece, as the joining rule says; a command for the Lead Product Manager to name the evidence, because her handling of a report already records a decision and its time.

### What records the answer on scope

Ruled on November 26, 2026, for piece 10d (yoyodyne-9k1y.12.4), and used by piece 11.

1. A question about scope is put to the Lead Product Manager as a report addressed to that role, filed by the harness, naming the epic, the piece and the digest of the text judged. The report's identifier is written into the piece's judgment entry in the progress record.
2. The answer is that role's recorded handling of the report: what was decided, and when it was recorded. Nothing else counts as the answer, whatever a note or the item says.
3. A text that differs from every text judged inside makes a new judgment only once that handling is recorded, and only for an edit seen after the handling's time. An edit seen before it makes no call, and the piece waits. Where the answer is that the epic can be delivered without the piece, the piece waits, and status says that only the operator can take it out, as piece 8 says.
4. Each new judgment of outside, or cannot tell, files a new question, so each answer is to one judged text.

Rejected: a separate command or record for the answer, because the handling of a report already records the decision and its time; reading the answer from a note, because any role can write a note.

### What a reviewer checks in piece 10

Beside the list for piece 10 at the end of this document: where a result for every configured check on the commit exists, no check is run; where one is missing, the checks run on that commit before the session starts, are charged to the epic, and run past its budget; a failing check is given to the session and starts no repair; a check that cannot run is named to the session; a restart between reserving the checks and their result starts them once. A piece created naming a check for which a correction is owed joins only after its judgment, and its link is in the progress entry; the same action naming a check with no owed correction, or a second piece for one, is refused; a note of the fixed form with no matching entry makes nothing join. An edited piece judged outside makes no call before the handling of its question is recorded, and makes one for the first edit after it.

## Review comments and questions about scope: piece 11 (yoyodyne-9k1y.13)

Piece 11 waits for piece 10 as well as piece 9, because adding to a delivered epic needs a delivered epic.

- A change request on a member's open pull request is judged before its repair starts. The harness records the forge's identifier of the comment first, then reserves the judgment. The reviewer session of the joining rule is given the copied description, the member and the comment. Judged to ask only for what they already ask, it goes through the repair from pull request feedback that exists, on the same pull request, once. Otherwise no repair starts and it is a question about scope. Whose comments are accepted is as the existing repair decides.
- Feedback after a member closed, while the epic is open, becomes a new visible piece naming the comment and the member, written by the development manager. It joins only by the joining rule.
- Feedback after the epic was delivered becomes a visible item that starts nothing. It is put to the Lead Product Manager, who asks the operator whether to add it. Adding it with `yoyo epic add` opens the epic again. While any epic is open, such feedback starts a pass of that role. With none open it waits: status shows it beside the delivered epic, and it is put in front of that role's next turn of any kind, including one the operator's own message starts.
- A request for something new is a question about scope, however it was worded. The harness decides nothing by reading words.

The Lead Product Manager answers a question about scope first. The answer may be that the epic can be delivered without the piece, or that the piece should be rewritten to fit. The development manager then rewrites it and it is judged again. Adding what the description does not cover is the operator's. Naming an epic is a hold the operator placed on everything else, and adding an item lifts it for that item, so the reason is lifting a hold the operator placed, and the closed list does not change. The operator is asked only where the epic cannot be delivered without the piece, or where a person asked for it in review. One question is asked for one piece, with the command that adds it, and the operator may ask about it before answering. The other members carry on meanwhile. Anything else is a suggestion.

## What an epic costs: piece 12 (yoyodyne-9k1y.14)

An epic's cost is the sum of what is attributed to it: every provider call of its members' runs and reviews, its checks and joining judgments, and each role pass it woke. A pass woken for two epics is shared equally between them. Calls whose cost is unknown are counted and shown as unknown, never as nothing. Nothing is counted twice.

`yoyo epic budget <epic> <amount>` sets a limit, and there is none unless it is set. It is kept in the record only the operator's commands write. It is measured in provider reported cost, which status names. Calls with unknown cost are shown beside it and do not count toward it. The limit stops new starts only. Work already started finishes its repairs, its review and its landing, and a check of the whole epic that is owed is made. The epic may therefore end above its limit, and status shows by how much. At four fifths of the limit status says so. Raising it is the operator's command.

Role passes are counted once piece 13 lands, and checks and joining judgments once pieces 10 and 9 land. The tests for those costs belong to those pieces.

## When each role runs: piece 13 (yoyodyne-9k1y.15)

Today, while epic mode is on, the one decision lets a pass of the Lead Product Manager, the development manager or the architect start whenever any epic is open, so the clock still starts them. It refuses those passes when no epic is open, and refuses a program manager's pass always. This narrows the first case only, and that rule applies as it is until this piece lands. With this piece, a role's pass is not started by the clock. It is started by an event of an open epic that the role owes something for.

- **Lead Product Manager:** a question about scope; a check of the whole epic that could not tell, where the evidence is that role's to name; a third failed check; a cancelled or given up member; a member's pull request closed unmerged by a person; feedback on a delivered epic, while any epic is open; a member with no description to build from.
- **Development manager:** a stopped member; a member too large for one change; a piece to write, whether a correction, the missing evidence, a replacement, or the piece for feedback on a closed member; a piece to rewrite after the Lead Product Manager's answer on scope; a paid call or a start whose result cannot be known; a follow-up of a member that fell due; an automatic hold on new work, whatever caused it.
- **Architect:** a member held for design or carried in the architect's conversation; a change proposed to a design a member needs; a document that answers a member and that a run handed back.
- **Each of those three:** a report or a question waiting on that role that names a member, an operator's answer to something that role asked about the epic, and a member's ordinary comment where an existing rule gives it to that role.
- **Reviewer:** a candidate, a check of the whole epic, a joining judgment, or the judgment of a review comment, as each comes.
- **Program managers:** none.

Every step these pieces give a role is in this list. A builder who finds one that is not stops and asks the architect, because nothing else would start that step.

While any epic is open, a critical report that waits on one of the first three roles starts a pass of that role even when it names no member, because waiting could cost more than the pass. One that waits on a program manager starts a pass of the Lead Product Manager instead, who may handle any report. A report that names a member and waits on a program manager goes the same way, whatever its severity. Any other report waiting on a program manager stays on that list.

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
- **Ending and naming the epic again as the only way to take an item out.** The new naming lists only unfinished items, so the check of the whole would lose the finished members, the joined pieces and the rounds already made.
- **Taking an item out by writing the progress file.** The operator's word would then be in a file the harness writes.
- **Raising the limit of twelve by command.** Adding an item is already the operator's act, and a second command would authorize pieces nobody has seen.
- **The checking reviewer writing the correction.** The session that found a thing missing would then be setting the work that it judges.
- **A cancelled member counted as done by a role's word.** The operator named it, and an epic would be shown as delivered without something the operator asked for.
- **Asking the operator about every closed pull request or cancelled member.** Admitting the work again or replacing it is a role's decision.
- **Feedback on a delivered epic starting a pass while no epic is open.** With nothing named the operator has asked for nothing to run, and the feedback loses nothing by waiting where it is seen.
- **A cost limit that stops work under way.** It would leave paid work unfinished.

## How a reviewer checks it

- Piece 7: a test for each of the six rules on what shows an entry existed, including a stop between the two writes; status says progress is missing after both files are removed; a listing of every writer of each file; a test that a naming ended and made again has a new empty entry and the old one unchanged; a test that a join and an operator's add of the same item at once put it in one epic only; an item written into two open epics by hand does not start and both are named.
- Piece 8: records for every state, in the order in which they apply; one cancelled member or one open question is never shown as delivered; an epic with only a cancelled member left, or only a pull request closed unmerged, is Working and names the Lead Product Manager; adding a member after delivery opens the epic again.
- Piece 8, taking one item out: refused to a role's process, without words, and for an item not in the epic; a removed member and a removed joined piece no longer start, with the progress file unchanged; a removed item cannot join again and can be added; it can then be added to another epic; an epic whose only unfinished member was cancelled owes its check once that member is taken out; a record without removals is written byte for byte as before.
- Piece 9: a thirteenth piece does not join, and a removed piece still counts; an item the operator adds is not counted; the judging session wrote nothing it judges and gets the copied description and the removed items.
- Piece 10: each answer and what follows; only met and taken out together deliver; taken out with no removal to name is refused; an empty description is refused and no other is.
- Piece 11: the same comment read twice starts one judgment and at most one repair; one judged outside starts none; a piece to be rewritten is judged again; feedback on a delivered epic with none open starts nothing and is in status.
- Piece 12: past its limit an epic starts no member, and a run under way still lands; unknown cost is never nothing; a removed item's earlier cost stays.
- Piece 13: no pass starts by the clock; one test for each listed event shows the pass it starts; a critical report that names no member starts one pass; one that waits on a program manager starts the Lead Product Manager's and no program manager's; a day with every epic waiting and no such report starts none.
- Piece 14: the pass is sent what the list says and nothing else.
- Every piece: with no epic named, every existing test passes unchanged.
