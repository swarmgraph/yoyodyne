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
    - action: amended
      by: architect
      at: 2026-10-15T13:10:19.311988Z
      reason: 'Keeps the promises of the operator-approved team-mode scope that the published text broke, as the reviewer of its publishing run found (yoyodyne-ifd.437.52): reports, their handling and directives travel between machines through a records reference on the shared remote; each item''s budgets are counted across every factory, spent only by the factory holding the item''s claim and acted on only after the spend is pushed; the team pause is the shared form of the intake hold; and the old draft docs/team-mode-coordination.md is named as replaced, with a build item to change that file, which lies outside the architect''s folders.'
    - action: amended
      by: architect
      at: 2026-10-29T02:56:39.625831Z
      reason: 'yoyodyne-ifd.429.111: claims, the team pause, the shared records and shared budgets apply only when the project turns team mode on, so a factory working a repository alone reads no team pause, takes no claim and never holds back new work for an unreachable remote; the forge check runs on its own schedule apart from the maintenance pass, and a change it read but could not finish acting on is acted on again at the next check.'
    - action: amended
      by: architect
      at: 2026-11-05T18:08:29.095053Z
      reason: 'Two lines of this section were out of date: the decision record on Beads gained its consequence on October 15, and the invariant on one promotion per target branch gained the remote compare and swap on November 5, so neither still waits. The v1 design line now names the fix it waits on.'
    - action: amended
      by: architect
      at: 2026-11-06T19:31:47.681685Z
      reason: 'yoyodyne-ifd.429.124: feedback on the harness''s pull requests ignores only what a factory wrote, told apart by a hidden mark on everything the harness writes and the run''s list of the forge identifiers of what it wrote, never by the account, so the owner''s own feedback is read on a project that publishes under the owner''s account; a separate publishing account is optional and changes only what the forge lets the owner do. The build piece joins build item 2.'
    - action: amended
      by: architect
      at: 2026-11-07T06:45:59.585889Z
      reason: 'Approved amendment-c7613717 from the developer building tracker sharing: Beads has no merge of its own for its export, so the harness merges it item by item, and the export travels through a reference of the harness''s own on the shared remote rather than a commit on the target branch, so a protected target cannot stop it. Adds the rules for held items and for a machine''s own earlier versions, and build item 15. Written again from main at c54e563c because the first writing of this revision did not reach main.'
    - action: amended
      by: architect
      at: 2026-11-07T09:40:34.421244Z
      reason: 'The Beads decision record landed on November 7, 2026 with the line this section said it was still to gain, so the section now says what the record holds: the export travels through the tracker reference, and the merge''s compared versions and holds are kept outside Beads. The other lines are unchanged.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-15T12:26:09.541038Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1010, for document-1010.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-15T13:10:19.311988Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1013, for document-1013.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-10-29T02:56:39.625831Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1930, for document-1930.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 3
      by: harness
      at: 2026-11-05T18:08:29.095053Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2298, for document-2298.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 4
      by: harness
      at: 2026-11-06T19:31:47.681685Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2367, for document-2367.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 5
      by: harness
      at: 2026-11-07T06:45:59.585889Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2415, for document-2415.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 6
      by: harness
      at: 2026-11-07T09:40:34.421244Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2428, for document-2428.1 under the automatic approval policy
---
# Working alongside other contributors

Serves the team goal: a team can run Yoyodyne against one shared repository, with collaborators each running their own harness without losing work, splitting the tracker, or weakening any safety invariant. It answers the design item for working alongside other contributors (yoyodyne-7o2), from Mason's direction of October 9, and keeps the promises of [the team-mode scope](../team-mode-scope.md), which the operator approved on August 19: one tracker, so that every machine sees the same backlog, attributions and reports, and findings and control travel.

The factory is one contributor among others. The others may be people, or another copy of this factory on a different machine. The design has to work the same way in both cases. So everything one contributor learns about another comes through the forge and Git, and never through shared local state. Every machine keeps its own home, as [the machine home](machine-home.md) rules. What a team shares is the repository, its remote, and the forge.

