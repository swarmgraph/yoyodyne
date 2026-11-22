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
    - action: amended
      by: architect
      at: 2026-11-20T20:43:38.056438Z
      reason: 'Point 2 of checking six designs against what has landed (yoyodyne-ifd.437.72): case 2 did not say that, with checks on CI on, GitHub''s required checks on the exact candidate commit can show the queue''s full check, as the quick local checks item (yoyodyne-ifd.429.186) built on November 18, nor that GitHub''s required race check passed on the candidate is a full race result. Case 4, the record, the reviewer lines and the fit list follow. Read whole at main 4e4f5fe9.'
    - action: amended
      by: architect
      at: 2026-11-21T06:51:10.917077Z
      reason: 'Part of rewording the designs that still call the README walk the adoption check, now that the rename to getting-started has landed (yoyodyne-ifd.437.67): the Repeated checks bullet now says the getting started check. Nothing else changes. Written against main at 96c90b9b. The v1 harness design''s mention follows last.'
    - action: amended
      by: architect
      at: 2026-11-22T06:57:28.418764Z
      reason: 'Brings the design in line with the code at main 3b1057ae4bdf, from the second comparison reading for the checks group: says what runs today (three separate filters: Go package narrowing, path checks, the race skip script) and that the one policy, analyser, runstate rule, shadow period, switch and case 3 declarations are not built; the filters move into the policy rather than being removed first; case 3 keeps the declaration; and the queue''s local run with checks on CI can add the narrowed race check.'
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
    - policy: approvals.designs
      revision: 4
      by: harness
      at: 2026-11-20T20:43:38.056438Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3228, for document-3228.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 5
      by: harness
      at: 2026-11-21T06:51:10.917077Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3303, for document-3303.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 6
      by: harness
      at: 2026-11-22T06:57:28.418764Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3421, for document-3421.1 under the automatic approval policy
---
# Choosing checks by what a change touches, through one shared policy

This design says how a developer run's checks may be narrowed to what a change can affect, without ever letting a change land on less verification than it needs. It serves the v1 goal of a traceable chain from the brief through work, code changes and verification: every check a run narrows or skips is recorded with the reason, so the record of what verified a change stays complete. It also serves the goal that the harness supports any language: the harness itself knows only path rules and an analyser command a project declares, and nothing about Go.

It was ruled on October 12, 2026 for the design item on choosing checks by change (yoyodyne-ifd.429.44), and the ruling was recorded on that item. This document is that ruling, with the reasons and the rejected alternatives added. It was revised on October 25, 2026 to agree with [the design for one owner for each guarantee](one-owner-for-each-guarantee.md), whose guarantee 9 owns the checks and reads this policy. It was revised on November 8, 2026, when the harness's own merge queue was switched on in this repository. It was revised again on November 17, 2026, when the merge queue's pace design moved the full race check out of the queue's run on every candidate: the queue now runs a list of checks the project declares, and case 2 below says what that list must show. It was revised on November 20, 2026 to say that, with checks on CI switched on, GitHub's required checks on the exact candidate commit can show case 2's full check instead of that list, as the merge queue's quick local checks (yoyodyne-ifd.429.186) built on November 18. It was revised on November 21, 2026, after a comparison with the code at main 3b1057ae4bdf, to say what of it is built and what runs today.

## What runs today

The merge candidate's cases 1, 2 and 4 below are built. The rest of this design is not built yet: the one policy, the analyser interface, the rule for the tests of `internal/runstate`, the list of when the whole suite runs as written below, the record for each check of whether it ran whole, narrowed or skipped, the shadow period and the switch. Case 3's two declarations are not built either.

What narrows a developer run's checks today is three separate filters, each of its own:

- **Narrowing to the Go packages a change touches.** The harness passes those packages to `make test` and `make race`, chosen in the checks package (`internal/checks/narrow.go`). It widens to the whole module for a change to `go.work` or `go.work.sum`, or where the root is not a Go module. The Makefile and `scripts/check-scope.sh` also widen to the whole module for a change that touches only files outside every Go package, other than Markdown and the tracker export. A change that touches Go packages and also the Makefile, a workflow or the check configuration is not widened.
- **The path checks.** `path_checks` in the project's configuration runs a check only when a changed path matches the list it names. In this repository that is the getting started walk and the Codex resume test.
- **The race skip script.** `scripts/race-skip.sh`, used by the race target, is a further filter of its own.

