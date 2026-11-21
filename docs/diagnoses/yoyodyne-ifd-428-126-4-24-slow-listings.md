# yoyodyne-ifd.428.126.4.24: why listings of open and ready work still ran past 10 seconds

All times are Pacific (PST). Measured on the live factory on November 19, 2026
from the tracker's own call records (`tracker-calls` and `tracker-slow-calls`
in the product's state), 00:00 to 10:08, with the store at about 40 MB.

## What the records show

bd lets one command into its store at a time, reads included, so a listing is
slow when many other commands are made around it. Between 00:00 and 10:08,
`yoyo work --watch` (one process) made 39,502 single-item `bd show` calls, about
2,000 an hour and about seven times as many as reconcile and the dashboard
together (reconcile 4,224; the dashboard 1,469 listings and ready reads). The 10-hour
sum of the time those calls took was 134,551 seconds, so on average several
were always waiting for the store at once, and 2,144 of them ran past 10
seconds, as did 190 of the watch's listings, 139 of the dashboard's ready reads
and 28 of `yoyo status`'s listings.

Of the watch's reads from 10:00, 9,694 were made by no recurring pass and the
rest by the recurring passes: the Lead Product Manager's sweep (4,093), the
program manager's (1,667), the architect's (1,008) and the development
manager's (700). One sweep made 763 reads in 43 minutes of 71 different items,
about ten reads of each. The items read most were parents (the same few,
read 400 to 570 times in the day).

## What waits on what

When a role reads an item, the harness adds an "inherited block" line
(`renderInheritedBlocks`, `internal/chat/inherited.go`): it reads each parent
of the item up to the top, then reads the whole chain again to judge whether the
parent is waiting on the item itself, and reads the work each parent waits on.
Each read of a child under a deep parent therefore made several `bd show` calls
for the parents alone, and a pass reading dozens of siblings read the same few
parents hundreds of times. Each call costs the store about 0.6 seconds held
exclusively, so the passes kept the store busy for a large part of every hour
and every listing queued behind them. Neither the store's size nor separate
processes was the cause.

## The fix

A session now keeps the parents it read for the inherited-block line for one
minute (`internal/chat/ancestorreads.go`), and forgets them when it carries out
any tracker action other than a read. Two sibling reads read their shared
parent and its blockers once.

## After that fix: the reads made by no pass

That fix landed at 15:40 (pull request #2224) and did not end the slow
listings: maintenance's reading at 16:54, from a build carrying it, found 30 of
112 listings over 10 seconds. The records from 17:00 to 19:00, with the store
at about 78 MB, show why. In the hour from 17:00, `yoyo work --watch` made 926
single-item reads that no pass asked for, which took 5,099 seconds between
them, more than any other caller (the four recurring passes together made
1,176, and reconcile 199); 39 of 179 listings ran past 10 seconds. In the hour
from 18:00 it made 675 such reads taking 3,073 seconds, and 53 of 163 listings
ran past 10 seconds.

Read in order, those reads come from two places in every pull and every merge
queue pass, each reading one item per `bd show`:

- **The merge queue's choice of its next entry** (`queueOrder.pick`,
  `internal/orchestrator/mergequeueorder.go`) read every waiting entry's item,
  one after another. The worker and the promoter each choose in every pass, and
  a waiting queue goes round once a minute, so the ten entries waiting that
  evening were read twice a pass, about 20 reads a minute while passes ran. The
  same ten items, the queue's entries, were each read 22 to 26 times in the hour
  from 18:00; the items read 14 times or more made 448 of the 926 reads in the
  hour from 17:00.
- **The pull's reading of the development manager's decisions to wait**
  (`endWaits`, `internal/readmodel/holds.go`, run by every pull through
  `HeldForAPerson`) read the work each standing wait named and then each held
  item's parents to the top, to tell a wait that can never end. With dozens of
  standing waits, every pull made about sixty reads, one after another, of the
  same items (the waited items and parents such as yoyodyne-ifd.428 and
  yoyodyne-ifd), every few minutes.

## The second fix

Both now read what they need together, in one batched read naming every item
(`beads.Client.ShowMany`, then a `bd show` of all of them; see the third fix
for why it no longer is): the queue reads all its entries' items in one call
per choice, and the pull reads the standing waits and held items in one call
and their parents in one call for each level above them. A batch the tracker
does not answer is not followed by a read of each item alone, which would wait
on the same store again: the queue then chooses in admission order and says the
tracker could not be read, and each wait says it could not be read, as either
did when every single read failed.

