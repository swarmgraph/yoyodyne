# Self-work review, case 4: the merge queue: hand-backs, catch-up and order

One of seven case studies of work the factory did on itself, made at the operator's request of 2026-11-21: "Can you do a deep dive into a few of the factory flow items-- what caused them, how many beads did it take to address the issue, was it really fixed? Was is a real issue in the first place?", written up to "critically consider the possibility that we are wasting time with some or many of these issues". The summary of all seven is `yoyodyne-self-work-0.md` beside this file.

- **Read on:** 2026-11-21, between 18:44 and 19:00 PST.
- **Read by:** a read-only investigator on Opus 5.5 in the operator's assistant's session, told to be a sceptical auditor. It read the tracker export, git history and the factory's own logs and run records, and ran nothing.
- **Checked by hand:** the operator's assistant checked the rate of code items landed per day before and after the queue was switched on, from git. Every other figure is the investigator's own.
- **How to use it:** evidence for the Lead Product Manager and the architect, not a ruling. The share of effort with lasting value and the verdict are the investigator's judgement. Costs are estimates for developer runs and reviews at mean prices, and leave out role conversations.

The investigator's summary of its findings:

```
thread: The merge queue - hand-backs, catch-up and order
items_total: 119
items_landed: 66
items_retired_undone: 10
items_open: 43
first_item: 2026-09-25
latest_item: 2026-11-21
runs: 82 developer runs on the thread's own items (run records); about 94 more developer runs caused by conflict hand-backs
estimated_cost_usd: 430 for the thread's own items; 950-1,000 counting the re-runs its conflict hand-backs caused (estimate)
fixed: partly
real_problem: yes
share_of_effort_with_lasting_value: 35-50%
verdict: partly wasted
confidence: medium
```

## In short

- The harness's own merge queue was switched on at 09:06 PST on November 8. For the next ten days it landed fewer code changes per day than the setup it replaced, stopped several times, and was bypassed by about 48 hand merges. Fourteen of the fixes to the queue itself landed around it.
- Since November 18 it has landed about 28 distinct code items a day, more than any earlier period. That came from one decision: stop re-running GitHub's checks on this machine. That option existed before the switch.
- It has not removed the problem it was built for. Before, 11% of runs stopped because main moved. Now 26% of code admissions to the queue are handed back for a conflict with a moved main, after hours of waiting.
- 119 items name the queue. 43 are still open. Order rules have been redesigned four times and are still wrong today: on November 21, seven documents landed past an approved code change that had waited since 15:17 PST.
- Verdict: the queue works and is now fast. But much of the effort since November 8 went into problems that came from running checks locally and from holding changes for hours.

## What went wrong

**Why it was built.** On September 25 the operator's direction (in `yoyodyne-ifd.429.22`) was that "14 of 123 runs ended because main moved between their checks and their merge". A queue would test each change against the main it lands on. The operator ruled that it be one switch that turns the queue off. The build ran from October 5 to 17 (`yoyodyne-ifd.429.59` and children `.59.1` to `.59.6.2`). It was switched on in this repository on November 8 (`yoyodyne-ifd.429.59.7`), after the operator's assistant removed GitHub's own "Require merge queue" rule. Until then the factory had landed through GitHub's merge queue, with the harness re-running the checks locally ("replay"). That ruleset was active by October 21 at the latest.

**What someone watching saw.** All figures below come from git (first-parent merges to main, Pacific days) and from the interventions record.

| Period | How changes landed | Code landings/day | Distinct code items/day | Document landings/day |
|---|---|---|---|---|
| Oct 1-14 | GitHub queue / plain PR merges | 15.1 | ~14 | 2.3 |
| Oct 15 - Nov 7 | GitHub merge queue + local replay | 8.6 | not split | 5.3 |
| Nov 8-17 | Harness queue, full checks run locally | 11.4, of which 3.8 were hand merges outside the queue | 8.2 | 2.4 |
| Nov 18-21 | Harness queue, GitHub's checks accepted, race check off the queue | 30.8 | ~28 | 29.2 |

