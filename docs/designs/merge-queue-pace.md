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
    - action: amended
      by: architect
      at: 2026-11-19T20:09:55.758998Z
      reason: 'Brings decision 1''s run on main every four hours into line with the November 19 ruling that a failure there runs only its failing tests once more on the same commit: a pass the second time files a warning with both results and leaves the last clean commit where it was; a second failure, tests that cannot be named, or a second run that cannot run files the critical report at priority 0; the same test doing this twice in seven days files the critical report. Written from a read of the whole design at main 6df1c6caf; nothing else in the section changes.'
    - action: amended
      by: architect
      at: 2026-11-22T03:53:17.775361Z
      reason: 'Writes in three approved but unwritten changes: amendment-25324898 and amendment-097450b4 (the queue passes on its own every minute while it waits on GitHub, reads only, lease unchanged, stops when the watch session drains or ends, waits recorded for the weekly figures) and amendment-cec9c712 (checking ahead gives nothing on the GitHub path, so it is off there). Adds build piece 6 and two reviewer lines. Whole revision from main 3220fd777.'
    - action: amended
      by: architect
      at: 2026-11-24T04:06:39.271504Z
      reason: Resends the revision the review returned, whole from main 585539c6ef62. The reviewer checklist now separates a queue that runs its whole list on this machine from one using GitHub's required checks, the list of what was read is corrected, and decisions 1, 2 and 3 and the closing sentence say the same as the build section. Says piece by piece what is built, from the November 21 comparison and a read of the code at 9cde11a1e429. Part of the review of every design against the code.
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-09T17:52:13.891049Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2540, for document-2540.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-11-19T20:09:55.758998Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3133, for document-3133.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-11-22T03:53:17.775361Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3396, for document-3396.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 3
      by: harness
      at: 2026-11-24T04:06:39.271504Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3810, for document-3810.1 under the automatic approval policy
---
# How the harness's merge queue lands more than one change an hour

## What this is for

The milestone goal for an internal week on Yoyodyne's own repository asks that no hour pass with ready work waiting and a developer slot idle ([milestone goals](../product/milestone-goals.md)). On November 8 the harness's own merge queue landed four changes, one at a time: about 70 minutes each for code, from candidate to merge, and 35 for a document. Two code changes waited behind each other. Three developer slots finish more than one change an hour on a busy day, so the queue then limited everything that landed.

The operator asked on November 8 why the queue lands only one change an hour, and proposed moving the race check out of it (yoyodyne-ifd.429.132). This design rules on that proposal and says what else raises the rate.

It changes how [integration through a merge queue](integration-through-a-merge-queue.md) runs, not what it guarantees. Every commit that lands is still checked, on exactly that commit, and independently reviewed, as [the invariant that integration requires evidence bound to the exact candidate revision](../decisions/invariants/integration-requires-revision-bound-evidence.md) requires.

## What a candidate cost on November 8

These figures are the operator's assistant's, from about 390 check runs across the latest 80 or so runs at the time. They are not yet checked against the harness's records.

- `make race`: about 30 minutes on average, longest 5 hours 25 minutes, 44 runs that did not pass.
- `make test`: about 19 minutes on average, longest 42 minutes, 10 runs that did not pass.
- Vet and the format check: under a minute each.

Each code candidate then ran all of these one after another, and then the independent review.

The race check was the largest single part. It was also the part that most often failed for reasons other than the change. The one diagnosis of the race check in this repository was about timing under load, and no commit between August and November 9 described fixing a data race.

## Decisions

Decisions 1 and 2 describe a queue that runs its whole declared list on this machine. Where GitHub's required checks are used, as on this repository, the section on that path under the build pieces says how they apply.

### 1. The queue runs the checks the project declares for it, and this repository leaves the race check out

A project may declare which of its configured checks the queue runs on a candidate. A project that declares none has its configured checks run whole.

This repository declares format, vet and `make test`. A queue that runs its list on this machine runs those whole, on exactly the candidate commit, and the independent review judges that same commit. The declared list and its digest are recorded with the candidate's evidence, as the queue already records the check set.

