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
    - action: amended
      by: architect
      at: 2026-10-31T05:30:49.169906Z
      reason: 'yoyodyne-ifd.428.126.4.12: rules that run storage may take types and constants from the tracker''s package to store what the tracker client hands it, but never calls the tracker or decides about its store, and that the tracker''s package never imports run storage. Names one build piece: a check in make test with a list of exceptions that may only shrink, which first reads the three files not read here. Written from reads of trackerreading.go, trackerslowcall.go and trackerstoresquash.go at main be20dec3.'
    - action: amended
      by: architect
      at: 2026-11-22T16:31:57.846848Z
      reason: 'Brings the design into line with main 939bc805: names internal/developerslot as the existing owner of slot assignment and retires the orchestrator''s free slot count rather than moving it; adds the GitHub identity path (githubidentity.go) as a fifth continuation path with its own closing copy and take up count; notes the planned packages do not exist yet; rewords hyphenated words. Part of the review of every design against the code.'
    - action: amended
      by: architect
      at: 2026-11-23T16:49:21.31876Z
      reason: 'Part of the review of every design against the code: the section named a file that has been replaced and left out five others, and its rule forbade calls run storage already makes. The file list and the calls come from the readings under docs/diagnoses at 848d86190, and the section says so. The rule now allows a short named list of functions that depend only on their arguments, starting with the test for a work item id; the byte wording call becomes the first exception, to be removed. The check is said plainly to be not built. Written against main at 4e6572cd; nothing else in the design changes.'
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
    - policy: approvals.designs
      revision: 3
      by: harness
      at: 2026-10-31T05:30:49.169906Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2026, for document-2026.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 4
      by: harness
      at: 2026-11-22T16:31:57.846848Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3477, for document-3477.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 5
      by: harness
      at: 2026-11-23T16:49:21.31876Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3694, for document-3694.1 under the automatic approval policy
---
# How internal/orchestrator is split, one admitted change at a time

## Why this exists

`internal/orchestrator` runs one work item from claim to closure and decides what runs next. At commit 348e9a43 its own code map puts `pipeline.go` at about 9,700 lines and `schedule.go` at about 6,300. The problem is not size. Branching, changes of run state, and knowledge of how stopped runs recover are concentrated in one package, so three reliability changes conflicted on replay in one day, and fixes to one continuation path have been copied to another by hand.

The Lead Product Manager decided there is no standalone rewrite while the priority 0 work in these files and configurable workflows are landing. Instead this design names the boundaries now, and each change already admitted that touches one moves its own responsibility across it as part of its own work.

It serves the autonomy goal in [the v1 goals](../product/goals/v1-goals.md): changes that conflict on replay, and fixes copied by hand between paths, are what pull a person back into the loop. It is filed as a design of its own rather than inside [the v1 harness design](v1-harness-design.md) because that design is too long to be revised whole at present; when it is split, its run section links here.

## The responsibilities

The review that asked for this named four candidates. Each is confirmed or changed below, and two are added.

1. **Delivery sequencing: confirmed.** `pipeline.go`: one run's steps from claim to closure, and how each stop is classified. It stays in `internal/orchestrator` and is what remains once the others leave. The prompts and the notes written on work items (sections 20 and 21 of the package's code map) are text built from the run record; they move out last, because nearly every behaviour change edits them.
2. **Scheduling and capacity: confirmed, and split in three.**
   - Choosing what to start (`schedule.go`, `scheduleslots.go`, `scheduledocuments.go`) stays with the watch loop in `internal/orchestrator`.
   - Which developer slot each run in flight occupies, and which slots are free to pull, already has its own package, `internal/developerslot`. Its one function, `Assign`, is read by both the scheduler and the standing status, so the two never disagree about a slot. It reads neither the run store nor the tracker: its callers hand it the runs in flight and their labels. It stays where it is, and no other package works out a slot's occupant again.
   - Taking a slot belongs to the run store in `internal/runstate`: the one reservation that guarantee 3 of [one owner for each guarantee](one-owner-for-each-guarantee.md) sets, which checks a slot is free and records it taken in the same act. At 939bc805 a continuation takes it through `Store.ReserveLive` with the capacity, beside `Store.ReclaimSlot` and `CapacityError`. The orchestrator's own count of free slots (`slotIsFree`), which some continuation paths read before they reserve, is retired rather than moved, as each path moves onto operation 4 below: a count read before the reservation can be out of date by the time the reservation is made. The places for checks that need one (`heavychecks.go`) read only the run store, so they move into `internal/runstate`.
