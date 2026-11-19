---
id: configuration-never-grants-authority
title: Configuration selects sequence and never grants authority
status: active
established_by:
    - yoyodyne-ifd.210
revisions:
    - action: created
      by: architect
      at: 2026-08-31T15:58:12Z
      reason: extracted from the configurable-workflows promotion - the rule that makes declarative topology safe to hand to projects, stated so it binds every future configuration key
    - action: amended
      by: architect
      at: 2026-11-19T16:16:39.997713Z
      reason: Replaces the word 'gate', which the operator retired on November 8, 2026, with 'the checks a change must pass before it lands', one place in the Why section, under the replacement of retired words in the architect's documents (yoyodyne-ifd.437.21). The constraint and its scope are unchanged.
---

## Must hold

No project configuration - workflow definition, agent profile, trigger binding, or persona - can widen an agent's authority. Capability primitives and their enforcement exist only in trusted Go code; ordinary configuration selects and narrows among registered actions and validated role contracts; a protected role definition composes only registered primitives and takes effect only through operator-authorized activation that pins its digest. No configuration path can mint evidence, grant tools, or bypass a registered action's refusals.

## Why

Configurable topology is safe exactly as long as configuration chooses sequence and nothing else. The first configuration key that grants capability converts every project file into an escalation path, and neither the reviewer nor the checks a change must pass before it lands can catch what the schema itself permits. This binds every future configuration key - which is precisely the code that will never cite it.
