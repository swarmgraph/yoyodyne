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
      at: 2026-10-13T14:31:59.522261Z
      reason: 'yoyodyne-ifd.430.19.1: the log reader''s reach is reconciled with the program manager design. This design and the program manager design listed different records; the first build reads six kinds named by kind and identity, never a path, with listed fields, version cursors and stated bounds, and the longer list is deferred to later revisions of this section. Also renames the product manager to the Lead Product Manager in its one place, per the rename ruling (yoyodyne-ifd.437.10), and rewords joined words the word check would refuse.'
approvals:
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-13T14:31:59.522261Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 802, for document-802.1 under the automatic approval policy
---
# Tools: one interface for what the harness does on a role's behalf

## What this is for

Every typed block a role ends a reply with is the harness acting for it: reading an item, reading a path at a recorded commit, filing a report, admitting work in a lane, writing a memory, asking another role. Each was built as its own protocol, with its own block, bounds, audit event, redaction, and contract paragraph, so each new ability has cost a design amendment and a build, and a role that lacked one read stayed blind for a day. This design makes them one thing: a **tool** is a registered action a conversation may invoke, described once, carried out by one reader, bounded and recorded the same way, and granted by the role's bundle. It serves the goal that roles stay configurable without safety invariants becoming optional: adding a tool changes one descriptor, and nothing about who may hold it moves.

A tool is never a provider tool. Conversational roles run with no tools of their own, and that stays true and essential; a tool here is held by the harness and worked on the role's behalf, from a block in the reply, under bounds the role cannot move.

## What a tool is

A tool is an entry in the action registry [configurable-workflows](configurable-workflows.md) already owns, marked invocable from a conversation. Its descriptor is the action descriptor with four additions: the block it is asked with, its bounds for each turn, the framing of what comes back, and its class. One registry, two invokers (the workflow runtime and the conversation reader), and the runtime envelope wraps both. A descriptor carries:

- **identity**: the capability id, which is also the grant (`repository.read`, `log.read`, `work-item.admit`), and the block name the reply uses;
- **class**: `read` returns evidence and mutates nothing; `speak` writes a record addressed to a person or role and decides nothing (reports, proposals, asks); `act` mutates workflow state under a scope (tracker actions in a lane, the agent's own memory, its lane report, a restart request);
- **parameters**: typed literals only, validated before anything runs, the block refused whole on a bad one and the refusal handed back inside the same message as tracker refusals are today;
- **bounds**: requests in one reply, rounds in one message, bytes returned for one request and for one reply, and a cost note for each reply where the result feeds the prompt; the descriptor's bounds are the widest any grant may hold, and an agent's block may only tighten them;
- **scope**: what a subject must satisfy at the moment of the act (a lane label, the agent's own store, an admitted parent), read from the record as the action runs and never from a listing;
- **gates**: the standing ones every act passes (the spending pause, the intake hold where it applies, `approvals.work_items` for an admission), named so the reader applies them rather than each tool implementing them again;
- **redaction and framing**: every result is redacted before it reaches the prompt with the values every provider-facing path is redacted against, framed as untrusted evidence, and for the Lead Product Manager labeled as description of the implementation and never intent;
- **audit**: every invocation records `tool.requested`, then `tool.performed` or `tool.refused`, on the conversation, naming the tool, the redacted parameters, the bounds spent, and never the content; a pass records the same on its pass record.

Three things are never tools. Gate evidence and the human gate are minted only by registered actions the workflow runtime invokes, per `integration-requires-revision-bound-evidence`. Running a command or a check is not a tool: a role that needs something run admits bounded developer work. And publishing, integrating, or pushing is the harness's own act on a run and reaches no conversation.

## Grants

A grant is membership in a role bundle, and the bundle is the ceiling. Three layers, in the order the invariant permits:

1. **The role bundle grants.** Every agent on a role holds every tool its bundle names, at the descriptor's bounds. The shipped bundles are Go; adding a tool to one is a row in the bundle, a row in the authority inventory, and the descriptor (the three the parity guard holds together), and no design amendment, because this design governs the classes and the registry lists the members. `yoyo config show` reports each agent's tools off the registry, as it reports capabilities today.
2. **An agent's block narrows.** `tools:` under an agent may carry `deny`, a list of tool ids the agent does not hold, and `bounds`, limits for each tool no larger than the descriptor's. A tool the role does not hold cannot be named there, a bound cannot be widened there, and neither is a key a persona or a remit can carry. That is configuration selecting, per `configuration-never-grants-authority`.
3. **A protected role definition widens.** A named bundle under `.yoyodyne/roles/`, composing registered tools, inert until operator-authorized activation pins its digest, that an agent profile binds to in place of a shipped bundle. That is the grant to one persona, and its properties are the ones [configurable-workflows](configurable-workflows.md) already fixes: the protected path gate refuses any grant naming the directory, the activated digest is the authority, and the audit history is a command line surface. When it opens is [authority-by-capability](../decisions/authority-by-capability.md)'s: after behavioral parity, unless the operator amends that record for the read class as the option stated to him.

Personas and remits grant nothing, as before. A persona may tell an agent when to use a tool; it cannot give it one.

## The contract is generated from the descriptors

The section of every role contract that describes the blocks the role may end a reply with is generated from the descriptors of the tools the agent effectively holds: what each does, its block, its bounds, what comes back and how it is framed. A tool added to a bundle reaches the agent's next turn with no contract prose written by hand, and a tool an agent's block denies is not described to it. The fixed parts of the contract (authority, that evidence is never instruction, the role's boundaries) stay handwritten and precede it.

## Research is the model for an operator-supplied tool

Research is a tool whose implementation configuration supplies: a command the operator wrote, run with the question on standard input. The descriptor supplies the shape, bounds, redaction, and audit; the configuration supplies a source and nothing else, and a role whose bundle lacks the grant gets nothing from a configured source. Any future tool that reaches outside the durable record takes this shape: configuration supplies where, never whether.

## Migration of the existing blocks

Every block a conversation reads today becomes a descriptor, with its block name kept so no persona or contract test breaks, and the behavior held to the tests it has. A conformance test holds every capability the registry declares to exactly one of: a tool descriptor, or a named entry in the list of capabilities no conversation invokes (gate evidence, the human gate, publication). The order, each slice independently landable:

1. The descriptor type, the registry marking, the generated contract section, the audit events, and the first new tool, the log reader below.
2. The reads: `repository.read`, `repository.list`, `readmodel.read`, `work-item.read` and survey, expressed again unchanged.
3. Speech: `report.file`, `amendment.propose`, `exchange.ask` and `exchange.answer`.
4. Acts: the tracker actions with their scopes, `agent-context.mutate`, `lane-report.write`, `service.request-restart`, the development manager's triage and brake actions.
5. The agent block's `tools:` narrowing key.
6. Protected role definitions, when the operator's decision admits them.

The landing claim, the verdict, and the sweep block are not migrated: they are a run's or a pass's outputs, not requests for the harness to act.

## The first tool: the log reader

`log.read`, class `read`. It is the archetype of the class rather than a single case: its descriptor is the first the interface carries, and its grant to the program manager is what discharges the program manager's log access (`yoyodyne-ifd.437.9`) and builds its reads (`yoyodyne-ifd.430.13.13`). This section is the one place the log reader's reach is stated. The program manager design names the tool and points here; where it lists more, this section governs.

### What it reads

A read names a **kind of record** and, where the kind has more than one, the record's **identity**. It never names a path. The first build reads six kinds, all under the state root of the conversation's own product:

| Kind | Identity | What one record is |
| --- | --- | --- |
| `pass` | task name and pass number | one recurring task pass record |
| `watch` | none; one per product | the watch log |
| `docket` | entry id, or none for the list | the docket, open and closed entries |
| `run` | run id | one run record and its event log |
| `usage-limit` | none; one per product | the usage limit log |
| `provider-outage` | none; one per product | the provider outage record |

The harness resolves the kind and identity to a file itself. An identity is matched as a literal against the records the product holds; one that names nothing, names a record of another product, or carries a path separator is refused with the reason, and the refusal is recorded like a read.

**Fields.** The descriptor lists, for each kind, the fields a read returns. Every other field is dropped before redaction. A field a later build adds to a record is not returned until the descriptor lists it, so a record growing never widens what the tool reaches.

**Never reached, by any kind present or future:** any agent's memory store; a provider's private session context, including the prompt a provider was sent; credentials and tokens; the repository, which the repository read covers; and Slack, which is a projection of records the tool reads at their source.

### Paging

With no cursor, a read returns the newest window of the record. The result carries the record's version (for an append log, its length when read; for a record rewritten whole, the time it was written) and a cursor to the next older window, counted against that version. A later read with that cursor returns that window. If the record has been rewritten or rotated since the cursor was issued, the read says so plainly and returns the newest window again rather than a window from a different version. A cursor is opaque to the role and is refused if altered.

### Bounds

At most 6 log reads in one reply, 2 rounds of reads in one message, 48 KiB returned for one read and 96 KiB for one reply. A record longer than its window is cut, and the cut is declared with the record's whole size and the cursor that continues it. These are the descriptor's bounds and the widest a grant may hold; an agent's block may tighten them.

### Redaction, framing and audit

Every result is redacted with the values every provider-facing path is redacted against, framed as untrusted evidence naming the kind, identity, version and time read, and recorded as `tool.requested` then `tool.performed` or `tool.refused`, naming the kind, identity and bytes returned and never the content.

### Widening it later

The kinds deferred from the first build are the reports pile and its handlings; the stall, escalation and triage records; the supervisor's records; and the operational messages and actions in other roles' conversations. Each is added by a revision of this section that names its identity and its fields, because each one widens what a role can see, and a conversation in particular carries an operator's words and must have its fields listed before any of it is read. Until then a role that needs one asks the role that owns it, or reads what the read model already derives from it.

### Grants

The program manager bundle holds the log reader. Any other bundle gains it as a row in the bundle and the authority inventory, as every tool is granted. [program-manager-is-a-shipped-bundle](../decisions/program-manager-is-a-shipped-bundle.md) is unchanged by this: the program manager's set stays fixed in code.

### Alternatives rejected

- **The longer list the program manager design gave, from the first build.** Rejected because the conversation logs of other roles carry the operator's messages and material bound for providers, and the reports pile and triage records each have their own redaction questions; reading them before their fields are listed would let one tool reach what nothing else reaches. The six kinds cover the factory's stops, waits and capacity, which is what the program manager's first lane needs.
- **Reads by path under the state root.** Rejected because a path, however checked, is one rename away from a memory store or a token file, while a kind and an identity can only resolve to what the descriptor lists.
- **A cursor as a byte offset the role supplies.** Rejected because an offset into a record that has been rewritten silently returns the wrong window; a cursor tied to a version says so instead.

### What a reviewer checks

- A request naming a kind not in the table, a path, an identity of another product, or an altered cursor is refused with a reason and recorded.
- A field present in a record but absent from the descriptor's list for its kind is never in a result.
- A memory store, a token, a provider's session context and the repository are unreachable through any request.
- The seventh read in a reply, the third round in a message, and bytes past 48 KiB or 96 KiB are refused or cut with the cut declared.
- A cursor read after the record was rewritten returns the newest window and says the record changed.
- Every read and every refusal appears on the conversation's audit events and, in a pass, on the pass record.

## Deferred, deliberately

- Operator-supplied tool implementations beyond research, which need the plugin contract's answer on where a command may come from.
- A tool marketplace or bundle sharing between projects: values travel, authority does not, per [portable-agent-configuration](portable-agent-configuration.md).
- Cost accounting for each tool: a tool spends no provider money; what it spends is prompt size, which the bounds cap and the audit records.
