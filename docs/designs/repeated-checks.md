---
id: repeated-checks
kind: design
title: 'Repeated checks: when two runs are the same run, and the first one removed'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-13T11:29:35.922877Z
      reason: 'yoyodyne-ifd.429.53 - the audit of repeated checks Mason asked for on October 3: what makes two runs equivalent, the inventory of every stage that runs checks, which pairs are required coverage and which is removable, conservative fallback, one bounded first implementation in shadow behind a switch, and the measurements before rollout; no live check is removed by this design'
    - action: amended
      by: architect
      at: 2026-10-27T00:37:16.945881Z
      reason: 'Brings this design into line with guarantee 9 of the design for one owner for each guarantee, landed October 25: the checks component decides every skip and kept result; a passed result may also be kept for a later commit where the project''s analyser shows with complete evidence that nothing the check covers changed, compared on the change combined with the new target branch when it moved, measured over 50 runs before it is switched on; the before-review and before-promotion pair and the reviewer checks updated to match. The first removal is unchanged. Written from reads at main 89284e43.'
    - action: amended
      by: architect
      at: 2026-11-21T05:32:47.612549Z
      reason: 'First of three designs renamed for the README walk check (yoyodyne-ifd.437.67): the check called the adoption check is now getting-started on main (yoyodyne-9f4), so this section names it that way. Nothing else changes. Read whole at main 3e45c2a2.'
    - action: amended
      by: architect
      at: 2026-11-22T05:54:36.018658Z
      reason: Brings this design into line with the code from the comparison findings for the checks designs (docs/diagnoses, group 4; the second reading stands). Says the single rerun of untouched tests is the one built rule. Redraws the inventory for checks on CI and the harness's merge queue. Restates the one possible duplicate as the candidate pull request against the push to main, with the commit condition the trial week records. Records that the getting-started walk already has its own path check. Read at main 62d9a16a5.
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-13T11:29:35.922877Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 782, for document-782.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-27T00:37:16.945881Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1823, for document-1823.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-11-21T05:32:47.612549Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3299, for document-3299.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 3
      by: harness
      at: 2026-11-22T05:54:36.018658Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3407, for document-3407.1 under the automatic approval policy
---
# Repeated checks: when two runs are the same run, and the first one removed

## What this is for

A change is checked at several stages, and some of those runs look alike. Mason asked on October 3 for an audit of them: which repetitions are true duplicates that can go, and which only look alike and are required coverage. Removing a check that was covering something is how an unverified change lands, so this design is conservative: a run is removed only when evidence proves an identical run already passed, or that nothing it covers has changed since one did, and anything uncertain runs. It serves the goal of a traceable chain from the brief through goals, designs, work, code changes and verification, and keeps every existing requirement: full verification of the actual merge candidate, the behaviour that only macOS shows, formatting and vet, independent review, and a record of what ran. No live check is removed by this design. Most of what follows is not built; the next sections say which part is.

## Who decides

Every decision this design describes, whether a run is skipped, whether a result is kept for a later commit, and what a result proves, is made by the checks component, `internal/checks`, under guarantee 9 of [the design for one owner for each guarantee](one-owner-for-each-guarantee.md). No execution path decides it for itself. A result counts as evidence only when the checks component recorded it, with the five things below.

## What is built

One rule of this kind is built. A check failure confined to tests the change does not touch is run once more on the same commit before any repair attempt is spent (`internal/checks/untouchedrerun.go`, `scripts/rerun-failing.sh`, described in the user guide in `docs/configuration.md`). The scheduled race check on main likewise runs only its failing tests once more (the `Makefile`). Both run the same check again rather than skip one, so they add evidence rather than remove it.

Nothing else here is built. There is no record of the five things below, no kept result, and no skipped run: `.github/workflows/ci.yml` still runs on every push to main.

## When two runs are the same run

Two runs of a check are equivalent only when all of these match:

- the exact commit checked, by its identity, not its branch;
- the check's definition: the command, and the digest of the workflow or configuration that declares it;
- the toolchain: the language and tool versions the run used;
- the platform: operating system and architecture;
- the relevant environment: any setting or variable the check declares it reads.

A run on a different commit, platform or toolchain is not a duplicate, however similar it looks. Two commits with the same files are still two commits. A run whose record lacks any of these is treated as not equivalent to anything.

## Keeping a result for a later commit

Not built. A passed result is also kept for a later commit, without running the check again, when the project's declared analyser says, with complete evidence, that nothing the check covers changed between the commit that passed and the later one. The other four things above must still match. When the target branch has moved, the comparison is made on the change combined with the new target branch, not on the change alone. A path the analyser cannot place, an answer it cannot give in full, or any other doubt runs the check. The kept result is recorded with the commit it passed on and the analyser's answer beside it.

It rolls out the way narrowing does under [choosing checks by change](choosing-checks-by-change.md): while it is measured, the answer is computed and recorded and the check runs anyway. It is switched on only once 50 such runs show no case where the analyser said nothing changed and the check then failed. A setting restores running every time. No analyser is declared in this repository yet, so nothing here can start before one is.

## The inventory

These are the stages that run checks in this repository, with checks on CI switched on, as the comparison findings for the checks designs read them:

| Stage | Where it runs | Commit checked |
| --- | --- | --- |
| A developer's own probes in its session | macOS, this machine | the developer's working state |
| The short local check before review: formatting, vet, build, and the tests only macOS shows | macOS, this machine | the candidate commit |
| The required checks on the run's pull request before review | Ubuntu, GitHub | the pull request's head |
| The harness's merge queue: its quick local checks, and the narrowed race check where no required check is a race check | macOS, this machine | the merge candidate on current main |
| The required checks on the merge candidate's own pull request | Ubuntu, GitHub | the candidate branch's head |
| CI on the push to main | Ubuntu, GitHub | main after the landing |
| The scheduled race check on main, every four hours | macOS, this machine | main |

A project with checks on CI switched off runs the full checks on this machine before review and over the merge candidate before it lands, as [checks on CI](checks-on-ci.md) describes; the pairs below then shift to those local runs.

What each pair is:

- **Developer probes and the short check before review**: different states, and the probes are the developer's own; not duplicates, and the harness does not count the probes as evidence.
- **The required checks before review and on the merge candidate's pull request**: equivalent only when the candidate is the very commit that was reviewed, on a main that has not moved. Where main moved, they check different commits. Once keeping a result by the analyser's answer is switched on, a check whose covered code did not change on the change combined with the new main keeps its earlier result; every other check runs again.
- **Any run on macOS and any run on Ubuntu**: different platforms; both are required, because some behaviour (the keychain, launchd, file system and process handling) is only exercised on macOS.
- **The required checks on the merge candidate's pull request and CI on the push to main**: when the queue lands a candidate, what it checked becomes main. If main after the landing is the candidate's own commit, the push run checks the identical commit under the identical workflow on the identical platform, and **this is the one possible duplicate the inventory finds**. If the landing makes a new commit, the push run checks a different commit with the same files, and under the rule above it is not a duplicate. Which of the two happens in this repository was not read for this revision.

An earlier revision named GitHub's own merge queue run as the duplicate. This repository no longer lands through GitHub's merge queue, so that pair no longer exists here.

## Fallback

Every decision to skip a run is made from recorded evidence and falls back to running the check when anything is missing or different: no record, a different commit, a workflow file whose digest changed, a different runner image, a run that ended in anything but a pass, an analyser answer that is incomplete, or evidence the harness cannot read. A skipped run is recorded with the evidence it relied on, so the record of what verified a commit stays complete. A required check always reaches a final state, pass, fail or skipped with evidence, and nothing merges on a check left without one.

## The first removal

Not built. The first bounded implementation is the possible duplicate above: the CI run on the push to main is skipped when, and only when, main after the landing is the very commit whose candidate pull request passed every required check, under a workflow file with the same digest on the same runner image. It is done in two steps:

1. **Shadow.** For one week the push run still runs; beside it the harness records whether main was the checked commit, whether the run would have been skipped, and whether the two results agreed.
2. **Removal.** Only if every case where it would have been skipped agreed is the skip switched on. A setting restores today's behaviour at any time, and any disagreement found later switches it back off and is reported to the development manager.

If the shadow week finds no landing where main is the checked commit, the removal saves nothing and is dropped. It is not widened to comparing the files of two commits: that would be a new decision, recorded in this design before anything is built for it.

Release packaging is not touched. The `getting-started` check, which walks the README, already has a filter of its own on this machine: a path check that runs it only when the paths in `scripts/walk-adoption.paths` change. On CI its job runs on every pull request. This removal touches neither. Moving that path check under the one shared selection policy is [choosing checks by change](choosing-checks-by-change.md)'s work.

## Measurements

Before and after the first removal, for representative narrow, broad, non-Go and concurrent changes, record CPU time and wall time for each stage, the coverage each stage provides, and changes landed per day. The shadow week's agreement rate is the rollout condition for the first removal, and the 50 measured runs are the rollout condition for keeping results by the analyser's answer; the measurements are what any claim of benefit rests on, and none is made before them.

## How this fits the other designs

- **[The design for one owner for each guarantee](one-owner-for-each-guarantee.md)** owns these decisions under guarantee 9 and decides where this design and it differ.
- **[Checks on CI](checks-on-ci.md)** is switched on in this repository and decides the stages in the inventory above. Tests that only behave correctly on macOS stay in the short local check unless the CI workflow gains a macOS job, because Ubuntu runs cannot stand in for them.
- **[Choosing checks by change](choosing-checks-by-change.md)** rests on the same analyser and the same rollout of 50 measured runs. Neither is built.
- **Capacity for heavy checks** bounds how many run at once; this design reduces how many run at all. They do not overlap.
- **The document landing route** already runs only the checks that read documents for a change confined to documents.

## How a reviewer checks an implementation

- During the shadow week the push run still runs, and each run's record shows whether main was the checked commit, whether the run would have been skipped, and whether the results agreed.
- With the switch on, the push run is skipped only where main is the very commit whose candidate pull request passed under the identical workflow digest and runner image, and the skip is recorded with that evidence.
- A result is kept for a later commit by the analyser's answer only once 50 measured runs showed no miss, only where the answer was complete, and with the comparison made on the change combined with the new target branch where it moved; the kept result names the commit it passed on and the answer.
- Any missing or mismatched evidence makes the check run.
- No code outside `internal/checks` decides that a run is skipped or a result kept.
- The single rerun of tests a change does not touch still happens, and is not counted as a skip.
- The switches off restore today's behaviour exactly.

## Evidence

This revision was written from the comparison findings for the checks designs under `docs/diagnoses`: a first reading at main fe2db3d37 and a second reading at main 848d86190, the second standing where they differ, both read at main 62d9a16a5. The code was not read again for this revision. In particular, how a landing from the harness's merge queue is recorded on main, and so whether the push run ever checks the candidate's own commit, was not read.