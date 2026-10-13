---
id: program-manager
kind: design
title: 'The program manager: one role type, one lane per instance, requests batched to the Lead Product Manager'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-09-25T04:00:00Z
      reason: yoyodyne-ifd.430.6 - the program manager designed on the operator's 2026-09-24 decisions; the ten open questions ruled, the lane enforced in the conversation's authority table, the lane report and the blocked, stale, and working statuses specified as read-model derivations, and the boundary with the development manager's hourly sweep written
    - action: amended
      by: architect
      at: 2026-09-26T19:54:03Z
      reason: yoyodyne-ifd.437.7 - 'tool posture' removed; the sentence says what the instance can reach in plain words (wording from approved amendment c41c0b30)
    - action: amended
      by: architect
      at: 2026-09-26T19:54:03Z
      reason: tool-interface companion - the program manager's capabilities are tools under the tool interface; the set stays fixed in code
    - action: amended
      by: architect
      at: 2026-09-26T07:00:00Z
      reason: yoyodyne-ifd.437.5 - the admissions trigger's cursor reads the tracker's item export by creation time, since the interactions log records no creations; the three cursor streams are named per trigger class; lands approved amendment 898ae03f from yoyodyne-ifd.430.13.4
    - action: amended
      by: architect
      at: 2026-09-27T09:00:00Z
      reason: yoyodyne-ifd.437.9 - the no-other-source sentence gives way to log.read, a read-only bounded redacted read of named records under the state root, the first tool under the tool interface; fixed in code like the rest of the bundle
    - action: amended
      by: architect
      at: 2026-09-27T07:20:39Z
      reason: yoyodyne-ifd.432.17 companion - the lane report may annotate derived factory problems by citation; derivation decides and judgment annotates
    - action: amended
      by: architect
      at: 2026-10-13T14:33:29.610198Z
      reason: yoyodyne-ifd.430.19.1 - the log reader's reach and limits are stated once, in tool-interface; this design points there and its longer list of records moves to that design's deferred list, so the two designs no longer disagree. With it, per the role rename ruling (yoyodyne-ifd.437.10), the role is named the Lead Product Manager where a person reads it, title included; identifiers and the mover vocabulary keep product-manager. Nothing else changes.
approvals:
    - policy: approvals.designs
      revision: 6
      by: harness
      at: 2026-10-13T14:33:29.610198Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 804, for document-804.1 under the automatic approval policy
---
# The program manager: one role type, one lane per instance, requests batched to the Lead Product Manager

## What this is for

The harness has five roles, each defined by what it owns: intent, design, decomposition, the change, the verdict. Nothing owns an outcome that cuts across all of them — the factory not stalling, tokens not being wasted, the writing staying clear — and each of those has so far been the operator's own attention, exercised by noticing. A program manager is a role instance that owns one such outcome, watches it on a schedule, admits the work it needs inside a bounded lane, and asks the Lead Product Manager for everything outside it. It serves the goal that configurable roles collaborate without redefining upstream intent: an instance is configured, its lane is a label, and its authority is fixed in code and narrower than the Lead Product Manager's on every axis.

The operator decided the shape on 2026-09-24 and those decisions are restated here as binding rather than re-argued: one role type with capabilities fixed in code; instances defined in configuration, each with a name, a remit as prose, its own triggers, and its own memory store; it writes no code; each instance has exactly one tracker label as its lane and admits work only under it, at any priority, with no cap; everything outside the lane goes to the Lead Product Manager as one batched asynchronous digest per pass through the report channel; it reviews new admissions after they land and never gates one; it keeps a brief lane report, rewritten each pass, pulled and never pushed; the dashboard lists each instance with its status; and the development manager's hourly sweep stays, with the boundary written below. The name is written in full wherever a person reads it, and `pgm` is the identifier form only, per the terms register.

## The role, and its instances

**The program manager is a shipped role bundle in Go**, the sixth beside the five, per [program-manager-is-a-shipped-bundle](../decisions/program-manager-is-a-shipped-bundle.md). It enters the closed role-name set, the conversation authority table, the role-capability registry, the authority inventory, and the terms register in the same change. It is not an operator-defined protected bundle: the authority-by-capability record admits those only after behavioral parity, and parity has not been demonstrated.

