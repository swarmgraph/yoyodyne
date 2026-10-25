---
id: one-owner-for-each-guarantee
kind: design
title: 'Guarantees the whole system relies on: one owning component and one way in for each, checked in make test, moved onto one path at a time'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-25T16:01:05.958007Z
      reason: 'yoyodyne-ifd.428.127: the operator''s direction of October 25 that the guarantees the whole system relies on become one coherent system. Names eight guarantees, each with one owning component and one way in; the check in make test for each, with a list of bypasses that may only shrink; fourteen migration items in the outside review''s order; how forward progress fits; how each of the five items waiting on design is built; and the invariant this warrants. Written from reads at main 8a4317dd.'
    - action: amended
      by: architect
      at: 2026-10-25T17:58:45.787484Z
      reason: 'yoyodyne-ifd.428.127: adds checks as guarantee 9 at the operator''s direction of October 25 (owner, way in, check in make test, and the five things it takes in) and places its seven migration items second, after storage and admission; heavy check permits move into the same reservation as developer slots. Records the ruling on the race check policy (yoyodyne-ifd.428.126.6.1.1) as this repository''s application of guarantee 9, including the macOS race coverage and where it runs. Existing migration numbers are kept so items citing them stay right. Written from reads at main 0ef00fa8.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-25T16:01:05.958007Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1714, for document-1714.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-25T17:58:45.787484Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1728, for document-1728.1 under the automatic approval policy
---
# Guarantees the whole system relies on: one owning component and one way in for each

## What this is for

Serves the autonomy goal. On October 25 the operator asked for the guarantees the whole system relies on to become one coherent system (yoyodyne-ifd.428.126). The same day he added checks to them, and asked that checks move second, right after storage and admission, because they are the largest lever on throughput and machine load.

The product leadership review of October 14 and the outside code review of October 25 found the same thing: a guarantee meant to hold everywhere is implemented separately in each execution path, and the copies now disagree. The review traced, among others:

- four paths that load a run record, do other work, and save the whole record back, so a field another writer changed in between is lost (yoyodyne-ifd.428.116);
- fresh dispatch reserving a developer slot under a lock, while recovery paths read the free slots and mark a run live later, so two starts can take one slot (yoyodyne-ifd.428.117);
- the log, run and health tools ending a conversation when their limit is spent, while the repository read, research and recovery tools let it take another paid turn (yoyodyne-ifd.428.118);
- checks decided across four designs and about ten files in `internal/orchestrator`, each with its own rule for what runs, how long it may take and when a result is kept.

Each copy was right where it was written and wrong as a whole, because nothing said which code owns the guarantee. This design names, for each guarantee:

- the one component that owns it and the one way in;
- the check run by `make test` that refuses code going around it;
- the order the execution paths move onto it;
- for each item waiting on design, how it is built.

## What it rests on, and what was not read

Read whole at commit 8a4317dd:

- [fewer lifecycle states](fewer-lifecycle-states.md);
- [follow-ups](follow-ups.md);
- [forward progress](forward-progress.md);
- [how internal/orchestrator is split](orchestrator-boundaries.md);
- the items for this design and its parent, and for the run record (yoyodyne-ifd.428.116), the reservation (yoyodyne-ifd.428.117), the message budget (yoyodyne-ifd.428.118), bounded reads (yoyodyne-ifd.428.119) and the priced ending (yoyodyne-ifd.428.121);
- the names of the files in `internal`, `internal/runstate`, `internal/workflow`, `internal/developerslot` and `internal/execution`.

Read whole at commit 0ef00fa8, for guarantee 9:

- [checks on CI](checks-on-ci.md);
- [choosing checks by change](choosing-checks-by-change.md);
- [repeated checks](repeated-checks.md);
- [capacity for heavy checks](heavy-check-capacity.md);
- the item for the race check policy (yoyodyne-ifd.428.126.6.1.1), with its measurements of October 20 to 25.

Not read:

- the code of any of them, including `internal/runstate/store.go` and `internal/checks`;
- the files in `internal/orchestrator` that run checks today;
- the audit tests already in `internal/runstate`, such as `runrecordwriters_audit_test.go` and `strictdecode_audit_test.go`;
- `internal/developerslot`, the conversation loop and its tools, `internal/cost`, `internal/beads`, and the provider adapters.

Where this design says how the code behaves today, it takes that from the items, which cite the outside review at revision ddef0b1e. Each build item reads the code it changes first. Where the code holds a copy of a guarantee this design does not name, the item stops and the architect is asked.

## The rule

A guarantee here is a rule that has to hold whichever path is running: a fresh run, a continuation, the carrying out of a decision, a reconciliation, a conversation, a surface. For each one:

1. One component owns it, and the rule is written once, there.
2. Code outside the component reaches it only through its way in. Where a type can make going around it impossible, the way in is a type. Otherwise it is a function, and a check in `make test` refuses calls that go around it.
3. A change that needs the guarantee to behave differently changes the component, never a copy beside it.

No new package collects the guarantees. Each lives with the records it protects, in the packages [how internal/orchestrator is split](orchestrator-boundaries.md) already names, so a guarantee is never kept apart from its data.

The guarantees are numbered in the order they were written down. The numbers name them and do not set the order of migration, which is its own section below.

## The guarantees

### 1. A run record has one writer at a time, and no change is lost

Owner: the run store in `internal/runstate`.

Way in: narrow updates. Each changes only its own fields, under the run's write lock, against the record as stored at that moment. Examples: record integration evidence, record cleanup progress, mark a pull request superseded, record a merge queue retirement, record a transfer, record a stop, record a decision. The migration finds the rest.

A save of the whole record stays only for the holder of the run's lease, and carries the version it loaded. The store refuses a save whose version is no longer current, naming the run and the fields that differ. A refused save returns an error the caller must handle; discarding it is a defect the check below finds.

Decided: both, as the item asked. A version check alone would turn every lost update into a refusal and leave each caller to retry by hand, which is the copied logic this design ends. Narrow updates alone would leave the whole save open to the next path that reaches for it. Narrow updates make the common case safe, and the version check catches what is left.

The same rule covers every other store in the harness's state directory that replaces whole records, each moved by its own migration item: the merge queue records, the list of stopped runs waiting on the development manager, and triage decisions. The follow-up store is built to this rule from its first piece.

### 2. A run moves between states only through one function

Owner: `internal/runstate`, in the functions that read meaning from the record.

Way in: one function. It takes the run's saved status and step and the change asked for, refuses a change that is not allowed, and writes an allowed one as a narrow update under guarantee 1. The one function that chooses where a stopped run resumes, which slice 5 of [stopped run recovery](stopped-run-recovery.md) builds (yoyodyne-ifd.428.99.6), is part of it. The states it moves between are the ones [fewer lifecycle states](fewer-lifecycle-states.md) leaves; it adds none.

The workflow graph binds to this function. Until the graph runs the pipeline, its position is worked out from the run record and never kept beside it as a second answer. The ruling on whether the graph runs the pipeline (yoyodyne-ifd.428.123) is recorded in [configurable workflows](configurable-workflows.md) against this.

### 3. Every start, and every heavy check, takes capacity through one reservation

Owner: `internal/runstate`, where counting what is free moves under [how internal/orchestrator is split](orchestrator-boundaries.md), beside `Store.ReclaimSlot` and `CapacityError`.

Way in: one reservation, taken under the shared reservation lock, that checks a place is free and records it as taken by a named run in the same act.

- Making a run live takes the reservation as an argument, so a start without one does not compile.
- A start refused after reserving gives the reservation back in the same act.
- Fresh dispatch, every continuation, the carrying out of every decision, integration resumption and the limited reclaim all go through it. None of them reads the free slots and acts later.

`internal/developerslot` keeps describing the slots and their tags. If it already counts what is free, the first build item moves that count into the reservation and says so.

