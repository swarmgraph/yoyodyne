---
id: choosing-checks-by-change
kind: design
title: Choosing checks by what a change touches, through one shared policy
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-13T13:46:59.85692Z
      reason: 'yoyodyne-ifd.429.44: the design document for the October 12 ruling on choosing checks by what a change touches, which the Lead Product Manager recorded on the item; the ruling is unchanged, with the rejected alternatives and how it fits the landed repeated-checks design added.'
    - action: amended
      by: architect
      at: 2026-10-25T19:32:03.829876Z
      reason: 'Agrees with the guarantees design''s race check ruling: the merge candidate is still always verified in full, and the revision names which run does that in each of three cases. The local full race check is dropped only once integration through the merge queue is on and both conditions are confirmed at that landing, as the Lead Product Manager decided on October 25. A condition the harness cannot confirm, including unreadable forge settings, runs it locally. Written from a read at main 08be9d12. A matching sentence in the guarantees design''s race point 3 follows in its own revision.'
    - action: amended
      by: architect
      at: 2026-11-09T04:14:36.134773Z
      reason: Answers a developer's proposal (amendment-ce54085e, approved). The harness's own merge queue was switched on here on November 8 and the forge's merge-queue rule was taken off main, so the forge conditions could never be confirmed here. The harness's own queue's whole run of the configured checks, the full race check included, over exactly the commit that lands now verifies that candidate for the race check, read from the harness's own records. Anything not shown runs the local full race check. The forge route stays for projects using the forge's queue. Read whole at main d06f03ab. The guarantees design's race point 3 follows in its own revision.
    - action: amended
      by: architect
      at: 2026-11-17T07:07:41.608844Z
      reason: 'Answers amendment-4ae0c6e2 (approved November 16), from the merge queue''s declared checks (yoyodyne-ifd.429.142): case 2 now asks for the declared list merge_queue_checks run whole and passed, and either the approved head''s full race result or the narrowed race check merge_queue_race_check, and still that the checked commit is exactly the commit that lands. The paragraph waiting on the pace design is dropped, and the design now states that case 2 accepts less than a full race check on the candidate and what that costs. Also answers report-687aa860: any changed Go file selects the tests of internal/runstate, so the check on how a run''s record is written is never narrowed away. The getting-started rename waits for yoyodyne-9f4 to land. Read whole at main 48aa6b57.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-13T13:46:59.85692Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 800, for document-800.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-25T19:32:03.829876Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1736, for document-1736.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-11-09T04:14:36.134773Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2501, for document-2501.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 3
      by: harness
      at: 2026-11-17T07:07:41.608844Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2933, for document-2933.1 under the automatic approval policy
---
# Choosing checks by what a change touches, through one shared policy

This design says how a developer run's checks may be narrowed to what a change can affect, without ever letting a change land on less verification than it needs. It serves the v1 goal of a traceable chain from the brief through work, code changes and verification: every check a run narrows or skips is recorded with the reason, so the record of what verified a change stays complete. It also serves the goal that the harness supports any language: the harness itself knows only path rules and an analyser command a project declares, and nothing about Go.

It was ruled on October 12, 2026 for the design item on choosing checks by change (yoyodyne-ifd.429.44), and the ruling was recorded on that item. This document is that ruling, with the reasons and the rejected alternatives added. It was revised on October 25, 2026 to agree with [the design for one owner for each guarantee](one-owner-for-each-guarantee.md), whose guarantee 9 owns the checks and reads this policy. It was revised on November 8, 2026, when the harness's own merge queue was switched on in this repository. It was revised again on November 17, 2026, when the merge queue's pace design moved the full race check out of the queue's run on every candidate: the queue now runs a list of checks the project declares, and case 2 below says what that list must show.

## One policy

One typed policy in the checks package decides every check for a run. Its inputs are:

- the base commit and the candidate commit;
- every changed path, where a deleted file counts and a renamed file counts under both its old and its new path;
- the project's path rules;
- an optional analyser command the project declares.

No other surface filters checks on its own. The existing selection of directly touched packages is extended into this policy, not replaced and not run beside it. The one plan of guarantee 9 reads this policy to decide whether each check runs whole, narrowed or not at all.

## What the harness knows, and what the project declares

The harness knows only two things: path rules, which say which checks a path concerns, and an analyser command, which given the changed paths returns the further paths or test groups they affect. A project with neither gets every check run whole, as today.

This repository declares a Go analyser. It returns:

- the packages that depend on each changed package, directly or through others;
- the tests of those packages;
- the test data and embedded files those packages read;

for both the macOS and the Linux build variants, so a file built on only one platform is never missed.

Tests that read documents, templates or scripts are declared in the path rules by the paths they read, so a change to such a file selects them.

Some tests guard code outside their own package, so the analyser cannot find them from the changed paths. They are declared in the path rules by the paths they guard. In this repository, any changed Go file selects the tests of `internal/runstate`. The check that refuses a whole save of a run's record lives there, and it guards against such a save added in any package, such as `internal/orchestrator`. A test run narrowed to the packages a change touches would otherwise skip it. This rule applies wherever tests are narrowed, the merge queue's declared list included.

