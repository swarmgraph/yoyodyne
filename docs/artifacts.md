# Artifacts, goals, and invariants

*For an operator maintaining the brief, the goals, the designs, and the
invariants. Part of [yoyo's documentation](../README.md#further-reading).*

## Artifact identity

The documents upstream of a work item — the brief, the goals, the designs and
specifications under `product.designs`, and the decision records under
`product.decisions` — each carry a stable identity in frontmatter: an id, a
kind, a lifecycle status, what they support upstream, and a revision log. It is
the same model the invariants use rather than a second one beside it. The file
name is the id, an id that disagrees with its file name is refused, and two
files claiming one id refuse both, each naming the other.

```sh
./bin/yoyo artifact list
./bin/yoyo artifact show v1-goals
```

**Where these documents are kept.** By default they are committed in the
project's own repository, beside the code they govern. A project whose
configuration is [kept outside its
repository](configuration.md#keeping-the-configuration-outside-the-repository)
may keep them — the brief, the goals and every other specification, the designs,
the decision records, and the invariants — in a [companion intent
repository](configuration.md#keeping-the-intent-outside-the-repository-too)
instead: a Git repository of their own in the project's directory in the machine
home, which `yoyo init --external --intent` creates and `--intent-from` clones.
The homes keep their names; every reader of them, from `yoyo artifact list` to
the context a developer and a reviewer are handed and the management
conversations' picture of the product, resolves them in the companion repository
where the configuration names one and in the project's repository otherwise.
What they are does not change: a write that lands, an approval, and a revision
recorded from the command line land in that repository's checkout, for you to
commit there. The command says so, naming that repository rather than the
project's checkout; no run starts from it, so it does not say a run will refuse
to start over the uncommitted file the way it does for a write in the project's
own checkout.

**The specifications directory is authoritative whole.** Everything filed under
`product.specifications` — `docs/product` by default — is authoritative product
intent, not only the brief and the goals: the non-goals, and any other document
the Lead Product Manager files there, count exactly as much. Every role reads all
of it, labelled as such. The management conversations are briefed with every
document there, and every developer and reviewer is handed every document there
after the work item it is given. A `README.md` there is read as the directory
index it is, and what it says about who owns the documents filed there is
delivered as a rule. Each document there is an artifact with the identity and the
approval the goals have, and two of them that contradict each other are
[reported by `yoyo stale`](#what-a-change-upstream-leaves-stale), naming both. The
[configuration guide](configuration.md#product-specifications) has what is carried
where and what happens to a document that does not fit.

Your approval of one of these documents lives in the same frontmatter, and it is
recorded against the revision it was given for:

```sh
./bin/yoyo artifact approve v1-goals --reason "approved in conversation on 2026-08-17"
```

That is what makes an approved goal and a draft one different things to
everything downstream, instead of two identical documents whose difference lived
in a chat log. Because the approval names a revision and the revision log is
append-only, a document amended after you approved it reads as
approved-and-amended-since rather than as approved — the approval still stands
for what you gave it for, and the document as it now reads is not that.

**An identity revision is not an amendment.** Giving a goals document's goals
[identifiers](#goals-and-what-work-serves-them) — adding the bracketed name an
entry opens with, or changing one — changes no goal's words, so it is recorded
as an `identified` revision rather than an `amended` one. Your approval stands
through it, admissions against its goals are exactly what they were, and
[`yoyo stale`](#what-a-change-upstream-leaves-stale) reports nothing for it. It
is the owning role's write and needs nothing from you:

```sh
./bin/yoyo artifact identify v1-goals --body-file v1-goals-body.md \
  --reason "yoyodyne-ifd.344 - goal identifiers recorded"
```

The body is the document below its frontmatter as it should now read, and it is
refused unless the only difference is those identifiers — one changed word, one
added line, and it is an amendment, which this will not record. It acts with the
authority of the role that owns the document, as `yoyo invariant` acts with the
architect's, and like an approval the write lands in your checkout uncommitted.
A document edited by hand can record `identified` itself, as it can record any
revision; what the command adds is that its path to the action is one that
checks.

**Only a change of what the goals admit comes back to you.** A change is of
fundamental intent if the goals would afterwards admit work they refused before,
or refuse work they admitted; that is yours to approve. Anything else is a
consistent rewording or a decision the goals already delegate, and it is the
Lead Product Manager's to make. Which one a change is, is said on the amendment
itself: one the Lead Product Manager records as `intent: consistent`, with a
reason that opens with the work item that directed it —
`yoyodyne-ifd.437.11 - the autonomy goal names the Lead Product Manager` — leaves
the goals document approved, leaves admissions against its goals exactly as they
were, and is listed by [`yoyo stale`](#what-a-change-upstream-leaves-stale) as a
rewording rather than an amendment. One recorded as `intent: fundamental`, and
one that does not say which it is, reads as amended-since and puts admissions
back to you, as every amendment did before: the default is yours, and it takes
the Lead Product Manager's recorded claim to move off it. The
[configuration guide](configuration.md#approving-a-document) says what else the
record has to carry for the claim to count.

What is
asked of you is your configuration's to say: `approvals.brief` and
`approvals.goals` are `human`, `approvals.designs` is `automatic`, and a decision
record is an account of how something was decided rather than a statement of
intent, so nothing asks you to approve one. A document filed in the
specifications directory that is neither the brief nor the goals is asked for as
the goals are, under `approvals.goals`, whatever its kind.

**Recording an approval gates one thing: what reaches the work queue.** An
unapproved document still loads, still governs what is downstream of it, and
stops nothing that reads it, and approving writes nothing but the approval — the
document itself stays the owning role's to change. What your approval of the
goals decides is whether work serving them is admitted without asking you, which
is [`approvals.work_items`](configuration.md#what-reaches-the-queue) to say:
it is `human` until you set it otherwise, and every item is put to you. Set it to
`automatic` and your approval of the goals document is what lets work serving
those goals into the queue — so a goals document nobody approved, and one amended
since you approved it, are documents nothing is admitted under — an identity
revision is not an amendment for this, so recording identifiers puts nothing
back to you, and neither is a consistent rewording the Lead Product Manager
recorded. Everywhere else
an amendment after approval changes what is reported about a document rather than
what is allowed. The
[configuration guide](configuration.md#approving-a-document) has the schema
and what is refused.

**An approval given on the command line is written into your checkout and stops there**, and the command
says so as it writes: the document is now an uncommitted change, a run against
that checkout refuses to start while it is, and committing it is yours under your
own identity. The checkout is named in so many words, because which one the write
landed in follows the configuration the command read rather than where you are
standing. That is the settled shape rather than an unfinished one. The only
ways into the target branch are a reviewed promotion and your own hand, so a
harness-made commit would be a promotion by another name carrying tree state
nothing reviewed — and leaving the write in your tree keeps what a document is
down to two readings: committed, or an edit of yours that is visibly holding
the runs up. It is said at the moment of the write because the alternative is
meeting it as a refusal from whatever you run next.

A document in one of those directories with no usable identity is named on
stderr rather than governed under a guessed id, so a home you have not given
identity to yet says so. Nothing else changes for it: a specification with no
frontmatter is still read as product intent, because refusing intent somebody
wrote down is worse than reading it and saying its identity is missing.

Who may change one of these documents is in the code rather than in a persona.
The Lead Product Manager owns the brief and the goals, the architect owns the designs,
specifications, and decision records, and the development manager owns no
document at all. Creating, amending, superseding, and retiring an artifact each
refuse a role that does not own the kind, the way the invariants already do,
and that is the path a document [written from a
conversation](#writing-a-document-from-a-conversation) takes. What runs on every
load is the other half: a document
whose revision log records a change by a role that does not own it is reported,
naming the file and the entries that crossed. It is reported rather than refused
because the log is append-only, so losing the document would leave one that could
neither load nor be corrected. None of it constrains you: the boundary is between
agent roles, and you direct any of them.

A role that meets that boundary is not left with nothing to say. It proposes the
change instead, the proposal reaches the owner and you, and only a decision on it
is ever recorded — see [what agents propose changing, and who
decides](reporting.md#what-agents-propose-changing-and-who-decides).

A revision's reason is also read back to the tracker, in one narrow way. A work
item a role's conversation carries — admitted with `executor:
conversation:architect`, say — is closed by the harness when a document that
role owns records a revision, by that role, whose reason *opens* with the
item's identifier: `yoyodyne-ifd.330 - side conversations designed`. Opening
with it is the convention and the whole of the judgement; a reason that mentions
an item further in is about something else and closes nothing. So when the
revision is the landing of a tracked item, write the identifier first, and when
it is not, do not. [How work flows](work.md#letting-the-harness-choose-the-work)
says what the close records and how it is undone: reopening the item with a
note, which holds because the item carries the revision it was closed on and
is not closed on it again.

The chain that identity makes expressible is then checked, every time the
artifacts are loaded: a `supports` entry naming an id no artifact answers to is
reported with both ends named, and an artifact that nothing connects back to the
brief is reported as an orphan. Neither refuses the document — a broken
relationship is a name to correct, not a reason to lose what somebody wrote. The
brief is the root and a decision record is not downstream of intent, so neither
is asked to support anything. The
[configuration guide](configuration.md#traceability-references-and-orphans)
is the reference for the schema, the fields, and what is reported.

### The index at the door of each home

`yoyo init` puts a `README.md` at the door of every artifact home — the
specifications directory and the goals under it, the designs, the decision
records, and the invariants — saying three things about that directory: what is
filed there, which agent owns it, and whether you may edit one of its documents
by hand. The answers are the ones the harness already enforces rather than a
policy the file invents: a role that is not the owner proposes an amendment,
your own edit is reported rather than refused, and what a change leaves stale
downstream is what `yoyo stale` reports. An index states no intent, so the Lead
Product Manager reads it under a heading of its own and never counts it as a
specification. An index that is already there is left exactly as it is,
`--force` included, because it is your prose rather than something `init`
generated. [`yoyo doctor`](operations.md#checking-the-installation) reports one
that is missing or has stopped answering, and `yoyo setup` offers to write it,
which is how a project configured before these existed gets them.

## Writing a document from a conversation

A document the owning role drafted used to reach the repository by hand: fenced
Markdown in a reply, your approval in prose, and then you or an agent of yours
working out the path, writing the frontmatter, and committing it. The drafted
content was rarely the part that went wrong — the transcription was.

So a document is written the way work is proposed. Ask the Lead Product Manager
for the goals or the architect for a design, and what comes back is prose you
read plus a typed action carrying the document. The harness checks ownership
and the document's home before confirming anything.

With an automatic policy, such as `approvals.designs: automatic`, the harness
confirms the saved document without asking you. It records `by: harness` and
the policy name against the revision, then opens a run carrying exactly the
owning role's document, with permission to change only its file. No developer
rewrites it. The configured checks and an independent reviewer judge that
candidate, and it lands through the normal integration path. The primary
checkout is left clean. The run's record says it was chosen by the owning
conversation, naming the conversation, the document, and the turn it was
written in.

That needs `approvals.integration: automatic` too, because a reviewed run lands
only where the project integrates automatically. Where it is `human`, nothing
is confirmed by policy and you are asked as below, whatever the document's own
policy says. A project that keeps its documents in a
[companion intent repository](configuration.md#keeping-the-intent-outside-the-repository-too)
has no reviewed run into that repository yet, so a document its automatic
policy confirms is held rather than landed: it stays confirmed and saved, the
owning role is told once what holds it, and it is tried again at each later
message. It is not put to you; what your policy puts to you anyway is, and your
confirmation writes it into that repository's checkout. A document confirmed while integration was automatic, whose run
had not started when the setting changed, is put to you the same way, and the
owning role is told.

A run that cannot start or finish — the primary checkout has uncommitted
changes, say — never holds up the conversation. The confirmed document stays
saved, the owning role is told what is holding it, and it is tried again at the
next message without being written again.

A run that cannot start because every developer slot is taken is not a failure.
The document is kept as waiting for a slot, in the run store beside the runs it
waits behind, and the owning role is told once. A watching session
(`yoyo work --watch`, which `yoyo start` runs) starts it in the next slot that
frees, ahead of any new development run and without waiting for a message in
the conversation; a run already going is never stopped for it. The conversation
still offers it at its next message, and whichever reserves the slot first runs
it — the same document is never given two runs, across a restart too.
`yoyo status` lists a waiting document under the not-startable line, beside the
ready work waiting for a slot, naming the document and the role that owns it.

This also applies to documents already waiting in a conversation's store:
when that conversation resumes, the harness uses their saved identities and
content before asking the role to write anything else. Confirmation and the
complete candidate are saved before the run starts, so a restart continues
the same run rather than losing the document or landing it twice.

A policy that is not automatic retains operator confirmation. A document of
the product's intent — anything governed by `approvals.brief` or
`approvals.goals`, which takes in the non-goals, the operating rules, and every
document filed in the specifications directory — also retains it unless it is a
revision the Lead Product Manager recorded as `intent: consistent`, with the
directing work item opening the reason. A new document of intent is always put
to you.
For these documents, you are shown what would happen and the document itself,
and asked:

```
document document-4.1 · create v2-goals (goals) in docs/product
  title: What v2 is for
  because: drafted with you in this conversation

  # Goals
  ...

create v2-goals (goals) in docs/product? [y or yes writes it and records your
approval in it; anything else declines, and is kept as the reason]
```

On your `y` the harness performs the write itself: it files the document in the
artifact home, generates the frontmatter the contract requires, records the
revision under the role that wrote it, and records your approval against that
revision. `yoyo artifact show v2-goals` then reads back exactly what any
hand-written document reads back as, because it is one. Anything else declines,
and what you said is kept as the reason.

Nothing about that widens what a role may do. The write goes through the same
ownership boundary every other change to these documents goes through, so the
architect cannot write the goals and the Lead Product Manager cannot write a
design — each proposes to the other instead. Each kind also has one home and is
written only there: the brief, the goals, the non-goals, and the operating
rules go under `product.specifications`, designs and specifications under `product.designs`,
and decision records under `product.decisions`. A role is told which of those its
own kinds go in rather than being handed the list, so a design filed under the
Lead Product Manager's home is refused even though that is an artifact home — it
is not the one a design is filed in.

A kind the role does not own, a document filed anywhere but its kind's home, a
revision of a document that belongs to another role, a revision of one that was
superseded or retired, and a block the harness cannot read are all refused before
anything is written, and you are never asked to approve one. A revision naming a
document nothing records is refused the same way, saying that a document which
does not exist yet is created rather than revised — and a creation over an id
something already answers to is refused for the mirror of that reason. A role
that owns no document at all — the development manager, the developer, the
reviewer — cannot write one under any circumstances.

A readable document submission that the write gate refuses does not fail the
conversation or its scheduled pass. The other tracker actions, reports, and
memory writes continue. The role receives the refusal reason and the identifiers
and names of any waiting documents in a further round of the same message. If
that round submits another refused document, its refusal waits in the durable
conversation for the role's next turn instead of starting another round. The
limit remains two waiting documents; a refusal confirms none of them.

A revision is the same action, carrying the document whole:

```sh
./bin/yoyo chat --message "revise the second goal to name the adoption path"
./bin/yoyo chat --message "approve document-5.1"
```

Deciding it as its own message is what makes this work outside an interactive
conversation: the drafted document is recorded with the conversation, so it
survives the process that wrote it and your approval names it hours later. An
approval sent as a message has to name the document — a bare "yes" decides
nothing, because a message is not an answer to a question you were just asked.
What is left undecided when a conversation ends is named on the way out.

**A document that something judged returns to its owner.** When a check ran
and failed, the independent reviewer refused the document, the change touched
a path it may not, or the target document changed after confirmation, the
owning conversation receives the review findings, failing check and its output,
or conflicting paths. The owning role is named as the one to revise it; no
developer or operator is asked to repair it. A revised submission opens a fresh
run for that content. After three such returned runs for the same document in
that conversation, automatic publication stops. The owner receives that reason
and must revise its plan; further submissions are saved as conversation
events but open no more runs for that document in that conversation.

**A run that judged nothing is run again.** A run stopped by something that
says nothing about the document — a check stopped by a time limit, a forge or
network error, the machine or the harness stopping — is not a return. The
harness starts another run of the same confirmed text at the next message, up
to three runs in all, and the owner is told it does not need to write anything
again. After a check time limit the next run waits until fewer developer runs
are going than when the checks ran out of time, and the run's record says what
it waited for. A retry that finds every developer slot taken waits
for a slot the way a first run does, and a watching session starts it as one
frees. If all three runs stop that way, the last is handed to the
development manager as a stopped run, with the confirmed text kept on its
record, rather than back to the owner. A document whose publication had already
stopped over such runs is published again from its kept text the next time its
conversation is opened. Each run's start and end is also noted on the work item
named at the start of the document's revision reason, where the tracker holds
one.

**A write confirmed by you still stops at your working tree.** This is the
existing human confirmation path: the document is an uncommitted change in
the checkout the configuration points to, and committing it is yours under
your own identity. The printed result names that checkout and file, and
`--json` carries it as `pending_commit`. This does not apply to documents
confirmed under an automatic policy.

## Goals, and what work serves them

The last link of the chain is the goal a work item names, and that link is
closed by reading the goals out of the goals artifacts themselves: every entry
under a goals document's `Goals` heading is a goal work can be attributed to.

Each goal carries a stable identity, and that identity is what an attribution
resolves by. It is written in square brackets at the start of the entry:

```markdown
- [traceable-chain] Maintain a traceable chain from the product brief through goals, designs, work, code changes, and verification.
  *Supports: every change traces to intent somebody approved.*
```

The identifier is assigned once, never reused, and unchanged by every re-wording
of the sentence beside it. The words are what you read and what a work item
displays; they are not what the match depends on, so amending a goal is editing
a sentence rather than renaming a thing — no item attributed by identity is
orphaned, and no admission naming that identity is refused. That was not true until
yoyodyne-ifd.344: attribution matched on the exact prose, and three amendments
in three weeks orphaned items or refused admissions, the last of them found
because four admissions failed at intake in front of the operator.

A goal stating no identifier is matched on its words alone, which is the older
arrangement and the one a re-wording breaks. `yoyo goals list` says which goals
those are, and `yoyo goals attribution` says which work items still match that
way.

One thing identity does not reach: an admission that quotes a goal's *earlier*
wording and carries no identifier still resolves against nothing, because the
words are the whole of what it gave. What closes that is naming the goal by its
identity, which is what the roles are asked for and what the harness records on
the item.

```sh
./bin/yoyo goals list          # the goals work can be attributed to, their identities, and where each is stated
./bin/yoyo goals attribution   # what each work item the tracker holds says it is for
./bin/yoyo goals witness       # witness the goals already recorded on work items
./bin/yoyo goals reattribute   # move an attribution off the wording and onto the goal's identity
./bin/yoyo goals origins       # record who asked for work admitted before admissions recorded it, from its notes
./bin/yoyo goals guard         # refuse wholesale notes replacement or a status set with no note
```

No command there decides what a piece of work is for, for the same reason no
command writes an artifact's content: that judgement is a product one, made by the Lead Product
Manager in the conversation where you can see it. What the harness owns is
resolving the claim. Three of those commands do write — `witness`,
`reattribute`, and `origins` — and none writes a judgement: the first two record
the goal an item already names, one into the tracker's metadata and one by the
goal's identity, and `origins` copies where an item's notes say it came from
into the tracker's metadata. An item that names no goal at all and one that names a
goal your goals do not state are reported apart and treated differently, because
they are not the same thing to do: the first predates the check, is somebody's
to attribute, and never stops the work running; the second is a claim that is
wrong, and it is what `yoyo goals attribution` exits non-zero for.

There is a third way to record no goal, and it is reported apart from both.
`yoyo` only ever appends to an item's notes, but anything else with the tracker's
command line can replace them, and a replacement that does not carry the goal
forward destroys it — which has happened twice, to six items at once and then to
twelve more, and read afterwards exactly like work admitted before the check
existed. So every write
that puts a goal into an item's notes also records that goal in the tracker's
metadata, where replacing the notes cannot reach it. An item carrying that
witness and no goal has lost one rather than never had one: it is reported as
`lost`, it exits non-zero, and the words it lost are quoted so putting them back
is a restoration rather than a fresh judgement about what the work is for. The
notes stay the record — what an item serves is resolved from them and never from
the copy, because a loss the report answered out of metadata would read as intact
while the item stayed empty.

The witness covers a goal from the moment it is written and no earlier, so an
attribution made before it existed is protected by nothing. `yoyo goals witness`
sweeps that up: it records, on every work item whose notes already state a goal
and which carries no witness, the goal those notes state. It decides nothing —
the statement is the item's own — and it is worth running once over an existing
backlog. It walks every status the tracker holds rather than the queue, because
the command that destroys an attribution reaches a claimed or closed item just
as easily, and most of the losses on record were on items that had closed.

`yoyo goals reattribute` is the same kind of sweep for the same kind of gap, on
the other side of it: an attribution recorded before goals carried identities
names the words, and the words are what the next amendment changes. It resolves
what each item recorded against the goals as they now stand and appends the same
goal named by its identity, so nothing about what the work is for is decided —
the goal is the one the item already named. An item whose recorded goal resolves
to nothing, or whose goal carries no identity yet, is reported and left exactly
as it was rather than guessed at, and the command exits non-zero while any item
is left behind. `--dry-run` reports what it would do and writes nothing, which
is worth reading before a run over a live backlog.

The witness is what survives a loss; `yoyo goals guard` stops the write that
causes one. It reads a tool call an agent session is about to make and
refuses a shell command running `bd update <id> --notes`, which is where every
recorded loss came from. It refuses every recognized replacement, even one
carrying a `Goal served:` line. An item's notes are append-only: preserve every
earlier note and add a correction with `--append-notes`. It decides from the
command line alone and never reads the item, which is what keeps it from waiting
on a locked tracker in front of every command an agent runs. The same guard refuses
`bd update <id> --status=...` with no `--append-notes` on the same command: a
status set with no note saying what moved it is the other silent rewrite, and on
2026-09-18 it moved four items backwards — two closed on confirmed merges, two
escalated to a person — with nothing on any of them saying so
(`docs/diagnoses/yoyodyne-ifd-392-status-rewrites-by-the-carry-out-queue.md`).
The direction of a move is not readable from the command line, so a note is asked
for on every status set there; `--claim` is not a status set and passes. The
harness's own writers already carry the account on the same invocation, so the
rule costs them nothing. The harness gives the
guard to every developer run it makes on the Claude Code backend, which is where
the hook is passed; an interactive session in your own repository gets it by
wiring the same command as a `PreToolUse` hook on `Bash` in
`.claude/settings.json`:

```json
{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"yoyo goals guard"}]}]}}
```

The audit reads as far as the sweep does. `yoyo goals attribution` walks every
status the tracker holds — `open`, `in_progress`, `blocked`, and `closed` —
because nine of the twelve recorded losses were on closed items, and an audit
that could not see them reported nothing about any of them: that blindness is why
the intact counter-example was missed and the same destruction was diagnosed
wrong twice. `--scope=queue` reads `open` and `blocked` alone, which is the
narrower question of what the work still waiting traces to. Either way, the
report opens with the statuses it read and the ones it did not, so a clean tally
is never mistaken for a slice nobody looked at.

Work that has closed is held to one half of the failure rule. A destroyed
attribution fails there like anywhere else — the record was written over, the
witness holds the words, and putting them back is something somebody can do. An
item naming a goal no goals document states is counted and named on closed work
and does not fail: it named what the goals stated when it was admitted, and a
goal reworded after it closed is what [`yoyo stale`](configuration.md#what-a-change-upstream-leaves-stale)
reports rather than a claim anybody can now correct.

The two halves of the protection are not interchangeable, and neither of them
covers everything. The guard covers the sessions it is wired into and says
nothing about a command typed anywhere else; it is also passed to the provider
rather than enforced by the harness, so a session where the hook does not fire
runs the command exactly as it did before and nothing reports that it did. The
witness is what survives a write the guard did not see: it keeps the words on
every item the sweep walked, so the audit can say an attribution was destroyed
and quote what it was.

A goals document nobody can read goals out of — one with no `Goals` heading, or
with nothing stated under it — is named on stderr rather than quietly shrinking
the set work may be attributed to, and a repository with no active goals is
told that nothing was checked rather than having its queue reported as
unattributed.

The link the other way is read too. A goals document's frontmatter says the
document serves the brief; it says nothing about which of the brief's goals any
one entry in it reaches, and that is the link a goal states in an emphasized
`*Supports: ...*` line directly under it — indented with the entry and with no
blank line between, or the trailer is not read as part of the goal. `yoyo
goals list` resolves each one against the
goals the brief itself states — named by the claim each opens with — and prints
it beside the goal. A goal that names nothing upstream, and one naming a brief
goal the brief does not state, are reported on stderr; a brief that states no
goals at all is reported once, naming the brief, rather than against every goal
below it. Nothing is refused over a broken link: the goal is still what the
document states and work naming it still resolves, because what is wrong is the
chain above it rather than the goal.

The listing itself is laid out to be read: one goal to an entry, a blank line
between entries, and where the goal is stated indented under it. On a terminal
the statement is bold and the lines about it italic, and that emphasis is an
addition and never the meaning — the same discipline everything else the harness
dresses holds to. What separates two goals is the blank line, what says a line is
about the goal above it is the indent and its label, and what says a goal is no
longer active is said in words, so a listing piped to a file, read with
`NO_COLOR` set, or shown on a terminal that says it is dumb says exactly what it
says dressed. `--json` carries none of it. The listing closes with a line naming
what these goals sit underneath — the goals the product brief states, and the
file to open for them — because read on its own it stops one link short of where
the chain begins.

A goal hard-wrapped across physical lines is reported on stderr the same way,
and is likewise not refused. The statement is rejoined and work naming it still
resolves — recording only the first line was a silent truncation that corrupted
every wrapped goal at once, and rejoining is what closed it. What the report says
is that the rejoining is a reading of the file rather than something the file
states: an indent, or a wrapped line that reads as the `Supports:` trailer,
changes the recorded goal without changing a word of it, and a goal written on
one line cannot be changed that way.

## What a change upstream leaves stale

Amend a goal and the documents that serve it, and the work admitted under its
old wording, may no longer be right — and until now nothing said so. `yoyo
stale` says it:

```sh
./bin/yoyo stale          # what a change upstream left unanswered downstream
./bin/yoyo stale --json   # machine-readable
```

An artifact is reported when something it traces to upstream — the goal a design
serves, the brief a goal serves, through as many links as the chain runs —
recorded a change after that artifact was itself last revised. An identity
revision — goals given identifiers, no words changed — counts on neither side:
it is not a change anything downstream was built on, and it is not the record
that the document's owner went over one. An admitted work
item is reported when the goals document stating the goal it serves, or anything
upstream of that, changed after the item was admitted. Each one names what
changed, when, under whose authority, and the reason that change recorded, which
is what tells a rewording apart from a reversal of intent. A rewording the Lead
Product Manager recorded as consistent with intent is listed as `reworded,
consistent with intent` rather than as `amended`, so it does not read as an
amendment still waiting on you; it is listed at all because work admitted under
the old wording may still read differently.

Two documents of the product's intent that contradict each other are reported
too, naming both, because every role is handed both as authoritative and would
otherwise settle it by whichever it read last. What is reported is what the
documents' own structure makes readable: two active briefs, one statement one
document states as a goal and another rules out as a non-goal, and one goal
identity two documents give to different goals. Whether two paragraphs of prose
mean opposite things is a reading for you or the owning role, and is not guessed
at.

Nothing is stored to make this true and nothing has to be marked. The documents'
own revision logs and the tracker's record of when each item was admitted
already say it, so any reading reports the same thing, a document edited by hand
counts exactly as one amended through the harness, and there is no second
account of staleness that can drift from the documents it describes. What it
costs is that it clears only where those records say so: an artifact stops being
reported once its owner records a revision later than the change — the durable
record that somebody looked — and a work item carries only its admission time,
so a stale item stays reported until it is closed.

**Stale is not cancelled.** Nothing is stopped, closed, blocked, or reordered,
and `yoyo stale` exits zero whatever it finds. A change to a goal's wording is
frequently not a change to what the work should do, and a harness that failed a
build or cancelled a queue over an edit would teach you not to edit — which
costs more than the divergence this reports. What to do about each of these is
yours to decide, or the owning role's.

A tracker that cannot be read costs the work half of the report and not the
whole of it: the documents still report, and the queue is stated as unread
rather than rendered as one nothing has moved under.

## Architectural invariants

Some constraints outlive the work item that established them: a contract one
change created that later changes must not break. Those live under
`product.invariants` — `docs/decisions/invariants` by default — as one Markdown
file per constraint, named by its id, carrying what must hold, why, what
established it, and a recorded revision history. They belong to the architect,
and it is worth being precise about which half of that is enforced and which is
not. Recording, amending, and retiring one goes through a single code path that
refuses every role but the architect, so `yoyo invariant` and any future
architect agent are bound by it and no other role has an authorized way to write
one. A developer, though, has a shell in its worktree, exactly as it does for
the [pushes and merges the harness never routes through an agent](designs/v1-harness-design.md#what-is-enforced-and-what-is-not):
what stands in the way of it editing an invariant is its contract, which forbids
it and tells it to propose the amendment instead, and the reviewer, which is
told that a change creating, amending, retiring, or editing one is a finding.
Treat "only the architect changes an invariant" as the authorized path plus a
caught one rather than as something the harness makes impossible.

They exist because a change whose own work is correct can still break something
outside its scope, and they are only worth having if they reach the people doing
the work. The harness selects the ones relevant to a work item and delivers them
into the developer's context and the reviewer's evidence, so nothing depends on
whoever wrote the bead having remembered the constraint. Selection is what keeps
that affordable: an invariant with no declared scope is repository-wide and
reaches every item, and a scoped one is delivered when the evidence names a path
it constrains. The developer's evidence is the work item's own prose; the
reviewer's adds the change itself, so an invariant scoped to code the item never
mentioned still reaches the gate that judges the change that touched it. The
reviewer is told that a change violating a delivered invariant draws a finding
naming it, and that editing one is a finding of its own. The limits are stated
where they apply: the delivered set is never presented as the whole set, an
invariant the harness could not read is named as a gap rather than dropped
silently, and what was delivered is recorded on the work item so an operator can
see afterwards which constraints applied.

The architect can be asked what an invariant should say — `yoyo agent chat
architect` — and it cannot write one, because no conversation has tools. So the
lifecycle is reachable from the command line, acting with the architect's
authority and recording that it did:

```sh
./bin/yoyo invariant list
./bin/yoyo invariant create \
  --title "One process at a time acts on an in-flight work item" \
  --statement "Every entry into an in-flight run takes the run's exclusive lease first." \
  --rationale "The lease is the only thing keeping two developers off one item." \
  --established-by yoyodyne-ifd.2.7 \
  --scope internal/runstate,internal/orchestrator \
  --reason "extracted from the decision that added the reservation" \
  one-writer-per-item
./bin/yoyo invariant show one-writer-per-item
./bin/yoyo invariant retire --reason "the reservation moved into the store" one-writer-per-item
```

Retirement is explicit and recorded rather than a deletion: the file stays, the
constraint stops being delivered, and the reason it was lifted is readable by
whoever read the invariant last month. A repository with no invariants directory
simply has none, and runs are unaffected.

## Asking all of this at once, before a tag

Each check above answers for one thing. `yoyo conformance` asks the whole set
together, which is what a release is gated on: the artifacts and their
references, the links the documentation makes to itself, the invariants, and
every admitted work item's attribution to a goal — with staleness surveyed
alongside.

```sh
./bin/yoyo conformance          # what the release cut runs before it tags
./bin/yoyo conformance --json   # machine-readable
./bin/yoyo conformance --notes  # the Markdown section a release's notes carry
```

It writes nothing to the checkout or to the tracker — a gate must not be what
changed what it was about to judge — and exits 1 on a divergence, naming every
mismatch the check that found it collected. One record is written outside both:
the run of the workflow itself, under the harness's own state root, so what a
release was gated on can be read back. Nothing prunes those, and one is written
per invocation.

Staleness is reported and never refuses, for the reason
[stale is not cancelled](#what-a-change-upstream-leaves-stale); work naming no
goal at all is likewise reported, because that is what work admitted before
attributions were checked looks like. What refuses is a claim that is wrong — a
file in an artifact home that is not an artifact, a reference or link reaching
nothing, an item naming a goal no goals document states or having lost the one it
recorded — and having checked nothing at all, which an unreadable tracker or
unreadable goals amount to.

The order the checks run in is not in the code. It is a
[workflow definition](configuration.md#the-release-readiness-workflow) —
project-owned data selecting actions the harness registered in Go — validated and
compiled whole before a single check runs. A project may write its own; nothing
it writes can make the gate perform anything beyond reading this repository and
the tracker.

[Cutting a release](developing-yoyo.md#cutting-a-release) runs this before it
tags, and the passing result is stamped into that tag's notes — through a pull
request of its own, ahead of the tag, since the cut writes nothing to the
default branch — so a published release says what was true of the tree it
names.
