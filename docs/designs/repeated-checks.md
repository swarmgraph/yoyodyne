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
---
# Repeated checks: when two runs are the same run, and the first one removed

## What this is for

A change is checked at several stages, and some of those runs look alike. Mason asked on October 3 for an audit of them: which repetitions are true duplicates that can go, and which only look alike and are required coverage. Removing a check that was covering something is how an unverified change lands, so this design is conservative: a run is removed only when evidence proves an identical run already passed, or that nothing it covers has changed since one did, and anything uncertain runs. It serves the goal of a traceable chain from goals to verification, and keeps every existing requirement: full verification of the actual merge candidate, the behaviour that only macOS shows, formatting and vet, independent review, and a record of what ran. No live check is removed by this design; it names the first one to remove and the conditions for doing so.

## Who decides

Every decision this design describes, whether a run is skipped, whether a result is kept for a later commit, and what a result proves, is made by the checks component, `internal/checks`, under guarantee 9 of [the design for one owner for each guarantee](one-owner-for-each-guarantee.md). No execution path decides it for itself. A result counts as evidence only when the checks component recorded it, with the five things below.

## When two runs are the same run

Two runs of a check are equivalent only when all of these match:

- the exact commit checked, by its identity, not its branch;
- the check's definition: the command, and the digest of the workflow or configuration that declares it;
- the toolchain: the language and tool versions the run used;
- the platform: operating system and architecture;
- the relevant environment: any setting or variable the check declares it reads.

A run on a different commit, platform or toolchain is not a duplicate, however similar it looks. A run whose record lacks any of these is treated as not equivalent to anything.

## Keeping a result for a later commit

A passed result is also kept for a later commit, without running the check again, when the project's declared analyser says, with complete evidence, that nothing the check covers changed between the commit that passed and the later one. The other four things above must still match. When the target branch has moved, the comparison is made on the change combined with the new target branch, not on the change alone. A path the analyser cannot place, an answer it cannot give in full, or any other doubt runs the check. The kept result is recorded with the commit it passed on and the analyser's answer beside it.

This is new, so it rolls out the way narrowing does under [choosing checks by change](choosing-checks-by-change.md): while it is measured, the answer is computed and recorded and the check runs anyway. It is switched on only once 50 such runs show no case where the analyser said nothing changed and the check then failed. A setting restores running every time.

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
- **The harness's checks before review and before promotion**: the same platform; equivalent when the merge candidate is the very commit that was reviewed, which the evidence bound to the reviewed revision already handles. Where the base moved, they check different commits. Once keeping a result by the analyser's answer is switched on, a check whose covered code did not change on the change combined with the new base keeps its earlier result; every other check runs again.
- **The harness's checks on macOS and any CI run on Ubuntu**: different platforms; both are required, because some behaviour (the keychain, launchd, file system and process handling) is only exercised on macOS.
- **CI on the pull request and CI in the merge queue**: different commits unless the queue's merge commit has the same tree as the pull request's head on an unchanged base; in practice both are required.
- **CI in the merge queue and CI on the push to main**: when the queue lands, the commit it tested becomes main. The push run then checks the identical commit, under the identical workflow, on the identical platform. **This is the one true duplicate the inventory finds.**

## Fallback

Every decision to skip a run is made from recorded evidence and falls back to running the check when anything is missing or different: no record, a different commit, a workflow file whose digest changed, a different runner image, a run that ended in anything but a pass, an analyser answer that is incomplete, or evidence the harness cannot read. A skipped run is recorded with the evidence it relied on, so the record of what verified a commit stays complete. A required check always reaches a final state, pass, fail or skipped with evidence, and nothing merges on a check left without one.

## The first removal

The first bounded implementation is the duplicate above: the CI run on the push to main is skipped when, and only when, the merge queue's run passed the same commit under a workflow file with the same digest on the same runner image. It is done in two steps:

1. **Shadow.** For one week the push run still runs; beside it the harness records whether it would have been skipped and whether the two results agreed.
2. **Removal.** Only if every shadow case agreed is the skip switched on. A setting restores today's behaviour at any time, and any disagreement found later switches it back off and is reported to the development manager.

Release packaging and the adoption check are not touched by this first step. Narrowing them later needs the shared selection policy of the change-aware verification design (yoyodyne-ifd.429.44), with complete dependency coverage of scripts, templates and configuration, not a filter of its own.

## Measurements

Before and after the first removal, for representative narrow, broad, non-Go and concurrent changes, record CPU time and wall time for each stage, the coverage each stage provides, and changes landed per day. The shadow week's agreement rate is the rollout condition for the first removal, and the 50 measured runs are the rollout condition for keeping results by the analyser's answer; the measurements are what any claim of benefit rests on, and none is made before them.

## How this fits the other designs

- **[The design for one owner for each guarantee](one-owner-for-each-guarantee.md)** owns these decisions under guarantee 9 and decides where this design and it differ. Its race check rules for this repository are an application of keeping a result by the analyser's answer, and of when the merge candidate may be verified by the forge's required check instead of a local run.
- **Checks on CI** removes the harness's full local run in CI mode. It keeps the short local check on macOS, and tests that only behave correctly on macOS stay in that local check unless the CI workflow gains a macOS job, because Ubuntu runs cannot stand in for them.
- **Capacity for heavy checks** bounds how many run at once; this design reduces how many run at all. They do not overlap.
- **The document landing route** already runs only the checks that read documents for a change confined to documents.

## How a reviewer checks an implementation

- During the shadow week the push run still runs, and each run's record shows whether it would have been skipped and whether the results agreed.
- With the switch on, the push run is skipped only where the merge queue passed the identical commit under the identical workflow digest and runner image, and the skip is recorded with that evidence.
- A result is kept for a later commit by the analyser's answer only once 50 measured runs showed no miss, only where the answer was complete, and with the comparison made on the change combined with the new target branch where it moved; the kept result names the commit it passed on and the answer.
- Any missing or mismatched evidence makes the check run.
- No code outside `internal/checks` decides that a run is skipped or a result kept.
- The switches off restore today's behaviour exactly.

## Evidence

This revision was written from reads at main 89284e43 of this design and of the design for one owner for each guarantee, both whole. The code of `internal/checks` and the files in `internal/orchestrator` that run checks today were not read.