This design replaces the earlier draft, docs/team-mode-coordination.md, which was never adopted. Where the two disagree, this design is the one that applies.

The standing goals apply throughout. Every state below is shown in ordinary words where the operator reads it. Every decision named here belongs to a role, and none is put to the operator for approval.

## 1. A change that waits for a person's merge

Where a project's merge policy leaves a pull request open for a person to merge, a run that has published its approved change stops holding a developer slot. Its item is held in a state of waiting for a person to merge, which names the pull request. The status line, the dashboard and the item's notes say: waiting for a person to merge pull request #N.

On each forge check (section 6), the harness reads the state of every pull request it is waiting on:

- **Merged:** the harness follows the merge locally by fast-forward, closes the item with the merge commit recorded, ends the run as integrated, and releases the claim where team mode holds one. Items that depend on it go on.
- **Closed without merging:** the item goes back to the Lead Product Manager with a note naming who closed it and when. The branch and the run's work are kept, and nothing is deleted. Deciding whether to drop the work, revise it or admit it again is hers.
- **Still open:** nothing changes, except the bringing up to date in section 3.

A merge a person makes is therefore noticed by the next check, not by luck.

## 2. Feedback left on the pull request

On each check the harness reads the reviews and comments on its open pull requests that came after the latest head it pushed. It ignores only what a factory wrote, and reads everything a person wrote, whichever forge account wrote it. The account cannot tell the two apart: on a project with one person, the harness publishes under that person's own forge account, and ignoring by account would throw away the owner's own feedback with the harness's own comments.

So the harness marks what it writes, and keeps a list of it:

- **A mark on everything it writes.** Every pull request description, comment and review the harness writes on the forge ends with a hidden line, `<!-- yoyodyne: <factory identity> -->`, naming the factory identity of section 5, which includes the product id. The forge does not show the line to a reader.
- **A list of what it wrote.** When the forge answers a write, the harness records in the run's record the identifier the forge gave that comment or review.
- **What is ignored.** A comment or review is ignored when its identifier is on the run's list, or when it carries a mark naming this product. The list catches a comment of the harness's whose mark a person removed while editing it. The mark catches a write whose answer was lost, such as one that timed out after the forge had taken it. Together they mean the harness never reads its own words back as a request to repair. Writing marked by another factory of this product is ignored too, because feedback is a person's: no factory reviews another factory's pull request on the forge, and the independent reviewer runs inside the factory that owns the run.
- **Everything else is a person's.** It is read as below, whichever account wrote it, including the account the factory publishes under.

A person's edit to a comment the harness wrote is not read as feedback, because the forge does not reliably say who made an edit; a person gives feedback by writing a comment or review of their own. A person who copies the hidden line into their own comment has that comment ignored. Collaborators are trusted peers, as the team-mode scope says, and the mark protects nothing except the harness from its own words.

What a person wrote is handled this way:

- **A review that requests changes, or a comment on a line of the change,** starts a repair round on the same run. It uses the run's existing worktree and repair budget. The developer is given the feedback as evidence, with its author and time. It is never an instruction that widens what the run may do. The repaired head goes through the configured checks and the independent reviewer again before it is pushed.
- **An ordinary comment on the pull request** is put on the development manager's list. She decides whether it calls for a repair round, because a conversation comment is as often a question as a request.
- **A person's approval on the forge** is recorded as information. It never stands in for the independent reviewer, and it never changes what the item's goal is.

Where the harness publishes under the owner's own account, GitHub does not let that account approve its own pull request or request changes on it. The owner's feedback then arrives as comments: a comment on a line of the change starts a repair round, and any other comment goes to the development manager's list.

A project may instead publish under an account of its own. That changes only what the forge lets the owner do: the owner can then approve and request changes on the forge, and readers of the pull request see the owner's words under the owner's name. It does not change what the harness ignores, which is still only what is marked or on the list, so a person signed in as the publishing account is still read. A separate account is never required. Where a project wants one, the account and its credential are the operator's to supply, and nothing in the harness asks for one.

