---
id: merge-queue-pace
kind: design
title: How the harness's merge queue lands more than one change an hour
supports:
    - milestone-goals
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-09T17:52:13.891049Z
      reason: 'yoyodyne-ifd.429.132: rules on the operator''s proposal of November 8 to move the race check out of the merge queue, and on what else raises the queue''s landing rate, keeping every landing commit checked whole by its declared list and independently reviewed. Names five build pieces. Written from reads at main 06f384c0.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-09T17:52:13.891049Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2540, for document-2540.1 under the automatic approval policy
---
# How the harness's merge queue lands more than one change an hour

## What this is for

The milestone goal for an internal week on Yoyodyne's own repository asks that no hour pass with ready work waiting and a developer slot idle ([milestone goals](../product/milestone-goals.md)). On November 8 the harness's own merge queue landed four changes, one at a time: about 70 minutes each for code, from candidate to merge, and 35 for a document. Two code changes waited behind each other. Three developer slots finish more than one change an hour on a busy day, so the queue now limits everything that lands.

The operator asked on November 8 why the queue lands only one change an hour, and proposed moving the race check out of it (yoyodyne-ifd.429.132). This design rules on that proposal and says what else raises the rate.

It changes how [integration through a merge queue](integration-through-a-merge-queue.md) runs, not what it guarantees. Every commit that lands is still checked, on exactly that commit, and independently reviewed, as [the invariant that integration requires evidence bound to the exact candidate revision](../decisions/invariants/integration-requires-revision-bound-evidence.md) requires.

## What a candidate costs today

These figures are the operator's assistant's, from about 390 check runs across the latest 80 or so runs. They are not yet checked against the harness's records.

- `make race`: about 30 minutes on average, longest 5 hours 25 minutes, 44 runs that did not pass.
- `make test`: about 19 minutes on average, longest 42 minutes, 10 runs that did not pass.
- Vet and the format check: under a minute each.

Each code candidate runs all of these one after another, and then the independent review.

The race check is the largest single part. It is also the part that most often fails for reasons other than the change. The one diagnosis of the race check in this repository is about timing under load, and no commit since August describes fixing a data race.

## Decisions

### 1. The queue runs the checks the project declares for it, and this repository leaves the race check out

A project may declare which of its configured checks the queue runs on a candidate. A project that declares none has its configured checks run whole, as today.

This repository declares format, vet and `make test`. The queue runs those whole, on exactly the candidate commit, and the independent review judges that same commit. The declared list and its digest are recorded with the candidate's evidence, as the queue already records the check set.

The race check moves to two other places. Both are already ruled under guarantee 9 of [one owner for each guarantee](one-owner-for-each-guarantee.md).

- **Before review, in the developer's run.** The race check runs whenever Go code changed, on the packages the change touches and the packages that depend on them (race point 1 of guarantee 9). Its passing result is recorded against the approved head.
- **On main every four hours, whenever main has moved** (race point 4).
  - A failure is not run again to make it pass.
  - The harness files a critical report naming the failing tests, the commit, the last clean commit and the commits between them.
  - The failure is admitted at priority 0 with the reliability label.

  The operator proposed a nightly run. Four hours is kept because it was already ruled, and it shortens the time a race can sit on main.

At admission the queue reads the approved head's record. If Go code changed and that head has no passing race result, the queue adds the narrowed race check to that candidate's run. So no change with Go code changed lands without a race check on its own code.

**The risk accepted.** A race that appears only when two changes are combined is not checked before landing. It can reach main and sit there until the next run on main: at most four hours, plus that run's own length. This is accepted for three reasons:

- The 44 race failures look like timing under load rather than races found, and no fix of a data race has landed since August.
- The race detector sees only memory shared inside one process. The harness's real trouble with sharing has come between processes, which file locks guard and the detector cannot see.
- The race check costs more than half of each code candidate's time.

A separate item is classifying the 44 failed race runs. If it finds a real race that only the check on the combined change caught, this decision is revisited, and the race check goes back on the queue's list.

