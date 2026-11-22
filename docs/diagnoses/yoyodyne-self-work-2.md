# Self-work review, case 2: developer slots left empty or held by nobody

One of seven case studies of work the factory did on itself, made at the operator's request of 2026-11-21: "Can you do a deep dive into a few of the factory flow items-- what caused them, how many beads did it take to address the issue, was it really fixed? Was is a real issue in the first place?", written up to "critically consider the possibility that we are wasting time with some or many of these issues". The summary of all seven is `yoyodyne-self-work-0.md` beside this file.

- **Read on:** 2026-11-21, between 18:44 and 19:00 PST.
- **Read by:** a read-only investigator on Opus 5.5 in the operator's assistant's session, told to be a sceptical auditor. It read the tracker export, git history and the factory's own logs and run records, and ran nothing.
- **Checked by hand:** the operator's assistant checked nothing in this file beyond the earlier hand checks of the same day's stall. Every other figure is the investigator's own.
- **How to use it:** evidence for the Lead Product Manager and the architect, not a ruling. The share of effort with lasting value and the verdict are the investigator's judgement. Costs are estimates for developer runs and reviews at mean prices, and leave out role conversations.

The investigator's summary of its findings:

```
thread: Developer slots left empty or held by nobody
items_total: 38
items_landed: 24
items_retired_undone: 1
items_open: 12
first_item: 2026-09-21
latest_item: 2026-11-21
runs: 45 (36 repair attempts, 106 review rounds, from runs/run-*.json)
estimated_cost_usd: 270-420
fixed: partly
real_problem: yes
share_of_effort_with_lasting_value: 30-45%
verdict: partly wasted
confidence: medium
```

(The 38 items: 24 landed, 1 retired undone (`ifd.428.34`), 1 closed as answered with no run (`7mk`), 12 open or in progress.)

## In short

- The problem was real. A developer slot sat empty, or was counted as taken by a run nobody was working, again and again from 2026-09-20 to today. From the episodes I could document, about 150-250 slot-hours were lost, which is roughly 3-6% of the slot time available. It reached the operator: he raised capacity by hand on 09-21 to get round two dead runs, and his assistant wrote to the Lead PM about idle slots at least 15 times.
- There was never one root cause. The scheduler, the run store's reservation, the per-slot routing records, the health check and `yoyo status` each count "taken" in their own way. Every new run state slipped through one of those counts, and each slip became a new item.
- 38 items, 45 runs, about $270-420 (estimate). About 11 of them exist only because an earlier fix in the chain did not finish the job or caused the next problem.
- It is not fixed. On 11-21 three more episodes of a slot standing empty for close to an hour came after the day's landed fixes. All three slots were held with nobody working from about 02:23 to 04:40 PST, and only partly filled until about 06:15. Two items are in progress and 10 more are open.
- Which kind of fix lasted: the September fixes that settle dead or paused runs, the October fix that refills a freed slot at the next poll, and the November record of why a slot is empty. Most of the November patches that taught the scheduler's own private count about runs waiting on GitHub's checks did not last.

## What went wrong

What a watcher saw, in order. Times are Pacific.

