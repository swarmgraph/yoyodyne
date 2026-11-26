---
id: integration-through-a-merge-queue
kind: design
title: Integration through a merge queue
supports:
    - v1-goals
    - milestone-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-05T00:33:49.641499Z
      reason: 'yoyodyne-ifd.429.22: record portable ordered integration under one switch, exact candidate checks and independent review, separate worker and promotion leases, confirmed withdrawal and restart-safe reconciliation.'
    - action: amended
      by: architect
      at: 2026-10-09T13:55:08.84697Z
      reason: 'yoyodyne-jj8: with the merge queue switched on, the target branch must not require GitHub''s own merge queue; the harness refuses queue mode, holds waiting changes naming the setting, never falls back to replay, reports once and changes no settings. Also states how merging stays safe with the switch off. Sent again whole after the earlier run (document-633.1) was stopped during make race without a reviewer judging it.'
    - action: amended
      by: architect
      at: 2026-11-07T20:28:01.619458Z
      reason: 'Approved amendment-59a0a032 from the developer: the queue checks a confirmed document''s candidate with the declared document checks, as the document landing route''s rule 3 says, and every other candidate with the configured checks run whole; chosen by what the candidate changes, with the check set''s digest in the evidence. Read whole at a98c5e10 before revising.'
    - action: amended
      by: architect
      at: 2026-11-12T02:57:42.623649Z
      reason: 'yoyodyne-ifd.429.154: adds the order the queue takes waiting entries in, starting from the operator''s direction that priority 0 code goes ahead of documents; and, for yoyodyne-ifd.429.160, when GitHub''s required checks on the exact candidate commit stand as the queue''s full check. Carries approved amendments 29000396 (checks and review side by side, spend charged to the entry), 57e91f68 (a check ended by its runner is run once more), 5f963776 (passed checks kept across a restart only with commit and check set recorded) and 9414b909 (checking one entry ahead), and removes the word ''gate'' throughout. Read whole at 69141ebf before revising.'
    - action: amended
      by: architect
      at: 2026-11-15T03:03:26.58645Z
      reason: 'Adds the placed-first rule ruled on November 13 for the queue-order item (yoyodyne-ifd.429.154), which the revision that landed as dd131700 did not carry: within the priority 0 step and within each priority of other code, an item the Lead Product Manager placed first goes ahead, earliest placement first, read at the moment of choosing; there is no command to move an entry. Answers the Lead Product Manager''s November 12 note. Section read whole at f5c47b24.'
    - action: amended
      by: architect
      at: 2026-11-16T20:41:37.543491Z
      reason: 'yoyodyne-ifd.429.199: records the architect''s pick among the three ways out of the slow merge queue the operator was given on November 16, 2026 (keep the harness''s queue and let GitHub''s required checks on the exact candidate stand as its full check), why the other two were rejected, and the trial that switches it on for this repository: what it measures, how long it runs, when it switches back, what happens to the entries already waiting, and the settings in order. The section''s existing text is unchanged. Read whole at 101b2da5 before revising.'
    - action: amended
      by: architect
      at: 2026-11-18T15:56:15.443629Z
      reason: 'yoyodyne-ifd.429.224: a candidate''s pull request opens as a draft and is marked ready by the harness only once the reviewer has approved and every required check has passed on that exact commit, in the same step as the merge; the step is retried through GitHub outages like the merge and never hands an entry back; it returns to draft before a new commit is pushed or on withdrawal; a person''s merge of a ready candidate is followed like the harness''s own, and a merge the harness did not approve files a critical report. A label and a protection rule were rejected because each needs a setting only a person can change. Names one build piece. The rest of the section is copied unchanged from main 1d45a162, read whole before revising.'
    - action: amended
      by: architect
      at: 2026-11-19T04:55:04.87004Z
      reason: 'yoyodyne-ifd.429.230: the trial starts only when the change turning the setting on lands, and that change waits for every listed piece including confirming the merge commit (yoyodyne-ifd.429.187) and for that confirmation run once over the landings through GitHub since build piece 8 of checks on CI, because unconfirmed landings would otherwise count as successes. Records step 2: the full local check compared with .github/workflows/ci.yml at main e4c9aa75; macOS tests stay in the quick checks, tests needing bd and possibly the Node render test are named gaps, with one build piece. Section read whole at e4c9aa75 before revising.'
    - action: amended
      by: architect
      at: 2026-11-19T21:13:18.114339Z
      reason: 'yoyodyne-ifd.429.241: adds a subsection ruling how a candidate already sent to GitHub is kept from landing unchecked when main moves by another merge: the merge is asked for directly, never by merging when ready, under the promotion lease and only after reading main at the recorded base; every pushed, unlanded candidate whose base is no longer main''s tip returns to draft and is rebuilt with no repair charged; GitHub''s refusal for a branch not up to date counts as a moved main; the up-to-date requirement on main is a setting only a person can change, reported by yoyo doctor whatever the switch says, and the queue does not stop without it. Names two build pieces. The rest of the section is copied unchanged from main 0b641fc8a, read whole before revising.'
    - action: amended
      by: architect
      at: 2026-11-20T21:58:49.478348Z
      reason: 'yoyodyne-ifd.429.255: resent whole after the reviewer returned run-84eb0419 for a contradiction. Order: waiting documents land together (at most eight, within the review size limit, no shared files, split on failure), a document candidate never lands twice in a row while ordinary code waits, no document beside priority 0 code, age rule four hours. The verification list now points to the order section''s reviewer list, the placement checks are restored, the trial wording and build pieces follow, and the drafts subsection records the CI workflow already read. Read whole at 596bbfe1409e.'
    - action: amended
      by: architect
      at: 2026-11-21T08:32:16.166197Z
      reason: 'Approved amendment-1bd7f85d from the developer on the item fixing queue order when the tracker cannot be read: a tracker that cannot be read orders entries by the priority and placement last read for each, and only entries with none remembered go last in admission order; each choice records whether remembered values were used. Rule 5, the tracker line, the record line and the reviewer list change; the rest of the section is copied unchanged from main 0600a4af, read before revising.'
    - action: amended
      by: architect
      at: 2026-11-21T16:35:48.658821Z
      reason: The CI job that installs the tracker program is now named getting-started, not adoption (ci.yml and the Makefile read at cdcd0559a79a); the adoption make target stays only as an alias. The trial section's three mentions of the adoption job and the list of targets outside the full local check are corrected, and the job is described as running the conformance tests and the README walk. Nothing about the trial changes. The rest of the section is copied unchanged from main cdcd0559a79a, read whole.
    - action: amended
      by: architect
      at: 2026-11-26T00:36:38.46153Z
      reason: 'yoyodyne-32of.2: states how a confined conflict resolution is admitted on the approval of the head it resolves, and how the queue''s record is kept from filling: its own 16 MiB limit, and ended entries moved to history files with the order, superseding and crash guarantees kept. The order section follows once the corrected conflict design lands. Section read whole at 0f35eeff1e4a with internal/runstate/mergequeue.go.'
    - action: amended
      by: architect
      at: 2026-11-26T10:48:36.851338Z
      reason: 'yoyodyne-32of.3: resends the next turn rule after run c376f122 was returned. A code entry brought up to date after a conflict at landing takes the next candidate alone. A document entry never does, because a conflicting document goes back to its writer as the design for approved changes that no longer merge says on main (5abead9023). Adds the record line and reviewer checks. The rest of the subsection is copied unchanged from main deaa4fd73275, read whole.'
