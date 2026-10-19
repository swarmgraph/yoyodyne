---
id: one-label-per-item
kind: design
title: One area label per work item, from a configured list
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-19T03:06:52.04018Z
      reason: 'yoyodyne-ifd.433.43: the operator directed on October 18 that every work item carries exactly one label from a configurable list. This design says where the list lives, how filing enforces it, how lane and state labels sit beside it, the rules for the label for unsorted work, how existing items are labelled, and how a new project gets its list.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-19T03:06:52.04018Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1236, for document-1236.1 under the automatic approval policy
---
# One area label per work item, from a configured list

It serves the goal "Run development nearly autonomously": labels already decide which developer slot prefers which work, and 72 of 267 open items carry none, so the scheduler is choosing with missing information and somebody has to sort work by hand. It must not break three other goals: configuration stays configurable without weakening safety, a newcomer gets a working first run from the readme alone, and what a person reads is in ordinary words.

The build work is the item the Lead Product Manager admitted beside the design item (yoyodyne-ifd.433.43) to build one label per work item; this design is what that item builds.

## The decision: three kinds of label, and exactly one of the first kind

"Exactly one label" cannot mean one label of any kind. Two kinds of label already carry rules:

- The harness adds a program manager's lane label, such as `factory-flow`, to every item that program manager files, and checks the program manager's authority against it.
- The operating rules require work that blocks reliable execution to carry `reliability`, so the developer seat kept for reliability work prefers it.

A filing check that refused a second label would refuse every program manager's filing. So an item's labels are of three kinds, kept apart by their names:

| Kind | How many | Who sets it | Written as |
| --- | --- | --- | --- |
| Area | exactly one | the role filing the item, from the configured list | the bare name, such as `reliability` |
| Lane | none or one | the harness alone, when a program manager files | `lane:` and the lane's name, such as `lane:factory-flow` |
| State | any | the roles the state's own design names | names held in code; today only `needs-design` |

The area label names the part of the product the work improves. It is the label the operator asked for, and the only kind a developer slot's preference may name.

Lane labels take the `lane:` prefix because `factory-flow` and `technical-health` are both area names and lane names. A factory-flow program manager must be able to file an item whose area is `reliability`, and without the prefix that item would look like it had two area labels.

State labels are a closed list in code, beside the role contracts, so configuration can never add one and a configured area can never take a state's name.

The reliability operating rule holds unchanged: work that blocks reliable execution has `reliability` as its area. Nothing in the operating rules needs rewording.

### Alternatives rejected

- **One label of any kind.** This breaks program manager authority and the reliability seat, as above.
- **A separate area field beside the labels.** Slots already prefer by label. A second field would be a second way to say what a label says, and every reader of slot preferences would have to learn it.
- **Unprefixed lane labels with reserved names.** The names already collide with areas the operator wants, so reserving them would take those areas away.

## 1. Where the list lives

The list lives in `.yoyodyne/config.yaml` under `labels.area`. Each entry has a name and a meaning of a single line, and exactly one entry is marked `unsorted: true`:

```yaml
labels:
  area:
    - name: reliability
      meaning: the factory keeps running correctly
    - name: unsorted
      meaning: not yet sorted; say why when filing
      unsorted: true
```

Configuration validation refuses a list with:

- no entries, more than twelve, or a repeated name;
- a name that is not lowercase letters, digits and hyphens;
- a name that begins with `lane:`, or equals a state label held in code;
- no entry, or more than one entry, marked `unsorted`.

A project with no `labels.area` uses the list `yoyo init` writes (section 7), supplied as a harness default, so an older configuration keeps working.

### A slot preference naming a label not on the list

This is refused when the configuration is changed, never when a service starts:

- `yoyo config validate`, `yoyo doctor`, and the role's typed configuration write refuse a slot preference naming a label that is not an area on the list, and name the list.
- When the scheduler or any service loads a configuration whose slot preference names such a label, it starts. It writes one warning in plain words naming the slot and the label, drops that one preference, and keeps every other.

One stale entry must never stop every run until a person edits a file.

## 2. Enforcement at the one filing path

Every role files work through the harness's own tracker write; no role runs the tracker's command. That write refuses a new item unless it carries exactly one area label from the list. The refusal lists every area with its meaning, so the role can file again at once.

The same write:

- refuses a lane label a role supplied, because only the harness sets one;
- refuses a state label from a role its own design does not allow.

Changing an item's area through the same write replaces the old area label in the same act, so an item never holds two.

The harness adds the lane label after the area check passes, from the program manager's configured lane. It checks the program manager's authority against `lane:<name>` and nothing else.

## 3. Items split off a parent

An item filed with a parent takes the parent's area label when the filing names none. A filing that names an area keeps the one it names. The lane label is never inherited; the harness sets it from whoever files.