When the run's repair budget is spent, the run stops in the ordinary way and goes to the development manager, as [stopped-run recovery](fresh-factory-health-and-preserved-run-recovery.md) rules.

## 3. Merges by other contributors

- **Before each run.** Every developer run starts from a target the harness has just fetched from the remote. If the fetch fails, the run starts from the last fetched target. The run's record names that target, the failed fetch, and how old the target is. Landing never trusts the base anyway, so a stale base costs at most a replay. A flaky network never stops a run, under the operating rule on networks.
- **Waiting pull requests.** When a check finds that the target has moved past a pull request the factory is waiting on, the harness brings that pull request up to date. It first withdraws the pull request's merge authority, as [the merge queue design](integration-through-a-merge-queue.md) requires before any rewrite. It then replays the change onto the new target, runs the configured checks and the independent reviewer on the replayed head, and pushes that head. This happens at most once per check for each pull request. A pull request a person is already merging is left alone: the harness reads its state immediately before the push, and does not push over a merge in progress.
- **A conflict while bringing a pull request up to date** is a repair round, given to the developer with the conflicting paths named. It is counted against a limit of its own, three rounds, and not against the item's repair budget, because another contributor's work caused it. Once that limit is spent, the run goes to the development manager with the change kept.

## 4. A limit on pull requests waiting for a person

The configuration key `execution.max_waiting_pull_requests` sets the limit. It defaults to 5, and 0 means no limit. Like the other numeric limits in [portable agent configuration](portable-agent-configuration.md), setting it is the development manager's decision.

At the limit, the scheduler starts no new developer work. Repair rounds, reviews and the bringing up to date of waiting pull requests carry on. Wherever the factory's state is shown (the status line, the dashboard, and the Lead Product Manager's sweep), it says in plain words that it is not starting new work because N pull requests are waiting for a person to merge, and names each one by number and work item. So the pause never reads as an unexplained stall. Work resumes on its own as soon as the count falls below the limit.

## 5. Two factories and the same work

Everything in this section, and in sections 7 and 8, applies only when the project has turned team mode on. A project turns it on with `team.enabled: true` in its committed configuration, so every factory working the repository reads the same value. It is off by default. Turning it on is the Lead Product Manager's decision, because it changes how work is taken from the backlog, and the development manager applies it, as [portable agent configuration](portable-agent-configuration.md) rules for settings a role applies. Where the configuration already names team mode under another key, that key is the one, and build item 6 uses it.

A factory with team mode off works the repository alone. It reads no team pause, takes no claim, fetches no records or tracker reference, and counts no spends across machines. It never stops or delays new work because the remote cannot be reached: its fetch before each run falls back as section 3 says, and its forge check waits out a forge it cannot reach. What it still does is sections 1 to 4, which serve any project whose pull requests can be merged, commented on, or overtaken by someone else.

Team mode needs the harness to publish through pull requests on a forge, because the forge is where contributors learn about each other. `yoyo doctor` refuses team mode, in plain words, for a factory whose publishing is off.

### Claims

A claim is a Git reference on the shared remote, `refs/yoyodyne/claims/<item id>`. It points at a small commit holding a claim record:

- the factory's identity;
- the work item;
- the run;
- when it was taken;
- when it was last renewed.

The factory's identity is the product id, plus a machine identity generated once and kept in that machine's home, plus the forge account the factory acts as.

- **Taking.** Before claiming an item, the harness fetches the claim references, the records reference (section 7) and the team pause, and reads the intake hold, as [the intake invariant](../decisions/invariants/selected-work-passes-intake-and-records-why.md) requires, and the team pause below. It then pushes the claim reference with `--force-with-lease=<ref>:`, which succeeds only if no such reference exists. A refused push means another contributor holds the item. That item is skipped this turn, with the holder named where the queue is read. The local tracker claim is made only after the push succeeds. A factory in team mode that cannot reach the remote cannot push a claim, so it never starts new work on a reading of the team pause it could not refresh.
- **Renewing.** While a run holds the item, including while it waits for a merge or is stopped and kept for a decision, the harness renews the claim every 15 minutes. It pushes a new claim commit with a lease that names the commit it last pushed.
- **Expiry.** A claim not renewed for two hours may be taken over by another factory. The takeover pushes with a lease naming the stale commit, and records the earlier holder in the new claim record and on the item. A failed renewal is a wait caused by something outside the work, and it is tried again on the next turn. Only a claim that has gone two hours unrenewed can be lost.
- **Release.** When the run ends, the harness deletes the reference with a lease naming its own commit. A reference that is no longer its own is left alone and reported.

