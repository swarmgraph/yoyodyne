---
id: reading-published-prices
kind: design
title: 'Reading the providers'' published prices: how the harness fills the price list without any developer run reaching the network'
supports:
    - v1-goals
    - operations-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-28T02:39:06.183006Z
      reason: 'yoyodyne-ifd.435.34: rules how the harness reads the providers'' published prices and fills the price list. It reuses the pinned outside files fetching limits, keeps dated page copies out of the repository, hands them to the developer who writes the reading, and lands every price change through a reviewed run. It also corrects the item''s third done condition: a subscription call is shown for comparison only, never as an estimate.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-28T02:39:06.183006Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1878, for document-1878.1 under the automatic approval policy
---
# Reading the providers' published prices

## What this is for

This design serves the goal that spend follows the work, and the goal that the operator can see what the harness spends on their behalf. [Provider usage and cost](provider-usage-and-cost.md) says every estimate is computed from rate entries in `internal/pricing/rates.json`, and that each entry names the page its price was read from and the day it was checked. That file has no entries. The developer run that built it could not reach the pricing pages the operator supplied on October 2, because developer runs have no network ([the price list's README](../../internal/pricing/README.md)). So every Codex call with no cost reported by the provider shows as unknown.

No role can read a web page today. That is a missing capability, not a step for a person. This design says how the harness reads the pages itself and fills the price list through a reviewed change, without widening what any developer run can reach. It was ruled on October 27 for yoyodyne-ifd.435.34.

## Why the pinned outside files route is not used as it stands

[Pinned outside files](pinned-outside-files.md) already lets the harness fetch a file from outside the machine and land it through a reviewed run. Three of its rules do not fit a pricing page:

- A pinned address must contain the declared version in its path. A pricing page has no version; it changes in place.
- A pinned file is landed exactly as served and is never interpreted by the harness. Prices have to be read out of the page.
- A pinned file is committed to the repository. A full copy of a provider's web page is someone else's work and is not needed by anyone once its prices are read.

So this design reuses that route's fetching and its safety limits, and nothing else of it. The pinned outside files design is unchanged.

## The list of pages

The pages are listed in `internal/pricing/sources.json`, on main, changed only by a reviewed change. Each entry names:

- `url`: the page's address;
- `provider`: the backend identifier the rate entries will carry, such as `codex`;
- `billing_mode`: `api_key` or `credits`, as the price list's README defines them;
- `for`: the full id of the work item that asked for the page.

It starts with the two pages the operator supplied on October 2: the OpenAI model price page and the ChatGPT credit rules page, as the price list's README names them.

The hosts of those pages must also be on the project's list in `.yoyodyne/config.yaml` under `outside_files.hosts`. There is one list of hosts for every reach outside the machine. In this repository the development manager decides it, under section 6 of [portable agent configuration](portable-agent-configuration.md).

## Fetching and keeping a page

The harness fetches a listed page in its own process, outside every agent's sandbox, with the same fetching code and under the same limits as a pinned file:

- https only, a host on the list, no user name, password, query string or fragment;
- no credentials, cookies or authorization header;
- at most five redirects, each to https and a listed host, with the final address recorded;
- at most `outside_files.max_bytes`, and never more than 50 MiB.

The one rule that does not apply is that the version must appear in the address. It exists to make a pinned file's identity fixed, and a page has none. Here the identity is the checksum of what was fetched and the time it was fetched. The address itself has still been reviewed on main.

Only addresses in `sources.json` on main are fetched. An address a developer run has just written in its checkout is never fetched, for the same reason as in the pinned outside files design: an address no reviewer has seen could carry data out.

The harness keeps each fetched page in its state directory, through the confined writer, named by the page and the time it was fetched, with its checksum, its size and the final address. Kept copies are never committed to the repository, and like all history they are kept.

## Writing the reading

The code that reads prices out of a page is ordinary code in `internal/pricing`, written by a developer run and reviewed like any change. It is one function from a page's bytes to rate entries, with a reason for each model it could not read. It never runs anything the page contains, and it never guesses: a price it cannot find is left out, with its reason.

A developer cannot write that code without seeing the page, and a developer run has no network. So:

1. The harness fetches and keeps every listed page first, as above, before the item that writes the reading starts.
2. When a developer run starts on an item that names `internal/pricing/sources.json` as what it reads, the harness places the latest kept copy of each listed page in that run's checkout, in a directory git ignores, and names the directory in the run's instructions.
3. The developer writes the reading and its tests. The tests use short excerpts of the price rows the reading uses, saved under `internal/pricing/testdata` with the page's address and the date it was fetched. A full page copy is never committed; a change that adds one is refused by the reviewer.

The sandbox code was not read for this design. If the harness cannot place a file in a run's checkout before the run starts, the build item says so and asks the architect, rather than widening what the run can reach.

## The run that reads prices

Once the reading is on main, a harness run fills the price list. It runs once when a listed page has no rate entry naming it, and after that once a week as one of the harness's recurring tasks, whose schedule the development manager sets under section 6.

1. **Its own place.** At most one such run is in progress at a time. It never takes a developer slot, and no developer run waits on it, as for a pinned file.
2. **Attributed.** The run is attributed to the item each page names in `for`, and its cost is counted there.
3. **Fetch and keep** each listed page, as above.
4. **Read** each kept copy with the reading on main.
5. **Compare** with `rates.json` on main. If every price read is the same as an open entry, the run ends with no change, and the item's notes say when the pages were read and that nothing changed.
6. **Write** where prices changed:
   - a new model or a new price is a new entry, with `source` the page's address and `checked` the date of the fetch;
   - where a price changed, the old entry is given `effective_until`, and the new one `effective_from`, both the date of the fetch. The page does not say when its price changed, so the reviewer is told the true date may be earlier;
   - the file's `version` changes, as the README requires.
   The write goes through the confined writer, with the run's checkout as the root. The candidate changes only `rates.json`; a candidate that changes anything else is refused before any check runs.
7. **Checks.** The project's full checks run on the exact candidate, and the evidence is bound to it, as for any change. Those checks include the rate data's own tests: every entry names its source page and date checked, and no two entries overlap in time.
8. **Review.** An independent reviewer judges the candidate. The reviewer is shown, for each page: its address, the final address, the time it was fetched, its checksum, and the part of the page the reading used, up to 4 KiB a page. Every new or closed entry is shown beside it. The reviewer refuses any entry that differs from the page.
9. **The merge** goes through the pull request, and through the merge queue when it is switched on. It is handled exactly as step 8 of the run that brings a pinned file in, including what happens when the queue's combined change fails: the run ends with its reason on the item, a later pull starts a fresh run, and three such runs in a row stop it with one warning report for the development manager.

## When reading fails

- **The network.** A connection that fails, a timeout or a server error is waited out and tried again at a later run, with nothing counted against it.
- **The address.** An address that breaks the fetching rules, a redirect to a host not on the list, a missing page or a page over the size limit stops that page. The harness files one warning report for the development manager naming the page and the reason. The page is tried again only after its entry in `sources.json` changes on main.
- **The reading.** A page the reading can no longer read, because its layout changed, writes no change for that page. The harness files one warning report for the development manager naming the page and what the reading could not find. It files no further report for that page until the kept copy's checksum changes. The prices already in `rates.json` stay as they are, with the date they were checked, so a reader can see how old they are.

Nothing in any of these is put to the operator.

## What the surfaces show

Nothing changes in how cost is shown; [provider usage and cost](provider-usage-and-cost.md) decides it. Once entries exist, a Codex call shows one of the four kinds that design names, according to how it was paid:

- paid by an API key: an **estimated** cost, labelled as an estimate and naming the rate entry;
- paid by a subscription: a **comparison only** figure at the API price, never shown as money spent;
- paid by credits: the credits, from the credit rules page;
- where the model, the usage or a matching entry is missing: **unknown**, saying why.

## What is built, in what order

Turning this into work items, and their order, is the development manager's. The pieces are:

1. `sources.json`, the fetching and keeping of listed pages, and the first fetch. Tested against a fake fetch, including every refused address. It uses the fetching code of pinned outside files (yoyodyne-ifd.429.90) and waits on it where that is not yet on main.
2. Placing the kept copies in a developer run's checkout for an item that reads `sources.json`.
3. The reading in `internal/pricing`, tested on excerpts of the real pages.
4. The run that reads prices, with its weekly recurring task and its failure handling, tested against a fake fetch and a fake page whose prices change.
5. A test that a Codex call is shown as the kind its billing mode decides, as listed above.

## Alternatives rejected

- **Pinning the pages as outside files.** A page has no version for its address, the pinned route forbids reading what it fetches, and it would commit a full copy of a provider's page.
- **A developer run reading the page.** It would widen a sandbox, and every run on that seat could then carry data out.
- **The operator writing the prices by hand.** That is a hand step outside the operator's closed list, and prices change.
- **An AI session reading the page and writing prices.** Its answer could not be checked by a test, and a misread price would look like a real one. The reading is code, reviewed once and tested, and the reviewer still compares each entry with the page.
- **The harness writing the price list straight to main.** Every change lands only after checks and an independent review.

## What is enforced in code

- No fetch happens inside any agent's sandbox, and no developer run gains network reach.
- Only addresses in `sources.json` on main are fetched, only from listed hosts, and only under the fetching limits.
- The run that reads prices changes only `rates.json`.
- No full page copy is committed.
- No price appears outside `rates.json`, as the cost design already requires.

## What a reviewer can check

- With an empty host list, nothing is fetched.
- An address not yet on main, an address with a query string, a host not on the list, or a redirect to one is refused before any request is sent, or at the redirect.
- Pages whose prices did not change produce no pull request.
- A changed price closes the old entry and opens a new one dated the day of the fetch.
- A page the reading cannot read writes no change and files exactly one warning report until its copy changes.
- A Codex call paid by a subscription is never shown as an estimate.

## Evidence and limits

Read at main 18b3d3d5: the pinned outside files design, the provider usage and cost design, and the price list's README. No code was read: not the pinned files fetcher, not the sandbox, and not `internal/pricing/pricing.go`. Whether the pinned files fetcher is on main was not checked; the first build item checks it.