---
id: stopped-run-recovery
kind: design
title: 'Stopped-run recovery: every step a stopped run takes, what each reads and writes, and what none may do'
supports:
    - v1-goals
    - fresh-factory-health-and-preserved-run-recovery
    - recoverable-and-terminal-failures
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-08T13:48:06.947303Z
      reason: 'yoyodyne-gtx - the operator''s direction of October 8: one design for every step a stopped run takes, with what each step reads, writes and must never do, the rules all steps share, the builds each with a test from start to finish, and the open items it covers; absorbs yoyodyne-ifd.429.42 and yoyodyne-ifd.437.38 and the recovery-contract half of yoyodyne-ab2'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-08T13:48:06.947303Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 609, for document-609.1 under the automatic approval policy
---
# Stopped-run recovery

## What this is for

This design serves the autonomy goal: a run should end for a reason about the work, and a run that stops should reach somebody who can decide about it and then be acted on without a person typing a command. It was asked for by the operator on October 8 (yoyodyne-gtx). An analysis of the 479 items filed between September 23 and October 7 found that about a third of them, 160, concerned what happens to a run after it stops, and that the mistakes came in chains where each fix needed another fix: yoyodyne-ifd.428.29, then .38, then .61, then .71; and yoyodyne-ifd.428.46, then yoyodyne-edi, then yoyodyne-8ff, then yoyodyne-8w1.

The code that handles a stopped run is spread over about thirty files in `internal/orchestrator` and over `internal/triage`. Each file states, in its own comments, rules it learned from an incident: ask everything that can refuse before spending anything, give back what was spent on a run that never happened, never let a refusal go unrecorded. The rules are right. They are written separately in each file, and a new path that does not repeat them breaks them. This design states them once, says which step owns which record, and orders the changes that make one shared piece of code enforce them.

It starts where two existing designs stop:

- [Fresh factory health and preserved-run recovery](fresh-factory-health-and-preserved-run-recovery.md) owns the shared account of what state a run is in (a live worker, an uncertain one, a wait, a preserved run with no worker, an integrated run, an ended one) and the rule for continuing the same run after a redeploy or a dependency wait. This design takes that account as given and covers what happens once a run has stopped and is put in front of the development manager.
- [Recoverable and terminal failures](recoverable-and-terminal-failures.md) owns the three classes of failure at each outside boundary (recoverable, an answer, ambiguous) and what a retry may never do. That stays as it is, except for the one passage on check timeouts amended below.

## The steps, in order

A stopped run goes through these steps. Not every run takes every step; the order is fixed.

| Step | What happens | Where it is done |
| --- | --- | --- |
| 1. Recorded as stopped | The run ends and its record says why | `pipeline.go` (`stop`, `fail`), `reconcile.go` (`settle`) |
| 2. Listed | The stoppage is put on the docket and shown to the development manager | `triage.go` (`Docketer`), `internal/triage/window.go` |
| 3. Decided | The development manager records a decision in her own conversation | the item's triage record |
| 4. Carried out | The harness finds decisions not yet acted on and hands each to the action for it | `carryout.go` |
| 5. Acted on | A fresh run (`rerun`), a continued repair (`repair`), an armed merge (`rearm`), or one of the harness's own continuations | `rerun.go`, `repaircontinue.go`, `carryrearm.go` and `rearm.go`, `checkstagecontinue.go`, `stallcontinue.go`, `reconcile.go` |
| 6. Reconciled | A run whose process is gone is settled from its record and what the repository shows | `reconcile.go` |
| 7. Taken off the list | A decision closes the entry, the item closes, or the harness settles a publication | `triage.go` (`Close`, `SettleClosedItems`, `SettlePublication`) |

## Which record owns what

Every fact about a stopped run lives in exactly one record, and every step reads it from there rather than keeping its own copy.

- **The run record** (`runstate.State`) owns the run's status, phase, blocker, repair attempts, continuations, the session it ran in, and what it left behind: branch, worktree, and publication.
- **The docket** owns that work stopped. It is a log: an entry is written once, keyed by the class of event and the run, and never rewritten. A closure is a separate record joined to the entry when the docket is read.
- **The item's triage record** (`runstate.TriageStore`) owns every decision, the budgets each decision spends, and every finding about a decision the harness tried to carry out and could not, or did not try within one poll interval.
- **The claims of fresh runs** (`runstate.RerunStore`) own that a `rerun` decision has been acted on: one claim for each docketed stoppage.
- **The tracker item** owns the item's status and notes. Only what a person reads is written there; no step decides anything from the notes.

A listing read at the start of a pass is a snapshot. No step changes a run on the strength of a snapshot: it takes the run's lease and reads the record again under it.

## The rules every step keeps

