# yoyodyne-ifd.430.93: what starts the development manager's sessions, what fills her messages, and why some items took five or more reviews

From [the cost-reduction design](../designs/reducing-what-the-factory-costs.md),
sections 3 and 5. Her sessions rose from about 32 a day before November 10 to
48 to 96 a day, her messages averaged 63 KB, and 49 items took five or more
reviews each. This records what caused each, and the fix for each.

Read on November 22, 2026 from the harness's own records for this product: the
development manager's conversation log
(`conversations/chat-9d5975d3dea491428cf7f5882ab19202.events.jsonl`), the spend
record (`spend.jsonl`), and the run records under `runs/`. Days are Pacific
time. Code read at commit b7de3295f.

## In short

1. **Two thirds of her sessions on her busiest day were the harness paging her
   through waits she had already decided.** Every turn of a pass is a provider
   session of its own. From November 10 her docket held more entries than one
   turn shows, almost all of them stoppages she had decided to wait on. The
   harness counted every entry not yet shown as owed, so it kept giving the
   pass another turn, up to its four, after she had said the pass was complete.
   **Fixed in this change.**
2. **Her messages are large because two sections are each about 30 KB, and
   one of them repeats the other.** The docket and the factory health
   observation fill most of the pass's message. About half of the health
   section describes runs the docket in the same message already lists. Behind
   the message, each session also starts from a 171 KB list of work items and
   a second copy of the docket, and both are written to the provider's cache
   again every session. **Named for admission.**
3. **The items with five or more reviews were mostly approved changes run
   again, not changes the reviewer kept refusing.** Before November 8, an
   approved patch was reviewed again each time main moved during its checks.
   From November 10, the merge queue handed approved changes back when they no
   longer merged onto main, and each was run and reviewed again from scratch,
   often several times. The hand-backs also went on her docket, and her
   decisions to wait on them are the waits in cause 1. **Mostly fixed by
   changes that landed November 18 and 21; the rest is named for admission.**

## 1. What started each session, November 18

November 18 was her busiest day: 100 turns.

| What started the turn | Turns | Spend | Average message |
| --- | ---: | ---: | ---: |
| The scheduled pass, every 45 minutes: its first turn | 36 | $57.08 | 85.8 KB |
| A later turn of the same pass, given because docket entries had not been shown, although her account said the pass was complete | 64 | $81.35 | 44.5 KB |
| **All** | **100** | **$139.13** | **57.1 KB** |

There were 97 provider sessions, one for each turn except three that were
asked again for a missing closing block. There were 36 passes against the 32
the schedule gives in a day; the four extra were not traced. Every turn that day
belonged to the scheduled sweep: no other trigger started one, and the single
stopped run delivered on its own, and the three requests for a missing closing
block, arrived inside turns already running.

Every later turn opened with *The pass of "development-manager-sweep" has more
to do than that turn held* and carried the next slice of the docket. Her
replies were of this kind: *Every docket entry in this slice is a wait I
already recorded.* In 29 of the 64, she wrote nothing to the tracker.

The first turn's message that day said, for example, *44 further live docket
entry(s) are not listed here … 0 of them nobody has decided, and 44 are
stoppages you decided to wait on.* By day, across November:

| Day | Turns | First turns | Later turns | Later turns with no tracker write |
| --- | ---: | ---: | ---: | ---: |
| Nov 1 to 9 | 30 to 34 a day | about 30 | 0 to 4 | 0 to 2 |
| Nov 10 | 47 | 28 | 19 | 13 |
| Nov 12 | 70 | 30 | 40 | 26 |
| Nov 16 | 85 | 33 | 52 | 39 |
| Nov 18 | 100 | 36 | 64 | 29 |
| Nov 20 | 56 | 29 | 27 | 7 |
| Nov 21 | 33 | 30 | 3 | 0 |

The rise begins on November 10, the day the unshown entries first appeared, and
it falls away as the docket shrinks back into one turn. It is not caused by the
schedule, which has not changed since September 28 (every 45 minutes, so 32 a
day).

**Why.** `sweepDocketPass.Remaining` (`internal/cli/recurring.go`) counted
every live entry the pass had not shown as owed, including stoppages whose
wait still stands (`triage.Stoppage.Waiting`). Any owed entry marks the pass as
having more to do (`internal/orchestrator/recurring.go`, the loop in
`Trigger.run`), so the harness overrode her "complete" until it had shown
every wait or spent all four turns. This rule came with the docket
continuation change on October 4 (yoyodyne-ifd.430.40), which was right for
undecided entries. The docket itself already says a standing wait "asks
nothing of anybody until it does" run out.