The race check moves to two other places. Both are already ruled under guarantee 9 of [one owner for each guarantee](one-owner-for-each-guarantee.md).

- **Before review, in the developer's run.** The race check runs whenever Go code changed, on the packages the change touches and the packages that depend on them (race point 1 of guarantee 9). Its passing result is recorded against the approved head.
- **On main every four hours, whenever main has moved** (race point 4, which also decides what that run covers).
  - When the run fails, the harness runs only the failing tests once more, on the same commit, under the race detector with the same settings. Both results are kept and shown.
  - If every one of those tests passes the second time, the harness files a warning report naming them, the commit, and both results. A run that passed only on the second try is not clean: the last clean commit named in later reports does not move, so a race that comes back is still traced to every commit it could have come from.
  - If a test fails again, the failing tests cannot be named from the output, or the second run cannot run, the harness files a critical report naming the failing tests, the commit, the last clean commit and the commits between them. The failure is admitted at priority 0 with the reliability label.
  - The same test failing and then passing on main twice within seven days files the critical report after all, naming both runs.
  - Nothing is hidden by the second run: the first result is kept and reported in every case, and only how severe the report is depends on the second run. This was ruled on November 19 (yoyodyne-fgkk), because tests that GitHub's race job passed on the same commit were raising critical reports, and the same single second run was already ruled for developer runs (yoyodyne-ifd.429.87).

  The operator proposed a nightly run. Four hours is kept because it was already ruled, and it shortens the time a race can sit on main.

At admission the queue reads the approved head's record. If Go code changed and that head has no passing race result, the queue adds the narrowed race check to that candidate's run, except where one of GitHub's required checks is the race check and so covers the candidate itself. So no change with Go code changed lands without a race check on its own code.

**The risk accepted.** Where the candidate itself gets no race check, a race that appears only when two changes are combined is not checked before landing. It can reach main and sit there until the next run on main: at most four hours, plus that run's own length. This is accepted for three reasons:

- The 44 race failures look like timing under load rather than races found, and no fix of a data race landed between August and this ruling.
- The race detector sees only memory shared inside one process. The harness's real trouble with sharing has come between processes, which file locks guard and the detector cannot see.
- The race check cost more than half of each code candidate's time.

A second risk follows from the run on main: a real race that shows only now and then is reported as a warning the first time it passes on the second try. The seven-day repeat rule, once built, and the unmoved last clean commit limit that risk.

A separate item is classifying the 44 failed race runs. If it finds a real race that only the check on the combined change caught, this decision is revisited, and the race check goes back on the queue's list.

**How this fits the rule that the exact candidate is checked whole.**

- The queue still runs every check on its declared list whole, never narrowed, on exactly the commit that lands, or has GitHub's required checks run on exactly that commit in place of the part of the list it does not run here.
- The review still judges that same commit.
- What changes is the list.

The invariant needs the configured checks for integration to approve the exact candidate. The queue's declared list, or the quick checks with GitHub's required checks, is that set, so the invariant is kept and needs no revision.

The race check in the developer's run and on main produces evidence about two other things: the change's own code, recorded on the approved head, and main, recorded on main. Neither is presented as evidence about the candidate.

**How this fits stopping the local full race check at landing (yoyodyne-ifd.428.126.6.5).** That item builds case 2 of [choosing checks by change](choosing-checks-by-change.md). For a queue that runs its declared list on this machine, the local full race on the merge candidate does not run when all of these hold:

- the queue ran its declared list whole on exactly the commit that lands;
- every check on that list passed;
- the approved head carries a passing race result, or the queue ran the narrowed race check itself.

Case 2 of that design says what holds where GitHub's required checks are used. While the queue is on, the local full race at landing is not brought back in any case.

This decision took effect in this repository only once the run on main every four hours (migration item 21 of the guarantees design) was running.

### 2. Checks and the review run side by side on the same candidate

Both are bound to the same candidate commit, so neither needs the other's result first. A queue that runs its whole list on this machine starts both at once. Where GitHub's required checks are used, the quick checks come first and the review runs beside GitHub's checks.

