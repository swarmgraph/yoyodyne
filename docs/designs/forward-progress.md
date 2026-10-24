---
id: forward-progress
kind: design
title: 'Forward progress: every admitted item moves or stops with an owner, slots fill when work and capacity exist, recovery ends, and the harness repairs within bounds when management cannot'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-24T21:03:17.504736Z
      reason: 'yoyodyne-ifd.428.112: the operator''s direction of October 24 for predictable forward progress. Every admitted item has one state worked out from records, and an item in none is a problem with an owner; slots fill when a place frees, every unstarted ready item has a recorded reason, and an empty slot beside ready work with no reason is filed as a bug; recovery records what failed and refuses a third use of the same act on the same failure; while the development manager is unavailable the harness takes five bounded acts already on the recovery tool''s list and hands them back. Names what stopped run recovery, the follow-up queue, the hold re-check and fresh factory health become, and the build pieces.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-24T21:03:17.504736Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1648, for document-1648.1 under the automatic approval policy
---
# Forward progress: every admitted item moves or stops with an owner, slots fill when work and capacity exist, recovery ends, and the harness repairs within bounds when management cannot

## What this is for

Serves the autonomy goal: the operator states intent and answers what only he can answer, and the line runs the rest. In the week before October 24 it did not. Ready work waited while developer slots stood empty:

- behind the hold on new work after the Xcode license was accepted, because nothing checked the hold's cause again;
- behind the brake on new work, waiting for a trial run nobody followed;
- behind confirmations nobody gave.

In each case every part was doing what it was built to do. Nothing said that an admitted item had stopped moving, and nothing acted when the role that should act could not.

The operator asked for a design that settles four things (yoyodyne-ifd.428.112):

1. every admitted item is always either moving or stopped with a named owner and a next step, and that is checked;
2. slots are filled whenever ready work and capacity exist;
3. recovery ends, rather than retrying the same failure, and the recovery machinery is sturdier than what it repairs;
4. a bounded repair path still works when the management roles are down or out of capacity.

This design settles each and names the build pieces. It adds no new store and no new list. It works out what already exists: run records, the follow-up store, the registry of waiting entries, the health observation and the recovery tool.

## What it rests on, and what was not read

Read whole at commit 4efea121:

- [stopped run recovery](stopped-run-recovery.md);
- [follow-ups](follow-ups.md);
- [fresh factory health and preserved run recovery](fresh-factory-health-and-preserved-run-recovery.md);
- [management and supervision](management-and-supervision.md);
- [recoverable and terminal failures](recoverable-and-terminal-failures.md);
- [capacity for heavy checks](heavy-check-capacity.md);
- the items for this design and for re-checking a hold whose cause a command can confirm (yoyodyne-ifd.432.25.19).

Not read:

- the scheduler's code, including how it selects work, the developer slots and their tags, and the brake on new work;
- the read model;
- the code of the recovery tool.

Where this design says how the build behaves today, it takes that from those designs and from tracker notes. A developer building a piece reads the code it changes first. Where the code does something this design does not account for, the piece stops and the architect is asked.

## Point 1: every admitted item has one state, and an item with none is a problem

### The states

Every open work item that is not a parent of open items has exactly one of the states below. The state is worked out from records each time it is read, and is never stored. A parent is counted through its children.

- **Running.** A run of the item has a live worker, by the one rule for identifying a worker in fresh factory health. Owner: the run's role.
- **Waiting for capacity.** The item is ready, nothing holds it, and it was not started at the last pull only because no place was free. A place is a developer slot whose provider can be used, or a permit for a heavy check. The state names which. Owner: nobody; the harness shows it and does nothing about it beyond starting the item when a place frees. An item waiting behind ready work of higher priority is in this state too, with the work ahead of it counted.
- **Waiting with a follow-up.** The item, or its stopped run, waits on something, and an open follow-up from [follow-ups](follow-ups.md) records it. That covers:
  - an item that depends on another item;
  - a parked item;
  - a run's dependency wait;
  - a wait decided on a stop;
  - a hold on new work;
  - a question to another role or to the operator.

  Owner: the follow-up's owner, with its check and deadlines.
- **Stopped.** A stop of its run is on the development manager's list, as step 2 of stopped run recovery says. Owner: the development manager.
- **Waiting on its conversation.** The item is done by a role's conversation rather than a developer run. Owner: that role. The item gets a follow-up when it is admitted, with the check that the item is closed and deadlines from its priority, so it comes back to its owner like any other wait.