3. **Continuing and recovering stopped runs: confirmed.** The harness's own continuations (`stallcontinue.go`, `checkstagecontinue.go`, `integrationresume.go`, `githubidentity.go`), the development manager's repair (`repaircontinue.go`), and restoring a missing checkout (`checkoutrecovery.go`) move to a new package, `internal/continuation`. Carrying out the development manager's other decisions (`rerun.go`, `carryout*.go`, `carryrearm.go`, `rearm.go`, `recoveryapplicability.go`, `docketredispatch.go`, `triagecaps.go`) joins it only after the recovery slices for stopped runs (yoyodyne-ifd.428.99.2 to yoyodyne-ifd.428.99.8) have landed, because those slices rewrite exactly those files and moving them first would make every slice conflict.
4. **Publication and reconciliation: confirmed, without the merge queue.** `publish.go`, `publication.go`, `reconcile*.go`, `stuckmerge.go`, `queuedchecks.go`, `divergedtarget.go`, `redtarget.go`, `landingstop.go` move to `internal/integration`. The forge client itself stays in `internal/publish`, which this package calls. The merge queue (`mergequeue*.go`) is a boundary of its own and moves to `internal/mergequeue`: [integration through a merge queue](integration-through-a-merge-queue.md) gives it its own records, its own worker, and its own promotion.
5. **Document landing: added.** `documentpublication.go`, `documentdocket.go`, `documentdecision.go`, `conversationlanding.go` move to `internal/documentlanding`. It has its own route ([the document landing route](document-landing-route.md)) and its own places, and shares no developer slot.
6. **Role passes: added.** `recurring.go`, `recurringfirings.go`, `passedover.go`, `repeatleave.go`, `programmanagerpass.go`, `supervision.go` move to `internal/rolepass`. These invoke roles in their conversations; they never start or continue a run.

A boundary's package is created by the first admitted change that moves a file into it, not before. At 939bc805 none of `internal/continuation`, `internal/integration`, `internal/mergequeue`, `internal/documentlanding` or `internal/rolepass` exists yet, as that rule allows. Until a package exists, a change that only touches those files leaves them where they are, and a change that adds new code to them adds it in the named package once it exists.

## What owns the run's shared rules

`internal/runstate` owns the run record and everything that reads meaning from it: whether a stopped run is one the harness continues itself, how many continuations it has left, and which step a stopped run resumes at. That last one becomes one function of the record alone, which the recovery slice that settles where a run resumes (yoyodyne-ifd.428.99.6) builds. `Pipeline.Run`, `Pipeline.Continue`, adopting a run again after a new build, and every path in `internal/continuation` call that function, and none of them decides the step again.

The ordering rules every continuation keeps — ask everything that can refuse before writing anything; put the item back before making the run live; close the entry for the stopped run last; give up the run's lease only just before starting it — belong to `internal/continuation`, because keeping them needs calls to the tracker and acts on the worktree, which `internal/runstate` never makes.

Inside `internal/runstate`, storage is kept apart from reading meaning. Storage is the run store, its leases, the store of stopped runs waiting on the development manager, and replacing a file in one step. Reading meaning is the functions on `runstate.State`. They separate in this order: the shared replacement of a file in one step first (yoyodyne-ifd.431.5, already admitted); then the functions on `State` gather into their own files as the function that chooses where a run resumes lands, with no storage file calling them. No stored field is renamed, so no stored record needs migrating beyond what the machine home already does.

### What run storage may take from the tracker's package

