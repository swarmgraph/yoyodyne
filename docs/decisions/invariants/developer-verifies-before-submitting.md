---
id: developer-verifies-before-submitting
title: Developers probe execution at minute zero, and evidence what the checks would judge
status: active
established_by:
    - yoyodyne-ifd.183
    - checks-on-ci
revisions:
    - action: created
      by: architect
      at: 2026-08-31T15:46:12.623529Z
      reason: The architect's wording of the operator's principle from the sandbox-blindness incident, with his minor-change leeway; transcribed from her 2026-08-31 rulings batch.
    - action: amended
      by: architect
      at: 2026-11-05T15:01:41.739842Z
      reason: 'Adds the sentence the checks-on-ci design has waited to place here since October 14: with the project''s full checks on CI, submission evidence is the short local check, and the full suite''s evidence is the CI result the harness reads for the same commit. The earlier wording is also put in ordinary words under the plain-language goal, with its meaning unchanged.'
---

## Must hold

Every developer run begins by executing the declared checks' entry point in its worktree, before any edit, and recording the result, with no exemption for any kind of work. A submission whose diff touches files the declared checks exercise must carry submission evidence: the developer's own record of executing the relevant checks against its change before handing it to review. A diff touching only files no declared check exercises may submit on that first run alone. Where the project runs its full checks on CI before review, the relevant checks for submission evidence are the short local check the project declares, and the full suite's evidence is the CI result the harness reads for the same commit before review starts.

## Why

A reviewer judges evidence, and a developer that never executed anything submits claims. The first run is required of every developer because it is cheap and catches a toolchain that cannot run at all before any work is done, instead of at review, which is where that has historically been discovered. Submission evidence is limited to what the checks exercise because demanding the whole suite for a diff it never touches makes honesty expensive and teaches padding, not verification. Where CI runs the full suite before review, a developer's sandbox has no network and cannot run it; the harness reads CI's result for the same commit before review starts, so the reviewer still judges evidence, and the short local check keeps what CI cannot run, such as checks only this machine can run, in the developer's own record.