## 4. The label for unsorted work

- Filing with the area marked `unsorted` needs a note saying why no other area fits. Without that note the filing is refused, and the refusal names the list.
- The Lead Product Manager gives each unsorted item a real area within 3 days of its filing, in her ordinary passes.
- An open item still unsorted after 3 days shows in the dashboard's list of what is broken, stalled, blocked or degraded, with its age and the Lead Product Manager as its owner, until it is relabelled.

## 5. Interactive sessions

A person may still run the tracker's own command at their terminal. The notes guard the harness installs for interactive sessions warns, in plain words, when `bd create` makes an item with no area label from the list, and names the list. It never refuses: the person's own tracker use is theirs.

The item then shows as unsorted on the dashboard after 3 days, as in section 4, so it is found either way.

## 6. Labelling existing items

**Lane labels are renamed once.** In the same change that moves the authority check to `lane:<name>`, the harness renames, on every item, a lane label it added to the prefixed form. An item's bare `factory-flow` or `technical-health` that the harness did not add is left as an area label. The rename is recorded on each item as a note saying the harness renamed the lane label.

**`bug` is retired as a label**, because the item's type already says bug. The relabelling removes it, and no slot preference may name it.

**Open items are the Lead Product Manager's to label**, in her passes. She gives each open item exactly one area and removes any second one. The open items left with no area or two areas are counted on the dashboard until none remain.

**Closed items** are labelled once, in one batch, by a run on the cheapest configured model, so the history charts fill in. Each label it writes is marked on the item as written after closing. Getting one wrong costs only a chart, so it is not reviewed item by item. The development manager admits and schedules that run.

## 7. New projects and changing the list

`yoyo init` writes a small general list:

| Area | Meaning |
| --- | --- |
| `product` | new capabilities |
| `reliability` | the product keeps working correctly |
| `performance` | speed and cost |
| `usability` | what a person sees and does |
| `technical-health` | code quality and how accurate the documents are |
| `unsorted` | not yet sorted; say why when filing |

The newcomer goal holds, because this list works unchanged and the readme need say nothing about it.

**The list is the Lead Product Manager's to decide.** In her first pass on a project, she fits the list to the brief and applies it through the typed configuration write of [section 6 of the configuration design](portable-agent-configuration.md#6-values-the-harness-recommends-applied-by-a-role). That write adds `labels.area` to its keys and names her as the one who decides it. The change lands through the reviewed run every such value takes, and is reported to the operator afterwards. Choosing names for areas changes what work is called, not what the goals admit, so no operator approval is asked for.

She revises the list later when unsorted items are more than a fifth of what was filed over two weeks, or when an area has had no item filed in 30 days. The write that changes the list refuses to remove an area while any open item or slot preference carries it, and names the count, so items are relabelled before their area goes.

The operator's own starting list for this repository is the one in yoyodyne-ifd.433.43. It is the init list above plus `factory-flow`, `team-development`, and `security` where wanted, and the Lead Product Manager applies it the same way.

## 8. How role instructions show the list

The harness renders the configured list, each area with its meaning, into the briefing of every role that files work, at the place filing is described. It also states the three kinds of label and the unsorted rule in two sentences. The list is never copied into a persona or a role contract, so a change to it reaches every role at its next turn with nothing else edited. The developer slots' preferences are shown beside it, so a role can see which areas a seat prefers.

## Not an invariant

This is a rule for one filing path that the harness enforces in code, and no change whose own work is correct could break it from outside that path. So it is recorded as a design and not as an invariant.

## What a reviewer checks

1. Filing with no area label, two area labels, or an area not on the list is refused, and the refusal lists every area with its meaning.
2. A program manager filing with area `reliability` gets `lane:<its lane>` added by the harness and passes its authority check. A role supplying `lane:` itself is refused.
3. A child item filed with no area takes its parent's area. One naming an area keeps it.
4. Filing as unsorted without a reason is refused. An unsorted item 3 days old shows in the dashboard's list of problems with the Lead Product Manager as owner.
5. `yoyo config validate` refuses a slot preference naming `bug`. A scheduler started on a file holding that preference starts, warns once naming the slot and label, and still applies every other preference.
6. A list with no unsorted entry, two of them, a name beginning `lane:`, or the name `needs-design` is refused by validation.
7. The typed configuration write accepts `labels.area` only from the Lead Product Manager. It refuses removing an area that an open item carries, naming the count.
8. After the lane rename, no item carries a harness lane label without the prefix, and each renamed item has the rename note.
9. A role's briefing shows the list from the configuration that applies at that turn. Changing the list changes the next briefing with no persona edited.
10. `bd create` with no area in an interactive session warns and creates the item.