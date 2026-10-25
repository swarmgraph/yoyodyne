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
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-25T16:01:05.958007Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1714, for document-1714.1 under the automatic approval policy
---
# Guarantees the whole system relies on: one owning component and one way in for each

## What this is for

Serves the autonomy goal. On October 25 the operator asked for the guarantees the whole system relies on to become one coherent system (yoyodyne-ifd.428.126). The product leadership review of October 14 and the outside code review of October 25 found the same thing: a guarantee meant to hold everywhere is implemented separately in each execution path, and the copies now disagree. The review traced, among others:

- four paths that load a run record, do other work, and save the whole record back, so a field another writer changed in between is lost (yoyodyne-ifd.428.116);
- fresh dispatch reserving a developer slot under a lock, while recovery paths read the free slots and mark a run live later, so two starts can take one slot (yoyodyne-ifd.428.117);
- the log, run and health tools ending a conversation when their limit is spent, while the repository read, research and recovery tools let it take another paid turn (yoyodyne-ifd.428.118).

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

Not read:

- the code of any of them, including `internal/runstate/store.go`;
- the audit tests already in `internal/runstate`, such as `runrecordwriters_audit_test.go` and `strictdecode_audit_test.go`;
- `internal/developerslot`, the conversation loop and its tools, `internal/cost`, `internal/beads`, and the provider adapters.

Where this design says how the code behaves today, it takes that from the items, which cite the outside review at revision ddef0b1e. Each build item reads the code it changes first. Where the code holds a copy of a guarantee this design does not name, the item stops and the architect is asked.

## The rule

A guarantee here is a rule that has to hold whichever path is running: a fresh run, a continuation, the carrying out of a decision, a reconciliation, a conversation, a surface. For each one:

1. One component owns it, and the rule is written once, there.
2. Code outside the component reaches it only through its way in. Where a type can make going around it impossible, the way in is a type. Otherwise it is a function, and a check in `make test` refuses calls that go around it.
3. A change that needs the guarantee to behave differently changes the component, never a copy beside it.

No new package collects the guarantees. Each lives with the records it protects, in the packages [how internal/orchestrator is split](orchestrator-boundaries.md) already names, so a guarantee is never kept apart from its data.

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

The workflow graph binds to this function. Until the graph runs the pipeline, its position is worked out from the run record and never kept beside it as a second answer. The ruling on whether the graph runs the pipeline (yoyodyne-ifd.428.123) is written against this.

### 3. Every start takes capacity through one reservation

Owner: `internal/runstate`, where counting what is free moves under [how internal/orchestrator is split](orchestrator-boundaries.md), beside `Store.ReclaimSlot` and `CapacityError`.

Way in: one reservation, taken under the shared reservation lock, that checks a slot is free and records it as taken by a named run in the same act.

- Making a run live takes the reservation as an argument, so a start without one does not compile.
- A start refused after reserving gives the reservation back in the same act.
- Fresh dispatch, every continuation, the carrying out of every decision, integration resumption and the limited reclaim all go through it. None of them reads the free slots and acts later.

`internal/developerslot` keeps describing the slots and their tags. If it already counts what is free, the first build item moves that count into the reservation and says so.

This changes operation 4 of the shared continuation operations in [how internal/orchestrator is split](orchestrator-boundaries.md): it reserves the slot rather than reading it. A slot that is not free is still a wait, not a refusal.

Permits for heavy checks keep their own lock until a later item moves them onto the same form of reservation. A run waiting for a permit keeps its developer slot, as [capacity for heavy checks](heavy-check-capacity.md) says, so the two are never taken together.

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

### 5. A step counts as done only on its recorded evidence, and cost is known by one rule

Owner: for steps, the narrow update that records the step, under guarantee 1. For cost, `internal/cost`.

Way in:

- A step is done when its narrow update has been written and read back. Integration still needs evidence bound to the reviewed revision, as the invariant on that says. This guarantee adds only that the evidence is written by one update, not assembled by each path.
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
| 3. Capacity | Making a run live requires a reservation, by its type. A test races two starts, one fresh and one recovery, for the last slot, and exactly one gets it. |
| 4. Budget | A provider turn in a conversation requires the decision's permission, by its type. A test drives a model that never stops asking after every limit and shows the message ends within its budget, with the stop recorded in ordinary words. |
| 5. Evidence and cost | No package outside `internal/cost` decodes a cost event. Each adapter, fed a priced ending followed by malformed output, keeps the cost. |
| 6. Waits and holds | The tests [follow-ups](follow-ups.md) and [fewer lifecycle states](fewer-lifecycle-states.md) already name. |
| 7. Tracker | No package outside `internal/beads` runs the tracker's command. |
| 8. Bounded reads | No routine reader calls the reading of every run record or every event stream. Only rebuilding an index and maintenance may, and the check names them. |

## The order of migration

Each step is an ordinary work item that leaves the factory running and moves one execution path or adds one component. The order is the outside review's: storage and admission first, then one invocation policy, then bounded reads, then tests across component boundaries. Turning these into work items, and their order inside each group, is the development manager's. The Lead Product Manager sets the pull order.

