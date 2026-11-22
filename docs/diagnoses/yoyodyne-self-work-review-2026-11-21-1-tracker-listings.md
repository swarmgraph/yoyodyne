# Self-work review, case 1: slow tracker listings and tracker timeouts

One of seven case studies of work the factory did on itself, made at the operator's request of 2026-11-21: "Can you do a deep dive into a few of the factory flow items-- what caused them, how many beads did it take to address the issue, was it really fixed? Was is a real issue in the first place?", written up to "critically consider the possibility that we are wasting time with some or many of these issues". The summary of all seven is `yoyodyne-self-work-review-2026-11-21-0-summary.md` beside this file.

- **Read on:** 2026-11-21, between 18:44 and 19:00 PST.
- **Read by:** a read-only investigator on Opus 5.5 in the operator's assistant's session, told to be a sceptical auditor. It read the tracker export, git history and the factory's own logs and run records, and ran nothing.
- **Checked by hand:** the operator's assistant checked the weekly measurement recording "dolt is not installed" on November 5, 12 and 19; the note of October 25 raising server mode; the ticket-file change in commit 6b10540dd. Every other figure is the investigator's own.
- **How to use it:** evidence for the Lead Product Manager and the architect, not a ruling. The share of effort with lasting value and the verdict are the investigator's judgement. Costs are estimates for developer runs and reviews at mean prices, and leave out role conversations.

The investigator's summary of its findings:

```
thread: Slow tracker listings and tracker timeouts
items_total: 62
items_landed: 39
items_retired_undone: 4
items_open: 15
first_item: 2026-09-01
latest_item: 2026-11-21
runs: 75 (61 not cancelled; 153 review rounds; 49 repairs)
estimated_cost_usd: 380-450 (developer runs and reviews only; role conversations not counted)
fixed: no
real_problem: yes
share_of_effort_with_lasting_value: 25-35%
verdict: mostly wasted
confidence: medium
```

## In short

The problem is real and still getting worse. Tracker calls time out, maintenance's reconcile runs out of time, and runs and passes stall. On 2026-11-21 about half of all listings took longer than 10 seconds, and reconcile timed out 35 to 63 times a day over the last three days. Before 2026-10-28 it almost never timed out.

The cause is how `bd` opens its store when it runs without a server. Every process takes an exclusive lock on the store, reads included, and while a write journal exists each process also pays to rewrite that journal. Both facts were on record by 2026-10-14 (diagnoses for 433.20 and 6u7). Server mode, the option that removes both, was raised by the operator on 2026-10-25. Every weekly measurement since 2026-11-05 has failed to test it because `dolt` is not installed. That missing binary still blocks the question today (item 428.126.4.26).

In the meantime the factory spent about 15 items shrinking the store. That work rested on a premise the factory's own first measurement had disproved the same day. It spent more items raising timeouts, retrying and reporting. One fix put ticket files into the main checkout, and those files intermittently stopped run starts across the factory for about nine hours overnight on 2026-11-20/21 (t3qs).

## What went wrong

What someone watching would have seen, from the state files:

