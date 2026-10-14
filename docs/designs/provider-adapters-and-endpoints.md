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
      at: 2026-10-14T05:49:38.11132Z
      reason: 'yoyodyne-y3p: adds the Usage and cost section. One normalized usage record per provider call on every invocation path, five counters that never overlap, each measured, measured zero, or absent; one shared code path turning running session totals into per-call amounts; four cost classes never summed together; one pricing service over versioned rate data; estimates appended to the existing spend ledger and never rewritten; the test grid across adapters, paths and counting cases. The Records paragraph now points to it. Nothing else in the design changes.'
approvals:
    - policy: approvals.designs
      revision: 4
      by: harness
      at: 2026-10-14T05:49:38.11132Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 887, for document-887.1 under the automatic approval policy
---
# Provider adapters and execution endpoints

**What this is for.** The goals now require multiple providers behind one adapter contract. This design owns what an adapter must supply, which roles a provider may serve, how pooling and failover work across providers rather than within one, and what durable records carry so evidence stays provider-independent. Claude-specific configuration and capacity semantics stay in [claude-execution-and-account-routing](claude-execution-and-account-routing.md), which this generalizes rather than replaces.

**The adapter contract.** Availability, capability declaration, start, resume, normalized events, usage and cost reporting, sandbox and permission mapping, and refusal classification into the governed taxonomy — usage limit with reset, server overload, the recoverable classes, the terminal auth and permission refusals. An adapter that cannot classify refusals cannot be admitted: the wait machinery, the capacity-blocked state, and the retry taxonomy all key on that classification. A provider speaking a protocol a compiled adapter already reads enters as configuration under the plugin contract; a different protocol is a harness change.

**Role eligibility.** Derived from declared capabilities, never from a provider name, and refused at configuration load. The developer requires worktree-scoped write under a sandbox. The reviewer and management roles require read-only access: they may reason over supplied evidence and inspect repository context, but cannot edit it or perform external actions. Claude Code enforces this with no tools; Codex uses its native read-only sandbox with tool network access and escalation disabled, and adapter-controlled configuration that disables external integrations. Both providers serve all six roles. Codex's reads are not confined to an evidence directory or to the repository; the operator accepted native read-only inspection in place of the earlier evidence-confinement requirement. The adapter isolates configuration discovery from the repository and reapplies its restrictions on native resume. Analysis-only prompting supplements the sandbox; it is not a claim that exec supports native Plan mode. Provider authentication and session persistence remain provider-owned. Local CLI and sandbox probes verify the supported launch behavior, while recorded streams verify event parsing; neither certifies every later provider version. The management roles' structured output remains a harness property: the harness parses and refuses the blocks it reads on every provider. Conversation turns retain native resume, normalized events, per-turn accounting, and refusal classification under the shared adapter contract.

**Endpoints and pools.** The unit is the execution endpoint — provider, adapter version, account alias, model. Pools are pools of endpoints, with round-robin among active endpoints, a reserved fallback, affinity, health, and per-alias attribution as already contracted. Failover only moves an invocation to an endpoint that gives the role the same tool access, per the fallback clause that never weakens checks, authority, or review independence. Crossing providers rebuilds from the durable record rather than resuming a session, at the cost of context reconstruction paid only when crossing.

**Records.** Every invocation records endpoint identity in full, the configuration revision, the normalized event schema version, the refusal classification where one occurred, and its usage and cost as the usage and cost section below specifies, with provider-reported cost naming its provider. Review-independence evidence is expressed as endpoint-plus-invocation identity and never as provider session semantics, so a cross-provider review remains verifiable.

## Usage and cost

This section serves the goal that the operator can see what the harness spends on their behalf, per work item, per run, and in total, and the goal that spend follows the work. It settles what the adapter contract's usage and cost reporting means, so that every provider, every compiled adapter, and every way the harness calls a provider report spend the same way into the one spend ledger that already exists. Reading a provider's own events stays in its adapter; everything after that is shared.

### One usage record per provider call

Every call the harness makes to a provider writes exactly one usage record, on every path: a developer run, including its repair rounds and continuations; a review, including a second ask; a conversation turn of any role, including a scheduled pass; an exchange between roles; a side thread; and a document landing run. A path that calls a provider and writes no usage record is a defect.

A usage record carries:

- **Who called.** The run or conversation, the turn, and the attempt; the endpoint in full — provider, adapter version, account alias; the model the harness asked for and, where the provider says, the model that answered.
- **How it is paid for.** The billing mode: API key, subscription, purchased credits, or `not recorded`. The adapter reads it from the account's configuration and the provider's own report; it never infers it from a model name.
- **Five counters.** Fresh input, cached input read, cache writes, output, and reasoning. Each counter is one of: a measured number, a measured zero, or absent because the provider did not report it. Absent is never stored or shown as zero.
- **Reported cost.** The amount in US dollars the provider itself reported for the call, where it reported one, naming the provider.
- **Where it came from.** The normalized event schema version and a pointer to the raw provider events the counters were read from, which are kept with the run's or conversation's event log as they are today.

