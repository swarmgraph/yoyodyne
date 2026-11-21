---
id: integrating-to-a-local-branch
kind: design
title: Integrating to a local branch and handing landed work to the team through a pull request the harness tracks or opens
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-07T19:44:19.303564Z
      reason: 'yoyodyne-ifd.429.127: the design for integrating to a branch other than the team''s main and handing landed work to the team through a pull request the harness tracks. Items stay open until the team merges them; an outside pull request is tracked by an explicit, forge-checked record; the local branch is kept current by a merge that earns checks and independent review; and an outside tool can hold landing for a bounded time. A separate document because the contributors design is close to the read limit; it extends that design''s sections 1 to 3 and changes none of them.'
    - action: amended
      by: architect
      at: 2026-11-15T04:46:30.240554Z
      reason: 'yoyodyne-ifd.429.169: records in section 3 the ruling of November 12, 2026 that the harness''s own merge of the team''s branch, bound by its recorded parents and a tree identical to the merge the harness builds again under the landing lease, stands as acceptance on protected paths; anything a run writes keeps the ordinary check, checks and review still run, and the evidence invariant keeps no list of exceptions so it needs no pointer.'
    - action: amended
      by: architect
      at: 2026-11-17T02:17:03.413365Z
      reason: 'yoyodyne-ifd.429.176 and yoyodyne-ifd.429.198: records the architect''s ruling of November 13, 2026, extended November 16, on how the development manager''s decision to repair an item whose run has ended is recorded, paid for and run. It is a decision of its own kind bound to the list entry it answers, refused once the item has closed; it starts a new run, from the target branch''s head for an item waiting on the team''s merge and from the kept branch for an item waiting on a pull request the harness opened (the case from yoyodyne-ifd.429.120); it spends one of the item''s attempts, except a conflict repair, which spends one of the update''s three rounds. Section 2, section 3''s conflict paragraph, the enforced list, the reviewer checks and build items 3 and 4 change; the rest is copied unchanged from main 668e95f1.'
    - action: amended
      by: architect
      at: 2026-11-20T18:57:51.760645Z
      reason: 'yoyodyne-ifd.429.127.1: records the operator''s November 19, 2026 narrowing of the November 7 ruling. A batch pull request becomes a fourth delivery route the harness opens, grows, tracks and merges under a few bounded settings, and no outside code holds push or merge rights. Adds section 5, rewrites the rejected alternative about the harness opening the batch, maps the seven asks to build items, and adds build items 7 to 14. Sections 1 to 4 are unchanged from main 18d599c6.'
    - action: amended
      by: architect
      at: 2026-11-21T20:32:41.401529Z
      reason: 'amendment-3f15bd62, approved Nov 21: a repair of an item waiting on a pull request the harness opened applies the kept branch''s change onto the target branch''s current head and replaces the kept branch''s head by compare and swap against the head the harness last wrote; a refused push keeps the change and goes to the development manager''s list. Adds one rejected alternative. The rest of section 2 is copied unchanged from main 0d6453a6.'
    - action: amended
      by: architect
      at: 2026-11-21T21:18:03.568316Z
      reason: 'Follows amendment-3f15bd62, landed in section 2 on Nov 21: build item 3 now says a repair of an item waiting on a pull request the harness opened applies the kept branch''s change onto the target branch''s head, replaces the kept branch''s head by compare and swap, and keeps a refused push for the development manager''s list. Drops the closing line saying the contributors design should point here. The rest of the section is copied unchanged from main a99aedc98.'
    - action: amended
      by: architect
      at: 2026-11-21T22:06:04.391207Z
      reason: 'Follows amendment-3f15bd62, landed in section 2 on Nov 21: the reviewer check for a repair of an item waiting on a pull request the harness opened now says the run starts from the kept branch''s change applied onto the target branch''s head, replaces the kept branch''s head by compare and swap, and keeps a refused push for the development manager''s list. The rest of the section is copied unchanged from main 936110b14.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-07T19:44:19.303564Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2448, for document-2448.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-11-15T04:46:30.240554Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2777, for document-2777.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-11-17T02:17:03.413365Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2922, for document-2922.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 3
      by: harness
      at: 2026-11-20T18:57:51.760645Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3206, for document-3206.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 4
      by: harness
      at: 2026-11-21T20:32:41.401529Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3343, for document-3343.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 5
      by: harness
      at: 2026-11-21T21:18:03.568316Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3346, for document-3346.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 6
      by: harness
      at: 2026-11-21T22:06:04.391207Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3348, for document-3348.1 under the automatic approval policy