This changes operation 4 of the shared continuation operations in [how internal/orchestrator is split](orchestrator-boundaries.md): it reserves the slot rather than reading it. A slot that is not free is still a wait, not a refusal.

Permits for heavy checks are taken through the same reservation, under the same lock, as developer slots (migration item 6). The two stay separate counts with separate limits, as [capacity for heavy checks](heavy-check-capacity.md) says, and everything else in that design is unchanged: its four states of a permit, its release only once the process group is empty, its order of granting, and its recovery after a restart. A run waiting for a permit keeps its developer slot, so the two are never asked for in the same act. Until item 6 lands, permits keep their own lock.

### 4. Every further paid call or recovery act is decided by one budget

Owner: a new package, `internal/budget`, created by the first item that uses it.

Way in: one decision. It takes what has been spent and the limits that apply, and says whether one more may be taken, with the reason in ordinary words when it may not. It keeps no store of its own: what has been spent stays on the records that hold it today.

It decides three kinds of thing:

- **A paid conversation turn.**
  - Each message has one budget: provider turns and elapsed time, and spend where the provider reports it.
  - After any tool's limit is spent, the message ends with one closing turn and no more. That is what the log, run and health tools already do. The repository read, research and recovery tools, and every tool added later, go through the same decision.
  - The limits are numeric limits the development manager sets under section 6 of [portable agent configuration](portable-agent-configuration.md).
  - The conversation loop takes a provider turn only with a permission the decision returns, so a turn taken without asking does not compile.
- **A repair, a rerun, a merge armed again, and the harness's own continues.**
  - The item's recovery budget stays on the run record.
  - The decision whether one more may be spent moves here, with a grant and an attempt counted in one unit (yoyodyne-ifd.428.86).
  - The rule in [forward progress](forward-progress.md) that the same act is not taken a third time on the same failure is one of its limits, and so is the limit on the harness's own continues each hour.
- **A retry in place at a boundary.** Each boundary's window stays as [recoverable and terminal failures](recoverable-and-terminal-failures.md) sets it. The decision to retry inside it moves here, one boundary at a time, last.

A rerun of a check is not a recovery act and is not decided here. When a check is run again is guarantee 9's.

### 5. A step counts as done only on its recorded evidence, and cost is known by one rule

Owner: for steps, the narrow update that records the step, under guarantee 1. For cost, `internal/cost`.

Way in:

- A step is done when its narrow update has been written and read back. Integration still needs evidence bound to the reviewed revision, as the invariant on that says. This guarantee adds only that the evidence is written by one update, not assembled by each path.
- A check result is evidence of this kind. What a result proves, and the record it is written into, is guarantee 9's; this guarantee writes it, and nothing else assembles it.
- Each provider adapter returns the best result it has together with any error, so a priced ending is kept when later output is malformed. This is a change to the adapter contract in [provider adapters and endpoints](provider-adapters-and-endpoints.md).
- `internal/cost` alone parses cost events and decides whether a total is complete. Stream spend reporting and the pricing of each item both call it. A total missing evidence is marked incomplete and says what is missing. No event is skipped silently.

### 6. Every wait, every hold and the brake are follow-ups

Owner: the follow-up store, as [follow-ups](follow-ups.md) and [fewer lifecycle states](fewer-lifecycle-states.md) already rule.

Way in: the act that leaves something waiting writes its follow-up in the same act.

- Holds and the brake become follow-ups in the order of folding that fewer lifecycle states sets. The brake keeps only its count of stops and the spacing of trial runs.
- Re-checking a hold whose cause a command can confirm (yoyodyne-ifd.432.25.19) is piece 2 of follow-ups.
- The one registry of waiting entries (yoyodyne-ifd.432.25) is how each is shown.

Nothing here changes those designs. Their build pieces are the migration for this guarantee and are not repeated below.

### 7. The tracker is reached through one client

Owner: `internal/beads`.

Way in: the client, which is the only code that runs the tracker's command. It:

- carries each call's time limit;
- records how long a call that ran past its limit took and what held the store (yoyodyne-ifd.433.49);
- shares a listing between its users within one cycle.

The sharing built for the scheduler (yoyodyne-ifd.433.45, in `internal/orchestrator/cyclelistings.go`) moves into the client. The status reading behind the dashboard, `yoyo status` and Slack (yoyodyne-ifd.432.46) uses it once both have landed. The run already in progress for that item is not stopped or rewritten.

### 8. Routine reads never walk all kept history

Owner: for run records, the run store. For event streams, `internal/cost`.

Way in:

- **A durable index of run records:** active runs, outstanding work, and the runs of each item.
  - The run store writes it in the same act as every narrow update under guarantee 1, and rebuilds it from the records when it is missing or older than the store.
  - The scheduler, the reservation, the read model and the health observation read the index, never every record.
  - The reservation lock never covers reading a record outside the index's answer, so an unreadable old record cannot hold it. An unreadable record is reported and skipped, as yoyodyne-6d1 builds.
- **Spend for completed event streams is added up once,** when a stream completes, and kept. The spend query reads only what its date window needs.
- **A dashboard reading builds one index of records,** and every section reads it.

Decided: this index does not also serve the tracker side (yoyodyne-ifd.432.46). The two read different records written by different writers, and one index over both would need two writers, which [fewer lifecycle states](fewer-lifecycle-states.md) refuses. They follow the same rule: each is kept up to date by the one writer of what it indexes, and neither is rebuilt on every read.

All history is kept. Where ownership of a run is uncertain, the harness stays conservative, as [fresh factory health](fresh-factory-health-and-preserved-run-recovery.md) says.

### 9. What a change's checks are, and what their results prove, is decided in one place

Owner: `internal/checks`.

Way in: two things, and nothing else launches a configured check or decides about one.

- **One plan.** For a run at a stage, the plan says which declared checks run, whether each runs whole, narrowed or not at all, and where: on this machine, or on the forge as [checks on CI](checks-on-ci.md) says. Each choice carries its reason. The selection policy of [choosing checks by change](choosing-checks-by-change.md) is how the plan narrows, and its list of when the whole suite runs still holds: when in doubt, everything runs.
- **One runner.** The runner launches only what a plan names, in its own process group, with a permit taken under guarantee 3 where the check is heavy. It applies the time limit, starting at launch and scaled for machine load as the capacity design says. The stage limit is a numeric limit the development manager sets under section 6 of [portable agent configuration](portable-agent-configuration.md). The limit returns to 60 minutes once the narrowed race check below has landed (yoyodyne-g9g).

What a result proves:

- A result is evidence only as the checks component records it, through a narrow update under guarantee 5. It names the commit, the check's definition and the digest of what declares it, the toolchain, the platform and the relevant settings, which are the five things [repeated checks](repeated-checks.md) requires for two runs to be the same run. A result read from the forge is recorded the same way, for the exact commit.
- A revision none of whose configured checks ran is not integrated. A check that could not run counts only beside at least one that ran and passed. This takes in the rule from yoyodyne-44j and the ruling on checks that could not run (yoyodyne-1m3).
- A check whose finish record failed to save is reported, and counts as not finished until its record is written. It is never dropped (yoyodyne-ifd.429.77).

When a result is kept, and when a check runs again:

- A result is kept for a later commit only where the commits are the same run under repeated checks. It is also kept where the project's analyser says, with complete evidence, that nothing the check covers changed between the commit that passed and the later one. When the target branch has moved, the comparison is made on the change combined with the new target branch. Any doubt runs the check.
- A failure confined to tests the change does not touch is run once more on the same commit, before any repair attempt is spent, with both results kept (yoyodyne-ifd.429.87).
- A check stopped by its time limit is unfinished, not failed, and is run again at most twice within three times its limit, each run with its own permit, as the capacity design says.
- Every other rerun is the plan's to decide, never a path's.