**How this fits the rule that the exact candidate is checked whole.**

- The queue still runs every check on its declared list whole, never narrowed, on exactly the commit that lands.
- The review still judges that same commit.
- What changes is the list.

The invariant needs the configured checks for integration to approve the exact candidate. The queue's declared list is that set, so the invariant is kept and needs no revision.

The race check produces evidence about two other things: the change's own code, recorded on the approved head, and main, recorded on main. Neither is presented as evidence about the candidate.

**How this fits stopping the local full race check at landing (yoyodyne-ifd.428.126.6.5).** That item, being built now, builds case 2 of [choosing checks by change](choosing-checks-by-change.md). Case 2 says that with the harness's queue on, the local full race on the merge candidate does not run when the queue ran the configured checks whole, the race check among them. That item lands as written.

Once build piece 1 below lands, case 2 says instead that the local full race on the merge candidate does not run when all of these hold:

- the queue ran its declared list whole on exactly the commit that lands;
- every check on that list passed;
- the approved head carries a passing race result, or the queue ran the narrowed race check itself.

While the queue is on, the local full race at landing is not brought back in any case.

This decision takes effect in this repository only once the run on main every four hours (migration item 21 of the guarantees design) is running. Until then, the queue keeps the race check on its list.

### 2. Checks and the review run side by side on the same candidate

Today the review follows the checks. Both are bound to the same candidate commit, so neither needs the other's result first, and the queue starts both at once.

- A failing check ends the candidate. The review in progress is stopped, and its spend is recorded against the candidate.
- A review that refuses ends the candidate the same way.
- A candidate lands only with every declared check passed and the review approving, as now.

### 3. The next candidate is checked while the current one is still being checked

While the current candidate's checks and review run, the queue builds the next entry's candidate on top of the current candidate's commit, as if that commit lands, and starts its checks and review.

That next candidate is a commit of its own, with its own base recorded, and its evidence is bound to it alone. It may land only if, when the promotion lease is taken, the target equals the base it was built on. That happens exactly when the candidate before it landed as that commit.

If the candidate before it failed, was withdrawn, or landed as any other commit, the next candidate's results are thrown away and it is built again on the real target. As with any movement of the target, no repair attempt is charged for that.

The queue checks at most one candidate ahead of the current one. Checking further ahead multiplies the work thrown away when one fails, and one candidate ahead already removes the wait between candidates.

This design does not establish whether a candidate landed through the forge's pull request path lands as exactly its own commit. The build piece reads the landing code first. Where it does not land as its own commit, the next candidate is always rebuilt, and the piece is reported as giving nothing on that path.

### 4. Keeping a passing result when only main has moved

The queue does not keep a candidate's result for a new candidate merely because the change is the same and main moved. That is the case the queue exists to check.

Guarantee 9 already allows a result to be kept across commits in one case: where the project's analyser says, with complete evidence, that nothing the check covers changed, worked out on the change combined with the new main, and in a trial period first. Once that rule is built, the queue uses it and adds no rule of its own.

### Combining two or three changes into one candidate: rejected for now

Checking several waiting changes as one combined candidate, and splitting it only on a failure, would cut the checks for each change the most. It is rejected for now for three reasons:

- On this repository's protected main, the queue lands each change through its own pull request. A combined candidate is a commit none of those pull requests holds, so landing it needs a path of its own.
- A failure in a combined candidate costs a split and a second round of checks. The race check's failures, the most common kind, leave the queue under decision 1.
- With decisions 1 to 3, the time for each code candidate is expected to fall from about 70 minutes to about the longer of `make test` and the review, roughly 20 to 30 minutes, with the next candidate already under way.

It is reconsidered from the figures that build piece 5 records, if the queue is still the limit after pieces 1 to 3 land.

## What does not change

