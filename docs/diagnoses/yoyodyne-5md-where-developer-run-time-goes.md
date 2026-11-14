# yoyodyne-5md: where developer runs' elapsed time goes

This note covers the item to measure where developer runs' elapsed time goes
(yoyodyne-5md). Its answer: **86 per cent of a developer run's elapsed time is
spent after review approves the change, waiting in the harness's merge queue.**
Writing code, checking it and reviewing it come to 14 per cent all together.
The queue checks one candidate at a time, and each one takes about 81 minutes.
That is about ten landings a day, against about 27 admissions a day. Nothing
in this note changes a limit, a setting or a check, and no test suite was run
to produce it.

All times are PST.

## How it was measured

The figures come from the product's state directory, read only, by a scratch
script that is not part of this change. The window is the seven days before
this run started: November 6, 16:04 to November 13, 16:04. A run is counted if
its developer run ended in that window. That gives 175 runs.

These records were read:

- each run's record and its event log: developer sessions, every tool call
  with its start and end, each check the harness ran with its result, review
  start and end with the decision, and the check stage's machine load and
  permit wait;
- the merge queue's entries, each candidate built from an entry with its check
  and review times, and each landing with its pushes, pull request and the
  time GitHub confirmed the merge;
- the heavy-check permit measurements, which start on November 8 at 14:40:
  511 of them were asked for inside the window. The file keeps growing, and
  every later one is left out.

A run's elapsed time starts when the scheduler selected it. It ends when its
change landed on main. If the queue handed the change back, it ends at the
hand-back. If the change is still waiting, it ends at the window's end. If the
change never went through the queue, it ends when the run finished. Every
moment is counted once. Where two parts overlap, it goes to the first part in
this order: deploy pauses and restarts, permit wait, review, harness checks,
the developer's own checks, developer working, waiting for review, waiting
between rounds, landing. A "repair round" is anything that happened after the
first verdict: a check that failed, or a review that sent the change back.

## The split

There are 175 runs, with 4,432 hours of elapsed time between them. The median
run took 22.9 hours.

| Part of a run | Total hours | Share of all elapsed | Median per run |
| --- | ---: | ---: | ---: |
| Developer working | 15.8 | 0.4% | 2.6 min |
| The developer's own checks | 90.6 | 2.0% | 27.6 min |
| The harness's check stage | 120.9 | 2.7% | 19.6 min |
| Waiting for review | 1.5 | 0.0% | 0.1 min |
| Review | 1.9 | 0.0% | 0.5 min |
| Repair rounds (all parts after the first verdict) | 171.0 | 3.9% | 0 (49 runs had one) |
| Waiting for a developer slot or check permit | 52.8 | 1.2% | 0 |
| Deploy pauses and restarts | 5.0 | 0.1% | 0 |
| Landing (merge queue, replays, conflicts) | 3,829.7 | 86.4% | 14.3 h |
| Not accounted for by the records | 142.9 | 3.2% | 4.9 min |

**Landing is the largest share by far.** The landing periods, from approval
to landing, hand-back or the window's end, come to 3,847.3 hours before the
overlap rule is applied. They break down as follows:

- Waiting in the queue before the entry's own candidate was built: 3,718.2
  hours.
- Candidate checks: 50.4 hours. 43.7 were for first candidates, and 6.7 were
  for later candidates, rebuilt because main had moved.
- Candidate review: 0.5 hours.
- Getting from approval into the queue: 19.1 hours.
- Everything else, mostly waiting on GitHub's merge: 59.0 hours.

17.6 of those hours overlap a part listed earlier in the overlap order, such
as a repair round's checks or a developer session that ran after approval.
Under the rule they are counted there, and that is why the table gives
landing 3,829.7 hours.

The 140 runs that reached the queue ended up three ways:

| Outcome | Runs | Median time in the queue |
| --- | ---: | ---: |
| Landed | 51 | 13.6 h |
| Still waiting at the window's end | 58 | 24 h |
| Handed back | 31 | 45 h |

The 31 hand-backs break down as:

- 15 conflicted with main by the time their turn came;
- 6 were in a run that was interrupted and then settled;
- 4 had their queued merge withdrawn;
- 4 were handed back by hand because the change had been merged by hand;
- 2 had a candidate check fail.

Every hand-back becomes another run, which joins the back of the queue.

Repair rounds come to 171 hours, and 135 of them are repeated check stages.
The worst runs had up to four check runs stopped part-way and started again.
Only 4.3 hours of repair time was a developer working.

The developer's own checks are mostly full test runs the developer starts. A
test command run by the developer is cut off after 10 minutes, so a developer
often runs it twice, as the item for concerns naming any recorded goal
(yoyodyne-gys) did. That one run spent 22 of its 28 minutes with the developer
waiting on its own checks, against 6 minutes of the developer working.

The two gaps in the run that prompted this item, between the developer
finishing and review starting, do not recur this week. From the end of the
harness's checks to the start of review takes a median of 6 seconds and 1.5
hours in total. From the developer finishing to the checks starting takes 3.4
hours in total, across 210 occurrences.

## Machine load during check stages

The load is recorded when each run's check stage starts. There are 171 such
readings this week, on a 16-core machine:

| Median | Mean | 90th percentile | Highest |
| ---: | ---: | ---: | ---: |
| 20.7 | 28.3 | 52.2 | 169.9 |

The load is also recorded at each heavy-check permit request. There are 511
such readings:

| Median | 90th percentile | Highest |
| ---: | ---: | ---: |
| 14.8 | 34.4 | 291 |

