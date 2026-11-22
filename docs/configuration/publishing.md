# Configuring publishing, branches, and promotion

How a finished branch reaches the remote, what to do when you cannot push to
it, which branch is authoritative, what publishing needs from the machine, how
one repository is worked from more than one machine, what happens when two
promotions race for the same target, and how a merge queue lands approved
changes one at a time.

[The configuration index](../configuration.md) lists the other guides.

## Publishing through pull requests

This section is about run branches and their pull requests. A release tag is
published separately, under `approvals.releases`. Under
`human`, the default, a person cuts a release with `make release` and pushes
its tag. Under `automatic` the supervisor's maintenance pass cuts a release the
Lead Product Manager decided, once its notes and readiness stamp are on `main`,
and pushes the tag alone under the harness's identity. It needs neither of the
settings below. [Cutting a decided release](../operations.md#cutting-a-decided-release)
is the whole of it.

By default Yoyodyne is entirely local: it creates a branch and a worktree, runs
the work, and fast-forwards your target branch. Nothing is pushed, and a
repository with no remote never notices publishing exists.

A project opts in the way it opts in to automatic integration. **Both settings
matter**: publishing opens the pull request, and integration is what merges it.

```yaml
approvals:
  publishing: automatic
  integration: automatic   # required for the harness to merge what it opened

execution:
  remote: origin   # the default; name another remote if yours is not origin
```

If you cannot push to that remote — you are contributing to somebody else's
repository — name your fork as well:

```yaml
execution:
  remote: upstream     # where pull requests are opened; you need no push access
  push_remote: fork    # where run branches go; the one you can push to
```

With both on, a run works like this:

1. **The developer phase publishes.** When a developer attempt finishes, the
   harness commits its work under its own identity, pushes the run branch to
   `execution.remote` — or to your fork, if you named one; see [Publishing from
   a fork](#publishing-from-a-fork) — and opens a pull request against the
   target branch on `execution.remote`. Each repair attempt pushes
   onto the same branch and updates the same pull request, so one change never
   ends up with two places to be reviewed. This happens *before* the checks run:
   a pull request is where work is reviewed, and work that does not pass yet is
   exactly what a reviewer should be able to see.
2. **The reviewer's verdict merges it.** An approving verdict authorizes the
   merge, and the harness asks the forge to perform it — it never pushes your
   target branch. Nothing about the gate changes: the same passing checks, the
   same independent-reviewer evidence, and the same fast-forward rule that gate
   integration also gate the merge, and the remote target is checked again right
   before the call, so a target that moved in the meantime refuses the merge
   rather than having the forge reconcile it. That holds of a merge
   [reissued after a dropped connection](recovery.md#waiting-out-a-network-that-dropped)
   too: the check is made again on each attempt rather than once in front of
   them, so what authorizes a merge made after a wait is a reading of the target
   taken after that wait.
   The merge is asked for as of *when your branch protection is satisfied*
   rather than as of now, so required checks that are still running are waited
   for by the forge instead of refused seconds after the approval. Administrator
   override is never used to get past them. Waiting that way needs **"Allow
   auto-merge"** enabled in your repository settings, which is off by default;
   when it is off and nothing is holding the pull request back, the harness
   simply merges, so a repository without branch protection needs no setting
   changed at all. Only the combination of the two — something holding the
   request back and no way to queue the merge behind it — cannot be published
   to, and the run says exactly that and names the setting rather than
   reporting a merge that mysteriously fails.
3. **The merge method is a merge commit.** The harness names it rather than
   taking your repository's default, because it is the only method that puts the
   reviewed commit itself on your target branch. A squash replaces it with a
   commit nobody reviewed, and GitHub's rebase always rewrites what it merges —
   new committer, new SHA, even when the request needs no rebasing — so both
   would leave the remote carrying a copy of the work your local branch does not
   have. The method is recorded on the run and on the work item, along with the
   commit the merge produced.
4. **The merge is confirmed, then the branch is cleaned up** on both sides,
   locally and on the remote, on the same compare-and-swap evidence. The
   confirmation waits briefly and boundedly, because a forge's own record of a
   request can lag the merge it just performed. If the forge refuses outright —
   a request that conflicts with its base, a merge method the repository
   forbids — the run reports which requirement was unmet rather than a generic
   failure.
5. **A merge the forge queued ends the run rather than being waited for.** It
   lands minutes later, when your checks pass. The run reports the pull request
   as queued and finishes: your change is already in the local target branch,
   which is the authoritative one (on a
   [protected target](#a-protected-target-lands-through-its-pull-request) it is
   not, and nothing local moves until the forge merges), and the run branch stays on the remote
   because that is what the forge still has to merge. On a protected target the
   run's local branch and worktree stay too, until the forge's merge is
   confirmed, because nothing proves the change is on the target before then.
   A request the forge's merge queue has taken is read as a merge the forge
   still holds, not as one it dropped. The work item is the one
   thing the run does **not** settle — it stays open, with the queued merge named
   on it, because closing it as integrated would record a publication that has
   not happened and may not. `yoyo reconcile` settles both afterwards: it asks
   the forge, and either finishes the publication and settles the item — closed,
   or put back in the backlog parked or waiting on a named impediment where the
   run's own records say its change does not discharge it, its landing claim or
   what its reviewer approved (merge
   commit recorded and your local target branch caught up
   onto the forge's merge commit, with the branch the merge consumed deleted
   afterwards as hygiene that cannot hold the closure up) — or, if the forge dropped the queued merge
   because something it required went unmet, records an outstanding publication
   carrying the forge's own account of the drop — the reason it stated, the
   requirement its merge state reports unmet, or, where it gives neither, that
   it gave none beside the checks and merge-queue events the harness could read
   — and puts the item on the development manager's docket with a blocker
   rather than closing it, whatever repair attempts the run has left. A drop
   on a protected target whose head fell behind, failing nothing the change
   touches, is not handed back: the change is brought up to date from the kept
   branch, checked and reviewed again, and queued again, as a queued head behind
   its target is. It never
   merges anything itself: what is done about a requirement that stopped the
   forge is the development manager's to decide — a repair, a re-run, or a
   re-arm, each carried out by the harness, or, where the forge's account names
   a repository setting only a person can change, that exact step asked of
   you — and re-arming a dropped merge is a bounded triage decision — one per
   publication, carried out by `yoyo triage rearm` — rather than something a sweep
   does.

`gh` is invoked by the harness and never by a developer or reviewer: no role is
given a credential, a tool, or a request to push or merge. For the reviewer that
is a hard boundary — it can only read: on Claude Code its shell runs nothing but
read-only Git commands, in a sandbox with the worktree refused for writing and
no network, and on Codex it runs under Codex's read-only sandbox. So the role
whose verdict authorizes a merge has no way to perform one, and cannot be talked
into merging something the checks would have refused.

For the developer it is not. A developer has a shell in its worktree and runs
under your account, so it could in principle reach a `gh` you have
authenticated; what stands in the way is its backend's sandbox and the harness
contract in its prompt, not a boundary the harness enforces. What does hold is
that your local target branch is authoritative: work an agent pushed by itself
is not integrated by having been pushed, and a pull request merged behind the
harness's back moves the remote away from the local branch, which the harness's
own check of the remote target then refuses rather than force-resolves.
### Publishing from a fork

Everything above assumes you can push to the remote you publish into. When you
cannot — the ordinary situation for a contributor to somebody else's
repository — `execution.push_remote` names the remote your run branches go to
instead, and the pull request is opened across the two repositories:

```yaml
execution:
  remote: upstream
  push_remote: fork
```

Both remotes have to exist in your checkout. Add the fork the way you would add
any remote, and check both with `yoyo doctor`, which names whichever one is
missing:

```bash
git remote add fork git@github.com:you/theirproject.git
yoyo doctor
```

Nothing else about publishing changes, and nothing about it is a second mode.
Run branches are pushed to the fork, the pull request is opened against the
remote you publish into with the head qualified by your account, and every
question about the target branch — whether it may still be merged into, where
the forge's merge left it, catching your local branch up afterwards — is asked
of the repository the work is going into. The merged run branch is deleted from
the fork, because that is the only place it ever was.

Your fork's account is read from the fork remote's URL rather than configured
separately, so there is nothing to keep in step with it. A repository that does
not have the remote you named publishes nothing and says which remote it was
looking for, the same way a repository with no remote at all does.
### Publishing without automatic integration

`approvals.publishing: automatic` with `approvals.integration: human` is
supported and does exactly half of the above: the change goes through the same
checks, independent review, and repair rounds as under automatic integration,
and once the reviewer approves it the harness pushes and opens the pull
request, and then stops. **It merges nothing.** The pull request is opened only
for an approved change: the developer's attempts before that are committed on
the run branch and not pushed, so nothing unreviewed reaches the button you
merge with. You get an open pull request, a run branch that stays on the
remote, and a preserved worktree; the harness never merges the request.

Once it has published, the run ends and gives up its developer slot, so other
ready work can start. Its item stays claimed, and the item's notes, `yoyo
status` and the dashboard all say it is waiting for a person to merge pull
request #N. Each check of the forge, which the supervisor makes on a schedule of
its own every `execution.forge_check_interval` (five minutes by default), apart
from the maintenance pass, then asks the forge about every request waiting this
way (`yoyo forge-check` makes one check by hand):

- **Merged:** the local target branch is brought onto the merge by
  fast-forward, the item is closed with the merge commit named, so work
  waiting on it can start, and the run is recorded as landed. Its branch and
  worktree are then removed, once the local target is shown to contain the
  change; after a merge that rewrote the change, such as a squash, they stay
  and the run is shown as still owing its cleanup.
- **Closed without merging:** the item goes back to the Lead Product Manager,
  parked with a note naming who closed the request and when, so nothing starts
  it again before she decides whether to drop the work, revise it, or admit it
  again. The branch and the worktree are kept, and nothing is deleted.
- **Still open:** nothing changes, and the next check asks again, unless the
  target branch has moved past the request because other people's work
  landed first. Then the harness brings the request up to date, at most once
  per check: it first withdraws any merge the forge was set to make of it and
  checks that it is withdrawn, then replays the change onto the new target,
  runs the configured checks and the independent review again on the
  replayed change, and pushes it to the same request, which waits for you
  again. The run takes a developer slot while it does this. A request the
  forge is already merging is left alone, and the harness reads the request
  again just before it pushes, so it never pushes over a merge you have
  started. If the replay conflicts, the developer is given the conflict with
  the conflicting files named; these rounds are counted against a limit of
  three of their own, not the item's repair attempts, and a conflict after
  the third stops the run for the development manager with the change kept.
  However often the target moves, these updates never use up the run's
  allowance of resumed promotions. If the harness cannot bring the request up
  to date at all, because the run's branch, worktree, approval or developer
  session is gone, it says so once on the item, and the request is shown as
  the development manager's to decide rather than only yours to merge.

How often the forge is checked is `execution.forge_check_interval`, five
minutes by default and at least a minute; setting it is the development
manager's decision. The check runs on its own schedule, not inside the
maintenance pass, so a slow or failing maintenance step never delays it, and it
runs only for a project whose `approvals.publishing` is `automatic`. Each check
reads the pull requests waiting for a person to merge them, the reviews and
comments on them, and how far the target branch has moved past each. The reads
GitHub can answer conditionally are asked that way, carrying the answer kept
from the last check (in the product's state directory, as
`forge-responses.json`), so an answer that has not changed costs a "not
modified" reply rather than the whole answer again. The batched query for each
request's state is GitHub's GraphQL API, which has no conditional requests, so
it is asked in full: one query for up to 50 requests.

```yaml
execution:
  forge_check_interval: 5m   # the default
```

A check that cannot reach the forge changes nothing, and the next check tries
again. A change a check read but could not finish acting on, such as closing an
item whose pull request merged while the tracker write timed out, is read and
acted on again by the next check; a write repeated after it had in fact gone
through leaves the item as it was, with no second note. Neither stops a run or
holds back other work.

That is deliberate rather than a gap. Merging is a promotion, promotion is what
`approvals.integration` governs, and a harness that merged under a `human`
integration policy would be taking the decision that setting reserves for you.

| `publishing` | `integration` | What you get |
| --- | --- | --- |
| `human` | `human` | Reviewed change on a local branch and worktree, preserved for you. |
| `human` | `automatic` | Local fast-forward into the target branch, artifacts removed. Nothing pushed. |
| `automatic` | `automatic` | Pull request opened, merged on approval — or queued with the forge until your required checks pass — and the branch removed locally, then on the remote once the merge has happened. |
| `automatic` | `human` | Pull request opened once the reviewer approves, and left for you; the run frees its developer slot. Nothing merged. The item closes once you merge, or goes back to the Lead Product Manager if the request is closed unmerged. |
### Which branch is authoritative

**The local target branch.** Your work is where that branch says it is. The
exception is a target branch the forge protects, where the forge's copy leads and
the local one follows it: see
[a protected target lands through its pull request](#a-protected-target-lands-through-its-pull-request).

Merging is not a second promotion performed on the remote. The harness
fast-forwards the local target exactly as it always has, and the forge merges
the pull request carrying exactly that commit. One promotion, one reviewed
commit, the same commit on both sides.

The merge itself does not leave the two at the same commit, and no forge merge
method would: **the merge leaves the remote target at your local target plus one
merge commit**, made by the forge and identical in content. The last step of the
promotion is to catch your local branch up onto the remote, which is an ordinary
fast-forward onto a commit that already contains the promotion. Nothing is
rewritten, reset, or merged, and nothing is decided: that is the `git pull` you
used to run yourself.

A catch-up the harness cannot make cleanly is held rather than forced, and says
why:

- **Uncommitted work in your checkout that the incoming commits would
  overwrite.** The branch is left where it is and the file is named. The
  exception is the work tracker's own exports — `.beads/issues.jsonl` and
  `.beads/interactions.jsonl`, the same two a run is allowed to rewrite in your
  checkout while it works. They are derived from a store that is authoritative
  elsewhere, so their churn is discarded and the catch-up goes through.
- **A remote that has diverged from your local branch** — a history somebody
  rewrote, or work that reached the remote another way. Which of the two is
  right is your answer rather than the harness's, so it is reported and nothing
  moves. A promotion refused on one records it against the product, and the line
  [chooses no work](../operations.md#unwedging-a-target-branch-that-diverged-from-the-forge)
  until a `yoyo reconcile` finds the branches converged.

A merge that landed after its run had finished, and any catch-up that was held,
are swept by `yoyo reconcile`, which also removes the leftover local branches of
settled runs whose work the target already carries. Catching a branch up takes
that branch's promotion lease, so it never races a run promoting into it.

The same catch-up is made immediately before every developer run, so a run
starts from the target the remote has now, other people's merges included,
rather than from wherever the local branch last stood. If the remote cannot be
reached, the run starts anyway, from the target the last fetch left. Either
way the run's record names the commit it started from and whether it was
fetched for the run, and a note on the work item says the same; where the fetch
failed, both say why and how long before the run the target was last fetched,
and `yoyo status` shows it on the run. A catch-up held for one of the reasons
above starts the run from the local branch as it stands, with the reason named.
The time of the last successful fetch of each target is kept in the repository
under `refs/yoyodyne/fetched-target/<branch>`.

Because the forge performs the merge, the harness checks that relationship
rather than assuming it. Before the merge, the remote target must contain the
commit your promotion was made from and carry exactly its content — that is what
tells a target another run already published into from someone else's work.
After the merge, it must contain the promoted commit itself, unrewritten. It
need not carry exactly its content: a merge that lands among others — ten held
requests merged in one sitting — leaves every promotion but the last under a
merge commit later merges have built on, and requiring equality there reported
nine confirmable publications as unconfirmable for good. What is recorded as the
merge commit is the one the forge names for the pull request, where that commit
is on the remote target with the promoted commit as a parent, or otherwise the
one found in the remote history with the promoted commit as a parent; the
forge's record never decides the confirmation, only what is recorded. A forge that rewrote the
commit is reported, not reconciled, and the run branch is left on the remote for
whoever decides which history is right.

If a promotion onto an unprotected target cannot be published — the forge is
unreachable, the remote target moved, or the forge refused the merge — the run
still succeeds and closes its item, and reports an *outstanding publication*. A forge that could not be reached
is [waited out and asked again](recovery.md#waiting-out-a-network-that-dropped) first, so
an outstanding publication over a dropped connection is one that went on being
dropped rather than one reset the next attempt would have survived. The change is integrated where
it counts; only its publication is unfinished, and it is reconciled by hand.
Nothing is ever force-pushed to resolve it. On a protected target the same
failures leave nothing integrated, so the run stops rather than closing the item;
the next section says how.
### Reviews and comments on a pull request waiting for you

While a pull request waits for a person to merge it, under
`approvals.integration: human`, each check of the forge reads the reviews and
comments left on it since the harness last pushed its head, and nothing is
acted on twice. What a person wrote is read, whichever forge account they wrote
it under, so your own review is read even where the harness publishes under
your account.

Only the harness's own words are left out, and it tells them apart without
looking at the account. Every pull request description and comment it writes
ends with a hidden line, `<!-- yoyodyne: product <id>, machine <id>, forge
account <name> -->`, which the forge does not show; and the run's record keeps
the forge's number for each comment it wrote. A review or comment is left out
when its number is on that record, or when it carries a hidden line naming your
product. The record catches a harness comment you edited the line out of; the
line catches a comment the forge took after the harness stopped waiting for its
answer, and anything another copy of the harness working the same product
wrote. If you copy the line into a comment of your own, that comment is left
out too.

- **A review that requests changes, or a comment on a line of the change,**
  starts a repair round on the same run, in its kept worktree, counted against
  its repair budget (`execution.repair_attempts_before_replan`). The developer
  is given the feedback with who left it and when, as evidence about the
  change; it does not widen what the run may change and grants no protected
  path. The repaired change goes through the configured checks and the
  independent reviewer again, and only an approved change is pushed to the
  same pull request. A round needs a free developer slot; without one the
  feedback waits for the next check. Where the repair budget is already spent,
  the run stops in the ordinary way with the feedback kept, and the development
  manager decides what happens next.
- **An ordinary comment on the pull request** goes on the development
  manager's list as a report addressed to her, because a comment is as often a
  question as a request. No repair round starts for it.
- **An approval** is recorded on the run and the item as information. It never
  stands in for the independent review and never changes what the item is for.

### A protected target lands through its pull request

Before it promotes, a publishing run asks the forge whether the target branch is
protected, both ways GitHub protects one: per-branch protection, and a ruleset
carrying a pull-request, required-status-check, or update rule. It is the same
question `make release` asks before a cut. The answer decides the order of the
two halves above.

**An unprotected target** is promoted exactly as described above: the local
target is fast-forwarded onto the reviewed commit, and the forge merges the pull
request carrying it.

**A protected target is never moved locally ahead of the forge.** The run
commits the change, checks that the local target still stands where the change
was written against, and asks the forge to merge, with the local target left
where it was. The reviewed commit reaches the remote only through the forge's
merge, and the local target follows by the catch-up above: a fast-forward onto
the remote, taken under the branch's promotion lease. So no ending of the run
leaves your local target ahead of the remote:

- **Merged.** The run reads the remote target and records the change as merged
  only once it finds the reviewed commit there; the catch-up then brings the
  local target onto the forge's merge commit, and the item closes.
- **Merged on the forge's word only.** The forge reports the request merged and
  the remote target does not carry the reviewed commit — a merge that replayed
  the change as fresh commits does this. The run is not recorded as merged: it
  stops at its merge with the forge's state on its record and the item handed
  back, as a refused merge is. The same reading by `yoyo reconcile`, for a
  queued merge or an interrupted landing, leaves the run where it was, writes
  the disagreement on it, and lists it for the development manager with the
  cause given as uncertain.
- **Queued.** Nothing moves locally. The worktree and branch are kept, because
  nothing proves the change is on the target yet; `yoyo reconcile` catches the
  local target up once the forge merges, closes the item, and cleans up.
- **Refused or dropped**: a required review, a failing check, anything the
  forge would not merge. The change is on its pull request and on no target
  branch, so the item is not closed. The run stops and hands the item back with
  the forge's answer as the blocker, keeping the record `yoyo triage rearm`
  repeats the merge from once the requirement is met.
- **The remote target moved** after the landing was prepared. Nothing was
  promoted, so this is a lost race rather than a divergence: the local target
  is fast-forwarded onto the remote and the change is replayed onto it, with
  the checks and the review re-earned, under the same
  [retry budget](#losing-a-race-for-the-target-branch).
- **The process was killed** after the landing was prepared and before the
  forge's answer was heard. The run recorded the landing before asking, and
  `yoyo reconcile` settles it on what the forge says rather than on the local
  target, which says nothing here: merged is confirmed on the remote target,
  caught up onto, closed, and cleaned up, or left at its merge as above where
  the remote target does not carry it; queued is recorded as a queued landing and settled like any
  other; anything else hands the item to a person, as the run itself would have.

**Why.** On a protected target, promoting locally first strands commits. A
merge the forge refuses or holds leaves the local target ahead of the remote,
and every later run that has to bring the target onto the remote collides with
it. That happened on 2026-09-20 and again on 2026-09-24, and both times every
run stalled until the checkout was reset by hand.

**A forge that cannot be asked is treated as protecting the branch**, and the
run says so on the work item. The protected path costs an unprotected target
nothing it needs, since the change still lands by the merge, while the other
reading could move a branch the forge then refuses. Every publishing run
records which path it took, and why, as the `Target branch:` line in its notes
on the work item.
### What publishing needs

- A remote by the configured name. **Without one the run is purely local**,
  reports `publishing skipped`, and behaves exactly as it did before publishing
  existed. That is a property of the repository, not an error.
- The GitHub CLI, installed and authenticated (`gh auth login`). If a project
  asked to publish and `gh` is missing or logged out, the run **fails before it
  claims anything** — a harness that quietly stopped publishing would look the
  same as one with nothing to publish.
- Permission to merge the pull request. The target branch itself is never
  pushed, so a branch protected against direct pushes — requiring a pull
  request, a build check, or a review — is merged into normally, provided the
  account `gh` is authenticated as may merge and the request satisfies whatever
  the protection requires. Only the run branch is pushed. If the protection is
  not satisfied, the run stops with the unmet requirement named, the item is
  handed back to you, and your local target is left where the forge has it —
  see [a protected target lands through its pull request](#a-protected-target-lands-through-its-pull-request).
- **Merge commits allowed** in the repository's settings, since that is the
  method the harness asks for. A repository that permits only squashing or only
  rebasing refuses the merge, and the run reports that refusal — it does not
  fall back to a method that would replace the reviewed commit with a rewritten
  copy your local branch does not have. A protection rule requiring linear
  history has the same effect.
## Working one repository from more than one machine: `team`

Team mode is for a repository worked by more than one copy of the harness, each
on its own machine, sharing one remote. It is off unless the committed
configuration turns it on, so every factory working the repository reads the
same value:

```yaml
team:
  enabled: true
```

Turning it on is the Lead Product Manager's decision and the development
manager applies it
([working alongside other contributors](../designs/working-alongside-other-contributors.md#5-two-factories-and-the-same-work)).
With it off, the harness works the repository alone: it takes no claim on the
remote, reads no team pause, and no start of new work waits on the remote.

With it on, the harness claims each item on the shared remote before it starts
it, so two factories never do the same work:

- **Taking.** Before a fresh run claims an item in the tracker, the harness reads
  the intake hold, fetches the claims and the team pause from the remote
  (`execution.remote`) in one fetch, and pushes `refs/yoyodyne/claims/<item id>`
  with a lease that fails if the reference already exists. While the team pause
  (`refs/yoyodyne/pause`, set and cleared with
  [`yoyo pause --team`](../operations.md#pausing-everything-and-resuming-it)) is
  set, no item the harness chose is claimed; one the operator named on this
  machine still is. The tracker is claimed only after that push
  succeeds. An item another factory holds is not started this turn, and the
  schedule says who holds it; it is tried again at the next poll. A remote that
  cannot be reached starts no new work in team mode, because a claim cannot be
  taken without it.
- **Renewing.** While the run holds the item — running, waiting for a person to
  merge, or stopped and kept for a decision, which is any run in flight or any
  item this machine's tracker still shows as claimed or blocked — the watch loop
  renews the claim every 15 minutes, with a lease naming the commit it last
  pushed. A renewal that cannot reach the remote is said on the schedule, tried
  again at the next poll, and charges the run nothing.
- **Taking over.** A claim nobody has renewed for two hours may be taken over,
  with a lease naming the stale commit. The new claim names the earlier holder,
  and so does a note on the item. A claim this machine left from an earlier run
  of the same item is taken on by the next run without waiting. A claim counts
  as this machine's own only when its record under `team-claims/` is still held
  and names the very commit the remote has; a claim under the same name is
  never enough, so a second home on the same machine is held to the same two
  hours as anybody else.
- **Releasing.** Once the run no longer holds the item, the watch loop deletes
  the reference with a lease naming its own commit. A claim taken in the last
  15 minutes is not released, because a run takes its claim a moment before
  it records itself and claims the tracker, and the watch loop must not take
  that moment for a run that has ended. A reference that is no
  longer this machine's, because another factory took it over, is left alone and
  said on the schedule.

Each claim is a commit of the empty tree whose message records the factory
holding it (the factory identity: the product id, the machine identity kept in
the home's `machine-identity.json`, and the forge account the harness acts as),
the item, the
run, and when it was taken and last renewed. What this machine holds is kept
under its state directory in `team-claims/`. Only the harness pushes these
references, as it pushes pull request branches; no agent is given anything that
writes one.

### What the factories share: the records reference

Reports, what was done about them, directives and later changes to directives
travel between the team's factories as one file each, named by its own
identifier and never rewritten; a change to a directive is a new file naming the
directive. They are kept in the reference `refs/yoyodyne/records` on
`execution.remote`, built in a Git repository of the harness's own under the
state directory, `records.git`, so nothing is written in your repository.

Each forge check shares them. The records other factories shared are copied
into this machine's own report and directive records, naming the factory that
filed them, and the ones filed here are sent. If another factory sent first, the
check reads theirs and sends again; a record already on the remote stands. A
directive from a Slack reply is named by the Slack message, so a reply two
factories both see is one directive, and the reply is answered once the
directive is shared or found already recorded. With the remote out of reach, the
reply is told the directive is kept on this machine and shared at the next forge
check. `yoyo status` counts the records not yet shared. Only the harness writes
the reference; no agent is given anything that writes one.

Team mode needs three things of the project, and `yoyo doctor` refuses team
mode in plain words where any is missing: the harness has to publish through
pull requests (`approvals.publishing: automatic`), because the forge is where
factories learn about each other; the scheduler service has to be on
(`services.scheduler.enabled`), because the watch loop is what renews and
releases claims, including those taken by `yoyo run`; and the remote has to
accept references under `refs/yoyodyne/`, which doctor tests by pushing
`refs/yoyodyne/probe/doctor` and deleting it again. A push that never reaches
the remote is reported as a check that could not be made, not as the forge
refusing the reference.

## Losing a race for the target branch

A run promotes its change by fast-forwarding the branch it was written against,
which requires that branch to still be where the run started from. On a target
the forge protects the local branch is not fast-forwarded — the change lands
through its pull request ([a protected target lands through its pull request](#a-protected-target-lands-through-its-pull-request))
— but the same requirement holds, and a remote target that moves between the
landing being prepared and the forge being asked to merge is a lost race too,
answered by the same replay and the same budget. It may not
be: another run can promote into the same branch first, and an operator who
commits to it while a run is working moves it just as effectively. The
promotion fails closed in both cases — nothing is force-merged and nothing is
reset — and the run then re-prepares rather than dying on it:

```yaml
execution:
  integration_retries_before_reconciliation: 2
```

Each lost race replays the change onto wherever the target went, runs the
configured checks again, and obtains a **fresh independent review**. The earlier
approval is discarded rather than carried over: it described a diff on the base
the change no longer sits on, and an approval that survived a replay would be
authorizing a promotion nobody judged. Nothing is handed back to the developer,
so a replay spends no repair attempt — the change is not what went wrong.

**Losing the race spends no budget.** The change standing at a refused
promotion passed its checks and was approved, so a lost race replays, and a
replay that passes its checks and is approved is charged nothing, whatever the
budget says. Every lost race is recorded on the run before its replay begins and
is said in the item's thread at note severity.

Each replay is made onto the target as it stands when the race is lost, never
onto the base the change was last checked on. A lost race whose replay applies
cleanly onto the newer target and passes its checks is **not counted as lost**:
the change was sound and level with the target, and only other work landing
first beat it. Every lost race is recorded on the run (`lost_landings`, with
`clean_replays` counting the clean ones): the base the change was moved from,
the commit it was moved onto, and whether the replay applied cleanly, conflicted,
or was stopped on the change. The run's summary says each of them in plain
words, and so does the stop reason of a run that stops at landing.

What a counted lost race does cost is the developer slot the run holds while it
replays, so a run gives the slot back at its **fifth counted lost race** — a
race whose replay conflicted or was stopped on the change. That race is not
replayed: the run stops with its branch and worktree kept, and the item is
blocked with a note saying how many races the change lost, how many of them
were counted, and that its work is kept, which puts it on the development
manager's list to decide when it is replayed again. The number is fixed in the
harness (`lostRacesBeforeGivingBackTheSlot` in
`internal/orchestrator/pipeline.go`) and is not a configuration key. The races
are counted from the last time the run started again: a repair the development
manager decides starts the count from zero, whatever the run stopped on, because
the slot the earlier races were spending was given back when it stopped. A run
whose replays keep applying cleanly is not stopped by this limit at all; it
keeps replaying, and holding its slot, until it lands.

The budget bounds replays that stop on the change instead: one that conflicts,
and one whose replayed change is handed back for a failing check, a refused
path, missing verification, or a repair verdict. Each replay is charged at most
once, at the first such stop after it, and the charge is enforced right there.
While the count is within the budget the replayed change is handed back to the
developer like any repair; the replay that takes it past the budget stops the
run at that point, on the change, with a blocker saying what stopped the replay,
how many replays stopped on the change, and how many races were lost.
`integration_retries` counts the races and `charged_replays` the replays that
were charged, both saved before what they count takes effect, so a process that
dies mid-replay comes back to both. At `0` no replay may stop on the change:
the first replay that does ends the run there, and a lost race whose replay
passes still lands.

Whether a replay whose diff is byte-identical to the one approved may promote
without a fresh review is a question about the gate and the architect's to
rule on. Until the architect rules, every replay is reviewed afresh as above.

A merge the forge queued can lose the same race after the run is over: the
target moves on, the queued head falls behind it, and its checks fail on files
the change does not touch. `yoyo reconcile` reads a queued merge's checks on
every sweep and, finding that, withdraws the queued merge and replays the change
onto the target through the same run — checks again, a fresh review, and the
merge queued again. The update is a lost race and is never handed back for
being one; the replay's own gate charges the budget if the replayed change
stops on the change, as above. A run that cannot be replayed at all is handed
back instead
([operations](../operations.md#recovering-interrupted-runs)).

Before any of that, a queued merge whose checks on GitHub fail only in tests
the change does not touch is given one more run of the failing job. The sweep
reads GitHub's log of each failing job for the Go tests it names, and judges
each test touched where the change changed a file in the test's own directory
(or a Go file, `go.mod` or `go.sum` at the top of the repository). Where every
failure is a named test, there are at most five, and none is touched, the sweep
writes the request on the run and then asks GitHub to run the failing job once
more on the same commit, leaving the merge queued: a pass lets GitHub make the
merge with no repair attempt spent, and the run keeps the first failure beside
the passing result. A commit is given one such run however often the harness
restarts. A second failure, a failure that is not confined to untouched tests,
or a job GitHub will not run again is settled as it would have been without the
extra run, and the stop names the failing tests and says whether the change
touched them.

Where the repository uses GitHub's own merge queue, GitHub can drop a queued
pull request because its checks failed on the merge commit the queue built,
while the pull request's own head passes. Running the failed job again on that
merge commit would not put the request back in the queue, so where those
failing tests are all ones the change does not touch, the sweep queues the
merge once more instead, writing that on the run first: GitHub builds a new
merge commit and runs its checks again. A head is given one such second run;
a second drop is settled as a dropped merge is, naming the failing tests and
saying the change did not touch them.

A replay that **conflicts** is never retried and never resolved automatically
— which side of a conflict is right is a decision about the product, not a Git
operation — and it goes back to the developer that wrote the change before it
goes to anybody else. The change is moved onto where the target went, with
whatever would not merge left in the worktree between Git's own conflict
markers, and the same developer session is handed the conflict to settle: the
branch it could not be replayed onto, the commit that branch is at, and the
paths the replay stopped on. The first conflict a run meets is handed back as
the run's one **reconciliation attempt**, whatever repair attempts remain: it is
recorded on the run (`reconciliation`) before the developer is invoked, and it
spends neither a repair attempt nor the integration budget, because a run whose
review rounds used up its repair attempts would otherwise stop with approved work
it never got to bring level with the target. A later conflict is handed back as
a repair attempt, spent from `execution.repair_attempts_before_replan` like any
other. Either way, what the developer produces goes through the whole gate again
— the protected-path refusal, the checks, and a fresh independent review — before
it is promoted by the same fast-forward. A replay conflict on an approved change
therefore costs a continuation of the session that wrote it rather than a fresh
run. The target
keeps every commit it has; what the move drops is only the run's own commits,
whose content is what the developer is handed back as uncommitted work.

A conflict after the reconciliation attempt is also a replay that stopped on
the change, so it is charged to the integration budget above at the same moment,
and a run past either budget — no repair attempt left, or more conflicting
replays than `integration_retries_before_reconciliation` permits — stops
instead, with a stop reason saying the reconciliation attempt was already used,
as every conflict did before:
the replay is abandoned, the branch and worktree are left exactly as they were,
both sides survive, and the blocker on the item names the paths and the target
commit. The conflict stays on the run's record, so a repair the development
manager grants in triage continues that same session with the same conflict,
moved onto the target first; a re-run starts the change over instead.

A replay the harness itself ended — its local Git budget ran out, its context
was cancelled, or it went silent past its liveness bound — is **not** a
conflict, although it leaves the same half-applied state behind. It is told
apart by how the rebase stopped, abandoned the same way so the worktree is back
on its branch before anything is recorded. The replay's `git rebase` is given
a time limit worked out from what it does: the local Git budget, plus that budget's idle
thirty seconds again for each commit it replays and fifty milliseconds for each
file it writes, all scaled for load. A replay stopped on time is asked again by
the harness on its own, up to three times, after waits of one, two, and four
minutes and with its budget doubled each time, spending no repair attempt and
counting no lost race; each wait is on the run's record. Only a replay still
stopped after the third retry ends the run, reported as an integration stop by
something outside the work, of cause `replay-killed`, whose reason names the
retries, that `yoyo triage resume` picks up, rather than as a conflict for a
person to settle.

Two more stops of an approved change before its promotion are integration stops
of the same kind, although a person clears them: a target branch the harness
will not catch up to the remote's (`diverged-target`), and a remote that refused
the harness's SSH key or forge login on a push or a fetch
(`remote-auth-refused` — "Permission denied (publickey)" is the usual one). What
the person settles is the branches or the credential, never the change, so
`yoyo triage resume <run-id>` carries the approved change on afterwards at no
cost to the item. Asked before then it refuses, writes nothing, and says what
clears the cause: the diverged-target recovery in docs/operations.md, or
loading the key (`ssh-add`) or renewing the login (`gh auth login`). A refused
credential is never retried against a clock, because asking again earns the
same answer; the harness instead asks GitHub with the same identity at its
regular look at held work, about every five minutes, and resumes the run itself
at its promotion the first time GitHub answers, so the person only has to
restore the identity. A machine that cannot name its user ("No user exists for
uid 501") and a GitHub command line tool that is not logged in are read the same
way. A run that stopped earlier, while pushing its branch for its pull request
before its checks, for one of these causes only, is taken up the same way at its
checks, on the same branch and in the same worktree, with no repair attempt or
re-run spent; it pushes its branch once the checks pass and before its review.
Either kind is taken up only while its branch and its checkout are both still
there: a run whose branch or checkout is gone is left for the development
manager, with the reason said, and the harness does not put the checkout back to
take it up.

A published run's pull request follows the replay: the run branch is replaced on
the remote from exactly the commit the harness published there, so the request
carries the change that would actually be promoted. A change handed back to
reconcile a conflict is replaced the same way, once the developer's attempt is
committed — never with the target alone in between, since a request whose head
is already in its base reads as merged. That is the same
compare-and-swap every other write makes — a remote branch carrying anything
else is refused rather than overwritten — and the refusal stops the run, because
nothing has been promoted yet and there is nothing outstanding to report.
## Integrating through a merge queue

A run that loses a race for its target replays its change, checks it, and has
it reviewed again ([above](#losing-a-race-for-the-target-branch)). With several
runs landing on one branch that is work repeated on every race. A merge queue
does it once per landing instead, on exactly what lands:

```yaml
execution:
  merge_queue: false   # the default; true integrates through the queue
```

**Off, which is the default and what a file without the key means,** nothing
changes: a run promotes its own approved change, replaying it onto a target
that moved and earning its checks and a fresh review again first, exactly as
the sections above describe.

**On, a run that earns its approval admits its change to the queue of its
target branch and ends.** Its gate is the same — the protected-path gate, the
configured checks, and an independent review of the change — and is read off
the run's record before anything is admitted. What the run no longer does is
check its approved change whole a second time where the change goes to the
harness's own queue ([where the whole suite runs](runs.md#where-the-whole-suite-runs)),
promote the change, or replay it: the change is committed as it was approved,
admitted as the queue's next entry, and the run ends succeeded with its item
still claimed and its branch and worktree kept, because the queue may hand the
change back to it. A run waiting in the queue holds no developer slot, so the
next item starts beside it, and the claim audit leaves its item alone. The
item's notes say which queue and entry the change waits in.

**The queue lands only a candidate it verified itself.** For the first waiting
entry, in the order the queue is worked, the harness builds a candidate in a
checkout of its own: the target branch as it stands, with the approved head
merged onto it. That candidate is what would land, so it is what is judged:

- the protected-path gate is asked of every path the candidate changes, against
  what the entry's work item grants, before anything runs on it, and its answer
  is recorded on the candidate: a candidate with no such answer, or one the gate
  refused, earns no landing whatever its checks and review say;
- the configured checks run on it whole, told
  `YOYODYNE_CHANGED_GO_PACKAGES=./...`, so a race check among them is the full
  race check on the change combined with the target; a confirmed document's
  candidate runs `document_checks` instead, where you declare any
  ([the document checks](runs.md#the-document-checks));
- an independent reviewer, under the run's account and charged to the run and
  its item as review spend, is asked whether the change combined with what the
  target now carries still does what the item asks.

The approval the run earned admitted the change; it authorizes no landing, and
evidence about one candidate never answers for another. The target moving while
a candidate is checked or reviewed sets that candidate aside and builds a new
one on the moved target, charged to nobody. The landing itself takes the
target's promotion lease, reads the operator's pause, the item's directives and
dependencies, and the integration approval policy again, checks the candidate's
gate again, and moves the target only from the base the candidate was built on:
a local fast-forward on an unprotected target, and the candidate's own pull
request (on a branch named `yoyodyne/merge-queue/<entry>`) on a protected one,
which the local target follows once the forge's merge is confirmed. The landing
is then recorded on the run as its integration and the item is settled the way
a run's own landing settles it. The run's own pull request is closed with a
comment naming the entry that landed it, and the run's branch and the queue's
candidate branch are deleted from the remote on the commit the harness last put
there, so neither is left open; a failure to tidy them is said on the queue's
pass and holds the landing up in nothing.

**The next entry is checked while the first one is.** On a target the harness
moves itself, in a project that does not publish, a candidate lands as exactly
its own commit, so the queue builds the next waiting entry's candidate on top
of the first entry's candidate, as if it lands, and runs its protected-path
check, checks and review beside the first one's. It checks at most one entry
ahead, and the pass lands the first entry once both are done. The candidate
checked ahead is a commit of its own with its own recorded base, and its
evidence is bound to it alone; it lands only if, as the promotion lease is
taken, the target is exactly the commit it was built on. Where the entry before
it fails, is withdrawn or handed back, or lands as any other commit, the
candidate checked ahead is thrown away — recorded as `base_not_landed` — and
built again on the target as it stands, and nothing is charged to its change;
nothing found on it is handed back on its own account, and a check that fails
on it is run on its base only once that base is the target. A project that
publishes lands every candidate through a pull request, and the forge lands
that with a merge commit of its own above the candidate, so the target never
becomes the candidate: nothing is checked ahead there.

**The candidate's checks and review pass the doors a run's do.** Nothing starts
while the operator has paused harness activity (`yoyo pause`), and the review
also waits out a provider nobody can reach, a usage limit already known for its
account and model, and a provider that is not installed or not logged in. A
provider refusing the review earns nothing and is asked again later. Holding
intake does not stop the queue: what it lands was approved already, which is
the "let what is running finish" half of the hold.

**A failing candidate is the change's only where the target passes.** The queue
runs no checks of the target's own, so a check that fails on a candidate is run
once more on the target at the candidate's base. Where the base passes it, the
failure is the change's: the change is handed back to its run for repair under
the run's own repair budget, or, with that spent, put to the development manager.
Where the base fails it too, the failure is the target's: the item a red landing
files for that check on that branch is found or filed, nothing is charged to the
change, and the entry steps aside for the entries behind it — one of which may
be the fix — and is built afresh once the target moves. A head that will not
merge onto the target at all, because the target moved while it waited, is
first given its run's one reconciliation attempt rather than a stop: the run is
put back live at its developer attempt, the reconciling sweep hosts it, its
developer reconciles the conflict on top of the target, and the change is
checked and reviewed again and rejoins the queue by itself, charged no repair
attempt and no fresh run (`merge-queue-behind` in
[run stops](../run-stops.md)); while every developer slot is taken, the queue
gives it at a later pass. A run that already used that attempt, or cannot take
it, is handed back the way a replay conflict is, saying why, so the repair
continuation moves the change onto the target and asks its developer to
reconcile it. A handback no single run can take up goes on the development
manager's docket. While the forge holds a candidate's merge, a check on the
candidate's pull request that ended without judging the change — cancelled by
its own time limit, out of time, or never started — is run again once, at once,
and the queue's pass says so on status and the dashboard. At every pass that
finds GitHub holding the merge, the queue also asks GitHub what the merge is
waiting on — whether the pull request can merge, its merge state, its reviews,
and how each check the branch rules require stands — and keeps the latest
answer on the entry with its time; status and the dashboard show it beside the
entry. A pass that cannot ask GitHub, because GitHub cannot be reached or its
certificate cannot be checked, keeps that failure instead, and the time until
GitHub can be asked again is not counted toward `triage.stuck_merge_age`. A
candidate's merge the forge drops, holds queued longer than
`triage.stuck_merge_age`, or can never make — because a required check failed
on it with no required check still running, or because such a check ended
without judging the change again after being run again, or could not be run
again, with nothing else still running — is withdrawn by the queue's own pass,
naming what GitHub last said the merge was waiting on or the check that
failed, and once the forge confirms the withdrawal the entry leaves
the queue and the change is handed back to its run, kept, for the development
manager to decide on. That limit counts from when the harness asked the forge
for the merge, which it does only once the entry reaches the head of the queue,
so time spent waiting behind other entries never counts toward it; the
withdrawal says how long the forge held the merge and how long the entry was in
the queue before that. An entry that has left the queue holds up nothing behind
it: if its run cannot be told, the pass says so, works the entries behind it,
and tries again on the next pass.

While GitHub holds a candidate's merge, each pass reads the candidate's checks
on GitHub as well. A check that ended without judging the change is run again
as the paragraph above says, and what follows is about the checks that did
judge it. Where they fail only in tests the candidate does not touch —
every failure in GitHub's log is a named Go test, there are at most five, and
none is in a directory the candidate changes — the pass asks GitHub to run the
failing job once more on the same commit, writing the request down on the
entry first, so a restart never asks twice for one candidate. While that run
is asked for or going, a required check GitHub reports as failed does not
withdraw the entry, because that failure is the one being run again. If that run
passes, GitHub makes the merge and the entry lands. If it fails again, or the
failure was anything else, or GitHub will not run the job again, the entry is
withdrawn at once rather than after `triage.stuck_merge_age`, and the handback
names the failing tests and says whether the candidate touched them; a failure
in tests it does not touch is not put down to the change, and charges it
nothing.

The same goes for a candidate whose merge GitHub's own merge queue drops
because its checks failed on the merge commit the queue built. Running the
failed job again on that commit would not put the pull request back in GitHub's
queue, so where the failure is confined to tests the candidate does not touch,
the pass asks for the candidate's merge once more instead: GitHub builds a new
merge commit and runs its checks again. That request is written on the entry
first and made once per candidate; a second drop withdraws the entry, naming
the tests.

**Which queue an entry is in is chosen when it is admitted.** The harness runs
the queue itself on any forge. The forge's own merge queue is used only where
its adapter establishes, and the forge enforces before landing, everything the
harness's queue gates on — the combined commit it lands, the project's
configured checks bound to that commit, and an independent approval of it.
GitHub's queue does not today: its required checks are named rather than the
project's, and its required reviews approve a pull request's head rather than
the commit it lands. The mode chosen, and why, is recorded on the entry and
shown wherever the queue is.

**A target that lands changes only through the forge's own queue holds the
change rather than merging it**, because the forge would land a commit of its
own rather than the candidate the harness checked and reviewed, and merging the
old way would land work through the path the switch turned off. Before it
admits anything the harness reads the target branch's protection. Where that
protection requires GitHub's merge queue, or cannot be read:

- nothing is admitted to the queue and nothing is replayed or merged the old
  way;
- the run stops at its promotion with its approval standing — an integration
  stop outside the work, cause `merge-queue-held`, which charges nothing and
  counts toward no brake — and the item is blocked naming the setting: the
  branch rule that requires the forge's queue, or the protection read that
  failed and why;
- one report is filed for the queue when the condition is first found, and no
  further report until it has cleared and been found again; the condition is
  also kept beside the queue, where `yoyo status` and the dashboard show it. A
  requirement or a refused login is a person's to change; a forge that simply
  did not answer is the development manager's to watch;
- the harness never changes a repository setting.

A watching session looks at the held changes every five minutes. At the first
reading that finds the protection readable and the forge's queue not required,
it resumes each held run at its promotion, in the order they were held — which
is the order they were approved — through the same action as
`yoyo triage resume`, and each is admitted to the queue as any approved change
is; the intake hold and a full harness hold that taking up as they hold any
resumption. A change already admitted when the requirement appears is not
landed while it stands: its verified candidate waits with its checks and review kept, and a
target that moves meanwhile is drift as always.

With the switch off nothing of this applies, so a person can remove GitHub's
queue requirement from the branch's protection rules before turning the switch
on, and merging carries on as before in between.

**Turning the switch off admits nothing new and drains what is queued.** Each
entry lands in the mode it was admitted in; nothing is discarded, admitted
twice, or moved to another mode. A change the queue was holding because of the
forge's own queue is taken up at the next look and integrated the way changes
integrate with the switch off. A watching session works every queue that has
an entry in it whatever the switch says, and a restarted one carries on from the
queue's records.

**Moving an entry to the other mode is something a person asks for:**

```sh
yoyo queue transfer <entry> --mode harness|forge
```

Whatever the entry's current queue was asked for is withdrawn first, and the
withdrawal has to be confirmed: while the forge has not said it took a queued
merge back, the transfer moves nothing. The entry is then released, and the
change is admitted again in the other mode in the place it had, keeping the
released entry, its candidates, and its landing record as history. The new
entry has no candidate; it is built, checked and reviewed from nothing. A
transfer interrupted part-way is finished by asking again.

**Pushing a candidate.** A push of the candidate branch that fails without the
remote refusing it — cut off by a restart onto a new build, timed out, exiting
with nothing printed, or failing to reach the remote at all, such as a host
that could not be looked up, a dropped connection or a server error — is not
read as a refusal: the next pass looks at the candidate branch, records the
push made where it arrived, and asks for it again where it did not, so nothing
is pushed twice. A push the remote does refuse, which Git reports by naming the
branch as rejected, hands that one entry back to its run, with the refusal in its words, for the
development manager to decide on, and the queue goes on to the entries behind
it.

**An entry the queue stopped on** is taken up again, or handed back, with a
command rather than by editing records:

```sh
yoyo queue retry <entry> --reason "<why>"
yoyo queue hand-back <entry> --reason "<why>"
```

`retry` sets the stopped promotion attempt aside with the reason, and the next
pass begins a fresh one on the same checked and reviewed candidate where it
still stands. It refuses where something the queue asked the forge for could
still land the change, and leaves a request with no recorded answer to the next
pass, which looks at what it did first. `hand-back` withdraws whatever the queue
asked the forge for, as a transfer does, and ends the entry's turn with its
change kept on its run for the development manager, so the entries behind it go
on. Before it hands the change back, it looks for it on the target: where the
target carries the approved head, or the run's own pull request reads as merged
with the approved head as its head and its merge commit is on the target — a
change merged by hand while the queue was stopped on it — the entry leaves the
queue as landed instead, the run ends landed, and its work item is settled as
after any landing, with nothing asked of the development manager. Where the
target does not show the change, the hand-back's reason says what was found,
such as a pull request that reads as merged with nothing of it on the target.
A hand-back whose work item is already closed leaves the item closed and says
so in a note, without stopping the run or asking anything.

The development manager can release the entry at the head of a queue herself,
from her conversation, whatever stopped it there: a push the remote refused, a
push cut off before it was answered, an approved commit already on the target
branch, or a candidate that could not be built. Her `queue` action names the
entry and the item it is for, and either hands the entry back to that item, as
`hand-back` does, or, where the target branch already holds the entry's approved
commit, records it as landed outside the queue: the run records the change as
integrated, and the item is settled as its run's claim says. Either way the
queue entry and the work item both say who released it and why, and the next
pass goes on to the entry behind it. Only the head is released, because only the
head holds the others back, and the release is refused while anything the queue
asked the forge for could still land the change. `yoyo status` shows an entry
released as landed as landed outside the queue, with who released it and why.

**Who works the queues.** A `yoyo work` session works every queue with an entry
in it beside its pulls, one pass per queue at a time, each in its own
goroutine, and never inside a developer slot. A queue is worked by one process
at a time: each step takes the queue's own worker lease, which is not the
promotion lease, so a second session finds the queue taken and leaves it. Queues
for different target branches move independently. A pass ends on its own — when
nothing can move, when what is left waits on the operator, a provider or the
forge, or when it stops on something that needs a look. A pass that ends
waiting goes round again by itself a minute later, without waiting for the
session's next pull, so a merge the forge makes is noticed within a few minutes
however long the session's pulls take while the tracker is slow. Recording a
landing on its work item may take at most a minute; where the tracker has not
answered by then, the queue goes on to the next entry and records the work item
at a later pass, so a slow tracker never holds the next candidate. Each time a
candidate's checks begin, the queue writes beside itself how long it waited
since its last landing — from the forge's own merge time where the forge gave
one — so the queue's weekly figures can count it. A session draining to restart
into a new build stops its passes going round and waits for them like its
recurring firings. Past the drain bound no pass starts, and none starts a check
stage or a review; a pass whose candidate's checks are running is let finish
them, each check up to its configured `check_timeout`, so the check under way is
not thrown away — and none for more than ten minutes past the drain bound, when
the pass is stopped and the session restarts. Every other pass is given thirty
seconds at the restart to end the step it is in, which ends it waiting, and is
stopped only after that. A pass its session stops part-way through a step is
recorded as waiting on the next pass, never as stopped: whatever the stopped
step then failed on, such as reading where the target stands, says nothing about
the queue. A stage stopped
part-way keeps the checks that had already passed, as long as the candidate's
commit and its list of checks are unchanged, and the session that comes back
runs only the others, the one cut off included; the cut-off check is recorded
as cut off by the restart, not as a failure, and nothing is charged for it.
That wait too is given up while a developer slot sits empty with work for it:
the pass is then stopped part-way, and the session that comes back keeps what
had passed as above. Where the record of the stopped attempt cannot say which
checks passed on that candidate, every check runs again and the candidate's
record says why. Past the bound no queue starts a new
check stage; the session that comes back runs it. While the session waits, its
line names the queue, the candidate's generation, the check, and the latest
moment that check can run to. A candidate's checks and review write to that
candidate's own event stream under the product's merge queues.

**What you can see.** `yoyo status` prints each queue under the four lines, and
the dashboard has a section for them, both from the shared read model: each
entry in the order it is worked, named by its work item's identifier and title,
with its queue mode, where it stands — waiting, verifying, failed, waiting on the
target, verified, landing, held by the forge, uncertain, landed, handed back, or
released — and why, and what the queue's last pass found. Only a landing the
queue confirmed on the target reads as landed: an approval, or a candidate that
passed its gate, never does, and a merge asked of the target or the forge whose
outcome nothing has established reads as uncertain.

`execution.merge_queue` is a configuration key, so a running part of the product
on a build from before it refuses a file that carries it; restart the product
after upgrading and before turning it on.
