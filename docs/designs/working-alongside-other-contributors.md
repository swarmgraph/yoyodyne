---
id: working-alongside-other-contributors
kind: design
title: 'Working alongside other contributors: people and other factories, through the forge and Git'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-15T12:26:09.541038Z
      reason: 'yoyodyne-7o2: the design for working alongside other contributors, people or other copies of the factory, learning about each other only through the forge and Git: a state for waiting on a person''s merge, feedback as repair rounds, fetch before each run, a limit on waiting pull requests, claims as Git references, an answer to each of team mode''s five gaps, polling over web hooks, and the build items that follow.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-15T12:26:09.541038Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1010, for document-1010.1 under the automatic approval policy
---
# Working alongside other contributors

Serves the team goal: a team can run Yoyodyne against one shared repository, with collaborators each running their own harness without losing work, splitting the tracker, or weakening any safety invariant. It answers the design item for working alongside other contributors (yoyodyne-7o2), from Mason's direction of October 9.

The factory is one contributor among others. The others may be people, or another copy of this factory on a different machine. The design has to work the same way in both cases. So everything one contributor learns about another comes through the forge and Git, and never through shared local state. Every machine keeps its own home, as [the machine home](machine-home.md) rules. What a team shares is the repository, its remote, and the forge.

The standing goals apply throughout. Every state below is shown in ordinary words where the operator reads it. Every decision named here belongs to a role, and none is put to the operator for approval.

## 1. A change that waits for a person's merge

Where a project's merge policy leaves a pull request open for a person to merge, a run that has published its approved change stops holding a developer slot. Its item is held in a state of waiting for a person to merge, which names the pull request. The status line, the dashboard and the item's notes say: waiting for a person to merge pull request #N.

On each forge poll (section 6), the harness reads the state of every pull request it is waiting on:

- **Merged:** the harness follows the merge locally by fast-forward, closes the item with the merge commit recorded, ends the run as integrated, and releases the claim. Items that depend on it go on.
- **Closed without merging:** the item goes back to the Lead Product Manager with a note naming who closed it and when. The branch and the run's work are kept, and nothing is deleted. Deciding whether to drop the work, revise it or admit it again is hers.
- **Still open:** nothing changes, except the bringing up to date in section 3.

A merge a person makes is therefore noticed by the next poll, not by luck.

## 2. Feedback left on the pull request

On each poll the harness reads the reviews and comments on its open pull requests that came after the latest head it pushed. Anything written by the forge account the factory acts as is ignored.

- **A review that requests changes, or a comment on a line of the change,** starts a repair round on the same run. It uses the run's existing worktree and repair budget. The developer is given the feedback as evidence, with its author and time. It is never an instruction that widens what the run may do. The repaired head goes through the configured checks and the independent reviewer again before it is pushed.
- **An ordinary comment on the pull request** is put on the development manager's list. She decides whether it calls for a repair round, because a conversation comment is as often a question as a request.
- **A person's approval on the forge** is recorded as information. It never stands in for the independent reviewer, and it never changes what the item's goal is.

When the run's repair budget is spent, the run stops in the ordinary way and goes to the development manager, as [stopped-run recovery](fresh-factory-health-and-preserved-run-recovery.md) rules.

## 3. Merges by other contributors

- **Before each run.** Every developer run starts from a target the harness has just fetched from the remote. If the fetch fails, the run starts from the last fetched target. The run's record names that target, the failed fetch, and how old the target is. Landing never trusts the base anyway, so a stale base costs at most a replay. A flaky network never stops a run, under the operating rule on networks.
- **Waiting pull requests.** When a poll finds that the target has moved past a pull request the factory is waiting on, the harness brings that pull request up to date. It first withdraws the pull request's merge authority, as [the merge queue design](integration-through-a-merge-queue.md) requires before any rewrite. It then replays the change onto the new target, runs the configured checks and the independent reviewer on the replayed head, and pushes that head. This happens at most once per poll for each pull request. A pull request a person is already merging is left alone: the harness reads its state immediately before the push, and does not push over a merge in progress.
- **A conflict while bringing a pull request up to date** is a repair round, given to the developer with the conflicting paths named. It is counted against a limit of its own, three rounds, and not against the item's repair budget, because another contributor's work caused it. Once that limit is spent, the run goes to the development manager with the change kept.