Agents never touch these references. The harness pushes them, as it already pushes pull request branches. A forge that refuses references under `refs/yoyodyne/` is reported by `yoyo doctor`. Team mode is refused on that forge, in plain words, rather than run without claims.

People are not expected to push claims. A factory notices a person's work only through that person's pull requests, and a person's pull request that implements an item is resolved by the Lead Product Manager, not by the claim.

### The team pause

The team pause is the shared form of the intake hold: the reference `refs/yoyodyne/pause`, set and cleared with `yoyo pause --team`. While it is set, no factory starts new work from the product; runs already in progress finish, and repair rounds and waiting pull requests carry on. Every factory in team mode reads it in the same fetch that comes before a claim. An item the operator named on a machine is still that operator's exception, as the intake invariant already allows. The operator's pause on each machine, set by `yoyo pause`, stops that machine's spending and is unchanged: it does not travel, because one collaborator must not be able to stop another's machine.

The team pause exists only in team mode. A factory with team mode off never fetches or reads it, so a pause reference left on the remote by an earlier team does not stop it, and an unreachable remote never stands in for a pause it could not read. `yoyo pause --team` is refused, in plain words, on a project whose team mode is off.

### The five gaps team mode named

- **The tracker drifting apart between machines.** Each factory publishes its tracker's export through a reference of the harness's own, and merges what the others published item by item, as section 7 describes. The claim reference, not the tracker, is what stops two factories doing the same work, so a tracker that is briefly out of date costs a skipped item, not a duplicate run. When two machines' edits to one item cannot be merged, the harness holds that item for the development manager, naming both versions, and edits neither.
- **No claim that works across machines.** The claims above.
- **The one-merge-at-a-time lock being per machine.** The lease stays per machine. Across machines, the guarantee is the remote's own compare and swap. A protected target lands only through its pull request with the reviewed head pinned. An unprotected target is pushed only with a lease naming the target the candidate was built on. Two factories racing to promote cannot both land: the loser's candidate is invalidated and rebuilt without charging a repair, as the merge queue design rules for target movement. The invariant on one promotion per target branch is amended to say this.
- **Reports, run records and the pause switch being per machine.** Reports, their handling and directives travel between machines, as section 7 describes, and so does what each item has spent, as section 8 describes. The pause gains its team form above. Run records stay per machine, for the reason section 7 gives.
- **No proof of who an operator is.** The factory does not establish one. The forge account is the identity, and the forge's permissions decide who may merge, review or push. A factory acts on its own operator's decisions, on directives that travel (section 7), and on what the forge shows. It never acts on another machine's own controls: that machine's pause, stop, or decisions about that machine's runs. Permissions between users stay out of v1, as the non-goals say.

## 6. Checking the forge

The harness checks the forge on a schedule of its own, every five minutes by default, apart from the maintenance pass. Maintenance does slow work that has nothing to do with the forge, such as clearing old checkouts and shrinking the tracker's store, and a forge check waiting behind it would notice a person's merge late, or not at all while a maintenance step kept failing. The interval is a numeric limit, and setting it is the development manager's decision.

Each check covers the pull requests the factory is waiting on, the reviews and comments on its open pull requests, and the target branch. With team mode on, it also covers the claim references, the records reference, the tracker reference and the team pause. It uses the forge's conditional requests, so an unchanged answer costs little.

A check that cannot reach the forge changes nothing, and the next check tries again. A check that reads a change but cannot make the tracker write that follows from it, such as closing an item whose pull request merged, records nothing as handled: the next check reads the same change and makes the write again. Every write a check makes can be made twice without harm, so a write repeated after a timeout in which it had in fact succeeded leaves the item as it was and adds no second note. Neither failure stops a run or holds back any other work.