- **Nov 8-17: slower, and stopping.** The operator's assistant wrote on November 11 that "the merge queue is landing about one change an hour, and about one change an hour is joining it." At about 81 minutes per code candidate, half of it the race check, it was the narrowest point in the factory (`docs/designs/merge-queue-pace.md`). The queue stopped and needed a person on Nov 10 (twice), Nov 11 (several times) and Nov 12, when it sat more than two hours on entry 49. Twice a run record was edited by hand to get it moving (interventions, Nov 10 22:48 and Nov 11). One stuck entry wrote the same hand-back note 217 times on `yoyodyne-fo4v`, and another 129 times on `yoyodyne-onxb`. Developer slots were cut from four to three (`yoyodyne-cfv6`, Nov 11) and then to two (operator configuration, Nov 15) "until the merge queue keeps up". They went back to three on November 19 (`yoyodyne-nn0z`). By November 16, about 50 approved entries had waited two days or more, and about 10 a day were handed back (`yoyodyne-ifd.429.199`).
- **The bypass.** The interventions record has 105 entries naming the merge queue from November 8 to 22, and 29 of them are hand merges. Most of those came on Nov 16 (6) and Nov 17 (12), "ahead of 46 queue entries" and "47th of 47". Git shows about 48 merges to main outside the queue in that period.
- **Hand-backs.** Item notes record 357 admissions of code changes across 221 items. Queue entry numbers reached about 507 once documents are counted. The notes record 94 hand-backs because the approved change no longer merged onto a moved main, 15 for a check failing on the combined copy, 13 for the candidate's reviewer asking for repair, and 24 by hand. A hand-back for a conflict usually came after five to ten hours of waiting (`yoyodyne-ifd.429.255`, `yoyodyne-ifd.436.15`). Example: `yoyodyne-yrku` (taking up runs stopped by GitHub refusing the harness's key) was admitted to the queue six times between November 4 and 21. It was handed back four times for conflicts, took seven runs, and landed on November 21.
- **Order.** On November 20, from midnight to 13:10 PST, ordinary code waited a median of 7.9 hours while documents waited 0.8 hours (`yoyodyne-ifd.429.255`). On November 21 the release-step fix (`yoyodyne-ifd.436.15`) entered the queue for the third time, as entry 499, at 15:17 PST. Up to 18:35 PST git shows nine landings past it: seven documents and two code changes. Six of the documents landed in a row, from 15:59 to 17:49. Its first admission had already been handed back for a conflict after about ten hours, with no catch-up attempt because "the run's worktree is gone".
- **Documents.** From November 18 to 21 there were 117 document landings but only 44 distinct documents. The "Yoyodyne V1 Harness Design" landed four times between 07:12 and 11:40 on November 21. The queue's own design, `docs/designs/integration-through-a-merge-queue.md`, has 12 revisions and has landed 9 times since November 8.

The scheduler's watch log (`watch.jsonl`) does not record the merge queue's waits directly. It does show the knock-on effect. "Slot still counted as taken" entries began on November 16 (11 to 27 a day through November 21) while runs sat waiting in the queue (`yoyodyne-z5f9`, `yoyodyne-vjbp`).

## What caused it

The queue itself does what its design says. The trouble came from four design choices, all made in the factory's own earlier work:

1. **It re-ran on this machine the checks GitHub already ran.** The design (`yoyodyne-ifd.429.22`, Oct 5) checks the exact candidate with the configured checks. This repository took that to mean running `make race` and `make test` locally, one candidate at a time, beside developer runs. GitHub's CI was already running the full check on every pull request (median 19 minutes). The pace design (`yoyodyne-ifd.429.132`, Nov 9) and the architect's three-way choice (`yoyodyne-ifd.429.199`, Nov 16) both arrived after the switch. The fix that worked, accepting GitHub's required checks (`.185`, `.186`) and taking the race check off the queue (`.212`), landed by hand on November 18.
2. **One change per candidate, so waits run to hours, and hours of waiting become conflicts.** The more slowly the queue lands, the further main moves under each waiting entry. The queue found a conflict only when the entry reached the head of the line (`yoyodyne-ifd.429.229`, still open). That is why the conflict rate (26% of admissions) is higher than the "main moved" rate the queue was built to remove (11% of runs in September).
3. **A hand-back threw the work away.** The earlier free catch-up (`yoyodyne-ifd.429.96`, Oct 30) did not cover the harness's queue (`yoyodyne-3v2x`). When the catch-up was built (`.174`), it skipped every entry whose checkout had been cleaned up, and long-waiting entries are exactly those (`yoyodyne-1tf8`). The notes record 29 hand-backs refused for that reason and only 7 that got the attempt.
4. **Every document revision is its own landing.** Document-writing roles revise often. Each revision takes a whole candidate, a review and about 20 minutes of queue time. An age-and-priority order rule (`.154` and `.183`) then lets documents and priority 0 code take most of the slots.

