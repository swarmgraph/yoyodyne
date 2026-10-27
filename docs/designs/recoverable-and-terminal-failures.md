---
id: recoverable-and-terminal-failures
kind: design
title: 'Recoverable and terminal failures: the boundary per failure class, and what a retry may never do'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-09-25T09:00:00Z
      reason: yoyodyne-ifd.265 - the operator's 2026-09-03 rule that the harness never fails outright on anything that can recover, drawn as a taxonomy per boundary; the provider boundary's window narrowed to thirty minutes, lease-held waits bounded to the promotion queue's fifteen minutes, and what exhausting a window produces stated
    - action: amended
      by: architect
      at: 2026-10-12T13:29:00.37981Z
      reason: 'yoyodyne-gms - states the October 11 ruling on checks stopped by their time limit: unfinished rather than failed, rerun from the start on the same candidate at most twice within three times the limit, bounds shared by every path and never reset by a restart, no reruns added by scaling for machine load, spent bounds kept for the development manager; resent with the one undefined joined word in the closing sentence written out in ordinary words after the word check refused the first run'
    - action: amended
      by: architect
      at: 2026-10-19T11:31:07.53834Z
      reason: 'Answers the reviewer''s reports on the hold on new work (report-c3429c9e and report-17c8bcbf): the brake keeps out of its count only the causes outside the work stopped run recovery lists and cancellations, counts the harness''s own failures and unrecognized causes, and never lets a trial run that stopped for an uncounted cause make the next trial due at once. Changes the brake bullet and adds the order and checks for it; nothing else.'
    - action: amended
      by: architect
      at: 2026-10-19T18:23:59.886625Z
      reason: 'yoyodyne-ifd.429.86 - the October 19 ruling on a check failure confined to tests the change does not touch: how the harness decides a test is untouched from paths alone, one rerun of only those tests on the same candidate commit before a repair attempt is spent, a second failure checked on the base commit so a change''s own failure is charged and a failure on main is held for the development manager, the reviewer and the dashboard shown every test that failed and then passed, and the build work it needs. Changes the checks boundary''s first sentence and adds one section, its orders and its checks; nothing else.'
    - action: amended
      by: architect
      at: 2026-10-22T15:36:38.20726Z
      reason: Replaces the retired phrase for a round refused from outside the work, in the Local Git boundary, with the terms register's own ordinary wording, as part of replacing the coined vocabulary in the governed documents (yoyodyne-ifd.437.21). One sentence changes; nothing else.
    - action: amended
      by: architect
      at: 2026-10-27T03:46:36.765086Z
      reason: 'Says which code makes each decision this design describes, as the design that gives each guarantee one owner requires (yoyodyne-ifd.428.127): a retry in place and a further recovery act are decided by internal/budget, and the reruns of a check by the plan in internal/checks; the windows and bounds are unchanged. Corrects the two places that said the checks before promotion always run whole, which the race check ruling changed. Written from a read at main 0158d2ad.'
approvals:
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-12T13:29:00.37981Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 754, for document-754.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-10-19T11:31:07.53834Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1276, for document-1276.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 3
      by: harness
      at: 2026-10-19T18:23:59.886625Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1296, for document-1296.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 4
      by: harness
      at: 2026-10-22T15:36:38.20726Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1496, for document-1496.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 5
      by: harness
      at: 2026-10-27T03:46:36.765086Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1830, for document-1830.1 under the automatic approval policy
---
# Recoverable and terminal failures: the boundary per failure class, and what a retry may never do

## What this is for

The operator's standing rule of 2026-09-03 is that the harness never fails outright on anything that can recover, waiting Fibonacci seconds capped at half an hour and asking again. The rule is safe only with its boundary drawn: retrying a judgment on the work is as wrong as terminating on a reset connection, and a retry that could double-promote, double-spend, or hold a shared lease through an outage is worse than the failure it replaced. This design draws that boundary, per place the harness meets somebody else's system, and serves the autonomy goal: a run should end for a reason about the work, and for nothing the next attempt would have survived.

## Three classes, one question

Every failure at a boundary is one of three things, decided by one question: **would the identical request, made again later, get a different answer for no reason of ours?**

