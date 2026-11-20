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