approvals:
    - revision: 0
      by: operator
      at: 2026-10-05T00:33:49.641499Z
      reason: approved by the operator in conversation chat-11558d325e9a214ebfd00bb4a0012750, turn 147, for the document the architect wrote there (document-147.1)
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-09T13:55:08.84697Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 645, for document-645.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-11-07T20:28:01.619458Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2450, for document-2450.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 3
      by: harness
      at: 2026-11-12T02:57:42.623649Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2689, for document-2689.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 4
      by: harness
      at: 2026-11-15T03:03:26.58645Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2805, for document-2805.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 5
      by: harness
      at: 2026-11-16T20:41:37.543491Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2909, for document-2909.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 6
      by: harness
      at: 2026-11-18T15:56:15.443629Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3018, for document-3018.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 7
      by: harness
      at: 2026-11-19T04:55:04.87004Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3036, for document-3036.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 8
      by: harness
      at: 2026-11-19T21:13:18.114339Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3136, for document-3136.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 9
      by: harness
      at: 2026-11-20T21:58:49.478348Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3240, for document-3240.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 10
      by: harness
      at: 2026-11-21T08:32:16.166197Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3308, for document-3308.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 11
      by: harness
      at: 2026-11-21T16:35:48.658821Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3331, for document-3331.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 12
      by: harness
      at: 2026-11-26T00:36:38.46153Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 4079, for document-4079.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 13
      by: harness
      at: 2026-11-26T10:48:36.851338Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 4117, for document-4117.1 under the automatic approval policy
---
# Purpose

This design serves the goal to isolate implementation in harness-managed worktrees and integrate successful work automatically, and the milestone goal that measures useful software per hour of the owner's attention ([milestone goals](../product/milestone-goals.md)): a queue that holds urgent code behind documents, or checks one change twice, spends developer time and his. It records integration through a merge queue (yoyodyne-ifd.429.22), the order the queue takes waiting entries in (yoyodyne-ifd.429.154, revised for yoyodyne-ifd.429.255), and when GitHub's required checks on a candidate stand as the queue's full check (yoyodyne-ifd.429.160). The queue prevents the target moving between a change's final checks and its landing from invalidating that work.

# Decision

Use one configuration switch, execution.merge_queue, default false. False preserves existing integration, including pre-publication replay and its checks and independent review. True admits approved changes to a durable queue and omits the harness's pre-publication replay. Existing integration and publishing approval policies still apply; enabling the queue grants no authority.

Provide a harness-run queue on every supported forge. Use a forge-native queue only when its adapter can establish and enforce the same checks and review bound to the candidate, described below. Queue availability alone is insufficient. Reject permanent dependence on GitHub's queue, checks bound only to a pull-request head, and a second switch controlling replay: each violates either portability, verification or the operator's one-switch decision.

# Durable admission and ownership

One queue exists per repository and target branch. Admission records an immutable entry identity and monotonically assigned order under a short lock on the queue's record. Deduplicate repeated admission of the same run and head. Record product, repository, work item title and identifier, run, publication, approved head, integration policy, chosen queue mode, admission time and predecessor order. Priority is not recorded at admission: it is read from the tracker each time the queue chooses, as the next section says.

Only one worker holds the lease that makes it the worker for that repository and target. This lease owns selection and candidate generation; it is separate from the existing target promotion lease. Checks and review run outside the promotion lease. The promotion lease covers only fresh target validation, the guarded mutation and its durable handoff. Never retain it through provider work or a long remote wait.

Queue waiting alone does not occupy a developer slot. Any developer or reviewer invocation uses its existing capacity and spending rules. That only one worker runs must not be implemented by permanently occupying a developer slot.

## A resolved change whose approval was of an earlier head

A change is admitted on the approval of its exact head, with one exception, which decision 4 of [approved changes that no longer merge](approved-changes-that-no-longer-merge.md) makes: a resolution of a conflict that the harness found confined to the conflicting files is admitted on the approval of the head it resolves. These rules hold for it:

- the entry records two heads: as its approved head, the head the reviewer approved, with that review; and as its resolved head, the commit the developer's session produced; and it records the comparison that found the resolution confined;
- the exception is for the run that earned the approval and for no other run;
- the candidate is built from the resolved head, and wherever this document speaks of an entry's admitted head, the resolved head is meant where there is one;
- two admissions of the same run are the same admission only where both heads agree;
- nothing shows the resolved head as approved until the candidate's own review approves it, and the landing needs that review and the candidate's checks as every landing does;
- a resolution the harness did not find confined, or whose comparison could not be made, is admitted only once it has been reviewed itself.

The two new fields are left out where an entry has no resolved head, so entries already recorded are read as they are. A build that does not know the fields refuses to write the record rather than writing it without them.

## The size of the queue's record

On November 25, 2026 the record for `main`, which keeps every entry ever admitted, reached 1,048,576 bytes, the limit it shared with other records. From then on no approved change could be admitted, code or document. Two rules keep that from happening again:

- **A limit of its own.** The queue's record has a limit of 16 MiB, used wherever that record is written or read, and no other record's limit changes. An admission that would pass it is refused with the size and the limit named, and the record stays as it was. The item giving the record this limit (yoyodyne-32of.5) builds it.
- **Ended entries move to a history.** Once the record passes 1 MiB, the next admission first moves ended entries, earliest first, until the record is under 512 KiB or no ended entry is left. An entry has ended when its landing is recorded complete, when a later entry of the same run supersedes it, or when it was handed back or released and its run has ended. A waiting entry, and one being checked, reviewed or landed, is never moved. Moved entries go, unchanged, into history files beside the record, one for each calendar month in which entries are moved. A history file that would pass 16 MiB is continued in a second file for that month.

The history keeps the guarantees the record gives:

- the move is made under the record's lock; the history file is written and confirmed saved first, and the record after it, so a crash between the two leaves an entry in both and never in neither;
- an entry in both places with the same contents is one entry, and the next move completes it; with different contents it is an error that names the entry, and nothing is admitted or moved until it is settled;
- the record keeps the next order to assign, so an order in the history is never assigned again;
- the order, predecessor, place and superseding checks are made over the record and its history together when an admission moves entries, and over the record alone otherwise, where a predecessor, or an entry superseded, that the record does not hold must be earlier than the entry that names it;
- an admission whose run has no entry in the record looks for the run's newest entry in the history before deciding it is the run's first, so a run continued long after its entry ended still names the entry it supersedes;
- every reader that reports past entries, the daily wait figures among them, reads the history as well, and an unreadable history file is reported as unread and never as empty. An unreadable history file stops only the admissions that must read it: one whose run has no entry in the record, and one that would move entries. The refusal names the file and spends no repair attempt.

The records kept beside each entry, of its candidates and its landing, stay where they are.

yoyo doctor and the factory health check report the record once it passes 4 MiB, naming its size and how many of its entries have ended. Passing 4 MiB means the move is not keeping up, and it leaves time to act before admission stops.

Alternatives rejected: only raising the limit, because the record is written whole at each admission and would fill again; deleting ended entries, because they are the history of every attempt to land a change; moving entries by age alone, because an entry can wait for days and must stay; one history file without limit, because it would meet the same fault later; moving only an unbroken run of the oldest entries, because one old entry whose run never ended would keep everything after it.

Build work: two pieces, each built and checked on its own.

1. The limit of 16 MiB for this record (yoyodyne-32of.5). It lands outside the queue, because the full record refuses it too.
2. The history files, the move, the readers that read them, and the report at 4 MiB. It is queued like any other change.

Admitting a confined resolution is built with build piece 4 of approved changes that no longer merge.

A reviewer can check:

- a record of more than 1 MiB whose size is under 16 MiB is admitted to and read back, and one that would pass 16 MiB is refused with its size and limit named and the earlier record unchanged;
- an admission with the record past 1 MiB moves only ended entries, unchanged, and leaves every waiting entry and the next order as they were;
- a crash after the history is saved and before the record is loses no entry and admits nothing twice;
- a run whose ended entry is in the history, admitted again, names that entry as the one it supersedes;
- an unreadable history file refuses only the admissions that must read it, naming the file, and the daily figures say it was unread;
- a confined resolution is admitted with both heads and the comparison recorded, its candidate is built from the resolved head, and no surface shows that head as approved before the candidate's review;
- a resolution not found confined is refused admission until it is reviewed.

# The order the queue takes entries in

The operator directed that priority 0 code goes ahead of documents. On November 20, 2026 ordinary code waited a median of 7.9 hours in the queue, and four of the five entries handed back that day were conflicts found only after that wait (yoyodyne-ifd.429.255). The rules were being applied as written. The cause was that every document took a whole landing of its own and every document went ahead of ordinary code: that day documents and priority 0 code together arrived about as fast as the queue lands one candidate, about one every 22 minutes, so ordinary code moved only when the age rule took it. The order below lands waiting documents together and makes them take turns with ordinary code.

The queue chooses its next candidate each time it builds one, never at admission, so a priority changed after admission counts. Each time it reads an entry's priority and placement it keeps them, with the time it read them, for use only when the tracker cannot be read, as the further rules below say. It chooses by these rules, the first that applies deciding:

1. An entry waiting four hours or more since admission goes first, oldest first, whatever its priority, so no entry waits without limit. A document entry chosen this way goes as the first entry of a document batch, as the next subsection says.
2. Then priority 0 code entries.
3. Then the waiting document entries, landed together as one candidate, as the next subsection says. When the last candidate to land was a document candidate and an entry under rule 4 or rule 5 is waiting, this rule is passed over for that one choice, so a document candidate never lands twice in a row while ordinary code waits.
4. Then the other code entries, by priority.
5. Last, code entries whose work item's priority cannot be read now and was never read before, in admission order.

Within rule 2, and within each priority under rule 4, an entry whose work item the Lead Product Manager placed first goes ahead of the others, earliest placement first, as [placing an item first](placing-an-item-first.md) records that placement; the rest follow in admission order. The placement is read from the tracker at the moment of choosing, like the priority, so placing an item first after its entry was admitted counts at the next choice. Placement never lifts an entry past rule 1 or into a higher rule, and never stops a candidate already being checked.

An entry is a document entry when every path its candidate changes against its recorded base lies under `docs/product`, `docs/designs` or `docs/decisions` or their directory indexes: the same test that chooses its checks below. Every other entry is a code entry. The recorded kind of an entry never decides which it is, and no document entry has a place beside priority 0 code, whoever wrote it. Because this test reads only paths, it holds whether or not the tracker can be read.

## Waiting documents land together

A document candidate carries the waiting document entries together, not one at a time:

- it takes at most eight entries: an entry rule 1 chose first, then entries whose items were placed first, earliest placement first, then the rest in admission order;
- it stops before the entry whose addition would take the candidate past the reviewer's size limit on a change, as [review size limits](../decisions/review-size-limits.md) records that limit;
- no two entries in one candidate change the same file; the later one waits for the next document candidate;
- the entries are applied in that order on the target as it stands; an entry that conflicts with the target is handed back exactly as it would be alone;
- the candidate changes only document paths, so it runs the declared document checks once, as the section on the harness-run queue below says, and one independent review judges the candidate, each entry against its own work item;
- if a check fails or the reviewer refuses, each entry is then tried as a candidate of its own, in the same order, and only an entry that fails alone is handed back; the others are charged no repair;
- one landing records every entry in it, each entry's run and item are confirmed landed by that merge, and the cost of the checks and the review is charged to the entries in equal shares;
- where landing goes through a pull request, the candidate's pull request names each entry and its work item.

This is separate from the batched pull request route for a team branch in [integrating to a local branch](integrating-to-a-local-branch.md). That route lands through people's review, on another branch, at people's pace; tying the two together would make this queue wait on people.

## What the order costs

With code arriving at no more than about two entries an hour, ordinary code waits a median of about one to two hours, and at worst about four and a half: the four hour age rule plus the candidate already being checked. Above that rate no order helps, because the age rule turns the queue into oldest first; the answer then is the queue's pace, as [merge queue pace](merge-queue-pace.md) describes, not its order.

## Further rules

Five further rules hold whatever the order says:

- nothing already being checked or reviewed is stopped for a later entry that ranks higher; the order chooses only the next candidate;
- an entry whose work item depends on another admitted entry's work item waits until that one lands, wherever it ranks;
- checking one entry ahead, below, takes the next candidate in this same order;
- when the tracker cannot be read at the moment of choosing, that choice uses the priority and placement last read for each entry, under the same rules as if they had just been read; an entry with none remembered goes under rule 5, in admission order. The record says the tracker could not be read, and names the entries ordered by remembered values and when each value was read. The dependency rule above uses the dependencies last read in the same way;
- a code entry that was handed back because it conflicted with the target at landing, and has since been brought up to date and admitted again, takes the next candidate alone, ahead of every numbered rule above, age rule included. Where two such entries wait, the one first admitted earlier goes first. The three rules before this one still hold over it: it stops no candidate already being checked, it waits for an admitted entry its item depends on, and it goes in no document candidate. This rule is for code entries only. A document entry whose publication conflicts goes back to the role that wrote it, as [approved changes that no longer merge](approved-changes-that-no-longer-merge.md) says, and takes no next turn: when that role writes the document again, the new entry is chosen under the numbered rules like any other document entry. How long the repair of a conflicting code entry waits before it is brought up to date is that design's to say, not this one's.

Each choice is recorded: the candidate chosen and every entry in it, the rule that chose it and why, whether a placement decided it, whether remembered priorities or placements were used and for which entries, whether rule 3 was passed over because the last landing was a document candidate, whether the entry was chosen as one brought up to date after a conflict, and the entries it passed over. yoyo status and the dashboard show the waiting entries in this order, with the rule that put the first one first. Each day the queue's own records give the median and longest wait from admission to landing for each kind of entry: priority 0 code, other code, and documents.

A reviewer can check:

- a priority 0 code entry admitted after waiting document entries is chosen before them, and no document entry is chosen with the priority 0 code;
- an entry waiting four hours or more is chosen before any priority 0 code;
- waiting document entries land as one candidate of at most eight, within the review size limit, with no two changing the same file;
- a document candidate that fails a check or review is split, each entry is tried alone, and only an entry that fails alone is handed back, with no repair charged to the rest;
- after a document candidate lands, the next choice takes ordinary code where any waits, unless rule 1 or rule 2 applies;
- a priority raised after admission changes the next choice;
- within priority 0 code, an entry whose item was placed first is chosen ahead of an older unplaced one; two placed entries go in the order they were placed; a placement made after admission changes the next choice; and a placed entry never goes ahead of an entry waiting four hours or more;
- a candidate already being checked is not stopped when a higher entry arrives;
- an entry whose item depends on another admitted entry's item waits for it;
- with the tracker unreadable, an entry last read as priority 0 is chosen ahead of an older entry last read as priority 1, and an entry with no priority ever read goes last among code, in admission order;
- the record of a choice made with the tracker unreadable says so and names the entries ordered by remembered values;
- whether an entry is a document or code is decided by its paths, the same with the tracker readable or not;
- a code entry brought up to date after a conflict at landing is chosen next, alone, ahead of an entry waiting four hours or more and of priority 0 code, and the record says it was chosen for that reason;
- of two such code entries, the one first admitted earlier is chosen first;
- such an entry still waits for a candidate already being checked and for an admitted entry its item depends on;
- a document entry whose publication conflicted is never chosen by that rule, and a later entry for the same document is chosen under the numbered rules;
- every choice is recorded with its rule, every entry in it and the entries passed over, and status and the dashboard show the same order;
- the daily wait figures for each kind of entry are read from the queue's records, not estimated.

Build work: three pieces, each built and checked on its own, listed as pieces 8, 9 and 10 under the pieces of work below. Keeping the last read priority, placement and dependencies for each entry and using them when the tracker cannot be read is built by the item fixing that ordering (yoyodyne-ifd.429.252). The next turn for a code entry brought up to date after a conflict is built by the item for it (yoyodyne-32of.3).

Alternatives rejected: fixing the order at admission, because a priority raised while an entry waits would be ignored; strict priority with no age rule, because a document or low priority change could wait without limit; only lowering the age rule, because it adds no landings and so moves the wait from one entry to another; making documents take turns with code without landing them together, because documents are what the development manager's decompositions wait on, so making each wait longer costs as much as it saves; keeping a place for the architect's oldest document beside priority 0 code, because documents now land at least every other turn; landing code entries together as well, for now, because each code candidate runs the full checks and a failed combined candidate would cost one full check per entry when split, which is worth weighing only once the queue's pace work has measured how often candidates fail; a separate queue for documents, because two queues landing on one target would need two promotions at once, which [one promotion per target branch](../decisions/invariants/one-promotion-per-target-branch.md) forbids; a command that moves a queue entry forward, because the order the work is pulled in is already the Lead Product Manager's to set by placing an item first, and a second way to move an entry would let the queue order and the backlog order disagree; falling back to admission order whenever the tracker cannot be read, because that let a priority 1 entry pass two priority 0 fixes (yoyodyne-ifd.429.252), against the operator's direction that urgent code goes first; stopping the queue until the tracker can be read, because tracker reads are often slow and the queue would stand still each time; giving a document entry brought up to date the next turn as well, because no developer rewrites a document, so a conflicting document goes back to its writer and a new entry for it is ordinary document work; letting a code entry brought up to date wait its turn under the numbered rules, because main would move again under it and the same conflict would come back.

# Harness-run queue

Process entries in the order the previous section gives. Construct a candidate in a harness-managed checkout from the current authoritative target and the admitted head. Keep the source branch and original approval evidence intact. Record a generation containing the exact target base, contributing heads in order, candidate commit, content fingerprint and check configuration.

Run deterministic checks and obtain an independent reviewer verdict on that exact candidate. Which checks run follows what the candidate changes:

- a confirmed document's entry, whose candidate changes nothing against its recorded base outside `docs/product`, `docs/designs` or `docs/decisions` and their directory indexes, runs the project's declared `document_checks`, as rule 3 of [the document landing route](document-landing-route.md) says, and so never the race check; a project that declares no `document_checks` runs its configured checks whole;
- every other entry, including a run that brings in [pinned outside files](pinned-outside-files.md), runs the configured checks whole, unless GitHub's required checks stand in for them as the section on them below says.

Where [the merge queue pace design](merge-queue-pace.md) declares which checks a candidate runs, that declared list is what the configured checks mean here. An entry recorded as a document whose candidate changes any other path is checked like every other entry. The evidence is bound to the exact candidate and to the digest of the check set that ran, so a pass of the document checks never counts for a candidate that changes code.

The checks and the review run side by side on the candidate, not one after the other. A failing check stops the review. What each costs is charged to the entry's own run and work item, never to the queue.

