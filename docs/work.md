# How work flows once you approve it

*For an operator who has approved work and wants to know what happens
to it. Part of [yoyo's documentation](../README.md#further-reading).*

`/work <beads-id>` and `yoyo run <beads-id>` execute the same thing. The run
claims the item, creates a branch and an isolated worktree outside your primary
checkout from exactly the branch the work will be promoted into, and asks the
developer for the change:

```sh
./bin/yoyo run --json "$work_item_id"
```

On success, the JSON result reports the run ID, branch, worktree, base commit,
change summary, checks, and agent summary.

Runs cut and remove worktrees in one repository beside each other, and Git
refuses a creation that crosses another worktree's creation or removal rather
than waiting for it — the entry it was reading gone from under it, or the
directory it was about to make its own entry in deleted with the last one. A
crossing is retried rather than failing the run, in every form Git reports it,
up to three attempts; where a watch session started the run, each retry is
named on the watch log with the item, the Git command, and Git's own words.
[A registration a run never finished
writing](operations.md#a-registration-a-run-never-finished-writing) lists the
forms.

Before giving a new worktree `.beads/issues.jsonl`, the harness asks bd for a
fresh snapshot in your checkout rather than using the copy its base commit
carried. Ordinary harness tracker writes disable bd's automatic export with
`BD_EXPORT_AUTO=false`, so each write no longer rewrites the whole snapshot.
The snapshot is published only after the export succeeds; a failed export
refuses worktree preparation rather than passing an older copy off as current.
That export is derived from a store Git is not
authoritative for and it is committed on a cadence of its own — a release cut,
not a run — so between cuts every commit carries a copy some number of items
behind, and a developer reading it for the work around its own would find items
admitted since simply absent. The copy is held out of the change the run makes:
it is not in the diff you review, it is not in what gets promoted, and it does
not conflict against another run that was given its own. Where your tracker's
export is ignored rather than committed, the copy arrives the same way and needs
no holding; where it is neither committed nor ignored, none is made, because a
copy there would arrive as a file the developer never wrote.

The hold itself is a bit in the worktree's index, which lives under `.git` — a
directory a developer's sandbox grants writes to, so one Git command inside the
run can undo it and hand the refreshed copy back as part of the change. The
export is therefore refused in the change as well, by the same gate below that
refuses an upstream artifact home: a diff containing it goes back to the
developer with the mechanism named, and the promotion carries the export the
branch already had. What keeps a derived file
out of your review is a check made against the change rather than an index bit
surviving whatever ran in the worktree.

What the item waits on is read from the tracker at every point the run is about
to commit to work — before it is claimed or resumed, at the start of each round
of the gate, and once more before the promotion — rather than trusted from
whatever readiness selection saw. A dependency link added to an item that is
already in flight therefore takes effect on that run: it pauses at the next of
those boundaries exactly as an unresolved directive does, keeping its claim, its
branch, its worktree, and its developer session, and it carries on when the work
it waits on is closed or the link is removed. **Nobody has to type anything for
it to carry on: a watching `yoyo work` session continues it.** Its item stays
claimed while it waits, so no queue ever offers it again, and until the watch
came to continue these runs (yoyodyne-ifd.428.51) the only thing that continued
one was somebody typing `yoyo run` on the item. Now every pull reads the runs
paused this way, oldest pause first, and asks the tracker what each item still
waits on. One whose work is all closed is continued in its own worktree and
developer session, against a developer slot and under a session's `--limit`
exactly as a recorded repair is fired, and under the same two switches: your
pause and a held intake both leave it paused, and the first pull after either
lifts continues it. The run writes on the item that it was continued, by whom
and why, at the moment its pause is lifted — so a continuation refused before
it got that far writes nothing there. A continuation that comes back without
the run going on — declined for want of a slot, refused, or paused again — is
said on the pass and left for a quarter of an hour before a pull attempts it
again, rather than being tried at every poll. One whose work is still open is
passed over naming what it waits on, and the sweep leaves it alone however long
that lasts, rather than settling it after half an hour as it does a park
nothing continues. A stop recorded on such a run is honoured before any
continuation: the pull does not pick it up, and the next `yoyo reconcile` ends
it as the stop asked.
**A run paused this way gives its developer slot back
as it records the pause.** A paused run has no process and spends nothing, so a
slot it held would stand empty for as long as the wait lasts — on 2026-09-27
one run held the only free slot this way for nineteen hours beside a ready queue.
So the next pull fills the slot, `yoyo status` lists the run under *Paused,
holding no developer slot* rather than under *Running*, and the slot counts as
free wherever capacity is said. Continuing the run takes a slot again under the
same `execution.max_concurrent_developers` limit, and a harness with no slot
free turns the continuation away exactly as it turns away a fresh reservation:
the run stays paused, untouched, until a slot frees — which is the continuation
declined for want of a slot above. That matters because a development
manager linking a dependency onto work already moving is precisely how a gate
gets added late, and a run that answered from selection-time state would develop
straight through the gate filed to stop it — and spend review rounds on a change
that should never have been dispatched. Both the developer's context and the
reviewer's evidence state what the item waits on, and state it as `nothing` when
it waits on nothing, so neither can mistake an item this context happens not to
describe for one nothing blocks.

The dependency line also names each blocker's tracker status. Review evidence
carries the upstream item's title and explanation, or says when the tracker
could not supply them; at most eight explanations of 1,536 bytes each are
included. An undecided upstream is a reason to stop, not permission for a
developer to invent its design. A sound diagnosis can be approved as evidence,
and an item that cannot be met as written can be escalated, using the terminal
outcomes described below rather than spending repair rounds on missing work
the developer correctly declined to implement.

Then the change is gated on what it touched. The project configuration and the
artifact homes upstream of the work — `.yoyodyne/`, `docs/product/`,
`docs/designs/`, and `docs/decisions/` by default — are default-deny for a
developer's diff, because a developer that edits one is redefining what its own
work is measured against, and so is the refreshed export above, because it is
the harness's copy of a store no run writes to. A change that touches one without the work item
granting it is refused before any check runs and before any reviewer is asked,
and handed back to the same developer in the same repair loop a failing check
uses. An item grants an exception in its own text, on a line beginning
`Protected-path grant:`, so every exception is declared in reviewed item text
rather than discovered in a diff. A grant admits the path and decides nothing
about what goes into it, so the reviewer is told to read the item for the decided
change behind each grant and to raise a finding when none is named.
[Configuration](configuration.md#protected-paths-in-a-developers-change)
has the details.

The change is then gated on whether anybody ran it. A developer's reply records
what it executed — the probe it ran in the worktree before it changed anything,
and the checks it ran against the change itself — and a change that records
nothing is handed back for it in the same repair loop, before the suite is spent
on it and before a reviewer is asked. A run that spends its attempts that way
stops in front of a person instead of reaching a reviewer. The probe is asked of
every run because it is nearly free and because what it catches is invisible
from the inside: an environment where nothing can be spawned at all looks exactly
like one nobody has asked yet, and a developer that never tries can write code,
report it working, and close a work item on it. What the probe answers is whether
commands run rather than whether they pass: a probe the developer records as
never having started ends the run at once, naming what refused, rather than
spending the rest of its context against a wall, and one that ran and failed says
the environment works and something else is red — so the run carries on and the
configured checks report it. The check half is asked only of a change the
declared checks would read, and
[what a developer has to have run](configuration.md#what-a-developer-has-to-have-run)
says how that line is drawn. What the developer executed is part of the
reviewer's evidence either way.

Then the configured checks run in that worktree. Their passing result is bound
to a versioned fingerprint of the recorded base and every changed path, including
its Git file mode and its content: a regular file's blob or a symlink's target
text, read without following the link. Deletions and other file types are named
too. Changing the executable bit or retargeting a link therefore loses the
checks' credit, just as changing a file's bytes does; the changed tree needs
fresh checks before it can be integrated.

An independent reviewer —
its own provider invocation, with no tools at all — judges the change against
the work item, its design guidance and acceptance criteria, the goal it serves,
the standing goals stated in the delivered product intent, the invariants
delivered with it, and the check results. Every change is held to the standing
set whichever goal its item serves. A change that breaks a standing goal is
refused with a major finding naming the goal and the place it is broken, so the
developer repairs it within the item's grant; meeting the item's criteria does
not excuse the violation. The review summary names which standing goals were
checked and any evidence limitation. The change it is shown is measured
against the commit the run was cut from rather than against what happens to be
uncommitted, so work an earlier attempt already made — every attempt is
committed by the harness before the checks run, whether or not the run
publishes — is in the patch it judges. The evidence says that rather than
leaving it to be worked out: it names the base commit the
change is measured against and lists the commits already made for it, so a
reviewer never has to guess whether a branch's committed work is inside the
patch or behind it. It used to have to, and
[nine review filings across two items](diagnoses/yoyodyne-ifd-321-review-evidence-span.md)
hedged their verdicts over committed work they had in fact been shown. The one
emptiness that follows from the same measurement is stated too: a
worktree carrying commits whose combined effect on the base is nothing is a
change made and then undone, and the evidence says so and lists them, because a
reviewer told only that the patch is empty concludes the harness lost the
evidence. [One did](diagnoses/yoyodyne-ifd-236-review-evidence-over-committed-work.md).

**Each check result carries what the check printed, not only whether it
passed.** Beside every check's pass or fail the reviewer is shown the last 4 KiB
of each of its streams, cut at a line, and every line of its retained output
containing something the item's done-conditions quote — the text between
backticks or quotation marks in the description's "Done means" paragraphs and
in the acceptance criteria, at most sixteen of them, compared with whitespace
collapsed so a quoted `=== RUN TestX` finds Go's `=== RUN   TestX`. Those lines
are at most forty per check and 512 bytes each, and a quoted pattern no line
contains is said to be absent rather than left out. Every quotation says it is
the check's own output and untrusted, because a check runs the change's code and
what it prints is the change's to choose; and every one that was cut says the
bound that cut it and that the whole is in the run's event log, which keeps
every line every check printed. A pass alone could not answer a criterion like
"the run quotes the test's run line": the reviewer of `yoyodyne-ifd.141.5`
judged one indirectly from the suite passing, and otherwise would have spent a
repair round asking for the line.

**What a reviewer is shown for a continued run** — a repair round, a grant
carried out on a preserved worktree, a run picked up after its process died —
is therefore the branch's whole diff against the run's recorded base, never the
tail above the commits earlier attempts made. A 3,000-line README reduction the
first attempt committed is in the patch the fourth review judges, whole, beside
whatever that round changed. The evidence names the base and the tip it was read
at — the commit the run was cut from and the branch's HEAD at the review, with
the uncommitted worktree above it — and the run's record and the item's notes
carry the same two commits as `review_base_commit` and `review_head_commit`
beside the reviewer's session and verdict, so what any verdict was judged
against reads back as two commits rather than being reconstructed from a patch
byte count. Seven reviews across three items said their verdicts covered less
than the item asked for and approved on the passing tests instead
(`yoyodyne-ifd.387`); what is stated here is what each of them is actually
shown, so a verdict hedged that way is a finding about the reviewer rather than
about the evidence.

**A file the item references is shown to the reviewer as that same base holds
it**, and its heading names the commit — `## Referenced file: docs/guide.md (at
base commit <id>)`, the same label on an excerpt and on a reference stated as
left out. The developer's context reads those files from the checkout when the
item is claimed, which is the base then; by the time a review is asked for,
anything else promoted meanwhile has moved the checkout on, and a document read
there beside a patch measured against the older base makes a correct change read
as a divergent one. `yoyodyne-ifd.117.3` spent three repair rounds that way, on
guides extracted from `docs/configuration.md` and judged against the file as the
target branch had it later. The reviewer is told to judge against the labelled
copy and not to report a difference from a later revision it may know of. A path
the base does not hold — a document the change itself creates — is not carried as
a reference at the base commit; it can still appear in the patch and in the
candidate content described next.

Product intent is discovered from the base commit's file listing as well as
read at that commit, in both per-item and branch reviews. A goals document
removed or renamed in the current checkout therefore still supplies the standing
set. If the base listing cannot be read completely, including a cut at its
20,000-path or 128 KiB limit, review stops and names the cause before invoking
the reviewer; a partial listing cannot establish the whole set of documents.

**Repository evidence also names what the reviewed commit holds.** A committed
tree listing includes unchanged files and binary assets, so a path omitted from
the patch is not mistaken for a missing file. It is bounded to 20,000 paths and
128 KiB of quoted paths. Any cut is stated with a count, and a partial listing
proves presence only. Every finding claiming a repository path is missing names
it in `absent`; the harness normalizes that path and rejects the verdict if the
presence evidence contradicts it or either listing is incomplete. The change
listing also accounts for uncommitted additions and deletions; new-file,
omission, removal, and whole-content evidence is checked alongside it. A complete
HEAD listing cannot prove a path absent from a worktree whose changed-file listing
was cut, because an unlisted uncommitted addition may hold it. This check covers
the structured field;
the contract requires it for absence claims written in a finding's prose.

Whole text at the reviewed commit is supplied separately from references at the
base commit, for literal counts and content claims that a patch cannot settle.
Files cited by the acceptance criteria come first, followed by other item
references, then other changed files. Citations are matched against the committed
listing, so unchanged extensionless sources such as `Makefile`, `Dockerfile`,
`LICENSE`, and `scripts/check` are included too. Up to 32 files share a 64 KiB content
budget. Each supplied file is whole and labelled with its commit; unreadable,
binary, oversized, and excluded content is stated as unavailable. The total
review input bound can shorten this evidence further, with those losses stated
too. A reviewer lacking the needed whole content and an inspection tool says
what it cannot verify instead of asserting a defect from patch lines or an
excerpt. These candidate copies do not replace the labelled base references
used to judge an extraction. The review's start event records the candidate
commit, the listing's omission count, and which file contents were supplied or
unavailable.

The patch is bounded, and the bound is spent whole file by whole file rather
than cutting the patch at a byte count: a patch cut tail-first keeps whichever
files Git rendered first and loses the rest without naming them, so a reviewer
handed one could not say which files its verdict covered. Every file the patch
shows it shows in full, and every file it cannot show is named above the patch
with the file's size, the bound that dropped it, and — for a tracked file, whose
diff was rendered before the bound was applied — the size of that diff: a diff
bigger than the whole patch bound, one the bound had no room left for by the
time it was reached, a new file bigger than the per-file ceiling (measured
before anything is rendered, so it carries no diff size), or content with no
reviewable diff — delivered but too large to show, rather than absent. A new
file that is not a readable regular file is named with neither size, because
there is nothing to measure. A reviewer that cannot tell a file the change delivers from
one it never wrote judges the delivery blind, and an absence it is told about is
one it can hold the change to.

**A removal is described rather than rendered, and is not held to the bound.**
A file the change deletes whole is never shown as the diff removing it, which is
the file's whole content with a minus on each line: it is named above the patch
as deleted whole, with its size and its `git-blob:` digest at the base commit and
where the whole of it can be opened there, as `git show <base>:<path>`. A file
whose diff is nothing but removed lines is placed after every other file, whatever
its class: it is shown whole where what the bound has left holds it, because which
lines went is what a reviewer of a partial reduction reads, and described the same
way as a deletion — with what is left of it at the tip beside it — where it does
not. Neither ever displaces another file from the bound or is an omission, so
neither can refuse the approval, directly or by pushing a source or test file out; the reviewer is told to judge each
removal against the item's stated reason for it, and to raise a finding where the
item names none. A file rewritten in part — any added line — is unchanged by
this and still rendered, or omitted, under the bound. `yoyodyne-ifd.117.4` is why:
it cut `docs/configuration.md` by 378,605 bytes, the removal outgrew the
262,144-byte bound, and the document named as omitted refused an approval
however sound the reduction was. The run's `review.started` event names the
removed files with their base digests, as `deleted_files` and
`deleted_digests`.

The bound is spent in class order rather than in the order Git lists the files,
which is alphabetical: source files first, then tests, then test data and
generated or golden files — anything under a `testdata`, `fixtures`, `golden`,
or `snapshots` directory, a `.golden` or `.snap` file, a lock file, a Go file
named as generated, or a file whose first line carries Go's `Code generated ...
DO NOT EDIT.` marker. Within a class the files are in path order. So the cut
falls at the tail: a change whose committed fixtures sort ahead of its code —
5,500 lines of `internal/dashboard/testdata/renders` ahead of the 850 lines of
`internal/readmodel` the page depends on — presents the read model whole and
lets the bound fall on the renders, where spent alphabetically it spent the
whole bound on renders and sent the code to review unseen, which cost
`yoyodyne-ifd.141.3` two rounds. Each omission says what kind of file it is, so
a reviewer told the patch is missing test data reads that differently from one
told it is missing code, and a source or test file among the omissions means
the change outgrew the bound before its test data was reached. Each omission
is also delivered whole outside the patch, and the evidence says where: the
run's worktree, and for a committed file the tip commit as `git show
<tip>:<path>`. The reviewer has no tools and cannot open one, so it judges an
omitted file as unreviewed; what the location is for is the person following the
review, who can open the fixture the reviewer did not see. The run's
`review.started` event names the omitted files too, so what a verdict could not
have covered is read back from the record rather than reconstructed from the
prompt.

**A change whose test data alone outgrows the bound is approvable.** Ordering
the bound in class order made such a change reviewable; on its own it left the
change unclosable, because an approval used to be refused over any omission at
all — so a change of 5,500 lines of golden HTML beside 850 lines of Go could be
read in full and never approved. The refusal is now narrowed to the omissions
that really leave a change unjudged, and three things hold the approval
together:

- **Every file that is not test data is in the patch whole.** A source or a test
  file among the omissions still refuses the approval: the change outgrew the
  bound before its test data was reached, and what was not shown was not
  reviewed.
- **Every omitted fixture is listed with its path, its size, and a content
  digest**, and delivered whole where a person can open it — `sha256:` of the
  file for a change read out of a worktree, and `git-blob:` of the blob at the
  tip for a branch's accumulated change, each being what a reader checks where
  the evidence sends them. A fixture named with nothing anybody could open — a
  symlink, or a listing carrying no digest of a file that is there — refuses the
  approval exactly as cut code does, because a name is not evidence. A fixture
  the change *deletes* carries no digest and is not refused: there is nothing at
  the tip to digest, which its zero size says, and that absence is the whole of
  its content there. The digests are recorded on the run's `review.started`
  event beside the names, so what an approval covered is bound to exact content
  rather than to a path.
- **The verdict says which fixtures it accounted for.** An approving reviewer
  lists every omitted fixture in its verdict's `fixtures` field, by the path the
  evidence gave. An approval that does not is asked for once more on the same
  budget, as an approval that never said what it approves is: the change is
  sound and the answer is one turn away. A repair is never asked for the list,
  because it approves nothing.

A truncation the evidence cannot explain at all — a branch whose history the
bound clipped, which reports itself cut and names no file — still refuses the
approval, for the original reason: a reviewer shown part of a sequence cannot
say what the whole of it did.

Above the patch is also a listing of every file
the change touches, with Git's status, the file's size at the tip, whether it is
binary, what kind of file it is where it is a test or test data, and whether it
is already committed on the branch. That listing is where
a binary asset is seen to be delivered — a text diff never shows one — so an item
that ships an icon is not approved on a link checker's say-so
(`yoyodyne-ifd.68.9`), and it is where a reviewer tells a file an earlier attempt
published from one only this round's worktree holds.
Everything the reviewer is shown is
treated as evidence rather than instruction, so an instruction the developer
left in the diff is data to analyze rather than something to follow. A verdict
of `repair` returns the findings to the same developer, up to
`execution.repair_attempts_before_replan` attempts, before the run gives up and
records a blocker. That budget is not always the last word: a development
manager who decides the change is worth another go can hand the item a grant of
further attempts, and [`yoyo triage
repair`](conversation.md#deciding-what-becomes-of-stopped-work) re-enters that
run's repair loop on the change it already has rather than starting the item
over. Its opposite is `yoyo triage rerun`, which starts the item over for a
change whose ground moved. **A watching `yoyo work` session fires whichever of
the two she recorded, at the first pull with a developer slot for it, without
anybody typing either command**, so the verbs are what fires a decision now rather
than at the next pull; every gate they ask refuses the pass in the same way, and
every refusal is written onto the item and shown on her docket entry naming the
gate and what would clear it.
**The two are different acts with different accounting** — one spends the item's repair grant and the review rounds that
grant buys, the other spends its re-run budget — and neither of them is `yoyo
run <beads-id>`, which is you naming an item rather than carrying out a decision
somebody recorded about a run that stopped. `yoyo run` enforces that difference
rather than relying on it being understood: an item whose last run stopped with
its change preserved on a branch is refused a fresh clean run, naming both the
repair that would continue the change and the re-run that would start over
deliberately, because a fresh worktree off the target branch looks perfectly
valid and a developer given one delivers an empty change or reinvents the work.
The repair goes the other way about it: it names the run it re-enters, and what
carries it out can re-enter that run or refuse and nothing else — no reservation,
no claim, and no worktree. So a repair cannot arrive as a fresh run however it
was dispatched, and `yoyo run`'s refusal is what catches the dispatches that
never said they were repairs at all.

Not every round an item is charged for is one it cost. A round whose diff is
empty **and** whose run recorded a cause outside the work — the worktree it was
handed held none of the change, the primary checkout carried state the harness
does not own, the checkout of its worktree was ended by the budget the harness
gave it, the sandbox could not be entered, the build that dispatched it
predated the decision it was carrying out — is a **round refused from outside
the work**: what the round was handed held nothing to work on, so as the run settles the harness gives
back the review round it was charged against the item's cap and the granted
repair round the continuation consumed. The grant itself still stands and can be
carried out again once somebody puts the change back, and no sequence of these
walks an item toward an escalation it never earned. Both halves of that are
required. A cause on its own excuses nothing — a round that recorded one and
delivered a change anyway spends as any round does — and an empty delivery with
no cause recorded is in no class at all: nothing is given back, because nothing
was refused, and the run stops on its own repair budget. What such a round is
not charged is a review round, and that is the cap's own rule rather than this
class's — the cap counts only a verdict requiring repair against a change that
was present, so a reviewer shown an empty diff charges the item nothing whatever
it said, and what bounds a developer that delivers nothing is the run's repair
budget it spends doing it. [What spends a round and what does not](configuration.md#what-spends-a-round-and-what-does-not)
states the whole rule. The diff that
has to be empty is what **that round** added, which is not the same question as
whether the worktree differs from the base commit: a round of a repair grant runs
in the worktree earlier rounds already filled. Where the harness refused before
anything that round would have delivered could exist — no agent invoked, or an
invocation the machine never started — the round added nothing whatever the
worktree holds, and the refusal says so rather than the worktree being asked.
Where it applies, the run's record, the docket entry, and the thread all say so
beside the counters, because a stoppage whose last round was refused means
something different about how close the item is to its cap — including the two
cases where the item does not stand where it did: the round the harness
classified and could not write the return for, and the round another process is
credited with. Every one of those surfaces says both out loud rather than
claiming the item stands where it did.

That second case is the other direction this accounting can fail in, and it is
guarded the same way. A round is counted under the developer attempt that
produced it — the run it was made in and how many repairs into that run it is —
and a run picked up again after its process died is that same run at that same
attempt number, so the attempt alone would let a refusal in the new process give
back a round the process before it spent on a verdict the item really got. So the
count carries the process that charged it as well, and only that process is ever
given the round back; a return from any other is refused and the round left
spent.

Three of those four causes are recognized today. The last one is not: a stale
build does not refuse, it proceeds, so there is no refusal to hang the cause on.
Half of what recognizing it needs now exists — **every run records the revision
the harness that reserved it was built from**, alongside the account and the
configuration, and so does every line in the cost log, every conversation record,
and every round of an inter-role exchange. What is still missing is the other
half: which build carried out each triage decision, and the comparison between
the two at dispatch. Until that exists a stale dispatch reaches the class only
through whichever symptom it trips — which is how the field cases reached it, as a
handback holding none of its change — but it is now answerable after the fact
from the records alone, which is what those cases could not be.

What happens on approval depends on `approvals.integration`. This repository
sets it to `automatic`, so a run that passes its checks and is approved by the
reviewer is committed, fast-forwarded into the target branch, settled in Beads,
and its worktree and branch removed — the JSON reports the integrated commit and
what was cleaned up. Settled is closed for nearly every run, and the exception is
a change that discharges nothing: a landing the developer claimed does not
discharge the item, or an approval the reviewer gave to evidence rather than to
the work. An honest "not doable yet" lands its evidence like any other change and
puts the item back in the backlog parked, with the account recorded on it, rather
than closing an item against the evidence that says the work was not done.
[What a landing claims](#what-a-landing-claims) and
[what an approval approves](#what-an-approval-approves) are the whole of that.
A run that never gets that far because either role found the item unmeetable is
[an escalation](#saying-the-item-cannot-be-met), which integrates nothing. A
freshly generated configuration says `human` instead, so a new project preserves
the worktree for external integration until it opts in.
Either way the harness refuses `automatic` unless deterministic checks and a
reviewer agent both exist.

Development is parallel and integration is serial. Two runs may develop, check,
and review at the same time, but a run reaching its promotion phase waits its
turn: the harness takes a lease on the target branch out of the run state store
before it promotes and releases it once the promotion has settled, so at most
one promotion per target branch is ever in flight. The lease is an advisory file
lock, so it dies with the process holding it — a promotion whose process was
killed leaves no stale lock, and `yoyo reconcile` settles what it left behind.
No agent takes the lease or performs a promotion; the harness does both.

A fast-forward needs the target branch to still be where the run started from,
and it may not be: the run ahead of it in the queue can have promoted into it,
and committing to it yourself while a run is working moves it just as
effectively. The promotion fails closed either way, and the run then replays its
change onto where the target went, re-runs the checks, and gets a fresh
independent review before trying again. Losing the race spends nothing: a run
whose replays keep passing keeps replaying until it lands, each lost race said
in the item's thread as a note and never docketed.
`execution.integration_retries_before_reconciliation` bounds only the replays
that stop on the change — one that conflicts, or is handed back for a failing
check or a repair verdict — and the replay that takes the count past it stops
the run there, on the change; at `0` no replay may stop on the change. The earlier
approval never carries over, because the diff it approved is not the one that
would now be promoted. A replay that conflicts is never
resolved automatically, and it goes back to the developer that wrote the change
first: the change is moved onto where the target went with the conflict left in
the worktree as Git's markers, the same developer session settles it as a repair
attempt, and the result is checked and reviewed again before it is promoted. A
conflict is a replay that stopped on the change, so it is charged to the
integration budget as well as spending a repair attempt. Only a run with either
budget spent stops, both sides untouched, with a blocker on the item naming the
conflicting paths — and a repair granted in triage hands that same conflict to
that same session. The conflict is also written on the run's own record, before
the blocker is attempted on the tracker, so the run says what stopped it even
when that write fails — and the docket entry, the item's notes, and the refusal
of `yoyo triage resume` all name the conflict and `yoyo triage repair` from that
record rather than from the error's prose. A replay the harness itself killed —
timed out, cancelled, or stalled — is not a conflict, although it leaves the same
half-applied state: it is abandoned so the worktree is back on its branch, and
recorded as a stop outside the work, below, rather than handed to a person.

**A stop outside the work after approval costs nothing.** Not everything that
stops an approved change short of the target branch is a verdict on it, and
the ones that are not spend nothing. A promotion refused because the primary
checkout carried somebody's uncommitted edit, a tracker read that timed out
under load on the way to it, a forge or a network that went away — each ends
the run, and each is recorded on the run as an *integration stop*: which
cause outside the work it was, and which step the run was in. The cause is read
from the error that ended the run rather than from the run's prose afterwards,
in five ways: a dirty checkout by the sentinel the worktree manager declares,
a replay onto the moved target that the harness killed before it finished
(`replay-killed`) by the one it declares for that, a target branch the harness
would not catch up to the remote's before promoting (`diverged-target`) by the
sentinel the run's stop carries, a remote that refused the harness's SSH key or
forge login on the push or the fetch around it (`remote-auth-refused`, the
"Permission denied (publickey)" three approved changes each spent a re-run on)
by the one the worktree manager declares for that, and a tracker, forge, or network that did not answer by the [recovery
rule](operations.md#waiting-out-a-network-that-dropped)'s closed reading of the
error — the same reading that decides what the harness waits out at the
boundaries that have a window, applied to a step that has none. What is read
is the step's own failure, and not the failure of any write the run then
attempted about it: a run that stops and then cannot record the stop — a
blocker the tracker did not take in time, a replayed base the store would not
save — ends on both, and the write's failure is transport-shaped whatever the
stop was. On yoyodyne-ifd.441 a replay conflict whose blocker write timed out
was read from its tail as a transport failure, and the docket sent the
development manager to a resume that would have met the conflict again. A
replay conflict is refused by its own sentinel besides, so it is never an
integration stop whatever wrapped it. A refused
credential is never waited out, even where SSH's closing "Connection closed"
would read as a dropped network on its own. Nothing about
the change is in question, so nothing about it is anybody's to decide. A
diverged target and a refused credential do need a person — to settle the
branches, or to load the key or renew the login — but what they settle is the
environment and not the change, so the approval stands through it. `yoyo triage resume
<run-id>` resumes the run at the promotion it stopped short of — replay onto
where the target now stands, push, merge request — with its approval standing,
and it charges the item nothing: no review round, no repair grant, no re-run.
The resumption is recorded on the run as a continuation rather than an
attempt, so the repair count and the review evidence are exactly what the
reviewer left, and every counter on the item's triage record stands where the
review left it. While the promotion is going, `yoyo status` says
**approved, resuming integration** of the run rather than the bare phase. A
run can be resumed each time the environment stops it, up to sixteen times —
a bound on the run's record rather than a cap on the item, refused before
anything is written, because an environment that has refused one promotion
that often is a machine somebody has to look at rather than a run to resume
again. The one thing that leaves the path before that is a replay that
conflicts, which re-enters the conflict
path above — handed back to the change's developer while the run has a repair
attempt, and stopped for a person with both sides intact where it has none — and
is never recorded as a stop the harness can resume past. Before this existed
every verb that could pick such a run up spent something for it, and
yoyodyne-ifd.309's approved change cost four operator overrides to reach the
target, none of them for a verdict.

The resume asks everything that can refuse before it writes anything: the
run's own record has to say it is one of these, the primary checkout has to
be one a promotion can be made from again, the cause of a `diverged-target`
stop has to be gone — the local target fast-forwards onto the remote's again —
and so does the cause of a `remote-auth-refused` one — both remotes list the
target branch with the harness's credential — each refused in words that say
what clears it while it still stands, the preserved worktree has to be
as the harness left it and still hold the approved change, the item must not
be closed or waiting on other work, and the harness has to have a free slot —
a full one waits rather than refusing, and so does a held intake. A refused
resume leaves the run exactly as it stopped, and asking again once the cause
has cleared resumes the same run. A worktree the [convergence
sweep](operations.md#recovering-interrupted-runs) retired while the run stood
stopped — the fourth way 309's change was held up — is not a refusal either:
the branch still holds the reviewed commit, so the resume puts the checkout
back from the branch at exactly that commit before it asks the two worktree
questions, and records on the run that the checkout is back as it does so.
What it will not do is restore past a branch that has moved or a sweep that
captured uncommitted work off the directory, because what a restored
checkout would then promote is not what was reviewed; both refuse to a
person, and only a deleted branch ends the change for good. The docket entry for such a stop says all of
this itself: it names the harness as the next mover and the verb that resumes
it, so the development manager is not asked to choose among decisions that
each spend something for a stop that was never hers to decide. That holds while
the branch is there, and the docket asks the repository for it each time it is
built for her, by the same look and the same rule the pull's hold and `yoyo
status` ask. A stop whose branch is gone names no resume, because the resume
would refuse: the entry says what was found — the branch checked and not there,
and the worktree whichever way it was found — and that a re-run is the way on,
and names her as the next mover, or the harness where a decision of hers about
the stoppage is already recorded and not yet carried out — or, where the
worktree is gone too and nothing is decided, the next pull, since nothing then
holds the item. `yoyo status` says the same on the run's integration-stop line,
`yoyo triage repair` refuses such a run in the same words, and the channel line
the stop is said in ends on them, asking the repository the same way as it is
said.

## What an item may ask of a run

The gate above refuses a *change* that touches an artifact home the item did not
grant. It cannot refuse an *item* whose done-condition lives there — "the
design's query list marks the query as existing", "reconcile the design
document's `yoyo status` entry", "her ruling is recorded on the design" — and a
run handed one of those does what the gate lets it and parks on the rest, with
the reviewer naming the clause after the run has spent itself. Three items did
that in one week (`yoyodyne-ifd.141.1`, `.63`, and `.68.25` before them), and
each was put right the same way afterwards: the clause came out of the item, and
the architect amended the document through the governed path. The development
manager's 2026-09-03 checklist said where that belongs — a precondition or an
edit the done-means implies is structure at admission, not a finding at review.

So it is structure. **A done-condition that names a document a developer run
may not write is refused where the item is written**, unless the item grants the
path. A creation, an update that rewrites the description, or a proposal whose
"Done means" clauses or acceptance criteria name a path under the product,
designs, decisions, or invariants homes — or a document one of those homes owns,
by its name, so "the slack-reporting design" names
`docs/designs/slack-reporting-design.md` as surely as the path does — is refused
with the clause quoted and the fix named, and the fix is one of two things:
take the clause out of what done means and say that the document's owner (the
architect for a design or a decision record, the Lead Product Manager for a product
artifact) amends it through the governed path once the run's summary names what
there is to record; or, where the change behind it is already decided, carry
the grant on a `Protected-path grant:` line. Only the done-conditions are read —
the acceptance criteria whole, and in the description the sentences from a
"Done means" to the end of their paragraph — because an item cites these
documents in nearly every description as the design it builds against or the
ruling it obeys, and a citation is not a condition; measured over the 596 items
this tracker held when the check was written, reading every clause would have
refused a fifth of the backlog, and reading the done-conditions refuses ten,
each of which names a document as something the work leaves in a state.

**The run repeats the test on the item it is handed**, over the acceptance
criteria as well as the description, before it claims the item: an item whose
criteria were written with the tracker's own command, or admitted before the
check existed, is refused at the start of the run rather than parked at the end
of it, and nothing is spent — no worktree, no claim, no attempt. The refusal
says the same thing admission's does, quoting the clause, so what a run refuses
and what admission would have refused cannot come apart. Neither check reaches
`.claude/settings.json`, `.claude/settings.local.json`, or `.yoyodyne/roles/`,
which stay [beyond any grant](configuration.md#protected-paths-in-a-developers-change)
whatever an item says.

## What a landing claims

Whether a change landed and whether it discharged the item it was made for are
two different facts, and the harness cannot derive one from the other. Closure
used to follow the promotion alone, which produced exactly the defect you would
expect: a run that found its item was not doable yet answered with a diagnosis
saying so in bold, the diagnosis was good evidence and integrated, and the item
closed against it — retiring a marker the diagnosis existed to keep.

So the developer says which kind of landing it made, and the closure follows the
claim. Nearly every run claims nothing, which is the ordinary landing and closes
the item. A run that has evidence to land and no work to discharge says so in one
fenced block:

````
```yoyodyne-landing
{"outcome":"evidence","why":"the design this needs has not landed, so what this run landed is the diagnosis"}
```
````

`"discharged"` is the ordinary landing said out loud; `"evidence"` is the one
that withholds the closure. A run that claims evidence is not a failed run and is
not handed to anybody: its change is reviewed and promoted exactly as any other
is, and what changes is only what becomes of the item — it goes back to the
backlog with the run named on it and the developer's own account of why, rather
than being recorded as done.

### Where an undischarged item is left

Back in the backlog is not back in the queue. An item returned open with nothing
marking it is the next thing an autonomous pull selects, which buys another run,
another diagnosis of the impediment the last run just diagnosed, and a full
integration each cycle — and because the run that landed the evidence succeeded,
no brake counts it. So a landing that does not discharge **parks** its item, with
the developer's account as the parking reason. Parking is the machinery the
Lead Product Manager already has: the item keeps its place in the order, says why it
is not to be started, and is offered by no pull however far the queue drains,
until somebody releases it with `unpark`. That is what the developer's "why" is
written for — it is the sentence whoever considers releasing the item reads, so
it names what would release it.

The one alternative is a marker selection honours. Where the impediment is itself
a work item, the landing names it and the item is left open waiting on that item
instead of parked:

````
```yoyodyne-landing
{"outcome":"evidence","why":"the conversion needs the management-conversion design first","blocked_by":"yoyodyne-ifd.209.25"}
```
````

A dependency releases itself — the item is offered again as soon as the
impediment closes, with nobody having to remember — where a parking waits for a
person. There is deliberately no third answer: a claim either names what it is
waiting for or takes the parking, and bare openness is not on offer.

The marker is developer-written text, so the harness resolves it against the
tracker before it acts on it. It makes the item wait only where the tracker has
the named work, that work is not the item itself, is not already closed, and does
not already wait on this item. The first two would be refused as a dependency;
the third would be written and hold nothing, which is worse, because the item
would sit in the queue unparked behind a blocker that is already satisfied; the
fourth is the cycle a follow-on item makes ordinary.

Anything else takes the parking. The item is held back either way, and the
parking reason and the item's notes say which marker was asked for and why it was
not used. That is also what happens if the tracker refuses the dependency anyway —
a cycle further round the graph than the harness looked, say. A refusal that late
moves the run's own record with it, so `yoyo status` and the conversation say the
item was parked rather than naming a wait it never got. Nothing about the
marker can cost the run its landing: the change is promoted before any of this is
decided, so failing here would leave the item claimed with nobody watching it,
which is the one outcome worse than parking it.

A settlement never lifts a parking. An item somebody had parked and then named
for a run anyway comes back still parked, with the dependency added underneath.
Where the run parks it instead, the run's reason replaces the one that was there
and the superseded one is written into the item's notes, because a parking holds
one value and the decision it replaced still has to be readable.

Three things follow from the claim deciding something, which the report and
amendment channels do not:

- The reviewer is shown it, above the patch and inside the untrusted evidence,
  so a diagnosis is judged as a diagnosis rather than as a missing
  implementation. A change that claims evidence and is plainly the
  implementation is a finding, for the mirror-image reason. The default is shown
  too, on the nearly-every run that claims nothing: the reviewer is told this
  change closes the item unless its own approval says otherwise. Without that the
  claim that closes items was the one claim nobody ever saw — which is how a
  diagnosis came to be approved, and its item closed, by a reviewer that called
  the change evidence in its own summary
  (`docs/diagnoses/yoyodyne-ifd-209-26-closure-routes.md`).
- A claim the harness cannot read withholds the closure rather than being
  swallowed the way an unreadable report is. The developer wrote a block, so it
  was trying to say something about the closure; an item left open is something
  a person can settle, and a false closure is the thing nobody sees.
- It is durable, because the closure is not always made by the process that read
  it. A merge the forge only queued is settled by a later `yoyo reconcile`, and
  that sweep decides from the recorded claim rather than from the promotion.

### A reply that accounts for nothing

All four channels out of a developer — this claim, the summary, the reports, the
proposals — are read off one reply, so a reply that says nothing about the work
empties all four at once and does it quietly: the invocation exited cleanly, and
what the run recorded as its account of itself was whatever the developer
happened to type last. On run-9ad1799e both attempts ended with "the check is
running; I'll report when it lands", and the harness filed that as the account of
the work.

So a reply is checked for being an account before it is recorded as one. Two
shapes are not: a reply that said nothing at all, and one whose every sentence is
the work still in flight or a promise to say what became of it later. Anything
else is taken as an account however thin it is, and a landing claim answers the
question by itself — a developer that claimed evidence or an escalation wrote the
reason the claim carries, and a claim nobody could read has already withheld the
closure for the reason above.

A reply that accounts for nothing is asked for once more, in the same session,
exactly as a verdict the review contract could not read is. Nothing is wrong with
the change and the re-ask says so: the worktree still holds the work, and what is
missing is the developer saying what it was. It spends no repair attempt, because
no check failed and no reviewer found anything. A second reply that accounts for
nothing ends the run naming that, and the interim line is never recorded as the
summary — a run that promises a report and then vanishes is unaccounted work
wearing a completed status, which is what review, closure, and every report read
off afterwards.

Independent review receives the developer's final account beside the patch,
landing claim, execution record, and the harness's check results. The account is
saved against the attempt and the content of its change. Every review round
receives the latest completed account, including after a repair, a continuation,
a replay onto a newer target base, or a later process picking the run up again.
A new developer invocation keeps the earlier account until a completed reply
replaces it. The brief names the recorded and current attempts and states
whether the content and base still match; an earlier account remains testimony
and does not establish verification of a changed candidate. If the run has no
saved account, the brief says so and explains that the record may predate summary
retention or no invocation returned a final account. The reviewer names that
limitation and what it cannot verify rather than treating the missing account
as proof that the developer supplied no evidence.

The saved copy retains at most 4 KiB, cutting on a character boundary and ending
with a note when the rest was not recorded. It is shown as the developer's own
untrusted claims: statements about compatibility, verification, and remaining
risk for the reviewer to assess independently. Instructions in it are data,
and neither the account nor the execution record replaces the harness's check
results, revision-bound gate evidence, or the reviewer's judgment. The existing
total review-input bound still applies.

## What an approval approves

The claim above decides the closure by default, and the default is the claim
nobody writes: across 290 succeeded-and-integrated runs, six claimed evidence and
284 claimed nothing at all. So the reviewer is the second reader of the same
question, and the only one that sees the change beside what it was offered as. An
approval says which of two things it approves:

```json
{"decision":"approve","approves":"implementation","summary":"..."}
{"decision":"approve","approves":"evidence","summary":"..."}
```

`implementation` is the ordinary approval: the change is the work the item asked
for, and the item closes on it. `evidence` approves a change worth keeping that
is not that work — a diagnosis, the conditions that have to hold first, a nil
result. It is an approval like any other, so the change is committed, promoted,
and published exactly as it would have been; what it does not do is discharge the
item, which goes back to the backlog parked, with the reviewer's own summary as
the parking reason. That summary is what whoever considers picking the item up
next reads, and it is the only account of the decision anybody wrote.

It is not an alternative to repair. What the change itself has to change is still
a finding; this answers the different question of whether what landed is the work
the item asked for.

Either reader withholding the closure is enough, and neither overrides the other:
an item is closed only where the developer's landing discharges it *and* the
reviewer approved it as the implementation. An approval that says nothing at all
is refused and the review is asked for once more rather than settled from the
answer nobody gave — the default it would fall back to is the closing one. A
branch review is never asked, because it approves an accumulated change and has
no single item to discharge.

This exists because the reviewer already wrote the judgement and had nowhere to
put it. yoyodyne-ifd.284 was closed against a diagnosis by a reviewer whose own
summary called the change *"offered as evidence rather than implementation"* —
prose nothing reads, beside a claim nobody wrote
(`docs/diagnoses/yoyodyne-ifd-209-26-closure-routes.md`).

## Saying the item cannot be met

Both of the answers above are about a change: this is the work, or this is
evidence worth keeping. Neither is the answer for a work item nothing could
satisfy — one whose acceptance criteria contradict a delivered invariant or a
recorded ruling, ask for something no change in the repository can produce, or
describe work that has to be replanned before anybody can do it.

Saying that used to cost more than not saying it, which is backwards. A reviewer
that saw it could only ask for repair, round after round, until the item's budget
ran out and the stoppage reached the development manager that way:
yoyodyne-ifd.100.1 spent three runs and six review rounds on repair verdicts
against criteria a design ruling had already forbidden. A developer that saw it
could only land a diagnosis, which is a landing of evidence and parks the item
without putting the finding in front of anybody who can act on it.

So both roles have one verb for it. The developer claims it as a landing outcome:

````
```yoyodyne-landing
{"outcome":"escalate","why":"the acceptance criteria ask for what the entanglement ruling forbids, so no change here meets them"}
```
````

and the reviewer decides it as a verdict:

```json
{"decision":"escalate","summary":"the criteria ask for the conversion the ruling forbade; this needs replanning"}
```

Whichever raises it, the run ends in the round it was raised in. Nothing further
is bought: a developer's escalation runs no checks, buys no review, and publishes
nothing, and a reviewer's hands nothing back, so it costs no repair attempt — and
no review round against the item's cap either, because the cap counts only a
verdict requiring repair and an escalation is the reviewer saying the item cannot
be met rather than arguing with the change (see
[what spends a round and what does not](configuration.md#what-spends-a-round-and-what-does-not)).
The run is recorded as having succeeded,
because it did what it was for — recording it as a failure would count honesty
about an unmeetable item in the same tally as a broken toolchain, and the
failure-storm brake counts that tally. `yoyo status` says **succeeded**, which
is what the run did; what it raised is on the item's notes, in its parking
reason, and on the triage docket.

The item neither closes nor goes back bare-pullable. It is parked, in the words
of whichever role raised it, and the parking reason says who releases it: the
item's owner, once it has amended the item so it can be met, or the development
manager, by retiring the raise. And the escalation is docketed as the run ends,
so it reaches her the way a stopped run does — as an *item raised as
unmeetable* on the triage docket, carrying the account, what was preserved, and
what the item has spent. The decision she takes is hers: replan, park,
resequence, or redirect. Once the item is amended, what becomes of the raising
run's change is hers too, and a repair is not among the answers, because the run
did not stop: `rerun` starts the item again from that change once the owner has
released the parking, and `retire-raise` ends the raise where the amendment made
the change moot. The owner's release ends the raise and clears any blocked
status left while it stood, so a pull can select the item.
[Deciding what becomes of stopped work](conversation.md#deciding-what-becomes-of-stopped-work)
is the whole of it.

An escalation takes no `blocked_by`. A developer that can name the impediment as
another work item has the evidence landing above for exactly that; this verb is
the answer for when nothing in the backlog is what the item is waiting on, and
the parking is what holds it while the decision is pending.

It is not the answer for a run that could not finish. That is a failure and is
reported as one. Nor is it an alternative to repair: a change that has work left
to do is a repair however much of it there is, and an escalation says that no
change would help. A branch review cannot escalate at all, because it has no
single item to raise and nowhere to send the decision.

## A decision about work in flight

The Lead Product Manager decides what is admitted, and sometimes she decides it
about an item that already has a developer run going: another item now does its
work, its scope was cut after the run started, or it is not going to be done at
all. Whether that run stops is not hers to decide — stopping a run in flight is
the development manager's — and until this existed nothing carried the one
decision to the other. A note on the item is read when a stoppage on it is
decided, never while its run is still going, and the ask channel decides
nothing. On 2026-09-27 the operator's assistant relayed exactly that by hand, for
yoyodyne-ifd.428.34 superseded by yoyodyne-ifd.398, and by the operator's rule a
hand step routed to a person is a defect.

So the Lead Product Manager records it as an action in her conversation:

```json
{"action":"inflight","id":"yoyodyne-ifd.428.34","decision":"superseded","superseded_by":"yoyodyne-ifd.398","reason":"398 rebuilds this card whole, so this run's change would be thrown away"}
```

`decision` is `superseded`, `narrowed`, or `retired`; `superseded_by` names the
item doing the work instead, and is required on a supersession. She names the
item and not the run: the harness finds the run in flight from its own records,
and refuses the action where there is none, because a decision about an item
with nothing running is a `retire`, a `park`, or an `update` she makes directly.
Nothing about the item changes — not its status, not its place in the order —
and retiring it stays hers, once the run has stopped or finished.

What the action does is docket the decision for the development manager at
once, as a *product decision about a run in flight*: what was decided, why, by
whom, the item that supersedes it, and the run's branch and worktree. It is the
one entry on her docket about a run that has not stopped, and it is put ahead of
the stoppages there, because the run goes on spending while the question waits.
Where the run stands is read again from its own record every time the docket is
built for her, so her sweep or her triage conversation shows the phase it has
reached by then rather than the one it was in when the decision was made. The
item's notes say the same, attributed to the Lead Product Manager's
conversation.

The entry asks her one thing: does the run stop, or finish? `stop` is the stop
[below](#letting-the-harness-choose-the-work) — the harness writes the request
the operator's `/stop` writes, in her name, and the run ends cancelled at its
next boundary with its change preserved and its slot free. `proceed` lets it
finish and be reviewed and promoted as it would have been, and records that she
looked; it is for the run nearly done, or building something the superseding
work keeps. Either is a triage decision naming the run, recorded on the item's
triage record and its notes with her reason, and either closes the entry — and
neither decides a stoppage, so a run she let finish that stops anyway reaches her
as a stoppage like any other. Both are refused for a run that has already ended.

A run that ends before she decides settles the entry itself. One that stopped
holding its change is docketed as that stoppage, with the product decision
folded beneath it, and her decision about the stoppage settles both. One that
finished, or that the operator stopped, leaves nothing to stop, and the next
build of the docket closes the entry saying so; one whose item was closed closes
with the item. While the entry stands undecided and its run is in flight, `yoyo
status` names it under what needs a human, as the development manager's move,
and so do the dashboard and the channel, from the same read model.

## Letting the harness choose the work

`/work <id>` and `yoyo run <id>` are you naming an item. `yoyo work` is the
harness choosing:

```sh
./bin/yoyo work                 # drain what is ready
./bin/yoyo work --limit 2       # start two runs and stop choosing
./bin/yoyo work --watch         # stay open, pulling work as it becomes ready
./bin/yoyo work --json
```

It reads the admitted work in the order the Lead Product Manager set, works out which
of it can be pulled, and starts as many of those at once as
`execution.max_concurrent_developers` leaves free — which is `1` until you raise
it. Each run is the run above: its own branch, its own worktree, the same checks,
the same independent reviewer, the same serial promotion. The command returns
once every run it started has ended.

Which items those are is asked of the records rather than of the item's status.
For open work it is the tracker's own ready list, because a blocker lives in the
tracker's dependency graph and a status listing does not reliably carry one. For
work at status `blocked` it is that item's own blocking dependencies, because the
ready list is computed from the same status field and so can never offer it. That
distinction is the fix for a specific failure: a status is written when work
stops and nothing rewrites it when what stopped it clears, so on 2026-09-04
forty-one items sat at `blocked` with every dependency they had already closed —
two-thirds of the backlog, two p0 items among it — and the line was idle for a
morning with nothing saying why.

**The claim makes the same reading.** The tracker's own claim refuses a `blocked`
item on the status field alone, so releasing those items made every one of them
selectable and unclaimable at once: one of them was dispatched twenty-nine times
between 2026-09-06 and 2026-09-07, each run dying at the claim before it took
anything. So a claim the tracker refuses for the status re-reads the item, asks
what it actually waits on, and — where that is nothing unfinished — clears the
stale status, recording in the item's notes what it is doing and what was
refused. An item that really does wait on unfinished work is refused with that
work named, so the run's record says which of the two it was. Re-reading under
the claim also settles the case where the item's state genuinely moved after it
was selected: what is judged is the state that is then claimed.

The write is not the clear; the read that returns `open` is. After the write the
claim reads the status back, up to five times a second apart, and takes the item
only on a read that returns `open`. On 2026-09-20 the claim on yoyodyne-ifd.415
recorded its clear as made and the tracker refused the claim that followed on the
same status, so the re-run tripped on its own correction. A claim the tracker
still refuses on the status after a read returned `open` is retried on a later
read within the same bound rather than ending the claim: on 2026-09-22 and
2026-09-23 two recorded re-runs (yoyodyne-ifd.432.10 and yoyodyne-ifd.117.3)
cleared the status, read it back, and were refused the claim with `issue not
claimable: status blocked`, and each item was claimed only by a later pull. A
clear no claim within that bound is taken on is reported as unconfirmed, with
the status the tracker returned and how many claims it refused, and never as
cleared; a note saying so is appended to the item, and the item is left for the
next pull rather than claimed. Either way the run's record says which of the
three endings the clear had — confirmed on the first read, confirmed on a later
one, or never confirmed — with the refused claims counted beside it, and `yoyo
status` prints it on the run.

So the order is always the same: the claim bd refused on the status, then the
clear, then the status read back, and only then the claim. Both writes are
recorded on the item. The clear's note quotes the refusal that came before it
and says so, ahead of saying that the claim waits on the read back; once the
claim lands, a second note says which read returned `open` and how many claims
bd refused after it. A claim that still cannot be made after the clear is
refused with that order spelled out first — the refusal, the clear, what the
reads returned, whether any claim followed — and bd's own words after it. On
2026-09-28 the reviewer of yoyodyne-ifd.428.44 read the old note, which quoted
bd's refusal after promising a claim, as bd refusing the claim that followed the
clear, when the claim had in fact landed.

**This is the same claim on every path that starts a run.** A pull choosing the
item, you naming it with `yoyo run`, and the harness carrying out a re-run the
development manager recorded all claim through it, so a re-run carried out
against an item at `blocked` clears the status, confirms it, and claims the item
on the pull that cleared it, under the same bound and with the same account on
the item and the run. The carry-out reads the item before it claims its re-run,
and accepts `blocked` there because the claim is what corrects it.

What still holds a blocked item back is a **hold**, which is the harness's own
durable record rather than a field: a run that stopped on the item and whose
change is still on a branch or in a checkout, a stoppage put in front of the
development manager that nobody has decided about, a stopped run about which
she has recorded a decision the harness has still to carry out, or a run that
integrated the item's change and could not finish publishing it. Whether a
stopped run's change is still there is looked for in the repository as the hold
is read, not taken from the run's own removal flags — a flag is a field
something has to remember to write, and on 2026-09-19 the Lead Product Manager's
repair cleared yoyodyne-ifd.372 on one while the item's own notes still said the
run's branch and worktree were checked and there.

**The same look is what every surface says preservation from.** The hold, the
[claim audit](operations.md#claims-with-nothing-working-on-them) and the note a
release writes on the item, the triage docket entry — as the run stops, and again
each time the docket is built into the development manager's context — `yoyo
status`, `yoyo cost`, the Slack sink, the dashboard's item card, and the
repair, re-run, and integration-resume guards all ask the repository for the run's
branch and checkout at the moment they write, and each says which it found and
when: `checked and there`, `checked and NOT there`, or `not checked` with the
reason. Preservation is a fact of the repository, not of the removal flags: a branch
or checkout still there is preserved work even when the flags say it was removed,
and flags saying it was kept do not make a missing branch or checkout present.
A guard that finds nothing names the branch and checkout it looked for; a look
that fails says it could not check rather than declaring the change removed.
A decided repair of a run stopped at its check-stage bound continues the same
run at its checks on the finished change, recording the grant as carried out
without another developer attempt or review round for the continuation.
On 2026-09-23
run-838ffc48's flags said its artifacts were gone and the claim audit released
yoyodyne-ifd.432.10 saying only that nothing was working on it; the development
manager crossed the item's re-run cap reasoning the run had preserved nothing,
while its branch held the approved change. A release written before the audit
looked is corrected by the convergence sweep, which appends to the item the
branch it found standing, once.

**A run that ended `failed` holding its change is held exactly as one that ended
`stopped` is.** The two look different in a listing and are the same fact to a
reader: a run that fails inside its own process deliberately hands nobody a
blocker — the harness may yet resume it, and a blocked item is one it would
refuse to resume — so its record ends `failed` with no blocker on it while its
branch sits there exactly as a stoppage's does. Reading only the blocker is what
cost yoyodyne-ifd.436.4 a second run on 2026-09-22: run-b0b6d18d's change was
approved and then stopped short of `main` by a tracker read that timed out, and
the pull found nothing holding the item. The [claim
audit](operations.md#claims-with-nothing-working-on-them) reads the same endings
and leaves such an item's claim alone for the same reason.

None of these is released by a pass deciding to. Releasing a stoppage would start a fresh run on top of a change that is still
there, and releasing an outstanding publication would start one over work the
promotion has already put on the target branch — which is what
[yoyodyne-ifd.295](operations.md#recovering-interrupted-runs)
cost, three developer runs and three reviews each re-deriving that the change
was already on `main`. So a hold is lifted only by the development manager
deciding the stoppage, by her decision being carried out, by the escalation
being answered, or by the publication being settled — at which point the records
stop saying the item is held, and it becomes pullable without anybody having
edited its status. The first and the third are a person's. The second is the
pass's own: a repair, a re-run, or a merge re-arm she recorded is fired at the
next pull, under the gates the carry-out paragraph further down names, and the hold goes with it
— so an item awaiting carry-out waits on an interval, unless a permanent
refusal requires her to record a new decision. The fourth is
[`yoyo reconcile`](operations.md#recovering-interrupted-runs)'s: every sweep
asks the remote again whether it carries a publication the record says is
unfinished, and where it does — a merge that landed among others, a dropped merge
somebody then made by hand, a consumed branch somebody removed — the sweep
finishes the record, and the hold, the heartbeat's count, the item's
`Publication outstanding` line, and the docket entry all stop with it. A pass
that cannot read those records holds every blocked item rather than releasing
work whose hold it could not see.

The claim clearing a stale status is the harness meeting one; the Lead Product
Manager corrects them deliberately, in her own pass over the queue, along with
dependencies on work that closed and attributions the goals no longer state. She
is refused by the same holds and reads them from the same records, so an item
held after a stopped run is reported with its reason and left alone there too. What that
looks like from the conversation is
[backlog state that has stopped being true](conversation.md#backlog-state-that-has-stopped-being-true).

Capacity is enforced where a run is reserved rather than by the scheduler, so two
of these, or one of these and a `yoyo run` beside it, share one limit rather than
getting one each. A run that loses the race for the last free slot is reported as
declined and the pass exits zero: that is two schedulers doing exactly what they
should, not a failure.

**A slot freed by any run is refilled at the next poll**, whatever else is still
in flight and whichever process hosted the run that freed it. A watching session
with a slot free, or with every slot taken and any of them held by another
process's run, waits for whichever comes first of one of its own runs ending and
`execution.work_poll`, and at each poll reads the limit again against every run
in flight wherever it runs. Only a session whose every slot is held by its own
runs waits for one of them to end, because then nothing else can change its
answer. On 2026-09-26 a session did not refill: two of three runs ended early,
the pulls they woke found nothing they could start at that moment, and the
session then waited only on the third run — so two slots stood empty for over an
hour beside twenty-nine ready items. The same stall held a full session's slots
that another process's runs held, which a bounded drain makes the ordinary case:
when those runs ended, the session did not notice until one of its own did.
Every poll that starts work says in the watch log how many slots it filled, of
how many it found free, and how many of the session's own runs were already in
flight beside them, and which slots freed since the last poll and whose runs
held them — `filled 1 of 1 free developer slot, beside 1 run this session already
had in flight; …; 1 developer slot freed since the last poll: run-… over
yoyodyne-ifd.12, another process's run` — so a slot left empty is visible there
rather than inferred from the silence.

Twelve things keep an item out of a pass, and the pass accounts for them at two
different grains. The first nine are named against the item, because nothing
else would report that this particular item was passed over; the last three are
facts about the pass rather than about any one item. The
[configuration guide](configuration.md#scheduling-ready-work) lists the same
twelve in the same order, and a test fails when the two lists differ:

<!-- selection-rules: the same names, in the same order, as docs/configuration.md and docs/configuration/runs.md; internal/doclink/selectionrules_test.go holds them together -->
1. **An unresolved directive** withholds the item until a person resolves the
   directive, and is named in the directive's own words.
2. **Unfinished children that carry its execution** withhold a container while
   any child it was broken into is queued, blocked, or claimed, and release it
   once the last of them leaves the backlog.
3. **A race with work in flight** withholds an item that shares an epic
   decomposition or files with a run in flight, and releases it at the first
   pull after that run ends.
4. **A conversation executor** withholds an item whose `executor` names a
   persona conversation from every developer run; nothing clears it, and what
   moves the item is somebody opening the conversation it names.
5. **Parking** withholds an item the Lead Product Manager parked however far the
   queue drains, and only her `unpark` releases it.
6. **A hold** withholds an item whose stopped run left its change on a branch,
   or whose publication did not finish, until the development manager's
   decision is carried out, the escalation is answered, or `yoyo reconcile`
   settles the publication.
7. **A step only a person can take** withholds an item that declares a
   `human-gate:` nobody has recorded taking, and only a person's
   `yoyo gate record <name> --for <item>` releases it; closing an item never does.
8. **A prerequisite the tree does not meet** withholds an item that pinpoints
   code the repository no longer has, or says in its own words that something
   must land first; a pinpoint releases it when the code lands, and a sentence
   when the item is amended or the dependency recorded.
9. **A label another slot prefers** withholds an item every free developer slot
   walked past for its preferred label, and the next slot with no preference to
   come free — or the preferring slot, once its label's work is exhausted —
   releases it.
10. **The tracker not calling it ready** withholds an item with unfinished
    dependencies or a status that is not open, and the tracker's own readiness
    releases it.
11. **A run already in flight for it** withholds the item while that run lasts,
    and the run ending releases it.
12. **No free developer slot** withholds everything once the slots are taken,
    and any run ending releases one.
<!-- /selection-rules -->

The children rule is there because a decomposed epic and the child that does
its work are both reported as ready to pull, so a scheduler that did not know
the difference would buy the same change twice — two developers rewriting one
file, the second of them guaranteed a conflict at integration. A race is
sequenced behind the run it would have raced rather than started beside it,
with that run and what the two share both named. A conversation executor is
passed over with what carries the item named, which the paragraph after next is
about, and a parked item with the parking reason named, which the paragraph
after that is about. A **held** item — a stoppage whose change is still on a
branch, one nobody has decided about, or a publication that did not finish over
work already integrated, in the sense the hold paragraph above gives it — is
passed over with the hold named. A stoppage nobody has decided about is, like
the parking, not a wait for anything and will not clear on its own; one she has
decided into something for the harness to do — a repair handed back, a re-run, a
merge re-armed — is a wait on the pass carrying that decision out, which the next pull
does unless a gate stops it — and a gate that stops it is written onto the item
and her docket rather than left silent. An unfinished publication is the other
hold that is a wait: the next `yoyo reconcile` re-asks the remote, and a merge
the forge has since made — queued and then landed, landed among others, or made
by hand after a drop — settles on that sweep with nobody acting, while a merge
the forge dropped and nobody has made stays a person's. A stoppage is passed over
as one of two things rather than one, because the two have different next
movers: an item **awaiting a decision** is the development manager's to settle,
and one **awaiting carry-out of a decision** is one she has settled into one of
the three decisions that buy another attempt — a repair, a re-run, a merge
re-arm — which the harness has not yet acted on. A stoppage she settled by
waiting, re-scoping or escalating leaves the harness nothing to carry out, so it
is not in that wait.

An approved change the environment stopped short of its promotion — the
[integration stop](#how-work-flows-once-you-approve-it) above — is in the second
wait without her having decided anything, and that is deliberate rather than a miscount: the reviewer decided,
the environment got in the way, and what the item waits on is the harness
resuming the promotion with `yoyo triage resume`. The hold names that verb, and
it names it first, ahead of what was found of the change, so the part that says
what to do survives a rendering that cuts the reason to a line. The development
manager's docket says the same thing on the same stoppage, because an item given
two next movers is a disagreement only you could settle. Both say it only while
the run's branch is there, which the resume needs and which both ask the
repository for: once it is gone the stop is held exactly as any other stoppage
is — held while its worktree survives or a decision about it stands, and let go
otherwise — and the hold and the docket both say the branch is gone and that a
re-run is the way on, naming the development manager, the harness where her
decision is waiting to be carried out, or — where the hold has let the item go —
the next pull. The
[claim audit](operations.md#claims-with-nothing-working-on-them) reads the same
look and gives such a run's claim back once branch and checkout are both gone, so
where nothing is decided the pull and the audit both let the item go; where a
re-run is decided the hold is the harness's carry-out, which takes a claim of its
own, and neither names a resume.
Reporting both as a single class is what made thirty-three already-decided items
read as a decision backlog for days on 2026-09-07. And an item **the tree is not ready
for** — one that pinpoints code the repository no longer has, or that says in its
own words that something has to land first — is passed over with the unmet
prerequisite named and routed to the development manager's docket, which the paragraphs
below on the unready tree are about. And an item every free developer slot
**walked past for its preferred label** — an unlabelled item ranked above the
labelled one a preferring slot pulled, with no slot preferring nothing free to
take it — is passed over as **left for another developer slot** rather than as
deferred, naming the slot and what it pulled ahead of the item: it waits on
nothing about itself, and the next slot with no preference to come free takes
it in the order, or the preferring slot does once its label's work is exhausted.
[A developer slot that prefers a label](configuration.md#a-developer-slot-that-prefers-a-label)
is how a slot comes to prefer one. And an item **held by a step only a person
can take** is passed over with that step named and with what records it, which
the last of the paragraphs below is about. The other
three — nothing reporting an item as ready, a run for it already being in
flight anywhere, and no free slot — are facts about the pass rather than about any
one item, so that is how they are reported: the stop reason says which of them
ended the choosing, and
a pass that got as far as reading the queue prints how many items were admitted,
how many of them could be pulled, and how many slots were taken. Counts
rather than a list, deliberately — a line per unready item would be a line per
backlog entry on every pass, which is how a listing stops being read. A pass that
stopped before reading the queue at all, because you were holding intake or the
machine was already full, says nothing about the backlog rather than reporting
zeroes it never looked up.

Sequencing is a wait rather than a refusal. Two
items race when one is the epic the other was broken out of, or when the files
they will change overlap. Being filed under one epic is deliberately not a
third: an epic is as often a heading the backlog is filed under as it is one
piece of work broken into several, nothing structural tells the two apart, and
counting every pair of children as a race held most of the queue behind
whichever of them started first — 29 of 109 unfinished items behind one epic on
the reading of 2026-09-22. What a real decomposition's children share is caught
by the surfaces below, which read what the items say rather than what their
filing implies.
An item says which
files those are by naming them after `conflict-surface:` on a line of its own, in
its title, description, design guidance, or acceptance criteria — the fields
somebody authored, not the notes the harness appends each run's record to — and an
item that declares nothing has those same fields read for the files it plainly
names. That inference is deliberately narrow, taking a path with a separator and
an extension on the end and nothing else: a surface invented out of prose holds
unrelated work back, and unrelated work running at once is what the concurrency is
for. Nothing here enforces anything — the promotion lease still serializes
integration, and a change whose target moved is still replayed onto where it went
— and what it buys is the difference between one wait and a replayed, re-checked,
freshly reviewed run, or a stopped one where the replay will not apply. An item is
held for exactly as long as the run it would have raced lasts, because the
conflicts are re-read at every pull from what is actually in flight — and in
flight means a run whose status is pending or running, whatever phase it is in:
a run integrating is in flight and holds its epic until the promotion settles.
That is one predicate, `runstate.Status.InFlight`, and it is the same one the
store's listing of incomplete runs and the running line of `yoyo status` are
built on, so a run the guard holds an item behind is a run that line lists — or,
for a run paused on work its item waits on, the paused line beneath it: such a
run still holds its epic and its files, because its change is still in its
worktree, and it holds no developer slot. A run
whose status is succeeded, failed, cancelled, or timed out is a record of its
item and holds neither a developer slot nor the epic, whatever branch or pull
request it left behind and whatever a person has yet to decide about it. And the slot a
hold frees is not idled: the pass carries on down the order to the next item that
races nothing, and both runs record what the sequencing did — the one that waited
says what it waited for, and the one pulled past it says which items it was pulled
ahead of.

The line the pass prints for a held item names the run it was held behind — the
run's identifier, so it can be checked against `yoyo status`, and the item that run
is over — and says what the last pull that held the item found rather than the
first. A watching session renders its report when it ends, which can be days after
a hold was first recorded, and an item held behind three runs in turn over that
time is one line naming the third. Until 2026-09-18 it named the first, in the
present tense, and a report that said a run two days dead was "already in flight"
was read as the guard holding a slot on it.

Not everything in the backlog is a developer run. Promoting a document the
architect owns, settling a decomposition, recording a decision: those happen in a
conversation with a role, and the harness's own gates already say so — the
artifact homes are default-deny for a developer's diff, so a run pointed at one
of them produces a correctly refused empty change. What it also produces is a
spent run, two review rounds, and two rounds counted against that item's cap, so
an item mis-selected twice reaches its cap having done nothing and escalates work
nobody ever started.

So an item says what carries it, and whose conversation that is. The Lead Product
Manager sets `executor` on the item as it is admitted — `conversation:` followed
by the role, as in `conversation:architect` — and `update` takes it too, for work
already in the queue. The bare word `conversation` is refused: from the handoff
until whoever holds the item starts on it, the role named here is the only thing
that says who has it, and a marker that named none left exactly that stretch
unattributed. An item carrying it keeps its place in the order, is reported in
the queue with what carries it, and is never selected for a developer run. It is
not a wait, and the pass says so rather than counting it among the items that are
about to become pullable: nothing clears, and what moves it is somebody opening
the conversation the item names. Work that says nothing is a developer run, which
is nearly all of it.

Naming the item yourself is unaffected. `yoyo run <id>` is you deciding, and the
marker steers what the harness chooses rather than what you may ask for.

The marker is not retroactive, which is the part worth knowing before you rely
on it: it covers exactly the items that carry it, so work admitted before you
started marking carries none and is chosen as ordinary developer work. Bringing
an existing queue under the guard means marking its conversation-executed items,
one `update` each, in the Lead Product Manager's conversation.

Two things now read the shape of conversation work, so an item left unmarked by
mistake is caught before a run is spent on it rather than by the run. On
2026-09-07 yoyodyne-ifd.330 — "The architect designs side conversations with
merge-back", done when "the design is recorded in the governed documents" — was
admitted with no executor; nothing refused it, the tracker called it ready, and
a developer run was handed it after its design had already merged.
[The diagnosis](diagnoses/yoyodyne-ifd-367-conversation-item-dispatched.md)
traces it. So admission refuses a creation or update whose done-means says a
design or a ruling is recorded, published, promoted, or ratified, or whose title
has the architect as its subject, when the item names no executor — with the
marker named as the fix — and the run asks the same question of the item it is
handed, before it claims it, for items that reached the queue some other way.
The reading is narrow on purpose: "decision" is what triage records on an item,
"the design" is cited by nearly every developer item, and neither fires. A
grant under an artifact home turns the reading off, because a grant is
somebody's decision that a run writes there.

A marked item also closes when its work lands, rather than by hand. A
design-only item's landing is a revision in a document the marked role owns,
and until now the only thing that carried that back to the tracker was the
Lead Product Manager closing the item on evidence some turns later — twice, once
after a developer run had been spent. Now every pull reads the documents the
marked role owns, and an item whose identifier opens the reason of a revision
in one of them, made by that role — `yoyodyne-ifd.330 - side conversations
designed` in a design's revision log — is closed at that pull, with the
document, the revision, and the revision's own words in the close reason. The
convention is that the reason *opens* with the identifier: a revision that
mentions an item further in — "published under yoyodyne-ifd.280 after three
reviewer reports" — is about something else and closes nothing, so an item with
two deliverables is not closed on a revision that carries one. A close you
disagree with is one you can read and reopen with a note, and the reopen
holds: the revision the item was closed on is written onto the item, as
`yoyodyne_landed` in its metadata, before the close, and a pull that finds an
open item still carrying that revision leaves it where you put it rather than
closing it again a minute later. What closes it again is a later revision
opening with its identifier, which is a new landing. The development manager's
conversation owns no document, so an item it carries is never read for a
landing; and an item nobody marked is a developer run, whose landing is its
merge.

**Parked work is out of reach until somebody puts it back.** Some admitted work
is work you still want and do not want started: deferred by a scope decision,
waiting on something outside the harness, held back until a design settles. That
is not a priority, and expressing it as one is what this exists to stop. A
priority says what comes before what among the work that is to be done, so the
bottom of the order is the last thing pulled and not the thing that is never
pulled — and `--watch` drains queues as a matter of routine. On 2026-08-27 one
did: it reached work a scope decision had put off the critical path, started it,
and the run failed having cost $34.38. Nothing about the selection was wrong. The
deferral lived in a convention nothing that selects work could read.

So the Lead Product Manager parks it, with `park`, and the reason is the action's own
reason. A parked item keeps its place in the order, is listed as parked wherever
the queue is shown, says why it is parked when you read it, and is never selected
however far the queue drains. It is not a wait: nothing clears, and what moves it
is `unpark`. Work can also be admitted already parked, with `parked` on the
creation, because the identifier a creation assigns does not come back until the
next turn and the item is pullable in between. Neither works on closed work,
which has left the backlog anyway.

Naming a parked item yourself is unaffected, exactly as with the executor:
`yoyo run <id>` is you deciding, and parking steers what the harness chooses
rather than what you may ask for. Parking is not retroactive either: it covers
exactly the items that carry it, so a queue parked by convention stays selectable
until each is parked in fact, one `park` each.

**An item the tree cannot serve is not dispatched, and the read that says so is
free.** Four items in a fortnight were handed to a developer that could not do
them, and each cost a full run to establish why: one asked for machinery that was
whole and approved on a branch nothing had merged, one for a conversion whose
subject is on a branch too, one was written blocked on a design answer that came
back negating every condition it stated, and one cited a symbol a commit weeks
earlier had deleted. Every one of them was reported by the tracker as ready to
pull, and correctly: the tracker knows about dependency links, and none of those
four was expressed as one.

So before a slot is spent, the pass reads what the item asks of the repository.
Two readings, both cheap, and neither of them a judgement about the work:

- **What the item pinpoints.** A `file:line` or a package-qualified symbol in the
  item's title, description, design guidance, or acceptance criteria is read
  against the tree. A citation naming a file the tree does not have, a line past
  the end of one it does, or a symbol nothing declares is the cheapest possible
  already-satisfied signal. A bare path is deliberately not read as a citation:
  an item that names `docs/configuration/agents.md` is as likely to be asking for
  the file as citing it. Neither are the notes, which are where the harness
  appends each run's record and where an implementation plan names the files the
  work will create.
- **What the item says about itself.** A sentence in which the item states that
  something has to happen first — *blocked until*, *gated on*, *not decomposable
  further until*, *shipping after … and inheriting its machinery* — is sequencing
  that lives where nothing which pulls can read it. The phrasings are a closed
  list, calibrated against this backlog rather than chosen for how they read: over
  the 435 items the tracker held when it was written, the whole check fires on 15
  of them and on 6 of the unfinished ones, and each of those six states a real
  gate.

An item that fails either reading is passed over with the unmet prerequisite
named, and it is put on the development manager's docket as an *item the tree is
not ready for* — the one docket entry with no run behind it, carrying what the
item asks for, the read that says the tree does not have it, and who releases it.
The two halves clear differently and the refusal says which: a pinpoint clears
itself, so the item is pulled at the first pull after the code lands, while a
sentence never clears on its own — the Lead Product Manager amends the item, or the
development manager records the dependency the sentence names.

**The reading is remade from the item at every pull, and nothing remembers it.**
Each pull reads the title, description, design guidance, and acceptance criteria
as the tracker holds them at that moment — never the notes — so a sentence that
has been removed stops refusing at the next pull, and the item is taken there
like any other. The docket entry follows the same reading: every pull reads again
each item the docket holds as not ready, and takes the entry off, saying why, as
soon as the item asks for nothing the tree lacks, has left the backlog, or asks
for something other than what the entry quotes — in which case the same pull
dockets it again in the words it now carries. Until that existed nothing took an
entry off: yoyodyne-ifd.298's went on quoting "this item does not start before
282's design lands" after the Lead Product Manager had removed the words and after the
item had been dispatched and closed.

A sentence is quoted with the field it is in — `its description says of it`,
`its design guidance says of it` — because a copy left in a field nobody looked
in outlives an amendment to the one they did. The design guidance and the
acceptance criteria are said to be fields the Lead Product Manager's update does not
rewrite, since it takes the title and the description only and her read of an
item is bounded: a sentence there is one she can be shown the refusal for and
not be able to remove herself.

**The refusal also reaches the Lead Product Manager.** When the entry is docketed, the
same finding is filed as a report — from the harness itself, naming the item and
what it states — into the pile her conversation is given, so amending the item
is put in front of the role that can do it rather than relayed to her by whoever
read the development manager's docket. The docket entry stays, because recording
the dependency the sentence names is still the development manager's. One entry
is one report: a pull that finds the entry already standing says nothing more,
and an entry taken off and then docketed again — the item restated, or the
sentence put back — is reported again.

Naming the item yourself is unaffected here too. `yoyo run <id>` is you deciding,
exactly as it is with parking and the executor.

Two failures are possible and both are reported rather than hidden. A repository
that cannot be read says nothing about any item, so the item is dispatched
exactly as it would have been and the failed reading is printed beside the pass —
turning an unreadable checkout into a stopped line would be a worse failure than
the one this guards against. And where the finding cannot be written to the
docket, the refusal still stands and says so: dispatching an item the tree cannot
serve in order to avoid losing a line of the record would spend a run to save a
sentence.

**A step only a person can take is a gate, and closing an item never passes
one.** Some work must not start until somebody has actually done something —
read a soak, signed a release off, checked a migration against production. The
tracker cannot say that. It knows one kind of completion, an item being closed,
and machinery closes items: on 2026-09-04 the parity soak's done condition
reserved the operator's own reading of that soak, the only encoding available
was an item somebody closes, a run closed it, and the flip that depended on it
became pullable with the operator's step untaken.

So the step is declared on the work it holds, by naming it after `human-gate:`
on a line of its own — in the item's title, description, design guidance, or
acceptance criteria, the fields somebody authored and not the notes the harness
appends each run's record to:

```
human-gate: soak-reviewed — the operator has read a week of soak runs and is content to flip
```

Nothing is inferred: a gate invented out of prose would stop work nobody meant
to stop, so the marker is the whole of it. An item carrying an undischarged gate
keeps its place in the order, is listed as
waiting on a person wherever the queue is shown, is named on the status's
needs-a-human line with what the person has to do, and is never selected however
far the queue drains. It is not a wait: no run passes it, no check passes it, and
neither does closing anything — not the item that declares it, and not the item
it depends on.

The gate holds selection — every route by which the harness chooses the work —
and not every route into a run. The pull above refuses it, and so does a re-run
the development manager decides: `yoyo triage rerun` is the harness choosing the
work, which is why the intake hold applies to it, and the same classification
puts it behind the gate, refused before the stoppage's one re-run is claimed and
in the same words the queue holds the item with. Naming the item yourself is
unaffected, exactly as with parking and the executor: `yoyo run <id>` is you
deciding, and the step a gate reserves is yours to take or to waive. Waiving it
that way leaves no record. A gate is listed only while its item is admitted, so
it leaves `yoyo gate list` and the needs-a-human line the moment your run claims
the item, and a run that lands closes the item with no act recorded and nothing
saying the step was passed without one. Record the act first if you want the
record to say so.

A declaration nothing could read — a mistyped name, no separator between the name
and the sentence, nothing said about the act — holds the work in exactly the same
way, and the queue and the status both say what is wrong with it. That is
deliberate and it is the same failure in miniature: you wrote a line meaning to
reserve your own step, and a reader that quietly dropped what it could not parse
would turn that into an item with no gate, pulled past a step nobody was ever
asked to take. Nothing records an act against one of these, because there is no
name to record against; what clears it is correcting the line on the item.
`yoyo gate list` names the item and the problem.

What passes it is
`yoyo gate record <name> --for <item> --by <you> --did "<what you did>"`, and
that is the only thing that does. The record says who took the step and what
they say they did, because a gate passed by nobody in particular and described by
nothing is the flag this replaced; a gate already passed is refused rather than
overwritten, so the record keeps saying whose act it was. `yoyo gate list` shows
what is still waiting, on which item, and what has been recorded.

**A name is not spent by being recorded once.** The act is recorded against the
item that declared the gate, and passes it there and nowhere else. That is what
makes the useful names usable: `release-signed` is a step taken once per release
and `soak-reviewed` once per soak, not once ever. Declare either on the next
piece of work and it holds that work until somebody signs that one off, whatever
was recorded before — which is why `--for` is required and not a nicety. If the
name alone decided it, the first act would pass every later declaration of the
word, the next release would be pullable on the strength of the last one's
signature, and you could not even record the new act, because the gate would
already read as passed. That is this mechanism's own failure arriving through
the namespace rather than through the tracker.

A workflow definition declares one the same way, as `gate:` on a state, and the
executor performs nothing at that state until the act is on the record — the
instance stands exactly where it was, and steps on when it is next stepped after
somebody records it. A gate whose record cannot be read is never treated as open.
There the act is recorded against the instance rather than a work item, and for
a sharper version of the same reason: every instance of one definition reaches
that same state, so an act against the name alone would approve one run's step
and every run the harness made afterwards.
That half is less visible than this one, and it holds less than this one does
today. An instance held at a gated state says so in the refusal raised when
something tries to step it, and no status surface lists it. And a gate on a state
holds what the executor performs — nothing more. Under the delivery trial the
definition observes the run rather than performing it, so a `gate:` written on a
delivery state in a project's own copy does not hold the run: the pipeline
delivers as it always has, the observing instance stops being stepped at the
gated state, and the run records a `workflow_divergence` saying so. That is this
mechanism's own failure by another encoding, and it stays so until the executor
is what delivers. Nothing shipped declares a gate on a state today, so there is
no such instance to miss; a definition that declares the first one wants that
surface, and the executor performing, with it.

A thirteenth thing deliberately keeps nothing out: an item whose goal was amended
after it was admitted is pulled exactly as it would have been, because
[staleness reports rather than decides](artifacts.md#what-a-change-upstream-leaves-stale),
and what changed goes into the run's recorded reason instead.

Two things make this accountable rather than work happening behind your back.
Holding intake stops it choosing anything more while what is running finishes,
and is read at every pull rather than once at the start, so a hold you place
mid-pass takes effect at the next selection. And every run it starts records in
durable state why that item was chosen — its place in the order, how much of the
queue was pullable, how much of the machine was free, anything upstream that had
moved, and whether conflict-avoidance shaped the choice. `yoyo status` reads it
back.

The configuration is re-read before every pull for the same reason: a capacity
you raise or a priority you reorder mid-pass is picked up the next time it
chooses, rather than at the next restart. Runs already in flight keep the
configuration they started under.
[Configuration](configuration.md#scheduling-ready-work) has the rest.

**A pass also delivers stopped work into the development manager's
conversation** — a run that failed independent review after every permitted
attempt, rather than that run waiting on the docket for somebody to tell the
development manager. Only the courier changes, and `yoyo work --help` has what
bounds it. It is delivered per stopped run rather than per docket entry: a run
docketed both as that stoppage and as an escalation a role raised from it is put
to her once, as the one entry her docket folds the two into.

**A pass also carries out what she decided about it.** A repair or a re-run she
recorded is fired by the pass itself, oldest stoppage first, as many per pull as
there are developer slots free for them — and as a session's `--limit` leaves,
since each is a run started — against a developer slot exactly as a pulled item
is. It goes ahead of the queue's own work at its item's priority and below, and
behind ready work of a higher priority: a decision about a priority-3 item waits
while a priority-0 item stands ready, takes the slot the walk of the order
reaches its priority with, or takes a slot the queue left empty, and a pull that
gave it none writes onto the item which ready items outranked it. Recording the
decision is what causes it, and `yoyo triage repair` and
`yoyo triage rerun` are what fires one now rather than at the next pull. A merge
re-arm she recorded is fired by the same pass, whether the merge it makes is one
the forge dropped or one nothing ever asked the forge for — on every pull and
outside the developer slots, because it is one merge request rather than a run —
through the action `yoyo triage rearm` takes, which is what fires one now. Its
gates are that action's own: the forge's merge state, the request's head and
checks where nothing had asked for the merge before, the pre-merge check on the
remote target, and a decision standing that nothing has carried out; your pause
and your intake hold stop it as they stop the other two, and a run of the item
in flight holds it back. A re-arm about a publication whose run never promoted
its change is attempted too. It is refused on the item, naming the re-run as the
decision that applies, rather than passed over, and a refused re-arm's entry
comes back onto her docket as a refused repair's or re-run's does. Every decision the item's record holds is read, whatever its place on the docket: the
one about a stoppage docketed as a stopped run, and the item's latest decision
where it names a run no such entry stands for. A re-run recorded again about a
stoppage whose one re-run was already claimed is attempted as well, and refused
on the record — triage re-runs one docketed stoppage once — naming the item's
latest stoppage as where the decision belongs. It runs under every gate those verbs
already ask: your pause, your intake hold, the item's own triage budgets,
developer capacity, and the preserved worktree being what a continued developer
could be handed back. A refusal spends nothing and is never silent: it is
written onto the item's triage record and the docket entry she reads comes back
carrying the decision and the gate, naming what would clear it. From then on the
decision is hers rather than the harness's: the entry's next mover names her, and
so does the item's held line, which says what was refused and what clears it,
because a line naming the harness over a decision the harness cannot carry out
is one nobody acts on. A decision waiting on your pause or your intake hold, or
one no pass has attempted yet, stays the harness's. A repair counts as carried
out once its run records being handed back since the decision, however few of
its granted rounds were judged, so a repaired run that stops again is hers to
decide about too. A gate shut for
one item — a directive, work it waits on, a worktree somebody has been in — is
retried at a paced interval rather than every poll, so one decision that cannot
fire does not starve the ones behind it. **A refusal whose cause will not clear
on its own is recorded once and is not retried until the development manager
changes the decision.** A retired or missing worktree, a deleted branch, a HEAD
that moved from the harness's recorded commit, a superseded or missing decision,
a missing stoppage record, and a publication no re-arm can make are these
permanent causes. The item and her docket carry the refusal's own words and the
ways forward: a re-run or an escalation, or recording a missing decision again
with an override where the budget requires it. The exact item note is saved
with the refusal; if the tracker refuses its delivery, later pulls retry the
note alone and check whether it is already there before appending it. Changing
the decision or clearing the refusal does not discard an undelivered note.
The refusal is delivered to her
as a new stoppage even when the original stoppage was already delivered; later
pulls neither attempt it nor deliver the same gate again. A new decision,
including a newly recorded decision of the same kind, is attempted at the next
eligible pull. A failed reading of a branch or worktree proves neither gone and
keeps its paced retry. [The run-stop inventory](run-stops.md#permanent-carry-out-refusals)
names the causes. A gate shut for everything at once —
your pause, your intake hold, a full harness — is attempted once while it
stands and again on the first pull after it opens. Before this, thirty-three
decided items stood unfired for days because the only executor was a person
typing one of the two verbs.

**A decision is never silently unattempted.** Fired or refused are the two
endings an attempt has, and both are written down; the third ending is a
decision no pass hands to an action at all — held back because another run of
the item is in flight, because the slots the pull had went to decisions ahead
of it, or because the item's own record disagrees about what is left of its
re-runs — and nothing refuses a decision nobody attempts. So a decision still
standing one poll interval after it was recorded, with nothing attempted since,
is written onto the item's triage record as *unattempted*, saying what kept it
back and what clears it, and her docket entry carries it and puts it ahead of
the walk. It paces nothing: the first pass that reaches the decision attempts
it, and what that attempt comes to replaces the record. `yoyo status` counts
these beside the refused ones on its held-work line — *decisions not carried
out: 1 refused, 1 unattempted* — because the two are fixed in different places.
The re-runs recorded for yoyodyne-ifd.192 and .187 on 2026-09-19 sat in that
third ending for a week with nothing anywhere saying so;
[the diagnosis](diagnoses/yoyodyne-ifd-428-39-unattempted-carry-outs.md) is how.

This holds whatever became of the docket entry for the decision's run. A
decision about a run whose entry an earlier decision closed — a re-run recorded
after a repair settled the same stoppage — is attempted at the next pull like
any other, because the entry is still there to act on. A re-run of a run the
docket holds **no** stoppage of — a re-run the harness cancelled on its way out,
say, which is never docketed — is carried out too. The item is started again
from the target branch, claimed under the key the docket would have given the
run, so that run is re-run once like any stoppage. Every gate a re-run asks
still applies, and the run only has to have ended. What such a run cannot take
is a repair, because a repair re-enters a docketed stoppage's worktree. A repair
of it, or a re-run of a run the harness holds no record of, is attempted and
refused. The refusal says how the run ended and names the decision the harness
would carry out instead: the re-run of that same run for a repair, or else the
same decision against a docketed stoppage of the item that can still take it,
or a re-run of the item's latest recorded run. That refusal, and an
unattempted record about such a run, is shown on every docket entry of the item
for as long as it is her latest decision, labelled with the run it is about.
Otherwise no entry would carry it, because each entry shows only the findings
about its own run. The re-run recorded for yoyodyne-ifd.187 against run-04e578ce
on 2026-09-26 was refused thirty-nine times this way while every entry she read
was silent;
[its diagnosis](diagnoses/yoyodyne-ifd-428-52-decision-on-an-undocketed-run.md)
is how.

[Deciding what becomes of stopped
work](conversation.md#deciding-what-becomes-of-stopped-work) is the decision
side of it.

**A run in flight can be stopped by the development manager as well as by
you.** A run whose work she decides is superseded by another item, narrowed so
it is building more than the item now asks for, or launched on something that
should never have been dispatched is one she records a `stop` decision about, in
her sweep or her triage conversation, with her reason and the superseding item
where there is one. Recording it is what stops the run, and nothing waits for a
pass: the harness writes beside the run the same request your
[`/stop`](conversation.md#steering-the-work-from-the-conversation) writes, naming her rather than you,
and the run honors it at its next provider-call boundary exactly as it honors
yours — an invocation already streaming finishes first — ending cancelled with
its branch and worktree preserved, and its developer slot free as its record
goes terminal. A run with no process behind it — parked on a dependency, say,
with the process that parked it long gone — reaches no boundary, so the next
[`yoyo reconcile`](operations.md#recovering-interrupted-runs) honours the stop
in its place, at once, and ends the run exactly as it would have ended itself.
Until the sweep honoured a dead run's stop (yoyodyne-ifd.428.49), nothing did: on 2026-09-27 her stop of run-3b94404c
stood unread for most of a day over a run that had been dead since the evening
before. The run's record, its ending on the item's notes, and the triage
note her decision leaves all say who stopped it and why. What differs from your
stop is the docket: yours hands nobody a decision and is docketed nowhere, while
hers is docketed as the stopped run it is and closed by her decision in the same
write, so it reads as a stoppage already decided rather than one waiting on her.
The stop spends no budget and starts nothing, so none of the carry-out gates
above apply to it; what it is refused for is a run that has already ended, whose
stoppage is decided about rather than stopped. Only her conversation can make it
— the `stop` decision is a triage action, which no other role holds, and the
hand that writes the request is wired into her conversation alone. What becomes
of the item and the preserved change afterwards is a separate decision, and
retiring the item is the Lead Product Manager's. Until then the item is not
pulled again: while the stop is the latest decision on the item's triage record
and the stopped run's branch or worktree is still there, the pull holds it,
saying which run she stopped, which decision, and which item supersedes it.
Closing or retiring the item, the change going, or a later decision of hers
releases it.
Where it is the Lead Product Manager who decided the run's item is superseded,
narrowed, or to be retired, that decision reaches the development manager's
docket by itself, and `proceed` is the answer beside `stop`;
[a decision about work in flight](#a-decision-about-work-in-flight) is how.

A stop decides the stoppage it causes and no other. A run that passed its last
boundary before the request was read, and then stopped for another reason — a
failed review, say — reached a stoppage the stop never reached, and it is
docketed and held like any undecided one: her decision, not the harness's
carry-out, is what it waits on, and the entry names the stop she asked for
beside what actually stopped the run.

**A pass also fires whichever [recurring task](configuration.md#recurring-tasks)
is due**, where a project has configured any — a role woken on a cadence to look
at its own domain, rather than because something happened. At most one per pass,
and every firing ends in a durable report that
[`yoyo sweeps`](operations.md#reading-what-the-recurring-tasks-found) reads. A
firing of a development manager's task carries the [triage
docket](conversation.md#roles-asking-each-other-things) as it stands, read for
that firing, in the message that wakes her. A
project that schedules nothing has none of this and its passes are unchanged.

**A pass also wakes a role whose block of tracker actions the harness refused.**
A block it cannot read is refused whole, so nothing in it happens. Most refusals
never reach a pass: the refusal is
[handed back inside the message that earned it](conversation.md#the-conversation),
and the role reissues the block before its reply ends. What reaches a pass is a
refusal on a message's last round, or one whose hand-back round never came back.
That refusal opens the role's next turn in the harness's own words, but nothing
starts that turn, so the correction would wait for somebody to open the
conversation. The pass starts it: one turn per refusal, at most one per pass,
and the role re-issues the actions itself. A turn that does not put the actions
back goes to you rather than earning another wakeup — a second block refused with
the first still unanswered, whether the harness woke that turn or somebody else
did, or the woken turn answering without asking for any tracker action at all.
Both leave the actions exactly as lost, and a second copy of the same message is
not going to change that. A turn the provider never took — refused for want of
capacity, or answering nobody because it is down or the account's login has
lapsed — put nothing in front of the role, so the turn is given back and made
again a quarter of an hour later, three times in all before the harness stops;
the attempt is kept either way, which is what makes that bound reachable, and
the pass says which attempt each one was. When the last attempt is spent that
way the refusal goes to you, said the same way as the other unanswered endings,
rather than going quiet. A conversation nothing else can open — no agent fills
the role, say — keeps its turn spent, because what that waits on is somebody
changing something rather than a provider coming back. Nothing about it is
configured, and a pass with no refusal to wake for asks no provider anything.

**A pass also audits the claims the tracker holds against the runs the harness
actually has**, and gives back the ones with nothing alive behind them. A run
that is killed leaves its item claimed and its record saying it is in flight, and
nothing else ever undoes either: the item has left the ready queue, so no pull
chooses it, and the record goes on filling a developer slot, so a machine stuck
behind two of them reads as a drained queue rather than as a stall. A claim with
no run alive behind it for half an hour is given back with the reason on its
notes, the run's record is ended as cancelled under its own lease — which a live
process holds and the operating system drops when it dies, so a lease the audit
can take is a process that is gone — and the item is pulled again on the same
pass. It runs before the intake hold and the capacity check, because a held or
full session is exactly where a dead claim hides, and it asks no provider
anything. Each release is on the pass and
[reaches the operators once](operations.md#claims-with-nothing-working-on-them).

A stoppage the harness tried to deliver and gave up on is not restated by every
pass after that. It is not lost either: the item stays held, undecided on the
development manager's docket, with the reason on it, wherever `yoyo status` reports what the harness is holding. The
restating was worth having until there were twelve of them, at which point what a
session start said was a paragraph per stoppage nobody was going to act on and
nothing about what the pass had just done.

**`--watch` keeps it open.** Instead of returning when the queue empties, it
waits `execution.work_poll` — a minute by default — and reads the queue again,
until you stop it. Nothing else about the pass changes, and nothing needed to:
the re-reading above is per pull. An idle session costs one local tracker read
per interval, and one more for each run paused on work its item waits on, and
asks no provider anything, unless it has a stopped run to put to
the development manager, a recurring task that has come due, or a refused tracker
block to wake a role for, or a triage decision of hers to carry out. Holding intake
brakes a watching session in place rather
than stopping it — it keeps polling, chooses nothing, and resumes when you
release it. It does not stop the first three, which are read before it and choose no
work: a held intake still delivers a stoppage, still fires a due task, and still
wakes a role to put its own refused block right, so
`yoyo pause` is the switch for stopping what a quiet session spends. It does stop
the fourth: carrying out a decision is the harness choosing work, so a held intake
leaves the decision standing and the docket entry says the hold is what it is
waiting on, and the first pull after you release it carries the decision out. It stops
continuing a run paused on work its item waited on for the same reason: picking
that run back up is the harness choosing what to spend a slot on, so it stays
paused under the hold and the first pull after you release it continues it.
Nor does it stop the claim audit, which is read before it for the same reason
the first three are: the brake that holds intake is placed exactly when runs are
failing one after another, which is when a claim is most likely to have just
died.

**Only one session watches a product at a time.** A second `yoyo work --watch`
is refused as it starts, in a sentence naming the session holding the watch and
the process running it. Two sessions are not two workers: each reads the whole
ready queue and chooses from it, and an item one of them chose is invisible to
the other until its run reserves — several steps later — so both can start on
it. Two of them briefly coexisted while the 2026-09-05 wedge was being cleared,
which is what this stops. The watch is an advisory lock the operating system
drops when its holder exits, so a session that was killed leaves nothing for
anybody to clear, and a session that stops to take up a deploy lets it go before
it restarts so the build it becomes can take it up. A drain takes nothing and is
refused nothing: what this refuses is a second session that stays open.

The same lease is how the product's supervisor keeps a session watching. With
the scheduler enabled in the configuration's
[`services`](configuration.md#services) section,
[`yoyo start`](operations.md#starting-the-product-and-stopping-it) starts
`yoyo work --watch` as one part of the product and starts it again if it dies,
within the supervisor's bounds; a session you started by hand before that is
found holding the watch and taken as it is, and `yoyo stop` stops the session
with the rest, which cancels the runs it is hosting exactly as stopping it
yourself does.

Four things guard a loop that no longer ends. A session does not start the same
item twice unless the item has changed — what it says, what it is for, its
priority, its status, what it depends on, its notes — so a start the harness
cannot get past is not retried every minute forever, and a blocker you release is
picked up because releasing it changed the item. Runs blocking one after another
with nothing landing between them hold intake at
`execution.blocked_runs_before_intake_hold`, so a broken machine cannot put the
whole backlog through a failed run overnight — and the same poll summons the
development manager to decide what happens to the hold, with the blocked runs
in front of her, and probes the line by itself with one run if she has not
decided by `execution.brake_cooldown`, so a hold the brake placed
[waits on nobody unless it is escalated](operations.md#pausing-everything-and-resuming-it)
— by her, or by the harness itself once that summons-and-probe loop has gone
round `execution.brake_escalation_cycles` times.
Stops the environment made count toward nothing. What reports that hold
names the brake rather than you, because the hold records which of the two
placed it, names the runs it counted with what stopped each, and says who is
deciding it. And it records what it is doing — watching, idle, braked, blocked, resumed,
stopped — where `yoyo status` and the Slack sink read it, because an idle
session and a dead one are otherwise the same silence. A
poll that starts nothing names the runs going and what it passed over. It records
that account in classes as well as in words — how many items were held for a
person, parked, carried in a conversation, sequenced behind a run — so that the
stall alarm below states the same cause rather than deriving a second one.

A start the environment refuses before reserving a run is eligible again after
one poll interval, without any change to the item. Uncommitted changes in the
primary checkout, a lost race for capacity, and failures to read repository
readiness, durable state, or architectural invariants leave no memory that the
work was tried and count nothing toward `blocked_runs_before_intake_hold`.
The refusing step records that the cause is outside the work. A sandbox that
will not spawn a shell is covered when that step marks its refusal that way.
A readiness read also covers an unmarked failed start while the checkout is
still refusing work.

The primary checkout's readiness is read at every pull before new work is chosen.
A watching session waits and reads again; a drain stops on the refusal. The watch
log, `yoyo status`, and the Slack heartbeat carry the cause, including
`runs cannot start: uncommitted changes in the primary checkout (<file>); commit or stash to release`.
Once you commit or stash, the next poll can start the queued work without a
session restart or an edit to the item. Other refusals carry the condition and
the beginning of the cause separately, so bounding a long cause does not spend
the detail limit repeating the condition.

The first of those four guards says why, against each item it holds out. An
item passed over as *already tried this session* is the one exclusion whose
cause is not somewhere you can go and look — every other class names a state of
the item, the queue, or the machine, and this one names an attempt only the
session remembers — so the poll records what became of that attempt beside the
item's name: the run it started and how it ended, the work having gone to
another process, or the dispatch having failed before any run was recorded, and
where the record of that failure now is. That last case is the one that could
leave nothing at all. A dispatch that dies before the reservation writes no run
record, so nothing built on the run store — the sweep, the docket, `yoyo status`,
the stall alarm — can ever see it; on 2026-09-13 a session tried two items four
hours into a returned capacity window, both died that way, and it then excluded
both for the rest of its life with no surface saying why, over a queue of
seventy-four. So a dispatch that fails on the work before a run is reserved is put on the
development manager's docket by the session that tried it, as an *attempt that
never became a run*, carrying the item, why it was selected, what stopped it,
and that the session will not try it again until the item changes. It is keyed
to the item and the failure, so a session meeting the same dead dispatch twice
dockets it once and a dispatch failing a new way is news. A docket that refuses
the write does not lose the account: the exclusion says the session's own log is
all there is, and the pass reports the dispatch nothing recorded.

**A watching session also notices that the harness has stopped doing anything.**
Everything above is what the session says about itself, which works exactly as
long as it is choosing at all. So on every pull — at most once per
`--stall-after`, ten minutes by default — it reads the durable records instead:
nothing started for that long, work the tracker calls ready, and no hold, full
machine, still-moving run or provider usage window to account for it is
[recorded against the product as a stall](operations.md#when-nothing-happened-at-all),
which `yoyo status` reads back and the Slack sink, where one is running, takes to
the operators once. What this loop catches is the session that is alive and has
stopped starting anything — a queue whose ready items are all claimed by runs
that died, say. It can catch that because the silence is dated from the runs
rather than from the session's own account of itself: a poll that started nothing
is not a start, so a session polling all night does not move the moment this is
measured from, and one that has never started anything is dated from the first
thing its log holds. A session that died writes no polls at all, and
[`yoyo reconcile`](operations.md#recovering-interrupted-runs) takes the same
reading for that case, under the same flag name. Nothing on the path asks a
provider anything, and noticing is all it does — restarting whatever died is the
session's own exit and the supervisor that starts it. It costs one tracker read
per `--stall-after`, and none while something else already accounts for the quiet.

**A reading of the harness that fails does not end the session.** The tracker is
a database a reconcile and every settling run write to, so a reading that fails
is contention far more often than it is a store that is broken. The one that
ended a session on 2026-09-01 succeeded again in 0.4s a few minutes later; what
it cost was the session, which stopped on that single reading while the queue sat
idle until an external job noticed the process was gone. So a watching session
waits and reads again — two seconds, then four, doubling to thirty — and stops
only once the readings have gone on failing for five minutes, saying how long it
tried and what the last failure was. What it rode through is on the pass it
returns and in the watch log while it happens, because a reading that succeeds on
the second attempt leaves nothing behind. A drain does none of this: it is a
command you are waiting on the return of, and one that slept
through an outage would be one that hung. A pull that is assembled and unusable —
a capacity of zero, a `--budget` with nothing to price it — is a decision about
the configuration rather than a reading that failed, and stops either kind of
pass at once.

**A watching session takes up a build deployed over it.** A session runs the
binary it was started from, so every fix that lands behind it is a fix the work
it dispatches is spent without — which reads as agents failing rather than as a
process nobody restarted. It had already cost three review rounds against a bug
dead before they started, and then a session was found forty-three changes old.
So when the `yoyo` it is running is written over, the session drains: it
restarts into what you deployed the moment it hosts no run, and until then it
carries on exactly as it was — polling, pulling into free seats, firing its
recurring tasks — because the drain is about the runs it hosts and not about the
scheduler's other duties. The wait is bounded, by
[`execution.redeploy_drain_limit`](configuration.md#watching-instead-of-draining),
fifteen minutes by default: past it the session restarts anyway, stopping each
run it still hosts at a developer attempt, its checks, or a review where it is and
preserving it whole for the session that comes back to re-adopt, with every
counter as it was. A running check stage is stopped at the drain limit even
when load has extended the stage's own limit; the session that comes back runs
it again from the start. A stage that has already finished gets a brief grace
to record its verdict. A run at its promotion is waited out past the bound
because it holds the target branch's lease. That stop is recorded as
a restart rather than an ending, so `yoyo status` and the Slack sink say a
session is coming back on the new build instead of telling you to start one,
and the drain and its bound are on every line the session writes while it
lasts. [Operations](operations.md#a-session-draining-to-restart-into-a-deployed-build)
says what a drain does and does not stop.

The product's supervisor holds to the same rules rather than adding its own. It
never stops a watching session to move it onto a deployed build — it waits for
the session to restart itself, and reads the moment the session lets its lease
go as that restart rather than a death — and its
[maintenance pass](operations.md#the-supervisors-maintenance-pass) never stops the
scheduler for any reason, a program manager's restart request included, because
stopping it cancels the runs it hosts. What the supervisor builds after a
landing is what a draining session restarts into.

A restart has to be recorded before it is known to have happened, because one
that works never comes back to record anything. So on the rare occasion it does
not — the operating system refuses the re-execution, a bound turns out to have
nothing left of it, or the restart is given up on below — the session writes a
second stop saying it ended after all, and both surfaces correct themselves. What
you never get is a stopped line that both places tell you needs nothing from you.

Nothing outside the process could do that. Killing a session cancels the run it
is carrying, so an external job may only bounce it while nothing is running; with
two developer slots and a deep queue the next run starts the moment one settles,
and a poll at any interval never lands in that window. The session is the only
thing that can put a run down without cancelling it, which is why the session
is the only thing that can close this. A run inside the bound is never
interrupted for it; one the bound stops is put down at a phase it can be picked
up from, never killed; and the queue is re-read from scratch on the way back in
exactly as it is at every poll.

**The restart is given a minute, and a session you ask to stop stops.** A
re-execution that is going to happen happens at once, so a session still waiting
on one a minute later is not restarting slowly — it is not restarting. It exits
instead, and whatever started it starts the next one from the build that was
deployed, which is where the restart was going. A stop signal that arrives while
it is waiting ends it the same way: you asked for the session to stop, so it
stops rather than finishing turning into something else first.

Every command gets the other half of that. The first stop signal cancels what
the process is doing and hands the signal back to the operating system, so a
second one ends a process that turned out not to be listening; and a process that
has not stopped two minutes after being asked to exits on its own. On 2026-09-05
one did none of this: it logged the restart, went quiet for an hour holding the
queue, ignored SIGTERM, and had to be killed. Stopping a session should take the
signal and nothing else.

**The bounds you set cross the restart reduced to what is left of them.** A
session given `--budget 50` that has spent $45.01 comes back with $4.99, and one
given `--limit 10` that has started six comes back with four — because a bound
carried whole would start again at every deploy, and a machine that deploys
several times a day would have no bound at all. A session that has reached
either bound stops on it instead of restarting: you set that number, and taking
up a build is not you raising it. So what a deploy costs the line is one restart
and no work, and it costs a bounded session nothing of its cap.

A drain never does this. It is a command you are waiting on the return of, and
restarting it would run the pass again from the top.

`--budget <usd>` caps what one session spends, and fails closed: a pass that
cannot price itself is refused before it starts, and a session that meets a run
whose evidence will not price stops and names it rather than counting it as free.
A session that stops that way is a stopped line like any other: with work still
ready, [the Slack sink](reporting.md#reporting-into-slack) says so again every
hour until somebody starts one.

The default is still the drain, and `--until-drained` says so out loud. What
changes when you watch is what bounds the spend: a drain is bounded by the queue
emptying, and a watching session is bounded by what you admit to it.

Documentation counts as part of a work item rather than as follow-up: the
developer contract makes updating the documents that describe changed behavior
part of the assigned work, and the reviewer reports a change that leaves a
document asserting something the change has made false. That reconciliation is
diff-scoped, and the limit is worth stating plainly — the reviewer is given one
change, not the repository, so it catches a contradiction with documentation it
can see and misses a claim invalidated in a file the change never touches. What
that misses across a whole branch is what [`yoyo review`](#reviewing-what-a-branch-adds-up-to)
is for; nothing in the harness compares the accumulated documentation against
the repository as a whole.

## Reviewing what a branch adds up to

A per-item review sees exactly one work item's worktree, so a defect that is
consistent inside every change that produced it and wrong only in their sum is
structurally invisible to it. `yoyo review` is the same reviewer — the same
contract, the same structured verdict, the same independence — pointed at a
branch against the base it grew from:

```sh
./bin/yoyo review --base main                 # the branch you are on
./bin/yoyo review --base main --branch milestone --json
```

It describes every commit the branch carries over that base and diffs the whole
range as one patch. The product intent, including its standing set, is supplied
as that base commit holds it, and the reviewer checks the accumulated change
against the set. The patch has the same bounds a single change is described within: a
range too large to show in full is clipped whole file by whole file and in the
same class order — source, then tests, then test data — with each file the
bound kept out named above the patch with the size of its diff, its content
digest, and where it can be opened at the branch's tip, and it is reported as
truncated. What that truncation does to the verdict is the rule above: a range
whose test data alone outgrew the bound is approvable and its approval names
those fixtures, and a range that kept out a source or test file, that named a
fixture nobody could open, or whose own history the bound clipped is not,
because what was not shown was not reviewed. The base must
be an ancestor of the branch — a base that has moved on is a reconciliation
rather than an accumulated change, and the command says so instead of quietly
reviewing a range you did not name.

The verdict is recorded with the same session and model evidence a per-item
review leaves behind, in the `branch-reviews` directory beside the runs and the
conversations, and what the reviewer noticed beside its verdict is collected
with every other report. It is a provider invocation like any other the harness
makes, so it records the event stream every other one records: it can be
followed while it runs with
[`yoyo status --follow`](operations.md#following-a-run-a-conversation-or-a-branch-review), and what
the provider reported it cost is priced beside runs and conversations rather
than quietly missing from the harness's total.

What a `repair` verdict here does is deliberate and narrow: **nothing to the work
already integrated.** Every commit under review was checked, reviewed, and
promoted by a run that has since settled, so there is no gate left to hold and
the harness does not revert or reopen a promotion on a second opinion — the
branch review is wired with no run store and no integration, so it could not if
it were asked to. What it does instead is answer one question, and enforce the
answer: the branch is approved only if an independent reviewer approved it, and
`yoyo review` exits non-zero on anything else — a repair verdict, or a review
that never answered. The exit code follows the verdict and nothing else, so a
range the bound clipped is not refused for the clipping: a truncation that kept
out only listed fixtures is one the reviewer may approve, by the rule above, and
the command exits zero on that approval as it does on any other. Where the
omissions are ones no approval may be given over — a source or test file kept
out, a fixture nobody could open, a history the bound clipped — the harness
refuses the approval itself, so the review comes back carrying no verdict and
the command exits non-zero on that, exactly as it does on a review that never
answered. The command says on stderr what the bound kept out either way,
because what was not shown was not reviewed whatever the verdict decided about
it. The
findings are then work, and admitting work to the backlog is the Lead Product
Manager's.

### Measuring the reviewer against itself

A branch review is a replayable function of a branch state: the same commits over
the same base, described the same way, judged under the same contract. That is
what makes a *shadow* review possible — the same review, made to measure the
reviewer rather than to judge the branch:

```sh
./bin/yoyo review --shadow --model sonnet --base main --branch milestone
./bin/yoyo review --compare                   # what the collected ones amount to
```

A shadow verdict approves nothing, whatever it decided, and that is enforced in
the record rather than remembered by whoever reads it: the durable review is
marked, and `Approved` answers no for a shadow verdict exactly as it does for a
repair one. That is what makes the measurement free of risk — a cheaper reviewer
pointed at a branch cannot leave an approval of it behind. `--model` is refused
without `--shadow` for the same reason: a review whose reviewer was chosen at a
terminal rather than by the configuration is a measurement and only ever that.
Because it decides nothing about the branch, a shadow review exits on the
question it was actually asked — whether it produced a verdict — so a shadow
`repair` verdict is a successful measurement rather than a failure.

The baselines are the branch reviews already recorded. Every verdict `yoyo
review` has ever given is in the `branch-reviews` log with the base commit and
head commit it was given on, and `--compare` pairs on those two — so a branch
state the configured reviewer has already judged can be shadowed without paying
for its baseline again. What that costs is one shadow review per state and
nothing else.

Reaching one of those states is the only manual step. `--branch` takes a local
branch name, so a state that is still a branch head is shadow-reviewable as it
stands; an earlier state needs a local branch pointed at the head commit the
recorded verdict names (`git branch <name> <commit>`), and `--base` takes the
base commit from the same record. That new branch name is deliberately not part
of the pairing: the same commits reached under a second name are the same code,
so the two reviews still pair, and each side's own branch name is reported so the
one thing the reviewers were told differently is visible. A state nothing has
reviewed has no baseline at all, and there an ordinary `yoyo review` is what
makes one first.

`--compare` reads what was recorded and invokes nothing. For each shadow review
it reports, per severity, how many of the baseline reviewer's findings the shadow
also anchored to, how many it missed, and how many it raised alone, with what
each of the two reviews cost beside it. Findings are paired by the file each
anchors to, which is the only thing two reviewers reliably agree on — they will
differ on the line and always on the wording — so a finding that names no file
cannot be paired at all, and the count of those is reported rather than folded
silently into the miss rate. Every finding is listed under its comparison for the
same reason: whether a missed finding was a local, mechanical catch or one that
only exists in the accumulated shape of the branch is a judgement about its
content, and the numbers cannot make it. A finding only the shadow raised is a
candidate false positive rather than a proven one — what this measures against is
the other reviewer, not what is true of the branch.

A shadow review costs money like any other provider invocation, and is priced
where every other branch review is: it records the same event stream, so
[`yoyo status --spend`](operations.md#following-a-run-a-conversation-or-a-branch-review) counts it
under `branch reviews`, and `--compare` reports each side's own cost from that
same log. It is not in `yoyo cost`, which prices work items from the runs made
for them — a branch review belongs to no run, and a shadow review belongs to no
work item either. So measuring a reviewer is spend an operator can see, but not
under the item that prompted it, and it is indistinguishable in the status total
from a review that gated something.

The first use of this is recorded in
[the ifd.92 experiment note](experiments/yoyodyne-ifd-92-shadow-review.md): what the
instrument is, which recorded verdicts are the benchmark, and what has not been
measured yet.

## Publishing, and the merge that follows it

Runs are local until a project sets `approvals.publishing` to `automatic`. With
publishing on, the developer phase is what pushes: when a developer attempt
finishes, the harness commits it, pushes the run branch to `execution.remote`,
and opens a pull request against the target branch — and each repair attempt
updates that same request. The approving reviewer verdict is what merges it: the
harness asks the forge to merge the pull request, subject to exactly the checks,
independence evidence, and fast-forward rule that gate integration, plus a fresh
check that the remote target has not moved. The harness makes every push and
every merge request itself and routes neither through an agent: no role is given
a credential, a tool, or a request for either, and the reviewer — the role whose
verdict authorizes the merge — runs with no tools at all, so it cannot perform
one. A developer does have a shell in its worktree and runs under your account,
so "no agent pushes" describes what the harness does rather than a boundary it
enforces; the [design document](designs/v1-harness-design.md#what-is-enforced-and-what-is-not)
says which half is which. The local target branch stays authoritative: the
harness fast-forwards it as it always has, and the forge merges the pull request
carrying exactly that commit under a merge commit — the one method that puts the
reviewed commit itself on the base, where a squash or a rebase would substitute
a rewritten copy. So the merge leaves the remote target at your local branch
plus one forge merge commit, identical in content, and the harness checks that
relationship on both sides of the merge — with one deliberate asymmetry. Before
the merge, the remote must carry exactly what the promotion was written against,
because anything else is work the forge would reconcile that nobody here saw.
After it, the remote must *contain* the promoted commit, unrewritten; it need
not carry exactly its content, because a merge that lands among others — ten
held requests merged in one sitting, on 2026-09-13 — leaves every promotion but
the last under a merge commit later merges have built on, and demanding equality
there confirmed the last one and reported the other nine as unconfirmable for
good. What the harness records as the merge commit is the one the forge names for
the pull request, where that commit is on the remote target with the promoted
commit as a parent, or otherwise the one it finds in the remote history with the
promoted commit as a parent; the forge's record never decides the confirmation,
only what is recorded. The last step of the promotion is to
catch your local branch up onto the remote: a fast-forward onto a commit
that already contains the promotion, so nothing
is rewritten, nothing is merged, and nothing is decided. That is the `git pull`
you used to have to remember after every merge, and it is why your checkout
stays level with the forge on its own. A fast-forward blocked by uncommitted
work in your checkout is held rather than forced, and says which file held it —
except for the control-plane exports the harness declares (Beads' passive JSONL
dumps), whose churn is discarded, because they are derived from a store that is
authoritative elsewhere. A merge that landed after its run was over, and any
catch-up that was held, are swept by
[`yoyo reconcile`](operations.md#recovering-interrupted-runs).
Your target branch itself is never pushed, so a branch protected against
direct pushes is merged into normally; a forge that refuses reports which
requirement was unmet, and a merge that did not carry the promotion is reported
rather than reconciled. The merge is asked for as of when your branch protection
is satisfied rather than as of now, so required checks that are still running
are waited for by the forge rather than refused seconds after the reviewer
approved. Waiting that way needs "Allow auto-merge" enabled on the repository;
when it is off and nothing is holding the pull request back the harness just
merges, so only a repository that has something to wait for and no way to wait
for it is reported as unpublishable, naming the setting. Administrator override
is never used to get past a protection rule. A run whose merge is queued that
way reports the pull request as queued and finishes, leaving the work item open
because nothing has yet merged the change anywhere but locally;
[`yoyo reconcile`](operations.md#recovering-interrupted-runs) settles it once the
forge has merged — settling the item then, closed or put back as its own landing
says — or, if the forge dropped the queued
merge, records an outstanding publication and hands the item back with a
blocker. A repository with no configured remote publishes nothing and behaves
exactly as a purely local project does.

**A target branch the forge protects is the exception to "local first."** Before
it promotes, a publishing run asks the forge whether the target is protected,
both ways GitHub protects a branch (per-branch protection, and a ruleset with a
pull-request, status-check, or update rule), which is the question `make release`
asks before a cut. On a protected target the run never moves your local target
branch ahead of the forge. It commits the change, checks the target still stands
where the change was written against, and asks the forge to merge with the local
branch untouched. The reviewed commit reaches the remote only by the forge's
merge, and the local branch then follows it by the same fast-forward catch-up,
under the branch's promotion lease. A merge the forge queued moves nothing
locally until `yoyo reconcile` finds it merged. A merge the forge refused or
dropped leaves the change on its pull request and on no target branch, so the
item is not closed: the run stops and hands it back with the forge's answer as
the blocker, and a re-arm the development manager records repeats the merge — at
the next pull, or at once by `yoyo triage rearm` — once the requirement is
met. A remote target that moved in the meantime is replayed onto, like any lost
race. A process killed mid-landing is settled by `yoyo reconcile` on the forge's
answer about the pull request, never on the local target, which the landing did
not move. A forge that cannot be asked is treated as protecting the branch, and the
run says so on the item's `Target branch:` line, which every publishing run
writes to name the path it took. An unprotected target keeps the local-first
order above. The reason is the two stalls this ended: on 2026-09-20 and again on
2026-09-24 a local promotion onto the protected `main` the forge would not merge
left `main` ahead of `origin`, and every later run collided with it until the
checkout was reset by hand.
[The configuration guide](configuration.md#a-protected-target-lands-through-its-pull-request)
has the whole of it.

Merging belongs to `approvals.integration`, so the two settings compose rather
than imply one another. Publishing with `integration: human` opens the pull
request and stops: nothing is merged, the run branch survives on the remote, and
the worktree is preserved for you — which is what a `human` integration policy
means. See the
[configuration guide](configuration.md#publishing-through-pull-requests).
