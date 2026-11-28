---
id: landing-code-changes-together
kind: design
title: Landing several approved code changes in one checked merge queue candidate
supports:
    - v1-goals
    - milestone-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-28T02:17:36.60846Z
      reason: 'Answers yoyodyne-ifd.429.317 and the Lead Product Manager''s report 1075017c: several approved code entries share one merge queue candidate up to a setting (default 4, bounds 1 to 8 held in code), none sharing a file and priority 0 never mixed. One review and one check bind to the combined commit. A failure is halved until one entry fails on its own candidate; every part gets fresh checks and review. It shares the several-entry record with document batching and lifts the merge queue design''s earlier rejection of landing code together.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-28T02:17:36.60846Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 4311, for document-4311.1 under the automatic approval policy
---
# Purpose

This design answers the item for landing several approved changes in one checked candidate (yoyodyne-ifd.429.317). It serves the milestone goal of a week on this repository in which no hour passes with ready work waiting and a developer slot idle ([milestone goals](../product/milestone-goals.md)), and the goal to integrate successful work automatically. On November 27, 2026 developer runs had about 33 changes approved between 08:32 and 14:41 PST while the merge queue landed 17. By evening 25 changes had landed that day, a median of 18 minutes apart, while 18 approved changes waited and all four developer slots stood empty. The queue checks and lands one code candidate at a time, so landing, not development, is now the limit.

# What this changes

[Integration through a merge queue](integration-through-a-merge-queue.md) lands waiting documents together, and rejected landing code entries together for now, until the queue's pace work had measured how often candidates fail. This design lifts that rejection. The queue is now the limit, and the split below costs at most a few extra candidates when one entry fails, against the landings it saves when none does.

Everything else in that design holds unchanged:

- the order the queue takes entries in;
- the hold for a returned change;
- checks chosen by the paths a candidate changes;
- GitHub's required checks standing as the full check;
- draft pull requests;
- rebuilding when main moves;
- one promotion at a time.

The merge queue design's next revision drops the rejected alternative, lists this design among its pieces of work, and points here. Until then, where the two differ on landing code together, this one decides.

# Decision

## The setting

`execution.merge_queue_code_batch` is the most code entries one candidate carries. It is a whole number from 1 to 8, and its default is 4. With 1 the queue works exactly as it does today. The default and bounds are held in code, as [limits held in code](../decisions/limits-held-in-code.md) records for limits of this kind, and a value outside them refuses the configuration, naming the setting and its range. Document candidates keep their own limit of eight.

Why 4:

- a candidate that passes lands four changes for the cost of one check and one review;
- with one failing entry among four, the split below checks at most five candidates, where one at a time checks four;
- so a failed batch costs about one candidate more than today, and a batch that passes saves three.

Eight would let a failed batch cost up to seven candidates, and would ask one review to judge eight changes at once. Any project can set 1.

## Which entries share a code candidate

The queue chooses the first entry exactly as the order says. If it is a code entry that may share, the queue adds further waiting code entries, in the order the same rules would choose them next, until the candidate holds the setting's number.

A waiting entry is passed over, and waits for a later candidate, when:

- it is a document entry, because documents land in candidates of their own;
- it changes a file that an entry already in the candidate changes, so changes to the same file are kept apart;
- adding it would take the candidate past the reviewer's size limit on a change, as [review size limits](../decisions/review-size-limits.md) records that limit;
- its work item depends on the work item of an entry not yet landed, the candidate's own entries included;
- it is held under the hold for a returned change;
- its files cannot be read;
- the first entry is priority 0 code and this entry is not.

The files of an entry are the paths its change modifies, adds, deletes or renames against its recorded base, as the merge queue design says.

Two kinds of entry never share a candidate with a new entry:

- a code entry brought up to date after a conflict, which takes the next candidate alone, as the further rules of the merge queue design say;
- an entry in a split that is still running, below, whose part takes only the entries already in it.

Passing over never moves the first entry. The record of each choice names every entry in the candidate, and each entry passed over with the reason.

