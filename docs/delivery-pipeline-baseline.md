# What the delivery pipeline actually guarantees

*For someone changing the harness's execution path, and for whoever comes to
re-express it. Part of [yoyo's documentation](../README.md#further-reading).*

The pipeline in `internal/orchestrator` is the only thing that turns an approved
work item into a promoted change. What it promises has never been written down
in one place: it is spread across five thousand lines of Go, a few hundred
assertions, and [what work flows through](work.md), which describes the path an
operator sees rather than the boundaries the code actually holds.

This document is that enumeration — the delivery paths, the step boundaries, the
events, the durable fields, the side effects, the policy branches, the pause
cases, the retry counters, the reconciliation behavior, and the terminal
outcomes. It is worth having on its own: a specification of the hard-coded
pipeline that a reviewer can read and disagree with. It is worth more as a
baseline, because anything that re-expresses this pipeline — a configurable
executor, a second backend, a rewritten integration step — has to be measured
against what the current one does rather than against what somebody remembers it
doing, and a memory is not something you can diff.

**Nothing here is a decision.** Every line is a description of code that already
exists. Where this document and the code disagree, the code is right and this
document is a defect.

## How this document is kept true

Prose goes stale silently, so the guarantees below are also recorded as
executable traces in `internal/orchestrator/baseline_test.go`, one per delivery
path, under `internal/orchestrator/testdata/baseline/`.

Each trace drives one path end to end against a real Git repository and a fake
provider, and writes down everything the path produced: the outcome the caller
was handed, the durable run record left on disk, the run's event log, every
tracker call and the words the run left on the work item, and each provider
invocation said as the three things that tell one from another: which role it
was made as, what it was asked for — the assigned work item, a review, or a
repair of findings, of a failing check, or of a refused path — and whether it
continued a session an earlier attempt established. The prompt itself is
classified rather than recorded, so what a diff shows is a run asking for a
different thing or losing a session, not a wording change. Temporary directories, commit
identifiers, timestamps, the configuration digest, and elapsed times are
replaced by stable placeholders, so what a trace holds is the behavior rather
than the machine it ran on.

The traces are not assertions about the fields somebody thought to check. They
are the whole of what the path produced, which is what makes them useful to a
parity harness: a new executor is compared to the recorded document field by
field, and a field it writes at a different step or does not write at all shows
up whether or not anyone predicted it.

Changing behavior therefore changes a trace, and re-recording is deliberate:

```sh
go test ./internal/orchestrator -run TestDeliveryPipelineBaseline -update-baseline
```

The diff is the review. An intended change reads as the fields it moved; an
unintended one reads as the fields nobody meant to move.

The section at the end, naming what no trace holds, is held to that promise by a
check rather than by a reader: `TestBaselineDocumentDisclosesEveryFieldNoTraceHolds`
fails when this document states a durable field that no trace carries and the
gap list does not name, and says which field and the sentence that states it.
It is a floor rather than a fence — it recognizes field
names, so a behavior stated only in prose is still a reviewer's to catch — and
it reports rather than decides: recording a trace satisfies it, and so does
naming the field below.

## The entry points

| Entry point | What it is | Covered by a trace |
| --- | --- | --- |
| `Pipeline.Run` on an item with nothing in flight | A fresh run: claim, worktree, develop, gate, promote | yes |
| `Pipeline.Run` on an item with a run in flight | Adopting that run under the same exclusive lease and continuing it from durable state | yes, for the usage-limit pause and the provider stopped on time |
| `Reconciler.Reconcile` | Settling every run no live process owns | yes, for the completed and blocked settlements |
| `yoyo triage repair` (`repaircontinue.go`) | Re-entering a stopped run's repair loop on a granted budget | **no** |
| `yoyo triage rerun` (`rerun.go`) | Starting a fresh run of an item whose last run stopped owing a repair | **no** |
| `BranchReviewer` (`branchreview.go`) | Reviewing a branch outside a run | **no** — not a delivery path |

The three uncovered rows are named rather than omitted. The first two re-enter
the same run machinery through their own preconditions and are part of what a
later executor has to reproduce; a baseline that did not say they were missing
would read as covering them.

## What happens before anything is claimed

`Pipeline.Run` asks a fixed sequence of questions, and the order is itself a
guarantee: each one is cheaper than the one after it, and each refuses before
anything more has been spent.

1. **Wiring and configuration.** A pipeline missing a required collaborator, a
   configuration that does not validate, a developer on a backend with no
   compiled adapter, an agent whose model selector is not pinned, a project with
   no configured checks, and — where integration is automatic — a review policy
   that does not gate it, are each refused here. Nothing is claimed.
2. **The operator's hold on all harness activity.** Read first among the durable
   questions because it is the broadest answer: a held harness starts nothing,
   claims nothing, and does not ask the provider whether it is installed. A run
   already in flight is left in flight rather than resumed into a boundary that
   would park it again.
3. **Publishing.** Whether this run publishes is settled before anything is
   claimed. A project that asked for pull requests and cannot open one fails
   here; a project whose repository has no configured remote degrades to local
   behavior and says so on the outcome as `publish_skipped`.
4. **The work item**, loaded from the tracker.
5. **Unresolved directives that pause this item.** The work stops before it is
   claimed and the outcome names the directive.
6. **Unfinished work this item depends on.** Only a `blocks` dependency counts;
   a parent-child link is not a blocker. The work stops before it is claimed and
   the outcome names the blocking items.
7. **A run already in flight.** Adopting it takes the same exclusive lease a
   fresh reservation takes. It is continued only when its remaining work is
   fully described by durable state — a usage-limit or overload pause, a
   provider the harness stopped on time, a directive pause, a dependency pause,
   an operator hold, or a resumable repair loop under automatic integration —
   and is otherwise refused as `ExistingRunError`.
8. **The intake hold**, asked only here and only for work the harness chose for
   itself. What it holds is the choosing, so a run already under way carries on
   under it and an item the operator named is exempt.
9. **A fresh run substituted for a repair somebody is owed.** An item whose last
   run stopped owing a repair refuses a clean start unless triage claimed the
   re-run.
10. **Item readiness**, the context bundle, the invariants, and the repository's
    readiness for an isolated worktree — which is where a primary checkout
    somebody left uncommitted is refused, by name, file by file.
11. **Provider availability.** Not installed refuses. Not authenticated is
    [a wait rather than a refusal](operations.md#waiting-out-a-provider-nobody-can-reach):
    it is recorded on the product so every surface names it, and reported typed
    so the scheduler counts the dispatch toward nothing — not the brake, not the
    docket, not the session's exclusion of the item — and dispatches nothing
    more until the login is renewed. It
    is asked here, after every question this repository can answer on its own,
    because it is the only refusal among them that is a fact about the machine:
    the ones ahead of it hold whether or not Claude Code is installed, so asking
    this one first would answer a newcomer's uncommitted checkout with "Claude
    Code is not installed". Nothing has been reserved, claimed, or cut by the
    time it is asked, so deferring it spends nothing. A resumed run asks the same
    two questions in the same order, and is charged for neither.
12. **The integration target.** An automatic or publishing run fixes the branch
    it will be promoted into before any work starts, and never infers it
    afterwards.
13. **The run identifier and the provider account.** A pool with nothing left to
    spend refuses here, before a work item has been taken.
14. **Reservation**, which enforces `execution.max_concurrent_developers`.
15. **The claim**, then the item is re-validated against what was claimed and the
    context bundle is re-assembled from it.
16. **The worktree**, cut from the target branch, and the run's state written as
    `running` in the `developing` phase.

**Nine of those sixteen steps have a trace; seven do not.** Traced are the
operator's hold (2), loading the item (4), the directive and dependency
questions (5, 6), continuing a run already in flight (7), the intake hold (8),
fixing the integration target (12), the claim (15), and the worktree (16) — each
by the scenario named for it, or by every scenario that reaches it.

Untraced are the refusals, and they are the half of this sequence a parity
harness is most likely to under-measure, because a refusal that never fires
looks exactly like a step that is not there. Nothing here holds: the wiring and
configuration refusals (1) — a missing collaborator, a configuration that does
not validate, a backend with no compiled adapter, an unpinned model selector, no
configured checks, or a review policy that does not gate automatic integration;
publishing settled before the claim (3), which belongs to the publishing half
listed below; provider availability (11); the `ExistingRunError` refusal of a run
in flight whose remaining work durable state does not fully describe (7); the
refusal of a clean start where a repair is owed (9); item readiness, the
context bundle, the invariants, and the repository's readiness (10); an account
pool with nothing left to spend (13); and the `execution.max_concurrent_developers`
bound reservation enforces (14).

**The order is a guarantee no trace holds either.** Each refusal above is
asserted somewhere in `internal/orchestrator`, but that a held harness refuses
before the provider is asked, and that every refusal answerable from this
repository alone — the uncommitted checkout among them — is reported before the
provider is asked at all, is a property of the sequence rather than of any one
step — so a new executor could satisfy every individual refusal and still ask
them in an order that spends more before refusing, or answer a question about
the repository with a fact about the machine. Measuring that needs its own
harness.

## The steps of a run

| Phase | What it does | What it writes |
| --- | --- | --- |
| `developing` | One developer invocation in the run's worktree, resuming the run's session on every attempt after the first, and a harness commit of whatever it left there; retains the latest completed account until a final reply replaces it, bound to the attempt and change content | `provider_session_id`, `provider_model`, `provider_resolved_model`, `harness_commit`, `changes`, `last_sequence`, `verification`, `developer_summary` |
| `checking` | The protected-path gate first, then the execution-evidence gate, then every configured check in order | `path_refusal`, the `verification` record's `owed`, or `check_failure` while one is outstanding, and clears the others when a gate passes; `checks_passed` when every check passes, bound to the content of the change as the worktree holds it, the attempt, and the harness commit |
| `reviewing` | One independent review invocation, its own session, no tools, shown the branch's whole diff against the run's recorded base and the developer's latest bounded final account, with its recorded attempt and whether the content and base still match stated; unavailable accounts are stated as missing with a reason and any cut is visible | `review_session_id`, `review_model`, `review_resolved_model`, `review_base_commit`, `review_head_commit`, `review_decision`, `review_approves`, `review_summary`, `review_findings`, `review_finding_details`, `review_rounds` |
| `integrating` | Reads the gate off the record first — `checks_passed` for exactly this attempt and commit, and for the content the worktree holds now, no `check_failure` or `path_refusal` standing, an approving verdict — and refuses without it, before the lease is taken; then, under the target branch's promotion lease: fast-forward the local target onto the branch tip the developer's attempts were committed at, and merge where the project publishes | `integration`, `pull_request`; `harness_commit` only when the promotion is replayed or a refused promotion committed something itself |
| `completing` | Record the outcome on the item, settle it — closed when the change discharges the item, back in the backlog parked or waiting on a named impediment when it does not — price it | the tracker's record and settlement |
| `cleaning_up` | Remove the worktree and the branch, each recorded separately | `worktree_removed`, `branch_removed` |
| `complete` | Nothing outstanding | `completed_at` |

A run whose integration a person still approves stops after `checking`: it
records the outcome, leaves the change on its branch, and closes nothing.

`harness_commit` is written by `developing`, and an ordinary promotion leaves
it where `developing` put it: the commit promoted is that commit, which is why
the traces of a promotion carry it equal to `integration`'s `source_commit`.
`integrating` rewrites it in two cases only. A replay onto a moved target names
the replayed commit, or clears it where nothing is left above the new base; and
a promotion refused after it committed what the developer left in the worktree
names that commit before it reports the refusal.

### The repair loop

Under automatic integration the whole gate is a loop, and every round asks the
directive and dependency questions again before it spends another developer
invocation. Three different failures are repair input for the same developer,
and all three draw on **one** budget, `execution.repair_attempts_before_replan`:

1. **A protected-path refusal**, decided before the checks run. Recording it
   clears any check failure and any findings, because those describe a change
   the gate has already moved past.
2. **A failing check**, decided before the reviewer is asked. Recording it clears
   the findings for the same reason. A check that could not run at all — timed
   out, cancelled, or infrastructure that failed — is not repair input and ends
   the run.
3. **A reviewer's repair verdict**, with its findings recorded in full because
   they are the next attempt's input.

Sharing one budget is what bounds the total developer invocations a run can
make. The attempt is recorded before the developer is invoked, so an interrupted
attempt still counts and a restart cannot buy a fresh budget. A run that spends
the budget blocks the item and preserves the worktree and the branch.

Triage can grant further attempts on a stopped run; the granted attempts add to
the configured budget rather than replacing it, and `repair_attempts` goes on
meaning every attempt this run has handed back.

### What the promotion re-earns

An approval authorizes integration only when it demonstrably came from a second
invocation: missing or reused provider identity fails the run rather than
promoting it.

A promotion that loses its race for the target branch is re-prepared rather than
failed. The change is replayed onto where the target went, the published branch
is replaced from exactly the commit the harness put there, the approval is
discarded, and the whole gate runs again — the checks against the replayed
change and a fresh independent verdict on it. The replay spends an
`integration_retries` and **no repair attempt**: it is the target branch that
moved, not the change that failed. Its verdict is counted like any other, so
`review_rounds` goes up — `promotion-is-replayed-when-the-target-branch-moves`
records 2, the same as an ordinary repair round, and a reader expecting a replay
to be free of that counter should read the trace rather than this sentence. A
replay that conflicts is never resolved by the harness: both sides are left
intact and the item is blocked.

## The pause cases

A pause is not a failure. The run stays in flight with its claim, worktree,
branch, and developer session preserved, and the record on disk says what it is
waiting for so a later invocation resumes it rather than starting a second
attempt.

| Pause | Where it is read | What lifts it | On the outcome |
| --- | --- | --- | --- |
| Exhausted provider usage limit | Every developer and reviewer invocation | The deadline the provider named, or `yoyo resume` | `paused`, `pause_cause: usage_limit`, `usage_limit_resets_at`, `usage_limit_kind` |
| Transient provider server overload | The same boundaries | The configured overload interval | `paused`, `pause_cause: server_overload` |
| A provider invocation the harness stopped on time | After an invocation that stalled or exhausted its budget | Nothing; it is runnable immediately | `paused`, `provider_stop` |
| An unresolved user directive | Before the claim, before a resume, and at every round of the gate including the promotion | Somebody settling the directive | `paused`, `paused_by_directive` |
| Unfinished work the item depends on | The same boundaries | That work closing, or the link being removed | `paused`, `paused_by_dependency` |
| The operator's hold on harness activity | Every provider-call boundary | The operator lifting it | `paused`, `paused_by_operator`, `pause_cause: operator_hold` |
| The operator's hold on intake | Only where a run would be started for a reason other than the operator naming the item | The operator lifting it | `paused`, `paused_by_intake`, and **no run at all** |

The directive, the dependency, and both holds can also stop work before there is
a run: nothing is claimed and no worktree exists, so the outcome names the work
item and what stopped it and nothing else.

Two bounds govern waiting. `execution.usage_limit_in_process_pause` is how much
of a wait this process will hold open — a longer one exits with the run still in
flight, to be picked up later — and `execution.usage_limit_max_pause` bounds the
waiting one run may commit to across every pause it takes, recorded in
`usage_limit_paused_seconds` so a restart part-way through cannot buy a fresh
budget. Time an operator hold accounts for is kept apart in
`operator_held_seconds` and is bounded by nothing: a maximum pause that stopped
a held run would be the harness overriding the operator.

Beside the deadline, the run's record keeps three things the deadline alone
cannot say, because a park is read back as a refusal wherever the refusals
outside a run are read — the [hold over every role](reporting.md#the-provider-holding-every-role)
first among them. `usage_limit_paused_since` is when the pause began, which
every probe after that leaves alone; `usage_limit_reset_unknown` says the
deadline is the harness's own next probe rather than a reset the provider named,
which is every overload and outage wait and a limit the provider gave no reset
for; and `usage_limit_model` is the model selector the refused invocation asked
for, kept because the reviewer's is otherwise recorded only once a review has
answered. The first two are cleared with the deadline; the model outlives it as
the kind does.

## The counters, and what each one bounds

| Counter | Configured by | What it bounds | What it is evidence about |
| --- | --- | --- | --- |
| `repair_attempts` | `execution.repair_attempts_before_replan` | Developer invocations spent on failures of the change | The change |
| `integration_retries` | nothing — it is the record of the races | Nothing the run stops on: every promotion re-prepared after losing the target branch, however many | The target branch moving |
| `charged_replays` | `execution.integration_retries_before_reconciliation` | Replays that stopped on the change — conflicted, or handed back for a failing check, a refused path, missing verification, or a repair verdict; the one that takes the count past the budget stops the run there. A replay that passed is charged nothing | The replayed change |
| `transient_relaunches` | `execution.transient_relaunches_before_blocking` | Provider invocations reissued after one died without judging the work; the developer and the reviewer share it | The provider |
| `usage_limit_paused_seconds` | `execution.usage_limit_max_pause` | Total waiting committed across every pause | The provider's capacity |
| `retries` | nothing in the configuration | Per boundary: a two-hour window of Fibonacci waits capped at half an hour | The network under one boundary |
| `review_rounds` | nothing in the run | Nothing the run stops on — it counts every verdict **this run** obtained, approvals included | What this run has cost |

The first four are per run and go with it, and each is what stops the run when it
is spent. `review_rounds` stops nothing: it is this run's own tally, incremented
on every verdict whichever way it went, and it is never cleared — what a repair
discards is the judgement, not the fact that the work has been round once more.

`retries` is a list rather than a count, and it bounds per boundary rather than
per run: each entry names where the failure happened, which attempt at that
boundary it was, the interval that was waited, and the failure itself, and a
boundary's window is what its own entries have already committed. The boundaries
are every place a run reaches something outside itself — the branch push, opening
the pull request, republishing a replayed branch, reading and confirming the
remote target, the merge, the merge confirmation, deleting the merged branch,
catching the local target up, provider invocation, and the writes a finishing run
makes to the tracker. Only failures whose class is clearly recoverable — a
connection reset, a network drop, a transport-level refusal, a subprocess that
produced no verdict at all — are waited on; everything else is reported exactly
as promptly as it was before, which is what keeps this a wait around the existing
behavior rather than a second opinion about it.

The provider's boundary is the one that interacts with a counter above it.
`transient_relaunches` is spent first and bounds every death; past it, a death
whose class is clearly recoverable goes on being waited out against `retries`,
and only a spent window blocks. That is why the two are separate: the budget
bounds provider weather nobody has classified, and the window bounds a network
that is down.

**It is not the counter `triage.review_rounds_cap` measures, and a repair grant
is not truncated against it.** That one is the work item's durable counter, which
spans every run of the item and takes only the verdicts that sent something back:
an approving verdict records an approved attempt instead. So the two disagree by
design, and reading the run's field as the item's cap would over-count every
approval — including the second verdict a replayed promotion buys.

Each counter is recorded **before** the thing it bounds happens, so a process
that dies mid-attempt resumes against the budget it had.

## What a run does outside itself

| Side effect | When |
| --- | --- |
| `Tracker.Claim` | Once, before the worktree is cut |
| `Tracker.RecordOutcome` | On success, on failure, and on every pause of a claimed run |
| `Tracker.Complete` | Only on a promotion that is where it will stay and a change that discharges the item — the developer's landing and the reviewer's approval both saying so; a merge the forge only queued defers the closure to reconciliation |
| `Tracker.Reopen` | On a promotion whose landing the developer claimed does not discharge the item, on one whose claim could not be read, and on one the reviewer approved as evidence rather than as the work; the item goes back parked unless the landing named the impediment it waits on |
| `Tracker.AddBlocker` | On the same promotion where the landing named its impediment, before the item is returned to the backlog |
| `Tracker.Block` | On every stoppage a person has to decide about, on its own deadline rather than the run's context |
| Git: worktree created, harness commit, local target fast-forwarded, branch and worktree removed | The phases above |
| Forge: branch pushed, pull request opened or updated, merge requested, remote target observed | Only where the project publishes |
| Run record and event log | Every step boundary |
| Spend log, price on the item, reports, amendments, triage docket | Where each is wired; a pipeline without one runs identically and names what it could not keep |

## The events

The run's log carries what the harness itself observed. From a run driven with a
fake provider that is `command.started` and `command.completed` per check, and
`review.started` and `review.completed` per review invocation, plus
`review.drift` where a reviewer answered with a field the verdict schema does not
define. A real provider adapter adds its own stream — `agent.message`,
`process.output`, `file.changed`, and the `run.*` terminals that carry what an
invocation cost — so the recorded traces are the harness's half of the log
rather than the whole of it.

Events are append-only and never rewritten. `last_sequence` on the run record is
where the log had reached, and it is what a resumed invocation continues from.

## Terminal outcomes

| Status | Reached by |
| --- | --- |
| `succeeded` | The gate passed and, where integration is automatic, the promotion landed |
| `failed` | Anything the run could not carry further, including a spent budget |
| `cancelled` | The operator asked the run to stop, or the context was cancelled |
| `timed_out` | The context's deadline passed, or the harness stopped a process on time |

A terminal run is not the only outcome. A paused run is `running` and still in
flight; a held or directive-stopped item may have no run at all.

Beside the status a run also carries an **outcome** — `succeeded`, `stopped`,
`cancelled`, `timed out`, or `failed` — which is the reading of that status in
the closed vocabulary every listing says it in. The status is the durable value
and stays what it always was; the outcome exists because `failed` is accurate
about the attempt and says nothing about the work, so a run handed back to a
person with its branch and worktree intact printed the same word as one that
broke with nothing to show for it.

The outcome is derived rather than durable — `Outcome.Ending()` for a run,
`State.Outcome()` for a record — so every trace carries it beside the status it
was read from, and a settlement carries it as a field of its own. That is what
makes the distinction checkable: `protected-path-refusal-spends-the-repair-budget-and-blocks`
records `failed` and `stopped` together, and an executor that computed `failed`
for a run whose work is preserved would move the derived word without moving any
recorded field.

Three things are reported on a succeeded run rather than turning it into a
failure, because the work is already integrated and the item already settled —
closed where the landing discharged it, back in the backlog where it did not:
`publish_failure` (an outstanding publication), `cleanup_failure` (an artifact
that survives, or a removal that could not be confirmed — `worktree_removed` and
`branch_removed` tell those apart), and `completion_recording_failure` (the final
record arrived late).

Beside the status and the outcome a stopped run records **`stop_class`**, the gate
or bound that stopped it. The closed vocabulary in `runstate` extends the older
gate names with the specific clocks, budgets, requests, and causes outside the work
in [the run-stop inventory](run-stops.md). It is written where the run ends,
including reconciliation and the dead-claim audit. Old records without it read
as `unknown`, with their prose preserved and no inferred classification.
The representative traces cover gate and bound endings; the exhaustive terminal
write test ends a run for every class and reloads its field.

An unsettled refusal from outside the work converts its existing cause into the
same field, except that an exhausted transport recovery window names that bound;
its accounting rules are unchanged. A settled provider
stall or total-time stop retains `provider-idle` or `provider-budget`. An explicit
stop records `operator-stop` or `manager-stop`, a redeploy cancellation records
`redeploy-drain`, and other cancellation keeps `cancelled`. Past these, the
specific bound wins over a broader gate label, whether that gate wrapped the
bound or supplied the error that spent it. A recording failure joined to the
original error does not replace its cause. Leftover `check_failure`, `path_refusal`,
or `review_findings` never classify a later ending. A re-entered or resumed run
clears the class its stop left. Successful landings with an outstanding
publication, cleanup, or recording step keep the corresponding gate class;
they do not count as stops in throughput.

A round **refused from outside the work** is the one stoppage that leaves the
item's budgets where they were: something outside the work refused the round
rather than the work failing,
so a caller that reported the blocker without it would say the item had spent a
round it never spent.

A run that fails having made a branch or a worktree looks at both before it
writes its ending down, and records what it saw as `preservation`:
`branch_present` and `worktree_present`, or `unverified` where the check could
not be made at all. Nothing else in the note claims preservation. The record
cannot supply the claim — `branch_removed` and `worktree_removed` say what the
harness removed, and both are false for an artifact something else took — so a
note derived from them promises a checkout it has never looked at, which is how a
developer came to be sent to a directory that was gone and to report the work in
it destroyed.

The removal flags do settle the other direction, and the check reads them for
that: an artifact this run's own cleanup removed is gone on purpose, so it is
carried onto the `preservation` as removed, never looked for, and never reported
lost — a run that promoted its work and failed afterwards has the integrated
commit as its surviving evidence and no preserved-work ref to send anybody to.
What is left is stated as the loss it is, at the top of the note and in the run's
own failure: an artifact the run made, nothing recorded removing, and the check
did not find, with the branch and the sweep's preserved-work ref named as the two
places the work can still be.

## Reconciliation

`Reconciler.Reconcile` settles every run no live process owns. It takes each
run's lease and re-reads its state under it, so nothing is decided from a
listing another process has moved on from. **It never starts a developer.**

| Action | When |
| --- | --- |
| `held` | A live process holds the run; it is left alone |
| `cancelled` | Somebody asked the run to stop and no process holds it, so nothing would reach the boundary that honours a stop: the sweep ends it cancelled at once, as the run would have, the artifacts left as they were and a stop the development manager decided docketed and closed by her decision |
| `resumable` | The run's own pipeline can continue it: a repair loop with durable state, a usage-limit or overload pause, an operator hold that still stands, or — within the last thirty minutes — a provider stopped on time, a directive, dependency, or tracker pause, an outage wait past its probe, or an operator hold since lifted |
| `blocked`, for a run whose process vanished | A park nothing continued for thirty minutes (`Reconciler.VanishedGrace`) — a provider stopped on time, a directive, dependency, or tracker pause, an outage wait, or an operator hold since lifted: the harness ends the run, recorded as ended by something outside the work (`process-vanished`), naming what the sweep observed and what it was parked on, the item is blocked, the artifacts are left as they were, and the stoppage is docketed |
| `queued` | The forge accepted a merge it has not performed; nothing can be decided until it does |
| `completed` | The work is promoted — recorded, or found in the target branch by containment — so the item is settled by the run's own records, its landing claim and what its reviewer approved: closed, or put back parked or waiting, and the artifacts removed |
| `blocked` | Nothing could finish the run; the item carries a durable blocker and the work is preserved |
| `failed` | The run left nothing behind for anyone to act on |
| `unsettled` | Settlement itself could not finish; the run stays outstanding for the next sweep |

A settlement reports the run's status and its outcome, the phase it was
interrupted in, and the artifacts it made — the branch and the worktree by name,
beside the two flags saying which of them the sweep removed. Naming them is what
makes the report checkable: a sweep saying a branch was removed without saying
which one has told an operator something they cannot verify. A record naming no
artifact at all and one whose artifacts were removed are opposite answers to "is
my work gone", and the flags are false on both, so the names are what tells them
apart.

An interruption inside the integration step is the one boundary durable state
cannot describe — the promotion either landed or it did not — and only the
repository can answer. A recorded integration that the repository contradicts is
blocked rather than believed. A second sweep over a settled run does nothing:
re-closing or re-blocking an item is exactly what a sweep must not do.

## The recorded scenarios

| Trace | What it freezes |
| --- | --- |
| `human-approved-change-is-preserved-for-its-approver` | The non-automatic path stops after the checks and promotes, closes, and cleans up nothing |
| `automatic-run-promotes-reviews-closes-and-cleans-up` | The whole happy path, including both cleanup steps |
| `failing-check-is-repaired-and-then-promoted` | A check failure as repair input, and the durable failure cleared once it passes |
| `failing-check-spends-the-repair-budget-and-blocks` | The repair budget, and what a blocked run leaves behind |
| `review-findings-are-repaired-and-then-promoted` | Findings returned to the same developer and re-reviewed by a fresh invocation |
| `review-findings-spend-the-repair-budget-and-block` | The same budget reached through the reviewer |
| `protected-path-refusal-is-repaired-before-any-check-runs` | The gate deciding before the checks, on the shared budget |
| `protected-path-refusal-spends-the-repair-budget-and-blocks` | `path_refusal` while it is outstanding — the refused paths in sorted order — and the same budget reached through the gate |
| `protected-path-grant-admits-the-change-it-names` | A grant in the item's own text admitting exactly that path |
| `usage-limit-pause-exits-resumable-and-a-later-invocation-finishes-it` | The pause, the durable deadline as the instant the provider named, and the resume continuing the same run and session |
| `server-overload-pause-reissues-the-same-attempt` | An overload told apart from an exhausted account |
| `provider-stopped-on-time-is-resumable-and-continues-the-same-attempt` | A stop the harness made, owed the rest of an attempt rather than a wait, and never blamed on the developer |
| `operator-stop-cancels-the-run-and-preserves-its-work` | The `cancelled` terminal, taken at a provider-call boundary before a verdict is bought |
| `verdict-fields-the-schema-does-not-name-are-recorded-as-drift` | The `review.drift` event, and a verdict acted on without what the schema does not define |
| `partial-cleanup-leaves-a-succeeded-run-reporting-what-survives` | `cleanup_failure` on a succeeded run, with each artifact reported separately |
| `outstanding-publication-leaves-a-succeeded-run-naming-it` | `publish_failure` on a succeeded run whose merge the forge could not confirm, and `stop_class` `publish` |
| `completion-record-that-arrives-late-says-so` | `completion_recording_failure` on a succeeded run whose terminal record the store refused twice, carried by the late write that landed, and `stop_class` `recording` |
| `environment-refusing-the-probe-ends-the-run-as-outside-the-work` | A developer that could not start its probe ending the run on its first reply, as a round refused from outside the work and `stop_class` `sandbox-spawn-failure` |
| `transient-provider-death-relaunches-without-charging-the-developer` | The relaunch budget spending no repair attempt |
| `transient-deaths-spend-the-relaunch-budget-and-block` | The relaunch budget's bound, for a death whose class the harness does not recognize |
| `recoverable-death-carries-on-past-the-relaunch-budget` | A dropped connection waited out past the spent relaunch budget, with each wait recorded on the run |
| `unresolved-directive-pauses-the-work-before-anything-is-claimed` | A pause with no run behind it |
| `unfinished-dependency-pauses-the-work-before-anything-is-claimed` | The same, and that a parent-child link is not a blocker |
| `operator-hold-starts-nothing-at-all` | The hold read before the provider is asked anything |
| `operator-hold-parks-a-claimed-run-and-accounts-for-what-it-cost` | The same hold read at a provider-call boundary of a run already claimed, and `operator_held_seconds` |
| `intake-hold-starts-nothing-the-harness-chose` | The narrower hold, on the choosing rather than the work |
| `promotion-is-replayed-when-the-target-branch-moves` | The replay re-earning the whole gate |
| `a-replay-that-stops-on-the-change-spends-the-integration-budget` | The integration budget, spent by a replay that stops on the change and never by the race |
| `reconciliation-completes-a-run-interrupted-inside-integration` | Settlement from the repository rather than the record |
| `reconciliation-blocks-a-run-interrupted-while-developing` | Settlement as a blocker, and a second sweep finding nothing |

## What this baseline does not yet cover

Named so that a parity harness measuring against it knows what it is not
measuring. Everything this document states above is behavior of the current
pipeline; what is listed here is behavior the document states and **no trace
holds**, so a parity harness has to measure it some other way or accept that it
is unmeasured. Most of these are asserted somewhere in
`internal/orchestrator`; what none of them has is a recorded trace.

**Whole paths.**

- Most of the publishing half — queued merges, the remote target,
  `publish_skipped` (which only a run that asked to publish and could not ever
  carries, so the scenarios here leave it empty), and the catch-up after a forge
  merge. One trace publishes, and only as far as a merge the forge could not
  confirm: it holds the pull request and the outstanding publication, and none
  of the rest. The rest has its own assertions in
  `internal/orchestrator/publish_test.go`.
- **Four of the ten `retries` boundaries.** The boundaries are the ten
  `runstate.Retry*` constants, counted as constants rather than as call sites —
  `reading the remote target branch` is one boundary with three call sites and
  one window, and `writing to the tracker` is one with three, and each is counted
  once.

  One has a recorded trace: a provider death waited out past its budget. Five
  more are asserted in `internal/orchestrator/recovery_test.go` and held by no
  trace here — the branch push (including that the retry does not read as an
  empty change), opening the pull request, the merge (twice over: the reset that
  is waited out, and the window that runs out and leaves an outstanding
  publication), reading the remote target, which a retried merge re-reads before
  each attempt, and the tracker writes, whose test is the one that holds a
  promoted change closing its item rather than being recorded as a failed run.

  The remaining four have neither a trace nor an assertion: republishing a
  replayed run branch, confirming the merge with the forge, deleting the merged
  remote branch, and catching the local target up. Two are worth naming rather
  than counting, because a retry there is not simply a second read: republishing
  and deleting the remote branch are both compare-and-swap writes, so a reset
  that arrived *after* the write reached the forge leaves the retry refused by
  the swap rather than succeeding. Such a run stops exactly as it would have
  stopped without the retry, and the failure it names is the refused swap rather
  than the reset that caused it.
- `yoyo triage repair` and `yoyo triage rerun`, which re-enter a stopped run
  through their own preconditions.
- **A change that discharges no item.** Every recorded scenario here closes or
  leaves unclosed for some other reason, so the `landing_outcome`,
  `landing_reason`, `landing_blocked_by`, `landing_impediment_problem` and
  `landing_problem` fields are empty in all of them, every trace that holds
  `review_approves` holds it as `implementation`, and no trace holds
  `Tracker.Reopen` or the `Tracker.AddBlocker` that precedes it. Both readers
  that can withhold the closure — the developer's landing claim and an approval
  the reviewer gave to evidence rather than to the work — both directions of the
  closure, the run's own and reconciliation's, and both places an undischarged
  item is left are asserted in `internal/orchestrator/landing_test.go`.
- **Seven of the sixteen pre-claim steps, and the order of all sixteen.** The
  refusals in steps 1, 3, 4, 8, 10, 11, 13, and 14 are enumerated where they are
  asked, with the untraced ones named there rather than only here, because a
  reader deciding what to measure reads the sequence rather than this list. The
  ordering itself is a guarantee no single trace can hold.
- Five of the seven reconciliation actions. `completed` and `blocked` have
  traces, and a second sweep finding nothing is recorded beside them; `held`,
  `resumable`, `queued`, `failed`, and `unsettled` do not.
- The escalation in `escalate.go`, which delivers a run stopped on a 2-of-2
  review verdict, or an item a role raised as unmeetable, into the development
  manager's conversation. It happens after a run has stopped rather than inside
  one, so no trace here reaches it, and a parity harness measuring what becomes
  of a stopped run has to measure it separately.
- **A run either role escalated.** A developer or a reviewer saying the work item
  cannot be met as it stands ends the run in the round it was raised in, with
  nothing integrated and the item parked. The delivery definition has no outcome
  for it, so such a run records a workflow divergence naming the state its
  instance stopped in — which is the same thing every ending the definition
  cannot express records, and is why no trace here holds one. It is asserted end
  to end in `internal/orchestrator/escalation_test.go` instead.

**Outcomes and fields.**

- The `timed_out` terminal, reached by a check stopped at
  `execution.check_timeout`, by a stage stopped at
  `execution.check_stage_timeout`, or by a context deadline. A check that timed out is
  also the branch of the repair loop that ends a run rather than spending an
  attempt, so both are untraced together. It is left untraced deliberately: what
  such a run records includes how long the check ran, which is the machine rather
  than the behavior.
- `workflow_unobserved`, which is why a run that was to be observed has no
  instance. No trace here holds it and none ever can: a trace whose run was
  eligible for observation and carries it is refused rather than recorded, which
  is the point of the field. `internal/orchestrator/declarative_test.go` asserts
  both halves — the run recording it and delivering anyway, and the recorder
  refusing the trace.
- The budgets a round refused from outside the work hands back. The
  classification itself is traced, on a first round that had nothing to hand back.
- `usage_limit_paused_seconds` reaching `execution.usage_limit_max_pause`, and
  the blocker a usage limit with an unusable reset time produces.
- `usage_limit_paused_since` and `usage_limit_reset_unknown`, which are on the
  record only while a run is parked: every trace here records the run after
  its pause was cleared, so neither is held by one. That both are written
  before the wait begins is asserted in `internal/orchestrator/pipeline_test.go`,
  and what reads them is `internal/readmodel`, which is not this pipeline.
- The directive and dependency pauses of a run that is **already claimed**. The
  pause table above says the directive is read before the claim, before a
  resume, and at every round of the gate including the promotion; only the
  pre-claim reading has a trace, and both of those traces record no run at all.
  The operator hold is traced at both boundaries and is the one of the three
  that is not a gap here; a parity harness measuring the other two mid-gate has
  to reach them another way.

**Guarantees stated above with no trace behind them.** These are the ones the
mechanical check cannot see: it recognizes durable field names, and every
guarantee here is stated in prose, about an ordering, a sharing, or a boundary
that no field records. This list is the only thing a parity harness has for
them.

- The independence check that refuses to integrate on a reused or missing
  provider session.
- **The integrate door refusing a change the record does not show passed its
  gate.** Every recorded scenario that reaches the promotion earned it — the
  repair loop re-earns the whole gate before every promotion — so no trace
  holds `candidate.integrate` reading `checks_passed` off the record and
  refusing: a standing `check_failure` or `path_refusal`, no passing checks at
  all, passing checks for another attempt, another commit, or other content
  than the worktree holds, or no approving verdict. Each refusal, and that it
  leaves the run in `reviewing` with no lease taken and no phase written, is
  asserted through the registered door in `internal/orchestrator/actions_test.go`
  (`TestPerformingIntegrateRefusesAChangeTheRecordDoesNotShowPassedItsGate`)
  and held by no trace here.
- A replay that conflicts, which is handed back to the change's own developer
  as a repair attempt on top of the target (yoyodyne-ifd.132), and blocks with
  both sides intact only once the repair budget is spent.
- A reviewer's reply that cannot be read as a verdict, which is asked for once
  more and fails the run on the second.
- **`integrating` rewriting `harness_commit`.** Every trace that carries the
  field carries the value it ended on, and on a promotion that is the commit
  promoted. The replay trace ends on the replayed commit, but a trace records
  only where a run finished, so the commit the replay replaced never appears in
  it and the rewrite is indistinguishable from a replay that left the field
  alone. A refused promotion naming the commit it made is held by no trace at
  all.
- **A recorded integration the repository contradicts, blocked rather than
  believed.** The traced settlement is the opposite case — an integration the
  record does not carry, found in the target branch by containment — so what no
  trace holds is the sweep disbelieving a record that claims more than the
  repository shows.
- **The developer and the reviewer sharing `transient_relaunches`.** Every
  relaunch trace kills only the developer, so the budget is frozen as one the
  developer draws on. That a reviewer's death draws on the same budget — and on
  the same recovery window past it — is asserted in
  `internal/orchestrator/recovery_test.go` and held by no trace here.
- **Every counter being recorded before the thing it bounds.** No scenario dies
  mid-attempt and resumes, so the guarantee that an interrupted attempt still
  counts — and that a restart therefore cannot buy a fresh budget — is untraced
  for all five counters. The reconciliation traces interrupt a run, but between
  steps rather than inside a recorded attempt.
- **The three repair inputs drawing on one budget.** A refused path, a failing
  check, and a reviewer's findings each have a trace spending the budget alone;
  none mixes two of them in one run, so what is frozen is three budgets behaving
  identically rather than one budget serving three failures.
- **A protected-path refusal clearing a recorded check failure and any
  findings**, and a passing gate clearing the other two. The traced refusal is
  the first thing that happens to its change, so there is nothing recorded for
  it to clear.
- **`Tracker.Block` running on its own deadline rather than the run's context.**
  What that exists for is blocking an item whose run was cancelled or timed out,
  and no trace drives a block under a cancelled context.
- **The reconciler taking each run's lease and re-reading state under it.** The
  settlement traces show what a sweep decided, not that it re-read under the
  lease rather than deciding from the listing it started with.
- **An operator hold outlasting `execution.usage_limit_max_pause`.** The traced
  hold is lifted after one probe; that hold time is bounded by nothing, unlike
  every other wait, is stated and untraced.
- **The work item's durable review counter** — the one `triage.review_rounds_cap`
  measures and a repair grant is truncated against, which takes only the verdicts
  that sent something back. The traces carry the run's `review_rounds` and never
  the item's, so an executor could get the item-level counter wrong without
  moving anything recorded here.

**What the traces are of.**

- Every trace here records the pipeline's Go control flow. `actions.go`
  registers the same steps under selectable names — `work-item.claim`,
  `candidate.develop`, `candidate.publish`, `candidate.check`,
  `candidate.review`, `candidate.integrate`, `run.complete`, `run.clean-up` —
  and each one is a single call to the function the pipeline itself calls, but
  nothing in the delivery path walks through that door: `Pipeline.Run` still
  runs the sequence Go control flow puts it in, and these traces are of that
  sequence. That stays true now that the declarative path is the default
  (`execution.declarative_delivery`, `true` unless a project rolls back): a run
  records a workflow instance and steps it at each boundary, and the doors that
  instance is stepped through perform nothing, so the delivery is the same
  delivery and the instance is an observation beside it.

  Every scenario here is driven on that default, which is what makes these
  traces the baseline of what a run actually does rather than of a path nothing
  takes any more. Each one that reserves a run therefore carries the
  `workflow_instance_id` its observation was recorded under, and the delivery
  they record is otherwise unmoved by the flip — the whole of what changed when
  the default moved is that one line per trace.

  That is enforced rather than asserted: a scenario driven on the default whose
  run reserved no instance is refused, on a re-record and on an ordinary
  comparison alike, so a run nothing observed is never what a path is frozen as.
  It is enforced because it was broken.
  `recoverable-death-carries-on-past-the-relaunch-budget` was once recorded from
  a run whose observation never started, which differs from the right trace by
  one absent field and passes every other check here; the frozen document then
  agreed with three consecutive full checks and began failing when the
  observation started working again — a document failing for a reason unrelated
  to the change that had to fix it, and, until then, a run counted as one the
  definition agreed with. A run whose project rolled back and a path that
  reserves no run at all are unaffected: carrying no instance is what both of
  them are.

  One trace carries a
  `workflow_divergence` as well:
  `reconciliation-completes-a-run-interrupted-inside-integration`, where a
  process killed inside integration is settled as succeeded with its instance
  still standing in `integrate`, and the gap that leaves is recorded rather than
  smoothed over. The other interrupted scenario,
  `reconciliation-blocks-a-run-interrupted-while-developing`, carries none, and
  that is the observation having finished rather than the blocked settlement
  skipping it: the process survives the refused write, records its own ending,
  and the developer's ending is one the definition has an outcome for, so the
  instance is on the `abandoned` terminal before the sweep looks at it.
  `TestASweepRecordsTheGapAnInterruptedObservationLeaves` and
  `TestASweepRecordsNoDivergenceWhereTheObservationReachedATerminal` hold both
  halves of that, the first of them over a blocked settlement as well as a
  completed one, and `TestABlockedSettlementRecordsTheGapItsInstanceLeaves`
  holds both halves at the blocked settlement itself — at `blockRun`, which
  every route into it shares, so a gap recorded on one route and missed on
  another cannot pass. Where each instance *went* is not read off these traces:
  `internal/orchestrator/declarative_test.go` drives the eight paths the parity
  harness holds a transcript for and compares the instance's own sequence
  against it.

  What now reads them is the parity harness in
  `internal/orchestrator/parity_test.go`, which walks the built-in workflow
  definitions — `delivery.yaml` and `delivery-human-approval.yaml`, the same
  loop under its two integration policies — against every trace here. Each path
  is written down there as the states a run stood in and the outcome it produced
  in each, held against the trace that evidences it (a developer invocation is a
  `develop` state, a verdict is a `review` state, and the counters below decide
  how many of each), and then stepped through the compiled definition by
  `internal/workflow`'s own executor with the registered actions' doors
  performing nothing. A recorded path no scenario walks fails that harness by
  name.

  One thing there is not read off a trace. Only one recorded path runs under a
  person's approval — the change that passes — so the three ways that gate can
  stop a run instead (a check that failed, a path the item never granted, and a
  suite the machine could not run) would otherwise be transitions nothing
  measured. The harness drives the real non-automatic pipeline into each of them
  and holds the definition's destination against what the run left behind: one
  developer invocation, no repair attempt, the item neither closed nor blocked,
  and the worktree and branch still standing. Driven rather than recorded,
  because adding a trace here is re-recording this document's own artifact.

  What it measures is the **sequence** and not the record. The pipeline does
  everything outside its eight registered steps in Go control flow behind no
  door at all — the pre-claim questions, the pauses, and the budgets — so there
  is nothing for an executor to perform that would produce a durable record to
  compare field by field. The four paths that stop before anything is claimed
  reach no state at all and are named as inexpressible there rather than walked;
  the two reconciliation paths are walked as far as the step the process died
  inside, because the settlement after it is not an action anything registers.

  Recording the outcome on the work item, settling it once its promotion is
  settled and pricing what the run spent is the `complete` state, between the
  promotion and the cleanup on the automatic path and the last state on the
  human-approval one. Because the parity walk performs nothing, what that step
  does to the item is held separately, in `actions_test.go`: the door is
  performed against a tracker and a ledger and the item has to end up closed and
  priced exactly as the hard-coded loop leaves it. The `complete` and `delivered`
  summaries in `delivery.yaml` say both outcomes — closed where the run's landing
  discharges the item, back in the backlog where it lands evidence instead — and
  the copy under `.yoyodyne/workflows/` says the same words, because a summary is
  digest-bearing and the two are held to one digest. The human-approval
  definition's `complete` state closes nothing, so its summary says nothing about
  closure and did not have to move.

  One state is missing from the definitions and is worth knowing about before
  reading one as the pipeline. There is no `publish` state, because
  `candidate.develop` ends by calling `publishAttempt` — a definition selecting
  `candidate.publish` beside it would publish every attempt twice. There is no
  `commit` state either, and for a stronger reason: the commit is not something a
  sequence may order or omit. `candidate.develop` makes one of whatever each
  invocation left in the worktree, before it decides what became of the
  invocation, so a round whose change the branch tip does not carry cannot be
  arranged.

**Not observable in these scenarios.**

- The spend log, the price on the item, collected reports, proposed amendments,
  and the triage docket, none of which are wired in the recorded scenarios: a
  pipeline without them runs identically, so the traces say nothing about what
  they receive.
- A real provider adapter's event stream. The fake provider emits none, so the
  recorded event sequences are the harness's half of the log rather than the
  whole of it.