- **September.** The scheduler's pulls failed on `list open work items: bd list failed with status cancelled`. `watch.jsonl` records 9 such failures on 2026-09-01 and 38 on 2026-09-03, then one or two a week until early October. Write calls also timed out. The 433.20 diagnosis counted 117 timed-out tracker calls in conversation logs between 2026-09-02 and 09-30.
- **The "twelve-hour stall" of 2026-09-29/30** was blamed on the tracker, but 433.20 proved it was the laptop asleep with the lid shut (`pmset` log). This was a real case of misattribution, and the factory corrected it.
- **October.** Reads and writes failed after 30 seconds, tried three times. 6u7 found that most of the October 12–13 failures were the harness's own 5- and 10-second deadlines, not the tracker's limit. Notes written under those short deadlines were lost, for example on the passes of 2026-10-11 to item 9j7. The dashboard listed every closed item several times a minute (432.46). Decided recoveries were refused because one tracker call timed out (q8w).
- **Maintenance's reconcile.** In `supervisor.log` it timed out 0 or 1 times a day through 2026-10-27. Then it timed out 3 to 16 times a day from 10-28 to 11-08, 23 to 42 a day from 11-09 to 11-18, then 45 (11-19), 63 (11-20) and 35 (11-21, partial day).
- **Listings over their 10-second limit.** The limits step has measured this since 2026-11-04 (`maintenance: limits`). By day: 15–28% over 10 s from 11-04 to 11-08, 31–41% from 11-09 to 11-17, 28% (11-18), 18% (11-19), 22% (11-20) and 24% (11-21). Through 11-21 the slowest listing almost every hour hit the 2-minute limit, and one took 3m59s. The last reading before this write-up, at 18:46 PST on 11-21, found 48 of 97 listings over 10 s. The step began counting waiting time on 11-21, so its figures after 02:17 PST that day are not directly comparable with earlier ones. The reconcile timeouts above are comparable, and they rose.
- **Effects on work.**
  - On 2026-11-15, starts waiting on a slow tracker kept 255 ready items unstarted for 70 minutes (428.151).
  - An approved change in the merge queue was handed to a person because a note write was cancelled (ibqo).
  - The merge queue chose a priority 1 entry ahead of two priority 0 entries after its read timed out (429.252).
  - Publishing runs stopped on a 30-second write timeout (433.59).
- **It reached the operator.** 27 entries in `interventions.jsonl` mention the tracker, from 2026-10-25 to 11-21. On 2026-10-25 the operator himself asked for the store's growth to be measured and for server mode to be tried on a copy.

## What caused it

There are two parts, and both have been there since the start. Neither was introduced by factory work.

1. **One process at a time.** Without a server, `bd` takes an exclusive lock to open its Dolt store for every command, reads included. The factory runs a scheduler, the dashboard, reconcile, maintenance, `yoyo status`, four recurring role passes, runs and the merge queue, and each starts its own `bd` processes. Each call waits for every other. 433.20 recorded this on 2026-09-30.
2. **Every open pays for the journal.** Dolt writes changes to a journal file, and each `bd` process rewrites the journal, its index and the manifest when it opens the store. 6u7 measured this on 2026-10-14: a read went from 0.7 s to 0.15 s, and a listing from 3–4 s to 0.6 s, after `bd gc`. 4.24's fifth investigation measured it again on 2026-11-21 (0.12–0.29 s with no journal, 0.6–3.6 s with one). The harness writes every few minutes, so a journal almost always exists.

`bd` makes this worse because it does not queue waiters. A process that finds the store taken backs off and retries, so whoever has waited longest keeps losing (4.24, fourth investigation).

The factory then made it worse itself:
- The harness made far too many calls. On 2026-11-19 the scheduler alone made 39,502 single-item reads in 10 hours, mostly from repeated reads of parent items for the "inherited block" line and from the merge queue's choice of its next entry (4.24, first and second investigations).
- Per-call reads were raised to wait 120 s (428.126.4.4, landed 2026-11-01), so each stuck call holds its caller four times longer. 4.14 traced maintenance's two timeouts of 11-01 to the longer waits.
- Programs outside the harness (`bd --sandbox ...`) also read the store, and they appear next to most calls that hit the 2-minute limit (4.24).

Store size was not the cause of slow listings. The first measurement on an idle copy, on 2026-10-28 at 943 MB, listed open items in 0.7 s and ready items in 0.4 s. Copies at 1,140 MB (11-05) and 1,449 MB (11-12) gave the same result. When the live store was rebuilt from 1,637 MB to 15 MB on 2026-11-19, the share of listings over 10 s barely moved: 10–28% that morning, 18–41% over the following days. The store-size design written on 10-28, an hour after that first measurement, nevertheless opens by saying a listing "ran past its 30 second limit with no other process holding the store".

## The chain of items

Items are listed in order of creation. "Admitted by": LPM is the Lead Product Manager, PM the product manager in September, FF-PM the factory-flow program manager. "Runs / rounds / repairs" are summed from `runs/run-*.json`, and the run count includes cancelled runs. The last column says whether the item answered an observed failure (obs) or a possibility (might), and which earlier item's shortfall produced it.