Run storage also keeps records about the tracker under the harness's state directory: the shared reading of the tracker's listings, each tracker call that ran out of time, and what maintenance measured, cleaned and rebuilt in the tracker's store. The reading of the code recorded in `docs/diagnoses/yoyodyne-ifd-437-84-1.md` and corrected in `docs/diagnoses/yoyodyne-ifd-437-84-1b.md`, the second of them at 848d86190, found ten files in `internal/runstate` that import `internal/beads`. Seven are about the tracker or its store: `trackerreading.go`, `trackerslowcall.go`, `trackercalllog.go`, `trackerstoremeasurement.go`, `trackerstorecleanup.go`, `trackerexportcleanup.go` and `trackerstorerebuild.go`, which replaced `trackerstoresquash.go`. The other three are `document.go`, `emptyslots.go` and `state.go`.

That reading also found two functions of `internal/beads` called from run storage: `ValidIssueID`, which says whether a text has the form of a work item id, in `document.go`, `emptyslots.go` and `state.go`; and `SayBytes`, which words a number of bytes, in `trackerstorecleanup.go`. It found no check holding the rule below.

Taking types and constants is allowed, and this is the rule:

- A file in `internal/runstate` may take types and constants from `internal/beads`, to store what the tracker client hands it in the client's own shape.
- It may call the functions on one short list kept in the check, and no others. A function is on that list only where its answer depends on nothing but the values it is handed: it starts no process, opens no file, and reads no tracker. The list starts with `ValidIssueID` alone, so that a stored work item id is judged by the same rule as the tracker's. Adding a function to it is a revision of this section.
- Apart from that list it never calls a function or method of `internal/beads`, never runs the tracker's command, and never decides anything about the tracker's store. Calling the tracker is the client's, under guarantee 7 of [one owner for each guarantee](one-owner-for-each-guarantee.md). Deciding when the store is measured, cleaned or rebuilt is maintenance's (`internal/maintain`), as [keeping the tracker store small](tracker-store-size.md) says. Run storage only records what they did.
- `internal/beads` imports nothing from `internal/runstate`. Where the client hands a record to a store here, it does so through an interface it declares, and the command line connects the two.

A check in `make test` is to hold this, and is not built at 848d86190. Each file in `internal/runstate` uses from `internal/beads` only the names of types and constants and the functions on the list above, and `internal/beads` does not import `internal/runstate`. The check goes in with a list of the files that do more, and that list may only shrink. It starts with the call to `SayBytes`, which is wording rather than storage and goes when that file is next changed. The item that builds the check reads every file that imports `internal/beads` at its own commit first; any call to the tracker it finds there moves out in that item, to `internal/beads`, or to `internal/maintain` where it decides something about the store.

This is a design decision, not an invariant: [the rule that a guarantee is kept only by its owner](../decisions/invariants/each-guarantee-has-one-owner.md) already binds work that never mentions it.

Rejected:

- Giving run storage types of its own and converting at each caller. The work item is the tracker's largest type, and a copy of it would drift from the original with nobody owning the difference.
- Giving run storage its own copy of the test for a work item id. Two copies could disagree about which ids are valid, and a record could then be refused for an item the tracker holds.
- Allowing every function of `internal/beads` that does not call the tracker. Nothing in a function's name says whether it does, so a check could not hold it; a short list can be read whole.
- Moving these stores into `internal/beads`. They are files under the harness's state directory, written through the confined writer this package owns, and the table of constructors below already places every product store, the tracker listings included, here.
- Moving the shared types into a package of their own. It would add a package for no protection the check above does not already give.

A reviewer can check that no file in `internal/runstate` calls into `internal/beads` beyond the list, and that `internal/beads` does not import `internal/runstate`.

## The shared continuation operations