Web hooks are rejected. A laptop has no address the forge can reach, and a relay that received them on its behalf would be the hosted control plane v1 excludes. Checking within a few minutes is fast enough for work measured in runs.

## 7. What travels between machines

With team mode on, the backlog and attributions travel in the tracker's export, through the reference `refs/yoyodyne/tracker` on the shared remote, as the last part of this section describes. Reports and control travel through another reference of the harness's own, `refs/yoyodyne/records`, which the next three parts describe.

### What it holds

The records reference points at a commit whose tree holds one file for each record, named by the record's own identifier:

- `reports/<id>.json`: a report, with the factory identity that filed it;
- `report-handling/<id>.json`: what a role did with a report, naming the report;
- `directives/<id>.json`: a directive, and `directive-changes/<id>.json` for each later change to one, naming the directive;
- `spends/<item id>/<id>.json`: one spend of an item's budget (section 8).

A record is never rewritten or removed. A change to something already recorded, such as a report handled or a directive resolved, is a new record naming the earlier one.

### Writing

The harness builds each new commit on top of the records commit it last fetched, using Git's own object commands in the machine's state directory. No working tree is checked out and no file in the repository is written. It pushes with a lease naming the commit it fetched. A refused push means another factory pushed first: the harness fetches, builds its commit again on the newer one, and pushes again. Because every record has a path of its own, building again never has to choose between two contents for one file.

Where the path is already present on the remote, the record is already there. The harness drops its own copy and uses the one that stands. This is what makes a directive recorded from a Slack reply exactly one directive: such a directive is named by the Slack message's id, so when two factories' Slack connections record the same reply, the first push wins and the second names the directive that stands. A Slack connection acknowledges a reply only after its push succeeded or it found the reply already recorded, and the acknowledgement names the directive.

A report, its handling, or a directive that cannot be pushed because the remote cannot be reached is kept on the machine, marked as not yet shared, and pushed on the next turn. The status line says how many records are not yet shared. A spend is different, as section 8 says.

### Reading

On each check and in the fetch before each claim, the harness fetches the reference and copies every record it has not yet seen into the machine's own report and directive stores, marked with the factory that wrote it. Every surface keeps reading those stores through the one read model, as [the read model invariant](../decisions/invariants/surfaces-project-one-read-model.md) requires, so no surface reads Git and a report reads the same wherever it was filed. A role handling a report from another machine records its handling as a record that travels back.

### The tracker's export

Beads has no merge of its own for its export. The run building tracker sharing (yoyodyne-ifd.429.106) tested `bd` 1.1.2 and recorded this: Git merges the file line by line, so edits to neighbouring items conflict and so do two machines each adding an item; `bd import` refuses a file with a conflict in it; nothing brings a merged export into the tracker except `bd import`; and `bd import` keeps the later write without saying so, and adds labels and dependencies but never removes them. So the harness does the merge itself, and the export does not travel on the target branch.

**How it reaches the other machines.** `refs/yoyodyne/tracker` points at a commit whose tree holds the export. The harness builds that commit as it builds a records commit, using Git's own object commands in the machine's state directory, and pushes it with a lease naming the commit it fetched. It is not a branch anyone merges, so it needs no pull request, no review and no promotion, and a protected target branch cannot stop it. Nothing a factory shares this way depends on a commit reaching the target branch. Nothing reads the reference's older commits.

**Each check, in order.** The harness fetches the reference and merges what it holds into this machine's tracker. It then publishes this machine's export, where that differs from what the reference holds. A refused push means another factory published first: the next check merges that and publishes again. A remote or tracker that cannot answer changes nothing, and the next check tries again.

**The merge.** It goes item by item, comparing each side with what this machine last read from the reference:

- a change only one side made is taken;
- changes to different fields are combined, as are appended notes, labels, comments, dependencies and metadata keys, each note appearing once;
- two values for one field, a note rewritten rather than added to, or an edit meeting a removal, is a conflict.

