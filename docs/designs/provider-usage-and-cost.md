---
id: provider-usage-and-cost
kind: design
title: Provider usage and cost
supports:
    - v1-goals
    - operations-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-21T06:41:42.550824Z
      reason: 'yoyodyne-y3p: the usage and cost rules, first written on October 13 as a section of the provider adapters design (document-887.1), whose publication was dropped from the forge''s merge queue and never reached main. Filed as a design of its own so the provider adapters design stays small enough to read and revise whole. The rules are unchanged from that confirmed text; this adds the two corrections its reviewer asked for (attribution kept apart from the kind of cost, and the Codex stream item named by what it is), a list of what is reused, and the limits of what was read.'
    - action: amended
      by: architect
      at: 2026-10-23T22:52:53.276366Z
      reason: 'amendment-63e0d451, approved: the design said every total already comes from the ledger, but `yoyo cost` and `yoyo status --spend` still add up their price columns from the event logs. This says how the two figures are shown side by side until those commands move onto the ledger, and keeps the end state unchanged.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-21T06:41:42.550824Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1394, for document-1394.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-23T22:52:53.276366Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1583, for document-1583.1 under the automatic approval policy
---
# Provider usage and cost

**What this is for.** This design serves the goal that the operator can see what the harness spends on their behalf, per work item, per run, and in total, and the goal that spend follows the work. [Provider adapters and execution endpoints](provider-adapters-and-endpoints.md) lists usage and cost reporting as part of the adapter contract; this design says what that reporting means, so that every provider, every compiled adapter, and every way the harness calls a provider report spend the same way into the one spend ledger that already exists. Reading a provider's own events stays in its adapter; everything after that is shared.

The rules here were first written on October 13 as a section of the provider adapters design. That publication passed its checks and its review but was dropped from the forge's merge queue and never reached main. They are filed here as a design of their own, so the provider adapters design stays small enough to read and revise whole. The rules are unchanged; two corrections the reviewer of that publication asked for are made below.

## One usage record per provider call

Every call the harness makes to a provider writes exactly one usage record, on every path: a developer run, including its repair rounds and continuations; a review, including a second ask; a conversation turn of any role, including a scheduled pass; an exchange between roles; a side thread; and a document landing run. A path that calls a provider and writes no usage record is a defect.

A usage record carries:

- **Who called.** The run or conversation, the turn, and the attempt; the endpoint in full: provider, adapter version, account alias; the model the harness asked for and, where the provider says, the model that answered.
- **How it is paid for.** The billing mode: API key, subscription, purchased credits, or `not recorded`. The adapter reads it from the account's configuration and the provider's own report; it never infers it from a model name.
- **Five counters.** Fresh input, cached input read, cache writes, output, and reasoning. Each counter is one of: a measured number, a measured zero, or absent because the provider did not report it. Absent is never stored or shown as zero.
- **Reported cost.** The amount in US dollars the provider itself reported for the call, where it reported one, naming the provider.
- **Where it came from.** The normalized event schema version and a pointer to the raw provider events the counters were read from, which are kept with the run's or conversation's event log as they are today.

**The counters never overlap.** Fresh input excludes cached input and cache writes; output excludes reasoning. Where a provider reports a total that includes another counter, the adapter subtracts it, and the record says the adapter did so. Codex reports cached input inside its input count and reasoning inside its output count; Claude Code reports cache reads and cache writes apart from input. Adding the five counters therefore counts every token once.

## Turning running totals into per-call amounts

Some providers report a running total for the session rather than an amount for the call, and a resumed session carries its total forward. The adapter hands each reading to one shared piece of code, marked as a running total or a per-call amount, with the session and the provider's own identity for the event. That shared code alone turns readings into per-call amounts, by these rules:

- A running total becomes the difference from the last reading for the same session. A fresh session starts from zero.
- A reading lower than the last one for that session is a reset: it is taken whole as the call's amount, and the record says a reset was seen.
- A final event repeated with the same event identity is counted once.
- A call that was cut off, because the provider's stream stopped, the run was ended, or the machine slept, records what the provider reported before the cut, marked as partial.
- A call that failed with no usage reported writes a record whose counters are all absent, not zero.
- A rate tier that depends on how large one request is, such as a higher rate above a set number of input tokens, is decided from that call's own input. It is never decided from a session's running total. Where the call's own input was not reported, the tier is unknown.

## Four kinds of cost, never added together

Every cost figure is exactly one of:

- **Reported**: the provider's own figure for the call. It is what the provider said the call cost, not an invoice, and it is never recalculated.
- **Estimated**: computed by the pricing service from the counters and a rate entry, naming the rate entry and its version.
- **Unknown**: neither reported nor computable, because the counters were absent, or no rate entry matches, or the tier could not be decided. The figure says why.
- **Comparison only**: what the same use would cost at the provider's API prices, for a call paid for by a subscription or purchased credits. It is not money spent. What a subscription actually charges is the subscription itself; purchased credits used are shown as credits where the provider reports them.

No total adds two kinds together. Every total shows a reported subtotal, an estimated subtotal, the comparison figure apart from both, and how many calls and what share of them are unknown.