An item can look as though it fits two states, such as a stopped run whose stop has a wait recorded. The one that applies is the first of these that fits: stopped, then waiting with a follow-up, then running, then waiting for capacity. Under step 2 of stopped run recovery a wait already takes the stop off the list, so the two never both apply.

### The check

The read model works out the state of every admitted item. The health observation reports an item that fits none of them as a problem: no recorded way forward, owned by the development manager. It names:

- the item, by title and identifier;
- its tracker status;
- the records that were read.

This check runs while the factory runs, not only in a test. A new kind of wait that some later change records outside the follow-up store, or a parked item that lost its follow-up, is found the next time anyone observes. A test cannot know about a kind of wait nobody has built yet.

The counts in each state are shown on `yoyo status` and on the dashboard, in that order, with the oldest entry of each.

## Point 2: slots fill whenever ready work and capacity exist

### Starting work when a place frees

The scheduler pulls new work on its regular interval, as today. It also pulls within one minute of any of these:

- a developer slot freeing;
- a permit for a heavy check freeing, where a ready item is waiting for it;
- a hold on new work being released;
- an item becoming ready, such as the item it depended on closing;
- a provider becoming usable again.

The pull itself is unchanged: the same order, which is the Lead Product Manager's to set, and the same checks before an item is started.

### Recording why ready work was not started

At every pull, the scheduler records, for each ready item it did not start, the one reason it did not. The reason is chosen from a closed list kept in the harness's code:

- no free slot;
- no free slot whose tag accepts the item;
- the slot's provider cannot be used;
- the hold on new work;
- the operator's pause, set by `yoyo pause`;
- the item is reserved by another run;
- the readiness check is holding it;
- a run of the item already exists;
- work of higher priority took the free places.

The record is kept with the pull, in the scheduler's own records, and the health observation reads the latest one. Nothing else is recorded for an item that was started.

Every reason that is a hold or a pause already has its follow-up under [follow-ups](follow-ups.md). A hold that keeps every slot empty counts as priority 0 there, whatever it is about, so its first deadline is one hour.

### An empty slot beside ready work is a defect

A developer slot whose provider can be used, left empty for ten minutes while at least one ready item it accepts was not started, is a problem: a slot empty beside ready work. That holds unless the recorded reason is a hold or a pause with an open follow-up. Where the latest pull recorded no reason, or recorded only a reason that does not apply to that slot, the harness files a reliability bug at priority 0, as the operating rule on reliability defects says. It files through the same intake as any filing, with its origin recorded as the harness. While that bug is open, a later occurrence is added to it as a note rather than filed again. The ten minutes is `execution.idle_slot_after`, which the development manager sets under section 6 of [portable agent configuration](portable-agent-configuration.md).

### Capacity is counted where it is, and only there

- A slot whose provider cannot be used, by the pause on an exhausted provider endpoint (yoyodyne-ifd.435.10) or a login that has lapsed, is not a free slot. It is shown as a slot without capacity, and the items it would take are waiting for capacity, not neglected.
- A run waiting for a permit for a heavy check keeps its developer slot, as [capacity for heavy checks](heavy-check-capacity.md) says, and is running rather than waiting for capacity, because its worker is live.
- **A run waiting only to land holds no developer slot.** That is a run whose change is approved and whose remaining steps are the landing and confirming its merge. Those steps are the harness's own under the target branch's landing lease, and a developer slot taken by them keeps a developer from new work while nothing is being developed. The slot is released when the run reaches that step, as a dependency wait releases it. The item stays reserved against a second run. If the run is sent back to its developer, by a conflict the developer can resolve or by checks before landing failing, it takes a slot again like a decided repair, ahead of fresh work of the same priority.

### Landing is coordinated, and visible when it is not

One promotion per target branch and the merge queue, where it is switched on, decide the order changes land. This design adds only that an approved change that has waited more than one hour to land is shown on `yoyo status` and in the dashboard's section for what is broken, stalled, blocked or degraded. It shows what it waits behind and who owns that, so a landing that has stopped is never seen only as empty slots.

## Point 3: recovery ends

### What failed is recorded, so a repeat can be recognised

Step 1 of stopped run recovery already records the cause on every stop. Each stop now also records what failed, as four things together:

- the cause, from step 1's list;
- the step the run was at;
- the failing check, or the failing tests where they can be named with their files, or the kind of provider error;
- the item.