A check that the machine running it ended, rather than one that failed on its own, is run once more on the same candidate. If it is ended a second time, the entry is withdrawn from the queue with a report naming that check, and no repair attempt is spent.

After a restart, checks that passed on a generation are kept for that same generation only where the record holds both the candidate commit and the check set's digest. Where either is missing, every check runs again.

Original head approval authorizes admission; it does not authorize a different combined revision. Missing or incomplete checks and verdicts receive no credit.

Alternatives rejected: running the race check on every document candidate, because the landing route ruled it out and it would put document landings back behind code work; letting the entry's recorded kind choose the checks, because what a candidate changes is the fact and the kind is only a label.

Before promotion, acquire the target promotion lease and reread the target and applicable holds, directives, dependencies and approval policy. Promote only if the target still equals the recorded base and the candidate's checks and review are valid for it. Use compare-and-swap or an equivalent protected publication operation; never force the target. Target movement invalidates the generation and its checks and review. Construct and verify a new generation without charging a repair merely for drift.

For a protected target, land through its authorized pull-request path and follow the confirmed remote result locally by fast-forward. For an unprotected target, retain the existing local promotion and publication authority. Neither path may rewrite an authoritative branch or bypass repository protection.

## A candidate's pull request cannot be merged before the harness is done

On November 18, 2026 the reviewer reported that a candidate's pull request was opened ready to merge while its review was still running, so anyone with merge rights could land it before the harness's review and checks finished (yoyodyne-ifd.429.224). Where landing goes through a pull request, these rules close that path:

- **It opens as a draft.** The harness opens every candidate's pull request as a draft. GitHub refuses to merge a draft, so nobody can merge it early. GitHub's required checks still run on a draft, so the wait for them is unchanged.
- **The harness marks it ready, and only then.** The harness marks the pull request ready for review only when the reviewer has approved this exact candidate and every required check has passed on its exact commit, as the sections above and below require. It marks it ready and asks GitHub to merge it in one step, naming the candidate commit. No person is ever asked to mark it ready.
- **An outage is waited out.** Marking ready and merging are retried through a GitHub outage exactly as the queue's merge step is, and the entry is never handed back for an outage. If GitHub refuses the merge because a required check on the same commit is running again, the harness waits for it as for any check on that commit, and spends no repair.
- **It goes back to draft before anything changes.** Before the harness pushes a new generation to a candidate's pull request, and when it withdraws an entry, it first returns the pull request to draft and confirms GitHub shows it as a draft. Withdrawal also disables merging when ready and removes the entry from the queue, as before. An approval therefore never covers a commit it did not judge.
- **A person's merge.** A person with merge rights may merge a candidate the harness has marked ready. The harness follows that landing exactly as it follows its own: it confirms the merge commit's first parent and files as the section below says, follows the result locally, and records the landing as merged by a person. If a candidate is merged when the harness had not marked it ready, it files a critical report naming the merge and whoever GitHub says merged it, because the landed revision may not be the one that was approved.

This repository's CI jobs must run on draft pull requests and must not start again on the same commit when it is marked ready. A job that skips drafts would leave a required check never started, which the section below treats as a check to ask for once more and then replace with the full local checks. At main e4c9aa75, `.github/workflows/ci.yml` starts its jobs on GitHub's default pull request events, which include drafts and do not include being marked ready, so both already hold; the build piece below keeps them so.

Alternatives rejected: a label, because a label alone stops nobody merging; a branch protection rule, or a required status the harness posts, because each needs a repository setting that only a person can change, and the queue would stall on every candidate if that setting were missing or wrong; marking the pull request ready when review approves and then merging later, because a pull request ready while its checks still run could be merged before they pass.

Build work: one piece, built and checked on its own. Candidate pull requests open as drafts; the harness marks one ready and merges it in one step, retried through outages; it returns to draft before a push and on withdrawal; a merge the harness did not mark ready files a critical report; and this repository's workflows run on drafts and do not start again on being marked ready.

A reviewer can check:

- a candidate's pull request is a draft from the moment it opens until the harness marks it ready;
- the harness marks it ready only after the reviewer approved and every required check passed on the candidate commit, and asks for the merge in the same step;
- a GitHub outage while marking ready or merging is waited out, and the entry is not handed back;
- a pull request ready for review is returned to draft before a new commit is pushed to it and when its entry is withdrawn;
- a person's merge of a ready candidate is confirmed and recorded like the harness's own merge;
- a merge of a candidate the harness had not marked ready files a critical report naming the merge;
- no repository setting is changed by the harness.

## When main moves after a candidate was sent to GitHub

On November 18 and 19, 2026 a candidate landed on `main` after another merge had already moved `main` past the candidate's recorded base, so it landed without being checked against that merge (yoyodyne-ifd.429.241). Each time the other merge was made by hand. The protection on `main` does not require a pull request to be up to date with `main`, so GitHub merged the candidate once its checks passed. Confirming the merge commit after landing finds this only once it has happened. These rules stop it before landing:

- **The merge is asked for once, at the moment it can be checked.** The harness never turns on merging when ready for a candidate. It asks GitHub to merge directly, holding the promotion lease, immediately after reading `main` and finding it still at the candidate's recorded base. If `main` has moved, it asks for no merge.
- **A moved main withdraws the candidate.** On every pass of the queue, and whenever the harness sees `main` move, each candidate pushed to GitHub and not yet landed whose recorded base is no longer the tip of `main` is withdrawn: its pull request returns to draft, as the section above says, any merging when ready is turned off, and a new candidate is built on `main` as it stands, with checks and review earned again. No repair attempt is charged, as for any movement of the target. The record names the commit `main` moved to.
- **GitHub's refusal counts as a moved main.** If GitHub refuses the merge because the branch is not up to date with `main`, the harness treats it exactly as a moved main above, and spends no repair.
- **The forge's own guard.** Between the harness reading `main` and GitHub merging there is still a moment in which another merge can land. Only GitHub can close it, by the protection on `main` requiring branches to be up to date before merging. That setting is a step only a person can take, and the harness never changes it. yoyo doctor reports it missing whatever execution.merge_queue says. The queue does not stop while the setting is missing, because the rules above leave only that moment, and confirming the merge commit after landing still files a critical report if a landing made in it differs from the candidate that was checked.

Alternatives rejected: relying only on the up to date setting, because a project that has not set it, or a setting changed later, would leave candidates landing unchecked with nothing in the harness noticing before the landing; relying only on confirming the merge commit after landing, because it finds the unchecked landing once it is already on `main`; stopping the queue until the setting is present, because every approved change would then wait on a setting only a person can change, while the rules above already close all but a moment.

