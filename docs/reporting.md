# What comes back to you

*For an operator asking what the work cost and what came back. Part of
[yoyo's documentation](../README.md#further-reading).*

## What the work cost

Ask what is done and the completed items come back with a price tag:

```text
completed (3):
  [yoyodyne-ifd.2.7] p1  $27.93 Resume an interrupted run
  [yoyodyne-ifd.12]  p2 ≥ $4.50 Pause on a provider usage limit
  [yoyodyne-ifd.13]  p2         Publish a pull request
  ≥ marks a floor: some runs of that item have no surviving record and could not be priced.
  1 completed item(s) carry no price: work the harness did not run has none, and work it did is priced by `yoyo cost --record`.
```

The price is of the item rather than of a run, which is the only figure that
answers what a piece of work cost: every run made for it counts, including the
attempt that was rejected, the repair attempts, and the reviewer's invocation
beside the developer's. One item in this repository cost roughly twenty-eight
dollars across a rejected attempt and a successful one; a per-run view shows two
numbers and this shows the truth.

Every figure is the provider's own report of what an invocation cost, read from
the run's event log, never an estimate from a price table that drifts the moment
a provider changes what it charges.

**What an invocation cost is not always what the provider said it cost.** A
provider asked to resume a session reports what that session has cost since it
opened, so the second invocation of a session reports the first one's money over
again. An invocation is therefore priced at what its own session's reported total
moved by — and the first invocation of a session, which has no earlier total to
have moved, at the whole of what was reported. A figure that did not rise is a
beginning rather than an increment: that is a provider reporting each
invocation's own cost, or one whose running total restarted, and both are
recorded whole. Nothing asks the provider which of the two it does; the rule
reads it off the figures, and it can never produce a negative one.

An invocation the provider ended twice is priced at both endings. The second
terminal is recorded as a `duplicate_terminal_result` anomaly rather than as a
second invocation, but the provider charged for the turn that produced it, so
its figure is priced by the same rule against the one before it and added to
the invocation it followed — in the same phase, without counting another
invocation. Until yoyodyne-ifd.435.2 that money was on the anomaly event alone,
and where nothing resumed the session afterwards no surface counted it: $0.88
of run-f3755e3f's last attempt.

That is a correction rather than a refinement, and it is the difference between
this product's recorded spend and the operator's bill. Every figure on this page
was wrong by it until yoyodyne-ifd.432.10. Management conversations resume one
session across every turn, so the development manager's own conversation read at
$24,659 against an actual $825; repair attempts resume the run's developer
session, so repair read at $1,467 against $351. Taken across this machine's
records as they stood, `yoyo status --spend` read the last seven days at $33,400
where it now reads $3,108, and `yoyo cost` read every run ever made at $12,578
where it now reads $10,668. What was never affected is a run of one development
attempt and one review, since each of those opens a session of its own — which is
why the overstatement hid in the repairs and the conversations rather than
showing up in every total at once.

Reading the rule off the figures is also its one limit, and the limit falls
entirely on records written before a provider began reporting running totals.
Claude Code began on 2026-09-19 — its schema now calls the field "cumulative
estimated cost … for this query() call", and
[`docs/diagnoses/yoyodyne-ifd-424-one-shot-cache-reads.md`](diagnoses/yoyodyne-ifd-424-one-shot-cache-reads.md)
is where it was first caught — and before that each figure was the invocation's
own. Two of those in a row that happen to rise are indistinguishable from a
running total, so they are differenced and the later one reads as less than it
cost. Over this product's recorded history that understates the five weeks before
the change by $796 of $8,325, against the $30,000 the other direction was costing
after it. Nothing tells the two apart from the money alone, and the alternative —
a date in the code — would be this machine's upgrade hour written into every
installation's ledger.

A run finishing writes the item's total onto the item in the tracker, and that
recorded total is what travels with the work: `/status`, the Lead Product Manager's
briefing, and `bd` itself all read the one number the tracker holds, rather than
each assembling a price of their own. `/show` is the exception, deliberately: it
prices the item from the run records themselves every time it is asked. That is
what lets it answer for an item nothing has recorded a price for yet — anything
finished before this existed, or an item whose run could not write its price
down — and it is why the two can differ. Where they do, `/show` is the current
one and the tracker is what was last recorded; `yoyo cost --record` makes them
agree. It agrees them by writing over what the item carries rather than by
filling in what it lacks: the price is re-derived from the run records every time
and stored whichever way it moved, which is what carries the correction above
onto every item priced before it — each of which is still carrying a total summed
from session running totals until the backfill is run.

Three things it deliberately will not do. A run whose event log no longer
survives is priced as unknown rather than as nothing — it is counted, left out
of the total, and marked with `≥`, because a zero meaning "no record" would
quietly understate every total it entered. An item the harness has never run
carries no price at all rather than a price of nothing. And what is priced *per
item* is runs: the conversations that steer them cost money too and are recorded
just as durably, but attributing a conversation that discussed five items to any
one of them is a judgement rather than a join, so it is left out here and said
to be left out. The same holds for an
[exchange](conversation.md#roles-asking-each-other-things): its record names the
product, the repository, the two roles in it, and the conversation the asker
spoke from, and nothing that identifies a piece of work, so there is nothing to
attribute it to rather than a judgement declined. Neither is left out of
what the harness has spent altogether —
[`yoyo status --spend`](operations.md#following-a-run-a-conversation-or-a-branch-review) prices
conversations, branch reviews, side threads, and exchanges beside runs, and
`yoyo cost` carries the exchanges and the side threads into its total on rows of
their own, because a total that skipped any of them would be wrong rather than
merely unattributed. A side thread is attributed to the conversation it was
opened beside rather than to a work item, for the reason a conversation turn is.

`/show` breaks one item's price down by attempt, which is what a single total
invites:

```text
cost: at least $27.93 across 3 run(s)
  run-0123…  started 2026-08-10T09:14:02Z [stopped, reviewing] $8.91 from 3 invocation(s)
  run-89ab…  started 2026-08-10T11:02:41Z [succeeded, complete, integrated] $19.02 from 2 invocation(s)
  run-cdef…  started 2026-08-09T18:30:00Z [failed, developing] unknown: the run's event log is no longer recorded
```

The word in the brackets is the same fixed vocabulary
[`yoyo status`](operations.md#what-became-of-the-runs-and-what-remains-of-them) uses,
read from the same records: `stopped` ended on a blocker somebody has to decide
about and left its change intact, `failed` left nobody anything to act on, and a
run met in both places is described the same way in both.

From the command line, `yoyo cost` prices items from the same recorded runs —
one line per item, or a run-by-run breakdown when you name one — and
`yoyo cost --record` writes those prices onto the items. That is also the
backfill: the run state and event logs of everything already finished are still
under the state directory, so items closed before any of this existed can be
priced retroactively rather than the ledger starting today.

```sh
./bin/yoyo cost                     # every item the harness has run, and the total
./bin/yoyo cost yoyodyne-ifd.2.7    # one item, broken down by run
./bin/yoyo cost --record            # write each price onto its work item
./bin/yoyo status --shipped         # the 10 most recently shipped items, with the wall clock beside the price
```

What shipped lately, and how long each item took as well as what it cost, is
[`yoyo status --shipped`](operations.md#what-shipped-lately-and-what-it-took):
the same per-item join, read for the items whose work the harness promoted and
sorted by when each shipped, with elapsed time from first claim to promotion and
the part of it spent parked stated separately.

### Where the money went

Every price `yoyo cost` reports is split by what the money bought, per run, per
item, and across everything the harness has run:

```text
item                                     runs  unpriced      develop       review       repair         cost  cached    waited
yoyodyne-ifd.1.5                            4         0       $29.18        $5.78       $14.47       $49.43   68.4%     3h37m
ASKS BETWEEN ROLES                          -         0            -            -            -        $4.12       -
-----------------------------------------------------------------------------------------------------------------------------
TOTAL                                     176         1   ≥ $1764.42    ≥ $234.41    ≥ $732.75   ≥ $2735.69   61.2%    21h01m
```

The rule above the total is the shape
[`yoyo status --spend`](operations.md#following-a-run-a-conversation-or-a-branch-review)
closes its table with, and the shipped ledger closes the same way: the operator
reads all three and asked for one shape across them.

**ASKS BETWEEN ROLES** is what the roles spent asking each other, summed over
every recorded exchange. It is a row rather than a note under the table because
the total has to add up: an ask is a provider invocation the harness made and it
belongs in what the harness spent, and a figure visible only in the total would
be a difference the reader has to take on trust. It is one row for the product
rather than a figure per item because the record names no item — and because the
channel runs between the roles that own documents and queues, with the developer
and the reviewer, the two that work inside a run, off it. If an exchange ever
records the work it was taken for, it belongs in that item's price beside the
runs instead. The columns that split a run's price are left empty rather than
filled with zeros — an ask is not development, review, or repair — with one
exception: **unpriced** means on this row exactly what it means on an item's. An
exchange record that cannot be read is counted there, left out of the figure
beside it rather than counted as nothing, and every exchange that could be read
is still priced; the total carries the same `≥` a run's unpriced attempt puts on
it. That marker goes on the total column alone, since money nobody could read is
money that belongs to no phase.

Exchanges that cannot even be listed are the one case with no floor to state:
how much is missing is unknown, and so is how many records it is missing from,
so the row reads `unknown` rather than a figure.

**SIDE THREADS** is what the agents' side conversations spent, priced from their
own event logs by the same reader `yoyo status --spend` uses, and it sits beside
the asks for the same reason: it is money the harness spent that belongs to a
conversation rather than to an item. Under the table each conversation a side
thread was opened beside is listed with what its side threads cost, which is the
attribution the row itself has no column for. Its **unpriced** column counts
side thread logs that could not be read, and its **cached** column is filled,
because a side thread's terminals carry the provider's usage as a run's do. The
row is absent on a product whose agents have never held a side thread.

**develop** is each run's first developer attempt, **review** is every reviewer
invocation it made, and **repair** is every developer attempt after the first —
the failing check, the refused path, and the reviewer's findings handed back are
all repair, because from the money's point of view each is the same thing: the
change being made again because it was not right the first time. Every priced
run invocation that says which part of the run it served lands in exactly one of
the three, so nothing is missing from them and the split is a decomposition of
the price rather than a second opinion about it. An invocation the provider
refused or killed and the harness reissued is
charged to the attempt it was reissuing, not counted as a repair nobody asked
for: what an attempt cost is what it took to get it made.

An invocation that ends in a run's log without saying which part of the run it
served is the one thing none of the three will take. It is counted in the total
and named in a line under the table instead — how many, and what they cost —
because charging it to a phase would put somebody else's money in a column the
operator reads to decide something. That line is absent from a healthy record:
anything in it is a defect in whatever wrote the log rather than a cost of the
work. What "without saying" means exactly, and why the runs recorded before
anything could say are unaffected, is below.

**waited** is time rather than money — a provider that would not serve the
account, and the harness parked on the operator's hold — and it is counted apart
for that reason, since adding it to the money would make a run that waited
overnight read as expensive when what it was is slow. It comes from the run's
own record rather than from its event log, which is why a run nothing can price
still says how long it was held up.

**cached** is the one column that is not money: the cache-read share of input
tokens, `cache_read / (input + cache_read + cache_creation)`, read off the same
terminals the price is. It is here because the money on its own cannot answer
the question a token-efficiency change asks of itself — a run that got cheaper
because the provider changed its prices and a run that got cheaper because more
of its prompt was already cached are the same figure in dollars and opposite
facts about the harness. It is the measure a change made to share a longer
prompt prefix is kept or reverted on, and the first instrument any later
input-token lever has.

The denominator is every input token however the provider billed it, not the
fresh input alone: a prompt served entirely from the cache is reported with
almost no fresh input, and dividing by that would make the emptiest prompt read
as the best cached one. An invocation whose terminal carried no usage object at
all is counted apart and named under the table rather than added in as nought —
a run nobody measured and a run measured at nothing are the same figure and
opposite facts, and folding the first in would read as a caching change that
achieved nothing. The same distinction runs the other way: an invocation the
provider really did report as reading nothing keeps its `0.0%`, because that is a
reading, and a window where nothing reported usage says so instead of showing
one. What separates them is the count of invocations behind the share, which is
why every line carries it. The column carries no `≥`: it is a share rather than
a sum, so an unpriced run does not leave it short, and the ask row leaves it
empty because an exchange record carries what its rounds cost and no token counts
at all.

The share is over provider invocations rather than over runs, so a window is
whatever set of runs you take it across — which is what makes it answer a
before-and-after question about a change to what the harness sends. Whether it
can answer one is a separate matter, and the ifd.84 experiment is the worked
example of it not being able to: most of the cache-read in any window is a
developer session re-reading its own conversation, so a lever worth a few
thousand tokens of shared prefix does not move an aggregate built from tens of
millions. See
[`docs/experiments/yoyodyne-ifd-84-prompt-prefix-stability.md`](experiments/yoyodyne-ifd-84-prompt-prefix-stability.md)
for the numbers and what they did and did not establish.

The line under it is that experiment's lesson made into an instrument. It says
the same share once per phase:

```text
cache-read share by phase: development 98.4% over 296 invocation(s), review 0.5% over 534 invocation(s), repair 95.8% over 252 invocation(s)
```

The phases neither assemble their prompts alike nor cache alike. A developer
session resumes and re-reads its own conversation on every turn; a review is one
short invocation with no session to resume, whose only cacheable part is the
prefix it shares with every other review. Summed, the larger decides the column,
and a review reading nothing at all leaves that column at ninety-seven per cent —
which is exactly what it did, unnoticed, for as long as the harness has kept
usage. So a change to what one phase sends is read here, and the column above is
what the harness costs altogether. Each phase carries the count of invocations
behind its share for the reason every other line does, and a phase nothing
measured is a dash rather than a nought; a window where nothing measured anything
has no line at all, because the one above it already says so.
[`docs/experiments/yoyodyne-ifd-205-review-prompt-cache.md`](experiments/yoyodyne-ifd-205-review-prompt-cache.md)
is the finding it was built for.

Naming an item says the same thing per run, under each attempt:

```text
yoyodyne-ifd.1.5: $49.43 across 4 run(s)
  development $29.18 from 4 invocation(s), review $5.78 from 4, repair $14.47 from 3; waited 3h37m for the provider
  cache-read share 68.4% of 41905311 input token(s) over 11 invocation(s): 28663234 cached, 12984077 fresh, 258000 written to the cache; 194422 output
  cache-read share by phase: development 71.2% over 4 invocation(s), review 0.0% over 4 invocation(s), repair 69.8% over 3 invocation(s)
  run-c25525d6…  started 2026-08-18T14:31:34Z [cancelled, developing] $26.93 from 3 invocation(s)
    development $22.84 from 1 invocation(s), review $0.96 from 1, repair $3.13 from 1; waited 3h37m for the provider
    cache-read share 71.0% of 19218662 input token(s) over 3 invocation(s): 13645250 cached, 5461412 fresh, 112000 written to the cache; 88104 output
    cache-read share by phase: development 74.1% over 1 invocation(s), review 0.0% over 1 invocation(s), repair 66.3% over 1 invocation(s)
```

The split is read out of the run's event log, which is what makes it answer for
runs that finished long before it existed. Each invocation's terminal names the
role it was made as, so the reviewer's invocations are the reviewer's because
they say so, the developer's are the developer's for the same reason, and they
group into attempts by how each one ended. An invocation naming any other role,
or naming none at all, is left unattributed rather than placed.

Runs recorded before event schema version 2 had no role on their terminals to
omit, and are read the way they were written: a review announces itself with a
`review.started` and then makes exactly one invocation, so the terminal after
that announcement is the reviewer's and every other terminal is a developer's.
The schema version on each event is what confines that reading to those runs, so
a terminal written today with no role is never read positionally — it is an
invocation that could have said whose it was and did not, which is unattributed
money rather than a phase.

That older inference was sound for the runs it covers, and this is the evidence
it rests on. Only two things have ever written a terminal into a run's log: the
developer's attempts, and the reviewer, which always announces itself with a
`review.started` first. Every other provider invocation the harness makes writes
somewhere else — a conversation turn to its conversation's log, an inter-role ask
to the exchange record, and a branch review, **including every shadow review of
the ifd.92 experiment**, to its own log under `branch-reviews/` rather than under
`runs/`. So the closed shadow experiment polluted no run's phase data, and there
are no affected runs to name: not because its invocations announced themselves,
but because none of them was ever written into a run's log at all. The sweep in
`internal/execution/terminal_test.go` is what keeps that set of writers closed.

### Every provider spend, one line

Beside all of that there is an append-only cost log, one line per provider
invocation, written the moment the provider says what that invocation cost. It
lives at `<state root>/products/<product id>/spend.jsonl`, and every process the
harness runs appends to it: the developer's first attempt and every repair after
it, every review including a branch review, every management-conversation turn,
and every round of an inter-role exchange.

Each line carries the role and the configured agent that spent it, the phase, the
amount and its classification, the account alias and the configuration revision
that applied, the revision the harness binary that made the call was built from,
the one thing the invocation belonged to — and the work item, where the invocation was
made for one — the backend, the adapter version that reached it, the requested and
resolved models, and when it happened.

The backend, the adapter version, the account alias, and the requested model are
one identity rather than four facts: they are the execution endpoint the turn was
served by. The adapter version is the one of the four that is not obvious. A
provider a project declared for itself is reached by an adapter this build ships,
so "which provider" and "which harness code read what it said" are separate
questions, and a line carrying only the first cannot answer the second. It is the
adapter's version rather than the provider CLI's: what a record has to be able to
tell apart is two harness builds reading one provider differently.

The last pin is the build, and it is taken by the metering itself rather than
supplied by whatever is invoking: a build a call site could pass in is one a call
site could forget, and the line would then say whose account paid for an
invocation without saying what code made it. The harness always knows the account
and the revision, so the build and the adapter version are the two pins that can
be absent on a line the harness wrote: a binary installed from the module cache
carries no revision of its own, and an invocation that died before its adapter
could say anything on a provider this build has no description of leaves nothing
to name. Each absence is recorded as one rather than guessed at, because a
comparison nobody can make is an answer and a comparison made against the wrong
thing is not.

That one thing is a run, a conversation, an exchange, or a branch review, and a
line names exactly one of the four. A branch review has a field of its own rather
than borrowing the run's: it is not a run and nothing ever made one for it, so a
line that carried its identifier as a run id would hand anything joining these
lines back to run records an id naming no run.

**A line's amount is that invocation's own cost**, by the rule above, and never
the session's running total. Where the two differ the figure the provider
actually reported is kept beside it as `reported_total_usd`, so the correction
can be checked rather than taken on trust and the session's next invocation has
something to be priced against. A line with no such field is one the correction
left alone: the invocation that opened its session, an invocation with no session
at all, and every line written before any of this existed, whose `amount_usd` is
the figure the provider reported. Those older lines are re-derived by the same
rule when the log is read, from the session identifier every line has always
carried — the file itself is left exactly as it was written, because it is the
evidence of what the provider said, and a money record edited after the fact is a
worse thing to hold than one every reader corrects. Adding up `amount_usd`
straight out of the file therefore still overstates the part of it written before
2026-09-22.

Three things it does deliberately. An invocation the provider ended without
pricing is classified `unknown` rather than recorded as zero or left out, because
a zero meaning "nobody was told" understates every total it enters by however
much was really spent. Nothing here aggregates: adding the lines up is yours, and
any later query builds on the same lines rather than on a rollup something
decided for you in advance — which is also what makes the log evidence about what
each model charges rather than only about what they charge together. And a line
that cannot be made durable fails the invocation it belonged to, the same weight
an unrecordable event log carries, with one exception: a conversation turn comes
back anyway and says on the reply what could not be recorded. The provider has
already written that answer and already charged for it, and losing the answer to
report that the bookkeeping missed would cost you both.

None of the prices above change with it. `yoyo cost` still reads a run's event
log, which is what lets it answer for runs that finished long before this log
existed; this is the record that does not have to be reassembled from one. Both
work out an invocation's own cost by the same rule, from the sessions the
terminals name, so the two records agree about what a resumed session cost. The
one place they differ is their reach: this log follows a session across every
process that resumed it, and a run's event log sees only that run's terminals, so
a session a later run resumed begins again there.

`/diff` says what a run changed. It reads the run's own durable record rather
than shelling out to git, and that is what makes it survive success: a run is
cleaned up once it integrates, its worktree removed and its branch deleted, so
anything that answered by diffing a tree would stop having an answer exactly
when the work landed. The record keeps the file listing and the diff stat the
harness took while the worktree existed, along with the branch, the promotion,
and the pull request the work was published through — all still there to point
at after the tree is gone. Naming nothing asks about the run this conversation
last started — still going, already collected, or started by an earlier process
this conversation was resumed from, since the item it ran is written into the
conversation's own record rather than kept in whichever process happened to
start it. Naming an item asks about the most recent run of that item, whoever
started it. A run whose record holds no summary says so rather than printing an
empty listing that reads like one.

`/redirect` records your direction in the item's notes, where the developer's
context reads it on the next attempt, and stops the run first when the item you
are redirecting is the one running. It never changes the item's status: saying
what to do differently is not deciding that the work is done or blocked. Start
it again with `/work` when you want it retried.

## Every work item beside its title

A work item's identifier is always shown with its title. Wherever the harness
puts text in front of a person — the four lines of `yoyo status` and the
needs-a-human line among them, a program manager's lane report and the card the
dashboard opens on it, a pass's account in `yoyo sweeps`, the report pile a
program manager's digest is filed into (`yoyo reports` and `/reports`), and
every message the Slack sink posts — each identifier in it is shown beside the
item's title, in plain words:

```text
Blocked on 434.9 (Price a resumed session at what it moved by) and 434.3 (Say the provider's reset in local time).
```

That holds whoever wrote the text. On 2026-09-26 a lane report named work as
"434.9 and 434.3", with nothing saying what either was. The roles are told to
name an item by what it is, and a surface that printed whatever a role wrote
would still let the next bare number through, so the titles are put in by the
read model as the text is shown rather than trusted to the writer.

Closed items are titled too. Most of what a report or a pass names has
closed by the time anybody reads it, and `bd list` given no status leaves
closed work out, so the titles are read from a listing that asks for every
status; a test against `bd` itself holds that a closed item comes back with
its title.

An identifier the tracker holds nothing under is shown as `(unknown to the
tracker)` rather than dropped or left bare. That applies to anything shaped
like one of this tracker's identifiers — `yoyodyne-ifd.434.9`, or `ifd.434.9`
where one root carries that hash. A bare dotted number like `434.9` is read as
an item only where the tracker holds exactly one item it could name, because a
dotted number is also a version, a price, a duration, or an address: `3.5 hours`
and `$27.93` are left alone, and so is anything in a path, a link, or between
backticks, where a title put into the middle would break what somebody is meant
to type.

An item is titled once per piece of text: text that already says the title is
left as written, and a later mention of an item already titled is left bare. The
title is cut to a line where it runs longer. Where the tracker cannot be listed
the text is shown as it was written, because calling every number unknown over
a listing that failed would be false. The Slack sink lists the tracker at most
once a minute rather than once a message, so an item admitted in the last minute
can reach the channel by its number until the next listing.

The records themselves are not changed. A lane report, a report, and a pass's
account are kept exactly as their author wrote them, and `yoyo reports --json`
and `yoyo sweeps --json` carry them that way. What the read model hands a
surface is titled: a program manager's blockers in `yoyo status --json` and the
lane report the dashboard's card reads, and the attention entries, which carry
the titled sentences beside the derived ones as `said_what` and `said_whose`.

## The words roles write for a person

What a role writes for a person is held to [the terms register](terms.md),
just as the documents and the strings in the code are. The read model checks
a program manager's lane report and post-mortem, a digest, a sweep or pass
account, and the words on the attention line as they are shown. Every message
the Slack sink posts passes through the same check, including replies and
direct messages. A word the register permits is left alone; a known coined
word with no entry, or one listed as replaced, is flagged beside the text:

```text
The provider's posture changed. [wording: "posture" was replaced; write tool access, meaning the tools a role may use]
```

The flag names the word and the wording to write instead, using the register's
replacement where it gives one. It stays readable without colour or emphasis.
The author’s text stays as written: the check adds the flag at render time,
and changes no lane report, collected report, or pass account. The raw accounts
in `yoyo reports --json` and `yoyo sweeps --json` remain the author’s words.

A recurring pass records its language findings beside its account, under
`wording`, including those from the lane reports and digests its turns wrote.
The next pass of that task that takes a turn is told which words to correct.
A firing that takes no turn does not consume that reminder, and a language
finding stops no work and asks no person to approve anything.

The register is read from the product's repository when text is rendered, so
changing a row takes effect on the next reading without rebuilding the harness.
A project with no `docs/terms.md` has no register to apply; a register that exists
but cannot be read is named beside the text as a check that could not be made.
The vocabulary and spelling rules are the document check's: fenced code is
left alone, and words broken by a line wrap or joined by a hyphen are read the
same way. The check catches known words; a word coined this morning still needs
a reviewer to notice it.

## What agents report, and where it reaches you

An agent used to be able to reach you only by failing. A spent repair budget
becomes a durable blocker, a failed run is reported where you are already
looking — and everything an agent noticed while its work *succeeded* survived
only as prose in a run summary copied into an item's notes, where nothing
surfaces it. Two real examples from a single session reached the operator only
because a person happened to be reading: a reviewer's observation that the
built-in bundle's declared version had gone inert, and a developer's report that
`bd lint` could not run in its sandbox.

Every role can say such a thing without stopping: the developer, the
reviewer, and the Lead Product Manager each end what they say with one small block,
and the harness collects it. `/reports` shows you the pile, newest last, with
the twenty most recent listed and the rest counted.

`yoyo reports` shows the same pile without opening a conversation, which is what
a run finishing overnight needs: it says it reported something, and reading it
must not cost an interactive conversation with a provider behind it. It prints
the whole pile rather than the most recent twenty, because a command's output
can be paged and piped where a listing beside a conversation cannot, and
`--json` hands a script the collected records themselves. Both listings are
read-only, and both say which reports somebody has already decided about: a
report is written once and never revised, and neither of them retires one,
handles one, or decides anything at all.

```sh
./bin/yoyo reports                 # the whole pile, oldest first
./bin/yoyo reports --json          # the collected records, for triage or a script
```

A report is deliberately not a blocker, and nothing about it behaves like one.
The run carries on exactly as it would have: an approving verdict that mentions
something still approves, a developer that reports something still finishes, and
a report the harness cannot read or cannot store costs its run nothing at all —
it is named on the outcome instead, because a report nobody kept would otherwise
be silence. That is the property worth relying on, since a channel that could
cost an agent its run is one agents learn not to use.

Each collected report carries the role and the configured agent that made it,
the run or conversation it came from, the work item where there is one, a
severity — `critical`, `warning`, or `note` — and the text. That is enough
structure to filter the pile later without deciding now how it should be
filtered; an agent that judges which of its own observations are worth your
attention is a later question, and nothing here does it. The severities are
deliberately not the reviewer's `blocker`/`major`/`minor`: a finding decides
whether a change is repaired, and a report decides nothing.

### Which build a report is about

A report is a claim about the build that filed it, not about the tree as it is
when you read it. So each one also carries the harness revision its invocation
executed: a run's report carries the build that run's record pins — the same one
`yoyo status <id>` prints on the run's `ran under ... harness ...` line — a
conversation's report carries the build holding the conversation, a branch
review's the build that made the review, and an exchange's escalation the build
its latest round ran under. Without it a report about a defect fixed since reads
exactly like one about a live defect. That is how the invariants-index gap, fixed
on 2026-08-23, was admitted as fresh work twice more — `yoyodyne-ifd.201` and
`yoyodyne-ifd.380` — each costing a run to find the fix already on main.

Every listing prints the build beside the run, and says how far it is behind the
target branch's tip:

```text
  !  report-… [warning] 2026-09-22T09:14:02Z from the developer on yoyodyne-ifd.380 (No run's invariant delivery names the invariants README as an unreadable invariant) (run-…, build 0123456789ab, 31 change(s) behind the target branch)
     report-… [note] 2026-09-22T11:02:41Z from the reviewer on yoyodyne-ifd.402 (A docket entry names the open publication and how to arm it, …) (run-…, build fedcba987654, the target branch's tip)
     report-… [note] 2026-08-25T18:30:00Z from the developer on yoyodyne-ifd.201 (The invariant loader skips the directory README, as everything else already documents) (run-…, no build recorded)
```

`yoyo reports` and `/reports` print it that way, each item
[beside its title](#every-work-item-beside-its-title), and the reports carried into
the Lead Product Manager's turn are printed the same way, with the instruction to
check whether a fix has landed before admitting work from a report whose build
is behind. A build behind the tip is not a verdict. It says the fix may already
be there and is worth checking, and the report's own run is where to start. The
count is the one the channel uses for a watch session's build: the commits the
product's checkout holds past the build, counted by Git (`rev-list --count
<build>..HEAD`), where HEAD is the target branch every run is written against.
`yoyo reports --json` carries it as data, under `builds`, keyed by the build.

Reports filed before reports carried a build have none, and neither does one
filed by a binary that recorded no revision of its own. Both say `no build
recorded` rather than being read as current. A build the product's repository
does not hold is not counted either: the builds are the harness's own revisions,
so the count means something only where the product is the harness's own source,
and nothing assumes that. Such a report says its build was `not counted against
the target branch`, and the listing says why once, under the reports.

The severity is the one recorded signal of importance, so every surface that
shows you a report renders it rather than merely printing the word. In a listing
it is a mark in the column before the identifier — `!!` for critical, `!` for
warning, nothing for a note — and a colour where the terminal permits one, red
and bold for critical and orange for warning. The mark is the part that always
holds: piped to a file, read under `NO_COLOR`, or shown on a terminal that says
it is `dumb`, a critical report still does not read like a note. `--json` carries
none of it, because there the severity is a field.

A run's closing lines carry the same signal without listing the pile. Instead of
"reported 3 thing(s)" they say how many of what and mark themselves by the worst
of them — `!! reported 3 thing(s) without stopping the run (critical 1, note 2)`
— which is the difference between a line an operator can skip and one they
cannot. What became of a report is printed under it undressed, whatever it was
filed at: a plain line beneath a loud one is what says somebody has already dealt
with that one.

Volume is the risk this design has, and the answer to it is in the role
contracts rather than in a filter. Every contract says what merits a report — a
risk worked around, an assumption that may not hold, a defect or a stale
document outside the assigned work, something in the environment that stopped a
check being run — and says plainly that most replies should carry none, because
a channel full of routine observations is worse than nothing: it looks like
coverage. That guidance is in Go, alongside the rest of each contract, so no
persona can loosen it.

The pile lives outside the repository under the operating system's state
directory, beside the run and conversation records rather than among them. It
outlives them: a run is settled and its worktree and branch are removed, and
what it reported is still there for you to read.

One entry in the pile is filed by the harness rather than by an agent: an
[inter-role ask exchange](conversation.md#roles-asking-each-other-things) that reached the
round limit it was opened with closes as unresolved and escalates itself here, at
`warning` severity, naming the two roles, the question, the rounds it spent, and
what it cost. It is a report rather than a blocker for the same reason everything
else here is — nothing was stopped, and two roles simply did not settle something
one of them needed — and it is filed at all because an exchange that ended in a
silent limit is exactly the failure nobody would otherwise see.


### Who reads them, and what became of each one

A report that only you can read is a report that reaches triage when you happen
to be reading. That was the whole of it until recently — you read the channel,
noticed something, and repeated it to the Lead Product Manager yourself — which routes
an agent's escalation through you rather than through the role the goals put in
front of it. The Lead Product Manager could not have read the pile if it wanted to:
its evidence is the specifications, Beads state, and the documentation of what
ships, and the pile is none of those.

So the reports nobody has decided about are carried into its conversation, the
way changes proposed to its documents already are. Each names itself, the role
and agent that filed it, the work item where there was one, the run or
conversation it came out of, and
[the build that filed it and how far that build is behind](#which-build-a-report-is-about).
That is enough to act on without going and fetching anything, including
checking whether a fix has already landed before work is admitted.

They arrive as a walk through the pile rather than as its worst slice. The
conversation carries a durable position in the order the pile was filed; a turn
is offered what that position has not passed, oldest first, and the position
advances over what was actually shown, so the next turn resumes rather than
starting over. Anything filed at `critical` jumps the walk, because something
already costing somebody has to be read today rather than when the walk reaches
it.

**A critical report does not wait for a turn to come round.** The pull after it
is filed — within a minute on a watching session — the harness wakes the Lead
Product Manager for it as a turn of its own: a firing of her report task out of
its cadence, ahead of anything else the schedule has due, with the critical
report in the message rather than somewhere in the pile. Each critical is
delivered that way once, and the pass records which it carried. From then on the
pass holds her to it: an account that says the pass is complete while any
critical it was shown — in that message, or carried into a turn ahead of the
walk — stands unhandled is refused as complete, naming the reports, and she is
asked for another turn; a pass that spends every turn without handling it is
recorded as partial. A critical is ended the way every report is, by a
handling, and one that has already resolved is handled by saying so. Her own
critical reports are not delivered back to her this way, because she filed them
in the conversation it would go into. It needs a task that wakes her — the one
[working the pile on a cadence](#whether-the-pile-is-draining) is — and a
project with none delivers a critical to her only in the next conversation
somebody opens, as it did before.

The bar was set by the night of 2026-09-27. A program manager's critical,
"Nothing is landing on the protected main", filed at 23:22 Pacific, was shown at
the head of the 03:40 sweep's slice and passed over while the sweep handled
other reports, and waited more than eight hours.

A program manager's warnings and notes have a softer version of the same
guarantee. One that has stood unhandled through two of her passes is named on
the next pass's message under **Overdue reports**, with how many passes it has
stood through, so a report about a whole lane cannot sit behind a pass that
keeps choosing others.

The overdue list previews at most 240 bytes of each message after joining
whitespace. Every cut is declared beside the preview, with the full message's
size in bytes. A role can read that report, or a report cited inside another
report, in the same turn through its existing tracker block:

```yoyodyne-tracker
{"actions":[{"action":"read","report":"report-00000000000000000000000000000001"}]}
```

On a report read, `report` replaces the work item's `id`; the two cannot be
given together. The harness reads the existing report store, records the
request and its result like other reads, and returns the whole message before
the role finishes answering. Messages are limited to 4 KiB when filed, and the
result uses the same 8 KiB bound as an item read: the message is kept whole,
with any cut to its attribution declared. The existing action-count and round
limits still apply. A handled report remains readable, and an identifier the
store does not hold is refused with that reason. Reading changes no report's
handling and no work item's state.

That ordering is the difference between a bound and a bottleneck, and this
project learned it the expensive way. Delivering the worst ten of a worst-first
listing takes the same ten every turn until somebody decides about one of them,
so everything filed behind them is invisible however long it waits: 564 of 1313
reports sat unhandled with the oldest three weeks old, while every turn showed a
full listing and a reviewer's report of a real defect aged in the pile with it.

The bound itself is still a bound — a pile nobody has worked through must not
become the whole of a turn — but it scales with the pile. A turn carries ten
while the pile is shallow and forty while it is deeper than fifty, and either
way it is cut to a fixed number of bytes, so what a turn actually holds is
limited by size rather than by a count. Whatever is not carried is counted, and
the count is of the whole unhandled pile: a role told the pile is five hundred
deep works at it differently from one told it is twelve.

Deciding what becomes of one is a product decision and it is the Lead Product
Manager's: work to admit, a proposal to put to you, a concern to raise, or
nothing at all — a report that asks for nothing is handled by saying so. It
records the decision with a `handle` action, the same bounded, recorded mechanism
it acts on the queue through, and that record is the only thing that takes a
report out of the pile. A report it read and did not handle comes back to the
next conversation. No other role has the action.

What is recorded is a second file beside the pile rather than a change to it. The
report stays exactly as its author wrote it — that is what makes the pile evidence
rather than a worklist somebody has been editing — and the handling beside it
carries who decided, when, in which conversation, and why. Deciding twice is two
records and the later one is what is read. There is no vocabulary of outcomes:
"admitted as ifd.150", "already fixed", "not worth doing" are the same fact to
everything that reads this, and the reason says which.

The one structure a handling does carry is for a report handled as covered by
work. A report can ask for two things, and a reason naming one covering item
answers for both whether that item covers both or not: on 2026-09-05 a
development manager report asking the docket to consume recorded decisions *and*
closed status was handled as covered by `yoyodyne-ifd.269`, which did the
decisions, and the closed-status half lapsed silently until it surfaced three
weeks later as 125 dead docket entries. So such a handling lists the report's
requests in `requests`, and gives each exactly one answer: `covered_by` names the
item that already covers it, `admitted` names a creation earlier in the same
block that admits it, and `declined` says why nothing is being done. A request
with no answer refuses the whole block, and the refusal quotes the request; so
does a reason that says "covered by" an item with no mapping beside it. An
admission the block asked for that did not happen fails the handling, and the
report stays in the pile. The harness notes on every item that answers a request
which of the report's requests it answers, and records the mapping on the
handling — with each admission by the identifier it was assigned — so the
development manager or a program manager can check later that each covering item
actually covered what it was said to.

One handling is not a closing. A report whose answer is a change only you can
make by hand — a file the harness may not write, a credential, a workspace
setting — is handled with `"needs": "operator"` and a reason saying what you
have to do, and that record is [a finding that needs your hand](#a-finding-that-needs-your-hand):
said to you once, directly, and named on `yoyo status` until a later handling of
the same report records the change made. It is a field the harness reads rather
than a sentence in the reason, because a sentence is what six such reports were
for a month. A report filed at `critical` is the same kind of finding on its
own, until somebody handles it. A report handled as yours counts as handled and
is not offered to the Lead Product Manager again, so her turns list the findings she
has handed you, each with its identifier, until she records it done: tell her
when the change is made, and she handles the same report once more, without
`needs`.

Where the decision is work, the admission can name the report it came from, and
the item then records it. That citation is not bookkeeping: it is what a later
admission citing the same report is checked against, and where one is found
nothing is created and the result names the item that report already produced.
`yoyodyne-ifd.274` and the closed `yoyodyne-ifd.229` were both admitted from one
developer report, the second after the first had landed, and it cost a run that
could not have succeeded — see [the conversation](conversation.md) for what else
a creation is refused for.

That is what `/reports` and `yoyo reports` are showing you when they count the
unhandled ones and print what was decided under the rest, with each mapped
request and what answered it on a line of its own. It is also the honest
limit of it: the harness carries reports to the role that decides, and nothing
here judges whether it decided well.

### Whether the pile is draining

Neither the walk nor the bound is worth anything if nobody talks to the Lead Product
Manager, and nothing in the harness talks to it on your behalf unless you have
said it should. **The pile is worked on a cadence only once you configure a
recurring task to do it**, the way the development manager's sweep is
configured; the harness ships the task as a commented example in the file `yoyo
init` writes and enables nothing by itself, because which roles are woken and
how often is a project's judgement rather than a release's.
[Working the report pile on a cadence](configuration.md#working-the-report-pile-on-a-cadence)
is the entry and what it should say. A project that has not added it works the
pile only when somebody opens a conversation with the Lead Product Manager, which for
a pile of hundreds is not often enough — that is the state this project was in
when 564 of 1313 reports were unhandled with the oldest three weeks old.

Once the task is configured, nothing about the turns it takes is special — the
same persona, the same authority, the same `handle` action — and what each pass
decided is on the record twice over, as a handling beside each report and as the
pass's own durable account in `yoyo sweeps`.

**A finding has to leave a trace outside the pass's account.** The account in
`yoyo sweeps` is read by a person; the role never reads it back, and its
conversation is compacted. So a pass of any role that reports findings has to
leave, on the same pass, at least one of four traces: a memory written, its lane
report changed where it keeps one, a report filed, or work admitted. Every sweep
and pass message says so. After each pass the harness reads whether the account
reported findings of the role's own — the forge's open pull requests the harness
lists on a development manager's pass are not hers — and whether any of the four
happened. A pass that found something and left none is recorded as untraced:
`yoyo sweeps` marks it `UNTRACED`, and `--json` carries `untraced` beside the
traces it counted, `saved`, `reports_filed`, and `admitted`. The task's next pass
that takes a turn is told which findings they were, so the role can leave the
trace then, and until that pass the untraced pass is an entry on the attention
line with the role as the one to move, under `Waiting on the development
manager` or whichever role's pass it was. It asks nothing of a person. The
dashboard's factory-problems section, once it is built, is meant to show it as
one of that section's problems.

**A factory stall is reported by the harness itself.** When no work has been
pulled and no recurring pass has succeeded for longer than
`execution.factory_stall_after`, the supervisor files a critical report in the
harness's own voice, naming how long, the last success, and what each pass
failed on; it files it once per stall, and files a note when a pull or a
successful pass ends it. While the stall stands it is an entry on the attention
line with the harness as the one to move. It is read by the supervisor rather
than by any role's pass because the roles that would notice are the ones
failing — see [saying when the factory has
stalled](configuration.md#saying-when-the-factory-has-stalled). The
factory-problems section, once it is built, is meant to show it too.

Whether it is keeping up, or whether there is no cadence at all, is a question
about a week rather than about a moment, so every listing of the pile leads with
the two numbers that answer it:

```text
reports: 564 of 1313 collected report(s) are unhandled, the oldest filed 22d ago, 3 of them critical
```

Where any unhandled report was filed at `critical`, the line counts them in
place of naming the worst severity, because each one is somebody having to act
and "worst critical" reads the same for one as for five. Every number on it
comes from one derivation, so the terminal and the channel cannot disagree
about them. A pile that is draining says nothing anywhere else; one
whose oldest undecided report has been waiting more than a week is named on
`yoyo status`'s "needs a human" line as the Lead Product Manager's. That line is what
catches both failures a single reading cannot tell apart: a cadence that has
stopped keeping up, and no cadence configured at all. The queue of proposed
amendments has the same line as a sibling, on the same threshold and for the
same reason — see [working the amendment queue on a
cadence](configuration.md#working-the-amendment-queue-on-a-cadence).

## What agents propose changing, and who decides

The canonical documents each belong to one role, and that boundary is enforced
rather than asked for. A role that meets it and has nothing else to say has two
moves left, both bad: build against intent it believes is wrong, or edit the
document anyway. So it has a third — it proposes the change, in one small block
like the report block, and the harness carries it to the role that owns the
document and to you. The developer carries that block today, being the role that
meets the boundary while implementing against a document; the reviewer says what
is wrong with a change as a finding instead, and the Lead Product Manager stops and
asks you.

```sh
yoyo amendment list                       # what is waiting to be decided
yoyo amendment show <id>                  # one proposal and what became of it
yoyo amendment approve <id> --reason ...  # record the change as authorized
yoyo amendment decline <id> --reason ...  # turn it down, keeping why
```

Who is being asked follows from the document rather than from anything the agent
says: the harness resolves the artifact it names to its kind, and the kind to its
owner. A proposal about a document nobody records is refused, because there is
nobody to decide it, and a proposal from the role that owns the document is
refused too — that role amends it.

**A proposal is never a deferred edit.** It carries what should become true and
why, not replacement prose, and nothing in one ever reaches the document —
approved or not. Approving records that the owner's authority came down in
favour of the change; the change is then made by the owner, in the document, in
a revision recorded under that role. That is what keeps this from becoming the
slow path by which a downstream role redefines upstream intent: the only thing a
proposal can produce on its own is a decision.

Like a report, it costs the run nothing. The run integrates exactly as it would
have, and a proposal the harness cannot read or cannot keep is named on the
outcome rather than failing the attempt it arrived with. It is durable in the
same place and for the same reason: the run that argued the design was wrong is
long finished before anybody decides what to do about it. A developer that makes
the same argument again on a repair attempt raises one proposal rather than one
per attempt.

That naming reaches the agent as well as you **when the same role is invoked
again on the same run**, and reaches you alone when it is not. A refusal used to
reach you alone in every case, which is how a developer whose proposal named a
document nobody records went on to write into a checked-in file that it had
raised one — a false claim durable in prose, which only `yoyo amendment list`
disproved. So the refusal is carried on the run's own state, tagged with the role
that proposed it, and that role's next invocation on the run — a repair attempt,
a continuation triage granted, the re-ask an interim reply earns — opens with it
in the harness's own words: nothing was recorded, nobody was asked, do not
describe it as raised, take the claim back out of anything already written, and
propose it again if it is still worth proposing. That role's next reply spends
it, so a refusal is carried once and no further, and it is never shown to a role
that did not earn it. The developer carries the block today, so the developer is
the role this runs for; the tagging is what keeps it correct when another role
gets the block.

The condition matters because the ordinary run fails it: a developer invoked
once, whose only reply carried the refused block, is asked nothing afterwards and
is never told. The harness cannot read the block before the reply that carries
it, and there is no later invocation to open with the refusal. What covers that
run is the contract rather than the carry-back — every developer is told in
advance that writing the block is not the proposal being recorded, that the
harness can only answer after the reply, and that nothing it writes may therefore
claim a proposal has been raised. The carry-back repairs a claim the contract did
not stop; the contract is what stops it where nothing can carry anything back.

**What could not be kept is on the run's record, not only on the outcome.** A
proposal the harness could not read or record, and a report it could not read or
collect, are each written onto the run's state in the harness's own words —
`amendment_problem` and `report_problem`, the same two fields the outcome carries
— and `yoyo status <beads-id>` prints them under the run as `proposal not kept:`
and `report not kept:`. They were for a long time on the outcome alone, which
`yoyo run` prints once and which is gone with the process, so a refused proposal
and one made on a run that died before it reported both read afterwards as a
proposal never made; one run's three lost proposals went unnoticed for four runs
on exactly that account. The carried refusal above is spent by the developer's
next reply and this is not: it is what an auditor reads once the run is over.

The owner hears it where it works, and you are the one who decides. Proposals
against the brief and the goals are carried into the Lead Product Manager's
conversation and proposals against the designs, the specifications, and the
decision records into the architect's, and each argues for or against them and
can decide or edit nothing. So every decision is recorded by you through
`yoyo amendment` — the same override path `yoyo invariant` takes — and the record
says you exercised the owner's authority rather than that the owner answered.

## Reporting into Slack

Everything above needs you at the terminal, which is the wrong requirement for
work that runs while you are not. `yoyo slack` is the same account of the work in
a Slack workspace: one thread per work item, one message per milestone, and every
report an agent filed beside the item it was filed on. The backlog moving is a
milestone too — work admitted with the goal it serves, decomposed, attributed, or
reordered — so the queue changing is as visible as the runs it feeds. What the
top of the channel carries out of all that is what is important or needs you, and
nothing else; the rest is in the threads and in the durable records, and
[`docs/slack/setup.md`](slack/setup.md#what-it-posts) says which is which. Each
role speaks under its own name and in its own voice, and what no persona did — a
promotion, a merge, your own holds — arrives from the harness itself. It is a process you
start and leave running, and it needs your project to have opted in:

```yaml
# .yoyodyne/config.yaml
slack:
  enabled: true
  channel: C0123456789
```

```sh
export SLACK_BOT_TOKEN=xoxb-...   # this process's environment, and nowhere else
export SLACK_APP_TOKEN=xapp-...
./bin/yoyo slack                  # or --once to make a single pass and exit
```

That is the shape of it, and it is not the shape to leave running. Tokens
exported into a shell are inherited by everything started from it — the agents
the harness starts excepted, since every run's environment is built from an
allowlist rather than inherited — and on a machine running more than one
harness the sink you start second reads whichever pair that shell happened to
have: it connects, authenticates, and posts this project's work into another
project's channel. So the supported arrangement is a
launcher that reads **this project's own** secrets, stored under names that carry
the product, into exactly one process:

```sh
SLACK_BOT_TOKEN="$(security find-generic-password -s yoyo-slack-bot.<product id> -a yoyo -w)" \
SLACK_APP_TOKEN="$(security find-generic-password -s yoyo-slack-app.<product id> -a yoyo -w)" \
YOYO_SLACK_SECRET_NAMESPACE=<product id> \
exec yoyo slack
```

`YOYO_SLACK_SECRET_NAMESPACE` is not read as a credential and is not one: it is
how the sink records whose secrets it was launched with, so
[`yoyo doctor`](operations.md#checking-the-installation) can tell a sink that is merely
running from one that is running for this project. Leave it out and the sink
still works; what is lost is anything being able to notice when it is wrong.

On macOS, `yoyo slack ensure` is that launcher done by the harness: it starts a
sink only if nothing is reporting for this product, reading this product's own
keychain items into that one process. It is safe to run on a schedule — an
unattended pass every few minutes meets a running sink and does nothing — and it
is safe on a machine running several harnesses, because whether a sink is
running is asked of this product's lease rather than of the process table, where
one project's `yoyo slack` answers for every project's. With the Slack service
enabled in the [`services`](configuration.md#services) section, the product's
supervisor makes that same start for you:
[`yoyo start`](operations.md#starting-the-product-and-stopping-it) starts the
sink with the rest of the product and starts it again whenever it dies, within
the supervisor's bounds, so the timer is no longer yours.
[`docs/slack/setup.md`](slack/setup.md#6-start-the-sink) has the rest of it.

**What each severity means in the channel.** `critical` is what reaches you
wherever you are, so it is kept for what is yours to act on or already costing
somebody: a line stopped for hours, the provider holding every role, a stoppage
whose cause only a person can fix on the machine — a target branch that diverged
from the remote's, a credential the remote refused, a primary checkout carrying
state the harness does not own. `warning` is a real risk or a real decision that
belongs to one of the roles: a stoppage the work caused — findings nobody
repaired, a check that kept failing, paths the item never granted — is the
development manager's, and is said as one at the top of the channel. `note` is
the ordinary course of things: a stoppage the environment caused and a role or
the harness moves next — a lost race for the target, a replay the harness's
budget killed, a tracker read that timed out, a usage window — is said as a note
in the item's thread, because nothing was judged and nobody but that role has
anything to do. A stoppage's next mover is the read model's, the one the
docket, the pull's hold, and `yoyo status` read, and its `Next:` clause names
the same move they do. A cause only a person clears does not change that
mover — the harness's resume or the development manager's decision still
follows — and it is what makes the message `critical`, because neither can
happen until somebody has cleared it. It used to be `critical` for every
stoppage whoever decided it; on 2026-09-25 that paged the operator for an
approved change that lost its race for main twice and waited on the development
manager to re-run it.

**The top of the channel reads as a status board.** Each thread's opening message
carries one reaction saying what that item is doing now — working, with the
reviewer, blocked, or landed — replaced as the record moves and taken off when it
stops being true. So which threads need you is answerable by scanning the channel
rather than by opening them. Those four are the whole vocabulary: a status is
about the item where a severity is about one message, and the two never share a
symbol. It needs the `reactions:write` scope the checked-in manifest asks for, and
a workspace that refuses it costs the board and not one message.

One message there is a state rather than an event, and it is the one an overnight
asked for. A line that is **choosing nothing while work is ready** — intake held,
everything held, a target branch the harness will not catch up to the remote's,
the watch session unable to start work, the watch session idle, or no session running — says so again
every `--heartbeat`, an hour by default, naming what stopped it, how long that has
been true, how much ready work is behind it, and how many promotions are waiting
on the forge to publish them. That count is what a developer
run could actually be started for rather than everything the tracker calls ready:
the tracker's readiness is about dependencies alone, so its answer includes work
marked for a conversation and work the Lead Product Manager parked, neither of which
any pull will ever take. Counting those sent an operator three times to a line
that had not stopped. Everything else is a
transition and is said once, which is right for a thread and wrong for a night:
"intake is held" posted at 00:02 is ten hours stale by the time anybody reads it,
and the silence after it is indistinguishable from a healthy queue or a dead sink.
A session unable to start work repeats the cause recorded by the watch, such as
`runs cannot start: uncommitted changes in the primary checkout (<file>); commit or stash to release`.
That names the refusing file and what clears it, rather than describing the
session as idle over an empty queue.

It stops the moment the state clears, says nothing while a run is in flight, and
stays completely silent on an idle line with nothing a run could take and nothing
waiting on the forge — silence has to keep
meaning nothing to do, which is what makes the times it does not worth reading.

A [diverged target](operations.md#unwedging-a-target-branch-that-diverged-from-the-forge)
is said louder than the rest: at `warning`, tagged to the operators every time,
with the recovery steps named as the operator's move. It is the one of these
nothing but a person ends, and the only thing that said it before was the
`critical` blocker on the one item whose promotion first met it.

The provider's usage window is deliberately not one of the states it repeats. It
has its own message below, which opens with the cause rather than saying it after
the fact that choosing stopped, and two messages about one silence — one of them
wording it the way the operator asked not to be told — is worse than one.

The promotions are counted for the sake of the one message a reader cannot afford
to miss. A **merge the forge will not make** — refused while the run watched
because the remote target moved under it, or dropped after the forge had queued
it — is recorded as it happens and said as a `warning` in that item's thread, and
like every other crossing it is said once. Nothing else would ever mention it
again: the change is promoted, the item reads as landed, and the pull request
waits on somebody who does not know it is theirs. So the count of promotions the
forge has not published rides with the hourly line while any of them stands, and
it is why a line with nothing at all ready still says something.

Under that sentence it carries [the four lines](operations.md#where-the-harness-stands-the-four-lines)
— Running, Working, Not startable, Needs a human, with the fourth line's head
for each other mover under it, such as `Waiting on the development manager (30)` —
from the same derivation `yoyo status` prints them from, so the channel and the terminal answer one
question one way. It carries them counted rather than listed: nobody asked for
this message, it arrives again every hour the state stands, and an enumerated
queue under every line is a screen of detail in front of the one sentence that
says the line has stopped. The entries are what `yoyo status` prints for somebody
who typed it, and what @-mentioning the app is answered with — both of those are
asks.

Under the four lines it counts the [program managers that are
stale](operations.md#where-the-harness-stands-the-four-lines) — `Program managers
stale: 1 of 2 (factory-pgm)` — where any is. That count rides on this message
and on nothing else: a stale instance is something you review when you choose,
so it never causes a message of its own.

Before this the message said that choosing had stopped and
nothing whatever about what the machine was doing instead, which is exactly what
somebody woken by it at three in the morning then had to reconstruct. A sink
assembled without a way to read them says so in the message rather than leaving
them out: a message that simply lacked the lines is indistinguishable from a
harness with nothing in any of them.

Beside it is the **session's own age**, which is the same shape of message about
the opposite situation. A `yoyo work --watch` session runs whatever binary it was
started with, so a fix that lands after it started is not in it until that build
is installed over it — and nothing else says so, because the session goes on
choosing work and the runs it starts go on looking ordinary. What it actually
produces is rounds spent against defects that were fixed on the main line hours
earlier, which reads as agents failing rather than as a process running an old
build. So the session records the revision it was built from, and while the
repository has moved past it the channel says so every `--heartbeat`: how many
changes have landed since, which build it is on, and that installing the build is
all that is left — [the session takes it up itself](work.md#letting-the-harness-choose-the-work),
between the runs it is carrying and without interrupting one. Unlike the waiting
line it is said the first time it is seen rather than armed silently — nothing
else said it as it happened, and being told after the first round has been spent
is being told too late. It is silent on a session running what is deployed, and on one whose
binary recorded no revision at all, which is a comparison nobody can make rather
than a session that is current.

Two records answer which build that is, and they are asked in that order. The
watch log is the direct one: that process is the resident, and it stamps what it
was started with on every transition it writes. Where no live session names one,
the **runs still in flight** do — a run's record pins the harness that reserved
it, so a resident whose own log predates the stamping is still visible through the
work it is dispatching, and so is a dispatcher that is not a watch session at all.
Neither is inferred from anything else: both are stamps the process wrote about
itself.

A live session's stamp settles it outright, and the runs are consulted only where
no live session carries one — including where a run in flight was reserved by a
different binary and started later. That is a precedence rather than a contest of
which record is newer, because a live watch session is the resident by definition
while a run reserved by some other binary is usually an operator's `yoyo run` or a
triage carry-out: a process that has already ended or is about to, whose build is
not the one that will go on choosing work. Within each source the most recent is
taken — the newest live session that recorded a build, and the latest-started run
still in flight. A run that has ended says which build made it and is no longer
evidence about what is running now.

**It is a self-hosting line, and it says so rather than assuming it.** The
revision is the one the `yoyo` binary was built from, and the repository it is
counted against is the product's — the same one the tracker and the worktrees use.
Those are one history only where the product under management is the harness's own
source, which is how Yoyodyne develops itself and is not true of any other
product. Nothing asserts it: the sink asks the repository whether it holds that
revision before it counts anything, and where it does not there is no number to
say, so the channel hears nothing and the sink's log says why once per build. A
count taken from an unrelated history would be a number an operator would act on,
which is worse than the silence. Measuring a resident against a harness repository
that is not the product's would need that repository named in configuration, and
nothing names it today.

Far enough behind — twenty changes — it stops being a line worth reading and
becomes a degraded harness, said as a warning and sent as a direct message to each
person the project granted direct-work, once per build. That needs the `im:write`
scope the checked-in manifest asks for; a workspace that refuses it costs the
direct message and not the channel's copy. What is left for it to say is
narrower than it was: a watching session now restarts itself into a build
installed over it, so the ordinary case clears itself within one run and the line
is a transient rather than a standing chore. It still has the cases the session
cannot answer for — a build nobody has installed yet, a session started before it
could do this, and a dispatcher that is not a watch session at all — and it stays
until those have an owner too.

The third of these states is the one the other two structurally cannot see: **the
harness having stopped doing anything at all**. Everything above is read from
something a process wrote down about itself — a hold somebody placed, a session
saying it is idle, a build a session stamped — which works exactly as long as
that process is alive to write it. On 2026-09-01 a watch session died on a
transient tracker read at 06:05 and wrote nothing further: no stop, no idle poll,
no run, no hold. For seven and a half hours every surface here was correct and
silent, and the operator found it by noticing.

So something reads the absence. When nothing has started for ten minutes, the
tracker reports work ready, and no hold, no full machine, no still-moving run and
no provider usage window accounts for it, that is a stall: it is recorded durably
against the product, and each one is sent as a direct message to every person the
project granted direct-work and tagged to them by member id in the channel — and
sent again, louder, for as long as it stands. What it says is how long nothing
has happened, how much was waiting, the four lines, and — the fact that decides
what to do about it — what the thing that chooses work last said before it went
silent, because a session whose last word was `stopped` wants starting and one
still claiming to be watching wants killing first.

"Nothing has started" is measured from the last moment anything held a
developer slot: the later of the last run start and the last run end. The
message's "for one hour" counts from that moment. A batch of runs ending a few
seconds before the pull that refills the slots is therefore not a stall. Until
yoyodyne-ifd.428.19 the measure was the last start alone, and the watch read a
stall in exactly that gap every time a batch of long runs ended. On 2026-09-24
the last twenty alarms were all that false alarm. A line whose slots stay free
for the whole threshold is still one. An end the harness wrote while settling a
run whose process was already gone is not counted: that run held its slot only
until its record last moved.
[The operations guide](operations.md#when-nothing-happened-at-all) has the rest.

**The sink says it and does not notice it.** What notices is the harness's own
loop — [`yoyo work --watch`](work.md#letting-the-harness-choose-the-work), on
every pull and at most once per `--stall-after` — and
[`yoyo reconcile`](operations.md#recovering-interrupted-runs), which takes the
same reading on every sweep for the case the loop cannot see: itself being dead.
The product's supervisor runs that sweep on its
[maintenance pass](operations.md#the-supervisors-maintenance-pass), every
`services.maintenance.every`; scheduling `yoyo reconcile` yourself is needed
only where that part is off or no supervisor is running.
Both take the threshold under that name. That division is `yoyodyne-ifd.295` and
it exists because everything on this page is opt-in: reporting is an observation
and never a gate, so while the sink was the only thing taking this reading, a
product that never started one recorded no stalls at all and its
[`yoyo status` history](operations.md#when-nothing-happened-at-all) was
permanently empty — the instrument that exists so silence is impossible, absent
for exactly the installations least able to notice. The sink says where the
reading is taken every time it starts, so an installation that had it cannot lose
it quietly. `yoyo slack --stall-after` is still accepted so a launcher passing it
starts a sink, and it decides nothing; the sink says that too.

**Ten minutes is a default.** It used to be half an hour, and half an hour was
not what it cost: the tracker read this turns on was taken once an hour, so the
number an operator could set was not the number that decided when they were told,
and a harness that stopped could be quiet for ninety minutes before anybody
heard. That is what happened on 2026-09-05, and what the operator said about it
is that his bar is minutes rather than an hour. A watching session holds to that
number exactly, because it takes the reading itself as it polls. Where the
session is the thing that died, what decides the latency is the number and how
often the sweep runs — a sweep every half hour makes a ten-minute threshold mean
half an hour — which is why the two want setting together. Every legitimate
reason for a quiet line is named and read first,
which is what makes a margin measured in minutes safe: the two switches, a run in
flight and still moving, a product nobody watches, and the provider's own usage
window.

**It is deliberately not a model-based watcher, and nothing on this path may
become one.** Every watcher the harness has that asks a model something — the
development manager's sweep, any role turn — pauses with the provider's usage
window, so a watchdog built on one goes to sleep at exactly the moment the thing
it watches goes quiet. Both halves are plain Go reading durable files, and
neither makes a provider call directly or transitively. The other half of that
boundary is that noticing is all either of them does: restarting whatever died
belongs to the session's own exit and to the supervisor that starts it.

A run in flight quiets it only while that run is still moving, which is what
keeps a killed one from silencing it: a run whose process is gone leaves a record
saying it is in flight until `yoyo reconcile` settles it, and that is the crash
this exists to catch. A working run stamps every provider event onto its own
record, so a record that has not moved for an hour is what separates the two.

The provider's usage window quiets it too, and says so rather than only going
quiet. A run that comes back parked on an exhausted limit hands the watch session
the time the provider named, the session records that on entering the window —
one entry, not one per poll, since a run of unchanged polls is one account — and
the sink says one note in the channel instead of the alarm, at note severity and
to nobody's phone. The note opens with the cause and nothing else:

> Paused on the provider's usage window until 13:43Z. Nothing has been chosen on
> this product for 30 minutes; nothing has stopped and nothing is waiting on
> anybody, and the harness asks again when the window lifts.

That opening is the operator's own acceptance rather than a house style: when the
system is paused on a provider usage window, the cause is the first words of any
message that reaches him. It was bought the same way the rest of this was: on
2026-09-05 ninety minutes of a window read as a machine that had quietly stopped,
and somebody was paged for the provider behaving normally, with the cause left to
archaeology. It quiets the alarm
only until the time the provider named, for the reason a run's record does: a
session still choosing nothing after its own window lifted is a session that has
stopped working. A window the provider named no time for — the monthly overage
allowance reports that way — is bounded by the hour a session's last word stands
as evidence instead, which is the same hour a run's own record gets and
deliberately not the alarm bar: a session that died inside an untimed wait must
not go on accounting for every hour after it. `yoyo status` opens with the same
sentence while the window
stands, above [the four lines](operations.md#where-the-harness-stands-the-four-lines).

**It gets louder as it stands, not quieter.** It used to be said once per stall
and then nothing while the stall stood, on the reasoning that a stopped machine
is either acted on or it is not. On 2026-09-07 it was not: the alarm fired at
02:48Z, said nothing more by design, and a line fully stopped for over four
hours had produced one message, four hours old, by the time anybody read it. The
operator's direction inverted the design — a line that is completely stopped is
the most serious thing the harness can report — so the message is **said again
every `--heartbeat` while the stall stands**, to the operators directly and
tagged to them by member id every time; it is a **`warning` while the stall is
young and `critical` once nothing has started for two hours** — two heartbeats,
so the warning is said and said once more before it is raised — and each
repetition re-reads the cause and who it is waiting on, so a louder message is also a
more current one. Four silent hours replayed produce a rising sequence rather
than one message:

> :warning: Warning — Nothing at all has started on this product for 10 minutes …
> :warning: Warning — Nothing at all has started on this product for one hour …
> :rotating_light: Critical — Nothing at all has started on this product for 2 hours …
> :rotating_light: Critical — Nothing at all has started on this product for 3 hours …

The age is the stall's rather than the sink's, so a stall a sink first sees hours
old is critical from its first word, and a restarted sink over a standing stall
says it again rather than reading it past as history: the stall is the present
state of the line, and a sink silent over it would be silent over the one thing
it exists to say. A session and a sweep both reading the same standing stall
still open nothing, because one stall at a time is the record's rule. Each
reading checks for an open stall and records one under a single lock shared
across processes, so the two readers cannot both open a stall at the same
moment. The tracker
behind it is asked once per reading and only where nothing else already accounts
for the quiet — once per `--stall-after` in a watching session, once per sweep in
`yoyo reconcile` — so an idle product spawns no `bd` storm; the waiting line above
keeps its own hourly read and this surface spends none of its own on stalls. When
it clears the record closes, saying what accounted for it, and the channel hears
nothing — what cleared it said so itself, as the run that started. The whole history is
read back afterwards by
[`yoyo status`](operations.md#when-nothing-happened-at-all), which is the only
place it exists: a stall leaves no other trace, because the process that would
have left one is the process a stall means has died.

The brake's hold is the same shape one layer over, and is said the same way.
A hold [the brake placed](operations.md#pausing-everything-and-resuming-it) asks a
person for nothing while the development manager is deciding about it or a
probe is running under it, and the hourly line says so at note severity, naming
her move and where the summons-and-probe loop stands — which cycle it is, and
at what cycle the harness stops asking — so a note repeated through a night
says how much longer the loop goes on. Once it waits on the operator — she
escalated it, the harness escalated it at that bound, or it was written before
the brake summoned anybody, which is the hold that stood for two hours on
2026-09-19 with a free slot idle — the hourly line is **tagged to the
operators every time it is said, a `warning` while it is young, and `critical`
and sent to them directly once it has stood two hours**, until intake is
released. The operator's own intake hold is a state they chose to sit with and
stays the hourly note it was.

### A brake hold the harness escalates

The one message about a brake hold that asks a person for something. On a
machine that stays broken the brake's loop goes round — each blocked probe
summons her again and restarts the cooldown — and nothing about it got louder
unless she escalated it. After
[`execution.brake_escalation_cycles`](configuration.md#watching-instead-of-draining)
of those cycles with no escalation of hers, the harness escalates the hold to
the operators itself and says so **once, the moment the record shows it, sent
to them directly and tagged to them by member id**, at `warning` severity:

> :warning: Warning — The brake's hold on intake is escalated to the operator
> by the harness: the harness's own brake placed it after 3 run(s) blocked in a
> row with nothing landing between them, which is the configured brake at 3,
> and the harness escalated it to the operator after 4 summons-and-probe cycles
> with the development manager not escalating it (the last probe run, of
> yoyodyne-ifd.405 (Every yoyo verb runs from inside a harness-managed
> worktree), blocked: the checks failed on main), so it stays held until
> somebody releases it. Next: the operator's — the harness has stopped probing,
> and nothing new is chosen until `yoyo release` lifts it.

It is never said again on a later pass: the hourly line above carries the hold
from there, tagged as any hold that waits on a person is, and the release says
the hold lifted. `yoyo status` reads the same record and names the hold on its
"Needs a human" line as the operator's by the harness's escalation.

### The provider holding every role

The window above is a session's own account of a limit it met, and it is only
written when the session choosing work is the thing refused. Between
2026-09-08 and 09-13 it was not: the session was idle over items waiting on a
decision, the role that would have decided was refused twenty times a day, all
five agents were on the one model being refused, and no agent named an
alternate. Each of the 134 refusals was said once in the channel as itself, at
warning severity, and nothing said what they added up to. The operator heard
five days later, from his assistant.

So the sink reads the refusals against the configuration and says the sum. The
refusals are in two records, and it reads both: the usage-limit log, which holds
every refusal met outside a run — a conversation turn, an exchange, a side
thread, a standalone review — and the runs' own records, where a run parked on
a limit writes its deadline and nothing in the log says so. A product with no
recurring task and no open conversation, whose developer runs are all asleep on
the reset, is held exactly as September was, and the log alone would never say
it. When the refusals the provider has not said lifts yet cover the model every
agent's turn ends on — its alternate where it names one, its own model
otherwise — and at least one of them stopped a turn or parked a run rather than
being served through, that is **the provider holding every role**, and it is
said as a state:

> Every role is paused on the provider's usage window until
> 2026-09-13T03:00:00Z: all 5 agents run on opus and none names an alternate,
> so nothing fails over; 134 turns refused since 2026-09-08T07:38:40Z. Nothing
> has moved on this product for 26 hours: every turn every role would take asks
> a model the provider is refusing, and nothing any of them may fail over to is
> being served either. The window lifts on the provider's clock; failover on
> the agents is what would move the work before it does.
>
> Next: the operator's — the window lifts on the provider's clock, and enabling
> failover on the agents is what would move the work onto another model before
> it does.

It is shaped the opposite way from the window on purpose. The window is the
provider's ordinary behaviour, said once as a note and to nobody's phone. This
is the harness stopped by something a person can change, so it **reaches the
operators directly the first time it is seen** — the first pass after the
window closes, minutes rather than days — and is **said again in the channel
every heartbeat it stands**, at warning severity while it is young. Once it has
stood for six hours — the longest a run itself will wait out a limit, which is
where `execution.usage_limit_max_pause` ships and for the same reason: a
capacity problem that has outlasted every timer needs a person — it is said as
**critical and taken to the operators again with every repetition**. A hold
nothing but a person ends early is the one state where getting quieter as it
stands is the wrong shape. The stall alarm above escalates the same way on a
shorter bar; this is the capacity half, and a line stopped on a known reset is
a different message from a line stopped for reasons nobody can name.

The hold is marked by the reset the provider named, so the same window is one
thing to say and a later one is another; the sweep adding a refusal an hour, or
a parked run probing every half hour, does not restart the clock. It lifts at
the reset — or, for a refusal the provider named no reset for, after
`execution.usage_limit_unknown_reset_pause` with nothing recorded since, which
is the same reading failover takes of the same log — and nothing is said about
that: the turn that is served says it. It lifts sooner on evidence: a turn or
a run the provider served on the same account and model after a refusal was
recorded reads that refusal as lifted, whatever reset it quoted, and a
refusal of a conversation its role has since replaced holds nobody — the
same reading [`yoyo status`](operations.md#where-the-harness-stands-the-four-lines)
and the dashboard take, so the channel never says a hold the terminal has
cleared. A parked run is read on the same rule:
it stands until the reset it is parked on where the provider named that reset,
and for the probe interval from when it parked where the deadline it recorded
is the harness's own next probe, which is never said as a time the provider
named. The hold's age, which is what decides when it becomes critical, runs
from the earliest standing refusal — for a run, from when it parked rather
than from its latest probe, which its record keeps for exactly this reason.
The sentence counts the two records as what they are: turns the log refused,
and runs parked on the limit, a run being one refusal however many probes it
has made. A refusal that names no model, which is every one written before the
model was recorded, counts only where every agent asks for the same thing, and
then only as a refusal of the model they ask for first — never of an
alternate, which is asked only after that model has refused and which a record
naming no model cannot have asked. On a project whose agents differ it cannot
be attributed, and a hold invented over it would send somebody to look at
roles that are being served; on a project that enabled failover after such
refusals were written, the unexpired ones hold nobody while the alternate is
being served, rather than reading as the provider refusing both. A run's park
records the model it was refused on; one recorded before it did is read as a
refusal of the developer's model where the run was developing, which its record
carries already, and as unnamed otherwise. Failover working is the
opposite of a hold: a window that closed and was served through by an
alternate stopped nothing, however many times it is recorded. The condition
that makes a hold possible at all — every agent on one model with nothing to
fail over to — is what [`yoyo doctor`](operations.md#checking-the-installation)
names under `failover` before any window closes.

### A provider nobody can reach

The two states above have a reset. This one has none. From 2026-09-17 18:17
local the Claude Code login on the operator's machine had expired: every
dispatch was refused, every recurring pass recorded 0 turns, the runs already
going spent their relaunch budgets and blocked, and the intake brake tripped
over three of them — which was the one thing that did reach the channel, and it
prescribed `yoyo release`, which lifts nothing here. The operator learned what
had happened by asking, three days later.

So the sink reads the product's own record of the provider answering nobody —
written by whatever met it refusing, a dispatch, a run, or a conversation turn,
and cleared by the first thing it serves again — and says it **once, the moment
it is seen, tagged to the operators by member id**. It is both important and
theirs to act on, which is the communication rule's own test for a tag: a login
is nobody else's to renew.

> @operator The provider is not authenticated; the operator must log in: every
> role is waiting on it, and the harness asks again on its own until it
> answers; 3 turns refused since 2026-09-17T15:17:00Z (claude-code, account
> default). Every run in flight is waiting on it with its claim, its branch,
> and its worktree kept, no relaunch or repair attempt is being spent, and the
> intake brake is not tripping on it. The harness asks again on its own;
> nothing to release, nothing to restart.
>
> Next: the operator's — log in to the provider, or wait for the network; the
> harness resumes on its own once it answers, and nothing is released or
> restarted.

The other cause reads *The provider cannot be reached*, and the move is the
network's. It is shaped the opposite way from the hold above on purpose: the
hold is repeated every heartbeat because a person ends it early only by
changing the configuration and needs reminding, and this is **not said again
while it stands** — the four lines carry it as their banner, the stall alarm
does not fire over it, and what a repeated message would buy is a reason to
mute the channel. When the provider answers again the operators are told **once
more, as a note**, because they were told the line had stopped and are owed
being told it carried on by itself:

> The provider is answering again after 3 days: a provider that is not
> authenticated; the operator must log in. Every run that was waiting has
> resumed where it stopped, and the queue is being pulled from again. Nothing
> was released and nothing was restarted.

What the wait does to the runs, the scheduler, the brake, and the recurring
tasks is in [operations](operations.md#waiting-out-a-provider-nobody-can-reach).

### A recurring task failing before its first turn

A provider nobody can reach ends when it answers. This does not end by waiting.
From 06:39Z on 2026-09-26 every development manager sweep was refused before its
first turn — the harness held the message it had composed for the pass to the
bound on what a person may type — six times in a row, and every triage decision
the sweeps would have made waited a day. Nothing reached the channel; the only
account was a line per firing in the sweep log.

So a recurring task whose firings fail before their first turn twice in a row —
a message the harness refused, a conversation that would not open, a turn that
would not assemble — is said here from the same derivation `yoyo status` lists
it from, [on the attention line](operations.md#reading-what-the-recurring-tasks-found):
**once as a `warning`** when it becomes an entry, and **once more as
`critical`**, sent to the operators directly as well, once it has stood two
hours. It is not repeated beyond that, since the attention line carries it
while it stands. The first firing that takes a turn clears it, and a later run
of failures is said as a new one.

> The recurring task development-manager-sweep has failed before its first turn
> 2 times in a row since 2026-09-26T06:39:00Z: the harness refused the message
> it composed for the pass; latest: scheduled pass's message is 47768 bytes,
> limit is 32768 … Each of those is a failed firing rather than a partial pass:
> nothing was asked of the role and nothing was spent, and the next firing meets
> the same refusal until its cause is fixed. It has stood for 1h0m.
>
> Next: the harness's — the harness refuses what it composed for the pass, which
> is a defect in the harness rather than anything waiting it out will end; every
> firing meets the same refusal until the harness is fixed, and the first firing
> that takes a turn clears this

### An item claimed with nothing working on it

The stall reading has a blind spot, and neither state above can see it either:
**an item the tracker calls in progress with nothing working on it**. A stall is
derived from ready work going unstarted, and an item the harness claimed is not
ready — so a machine whose only startable work is sitting under dead claims reads
as a drained queue, and the stall is silent about it by construction. Four
nights of throughput went that way in the week of 2026-09-01.

The audit is the watch loop's rather than the sink's, because giving a claim back
is directing work and this surface does not direct work. What arrives here is
what it did:
[the harness gives the claim back and ends the record of the run that left it](operations.md#claims-with-nothing-working-on-them),
so the item is pullable again and the developer slot is free, and the sink says
each release once — in the item's own thread, shown at the top of the channel as
well, and as a direct message to whoever the project granted `direct-work`. Once is the
log's position rather than this process's memory, so a restarted sink re-says
nothing; and it is never repeated, because what follows a release is the item
being pulled again, which says so itself.

### A finding that needs your hand

Some of what the roles find is yours alone to act on: a hook that has to go into
`.claude/settings.json`, a credential to renew, a workspace setting, a file the
harness may not write. Six developer reports of that class sat in the pile from
2026-08-17 until the sweep of 2026-09-14 reached them, and the finding that
sweep produced landed on the Lead Product Manager's own checklist, which you saw
when you asked — a month after the first was filed.

So the class is one the harness reads, and **each such finding is said to you
once, directly, tagged by member id** — the communication rule's own test for a
tag, since it is both important and yours — the pass after it is recorded.
[Operations](operations.md#where-a-finding-that-needs-your-hand-goes) says what
makes one: the Lead Product Manager handling a report with `"needs":
"operator"`, a report filed at critical severity that nobody has handled, a
stopped run the development manager escalated to you, and the batch of
recommendations an owning role argued on a recurring pass over the changes
proposed to its documents — one message per pass, listing each proposal with
what its owner recommends and why, for you to decide with `yoyo amendment`. The message says what is
needed, who found it, where it is recorded, and what ends it, so you can go and
read the whole of it:

> @operator This needs your hand: add the PreToolUse hook to
> `.claude/settings.json`; the harness may not write that file. Found by the
> Lead Product Manager, handling the report; recorded in the handling of
> report-9f2c… recorded in chat-91253e0e…, over the developer's report from
> run-4f2a…. Nothing here changes it, and this is not said again —
> `yoyo status` names it until it is done, and a later handling of the report
> records it done. Next: the operator's — only a person can act on this; a later
> handling of the report records it done.

It is said once and never again while it stands. The sink marks each finding by
name in its own durable cursors, so a second pass, a restarted sink, and every
poll afterwards send nothing more; `yoyo status` names it under `Needs a human`
until it ends, which is the record that says it is done — the report handled,
the change recorded made, the escalated run decided again or its escalation
ended, every proposal in a batch decided — and the mark is
dropped with it, so the same report handled as yours again later is a second
finding, said once more. An escalated stoppage is read here from the runs and
the triage record alone, without asking the tracker whether the item is still
admitted or parked, or the repository whether the run's change is still there.
So an escalation ended by one of those keeps its mark until the next `yoyo
reconcile` records the ending on the run, and the mark is dropped then. It was
said once either way, and `yoyo status` reads the queue and the repository and
drops it at once. One that ended before the channel said it is not said at all
once the sweep has recorded the ending. A
finding whose report was filed before this channel was turned on is history
like every other record from before the watermark, and is marked without being
said; its moment is the record that made it, so a handling made today of a
month-old report is today's news.

The brake's trip is sent to you the same way — directly, tagged, once, the
moment it is recorded — naming what a count did not: each run it counted, with
its item and what stopped it, and the verb that lifts it. It is not yours to
move while the development manager and the harness are working it, and its
closing clause says whose it is; it is the one hold you did not place, so you
are told it happened:

> @operator Intake is held for this product: the harness's own brake placed it
> after 3 run(s) blocked in a row with nothing landing between them, which is
> the configured brake at 3, and the development manager was summoned at … to
> decide what happens to it …; the runs it counted: run run-7c27… of
> yoyodyne-ifd.398 (A draining watch session keeps pulling and running
> recurring tasks until it restarts, …): its reviewer still required repair …;
> run run-a17c… of yoyodyne-ifd.401 (A check stage cannot take hours: …):
> check `make test` failed (exit 1) …; run run-5035… of yoyodyne-ifd.402 (A
> docket entry names the open publication and how to arm it, …): ….
> `yoyo release`, or `/release` in the conversation,
> lifts it sooner. Next: the development manager's — …

When she escalates it to you, that hold is said to you once more, directly and
tagged, in its own account of who decided it; the harness escalating it at the
bound on its loop is [said the same way](#a-brake-hold-the-harness-escalates).
While a hold stands the [heartbeat](#reporting-into-slack) says intake is held
every hour, runs in flight or not: a held intake is the one state said over a
run, because the runs are the ones that were already going when the line
stopped, and a free slot idle beside one is exactly the shape the 2026-09-19
trip stood in unseen. The release is said once, naming who lifted it —
`released by the operator, at a terminal (`yoyo release`)`, the conversation and
turn, or the harness and what moved it — read from the release the store
records beside the hold's absence. Your own hold is said to the channel alone,
because you placed it.

### What arrives as a direct message

Almost everything above is posted in the channel and nowhere else, because a
channel is somewhere somebody chooses to look and most of what the harness says
can wait until they do. A direct message is the exception, and it is admitted
only by naming its class — the
[slack-reporting design](designs/slack-reporting-design.md) holds the rule, and a
state fitting neither class does not get one:

- **Degraded** — the system is stopped, stale, or choosing nothing over ready
  work: something only a person fixes. The shipped states are the ones
  above — a session running a build the harness has moved well past, the
  harness having started nothing at all while work was ready, the provider
  holding every role with nothing configured to fail over to, the brake's own
  hold handed to them by the harness at the bound on its summons-and-probe
  loop, a recurring task that has failed before its first turn for two hours
  (its critical message), an item that
  sat claimed with nothing working on it until the harness gave it back, the
  brake having held intake (its trip, once), a brake hold the development
  manager escalated to you, and **the line choosing
  nothing over ready work**, which is the one state that is
  asked rather than reported and is below. The released claim is a fix rather
  than a request, and it is still in this class:
  the line was quietly degraded for as long as it stood, and a second run for an
  item with nothing accounting for the first is the kind of thing a person has
  to be able to read afterwards.
- **Advisory-once** — a fact addressed to a person that speaks exactly once per
  fact, never repeated and never urgent in presentation. Two states ship in it:
  **a value the project's template has improved that this project never
  edited**, and [**a finding that needs your hand**](#a-finding-that-needs-your-hand),
  which is urgent in what it says and is still said exactly once, because
  `yoyo status` carries it from then on.

The stopped line is the hourly state above, put to you personally. At the same
moment the channel first says the line has stopped over ready work, the sink
opens a direct message with each person the project granted `direct-work` with
a bound Slack member id — the same people whose thread replies steer the work,
one conversation each — with a brief top line carrying the ask, and the context
with the answers numbered threaded under it. A line said for a promotion
waiting on the forge alone, with nothing ready to pull, is the channel's to
carry and asks nobody. **The reply in that thread
is the decision**: a number takes the option it names, anything else is recorded
in your own words, and either way it lands as one unscoped operational directive
in the record every run consults, carrying what was asked, what you chose, and
what you said. Nothing is carried out on its own — deciding to release intake
records that you decided to, and the switch stays yours. Each person is asked
once per state, because a direct message repeated hourly is what gets an app
muted, and the channel is where the repetition belongs. A reply typed in the
conversation rather than in the ask's thread — which is what tapping a phone
notification opens — records nothing and is told so, and pointed at the ask
where it is still the live one; where it has since cleared it is told that
instead, so nobody is sent to decide about a line that is already moving; and
where the sink could not read which state stands it says so rather than
claiming either. A message there that @-mentions the app is a question, and is
answered as one.
[`docs/slack/setup.md`](slack/setup.md#deciding-a-stopped-line-from-a-direct-message)
has the whole of it.

The last of those is the comparison
[`yoyo config drift`](configuration.md#extending-a-built-in-bundle)
prints and `doctor` and `config validate` say as an aside — every one of which is
a command somebody has to run. A harness left running for a fortnight runs none
of them, so a fix the template has since made to a persona sits unheard while the
project goes on without it. So the sink says the same thing: **one direct message
per reading that finds something new**. A reading that finds one improvement
names the setting, what this project holds, and what the template supplies now;
a reading that finds several — the first one on a project many template
revisions behind — says them together, how many and the first few by name, with
`yoyo config drift` holding what each one was and is. That is the bound on the
class: an operator joining a project a dozen revisions behind is sent one message
they can read in a sitting, not a dozen. Nothing is adopted for you and nothing
is waiting on you — the message says the move is nobody's, and it is an offer a
project is entitled to decline forever.

Once means once. Each improvement is marked in the sink's own durable cursors as
it is sent — one mark per value, whether it was said alone or among several — so
a restarted sink, a second sink, and every poll for the rest of the project's
life stay silent about it, and a later reading that finds one more says that one
alone. The mark names the value the template
supplies rather than the setting alone, so a template that improves one setting
again later is a second improvement and is said again; and a mark is dropped once
its improvement is no longer offered — adopted, edited, or superseded — which
keeps the record a statement of what stands rather than a list of everything ever
said. The comparison itself is read once per `--heartbeat` rather than once per
poll, so a sink polling every fifteen seconds reads the configuration hourly and
not four times a minute.

All of them need the `im:write` scope the checked-in manifest asks for, and the
ask needs `im:history` and the `message.im` event beside it to read the reply.
A workspace that refuses them costs the direct messages and nothing else: the
stale build, the provider's hold, the improvement, the released claim, each
finding, and the stopped line are in the channel either way, and the stall is in the durable
record `yoyo status` reads back.

Every message ends by saying who it waits on next. A thread is a narrative and a
narrative goes quiet — a run takes an hour, an item sits in the queue overnight,
work routed to a role waits on somebody opening a conversation — and the silence
after the last message reads the same whether somebody is working, somebody is
waiting to be asked, or nobody at all holds the ball. So each message closes on
one clause: `Next: the reviewer's — a verdict on the change.` It is the same
clause whoever is speaking, because who a promotion waits on next is a fact about
the state of the work rather than an opinion a persona has about it, and it is on
every message rather than only the ones that look final — which message turns out
to be a thread's last is not knowable when it is written.

The work that most needed it is the work no run ever touches. An item
[marked for a conversation](work.md#letting-the-harness-choose-the-work) is never
selected, so no run reports anything about it, and its thread used to show the run
that could not carry it and then nothing for the rest of the item's life. Three
messages carry that journey now: the item being handed to a role's conversation,
that role's first act on it, and the close that finishes it. The handoff names
which role's conversation, taken from the marker on the item, so the wait between
being handed over and being taken up belongs to somebody by name rather than to
the anonymous role that carries it. Work marked before the marker named a role
still says only that a conversation carries it, because that is all its record
holds and a thread that guessed would name the wrong person. The close is reported
here and nowhere else, because everywhere else the run that landed the work
already says it, and this is the work with no run to say anything.

[`docs/slack/setup.md`](slack/setup.md) takes you from an empty workspace to
live reporting, and the app it asks you to create is the checked-in manifest
beside it rather than a list of checkboxes to work through by hand.

It is an observation and never a gate. Nothing waits on it: a workspace that is
down delays messages rather than losing them, because the sink reads the same
durable records the verbs above read and catches up from its own cursors when it
returns. The moment its history starts from is written down the first time you
ever run it and never taken again, so time the sink itself spent stopped is a gap
it reads across rather than a gap in what it says. It is also half the reason no
run holds a Slack token — one separate process posts, and the harness builds
every run's environment from an allowlist rather than handing down its own, so
no agent's subprocess tree ever has a credential for your workspace in it, even
where the pair was exported in the shell that started the harness.

Replies go the other way, and what a reply is for is read before anything is
written down. An instruction in a work item's thread, from somebody this project
granted `direct-work` with a bound Slack member id, is recorded as a
[directive](conversation.md#directives-and-the-work-they-pause) against that item
— the same record `yoyo directive record` writes, with the same pause semantics
and the same resolution, so a run meets it whichever way it arrived. A question
in the same thread — one ending with a question mark — is recorded as nothing
and carried to the Lead Product Manager instead, in the same durable conversation
`yoyo chat` holds; the thread gets a one-line receipt saying it was heard as a
question, and then the Lead Product Manager's answer, in the Lead Product Manager's own
name. A reply that is neither outright is asked back in one line rather than
guessed at. The record used to take everything, and on 2026-08-30 it took the
operator's question about a phrase in a receipt as a standing instruction and
acknowledged it with the same phrase: a question in the directive record is a
directive nobody gave, and `yoyo directive list` marks any entry that still
applies and reads as one, so the ones recorded before this can be withdrawn. Every reply is
answered in its own thread, tagging whoever wrote it, with what was recorded or
why nothing was; the reply itself is marked with where its directive stands,
recorded and open or settled; and when the record later says the directive was
settled, that is said in the same thread, tagged the same way, and the mark on
the reply moves with it. A project that has granted nobody is steered by nobody.
What a reply may say is in
[`docs/slack/setup.md`](slack/setup.md#steering-the-work-from-a-thread).

Outside those threads the sink is silent, with two exceptions. The first is a
direct message it opened to ask you something: a reply in that thread is the
decision and is acted on and answered there, and a message typed at the top of
that conversation is answered saying nothing was recorded — see
[what arrives as a direct message](#what-arrives-as-a-direct-message) above.
The second is that **a message that @-mentions the app is always answered**,
wherever it can see one — at the top of the channel, in a thread it never
opened, or in a direct message with the app outside the thread of an ask, where
it is answered as a question rather than as a decision typed in the wrong place. A question about where things stand
is answered with the same four lines `yoyo status` prints, read from the same
place rather than assembled a second way. Everything else reaches **the Lead Product
Manager**, in the [same durable conversation](conversation.md) `yoyo chat` holds
rather than in one this channel keeps of its own: a conversation begun at a
terminal carries on in Slack and back, because both are clients of one record.
The answer comes back in the Lead Product Manager's own name, in the thread it was
asked in, and it is held to the same `direct-work` grant a thread reply is —
talking to the Lead Product Manager admits work and spends money, while asking where
things stand tells a reader nothing the channel was not already telling them.

The wait for it is bounded and the failures are said rather than swallowed: a
turn gets ten minutes, and the Lead Product Manager being mid-turn with another
client, the wait running out, and a provider out of capacity are each answered
in the thread with what happened. A turn the channel stopped waiting for holds
the conversation until it lands, and the thread says so: `yoyo chat` queues
behind it and continues once it has landed, rather than showing a turn that is
still being written. No directive is recorded from a mention, because a
message at the top of the channel names no item to scope one to — what a mention
changes about the work it changes by speaking to the Lead Product Manager, which is
the Lead Product Manager's own doing and reaches this channel through the ordinary
reporting of its record. Commands are not carried out from here either: a
message that opens with a slash is refused with where to type it, so it costs no
turn. And every message addressed to the app goes into the sink's own log, with
what was asked in it and before the answer is posted, so being heard does not
depend on the workspace carrying the answer. It exists because the alternative
was silence, and a reporting process that answers nothing is indistinguishable
from one that has died.
