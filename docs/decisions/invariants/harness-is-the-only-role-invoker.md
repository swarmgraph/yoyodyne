---
id: harness-is-the-only-role-invoker
title: The harness is the only thing that invokes a role or execution agent
status: active
established_by:
    - yoyodyne-ifd.138
revisions:
    - action: created
      by: architect
      at: 2026-08-23T16:33:43.675582Z
      reason: Recorded while promoting the management-and-supervision brief under the operator's 2026-08-22 mandate; the one of the contract's seven invariants that binds code which will never mention it.
    - action: amended
      by: architect
      at: 2026-09-26T20:02:03.920282Z
      reason: yoyodyne-ifd.437.7 - 'tool posture' replaced with 'tool access', the register's plainer words; the constraint is unchanged
    - action: amended
      by: architect
      at: 2026-11-19T16:25:16.971629Z
      reason: 'Replacing retired words (yoyodyne-ifd.437.21): ''gate'' in two places becomes the checks the harness makes before it starts a role, as the operating rules ask; ''inter-role'' and ''machine-local'' become plain words. The constraint is unchanged.'
---

## Must hold

Only the harness invokes, resumes, or wakes a role or execution agent. A role never spawns, invokes, or schedules another role; communication between roles is persisted, typed requests that the harness delivers by invoking the target under its own lease, its own checks on what may start, and its budgets. No coordination feature, client, or supervisor introduces a second invoker.

## Why

Every check the harness makes before it starts a role - authority tables, tool access, the intake hold, the spending pause, budgets, independence evidence for review - is enforced at the point of invocation. A role that can invoke another role routes around all of it at once, which is why this rule was ratified by the operator and why it must bind coordination code that will never mention it. Until now it lived in a note kept on one machine, outside the repository; this is its governed home.
