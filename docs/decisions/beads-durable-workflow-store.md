---
id: beads-durable-workflow-store
kind: decision
title: Beads is the durable workflow store
status: active
revisions:
    - action: created
      by: architect
      at: 2026-08-17T00:00:00Z
      reason: identity added with the artifact metadata schema; the record itself is unchanged
    - action: amended
      by: architect
      at: 2026-08-19T00:00:00Z
      reason: drafting residue removed — a stray code fence and trailing rule, and the offer of ratification the architect has since given; the decision itself is unchanged
    - action: amended
      by: architect
      at: 2026-10-15T14:38:02.554689Z
      reason: 'Follows the landed design for working alongside other contributors, which names this record as gaining a consequence once it landed: who may work an item is decided across machines by the claim reference on the shared remote, while the tracker still records the holder; reports, their handling, directives and spends travel in the records reference beside the tracker, not in it; and neither reference is a repository file, so the record''s rule against workflow state in repository files still holds.'
    - action: amended
      by: architect
      at: 2026-11-07T08:54:36.553212Z
      reason: Follows the design for working alongside other contributors as revised on November 7, 2026. The export travels through refs/yoyodyne/tracker rather than a commit on the target branch, and the harness merges it item by item. Written again because the reviewer of the first writing found that it claimed the merge keeps nothing but what it needs to compare versions, while section 7 of that design keeps a hold on a conflicting item in the machine's own state. This revision names both as kept outside Beads and narrows the decision's statement about blockers to match; no other part of the decision changes.
approvals:
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-10-15T14:38:02.554689Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1019, for document-1019.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 3
      by: harness
      at: 2026-11-07T08:54:36.553212Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2426, for document-2426.1 under the automatic approval policy
---
# Beads is the durable workflow store

**Status:** Accepted. This decision was made and acted on during the v1 design;
it was stated in [the v1 goals](../product/goals/v1-goals.md) until it was
moved here, because it is a decision about how Yoyodyne is built rather than
an outcome the product is trying to reach. Revised on October 15, 2026, when
[working alongside other contributors](../designs/working-alongside-other-contributors.md)
landed, to say what that design keeps out of the tracker and why. Revised again
on November 7, 2026, when that design ruled how the tracker's export travels
and what its merge keeps outside Beads.

## Context

Yoyodyne needs somewhere durable to hold workflow state: what work exists, what
depends on what, what is blocked, what directives the operator has given, and
what one role has handed to another. That state has to survive process restarts
and agent turns, it has to be queryable by every role, and no agent may be the
only place it lives.

When a team runs several copies of the factory against one repository, each on
its own machine, some of that state also has to reach machines that share
nothing but the remote and the forge, and one question has to be settled
between them at once: which factory is working an item.

## Decision

Beads is that store. Workflow, dependencies, blockers, directives, approvals, and
handoff state live in Beads, with the exceptions this section names. Yoyodyne
does not implement its own tracker and does not keep authoritative workflow
state in memory, in prompts, or in repository files.

Two things a team needs between machines are deliberately not carried by the
tracker. They travel in Git references of the harness's own on the shared
remote, as [working alongside other contributors](../designs/working-alongside-other-contributors.md)
rules:

- **Who may work an item.** The tracker still records an item's holder, but
  across machines the claim reference, `refs/yoyodyne/claims/<item id>`, is what
  decides it. A push that fails if the reference already exists settles a race
  between two factories at once; the tracker reaches another machine only when
  its export is published and fetched, by which time both could have claimed
  the item.
- **Reports, their handling, directives, and the spends of each item's
  budgets.** They travel between machines in the records reference,
  `refs/yoyodyne/records`, and each machine copies what it fetches into its own
  stores. Where each machine keeps them is unchanged by this; the reference is
  how they reach the others.

The tracker's own export reaches other machines through a third reference of the
harness's own, `refs/yoyodyne/tracker`, and not through a commit on the target
branch, so a protected target cannot stop it. Beads has no merge of its own for
the export, so the harness merges it item by item and brings the result in
through `bd import`. That does not make the harness a tracker: Beads remains the
store of every item, and the merge only decides what to bring into it.

The merge keeps two things outside Beads, in the state of the machine that runs
it, and both exist only with team mode on:

- **The versions it compares.** What this machine last read from the reference,
  and the versions of each item it has published, up to a bound. These are how
  two versions are compared, not the state of any item.
- **A hold on an item whose versions clash.** This is a blocker that does not
  live in Beads: nothing the harness chooses starts a held item. It stays on the
  machine that found the conflict. Writing it on the item would edit one of the
  two versions the development manager has to choose between, and would publish
  that edit to the other machine. The report naming both versions travels like
  any other report, and the hold ends when her decision is carried out or the
  two versions agree.

None of the three references is a repository file. No branch carries them,
nothing is checked out to write them, and they never pass through review or the
promotion lease, so the rule above against keeping workflow state in repository
files still holds.

## Alternatives rejected

- **Claims in the tracker alone.** Two factories could each claim an item before
  either saw the other's claim, because the tracker's export is shared only when
  published and fetched.
- **Reports, directives and spends as tracker items or notes.** None of them is
  work, and putting them in the tracker would put them in the queue of work to
  pull.
- **A store of Yoyodyne's own to replace Beads across machines.** It would be the
  tracker this decision declines to build, and replacing Beads is a v1
  non-goal.
- **Sharing the export by committing it to the target branch.** On a protected
  target every tracker change would need a pull request, a review and a
  promotion, and the export would conflict on most merges.
- **Leaving the merge to Git and `bd import`.** Git conflicts on neighbouring
  items that do not clash at all, and `bd import` keeps the later write without
  saying so, which loses one machine's edit with nothing reported.
- **Keeping a hold on the item in Beads.** It would change one of the versions in
  conflict, and it would reach the other machine as an edit to be merged.

## Consequences

- Repository Markdown owns artifact *content*; Beads owns workflow *state*. The
  division is deliberate, and the two are not alternatives.
- Replacing Beads is a non-goal for v1. The claim, records and tracker references
  sit beside it and replace no part of it.
- Agents act on the tracker through bounded, validated operations rather than by
  editing its storage, so every change to workflow state is attributable. The
  same holds for the claim, records and tracker references: only the harness
  writes them, and no agent touches one.
- Across machines, an item's claim reference and the tracker's record of its
  holder can briefly disagree. The claim reference decides who may run the
  item, and the tracker catches up when its export is next merged.
- A held item is blocked on one machine and not on the others, and Beads does not
  show the hold. The machine that found the conflict shows it, and the report
  says what is held and why.
- Yoyodyne inherits Beads' data model. Where the product needs something Beads
  does not express, the answer is a design question, not a reason to fork the
  store.