---
# Integrating to a local branch and handing landed work to the team

Serves the team goal: a team can run Yoyodyne against one shared repository, with collaborators each running their own harness without losing work, splitting the tracker, or weakening any safety invariant. It answers the design item for integrating to a branch that is not the team's main (yoyodyne-ifd.429.127), from a user's setup the operator passed on on November 7, 2026, and the item revising it for batch pull requests the harness opens itself (yoyodyne-ifd.429.127.1), and stays inside [the team-mode scope](../team-mode-scope.md).

In that setup the factory lands its work on a branch of the project's own, such as `staging`, and a batch of landed items goes to the team's branch, such as `main`, in one pull request. Which items go into a batch, and when, is that project's policy. A project can keep that policy in a script of its own, which opens the pull request and tells the harness about it (sections 2 and 4). Or, since the operator narrowed the November 7 ruling on November 19, 2026 (recorded on yoyodyne-ifd.429.128), it can state the policy as a few settings and have the harness open, grow, track and merge the batch pull request itself (section 5). That is a fourth delivery route beside a pull request for each change, the merge queue, and landing on a local branch alone. On that route no outside code holds push or merge rights.

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

- **A review that requests changes, or a comment on a line of the change,** is put on the development manager's list as one entry, naming the pull request, the comment, and the items whose landed commits touched the file it is about.
- **An ordinary comment** is put on her list in the same way, with no item named.
- **Her decision** to repair names one item, and is recorded and carried out as the next part of this section says.
- **A person's approval** is recorded as information and never stands in for the independent reviewer.

Tracked pull requests are not counted against `execution.max_waiting_pull_requests`. That limit stops new work while the harness's own pull requests wait, and work landing on the target branch does not wait on the team's merge.

### Deciding a repair of an item whose run has ended

The development manager's other decisions are each bound to a stopped run waiting for her. An item waiting for the team's merge has none: its run ended as integrated and freed its slot. So a repair from feedback is a decision of its own kind on the fixed list of decisions she can record, bound to the list entry it answers rather than to a run.

- **What is recorded.** The decision names the entry it answers, one item, and her reason, with who decided and when. It is refused, in plain words, when that entry already has a decision, when the item has closed, when the item is not waiting on a merge, or when the item has no attempts left. An entry is answered once; later feedback on the same pull request makes a new entry.
- **A new run, never a continued one.** The decision starts a new run of the item. The earlier run stays ended as integrated, exactly as it was recorded. The new run's record names the decision and the entry it answers, so a reader can follow the chain from the team's feedback to the change it caused.
- **Where the run starts, and where its change goes.**
  - An item landed on the target branch and waiting for the team's merge starts from the target branch's current head. Its change lands on the target branch, the item stays waiting for the team's merge, and the tracked pull request picks the new landing commit up by the rule for items found among its commits.
  - An item whose change waits on a pull request the harness opened, for a person to merge (section 1 of the contributors design), starts from the change that pull request's kept branch carries, applied onto the target branch's current head, rather than from the kept branch's old head. The run's change then replaces the kept branch's head and updates the same pull request rather than opening another, so the reviewer judges the whole change against the target branch as it now stands. The replacing push is a compare and swap against the head the harness last put on that branch. Where the branch holds anything else, such as a commit a person pushed there, the push is refused and nothing on the branch is overwritten: the run's change is kept, and the development manager's list gets one entry naming the branch and the commit found there. This is the same gap the item for deciding a repair of such an item raised (yoyodyne-ifd.429.120, now folded into yoyodyne-ifd.429.176), and one kind of decision covers both.