**Fix, made here.** A stoppage whose wait still stands is no longer counted as
owed, unless it is critical. It is still listed where there is room, and once
the wait runs out it is owed again like any other entry. On November 18 this
would have removed most of the 64 later turns, about $80 that day. On a day
like November 21, when the docket fits in one turn, it changes nothing.

## 2. What each message into her conversation holds

**The pass's message, by section, November 18** (104 messages; the first turn
of a pass carries every section, a later turn carries the docket and the
closing instructions again):

| Section | Present in | Average | Total for the day |
| --- | ---: | ---: | ---: |
| Triage docket | 100 | 31.6 KB | 3,159 KB |
| Factory health, observed for this pass | 36 | 31.7 KB | 1,140 KB |
| Work waiting in your conversation | 100 | 5.8 KB | 585 KB |
| Closing instructions (the sweep block and the rules for it) | 100 | 6.1 KB | 612 KB |
| Reports addressed to you | 36 | 5.5 KB | 198 KB |
| Waiting on the operator | 100 | 1.9 KB | 185 KB |
| Product passes failing | 12 | 1.0 KB | 12 KB |
| Opening line, carry-on line, words to correct | — | — | 57 KB |

On November 21, with almost no later turns, the 37 messages averaged 60.8 KB:
health 27.6 KB, docket 24.0 KB, work waiting 6.0 KB, reports 5.5 KB, closing
instructions 6.3 KB.

The Lead Product Manager's messages are about 7 KB because she is given
neither the docket nor the health observation.

- **The health observation repeats the docket.** On November 21 the 30 health
  sections carried 492 conditions (555 KB). 206 of them (264 KB) were about a
  run the docket in the same message already listed, and 214 said she had
  decided to wait. The largest kind was *held after its run stopped*, 11
  conditions in a typical pass and 14 of its 21 KB. The observation is
  rendered whole as one line of JSON, with long *needs* and *says* prose for
  each condition.
