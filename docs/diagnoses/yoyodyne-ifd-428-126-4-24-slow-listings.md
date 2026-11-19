# yoyodyne-ifd.428.126.4.24: why listings of open and ready work still ran past 10 seconds

All times are Pacific (PST). Measured on the live factory on November 19, 2026
from the tracker's own call records (`tracker-calls` and `tracker-slow-calls`
in the product's state), 00:00 to 10:08, with the store at about 40 MB.

## What the records show

bd lets one command into its store at a time, reads included, so a listing is
slow when many other commands are made around it. Between 00:00 and 10:08,
`yoyo work --watch` (one process) made 39,502 single-item `bd show` calls, about
2,000 an hour and more than ten times as many as every other caller together
(reconcile 4,224; the dashboard 1,469 listings and ready reads). The 10-hour
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

## Left for after the change runs live

Whether every listing of open or ready work then stays within 10 seconds for a
full hour needs the new build running, and is recorded after that. The reads
made by no pass (9,694 from 10:00 on) go through the same code as far as this
change reaches; if listings still run over after this, that traffic is next.
