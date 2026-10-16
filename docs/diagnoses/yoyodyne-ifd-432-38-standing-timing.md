# yoyodyne-ifd.432.38: where the dashboard's standing spends its time

On October 14 at about 08:05 PDT, a freshly restarted dashboard took 58
seconds to answer `/api/standing`. The page asks every 10 seconds, so its main
view was marked stale, showing a reading 92 seconds old. This records where that
reading's time goes, source by source, what the change for the item to make
`/api/standing` answer within the page's refresh interval
(yoyodyne-ifd.432.38) did about it, and what is left for the investigation of
slow tracker reads (yoyodyne-6u7) and the routine tracker-store cleanup
(yoyodyne-ifd.433.40). All times are PDT.

## How it was measured

`/api/standing` is served from a snapshot. Each snapshot is one call of the read
model's standing build (`readmodel.ReadStanding`), which is also what
`yoyo status` and the Slack status read. So the time a snapshot takes is that
build's time. A dashboard just restarted has no snapshot yet, and before this
change its first request waited for the whole first build.

The build was run on October 16 against this project's own state: the run
records, the conversations, the reports, and the rest under the product's
state directory, with the repository as its git repository. A timer was placed
after each source it reads. A developer run cannot open the tracker's store, so
the tracker was pointed at a copy of the main checkout's store, taken that
morning: 950 MB, 1,284 items, and a 22 MB listing of every item. The timers and
the program that ran them were temporary and are not part of the change.

The copy is not the live store in two ways that matter. Nothing else uses the
copy, so no tracker command waited on the store's lock behind the harness's
own writes. And the timings depend heavily on how busy the machine is, so each
set below names its load average.

## The tracker commands one at a time

Timed from a shell between 06:58 and 07:00, at a load average of 13 to 20, just
after the copy was made:

| Command | Time |
|---|---|
| open items (`bd list --status=open`) | 9.1–15.0 s |
| blocked items (`--status=blocked`) | 5.4–16.4 s |
| what is ready to start (`bd ready`) | 1.7–6.9 s |
| items in progress (`--status=in_progress`) | 4.0–4.5 s |
| every item (`bd list --all`) | 4.2–6.2 s |

Two minutes later the same commands took 1.3 s (open) and 2.3 s (every item).
Narrowing a listing to one status does not make it cheaper: the listing of every
item was never slower than the open items alone.

The listing of every item carries exactly the same records, field for field, as
each narrower listing. Compared on this copy: 246 open, 16 blocked, and 7 in
progress, with no field different in any record.

## The standing build, source by source

Four builds at each load. Ranges are the fastest and slowest of the four.

| Source | Load about 7 (07:02–07:03) | Load about 14 (07:16) |
|---|---|---|
| Tracker: open items | 0.70–1.20 s | 1.73–1.87 s |
| Tracker: blocked items | 0.72–0.88 s | 1.36–1.59 s |
| Tracker: what is ready to start | 0.42–0.52 s | 0.66–0.85 s |
| Tracker: items in progress | 0.70–0.94 s | 1.38–1.66 s |
| Tracker: every item, for titles | 1.52–1.94 s | 2.50–2.91 s |
| **Tracker, all five** | **4.06–5.48 s** | **7.63–8.88 s** |
| Held for a person: every run record, and a git query for each stopped run | 3.52–4.50 s | 4.38–4.98 s |
| Runs in flight | 0.86–0.96 s | 0.83–1.04 s |
| Capacity held and blocked | 0.62–0.75 s | 0.76–0.89 s |
| What needs a person | 0.52–0.62 s | 0.63–0.91 s |
| Operator actions, services, program managers, failing tasks, factory stall, unrun checks | 1.0–1.2 s | 1.2–1.3 s |
| Everything else (under 0.1 s each) | under 0.3 s | under 0.3 s |
| **Whole build** | **11.30–13.86 s** | **16.76–17.56 s** |

The tracker was about half of each build, and the five commands ran one after
another. The held-for-a-person check was the largest single source. It does not
touch the tracker: it reads every recorded run and asks git, for each stopped
run, whether its branch and worktree are still there.

## Which share belongs to the tracker work

The time inside the tracker commands, 4 to 9 seconds a build here and 24 to 49
seconds for the five from the shell just after the copy, belongs to yoyodyne-6u7 and
yoyodyne-ifd.433.40. That time is the store's opening cost and its lock. Every
bd process opens the whole store under one exclusive lock. The 6u7 diagnosis
found a cleaned store answers 1.5 to 5 times faster, and 433.40 makes that
cleanup routine. The 58 seconds of October 14 was not reproduced. On the live
store each of the five commands also queues behind the harness's own writes,
which a copy does not show. Five commands in a row, each paying that cost, is
consistent with it.

What the dashboard and the read model did themselves was ask five times, one
after another, for what one listing holds. That, and waiting for a whole build
before answering, is this item's share.

## What changed

- One standing now asks the tracker twice instead of five times: one listing of
  every item, from which the open, blocked, and in-progress items and the titles
  are all read, and one readiness query. The two start together at the top of
  the build, and the held-for-a-person check runs while they are under way
  (`internal/readmodel/sharedlisting.go`).
- The same comparison, run at 07:16 by alternating the build from before and
  after the change four times each at a load of about 14: **16.76–17.56 s
  before, 8.54–8.74 s after.** Four more builds after the change at that load
  took 8.47–9.18 s. The timers were only in the build from before, so the
  after figures are whole builds only.
- The dashboard starts the standing's first build when it starts. A request
  that arrives before any build has finished waits at most 4 seconds, and is
  then told how long the first build has been under way.
- Every request after that is answered at once from the latest snapshot. A
  reading is marked stale only when it is older than two intervals plus two of
  its last build's durations. A slow but current reading is shown with its age
  and how long it took: `taken 1m ago, by a reading that took 58s`.

## What is left

- At a load of about 14 the build still takes about 8.7 seconds. That is under
  the dashboard's tolerance now that a slow reading is shown with its age, but
  it is not under 5 seconds.
- The held-for-a-person check, 3.5 to 5 seconds, is now the largest part. It is
  the read model's own work. Asking git about the stopped runs at the same time,
  or reusing an answer for a run that has not changed, would shorten it.
- `yoyo gate list` reads the queue through `readmodel.Queue`, which still makes
  the narrow listings one after another.
- On the live store, bd lets one process at a time hold the store, so the two
  remaining tracker commands may still queue one behind the other there.