The Makefile's list of tests that read documents holds the guard tests of `internal/runstate`, including the check that refuses a whole save of a run's record. That list runs beside narrowed packages only when a change also touches a file outside every package, so a change to Go files alone skips those tests before review. They still run on the merge candidate, where the queue's declared checks run whole.

These line references come from a reading of the code made for this revision; the selection code was not read again when it was written.

## One policy

One typed policy in the checks package decides every check for a run. Its inputs are:

- the base commit and the candidate commit;
- every changed path, where a deleted file counts and a renamed file counts under both its old and its new path;
- the project's path rules;
- an optional analyser command the project declares.

Once the policy is built, no other surface filters checks on its own. The three filters that exist today, described above, move into it: the Go package narrowing is extended into the policy rather than replaced or run beside it, the path checks become path rules, and the race skip script's decision becomes a rule or an analyser result the policy records. None of them is removed before the policy makes the same selections, because removing one early would widen every run and slow the factory with nothing more checked. The guarantees design already lists path checks as a check path still to be moved onto its runner. The one plan of guarantee 9 reads this policy to decide whether each check runs whole, narrowed or not at all.

## What the harness knows, and what the project declares

The harness knows only two things: path rules, which say which checks a path concerns, and an analyser command, which given the changed paths returns the further paths or test groups they affect. A project with neither gets every check run whole.

This repository will declare a Go analyser. It returns:

- the packages that depend on each changed package, directly or through others;
- the tests of those packages;
- the test data and embedded files those packages read;

for both the macOS and the Linux build variants, so a file built on only one platform is never missed.

Tests that read documents, templates or scripts are declared in the path rules by the paths they read, so a change to such a file selects them.

Some tests guard code outside their own package, so the analyser cannot find them from the changed paths. They are declared in the path rules by the paths they guard. In this repository, any changed Go file selects the tests of `internal/runstate`. The check that refuses a whole save of a run's record lives there, and it guards against such a save added in any package, such as `internal/orchestrator`. A test run narrowed to the packages a change touches would otherwise skip it, as a change to Go files alone does today. This rule applies wherever tests are narrowed, the merge queue's declared list included.

## When the whole suite runs

Once the policy is built, the whole suite runs, with nothing narrowed, whenever any of these holds:

- a module, dependency or toolchain file changed;
- the Makefile, a CI workflow or the check configuration changed;
- a changed path is claimed by no path rule and no analyser result;
- the analyser failed, timed out, or said it cannot tell;
- the base commit is not an ancestor of the candidate.

When in doubt, everything runs. A narrowed run is only ever the result of complete evidence. Today, a change that touches Go packages and also the Makefile, a workflow or the check configuration is not widened, as described above; the policy closes that difference.

## What is never narrowed

Formatting and vet always run. A check is skipped as having nothing to check only when every changed path is claimed by a rule that says so; an empty selection reached any other way runs the check whole.

Selection applies to a developer run's checks before review. The checks before promotion, and CI on pull requests and on main, are never narrowed. The one exception is the race check on a merge candidate in the harness's own queue, in case 2 below. There, the race check is either taken from the approved change's own whole run, or run narrowed on the candidate. Every other check that verifies a merge candidate runs whole, on this machine, in the harness's merge queue, or on the forge.

### What verifies the merge candidate

The merge candidate is the change combined with the current target branch, as it is about to land. Which run verifies it for the full race check depends on the four cases below. Every other check before promotion runs whole on the candidate in every case: on this machine, in the queue, or on GitHub through its required checks. With checks on CI switched on, as [checks on CI](checks-on-ci.md) describes, the queue's own run on this machine keeps the quick checks GitHub does not run, because GitHub's required checks run the rest whole on the same commit. It also adds the narrowed race check, `merge_queue_race_check`, where none of GitHub's required checks is a race check; in this repository `race` is a required check, so it is not added here. Running on GitHub is not narrowing: the same checks still run whole on the same combined change, on GitHub rather than here.