Build work: two pieces, each built and checked on its own.

1. The merge step: no merging when ready for a candidate; the merge asked for directly under the promotion lease, only after reading `main` at the recorded base; GitHub's refusal for a branch not up to date treated as a moved main, with no repair spent.
2. Watching for a moved main: on every queue pass and on seeing `main` move, every pushed candidate not yet landed whose base is no longer the tip of `main` is returned to draft, has merging when ready turned off, and is rebuilt, with no repair charged and the new tip recorded; and yoyo doctor reports a missing up to date requirement whether the switch is on or off.

A reviewer can check:

- no candidate ever has merging when ready turned on;
- the merge is asked for only after a read of `main` that found it at the recorded base, under the promotion lease;
- a candidate pushed and not landed is returned to draft and rebuilt when `main` moves by any merge, a person's included, and no repair is charged;
- GitHub's refusal for a branch not up to date leads to a rebuild with no repair spent;
- yoyo doctor reports a missing up to date requirement with the switch on and with it off, and the harness changes no setting.

# Checking one entry ahead

While a candidate is being checked, the queue may build the next entry's candidate on top of the current candidate and check it at the same time. That second candidate lands only if, when its turn comes, the target equals the base it was built on. Otherwise it is built again on the target as it stands and checked again, and the entry is not charged a repair for it.

No entry is checked ahead where landing goes through a pull request. GitHub lands a merge commit above the candidate, so the next candidate's base is never the target that results, and checking ahead there would spend checks that never count. Checking ahead helps only a project that lands locally.

# GitHub's required checks as the queue's full check

Where the project has switched on checks on CI, as [checks on CI](checks-on-ci.md) describes, GitHub's required checks run on every commit the harness pushes, including a queue candidate. Running the full local checks on that candidate as well would check the same commit twice. Under that switch, the required GitHub checks on the candidate commit stand as the queue's full check, but only when all of these hold:

- the harness itself read the check results for the exact candidate commit, not for the pull request's head or any other commit;
- every check the target branch requires passed; one missing, pending, failed or neutral counts as not passed;
- the digest of each workflow file the required checks ran from is recorded with the result, so a later reader can tell what ran;
- the target stayed at the candidate's recorded base from the push until the merge.

The local run then keeps only the quick checks GitHub does not run: formatting, vet, build, the tests that need macOS, and, while the target requires no race check of its own, the race check narrowed to what the candidate touches as [choosing checks by change](choosing-checks-by-change.md) describes.

The review runs beside the wait for GitHub's checks, and a failing required check stops the review. A required check that never started, was cancelled or was skipped is asked to run again once. If it still has not passed, the harness runs the full local checks on the candidate instead, records why, and spends no repair attempt.

After the merge, the harness confirms that the merge commit's first parent is the recorded base and that its files are exactly the candidate's. If either is not so, it files a critical report naming the merge, because the landed revision is then not the one that was checked.

This repository's CI jobs pass at once for a pull request that changes only documents: each job's first step checks the changed paths and ends the job as passed. A path filter on the workflow is not used, because GitHub then leaves a required check missing and the pull request can never merge.

The evidence stays bound to one exact commit, so [integration requires revision-bound evidence](../decisions/invariants/integration-requires-revision-bound-evidence.md) holds unchanged.

Alternatives rejected: accepting the checks on the pull request's head, because that head is not the candidate; keeping the full local run as well, because it checks the same commit twice and holds the queue for it; trusting a check GitHub skipped, because a skipped check proves nothing about the candidate.

## Why this way out of the slow queue

On November 16, 2026 the operator asked for three ways out of the slow merge queue to be weighed, one picked, and the pick tried for a few days (yoyodyne-ifd.429.199). The facts he was given: about 11 changes landed a day on GitHub's merge queue with the harness's replay (October 26 to November 7), and about 12 a day on the harness's own queue (November 9 to 15). About 50 approved entries had waited two days or more, and about 10 a day were handed back. GitHub's CI already runs the full make check on every pull request, in a median of 19 minutes. The harness's queue runs it again on this machine, one candidate at a time, in about 81 minutes, more than half of it the race check, beside developer runs.

The pick is this section: keep the harness's queue, and stop repeating GitHub's checks on this machine. It removes the 81 minute local step, which is the cost the facts point to, and keeps every rule the queue holds now: one candidate built by the harness, checks and an independent review bound to that exact commit, and one promotion at a time.

Rejected:

- **Going back to the setup before November 8,** with `execution.merge_queue` off and GitHub's merge queue required again. The replay path still runs the full checks on this machine before handing a change to GitHub, so the 81 minute step stays, and the record before November 8 shows no faster landing.
- **Handing approved changes straight to GitHub's merge queue with no check here.** GitHub builds its own combined commit and lands it, and the harness cannot bind its checks and review to that commit, as the section on GitHub's own merge queue below says. It would land a revision nobody's review judged, against [integration requires revision-bound evidence](../decisions/invariants/integration-requires-revision-bound-evidence.md).

## The trial on this repository

**What must have landed first.** The setting, off by default (yoyodyne-ifd.429.76.1); reading GitHub's required checks for the exact candidate commit, with workflow digests and the base recorded (yoyodyne-ifd.429.185); the quick local checks, with the review beside the wait (yoyodyne-ifd.429.186); CI results counting only for the exact candidate commit and shown to the reviewer (yoyodyne-ifd.429.76.7); and confirming the merge commit after landing (yoyodyne-ifd.429.187). Each lands with the setting off, so nothing about the queue changes until the switch.

**When the trial starts.** The trial starts with the landing of the change that turns the setting on for this repository (yoyodyne-ifd.429.76.8), and that change may land only once every piece above is on main and the check below has been made. Build piece 8 of [checks on CI](checks-on-ci.md) landing does not start it. The reason: until confirming the merge commit is on main, a landing through GitHub whose revision differs from the one that was checked goes unnoticed, so a trial started earlier would count as successes landings that nobody confirmed.

**Checked once before the switch.** Changes have already landed through GitHub pull requests since build piece 8 of checks on CI landed, without the merge commit being confirmed. Once confirming the merge commit is on main, it is run once over each of those landings. A landing whose merge commit's first parent is not its recorded base, or whose files differ from its candidate's, is filed as a critical report, and the switch waits until each such report is settled.

**The order of the switch.**

