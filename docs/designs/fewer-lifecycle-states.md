---
id: fewer-lifecycle-states
kind: design
title: 'Fewer lifecycle states: one record for every wait and hold, one meaning for each recovery decision, one writer for each store'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-24T23:11:21.548245Z
      reason: 'yoyodyne-ifd.428.113: the operator''s direction of October 24, from the product leadership review, that recovery stop growing special cases. Inventories the item and run states, waits, holds, recovery paths and stores; sets a target where every wait and hold is one follow-up record, the unresolved hold is a wait that keeps a stop listed, tracker progress fields are display only, and each store has one writer; orders the folding starting with this week''s brake and hold additions; states the test for future fixes and the invariant it warrants; names the build pieces.'
    - action: amended
      by: architect
      at: 2026-11-22T16:41:12.483636Z
      reason: Checked against the code at main 939bc805 on Nov 22. The brake on new work was never folded and has grown its own pieces, now mapped onto the follow-up's deadlines; the landing hold is ruled the one exception; the operator's pause is one file at the state root; the development manager's wait is kept on her list, with a sweep of its own; about twenty holds known only by file name are listed for an inventory that goes first, and the invariant is recorded after it.
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-24T23:11:21.548245Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1661, for document-1661.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-11-22T16:41:12.483636Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3483, for document-3483.1 under the automatic approval policy
---
# Fewer lifecycle states: one record for every wait and hold, one meaning for each recovery decision, one writer for each store

## What this is for

Serves the autonomy goal: the line runs without a person carrying it, and that needs recovery a person can reason about. In the week before October 24, recovery grew by one special case for each incident:

- the brake on new work gained trial runs, an overdue setting and a fresh decision on every development manager pass;
- the hold on new work for a cause outside the work gained a check of its own;
- the pause for an exhausted provider endpoint gained a probe on its own schedule.

Each was right on its own. Together they are several ways of waiting, each with its own check, its own schedule and its own place on a surface, and the next incident would have added another.

The operator asked on October 24 for a review that designs fewer of them (yoyodyne-ifd.428.113). This design gives:

- an inventory of the states an item and a run can be in, the recovery paths, and who owns each stored record;
- a target with fewer of each;
- the order the special cases fold into the general mechanism;
- the test any future fix must meet;
- the build pieces.

It was first written on October 24 without reading the code. It was checked against the code on November 22, and the sections below say what that check found. The finding that matters most is that the inventory was incomplete then and is further from complete now: many ways of waiting and holding were added after October 24, and none of them came to the architect for a ruling, although the test in this design asked for one.

## What it rests on, and what was not read

Read whole at commit a7f6d4b6 when first written:

- [forward progress](forward-progress.md);
- [follow-ups](follow-ups.md);
- [stopped run recovery](stopped-run-recovery.md);
- [fresh factory health and preserved run recovery](fresh-factory-health-and-preserved-run-recovery.md);
- [recoverable and terminal failures](recoverable-and-terminal-failures.md).

Read at commit 939bc805 on November 22:

- this design, whole;
- in `internal/runstate`: `followup.go`, `hold.go`, `intakebrake.go` and `landinghold.go`;
- in `internal/orchestrator`: `neverendingwait.go`;
- the listing of `internal/runstate`, whole;
- the listing of `internal/orchestrator`, cut at its first 400 of 503 names, so files later in the alphabet than `runindexrebuild_test.go` were not seen.

Not read: every other file named in the inventory below, the scheduler's code, the readiness check and the read model. What those files hold is known only from their names. Each build piece starts by reading the code it changes, and where it finds a wait, a hold or a store this design does not name, it stops and the architect is asked.

## The inventory

### What an admitted item can be in

The states are those of point 1 of [forward progress](forward-progress.md), worked out from records and never stored:

- running;
- waiting for capacity;
- waiting with a follow-up;
- stopped;
- waiting on its conversation.

Beside them, the tracker stores a status (open, in progress, closed), an assignee set when the harness claims an item, a parked label, and dependencies. The stored status and assignee are copies of what the run records say. They have drifted from them: items have shown as in progress with no run, and items have kept an assignee after their run ended (yoyodyne-819, yoyodyne-ifd.392).