## After the second fix: a batched read held the store for every item it named

The second fix landed at 22:59 on November 19 (pull request #2248) and cut the
number of calls by about three quarters, but not the timeouts: maintenance's
reconcile step timed out twelve times overnight, and its limits step still
found a fifth to a third of listings over 10 seconds. The records from 00:00 to
05:00 on November 20, with the store at about 100 MB, show why.

Batched reads were the slow calls. In the hour from 04:00 there were 784
tracker calls taking 11,643 seconds between them; the 29 batched reads of 10 to
19 items took a median of 47 seconds each, and none ran without another call
waiting beside it. Timed one at a time on a copy of the store taken at about 05:15
(copied while the live store was written, and it opened and answered normally),
with nothing else running, `bd show` costs about half a second for each item it
names, all of it with the store held:

| Read on an idle copy | Took |
| --- | --- |
| `bd show` of 1 item | 0.6s |
| `bd show` of 5 items | 2.8s |
| `bd show` of 10 items | 5.7s |
| `bd show` of 20 items | 11.3s |
| `bd show` of 50 items | 30.6s |
| `bd list --id=…` of the same 23 items as a 9.0s `bd show` | 0.4s |
| `bd list --status=open` | 0.6s |
| `bd ready` | 0.4s |

Three listings started half a second into a 20-item `bd show` on the copy all
finished only after it did. So a batch saves the waits to get in, as the second
fix meant, but not the time inside: a read of 20 items held the store for 11
seconds at a stretch, every listing behind it waited the whole time, and on
the live store, with other calls already waiting, that became the 2-minute
timeouts. Counting each call at what it costs alone (half a second for each
item a `bd show` names, about half a second for anything else), batched reads
held the store for 1,037 of 1,586 seconds in the hour from 00:00, 991 of 1,395
in the hour from 02:00, and 686 of 1,142 in the hour from 04:00: two thirds of
the time the store was held, from about one call in ten.

The read of `yoyodyne-ifd` that reconcile was seen waiting on is the top of
the work tree, an item the tracker holds, read while climbing to a held item's
top parent; it is not a read of a wrong identifier. Reading a run identifier
as a work item is yoyodyne-ifd.428.174's to fix.

## The third fix

`beads.Client.ShowMany` now reads the items named with one `bd list --id=…`
for each fifty, which holds the store about as long as a read of one item,
whatever the number. A listing names each linked item but not its status or
title, which `bd show` gives and the callers judge waits by, so the linked
items not already read are read the same way in one more call, and each link
is given exactly what `bd show` gives it. Every caller of a batched read (the
merge queue's choice, the pull's reading of standing waits, reconcile's and
the passes' reads ahead) is unchanged and gets the same answer:
`TestShowManyConformance` reads items with a parent, a blocker read beside
them and a closed blocker against bd itself, and requires each to be what a
read of it alone gives. A batch of 20 items now holds the store for under a
second where it held it for 11. A listing narrowed to named items is logged
as a read of them, so the limits step does not count it as a listing of open
work.

## Left for after the change runs live

Whether every listing of open or ready work then stays within 10 seconds for a
full hour, and whether `yoyo status`'s listing of the items not closed does,
needs the new build running, and is recorded after that. If listings still run
over, the next largest holders of the store are single-item reads (450 to
520 an hour, about 270 to 310 seconds held, the recurring passes' and reconcile's among
them) and the scheduler's writes of why it passed an item over (50 to 200
updates an hour, up to about 140 seconds held).

## After the third fix: the store was idle most of the time, and calls still waited

The third fix landed at 07:05 on November 20 (pull request #2308), and the
scheduler ran it from 09:35. Batched reads fell from a median of 24 to 28
seconds to 1.9 seconds, but in the hour to 09:28 maintenance still found 28 of
82 listings over 10 seconds, and 27 of 135 in the hour to 10:48.

The records from 10:00 to 12:00, with the store at about 130 MB, show that the
store was not busy enough to explain the waits. The harness made 1,678 calls
and its callers waited 16,682 seconds for them between them: two to three
calls waiting at any moment. Timed alone on a copy of the store taken at about
11:57 (126 MB), every kind of call those were costs under two seconds: a
single-item `bd show` 0.5 to 0.75 seconds, `bd ready` 0.5, a listing of every
status 1.5, `bd export` 1 to 4, and an update 0.55. At those costs the
harness's 1,678 calls held the store for about 1,000 to 1,250 of the 7,200
seconds, under a fifth of the time. Yet 295 of them took more than 10 seconds,
and 32 ran to the two-minute limit.

The processes the slow-call records name beside each stopped call include
commands no part of the harness makes: `bd --sandbox show yoyodyne-ifd --json
--include-dependents`, `bd --sandbox list --json --tree=false --status …`,
`bd --sandbox blocked`, `bd --sandbox ready --limit 1000` and
`bd --sandbox epic status`. Of the 22 slow-call records from 10:00 to 12:00,
20 name one of these beside the stopped call, and 10 the read of the top item
with all its dependents. Nothing in
this repository runs bd with `--sandbox`, `--tree=false` or
`--include-dependents`; they come from a program outside the harness reading
the same store, such as a board that shows the tracker. Each costs 0.5 to 1.7
seconds alone on the copy, so they are a few more callers rather than long
holders.

What turns a store busy a fifth of the time into two-minute waits is how bd
waits for it. A bd that finds the store taken does not wait in line; it sleeps
and tries again, sleeping longer each time. On the copy, with nothing else
running:

| Test on the copy | Result |
| --- | --- |
| One caller making 8 single-item reads in a row | longest 1.6s |
| Three callers making 8 each at once | longest wait 21.3s |
| Six callers making 8 each at once | longest wait 48.2s |
| Three reads started 0.15 to 0.26s into a 0.65s listing | got in 0.5, 1.1 and 3.1s after it ended |

In a line served in order, the longest of those waits would have been about 2
and 4 seconds. Whichever bd happens to try next when the holder finishes gets
in, usually one that has just arrived, and the one that has waited longest is
the one sleeping longest, so it keeps losing. With two to four callers always
waiting on the live factory, some caller kept losing for the whole two minutes.
That is the cause the earlier fixes did not reach: they made fewer and cheaper
calls, but each call still entered bd's lock in no order, and the store's size
does not enter into it.

## The fourth fix

Every bd call the harness makes on the live store now waits for its turn
before starting bd, and the harness's calls are served in the order they asked
(`internal/beads/turns.go`). The line is a directory of tickets inside the
store's own directory, so every harness process reaching the store shares it:
a ticket is named for when it was taken, is held under an advisory lock while
its call waits and runs, and is removed when the call ends. A call starts bd
only when no earlier ticket is still held, so at most one of the harness's bd
processes is ever waiting on bd's own lock, and only against processes the
harness does not start. A ticket left by a process that died is one nobody
holds, and the next caller removes it. A call whose turn does not come within
its bound is not made and reads as unanswered, as a call bd's own bound
stopped does.

The time a call waits for its turn, and a listing's wait for the listing lock,
is now logged with the call (`waited` in the tracker call records), and the
limits step judges a listing by what its caller waited for the answer, both
waits and bd's own time together. A call that never got its turn is logged
too, as stopped at its limit. Without that, moving the wait out of bd into the
line would have made listings look quick to the limits step while their callers
waited as long as before.

The line was checked against bd itself, on a copy of the store taken at about
14:30 (154 MB): six processes, each making eight single-item reads through the
harness's client, against six shell loops making the same reads with bd
directly, run alternately twice while the machine was loaded by other work.
Loaded, one read alone took 0.6 to 2.5 seconds, so each run is judged by its
longest wait against the average time a read held the store:

| Six callers, eight reads each | Average read | Longest wait |
| --- | --- | --- |
| bd directly, first run | 2.8s | 88.1s |
| through the line, first run | 2.2s | 16.9s |
| bd directly, second run | 2.6s | 98.3s |
| through the line, second run | 0.9s | 7.3s |

Directly, the longest wait was 32 to 38 times an average read and came within
half a minute of the two-minute limit; through the line it was about eight
times, close to the six a line served in order allows, and the 48 reads took no
longer in all. The hour from 13:00 on November 20 showed the same cause on the
live factory: 942 calls whose times added to 11,697 seconds, 203 of them over 10
seconds and 23 at the limit, though at about 0.6 seconds each they needed the
store for under a sixth of the hour. The 43 slow-call records from 12:00 to
14:50 name `bd --sandbox` reads beside the stopped call 25 times, the read of
the top item with all its dependents 15 of them.

The programs outside the harness keep using bd's own lock. If the hour after
this fix still finds listings over their bound, the waits recorded with each
call say whether the time went in the line, which would mean the harness's own
calls are too many, or inside bd, which would mean those outside processes are
holding the store; the slow-call records name them.