## The chain of items

119 items mention the merge queue in their title, or exist only because of it. Of those, 26 date from before the switch and 93 from after it. The table lists the 50 that carry the story, in order. "By hand" means the change landed outside the queue. Runs and review rounds come from the run records.

| Item | Date filed | Raised by, why | Outcome | Runs / reviews |
|---|---|---|---|---|
| ifd.429.22 queue design | Sep 25 | Operator: 11% of runs lost the race to main | Design Oct 5 | 0 |
| ifd.429.59 + 8 build children | Oct 5 | Lead PM, against the design | Landed Oct 6-17 | ~10 / 25 |
| ifd.429.59.7 switch on | Oct 20 | Lead PM | Landed Nov 8 | 2 / 1 |
| fo4v queued change wrongly stopped | Nov 8 | Program manager, observed | Landed Nov 12, merged by hand by operator | 4 / 6 |
| ifd.429.132 pace design | Nov 9 | Operator asked why one change an hour | Design Nov 10 | 0 |
| ifd.429.142 declared checks | Nov 10 | From .132 | Landed Nov 16, by hand | 1 / 1 |
| ifd.429.143 checks and review side by side | Nov 10 | From .132 | **Blocked**, 6 repairs | 3 / 1 |
| ifd.429.148 restart-cut push | Nov 10 | Queue stopped | Landed Nov 10, by hand by operator | 1 / 2 |
| 3v2x catch-up for conflict hand-back | Nov 10 | Program manager, observed | **Retired**, duplicate of .174 | 0 |
| ifd.429.150 refused hand-back stops queue | Nov 10 | Queue stopped | Landed Nov 15 | 2 / 2 |
| ifd.429.153 check one ahead | Nov 11 | From .132 | Landed Nov 16 | 2 / 3 |
| ifd.429.154 order design | Nov 11 | Operator: P0 ahead of documents | Design Nov 12-15 | 0 |
| cfv6 / nn0z slots to three, then back | Nov 11 | Queue too slow | Done Nov 13 / Nov 19 | 1 / 1 |
| ifd.429.155 queued item not started fresh | Nov 11 | Observed | Landed Nov 16 | 2 / 2 |
| ifd.428.99.23 resume a hand-back from its kept commit | Nov 12 | Observed (a $10.70 change rebuilt) | **Open** | 0 |
| ifd.429.160 GitHub check stands as the full check | Nov 12 | Pace | Ruling Nov 15 | 0 |
| ifd.429.161 entry already on main | Nov 12 | Queue stopped | Landed Nov 13, by hand | 2 / 1 |
| ifd.429.166 release the head entry | Nov 12 | Queue fixes stuck behind the stop | Landed Nov 18 | 3 / 5 |
| ifd.429.174 catch-up after main moved | Nov 13 | Program manager: zt5n waited 45 h | Landed Nov 18, **by hand** | 3 / 3 |
| ifd.429.179 catch-up after a check failure | Nov 13 | Observed once | **Open** | 0 |
| ifd.429.181 catch-up takes the next slot | Nov 13 | Developer found a rule gap | **Retired** into 428.99.23 | 0 |
| r976 entry passed over silently | Nov 15 | Program manager, observed | Landed Nov 18 | 2 / 2 |
| ifd.429.183 order by priority and age | Nov 15 | Build of .154 | Landed Nov 16, by hand, 3 repairs | 3 / 6 |
| ifd.429.184 record the rule that chose | Nov 15 | Build of .154 | **Open**; overlapped by .252 | 0 |
| ifd.429.185 / .186 GitHub's checks as the full check | Nov 15 | Build of .160 | Landed Nov 18, **by hand** | 2 / 2 |
| ifd.429.187 confirm the merge commit's base | Nov 15 | Safety gap made by .185 | Landed Nov 20, 3 repairs | 1 / 2 |
| ifd.429.199 three ways out of the slow queue | Nov 16 | Operator | Ruling Nov 16 (option 3) | 0 |
| ifd.429.200 flaky check-ahead test | Nov 16 | Test from .153 turned main red (1cj7) | Landed Nov 17, by hand | 1 / 1 |
| ifd.429.201 check ahead the right entry | Nov 16 | .153 checked the wrong entry once order existed | **Open** | 0 |
| pnv2 hand merge ends as a stop | Nov 16 | Caused by the hand merges | Landed Nov 17, by hand | 1 / 1 |
| ifd.429.212 race check off the queue | Nov 17 | Pace | Landed Nov 18, **by hand** | 1 / 1 |
| 1tf8 catch-up when the checkout is gone | Nov 18 | Program manager: .174 skipped its first case | Landed Nov 21 14:07 | 1 / 2 |
| ifd.429.229 find conflicts when main moves | Nov 18 | From .199 | **Open** | 0 |
| ifd.429.224 / .233 candidate PR as a draft | Nov 18 | A person could merge a candidate early | Ruling Nov 18; build **open** | 0 |
| ifd.429.241 / .242 / .243 a pushed candidate when main moves | Nov 19 | Gap from relying on GitHub | Ruling Nov 19; builds **open** | 0 |
| fp3.10 no second copy of a waiting document | Nov 19 | Four copies of one design | Landed Nov 21 00:10 | 1 / 1 |
| z5f9 / vjbp slot counted while waiting | Nov 19 / 21 | Slots held by queued runs | Landed Nov 20 / **open** | 1 / 1 |
| ifd.429.252 record the choosing rule; remember priorities | Nov 20 | P1 picked ahead of P0 when the tracker was slow | Landed Nov 21, 3 repairs | 1 / 2 |
| ifd.429.255 code waits 7-9 h | Nov 20 | Operator's assistant, measured | Ruling Nov 20 | 0 |
| ifd.428.185 catch-up takes any free slot | Nov 21 | Follow-on from .181 and 428.99.23 | **In progress** | 1 / 0 |
| ifd.429.266 no two document candidates in a row; four-hour age rule | Nov 21 | Build of .255 | **Open, P0** | 0 |
| ifd.429.267 batch up to eight documents | Nov 21 | Build of .255 | **Open** | 0 |
| ifd.429.268 daily wait figures | Nov 21 | Build of .255 | **Open** | 0 |
| ifd.429.274 landed only when GitHub says merged | Nov 21 | Observed | **Open** | 0 |

