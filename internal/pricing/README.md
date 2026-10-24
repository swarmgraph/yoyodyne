# Rate data

`rates.json` is every price the harness estimates a provider call's cost from.
It is data rather than code so that a price is changed by a reviewed change to
this file and nowhere else; `pricing.go` beside it is the one place it is read.
The rules it follows are in
[the provider usage and cost design](../../docs/designs/provider-usage-and-cost.md).

## What an entry says

```json
{
  "id": "openai-example-model-api-standard-2026-10-02",
  "provider": "codex",
  "model": "example-model",
  "billing_mode": "api_key",
  "tier": "standard",
  "default_tier": true,
  "effective_from": "2026-10-02",
  "unit": "usd",
  "prices": {
    "fresh_input": {"per_million": 1.25},
    "cached_input": {"per_million": 0.125},
    "cache_write": {"rule": "no_charge"},
    "output": {"per_million": 10},
    "reasoning": {"rule": "charged_as", "as": "output"}
  },
  "long_context": {"above_input_tokens": 272000, "prices": {"...": "the same five"}},
  "source": "https://the page the prices were read from",
  "checked": "2026-10-23"
}
```

The figures above are an example of the shape and are not anybody's price.

- `provider` is the backend identifier the harness records, and `model` the
  model identifier the provider reports serving the call, matched exactly.
- `billing_mode` is `api_key` (dollars) or `credits` (`unit` `credits`). A
  subscription has no per-token price; a subscription call is compared at the
  `api_key` entry for the same model, and that figure is shown as a comparison,
  never as money spent.
- `tier` is the processing tier, and `default_tier` marks the one the provider
  applies to a request that names none, which is every request the harness
  makes. A call whose provider reported no tier is priced at it and says so.
- Every one of the five counters has a price or a rule: `no_charge` where the
  provider states it charges nothing, `charged_as` with `as` where it charges
  the counter at another's price, and `not_separate` where the provider does not
  report the counter apart because its tokens are inside another counter.
- `long_context` is the rate above a per-request input size. A call is placed
  above it only when its own input was reported whole and it was one request;
  otherwise the figure is unknown and says why.
- `effective_from` and the optional `effective_until` bound the entry in time.
  Entries for one provider, model, billing mode, and tier never overlap.
- `source` is the page the price was read from and `checked` the day it was
  read there. A changed price is a new entry from the day it changed, with the
  old one given an `effective_until`, and `version` at the top of the file
  changes with every edit, because every estimate names the version it used.

## What is here now

No entries yet. The operator supplied on October 2 the pages for the OpenAI
model prices (`https://developers.openai.com/api/docs/models/gpt-6.1-sol`) and
the ChatGPT credit rules
(`https://learn.chatgpt.com/docs/pricing#what-are-tokens-and-credits`). The
design asks whoever writes the first entries to read each page again and record
the day they did; the run that wrote this file could not reach either page, so
it wrote none rather than prices nobody checked. Until entries are added, a
call with no provider-reported cost is shown as unknown, with the reason that no
rate entry covers it.
