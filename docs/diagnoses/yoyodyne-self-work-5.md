# Self-work review, case 5: moving the orchestrator's tests, and tests that fail under load

One of seven case studies of work the factory did on itself, made at the operator's request of 2026-11-21: "Can you do a deep dive into a few of the factory flow items-- what caused them, how many beads did it take to address the issue, was it really fixed? Was is a real issue in the first place?", written up to "critically consider the possibility that we are wasting time with some or many of these issues". The summary of all seven is `yoyodyne-self-work-0.md` beside this file.

- **Read on:** 2026-11-21, between 18:44 and 19:00 PST.
- **Read by:** a read-only investigator on Opus 5.5 in the operator's assistant's session, told to be a sceptical auditor. It read the tracker export, git history and the factory's own logs and run records, and ran nothing.
- **Checked by hand:** the operator's assistant checked nothing in this file. Every other figure is the investigator's own.
- **How to use it:** evidence for the Lead Product Manager and the architect, not a ruling. The share of effort with lasting value and the verdict are the investigator's judgement. Costs are estimates for developer runs and reviews at mean prices, and leave out role conversations.

The investigator's summary of its findings:

```
thread: Moving the orchestrator's tests out, and tests that fail under load
items_total: 67 (23 test-move items, 36 flaky-test items, 8 time-limit items)
items_landed: 44 (counting 3 parents closed as done once their children landed)
items_retired_undone: 4
items_open: 19
first_item: 2026-09-01
latest_item: 2026-11-21
runs: 100 run records (60 that did real work; 40 were cancelled, 27 of them in two mass cancellations)
estimated_cost_usd: 300-450 (estimate from $3.83 per developer run and $0.93 per review)
fixed: partly
real_problem: yes
share_of_effort_with_lasting_value: 35-50%
verdict: partly wasted
confidence: medium
```

## In short

- The problem was real. Tests that fail when the machine is busy, and one test package (`internal/orchestrator`) that ran up against its time limit, sent correct changes back for repair rounds. They turned main red 12 times between Sep 23 and Oct 8, and they still do so today.
- Moving the tests out has produced no measurable gain. Six groups moved 251 tests. The package's own run time changed by -30s, +8s, +40s, -57s and 0s, all within noise. Over the same weeks the package grew from 834 to 1,982 tests, about 4.6 added for each one moved.
- What actually got the package back under its limit was one 2-hour item, 429.152 on Nov 11. It split the race run into three parallel parts and cut the run from 1,395s to 839-963s. Raising the limit from 20 to 28 to 43 minutes also helped. A developer slot at priority 0 for further moves (429.14.5.1) was not justified.
- Every flaky test fixed in this thread was written by the factory. Of the 30 items that name a specific test, 13 fixed one written two weeks or more earlier. The other 17 fixed one written less than two weeks earlier, 7 of them within 3 days, so those tests were flaky from the day they passed review.

## What went wrong

There were two symptoms, and the factory kept them on separate tracks.

**1. One test package outgrew its time limit.** On Sep 25 the developer on 429.11 found that `internal/orchestrator`'s test binary took 651s under `make test` and 606s under `make race`. The limit was `TEST_TIMEOUT` = 20 minutes, at a load average near 50. The diagnosis 429.12 then measured 1,016s with a second `make race` running beside it (`docs/diagnoses/yoyodyne-ifd-429-12-orchestrator-binary-under-load.md`). After that the binary only got slower:

| when (Pacific) | package size | time for the package's own binary | source |
| --- | --- | --- | --- |
| Sep 25 | 834 tests | 606-1,016s under race | 429.12 diagnosis |
| Oct 6 | – | about 1,170s at load 15-23; two approved changes stopped at the 20-min limit | 429.69 |
| Oct 14 | 1,453 tests | 699s plain at load 9-16; race stopped at the 43-min limit at load over 100 | diagnosis, group 1 |
| Nov 11 | – | 2,370s race passing; 40-46 min against a 43-min limit | 429.152 |
| Nov 19 | 1,867 tests | 1,177-1,234s plain at load 8-36 | diagnosis, group 5 |
| Nov 21 | 1,982 tests | 975s plain at load 6-15 | diagnosis, group 6 |

