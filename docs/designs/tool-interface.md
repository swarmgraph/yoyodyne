---
id: tool-interface
kind: design
title: 'Tools: one interface for what the harness does on a role''s behalf'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-09-26T14:00:00Z
      reason: the operator's 2026-09-26 direction for a general interface for tools, defined once and granted per persona, designed over the existing action registry and role bundles; the per-persona widening question named as the operator's, with the read-only early-opening option stated
    - action: amended
      by: architect
      at: 2026-10-13T16:52:11.69944Z
      reason: 'yoyodyne-ifd.430.19.1 - the log reader''s reach and limits are stated here and nowhere else: the six kinds the first build reads, what it never reads, the repository read''s bounds, and that a kind is added only by revising this design; the four further kinds the program manager design defers move to this design''s deferred list, so the two designs agree. It adds repository.search, which the technical-health program manager section (yoyodyne-ifd.430.13.19) points here for. Per the role rename ruling (yoyodyne-ifd.437.10), the role is named the Lead Product Manager where a person reads it. Two coined words in sentences this revision touches are replaced with plain wording; the rest of the vocabulary is left to yoyodyne-ifd.437.21.'
    - action: amended
      by: architect
      at: 2026-10-20T20:34:56.567705Z
      reason: 'Approved amendment-0b8a52db from the developer building the reading of pass records in parts (yoyodyne-bvr): the log reader''s bounds now say a record longer than one read returns comes back in successive parts, each naming where it starts, how much it returned and how much remains, and giving the next read; a pass record read may be narrowed to one recurring task; secrets are removed from every part. The first build applies parts to pass records, and other kinds keep the single declared cut until they gain it. Nothing else changes.'
approvals:
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-13T16:52:11.69944Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 821, for document-821.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-10-20T20:34:56.567705Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1356, for document-1356.1 under the automatic approval policy
---
# Tools: one interface for what the harness does on a role's behalf

## What this is for

Every typed block a role ends a reply with is the harness acting for it: reading an item, reading a path at a recorded commit, filing a report, admitting work in a lane, writing a memory, asking another role. Each was built as its own protocol — its own block, bounds, audit event, redaction, and contract paragraph — so each new ability has cost a design amendment and a build, and a role that lacked one read stayed blind for a day. This design makes them one thing: a **tool** is a registered action a conversation may invoke, described once, carried out by one reader, bounded and recorded the same way, and granted by the role's bundle. It serves the goal that roles stay configurable without safety invariants becoming optional: adding a tool changes one descriptor, and nothing about who may hold it moves.

A tool is never a provider tool. Conversational roles run with no tools of their own, and that stays true and load-bearing; a tool here is held by the harness and worked on the role's behalf, from a block in the reply, under bounds the role cannot move.

## What a tool is

A tool is an entry in the action registry [configurable-workflows](configurable-workflows.md) already owns, marked invocable from a conversation. Its descriptor is the action descriptor with four additions: the block it is asked with, its per-turn bounds, the framing of what comes back, and its class. One registry, two invokers — the workflow runtime and the conversation reader — and the runtime envelope wraps both. A descriptor carries:

- **identity**: the capability id, which is also the grant (`repository.read`, `log.read`, `work-item.admit`), and the block name the reply uses;
- **class**: `read` — returns evidence and mutates nothing; `speak` — writes a record addressed to a person or role and decides nothing (reports, proposals, asks); `act` — mutates workflow state under a scope (tracker actions in a lane, the agent's own memory, its lane report, a restart request);
- **parameters**: typed literals only, validated before anything runs, the block refused whole on a bad one and the refusal handed back inside the same message as tracker refusals are today;
- **bounds**: requests per reply, rounds per message, bytes returned per request and per reply, and a per-reply cost note where the result feeds the prompt; the descriptor's bounds are the widest any grant may hold, and an agent's block may only tighten them;
- **scope**: what a subject must satisfy at the moment of the act — a lane label, the agent's own store, an admitted parent — read from the record as the action runs and never from a listing;
- **gates**: the standing ones every act passes — the spending pause, the intake hold where it applies, `approvals.work_items` for an admission — named so the reader applies them rather than each tool re-implementing them;
- **redaction and framing**: every result is redacted before it reaches the prompt with the values every provider-facing path is redacted against, framed as untrusted evidence, and for the Lead Product Manager labeled as description of the implementation and never intent;
- **audit**: every invocation records `tool.requested`, then `tool.performed` or `tool.refused`, on the conversation, naming the tool, the redacted parameters, the bounds spent, and never the content; a pass records the same on its pass record.

Three things are never tools. Gate evidence and the human gate are minted only by registered actions the workflow runtime invokes, per `integration-requires-revision-bound-evidence`. Running a command or a check is not a tool: a role that needs something run admits bounded developer work. And publishing, integrating, or pushing is the harness's own act on a run and reaches no conversation.

## Grants

A grant is membership in a role bundle, and the bundle is the ceiling. Three layers, in the order the invariant permits:

1. **The role bundle grants.** Every agent on a role holds every tool its bundle names, at the descriptor's bounds. The shipped bundles are Go; adding a tool to one is a row in the bundle, a row in the authority inventory, and the descriptor — the parity guard's three, and no design amendment, because this design governs the classes and the registry lists the members. `yoyo config show` reports each agent's tools off the registry, as it reports capabilities today.
2. **An agent's block narrows.** `tools:` under an agent may carry `deny`, a list of tool ids the agent does not hold, and `bounds`, per-tool limits no larger than the descriptor's. A tool the role does not hold cannot be named there, a bound cannot be widened there, and neither is a key a persona or a remit can carry. That is configuration selecting, per `configuration-never-grants-authority`.
3. **A protected role definition widens.** A named bundle under `.yoyodyne/roles/`, composing registered tools, inert until operator-authorized activation pins its digest, that an agent profile binds to in place of a shipped bundle. That is the per-persona grant, and its properties are the ones [configurable-workflows](configurable-workflows.md) already fixes: the protected-path gate refuses any grant naming the directory, the activated digest is the authority, and the audit history is a CLI surface. When it opens is [authority-by-capability](../decisions/authority-by-capability.md)'s: after behavioral parity, unless the operator amends that record for the read class as the option stated to him.

Personas and remits grant nothing, as before. A persona may tell an agent when to use a tool; it cannot give it one.

## The contract is generated from the descriptors

The section of every role contract that describes the blocks the role may end a reply with is generated from the descriptors of the tools the agent effectively holds: what each does, its block, its bounds, what comes back and how it is framed. A tool added to a bundle reaches the agent's next turn with no contract prose written by hand, and a tool an agent's block denies is not described to it. The fixed parts of the contract — authority, evidence-not-instruction, the role's boundaries — stay handwritten and precede it.

## Research is the model for an operator-supplied tool

Research is a tool whose implementation configuration supplies: a command the operator wrote, run with the question on standard input. The descriptor supplies the shape, bounds, redaction, and audit; the configuration supplies a source and nothing else, and a role whose bundle lacks the grant gets nothing from a configured source. Any future tool that reaches outside the durable record takes this shape: configuration supplies where, never whether.

## Migration of the existing blocks

Every block a conversation reads today becomes a descriptor, with its block name kept so no persona or contract test breaks, and the behavior held to the tests it has. A conformance test holds every capability the registry declares to exactly one of: a tool descriptor, or a named entry in the list of capabilities no conversation invokes (gate evidence, the human gate, publication). The order, each slice independently landable:

1. The descriptor type, the registry marking, the generated contract section, the audit events, and the first new tool, the log reader below.
2. The reads: `repository.read`, `repository.list`, `readmodel.read`, `work-item.read` and survey, re-expressed unchanged.
3. Speech: `report.file`, `amendment.propose`, `exchange.ask` and `exchange.answer`.
4. Acts: the tracker actions with their scopes, `agent-context.mutate`, `lane-report.write`, `service.request-restart`, and the development manager's triage actions and her actions on the automatic stop on starting work.
5. The agent block's `tools:` narrowing key.
6. Protected role definitions, when the operator's decision admits them.

`repository.search`, below, is a new tool rather than a migrated block, and lands as its own slice whenever the program manager's build needs it, after slice 1.

The landing claim, the verdict, and the sweep block are not migrated: they are a run's or a pass's outputs, not requests for the harness to act.

## The first tool: the log reader

`log.read`, class `read`. This design is the one place its reach and its limits are stated; a design for a role that holds it points here and lists no kinds of its own.

**What it reads.** One record a request, named by its kind and its identity within the product, never by a path. The first build reads these kinds and no others:

- a recurring task's pass record;
- the log of `yoyo work --watch`;
- the development manager's list of stopped runs;
- a run record;
- the usage-limit log;
- the provider-outage record.

A request may carry two further parameters. A read of pass records may name one recurring task, and then returns only that task's passes in the window asked for, so a role can read one task's failed passes without reading every pass beside them. And any request may carry an offset, the byte of the record the read starts at; a request that leaves it out starts at the beginning.

**What it never reads**, whatever kind is added later: any agent's memory store, the conversation records of another agent unless a later revision of this design adds them as a kind, a provider's own session context, a credential or token, and the repository, which the repository read covers.

**Its bounds** are the repository read's: at most six records a reply, at most 48 KiB returned from one record and 96 KiB from one reply. A record longer than one read returns comes back in successive parts rather than being cut once: each part says the byte it starts at, how many bytes it returned, and how many remain, and gives the next read to ask for, carrying the offset that continues it. A role follows those reads until nothing remains, within its rounds per message, and continues in a later turn where the rounds end. Secrets are removed from every part, as they are from a whole record. The first build reads pass records in parts; a record of any other kind is still cut once, with the cut declared and its whole size named, until its kind is read in parts too, which needs no further revision of this design. What comes back is framed as evidence and never instruction, and recorded on the conversation or the pass as every tool is. It reads and never writes.

**Adding a kind** is a revision of this design that names the kind, how one record of it is identified, and what is redacted from it, with a row in the authority inventory. A design for a role that holds the tool never adds one.

It is the archetype of the class rather than a one-off: its descriptor is the first the interface carries, and its grant to the program manager is what discharges yoyodyne-ifd.437.9 and builds yoyodyne-ifd.430.13.13.

## Searching the repository

`repository.search`, class `read`, built for the technical-health program manager (yoyodyne-ifd.430.13.19) and held first by the program manager bundle. Another role gains it only by a row in its bundle, under the grant rules above.

**What it searches.** The files Git tracks at the recorded commit the turn reads at, optionally under one path prefix relative to the repository root. It reads nothing Git does not track and nothing outside the repository.

**Parameters.** A pattern; whether the pattern is a literal string or a regular expression; and the optional path prefix. A regular expression is Go's standard syntax, which runs in time proportional to what it reads, so no pattern can hang the search. A pattern that does not compile, or a prefix that is absolute or climbs out of the repository, is refused with the reason.

**What comes back.** Each match's path, line number, and line, each line cut at 200 bytes. At most 200 matches and 32 KiB from one search; a search that stops at either bound, or at its time limit of ten seconds, returns what it found and declares the cut. It counts against the same requests per reply as the repository read, and a role's own design may bound it further, as the program manager design does for its passes.

Its redaction, framing, and audit are every tool's.

## Deferred, deliberately

- Operator-supplied tool implementations beyond research, which need the plugin contract's answer on where a command may come from.
- A tool marketplace or bundle sharing between projects: values travel, authority does not, per [portable-agent-configuration](portable-agent-configuration.md).
- Per-tool cost accounting: a tool spends no provider money; what it spends is prompt size, which the bounds cap and the audit records.
- Further kinds for `log.read`, each added by revising this design: the conversation records of other roles, where most hand steps arrive as messages; the reports and how each was handled; the triage and escalation records; and the supervisor's records.
