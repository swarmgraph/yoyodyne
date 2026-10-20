---
id: stopped-run-recovery
kind: design
title: 'Stopped run recovery: every step from the stop to the run going on, and what each step may never do'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-15T09:22:49.968988Z
      reason: 'yoyodyne-gtx: the one design for stopped run recovery the operator asked for on October 8, written again whole against recoverable-and-terminal-failures as amended on October 12, after review refused the October 9 copy (document-642.1) for contradicting it; that copy is not to be published'
    - action: amended
      by: architect
      at: 2026-10-18T01:19:46.462493Z
      reason: 'Answers the developer''s report-ba29f069 from the merge-finishing slice: a stop the harness itself takes on, by continuing the run or confirming its merge, now leaves the development manager''s list and takes the place of any earlier wait, so a wait never brings a closed stop back. Changes step 2, the writes of steps 5 and 8, slice 2''s tests and one reviewer check; nothing else.'
    - action: amended
      by: architect
      at: 2026-10-19T19:09:00.497254Z
      reason: 'Answers the reviewer''s report-0b4eddea: failing tests a change did not touch that fail again and also fail on the base commit, ruled outside the work on October 19, are added to step 1''s list so the hold on new work keeps them out of its count, and step 1 now says any cause recoverable-and-terminal-failures places outside the work is added here in the same decision. Adds one slice 1 test, one reviewer check and one rejected alternative; nothing else.'
    - action: amended
      by: architect
      at: 2026-10-20T02:11:47.339332Z
      reason: 'Answers the reviewer''s report-40dbc3db on the fix counting unrecognised stop causes toward the hold on new work (yoyodyne-ifd.428.99.12): step 1 now names the usage limit, a provider session ending after its retries, an unusable provider account and a stop by the operator or the development manager as outside the work, and rules that a dead process, a cancellation with no recorded origin, and a publishing failure other than the two named count toward the hold. Adds slice 1 tests, one reviewer check and two rejected alternatives; nothing else.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-15T09:22:49.968988Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 998, for document-998.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-18T01:19:46.462493Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1185, for document-1185.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-10-19T19:09:00.497254Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1298, for document-1298.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 3
      by: harness
      at: 2026-10-20T02:11:47.339332Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1318, for document-1318.1 under the automatic approval policy
---
# Stopped run recovery: every step from the stop to the run going on, and what each step may never do

## What this is for

The operator's direction of October 8 asked for one design covering stopped run recovery (yoyodyne-gtx). About a third of the 479 items filed from September 23 to October 7 concerned it, and its fixes came in chains where each fix broke something the one before had mended, because each step was designed on its own. This design states every step a stopped run can take: the stop itself, the development manager's list, her decision, the harness carrying it out, continuing the same run, a repair, a rerun, and finishing a merge. For each step it says what the step reads, what it writes, and what it must never do. It serves the autonomy goal: a stopped run should reach a decision and an effect without a person carrying it, and without a fix to one step undoing another.

It replaces the design written on October 9 (document-642.1), which review refused because it contradicted [recoverable-and-terminal-failures](recoverable-and-terminal-failures.md) as amended on October 12. That copy is not to be published. This design agrees with recoverable-and-terminal-failures in full and with [fresh-factory-health-and-preserved-run-recovery](fresh-factory-health-and-preserved-run-recovery.md), and amends the second in one place, named below.

## What it rests on, and what was not read

Read at commit fac5822d: recoverable-and-terminal-failures, fresh-factory-health-and-preserved-run-recovery, and [management-and-supervision](management-and-supervision.md). Read at commit 46bbf9eb for the October 19 revision of step 1: recoverable-and-terminal-failures again, and `internal/runstate/stopcause.go`, which names the stop causes a run record prints. Not read for this design: the code in `internal/orchestrator` (`triage.go`, the carry out files, `rerun.go`, `repaircontinue.go`, `schedule.go`, `pipeline.go`) and `internal/triage/window.go`. Where this design names how the code behaves as it ships, it takes that from tracker notes and reports. A developer building a slice reads the code it changes first. Where the code does something this design does not account for, the slice stops and the architect is asked, rather than the design being bent around it.