| When | What was seen | Size | Source |
|---|---|---|---|
| 09-20 07:20 to 09-21 evening | Two runs with no live process still read as "running" and held 2 of 3 slots. The operator raised capacity to 4 "while two slots are held by stalled runs" (config commit bfd4c6a7e). | about 2 slots × 30 h | `ifd.428.4`; config history; no runs started 09-20 17:46 to 09-21 13:18 |
| 09-26 afternoon | Two of three slots were empty for more than an hour, first after a restart and again during a deploy drain | about 4 slot-h | `ifd.428.31`, `ifd.428.34` |
| 09-26 18:04 to 09-27 ~14:00 | A run that was dead, or paused on a dependency, held slot 1 for about 20 h | about 20 slot-h | `ifd.428.49`, `ifd.428.50`; `docs/diagnoses/yoyodyne-ifd-428-49-dead-run-held-its-slot.md` |
| 10-25 09:39 PDT to about 10-26 03:00 | All slots were counted taken by left-over routing claims with no run behind them, and no developer run started | up to ~45 slot-h | `ifd.428.128`; no runs started 10-25 12:20 to 10-26 02:54 |
| 10-31 14:31 to 15:25 | One slot stayed empty during a build move after its wait limit ran out | about 1 slot-h | `ifd.428.133` |
| 11-09 18:17 to 21:20 | One slot was empty in two windows, and nobody could tell why | about 2.6 slot-h | `zt5n`; `docs/diagnoses/yoyodyne-zt5n-november-9-empty-slots.md` |
| 11-15 09:20 to ~12:00 | Two of three slots were free while decided re-runs did not start, and the scheduler's own slow starts were counted as taken | about 5 slot-h | `ifd.428.99.24`, `ifd.428.151` |
| 11-16 to 11-21 | The scheduler itself recorded leaving a slot empty "because of a slot still counted as taken": 1.3, 1.5, 3.3, 4.1, 2.8 and 0.9 slot-hours a day. A publication run whose item closed on 10-13 held a slot in its count for four days. | about 14 slot-h | `watch.jsonl` `empty_slots` records; `fp3.9` |
| 11-18 to 11-20 | Runs waiting on GitHub's checks were counted as holding slots. At 23:00 on 11-19 all three slots stood empty with 291 items ready. | at least 10 slot-h | `ifd.428.164`, `z5f9`, `ifd.428.175`, `ifd.428.176` |
| 11-21 02:23 to ~06:15 | Four runs refused at start or continuation, because the harness's own tracker marker files counted as uncommitted changes, still counted against slots with no lease. No developer worked from 02:23 to 04:40, after which slots filled only partly. `watch.jsonl` shows 61 refusals that day, the first ever. | about 8-10 slot-h | `t3qs`, `ifd.428.187`; `watch.jsonl` 10:20Z to 14:15Z |
| 11-21 10:07 to 15:17 | One slot stood empty for close to an hour three times, each counted for a run already in the merge queue or waiting on checks | about 2.7 slot-h | `vjbp` |

How I measured it, and the limits:
- `watch.jsonl` has given a reason for each empty slot only since 11-16 (the `empty_slots` field, added by `zt5n`).
- Before 11-16 the scheduler wrote nothing while it believed every slot was taken. In those periods the gaps in run starts and the item descriptions are the only evidence.
- The worst failure mode, a slot counted taken by a run that is not working, is by its nature invisible in the scheduler's own log. Its lines read "no developer slot is left empty while ready work waits: every developer slot is counted taken" even at 02:30 on 11-21, when nobody was working.

## What caused it

The capacity design has several independent counts of "taken":

1. **The run store's reservation**: runs on record under a lock (`runstate.Store.Reserve`).
2. **The per-slot routing records**: `ClaimSlot` in `internal/runstate/routingstore.go`, plus provider attempts not marked ended.
3. **The scheduler's own in-memory count of "starts of this session"**, which includes starts not yet on record (`internal/orchestrator/idlestarts.go`, `emptyslots.go`).
4. **The health check's rule** (`runstate.State.HoldsDeveloperSlot`, `internal/readmodel/emptyslots.go`).
5. **`yoyo status` and the dashboard's own count**: `ifd.436.16` and `ifd.436.24`, where status read "4 of 3 in use".

A slot is freed only when the thing that took it releases it. That works only if a process is alive to do the releasing. Nothing ties a slot to a live worker by lease and heartbeat. So every way a run can stop working without releasing becomes a new failure: the process vanished, it paused on a dependency or on usage limits, it was refused at continuation, it pushed and began waiting on GitHub's checks, it went into the merge queue, or the scheduler restarted. `idlestarts.go` is now a 60-line comment explaining special cases.

Earlier factory work that introduced or set off parts of it:

- **`ifd.429.76.3` (checks on CI, build 3, landed 11-17, 80265d26d)** added a state in which a run "gives back its slot and waits for CI". The scheduler's private count did not learn this. Directly behind it come `ifd.428.164` (first seen 11-18, the day after), `z5f9`, `ifd.428.176`, `ifd.428.170`, `hnc0`, `ifd.428.185`, `ifd.428.188` and `vjbp`: eight items.
- **`ifd.428.126.2.2` (recording the slot a run holds, 10-30)** made runs that were taken up again wait for their own recorded slot while other slots were free. That produced `ifd.428.126.2.3`.
- **`zt5n` (11-13)** added an id check that refused the watch's whole record of what it was doing whenever a work-item id contained a dot. That produced `ifd.428.157`, still open after 25 failed writes.
- **`ifd.433.23` (10-01)** created the tracker-write marker files under `.beads/yoyodyne-writes/turns/`. The "uncommitted changes" start guard did not ignore them. That was harmless until 11-21, when the guard refused every start and continuation (`t3qs`).
- **Per-slot routing (`ifd.435.14.x`, `ClaimSlot`)** brought in the routing claims that, left behind after a restart, held every slot on 10-25 (`ifd.428.128`).

