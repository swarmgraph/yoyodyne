# The harness's own vocabulary

Every term of art the harness uses where a person or a role reads it: where it
appears, how often, what it means in one plain sentence, and the decision made
on it — replace it with ordinary words, or register it in
[the register](terms.md) as a term that names something real. The operator
asked for it on 2026-09-27, after a program manager's report reached him saying
"environmental stop" and "idle bound" and nothing told him what either meant.
It is the inventory of the harness's coined vocabulary (yoyodyne-ifd.437.18).
The Lead Product Manager made the decisions on every term in it on 2026-10-08.

**This document is generated. Edit the script, not this file.** Run
`go run ./scripts/vocabulary > docs/vocabulary-inventory.md` from the
repository root. The terms, what they mean, and what was decided for each are
in `internal/terms/inventory`; the counts are measured each time it
runs. These were measured on 2026-10-08.

## The decisions

The decisions below were made on 2026-10-08. The inventory began as a list of
proposals; the Lead Product Manager recorded on the inventory's work item
(yoyodyne-ifd.437.18) that every one of them stands as decided, unchanged, and
the items admitted beside it carry the decisions out:

- replacing the coined vocabulary in printed strings, role contracts, shipped
  personas, and pass prompts (yoyodyne-ifd.437.19);
- replacing it in the shipped guides (yoyodyne-ifd.437.20);
- the architect replacing it in the governed documents (yoyodyne-ifd.437.21);
- the terms check, which refuses a compound word that is neither registered,
  replaced, a term below, ordinary English, nor on the list at the end of this
  document (yoyodyne-ifd.437.22).

Three terms are already being replaced on their own item — 'environmental
stop', 'idle bound', and 'stall continuation' (yoyodyne-ifd.437.17) — and
what roles write for a person is being held to the register on its own item
too (yoyodyne-ifd.437.16). When a decision changes, change the term's entry in
`internal/terms/inventory` and run the script again, so this document
says what stands.

The "In the register today" column says how far each decision has been carried
out: a term decided for registration reads "registered" once its row is
written, and a term decided for replacement reads "replaced" once
[the register](terms.md) lists it as replaced, which is when the terms check
begins refusing it.

A term decided for replacement keeps its name wherever it is an identifier: a
type, a field in a stored record, a configuration key, a package. What changes
is the words a person or a role reads.

## What was counted

- **Printed strings:** every string literal holding a space in the Go source,
  outside tests and test data, counted by package. That is what the commands
  print and their help, `yoyo status`, the development manager's list of
  stopped runs, Slack messages, and the read model the dashboard shows — and the
  role contracts and pass prompts too, which are Go strings. The dashboard's own
  files under `internal/dashboard/assets` are read whole.
- **Personas:** the shipped personas and bundle under
  `internal/config/builtin`.
- **Guides:** the README and every Markdown file under `docs/` that is
  not a governed document, the register, a record under `docs/diagnoses`,
  `docs/experiments` or `docs/releases`, or this document.
- **Governed documents:** every Markdown file under `docs/product`,
  `docs/designs`, and `docs/decisions`, less its frontmatter.

The example of what not to write that the personas and the role contracts
quote on purpose — *stopped by the harness's idle bound when the provider's
stream went silent, settled as an environmental stop* — is not counted: it is
meant to stay where it is.

A count is of occurrences, matched at the start of a word and ignoring case, so
a stem counts its inflections: `docket` counts `docketed`. A
term written in parts is matched however its parts are spaced. Where a term's
ordinary use cannot be told from its use as a term, the entry says so, and its
count is an upper bound.

Not counted: the tracker's own items, which are the Lead Product Manager's to
reword; the records under `docs/`, which say what was true on a date in
the words used then; code comments; and a string literal with no space in it,
which is a key or an identifier rather than words anybody reads.

## Summary