- A failing check ends the candidate. A review in progress is stopped, and its spend is recorded against the candidate.
- A review that refuses ends the candidate the same way.
- A candidate lands only with every check it owes passed and the review approving.

### 3. The next candidate is checked while the current one is still being checked, where the queue lands on a local branch

While the current candidate's checks and review run, the queue builds the next entry's candidate on top of the current candidate's commit, as if that commit lands, and starts its checks and review.

That next candidate is a commit of its own, with its own base recorded, and its evidence is bound to it alone. It may land only if, when the promotion lease is taken, the target equals the base it was built on. That happens exactly when the candidate before it landed as that commit.

If the candidate before it failed, was withdrawn, or landed as any other commit, the next candidate's results are thrown away and it is built again on the real target. As with any movement of the target, no repair attempt is charged for that.

The queue checks at most one candidate ahead of the current one. Checking further ahead multiplies the work thrown away when one fails, and one candidate ahead already removes the wait between candidates.

**On the GitHub path it gives nothing, so it is off there.** Ruled on November 11. A queue that lands through GitHub asks GitHub to merge each candidate directly, as [integration through a merge queue](integration-through-a-merge-queue.md) rules, and that merge puts a merge commit above the candidate. The target after a landing is therefore never the candidate's own commit, so a candidate built ahead on that commit could never land and would always be thrown away. Checking ahead is used only where the queue lands on a local branch as the candidate's own commit. With the queue landing through GitHub, as on this repository, the queue does not build a candidate ahead at all, and the time saved for each candidate here rests on decisions 1 and 2 alone.

### 4. Keeping a passing result when only main has moved

The queue does not keep a candidate's result for a new candidate merely because the change is the same and main moved. That is the case the queue exists to check.

Guarantee 9 already allows a result to be kept across commits in one case: where the project's analyser says, with complete evidence, that nothing the check covers changed, worked out on the change combined with the new main, and in a trial period first. Once that rule is built, the queue uses it and adds no rule of its own.

### 5. The queue keeps moving while it waits on GitHub

Ruled on November 10, with the rule for a pass that ends while waiting approved on November 12 (yoyodyne-ifd.429.138).

Between one landing and the next candidate's first check, the queue often sits waiting on GitHub: for checks to start, to finish, or for a merge to be reported. If it looks again only at the scheduler's next watch round, every one of those waits is lengthened by the time to that round.

- A queue pass that ends while it is waiting on GitHub runs itself again every minute, on its own, apart from the watch session's rounds.
- These passes only read GitHub. Anything they would change, such as landing a candidate, still goes through the promotion lease exactly as an ordinary queue pass does; the lease and what it covers are unchanged.
- The passes stop when the watch session drains or ends, so a stop or a move onto a new build is never held up by them.
- Each wait from one landing to the next candidate's first check is recorded, so the weekly figures in build piece 5 show how long the queue sat between candidates.

### Combining two or three changes into one candidate: rejected for now for code

[Integration through a merge queue](integration-through-a-merge-queue.md) rules that document candidates are combined into one candidate, up to eight. For code, checking several waiting changes as one combined candidate, and splitting it only on a failure, would cut the checks for each change the most. It is rejected for now for three reasons:

- On this repository's protected main, the queue lands each code change through its own pull request. A combined candidate is a commit none of those pull requests holds, so landing it needs a path of its own.
- A failure in a combined candidate costs a split and a second round of checks. Under decision 1 the local race check's failures, the most common kind, leave a queue that runs its list on this machine.
- When this was ruled, decisions 1 and 2 were expected to bring the time for each code candidate from about 70 minutes to about the longer of `make test` and the review, roughly 20 to 30 minutes.

It is reconsidered from the figures that build piece 5 records, if the queue is still the limit.

## What does not change

- A confirmed document's candidate still runs the declared document checks, as the queue design and [the document landing route](document-landing-route.md) say.
- The promotion lease covers only reading the target, the guarded change to it, and its record.
- Every candidate earns its checks and review on its own commit.
- A moved target is never charged as a repair.
- With the queue switched off, every check before promotion runs whole on this machine, the full race check included.
- Formatting and vet run on every change.

## Build pieces