Median length of a heavy check, by who ran it:

| Who ran it | `make test` | `make race` |
| --- | ---: | ---: |
| Ordinary run | 20.4 min | 22.3 min |
| Merge-queue candidate | 28.0 min | 28.0 min |

Waiting for a heavy-check permit:

| Who waited | Total this week | Typical wait | Longest wait |
| --- | ---: | --- | ---: |
| All requests | 57 h | median 0 s; 90th percentile 20 min | 4 h 41 min |
| Runs' check stages | 28.7 h | not separated | not separated |

## Why the queue cannot keep up

The queue's own records show where a candidate's 81 minutes go (median of each
step):

| Step | Minutes |
| --- | ---: |
| `make test` | 32 |
| `make race`, run after the test, not beside it | 27 |
| GitHub holding the merge after the harness asks for it (90th percentile 34) | 21 |
| Review | about 0.5 |

The queue was working on a candidate for 112.8 of the 123.3 hours since its
first candidate. It is busy 91 per cent of the time, and it still lands about
10 changes a day. 140 entries were admitted, for 118 different items, which is
about 27 a day. So the queue grows by about 17 entries a day. The longer an
entry waits, the likelier it is to conflict with main and come back as another
run.

## What the records cannot separate today

Each of the following is time the records hold only as a lump, or not at all.
Recording it is work to admit.

1. **The developer's own checks.** The run's event log keeps each tool call's
   length but not the command. A test run cannot be told from any other long
   command. This note counts any shell command of 60 seconds or more as the
   developer's own check.
2. **Why a run sat still.** One run, for the item to cut release v0.6.0
   (yoyodyne-fvn), passed its checks
   on November 8 at 17:30 and recorded nothing more until new checks on
   November 12 at 17:13. That is 95.7 hours, two thirds of all the time not
   accounted for. A hold, a parking or a stop is not written into the run's own
   event log.
3. **Deploy pauses.** A redeploy that stops a developer session shows only as
   a session with no recorded end, followed later by a new one. There were 28
   of these, totalling 10.9 hours, and they are counted here as time not
   accounted for. The run's events do not say a redeploy caused the stop. Time
   during which slots are held empty for a redeploy is not recorded against
   any run. The worker's attempt counter reaches the hundreds, so it does not
   count restarts either.
4. **Waiting for a developer slot.** A run is created only once a slot is
   free. The time ready work waited for a slot is in no run record.
5. **The gaps between one check and the next inside a check stage.** There
   are 17 hours of these, leaving out the run in point 2. Permit waits before
   November 8, 14:40 are not recorded at all. Light checks, the ones that need
   no permit, record no wait.
6. **The queue's own state.** The records do not say whether an entry waiting
   behind others waited because the queue was busy, stopped for someone to
   release it, or idle.
7. **Where a repair round starts.** No record marks it. This note infers it
   from a developer session that starts after a verdict.

Runs that finished before the queue started taking entries (November 8, 12:36)
landed some other way. That landing time is not in their records, so it is not
counted here.

## The next fixes, ranked by expected hours saved per day

"Hours" here are hours of elapsed time added up across all runs, the measure
used in the table above. The queue accounts for about 531 of those hours a
day. Most of it goes away only once the queue lands changes faster than they
are approved, which is about 27 a day. Below that rate, a fix makes the queue
grow more slowly but does not make it shrink.

1. **Check several waiting approved changes together as one candidate.** The
   queue's candidate record already has room for several changes. With three
   changes to a candidate, the queue could land about 30 a day at today's 81
   minutes. That is enough to stop the queue growing. It would save most of
   the 531 hours a day, and fewer changes would come back conflicting with
   main.
2. **Keep the queue working while GitHub holds a merge.** Starting the next
   candidate's checks during GitHub's 21-minute hold would bring a candidate
   from 81 minutes to about 60. Running a candidate's `make test` and
   `make race` at the same time would save about another 27 minutes, but it
   adds load, which is the same question as item 4 below. With both, a
   candidate takes about 33 minutes, or roughly 40 a day. The item for
   checking the next candidate ahead (yoyodyne-ifd.429.153) is the first of
   these. The record of the decision to cut to three developer slots
   (yoyodyne-cfv6) says that check-ahead gives nothing on this repository.
   These numbers disagree: the 21 minutes of GitHub's hold are time the queue
   now spends idle.
3. **Stop repeating whole check stages in repair rounds and after restarts.**
   This would save some of the 19 hours a day spent repeating check stages in
   repair rounds.
4. **Limits on heavy checks across the machine.** These limits affect about 50
   hours a day: the harness's check stages (17 hours), the developers' own
   checks (13 hours) and the repeated checks in repair rounds (19 hours). Plus
   7.5 hours a day of waiting for a permit. A lower load could shorten those
   checks by perhaps a fifth, saving about 10 hours a day. It would also make
   the queue's own `make test` take about 22 minutes instead of 32. That is
   real, but it is an order of magnitude below fix 1.
5. **Developers run only the scoped checks they are told to run.** Not
   starting full test runs that hit the 10-minute cut-off would save about
   half of the 13 hours a day spent on the developer's own checks. It would
   also lower the machine's load.
6. **Run a candidate's checks and its review side by side** (yoyodyne-ifd.429.143).
   A candidate's review takes about half a minute. This saves about half a
   minute per candidate, about 5 minutes a day. Two slot-count items both wait
   on it: the cut to three developer slots (yoyodyne-cfv6) and the return to
   four (yoyodyne-nn0z). It is the wrong thing for them to wait on.