These ten rules hold for every step and every action. Each is already true of some code read for this design; the builds below make them true of all of it.

1. **A decision is read, never given.** The development manager's decision, the reasoning she recorded, and the budget it spent are read from the item's triage record. No command or pass hands an action words to attribute to her. A stoppage with no recorded decision is refused, naming the missing record.
2. **Ask everything that can refuse before writing or spending anything.** The order is: the decision and what is left of it; that the run has ended; that the worktree is as the harness left it and still holds the change, where the step continues that change; the item's own state; the operator's pause and the intake hold; developer capacity last, because it is the condition most likely to change while the others are asked.
3. **Three kinds of not now, never confused.** A step that does not go ahead says which of three it met. *Waiting*: a gate shut for everything at once, such as the operator's pause, the intake hold, or a full harness, which clears without anybody acting; nothing is spent and the next pass tries again at once. *Refused for this item*: a gate shut for one item, such as a directive pausing it, unfinished work it depends on, or a worktree somebody has been in; nothing is spent and later attempts are spaced out so the item cannot take a slot every poll. *Refused for good*: the decision cannot be carried out as it stands; it is not tried again until the decision changes.
4. **Spend only on something that ran.** A budget, a claim, or a repair attempt is spent only where an agent of the run was invoked and something judged or produced work. Where the action provably started nothing, what it took is given back: the slot went to another run first, a pause was met before the run was reserved, any refusal came before the reservation, the machine refused the run before any agent ran, or the provider's usage window stopped it before anything was judged. A run that did something keeps its claim, whatever it came to.
5. **Writes in a fixed order, each read back.** Each action writes its records in one stated order and confirms each by reading it back before the next. A repair continuation writes: the item put back, the run's continuation, the success note on the item, the dispatch, and the record that the dispatch was accepted. An action interrupted part way reads every record it may have written before repeating any write, so nothing is charged twice. A save that reports an uncertain result is read back under the lease, as the fresh-health design rules.
6. **Every change to a stopped run is made under its lease.** Reconciliation, a repair continuation, and the record of a handed-back publication each take the run's lease first. A run a live process holds is left to that process.
7. **Nothing is silent.** Every refusal is written onto the item's triage record with the gate, what it said, and what would clear it. A decision no pass has attempted within one poll interval of being recorded is written there too, with why. The docket entry the development manager reads carries the finding. The finding is cleared at the moment the action actually starts, whichever hand started it.
8. **The same stoppage is listed once, and a new one is listed again.** One run has one live entry, with any others about it folded beneath. A run that stops again after a decision about its last stoppage is listed again, measured by the run's own ending rather than the clock of whoever scans.
9. **Never a second developer for one item.** No step starts a developer for an item while any run of that item is in flight, and reconciliation never invokes a provider except through the run's own continuation, named and adopted.
10. **Preserved work is never discarded by recovery.** A stopped run's branch, worktree, and publication are kept while any decision could still need them. They are retired only after a fresh run of the item has integrated, or the item has closed, and a worktree is removed only after its uncommitted work is recorded on a ref kept for that run.

## Step 1: recorded as stopped

**Reads:** the run's own record and the error that ended it.

**Writes:** the run's status, phase, stop class, any outside cause, the blocker, and the failure notes on the item; then the docket entry, through `RecordStoppedRun`, `RecordUnstartedRun`, or `RecordEscalation`.

**Never:** records a stop the harness caused, such as a stall, a time limit, or a usage window, as a judgment on the work; writes a docket entry for a run that ended for a reason nobody has to decide about.

A run reaches this step from the pipeline as it ends, or from reconciliation when its process is gone. Both call the same docketing, and the entry is keyed by event so the two produce one entry between them.

## Step 2: listed

**Reads:** every run record, the docket log, each item's triage record, and the claims of fresh runs, joined at the moment the docket is built. The repository is asked again what each run left behind.

**Writes:** new entries only, each once. Nothing already on the log is changed.

**Never:** shows an entry on a closed item as a question; shows a decided entry as a question unless the harness tried to carry the decision out and was refused; drops an entry from the bound without counting it.

The window the development manager is given shows only live entries, one for each run, oldest stoppage first. Critical entries come ahead of the walk. Stoppages nobody has decided come before decided ones the harness was refused carrying out, and stoppages she chose to wait on come last until the wait runs out. A durable position lets the next pass start where this one stopped.

## Step 3: decided

**Reads:** the docket entry with its joined record.

**Writes:** one decision on the item's triage record, with its reasoning, the run it names, and the budget it spends, in one write. The decisions are `repair`, `rerun`, `rearm`, `wait`, `rescope`, `escalate`, and `stop`.

**Never:** records a decision past the item's caps without the operator's recorded override; records a `repair` on a raise, which has no stopped run to continue.

