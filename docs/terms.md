# Terms

Every word this project coined that a reader can still meet, with what it means in ordinary words and where it is met. This is the one list; a coined term that is not here is one nothing defines.

The rule it serves is the legibility goal's, in
[the v1 goals](product/goals/v1-goals.md): *user-facing language chooses the
ordinary, literal word over metaphor, coinage, or term of art* — unless the term
is defined here. Registration is the whole of the exception, and it is a low bar
on purpose. A word that names a real mechanism an operator meets in command
output is worth keeping and cheap to define. A word that only decorates a
sentence is worth replacing, and the ones that were replaced are listed below
rather than registered. What is not acceptable is the third case: a coinage in
front of a reader with no definition anywhere, so somebody meeting it has
nowhere to go.

The inventory this was seeded from is
[the yoyodyne-ifd.206 sweep](diagnoses/yoyodyne-ifd-206-coined-terms-sweep.md),
which measured every term below across the tracker, the governed documents, the
Go source, and command output. Its governed-document figures were a floor rather
than a count: the scan looked for one spelling of each term, so `minute-zero`
went past it, and two documents were written after it ran. The governed homes
were measured again on 2026-08-31 under yoyodyne-ifd.220, tolerant of how a
term's parts are spaced, and
[what that re-run found](diagnoses/yoyodyne-ifd-206-coined-terms-sweep.md#the-yoyodyne-ifd220-re-run)
is what this document is now written against: **25 occurrences of 6 terms** in
the prose the check reads, every one of them a term with a row below.

That sweep measured the terms it already knew. The whole of the harness's own
vocabulary — every term of art in the printed strings, the role contracts and
personas, the guides, and the governed documents, with where each appears, how
often, what it means, and whether it is proposed for replacing or for a row
here — is [the vocabulary inventory](vocabulary-inventory.md), written by
`go run ./scripts/vocabulary` so it can be measured again. A term it proposes
to register is not registered until its row is written here, and one it
proposes to replace is not replaced until it is listed below. Until then the
check below allows it by name, as a term whose decision is still to be made,
and reads the inventory's list of terms for that from
`internal/terms/inventory` — the same list the document is written from —
rather than from the document, so a document nobody has regenerated cannot
make the check wrong.

## The register


| Term          | In plain words                                                                                                                             | Where it is used                                                                                                                                                                                                                         |
| ------------- | ------------------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `brake`       | the automatic stop after a set number of blocked runs in a row                                                                             | the scheduler's own messages about why it stopped choosing work; `internal/orchestrator`; the guides under `docs/`                                                                                                                       |
| `direct-work` | one of the two authorities a person can hold in a project's configuration: authority over work already running. Its pair, `own-intent`, is authority over what the product is for | the authority a person is given in [the agents configuration](configuration/agents.md) and the Slack identities in [Slack setup](slack/setup.md); `internal/config` and `internal/slack` |
| `discharge`   | to be the work an item asked for, so the item closes on it — as against landing evidence, which does not                                   | the developer's contract and the reviewer's; a work item's own notes after a run; `internal/landing` and `internal/orchestrator`; [how work flows](work.md)                                                                              |
| `docket`      | the list of stopped runs, of runs that died before they started, and of items dispatch would not start, waiting on the development manager | `yoyo reconcile` and `yoyo triage` output, and the Lead Product Manager's context bundle; `internal/runstate`; [management and supervision](designs/management-and-supervision.md)                                                            |
| `exchange` | a question one role puts to another, which the harness delivers by invoking the other role and records with what it cost | `yoyo exchange` and its output; [the conversation](conversation.md); `internal/exchange` |
| `handback`    | handing the work back to the developer that made it                                                                                        | `internal/orchestrator` and `internal/runstate` only — it names no command output and no document                                                                                                                                        |
| `heartbeat`   | how often to repeat                                                                                                                        | the `yoyo slack --heartbeat` flag, whose own help says it in plain words; [reporting into Slack](slack/setup.md)                                                                                                                         |
| `intake hold` | a stop on the harness choosing new work by itself, placed by the operator or by the automatic stop after blocked runs; `yoyo release` lifts it, and work already running carries on | `yoyo status`, `yoyo release` and its help, the Slack posts, and the [selected-work-passes-intake-and-records-why](decisions/invariants/selected-work-passes-intake-and-records-why.md) invariant; [operations](operations.md); `internal/runstate` and `internal/orchestrator` |
| `Lead Product Manager` | the role that owns what the product is for: the brief, the goals, and what is admitted to the backlog and in what order. Each program manager brings it everything outside its own work stream, which is what *Lead* says. *Lead PM* is the short form, for a label with no room for the whole name; *PM* on its own is not written, because it could mean either manager. It is the name a person reads, and only that: the identifier stays `product-manager` in configuration, agent names, persona file paths, command arguments, and every record already written | `yoyo chat`, which is the conversation with it, and every surface that names the role — `yoyo status`, the dashboard, the Slack posts, the other roles' contracts, and the personas; the guides under `docs/`; `internal/domain`, where `AgentRole.Title` derives the name from the identifier in one place |
| `minute zero` | before development begins                                                                                                                  | the [developer-verifies-before-submitting](decisions/invariants/developer-verifies-before-submitting.md) invariant, whose wording only the architect changes — written there both spaced and as `minute-zero`, which this one row covers |
| `park` | to keep an item in the Lead Product Manager's order but stop the harness selecting it until somebody releases it with `unpark`; the reason is shown wherever the item is listed | the Lead Product Manager's `park` and `unpark` actions in `yoyo chat`, the backlog listings, and a work item's notes; [how work flows](work.md); `internal/chat` |
| `program manager` | a role instance that owns one named work stream: it admits work only under that stream's own tracker label, and asks the Lead Product Manager for anything outside it. There is one program manager role type, with what it may do fixed in code, and each configured instance owns one stream and one label. Written in full wherever a person reads it; `pgm` is the identifier form only — in configuration keys, code, and instance identifiers — because *PM* alone could mean either manager; the Lead Product Manager's short form is *Lead PM* | [the program manager design](designs/program-manager.md); `role: program-manager` on an agent block in [the configuration](configuration.md), which declares each instance; the role-capability registry and [the authority inventory](authority-inventory.md); and the surfaces that name role instances — each named here as it comes to exist |
| `protected path` | a path a developer's change may not touch unless its work item carries a `protected-path grant:` line naming it | the refusal a developer run receives and the grant line a work item carries; [configuration](configuration.md); `internal/orchestrator` |
| `re-arm`      | repeat the merge request a forge dropped, once per publication — the `yoyo triage rearm` verb and the budget it spends                     | `yoyo triage rearm` and its help; the merge re-arms count in `yoyo status`; the development manager's triage decisions and `yoyo ground`; the guides that say when to type it — [operations](operations.md), [recovery](configuration/recovery.md), [the conversation](conversation.md), and [configuration](configuration.md); `internal/orchestrator` and `internal/runstate` |
| `repair` | a developer's further attempt at its own change, in the same worktree, after a failed check or review findings; `yoyo triage repair` asks for one | `yoyo triage repair` and its help, the repair budget `yoyo status` counts, and a work item's notes; [recovery](configuration/recovery.md); `internal/orchestrator` |
| `seat`        | an instance of a specific persona type — a developer seat, the Lead Product Manager seat — often with persistent memory but not always. A *developer slot* is the harness's word for the capacity one developer seat fills: the seat is what does the work, and the slot is what it takes up while it does | the operator's own conversations, which is where the word came from; [a developer slot that prefers a label](configuration.md#a-developer-slot-that-prefers-a-label), the yoyodyne-ifd.388 mechanism, and the reliability seat yoyodyne-ifd.415 configured under it |
| `shadow review` | a second review of the same change by another model, recorded for comparison and deciding nothing | `yoyo review --shadow`; [how work flows](work.md); `internal/shadow` and `internal/cli` |
| `side thread` | a secondary conversation a role opens to work something out: it judges and drafts but acts on nothing, and its conclusion is merged back into the main conversation. *Side stream* names the same thing and is not written for a person; the `sidestream` package keeps its name | the role contracts in `internal/sidestream`, `yoyo chat` output, and [the conversation](conversation.md) |
| `sink`        | the process that posts to Slack                                                                                                            | `yoyo slack` and `yoyo doctor` output; `internal/slack`; [the Slack reporting design](designs/slack-reporting-design.md)                                                                                                                 |
| `steer`       | direct the work, or change what is being worked on                                                                                         | `yoyo chat` help and the Slack thread replies; `internal/chat`; [the Slack reporting design](designs/slack-reporting-design.md)                                                                                                          |
| `sweep` | one run of a recurring task and the record it leaves; `yoyo sweeps` reads them | `yoyo sweeps` and its output, and the heading a recurring task's findings are listed under; [operations](operations.md); `internal/runstate` and `internal/orchestrator` |
| `triage` | the development manager deciding what happens to each stopped run, and `yoyo triage` carrying the decision out | `yoyo triage` and its verbs; [recovery](configuration/recovery.md); `internal/triage` and `internal/orchestrator` |
| `witness` | the tracker's recorded copy of an item's `Goal served:` line, kept so a destroyed attribution can be found and restored; `yoyo goals witness` records it | `yoyo goals witness` and its help; [goals configuration](configuration/goals.md) and [artifacts](artifacts.md); `internal/cli` |


One entry is here because the word is still written somewhere no other role
may edit. `minute zero` is the sweep's decoration rather than a mechanism name,
and it survives only inside the text of an active invariant. That wording is
the architect's alone — the sweep says so outright — so the entry is what keeps
the word readable until the architect decides otherwise, and it is retired
when the architect does. `in force` and `posture` were two more of these until
the operator objected to each by name: yoyodyne-ifd.418 retired `in force`, and
yoyodyne-ifd.437.6 retired `posture` on 2026-09-25 because it was unclear to
him. Both are now listed below as replaced. The architect has since amended
every governed document that carried either, so neither row names a document
any more, and both are refused everywhere the check reads.

`environmental stop`, `idle bound`, and `stall continuation` were never
registered. They are the harness's own names for a run ended by something
outside the work, for the harness ending a run whose AI session produced no
output for a set time, and for the development manager resuming such a run in
the same session. All three reached the operator in one program manager's
report on 2026-09-27 with nothing saying what any of them meant, so the item
retiring these three terms (yoyodyne-ifd.437.17) replaced them with the plain account of what happened —
*the AI session running the developer produced no output for five minutes, so
the harness ended the run; the cause was outside the work, so no repair attempt
was spent and the change was kept* — and listed them below. Identifiers in the
code and field names in the records keep their names; only what a person reads
changed. The personas the running roles read, under `.yoyodyne/personas`, say it
plainly: the development manager's says a failure
*came from outside the work*, and the only place any of them writes the three
words is the example each gives of what not to write.

One entry is a command's own name. The sweep replaced `re-arm` in the prose of
the governed documents, but `yoyo triage rearm` is a verb an operator types and
`yoyo status` counts, and a word a command is called cannot be swept out of the
command's help without renaming the command. So it is registered, and a row
permits its term everywhere the check reads — the governed documents included,
in every spelling — not only in the command's output. The guides that tell an
operator when to type the verb use the word too, so the check reads them for
this term as well: take the row out and every guide sentence saying `re-arm`
or `rearm` fails with the command. What keeps it out of a
sentence that could have said *repeat the merge request* is the reviewer, not
the check.

One entry is the operator's word rather than the project's. He introduced
`seat` on 2026-09-19 and wants to keep using it, so its row is what makes it
read the way he means it wherever it is met. The distinction the row draws is
instance against capacity: a seat is the running persona that does the work,
and a developer slot is one unit of `max_concurrent_developers`, the capacity
that seat fills. So the configuration guide, the status line, and the scheduler
say *slot* when they count, fill, free, or configure capacity, and *seat* when
they mean the developer that sits in one — the reliability seat is the developer
that works in the slot configured to prefer the `reliability` label.

`program manager` is the operator's too. He decided the role on 2026-09-24,
and its row is here before any document or surface names it, so the word is
defined from its first use. It is always written in full for a person, never
shortened to *PM*, which could as well mean the Lead Product Manager; `pgm` is the form
an identifier takes and nothing else. Its row names no document yet because
none exists: each place of use is added to the row as it comes to exist,
starting with the architect's design document.

`Lead Product Manager` is the operator's as well. On 2026-09-26 he renamed the
product manager so that it could not be confused with the program managers,
who answer to it: *PM* had come to mean either role. Its row is the name a person
reads; the identifier underneath it is unchanged, so configurations, agent
names, persona paths, and the records already written still say
`product-manager` and still load. The governed documents that name the role
the old way are their owners' to amend.

Eleven rows are the Lead Product Manager's decisions of 2026-10-08 on
[the vocabulary inventory](vocabulary-inventory.md), which accepted every
proposal in it unchanged: `direct-work`, `exchange`, `intake hold`, `park`,
`protected path`, `repair`, `shadow review`, `side thread`, `sweep`, `triage`,
and `witness`. Each names a command, a flag, a configuration value, or an action
a role records, so it is defined here rather than swept out. The rows were
written by the item replacing the coined vocabulary in printed strings, role
contracts, shipped personas, and pass prompts (yoyodyne-ifd.437.19).

## Replaced rather than registered

These were decoration: each named nothing a reader can point at, and each had an
ordinary word that said the same thing. The second column is the wording to
write instead, rather than a claim that every occurrence has been changed: where
one of these was written in the prose of a governed document it was replaced,
and the rest are still in places outside this sweep — mostly the tracker's own
items, which are the Lead Product Manager's to reword. The check below refuses any
of them coming back into the prose of a governed document, or into the strings
the commands, the conversation, and the notifier print, without an entry.

The third column is the one exception, and it is a narrow one. A governed
document is its owner's alone to reword, so a term retired from everywhere else
can still be written in one while its owner gets to the amendment. The row names
each such document, in backticks and repository-relative, and the check excuses
the term there and nowhere else: not in another document, and never in a
command's or the notifier's strings. The excuse ends with the amendment. Once a
named document no longer carries the term the check refuses the row itself, so
the document comes off the row rather than staying excused for a word it no
longer says.

| Term                | Write instead                                          | Still written in, until its owner amends it                                                                                                    |
| ------------------- | ------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| `cadence`           | how often it repeats, or its schedule                  |                                                                                                                                                |
| `environmental stop` | what happened, with the cause named as outside the work: *the run was ended by something outside the work*, and what that something was. Every form of the word is covered — `environmental refusal` (*a round refused from outside the work*), `environmental cause` (*a cause outside the work*), `refused environmentally` | `docs/designs/recoverable-and-terminal-failures.md` |
| `held for a person` | the mover named: waiting on the development manager's decision, waiting on the harness carrying out her decision, waiting on the architect's ruling — *a person* or *a human* only where the mover is the operator |                                                                                                                                                |
| `idle bound`        | what happened: *the AI session running the developer produced no output for five minutes, so the harness ended the run* |                                                                                                                                                |
| `in force`          | active, or still applies                               |                                                                                                                                                |
| `integration target` | the target branch |  |
| `one pane of glass` | one window                                             |                                                                                                                                                |
| `operator hold` | the operator's pause, after the command that sets it, `yoyo pause`; *the operator paused all harness activity* | `docs/designs/fresh-factory-health-and-preserved-run-recovery.md` `docs/designs/machine-home.md` `docs/designs/management-and-supervision.md` `docs/designs/observability-and-dashboard.md` `docs/designs/v1-harness-design.md` |
| `posture`           | tool access, meaning the tools a role may use          |                                                                                                                                                |
| `seam`              | the boundary, named for what attaches to what          |                                                                                                                                                |
| `sidecar`           | a separate directory outside the repository            |                                                                                                                                                |
| `soak`              | a trial run kept alongside the old path for comparison |                                                                                                                                                |
| `starving`          | stopping                                               |                                                                                                                                                |
| `stall continuation` | what happened: *the development manager resumed the run in the same AI session*, after the harness ended it for producing no output |                                                                                                                                                |
| `supersession pile` | the list of superseded pull requests                   |                                                                                                                                                |
| `tranche`           | stage, or part 1 of 4                                  |                                                                                                                                                |
| `usage window` | the provider's usage limit, until it resets at a named time: *paused on the provider's usage limit until 15:00 PDT* | `docs/designs/management-and-supervision.md` `docs/designs/ownership-and-the-operator.md` |
| `wedged`            | stuck, or the condition said outright                  |                                                                                                                                                |
| `whose-move`        | waiting on you — or, of a thing, who it is waiting on  | `docs/designs/observability-and-dashboard.md`                                                                                                  |

`integration target`, `operator hold`, and `usage window` were retired under the
same decisions of 2026-10-08, the first of the inventory's replacements carried
out by the item replacing the coined vocabulary in printed strings and role
guidance (yoyodyne-ifd.437.19). Each is gone from what the commands print, the
role contracts, the shipped personas, and the pass prompts; the governed
documents named on their rows are the architect's to amend. The record values
and identifiers behind them keep their names: the run record's `usage-window`
cause, the `operator-hold.json` file, and the `integration.target_branch` field.
The rest of the inventory's replacements follow in further parts of that item,
each listed here as it lands.




## Ordinary compounds

The terms check reads for one shape of new word as well as the terms listed
above: a compound, letters joined by hyphens. A compound is ordinary English by
its form when its first part is a prefix English builds words with (`re-run`,
`non-zero`, `self-hosting`), when its last part is one English builds
adjectives with (`read-only`, `repository-wide`, `operator-facing`, and any
ending in *-ed*), when a part is a number or the first part a single letter, when every part is
capitalised, when it starts with the harness's own name or a tool's
(`yoyodyne-report`, `claude-code`), or when it is the name of a document under
`docs/`. [What the terms check counts as a new term](developing-yoyo.md#what-the-terms-check-counts-as-a-new-term)
states the whole rule.

The compounds below are ordinary English, or names, that the rule does not
recognise by form. A compound listed here passes wherever it is written. Adding
one is the answer to a refusal only for a word a reader would not have to look
up; a term of art needs a row in the register, or the ordinary words it stands
for.

`ad-hoc` `add-generic-password` `agent-to-agent` `alt-return` `amendment-id`
`apt-get` `asked-for` `at-least-once` `base-uri` `beads-id` `before-and-after`
`built-in` `built-ins` `by-hand` `byte-identical` `cache-read` `call-site`
`carve-out` `cat-file` `catch-up` `cherry-pick` `cherry-picking` `clean-up`
`closed-loop` `coined-term` `coined-terms` `compare-and-swap` `connect-src`
`ctrl-c` `cut-off` `dead-claim` `default-src` `development-manager` `directive-id`
`end-to-end` `exactly-once` `fan-out` `fast-forward` `fast-forwarding`
`fast-forwards` `find-generic-password` `follow-on` `follow-up` `follow-ups`
`form-action` `frame-ancestors` `free-form` `front-loading` `general-purpose`
`grep-resistant` `hand-edit` `hand-written` `high-judgment` `high-risk`
`highest-priority` `html-comment` `img-src` `input-token` `intra-document`
`intra-file` `local-timezone` `long-running` `long-term` `low-cost` `lower-case`
`lowest-cost` `ls-tree` `machine-readably` `merge-queue` `minute-long`
`mirror-image` `mis-selection` `month-old` `near-autonomous` `near-misses`
`near-term` `nearly-every` `newly-available` `operating-system` `opt-in`
`opt-ins` `opt-out` `parent-child` `part-way` `plain-http` `plain-language`
`plain-word` `pop-up` `pop-ups` `product-manager` `read-back` `read-write`
`reduced-motion` `release-manager` `repo-root` `required-status-check`
`rev-list` `rev-parse` `root-cause` `round-robin` `round-trip` `run-id` `say-so`
`script-src` `several-turn` `shift-return` `side-effect` `skip-worktree`
`ssh-add` `stand-in` `state-root` `status-check` `style-src` `test-data`
`thinking-token` `tie-break` `tie-breaker` `timed-out` `tool-less` `trade-off`
`twice-daily` `unasked-for` `union-merge` `union-merges` `unique-prefix`
`unit-test` `unknown-flag` `url-of-your-fork` `wall-clock` `warning-or-above`
`whole-file` `work-item` `work-item-id` `working-tree` `world-mutating`
`write-once` `your-account` `your-name`

## Adding an entry

Write the row. The register is the authority: a term with a row is permitted and
a term whose row is removed is refused again, and neither is a change to any
code. An entry has to carry all three columns — a row with no definition is the
coinage with the appearance of having been registered, which is worse than no
row, and the check refuses it.

Prefer replacing the word. An entry is for a term that names something real and
would cost more to rename than it costs to define — the mechanism names above
are all of that kind, and each reaches operators through command output that
would have to change with it. A word invented for one sentence does not need an
entry; it needs the ordinary word.

## What the check covers, and what it does not

`internal/terms` runs under `make test` and reads every Markdown file under
`docs/product`, `docs/designs`, and `docs/decisions` for the terms above. One
with no entry here fails, naming the file, the line, and the ordinary wording to
write instead. It also holds this document to its own shape: an entry that
defines nothing, or names no place the term is used, fails the same check.

It reads the strings an operator is shown as well as the documents, since
yoyodyne-ifd.418: every string literal in the Go source of `internal/cli`,
`internal/chat`, `internal/notify`, `internal/slack`, `internal/readmodel`,
`internal/dashboard`, `internal/directive`, `internal/goal`, `internal/backend`,
and `internal/config` — the commands and their help, the conversation, the
notifier's lines, the read model every surface projects, the refusals
`yoyo directive` and `yoyo goals` print, and the provider and configuration
refusals that loading a configuration and `yoyo doctor` print — and the
dashboard's own script, style, and page under
`internal/dashboard/assets`, read whole. A string there is held to the same
register as a sentence in a document, with the one difference that nothing
excuses it: a term retired from the documents and still in the help text has
not been retired, which is what this is for. Only string literals are read, and
only outside test files — a comment is written for whoever reads the code, and a
test names the wording it refuses as often as the wording it wants. A struct
tag is not read either: it is the key a field is written under in a file or a
record, and a key keeps its name when the words around it change. The
dashboard's script is read comments and all, because nothing cheap tells a
comment from a string in a language the check does not parse.

The same vocabulary is applied to what roles write for a person at render time
by the read model: lane reports, post-mortems, digests, sweep and pass accounts,
attention lines, and every message the Slack sink posts. A term without an
entry or listed as replaced is flagged beside the original text, with the
register's replacement where it supplies one. A newly replaced row takes effect
without a code change too. A pass records its findings beside its account and
the next pass is told what to correct; nothing rewrites the author's record.
[Reporting](reporting.md#the-words-roles-write-for-a-person) says where the flags
are shown and what is carried forward.

A term of more than one word is looked for however its parts are spaced —
`minute zero`, `minute-zero`, `minutezero`, and a `minute` a line wrap left with
its `zero` on the next line are the same coinage and all four fail. That cuts
both ways: a row here permits every spelling of its term, so registering
`minute zero` is what makes the invariant's `minute-zero` legal, and no variant
of a registered term is reported as though nothing defined it. The tolerance
applies only where the term is already written in parts — a term written here as
one word is looked for as one word, so `hand back` in a sentence about handing
something back is not reported as `handback`. Nothing is matched across a blank
line or a fenced block, because a term cannot wrap across either.

It reads the guides for a few terms only, since yoyodyne-ifd.360: the README
and every Markdown file under `docs/` outside the homes above, this document,
[the vocabulary inventory](vocabulary-inventory.md) — which names every coined
term, the retired ones included, because deciding about each is its purpose —
and the records under `docs/diagnoses`, `docs/experiments`, and
`docs/releases`. A guide is held to the register only for a term the check
marks as used in the guides — today `re-arm`, and the three retired by the item
retiring them (yoyodyne-ifd.437.17), `environmental stop`, `idle bound`, and
`stall continuation`, which the guides had been written with — so a guide that
leans on a row fails once the row is gone, a guide that writes one of the
three retired terms fails outright, and a guide is not read for any other listed
term. Every guide is read for new compound words, which the paragraph on
compounds below describes.

Three things it deliberately does not read. A document's frontmatter is identity
and revision history, and a revision's recorded reason is what somebody decided
in their own words on a date — rewriting one to change a word falsifies a record
instead of clarifying a sentence. Fenced blocks are code. And, for the terms
listed above, the guides for every other term, the records under `docs/`, the
tracker's own items, and the Go source outside the packages named above are
outside it; the compound half reads more widely, and never the records or the
tracker's items, as the paragraph on compounds below says: they
are operator-facing too, but no sweep has been run over them and holding a
document to an inventory nobody took over it would fail on words nobody was
asked about.

It also reads for terms nobody has listed yet, in one shape: a compound, letters
joined by hyphens. It reads every string literal holding a space in the Go
source under `cmd` and `internal`, the personas under `internal/config/builtin`
and `.yoyodyne/personas`, the guides (not the records under `docs/diagnoses`,
`docs/experiments`, and `docs/releases`, this register, or the vocabulary
inventory), and the governed documents, and refuses a
compound nothing accounts for, naming the file and the line. A compound is
accounted for by a row in either table above, by a term in the vocabulary
inventory still waiting on its decision, by being ordinary English by its form
or by the list under [ordinary compounds](#ordinary-compounds), or by being one
of the compounds the inventory lists as still to be decided — the ones already written
when this half of the check began, each allowed by name until somebody decides
it. So a compound written from now on is refused the first time it is written,
and the answer to the refusal is the ordinary words, a row here, or, for a word
nobody would have to look up, a place on the ordinary list. What it does not
read for compounds is anything that is not prose: a code span, a link's target,
a fenced block, a flag, a path or a file name, a value between quotes, and a
string with no space in it.

The guidance a role reads is held more tightly than the strings a command
prints, because a role copies its words from it. The personas and bundle the
executable ships under `internal/config/builtin`, and every string literal in
the packages that build the role contracts and the pass prompts — the developer
contract and the recurring prompts in `internal/orchestrator`, the conversation
contracts in `internal/chat`, the reviewer's in `internal/review`, the contract
sections every role is given, and the prompts `yoyo init` writes — carry no term
listed above as replaced, and a row naming a governed document excuses nothing
there. The one exception is the quoted example of what not to write, *stopped by
the harness's idle bound when the provider's stream went silent, settled as an
environmental stop*, which the writing rule quotes on purpose and which is not
read. The copies a project binds under `.yoyodyne/personas` are its own and are
not read by this half. `internal/terms` (`roleguidance.go`) holds the list of
places it reads.

What no check can recognize is an ordinary word given a sense of its own this
morning — a `lane`, a `docket` — because nothing in its shape says so. That is
the reviewer's, and it is written into the reviewer persona as a finding class:
a coined term in operator-facing text with no entry here is a finding, whatever
else the change does. The check is the floor under it, so a term once swept out
cannot quietly come back, and a new compound cannot arrive unnoticed.
