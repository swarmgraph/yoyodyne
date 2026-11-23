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
      at: 2026-11-23T16:43:56.057861Z
      reason: 'yoyodyne-ifd.429.291: measures the reviewer''s whole copy before checks with room kept for check results, holds the diff limit and the work item context to 512 KiB together so 256 KiB is left for the rest, bounds the status listing, diff stat and new file list, says which refusals go to the developer and which to the development manager, corrects what exists, and adds build piece 3. The configurable limits land first.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-31T16:12:14.538209Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2048, for document-2048.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-11-23T16:43:56.057861Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3691, for document-3691.1 under the automatic approval policy
---
# The limits on how large a change the reviewer is shown

It serves the goal "Every change is reviewed against the intent it claims to serve, by a role that did not write it, before it is integrated", and must not break "Keep roles, policies, and provider selection configurable without making safety invariants optional" or "Run development nearly autonomously".

## What exists

Read at main 4e6572cd9d0c.

The reviewer is shown a copy of the change with three limits: 256 KiB of diff in all, 64 KiB for one new file, and 200 new files. Source files are placed first, then tests, then test data and generated files. A source or test file the limits keep out refuses an approval; test data kept out does not, where it is listed with its size and digest. Everything the reviewer is shown together is held to 768 KiB.

Before any check runs, the size check (`internal/orchestrator/reviewbound.go`) measures the change the way that copy is rendered. The developer is told the three limits in the developer's contract. The first refusal on a run goes back to the developer with each file kept out and its size, and spends one repair attempt. A second refusal stops the run for the development manager: nothing is checked or reviewed, and the branch and worktree are kept. A run that publishes a document is not measured by it.

The size check measures only the change's files. The reviewer's copy (`internal/review/reviewer.go`) also holds these parts:

- the reviewer's contract, the architectural invariants, and the work item context;
- the claimed landing outcome, the developer's record of what was run, and the developer's summaries, earlier attempts included;
- the records of the developer's tool calls, held to 64 KiB;
- the status listing, the diff stat, the list of new files, the list of files in the change, the files kept out, the files removed, and the commits;
- the repository listing and the copies of project files;
- the results of the forge's checks and of this machine's checks, and the tests that failed and then passed.

The status listing and the diff stat have no limit. The developer of the configurable limits (yoyodyne-ifd.429.116) measured that at today's 256 KiB default the copy can pass 768 KiB: about 877 KiB for a change of about 1,100 small files, and about 791 KiB for a change of one file at the full diff limit. Such a run passes its size check and its checks and is then stopped before review.

## Correcting this ruling's arithmetic

The first revision compared the highest diff limit with the item, the intent and the rules, and left out every other part listed above. The one file figure shows the cost: 791 KiB less a diff of 256 KiB is about 535 KiB for the rest at its largest. The breakdown of that figure was not read for this revision. Work item context is limited to 256 KiB today, so about 279 KiB of the 535 KiB would be the other parts if that limit is full, and the two together are more than the total allows. This is why decision 4 measures the copy a run really has, rather than trusting that every part at its largest would fit.

## Decided

### 1. A project may set the three limits, inside bounds the harness holds

