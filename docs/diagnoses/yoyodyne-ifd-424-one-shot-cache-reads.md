# yoyodyne-ifd.424: short one-shot invocations read the cache they write, and what they write for nobody costs double

The finding, as raised: short one-shot invocations — the reviewer's, and by
shape the triage, exchange, and sweep turns — report `cache_read_input_tokens`
of exactly 0 almost without exception while writing 10,000 to 160,000 tokens of
fresh cache each time. yoyodyne-ifd.205 changed how a review is invoked so that
it would read a shared prefix, and its experiment document
([`docs/experiments/yoyodyne-ifd-205-review-prompt-cache.md`](../experiments/yoyodyne-ifd-205-review-prompt-cache.md))
asked for an after-window measurement that was never taken. This is that
measurement, the cause of what it shows, the change made on it, and two things
found on the way that are larger than the finding.

Everything below is read from the recorded provider terminals under the
product's state directory on 2026-09-20 — 573 run logs, 7 conversation logs, and
25 branch-review logs, 3,902 priced terminals in all — with the harness's own
commands where one exists and a scratch script over the same logs where none
does. A developer run reads that store and cannot write the tracker, so the
figures the item asked to have quoted on it are quoted here.

## The after window

The command the experiment document records, on the checkout at `ccc252f`:

```text
$ ./bin/yoyo cost
cache-read share by phase: development 98.6% over 634 invocation(s), review 18.7% over 1175 invocation(s), repair 96.5% over 532 invocation(s);
```

That line is every run ever recorded. The document's criterion is a window of
runs started after the promotion that landed 205, which merged as `9f208db` at
2026-08-31T05:08Z. Cutting `./bin/yoyo cost --json` on `started_at` at that
moment, by phase:

| Window | Phase | Invocations | Cached | Written to the cache | Fresh | Cache-read share |
|---|---|---|---|---|---|---|
| Before 205 | Review | 537 | 121,343 | 24,318,186 | 1,070 | **0.50%** |
| After 205 | Review | 638 | 13,868,680 | 36,661,460 | 1,550 | **27.45%** |
| Before 205 | Development | 297 | 3,566,351,051 | 56,070,618 | 270,496 | 98.44% |
| After 205 | Development | 337 | 5,283,151,174 | 65,777,015 | 53,509 | 98.77% |

All 638 after-window review invocations carried a usage object; none is
unmeasured. **The review phase's share rose from 0.50% to 27.45%, and 205's done
condition — review invocations showing nonzero cache reads — is met.** Of the
638, 579 read a nonzero amount; 59 read nothing, and 28 of those were the first
review after a gap of more than an hour, which is the cache lifetime.

The same window under the instrument this item adds,
`./bin/yoyo status --spend 21` (the twenty-one local days to 2026-09-20, which
begins two hours after the promotion):

```text
role                   calls        cache_w  cache_w USD        cache_r  cache_r USD  cache_r%        USD
developer                607    124,960,914     $1336.84  7,181,034,059     $4036.27     98.3%   $5933.59
reviewer                 630     36,445,160      $365.51     13,868,680        $6.94     27.6%    $523.39
product-manager          392     34,373,545      $674.86    155,713,461      $294.73     81.9%   $1046.19
development-manager      705     81,545,332     $1611.74    225,890,705      $771.65     73.5%   $2461.42
architect                 24      4,795,112       $89.40      5,627,214        $4.98     54.0%     $99.33
```

## What a review actually reads, and what it writes

The share alone does not say whether the flag did what it was meant to. The
per-invocation reads do. After 205, a review's `cache_read_input_tokens` is one
of a handful of fixed values — 4,647 on every review from 2026-08-31 to
2026-09-04, then 4,874, 5,934, 6,062, 5,781, and 6,076 as the installed CLI and
the appended contract changed — with a median of 5,669 over the window and 477
of 638 reviews between 4,000 and 7,000. That is the whole system block: Claude
Code's own static system prompt for an invocation with no tools, plus the review
contract (11,112 bytes) and the reviewer persona (2,028 bytes) the harness
appends. Every review reads all of it back, so the prefix is shared and the
flag is doing exactly what 205 said it would.

What a review writes is everything after that: the evidence prompt — work item,
invariants, patch, check results — which is unique to the review and goes on
stdin as the one user message. Claude Code puts a cache breakpoint on the last
user message as well as on the system block, so the whole evidence prompt is
written into the cache on every review: a median of 39,304 tokens and up to
277,702, at the write rate, and nothing ever reads it back except the 66
reviews whose prompt was byte-identical to one made less than an hour earlier
(a review reissued after a transient failure, or a re-review of an unchanged
change), which read 50,000 to 828,879 tokens.

