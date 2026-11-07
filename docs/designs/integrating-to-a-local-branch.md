---
id: integrating-to-a-local-branch
kind: design
title: Integrating to a local branch and handing landed work to the team through a pull request the harness tracks
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-07T19:44:19.303564Z
      reason: 'yoyodyne-ifd.429.127: the design for integrating to a branch other than the team''s main and handing landed work to the team through a pull request the harness tracks. Items stay open until the team merges them; an outside pull request is tracked by an explicit, forge-checked record; the local branch is kept current by a merge that earns checks and independent review; and an outside tool can hold landing for a bounded time. A separate document because the contributors design is close to the read limit; it extends that design''s sections 1 to 3 and changes none of them.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-07T19:44:19.303564Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2448, for document-2448.1 under the automatic approval policy
---
# Integrating to a local branch and handing landed work to the team

Serves the team goal: a team can run Yoyodyne against one shared repository, with collaborators each running their own harness without losing work, splitting the tracker, or weakening any safety invariant. It answers the design item for integrating to a branch that is not the team's main (yoyodyne-ifd.429.127), from a user's setup the operator passed on on November 7, 2026, and stays inside [the team-mode scope](../team-mode-scope.md).

In that setup the factory lands its work on a branch of the project's own, such as `staging`. A script of the project's own then opens one pull request into the team's branch, such as `main`, carrying a batch of landed items. Which items go into a batch, and when, is that project's policy. The harness does not batch, and this design adds no batching rule.

## How this sits beside the design for working alongside other contributors

[Working alongside other contributors](working-alongside-other-contributors.md) covers a pull request the harness opened and a person merges (its section 1), feedback on that pull request (section 2), and keeping it current when the target moves (section 3). This design extends those three sections to a pull request the harness did not open and to a target branch that follows another branch. It changes nothing they say. Where a rule below is the same as one there, it points there rather than restating it.

This design is separate from team mode. Like sections 1 to 4 of that design, it works with team mode off. With team mode on, the factory that landed an item keeps that item's claim while it waits for the team's merge, as section 5 of that design already says for any item waiting on a merge, and only that factory acts on the pull request that carries it.

It is also separate from which feedback on the harness's own pull requests is ignored (yoyodyne-ifd.429.125). The rules for reading feedback below follow whatever that settles.

## 1. The team's branch, and an item waiting on the team's merge

A project names the team's branch with `execution.upstream_branch`, such as `main`, on the same remote as the target branch. It is unset by default, and unset means today's behaviour exactly. Setting it changes when items close, so it is the development manager's decision, applied as [portable agent configuration](portable-agent-configuration.md) rules for settings a role applies.

With it set, an item whose change lands on the target branch is not closed. It is held in a state of waiting for the team's merge, and the status line, the dashboard and the item's notes say: landed on `staging`, waiting for the team to merge it into `main`. The run ends as integrated in the ordinary way, and its developer slot is freed.

An item in this state:

- is never pulled again for a developer run;
- counts as met for an item that depends on it and lands on the same target branch, because that work is built on the target branch, which already holds it;
- closes when the pull request carrying it merges (section 2), or when a fetch of the team's branch finds the item's landing commit there, whichever comes first, with the commit recorded on the item.

A team that squashes a batch into one commit leaves no landing commit to find, which is why a merge of the pull request carrying the item also closes it.

## 2. A pull request the harness did not open

The outside tool tells the harness which pull request carries which items:

- `yoyo pull-request track <number> --item <id> [--item <id>...]` records it;
- `yoyo pull-request untrack <number>` removes the record.

Before recording, the harness reads the pull request from the forge and refuses the record, in plain words, unless:

- the pull request exists and goes into the configured team's branch;
- each named item is waiting for the team's merge on this target branch;
- each named item's landing commit is among the pull request's commits.

A script that builds the batch from new commits, such as a squash or a set of picked commits, may name an item whose landing commit is not among them only with `--unverified`. The item is then recorded with that said wherever it is shown.

Recording is information, not a decision. It grants nothing, and the harness still decides nothing on the strength of it except what this section says.

On each forge check (section 6 of the contributors design), the harness reads every tracked pull request as it reads its own:

- **Merged:** every item it carries closes, with the merge commit recorded, and items that depend on them go on.
- **Closed without merging:** every item it carries goes back to the Lead Product Manager with a note naming who closed it and when. The change is already on the target branch, and nothing is undone. Whether to leave it for a later batch, admit an item that reverts it, or close it as wanted only on the target branch is her decision.
- **Items found in it but not named:** an item waiting for the team's merge whose landing commit appears among the pull request's commits is added to the record, with a note on the item. This keeps the record right when the pull request's head is the target branch itself and grows as more work lands.

An item can be carried by one tracked pull request at a time. Recording it on a second one moves it there, with a note naming both.

### Feedback on a tracked pull request

Feedback is read as section 2 of the contributors design reads it, ignoring only what the harness wrote. A tracked pull request carries many items, and their runs have ended, so feedback is never turned into a repair round on its own:

- **A review that requests changes, or a comment on a line of the change,** is put on the development manager's list, naming the pull request, the comment, and the items whose landed commits touched the file it is about.
- **An ordinary comment** is put on her list in the same way, with no item named.
- **Her decision** to repair names one item. It starts a run of that item on the target branch, with the feedback given to the developer as evidence, never as an instruction that widens what the run may do. The run goes through the configured checks and the independent reviewer as any run does, and is charged to that item's repair budget. Its change lands on the target branch, where the outside tool picks it up.
- **A person's approval** is recorded as information and never stands in for the independent reviewer.

Tracked pull requests are not counted against `execution.max_waiting_pull_requests`. That limit stops new work while the harness's own pull requests wait, and work landing on the target branch does not wait on the team's merge.

## 3. Keeping the target branch current with the team's branch

On each forge check, after its fetch, when the team's branch has moved past what the target branch holds, the harness brings the target branch up to date:

1. It builds, in a checkout of its own, a merge of the team's branch into the current target branch. It never rebases the target branch, because other pull requests, the tracked ones included, point at its commits.
2. It runs the configured checks and the independent reviewer on that exact merge commit. The rule that [integration requires evidence bound to the exact candidate](../decisions/invariants/integration-requires-revision-bound-evidence.md) applies to this merge as to any change, so a merge that changes no line of its own still earns both. The reviewer is told that the change brings in the team's merged work and is judged on whether the merge is faithful and the checks pass.
3. It lands the merge as [one promotion per target branch](../decisions/invariants/one-promotion-per-target-branch.md) rules: under the landing lease, with the remote's compare and swap. A target that moved meanwhile means the merge is built again, as for any candidate.

This happens at most once per forge check, and one merge covers everything the team's branch gained since the last one, so review is spent once per check rather than once per teammate's change.

Each update is recorded beside the target branch's landing records: the team's commit brought in, the merge commit, the checks, the reviewer's verdict, and when it landed. The status line shows when the target branch was last brought up to date, and the update waiting, if any.

**A conflict** is not resolved by the harness. The update waits, and the development manager's list gets one entry naming the conflicting paths and the items on the target branch whose landed commits touched them. Her decision names one item, and starts a run of it that resolves the conflict on the target branch. That run is counted against a limit of its own, three rounds, and not against the item's repair budget, as section 3 of the contributors design rules, because another contributor's work caused it. Work keeps landing on the target branch while the update waits.

## 4. A hold on landing that an outside tool can set

An outside tool that needs the target branch still while it builds a batch can set a hold:

- `yoyo landing hold <target> --for <duration> --reason <text>` stops the harness landing anything on that target branch until the hold is released or expires;
- `yoyo landing release <target>` releases it.

The duration is required and may not exceed four hours, so a script that dies cannot stop landing for good. The hold is read under the landing lease, immediately before the guarded change, with the other holds the merge-queue design rereads there. While it stands, checks and reviews carry on, and a change ready to land waits with its evidence kept. A target branch that moves while it waits still needs its evidence earned again, as always. The status line names the hold, its reason, and when it ends.

The hold stops landing on one target branch on this machine, nothing else. It is not `yoyo pause`, which stops this machine's spending, and not the intake hold, which stops new work. It does not travel between machines.