- **What the developer is given.** The feedback is given to the developer as evidence, never as an instruction that widens what the run may do.
- **Who pays.** The run spends one of the item's attempts, counted in the same unit as the development manager's other repair and re-run grants, as the fixed list of decisions in stopped-run recovery is built (yoyodyne-ifd.428.99.4). An item with no attempts left goes to the Lead Product Manager, as any item that has used its attempts does.
- **When it runs.** A freed developer slot takes it as it takes any decided repair, ahead of fresh work, under the operating rule that work on a change a run already wrote goes first.
- **Checks and review.** The run goes through the configured checks and the independent reviewer as any run does. A person's comment or approval never stands in for either.

A conflict while bringing the target branch up to date (section 3) is decided with the same kind of decision, bound to the conflict's entry. It differs only in who pays, as that section says.

Rejected:

- **Continuing the run that landed.** It ended as integrated, its slot is freed and its checkout may be gone; reopening it would give one run two endings and break the record of what landed when.
- **Admitting a new item for each piece of feedback.** It loses the link between the feedback and the item it is about, and spends a Lead Product Manager decision on routine repair.
- **Charging the repair to no budget.** Feedback on a busy pull request could then start runs without limit.
- **Continuing the kept branch's history from its old head, for a pull request the harness opened.** The reviewer would judge only the new commits, on a base the target branch may have moved past, and the pull request could carry a conflict nobody checked.

## 3. Keeping the target branch current with the team's branch

On each forge check, after its fetch, when the team's branch has moved past what the target branch holds, the harness brings the target branch up to date:

1. It builds, in a checkout of its own, a merge of the team's branch into the current target branch. It never rebases the target branch, because other pull requests, the tracked ones included, point at its commits.
2. It runs the configured checks and the independent reviewer on that exact merge commit. The rule that [integration requires evidence bound to the exact candidate](../decisions/invariants/integration-requires-revision-bound-evidence.md) applies to this merge as to any change, so a merge that changes no line of its own still earns both. The reviewer is told that the change brings in the team's merged work and is judged on whether the merge is faithful and the checks pass.
3. It lands the merge as [one promotion per target branch](../decisions/invariants/one-promotion-per-target-branch.md) rules: under the landing lease, with the remote's compare and swap. A target that moved meanwhile means the merge is built again, as for any candidate.

This happens at most once per forge check, and one merge covers everything the team's branch gained since the last one, so review is spent once per check rather than once per teammate's change.

Each update is recorded beside the target branch's landing records: the team's commit brought in, the target commit the merge was built on, the merge commit and its tree, the checks, the reviewer's verdict, and when it landed. The status line shows when the target branch was last brought up to date, and the update waiting, if any.

**A conflict** is not resolved by the harness. The update waits, and the development manager's list gets one entry naming the conflicting paths and the items on the target branch whose landed commits touched them. Her decision is the same kind as a repair from feedback (section 2), bound to this entry: it names one item, and starts a new run of it, from the target branch's current head, that resolves the conflict on the target branch. That run is counted against a limit of its own, three rounds for the update, and not against the item's attempts, as section 3 of the contributors design rules, because another contributor's work caused it. Work keeps landing on the target branch while the update waits.

### The check on protected paths for this merge

Every change that lands on the target branch is checked for changes to the paths the project protects, the files no run may write. The team's merged work was written by people on the team's branch, and may change those paths for good reason. Checked as if a run had written it, the merge would be refused, and the target branch would never catch up with the team's branch.

So this merge lands without that check. In its place it carries proof that no run wrote any of it:

- its first parent is the target commit the update recorded, and its second parent is the team's commit the update recorded;
- its tree is identical to a merge of those two commits that the harness builds again itself, under the landing lease, immediately before the guarded change.

Only the harness's own merge step in step 1 records this proof, and only the landing action in step 3 checks it. A merge that fails either part is not refused for that alone: it is treated as any other change and checked on protected paths. Configured checks and independent review run on the merge whatever the proof says; the proof stands in for the check on protected paths and for nothing else.

Anything a run writes keeps the ordinary check. A merge whose tree differs in any file from the one the harness builds, including a merge carrying a run's change that resolves a conflict, is checked on protected paths, and a refusal goes to the development manager's list naming the paths.

