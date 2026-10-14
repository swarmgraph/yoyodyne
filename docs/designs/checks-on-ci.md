---
id: checks-on-ci
kind: design
title: 'Checks on CI: a short local check, the full suite on the forge before review, and a switch back to local checks'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-14T04:19:53.595166Z
      reason: 'yoyodyne-ifd.429.63: Mason''s direction of October 5. An earlier copy (document-756.1) was reported published by run-2a0fffa7 but never reached main in more than a day, so this copy is written again from scratch against main at ca07798b; if the earlier pull request is found, this revision replaces it.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-14T04:19:53.595166Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 878, for document-878.1 under the automatic approval policy
---
# Checks on CI: a short local check, the full suite on the forge before review, and a switch back to local checks

Today every change is tested three times: on this machine before review (`make fmtcheck`, `make test`, `make race`, `make vet`, 15 to 30 minutes or more when four runs go at once, and the main cause of checks stopped by their time limit), again by CI on the pull request (about 8 minutes), and again by CI in the merge queue (about 8 minutes). The repository is public, so CI time costs nothing. Mason directed on October 5 that most of a run's testing move to CI. This design says how, and how to switch back.

It serves the v1 goal to isolate implementation tasks in harness-managed Git worktrees and integrate successful work automatically: fewer checks stopped by load means fewer runs stopped for reasons outside their work. It keeps the goal that verification is whatever commands the project declares: the harness learns nothing about any build system, and a project with no CI, or on a forge the harness cannot read checks from, keeps today's behaviour.

## The switch

```yaml
execution:
  ci_checks:
    enabled: false
    local: [fmtcheck, vet, build, darwin]
    required: [test, race, vet]
    wait_limit: 60m
```

- `enabled` is false by default. False is today's behaviour exactly: no push before review, no wait, the full declared checks run on this machine before review. Flipping it back at any time restores that.
- `local` names the declared checks that form the short local check. Each must be a check the project declares.
- `required` names the forge's checks that must pass on the candidate commit. It must not be empty. The harness counts only these, so another check on the pull request that the project did not name neither blocks nor passes a change.
- `wait_limit` is how long the harness waits for the required checks to finish, measured from the push.

The configuration is refused when it is read if `enabled` is true and the project's forge adapter cannot push a branch, open a draft pull request, and read check results and their output for a named commit. The setting is the development manager's to change, under section 6 of the portable agent configuration design, through the reviewed path.

## What runs where, with the switch on

1. **The developer's first check is unchanged.** Before any edit the developer runs the declared checks' entry point, as the rule on developers verifying before submitting requires.
2. **The short local check.** When the developer submits, the harness runs the checks named in `local` on the candidate commit, on this machine. A failure goes back to the developer as any failed check does today. These checks are not heavy unless the project declares them so, so they take no permit under the capacity design for heavy checks.
3. **The push.** When the short check passes, the harness pushes the run's branch and opens its pull request as a draft, or updates it. No agent pushes; the harness does, as it already does at publication. The pushed commit is the candidate commit, recorded by its identity.
4. **The wait.** The run waits for every check in `required` to finish on that exact commit. The run is shown as waiting for CI, with the time waited, on the dashboard and in `yoyo status`.
5. **Review.** Review starts only when every required check has passed on that commit. A reviewer never sees a change CI rejected. The reviewer's evidence includes each required check's name, result and the commit it ran on.
6. **After approval.** The pull request is marked ready, and integration proceeds as configured.

For this repository, `local` holds formatting, vet, a build, and a target that runs the tests only macOS exercises (the keychain, launchd, file system and process handling). CI runs on Ubuntu and cannot stand in for those tests, so they stay local unless the CI workflow gains a macOS job. The build of this repository's configuration names that target.

## The developer slot during the wait

The developer slot is released when the push succeeds. Waiting holds no slot and no permit. When CI fails, the repair takes a slot again like any repair, and resumes the developer's session where the provider allows it. A slot held through an eight minute wait would sit idle while ready work waits for it.

## A CI failure goes back to the developer

When a required check fails, the harness fetches through the forge adapter, because a developer's sandbox has no network:

- the name of each failing check and its result;
- the failing tests, where the output names them;
- the last 200 lines of each failing check's output, at most 16 KiB each.

The output is scrubbed of secrets before it is written anywhere. It is recorded on the run as a failed check, in the same shape as a local failed check with its failing tests, and spends one repair round as a local failure does. The developer receives it the same way. After the repair, the cycle starts again at the short local check, and the new commit needs its own CI result: a result for an earlier commit is never carried forward.

## When CI is down or slow

These are outside the work, and none of them spends a repair round:

- a required check has not finished within `wait_limit`;
- a required check ends cancelled, skipped, or in any state other than passed or failed;
- a required check never starts for the commit;
- the forge cannot be read.

