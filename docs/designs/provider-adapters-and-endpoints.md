---
id: provider-adapters-and-endpoints
kind: design
title: Provider adapters and execution endpoints
supports:
    - v1-goals
status: active
revisions:
    - action: amended
      by: architect
      at: 2026-10-02T03:00:00Z
      reason: Operator-approved read-only Codex inspection for reviewers and management roles replaces the earlier evidence-confinement requirement; external actions remain disabled and native resume preserves the launch policy.
    - action: created
      by: architect
      at: 2026-09-07T19:30:00Z
      reason: promoted from the operator's multi-provider scoping question (yoyodyne-ifd.337) under the goals clause admitting multiple providers behind one adapter contract
    - action: amended
      by: architect
      at: 2026-09-26T19:54:03Z
      reason: yoyodyne-ifd.437.7 - 'posture' replaced with 'tool access' in the eligibility and failover rules; nothing about either rule changes (wording from approved amendment 5eca2c89)
    - action: amended
      by: architect
      at: 2026-09-25T04:56:31Z
      reason: 'yoyodyne-ifd.352 - all five roles on Codex scoped: one confinement obstacle shared by the four evidence-only roles, gated on a harness-side probe, with the management roles'' output contract shown to be the harness''s already; the probe, the confinement layer, and the conversation adapter are the three slices'
    - action: amended
      by: architect
      at: 2026-10-21T07:25:44.860152Z
      reason: 'The usage and cost design is now on main, so this design links it for what usage and cost reporting means. With it, three decided changes: configuration discovery is also kept from the account''s own provider home and the Claude Code reviewer gets read-only tools over the run''s worktree while management roles get none (both approved October 18); the per-run Codex home lives under the machine home, not in ~/.codex (ruled October 17); and the adapter contract names the sub-agent ability and links its design.'
    - action: amended
      by: architect
      at: 2026-10-26T23:49:21.104872Z
      reason: 'Brings the adapter contract into line with guarantee 5 of the design for one owner for each guarantee, which landed on October 25: an adapter returns its best result together with any error so a priced ending is kept, and only internal/cost decodes cost events. The earlier sentence that an adapter reads its provider''s events into the cost counters contradicted that design''s check in make test. Written from reads at main dba8cf1f.'
    - action: amended
      by: architect
      at: 2026-11-22T09:36:39.033155Z
      reason: Brings the design into line with the code, as the review's second reading for providers found it at 848d86190. It says what is built. It keeps the rule against the account's own Codex settings, choosing a list of what to allow over today's list of keys to remove. It records the Claude Code adapter's cost reading as unbuilt work shared with the usage and cost design. It adds the reviewer's read-only git shell and counts one usage record per invocation.
approvals:
    - policy: approvals.designs
      revision: 4
      by: harness
      at: 2026-10-21T07:25:44.860152Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1401, for document-1401.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 5
      by: harness
      at: 2026-10-26T23:49:21.104872Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1821, for document-1821.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 6
      by: harness
      at: 2026-11-22T09:36:39.033155Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3437, for document-3437.1 under the automatic approval policy
---
# Provider adapters and execution endpoints

**What this is for.** The goals require multiple providers behind one adapter contract. This design owns four things:

- what an adapter must supply;
- which roles a provider may serve;
- how pooling and failover work across providers rather than within one;
- what durable records carry, so evidence stays provider-independent.

Claude-specific configuration and capacity rules stay in [claude-execution-and-account-routing](claude-execution-and-account-routing.md). This design generalizes that one rather than replacing it. What is built today and what is not is listed under *Where this stands*.

## The adapter contract

An adapter supplies:

- availability;
- a declaration of its capabilities;
- start and resume;
- normalized events;
- usage and cost reporting;
- a mapping of sandbox and permissions;
- a classification of every refusal into the governed taxonomy: usage limit with reset, server overload, the recoverable classes, and the terminal refusals for signing in and for permission.

What usage and cost reporting means, the same way for every provider and every path that calls one, is [provider usage and cost](provider-usage-and-cost.md).

