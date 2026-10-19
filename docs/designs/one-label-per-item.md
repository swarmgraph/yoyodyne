---
id: one-label-per-item
kind: design
title: At least one label per work item, from a configured list
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-19T03:06:52.04018Z
      reason: 'yoyodyne-ifd.433.43: the operator directed on October 18 that every work item carries exactly one label from a configurable list. This design says where the list lives, how filing enforces it, how lane and state labels sit beside it, the rules for the label for unsorted work, how existing items are labelled, and how a new project gets its list.'
    - action: amended
      by: architect
      at: 2026-10-19T06:58:50.604019Z
      reason: 'yoyodyne-ifd.433.46: the operator corrected his direction on October 18. Every item needs at least one label from the configured list, listed labels combine, `bug` stays a valid label, and the label for unsorted work is used only when nothing else fits. The design is rewritten to that rule: the lane prefix, the rename, the replace-on-change rule and the two-label count go.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-19T03:06:52.04018Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1236, for document-1236.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-19T06:58:50.604019Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1262, for document-1262.1 under the automatic approval policy
---
# At least one label per work item, from a configured list

It serves the goal "Run development nearly autonomously": labels already decide which developer slot prefers which work, and 72 of 267 open items carried none when this was first designed, so the scheduler was choosing with missing information and somebody had to sort work by hand. It must not break three other goals: configuration stays configurable without weakening safety, a newcomer gets a working first run from the readme alone, and what a person reads is in ordinary words.

The build work is the item that builds this rule (yoyodyne-ifd.433.44). Where that item's conditions say "exactly one", this design governs and the rule is "at least one".

## The decision: at least one listed label, and labels combine

The operator's rule, as corrected on October 18: every work item carries at least one label from the project's configured list. Any number of listed labels may be on one item; his example is `factory-flow` beside `bug` or `performance`. `bug` is a valid label. The label for unsorted work is used only when no other label fits.

An item's labels are of two kinds:

| Kind | How many | Who sets it | Where the names come from |
| --- | --- | --- | --- |
| Listed | at least one | the role filing the item; the harness also adds a program manager's lane label | the project's configured list |
| State | any | the roles the state's own design names | held in code; today only `needs-design` |

A program manager's lane label, such as `factory-flow`, is an ordinary listed label. The harness adds it to every item that program manager files, as it does today, and it counts toward "at least one". The program manager's authority check against its lane label is unchanged.

State labels are a closed list in code, beside the role contracts, so configuration can never add one and a listed label can never take a state's name. A state label does not count toward "at least one".

The reliability operating rule holds unchanged: work that blocks reliable execution carries `reliability`, whatever else it carries.

### Alternatives rejected

- **Exactly one label from the list.** This was the first version of this design. The operator withdrew it on October 18, because one item often belongs to more than one part of the product.
- **Prefixing lane labels, as `lane:factory-flow`.** The prefix existed only so a lane label would not count as a second area. With labels combining it separates nothing, and it would rename labels on every existing item for no gain.
- **Counting only the labels the filer chose, not the lane label the harness adds.** The lane label says something true about the item, and the operator's own example treats `factory-flow` as an ordinary label. Making a program manager repeat it would be a special case with no purpose.
- **A separate area field beside the labels.** Slots already prefer by label. A second field would be a second way to say what a label says.

## 1. Where the list lives

The list lives in `.yoyodyne/config.yaml` under `labels.area`. Each entry has a name and a meaning on a single line, and exactly one entry is marked `unsorted: true`:

```yaml
labels:
  area:
    - name: reliability
      meaning: the factory keeps running correctly
    - name: bug
      meaning: something that already exists does not work as it should
    - name: unsorted
      meaning: not yet sorted; say why when filing
      unsorted: true
```

Configuration validation refuses:

- a list with no entries, more than twelve, or a repeated name;
- a name that is not lowercase letters, digits and hyphens;
- a name that equals a state label held in code;
- a list with no entry, or more than one entry, marked `unsorted`;
- a configured program manager whose lane name is not on the list, so the label the harness adds is always a listed one.

A project with no `labels.area` uses the list `yoyo init` writes (section 7), supplied as a harness default, so an older configuration keeps working.

### A slot preference naming a label not on the list

This is refused when the configuration is changed, never when a service starts:

- `yoyo config validate`, `yoyo doctor`, and the role's typed configuration write refuse a slot preference naming a label that is not on the list, and name the list.
- When the scheduler or any service loads a configuration whose slot preference names such a label, it starts. It writes one warning in plain words naming the slot and the label, drops that one preference, and keeps every other.

One stale entry must never stop every run until a person edits a file.

## 2. Enforcement at the one filing path

Every role files work through the harness's own tracker write; no role runs the tracker's command. That write works in this order:

1. It takes the labels the filing names, and the parent's labels where section 3 applies.
2. Where a program manager files, it adds that program manager's lane label.
3. It refuses the item unless at least one of the resulting labels is on the list. The refusal lists every label with its meaning, so the role can file again at once.
4. It refuses the item if the unsorted label is there beside any other listed label, and says that unsorted is only for an item no other label fits.
5. It refuses a state label from a role whose design does not allow it, and a label that is neither listed nor a state label, naming the list.

Changing an item's labels through the same write:

- refuses removing the last listed label from an item, naming the list;
- removes the unsorted label in the same act when a listed label is added to an unsorted item, so an item is never both sorted and unsorted.

Adding a listed label never removes another.

## 3. Items split off a parent

An item filed with a parent takes all of the parent's listed labels when the filing names none. A filing that names at least one listed label keeps exactly what it names. A program manager's lane label is still added by the harness from whoever files.

## 4. The label for unsorted work

- Filing with the label marked `unsorted` needs a note saying why no other label fits. Without that note the filing is refused, and the refusal names the list.
- The Lead Product Manager gives each unsorted item a real label within 3 days of its filing, in her ordinary passes.
- An open item still unsorted after 3 days shows in the dashboard's list of what is broken, stalled, blocked or degraded, with its age and the Lead Product Manager as its owner, until it is labelled.

## 5. Interactive sessions

A person may still run the tracker's own command at their terminal. The notes guard the harness installs for interactive sessions warns, in plain words, when `bd create` makes an item with no label from the list, and names the list. It never refuses: the person's own tracker use is theirs.

An item filed that way with no listed label is counted on the dashboard, as in section 6, so it is found either way.

## 6. Labelling existing items

**Existing labels stay.** No label is renamed or removed. `bug`, `factory-flow` and `technical-health` stay as they are on every item.

**Open items are the Lead Product Manager's to label**, in her passes. She gives each open item with no listed label at least one. The open items with no listed label are counted on the dashboard until none remain.

**Closed items** are labelled once, in one batch, by a run on the cheapest configured model, so the history charts fill in. It adds labels only to closed items with no listed label, and each label it writes is marked on the item as written after closing. Getting one wrong costs only a chart, so it is not reviewed item by item. The development manager admits and schedules that run.

## 7. New projects and changing the list

`yoyo init` writes a small general list:

| Label | Meaning |
| --- | --- |
| `product` | new capabilities |
| `bug` | something that already exists does not work as it should |
| `reliability` | the product keeps working correctly |
| `performance` | speed and cost |
| `usability` | what a person sees and does |
| `technical-health` | code quality and how accurate the documents are |
| `unsorted` | not yet sorted; say why when filing |

The newcomer goal holds, because this list works unchanged and the readme need say nothing about it.

**The list is the Lead Product Manager's to decide.** In her first pass on a project, she fits the list to the brief and applies it through the typed configuration write of [section 6 of the configuration design](portable-agent-configuration.md#6-values-the-harness-recommends-applied-by-a-role). That write adds `labels.area` to its keys and names her as the one who decides it. The change lands through the reviewed run every such value takes, and is reported to the operator afterwards. Choosing names for labels changes what work is called, not what the goals admit, so no operator approval is asked for.

She revises the list later when unsorted items are more than a fifth of what was filed over two weeks, or when a label has had no item filed with it in 30 days. The write that changes the list refuses to remove a label while any open item, slot preference or configured lane carries it, and names the count, so items are relabelled before their label goes.

The operator's own starting list for this repository is the one in the design item for labels (yoyodyne-ifd.433.43): the init list above plus `factory-flow`, `team-development`, and `security` where wanted. The Lead Product Manager applies it the same way.

## 8. How role instructions show the list

The harness renders the configured list, each label with its meaning, into the briefing of every role that files work, at the place filing is described. It also states the rule in two sentences: at least one listed label, labels combine, and unsorted only when nothing else fits, with a note saying why. The list is never copied into a persona or a role contract, so a change to it reaches every role at its next turn with nothing else edited. The developer slots' preferences are shown beside it, so a role can see which labels a seat prefers.

## Not an invariant

This is a rule for one filing path that the harness enforces in code, and no change whose own work is correct could break it from outside that path. So it is recorded as a design and not as an invariant.

## What a reviewer checks

1. Filing with no listed label, or only labels not on the list, is refused, and the refusal lists every label with its meaning.
2. Filing with two or more listed labels, such as `factory-flow` and `bug`, is accepted with every label kept.
3. A program manager filing with `reliability` gets its lane label added and passes its authority check. A program manager filing with no label of its own is accepted, because its lane label is listed.
4. A child item filed with no listed label takes all its parent's listed labels. One naming a label keeps exactly what it names.
5. Filing as unsorted without a reason is refused. Filing unsorted beside another listed label is refused, including a program manager's filing. Adding a listed label to an unsorted item removes the unsorted label. An unsorted item 3 days old shows in the dashboard's list of problems with the Lead Product Manager as owner.
6. Removing the last listed label from an item is refused. Adding a listed label removes no other.
7. `yoyo config validate` accepts a slot preference naming `bug` when `bug` is listed, and refuses one naming a label not on the list. A scheduler started on a file holding such a preference starts, warns once naming the slot and label, and still applies every other preference.
8. A list with no unsorted entry, two of them, or the name `needs-design` is refused by validation, and so is a configured lane whose name is not on the list.
9. The typed configuration write accepts `labels.area` only from the Lead Product Manager. It refuses removing a label that an open item, slot preference or configured lane carries, naming the count.
10. No existing label is renamed or removed by the change.
11. A role's briefing shows the list from the configuration that applies at that turn. Changing the list changes the next briefing with no persona edited.
12. `bd create` with no listed label in an interactive session warns and creates the item.