# Self-work review, case 6: the wording the factory shows to people

One of seven case studies of work the factory did on itself, made at the operator's request of 2026-11-21: "Can you do a deep dive into a few of the factory flow items-- what caused them, how many beads did it take to address the issue, was it really fixed? Was is a real issue in the first place?", written up to "critically consider the possibility that we are wasting time with some or many of these issues". The summary of all seven is `yoyodyne-self-work-0.md` beside this file.

- **Read on:** 2026-11-21, between 18:44 and 19:00 PST.
- **Read by:** a read-only investigator on Opus 5.5 in the operator's assistant's session, told to be a sceptical auditor. It read the tracker export, git history and the factory's own logs and run records, and ran nothing.
- **Checked by hand:** the operator's assistant checked the constant at internal/chat/correction.go:34 that admits every correction for a broken standing goal at priority 0. Every other figure is the investigator's own.
- **How to use it:** evidence for the Lead Product Manager and the architect, not a ruling. The share of effort with lasting value and the verdict are the investigator's judgement. Costs are estimates for developer runs and reviews at mean prices, and leave out role conversations.

The investigator's summary of its findings:

```
thread: The wording the factory shows to people (vocabulary items and the word check)
items_total: 61
items_landed: 41
items_retired_undone: 2
items_open: 18
first_item: 2026-08-20
latest_item: 2026-11-21
runs: 39 developer runs that did work (plus 82 usage-limit cancellations of under a minute each); 115 reviews; 30 repair rounds
estimated_cost_usd: 256 direct (estimate); 300-450 with the side costs counted below
fixed: partly
real_problem: partly
share_of_effort_with_lasting_value: 30-45%
verdict: partly wasted
confidence: medium
```

## In short

- The operator really did complain about jargon, about six times between Aug 20 and Nov 8. The 25 items that come from those complaints mostly worked. The words he named are gone from printed text, except "gate", which is only partly gone.
- The other 36 items (59%) are the factory policing its own words. In 27 of them an audit, a reviewer or a role found a word it preferred to change, and none of those 27 records a person misreading anything. The other 9 fix or extend the word check.
- The code admits every finding from the Lead Product Manager's sweep audit at priority 0 (`internal/chat/correction.go:34`). That is why nine one-line rewordings went to the front of the queue at P0, and the rule is still in the code although the operator's Nov 15 rule says it should not be.
- The word check (about 5,000 lines in `internal/terms` and `scripts/vocabulary`) failed the harness's checks on 36 runs, 13% of all runs with a failing Go test package. In 25 of those it was the only failing package. 13 of the 36 were design publications refused outright (Oct 7-21), and fixing the check took three P0 items.
- Much of the "plain-language" tag is not about words at all. Of the 86 items tagged `[plain-language]`, only 17 are about vocabulary. About 60 are status lines that say something false or leave out a cause. Those are real bugs filed under a wording goal.

## What went wrong

A person reading what the factory printed met its own private words: "in force", "whose-move", "posture", "environmental stop", "idle bound", "stall continuation", "closed-list rule", "gate", raw provider keys like `seven_day_sonnet`, refusal codes, and times in UTC. The evidence that a person was actually affected:

- **Aug 20 and Sep 5**: Slack messages showed ids in place of titles. The operator raised it himself, and "met this renderer in person today" (yoyodyne-ifd.68.15, yoyodyne-ifd.68.26).
- **Aug 31**: the operator added the plain-language clause to the legibility goal. Every later item cites it.
- **Sep 19 to 25**: he objected to "in force", "whose-move" and "posture" (yoyodyne-ifd.418: "The operator asked for plain words by name a month ago"; yoyodyne-ifd.437.6: "the term is unclear to him").
- **Sep 26**: he asked that every time shown to a person be in local time (yoyodyne-ifd.432.15).
- **Sep 27**: "the operator's readability defect". A program manager's report reached him in harness categories ("Environmental stop, idle bound, stall continuation" in one sentence, with nothing saying what they meant). This one complaint produced seven items (yoyodyne-ifd.430.23, yoyodyne-ifd.437.16 to yoyodyne-ifd.437.22). The same day he directed that the Lead Product Manager's sweep audit closed work against the "standing goals" and "submit a p0 item to correct that defect" (yoyodyne-ifd.430.25).
- **Oct 11**: his goal identifiers were blocked by the word check itself (yoyodyne-hch, P0, open 10 days).
- **Nov 8, 7:10 PM PST** (interventions.jsonl): "gate". the operator: "Can we get rid of this term? It's unhelpful." This became an operating rule and five items.