An adapter hands its provider's events to `internal/cost`. That package alone decodes cost events into the counters and the reported cost that design names, and alone decides whether a total is complete, as guarantee 5 of [one owner for each guarantee](one-owner-for-each-guarantee.md) rules. No adapter decodes a cost event itself.

Every call into an adapter returns the best result it has together with any error, never the error alone. That way a priced ending the provider already sent is kept when later output is malformed or the stream breaks.

Where a provider lets a developer run hand coding to sub-agents, the adapter declares that ability and the run uses it as [developer sub-agents](developer-sub-agents.md) describes.

An adapter that cannot classify refusals cannot be admitted. The waiting machinery, the capacity-blocked state and the retry taxonomy all depend on that classification.

A provider speaking a protocol a compiled adapter already reads enters as configuration under the plugin contract. A different protocol is a harness change.

## Role eligibility

Eligibility comes from declared capabilities, never from a provider name, and an ineligible pairing is refused when the configuration loads.

- **The developer** needs worktree-scoped write access under a sandbox.
- **The reviewer and the management roles** need read-only access. They may reason over the evidence they are given and inspect repository context. They cannot edit it or take any action outside the harness.

Both providers serve all six roles.

**On Claude Code:**

- The reviewer is given read-only tools over the run's worktree: tools that read and search files, and a shell limited to git commands that only read.
- The management roles are given no tools.

**On Codex:**

- Codex runs in its native read-only sandbox, with network access for tools and escalation both turned off.
- Settings the adapter controls turn off external integrations.
- Codex's reads are not confined to an evidence directory or to the repository. The operator accepted native read-only inspection in place of the earlier requirement to confine reads to the evidence.

**Where configuration comes from.** The adapter keeps configuration discovery away from the repository and from the account's own provider home. Neither a file in the repository nor a setting the person keeps for their own use of the provider may change what a run may do. The adapter reapplies its restrictions on native resume.

For Codex this means the following.

- The home the harness prepares for each run lives under the machine home, at `~/.yoyodyne/accounts/<account>/codex-home/`, never inside `~/.codex` and never inside the account's own home. [machine-home](machine-home.md) keeps every file the harness writes in one home.
- That home holds a settings file the adapter writes itself, from the harness's own configuration of the endpoint: the model, the sandbox and its settings, and how to reach the provider.
- From the account it links only what Codex needs to sign in and its own record of past sessions. Codex keeps that record where it always does.
- Nothing in the account's own settings file reaches the run.

The alternative rejected is what the code does today: copying the account's settings file into the run with a list of keys taken out. That list has to grow with every Codex release, and one missed key changes what a run may do. A declared MCP server, an approval or sandbox setting, or a profile would each do that, and the goals require safety to hold whatever the configuration says. The constraint that decided it: a list of what to allow fails closed, and a list of what to remove fails open.

The cost of that choice: a person who reaches Codex through a provider declared in their own settings file has to declare that endpoint in the harness's configuration instead.

**What else holds for every provider.**

- Prompting that asks for analysis only adds to the sandbox; it is not a claim that exec supports native Plan mode.
- Signing in to the provider and storing its sessions stay with the provider.
- Local probes of the command line and the sandbox verify the supported launch behaviour, and recorded streams verify how events are read. Neither certifies every later version of a provider.
- The management roles' structured output remains a harness property: on every provider, the harness parses the blocks it reads and refuses the ones it cannot accept.
- Conversation turns keep native resume, normalized events, accounting for each turn, and refusal classification under the shared adapter contract.

## Endpoints and pools

The unit is the execution endpoint: provider, adapter version, account alias and model.

Pools are pools of endpoints. Among the active endpoints in a pool, work is shared out in turn. A pool also has a reserved fallback, affinity, health, and attribution to each alias, as already contracted.

Failover only moves an invocation to an endpoint that gives the role the same tool access. That follows the fallback clause, which never weakens checks, authority or the independence of review.

The invocations that can move today are the developer's, the run reviewer's and the merge queue reviewer's. The details for each slot are in [claude-execution-and-account-routing](claude-execution-and-account-routing.md).

Moving between providers rebuilds the work from the durable record rather than resuming a session. The cost of rebuilding the context is paid only when moving between providers.