Five continuation paths repeat one sequence. Four were named at 348e9a43: the harness continuing a run whose AI session it stopped for going silent or running out of its total budget (`stallcontinue.go`), continuing at the checks (`checkstagecontinue.go`), resuming at the landing (`integrationresume.go`), and the development manager's repair (`repaircontinue.go`). The fifth, read at 939bc805, is `githubidentity.go`: it takes up a run that stopped only because GitHub refused the harness's identity, at the first look that finds GitHub answering that identity again, either at its landing through the integration path's resumer, or at its checks on the same branch and worktree, owing its push. The paths differ in which runs they take and what they record, not in the steps between. A further path found to repeat the sequence is held to this section the same way, and the change that touches it adds it here.

The steps become these operations in `internal/continuation`, each a small function taking only the narrow interfaces it needs:

1. **Find and take the stopped run.** Find the run on the development manager's list (`docketedStoppage`), take its lease (`AdoptRun`), and check the run was made for the item the list names. Today not every path checks the last; all will.
2. **Check nothing else runs for the item** (`noRunInFlight`).
3. **Check the item may be continued** (`Items.Show`, then `continuableItem`).
4. **Read the operator's pause on new work, then reserve a developer slot.** Read the intake hold first. Then take a slot through the one reservation that guarantee 3 of [one owner for each guarantee](one-owner-for-each-guarantee.md) sets, which checks a slot is free and records it as taken by this run in the same act. No path reads the free slots and acts on them later: at 939bc805 the GitHub identity path reads `slotIsFree` before it reserves, and that earlier read goes when the path moves. The pause, or a slot that is not free, is a wait, not a refusal, and writes nothing; a reservation refused because the last slot went to another run meanwhile is the same wait. Operation 7 takes the reservation as its argument, and a continuation refused after this operation gives the reservation back in the same act as the refusal of operation 10.
5. **Make sure the checkout is there and is the harness's.** Restore a missing checkout from the recorded branch, through one restore (`checkoutRecovery`); integration's own `restoreWorktree` is replaced by it. A path that must not restore one, such as the GitHub identity path, whose owed push is of exactly the change that was on that branch, refuses instead and says so. Then `VerifyOwnedHead`, then `preservedChangeHeld` wherever the path judges a change that already exists.
6. **Put the item back:** the note, the claim, `validateClaimedItem`.
7. **Save the run live,** with the continuation record the path builds.
8. **Read the save back and confirm the note,** so a process that ends between the claim and the save is finished on the next attempt without spending anything twice. The repair path's way of doing this (`saveContinuation`, `confirmContinuationNote`) becomes the shared one.
9. **Close the entry for the stopped run in the harness's name.** One function taking the decision word and who decided, replacing the copy of `closeEntry` each path keeps: three were counted at 348e9a43, and the GitHub identity path has its own.
10. **Refuse and hand back to the development manager.** One function writes the refusal on the run, puts the run back on her list, and notes the item, replacing the copies in the paths for a silent session and for the checks. A path that refuses before writing anything leaves the entry on her list as it stands and writes nothing, as the GitHub identity path does.
11. **Give up the lease and start the named run.**

What stays separate on each path, as its own function and never as a flag:

- **Which runs it takes.** A silent session: `HarnessContinuesStall`. Checks: `HarnessContinuesCheckStage`. Landing: `resumableStop` and `causeCleared`. GitHub identity: a run whose record holds the identity stop and is ended while still developing, or an integration stop with the cause that the remote refused the harness's key, once GitHub answers that identity again for the run's target branch. Repair: the development manager's recorded decision and what is left of its grant (`granted`).
- **What it records and what it counts.** Only a repair counts a repair attempt. The GitHub identity path counts its own take ups, at most `runstate.MaxIdentityTakeUps`, after which the stop is the development manager's.

Each path composes the operations in a function of its own. There is no single continue function that takes a mode, because a mode hides which path a fix changes, which is how fixes came to be copied by hand.

Two things that look like continuations stay off these operations:

- **A run paused on unfinished work** (`schedulecontinue.go`). It is paused, not stopped, has no entry on the development manager's list, and keeps its own slot rules. It takes its slot through the same reservation as operation 4 and uses the check for a request to stop the run, and stays with the watch loop.
- **A run adopted again after a new build** (`redeploydrain.go`, through `Pipeline.Run`). It uses only the function that chooses where a run resumes.