**Storage**

1. The run store refuses a stale whole save, and the four paths the review traced move onto narrow updates: merge queue retirement, transfer (which stops discarding the save's error), admission, and reconciliation's cleanup progress. This is yoyodyne-ifd.428.116 as written.
2. Each other whole save outside the run store moves onto narrow updates, one execution path an item, shrinking the check's list. The paths are the pipeline's completion, each continuation path as it moves into `internal/continuation`, the carrying out of decisions, and triage.
3. Run state changes go through one function, with its check. It is built with the function that chooses where a run resumes (yoyodyne-ifd.428.99.6, and yoyodyne-0rm, which asks for the same).
4. The merge queue records, the list of stopped runs and triage decisions each refuse a stale whole save, one store an item.

**Admission**

5. One reservation for every start, and making a run live requires it. This is yoyodyne-ifd.428.117 as written, covering every path in one item, because a path left outside it can still overrun the slots, and that is the defect.
6. Permits for heavy checks are taken through the same form of reservation.

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

14. A suite drives a completion, an admission, a reconciliation, a recovery start and a reservation race against one run in fixed orders, through the components' ways in only. It shows no field lost, no slot taken twice and no budget passed.

Guarantee 6 moves by the build pieces of follow-ups and fewer lifecycle states, in their order.

This order differs from the review's in one place: item 3 goes with the function that chooses where a run resumes rather than first, because slice 5 of stopped run recovery rewrites the same code.

## The items waiting on design

| Item | Built as |
| --- | --- |
| A run's record never overwritten by an older copy (yoyodyne-ifd.428.116) | the first item of guarantee 1, migration item 1, as written |
| One atomic reservation for every start (yoyodyne-ifd.428.117) | the first item of guarantee 3, migration item 5, as written |
| One budget for each paid message (yoyodyne-ifd.428.118) | the first item of guarantee 4, migration item 7, as written; its one central decision is `internal/budget` |
| Durable indexes for routine reads (yoyodyne-ifd.428.119) | the first items of guarantee 8, migration items 11 and 12; depends on yoyodyne-ifd.428.116; not one index with the tracker |
| A priced ending kept when later output is malformed (yoyodyne-ifd.428.121) | the first item of the cost half of guarantee 5, migration item 9, as written |

Each of the five is ruled by this design, and its label saying it waits on design is to be lifted. The architect cannot change a label from her conversation until writing an item's design field from there (yoyodyne-2kr.6) is built, so the development manager lifts them.

## The invariant this warrants

A change that is correct for its own path can still add a copy of a guarantee beside its component, so the rule has to bind work that never mentions it. This is the wording, to be recorded once the architect can submit invariant changes from her conversation (yoyodyne-0bj):

> Each guarantee named in the design for one owner for each guarantee is kept by its one owning component, and code outside that component reaches it only through its way in. A change that needs a guarantee to behave differently changes the component, never a copy beside it.

Until then, this design, the checks above and the reviewer hold it.

## Designs this changes

- **[How internal/orchestrator is split](orchestrator-boundaries.md):** operation 4 reserves a slot rather than reading one. That revision follows this one.
- **[Provider adapters and endpoints](provider-adapters-and-endpoints.md):** the adapter contract returns the best result it has together with any error.
- **[Configurable workflows](configurable-workflows.md):** the graph binds to the function of guarantee 2, in the ruling for yoyodyne-ifd.428.123.
- **[Recoverable and terminal failures](recoverable-and-terminal-failures.md):** a retry in place is decided by `internal/budget`; the windows are unchanged.
- **[Follow-ups](follow-ups.md), [fewer lifecycle states](fewer-lifecycle-states.md) and [forward progress](forward-progress.md):** unchanged.

## Alternatives rejected

- **One package that holds every guarantee.** Each guarantee would sit away from the records it protects, and the package would import nearly everything.
- **Moving every path at once.** The Lead Product Manager decided against a standalone rewrite when the orchestrator was split, and the same holds here: it would conflict with every priority-0 change in these files.
- **Checks by searching the source alone.** Where a type can make going around a component impossible, a search is the weaker guard. Searches are used only where no type can do it.
- **A version check alone, or narrow updates alone, for the run record.** See guarantee 1.
- **One index over run records and the tracker.** It would need two writers for one record. See guarantee 8.
- **Each tool keeping its own limit on paid turns.** That is how they came to disagree.
- **A list of places going around a component that may grow.** The next fix made in a hurry would add to it.

## What a reviewer can check

- Each guarantee's way in exists once, and no copy of its rule is left on a path that has moved.
- The list of places still going around a component only shrinks.
- No new code adds a whole save of a run record outside the lease holder's versioned save, a slot read followed by a later start, a paid turn without the budget's permission, a cost event parsed outside `internal/cost`, a tracker command run outside `internal/beads`, or a routine read of all history.
- A fix admitted to one of these areas names the parent item (yoyodyne-ifd.428.126) and is written against the component, as the Lead Product Manager's rule says.