- A confirmed document's candidate still runs the declared document checks, as the queue design and [the document landing route](document-landing-route.md) say.
- The promotion lease covers only reading the target, the guarded change to it, and its record.
- Every candidate earns its checks and review on its own commit.
- A moved target is never charged as a repair.
- With the queue switched off, every check before promotion runs whole on this machine, the full race check included.
- Formatting and vet run on every change.

## Build pieces

1. **The queue's declared check list.**
   - Guarantee 9's plan reads it, and it is recorded with the candidate's evidence.
   - This repository declares format, vet and `make test`.
   - At admission, the queue reads the approved head's race result and adds the narrowed race check where Go code changed and none passed.
   - Case 2 of choosing checks by change takes the new condition.
   - It takes effect here only once the run on main every four hours is running.
   - It depends on the work on stopping the local full race check at landing (yoyodyne-ifd.428.126.6.5) and on migration item 21 of the guarantees design.
2. **Checks and the review side by side on one candidate.** A failing check stops the review, and the review's spend is recorded.
3. **Checking the next candidate ahead.** One candidate deep. Thrown away and rebuilt when the target is not the base it was built on. Reads the forge landing code first, and reports if candidates do not land as their own commit.
4. **Keeping a queue candidate's result across a moved main by the analyser's answer.** No new piece: this is migration item 19 of the guarantees design, and the queue calls it.
5. **The queue's weekly figures,** from its own records: candidates started and landed, time from admission to landing, time for each candidate, and candidates thrown away by checking ahead. They are shown with the weekly check-time figures.

Pieces 1, 2 and 3 are independent of each other and can be built in any order the Lead Product Manager sets. Turning them into work items is the development manager's.

## How a reviewer checks an implementation

- A candidate lands only when every check on its declared list passed whole on exactly the commit that lands, and the review approved that commit.
- A project with no declared list has its configured checks run whole in the queue.
- A change whose Go code changed, and whose approved head has no passing race result, gets the narrowed race check in its candidate's run.
- In this repository, the race check is left off the queue's list only while the run on main every four hours is running.
- A candidate checked ahead lands only when the target equals its recorded base. Otherwise its results are thrown away, it is rebuilt, and no repair is charged.
- A failing check stops the review on the same candidate, and the review's spend is recorded.

## Designs this changes

- **[Choosing checks by change](choosing-checks-by-change.md):** case 2 takes the condition in decision 1, and its paragraph saying this design was still to come is replaced.
- **[One owner for each guarantee](one-owner-for-each-guarantee.md):**
  - race point 3 and its reviewer line follow choosing checks by change;
  - its rejected alternative of keeping the local full race on every merge candidate is brought into line;
  - its entry for choosing checks under the designs it changes is brought into line.
- **[Integration through a merge queue](integration-through-a-merge-queue.md):** the queue runs the project's declared list, runs checks and the review side by side, and checks one candidate ahead.

Until each is revised, this design decides where they differ.

## Alternatives rejected

- **Keeping the full race check in the queue.** It is more than half of each code candidate's time, and it is the most common failure not caused by the change.
- **Running the race check on main only nightly, as proposed.** Four hours was already ruled, and it shortens the time a race found only on the combined change can sit on main.
- **No race check before landing for a change whose developer run did not record one.** Every change with Go code changed carries a race check on its own code.
- **Checking more than one candidate ahead.** Each further candidate multiplies the work thrown away when one fails.
- **Combining waiting changes into one candidate.** See above.
- **Keeping results when only main moved, without the analyser.** That is what the queue exists to check.

## What this rests on

Read at main 06f384c0:

- the item for this design (yoyodyne-ifd.429.132), with the operator's proposal recorded on it;
- integration through a merge queue;
- choosing checks by change;
- one owner for each guarantee;
- the invariants that integration requires evidence bound to the exact candidate revision, and that developers verify before submitting.

Not read:

- the queue's code;
- the forge landing code;
- the race diagnosis;
- the classification of the 44 failed race runs.

The figures for check times are the operator's assistant's and are still to be checked against the harness's records.