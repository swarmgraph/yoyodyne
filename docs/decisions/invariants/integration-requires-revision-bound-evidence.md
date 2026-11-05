---
id: integration-requires-revision-bound-evidence
title: Integration requires typed evidence bound to the exact candidate revision
status: active
established_by:
    - yoyodyne-ifd.210
    - yoyodyne-1m3
revisions:
    - action: created
      by: architect
      at: 2026-08-31T15:58:12Z
      reason: extracted from the configurable-workflows promotion - the existing promotion gate restated so it survives configurable topology, where a definition can route anywhere but an action must still refuse without proof
    - action: amended
      by: architect
      at: 2026-11-05T14:20:33.923451Z
      reason: 'yoyodyne-1m3: the ruling of October 10 on checks that could not run. Such a check counts only beside at least one check that ran and passed, with its reason recorded against the exact revision; a change on which no check ran is not integrated and is held for the development manager without spending a repair attempt. Rejected: letting a check that could not run count alone, which merges unchecked work, and treating it as a failure, which fails work for a fault in the machine.'
---

## Must hold

No action integrates or publishes a candidate without verifying trusted, typed evidence that every required gate - protected-path acceptance, configured checks, and independent review - approved the exact candidate revision being integrated. That evidence is minted only by the registered actions that performed those operations, and must not have been invalidated by any later change to the candidate, the check set, or the target branch. Graph topology, step naming, and reachability are never proof.

A configured check that reports it could not run counts toward the configured checks only when its evidence names the check, why it could not run, and the exact candidate revision, and only beside at least one configured check that ran against that revision and passed. A candidate on which no configured check ran has not met the configured checks: it is not integrated, and it is held for the development manager to decide, without spending a repair attempt.

## Why

Once topology is project-owned data, a definition can route directly to integration, and validation may miss the path a project writes next year. The gate must therefore live in the action rather than in the graph: an integration reached by any route refuses for want of evidence, whatever the definition says. This binds every workflow nobody has written yet, which is the code that will never mention it.

A check that could not run is a fact about the machine rather than the change, so recording it with its reason keeps the evidence honest without failing work for something outside it. Evidence that no check ran at all proves nothing about the revision, and accepting it would let a project whose every check cannot run merge unchecked work with only a status line to show for it. Holding that change for the development manager rather than repairing it is right because no change to the code makes a check able to run.