The measured limits for heavy checks (yoyodyne-ifd.429.48) apply on every execution path because every path launches checks through the runner. They are set by the development manager under section 6.

Keeping a result across commits by the analyser's answer is new. It rolls out through the same shadow period and switch as narrowing does under choosing checks by change: the result is computed and the check runs anyway until 50 shadow runs show no miss.

#### How this repository applies it: the race check

This is the ruling on the race check policy (yoyodyne-ifd.428.126.6.1.1). It is this repository's declaration under the rules above. It adds nothing about Go to the harness.

Measured October 20 to 25, the single `make race` permit was busy 72% of 130 hours, 80 of 249 race checks waited more than 10 minutes, and each item ran the full race check about 4.2 times on this machine. Each of the four points below is decided.

1. **Before review: accepted.** `make race` runs only on the packages the change touches and the packages that depend on them, from the module's import graph, through the Go analyser this repository declares. It runs only when Go code changed. Go code changed means any of these:
   - a `.go` file;
   - `go.mod` or `go.sum`;
   - a file built only for some platforms or build tags;
   - any file a test reads by embedding it or as test data.
   A path the analyser cannot place runs the whole race check. Formatting and vet still run on every change.
2. **After a repair, or when main moves: accepted.** The race check runs again only where the change's own Go code, or code it depends on, changed since the result that passed. When main has moved, this is worked out on the change combined with the new main. Otherwise the earlier result is kept, with the commit it passed on and the analyser's answer recorded beside it.
3. **The merge candidate: accepted, on two conditions.** The local full race on the merge candidate does not run when both of these hold:
   - the forge's required check runs the full suite under the race detector;
   - it runs on the change combined with current main, through the forge's merge queue or because the forge requires a branch to be current before it merges.
   The project declares both in its configuration. The harness confirms them from the forge at each landing where it can read the forge's settings, and records whether it confirmed them or could not read them. Where the forge shows either condition false, the local full race on the merge candidate runs as it does today.
   This differs from the answer I gave in exchange 9bb52ecf, which only recorded a failed condition. Running locally is what when in doubt, everything runs requires.
4. **A full race run on main: accepted.** The harness runs the whole `make race` on main every four hours, only when main has moved since the last clean run. It runs as one of the harness's recurring tasks, whose schedule the development manager sets under section 6, and takes a permit like any other check. A failure is not run again to make it pass. The harness files a critical report naming the failing tests, the commit, the last clean commit and the commits between them. That is admitted at priority 0 with the reliability label, as the operating rules require for anything that blocks reliable execution, so work enters through admission like any other.

**Race coverage on macOS is required.** This repository has code built only on macOS: the keychain, launchd, file system and process handling. The tests only macOS exercises keep running on this machine on every change, in the short local check that [checks on CI](checks-on-ci.md) keeps. The race detector over that code runs in point 4's run on main, on this Mac. When the macOS CI job (yoyodyne-ifd.428.122) runs the race detector over that code on each pull request, it takes over that coverage, and point 4 may move to the forge.

**What is measured afterwards.** Each week, the race permit's busy share and the number of race checks that waited more than 10 minutes are recorded (yoyodyne-ifd.428.126.6.2). The target is about one narrowed race run per item on this machine. Whether a second race permit or a fourth developer slot then raises throughput is the development manager's to decide from those figures, under section 6.

## How forward progress fits

[Forward progress](forward-progress.md) adds no component. It reads and uses these guarantees:

- each item's state is worked out from the run store through the index (guarantees 1, 2 and 8) and from the follow-up store (guarantee 6);
- the reason ready work was not started comes from the reservation's answer (guarantee 3);
- the harness's own acts while the development manager is unavailable go through the recovery tool. That tool takes capacity through the reservation (guarantee 3), writes through narrow updates (guarantee 1), and asks the budget before each act (guarantee 4).

## Checks run by make test

Each check goes in with its component's first item.