**Phase 1: September, living with timeouts by retrying (9 items)**

| Item | What | Created | Admitted by | Outcome | Runs/rounds/repairs | Obs/might; follows from |
|---|---|---|---|---|---|---|
| ifd.232 | Watch retries transient tracker reads | 09-01 | PM | landed 09-01 | 1/2/0 | obs |
| ifd.271 | Exercise concurrent tracker access live | 09-03 | PM | **retired** 09-30 (overtaken) | 2/4/– (both failed) | might |
| ifd.327 | Timed-out write says what landed | 09-06 | PM | landed 09-06 | 1/4/0 | obs |
| ifd.366 | Conversation-side tracker writes retry | 09-13 | PM | landed 09-20 | 1/2/0 | obs |
| ifd.428.6 | A `bd show` timeout at a gate is waited out | 09-22 | ? | landed 09-23 | 2/3/0 | obs |
| ifd.433.3 | Retry never repeats an unsafe write | 09-25 | PM | landed 09-25 | 1/2/0 | might (guard on retry of 366) |
| ifd.433.4 | Listing round-trips notes for timed-out-create check | 09-25 | PM | landed 09-25 | 1/2/0 | might; follows 433.3 |
| ifd.433.20 | Find what holds the store; sweep says when it can't list | 09-29 | LPM | landed 10-01 | 1/4/2 | obs (and found the "stall" was sleep) |
| ifd.433.24 | Export temporaries cleaned; export off the write path | 09-30 | LPM | landed 10-03 | 14 (12 cancelled)/4/2 | obs; from 433.20 |

**Phase 2: October, timeouts, contention, first cleanup (15 items)**

| Item | What | Created | Admitted by | Outcome | Runs/rounds/repairs | Obs/might; follows from |
|---|---|---|---|---|---|---|
| 06m | Preserved-run recovery, including a run parked on a tracker read ended for age | 10-02 | LPM | landed 10-17 | 2/7/4 | obs (tracker part is one case of several) |
| 6u7 | Why tracker reads exceed 30 s | 10-13 | LPM (from FF-PM) | landed 10-14 | 1/7/3 | obs |
| ifd.433.40 | Harness cleans the store on a bound | 10-14 | LPM | landed 10-16 | 2/5/6 | obs; from 6u7 |
| ifd.433.45 | Scheduler's listings shared or lighter | 10-19 | LPM | landed 10-19 | 1/5/2 | obs |
| ifd.433.47 | Status reading and worktree export stop holding the store | 10-19 | LPM | landed 10-20 | 1/3/2 | obs; reviewer of 433.45 found it incomplete |
| q8w | Recovery refused for a tracker timeout is retried | 10-20 | FF-PM | landed 10-21 | 1/2/0 | obs |
| ifd.433.49 | Slow calls record time, size, holder | 10-20 | LPM | landed 10-26 | 1/3/1 | obs; asked for by 6u7 |
| ifd.433.50 | Maintenance runs the cleanup itself | 10-20 | LPM | landed 10-27 | 1/9/1 | obs; 433.40's cleanup kept skipping |
| ifd.428.99.17 | Put back recoveries refused before q8w | 10-21 | LPM | **retired** 11-15 (obsolete) | 0 | obs; follows q8w |
| ifd.432.46 | Dashboard stops listing every closed item | 10-24 | LPM | landed 10-25 | 1/4/0 | obs |
| ifd.433.57 | Timed-out export names the right run | 10-23 | LPM | **retired** 11-15 (seen once) | 0 | wording |
| ifd.433.59 | Step failing on a passing cause (tracker write timeout) retried | 10-25 | LPM | landed 11-18 | 2/2/1 | obs |
| 4xf3 | Tracker-contention test flaky under load | 10-25 | FF-PM | landed 11-18 | 1/1/0 | obs (test of 433.45/47) |
| 2dl | Shared-listing waiter race test flaky | 10-18 | LPM | open | 0 | obs (test of 433.45) |
| ifd.433.55 | Stray tracker process breaks the timeout-holder test | 10-23 | LPM | open | 0 | obs (test of 433.49) |

