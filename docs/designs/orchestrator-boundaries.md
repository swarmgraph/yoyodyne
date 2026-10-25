---
id: orchestrator-boundaries
kind: design
title: How internal/orchestrator is split, one admitted change at a time
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-15T20:04:00.548696Z
      reason: Names the boundaries internal/orchestrator is split along, the shared continuation operations, what the run-state package owns, how the split meets configurable workflows, and the rule that makes each admitted change move its own boundary; written from reads at 348e9a43. The shared command-line constructors are left for the next revision.
    - action: amended
      by: architect
      at: 2026-10-15T20:48:01.031593Z
      reason: 'yoyodyne-ifd.437.27: finishes the orchestrator split. Names the package each remaining boundary moves to (internal/integration, internal/mergequeue, internal/documentlanding, internal/rolepass) and lists the shared command-line constructors and their owners, from reads of internal/cli/run.go and cli.go at ed48d235. The responsibilities, the owner of the shared rules, the fit with configurable workflows, the first boundary to move and the rule on admitted work were already recorded in the first revision.'
    - action: amended
      by: architect
      at: 2026-10-25T18:44:56.634718Z
      reason: 'Follows the design for one owner for each guarantee, as that design says: operation 4 reserves a developer slot through the one reservation of its guarantee 3 rather than reading whether one is free, and a run paused on unfinished work takes its slot through the same reservation. Nothing else in this section changes. Written from a read at main 22aa9a32.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-15T20:04:00.548696Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1036, for document-1036.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-15T20:48:01.031593Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1040, for document-1040.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-10-25T18:44:56.634718Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1734, for document-1734.1 under the automatic approval policy
---
# How internal/orchestrator is split, one admitted change at a time

## Why this exists

`internal/orchestrator` runs one work item from claim to closure and decides what runs next. At commit 348e9a43 its own code map puts `pipeline.go` at about 9,700 lines and `schedule.go` at about 6,300. The problem is not size. Branching, changes of run state, and knowledge of how stopped runs recover are concentrated in one package, so three reliability changes conflicted on replay in one day, and fixes to one continuation path have been copied to another by hand.

The Lead Product Manager decided there is no standalone rewrite while the priority-0 work in these files and configurable workflows are landing. Instead this design names the boundaries now, and each change already admitted that touches one moves its own responsibility across it as part of its own work.

It serves the autonomy goal in [the v1 goals](../product/goals/v1-goals.md): changes that conflict on replay, and fixes copied by hand between paths, are what pull a person back into the loop. It is filed as a design of its own rather than inside [the v1 harness design](v1-harness-design.md) because that design is too long to be revised whole at present; when it is split, its run section links here.

## The responsibilities

The review that asked for this named four candidates. Each is confirmed or changed below, and two are added.

1. **Delivery sequencing: confirmed.** `pipeline.go`: one run's steps from claim to closure, and how each stop is classified. It stays in `internal/orchestrator` and is what remains once the others leave. The prompts and the notes written on work items (sections 20 and 21 of the package's code map) are text built from the run record; they move out last, because nearly every behaviour change edits them.
2. **Scheduling and capacity: confirmed, and split in two.** Choosing what to start (`schedule.go`, `scheduleslots.go`, `scheduledocuments.go`) stays with the watch loop in `internal/orchestrator`. Counting what is free (`slotIsFree`, and the places for checks that need one in `heavychecks.go`) reads only the run store, so it moves into `internal/runstate`, beside `Store.ReclaimSlot` and `CapacityError`, which already hold the other half of that count.
3. **Continuing and recovering stopped runs: confirmed.** The harness's own continuations (`stallcontinue.go`, `checkstagecontinue.go`, `integrationresume.go`), the development manager's repair (`repaircontinue.go`), and restoring a missing checkout (`checkoutrecovery.go`) move to a new package, `internal/continuation`. Carrying out the development manager's other decisions (`rerun.go`, `carryout*.go`, `carryrearm.go`, `rearm.go`, `recoveryapplicability.go`, `docketredispatch.go`, `triagecaps.go`) joins it only after the stopped-run recovery slices (yoyodyne-ifd.428.99.2 to yoyodyne-ifd.428.99.8) have landed, because those slices rewrite exactly those files and moving them first would make every slice conflict.
4. **Publication and reconciliation: confirmed, without the merge queue.** `publish.go`, `publication.go`, `reconcile*.go`, `stuckmerge.go`, `queuedchecks.go`, `divergedtarget.go`, `redtarget.go`, `landingstop.go` move to `internal/integration`. The forge client itself stays in `internal/publish`, which this package calls. The merge queue (`mergequeue*.go`) is a boundary of its own and moves to `internal/mergequeue`: [integration through a merge queue](integration-through-a-merge-queue.md) gives it its own records, its own worker, and its own promotion.
5. **Document landing: added.** `documentpublication.go`, `documentdocket.go`, `documentdecision.go`, `conversationlanding.go` move to `internal/documentlanding`. It has its own route ([the document landing route](document-landing-route.md)) and its own places, and shares no developer slot.
6. **Role passes: added.** `recurring.go`, `recurringfirings.go`, `passedover.go`, `repeatleave.go`, `programmanagerpass.go`, `supervision.go` move to `internal/rolepass`. These invoke roles in their conversations; they never start or continue a run.