The basic shape — the scheduler counting its own starts, and slots released only by the run that took them — was there from the start.

## The chain of items

Runs, repairs and review rounds are from `runs/run-*.json`. "Because" means the item exists because an earlier item in this chain fell short or caused it.

| # | Item | Short name | Filed | Raised by, and why | Outcome | Runs / repairs / reviews | Because |
|---|---|---|---|---|---|---|---|
| 1 | `ifd.428.4` | Settle runs whose provider vanished | 09-21 | Operator: two dead runs held 2 slots | Landed 09-22, 1fff1644 | 1 / 0 / 2 | |
| 2 | `ifd.428.5` | Settle runs that exited on the usage bound | 09-22 | Reviewer of #1: same shape, not seen | Landed 09-22, 887866ac | 2 / 2 / 7 | #1 |
| 3 | `ifd.428.31` | Refill a freed slot at the next poll | 09-26 | Seen: 2 of 3 slots empty for more than 1 h | Landed 09-27, 5f349c7f | 1 / 0 / 2 | |
| 4 | `ifd.428.34` | A deploy restart does not idle slots | 09-26 | Seen: 2 slots empty during a drain | Retired 09-28, superseded by the bounded drain | 2 / 0 / 0 | |
| 5 | `ifd.428.43` | Refill a slot freed by another process | 09-27 | Developer on #3: sibling path, not seen | Landed 09-27, 49139a53 | 1 / 0 / 3 | #3 |
| 6 | `ifd.428.49` | Sweep settles a run with no process | 09-27 | Seen: dead run held slot 1 for 20 h; #1's sweep missed it | Landed 09-27, e8c8f97b | 1 / 2 / 4 | #1 |
| 7 | `ifd.428.50` | A paused run gives its slot back | 09-27 | Seen: same run, paused, held its slot for 19 h | Landed 09-28, f02aae1b | 1 / 1 / 3 | |
| 8 | `ifd.428.73` | Retire a run whose item closed, freeing its slot | 10-04 | Operator: maintenance failing every 10 min | Landed 10-05, 5cbb8411 | 1 / 3 / 5 | |
| 9 | `7mk` | Does the 10-02 run still hold a slot? | 10-07 | Development manager | Answered, no run (cancelled 10-09) | 0 | |
| 10 | `ifd.428.115` | A repair that cannot start frees the slot | 10-25 | Seen: a recovery that never begins took every pull | Landed 10-28, e5a3f76b | 1 / 2 / 6 | |
| 11 | `ifd.428.117` | One atomic reservation for every start | 10-25 | Outside code review: "traced in code, not reproduced" | Landed 10-28, 6c86a8b9 | 1 / 2 / 6 | |
| 12 | `xuas` | Why no run started on 10-24, 11:49 to 13:33 | 10-24 | Factory-flow PM | **Open** | 0 | |
| 13 | `ifd.428.128` | Free slot claims with no live run | 10-26 | Factory-flow PM: no run started from 10-25 09:39 | Landed 10-29, ec7b461a | 1 / 2 / 11 | |
| 14 | `ifd.428.131` | Release note names every attempt it settled | 10-29 | Reviewer of #13: wording | **Open** | 0 | #13 |
| 15 | `ifd.428.126.2.3` | A run taken up again uses any free slot | 10-30 | Reviewer of 126.2.2; seen 10-31 | Landed 11-02, 24deb232 | 2 / 4 / 18 | 126.2.2 |
| 16 | `ifd.428.133` | Fill a free slot during a build move | 10-31 | Development manager: slot empty for most of an hour | Landed 11-19, 22d61633 (7 runs over 19 days) | 7 / 1 / 6 | |
| 17 | `zt5n` | Record why a slot is empty | 11-10 | Factory-flow PM: 11-09 gaps nobody could explain | Landed 11-15, 482ee238 | 2 / 3 / 5 | |
| 18 | `ifd.428.157` | Watch record refused for dotted ids | 11-18 | Seen in the log 25 times; introduced by #17 | **Open** | 0 | #17 |
| 19 | `ifd.428.99.24` | Decided fresh runs take the next free slot | 11-15 | Program manager: 2 slots free, 6 decided runs waiting | Landed 11-18, 4abf8267 | 1 / 0 / 1 | |
| 20 | `ifd.428.151` | Slow-tracker starts not counted as taken | 11-17 | Developer report: 255 items unstarted for 70 min | Landed 11-19, 377af9c9 | 2 / 1 / 2 | |
| 21 | `ifd.428.158` | Test: a re-run that lost its slot is retried first | 11-18 | Reviewer of #20: test gap | **Open** | 0 | #20 |
| 22 | `fp3.9` | A closed publication run never holds a slot | 11-18 | Development manager's health check: flagged 17 times | Landed 11-19, 33d5308e | 1 / 1 / 2 | |
| 23 | `ifd.428.164` | Slot unfilled while its runs wait on GitHub | 11-18 | Development manager report; seen the day after 429.76.3 | Landed 11-19, 290957fa | 2 / 0 / 2 | 429.76.3 |
| 24 | `ifd.428.99.32` | Stop retrying permanently refused decisions | 11-19 | Reviewer of #23: 17 refusals | **Open** | 0 | #23 |
| 25 | `z5f9` | A slot counts taken only while a run works | 11-19 | Factory-flow PM: 2 of 3 slots with no working run | Landed 11-20, 587babff | 1 / 0 / 1 | #23 |
| 26 | `ifd.428.170` | An approved run waiting on GitHub is taken up | 11-19 | Development manager: stranded run, continued by hand | Landed 11-19, ac8907bd | 1 / 0 / 1 | 429.76.3 |
| 27 | `ifd.428.175` | The scheduler moves builds; no slot counted on a stale build | 11-20 | Development manager: 2 slots empty, 27th such count | Landed 11-20, 869e7d4a | 1 / 2 / 3 | |
| 28 | `ifd.428.176` | Slot free while its run only waits on GitHub, on the build with #25 | 11-20 | Operator's assistant: all 3 empty at 23:00 | Landed 11-20, 5b711351 | 1 / 0 / 1 | #25 |
| 29 | `ifd.428.178` | Health check names the real cause of an idle slot | 11-20 | Reviewer of #28: wrong cause sent three roles after the wrong fix | **Open** | 0 | #28 |
| 30 | `ifd.436.16` | `yoyo status` slot line never exceeds the slot count | 11-20 | Program manager digest: "4 of 3 in use" | **Open** | 0 | 429.76.3 |
| 31 | `ifd.436.24` | Running figure counts only developers in slots | 11-20 | Operator asked whether six developers were running | **Open** | 0 | |
| 32 | `ifd.428.181` | Due continuation starts while a build move waits | 11-20 | Report: continuation due for an hour, 3 times that day | Landed 11-21, 91c0fe8d | 2 / 1 / 2 | #16, #27 |
| 33 | `hnc0` | Run stranded after a restart taken up by the next freed slot | 11-20 | Factory-flow PM: #26's stall recurred | Landed 11-21, dba77439 | 1 / 2 / 4 | #26 |
| 34 | `t3qs` | Tracker marker files never refuse starts | 11-21 | Factory-flow PM: every start refused | Landed 11-21, 586af976 | 2 / 1 / 2 | 433.23 |
| 35 | `ifd.428.185` | Every take-up path uses any free slot | 11-21 | Developer of #33: four other paths, not seen | **In progress** | 1 / 1 / 0 | #33 |
| 36 | `ifd.428.187` | A slot with no lease and no live worker is given back at the next poll | 11-21 | Program manager: no developer worked from 02:23 | **In progress** (3 failed runs so far) | 4 / 3 / 2 | #13 |
| 37 | `vjbp` | Give back a slot from the scheduler's own count when its run stops holding one | 11-21 | Factory-flow PM: 3 near-hour gaps on 11-21 | **Open** | 0 | #25, #28 |
| 38 | `ifd.428.188` | Runs whose checks passed are taken up in priority order | 11-21 | Operator's assistant's readings | **Open** | 0 | #33 |