## The rules that hold across every step

1. **One record.** Every step reads and writes the run record and the item's recovery budget, under the run lease. No step keeps a list, a counter or a decision of its own anywhere else. The development manager's list of stopped runs is worked out from run records each time it is read.
2. **One way of deciding whether a run can resume.** Whether a run can go on, and from which step, is decided by one function over the run's saved state (the work on one function deciding whether a paused run can resume, yoyodyne-0rm). The watch session coming back after a redeploy, `yoyo run` on an item, and the carrying out of a decision all call it. None decides it its own way.
3. **One unit of budget.** A repair grant and a repair attempt are counted in the same unit, one attempt (yoyodyne-ifd.428.86). A rerun is counted apart, as one rerun for each stop.
4. **A cause is decided once.** It is decided when the run stops and never changed by a later step. A later stop of the same run is a new stop, decided afresh (yoyodyne-ifd.428.74).
5. **Nothing was judged when the cause is outside the work.** A stop outside the work charges no repair attempt and no review round, and the change is kept, as recoverable-and-terminal-failures already says.
6. **Every act is recorded before it is taken and read back after.** How a request is bound to what the caller saw, how a repeat is recognised, and the three results applied, refused and unknown are those of `run.recover` in fresh-factory-health-and-preserved-run-recovery, and are not restated here.

## Step 1: the run stops

Reads: what ended the run, the run's counters, and the bounds recoverable-and-terminal-failures sets for each boundary.

