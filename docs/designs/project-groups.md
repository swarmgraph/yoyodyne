---
id: project-groups
kind: design
title: 'Project groups: several repositories sharing one intent and one set of management roles'
supports:
    - project-groups-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-20T22:52:10.59931Z
      reason: 'yoyodyne-ob8d: the design for project groups. First stage needs no change to intent: each project keeps its own harness and repository, the group shares one intent repository, tracker and records through the contributors design''s references, management runs only in the managing project, work spanning projects lands as ordered steps that keep every combination working, proved by group checks bound to every commit read. One harness for the whole group waits on the operator amending the v1 non-goal and is not designed here.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-20T22:52:10.59931Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3253, for document-3253.1 under the automatic approval policy
---
# Project groups

Serves the project groups goal in [the project groups goals](../product/project-groups-goals.md): a group of projects, each bound to its own repository, shares one statement of intent and one set of management roles, and delivers work that spans them in an order that keeps every deployed combination of the projects working. It answers the design item for project groups (yoyodyne-ob8d).

The standing goals apply throughout: every state below is shown in ordinary words where the operator reads it, and every decision named here belongs to a role, never put to the operator for approval, except the one the v1 non-goals reserve for him.

## What bounds this design

[The v1 non-goals](../product/goals/v1-non-goals.md) keep one harness to one active product in one repository. The goal's own document says work toward it may be designed and admitted now, and that running more than one repository in one harness needs that non-goal amended first, which is the operator's decision.

So this design comes in two stages:

- **The first stage needs no amendment.** Each project keeps its own harness, bound to its own repository, exactly as [the machine home](machine-home.md) rules. What the projects share is one intent repository, one tracker, and one set of management roles running in one of the harnesses. Every build piece of this stage can start now.
- **The second stage needs the amendment.** One harness running several repositories at once. Its build pieces are marked as waiting on the operator amending the non-goal. The architect's recommendation is not to propose that amendment until the first stage has run a week, because the first stage already meets the goal and the second only saves running several harnesses.

## 1. What a group is

A group is a set of projects, each with its own `product.id`, its own repository, and its own project directory in the machine home, plus one companion intent repository they all share. It is built on the companion intent repository of [the machine home](machine-home.md), which already moves the governed set and the tracker out of a project's repository. A group is that same arrangement with more than one project pointing at the one intent repository.

The intent repository holds, as for one project: `docs/product`, `docs/designs`, `docs/decisions` with its invariants, `docs/terms.md`, and the tracker's export. It holds one more file, `group.yaml`, at its root:

- the group's id, one safe path segment, as a product id is;
- each member project, by its `product.id` and its repository's remote URL;
- which member is the managing project (section 3);
- the group checks (section 5).

The file lives in the intent repository because it is part of the one statement of intent: which projects the intent covers. It is a governed document of the Lead Product Manager's. Adding or removing a member is hers, recorded in its revision log and reported to the operator afterwards. It does not change what the goals admit, because the goals already admit work on any project of the group. Making a group in the first place is the operator's setup, with `yoyo group init`, just as binding a repository is.

Each member's configuration, kept under its project directory as the companion repository already requires, names the group: `group: <group id>`. On each machine the group's intent repository is cloned once, at `groups/<group id>/intent/` at the top of the machine home, because no single project owns it. This adds one entry to the list of what sits at the top of the home, and the machine home design is revised to say so.

## 2. One tracker

The tracker is the one in the group's intent repository. Each member's harness keeps its own tracker store, as every harness does today, and the stores are kept in agreement through the reference `refs/yoyodyne/tracker` on the intent repository's remote, merged item by item exactly as section 7 of [working alongside other contributors](working-alongside-other-contributors.md) rules. Two harnesses on one machine are, for this purpose, two factories, and nothing new is needed to keep them from losing each other's edits.

A group's intent repository therefore needs a remote. It may be a bare repository on the same machine. `yoyo doctor` refuses a group whose intent repository has none, in plain words, naming `yoyo group init` as the remedy.

Every work item that changes a repository names the project it changes, in the item's metadata under the key `project`, as a `product.id` listed in `group.yaml`. A member's harness starts only items whose `project` names its own product id. An item that names no project is work in the intent repository, such as a design or a ruling, and is started only by the managing project's harness. An item naming a project the group does not list is refused at intake, naming the project.

Claims work as section 5 of the contributors design rules, on the intent repository's remote. Because each item names one project, two harnesses never want the same item unless they are two machines running the same project, which is exactly the case claims already settle.

## 3. One set of management roles