Counts:
- **"Because" column:** 18 items (#2, #5, #6, #14, #15, #18, #21, #23, #24, #25, #26, #28, #29, #30, #33, #35, #36, #37) exist because an earlier fix fell short, a reviewer of a fix found its sibling, or earlier work introduced the defect. 11 of them are pure follow-ons from inside this chain: #2, #5, #6, #14, #18, #21, #24, #28, #29, #33 and #35.
- **Left out on purpose:** `ifd.428.95` and `ifd.428.108` were filed as "slots exist but P0 work gets no run". Their own write-ups found every slot busy with live runs, and `ifd.428.108` was retired as "premise wrong". `ifd.429.181` was folded into `ifd.428.99.23`. Dedicated-slot and routing items are also out. Counting those would make the thread about 45 items.

## Was it fixed

For each landed fix, the state files after its landing:

- **`ifd.428.4`, `ifd.428.49`, `ifd.428.50` (September dead and paused runs):** largely held. I found no later case of a dead process holding a slot for most of a day. But the same class came back in a new form: claims and leases with no live worker on 10-25 (`ifd.428.128`), and again on 11-21 while the scheduler process was alive (`ifd.428.187`).
- **`ifd.428.31` / `ifd.428.43` (refill at the next poll):** held. Thousands of "filled N of M free developer slot" lines afterwards, and no recurrence of a scheduler blocking on a completion.
- **`ifd.428.128` (free orphaned claims):** fixed the 10-25 case. Its rule frees only a run "whose program has gone", which is exactly the gap 11-21 fell through.
- **`ifd.428.133` (fill during a build move):** landed 11-19. Drain-time idle seats were 0.4 slot-hours a day on 11-17 to 11-20 and below 0.1 on 11-21, against 1-2.6 a day on 11-12 to 11-15. The three-hour build-move rule (`ifd.428.166`) also cut the number of moves, so I cannot credit this fix alone. Lessened.
- **`zt5n` (record why):** works. It is the source of most of the measurements here, but it also produced `ifd.428.157`.
- **`ifd.428.151`, `fp3.9`, `ifd.428.164`, `z5f9`, `ifd.428.175`, `ifd.428.176`, `ifd.428.181`, `hnc0`, `t3qs` (11-19 to 11-21):**
  - The scheduler-admitted empty-slot hours did not fall across the week: 1.3, 1.5, 3.3, 4.1, 2.8, 0.9 (11-16 to 11-21).
  - Records counting a slot for "no run on record" went 15, 16, 16, 24, 40, 19 a day.
  - `z5f9` landed 11-20 04:35 UTC, and within hours `ifd.428.176` found the same symptom on the build that carried it.
  - `hnc0` landed 11-21 11:01 PST; `vjbp` records empty-slot gaps at 11:49 and 14:22 PST that afternoon.
  - `t3qs` stopped the marker refusals: none in `watch.jsonl` after 11-21 14:08 UTC.
- **Still happening on 2026-11-21:**
  - The scheduler's count still disagrees with the run records (`vjbp`).
  - A slot with no lease is freed only when the scheduler restarts (`ifd.428.187`, which has had three failed runs).
  - Runs taken up again by four other paths still wait for their own slot (`ifd.428.185`).
  - The health check names the wrong cause (`ifd.428.178`).

**Verdict: partly fixed.** The September class is mostly gone, and visibility is much better. The core November disagreement between the counts is not fixed.

## Was it real

Yes. The episodes above came from the factory's own records, they stopped work, and they reached the operator:
- the capacity raise on 09-21;
- 15 operator's-assistant messages to the Lead PM about idle or held slots between 10-24 and 11-21 (`interventions.jsonl`);
- slot-limit changes the operator made by hand on 11-09, 11-15 and 11-19.

The cost was lost throughput, not money spent. My estimate is about 150-250 slot-hours over two months, at most about 6% of the roughly 4,500 slot-hours available. It is concentrated in four bad stretches: 09-20/21, 09-26/27, 10-25/26 and 11-19 to 11-21.

The split:
- **Observed failures (27):** #1, 3, 4, 6, 7, 8, 9, 10, 12, 13, 15, 16, 17, 19, 20, 22, 23, 25, 26, 27, 28, 32, 33, 34, 36, 37, 38.
- **Might happen, wording, or test gaps (6):** #2 (`ifd.428.5`), #5 (`ifd.428.43`), #11 (`ifd.428.117`, "traced in code, not reproduced"), #14 (`ifd.428.131`, wording), #21 (`ifd.428.158`, test), #35 (`ifd.428.185`, other paths not seen).
- **Observed, but a display or reporting fault rather than a lost slot (5):** #18, #24, #29, #30, #31.

## Where the effort was wasted

- **Patches on a private count (about 9 items, about 11 runs):** #23, #25, #26, #28, #33, #35, #37, #38, plus #20. Each taught the scheduler's own list of starts about one more state in which a run holds no slot. Together they did not stop the symptom. `z5f9` → `ifd.428.176` → `vjbp` is the same bug filed three times in three days.
- **Fixes that produced the next item (4):**
  - `ifd.428.126.2.2` → #15;
  - `zt5n` → #18;
  - `ifd.429.76.3` → the CI-wait cluster;
  - `ifd.433.23` → #34.
  `ifd.429.76.3` is the costly one: it spawned about eight items in five days.
- **Retired, folded or overtaken (2):** #4 was retired when the bounded drain superseded it. #16 took 7 runs over 19 days, and its subject, slots idle during build moves, had largely been overtaken by the three-hour move rule by the time it landed.
- **Never observed (6):** the "might happen" items. #11 (`ifd.428.117`) and #2 are reasonable hardening. #14 and #21 are low value.
- **Reporting fixes for counts that would not disagree if there were one count (3):** #29, #30, #31. They exist only because there are five counts.

What had lasting value: #1, #3, #6, #7, #8, #10, #13 (in part), #17, #22, #34, and probably #11. That is about 11-12 of 38 items, but they carried a larger share of the real effect. My estimate is that 30-45% of the effort produced something lasting.

Cost (estimate):
- 45 developer runs × $3.83 ≈ $172;
- 106 review rounds × $0.93 ≈ $99;
- 36 repair attempts, which may or may not be inside the run cost: $0-140 more.
- Total: about **$270-420**, before the role conversations (program managers, development manager, Lead PM) that diagnosed and admitted these items. Those are not counted and are probably comparable.

## What would have been better

- **One count, tied to a live worker.** A slot should be a lease in the run store, renewed by the process actually running the developer, and lapsing on its own when that process stops. Free slots would then be configured slots minus live leases, read the same way by the scheduler, the health check, status and the dashboard. Pushing, waiting on checks, entering the merge queue, pausing, being refused, or the scheduler restarting would all release the slot by construction, with no case-by-case code. `ifd.428.117` (one atomic reservation) was a step toward this in October. The right move then was to make the reservation the only count and delete the scheduler's private count, not add to it.
- **A stop after the second failed fix.** When `ifd.428.176` showed `z5f9` had not held (11-20), the Lead PM should have stopped admitting symptom items and asked the architect for the single-count design. Instead `hnc0`, `ifd.428.185`, `ifd.428.187`, `vjbp` and `ifd.428.188` were admitted within 30 hours.
- **An audit before changing run states.** `ifd.429.76.3` added a "holds no slot while waiting for CI" state. A required check — every place that counts slots honours the new state, with a test across all five readers — would have prevented about eight items.
- **Fewer items for visibility and sibling paths.** Reviewer-found siblings (#2, #5, #14, #21, #35) could have been folded into the item under review, or declined.
- **Not doing it at all** would have been wrong. The loss was real and reached the operator. The waste is in how it was fixed, not in whether.

## What I could not establish

- **True slot occupancy over time.** Run records hold only their final state, and I did not read the per-run event files or the health observations, which the brief excludes. Lost slot-hours before 11-16 come from item descriptions and gaps in run starts. Runs that wait on checks are invisible in the scheduler log. The 150-250 slot-hour figure is an order-of-magnitude estimate.
- **Whether ready work was always waiting.** Every episode cited records ready work, but I did not check intake holds for each hour.
- **Whether repair attempts are separate paid sessions** beyond the run cost, so the cost range is wide.
- **Who raised a few items:** #3, #4 and #10 say only that the Lead PM admitted them.
- **How much of the 11-21 morning was marker refusals and how much was leaseless counting.** The two overlap, and I could not separate them.
- **`ifd.428.133`'s own effect,** separate from the three-hour build-move rule.