## When the whole suite runs

The whole suite runs, with nothing narrowed, whenever any of these holds:

- a module, dependency or toolchain file changed;
- the Makefile, a CI workflow or the check configuration changed;
- a changed path is claimed by no path rule and no analyser result;
- the analyser failed, timed out, or said it cannot tell;
- the base commit is not an ancestor of the candidate.

When in doubt, everything runs. A narrowed run is only ever the result of complete evidence.

## What is never narrowed

Formatting and vet always run. A check is skipped as having nothing to check only when every changed path is claimed by a rule that says so; an empty selection reached any other way runs the check whole.

Selection applies to a developer run's checks before review. The checks before promotion, and CI on pull requests and on main, are never narrowed. The one exception is the race check on a merge candidate in the harness's own queue, in case 2 below. There, the race check is either taken from the approved change's own whole run, or run narrowed on the candidate. Every other check that verifies a merge candidate runs whole, on this machine, in the harness's merge queue, or on the forge.

### What verifies the merge candidate

The merge candidate is the change combined with the current target branch, as it is about to land. Which run verifies it for the full race check depends on the four cases below. Every other check before promotion runs whole on the candidate in every case, on this machine or in the queue.

1. **No merge queue verifies the candidate.** Integration through a merge queue is off. Every check before promotion runs whole on this machine, including the full race check.
2. **The harness's own merge queue verifies the candidate.** This is how this repository runs from November 8, 2026, when the harness's own queue was switched on (yoyodyne-ifd.429.59.7). The local full race check on the merge candidate does not run when all of these hold at that landing:
   - integration through the harness's own merge queue is on;
   - the queue ran the project's declared list of queue checks, `merge_queue_checks`, whole over the candidate, and every check in it passed;
   - one of these two is shown:
     - the approved head of the change has a passing result of the full race check;
     - the queue ran the project's narrowed race check, `merge_queue_race_check`, over the candidate, and it passed;
   - the commit the queue checked is exactly the commit that lands.

   The harness reads all of these from its own records, not from the forge. The run's record names the queue's run as the one that verified the candidate for the race check, in `checks_passed.whole_by`, together with which of the two race results it relied on.

   This case accepts less than a full race check on the combined change, and the cost is stated so it can be weighed. A race that appears only when the change meets what landed on main after its head was checked is not caught before landing. The scheduled full race check on main catches it after landing, within four hours of main moving. That is accepted because the queue could otherwise land only about one change an hour, as [the merge queue's pace design](merge-queue-pace.md) records.
3. **The forge's merge queue verifies the candidate.** This applies to a project that integrates through the forge's merge queue rather than the harness's own. The local full race check on the merge candidate does not run when both conditions of guarantee 9's race check ruling hold:
   - the forge's required check runs the full suite under the race detector;
   - it runs on the change combined with current main, through the forge's merge queue.

   The project declares both in its configuration, and the harness confirms both from the forge's settings at this landing and records that it did. A forge rule that a branch must be current before it merges does not by itself meet the second condition; only the forge's merge queue does.
4. **A merge queue is on, but its conditions are not shown to hold.** This covers any of these cases:
   - a condition of case 2 or case 3 is not met;
   - a condition the project had to declare was not declared;
   - the forge's settings show a condition to be false, or could not be read at this landing;
   - the harness's queue did not run its declared list whole, or neither race result of case 2 is shown;
   - the queue checked a commit other than the one that lands.

   The local full race check runs on the merge candidate, as in case 1. The harness records which condition it could not confirm, and why.

The local full race check is dropped only in cases 2 and 3. That is the Lead Product Manager's decision of October 25: the drop takes effect only once integration through a merge queue is switched on. In this repository that happened on November 8 with the harness's own queue. This repository took the forge's merge queue rule off main for it, so case 3 does not apply here.

The full race check also runs on main every four hours when main has moved, as guarantee 9's race check ruling says. In cases 1, 3 and 4 that run is only a backstop. In case 2 it is also what catches a race the candidate's own checks did not, as stated there.

The independent review is unchanged in every case.

## What is recorded

Every run records, for each check, whether it ran whole, ran narrowed or was skipped, and why: the rule or analyser result that decided it, or the reason the whole suite was forced. For the merge candidate it also records which of the four cases above applied. For case 2 it records:

- the queue's run and the commit it checked;
- the declared list it ran and that list's digest;
- which race result it relied on, the approved head's or the narrowed race check's.

For case 3 it records that both conditions were confirmed from the forge at that landing. For case 4 it records which condition was not shown and why. The record is kept with the run's other check results and is shown where those are shown.

## Rollout

1. **Shadow.** Selection is computed and the narrowed checks run, and the whole suite runs beside them. Any failure the whole suite found that the narrowed run did not is recorded as a miss, with the change that caused it. Shadow runs of the whole suite each take a permit for heavy checks, like any other heavy check.
2. **Switch on.** Selection is used for real only after 50 shadow runs with no miss, spanning narrow, broad, non-Go and concurrent changes.
3. **Switch off.** A setting turns selection off at any time and restores today's behaviour exactly. Any miss found after rollout switches it off and is reported to the development manager.

Coverage, CPU time and wall time are measured separately, before and after, and the cost of selection itself is counted in them. No claim of benefit is made before those measurements.

## How this fits the other designs

- **[One owner for each guarantee](one-owner-for-each-guarantee.md)** owns checks as guarantee 9. Its one plan reads this policy, its one runner launches what the plan names, and its race check ruling is this repository's application of both. Where a result is kept across commits rather than the check run again, that is guarantee 9's and repeated checks', not narrowing.
- **[Repeated checks](repeated-checks.md)** removes true duplicate runs of the same commit; narrowing release packaging or the adoption check later uses this policy rather than a filter of its own.
- **[Checks on CI](checks-on-ci.md)** keeps a short local check; tests that only behave correctly on macOS stay in it unless CI gains a macOS job.
- **[Capacity for heavy checks](heavy-check-capacity.md)** bounds how many heavy checks run at once; shadow runs count against it.
- **[Integration through a merge queue](integration-through-a-merge-queue.md)** is the switch that decides whether case 1 or one of cases 2 to 4 above applies. Its queue runs the declared list and the narrowed race check that case 2 names.
- **[The merge queue's pace](merge-queue-pace.md)** is why case 2 no longer needs the full race check on every candidate.
- **[The document landing route](document-landing-route.md)** already runs only the checks that read documents for a change confined to documents, so this design does not handle that case.

## Alternatives rejected

- **A filter for each surface** (the developer run, release packaging, CI). Rejected because each would drift from the others and none would be explainable as a whole; one policy gives one record of why.
- **Building Go analysis into the harness.** Rejected because projects Yoyodyne manages are not assumed to be Go; the analyser is a command the project declares.
- **Narrowing the checks before promotion or in CI.** Still rejected, with the one exception of the race check in case 2. Letting the forge's required check be the whole run for the race check, in case 3, is not narrowing: the same full suite still runs whole on the same combined change, on the forge rather than in the run's own landing step.
- **Running the full race check on every candidate in the harness's queue.** Rejected on November 17. At about an hour each, it held the queue to about one landing an hour. The approved head's full race result, or a narrowed race check on the candidate, together with the scheduled run on main, is the cost accepted instead.
- **Accepting the approved head's race result alone, with no check on the candidate.** Rejected because nothing would then show the declared checks passed on the combined change; case 2 still needs the declared list to run whole on the candidate.
- **Leaving tests that guard other packages to the analyser.** Rejected because the analyser finds tests from the packages a change touches. A check that guards every package, such as the check on how a run's record is written, would be skipped for exactly the change it exists to catch.
- **Dropping the local full race check on the merge candidate before integration through a merge queue is on.** Rejected by the Lead Product Manager on October 25. Without a queue, nothing shows that another run was on the change combined with current main at the moment it lands.
- **Requiring the forge's merge queue even where the harness's own queue checks the candidate.** Rejected on November 8. This repository took the forge's queue rule off main for the harness's queue, so the forge conditions could never be confirmed.
- **Skipping the local full race check when a condition cannot be confirmed.** Rejected because when in doubt, everything runs, and a race on the combined change would otherwise be caught only by the run on main, after it landed.
- **Rolling out without a shadow period.** Rejected because a miss lands an untested change, and only a shadow comparison shows the analyser is complete.

## Builds

1. The policy and its records.
2. Path rules, with this repository's rule that any changed Go file selects the tests of `internal/runstate`.
3. The analyser interface and this repository's Go analyser.
4. Shadow selection before review, with miss recording.
5. The switch and the miss report.

The cases for the merge candidate are built with guarantee 9's plan, in migration item 17 of the guarantees design, not as a separate piece here. Case 2, for this repository, is the work on dropping the local full race check on landing (yoyodyne-ifd.428.126.6.5), and the queue's declared checks (yoyodyne-ifd.429.142).

## How a reviewer checks an implementation

- No code outside the policy decides which checks run.
- Each case in the list of when the whole suite runs, given as a test, produces a whole run.
- Formatting and vet run on every change, and the checks before promotion and in CI are never narrowed, apart from the race check of case 2.
- A change to a Go file outside `internal/runstate`, with tests narrowed, still runs the tests of `internal/runstate`.
- With integration through a merge queue off, the full race check runs locally on every merge candidate.
- With the harness's own queue on, the local full race check on the merge candidate is skipped only when the queue ran its declared list whole and all passed, the approved head has a passing full race result or the narrowed race check passed on the candidate, and the queue checked exactly the commit that lands. The record names the queue's run in `checks_passed.whole_by`, the declared list and its digest, and the race result relied on. A test shows that a queue run missing a check from its list, with neither race result, or on another commit, runs the full race check locally.
- With the forge's merge queue on, it is skipped only when both forge conditions are declared and confirmed from the forge at that landing. A condition undeclared, shown false or unreadable runs it locally.
- Every check in a run's record says whole, narrowed or skipped, with its reason, and the merge candidate's record names its case.
- With the switch off, the checks a run performs are exactly today's.
- Shadow runs record misses and each take a permit for heavy checks.