1. **No merge queue verifies the candidate.** Integration through a merge queue is off. Every check before promotion runs whole on this machine, including the full race check.
2. **The harness's own merge queue verifies the candidate.** This is how this repository runs from November 8, 2026, when the harness's own queue was switched on (yoyodyne-ifd.429.59.7). The local full race check on the merge candidate does not run when all of these hold at that landing:
   - integration through the harness's own merge queue is on;
   - the candidate's full check is shown in one of these two ways:
     - the queue ran the project's declared list of queue checks, `merge_queue_checks`, whole over the candidate, and every check in it passed;
     - checks on CI are switched on and GitHub's required checks stood as the queue's full check, under the four conditions [the merge queue design](integration-through-a-merge-queue.md) sets in its section on GitHub's required checks: the harness read the results for the exact candidate commit, every check the target requires passed, the digest of each workflow file they ran from is recorded, and the target stayed at the candidate's recorded base from the push until the merge; and the quick local checks the queue still runs on the candidate passed;
   - one of these three race results is shown:
     - the target requires a race check of its own on GitHub, and it passed on the exact candidate commit as one of those required checks;
     - the approved head of the change has a passing result of the full race check;
     - the queue ran the project's narrowed race check, `merge_queue_race_check`, over the candidate, and it passed;
   - the commit the queue checked is exactly the commit that lands.

   The harness reads all of these from its own records, and, for GitHub's required checks, from the results it read itself for the exact candidate commit. The run's record names the run that verified the candidate for the race check, in `checks_passed.whole_by`, together with which of the three race results it relied on.

   Where the race result is GitHub's own required race check, the full race check did run on the combined change, on GitHub, and nothing is accepted below a full race check. This repository requires `race` on GitHub and has checks on CI switched on, so that is the result case 2 relies on here.

   Where the race result is the approved head's or the narrowed one, this case accepts less than a full race check on the combined change, and the cost is stated so it can be weighed. A race that appears only when the change meets what landed on main after its head was checked is not caught before landing. The scheduled full race check on main catches it after landing, within four hours of main moving. That is accepted because the queue could otherwise land only about one change an hour, as [the merge queue's pace design](merge-queue-pace.md) records.
3. **The forge's merge queue verifies the candidate.** This applies to a project that integrates through the forge's merge queue rather than the harness's own. The local full race check on the merge candidate does not run when both conditions of guarantee 9's race check ruling hold:
   - the forge's required check runs the full suite under the race detector;
   - it runs on the change combined with current main, through the forge's merge queue.

   The project declares both in its configuration, and the harness confirms both from the forge's settings at this landing and records that it did. The declaration stays because the forge's settings cannot say whether a check runs under the race detector. A forge rule that a branch must be current before it merges does not by itself meet the second condition; only the forge's merge queue does.

   Neither declaration is built. Whether the forge's queue is used is decided today from what the forge adapter can establish, and none of those requirements concerns the full suite or the race detector. Until the declarations are built, case 3 cannot be shown and case 4 applies. Whether the forge route as built runs the local full race check was not checked against the code for this revision.
4. **A merge queue is on, but its conditions are not shown to hold.** This covers any of these cases:
   - a condition of case 2 or case 3 is not met;
   - a condition the project had to declare was not declared;
   - the forge's settings show a condition to be false, or could not be read at this landing;
   - neither way of showing the full check in case 2 holds, or none of its race results is shown;
   - a GitHub result was read for the pull request's head or any commit other than the candidate, or a required check is missing, pending, failed or neutral;
   - the queue checked a commit other than the one that lands.

   The local full race check runs on the merge candidate, as in case 1. The harness records which condition it could not confirm, and why. Where a required GitHub check never started, was cancelled or was skipped and still has not passed after being asked for once more, the merge queue design has the full local checks run on the candidate instead; those include the full race check, so this case is met by that run.

The local full race check is dropped only in cases 2 and 3. That is the Lead Product Manager's decision of October 25: the drop takes effect only once integration through a merge queue is switched on. In this repository that happened on November 8 with the harness's own queue. This repository took the forge's merge queue rule off main for it, so case 3 does not apply here.

The full race check also runs on main every four hours when main has moved, as guarantee 9's race check ruling says. In cases 1, 3 and 4, and in case 2 when GitHub's required race check passed on the candidate, that run is only a backstop. In case 2 with the approved head's or the narrowed race result, it is also what catches a race the candidate's own checks did not, as stated there.

