---
id: review-size-limits
kind: decision
title: 'The limits on how large a change the reviewer is shown: set by a project inside fixed bounds, and returned to the developer once'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-31T16:12:14.538209Z
      reason: 'Rules the work item asking whether the reviewer''s size limits may be configured (yoyodyne-a5vm): set by the development manager inside bounds held in code, the total review limit fixed, an oversized change returned to its developer once before it goes to the development manager, and no larger limit for a single run.'
    - action: amended
      by: architect
      at: 2026-11-23T11:09:22.896745Z
      reason: 'yoyodyne-ifd.429.291: every part of the reviewer''s copy gets its own limit held in code, the limits add up within the fixed 768 KiB, the highest diff limit falls from 512 to 448 KiB, and the size check measures the whole copy so an oversized copy goes back to its developer before checks. Corrects the ruling''s own arithmetic: the parts beside the diff reach about 535 KiB, which it never added. Adds build piece 3; the configurable limits (429.116) land first.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-31T16:12:14.538209Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2048, for document-2048.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-11-23T11:09:22.896745Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3661, for document-3661.1 under the automatic approval policy
---
# The limits on how large a change the reviewer is shown

It serves the goal "Every change is reviewed against the intent it claims to serve, by a role that did not write it, before it is integrated", and must not break "Keep roles, policies, and provider selection configurable without making safety invariants optional" or "Run development nearly autonomously".

## What exists

The reviewer is shown a copy of the change with three limits: 256 KiB of diff in all, 64 KiB for one file, and 200 files. Everything the reviewer is shown together, the change and the intent and rules around it, is held to 768 KiB. Before any check runs, the harness measures the change the way that copy is rendered (`internal/orchestrator/reviewbound.go`). If a source or test file would be kept out of the copy, the run stops: no check runs, no review round is spent, the branch and worktree are kept, and the item goes to the development manager, most often to be split.

The size check measures the diff only. The rest of the copy is not measured until the copy is built for the reviewer, after the checks have passed. The developer of the configurable limits (yoyodyne-ifd.429.116) measured, in a test in `internal/review`, that at today's 256 KiB default the whole copy can pass 768 KiB: about 877 KiB for a change of about 1,100 small files, because the file listing and the summary of changes have no limit, and about 791 KiB for a one-file change at the full diff limit with every other part at its largest. Such a run passes the size check and its checks and is then stopped before the reviewer is asked.

## Correcting this ruling's arithmetic

The first revision of this ruling said the highest diff limit is the lesser of 512 KiB and whatever room the rest of the review evidence leaves, and assumed that room was large. It never added the rest up. The one-file figure shows what it comes to: 791 KiB less a 256 KiB diff leaves about 535 KiB for everything beside the diff at its largest, with the file listing only a small part of that for a change of one file. Beside 535 KiB, the fixed total leaves about 233 KiB for the diff, below today's default, so the 512 KiB highest bound could never have held. The figures for each part are in that test's output on the developer's run and were not read for this revision; the correction rests on the totals, which are enough to show the parts were never bounded together.

## Decided

### 1. A project may set the three diff limits, inside bounds the harness holds

