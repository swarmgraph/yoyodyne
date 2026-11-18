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
    - action: amended
      by: architect
      at: 2026-11-15T04:36:43.903228Z
      reason: 'Answers the reviewer''s finding on run-f76cf179: with checks on CI, the rule that review waits for every required check governs the review before integration; a merge-queue candidate follows the merge-queue design, where the second review runs beside the wait and a failing required check stops it, and a required check cancelled, skipped or never started is asked to run once more before the local fallback. Read whole at e3b627a4.'
    - action: amended
      by: architect
      at: 2026-11-18T16:42:25.905442Z
      reason: 'yoyodyne-ifd.437.70: the short local check names exact commands from the project''s checks list, as the landed setting requires (amendment-07077ea5, report decf67ef); required check names must be the forge''s own names, and a required check that never starts on three runs in a row files a warning for the development manager (report f8a0276f, new build 9); a CI failure''s output is held to 8 KiB in total across failing checks, the run record''s limit, instead of 16 KiB each (amendment-15e93515, report 4232f3eb). Read whole at main 160942a0.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-14T04:19:53.595166Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 878, for document-878.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-11-15T04:36:43.903228Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2757, for document-2757.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-11-18T16:42:25.905442Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3021, for document-3021.1 under the automatic approval policy
---
# Checks on CI: a short local check, the full suite on the forge before review, and a switch back to local checks

Today every change is tested three times: on this machine before review (`make fmtcheck`, `make test`, `make race`, `make vet`, 15 to 30 minutes or more when four runs go at once, and the main cause of checks stopped by their time limit), again by CI on the pull request (about 8 minutes), and again by CI in the merge queue (about 8 minutes). The repository is public, so CI time costs nothing. Mason directed on October 5 that most of a run's testing move to CI. This design says how, and how to switch back.

It serves the v1 goal to isolate implementation tasks in harness-managed Git worktrees and integrate successful work automatically: fewer checks stopped by load means fewer runs stopped for reasons outside their work. It keeps the goal that verification is whatever commands the project declares: the harness learns nothing about any build system, and a project with no CI, or on a forge the harness cannot read checks from, keeps today's behaviour.

## The switch

```yaml
execution:
  ci_checks:
    enabled: false
    local: [make fmtcheck, make vet]
    required: [test, race]
    wait_limit: 60m
```

The names in this example are illustrations. A project copies its `local` entries from its own `checks` list and its `required` names from the checks its forge shows on its own pull requests.

- `enabled` is false by default. False is today's behaviour exactly: no push before review, no wait, the full declared checks run on this machine before review. Flipping it back at any time restores that.
- `local` names the declared checks that form the short local check. Each entry is written exactly as the project's `checks` list writes the command, such as `make fmtcheck`, not a shortened name such as `fmtcheck`. An entry that matches no command in `checks` is refused when the configuration is read.
- `required` names the forge's checks that must pass on the candidate commit. It must not be empty. Each name is written exactly as the forge shows the check on the project's pull requests; on GitHub that is the name of the job in the workflow. The harness counts only these, so another check on the pull request that the project did not name neither blocks nor passes a change. A name the forge never shows is never satisfied, so every run falls back to the local checks while the project appears to use CI; the warning described under When CI is down or slow makes that visible.
- `wait_limit` is how long the harness waits for the required checks to finish, measured from the push.

The configuration is refused when it is read if `enabled` is true and the project's forge adapter cannot push a branch, open a draft pull request, and read check results and their output for a named commit. The setting is the development manager's to change, under section 6 of the portable agent configuration design, through the reviewed path.

## What runs where, with the switch on

