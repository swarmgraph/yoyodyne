# Self-work review, case 3: moving the factory onto new builds of itself

One of seven case studies of work the factory did on itself, made at the operator's request of 2026-11-21: "Can you do a deep dive into a few of the factory flow items-- what caused them, how many beads did it take to address the issue, was it really fixed? Was is a real issue in the first place?", written up to "critically consider the possibility that we are wasting time with some or many of these issues". The summary of all seven is `yoyodyne-self-work-review-2026-11-21-0-summary.md` beside this file.

- **Read on:** 2026-11-21, between 18:44 and 19:00 PST.
- **Read by:** a read-only investigator on Opus 5.5 in the operator's assistant's session, told to be a sceptical auditor. It read the tracker export, git history and the factory's own logs and run records, and ran nothing.
- **Checked by hand:** the operator's assistant checked the 716 stops for a move onto a new build in the scheduler's log, and the 487 in which the wind-down ran out with runs still going; the move cadence since the three-hour rule. Every other figure is the investigator's own.
- **How to use it:** evidence for the Lead Product Manager and the architect, not a ruling. The share of effort with lasting value and the verdict are the investigator's judgement. Costs are estimates for developer runs and reviews at mean prices, and leave out role conversations.

The investigator's summary of its findings:

```
thread: Moving the factory onto a new build of itself
items_total: 55
items_landed: 38
items_retired_undone: 3
items_open: 13
first_item: 2026-08-31
latest_item: 2026-11-21
runs: 71 developer runs recorded against these items (108 review rounds)
estimated_cost_usd: 400-650 (developer runs and reviews only; role conversations not counted)
fixed: partly
real_problem: yes
share_of_effort_with_lasting_value: 30-45%
verdict: partly wasted
confidence: medium
```

## In short

- From 1 September the scheduler moved onto a new build after almost every landing: 716 moves in 82 days, up to 24 in one day. Landings came about hourly, which is less time than a run takes, so almost every move found work running.
- Since the 15-minute wind-down limit arrived on 28 September, 98% of moves (487 of 498) used up the full limit and stopped running work: 844 run interruptions in all, about 3.5 hours a day of winding down, and at least eight stalls that each cost 20 minutes to several hours.
- 55 items treated the symptoms. They swung back and forth between "wait for the work" and "cut it off and resume it". 31 exist only because an earlier item in the chain did not finish the job.
- The obvious lever, moving less often, arrived on 19 November (`ifd.428.166`, 11 weeks in). It cut moves from about 8-24 a day to about 5, but each move still cuts off about 1.8 runs. Within two days it had produced four follow-up items of its own.
- About 40 of the 55 items exist only because the factory moves onto its own output many times a day while work is in flight. "Hold intake when a move is due and move when empty, at most once a day" would have made most of them unnecessary.

## What went wrong

What someone watching the factory saw, in order:

1. **August: parts running old builds.** The scheduler ran a binary that predated fixes already on main for six days (24 runs), which produced "phantom" warnings and misdiagnoses (`ifd.213`). The operator kept getting a status line saying the watch session was "43 harness changes old" and that the next move was his (`ifd.224`). The dashboard and Slack service stalled for 31 hours and 20 hours on run-record fields their old builds could not read (`ifd.428.9`). The dashboard ran a day and a half without a section the operator was waiting for (`ifd.434.3`).
2. **September: the self-move wedges or waits.** The self-redeploy from `ifd.230` (landed 1 Sep) waited for the runs it hosted to finish. On 5 Sep it went silent for an hour and ignored SIGTERM (`ifd.288`). On 19 Sep it did nothing for over two hours behind one run's checks, missing two management sweeps (`ifd.398`). On 26 Sep two of three slots sat idle for over an hour during a move (`ifd.428.34`).
3. **28 Sep onward: moves cut work off.** `ifd.398` capped the wait at 15 minutes. From then on, nearly every move ran out the 15 minutes and stopped what was running:

| Period (PST) | Scheduler moves | Per day | Run interruptions | Per move | Moves that ran out the 15-min limit | Winding down, hours/day |
|---|---|---|---|---|---|---|
| 1-27 Sep (wait with no limit) | 218 | 8.1 | 0 recorded | 0 | n/a | not recorded |
| 28 Sep-16 Oct (15-min limit) | 210 | 11.1 | 458 | 2.2 | 206 | 3.5 |
| 17 Oct-17 Nov (after `fbt`) | 242 | 7.6 | 315 | 1.3 | 238 | 3.5 |
| 18 Nov (`.153`/`.156` landed) | 28 | 22.9 | 38 | 1.4 | 27 | 9.7 |
| 19 Nov 05:18 PST-21 Nov (three-hour rule) | 18 | 5.2 | 33 | 1.8 | 16 | 2.0 |

   Source: `watch.jsonl`, counting `stopped` records whose reason is "a build was deployed over the one this session was started from, and the session is restarting into it". The run count comes from "with N run(s) still going, which were stopped and preserved". The wind-down time runs from the "since" stamp in the drain records to the stop. Stopped runs were mostly "preserved" and picked up again, so an interruption usually cost lost check time, not lost work. Some were lost or left for a person, though: `ifd.428.153` (work had to be redone), `ifd.428.163` (three runs stopped as failures), `ifd.428.183` (three runs cancelled at 12:59 PST on 20 Nov and still unstarted 77 minutes later).
4. **Stalls with a stated length** (from the item texts; I matched these to `watch.jsonl` where the times allow): about 40 minutes with all developer runs stopped on 5 Oct (`ifd.428.77`). A 12-hour hold on new work on 18 Oct, set off by runs paused for a move (`ifd.432.25.6`). A run lost for an hour on 23 Oct (`ifd.428.111`). The parts left on an old build for 90 minutes on 30 Oct (`ifd.434.36`). A slot idle for 54 minutes with 231 items ready on 31 Oct (`ifd.428.133`). The merge queue stopped with 15 entries waiting after a push was cut off on 10 Nov (`ifd.429.148`). The queue held for 4 hours by a check relaunched after a restart with no limit on 12 Nov (`ifd.429.170`). New work stopped for at least 19 minutes on 18 Nov (`ifd.428.169`). Two of three slots idle on 19 Nov (`ifd.428.175`). A full stall from about 02:23 to about 04:40 PST on 21 Nov, with the scheduler not moving (`yoyodyne-i84d`, together with a slot-accounting fault).
5. **False alarms reached the operator.** The Slack line "The watch session ... is running a build from before 27 harness changes landed ... Next: the operator's" was wrong in every part on 20 Nov: everything was already on that build, and the 27 commits were documents. It reached him about nine times in 30 hours (`ifd.431.13`). After that fix it still posts to the channel at every due move (`ifd.431.14`, open). The health check also reported "missed" moves that were the three-hour rule working as designed (`ifd.428.177`).

**Today (21 Nov, PST):** the scheduler moved at 02:14, 05:36, 08:48, 12:15 and 15:37. The last was forced by the supervisor after the wind-down overran by 5 minutes 7 seconds. Two of the four ordinary moves ran out the 15-minute limit. The supervisor has forced a stuck scheduler to restart seven times since 20 Nov 18:25 PST. The supervisor, dashboard and Slack service are not under the three-hour rule. They still move on every code landing: the binary was rebuilt 33 times on 18 Nov and on 19 Nov, 28 on 20 Nov and 18 on 21 Nov (`supervisor.log`).

## What caused it

The root cause is a design choice that was there from the start of the self-redeploy, not a bug any one item added:

- **A move on every landing.** From `ifd.224`/`ifd.230` (1 Sep) the scheduler restarted into every new `bin/yoyo`. From `ifd.434.3` (29 Sep) the supervisor did the same for every other part. The rebuild was triggered by each harness code landing, first by the operator's launchd job and then by the supervisor (`ifd.434.9`).
- **Landings came closer together than runs last.** Landings arrived about hourly, and 8-33 times a day once the merge queue was running. A run lasts longer: checks take 12-45 minutes and the race check alone took up to 93 minutes under load. "Wait until idle" therefore never finishes without holding intake, and "cut off after 15 minutes" cuts something off almost every time. `ifd.224` itself stated the first half: with two slots and a deep backlog, "an idle window" never comes.
- **Run state lives in the process that hosts it.** A run's provider subprocess, its tracker writes, Git commands and merge-queue pushes all belong to the scheduler process. Every move therefore had to be made safe step by step: checks (`xtx`, `fbt`, `ifd.428.101`, `ifd.429.136`), pushes (`ifd.429.148`), tracker writes and Git steps (`ifd.428.163`), promotions (`ifd.428.183`), GitHub waits (`ifd.428.138`, `hnc0`), scheduled passes (`udpl`), and slot and lease stamps (`ifd.428.129`, `i84d`).
- **Parts moved at different times.** Some items come from the gap between the configuration or record format on main and the build a part is running: `ifd.428.9`, `dnyy`, `ifd.434.45`, `y40x`, `ifd.434.19`, `ifd.434.25`, `ifd.434.31`. Some of these the three-hour rule makes more likely, not less: the scheduler now lags main by up to three hours.