The other items fall into a few groups:

- About 20 flaky-test items for the queue's own tests (`.74`, `.83`, `.200`, `.247`, `.264`, `.231`).
- Restart and build-move interactions (`.136`, `.138`, `.170`, `428.156`, `428.167`, `.225`).
- GitHub outage and cancelled-check handling (`.140`, `.151`, `.158`, `.159`, `.213`, `was2`).
- Status and display items (`ndmc`, `xcz6`, `.162`, `.191`, `.222`, `.226`, `.249`).
- Duplicate check-time counters (`428.126.6.8`, `.131`, `.59.9`, three items for one figure).

**Totals.** 119 items: 66 done (9 of them design rulings), 10 retired undone (all as duplicates or merged into another item), 43 still open, in progress or blocked. 82 developer runs and 125 review rounds are recorded on the thread's own items.

**Items that exist because an earlier one in the chain did not finish the job:**

- `1tf8` because of `.174`; then `428.185` because of `.181` and `428.99.23`.
- `.200`, `.201` and `1cj7` because of `.153`.
- `.184`, then `.252`, then `.255`, `.266` and `.267` because of `.154` and `.183`.
- `.187`, `.224`, `.233` and `.241` to `.243` because of `.185` and `.186` (relying on GitHub opened gaps of its own).
- `pnv2` and `.161` because of the hand merges.
- `fp3.10` because of `fp3.8`.

