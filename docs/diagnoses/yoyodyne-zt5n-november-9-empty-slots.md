# yoyodyne-zt5n: what the scheduler would have said about the empty slots of November 9

The work that makes the scheduler record why it leaves a developer slot empty
(yoyodyne-zt5n) was asked to say what that record would have said for the empty
slots of November 9, 2026: 18:17 to 19:55 PST, and 20:17 PST until all three
slots were filled at 21:20 PST. This is that account, worked out after the fact
from the harness's own records. All times are PST.

## The records it rests on

- The watch log, `products/yoyodyne/watch.jsonl` in the harness's state
  directory: every line the watch session wrote between 18:00 and 21:45.
- The scheduler's log, `products/yoyodyne/scheduler.log`: what each session
  started and how each ended, and its closing summary.
- The run records under `products/yoyodyne/runs/`: when each run started and
  ended, who started it, and the slot it recorded.
- The project's configuration on that day: three developer slots. Slot 1
  prefers reliability work, slot 2 prefers usability, product and performance
  work, and slot 3 is dedicated to team-development work.

## What the records show

**The scheduler counted every slot as taken.** In neither window did the session
write a line saying it had started nothing. Each time it started work, it wrote
"filled 1 of 1 free developer slot", at 18:17 and again at 20:23. The session
that ran from 18:08 to 19:47 closed with "3 of 3 developer slot(s) taken at the
last pull", and so did the one that ran from 19:48 until its restart after 21:27.

**Only two runs on record held slots for most of the first window:**

- the build-move fix (yoyodyne-ifd.428.133, run-c030729a), in slot 2 from 18:16;
- a run of yoyodyne-fo4v that the operator started by name at 14:58 and that
  ended at 20:17. It records no slot number, so status lists it as running but
  in no numbered slot.

The run in slot 3 (yoyodyne-ifd.429.130, run-26bcb2f0) ended at 18:17. No run on
record took that slot until yoyodyne-ifd.78.7 (run-a26f43a6) started at 19:55.

**The pulls ran slowly, and each one tried the same decided repairs and re-runs.**
The harness recorded refusing a decided repair of run-152784a8 (yoyodyne-ifd.309)
at 18:10, 18:30, 18:55, 19:16, 19:33, 19:50, 20:30, 20:47 and 21:14. Four other
decided repairs (yoyodyne-ifd.434.12, ifd.428.126.4.9, ifd.207 and ifd.187) were
refused alongside it. In the second session, three re-runs the development
manager decided were tried the same way. So pulls came 17 to 40 minutes apart.
While a pull carries out a decided repair or re-run, that work is counted as
holding a slot until the harness has tried it.

**Slot 3 is kept for team-development work.** At 19:55 the scheduler put
yoyodyne-ifd.78.7, a priority 2 item labelled team-development and 369th in the
order, into slot 3. That was ahead of the priority 0 items yoyodyne-ifd.429.117
and yoyodyne-h0ou, 20th and 21st. This is the operating rule of October 28: a
dedicated slot takes its own label's work first. So once team-development work
was ready, slot 3 was never going to take those two items.

**The second window began as yoyodyne-fo4v's run ended at 20:17.** The pull at
20:23 filled one slot, and the next lines in the watch log are at 21:12. At
21:12 a new build (05f69592) was found, and the session began waiting out its
runs before restarting. yoyodyne-ifd.429.105 started in slot 3 at 20:57.
yoyodyne-ifd.429.117 started at 21:19 and yoyodyne-h0ou at 21:20.

## What the new record would have said

- **18:17 to 19:55, slot 3:** most likely *a slot still counted as taken*, naming
  each decided repair or re-run that pull was carrying out. That fits the
  closing count of three against two runs on record. The records cannot confirm
  it: nothing written that night says what the third counted slot was. From
  19:55 the slot held yoyodyne-ifd.78.7, by the dedicated-slot rule, and nothing
  would have been recorded.
- **The slot yoyodyne-fo4v's run held until 20:17:** nothing. A run the operator
  starts by name holds a developer slot like any other, so the slot was not
  empty, only unnumbered.
- **20:17 to 21:20:** the same likely reason, *a slot still counted as taken*,
  for the pulls carrying out decided repairs and re-runs. From 21:12 it would
  have been *a rule keeping work out*: the session was restarting onto build
  05f69592 and pulls no new work while it waits out its runs. The records cannot
  say which item, if any, a slot was counted for between 20:23 and 20:57.

The change has one limit that bears on this. It records once per pull. A pull
held for twenty minutes while it carries out decided repairs and re-runs writes
nothing until that pull ends. So the reason appears in status at the end of
such a pull, not at its start.

## Work this points to

- Find out what held the third counted slot between pulls. The pulls carrying
  out decided repairs and re-runs, which take 17 to 40 minutes and are refused
  every time, are the first place to look.
- Decide what becomes of the decided repairs the harness refuses at every pull
  (yoyodyne-ifd.309, ifd.434.12, ifd.428.126.4.9, ifd.207, ifd.187).
- Consider having a run the operator starts by name record its slot number, so
  status shows which slot it holds.