After Sep 27 the stream changes character. Most new items come from the sweep audit ("the landed change breaks the plain-language standing goal"), from reviewers' minor findings, or from developers' notes, not from the operator. None of them records a person misreading a line.

This thread does not appear in `factory-stalls.jsonl` or `watch.jsonl` at all. It never stopped the scheduler, with one exception: the word check refused design documents (below).

## What caused it

There are two causes, and the factory produced both.

1. **The factory coins the words itself.** The ifd.206 sweep (Aug 31) found that "the product manager's own admissions supplied much of it". The inventory (yoyodyne-ifd.437.18, Sep 28) counted 55 coined terms, ten stop-cause words and 333 compounds. "Closed list" is a clear case. The ownership work (yoyodyne-ifd.432.25) put "closed-list reason" in its own title on Sep 29. yoyodyne-ifd.432.25.9 then printed "recorded before the closed-list rule" on status. yoyodyne-ifd.432.25.16 (P0) removed it from one line, and yoyodyne-ifd.432.25.17 was filed for the two lines it missed. Those two lines are still there today (`internal/readmodel/attention.go:977`, `internal/chat/exchange.go:318`). The diagnosis of ifd.206 calls its own counts "a floor", the same figure of speech a P0 item removed from status on Nov 6 (yoyodyne-ifd.428.126.6.7).
2. **The response was enforcement, and the enforcement itself became a source of work.** The design was: a register of permitted terms (`docs/terms.md`, 423 lines), a check that refuses unregistered words and hyphenated compounds, a sweep audit that files P0 corrections, and reviewers who report wording as findings. Each part made new items:
   - Holes in the check: yoyodyne-ifd.220, yoyodyne-ifd.437.8, yoyodyne-ifd.437.23, yoyodyne-ifd.437.28, yoyodyne-ifd.437.40, yoyodyne-lz0r.
   - False refusals by the check: yoyodyne-hch, yoyodyne-ifd.437.45, yoyodyne-ifd.437.48, all P0.
   - Persona text describing the check: yoyodyne-ifd.437.41.
   - P0 corrections from the audit: nine items. `correctionPriority = 0` was written on Oct 8 (commit 2fcd737eb, yoyodyne-ifd.430.25) from the operator's Sep 27 words.

## The chain of items

Origin: **P** = the operator's own complaint or direction; **D** = an item carrying out one of his directions; **C** = the word check itself; **F** = found by an audit, reviewer, developer or role, with no person recorded as misreading anything. "Runs" counts developer runs that did work. Reviews and repairs are totals across those runs. Items owned by the architect or the Lead Product Manager show 0 runs because they land through conversations and publication runs, which I could not tie to the item.