**A machine's own earlier versions.** Each machine keeps a record, in its own state, of the versions of each item it has published, up to a bound. A version in the reference that this machine published is this machine's own past and never a clash, so a machine's later edit to its own item causes no hold.

**A held item.** A conflict holds the item for the development manager. The hold is kept in this machine's state, not on the item, and neither version is edited. Nothing the harness chooses starts a held item. She gets one report naming both versions, and another only if a version changes. She decides which version stands, and the next check carries that out; a decision that cannot be carried out is refused in plain words and the hold stands. A hold also ends once the two versions agree. While an item is held, what this machine publishes carries the reference's version of that item unchanged, so publishing never overwrites the other machine's edit. Holds apply only with team mode on: with it off, no hold stops any work, and no decision about one is taken.

### What stays on each machine

Run records, worktrees, event logs, provider sessions, conversations, the spend ledger, and the operator's pause stay on the machine that holds them. They describe one operator's own runs, money and afternoon, and two machines holding different answers about them are both right. What another contributor needs to know about a run is what became of its item: its status, notes, reports, claim and pull request, all of which travel. The scope does not promise that cost travels, so each operator sees what their own factory spends; a total across machines is not designed here.

## 8. Each item's budgets across machines

The scope records that an item's budgets are counted on the machine that counted them, so a cap of one is a cap of two across a pair. The budgets are the repair grants an item is given, the re-runs it causes, the merge re-arms it makes, and the review rounds it costs across every run of it. With team mode on, this design counts them across every factory. With team mode off there is one factory, and its own count is the whole count.

- **Only the claim holder spends.** Only the factory holding an item's claim spends any of the item's budgets, and a decision about a stopped run is carried out on the machine whose run it is, which still holds the claim. The claim already lets one factory hold an item at a time, so spending is ordered across machines without a shared counter or a tie-break.
- **Every spend is a record.** A spend is written under `spends/<item id>/` in the records reference, naming the budget, the run, the factory and the time. Review rounds, counted today from local run records, are counted from these records instead: a run records each review round when it starts.
- **Counted before spending, acted on after pushing.** Before spending, the holder fetches and counts the item's spends of that budget across all factories against the configured cap. It pushes its spend record and acts only after the push succeeds. A spend that cannot be pushed is not made: the run waits, as a wait caused by something outside the work, and nothing is charged.
- **A takeover inherits what was spent.** A factory taking over an expired claim fetches the item's spends first, so it starts from what the earlier holder spent.
- **Different caps.** Where factories are configured with different caps for one budget, each applies its own cap to the count across all factories, so the lower cap is the one an item meets.
- **Joining.** When a machine first runs in team mode, it pushes the spends it had already counted on its own items as spend records, once, so nothing spent before is forgotten.

## Alternatives rejected

- **Claims in the tracker alone.** The tracker reaches other machines only when its export is published and fetched, so two factories could each claim an item before either saw the other's claim.
- **Claims as files on the target branch.** Every claim would be a commit to main, through review and the promotion lease, for something that has to take seconds.
- **A shared lock service.** It would be shared state outside Git and the forge, which the operator's direction rules out.
- **Sharing the export by committing it to the target branch.** On a protected target every tracker change would need a pull request, a review and a promotion, and the export would conflict on most merges because new items go at the top of the file.
- **Leaving the merge to Git and `bd import`.** Git conflicts on neighbouring items that do not clash at all, and `bd import` keeps the later write without saying so, which loses one machine's edit with nothing reported.
- **Beads' own Dolt remotes.** The help for `bd init` says cross-machine sync uses them rather than the export. That would add a place contributors learn about each other outside Git and the forge, which the operator's direction rules out. Whether a Dolt remote can sit on the same Git remote has not been read for this design; if it can, this choice is worth looking at again.
- **Treating every pull request comment as a repair request.** It turns questions into repair rounds and spends budget on conversation.
- **Ignoring feedback by the account that wrote it.** On a project with one person the harness publishes under the owner's own account, so the owner's feedback would be thrown away with the harness's own comments.
- **The hidden mark alone, or the list alone.** With the mark alone, a person editing one of the harness's comments could remove the mark, and the comment would then be read as that person's feedback. With the list alone, a write that timed out after the forge had taken it would be read back as feedback.
- **Requiring a separate publishing account.** It would cost every project with one person an extra account and a credential before the factory could take feedback, which the readme's first run and the aim of needing the owner only where a person is needed both argue against.
- **Keeping reports on each machine.** It breaks the scope's promise that findings travel: a finding about shared work would reach only the operator whose run noticed it.
- **Reports and spends as tracker items or notes.** A report, a directive and a spend are not work, and putting them in the tracker would put them in the queue of work to pull.
- **A shared counter for budgets with a tie-break.** The claim already decides who may spend, so a second way of deciding would only be a place for the two to disagree.
- **Reading the team pause and taking claims on every factory, team mode or not.** A factory working a repository alone would stop new work whenever the remote could not be reached, which the operating rule on networks forbids, and a pause reference left behind by an earlier team would stop it with nobody left to clear it.
- **Checking the forge inside the maintenance pass.** How long maintenance took would decide how late a person's merge was noticed, and a maintenance step that kept failing would take the forge check down with it.