Two stops are the same failure when all four match. Nothing else, such as the time, the run, or the wording of an error message, enters the comparison, so a failure that recurs is recognised even when its output differs.

### The same act is not taken on the same failure again and again

- The recovery tool refuses a decision whose act was already carried out twice on the same failure of the same item. The refusal names the two earlier stops and the acts taken on them. The acts counted are continuing the same run, a repair, and a rerun, whether the development manager decided them or the harness took them on its own. The development manager then takes a different decision: another kind of act, giving the item back, an unresolved hold, or asking the Lead Product Manager.
- A rerun that stops with the same failure as the run it replaced shows the failure is not about that run. The second such rerun is refused by the rule above, and the item is given back or rewritten.
- The limits on repair attempts and on raising them stay as stopped run recovery sets them. This rule bounds how often one act can be taken on one failure inside those limits, so an item cannot spend its whole allowance on the same move.

### The same failure across items is one problem

The same failure, matching on everything but the item, on three or more items within twenty four hours is shown once as one problem. It names the items, the failing check or tests and the first time it was seen, and is owned by the development manager. While it is shown, the harness takes no act of its own under point 4 on a stop with that failure, because three items failing the same way is most likely the harness or the target branch, which no act on one run will mend. A failure that also fails on the base commit is already filed as a warning report under [recoverable and terminal failures](recoverable-and-terminal-failures.md), and is shown once, with that report beside it.

### Recovery is sturdier than what it repairs

These hold for every part that recovers runs: the list of stopped runs, the recovery tool, the follow-up checks, the hold's own re-checks, and the harness's own acts under point 4.

- **Carrying out needs no model.** Deciding a stop may need the development manager. Carrying the decision out, checking its effect with `run.verify`, running a follow-up's check and taking the harness's own acts never call a provider. They therefore keep working while a provider's usage limit is reached, as [management and supervision](management-and-supervision.md) already requires of detecting that nothing is running.
- **One bad record does not stop recovery.** A run record that cannot be read is listed as unreadable and the rest are read. A record the reading build does not fully understand is read with the unknown values marked, not refused. This is what the items on one unreadable run record not failing a listing (yoyodyne-6d1) and on run records read tolerantly (yoyodyne-d56) build.
- **A busy tracker is waited out, never charged.** A tracker read or write that times out inside a recovery act is retried within the tracker boundary's window under recoverable and terminal failures. A recovery act that could not finish because of it spends no rerun and no attempt, and is put back to be carried out, as the item on recoveries refused for a tracker timeout (yoyodyne-ifd.428.99.17) does once.
- **Recovery runs in the watch loop.** The recovery parts run as steps of the scheduler's watch loop, under the supervisor, as the dispatchers do. A recovery part that fails is restarted with that loop and, past its bound, shown as a degraded part of the harness, owned by the development manager. It never stops unrelated work from being started.
- **A failure of recovery is shown, not retried in a loop.** A recovery act whose result is unknown puts the unresolved hold on the run that fresh factory health already requires. A follow-up check that cannot run three times in a row is shown as degraded, as [follow-ups](follow-ups.md) says.

## Point 4: the harness repairs within bounds when management cannot

### When the development manager counts as unavailable

The harness decides this from its own records, with no model. The development manager is unavailable while any of these is true:

- her scheduled pass has not completed for twice its scheduled interval, and for at least one hour;
- every provider endpoint her conversation may use cannot be used, by the provider's usage limit, an exhausted endpoint or a lapsed login;
- her last three scheduled passes each ended without completing.

She is available again when one of her passes completes. Whether she is unavailable, since when and why is shown on `yoyo status` and on the dashboard.

The same test applies to the other management roles and is shown for each. Only the development manager's unavailability lets the harness act on its own, because only her decisions keep runs moving. The Lead Product Manager's or the architect's unavailability leaves their follow-ups to lapse and escalate as [follow-ups](follow-ups.md) says.

### What the harness does on its own, and nothing else

While the development manager is unavailable, the harness takes these acts by fixed rules. Each is already on the recovery tool's closed list of actions. Stopped run recovery already lets the harness continue a run and confirm a merge by itself.

1. **Continue a run whose stop is outside the work, once the cause has demonstrably cleared.** That is a stop for:
   - a provider's usage limit, once its reset has passed;
   - a provider that could not be reached, or a session that ended after its retries, once a check of the provider succeeds;
   - a lapsed login, once the provider check succeeds;
   - a cause with an environment check the harness supplies under [follow-ups](follow-ups.md), once that check passes;
   - a restart onto a new build that the watch session could not pick up, once the new build is running.

   Never for a check stopped by its time limit whose reruns are spent, for a failure that also fails on the base commit, for a merge the forge refused, or for any stop about the work.