That is about 20 items.

## Was it fixed

- **Throughput: yes, from November 18.** After `.185`, `.186` and `.212` landed (all by hand, that day), landings went from about 12 a day to between 44 and 68 a day. Distinct code items went from 8.2 a day to about 28 a day. I could not separate how much of that came from the queue and how much from more approved work arriving (slots went back to three on November 19).
- **Queue stops: mostly.** After `.148`, `.150`, `.161` and `.166`, the record shows two short stops on November 20 that cleared without a person. Hand merges fell from 12 on November 17 to one on November 19.
- **Catch-up after a conflict: not shown yet.** `.174` landed on November 18 and then refused its attempt on most hand-backs. Notes record 29 refusals because the worktree was gone, including `.436.15` on November 21. `1tf8` landed at 14:07 PST on November 21, too recently to judge. Even when it works, a catch-up is still a developer session. It saves a decision and a fresh run, not the work.
- **Conflict hand-backs: no.** Nothing that finds conflicts early (`.229`) or lands documents together (`.267`) has been built. On November 20, four of the day's five hand-backs were conflicts found after 5.7 to 8.3 hours of waiting.
- **Order: no.** The four-hour age rule and the rule against two documents in a row (`.266`, P0) are open. On November 21 the queue landed six documents in a row past waiting approved code, after the architect's ruling (`.255`) said it should not.
- **Duplicate document copies: partly.** `fp3.10` landed at 00:10 PST on November 21 and stopped parallel copies. Revisions one after another still each take a landing. That day the V1 design landed four times, "Integrating to a local branch" three times and the program manager design four times.

## Was it real

Yes, the problems were observed failures, not guesses. Approved work waited days, the queue stopped with nothing able to restart it, and the operator cut developer slots and approved hand merges because of it. The operator said on November 7 that the old path was "costing a bunch of time". His assistant spent more than 38 messages and 29 hand merges on the queue in two weeks.

Split of the 50 items in the table:

- **An observed failure (about 34):** for example fo4v, .148, .150, .161, .166, .174, 1tf8, r976, .183, .185 and .186, .212, .252, .255, .266, fp3.10, z5f9, pnv2, .200, 428.99.23.
- **Something that might happen (about 16):**
  - .179: seen once.
  - .224 and .233: a person could merge a candidate early. Never seen.
  - .241 to .243: a pushed candidate landing unchecked. A gap that relying on GitHub created, not seen to cause harm.
  - .187: no wrong-base merge reported.
  - .201, .184, .268, .274, .143.
  - Most of the status and display items.

**Safety, the other half of the question: not shown.** The queue was meant to stop changes landing on a main they were not tested against. I found only one item about main being red in this period (`yoyodyne-1cj7`, November 17). Its cause was the queue's own flaky test, during the hand-merge days. I found no record that the September replay setup ever broke main. Candidate-level catches exist: 15 check failures and 13 reviewer repair verdicts. Two of the check failures I read were the queue's own flaky test (`mergequeuehold_test.go`), which handed back changes that had nothing to do with it. The reviewer verdicts I read mostly judged the change itself again ("the acceptance criteria are not fully met"), not the integration. On `.436.15` the reviewer said that "nothing else has landed on main since... the combined change is this change alone" and still sent it back. One (`jn5n`, release notes missing thirteen items that landed meanwhile) was a genuine catch that the old path would have missed.

## Where the effort was wasted