### What a run can be in

- **Saved status and step**, on the run record.
- **Worker presence**, worked out by the one rule for identifying a worker: live, absent or uncertain.
- **Waits taken inside a run while its worker is live:**
  - a usage limit waited to its reset;
  - a provider overload waited out;
  - a provider that cannot be reached;
  - a permit for a heavy check;
  - the recovery series at each boundary.
- **Waits that outlive the worker and release the developer slot:**
  - a dependency wait;
  - a run waiting only to land, under forward progress.
- **A stop**, with its cause (about the work, outside the work, or uncertain) and what failed.
- **A decision on a stop:** recorded, being carried out, carried out and checked, refused, or unknown.
- **An unresolved hold:** recorded as a decision, or put on the run by an unknown result.
- **Integration recorded**, owing the steps after it.
- **Ended**, possibly still owing cleanup or holding preserved work.

### What holds new work, as built on November 22

1. **The brake on new work** (`internal/runstate/intakebrake.go`). It trips on counted stops and keeps its own record on the hold on new work. It was not folded into a follow-up. Since October 24 it has grown two pieces of its own: a count of trial runs that were blocked, with a bound after which the harness hands the hold to the Lead Product Manager; and a decision for the development manager with three answers, release it, try a trial run now, or hand it up.
2. **A hold for a cause outside the work,** such as a license not accepted, checked again by a command (yoyodyne-ifd.432.25.19). Its code was not read.
3. **The pause for provider capacity,** for each exhausted endpoint. Fresh dispatch is held until the endpoint's reset or a successful probe (yoyodyne-ifd.435.10), on a paced schedule where there is no reset (yoyodyne-ifd.435.21). Its files are `internal/runstate/capacityprobe.go` and `internal/runstate/provideroutage.go`; not read.
4. **The operator's pause,** set by `yoyo pause`. It is one file at the root of the state directory, and every provider call on the machine reads it before spending. It is not one product's hold on new work, as this design first said; it stops spending across the machine.
5. **The readiness check holding one item,** such as for a missing cited name.
6. **Parking one item.**
7. **The landing hold** (`internal/runstate/landinghold.go`), which this design first missed. A tool outside the harness stops landing on one branch through `yoyo landing hold`, for at most four hours. It ends on its own at its expiry, or when that tool releases it.

### The wait the development manager decides

When the development manager decides to wait on a stopped run, the decision is kept as a closed entry on her list of stopped runs, naming the item it waits on and a time it holds until. It is not a follow-up. A sweep in `internal/orchestrator/neverendingwait.go` reopens any such wait that names an item above the stopped one, or an item that still has open child items, because an item cannot close while work under it is open and such a wait would never end on the work it meant. A new wait of that kind is already refused when it is recorded, so the sweep only mends records written before that refusal existed. It is a check of a wait's cause running outside the follow-up checks, which this design rules out.

### Holds and waits known only by file name

The listings show these further ways of waiting, holding or stopping, none of them in the inventory as first written. What each holds, who writes it and who reads it is not known until build piece 8 reads them.

In `internal/runstate`:

- `needsdesignwake.go` and `needsdesignlabelled.go`, work held until the architect settles its design;
- `conversationwait.go`, an item waiting on its conversation;
- `continuationwait.go`, a run waiting to be continued;
- `documentwait.go`, an item waiting on a document;
- `forgewait.go`, a wait on GitHub;
- `mergequeuehold.go`, a hold on the harness's merge queue;
- `launchsettingshold.go`, a hold on launch settings;
- `identitystop.go`, a stop for GitHub refusing the harness's identity;
- `divergedtarget.go`, a target branch that has diverged from GitHub;
- `stall.go` and `factorystall.go`, a stalled factory;
- two files on a provider's usage limit and the time it resets.

In `internal/orchestrator`:

- `needsdesignwait.go`;
- `reconcilewait.go`, a wait while a merge is reconciled;
- `mergequeuewait.go` and `mergequeueforgewait.go`, merge queue entries waiting, including on GitHub;
- `redtarget.go`, work held while the target branch fails its checks;
- `pausenote.go`;
- `githubidentity.go` and `provideroutage.go`, the orchestrator's side of two holds above.

### Recovery paths

- **Retried in place:** the recoverable failures at each boundary of recoverable and terminal failures, inside each boundary's window.
- **Checks run again:** a check stopped by its time limit, run again up to twice; a failure confined to untouched tests, run again once.
- **A repair loop,** while repair attempts remain.
- **The development manager's decisions** on a stop: continue, repair, rerun, finish the merge, wait, give the item back, record an unresolved hold, ask the Lead Product Manager. Beside them is the one recovery act that is not a decision: releasing what a finished run with no worker still holds.
- **The harness's own five acts** while she is unavailable.
- **Releasing an earlier program's leases** when a program starts.
- **Reconciling a merge** that was asked for and never confirmed.

### Stores and who writes them

| Record | Kept in | Written by |
| --- | --- | --- |
| Run record and the item's recovery budget | the run store | the harness under the run lease, for the run's worker and for recovery acts |
| Follow-ups | the follow-up store, one file per follow-up under each product, kept on the machine that recorded it | the act that leaves something waiting, in the same act; the check runner; a close with a reason |
| Item status, assignee, labels, dependencies, notes | the tracker | the harness on roles' behalf |
| Waiting entries | worked out from the records above | nobody; read only |
| The development manager's list of stopped runs, including her waits | its own store | the harness for her decisions; the sweep that reopens waits that cannot end |
| Pass records, dispositions and reports | the pass and report stores | the harness for each role's pass |
| Why ready work was not started | the scheduler's pull records | the scheduler |
| Workflow observation | the workflow store | the harness, observing the run |
| Program start records and records beside leases | the state directory | each program, and each worker as it starts |
| The brake's record, count and trial runs | the hold on new work | the scheduler and the development manager's decision |
| The operator's pause | one file at the state root | `yoyo pause` |
| The landing hold | its own record | the outside tool, through `yoyo landing hold` |
| The pause for each endpoint | the scheduler's own records | the scheduler |
| The records named only by file above | the run state and orchestrator packages | not read |

## The target

### 1. Every wait and every hold is one follow-up

The target stands. A follow-up from [follow-ups](follow-ups.md) is the only record of anything waiting, whatever waits on it. That covers every hold above except the landing hold, both waits that release a slot, every wait on a stop, and every hold in the list known by file name unless piece 8 finds a reason to keep it. The following stop existing as separate mechanisms:

- any check of a hold's cause outside the follow-up check runner;
- any schedule for checking a cause again other than the check runner's intervals;
- any overdue setting other than the follow-up's deadlines;
- any surface entry for a hold other than the follow-up shown beside its own kind.

What each hold keeps of its own is only what starts it and what it blocks:

- **The brake** keeps its count of stops, which decides when it trips, and the spacing of trial runs, which recoverable and terminal failures sets; the trial run that starts by itself stays the brake's own for that reason. Tripping writes a follow-up owned by the development manager, which holds intake and so counts as priority 0. Its check is that an item has a run that landed on the target branch, naming the trial run's item. The pieces it has grown map onto the follow-up as follows:
  - the follow-up being on her list on every pass, until it ends, is the follow-up's first deadline, replacing the fresh decision on every pass (yoyodyne-ifd.432.25.8) and `execution.brake_probe_overdue_after`;
  - the harness handing the hold to the Lead Product Manager at the bound of blocked trial runs is the follow-up's second deadline;
  - her release closes the follow-up with her reason; her choice to try a trial run now asks for the brake's own trial run; her choice to hand it up is the second deadline taken early.