The three limits on the diff join the numeric limits the development manager may apply under [section 6 of the portable agent configuration design](../designs/portable-agent-configuration.md#6-values-the-harness-recommends-applied-by-a-role), by the same typed write and reviewed path. The bounds are held in Go beside the limits themselves, and no configuration can move them:

| Limit | Default | Lowest | Highest |
| --- | --- | --- | --- |
| diff in all | 256 KiB | 64 KiB | 448 KiB |
| one file | 64 KiB | 16 KiB | 128 KiB |
| number of files | 200 | 50 | 400 |

A value outside these bounds is refused when it is written and when the configuration loads, naming the limit and the bound. The highest diff limit was 512 KiB in the first revision; it is 448 KiB because of decision 2.

### 2. Every other part of the reviewer's copy has its own limit, and the limits add up within 768 KiB

The 768 KiB limit on everything the reviewer is shown together is not configurable. Every part of the copy beside the diff has a limit held in Go, not configurable:

| Part | Limit | What happens past it |
| --- | --- | --- |
| the file listing | 16 KiB | cut, saying how many files were left out of the listing |
| the summary of changes (lines added and removed per file) | 16 KiB | cut, saying how many files were left out of the summary |
| earlier developer summaries on the run | 4 KiB each, the latest five | each cut, saying so; older ones named as left out |
| records of tool calls shown to the reviewer | 32 KiB | cut, saying how many were left out |
| the item, the intent it serves, and the rules and invariants delivered to the reviewer | 192 KiB | never cut; see below |
| the reviewer's own instructions and the text around the parts | 32 KiB | never cut; a test holds the shipped text under it |

These come to 308 KiB, leaving 460 KiB of the fixed total. The highest diff limit is 448 KiB, leaving 12 KiB to spare. A test adds the limits of every part to the highest diff limit and fails if the sum passes 768 KiB, so a part added later, or a limit raised, cannot quietly break the total.

The file listing and the summary of changes are what a reviewer uses to see the shape of a change, not what it approves; the files it approves over are in the diff. Cutting them, with the count left out stated, loses no file the reviewer approves.

The intent and rules are never cut, because a review against part of the intent is not a review against the intent. If the item, its intent and its rules together pass 192 KiB, the run stops for the development manager before any check runs, naming the part that is too large and its size. That is a fault in what the item carries, not in the change, so it is not sent back to the developer.

Whatever the limits, every source and test file the reviewer approves over is shown to the reviewer whole; nothing here lets a review approve a file it was not shown.

### 3. The size check measures the whole copy, before any check runs

The size check renders the whole reviewer's copy, every part under its limit, and compares the total with 768 KiB, as well as the diff with its three limits. A run whose copy would be too large is caught there, before any check runs, and is handled by decision 4. No run passes the size check and its checks and is then stopped before the reviewer is asked; the copy the size check measured is the copy the reviewer is sent, built by the same code.

### 4. An oversized change goes back to its developer once

The developer is told the three diff limits that apply to the run when the run starts, beside the other things the developer must verify before submitting.

The first time the size check refuses a run's change, the run goes back to its developer with the files that were kept out, each with its size and the limit it was compared against, the same lines the refusal already writes. That return spends one repair attempt, as a failed check does. A change can often be made to fit: a generated or vendored file left out, a fixture recorded as test data, the work done with a smaller edit. The developer may also answer that it cannot be made smaller.

The second refusal on the same run stops the run for the development manager: nothing checked, nothing reviewed, branch and worktree kept, the files and sizes on the item.

### 5. No run is given larger limits of its own

The limits are the project's, not the run's. Neither the development manager nor any other role raises them for one run. Raising them is a change to the project's configuration through section 6, reviewed like any change, and it applies to runs that start after it lands. A run stopped by the size check may then be run again under the new limits by the development manager's ordinary decision.

### 6. The order of the work

The configurable limits (yoyodyne-ifd.429.116) land first. That change keeps the default diff limit at 256 KiB, so no copy the reviewer is sent grows because of it; the overflow it measured exists today at the fixed limits. The largest real copy this repository has sent was not measured for this revision, and the order does not depend on it.

Until build piece 3 lands, the development manager raises no diff limit above its default, because until then nothing holds the other parts within the total.

## Rejected

- **Leaving the limits fixed.** A project whose ordinary changes are larger than these defaults, such as one with large generated sources or long test tables, would have every such change stopped, and only splitting would move it. Section 6 exists so a value like this is decided by a role rather than pasted by the operator.
- **No upper bound.** The limits are what keeps independent review meaningful: a reviewer handed more than one session can read whole is approving what it skimmed. The upper bound is held in code so no configuration can trade review away for throughput.
- **Keeping 512 KiB as the highest diff limit and cutting the other parts harder to fit it.** The parts that would have to shrink are the intent, the rules and the reviewer's instructions, which are never cut. The diff limit is the one that gives way.
- **Raising the fixed 768 KiB total.** It is set by what one reviewer session can read whole; raising it to fit unbounded parts would move the problem, not solve it.
- **Measuring only the diff and catching the rest when the copy is built.** That is today's behaviour, and it spends a full set of checks on a run that cannot be reviewed.
- **Showing the reviewer a large change in parts across several sessions.** No session would see the whole change, and an approval made of partial approvals is not a review of the change that lands.
- **A larger limit for one run.** It would make the limit a decision made per change, under pressure to land that change, which is the decision the limit exists to take out of the moment.
- **Sending every refusal straight to the development manager.** It spends a manager decision and usually a split on changes the developer could have trimmed, and the developer is never told why.
- **An invariant.** The constraint that matters, that a change is reviewed whole by a role that did not write it, is already held by the goal and by the rule that integration needs evidence bound to the reviewed revision. This decision only says how large that review may be and who sets it.

## What has to be built

1. The three diff limits become configuration with the bounds above held in Go, read by the code that renders the reviewer's copy and by the size check; the 768 KiB limit stays fixed; the limits join the keys section 6 lets the development manager apply. This is the configurable limits item (yoyodyne-ifd.429.116), and its highest diff bound becomes 448 KiB when piece 3 lands.
2. The developer is told the limits at the start of a run; the first refusal by the size check returns the run to its developer with the files kept out and spends one repair attempt; the second stops the run for the development manager; tests for both; the operations guide says so.
3. Every part of the reviewer's copy beside the diff gets the limit in decision 2, held in Go, each cut saying what it left out; the item, intent and rules are never cut and a run whose context passes 192 KiB stops for the development manager naming the part; the highest diff bound becomes 448 KiB; the size check renders the whole copy with the same code that builds it for the reviewer and compares it with 768 KiB before any check runs; a test adds every part's limit to the highest diff limit and fails past 768 KiB; a test builds a change of about 1,100 small files and a one-file change at the highest diff limit and shows each is either within 768 KiB or refused by the size check, never stopped after its checks.