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

Both now read what they need together, in one `bd show` naming every item
(`beads.Client.ShowMany`): the queue reads all its entries' items in one call
per choice, and the pull reads the standing waits and held items in one call
and their parents in one call for each level above them. A batch the tracker
does not answer is not followed by a read of each item alone, which would wait
on the same store again: the queue then chooses in admission order and says the
tracker could not be read, and each wait says it could not be read, as either
did when every single read failed.

## Left for after the change runs live

Whether every listing of open or ready work then stays within 10 seconds for a
full hour, and whether `yoyo status`'s listing of the items not closed does,
needs the new build running, and is recorded after that. If listings still run
over, the next largest callers are the recurring passes' own reads (about
1,200 an hour from 17:00) and reconcile's (about 200 an hour).