Earlier items in this thread introduced several later problems:

- `ifd.230` introduced the wedge (`ifd.288`).
- The unlimited wait from `ifd.288` caused the two-hour silence (`ifd.398`).
- The 15-minute limit from `ifd.398` started the run interruptions (`ifd.428.55`).
- `ifd.428.64` reversed `ifd.428.55`, which led to `xtx` and `fbt`.
- `ifd.428.169`'s forced restart cut off promotions (`ifd.428.183`).
- `ifd.428.166`'s whole-hour interval lines up with the Slack sink's hourly check (`ifd.431.14`) and produced false "missed move" reports (`ifd.428.175` in part, then `ifd.428.177`).

## The chain of items

"Seen" means a failure that actually happened. "Might" means a hypothetical case, a code-reading finding, or a wording fix. Run counts are developer runs from `runs/run-*.json`. Rev = review rounds; rep = repair attempts.

| # | Item | Filed | Raised by | Outcome | Runs / rev / rep | Seen or might | Exists because an earlier item fell short? |
|---|---|---|---|---|---|---|---|
| 1 | `ifd.213` every run pins the build it ran | 08-31 | operator's assistant (retrospective) | landed 08-31 | 1/2/1 | seen: stale binary for 6 days | |
| 2 | `ifd.224` watch redeploys itself between runs | 09-01 | operator | superseded by 230 (its reviewed change carried over) | 1 failed/5/3, $84 | seen | |
| 3 | `ifd.230` land the self-redeploy, replayed onto main | 09-01 | operator | landed 09-01 | 1/3/1 | seen | yes: 224 hit a merge conflict and its review cap |
| 4 | `ifd.288` self-redeploy cannot wedge | 09-05 | operator | landed 09-15 | 2/3/1 | seen: 1-hour wedge | yes: 230 |
| 5 | `ifd.398` draining session keeps pulling; wait limited | 09-19 | operator's assistant | landed 09-28 | 4 (3 failed)/10/2 | seen: 2-hour silence | yes: 288's unlimited wait |
| 6 | `ifd.428.9` long-lived readers tolerate new fields | 09-23 | operator's assistant | landed 09-23 | 1/2/0 | seen: 3 stalls, up to 31 h | |
| 7 | `ifd.428.34` move does not idle slots | 09-26 | operator | retired 09-28 (superseded by 398) | 2 cancelled | seen | |
| 8 | `ifd.434.3` supervisor moves every part | 09-26 | operator's assistant | landed 09-29 | 1/3/1 | seen: dashboard 1.5 days stale | |
| 9 | `ifd.434.9` retire the operator's launchd job | 09-27 | developer | landed 09-27 | 1/2/0 | seen | |
| 10 | `ifd.428.55` wind-down waits out a check stage | 09-29 | program manager | landed 09-29 | 1/1/0 | seen: 3 stops | yes: 398's limit |
| 11 | `ifd.428.64` that wait capped at the wind-down limit | 10-01 | developer (code reading) | landed 10-03 | 14 (13 cancelled)/2/2 | might: 5-hour wait in theory | yes: reverses 55 |
| 12 | `9j7` recover one run stopped by a move | 10-02 | operator | closed 10-14 by the development manager, no code | 0 | seen | |
| 13 | `06m` development manager can recover runs stopped by moves | 10-02 | operator | landed 10-17 | 2/7/4 | seen | |
| 14 | `ifd.434.19` one copy of each service after a move | 10-05 | program manager | landed 10-08 | 1/2/0 | seen: 7 stale Slack copies | yes: 434.3 |
| 15 | `ifd.428.77` wind-down deadlock | 10-05 | operator | landed 10-06 | 1/2/1 | seen: 40-min stall | yes: 398 |
| 16 | `ifd.428.80` waiting at promotion not reported as stuck | 10-06 | reviewer of 77 | landed 10-07 | 1/5/1 | might: predicted false alarm | yes: 77 |
| 17 | `ifd.429.70` flaky test of the redeploy wait | 10-06 | developer | landed 10-10 | 1/2/1 | seen (test suite) | yes: 398's tests |
| 18 | `xtx` keep finished checks across a move | 10-11 | program manager | landed 10-12 | 2/4/4 | seen: 11+ runs | yes: 64's ruling |
| 19 | `fbt` a move never starts a check over | 10-11 | operator | landed 10-17 | 1/3/2 | seen: 5 document landings | yes: xtx |
| 20 | `ifd.434.25` supervisor stops its own old dashboard | 10-16 | operator's assistant | landed 10-17 | 1/2/1 | seen: 3rd handover done by hand | yes: 434.3 |
| 21 | `ifd.434.26` Slack-started landing across a move | 10-17 | Lead PM (from reviewer) | open, P3 | 0 | might | yes: fbt |
| 22 | `ifd.428.101` check still restarted after `fbt` | 10-17 | Lead PM | landed 10-17 | 1/3/1 | seen | yes: fbt |
| 23 | `ifd.432.25.6` pauses for a move don't trip the hold on new work | 10-18 | development manager | landed 10-19 | 1/2/0 | seen: 12-hour hold | |
| 24 | `ifd.434.31` health view build wording | 10-21 | program manager | open, P2 | 0 | might (wording) | |
| 25 | `udpl` pass cut off by a restart is retried | 10-21 | development manager | landed 10-22 | 1/3/0 | seen | |
| 26 | `ifd.428.111` run in flight carried on by the new build | 10-23 | program manager | landed 11-09 | 1/1/0 | seen: run lost for 1 h | |
| 27 | `ifd.428.129` stale lease stamps after a restart | 10-27 | architect | open, P2 | 0 | might | |
| 28 | `ifd.434.36` parts move promptly when main moves | 10-30 | development manager | landed 11-15 | 2/3/2 | seen: 90 min on old build | yes: 434.3 |
| 29 | `ifd.428.133` free slot filled during a move | 10-31 | development manager | landed 11-19 | 7 (3 failed, 2 cancelled)/6/1 | seen: 54 min idle | yes: 398 |
| 30 | `ifd.428.135` workflow shown abandoned after a move | 11-03 | development manager | retired 11-15 (merged into 428.147) | 0 | might: "cost nothing yet" | |
| 31 | `ifd.428.138` GitHub wait survives a restart | 11-09 | developer | open, P2 | 0 | might | |
| 32 | `ifd.429.136` queue keeps passed checks across a restart | 11-09 | operator's assistant | landed 11-12 | 1/2/1 | seen: 40 min lost | |
| 33 | `ifd.429.148` queue push cut off by a restart | 11-10 | operator's assistant | closed 11-12 (operator merged it by hand) | 1 failed/2/1 | seen: queue stopped, 15 entries | |
| 34 | `ifd.429.170` relaunched check held to its limit | 11-13 | operator's assistant | landed 11-16 | 2/1/0 | seen: queue held 4 h | |
| 35 | `dnyy` older build loads newer config | 11-17 | program manager | landed 11-17 | 1/1/0 | seen: scheduler could not pull | |
| 36 | `ifd.434.45` the same for other projects | 11-17 | developer | open, P2 | 0 | might | yes: dnyy |
| 37 | `y40x` docs for `dnyy` | 11-17 | Lead PM (from reviewer) | open, P2 | 0 | might (wording) | yes: dnyy |
| 38 | `ifd.428.153` a move never cancels a working run | 11-17 | program manager | landed 11-18 | 1/2/1 | seen, once | |
| 39 | `ifd.428.156` a move does not cut off the queue's check | 11-18 | operator's assistant | landed 11-18 | 2/3/1 | seen: 11 moves that day | |
| 40 | `ifd.428.161` architect: moves within 30 min a day | 11-18 | program manager | design landed 11-18 | 0 (architect pass) | seen | yes: 156 |
| 41 | `ifd.428.163` harness steps finish, or a cut-off counts as a restart | 11-18 | program manager | landed 11-21 | 2/2/1 | seen: 3 runs | |
| 42 | `ifd.428.166` at most one move every three hours | 11-19 | Lead PM (design piece 1) | landed 11-19 | 1/3/2 | seen | yes: 161 |
| 43 | `ifd.428.167` a due move waits for the queue's check | 11-19 | Lead PM (piece 2) | open, P1 | 0 | might, from one observed cut-off | yes: 161 |
| 44 | `ifd.428.168` hold new work only during the restart | 11-19 | Lead PM (piece 3) | open, P1 | 0 | might | yes: 161 |
| 45 | `ifd.428.169` overrun wind-down is forced to restart | 11-19 | program manager | landed 11-19 | 2/2/0 | seen: 19+ min | yes: 166 |
| 46 | `ifd.428.175` scheduler misses moves under the three-hour rule | 11-20 | development manager | landed 11-20 | 1/3/2 | partly a false alarm (per 177) | yes: 166, 169 |
| 47 | `ifd.428.177` health check misreports a "missed" move | 11-20 | developer | open, P1 | 0 | seen (false alarm) | yes: 166, 175 |
| 48 | `ifd.428.181` continuation started while a move is pending | 11-20 | development manager | landed 11-21 | 2/2/1 | seen: 3 times in a day | yes: 153, 175 |
| 49 | `ifd.428.183` promotion cut off by the forced restart | 11-20 | developer | open, P1 | 0 | seen: 3 runs cancelled | yes: 163, 169 |
| 50 | `ifd.428.184` operator's stop keeps runs | 11-21 | operator (another project's report) | open, P1 | 0 | might, here | |
| 51 | `ifd.434.52` supervisor force-stops a hung scheduler | 11-21 | developer | open, P1 | 0 | might: "no hang yet shown" | yes: i84d |
| 52 | `ifd.431.13` stale-build warning false alarms | 11-20 | operator | landed 11-21 | 1/1/0 | seen: about 9 in 30 h to the operator | |
| 53 | `ifd.431.14` stale-build line at every due move | 11-21 | operator follow-up | open, P1 | 0 | seen (channel posts) | yes: 431.13, 166 |
| 54 | `i84d` scheduler did not move on 21 Nov | 11-21 | program manager | landed 11-21 | 1/2/1 | seen: full stall | yes: 169, 175 |
| 55 | `hnc0` run stranded after a restart is taken up first | 11-20 | program manager | landed 11-21 | 1/4/2 | seen | yes: 428.170 |