- **A hold for a cause outside the work** has the environment check the harness supplies, as follow-ups already says. Nothing else checks it.
- **The pause for an exhausted endpoint** is a hold whose check is that a provider can be used again. The paced probe of yoyodyne-ifd.435.21 is how that check runs for an endpoint with no reset. Its pacing is the check's own interval, set in the check runner, not a schedule in the scheduler.
- **The operator's pause** keeps its one file at the state root, because every provider call on the machine reads it before spending, across every product. It also has a follow-up that he alone ends, so it shows beside every other hold.
- **The readiness check holding an item** parks the item, with its reason in the parking note. The parking follow-up's check is that the item is no longer parked, owned by the role that filed the item. A second way of holding one item from dispatch disappears.
- **Parking** is unchanged from follow-ups.
- **The development manager's wait** becomes a follow-up whose check is that the item waited on has closed. Refusing a wait on an item above the stopped one, or on an item with open child items, stays where it is, at the moment she records it. The sweep that reopens old waits of that kind stays only until no such record is left, and is then removed.

**The one exception is the landing hold.** It stays its own record, for three reasons. The tool that places it is outside the harness, so no role could own a follow-up for it. It carries its own expiry of at most four hours, which is the deadline a follow-up would give it. And nothing in the harness has to check its cause.

### 2. The unresolved hold is a wait that keeps the stop listed

The decision to record an unresolved hold, and the hold an unknown result puts on a run, become one thing: a wait whose follow-up keeps the stop on the development manager's list. It does not take the stop off. That is the meaning step 2 of stopped run recovery already gives a wait the harness records on its own.

It names:

- its owner;
- its next step;
- its check, most often that the stop has left the list, or for an unknown result that the run has been reloaded and what it holds established;
- its deadlines.

While it is open on a run after an unknown result, no act is taken on that run, as fresh factory health requires.

The closed list of decisions loses one entry. There are two kinds of wait, told apart by one field the follow-up carries, whether it keeps the stop off the list:

- a wait she decided, which takes the stop off the list while its follow-up is open and before its first deadline;
- a wait that keeps it listed.

Every decision then has one meaning, whoever takes it:

| Decision | Meaning |
| --- | --- |
| Continue | the same run goes on from its step, spending nothing |
| Repair | the same run goes back to its developer with stated attempts |
| Rerun | a fresh run of the item from the current target branch |
| Finish the merge | complete the steps after a recorded or possible integration |
| Wait | a follow-up, which either takes the stop off the list or keeps it listed |
| Give the item back | retire the run with its change kept, release the item, note the stop |
| Ask the Lead Product Manager | a question recorded on the run, with the stop it concerns |

The release of what a finished run still holds stays as the one act that is not a decision on a stop.

### 3. Tracker progress fields are display only

An item's status of in progress and its assignee stay written, because people read them in the tracker. No reader decides anything from them:

- not the scheduler;
- not the readiness check;
- not the health observation;
- not a role's survey;
- not the claim audit.

They decide from the run records and the reservation, as point 1 of forward progress works out each item's state.

The harness writes the copy when it claims an item, and clears it on every way a run ends, as yoyodyne-819 builds. A difference between the copy and the run records is shown by the health observation as a problem the harness owns, and the harness writes the copy again. It is never treated as a state of the item.

### 4. Each store has one writer

Every record in the table above has one writer, or names each writer and why. No record holds a copy of what another record already holds, except the tracker's display fields under point 3.

The small records in the run state package, and the records known only by file name, are each folded into one of three places, or kept with a stated reason:

- the run record, if it describes one run;
- the follow-up store, if it describes something waiting;
- the pass record, if it describes a pass.

A record that holds only a copy of another is removed, and its readers read the original.

### 5. Waits inside a run stay on the run

The waits taken while a run's worker is live are the boundary's own retries. They stay on the run record and are not follow-ups, because the run is running and its worker owns them. A wait like that whose worker is gone becomes a stop outside the work, by step 1 of stopped run recovery, and is decided like any stop.

The two waits that release a developer slot each write a follow-up when they release it:

- the dependency wait, whose check is that the item's dependencies have closed;
- the wait to land, whose check is that the item has a run that landed on the target branch.

The item is then counted as waiting with a follow-up, and a wait that never ends comes back on its deadlines.

## The order of folding

Each step folds one special case into the general mechanism and removes the special path. A step lands only when the general mechanism it relies on is built. The inventory goes first, because folding a hold nobody has listed cannot be checked.

