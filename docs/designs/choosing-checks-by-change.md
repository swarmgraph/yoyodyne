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
---
# Choosing checks by what a change touches, through one shared policy

This design says how a developer run's checks may be narrowed to what a change can affect, without ever letting a change land on less verification than it needs. It serves the v1 goal of a traceable chain from the brief through work, code changes and verification: every check a run narrows or skips is recorded with the reason, so the record of what verified a change stays complete. It also serves the goal that the harness supports any language: the harness itself knows only path rules and an analyser command a project declares, and nothing about Go.

It was ruled on October 12, 2026 for the design item on choosing checks by change (yoyodyne-ifd.429.44), and the ruling was recorded on that item. This document is that ruling, with the reasons and the rejected alternatives added. It was revised on October 25, 2026 to agree with [the design for one owner for each guarantee](one-owner-for-each-guarantee.md), whose guarantee 9 owns the checks and reads this policy.

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

Selection applies only to a developer run's checks before review. The checks before promotion, and CI on pull requests, the merge queue and main, are never narrowed. Each check that verifies a merge candidate runs whole, on this machine or on the forge.

### What verifies the merge candidate in full

The merge candidate is the change combined with the current target branch, as it is about to land. It is always verified in full. Which run does that for the full race check depends on three cases. Every other check before promotion runs whole on this machine in all three.

1. **Integration through the merge queue is off.** This is how this repository runs today. Every check before promotion runs whole on this machine, including the full race check.
2. **Integration through the merge queue is on, and both conditions hold.** The two conditions are those of guarantee 9's race check ruling:
   - the forge's required check runs the full suite under the race detector;
   - it runs on the change combined with current main, through the forge's merge queue.

   The project declares both in its configuration, and the harness confirms both from the forge's settings at this landing and records that it did. The forge's required check is then the whole run that verifies the merge candidate for the race check, and the local full race check on the merge candidate does not run.
3. **Integration through the merge queue is on, but either condition is not shown to hold.** This covers a condition the project has not declared, one the forge's settings show to be false, and settings the harness could not read at this landing. The local full race check runs on the merge candidate, as in case 1. The harness records which condition it could not confirm, and why.

The local full race check is dropped only in case 2. That is the Lead Product Manager's decision of October 25: the drop takes effect only once integration through the merge queue is switched on. For this repository, switching it on is yoyodyne-ifd.429.59.7. Until then, case 1 applies. A forge rule that a branch must be current before it merges does not by itself meet the second condition here; only the forge's merge queue does.

The full race check also runs on main every four hours when main has moved, as guarantee 9's race check ruling says. That run is a backstop and does not replace the checks on the merge candidate in any case.

The independent review is unchanged in every case.

## What is recorded

Every run records, for each check, whether it ran whole, ran narrowed or was skipped, and why: the rule or analyser result that decided it, or the reason the whole suite was forced. For the merge candidate it also records which of the three cases above applied, and for case 2 that both conditions were confirmed from the forge at that landing. The record is kept with the run's other check results and is shown where those are shown.

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
- **[Integration through a merge queue](integration-through-a-merge-queue.md)** is the switch that decides whether case 1 or cases 2 and 3 above apply.
- **[The document landing route](document-landing-route.md)** already runs only the checks that read documents for a change confined to documents, so this design does not handle that case.

## Alternatives rejected

- **A filter for each surface** (the developer run, release packaging, CI). Rejected because each would drift from the others and none would be explainable as a whole; one policy gives one record of why.
- **Building Go analysis into the harness.** Rejected because projects Yoyodyne manages are not assumed to be Go; the analyser is a command the project declares.
- **Narrowing the checks before promotion or in CI.** Still rejected: the merge candidate must be verified in full, and narrowing is only safe where a whole run still follows. Letting the forge's required check be the whole run for the race check, in case 2 above, is not narrowing. The same full suite still runs whole on the same combined change; it runs on the forge rather than here.
- **Dropping the local full race check on the merge candidate before integration through the merge queue is on.** Rejected by the Lead Product Manager on October 25. Without the queue, nothing shows that the forge's run was on the change combined with current main at the moment it lands.
- **Skipping the local full race check when the forge's settings cannot be read.** Rejected because when in doubt, everything runs, and a race on the combined change would otherwise be caught only by the run on main, after it landed.
- **Rolling out without a shadow period.** Rejected because a miss lands an untested change, and only a shadow comparison shows the analyser is complete.

## Builds

1. The policy and its records.
2. Path rules.
3. The analyser interface and this repository's Go analyser.
4. Shadow selection before review, with miss recording.
5. The switch and the miss report.

The three cases for the merge candidate are built with guarantee 9's plan, in migration item 17 of the guarantees design, not as a separate piece here.

## How a reviewer checks an implementation

- No code outside the policy decides which checks run.
- Each case in the list of when the whole suite runs, given as a test, produces a whole run.
- Formatting and vet run on every change, and the checks before promotion and in CI are never narrowed.
- With integration through the merge queue off, the full race check runs locally on every merge candidate.
- With it on, the local full race check on the merge candidate is skipped only when both conditions are declared and confirmed from the forge at that landing, and the record says so. A condition undeclared, shown false or unreadable runs it locally.
- Every check in a run's record says whole, narrowed or skipped, with its reason, and the merge candidate's record names its case.
- With the switch off, the checks a run performs are exactly today's.
- Shadow runs record misses and each take a permit for heavy checks.