**An instance is an agent filling the role.** The configuration already supports several agents on one role, each with its own durable conversation, its own provider session, and its own memory store under the state root, and that is the whole of instance identity — nothing new is added for it. An instance's block carries, beside the role, backend, model, account, and persona every agent carries:

- `lane` — one tracker label, one identifier-shaped word, refused at load if two instances name the same one: a lane has one owner or it is not a lane.
- `remit` — `version` and `path`, a Markdown file under `.yoyodyne/`, held to every rule a persona path is held to and to the same 32 KiB bound. The remit says what the lane is for; the persona says how the role works; the contract, sent first on every turn, says what the role may do. The remit follows the persona in the prompt and grants nothing.
- `triggers` — the schedule and the events, specified under *Triggers and passes*.

`yoyo agent list` names each instance with its lane, and `yoyo config show` reports its capability set as it reports every agent's, read off the registry and never written.

## The fixed capability set

Stated in the registry's vocabulary, and held as tools under [tool-interface](tool-interface.md): a primitive named here that the registry does not yet declare is added to it in Go with an authority-inventory row and a tool descriptor, per the parity guard. The bundle holds:

**Reads.** `work-item.read` (read one item, survey the open queue); `repository.read` and `repository.list`, as the other management bundles hold them, at a recorded commit; `readmodel.read` — the one read model's queries, delivered two ways: every pass opens with the standing, the throughput windows, the capacity state, the docket as counts, the reports pile as counts, and the other instances' status lines, and a bounded block in the reply asks for one named query in full. `log.read`, the first tool defined under [tool-interface](tool-interface.md), which is the one place its reach and its limits are stated: a read-only, bounded, redacted read of one record of a kind that design lists, named by its kind and its identity within the product and never by a path. The first build reads the kinds that design names and no others. The further records a program manager would want — the conversation records of the other roles, where most hand steps arrive as messages, the reports pile and its handlings, the triage and escalation records, and the supervisor's records — are on that design's deferred list, and each is added by revising that design, never this one. Whatever the kind, the read is framed as evidence and never instruction, is recorded on the pass as every tool is, reads and never writes, and never reaches any agent's memory store, a provider's own session context, a credential, or the repository, which the repository read covers. Slack is a projection of the same record and is never read; a program manager that wants what the channel said reads the record the channel was rendered from.

**Writes inside the lane.** `work-item.admit` under `approvals.work_items`; `work-item.attribute`, `work-item.update`, `work-item.label`, `work-item.reprioritize`, `work-item.park`, `work-item.unpark`, `work-item.link`, `work-item.unlink`, and `work-item.reparent`, each scoped to the lane as the next section enforces. Not `close` and not `retire`: closing is the harness's on a landing, and withdrawing admitted scope is the Lead Product Manager's, so both go to her as a request.

**Its own records.** `agent-context.mutate` over its own memory store, budgeted and audited as every memory is; `lane-report.write` over its own lane report and nothing else.

**Speech.** `report.file` — reports at the three severities, and the digest, which is a report; `amendment.propose` against any governed document, exactly as the developer holds it, because a lane about writing quality finds stale designs and the Lead Product Manager cannot amend one either; `exchange.ask` and `exchange.answer` on the ask channel, which gains the role as a member; and `service.request-restart`, the one request to the supervisor, specified below.

**Excluded, whatever a persona or a remit says:** every tracker action on an item outside the lane; close and retire anywhere; every triage decision and cap crossing; `run.cause`, `run.repair-continue`, `publication.merge-repeat`; directives, resolutions, and withdrawals; artifact writes; gate evidence of any kind; the human gate; any repository write; any command. It writes no code, and it may use no tools, as the other management roles may not: what it reads, the harness reads for it.

## The lane, and how it is enforced

The lane is enforced where every conversation authority is enforced, in Go at the authority table, and it is a scope on the tracker actions rather than a new mechanism:

- **A creation carries the lane label in the same write that admits it.** The harness adds the label whether or not the reply named it, so an admission never exists unlabelled. Other labels may be added beside it.
- **Every other tracker action is refused unless the item carries the lane label at the moment of the act**, read from the tracker as the action runs — the same at-act reading the Lead Product Manager's repair makes. A listing that has moved does not widen the lane.
- **The lane label is never removed by its owner.** A `label remove` naming the lane label is refused; taking an item out of a lane is the Lead Product Manager's or the development manager's act.
- **Links.** A lane item may be linked to wait on any item, in or out of the lane, because waiting on work is a fact about the lane item and mutates nothing outside it. The reverse link, which would make an outside item wait on the lane, is refused.
- **Reparent** requires both the item and the new parent to carry the lane label.
- **Priority is unbounded inside the lane**, as the operator decided, and a lane admission that names a priority is placed at it.

**A lane admission is an admission**, so it passes through `approvals.work_items` exactly as the Lead Product Manager's own does: under `human` the instance proposes rather than admits, and the proposal is put to the operator with the lane named; under `automatic` it admits against a goal the operator approved, with the same attribution, the same duplicate-admission guard, and the same refusal of a done-condition naming a document no run may write. A role cannot hold a wider door into the backlog than the role that owns the backlog; `configuration-never-grants-authority` rules the alternative out. Each admission's notes record the instance and the lane, and the digest lists every admission the pass made, which is how the Lead Product Manager sees them after the fact.

## What leaves the lane

**One digest per instance per pass**, and nothing per finding. The digest is a report — filed through `report.file`, carried into the Lead Product Manager's conversation in the slices she already works, decided with her `handle` action — at `note` severity, or `warning` where it carries an objection to an admission or a blocker of the instance's own. Its shape is fixed so a slice of them reads alike: the lane and the pass; the admissions the pass made in the lane; each request outside the lane as one entry with the work it recommends, the goal it would serve, the recommended priority, and why; each objection to a new admission as one entry naming the item and the concern; and the instance's open requests from earlier passes, restated by id rather than repeated. A pass with nothing to say files no digest.

**Requests to the development manager and the architect** are asks on the exchange channel — judgment-only, decisionless, durable, round-capped — because what a program manager wants from either is a judgment, never an act, and the acts it might want (a triage decision, a ruling) land through those roles' own paths.

**Review of new admissions is after the fact and never a gate.** An event-triggered pass sees the items admitted since its last pass; an instance whose remit reaches one of them may object in the digest, and may park it only where the item is in its own lane. Nothing an instance says holds an admission out of the queue.

## Triggers and passes

A pass is a recurring-task firing, and every rule the recurring-task design states holds unchanged: the intake hold does not stop it, the spending pause does, budgets are committed before spend, it skips when a turn is in flight on the instance's conversation, at most one task fires per scheduler pull, and every pass ends in a durable record `yoyo sweeps` reads. An instance's `triggers` block names its schedule as `every` and its events as `on`, from a closed set: `landings` (a run integrated or a merge confirmed), `admissions` (items created), and `stoppages` (a docket entry opened). A trigger class decides when the instance gets an opportunity to judge, never what it concludes.

**A burst wakes an instance once.** Each instance keeps a durable cursor per event stream, one stream per trigger class: the run records for `landings`, the tracker's item export (`.beads/issues.jsonl`) read by each item's recorded creation time for `admissions`, and the docket records for `stoppages`. The tracker's interactions log is not among them: Beads writes an entry there for a status, priority, or assignee change and none for an item's creation, so an admissions trigger reading it would never fire. The export is a dump the tracker refreshes rather than its live store, so an admission reaches the trigger when the export next carries it; the schedule's `every` bounds how late that can be. An event past the cursor arms one wake; the wake is taken at the next pull only once the stream has been quiet for a settle window of two minutes, or the schedule is due, whichever comes first; and the pass is handed everything between the cursor and the moment it was taken, after which the cursor moves. A Lead Product Manager admitting thirty items in one turn produces one pass carrying thirty admissions. A pass that fails leaves the cursor where it was, so the next pass carries the same events rather than losing them, and the failure is on the pass record as it is for any recurring task.