So the finding as raised describes the before window. In the after window a
one-shot invocation does read the cache it writes — the part of it that is
shared — and the 10,000 to 160,000 tokens written each time are the part that
cannot be.

### The lifetime, and what it costs

Every cache write the harness has ever made is `ephemeral_1h_input_tokens`:
3,631 of the 3,632 terminals that wrote anything wrote at the one-hour lifetime,
one wrote at five minutes. Claude Code chooses the lifetime, and its own
schema says how: unset, it is "1 hour on a Claude subscription within its usage
limits, 5 minutes on an API key"; the environment variable
`CLAUDE_CODE_PROMPT_CACHE_TTL` (`"5m"` or `"1h"`) takes precedence over
everything else for the main conversation, `-p` turns included. The one-hour
write is billed at double the fresh input rate; the five-minute write at a
quarter over.

The rate multiples are the provider's and hold across its models, and the
recorded terminals confirm it: pricing each invocation's usage at fresh 1×,
cache read 0.1×, five-minute write 1.25×, one-hour write 2×, output 5× and
solving for the base rate, 1,317 of 1,399 opus-5 terminals and 484 of 485
fable-5 terminals land on one base rate within one per cent ($5 and $10 per
million respectively). fable-5-1's writes and output fit the same multiples at
$10 per million; its cache reads are priced at $0.25 per million rather than
the $1.00 the tenth would give, so the apportioning below overstates that
model's read cost fourfold and its write cost not at all.

Over the 638 after-window reviews, at the opus-5 base rate the multiples
reproduce the recorded review cost to within half a per cent ($524.68 modelled,
$527.02 recorded), which is what licenses the rest of this table:

| Lifetime | Review cost over the window | Per review |
|---|---|---|
| One hour, as recorded | $524.68 | $0.82 |
| Five minutes, modelled: a read survives only where the previous review ended inside five minutes (21% of gaps; median gap 749s) | $453.85 | $0.71 |
| Five minutes, worst case: every read lost | $466.94 | $0.73 |
| No caching at all: every token fresh | $403.78 | $0.63 |

The one-hour write premium on the 36,661,460 tokens the reviews wrote is
$137.48 over the window — a quarter of the phase's cost — against 13,868,680
tokens read back by the prefix and the reissued reviews, which cost $6.94 and
would have cost $69.34 fresh. The five-minute lifetime
keeps the reads that happen inside five minutes and cuts the premium to
$45.83. Caching costs the reviewer money at either lifetime, because what it
shares is six thousand tokens and what it writes is forty thousand.

## What changed

The reviewer's invocations are made with `CLAUDE_CODE_PROMPT_CACHE_TTL=5m`
(`internal/backend/claudecode/backend.go`, beside the flag 205 added). It is
the reviewer's alone: a review is one turn nobody resumes, by the contract in
`internal/review`, and the other roles resume a session whose whole transcript
is the cached prefix, which an hour is what keeps warm.
`TestAReviewIsCachedForFiveMinutesAndNoResumedSessionIs`
holds it onto the reviewer and off every other role, and holds that the
harness's own value replaces one carried through from the operator's
environment. The variable is read by the installed CLI's own TTL selection —
`FORCE_PROMPT_CACHING_5M` first, then this variable for the main thread, then
the `promptCacheTtl` setting, then the agent's frontmatter, then
`ENABLE_PROMPT_CACHING_1H` — which was read off Claude Code 2.1.278 itself; no
provider call could be made from the run to watch it take, because the run's
sandbox refuses the provider's host, so the after-window of this change is
taken the way 205's was: on the reviewer's line of `yoyo status --spend` over
the reviews made after it lands, where `cache_w USD` should fall by roughly
three eighths against `cache_w` tokens and `cache_r` should not go to nought.

Disabling caching for the reviewer outright would save a further $50 over the
same window on the model above. It is not done here: it would take away the
reads the reissued reviews get, it reverses the decision 205 landed, and it is
a smaller sum than the modelling error, so it is named for the product manager
rather than taken.

`yoyo status --spend` now prints the table quoted above: per role, the cache
writes and reads in tokens, each role's reported cost apportioned across what
it was billed for at the multiples above, the role's cache-read share, and its
total. The apportioning is per invocation and normalised to the provider's own
figure, so a role's parts add up to what the provider said and no price of the
harness's enters it. `--json` carries the same split on every row under
`roles`. It answers the question this item was admitted on — does a role read
the cache it writes, and what does writing it cost — where the one cache-read
share under the total cannot, because that share is decided by the developer's
98%.