- **Ten days of tuning a local re-check that was then dropped.** `.132`, `.142`, `.143` (blocked after 6 repairs), `.153` and its follow-ons `.200` and `.201`, `.136`, `.138`, `.139`, `.170` and `twh5` all made the local checks faster or more robust. Then `.199` chose to rely on GitHub's checks instead, and the slot cuts (cfv6, nn0z) followed from the same slowness. That is roughly 15 items and 20 runs whose value mostly lapsed on November 18.
- **Order redesigned four times.** `.154` and `.183`, then `r976`, `.184` and `.252` (which overlap), then `.255` with `.266`, `.267` and `.268` (open). Each round fixed the order that the previous rule produced. The real limit was the landing rate, as `.255` itself says: "The order works as the code writes it; the rate limits it."
- **Fixes that did not cover the case they were for.** `3v2x` was retired as a duplicate of `.174`. `.174` then missed most hand-backs, which needed `1tf8`. `.181` was retired into `428.99.23`, which is still open and has now spawned `428.185`.
- **Duplicates.** Ten items were retired undone, all as duplicates or merges, including three items for one check-time figure.
- **Fixes to the queue landed around the queue.** Fourteen of them, so the queue could not carry its own repairs (hence `.166`). Each hand merge then caused hand-backs and records of its own (`pnv2`, interventions of Nov 16-18).
- **Conflict re-runs.** 94 conflict hand-backs at about $5.69 each (one developer run plus two reviews) come to about $535 of re-work, which the long waits caused. This is an estimate, and some hand-backs were later caught up.
- **Document churn.** 73 of the 117 document landings from November 18 to 21 were repeat revisions of a document already landed that day or week. Each took a queue slot, a review and about 20 minutes. The cause is upstream, in the roles that revise documents, but the queue has no batching to soak it up.

**Cost (estimate).** Using $3.83 per developer run and $0.93 per review round: about $430 for the thread's own 82 runs and 125 review rounds, plus about $535 of conflict re-runs, so about $950 to $1,000. Not counted: role conversations (architect, Lead PM, program manager), the operator's assistant's time, and the reviewer's pass on every candidate. At about 500 entries and $0.93 each, those reviews alone are about $470. That last one is a running cost of the design, not waste.

**Share with lasting value: 35 to 50%.** These items will last:

- the build;
- the switch to GitHub's checks (`.185`, `.186`, `.212`, `.187`);
- the fixes that stop the queue stopping (fo4v, `.148`, `.150`, `.161`, `.166`);
- `.155`, `fp3.10`, `z5f9`;
- probably `1tf8`.

Most of the local-pace work, the repeated order rework, the duplicates and the hand-merge side effects will not.

## What would have been better

1. **Switch on with GitHub's checks, not local ones.** GitHub was already running the full check on every pull request. If the queue had accepted those results from November 8, or the switch had waited until it could, there would have been no ten days of one landing an hour, no slot cuts, no 48 bypass merges, and most of the pace items would never have existed. This was option 3 of `.199`, and its pieces were already filed.
2. **Treat waiting time as the root cause, not hand-backs and order.** One design should have covered three things together: batching documents, finding conflicts as soon as main moves, and keeping catch-ups cheap. Instead each was found separately from the symptom it produced (`.174`, `1tf8`, `.229`, `.255`, `.267`).
3. **Stop after the second failed fix.** For order, `.183` followed by `r976` should have triggered a rate measurement rather than a third and fourth rule. For catch-up, `3v2x` and then `.174` should have triggered a test against the real case (entries whose checkout was cleared) before landing.
4. **Count what the queue catches.** Nobody measured whether candidate checks and reviews catch integration faults that the old path missed. Before spending more on safety pieces (`.233`, `.242`, `.243`), the factory should count how many hand-backs were real integration faults. From what I read, the answer is very few.
5. **Not doing it at all?** For this one repository, GitHub's merge queue with grouping was available and landed about 11 changes a day. The harness queue is a portability feature for projects without one. Building it was a fair product choice. Making it this repository's only landing path before it was fast was the costly part.

## What I could not establish

- The merge queue's own records were not available to me. Waits per entry, hand-back counts for document entries, and exact candidate times come from item notes, interventions and git only, so document hand-backs are not counted.
- How much of the November 18-21 rise in throughput is the queue and how much is more approved work arriving.
- Whether the September and October setups ever landed a change that broke main. I found no record of it, but I did not read every diagnosis.
- Run records may not hold every run. The runs count is what the records show.
- The cost of role conversations and of the per-candidate reviews is not in the run records.
- When GitHub's merge queue rule was first switched on for main. It was active by October 21.