A boundary's package is created by the first admitted change that moves a file into it, not before. Until then a change that only touches those files leaves them where they are, and a change that adds new code to them adds it in the named package once it exists.

## What owns the run's shared rules

`internal/runstate` owns the run record and everything that reads meaning from it: whether a stopped run is one the harness continues itself, how many continuations it has left, and which step a stopped run resumes at. That last one becomes one function of the record alone, which the stopped-run recovery slice that settles where a run resumes (yoyodyne-ifd.428.99.6) builds. `Pipeline.Run`, `Pipeline.Continue`, re-adopting a run after a new build, and every path in `internal/continuation` call that function, and none of them decides the step again.

The ordering rules every continuation keeps — ask everything that can refuse before writing anything; put the item back before making the run live; close the stopped-run entry last; give up the run's lease only just before starting it — belong to `internal/continuation`, because keeping them needs the tracker and the worktree, which `internal/runstate` must not import.

Inside `internal/runstate`, storage is kept apart from reading meaning. Storage is the run store, its leases, the store of stopped runs waiting on the development manager, and replacing a file in one step. Reading meaning is the functions on `runstate.State`. They separate in this order: the shared one-step file replacement first (yoyodyne-ifd.431.5, already admitted); then the functions on `State` gather into their own files as the function that chooses where a run resumes lands, with no storage file calling them. No stored field is renamed, so no stored record needs migrating beyond what the machine home already does.

## The shared continuation operations

At 348e9a43 the four continuation paths each repeat one sequence. They differ in which runs they take and what they record, not in the steps between. The steps become these operations in `internal/continuation`, each a small function taking only the narrow interfaces it needs:

1. **Find and take the stopped run.** Find the run on the development manager's list (`docketedStoppage`), take its lease (`AdoptRun`), and check the run was made for the item the list names. Today only the stall and repair paths check the last; all four will.
2. **Check nothing else runs for the item** (`noRunInFlight`).
3. **Check the item may be continued** (`Items.Show`, then `continuableItem`).
4. **Read the operator's pause on new work, then reserve a developer slot.** Read the intake hold first. Then take a slot through the one reservation that guarantee 3 of [one owner for each guarantee](one-owner-for-each-guarantee.md) sets, which checks a slot is free and records it as taken by this run in the same act. No path reads the free slots and acts on them later. The pause, or a slot that is not free, is a wait, not a refusal, and writes nothing. Operation 7 takes the reservation as its argument, and a continuation refused after this operation gives the reservation back in the same act as the refusal of operation 10.
5. **Make sure the checkout is there and is the harness's.** Restore a missing checkout from the recorded branch, through one restore (`checkoutRecovery`); integration's own `restoreWorktree` is replaced by it. Then `VerifyOwnedHead`, then `preservedChangeHeld` wherever the path judges a change that already exists.
6. **Put the item back:** the note, the claim, `validateClaimedItem`.
7. **Save the run live,** with the continuation record the path builds.
8. **Read the save back and confirm the note,** so a process that ends between the claim and the save is finished on the next attempt without spending anything twice. The repair path's way of doing this (`saveContinuation`, `confirmContinuationNote`) becomes the shared one.
9. **Close the stopped-run entry in the harness's name.** One function taking the decision word and who decided, replacing the three copies of `closeEntry`.
10. **Refuse and hand back to the development manager.** One function writes the refusal on the run, puts the run back on her list, and notes the item, replacing the copies in the stall and checks paths.
11. **Give up the lease and start the named run.**

