---
id: claude-only-v1-execution
kind: decision
title: V1 first ran every role on Claude Code; that scope ended when the goals admitted more than one provider
status: active
revisions:
    - action: created
      by: architect
      at: 2026-08-23T16:39:19Z
      reason: recording the operator's 2026-08-22 decision during the multi-model promotion
    - action: amended
      by: architect
      at: 2026-10-14T06:48:37.51649Z
      reason: 'yoyodyne-ema: the record still said, as if current, that every role runs on Claude Code and that Codex is parked at priority 4, while the approved goals clause of 2026-09-07 and the provider adapter design let both providers serve all six roles. The record now states its scope as historical, names where current eligibility and routing are decided, keeps the protections it relied on, and says that a priority orders work while only parking with a release condition defers it. Execution routing already withdrew its own Claude-only statements on 2026-10-05 and needs no change.'
approvals:
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-14T06:48:37.51649Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 907, for document-907.1 under the automatic approval policy
---
# V1 first ran every role on Claude Code; that scope ended when the goals admitted more than one provider

**Status:** Historical scope. The decision below was the operator's on 2026-08-22 and governed V1 until 2026-09-07. On that date the operator approved the clause in [the v1 goals](../product/goals/v1-goals.md) that the harness supports more than one provider behind one adapter contract, and [provider adapters and execution endpoints](../designs/provider-adapters-and-endpoints.md) took over which roles a provider may serve. This record is kept so the earlier decision stays readable; it no longer says what may run where.

It refined [claude-code-default-backend](claude-code-default-backend.md) rather than superseding it, and this revision changes nothing in that record.

## What was decided on 2026-08-22

The Codex adapter (yoyodyne-ifd.6) was designed and unbuilt; the generic provider plugin (yoyodyne-ifd.32) was tracked and unstarted. Every role ran on Claude Code, and the near-term risk was an unreliable Claude path, not a missing second provider.

The decision was: V1 runs every role through Claude Code, with fixed assignments the operator configures, one configured account, and no automatic routing of work by cost. The Codex adapter and the plugin contract were given priority 4 and kept, to be reconsidered when they competed against the remaining work.

## What holds now

- **Which provider may serve a role** is decided by the capabilities the provider declares, never by its name, and is refused when the configuration loads. Both Claude Code and Codex may serve all six roles. [Provider adapters and execution endpoints](../designs/provider-adapters-and-endpoints.md) is the only place that says so; nothing in this record restricts it.
- **Which endpoint a run uses, and when it may move to another**, is [execution routing](../designs/claude-execution-and-account-routing.md): stable developer slots with a primary and an alternate, one automatic switch per piece of work on a usage limit, and the same rules for the reviewer.
- **What this record's consequences protected still holds** and now lives in those two designs: every invocation is pinned to one endpoint for its whole life; the reviewer and the management roles have read access only and take no outside action; a fallback never weakens a check, an authority, or the independence of review; and evidence stays readable whichever provider produced it.

## Priority is not deferral

The 2026-08-22 decision said the Codex adapter and the plugin contract were "parked at priority 4". Those are two different things, and only one of them holds work back. A priority orders work: it says what is pulled first, and it never stops an item being chosen. Holding work back means parking the item with a stated condition for releasing it. Anything this project means to defer is parked with that condition, and a low priority alone is never read as a deferral.

## Alternatives rejected

- **Retiring or deleting this record** was rejected because the operator's 2026-08-22 decision is history a reader tracing provider choices needs, and execution routing still refers to it.
- **Adding a note at the top and leaving the rest** was rejected because the old sentences below the note, written as though still current, would read as the current rule to anyone who skimmed past it.