1. **The developer's first check is unchanged.** Before any edit the developer runs the declared checks' entry point, as the rule on developers verifying before submitting requires.
2. **The short local check.** When the developer submits, the harness runs the checks named in `local` on the candidate commit, on this machine. A failure goes back to the developer as any failed check does today. These checks are not heavy unless the project declares them so, so they take no permit under the capacity design for heavy checks.
3. **The push.** When the short check passes, the harness pushes the run's branch and opens its pull request as a draft, or updates it. No agent pushes; the harness does, as it already does at publication. The pushed commit is the candidate commit, recorded by its identity.
4. **The wait.** The run waits for every check in `required` to finish on that exact commit. The run is shown as waiting for CI, with the time waited, on the dashboard and in `yoyo status`.
5. **Review.** Review starts only when every required check has passed on that commit. A reviewer never sees a change CI rejected. The reviewer's evidence includes each required check's name, result and the commit it ran on. This is the review before integration. A merge-queue candidate, whose change has already passed this review, is reviewed again as [the merge queue design](integration-through-a-merge-queue.md) says, described under How this fits the other designs below.
6. **After approval.** The pull request is marked ready, and integration proceeds as configured.

For this repository, `local` holds formatting, vet, a build, and a target that runs the tests only macOS exercises (the keychain, launchd, file system and process handling), each written as the exact command from this repository's `checks` list. CI runs on Ubuntu and cannot stand in for those tests, so they stay local unless the CI workflow gains a macOS job. The build of this repository's configuration names that target, and names in `required` only jobs this repository's CI workflow actually has.

## The developer slot during the wait

The developer slot is released when the push succeeds. Waiting holds no slot and no permit. When CI fails, the repair takes a slot again like any repair, and resumes the developer's session where the provider allows it. A slot held through an eight minute wait would sit idle while ready work waits for it.

## A CI failure goes back to the developer

When a required check fails, the harness fetches through the forge adapter, because a developer's sandbox has no network:

- the name of each failing check and its result;
- the failing tests, where the output names them;
- the end of each failing check's output, at most its last 200 lines, with the output of all the failing checks together held to 8 KiB, the limit a run record keeps for a failed check's output. Where they are over it, the 8 KiB is shared equally between the failing checks, each keeping its last lines.

The output is scrubbed of secrets before it is written anywhere. It is recorded on the run as a failed check, in the same shape as a local failed check with its failing tests, and spends one repair round as a local failure does. The developer receives it the same way. After the repair, the cycle starts again at the short local check, and the new commit needs its own CI result: a result for an earlier commit is never carried forward.

## When CI is down or slow

These are outside the work, and none of them spends a repair round:

- a required check has not finished within `wait_limit`;
- a required check ends cancelled, skipped, or in any state other than passed or failed;
- a required check never starts for the commit;
- the forge cannot be read.

In each case, before the review that precedes integration, that run falls back to the full declared checks on this machine, recorded on the run with the reason, and the fallback's heavy checks take permits like any local check. At the merge-queue stage, a required check that was cancelled, skipped or never started is first asked to run again once, as the merge queue design says, and the full local checks run only if it still has not passed. If a CI result for the same commit arrives later, it is recorded too, and a failure from either counts as a failure. If the push itself is refused, the run is held for the development manager with the forge's refusal, because integration would need the same forge.

When the same required check has never started on three runs in a row, the harness files a warning report for the development manager naming the check and saying the forge has not shown it on those runs' commits. A name that matches no job in the project's CI, or a job that does not run on pull requests, otherwise sends every run to the local checks with nothing but a recorded reason on each run to say so. One report is filed for each such check until it starts on a run again.

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

- **Integration through a merge queue.** Every stage where the harness runs checks follows the switch, not only the stage before review. A queue candidate is checked as [the merge queue design](integration-through-a-merge-queue.md) says in its section on GitHub's required checks as the queue's full check: the short local check, then the harness pushes the candidate and the required checks run on that exact commit. There the second review runs beside the wait rather than after it, because the change has already passed review and its full checks once, so a failure on CI at that stage is rare and waiting would lengthen every entry's turn; a failing required check stops that review. A replayed revision with the queue off gets the short local check, then the harness pushes it and waits for the required checks on that commit before its review. Where the replayed revision is the commit already reviewed on an unchanged base, its evidence already exists and nothing runs again. The promotion lease is never held during a CI wait.
- **Checks stopped by their time limit.** The 60 minute stage limit and scaling for machine load apply to local checks only. The CI wait has its own `wait_limit`. A run stopped during the wait continues at the wait. Failing tests are recorded for a CI failure as for a local one.
- **Capacity for heavy checks.** The full suite on CI takes no permit; the short check takes one only if declared heavy; the fallback takes permits.
- **Choosing checks by change.** Narrowing applies to local checks before review, so with the switch on it applies to the short check and the fallback. CI is never narrowed.
- **Repeated checks.** With the switch on, the harness's local full run before review and CI on the pull request become one run, which is the saving. The push run on main and its removal are unchanged.
- **The document landing route.** A change confined to documents still runs only the checks that read documents, and needs no CI wait unless the project names those checks in `required`.