Where today's code already goes around a component, the check carries a list of the places that still do. The list may only shrink: the check fails if an entry is added, and each migration item removes the entries it moves. When the list is empty, it is deleted.

Where `internal/runstate` already has an audit test of the same kind, the check extends it rather than adding a second.

| Guarantee | The check |
| --- | --- |
| 1. Run record | No package outside `internal/runstate` saves a whole run record except through the versioned save of the lease holder, and no caller discards the error a save returns. |
| 2. State changes | No code outside `internal/runstate` assigns a run's saved status or step. |
| 3. Capacity | Making a run live requires a reservation, by its type, and so does launching a heavy check once item 6 lands. A test races two starts, one fresh and one recovery, for the last slot, and exactly one gets it. |
| 4. Budget | A provider turn in a conversation requires the decision's permission, by its type. A test drives a model that never stops asking after every limit and shows the message ends within its budget, with the stop recorded in ordinary words. |
| 5. Evidence and cost | No package outside `internal/cost` decodes a cost event. Each adapter, fed a priced ending followed by malformed output, keeps the cost. |
| 6. Waits and holds | The tests [follow-ups](follow-ups.md) and [fewer lifecycle states](fewer-lifecycle-states.md) already name. |
| 7. Tracker | No package outside `internal/beads` runs the tracker's command. |
| 8. Bounded reads | No routine reader calls the reading of every run record or every event stream. Only rebuilding an index and maintenance may, and the check names them. |
| 9. Checks | No package outside `internal/checks` launches a configured check command, decides which checks run or are narrowed, or decides that a result is kept. A check result counts as evidence only when the checks component recorded it. |

## The order of migration

Each step is an ordinary work item that leaves the factory running and moves one execution path or adds one component. The groups run in this order: storage and admission first, then checks, then one invocation policy, then bounded reads, then tests across component boundaries. Turning these into work items, and their order inside each group, is the development manager's. The Lead Product Manager sets the pull order.

The items keep the numbers they were first given, so work already citing an item stays right. The checks group was added later and is numbered 15 to 21, but it comes second.

**Storage**

