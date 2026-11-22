# Self-work review: is the factory wasting its time on itself?

The operator asked on 2026-11-21 for deep dives into a few threads of the factory's work on itself: "what caused them, how many beads did it take to address the issue, was it really fixed? Was is a real issue in the first place?", and for a write-up that "critically considers the possibility that we are wasting time with some or many of these issues". Seven investigators, one thread each, traced seven threads through the tracker, git history and the factory's own logs. Their reports are the seven files beside this one, `yoyodyne-self-work-1.md` to `yoyodyne-self-work-7.md`, listed under "The files" below.

This summary argues the sceptical case, because that was the question asked. "What was worth it" below gives the other side.

## The files

| File | Thread |
|---|---|
| `yoyodyne-self-work-0.md` | this summary |
| `yoyodyne-self-work-1.md` | slow tracker listings |
| `yoyodyne-self-work-2.md` | developer slots left empty or held |
| `yoyodyne-self-work-3.md` | moving onto new builds |
| `yoyodyne-self-work-4.md` | the merge queue |
| `yoyodyne-self-work-5.md` | moving tests and tests that fail under load |
| `yoyodyne-self-work-6.md` | the wording shown to people |
| `yoyodyne-self-work-7.md` | the factory's own documents |

## In short

- **By the investigators' estimates, 30 to 45% of the effort on these seven threads left something lasting.**
- **The problems were mostly real.** Six of the seven began with something that stopped work or reached the operator; the seventh, wording, was partly real.
- **None of the seven is fully fixed.** One, slow tracker listings, is not fixed at all.
- **The waste is in how they were fixed.** Each new symptom got a new item, and a fix that did not hold was followed by another fix, not by a design for the mechanism.
- **Much of the load is self-made.** The factory redeploys itself many times a day, publishes its own documents through the same queue as code, polices its own wording, and filed its own corrections at priority 0 by a constant in the code.

## Thread by thread

| Thread | Items | Landed | Open | Runs | Estimated cost (runs and reviews) | Real problem | Fixed | Lasting value | Verdict |
|---|---|---|---|---|---|---|---|---|---|
| Slow tracker listings | 62 | 39 | 15 | 75 | $380 to $450 | yes | no | 25 to 35% | mostly wasted |
| Developer slots left empty or held | 38 | 24 | 12 | 45 | $270 to $420 | yes | partly | 30 to 45% | partly wasted |
| Moving onto new builds of itself | 55 | 38 | 13 | 71 | $400 to $650 | yes | partly | 30 to 45% | partly wasted |
| The merge queue | 119 | 66 | 43 | 82 | $430 to $1,000 | yes | partly | 35 to 50% | partly wasted |
| Moving tests, and tests that fail under load | 67 | 44 | 19 | 100 | $300 to $450 | yes | partly | 35 to 50% | partly wasted |
| The wording shown to people | 61 | 41 | 18 | 39 | $256 to $450 | partly | partly | 30 to 45% | partly wasted |
| Writing and landing its own documents | 89 | 47 | 36 | 474 | $600 to $2,500 | yes | partly | 30 to 45% | partly wasted |

Threads overlap, so the item counts cannot simply be added. Costs leave out the role conversations that admitted, ranked and discussed these items, which are most of what the factory spends.

## How the effort is lost

**The cause is known and the work goes elsewhere.**
- Slow tracker listings: the cause was on record by 2026-10-14, and the operator raised the remedy, the tracker's server mode, on 2026-10-25. The weekly measurement recorded "dolt is not installed" on 2026-11-05, 11-12 and 11-19, so server mode was never measured, and no item asked for dolt to be installed. It was installed on the operator's word on 2026-11-21. Meanwhile about 14 items shrank the tracker store, a cause the factory's own first measurement had ruled out the same day.
- The merge queue: for its first ten days it landed fewer code changes a day than the setup it replaced, and was bypassed by about 48 hand merges. What made it fast was one decision available before it was switched on: stop re-running GitHub's checks on this machine.

**One thing is built in several places, so each fix covers one.**
- Developer slots: five parts of the system each count a slot as taken in their own way. Every new state a run can be in slipped past one of them and became a new item. One bug was filed three times in three days (`yoyodyne-z5f9`, `yoyodyne-ifd.428.176`, `yoyodyne-vjbp`).

**The fix makes the next bug.**
- A change for slow listings (commit `6b10540dd`) wrote ticket files into the main checkout. Those files refused run starts across the factory through the night of 2026-11-20 (`yoyodyne-t3qs`).
- "Checks on CI" (`yoyodyne-ifd.429.76.3`, landed 2026-11-17) spawned about eight slot items in five days.

**The factory makes its own load.**
- Moves onto a new build: 716 in the scheduler's log, and in 487 of them the wind-down ran out with runs still going. About 40 of that thread's 55 items exist only because the factory moves onto its own output many times a day.
- Documents: from 2026-11-19 to 11-21 documents were more than half of all landings, 103 against 86. The V1 harness design landed ten times in two days.
- Wording: 36 of 61 wording items came from the factory's own audits and reviewers, with no record of a person misreading anything.

**Nobody measures whether it worked.**
- Moving tests: six groups moved 251 tests, and the package's run time changed by amounts within noise. What fixed the package's time limit was one item that split its race run in three.
- The weekly tracker measurement checked an idle copy and twice reported that the limit had held, while 30 to 40% of live listings ran over it.

**Priority is spent on the wrong things.**
- `internal/chat/correction.go:34` admitted every correction for a broken standing goal at priority 0, which produced nine priority 0 rewordings. The Lead Product Manager admitted the fix as `yoyodyne-ifd.428.191` on 2026-11-21.

## What was worth it

- **The problems were not imaginary.** Slots stood empty for an estimated 150 to 250 slot-hours. Listings stalled passes and runs. In early October only 2 of 41 document publications succeeded.
- **Some fixes held.** The separate route for documents took publication from 2 in 41 to 87% succeeding. The September fixes that settle dead runs held. Moving at most every three hours cut moves from as many as 24 a day to about 5.
- **Instrumentation paid for itself.** Recording every tracker call is what finally let one item name the cause of slow listings.
- **The wording work the operator asked for mostly held.** 25 of the 61 wording items trace to his own complaints or directions.
- **Doing nothing would have been wrong.** None of the investigators said the factory-flow threads should have been left alone.

## What the case studies point to

- **A stop after a fix that has not held.** The Lead Product Manager adopted this into her admission practice on 2026-11-21: when a fix for a mechanism has not held once, the next item is the architect's design for that mechanism, not another patch.
- **No fix closes without its measure.** An item that fixes a symptom names how the symptom will be counted.
- **One design per root cause:** server mode for the tracker; one count of slots tied to a live worker; holding new work when a build move is due and moving far less often; keeping documents out of code's way in the queue.
- **Stop what has shown no benefit:** moving tests until a benefit is shown; shrinking the tracker store as a cure for slow listings; publishing designs for work that is not scheduled.

## How it was done

- **Read on:** 2026-11-21, between 18:44 and 19:00 PST, by seven read-only investigators on Opus 5.5 in the operator's assistant's session, each told to be a sceptical auditor.
- **Checked by hand:** the operator's assistant checked the move counts, the three weekly measurements naming the missing program, the note of 2026-10-25, the document and code landing counts, the ten landings of the V1 design, the priority 0 constant, and the rate of code items landed per day around the merge queue. Every other figure is the investigators' own.
- **Treat with care:** the lasting-value shares and verdicts are judgements; the threads were chosen, not sampled, and are the large and troubled ones; each report lists what it could not establish at its end.
