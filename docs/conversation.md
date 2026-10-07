# The conversation

*For an operator driving work from `yoyo chat`. Part of
[yoyo's documentation](../README.md#further-reading).*

The Lead Product Manager reads the product specifications — every Markdown file under
`product.specifications`, which defaults to `docs/product` — plus the open Beads
items, and discusses product intent with you. It owns the queue that serves that
intent, and it manages it directly rather than dictating changes for you to
type.

```sh
yoyo chat
yoyo chat --message "What is missing from the brief?" --json
```

That queue is a backlog with an order, and the order is the Lead Product Manager's.
What is admitted to it and what comes before what are product decisions — the
same intent it already owns, expressed as what to do next — while decomposition,
dependencies, and assignment stay a development manager's, which takes work from
the top of that order rather than choosing for itself. A role that disagrees
with the ordering proposes a change to it exactly as it would propose a change
to a goal; none of them reorders it or admits work to it. The order is written
down as Beads priority — 0 first, 4 last — so there is one place it lives rather
than a second copy that could disagree with the tracker, and items left at the
same priority are in no order anybody decided: the harness takes them
oldest-admitted first, so an item already waiting is not passed over for one
admitted after it while it can start — one that is held, unready, or waiting for
a slot that prefers another label still is — and the Lead Product Manager says
which comes first by giving it a higher one. Admitting work says where it goes as part
of admitting it, because a new item has no identifier until the tracker answers
and an ordering left for a later step is an item sitting at the tracker's
default in the meantime — including for an item you approved from a proposal,
which is admitted at that default until the Lead Product Manager places it.
`/backlog` shows it to you in order, with what is holding each unready item back
and what would be pulled next.

The harness also pulls from that order on its own, and the difference is worth
stating plainly: `/work` is you naming an item, while `yoyo work` reads the same
order, takes what the tracker calls ready, and starts as much of it at once as
your configured capacity leaves free. `/hold` is what stops it choosing while
what is already running finishes, and every run it chooses records why that item
was the one — so work that started without you asking still says what it was
doing there. [Letting the harness choose the
work](work.md#letting-the-harness-choose-the-work) is the whole of it.

Work leaves the backlog in one of two ways, and both are recorded on the item.
`close` says the work is done. `retire` says it will not be done, and is the
only way admitted work leaves the queue without being done: there is no delete
and no third way, so scope you asked for cannot quietly disappear from the
queue — it is withdrawn in the open, with a reason, and the item afterwards says
it was retired rather than finished. Work you still want and do not want started
does neither: it is **parked**, which stops it being selected without taking it
out of the backlog, and is described below.

A specification opens with an introduction saying what the thing is and why it
exists, and states the goals that serve it after that introduction. That shape
is the contract, and the harness checks it: one that has no goals, no
introduction before them, or an empty goals section is named on stderr when the
conversation opens and listed for the Lead Product Manager alongside the
specifications themselves — and still read, because refusing to load it would
silently lose intent somebody wrote down. A directory index and a non-goals
document are not held to that shape, because neither states goals: the index is
held to none, and the non-goals document to its own, an introduction and then a
`Non-goals` heading with something under it. A directory with nothing in it is
reported the same way rather than treated as a product with no intent.

The context also says outright what those specifications record of the two
documents intent is written in — the brief saying what the product is and who it
is for, and the goals that serve it — naming each with roughly how much prose it
carries, and calling one that carries almost none a placeholder. That is what
makes the first conversation on a fresh repository the intended opening move
rather than a confusing silence: with no brief and no goals to read, the Lead Product
Manager says so, offers to draft both from your answers, and starts asking what
only you can answer — what this is, who it is for, what finished looks like. It
asks them one at a time. The opening reply says there are three, says which order
they come in and why that order, and then asks the first, because a paragraph
holding three questions gets one answer and loses the other two. It is a question
and not a gate. Nothing is blocked, nothing is written, later means later, and a
repository whose goals are already written gets no such prompt. The asking is
persona guidance, so a project that wants a different opening
[replaces it](configuration.md#personas) like any other part of the
persona.

One more section sits below those, and it is a different kind of thing: **what
the product ships today**, which is the README, the six operator documents the
README split put beside it — this one, [how work flows](work.md),
[what comes back to you](reporting.md),
[artifacts, goals, and invariants](artifacts.md),
[operations and recovery](operations.md), and
[working on yoyo itself](developing-yoyo.md) — the configuration guide, and
the help every command prints. It is labeled as exactly that — a description of
the implementation as built, never authority about what the product is for — so
that the role deciding what to build next can say which surfaces already exist
without you having to tell it. Where that description and a specification
disagree, the Lead Product Manager reports the conflict rather than settling it.

Not the source, not the design document, and no way to run a command: those say
how the product is built rather than what it is for or what it ships. Nor
everything [Further reading](../README.md#further-reading) reaches, which is
more than these eight — the set is named one document at a time, so adding one
to that index does not thereby show it to the Lead Product Manager. The eight are
named, and the narrowing this partially undoes is described with what it bought
and what it cost, in the
[configuration guide](configuration.md#what-the-lead-product-manager-sees-besides-them-and-what-it-does-not).

It has no tools: no filesystem, no commands, no network. What it has instead are
capabilities the harness performs on its behalf — the tracker below, [a read of
one repository path at a recorded commit](#reading-the-repository-at-a-recorded-commit),
and [research](#bringing-it-an-idea-rather-than-a-work-item) where you have
configured a source. The first is the work tracker,
through a fixed set of named operations the harness carries
out for it — read an item in full, survey the open queue, create, attribute to a
goal, update, label and unlabel, reparent, reprioritize, park and unpark, link
and unlink a dependency, repair state the records have made stale, close, and
retire. Three further operations are about none of that: `handle` records
what became of a report another role filed, which is how the pile it is shown
[stops being asked about](reporting.md#who-reads-them-and-what-became-of-each-one),
`withdraw` takes back a proposal of the Lead Product Manager's own that you
have not decided yet ([below](#proposals-and-deciding-them-in-batches)), and
`directive` ends a directive you recorded, by resolving it into what now carries
it or withdrawing one that directs nothing
([below](#directives-and-the-work-they-pause)). Every
argument is validated before anything runs, at most ten actions happen per reply,
each one is recorded in the conversation's log as asked-for and then as applied
or failed, and all of them are printed to you as they happen. An action that
failed is reported as failed rather than described as done, and a block the
harness cannot read changes nothing at all. Some actions are more than one write,
and one that failed after part of it landed — or that failed without anything
being able to say whether it landed — is reported as neither: it says it did not
finish, and lists what landed and what nothing can say either way. Only "failed"
means the action changed nothing, so an action reported as unfinished is never
one to simply ask for again — see
[deciding what becomes of stopped work](#deciding-what-becomes-of-stopped-work),
where the spend an action makes before it writes is the case this exists for. The distinction being drawn is
deliberate: arbitrary execution is what was refused, and a typed call against the
tracker is not that.

**A block the harness refuses is handed straight back, inside the same
message.** A refused block is refused whole, so nothing in it happens: one title
over 200 bytes loses all ten actions beside it. The refusal is a result like any
other, so it comes back to the role as a further round of the message you sent,
in the harness's own words and saying how many rounds of tracker actions the
message has left, and the role reissues the corrected block before its reply
ends. You are shown the refusal and then what the reissue did, and nobody has to
relay anything. In the week to 2026-09-25 that relay was done by hand four times,
because a refusal used to wait for the role's next turn. A refused round counts
against the message's four rounds. Anything else the refused reply asked for —
a proposal, a question, a read, a memory — was not taken up either, and the role
is told so.

A block refused again on the round it was handed back in goes to you as
critical, and so does a round that answers without asking for any tracker
action. Both leave the actions lost, and a second copy of the same message would
not fix them. A refusal on a message's last round has no round left to come back
in. It opens the role's next turn instead, and the harness
[wakes the role for it](work.md#letting-the-harness-choose-the-work) at the next
pull. The same happens when the round it was handed back in never came back:
the provider failed it, or the process ended first.

A tracker call that fails in a way a later attempt could survive — a `bd` killed
at its timeout, a contended store — is waited out and asked again, under the
rule and the window of
[waiting out a network that dropped](operations.md#waiting-out-a-network-that-dropped).
**A write that is not safe to repeat is checked before it is asked again.**
Reads are asked again as they stand. So are linking and unlinking a dependency
and an update that appends no note, because each leaves the tracker as the
first attempt did. A write that adds something is checked for having landed
first, because a `bd` killed at its timeout may already have written. A creation
is checked by listing the tracker for the item it would have made under its
parent. An update, a block, and the clearing of a blocked status are checked by
reading the item for the note they were appending at the end of its notes. A
close is checked by reading the item for being closed. A write the check finds is
reported as applied and not made a second time, and a write whose check cannot
be read is not asked again at all. That is the difference between one copy of a
note and two, and on 2026-09-24 between one admitted item and
yoyodyne-ifd.428.22 beside 428.21.

**One thing a creation is refused for is being work already in the tracker.**
Before an item is created the harness compares it against every item the tracker
holds, closed work included, and looks for two things: work already admitted from
the report or the directive this creation cites, and a child already carved out
of the parent it names carrying the same scope. Either one refuses the creation
and names the item, its state, and why it matched, in the result the role reads
and the line printed to you.

That is narrow on purpose. The source question is exact — a report or a directive
is an identifier, and the item that came from one says so in its own record — and
the scope question is a judgement, so it is asked only among the children of one
parent, which is where a decomposition repeats itself. Nothing compares two
unrelated items by how alike they read: a backlog shares most of its vocabulary
with itself, and a guard that fired on a quarter of admissions would be one
nobody reads. One record can genuinely prompt more than one piece of work — a
directive of yours routinely does — so the refusal for a source says what to do
about that too: the citation belongs on the one item that answers the record, and
the second is admitted without it.

**A match against closed work can be named as separate work.** A closed item can
match work that is genuinely different, and the ordinary case is a design: the
architect's design item closes when the design is recorded, and the item that
builds it hangs under the same parent with most of the same title. On 2026-09-28
the build of the configurable state root was refused against
yoyodyne-ifd.434.2, the closed design it was built from; the refusal told the
Lead Product Manager to propose it instead, and proposal 959.1 then waited on you
for a decision her own authority covered. So a creation can carry
`distinct_from`, naming the closed item and saying in one sentence what is
separate:

```text
{"action":"create","title":"…","description":"…","goal":"…","parent":"yoyodyne-ifd.434",
 "distinct_from":{"id":"yoyodyne-ifd.434.2","separate":"434.2 recorded the design; this builds it."},
 "reason":"…"}
```

The harness sets that one match aside and writes the item and the sentence onto
the new item's notes — whether the check had matched it, and why — so whoever
reads the item later reads why it was admitted beside the one it resembles.
Nothing is put to you. The field is refused where the item it names is open or
not in the tracker, and where the sentence is missing or runs past one line; it
sets aside only the item it names, so any other match still refuses the
creation. The proposal is what is left for a match against open work: the
refusal names it only there, because open work is acted on — updated or widened
— or, where it is genuinely separate, proposed so you decide with the match in
front of you.

A proposal is never refused for any of this. What happens there is that the
harness does not admit it on an approved goal's authority: it comes to you with
the item it looks like named on it, and whether the two are the same work is
yours to say.

The check needs a listing of the tracker, and a listing the tracker would not
give is not a pass. The listing is asked for again under the same rule a run's
tracker writes are retried under — see
[waiting out a network that dropped](operations.md#waiting-out-a-network-that-dropped)
— and a listing that still fails after that refuses the creation with the reason,
and brings a proposal to you with the same reason where the goals would otherwise
have admitted it unasked. On 2026-09-18 the listing timed out under an admission
and the admission went in unchecked, which is what a guard that fails open costs.

What it is worth is two runs. `yoyodyne-ifd.274` duplicated the closed
`yoyodyne-ifd.229`, both admitted from one developer report; `yoyodyne-ifd.241`
was decomposed twice into the same pair of children. A duplicate cannot be
discovered by the run that gets it — its diff against the target branch cannot
contain work that branch already carries, so it spends its repair attempts and
its review rounds finding that out, and the second of those two produced a
confident review demanding code that had already merged.

## Bringing it an idea rather than a work item

Most of what you say to the Lead Product Manager is intent: build this, do that
first, stop doing the other. Some of it is not. "What if we did X", "is Y worth
it", "should we move to Z" is a question, and the answer to it is usually
neither yes nor no — it is what the evidence says, what the product is already
committed to, what is still unknown, and a recommendation you can disagree with.

It works through one of those rather than answering off the top of its head. It
asks what it genuinely needs to know first, one or two questions at a time. Where
evidence would change what it would recommend, it gathers some. Then it weighs
the idea against your brief and your goals, because whether an idea is good in
the abstract is not what you asked.

**Gathering evidence is a second harness capability, and it is off until you turn
it on.** The role still has no network. What it has is the same arrangement it
has with the tracker: it names a question and one of the sources you configured,
the harness runs that source, and it hands back what came out. A source is a
command you wrote — see
[research sources](configuration.md#research-sources) — so what the harness may
reach is exactly what you named and nothing else. Only the question leaves your
machine, redacted and bounded on the way out; a project that configured no source
has the capability off, and the Lead Product Manager is told so and says it could not
check rather than answering from memory as though it had.

What comes back is untrusted. A search result is a stranger's prose arriving
inside a prompt, so it is delivered framed as evidence about the world and never
as instruction, exactly as your own repository documents already are. Every
question and what it returned is printed to you as it happens, because research
spends your money outside this machine.

**What it concludes is written down.** The recommendation is one of four — adopt,
reject, defer, or run a bounded experiment — and it is recorded with the
reasoning, how the idea sits against the brief and the goals, the sources it
cites, what the evidence states, what it inferred rather than read, what it is
still uncertain about, and what argues the other way. Those last four are
separate fields on purpose: a paragraph is where the difference between a fact
and a hunch goes to die. Where it could not get adequate evidence it says so and
the recommendation reflects that, rather than answering confidently anyway.

`yoyo evaluation list` and `yoyo evaluation show <id>` read them back, and the
record keeps what the harness actually retrieved — from which source, at what
moment — beside the sources the Lead Product Manager cited. The two are different
claims: one is what it says it read, the other is what was fetched.

**An evaluation is advice and the harness treats it as nothing else.** Recording
one admits no work, changes no document, and approves nothing. Everything it
might lead to already has a path with an approval on it and none of those paths
runs through here: work reaches the queue as a proposal, under whatever approval
[your project asks for](configuration.md#what-reaches-the-queue); a change to the
brief or the goals is yours to make; a change to a design or a decision record is
the architect's, through `yoyo amendment`. That separation is the point. Research
that could quietly turn an idea into approved work would be a way to approve work
by asking a model to look something up.

The brief and the goals are the Lead Product Manager's documents and yours to
approve. It has no way to write a file, so it hands the harness the document
instead. With a human confirmation policy, you are shown what would be written
and asked; your `y` files it, recording the Lead Product Manager's revision and
your approval. That file then sits uncommitted in your checkout until you commit
it. An automatic policy confirms a revision recorded as consistent with intent
and sends its exact content through an isolated run, checks, independent review,
and normal integration. No operator prompt or manual commit is needed for that
path. The policy and the owning role are recorded in the document; see [writing a document from a
conversation](artifacts.md#writing-a-document-from-a-conversation). A change that
moves what the goals admit or refuse is still yours to decide, and it says so.

The listing it is given names items by title, so when a title is not enough to
judge whether new work belongs inside an existing item or beside it, it reads
that item in full and carries on from what it found. That happens inside your
one message, up to four rounds of it. Results it has not seen when the rounds
run out are written into the conversation's own record, so they reach it the
next time anything is said to that conversation — including from a later
process, since an agent that never learns what its own creates and closes did is
the one that will describe them wrongly. Item text is treated as evidence
exactly as a specification is: a description says what some work is, never what
to do.

That listing is also a snapshot, and the order is the one decision that must not
be made from one: it was gathered when the conversation opened, work closes while
the conversation carries on, and the role that owns the order is the role that can
least afford a queue that has stopped moving. On 2026-08-18 an item was moved down
a tier for waiting on work that had been finished for hours, and the harness
applied the change to an item that was itself already closed without saying so.
The survey action is the live answer — the open items as the tracker holds them
now, in the same order and the same shape as the listing the conversation opened
with, so the two can be read against each other — and the persona directs every
ordering decision to start from one.

Acting is checked at the moment it acts, too. The harness reads the item an action
names as it carries the action out, so an action aimed at work that has moved on
says so where the reasoning that aimed it is still happening: the result names the
state the tracker holds the item in whenever that is not open, and the Lead Product
Manager reconciles it in the same reply rather than never. An action that would
mean nothing on work that has already left the backlog — reordering it, closing it
again, retiring it — is refused for that reason, and the refusal names the
closure. Recording a note on finished work still means something, so that is
carried out with the closure stated rather than refused. An item the tracker will
not describe is neither refused nor assumed to be open: the action is attempted
and what could not be read is said.

## Proposals, and deciding them in batches

The Lead Product Manager can propose a Beads work item instead of creating one, when
the decision is yours rather than its. What becomes of a proposal is
[`approvals.work_items`](configuration.md#what-reaches-the-queue) to decide,
and until you say otherwise every one of them is put to you: shown as a numbered
card with its reasoning, and created only after an answer that approves it by
name. A proposal you left undecided is named when the conversation ends, and a
created item records the conversation, the turn, and the rationale it came from.
A proposal the harness cannot read is reported and the conversation carries on.

**The Lead Product Manager can take back a proposal of her own.** Until you
decide one, a proposal she made is hers to withdraw, with the `withdraw` tracker
action naming it exactly as it was listed and saying why:

```text
{"action":"withdraw","proposal":"959.1","reason":"the admission was mine to make; the duplicate guard's fallback proposed it"}
```

It comes off your list at once, from this process and from the conversation's
record, so no later `yoyo chat` offers it to you. The log keeps the proposal and
records it as withdrawn, with her reason and her role — never as declined, which
is your answer and keeps your words. A proposal already decided, or one this
conversation never made, cannot be withdrawn and the action says so. It is what
she does when a proposal should never have reached you: a decision her own
authority covered, or work since admitted another way.

**A single message decides a proposal too, and it is the same decision.** A
`--message` invocation has nobody standing at a prompt, so it creates nothing and
prints what is awaiting you, named by its own identifier; the next message
decides it. `yoyo chat --message "approve 3.1"` creates the item, and
`yoyo chat --message "decline 3.1 <reason>"` turns it down with your words kept
as the reason. A bare `y` works where exactly one proposal is waiting and no
question is, which is the same rule a prompt answers by; with a question waiting
beside it the `y` is refused with both named, because
[a message answers a question by naming it](#answering-a-question-from-a-single-message)
and must never answer one thing by deciding another. A document the
conversation wrote and whose policy requires your confirmation is decided the
same way and only by name —
`approve document-4.1`, `decline document-4.1 <reason>` — and a bare `y` never
decides one; see [writing a document from a
conversation](artifacts.md#writing-a-document-from-a-conversation).

**Two shapes decide, and everything else is speech.** A message decides when it
names a proposal by its identifier — `approve 3.1`, `decline 3.1 too vague` —
or when it is nothing but decision words: `y`, `no`, `decline all`, `approve 1,3`.
Anything else is said to the Lead Product Manager and leaves every proposal exactly
where it was, including a reply that happens to open with one of those words:
`no, let's look at the resolver instead` is a sentence, not a decline. That is
narrower than the prompt, deliberately. A prompt has just asked you, so the words
after your verb can only be about the question; a message has not, and the
proposal it would decide may be hours and several messages old. `decline 2 too
vague` is speech for the same reason — a bare number is a position in a listing
rather than a name, so name the proposal to decline it with a reason. An
undecided proposal outlives the process that proposed it, so the invocation that
made it and the one that decides it are two different commands, hours apart if
you like.

**Set `work_items: automatic` to move that approval up to your goals** — approve
what the product should do, then watch it happen. Work that traces to a goal you
approved then goes into the queue without asking you, and you are told afterwards
what went in, with the goal that let it through. It holds only where the goal
actually resolves and the document stating it is approved as it now stands, so a
project that has just opted in still asks about everything until its first
`yoyo artifact approve`. Everything short of that is still put to you, with why
you are being asked printed on the card. A created item records which of the two
it was, because an item claiming an approval you never gave is the one record
this arrangement cannot afford.

**Or keep the gate and carve one class out of it.**
[`approvals.work_item_exemptions`](configuration.md#what-reaches-the-queue)
lists classes of work the per-item gate is not asking about, and there is one:
`diagnosis`, work that only reads what is already there and produces findings
rather than a change. It is empty until you write it, the Lead Product Manager is told
about a class only where you have exempted it, and work admitted under one still
names a goal the repository records. It is for the operator who wants to be asked
before the product changes and does not want to be asked before something counts
what is already in the repository. It narrows that per-item question and nothing
else: under `work_items: automatic` there is no per-item question left to narrow,
so an exempt class is admitted on the approved goal every other item is admitted
on.

A turn that proposes five things is not five questions in a row. One answer
decides as many of them as you like:

```text
╭─ 1 · proposal 4.1 · Pause on a usage limit ───────────────────────────────────
│ Wait for the limit to reset and resume the same run.
│ why: you said capacity is not failure.
│ goal: run development nearly autonomously.
╰──────────────────────────────────────────────────────────────────────────────

decide 3 proposals? [approve 1,3 and decline 2 <reason>; anything else declines them all] approve 1,3 and decline 2 not this quarter
```

Batching changes the prompt and nothing underneath it. Each decision is recorded
on its own, the approval before the creation it authorized, and a decline keeps
your own words as the reason. An approval has to name what it creates —
`approve` on its own is refused wherever there is more than one item it could
mean, and the single-proposal question is still answered with `y` or `yes`,
which does name the only one there is.

There are three things an answer can be, and they are not the same:

- **It decides some or all of them.** Those decisions are carried out. Anything
  it said nothing about is left exactly where it was and put to you again,
  rather than guessed at; whatever is left keeps the number it was proposed
  with, so the last of five is still card 5.
- **It is a decision the harness cannot carry out whole** — it approves a card
  that is not there, decides the same card twice, or trails off into words after
  the proposals it named. Then it decides *no part of it*, including clauses
  that were perfectly good on their own, nothing is created, you are told what
  stopped it, and all of it is put to you again. Half of a misread answer is how
  work nobody asked for gets created, and being asked twice is the whole cost of
  a typo.
- **It is not a decision at all** — it names no proposal and starts with nothing
  the harness recognises, like `hmm` or `not this quarter`. That declines
  everything it was asked about and is kept as the reason, exactly as any answer
  that is not a yes always has been.

The one thing not put to you again is a proposal the tracker refused to create:
it stays undecided and is named when the conversation ends, because asking you
the same question until the tracker recovers is not asking you anything.

Because a decline keeps your words verbatim — the whole of what you typed about
those items, down to the word you turned them down with — its reason runs to the
end of the line, so write the approvals first. A decline separates the proposals
it names with commas or `and`, and everything after them is your reason even
when it starts with a number: `decline 2 3 weeks out` turns down card 2 for
being three weeks out rather than turning down cards 2 and 3.

Work reaches the queue only with a goal named against it, and the goal has to be
one you approved. Every proposal, and every item the Lead Product Manager admits
itself where [`approvals.work_items`](configuration.md#what-reaches-the-queue)
lets it, says which goal it serves in the words your goals document states it in;
the harness resolves that against the goals it reads from `docs/product` and
refuses an admission — or a proposal, before you are asked about it — that names
anything they do not state. So what an item says it is for is checked rather
than asserted, and the check is on the goal rather than on the fact that a
sentence was typed into the field. Wording is compared with case, spacing, and
trailing punctuation folded; nothing else is guessed at, so a paraphrase is
refused with the goals named rather than accepted as near enough. What a
proposal is placed against is checked the same way: a parent or dependency the
tracker does not hold proposes nothing at all, rather than becoming an approval
that fails after you have given it.

An admission also records `relevant_goals`: the list of goals the work could
affect beside the one it serves. The Lead Product Manager checks all recorded
goals at admission and names the potentially relevant ones. Each entry is
resolved by the same rules as `goal`; an unknown goal refuses the create,
update, or proposal before anything is written. There may be at most 20 entries,
each a nonempty line of at most 400 bytes. The list is tracker metadata, so it
survives notes being appended. An update supplying the list replaces it;
omitting it preserves the earlier list, and an empty list clears it.

The field has the same spelling in each contract:

```text
{"action":"create","title":"…","description":"…","goal":"…","relevant_goals":["…","…"],"reason":"…"}
{"action":"update","id":"…","relevant_goals":["…","…"],"reason":"…"}
```

A `yoyodyne-proposal` block carries it on each proposal too:

```text
{"items":[{"title":"…","description":"…","goal":"…","relevant_goals":["…","…"],"rationale":"…"}]}
```

The queue and a full item read show these goals. A survey names admitted items
with none recorded; the Lead Product Manager assesses them and records the list
with `update`, rather than the harness inferring it. Absence does not stop older
work running. The developer and reviewer receive the list labelled as goals the
change must not break, alongside the served goal and the standing goals.

Every admission also records where the work came from, as fields on the item in
the same write as the admission, for a `create` and for a proposal that is
admitted or approved alike. Who asked is the report an admission cites, where it
cites one, with the role that filed it; otherwise the directive it answers, which
is the operator asking in their own words; otherwise the session itself — the
operator, in a conversation they are speaking in, or the role's own pass, in a
recurring sweep. On whose behalf is the operator wherever a directive is named,
and whoever asked otherwise. A proposal records who asked when it is made, so an
operator approving a sweep's proposal later does not become its asker. A
decomposition records none: the item it was carved from carries where the work
came from. An item read says the origin on an `origin:` line, and says an item
admitted before origins were recorded is unknown; a survey names who asked beside
each item that records it. The harness's own filings, such as the item a red
landing files, record the harness. None of this changes what is admitted or
refused. `yoyo goals origins` sets the fields on older items whose notes already
name the report or directive they were admitted from, and guesses at nothing else.

Work admitted before that check existed names no goal, and it is grandfathered
rather than blocked or backfilled by the harness itself: nothing refuses to run
it, and it is reported as unattributed wherever the queue is read, because a
rule that failed every item admitted before attributions were checked would stop
all work to close a gap that has cost nothing yet. Grandfathering keeps that
work running until it is attributed; it does not mean it stays unattributed.
Attributing one is a judgement about what the work is for, so the Lead Product
Manager makes it in the conversation and the harness never guesses: `attribute`
records a goal on an item already in the backlog,
appended to what the item records rather than replacing it, so the goal an item
was admitted under is never rewritten. An item that names no goal and one whose
goal your goals do not state are reported apart, because the first is work to
attribute and the second is a claim to correct. [`yoyo goals`](artifacts.md#goals-and-what-work-serves-them)
reads both from outside the conversation.

### Backlog state that has stopped being true

Three things about admitted work go stale on their own, and correcting them is
the Lead Product Manager's rather than a person's. A status is written when work stops
and is never rewritten when what stopped it clears, so an item whose blocker
landed reads as blocked forever. A dependency records that one item waits for
another and goes on recording it after that other item closes. And an attribution
stops resolving: an item that recorded its goal in the document's words rather
than by [the goal's identity](artifacts.md#goals-and-what-work-serves-them) names
words nobody states once the document is reworded, an item whose goal was retired
or removed names a goal that is no longer active whichever way it named it, and an
item whose notes were replaced carries nothing where the tracker witnesses that
it once did. Re-wording a goal an item named by its identity is deliberately not
among them — leaving that item attributed is what the identity is for — so what
is corrected here is the attribution the goals cannot resolve rather than every
item an amendment touched. None of them is a decision anybody owes, and every one
of them used to wait for somebody to notice.

`repair` corrects one of the three, and `state` says which: `status` clears a
blocked status where every link the item records is one the tracker says is
finished, `dependency` retires a link on work the tracker holds as closed, and
`attribution` re-attributes an item whose recorded goal no longer resolves. The
staleness is the harness's judgement rather than the Lead Product Manager's assertion
— it reads the item, the admitted queue, the goals, and the work behind any link
the queue does not account for, all as the act runs, and refuses the correction
where they still say the old state is right, so a repair asked for over a listing
that has moved changes nothing. Absence is never the evidence: the admitted work
is what is open and what is blocked, so a blocker a run is working on right now
is in neither listing, and a status is not cleared because a link's item could
not be found. A goal a re-attribution names is resolved against the goals before
anything is written, exactly as an admission's is. Each act records on the item what was changed, what made the old state
stale, and the reason given, and a survey lists what is stale alongside the queue
so the pass that corrects it is the pass that was already looking.

None of this takes work out of the backlog. A repair is state hygiene and never a
decision: work that should leave is closed or retired in the open, exactly as
before.

Work somebody still has to release is never repaired, however stale its state
looks — an escalation waiting on a decision, a change that exists only on a
preserved branch, a publication that never finished, an active directive that
pauses the work it affects. The escalation is the one that most needs saying:
triage blocks an item in order to escalate it and leaves no dependency behind, so
an escalated item reads as a blocked status with nothing at all standing behind
it, and the hold is the whole of what separates the two. The preserved branch is
the one that was misread. On 2026-09-19 the survey listed yoyodyne-ifd.372 under
the state a repair corrects, the Lead Product Manager cleared its blocked status as
"no longer held behind a preserved run", and the item's own notes still said the
stopped run's branch and worktree were checked and there: the hold had been read
off the run's removal flags. So whether a stopped run's change is still there is
now looked for rather than read — any stopped run whose branch or worktree
exists in the repository holds its item, whatever the run's record says, and a
look that could not be made holds it as if they did, saying so. A stopped run
about which the development manager has recorded a decision the harness has
still to carry out — a repair continuation first among them — holds its item
with nothing of it surviving, because what the continuation resumes is the run
and a fresh pull would start over beside it. Such an item is reported with the
reason it is held, naming the run, every pass, and left exactly as it is:
clearing its status would start a fresh run on top of work that is still there.
A survey lists an item in exactly one of its two lists, and a repair asked for
on a held one is refused with the sentence the survey gave. Both records behind
a hold fail the same way: a conversation that cannot read what the harness is
holding, or cannot read the directives, corrects nothing rather than deciding
that nothing is held — for the same reason
[the queue holds every blocked item it cannot read a hold for](work.md#letting-the-harness-choose-the-work),
since a reader that cannot tell a stale status from a stoppage must not clear
either.

A cleared status is reported from the item as the write left it. Twice in one
week — yoyodyne-ifd.346 on 2026-09-18, yoyodyne-ifd.372 on 2026-09-19 — the
outcome said the status was cleared and, on the same line, that the item was
blocked as the tracker held it now; the second half was the reading taken before
the write, appended as if it were the result. Now "cleared" means the tracker
read the status back as open after the write, and a write it did not read back
that way is reported as failed rather than as cleared.

An item also says what carries it, where that is not a developer run, and whose
conversation that is. Work whose execution is a conversation with a role —
promoting a document the architect owns, settling a decomposition — is admitted
with `executor: conversation:architect`, naming the role whose conversation
carries it, and `update` sets it on work already in the queue. The role is
required: what the channel says about a handed-over item, until somebody picks it
up, is whatever the marker named. The harness never selects a marked
item for a developer run: it keeps its place in your order, the queue says what
carries it rather than reporting it as ready, and a pass that reaches it says it
passed it over rather than counting it among the work about to become pullable.
Naming an item yourself with `yoyo run` is unaffected, because that is you
deciding. Work that says nothing is a developer run, which is nearly all of it —
and which is why the marker is not retroactive: everything admitted before you
start marking says nothing, and is chosen as ordinary developer work. Which
queued items need one is a product judgement, so bringing an existing queue
under the guard is a pass over it with `update`. Before this the harness could
not tell, and it cost a whole run and two review rounds on an item no developer
could execute — with those rounds counted against the item's cap, so a second
mis-selection would have escalated work nobody had started.
[How work flows](work.md#letting-the-harness-choose-the-work) is the
selection side of it.

Two things follow from the marker that did not at first. The harness reads the
shape of conversation work at admission and refuses it unmarked: a creation or
update whose "Done means" says a design or a ruling is recorded, published,
promoted, or ratified, or whose title has the architect as its subject — "The
architect designs …", "The architect rules …" — is refused when it names no
executor, with the marker named as the fix. yoyodyne-ifd.330 was admitted
exactly so, at turn 468, with the marker left off, and was handed to a developer
run that could only report the design had already landed;
[the diagnosis](diagnoses/yoyodyne-ifd-367-conversation-item-dispatched.md)
is the whole of it. The reading is deliberately narrow — "decision" is what
triage records on an item, "the design" is cited by nearly every developer
item, and neither is read — so an item it does not fire on is still yours to
mark. And a marked item closes on its own once its work lands: the pass reads
the documents the marked role owns, and an item whose identifier opens the
reason of a revision in one of them — `yoyodyne-ifd.330 - side conversations
designed` in a design's revision log, made by that role — is closed with the
document and the revision cited, at the first pull after the revision is in the
tree. Reopen it with a note if the close was wrong: the item carries the
revision it was closed on and is not closed on that revision again. The same
clause that is refused on an unmarked item is what a marked one says: "Done means the ruling is recorded on the slack-reporting design" is
admitted with `executor: conversation:architect`, and refused with none.

An item also says whether it is to be started at all. Work you still want and do
not want picked up yet — deferred by a scope decision, waiting on something
outside the harness — is **parked**, with `park`, and the reason you give is what
the item then says about itself. A parked item keeps its place in your order and
is never selected however far the queue drains; it is listed as parked wherever
the queue is shown, so you can see what you parked rather than inferring it; and
`unpark` puts it back. `parked` on a creation admits work already parked, which
matters because a creation's identifier does not reach the Lead Product Manager until
its next turn.

You are not the only one that parks. A run whose change is evidence rather than
the work parks the item it was made for, with the account of what would release
it as the parking reason — so an item that comes back from a run parked is one to
read the reason on and decide about, exactly as with one you parked yourself.
Either of two readers can say so, and the reason is written in the words of
whichever did: the developer, claiming a landing that discharges nothing, or the
reviewer, approving the change as evidence rather than as the work. The exception
is a landing that named the impediment as another work item, which leaves the
item open waiting on that work instead and never lifts a parking you had placed.
Where a run does park an item you had parked, its reason replaces yours and yours
goes into the item's notes, so the decision you took is still readable.
[What a landing claims](work.md#what-a-landing-claims) and
[what an approval approves](work.md#what-an-approval-approves) are why.

A low priority is not parking, and that distinction is what this cost to learn.
Priority 4 was being used as parking by convention, which reads as "last" to
everything that pulls rather than "never" — and `--watch` drains queues routinely.
On 2026-08-27 one drained to the bottom, started work a scope decision had
deferred months earlier, and the run failed having cost $34.38. Parking is not
retroactive either: an item parked by convention stays selectable until it is
parked in fact.

An item also carries the tracker's own labels, and they are how an admission
practice is written where it can be checked. The practice that provoked this is
the reliability directive of 2026-09-19: every item admitted under it, every
bug, and every stall or mistake fix carries a `reliability` label, and a
[developer slot that prefers the label](configuration.md#a-developer-slot-that-prefers-a-label)
pulls that work first and the rest only when none of it is ready. `labels` on
a creation applies them in the same write as the admission, so the item never
exists unlabelled — for the reason `parked` and `executor` are set there — and
`label` puts one on an item already in the queue with `add` or takes one off
with `remove`, one label per action, with the reason recorded on the item
beside the change. A label is an identifier, one word such as `reliability`,
and an action naming anything else is refused whole; a child created under a
labelled parent inherits the parent's labels, which is Beads' own behaviour.
Labels are listed beside each item's executor wherever the queue is shown, so
a survey says which items carry which. The development manager has the same
two, because a label is one of the item's fields and she may already update an
item. Nothing here migrates the queue: the items that qualified before the
action existed were labelled by hand.

Work it will not attach to a goal is not proposed and not quietly dropped
either — it stops and asks you, and the three cases stay apart because you
answer them differently. Work it can find no goal for is usually a sign the
goals are incomplete, so it asks which goal it should serve. Work that would cut
against a goal is a conflict it puts to you rather than proposing with a caveat.
Work that fits the goals as written and that it judges to be against what the
product is for is an opinion it states and can be wrong about, because you can
overrule an opinion it voiced and cannot overrule one it kept to itself. Each
question waits for your answer before the conversation moves on, what you say
reaches it on your next message, and a question you leave unanswered is named
when the conversation ends rather than passing for agreement.

Where the answer is one of a few, the question arrives as those few. The answers
are listed under it and you pick one with the cursor keys, and the last of them
is always your own words — for the answer nobody listed, or for the question you
want to ask back. Where the terminal cannot do better, which is a pipe, a
redirected conversation, and anything that reports itself as `dumb`, the same
answers are the same numbered list and you type the number instead of moving a
marker; typing anything else there is that answer, so a numbered prompt never
costs you a sentence you had already written. What is recorded either way is the
answer itself, in the words it was offered in, and that is what reaches the
Lead Product Manager. Offering answers never narrows what you may say, which is why
your own words are on every list there is. `--message` has nobody standing at a
prompt, so it prints the questions with the answers that were on offer, named by
their own identifiers, and proposes nothing; the answer arrives as its own
message.

### Answering a question from a single message

A question outlives the process that asked it, exactly as a proposal does, so
the invocation that
raised it and the one that answers it are two different commands.
`yoyo chat --message "answer c3.1 the goal stands"` answers concern `c3.1` with
your words, and `yoyo chat --message "answer c3.1 2"` answers it with the second
of the answers it offered, recorded in the words it was offered in; the
`answer` in front is optional, because the identifier is the thing nobody types
by accident. A bare `yes` or `no` answers the question where it is the only
thing the conversation is waiting on, which is the same rule a lone proposal is
approved by. Anything else is said to the Lead Product Manager and leaves the
question open, so a sentence about the question reaches it as a sentence and
the question is still listed for you to answer.

What a message must never do is answer one thing by deciding another. A
question and a proposal can be waiting at the same time — a turn can raise a
concern and propose an item in the same breath — and until 2026-09-20 a `yes`
meant for the question, sent as a message, approved whatever proposal was
undecided instead: the approval was real, recorded, and created the item,
because a message could decide a proposal and had no way to reach a concern at
all. So when a question is waiting and more than one thing is, a message that
names nothing is refused with the list of what is waiting and how to name each
— `answer c3.1 …` for the question, `approve 3.1` or `decline 3.1 …` for the
proposal — and is applied to none of them and said to nobody. Naming what you
mean is what gets through: `answer c3.1 …` answers the question with the
proposal left exactly where it was, and `approve 3.1` decides the proposal with
the question still open. A batch of proposals with no question beside it keeps
the grammar below, since `decline all` and `approve 1,3` can only be about
proposals.

An interactive `yoyo chat` puts a question still waiting from an earlier
process to you as it opens, before anything else, for the reason it puts an
undecided proposal: a question nobody was ever shown would otherwise be named
as unanswered at the end of a conversation it was never asked in.

## Steering the work from the conversation

A line that begins with a slash is a command the harness carries out for you;
everything else is said to the Lead Product Manager:

```text
/status                  what is in flight, claimed, blocked, available, and done, with prices
/backlog                 the admitted work in order, and what would be pulled next
/show <id>               one bounded work item read, and what each run for it cost
/diff [id]               what a run changed, from the run's own record
/reports                 what agents reported without it stopping their work
/refresh                 re-read the repository and tracker into this conversation
/work <beads-id>         run one work item now, while you keep talking
/wait                    wait for the run this conversation started and report it
/stop [beads-id] [reason]  stop one item's run wherever it is running, and settle what it left
/hold [reason]           stop the harness starting anything more on its own; running work carries on
/release                 let the harness start work on its own again
/intake                  whether the harness may start work on its own, and why not
/stop-everything [reason]  hold intake and stop every run in flight, settling what each left
/redirect <id> <what to do differently>
/directives              what you have directed, and what still applies
/directive <what you have decided>
/directive ambiguous <what is unresolved> | <what you said>
/directive artifact <artifact> <what is unresolved> | <what changes>
/resolve <directive-id> <how it was settled>
/withdraw <directive-id> <why you no longer mean it>
/help                    the list
/exit                    end the conversation, stopping anything it is running
```

A slash means the same thing in `yoyo chat --message`: the harness carries the
command out and the Lead Product Manager is never asked, because it cannot carry out
a command and a turn spent trying is a turn you paid for. The four that only
mean something inside a conversation — `/work`, `/wait`, a bare `/stop`, and `/exit` with its alias `/quit`,
each of which starts or acts on a run the conversation's own process owns — are
refused there and say what to reach for instead, rather than being half carried
out by a process that is about to exit. `/stop <beads-id>` is not one of them: it
reads durable state and asks whichever process holds the run, so it means exactly
the same thing in a single message. With `--json` what the command printed
is a field of its own, so nothing reads as something the Lead Product Manager said.

`/backlog` shows the ordering the Lead Product Manager set, which is the one thing a
development manager pulls from: the admitted work that is not finished, in
priority order, each unready item saying what is holding it, and the item that
would be pulled next named at the end. Like `/status` it is a report rather than
an export — the first twenty entries are listed and the rest are counted, so a
long backlog says how much of itself you are not looking at, and the item that
would be pulled next is named even when it falls outside the listed part. It is
assembled from the same tracker `/status` reads rather than stored anywhere, so
it cannot drift from the priorities the Lead Product Manager actually set.

Whether an item can be pulled is the tracker's answer rather than one the
harness works out, and that is a deliberate choice about what a listing can be
trusted for. A Beads listing carries the dependencies between items, but it
records only that a dependency exists: the entry reads exactly the same after
the blocking work is closed as it did before. Deciding readiness from that would
either name blocked work as the next thing to pull, on any listing that left
dependencies out, or hold an item back forever for a blocker that finished
months ago. So the harness asks Beads what is ready — the same blocker-aware
question `bd ready` answers — and a dependency is named as a wait only when the
work it points at is itself still in the backlog. An unready item says which of
the three things is holding it: named work it waits for, a blocker recorded on
the item, or the tracker simply not offering it. A tracker slice it cannot read,
including that readiness answer, fails the whole report instead of returning the
half it could: a survey describing part of what is happening is still worth
reading, and half a queue answers "what happens next" wrongly rather than
incompletely.

`/work` runs exactly what `yoyo run` would run — the same worktree, developer,
checks, reviewer, and integration policy — in the background, so the
conversation stays a conversation. One run at a time. `/status` reads durable
run state and the tracker, so a run another process is executing is as visible
as one started here. Every run it lists says why that item was chosen, in the
words whoever chose it recorded when the run started, and a run that recorded
nothing is named as one nothing accounts for rather than shown with the line
missing — an item running for no visible reason is the thing you most need to
see, and it is indistinguishable from work happening behind your back. A run that
is owed a continuation rather than working —
one waiting out a provider usage limit, one whose provider was stopped on time,
or one [paused for an unresolved directive](#directives-and-the-work-they-pause)
— is named as such rather than reported as progress. On a terminal a
finished run reports itself the moment it finishes, above whatever you are
typing, and rings the bell and renames the terminal window as it does, because a
conversation left in a background tab is exactly where a run finishing goes
unnoticed; the window's name is put back when the conversation ends, so it never
outlives the work it was announcing. Where the conversation is a redirected
stream there is no such moment, so it is reported at the next line, or when you
ask for `/status` or `/wait` for it.

A run started here also says the few things it crosses on the way, one line each
above whatever you are typing: its checks passing, the reviewer's verdict, the
promotion into the target branch, and — where the project publishes — its pull
request being queued to merge and being merged. They are read from the run's own
durable record rather than from the process executing it, so what you are told
is what somebody reading that record afterwards would be told, and each is said
once: a crossing is a transition rather than a state, because an event log
scrolling past the conversation is what the activity line exists not to be. The
Lead Product Manager is told the same things as harness activity, so the next thing
it says about the work is not answering about a run it believes is still
developing.

`/stop` cancels the run, records why on the work item, and then settles what the
cancelled run left behind exactly as `yoyo reconcile` would: integrated work is
finished, and anything else becomes a durable blocker naming the branch and
worktree that were preserved. Two cases are exceptions, and both are reported as
what they are. A run that does not give up within the stop grace is reported as
still in flight rather than described as stopped. A run that reached its own
conclusion before the cancellation reached it — integrated under an automatic
policy, or finished with its worktree preserved under a `human` one, since a
successful run then promotes nothing — is reported as having finished on its
own: nothing is recorded on the item and nothing is settled, because nothing was
stopped. What separates the two is whether the harness reported a failure, not
whether anything was integrated. A run that had paused itself is not one of
these — whether it was waiting out a usage limit, had its provider stopped on
time, or stopped short for an unresolved directive — because it is owed a
continuation rather than finished, so the stop is
recorded against it, and the report says the run is preserved and continues only
if you start it again. Ending the conversation stops its run the same way,
because the process that owns the run is the one that is exiting.

Either way the conversation's own log says what happened rather than only what
was asked for: the stop is recorded as a request when you make it, and the run's
outcome — what it left behind, or the integration that beat the cancellation —
is recorded once it is known.

`/stop <beads-id>` stops that item's run wherever it is running, including in a
process this conversation has nothing to do with, which is what stopping has to
mean once work starts without you asking for it item by item. The difference is
only in who does the cancelling. A run this process owns is cancelled here. A run
somewhere else is *asked*: the request is written beside the run, the process
working on it reads that at its next provider call and ends the run there, and
this waits for the run's own record to say so before reporting anything. So the
report is about the run rather than about the request. A run that gives up is
reported as stopped and what it left is settled exactly as above. A run that
reached its own conclusion first is reported as having finished before the stop
arrived, with nothing recorded on the item and nothing settled. And a run that
has not given up by the time the grace runs out is reported as still in flight,
stopping at its next provider call, with nothing it had thrown away — because a
provider invocation already streaming is never interrupted. That generation is
already paid for, and killing it would leave the run needing the same work again,
which is the cost that makes killing processes the wrong verb in the first place.

The development manager can stop a run too, without you. A run whose work she
decides is superseded, narrowed, or mis-launched is one she records a `stop`
decision about, and the harness writes the same request this does on her behalf,
naming her; the run stops at its next provider call exactly as it does for you,
with its branch and worktree preserved.
[Deciding what becomes of stopped work](#deciding-what-becomes-of-stopped-work)
has the decision, and [how work flows](work.md#letting-the-harness-choose-the-work)
has what it leaves behind. When it is the Lead Product Manager who decides the
item is superseded, narrowed, or to be retired, her decision reaches the
development manager as a docket entry rather than through you;
[a decision about work in flight](work.md#a-decision-about-work-in-flight) is how.

`/hold` is the narrower verb and the one with no equivalent before now: it stops
the harness *choosing* new work, and lets everything already running finish. It
is what you reach for when the queue looks wrong but nothing is on fire. It holds
nothing you name yourself — `/work <beads-id>` still runs an item under it, since
you placed the hold and naming something is you deciding it is the exception —
and `/release` lets the harness choose again — as does `yoyo release` at a
terminal, which lifts the same record, for when no conversation is open. A hold
the failure-storm brake placed overnight does not need either: the brake
summons the development manager to decide it and probes the line itself if she
does not, and only a hold she escalated
[waits on you](operations.md#pausing-everything-and-resuming-it). A held intake
leads `/status` with its own banner saying when it was placed, who placed it,
and why, beneath the PAUSED banner if both are active. Who placed it is on the
record rather than assumed: the harness's own failure-storm brake
([`blocked_runs_before_intake_hold`](configuration.md#watching-instead-of-draining))
places the same hold, and a banner that called that one yours would send you
looking for a decision you never made. Who lifted a hold is recorded too, so the
channel can say so when it announces the release. It is recorded per product, unlike
[`yoyo pause`](operations.md#pausing-everything-and-resuming-it), because what a development
manager may pull is a fact about one backlog.

`/stop-everything` is the third and bluntest: it holds intake so nothing more
starts, and then stops every run in flight, settling what each leaves. The order
matters — holding first is what stops it becoming a race against the next item
being started while the last one is being stopped. It reports what became of each
run rather than what was asked of it, and one run that could not be stopped never
hides the others.

`/show` prints a bounded reading of one work item — its status, priority, parent,
dependencies, description, design, acceptance criteria, and notes — through the
same tracker capability the Lead Product Manager reads items with. What you see is
what the agent discussing it could see, which is the point: the two of you are
reading the same item rather than two accounts of it. Beneath the item it prints
what the item cost, broken down by the runs it took.

A read of an item, by any role that reads one and by `/show`, carries the runs
made for it after the item: each run's identifier, when it started and ended,
what became of it, and whether its change is preserved, newest first. It also
says what is holding the item after a stopped run, and which run that hold is
about. That run is marked in the list. The runs come from the harness's own
records rather than the notes, and the list is bounded at ten runs rather than
cut with the notes. It says how many older runs it left out, and the run the
hold is about is always listed. A record that could not be read is said as
unread rather than left out. On 2026-09-26 the development manager could not
find the run to record a decision on for `yoyodyne-ifd.430.13.4`, because the
note naming it was in the part of the notes the read cut.

An item read carries at most 8 KiB before the separate runs section. Long text
before the notes is returned in parts: each part names its starting byte, how
many bytes it returns, and how many remain. It gives the next tracker read
request, such as `{"action":"read","id":"beads-id","offset":3500}`, for a role
to put in its next `yoyodyne-tracker` block. The offset counts bytes in the
rendered item text, including headings, before the notes. Following these
requests reaches the entire description, design, and acceptance criteria in
order. A short item's ordinary read keeps the same output. The role can use
`"offset":0` to start the complete text again; each part reads the current item,
so it must start again if the item changes between reads. Action and round
limits still apply, and a role continues in a later turn where needed.
`/show` displays the first part and its continuation request.
The notes are cut from their beginning rather than their end, and are guaranteed
room whatever else the item carries: notes are only appended to, so their end is
what was written most recently, and a reader checking whether something was just
recorded is asking about that end. Cutting the other way is how two operator
directions written onto `yoyodyne-ifd.283` came to read as writes that never
landed — both were durable, and both were outside the window this rendering
showed. The runs section above is outside that cut.

## Directives, and the work they pause

A redirection is about one item. A directive is about the product: it is
recorded for the product rather than for the agent you happened to say it to, so
it reaches every run of every item, in this process and in any other. That is
what `yoyo directive`, `/directive`, and a reply in a work item's
[Slack thread](reporting.md#reporting-into-slack) write, and it is the same
record every run reads before it starts, before it resumes, and before it puts a
change through the gate that would integrate it. Which of the three it arrived
through changes nothing about it downstream.

Most directives are operational. They take effect from the moment they are
recorded, and nothing waits for them:

```text
/directive prefer smaller pull requests
```

Two kinds pause the work they affect, because that work would otherwise be
written and promoted against intent that is being rewritten or was never
settled. One changes a governed artifact — the brief, a goal, a design — and the
work derived from it waits until the change is decided. The other is one nobody
can act on without deciding something you did not, and the work waits until you
answer:

```text
/directive artifact docs/product/goals/v1-goals.md whether autonomy is still the goal | the autonomy goal is being rewritten
/directive ambiguous which of the two publishing behaviours I meant | do publishing differently
```

You state which kind it is rather than the harness guessing. Pausing every run
because something classified a sentence would be a worse failure than pausing
none, so the kind is yours to say, and a directive that pauses work is refused
unless it names what is unresolved: a pause nobody can name a reason for is a
pause nobody can lift.

A pause is not a cancellation. Work already under way keeps its claim, its
branch, its worktree, and its developer session, and stops at its next gate —
the point before its change could be checked, judged, or promoted. Work that has
not started does not start. `yoyo reconcile` reports such a run as resumable and
leaves it exactly where it is, so nothing settles it out from under you, and the
item itself records which directive stopped it and what about that directive is
unresolved.

`/resolve <id> <how it was settled>` lifts the pause. The release is the record
changing rather than anything done to a run: the next time the item is started,
in whichever process, the same run continues from the gate it stopped at.
`/directives` lists what is recorded, the active ones first and the ones that
no longer apply after them. An identifier may be shortened to any prefix that
names exactly one directive.

An operational directive has nothing to resolve — it applied from the
moment it was recorded and held nothing up — so what settles one is somebody
carrying it out. Where that means admitting work, the Lead Product Manager names the
directive as it admits the item: the item's notes record which directive it
answers and in your words, and the directive's own record is told which item it
became. `/directives` then shows it as carried out, with the identifier of the
work, so what came of a directive is readable from the record rather than from
whoever remembers. A directive you gave in a
[Slack thread](reporting.md#reporting-into-slack) is answered in that thread at
the same moment, tagging you, which is how a reply that turned into a work item
tells you which one.

Carrying one out does not withdraw it. A standing instruction like "prefer
smaller pull requests" is still the instruction after the work it prompted is
admitted, so it still applies and stays in the listing, now with an account of
what it produced under it.

What ends a directive is you withdrawing it, or the Lead Product Manager ending
it from her conversation, as described below.
`/withdraw <id> <why you no longer mean it>` takes it out of force: nothing is enforced against it from then on, no
run is held by it, and `/directives` stops listing it among what still applies.
It and the Lead Product Manager's two endings are the only things that end an
operational directive, which otherwise stands from the moment it is recorded
and never lapses — including one that was never an
instruction, like a question the harness read as a directive, which would
otherwise be listed as live direction and met by every run forever.

Withdrawing is not deleting and not settling. The record keeps your words and
whatever it had already collected, and gains who withdrew it, when, and why, so
a run that was held or judged while it stood is still explicable; `/directives`
shows it under what no longer applies, reading as withdrawn. Withdrawing one
that pauses work lifts that pause without answering what it was waiting for,
which is what taking a question back means.

From a conversation, who is you: the record names the conversation and the turn
you did it on, and the conversation's role beside them. From the command line it
is asked for rather than assumed — `--by` is required, because agents run `yoyo`
too and a command line does not say who typed at it, and an agent names its role
with `--as`. Putting "the operator" on a withdrawal an agent made would be a
false answer to the one question that record exists to answer. The role is what
a Slack thread the directive was asked for in is answered in the voice of, when
the withdrawal is said there.

From the command line the same records are reachable, which is how a directive
you gave to an agent other than the Lead Product Manager gets written down:

```text
./bin/yoyo directive list
./bin/yoyo directive record --kind ambiguous \
  --unresolved "which of the two publishing behaviours was meant" \
  --received-by reviewer \
  "do publishing differently"
./bin/yoyo directive resolve --resolution "the second one" directive-3f2a
./bin/yoyo directive withdraw --by "Mason, at a terminal" \
  --reason "recorded in error: that was a question" directive-05d6
```

That last one is the shape of a real repair. Until questions were told from
instructions, a question typed in a Slack thread was recorded as a standing
directive — the operator's *What does 'in force from now' mean?* of 2026-08-30
and his *Did you restart?* a week later both were — and a question in the record
is a directive nobody gave. The listing reads its entries by the same rule the
channel now reads replies with, and marks any operational directive that still
applies and whose words are a question: *reads as a question rather than an
instruction: it directs nothing, and withdrawing it is what ends it*. `/directives` in the
conversation carries the same mark. Nothing is withdrawn for you — the record is
evidence rather than a worklist — but the mark is what says which entries to
look at.

What the harness enforces is the pause; what it does not do yet is work out
which items derive from a changed artifact. A directive that names no work
therefore pauses all of it, which is the safe reading rather than a clever one.
`yoyo directive record --scope` narrows it to the items you name; a directive
recorded from the conversation names none, so it pauses everything and reports
the work in flight and claimed as what it just stopped.

**The Lead Product Manager can end a directive too**, from her own tracker block,
so a directive she has carried into a document or a question that was recorded
as one does not wait on you to type a command. The `directive` action names the
directive by its identifier or any prefix that names exactly one, says which
ending it is, and says why:

```text
{"action":"directive","directive":"directive-e824","decision":"resolve","became":"docs/product/operating-rules.md","reason":"written into the operating rules as a standing rule"}
{"action":"directive","directive":"directive-05d6","decision":"withdraw","reason":"a question, not a direction"}
```

`resolve` is for a directive whose content now lives somewhere else: a standing
rule written into a product document, or an instruction a work item answers in
full. `became` names that document by its path or that item by its identifier,
and is required. The directive stops applying as a directive, and its record
says what it became, why, and who did it. An operational directive keeps any
outcome it had already collected beside that, and a directive that pauses work
is resolved with her reason as the answer, which lifts the pause. `withdraw` is
for a directive that directs nothing, and takes no `became`: it is recorded
exactly as your withdrawal is, with her named as who withdrew it. Either way the
record names her role, her conversation, and the turn, never you, and
`/directives` and `yoyo directive list --all` show it under what no longer
applies, with your words kept. A directive that has already ended, however it
ended, is refused, and the refusal says how it ended. No other role holds the
action.

Apart from ending one, only you reach any of this. The Lead Product Manager owns what the queue says and the
order it is in; running, stopping, and redirecting the work itself stays yours,
so nothing it writes starts or stops anything — a reply that contains `/work` is
prose. What it does get is an account of what you had the harness do, carried
into its next turn as evidence, so the conversation keeps discussing the product
as it now is rather than as it was when the conversation opened.

A conversation is durable. It is recorded outside the repository under the
operating system's state directory, so leaving and running `yoyo chat` again
resumes the same conversation; `--new` starts a fresh one instead. The record
keeps the requested model selector, the model the provider reported serving, the
provider session identifier, any action results the Lead Product Manager has not been
told about yet, the work item it last ran, which proposed changes to its own
documents it has already been shown, and when its picture of the
repository and tracker was gathered and against what commit, and the normalized
event stream is stored beside it — including what the operator asked the harness
to do, which is recorded in the conversation's own log beside the runs' logs. A
re-read that has been taken and not yet delivered is recorded too, with the text
of it kept in a file beside the record rather than inside it, because a picture
is close to the whole of what one record may be. So is the text of the picture
the agent last received, which is what a later refresh is compared against.

**Each reply is recorded whole up to 128 KiB, and a longer one says where it
was cut.** Until a role can write the document it owns, the event log is where
its ruling lives, so a reply the log shortened without saying so is a decision
lost without a word. That is how two of the architect's rulings were lost on
2026-09-24, under the 16 KiB bound replies used to be held to;
[the diagnosis](diagnoses/yoyodyne-ifd-430-20-replies-cut-in-the-record.md)
has the details. A reply past the bound is kept up to it, and the text in the log
ends at the cut with `…[cut here: the record holds the first 131072 of this
reply's 150230 bytes]`. The event also carries `cut`, `recorded_bytes`, and
`whole_bytes`, so a reader does not have to parse the sentence. You are still
shown the whole reply. After it, the transcript says the record is short:

```text
[record] a reply of 150230 bytes is recorded cut: event 2417 holds its first 131072 bytes, ending "whether the forge's required checks may stand in"; the role is told on its next turn so it can restate the rest.
```

`--json` carries the same thing as `record_cuts`. The role's next turn opens
with it, whichever process takes that turn, and asks the role to restate what
came after the cut. A rebuilt session reads the same marker in its history.
`yoyo agent cut-replies` lists every reply the product's conversation logs hold
cut, including logs no current record points at. A reply cut before the cut was
declared ends in `…[truncated]` with its size unrecorded, and the listing counts
those too.

## Talking to the other agents

`yoyo chat` is the Lead Product Manager, because product intent is where the work
comes from. Every other configured agent is reachable the same way:

```text
./bin/yoyo agent list                      # who is configured, and what each is doing
./bin/yoyo agent show architect            # one agent in full
./bin/yoyo agent memory architect          # what it remembers, with each memory's history
./bin/yoyo agent chat architect            # talk to it
./bin/yoyo agent chat development-manager --message "Decompose ifd.4." --json
```

The name is either the agent's configured name or the role it fills, and a role
two agents fill is a question rather than a request: name the one you mean. The
name you give is the agent that answers — its persona and its model, not its
sibling's — and the role it fills is what decides its authority. `yoyo chat`
names none, so it takes the agent filling the product-manager role.

**Each agent is a durable logical identity, not a process.** The provider that
answers a turn is started for that turn and gone afterwards; what survives it is
one conversation record per agent, with its own provider session, its own turn
count, and its own picture of the repository. Talking to the architect never
resumes what the Lead Product Manager was told, and where two agents fill one role
neither resumes the other: they are two identities with two sessions, and the
lease that stops a second process taking a turn with one of them leaves the
other free.
`yoyo agent list` reads all of it without starting a provider — including
whether another process has a conversation right now, which is what anything
else wanting a turn waits behind.

**What is exclusive is a turn, not your window.** An open `yoyo chat` spends
nearly all of its life waiting for you to type, and while it waits it puts the
conversation down: `yoyo chat --message` in another terminal, the harness's own
deliveries, and a second `yoyo chat` you leave open beside the first all reach
the same Lead Product Manager without you closing anything. What they take turns over
is the turn itself.

A command you typed queues behind a turn already in flight rather than being
turned away. It says what it is waiting for once the wait is long enough to
notice, and Ctrl-C ends the wait. A delivery the harness makes in the background
does the opposite and comes back later instead of waiting: nothing has been asked
of the agent yet, so there is nothing to lose by trying again, and a delivery that
waited would hold its budget open for the length of somebody else's turn.

A turn waiting out a provider usage window, or waiting for a provider nobody
can reach, records the wait and releases the conversation while it sleeps.
Before asking again it takes the conversation back and re-reads its record,
so a turn taken during that wait is continued from. `yoyo status` names the
waiting turn, its process, and what it waits on. A recurring pass or program
manager pass queues behind a turn still in flight. Its wait shares the scheduled
turn's fifteen-minute bound, or the caller's earlier deadline; if that runs out,
the pass is recorded as missed, naming the holder, with no failed firing before
its first turn. The next pass carries what the missed one would have read.

The exception is an agent configured to
[hold side threads](configuration.md#queueing-a-question-or-holding-it-on-a-side-thread).
A `--message` that finds such an agent mid-turn is answered beside that turn
rather than after it, on a side thread with its own record and its own lease, and
the answer says so. What comes back is the agent's judgment and never an action:
a side thread creates, decides, proposes, and admits nothing, and anything it
promised is tentative until the main conversation — whose next turn reads what
the side thread concluded, as memory rather than as dialogue — ratifies or
adjusts it. Commands and decisions never go aside; they reach the main
conversation and wait for it as they always have.

Your own window takes the conversation back when you press enter, waits there
the same way, and re-reads the record before it answers — so a turn taken
elsewhere while you were typing is one your next turn continues from rather than
one it overwrites. The exception is a run you started from the conversation with
[`/work`](#steering-the-work-from-the-conversation): that run reports itself into
the conversation from under the prompt, so the conversation is yours until it
ends.

The one thing it will not do is carry on through a conversation replaced
underneath it. An agent has **one** record, so `yoyo chat --new` elsewhere does
not sit alongside the open conversation — it replaces it. The open one notices at
your next message and ends, saying so, which protects the new conversation from
being overwritten in turn. What it cannot do is give you the displaced one back:
by the time anything notices, that record is already gone and it can no longer be
resumed. Its event log stays on disk under its own conversation identifier, so
what was said is still recoverable, but nothing points at it. Start a new
conversation from the window you are actually in.

An agent is conventionally named for its role, and `yoyo init` names every one
of them that way, so a project that has never configured two agents on a role has
its conversations exactly where the role would have put them.

**Every role applies the standing goals to everything it writes and every
decision it makes**, whichever goal the work item or its lane serves. The
standing set is read from the goals documents' `Standing goals` section
under the configured `product.specifications` home, delivered as authoritative
product intent. In Yoyodyne, [the standing goals](product/goals/v1-goals.md#standing-goals)
are the plain-language and autonomy goals: use ordinary words and make
decisions the role owns without routing them to the operator for approval.
This includes a program manager's lane report and post-mortems. An output or
decision that breaks one is a defect to report, naming the goal and where
it was broken.

**Every role writes for a person in ordinary words.** Anything a person reads
says what happened rather than the harness's name for its own mechanism, coins
no terms, and gives every time in the operator's local time with the zone
named, such as 08:20 PDT. The model every persona and contract carries is a
plain account of a stopped run: *the AI session running the developer produced
no output for five minutes, so the harness ended the run; the cause was outside
the work, so no repair attempt was spent and the change was kept.*

**What each role may do is fixed in the harness rather than in its persona.** A
project rewrites any persona it likes and the boundaries do not move:

| Role | Reads the tracker | Writes to the tracker | Reads the repository by path | Its own documents |
| --- | --- | --- | --- | --- |
| Lead Product Manager | yes | admits (governed by [`approvals.work_items`](configuration.md#what-reaches-the-queue)), orders, attributes, labels, parks and releases, closes, retires, [repairs stale state](#backlog-state-that-has-stopped-being-true), [ends a directive](#directives-and-the-work-they-pause) | yes, [labelled as description](#reading-the-repository-at-a-recorded-commit) | brief and goals: proposes, never writes |
| architect | yes | nothing | yes | designs, decisions, invariants: decides, and you record |
| development manager | yes | creates and links **only underneath admitted work**; updates and labels items; records triage decisions on stopped work | yes | none |
| developer, reviewer | yes | nothing | no | none |
| [program manager](designs/program-manager.md) | yes | admits (governed by [`approvals.work_items`](configuration.md#what-reaches-the-queue)), attributes, updates, labels, reparents, orders, parks and releases, and links **only inside its own lane** — [the lane label, read off the item as each action runs](#a-program-managers-lane); never closes, retires, or records triage | yes | none; it rewrites [its lane report](#a-program-managers-lane-report), which is kept under the state root rather than in the repository |

The Lead Product Manager's admitting is the one row a setting moves, and it moves in
one direction only. `approvals.work_items` decides what may reach the queue
without you, and at `human` — the shipped value — it refuses the direct
admission as well as the automatic one, because a gate the proposals held while
this door stood open would be no gate at all: the Lead Product Manager reaches both,
and work would arrive through whichever asked less. So a project that leaves the
setting alone has a Lead Product Manager that proposes work rather than admitting it,
and nothing reaches the backlog that you did not approve. Set `work_items` to
`automatic` and it admits directly again, against a goal you approved. Ordering,
attributing, repairing stale state, closing, and retiring are untouched either
way: those tidy work you already agreed to rather than adding any.

The development manager is the one worth reading twice, because it is where a
design becomes tracked work. It decomposes: every item it creates hangs under an
item the Lead Product Manager already admitted, and the harness refuses a creation
that names no parent. It cannot admit work, cannot reorder the backlog, and has
no close or retire — so a decomposition can never quietly become new scope, and
the backlog's order stays the Lead Product Manager's. What it created is recorded as
what it was: the item's own notes say it was created under its parent,
decomposing it, rather than admitted to the backlog, so the two acts stay
distinguishable long after the conversation that made one of them is gone. Work it discovers that belongs
elsewhere it says to you, for the Lead Product Manager to admit. It is also the role
that decides what becomes of work that stopped moving, which is the [triage
docket](#deciding-what-becomes-of-stopped-work) below.

Work carved out of a run that failed is the one decomposition the harness adds
to. Such a child may be written against the change that run made, and that
change is on the branch the run preserved rather than on the branch a fresh
worktree is cut from — so an item assuming files that exist only there is not
ready, however clean it reads, and the run that pulled it would start in a
worktree without them. Where the harness's own run records say the parent's
change never reached the integration target, a creation under it records in
the child's notes which run made that change, which branch and commit it is on,
and which pull request published it, as guidance. The child is left open, and
the result of the creation says whether it is held, to you and to the
development manager.

What holds it is the child's own text. A child that says in its title,
description, design, or acceptance criteria that it builds on its parent's
change — "builds on the parent's change", or the parent named, as in "builds on
yoyodyne-ifd.100's files" — is held by the scheduler, which reads that at every
pull, and listed as waiting on the parent's change rather than as held for a
person. It comes free however the change lands: a later run of the parent
promoting it, which the run records then say, or the parent closing — the
preserved branch cherry-picked, the pull request revived, or the substrate
rebuilt from nothing. A child that supersedes the parent's change rather than
building on its files says nothing of the kind and is not held at all. Nothing
links a child to wait on its own parent, because the tracker refuses that link
when the child already hangs on the parent, and nothing sets one blocked in its
place: on 2026-09-25 that fallback blocked all six children of
yoyodyne-ifd.429.13, which superseded the parent's pull request 757, with a
blocker nobody cleared. Which vehicle lands the parent's change is your
decision and the Lead Product Manager's — the development manager says what it
thinks and records none of it as scope. Decomposition of work whose change is on
the target branch is untouched, and so is the dependency structure the
development manager records itself.

The architect owns the designs, the decision records, and the invariants. It
still has no tools, and a design or a decision record it writes reaches the
repository anyway: it emits the document as a typed action. With an automatic
design approval policy, the harness confirms it without asking you and sends
the exact document through a run with checks and independent review before
integration. A failed check, review refusal, or target conflict returns to the
architect's conversation for revision; after three returned runs for that
document in the conversation, automatic publication stops and the architect
must revise its plan. Other policies, and a project whose
`approvals.integration` is not automatic, retain your confirmation and the write
into your checkout — see [writing a
document from a conversation](artifacts.md#writing-a-document-from-a-conversation).
An invariant is the exception and stays yours to record with `yoyo invariant`.
Changes other roles proposed against its documents are carried into its
conversation for it to argue, the same way the Lead Product Manager hears
proposals against the brief and the goals.

Each role is also given the documents it answers for. The architect gets the
designs, the invariants, and the decision records alongside the specifications;
the development manager, developer, and reviewer get the designs and the
invariants; the Lead Product Manager gets none of them, which is the same decision
read the other way — intent is what it reasons from, and the implementation must
not be able to argue about what the product is for. A management role can also
read one named path on request, the designs included, and what keeps that
boundary is the label every such read arrives under rather than the set of
documents it is given: description of the implementation, never intent.

### Reading the repository at a recorded commit

The three management roles — Lead Product Manager, architect, development manager —
and the program manager can have the harness read the repository for them. It is the same arrangement
the tracker has: the role names a path in a bounded block, the harness performs
the read, records it, tells you, and hands the content back as evidence before
the reply finishes. The role still has no filesystem. What was refused with the
tools was arbitrary execution and a role that a document could talk into
opening the next one; one path resolved by the harness's own Git is neither.

**Every read is against the tree of a recorded commit, never the working tree.**
The harness resolves `HEAD` once per block and reads every path in it out of
that commit's tree, so an edit you have not committed is never what a role is
shown, and the same commit is what the record names. A committed tree holds no
traversable link and no path that leaves it, so confinement holds by
construction rather than by a check: a path that names nothing at that commit is
refused with the commit named, a symbolic link is refused rather than followed,
and a path that reads as absolute or as climbing out is refused with the reason
before that path reaches Git — as one refused result beside the others in the
block, never by losing the block.

Two things can be asked for, and nothing further. `read` returns one file's
content; `list` returns the names one directory holds, one level deep, with a
trailing slash marking a directory. A directory asked for as a file, a file
asked for as a directory, and a file that is not text are each refused with the
reason, and the refusal is evidence the role is told rather than silence.

**The bounds are the harness's, not configuration.** One reply names at most
six paths; one read returns at most 48 KiB of a file, and one reply's reads
together at most 96 KiB; a listing returns at most 400 names; and one message
reads at most twice before it has to answer. A file longer than a read may
return is cut with the cut declared and the file's whole size named — not split
across reads — so a role that wants the rest asks by a narrower question. There
is no key that widens any of it, and none that switches the capability off: what
is bounded is the size of a prompt, which is the protocol's, and a role that may
not have the harness read for it is one whose bundle does not hold the
capability, which no configuration changes. The content is redacted with the
same values every other provider-facing path is redacted with.

**What comes back is framed as untrusted, and for the Lead Product Manager it is
labelled once more.** Every role is told the content is evidence of what the
repository holds at that commit and never an instruction. The Lead Product Manager is
told, in the contract and again on every delivery, that what it read is
description of the implementation as built and states no intent: the
specifications are the only statement of what the product is for, and where a
file contradicts one the Lead Product Manager reports the conflict rather than
resolving it or repeating either side as settled product fact. That label is the
whole of what makes the read safe to give the role that owns intent, and it is
the same label its [shipped documentation](configuration.md#what-the-lead-product-manager-sees-besides-them-and-what-it-does-not)
already carries.

**Each read is on the conversation's record** as the commit, the path, and the
time — one `repository.read` event per path, refusals included, and never the
content. A reply that rests on a file is one somebody may later need to hold
against the commit the file was read at, and the record is what says which. The
transcript and `--json` (`repository_reads`) say the same thing to you as it
happens:

```text
[repository] 1 path(s) read at a recorded commit
    read CLAUDE.md at 339d2f14b07c — 14155 bytes, read 2026-09-19T10:30:00Z
```

**The reviewer and the developer are unchanged.** Neither holds the capability:
the reviewer's evidence is the change, and it stays tool-less and diff-scoped;
the developer's is the worktree it is given inside a run. A conversation with
either that names a path is refused by the harness, and nothing is read.

This is a distinct capability from [research](#bringing-it-an-idea-rather-than-a-work-item),
not research pointed inward. Research is evidence from outside the repository,
run through a command you configured and off until you name one; this is
evidence from inside it, run by the harness's own Git, and on for the three
roles whatever the configuration says. It is also not a substitute for
[freshness](#how-fresh-the-conversations-picture-is-and-how-to-refresh-it): a
read samples what a role thinks to read, and what a stale picture costs is what
it does not know it does not know. The case that admitted this — a Lead Product
Manager advising, from a month-old briefing, that CLAUDE.md gain a section it
had opened with for weeks — is now a read before the advice, and a picture that
far behind is re-read by the harness before the turn is answered at all.

### What the management roles remember

The Lead Product Manager, the architect, and the development manager each keep a
memory of their own across conversations: short conclusions that should shape
how they work next time — how you read a reply, what this project's checks tend
to do, a mistake not to repeat, where a piece of work they are carrying stands.

**Every turn opens with what the agent remembers**, under "What you remember",
labelled as its own earlier conclusions rather than as evidence or instruction:
the canonical documents, the tracker, and what you say now all outrank a memory,
and the role is told that a memory they contradict is out of date. The newest
memories are carried up to 16 KiB and the rest are named as left out rather than
dropped silently. What an agent's side threads concluded still arrives in its own
block, as before, and is not repeated here.

**A turn records what it learned** by ending its reply with a `yoyodyne-memory`
block: `remember` records a memory or a new revision of one, `retire` takes one
out of the briefing and says why it stopped being true, and `compact` folds a
memory's earlier revisions into a shorter one that names them. Each write goes
through the same `agent-context` actions a side thread's merge does, so it is
redacted before it reaches the disk, held to the store's budget (at most four
writes a reply, 8 KiB each, 32 KiB for everything the agent knows), and recorded
with the conversation, turn, provider, model, account, and configuration that
wrote it. A write the store refuses — past the budget, compacting a revision that
does not exist — is told to the role on its next turn rather than failing the
reply. You are told what was written as it happens, in the transcript and in
`--json` (`memories`):

```text
the Lead Product Manager recorded memory "checks-are-slow" (revision 1)
```

**The conversation's record says a write happened and never what it said.** Each
write is one `memory.requested` event and then one `memory.recorded` or
`memory.failed`, carrying the memory's name and revision number; the text lives
only in the memory store, under the state root at
`projects/<product>/state/memory/<agent>.memory.jsonl`, because a copy in the
conversation record would be a second store.

**Before compacting a provider session, the harness gives every role that keeps
memory one turn to save what it has learned.** The message says compaction is
next and what the new session will keep: the current repository and tracker
picture, recorded memories, and the newest 80 messages within 256 KiB. The role
records its conclusions through its usual `yoyodyne-memory` block, or replies
"Nothing to save." with no block. The harness records the memory writes before
rebuilding the session, and the conversation log says the save turn happened,
how many writes it recorded, and whether the role had nothing to save. The
transcript and `--json` (`compaction_saves`) say the same thing. If the provider
refuses the save request for size, the harness sets aside the unusable session
and retries the save once from the durable conversation record, with older
messages shortened. The retry keeps recorded memories and the save instructions;
it does not replay the waiting message. Its memory writes are recorded before
the waiting reply continues. Conclusions held only in the refused native
session cannot be recovered from that record.

A save turn or memory write that still fails leaves the waiting message
unanswered. The old session stays in place unless a size refusal already set
it aside; in that case the next turn rebuilds from the record. A memory-budget
refusal refuses only the write, reports the sizes and largest memories to the
role on its next turn, and lets the rebuild and waiting reply continue. Program
managers receive this turn too; developers and reviewers, which keep no memory,
do not.

**`yoyo agent memory <name>` reads that history as text.** Every memory the
agent holds is listed with all of its revisions, newest first, each quoted as it
was written and followed by the invocation that wrote it — the conversation or
run and its turn, the provider, the model asked for and the one that answered,
the account, and the configuration — with the records it cites and, for a
compaction, the revisions it folded together. A retired memory says `(retired)`
beside its name and the revision that retired it says so, so nothing about its
state rests on colour or position. On a terminal the listing is dressed as
Markdown by the same renderer a reply is; under `NO_COLOR`, on a `dumb`
terminal, or into a pipe it is the same text undressed. `--json` carries the
store's records themselves. An agent with none gets one sentence saying so, a
line of the store that would not decode is listed under its own heading beside
what could be read, and a store that cannot be read at all is named as
unreadable rather than shown as empty.

**The developer and the reviewer are unchanged.** Neither keeps a memory: their
turns carry no memory briefing, their contracts do not describe the block, and a
reply from either that carries one is refused whole, with nothing recorded. Asked
what either remembers, `yoyo agent memory` says it keeps none.

### A program manager's lane

A [program manager](designs/program-manager.md) instance owns one tracker label,
its `lane`, and every tracker write it holds is confined to it. The confinement
is in the harness's authority table rather than in anything the role is sent,
and it is read from the tracker as each action runs.

- **A creation carries the lane label in the write that admits it**, whether or
  not the reply named it, with any other labels beside it. Its notes record the
  lane and the instance. A parent it names has to carry the lane label too.
- **Every other action is refused unless the item carries the lane label at the
  moment of the act.** A survey that showed the label earlier is not the item as
  it stands, so a listing that has moved does not widen the lane. An item the
  tracker will not describe counts as outside it.
- **The lane label is never removed by its owner.** A `label` removing it is
  refused. Taking an item out of a lane is the Lead Product Manager's or the
  development manager's act.
- **A lane item may be linked to wait on any item.** Linking an item outside the
  lane to wait on a lane item is refused.
- **A reparent needs the item and its new parent both in the lane.**
- **Priority inside the lane is the instance's to set freely.**

A lane admission is an admission, so it goes through
[`approvals.work_items`](configuration.md#what-reaches-the-queue) exactly as the
Lead Product Manager's does. At `human` the creation is not admitted. It is put to
you as a proposal with the lane named on the card, and approving it creates the
item in the lane. A proposal carries the title, description, goal, relevant goals, parent, and
class. Anything else the creation named, such as its priority, is listed in the
result for the instance to set once the item is admitted. At `automatic` it is
admitted against a goal you approved, through the same duplicate-admission guard
and the same done-condition check. A goal whose document nobody approved puts it
to you as a proposal instead. Close and retire are refused everywhere. An
instance configured with no lane has nothing inside one, so every one of its
tracker writes is refused.

### A program manager's lane report

A [program manager](designs/program-manager.md) keeps one report on its lane: a
short summary of how the lane is going, what remains, and what is blocking it.
Nobody is sent it. The operator and the other roles read it when they choose.

**It is rewritten whole by one block.** A program manager's reply ends with a
`yoyodyne-lane-report` block, which carries three fields and nothing else:

```text
{"summary":"where the lane stands","remaining":["what is still to do"],
 "blockers":[{"what":"what is blocked","waiting_on":"product-manager","cites":"report-7"}]}
```

`remaining` is a list, and so is `blockers`. An empty list says nothing
remains, or nothing is blocking. Each blocker names `what` is blocked,
`waiting_on` (the mover it waits for), and `cites` (the identifier of the
request, report, amendment, or exchange the instance raised about it). The
mover comes from the read model's own vocabulary, narrowed to the movers a lane
can wait on: `operator`, `product-manager`, `development-manager`, `architect`,
`harness`, `forge`, or `provider`. What each citation resolves to decides
whether the instance reads as blocked: only an open record of its own does, and
the rest are shown as claims with the reason ([the instance's
status](operations.md#where-the-harness-stands-the-four-lines)).

The report is kept under the state root at
`projects/<product>/state/program-managers/<agent>/report.json`. Beside it,
`history.jsonl` holds the last fifty versions, oldest first. Each version is
numbered, and each is stamped with the conversation and turn that wrote it.
Where a recurring task's pass woke that turn, the stamp also names the pass, as
the task and its firing number (`factory-watch#12`). A fifty-first version drops
the oldest. The numbering keeps counting. The report is never in the repository,
which is public, and never in the memory store. The report is bounded at 16 KiB
and redacted before it is written, with the same values every durable record is
redacted against. The dashboard's program managers section opens the current
version on a card and writes nothing
([opening a program manager's report](operations.md#opening-a-program-managers-report)).

**A malformed block is refused whole, and the turn goes on.** A block over the
bound, a block missing a field, a block with a field the report does not have,
and a blocker waiting on anybody outside those seven movers are all refused.
Nothing is written, so the report before it still stands. The refusal is
recorded in the conversation's log as `lane_report.refused`. The operator is
told on the transcript, and `--json` carries it under `lane_report`. The program
manager is told why on its next turn. Where a recurring task's pass woke the
turn, the refusal is written onto the pass's record, which is what
[`yoyo sweeps`](operations.md#reading-what-the-recurring-tasks-found) reads. A block that lands is recorded as `lane_report.recorded`, with
its version number. Neither event copies the report's text.

**Only the program manager may write one.** Writing a lane report is
`lane-report.write`, which only the program manager's bundle holds. The block
in any other role's reply is refused like any block a role has no authority
for: the turn fails, and nothing is written. No other contract describes the
block.

### Roles asking each other things

A question one role cannot answer itself used to cost you one of two things:
relaying it between two conversations by hand, or a whole work-item cycle. Now
the role asks directly and the harness carries it. The Lead Product Manager asking the
architect *what does this goal cost, and what am I missing?* before it orders the
backlog, and the architect asking the Lead Product Manager *if we sacrifice some
performance, is that an unacceptable trade-off from the user's standpoint?*
before it settles a design, are the two cases it exists for. They are one
mechanism with the parties swapped, and everything below holds identically in
both directions.

Three things are true of every exchange, and each is enforced rather than asked
for:

- **It is durable and visible.** An exchange is a record of its own under the
  product's state, written before each round is taken, and `yoyo exchange list`
  and `yoyo exchange show <id>` read the whole thread. Two roles cannot say
  anything to each other that you cannot read afterwards, which is the
  no-side-conversations property traceability implies. The conversation that
  asked tells you at the time as well, and where the project reports to Slack
  each round arrives in a thread of its own.
- **It is judgment-only.** Both halves are toolless: the role being asked has no
  filesystem, no commands, and nothing to check anything against, so an ask moves
  opinion and never evidence. An answer reaching for any harness block at all is
  refused whole and the asker is told its question went unanswered. Work that
  needs something verified is still commissioned as bounded developer work.
- **It is decisionless.** No authority moves through an ask. Nothing an answering
  role says admits work, orders a backlog, edits a document, or resolves
  anything, and decisions still land as amendments, proposals, and directives.

```sh
./bin/yoyo exchange list           # every exchange, the open ones first, with what each cost
./bin/yoyo exchange show <id>      # one exchange in full: every question and every answer
./bin/yoyo exchange list --json    # the records themselves, for a script
```

The channel runs between the three roles that hold judgement about the product —
the Lead Product Manager, the architect, and the development manager — and the
[program manager](designs/program-manager.md), whose requests to the architect
and the development manager are asks rather than acts. Each end is its own
capability, `exchange.ask` to put a question and `exchange.answer` to be put one,
and every role on the channel holds both. The developer
and the reviewer are not on it: their judgement is exercised inside a run,
against a change and a worktree, and an opinion from either with none of that in
front of it is worth less than the round it would cost.

The answer comes back inside the reply you were already waiting for, as a further
round of it, and the asking role then either asks again in the same thread or
closes it with what it took from the exchange. Closing is the ordinary ending.

**Every exchange is opened with a hard limit on rounds**, which is
[`exchange.max_rounds`](configuration.md#how-long-one-role-may-ask-another) and defaults
to ten. The limit is copied onto the exchange as it opens and is durable with it,
so neither a process dying nor an edit to the configuration lengthens a thread
that is already running long. Reaching it is not a silent cutoff: the exchange
closes as unresolved and is escalated to you as a report at warning severity,
naming what the two roles did not settle. That is the one way this fails — two
judgement models deferring to each other politely for ever — and a limit that
ended the conversation quietly would hide exactly the case worth seeing.

**A thread one process died in the middle of is picked up by the next sweep
rather than left dangling.** A round is written down before the answering role is
invoked, so a harness killed between the two leaves a round asked and never
answered — which looks identical, in the record, to a round somebody is taking
right now. What tells them apart is a lease: one exchange takes its rounds one at
a time, held by the process taking them, and the operating system drops it when
that process exits. So [`yoyo reconcile`](operations.md#recovering-interrupted-runs)
finds the exchange free, closes the dead round saying which process was carrying
it, and leaves the thread open with its remaining rounds. The round stays spent,
because it was: a cap a crash could reset is not a cap. The same sweep closes a
thread that spent every round it was given and was never asked again — which is
where the cap would otherwise never fire, since it fires when somebody asks past
it — and tells you about that one exactly as an exhausted exchange is always
reported. The sweep asks nobody anything: recovering from a lost process is never
a reason to start a round nobody asked for.

**A bound on how many exchanges have a round open at once is derived and not yet
enforced.** The sweep works out, from the records and the leases, how many
threads the product could carry — four, not configurable — and which ones are
past it; that reading is in `yoyo reconcile --json` under `supervision`, marked
`queued`. **Nothing acts on it today.** The path that actually takes a round is
the conversation's own ask, and it does not consult the bound, so five roles
asking at once produce five open rounds and none of them waits. The reading
arrives ahead of the thing that would use it: the bound becomes real when the
half of the loop that wakes roles and takes rounds on their behalf lands, and
until then it is a number to look at rather than a limit you are running under.

It is derived early because the case nobody designs is a morning on which several
roles each decide they need one more judgment before they can proceed, each of
them a provider invocation beside the runs already being paid for — and seeing
that in the record is what tells you whether the bound is set anywhere near
right before anything is held back by it.

**A question can name what it was asked against.** Where a role's question rests
on something that can be amended underneath it — a goal, a design, a work item —
the ask may name it and the revision that was read. What that buys is that a
question answered against wording that has since changed can be found afterwards
instead of being read as though nothing had moved. If it is ever reported, it is
reported and nothing else: the exchange is not stopped, closed, or held back,
because a change to a goal's wording is frequently not a change to the question,
for the reason
[staleness reports rather than decides](artifacts.md#what-a-change-upstream-leaves-stale)
everywhere else. What it produces is a reason to tell the role that asked.

**What is recorded today is what a question rests on; the comparison is not yet
wired.** `yoyo reconcile` derives staleness from the references and whatever it
has been told the current revisions are, and it is told none, so every reference
comes back in `--json` named as unjudged rather than as unmoved — which is the
honest answer and not a finding about the goal. Silence is not evidence that
something held still, so a reference nothing current is known about is never
counted as one that has not moved. Feeding it the revisions is what turns this
from a record into a report.

**What an exchange cost is reported beside the rounds it took**, wherever one is
read. Rounds alone say how long a conversation went on and cost alone says what
it came to; the question you actually have — was that worth it — is answerable
only from the two together.

It is also in what the harness spent altogether, rather than only beside its own
rounds. [`yoyo status --spend`](operations.md#following-a-run-a-conversation-or-a-branch-review)
counts each round on the day it was answered, alongside the runs, conversations,
and branch reviews of that day, and [`yoyo cost`](reporting.md#what-the-work-cost)
carries the exchanges into its total on a row of their own rather than into any
item's price. What the record holds is the product, the repository, the two roles,
and the conversation the asker spoke from — nothing that says which piece of work
the question was for — and the conversation is no stand-in for one, since a role
stays in the same conversation across everything it discusses. That is also why
the membership above matters here: the roles on this channel own documents,
queues, or a lane, and the two that work inside a run are not on it.

The development manager is given one more thing: the **triage docket**, the work
that has stopped moving. It reaches that conversation the way the backlog
reaches the Lead Product Manager's — carried in the context rather than by you
noticing something went quiet. What she is shown is a window onto it, described
below: the live entries, one per stopped run, every stoppage nobody has decided
before any she has, oldest first with anything critical ahead, and a count of
what did not fit.

**A scheduled sweep carries it too.** The context is built when her conversation
opens, and a [recurring task](configuration.md#recurring-tasks)'s pass resumes
that conversation rather than opening it, so the docket she held was the one
from whenever the conversation began. Stoppages otherwise reached her one per
delivery, and a pass between deliveries saw none: on 2026-09-25 three of her
sweeps ran after an approved change stopped waiting on her, and decided nothing
about it or the two that stopped after it. So every firing of a development
manager's task — a summons from the intake brake included — builds the docket
again and puts it in the message that wakes her, in the same listing her
conversation carries, whether or not anything was delivered since her last turn.

Each continuation turn carries the next slice in that order, with entries
already delivered on the pass left out. Decisions and closed work are read
again between turns. A pass cannot end complete while live entries remain
undelivered; if its turn bound or a provider refusal ends it first, its record
counts those entries and names the oldest. The next pass starts with those
unread entries before showing entries an earlier pass already delivered.

**What she is shown is a window onto the docket, not the docket.** The docket
is a log that only grows, and a conversation has a budget. So each picture she
is given, and each pass's message, lists at most 25 entries, within a fixed
size, chosen like this. Only live entries are in it: entries whose work item is
still open, and that nobody has decided about, whose decision has lapsed, or
whose decision the harness was stopped carrying out. Whether an item is closed
is read from the tracker as the window is built. If the tracker cannot be read,
nothing is left out, and the window says so. A stopped run is listed once, with
its other docketings beneath it, and says how long it has waited since it was
first docketed. A scheduled pass first lists the live entries its earlier slices
or a previous pass did not reach, in their saved order. Among the remaining
entries, every stoppage nobody has decided comes before any entry whose
decision is recorded, however the harness was stopped carrying that decision
out. Among the undecided, critical entries come first: an item a role raised as
unmeetable, and a Lead Product Manager's decision about a run still in flight.
After them come the oldest stoppages. The decided entries come last, where
there is room: first those stopped by a gate that will not clear on its own,
then those waiting on a gate that will. The fixed size is shared among the
entries listed, so the window lists 25 whenever the docket holds more than 25.
An entry longer than its share is cut, and says where. The window says how many
live entries it did not list, how many of those are already decided, how long
the oldest of those has waited, and how many entries it left out because their
work is closed. It
walks the docket the way
[the report pile](reporting.md#who-reads-them-and-what-became-of-each-one) is
walked. Where the last window stopped is recorded per product, beside the
docket, and the next window starts past it and goes back to the oldest when it
reaches the end. Her conversation and her task's passes share that position, so
an entry one window had no room for is the first thing the next one lists,
whether it comes from a pass, a refresh, or a new conversation. On 2026-09-25
the window listed the newest entries on the log instead: eleven of them, mostly
on closed items. Twelve stopped runs waited behind them for between seven and
thirty-six days. On 2026-09-26 the window held four entries, all already decided
and waiting on a gate, while 29 stoppages waited on her decision:
[the diagnosis](diagnoses/yoyodyne-ifd-428-38-decided-entries-filled-the-docket-window.md)
has the details.

Four things put an item on it. A run stops with
its change still there, an approved publication does not finish, dispatch
declines to start an item whose stated prerequisites the tree does not meet, and
a developer or a reviewer says the item cannot be met as it stands.

The last is the only entry that is a judgement rather than an observation, and
the only one raised before anything has been spent failing. Either role can say
it in the round it reached — the developer as a landing outcome, the reviewer as
a verdict — and the run ends there with nothing integrated and the item parked
until its owner amends it and releases it, or she retires the raise. What she is
being asked for is a decision about the item rather than about a change: replan,
park, resequence, or redirect, and then what becomes of the raising run's change
once the item is amended — [deciding what becomes of stopped
work](#deciding-what-becomes-of-stopped-work) says how a raise is decided.
`docs/work.md` says what each role writes to raise one.

A stopped run reaches the docket by either of two routes. Most of them end on a
durable blocker, which dockets itself as the run stops, or which a `yoyo
reconcile` sweep records and dockets for a run whose process died. The rest died
before anything could record one — a push the remote refused, a backend that
broke mid-attempt — and those are docketed on the reason they gave for dying,
once the run is terminal and the change it made is still on its branch or in its
worktree. The two are one class because they are one fact to whoever reads them:
the work is still there and nothing is going to pick it up on its own.

A death is docketed as it happens and is never found later by a scan, which is
the one place the two routes differ. A blocker is a durable classification the
sweep can re-derive from any record that carries one; a death is a shape every
terminal failed run with a surviving branch has, so re-deriving it would fill
this section with settled work from months back and push the stoppages you have
to decide about off the end of it.

An unfinished publication is docketed too: one the harness already recorded as
outstanding — a merge the forge dropped, or one it performed that could not be
confirmed — and one that has simply been sitting unmerged past
`triage.stuck_merge_age`. A merge the forge is still holding is never docketed
as only that: its entry carries the checks the last reconcile sweep read — which
failed and on what files, and how far behind its target the head is — or says
that no sweep has read them yet. A red one rarely stays queued long enough to
be docketed at all, because the sweep brings a head that fell behind its target
up to date and hands back one whose own change fails
([operations](operations.md#recovering-interrupted-runs)).

Each entry carries the evidence rather than a summary of it: the blocker in the
words it was recorded in — or, for a death that recorded none, the failure the
run gave, labelled as what it is so nobody goes to the item for words nobody
wrote there — the reviewer's own findings, the check that was
failing and what it printed, the branch and worktree that were preserved and
whether they still exist, the developer session the run was working in, what the
forge says about the merge, and the counters
saying how many review rounds the item has accumulated against the configured
cap and what a repair grant would be worth. A decision made without those last
ones is a decision the cap then contradicts. The session is on the entry because
a decision turns on it: a repair carries it on, and a re-run discards it along
with whatever it left uncommitted — which is most of what there is to decide
about a run [a stall stopped](#deciding-what-becomes-of-stopped-work), where the
entry says outright that the session is resumable and what continuing it costs.

It carries what triage has already decided about the item too, joined from the
durable record the guards spend and refuse against at the moment the docket is
read rather than frozen into the entry when the work stopped: the repair grants,
re-runs, and merge re-arms recorded beside the caps that refuse the next one,
what a grant came to and whether the round cap cut it down, what the item now
stands committed to, and which of those decisions the harness has carried out.
Where a further decision would be refused, the entry says so and says which
budget refuses it. Where the harness tried to carry a decision out and a gate
stopped it, the entry says that too — which gate, what it said, and what would
clear it — so a decision recorded days ago and still not fired is visible on the
entry rather than only in the silence. That is what stops a decision already recorded from reading
as an entry nobody has looked at — the reading that had one authorized recovery
decided a second time, and then paid for by a round-trip on every docket after
it.

An entry states that something stopped; it decides nothing, and nothing decides
it for the development manager. Docketing is keyed to what stopped, so a run
that dockets its own ending and a sweep that settles it afterwards produce one
entry between them, and a run that is merely parked — waiting out a usage limit,
held by a directive, or paused by you — is never docketed at all, because it is
owed a continuation rather than a decision.

**An entry is one per stopped run.** One run can be docketed more than once on
the same stoppage — it stops on a blocker, and a later scan finds the
publication it left sitting unmerged behind it; a role's escalation and the
stoppage it ended the run on — and those are one question. So the docket she
reads holds one live entry for the run: the account recorded last, with every
other open docketing of that run kept beneath it whole — its blocker, findings,
failing check, branch and worktree, session, and what was decided about it,
everything the entry would carry listed on its own — rather than listed beside
it. One decision settles the
whole entry, including what is folded beneath it. The log keeps every record, and
the fold is made wherever the docket is built, so the sweep folds the repeats
already standing the first time it runs. On 2026-09-25 the docket held 62 open
entries for 38 items, yoyodyne-ifd.362 six times. A different run of the same
item is different stopped work and is its own entry.

**A decision closes the entry it settled**, which is the other half of that
lifecycle: an entry is created where work stops and closed where somebody
decides. The docket is rebuilt from the durable records at every scan, so an
entry nothing closed came back on every docket after it — and four of the seven
decisions that settle a stoppage spend no budget, so nothing the harness reads could tell a stoppage
somebody had settled from one nobody had looked at. The decision is recorded
beside the entry rather than over it: the entry stays on the log, which is what
stops the same stoppage being docketed a second time from the same records, and
what a reader is shown is the join of the two. So the docket in that
conversation is the stoppages nobody has decided about, and a settled one is
neither listed there nor delivered again.

**An entry closes with its item.** Every entry is about a work item, and a
closed item asks nobody anything, so when the item is closed or retired the
harness closes the entries standing for it, recording the decision
`item-closed`, who closed the item and how, and when. That is every class but
one: an unfinished publication asks about a merge the forge holds rather than
about the item, which closes as its change is integrated while the merge can
still be dropped or stuck afterwards. So that entry stays until the publication
settles or the development manager decides it, `rearm` included. A run whose
change lands, and the Lead Product Manager's `close` and `retire`, close the item's
entries in the same act, and the Lead Product Manager is told how many. Every `yoyo
reconcile` sweep lists the items the tracker holds as closed and closes any
entry still standing for one. That catches an item closed by some other hand,
the merge a sweep settles among them, and the entries left standing from before
this existed. The sweep reports how many it closed (`closed_with_item` in
`--json`). Nothing did this
before. On 2026-09-25, 125 of the docket's 187 open entries were for closed
items: stoppages settled by a re-run that landed, or by the item closing for
any other reason, that stayed on the docket and crowded live ones out of the
bounded listing the development manager is shown.

**What closing does not do is silence the same work stopping again.** A repair
continues the run that stopped, so a repaired run that dies again is a fresh
stoppage under the identifier the settled entry carries — and it is docketed,
because what the decision settled was the stoppage rather than the run. The scan
compares the two: work that stopped after the decision about it goes back on the
docket with the blocker it stopped on this time — and is delivered into her
conversation by the watching pass as the first stoppage was, since the record of
that delivery is about the stoppage it delivered and not the run — and work
nothing has happened to since stays settled. **A decision to `wait` is the one that lapses rather than
settling anything**: it says nothing is to be done yet — the forge still has the
merge, or the stopped run is waiting on something that moves without her — so
the entry comes back once it has been sitting there for another
[`triage.stuck_merge_age`](configuration.md#triage-thresholds), carrying what
was decided last time so whoever gets it knows they have seen it. Until it comes
back it is still listed, last, after every entry nobody has decided, saying until
when, and the line counting what the docket could not list says how many of
those are waits; once the wait runs out it is among the undecided again, at the
age it had when it first stopped. **A repair or
a re-run the harness tried to carry out and could not comes back the same way**:
the entry is listed again carrying the decision she made and the gate that
stopped it — which gate, what it said, and what would clear it — so a decision
she recorded that is not happening is read as exactly that rather than as a
stoppage nobody has looked at. What it asks of her is the gate; deciding the
same stoppage again is what the budgets refuse. One waiting on a gate that
clears by itself — your pause, your intake hold, a full harness — is listed
worded as waiting, and it leaves the docket again the moment the decision
fires, whichever hand fires it. [Recording a decision is what causes
it](#deciding-what-becomes-of-stopped-work) says what fires one.

Finding a publication nobody merged is a scan rather than an event, because
nothing happening is not something anything can be present for. Three things
scan: `yoyo reconcile`, opening a development manager conversation, and a
firing of one of her recurring tasks. Only the last runs on a schedule, and only
where a project configured one, so the configured age is a floor rather than a
promise about when the entry appears.

### Deciding what becomes of stopped work

An entry decides nothing, and the development manager is the role that does. It
records one decision per entry, on the work item, through a `triage` action that
names the run the entry is about: `repair` hands the item another bounded go at
the change it has, `rerun` runs it again from the start, `rescope` splits out
what was refused as out of scope, `rearm` repeats a merge the forge dropped —
or arms one nothing ever asked the forge for, which is also answered by
`rerun` — `wait` says nothing is to be done yet, `escalate` hands it to you, and
`retire-raise` ends a raise its owner's amendment made moot. One more,
`stop`, is about a run that has not stopped yet: it names a run still in flight
whose work is superseded, narrowed, or mis-launched, with the item doing the work
instead as `superseded_by` where there is one, and the harness asks that run to
stop as it is recorded — the request your `/stop` writes, made in her name. The
run ends cancelled at its next boundary with its change preserved and its slot
freed, and it is docketed already closed by her decision, so it never reaches her
as a question. The entry says the item is superseded and by what, and the item is
not pulled again while the stop is its latest decision and the stopped run's
change is preserved. A `stop` naming a run that has already ended is refused.
**A stop decides one stoppage**, the one it causes. A run that passes its last
boundary before it reads the request, and then stops for another reason — a
failed review, say — reached a stoppage the stop never reached: it is docketed as
an undecided entry naming both the stop she asked for and what actually stopped
the run, and it waits on her decision like any other. `proceed` is the other
answer about a run in flight: it lets the run finish, asks nothing of it, and
records that she looked. Both answer the one docket entry that is about a run
still going — the Lead Product Manager's decision that its item is superseded,
narrowed, or to be retired, which that role records with an `inflight` action and
the harness dockets for her at once, ahead of the stoppages, with where the run
stands; either decision closes it, and `proceed` is refused on a run that has
already ended, as a stop is. The decision
lands in the item's notes, so the next reader of a run that stopped finds the
reasoning beside the evidence rather than deciding it a second time, and it
closes the entry it settled — a repair, a re-run, or a re-scope closes the
run's own entries, whichever of the stopped run, the run that died before it
claimed, and the escalation a role raised from it the run carries, and a re-run
closes the unfinished publication's entry too, and retiring a raise closes the
raise's; a re-arm closes the unfinished publication's entry for good; a wait
closes any of them, the run's own entries and the publication's alike, only
until the entry has been sitting there as long again; and an escalation closes
all of them, because an escalated item is waiting on you and none of it is hers
to decide until you answer. Those are the entries a decision answers; what it
closes is the run's whole live entry, so a decision that answers any of a run's
open entries closes the rest of them with it, and a re-run decided on a stopped
run settles the stuck publication folded beneath it too. Every decision but `wait` closes its entry for good,
and what puts one of those back on the docket is the same work stopping again
rather than anything about the decision. The two entries that name no run are
closed by no decision of hers, because a decision names a run and neither has
one. An item the tree is not ready for is closed by the harness instead: every
pull reads the item again, and takes the entry off once the item asks for nothing
the tree lacks, has left the backlog, or has been restated — in which case it is
docketed again in its new words. An attempt that never became a run is closed by
nothing of hers either; like every entry, it closes when its item does.

**An item a role raised as unmeetable is decided with two of these, and a
repair is not one of them.** The run that raised it did not stop: it succeeded
at saying the item cannot be met as it stands, parked the item, and left what it
had written on its branch. There is no stopped run for a repair to continue, so
`repair` on a raise is refused before anything is spent, in a sentence naming
the two decisions that apply. What ends the raise is the item's owner: the Lead
Product Manager amends the item and releases the raise's parking, and that
release also clears the blocked status left while the raise stood — the
escalation that followed it, most often — so a pull can select the item. What
becomes of the raising run's change is the development manager's. `rerun` starts
the item again from that change where its branch still stands: the fresh
worktree is cut from the target branch as every worktree is, and what the
raising run's branch carries past the target is applied to it, uncommitted,
before the developer is invoked, so the fresh run's change is judged whole
against the target and the run records the commit it started from. It is
carried out like any re-run, by the pass or by `yoyo triage rerun`, once the
owner has released the parking — asked for while the raise's parking stands, it
is refused with nothing spent, and asking again after the release carries out
the same decision — and the item is held from a fresh pull until it starts. A
change that no longer merges with the target fails that run rather than being
half-applied. `retire-raise` is for when the amendment makes that change moot:
it spends nothing, is carried out as it is recorded, lifts the parking the raise
placed and no other, clears the blocked status the raise left, closes the
raise's entry, and the item starts from the target branch at its next pull. A
pull that reaches a released item before she has decided either starts it from
the target branch too. On 2026-09-27 yoyodyne-ifd.437.13 cost a repair grant, a
re-run, and two escalations because none of this existed: the repair and the
re-run were each refused at carry-out for want of a stopped run, and once its
owner had amended and released it the item still read blocked.

**It also lands as a record the harness reads**: the decision, the stoppage it
settles, the reasoning verbatim, and where it was recorded, on the item's durable
triage record. That is what the verbs below carry out, and why a decision
requires its `reason`: before it existed, `yoyo triage rerun` took the reasoning
as a `--reason` flag and recorded it as the development manager's — an
attribution nobody in that role wrote.

**The run a decision names has to be that item's own stopped work.** The harness
reads the run's record and refuses a decision whose run was made for a different
item, before any budget is spent and before anything is written down. That is
weaker than asking whether the run is on the docket — an entry may have been cut
from a bounded listing, and refusing a decision for that would refuse exactly the
oldest stoppages nobody has got to yet — and it catches what a docket of several
entries actually produces: two of them read across each other, putting each
decision's reasoning onto the other item, where it reads as a settled judgement
about a change that item never made. A run the harness has no record of is
refused the same way, since nothing then says the decision is about that item's
stoppage at all.

Six of the eight the harness holds to more than a note. **A repair, a re-run,
and a re-arm each spend the item's durable budget as they are recorded**, and are
refused once it is gone — the refusal names every budget that refused, what each
has spent, and the ceiling that would permit the decision.
A repair and a re-run are each once per item, and a re-arm once per publication
— a second of any of them is an escalation rather than a larger budget — and past the
[review-round cap](configuration.md#what-one-work-item-has-been-given) even
the first is refused.

**The eighth decision is `cross`, and it is what she does about that refusal
without waking you.** It raises the budget the refusal named to exactly the
ceiling that refusal quoted — one more than the item has spent against it — it
takes the reason it is being crossed for, and it is bounded to five per item;
past those five, or for any ceiling beyond that, the cap is yours again and
the refusal says so. Each crossing is recorded on the item beside the cap and the
crossing number, and reported to you in the channel at `warning` severity as it
happens — a veto by reading rather than a request, because every override
recorded in the week to 2026-09-06 was granted, most within minutes, and the step
through you was latency rather than judgement. A crossing carrying no
justification is refused outright, which is the condition the delegation rests
on.

What is still yours is
[`yoyo triage override`](configuration.md#crossing-a-cap-the-operator-decides-to-cross),
in your name and with your reason: any ceiling, any budget, and lifting one
entirely. **Nothing crosses a cap except a recorded crossing**, and the refusal
prints both — her own, with the budget already in it, and your command with the
item and the ceiling filled in — because naming the remedy without naming the
verb sent two of these overrides into the item's notes instead, where no guard
reads them and where the resubmitted decision met the identical refusal.
Where both of a decision's budgets are spent it prints one of each and
says both are needed, because crossing one and meeting the other is what cost two
override sittings minutes apart on each of two items. **A merge
re-arm is bounded once per publication** rather than by the rounds, because it
buys no round at all and because what it repeats is one merge request the
reviewer's verdict already authorized: an item that published three times has
three separate budgets, and a second drop of one publication is an escalation
rather than another re-arm. **An escalation is a durable blocker on the item and a report at
`warning` severity or above**, in the same reply: the item itself says it is
waiting on a person, and the report reaches [the pile you
read](reporting.md#what-agents-report-and-where-it-reaches-you). Prose alone is not an escalation, and the
harness refuses one carrying no such report rather than blocking an item you
were never told about. The decision itself is
[a finding that needs your hand](operations.md#where-a-finding-that-needs-your-hand-goes):
it is said to you directly once, and named under `Needs a human` on
`yoyo status` while it stands as the decision on the item's latest stopped run. **A `retire-raise` lifts the raise's own parking and the blocked status it
left, and closes the raise's entry**, as it is recorded, as the paragraph on
raises above says. `rescope` and `wait` are the two that are a note and
nothing else — a re-scope's real work is the child item it creates beside the
note, and a wait asks for nothing at all.

**A decision whose write fails after the budget is spent says so, rather than
saying it changed nothing.** The spend is durable the moment it is recorded and
the note onto the item comes after it, so a tracker that times out in between
leaves an action that half happened — and the result reports it as one: it names
the spend as landed and not to be made again, and says whether the write reached
the item, which it settles by reading the item back and saying what it found or
saying plainly that it could not. What it looks for is what that decision would
have left: the note for the seven that write one, and for an escalation
the blocker itself, since blocking sets the item's status as well as recording
the reason — and an item that was already blocked when the decision was asked for
settles nothing, because that blocker is somebody else's. A crossing is settled
the same way, naming the cap it already moved rather than a spend: the cap is
raised on the durable record before the note is written, and a crossing reported
as having changed nothing is one asked for again, at the cost of another of the
five. A decision that spends
nothing — an escalation, a re-scope, a wait, a retired raise — has no spend to name, and a write of
one that cannot be confirmed is reported the same way rather than as a failure:
it says it did not finish and that what it may have changed is not settled,
because the harness not knowing is not the same claim as nothing having happened. That distinction is not cosmetic: on
2026-09-06 a re-run of yoyodyne-ifd.142 was reported as having changed nothing
while its spend had already landed, and what stopped the same decision being
asked for a second time was the cap refusing it rather than anything anybody was
told.

Recording a decision is not carrying it out, and three of the eight now have an
action that does; a fourth, `retire-raise`, needs none, because it is carried
out as it is recorded. Two are the opposite answers to a run that stopped: `yoyo
triage rerun` starts the item over, and `yoyo triage repair` continues the run
that stopped on the change it already has. The third, `yoyo triage rearm`, is
about a publication rather than a run: it repeats the merge request the forge
dropped. A fourth action, `yoyo triage resume`, carries out no decision at all,
and [the paragraph on it below](#resuming-an-approved-change-the-environment-stopped)
says why there is none to carry out.

Neither of the first two waits on being typed. A watching `yoyo work` session
fires a recorded repair or re-run itself, oldest decision first and as many per
pull as there are developer slots for them — ahead of fresh pulls of any priority
where the stopped run's branch or worktree is still there, and otherwise behind
any ready work of a higher priority than its item
([how work flows](work.md#letting-the-harness-choose-the-work) says why) —
through these same two actions and under every condition
each of them asks — so
recording the decision is what causes it, and the verbs are what fires one *now*
rather than at the next pull. A re-arm is not typed either, whether of a merge
the forge dropped or of a request nothing ever asked the forge to merge — the
pass makes it itself through `yoyo triage rearm`'s own action, on every pull,
outside the developer slots since it is one merge request rather than a run, and
not while your pause or intake hold stands, which is written onto the item as
what it waits on. Every refusal is written onto the item's own triage record and shown on the
docket entry the development manager reads, naming which gate refused and what
would clear it, so a decision that cannot be carried out says so where she is
already looking. A re-run of a run the docket holds no stoppage
of, such as a re-run the harness cancelled on its way out, is carried out as
well, by starting the item again from the target branch. A repair of such a run
cannot be, and its refusal is shown on the item's entries, labelled with the
run, naming the re-run that would apply instead.
A decision no pass has attempted a poll interval after it
was recorded is written there too, as unattempted with why, so none is ever
silently passed over. Before that existed, thirty-three items stood decided and
unfired, some for days, because the only executor was somebody typing one of these
two commands.

`yoyo triage rerun <run-id>` starts a fresh run of the item whose stopped run the
docket entry names — the case where the ground moved under a change that was
never wrong — or whose raise it names, once the item's owner has released it,
starting from the raising run's preserved change. It also takes a run the docket
never held, such as a re-run the harness cancelled on its way out, and starts that
run's item again from the target branch. It takes the run and nothing else: why the run exists is read from
the recorded decision and cites it, so the account is one you can check.
**Your hold on intake applies to it**,
because the harness is the one choosing here and the exemption for an item named
by hand is yours rather than the development manager's.

Four things refuse it. The stopped run has to be really over — terminal, and
still standing on whichever of the two put it on the docket: its blocker, or, for
a run that died before anything recorded one, the change it left behind — or,
for a raise, the raise itself, with the item no longer parked by it. Either
way that is read from the run's own record rather than from the docket entry. A
run the docket never held only has to have ended: it was never docketed because
it left neither a blocker nor a change behind, so asking for one could only
refuse what the development manager decided. A run the harness holds no record
of is refused, naming the docket. One stoppage is re-run once, and a run the
docket never held counts as one, claimed under the key the docket would have
given it. The work item has to be one a run
may start on — open or blocked, with nothing it depends on outstanding. Blocked
counts because stopping the run is what blocked it, so re-entry supersedes that
blocker rather than waiting for somebody to remember to reopen the item; what
still refuses is unfinished work the item waits for. And a decision of the
development manager's has to be there to carry out: a re-run decision recorded
about *this* stoppage, which the harness finds on the durable record or refuses
naming what is missing. Deciding one spends the item's re-run budget in the same
write, and each decision authorizes one re-run, so what has already been carried
out is read back against what was decided. A stoppage nobody decided this about
is refused, so is one decided otherwise, and so is one whose decision has already
been acted on — a second stoppage of an item that was
already run again needs somebody to decide about *that* stoppage, which past the
once-per-item cap means an escalation rather than a bigger budget. The harness
will not start a run attributed to a decision that does not exist, or to one that
was about something else.

**Every one of the four is asked before anything is claimed**, so a refused
re-run costs the stoppage nothing and says what would make it stop refusing.
That matters most for the item's own state: the budget is spent by claiming it
rather than by running anything, so a refusal made after the claim would be the
decision defeating itself on exactly the blocked items it exists for — refused
once for the status, and refused again by the once-only guard for a run that
never happened. A refusal the fresh run meets past the claim gives the claim
back for the same reason: it reserved no run, so nothing was done on the claim,
and asking again once it no longer refuses carries out the same decision.

**A harness with no free developer refuses nothing at all.** Two developers
happening to be busy at that second is not an argument about the work, and it
stops being true on its own, so a carry-out that meets it waits rather than
failing: it says what it is waiting on, claims nothing, and leaves the
authorization standing until it is carried out or the development manager
withdraws it. Ask again once a slot frees and the same decision runs — and
meanwhile the item is open work [the scheduler pulls
from](work.md#letting-the-harness-choose-the-work), so the work can reach a developer
without this being asked again. The last slot can also go between the reading
and the reservation; a claim taken for a run the reservation then refused is
given back, because that run provably never started.

**A pause that arrives in that same window costs the stoppage nothing either.**
Pausing everything, a directive nobody has settled, work the item has been made
to wait on, and a held intake are all read where the fresh run would start, and
each of them stops it before anything is reserved or claimed. The claim taken for
it is given back and the carry-out says which pause it met; lift that pause and
ask again, and the same decision runs.

What that stopped run preserved is kept until the fresh run integrates and
retired explicitly then — removed, and the removal written onto the stopped run's
own record so `/status` and the docket stop advertising a branch and a worktree
that are gone. Anything that could not be retired is recorded as kept and why; a
branch whose work nothing promoted is never deleted, so what survives is
discoverable rather than orphaned. The pull request that stopped run published is
retired with them: the fresh run's promotion is what its work landed by, so the
request is closed with that vehicle named and the remote branch it published is
deleted, and the forge's open list stops showing it as pending work.

The worktree alone has one other way of going, on a stoppage nobody re-ran: once
enough later runs have settled past it, the [convergence
sweep](operations.md#recovering-interrupted-runs) unregisters the checkout so a
machine's worktree registrations stay bounded. It records that removal the same
way, and it takes nothing — the branch is untouched, and whatever the developer
left uncommitted is recorded on `refs/yoyodyne/preserved-work/<run-id>`, named on
the run's record, before the directory goes. What it costs is `/continue` on that
run, which needs the checkout itself; `/rerun`, the branch, and the preserved
work are all still there.

Guidance the development manager left on the item — what the preserved branch
holds, what is worth cherry-picking rather than writing again — reaches the
developer of the fresh run the way everything in an item's notes does. Nothing
special carries it, deliberately: notes are not evidence for a [protected-path
grant](configuration.md#protected-paths-in-a-developers-change), so
guidance that travels this way can never widen what the re-run is allowed to
change.

`yoyo triage repair <run-id>` is the other one, and it is the answer to the
opposite case: the change is nearly
right and the run ran out of attempts. It starts nothing over. The stopped run
goes on — same branch, same worktree, the developer session that already holds
the context, and the reviewer's findings handed back exactly as they were
written — under the grant the development manager already recorded. Deciding
`repair` is what takes that grant and sizes it; this reads that record for what
it is worth and hands the run exactly that, so it can never give a run more
attempts than the round cap let the item have. Like a re-run it takes no
reasoning: what the run and the item record as why it is going again is the
decision itself, read from that record and citing the conversation and turn it
was recorded on, and a stoppage with no repair decision recorded about it is
refused naming the missing record. **Your hold on intake applies to
this too**, for the same reason it applies to a re-run.

**A stall is the second thing it continues, and it is charged nothing.** A run
whose provider [the harness stopped on time](operations.md#when-a-provider-stalls-or-runs-out-of-budget)
before anything was ever returned to its developer — a stream gone silent, or a
total budget run out, in the run's first attempt — is settled by the reconciling
sweep half an hour later and docketed like any other stoppage. Its entry says the
developer session is preserved and names it, because that is what separates the
two verbs here: a repair carries that session on at the point it stalled, and a
re-run discards it along with whatever the stalled attempt left uncommitted in
the worktree. The continuation counts no review round and no repair attempt, as a
stall judges nothing; what it spends is the grant the decision spent when it was
recorded, so one decision still buys one continuation. It is the one continuation
the preserved worktree does not have to hold a change for — a first attempt
stopped early may never have written anything, and an empty worktree is exactly
what the attempt it is owed starts from. A stall at the review or the checks,
after the attempt finished, is continued at that step instead: the entry says
so, the review is asked again on the change the run has with no developer
invoked, and the branch is kept — so the worktree does have to hold that change,
as it does for any repair. A first stall of a stream gone silent does not wait
on you at all, including when it interrupts a repair already underway. After
the sweep settles it, the harness continues it itself, once, at a watching
session's next pull with a developer slot free, in the same session and at the
same step, and closes the entry in its own name. The interrupted repair keeps
its findings or failing check and the attempt already counted; continuing it
adds no repair attempt, review round, or grant. What reaches you is a run that
stalls again after that continuation, including during repair, a provider
stopped for running out of its total budget, or a continuation the harness
refused for the state of the worktree — see
[what a stall is owed](operations.md#when-a-provider-stalls-or-runs-out-of-budget).

**It supersedes the blocker rather than needing you to remember to.** The run
that stopped blocked its item and recorded the blocker on its own state, which
`/status`, `yoyo reconcile`, and the docket all read as the fact that it stopped.
Re-entry clears both as it happens: the item is put back with the decision
recorded on it, and the run's blocker is cleared onto the continuation that
supersedes it, keeping the words it was recorded in. So a repair does not need
the reopening a re-run does.

Seven things refuse it, and every one of them is asked before either of those
writes, so a refused re-entry leaves the grant exactly where it was. The stopped
run has to be really over. **It must not be an approved change the environment
stopped**: a run whose record carries an integration stop is refused first,
ahead of everything else — an approving verdict can carry minor findings, and a
repair re-entered on those would spend a grant to have an approved change
repaired — and the refusal is one sentence saying the change is approved, what
stopped it and at which step, and that `yoyo triage resume <run-id>` is what it
needs once the cause has cleared. That is the same sentence the docket entry
carries and the channel line ends on, so wherever you read about the stop, you
are sent to the same command — while the run's branch is there, which the
resume needs. The refusal asks the repository for it by the look and the rule
the docket asks, and once the branch is gone it says so, with what was found of
the worktree, and that a re-run is the way on, which is what the docket entry
and `yoyo status` then say too. The channel line asks the same way as it says
the stop, so a sink catching the stop up after the branch was deleted ends it
on the branch being gone and the re-run rather than on a resume that would
refuse. It has to have recorded a failure that was actually
returned to its developer — findings, a failing check, refused paths, or a
replay conflict, which a run records before it stops on one — or be the stall
above, which returned none because the harness stopped it; a run whose provider
kept refusing is neither, and has no attempt to carry on with. The item must
not be closed or waiting on other work. A grant of the development manager's has
to be there and not already carried out. **The preserved worktree has to be
as the harness left it**: what a continued developer is handed back is whatever is
in that worktree, so a HEAD that moved — you mid-surgery, an agent that
committed — refuses to a person, leaves the item blocked, and says so. A checkout
the [convergence sweep](operations.md#recovering-interrupted-runs) retired
refuses here too, and for the same reason: there is nothing left to hand back.
That is what the sweep's tail is for — a recent stoppage still has its checkout,
and an old one is replanned or re-run instead, from its branch and from the
preserved-work ref the sweep recorded before it took the directory. And **the
change has to still be in it**: a worktree the harness would call its own and that
holds nothing passes the check above and fails this one, and a developer handed
the reviewer's findings and an empty directory delivers an empty repair or
reinvents the change from them. That last one is the one condition a stall in
its developer attempt is not held to, because that attempt may never have
written anything and an empty worktree is what it starts from — and the resumed
run makes the same exception, so the two cannot disagree about which
continuations may start on an empty worktree. A stall at the checks or the
review is held to it like any repair: its attempt was complete, and the change
that attempt left is the whole of what the step asked again has to judge.

The run asks that last question again itself, on every resume whose worktree is
supposed to hold a change already — one picked up inside its repair loop, and one
picked up at the checks or the review, which has a developer attempt behind it
and nothing else for those steps to judge. Only the run owed its first attempt is
exempt. And a **fresh run is refused where a repair is owed**: an item whose last
run stopped with its change preserved on a branch does not get started over from
nothing, because a clean worktree off the target branch looks perfectly valid and
is caught by nothing downstream. The refusal names the branch and both ways out,
and `yoyo triage rerun` is the one that starts over deliberately — a claimed
re-run is exactly what lets a fresh run through.

The repair is also dispatched to the run it is about rather than to the item:
carrying it out re-enters that run or refuses, and creates no worktree either
way. That is what makes the refusal above a backstop rather than the only
defense — every recorded case of a repair round going missing was a dispatch
that started something fresh in its place.

`yoyo triage rearm <run-id> --reason "<what the development manager decided>"`
is the third, and it is about the other thing that stops: an approved change
published to a forge that queued its merge and then dropped it. **It repeats
exactly the request your reviewer's verdict authorized** — the same pull request,
by the method that verdict's own merge recorded, pinned to the commit that was
integrated — and it overrides nothing to do it. Administrator privileges are
never used, and repeating an identical request is not merging past a
requirement: the forge runs its whole requirement machinery again. It takes the
target branch's promotion lease before it asks the forge anything and holds it
across the check and the merge together, so it queues behind whatever is
promoting into that branch rather than racing it — and so nothing can move the
target between the check that authorizes the merge and the merge itself.

Five things refuse it, all of them asked before anything is spent. The forge's
own merge state has to name nothing only a person can satisfy — a conflict with
the base, a draft, a base branch that moved ahead, a protection rule the request
does not meet — and a merge state that could not be read refuses too. The request
has to be unchanged: its head is still the commit that was integrated, and the
remote target still passes the same pre-merge content check the original gate
ran. The run that made the publication has to be terminally recorded and the item
has to have no run in flight — a re-arm against live work is what stranded a
hand-written amendment on a preserved branch on 2026-08-19, when the forge merged
an earlier promotion mid-run and that run's republish then failed. And a decision
of the development manager's has to be there to carry out and not already carried
out — and still standing, since a later decision about the same run supersedes
it: **one re-arm per publication**, after which a further drop is an escalation
rather than another re-arm, and the blocker the sweep leaves for a second drop
says exactly that. Your hold on intake does not apply, because a re-arm chooses
no work — it repeats a merge an approving verdict already authorized, for a
change that already passed every gate, whether that change is already on the
local target or, on a target the forge protects, only on its pull request
([configuration](configuration.md#a-protected-target-lands-through-its-pull-request)).

**The same verb arms a request nothing ever asked the forge to merge.** Such a
publication — its record holds the pull request, with no merge queued, none
dropped, and no account of anything going wrong — is docketed the moment it is
recorded, and a `rearm` decision about it is carried out as the merge request
the run's own merge would have made: that merge's method, since no method was
ever recorded, pinned to the promoted commit, under every refusal above. It is
held to two more, read from the forge's checks under the promotion lease: the
request's head has to be level with its target, and no check on its head may be
failing. Each refusal names its gate, and a reading of the checks that could
not be made refuses too. A `rerun` decision is the other answer, and hands the
change back for a fresh run; once it is carried out the prior run's publication
is marked handed back and nothing names it as waiting any more. A request the
forge has closed unmerged is offered the re-run alone. A watching `yoyo work` session carries the re-arm
out itself on its next pull, and a refusal is written onto the item naming the
gate and comes back to you on the docket. A temporary refusal is asked again
only once it has cooled. A permanent refusal is delivered once as a new stoppage
and is not retried until you record a new decision. A re-arm of a run that never
promoted its change cannot be made, so it is recorded as a permanent refusal
naming the re-run or an escalation as the decision to record instead; a re-arm never
brings a head up to date, so a head behind or in conflict with its target is a
re-run too. `yoyo triage rearm` makes it now
rather than at the next pull. Before yoyodyne-ifd.429.31 nothing armed this
state, and its only exit was a hand merge on the forge.

The harness carries out none of the other three: a re-scope, a wait, and an
escalation ask for no action at all. A `stop` and a `retire-raise` are each
carried out as they are recorded rather than by a pass, and neither spends
anything. The budget is spent when the decision is
recorded whether or not the harness acts on it, which is the same direction
every counter here fails in: an attempt nobody took rather than one nobody
counted. What triage changed is that stopped work is decided by the role that
owns it, the decision is durable on the item, and it reaches you only when the
development manager judged it had to.

### Deciding the brake's hold on intake

One decision of hers is about no run at all. When the failure-storm brake holds
intake — that many runs blocked in a row, with nothing landing between them —
the poll that trips it summons her sweep out of its cadence with the runs that
blocked and the reason each blocked in the message that wakes her, and what it
asks first is what happens to the hold. She records that as a `brake` action,
which names no item and takes one of three decisions: `release`, because the
stops were verdicts on three changes rather than on the machine, and the
watching session lifts the hold at its next poll; `probe`, to keep the hold and
have the session start one probe run now, which reopens intake if it lands and
keeps it held — and summons her again, with the probe's own stoppage — if it
blocks; or `escalate`, to keep the hold for you, which is the only decision of hers
under which a brake hold waits on a person, and which she makes with a report
at `warning` severity so it reaches you. The other way it comes to wait on you
is the harness's: the summons names which cycle of the loop it is and at what
cycle the harness stops asking, and at `execution.brake_escalation_cycles`
blocked probes with no escalation of hers the harness escalates the hold
itself, after which a `probe` decision is refused and a `release` still lifts
it. The decision lands on the hold's own
record, naming her conversation and turn, and it lifts and starts nothing from
the conversation: the watching session reads it at its next poll and acts. A
`brake` decision aimed at a hold you placed is refused, because that switch is
yours. The runs themselves are triaged as above, entry by entry, and neither
kind of decision decides the other. Where she records no brake decision by
`execution.brake_cooldown`, the session probes by itself; the whole of that is
in [operations](operations.md#pausing-everything-and-resuming-it).

### Resuming an approved change the environment stopped

`yoyo triage resume <run-id>` is the one action here that carries out no
decision, because the stoppage it answers asks for none. A change the reviewer
approved can still stop short of the target branch for reasons that are not
about the change: the primary checkout carrying an edit you had not committed,
a tracker read that timed out under load, a forge or a network that went away.
Each of those ends the run, and each used to reach the development manager as
a stoppage to decide about — where every decision she could record spent
something for it. A repair was refused outright, because the run recorded no
findings, failing check, or refused paths to hand back; a re-run spent the
item's re-run budget and bought a fresh run and a fresh review of a change
nobody disputed. Five overrides were signed on yoyodyne-ifd.309 that way, four
of them for the environment.

So the harness records such a stop for what it is. As the run ends it reads
the error that ended it — a dirty checkout by the worktree manager's sentinel,
a transport that did not answer by the same closed reading of the error's
text the [recovery rule](operations.md#waiting-out-a-network-that-dropped)
waits out elsewhere — and where the change was approved and nothing was
promoted, it writes an *integration stop* on the run: the cause and the step.
It reads the step's own failure and not the whole error: a run that stopped
and then could not record the stop ends on both, and a blocker write that
timed out reads as transport whatever it was recording, which is how
yoyodyne-ifd.441's replay conflict was once recorded as weather. The docket entry carries
it too, names the harness as the next mover, and prints the command — in one
sentence saying the change is approved, what stopped it, and that `yoyo triage
resume` is what it needs. The channel line for the stop ends on that same
sentence, and so does the refusal `yoyo triage repair` gives if it is asked for
such a run instead. The entry says so while the run's branch is there, which
it asks the repository each time the docket is built, by the look and the rule
the pull's hold and `yoyo status` ask too — and the channel line and the
repair's refusal ask it the same way, the line as it is said and the refusal as
it is given. A stop whose branch is gone is one the resume would refuse, so
none of them names the resume: the channel line and the refusal each say the
branch is gone, what was found of the worktree, and that a re-run is the way
on because the approved change has nothing left for a resume to promote. The
entry says the same, and it
names the development manager as the next mover — or the harness, where a
decision of hers about the stoppage is recorded and not yet carried out. Where
the worktree is gone as well and nothing is decided, the pull's hold lets the
item go, so the entry names the next pull instead: that is what starts the item
over, and nothing about it waits on her unless she wants it held back. The
resume then makes the run live again at exactly that step, with the approval
it already has, and the pipeline promotes — replay onto where the target now
stands, push, merge request — without invoking anybody: no developer attempt,
no review round, no repair grant, no re-run, and every counter on the item's
triage record where the review left it. The resumption is recorded on the run
as a continuation rather than an attempt, `/status` says **approved, resuming
integration** of the run while it promotes, and the item's notes carry the
harness's own account of the stop it superseded, plus whatever `--reason` you
gave beside it, attributed to the command rather than to any role.

Five things refuse it, all asked before anything is written, so a refused
resume leaves the run exactly as it stopped and asking again once the cause
has cleared resumes the same run. The run's own record has to say it is one
of these — an approving verdict standing, no promotion, an integration stop
recorded, the branch still there, fewer than sixteen resumptions already on it
— and a run whose record says anything else is refused naming what it is and
which verb it needs. The sixteen is the record's own bound rather than a cap on
the item: nothing spends toward it, and a promotion the environment has
stopped that often needs somebody looking at the machine, after which a re-run
is the way on. The primary checkout has
to be one a promotion can be made from again, because it is what stopped the
run once already. The item must not be closed or waiting on other work. And
the preserved worktree has to be as the harness left it and still hold the
approved change, on the same two conditions a repair asks and to the same
person — two refusals, asked last. Two more things are waits rather than
refusals, and write nothing: your hold on intake applies, for the reason it
applies to a repair, and a full harness waits for a slot.

A worktree the [convergence sweep](operations.md#recovering-interrupted-runs)
retired while the run stood stopped is neither. The branch still holds the
reviewed commit, so the resume puts the checkout back from the branch at
exactly that commit — after the waits, because it is the one write made before
the re-entry — and records on the run that the checkout is back as it does so,
so a refusal past it leaves a stopped run whose worktree is there again rather
than a record that says it is gone. A branch that has moved past the recorded
commit, or a sweep that captured uncommitted work off the directory, refuses
to a person: what a restored checkout would promote is not what was reviewed.

One thing leaves the resumed path, and it leaves it exactly as it always did:
a replay onto a target that moved re-earns the checks and the review like any
replay, and a replay that conflicts goes back to the change's developer as a
repair attempt, or stops the run for a person with both sides preserved where no
attempt is left. A conflict is never recorded as an integration stop, because the
environment does not answer for it; it is recorded on the run as a *replay
conflict* instead, written before the blocker about it is attempted on the
tracker so it survives that write failing, and the record refuses the two
together. The docket entry for such a run names the conflict and you as the
next mover, names `yoyo triage repair` as the verb that hands the conflict back
to the same developer session (yoyodyne-ifd.132), and says the resume is not the
answer; asked anyway, the resume refuses in the same sentence.

Everything you type as a command — `/status`, `/backlog`, `/show`, `/work`,
`/reports`, `/refresh` — means the same thing in every conversation, because
those are your authority carried out by the harness rather than anything the
agent did.

## The same conversation from Slack

`yoyo chat` is not the only way into it. Where the
[Slack sink](slack/setup.md#asking-the-app-directly) is running, @-mentioning the
app in its channel reaches the same Lead Product Manager, and the answer comes back in
the thread you asked in and in the Lead Product Manager's own name.

It is the same conversation and not a copy of one. There is one durable record
per agent, and the terminal and the channel are two clients of it: the provider
session is resumed rather than restarted, the turns accumulate on one record, and
a proposal put to you in one client is decided in the other — `y` typed in the
channel approves what `yoyo chat` offered you, because the harness carries out a
decision the same way whichever client it arrived through. So a question you
asked before you left your desk is answered from a phone, and what was said there
is in front of you when you open `yoyo chat` again.

What is exclusive is a turn, [as it is everywhere](#talking-to-the-other-agents),
and that is what the two rules around it are for. The channel takes the
conversation when you say something and gives it back as soon as the answer is
in hand — the same span your own window holds it for, so neither client is ever
locked out for longer than one turn, and a `yoyo chat` waiting at its prompt
holds nothing against the channel at all. Where the Lead Product Manager is mid-turn
with another client when your message arrives — your terminal answering, or the
harness delivering something to it — the thread says so rather than failing
quietly, and rather than queueing behind it: nothing was said, and you say it
again once that turn lands. And the channel's wait is bounded at ten minutes,
after which the thread is told what happened: the wait running out, the
conversation mid-turn elsewhere, or the provider's own reason, which for an
exhausted usage limit is that limit in its own words. A turn that steers work
can wait on capacity for hours, and that is a thing you choose at a terminal
rather than something a channel does to you while you watch a thread.

That bound is on your wait rather than on the turn: a turn the channel stopped
waiting for may still be running. A turn sleeping out a provider usage window
releases the conversation while it waits, and takes it back before asking again;
other messages can reach the Lead Product Manager during that wait. While a
provider is actively answering, the turn holds the conversation and a new channel
message is answered with the Lead Product Manager being busy. `yoyo chat` is not refused, and it does not show you a turn
that is still being written: it queues behind that turn, says that another
process is mid-turn and that it is waiting, and then continues the same
conversation from wherever the turn got to. `yoyo agent list` says whether the
Lead Product Manager is still mid-turn without waiting on it, which is the reading to
take before deciding whether to wait.

Two things do not go to the Lead Product Manager from there. Where things stand is
answered without a turn: `@yoyodyne status` is the read model's own four lines
rather than something the Lead Product Manager was asked for. And the commands above
are refused with where to type them: they are your authority carried out by the
harness, so `@yoyodyne /backlog` is answered rather than read out to the Lead Product
Manager as a sentence, and costs nothing. Talking to it at all is held to the
same `direct-work` grant a thread reply is, because it admits work, reorders the
queue, and spends your money.

## What the conversation looks like on a terminal

On a terminal, the line you are composing has a region of its own at the bottom
of the screen and everything the harness writes goes above it. A reply, a
proposal, or a run that finishes never lands in the middle of a half-typed
sentence: what you have typed stays exactly as it is, and you carry on from
where you were. The conversation is written into the terminal's ordinary output
rather than an alternate screen, so scrollback, selection and copying, and
resizing keep working on it as they would on any other command's output. Editing
what you are composing is deliberately small — the arrow keys, home and end,
backspace and delete, and Ctrl-U and Ctrl-W.

Return sends the message, and shift-return puts a newline in it, so what you say
can be more than one line. Whether shift-return reaches yoyo at all is the
terminal's decision rather than yoyo's: in a terminal's legacy mode return and
shift-return are the same byte, and a key that silently does nothing is worse
than one you were never offered. So the terminal is asked when the conversation
opens — the kitty keyboard protocol, or xterm's modifyOtherKeys — and where it
answers, shift-return inserts a newline. Where it does not, **alt-return** does,
and so does **ending a line with a backslash**, which asks the terminal for
nothing at all and works on a redirected stream too. `/help` says which of these
this terminal supports rather than listing all of them at you. The price of the
backslash is that a message ending in one cannot be typed: the backslash is what
carries the line on. What you compose is drawn in the same region, over as many
rows as it has lines, and reaches the Lead Product Manager with its lines where you
put them. A message with more lines than your window has rows is drawn as the
part of it that fits — the end of it, where you are typing, or wherever you have
moved the cursor to — because a region drawn past the top of the window could no
longer be erased without taking the conversation above it. Only the drawing is
bounded: the message you send is all of it.

A paste is one message, newlines and all. The terminal is asked to bracket what
is pasted — the `ESC[200~ … ESC[201~` that kitty, xterm, and Terminal.app all
wrap a paste in — so a block of several lines, blank lines among them, lands in
the region as one message with its lines where they were, and return then sends
it; before this a paste of three lines sent the first and spilled the other two
into the prompts that followed. There is no question a terminal answers about
the mode, so it is asked for on every terminal rather than negotiated the way
shift-return is, and one without the mode goes on handing a paste over as
keystrokes, which is what every terminal did before. Only the newlines are kept exactly as they were: a tab in a paste
becomes a space, because the region measures what it drew in columns and a tab
is as wide as the terminal decides, and any other control character is dropped
as it is when typed. The bracketing is turned off whenever the terminal changes
hands, exactly as the keyboard is.

Ctrl-C still interrupts the way it always did, and Ctrl-Z still stops the
conversation. A terminal that has agreed to report shift-return stops raising
the signal keys itself, so yoyo raises what it reports — to the same process
group the terminal would have. The keyboard is handed back exactly as it was
found whenever the terminal changes hands: when the conversation ends, and when
Ctrl-Z stops it, so the shell you drop into finds none of this on it. Resuming
takes it back and asks again, because what a terminal agreed to before a stop is
not what it is doing after one.

While a turn is being answered, the line below the conversation says what it is
doing: a spinner, the phase it has reached, and how long you have been waiting.
The phases are read off the same event stream the turn is already recording —
your message going out, the model thinking, the model writing its reply, the
harness carrying out tracker actions it asked for — and a provider that is
refusing requests is named as exactly that, with the attempt it is on, because a
turn that is slow because the service or your account is declining work is
telling you something worth knowing. Nothing arriving for twenty seconds stops
the animation: the line then says how long it has been quiet, because a display
that keeps moving through a stall looks like progress, and looking like progress
is worse than saying nothing. It is drawn above the line you are typing and
erased when there is a reply to read, so it is never in your way and never in
the scrollback.

The reply itself arrives while it is being written rather than all at once when
it is finished. The provider reports the Lead Product Manager's message before the
terminal result the turn is recorded from, so the text already exists before the
turn is over and what changed is only when you are shown it. It reads exactly as
the finished reply reads — the same opening, the same Markdown, the same
questions in the same colour — and it is not written a second time when the turn
ends. The blocks the Lead Product Manager writes for the harness rather than for you
are not shown as prose: a proposal, a tracker action, a concern, and a report
are each reported in their own way once the turn is over, and the source of one
arriving mid-sentence would be the protocol rather than the answer. None of this
touches the record: the reply that is recorded, the events, and
`--message --json` are byte for byte what they would have been with nobody
watching, because the fragments are the same text the harness had already
redacted and already written down. A turn whose provider stops before the reply
is finished says so on the line after the prose it managed to show, because
prose that simply stops reads as a Lead Product Manager that had nothing more to say.

Between turns that line carries what the conversation has cost: what the last
answer was charged and what this session has spent, taken from what the provider
itself reported per invocation and worked out no further. It is replaced rather
than written into the conversation, so a running total is somewhere you can see
it rather than a log of itself, and a provider that reports no cost is left
unanswered rather than reported as free. Work in progress covers it while there
is any, because what you are waiting on is the more urgent of the two.

A horizontal rule separates your turn from the answer to it, and colour tells
apart the things you have to act on rather than read past: a question the
Lead Product Manager asks you is orange, and a proposal awaiting your decision and
the harness's own answer to a command each have a colour of their own. A
proposal is framed as a card so a batch of them reads as several things rather
than one wall of text; the frame is decoration exactly as the rule is, and where
decoration is suppressed the same card is its heading with the body indented
under it. The states work is in are coloured too, and the same way wherever they
appear — running blue, blocked orange, done green, failed red — so `/status` is
read down its aligned columns rather than picked out of ragged prose. So is what
a report or a concern is asking for: something already wrong is red and bold,
a risk that has not cost anything yet is orange, and a note is left plain,
because a listing where every line is coloured has no emphasis left for the line
that matters. That one carries a mark as well as a colour — `!!` at the left
margin for critical and `!` for warning, in the column before the identifier —
so the pile can be scanned down its margin, and so the distinction is the one
thing here that survives a terminal which cannot be dressed at all. A concern is
marked the same way, by kind: work the Lead Product Manager says would cut against a
goal is the critical one, and the two that are questions about incomplete goals
or about its own judgement are warnings. Colour is
an addition to the text and never what carries the meaning — the question still
ends in a question mark, the proposal still says what it is proposing, the group
still says "blocked (2)" in words — so a transcript with the escapes stripped
out loses the decoration and nothing else. `NO_COLOR`, a terminal that reports
itself as `dumb`, and output that is not a terminal each suppress all of it
together — the colour, the rules, the cards, the reply shown as it forms, the
milestones a run reports, the bell and window title, and the cost line — because
every one of them writes an escape or depends on there being a moment at which
something unprompted can be written, and somebody who asked for an undecorated
conversation asked for all of it.

The Lead Product Manager writes Markdown, and on a terminal you read it as Markdown:
headings, list markers, thematic breaks, and bold spans are shown as structure
rather than spelled out in punctuation. That is presentation and only
presentation. Nothing is added to the reply and nothing is taken out of it —
every escape is inserted between characters that were already there — so the
same reply stripped of its escapes is the recorded reply byte for byte, and a
stream that may not be dressed is shown exactly what was written.

Anywhere else the same conversation is an ordinary stream of text. A pipe, a
file, a redirected terminal, and a terminal that reports itself as `dumb` get no
cursor control, no colour, and no rules at all: the same lines in the same order
a redirected conversation has always had, plus each phase of a turn said once as
a line of its own, with nothing in it that a clock decided — there is nothing to
animate or erase on a stream, and a transcript whose contents depended on how
long the provider took would not be one you could compare against another. For
the same reason a stream is shown the reply when it is finished rather than as
it forms, and a run it started is not watched at all: a stream has no moment
where you are waiting with the screen to yourself, so anything written between
two lines that are already buffered would make what the transcript holds depend
on timing. None of this reaches the recorded reply, the event stream, or
`--json` — it is how the conversation is shown and nothing more, so what is
recorded is identical either way.

## How fresh the conversation's picture is, and how to refresh it

The specifications and tracker the Lead Product Manager reads are gathered once, when
a conversation opens, and sent on its first turn only. Every later turn resumes a
provider session that already holds them, so re-sending would pay to restate what
it was already told. The consequence is worth knowing before it surprises you:
**a resumed conversation keeps the snapshot it opened with.** Change a
specification, and a conversation started beforehand will still describe the old
one, confidently, because that is genuinely the evidence it has.

So the conversation says so itself, on one line, as it opens and as it resumes:

```text
context gathered 2h ago at 6b069347e3f4; 14 commits and 3 tracker changes since. /refresh reads what moved into this conversation.
```

Freshness is a comparison rather than a timestamp. The picture records when it
was assembled and what commit the repository was on, both durably, so the
process that resumes a conversation can say how old it is without having been the
one that briefed it. The commit is named on the line because the age alone
cannot be checked: a line saying the picture is two hours old and fourteen
landings behind reads the same whether the picture is advancing or stuck, and a
commit that changes between two of these lines is a refresh having landed. A
repository that would not say what it was on leaves the clause out rather than
printing a commit nobody established. What has moved since is two cheap questions: what `HEAD`
holds that the picture did not, and what the tracker wrote into its own
interactions log after the picture was taken. Either comparison can fail — an
unrecorded commit, a repository that will not answer — and a comparison that
could not be made is reported as unknown rather than counted as nothing, because
"0 commits" from a broken comparison is the same confident staleness this exists
to end. The tracker's log is an export rather than its live state, so the count
is a floor on what has moved; a log a tracker has never exported, and one too
large to read to the end, are both reported as unknown rather than as unchanged,
because a truncated comparison that answered "nothing moved" would be the same
false confidence in a smaller place. A one-shot `--message` says the same line on
stderr, where it cannot disturb the reply on stdout or the `--json` document.
Where the commits are past the threshold below, the line says so and what
follows from it — `That is past the 20 landings this project allows, so the next
reply re-reads it first; /refresh reads it now.` — so nothing on it is left for
you to decide that the next reply decides anyway.

`/refresh` re-reads the repository and the tracker into the running
conversation. It discards nothing: what has been said stays said, and the new
picture reaches the Lead Product Manager on your next message, framed as evidence
with an account of what moved, so it reconciles what it believed rather than
having it swapped underneath. The transcript says the refresh happened, the
conversation's own log records it, and the durable record moves to the new
picture as it is read while also saying the picture is still owed, and which
picture the agent last received, until a turn has actually carried it — a
refresh nobody was told about never reads as one that landed.

**A refresh carries what moved, not the whole picture.** The agent's session
already holds the picture it was last given, and it keeps everything it is ever
sent, so a refresh that sent the whole picture again added the whole picture to
the session again. The development manager's conversation showed where that
ends: about twenty re-reads of roughly a megabyte each went into one session
from 2026-09-20, and on 2026-09-23 a turn that failed was asked again with the
whole picture some twenty-five times, until the session passed the 32 MB the
provider accepts in one request and no turn could be taken at all. So the text
of the last delivered picture is kept beside the record, and a refresh is
delivered as the sections that differ from it: the changed lines of a document
or listing both pictures carry, with a line or two around them to show where
they sit, a section that is new in full, and a section that is gone by name.
Every section not named is as the agent was last given it, and the framing tells
it so. The turn after a refresh is as large as what moved rather than as large
as the product. A conversation that has nothing to compare against — one begun
before the text was kept — gets the whole picture once, and changes after that;
and a turn rebuilt for a provider holding no session is given the whole picture
in front of the changes, since changes mean nothing to a provider that never
held what they changed.

**A session is compacted before it outgrows the request.** Smaller turns slow a
session's growth without bounding it, and the provider's own compaction triggers
on its token count rather than on the request ceiling — and sends the whole
session to do it, so on 2026-09-24 a Lead Product Manager session at about 34 MB
could not even be compacted. So the harness measures each provider session
itself, as the bytes of every prompt it sent the session and every reply it got
back, and records the measure on the conversation beside the budget it is held
to (`provider_session_bytes` and `provider_session_budget_bytes`, and
`session_bytes` and `session_budget_bytes` in `--json` evidence). The budget is
8 MiB, a quarter of the 32 MB ceiling, because the provider's request carries
framing, encoding, and reasoning the harness never sees. Before a turn that would
take the session past it, a role that keeps memory receives the save turn
[described above](#what-the-management-roles-remember), on the old session.
After its memory writes are attempted, the waiting turn is sent without the
session: the conversation is rebuilt
from its record in front of the turn, exactly as for a provider holding no
session, and the provider's answer starts a new session the measure starts again
from. That is recorded as a `session.compacted` event naming the old session,
its size, and the budget. A rebuild that cannot be made is recorded as
`session.compaction_failed` and the turn is not sent, and the error says so;
it is not a provider refusal, so nothing waits and asks again. A session recorded
before the harness measured sessions is compacted on its next turn, since
nobody knows how large it is.

**The rebuilt request must fit the endpoint too.** A small new session can still
start with too large a prompt: the working picture, recent exchange, memories,
role instructions and current evidence all travel together. Before sending,
the harness asks the selected adapter to measure what it will send, including
its own added instructions, and reserves five percent of that endpoint's bound.
Codex accepts at most 1,048,576 characters of input; the adapter counts UTF-8
bytes conservatively. Claude Code's supplied text is measured with JSON
escaping against the API's 32 MiB ceiling; its hidden native history still needs
the session budget above.

When a reconstruction is over the bound, the harness lowers the existing
rebuild's allowance for old messages until the request fits, oldest first. The
role is told how many messages are omitted and where they remain. Its current
turn, role instructions, working picture, saved memories and pending results
are kept intact. Decisions and docket entries stay in their durable stores;
shortening a request removes no stored record. The event log records the
shortening as `session.compacted` with reason `request_size`. The check runs
after endpoint selection, so failover uses the receiving endpoint's bound.
If a provider still rejects input for size, including Codex's `input_too_large`,
the harness tries the turn once with a shorter reconstruction and records
`request_size_retry`; a served retry completes the same pass. This also applies
to the memory-save turn before compaction. Capacity waits keep the shortened
request and do not renew its size retry. If another turn advances the record
while it waits, the harness rebuilds from that newer record with the reduced
history allowance. A second size refusal ends that turn.
A reconstruction that cannot read the durable record ends without another
provider attempt, returns the read error, and retains the event position of
any session replacement already recorded. If the fixed instructions and current
evidence alone cannot fit, the harness refuses before starting the provider and
says why rather than cutting them to obtain an answer.

A memory write that would exceed the 32,768-byte live memory budget is refused
whole, including a memory compaction: it changes no memory. The refusal names
the proposed total, the current total, the budget, and the three largest current
memories by name and size, so the role can shorten or retire one. A budget
refusal during the save turn does not stop the session rebuild or the waiting
reply. The role receives the refused write's result on its next turn, including
the turn answering the waiting message after the rebuild.

**The harness refreshes on its own past a threshold.** The line above turned
out not to be enough: on 2026-09-18 the Lead Product Manager advised adding a
section to CLAUDE.md that the file at HEAD had opened with for a month, from a
picture roughly 500 landings old, and the freshness line had said so every time
the conversation resumed. A line you have to act on is a line somebody
eventually reads past. So before every reply — every one, the first included —
the harness measures how far the picture has fallen behind the target branch,
in landings rather than hours, and writes the answer to the conversation's log
as a `context.measured` event: when the picture was gathered, against which
commit, how many landings and tracker changes since, the threshold, and what
was done about it. Past the threshold — `conversation.refresh_after_landings`,
20 unless [you set it](configuration.md#how-far-behind-a-conversations-picture-may-fall)
— the harness re-reads the repository and the tracker before the turn is
answered, exactly as `/refresh` does and with the same framing to the role, and
the transcript and `--json` (`picture`) tell you afterwards:

```text
[picture] 41 landings behind the target branch, past the 20 this project allows; the harness re-read the repository and the tracker before answering, and nothing said here was discarded. The picture moved from a1a1a1a1a1a1 to b2b2b2b2b2b2.
```

The pair of commits is the point of that last sentence, and `/refresh` says the
same pair its own way. A refresh that landed moves the picture from one commit
to another, and the commit the freshness line carries on the next message is
the one it moved to; a re-read that changed nothing is a claim you would
otherwise have to take on trust.

**A re-read outlives the turn it was taken for.** Both kinds — yours and the
harness's — are written to the conversation's durable record before the turn
that would carry them is asked: which picture is waiting and the commit it was
read against, with the text of it kept beside the record. So a turn that fails
leaves the picture advanced rather than throwing the re-read away, and the next
process delivers what was already read instead of walking the repository and the
tracker again from the same old commit. The `context.measured` event says which
of the two happened — a picture read for this turn, or one carried from a
refresh a failed turn left — because the two cost different things, and the
freshness line says so too rather than sending you to spend a second re-read on
a picture already in hand:

```text
context gathered 3h ago at a1a1a1a1a1a1; a re-read taken 12m ago at b2b2b2b2b2b2 is waiting, and is delivered with the next thing said to the agent.
```

That is the amplifier of 2026-09-20. Every management turn was being refused
that day by the turn-size backstop, and each refusal came after a completed
re-read that went down with the process holding it, so one stuck picture became
21 full re-reads of the repository and the tracker — all of them discarded, all
of them measured from the same month-old commit. The backstop was corrected the
next day, which removed that day's reason for the turns to fail; turns fail for
other reasons, and this is what stops the next burst of them costing a re-read
apiece. The record's picture moves when the re-read is taken, so the next
measurement starts from it rather than from a picture a failing turn never
replaced; the record also says the re-read is still waiting, and a refresh
nobody was told about is still a picture waiting, never one that landed. A
picture carried out of a failed turn is measured again before it is delivered,
because it can be any age by then: on 2026-09-23 one was carried through thirty
hours of failing turns. One that has itself fallen past the threshold is read
again, and the agent is still told everything that moved since the picture it
last received.

Where the re-read cannot be made — the tracker is locked, the repository will
not answer — the reply is still given, and it says in its own text, ahead of
whatever the role goes on to say, how many landings old the picture it was
answered from is and why it could not be brought current. The role is told the
same thing in its turn and asked to say which of its claims rest on the old
picture; the sentence in the reply is the harness's, so it is there whatever the
role chose to say. An age the repository would not give at all — a conversation
recorded before commits were, a `git` that fails — is stated the same way rather
than read as current, and a `/refresh` that lands is what clears it.

The threshold is a number about your project's pace and not a switch: it may
not be zero, it may not be more than 200, and no value turns the measurement,
the re-read, or the statement off. A picture within it is answered from as it
stands, with nothing said and the age still on the record.

It is not a cost control, and tuning it as one is a mistake this project has
already made once. Measured over the seven days to 2026-09-22, a refresh happens
on about one management turn in forty and costs roughly $7.80 in re-delivered
bundle, against the 88 per cent of real conversation spend that goes on turns
whose prompt cache had expired before they began —
[the measurement](diagnoses/yoyodyne-ifd-430-1-where-the-conversation-spend-goes.md)
says what a turn actually costs and which knob moves it. Set the threshold for
how old you are willing for the advice to be, and look at the recurring-task
intervals for the money.

It was never frozen entirely. Every turn carries what you did through the
harness since the last reply — the runs you started, stopped, and redirected —
so `/work`, `/stop`, and `/redirect` reach a resumed conversation, and reading an
item, surveying the open queue, and acting on any item all go to the tracker as it
stands rather than to that opening snapshot. A management role can also
[read one repository path](#reading-the-repository-at-a-recorded-commit) at the
commit `HEAD` names now, which is how it checks a document before advising about
it rather than describing the copy in its briefing. Nothing outside those commands
arrives on its own — an item something else created or closed reaches the
conversation when the Lead Product Manager asks, by surveying or by acting on it, and
not before — and edits under `docs/product` do not reach it that way at all, since
the tracker does not hold them. That is what `/refresh` is for, and what the
harness's own refresh does once enough has landed; between the two, `/refresh`
is how you bring an edit in before the threshold would.

`--new` is a different tool rather than the answer to staleness. A refreshed
conversation and a new one end up equally current; they differ in what they
remember, and that difference is the point. Start a new one when the history
itself is the problem — an unrelated topic where its memory of the last one is
not worth carrying — and refresh when the ground has moved under a discussion
worth keeping. `--new` replaces the recorded conversation: there is one per
product, so the previous discussion is not kept alongside it.