1. The run store refuses a stale whole save, and the four paths the review traced move onto narrow updates: merge queue retirement, transfer (which stops discarding the save's error), admission, and reconciliation's cleanup progress. This is yoyodyne-ifd.428.116 as written.
2. Each other whole save outside the run store moves onto narrow updates, one execution path an item, shrinking the check's list. The paths are the pipeline's completion, each continuation path as it moves into `internal/continuation`, the carrying out of decisions, and triage.
3. Run state changes go through one function, with its check. It is built with the function that chooses where a run resumes (yoyodyne-ifd.428.99.6, and yoyodyne-0rm, which asks for the same).
4. The merge queue records, the list of stopped runs and triage decisions each refuse a stale whole save, one store an item.

**Admission**

5. One reservation for every start, and making a run live requires it. This is yoyodyne-ifd.428.117 as written, covering every path in one item, because a path left outside it can still overrun the slots, and that is the defect.
6. Permits for heavy checks are taken through the same reservation, under the same lock, as developer slots, kept as a separate count. This is the capacity part of checks.

**Checks**

15. The runner in `internal/checks` becomes the only way a configured check is launched, with guarantee 9's check and its list of places still going around it. The check stages, with their continuation and recovery, move onto it first.
16. Each other path that runs checks moves onto the runner, one an item, shrinking the list: heavy checks, kept checks, landing checks, path checks, queued checks on the forge, the harness's check of its own build, and the repair of a forge check.
17. One plan decides which checks run, whether each is whole or narrowed, and whether it runs here or on the forge, with the reason for each. The selection policy of choosing checks by change and the switch of checks on CI are both read there. This repository's race check points 1 and 3 are built on it: its declaration of what counts as Go code changing, the narrowed race before review, and the two conditions for the merge candidate with their confirmation and fallback.
18. What a result proves: the result record with the five things that make two runs the same run, written through one narrow update. It includes the rule that a revision none of whose checks ran is not integrated, and the report for a finish record that failed to save (yoyodyne-ifd.429.77).
19. When a result is kept or a check runs again: kept results, the single rerun of failures in untouched tests (the pieces of yoyodyne-ifd.429.87), reruns of a check stopped by its time limit, and keeping a result across commits by the analyser's answer, in shadow first. This repository's race check point 2 is built on it.
20. Time limits in one place: from launch, scaled for load, the stage limit as a numeric limit, and the stage limit back to 60 minutes once item 17's narrowed race check has landed (yoyodyne-g9g). The measured limits for heavy checks then apply on every path (yoyodyne-ifd.429.48).
21. The recurring full run on main, with its critical report on failure: this repository's race check point 4, run as a plan of the same runner.

**One invocation policy**

7. `internal/budget` and the budget for each message, with the conversation loop and every tool on it. This is yoyodyne-ifd.428.118 as written.
8. The recovery budget's decisions move onto `internal/budget`, with a grant and an attempt counted in one unit. This takes in yoyodyne-ifd.428.86.
9. The rule for cost evidence: adapters return the best result they have with any error, and `internal/cost` alone parses cost events and decides completeness. This is yoyodyne-ifd.428.121 as written.
10. Retries in place, one boundary an item, decided by `internal/budget` inside each boundary's window.

**Bounded reads**

11. The index of run records, written with every narrow update and read by the scheduler, the reservation, the read model and the health observation. This is the first half of yoyodyne-ifd.428.119, and it depends on item 1.
12. Spend added up once for each completed stream, the spend query reading only its window, and one index for each dashboard reading. This is the second half of yoyodyne-ifd.428.119. Whether that item is built as one change or two is the development manager's.
13. The tracker client: shared listings move from the scheduler into `internal/beads`, the status reading uses them once yoyodyne-ifd.432.46 has landed, and the check that nothing else runs the tracker's command goes in.

**Tests across component boundaries**

14. A suite drives a completion, an admission, a reconciliation, a recovery start and a reservation race against one run in fixed orders, through the components' ways in only. It shows no field lost, no slot or permit taken twice, no check launched outside the runner, and no budget passed.

Guarantee 6 moves by the build pieces of follow-ups and fewer lifecycle states, in their order.

This order differs from the outside review's in two places. Item 3 goes with the function that chooses where a run resumes rather than first, because slice 5 of stopped run recovery rewrites the same code. Checks come second, at the operator's direction, because they are the largest lever on throughput and machine load. Item 18 writes results through guarantee 5's one update before item 9 settles cost evidence, because the step half of guarantee 5 needs only guarantee 1, which storage has already built.

## The items waiting on design

| Item | Built as |
| --- | --- |
| A run's record never overwritten by an older copy (yoyodyne-ifd.428.116) | the first item of guarantee 1, migration item 1, as written |
| One atomic reservation for every start (yoyodyne-ifd.428.117) | the first item of guarantee 3, migration item 5, as written |
| One budget for each paid message (yoyodyne-ifd.428.118) | the first item of guarantee 4, migration item 7, as written; its one central decision is `internal/budget` |
| Durable indexes for routine reads (yoyodyne-ifd.428.119) | the first items of guarantee 8, migration items 11 and 12; depends on yoyodyne-ifd.428.116; not one index with the tracker |
| A priced ending kept when later output is malformed (yoyodyne-ifd.428.121) | the first item of the cost half of guarantee 5, migration item 9, as written |
| The race check policy (yoyodyne-ifd.428.126.6.1.1) | ruled under guarantee 9; built in migration items 17, 19 and 21, measured by yoyodyne-ifd.428.126.6.2 |

Each of the first five is ruled by this design, and its label saying it waits on design is to be lifted. The architect cannot change a label from her conversation until writing an item's design field from there (yoyodyne-2kr.6) is built, so the development manager lifts them.

## The invariant this warrants

A change that is correct for its own path can still add a copy of a guarantee beside its component, so the rule has to bind work that never mentions it. This is the wording, to be recorded once the architect can submit invariant changes from her conversation (yoyodyne-0bj):

> Each guarantee named in the design for one owner for each guarantee is kept by its one owning component, and code outside that component reaches it only through its way in. A change that needs a guarantee to behave differently changes the component, never a copy beside it.

Until then, this design, the checks above and the reviewer hold it.

## Designs this changes

- **[How internal/orchestrator is split](orchestrator-boundaries.md):** operation 4 reserves a slot rather than reading one. That revision follows this one.
- **[Provider adapters and endpoints](provider-adapters-and-endpoints.md):** the adapter contract returns the best result it has together with any error.
- **[Configurable workflows](configurable-workflows.md):** the graph binds to the function of guarantee 2, as its ruling for yoyodyne-ifd.428.123 now says.
- **[Recoverable and terminal failures](recoverable-and-terminal-failures.md):** a retry in place is decided by `internal/budget`; the windows are unchanged.
- **[Choosing checks by change](choosing-checks-by-change.md):** its statement that the checks before promotion always run whole changes. They may be met by the forge's required checks on the change combined with the current target branch, under the two conditions of guarantee 9. The selection policy is read by guarantee 9's plan.
- **[Repeated checks](repeated-checks.md):** a result may also be kept across commits by the analyser's answer, in shadow first, under guarantee 9.
- **[Capacity for heavy checks](heavy-check-capacity.md):** permits are taken through guarantee 3's reservation. Nothing else in it changes.
- **[Checks on CI](checks-on-ci.md):** unchanged; its switch is read by guarantee 9's plan.
- **[Follow-ups](follow-ups.md), [fewer lifecycle states](fewer-lifecycle-states.md) and [forward progress](forward-progress.md):** unchanged.

Until each named design is revised, this design decides where they differ.

## Alternatives rejected

- **One package that holds every guarantee.** Each guarantee would sit away from the records it protects, and the package would import nearly everything.
- **Moving every path at once.** The Lead Product Manager decided against a standalone rewrite when the orchestrator was split, and the same holds here: it would conflict with every priority-0 change in these files.
- **Checks by searching the source alone.** Where a type can make going around a component impossible, a search is the weaker guard. Searches are used only where no type can do it.
- **A version check alone, or narrow updates alone, for the run record.** See guarantee 1.
- **One index over run records and the tracker.** It would need two writers for one record. See guarantee 8.
- **Each tool keeping its own limit on paid turns.** That is how they came to disagree.
- **A list of places going around a component that may grow.** The next fix made in a hurry would add to it.
- **Checks folded into the evidence guarantee.** Evidence is only one of the six things checks decide. Folding them in would leave what runs, where, for how long and how often with no owner.
- **Renumbering the migration items to put checks second.** Items already admitted cite the numbers, and the order is stated by the groups.
- **Keeping the local full race on every merge candidate.** It is about three of the 4.2 full race runs each item costs here, and the forge's required check already runs the same suite on the combined change. It stays only where those two conditions are not shown to hold.
- **Recording only that a merge candidate's conditions failed, without running locally.** A combined race would then be caught at best four hours later, after it landed.

## What a reviewer can check

- Each guarantee's way in exists once, and no copy of its rule is left on a path that has moved.
- The list of places still going around a component only shrinks.
- No new code adds a whole save of a run record outside the lease holder's versioned save, a slot read followed by a later start, a paid turn without the budget's permission, a cost event parsed outside `internal/cost`, a tracker command run outside `internal/beads`, a configured check launched outside `internal/checks`, or a routine read of all history.
- With the race check rules on, a change with no Go code changed runs no race check before review; a path the analyser cannot place runs the whole race check; and the local full race on the merge candidate is skipped only with both conditions recorded.
- The run on main happens only when main moved since the last clean run, and a failure files one critical report naming the commits between.
- A fix admitted to one of these areas names the parent item (yoyodyne-ifd.428.126) and is written against the component, as the Lead Product Manager's rule says.
