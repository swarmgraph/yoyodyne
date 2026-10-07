<!--
Landed by yoyodyne-ifd.117.2, tranche 2 of the configuration.md split, with
docs/configuration.md left intact. Nothing here links into ../configuration.md
except the index link below, and no link here points at a tranche 3 guide
(runs.md, publishing.md, recovery.md).

"The configuration index ... lists the other guides" below is a forward claim:
configuration.md becomes the index in 117.4.

Scope against docs/docs-map.md: every section the map's disposition table
assigns this guide — What reaches the queue, Traceability: references and
orphans, Goals, and the work attributed to them, What a change upstream leaves
stale, and Architectural invariants. The four lifted from under Artifact
identity and metadata are promoted from ### to ## here, as the preserved
branch had them; their slugs are unchanged. "The release-readiness workflow",
which sits between the last two in configuration.md, has no destination row
in the map and stays there. Size: 528 lines against the map's 440-line
budget; the sections themselves grew after the map's counts were taken.
-->
# Configuring admission, attribution, and staleness

What reaches the work queue and on whose approval, how work is traced back to
the brief, how a goal claims the work attributed to it, what a change upstream
leaves stale, and the invariants a change is held to.

[The configuration index](../configuration.md) lists the other guides.

## What reaches the queue

**`approvals.work_items` decides whether you are asked about every work item.**
It is `human` until you say otherwise: every item is put to you before it is
admitted to the queue. Set it to `automatic` to move that approval up to your
goals, which is what it exists for — work that traces to a goal you approved is
then admitted without a further prompt, and you are told afterwards what went in.

**It is opted in to rather than inherited**, for the reason `integration` and
`publishing` are: this is the setting that lets work reach the queue with no
person in the loop, and autonomy is something you turn on once you have the gates
to justify it rather than something a repository acquires by extending a bundle
or by upgrading the executable. The bundle states `human` at the same value the
harness default holds, so `automatic` never arrives on its own.

**Upgrading does move one thing, and it moves toward asking you.** Before this
key existed the Lead Product Manager could admit work to the backlog directly, and you
were told afterwards rather than asked; `human` refuses that direct admission, so
a project that upgrades and leaves the key alone has a Lead Product Manager that
proposes work instead of admitting it. That is the whole of the change, it is in
the direction of more consent rather than less, and the work is not lost — the
proposal is put to you, and approving it creates the item. Set `work_items` to
`automatic` to have it admit directly again, against goals you approved.