## Records

Every invocation records:

- the endpoint's identity in full;
- the configuration revision;
- the version of the normalized event schema;
- the refusal classification, where a refusal occurred;
- one usage record per invocation, as [provider usage and cost](provider-usage-and-cost.md) defines it, with any cost the provider reported naming its provider.

Evidence that a review was independent is expressed as endpoint and invocation identity, never as a provider's session semantics. A review on a different provider therefore stays verifiable.

## Where this stands

As the review of designs against code found it on main at 848d86190, on November 21, 2026:

| Rule | Built | Where |
|---|---|---|
| Eligibility decided from declared capabilities, refused when the configuration loads | Yes | `internal/backend/endpoint.go` (`Serves`, `ServesAt`); `internal/config/endpoints.go` |
| Claude Code reviewer has read-only tools over the worktree; management roles have none | Yes | `internal/backend/claudecode/backend.go` |
| One usage record per invocation | Yes | `internal/spend/spend.go` |
| Only `internal/cost` decodes cost events | No. The Claude Code adapter reads the total cost and each model's cost from the stream, applies its own rule for a final event that arrives twice, and reads the cost of sub-agents. | `internal/backend/claudecode/parser.go`, `internal/backend/claudecode/subagents.go` |
| Nothing from the account's own settings reaches a Codex run | No. The account's home is used as it is when it holds no instruction file or key; otherwise `config.toml` is copied with only the instruction keys removed. | `internal/backend/codex/context.go` |
| The Codex run's home is under the machine home | No. The home the harness makes is `.yoyodyne-codex-home`, inside the account's own home. | `internal/backend/codex/context.go` |

## Build pieces

1. **The Codex run's home and settings.** The adapter makes the run's home at `~/.yoyodyne/accounts/<account>/codex-home/` and writes the settings file there itself, from the harness's endpoint configuration. From the account it links only what Codex needs to sign in and its own record of past sessions.

   The piece first checks whether an MCP server declared in the account's settings file reaches a run today. If it does, that breaks the invariant that no agent session holds an MCP server, and it is fixed first.

   Tests show that each of the following, set in the account's file, does not reach the run: an MCP server, a sandbox or approval setting, and a profile. They also show that nothing is written under `~/.codex` or inside the account's own home. Where the harness's endpoint configuration cannot yet say how to reach a provider a person's file declares, this piece adds that setting rather than reading the person's file.
2. **Cost reading leaves the Claude Code adapter.** This is the same piece as the first build piece in [provider usage and cost](provider-usage-and-cost.md), and is admitted once, not twice.

## What a reviewer can check

- The adapter contract links the usage and cost design and states no counting or pricing rule of its own.
- Once build piece 2 lands, no adapter decodes a cost event; only `internal/cost` does.
- Each adapter, fed a priced ending followed by malformed output, returns the result with the cost kept beside the error.
- No adapter reads configuration from the repository or from the account's own provider home. Once build piece 1 lands, this holds for Codex, as its tests show.
- The Claude Code reviewer's tools are read-only and limited to the run's worktree, its shell runs only git commands that read, and the management roles hold no tools.
- No file the harness writes for a Codex run is under `~/.codex` or inside the account's own home.

## Evidence

- **Read on main at 55b3261d24d2, on November 22, 2026:** this design, and the review's second reading for execution, providers and cost (`docs/diagnoses/yoyodyne-ifd-437-84-6b.md`). That reading was taken against main at 848d86190 on November 21 and outranks the first reading where the two differ. The table under *Where this stands* and the build pieces rest on it.
- **Not read for this revision:** the adapter code and `internal/cost`. Each build piece reads them first.
- **Earlier sources:**
  - The rules on cost and on returning the best result with any error come from guarantee 5 and migration item 9 of [one owner for each guarantee](one-owner-for-each-guarantee.md), read whole at dba8cf1f on October 26.
  - The two changes the operator approved on October 18 and the Codex home location ruled on October 17 are written here as decided.
- **Still to do elsewhere:** [machine-home](machine-home.md) lists `accounts/` without naming the Codex home inside it, so the location rests on this design alone until that design names it.