## What is enforced in code

- With team mode off, the harness reads no team pause, takes no claim, and fetches no records or tracker reference, and no start of new work waits on the remote.
- With team mode on, a claim is taken only by a push that fails if the reference exists. It is renewed, taken over and released only by a lease naming the expected commit. No agent touches a claim reference.
- The intake hold is read before every start. With team mode on, the team pause is read before every claim, from the fetch made for it.
- The forge check runs on its own schedule, not inside the maintenance pass, and a change it read but could not finish acting on is read and acted on again at the next check.
- Everything the harness writes on a pull request carries the hidden mark, and the forge identifier of each write is recorded in the run's record. Feedback is ignored only when its identifier is on that list or it carries a mark naming this product; the account that wrote it is never the test.
- A rewrite of a published head happens only after its merge authority is confirmed withdrawn.
- Nothing lands on the target except through the pinned pull request merge, or a push with a lease naming the candidate's base.
- A person's approval on the forge never satisfies the independent review.
- Records are written only through Git objects in the machine's state directory, pushed with a lease, and never rewritten or removed. No agent writes one directly.
- The tracker reference is written only through Git objects in the machine's state directory, pushed with a lease. No agent writes it, and what a machine publishes carries the reference's own version of any item it holds.
- Only the claim holder spends an item's budget, and a spend is acted on only after its record is pushed.

## What a reviewer can check

- An item waiting for a person names its pull request wherever it is shown, closes when that pull request merges, and goes to the Lead Product Manager when it is closed unmerged.
- Changes requested on the pull request start a repair round on the same run; an ordinary comment reaches the development manager's list.
- On a project that publishes under the owner's own account, the owner's comment on a line of the change starts a repair round, while the harness's own comments start nothing, including one whose write timed out after the forge had taken it.
- Every run's record names the target it started from and whether that target was fetched for the run.
- A conflict while bringing a pull request up to date is a repair round counted against its own limit.
- At the limit on waiting pull requests, no new developer work starts, and the status line names the pull requests being waited on.
- With team mode off and the remote unreachable, new work still starts, and no claim or pause reference is read.
- A pause reference left on the remote does not stop a factory whose team mode is off, and `yoyo pause --team` is refused there.
- A forge check keeps its schedule while a maintenance pass is running.
- A pull request that merges while the tracker write closing its item times out has its item closed at the next check, with one note.
- Two factories racing for one item: exactly one claim push succeeds.
- A claim unrenewed for two hours can be taken over, and the takeover names the earlier holder.
- A report filed on one factory is shown on the other after its next check, naming the factory that filed it, and its handling travels back.
- One Slack reply seen by two factories produces one directive, and both acknowledgements name it.
- With a repair cap of one, two factories working one item in turn spend one repair between them.
- A spend whose push fails is not acted on, and the run waits without being charged.
- With the team pause set, no factory starts new work, and runs in progress finish.
- Two factories editing different items, against a protected target, each hold both edits after their next checks, with no commit on the target.
- One item changed two ways on two factories is held on the factory that finds it, naming both versions, with neither edited and the other factory's version still in the reference.
- A machine that published a version of its own item and then edited it again is not held.
- A decision on a hold is carried out by the next check, or refused in plain words with the hold still standing.
- With team mode off, a hold left behind stops no work.