The managing project's harness runs every management pass for the group: the Lead Product Manager, the architect, the development manager and any program manager. The other members' harnesses run no management pass at all; they run developers, reviewers, and the integration of their own repository. A member that is not the managing project refuses at start a configuration that turns on a management pass, naming the managing project.

The management roles see the whole group:

- **Work.** They read the one tracker, which holds every member's items.
- **Reports and directives.** Every member's harness writes its reports, their handling and directives to `refs/yoyodyne/records` on the intent repository's remote, as section 7 of the contributors design rules, and the managing harness copies them into its own stores. Every surface reads them through the one read model, as [the read model invariant](../decisions/invariants/surfaces-project-one-read-model.md) requires.
- **Repositories.** A management role reads a member's repository by naming it on the read. The harness resolves the read at the commit that member's target branch holds and says which project and commit it read, just as a read of the intent repository is resolved today.
- **Spend.** What each run spent stays on the machine and harness that ran it, as today. The managing harness shows the spend each member reports in its records, by project and in total for the group, so the operator can see what each project costs.

Which member is the managing project is the Lead Product Manager's choice, recorded in `group.yaml`. Changing it moves the management passes at the next start of each harness; a pass in flight finishes where it started.

The operator's routine surface is the managing project's conversation with the Lead Product Manager. The operator's pause on a machine stops every harness on that machine, as it stops every project today.

## 4. Work that spans projects

The development manager decomposes work that spans projects into one item per project it changes, each naming its project, linked by the tracker's ordinary dependencies. No item changes two repositories, so every run, review and landing stays within one repository, and every rule that applies to one project's change applies unchanged.

The order is chosen so that every combination of the members' target branches that can exist while the steps land still works together. The rule the development manager follows:

1. A change that other projects depend on lands first, in a form that still serves what used to depend on it: a new interface beside the old, a new field that may be absent, a new behaviour behind a setting that is off.
2. Each project that depends on it then moves to the new form, one item each, each depending on step 1.
3. Only once every dependent project's item has landed does a final item remove the old form, depending on all of them.

The architect's design for such work says which project's change comes first and what each step must keep working. A design that cannot be split this way, because no form of the first change can serve both the old and the new, says so and names the projects whose steps must then land with nothing else landing between them; section 6 says what the harness does for that case.

An item does not start until every item it depends on has landed on its own project's target branch. That is how dependencies already work: an item closes when its change lands, and a dependent item is not ready until then. So the order is held by the tracker and needs no new mechanism.

If a later step fails and stops, nothing already landed is undone. Each landed step was chosen to leave every combination working, so a stopped step leaves the group working and goes to the development manager in the ordinary way, as [stopped-run recovery](fresh-factory-health-and-preserved-run-recovery.md) rules.

## 5. The group checks

Each project's own checks prove its own change. The group checks prove that the combination still works. `group.yaml` declares them: for each, the commands to run, the members it needs, and where each member is placed relative to the others. They are commands the group declares, never something the harness understands, as for any project's checks.

A group check runs on every change to a member that the check names, before the change lands. It runs against the candidate commit of the member being changed and the commit each other member's target branch holds, for every member it needs. The harness makes those other checkouts for the check only, outside the run's worktree, from each member's remote at a recorded commit, and removes them afterwards. The check's record names every commit it ran against.

A change lands only with its own checks and the group checks that name its project passed on exactly those commits, as [the evidence invariant](../decisions/invariants/integration-requires-revision-bound-evidence.md) requires: its evidence is bound to every commit the check read, and is stale once any of them moves. If another member's target moves between the group check and the landing, the candidate is checked again against the new commit before it lands, as the merge queue already does when its own target moves.

A developer run never reaches another member's repository. Its worktree is its own project's, and the checkouts for a group check are made by the harness, for reading only, outside the worktree, so [the confinement invariant](../decisions/invariants/repository-writes-are-physically-confined.md) holds unchanged.

A member with no group check declared lands as it would alone. The development manager is told, once, when an item spanning projects is admitted for a group whose members it changes have no group check between them, because the order then rests on the design alone.

## 6. Steps that must land together

Where a design says two projects' steps must land with nothing else between them, the development manager links them as a pair. The harness lands the pair in the design's order, the second only after the first has landed and the group check has passed against the first's landed commit and the second's candidate. While the pair is open, no other change to either member lands; each member's queue holds its other candidates and says in plain words that it is waiting for the pair to finish. If the second step cannot land, the first stays landed, the group is left in a combination the design said might not work, and one warning report goes to the development manager naming both items and the combination now on the target branches.