Counts:

- **Outcomes:** 55 items. 38 landed (37 code changes and one design ruling). One closed with no change (`9j7`). 3 retired without their own work landing (`ifd.224`, superseded by 230; `ifd.428.34`; `ifd.428.135`). 13 still open.
- **Runs:** 71 developer runs and 108 review rounds against these items. `ifd.428.64` alone took 14 runs, 13 of them cancelled.
- **Follow-ons:** 31 items are marked as existing because an earlier item fell short.
- **Ownership:** the program manager, development manager and developers raised 27 of the 55. The rest came from the operator or his assistant, or from the Lead PM acting on design pieces.

Related items left out of the count:

- `ifd.428.187`: slot held without a lease, in progress. It is part of the 21 Nov stall, but not about moves.
- `ifd.434.27`: restart on a configuration change.
- About 20 items whose titles say "rebuild" but mean redoing a change on current main.

## Was it fixed

Fix by fix, against `watch.jsonl` and `supervisor.log` after each one landed:

- **Wedge and silence (`ifd.288`, `ifd.398`, `ifd.428.77`):** these held. No silent wedge recurs in the records after 28 Sep. The price was that the limit turned waiting into interrupting: 0 run interruptions recorded before 28 Sep, 458 in the next 19 days.
- **Interrupted checks (`ifd.428.55` → `ifd.428.64` → `xtx` → `fbt` → `ifd.428.101` → `ifd.429.136` → `ifd.428.156` → `ifd.428.167`, still open):** these partly held. Interruptions per move fell from 2.2 to 1.3 after `fbt`. Wind-down time per move rose from about 15 to about 25 minutes, because moves now wait for checks. Moves still cut off checks: 52 such records from 17 Oct to 17 Nov, 3 since the three-hour rule, one of them today at 05:36 PST. The thread's ruling flipped at least four times between "wait for the check" and "cut it off at 15 minutes".
- **Parts left on old builds (`ifd.434.3`, `ifd.434.36`, `ifd.428.169`, `ifd.428.175`, `i84d`):** not held as a class. The scheduler failed to move or hung in its wind-down on 18, 19, 20 and 21 Nov. The forced restart from `ifd.428.169` works, but needed seven times since 20 Nov. Each forced restart cuts off whatever is running, which is the next open item (`ifd.428.183`).
- **Number of moves (`ifd.428.166`):** held. Scheduler moves fell from 7.6 a day (Oct-Nov) and 22.9 (18 Nov) to 5.2 a day. Wind-down time fell to about 2 hours a day. That is still four times the architect's 30-minute target (`ifd.428.161`). Per-move cost did not fall: 16 of 18 moves since then ran out the limit, cutting off 1.8 runs on average.
- **False alarms (`ifd.428.80`, `ifd.431.13`, `ifd.428.177`, `ifd.431.14`):** partly held. Direct messages to the operator have stopped. Channel posts continue at each due move, and the "missed move" misreport is still open.
- **Version skew (`ifd.428.9`, `dnyy`):** held for this repository. No reader stall or configuration refusal recurs in the records I read.