The three limits on the change join the numeric limits the development manager may apply under [section 6 of the portable agent configuration design](../designs/portable-agent-configuration.md#6-values-the-harness-recommends-applied-by-a-role), by the same typed write and reviewed path. The bounds are held in Go beside the limits themselves, and no configuration can move them:

| Limit | Default | Lowest | Highest |
| --- | --- | --- | --- |
| diff in all | 256 KiB | 64 KiB | 512 KiB |
| one new file | 64 KiB | 16 KiB | 128 KiB |
| number of new files | 200 | 50 | 400 |

A value outside these bounds is refused when it is written and when the configuration loads, naming the limit and the bound.

The 768 KiB limit on everything the reviewer is shown together is not configurable. The diff limit and the work item context limit may come to at most 512 KiB together, which leaves 256 KiB for every other part. A pair that comes to more is refused when either is written and when the configuration loads, naming both values and the 512 KiB. Until the work item context limit is configurable ([limits held in code](limits-held-in-code.md)), it counts as its fixed 256 KiB. Each setting keeps its own bounds; this is a further rule on the two together. So today the most a project may set for the diff is 256 KiB, its default, and a project that needs a larger diff limit lowers its work item context once that is configurable.

Whatever the limits, every source and test file the reviewer approves over is shown to the reviewer whole; nothing here lets a review approve a file it was not shown.

### 2. An oversized change goes back to its developer once

The developer is told the three limits that apply to the run when the run starts, beside the other things the developer must verify before submitting.

The first time the size check refuses a run because of its change, the run goes back to its developer with the files that were kept out, each with its size and the limit it was compared against. Where the whole copy is what is too large, it names the size of the copy, the largest files in the change, and the 768 KiB limit. That return spends one repair attempt, as a failed check does. A change can often be made to fit: a generated or vendored file left out, a fixture recorded as test data, the work done with a smaller edit. The developer may also answer that it cannot be made smaller.

The second refusal on the same run stops the run for the development manager: nothing checked, nothing reviewed, branch and worktree kept, the files and sizes on the item.

### 3. No run is given larger limits of its own

The limits are the project's, not the run's. Neither the development manager nor any other role raises them for one run. Raising them is a change to the project's configuration through section 6, reviewed like any change, and it applies to runs that start after it lands. A run stopped by the size check may then be run again under the new limits by the development manager's ordinary decision.

### 4. The size check measures the whole copy before any check runs

The size check builds the reviewer's copy with the same code that builds it for the reviewer, and compares it with 768 KiB less 64 KiB kept for the parts that do not exist yet. Those are the forge's check results, this machine's check results, and the tests that failed and then passed. Any other part not yet recorded when the size check runs comes out of the same 64 KiB.

Once checks have run, the parts kept for are held to that 64 KiB together. Past it, quoted check output is cut first, oldest check first; each check's command and result is always shown, and each cut says how much was left out and where the whole is. They never make a copy too large.

The repository listing and the copies of project files are not counted. They take whatever room is left, as they do today, and are cut to it, saying so.

So no run passes the size check and its checks and is then stopped before the reviewer is asked because its copy is too large.

Who is handed a refusal of the whole copy depends on the cause:

- If the parts that do not come from the change, with the 64 KiB kept back, pass 768 KiB less the project's diff limit, no smaller change is asked for. The run stops for the development manager, naming the largest of those parts and their sizes, and no repair attempt is spent.
- Otherwise the change is the cause, and decision 2 applies.

This part of the size check covers every run a review follows. For a run that publishes a document, the three limits on files still do not apply, and a copy that is too large goes back to the document's owner as any document run that did not land does. A merge queue candidate that is reviewed again is measured by the same code when it is built, and one that is too large is handed back as this decision says.

### 5. The lists that describe a change are limited

The status listing, the diff stat and the list of new files are held to 32 KiB together. They are cut in this order: the list of new files, then the diff stat, then the status listing. Each cut says how many entries were left out. The limit is held in Go and is not configurable. The list of files in the change keeps its limit of 1,000 paths.

These lists help a reviewer see the shape of a change. What the reviewer approves is the patch, which is whole, and the list of files kept out, which is never cut, because an approval must account for each of them. A cut list proves only that the paths it shows are there; it says nothing about a path it does not show.

### 6. What arrives whole, and the order of the work

These parts are never cut to make a copy fit: the reviewer's contract, the invariants, the work item context with the intent it carries, the source and test files in the patch, the files kept out and the files removed. The parts held to a size of their own, such as the developer's summaries and the records of tool calls, keep that size. The rest are cut only as decisions 4 and 5 say.

The configurable limits (yoyodyne-ifd.429.116) land first, with the 512 KiB rule on the two settings together. They leave every default as it is and, until the work item context limit can be lowered, allow no larger diff than today, so no copy grows because of them. The largest copy this repository has really sent was not measured, and the order does not rest on it. The test in that change that measures the copy at its largest stays as a measurement that reports its figures; it is not required to fit 768 KiB until build piece 3 lands.

Until build piece 3 lands, the development manager raises no diff limit above its default.

## Rejected

- **Leaving the limits fixed.** A project whose ordinary changes are larger than these defaults, such as one with large generated sources or long test tables, would have every such change stopped, and only splitting would move it. Section 6 exists so a value like this is decided by a role rather than pasted by the operator.
- **No upper bound.** The limits are what keeps independent review meaningful: a reviewer handed more than one session can read whole is approving what it skimmed. The upper bound is held in code so no configuration can trade review away for throughput.
- **A fixed limit for every part, adding up to 768 KiB.** Parts are added to the copy over time, and several are small on nearly every run and large on a few. Limits low enough to add up would cut or refuse runs that fit. Measuring the real copy refuses only what does not fit.
- **Raising the fixed 768 KiB total.** It is what one reviewer session is trusted to read whole; raising it to fit parts without limits moves the problem.
- **Measuring only the change and catching the rest when the copy is built.** That is today's behaviour, and it spends a full set of checks on a run that cannot be reviewed.
- **Sending a copy that is too large because of the item back to the developer.** The developer cannot shorten the item, its intent or the invariants.
- **Showing the reviewer a large change in parts across several sessions.** No session would see the whole change, and an approval made of partial approvals is not a review of the change that lands.
- **A larger limit for one run.** It would make the limit a decision made per change, under pressure to land that change, which is the decision the limit exists to take out of the moment.
- **Sending every refusal straight to the development manager.** It spends a manager decision and usually a split on changes the developer could have trimmed, and the developer is never told why.
- **An invariant.** The constraint that matters, that a change is reviewed whole by a role that did not write it, is already held by the goal and by the rule that integration needs evidence bound to the reviewed revision. This decision only says how large that review may be and who sets it.

## What has to be built

1. The three limits become configuration with the bounds above held in Go, read by the code that renders the reviewer's copy, by the size check and by the developer's contract; the 768 KiB limit stays fixed; the diff limit and the work item context limit together are refused past 512 KiB, when written and when the configuration loads; the limits join the keys section 6 lets the development manager apply. This is the configurable limits item (yoyodyne-ifd.429.116).
2. Built: the developer is told the limits at the start of a run; the first refusal by the size check returns the run to its developer with the files kept out and spends one repair attempt; the second stops the run for the development manager.
3. The size check builds the whole copy and compares it with 768 KiB less 64 KiB; the parts that exist only after checks are held to 64 KiB together as decision 4 says; a refusal goes where decision 4 says; the lists in decision 5 are held to 32 KiB. Tests: a change of about 1,100 small files, and a change of one file at the full diff limit, are each either reviewed or refused before checks, never stopped after them; a run whose item and invariants alone are too large stops for the development manager and spends no repair attempt; check output past 64 KiB is cut with each check's result still shown; each cut list says what it left out; a document run that is too large goes back to its owner. The operations guide says all of this.