What is built was compared with the code on November 21, at commit 848d86190. The code for checking ahead and for the quick checks was read again on November 23, at commit 9cde11a1e429.

1. **The queue's declared check list. Built.**
   - Guarantee 9's plan reads it, and it is recorded with the candidate's evidence.
   - A project declares it with `merge_queue_checks`, and names the queue's race check with `merge_queue_race_check`. This repository declares format, vet and `make test`.
   - At admission, the queue reads the approved head's race result and adds the narrowed race check where Go code changed and none passed, as decision 1 says.
   - Case 2 of choosing checks by change takes the condition in decision 1.
   - It took effect here only once the run on main every four hours was running.
2. **Checks and the review side by side on one candidate. Built.** A failing check stops the review, and the review's spend is recorded.
3. **Checking the next candidate ahead, for queues that land on a local branch. Built.** One candidate deep. Thrown away and rebuilt when the target is not the base it was built on. Where the project lands through pull requests, the queue builds nothing ahead and gives the reason with its result. Status is to show that reason; whether it does was not read.
4. **Keeping a queue candidate's result across a moved main by the analyser's answer.** No new piece: this is migration item 19 of the guarantees design, and the queue calls it. Whether that item is built was not read.
5. **The queue's weekly figures. Not found in the code.** From the queue's own records: candidates started and landed, time from admission to landing, time for each candidate, the wait from each landing to the next candidate's first check, and candidates thrown away by checking ahead. They are to be shown with the weekly check time figures. Those figures count starts of the heavy checks today, and nothing about the queue was found beside them.
6. **The queue's own pass every minute while it waits on GitHub.** Reads GitHub only; changes go through the promotion lease as before; stops when the watch session drains or ends; records each wait for piece 5. Whether it is built was not read. The piece reads the queue's code first and builds only what is not already there.

The pieces not yet built are independent of each other and can be built in any order the Lead Product Manager sets. Turning them into work items is the development manager's.

### Where GitHub's required checks are used

Ruled on November 23. Where a project has checks on CI switched on, names required checks, and lands through the forge, as this repository does, a code candidate is checked as follows.

- The queue runs the quick local checks that `execution.ci_checks.local` names, on the candidate. GitHub's required checks, on exactly that commit, take the place of the rest of the declared list.
- The queue's own narrowed race check is added where the change owes one and no required check is the race check. The code recognises a required check as the race check when its name is one of the words of the queue's race command, as `race` is in `make race`. In that case GitHub runs the race check on the candidate itself, and the queue adds none.
- The quick checks run first. The candidate is pushed for GitHub's checks only once they pass, and the review then runs beside GitHub's checks. A required check that GitHub reports failed stops the review. This order is kept: the quick checks are short, and a candidate that fails one is never pushed.
- Where GitHub cannot settle a candidate, the entry's next candidate runs the full local checks, and nothing is charged for it.

The rest of how GitHub's checks are used is in [integration through a merge queue](integration-through-a-merge-queue.md), and is not restated here.

### The run on main

What the run on main covers is decided under race point 4 of [one owner for each guarantee](one-owner-for-each-guarantee.md). Decision 1 says how a failure there is reported.

- The single second run of the failing tests, and the warning when they pass, are built.
- The rule that the same test failing and then passing twice within seven days files the critical report is not built. It is the work on when main's scheduled check is critical (yoyodyne-ifd.429.240).
- Pointing this repository's run on main at the narrower set of packages is the work on the race check for the packages only macOS covers (yoyodyne-ifd.429.279), which was open on November 23.

### Documents

Landing several documents as one candidate is ruled in integration through a merge queue, and its build (yoyodyne-ifd.429.267) was open on November 23. Until it lands, each document is a candidate of its own.

## How a reviewer checks an implementation

Where the queue runs its whole declared list on this machine:

- A candidate lands only when every check on its declared list passed whole on exactly the commit that lands, and the review approved that commit.
- A project with no declared list has its configured checks run whole in the queue.
- A change whose Go code changed, and whose approved head has no passing race result, gets the narrowed race check in its candidate's run.
- A failing check stops the review on the same candidate, and the review's spend is recorded.

Where GitHub's required checks are used:

- A code candidate lands only when the quick local checks passed, every required check passed on exactly the commit that lands, and the review approved that commit.
- A candidate that fails a quick check is not pushed.
- A change that owes a race check gets it on its candidate, either from a required check that is the race check or, where there is none, from the queue's own narrowed race check.
- A required check that GitHub reports failed stops the review on the same candidate, and the review's spend is recorded.
- A candidate GitHub cannot settle is followed by one that runs the full local checks, and nothing is charged for it.

In either case:

- In this repository, the race check is left off the queue's list only while the run on main every four hours is running.
- A candidate checked ahead lands only when the target equals its recorded base. Otherwise its results are thrown away, it is rebuilt, and no repair is charged. With the queue landing through GitHub, no candidate is built ahead.
- While the queue waits on GitHub it passes again every minute without the watch session's round, those passes change nothing outside the promotion lease, and they stop when the watch session drains or ends.
- The wait from each landing to the next candidate's first check is recorded and appears in the weekly figures.

## Designs this changes

- **[Choosing checks by change](choosing-checks-by-change.md):** case 2 takes the condition in decision 1, and its paragraph saying this design was still to come is replaced.
- **[One owner for each guarantee](one-owner-for-each-guarantee.md):**
  - race point 3 and its reviewer line follow choosing checks by change;
  - its rejected alternative of keeping the local full race on every merge candidate is brought into line;
  - its entry for choosing checks under the designs it changes is brought into line.
- **[Integration through a merge queue](integration-through-a-merge-queue.md):** the queue runs the project's declared list, runs checks and the review side by side, checks one candidate ahead only where it lands on a local branch, and passes every minute while it waits on GitHub.

Until the first two are revised, this design decides where they differ from it, except that race point 4 of one owner for each guarantee decides what the run on main covers. Integration through a merge queue was revised after this design was first written: where the two differ about the queue's order, combining documents, or GitHub's path, that design decides.

## Alternatives rejected

- **Keeping the full race check in the queue's list on this machine.** It was more than half of each code candidate's time, and the most common failure not caused by the change.
- **Running the race check on main only nightly, as proposed.** Four hours was already ruled, and it shortens the time a race found only on the combined change can sit on main.
- **No race check before landing for a change whose developer run did not record one.** Every change with Go code changed carries a race check on its own code.
- **Checking more than one candidate ahead.** Each further candidate multiplies the work thrown away when one fails.
- **Checking ahead on the GitHub path anyway.** Every candidate built ahead would be thrown away, because GitHub's merge commit is never the base it was built on.
- **Starting the review beside the quick checks on the GitHub path.** The quick checks are short, so little time is saved, and a candidate that fails one would already have spent on a review.
- **Waiting for the watch session's next round while the queue waits on GitHub.** It adds the time to that round to every wait between candidates.
- **Combining waiting code changes into one candidate.** See above.
- **Keeping results when only main moved, without the analyser.** That is what the queue exists to check.

## What this rests on

Read at main 06f384c0 for the first version:

- the item for this design (yoyodyne-ifd.429.132), with the operator's proposal recorded on it;
- integration through a merge queue;
- choosing checks by change;
- one owner for each guarantee;
- the invariants that integration requires evidence bound to the exact candidate revision, and that developers verify before submitting.

The revision of November 21 was written from a read of the whole design at main 3220fd777. It recorded three changes approved on November 10, 11 and 12: the queue's own pass while it waits on GitHub, that pass running every minute until the watch session drains or ends, and checking ahead giving nothing on the GitHub path.

The revision of November 23 was written from a read of the whole design at main 585539c6ef62. What it says is built rests on the comparison of the code with this design made on November 21 at commit 848d86190, and on a read of the code for checking ahead and for the quick checks at commit 9cde11a1e429. The settings it names are as that comparison found them.

Not read for that revision:

- the code behind build pieces 4 and 6;
- whether status shows why nothing is checked ahead;
- the rest of the queue's code, beyond what the comparison reports;
- the race diagnosis;
- the classification of the 44 failed race runs.

The figures for check times are the operator's assistant's and are still to be checked against the harness's records.