A script does not need the hold if it builds its batch from a commit it has chosen, rather than from the moving head of the target branch, and that is the simpler way.

## Alternatives rejected

- **The harness opening the batch pull request itself.** Which items go into a batch, and when, is the project's policy, and the item that asked for this design says so.
- **Closing an item when it lands on the target branch, as today.** The team's merge is the outcome that matters, and an item closed early sends nothing back when the team refuses the batch.
- **Finding every pull request whose head is the target branch, with no record.** It would adopt pull requests nobody meant to hand over. A record the outside tool makes, checked against the forge, says what the batch carries.
- **Turning feedback on a batch into a repair round directly.** A batch carries many items, and a comment cannot reliably name its item, so the development manager decides which item to repair.
- **Rebasing the target branch onto the team's branch.** It rewrites commits that tracked pull requests point at, and no promotion forces a target.
- **Bringing in the team's branch with checks but no review.** The rule on evidence bound to the exact candidate requires independent review for every integration, and an exception for merges would be a path around it.
- **Using `yoyo pause` to stop landing.** It stops all spending on the machine, and it does not stop a landing already under way.
- **Writing this into the contributors design.** That design is close to the most one read returns, and another section would leave it revisable only after the fix for reading long files (yoyodyne-t2j).

## What is enforced in code

- With `execution.upstream_branch` unset, nothing in this design runs.
- With it set, an item landing on the target branch is never closed by the landing; it closes only on a merge of the pull request carrying it or on its landing commit being found on the team's branch.
- A pull request is tracked only after the forge read in section 2 confirms it, and an item not found among its commits is recorded only when marked unverified.
- Feedback on a tracked pull request never starts a run without the development manager's decision.
- An update of the target branch lands only with configured checks and an independent review of the exact merge commit, through the remote's compare and swap, and never by rebase or force.
- A landing hold is read under the landing lease before every guarded change to its target branch, and expires on its own.

## What a reviewer can check

- With the team's branch unset, an item closes when it lands, as today.
- With it set, an item that lands is shown as waiting for the team's merge, is not pulled again, and lets an item that depends on it start on the same target branch.
- Tracking a pull request into another branch, or naming an item whose landing commit is not in it without `--unverified`, is refused in plain words.
- A tracked pull request that merges closes every item it carries; one closed without merging sends each to the Lead Product Manager with the change kept.
- An item that lands while a tracked pull request's head is the target branch is added to that record with a note.
- Squash merging a tracked pull request closes its items.
- A line comment on a tracked pull request reaches the development manager's list naming the items that touched that file, and starts no run until she decides.
- When the team's branch moves, the target branch gains one merge commit that passed the checks and the reviewer, and the status line shows when.
- A conflict while bringing the target branch up to date reaches the development manager's list naming paths and items, and landing on the target branch carries on.
- With a landing hold set, nothing lands on that target branch, checks and reviews carry on, and the hold ends at its expiry with nobody releasing it.

## Build items, for the Lead Product Manager to admit under `team-development`

1. The team's branch setting and the state of waiting for the team's merge: kept open on landing, never pulled again, met for dependents on the same target branch, closed when the landing commit is found on the team's branch, and shown on every surface.
2. Tracking a pull request: `yoyo pull-request track` and `untrack`, the forge read at recording, merged and closed handling on each forge check, and items found among its commits added.
3. Feedback on a tracked pull request put on the development manager's list with the items that touched the file, and her decision starting a run of one item on the target branch. It builds on build item 2 of the contributors design and on yoyodyne-ifd.429.125.
4. Bringing the target branch up to date: the merge, its checks and review, landing by compare and swap, the update record and status line, and conflicts put on the development manager's list with a repair limit of three rounds.
5. The landing hold: `yoyo landing hold` and `release`, the four-hour bound, reading it under the landing lease, and the status line.
6. A validation on a scratch repository with a target branch and a team's branch, covering every check under What a reviewer can check.

Items 2, 3 and 4 need item 1. Item 4 needs the fetch before each run and the forge check on its own schedule from the contributors design (its build items 3 and 10). Item 5 stands alone.

This design changes no other document and adds no invariant: the two rules linked above already bind every landing it describes.