1. The builds above land, with the setting off.
2. The architect compares what this repository's CI jobs run with what the full local check runs, and records any test the local check runs that GitHub does not. Each one either joins the quick local checks or is named as a known gap before the trial starts. The list is below.
3. The operator's assistant confirms, on his direction, that the protection on `main` requires exactly the CI job names the harness reads, and that GitHub's own merge queue stays off for `main`, as the section on it below requires. Repository settings are changed only by a person, never by an agent.
4. The check of earlier landings above is made.
5. The change that turns the setting on for this repository (yoyodyne-ifd.429.76.8) lands. That landing starts the trial.
6. At the end, the architect records the result in this design.

**Step 2: what the full local check runs that GitHub does not.** Compared on November 18, 2026 from the Makefile and `.github/workflows/ci.yml` at main e4c9aa75, with the job and target names read again at main cdcd0559a79a on November 21. The full local check, `make check`, runs `fmtcheck`, `test`, `race` and `vet`. GitHub's required jobs `test`, `race` and `vet` run `make test`, `make race` and `make vet` on Ubuntu, and the `build` job runs `make fmtcheck` and `make build`, so every target of the full local check runs on GitHub too. What differs is the tests those targets run on each machine:

- **Tests only macOS runs.** The packages `make test-macos` names, and the test in `internal/beads` that runs the developer sandbox under `sandbox-exec`, run nothing on Ubuntu. They are already in the quick local checks and stay there.
- **Tests that need the tracker program.** Tests that skip where `bd` is not installed skip in GitHub's `test` and `race` jobs, which install no `bd`. The `getting-started` job installs it, but runs only the tracker conformance tests and the README's getting started walk, and is not a required check. This is a known gap for the trial; it is not moved into the quick local checks, because that would run the whole of `internal/beads` locally on every candidate. A build piece closes it: GitHub's `test` job installs the same pinned `bd` the `getting-started` job installs.
- **The dashboard render test.** It skips where Node is not installed, and `make test` prints whether it passed or skipped. Whether GitHub's Ubuntu machine has Node was not confirmed for this revision. The same build piece reads that line in one `test` job's output; if it says SKIP, the piece adds a step that installs Node to the `test` job, and until then the test is a known gap.

The `race` check is the same on both: each runs `make race` with no package list, so both skip only a change that is all documents, and GitHub's job fetches the whole history that comparison needs. The `getting-started` target, with `adoption` kept only as its old name until the project configuration names the new one, and the `codex-resume` target are not part of the full local check, so they are not gaps.

**What it measures,** each against the three days before the trial, read from the queue's own records of each choice and each landing:

- changes landed per day;
- the median time from approval to landing;
- the number of entries waiting two days or more;
- entries handed back, by cause: a real failure, a check that fails and then passes on the same commit, or a conflict;
- minutes per candidate, from being chosen to landing;
- any break on `main` from a change that passed GitHub's checks.

**How long.** Three working days or 40 landings, whichever comes later.

**When it switches back.** At once on a break on `main` that the full local check would have caught, including one in a test named above as a known gap; or when fewer than 11 changes land a day for two days running. Switching back is one configuration change, the setting off again, made by the operator's assistant on his direction. Nothing else changes back, because every other rule of the queue stays as it was.

