<!--
Landed by yoyodyne-ifd.117.2, tranche 2 of the configuration.md split, with
docs/configuration.md left intact. The links below into ../configuration.md
resolve today and point at sections a later tranche moves; the tranche that
moves a section retargets the link:

  #reading-the-repository-from-a-conversation (two uses)
                                      -> no row in docs/docs-map.md; it stays
                                         in configuration.md until the map
                                         gives it a home

No link here points at a tranche 3 guide (runs.md, publishing.md,
recovery.md).

"The configuration index ... lists the other guides" below is a forward claim:
configuration.md becomes the index in 117.4.

"What Yoyodyne itself carries" names ../configuration.md rather than this
guide: HarnessShippedDocumentation in internal/contextbundle/product.go still
lists docs/configuration.md and none of the guides, and docs/docs-map.md has
the run that lands the last split document grow the list.

Scope against docs/docs-map.md: every section the map's disposition table
assigns this guide — Product specifications (+ What the Lead Product Manager
sees…), Artifact identity and metadata, Approving a document, Who may change
an artifact, Protected paths in a developer's change, and Proposing a change
to a document you do not own. Size: 794 lines against the map's 549-line
budget; the sections themselves grew after the map's counts were taken.
-->
# Configuring artifact homes, identity, and ownership

Where every role reads the product's intent from, what identifies a document, how an
approval is recorded, who may change which document, and what a developer's
change is refused from touching.

[The configuration index](../configuration.md) lists the other guides.

## Product specifications

**Every role reads the specifications directory as authoritative product
intent, and reads all of it.** That is the operator's direction of 2026-09-27 —
"everything in docs/product is authoritative" — and it is what the directory is
for: one configured place the product's intent is written down in, near the top
of the configuration, directly under `version`.

```yaml
product:
  id: example
  repository: .
  specifications: docs/product   # the default; nothing to write down if you use it
```

Every document filed there is carried, not a fixed pair: the brief and the goals,
the non-goals, and anything else the owner files there. The management
conversations — the Lead Product Manager, the architect, the development manager,
and a program manager — are briefed with each of them under the heading
`Authoritative product intent`, and every developer and reviewer is handed them
the same way after the work item it is given, the reviewer reading each as the
change's base commit holds it. The work item is what a run is for and is carried
first; the home is read next and takes at most half of what the run's context
has room for, so a large home still leaves the item's own references room, and a
document that does not fit is named rather than silently missing. A document the
item names that the home already carried is not carried twice. A `README.md`
there is read as the directory index it is, and what it says about ownership —
which role owns what is filed there, and that every other role proposes rather
than edits — is delivered as a rule rather than as description.