The rule that integration requires evidence bound to the exact candidate names acceptance on protected paths as one piece of the evidence every landing needs, and keeps no list of exceptions. For this merge, the proof above is that acceptance, bound to the exact commit by its parents and its tree, so the rule stands as it is and needs no change.

Rejected:

- **The ordinary check on protected paths.** It refuses a team's own change to a protected file, and the target branch then never catches up.
- **Dropping the check with no proof in its place.** A run's change could then reach a protected path by riding inside the merge.
- **Trusting the branch name or the commit message.** Either says who claims to have made the merge, and nothing about what its tree holds.

A reviewer can check:

- A merge whose parents are the recorded commits and whose tree matches the harness's own lands without the check on protected paths, and still passes the configured checks and independent review.
- A merge whose tree differs in any file, such as one carrying a run's change that resolves a conflict, is checked on protected paths, and a refusal reaches the development manager's list naming the paths.
- The update record names the target commit the merge was built on and the merge's tree.

## 4. A hold on landing that an outside tool can set

An outside tool that needs the target branch still while it builds a batch can set a hold:

- `yoyo landing hold <target> --for <duration> --reason <text>` stops the harness landing anything on that target branch until the hold is released or expires;
- `yoyo landing release <target>` releases it.

The duration is required and may not exceed four hours, so a script that dies cannot stop landing for good. The hold is read under the landing lease, immediately before the guarded change, with the other holds the merge-queue design rereads there. While it stands, checks and reviews carry on, and a change ready to land waits with its evidence kept. A target branch that moves while it waits still needs its evidence earned again, as always. The status line names the hold, its reason, and when it ends.

The hold stops landing on one target branch on this machine, nothing else. It is not `yoyo pause`, which stops this machine's spending, and not the intake hold, which stops new work. It does not travel between machines.

A script does not need the hold if it builds its batch from a commit it has chosen, rather than from the moving head of the target branch, and that is the simpler way.

## 5. The harness's own batch pull request

The operator ruled on November 19, 2026 that batching rules stay the project's, as settings, and the mechanics move into the harness. This section is that route. It came from a user's project that had run the batch as an outside shell job of about 720 lines every 15 minutes since November 8: a second control loop the harness could not see, forge logic rewritten in shell, Lead Product Manager conversations started from outside, and the review request posted by hand.

### What is set, and by whom

The route is on when `execution.upstream_branch` is set (section 1) and `execution.batch_pull_request.enabled` is true. Both are off by default, and with either off nothing in this section runs. Turning it on is the development manager's decision, applied as portable agent configuration rules for settings a role applies, like the team's branch.

The settings are few. Each is checked against bounds held in code when the configuration loads, and a value outside them refuses the configuration in plain words:

| Setting | What it says | Bounds | Default |
| --- | --- | --- | --- |
| `min_items` | how many unpublished items open or grow a batch | 1 to 100 | 5 |
| `max_wait` | how long the oldest unpublished item waits before a batch opens or grows with fewer | 15 minutes to 14 days | 24 hours |
| `review_window` | the days and hours, in a named time zone, when a batch may open or grow and a review request may go out | at least one hour a week; unset means any time | unset |
| `automated_reviewer` | the forge account of a review tool whose review the harness waits for and whose findings go to the Lead Product Manager | one account, or unset for none | unset |
| `review_request` | a command, or an https address, told when a batch is ready for a person, with its format, `json` or `text` | one of the two | unset, meaning no request is sent |
| `merge_method` | how the forge merges the batch, `merge` or `squash` | one of the two | `merge` |

Nothing else about batching is configurable, and no general way to plug outside code into the harness is built from this one case.

### Where each item stands

On this route an item waiting for the team's merge (section 1) is also in one of these states, shown by `yoyo status`, the dashboard and the item's notes:

- **unpublished:** on the target branch, in no open batch;
- **in the open batch:** its landing commit is in the batch pull request;
- **approved:** in a batch a person has approved on its head commit;
- **merged:** the batch merged, and the item closed as section 2 closes it.