## Build items, for the Lead Product Manager to admit under `team-development`

1. The state of waiting for a person to merge: close the item on merge, and send a pull request closed without merging back to the Lead Product Manager with the work kept.
2. Feedback on the pull request turned into repair rounds, and ordinary comments put on the development manager's list. It includes the hidden mark on everything the harness writes on a pull request, the run's list of the forge identifiers of what it wrote, and ignoring feedback by those rather than by account; the feedback build already admitted (yoyodyne-ifd.429.100) can be widened to carry it.
3. Fetch before each run, with the starting target and its age recorded.
4. Waiting pull requests brought up to date when the target moves, with conflicts as repair rounds under their own limit.
5. The limit on waiting pull requests, and the pause it causes, said in plain words on every surface.
6. The team mode setting, off by default, and claim references: taking, renewing, expiry and takeover, and release, plus the `yoyo doctor` checks that the forge accepts them and that publishing is on.
7. The factory identity, kept in the machine's home and named in claims, records and pull request descriptions.
8. The team pause reference and `yoyo pause --team`, read in the fetch before every claim and only with team mode on, and refused on a project whose team mode is off.
9. Tracker sharing between machines: establish how Beads merges its export, merge it item by item, and hold an item whose versions cannot be merged. The run building this (yoyodyne-ifd.429.106) reads the export from the target branch; item 15 moves that reading.
10. Checking the forge on a schedule of its own, apart from the maintenance pass, with conditional requests, acting again at the next check on a change whose tracker write did not go through; the claim, records, tracker and pause references are covered only with team mode on.
11. The records reference: writing records through Git objects with a lease, fetching and copying them into the machine's stores, the count of records not yet shared, and Slack directives named by message id.
12. Each item's budgets counted across factories through spend records, spent only by the claim holder, acted on only after the push, and the one-time push of earlier spends on joining.
13. The earlier draft docs/team-mode-coordination.md replaced by a short note naming this design as its replacement. The file is outside the architect's folders, so a developer run changes it.
14. A two-factory validation: two homes on one machine against one remote, covering every check under What a reviewer can check.
15. Publishing the tracker's export through `refs/yoyodyne/tracker`: building and pushing the commit with a lease at each check, moving the merge's reading from the target branch to this reference, keeping each machine's record of its own published versions in its own state, and carrying the reference's version of any held item.

No build that lets a second factory run can start before claims (6), because two factories without claims duplicate work. Budgets (12) also need the records reference (11). Publishing the export (15) needs the merge (9). The others can be taken in any order.

## Documents this amends

- [Beads is the durable workflow store](../decisions/beads-durable-workflow-store.md) gained its consequence on October 15, 2026. The claim reference is the cross-machine guarantee for who holds an item, while the tracker still records the holder; reports, directives and spends travel in the records reference beside it, not in the tracker. On November 7, 2026 it gained what section 7 rules for the tracker's export: the export travels through the tracker reference rather than a commit on the target branch, the harness merges it item by item, and the versions the merge compares and a hold on an item whose versions clash are kept in the machine's own state, outside Beads.
- [One promotion per target branch](../decisions/invariants/one-promotion-per-target-branch.md) gained the remote compare and swap as the guarantee across machines on November 5, 2026. The lock stays per machine and covers only the guarded change.
- The v1 harness design's text on fetching only during reconcile is replaced by section 3. This waits on the fix that lets roles read and revise long files (yoyodyne-t2j).
- The Lead Product Manager's team-mode scope document should say that working alongside other contributors is designed, name this design, and say that its question on a team-wide pause and its gap on budgets are answered here. That change is hers.
- The earlier draft docs/team-mode-coordination.md is replaced by this design, through build item 13.