**Phase 3: the `428.126.4` tree, store size and the shared reader (29 items)**

| Item | What | Created | Admitted by | Outcome | Runs/rounds/repairs | Obs/might; follows from |
|---|---|---|---|---|---|---|
| 428.126.4 | Parent: one owner for current-state reads | 10-25 | LPM (operator's direction) | open | 0 | design goal |
| 4.1 | Measure what the 930 MB store is | 10-25 | LPM (operator asked) | landed 10-28 | 1/2/0 | obs |
| 4.2 | Architect rules on store growth | 10-25 | LPM | design closed 10-28 | 0 | premise contradicted by 4.1's figures |
| 4.3 | Cleanup failures name items by title | 10-27 | LPM | open | 0 | wording |
| 4.4 | Reads wait 120 s instead of 30 | 10-27 | LPM | landed 11-01 | 2/6/1 | obs; **produced 4.14** |
| 4.5 | Measure server mode, writes by kind, calls by caller | 10-27 | LPM | landed 11-05 | 1/3/0 | obs; server mode never measured (no `dolt`) |
| 4.6 | Maintenance squashes old history | 10-28 | LPM | landed 10-31 | 1/4/0 | might; **never ran once live** |
| 4.7 | Export every 5 min, not every write | 10-28 | LPM | landed 10-31 | 1/2/0 | obs |
| 4.8 | Leave-notes once per reason | 10-28 | LPM | landed 11-01 | 1/11/3 | obs (store growth) |
| 4.9 | Weekly measurement checks limits | 10-28 | LPM | landed 11-08 | 1/1/0 | measured an idle copy, so reported "limit held" while live listings failed |
| 4.10 | Dashboard listings shared, once a minute | 10-28 | LPM | landed 11-06 | 1/12/3 | obs |
| 4.11 | Squash holds off new runs | 10-30 | LPM | landed 11-02 | 1/3/1 | held developer slots on 11-15/16 for a squash that never ran |
| 4.12 | May run storage read the tracker? (ruling) | 10-31 | LPM | ruling closed 10-31 | 0 | boundary question, tangential |
| 4.13 | `make test` check for that boundary | 10-31 | LPM | open | 0 | tangential |
| 4.14 | Why maintenance's tracker check timed out on 11-01 | 11-02 | LPM | diagnosis closed 11-15 | 0 | obs; from 4.4 |
| 4.15 | Cleanup without listing items | 11-02 | LPM | **retired** 11-02 (duplicate of 4.16) | 0 | duplicate |
| 4.16 | Cleanup lists no items on the live store | 11-02 | LPM | landed 11-03 | 1/2/0 | obs; 433.50's cleanup failed at size |
| 4.14.1 | Reconcile reads without listing every item | 11-02 | from 4.14 | landed 11-17 | 2/5/1 | obs |
| 4.14.2 | Cleanup runs even with every slot busy | 11-10 | from 4.14 | landed 11-13 | 1/1/0 | obs; cleanup kept skipping again |
| 4.17 | Squash due from size on disk | 11-11 | LPM | landed 11-16 | 2/2/0 | the squash still never ran |
| 4.18 | Harness processes share one tracker client | 11-15 | LPM | landed 11-19 | 3/4/1 | obs |
| 4.19 | Why the store is still 1.5 GB; bring under 1 GB | 11-16 | LPM | landed, closed 11-20 | 3/3/1 | obs; found squash frees nothing |
| 4.20 | Architect revises store design (rebuild) | 11-17 | LPM | design closed 11-18 | 0 | from 4.19 |
| 4.21 | Backup folder kept to 14 days | 11-18 | LPM | open | 0 | real (3.8 GB) but not about listings |
| 4.22 | Weekly measurement moves to the rebuild | 11-18 | LPM | open | 0 | follows 4.6/4.9 being wrong |
| 4.23 | Rebuild takes the write lock first | 11-19 | LPM | landed 11-19 | 1/1/0 | obs; 4.19's rebuild skipped whenever the store was open, the same mistake as 433.50/4.14.2 |
| 4.24 | Why listings still exceed 10 s at 16 MB | 11-19 | LPM | **open after six landings** (11-19 15:44, 22:23; 11-20 07:05, 23:18; 11-21 07:30, 18:14 PST) | 7/7/2 | obs; **its fourth change produced t3qs** |
| 4.25 | Tests refuse multi-id `bd show` | 11-20 | LPM | open | 0 | guard; from 4.24 |
| 4.26 | Architect rules on server mode | 11-21 | LPM | open | 0 | obs; from 4.24 |

**Phase 4: consequences and mitigations elsewhere (9 items)**

| Item | What | Created | Admitted by | Outcome | Runs/rounds/repairs | Obs/might; follows from |
|---|---|---|---|---|---|---|
| ifd.429.203 | Tracker timeout set by project within bounds | 11-16 | LPM | open | 0 | config |
| ibqo | Note write after merge-queue admission retries until it lands | 11-17 | FF-PM | landed 11-19 | 2/3/0 | obs |
| ifd.428.151 | Waiting starts not counted as holding slots | 11-17 | LPM | landed 11-19 | 2/2/1 | obs (255 ready items idle) |
| ifd.428.158 | Test for a decided re-run losing its slot on a slow tracker | 11-18 | LPM | open | 0 | might; reviewer of 428.151 |
| ifd.429.252 | Queue records its choice rule; keeps priorities when tracker unread | 11-20 | LPM | landed 11-21 | 1/2/3 | obs |
| ifd.430.86 | Timed-out briefing listing test flaky under race | 11-20 | LPM | open | 0 | obs (test) |
| ifd.429.262 | Same test, filed again | 11-21 | LPM | open | 0 | **duplicate of 430.86** |
| t3qs | Harness's own `.turn` files don't block starts | 11-21 | FF-PM | landed 11-21 09:07 PST | 2/2/1 | obs; **caused by 4.24's fourth change** |
| ifd.436.26 | Status and Slack say how old a fallback listing is | 11-21 | LPM | open | 0 | wording |

Counts: 62 items. 39 landed code. 4 were closed as rulings, designs or diagnoses without code (4.2, 4.12, 4.14, 4.20). 4 were retired undone (271, 99.17, 433.57, 4.15). 15 are open.

Items that exist only because an earlier item in the chain fell short: 433.47, 433.50, 99.17, 4.14, 4.14.1, 4.14.2, 4.16, 4.17, 4.19, 4.20, 4.22, 4.23, 4.25, 4.26, 428.158, t3qs, and the five flaky-test items (4xf3, 2dl, 433.55, 430.86, 429.262). That is 21 items, about a third of the chain.

I counted all of `428.126.4` and items whose title or description is about tracker timeouts, listings, contention or store size. I left out items that only touch listing wording, such as 430.66, 433.42 and 433.56. That is why my count of 62 is higher than the rough 50 in the brief.

## Was it fixed

No. Landing after landing, the symptom carried on, and over the thread as a whole it grew.

- **433.45, 433.47, 432.46 and 4.10** (October to early November; fewer listings). Reconcile timeouts still rose from late October. Listings over 10 s rose from 15–28% in early November to 31–41% in mid-November.
- **433.40, 433.50, 4.16 and 4.14.2** (cleanup). The cleanup ran 64 times and was skipped 4,644 times, 114 of them because another process had the store open. The store still grew from 567 MB (10-17) to 1,638 MB (11-19).
- **4.6, 4.11 and 4.17** (squash). The squash never ran on the live store: 2,447 skips, 388 of them for runs in flight. On 11-15/16 the hold from 4.11 left developer slots empty waiting for it (8 watch records).
- **4.4** (120 s reads). Reconcile timeouts went from about 7 a day to 11–16 a day in the week after it landed, and 4.14 attributes the 11-01 timeouts to it.
- **4.19 and 4.23** (rebuild). The store went from 1,637 MB to 15 MB at 05:21 PST on 11-19. The rebuild then ran six more times in the next 70 minutes on a store already at 15–16 MB, and each run stopped every tracker call for 36 to 54 seconds. Listings over 10 s did not fall. With no server and journal-producing writes, the store climbed back to 297 MB in two and a half days.
- **4.18 and the six landings of 4.24.** Each one cut call volume or cost, and the brief dips show it. At 02:53–04:38 PST on 11-21, after the fourth change and with the machine quiet, listings over 10 s fell to 4–15 of about 200–300. They rose again to 30–57 of 100–200 once developer runs loaded the machine. The sixth change's own diagnosis says it "does not bring listings within 10 seconds while every call pays for the journal". Two readings after it landed at 18:14 PST: 47 of 108 and 48 of 97.
- **t3qs.** This one worked. The `.turn` refusals ran from 23:48 PST on 11-20 to 08:54 PST on 11-21 (61 records in `watch.jsonl`). The fix landed at 09:07 PST and none have appeared since.
- **Mitigations that did hold** (q8w, ibqo, 428.151, 433.3, 327). The specific losses they targeted (refused recoveries, a run handed to a person, idle slots, lost writes) have not recurred in the records I read. They treat effects, not the slowness.

Today, 2026-11-21: roughly half of listings exceed 10 s, the slowest still hit the 2-minute limit, reconcile timed out 35 times so far today, and 4.24 is still open. The server-mode ruling, 4.26, was admitted at P0 and is the factory's own conclusion that nothing on the harness side can finish the job.

## Was it real

Yes. It stopped and slowed real work and reached the operator:
- reconcile timeouts in the hundreds;
- 255 ready items idle for 70 minutes (11-15);
- an approved change handed to a person (ibqo);
- lost notes (6u7);
- refused recoveries (q8w);
- publishing runs stopped (433.59);
- a merge-queue order inversion (429.252);
- nine hours of intermittent start refusals from the factory's own ticket files (t3qs);
- 27 operator interventions that mention the tracker.

I could not total developer-slot hours lost.

Split of the 62 items:
- **An observed failure (about 44):** most of phases 1 and 2, 4.1, 4.4, 4.5, 4.7, 4.8, 4.10, 4.14 and its children, 4.16, 4.18, 4.19, 4.23, 4.24, 4.26, ibqo, 428.151, 429.252, t3qs, and the five flaky-test items. The flaky tests are observed failures, but of the factory's own timing tests.
- **Something that might happen, a guard, a wording change, or premised on a cause that measurement had already ruled out (about 18):** 271, 433.3, 433.4, 433.57, 4.2's size premise, 4.3, 4.6, 4.9, 4.11, 4.12, 4.13, 4.17, 4.21, 4.22, 4.25, 428.158, 429.203, 436.26.

## Where the effort was wasted

- **The store-size branch (about 14 items: 433.40, 433.50, 4.2, 4.6, 4.9, 4.11, 4.15, 4.16, 4.14.2, 4.17, 4.19, 4.20, 4.22, 4.23).** It was aimed at a cause the factory's own measurements ruled out. Every idle-copy measurement (943, 1,140 and 1,449 MB) listed in under a second. 4.17's own title concedes "listings on an idle copy are fast". The squash was designed, built, held developer slots, was re-timed, and never ran. The cleanup and then the rebuild were each built to skip whenever another process had the store open, which on this factory means always. That mistake was made twice (433.50 then 4.14.2; 4.19 then 4.23). Shrinking 1.6 GB of store and 3.8 GB of backups is reasonable housekeeping, but it did nothing for listings.
- **Raising the wait to 120 s (4.4)** made stuck callers hold on longer and produced 4.14, 4.14.1 and 4.14.2.
- **The weekly measurement (4.9)** measured an idle copy and twice reported that "the limit has held", while the live factory had 30–40% of listings over 10 s.
- **The server-mode test was blocked for 16 days by a missing binary.** Every measurement since 11-05 records `dolt is not installed`. No item was filed to install it. Instead, 4.24 went through six fixes before concluding that server mode decides the matter.
- **4.24's fourth change (the ticket line, `internal/beads/turns.go`, commit 6b10540dd, landed 23:18 PST 11-20) caused t3qs.** It put a ticket file for every `bd` call in `.beads/yoyodyne-writes/turns/` inside the main checkout. `.beads/.gitignore` covers `*.lock` but not `*.turn`, so the scheduler's uncommitted-changes check (`blockedReason`, `internal/orchestrator/schedule.go`) saw untracked files and refused starts and continuations. The first refusal came 30 minutes after the landing. There were 61 refusals until t3qs landed at 09:07 PST. Two of three slots were held by runs nobody was working from about 02:14 PST, and the P0 fix hnc0 was stalled. It also clouded 4.24's own fifth and sixth measurements, which were taken while starts were being refused.
- **Duplicates and churn:** 4.15 (a duplicate of 4.16), 429.262 (a duplicate of 430.86), 433.57 (one message), 99.17 (retired as obsolete), and 271 (overtaken). Five items fix flaky timing tests that the thread's own contention tests created.
- **Effort with lasting value.** The instrumentation (433.49, 4.5), which made 4.24's diagnosis possible. The genuine cuts in call volume (433.45, 432.46, 4.7, 4.8, 4.10, 4.18, and 4.24's first, second, third and fifth changes). Write safety (327, 433.3). The mitigations that kept work from being lost (q8w, ibqo, 428.151). The sleep diagnosis in 433.20. I estimate this at 25–35% of the effort.
- **Cost.** 75 runs (61 not cancelled), 153 review rounds and 49 repairs. At $3.83 a run and $0.93 a review that is about $376, or about $430 if the cancelled runs count at full price. This is an estimate, and it leaves out the many Lead Product Manager, architect, program manager and development manager turns spent admitting, re-ranking and noting these items.