No forge can merge into two repositories at once, and the lease on promotion is held per target branch, so landing a pair is ordered, not simultaneous. That is why the design rule in section 4 is to avoid pairs, and a pair is the exception the design must argue for.

## 7. The second stage: one harness for the group

Once the operator has amended the v1 non-goal, one harness may run every member of a group: one supervisor, one scheduler with developer slots shared across the members, one tracker store, and worktrees of each member's repository under its own project directory. Everything in sections 1 to 6 still applies; what changes is only that the members' harnesses become one. Nothing in the first stage has to be undone for it: the item's `project` key, the group file, the group checks and the ordering rule are the same.

The second stage is not designed further here. It needs its own design once the amendment is decided, covering how the shared slots are divided between members and how one supervisor moves several repositories onto a new build.

## Alternatives rejected

- **One repository holding every project.** It avoids ordering altogether, but the goal names projects each bound to its own repository, and many teams cannot merge their repositories.
- **Landing every step at once.** No forge offers a merge into several repositories at once, and the lease on promotion is per target branch, so a promise of simultaneous landing would be broken by the first forge that refused the second merge.
- **A copy of the intent in each project.** The goal asks for one statement of intent; copies drift and give two answers to what the product is for.
- **One shared tracker store for every harness on a machine.** Several harnesses opening one store at once is a way of losing writes that nothing here needs, when the item by item merge already keeps separate stores in agreement.
- **Management passes in every member.** Several Lead Product Managers for one statement of intent would give several answers to one question.
- **One harness for the whole group now.** It is the v1 non-goal, and the operator's to amend.

## What is enforced in code

- A member's harness starts only items whose `project` names its own product id; an item naming no project is started only by the managing project's harness; an item naming a project the group does not list is refused at intake.
- Only the managing project's harness runs management passes; any other member refuses at start a configuration that turns one on.
- A group whose intent repository has no remote is refused by `yoyo doctor`.
- A change lands only with its group checks passed against every commit they read, and the evidence is stale once any of those commits moves.
- Checkouts of other members for a group check are made by the harness, outside the run's worktree, for reading only; no agent session is given a path into another member's repository.
- An item does not start until every item it depends on has landed on its own project's target branch.

## What a reviewer can check

- Two projects bound to their own repositories and one intent repository: an item naming one project is started only by that project's harness, and a design item only by the managing harness.
- An item naming a project not in `group.yaml` is refused at intake, naming it.
- A report filed in one member's harness is shown in the managing harness after its next check, naming the project it came from.
- A member that is not the managing project, configured with a management pass, refuses to start, naming the managing project.
- A change to one member runs the group check against its candidate and the commits the other members' target branches hold; the record names every commit; a move of another member's target before landing causes the check to run again.
- Work spanning two projects lands in the order its items depend on, and a later step that stops leaves the earlier step landed and the group checks passing on the target branches.
- A pair of steps lands in order with no other change to either member between them, and a pair whose second step fails produces one warning report naming both items.
- The managing harness shows spend by project and for the group.

## Build pieces, for the Lead Product Manager to admit under the project groups goal

The first stage, which can start now:

1. `group.yaml` and its checks: the group id, members, managing project and group checks, read and checked at load; `yoyo group init`, which makes the intent repository with its remote and the group file.
2. The group's intent clone at `groups/<group id>/intent/` in the machine home, with each member's configuration naming the group; the machine home design is revised to list it first.
3. The `project` key on work items: intake refusing an unknown project, and each harness starting only its own project's items and the managing harness starting the rest.
4. Sharing the tracker, claims and records over the intent repository's remote for a group, reusing the contributors design's references, on one machine as across machines.
5. Management passes only in the managing harness, with the refusal for any other member.
6. Reads of a member's repository by a management role, resolved at the commit that member's target branch holds and recorded.
7. Spend shown by project and for the group in the managing harness.
8. Group checks: checkouts the harness makes at recorded commits, evidence bound to every commit read, the check run again when another member's target moves, and the one notice to the development manager for spanning work with no group check.
9. Pairs of steps that must land together: holding both members' other landings while the pair is open, and the warning report when the second step fails.
10. A validation with two members and one intent repository on one machine, covering every check under What a reviewer can check.

The second stage, each waiting on the operator amending the v1 non-goal that keeps one harness to one repository:

11. A design for one harness running every member of a group.

Pieces 1 and 2 come first; 3, 4 and 5 need them; 8 and 9 need 3. The others can be taken in any order once 1 has landed.

## Documents this changes

- [The machine home](machine-home.md) gains `groups/<group id>/intent/` in its list of what sits at the top of the home. The architect writes that revision once this design lands.