**The counters never overlap.** Fresh input excludes cached input and cache writes; output excludes reasoning. Where a provider reports a total that includes another counter, the adapter subtracts it, and the record says the adapter did so. Codex reports cached input inside its input count and reasoning inside its output count; Claude Code reports cache reads and cache writes apart from input. Adding the five counters therefore counts every token once.

### Turning running totals into per-call amounts

Some providers report a running total for the session rather than an amount for the call, and a resumed session carries its total forward. The adapter hands each reading to one shared piece of code, marked as a running total or a per-call amount, with the session and the provider's own identity for the event. That shared code alone turns readings into per-call amounts, by these rules:

- A running total becomes the difference from the last reading for the same session. A fresh session starts from zero.
- A reading lower than the last one for that session is a reset: it is taken whole as the call's amount, and the record says a reset was seen.
- A final event repeated with the same event identity is counted once.
- A call that was cut off — the provider's stream stopped, the run was ended, the machine slept — records what the provider reported before the cut, marked as partial.
- A call that failed with no usage reported writes a record whose counters are all absent, not zero.
- A rate tier that depends on how large one request is, such as a higher rate above a set number of input tokens, is decided from that call's own input. It is never decided from a session's running total. Where the call's own input was not reported, the tier is unknown.

### Four kinds of cost, never added together

Every cost figure is exactly one of:

- **Reported**: the provider's own figure for the call. It is what the provider said the call cost, not an invoice, and it is never recalculated.
- **Estimated**: computed by the pricing service from the counters and a rate entry, naming the rate entry and its version.
- **Unknown**: neither reported nor computable — the counters were absent, or no rate entry matches, or the tier could not be decided. The figure says why.
- **Comparison only**: what the same use would cost at the provider's API prices, for a call paid for by a subscription or purchased credits. It is not money spent. What a subscription actually charges is the subscription itself; purchased credits used are shown as credits where the provider reports them.

No total adds two kinds together. Every total shows a reported subtotal, an estimated subtotal, the comparison figure apart from both, and how many calls and what share of them are unknown.

### The pricing service and the rate data

One pricing service computes every estimate. It takes a usage record and returns an estimate naming the rate entry it used, or an unknown with its reason. No price, and no condition on a model's name to find one, appears anywhere else in the code.

Rates are data, kept in the repository beside the pricing service and changed by reviewed change like any other file. Each entry names: the provider; the model; the billing mode; the tier; the date it takes effect; the per-request input size above which a different rate applies, and that rate; a price for each of the five counters, or the provider's stated rule where a counter has no separate price — for example, a provider that charges nothing extra for cache writes says so in its entry rather than leaving the price missing; the page the price was read from; and the date it was checked. Entries for one provider, model, billing mode, and tier never overlap in time. A record that matches no entry, or matches one whose tier it cannot decide, is unknown, never priced by guess.

The operator supplied the pages for the current OpenAI model prices and the ChatGPT credit rules on October 2. They are starting evidence: whoever writes the first rate entries reads each page again and records the date checked.

### The one ledger, and recalculation

Usage records and cost figures go into the spend ledger the harness already keeps. There is no second ledger. Ledger entries written before this section carry no counters, and are read with every counter absent, never zero.

An estimate is appended and never rewritten. When a rate entry is corrected, a recalculation appends a new estimate for each affected call, naming the new rate entry and the estimate it replaces. Surfaces show the latest estimate; the earlier ones stay readable, so any figure shown can be traced to the rate that produced it. A reported figure is never recalculated.

### What the surfaces show

The read model in [observability-and-dashboard](observability-and-dashboard.md) computes every total from the ledger, and `yoyo cost`, `yoyo status`, the dashboard's spend box, and Slack present it. Per work item, per run, and in total, each figure shows its reported subtotal, its estimated subtotal, its comparison figure apart, and its unknown share as a count of calls and a percentage. That design's cost entry, which still calls estimates reserved for future sources, is revised to match this section.

### Tests

The tests cover a grid: each adapter — Claude Code and Codex — on each path named above, in each case:

- a fresh session;
- a resumed session reporting running totals;
- a counter reset;
- a repeated final event;
- a call cut off part way;
- a failure with no usage;
- a measured zero beside an absent counter;
- one call above a per-request rate threshold in a session whose total is above it while the call is not;
- each billing mode.

Adapter cases run against recorded provider streams, reusing the recorded Codex streams of yoyodyne-ifd.435.7 where they cover a case. The rate data has its own tests: every entry names its source page and date checked, and no two entries overlap in time.

### Alternatives rejected

- **Pricing inside each adapter** was rejected because prices would be scattered across code and drift between providers.
- **A separate cost ledger** was rejected because the spend ledger is already the one record surfaces read, and a second would be a place for numbers to disagree.
- **Adding estimates into reported totals** was rejected because the operator would read a guess as money the provider stated.
- **Deciding a rate tier from a session's running total** was rejected because providers apply per-request thresholds to each request, not to the session.

### What a reviewer can check

- Every invocation path writes one usage record per provider call.
- No absent counter is shown as zero.
- The five counters add up without counting a token twice.
- No total mixes kinds of cost.
- No price appears outside the rate data.
- A recalculation leaves the earlier estimate readable.
- An unknown figure always says why.