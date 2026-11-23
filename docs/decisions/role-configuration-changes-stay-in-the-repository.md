---
id: role-configuration-changes-stay-in-the-repository
kind: decision
title: A role changes a setting it owns through the typed configuration write, and the factory's configuration stays committed in the repository
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-23T03:38:57.555208Z
      reason: 'Rules the operator''s question of November 22 on how a role changes a setting it owns without a developer run: through the typed configuration write the portable configuration design already gives, built with the key list held in Go, landing through a reviewed run that changes only that file; keeping the configuration outside the repository is rejected. Notes that a program manager''s trigger model needs piece 8 of the cost design.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-23T03:38:57.555208Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3631, for document-3631.1 under the automatic approval policy
---
# A role changes a setting it owns through the typed configuration write

Ruled by the architect on November 22, 2026, at the operator's request through his assistant at 7:37 PM PST. The case that raised it: the trial of a cheaper model for the program manager watching the flow of work (yoyodyne-ifd.435.87) needed one line of `.yoyodyne/config.yaml` changed, and no role action can change it, so the development manager planned a developer run for one line. It serves the goal that roles, policies and provider selection stay configurable without making safety rules optional, and the goal of running development nearly autonomously.

## Decision

The lasting route is the typed configuration write that [portable agent configuration](../designs/portable-agent-configuration.md) designs in its section on values the harness recommends, applied by a role. It is built, not designed again:

1. The role names one key, one value, its reason, and the work item the change serves.
2. The harness refuses a key that is not on the list held in Go, and a value outside its bounds, naming the key and the bound. The list and its refusal exist as `RoleMayApply` in `internal/config/roleapply.go`, called today only by tests.
3. The harness writes exactly that key into `.yoyodyne/config.yaml` and opens a run that changes only that file, as it does for a document a role writes. The run passes the configured checks, which load the configuration with every bound held in Go, and an independent reviewer, and lands through the merge queue. A run that does not land goes back to the role that asked, with the findings.
4. No developer run and no grant of the file is needed.
5. The change is recorded with the role, the key, the value before and after, the reason and the item; the commit keeps it in the history every role reads, and the drift report and `yoyo doctor` show the value as applied by a role.

A role may apply only the keys that list holds: checks, developer models and slots, a recurring task's schedule and model, numeric limits, an agent's time limits, and the cost settings, with the program manager trigger keys added by the build piece that adds them. Everything else stays with a person, including installation, MCP servers and tools, message intake, review threads, each agent's own model, and what any role may do. Configuration still selects and never widens what a role may do.

Until the write is built, the rule in the portable configuration design stands: the development manager applies a value through a developer run admitted for that one change, whose work item grants that file. The operator's own route for his configuration decisions covers only his decisions.

## The case that raised it

A program manager's scheduled trigger has no model key in the code today; adding one is piece 8 of [reducing what the factory costs](../designs/reducing-what-the-factory-costs.md). Until it lands, a program manager's scheduled passes change model only through the agent's own model, which covers every turn it takes and is the operator's to set. The trial as that design describes it, on scheduled passes alone, needs piece 8.

## Alternatives rejected

- **Keeping this factory's configuration outside the repository**, as [the machine home](../designs/machine-home.md) allows a project to. It would not remove the need for a typed write, because a role still needs a write the harness checks; and it would take changes to the factory's own settings out of review and out of the history every role reads at a recorded commit. That option exists for a project that cannot commit its configuration, not for this one.
- **A role writing the file directly, with no reviewed run.** Configuration chooses checks and models, so a change to it is reviewed like any other change that decides how work is verified.
- **A developer run for every such change.** It spends a developer slot and a whole run to change one line a role already owns.

## What a reviewer can check

- A role's write naming a key not on the list, or a value outside its bound, is refused before anything is written, naming the key and the bound.
- An accepted write changes only `.yoyodyne/config.yaml`, only at the named key, and lands only after the configured checks and an independent review.
- The record of each write names the role, the key, both values, the reason and the item.