The status line also says what the open batch is waiting on: the review window, the threshold, the automated reviewer, a person's approval, or the forge reporting it mergeable.

### Opening and growing the batch

The harness keeps the batch on a branch of its own, `yoyodyne/batch/<target>`, and never uses the target branch itself as the pull request's head. A head that moves whenever work lands could be neither held still for an approval nor held back for the automated reviewer.

On each forge check, inside the review window:

1. With no batch pull request open into the team's branch from this target, and `min_items` unpublished items or one older than `max_wait`, the harness opens one. It pushes the batch branch at the newest target commit that keeps every item whole, and opens the pull request from it. Before opening, it looks for an open pull request from that branch and takes it up rather than opening a second, so a restart never opens two.
2. With one open and not approved, and the same threshold met by items landed since its head, it moves the batch branch forward to the newest target commit that keeps every item whole. Where `automated_reviewer` is set, it first waits until that account has submitted a review on the exact head commit. A review tool that has not finished two hours after the head was pushed is passed over: the harness grows the batch, and says so on the pull request and the status line, naming the commit that went unreviewed.
3. With one open and approved by a person on its head commit, the batch is frozen. Items that land after the approval wait for the next batch, which opens after this one merges or closes.

A target commit keeps every item whole when no item landed at or before it has a decided repair run that has not ended (section 2). An item is never published with a repair half done.

Every push of the batch branch is a compare and swap against the head the batch record holds, so two harnesses never both move it. The one whose push is refused reads the branch again and does nothing more in that check.

One batch pull request is open per target branch at a time. It is tracked by the record of section 2 from the moment it opens, without `yoyo pull-request track`, so a merge closes its items, a close without merging sends them to the Lead Product Manager, and items found among its commits are added, exactly as section 2 says. It is not counted against `execution.max_waiting_pull_requests`, for the reason section 2 gives.

### Keeping the batch current with the team's branch

While the batch is not approved, bringing the target branch up to date (section 3) is enough. The next growth carries the update merge, which already passed checks and review.

A batch approved before the team's branch moved cannot take later target commits without carrying items nobody approved. So the harness brings the batch branch itself up to date, as section 3 brings the target branch: it builds a merge of the team's branch into the batch head in a checkout of its own, runs the configured checks and the independent reviewer on that merge commit, carries the same proof in place of the check on protected paths, and pushes it by compare and swap. The head has then moved, so the approval no longer stands on it. The harness says on the pull request that the team's branch moved after the approval and that the new head needs approval, and sends the review request again.

Where that merge conflicts, the harness does not resolve it. The conflict goes to the development manager's list as section 3's conflicts do. Once the target branch's own update has landed, the batch is no longer frozen: it grows to that update and asks for approval again on the new head.

### Describing the batch

Each time the batch opens or grows, the Lead Product Manager writes its description, because what a batch is for is product intent and hers to say. The harness gives her one turn whose only material is the batch's items, with their titles, the goals they serve and their landing commits, and no tools. Her answer is posted as the pull request's description, and its cost is recorded against the batch.

If her answer has not come within fifteen minutes, or the turn fails, the harness posts a plain list of the items, says on the pull request that her description is still to come, and asks again at the next forge check, at most three times for one head. Publishing never waits on her past that.

### Findings from the automated reviewer

Where `automated_reviewer` is set, each review comment and line comment that account leaves on the batch becomes one entry on the Lead Product Manager's list. The entry names the finding, the files it is about, and the items whose landed commits touched them. A review tool's findings range from defects to matters of preference, and deciding which deserve work is backlog work, so they go to her rather than to the development manager. Her decision is one of three:

- **decline**, with her reason;
- **admit a fix item**, under the ordinary admission rules, whose record names the finding it answers;
- **ask for a repair of one named item**, which goes to the development manager's list as feedback does in section 2 and is decided there.

The harness posts her answer on the pull request as a reply to the finding: her reason, the fix item's title and identifier, or that a repair was asked for. A fix item lands on the target branch like any work, and joins the batch at its next growth, or the next batch if this one is frozen. Feedback from people, and the development manager's handling of it, stays as section 2 says.