In each case that run falls back to the full declared checks on this machine, recorded on the run with the reason, and the fallback's heavy checks take permits like any local check. If a CI result for the same commit arrives later, it is recorded too, and a failure from either counts as a failure. If the push itself is refused, the run is held for the development manager with the forge's refusal, because integration would need the same forge.

## Restarts and switching off

Waiting for CI is a recorded state of the run, with the commit, the push time and the required checks. After a restart the harness reads CI for that commit and carries on; it never pushes a different commit and never starts the wait again from zero.

Switching off takes effect at each run's next check stage. A run waiting for CI when the switch goes off stops waiting and runs the full local checks on the same commit.

## Evidence bound to the reviewed revision

The rule that integration requires typed evidence bound to the exact candidate revision holds unchanged. A CI result counts as evidence only when the harness's own action reads it from the forge for the exact candidate commit, for every check in `required`, each passed, and records the digest of the workflow file at that commit. Any later change to the candidate, to `required`, or to that workflow file invalidates it. A result for a pull request's head that is not the candidate commit counts for nothing.

## The rule on developers verifying before submitting

With the switch on, the developer cannot run the full suite before handing to review; CI runs it before review instead. The rule needs this sentence added to its Must hold, which is written when invariants can be submitted from this conversation (yoyodyne-0bj):

> Where the project runs its full checks on CI before review, the relevant checks for submission evidence are the short local check the project declares, and the full suite's evidence is the CI result the harness reads for the same commit before review starts.

Until it is written, an implementation follows this design.

## How this fits the other designs

- **Integration through a merge queue.** Every stage where the harness runs checks follows the switch, not only the stage before review. A queue candidate, or a replayed revision with the queue off, gets the short local check, then the harness pushes the candidate and waits for the required checks on that commit. Where the replayed revision is the commit already reviewed on an unchanged base, its evidence already exists and nothing runs again. The promotion lease is never held during a CI wait.
- **Checks stopped by their time limit.** The 60 minute stage limit and scaling for machine load apply to local checks only. The CI wait has its own `wait_limit`. A run stopped during the wait continues at the wait. Failing tests are recorded for a CI failure as for a local one.
- **Capacity for heavy checks.** The full suite on CI takes no permit; the short check takes one only if declared heavy; the fallback takes permits.
- **Choosing checks by change.** Narrowing applies to local checks before review, so with the switch on it applies to the short check and the fallback. CI is never narrowed.
- **Repeated checks.** With the switch on, the harness's local full run before review and CI on the pull request become one run, which is the saving. The push run on main and its removal are unchanged.
- **The document landing route.** A change confined to documents still runs only the checks that read documents, and needs no CI wait unless the project names those checks in `required`.

No parked build is made unnecessary: the switch back and the fallback both run the full suite on this machine, so faster local checks still matter. Their benefit with the switch on is smaller, and when to build them is the Lead Product Manager's to weigh.

## Alternatives rejected

- **Starting review while CI runs.** Rejected because a reviewer would judge changes CI then rejects, spending a review on each.
- **Holding the developer slot during the wait.** Rejected because the slot sits idle while ready work waits.
- **Failing the run when CI is down.** Rejected because the cause is outside the work, and spending a repair round on it punishes the change.
- **Counting every check on the pull request.** Rejected because checks the project did not declare would decide integration; the project names what counts.
- **Running the full suite both locally and on CI.** Rejected because it keeps the load this design exists to remove.

## Builds

The development manager decomposes these into work items:

1. The setting and its validation, including refusal for a forge adapter that cannot do what the switch needs.
2. The forge adapter: push the run's branch and open or update a draft pull request; read required check results and failing output for a named commit. GitHub first.
3. The wait as a recorded run state: slot release, restart, switching off, and the wait shown on the dashboard and in `yoyo status`.
4. A CI failure recorded as a failed check with its output, scrubbed, spending a repair round.
5. The fallback to local checks, with its reasons recorded.
6. The same flow at the queue and replay stages.
7. CI results as typed evidence bound to the commit, and in the reviewer's evidence.
8. This repository's configuration: the short check, including the macOS target, and the required checks.

## How a reviewer checks an implementation

- With `enabled` false, a run's checks, timing and records are exactly today's.
- With it true, review never starts before every required check passed on the candidate commit.
- A CI failure reaches the developer with the failing checks, failing tests and the end of the output, scrubbed, and spends one repair round.
- CI not finishing within `wait_limit`, ending cancelled or skipped, never starting, or the forge unreadable each fall back to local checks, spending no repair round.
- No slot is held during the wait.
- A CI result for a commit other than the candidate gives no credit.
- After a restart during the wait, no new commit is pushed and the wait continues.
- The tests only macOS exercises run locally on every run with the switch on.