Writes: one stop on the run record, carrying the attempt number, the stop time, the step the run was at, the candidate commit, whether the change is kept, the evidence (the failing check's output, the reviewer's findings, the provider's last message, as applies), and one of three causes:

- **About the work:** a failing check, a repair verdict from the reviewer, a conflict on replay that the developer can resolve, a review refusal. While repair attempts remain, the run goes straight back to its developer and is not listed. When they are spent, it stops and is listed.
- **Outside the work:** a check stopped by its time limit once both reruns or three times its limit are spent; a provider's usage limit spent, whether it paused the run or refused it; the AI provider ending a session, or a session producing no output for longer than it is allowed, once the reissues and the thirty minutes the provider boundary allows are spent; the developer's or the reviewer's provider account being unusable, such as a login that has lapsed; the operator or the development manager stopping the run; a redeploy the watch session could not pick up again; no configured check ran; the reviewer's session ending because its budget ran out before a verdict (yoyodyne-ifd.428.78, as the reports describe it); an approved change that has lost the race to merge as many times as `execution.integration_retries_before_reconciliation` allows (yoyodyne-ifd.428.97); a merge the forge refused for a reason not about the change (yoyodyne-doa); publishing to the forge stopped because the forge boundary's two hours of retries ran out; failing tests the change did not touch that fail again when run once more and also fail on the base commit the change was built on, which means the target branch itself is failing (recoverable-and-terminal-failures, as ruled on October 19 for yoyodyne-ifd.429.86). Listed at once.
- **Uncertain:** the run may have merged and nothing said so. Listed at once, and only finishing the merge (step 8) may act on it.

Every cause in the list outside the work is kept out of the hold on new work's count, as recoverable-and-terminal-failures says. A cause that recoverable-and-terminal-failures places outside the work is added to this list in the same decision, so the two designs never disagree about which stops the hold counts. A provider account that cannot be used stops every run on that account the same way; it is kept out of the hold's count because the harness's pause on new work for provider capacity is what holds work for it (yoyodyne-ifd.435.10), and counting it as well would hold the same work twice for one cause.

Ruled on October 19, answering the reviewer's report-40dbc3db: these stops are not on the list above and count toward the hold on new work, as a failure of the harness itself does:

- **The process carrying the run died.** The run record cannot tell whether the harness's own process failed or the machine was shut down, and a harness process that keeps dying is what the hold exists to stop. Runs whose processes are found dead together when the watch session starts count as one stop, so one restart of the machine counts once and not once for each run it ended.
- **A cancellation whose origin is not recorded.** A cancellation is kept out only when the run record says it came from a person, from a recorded decision, or from the product restarting onto a new build, which is the redeploy named above. The harness records the origin of every cancellation it makes, and a cancellation that names none counts, as an unrecognised cause does.
- **Publishing the change to the forge failed** for any reason other than the two listed above: the forge boundary's retries running out, and a merge refused for a reason not about the change. A refusal of the harness's login or permission stops every run in the same way, so holding new work is right; the hold's escalation reaches the Lead Product Manager, who names the operator only where a credential or a repository setting is the cause.
- **The harness's own steps around the work failing**, such as recording the run or cleaning it up, and **any stop cause the harness does not recognise**, as recoverable-and-terminal-failures already says.

Counting a stop toward the hold changes only the hold. Whether a stop charges a repair attempt is decided by the three causes above, and none of these charges one, because nothing about the change was judged.

A redeploy that the watch session picks up again at its next start, as it did for the change letting owning roles decide proposed changes on October 15, is not listed: the run goes on by step 5 by itself. It is listed only if picking it up is refused.

Must never: count a check stopped by its time limit as a failing check; charge a repair attempt or a review round for a cause outside the work; end a run without writing a stop; write a stop that drops the reviewer's findings when a later check fails (yoyodyne-ifd.429.68).

## Step 2: the development manager's list

Reads: every run record with a stop that has not been closed in one of the ways below.

Writes: nothing. The list is worked out each time it is read.

Each entry is one stop, named by product, run, attempt number and stop time. It shows the item by its title and identifier, the cause in plain words, how long it has waited in the operator's local time, whether the change is kept, the budget left, and any decision already recorded with its state.

An entry leaves the list when, and only when, one of three things is recorded for that same stop:

- a decision for it has been carried out and checked (step 4);
- the harness has itself taken the run on, by continuing it (step 5) or by confirming its merge on the target branch (step 8), and recorded that on the stop;
- a wait recorded for it names a release condition that has not yet come.

A wait recorded against an earlier stop of the same run takes no later stop off the list. A decision carried out, or the harness taking the run on, takes the place of any wait recorded before it, and that wait does not bring the stop back when the time it names for looking again arrives (yoyodyne-ifd.428.71). A wait is a pause on the development manager's attention, never a decision that outranks what has already happened to the run.

Must never: show a decision that was carried out as still waiting; drop an entry because its run could not be read (the entry stays, marked unreadable); fold two stops of one run into one entry; bring back, when a wait's time for looking again passes, a stop that a carried out decision or the harness taking the run on has already closed.

## Step 3: the decision

Reads: the entry, the run as `run.inspect` reports it, the item's recovery budget, and a fresh health observation.

Writes: one decision on the run record, bound to the entry's stop and to the run's saved status and step as the development manager saw them, with her reason. The decisions are a closed list:

- **Continue** the same run from the step it stopped at, spending nothing.
- **Repair:** the same run goes back to its developer with a grant of a stated number of repair attempts.
- **Rerun:** a fresh run of the item from the current target branch.
- **Finish the merge:** complete the steps after a recorded or possible integration.
- **Wait,** naming the release condition and what will look at it again.
- **Give the item back:** the run is retired with its change kept, the item's claim and developer slot are released, and the item returns to the queue with the stop named in a note.
- **Record an unresolved hold,** naming its owner, its next step and when it will be looked at again.
- **Ask the Lead Product Manager,** when the decision needs a priority or a scope she owns.

A grant is taken from the item's recovery budget when the decision is recorded. A decision that asks for more than the budget holds is refused at once, naming what is left. The development manager is the only role that decides, as fresh-factory-health-and-preserved-run-recovery already says.

Must never: decide on an entry whose stop a later stop has replaced (refused as state moved); widen a budget; be taken by a program manager, by the architect, or by a person on a role's behalf.

## Step 4: carrying out the decision

Reads: the decision, the run under its lease, and the current dependencies, holds and gates that apply.

Writes: the act, recorded before it is taken and its result after, then a check of the outcome with `run.verify`. The decision is carried out when the check reports met. Until then the entry stays listed with the decision shown as being carried out.

Every recorded decision ends in exactly one of three ways: carried out and checked; refused, with the reason written on the entry, which stays listed for a new decision; or unknown, with an unresolved hold on the run naming the development manager. A decision skipped without one of these is a defect, and the next health observation reports it (report f7f26899).

Which comes first, carrying out a decision or pulling new work, is pull order, which is the Lead Product Manager's to set; this design does not set it.

Must never: carry out a decision twice; carry out an act that needs no worker while a worker of the run is live or uncertain; spend more of the budget than the decision granted.

## Step 5: continuing the same run

Reads: the run's saved step, its candidate commit, the results of checks already finished on that commit, and the bounds on reruns of a check stopped by its time limit.

Writes: the run goes on in the same branch, worktree, provider session and counters, from the step it stopped at. Development resumes development. At checks, a check that passed on the same candidate commit keeps its result. A check that had not finished is run again from the start, and a check stopped by its time limit counts against the two reruns within three times its limit, which no restart resets. An interrupted review starts again from the checks, as the delivery policy already says. When the harness continues a run by itself, with no decision asked, it records on the stop that it has taken the run on, which closes the stop on the list (step 2).

Must never: give credit to a check or a verdict that did not finish; move a run waiting on a dependency on to checking; start a new branch; reset a counter.

## Step 6: a repair

Reads: the stop's evidence and the grant.

Writes: the same run goes back into its repair loop with exactly the granted attempts. The developer is given the failing check and the reviewer's findings together when both exist. A repair decided for a conflict on replay is limited to that conflict.

Must never: be used for a cause outside the work when nothing in the change needs mending (continuing is the decision then); spend attempts past the grant; reset the item's earlier counts.

## Step 7: a rerun

Reads: the item, the stopped run's record and branch, and whether the stopped run's commit is on the target branch.

Writes: a fresh run of the item from the current target branch, on a new branch. The stopped run is retired, and its branch is kept until the rerun lands or is itself retired.

- A rerun is spent only when the fresh run holds the item. If carrying out loses the claim before then, the rerun is given back on the record (yoyodyne-ifd.428.53). A rerun that never ran is never counted as carried out (report 0ba929cf).
- A rerun already spent is given back only by a decision recorded on the triage surface, never by editing the store by hand (yoyodyne-ifd.428.41).
- For a run recorded as merged, the harness reads whether its commit is on the target branch. If it is, the rerun is refused as finished, and the decision to take is finishing the merge. If it is not, the disagreement is written on the run and the rerun goes ahead (report 0ba929cf).
- For a run that failed while merging, the rerun goes ahead once the harness has read the target branch and found the commit absent. If it finds the commit there, the rule for a run recorded as merged applies.

Must never: run beside a live worker of the same item; take the item from a run that still holds it; land the stopped run's branch as well.

## Step 8: finishing a merge

Reads: the target branch, the pull request's state on the forge, and the run's recorded integration steps.

Writes: whatever steps remain, under the target branch's promotion lease and within the fifteen minutes recoverable-and-terminal-failures allows for waits taken while holding it: confirming the merge by finding the commit on the target branch, deleting the branch, closing the item, and bringing the local copy of the target branch up to date. A merge the forge refused for a reason not about the change keeps its record of how it is to be merged, so trying it again needs no fresh run (yoyodyne-doa). When the merge is confirmed, the harness records on the stop that it has taken the run on, whether the merge was finished by a decision or by the harness alone, which closes the stop on the list (step 2) and takes the place of any wait recorded on it before.

Must never: start development afresh; ask the forge to merge a commit it has already merged; hold the lease past its bound; force a branch.

## Open items in this area

None is closed outright by this design. Each is narrowed into a slice below instead, so the work already admitted is kept.

Narrowed into the slices:

- a wait recorded on a stopped run's entry (yoyodyne-ifd.428.71);
- a run that stops again after a decided repair (yoyodyne-ifd.428.74);
- a spent rerun given back by decision (yoyodyne-ifd.428.41);
- a rerun that loses its claim (yoyodyne-ifd.428.53);
- a repair grant and attempt counted in one unit (yoyodyne-ifd.428.86);
- a check stopped by its own time limit (yoyodyne-ifd.428.37);
- a change that keeps losing the race to merge (yoyodyne-ifd.428.97);
- an approved change whose first merge is refused (yoyodyne-doa);
- one function deciding whether a paused run can resume (yoyodyne-0rm);
- the reviewer's findings kept after a failing check (yoyodyne-ifd.429.68);
- a change whose configured checks all could not run (yoyodyne-44j).

Left standing, outside this design:

- lifecycle recovery (yoyodyne-06m), shared health delivery (yoyodyne-e7c) and manager coverage validation (yoyodyne-5eu), which build the capabilities this design uses;
- the reviewer sending incomplete work back with four repair rounds before a replan (yoyodyne-o1b), which changes how large the budget is but not how it is counted;
- a parked item never being dispatched (yoyodyne-ifd.428.36).

The reports held behind this design are answered by it: f7f26899, 0ba929cf and 672b2b49, and those behind yoyodyne-ifd.428.78 and yoyodyne-ifd.429.65 as the Lead Product Manager recorded them. Report ba29f069, from the developer on finishing a merge (yoyodyne-ifd.428.99.10), is answered by step 2 as revised on October 17: the harness taking the run on closes the stop and takes the place of an earlier wait. Report 0b4eddea, from the reviewer of the October 19 revision of recoverable-and-terminal-failures, is answered by step 1 as revised on October 19: failing tests that also fail on the base commit are a cause outside the work, kept out of the hold on new work's count. Report 40dbc3db, from the reviewer of the fix counting unrecognised stop causes toward the hold (yoyodyne-ifd.428.99.12), is answered by step 1 as revised the same evening: the provider's usage limit, a provider session ending after its retries, an unusable provider account, and a stop by the operator or the development manager are named as outside the work; a dead process, a cancellation with no recorded origin, and a failure to publish other than the two named count toward the hold.

## Slices

Each slice is built and checked on its own. Each carries a test that walks its step from the stop to the checked outcome in a test home. Turning the slices into work items is the development manager's.

1. **Deciding the stop's cause and keeping its evidence.** Tests: a time limit stop after both reruns is listed with no attempt charged; a reviewer session out of budget is listed as outside the work; a failing check after review keeps the reviewer's findings; a spent race to merge is listed with the change kept; failing tests the change did not touch that fail again and also fail on the base commit are listed as outside the work, with no attempt charged, the change kept, and the stop left out of the hold on new work's count; a usage limit stop, an unusable provider account and a stop by the development manager are left out of the hold's count; a dead process, a cancellation naming no origin, and a publishing failure that is neither the forge's retries running out nor a merge refused for a reason not about the change each count toward it; three runs found dead together when the watch session starts count as one stop.
2. **The list worked out from run records, one entry for each stop.** Tests: a second stop of one run is a second entry; a wait on the first stop leaves the second listed; a decision carried out is never shown as waiting; an unreadable run stays listed as unreadable; a wait recorded on a stop and then the harness confirming that run's merge leaves the stop off the list, and it stays off after the wait's time for looking again has passed; the same for the harness continuing the run by itself.
3. **The closed list of decisions, their binding, and the one unit of budget.** Tests: a grant above the budget is refused; a decision on a replaced stop is refused as state moved; grant and spend match in attempts.
4. **Carrying out, with every decision ending in one of three ways.** Tests: a skipped decision is reported by the next health observation; a repeat of the same request does not act twice; an unknown result puts a hold on the run.
5. **One function deciding whether a run can resume, used by every path.** Tests: the watch session after a redeploy, `yoyo run`, and carrying out a continue decision reach the same step for the same saved state; a check passed on the same commit keeps its result, and one that had not finished runs again.
6. **Reruns.** Tests: each case named in step 7, including spent only on holding the item, given back on losing the claim, given back by decision, and the merged and failed while merging cases.
7. **Finishing a merge.** Tests: a refused merge whose record is kept is tried again with no fresh run; a merge already made is found and not asked again; a wait past fifteen minutes releases the lease with its state recorded.

Slices 1 and 2 come first. Slices 3 and 4 depend on them and on the capabilities lifecycle recovery builds. Slices 5 to 7 depend on 4.

## Designs and invariants this amends

- **fresh-factory-health-and-preserved-run-recovery:** the closed list of `run.recover` actions becomes the closed list of decisions in step 3, adding repair, rerun, giving the item back and asking the Lead Product Manager. The development manager's decisions and `run.recover` are then one path with one carrier. That revision follows this one.
- **recoverable-and-terminal-failures:** unchanged; this design applies it, including its October 19 rule on failures confined to tests a change did not touch and its rule that only step 1's list and cancellations by a person or a recorded decision are kept out of the hold's count.
- **management-and-supervision:** unchanged.
- **The recovery policy reconciliation (yoyodyne-ifd.429.42)** is folded in: its bounded reruns of a check are those of recoverable-and-terminal-failures, applied in steps 1 and 5.
- **Invariants:** none added. One promotion per target branch binds step 8. Integration needs evidence bound to the reviewed revision binds the kept check results in step 5. The rules above are enforced in code by the slices' tests rather than held over the whole repository, because nothing outside recovery can break them.

## What a reviewer can check

- No step keeps state outside the run record and the item's recovery budget.
- Every path that resumes a run calls the one function.
- No cause outside the work charges a repair attempt or a review round.
- Every cause recoverable-and-terminal-failures places outside the work appears in step 1's list.
- The code that sorts a stop's cause for the hold on new work keeps out exactly the causes step 1 lists outside the work and cancellations whose recorded origin is a person, a recorded decision or the product's restart, and counts every other cause, including a dead process.
- Every decision ends carried out and checked, refused with a reason, or unknown with a hold.
- No wait brings back a stop that a carried out decision or the harness taking the run on has closed.
- A rerun is counted only when the fresh run holds the item.
- No merge is asked for twice, and no lease is held past fifteen minutes.

## Alternatives rejected

- **Fixing each step as its bug arrived** was rejected because that is what made the chains of fixes the operator asked this design to end.
- **A recovery store of its own beside the run records** was rejected because a second account would disagree with the first, which fresh-factory-health-and-preserved-run-recovery already rejects.
- **Keeping the development manager's decisions and `run.recover` actions apart** was rejected because two carriers for one act would let one skip what the other checks.
- **Letting a wait outrank the harness's own finished act** was rejected because the wait was recorded about a run that has since moved on, and bringing it back asks the development manager to decide something already done.
- **Publishing the October 9 copy** was rejected because review refused it, and landing it would publish a contradiction.
- **Counting failures that also fail on the base commit toward the hold on new work** was rejected because nothing about the change was judged: the target branch itself is failing, which the harness's warning report and the dashboard's section for broken or degraded things already show, and holding unrelated new work for it would stop changes that caused nothing.
- **Keeping a dead process out of the hold's count** was rejected because the run record cannot tell the harness's own process failing from the machine shutting down, and a harness process that dies again and again would then never be stopped. Counting the runs found dead together at one start as one stop keeps a single machine restart from holding new work on its own.
- **Counting an unusable provider account toward the hold** was rejected because the harness's pause on new work for provider capacity already holds work for that account, and two holds for one cause would make the development manager clear the same thing twice.