### Asking a person to review

Once all of the following hold, the harness sends one review request for the head commit to the configured command or address:

- the batch has opened, grown, or been brought up to date;
- its description is posted, or the plain list in its place;
- the automated reviewer has finished with the head, or been passed over;
- the time is inside the review window.

The request goes out in one of two ways:

- **A command** is run by the harness without a shell, from a temporary directory, with the request on its standard input. It is given none of the harness's forge, tracker or provider credentials, and it is stopped after 60 seconds. The request counts as sent when the command exits with status 0.
- **An https address** is sent the request as a POST. It counts as sent when a success answer comes within 10 seconds.

With `json`, the request is one object naming:

- the pull request's number, address and title;
- the target and team's branches;
- the head commit;
- each item's identifier and title;
- the description;
- why it is sent: opened, grown, or brought up to date.

With `text`, it says the same as plain lines.

A request that fails is tried again at each forge check. After three failures, the development manager's list gets one entry naming the error, and the batch carries on. The head commit a request was sent for is recorded, so a restart does not send it again. A crash between sending and recording can send it twice.

### Merging on approval

The harness merges the batch only when all of these hold, read from the forge in one check:

- a person's approval is on the head commit, and no person's request for changes stands;
- the forge reports the pull request mergeable, and its required checks passed;
- the head contains the team's branch head, so the merged tree is exactly the head's tree, and every piece of that tree earned its evidence when it landed on the target branch.

It merges under the landing lease for the team's branch, as [one promotion per target branch](../decisions/invariants/one-promotion-per-target-branch.md) rules. The forge's merge call names the expected head commit, and uses `merge_method`.

When any condition fails, or the forge refuses the merge, the harness says plainly on the pull request and the status line what is missing: an approval on an older commit, a check that failed, or a team's branch the head does not contain. No role is asked to decide anything for it, and the next forge check tries again.

A merge closes the items as section 2 says, a squash merge included.

### Ownership and restarts

The batch record is kept in the harness's state directory and written after each step. It holds the pull request, its head, its items, the approval seen, the automated reviewer's state, the requests sent, and the description tries.

Every step is decided from a fresh forge read and this record, so a restart repeats nothing that was finished:

- an open batch pull request is taken up, never opened twice;
- every push is a compare and swap;
- a request is sent once for each head.

Only the harness opens, pushes, comments on and merges the batch pull request. No agent session holds a forge credential on this route, and the review request command runs without one.

### The seven asks, and what already provides them

| Ask | Already provided by build items 1 to 5 | Still to build |
| --- | --- | --- |
| 1. A target branch the harness knows, with each item shown as unpublished, in an open pull request, approved or merged, on `yoyo status` | Items 1 and 2: waiting for the team's merge, a tracked pull request, closing on merge | the batch states on every surface, the approved state, and what the batch waits on (item 7) |
| 2. Publication in review hours and on a count or time threshold, items whole, one open pull request, grown until approved, frozen by approval, grown only after the automated reviewer finished | nothing | item 8 |
| 3. Bringing in the team's branch, checked before it moves the target, with conflicts raised | Item 4, for the target branch | the same for an approved batch's own branch (item 9) |
| 4. A description written by the Lead Product Manager | nothing | item 10 |
| 5. Automated reviewer findings decided by the Lead Product Manager, fixes linked, answers posted | Item 3, for people's feedback and the repair her third choice hands on | item 11 |
| 6. A review request through a configured command or address, in a set format and time window | nothing | item 12 |
| 7. Merge on approval on the head commit when mergeable, with a plain notice otherwise | nothing | item 13 |

The landing hold (item 5) is not needed on this route, because the harness moves the batch branch only to commits it chose. It stays for projects that keep a script of their own.

Rejected for this route:

- **The target branch as the batch's head.** It cannot be held still for an approval or held back for the automated reviewer.
- **A way for any project to plug in its own batching code.** The operator ruled that settings stay few and that no general framework is built from one case.
- **Sending automated reviewer findings to the development manager, as people's feedback goes.** Many findings are not defects, and whether one deserves work is the Lead Product Manager's backlog decision.
- **Merging when the head does not contain the team's branch.** The forge would build a merged tree nobody checked or reviewed.
- **Keeping an approval across a change to the head.** The approval would then stand on a commit the person never saw.

## Alternatives rejected

- **The harness opening a batch pull request no setting asked for.** Which items go into a batch, and when, stays the project's policy. Until November 19, 2026 this design left the batch to the project's own script. The operator then ruled that the policy stays the project's, as settings, while the mechanics move into the harness, so the harness opens a batch pull request only on the route in section 5, under the settings that project chose.
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
- A repair of an item whose run has ended starts only from the development manager's decision bound to the list entry it answers, always as a new run; it is refused once the item has closed or the entry already has a decision, and it spends one of the item's attempts, except a conflict repair, which spends one of the update's three rounds.
- An update of the target branch lands only with configured checks and an independent review of the exact merge commit, through the remote's compare and swap, and never by rebase or force.
- A landing hold is read under the landing lease before every guarded change to its target branch, and expires on its own.
- With `execution.batch_pull_request.enabled` off, or the team's branch unset, the harness opens, moves and merges no batch pull request.
- Each batch setting outside its bounds refuses the configuration when it loads.
- The batch branch moves only by compare and swap against its recorded head: either forward to a target commit that keeps every item whole, or by an update merge that passed the configured checks and independent review. After a person's approval, it moves only by that update merge.
- At most one batch pull request is open for a target branch.
- The batch merges only when a person's approval is on its head commit, the forge reports it mergeable, and the head contains the team's branch head, under the landing lease with the expected head named.
- The review request command runs with none of the harness's credentials and is stopped after 60 seconds.
- A finding from the automated reviewer starts no run and admits no item without the Lead Product Manager's decision.

## What a reviewer can check

- With the team's branch unset, an item closes when it lands, as today.
- With it set, an item that lands is shown as waiting for the team's merge, is not pulled again, and lets an item that depends on it start on the same target branch.
- Tracking a pull request into another branch, or naming an item whose landing commit is not in it without `--unverified`, is refused in plain words.
- A tracked pull request that merges closes every item it carries; one closed without merging sends each to the Lead Product Manager with the change kept.
- An item that lands while a tracked pull request's head is the target branch is added to that record with a note.
- Squash merging a tracked pull request closes its items.
- A line comment on a tracked pull request reaches the development manager's list naming the items that touched that file, and starts no run until she decides.
- Her decision to repair from that entry starts a new run whose record names the decision and the entry, spends one of the item's attempts, starts from the target branch's head, and lands on the target branch, where the tracked pull request picks the new commit up; the item stays waiting for the team's merge.
- The same decision for an item that has closed, or a second decision for an entry already answered, is refused in plain words and starts nothing.
- For an item waiting on a pull request the harness opened, the decision starts a new run from the change that pull request's kept branch carries, applied onto the target branch's current head, and the run's change replaces the kept branch's head by compare and swap against the head the harness last wrote, updating the same pull request. Where the kept branch holds a commit the harness did not write, the push is refused, nothing on the branch is overwritten, the run's change is kept, and the development manager's list gets one entry naming the branch and the commit found there.
- When the team's branch moves, the target branch gains one merge commit that passed the checks and the reviewer, and the status line shows when.
- A conflict while bringing the target branch up to date reaches the development manager's list naming paths and items, and landing on the target branch carries on; her decision on it spends one of the update's three rounds and none of the item's attempts.
- With a landing hold set, nothing lands on that target branch, checks and reviews carry on, and the hold ends at its expiry with nobody releasing it.
- With the batch route off, no batch pull request opens, whatever lands.
- A batch setting outside its bounds refuses the configuration in plain words.
- With `min_items` reached inside the review window, one batch pull request opens from `yoyodyne/batch/<target>`; outside the window it waits; a restart after opening opens no second one.
- An item with a decided repair run that has not ended is left out of the batch until the run ends.
- The batch does not grow while the automated reviewer has not reviewed its head, and grows after two hours with that said on the pull request.
- After a person approves, items landing later stay out of the batch.
- When the team's branch moves after an approval, the batch branch gains one merge that passed checks and review, and the pull request says approval of the new head is needed.
- The batch merges only with a person's approval on its head commit; an approval on an older commit leaves a comment naming it, and no merge.
- An automated reviewer finding reaches the Lead Product Manager's list naming the items that touched its files, and her answer is posted as a reply to it.
- One review request is sent for each head; the command sees none of the harness's credentials; after three failures the development manager's list gets one entry.