1. **The inventory** of every hold and wait known only by file name: what each holds, who writes it, who reads it, and whether it folds or stays with a reason.
2. **The brake on new work:** tripping writes its follow-up; the pass list, the bound of blocked trial runs and her release map onto the follow-up as point 1 says. Needs pieces 1 to 4 of follow-ups.
3. **The hold for a cause outside the work:** any check of its own is removed in favour of the environment check in the check runner. Needs piece 2 of follow-ups.
4. **The pause for an exhausted endpoint:** the pause writes its follow-up; the probe becomes how its check runs; the scheduler keeps no probe schedule of its own.
5. **The development manager's wait** becomes a follow-up; the sweep that reopens old waits is removed once none is left.
6. **The unresolved hold and the unknown result** become a wait that keeps the stop listed. The decision list loses its entry for an unresolved hold.
7. **The readiness check's hold** becomes parking.
8. **The tracker's display fields:** every reader moved off them, and the difference between copy and run records shown and repaired.
9. **Each hold the inventory found,** folded or kept with a reason, one a change.

## The test for any future fix

A new failure is handled through one of four existing lists:

- the recoverable failures of a boundary in recoverable and terminal failures;
- the stop causes of step 1 of stopped run recovery;
- the closed list of checks in follow-ups;
- the closed list of reasons a pull records for ready work it did not start, in forward progress.

A fix that adds an entry to one of those lists, in the design that owns the list and in the same decision, meets the test.

A fix that adds any of these needs an architect ruling before it is built, naming why none of the four lists fits:

- a store;
- a queue;
- a list on a surface;
- a schedule that checks something again;
- an overdue setting;
- a new decision.

A reviewer refuses a change that adds one without that ruling.

This test did not hold between October 24 and November 22. Most of the holds known only by file name were added in that time, and none came with a ruling. A rule in one design is delivered only to work that names that design; a change to the scheduler, a surface, the merge queue or a provider adapter can add its own wait while being correct in everything it set out to do. That is why the rule belongs in an invariant:

> Nothing waits outside the follow-up store. Every record that holds work, an item, a run or intake back until something becomes true, and that outlives the process that wrote it, is a follow-up with an owner, a check from the closed list, and deadlines. The only exceptions are the retries a live run's worker takes at a boundary, the run record's own saved step, the operator's pause file, and the landing hold.

The invariant is recorded once build piece 8 has listed every existing hold and wait, so that it can name every exception it allows. Recording it before then would have reviewers judging changes to waits nobody has listed. Until then this design and the reviewer's check hold it.

## Designs this changes

- **[Stopped run recovery](stopped-run-recovery.md):** step 3's list of decisions loses the unresolved hold, and the wait gains the choice of keeping the stop listed. Step 2 says a wait that keeps the stop listed takes nothing off the list, whoever recorded it. That revision follows this one.
- **[Fresh factory health and preserved run recovery](fresh-factory-health-and-preserved-run-recovery.md):**
  - the `run.recover` list loses the unresolved hold;
  - the hold on an unknown result is that wait;
  - `run.verify`'s expected state of an unresolved hold recorded becomes a wait recorded, naming whether it keeps the stop listed.
- **[Follow-ups](follow-ups.md):**
  - the brake's trip, the development manager's wait and the readiness check's parking join the acts that write a follow-up;
  - the follow-up record gains whether it keeps a stop listed;
  - the paced probe for an exhausted endpoint is named as how the check that a provider can be used again runs where there is no reset.
- **[Recoverable and terminal failures](recoverable-and-terminal-failures.md):** unchanged; the brake's count and trial spacing stay there.
- **[Forward progress](forward-progress.md):** unchanged; this design applies its states and its pull reasons.

## What is enforced in code

- A test lists every hold and wait in the inventory and fails where one can be in effect without an open follow-up, except the operator's pause file and the landing hold. It is the follow-ups test over every act that leaves something waiting, extended to the brake's trip, the endpoint pause, the development manager's wait, the dependency wait, the wait to land and the readiness check's parking.
- A test fails where any of the readers named in point 3 decides from an item's in progress status or assignee.
- A test fails where the closed list of decisions, the `run.recover` actions or the `run.verify` expected states disagree with one another.
- A test fails where a schedule outside the follow-up check runner checks a hold's cause again.