What stays separate on each path, as its own function and never as a flag:

- **Which runs it takes.** Stall: `HarnessContinuesStall`. Checks: `HarnessContinuesCheckStage`. Integration: `resumableStop` and `causeCleared`. Repair: the development manager's recorded decision and what is left of its grant (`granted`).
- **What it records and what it counts.** Only a repair counts an attempt.

Each path composes the operations in a function of its own. There is no single continue function that takes a mode, because a mode hides which path a fix changes, which is how fixes came to be copied by hand.

Two things that look like continuations stay off these operations:

- **A run paused on unfinished work** (`schedulecontinue.go`). It is paused, not stopped, has no entry on the development manager's list, and keeps its own slot rules. It takes its slot through the same reservation as operation 4 and uses the check for a request to stop the run, and stays with the watch loop.
- **A run adopted again after a new build** (`redeploydrain.go`, through `Pipeline.Run`). It uses only the function that chooses where a run resumes.

## The first move

The first admitted change that touches any of `stallcontinue.go`, `checkstagecontinue.go`, `integrationresume.go` or `repaircontinue.go` creates `internal/continuation`. It moves in the operations its own path uses, exported, and moves its own path onto them. The paths still in `internal/orchestrator` call the moved operations from there.

Where nothing forces the choice, the path in `stallcontinue.go` moves first: the harness continuing a run whose AI session it stopped for going silent or running out of its total budget. It is the smallest of the four (17 KB at 348e9a43), it is the harness's own with no grant to read, and it uses every operation but 8.

Each later fix to a continuation path moves that path. A move changes no behaviour. Adding operation 8 to the stall, checks and integration paths is a behaviour change, so it is admitted as its own item and is not done inside a move.

## How the split meets configurable workflows

[Configurable workflows](configurable-workflows.md) make the delivery graph project-owned data. Today that graph is stepped beside a run and acts on nothing (`declarative.go`, `workflow.go`).

During the migration, continuation and reconciliation bind to the run record and the function that chooses where a run resumes, never to the workflow definition. A graph step that continues a run calls the `internal/continuation` operations, so a continuation fix is written once, not into both the Go path and the graph. The Go-coded run retires only when declarative delivery is the default, which the configurable workflows epic (yoyodyne-ifd.209) decides.

Before the graph may perform a real action rather than observe one, these must hold:

1. **Every action is safe to repeat.** Before acting, it records an attempt naming the action, the run, and a key for this attempt. Before acting again, it reads whether the first attempt's effect is already there, the way the repair path reads the item's notes before writing its note again.
2. **Each action records its outcome and the next step** on the run before giving up the run's lease.
3. **Every action that changes the forge or the target branch is asked again from the forge's or the branch's own state,** never from the record alone, as the limit on how long a queued merge waits and the merge queue's promotion already do.
4. **All four continuation paths have operation 8.**
5. **One function chooses where a run resumes,** and both the Go path and the graph call it.

## Shared constructors in the command line

At ed48d235, `buildComponents` in `internal/cli/run.go` resolves the roots, opens twenty-one stores under the state root, and builds the worktree manager, all for every command that acts on runs. `pipelineFrom`, `reconcilerFrom`, `docketerFrom`, `ledgerFrom` and `supervisionLoopFrom` then wire their services from that one set, and the forge client is built twice, field for field, in `pipelineFrom` and `reconcilerFrom`. Read-only surfaces build none of this: they call `resolveRoots` and `standingRemains` and open the stores they read.

The rule:

- Each store or service that more than one command uses gets one constructor, in the package that owns it, taking the state root and the configuration (or the parts of the configuration it reads).
- `internal/cli` parses flags, calls those constructors, and formats output. It prints what a constructor reports (such as a first binding of a project) rather than the constructor printing it.
- No constructor builds everything for every command. `components` stays in `internal/cli` as the set the acting commands assemble from these constructors, and loses each part as that part's constructor exists.
- A constructor opens and connects; it writes nothing a command did not ask for.
- Read-only and status commands call read-only constructors, so they keep their narrower access and cost.

The constructors, each with its owner:

| What | Built today in | Constructor goes to | Used by |
| --- | --- | --- | --- |
| The three roots: repository, worktree root, state root, agreed with the marker and the project binding | `resolveRoots`, `productStateRoot`, `agreeProductStateRoot`, `primaryCheckout` | `internal/home`, returning the roots and the agreement | every command, acting or read-only |
| Each product store (runs, reports, amendments, evaluations, restart requests, stopped runs waiting on the development manager, branch reviews, directives, intake, watch, released claims, usage limits, outages, capacity served, diverged targets, launch settings, spend, tracker listings, interventions) and the machine-wide pause | `buildComponents` | `internal/runstate`, one constructor per store, as today; no constructor that opens them all | each command opens only the stores it reads or writes |
| The tracker client that records how its listings end | `components.tracker`, `withListingRecord` | `internal/beads` | acting and read-only commands |
| The worktree manager for acting commands | `buildComponents` | `internal/gitworktree`, taking the export refresh as an argument so it imports nothing from `internal/beads` | run, work, reconcile, conversations that steer work |
| The worktree manager for looking only | `standingRemains` | `internal/gitworktree`, a second constructor with the narrow options | status, the dashboard, the Slack sink |
| The forge client | `pipelineFrom` and `reconcilerFrom`, twice | `internal/publish` | the pipeline, the reconciler |
| The check runner and the places for checks that need one | `pipelineFrom`, `heavyCheckPermits` | the runner in `internal/checks`; the places move with counting what is free into `internal/runstate` (responsibility 2), in the same change | the pipeline |
| The reviewer | `pipelineFrom` | `internal/review` | the pipeline, branch review |
| The cost ledger | `ledgerFrom` | `internal/cost` | the pipeline, `yoyo cost` |
| The pipeline, the reconciler, the docket, the supervision loop | `pipelineFrom`, `reconcilerFrom`, `docketerFrom`, `supervisionLoopFrom` | stay in `internal/cli` until their boundary's package exists, then move there: the pipeline to `internal/orchestrator`, the reconciler to `internal/integration`, the docket to `internal/continuation` after the recovery slices, the supervision loop to `internal/rolepass` | the commands that use them today |

Two things were not read for this revision and are left to the change that touches them:

- `observeProgramManagers`, which `buildComponents` calls. The change that moves the roots reads it; if it writes a record, it moves out to the commands that need it rather than into a constructor.
- `standingSources` in `internal/cli/status.go`. The first change that touches it lists the stores it opens against the read-only constructors above.

The machine-home build (yoyodyne-ifd.434.12) is the first change to use these constructors, starting with the roots.

## The rule on admitted work

Every item whose change touches a file this design names under a boundary carries, in its done-conditions: it moves the boundary it touches as this design says, or states why it may not. It also keeps declaring the files it may conflict on, as it does today. A change that would only add code to a file this design moves out adds the new code on the far side of the boundary once that boundary's package exists. The same holds for `internal/cli/run.go`: a change that touches a constructor this design names moves that constructor to its owner.

## What a reviewer can check

- `internal/continuation` imports nothing from `internal/orchestrator`.
- No continuation path takes a mode. Each path's test for which runs it takes is its own function.
- After a move, each shared operation exists once; no copy of it is left in `internal/orchestrator`.
- A move changes no behaviour: the moved path's tests pass unchanged in name and outcome, and tests that touch nothing unexported move with the path.
- Once the function that chooses where a run resumes exists, nothing else makes that choice.
- No stored field of a run record is renamed.
- Once its constructor exists, the forge client is built in one place, and no read-only command builds the acting worktree manager.
- No constructor in the table prints to the terminal or writes a record the command did not ask for.

## Alternatives rejected

- **One rewrite of the package.** The Lead Product Manager decided against it, because it would conflict with every priority-0 change landing in these files.
- **One continuation function with a mode.** It would hide which path a fix changes, and the item asked for the paths' own tests to stay separate.
- **Moving the carrying out of the development manager's decisions now.** The stopped-run recovery slices rewrite those files, so every slice would conflict with the move.
- **Putting the continuation operations in `internal/runstate`.** They need the tracker and the worktree, and `internal/runstate` stays storage and the reading of the record.
- **One constructor that opens every store.** It is what `buildComponents` is today, and it gives read-only commands access and cost they do not need.
- **Writing this into the v1 harness design.** That design is too long to be revised whole at present; it links here once it is split.