**The entries already waiting.** They stay in the queue in the order [the order the queue takes entries in](#the-order-the-queue-takes-entries-in) gives, and from the switch onward go through the new path; those waiting four hours or more go oldest first under its first rule. An entry whose candidate is being checked when the switch happens finishes in the way it started.

Build work: one piece, built and checked on its own. GitHub's `test` job installs the pinned `bd` the `getting-started` job installs, and the piece records from one `test` job's output whether the dashboard render test passed or skipped, adding a step that installs Node where it skipped.

A reviewer can check:

- with the setting off, the queue runs exactly as before, whatever has landed;
- the change that turns the setting on lands only after every piece above is on main, the earlier landings have been checked once, and the protection on `main` was confirmed;
- each earlier landing whose first parent or files differ is filed as a critical report;
- the step 2 list names every test the full local check runs that GitHub does not, each either in the quick local checks or named as a known gap;
- each figure above is read from the queue's records, not estimated;
- switching back needs only the setting turned off.

# Forge-native queue

The adapter must expose queue entry and group identity, exact target base, contributing heads, combined candidate commit, required checks and their configuration, independent approval evidence, queue timeout and confirmed landing identity. GitHub merge_group checks belong to the combined group commit, not the pull-request head. All-green grouping does not prove that configured harness checks and independent review occurred.

Use native mode only when those requirements are enforced before landing. Otherwise select the harness-run queue before admission and report why native mode is unsupported. If neither mode can satisfy repository protection, retain an actionable hold rather than bypassing it. A native queue timeout is recorded from the forge's actual policy; the reported sixty-minute setting is evidence about this repository, not a universal constant.

# GitHub's own merge queue

No adapter today meets the requirements in Forge-native queue for GitHub's own merge queue: GitHub builds its own combined commit and lands it, and the harness cannot bind its configured checks and an independent review to that commit. So with execution.merge_queue true, the target branch must not require GitHub's merge queue.

With the switch on, the harness reads the target branch's protection before it admits anything. If that protection requires GitHub's merge queue, or the protection cannot be read, the harness:

- does not run in queue mode for that repository and target, and admits nothing to its queue;
- holds every change waiting to be admitted, and the hold names the setting that caused it: the branch rule that requires GitHub's merge queue, or the protection read that failed and why;
- never falls back to replay for those changes while the switch is on, because the operator chose the queue by that switch, and replay would land work through the path they turned off;
- files one report for the repository and target when the condition is first found, and no further report until it has cleared and been found again;
- never changes repository settings itself. Removing the requirement, or granting the access the read needs, is for a person.

The hold lifts by itself at the first later read that finds the protection readable and GitHub's merge queue not required; the waiting changes are then admitted in the order they were approved. Changes already admitted before the condition appeared are not landed while it stands: their candidates wait with their checks and reviews kept, and checks the target has since moved past are run again as usual.

Alternatives rejected: running the harness queue and then handing its candidate to GitHub's queue, because GitHub regroups and lands a commit no harness check or review judged; falling back to replay without saying so, because it overrides the one switch the operator set; changing branch protection, because no agent may change repository settings.

# Merging with the switch off

With execution.merge_queue false, integration is the existing path, and it still guards against the target moving between the final checks and landing:

- the approved change is replayed onto the current target, and configured checks and an independent review run on the replayed revision;
- promotion happens under the target promotion lease, with compare and swap against the target the replay was built on, so a moved target sends the change back to replay instead of landing;
- a merge through the forge names the exact reviewed commit, and the forge refuses it if the pull request's head has moved, so nothing other than the reviewed revision is merged.

On GitHub, the target branch's protection should require the configured checks and require a branch to be up to date before merging. yoyo doctor reports either one missing, naming the setting, and the harness never changes it. A missing setting does not stop integration, because the lease and the pinned merge still hold; it is reported so a person can add the forge's own guard as well.

# Failure, withdrawal and continuation

Distinguish candidate conflict or failing work from infrastructure refusal, target failure, target drift and unreadable evidence. A file annotation is evidence, not sufficient proof of causation. Reuse target-failure work and existing triage records rather than creating duplicate repairs.

Before rewriting a queued head, handing it to repair or transferring queue mode, establish that its previous merge authority was withdrawn. Reuse the existing disable-auto-merge plus dequeue operation. On ambiguous withdrawal, read queue and merge state before proceeding. A request already landed goes to completion reconciliation, not replay.

Preserve the same run and its artifacts on a recoverable stop. Reuse supported integration continuation where its preconditions hold. Record the refusal and next mover when continuation is not yet possible. Apply existing finite retry and continuation budgets durably; restarts and mode changes do not reset them. Genuine candidate changes re-earn checks and independent review.

# Crash recovery and disabling

Persist intended mutations before requesting them, with pinned identities or idempotency keys. After restart, observe target, publication and queue state before repeating a mutation. Separate tracker, run, queue and docket writes require explicit partial-handoff reconciliation; no ordering makes them an atomic transaction. Uncertain saves require durable readback. A missing worker does not authorize deleting useful preserved work.

Disabling the switch blocks new queue admissions. Entries already admitted drain in their recorded mode; disabling does not silently transfer or re-arm them. An explicit transfer first establishes withdrawal from the old mode, preserves ordering and history, then earns fresh checks and review on a new candidate. New integration uses the existing replay path when the switch is false.

# Existing mechanisms and acceptance

The target promotion lease and shared build cache are reused. Post-landing checks supplement verification but do not replace the checks and review on the candidate before landing. Existing publication reconciliation, withdrawal, integration continuation, ownership resolution and recovery bounds remain the mechanisms for their respective duties.

Verification must cover two concurrent admissions, competing workers, target movement during checks and review, candidate or configuration changes, restart before and after each remote mutation, uncertain saves, partial tracker/run writes, confirmed withdrawal, queue drops and timeouts, target failures, exhausted budgets, protected targets, grouped forge candidates, and disable/drain/transfer behavior. Assert that the landed revision is the checked and reviewed candidate, no stale verdict authorizes a new generation, and neither duplicate execution nor lost preserved work follows a restart.

Verification must also cover the order: every point in the list a reviewer can check under [the order the queue takes entries in](#the-order-the-queue-takes-entries-in), which is the one statement of what the order must do.

And the running of checks:

- checks and review run at once, a failing check stops the review, and both are charged to the entry's run and item;
- a check ended by the machine running it is run once more, and a second ending withdraws the entry with a report naming the check and spends no repair;
- after a restart, passed checks are kept only where commit and check set digest are both recorded;
- a candidate checked ahead lands only on the base it was built on, is rebuilt without charge otherwise, and nothing is checked ahead where landing goes through a pull request.

And GitHub's required checks as the full check:

- with checks on CI on, a candidate whose every required check passed on its exact commit, with workflow digests recorded and the target unmoved, lands with only the quick local checks run;
- a check read for the pull request's head, a missing, pending, failed or neutral required check, or a target that moved, does not count;
- a required check never started, cancelled or skipped is asked to run once more, then the full local checks run, the reason is recorded and no repair is spent;
- a merge whose first parent is not the recorded base, or whose files differ from the candidate's, files a critical report;
- a pull request changing only documents passes this repository's CI jobs at once and is never left with a required check missing.

And GitHub's own merge queue and the switch off:

- switch on and the target requires GitHub's merge queue: nothing is admitted, each waiting change is held with the setting named, exactly one report is filed, no change is replayed, and no repository setting is changed;
- switch on and the protection cannot be read: the same, with the failed read named;
- the requirement removed: the next read lifts the hold and the waiting changes are admitted in approval order;
- an admitted change whose candidate waits while the condition stands: it is not landed, and its checks run again if the target moved;
- switch off: a change lands only as the replayed, checked and reviewed revision; a moved target sends it back to replay; a merge naming a reviewed commit is refused when the pull request's head has moved;
- yoyo doctor reports a target branch that does not require the configured checks or an up to date branch, and changes nothing.

# Pieces of work this implies

The development manager owns decomposition and the Lead Product Manager owns admission and order; this document creates no work items. These are pieces each of which can be built and checked on its own:

1. The order's first form: choosing the next entry by priority and age, with the tracker read at choosing time.
2. Recording each choice, and showing the waiting order and the rule for the first entry on yoyo status and the dashboard.
3. Reading GitHub's required checks for the exact candidate commit, with the workflow file digests and the base recorded, and counting them as the full check only when every condition above holds.
4. Under checks on CI, the local run dropping to the quick checks, the review running beside the wait, and a failing required check stopping the review.
5. A required check never started, cancelled or skipped: one request to run it again, then the full local checks, with the reason recorded and no repair spent.
6. After a merge, confirming the first parent and the files, and the critical report when either differs.
7. This repository's CI workflow: each job's first step passing at once for a pull request that changes only documents, with no path filter.
8. Document entries landing together: one candidate of at most eight document entries within the review size limit, no two changing the same file, the document checks and one review run once, a failed candidate split so each entry is tried alone and only the one at fault handed back, and the landing recorded on every entry with its cost shared.
9. The order's revised rules: the four hour age rule, priority 0 code with no document beside it, rule 3 passed over after a document landing while ordinary code waits, and each choice recording every entry in it and whether rule 3 was passed over.
10. The daily figures of median and longest wait from admission to landing for priority 0 code, other code and documents, read from the queue's own records and shown on yoyo status and the dashboard.

# Evidence and publication limits

Inspected queuedchecks.go, integrationresume.go and internal/publish/checks.go in full at 1c880b64631b. The existing forge check reader queries pull-request-head checks and is not a check reader for a native queue's candidate. Existing withdrawal disables auto-merge and dequeues. For the order's revision (yoyodyne-ifd.429.255), internal/orchestrator/mergequeueorder.go was read at d581db8fb016 and applies the earlier five rules as written; this document was read whole at 596bbfe1409e before this revision. The code that chooses queue mode (yoyodyne-ifd.429.59.3), the doctor and status checks and the code that reads GitHub's checks today were not read for this revision, so how much of the reading of required checks already exists is not established. V1's relevant integration sections are reconciled under yoyodyne-ifd.437.33. This design does not claim implementation, successful checks or completion of the parent work item.
