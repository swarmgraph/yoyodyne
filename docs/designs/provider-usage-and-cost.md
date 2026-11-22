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
    - action: amended
      by: architect
      at: 2026-11-22T08:44:38.85751Z
      reason: 'Brings the provider usage and cost design into line with what is built, from the review of every design against the code: a call is one invocation; a table of built parts; the empty rate data means every estimate is unknown; the Claude Code adapter''s own rule for repeated final events moves to the shared code; the merge queue review joins the paths; two Codex recordings exist; the stale dashboard pointer is dropped; four build pieces.'
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
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-11-22T08:44:38.85751Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3434, for document-3434.1 under the automatic approval policy
---
# Provider usage and cost

**What this is for.** This design serves the goal that the operator can see what the harness spends on their behalf, per work item, per run, and in total, and the goal that spend follows the work. [Provider adapters and execution endpoints](provider-adapters-and-endpoints.md) lists usage and cost reporting as part of the adapter contract; this design says what that reporting means, so that every provider, every compiled adapter, and every way the harness calls a provider report spend the same way into the one spend ledger that already exists. Reading a provider's own events stays in its adapter; everything after that is shared.

The rules were first written on October 13 as a section of the provider adapters design and filed here as a design of their own on October 20. This revision, of November 22, says which parts are built, after the review of every design against the code.

## Where this stands

| Rule | State | Where the code is |
|---|---|---|
| One usage record for each call | Built | `internal/spend/spend.go`, one ledger line for each invocation |
| Cost by kind, shown beside the older columns | Built | `internal/cli/cost.go`, `internal/cli/status.go` |
| One pricing service | Built; the rate data is empty, so every estimate is unknown | `internal/pricing/pricing.go`, `internal/pricing/rates.json` |
| Shared code turns readings into amounts for each call | Built in `internal/cost`, but the Claude Code adapter still applies its own rule for a repeated final event and reads cost off the stream | `internal/backend/claudecode/parser.go`, `internal/backend/claudecode/subagents.go` |
| Five counters, billing mode, partial and reset marks | Not checked in this revision | |
| The test grid | Codex has two recordings, one of which carries no usage; the other cases have none | `internal/backend/codex/testdata/streams` |

The build pieces at the end of this design close each gap.

## One usage record for each call

In this design a **call** is one invocation of a provider by the harness: one launch of the provider for one turn, one review, or one run attempt. A later invocation that resumes the same provider session is a new call.

Every call writes exactly one usage record, on every path: a developer run, including its repair rounds and continuations; a review, including a second ask; a merge-queue review, including a switch to an alternate account; a conversation turn of any role, including a scheduled pass; an exchange between roles; a side thread; and a document landing run. A path that calls a provider and writes no usage record is a defect.

A usage record carries:

- **Who called.** The run or conversation, the turn, and the attempt; the endpoint in full: provider, adapter version, account alias; the model the harness asked for and, where the provider says, the model that answered.
- **How it is paid for.** The billing mode: API key, subscription, purchased credits, or `not recorded`. The adapter reads it from the account's configuration and the provider's own report; it never infers it from a model name.
- **Five counters.** Fresh input, cached input read, cache writes, output, and reasoning. Each counter is one of: a measured number, a measured zero, or absent because the provider did not report it. Absent is never stored or shown as zero.
- **Reported cost.** The amount in US dollars the provider itself reported for the call, where it reported one, naming the provider.
- **Where it came from.** The normalized event schema version and a pointer to the raw provider events the counters were read from, which are kept with the run's or conversation's event log as they are today.

**The counters never overlap.** Fresh input excludes cached input and cache writes; output excludes reasoning. Where a provider reports a total that includes another counter, the adapter subtracts it, and the record says the adapter did so. Codex reports cached input inside its input count and reasoning inside its output count; Claude Code reports cache reads and cache writes apart from input. Adding the five counters therefore counts every token once.

## Turning running totals into amounts for each call

Some providers report a running total for the session rather than an amount for the call, and a resumed session carries its total forward. The adapter hands each reading to one shared piece of code in `internal/cost`, marked as a running total or an amount for the call, with the session and the provider's own identity for the event. That shared code alone turns readings into amounts for each call. No adapter keeps its own running total, compares readings, or decides that an event is a repeat. The rules:

- A running total becomes the difference from the last reading for the same session. A fresh session starts from zero.
- A reading lower than the last one for that session is a reset: it is taken whole as the call's amount, and the record says a reset was seen.
- A final event repeated with the same event identity is counted once.
- A call that was cut off, because the provider's stream stopped, the run was ended, or the machine slept, records what the provider reported before the cut, marked as partial.
- A call that failed with no usage reported writes a record whose counters are all absent, not zero.
- A rate tier that depends on how large one request is, such as a higher rate above a set number of input tokens, is decided from that call's own input. It is never decided from a session's running total. Where the call's own input was not reported, the tier is unknown.

Today the Claude Code adapter breaks the first sentence of this section: its parser keeps its own reported and repeated cost figures and adds the difference for a repeated final event itself, and it decodes the cost its helper agents report. Moving that into the shared code is build piece 1. The adapter keeps reading the fields off the stream and hands them on marked; it stops deciding what they add up to.

## Four kinds of cost, never added together

Every cost figure is exactly one of:

- **Reported**: the provider's own figure for the call. It is what the provider said the call cost, not an invoice, and it is never recalculated.
- **Estimated**: computed by the pricing service from the counters and a rate entry, naming the rate entry and its version.
- **Unknown**: neither reported nor computable, because the counters were absent, or no rate entry matches, or the tier could not be decided. The figure says why.
- **Comparison only**: what the same use would cost at the provider's API prices, for a call paid for by a subscription or purchased credits. It is not money spent. What a subscription actually charges is the subscription itself; purchased credits used are shown as credits where the provider reports them.

No total adds two kinds together. Every total shows a reported subtotal, an estimated subtotal, the comparison figure apart from both, and how many calls and what share of them are unknown.

**Which work a cost belongs to is a separate question from its kind.** Every usage record names the run or conversation that made the call, so every figure can be attributed to the role that spent it. A figure whose call was made for no work item, such as a scheduled pass or a conversation turn about no item, is counted in the total and shown under its role and conversation as not tied to a work item. It is never dropped, and it is never a fifth kind of cost: it is still reported, estimated, unknown, or comparison only.

## The pricing service and the rate data

One pricing service, in `internal/pricing`, computes every estimate. It takes a usage record and returns an estimate naming the rate entry it used, or an unknown with its reason. No price, and no condition on a model's name to find one, appears anywhere else in the code.

Rates are data, kept in `internal/pricing/rates.json` beside the pricing service and changed by reviewed change like any other file. Each entry names: the provider; the model; the billing mode; the tier; the date it takes effect; the size of one request above which a different rate applies, and that rate; a price for each of the five counters, or the provider's stated rule where a counter has no separate price (for example, a provider that charges nothing extra for cache writes says so in its entry rather than leaving the price missing); the page the price was read from; and the date it was checked. Entries for one provider, model, billing mode, and tier never overlap in time. A record that matches no entry, or matches one whose tier it cannot decide, is unknown, never priced by guess.

**The rate data is empty today.** The file holds a version and no entries, so every estimate the ledger shows is unknown, with no matching rate entry as its reason. That is the correct answer under the rule above, not a fault in the pricing service. Writing the first entries is build piece 2. The operator supplied the pages for the current OpenAI model prices and the ChatGPT credit rules on October 2; they are starting evidence, and whoever writes the first entries reads each page again and records the date checked. [Reading published prices](reading-published-prices.md) is the plan for keeping entries current once they exist.

## The one ledger, and recalculation

Usage records and cost figures go into the spend ledger the harness already keeps. There is no second ledger. Ledger entries written before this design carry no counters, and are read with every counter absent, never zero.

An estimate is appended and never rewritten. When a rate entry is corrected, a recalculation appends a new estimate for each affected call, naming the new rate entry and the estimate it replaces. Surfaces show the latest estimate; the earlier ones stay readable, so any figure shown can be traced to the rate that produced it. A reported figure is never recalculated.

## What the surfaces show

The read model in [observability and the dashboard](observability-and-dashboard.md) computes every total from the ledger, and `yoyo cost`, `yoyo status`, the dashboard's spend box, and Slack present it. Per work item, per run, and in total, each figure shows its reported subtotal, its estimated subtotal, its comparison figure apart, and its unknown share as a count of calls and a percentage.

**Until the commands move onto the ledger.** Today the price columns that `yoyo cost` and `yoyo status --spend` print are still added up from the run and conversation event logs, not from the ledger. Cost by kind is added up from the ledger and shown beside those columns, under its own label, until those commands are moved onto the ledger. While both are shown:

- each figure is labelled with where it was added up from, so a reader never takes one for the other;
- the two are never added together, never compared as though they should match, and never folded into one total;
- the figures from the event logs are not a fifth kind of cost; they are what the commands showed before this design, kept so that nothing the operator already reads disappears.

Moving those commands onto the ledger, and removing the columns added up from the event logs, is build piece 3. When it lands, this paragraph is removed and every figure comes from the ledger alone. Until then, a reviewer checks that every price figure these commands show carries a label naming where it was added up from.

## What this reuses

- The spend ledger and session accounting the harness already keeps, which this design extends with counters and kinds of cost rather than replacing.
- The read model, which already computes the totals every surface shows.
- Each adapter's existing reading of its provider's events, which gains the counters and the reported cost and nothing else.
- The work that tests the Codex adapter against real recorded streams from the supported versions (yoyodyne-ifd.435.7). Its recordings are where the Codex cases of the test grid come from; today it has two, and only one of them carries usage.

## Tests

The tests cover a grid: each adapter, Claude Code and Codex, on each path named above, in each case:

- a fresh session;
- a resumed session reporting running totals;
- a counter reset;
- a repeated final event;
- a call cut off part way;
- a failure with no usage;
- a measured zero beside an absent counter;
- one call above a rate threshold for one request, in a session whose total is above it while the call is not;
- each billing mode;
- a call made for no work item, counted under its role and in the total.

Adapter cases run against recorded provider streams. The Codex cases wait on recordings from the Codex adapter testing work (yoyodyne-ifd.435.7); the Claude Code cases are build piece 4. The rate data has its own tests: every entry names its source page and date checked, and no two entries overlap in time.

## Build pieces

These are for the development manager to turn into work items. The parked item on shared, attributable cost reporting (yoyodyne-ani) covers this ground and should be checked first, so nothing is admitted twice.

1. **The shared code alone turns readings into amounts.** The Claude Code adapter hands its cost and usage readings to `internal/cost` marked as a running total or an amount for the call, with the event identity, and keeps no running total of its own. Its helper agents' reported cost goes the same way. Done when no file under `internal/backend` compares two cost readings or decides that an event is a repeat, and the existing Claude Code cost tests pass unchanged in what they expect.
2. **The first rate entries.** Entries for the models this repository runs, each naming its source page and the date checked, read again from the provider's page at the time of writing. Done when an estimate for a call on a priced model is shown as estimated, naming its entry, and the rate data tests pass.
3. **`yoyo cost` and `yoyo status --spend` read the ledger alone.** The columns added up from the event logs are removed, and the paragraph in this design about showing both is removed in the same change. Done when every price figure those commands print comes from the ledger.
4. **The Claude Code half of the test grid.** A recorded stream for each case in the grid that has none today. Done when each case has a recording and a test reading it.

## Alternatives rejected

- **Pricing inside each adapter** was rejected because prices would be scattered across code and drift between providers. The same reason rejects leaving the Claude Code adapter's own rule for repeated final events in place: it is the first step of that drift.
- **A separate cost ledger** was rejected because the spend ledger is already the one record surfaces read, and a second would be a place for numbers to disagree.
- **Adding estimates into reported totals** was rejected because the operator would read a guess as money the provider stated.
- **Deciding a rate tier from a session's running total** was rejected because providers apply their thresholds to each request, not to the session.
- **A fifth kind of cost for figures tied to no work item** was rejected because attribution and kind answer different questions; mixing them would hide what such a figure is.
- **Defining a call as each request the provider receives inside one invocation** was rejected because the harness sees one stream for each invocation, and the provider does not report its own requests apart. Invocation is the unit the ledger already records.

## What a reviewer can check

- Every invocation path, the merge-queue review included, writes one usage record for each call.
- No absent counter is shown as zero.
- The five counters add up without counting a token twice.
- No total mixes kinds of cost.
- No price appears outside the rate data, and no adapter decides what two cost readings add up to.
- A recalculation leaves the earlier estimate readable.
- An unknown figure always says why.
- A figure tied to no work item appears in the total and under its role.

## Evidence and limits

This revision was written from this design as main held it at commit 63128145fe40 and from the second reading of the review of every design against the code, for execution, providers and cost (`docs/diagnoses/yoyodyne-ifd-437-84-6b.md`), read at the same commit. That reading was itself taken on November 21 against main at 848d86190 and ran nothing. The file paths and behaviour in the table under Where this stands are taken from that reading; the code itself was not read for this revision. The five counters, the billing mode, and the partial and reset marks were not checked against the code, and the table says so rather than guessing.