## What would have been better

- **Act on the cause once it was named.** By 2026-10-14 the record said every `bd` process locks the whole store, and the journal makes every open several times slower. That points to one decision: run `bd` in server mode, or put one long-lived harness process in front of the store that every caller goes through. On 2026-10-25 the operator asked for server mode to be tried. The right next item was "install `dolt` and measure server mode on a copy", which takes a person or the harness about an hour, not a developer run. Instead the factory admitted about 30 items around it.
- **Believe the measurement over the design.** The store-size design should have been withdrawn the day 4.1 measured 0.7 s listings on the 943 MB idle copy. That would have spared most of the squash and cleanup items.
- **Stop after the second failed fix.** 4.24 landed six changes in 51 hours, each followed by "did not end the slow listings". After the second, the right step was the architect's ruling (now 4.26), not a third, fourth and fifth change on the harness side. The fourth of those caused a factory-wide stop.
- **Measure the live system, not an idle copy.** The weekly check should have counted live listing times from the start, which the limits step began doing only on 11-04.
- **Don't raise timeouts to treat contention.** 4.4 lengthened every wait and caused its own follow-up chain.
- **Cheap, immediate relief existed and was underused.** 6u7 showed `bd gc` takes 9 seconds and removes the journal cost. The cleanup ran 64 times in five weeks and was skipped 4,644 times. Running it more often, under the harness's own lock rather than "only when nothing else has the store open", would probably have halved per-call cost while the server-mode question was settled. This is not measured on the live store.
- **Keep harness scratch files out of the checkout.** Any file the harness writes during normal work belongs in the product state directory, not under `.beads/` in the main checkout. That rule would have prevented t3qs.

## What I could not establish

- How many developer-slot hours were lost to slow listings and timeouts in total. The watch records give the t3qs night and the 11-15 episode, not a complete count.
- The tracker's own call logs (`tracker-calls`, `tracker-slow-calls`), which were outside the files I was allowed to read. The per-call figures above come from the diagnoses that read them.
- Whether server mode would actually bring listings within 10 s. It has never been measured here.
- The brief describes 4.24 as having landed its third change at 18:14 PST on 11-21. Git shows six merge-queue landings for 4.24 (11-19 15:44, 22:23; 11-20 07:05, 23:18; 11-21 07:30, 18:14 PST). The 18:14 landing is the sixth.
- Role-conversation cost for this thread, and the exact cost of cancelled runs.
- Whether the September "could not be read" failures share the cause of the November ones. They are the same kind of failure (`bd list` cancelled), but I did not trace them.
- Why the rebuild ran seven times on 11-19 on an already-small store. The likely reason is that slow listings kept marking it due, but I did not confirm this.