Only `repair`, `rerun`, and `rearm` are carried out by the harness. The others ask for no run, and their entries leave the list on closure.

## Step 4: carried out

**Reads:** the docket, the triage record, the claims of fresh runs, every run record (only where a `repair` decision stands), and the operator's pause.

**Writes:** a refusal finding or an unattempted finding on the triage record, and nothing else.

**Never:** decides, spends, or grants anything; starts an action while the operator's pause stands; takes a decision about an item with a run of it already in flight, except a continuation of that same run.

Each pass lists the decisions not yet acted on and starts as many as there are free developer slots, each counted against capacity like any chosen item. A decision whose stopped run still holds a branch or worktree goes ahead of fresh work of any priority, and the run's reason names the work it went ahead of; this is the rule the decided-recovery amendment asked for (yoyodyne-ifd.437.38), and it is already built (`CarryOutTask.Preserved`, `AheadOf`). The harness's own continuations, a stage stopped under load and a first stall, are started here too, without any decision of hers, and are never written onto her record as decisions.

## Step 5: acted on

### A fresh run (`rerun`)

**Reads:** the docket entry, or the key the docket would have given an undocketed run; the stopped run's record; what the repository holds now; the triage record and claims; the item; human gates; intake; capacity.

**Writes:** where the stopped run left the item claimed with nothing in flight, it releases that claim with a note. Then one claim for the stoppage, the fresh run, and after it ends the disposition of what the stopped run left.

**Never:** runs a stoppage a second time on one decision; runs a raise before its owner releases the parking; starts while anything of the stopped run is still resumable.

### A continued repair (`repair`)

**Reads:** the stopped run under its lease; what the repository holds; the worktree's ownership and that it still holds the change; the grant left; the item; intake; capacity.

**Writes:** where the checkout is missing, it is restored after every gate passes, with verification credit cleared first. Then, in this order: the item put back with a note, the run's continuation superseding its blocker, the success note, the dispatch, and the record that the dispatch was accepted.

**Never:** continues a run whose worktree somebody has been in, or one that holds none of the change, except a stall in the middle of an attempt; continues a run on the record of another item; starts a fresh run in its place.

### An armed merge (`rearm`)

Carried out on its own path, without a developer slot. It follows rules 1 to 7, and its refusals are shown on the docket like any other (yoyodyne-edi).

### The harness's own continuations

A stage stopped under load, a first silent stall, a usage wait whose deadline has passed with no process serving it, and a queued head brought up to date. Each continues the same run, in its own worktree and session. Each is started without a decision of hers and gives way to one if she records it. The check stage continuation is bounded by the allowance under *Check time limits* below.

## Step 6: reconciled

**Reads:** every outstanding run, each under its lease; what the repository and the forge show; stop requests; the operator's pause.

**Writes:** the run's terminal state, a blocker on the item where a person must decide, and the docket entry; or completion of an integrated run's remaining steps.

**Never:** invokes a provider, except the run's own continuation through the sweep verb; creates a worktree; promotes a change; moves a target branch except onto a remote commit that already contains it; settles a run a live process holds.

A run parked on something nothing but its process continues is settled as stopped once its record has not moved for the grace (`DefaultVanishedGrace`), so it reaches step 2 instead of holding a slot. A run waiting on its dependencies is left for the next pull. A run paused by the operator is left untouched.

## Step 7: taken off the list

A decision closes its entry. An item closing closes every entry standing for it, except an unfinished publication. The harness closes a publication entry once a sweep finishes that publication, and closes the stopped run's entry with it only where the settlement cleared a blocker about that publication. Entries stay on the log; the closure is joined when the docket is read.

## Check time limits

This replaces the recovery design's sentence that "a check the timeout ended is a failed check", for the reason yoyodyne-ifd.429.42 gave.

- A check ends **failed**, **unfinished**, or **passed**. A check stopped by its time limit is unfinished. It earns no credit, its output is kept, and it is never recorded as failed or handed to the developer as a failure.
- An unfinished check is continued only where the run's record shows the machine was loaded when the limit was reached. Each run has one allowance, recorded on the run before it is used: two continuations at most, and in total no more than three times the check's own limit.
- When the allowance is spent, the run stops for a reason outside the work. It is listed for the development manager, no repair attempt or review round is charged, and nothing counts toward the failure brake. A restarted process finds the allowance as spent as it was.

Per-check limits that scale with load (yoyodyne-ifd.429.41) set the limit this allowance is measured against.

## What is enforced in code

- One shared piece of code that every action in step 5 goes through to ask its gates in rule 2's order, to report which of rule 3's three kinds it met, and to give back what rule 4 says must be given back. No action decides those for itself.
- The fixed write order and reading back of rule 5, with a test that stops the process between each pair of writes.
- The lease of rule 6, taken before any change to a stopped run.
- The findings of rule 7, written by the shared code rather than by each caller.