2. **Release a hold whose follow-up check has passed,** as [follow-ups](follow-ups.md) already says. This is the hold re-check (yoyodyne-ifd.432.25.19), and it runs whether or not the development manager is available.
3. **Finish a merge** for a run recorded as merged, or whose merge is uncertain, by step 8 of stopped run recovery. That means reading the target branch, confirming the merge when the commit is there, and completing the steps after it. Where the commit is not there, the harness takes no further act and records a wait for her.
4. **Release what a finished run with no worker still holds:** its developer slot, the item's claim or its file reservations, by the release action in fresh factory health.
5. **Record a wait** on every other stop that reaches her list. Its follow-up names her, and its check is that the stop has left the list, so the stop is on her list with its deadlines the moment she is back.

The harness never, on its own:

- repairs, reruns or gives an item back;
- raises a limit on attempts;
- asks the Lead Product Manager on her behalf;
- changes a priority or admits work;
- cancels a run;
- lifts a hold the development manager decided, except by its follow-up's check passing;
- lifts the operator's own pause.

Those are judgments, and they wait for the role whose judgment they are.

### The bounds

- At most one act of the harness's own on any stop.
- An automatic continue that stops again with the same failure is not continued automatically again. The second stop gets a recorded wait for the development manager.
- At most four automatic continues an hour across the product, set by `execution.harness_recovery_per_hour`, which the development manager sets under section 6 of portable agent configuration. Past it, further stops get a recorded wait.
- No act on a stop whose failure is shown as the same failure across items, as point 3 says.
- Every act goes through the recovery tool exactly as hers does. It is bound to an observation taken within fifteen minutes, with the run's expected state and the stop. It is recorded before and after with the harness as the one who acted and the reason "the development manager has been unavailable since" the time, and checked with `run.verify`. The hold on new work, the operator's pause and the pause for provider capacity gate it as they gate any act that starts a provider.

### Handing back

When the development manager's next pass completes, it opens with every act the harness took on its own since her last completed pass. For each it gives the stop, the act, the reason and the `run.verify` result. Stops that got a recorded wait are on her list as usual. She decides those stops as she would any other; the acts already taken are not undone, and a stop that came back after one is a new stop.

The Lead Product Manager's summary and the operator's regular summary show how long the development manager was unavailable and how many acts the harness took. Neither is asked anything about it. Where the development manager stays unavailable past four hours, the second deadline for anything holding every slot, the follow-ups she owns are put to the Lead Product Manager as [follow-ups](follow-ups.md) says. The Lead Product Manager can admit work on the failing conversation; she still does not decide stops.

## What each existing piece becomes

- **Stopped run recovery (yoyodyne-ifd.428.99):**
  - step 1 records what failed on each stop;
  - step 3 gains the refusal of a third use of the same act on the same failure;
  - step 5 gains the harness's automatic continue under point 4, within its bounds.

  Its rules, closed list of decisions and slices otherwise stand. That revision follows this one, together with the revision [follow-ups](follow-ups.md) already promised for it.
- **The follow-up queue (yoyodyne-ifd.432.25.20)** is what gives every waiting state in point 1 an owner, a check and deadlines. It stands as designed. Its first piece is a prerequisite for this design's first piece.
- **The hold re-check (yoyodyne-ifd.432.25.19)** is piece 2 of follow-ups, and act 2 under point 4. Nothing is added to it.
- **Fresh factory health:**
  - `health.observe` reports the state of every admitted item, the problem of an item in no state, a slot empty beside ready work, and whether each management role is unavailable;
  - `run.recover` accepts the harness as the one who acts, for the five acts of point 4 only;
  - `run.verify` is unchanged.

  That revision follows this one.
- **Management and supervision:** unchanged. The harness's own acts run in the watch loop it already describes.
- **Capacity for heavy checks and recoverable and terminal failures:** unchanged; this design applies them.
- **Reviewing lifecycle states and recovery paths so there are fewer of them (yoyodyne-ifd.428.113)** starts from the states in point 1, which are the whole set of ways an admitted item can stand.

## What is enforced in code, and what is not an invariant

