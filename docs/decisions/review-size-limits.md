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
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-31T16:12:14.538209Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2048, for document-2048.1 under the automatic approval policy
---
# The limits on how large a change the reviewer is shown

It serves the goal "Every change is reviewed against the intent it claims to serve, by a role that did not write it, before it is integrated", and must not break "Keep roles, policies, and provider selection configurable without making safety invariants optional" or "Run development nearly autonomously".

## What exists

The reviewer is shown a copy of the change with three limits: 256 KiB of diff in all, 64 KiB for one file, and 200 files. Everything the reviewer is shown together, the change and the intent and rules around it, is held to 768 KiB. Before any check runs, the harness measures the change the way that copy is rendered (`internal/orchestrator/reviewbound.go`). If a source or test file would be kept out of the copy, the run stops: no check runs, no review round is spent, the branch and worktree are kept, and the item goes to the development manager, most often to be split. The developer is not told, because the limits are fixed and the developer cannot change them.

The figures above are from the work item that asked for this ruling; the files that hold them (`internal/gitworktree/manager.go` and `internal/review/reviewer.go`) were not read for it. The size check was read at main 02f0fb9e.

## Decided

### 1. A project may set the three limits, inside bounds the harness holds

The three limits on the reviewer's copy join the numeric limits the development manager may apply under [section 6 of the portable agent configuration design](../designs/portable-agent-configuration.md#6-values-the-harness-recommends-applied-by-a-role), by the same typed write and reviewed path. The bounds are held in Go beside the limits themselves, and no configuration can move them:

| Limit | Default | Lowest | Highest |
| --- | --- | --- | --- |
| diff in all | 256 KiB | 64 KiB | 512 KiB |
| one file | 64 KiB | 16 KiB | 128 KiB |
| number of files | 200 | 50 | 400 |

A value outside these bounds is refused when it is written and when the configuration loads, naming the limit and the bound.

The 768 KiB limit on everything the reviewer is shown together is not configurable. A diff limit is refused when, with it, the rest of the review evidence (the item, the intent it serves, the rules and invariants delivered to the reviewer) would no longer fit whole beside a diff of that size. The highest diff limit is therefore the lesser of 512 KiB and what that leaves room for. Whatever the limits, every source and test file the reviewer approves over is shown to the reviewer whole; nothing here lets a review approve a file it was not shown.

### 2. An oversized change goes back to its developer once

The developer is told the three limits that apply to the run when the run starts, beside the other things the developer must verify before submitting.

The first time the size check refuses a run's change, the run goes back to its developer with the files that were kept out, each with its size and the limit it was compared against, the same lines the refusal already writes. That return spends one repair attempt, as a failed check does. A change can often be made to fit: a generated or vendored file left out, a fixture recorded as test data, the work done with a smaller edit. The developer may also answer that it cannot be made smaller.

The second refusal on the same run stops the run for the development manager exactly as every refusal does today: nothing checked, nothing reviewed, branch and worktree kept, the files and sizes on the item.

### 3. No run is given larger limits of its own

The limits are the project's, not the run's. Neither the development manager nor any other role raises them for one run. Raising them is a change to the project's configuration through section 6, reviewed like any change, and it applies to runs that start after it lands. A run stopped by the size check may then be run again under the new limits by the development manager's ordinary decision.

## Rejected

- **Leaving the limits fixed.** A project whose ordinary changes are larger than these defaults, such as one with large generated sources or long test tables, would have every such change stopped, and only splitting would move it. Section 6 exists so a value like this is decided by a role rather than pasted by the operator.
- **No upper bound.** The limits are what keeps independent review meaningful: a reviewer handed more than one session can read whole is approving what it skimmed. The upper bound is held in code so no configuration can trade review away for throughput.
- **Showing the reviewer a large change in parts across several sessions.** No session would see the whole change, and an approval made of partial approvals is not a review of the change that lands.
- **A larger limit for one run.** It would make the limit a decision made per change, under pressure to land that change, which is the decision the limit exists to take out of the moment.
- **Sending every refusal straight to the development manager, as today.** It spends a manager decision and usually a split on changes the developer could have trimmed, and the developer is never told why.
- **An invariant.** The constraint that matters, that a change is reviewed whole by a role that did not write it, is already held by the goal and by the rule that integration needs evidence bound to the reviewed revision. This decision only says how large that review may be and who sets it.

## What has to be built

1. The three limits become configuration with the bounds above held in Go, read by the code that renders the reviewer's copy and by the size check; the 768 KiB limit stays fixed; a diff limit that would leave the rest of the evidence less than whole is refused, with a test that measures it against the largest review evidence this repository has sent; the limits join the keys section 6 lets the development manager apply.
2. The developer is told the limits at the start of a run; the first refusal by the size check returns the run to its developer with the files kept out and spends one repair attempt; the second stops the run for the development manager as today; tests for both; the operations guide says so.