## The turns the concern named, measured

The concern named the triage, exchange, and sweep turns as one-shot by shape.
They are not: no exchange or side-stream record exists on this machine, and
triage and the sweeps are turns of the development manager's and product
manager's conversations, which resume a session. Their reads are the resumed
transcript — a median of 260,611 tokens a turn for the development manager and
376,873 for the product manager — and their shares are 73.5% and 81.9%. What
they show instead is the first of the two things below.

## Found on the way: the hourly sweep sleeps just past the cache

The development manager's recorded conversation has 94 turns that read
nothing and wrote a median of 618,355 tokens each; the product manager's has
53 writing a median of 542,067. Those are resumed turns whose cache had
expired, and they are exactly the turns that follow a gap of more than an hour:
96% of the development manager's cold turns and 88% of the product manager's
follow such a gap, against 1.5% and 1.9% of the warm ones. The development
manager's cold gaps have a tenth percentile of 3,730 seconds and a median of
4,571 — the hourly sweep (`recurring_tasks.development-manager-sweep.every: 1h` in
`.yoyodyne/config.yaml`) firing an hour and change after the turn before it,
which is a few minutes past the one-hour cache lifetime. Each such turn rewrites
the whole transcript at double the fresh rate: roughly $12 at the fable rate,
against well under a dollar for the same turn served warm.

Apportioned as above, the cold turns are $1,068 of the development manager's
$2,502 and $549 of the product manager's $1,207 as recorded (the totals here
are the recorded ones and carry the overstatement described next, so they are
upper bounds; the per-turn figures are from usage and are not). A sweep that
fired inside the hour — every 50 minutes, say — would keep the transcript warm
and pay a read where it now pays a write. That is a line in the project's
configuration, which a developer run may not touch, so it is named for the
product manager rather than changed.

## Found on the way: since Claude Code 2.1.278, a resumed session's `total_cost_usd` is the session's running total

Every terminal of a resumed invocation recorded after 2026-09-19T20:02Z carries
in `total_cost_usd` the session's cumulative cost rather than the invocation's
own. The development manager's session `fc892f4e` reads, turn by turn on
2026-09-19/20: $15.81, $16.09, $16.25, $16.43, $16.71, $28.36, $28.52, … $66.45,
$66.64, $66.84, $67.11 — each cold turn adding the $11.6 its 580,000-token
write costs at the fable rate and each warm turn adding the $0.16–0.28 its
read and output cost, on top of everything before it. The developer's repair
attempts show the same signature from the same moment: every resumed developer
terminal to 2026-09-19T18:23Z prices to its own usage, and every one from
2026-09-19T20:02Z on prices to the previous terminal's total plus its own
usage. Claude Code 2.1.278 was installed on this machine at 2026-09-19T18:22Z.

The CLI's own schema describes `total_cost_usd` this way: "Cumulative estimated
cost in USD for this query() call … a resumed or forked session continues from
the total its transcript saved, when it has one (so the first result already
carries the earlier turns) … An estimate, not a billing statement." Sessions
whose transcripts the older CLI wrote had no saved total, which is why the
first resumed turn after the upgrade was still priced alone and every one after
it was not.

The harness records that field as the invocation's cost — in the run's terminal,
in the spend log's `amount_usd`, and so in `yoyo status --spend`, `yoyo cost`,
the dashboard, the account pool's spend-by-account, and this document's role
table. Since the upgrade the records overstate every resumed invocation: on
2026-09-19 the conversation turns are recorded at $469 where their usage prices
to $251, on 2026-09-20 at $890 against $58; the developer's resumed attempts at
$998 against $808 and $750 against $393. The overstatement grows with every
turn a long session takes, since each turn now records the whole session again.
Nothing recorded before 2026-09-19T20:02Z is affected, and the reviewer, which
never resumes, is not affected at all — which is why the review figures in this
document stand and the conversation figures are marked.

This is a pricing field misread, one of the four causes the item named, and it
is not fixed here: the fix is a change to what the adapter records for a resumed
invocation (the session's running total beside the invocation's own share of
it, differenced from the last total the harness recorded for that session), a
schema version for the terminal that carries it, and a correction of two days
of records, none of which is this item's. It is reported to the operator and
named for the product manager.