- **Recoverable** — yes: a connection reset, a transport drop, a store too busy to answer, a server-side error that is not an answer about the request. It is waited out on the recovery series and asked again, inside the boundary's window, with every wait recorded before it is taken.
- **An answer** — no: a verdict, a failing check, a refused protected path, a conflict, an authentication refusal, a protection rule unmet, a 4xx of any kind, a merge method forbidden. It is handed to the party whose answer it is, at once, and never retried.
- **Ambiguous** — the request may have taken effect and nothing said so: a push or a merge that timed out after it was sent. It is never retried blind. The boundary re-reads the state it would have changed and either adopts what it finds or retries a request that is idempotent by construction. Every mutating request the harness makes to a remote is compare-and-swap or pinned to a commit, which is what makes that safe.

A failure whose class the harness does not recognize is treated as an answer and reported, never retried: the recoverable set is small and enumerated per boundary below, and anything outside it keeps the behavior it had. Widening a set is a change to this design.

## The boundaries

**Provider invocation.** Recoverable: a connection closed mid-response, a transport error, a provider `api_error` with a 5xx status other than 529, and a stream that ended an invocation twice. Each is reissued in the same session under `execution.transient_relaunches_before_blocking`, and past that budget only a plainly dropped connection continues onto the recovery series — inside a **thirty-minute window** for this boundary, not the two hours the forge boundaries get, because every reissue here is a full billable invocation and a provider that keeps dropping a completed reply bills for each one. Waits with their own machinery are not this class and keep it: a usage limit waits to its reset under the usage-limit pauses, an overload waits `execution.server_overload_pause`, and a provider that is not authenticated or cannot be reached is the named wait that spends nothing. Answers: a 4xx other than the authentication refusal, a stream the harness cannot read, and a stop the harness itself made for a stall or an exhausted budget, which owes a continuation rather than a retry.

**The forge and the remote** — the push, the pull request opened or updated, the remote target read, the merge request, the merge confirmation, the remote branch deleted, the catch-up. Recoverable: transport failures, 5xx answers, and a rate limit that names when to ask again. Answers: an authentication refusal, a protection rule unmet, a conflict with the base, a head that is not the integrated commit, a method the repository forbids, and any 4xx. Ambiguous: a push or a merge request that timed out after it was sent, disambiguated by reading the branch or the request's state before anything is asked again; the push is compare-and-swap and the merge is pinned to the commit and asked as of the branch's requirements, so a request that did take effect is found rather than repeated. Each of these boundaries keeps its own two-hour window.

**The tracker.** Recoverable: a `bd` killed under load, a lock that timed out, a listing that did not return. Answers: a refusal by content — an unknown item, a status the tracker will not move, a claim it declines for a reason it states. The read a run makes at a gate boundary parks the run when its window is spent rather than ending it, because a busy store is not a verdict; the read a dispatch makes before any run exists refuses the dispatch and records nothing on a run, because there is none. A conversation's calls share one window per operator message.

**Local Git.** A command the harness's own budget ended was refused from outside the work and is not retried in place: a replay it killed is abandoned and recorded as an integration stop the resume picks up, and a checkout it killed is refused with nothing charged. A conflict is an answer, to a person, with both sides preserved. Neither is a recoverable failure, because the identical command on the same machine at the same load is not a different request.

**The checks and the review.** A failing check and a repair verdict are answers to the developer and are never retried, with one bounded exception: a failure confined to tests the change does not touch, which the next section describes. A check stopped by its own time limit, or by the stage's limit, is not a failing check: it did not finish, so it is neither a pass nor a failure, and it earns no credit toward any gate. It is recovered within fixed bounds:

- It is run again from the start, on the same candidate commit, at most twice, and the first run and both reruns together take at most three times its limit. Both bounds are shared by every path that runs checks, so no path gets an allowance of its own.
- Each rerun is recorded on the run before it starts. A restart or redeploy of the harness never resets the count: a process that comes back finds the reruns it had already spent.
- Scaling a check's limit for machine load lengthens that limit and adds no reruns.
- When either bound is spent, the change is kept, no repair attempt or review round is charged, and the run stops for a cause outside the work. It goes to the development manager, who decides what happens to it, and never back to the developer as a failure, because nothing about the change was judged.
- A new candidate commit starts the count again.