Rule 4 binds work that never mentions it: a new way of acting on a stopped run that spends before it knows something ran would be correct on its own terms and still break it. It should become an invariant once the shared code exists to enforce it; it is recorded here so the build can refer to it.

## The builds this needs

Each build ends with a test that walks its step from start to finish through the fakes in `internal/orchestrator/orchestratortest`. How they are split and ordered is the development manager's.

1. **The shared gate code.** The code above, adopted by the fresh run, the continued repair, the armed merge, and the harness's own continuations. Test: for each action, each of rule 4's give-back cases leaves the budget and claim as they were, and a run that did something keeps its claim.
2. **Unfinished checks and the continuation allowance.** As specified under *Check time limits*. Test: a check stopped under recorded load is continued twice, then the run stops outside the work with nothing charged, and a restart finds the allowance spent.
3. **One walk for each path.** Stopped, listed, decided, carried out, acted on, reconciled, and taken off the list, for each of `rerun`, `repair`, `rearm`, and the check stage continuation. Test: each walk ends with the entry closed, the right records written, and nothing else changed.
4. **Interrupted actions.** For the fresh run and the continued repair, stop the process after each write in rule 5's order. Test: the next attempt reads before writing, charges nothing twice, and finishes or reports the same state.
5. **Stopped again.** A run continued on a `repair` decision that stops again is listed again as a new stoppage, and the decision already carried out is never shown as waiting. Test: as stated. This is the build yoyodyne-ifd.428.74 asks for.

## Open items this covers

The open queue was read on October 8, and the listing was cut after 36 of 222 items; the items below are the ones read. Any other open item about these steps is named in the next revision of this design. Until then the Lead Product Manager's hold on new piecemeal fixes in this area stands.

- **Stays as is, built to this design:** a run that stops again after a decided repair is classified afresh (yoyodyne-ifd.428.74), which is build 5.
- **Stays as is, owned by the fresh-health design:** recovering the preserved redeploy-stopped run (yoyodyne-9j7); preserved-run recovery the development manager can call, and reconciling workerless redeploy stops (yoyodyne-06m); shared factory-health evidence (yoyodyne-e7c); validating accountable manager passes (yoyodyne-5eu). Each follows rules 4 to 6 where it acts on a stopped run.
- **Stays as is, owned by the recovery design:** a tracker write that outlasts its bound is checked and retried (yoyodyne-ifd.433.22); per-check limits that scale with load (yoyodyne-ifd.429.41).
- **Absorbed here:** the recovery-policy reconciliation for check timeouts (yoyodyne-ifd.429.42), stated under *Check time limits*; decided recovery ahead of fresh pulls (yoyodyne-ifd.437.38), stated under step 4; the recovery-contract half of the prompt and recovery addendum (yoyodyne-ab2), whose actions the development manager calls are the actions of step 5 under these rules. The prompt rules in that addendum stay with it.
- **Outside this design:** retrying a document run stopped without being judged (yoyodyne-ifd.433.21.2), which concerns document publication rather than a developer run.

## What this amends

- **Recoverable and terminal failures:** the sentence in *The checks and the review* that a check the timeout ended is a failed check is replaced by *Check time limits* above, and a line is added naming this design as the account of what happens after a run stops.
- **Fresh factory health and preserved-run recovery:** nothing changes. This design starts where its shared account ends.
- **No invariant changes in this revision.** Rule 4 is proposed as one once build 1 has landed.

## Alternatives rejected

- **Keep fixing each file as incidents arise.** That is how the chains of fixes on fixes came about: each file learned the same rules separately, and a path that had not learned them yet broke them.
- **One process that owns every stopped run from start to finish.** It would replace machinery that mostly works, and it would hold state the records already hold, which the fresh-health design rules out.
- **Spend the claim after the run instead of before it.** A process that dies between the run and the claim would then have run the item again with nothing recorded, and the next pass would run it a third time. Claiming first and giving back when nothing ran is safer.

## Evidence and limits

Read at commit c9cd4c259d66: the two designs above in full; `internal/orchestrator/README.md` and `internal/triage/window.go` in full; the first 48 KiB of `rerun.go`, `carryout.go`, `repaircontinue.go`, `triage.go`, and `reconcile.go`. Not read: the rest of those files, `carryrearm.go`, `rearm.go`, `checkstagecontinue.go`, `stallcontinue.go`, `schedulecontinue.go`, `integrationresume.go`, `checkoutrecovery.go`, and the stop path in `pipeline.go`. The statements above about those files rest on the code map and the callers that were read. No tests were run.