- **What each session starts from, beside the message.** A pass on November
  22 started from 437 KB: the instructions (98 KB) and the product intent
  (58.5 KB), which are reused from the provider's cache; then the work items
  (171.3 KB), a second copy of the triage docket (24.1 KB, read when her
  conversation's picture was taken, so older than the copy in the message), one
  line for each design left out (12.3 KB), and smaller listings; then her
  memories, previous account and the message (54.7 KB). Each session's first
  call read about 52,000 tokens from the cache and wrote about 117,000 fresh,
  about $1.00, the same as before the reuse fix (yoyodyne-ifd.430.89). The
  change that is meant to reuse the unchanged work items and docket too
  (yoyodyne-ifd.430.96) reached her on November 22 at 06:19 PST. In the three
  sessions after it, she still read 51,949 tokens and wrote 97,000 to 117,000,
  although the parts before her memories were unchanged from the pass before.

**Fixes named for admission:**

- The health section given to her pass leaves out, or names in one line,
  each condition about a run the docket in the same message lists. That is
  about half of the section.
- A pass's start does not carry a second, older copy of the docket in the
  briefing when the message carries the current one.
- Find why the unchanged work items and docket are still written fresh every
  session after yoyodyne-ifd.430.96, and whether the 171 KB list of work items
  is needed by her pass at all, beside the 6 KB of work waiting in her
  conversation.

## 3. The items that took five or more reviews

From the spend record, November 6 to 20: 1,026 reviews of 374 items. 48 items
took five or more, 331 reviews (32%) costing $347 of $922 spent on review.
The design counted 49 over a slightly different window.

Why each review happened, from the run records' review events for those items
(some runs have no record left, so a few items show fewer events than reviews
in the spend record):

| Why the review happened | Reviews | Share |
| --- | ---: | ---: |
| A new run of an item whose last review had approved, nearly always after the merge queue handed the change back because it no longer merged onto main | 88 | 36% |
| A repair the reviewer asked for | 56 | 23% |
| The first review | 48 | 20% |
| The same approved patch reviewed again on a new main, after main moved during its checks | 32 | 13% |
| The exact same commit on the same base reviewed again, nothing else recorded between | 13 | 5% |
| An approved change whose patch changed afterwards | 5 | 2% |
| A new run after a review that asked for a repair or raised the item | 4 | 2% |

By date, the re-reviews after main moved all fall on November 6 to 8, before
landing went through the merge queue. From November 10 the cause is the new
runs: 52 of the 141 runs behind these items ended when the merge queue handed
an approved change back for a conflict with main. The development manager
then decided a fresh run, which was developed and reviewed again whole, and by
the time it was approved main had often moved again. The files named most in
those conflicts were `internal/orchestrator/README.md` (7 items),
`docs/configuration.md` (5) and `internal/readmodel/standing.go` (5).

| Reviews | Item | Why |
| ---: | --- | --- |
| 17 | A developer run with sub-agents on receives the built-in delegation instruction file,… (yoyodyne-dgsx) | 12 review(s) of the same approved patch after main moved; 2 after the reviewer asked for a repair; 2 of the same commit again |
| 15 | Share the tracker between factories: establish how Beads merges its committed export,… (yoyodyne-ifd.429.106) | 4 new run(s) after an approval (3 run(s) handed back by the merge queue for a conflict with main); 6 review(s) of the same approved patch after main moved; 2 after the reviewer asked for a repair; 1 of the same commit again |
| 12 | MCP calls, piece 2: the small client that speaks MCP over standard input and https, one… (yoyodyne-ifd.435.43) | 1 new run(s) after an approval; 3 review(s) of the same approved patch after main moved; 1 after the reviewer asked for a repair; 3 of the same commit again; 1 after the approved patch changed |
| 12 | A run that cannot reach GitHub at the merge step waits until GitHub answers again, never… (yoyodyne-g2l4) | 6 new run(s) after an approval (4 run(s) handed back by the merge queue for a conflict with main); 3 after the reviewer asked for a repair; 2 run(s) interrupted |
| 12 | A run stopped only because it could not reach GitHub as itself is taken up again by the… (yoyodyne-yrku) | 5 new run(s) after an approval (4 run(s) handed back by the merge queue for a conflict with main); 1 after the reviewer asked for a repair |
| 10 | While a build move waits for running work, a free developer slot with ready work is… (yoyodyne-ifd.428.133) | 3 new run(s) after an approval (2 run(s) handed back by the merge queue for a conflict with main); 1 after the reviewer asked for a repair; 1 after the approved patch changed |
| 9 | Integrating to a local branch, piece 4: the harness brings a waiting item's change… (yoyodyne-ifd.429.146) | 3 new run(s) after an approval (3 run(s) handed back by the merge queue for a conflict with main); 1 after the reviewer asked for a repair; 1 after the approved patch changed |
| 9 | Find why listings of open and ready work still take past 10 seconds with the tracker… (yoyodyne-ifd.428.126.4.24) | 3 new run(s) after an approval; 1 after the reviewer asked for a repair |
| 8 | A check stopped at its time limit reports how long it actually ran and the limit that… (yoyodyne-qzj) | 6 review(s) of the same approved patch after main moved; 1 after the reviewer asked for a repair |
| 8 | MCP calls, piece 7: yoyo mcp calls, counts on status and the dashboard with no content,… (yoyodyne-ifd.435.48) | 3 new run(s) after an approval (2 run(s) handed back by the merge queue for a conflict with main); 4 after the reviewer asked for a repair |
| 8 | A recorded stop on a developer run takes effect promptly, and never cancels a change… (yoyodyne-ifd.429.156) | 2 new run(s) after an approval (2 run(s) handed back by the merge queue for a conflict with main); 4 after the reviewer asked for a repair |
| 7 | Cut v0.6.0: the release since v0.5.0, with notes refreshed from what merged into main,… (yoyodyne-fvn) | 1 new run(s) after an approval; 2 after the reviewer asked for a repair; 1 of the same commit again |
| 7 | An approved change waiting in the harness's merge queue is never stopped as an… (yoyodyne-fo4v) | 1 new run(s) after an approval; 2 after the reviewer asked for a repair; 2 of the same commit again |
| 7 | When GitHub's checks fail only in tests the change does not touch, the harness runs… (yoyodyne-ifd.429.87.7) | 3 new run(s) after an approval (2 run(s) handed back by the merge queue for a conflict with main); 2 after the reviewer asked for a repair; 1 run(s) interrupted |
| 7 | Integrating to a local branch, piece 6: the whole team-branch flow is checked end to end… (yoyodyne-ifd.429.147) | 1 new run(s) after an approval; 2 after the reviewer asked for a repair; 1 run(s) interrupted |
| 7 | Share reports, their handling and directives between factories through a records… (yoyodyne-ifd.429.108) | 4 new run(s) after an approval (4 run(s) handed back by the merge queue for a conflict with main); 1 after the reviewer asked for a repair |
| 7 | A design or goals document whose home is a companion intent repository reaches it… (yoyodyne-ifd.78.2) | 3 new run(s) after an approval (3 run(s) handed back by the merge queue for a conflict with main); 1 after the reviewer asked for a repair |
| 7 | An architect item closes only on a revision of a document the item names, not on any… (yoyodyne-ifd.437.47) | 1 new run(s) after an approval (1 run(s) handed back by the merge queue for a conflict with main); 2 after the reviewer asked for a repair |
| 6 | A team pause shared through the remote (yoyo pause --team), only in team mode, read in… (yoyodyne-ifd.429.105) | 2 new run(s) after an approval (1 run(s) handed back by the merge queue for a conflict with main); 1 after the approved patch changed |
| 6 | After yoyo project bind --replace moves an external configuration to a new clone,… (yoyodyne-ifd.78.4) | 1 after the reviewer asked for a repair; 3 of the same commit again |
| 6 | Integrating to a local branch, piece 3: feedback left on the team's tracked pull request… (yoyodyne-ifd.429.145) | 2 new run(s) after an approval; 1 after the reviewer asked for a repair |
| 6 | When a developer slot is free and ready work is not started, the scheduler records why… (yoyodyne-zt5n) | 1 new run(s) after an approval (1 run(s) handed back by the merge queue for a conflict with main); 3 after the reviewer asked for a repair |
| 6 | An item whose approved change is still in the merge queue is never treated as free to… (yoyodyne-ifd.429.155) | 1 new run(s) after an approval |
| 6 | A review on Codex whose reads report no size is treated as unmeasured, not empty, so the… (yoyodyne-kuo) | 2 new run(s) after an approval (1 run(s) handed back by the merge queue for a conflict with main); 1 run(s) interrupted |
| 6 | The development manager can always release the entry at the head of the harness's merge… (yoyodyne-ifd.429.166) | 2 new run(s) after an approval (1 run(s) handed back by the merge queue for a conflict with main); 2 after the reviewer asked for a repair |
| 6 | Giving an item back clears the assignee the harness's claim set, so finished, stopped or… (yoyodyne-819) | 2 new run(s) after an approval (2 run(s) handed back by the merge queue for a conflict with main); 2 after the reviewer asked for a repair |
| 6 | The CI check 'adoption' is renamed 'getting-started', with its make target, script and… (yoyodyne-9f4) | 3 new run(s) after an approval (3 run(s) handed back by the merge queue for a conflict with main); 1 after the reviewer asked for a repair |
| 6 | The dashboard shows each developer slot's preferred labels wherever it shows developer… (yoyodyne-6u6) | 3 new run(s) after an approval (1 run(s) handed back by the merge queue for a conflict with main) |
| 6 | The harness's merge queue chooses its next entry by priority and age each time it builds… (yoyodyne-ifd.429.183) | 2 new run(s) after an approval; 3 after the reviewer asked for a repair |
| 6 | The scheduler moves onto each new build under the three-hour rule, and a run is never… (yoyodyne-ifd.428.175) | 2 after the reviewer asked for a repair |
| 5 | Give each factory an identity of product id, a machine id kept in the machine's home,… (yoyodyne-ifd.429.104) | 2 review(s) of the same approved patch after main moved; 1 after the reviewer asked for a repair; 1 after the approved patch changed |
| 5 | The weekly check-time figures say 'at least' where they call a figure a floor, in yoyo… (yoyodyne-ifd.428.126.6.7) | 3 review(s) of the same approved patch after main moved; 1 of the same commit again |
| 5 | A release's readiness stamp is taken where the tracker can be read, so a decided release… (yoyodyne-onxb) | 1 new run(s) after an approval; 1 run(s) interrupted |
| 5 | The harness's merge queue notices a candidate's merge on GitHub within minutes and… (yoyodyne-ifd.429.138) | 3 new run(s) after an approval (2 run(s) handed back by the merge queue for a conflict with main); 1 after the reviewer asked for a repair |
| 5 | When main moves, the product moves all its running parts onto the new build promptly, or… (yoyodyne-ifd.434.36) | 1 new run(s) after an approval (1 run(s) handed back by the merge queue for a conflict with main); 1 after the reviewer asked for a repair |
| 5 | An ask from one role to another that refers to a work item is delivered, rather than… (yoyodyne-dfk) | 3 new run(s) after an approval (3 run(s) handed back by the merge queue for a conflict with main) |
| 5 | Scheduled message intake, piece 1: the intake configuration and every refusal when it… (yoyodyne-ifd.435.51) | 2 new run(s) after an approval (2 run(s) handed back by the merge queue for a conflict with main) |
| 5 | Shared records are fetched and pushed with the project's own Git remote settings, so a… (yoyodyne-ifd.429.168) | 1 new run(s) after an approval |
| 5 | A harness step around a run that fails on a passing cause, such as a tracker write… (yoyodyne-ifd.433.59) | 1 new run(s) after an approval |
| 5 | A developer run that started before its project configured slot pairs is given its slot… (yoyodyne-ifd.435.14.4.1) | 2 new run(s) after an approval |
| 5 | A heavy check's finish record that fails to save is reported, not dropped, so no check… (yoyodyne-ifd.429.77) | 1 new run(s) after an approval |
| 5 | A confirmed document whose linked documents were revised after it was confirmed goes… (yoyodyne-fp3.5) | 3 after the reviewer asked for a repair |
| 5 | Harness processes share one tracker client, so listings of open and ready work are read… (yoyodyne-ifd.428.126.4.18) | 2 new run(s) after an approval (1 run(s) handed back by the merge queue for a conflict with main); 1 after the reviewer asked for a repair |
| 5 | Stopped-run recovery slice 1: a stop's cause is decided once when the run stops, and its… (yoyodyne-ifd.428.99.2) | 2 new run(s) after an approval (2 run(s) handed back by the merge queue for a conflict with main) |
| 5 | Find why the scheduler leaves a developer slot unfilled while every run counted in it… (yoyodyne-ifd.428.164) | 1 new run(s) after an approval |
| 5 | When the scheduled check on main fails, it runs the failing tests once more before… (yoyodyne-ifd.429.236) | 1 new run(s) after an approval |
| 5 | Cut v0.7.0: the release since v0.6.0, with notes drafted from what merged into main,… (yoyodyne-jn5n) | 1 new run(s) after an approval; 1 after the reviewer asked for a repair |
| 5 | In team mode, a run whose review round the shared remote will not take shows that it is… (yoyodyne-ifd.429.245) | 2 new run(s) after an approval (1 run(s) handed back by the merge queue for a conflict with main) |

**Causes and fixes:**

- **Approved changes handed back for a conflict were run again from scratch.**
  Already addressed: an approved change handed back because it no longer
  merges onto main gets one attempt of its own to bring itself up to date
  (yoyodyne-ifd.429.174, landed November 18), and that attempt now works when
  its checkout has been cleared (yoyodyne-1tf8, landed November 21). Finding a
  conflict as soon as main moves, rather than when the entry reaches the head
  of the queue, is admitted and open (yoyodyne-ifd.429.229). The new runs fell
  to 2 on November 21. **Named for admission:** most conflicts were in a few
  shared files that nearly every change edits, the orchestrator's code map
  first; a way for those files to take independent additions without
  conflicting would remove much of what is left.
- **The same approved patch was reviewed again after main moved.** This has
  not happened since landing moved to the merge queue on November 8. The rule
  that a moved target earns its review again is an architectural invariant
  (one promotion per target branch), so whether a byte-identical patch could
  keep its review is the architect's to decide.
- **The same commit was reviewed twice with nothing between.** 13 reviews,
  mostly November 6 to 9. Each run's record shows only the reviewer's own
  session between the two reviews. **Named for admission:** find what asked for
  the second review, and keep an approval of a commit and base that have not
  changed.
- **Repairs the reviewer asked for** are the review doing its job, 23% of
  these reviews, and no item took more than four.

## How this was measured

Scripts kept outside the repository read the files named at the top. A turn is
a `session.pass_started` event; its provider sessions are the distinct
`session_id`s of the `run.started` events that follow it; its cost is the sum
of the spend record's `amount_usd` over those sessions; and its kind is read
from the message that opens it and from the status in the last account of the
turn before. A section of a message runs from one heading to the next. For
reviews, each `review.started` and `review.completed` pair in an item's runs
was compared with the review before it: a different run is a new run; the same
head and base commit is the same commit; a previous review that asked for a
repair makes this one a repair; otherwise an unchanged patch size on a new base
is the same patch on a moved main.