The time limit is still the operator's statement about how long the suite may take on this machine, and the two reruns within three times the limit are what keep the load it exists to bound bounded. Treating a stop by the limit as a failing check sent developers to repair changes that nothing was wrong with, and that is the failure this rule removes. A reviewer invocation the provider killed is the provider boundary, and is reissued without a repair attempt being spent.

## A check that fails only in tests the change does not touch

Ruled on October 19 for the item asking how such a failure is run once more before a repair attempt is spent (yoyodyne-ifd.429.86), after the factory-flow program manager's account of two runs that spent every repair attempt on tests they did not touch (report-1d9951df). One of them was the Slack notice for a lapsed provider login (yoyodyne-ifd.431.9), which the reviewer had already approved. A test that fails on some runs and passes on others when the machine is busy is a fault in that test, and fixing each such test stays with its own work (yoyodyne-ifd.429.85, yoyodyne-ifd.429.78, yoyodyne-ifd.428.106). This rule stops such a test from spending a change's repair attempts while it waits to be fixed, without ever letting a real failure through.

### Which tests a change touches

The harness decides from the check's failing tests and the change's paths, and from nothing about any language:

- Each failing test must be named, together with the file it is in, by the reading of the check's output that records failing tests. The project declares how its check output names them; this repository declares it for Go. A failure in which any failing test cannot be named with its file is an ordinary failed check, and nothing in this section applies.
- A failing test is touched when its own file changed, or when any changed path is in the same directory as its file. A deleted file counts, and a renamed file counts under both its old and its new path, as in [choosing checks by what a change touches](choosing-checks-by-change.md).
- Where the project declares that design's analyser and it returns complete evidence for the change, a test in any directory the analyser lists as affected is touched too. Where the analyser fails, cannot tell, or one of that design's reasons to run the whole suite holds, only the rule above decides. The analyser only ever adds tests to the touched set; it never takes one out.
- A failure is confined to untouched tests only when every failing test in it is untouched and there are at most five of them. More than five failing tests is an ordinary failed check, because that many failures together is more likely the change than chance.

### What the harness does

1. It runs only the failing tests again, once, on the same candidate commit, with the check's own command narrowed to those tests and under the check's own time limit. The rerun is recorded on the run before it starts, and a restart of the harness never resets it.
2. If every one of them passes, the check counts as passed. Both results are kept on the run: the first failure with its output, and the passing rerun. No repair attempt and no review round is spent.
3. If any of them fails again, the harness runs those tests once on the base commit the candidate was built on, in a checkout it makes for that purpose and removes afterwards.
   - If they pass on the base, the failure is the change's. It goes to the developer as an ordinary failed check, with both failing outputs, and spends one repair attempt as it does today.
   - If they fail on the base too, the change did not cause the failure. The change is not integrated, because the check did not pass. The change is kept, no repair attempt is spent, and the run stops for a cause outside the work and goes to the development manager. The harness files a warning report naming each test that fails on the base and the base commit, because a test failing on main stops every change that runs it.
4. If the rerun or the run on the base is stopped by its time limit, or cannot be started, the first failure stands and is handled as an ordinary failed check, exactly as it was before this rule.

### The bounds

- Each check stage gets at most one such rerun per candidate commit. A new candidate commit starts again.
- The rule applies at every stage where the harness runs checks on this machine: before review and before promotion.
- A check stopped by its time limit keeps its own two reruns under the boundary above. A run of it that then finishes with a failure confined to untouched tests may use this rule once; the two allowances are separate and neither extends the other.
- Exactly one rerun, never more. A test that passes one time in three would land a real fault if it were run until it passed.

### Nothing is hidden

- The reviewer's evidence lists every test that failed and then passed, with the first failure's output.
- The merge candidate is still verified in full before it lands: by the checks before promotion on this machine, or, where guarantee 9 of [one owner for each guarantee](one-owner-for-each-guarantee.md) allows it, by the forge's required check run on the change combined with current main. CI on the pull request, the merge queue and main still runs whole. So a test that passed only on its rerun runs again before the change lands.
- The read model counts, for each test name, how often it failed and then passed and when it last did. A test seen three times in seven days is shown in the dashboard's section for what is broken, stalled, blocked or degraded (yoyodyne-ps2), owned by the development manager, until a fix for it lands or seven days pass without it recurring.
- A rerun that passed changes nothing for the brake on new work, because no run stopped.