## One candidate, checked and reviewed once

The entries are applied in order on the target as it stands. Because no two of them change the same file, an entry applies on the entries before it exactly when it applies on the target alone. An entry that conflicts with the target is handed back exactly as it would be alone, and the rest are built without it.

The result is one candidate commit. Its generation records the target base and every contributing head in order, as the merge queue design already records for one.

The candidate runs the checks a code candidate runs today, unchanged:

- the configured checks whole;
- or, where checks on CI are on, GitHub's required checks on the exact candidate commit, under every condition the merge queue design sets.

One independent review judges the candidate, each entry against its own work item. Its verdict does one of three things:

- approves the candidate;
- refuses named entries;
- refuses the candidate as a whole.

The verdict and the check results are bound to the candidate commit and to the list of its entries. No entry is approved on any commit other than one its review judged, so [integration requires revision bound evidence](../decisions/invariants/integration-requires-revision-bound-evidence.md) holds as it is.

One landing records every entry in it, the merge commit, and the other entries landed with each one, and confirms each entry's run and work item landed by that merge. The cost of the checks and the review is charged to the entries' runs in equal shares. Where landing goes through a pull request, the candidate's pull request names each entry and its work item, and follows the draft and moved main rules of the merge queue design like any candidate.

These parts are built once and shared with document batching (yoyodyne-ifd.429.267):

- the candidate record holding several entries;
- the review that judges several entries, each against its own item;
- the landing recorded on every entry, with the cost shared.

Whichever of the two is built first builds them, and the other uses them. There is no second kind of candidate.

## When the candidate fails

Before anything is split, a failing check gets the reruns the queue already gives one entry. For example, a check ended by the machine running it is run once more. Then:

- **The review refuses named entries.** Those entries are handed back as refused, exactly as if each had been refused alone. The rest are built again as a new candidate without them, which earns its checks and review afresh.
- **A check fails, or the review refuses the candidate as a whole.** The entries are cut into two parts in their order, the first part taking the larger half when the number is odd. Each part becomes a candidate of its own, built on the target as it stands at its turn, and checked and reviewed afresh. A part that passes lands. A part that fails is cut in two again. Only an entry that fails on a candidate of its own is handed back, and only then is a repair charged, exactly as today. The entries of a part that passes are charged nothing.
- **Every part passes.** The failure did not repeat on any part, so everything lands. The record says the batch's failure did not repeat and names the check that failed, so the queue's figures count it apart from real failures.
- **Two entries fail only together.** The second part is built on a target that already holds the first, so the failure shows there. It is narrowed to the entry that fails on the target as it now is, and that entry is handed back as failing on the target, which it does.

Document candidates keep their own rule of trying each entry alone, because their checks are cheap. For code, trying each entry alone would cost one full check per entry. Halving costs about two candidates for each time the number of entries doubles, and lands the good part one candidate later.

## The order while a split runs

The parts of a split take the next candidates, one after another, ahead of numbered rules 1, 3, 4 and 5 of the order. Changes already checked once are therefore not sent back behind new work. Priority 0 code, and a code entry brought up to date after a conflict, still go ahead of a part, as neither waits behind ordinary code today.

While a split runs:

- a part never takes a new entry;
- an entry superseded or withdrawn meanwhile leaves its part, and a part left empty is dropped;
- an entry that a new hold covers leaves its part and waits in the order;
- when main moves, a part is rebuilt with no repair charged, like any candidate.

## A restart

Before the first part is built, the split is written on the failed candidate's generation: the parts in order, and why the candidate was split, naming the failing check or the review's refusal. A save whose result is uncertain is read back.

After a restart, the queue reads that record and goes on with the first part that has neither landed nor been handed back. A part that was being checked is built again, unless its commit and check set digest are both recorded, as the merge queue design already requires for keeping checks across a restart.

If the split record cannot be read, its entries return to waiting in the order, nothing is charged, and the record of the next choice names the unreadable split.

## What a person sees

yoyo status and the dashboard show:

- each candidate with every entry and work item in it;
- for a candidate made by a split, the candidate it came from and why that one was split, naming the check or the review's refusal, and the parts still to try;
- for each landed entry, the entries landed with it.

The queue's daily figures add, read from its own records:

- the number of entries per landed candidate;
- the number of splits;
- the number of failures that did not repeat on any part.

# Alternatives rejected

- **Trying each entry alone after a failure, as documents do.** Every code candidate runs the full checks, so this would cost one full check per entry for every failed batch.
- **Letting entries that change the same file share a candidate.** Changes to the same file are the likeliest to break together, and the hardest to tell apart when the candidate fails.
- **Using the batch's review for its parts.** A part is a different commit, and evidence counts only for the commit it judged.
- **Mixing priority 0 code with other code.** A failure in lower priority code would delay urgent code by the whole split.
- **Landing several candidates at once, or a second queue.** [One promotion per target branch](../decisions/invariants/one-promotion-per-target-branch.md) forbids two promotions at once.
- **A default of 1.** It leaves the queue as the limit on this repository and on any project with the same backlog.
- **A default of 8.** A failed batch would cost up to seven candidates, and one review would judge eight changes.

# Pieces of work this implies

The development manager owns decomposition; this document creates no work items. Each piece can be built and checked on its own.

1. **The setting, and choosing the entries.**
   - The setting, with its default and bounds held in code and its validation.
   - The rules for which entries share a candidate.
   - The record of each choice naming every entry and each one passed over with the reason.
   - With the setting at 1, the queue chooses exactly as today.
   - Tests: three code entries changing different files are chosen together; one sharing a file waits; priority 0 is not mixed with other code; a dependent entry waits; the size limit stops the candidate.
2. **One candidate of several code entries, checked, reviewed and landed.**
   - It uses, or builds, the parts shared with document batching.
   - The reviewer is given each entry with its work item.
   - The landing is recorded on every entry, with the cost shared and the pull request naming each entry.
   - Tests: a batch of three lands, and each entry's run and item are confirmed landed by one merge.
3. **The split.**
   - Halving, and a review refusing named entries.
   - The parts' place in the order, and the split record kept across a restart.
   - Status and the dashboard showing why a candidate was split.
   - Tests: a batch with one failing entry hands back only that entry, the others land, and no repair is charged to them; a failure that does not repeat lands everything and is recorded; a restart between parts goes on from the next part.

This repository needs no change to its configuration, because the default applies.

# A reviewer can check

- with the setting at 1, every candidate holds one code entry, as today;
- waiting code entries changing different files share one candidate, up to the setting, in the order the rules would choose them;
- an entry changing a file already in the candidate, one past the review size limit, a dependent one, a held one and one whose files cannot be read each wait, and the record says why;
- a candidate whose first entry is priority 0 holds only priority 0 code, and no code candidate holds a document entry;
- a code entry brought up to date after a conflict still goes alone;
- one review judges the candidate, each entry against its own item, and every candidate made by a split is checked and reviewed afresh;
- a review refusing named entries hands back only those, and the rest are rebuilt and checked again;
- a failing check splits the candidate in two in order, a part that passes lands, and only an entry that fails on a candidate of its own is handed back with a repair charged;
- a batch whose failure repeats on no part lands every entry, and the record says the failure did not repeat;
- the parts of a split go ahead of every numbered rule but priority 0 code, and behind a code entry brought up to date after a conflict;
- a restart between parts goes on from the next part, and an unreadable split record returns its entries to waiting with nothing charged;
- every landing records each entry, the merge commit and the entries landed with it, with the cost charged in equal shares;
- status and the dashboard show each candidate's entries and why a candidate was split.

# What it rests on

The merge queue design was read whole at main 2e6a85294129. The figures above come from the work item and from the Lead Product Manager's report of November 27, 2026, not from the queue's records, which were not read. The code that builds candidates and the reviewer's instructions were not read for this design, so how much of the shared record already exists is not established. Document batching (yoyodyne-ifd.429.267) is admitted and not built.