**Which work a cost belongs to is a separate question from its kind.** Every usage record names the run or conversation that made the call, so every figure can be attributed to the role that spent it. A figure whose call was made for no work item, such as a scheduled pass or a conversation turn about no item, is counted in the total and shown under its role and conversation as not tied to a work item. It is never dropped, and it is never a fifth kind of cost: it is still reported, estimated, unknown, or comparison only.

## The pricing service and the rate data

One pricing service computes every estimate. It takes a usage record and returns an estimate naming the rate entry it used, or an unknown with its reason. No price, and no condition on a model's name to find one, appears anywhere else in the code.

Rates are data, kept in the repository beside the pricing service and changed by reviewed change like any other file. Each entry names: the provider; the model; the billing mode; the tier; the date it takes effect; the per-request input size above which a different rate applies, and that rate; a price for each of the five counters, or the provider's stated rule where a counter has no separate price (for example, a provider that charges nothing extra for cache writes says so in its entry rather than leaving the price missing); the page the price was read from; and the date it was checked. Entries for one provider, model, billing mode, and tier never overlap in time. A record that matches no entry, or matches one whose tier it cannot decide, is unknown, never priced by guess.

The operator supplied the pages for the current OpenAI model prices and the ChatGPT credit rules on October 2. They are starting evidence: whoever writes the first rate entries reads each page again and records the date checked.

## The one ledger, and recalculation

Usage records and cost figures go into the spend ledger the harness already keeps. There is no second ledger. Ledger entries written before this design carry no counters, and are read with every counter absent, never zero.

An estimate is appended and never rewritten. When a rate entry is corrected, a recalculation appends a new estimate for each affected call, naming the new rate entry and the estimate it replaces. Surfaces show the latest estimate; the earlier ones stay readable, so any figure shown can be traced to the rate that produced it. A reported figure is never recalculated.

## What the surfaces show

The read model in [observability and the dashboard](observability-and-dashboard.md) computes every total from the ledger, and `yoyo cost`, `yoyo status`, the dashboard's spend box, and Slack present it. Per work item, per run, and in total, each figure shows its reported subtotal, its estimated subtotal, its comparison figure apart, and its unknown share as a count of calls and a percentage. That design's cost entry, which still calls estimates reserved for future sources and lists a class for figures that cannot be attributed, is revised to match this design.

**Until the commands move onto the ledger.** Today the price columns that `yoyo cost` and `yoyo status --spend` print are still added up from the run and conversation event logs, not from the ledger. Cost by kind is added up from the ledger and shown beside those columns, under its own label, until those commands are moved onto the ledger. While both are shown:

- each figure is labelled with where it was added up from, so a reader never takes one for the other;
- the two are never added together, never compared as though they should match, and never folded into one total;
- the figures from the event logs are not a fifth kind of cost; they are what the commands showed before this design, kept so that nothing the operator already reads disappears.

Moving those commands onto the ledger, and removing the columns added up from the event logs, is build work still to do. When it lands, this paragraph is removed and every figure comes from the ledger alone. Until then, a reviewer checks that every price figure these commands show carries a label naming where it was added up from.

## What this reuses

- The spend ledger and session accounting the harness already keeps, which this design extends with counters and kinds of cost rather than replacing.
- The read model, which already computes the totals every surface shows.
- Each adapter's existing reading of its provider's events, which gains the counters and the reported cost and nothing else.
- The work that tests the Codex adapter against real recorded streams from the supported versions (yoyodyne-ifd.435.7), whose recordings cover the Codex cases below where they apply.

## Tests

The tests cover a grid: each adapter, Claude Code and Codex, on each path named above, in each case:

- a fresh session;
- a resumed session reporting running totals;
- a counter reset;
- a repeated final event;
- a call cut off part way;
- a failure with no usage;
- a measured zero beside an absent counter;
- one call above a per-request rate threshold in a session whose total is above it while the call is not;
- each billing mode;
- a call made for no work item, counted under its role and in the total.

Adapter cases run against recorded provider streams. The rate data has its own tests: every entry names its source page and date checked, and no two entries overlap in time.

## Alternatives rejected

- **Pricing inside each adapter** was rejected because prices would be scattered across code and drift between providers.
- **A separate cost ledger** was rejected because the spend ledger is already the one record surfaces read, and a second would be a place for numbers to disagree.
- **Adding estimates into reported totals** was rejected because the operator would read a guess as money the provider stated.
- **Deciding a rate tier from a session's running total** was rejected because providers apply per-request thresholds to each request, not to the session.
- **A fifth kind of cost for figures tied to no work item** was rejected because attribution and kind answer different questions; mixing them would hide what such a figure is.

## What a reviewer can check

- Every invocation path writes one usage record per provider call.
- No absent counter is shown as zero.
- The five counters add up without counting a token twice.
- No total mixes kinds of cost.
- No price appears outside the rate data.
- A recalculation leaves the earlier estimate readable.
- An unknown figure always says why.
- A figure tied to no work item appears in the total and under its role.

## Evidence and limits

The rules were taken from the confirmed October 13 text as the stopped publishing run's record holds it, read on October 20. The provider adapters design was read on main at 7a0f0dfd and still carries its October 2 text, so it does not yet link here; its next revision adds that link. No code was read for this design, so the shared packages the rules touch are named by what they do rather than by path, and the developer names them before starting.
