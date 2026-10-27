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
---
# Provider adapters and execution endpoints

**What this is for.** The goals now require multiple providers behind one adapter contract. This design owns what an adapter must supply, which roles a provider may serve, how pooling and failover work across providers rather than within one, and what durable records carry so evidence stays provider-independent. Claude-specific configuration and capacity semantics stay in [claude-execution-and-account-routing](claude-execution-and-account-routing.md), which this generalizes rather than replaces.

**The adapter contract.** Availability, capability declaration, start, resume, normalized events, usage and cost reporting, sandbox and permission mapping, and refusal classification into the governed taxonomy — usage limit with reset, server overload, the recoverable classes, the terminal auth and permission refusals. What usage and cost reporting means, the same way for every provider and every path that calls one, is [provider usage and cost](provider-usage-and-cost.md). An adapter hands its provider's events to `internal/cost`, which alone decodes cost events into the counters and the reported cost that design names and decides whether a total is complete, as guarantee 5 of [one owner for each guarantee](one-owner-for-each-guarantee.md) rules; no adapter decodes a cost event itself. Every call into an adapter returns the best result it has together with any error, never the error alone, so a priced ending the provider already sent is kept when later output is malformed or the stream breaks. Where a provider lets a developer run hand coding to sub-agents, the adapter declares that ability and the run uses it as [developer sub-agents](developer-sub-agents.md) describes. An adapter that cannot classify refusals cannot be admitted: the wait machinery, the capacity-blocked state, and the retry taxonomy all key on that classification. A provider speaking a protocol a compiled adapter already reads enters as configuration under the plugin contract; a different protocol is a harness change.

**Role eligibility.** Derived from declared capabilities, never from a provider name, and refused at configuration load. The developer requires worktree-scoped write under a sandbox. The reviewer and management roles require read-only access: they may reason over supplied evidence and inspect repository context, but cannot edit it or perform external actions. On Claude Code the reviewer is given read-only tools over the run's worktree, and the management roles are given no tools. Codex uses its native read-only sandbox with tool network access and escalation disabled, and adapter-controlled configuration that disables external integrations. Both providers serve all six roles. Codex's reads are not confined to an evidence directory or to the repository; the operator accepted native read-only inspection in place of the earlier evidence-confinement requirement. The adapter keeps configuration discovery away from the repository and from the account's own provider home, so neither a file in the repository nor a setting the person keeps for their own use of the provider changes what a run may do, and it reapplies its restrictions on native resume. The home the harness prepares for each Codex run lives under the machine home, at `~/.yoyodyne/accounts/<account>/codex-home/`, never inside `~/.codex`, as [machine-home](machine-home.md) keeps every file the harness writes in one home; Codex's own record of its sessions stays where Codex keeps it. Analysis-only prompting supplements the sandbox; it is not a claim that exec supports native Plan mode. Provider authentication and session persistence remain provider-owned. Local CLI and sandbox probes verify the supported launch behavior, while recorded streams verify event parsing; neither certifies every later provider version. The management roles' structured output remains a harness property: the harness parses and refuses the blocks it reads on every provider. Conversation turns retain native resume, normalized events, per-turn accounting, and refusal classification under the shared adapter contract.

**Endpoints and pools.** The unit is the execution endpoint — provider, adapter version, account alias, model. Pools are pools of endpoints, with round-robin among active endpoints, a reserved fallback, affinity, health, and per-alias attribution as already contracted. Failover only moves an invocation to an endpoint that gives the role the same tool access, per the fallback clause that never weakens checks, authority, or review independence. Crossing providers rebuilds from the durable record rather than resuming a session, at the cost of context reconstruction paid only when crossing.

**Records.** Every invocation records endpoint identity in full, the configuration revision, the normalized event schema version, the refusal classification where one occurred, and one usage record per provider call as [provider usage and cost](provider-usage-and-cost.md) defines it, with provider-reported cost naming its provider. Review-independence evidence is expressed as endpoint-plus-invocation identity and never as provider session semantics, so a cross-provider review remains verifiable.

**What a reviewer can check.** The adapter contract links the usage and cost design and states no counting or pricing rule of its own. No adapter decodes a cost event; only `internal/cost` does. Each adapter, fed a priced ending followed by malformed output, returns the result with the cost kept beside the error. No adapter reads configuration from the repository or from the account's own provider home. The Claude Code reviewer's tools are read-only and limited to the run's worktree, and the management roles hold none. No file the harness writes for a Codex run is under `~/.codex`.

**Evidence.** Read on main at f098ce9a: this design, [provider usage and cost](provider-usage-and-cost.md), and the dashboard design. The two changes approved on October 18 and the Codex home location ruled on October 17 are written here as decided; the code that prepares the Codex home was not read, and moving it is build work already reported to the development manager. Read whole on main at dba8cf1f on October 26: this design and [one owner for each guarantee](one-owner-for-each-guarantee.md), whose guarantee 5 and migration item 9 are the source of the rules above on cost and on returning the best result together with any error. The adapters' code and `internal/cost` were not read; the build item that moves them (migration item 9) reads them first.