No parked build is made unnecessary: the switch back and the fallback both run the full suite on this machine, so faster local checks still matter. Their benefit with the switch on is smaller, and when to build them is the Lead Product Manager's to weigh.

## Alternatives rejected

- **Starting the review before integration while CI runs.** Rejected because a reviewer would judge changes CI then rejects, spending a review on each. This does not apply to the second review of a merge-queue candidate, whose change has already passed CI and review once; there the merge queue design runs the review beside the wait and stops it on a failing required check.
- **Holding the developer slot during the wait.** Rejected because the slot sits idle while ready work waits.
- **Failing the run when CI is down.** Rejected because the cause is outside the work, and spending a repair round on it punishes the change.
- **Counting every check on the pull request.** Rejected because checks the project did not declare would decide integration; the project names what counts.
- **Running the full suite both locally and on CI.** Rejected because it keeps the load this design exists to remove.
- **Keeping 16 KiB of output per failing check and raising the run record's limit.** Rejected because the end of a check's output is what names the failure, the record's limit already holds for local failures, and one limit for both keeps a CI failure in the same shape as a local one.
- **Refusing a `required` name the forge has never shown, when the configuration is read.** Rejected because reading the configuration has no forge access, and a new CI job has not yet run when its name is first added; the warning after three runs catches the same mistake without refusing a correct setting.

## Builds

The development manager decomposes these into work items:

1. The setting and its validation, including refusal for a forge adapter that cannot do what the switch needs, and refusal of a `local` entry that matches no command in `checks`.
2. The forge adapter: push the run's branch and open or update a draft pull request; read required check results and failing output for a named commit. GitHub first.
3. The wait as a recorded run state: slot release, restart, switching off, and the wait shown on the dashboard and in `yoyo status`.
4. A CI failure recorded as a failed check with its output, scrubbed, held to 8 KiB in total across failing checks, spending a repair round.
5. The fallback to local checks, with its reasons recorded.
6. The same flow at the replay stage; at the queue stage, the flow the merge queue design gives.
7. CI results as typed evidence bound to the commit, and in the reviewer's evidence.
8. This repository's configuration: the short check as exact commands, including the macOS target, and the required checks named as this repository's CI jobs are named.
9. The warning report for a required check that has never started on three runs in a row, filed for the development manager once per check until it starts again.

## How a reviewer checks an implementation

- With `enabled` false, a run's checks, timing and records are exactly today's.
- A `local` entry that is not written exactly as a command in `checks`, such as `fmtcheck` where `checks` lists `make fmtcheck`, is refused when the configuration is read.
- With it true, the review before integration never starts before every required check passed on the candidate commit.
- At the merge-queue stage, the review may run beside the wait for the required checks, and a failing required check stops it.
- A CI failure reaches the developer with the failing checks, failing tests and the end of the output, scrubbed, and spends one repair round.
- The output recorded for a CI failure is at most 8 KiB across all its failing checks, and at most 200 lines from any one check.
- Before the review that precedes integration, CI not finishing within `wait_limit`, ending cancelled or skipped, never starting, or the forge unreadable each fall back to local checks, spending no repair round.
- At the merge-queue stage, a required check cancelled, skipped or never started is asked to run once more before the local checks run, and no repair round is spent.
- A required check that never starts on three runs in a row files one warning report for the development manager naming it.
- No slot is held during the wait.
- A CI result for a commit other than the candidate gives no credit.
- After a restart during the wait, no new commit is pushed and the wait continues.
- The tests only macOS exercises run locally on every run with the switch on.