### Checks on CI

Where the full checks run on CI under [checks on CI](checks-on-ci.md), a failure of the required checks confined to untouched tests may be run again once through the forge adapter, for the same commit, where the adapter can ask the forge to run the failed jobs again. Where it cannot, the failure is an ordinary failed check. This is built after the rule for checks on this machine.

### Alternatives rejected

- **Running every failed check again whole.** Rejected because a whole suite takes fifteen to thirty minutes or more when runs overlap, and that load is what makes these tests fail in the first place.
- **Running any failing test again, touched or not.** Rejected because a test whose code the change edited is most likely failing because of the change, and a rerun would let an intermittent fault the change introduced pass.
- **More than one rerun.** Rejected because each extra rerun raises the chance that a real but intermittent failure is passed over.
- **A list of known unreliable tests to skip.** Rejected because it removes coverage without anybody deciding it for each change, and such a list goes stale.
- **Requiring the analyser before any rerun.** Rejected because a project that declares none would get no relief at all; the directory rule is the floor, and the analyser only makes it stricter.

## Where each decision is made

The windows, classes and bounds in this design are unchanged. Which code makes each decision is settled by [one owner for each guarantee](one-owner-for-each-guarantee.md), and this section says how that applies here:

- **A retry in place at a boundary** is decided by `internal/budget`, under guarantee 4 of that design: given what the boundary has already spent and its window here, it says whether one more may be taken, and why not in ordinary words when it may not. The window itself stays as this design sets it for each boundary. Boundaries move onto it one at a time, last in that design's order of migration. Until a boundary has moved, its own code decides, against the same window.
- **A further repair, a re-run of a stopped run, a merge tried again, and the harness's own continues** are decided by `internal/budget` too, with a grant and an attempt counted in one unit (yoyodyne-ifd.428.86). The recovery budget itself stays on the run record.
- **When a check is run again** is not a recovery act and is not decided by `internal/budget`. The two reruns of a check stopped by its time limit, and the one rerun of a failure confined to untouched tests, are decided by the plan in `internal/checks`, under guarantee 9 of that design, and launched only by its runner. Their bounds are the ones this design states, and no path keeps a copy of them.
- **The brake** keeps the counting rules below. It becomes a follow-up as guarantee 6 of that design says.

Where the two designs differ, that design decides which code makes a decision, and this one decides the windows and the bounds.

## What a retry may never do

- **Double-promote.** Promotion is a compare-and-swap from the recorded base onto the exact commit the harness made, and a merge is pinned to that commit and confirmed by containment. A retried promotion or merge that finds the target moved is a lost race and replays under `execution.integration_retries_before_reconciliation`; it never forces, and it never asks twice for a merge the forge has already made. `one-promotion-per-target-branch` binds every retry as it binds the first attempt.
- **Double-spend.** Every wait is recorded on the run before it is taken, every budget is committed before it is spent, and a process that dies mid-wait comes back to the window it had spent. A retry charges no repair attempt and no review round, because nothing about the change was judged.
- **Trip the brake.** A run stopped by a boundary's window counts toward nothing the failure-storm brake counts: the brake counts verdicts on a change that was present, and a network that stayed down is a verdict on nothing. Three rules decide the rest, ruled on October 19 after the reviewer's reports on the hold on new work (report-c3429c9e and report-17c8bcbf):
  - Only the causes outside the work that step 1 of [stopped run recovery](stopped-run-recovery.md) lists, and a run cancelled by a person or by a recorded decision, are kept out of the brake's count. A failure of the harness itself around a run, and any stop cause the harness does not recognize, count as a verdict does: a harness that keeps failing is what the brake exists to stop, and nothing else would stop it. Code that sorts an unrecognized cause outside the count by default is a defect.
  - A trial run the brake lets through that stops for a cause kept out of the count never makes the next trial due at once. The next trial waits its ordinary spacing, so trials never start back to back, whatever the cause of the stop and even where a cause is sorted wrongly.
  - Keeping a stop out of the brake's count changes only the brake. Whether a stop charges a repair attempt is still decided by step 1 of stopped run recovery.