**Models.** A pass asks for the task's `model` where the trigger block names one and the agent's `model` otherwise; an operator's turn in the instance's conversation asks for the agent's. That is the mechanism yoyodyne-ifd.420 shipped and it adds no key.

**Restarts.** `service.request-restart` writes one durable request naming a part the `services` section declares. The supervisor's periodic pass is the only executor: it treats a requested restart as it treats a death, under its own backoff and its five-in-two-minutes bound, refuses one while the provider-outage record stands, and records what it did on the request. At most one open request per part per instance; a second is refused while the first is unanswered. Until yoyodyne-ifd.413 lands, a request is recorded, shown on the instance's report and in the standing, and acted on by nothing, and the pass is told so in the result. No program manager restarts anything itself, kills anything, or invokes anything: noticing surfaces do not restart processes, and the harness is the only invoker.

## The lane report

Each instance owns one report: an executive summary of the lane's progress, what remains, and its blockers. It is **rewritten whole on every pass** by one `yoyodyne-lane-report` block in the reply, carrying `summary`, `remaining` as a list, and `blockers` as a list of entries each naming `what`, `waiting_on` (a mover from the read model's own vocabulary: operator, product-manager, development-manager, architect, harness, forge, provider), and `cites` (the id of the request, report, amendment, or exchange the instance already raised about it). A report may also carry `problems`, a list of entries each naming the id of a factory problem the read model derives, per [observability-and-dashboard](observability-and-dashboard.md), with the instance's `remedy`, its `cause` as it reads it, and the `fixes` item it admitted in its lane; the read model joins each onto the problem it cites, and an entry citing no derived problem is shown as a claim, as an unresolved blocker is. Nothing an instance writes creates, removes, or reclassifies a problem. The whole report is bounded at 16 KiB, redacted before it is written, and refused whole where it is malformed, with the previous report left standing and the refusal on the pass record.

It lives under the state root at `products/<product>/program-managers/<agent>/report.json`, beside a bounded history of the last fifty versions, each stamped with the pass and the conversation turn that wrote it. The history is kept for reading back and is not the report. It is never in the repository, which is public, and never in the memory store, which is append-only judgment rather than a surface. It is a distinct record from the pass record: the pass record says what a firing did, the report says where the lane stands.

## Status: blocked, stale, working

Status is a **read-model derivation**, computed once server-side and projected by the dashboard, `yoyo status --json` under `standing.program_managers`, and the channel's hourly line where it already posts, per `surfaces-project-one-read-model`. Nothing a program manager writes sets its own status.

- **Blocked** — the latest report names at least one blocker whose `cites` the read model resolves to a record of the instance's own that is still open: a report the Lead Product Manager has not handled, an amendment undecided, an exchange unresolved, a restart request unanswered. A blocker whose citation resolves to nothing, to another instance's record, or to a record already decided is not a blocker; it is shown on the card as a claim with the reason, and the instance is not blocked by it. This is what makes the flag mean the same thing for every instance: blocked is an open ask of the instance's own, addressed to a named mover, that the record can find.
- **Stale** — no pass has completed within twice the instance's schedule, measured from the last completed pass record by plain Go with no provider call on the path, per the liveness constraint in [management-and-supervision](management-and-supervision.md). A pass the provider refused, a turn the size backstop rejected, and a scheduler that died all look the same here, and that is the point: it is the out-of-band signal that the watcher is not watching, visible without anybody being pushed. An instance with no completed pass at all is stale from twice its schedule after activation.
- **Working** — otherwise.

Stale outranks blocked in the one word shown, and the card shows both. Neither is a direct message: the operator reviews when he chooses. A stale instance is counted on the channel's hourly line only where that line already posts for another reason, and the supervisor's pass, once 413 lands, reads staleness beside the scheduler's health and restarts the scheduler if the scheduler is what died — never the instance, which is a conversation and not a process.

## Instances see each other