- A test builds an item in each state and checks that the read model gives it exactly that state. It also builds one in none, a parked item with no follow-up, and checks that the health observation reports it.
- A test fails where a pull leaves a ready item unstarted without recording a reason from the closed list.
- A test holds a slot with capacity empty beside a ready item it accepts for the configured time with no reason recorded, and checks that one bug is filed and that a second occurrence is added to it as a note.
- A test refuses a third use of the same act on the same failure of one item.
- A test makes the development manager unavailable and checks that the harness takes each of the five acts within their bounds and no other act. That includes one continue for one cleared stop, a wait for a stop about the work, no act on a failure shown across items, and a wait past four continues in an hour.
- A test runs the recovery parts with every provider refusing calls and checks that carrying out, `run.verify`, follow-up checks and the harness's own acts all still work.

No invariant is added. Every rule here goes through the read model, the scheduler's pull, the recovery tool or the follow-up store, and their tests hold it. Point 1's check is made while the factory runs, which catches a wait recorded somewhere none of them reaches.

## Alternatives rejected

- **Storing each item's state.** A stored state disagrees with the records it was copied from the first time one of them changes without it. That is how items sat in the tracker as in progress with no run.
- **Letting the harness repair or rerun when the development manager is unavailable.** A repair spends the item's allowance and a rerun discards a run's place, and both are judgments about the change. The harness can tell that a usage limit has reset. It cannot tell whether a failing check is worth another attempt.
- **Leaving everything to wait for the development manager.** The factory's week of empty slots was largely stops whose cause had already cleared. Waiting for a role that cannot run keeps the factory idle for the whole time she is down.
- **Asking the operator when the development manager is unavailable.** Nothing here is on his closed list. Routing routine recovery to him is what the autonomy goal refuses.
- **A watchdog that asks a model whether the factory is stuck.** It stops at a usage limit, exactly when it is needed, as management and supervision already rules out.
- **An unlimited number of the same act on the same failure inside the item's allowance.** That is the loop this design exists to end. Two is enough to rule out bad luck.
- **Keeping a developer slot for a run waiting to land.** No developer is working while the harness lands a change, and on a busy afternoon several slots sat empty this way while ready work waited.

## Build

These are the pieces; turning them into work items is the development manager's.

1. **Each item's state, and the problem of an item in none.** The read model works out the state of every admitted item. `health.observe` reports an item in no state, with the counts on `yoyo status` and on the dashboard. Items done by a conversation get their follow-up when admitted. Depends on piece 1 of [follow-ups](follow-ups.md).
2. **Why ready work was not started.** The closed list of reasons, recorded at each pull, the problem of a slot empty beside ready work, and the bug filed or noted. Depends on nothing new.
3. **Pulling when a place frees.** The pull within one minute of each event listed in point 2. Depends on nothing new.
4. **A run waiting only to land holds no developer slot,** and takes one again ahead of fresh work if sent back to its developer. The developer reads the scheduler first. If the build already releases the slot, this piece is the test alone. Depends on nothing new.
5. **What failed, recorded on each stop, and the limit on repeating an act.** The four things recorded, the refusal of a third use of the same act on the same failure, and the problem of the same failure across items. Depends on slices 1 and 3 of stopped run recovery.
6. **When a management role is unavailable.** Worked out from pass records and endpoint state, shown for each role. Depends on nothing new.
7. **The harness's own acts.** The five acts and their bounds, the recovery tool accepting the harness as the one who acts for those five, and the hand back at the next completed pass. Depends on pieces 5 and 6, on slice 3 of fresh factory health, and on piece 2 of follow-ups.
8. **The landing that has waited more than one hour,** shown with what it waits behind and who owns that. Depends on nothing new.

Pieces 2, 3, 4, 6 and 8 can start at once. Piece 1 waits on the follow-up store, piece 5 on the stopped run recovery slices it names, and piece 7 comes last.

## What a reviewer can check

- Every admitted item that is not a parent shows exactly one state, and an item in none is reported with its owner.
- No pull leaves a ready item unstarted without a recorded reason from the closed list.
- A slot with capacity left empty beside ready work it accepts, for the configured time with no reason, files one bug and notes repeats on it.
- A run waiting only to land holds no developer slot.
- No act is taken a third time on the same failure of the same item.
- While the development manager is unavailable, the harness takes only the five acts, at most one on any stop and at most the configured number of continues an hour, each bound, recorded and checked like hers, and none on a failure shown across items.
- Every act the harness took on its own is shown at the start of her next completed pass.
- No recovery part calls a provider in order to carry out, check or take its own acts.
