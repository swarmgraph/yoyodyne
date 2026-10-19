---
id: claude-code-default-backend
kind: decision
title: Claude Code is the default backend; Codex is an optional alternative
status: active
revisions:
    - action: created
      by: architect
      at: 2026-08-17T00:00:00Z
      reason: identity added with the artifact metadata schema; the record itself is unchanged
    - action: amended
      by: architect
      at: 2026-08-19T00:00:00Z
      reason: drafting residue removed — a stray code fence and trailing rule, and the offer of ratification the architect has since given; the decision itself is unchanged
    - action: amended
      by: architect
      at: 2026-10-18T16:35:03.353702Z
      reason: The record still said Codex is supported only for the developer and the reviewer, while the provider adapter design and the 2026-09-07 goals clause let both providers serve all six roles. The default is unchanged; found while declining amendment-828ba91c, whose quoted sentence the record does not contain.
approvals:
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-10-18T16:35:03.353702Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1230, for document-1230.1 under the automatic approval policy
---
# Claude Code is the default backend; Codex is an optional alternative

**Status:** Accepted. This decision was made and acted on during the v1 design;
it was stated in [the v1 goals](../product/goals/v1-goals.md) until it was
moved here, because it is a decision about which tools Yoyodyne runs rather
than an outcome the product is trying to reach.

As first recorded, Codex was supported for the developer and the reviewer only.
That limit ended when the operator approved, on 2026-09-07, the goals clause
that the harness supports more than one provider behind one adapter contract;
[claude-only-v1-execution](claude-only-v1-execution.md) keeps that history.
This revision changes which roles the record names and nothing about the
default.

## Context

Roles need something that can actually execute: read a repository, edit files,
run commands, and report what happened. Local coding-agent CLIs already provide
that execution interface, including their own tool sandboxing and permissioning.

## Decision

Claude Code is the default backend: it is what `yoyo init` writes for every
agent. Codex is a supported alternative for any of the six roles, chosen agent
by agent in the project's configuration. Yoyodyne drives these CLIs rather than
calling model APIs directly, and does not reimplement their tool execution.

This record decides only which backend is the default. Which roles a backend may
serve is decided by the capabilities it declares, never by its name, in
[provider adapters and execution endpoints](../designs/provider-adapters-and-endpoints.md),
and nothing here restricts it.

## Consequences

- Direct model API integration is a non-goal while the local CLIs provide the
  required execution interface.
- Complete behavioral parity between Claude Code and Codex is a non-goal. Roles
  are configured knowing the backends differ.
- Yoyodyne depends on interfaces it does not control, and a backend's changes can
  break it.
- Naming specific vendors here rather than in the goals keeps the product's
  intent independent of which agents happen to run underneath it. Adding,
  replacing, or removing a backend is an architectural decision, not a change to
  what the product is for.

## Alternatives rejected

- **Keeping Codex limited to the developer and the reviewer, as first written,**
  was rejected because this record would then be cited to refuse Codex for the
  management roles, which the provider adapter design admits.
- **Retiring this record** was rejected because the default it names still holds
  and is still what `yoyo init` writes.
