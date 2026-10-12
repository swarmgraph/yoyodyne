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
      at: 2026-10-12T14:59:15.41232Z
      reason: 'yoyodyne-ifd.429.63 - records the October 11 ruling on moving most of a run''s testing to CI, as Mason directed on October 5: a project setting choosing local or CI checks, a short local check before the push, review only after CI passes, CI failures returned like local ones, fallback to local checks when CI is absent or slow, and one suite time limit both GitHub and the harness honour; resent with joined words the word check refuses written as ordinary words'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-12T14:59:15.41232Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 756, for document-756.1 under the automatic approval policy
---
# Checks on CI: a short local check, the full suite on the forge before review, and a switch back to local checks

## What this is for

Today every change is tested three times: the full declared checks on this machine before review (15 to 30 minutes or more with four runs going, and the main source of check timeouts), again on the pull request (about 8 minutes on the forge), and again in the merge queue (about 8 minutes). The repository is public, so the forge's CI minutes cost nothing, while the local runs cost machine load that slows every other run. Mason directed on October 5 that most of a run's testing move to CI, with a switch that restores today's behaviour exactly. This design serves the goal of isolating work in managed worktrees and integrating successful work automatically, and the autonomy goal: a run should not stop for load its own checks created.

It keeps the rule that verification is whatever commands the project declares. The harness builds no build system and assumes no CI: a project with no CI, or on a forge the harness cannot read check results from, keeps working in local mode.

## The setting

`execution.checks_mode` takes one of two values.

- `local`, the default: exactly today's behaviour. The developer's run executes the project's full declared checks before review, and the harness reads nothing from CI. Flipping back to `local` restores this with no other change.
- `ci`: the behaviour below.

Three further settings belong to `ci` mode: `execution.short_checks`, the project's declared list of short local checks (for this repository: formatting, vet and a build); `execution.ci_start_wait`, default 10 minutes; and `execution.ci_run_limit`, default 45 minutes. Under portable agent configuration section 6 the development manager decides these values through the reviewed path; the operator may also set them by hand. `yoyo doctor` refuses `ci` mode, with the reason, when the project has no forge adapter that reports check results for a commit, or declares no required CI checks; the harness then runs in `local` mode and says so on the run.

## A run in ci mode

1. **Short local check.** The developer runs `execution.short_checks` before submitting. A failure here is a check failure handed back to the developer as today. This is what the invariant on the developer verifying before submitting means in `ci` mode.
2. **Push and wait.** The harness pushes the candidate commit and opens or updates the pull request. The run then waits for the forge's results on that exact commit. While it waits it holds no developer slot and no reviewer slot: the slot is freed at the push and the run waits in a queue of its own, so a developer can start other work.
3. **Evidence.** The harness reads, from the forge, every check the project declares as required, for the pushed commit's identity. Review starts only when every required check has concluded with a pass. The evidence recorded on the run names the commit, each check's name, run identity, conclusion and link. Because forge check results are bound to the commit, the invariant that integration needs evidence bound to the reviewed revision holds unchanged: the reviewer judges that commit, and a new commit needs new results.
4. **Failure.** A required check that concludes as failed goes back to the developer the way a local check failure does today, and spends a repair attempt as one does. The harness fetches the material, because a developer's sandbox has no network: the failing check's name, the failing tests it can identify, the matching failure lines, and the last 4 KiB of the failing step's output. The repair run takes a developer slot again and is carried out ahead of fresh pulls of any priority, as any decided repair holding preserved work is.
5. **Unfinished on CI.** A CI run the forge cancelled, lost, or stopped at its own time limit is not a failure about the change: it is unfinished, earns no credit, and is rerun on CI once. If it is unfinished again, the run falls back to local checks as below. No repair attempt is spent.

## When CI is absent or slow

If no run of a required check has started on the commit within `execution.ci_start_wait`, or a required check is still running after `execution.ci_run_limit`, the run falls back to the project's full declared local checks, under the same capacity rules every local check takes, and records on the run that it fell back and why. From that point the local result decides the run; a CI result that arrives later is recorded beside it and not used. A forge that cannot be reached at all is the forge boundary in the recovery design, recovered inside its window; the fallback times above apply first, so an outage costs a run a local check rather than hours of waiting.

## One time limit for the full suite

The harness's declared limit for the full suite is the one source. A project in `ci` mode sets its CI job's own limit to at least `execution.ci_run_limit`, and `yoyo doctor` reports a CI job limit below it as a mismatch, naming the file and the setting. When the two differ, the forge's limit gives way: it is changed to match. For this repository that means `timeout-minutes` in `.github/workflows/ci.yml` moves from 20 to 45, so a change that passes slowly is no longer cancelled on GitHub with no failing step after approval. The local limits on each check (30 minutes a check, 28 for one package's tests) stay inside it.

## How this fits the other designs

- **The merge queue** (integration through a merge queue). Unchanged in shape: the queue still runs CI on the merged result before landing, and that run is still required. In `ci` mode a change is tested twice, both on the forge, instead of three times with one of them local.
- **Checks stopped by their time limit** (recoverable and terminal failures). Applies to local checks in both modes, including a fallback run. A CI run stopped by the forge's limit is covered by step 5 above.
- **Capacity for heavy checks** (yoyodyne-ifd.429.43). Still needed: it governs local mode and every fallback run. In `ci` mode most of the heavy load leaves this machine, so the parked work that pauses runs on machine load stays parked and is not needed while `ci` mode is on.
- **Selecting checks by what a change touched** (yoyodyne-ifd.429.44). Applies to local mode only. The short check in `ci` mode is a fixed declared list and is not narrowed; the CI suite is always whole.
- **The audit of repeated checks** (yoyodyne-ifd.429.53). Decides whether the pull request's CI run and the merge queue's CI run are both needed; this design keeps both until that audit rules.

## What it amends

- The v1 harness design's verification steps: the checks a developer runs before submitting are the declared checks of the active mode.
- The recovery design: a CI run cancelled, lost or stopped by the forge's limit is unfinished, as in step 5.
- The invariant on the developer verifying before submitting: "verifying" means running the project's declared checks for the active checks mode before submitting; in `ci` mode those are the short checks, and the full suite's evidence comes from the forge before review. That wording is delivered as its own change to the invariant.
- The invariant on integration needing evidence bound to the reviewed revision: no change in wording; forge check results bound to the commit satisfy it.

## Builds

For the development manager to break into bounded work items:

1. The setting, the short check list, and `yoyo doctor` validation, including refusing `ci` mode with no forge results available.
2. Reading required check results for one commit from the GitHub adapter, and the evidence record.
3. The run waiting on CI with no slot held, and resuming it when results arrive.
4. Handing back CI failures with the fetched failure material.
5. The fallback times, the single rerun of an unfinished CI run, and recording a fallback.
6. The CI job time limit check in `yoyo doctor`, and raising `timeout-minutes` in this repository's workflow to 45.
7. Showing a run waiting on CI, and a run that fell back, on the dashboard and in `yoyo status`.

## How a reviewer checks an implementation

- With `local` set, behaviour is identical to before this design: full declared checks before review, nothing read from CI.
- With `ci` set, no developer slot is held between the push and the CI results.
- Review never starts on a commit whose required checks have not all passed on that commit.
- A CI failure reaches the developer with the failing tests and the last 4 KiB of output.
- No CI run within 10 minutes, or one past 45, produces a recorded fallback to local checks whose result decides the run.
- A cancelled or lost CI run spends no repair attempt.