Overall: the dramatic failures (wedges, multi-hour silences) were fixed early. The daily cost (about 2 hours of winding down and about 9 run interruptions a day, plus a forced restart most days) continues, and 13 items are open.

## Was it real

Yes, mostly. This thread is not found by reading code. The state files show 844 run interruptions, 716 moves and several hours of winding down every day. The operator saw the problem directly: he complained in August, filed `ifd.224`, `ifd.428.34`, `ifd.288` and `fbt` himself, and complained again on 20 Nov about the Slack line. Stalls that stopped all developer work happened on at least 5 Oct, 18 Oct (the 12-hour hold), 10 Nov (merge queue), 12 Nov (merge queue), 18 Nov and 21 Nov.

Split:

- **Seen (about 41 items):** an observed failure, though several were seen only once. `ifd.428.153`, `ifd.428.163` and `ifd.428.169` were each admitted after a single occurrence.
- **Might (about 14 items):** `ifd.428.64`, `ifd.428.80`, `ifd.434.26`, `ifd.434.31`, `ifd.428.129`, `ifd.428.135`, `ifd.428.138`, `ifd.434.45`, `y40x`, `ifd.428.167`, `ifd.428.168`, `ifd.428.184`, `ifd.434.52`, and in part `ifd.428.175`. Its own fixer found it was mostly the three-hour rule working as designed, reported as a fault, and "a priority 0 item was admitted partly on that basis" (`ifd.428.177`).