| Created | Item | Origin | Outcome | P | Runs | Reviews | Repairs |
|---|---|---|---|---|---|---|---|
| 08-20 | ifd.68.15 Slack speaks in titles, not ids | P | landed 09-04 | 2 | 1 | 3 | 2 |
| 08-31 | ifd.206 sweep coined terms | D | landed 08-31 | 2 | 1 | 3 | 2 |
| 08-31 | ifd.216 docs/terms.md register | D | landed 08-31 | 2 | 1 | 4 | 2 |
| 08-31 | ifd.220 check matches variants | C | landed 08-31 | 3 | 1 | 1 | 0 |
| 09-05 | ifd.68.26 acknowledgments without ids | P | landed 09-05 | 2 | 1 | 3 | 0 |
| 09-13 | ifd.209.29 'closed' wording sweep | F | landed 09-13 | 3 | 1 | 3 | 1 |
| 09-13 | ifd.360 re-arm/rearm agree | F | landed 09-24 | 3 | 1 | 3 | 0 |
| 09-13 | ifd.363 reporting.md pronouns | F | landed 10-10 | 3 | 2 | 5 | 2 |
| 09-19 | ifd.418 'in force', 'whose-move' gone | P | landed 09-22 | 3 | 1 | 4 | 1 |
| 09-19 | ifd.426 define 'seat' | D | landed 09-21 | 3 | 1 | 2 | 0 |
| 09-22 | ifd.437.2 architect: 'in force' in docs | D | landed 09-30 | 3 | 0 | 0 | 0 |
| 09-24 | ifd.437.4 register 'program manager' | F | landed 09-25 | 1 | 1 | 1 | 0 |
| 09-25 | ifd.437.6 retire 'posture' | P | landed 09-26 | 2 | 1 | 2 | 0 |
| 09-25 | ifd.437.7 architect: 'posture' in designs | D | landed 09-26 | 2 | 0 | 0 | 0 |
| 09-26 | ifd.432.15 every time in local zone | P | open | 1 | 0 | 0 | 0 |
| 09-26 | ifd.437.10 designs say Lead Product Manager | P | landed 10-15 | 1 | 0 | 0 | 0 |
| 09-26 | ifd.437.11 goals say Lead Product Manager | P | open | 3 | 0 | 0 | 0 |
| 09-26 | ifd.437.12 'cadence' out of a design | F | landed 09-30 | 2 | 0 | 0 | 0 |
| 09-26 | ifd.437.8 check reads backend/config | C | retired 09-30 | 3 | 0 | 0 | 0 |
| 09-27 | ifd.430.23 role guidance: ordinary words | P | landed 10-07 | 1 | 1 | 3 | 0 |
| 09-27 | ifd.437.16 role output checked against register | P | landed 10-03 | 1 | 1 | 3 | 2 |
| 09-27 | ifd.437.17 three terms replaced | P | landed 09-28 | 1 | 1 | 6 | 3 |
| 09-27 | ifd.437.18 inventory of coined words | D | landed 09-28 | 1 | 1 | 2 | 0 |
| 09-27 | ifd.437.19 replace in strings/personas | D | open (part landed #1728) | 2 | 2 | 4 | 2 |
| 09-27 | ifd.437.20 replace in guides | D | open | 2 | 0 | 0 | 0 |
| 09-27 | ifd.437.21 replace in governed docs | D | landed 11-21 | 2 | 0 | 0 | 0 |
| 09-27 | ifd.437.22 check catches new coinages | D | landed 10-06 | 1 | 2 | 5 | 2 |
| 09-28 | ifd.437.23 check reads three more packages | C | landed 10-11 | 2 | 1 | 3 | 1 |
| 09-30 | ifd.437.28 check misfires inside a word | C | open | 3 | 0 | 0 | 0 |
| 10-06 | ifd.437.40 compound rule hole | C | open | 3 | 0 | 0 | 0 |
| 10-06 | ifd.437.41 reviewer persona on the check | C | open | 3 | 0 | 0 | 0 |
| 10-11 | 7my role-written documents word-checked | P | landed 10-13 | 0 | 2 | 7 | 5 |
| 10-11 | hch check blocks goal identifiers | C (operator's complaint about the check) | landed 10-21 | 0 | 1 | 3 | 1 |
| 10-13 | ifd.437.45 check fails a doc that removes a word | C | landed 10-14 | 0 | 1 | 4 | 0 |
| 10-14 | ifd.428.98 audit: generated text | F | landed 10-15 | 0 | 1 | 3 | 1 |
| 10-14 | ifd.433.39 queue survey line | F | open | 3 | 0 | 0 | 0 |
| 10-14 | ifd.437.48 check fails a doc that drops a term | C | landed 10-22 | 0 | 1 | 2 | 0 |
| 10-18 | ifd.430.64 audit: post-mortem cause names | F | landed 10-18 | 0 | 1 | 3 | 0 |
| 10-21 | ifd.432.42 audit: 'after DORA' | F | landed 10-21 | 0 | 1 | 3 | 0 |
| 10-22 | f0zf audit: `seven_day_sonnet` | F | landed 10-23 | 0 | 1 | 5 | 0 |
| 10-22 | ifd.432.43 same, status line | F | retired 10-23 (duplicate of f0zf) | 2 | 0 | 0 | 0 |
| 10-23 | ifd.432.25.16 audit: 'closed-list rule' | F | landed 10-23 | 0 | 1 | 3 | 0 |
| 10-23 | ifd.432.25.17 two more 'closed list' lines | F | open, 0 runs in 29 days | 2 | 0 | 0 | 0 |
| 10-24 | ifd.432.45 dashboard token refusal wording | P | landed 11-05 | 1 | 1 | 7 | 1 |
| 10-25 | ifd.437.64 'the operator', never 'he' | F | open | 2 | 0 | 0 | 0 |
| 10-27 | ifd.428.126.4.3 cleanup failures name titles | F | open | 3 | 0 | 0 | 0 |
| 10-31 | ifd.434.37 bind-address wording | F | open | 3 | 0 | 0 | 0 |
| 11-03 | ifd.428.126.2.6 audit: slot-move time in UTC | F | landed 11-03 | 0 | 1 | 4 | 1 |
| 11-03 | ifd.435.37 audit: reset time in UTC | F | landed 11-03 | 0 | 1 | 2 | 0 |
| 11-03 | ifd.435.38 pause lines in UTC | F | open | 2 | 0 | 0 | 0 |
| 11-05 | fe22 two dashboard messages | F | open | 2 | 0 | 0 | 0 |
| 11-06 | ifd.428.126.6.7 audit: 'floor' to 'at least' | F | landed 11-06 | 0 | 1 | 5 | 0 |
| 11-09 | v3cy 'gate' out of the brief | P | landed 11-09 | 2 | 0 | 0 | 0 |
| 11-09 | av7p 'gate' out of the rest (v3cy closed early) | D | landed 11-10 | 2 | 0 | 0 | 0 |
| 11-10 | ifd.435.61 MCP refusal codes | F | landed 11-18 | 2 | 2 | 3 | 1 |
| 11-12 | ifd.435.65 audit: refusal codes, round line | F | landed 11-15 | 0 | 1 | 1 | 0 |
| 11-12 | lz0r 'gate' in packages the check skips | D | open | 2 | 0 | 0 | 0 |
| 11-16 | ifd.437.67 designs: old check name | F | landed 11-21 | 2 | 0 | 0 | 0 |
| 11-17 | ifd.437.68 'gate' in workflow comments | F | open | 3 | 0 | 0 | 0 |
| 11-18 | ifd.436.11 plain wording of every reason | F | open | 2 | 0 | 0 | 0 |
| 11-21 | ifd.436.25 release step uses 'gate' | F | open | 2 | 0 | 0 | 0 |

Totals: 61 items. 13 P, 12 D, 9 C, 27 F. 41 landed, 2 retired undone, 18 open. 13 were admitted at P0.

Items that exist only because an earlier item did not finish the job, or because the thread's own machinery produced them:

- yoyodyne-ifd.432.25.17 (yoyodyne-ifd.432.25.16 fixed one line of three).
- yoyodyne-av7p (yoyodyne-v3cy closed when one of its three documents landed).
- yoyodyne-lz0r, yoyodyne-ifd.437.68 and yoyodyne-ifd.436.25 (the Nov 8 'gate' rule was not carried through).
- yoyodyne-ifd.435.65 (an audit correction for a sibling of yoyodyne-ifd.435.61).
- yoyodyne-ifd.432.43 (a duplicate of yoyodyne-f0zf).
- yoyodyne-ifd.435.38 (two lines missed by yoyodyne-ifd.435.37).
- Every C item (the check's own holes and false refusals).

Together that is 17 of 61 (28%).

What I left out on purpose: the 432.25 family is mostly ownership routing, not wording. Only .16 and .17 belong here. I also left out about 60 `[plain-language]`-tagged items that fix false or missing information. Examples: yoyodyne-ndmc (status counted queued approved runs as failed), yoyodyne-ifd.436.24 (the dashboard counted more developers than slots), yoyodyne-ifd.432.14 (four entries on the operator's list that were not his), yoyodyne-ifd.429.162 (a stopped queue read as "nothing"). Those are accuracy bugs and should be judged as such.

## Was it fixed

I counted string literals in non-test Go code outside `internal/terms`, on main today (2026-11-21):

- The words the operator named are gone. "posture", "whose move", "environmental stop", "idle bound" and "stall continuation" have 0 occurrences. "in force" has 3 left, in `internal/intakerules/rules.go`. Those items landed and held.
- **"gate"** (his Nov 8 request): 53 literals remain. Some are the `yoyo gate` command and its help, but some are messages, for example `internal/releasecut/releasecut.go`, 9. yoyodyne-ifd.437.19 landed one part (PR #1728). yoyodyne-lz0r, yoyodyne-ifd.437.68 and yoyodyne-ifd.436.25 are open. Partly fixed.
- **"closed list"**: 12 literals remain, including both lines yoyodyne-ifd.432.25.17 names. That item has had no run in 29 days, and the Lead Product Manager lowered it to P2 on Nov 15 as "wording only; stops nothing". Nothing in the records shows anyone harmed by it in that month.
- **The general goal is not fixed and cannot be.** New wording items arrived on Nov 16, 17, 18 and 21. Each new feature brings new words, and a reviewer or the audit files them as new items. yoyodyne-ifd.437.19, the item meant to carry the bulk of the replacement, is open after 55 days. Its notes run to 104 KB in 77 entries, at least 26 of them timed-wait renewals saying "nothing changed. Renewing."

## Was it real

**Observed, reached the operator (25 items: the 13 P and 12 D).** These are real. The operator was misled or annoyed by specific words more than once, said so, and the fixes for the words he named held. The Sep 27 report in harness vocabulary is a fair complaint: the program manager exists to save his attention, and a report he cannot read without looking things up defeats that purpose.

**The check's own costs (9 C items).** These are real problems, but the thread created them. Three were P0 because the check stopped work:
- yoyodyne-hch: the operator's approved goal identifiers were refused for 10 days.
- yoyodyne-ifd.437.45: the check failed "every architect document that removes a coined word", holding a P0 design reconciliation.
- yoyodyne-ifd.437.48: the same kind of false refusal, fixed Oct 22.

In the run records, 13 design publication runs failed at verification between Oct 7 and Oct 21 with `internal/terms` among the failures. Examples are "The artifact contract", "Stopped-run recovery", "Checks on CI", "Tools" and "Who owns what". That is the one place this thread stopped the factory's work.

**Something that might happen, or a preference (27 F items).** None records a person misreading the line. The admission reasons read "breaks the plain-language standing goal", "a term of art", "the reviewer found the forbidden word in comments". Some cases are defensible on their face, such as a raw key `seven_day_sonnet` or a refusal code on an operator line. About five F items apply rules the operator stated explicitly: local time (three items) and 'gate' (two). The rest apply a reviewer's or auditor's taste. "Floor" to "at least" was P0. A design calling a check by its old name took an architect item (yoyodyne-ifd.437.67). The pronoun item (yoyodyne-ifd.437.64) has been open since Oct 25.

## Where the effort was wasted

- **P0 for wording.** Nine audit corrections went to the front of the queue at P0 on Oct 14, 18, 21, 22 and 23 and Nov 3 (twice), 6 and 12. The code forces that priority (`correctionPriority = 0`; `correctionProblems` refuses any other value). Add 7my, hch, 437.45 and 437.48, and the thread had 13 P0s, in October and November, when the factory had real stalls. The operator's Nov 15 rule says a bug is P0 only if it "materially degrades usability" or costs 25% or more. No rewording meets that, yet `internal/chat/correction.go:34` still says 0, and no open item changes it.
- **Checking words by machine generated more work than it saved.** Across the whole factory, the word check failed 36 runs (Sep 28 to Nov 19). 23 of those were developer runs on unrelated work, such as provider stall fixes, store cleanup and merge-queue confirmation. Each failure forced a repair round. The usual cause was a new hyphenated compound (`TestThisRepositoryCoinsNoCompoundNothingDefines`, 27 failures). Twenty-five commits in unrelated items had to touch `docs/terms.md` or the inventory just to get past the check. Then 9 more items went to fixing the check itself.
- **Fixed one line, filed another item for the rest.** yoyodyne-ifd.432.25.16 was a P0 for one sentence. Its developer and reviewer filed four separate reports about the two other lines with the same words, which the Lead Product Manager folded into yoyodyne-ifd.432.25.17. That item has not run since. yoyodyne-v3cy closed early and needed yoyodyne-av7p.
- **Coining words, then scrubbing them out.** Roles kept inventing terms ("closed-list", "floor", "rung", "seat") while other items removed them.
- **Cost.** 39 working developer runs at $3.83 and 115 reviews at $0.93 come to about **$256** (estimate). The side costs are not priced: the repair rounds the check forced on 23 other runs, 13 failed publication runs, and the Lead Product Manager and architect turns spent on wording, including 26 or more renewal notes on 437.19. A fair range is **$300-450**. The bigger cost is the queue slots: 13 P0 slots.
- **Lasting value, my estimate: 30-45%.** That covers the operator-originated items (about 40% of items, but under a third of the runs), plus a few F items that removed raw keys and codes from operator lines. Most C and F work either fixed the thread's own machinery or changed words nobody had misread.

## What would have been better

1. **Treat the operator's complaints as the trigger, not the goal clause.** When he names a word, retire it everywhere in one item and register it as banned. That is what yoyodyne-ifd.418 and yoyodyne-ifd.437.6 did, and it worked. Do not let an audit turn a goal clause into P0 items.
2. **Make standing-goal corrections P2 and batch them**, as one wording sweep per week or two, not one P0 item per finding. Set `correctionPriority` to follow the Nov 15 P0 rule. This is a one-line change, and it is the most important fix this thread points to.
3. **Make reviewers fix wording themselves.** A minor wording finding should be fixed in the same change or dropped, not filed as a new item. That would have removed about 15 of the 27 F items.
4. **Keep the check narrow.** Ban the specific words the operator rejected. Drop the rule that every new hyphenated compound must be registered. That rule caused most of the 36 check failures and the three P0 false refusals.
5. **Keep accuracy bugs separate from vocabulary.** A status line that says something false is a correctness bug and should not be tagged as wording. Mixing them makes the real ones look like polish and makes the polish look important.

**If the operator stopped all vocabulary work for a month:** he would lose the leftover 'gate' cleanup, which he did ask for, and new coinages would build up in new lines until the next sweep. He would lose little else. "Closed list" has already sat for a month with no recorded harm. He would gain about 4-10 developer slots a month (the thread's recent rate) and fewer failed checks on unrelated work. He should **not** stop the accuracy bugs that carry the same tag, because those are where people were actually misled.

## What I could not establish

- Run costs per item. I used the brief's means. Publication runs and conversation turns for architect and Lead Product Manager items were not tied to items or priced.
- How much check time the 36 failures with `internal/terms` among them cost in minutes. I counted the failures but did not measure the length of each check stage.
- Whether the operator ever saw the lines the F items changed. No intervention record mentions any of them.
- The exact text of operator messages before Oct 8, when `interventions.jsonl` starts. For those I relied on item notes quoting him.
- Whether the factory admitted any standing-goal corrections after Nov 12. The last one in the tracker is Nov 12, but I could not tell whether the audit stopped finding them or stopped filing them.
- The classification into P, D, C and F is mine, made from the admission reasons. A few borderline items, such as yoyodyne-ifd.432.45, which is partly a feature, could move between groups without changing the conclusion.