## Build items, for the Lead Product Manager to admit under `team-development`

1. The team's branch setting and the state of waiting for the team's merge: kept open on landing, never pulled again, met for dependents on the same target branch, closed when the landing commit is found on the team's branch, and shown on every surface.
2. Tracking a pull request: `yoyo pull-request track` and `untrack`, the forge read at recording, merged and closed handling on each forge check, and items found among its commits added.
3. Feedback on a tracked pull request put on the development manager's list with the items that touched the file, and the decision to repair an item whose run has ended (section 2): recorded against the entry it answers, refused once the item has closed or the entry is answered, starting a new run from the target branch's head or, for an item waiting on a pull request the harness opened, from the change that pull request's kept branch carries, applied onto the target branch's head, with the kept branch's head replaced by compare and swap against the head the harness last wrote and a refused push kept and put on the development manager's list, and spending one of the item's attempts. It builds on build item 2 of the contributors design, on yoyodyne-ifd.429.125, and on the fixed list of decisions in stopped-run recovery (yoyodyne-ifd.428.99.4).
4. Bringing the target branch up to date: the merge, its checks and review, landing by compare and swap, the update record and status line, and conflicts put on the development manager's list, decided with the same kind of decision as build item 3, under a limit of three rounds for the update.
5. The landing hold: `yoyo landing hold` and `release`, the four-hour bound, reading it under the landing lease, and the status line.
6. A validation on a scratch repository with a target branch and a team's branch, covering every check under What a reviewer can check up to the landing hold.
7. The batch route's settings and states: `execution.batch_pull_request` with its bounds checked at load, the batch record in the state directory, and the unpublished, in the open batch, approved and merged states with what the batch waits on, on status, the dashboard and the item's notes.
8. Opening and growing the batch: the threshold and review window, the newest commit that keeps every item whole, one open batch for each target, the batch branch pushed by compare and swap, taking up an open batch after a restart, waiting for the automated reviewer with the two hour limit, freezing on approval, and tracking under section 2 without a command.
9. Bringing an approved batch's branch up to date when the team's branch moves: the merge with checks, review and the protected paths proof as in section 3, the notice asking for approval of the new head, and a conflict unfreezing the batch once the target's own update lands.
10. The Lead Product Manager's description: her one turn with the batch's items and no tools, the fifteen minute limit, the plain list in its place, three tries for each head, and its cost recorded against the batch.
11. Automated reviewer findings: one entry each on the Lead Product Manager's list naming files and items, her three decisions, a fix item naming the finding, and her answer posted as a reply.
12. The review request: the command without a shell or credentials and its 60 second limit, the https address and its 10 second limit, the `json` and `text` formats, one request for each head, three failures to the development manager's list.
13. Merging on approval: the three conditions read in one forge check, the merge under the landing lease with the expected head and `merge_method`, and the plain notice on the pull request and status line when a condition fails.
14. A validation on a scratch repository extending item 6 with every check from With the batch route off onwards.

Items 2, 3 and 4 need item 1. Item 4 needs the fetch before each run and the forge check on its own schedule from the contributors design (its build items 3 and 10), and its conflict decision needs build item 3. Item 5 stands alone. Item 7 needs item 1. Item 8 needs items 2 and 7. Items 10, 12 and 13 need item 8. Item 9 needs items 4 and 8, and item 13 needs item 9. Item 11 needs items 3 and 8. Item 14 needs items 7 to 13.

This design adds no invariant: the two rules linked above already bind every landing it describes, the batch's merge into the team's branch included. It changes no other document.