## The first move

The first admitted change that touches any of `stallcontinue.go`, `checkstagecontinue.go`, `integrationresume.go`, `githubidentity.go` or `repaircontinue.go` creates `internal/continuation`. It moves in the operations its own path uses, exported, and moves its own path onto them. The paths still in `internal/orchestrator` call the moved operations from there.

Where nothing forces the choice, the path in `stallcontinue.go` moves first: the harness continuing a run whose AI session it stopped for going silent or running out of its total budget. It is the smallest of the first four (17 KB at 348e9a43), it is the harness's own with no grant to read, and it uses every operation but 8. The GitHub identity path moves after the landing path, because it resumes a landing through that path's resumer.

Each later fix to a continuation path moves that path. A move changes no behaviour. Adding operation 8 to every path but the repair is a behaviour change, so it is admitted as its own item and is not done inside a move.

## How the split meets configurable workflows

[Configurable workflows](configurable-workflows.md) make the delivery graph project-owned data. Today that graph is stepped beside a run and acts on nothing (`declarative.go`, `workflow.go`).

During the migration, continuation and reconciliation bind to the run record and the function that chooses where a run resumes, never to the workflow definition. A graph step that continues a run calls the `internal/continuation` operations, so a continuation fix is written once, not into both the Go path and the graph. The run coded in Go retires only when declarative delivery is the default, which the configurable workflows epic (yoyodyne-ifd.209) decides.

Before the graph may perform a real action rather than observe one, these must hold:

1. **Every action is safe to repeat.** Before acting, it records an attempt naming the action, the run, and a key for this attempt. Before acting again, it reads whether the first attempt's effect is already there, the way the repair path reads the item's notes before writing its note again.
2. **Each action records its outcome and the next step** on the run before giving up the run's lease.
3. **Every action that changes the forge or the target branch is asked again from the forge's or the branch's own state,** never from the record alone, as the limit on how long a queued merge waits and the merge queue's promotion already do.
4. **Every continuation path has operation 8.**
5. **One function chooses where a run resumes,** and both the Go path and the graph call it.

## Shared constructors in the command line

At ed48d235, `buildComponents` in `internal/cli/run.go` resolves the roots, opens twenty-one stores under the state root, and builds the worktree manager, all for every command that acts on runs. `pipelineFrom`, `reconcilerFrom`, `docketerFrom`, `ledgerFrom` and `supervisionLoopFrom` then wire their services from that one set, and the forge client is built twice, field for field, in `pipelineFrom` and `reconcilerFrom`. Surfaces that only read build none of this: they call `resolveRoots` and `standingRemains` and open the stores they read.

The rule:

- Each store or service that more than one command uses gets one constructor, in the package that owns it, taking the state root and the configuration (or the parts of the configuration it reads).
- `internal/cli` parses flags, calls those constructors, and formats output. It prints what a constructor reports (such as a first binding of a project) rather than the constructor printing it.
- No constructor builds everything for every command. `components` stays in `internal/cli` as the set the acting commands assemble from these constructors, and loses each part as that part's constructor exists.
- A constructor opens and connects; it writes nothing a command did not ask for.
- Commands that only read, and status commands, call constructors that only read, so they keep their narrower access and cost.

The constructors, each with its owner:

| What | Built today in | Constructor goes to | Used by |
| --- | --- | --- | --- |
| The three roots: repository, worktree root, state root, agreed with the marker and the project binding | `resolveRoots`, `productStateRoot`, `agreeProductStateRoot`, `primaryCheckout` | `internal/home`, returning the roots and the agreement | every command, acting or only reading |
| Each product store (runs, reports, amendments, evaluations, restart requests, stopped runs waiting on the development manager, branch reviews, directives, intake, watch, released claims, usage limits, outages, capacity served, diverged targets, launch settings, spend, tracker listings, interventions) and the machine-wide pause | `buildComponents` | `internal/runstate`, one constructor per store, as today; no constructor that opens them all | each command opens only the stores it reads or writes |
| The tracker client that records how its listings end | `components.tracker`, `withListingRecord` | `internal/beads` | acting commands and commands that only read |
| The worktree manager for acting commands | `buildComponents` | `internal/gitworktree`, taking the export refresh as an argument so it imports nothing from `internal/beads` | run, work, reconcile, conversations that steer work |
| The worktree manager for looking only | `standingRemains` | `internal/gitworktree`, a second constructor with the narrow options | status, the dashboard, the Slack sink |
| The forge client | `pipelineFrom` and `reconcilerFrom`, twice | `internal/publish` | the pipeline, the reconciler |
| The check runner and the places for checks that need one | `pipelineFrom`, `heavyCheckPermits` | the runner in `internal/checks`; the places move into `internal/runstate` (responsibility 2), in the same change | the pipeline |
| The reviewer | `pipelineFrom` | `internal/review` | the pipeline, branch review |
| The cost ledger | `ledgerFrom` | `internal/cost` | the pipeline, `yoyo cost` |
| The pipeline, the reconciler, the docket, the supervision loop | `pipelineFrom`, `reconcilerFrom`, `docketerFrom`, `supervisionLoopFrom` | stay in `internal/cli` until their boundary's package exists, then move there: the pipeline to `internal/orchestrator`, the reconciler to `internal/integration`, the docket to `internal/continuation` after the recovery slices, the supervision loop to `internal/rolepass` | the commands that use them today |

Two things were not read for this revision and are left to the change that touches them:

- `observeProgramManagers`, which `buildComponents` calls. The change that moves the roots reads it; if it writes a record, it moves out to the commands that need it rather than into a constructor.
- `standingSources` in `internal/cli/status.go`. The first change that touches it lists the stores it opens against the constructors above that only read.

The machine-home build (yoyodyne-ifd.434.12) is the first change to use these constructors, starting with the roots.

## The rule on admitted work

Every item whose change touches a file this design names under a boundary carries, in its conditions for being done: it moves the boundary it touches as this design says, or states why it may not. It also keeps declaring the files it may conflict on, as it does today. A change that would only add code to a file this design moves out adds the new code on the far side of the boundary once that boundary's package exists. The same holds for `internal/cli/run.go`: a change that touches a constructor this design names moves that constructor to its owner.

## What a reviewer can check

- `internal/continuation` imports nothing from `internal/orchestrator`.
- `internal/developerslot` imports neither `internal/runstate` nor `internal/beads`, and no other package works out which slot a run occupies.
- No continuation path reads the free slots and then reserves one; each takes its slot through the one reservation.
- No continuation path takes a mode. Each path's test for which runs it takes is its own function.
- After a move, each shared operation exists once; no copy of it is left in `internal/orchestrator`.
- A move changes no behaviour: the moved path's tests pass unchanged in name and outcome, and tests that touch nothing unexported move with the path.
- Once the function that chooses where a run resumes exists, nothing else makes that choice.
- No stored field of a run record is renamed.
- Once its constructor exists, the forge client is built in one place, and no command that only reads builds the acting worktree manager.
- No constructor in the table prints to the terminal or writes a record the command did not ask for.

## Alternatives rejected

- **One rewrite of the package.** The Lead Product Manager decided against it, because it would conflict with every priority 0 change landing in these files.
- **One continuation function with a mode.** It would hide which path a fix changes, and the item asked for the paths' own tests to stay separate.
- **Moving the carrying out of the development manager's decisions now.** The recovery slices for stopped runs rewrite those files, so every slice would conflict with the move.
- **Putting the continuation operations in `internal/runstate`.** They need the tracker and the worktree, and `internal/runstate` stays storage and the reading of the record.
- **Moving the developer slot assignment into `internal/runstate`, as an earlier revision said for counting free slots.** It already stands alone, reads neither store, and is shared by the scheduler and the status; folding it into storage would give it access it does not need.
- **One constructor that opens every store.** It is what `buildComponents` is today, and it gives commands that only read access and cost they do not need.
- **Writing this into the v1 harness design.** That design is too long to be revised whole at present; it links here once it is split.