The independent review is unchanged in every case.

## What is recorded

Every run records, for each check, whether it ran whole, ran narrowed or was skipped, and why: the rule or analyser result that decided it, or the reason the whole suite was forced. For the merge candidate it also records which of the four cases above applied. For case 2 it records:

- the queue's run and the commit it checked;
- which way the full check was shown: the declared list it ran and that list's digest, or GitHub's required checks with the candidate commit they were read for, each workflow file's digest, and the quick local checks run;
- which race result it relied on: GitHub's required race check, the approved head's, or the narrowed race check's.

For case 3 it records that both conditions were confirmed from the forge at that landing. For case 4 it records which condition was not shown and why. The record is kept with the run's other check results and is shown where those are shown. The case record, `checks_passed.whole_by`, is built; the record for each check is not.

## Rollout

1. **Shadow.** Selection is computed and the narrowed checks run, and the whole suite runs beside them. Any failure the whole suite found that the narrowed run did not is recorded as a miss, with the change that caused it. Shadow runs of the whole suite each take a permit for heavy checks, like any other heavy check.
2. **Switch on.** Selection is used for real only after 50 shadow runs with no miss, spanning narrow, broad, non-Go and concurrent changes.
3. **Switch off.** A setting turns selection off at any time and restores the behaviour before the policy exactly. Any miss found after rollout switches it off and is reported to the development manager.

Coverage, CPU time and wall time are measured separately, before and after, and the cost of selection itself is counted in them. No claim of benefit is made before those measurements.

## How this fits the other designs