| Term | Decided | In the register today | Printed strings | Personas | Guides | Governed documents |
|---|---|---|---:|---:|---:|---:|
| [`stoppage`](#stoppage) | replace | — | 174 | 2 | 196 | 10 |
| [`docket`](#docket) | replace | registered | 251 | 1 | 311 | 14 |
| [`crossing`](#crossing) | replace | — | 56 | 0 | 115 | 6 |
| [`carry-out`](#carry-out) | replace | — | 38 | 0 | 36 | 1 |
| [`environmental stop`](#environmental-stop) | replace | replaced | 4 | 0 | 26 | 1 |
| [`idle bound`](#idle-bound) | replace | replaced | 4 | 0 | 0 | 0 |
| [`provider's stream`](#providers-stream) | replace | — | 6 | 0 | 6 | 0 |
| [`stall`](#stall) | replace | — | 192 | 0 | 216 | 11 |
| [`continuation`](#continuation) | replace | — | 89 | 0 | 121 | 20 |
| [`lane`](#lane) | replace | — | 150 | 23 | 115 | 83 |
| [`summons`](#summons) | replace | — | 38 | 2 | 53 | 1 |
| [`park`](#park) | register | registered | 135 | 0 | 232 | 11 |
| [`pull`](#pull) | replace | — | 268 | 6 | 369 | 6 |
| [`the line`](#the-line) | replace | — | 74 | 2 | 132 | 6 |
| [`brake`](#brake) | replace | registered | 85 | 4 | 143 | 6 |
| [`probe`](#probe) | replace | — | 97 | 4 | 159 | 4 |
| [`watch session`](#watch-session) | replace | — | 84 | 1 | 149 | 6 |
| [`the standing`](#the-standing) | replace | — | 35 | 14 | 39 | 12 |
| [`settle`](#settle) | replace | — | 230 | 2 | 349 | 21 |
| [`witness`](#witness) | register | registered | 21 | 0 | 35 | 1 |
| [`drain`](#drain) | replace | — | 52 | 2 | 135 | 3 |
| [`mover`](#mover) | replace | — | 86 | 0 | 36 | 15 |
| [`shadow review`](#shadow-review) | register | registered | 36 | 0 | 18 | 0 |
| [`landing`](#landing) | replace | — | 203 | 7 | 292 | 17 |
| [`exchange`](#exchange) | register | registered | 151 | 1 | 113 | 25 |
| [`intake hold`](#intake-hold) | register | registered | 163 | 2 | 163 | 18 |
| [`operator hold`](#operator-hold) | replace | replaced | 8 | 0 | 28 | 7 |
| [`sweep`](#sweep) | register | registered | 152 | 4 | 366 | 14 |
| [`firing`](#firing) | replace | — | 57 | 1 | 98 | 4 |
| [`pass`](#pass) | replace | — | 176 | 2 | 343 | 50 |
| [`context bundle`](#context-bundle) | replace | — | 2 | 0 | 12 | 4 |
| [`repair`](#repair) | register | registered | 290 | 18 | 572 | 32 |
| [`repair grant`](#repair-grant) | replace | — | 37 | 0 | 57 | 0 |
| [`needs-a-human`](#needs-a-human) | replace | — | 34 | 2 | 47 | 6 |
| [`integration target`](#integration-target) | replace | replaced | 8 | 0 | 4 | 0 |
| [`promotion`](#promotion) | replace | — | 144 | 2 | 467 | 73 |
| [`lease`](#lease) | replace | — | 52 | 0 | 207 | 57 |
| [`floor`](#floor) | replace | — | 30 | 0 | 26 | 4 |
| [`triage`](#triage) | register | registered | 352 | 5 | 322 | 15 |
| [`protected path`](#protected-path) | register | registered | 16 | 0 | 92 | 9 |
| [`direct-work`](#direct-work) | register | registered | 16 | 0 | 54 | 2 |
| [`side thread`](#side-thread) | register | registered | 145 | 0 | 75 | 13 |
| [`raise`](#raise) | replace | — | 27 | 0 | 20 | 1 |
| [`remit`](#remit) | replace | — | 6 | 1 | 13 | 9 |
| [`wake`](#wake) | replace | — | 40 | 0 | 41 | 4 |
| [`gate`](#gate) | replace | — | 127 | 4 | 335 | 66 |
| [`usage window`](#usage-window) | replace | replaced | 5 | 0 | 41 | 2 |
| [`sink`](#sink) | replace | registered | 96 | 1 | 238 | 19 |
| [`heartbeat`](#heartbeat) | keep | registered | 5 | 0 | 36 | 1 |
| [`steer`](#steer) | keep | registered | 13 | 0 | 43 | 1 |
| [`handback`](#handback) | keep | registered | 0 | 0 | 5 | 0 |
| [`discharge`](#discharge) | keep | registered | 23 | 0 | 20 | 2 |
| [`re-arm`](#re-arm) | keep | registered | 112 | 0 | 137 | 3 |
| [`seat`](#seat) | keep | registered | 10 | 1 | 19 | 2 |
| [`minute zero`](#minute-zero) | keep | registered | 4 | 0 | 2 | 2 |

## The terms

### stoppage

- **Means:** A run that stopped and is waiting for the development manager to decide what happens to it.
- **Decided:** replace it. Write instead: a stopped run, or a run that stopped.
- **Where:** 382 in all, in 27 places: `internal/orchestrator` 71, `docs/conversation.md` 43, `docs/work.md` 40, `docs/configuration.md` 36, `docs/operations.md` 34, `internal/runstate` 33, and 21 more.

### docket

- **Means:** The development manager's list of stopped runs, runs that died before they started, and items dispatch would not start; `docketed` is being put on it.
- **Decided:** replace it. Write instead: the development manager's list of stopped runs; for `docketed`, put in front of the development manager. The register's row is retired once the replacements land; `DocketStore` and other identifiers keep their names.
- **Where:** 577 in all, in 30 places: `internal/orchestrator` 94, `docs/conversation.md` 73, `docs/operations.md` 72, `docs/work.md` 64, `docs/configuration.md` 43, `internal/cli` 42, and 24 more.
- **Note:** Registered today. It is decided for replacement because it is the most frequent term in this inventory and the operator asked what `docketed` meant.

### crossing

- **Means:** Two things: the development manager raising one item's budget cap by one step past a refusal, with a reason; and a conversation turn answered by a permitted alternate model when the configured one refused.
- **Decided:** replace it. Write instead: for a budget, raising the cap ("raised the repair cap to 3"); for a model, a turn answered by the alternate model. The triage decision value `cross` keeps its name.
- **Where:** 177 in all, in 23 places: `docs/configuration.md` 42, `docs/configuration/recovery.md` 41, `internal/runstate` 17, `internal/chat` 15, `internal/notify` 11, `docs/authority-inventory.md` 9, and 17 more.

### carry-out

- **Means:** The harness acting on a decision the development manager recorded about a stopped run.
- **Decided:** replace it. Write instead: carrying out her decision; for the queue of them, decisions waiting to be carried out.
- **Where:** 75 in all, in 17 places: `internal/runstate` 20, `docs/operations.md` 11, `internal/orchestrator` 9, `docs/work.md` 9, `internal/dashboard/assets/dashboard.js` 3, `docs/configuration.md` 3, and 11 more.
- **Note:** Only the hyphenated noun is counted; the verb `carry out` is ordinary.

### environmental stop

- **Means:** A run ended by something outside the work — the sandbox, the network, the tracker, a provider limit — which spends none of the item's budgets.
- **Decided:** replace it. Write instead: stopped by something outside the work, naming what it was.
- **Where:** 31 in all, in 4 places: `docs/run-stops.md` 26, `internal/terms/inventory` 3, `internal/terms` 1, `docs/designs/recoverable-and-terminal-failures.md` 1.
- **Note:** The item replacing the three words the operator met first (yoyodyne-ifd.437.17) replaces this one. `environmental refusal` and `environmental cause` are counted with it.

### idle bound

- **Means:** The harness ending a run whose AI session produced no output for a set time.
- **Decided:** replace it. Write instead: the time limit on a session that produces no output: "produced no output for five minutes, so the harness ended the run".
- **Where:** 4 in all, in 2 places: `internal/terms` 2, `internal/terms/inventory` 2.
- **Note:** The item replacing the three words the operator met first (yoyodyne-ifd.437.17) replaces this one.

### provider's stream

- **Means:** The output of the AI session running a role; `went silent` is that output stopping.
- **Decided:** replace it. Write instead: the AI session's output; for `went silent`, produced no output.
- **Where:** 12 in all, in 7 places: `internal/terms/inventory` 5, `docs/operations.md` 2, `internal/cli` 1, `docs/configuration.md` 1, `docs/configuration/publishing.md` 1, `docs/provider-plugins.md` 1, and 1 more.

### stall

- **Means:** Nothing starting although work is ready, or a session producing no output.
- **Decided:** replace it. Write instead: nothing is starting although work is ready; or, of a session, produced no output.
- **Where:** 419 in all, in 30 places: `docs/operations.md` 92, `internal/runstate` 49, `docs/reporting.md` 33, `internal/cli` 32, `internal/orchestrator` 31, `internal/dashboard/assets/dashboard.js` 30, and 24 more.
- **Note:** Counts include the `--stall-after` flag of `yoyo work`, whose name changes only with the command; a rename is its own decision.

### continuation

- **Means:** Resuming a stopped run in its kept worktree and session rather than starting it fresh; a `stall continuation` resumes one ended for producing no output, a `repair continuation` resumes one mid-repair.
- **Decided:** replace it. Write instead: resuming the stopped run in the same session.
- **Where:** 230 in all, in 22 places: `internal/orchestrator` 54, `docs/operations.md` 32, `docs/configuration/recovery.md` 26, `docs/configuration.md` 21, `docs/conversation.md` 15, `docs/run-stops.md` 15, and 16 more.
- **Note:** The item replacing the three words the operator met first (yoyodyne-ifd.437.17) replaces `stall continuation`.

### lane

- **Means:** The work stream one program manager owns, identified by its tracker label.
- **Decided:** replace it. Write instead: work stream, the word the register's `program manager` row already uses; for `lane report`, the program manager's report; for `lane label`, the work stream's label.
- **Where:** 371 in all, in 35 places: `internal/chat` 74, `docs/designs/program-manager.md` 57, `docs/conversation.md` 37, `docs/configuration.md` 32, `internal/runstate` 23, `docs/authority-inventory.md` 20, and 29 more.

### summons

- **Means:** The harness starting a development manager turn at once rather than at its next scheduled time.
- **Decided:** replace it. Write instead: starts the development manager's turn at once.
- **Where:** 94 in all, in 16 places: `docs/configuration.md` 18, `internal/orchestrator` 12, `internal/runstate` 9, `docs/configuration/runs.md` 9, `docs/operations.md` 9, `internal/cli` 8, and 10 more.

### park

- **Means:** Keeping an item in the Lead Product Manager's order but stopping the harness from selecting it until somebody releases it with `unpark`, with the reason shown wherever it is listed.
- **Decided:** register it, with the meaning above: `park` and `unpark` are actions a role records and the operator reads in backlog listings, so the word is a name rather than decoration.
- **Where:** 378 in all, in 34 places: `docs/operations.md` 68, `docs/work.md` 53, `internal/chat` 44, `docs/conversation.md` 35, `internal/orchestrator` 28, `internal/cli` 19, and 28 more.

### pull

- **Means:** One time the harness picks the next ready item and starts a run on it.
- **Decided:** replace it. Write instead: the next time the harness picks work.
- **Where:** 649 in all, in 37 places: `docs/work.md` 85, `internal/orchestrator` 80, `docs/operations.md` 80, `docs/configuration.md` 77, `docs/configuration/runs.md` 40, `internal/notify` 36, and 31 more.
- **Note:** `pull request` is not counted. `git pull` and other ordinary uses are, so the counts are an upper bound.

### the line

- **Means:** The harness pictured as a production line choosing and running work, as in "the line is choosing nothing".
- **Decided:** replace it. Write instead: the harness, or say what is happening: "no work is starting".
- **Where:** 214 in all, in 32 places: `docs/operations.md` 50, `internal/notify` 33, `docs/conversation.md` 16, `docs/slack/setup.md` 15, `docs/configuration.md` 14, `docs/reporting.md` 11, and 26 more.
- **Note:** Counts include ordinary uses such as "the top line" of a message, so they are an upper bound.

### brake

- **Means:** The automatic hold on starting new work after a set number of blocked runs in a row, also written `storm brake`.
- **Decided:** replace it. Write instead: the automatic stop on starting work; for `braked`, stopped. The register's row is retired once the replacements land.
- **Where:** 238 in all, in 23 places: `docs/operations.md` 32, `docs/configuration.md` 30, `docs/configuration/runs.md` 22, `docs/reporting.md` 20, `internal/runstate` 17, `internal/chat` 15, and 17 more.
- **Note:** Registered today. Decided for replacement because the operator has asked for 'stopped' in place of 'braked'.

### probe

- **Means:** Two things: one trial run started under the automatic stop to see whether work can land again; and the first command a developer runs in its worktree to show commands can run there.
- **Decided:** replace it. Write instead: a trial run; for the developer's, a first command run to show commands work. The decision value `probe` and the verification block's `probe` field keep their names.
- **Where:** 264 in all, in 27 places: `docs/configuration.md` 37, `docs/operations.md` 37, `internal/orchestrator` 29, `internal/runstate` 22, `docs/configuration/runs.md` 19, `docs/configuration/recovery.md` 16, and 21 more.

### watch session

- **Means:** The long-running `yoyo work --watch` process that picks ready work and starts runs.
- **Decided:** replace it. Write instead: the process that starts work, or `yoyo work --watch` where the command is the point.
- **Where:** 240 in all, in 27 places: `docs/operations.md` 64, `docs/configuration.md` 20, `docs/work.md` 18, `docs/reporting.md` 17, `internal/runstate` 16, `internal/readmodel` 15, and 21 more.

### the standing

- **Means:** The list of role instances and what each is doing, where a restart request is shown.
- **Decided:** replace it. Write instead: the list of role instances.
- **Where:** 100 in all, in 29 places: `docs/operations.md` 16, `internal/dashboard/assets/dashboard.js` 13, `internal/config` 8, `docs/configuration.md` 6, `internal/chat` 4, `internal/terms/inventory` 4, and 23 more.
- **Note:** Counts include ordinary uses such as "the standing decision", so they are an upper bound.

### settle

- **Means:** Bringing something interrupted or half-finished to a recorded end: a run, a directive, a promotion.
- **Decided:** replace it. Write instead: finish, resolve, or record how it ended — whichever the sentence means.
- **Where:** 602 in all, in 52 places: `docs/operations.md` 138, `internal/orchestrator` 59, `docs/configuration.md` 44, `docs/conversation.md` 38, `internal/cli` 32, `docs/work.md` 30, and 46 more.

### witness

- **Means:** The tracker's recorded copy of an item's `Goal served:` line, kept so a destroyed attribution can be found and restored; `yoyo goals witness` records it.
- **Decided:** register it, with the meaning above: it is a subcommand of `yoyo goals`, so the word is typed and cannot be swept out of its help without renaming the command.
- **Where:** 57 in all, in 11 places: `internal/cli` 13, `docs/configuration.md` 12, `docs/configuration/goals.md` 12, `docs/artifacts.md` 10, `internal/beads` 3, `internal/goal` 2, and 5 more.

### drain

- **Means:** A queue emptying, or a restarting process waiting for the runs it hosts to finish first.
- **Decided:** replace it. Write instead: empties; for a restart, waiting for its runs to finish. `execution.redeploy_drain_limit` keeps its name.
- **Where:** 192 in all, in 21 places: `docs/configuration.md` 40, `docs/operations.md` 38, `docs/work.md` 23, `internal/orchestrator` 17, `docs/configuration/runs.md` 12, `internal/cli` 10, and 15 more.

### mover

- **Means:** Whoever an item or a stopped run is waiting on to act next.
- **Decided:** replace it. Write instead: who it is waiting on. The `waiting_on` field keeps its name.
- **Where:** 137 in all, in 14 places: `internal/dashboard/assets/dashboard.js` 68, `docs/operations.md` 21, `internal/triage` 13, `docs/conversation.md` 7, `docs/designs/program-manager.md` 6, `docs/work.md` 5, and 8 more.

### shadow review

- **Means:** A second review of the same change by another model, recorded for comparison and deciding nothing.
- **Decided:** register it, with the meaning above: it is the `--shadow` flag of `yoyo review`.
- **Where:** 54 in all, in 6 places: `internal/cli` 25, `docs/work.md` 16, `internal/dashboard/assets/dashboard.css` 6, `internal/shadow` 3, `internal/terms/inventory` 2, `docs/reporting.md` 2.

### landing

- **Means:** A change merging onto the target branch; in a developer's reply, its claim of what that merge does to the item — closes it, lands evidence, or escalates.
- **Decided:** replace it. Write instead: merge, or merged change; for a developer's claim, what the change does to the item. The `yoyodyne-landing` block keeps its name.
- **Where:** 519 in all, in 39 places: `docs/configuration.md` 99, `docs/operations.md` 91, `internal/orchestrator` 62, `internal/notify` 37, `internal/runstate` 30, `docs/work.md` 28, and 33 more.

### exchange

- **Means:** A question one role puts to another, which the harness delivers by invoking the other role and records with what it cost.
- **Decided:** register it, with the meaning above: it is the command `yoyo exchange`.
- **Where:** 290 in all, in 29 places: `internal/cli` 41, `internal/exchange` 38, `docs/conversation.md` 25, `docs/configuration.md` 21, `internal/notify` 20, `internal/runstate` 20, and 23 more.
- **Note:** Counts include ordinary uses, so they are an upper bound.

### intake hold

- **Means:** A stop on the harness choosing new work by itself, placed by the operator or by the automatic stop; `yoyo release` lifts it, and work already running carries on.
- **Decided:** register it, with the meaning above: it names the operator's control over autonomous work, is written in an architectural invariant, and has its own command to lift it.
- **Where:** 346 in all, in 35 places: `internal/orchestrator` 36, `internal/runstate` 30, `docs/operations.md` 30, `docs/configuration.md` 26, `internal/chat` 25, `internal/cli` 25, and 29 more.

### operator hold

- **Means:** The operator pausing everything the harness would spend on a provider, with `yoyo pause`.
- **Decided:** replace it. Write instead: the operator's pause, after the command that sets it.
- **Where:** 43 in all, in 15 places: `docs/delivery-pipeline-baseline.md` 13, `internal/terms` 4, `docs/operations.md` 4, `docs/slack/setup.md` 4, `internal/terms/inventory` 3, `docs/team-mode-coordination.md` 3, and 9 more.

### sweep

- **Means:** One run of a recurring task and the record it leaves; `yoyo sweeps` reads them.
- **Decided:** register it, with the meaning above: it is the command `yoyo sweeps` and the heading every recurring task's findings are listed under.
- **Where:** 536 in all, in 39 places: `docs/operations.md` 165, `docs/configuration.md` 51, `internal/orchestrator` 49, `internal/cli` 40, `internal/runstate` 32, `docs/conversation.md` 29, and 33 more.

### firing

- **Means:** One scheduled run of a recurring task, as in `FAILED FIRING`.
- **Decided:** replace it. Write instead: run of the task; `FAILED FIRING` becomes `FAILED RUN`.
- **Where:** 160 in all, in 19 places: `docs/configuration.md` 44, `docs/operations.md` 29, `internal/orchestrator` 18, `internal/notify` 10, `internal/runstate` 10, `docs/reporting.md` 10, and 13 more.

### pass

- **Means:** One scheduled turn of a program manager, the supervisor, or the scheduler.
- **Decided:** replace it. Write instead: turn, or run — whichever the role does.
- **Where:** 571 in all, in 40 places: `docs/operations.md` 131, `docs/configuration.md` 90, `internal/cli` 45, `internal/orchestrator` 44, `docs/work.md` 42, `docs/reporting.md` 26, and 34 more.
- **Note:** Only noun phrases are counted; the verb, as in "checks pass", is ordinary.

### context bundle

- **Means:** The documents and state a role is given to read at the start of each turn.
- **Decided:** replace it. Write instead: what the role is given to read at the start of its turn.
- **Where:** 18 in all, in 10 places: `docs/configuration.md` 3, `docs/delivery-pipeline-baseline.md` 3, `docs/designs/v1-harness-design.md` 3, `internal/terms/inventory` 2, `docs/configuration/artifacts.md` 2, `docs/configuration/runs.md` 1, and 4 more.

### repair

- **Means:** A developer's further attempt at its own change, in the same worktree, after a failed check or review findings; `yoyo triage repair` asks for one.
- **Decided:** register it, with the meaning above: it is a `yoyo triage` verb and a budget the status line counts.
- **Where:** 912 in all, in 49 places: `docs/configuration.md` 120, `internal/orchestrator` 118, `docs/operations.md` 75, `docs/configuration/recovery.md` 73, `docs/run-stops.md` 62, `docs/work.md` 60, and 43 more.
- **Note:** Counts include ordinary uses such as "backlog repair", so they are an upper bound.

### repair grant

- **Means:** The development manager allowing a stopped item further review rounds.
- **Decided:** replace it. Write instead: further review rounds granted.
- **Where:** 94 in all, in 15 places: `docs/configuration.md` 17, `docs/configuration/recovery.md` 14, `internal/orchestrator` 13, `docs/operations.md` 9, `internal/runstate` 7, `internal/cli` 6, and 9 more.

### needs-a-human

- **Means:** The line in `yoyo status` and the channel listing what is waiting on a person.
- **Decided:** replace it. Write instead: waiting on you, the operator's own words.
- **Where:** 89 in all, in 23 places: `docs/operations.md` 26, `internal/dashboard/assets/dashboard.js` 7, `internal/orchestrator` 7, `internal/readmodel` 7, `docs/reporting.md` 7, `docs/configuration.md` 6, and 17 more.

### integration target

- **Means:** The branch an approved change is merged onto.
- **Decided:** replace it. Write instead: target branch, which the same strings already use.
- **Where:** 12 in all, in 6 places: `internal/runstate` 4, `internal/terms` 2, `internal/terms/inventory` 2, `docs/delivery-pipeline-baseline.md` 2, `docs/configuration.md` 1, `docs/conversation.md` 1.

### promotion

- **Means:** Moving the target branch forward to include an approved change.
- **Decided:** replace it. Write instead: merging the change onto the target branch.
- **Where:** 686 in all, in 43 places: `docs/operations.md` 115, `docs/configuration.md` 69, `internal/orchestrator` 58, `docs/work.md` 51, `docs/delivery-pipeline-baseline.md` 38, `docs/run-stops.md` 38, and 37 more.
- **Note:** An architectural invariant says `promotion`; its wording is the architect's.

### lease

- **Means:** A lock one process holds so no other process does the same thing at the same time.
- **Decided:** replace it. Write instead: lock.
- **Where:** 316 in all, in 36 places: `docs/authority-inventory.md` 63, `docs/operations.md` 54, `docs/configuration.md` 28, `internal/runstate` 18, `docs/work.md` 10, `docs/designs/management-and-supervision.md` 10, and 30 more.
- **Note:** `release` is a different word and is not counted.

### floor

- **Means:** A total that is at least the figure shown, because some runs left no record to price.
- **Decided:** replace it. Write instead: at least: "at least $4.20; 2 runs could not be priced".
- **Where:** 60 in all, in 18 places: `internal/dashboard/assets/dashboard.js` 14, `internal/cli` 9, `docs/configuration.md` 6, `docs/developing-yoyo.md` 5, `docs/operations.md` 5, `internal/orchestrator` 3, and 12 more.

### triage

- **Means:** The development manager deciding what happens to each stopped run, and `yoyo triage` carrying the decision out.
- **Decided:** register it, with the meaning above: it is the command `yoyo triage`.
- **Where:** 694 in all, in 41 places: `internal/orchestrator` 118, `internal/runstate` 78, `docs/configuration.md` 77, `internal/cli` 61, `docs/configuration/recovery.md` 53, `docs/operations.md` 49, and 35 more.

### protected path

- **Means:** A path a developer's change may not touch unless its work item carries a `protected-path grant:` line naming it.
- **Decided:** register it, with the meaning above: it is the grant line an item carries and the refusal a developer receives.
- **Where:** 117 in all, in 25 places: `docs/authority-inventory.md` 31, `docs/configuration.md` 21, `internal/orchestrator` 8, `docs/configuration/artifacts.md` 7, `docs/delivery-pipeline-baseline.md` 7, `internal/terms/inventory` 5, and 19 more.

### direct-work

- **Means:** The two authorities a person can hold in a project's configuration: `own-intent`, over what the product is for, and `direct-work`, over work already running.
- **Decided:** register it, with the meaning above: they are configuration values a person writes.
- **Where:** 72 in all, in 11 places: `docs/slack/setup.md` 19, `docs/configuration.md` 12, `docs/configuration/agents.md` 11, `internal/slack` 7, `docs/reporting.md` 6, `internal/config` 5, and 5 more.

### side thread

- **Means:** A secondary conversation a role opens to work something out, which judges and drafts but acts on nothing, and merges its conclusion back into the main conversation.
- **Decided:** register it, with the meaning above: register `side thread` and write it for `side stream`, which is the same thing; the `sidestream` package keeps its name.
- **Where:** 233 in all, in 15 places: `internal/cli` 46, `internal/runstate` 39, `internal/sidestream` 36, `docs/authority-inventory.md` 32, `docs/configuration.md` 19, `internal/agentcontext` 14, and 9 more.

### raise

- **Means:** A role putting an item in front of the development manager as unmeetable as written.
- **Decided:** replace it. Write instead: escalating the item as unmeetable.
- **Where:** 48 in all, in 8 places: `internal/chat` 19, `docs/conversation.md` 17, `internal/triage` 3, `docs/work.md` 3, `internal/orchestrator` 2, `internal/terms/inventory` 2, and 2 more.

### remit

- **Means:** What a program manager is responsible for.
- **Decided:** replace it. Write instead: what it is responsible for.
- **Where:** 29 in all, in 7 places: `docs/configuration.md` 13, `docs/designs/program-manager.md` 7, `internal/config` 3, `internal/chat` 2, `docs/designs/tool-interface.md` 2, `internal/cli` 1, and 1 more.

### wake

- **Means:** The harness starting another turn of a role or resuming a waiting process.
- **Decided:** replace it. Write instead: started again, or given another turn.
- **Where:** 85 in all, in 17 places: `internal/orchestrator` 21, `docs/configuration.md` 21, `internal/cli` 8, `docs/operations.md` 7, `docs/work.md` 5, `internal/readmodel` 4, and 11 more.

### gate

- **Means:** A check a change has to pass before it merges: the configured checks, independent review, the protected paths.
- **Decided:** replace it. Write instead: name the check.
- **Where:** 532 in all, in 59 places: `docs/configuration.md` 76, `docs/operations.md` 63, `docs/work.md` 46, `internal/cli` 40, `docs/delivery-pipeline-baseline.md` 27, `docs/conversation.md` 22, and 53 more.
- **Note:** Counts are of the whole word only, so `gates` and `gated` are not included.

### usage window

- **Means:** The period a provider's usage limit applies to, after which it resets.
- **Decided:** replace it. Write instead: the provider's usage limit, until it resets at a named time.
- **Where:** 48 in all, in 14 places: `docs/operations.md` 12, `docs/run-stops.md` 8, `docs/reporting.md` 7, `docs/slack/setup.md` 7, `internal/terms` 2, `internal/terms/inventory` 2, and 8 more.

### sink

- **Means:** The process that posts to Slack.
- **Decided:** replace it. Write instead: the Slack reporter. The register's row is retired once the replacements land; `internal/slack` identifiers keep their names.
- **Where:** 354 in all, in 27 places: `docs/slack/setup.md` 95, `docs/reporting.md` 44, `docs/operations.md` 39, `internal/cli` 35, `internal/slack` 28, `internal/doctor` 24, and 21 more.
- **Note:** Registered today. The coined-terms sweep (yoyodyne-ifd.206) called it the strongest rename candidate: a dataflow term with no ordinary meaning that helps.

### heartbeat

- **Means:** How often `yoyo slack` repeats that no work is starting.
- **Decided:** no change. It is registered; it is a flag name whose help says it in plain words.
- **Where:** 42 in all, in 9 places: `docs/slack/setup.md` 15, `docs/reporting.md` 8, `docs/operations.md` 7, `internal/cli` 4, `docs/configuration.md` 2, `docs/configuration/runs.md` 2, and 3 more.

### steer

- **Means:** Directing the work, or changing what is being worked on.
- **Decided:** no change. It is registered; an ordinary verb that reads correctly where it is used.
- **Where:** 57 in all, in 15 places: `docs/slack/setup.md` 18, `internal/slack` 6, `docs/configuration.md` 5, `docs/reporting.md` 4, `internal/cli` 3, `docs/configuration/agents.md` 3, and 9 more.

### handback

- **Means:** Handing the work back to the developer that made it.
- **Decided:** no change. It is registered; it reaches nobody outside code.
- **Where:** 5 in all, in 3 places: `docs/run-stops.md` 3, `docs/operations.md` 1, `docs/work.md` 1.

### discharge

- **Means:** A change being the work its item asked for, so the item closes on it.
- **Decided:** no change. It is registered; the developer and reviewer contracts decide on it.
- **Where:** 45 in all, in 12 places: `internal/orchestrator` 13, `docs/work.md` 9, `internal/landing` 8, `docs/delivery-pipeline-baseline.md` 6, `docs/operations.md` 2, `internal/review` 1, and 6 more.

### re-arm

- **Means:** Repeating a merge request a forge dropped, once per publication.
- **Decided:** no change. It is registered; it is the verb `yoyo triage rearm`.
- **Where:** 252 in all, in 19 places: `internal/orchestrator` 53, `docs/operations.md` 36, `docs/configuration.md` 32, `docs/configuration/recovery.md` 29, `internal/cli` 25, `docs/conversation.md` 21, and 13 more.

### seat

- **Means:** One running instance of a role, as against the developer slot it fills.
- **Decided:** no change. It is registered; the operator's own word.
- **Where:** 32 in all, in 9 places: `docs/configuration.md` 9, `docs/operations.md` 7, `internal/runstate` 4, `internal/orchestrator` 3, `docs/configuration/runs.md` 3, `internal/chat` 2, and 3 more.

### minute zero

- **Means:** Before development begins.
- **Decided:** no change. It is registered while an invariant's wording carries it; it retires when the architect rewords that invariant.
- **Where:** 8 in all, in 5 places: `internal/terms` 2, `internal/terms/inventory` 2, `docs/decisions/invariants/developer-verifies-before-submitting.md` 2, `docs/configuration.md` 1, `docs/developing-yoyo.md` 1.

## The stop-cause words

The run record naming what stopped it (yoyodyne-ifd.409) prints one of ten words
in front of every stopped run's reason, as in `provider: …`. That item
asked for whoever owns their wording to confirm them before anything depends on
them, and the Lead Product Manager put the question here. They are listed and
not counted: that item's change is not on the main branch yet, and most of the
words are ordinary words a count could not tell from their ordinary use. What
was decided is to print the plain phrase in place of the bare word, keeping the
word as the value the record stores.

| Word | Means | Decided |
|---|---|---|
| `checks` | A configured check kept failing or could not run, a protected path kept being touched, or nothing was recorded running. | print instead: stopped at the checks |
| `review` | Findings the repair budget could not resolve, a reviewer that could not answer, or an approval whose independence could not be shown. | print instead: stopped at review |
| `integration` | Merging onto the target branch failed: the target kept moving, the change conflicts, or local and remote went different ways. | print instead: could not be merged |
| `publish` | Publishing the merged change to the forge failed, although the local merge landed. | print instead: could not be published to the forge |
| `cleanup` | Removing the run's worktree and branch after its work merged failed. | print instead: merged, but its worktree could not be removed |
| `recording` | The run's work was done but its completion record could not be written. | print instead: done, but the harness could not record that it finished |
| `provider` | The AI provider ended the run without the work being judged. | print instead: the AI provider ended the run |
| `environmental` | Something outside the work refused the run, which spends none of its budgets. | print instead: stopped by something outside the work |
| `cancelled` | The operator asked the run to stop, or its context was cancelled. | an ordinary word; keep it |
| `harness` | One of the harness's own steps around the work failed: saving state, writing the tracker, making a scratch directory. | print instead: the harness's own step failed |

## Finding terms this list does not have

`go run ./scripts/vocabulary -candidates` lists the hyphenated compounds in
the Go strings and the personas that no term above covers, most frequent first.
Nothing mechanical tells a coinage from an ordinary compound, so the list is for
a person to read: a term found there is added to `internal/terms/inventory`
with its meaning and a proposed decision, the Lead Product Manager decides it,
and the document is run again.

Between readings, the terms check keeps the vocabulary from growing unnoticed:
it refuses a compound word written anywhere a person or a role reads the
harness's words unless the register, this inventory, or the rules for ordinary
English account for it. [Working on yoyo
itself](developing-yoyo.md#what-the-terms-check-counts-as-a-new-term) states
the rule.

## Compounds still to be decided

The terms check began reading for new compound words on 2026-10-06. These are
the ones it found already written that nothing accounted for: no row in the
register, no term above, not ordinary English by the check's rules, and not on
the register's list of ordinary compounds. Some are terms of art and some are
ordinary English the rules do not recognise; nobody has decided yet
which they are. The check allows each by name until somebody does.

Deciding one takes it off the list in `internal/terms/inventory`: an
ordinary compound goes on the register's list of ordinary compounds, a term
worth keeping gets a row in the register, and a term worth replacing is added
above with its meaning and its plain words. A word nothing writes any more is
refused by the check until it is taken off.

- `account-pooling`
- `account-selection`
- `action-count`
- `activation-digest`
- `admission-trigger`
- `advisory-once`
- `agent-context`
- `agent-continuity`
- `agent-memory`
- `allow-list`
- `already-made`
- `already-runnable`
- `already-running`
- `already-spent`
- `amended-since`
- `application-support`
- `approval-forgeability`
- `approved-and-amended-since`
- `architect-amendments`
- `architect-pass`
- `artifact-changing`
- `artifact-home`
- `at-act`
- `attach-detach`
- `authority-model`
- `authority-relevant`
- `authorization-by-capability`
- `back-link`
- `base-revision`
- `bespoke-per-specialist`
- `bounce-when-idle`
- `branch-publication`
- `broken-sandbox`
- `browser-profile`
- `bulk-clear`
- `bundle-improvement`
- `candidate-bound`
- `capability-and-scope`
- `capacity-wait`
- `carried-out`
- `carry-back`
- `changed-file`
- `changed-path`
- `channel-nobody-reads`
- `chat-spawns-subprocesses`
- `check-set`
- `check-stage`
- `check-to-use`
- `checked-in`
- `checked-shape`
- `claim-audit`
- `clean-tree`
- `cli-help`
- `closed-list`
- `closed-reason`
- `closed-status`
- `code-implementation`
- `coding-agent`
- `configuration-file`
- `configuration-guide`
- `configured-check`
- `conflict-avoidance`
- `conflict-handling`
- `content-security`
- `context-reconstruction`
- `context-size`
- `contributor-mode`
- `control-plane`
- `conversation-held`
- `conversation-holding`
- `cumulative-report`
- `cut-replies`
- `decide-and-report`
- `decided-change`
- `deciders-stop`
- `default-deny`
- `delivery-pipeline`
- `dependency-test`
- `description-not-intent`
- `developer-session`
- `developer-slot`
- `developer-written`
- `diagnosis-class`
- `directory-sync`
- `disable-auto-merge`
- `display-identity`
- `diverged-target`
- `done-condition`
- `done-conditions`
- `done-means`
- `dropped-merge`
- `duplicate-admission`
- `duplicate-run`
- `durable-state`
- `effective-configuration`
- `empty-delivery`
- `empty-tool`
- `endpoint-plus-invocation`
- `endpoint-switch`
- `event-recording`
- `evidence-confinement`
- `evidence-not-instruction`
- `execution-evidence`
- `execution-policy`
- `execution-termination`
- `factory-flow`
- `factory-health`
- `factory-problems`
- `failure-storm`
- `fast-forward-or-nothing`
- `file-granularity`
- `final-reply`
- `fixed-roles`
- `fixture-proven`
- `fixture-shape`
- `footprint-and-dependency`
- `force-pushing`
- `force-resolves`
- `forge-hygiene`
- `forge-url`
- `full-budget`
- `full-suite`
- `further-reading`
- `give-back`
- `goal-level-approval`
- `goal-quality`
- `goals-directory`
- `grant-refusal`
- `hand-back`
- `has-a-disposition`
- `hand-closer`
- `handed-over`
- `harness-error`
- `harness-made`
- `harness-run`
- `head-behind-target`
- `held-back`
- `held-work`
- `human-approval`
- `ignore-rule`
- `independent-reviewer`
- `individually-acceptable`
- `initialized-here`
- `integrated-work`
- `integration-resume`
- `integration-stop`
- `interrupted-review`
- `introduction-then-goals`
- `invalid-record`
- `invariants-index`
- `label-model`
- `launch-preparation`
- `legacy-migration`
- `long-held`
- `machine-read`
- `machine-too-busy`
- `management-conversation`
- `management-conversion`
- `management-loop`
- `management-request`
- `management-role`
- `mechanical-low-risk`
- `memory-budget`
- `memory-save`
- `merge-back`
- `merge-rather-than-move`
- `merge-semantics`
- `minimum-interval`
- `missing-report`
- `missing-target`
- `model-dependent`
- `model-provider`
- `model-selection`
- `model-unavailable`
- `model-version`
- `moved-anchor`
- `named-account`
- `native-resume`
- `new-file`
- `new-format`
- `newer-run`
- `no-fallback`
- `no-hosted-control-plane`
- `no-schema-change`
- `no-side-conversations`
- `no-stub`
- `not-startable`
- `nothing-running`
- `once-per-item`
- `once-per-publication`
- `operator-action`
- `operator-activation`
- `operator-attention`
- `partial-handoff`
- `pass-and-query`
- `past-reset`
- `path-string`
- `pinned-candidate`
- `pinned-install`
- `plan-mode`
- `preserved-run`
- `preserved-work`
- `private-identifier`
- `product-document`
- `product-management`
- `product-pass`
- `product-role`
- `product-to-global`
- `project-state`
- `protected-file`
- `protected-home`
- `protected-source`
- `provider-adapter`
- `provider-call`
- `provider-general`
- `provider-hold`
- `provider-outage`
- `provider-plugin`
- `provider-policy`
- `provider-refusal`
- `provider-stop`
- `provider-unavailable`
- `provider-wait`
- `queue-record`
- `queue-worker`
- `queued-head`
- `read-model`
- `readme-split`
- `ready-work`
- `reboot-class`
- `recent-activity`
- `records-store`
- `recoverable-failure`
- `recoverable-versus-terminal`
- `recovery-policy`
- `recurring-task`
- `red-target`
- `registered-assessor`
- `release-and-record`
- `release-readiness`
- `released-claim`
- `remote-boundary`
- `remote-target`
- `repeated-failure`
- `replay-test`
- `reportable-event`
- `repository-setting`
- `resolve-then-write`
- `restart-requests`
- `restart-surviving`
- `retire-raise`
- `review-independence`
- `review-input`
- `review-round`
- `revision-bound`
- `revision-log`
- `role-capability`
- `role-contract`
- `role-definition`
- `role-definitions`
- `role-name`
- `rolled-back`
- `run-activity`
- `run-by-run`
- `run-ending`
- `run-state`
- `run-status`
- `run-stop`
- `runs-on`
- `runtime-internal`
- `runtime-state`
- `safe-write`
- `same-cell`
- `same-path`
- `same-provider`
- `same-run`
- `scheduled-pass`
- `security-coordinator`
- `security-review`
- `shipped-surface`
- `silent-stream`
- `slack-reporting`
- `software-delivery-bound`
- `spend-follows-the-work`
- `spine-touching`
- `split-era`
- `stale-build`
- `state-schema`
- `step-attempt`
- `still-applies`
- `still-moving`
- `stopped-run`
- `stray-identity`
- `structured-output`
- `subject-continuity`
- `symlink-escape`
- `sync-window`
- `target-failure`
- `target-position`
- `team-mode`
- `technical-health`
- `terminal-result`
- `terminal-run`
- `timing-flake`
- `token-efficiency`
- `tool-access`
- `tool-control`
- `traceable-chain`
- `tracker-action`
- `tracker-sync`
- `tree-listing`
- `turn-boundary`
- `turn-size`
- `uncertain-save`
- `unmeetable-item-returns`
- `unowned-entry`
- `unresolved-at-cap`
- `unresolved-escalation`
- `usage-limit`
- `usage-pause`
- `vanished-process`
- `web-security`
- `web-service`
- `whole-content`
- `whole-spine`
- `whole-stage`
- `work-turn`
- `worktree-write`
- `wrap-in-actions`
- `writable-root`
- `write-shell`