## 4. A limit on pull requests waiting for a person

The configuration key `execution.max_waiting_pull_requests` sets the limit. It defaults to 5, and 0 means no limit. Like the other numeric limits in [portable agent configuration](portable-agent-configuration.md), setting it is the development manager's decision.

At the limit, the scheduler starts no new developer work. Repair rounds, reviews and the bringing up to date of waiting pull requests carry on. Wherever the factory's state is shown (the status line, the dashboard, and the Lead Product Manager's sweep), it says in plain words that it is not starting new work because N pull requests are waiting for a person to merge, and names each one by number and work item. So the pause never reads as an unexplained stall. Work resumes on its own as soon as the count falls below the limit.

## 5. Two factories and the same work

### Claims

A claim is a Git reference on the shared remote, `refs/yoyodyne/claims/<item id>`. It points at a small commit holding a claim record:

- the factory's identity;
- the work item;
- the run;
- when it was taken;
- when it was last renewed.

The factory's identity is the product id, plus a machine identity generated once and kept in that machine's home, plus the forge account the factory acts as.

- **Taking.** Before claiming an item, the harness reads the intake hold, as [the intake invariant](../decisions/invariants/selected-work-passes-intake-and-records-why.md) requires, and the team pause below. It then pushes the claim reference with `--force-with-lease=<ref>:`, which succeeds only if no such reference exists. A refused push means another contributor holds the item. That item is skipped this turn, with the holder named where the queue is read. The local tracker claim is made only after the push succeeds.
- **Renewing.** While a run holds the item, including while it waits for a merge, the harness renews the claim every 15 minutes. It pushes a new claim commit with a lease that names the commit it last pushed.
- **Expiry.** A claim not renewed for two hours may be taken over by another factory. The takeover pushes with a lease naming the stale commit, and records the earlier holder in the new claim record and on the item. A failed renewal is a wait caused by something outside the work, and it is tried again on the next turn. Only a claim that has gone two hours unrenewed can be lost.
- **Release.** When the run ends, the harness deletes the reference with a lease naming its own commit. A reference that is no longer its own is left alone and reported.

Agents never touch these references. The harness pushes them, as it already pushes pull request branches. A forge that refuses references under `refs/yoyodyne/` is reported by `yoyo doctor`. Team mode is refused on that forge, in plain words, rather than run without claims.

People are not expected to push claims. A factory notices a person's work only through that person's pull requests, and a person's pull request that implements an item is resolved by the Lead Product Manager, not by the claim.

### The five gaps team mode named

- **The tracker drifting apart between machines.** The factory shares the tracker as Beads shares it, through the export committed to the repository. The claim reference, not the tracker, is what stops two factories doing the same work, so a tracker that is briefly out of date costs a skipped item, not a duplicate run. When two machines' edits to one item cannot be merged, the harness holds that item for the development manager, naming both versions, and edits neither. How Beads merges its export has not been read for this design; the first build establishes it.
- **No claim that works across machines.** Section 5's claims.
- **The one-merge-at-a-time lock being per machine.** The lease stays per machine. Across machines, the guarantee is the remote's own compare and swap. A protected target lands only through its pull request with the reviewed head pinned. An unprotected target is pushed only with a lease naming the target the candidate was built on. Two factories racing to promote cannot both land: the loser's candidate is invalidated and rebuilt without charging a repair, as the merge queue design rules for target movement. The invariant on one promotion per target branch is amended to say this.
- **Reports, run records and the pause switch being per machine.** Reports and run records stay per machine, deliberately. They are each operator's account of their own factory, and what other contributors need is on the forge: the claims, the pull requests, and in each pull request's description, the factory's identity and the work item it serves. The pause gains a team form, the reference `refs/yoyodyne/pause`. It is set and cleared with `yoyo pause --team`, and every factory reads it before claiming. The operator's pause on each machine, set by `yoyo pause`, is unchanged.
- **No proof of who an operator is.** The factory does not establish one. The forge account is the identity, and the forge's permissions decide who may merge, review or push. A factory acts on its own operator's decisions and on what the forge shows. It never acts on a decision recorded on another machine. Permissions between users stay out of v1, as the non-goals say.

