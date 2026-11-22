---
id: work-item-size
kind: decision
title: 'How large a work item should be: 300 to 600 changed lines by default, set by each project inside bounds held in code'
supports:
    - operations-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-22T18:08:40.15378Z
      reason: 'yoyodyne-ifd.437.86: rules the operator''s November 22 direction on work item size: 300 to 600 changed lines by default, combine under 150, split over 1,200; four project settings inside bounds held in Go; the range reaches the three management roles from configuration, with how to use it in their Markdown instructions; no check refuses a change for its size, and landed sizes are recorded and counted weekly. Three build pieces.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-22T18:08:40.15378Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3496, for document-3496.1 under the automatic approval policy
---
# How large a work item should be

Decided by the architect on November 22, 2026, for the operator's direction of about 09:35 PST that day (yoyodyne-ifd.437.86). It serves the goal that spend follows the work: the model, the context and the number of provider calls a task costs are chosen for that task. It must not break the goals that keep roles and policies configurable without making safety optional, that support any language, that keep surfaces in plain words, and that every change is independently reviewed.

## Why

The operator's assistant measured 243 items landed from November 1 to 22. Each item costs about $4.76 before it does anything, plus about $0.60 per 100 changed lines. Cost per 100 lines falls from $12.83 under 50 lines to $1.32 at 300 to 600 lines. Items needing a repair rise from 26 to 41 percent below 600 lines to 72 to 74 percent above. The wait after first review is shortest at 300 to 600 lines (median 8.6 hours) and reaches 35.6 hours over 1,200. The operator has found the same range on other projects, so it ships as a default every project gets without asking.

## Decided

### 1. What is counted

A change's size is the lines added plus the lines deleted, as Git reports them for the change against its base, across every file it touches: code, tests, configuration and documents together. A binary file counts no lines. Nothing about a language is assumed, so this works for any project.

The range applies to work items a developer runs. A document published from a conversation, and an item carried in a role's conversation, are outside it.

### 2. The default range

| Setting | Default | Meaning |
| --- | --- | --- |
| `work_items.size.combine_below` | 150 | A piece expected under this is combined with a neighbour. |
| `work_items.size.target_low` | 300 | The range a planner aims for starts here. |
| `work_items.size.target_high` | 600 | The range a planner aims for ends here. |
| `work_items.size.split_above` | 1200 | A piece expected over this is split. |

A piece expected between the top of the range and the split point is allowed only when its item says why splitting it would leave a piece that cannot be checked on its own.

### 3. A project sets its own range, inside bounds held in Go

This follows [which limits stay in code](limits-held-in-code.md): a size that suits one project and not another is set by the project inside bounds held in code.

| Setting | Lowest | Highest |
| --- | --- | --- |
| `combine_below` | 50 | 300 |
| `target_low` | 100 | 1000 |
| `target_high` | 200 | 2000 |
| `split_above` | 400 | 4000 |

The four must also stand in that order, each smaller than the next. An unset value is its default. A value outside its bounds, or out of order, is refused when it is written and when the configuration loads, and the refusal names the setting, the value and the bound. The development manager may change the values through the typed write and reviewed path that [section 6 of the portable agent configuration design](../designs/portable-agent-configuration.md#6-values-the-harness-recommends-applied-by-a-role) gives every configurable value.

The range is planning advice. It does not replace [the limits on how large a change the reviewer is shown](review-size-limits.md), which still hold whatever range a project sets.

### 4. How the management roles are given it

The numbers are never written into instructions, so a project that changes its range edits no instructions.

- The turns of the Lead Product Manager, the development manager and the architect each carry one plain line stating the project's range and its combine and split points, rendered from configuration.
- Their instructions, kept as Markdown files in the repository as the operator directed on October 27, say how to use it. The architect sizes the build pieces in a design to it. The development manager estimates each piece's changed lines when she breaks out work, combines and splits by it, and writes the estimate into each item she creates. The Lead Product Manager admits work so that it can be broken into pieces of that size.

### 5. No check refuses a change for its size

A finished change outside the range is not refused, sent back, or charged a repair. Refusing it would throw away finished, reviewed work over a planning estimate.

Instead, what landed is made visible. When a run's change lands, the harness records its changed lines on the run's record and in a note on its item. Each week the count of landed items below the combine point, inside the range, between the range and the split point, and above the split point is shown beside the weekly figures the milestone goals track, so a person can see whether planning is hitting the range.

## Alternatives rejected

- **Refuse or return a change over the split point.** It spends a repair on work that is done and reviewed, and the review size limits already protect review.
- **Hold an item from starting when its estimate is over the split point.** Estimates are guesses; a hold on a guess would stop work that is fine.
- **Write the numbers into each role's instructions.** Every project that changes its range would then have to edit instructions, and the two would drift.
- **An invariant.** A change of the wrong size breaks nothing outside its own scope. This is how work is planned, so it is a decision.
- **Count code lines only, leaving out tests.** The operator's measure counts code and tests together, and tests are where much of a repair's work lands.

## Build pieces

Each is expected inside the default range.

1. **The four settings.** Read from configuration with the defaults and bounds above, refused when out of bounds or out of order on write and on load, naming the setting, value and bound, and joining the values section 6 lets the development manager apply. The configuration guide under docs/configuration that describes run settings gains the key, its defaults and its bounds. Tests cover the defaults, a set value, each bound and the order rule.
2. **The roles are told.** The three management roles' turns carry the one line from section 4, rendered from configuration, with a test that a changed value appears in it. Their shipped instructions, wherever the harness's setup keeps the copies it writes for every project, and this repository's copies under .yoyodyne/personas, say how to use the range as section 4 does. Where the harness requires a grant for a developer to change a file under .yoyodyne, the development manager gives it on the item.
3. **What landed is shown.** On landing, the changed lines are recorded on the run's record and in a note on the item. The weekly count by size is shown on status and the dashboard beside the milestone figures. Tests cover a landing in each of the four groups.