Every pass opens with a line per other instance: name, lane, status, and its open requests by id. Any lane report is readable in full through `readmodel.read`. An instance never writes another's report, store, or lane; work it believes belongs in another lane goes to the Lead Product Manager in the digest, named for that lane, and a judgment it wants from another instance is an ask, round-capped and visible like every ask.

## The boundary with the development manager's hourly sweep

The sweep (yoyodyne-ifd.283) is the standing eye over the machinery, and it keeps everything it has: the docket and every triage decision, dead claims, stuck and dropped publications, forge hygiene, cap crossings, and root-cause work filed for each fix. A program manager whose remit is the factory takes none of that ground:

| The development manager's sweep | The program manager's pass |
| --- | --- |
| a stoppage, a docket entry, a claim, a publication — anything the read model already says is waiting on a named mover | what nobody is assigned: the pattern across passes — the same cause stopping runs twice, throughput falling with no hold to explain it, fixes landing without root-cause work behind them |
| decides, crosses, repairs, re-runs, re-arms | admits root-cause and prevention work in its lane, objects in the digest, asks the development manager for a judgment |
| files root-cause work for each fix it makes | checks that filing happened and names it in the report where it did not |

Two rules hold the line. A program manager never records or requests a triage decision about a docket entry, whose next mover is already named. And before it admits an item it checks the sweep's own filings, through the duplicate-admission guard and by reading the sweep records, so one stoppage never produces two items. Where the two still meet on one fact, the sweep acts and the program manager reports, which is the division the operator drew for forge hygiene and is the division here.

## The dashboard section

A section listing the active program managers: each row its name in full, its lane, its status as a word and a badge, and its report as a link that opens the report on a card — summary, remaining, blockers with each blocker's mover and citation, the claims that did not resolve, when the report was written, and the open restart requests. It is served from `standing.program_managers` and from `/api/program-managers/<agent>` for the report, both read-model queries, so the page, `yoyo status`, and the channel cannot disagree about an instance. The section is admitted under yoyodyne-ifd.432 as yoyodyne-ifd.432.11 and builds to [observability-and-dashboard](observability-and-dashboard.md), which this design revises to name the query.

## Deferred, deliberately

- Conversion of the pass into a workflow definition, which arrives with the management-request conversion under configurable-workflows and changes nothing here.
- Two instances sharing a lane, or a lane defined by anything but one label.
- A program manager reaching the forge, the network, or research sources: its evidence is the durable record and the repository at a recorded commit.
- A push of any status change: the operator asked for a pulled report and gets one.
- The supervisor acting on an instance's staleness beyond restarting a dead scheduler; what else it might do waits for the first stale instance to teach it.
- Any record kind `log.read` does not yet read: the list, and how a kind is added, are in [tool-interface](tool-interface.md).

## The ten questions, ruled

1. The capability set is the bundle above; the read surface is the tracker, the repository at a recorded commit, the read model, the other instances' reports, and the records `log.read` reaches as [tool-interface](tool-interface.md) states them, and never Slack.
2. The lane is a scope on the tracker actions in the conversation's authority table: the label applied on every creation, every other action refused unless the item already carries it, the label never removed by its owner, and admission under `approvals.work_items` as the Lead Product Manager's is.
3. A check run is not a capability; the instance admits a diagnosis-class item in its lane.
4. A restart is one bounded durable request the supervisor's pass executes; recorded and inert until yoyodyne-ifd.413 lands.
5. Event triggers collapse through a per-instance cursor and a two-minute settle window into one pass carrying everything since the last.
6. The backstop is the stale status, derived by non-model machinery and served by the read model; the supervisor reads it once it exists.
7. Instances see each other's status, open requests, and reports, mutate nothing of each other's, and may ask each other.
8. A pass runs on the task's model and an operator's turn on the agent's, as yoyodyne-ifd.420 shipped; no new key.
9. The lane report is a rewritten bounded document under the state root with a bounded history, written by one typed action, never in the repository or the memory store.
10. Blocked is an open, citable request of the instance's own to a named mover that the read model finds undecided; stale is no completed pass within twice the schedule; working is neither.