Cost to the work: an interruption usually kept the run and resumed it. Its cost was mainly machine time spent re-running checks and slot time spent idle during the wind-down. I could not put a dollar figure on the 844 interruptions.

## Where the effort was wasted

- **Treating symptoms one by one instead of the move frequency.** The move frequency was named as the cost driver repeatedly: `ifd.428.9` ("the binary is rebuilt many times a day"), `xtx` ("deploys every 70 minutes"), `fbt`, `ifd.428.55` ("a build is deployed on nearly every landing"), `ifd.429.136`, `ifd.428.138`. Each time, the remedy chosen was to make one more step survive a move. Moving less often was first ruled on with `ifd.428.161` on 18 Nov. By then about 40 items had been filed.
- **Flip-flopping on wait versus cut.** `ifd.428.55` waited for checks. Two days later `ifd.428.64` capped that wait, from a hypothetical five-hour case, at a cost of 14 runs, 13 of them cancelled. That cap made `xtx` necessary. `fbt` then went back to waiting up to the check's own limit, `ifd.428.101` found `fbt` had not held, and `ifd.428.167` again proposes waiting for the queue's check. About 7 items and roughly 25 runs went round this loop.
- **Fixes that produced the next item.** 230→288→398→55→64→xtx→fbt→101. 166→175→177. 166→431.14. 169→183. 431.13→431.14. dnyy→434.45 and y40x.
- **Duplicate or overtaken items:** `ifd.428.34` (overtaken by 398), `ifd.428.135` (merged into another item), `ifd.224` (redone as 230 after a conflict with an unrelated sweep, at a cost of $84 for the failed run). `9j7` and `06m` built recovery tooling for runs stopped by moves, which existed only because moves stopped runs.
- **False-alarm work:** `ifd.428.80`, `ifd.428.175` (in part), `ifd.428.177`, `ifd.431.13`, `ifd.431.14`. The monitoring of moves generated about as much work as some of the move failures.
- **Guards for cases never seen:** `ifd.434.52`, `ifd.428.184` (another project's case), `ifd.434.45` (other projects), `ifd.428.129`, `ifd.434.26`.

**Estimate.**

- **Cost:** 71 developer runs. At the brief's means ($3.83 a run plus two reviews at $0.93) that is about $404. The factory's own per-item cost records add up to $588. I put the range at $400-650, before role conversations and the machine time the interruptions spent re-running checks.
- **What has lasting value:**
  - Build pinning in records (`ifd.213`)
  - Tolerant readers (`ifd.428.9`)
  - The supervisor moving every part (`ifd.434.3`, `ifd.434.9`, `ifd.434.19`, `ifd.434.25`)
  - The wind-down limit and deadlock fix (`ifd.398`, `ifd.428.77`)
  - Keeping passed checks (`xtx`, `ifd.429.136`)
  - Resuming runs across any restart (`ifd.428.111`, `hnc0`), which also protects against crashes and machine restarts
  - Older builds loading newer configuration (`dnyy`)
  - The three-hour rule (`ifd.428.166`)
  - The forced restart (`ifd.428.169`)

  That is roughly 15 items. The rest went into workarounds that a simpler policy would make unnecessary, false alarms, or the wait-versus-cut loop. Lasting value is 30-45% of the effort.

## What would have been better

**The squarely-asked question.** About 40 of the 55 items exist only because the factory moves onto its own output many times a day while work is in flight. They come in three groups:

- **Work cut off by moves (about 30 items):** checks, pushes, tracker writes, promotions, GitHub waits, passes, the hold on new work.
- **Throttling and forcing the move (about 6):** the three-hour rule's pieces, the wind-down force, missed moves.
- **Alarms about the gap (about 5).**

An ordinary service deploys perhaps daily, in a quiet window, and drains first. The factory triggered a move on every landing. Its landings (about hourly) came faster than its runs finish, so the two collided by construction.

A simpler policy would have removed most of these items: when a move is due, stop pulling new work and move once the hosted runs finish, at most once a day, plus an operator command for an urgent fix. Its cost is one wind-down a day. September shows that can take up to two hours behind a slow check; with the check limit in place, call it 20-60 minutes of reduced capacity once a day. Today's cost is about 2 hours a day plus about 9 interruptions, and before 19 Nov it was 3.5 hours a day. It would not remove everything:

- **Version skew gets worse, not better, with rarer moves** (`dnyy`, `ifd.428.9`). The configuration lives in the repository and changes on landing, so it would need either tolerant readers (done) or the running build reading its configuration as of its own commit.
- **Resuming runs after a crash or machine restart** (`ifd.428.111`, `hnc0`, `ifd.428.184`) is worth having anyway, but at low priority. Those restarts are rare.
- **The real pressure for frequent moves is legitimate.** Roles and the operator want a landed fix in effect quickly (`ifd.434.36`: "every fix that has landed is not in effect"), and in August a stale binary caused a week of misdiagnosis (`ifd.213`). The answer to that is the build stamp in every record, which `ifd.213` provides, plus an on-demand move for a specific urgent fix. It is not a move on every landing.

Specific to this thread:

1. Stop after the second failed fix in a row on the same mechanism (`ifd.288` after `ifd.230`, or `ifd.428.55` after `ifd.398`). Ask the architect then, not on 18 Nov, how often the factory should move, not how each step survives a move.
2. Do not reverse a ruling from a hypothetical case: `ifd.428.64`'s five-hour wait had not happened.
3. Keep the move policy and the stale-build warning in one place, so the warning reads the scheduler's stated next move instead of guessing (`ifd.428.177`, `ifd.431.14`).
4. Put the supervisor, dashboard and Slack service under the same daily or idle rule. They still restart up to 33 times a day.

## What I could not establish

- Before 28 Sep, `watch.jsonl` did not record wind-down start times, so I could not measure how long September moves waited, beyond the cases the items describe.
- How many of the 844 interruptions ended in lost work, a run left for a person, or a clean resume. `stops` in the run records labels most of them only "unknown / outside". I counted interruptions, not losses.
- The machine time and money spent re-running checks after interruptions.
- The cost of role conversations (program manager post-mortems, Lead PM admissions, architect passes) spent on this thread, which is probably large given that 27 items were raised by roles.
- How many stale-build Slack messages were sent in total. I was not allowed to read `slack/`, and relied on `ifd.431.13`'s figure of about nine in 30 hours.
- Whether each landed fix's build was actually running when the next failure happened. `ifd.428.101` and `ifd.428.175` both had to ask this, and the records answer it only case by case.
- Where a run count of 0 is recorded for an open item, it simply has not run yet. `ifd.428.64`'s recorded cost of $0 over 14 runs is clearly incomplete.