**Approval moved up a level; it did not disappear.** Three things still stop and
ask, and they are exactly what the Lead Product Manager escalates rather than
proposes: work it can attach to no goal, work it says would cut against one, and
work that fits the goals and that it judges to be against what the product is
for. A change to what the goals admit is yours to decide and reaches the queue
through nothing at all: the Lead Product Manager drafts it, and it reaches the
repository only as a [typed write you
approve](../artifacts.md#writing-a-document-from-a-conversation). A rewording that
leaves them admitting and refusing the same work is the Lead Product Manager's
to record, and your approval stands through it — see [approving a
document](artifacts.md#approving-a-document).

**Nothing is admitted without asking until a goal is actually approved.** The
attribution has to resolve to a goal an active document states, and that
document has to be approved as it now stands. A goals document nobody approved,
one amended since you approved it — a rewording the Lead Product Manager recorded
as consistent with intent is not an amendment for this — and a repository with no
goals to check against all put the work to you instead, with the reason on the proposal. So
turning it on gets you a second ramp for free: a project that has opted in still
asks about everything until its first `yoyo artifact approve`. It is also why
`work_items: automatic` requires `approvals.goals` to be `human`: admitting work
rests on the goal it serves having been approved, and a project approving no
goals has nothing for it to rest on, so the combination is refused rather than
left to be discovered as a queue that never fills. That refusal only ever names a
key you wrote, because `automatic` is never inherited.

**Both ways work reaches the queue are governed by it.** The Lead Product Manager can
admit work to the backlog directly as well as propose it, and `human` refuses the
direct admission with a pointer at the proposal it should have made instead — a
setting that governed proposals while work arrived through the other door would
say one thing and do another. Decomposition is not admission: a role that may
only create underneath work you already admitted is building structure under a
decision that was made, and it is unaffected by either setting.

**`approvals.work_item_exemptions` narrows the per-item gate without lifting it.**
It is a list of classes of work this project admits without asking, whatever
`work_items` says, and it is empty until you write one:

```yaml
approvals:
  work_items: human
  work_item_exemptions:
    - diagnosis
```

There is one class. `diagnosis` is work that only looks: it reads what is already
there, says up front what it will read and stops there, and produces findings
rather than a change. It exists because "ask me about every work item" turns out
to be coarser than most operators who set it mean — being asked before something
reads the repository and writes down what it found is not what the gate was put
up for, and with no way to say so the policy stays a sentence nothing enforces.

**The class is the agent's claim about its own work, and the exemption is yours.**
A proposal or a `create` may carry `class: diagnosis`, and it means nothing at all
in a project that has not exempted that class — the Lead Product Manager is told about
a class only where you have exempted it, precisely so it is never invited to claim
one that would change nothing. What keeps the claim honest is that the exempted
class is work that changes nothing: an item claiming to be diagnosis and then
doing something else is an item whose description says what it does, under a goal
that had to resolve, in a queue you read.

**An exemption moves who is asked and never whether the work is for anything.**
Work admitted under one names a goal that *resolves* — one an active goals
document actually states — and nothing weaker. Anything short of that is put to
you exactly as it would be for work claiming no class: a goal the documents do
not state is `unresolved` and refused, and a goal nothing could check against is
`uncheckable` and asked about, because an attribution nobody could check is not
one you agreed to. What an exemption does not require is that the goal be
*approved*, which is what makes it usable by the projects that keep the human
gate. The [attribution table](#goals-and-the-work-attributed-to-them) is the same
table for exempted work as for everything else.

**What it narrows is the per-item gate and only that.** Under `work_items:
human` the exemption stands the per-item question down, and the goal has only to
resolve. Under `work_items: automatic` there is no per-item question left to
narrow: the approved goal is the whole of what admits work there, so an exempt
class clears exactly the gate every other item clears, and a goal nobody
approved — or one amended since — puts the work to you as it would anything
else. An exemption that reached past the per-item question would be a carve-out
admitting work under a goal nobody approved, which is the opposite of the
narrowing it is.

**What was admitted without asking is reported where a decision would have been.**
Each item is named with the goal it traces to and with what actually admitted it
— the approved goal, or the class you exempted — in the conversation and in
`yoyo chat --message ... --json` under `admitted`, where they are the `goal` and
`basis` fields. The two are not the same answer: under an exemption the item
still names a goal, and the goal is not what let it through. The item's own notes
record the same basis, and never that you approved the item. The conversation's
event log records an admission as its own event, so work nobody was asked about
is never readable as work somebody approved.

**Approving writes nothing but the approval.** The prose, the title, what the
document supports, and its status are untouched, so an approval can never become
a way to edit a document by another name — the document itself stays the owning
role's to change. Approval is recorded as yours rather than as a role's, because
every one of these documents is drafted by the role that owns it, and an
approval a role could record would be that role approving its own document.
Refused, rather than recorded: an approval with no reason saying how you gave it,
a second approval of a revision already approved, and approving a document that
has been superseded or retired.

## Traceability: references and orphans

Identity makes a relationship expressible; it does not make it true. So the
chain is validated across the whole set every time the artifacts are loaded, and
what it finds is **reported, never refused** — the opposite of how a document
with no usable identity is handled, and for the same reason a malformed
specification is still read. A broken relationship is a thing to correct, not a
reason to lose a document somebody wrote. What each document's revision log says
about who changed it is reported in the same place and for the same reason, so
one listing says everything that is wrong with the documents that loaded.

| Reported as | What it is |
| --- | --- |
| `dangling-reference` | A `supports` entry naming an id no artifact answers to. Both ends are named: the file the reference is written in, and the id it names. If that id belongs to a file that is in an artifact home and was refused, the report says so and names it, rather than reading as a document nobody wrote. |
| `orphan` | An artifact that nothing connects back to the brief. Following `supports` upstream from it — through as many artifacts as the chain runs — arrives at no `brief`. |
| `unauthorized-revision` | A revision recorded under a role that does not [own the document](artifacts.md#who-may-change-an-artifact). Reported once per document, naming which entries crossed, because opening the file and deciding is one job however many there are. |

Two kinds are never orphans. The **brief** is the root, so nothing is upstream
of it. A **decision** record says how the product is built rather than what it
is for: it is taken in service of the goals without being a statement of intent
downstream of them. Everything else — goals, non-goals, designs, and
specifications — has to trace to the brief.

Only references that resolve are followed, so a reference that names nothing is
reported once as the broken name it is rather than guessed at. Nothing is
followed twice, so two artifacts that support each other are reported as
reaching nothing rather than sending the check round in a circle. A repository
with no `brief` recorded at all is told that, once per document, instead of
being told that each of its documents is separately unconnected.

An artifact that is `superseded` or `retired` still answers to its id and still
holds its place in the chain. The record of what was intended is what makes a
later change traceable, so a design that traces through a goal since replaced is
not an orphan.

```sh
yoyo artifact list   # broken relationships go to stderr beside the listing
yoyo artifact show v1-goals   # and what is wrong with one document, for that one
```

`--kind` narrows the listing and not the reporting: the chain runs between
kinds, and a listing narrowed to the goals would otherwise hide the design that
names one of them and resolves to nothing.

## Goals, and the work attributed to them

The chain's last link runs from a work item to a goal, and a work item is in the
tracker rather than in an artifact home. So the goals themselves are read out of
the goals artifacts: **every entry under a goals document's `Goals` heading is a
goal work can be attributed to**, and an attribution resolves by naming that
goal's stable identity.

**A goal's identity is written in square brackets at the start of its entry** —
`- [traceable-chain] Maintain a traceable chain ...` — and is lower-case
letters, digits, and single hyphens between them. It is assigned once, never
reused, and unchanged by every re-wording of the sentence beside it. The words
are what a reader reads and what a work item displays; they are not what the
match depends on, so amending a goal orphans no item attributed by identity and
refuses no admission that names the identity. Brackets holding anything else —
a phrase with spaces, a path, a Markdown link — are prose, and an entry carrying
them states no identity rather than a malformed one.

**A goal that states no identity is matched on its words**, with case,
surrounding and repeated whitespace, and trailing sentence punctuation folded.
That is the older arrangement, it still resolves, and it is the one a re-wording
breaks. So is an admission that quotes a goal's earlier wording and names no
identity: the words are the whole of what it gave, and they match nothing.
`yoyo goals list` says which goals carry no identity, `yoyo goals attribution`
says which work items still match that way, and `yoyo goals reattribute` moves
those items onto the identity where the goal has one.

**An identity two active goals carry picks out neither.** It is reported by
`yoyo goals list` on stderr, carried into `yoyo release`'s goals check, and work
naming it is refused until one of the documents is corrected — choosing between
them would be exactly the guess identity exists to remove.

Only a `goals` artifact is read this way. A brief or a design with a `Goals`
heading of its own states no goals work may be attributed to — the goals are the
Lead Product Manager's document, and reading intent out of anything with the right
heading is how a design comes to authorize its own work.

The `Goals` heading is the heading whose **whole text** is `Goals`, at any
level. A title that merely opens with the word — `# Goals for V1` — is a title,
and a document with no such heading states no goals and is reported as stating
none. The exactness is load-bearing rather than pedantic: a title read as the
section opens the goals at the document's top level, and nothing written below
it can then end them by level, so everything in the file becomes something work
may be admitted under.

Each goal is one top-level list entry under that heading, and its statement is
**that entry's opening paragraph, rejoined onto one line**. Markdown is normally
hard-wrapped, so a goal written across several lines is the ordinary case: the
lines that continue it are joined with a single space, and the goal is recorded
whole rather than as its first line. The statement ends at the first thing that
is not more of the same sentence — a blank line, an unindented line, a nested
list entry, or the emphasized `*Supports: ...*` trailer naming what the goal
serves upstream. A trailer is recognised by the emphasis it **opens** with
rather than by where that emphasis closes, so a trailer hard-wrapped across
lines ends the statement exactly as a one-line trailer does; a line that opens
with an emphasized phrase and then carries on in plain text is the rest of a
wrapped sentence, and continues the statement. Everything after the statement
ends describes the goal rather than being part of it or being another, and a
heading below the
`Goals` heading divides the goals rather than ending them. The section ends at
the next heading at the same level or above, **or at any heading stating what
the product will not do** — a `Non-goals` heading ends it wherever it is
written, including nested inside it, so a document that files its non-goals
under its goals rather than beside them is read as ending the goals there rather
than as stating more of them. Attributing work to a non-goal is worse than
attributing it to nothing, so that bound does not depend on how the document was
nested.

**A wrapped goal is recorded whole and reported anyway.** Rejoining is what
closed the silent truncation that recorded only a goal's first line, so nothing
is refused over a wrap and work naming the whole statement still resolves. What
`yoyo goals list` says on stderr about one is that the rejoining is a reading of
the file rather than something the file states: the words an attribution has to
match exist only once the wrap is put back together, and an indent, or a wrapped
line that reads as the `Supports:` trailer, changes the recorded goal without
changing a word of it. A goal written on one physical line cannot be changed that
way, which is why the convention is worth holding rather than merely tolerating
the wrap. Only a goal in a document that still applies is reported, for the same
reason a broken link upstream is only reported for one: a goal in a superseded
document is not one work can name.

| Reported as | What it is | What it means for the work |
| --- | --- | --- |
| `attributed` | Names a goal an active goals artifact states. | The chain holds. |
| `unresolved` | Names something no active goals artifact states. | A claim that is wrong. Admission is refused, and an item already carrying one is reported for correction. |
| `unattributed` | Names no goal at all, and the tracker witnesses none was ever written. | Work admitted before this check existed. Grandfathered: reported, never refused, and nothing stops it running. |
| `lost` | Names no goal, on an item the tracker witnesses one was written onto. | A record that was destroyed rather than never made. Reported and failed. Where the witness kept the words, they are quoted and putting them back is a restoration rather than a fresh judgement; where it kept only that a goal was written, the words have to be recovered from outside the tracker. |
| `uncheckable` | The repository records no active goal, or the goals could not be read. | Nothing was checked, and it is said so rather than reported either way. Admitting work without asking is refused here, because an attribution nobody could check is not one the operator agreed to; the work is proposed instead and they decide, which is how a repository with no goals yet files the work of writing them. |

An identity that no active goal carries is `unresolved` and says so about the
identity, rather than falling back to the wording beside it: an item names one
goal, and reading its words as a second opinion would be the prose key coming
back in through the failure path. Where the match is on wording, nothing beyond
the folding above is guessed at — a paraphrase is `unresolved` with the goals
documents named, because deciding it was near enough is the inference a resolved
attribution exists to replace.

An attribution is written on the item as a `Goal served:` line — by the creation
that admitted the work, or by an `attribute` action afterwards, appended to what
the item already records rather than replacing it. The newest such line is the
item's current claim, so the goal an item was admitted under is never rewritten
and the record of how it came to be attributed survives. The line names the
goal's identity where the goal has one, and carries the words the document
states beside it for reading:
`Goal served: [traceable-chain] Maintain a traceable chain ...`. What the
harness never does is invent an identity: a goal that carries none is written
down in the words it was named by.

Every write that puts a goal into an item's notes also records that goal in the
tracker's own metadata for the item, under `yoyodyne_goal_recorded`. It exists
because the notes are what gets destroyed: `yoyo` only ever appends to them, but
anything else with the tracker's command line can replace them wholesale, and it
has. Six items lost the goal they were created under that way and read
afterwards exactly like work admitted before the check existed, which is the one
state nothing fails on. The witness is outside the reach of the write that does
the damage, so it survives to say both that an attribution was destroyed and
which one.

The notes stay the record. What an item serves is resolved from them and only
from them, and the copy in the metadata is never read as an answer — an
attribution the notes lost and the metadata answered for would report as intact
while the item stayed empty, which is the same silence arrived at from the other
side. The copy says what to put back, and putting it back is a `Goal served:`
line written onto the item like any other. A goal longer than a goals document
may state is witnessed without its words rather than stored cut in half.

**The witness covers a goal only from the moment it is written.** An attribution
made before this existed carries none, so replacing its notes reads as work
nobody ever attributed and does not fail the audit. `yoyo goals witness` closes
that gap: it records, on every work item whose notes state a goal and which
carries no witness, the goal those notes already state. It writes no attribution
and decides nothing — the statement is the item's own, copied to where a careless
writer cannot reach it — and it is worth running once after upgrading, and again
after any bulk import of work attributed elsewhere. It sweeps every status the
tracker holds rather than the queue, because the command that destroys an
attribution reaches a claimed or closed item just as easily, and most of the
losses on record were on items that had already closed.

**The audit reads as far as the sweep does.** `attribution` walks every status
the tracker holds — `open`, `in_progress`, `blocked`, and `closed` — so a
witnessed loss is a `lost` state it reports and exits non-zero for wherever the
item sits. It read the backlog alone until yoyodyne-ifd.276, and what that cost
is the reason it does not now: nine of the twelve recorded losses were on closed
items, so the slice the audit could not see is the slice the losses were actually
in, and two diagnoses of the same destruction were made wrong against a report
that said nothing about them. `--scope=queue` reads `open` and `blocked` alone
for the narrower question, and either way the report opens with the statuses it
read and the ones it did not.

**`yoyo goals reattribute` moves an attribution off the wording.** An
attribution recorded before goals carried identities names the words, and the
words are what the next amendment changes. It resolves what each item recorded
against the goals as they now stand and appends the same goal named by its
identity, deciding nothing about what any work is for. An item whose recorded
goal resolves to nothing, or whose goal carries no identity yet, is reported and
left exactly as it was rather than guessed at — the first is a claim somebody has
to correct and the second is a document somebody has to amend — and the command
exits non-zero while any item is left behind. `--dry-run` reports the same thing
and writes nothing, which is worth reading before a run over a live backlog. Like
the sweep it walks every status the tracker holds.

**`yoyo goals origins` backfills where older work came from.** Every admission
now records who asked for the work, and on whose behalf, as fields on the item in
the same write as the admission: the operator, a role's report (with the report
and the role that filed it), a role's own recurring pass, or the harness itself,
and the directive the work answers where there is one. An item admitted before
that reads as origin unknown. This sets those fields on such an item from what its
own notes already state — the role that admitted it, and the report or directive
it was admitted from — and from nothing else. The notes never said whether the
operator or a sweep asked, so an item citing neither a report nor a directive is
left unknown rather than guessed at, as is one whose notes name more than one of
anything; an item that already records an origin is never rewritten. `--dry-run`
reports what would be set and writes nothing.

`yoyo goals guard` is the same loss stopped rather than reported. Wired as a
`PreToolUse` hook on `Bash`, it reads the command an agent session is about to
run and refuses every recognized `bd update <id> --notes` replacement, even
one carrying a `Goal served:` line. An item's notes are append-only: preserve
every earlier note and add a correction with `--append-notes`. It decides from
the command line alone and never reads the item or opens the tracker. It
also refuses `bd update <id> --status=...` with no `--append-notes` on the same
command: a status set with no note saying what moved it is the other silent
rewrite, the one that on 2026-09-18 reopened two items closed on confirmed merges
and released two escalations with nothing on any of them saying so. The
direction of a move is not readable from the line, so a note is asked for on
every status set there; `--claim` is not a status set and passes. The
harness gives it to every developer run it makes on the Claude Code backend,
which is the backend that passes the hook; any other agent session is covered
only by wiring the same command into that session's own hooks. It is passed to
the provider rather than enforced by the harness, so where the hook does not fire
the command runs as it did before and nothing reports that it did.

```sh
yoyo goals list          # the goals work may be attributed to, their identities, and where each is stated
yoyo goals attribution   # what each work item the tracker holds says it is for
yoyo goals witness       # witness the goals already recorded on work items
yoyo goals reattribute   # move an attribution off the wording and onto the goal's identity
yoyo goals origins       # record who asked for work admitted before admissions recorded it, from its notes
yoyo goals guard         # refuse wholesale notes replacement or a status set with no note
```

`attribution` exits non-zero for an item whose attribution is `unresolved` or
`lost`, and zero for one with none. That asymmetry is the decision, not an
oversight: an item admitted before goals were checked is somebody's to attribute,
and a rule that failed every one of them would stop a backlog to close a gap that
has cost nothing yet. An item that lost the goal it recorded fails for the
opposite reason — it passed the check, and what is wrong is that the record of it
was written over. Attributing one is a judgement about what the work is for, so it
is the Lead Product Manager's to make in conversation and there is no command here that
makes it.

Work that has closed is held to one half of that rule. `lost` fails there like
anywhere else, because the record was destroyed and the witness holds the words
to put back. `unresolved` is counted and named on closed work and does not fail:
the item named what the goals stated when it was admitted, the work is finished,
and the ordinary way it stops resolving is a goal reworded afterwards — which is
what `yoyo stale` reports rather than a claim anybody can now correct. Both halves
are printed in the report, so neither is a rule to be inferred from an exit code.

That leaves a pass still owed. When the check arrived, Yoyodyne's own backlog
carried one attribution across seventeen open items, and **the rest are still to
be attributed**: until they are, `yoyo goals attribution` reports most of the
queue as naming no goal, and that is the queue's real state rather than a
reporting artefact. Grandfathering is what keeps the work running while the pass
is outstanding; it is not a substitute for making it. The pass is made by the
Lead Product Manager in conversation, working from `yoyo goals attribution` and using
the `attribute` action on each item — which appends, so nothing already recorded
is lost.

## What a change upstream leaves stale

A goal can be amended while the designs that serve it and the work admitted
under its old wording carry on unchanged. The amendment is somebody exercising
authority over their own document; the silence after it is the problem, and
`yoyo stale` ends it.

```sh
yoyo stale          # what a change upstream left unanswered downstream
yoyo stale --json   # machine-readable
```

| Reported as | What it is |
| --- | --- |
| a document | An artifact something upstream of it — through `supports`, as far as the chain runs — changed after the artifact itself was last revised. |
| open work | An admitted item whose goals document, or anything upstream of it, changed after the item was admitted. |
| a contradiction | Two active documents of the product's intent that say opposite things, both named: two active briefs; one statement one document states as a goal and another rules out as a non-goal, matched by the goal's identity where both carry one and otherwise by the words; or one goal identity two documents give to different goals. |

A contradiction is reported only where the documents' own structure makes it
readable — whether two paragraphs mean opposite things is a reading for a person
or the owning role, and a guess at it would be a report nobody could trust. Like
the rest, it refuses nothing and `yoyo stale` still exits zero: which document is
right is the owner's decision. `yoyo conformance` carries each one as a note on
its staleness survey.

A change is an `amended`, `superseded`, or `retired` revision. A `created` one is
not: a document that did not exist cannot be what anybody was working from. Each
report names the document that changed, when, the role whose authority it
happened under, and the reason it recorded — a rewording and a reversal of
intent are the same event without the reason.

Two things are never reported. An artifact that is itself `superseded` or
`retired` stated what was intended and stopped, so it is not asked to answer for
what happened upstream afterwards. An item naming no goal, or one the goals do
not state, has no reference to follow at all; that is a gap in the chain, and it
is [reported where attributions are](#goals-and-the-work-attributed-to-them)
rather than restated here. The counts say how many admitted items were judged
and how many were not, so what this could not answer for is never silence.

**Nothing is stored.** Staleness is a comparison over records that already
exist — each artifact's revision log, and the tracker's record of when an item
was admitted — rather than a mark somebody writes. So a document edited by hand
counts exactly as one amended through the harness, a process that dies between
the amendment and anything else leaves nothing unmarked, and there is no second
account of staleness that can disagree with the documents. What it costs is
where it clears: an artifact stops being reported once its owner records a
revision later than the change, which is the durable record that somebody looked
at it, and a work item carries only its admission time, so a stale item stays
reported until it is closed. The tracker's own modification time is deliberately
not used for this — it moves when the harness records what a run cost, and
staleness that vanished because a price was written would be a signal nobody
could trust.

**Stale is not cancelled.** Nothing is stopped, closed, blocked, or reordered,
and the command exits zero whatever it finds. A change to a goal's wording is
frequently not a change to what the work should do, and failing a build over an
edit would teach an operator not to edit. What happens to stale work is the
operator's decision or the owning role's; this surfaces the condition.

A tracker that cannot be read costs the work half of the report rather than all
of it: the documents still report, and the report says the queue was not read
instead of rendering it as one nothing has moved under.

## Architectural invariants

The architect's durable constraints live in a second configured directory:

```yaml
product:
  id: example
  repository: .
  invariants: docs/decisions/invariants   # the default; nothing to write down if you use it
```

An **invariant** is a cross-cutting constraint that outlives the work item that
established it — the kind a later change breaks while its own work looks
correct. One Markdown file per constraint, named by its id, with the metadata in
frontmatter and the constraint itself in two required sections:

```markdown
---
id: one-writer-per-item
title: One process at a time acts on an in-flight work item
status: active
established_by:
    - yoyodyne-ifd.2.7
scope:
    - internal/runstate
revisions:
    - action: created
      by: architect
      at: 2026-08-17T12:00:00Z
      reason: extracted from the decision that added the reservation
---

## Must hold

Every entry into an in-flight run takes the run's exclusive lease first.

## Why

The lease is the only thing keeping two processes off one in-flight item.
```

Only files directly in this directory are read, because the file name is the
identity; a `.md` filed in a subdirectory is reported rather than read. One name
is not read at all: `README.md` is the directory index this home carries like
every other, so it is skipped rather than reported as a malformed constraint, and
`yoyo invariant create readme` is refused because a constraint written there
would be one nobody is held to. `scope`
is optional: an invariant without one is repository-wide and reaches every work
item, and a scoped one is delivered when the work item's prose — or, for the
reviewer, the change itself — names a path it constrains. A missing directory is
not an error; the project simply records no invariants.

Writing these by hand works, and `yoyo invariant create|amend|retire` is the
supported path: it validates the constraint, records who changed it and why, and
refuses every role but the architect. Retirement sets `status: retired` and
records the reason. The file stays and stops being delivered, because an
invariant that vanished leaves whoever read it last month with no way to find out
it was lifted.

A file in this directory that cannot be read as an invariant is **reported and
not delivered**, which is the opposite of how a malformed specification is
handled and deliberately so: half a constraint is not one a developer can be held
to. `yoyo invariant list` names it on stderr, the gap is stated in the prompts
the harness builds, and it is recorded on the work item, so a set that is missing
something never looks complete.