## Alternatives rejected

- **Keeping each hold's own check and folding only how they are shown.** One list on the surface would hide several schedules underneath, each of which can stop without anything noticing. That is the failure of the week before October 24.
- **A state machine stored for each item.** A stored state disagrees with the records it was copied from, as forward progress already rejects.
- **Removing the tracker's progress fields.** People read them in the tracker, and the tracker is the shared record between machines. They stay as a copy that nothing decides from.
- **Folding the waits a live run takes into follow-ups.** A live worker owns them and ends them within seconds or minutes. A follow-up for each would write constantly, and the run's own lease already makes them recoverable.
- **Keeping the unresolved hold as its own decision.** It names an owner, a next step and a time to look again, which is a follow-up. Two records for one meaning is the duplication this design removes.
- **Folding the landing hold into a follow-up.** No role in the harness could own it, and it already carries the deadline a follow-up would give it. A follow-up would be a second record of the same expiry.
- **Recording the invariant now.** It would have reviewers judge changes to holds nobody has listed, and its list of exceptions would be wrong the day it landed.
- **Making the test for future fixes a rule of this design only.** That was tried from October 24, and many new holds were added without a ruling. Only an invariant is delivered to work that does not name it.

## Build

These are the pieces; turning them into work items is the development manager's. Each removes a special path as well as moving it.

1. **The brake's follow-up:** tripping writes it with the trial run's landing as its check; her pass list, the bound of blocked trial runs and her release map onto it as point 1 says, and the special code for each is removed. Depends on pieces 1 to 4 of follow-ups.
2. **The check for a cause outside the work** checked against the check runner, and any loop of its own removed. Depends on piece 2 of follow-ups.
3. **The endpoint pause's follow-up:** the probe runs as its check, and the scheduler keeps no probe schedule. Depends on piece 2 of follow-ups, and on the fix holding dispatch for an exhausted endpoint (yoyodyne-ifd.435.10).
4. **The unresolved hold as a wait that keeps the stop listed,** across the decision list, `run.recover`, `run.verify` and the unknown result. Depends on piece 1 of follow-ups and slice 3 of stopped run recovery.
5. **The readiness check's hold as parking.** Depends on piece 3 of follow-ups.
6. **The dependency wait and the wait to land each write a follow-up.** Depends on piece 3 of follow-ups and piece 4 of forward progress.
7. **Readers off the tracker's progress fields,** the difference shown and repaired. Depends on yoyodyne-819.
8. **The inventory:** read each file named under holds and waits known only by file name, the hold for a cause outside the work, the two usage limit files, and the small records of the run state package; record for each what it holds, who writes and reads it, and whether it folds or stays with a stated reason. The result goes to the architect, who revises this design with it and then records the invariant. Depends on nothing, and goes first.
9. **The development manager's wait as a follow-up,** and the sweep that reopens old waits removed once none is left. Depends on piece 3 of follow-ups.
10. **Each hold piece 8 found,** folded or kept with its reason, one a change.
11. **The tests in the section on what is enforced.** Each goes in with the piece it covers.

Pieces 7 and 8 can start at once. Pieces 2 and 3 follow piece 2 of follow-ups, piece 1 follows piece 4, piece 10 follows piece 8, and the rest follow piece 3.

## What a reviewer can check

- No hold or wait that outlives its process exists without an open follow-up, except the operator's pause file and the landing hold.
- No schedule outside the follow-up check runner checks a hold's cause again.
- No reader decides anything from an item's in progress status or assignee.
- The decisions, `run.recover` actions and `run.verify` expected states are one list, and it has no entry for an unresolved hold.
- Each record in the stores table has one writer, or names each writer and why.
- A change that adds a store, queue, surface list, schedule that checks something again, overdue setting or decision carries an architect ruling naming why none of the four lists fits.
