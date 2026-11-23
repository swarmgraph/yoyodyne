---
id: documents-and-code-in-one-merge-queue
kind: decision
title: Documents and code stay in one merge queue, the ruled order is finished first, and GitHub's merge queue waits on measured waits
supports:
    - milestone-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-22T23:31:45.107341Z
      reason: 'Rules the operator''s two questions of November 22 on the merge queue: documents stay in the one queue, with landing them together and taking turns with code finished first; the order running today is not kept and the ruled order stands; GitHub''s merge queue is not adopted now, with what moving would take and the measured wait that would reopen it. Written from reads at main 7d182b7d1.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-22T23:31:45.107341Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3601, for document-3601.1 under the automatic approval policy
---
# Documents and code in one merge queue, and GitHub's merge queue

Ruled by the architect on November 22, 2026, at the operator's request through his assistant at 2:30 PM PST, on two questions he asked after saying the merge queue seemed a bottleneck. It serves the milestone goal of a week in which no hour passes with ready work waiting, and the goal of integrating successful work automatically. Until [integration through a merge queue](../designs/integration-through-a-merge-queue.md) is revised to match, this record decides where the two differ.

## What was found

Figures from the operator's assistant, read from the queue's own records from November 19 to 22:

- code: 93 landed and 32 handed back; median wait 2.5 hours, a quarter of entries over 8.4 hours, longest 9.3 hours; 21 to 31 code landings a day;
- documents: 123 landed, 57% of all landings; median wait 1.0 hour; 5% handed back;
- on November 22 from 12:05 to 2:24 PM PST the queue landed 7 documents and 2 priority 0 fixes, and no priority 1 code; 18 code entries and 7 documents were waiting.

Read at main 7d182b7d1: the order code, `internal/orchestrator/mergequeueorder.go`, and the design. Read at 2:32 PM PST: the item for the queue order fix (yoyodyne-ifd.429.266) as the tracker held it.

The order running is not the order the design rules. The code takes an entry by age only after eight hours, puts every document ahead of code that is not priority 0, puts the architect's oldest document beside priority 0 code, and lands each document as a candidate of its own. The design, since November 20, rules an age limit of four hours, no document beside priority 0 code, waiting documents landed together, at most eight, and never two document candidates in a row while ordinary code waits. The change that builds that order was approved by its reviewer at 10:23 AM PST, passed GitHub's checks, and waits in the queue as entry 568, held back by the order it replaces. Landing documents together is not built.

So the eight hour wait for priority 1 code comes mostly from the order not yet built, not from the queue itself.

## Decision 1: documents stay in the one queue

Documents and code keep landing through one queue on main. What changes is how much a document costs code: documents land together and take turns with code, as already ruled, and that work is finished first.

Why not a separate line, or landing a document with no combined candidate:

- there is one main. Every landing moves it, and under the rule for a moved main every code candidate already sent to GitHub is returned to draft and built again. A document landed by any other route moves main just the same, so it costs code as much as now, or more, because it no longer takes turns;
- two lines landing on one target would need two promotions at once, which [one promotion per target branch](invariants/one-promotion-per-target-branch.md) forbids;
- a document landed without a candidate built on main as it stands is never checked against main, so a link to a file or heading another change removed would land broken.

What changes the cost is the number of landings documents take. Landing up to eight together cuts it by up to eight times. At the counts above that frees most of the roughly 30 landings a day documents now take, for code.

## Decision 2: the ruled order stands, and the order running today is not kept

The operator asked, if the queue stays, about documents ahead of all code that is not priority 0, with code taken by age only after eight hours. That is not kept: it is what held priority 1 code for eight hours. The order is the design's: four hours by age, then priority 0 code, then waiting documents together, never a document candidate twice in a row while ordinary code waits, then other code by priority. The age limit stays a fixed value in code, not a setting.

While the order fix waits, the architect sends no new revisions for the review of every design against the code (yoyodyne-ifd.437.84), so they add nothing to the line.

To be done, in this order, for the Lead Product Manager to schedule:

1. Land the order fix (yoyodyne-ifd.429.266). It is approved and waiting.
2. Landing waiting documents together, the design's build piece for it, admitted if it is not yet.
3. After three working days, read the daily wait for each kind of entry from the queue's records, as the design already requires.

## Decision 3: not GitHub's merge queue, for now

The design says the harness cannot bind its checks and review to the combined commit GitHub's queue builds. That is now only partly true:

- the checks can be bound. GitHub runs the required checks on the exact commit its queue builds, and that commit is the one that lands, so the harness can read and record them for that commit, as it does now for its own candidate;
- the harness's own review, and its quick local checks such as the tests only macOS runs, can be bound only one way: the harness runs them on GitHub's commit and posts the result as a status that GitHub requires before it lands anything. Without that, GitHub lands a commit no review of the harness judged.

What moving would take:

1. a person turns on GitHub's merge queue for main and makes the harness's status a required check, because the harness's token cannot change those settings;
2. the harness's token can post statuses;
3. the workflow runs its jobs for GitHub's queue as well as for pull requests;
4. the harness watches GitHub's queue, runs its review and quick checks on each commit GitHub builds within GitHub's time limit for a queue entry, and posts the result, within the capacity for heavy checks;
5. the harness asks GitHub to add an approved pull request to its queue, and treats a pull request GitHub takes out of its queue as handed back;
6. priority 0 work asks GitHub to put it at the front of its queue; placement and taking turns with documents are given up, because GitHub's queue takes entries first in, first out;
7. a trial on this repository, with one setting to go back to the harness's own queue.

What it would give that nothing else can: on GitHub the harness lands a merge commit above its candidate, so it cannot check a second candidate while the first is checked; GitHub's queue lands the commit it checked, so it can check several at once.

Why not now:

- the wait seen today has a known cause with an approved fix waiting, and a second fix already ruled. Moving before those land would hide whether the queue was ever the limit;
- every landing would then wait on a setting only a person can change, which the design already rejected for its rule on draft pull requests: if that setting were missing or wrong, nothing would land;
- on the figures above the harness's queue lands 21 to 31 code changes a day, against about 11 on GitHub's queue with replay from October 26 to November 7, although much else changed between those periods.

When it is reconsidered: once the order fix and landing documents together have run three working days, if priority 1 code still waits a median of more than two hours, or more than four hours for a quarter of its entries, the architect designs the move to GitHub's queue as above. That is a design, not a change of goal: no goal admits or refuses different work because of it.

## Alternatives rejected

- A separate line for documents, or landing documents without a combined candidate: see decision 1.
- Documents ahead of all ordinary code with an eight hour age limit: it held priority 1 code for eight hours.
- Moving to GitHub's merge queue now: see decision 3.
- Making the age limit a setting now: nothing shows another project needs a different value.

## What a reviewer can check

- After the order fix lands, the queue's records show no two document candidates landing in a row while ordinary code waited, and no choice made by an eight hour rule.
- After documents land together, one landing records several document entries.
- Whether to design the move to GitHub's queue is decided from the daily wait figures in the queue's records, not estimated.