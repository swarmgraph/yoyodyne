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
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-13T11:29:35.922877Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 782, for document-782.1 under the automatic approval policy
---
# Repeated checks: when two runs are the same run, and the first one removed

## What this is for

A change is checked at several stages, and some of those runs look alike. Mason asked on October 3 for an audit of them: which repetitions are true duplicates that can go, and which only look alike and are required coverage. Removing a check that was covering something is how an unverified change lands, so this design is conservative: a run is removed only when evidence proves an identical run already passed, and anything uncertain runs. It serves the goal of a traceable chain from goals to verification, and keeps every existing requirement: full verification of the actual merge candidate, the behaviour that only macOS shows, formatting and vet, independent review, and a record of what ran. No live check is removed by this design; it names the first one to remove and the conditions for doing so.

## When two runs are the same run

Two runs of a check are equivalent only when all of these match:

- the exact commit checked, by its identity, not its branch;
- the check's definition: the command, and the digest of the workflow or configuration that declares it;
- the toolchain: the language and tool versions the run used;
- the platform: operating system and architecture;
- the relevant environment: any setting or variable the check declares it reads.

A run on a different commit, platform or toolchain is not a duplicate, however similar it looks. A run whose record lacks any of these is treated as not equivalent to anything.

## The inventory

These are the stages that run checks in this repository, read from the Makefile, the check runner, and the CI and release workflows at c95f6af84aaa:

| Stage | Where it runs | Commit checked |
| --- | --- | --- |
| A developer's own probes in its session | macOS, this machine | the developer's working state |
| The harness's checks before review | macOS, this machine | the candidate commit |
| The harness's checks before promotion | macOS, this machine | the merge candidate on the current base |
| CI on the pull request | Ubuntu, GitHub | the pull request's head |
| CI in the merge queue | Ubuntu, GitHub | the merge commit the queue builds |
| CI on the push to main | Ubuntu, GitHub | main after the merge |

What each pair is:

- **Developer probes and the harness's checks before review**: different states, and the probes are the developer's own; not duplicates, and the harness does not count the probes as evidence.
- **The harness's checks before review and before promotion**: the same platform; equivalent only when the merge candidate is the very commit that was reviewed, which the evidence bound to the reviewed revision already handles. Where the base moved, they check different commits and both are required.
- **The harness's checks on macOS and any CI run on Ubuntu**: different platforms; both are required, because some behaviour (the keychain, launchd, file system and process handling) is only exercised on macOS.
- **CI on the pull request and CI in the merge queue**: different commits unless the queue's merge commit has the same tree as the pull request's head on an unchanged base; in practice both are required.
- **CI in the merge queue and CI on the push to main**: when the queue lands, the commit it tested becomes main. The push run then checks the identical commit, under the identical workflow, on the identical platform. **This is the one true duplicate the inventory finds.**

## Fallback

Every decision to skip a run is made from recorded evidence and falls back to running the check when anything is missing or different: no record, a different commit, a workflow file whose digest changed, a different runner image, a run that ended in anything but a pass, or evidence the harness cannot read. A skipped run is recorded with the evidence it relied on, so the record of what verified a commit stays complete. A required check always reaches a final state, pass, fail or skipped with evidence, and nothing merges on a check left without one.

## The first removal

The first bounded implementation is the duplicate above: the CI run on the push to main is skipped when, and only when, the merge queue's run passed the same commit under a workflow file with the same digest on the same runner image. It is done in two steps:

1. **Shadow.** For one week the push run still runs; beside it the harness records whether it would have been skipped and whether the two results agreed.
2. **Removal.** Only if every shadow case agreed is the skip switched on. A setting restores today's behaviour at any time, and any disagreement found later switches it back off and is reported to the development manager.

Release packaging and the adoption check are not touched by this first step. Narrowing them later needs the shared selection policy of the change-aware verification design (yoyodyne-ifd.429.44), with complete dependency coverage of scripts, templates and configuration, not a filter of its own.

## Measurements

Before and after the first removal, for representative narrow, broad, non-Go and concurrent changes, record CPU time and wall time for each stage, the coverage each stage provides, and changes landed per day. The shadow week's agreement rate is the rollout condition; the measurements are what any claim of benefit rests on, and none is made before them.

## How this fits the other designs

- **Checks on CI** removes the harness's full local run in CI mode. It keeps the short local check on macOS, and tests that only behave correctly on macOS stay in that local check unless the CI workflow gains a macOS job, because Ubuntu runs cannot stand in for them.
- **Capacity for heavy checks** bounds how many run at once; this design reduces how many run at all. They do not overlap.
- **The document landing route** already runs only the checks that read documents for a change confined to documents.

## How a reviewer checks an implementation

- During the shadow week the push run still runs, and each run's record shows whether it would have been skipped and whether the results agreed.
- With the switch on, the push run is skipped only where the merge queue passed the identical commit under the identical workflow digest and runner image, and the skip is recorded with that evidence.
- Any missing or mismatched evidence makes the push run happen.
- The switch off restores today's behaviour exactly.