- **[One owner for each guarantee](one-owner-for-each-guarantee.md)** owns checks as guarantee 9. Its one plan reads this policy, its one runner launches what the plan names, and its race check ruling is this repository's application of both. It lists path checks as a separate check path still to be moved, which agrees with this design's account of the filters that exist today. Where a result is kept across commits rather than the check run again, that is guarantee 9's and repeated checks', not narrowing.
- **[Repeated checks](repeated-checks.md)** removes true duplicate runs of the same commit. The getting started check is already narrowed locally by its own path check today; it moves into this policy with the other filters.
- **[Checks on CI](checks-on-ci.md)** keeps a short local check; tests that only behave correctly on macOS stay in it unless CI gains a macOS job. With it switched on, GitHub's required checks on the candidate commit can show case 2's full check.
- **[Capacity for heavy checks](heavy-check-capacity.md)** bounds how many heavy checks run at once; shadow runs count against it.
- **[Integration through a merge queue](integration-through-a-merge-queue.md)** is the switch that decides whether case 1 or one of cases 2 to 4 above applies. Its queue runs the declared list and the narrowed race check that case 2 names, and its section on GitHub's required checks sets the conditions under which those checks stand as the queue's full check.
- **[The merge queue's pace](merge-queue-pace.md)** is why case 2 no longer needs the full race check on every candidate.
- **[The document landing route](document-landing-route.md)** already runs only the checks that read documents for a change confined to documents, so this design does not handle that case.

## Alternatives rejected

- **A filter for each surface** (the developer run, release packaging, CI). Rejected because each would drift from the others and none would be explainable as a whole; one policy gives one record of why. The three filters that exist today are that drift, and they move into the policy.
- **Removing the existing filters before the policy is built.** Rejected because every run would then check the whole module, slowing the factory, with nothing more being checked than the filters already allow.
- **Building Go analysis into the harness.** Rejected because projects Yoyodyne manages are not assumed to be Go; the analyser is a command the project declares.
- **Reading case 3's race condition from the forge's settings alone.** Rejected because the forge's settings name required checks, not what they run; only the project can say its required check runs the full suite under the race detector.
- **Narrowing the checks before promotion or in CI.** Still rejected, with the one exception of the race check in case 2. Letting the forge's required check be the whole run for the race check, in case 3, or GitHub's required checks be the queue's full check, in case 2, is not narrowing: the same checks still run whole on the same combined change, on the forge rather than in the run's own landing step.
- **Counting GitHub's required checks read for the pull request's head.** Rejected because that head is not the candidate; only results read for the exact candidate commit count, as the merge queue design says.
- **Running the full race check on every candidate in the harness's queue.** Rejected on November 17. At about an hour each, it held the queue to about one landing an hour. The approved head's full race result, or a narrowed race check on the candidate, together with the scheduled run on main, is the cost accepted instead, where GitHub does not run the full race check on the candidate.
- **Accepting the approved head's race result alone, with no check on the candidate.** Rejected because nothing would then show the declared checks passed on the combined change; case 2 still needs its full check shown on the candidate.
- **Leaving tests that guard other packages to the analyser.** Rejected because the analyser finds tests from the packages a change touches. A check that guards every package, such as the check on how a run's record is written, would be skipped for exactly the change it exists to catch.
- **Dropping the local full race check on the merge candidate before integration through a merge queue is on.** Rejected by the Lead Product Manager on October 25. Without a queue, nothing shows that another run was on the change combined with current main at the moment it lands.
- **Requiring the forge's merge queue even where the harness's own queue checks the candidate.** Rejected on November 8. This repository took the forge's queue rule off main for the harness's queue, so the forge conditions could never be confirmed.
- **Skipping the local full race check when a condition cannot be confirmed.** Rejected because when in doubt, everything runs, and a race on the combined change would otherwise be caught only by the run on main, after it landed.
- **Rolling out without a shadow period.** Rejected because a miss lands an untested change, and only a shadow comparison shows the analyser is complete.

## Builds

None of these five is built yet.

1. The policy and its records, taking in the Go package narrowing, the path checks and the race skip script without changing what they select.
2. Path rules, with this repository's rule that any changed Go file selects the tests of `internal/runstate`. Today those tests run before review only when a change also touches a file outside every package.
3. The analyser interface and this repository's Go analyser.
4. Shadow selection before review, with miss recording.
5. The switch and the miss report.

The cases for the merge candidate are built with guarantee 9's plan, in migration item 17 of the guarantees design, not as a separate piece here. Case 2, for this repository, is the work on dropping the local full race check on landing (yoyodyne-ifd.428.126.6.5), and the queue's declared checks (yoyodyne-ifd.429.142). Its second way of showing the full check, through GitHub's required checks, was built with the merge queue's quick local checks (yoyodyne-ifd.429.186), which landed on November 18, 2026. Case 3's two declarations are not built and have no work item yet; until they are, a project on the forge's queue falls to case 4.

## How a reviewer checks an implementation

- No code outside the policy decides which checks run; the Go package narrowing, the path checks and the race skip script are read by the policy, not beside it.
- Each case in the list of when the whole suite runs, given as a test, produces a whole run, including a change that touches Go packages and also the Makefile.
- Formatting and vet run on every change, and the checks before promotion and in CI are never narrowed, apart from the race check of case 2.
- A change to a Go file outside `internal/runstate`, with tests narrowed, still runs the tests of `internal/runstate`.
- With integration through a merge queue off, the full race check runs locally on every merge candidate.
- With the harness's own queue on, the local full race check on the merge candidate is skipped only when the queue ran its declared list whole and all passed, the approved head has a passing full race result or the narrowed race check passed on the candidate, and the queue checked exactly the commit that lands. The record names the queue's run in `checks_passed.whole_by`, the declared list and its digest, and the race result relied on. A test shows that a queue run missing a check from its list, with none of the race results, or on another commit, runs the full race check locally.
- With the harness's own queue and checks on CI on, GitHub's required checks count as case 2's full check only when read for the exact candidate commit, every required check passed, each workflow file's digest is recorded, the target stayed at the recorded base, and the quick local checks passed. A test shows that a result read for the pull request's head, or a required check missing, pending, failed or neutral, does not count and the full race check runs locally. A required race check that passed on the candidate is recorded as the race result relied on. Where no required check is a race check, the queue's local run adds the narrowed race check.
- With the forge's merge queue on, it is skipped only when both forge conditions are declared and confirmed from the forge at that landing. A condition undeclared, shown false or unreadable runs it locally.
- Every check in a run's record says whole, narrowed or skipped, with its reason, and the merge candidate's record names its case.
- With the switch off, the checks a run performs are exactly those before the policy.
- Shadow runs record misses and each take a permit for heavy checks.