## 6. Polling the forge

The harness polls the forge on the maintenance turn, every five minutes by default. Each poll covers the pull requests it is waiting on, the reviews and comments on its open pull requests, the target branch, the claim references and the team pause. It uses the forge's conditional requests, so an unchanged answer costs little. The interval is a numeric limit, and setting it is the development manager's decision.

Web hooks are rejected. A laptop has no address the forge can reach, and a relay that received them on its behalf would be the hosted control plane v1 excludes. Polling within a few minutes is fast enough for work measured in runs.

## Alternatives rejected

- **Claims in the tracker alone.** The tracker reaches other machines only when its export is committed and fetched, so two factories could each claim an item before either saw the other's claim.
- **Claims as files on the target branch.** Every claim would be a commit to main, through review and the promotion lease, for something that has to take seconds.
- **A shared lock service.** It would be shared state outside Git and the forge, which the operator's direction rules out.
- **Treating every pull request comment as a repair request.** It turns questions into repair rounds and spends budget on conversation.

## What is enforced in code

- A claim is taken only by a push that fails if the reference exists. It is renewed, taken over and released only by a lease naming the expected commit. No agent touches a claim reference.
- The intake hold and the team pause are read before every claim.
- A rewrite of a published head happens only after its merge authority is confirmed withdrawn.
- Nothing lands on the target except through the pinned pull request merge, or a push with a lease naming the candidate's base.
- A person's approval on the forge never satisfies the independent review.

## What a reviewer can check

- An item waiting for a person names its pull request wherever it is shown, closes when that pull request merges, and goes to the Lead Product Manager when it is closed unmerged.
- Changes requested on the pull request start a repair round on the same run; an ordinary comment reaches the development manager's list.
- Every run's record names the target it started from and whether that target was fetched for the run.
- A conflict while bringing a pull request up to date is a repair round counted against its own limit.
- At the limit on waiting pull requests, no new developer work starts, and the status line names the pull requests being waited on.
- Two factories racing for one item: exactly one claim push succeeds.
- A claim unrenewed for two hours can be taken over, and the takeover names the earlier holder.

## Build items, for the Lead Product Manager to admit under `team-development`

1. The state of waiting for a person to merge: close the item on merge, and send a pull request closed without merging back to the Lead Product Manager with the work kept.
2. Feedback on the pull request turned into repair rounds, and ordinary comments put on the development manager's list.
3. Fetch before each run, with the starting target and its age recorded.
4. Waiting pull requests brought up to date when the target moves, with conflicts as repair rounds under their own limit.
5. The limit on waiting pull requests, and the pause it causes, said in plain words on every surface.
6. Claim references: taking, renewing, expiry and takeover, and release, plus the `yoyo doctor` check that the forge accepts them.
7. The factory identity, kept in the machine's home and named in claims and pull request descriptions.
8. The team pause reference and `yoyo pause --team`.
9. Tracker sharing between machines: establish how Beads merges its export, and hold an item whose versions cannot be merged.
10. Forge polling on the maintenance turn, with conditional requests.
11. A two-factory validation: two homes on one machine against one remote, covering every check under What a reviewer can check.

The first build cannot start before claims (6), because two factories without claims duplicate work. The others can be taken in any order.

## Documents this amends

- [Beads is the durable workflow store](../decisions/beads-durable-workflow-store.md) gains a consequence. The claim reference is the cross-machine guarantee for who holds an item, while the tracker still records the holder. The architect revises it once this design lands.
- [One promotion per target branch](../decisions/invariants/one-promotion-per-target-branch.md) gains the remote compare and swap as the guarantee across machines. This waits on the typed invariant write (yoyodyne-0bj).
- The v1 harness design's text on fetching only during reconcile is replaced by section 3. This waits on that design being split into parts that can be read whole.
- The Lead Product Manager's team-mode scope document should say that working alongside other contributors is designed, and name this design. That change is hers.