- **Hold a shared lease through an outage.** The pre-merge check and the merge request stay together under the target branch's promotion lease, because the check is the evidence the merge stands on and a promotion admitted between them invalidates it. But a recoverable wait taken while holding that lease is bounded to the promotion queue's own bound, **fifteen minutes in total across every lease-held boundary of one promotion**. Past it the holder releases the lease with the boundary's state recorded — a merge asked and unconfirmed, a confirmation not made, a branch not deleted, a catch-up owed — and ends; reconciliation re-asks each of those under the lease again, briefly, as it already does for a queued merge. So an outage longer than fifteen minutes costs the promoting run its confirmation and nothing else, and costs the runs queued behind it nothing at all. That is the bound the notes on this item asked for, and it replaces holding the lease for the sum of five two-hour windows.

## What exhausting a window produces

Never a silent terminal failure. A window that runs out produces exactly what the boundary would have produced on an answer, with the attempts and the time in front of it: an outstanding publication on the item, a blocker naming the provider's own last message, a parked run naming the read it could not make, a refused dispatch naming the store. Each is docketed or held as that class already is, and each reaches the channel at the severity the reach rule gives that class. The escalation names the failure, the boundary, the attempts, and the window, so a person handed it knows the network was retried and for how long rather than reading the last reset as the first.

## What this orders

Two changes to the machinery as shipped, for the product manager to admit: the provider boundary's post-budget window shortened from two hours to thirty minutes, and the lease-held waits bounded to the promotion queue's fifteen minutes with the release-and-record behavior above. Everything else in this design describes the recovery rule as it ships and gives it the taxonomy it was waiting on, including the bounded reruns of a check stopped by its time limit, built by the work that scales each check's time limit with machine load (yoyodyne-ifd.429.41).

One further change, from October 19, reported to the development manager to admit: the code that sorts a stop's cause for the brake counts the harness's own failures and unrecognized causes, keeps out only the causes stopped run recovery lists and cancellations, and gives a trial run that stops for a cause kept out of the count its ordinary spacing before the next one.

The rule on failures confined to untouched tests, ruled on October 19 for yoyodyne-ifd.429.86, needs these builds, for the Lead Product Manager to admit and the development manager to break into work items:

1. Naming each failing test with its file from a check's output, declared by the project, and this repository's declaration for Go.
2. Deciding whether each failing test is touched, from the change's paths and, where declared and complete, the analyser of the design for choosing checks by change.
3. The single rerun of only the failing tests on the same candidate commit: recorded before it starts, never reset by a restart, at most one per check stage per candidate commit, both results kept on the run and listed in the reviewer's evidence.
4. The run on the base commit after a second failure: charged to the developer when the base passes; when the base fails too, the run held for the development manager as a stop outside the work, with the warning report naming the tests and the base commit.
5. The read model's count of tests that failed and then passed, and the dashboard entry for a test seen three times in seven days.
6. Later, the same rule for required checks on CI, through the forge adapter where it can ask for failed jobs to run again.

Moving the decisions in the section on where each decision is made onto their owners is ordered by [one owner for each guarantee](one-owner-for-each-guarantee.md), in its migration items 8, 10, 19 and 20 under the work that gives each guarantee one owner (yoyodyne-ifd.428.126), and is not repeated here.

A reviewer can check: no stop cause outside step 1's list, other than a cancellation, is kept out of the brake's count; and no trial run starts sooner than its ordinary spacing after the one before it stopped.

A reviewer can check where each decision is made: a boundary that has moved onto `internal/budget` keeps no retry rule of its own; the reruns of a check are decided only by the plan in `internal/checks`; and no window or bound in this design has changed.

A reviewer can check the rule on untouched tests:

- A failure with any failing test touched, unnamed, or more than five failing tests spends a repair attempt exactly as before.
- A failure confined to untouched tests reruns only those tests, once, on the same commit, and the rerun is on the run record before it starts.
- A rerun that passes spends no repair attempt, keeps both results, and lists the tests in the reviewer's evidence.
- A second failure that passes on the base commit spends one repair attempt; one that fails on the base too is not integrated, spends none, goes to the development manager, and files the warning report.
- A restart never grants a second rerun for the same stage and commit.
- The merge candidate is still verified in full before it lands, and CI still runs whole.