Every document there is also [an artifact](#artifact-identity-and-metadata), with
the identity and the approval the goals have: one that is neither the brief nor
the goals is governed by `approvals.goals` rather than by `approvals.designs`,
because it is product intent by where it is filed. And two documents there that
contradict each other are reported by
[`yoyo stale`](goals.md#what-a-change-upstream-leaves-stale), naming both.

A **specification** is one Markdown file that opens with an introduction saying
what the thing is and why it exists, and states the goals that serve it after
that introduction:

```markdown
# Bounded runs

Yoyodyne runs one bounded work item at a time, because a change nobody can
review is not a change anybody can trust.

## Goals

- A run integrates only behind [protected paths](#protected-paths-in-a-developers-change),
  deterministic checks, and an independent review.
- A run that cannot finish leaves its work recoverable rather than lost.
```

The directory is walked to any depth, and every `.md` file inside it is a
specification, with one exception: a `README.md` is a directory index rather than
a document stating anything, so neither the shape above nor
[artifact identity](#artifact-identity-and-metadata) is asked of it, and it is not
counted as a specification. It is still read into the context, under a heading of
its own, because what is filed in a directory is worth knowing to whoever is about
to write the first document into it.
A non-goals document — one whose frontmatter records `kind: non-goals`, or
failing that one named `non-goals` — is held to its own shape rather than to
the specification's: an introduction saying what it bounds and why, then what
the product will not do under a `Non-goals` heading. It states no goals, and it
is not reported for that; one that states no non-goals, opens with them, or
leaves the section empty is reported exactly as a specification is. A rules
document — one whose frontmatter records `kind: rules` — is held to the same
shape with its rules under a `Rules` heading. Everything
else has its prose checked for the introduction-then-goals shape above, and its
identity — the frontmatter naming its id, kind, status, and what it supports — is
checked separately, by [artifact identity](#artifact-identity-and-metadata).
The two are read by different things and reported differently, so a
specification with a malformed id is still read as intent, and one with no goals
still has an id everything downstream can refer to it by.

That shape is checked rather than merely described, because the goals are what
downstream work is kept consistent with and goals with nothing behind them are
not traceable to anything. A specification that does not follow it — no goals, no
introduction before them, or an empty goals section — is **reported and still
read**. `yoyo chat` names it on stderr when the conversation opens — and in the
conversation itself when `/refresh` reads the specifications again — and it is
listed for the Lead Product Manager alongside the specifications themselves. Refusing
to load it would silently lose intent somebody wrote down, which is worse than
loading intent in the wrong shape and saying so.

An empty or missing specifications directory is not an error either. The
conversation says that product intent is not written down, which is a true
statement about the repository rather than a reason to fail. A directory holding
nothing but the indexes `yoyo init` wrote is that same directory: an index states
no intent, so it is not counted as a specification and the conversation still
says intent is not written down.

The context also states, in so many words, what the directory records of the two
documents intent is written in: the **brief** saying what the product is and who
it is for, and the **goals** that serve it. Each is named with roughly how much
prose it carries, and one that carries almost none is called a placeholder — a
count beside the verdict, because how much a short document needs to say is a
judgment. A document's kind comes from the `kind:` in its
[frontmatter](#artifact-identity-and-metadata) when it has one, and from what it
is called when it does not, so a brief written by hand before anything was said
about identity still counts as one. Goals stated in the brief's own `Goals`
section count as the goals when there is no goals document — that is where the
shape above already puts them, and a project that wrote them there has written
them; once a goals document exists, that document is what the goals are read
from and the brief's section is not named beside it. What the Lead Product Manager
does with that signal is the [persona's](agents.md#personas) — the built-in one opens a
project with no brief or goals by asking what the product is for and offering to
draft them, and a project that wants something else replaces that guidance like
any other part of the persona.

### What the Lead Product Manager sees besides them, and what it does not

**The specifications directory, the tracker, and a description of what the
product ships today.** That last part is the documentation your project names,
and the help every command prints. It is carried in a section of its own,
labeled as description of the implementation as built and never as authority
about intent. That is the whole of what is delivered in its context: no source
and no design document arrives there, and nothing runs a command. What it may
additionally do is [read one named path at a recorded commit](../configuration.md#reading-the-repository-from-a-conversation)
— a source file or a design among them — which arrives under the same
description-not-intent label, one path at a time, and only when it asks.

**Which documents those are is `product.shipped_documentation`**, a list of
Markdown files relative to the repository:

```yaml
product:
  id: example
  repository: .
  shipped_documentation:
    - README.md
    - docs/handbook.md
    - docs/operating.md
```

It is a named list rather than a directory for the same reason the set is
narrow at all: a walk would sweep in the design document and the decision
records, which say how the product is built and are the half of `docs/` that
made description reachable as intent in the first place. Each entry is confined
to the repository like every other product path and must be Markdown; a path
naming nothing is simply not carried, and the section says which of them it
found. The list is replaced wholesale rather than merged, as `checks` is.

**There is no default, and that is the point.** A project that names none is
shown none, and the section says so — that what the product ships is not
written down here, rather than that the repository holds no documentation.
Yoyodyne's own documentation layout is eight generic paths (`docs/work.md`,
`docs/reporting.md`, `docs/operations.md`, and five more), and a repository
that happens to hold a file at one of them means something else by it. Handing
those to an adopting project's Lead Product Manager labeled "what the product
ships" is a stranger's prose arriving as description of your product, so the
harness does not do it: **that set applies only to Yoyodyne's own repository**,
which it identifies by the Go module that repository declares, and every other
project is shown what it names and nothing else.

**What Yoyodyne itself carries**, from that set, is `README.md`; the six
operator documents the README split put beside it — [the
conversation](../conversation.md), [how work flows](../work.md), [what comes back to
you](../reporting.md), [artifacts, goals, and invariants](../artifacts.md),
[operations and recovery](../operations.md), and [working on yoyo
itself](../developing-yoyo.md) — and [the configuration reference](../configuration.md). It lives in
`HarnessShippedDocumentation` in `internal/contextbundle/product.go`, and it is
deliberately narrower than the README's [further-reading
index](../../README.md#further-reading): the provider-plugin format, the
coined-term register, the Slack setup, the release notes, the setup skill, and
the design are all reachable from there and none of them is carried here. So
adding a document to that index does not thereby show it to the Lead Product
Manager — the set has to name it, and a test holds the set to documents this
repository actually has, because a path that stops resolving is a surface the
Lead Product Manager silently stops being given.

**The set has a ceiling, and a margin under it that warns.** The eight
documents are carried in full — that is the Lead Product Manager's decision, taken
on yoyodyne-ifd.240 and kept on yoyodyne-ifd.403 — and what they add up to is
measured against `ShippedDocumentationCeiling` in the same file, which is set
well above what they are today and marks the point at which the briefing's
token cost is a product question again; the constant's comment says what that
cost is. Inside `ShippedDocumentationMargin` of it, `make test` prints a
`WARNING:` line after the suite — printed there deliberately, since
`go test ./...` discards what a passing test says — every conversation that
opens says so on stderr, and the briefing itself says so to the role, naming
the room left and the reduction (yoyodyne-ifd.117.4) that buys more; nothing
fails. Only reaching the ceiling fails the test. The set's size is written down on every pass, whatever
its standing: the briefing states it, the conversation's record keeps it with
the picture it was taken for (`yoyo agent list` prints it beside the picture's
commit), and a refresh records it on the event. Over the three weeks to
2026-09-19 the ceiling was a budget the set was always within bytes of, raised
four times, and every documentation edit failed the gate on a sentence
unrelated to it; a reported margin under a distant ceiling is what replaced
that.

The label is the whole of the arrangement, so it is worth reading twice. The
specifications are the only statement of what the product is for; nothing in the
shipped-surface section revises that, however emphatically it is written. Where
the two disagree, the Lead Product Manager **reports the conflict** rather than
resolving it silently or repeating either side as settled product fact. That is
what makes documentation safe to hand to the role that is authoritative about
intent: it arrives as an answer to *what exists*, never to *what is wanted*.

**This reverses half of an earlier trade, openly.** Until 2026-08-18 the Lead Product
Manager saw the specifications and the tracker and nothing else, narrowed on
2026-08-16 after a stale sentence in `README.md` reached the operator as a
statement about the product. What that bought is real and is kept: description
does not arrive labeled as intent, and it never will again while the section
carries its label. What it cost was underestimated. On 2026-08-18 the Lead Product
Manager did not know `bin/yoyo-status` or `yoyo cost` existed until the operator
described them, drafted a work item that mis-assumed which surfaces existed, and
could not evaluate a formatting question about two real outputs it had never
seen — three failures in one day of the operator's routine interface needing the
operator to stand in as its eyes.

What is still given up is also real. Reading all of `docs/` is what let the
Lead Product Manager notice a contradiction between documentation and reality, and
what it reads now is narrower than that: the design document and the decision
records are not there, because they say how the product is built and are the
half of `docs/` that made description reachable as intent in the first place.
Reconciling accumulated documentation against the code belongs to a role that
reads the code, and the harness still does not have one. What it has since
gained is narrower: a management role can [read one named path at a recorded
commit](../configuration.md#reading-the-repository-from-a-conversation), which lets the Lead Product
Manager check a document before it advises about it rather than sweep the tree
for contradictions. Point `specifications` at a wider directory if you would
rather have the breadth than the authority; the confinement rule is the only
limit on where it points.

The documentation is read **after** the specifications have taken what they need
of the context budget, so a repository too large for both keeps the half that is
authoritative and the section names what did not fit. A repository that holds
none of the documentation it named is told so rather than getting a section that
quietly carries less than it says it does — and a project that named none is
told *that*, in different words, because a setting nobody wrote and a document
nobody wrote are two different things to go and do something about.

## Artifact identity and metadata

The canonical documents upstream of a work item — the brief, the goals, the
designs and specifications, and the decision records — each carry a stable
identity in frontmatter, so something downstream can refer to one durably and
the relationship can be checked rather than believed. They live in three
configured homes:

```yaml
product:
  id: example
  repository: .
  specifications: docs/product     # the brief and the goals: the defaults, so
  designs: docs/designs            # nothing to write down if you use them
  decisions: docs/decisions
```

Each home is walked to any depth, and every `.md` file inside one is an
artifact. Two names are not: a `README.md`, which is a directory index rather
than intent anything refers to, and everything inside the invariants directory,
which sits inside the decisions home by default and carries
[its own identity scheme](goals.md#architectural-invariants). A home that does not exist
is not an error — a project that has not written its designs down yet records no
design artifacts.

That exemption is what makes the `README.md` the place these directories explain
themselves in, and `yoyo init` writes one for every home above — the
specifications directory, the `goals` directory under it, the designs, the
decision records, and the invariants. Each states three things and nothing else:
what is filed there, which agent owns it, and whether you may edit one of those
documents by hand. None of that is policy the file invents. A role that is not
the owner proposes an amendment and waits, per the [ownership table](#who-may-change-an-artifact); your
own edit is never refused, and what it is is reported — a revision recorded under
a role that does not own the document is an unauthorized revision every load
names, and what a change leaves stale downstream is
[`yoyo stale`](goals.md#traceability-references-and-orphans)'s to report. An index that is
already there is left alone, `--force` included, because it is the project's own
prose rather than something `init` generated; `yoyo doctor` reports one that is
missing or has stopped stating the three things, and `yoyo setup` offers to write
it — asking separately, and defaulting to no, before it replaces one somebody
wrote.

The metadata is the model the invariants already use, deliberately rather than a
second scheme beside it: **the file name is the id**, a frontmatter id that
disagrees with it is refused, the status is stated rather than inferred, and
every change appends to a revision log.

```markdown
---
id: v1-goals
kind: goals
title: V1 goals
supports:
    - brief
status: active
revisions:
    - action: created
      by: product-manager
      at: 2026-08-17T00:00:00Z
      reason: identity added with the artifact metadata schema
---

# V1 goals

The document itself, unchanged by any of the above.
```

| Field | Meaning |
| --- | --- |
| `id` | The stable identity, and the file's own name: `v1-goals` lives in `v1-goals.md`. Lower-case letters, digits, and hyphens. |
| `kind` | `brief`, `goals`, `non-goals`, `rules`, `design`, `specification`, or `decision`. A `rules` document records the standing rules the operator gave as directives, so every role reads them as product intent. |
| `title` | One line naming what the document is. |
| `supports` | The artifacts upstream of this one, by id: the goal a design serves, the brief a goal serves. Optional — the brief is the root and supports nothing. |
| `status` | `draft` (written, not yet active), `active` (what the product currently intends), `superseded` (replaced by a later artifact), or `retired` (stopped applying, not replaced). |
| `revisions` | Append-only: what changed (`created`, `amended`, `superseded`, `retired`), the role it was recorded under, when, and why. At least the creation is required, and the role must be the one that [owns the kind](#who-may-change-an-artifact). A creation or amendment may also say what it did to the document's intent — `intent: consistent` or `intent: fundamental` — which across the product home decides [whether it needs your approval](#approving-a-document). |
| `approvals` | Append-only, and optional: [your approval of the document](#approving-a-document), each entry naming the revision it was given for. |

Everything below the frontmatter is the document, and nothing about it is
prescribed here: a brief, a goals document, and a decision record have nothing in
common structurally. A specification's own prose contract is the
[introduction-then-goals shape](#product-specifications), which is checked
separately.

Status and revisions have to agree, the same way an invariant's retirement does.
A `superseded` or `retired` artifact must record the revision that ended it, and
one that still applies cannot record one — an artifact whose status says it
was replaced while nothing says when or why records no decision at all. Which
artifact superseded it is not part of the schema yet; the revision's reason says
so in prose.

A file in an artifact home that cannot be read as an artifact is **refused and
named**, which is how an invariant that cannot be read is handled and the
opposite of how a malformed specification is: a document with no usable identity
cannot be referred to, and admitting it under a guessed id would be worse than
saying it is not there. That covers a file with no frontmatter, an unknown or
mistyped field, a status or kind the harness does not know, a missing revision
log, and an id that disagrees with the file name. Two files claiming one id
refuse **both** — choosing between them would hand whatever refers to that id a
document nobody decided on — and each refusal names the other file.

```sh
yoyo artifact list                  # the recorded artifacts; what is not one goes to stderr
yoyo artifact list --kind decision  # one kind
yoyo artifact show v1-goals         # one artifact, its revisions, and your approvals
```

There is no `yoyo artifact create` or `amend`, unlike the invariant commands: an
artifact's content is written by the role that owns it — by hand, or from its
conversation as a typed write, which the harness files under delegated authority
for a qualifying consistent-intent mark or after your approval, with the
frontmatter generated ([writing a document from a
conversation](../artifacts.md#writing-a-document-from-a-conversation)). What the harness owns is refusing a
document whose identity is missing, malformed, or claimed by something else,
[reporting a change recorded by a role that does not own it](#who-may-change-an-artifact),
and [recording your approval](#approving-a-document).

### Approving a document

Approving the brief and the goals is the one thing the design asks of you
routinely, and until it is written down it lives only in the conversation where
you said it — which leaves an approved goal and a draft one indistinguishable to
everything downstream. `yoyo artifact approve` records it in the document:

```sh
yoyo artifact approve brief --reason "approved in conversation on 2026-08-17"
yoyo artifact approve v1-goals --reason "approved with the adoption goal added"
```

```yaml
approvals:
    - revision: 1
      by: operator
      at: 2026-08-18T09:00:00Z
      reason: approved in conversation, with the adoption goal added
```

`at` is when the approval was **recorded**, which is not always when it was given
— an approval given in conversation is written down afterwards — so say when and
how you gave it in `--reason`, which is the half only you can attest to.

**The write lands in your checkout and stops there, and the command says so.**

```
docs/product/brief.md is now an uncommitted change in /home/you/src/example, and
a run against that checkout refuses to start while it is; committing it is
yours, under your own identity
```

**The checkout is named rather than assumed**, because which one the write landed
in is this configuration's answer: `--config` naming another project, a
`YOYODYNE_CONFIG` in your environment, and a linked worktree carrying its own
`.yoyodyne` each point `approve` at a different repository, and the run that
refuses to start is a run over the one that was written to. Ordinarily that is
the checkout you are standing in and the sentence tells you nothing you did not
know; where it is not, it is the difference between committing the file and
looking for it where it never landed.

Nothing commits it and nothing opens a pull request for it. The only ways into
the target branch are a reviewed promotion and your own hand, so a harness-made
commit would be a promotion by another name, carrying tree state nothing
reviewed; and stopping at your tree keeps a document to two readings — committed,
or an edit of yours that is visibly holding the runs up — rather than adding a
third that is on the branch and that nobody touched. The price is the dirty
checkout, which is why it is said as the write happens rather than left to arrive
as `primary repository has uncommitted changes` from the next command you run.
`--json` carries the same sentence as `pending_commit`. A refused approval writes
nothing, so it says nothing.

**The approval is yours, and a process an agent started cannot record one.** A
goal you approved is what lets work be admitted without asking you, so a run
that could approve the goals could admit whatever it liked against them. Every
process the harness launches for a role carries `YOYODYNE_AGENT_ROLE`, and
`approve` refuses a process that carries it before it opens the store —
`yoyo artifact approve is refused from a process the harness launched for the
developer: a person approves an artifact, and an agent's process is not one` —
as [`yoyo pause`, `yoyo resume`, and `yoyo release`](../operations.md#pausing-everything-and-resuming-it)
do. Behind that, the write it would have made is to a protected path, which the
harness refuses in a run's change whatever ran the command — tested end to end
by a developer run whose change carries a forged approval in
`docs/product/goals`, refused with nothing reaching the target branch.

**The approval names the revision it was given for**, which is the index into the
revision log above it. The log is append-only, so that index means one change
forever, and the arithmetic that follows is the point: an approval of the last
revision is a document approved as it stands, and an approval of an earlier one
is a document that has been amended since you saw it. A revision that only gives
a goals document's goals identifiers — the bracketed name an entry opens with —
is not counted: it is recorded as `identified` rather than `amended`, changes no
goal's words, and leaves the document approved as it stands. `yoyo artifact list` and
`show` say which:

```
v1-goals [goals, active] V1 goals
  file: docs/product/goals/v1-goals.md
  supports: brief
  approval: approved and amended since — given by the operator 2026-08-18T09:00:00Z,
            for revision 1, and one revision was recorded after it, so the document
            as it now reads is not what was approved
```

**A rewording in the product home that is consistent with what you approved is not an
amendment you are asked about.** The test is what the goals admit: a change is of
fundamental intent if the goals would afterwards admit work they refused
before, or refuse work they admitted, and that is yours; anything else is a
consistent rewording, delegated to the Lead Product Manager. Which one a change
is, is the Lead Product Manager's judgement, and it is recorded on the amendment
rather than inferred from it:

```yaml
revisions:
    - action: amended
      by: product-manager
      at: 2026-09-26T20:00:00Z
      reason: yoyodyne-ifd.437.11 - the autonomy goal names the Lead Product Manager
      intent: consistent
```

An amendment of any document in the product's specification home recorded by
the Lead Product Manager as `intent: consistent`, with a reason that opens with
the work item that directed it, leaves the document approved: work naming its
goals is admitted exactly as before, `yoyo artifact show` says the approval stands through that many
rewordings, and [`yoyo stale`](../artifacts.md#what-a-change-upstream-leaves-stale)
lists it as a rewording rather than an amendment. Every other amendment is still
yours — one recorded as `intent: fundamental`, one that says nothing, and one
labelled consistent whose record is short of the rest: recorded by another role,
against a document the Lead Product Manager does not own, or with no item named.
The last of those is said on `show` with what it is missing, so the label does
not read as ignored. What is checked is the shape of the identifier the reason opens with and
not that the tracker holds it; the reason is the record you follow to the
decision.

A `created` revision may carry the same mark when it records existing intent,
such as rules the operator already gave. With `intent: consistent`, recorded by
the Lead Product Manager and a reason opening with the directing work item,
the document starts approved under that reason. `artifact list` and `show` name
that record instead of asking for your approval; `--json` carries it as
`delegated_creation`, with no operator `approval` invented. A creation marked
fundamental or left unmarked still needs the configured approval, and a later
fundamental or unmarked amendment counts against a delegated creation's approval.

**Your `approvals` configuration decides what is asked of you.** `approvals.brief`
and `approvals.goals` are `human` by default and `approvals.designs` is
`automatic`, deliberately rather than by inheritance: the brief and the goals are
what you state and what everything else traces back to, while a design serving an
approved goal is the architect's judgement about how, and approving each one is
the per-change gate autonomy is the absence of. Approving the goals is the one
approval that then carries weight elsewhere, because it is what work is admitted
against. `approvals.goals` covers the
non-goals with the goals, because a bound on intent nobody approved is as much
unapproved intent as a goal is; it covers the operating rules for the same
reason, and it covers every other document filed in the specifications
directory too, whatever its kind, because everything there is
authoritative product intent. A decision record is the architect's account of
how something was decided rather than a statement of what the product should do,
and no setting asks you to approve one.

**Recording an approval gates one thing: what reaches the work queue.** An
unapproved document still loads, still governs what is downstream of it, and
stops nothing that reads it. What your approval of the goals decides is whether
work serving them is admitted without asking you — see
[what reaches the queue](goals.md#what-reaches-the-queue). Everywhere else, an
amendment after approval changes what is reported about the document rather than
what is allowed, and what `human` buys you is that the difference is visible — in
the document, in the listings, and in `--json`, where each artifact's `state` is
`approved`, `amended`, or `unapproved`.

### Who may change an artifact

Ownership is an authorization boundary rather than a prompt convention, so it is
in code the way the invariants' is, rather than in a persona a configuration can
weaken.

| Kind | Owner | Every other role |
| --- | --- | --- |
| `brief`, `goals`, `non-goals`, `rules` | Lead Product Manager | Asks questions and [proposes amendments](#proposing-a-change-to-a-document-you-do-not-own) |
| `design`, `specification`, `decision` | Architect | Identifies risks, asks questions, and [proposes amendments](#proposing-a-change-to-a-document-you-do-not-own) |

The development manager appears in neither row, because it owns no repository
document: its decomposition is Beads work rather than Markdown. Nothing here
constrains **you**. The boundary is between agent roles, and the operator directs
any of them.

It holds in two places, and both are live.

**Writing.** The package that writes an artifact refuses a role that does not own
the kind, on creating, amending, superseding, and retiring one, and records the
role that did in the revision log. That is the path a document written from a
conversation takes: the owning role emits a typed action, you approve it, and the
harness performs the write through this boundary under that role's authority —
see [writing a document from a
conversation](../artifacts.md#writing-a-document-from-a-conversation). A role that
names a kind it does not own, or a home its kind is not filed in, is refused
before you are asked about it. There is still no `yoyo artifact create`: a
command would need the document's prose typed at a shell, which is the
transcription the typed action exists to end.

**Reading.** A document whose revision log records a change by a role that does
not own it is **reported every time the artifacts are loaded**, as an
`unauthorized-revision` beside the [broken relationships](goals.md#traceability-references-and-orphans),
naming the file and which entries crossed. It is the half that catches a
hand-edited log, wherever it came from.

It reports rather than refuses, deliberately. The revision log is append-only, so
a past entry cannot be made lawful without rewriting history, which is the one
thing the log exists to prevent. Refusing would drop the document out of the set,
report everything that referred to it as naming something nobody wrote, and leave
a file that could neither load nor be corrected. So the document keeps loading,
keeps governing, and stays amendable by its owner, and the entry stays reported
until somebody decides what to do about it.

**A third place, and the one that catches an editor.** Both halves above are
about the document — who wrote it, and what its log says. Neither notices a
developer that simply opens the file. That is what the protected-path gate below
is for, and it is why an agent with an editor in its worktree is no longer the
open case it was: the edit is refused before anybody reviews it, whatever the
revision log does or does not say about it.

### Protected paths in a developer's change

The documents above are upstream of every change a developer makes. A developer
that edits one is redefining what its own work is measured against, and reading
the diff does not tell that from a legitimate edit — both are a file that
changed. So these paths are **default-deny for a developer's diff**:

| Protected | Setting it follows |
| --- | --- |
| `.yoyodyne/` | fixed; the configuration directory |
| `docs/product/` | `product.specifications` |
| `docs/designs/` | `product.designs` |
| `docs/decisions/` | `product.decisions` |
| `docs/decisions/invariants/` | `product.invariants` |

The set follows your configuration rather than the default layout: a project
that keeps its designs elsewhere has not thereby made them a developer's to
rewrite.

**The tracker's export is refused the same way.** A worktree is given your
checkout's own `.beads/issues.jsonl` and that copy is
[held out of the run's change](../work.md) with Git's skip-worktree bit — which
lives in the worktree's index under `.git`, a directory the developer's sandbox
grants writes to. One `git update-index --no-skip-worktree` inside the run would
turn the refreshed export into a modification the harness commits, promotes, and
conflicts every other run against, so the export joins this gate: a diff
containing it is refused, and the refusal names what the file is and how it got
there rather than only the path. The paths refused are the exports the harness
refreshes, so the two never drift apart.

**How it behaves.** The gate runs in front of the deterministic checks, on every
attempt, over every path the change touches — tracked, untracked, and both sides
of a move. A change that touches one of these paths without a grant is refused
and handed back to the same developer inside the same repair loop a failing check
uses, spending from the same budget, and the refusal names how a grant is made.
Where the product [reports to Slack](../slack/setup.md), each refusal handed back
is also said in the item's thread, in the developer's voice, naming the refused
paths, what the item grants, and the grant line that would admit them — so a
repair round spent on it is a round with a stated reason rather than one only
the item's notes explain. A refusal that finds the budget already spent buys no
round, and the blocker line that ends the run is what names its paths.
No reviewer is asked about it: the class of finding this replaces used to cost an
Opus review cycle to reach, and it costs a string comparison here. A run whose
repair budget is spent still refusing is blocked on the work item, with the
refused paths and the item's grants both named, because which of the two is wrong
is a person's decision.

**Granting a path.** An exception is declared in the work item's text, on a line
beginning with the marker:

```text
Protected-path grant: docs/designs/v1-harness-design.md
```

Several paths on one line are separated by commas or spaces, and several such
lines are read together. A grant naming a file admits that file alone; a grant
naming a directory admits what is inside it. A grant of the repository root is
not a grant, and prose that merely discusses these paths grants nothing — the
marker has to begin the line, which is why it is an unlovely token rather than a
phrase an item could produce by accident.

**Which fields count, and why it is not "whoever wrote it".** A grant is read
from the item's **title, description, design guidance, and acceptance criteria**,
and **not from its notes**. The gate does not ask who typed a grant — it cannot,
because the tracker records no authorship the harness could check. What it relies
on instead is *when*: those four fields exist before the run starts and no part of
the harness writes to them, so a grant in one of them predates the change it
admits. The notes are the opposite — the harness appends each run's own record
there, including the reviewer's summary and findings — so a grant read from the
notes could be an agent's own prose, admitted to the next run of the same item.
That is the case this gate exists to stop, so the notes do not count.

The practical consequence: **a grant written into the notes silently does not
count.** A run refused despite an item that plainly names the path is usually
this. Both the refusal and the blocker name the fields a grant is read from.

**The paths a grant does not reach.** A grant is an exception to *this harness's*
default-deny, and to nothing else's — and one directory of this harness's own
is beyond it too, below. Claude Code refuses an agent's writes to its own
settings files above anything yoyodyne permits — the editing tools are denied
there however the run is configured, and the shell sandbox names the file and
cannot be disabled by policy — so an item that grants one of them has admitted
work no run can do, and the run discovers that by spending its repair budget
against it. One item spent three rounds there before this was recorded anywhere.

| Beyond any grant | Refused by |
| --- | --- |
| `.claude/settings.json` | Claude Code |
| `.claude/settings.local.json` | Claude Code |
| `.yoyodyne/roles/` | Yoyodyne, absolutely |

So the harness refuses a creation, an update, or a proposal whose text grants one
of these, and the refusal names who refuses it: what is wrong is not the item's
judgement about the path, but that the change is a person's to make by hand
rather than a run's to be given. Only the marker is refused — prose that names
one of these files grants nothing and admits fine, which is what lets an item
*about* this boundary exist at all.

**The role definitions are refused by this harness itself, and for a reason of
its own.** A role definition says what a role may do, so a run that could write
one could widen its own authority — the one thing
`configuration-never-grants-authority` forbids. The rest of `.yoyodyne/` is
default-deny with the item as the way out; this directory has no way out at
all, decided change or not
([configurable workflows](../designs/configurable-workflows.md#the-authority-model)).
A grant naming `.yoyodyne/roles` or anything inside it is refused at the same
three doors and by the run before it claims the item, and a change touching the
directory is refused by the diff gate **whatever the item grants** — a grant of
`.yoyodyne`, which is still how an item admits the rest of the configuration,
does not reach inside it. The comparison folds case, because on a
case-insensitive filesystem `.yoyodyne/Roles/` is the same directory. A person
changes a role definition by hand, and the operator's activation is what makes
it effective.

**And once more, where all four fields are read.** Those three doors carry an
item's title and description; a grant is honoured from its design guidance and
acceptance criteria too, and nothing in the harness writes either of those — they
reach an item through the tracker's own command. So the run asks the same
question itself, over all four fields, before the item is claimed: a run that
would start on such an item refuses to start, which is the same refusal one step
later and still before a single repair round is spent. That is also what covers
an item admitted before this gate existed. The developer contract names the same
paths for the case no gate can catch — an item that describes the work without
granting anything, whose developer would otherwise spend attempts looking for a
way in.

The provider rows are short and evidenced rather than a guess at everything a
provider's sandbox refuses: an entry refuses work at admission, so a path added on
suspicion costs items nobody needed to refuse. They grow the same way they started —
something meets the wall and reports it. The role-definitions row is not one of
them: it is this harness's own refusal, decided in the design rather than met by a
run, and it is absolute by that decision.

**A done-condition is never written against one of these homes.** The gate
above refuses a diff; what it cannot refuse is an item whose *done-condition*
lives in a document the run may not write — "the design's query list marks the
query as existing", "reconcile the design document's `yoyo status` entry", "her
ruling is recorded on the design". Such a condition is one no diff can meet, and
a run handed it lands what it can and parks on the rest. Three items did that in
one week (`yoyodyne-ifd.141.1`, `.63`, and `.68.25` before them), each caught by
the reviewer after the run had spent itself, each fixed afterwards by the
architect amending the document through the governed path. So the check is made
where the item is written: a creation, an update that rewrites the description,
or a proposal whose "Done means" clauses or acceptance criteria name a path
under the product, designs, decisions, or invariants homes — or a document one
of those homes owns, by its name — is refused unless the item grants that path,
with the clause quoted and both fixes named: take the clause out and say the
document's owner amends it through the governed path once the run's summary
names what there is to record, or, where the change behind it is already
decided, carry the grant. Only the done-conditions are read — the whole of the
acceptance criteria, and in the description the sentences from a "Done means"
(or "Done:", "done when") to the end of their paragraph — because an item cites
these documents in nearly every description, as the design it builds against or
the ruling it obeys, and a citation is not a condition. A document is named by
its path or, where its id is two words or more, by its id however the prose
joins the words ("the slack-reporting design" names
`docs/designs/slack-reporting-design.md`); a one-word id such as `brief` is left
to the path, and an invariant is named by its path only. An id that is also the
name of one of the harness's roles is read as the document only where the clause
names its path or names it as a document — the word `design` or `document`
beside the id, or its file name — and the bare phrase is the role: "no program
manager is configured" is about the role, while "the program manager design"
names `docs/designs/program-manager.md`. A done-condition saying a design is
recorded, published, or ratified is still refused on an item naming no
executor, whichever document it names. The run asks the same
question of the item it is handed, over the acceptance criteria as well, before
it claims the item, and refuses to start rather than parking on the condition
afterwards — which is what covers an item whose criteria were written with the
tracker's own command, and an item admitted before this existed. Neither check
reaches `.claude/settings.json`, `.claude/settings.local.json`, or
`.yoyodyne/roles/`, which stay beyond any grant as above. [How work flows](../work.md#what-an-item-may-ask-of-a-run)
states the rule from the item's side.

**What a grant does not do.** It admits the path; it does not decide what is
written into it. The legitimate use of the exception is recording a change
somebody already decided — an approved amendment, an operator's decision — never
delegating the deciding, so a grant should name that decision, and the reviewer
is instructed to look for it: a granted path whose item names no decided change
behind the grant is a finding at major severity or higher. The gate is a string
comparison and cannot ask this question; the reviewer can, which is why the two
halves sit where they do. A branch review is not asked it at all, because it
reads commits rather than the items their grants live in.

Nothing any agent produces during a run grants a path. A developer that
genuinely needs one says so in its summary and
[proposes the change](#proposing-a-change-to-a-document-you-do-not-own); the
grant goes into the item, which is not a developer's to write. Which role
maintains an item's text is a question about the fixed set of roles rather than
about this gate, and this document does not answer it.

### Proposing a change to a document you do not own

A role that may not edit a document and has no way to say it is wrong has two
moves left, and both are bad: build against intent it believes is wrong, or edit
the document anyway. So there is a third: one block, in the contract, carried by
every role that has it. Today that is the developer, which is the role that meets
the boundary while implementing against a document — a developer that finds the
design contradicts the goal it serves ends its reply with it:

````text
```yoyodyne-amendment
{"proposals":[{"artifact":"v1-design","change":"say which of the two orderings holds","why":"the work item cannot be implemented against both"}]}
```
````

The harness resolves the document to its kind and its kind to its owner, so who
is being asked follows from the document rather than from anything the agent
claims. A proposal naming a document nobody records is refused, because there is
no owner to decide a change to a document that does not exist, and a proposal
from the role that already owns the document is refused too: that role amends
it. **The refusal reaches you, and it reaches the agent that wrote it on that
agent's next invocation in the same run, if there is one.** It is named on the
run's outcome beside the proposals that were kept, and it is carried on the run's
own state, tagged with the role that proposed it, so that role's next invocation
on the run — a repair attempt, a continuation triage granted, the re-ask an
interim reply earns — opens with it in the harness's own words: nothing was
recorded, nobody was asked, do not describe the proposal as raised, take the
claim back out of anything already written, and propose it again if it is still
worth proposing. That role's next reply spends it, so an agent is told once, and
a refusal is only ever shown to the role that earned it. Since the developer is
the only role carrying the block today, the developer is the only role that earns
one.

The condition is the whole of it, and it is worth reading plainly: **a
developer invoked once, whose only reply carried the refused block, is never
told.** The harness cannot read a proposal before the reply that carries it, and
a run whose one attempt succeeds asks that developer nothing afterwards, so the
refusal is on the run for you alone. What covers that case is the contract rather
than the carry-back — every developer is told in advance that writing the block
is not the proposal being recorded, that the harness can only answer afterwards,
and that nothing it writes may therefore assert a proposal has been raised. The
carry-back is what repairs a claim the contract did not stop; the contract is
what stops it where nothing can carry anything back. The artifact ids are what
`yoyo artifact list` prints.

**Nothing an unapproved proposal contains reaches the document, and neither does
anything an approved one contains.** A proposal carries what should become true
and why, never replacement prose — the size bound on it is what keeps it an
argument rather than an edit waiting to be pasted. Approving records that the
owner's authority came down in favour of the change; the change itself is then
made by the owner, in the document, in a revision recorded under that role.

Like a report, a proposal costs its run nothing: the run integrates exactly as it
would have, and a proposal the harness cannot read or cannot keep is named on the
outcome rather than failing the attempt it arrived with. It is durable in the
same place and for the same reason — the run that argued the design was wrong is
finished and cleaned up long before anybody decides what to do about it.

**What could not be kept is durable too**, on the run's record rather than only
on the outcome `yoyo run` prints. Every proposal the harness could not read or
record, and every report it could not read or collect, is written onto the run's
state in the harness's own words — `amendment_problem` and `report_problem`, the
same two fields the outcome carries — and `yoyo status <beads-id>` prints each
under the run as `proposal not kept:` and `report not kept:`. They were for a
long time on the outcome alone, which is printed once by the process that made
it and gone with it, so a proposal that was refused, or that was made on a run
whose process died before it reported, read afterwards exactly as one never
made: one run's three lost proposals went unnoticed for four runs on that
account, and could no longer be diagnosed when they were. Nothing spends these
— the carried refusal above is emptied by the developer's next reply, and the
record stays for whoever audits later.

```sh
yoyo amendment list                       # what is waiting to be decided
yoyo amendment list --owner architect     # one owner's queue
yoyo amendment show <id>                  # one proposal and what became of it
yoyo amendment approve <id> --reason ...  # record the change as authorized
yoyo amendment decline <id> --reason ...  # turn it down, keeping why
```

**Every decision is yours, whoever owns the document.** An owning role that runs
is shown what has been proposed against its documents and argues for or against
it — proposals against the brief and the goals are carried into the Lead Product
Manager's conversation, and proposals against the designs, the specifications,
and the decision records are carried into the architect's, each told in so many
words that it cannot decide one. An owner may write its own documents, which is
how an approved change is made: it writes the revision as a typed action. The
harness performs a qualifying consistent revision under the Lead Product
Manager's delegated authority or awaits your approval — see [writing a document from a
conversation](../artifacts.md#writing-a-document-from-a-conversation). What no
owner can do is decide the proposal from there. Both owners can now be
asked directly: `yoyo agent chat architect` is where the argument about a design
happens. But no agent records a decision, `yoyo amendment` is the only thing that
does, and the record says you exercised the owner's authority rather than that
the owner answered — the same override path `yoyo invariant` documents. A decline
keeps the reason it was turned down with, because a proposal refused silently is
one the same argument arrives to make again.

An owning role recording its own decision is vocabulary the record already has
and nothing produces: what would make it real is a decision the harness carries
out for a role from its own reply, the way it carries out the Lead Product Manager's
tracker actions. Until something does that, read "under the architect's
authority" on a decision as your judgement standing in for the role, taken after
hearing it rather than instead of hearing it.

The reviewer is deliberately not given this block. What it finds wrong with a
change is a finding, which decides whether the change is repaired; a reviewer
that could also propose amendments would have two ways to say one thing. The
Lead Product Manager raises what it cannot place under a goal as a concern, which
stops and asks you, for the same reason.

A developer that could not be talked out of its argument makes it again on every
repair attempt, and the second and later copies within one run are dropped: one
disagreement is one proposal, rather than one per attempt for whoever decides to
answer several times over. Two proposals count as the same argument when they
ask for the same change to the same document; restating the reasoning does not
make a new one, and **neither does rewording what is asked for**. A developer
handed a repair writes its block again rather than copying the one before it, so
one argument arrives spelled two ways, and a comparison that read the wording
literally put both of them in front of whoever decides.

What is compared is the share of content words the two changes have in common,
with the function words dropped — those are what any two English sentences share
whatever they say, so leaving them in narrows the very gap this is reading.
Different documents are never one argument however alike the two read, because
they are decided by different owners. The boundary is measured rather than
picked: on the five proposals one run made about one design, the two pairs the
architect decided as one argument each score 0.47 and 0.97, and the closest pair
the architect decided as two — the same fact asked for in two different sections
— scores 0.28. It sits at 0.4, nearer the duplicates than the midpoint, because
the two errors do not cost the same: a restatement that gets through costs its
owner a second copy of an argument they are already reading, and two arguments
folded into one cost the second of them its decision, silently. Those five
proposals are quoted in the test beside the comparison, so moving the boundary
fails there rather than in an owner's queue.

What it reaches is one run, in whichever process continues it. Every proposal
the run's agents make is written onto the run's own state as `amendments`: each
one raised, with the id it was recorded under, and each one dropped as a
restatement, with the id of the raised proposal it was folded into and the
likeness it was folded on. That record is the memory the next proposal is
compared against, so a run continued in a second process — by a usage-limit
pause that exited on its in-process bound, or by a repair triage re-entering it —
reads it back and folds a restatement made there exactly as the first process
would have. It is also the only record a drop has: a dropped proposal reaches
neither the amendment log nor `amendment_problem`, so a pair the boundary folded
wrongly would otherwise lose its second argument with nothing anybody could
find. `yoyo status <beads-id>` prints each drop under the run as
`restatement dropped:`, naming the document, the proposal it was folded into,
the likeness, and the change as it was written, and `--json` carries the whole
list. The record holds 32 proposals; past that a restatement is raised rather
than dropped, because a drop the record cannot hold is exactly the lost argument
it exists to prevent. Nothing compares a proposal against one an earlier run
raised.

**This is a second proposal path rather than a reuse of the one the conversation
already has**, and that is worth knowing because it was not the first choice. The
Lead Product Manager's work-item proposals live in the conversation that raised them,
in memory, decided inside a turn. A proposed amendment has to survive the run
that raised it, is addressed to an owning role rather than to you alone, and is
decided from the command line days later — so what carries over is the shape
(propose, never defer an edit, decide explicitly, record the decision) rather
than the code. The cost is two vocabularies for one idea: a proposal in the
conversation is a work item, and a proposal in `yoyo amendment` is a change to a
document. Consolidating them is not done.