The harness's own `make race` check, as recorded in item notes for each run, grew from a median of about 1 minute (week of Aug 17) to 8 minutes (Sep 21), 17 minutes (Oct 5) and 23-25 minutes (Oct 12 to Nov 2). For runs touching the orchestrator, the check stage took a median of 30 min in the week of Oct 5, 46 min (Oct 12), 41 min (Oct 19), 44 min (Nov 9) and 38 min (Nov 16) (`check_stage` in `runs/run-*.json`). Nothing in either series falls after the move groups landed (Oct 14-18, Nov 18-21).

**2. Tests that fail only when the machine is busy.** A typical report says a test "failed in the full suite, passed alone", on a change that never touched it. The run records give the size of this. In their `verification.checks`, developers recorded 440 failed checks. Those whose own note blames a test outside the change, load, or a test that passes on a re-run come to this many per week (my keyword count, so approximate):

| week of | runs with a verification record | failed checks | blamed on an unrelated test or load |
| --- | --- | --- | --- |
| Sep 21 | 177 | 16 | 1 |
| Sep 28 | 137 | 69 | 1 |
| Oct 5 | 133 | 93 | 29 |
| Oct 12 | 93 | 60 | 24 |
| Oct 19 | 79 | 20 | 5 |
| Oct 26 | 48 | 9 | 2 |
| Nov 2 | 56 | 17 | 2 |
| Nov 9 | 154 | 93 | 17 |
| Nov 16 | 208 | 63 | 7 |

The items record what this cost:

- Main went red 12 times between Sep 23 and Oct 8, about 13 hours in all (4kr, the operator's own direction).
- A load failure used the last repair attempt on an approved priority 0 change on Oct 19 (6wu).
- Another used up the whole repair budget of the load-scaled check limits rebuild (429.78, run-99f9d284).
- Two approved changes were stopped on Oct 6 by the time limit alone (429.69).
- From Nov 17 to 21, main's scheduled race check failed on a different flaky test almost daily: 4xf3, 429.231, 429.235, 429.244, 430.86 and 429.262 (the same test twice), and 429.271.
- 429.133 records the operator's assistant counting 44 failed `make race` runs among about 390 check runs, with no commit since August that fixes a real data race.

## What caused it

- **Tests built on real processes and clocks.** The orchestrator tests start real `git` processes, about 53,800 of them in one pass of the package (429.12), and 284 of its 292 test files run in parallel (430.88). Many tests across the module wait for a fixed time or bound a real process by the wall clock. `docs/developing-yoyo.md` forbids that, but new tests keep arriving that do it. Every flaky test named in this thread was added by a factory run: its first commit carries a `yoyodyne: <item>` subject. The table below lists them.
- **The factory makes its own load.** The machine has 16 cores and runs three or four developer slots, merge-queue candidate checks, a race check on main every four hours, and the developer sessions' own test runs. 430.88 found that between 00:00 and 12:50 PST on Nov 20, developer sessions chose to run every test in the orchestrator package 45 times (294 minutes, two of them under the race detector) and the whole module 13 times, many with the test cache off. One-minute load averages of 15-50 were normal and peaks passed 100-300. The tests are flaky at the load the factory itself creates.
- **The package keeps growing.** Every feature item adds tests to `internal/orchestrator`. Nothing slowed the growth, so the move was trying to drain a bath with the tap running.
- **Review let the timing tests through.** 429.231, 429.235, 429.244 and 429.200 were filed 1 to 8 days after the tests they fix landed. All four had passed review and checks.

## The chain of items

Runs, review rounds and repairs come from `runs/run-*.json`. Cancelled runs are counted, but most were in two mass cancellations (429.13.6: 13; 429.36: 14; 429.41: 13).

### A. Moving the tests (23 items)

| id | short name | filed (Pacific date) | raised by, why | outcome | runs (cancelled) / reviews / repairs |
| --- | --- | --- | --- | --- | --- |
| ifd.429.12 | split or trim the orchestrator binary | Sep 25 | Lead PM, from 429.11 developer's report | **retired**: the run landed only a diagnosis; it said a split needs moving 674 tests | 1 / 3 / 0 |
| ifd.429.15 | gitworktree caches git answers | Sep 25 | Lead PM, from 429.12 diagnosis | landed Sep 25 (b1182b5c); 702.9s → 663.2s, about 6% | 1 / 1 / 0 |
| ifd.429.13 | move shared fakes to a helper package (parent) | Sep 25 | Lead PM, 429.12 reviewer | closed as done Oct 14; first attempt was an 807 KB patch, too big to review (PR 757) | 1 / 1 / 0 |
| ifd.429.13.1-.13.5, .13.7 | six migration pieces | Sep 25 | development manager | all landed Sep 25-26 | 7 / 11 / 1 |
| ifd.429.13.6 | remove in-package fakes | Sep 25 | development manager | closed as done Oct 11 via two children | 15 (13) / 3 / 3 |
| ifd.429.13.6.1, .13.6.2 | the two halves of 13.6 | Oct 6 | development manager, because 13.6 was too big | landed Oct 6 and Oct 9 | 2 / 6 / 2 |
| ifd.429.14 | move the tests that touch no internals (parent) | Sep 25 | Lead PM | **open**; its own run landed group 1 (43 publication tests, `scripts/movetests`, `testharness`) Oct 14; priority moved P2→P1→P0→P1→P0→P1 | 1 / 4 / 1 |
| ifd.429.14.1 | first half of pipeline_test.go (43 tests) | Oct 15 | development manager | landed Oct 17 (PR 1338); a timed-out replay needed a resume | 2 / 4 / 5 |
| ifd.429.14.2 | protection and landing (62 tests) + fix move tool | Oct 15 | development manager; widened by Lead PM for the tool defect the 14.1 review found | landed Oct 18 (PR 1344) | 1 / 2 / 1 |
| ifd.433.41 | duplicate check refuses sibling move groups | Oct 15 | Lead PM; the tracker refused 5 of the 7 group items as duplicates | landed Oct 29 | 1 / 3 / 0 |
| ifd.429.14.4 | reconciliation and recovery (59 tests) | Oct 15 | Lead PM | landed Nov 18 (merge queue 7d802e2d); first change sent back by `make vet` against a moved main | 2 / 2 / 0 |
| ifd.429.14.5 | everything left (parent of parts a-d) | Oct 15 | Lead PM | **open**; a run moved 33 tests, landed Nov 19; the development manager then logged about 13 timed waits over two days, looking for a landing that had already happened | 1 / 1 / 0 |
| ifd.429.14.3 | re-measure once every group has moved | Oct 15 | development manager | **open**, waits on the last group | 0 |
| ifd.429.14.5.1 | last 11 tests in pipeline_test.go | Nov 21 | development manager; filed at **P0**, with no reason recorded | landed Nov 21 (822d838a); the run took 07:53-10:40 PST; package time 975s before and after | 1 / 1 / 0 |
| ifd.429.14.5.2 | merge-queue tests | Nov 21 | development manager | **open** | 0 |
| ifd.429.14.5.3 | scheduler tests | Nov 21 | development manager | **open** | 0 |
| ifd.429.14.5.4 | final sweep | Nov 21 | development manager | **open**; "moves as much as fits" | 0 |

Subtotal: 36 run records (23 doing work), 42 review rounds, 13 repairs. Items that exist only because an earlier one could not finish: 429.13.1-.13.7 and .13.6.1-.6.2 (the first patch was too big), 433.41 (the duplicate check refused the groups), and 429.14.5.1 (429.14.5 left 11 tests behind to keep its diff under the review size limit).

### B. Tests that fail under load (36 items)

"Test written" is the date of the test's first commit; every one is a factory commit.

| id | short name | filed | test written | outcome | runs / reviews |
| --- | --- | --- | --- | --- | --- |
| ifd.235 | checkpoint size-estimate test | Sep 1 | Aug 31 | landed Sep 1 | 1 / 2 |
| ifd.254 | conversation-store lease race | Sep 3 | Aug 15 | landed Sep 3 (characterised) | 1 / 1 |
| ifd.270 | wall-clock tests in four packages | Sep 3 | Aug 17-19 | landed Sep 22; first run failed | 2 / 3 |
| ifd.429.3 | second batch: the three 270 left, plus 3 more | Sep 23 | Aug 15-Sep 4 | landed Sep 25 | 3 / 4 |
| ifd.429.8 | expired-login watch test | Sep 24 | Sep 18 | landed Sep 24; **failed again**, named in 4kr Oct 8 | 1 / 2 |
| ifd.429.11 | git and tracker steps scale with load | Sep 25 | – | landed Sep 25 | 1 / 3 |
| ifd.429.20 | promotion_test arrival timer | Sep 25 | Sep 22 (sibling) | landed Oct 27 at P0; first run timed out | 2 / 6 |
| ifd.429.36 | ten 30-second budgets on real processes | Sep 28 | Sep 24 + older | landed Oct 23 | 15 (14) / 2 |
| ifd.429.57 | run-lease holder test | Oct 4 | Sep 27 | landed Oct 4 | 1 / 3 |
| ifd.435.14.9 | launcher test on Linux | Oct 7 | Oct 6 | landed Oct 8 | 1 / 2 |
| yoyodyne-4kr | the tests that turned main red 12 times | Oct 8 | Aug 19-Sep 22 | landed Oct 8, P0; includes 3rd item for the scheduler worktree test, 2nd for the login test | 1 / 4 |
| ifd.429.74 | two merge-queue restart tests | Oct 13 | Oct 8 | landed Oct 15; 3 earlier reports had been mapped to closed 4kr and dropped | 1 / 5 |
| ifd.429.77 | heavy-check finish record / timing test | Oct 14 | Oct 12 | landed Nov 18 | 2 / 2 |
| ifd.429.78 | runstate tests and lock waits | Oct 14 | Aug 15 (2nd item for that test, after 429.3) and Oct 12 | landed Oct 19 | 1 / 2 |
| ifd.429.83 | merge-queue admission test | Oct 18 | Oct 6 | **retired**, duplicate of 429.78 | 0 |
| ifd.429.85 | `yoyo start` test waits on a condition | Oct 19 | Sep 19 | landed Oct 20 | 1 / 2 |
| yoyodyne-6wu | same test, filed 2 min later | Oct 19 | Sep 19 | **retired**, duplicate | 0 |
| ifd.428.106 | other short runstate lock waits | Oct 19 | – | **open**; "nothing yet shows these waits failing a run" | 0 |
| ifd.433.55 | stray tracker process reaches a test's store | Oct 23 | Oct 16 | **open** | 0 |
| yoyodyne-4xf3 | read-model tracker-contention tests | Oct 25 | Oct 19 | landed Nov 18 | 1 / 1 |
| ifd.433.61 | store-cleanup conformance test | Oct 26 | Oct 16 | **retired**: one failure, never again | 0 |
| ifd.429.133 | sort race failures into real races vs timing | Nov 9 | – | **open** | 0 |
| ifd.429.149 | test-run recovery tests | Nov 10 | Oct 26 | closed Nov 16; the real fix had already landed with another item (h0ou) | 2 / 2 |
| ifd.429.200 | merge-queue ahead-rebuild test | Nov 16 | Nov 13 | landed Nov 17 (hand merge) | 1 / 1 |
| ifd.429.231 | candidate-checkout cut-off test | Nov 18 | Nov 10 | **open** | 0 |
| ifd.429.235 | maincheck half-written checkout test | Nov 19 | Nov 17 | landed Nov 20 | 1 / 1 |
| ifd.429.236 | main check re-runs failing tests once | Nov 19 | – | landed Nov 20 (the pattern fix) | 3 / 3 |
| ifd.429.238 | tracker caller-deadline test | Nov 19 | Oct 26 | landed Nov 19 (hand merge) | 1 / 1 |
| ifd.429.244 | stop cuts a long attempt short | Nov 19 | Nov 18 | **open** | 0 |
| ifd.429.247 | merge-queue long-wait text test | Nov 20 | Nov 16 | **open** | 0 |
| ifd.430.86 | briefing listing that ran out of time | Nov 20 | Oct 26 | **open** | 0 |
| ifd.430.88 | developer runs test only what they touch | Nov 20 | – | **open** | 0 |
| ifd.429.262 | the same test as 430.86 | Nov 21 | Oct 26 | **open; a duplicate nobody caught** | 0 |
| ifd.429.264 | runstate exact-count process tests | Nov 21 | recent | **open** | 0 |
| ifd.429.270 | Codex resume test, now in the moved `pipelinetests` | Nov 21 | Nov 5 | **open** | 0 |
| ifd.429.271 | cost/preservation test | Nov 21 | Oct 1 | **open** | 0 |

Subtotal: 43 run records (29 doing work), 52 review rounds, 7 repairs.

The same test came back more than once:

- The scheduler worktree test was in 270, then 429.3, then 4kr.
- `TestAdoptWaitsOutALeaseNobodyHoldsAnyMore` was in 429.3, then 429.78.
- The expired-login test was in 429.8, then 4kr.
- The briefing-listing test is in 430.86 and 429.262, both open.
- 429.74 exists because three reports were mapped to a closed item and dropped.

### C. Time-limit stopgaps (8 items)

ifd.429.41 (load-scaled check limits, 15 runs, 13 cancelled, plus .41.1), ifd.429.60 (stage limit 30→60 min), ifd.429.69 (TEST_TIMEOUT 20→28 min), yoyodyne-lnt (per-check 30→45 min), yoyodyne-ur1 (TEST_TIMEOUT →43 min), ifd.429.84 (stage limit →90 min), ifd.429.152 (split the race run into three parts, Nov 11-13), and yoyodyne-g9g (return the limit to 60 min; open). 21 run records, 8 doing work, 19 reviews. Each raise was filed as lasting only "until the package split lands". The split has not landed, so the raises are still in place.

## Was it fixed

**Time limits: mostly yes, but not by the moves.**

- After group 1 (Oct 14), the package still hit the 43-minute limit at load over 100.
- After groups 2 and 3 (Oct 17-18), the package's own binary went 1,173s → 1,142s and 1,057s → 1,065s. The diagnosis itself says one pass at a changing load "is not evidence either way".
- By Nov 11 the race run had grown to 40-46 minutes against 43 (429.152).
- 429.152 split the race run into three parallel `go test` invocations (`scripts/race-run.sh`, `RACE_PARTS=3`): 1,395s as one binary, 839-963s as three parts, even at higher load. That is the outcome 429.14 was meant to deliver: no single binary near the limit. It came from a 2-hour run that moved no tests.
- After groups 4, 5 and 6, the package's own binary still runs 975-1,234s.
- The parent's done condition (no binary over half of TEST_TIMEOUT at load above 40) has never been measured. 429.14.3 waits on groups that are still open.

**Flaky tests: no.**

- Individual fixes land, and the developer-reported load failures dropped in late October.
- That drop coincides with scoped race checks (429.46, Oct 24), narrowed checks (428.126.6.1, Oct 28), whole-suite checks moving to the merge queue and CI, and fewer runs. It does not coincide with any move: none landed between Oct 18 and Nov 18.
- In the week of Nov 9 the count was back at 17. From Nov 17 to 21, eight new flaky-test items were filed.
- One of them, 429.270, is a test inside `pipelinetests`, a package the move created. Moving a test does not make it stop flaking.

Today (Nov 21), 12 flaky-test items are open. The structural fix for their cost, re-running only the failing untouched tests once (429.236 on main, 429.87.2 for runs, landed Nov 20-21), has just landed. Its effect is not yet visible in the records.

## Was it real

Yes, for the symptoms. Load failures demonstrably spent repair rounds:

- 53 developer-recorded failures blamed on unrelated tests or load in the two weeks from Oct 5.
- The last repair attempt on an approved priority 0 change (6wu).
- A whole repair budget (429.78).
- Two approved changes stopped by the time limit (429.69).
- Main red 12 times, about 13 hours in all (4kr).
- The operator stepped in personally on Oct 8.

Observed failure versus something that might happen:

- **Observed:** 429.12, 429.69, 429.152, and the four limit raises. Of the 36 flaky items, 30 name a test seen failing.
- **Might happen:** 429.20 (a "sibling with the same shape" as a fixed test), 428.106 ("nothing yet shows these waits failing a run"), 429.264 (other tests that "could" fail like the two that did), 433.61 (one failure, never repeated; retired).
- **Real problem, wrong remedy:** the test move. Its premise was that a package near its time limit fails unrelated changes, and that was observed. Splitting the package was a slow fix for it, and the faster one (split the run, not the code) was available all along.

## Where the effort was wasted

- **The move as a remedy (about 20 items, 23 working runs, roughly $125-180).** 251 tests moved and no measured change in the package's time. The package gained about 1,150 tests meanwhile, and 1,173 movable tests remain, more than the 1,035 at the Oct 14 census. Each group spent much of its effort on the review size limit: swapping tests so Git would show the diff as removals, splitting files under 64 KB, a tool defect that renamed "publishing" in comments, and a duplicate check that refused the groups (433.41). The helper code is now kept in two copies, about 2,500 lines in `testharness` and 1,900 in `orchestratortest`, plus a 1,449-line move tool. That is maintenance cost the factory took on for itself.
- **429.14.5.1 at P0.** It was created at priority 0 with no reason recorded. It held a developer slot from 07:53 to 10:40 PST on Nov 21 while the watch was blocking new run starts ("uncommitted changes in the primary checkout", 07:51-08:54 PST) and restarting onto a new build. It moved 11 tests, and the package time was 975s before and after. Under the operator's P0 rule (usability degraded, or 25% or more of total cost), it does not qualify.
- **Duplicates and repeats among the flaky items:** two retired duplicates, one open duplicate (429.262), three tests fixed twice or three times, and one obsolete item retired.
- **Bookkeeping.** About 13 timed-wait notes on 429.14.5 over two days, looking for a landing that had already happened on Nov 19.
- **Lasting value:** 429.152, the limit raises (stopgaps, but they stopped the time-outs), 429.15 (6% and fewer git processes everywhere), 429.236 and 430.88 (the first items aimed at the class, not the single test), and the single-test fixes that held. That comes to roughly 35-50% of the effort.

## What would have been better

1. **Split the run, not the package, on Sep 25.** The 429.12 diagnosis already said almost all tests are parallel and limited by one binary's `-parallel`. A sharding script like 429.152's was a two-hour change. It would have made the 22 move and fake-extraction items unnecessary for the time-limit problem.
2. **Stop after the second move group showed no measured change (Oct 18).** The diagnosis said so in plain words, and the factory carried on for five more weeks.
3. **Treat the flakes as a class from the start.** For example: a re-run-once of failing untouched tests (built only on Nov 20-21), a check that rejects new tests using wall-clock waits on real processes, and keeping developer sessions from running the whole orchestrator suite (430.88). Filing one item per test, 30 times, kept fixing yesterday's tests while new ones arrived.
4. **Make the reviewer reject timing-dependent tests.** Seven of the flaky tests were filed within 3 days of landing.
5. **Cap the package's growth, or put new tests elsewhere.** Without that, any move is outpaced.
6. **Close 429.14 and its four open children now.** Keep the sharding and decide by measurement whether any further move is needed. On the evidence above, it is not.

## What I could not establish

- How many of the harness's own check failures (as opposed to developer-recorded ones) were caused by unrelated tests. The run records keep only the final `check_failure` (103 runs) and a count of repair attempts, not the cause of each repair. My unrelated/load counts come from keyword matching on developer notes and are approximate.
- Check durations after Nov 9 are thin in item notes. The check-stage times mix in queue waits, narrowing and load, so neither series can isolate an effect of the moves. Nor can the diagnosis's single-pass timings.
- Why 429.14.5.1 was created at priority 0: the tracker shows no priority change, and no note gives a reason.
- What the 40 cancelled runs cost. My cost range assumes they cost little.
- Whether 429.236 and 429.87.2 (re-run failing untouched tests once) have reduced send-backs. They landed one to two days before this review.
